package snapgc

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// git is the test's own git, without the developer's global or system
// configuration (signing, hooks, templates).
func git(t *testing.T, repository string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repository}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestCaptureCleanAndRefuseDirty ports tests/git-snapshot.test.ts:14.
func TestCaptureCleanAndRefuseDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repository := t.TempDir()
	git(t, repository, "init", "-q")
	git(t, repository, "config", "user.email", "faultline@example.invalid")
	git(t, repository, "config", "user.name", "FaultLine test")
	os.WriteFile(filepath.Join(repository, "sample.txt"), []byte("stable\n"), 0o644)
	git(t, repository, "add", "sample.txt")
	git(t, repository, "commit", "-qm", "fixture")
	snapshot, err := CaptureCleanGitSnapshot(repository)
	if err != nil {
		t.Fatal(err)
	}
	hex40 := regexp.MustCompile(`^[a-f0-9]{40}$`)
	if !snapshot.Get("clean").Bool() || !hex40.MatchString(snapshot.Get("headCommit").Str()) || !hex40.MatchString(snapshot.Get("treeDigest").Str()) ||
		!regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(snapshot.Get("digest").Str()) {
		t.Fatalf("%v", snapshot)
	}
	keys := strings.Join(snapshot.Obj().Keys(), ",")
	if keys != "schemaVersion,recorder,repositoryName,headCommit,treeDigest,capturedAt,clean,digest" {
		t.Errorf("keys %s", keys)
	}
	file, err := WriteGitSidecarSnapshot(repository, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repository, ".faultline", "recordings", strings.TrimPrefix(snapshot.Get("digest").Str(), "sha256:")+".json"); file != want {
		t.Errorf("file %s, want %s", file, want)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repository, "untracked.txt"), []byte("dirty\n"), 0o644)
	if _, err := CaptureCleanGitSnapshot(repository); err == nil || !strings.Contains(err.Error(), "clean worktree") {
		t.Fatalf("%v", err)
	}
}
