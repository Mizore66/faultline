//go:build windows

package witness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A drive-letter junction is a symbolic link to libuv's lstat, so a junction
// parent is refused exactly like a symlink parent.
func TestReadModelOverlayInputJunction(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	target := filepath.Join(base, "target")
	overlayWrite(t, filepath.Join(target, "f"), []byte("x"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "j"), target).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v %s", err, out)
	}
	want := "Model overlay cannot traverse a symbolic link or non-directory: j/f"
	if _, err := ReadModelOverlayInput(root, []string{"j/f"}); err == nil || err.Error() != want {
		t.Fatalf("junction parent: %v", err)
	}
	wantLeaf := "Model overlay must be a regular non-symlink file: j"
	if _, err := ReadModelOverlayInput(root, []string{"j"}); err == nil || err.Error() != wantLeaf {
		t.Fatalf("junction leaf: %v", err)
	}
	if _, err := ReadModelOverlayInput(filepath.Join(root, "j"), []string{"f"}); err == nil {
		t.Fatal("junction root accepted")
	}
}
