//go:build faultline_test && linux

package difftest

import (
	"bufio"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// writerFault fails the first call of one syscall group on one writer path
// with errno (spec 3.2). glob is a path.Match pattern over the path relative
// to the case root (ws/..., tmp/...); the first path a fault-free TS run of
// the case names that matches it is the one faulted, on both sides.
type writerFault struct {
	caseID, label, glob, call, errno string
	// fakeGit names a difftest/testdata/fakegit directory put first on PATH
	// for both sides (and the fault-free discovery run).
	fakeGit string
	// everyCall fails every call of the group on any path instead of the
	// first one on the discovered path (see writerFaultPaths).
	everyCall bool
}

func (f writerFault) name() string { return f.caseID + "/" + f.label + "/" + f.call + "-" + f.errno }

// faultCalls are the syscalls of each group: libuv and Go reach a file
// through different members of a family (write or pwrite64, rmdir for an
// empty directory, renameat2 on newer libcs, fchmodat2 on newer kernels; a
// leading ? lets an strace that does not know the name ignore it).
var faultCalls = map[string]string{
	"open":   "open,openat,openat2",
	"write":  "write,writev,pwrite64,pwritev,pwritev2",
	"rename": "rename,renameat,renameat2",
	"link":   "link,linkat",
	"unlink": "unlink,unlinkat,rmdir",
	"close":  "close",
	"mkdir":  "mkdir,mkdirat",
	"chmod":  "chmod,fchmodat,?fchmodat2",
	"fsync":  "fsync,fdatasync",
}

// discoveryCalls are traced in the fault-free run that names the paths.
const discoveryCalls = "open,openat,openat2,rename,renameat,renameat2,link,linkat,unlink,unlinkat,rmdir,mkdir,mkdirat,chmod,fchmodat,?fchmodat2"

// writerFaultPaths are the writers of spec 3.2, each with the syscall
// groups that reach its path in a fault-free run (a group that never does
// injects nothing, which the test rejects).
var writerFaultPaths = []struct {
	caseID, label, glob string
	calls               []string
	fakeGit             string
}{
	{"propose-input", "witness-record", "ws/.faultline/witnesses/proposals/*.json", []string{"open", "write", "close"}, ""},
	{"policy-freeze", "policy", "ws/.faultline/witnesses/standing-policies/*.json", []string{"open", "write", "close"}, ""},
	{"ratify", "ratification", "ws/.faultline/witnesses/ratifications/*.json", []string{"open", "write", "close"}, ""},
	{"freeze", "binding", "ws/.faultline/incidents/bindings/*.json", []string{"open", "write", "close"}, ""},
	{"ledger-bind", "ledger-staged", "ws/.bound-new.json.<PID>.*.tmp", []string{"open", "write", "close", "rename"}, ""},
	{"minimize-git", "minimization-temp", "ws/.faultline/minimizations/.*.faultline-git-minimization.tmp", []string{"open", "write", "close", "link", "unlink"}, ""},
	{"investigate-two-states", "stage-manifest", "ws/.faultline/git-proof-bundles/.*.staging-*/manifest.json", []string{"open", "write", "close"}, ""},
	{"investigate-two-states", "stage-root", "ws/.faultline/git-proof-bundles/.*.staging-*/ROOT.sha256", []string{"open", "write", "close"}, ""},
	{"investigate-two-states", "stage-rename", "ws/.faultline/git-proof-bundles/.*.staging-*", []string{"rename"}, ""},
	{"investigate-two-states", "overlay-staged", "tmp/*/worktrees/*/.faultline-overlay-*", []string{"open", "write", "close", "rename"}, ""},
	{"commit-proof-preview", "preview-html", "ws/docs/*.html", []string{"open", "write", "close"}, ""},
	// Slice 3a (spec 3.3). The project config: rewritten in place under a
	// recursive mkdir of .faultline (src/config.ts:64).
	{"runtime-resolve-node", "config", "ws/.faultline/config.json", []string{"open", "write", "close"}, ""},
	{"runtime-resolve-node", "config-dir", "ws/.faultline", []string{"mkdir"}, ""},
	// The runtime mapping and the recursive mkdir of its parent, whose first
	// mkdir of maps/2026 fails ENOENT and climbs to maps
	// (src/runtime-mapping-file.ts:61).
	{"runtime-mapping-nested-output", "mapping", "ws/maps/2026/mapping.json", []string{"open", "write", "close"}, ""},
	{"runtime-mapping-nested-output", "mapping-parent", "ws/maps/2026", []string{"mkdir"}, ""},
	{"runtime-mapping-nested-output", "mapping-grandparent", "ws/maps", []string{"mkdir"}, ""},
	// fl init --yes: .faultlineignore (wx, src/project-init.ts:124), then the
	// turn-snapshot pre-warm: the object store and its alternates entry
	// (src/turn-snapshot.ts:207), the temporary index git writes by renaming
	// its lock onto it and fl removes (:1117), and the session cache (:781).
	// The rename is faulted through the lock, its source: x86_64 git calls
	// rename(2), on which strace -P matches only the first path (pathtrace.c
	// has no SEN_rename case), while arm64 has only renameat, which -P
	// matches on either path (CI round 1: on x86_64 the index path made no
	// call to fail).
	{"init-yes-dirty", "ignore-file", "ws/.faultlineignore", []string{"open", "write", "close"}, ""},
	{"init-yes-dirty", "snapshot-objects-info", "ws/.git/faultline/objects/info", []string{"mkdir"}, ""},
	{"init-yes-dirty", "snapshot-objects-pack", "ws/.git/faultline/objects/pack", []string{"mkdir"}, ""},
	{"init-yes-dirty", "alternates", "ws/.git/objects/info/alternates", []string{"open", "write", "close"}, ""},
	{"init-yes-dirty", "snapshot-index", "tmp/faultline-turn-tree-*.index", []string{"unlink"}, ""},
	{"init-yes-dirty", "snapshot-index-lock", "tmp/faultline-turn-tree-*.index.lock", []string{"rename"}, ""},
	{"init-yes-dirty", "snapshot-cache", "ws/.git/faultline/turn-snapshot-cache.json", []string{"open", "write", "close"}, ""},
	// The standing policy (0600 wx) and its 0700 directories
	// (src/standing-approval-policy.ts:154).
	{"init-standing", "standing-policy", "ws/.faultline/witnesses/standing-policies/*.json", []string{"open", "write", "close"}, ""},
	{"init-standing", "standing-policy-dir", "ws/.faultline/witnesses/standing-policies", []string{"mkdir"}, ""},
	{"init-standing", "witness-store-dir", "ws/.faultline/witnesses", []string{"mkdir"}, ""},
	// The project hook document (0600 wx) under a 0700 .codex
	// (src/cli-app.ts:1743).
	{"init-cli", "hooks", "ws/.codex/hooks.json", []string{"open", "write", "close"}, ""},
	{"init-cli", "codex-dir", "ws/.codex", []string{"mkdir"}, ""},
	// doctor --security: the fixture's removal in the finally
	// (src/doctor.ts:743), and the malicious hook file, its chmod and its
	// unlink inside that removal.
	{"doctor-security", "fixture", "tmp/faultline-doctor-security-*", []string{"unlink"}, ""},
	{"doctor-security", "hook", "tmp/faultline-doctor-security-*/malicious-hooks/pre-commit", []string{"open", "write", "close", "chmod", "unlink"}, ""},
	// Slice 3b (spec 3.3). No 3b writer unlinks in a fault-free run (the
	// stage removals run only after a failure), so no unlink group.
	// fl incident start: the witness proposal (0600 wx under the 0700 store
	// a recursive mkdir makes, src/witness-lock.ts:254), then the draft
	// (0600 wx) under directories made one segment at a time
	// (src/incident-store.ts:37).
	{"incident-start-command", "draft", "ws/repo/.faultline/incidents/drafts/*.json", []string{"open", "write", "close"}, ""},
	{"incident-start-command", "drafts-dir", "ws/repo/.faultline/incidents/drafts", []string{"mkdir"}, ""},
	{"incident-start-command", "incidents-dir", "ws/repo/.faultline/incidents", []string{"mkdir"}, ""},
	{"incident-start-command", "proposal", "ws/repo/.faultline/witnesses/proposals/*.json", []string{"open", "write", "close"}, ""},
	{"incident-start-command", "witness-store-dir", "ws/repo/.faultline/witnesses", []string{"mkdir"}, ""},
	{"incident-start-command", "repo-faultline-dir", "ws/repo/.faultline", []string{"mkdir"}, ""},
	// fl incident continue: the session binding (0600, overwritten,
	// src/incident-binding.ts:105). With none yet, its first open is the
	// write; an existing one is read first, so only its write is faulted.
	{"incident-continue-unapproved", "binding", "ws/repo/.faultline/incidents/bindings/*.json", []string{"open", "write", "close"}, ""},
	{"incident-continue-unapproved", "bindings-dir", "ws/repo/.faultline/incidents/bindings", []string{"mkdir"}, ""},
	{"incident-continue-inherit-digest", "binding-overwrite", "ws/repo/.faultline/incidents/bindings/*.json", []string{"write"}, ""},
	// The continuation's bundle: its root directory, the stage (mkdir and
	// the rename into place, faulted through the stage, its source) and a
	// staged file (src/git-proof-bundle.ts:1060).
	{"incident-continue-inherit-digest", "bundles-dir", "ws/repo/.faultline/git-proof-bundles", []string{"mkdir"}, ""},
	{"incident-continue-inherit-digest", "bundle-stage", "ws/repo/.faultline/git-proof-bundles/.*.staging-*", []string{"mkdir", "rename"}, ""},
	{"incident-continue-inherit-digest", "bundle-stage-manifest", "ws/repo/.faultline/git-proof-bundles/.*.staging-*/manifest.json", []string{"open", "write", "close"}, ""},
	// fl prevention write --from-bundle (src/prevention-proof.ts:470): the
	// proof root's directories, the 0700 stage, its 0600 wx files with
	// fsync (the first and the last), the stage's own fsync (open and fsync
	// errors swallowed), the rename and the parent directory's fsync
	// (swallowed); then AGENTS.md, created under a recursive mkdir of the
	// repository, its errors swallowed (src/cli-app.ts:2539).
	{"prevention-write-bundle-ok", "proof-root-parent", "ws/.faultline", []string{"mkdir"}, ""},
	{"prevention-write-bundle-ok", "proof-root", "ws/.faultline/prevention-proofs", []string{"mkdir", "open", "fsync"}, ""},
	{"prevention-write-bundle-ok", "stage", "ws/.faultline/prevention-proofs/.prevention-*.tmp", []string{"mkdir", "open", "fsync", "rename"}, ""},
	{"prevention-write-bundle-ok", "stage-readme", "ws/.faultline/prevention-proofs/.prevention-*.tmp/README.md", []string{"open", "write", "fsync", "close"}, ""},
	{"prevention-write-bundle-ok", "stage-repaired-runs", "ws/.faultline/prevention-proofs/.prevention-*.tmp/repaired-runs.json", []string{"open", "write", "fsync", "close"}, ""},
	{"prevention-write-bundle-ok", "agents", "ws/AGENTS.md", []string{"open", "write", "close"}, ""},
	{"prevention-write-bundle-ok", "agents-dir", "ws", []string{"mkdir"}, ""},
	// fl prevention write --input, whose write errors throw instead of
	// becoming PREVENTION_EXPORT_FAILED; and an existing AGENTS.md, read
	// (the first open) before it is rewritten.
	{"prevention-write-input-summary", "stage", "ws/.faultline/prevention-proofs/.s.*.tmp", []string{"mkdir", "rename"}, ""},
	{"prevention-write-input-summary", "stage-prevention", "ws/.faultline/prevention-proofs/.s.*.tmp/prevention.json", []string{"open", "write", "fsync", "close"}, ""},
	{"prevention-write-input-agents-append", "agents-existing", "ws/AGENTS.md", []string{"open", "write"}, ""},
	// Slice 4b (spec 2.6): fl investigate turns and its --minimize bridge.
	// The bundle is written under .faultline/turn-proof-bundles (the stage's
	// mkdir and the rename into place faulted through the stage, its source),
	// after the verifier's temporary pack repository; the session binding
	// freeze left is rewritten; the minimization goes through its temp file.
	{"turns-proof", "turn-root", "ws/.faultline/turn-proof-bundles", []string{"mkdir"}, ""},
	{"turns-proof", "turn-stage", "ws/.faultline/turn-proof-bundles/.turns-*.staging-*", []string{"mkdir", "rename"}, ""},
	{"turns-proof", "turn-stage-manifest", "ws/.faultline/turn-proof-bundles/.turns-*.staging-*/manifest.json", []string{"open", "write", "close"}, ""},
	{"turns-proof", "turn-stage-pack", "ws/.faultline/turn-proof-bundles/.turns-*.staging-*/source/trees.pack", []string{"open", "write", "close"}, ""},
	{"turns-proof", "turn-stage-source-dir", "ws/.faultline/turn-proof-bundles/.turns-*.staging-*/source", []string{"mkdir"}, ""},
	{"turns-proof", "turn-pack-verify-tmp", "tmp/faultline-turn-proof-pack-*", []string{"mkdir"}, ""},
	{"turns-proof", "turn-stage-root", "ws/.faultline/turn-proof-bundles/.turns-*.staging-*/ROOT.sha256", []string{"open", "write", "close"}, ""},
	// turn-pack-verify-tmp is the verifier's seeded mkdtemp: TS retries a failed
	// mkdir with the real mkdtemp (difftest/gen/seed.mjs), which -P cannot
	// match, so faultOrdinals fails every mkdir from the ordinal at which the
	// fault-free trace makes it, on both sides.
	// Fires only on the index-pack fallback, which the fail-unpack fake git forces.
	{"turns-proof", "turn-pack-index-file", "tmp/faultline-turn-proof-pack-*/objects/pack/turn-trees.pack", []string{"open", "write"}, "fail-unpack"},
	// The binding freeze made is read twice before it is rewritten. A failed
	// close of a READ aborts Node with its native assertion (a stack trace Go
	// cannot reproduce; retained evidence in the task 11 follow-up report), so
	// the close fault targets the rewrite's close through faultOrdinals.
	{"turns-proof", "turn-binding", "ws/repo/.faultline/incidents/bindings/*.json", []string{"open", "write", "close"}, ""},
	{"turns-minimize", "turn-minimization-temp", "ws/.faultline/minimizations/.*.faultline-git-minimization.tmp", []string{"open", "write", "close", "link", "unlink"}, ""},
	{"turns-minimize", "turn-minimization-dir", "ws/.faultline/minimizations", []string{"mkdir"}, ""},
}

// faultFirstInvocation are multi-invocation writer cases whose first
// invocation alone is faulted: prevention-write-bundle-ok's first is the
// only single --from-bundle write, and the only one creating AGENTS.md.
var faultFirstInvocation = map[string]bool{"prevention-write-bundle-ok": true}

// everyCallFaultPaths fail every call of a group, on any path. The fixture
// mkdtemp of doctor --security (src/doctor.ts:605) cannot be faulted by
// path: when the seeded mkdtempSync's mkdir fails other than EEXIST,
// difftest/gen/seed.mjs retries with the real mkdtempSync, whose random
// name strace -P does not match. The mkdtemp is the first mkdir either
// side makes, and its failure ends the command, so failing every mkdir
// fails exactly it in Go and both the seeded and the real attempt in TS
// (which then throws Node's own mkdtemp error).
var everyCallFaultPaths = []struct{ caseID, label, glob, call string }{
	{"doctor-security", "fixture-mkdtemp", "tmp/faultline-doctor-security-*", "mkdir"},
}

func writerFaultMatrix() []writerFault {
	var out []writerFault
	errnos := []string{"ENOSPC", "EIO", "EACCES"}
	for _, p := range writerFaultPaths {
		for _, call := range p.calls {
			for _, errno := range errnos {
				out = append(out, writerFault{caseID: p.caseID, label: p.label, glob: p.glob, call: call, errno: errno, fakeGit: p.fakeGit})
			}
		}
	}
	for _, p := range everyCallFaultPaths {
		for _, errno := range errnos {
			out = append(out, writerFault{caseID: p.caseID, label: p.label, glob: p.glob, call: p.call, errno: errno, everyCall: true})
		}
	}
	return out
}

// requireStrace skips without strace on PATH, except in CI.
func requireStrace(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("strace"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("strace is required in CI")
		}
		t.Skip("no strace")
	}
}

// TestLiveWriterFaults fails one syscall on one writer path with strace and
// compares TS and Go like the writer replay: stdout, stderr, exit status,
// recorded argv and the workspace tree (what each leaves behind). Go runs
// with GOMAXPROCS=1: strace counts when=1 per thread (round 6 finding 15),
// and each faulted path is one the writer touches once per group.
func TestLiveWriterFaults(t *testing.T) {
	oracle.Skip(t)
	requireStrace(t)
	setWriterUmask(t)
	repo := repoRoot(t)
	templates := map[string]writerTemplate{}
	for _, tpl := range loadWriterTemplates(t, repo) {
		templates[tpl.ID] = tpl
	}
	discovered := map[string]string{}
	matrix := writerFaultMatrix()
	defer func() { liveSummary(t, "%d fault cases (path x syscall group x errno)", len(matrix)) }()
	for _, f := range matrix {
		t.Run(f.name(), func(t *testing.T) {
			tpl, ok := templates[f.caseID]
			if ok && faultFirstInvocation[f.caseID] {
				tpl.Argv = tpl.Argv[:1]
			}
			if !ok || len(tpl.Argv) != 1 {
				t.Fatalf("writer template %s missing or not a single invocation", f.caseID)
			}
			tpl.Env = append(tpl.Env, [2]string{"GOMAXPROCS", "1"})
			rel, ok := discovered[f.caseID+"|"+f.glob+"|"+f.fakeGit]
			if !ok {
				rel = discoverFaultPath(t, repo, tpl, f.glob, f.fakeGit)
				discovered[f.caseID+"|"+f.glob+"|"+f.fakeGit] = rel
			}
			ts := runTSWithFault(t, repo, tpl, f, rel)
			goRes := runGoWithFault(t, repo, tpl, f, rel)
			if o, ok := faultOrdinals[f.caseID+"/"+f.label+"/"+f.call]; ok && o.onlyCalls {
				if !injectedOrdinal(ts.trace, o.ts) || !injectedOrdinal(goRes.trace, o.goSide) {
					t.Fatalf("the injected %s was not call %d (TS) / %d (Go) of its trace, or a side aborted (wrong operation)\n--- TS\n%s--- Go\n%s", f.call, o.ts, o.goSide, ts.trace, goRes.trace)
				}
			}
			if ts.injected == 0 {
				t.Fatalf("TS made no %s call on %s to fail", f.call, rel)
			}
			switch {
			case faultOrdinals[f.caseID+"/"+f.label+"/"+f.call].from:
				o := faultOrdinals[f.caseID+"/"+f.label+"/"+f.call]
				if ts.injected != 2 || goRes.injected != 1 || !strings.Contains(firstInjected(ts.trace), o.onPath) || !strings.Contains(firstInjected(goRes.trace), o.onPath) {
					t.Errorf("mkdir from ordinal TS %d / Go %d: %d injected in TS, %d in Go (want 2 and 1, the first on %s)\n--- TS\n%s--- Go\n%s", o.ts, o.goSide, ts.injected, goRes.injected, o.onPath, ts.trace, goRes.trace)
				}
			case f.everyCall:
				// TS's seeded mkdtemp fails, then its real one.
				if ts.injected != 2 || goRes.injected != 1 || !strings.Contains(ts.trace, rel) || !strings.Contains(goRes.trace, rel) {
					t.Errorf("every %s failed: %d in TS, %d in Go (want 2 and 1, the first on %s)\n--- TS\n%s--- Go\n%s", f.call, ts.injected, goRes.injected, rel, ts.trace, goRes.trace)
				}
			case goRes.injected != ts.injected:
				t.Errorf("%s on %s injected %d times in TS, %d in Go", f.call, rel, ts.injected, goRes.injected)
			}
			compareWriter(t, f.name(), goRes.writerResult, asGolden(ts.writerResult))
			first, _, _ := strings.Cut(strings.TrimSpace(ts.Stderr+ts.Stdout), "\n")
			t.Logf("both exit %d, %d file(s) changed: %.160s", ts.Exit, len(ts.Files), first)
		})
	}
}

// faultResult is an invocation run under strace and the number of calls
// strace failed.
type faultResult struct {
	writerResult
	injected int
	// trace is the strace log, with the case root written <R>.
	trace string
}

func runTSWithFault(t *testing.T, repo string, tpl writerTemplate, f writerFault, rel string) faultResult {
	return runWithFault(t, repo, tpl, f, rel, tsCommand(repo), faultOrdinals[f.caseID+"/"+f.label+"/"+f.call].ts, true)
}

func runGoWithFault(t *testing.T, repo string, tpl writerTemplate, f writerFault, rel string) faultResult {
	return runWithFault(t, repo, tpl, f, rel, []string{flBinary}, faultOrdinals[f.caseID+"/"+f.label+"/"+f.call].goSide, false)
}

// stagedPid is the process id in a ledger binding's staged name
// (src/ledger-binding.ts:540 `.${basename}.${pid}.${uuid}.tmp`).
var stagedPid = regexp.MustCompile(`(\.[^./]+\.json)\.[0-9]+(\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.tmp)`)

// maskStagedPid masks the pid in staged names in text and renames any such
// file the invocation left in the workspace, so TS and Go trees compare.
func maskStagedPid(t *testing.T, r writerRoots, _ int, stdout, stderr string) (string, string) {
	t.Helper()
	filepath.WalkDir(r.ws, func(p string, d os.DirEntry, err error) error {
		if err == nil && stagedPid.MatchString(d.Name()) {
			if err := os.Rename(p, filepath.Join(filepath.Dir(p), stagedPid.ReplaceAllString(d.Name(), "$1.<PID>$2"))); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	return stagedPid.ReplaceAllString(stdout, "$1.<PID>$2"), stagedPid.ReplaceAllString(stderr, "$1.<PID>$2")
}

// runWithFault runs the case's invocation with command under strace -D (the
// tracee keeps the shell's pid, so a staged name holding the pid is known
// before it runs), failing the first call of f's group on rel.
func runWithFault(t *testing.T, repo string, tpl writerTemplate, f writerFault, rel string, command []string, ordinal int, tsSide bool) faultResult {
	t.Helper()
	if ordinal == 0 {
		ordinal = 1
	}
	trace := filepath.Join(t.TempDir(), "trace")
	calls := faultCalls[f.call]
	var root string
	run := writerRun{
		noVerify: true,
		mask:     maskStagedPid,
		command: func(r writerRoots, _ int, args []string) []string {
			root = r.root
			quote := func(p string) string {
				return "'" + strings.ReplaceAll(p, "<PID>", `'"$$"'`) + "'"
			}
			abs := filepath.Join(r.root, filepath.FromSlash(rel))
			script := "exec strace -D -f -qq -s 4096 -o " + quote(trace)
			when := "+" // every call from the ordinal on
			// Go makes the same seeded mkdir by name, on a thread that varies
			// between runs (when= counts per thread), so Go is faulted by path.
			if !f.everyCall && (!faultOrdinals[f.caseID+"/"+f.label+"/"+f.call].from || !tsSide) {
				when = ""
				script += " -P " + quote(abs)
				// A writer may name a workspace path relative to its cwd.
				if after, ok := strings.CutPrefix(rel, "ws/"); ok {
					script += " -P " + quote(after)
				}
			}
			script += " -e trace=" + calls + " -e inject=" + calls + ":error=" + f.errno + ":when=" + strconv.Itoa(ordinal) + when + " -- \"$@\""
			return withFakeGit(repo, f.fakeGit, append([]string{"sh", "-c", script, "sh"}, append(command, args...)...))
		},
	}
	res := runWriterCaseWith(t, repo, tpl, run)[0]
	b, _ := os.ReadFile(trace)
	return faultResult{res, strings.Count(string(b), "(INJECTED)"), strings.ReplaceAll(string(b), root+"/", "<R>/")}
}

// tracedPath is a quoted path argument in an strace line.
var tracedPath = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// discoverFaultPath runs the case under TS without a fault, tracing file
// syscalls, and returns the first path (relative to the case root) matching
// glob, with the process id in it written <PID>.
func discoverFaultPath(t *testing.T, repo string, tpl writerTemplate, glob, fakeGit string) string {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "trace")
	var found string
	var seen []string
	run := writerRun{
		noVerify: true,
		command: func(_ writerRoots, _ int, args []string) []string {
			traced := append([]string{"strace", "-f", "-qq", "-s", "4096", "-o", trace, "-e", "trace=" + discoveryCalls}, append(tsCommand(repo), args...)...)
			return withFakeGit(repo, fakeGit, traced)
		},
		mask: func(t *testing.T, r writerRoots, _ int, stdout, stderr string) (string, string) {
			file, err := os.Open(trace)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			scanner.Buffer(nil, 1<<20)
			pid := ""
			for scanner.Scan() && found == "" {
				line := scanner.Text()
				first, _, _ := strings.Cut(line, " ")
				if pid == "" {
					pid = first
				}
				for _, m := range tracedPath.FindAllStringSubmatch(line, -1) {
					p := m[1]
					if !filepath.IsAbs(p) {
						// Only fl's own cwd is the workspace (git children
						// chdir); a Node thread-pool call logs its thread id.
						if first != pid {
							continue
						}
						p = filepath.Join(r.ws, p)
					}
					rel, ok := strings.CutPrefix(p, r.root+"/")
					if !ok {
						continue
					}
					rel = strings.ReplaceAll(rel, "."+pid+".", ".<PID>.")
					if !strings.Contains(rel, ".git/") && !strings.Contains(rel, "/hooks/") && !strings.Contains(rel, "/objects/") {
						seen = append(seen, rel)
					}
					if ok, _ := path.Match(glob, rel); ok {
						found = rel
						break
					}
				}
			}
			return stdout, stderr
		},
	}
	res := runWriterCaseWith(t, repo, tpl, run)[0]
	if found == "" {
		t.Fatalf("%s: no traced path matches %s (exit %d)\n%s%s\ntraced: %s", tpl.ID, glob, res.Exit, res.Stdout, res.Stderr, strings.Join(slices.Compact(slices.Sorted(slices.Values(seen))), "\n"))
	}
	t.Logf("%s: faulting %s", tpl.ID, found)
	return found
}

// withFakeGit runs command with difftest/testdata/fakegit/<fake> first on
// PATH (the fake git fails one subcommand and runs the real git otherwise),
// as a case template's fakeGit does; with no fake it is command itself.
func withFakeGit(repo, fake string, command []string) []string {
	if fake == "" {
		return command
	}
	dir := filepath.Join(repo, "difftest", "testdata", "fakegit", fake)
	return append([]string{"sh", "-c", `PATH='` + dir + `':"$PATH"; export PATH; exec "$@"`, "sh"}, command...)
}
