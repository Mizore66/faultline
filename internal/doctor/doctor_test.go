package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/safegit"
)

func code(n int) *int { return &n }

func TestGitMissingMakesRepoChecksInapplicable(t *testing.T) {
	run := func(exe string, args []string, _ time.Duration, _ int) hostcmd.Result {
		switch exe {
		case "node":
			return hostcmd.Result{ExitCode: code(0), Stdout: "v22.22.2\n"}
		case "docker":
			return hostcmd.Result{ExitCode: code(0), Stdout: "Docker version 27.0.0\n"}
		}
		return hostcmd.Result{Stderr: "spawn git ENOENT"}
	}
	r := Run(t.TempDir(), run)
	d := r.Get("diagnostics").Items()
	if d[0].Get("status").Str() != "UNAVAILABLE" || r.Get("dockerInvestigationPreflight").Str() != "UNAVAILABLE" {
		t.Fatalf("%v", d[0])
	}
	if d[1].Get("status").Str() != "INAPPLICABLE" || d[2].Get("status").Str() != "INAPPLICABLE" {
		t.Fatalf("%v %v", d[1], d[2])
	}
	if CliExitCode(r) != 0 {
		t.Fatal("node READY must exit 0")
	}
}

// The tests below port tests/doctor.test.ts (all but the live --security
// self-test, which is Task 9's).

type call struct {
	exe     string
	args    []string
	timeout time.Duration
	max     int
}

// fakeRunner records every call (tests/doctor.test.ts fakeRunner).
type fakeRunner struct {
	mu    sync.Mutex
	calls []call
}

func (f *fakeRunner) runner(respond func(exe string, args []string) hostcmd.Result) Runner {
	return func(exe string, args []string, timeout time.Duration, max int) hostcmd.Result {
		f.mu.Lock()
		f.calls = append(f.calls, call{exe, slices.Clone(args), timeout, max})
		f.mu.Unlock()
		return respond(exe, args)
	}
}

func ok(stdout string) hostcmd.Result { return hostcmd.Result{ExitCode: code(0), Stdout: stdout} }

// readyResponse is tests/doctor.test.ts readyResponse.
func readyResponse(t *testing.T, root string) func(string, []string) hostcmd.Result {
	return func(exe string, args []string) hostcmd.Result {
		switch {
		case exe == "git" && slices.Contains(args, "--version"):
			return ok("git version 2.47.0\n")
		case exe == "node":
			return ok("v22.17.0\n")
		case exe == "docker" && args[0] == "--version":
			return ok("Docker version 27.3.1, build deadbeef\n")
		case exe == "git" && slices.Contains(args, "rev-parse"):
			return ok(root + "\n")
		case exe == "git" && slices.Contains(args, "status"):
			return ok("")
		case exe == "docker" && args[0] == "info":
			return ok("27.3.1\n")
		}
		panic("Unexpected fixed doctor command: " + exe + " " + strings.Join(args, " "))
	}
}

// withMarkers is a directory holding the named root files (the TS tests'
// markerProbe).
func withMarkers(t *testing.T, markers ...string) string {
	dir := t.TempDir()
	for _, m := range markers {
		if err := os.WriteFile(filepath.Join(dir, m), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func diag(t *testing.T, r jsjson.Value, id string) jsjson.Value {
	t.Helper()
	for _, d := range r.Get("diagnostics").Items() {
		if d.Get("id").Str() == id {
			return d
		}
	}
	t.Fatalf("missing diagnostic %s", id)
	return jsjson.Value{}
}

func strs(v jsjson.Value) []string {
	var out []string
	for _, item := range v.Items() {
		out = append(out, item.Str())
	}
	return out
}

func TestReadyPreflightUsesOnlyFixedCommands(t *testing.T) {
	requested := t.TempDir()
	root := withMarkers(t, "package.json", "pnpm-lock.yaml")
	f := &fakeRunner{}
	r := Run(requested, f.runner(readyResponse(t, root)))
	if r.Get("schemaVersion").Str() != SchemaVersion || r.Get("repository").Str() != requested || r.Get("repositoryRoot").Str() != root {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
	if ProofReadyStatus(r) != "READY" || r.Get("imageSelection").Str() != "NOT_CHECKED" {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
	if got := jsjson.Stringify(r.Get("likelyRuntime")); got != `{"kind":"NODE","markers":["package.json","pnpm-lock.yaml"],"imageFamily":"node"}` {
		t.Fatal(got)
	}
	if !strings.Contains(strings.Join(strs(r.Get("limitations")), " "), "not a proof") {
		t.Fatal("limitations")
	}
	if diag(t, r, "worktree").Get("status").Str() != "READY" || diag(t, r, "runtime").Get("status").Str() != "READY" {
		t.Fatal("worktree/runtime")
	}
	var ids []string
	for _, d := range r.Get("diagnostics").Items() {
		ids = append(ids, d.Get("id").Str())
	}
	if !slices.Equal(ids, []string{"git", "repository", "worktree", "node", "docker-cli", "docker-daemon", "runtime"}) {
		t.Fatal(ids)
	}
	keys := r.Obj().Keys()
	if !slices.Equal(keys, []string{"schemaVersion", "repository", "repositoryRoot", "diagnostics", "likelyRuntime", "imageSelection", "dockerInvestigationPreflight", "limitations"}) {
		t.Fatal(keys)
	}
	safe := safegit.Doctor()
	var gitTails [][]string
	var others []call
	for _, c := range f.calls {
		if c.timeout != 5*time.Second || c.max != 65536 {
			t.Fatalf("%+v", c)
		}
		if c.exe != "git" {
			others = append(others, c)
			continue
		}
		if !slices.Equal(c.args[:len(safe)], safe) {
			t.Fatalf("git call without the safe config: %q", c.args)
		}
		gitTails = append(gitTails, c.args[len(safe):])
	}
	wantGit := [][]string{{"--version"}, {"-C", requested, "rev-parse", "--show-toplevel"}, {"-C", root, "status", "--porcelain=v1", "--untracked-files=all", "-z"}}
	if !slices.EqualFunc(gitTails, wantGit, slices.Equal) {
		t.Fatal(gitTails)
	}
	// node and docker --version run concurrently (Promise.all), so their
	// recorded order is racy; the daemon query comes after both.
	var gotOthers []string
	for _, c := range others {
		gotOthers = append(gotOthers, strings.Join(append([]string{c.exe}, c.args...), " "))
	}
	if len(gotOthers) != 3 {
		t.Fatal(gotOthers)
	}
	slices.Sort(gotOthers[:2])
	if !slices.Equal(gotOthers, []string{"docker --version", "node --version", "docker info --format {{.ServerVersion}}"}) {
		t.Fatal(gotOthers)
	}
}

func TestDirtyWorktreeCountsPathsWithoutNamingThem(t *testing.T) {
	root := withMarkers(t, "pyproject.toml")
	f := &fakeRunner{}
	ready := readyResponse(t, root)
	r := Run(root, f.runner(func(exe string, args []string) hostcmd.Result {
		if exe == "git" && slices.Contains(args, "status") {
			return ok(" M secret-file.txt\x00?? generated-output.log\x00")
		}
		return ready(exe, args)
	}))
	if ProofReadyStatus(r) != "ACTION_REQUIRED" {
		t.Fatal(ProofReadyStatus(r))
	}
	w := diag(t, r, "worktree")
	if w.Get("status").Str() != "ACTION_REQUIRED" || w.Get("observation").Str() != "2 changed paths reported by Git." {
		t.Fatal(jsjson.Stringify(w))
	}
	if strings.Contains(jsjson.Stringify(w), "secret-file.txt") {
		t.Fatal("path name leaked")
	}
	if r.Get("likelyRuntime", "kind").Str() != "PYTHON" || r.Get("likelyRuntime", "imageFamily").Str() != "python" {
		t.Fatal(jsjson.Stringify(r.Get("likelyRuntime")))
	}
}

func TestGitThatCannotStartSkipsRepositoryChecks(t *testing.T) {
	f := &fakeRunner{}
	r := Run(withMarkers(t), f.runner(func(exe string, args []string) hostcmd.Result {
		switch {
		case exe == "git":
			return hostcmd.Result{}
		case exe == "node":
			return ok("v22.0.0\n")
		case exe == "docker" && args[0] == "--version":
			return ok("Docker version 27.0.0\n")
		case exe == "docker":
			return ok("27.0.0\n")
		}
		panic("Unexpected command")
	}))
	if diag(t, r, "git").Get("status").Str() != "UNAVAILABLE" || diag(t, r, "repository").Get("status").Str() != "INAPPLICABLE" ||
		diag(t, r, "worktree").Get("status").Str() != "INAPPLICABLE" || ProofReadyStatus(r) != "UNAVAILABLE" {
		t.Fatal(jsjson.Stringify(r))
	}
	for _, c := range f.calls {
		if slices.Contains(c.args, "rev-parse") || slices.Contains(c.args, "status") {
			t.Fatalf("%+v", c)
		}
	}
}

func TestUnavailableDockerCliSkipsTheDaemon(t *testing.T) {
	root := withMarkers(t, "go.mod")
	f := &fakeRunner{}
	ready := readyResponse(t, root)
	r := Run(root, f.runner(func(exe string, args []string) hostcmd.Result {
		if exe == "docker" {
			return hostcmd.Result{}
		}
		return ready(exe, args)
	}))
	if diag(t, r, "docker-cli").Get("status").Str() != "UNAVAILABLE" || diag(t, r, "docker-daemon").Get("status").Str() != "INAPPLICABLE" ||
		ProofReadyStatus(r) != "UNAVAILABLE" || CliExitCode(r) != 0 {
		t.Fatal(jsjson.Stringify(r))
	}
	for _, c := range f.calls {
		if c.exe == "docker" && c.args[0] == "info" {
			t.Fatal("daemon queried")
		}
	}
}

func TestOldNodeAndAmbiguousRuntime(t *testing.T) {
	root := withMarkers(t, "package.json", "pyproject.toml")
	ready := readyResponse(t, root)
	r := Run(root, (&fakeRunner{}).runner(func(exe string, args []string) hostcmd.Result {
		if exe == "node" {
			return ok("v20.18.0\n")
		}
		return ready(exe, args)
	}))
	if diag(t, r, "node").Get("status").Str() != "ACTION_REQUIRED" || diag(t, r, "runtime").Get("status").Str() != "INAPPLICABLE" {
		t.Fatal(jsjson.Stringify(r))
	}
	if got := jsjson.Stringify(r.Get("likelyRuntime")); got != `{"kind":"MULTIPLE","markers":["package.json","pyproject.toml"],"imageFamily":null}` {
		t.Fatal(got)
	}
	if ProofReadyStatus(r) != "ACTION_REQUIRED" || CliExitCode(r) != 1 {
		t.Fatal(jsjson.Stringify(r))
	}
}

func TestUnknownRuntime(t *testing.T) {
	if got := jsjson.Stringify(DetectLikelyRuntime(withMarkers(t))); got != `{"kind":"UNKNOWN","markers":[],"imageFamily":null}` {
		t.Fatal(got)
	}
}

func TestOutputLimitFailsSafely(t *testing.T) {
	root := withMarkers(t)
	ready := readyResponse(t, root)
	r := Run(root, (&fakeRunner{}).runner(func(exe string, args []string) hostcmd.Result {
		if exe == "node" {
			return hostcmd.Result{OutputLimitExceeded: true}
		}
		return ready(exe, args)
	}))
	n := diag(t, r, "node")
	if n.Get("status").Str() != "UNAVAILABLE" || !strings.Contains(n.Get("observation").Str(), "output limit") ||
		ProofReadyStatus(r) != "UNAVAILABLE" || CliExitCode(r) != 1 {
		t.Fatal(jsjson.Stringify(r))
	}
}

func TestReadyNodeExitsZeroWithoutDocker(t *testing.T) {
	root := withMarkers(t, "package.json")
	ready := readyResponse(t, root)
	r := Run(root, (&fakeRunner{}).runner(func(exe string, args []string) hostcmd.Result {
		if exe == "docker" {
			return hostcmd.Result{}
		}
		return ready(exe, args)
	}))
	if ProofReadyStatus(r) != "UNAVAILABLE" || CliExitCode(r) != 0 {
		t.Fatal(jsjson.Stringify(r))
	}
}

// A runner that panics with a JS exception is runCommand's catch.
func TestThrowingRunnerIsANullResult(t *testing.T) {
	r := Run(withMarkers(t), func(exe string, args []string, _ time.Duration, _ int) hostcmd.Result {
		if exe == "node" {
			return ok("v22.1.0")
		}
		jsexc.Throw(errors.New("runner threw"))
		return hostcmd.Result{}
	})
	g := diag(t, r, "git")
	if g.Get("observation").Str() != "The command could not be started or did not return an exit code." {
		t.Fatal(jsjson.Stringify(g))
	}
}

func TestOutputLineAndUnavailableTexts(t *testing.T) {
	for _, c := range []struct {
		in, out string
		ok      bool
	}{
		{"", "", false},
		{" \r\n\t ", "", false},
		{"git version 2.99.0\r\n\r\n  ", "git version 2.99.0", true},
		{"a\r\n\nb\rc  ", "a b c", true},
		{" x\ufeff\u00a0", "x", true},
		{strings.Repeat("a", 159) + "\U0001F600z", strings.Repeat("a", 159) + "\xed\xa0\xbd", true},
		{strings.Repeat("b", 200), strings.Repeat("b", 160), true},
	} {
		got, ok := outputLine(c.in)
		if got != c.out || ok != c.ok {
			t.Errorf("outputLine(%q) = %q, %v", c.in, got, ok)
		}
	}
	for _, c := range []struct {
		r    hostcmd.Result
		want string
	}{
		{hostcmd.Result{OutputLimitExceeded: true, TimedOut: true}, "The command exceeded FaultLine's 65536-byte diagnostic output limit."},
		{hostcmd.Result{TimedOut: true, ExitCode: code(0)}, "The command timed out before returning a result."},
		{hostcmd.Result{}, "The command could not be started or did not return an exit code."},
		{hostcmd.Result{ExitCode: code(3)}, "The command exited with status 3."},
	} {
		if got := unavailableObservation(c.r); got != c.want {
			t.Errorf("%+v: %q", c.r, got)
		}
	}
}

func TestNodeVersionShapes(t *testing.T) {
	for _, c := range []struct{ version, status string }{
		{"v22", "READY"}, {"v22.1.2-nightly", "READY"}, {"22.0.0", "ACTION_REQUIRED"}, {"v21.9.9", "ACTION_REQUIRED"},
		{"v22.1.2.3", "READY"}, {"v22 x", "ACTION_REQUIRED"}, {"v100.0.0", "READY"}, {"v", "ACTION_REQUIRED"},
	} {
		d := nodeDiagnostic(ok(c.version + "\n"))
		if d.status != c.status {
			t.Errorf("%s: %s", c.version, d.status)
		}
	}
	if d := dockerDaemonDiagnostic(ok("  \n")); d.status != "UNAVAILABLE" || *d.observation != "Docker returned no server version." {
		t.Fatalf("%+v", d)
	}
	if d := gitDiagnostic(ok("\n")); d.status != "ACTION_REQUIRED" || d.observation != nil {
		t.Fatalf("%+v", d)
	}
}
