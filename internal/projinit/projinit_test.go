package projinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestHookConfigQuotesPosixPath(t *testing.T) {
	cfg := HookConfig("/tmp/it's/cli.js")
	cmd := cfg.Get("hooks", "Stop").Items()[0].Get("hooks").Items()[0].Get("command").Str()
	if cmd != `node '/tmp/it'"'"'s/cli.js' codex sidecar hook --quiet` {
		t.Fatal(cmd)
	}
}

func TestStarterIgnoreEndsWithNewline(t *testing.T) {
	if !strings.HasSuffix(StarterFaultlineIgnore, "\n") || strings.Count(StarterFaultlineIgnore, "\n") != 11 {
		t.Fatalf("%q", StarterFaultlineIgnore)
	}
}

// The hook document's shape (src/cli-app.ts:1689): five events, each one
// group holding one hook, only SessionStart with a matcher.
func TestHookConfigShape(t *testing.T) {
	cfg := HookConfig(`C:\a "b"\cli.js`)
	if keys := cfg.Obj().Keys(); len(keys) != 1 || keys[0] != "hooks" {
		t.Fatal(keys)
	}
	events := cfg.Get("hooks").Obj().Keys()
	if strings.Join(events, ",") != "SessionStart,UserPromptSubmit,PreToolUse,PostToolUse,Stop" {
		t.Fatal(events)
	}
	start := cfg.Get("hooks", "SessionStart").Items()[0]
	if strings.Join(start.Obj().Keys(), ",") != "matcher,hooks" || start.Get("matcher").Str() != "startup|resume|clear|compact" {
		t.Fatal(start.Obj().Keys())
	}
	hook := start.Get("hooks").Items()[0]
	if strings.Join(hook.Obj().Keys(), ",") != "type,command,commandWindows,timeout,statusMessage" {
		t.Fatal(hook.Obj().Keys())
	}
	if got := hook.Get("commandWindows").Str(); got != `node "C:\a \"b\"\cli.js" codex sidecar hook --quiet` {
		t.Fatal(got)
	}
	if hook.Get("timeout").Num() != 30 || hook.Get("statusMessage").Str() != "FaultLine observes this Codex session" {
		t.Fatal(jsjson.Stringify(hook))
	}
	explicit := HookConfigFor(HookCommand{Command: "x"})
	if _, ok := explicit.Get("hooks", "Stop").Items()[0].Get("hooks").Items()[0].Obj().Get("commandWindows"); ok {
		t.Fatal("commandWindows without --command-windows")
	}
}

// quoteWindowsCommandArgument (src/cli-app.ts:1625): backslashes double
// only before a quote or the closing quote.
func TestQuoteWindowsCommandArgument(t *testing.T) {
	for in, want := range map[string]string{
		`a\b`:   `"a\b"`,
		`a\`:    `"a\\"`,
		`a\\"b`: `"a\\\\\"b"`,
		`"`:     `"\""`,
		`é\`:    `"é\\"`,
	} {
		if got, _ := quoteWindowsCommandArgument(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestSidecarCliCommandRefusals(t *testing.T) {
	dir := t.TempDir()
	js := filepath.Join(dir, "cli.js")
	must(t, os.WriteFile(js, []byte("export {};\n"), 0o644))
	must(t, os.Symlink(js, filepath.Join(dir, "link.js")))
	must(t, os.WriteFile(filepath.Join(dir, "cli.ts"), nil, 0o644))
	must(t, os.Mkdir(filepath.Join(dir, "dir.js"), 0o755))
	cases := []struct {
		args []string
		want string
	}{
		{nil, "Provide exactly one of --cli <built-cli.js> or --command <stable-hook-command>."},
		{[]string{"--cli", js, "--command", "x"}, "Provide exactly one of"},
		{[]string{"--cli", js, "--command-windows", "x"}, "--command-windows is only valid with an explicit --command; --cli generates both platform commands safely."},
		{[]string{"--cli", "--x"}, "Missing required option: --cli"},
		{[]string{"--cli", filepath.Join(dir, "cli.ts")}, "--cli must point to the built JavaScript entry point (normally dist/cli.js), not " + filepath.Join(dir, "cli.ts") + "."},
		{[]string{"--cli", filepath.Join(dir, "link.js")}, "--cli must point to an existing built, non-symlink CLI file: " + filepath.Join(dir, "link.js") + " (not a real regular file)"},
		{[]string{"--cli", filepath.Join(dir, "dir.js")}, "(not a real regular file)"},
		{[]string{"--cli", filepath.Join(dir, "gone.js")}, "(ENOENT: no such file or directory, lstat '" + filepath.Join(dir, "gone.js") + "')"},
		{[]string{"--command", "  "}, "--command must not be empty."},
		{[]string{"--command", "a\nb"}, "--command cannot contain a NUL byte or a line break."},
		{[]string{"--command", "a", "--command-windows", "b\x00"}, "--command-windows cannot contain a NUL byte or a line break."},
	}
	for _, c := range cases {
		_, err := SidecarCliCommand(c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v", c.args, err)
		}
	}
	got, err := SidecarCliCommand([]string{"--cli", js})
	if err != nil || got.Command != "node '"+js+"' codex sidecar hook --quiet" || !got.HasWindows {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = SidecarCliCommand([]string{"--command", " run me ", "--command-windows", "w"})
	if err != nil || got.Command != "run me" || got.CommandWindows != "w" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestExtname(t *testing.T) {
	for in, want := range map[string]string{"/a/..js": ".js", "/a/.js": "", "/a/x.JS": ".JS", "/a/x.": ".", "/a/...": ".", "/a/.x.js": ".js", "/a/..": "", "/a/x": ""} {
		if got := extname(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	must(t, os.Mkdir(repo, 0o755))
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.test"}, {"config", "user.name", "T"}} {
		gitCmd(t, repo, args...)
	}
	must(t, os.WriteFile(filepath.Join(repo, "package.json"), []byte("{\"name\":\"x\"}\n"), 0o644))
	gitCmd(t, repo, "add", "package.json")
	gitCmd(t, repo, "commit", "-qm", "i")
	return repo
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
}

// readyRunner answers doctor's fixed commands without spawning anything.
func readyRunner(root string) func(string, []string, time.Duration, int) hostcmd.Result {
	zero := 0
	ok := func(s string) hostcmd.Result { return hostcmd.Result{ExitCode: &zero, Stdout: s} }
	return func(exe string, args []string, _ time.Duration, _ int) hostcmd.Result {
		last := args[len(args)-1]
		switch {
		case last == "--version" && exe == "git":
			return ok("git version 2.47.0\n")
		case exe == "node":
			return ok("v22.22.2\n")
		case exe == "docker" && args[0] == "--version":
			return ok("Docker version 27.0.0\n")
		case exe == "docker":
			return ok("27.0.0\n")
		case strings.Contains(strings.Join(args, " "), "rev-parse"):
			return ok(root + "\n")
		}
		return ok("")
	}
}

// Port of tests/coh12-autonomous-session.test.ts:151: init with a standing
// policy freezes it into the config's witness store.
func TestPlanFreezesStandingPolicy(t *testing.T) {
	repo := gitRepo(t)
	id := "init-default"
	result, err := Plan(Options{
		Repository: repo, WriteIgnoreIfMissing: true, WriteConfig: true,
		StandingPolicy: &StandingPolicyInput{PolicyID: &id, FrozenBy: "owner@example.test", AllowedCommands: []string{"pnpm test"}},
	}, readyRunner(repo))
	if err != nil {
		t.Fatal(err)
	}
	sp := result.Get("standingPolicy")
	if sp.Get("status").Str() != "CREATED" || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(sp.Get("policyDigest").Str()) {
		t.Fatal(jsjson.Stringify(sp))
	}
	if sp.Get("detail").Str() != "Standing approval frozen for 1 exact command(s). Novel commands still stop for review (COH-09/12)." {
		t.Fatal(sp.Get("detail").Str())
	}
	path := filepath.Join(repo, ".faultline", "witnesses", "standing-policies", "init-default.json")
	// Windows has no POSIX modes (Go and Node both report 0666 for files,
	// 0777 for directories), so there only the file's existence is checked.
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	if dir, _ := os.Stat(filepath.Dir(path)); runtime.GOOS != "windows" && dir.Mode().Perm() != 0o700 {
		t.Fatal(dir.Mode())
	}
	keys := strings.Join(result.Obj().Keys(), ",")
	if keys != "repository,doctor,suggestedRuntime,sidecar,ignoreFile,config,standingPolicy,next,nextCommands,limitations" {
		t.Fatal(keys)
	}
	if result.Get("suggestedRuntime").Str() != "node" || result.Get("next").Str() != "fl runtime prepare node --yes" {
		t.Fatal(jsjson.Stringify(result))
	}
	// A NaN timeout fails the policy schema.
	nan := jsjson.ToNumber("abc")
	id2 := "nan"
	_, err = Plan(Options{Repository: repo, StandingPolicy: &StandingPolicyInput{PolicyID: &id2, FrozenBy: "o", AllowedCommands: []string{"x"}, MaxTimeoutSeconds: &nan}}, readyRunner(repo))
	if err == nil || !strings.Contains(jsexc.Message(err), `"received": "nan"`) || !strings.Contains(jsexc.Message(err), `"maxTimeoutSeconds"`) {
		t.Fatalf("NaN maxTimeoutSeconds: %v", err)
	}
}

// The plan statuses and next commands of src/project-init.ts:96-205.
func TestPlanStatuses(t *testing.T) {
	repo := gitRepo(t)
	run := readyRunner(repo)
	plan, err := Plan(Options{Repository: repo, CliPath: "/x/cli.js", Runtime: "go"}, run)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Get("sidecar", "status").Str() != "PREVIEW_REQUIRED" || plan.Get("ignoreFile", "status").Str() != "SKIPPED" ||
		plan.Get("config", "status").Str() != "SKIPPED" || plan.Get("config", "value").Kind() != jsjson.Null ||
		plan.Get("standingPolicy", "status").Str() != "SKIPPED" || plan.Get("suggestedRuntime").Str() != "go" {
		t.Fatal(jsjson.Stringify(plan))
	}
	if plan.Get("next").Str() != "fl init --repo "+repo+" --yes" {
		t.Fatal(plan.Get("next").Str())
	}
	if _, err := os.Stat(filepath.Join(repo, ".faultlineignore")); !os.IsNotExist(err) {
		t.Fatal("a plan wrote .faultlineignore")
	}

	applied, err := Plan(Options{Repository: repo, CliPath: "/x/cli.js", WriteIgnoreIfMissing: true, WriteConfig: true}, run)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Get("ignoreFile", "status").Str() != "CREATED" || applied.Get("config", "status").Str() != "CREATED" ||
		applied.Get("standingPolicy", "status").Str() != "PROMPT_AVAILABLE" {
		t.Fatal(jsjson.Stringify(applied))
	}
	if applied.Get("next").Str() != "fl codex sidecar install --repo "+repo+" --cli /x/cli.js --yes" {
		t.Fatal(applied.Get("next").Str())
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".faultlineignore")); string(b) != StarterFaultlineIgnore {
		t.Fatalf("%q", b)
	}

	// An existing config is refreshed even without --yes.
	again, err := Plan(Options{Repository: repo}, run)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get("config", "status").Str() != "UNCHANGED" || again.Get("ignoreFile", "status").Str() != "ALREADY_PRESENT" ||
		again.Get("sidecar", "status").Str() != "CLI_REQUIRED" {
		t.Fatal(jsjson.Stringify(again))
	}
	must(t, os.MkdirAll(filepath.Join(repo, ".codex"), 0o700))
	must(t, os.WriteFile(filepath.Join(repo, ".codex", "hooks.json"), []byte("{}"), 0o600))
	present, _ := Plan(Options{Repository: repo, CliPath: "/x/cli.js"}, run)
	if present.Get("sidecar", "status").Str() != "ALREADY_PRESENT" {
		t.Fatal(jsjson.Stringify(present.Get("sidecar")))
	}
}

func TestHookTargetAndWrite(t *testing.T) {
	repo := gitRepo(t)
	file, codex, err := HookTarget(repo)
	if err != nil || codex || file != filepath.Join(repo, ".codex", "hooks.json") {
		t.Fatal(file, codex, err)
	}
	if err := WriteHookConfig(repo, HookConfig("/x/cli.js")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	dir, _ := os.Stat(filepath.Dir(file))
	if runtime.GOOS != "windows" && (info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700) {
		t.Fatal(info.Mode(), dir.Mode())
	}
	b, _ := os.ReadFile(file)
	if string(b) != jsjson.StringifyIndent(HookConfig("/x/cli.js"), "  ")+"\n" {
		t.Fatalf("%s", b)
	}
	if _, _, err := HookTarget(repo); err == nil || err.Error() != "FaultLine will not replace an existing project hook document: "+file+". Run sidecar config and merge the reviewed hooks yourself." {
		t.Fatal(err)
	}
	if err := WriteHookConfig(repo, HookConfig("/x/cli.js")); err == nil || !strings.HasPrefix(err.Error(), "FaultLine could not create the project hook document without replacing anything: EEXIST: file already exists, open '") {
		t.Fatal(err)
	}

	notRepo := t.TempDir()
	if _, _, err := HookTarget(notRepo); err == nil || !strings.HasPrefix(err.Error(), "--repo must be a usable local Git worktree for FaultLine sidecar capture: FaultLine could not safely inspect this Git worktree: ") {
		t.Fatal(err)
	}
	if _, _, err := HookTarget(filepath.Join(notRepo, "missing")); err == nil || err.Error() != "--repo must be a real directory, not a symbolic link or file: "+filepath.Join(notRepo, "missing") {
		t.Fatal(err)
	}
}

func TestCodexSidecarLedgerPath(t *testing.T) {
	repo := gitRepo(t)
	got, err := CodexSidecarLedgerPath(repo, "faultline-install-check")
	// sha256("faultline-install-check")
	want := filepath.Join(repo, ".git", "faultline", "recordings", "codex-")
	if err != nil || !strings.HasPrefix(got, want) || len(got) != len(want)+64+len(".json") {
		t.Fatal(got, err)
	}
	if _, err := CodexSidecarLedgerPath(repo, "bad id"); err == nil {
		t.Fatal("an identifier with a space is accepted")
	}
	// A recordings directory that is a file is refused.
	must(t, os.MkdirAll(filepath.Join(repo, ".git", "faultline"), 0o700))
	must(t, os.WriteFile(filepath.Join(repo, ".git", "faultline", "recordings"), nil, 0o600))
	if _, err := CodexSidecarLedgerPath(repo, "s"); err == nil || err.Error() != "FaultLine sidecar recordings must be a real directory, not a symlink or file." {
		t.Fatal(err)
	}
}

// must fails the test on a setup error.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
