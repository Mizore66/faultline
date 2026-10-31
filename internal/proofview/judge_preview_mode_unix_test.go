//go:build unix

package proofview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const previewModeChildEnv = "FAULTLINE_JUDGE_PREVIEW_MODE_ROOT"

// The writer asks for 0644 files and 0700 directories; the process umask
// decides the rest. The umask is process-wide, so the checks run in a child
// process that re-enters this same test with the env switch set, sets 0027
// and never touches the parent's umask.
func TestJudgePreviewModesUnderUmask(t *testing.T) {
	if root := os.Getenv(previewModeChildEnv); root != "" {
		previewModesChild(t, root)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestJudgePreviewModesUnderUmask$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), previewModeChildEnv+"="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mode child failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestJudgePreviewModesUnderUmask") {
		t.Fatalf("mode child did not run the mode checks to a PASS:\n%s", out)
	}
}

func previewModesChild(t *testing.T, root string) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	syscall.Umask(0o027)
	t.Chdir(root)

	if _, err := WriteJudgePreview("fresh/nested/out.html"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{"fresh": 0o700, "fresh/nested": 0o700, "fresh/nested/out.html": 0o640} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode %04o, want %04o", path, got, want)
		}
	}

	if err := os.WriteFile("existing.html", []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod("existing.html", 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat("existing.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteJudgePreview("existing.html"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat("existing.html")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("overwrite did not keep the same inode")
	}
	if got := after.Mode().Perm(); got != 0o600 {
		t.Errorf("existing file mode %04o, want 0600", got)
	}
	if after.Size() < 1000 {
		t.Errorf("existing file not rewritten: %d bytes", after.Size())
	}
}
