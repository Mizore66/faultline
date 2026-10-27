package prevention

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// setTempDir points os.tmpdir() (and os.TempDir) at a fresh directory.
func setTempDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
}

func gitRepo(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("config", "user.name", "FaultLine Test")
	run("config", "user.email", "faultline@example.test")
	os.WriteFile(filepath.Join(dir, "state.txt"), []byte("bad\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("x\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "bad")
	return dir, run("rev-parse", "HEAD")
}

func TestCommitRepairedWorktreeState(t *testing.T) {
	setTempDir(t)
	dir, head := gitRepo(t)
	os.WriteFile(filepath.Join(dir, "state.txt"), []byte("good\n"), 0o755)
	os.Chmod(filepath.Join(dir, "state.txt"), 0o755)
	os.Remove(filepath.Join(dir, "gone.txt"))
	os.MkdirAll(filepath.Join(dir, ".faultline-repair"), 0o755)
	os.WriteFile(filepath.Join(dir, ".faultline-repair", "README.md"), []byte("brief\n"), 0o644)
	os.Symlink("state.txt", filepath.Join(dir, "link"))
	s, err := CommitRepairedWorktreeState(dir, head)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "-C", dir, "ls-tree", "-r", s.Tree).Output()
	tree := string(out)
	// Windows has no execute bits: lstat reports 0o666 there.
	if (runtime.GOOS != "windows") != strings.Contains(tree, "100755 blob ") || strings.Contains(tree, "gone.txt") ||
		!strings.Contains(tree, ".faultline-repair/README.md") || strings.Contains(tree, "\tlink\n") {
		t.Fatalf("%s", out)
	}
	// The commit object's structure (its ids depend on the clock).
	cat, _ := exec.Command("git", "-C", dir, "cat-file", "-p", s.Commit).Output()
	lines := strings.Split(string(cat), "\n")
	if len(lines) < 6 || lines[0] != "tree "+s.Tree || lines[1] != "parent "+head ||
		!strings.HasPrefix(lines[2], "author FaultLine Test <faultline@example.test> ") ||
		lines[4] != "" || lines[5] != "faultline: repaired prevention state" {
		t.Fatalf("%s", cat)
	}
	msg, _ := exec.Command("git", "-C", dir, "log", "-1", "--format=%P %s", s.Commit).Output()
	if strings.TrimSpace(string(msg)) != head+" faultline: repaired prevention state" {
		t.Fatalf("%s", msg)
	}
	// The user's branch does not move.
	if h, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output(); strings.TrimSpace(string(h)) != head {
		t.Fatalf("HEAD moved: %s", h)
	}
	if entries, _ := filepath.Glob(filepath.Join(os.TempDir(), "faultline-repair-*.index")); len(entries) != 0 {
		t.Fatalf("index left: %v", entries)
	}
}

func TestCommitRepairedWorktreeStateUnknownBase(t *testing.T) {
	setTempDir(t)
	dir, _ := gitRepo(t)
	_, err := CommitRepairedWorktreeState(dir, strings.Repeat("0", 40))
	// The read-tree stderr, untrimmed.
	if err == nil || !strings.HasPrefix(err.Error(), "fatal: ") || !strings.HasSuffix(err.Error(), "\n") {
		t.Fatalf("%v", err)
	}
	if entries, _ := filepath.Glob(filepath.Join(os.TempDir(), "faultline-repair-*.index")); len(entries) != 0 {
		t.Fatalf("index left: %v", entries)
	}
}

func scripted(verdicts ...Verdict) func() (Verdict, error) {
	i := 0
	return func() (Verdict, error) {
		v := verdicts[i]
		i++
		return v, nil
	}
}

func TestCollectRepairedRuns(t *testing.T) {
	bundleDir := filepath.Join(basesDir, "git-unbound")
	id := func(c string) Verdict {
		return Verdict{OK: true, Detail: "PASS " + c, ExecutionID: "sha256:" + strings.Repeat(c, 64), HasExecutionID: true}
	}
	runs, reasons, err := CollectRepairedRuns("wt", bundleDir, "c", "t", scripted(id("a"), id("b"), id("c")))
	if err != nil || reasons != nil || len(runs.Items()) != 3 {
		t.Fatalf("%v %v %v", runs, reasons, err)
	}
	first := runs.Items()[0]
	if keys := strings.Join(first.Obj().Keys(), ","); keys != "runId,executionId,commit,tree,verdict,witnessDigest,environmentDigest,executionTrust,executionKind" {
		t.Fatalf("keys %s", keys)
	}
	if runs.Items()[0].Get("runId").Str() == runs.Items()[1].Get("runId").Str() {
		t.Fatal("run ids not distinct")
	}

	_, reasons, _ = CollectRepairedRuns("wt", bundleDir, "c", "t", scripted(id("a"), Verdict{Detail: "boom"}))
	if len(reasons) != 1 || reasons[0] != "Repaired-state execution 2/3 failed: boom" {
		t.Fatalf("%v", reasons)
	}
	_, reasons, _ = CollectRepairedRuns("wt", bundleDir, "c", "t", scripted(Verdict{OK: true, ExecutionID: "sha256:" + strings.Repeat("A", 64), HasExecutionID: true}))
	if len(reasons) != 1 || reasons[0] != "Repaired-state execution 1/3 omitted a content-bound executionId; refusing PREVENTION_VERIFIED placeholder synthesis." {
		t.Fatalf("%v", reasons)
	}
	_, reasons, _ = CollectRepairedRuns("wt", filepath.Join(basesDir, "git-shape-heterogeneous-proof"), "c", "t", scripted())
	if len(reasons) != 1 || reasons[0] != "Cannot collect repaired runs without a homogeneous environment digest." {
		t.Fatalf("%v", reasons)
	}
	boom := errors.New("verifier threw")
	if _, _, err := CollectRepairedRuns("wt", bundleDir, "c", "t", func() (Verdict, error) { return Verdict{}, boom }); err != boom {
		t.Fatalf("%v", err)
	}
	if _, _, err := CollectRepairedRuns("wt", t.TempDir(), "c", "t", scripted()); err == nil || !strings.Contains(err.Error(), "frozen.json") {
		t.Fatalf("%v", err)
	}
}

func TestVerdictJSON(t *testing.T) {
	if got := jsjson.Stringify(Verdict{OK: true, Detail: "d"}.JSON()); got != `{"ok":true,"detail":"d"}` {
		t.Fatal(got)
	}
	if got := jsjson.Stringify(Verdict{Detail: "d", ExecutionID: "x", HasExecutionID: true}.JSON()); got != `{"ok":false,"detail":"d","executionId":"x"}` {
		t.Fatal(got)
	}
}
