package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/doctor"
	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// fakeDoctor replaces the doctor runner for one test: every fixed doctor
// command answers READY, and the calls are counted.
func fakeDoctor(t *testing.T, root string) *int {
	calls := 0
	zero := 0
	ok := func(s string) hostcmd.Result { return hostcmd.Result{ExitCode: &zero, Stdout: s} }
	saved := doctorRunner
	doctorRunner = func() doctor.Runner {
		return func(exe string, args []string, _ time.Duration, _ int) hostcmd.Result {
			if exe == "node" {
				calls++
				return ok("v22.22.2\n")
			}
			switch joined := strings.Join(args, " "); {
			case strings.HasSuffix(joined, "--version") && exe == "git":
				return ok("git version 2.47.0\n")
			case exe == "docker" && args[0] == "--version":
				return ok("Docker version 27.0.0\n")
			case exe == "docker":
				return ok("27.0.0\n")
			case strings.Contains(joined, "rev-parse"):
				return ok(root + "\n")
			}
			return ok("")
		}
	}
	t.Cleanup(func() { doctorRunner = saved })
	return &calls
}

func initRepo(t *testing.T) (dir, repo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(dir, "repo")
	os.Mkdir(repo, 0o755)
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "faultline@example.test"}, {"config", "user.name", "FaultLine"}, {"add", "package.json"}, {"commit", "-qm", "init"}} {
		if args[0] == "add" {
			os.WriteFile(filepath.Join(repo, "package.json"), []byte("{\"name\":\"demo\"}\n"), 0o644)
		}
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, out)
		}
	}
	return dir, repo
}

func runInit(t *testing.T, args ...string) (jsjson.Value, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(append([]string{"init"}, args...), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	v, err := jsjson.Parse(stdout.String())
	if err != nil {
		t.Fatal(err)
	}
	return v, stderr.String()
}

// Port of tests/cli.test.ts:1025: a plan writes nothing; --yes applies the
// ignore file, config and hooks.
func TestInitPlanThenApply(t *testing.T) {
	dir, repo := initRepo(t)
	calls := fakeDoctor(t, repo)
	cli := filepath.Join(dir, "cli.js")
	os.WriteFile(cli, []byte("export {};\n"), 0o644)

	plan, _ := runInit(t, "--repo", repo, "--cli", cli, "--runtime", "node")
	if plan.Get("status").Str() != "INIT_PLAN" || plan.Get("suggestedRuntime").Str() != "node" ||
		plan.Get("sidecar", "status").Str() != "PREVIEW_REQUIRED" || plan.Get("ignoreFile", "status").Str() != "SKIPPED" {
		t.Fatal(jsjson.Stringify(plan))
	}
	if _, ok := plan.Obj().Get("snapshotPrewarm"); ok {
		t.Fatal("a plan pre-warmed")
	}
	for _, p := range []string{".codex/hooks.json", ".faultlineignore"} {
		if _, err := os.Stat(filepath.Join(repo, p)); !os.IsNotExist(err) {
			t.Fatalf("plan wrote %s", p)
		}
	}
	if *calls != 1 {
		t.Fatalf("plan ran doctor %d times", *calls)
	}

	*calls = 0
	applied, stderr := runInit(t, "--repo", repo, "--cli", cli, "--runtime", "node", "--yes")
	if applied.Get("status").Str() != "INIT_APPLIED" || applied.Get("sidecar", "status").Str() != "INSTALLED" ||
		applied.Get("ignoreFile", "status").Str() != "CREATED" || applied.Get("config", "status").Str() != "CREATED" ||
		!strings.HasPrefix(applied.Get("next").Str(), "fl ") {
		t.Fatal(jsjson.Stringify(applied))
	}
	if *calls != 2 {
		t.Fatalf("--yes --cli ran doctor %d times, want 2", *calls)
	}
	keys := strings.Join(applied.Obj().Keys(), ",")
	if keys != "status,repository,suggestedRuntime,doctor,ignoreFile,config,sidecar,standingPolicy,snapshotPrewarm,next,nextCommands,limitations,note" {
		t.Fatal(keys)
	}
	if strings.Join(applied.Get("doctor").Obj().Keys(), ",") != "dockerInvestigationPreflight,likelyRuntime,diagnosticCount" {
		t.Fatal(applied.Get("doctor").Obj().Keys())
	}
	pre := applied.Get("snapshotPrewarm")
	if pre.Get("status").Str() != "PREWARMED" || pre.Get("cachePath").Str() != filepath.Join(repo, ".git", "faultline", "turn-snapshot-cache.json") ||
		!regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(pre.Get("treeDigest").Str()) {
		t.Fatal(jsjson.Stringify(pre))
	}
	if !strings.HasSuffix(stderr, "first-turn snapshot pre-warmed\n") ||
		!strings.Contains(stderr, "FaultLine snapshot warning: Including previously untracked file in turn-tree snapshot: .codex/hooks.json\n") {
		t.Fatalf("%q", stderr)
	}
	for _, p := range []string{".faultline/config.json", ".codex/hooks.json"} {
		if _, err := os.Stat(filepath.Join(repo, p)); err != nil {
			t.Fatal(err)
		}
	}
	ignore, _ := os.ReadFile(filepath.Join(repo, ".faultlineignore"))
	if !regexp.MustCompile(`(?i)environment descriptors|lockfiles`).Match(ignore) ||
		regexp.MustCompile(`(?m)^pnpm-lock\.yaml$`).Match(ignore) || regexp.MustCompile(`(?m)^tests/$`).Match(ignore) {
		t.Fatalf("%s", ignore)
	}

	// Again: the hooks exist, so the preview says ALREADY_PRESENT and only
	// one more doctor run happens per plan.
	*calls = 0
	again, _ := runInit(t, "--repo", repo, "--cli", cli, "--yes")
	if again.Get("sidecar", "status").Str() != "ALREADY_PRESENT" || again.Get("config", "status").Str() != "UNCHANGED" ||
		again.Get("ignoreFile", "status").Str() != "ALREADY_PRESENT" || *calls != 2 {
		t.Fatal(*calls, jsjson.Stringify(again))
	}
}

func TestInitUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--runtime", "ruby"}, "--runtime must be one of node, python, or go."},
		{[]string{"--standing-policy"}, "Standing policy on-ramp requires --yes (exact allowlist is write-once)."},
		{[]string{"--standing-command", "x"}, "Standing policy on-ramp requires --yes (exact allowlist is write-once)."},
		{[]string{"--standing-policy", "--yes"}, "Pass --standing-command <exact UTF-8 command> for the YOLO standing-policy allowlist."},
		{[]string{"--yes", "--standing-command", " "}, "Pass --standing-command <exact UTF-8 command> for the YOLO standing-policy allowlist."},
		{[]string{"--yes", "--standing-command", "x"}, "Pass --standing-frozen-by <actor> so the standing policy records a human owner."},
		{[]string{"--yes", "--standing-command", "x", "--standing-frozen-by", "\t"}, "Pass --standing-frozen-by <actor> so the standing policy records a human owner."},
	}
	for _, c := range cases {
		stdout, stderr, code := run(append([]string{"init"}, c.args...)...)
		if code != 1 || stdout != "" || stderr != "FaultLine error: "+c.want+"\nNext: pnpm fl help\n" {
			t.Errorf("%q: %d %q %q", c.args, code, stdout, stderr)
		}
	}
}
