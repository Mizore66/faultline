//go:build faultline_test

package difftest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
)

// Frozen-TypeScript oracle for guided `fl investigate --ci-log / --resume`
// (spec 2026-11-13-go-guided-investigate, Task 5A).
//
// Every case of difftest/testdata/guided-investigate/oracle/cases.json is run
// twice, sequentially, at ONE owned absolute workspace slot: first the frozen
// dist/cli.js on the pinned Node v22.22.2 (under the same seed preload as the
// writer goldens plus the review-page clock hook in clock.mjs), then the built
// Go fl. Draft.repository is hashed, so identical input paths are what make
// the full draft/witness/proof digests comparable without any digest mask.
// After each round the slot is moved, whole, into an evidence directory
// (FAULTLINE_GUIDED_EVIDENCE, else a test temp dir) so no round's bytes are
// discarded before the other round recreates the same inputs.
//
// Compared per step, exactly: complete stdout, complete stderr, exit status or
// raw signal, the scripted HTTP statuses, every docker/git/node argv the
// owned fake toolchain saw, and the tree (path, kind, mode, size, digest,
// symlink target) of the workspace, HOME and TMPDIR, with two stated
// exclusions: the permanent capture omits every ".git" directory, and the live
// tree compares each fixture repository's .git/index by its semantic content
// (ls-files --stage -v -z, run by the harness with the real git, not through
// the tool log) plus kind and mode, never by raw bytes (stat cache). The raw
// index files stay in the retained snapshots. The only
// normalization is each side's actual bound ephemeral loopback port
// (127.0.0.1:<port> -> 127.0.0.1:{PORT}). No root, digest, number, date or
// payload text is masked. The slot is identical, so no root mask exists.
//
// The frozen side is also tied to the committed capture: its exit, signal,
// scripted statuses, tool-call counts and stderr (slot-normalized only for
// this check, because the capture was taken at another slot) must equal the
// capture, so a drifted Node, dist or source is caught here as well.
//
// The tests need explicit oracle enablement (oracle.Skip, first); once
// enabled there is no further skip. The fake toolchain never executes a
// target-repository command and nothing here is native Docker proof: the
// continuation cases run against an owned fake docker and are reported as
// non-proof by the CLI itself.

var guidedPortRE = regexp.MustCompile(`127\.0\.0\.1:[0-9]+`)

type guidedPlan struct {
	root, slot string
	fakebox    string
	realGit    string
	node       string
	repo       string
	side       string // "ts" or "go": the side currently running, for failure retention
	port       string // bound loopback address of the step just run, empty when none
}

type guidedObservedStep struct {
	Name     string
	Exit     *int
	Signal   *string
	Stdout   string
	Stderr   string
	Statuses []string
	Tools    map[string][]string
	Tree     map[string]string
	Files    []guidedFileEntry // the slot tree after this step, .git directories excluded
	RawArgs  []string
}

// guidedFileEntry is one entry of the slot tree as the generator's
// snapshotTree sees it.
type guidedFileEntry struct {
	Path, Kind, Mode, Target string
	Bytes                    []byte
}

var guidedBuildFakebox = func() func(t *testing.T) string {
	var path string
	return func(t *testing.T) string {
		t.Helper()
		if path != "" {
			return path
		}
		dir, err := os.MkdirTemp("", "guided-fakebox-")
		if err != nil {
			t.Fatal(err)
		}
		name := "fakebox"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		out := filepath.Join(dir, name)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		build := exec.CommandContext(ctx, "go", "build", "-o", out, "./"+guidedOracleDir+"/fakebox")
		build.Dir = repoRoot(t)
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=")
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building the fake toolchain: %v\n%s", err, combined)
		}
		// Fix the helper mode before any hard link so the tools never depend on the umask.
		if err := os.Chmod(out, 0o755); err != nil {
			t.Fatal(err)
		}
		path = out
		return path
	}
}()

func guidedGit(t *testing.T, realGit, dir string, i int, args ...string) string {
	t.Helper()
	cmd := exec.Command(realGit, args...)
	cmd.Dir = dir
	date := []string{"2026-07-16T11:00:00Z", "2026-07-16T11:30:00Z"}[i]
	cmd.Env = []string{
		"PATH=" + filepath.Dir(realGit), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_AUTHOR_NAME=Oracle", "GIT_AUTHOR_EMAIL=oracle@example.invalid",
		"GIT_COMMITTER_NAME=Oracle", "GIT_COMMITTER_EMAIL=oracle@example.invalid",
		"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date,
	}
	for _, n := range []string{"SystemRoot", "SYSTEMROOT"} {
		if v, ok := os.LookupEnv(n); ok {
			cmd.Env = append(cmd.Env, n+"="+v)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func guidedBuildRepo(t *testing.T, realGit, dir string, fx guidedFixture) []string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	guidedGit(t, realGit, dir, 0, "init", "-q", "-b", "main")
	if len(fx.GitExclude) > 0 {
		info := filepath.Join(dir, ".git", "info")
		if err := os.MkdirAll(info, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(info, "exclude"), []byte(strings.Join(fx.GitExclude, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var shas []string
	for i, c := range fx.Commits {
		names := make([]string, 0, len(c.Files))
		for n := range c.Files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(c.Files[n]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		guidedGit(t, realGit, dir, i, "add", "-A")
		guidedGit(t, realGit, dir, i, "commit", "-q", "-m", c.Message, "--no-gpg-sign")
		shas = append(shas, guidedGit(t, realGit, dir, i, "rev-parse", "HEAD"))
	}
	return shas
}

type guidedSegment struct {
	Text   *string `json:"text"`
	Repeat string  `json:"repeat"`
	Count  int     `json:"count"`
	Hex    string  `json:"hex"`
}

func guidedFileBytes(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []byte(s)
	}
	var segs []guidedSegment
	if err := json.Unmarshal(raw, &segs); err != nil {
		t.Fatalf("bad file spec %s: %v", raw, err)
	}
	var out []byte
	for _, g := range segs {
		switch {
		case g.Text != nil:
			out = append(out, *g.Text...)
		case g.Repeat != "":
			out = append(out, bytes.Repeat([]byte(g.Repeat), g.Count)...)
		case g.Hex != "":
			b, err := hex.DecodeString(g.Hex)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, b...)
		default:
			t.Fatalf("bad file segment %+v", g)
		}
	}
	return out
}

func (p *guidedPlan) toolsDir() string { return p.slot + ".tools" }
func (p *guidedPlan) homeDir() string  { return p.slot + ".home" }
func (p *guidedPlan) tmpDir() string   { return p.slot + ".tmp" }

// build recreates the case inputs at the slot and returns the commit ids.
func (p *guidedPlan) build(t *testing.T, cf *guidedCaseFile, c guidedCase) []string {
	t.Helper()
	for _, s := range []string{p.slot, p.toolsDir(), p.homeDir(), p.tmpDir()} {
		if _, err := os.Lstat(s); err == nil {
			t.Fatalf("slot path %s exists before %s", s, c.ID)
		}
	}
	for _, d := range []string{p.slot, p.homeDir(), p.tmpDir(), filepath.Join(p.slot, "in"), p.toolsDir()} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fx := cf.Fixtures[c.Fixture]
	shas := guidedBuildRepo(t, p.realGit, filepath.Join(p.slot, "repo"), fx)
	for _, extra := range c.ExtraRepos {
		guidedBuildRepo(t, p.realGit, filepath.Join(p.slot, extra), fx)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	box, err := os.ReadFile(p.fakebox)
	if err != nil {
		t.Fatal(err)
	}
	boxSum := guidedSha256(box)
	names := []string{"git"}
	if !c.Tools.NoNode {
		names = append(names, "node")
	}
	if !c.Tools.NoDocker {
		names = append(names, "docker")
	}
	for _, n := range names {
		// One immutable helper file; each tool name is a hard link to it with a plain copy as the
		// fallback. The helper never writes itself (it appends only to <tool>.log) and no compared
		// value includes an inode or link count.
		dst := filepath.Join(p.toolsDir(), n+ext)
		if err := os.Link(p.fakebox, dst); err != nil {
			if err := os.WriteFile(dst, box, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dst, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := os.ReadFile(dst); err != nil || guidedSha256(got) != boxSum {
			t.Fatalf("tool %s bytes differ from the helper: %v", n, err)
		}
		// POSIX: exactly 0755. Windows has no Unix permission bits (Mode().Perm() is synthetic), so
		// there the tool must be a regular file with the helper's bytes (checked above) and it is
		// proven executable by the cases that actually run it. Native Windows runtime is still pending.
		info, err := os.Lstat(dst)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o755) {
			t.Fatalf("tool %s is not a regular mode-755 file: %v", n, err)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(p.toolsDir(), name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("real-git", p.realGit)
	if c.Tools.NodeVersion != "" {
		write("node-version", c.Tools.NodeVersion)
	}
	if c.Tools.DockerDown {
		write("docker-down", "")
	}
	if c.Tools.ShowFails {
		write("show-fails", "")
	}
	names2 := make([]string, 0, len(c.Files))
	for n := range c.Files {
		names2 = append(names2, n)
	}
	sort.Strings(names2)
	for _, n := range names2 {
		path := filepath.Join(p.slot, "in", filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, guidedFileBytes(t, c.Files[n]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for n, text := range c.RepoFiles {
		path := filepath.Join(p.slot, "repo", filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		expanded := p.expandAs(t, cf, text, shas, strings.HasSuffix(n, ".json"))
		if err := os.WriteFile(path, []byte(expanded), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return shas
}

var guidedImageRE = regexp.MustCompile(`\{IMAGE:([a-z]+)\}`)
var guidedDigestRE = regexp.MustCompile(`\{DIGEST:([a-z0-9-]+)\}`)

func (p *guidedPlan) expand(t *testing.T, cf *guidedCaseFile, v string, shas []string) string {
	t.Helper()
	return p.expandAs(t, cf, v, shas, false)
}

// expandAs is expand; asJSON is for text that is itself a JSON document (a repoFiles *.json), where the
// owned paths are JSON-string-escaped exactly like the generator does.
func (p *guidedPlan) expandAs(t *testing.T, cf *guidedCaseFile, v string, shas []string, asJSON bool) string {
	t.Helper()
	esc := func(x string) string { return x }
	if asJSON {
		esc = guidedJSONEscaped
	}
	r := strings.NewReplacer(
		"{SLOT}", esc(p.slot), "{IN}", esc(filepath.Join(p.slot, "in")), "{REPO}", esc(filepath.Join(p.slot, "repo")),
		"{OTHER}", esc(filepath.Join(p.slot, "other")), "{C1}", shas[0], "{C2}", shas[1])
	v = r.Replace(v)
	v = guidedImageRE.ReplaceAllStringFunc(v, func(m string) string { return cf.Images[guidedImageRE.FindStringSubmatch(m)[1]] })
	v = guidedDigestRE.ReplaceAllStringFunc(v, func(m string) string {
		id := guidedDigestRE.FindStringSubmatch(m)[1]
		b, err := os.ReadFile(filepath.Join(p.slot, "repo", ".faultline", "witnesses", "frozen", id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var rec struct {
			FrozenDigest string `json:"frozenDigest"`
		}
		if err := json.Unmarshal(b, &rec); err != nil {
			t.Fatal(err)
		}
		return rec.FrozenDigest
	})
	return v
}

func (p *guidedPlan) env(kind string, c guidedCase, st guidedStep) []string {
	env := []string{
		"PATH=" + p.toolsDir(), "HOME=" + p.homeDir(), "USERPROFILE=" + p.homeDir(),
		"TMPDIR=" + p.tmpDir(), "TEMP=" + p.tmpDir(), "TMP=" + p.tmpDir(),
		"LC_ALL=C.UTF-8", "LANGUAGE=", "FAULTLINE_TEST_SEED=guided-investigate-oracle-v1", "FAULTLINE_NO_BROWSER=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	}
	for _, name := range []string{"SystemRoot", "SYSTEMROOT", "PATHEXT", "ComSpec"} {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	// Coverage counters belong to the instrumented Go binary only.
	if kind == "go" {
		if dir := os.Getenv("GOCOVERDIR"); dir != "" {
			env = append(env, "GOCOVERDIR="+dir)
		}
	}
	for _, m := range []map[string]string{c.Env, st.Env} {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, k+"="+m[k])
		}
	}
	return env
}

const guidedStepTimeout = 90 * time.Second

var guidedReadyRE = regexp.MustCompile(`^FaultLine guided investigate: PREFLIGHT\nFaultLine guided investigate: INTAKE \((?:ci-log|resume [^\n]*)\)\nFaultLine guided investigate: WITNESS_REVIEW\nFaultLine local witness review: (http://127\.0\.0\.1:[1-9][0-9]*)\nApprove, then Freeze \(separate clicks\)\. This command continues automatically after freeze\. Press Ctrl\+C to cancel and resume later with --resume <id>\.\n$`)

var (
	guidedTokenRE  = regexp.MustCompile(`name="token" value="([0-9a-f]{64})"`)
	guidedDigestFd = regexp.MustCompile(`name="reviewDigest" value="(sha256:[0-9a-f]{64})"`)
)

func guidedHTTP(method, target, body string) (int, string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, target, rd)
	if err != nil {
		return 0, "", err
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), err
}

// runStep runs one step of one side to completion, driving the review page
// through the case's script, and returns its raw observation.
func (p *guidedPlan) runStep(t *testing.T, kind string, cf *guidedCaseFile, c guidedCase, st guidedStep, shas []string, helper string) (stdout, stderr string, exit *int, signal *string, statuses []string) {
	t.Helper()
	args := make([]string, len(st.Args))
	for i, a := range st.Args {
		args[i] = p.expand(t, cf, a, shas)
	}
	cwd := filepath.Join(p.slot, "in")
	if st.Cwd != "" {
		cwd = p.expand(t, cf, st.Cwd, shas)
	}
	p.port = ""
	exe, prefix := flBinary, []string(nil)
	if kind == "ts" {
		repo := p.repo
		exe = p.node
		prefix = []string{"--import", filepath.Join(repo, "difftest", "gen", "seed.mjs"), "--import", filepath.Join(repo, guidedOracleDir, "clock.mjs"), filepath.Join(repo, "dist", "cli.js")}
	}
	env := p.env(kind, c, st)
	ctx, cancel := context.WithTimeout(context.Background(), guidedStepTimeout)
	defer cancel()
	if len(st.Script) == 0 {
		cmd := exec.CommandContext(ctx, exe, append(append([]string{}, prefix...), args...)...)
		cmd.Dir, cmd.Env = cwd, env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		cmd.WaitDelay = 5 * time.Second
		err := cmd.Run()
		if ctx.Err() != nil {
			t.Fatalf("%s %s/%s did not exit: stdout=%q stderr=%q", kind, c.ID, st.Name, out.String(), errb.String())
		}
		var ee *exec.ExitError
		if err != nil && !asExit(err, &ee) {
			t.Fatalf("%s %s/%s: %v", kind, c.ID, st.Name, err)
		}
		code := cmd.ProcessState.ExitCode()
		return out.String(), errb.String(), &code, nil, nil
	}
	started, err := servefixture.StartWithOptions(ctx, exe, prefix, args, env, cwd, servefixture.StartOptions{
		StartupLines: 5, StartupDescription: "guided review readiness", ReadyStream: "stderr",
		ValidateStartup: func(out []byte) (string, error) {
			m := guidedReadyRE.FindStringSubmatch(string(out))
			if m == nil {
				return "", fmt.Errorf("not the exact readiness stderr: %q", out)
			}
			return m[1], nil
		},
	})
	if err != nil {
		t.Fatalf("%s %s/%s: start: %v", kind, c.ID, st.Name, err)
	}
	t.Cleanup(func() {
		if err := started.Dispose(); err != nil {
			t.Errorf("%s %s/%s teardown: %v", kind, c.ID, st.Name, err)
		}
	})
	var page string
	for _, a := range st.Script {
		switch a.Do {
		case "ready":
			statuses = append(statuses, "ready")
		case "get":
			status, body, err := guidedHTTP(http.MethodGet, started.URL+"/", "")
			if err != nil || status != a.Expect {
				t.Fatalf("%s %s/%s: GET status=%d err=%v", kind, c.ID, st.Name, status, err)
			}
			page = body
			statuses = append(statuses, "get:"+strconv.Itoa(status))
		case "post":
			tok, dig := guidedTokenRE.FindStringSubmatch(page), guidedDigestFd.FindStringSubmatch(page)
			if tok == nil || dig == nil {
				t.Fatalf("%s %s/%s: no form fields on the last page", kind, c.ID, st.Name)
			}
			token := tok[1]
			if a.Token != "" {
				token = a.Token
			}
			form := url.Values{"token": {token}, "reviewDigest": {dig[1]}}
			if a.Path != "/freeze" {
				form.Set("approvedBy", cf.ApprovedBy)
				form.Set("acknowledged", "exact-witness-reviewed")
			}
			if a.Omit != "" {
				form.Del(a.Omit)
			}
			status, body, err := guidedHTTP(http.MethodPost, started.URL+a.Path, form.Encode())
			if err != nil || status != a.Expect {
				t.Fatalf("%s %s/%s: POST %s status=%d err=%v body=%.300s", kind, c.ID, st.Name, a.Path, status, err, body)
			}
			rec := "post:" + a.Path + ":" + strconv.Itoa(status)
			if a.Message != "" {
				if !strings.Contains(body, a.Message) {
					t.Fatalf("%s %s/%s: response lacks %q", kind, c.ID, st.Name, a.Message)
				}
				rec += ":message"
			}
			statuses = append(statuses, rec)
		case "sigint":
			if err := started.Interrupt(ctx, helper); err != nil {
				t.Fatalf("%s %s/%s: interrupt: %v", kind, c.ID, st.Name, err)
			}
			statuses = append(statuses, "sigint")
		case "sigterm":
			if err := started.Terminate(ctx); err != nil {
				t.Fatalf("%s %s/%s: terminate: %v", kind, c.ID, st.Name, err)
			}
			statuses = append(statuses, "sigterm")
		default:
			t.Fatalf("unknown action %q", a.Do)
		}
	}
	res, err := started.Wait(ctx)
	if err != nil {
		t.Fatalf("%s %s/%s: wait: %v", kind, c.ID, st.Name, err)
	}
	// The ephemeral port is the one observed readiness URL of this very run.
	port := strings.TrimPrefix(started.URL, "http://")
	p.port = port
	res.Stdout, res.Stderr = strings.ReplaceAll(res.Stdout, port, "127.0.0.1:{PORT}"), strings.ReplaceAll(res.Stderr, port, "127.0.0.1:{PORT}")
	return res.Stdout, res.Stderr, res.ExitCode, res.Signal, statuses
}

// normalizePort applies the generator's single tool-record normalization: this step's own
// bound loopback address becomes {PORT}.
func (p *guidedPlan) normalizePort(logs map[string][]string) map[string][]string {
	if p.port == "" {
		return logs
	}
	for k, recs := range logs {
		for i := range recs {
			recs[i] = strings.ReplaceAll(recs[i], p.port, "127.0.0.1:{PORT}")
		}
		logs[k] = recs
	}
	return logs
}

func asExit(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// guidedTree describes the slot (without .git), HOME and TMPDIR.
func (p *guidedPlan) tree(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, part := range []struct{ prefix, dir string }{{"ws", p.slot}, {"home", p.homeDir()}, {"tmp", p.tmpDir()}} {
		sub := treeEntries(t, part.dir, func(rel string, b []byte) string {
			sum := sha256.Sum256(b)
			return fmt.Sprintf("file %d %s", len(b), hex.EncodeToString(sum[:]))
		})
		for k, v := range sub {
			// The one excluded entry: a fixture repository's index holds
			// inode/mtime stat data of the build, not command output. Every
			// other repository entry (objects, refs, config, hooks) is compared.
			if part.prefix == "ws" && (k == "repo/.git/index" || k == "other/.git/index") {
				out["ws/"+k] = p.indexView(t, k)
				continue
			}
			out[part.prefix+"/"+k] = v
		}
	}
	return out
}

// indexView describes a fixture repository's index without its stat cache:
// kind and mode of the file plus the digest of "ls-files --stage -v -z" (mode,
// object id, stage, tag flags and path of every entry), observed with the
// absolute real git. A changed entry or flag is caught; inode and mtime data
// are not compared.
func (p *guidedPlan) indexView(t *testing.T, rel string) string {
	t.Helper()
	repoDir := filepath.Join(p.slot, filepath.FromSlash(strings.TrimSuffix(rel, "/.git/index")))
	info, err := os.Lstat(filepath.Join(repoDir, ".git", "index"))
	if err != nil {
		t.Fatalf("index %s: %v", rel, err)
	}
	cmd := exec.Command(p.realGit, "ls-files", "--stage", "-v", "-z")
	cmd.Dir = repoDir
	cmd.Env = []string{"PATH=" + filepath.Dir(p.realGit), "HOME=" + repoDir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_OPTIONAL_LOCKS=0"}
	for _, n := range []string{"SystemRoot", "SYSTEMROOT"} {
		if v, ok := os.LookupEnv(n); ok {
			cmd.Env = append(cmd.Env, n+"="+v)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ls-files in %s: %v", repoDir, err)
	}
	return fmt.Sprintf("index %v %o %s", info.Mode().IsRegular(), info.Mode().Perm(), guidedSha256(out))
}

// guidedCaseSlot is the workspace path of one case: unique per case, the
// same for the TS and the Go side of that case.
func guidedCaseSlot(work, caseID string) string { return filepath.Join(work, "slot-"+caseID) }

// guidedRetainFailed moves whatever of a side's slot, .tools, .home and .tmp
// still exists into evidence/<side>-failed[.N]<suffix>. It never overwrites
// and never deletes; a failed move is logged and the data stays in place.
func guidedRetainFailed(t *testing.T, slot, evidence, side string) {
	t.Helper()
	if err := os.MkdirAll(evidence, 0o755); err != nil {
		t.Logf("failed-side data stays at %s: %v", slot, err)
		return
	}
	n := 0
	for {
		clash := false
		for _, suffix := range []string{"", ".tools", ".home", ".tmp"} {
			if _, err := os.Lstat(filepath.Join(evidence, guidedFailedName(side, n)+suffix)); err == nil {
				clash = true
			}
		}
		if !clash {
			break
		}
		n++
	}
	for _, suffix := range []string{"", ".tools", ".home", ".tmp"} {
		if _, err := os.Lstat(slot + suffix); err != nil {
			continue
		}
		dst := filepath.Join(evidence, guidedFailedName(side, n)+suffix)
		if err := os.Rename(slot+suffix, dst); err != nil {
			t.Logf("failed-side data stays at %s (retain failed: %v)", slot+suffix, err)
			continue
		}
		t.Logf("retained failed %s data: %s", side, dst)
	}
}

func guidedFailedName(side string, n int) string {
	if n == 0 {
		return side + "-failed"
	}
	return fmt.Sprintf("%s-failed.%d", side, n)
}

func (p *guidedPlan) toolLogs(offsets map[string]int) map[string][]string {
	logs := map[string][]string{}
	for _, tool := range []string{"docker", "git", "node"} {
		b, _ := os.ReadFile(filepath.Join(p.toolsDir(), tool+".log"))
		text := string(b)
		fresh := text[offsets[tool]:]
		offsets[tool] = len(text)
		var records []string
		if fresh != "" {
			records = strings.Split(strings.TrimSuffix(fresh, "\x1e"), "\x1e")
		}
		logs[tool] = records
	}
	return logs
}

func guidedCheckExpect(t *testing.T, kind string, c guidedCase, st guidedStep, stdout, stderr string, exit *int, signal *string) {
	t.Helper()
	e := st.Expect
	where := fmt.Sprintf("%s %s/%s", kind, c.ID, st.Name)
	if e.ExitOrSignal {
		if exit == nil && signal == nil {
			t.Fatalf("%s: neither exit nor signal", where)
		}
	} else if e.Exit != nil && (exit == nil || *exit != *e.Exit) {
		t.Fatalf("%s: exit %v signal %v, want %d\nstdout=%.400q\nstderr=%.800q", where, exit, signal, *e.Exit, stdout, stderr)
	}
	if e.Stdout != nil && stdout != *e.Stdout {
		t.Fatalf("%s: stdout %q, want %q", where, stdout, *e.Stdout)
	}
	for _, s := range e.StdoutContains {
		if !strings.Contains(stdout, s) {
			t.Fatalf("%s: stdout lacks %q", where, s)
		}
	}
	for _, s := range e.StderrContains {
		if !strings.Contains(stderr, s) {
			t.Fatalf("%s: stderr lacks %q: %.800q", where, s, stderr)
		}
	}
}

func guidedRetain(t *testing.T, p *guidedPlan, evidence, side string) {
	t.Helper()
	if err := os.MkdirAll(evidence, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, part := range []struct{ suffix string }{{""}, {".tools"}, {".home"}, {".tmp"}} {
		dst := filepath.Join(evidence, side+part.suffix)
		if err := os.Rename(p.slot+part.suffix, dst); err != nil {
			t.Fatalf("retaining %s: %v", side+part.suffix, err)
		}
	}
}

// guidedRun executes one case on one side and returns every step's observation.
func guidedRun(t *testing.T, p *guidedPlan, kind string, cf *guidedCaseFile, c guidedCase, helper string, wantCommits []string) []guidedObservedStep {
	t.Helper()
	shas := p.build(t, cf, c)
	if wantCommits != nil && strings.Join(shas, ",") != strings.Join(wantCommits, ",") {
		t.Fatalf("%s %s: fixture commits %v differ from the captured fixture %v", kind, c.ID, shas, wantCommits)
	}
	offsets := map[string]int{}
	var steps []guidedObservedStep
	for _, st := range c.Steps {
		stdout, stderr, exit, signal, statuses := p.runStep(t, kind, cf, c, st, shas, helper)
		guidedCheckExpect(t, kind, c, st, stdout, stderr, exit, signal)
		args := make([]string, len(st.Args))
		for i, a := range st.Args {
			args[i] = p.expand(t, cf, a, shas)
		}
		steps = append(steps, guidedObservedStep{
			Name: st.Name, Exit: exit, Signal: signal, Stdout: stdout, Stderr: stderr, Statuses: statuses,
			Tools: p.normalizePort(p.toolLogs(offsets)), Tree: p.tree(t), Files: p.snapshot(t), RawArgs: args,
		})
	}
	return steps
}

// guidedCompareGit compares every git argv record exactly and in order. No
// call is exempt: a Go review page that omits any read-only range call the
// frozen server makes is a parity failure.
func guidedCompareGit(t *testing.T, where string, a, b guidedObservedStep) {
	t.Helper()
	if strings.Join(a.Tools["git"], "\x1e") != strings.Join(b.Tools["git"], "\x1e") {
		t.Errorf("%s: git argv differ: %s", where, guidedFirstRecordDiff(a.Tools["git"], b.Tools["git"]))
	}
}

// guidedFirstRecordDiff names the first differing tool-call record, showing
// only the arguments after git's fixed hardening prefix.
func guidedFirstRecordDiff(a, b []string) string {
	short := func(r string) string {
		args := strings.Split(r, "\x1f")
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "core.autocrlf=false" {
				args = args[i+1:]
				break
			}
		}
		return fmt.Sprintf("%q", args)
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		switch {
		case i >= len(a):
			return fmt.Sprintf("record %d only in Go (%d vs %d records): %s", i, len(a), len(b), short(b[i]))
		case i >= len(b):
			return fmt.Sprintf("record %d only in TS (%d vs %d records): %s", i, len(a), len(b), short(a[i]))
		case a[i] != b[i]:
			return fmt.Sprintf("record %d differs (%d vs %d records)\n  ts: %s\n  go: %s", i, len(a), len(b), short(a[i]), short(b[i]))
		}
	}
	return "identical"
}

func guidedPtrString[T any](v *T) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprint(*v)
}

func guidedCompare(t *testing.T, c guidedCase, ts, goSide []guidedObservedStep) {
	t.Helper()
	for i := range ts {
		a, b := ts[i], goSide[i]
		where := fmt.Sprintf("%s/%s", c.ID, a.Name)
		if guidedPtrString(a.Exit) != guidedPtrString(b.Exit) || guidedPtrString(a.Signal) != guidedPtrString(b.Signal) {
			t.Errorf("%s: exit/signal ts=%s/%s go=%s/%s", where, guidedPtrString(a.Exit), guidedPtrString(a.Signal), guidedPtrString(b.Exit), guidedPtrString(b.Signal))
		}
		if a.Stdout != b.Stdout {
			t.Errorf("%s: stdout differs\nts: %q\ngo: %q", where, a.Stdout, b.Stdout)
		}
		if a.Stderr != b.Stderr {
			t.Errorf("%s: stderr differs\nts: %q\ngo: %q", where, a.Stderr, b.Stderr)
		}
		if strings.Join(a.Statuses, "|") != strings.Join(b.Statuses, "|") {
			t.Errorf("%s: scripted statuses ts=%v go=%v", where, a.Statuses, b.Statuses)
		}
		for _, tool := range []string{"docker", "node"} {
			if strings.Join(a.Tools[tool], "\x1e") != strings.Join(b.Tools[tool], "\x1e") {
				t.Errorf("%s: %s argv differ: %s", where, tool, guidedFirstRecordDiff(a.Tools[tool], b.Tools[tool]))
			}
		}
		guidedCompareGit(t, where, a, b)
		for k, v := range a.Tree {
			if b.Tree[k] != v {
				t.Errorf("%s: tree entry %s: ts %q go %q", where, k, v, b.Tree[k])
			}
		}
		for k, v := range b.Tree {
			if _, ok := a.Tree[k]; !ok {
				t.Errorf("%s: tree entry %s only in Go: %q", where, k, v)
			}
		}
	}
}

// snapshot mirrors the generator's snapshotTree: every entry under the slot
// in byte order, no entry named ".git" (the fixture repositories' own
// metadata), symlinks unfollowed.
func (p *guidedPlan) snapshot(t *testing.T) []guidedFileEntry {
	t.Helper()
	var out []guidedFileEntry
	var walk func(rel string)
	walk = func(rel string) {
		entries, err := os.ReadDir(filepath.Join(p.slot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		sort.Strings(names) // ASCII names; byte order
		for _, name := range names {
			if name == ".git" {
				continue
			}
			r := name
			if rel != "" {
				r = rel + "/" + name
			}
			full := filepath.Join(p.slot, filepath.FromSlash(r))
			info, err := os.Lstat(full)
			if err != nil {
				t.Fatal(err)
			}
			// Mode is compared on every platform. The capture holds darwin permission bits; Windows reports
			// synthetic bits, so a Windows run is unverified and may fail here (it is not skipped or blanked).
			mode := strconv.FormatUint(uint64(info.Mode().Perm()), 8)
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(full)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, guidedFileEntry{Path: r, Kind: "symlink", Mode: mode, Target: target})
			case info.IsDir():
				out = append(out, guidedFileEntry{Path: r, Kind: "dir", Mode: mode})
				walk(r)
			default:
				b, err := os.ReadFile(full)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, guidedFileEntry{Path: r, Kind: "file", Mode: mode, Bytes: b})
			}
		}
	}
	walk("")
	return out
}

// guidedJSONEscaped is the slot as the inside of a JSON string, exactly as
// JSON.stringify writes it (no HTML escaping).
func guidedJSONEscaped(slot string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(slot)
	return strings.TrimSuffix(strings.TrimSuffix(buf.String(), "\n"), `"`)[1:]
}

// guidedRef is one expected slot-derived value and the stored token naming it.
type guidedRef struct{ Literal, Token string }

// guidedExpectedRefs derives, for this replay's own slot, every value the
// frozen templates name: first each "sha256:<hex>" digest, then each bare
// file hash, both in template-id order (the generator's order). The only
// inputs are the frozen template text and the slot; no Go-produced record or
// builder is read.
func guidedExpectedRefs(slot string, templates []guidedTemplate) ([]guidedRef, error) {
	resolved, err := resolveGuidedTemplates(slot, templates)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resolved))
	for id := range resolved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var refs []guidedRef
	for _, id := range ids {
		refs = append(refs, guidedRef{"sha256:" + resolved[id].Digest, "{DIGEST:" + id + "}"})
	}
	for _, id := range ids {
		if resolved[id].File != "" {
			refs = append(refs, guidedRef{resolved[id].File, "{FILE:" + id + "}"})
		}
	}
	return refs, nil
}

// guidedStoredForm rewrites an observed text into the capture's stored form:
// the owned slot becomes {SLOT} (or {SLOTJ} inside JSON files) and each
// expected slot-derived value becomes its named reference. It is the exact
// inverse of what the generator applied and masks nothing else.
func guidedStoredForm(text, slot string, json bool, refs []guidedRef) string {
	if json {
		text = strings.ReplaceAll(text, guidedJSONEscaped(slot), "{SLOTJ}")
	} else {
		text = strings.ReplaceAll(text, slot, "{SLOT}")
	}
	for _, r := range refs {
		text = strings.ReplaceAll(text, r.Literal, r.Token)
	}
	return text
}

type guidedStoredEntry struct {
	Path, Kind, Mode, Target, Content, Base64 string
	Size                                      int
	Sha256                                    string
}

func guidedStoredTree(files []guidedFileEntry, slot string, digests []guidedRef) []guidedStoredEntry {
	out := make([]guidedStoredEntry, len(files))
	for i, f := range files {
		e := guidedStoredEntry{Path: f.Path, Kind: f.Kind, Mode: f.Mode, Target: guidedStoredForm(f.Target, slot, false, nil)}
		if f.Kind == "file" {
			if utf8.Valid(f.Bytes) {
				e.Content = guidedStoredForm(string(f.Bytes), slot, strings.HasSuffix(f.Path, ".json"), digests)
				e.Size, e.Sha256 = len(e.Content), guidedSha256([]byte(e.Content))
			} else {
				e.Base64 = base64.StdEncoding.EncodeToString(f.Bytes)
				e.Size, e.Sha256 = len(f.Bytes), guidedSha256(f.Bytes)
			}
		}
		out[i] = e
	}
	return out
}

func guidedStoredTreeHash(entries []guidedStoredEntry) string {
	conv := make([]guidedTreeEntry, len(entries))
	for i, e := range entries {
		conv[i] = guidedTreeEntry{Path: e.Path, Kind: e.Kind, Mode: e.Mode, Size: e.Size, Sha256: e.Sha256, Target: e.Target}
	}
	return guidedCompactTreeHash(conv)
}

// guidedCheckCapture replays one side's complete observation against the
// frozen capture for this replay's own owned slot. For FULL captures the
// complete stdout, stderr, args, every tool argv record and the complete
// workspace tree (path, kind, mode, size, digest, bytes) must equal the
// capture expanded through the frozen templates. The loader accepts only FULL
// captures, so there is no declaration-only comparison.
func guidedCheckCapture(t *testing.T, p *guidedPlan, side string, capture guidedCapture, got []guidedObservedStep) {
	t.Helper()
	diffs, err := guidedCompareCapture(p.slot, side, capture, got)
	if err != nil {
		t.Fatalf("%s %s: %v", side, capture.CaseID, err)
	}
	for _, d := range diffs {
		t.Error(d)
	}
}

// guidedCompareCapture is the comparator itself: it returns one message per
// difference between the observation and the capture (none means exact
// equality) and an error only when the capture's template graph cannot be
// expanded. It is separate from guidedCheckCapture so a control can drive the
// very same code on a tampered observation.
func guidedCompareCapture(slot, side string, capture guidedCapture, got []guidedObservedStep) ([]string, error) {
	digests, err := guidedExpectedRefs(slot, capture.Templates)
	if err != nil {
		return nil, err
	}
	var diffs []string
	errorf := func(format string, args ...any) { diffs = append(diffs, fmt.Sprintf(format, args...)) }
	if len(got) != len(capture.Steps) {
		errorf("%s %s: %d steps observed, capture %d", side, capture.CaseID, len(got), len(capture.Steps))
		return diffs, nil
	}
	for i, step := range got {
		want := capture.Steps[i]
		where := side + " " + capture.CaseID + "/" + step.Name
		if guidedPtrString(step.Exit) != guidedPtrString(want.Exit) || guidedPtrString(step.Signal) != guidedPtrString(want.Signal) {
			errorf("%s: exit/signal %s/%s, capture %s/%s", where, guidedPtrString(step.Exit), guidedPtrString(step.Signal), guidedPtrString(want.Exit), guidedPtrString(want.Signal))
		}
		stderr := guidedStoredForm(step.Stderr, slot, false, digests)
		if stderr != want.Stderr {
			errorf("%s: stderr differs from the capture\nobserved: %q\ncapture:  %q", where, stderr, want.Stderr)
		}
		var wantStatuses []string
		for _, a := range want.Actions {
			switch a.Do {
			case "ready", "sigint", "sigterm":
				wantStatuses = append(wantStatuses, a.Do)
			case "get":
				wantStatuses = append(wantStatuses, "get:"+strconv.Itoa(a.Status))
			case "post":
				s := "post:" + a.Path + ":" + strconv.Itoa(a.Status)
				if a.Message != nil {
					s += ":message"
				}
				wantStatuses = append(wantStatuses, s)
			}
		}
		if strings.Join(step.Statuses, "|") != strings.Join(wantStatuses, "|") {
			errorf("%s: scripted statuses %v, capture %v", where, step.Statuses, wantStatuses)
		}
		if stdout := guidedStoredForm(step.Stdout, slot, false, digests); stdout != want.Stdout {
			errorf("%s: stdout differs from the capture\nobserved: %q\ncapture:  %q", where, stdout, want.Stdout)
		}
		if len(step.RawArgs) != len(want.Args) {
			errorf("%s: %d arguments, capture %d", where, len(step.RawArgs), len(want.Args))
		}
		for j, a := range step.RawArgs {
			if j >= len(want.Args) || guidedStoredForm(a, slot, false, digests) != want.Args[j] {
				errorf("%s: argument %d differs from the capture", where, j)
			}
		}
		for _, tool := range []string{"docker", "git", "node"} {
			var stored []string
			for _, r := range step.Tools[tool] {
				stored = append(stored, guidedStoredForm(r, slot, false, digests))
			}
			if strings.Join(stored, "\x1e") != strings.Join(want.Tools[tool], "\x1e") {
				errorf("%s: %s argv differ from the capture: %s", where, tool, guidedFirstRecordDiff(stored, want.Tools[tool]))
			}
		}
		tree := guidedStoredTree(step.Files, slot, digests)
		if h := guidedStoredTreeHash(tree); h != want.TreeHash {
			errorf("%s: workspace tree after the step hashes %s, capture %s", where, h, want.TreeHash)
		}
	}
	final := guidedStoredTree(got[len(got)-1].Files, slot, digests)
	if len(final) != len(capture.FinalTree) {
		errorf("%s %s: final tree has %d entries, capture %d", side, capture.CaseID, len(final), len(capture.FinalTree))
	}
	for i := 0; i < len(final) && i < len(capture.FinalTree); i++ {
		g, w := final[i], capture.FinalTree[i]
		if g.Path != w.Path || g.Kind != w.Kind || g.Target != w.Target || g.Content != w.Content || g.Base64 != w.Base64 || g.Size != w.Size || g.Sha256 != w.Sha256 || g.Mode != w.Mode {
			errorf("%s %s: final tree entry %d differs\nobserved: %+v\ncapture:  %+v", side, capture.CaseID, i, g, w)
			break
		}
	}
	// Every template must be witnessed: at least one of its values (digest or file hash) occurs in the
	// observed output (a tree file or any step's stdout/stderr); otherwise the template is stale or foreign.
	var seen strings.Builder
	for _, f := range got[len(got)-1].Files {
		if f.Kind == "file" {
			seen.Write(f.Bytes)
			seen.WriteByte(0)
		}
	}
	for _, st := range got {
		seen.WriteString(st.Stdout)
		seen.WriteString(st.Stderr)
	}
	witnessed := map[string]bool{}
	for _, r := range digests {
		id := r.Token[strings.Index(r.Token, ":")+1 : len(r.Token)-1]
		witnessed[id] = witnessed[id] || strings.Contains(seen.String(), r.Literal)
	}
	for id, ok := range witnessed {
		if !ok {
			errorf("%s %s: no value of frozen template %s occurs in the observed output", side, capture.CaseID, id)
		}
	}
	return diffs, nil
}

func runGuidedGroup(t *testing.T, group string) {
	t.Helper()
	oracle.Skip(t)
	root, dir := guidedCorpusDir(t)
	corpus, err := loadGuidedCorpus(root, dir)
	if err != nil {
		t.Fatalf("corpus rejected: %v", err)
	}
	node := oracleNode(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	realGit, err = filepath.Abs(realGit)
	if err != nil {
		t.Fatal(err)
	}
	// Evidence is never deleted by the harness: the default root is a persistent
	// os.MkdirTemp directory, and the per-case slots live inside it (same
	// filesystem, so retention is a rename).
	evidenceRoot := os.Getenv("FAULTLINE_GUIDED_EVIDENCE")
	if evidenceRoot == "" {
		if evidenceRoot, err = os.MkdirTemp("", "guided-evidence-"); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(evidenceRoot, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	helper := liveConsoleHelper(t)
	plan := &guidedPlan{root: work, fakebox: guidedBuildFakebox(t), realGit: realGit, node: node, repo: root}
	ran := 0
	for _, c := range corpus.Cases.Cases {
		if c.Group != group {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
			capture := corpus.Captures[c.ID]
			plan.slot = guidedCaseSlot(work, c.ID)
			// Registered before any process cleanup (cleanups run last-in first), so a
			// failed side is retained only after its children were disposed.
			t.Cleanup(func() { guidedRetainFailed(t, plan.slot, filepath.Join(evidenceRoot, c.ID), plan.side) })
			plan.side = "ts"
			ts := guidedRun(t, plan, "ts", &corpus.Cases, c, helper, capture.Fixture.Commits)
			guidedRetain(t, plan, filepath.Join(evidenceRoot, c.ID), "ts")
			plan.side = "go"
			gs := guidedRun(t, plan, "go", &corpus.Cases, c, helper, capture.Fixture.Commits)
			guidedRetain(t, plan, filepath.Join(evidenceRoot, c.ID), "go")
			guidedCompare(t, c, ts, gs)
			guidedCheckCapture(t, plan, "ts", capture, ts)
			guidedCheckCapture(t, plan, "go", capture, gs)
			t.Logf("%s: %d steps compared", c.ID, len(c.Steps))
		})
	}
	if ran == 0 {
		t.Fatalf("no cases in group %q", group)
	}
	t.Logf("evidence retained in %s", evidenceRoot)
}

func TestLiveGuidedInvestigateRefusals(t *testing.T)     { runGuidedGroup(t, "refusals") }
func TestLiveGuidedInvestigateIntake(t *testing.T)       { runGuidedGroup(t, "intake") }
func TestLiveGuidedInvestigateReview(t *testing.T)       { runGuidedGroup(t, "review") }
func TestLiveGuidedInvestigateContinuation(t *testing.T) { runGuidedGroup(t, "continuation") }
func TestLiveGuidedInvestigateSignals(t *testing.T)      { runGuidedGroup(t, "signals") }
