//go:build faultline_test && linux

package difftest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/demoartifactstest"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/clock"
)

// Fault rows for the demo-v2 bundle writer (Go Slice 5b.1, Task 3) and, from
// Task 5, the preview-write row of the real `judge-preview` CLI: the actual
// pinned TS CLI against the actual tagged fl binary, run by the tagged driver.
// The canonical-shim contract (the fixed invoke-only TS shim against the
// unchanged Task 1 adapter) is a subtest of TestLiveDemoArtifactFaults, under
// the same eligible preflight, so no separate oracle-skipping top-level test
// can be orphaned from the explicitly named faults job.
//
// Each row prepares a fresh case root outside tracing, then runs the same
// request on the frozen TS writer and on the tagged Go driver under
// `strace -f`, failing real syscalls on exact stage, backup and output paths.
// Stage and backup names come from the existing seed stream (the first two
// UUIDs: stage, then backup).
//
// For every row and side the intended operation is discovered in that side's
// own trace (a no-fault run, or for cleanup rows a run with the earlier fault
// of the same row), its ordinal is computed from that trace under the same
// -P filter the fault run uses, and the fault run's (INJECTED) records are
// validated against the intended syscall group, errno, path and destination
// with an exact count. strace counts when= per thread, so a trace in which
// the intended family spans threads is rejected; the Go driver pins its
// invoking goroutine to one OS thread (runtime.LockOSThread in the tagged
// driver only) so that synchronous library calls stay on one thread. The TS
// writer is single-threaded for these synchronous calls.

const (
	demoSeed     = "demo-artifacts-v1"
	demoReplayRt = "sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa"
	demoRerunRt  = "sha256:408bc1c3ebdf71c785b6192b34f465d4e00ec481c91a2283f62c7517ebcb0a37"
	demoOutRel   = ".faultline/bundles/demo"
)

// demoTSInvoke is the frozen-TS side of the invoke-only path. The Task 1
// oracle adapter performs setup and has no invoke-only mode, and it is
// captured evidence that this task does not edit, so this fixed private shim
// runs exactly one real frozen call after Prepare did the setup. It accepts no
// command or script and only the request semantics it actually implements:
//   - kind writer: base replay/rerun, setup none or existing-bundle (performed
//     by Prepare, not here), default output, no proofRoot or mutation: one
//     real writeProofBundle call, printed as {call};
//   - kind cli: setup none or existing-preview (performed by Prepare), argv
//     strings passed verbatim to the real `dist/cli.js judge-preview` child
//     under the seed preload, printed as {process}.
//
// Anything else exits 2. The contract subtest of TestLiveDemoArtifactFaults
// compares it with the canonical adapter for every shape it supports.
const demoTSInvoke = `import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { pathToFileURL } from "node:url";
const [, , requestPath, repo] = process.argv;
const request = JSON.parse(readFileSync(requestPath, "utf8"));
const reject = (why) => { process.stderr.write("demo shim: unsupported request: " + why + "\n"); process.exit(2); };
const allowed = { writer: ["id", "kind", "base", "setup"], cli: ["id", "kind", "setup", "output", "argv"] };
if (!(request.kind in allowed)) reject("kind " + request.kind);
for (const key of Object.keys(request)) if (!allowed[request.kind].includes(key)) reject("field " + key);
if (request.kind === "cli") {
  if (![undefined, "existing-preview"].includes(request.setup)) reject("setup " + request.setup);
  if (request.argv !== undefined && !(Array.isArray(request.argv) && request.argv.every((a) => typeof a === "string"))) reject("argv");
  const child = spawnSync(process.execPath, ["--import", repo + "/difftest/gen/seed.mjs", repo + "/dist/cli.js", "judge-preview", ...(request.argv ?? [])], {
    cwd: process.cwd(), encoding: "buffer", timeout: 60000, maxBuffer: 64 * 1024 * 1024, env: process.env,
  });
  if (child.error) throw child.error;
  if (child.status === null) throw new Error("CLI child has no exit status (signal " + child.signal + ")");
  process.stdout.write(JSON.stringify({ process: { stdout: child.stdout.toString("utf8"), stderr: child.stderr.toString("utf8"), exitCode: child.status } }));
  process.exit(0);
}
if (![undefined, "replay", "rerun"].includes(request.base)) reject("base " + request.base);
if (![undefined, "existing-bundle"].includes(request.setup)) reject("setup " + request.setup);
const load = (m) => import(pathToFileURL(repo + "/dist/" + m + ".js").href);
const { createDemoAnalysis } = await load("engine");
const { writeProofBundle } = await load("proof-bundle");
const analysis = request.base === "rerun"
  ? JSON.parse(readFileSync(repo + "/difftest/testdata/bases/demo-rerun/analysis.json", "utf8"))
  : createDemoAnalysis("REPLAY");
let call;
try {
  call = { valueJSON: JSON.stringify(writeProofBundle(".faultline/bundles/demo", analysis, {})), error: null };
} catch (error) {
  call = { valueJSON: null, error: error instanceof Error ? error.message : String(error) };
}
process.stdout.write(JSON.stringify({ call }));
`

// demoInjection fails calls of one syscall group on exact paths. when is
// strace's when= value ("N", or "N+" for the Nth and every later call).
type demoInjection struct {
	group, errno string
	paths        []string
	when         string
}

// demoPaths are the case-root paths a row faults: the bundle output, its stage
// and backup siblings, and the requested preview HTML.
type demoPaths struct{ out, stage, backup, html string }

func demoRoot(base string) string {
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		panic(err)
	}
	return filepath.Join(resolved, "root")
}

func (e demoEnv) paths(root string) demoPaths {
	out := filepath.Join(root, filepath.FromSlash(demoOutRel))
	return demoPaths{out, out + ".staging-" + e.stage, out + ".backup-" + e.backup, filepath.Join(root, demoPreviewOut)}
}

// demoPreviewOut is the preview row's requested output, relative to the case root.
const demoPreviewOut = "out.html"

// demoRun is one traced invocation of a fresh case. A library request fills
// call; a CLI request fills process (the real child's complete streams).
type demoRun struct {
	root    string
	call    demoartifactstest.Call
	process *demoartifactstest.Process
	tree    []demoartifactstest.Entry
	trace   string
	calls   []straceCall
}

type demoEnv struct {
	repo, driver, script string
	cli                  string // the actual tagged fl binary (CLI requests)
	stage, backup        string // UUID suffixes from the seed stream
}

type demoSide struct {
	name string
	cmd  func(e demoEnv, root, reqFile string) []string
	env  []string
}

var demoSides = []demoSide{
	{"TS", func(e demoEnv, root, reqFile string) []string {
		gen := filepath.Join(e.repo, "difftest", "gen")
		return []string{"node", "--import", "file://" + filepath.Join(gen, "seed.mjs"), e.script, reqFile, e.repo}
	}, nil},
	{"Go", func(e demoEnv, root, reqFile string) []string {
		args := []string{e.driver, "--root", root, "--repo", e.repo, "--request", reqFile, "--cli", e.cli}
		// A CLI request is faulted on a file the driver's own tree inventory
		// would also read inside the traced process (foreign open and close
		// records of the same path). The tree is inventoried outside strace.
		if cliRequest(reqFile) {
			args = append(args, "--no-inventory")
		}
		return args
	}, []string{"GOMAXPROCS=1"}},
}

// cliRequest reports whether the request file holds a CLI request.
func cliRequest(reqFile string) bool {
	b, err := os.ReadFile(reqFile)
	if err != nil {
		panic(err)
	}
	var r demoartifactstest.Request
	if err := json.Unmarshal(b, &r); err != nil {
		panic(err)
	}
	return r.Kind == "cli"
}

func demoDecode(e *demoartifactstest.Entry) string {
	if e == nil || e.Base64 == nil {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(*e.Base64)
	if err != nil {
		return ""
	}
	return string(b)
}

func demoFind(tree []demoartifactstest.Entry, rel string) *demoartifactstest.Entry {
	for i := range tree {
		if tree[i].Path == rel {
			return &tree[i]
		}
	}
	return nil
}

func demoHasPrefix(tree []demoartifactstest.Entry, rel string) bool {
	for _, e := range tree {
		if e.Path == rel || strings.HasPrefix(e.Path, rel+"/") {
			return true
		}
	}
	return false
}

// demoSiblings lists the tree entries directly under the managed root whose
// name starts with prefix.
func demoSiblings(tree []demoartifactstest.Entry, prefix string) []string {
	var out []string
	for _, e := range tree {
		if rest, ok := strings.CutPrefix(e.Path, ".faultline/bundles/"); ok && !strings.Contains(rest, "/") && strings.HasPrefix(rest, prefix) {
			out = append(out, rest)
		}
	}
	return out
}

// demoStraceProbe logs the installed strace version (the command must succeed;
// the version is not pinned here) and runs a real strace with the same
// -D -f -q options the rows use on /bin/true, requiring the printed trace to end
// with that tid's positive normal exit notice. It shows the option and exit
// grammar the parser relies on; it does not reproduce an unavailable entry.
func demoStraceProbe(t *testing.T) {
	t.Helper()
	version, err := exec.Command("strace", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("strace --version failed: %v\n%s", err, version)
	}
	t.Logf("strace --version:\n%s", version)
	trace := filepath.Join(t.TempDir(), "probe-trace")
	if out, err := exec.Command("strace", "-D", "-f", "-q", "-e", "trace=execve", "-o", trace, "--", "/bin/true").CombinedOutput(); err != nil {
		t.Fatalf("strace probe of /bin/true failed: %v\n%s", err, out)
	}
	body, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("strace -D -f -q probe of /bin/true:\n%s", body)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	last := lines[len(lines)-1]
	if !straceExit.MatchString(last) || !strings.HasSuffix(strings.TrimSpace(last), "+++ exited with 0 +++") {
		t.Fatalf("the probe trace does not end with a normal exit notice: %q", last)
	}
	if _, err := parseStrace(string(body)); err != nil {
		t.Fatalf("the probe trace is not accepted by the row parser: %v", err)
	}
}

func demoPreflight(t *testing.T, repo string) {
	t.Helper()
	out, err := exec.Command("node", "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "v22.22.2" {
		t.Fatalf("demo fault gate needs the pinned Node v22.22.2: %q %v", out, err)
	}
	manifest, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(manifest, &pkg); err != nil || pkg.Version != "0.1.4" {
		t.Fatalf("manifest version %q (%v), want 0.1.4", pkg.Version, err)
	}
	for _, m := range []string{"cli", "engine", "proof-bundle"} {
		if _, err := os.Stat(filepath.Join(repo, "dist", m+".js")); err != nil {
			t.Fatalf("dist is not built: %v", err)
		}
	}
	version, err := exec.Command("node", filepath.Join(repo, "dist", "cli.js"), "--version").Output()
	if err != nil || string(version) != "FaultLine 0.1.4\n" {
		t.Fatalf("pinned TS CLI version drift: %q %v", version, err)
	}
}

func demoBuildDriver(t *testing.T, repo string) string {
	t.Helper()
	// A Go-less container runs a prebuilt tagged driver, as it runs a prebuilt
	// fl through FAULTLINE_FL_BINARY. CI builds it.
	if prebuilt := os.Getenv("FAULTLINE_DEMO_DRIVER"); prebuilt != "" {
		return prebuilt
	}
	dir := os.Getenv("SLICE_WORKSPACE")
	if dir == "" {
		dir = t.TempDir()
	}
	out := filepath.Join(dir, "demo-driver")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-tags", "faultline_test", "-o", out, "./difftest/demoartifactsdriver")
	cmd.Dir = repo
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the demo driver: %v\n%s", err, b)
	}
	return out
}

// demoSeededNames are the first two UUIDs of the seed stream: the stage
// suffix, then the backup suffix (the backup is allocated even with no old
// output). The global clock is restored to its unseeded state afterwards.
func demoSeededNames(t *testing.T) (stage, backup string) {
	t.Helper()
	clock.ResetForTest(demoSeed)
	t.Cleanup(func() { clock.ResetForTest("") })
	return clock.NewUUID(), clock.NewUUID()
}

func demoRequest(existing bool, id string) demoartifactstest.Request {
	req := demoartifactstest.Request{ID: id, Kind: "writer"}
	if existing {
		req.Base, req.Setup = "rerun", "existing-bundle"
	}
	return req
}

// demoPreviewRequest is `fl judge-preview --output out.html`, optionally over
// an existing regular file (overwritten in place, not atomically replaced).
func demoPreviewRequest(existing bool, id string) demoartifactstest.Request {
	output, argv := demoPreviewOut, []string{"--output", demoPreviewOut}
	req := demoartifactstest.Request{ID: id, Kind: "cli", Output: &output, Argv: &argv}
	if existing {
		req.Setup = "existing-preview"
	}
	return req
}

// runDemo prepares a fresh case (outside tracing) and runs it on one side
// under strace. groups are the traced syscall groups in addition to the
// injected ones; filter is the -P path set (the injections' own paths are
// added). A trace that cannot be read or parsed fails the test.
func runDemo(t *testing.T, e demoEnv, side demoSide, req demoartifactstest.Request, base string, injections []demoInjection, groups []string, filter []string) demoRun {
	t.Helper()
	root := demoRoot(base)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := demoartifactstest.Prepare(root, e.repo, req); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	reqFile := filepath.Join(base, "request.json")
	if err := os.WriteFile(reqFile, body, 0o644); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(base, "trace")
	os.Remove(trace)

	var calls []string
	seen := map[string]bool{}
	add := func(group string) {
		if !seen[group] {
			seen[group] = true
			calls = append(calls, faultCalls[group])
		}
	}
	for _, g := range groups {
		add(g)
		// fd-based calls take their path from the open that produced the fd.
		if g == "write" || g == "close" {
			add("open")
		}
	}
	// -q (not -qq) keeps the per-tid normal exit notices the parser uses as
	// positive terminal evidence.
	args := []string{"-D", "-f", "-q", "-s", "4096", "-o", trace}
	pset := map[string]bool{}
	addP := func(p string) {
		if !pset[p] {
			pset[p] = true
			args = append(args, "-P", p)
		}
	}
	var injects []string
	for _, in := range injections {
		add(in.group)
		if in.group == "write" || in.group == "close" {
			add("open")
		}
		for _, p := range in.paths {
			addP(p)
		}
		injects = append(injects, "inject="+faultCalls[in.group]+":error="+in.errno+":when="+in.when)
	}
	for _, p := range filter {
		addP(p)
	}
	args = append(args, "-e", "trace="+strings.Join(calls, ","))
	for _, in := range injects {
		args = append(args, "-e", in)
	}
	args = append(args, "--")
	args = append(args, side.cmd(e, root, reqFile)...)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "strace", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), append([]string{"FAULTLINE_TEST_SEED=" + demoSeed}, side.env...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: adapter run failed: %v\nstderr: %s\nstdout: %.2000s", side.name, err, stderr.String(), stdout.String())
	}
	var captured struct {
		Call    *demoartifactstest.Call    `json:"call"`
		Process *demoartifactstest.Process `json:"process"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &captured); err != nil || (captured.Call == nil) == (captured.Process == nil) {
		t.Fatalf("%s: want exactly one of call and process: %v\nstdout: %.2000s\nstderr: %s", side.name, err, stdout.String(), stderr.String())
	}
	if (req.Kind == "cli") != (captured.Process != nil) {
		t.Fatalf("%s: a %s request returned the wrong outcome kind", side.name, req.Kind)
	}
	tb, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("%s: strace log unreadable: %v", side.name, err)
	}
	parsed, lifecycle, err := parseStraceLifecycle(strings.ReplaceAll(string(tb), "\r", ""))
	if err != nil {
		t.Fatalf("%s: strace log unparsable: %v\n%s", side.name, err, tb)
	}
	// An accepted unavailable syscall entry is not a call; make it visible.
	for _, note := range lifecycle {
		t.Logf("%s: accepted lifecycle record: %s\n%s", side.name, note, tb)
	}
	tree, err := demoartifactstest.Inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	run := demoRun{root: root, process: captured.Process, tree: tree, trace: string(tb), calls: parsed}
	if captured.Call != nil {
		run.call = *captured.Call
	}
	return run
}

func demoMask(root, s string) string { return strings.ReplaceAll(s, root, "<TMP>") }

// errorText is the propagated failure's message. A library call's is its
// error; a CLI's is the formatted failure line, and then the process must also
// have exited 1 with empty stdout (no success line is printed before a write).
func (r demoRun) errorText(t *testing.T) string {
	t.Helper()
	if r.process != nil {
		const prefix, suffix = "FaultLine error: ", "\nNext: pnpm fl help\n"
		p := r.process
		if p.ExitCode != 1 || p.Stdout != "" || !strings.HasPrefix(p.Stderr, prefix) || !strings.HasSuffix(p.Stderr, suffix) {
			t.Fatalf("CLI did not fail cleanly: exit=%d stdout=%q stderr=%q", p.ExitCode, p.Stdout, p.Stderr)
		}
		return strings.TrimSuffix(strings.TrimPrefix(p.Stderr, prefix), suffix)
	}
	if r.call.Error == nil {
		t.Fatalf("writer succeeded, want a propagated error (value %v)", r.call.ValueJSON)
	}
	return *r.call.Error
}

func noStage(t *testing.T, side string, r demoRun) {
	t.Helper()
	if s := demoSiblings(r.tree, "demo.staging-"); len(s) != 0 {
		t.Errorf("%s: stage left behind: %v", side, s)
	}
}

func noBackup(t *testing.T, side string, r demoRun) {
	t.Helper()
	if s := demoSiblings(r.tree, "demo.backup-"); len(s) != 0 {
		t.Errorf("%s: backup left behind: %v", side, s)
	}
}

func outputRoot(r demoRun) string { return demoDecode(demoFind(r.tree, demoOutRel+"/ROOT.sha256")) }

// demoPlan is one side's resolved fault: the injections to run, the exact
// (INJECTED) records they must produce, the renames the replacement protocol
// must perform (when the row is a rename row), and the text the propagated
// error must have (with the case root replaced by <TMP>).
type demoPlan struct {
	injections []demoInjection
	want       []wantFault
	renames    []string // nil = not a rename row
	errText    string   // "" = not asserted textually
	errPrefix  string
	filter     []string
	groups     []string
}

// demoDiscover runs this side's own traces for a row.
type demoDiscover struct {
	t    *testing.T
	e    demoEnv
	side demoSide
	req  demoartifactstest.Request
	base string
	p    demoPaths
}

func (d demoDiscover) run(injections []demoInjection, groups, filter []string) demoRun {
	d.t.Helper()
	return runDemo(d.t, d.e, d.side, d.req, d.base, injections, groups, filter)
}

// ordinal locates group on path (-> dest) in r and returns its position among
// the traced family calls, failing if it was not observed or the family spans
// threads.
func (d demoDiscover) ordinal(r demoRun, group, path, dest string) int {
	d.t.Helper()
	idx, err := findCall(r.calls, group, path, dest)
	if err != nil {
		d.t.Fatalf("%s: %v\n%s", d.side.name, err, r.trace)
	}
	n, err := ordinalOf(r.calls, group, idx)
	if err != nil {
		d.t.Fatalf("%s: %v\n%s", d.side.name, err, r.trace)
	}
	return n
}

// renameSequence checks r's rename protocol is exactly kinds on one thread.
func (d demoDiscover) renameSequence(r demoRun, kinds ...string) {
	d.t.Helper()
	got, _, err := classifyRenames(r.calls, d.p.out, d.p.stage, d.p.backup)
	if err != nil {
		d.t.Fatalf("%s: %v\n%s", d.side.name, err, r.trace)
	}
	if strings.Join(got, ",") != strings.Join(kinds, ",") {
		d.t.Fatalf("%s: rename sequence %v, want %v\n%s", d.side.name, got, kinds, r.trace)
	}
}

func (d demoDiscover) errTextFor(group, name, errno, path, dest string) string {
	return demoMask(demoRoot(d.base), expectedErrorText(group, name, errno, path, dest))
}

// demoRow is one registered writer fault row; each (group, errno) pair is its
// own execution. A row that registers no executions fails.
type demoRow struct {
	name     string
	existing bool
	preview  bool // the request is the judge-preview CLI, not the library writer
	errnos   []string
	groups   []string
	plan     func(d demoDiscover, group, errno string) demoPlan
	check    func(t *testing.T, side string, r demoRun)
	// checkGroup, when set, replaces check for rows whose resulting state
	// depends on which call failed (open, write or close).
	checkGroup func(t *testing.T, side, group string, r demoRun)
}

func (r demoRow) request(id string) demoartifactstest.Request {
	if r.preview {
		return demoPreviewRequest(r.existing, id)
	}
	return demoRequest(r.existing, id)
}

var allDemoErrnos = []string{"EIO", "ENOSPC", "EACCES"}

// fileFault plans a single fault on group of one exact file: the ordinal is
// this side's own discovered position of that call under -P file.
func fileFault(file func(p demoPaths) string) func(d demoDiscover, group, errno string) demoPlan {
	return func(d demoDiscover, group, errno string) demoPlan {
		path := file(d.p)
		r := d.run(nil, []string{group}, []string{path})
		n := d.ordinal(r, group, path, "")
		name := "open"
		if idx, _ := findCall(r.calls, group, path, ""); idx >= 0 {
			name = r.calls[idx].Name
		}
		pl := demoPlan{
			injections: []demoInjection{{group, errno, []string{path}, strconv.Itoa(n)}},
			want:       []wantFault{{Group: group, Errno: errno, Path: path}},
			errText:    d.errTextFor(group, name, errno, path, ""),
			groups:     []string{group}, filter: []string{path},
		}
		if group == "mkdir" {
			// The stage is made by a recursive mkdir that stays absent after the
			// injected failure: the complete message is exact (no prefix).
			pl.errText = demoMask(demoRoot(d.base), absentStageMkdirText(errno, path))
		}
		return pl
	}
}

// renameFault plans a single fault on one protocol rename (move-old or
// publish), after checking the discovered no-fault rename sequence.
func renameFault(target string, baseline ...string) func(d demoDiscover, group, errno string) demoPlan {
	return func(d demoDiscover, group, errno string) demoPlan {
		filter := []string{d.p.out, d.p.stage, d.p.backup}
		r := d.run(nil, []string{"rename"}, filter)
		d.renameSequence(r, baseline...)
		src, dst := d.p.out, d.p.backup
		if target == "publish" {
			src, dst = d.p.stage, d.p.out
		}
		n := d.ordinal(r, "rename", src, dst)
		return demoPlan{
			injections: []demoInjection{{"rename", errno, filter, strconv.Itoa(n)}},
			want:       []wantFault{{Group: "rename", Errno: errno, Path: src, Dest: dst}},
			errText:    d.errTextFor("rename", "rename", errno, src, dst),
			groups:     []string{"rename"}, filter: filter,
		}
	}
}

var demoRows = []demoRow{
	{name: "stage-mkdir", errnos: []string{"EIO"}, groups: []string{"mkdir"},
		plan: fileFault(func(p demoPaths) string { return p.stage }),
		check: func(t *testing.T, side string, r demoRun) {
			if demoHasPrefix(r.tree, demoOutRel) {
				t.Errorf("%s: output published", side)
			}
			noStage(t, side, r)
		}},
	{name: "stage-manifest", errnos: allDemoErrnos, groups: []string{"open", "write", "close"},
		plan: fileFault(func(p demoPaths) string { return p.stage + "/manifest.json" }),
		check: func(t *testing.T, side string, r demoRun) {
			if demoHasPrefix(r.tree, demoOutRel) {
				t.Errorf("%s: output published", side)
			}
			noStage(t, side, r)
		}},
	{name: "stage-root", existing: true, errnos: allDemoErrnos, groups: []string{"open", "write", "close"},
		plan: fileFault(func(p demoPaths) string { return p.stage + "/ROOT.sha256" }),
		check: func(t *testing.T, side string, r demoRun) {
			if got := outputRoot(r); got != demoReplayRt+"\n" {
				t.Errorf("%s: old output changed: %q", side, got)
			}
			noStage(t, side, r)
			noBackup(t, side, r)
		}},
	{name: "move-old", existing: true, errnos: []string{"EIO"}, groups: []string{"rename"},
		plan: renameFault("move-old", "move-old", "publish"),
		check: func(t *testing.T, side string, r demoRun) {
			if got := outputRoot(r); got != demoReplayRt+"\n" {
				t.Errorf("%s: old output not intact: %q", side, got)
			}
			noStage(t, side, r)
			noBackup(t, side, r)
		}},
	{name: "publish-new", errnos: []string{"EIO"}, groups: []string{"rename"},
		plan: renameFault("publish", "publish"),
		check: func(t *testing.T, side string, r demoRun) {
			if demoHasPrefix(r.tree, demoOutRel) {
				t.Errorf("%s: output published", side)
			}
			noStage(t, side, r)
		}},
	{name: "publish-rollback", existing: true, errnos: []string{"EIO"}, groups: []string{"rename"},
		plan: func(d demoDiscover, group, errno string) demoPlan {
			pl := renameFault("publish", "move-old", "publish")(d, group, errno)
			pl.renames = []string{"move-old", "publish", "restore"}
			return pl
		},
		check: func(t *testing.T, side string, r demoRun) {
			if got := outputRoot(r); got != demoReplayRt+"\n" {
				t.Errorf("%s: old bytes not restored: %q", side, got)
			}
			noStage(t, side, r)
			noBackup(t, side, r)
		}},
	{name: "rollback-fails", existing: true, errnos: []string{"EIO"}, groups: []string{"rename"},
		plan: func(d demoDiscover, group, errno string) demoPlan {
			filter := []string{d.p.out, d.p.stage, d.p.backup}
			// The no-fault protocol is move-old, publish. Failing publish
			// makes the restore happen, which only a faulted run can show:
			// discover its position in a run that fails the publish alone.
			base := d.run(nil, []string{"rename"}, filter)
			d.renameSequence(base, "move-old", "publish")
			ordP := d.ordinal(base, "rename", d.p.stage, d.p.out)
			partial := d.run([]demoInjection{{"rename", errno, filter, strconv.Itoa(ordP)}}, []string{"rename"}, filter)
			d.renameSequence(partial, "move-old", "publish", "restore")
			ordR := d.ordinal(partial, "rename", d.p.backup, d.p.out)
			if ordR != ordP+1 {
				d.t.Fatalf("%s: restore is rename #%d after publish #%d; the N+ injection would fail other calls\n%s", d.side.name, ordR, ordP, partial.trace)
			}
			return demoPlan{
				injections: []demoInjection{{"rename", errno, filter, strconv.Itoa(ordP) + "+"}},
				want: []wantFault{
					{Group: "rename", Errno: errno, Path: d.p.stage, Dest: d.p.out},
					{Group: "rename", Errno: errno, Path: d.p.backup, Dest: d.p.out},
				},
				renames: []string{"move-old", "publish", "restore"},
				// The restore failure supersedes the publish failure.
				errText: d.errTextFor("rename", "rename", errno, d.p.backup, d.p.out),
				groups:  []string{"rename"}, filter: filter,
			}
		},
		check: func(t *testing.T, side string, r demoRun) {
			if demoHasPrefix(r.tree, demoOutRel) {
				t.Errorf("%s: output exists after a failed restore", side)
			}
			if b := demoSiblings(r.tree, "demo.backup-"); len(b) != 1 {
				t.Errorf("%s: backup not retained: %v", side, b)
			} else if got := demoDecode(demoFind(r.tree, ".faultline/bundles/"+b[0]+"/ROOT.sha256")); got != demoReplayRt+"\n" {
				t.Errorf("%s: retained backup bytes %q", side, got)
			}
			noStage(t, side, r)
		}},
	{name: "backup-cleanup", existing: true, errnos: []string{"EIO"}, groups: []string{"unlink"},
		plan: func(d demoDiscover, group, errno string) demoPlan {
			// Which removal call comes first is whatever this side really
			// makes (unlink, unlinkat or rmdir, recursive or not).
			broad := d.run(nil, []string{"unlink"}, nil)
			first, err := firstCallUnder(broad.calls, "unlink", d.p.backup)
			if err != nil {
				d.t.Fatalf("%s: %v\n%s", d.side.name, err, broad.trace)
			}
			narrow := d.run(nil, []string{"unlink"}, []string{first.Path})
			n := d.ordinal(narrow, "unlink", first.Path, "")
			return demoPlan{
				injections: []demoInjection{{"unlink", errno, []string{first.Path}, strconv.Itoa(n)}},
				want:       []wantFault{{Group: "unlink", Errno: errno, Path: first.Path}},
				errText:    d.errTextFor("unlink", removalName(first), errno, first.Path, ""),
				groups:     []string{"unlink"}, filter: []string{first.Path},
			}
		},
		check: func(t *testing.T, side string, r demoRun) {
			if got := outputRoot(r); got != demoRerunRt+"\n" {
				t.Errorf("%s: new output not installed: %q", side, got)
			}
			if b := demoSiblings(r.tree, "demo.backup-"); len(b) != 1 {
				t.Errorf("%s: backup not left behind: %v", side, b)
			}
			noStage(t, side, r)
		}},
	{name: "stage-cleanup", errnos: []string{"EIO"}, groups: []string{"write"},
		plan: func(d demoDiscover, group, errno string) demoPlan {
			manifest := d.p.stage + "/manifest.json"
			wr := d.run(nil, []string{"write"}, []string{manifest})
			ordW := d.ordinal(wr, "write", manifest, "")
			write := demoInjection{"write", errno, []string{manifest}, strconv.Itoa(ordW)}
			// The stage removal that follows the failed write is whatever
			// this side really makes; discover it in a run with that write
			// already failed.
			broad := d.run([]demoInjection{write}, []string{"unlink"}, nil)
			first, err := firstCallUnder(broad.calls, "unlink", d.p.stage)
			if err != nil {
				d.t.Fatalf("%s: %v\n%s", d.side.name, err, broad.trace)
			}
			narrow := d.run([]demoInjection{write}, []string{"unlink"}, []string{first.Path})
			ordU := d.ordinal(narrow, "unlink", first.Path, "")
			return demoPlan{
				injections: []demoInjection{write, {"unlink", errno, []string{first.Path}, strconv.Itoa(ordU)}},
				want: []wantFault{
					{Group: "write", Errno: errno, Path: manifest},
					{Group: "unlink", Errno: errno, Path: first.Path},
				},
				// The removal error supersedes the write error.
				errText: d.errTextFor("unlink", removalName(first), errno, first.Path, ""),
				groups:  []string{"write", "unlink"}, filter: []string{manifest, first.Path},
			}
		},
		check: func(t *testing.T, side string, r demoRun) {
			if demoHasPrefix(r.tree, demoOutRel) {
				t.Errorf("%s: output published", side)
			}
			if s := demoSiblings(r.tree, "demo.staging-"); len(s) != 1 {
				t.Errorf("%s: the stage was not retained: %v", side, s)
			}
		}},
}

// demoPreviewPage is the committed default preview page, the independent literal
// the preview rows compare complete and partial bytes with.
func demoPreviewPage(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "demo-artifacts", "judge-preview.html"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// previewHTML is the requested HTML in a resulting tree, if any.
func previewHTML(r demoRun) (string, bool) {
	e := demoFind(r.tree, demoPreviewOut)
	if e == nil {
		return "", false
	}
	return demoDecode(e), true
}

// previewState checks one side's resulting preview file after the failed
// open, write or close, from the old bytes (hadOld) and the committed page. A
// failed open leaves the old file untouched (or no file); a failed write leaves
// the O_TRUNC'd file holding a strict prefix of the page; a failed close leaves
// the complete page; and nothing but the one file exists (no stage, no backup).
func previewState(t *testing.T, side, group string, hadOld bool, r demoRun) {
	t.Helper()
	const old = "old preview\n"
	page := demoPreviewPage(t)
	got, present := previewHTML(r)
	if len(r.tree) > 1 || (len(r.tree) == 1 && !present) {
		t.Errorf("%s: unexpected entries beside the preview: %s", side, demoTreeSummary(r.tree))
	}
	switch group {
	case "open":
		switch {
		case hadOld && (!present || got != old):
			t.Errorf("%s: a failed open changed the existing preview: present=%v %q", side, present, got)
		case !hadOld && present:
			t.Errorf("%s: a failed open created %q", side, got)
		}
	case "write":
		if !present || got == page || got == old || !strings.HasPrefix(page, got) {
			t.Errorf("%s: after a failed write the file must hold a strict prefix of the page: present=%v len=%d", side, present, len(got))
		}
	case "close":
		if !present || got != page {
			t.Errorf("%s: after a failed close the complete page must be present: present=%v len=%d", side, present, len(got))
		}
	}
}

func init() {
	for _, existing := range []bool{false, true} {
		name := "preview-write"
		if existing {
			name = "preview-overwrite"
		}
		existing := existing
		demoRows = append(demoRows, demoRow{
			name: name, existing: existing, preview: true, errnos: allDemoErrnos, groups: []string{"open", "write", "close"},
			plan: fileFault(func(p demoPaths) string { return p.html }),
			checkGroup: func(t *testing.T, side, group string, r demoRun) {
				previewState(t, side, group, existing, r)
			},
		})
	}
}

func demoEnvironment(t *testing.T) demoEnv {
	t.Helper()
	repo := repoRoot(t)
	demoPreflight(t, repo)
	setWriterUmask(t)
	script := filepath.Join(t.TempDir(), "ts-invoke.mjs")
	if err := os.WriteFile(script, []byte(demoTSInvoke), 0o644); err != nil {
		t.Fatal(err)
	}
	stage, backup := demoSeededNames(t)
	return demoEnv{repo: repo, driver: demoBuildDriver(t, repo), script: script, cli: flBinary, stage: stage, backup: backup}
}

// TestLiveDemoArtifactFaults fails real syscalls on the demo bundle writer's
// stage, backup and output paths and compares the frozen TS writer and the
// Go writer: the exact intended injected records, the propagated error and
// the complete resulting tree. It needs Linux, strace, Go and the pinned Node;
// nothing here is a stand-in.
func TestLiveDemoArtifactFaults(t *testing.T) {
	oracle.Skip(t)
	env := demoEnvironment(t)

	// The fixed shim the rows rely on is first checked against the canonical
	// Task 1 adapter here, under the same eligible preflight (live oracle,
	// pinned Node, built dist), so it cannot be orphaned from the explicitly
	// named faults job. It needs no strace; the rows below do, and the job that
	// names this test installs it (CI fails rather than skips without it).
	t.Run("canonical-shim-contract", func(t *testing.T) { demoShimContract(t, env) })
	requireStrace(t)
	demoStraceProbe(t)

	executions, preview := 0, 0
	for _, row := range demoRows {
		if len(row.groups) == 0 || len(row.errnos) == 0 {
			t.Fatalf("row %s registers no executions", row.name)
		}
		if row.preview {
			preview += len(row.groups) * len(row.errnos)
		}
		if row.check == nil && row.checkGroup == nil {
			t.Fatalf("row %s asserts no resulting state", row.name)
		}
		for _, group := range row.groups {
			for _, errno := range row.errnos {
				executions++
				row, group, errno := row, group, errno
				t.Run(row.name+"/"+group+"-"+errno, func(t *testing.T) {
					req := row.request("fault-" + row.name)
					results := map[string]demoRun{}
					for _, side := range demoSides {
						base := t.TempDir()
						d := demoDiscover{t: t, e: env, side: side, req: req, base: base, p: env.paths(demoRoot(base))}
						plan := row.plan(d, group, errno)
						res := runDemo(t, env, side, req, base, plan.injections, plan.groups, plan.filter)
						if err := validateInjections(res.calls, plan.want); err != nil {
							t.Fatalf("%s: %v\n%s", side.name, err, res.trace)
						}
						if plan.renames != nil {
							got, _, err := classifyRenames(res.calls, d.p.out, d.p.stage, d.p.backup)
							if err != nil || strings.Join(got, ",") != strings.Join(plan.renames, ",") {
								t.Fatalf("%s: faulted rename sequence %v (%v), want %v\n%s", side.name, got, err, plan.renames, res.trace)
							}
						}
						msg := demoMask(res.root, res.errorText(t))
						switch {
						case plan.errText != "" && msg != plan.errText:
							t.Errorf("%s: error\n got %q\nwant %q", side.name, msg, plan.errText)
						case plan.errPrefix != "" && !strings.HasPrefix(msg, plan.errPrefix):
							t.Errorf("%s: error\n got %q\nwant prefix %q", side.name, msg, plan.errPrefix)
						}
						if row.checkGroup != nil {
							row.checkGroup(t, side.name, group, res)
						} else {
							row.check(t, side.name, res)
						}
						results[side.name] = res
					}
					ts, gr := results["TS"], results["Go"]
					if a, b := demoMask(ts.root, ts.errorText(t)), demoMask(gr.root, gr.errorText(t)); a != b {
						t.Errorf("error differs\nTS: %s\nGo: %s", a, b)
					}
					if !reflect.DeepEqual(ts.tree, gr.tree) {
						t.Errorf("resulting trees differ\nTS: %s\nGo: %s", demoTreeSummary(ts.tree), demoTreeSummary(gr.tree))
					}
				})
			}
		}
	}
	if executions == 0 {
		t.Fatal("no fault executions were registered")
	}
	// preview-write and preview-overwrite: open, write and close for each of
	// the three errnos.
	if preview != 18 {
		t.Fatalf("preview fault executions = %d, want 18", preview)
	}
}

// demoShimContract checks the fixed invoke-only TS shim used by the fault rows
// against the unchanged Task 1 oracle adapter, with no fault, for a fresh
// replay write, an existing-bundle replacement, a fresh preview write and a
// preview overwrite: Prepare's setup inventory equals the adapter's `before`,
// and the shim's call (or process) and complete resulting tree equal the
// adapter's (the case-root prefix is replaced by <TMP> and nothing else). It
// runs inside TestLiveDemoArtifactFaults, never as a separate top-level test.
func demoShimContract(t *testing.T, env demoEnv) {
	gen := filepath.Join(env.repo, "difftest", "gen")
	for _, req := range []demoartifactstest.Request{
		demoRequest(false, "contract-fresh"), demoRequest(true, "contract-replace"),
		demoPreviewRequest(false, "contract-preview-fresh"), demoPreviewRequest(true, "contract-preview-overwrite"),
	} {
		t.Run(req.ID, func(t *testing.T) {
			body, _ := json.Marshal(req)
			// Canonical adapter, in its own fresh root (it does its own setup).
			canonBase := t.TempDir()
			canonRoot := demoRoot(canonBase)
			if err := os.MkdirAll(canonRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			canonReq := filepath.Join(canonBase, "request.json")
			if err := os.WriteFile(canonReq, body, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("node", "--import", "file://"+filepath.Join(gen, "seed.mjs"), filepath.Join(gen, "demo-artifacts-oracle.mjs"), canonReq, canonRoot)
			cmd.Dir = canonBase
			cmd.Env = append(os.Environ(), "FAULTLINE_TEST_SEED="+demoSeed)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("canonical adapter: %v", err)
			}
			var canon demoartifactstest.Capture
			if err := json.Unmarshal(out, &canon); err != nil {
				t.Fatal(err)
			}
			// Shim, after Prepare in a separate fresh root.
			shimBase := t.TempDir()
			shimRoot := demoRoot(shimBase)
			if err := os.MkdirAll(shimRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := demoartifactstest.Prepare(shimRoot, env.repo, req); err != nil {
				t.Fatal(err)
			}
			prepared, err := demoartifactstest.Inventory(shimRoot)
			if err != nil {
				t.Fatal(err)
			}
			shimReq := filepath.Join(shimBase, "request.json")
			if err := os.WriteFile(shimReq, body, 0o644); err != nil {
				t.Fatal(err)
			}
			sc := exec.Command("node", "--import", "file://"+filepath.Join(gen, "seed.mjs"), env.script, shimReq, env.repo)
			sc.Dir = shimRoot
			sc.Env = append(os.Environ(), "FAULTLINE_TEST_SEED="+demoSeed)
			sout, err := sc.Output()
			if err != nil {
				t.Fatalf("shim: %v", err)
			}
			var shim struct {
				Call    *demoartifactstest.Call    `json:"call"`
				Process *demoartifactstest.Process `json:"process"`
			}
			if err := json.Unmarshal(sout, &shim); err != nil {
				t.Fatal(err)
			}
			if (shim.Call == nil) == (shim.Process == nil) || (req.Kind == "cli") != (shim.Process != nil) {
				t.Fatalf("shim returned the wrong outcome kind for %s: %s", req.Kind, sout)
			}
			after, err := demoartifactstest.Inventory(shimRoot)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(prepared, canon.Before) {
				t.Errorf("Prepare setup differs from the adapter's before\nPrepare: %s\nadapter: %s", demoTreeSummary(prepared), demoTreeSummary(canon.Before))
			}
			mask := func(root string, c demoartifactstest.Call) demoartifactstest.Call {
				if c.ValueJSON != nil {
					v := strings.ReplaceAll(*c.ValueJSON, root, "<TMP>")
					c.ValueJSON = &v
				}
				if c.Error != nil {
					v := strings.ReplaceAll(*c.Error, root, "<TMP>")
					c.Error = &v
				}
				return c
			}
			if req.Kind == "cli" {
				if canon.Process == nil || canon.Call != nil {
					t.Fatalf("canonical adapter returned %+v for a CLI request", canon)
				}
				maskP := func(root string, p demoartifactstest.Process) demoartifactstest.Process {
					p.Stdout, p.Stderr = strings.ReplaceAll(p.Stdout, root, "<TMP>"), strings.ReplaceAll(p.Stderr, root, "<TMP>")
					return p
				}
				if got, want := maskP(shimRoot, *shim.Process), maskP(canonRoot, *canon.Process); !reflect.DeepEqual(got, want) {
					t.Errorf("process differs\nshim:    %+v\nadapter: %+v", got, want)
				}
			} else if got, want := mask(shimRoot, *shim.Call), mask(canonRoot, *canon.Call); !reflect.DeepEqual(got, want) {
				t.Errorf("call differs\nshim:    %+v\nadapter: %+v", got, want)
			}
			if !reflect.DeepEqual(after, canon.After) {
				t.Errorf("resulting tree differs\nshim:    %s\nadapter: %s", demoTreeSummary(after), demoTreeSummary(canon.After))
			}
		})
	}
}

func demoTreeSummary(tree []demoartifactstest.Entry) string {
	var b strings.Builder
	for _, e := range tree {
		fmt.Fprintf(&b, "%s:%s:%d ", e.Path, e.Kind, len(demoDecode(&e)))
	}
	return b.String()
}
