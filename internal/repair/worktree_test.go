package repair

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) (repo, commit string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.test"}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(repo, "state.txt"), []byte("bad\n"), 0o644)
	run("add", "state.txt")
	run("commit", "-q", "-m", "bad")
	return repo, run("rev-parse", "HEAD")
}

func TestCreateCollectRemoveWorktree(t *testing.T) {
	repo, commit := initRepo(t)
	bundle := t.TempDir()
	os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte("{}\n"), 0o644)
	out := filepath.Join(t.TempDir(), "repairs", "r1")
	s, err := CreateWorktree(repo, commit, bundle, out)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.WorktreePath, ".faultline-repair", "README.md")); !strings.HasPrefix(string(b), "# FaultLine isolated repair worktree\n\nBase commit: "+commit+"\n") {
		t.Fatalf("%q", b)
	}
	os.WriteFile(filepath.Join(s.WorktreePath, "state.txt"), []byte("good\n"), 0o644)
	patch, err := CollectPatch(s.WorktreePath)
	if err != nil || !strings.Contains(patch, "-bad\n+good\n") {
		t.Fatalf("%v %q", err, patch)
	}
	c := RemoveWorktree(s.Repository, s.WorktreePath, s.ManagedRoot, true)
	if !c.Cleaned {
		t.Fatalf("%v", c.Errors)
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Fatal("worktree left behind")
	}
	if _, err := CreateWorktree(repo, "nothex", bundle, out+"x"); err == nil || err.Error() != "Repair base commit is not a Git object id: nothex" {
		t.Fatalf("%v", err)
	}
}
