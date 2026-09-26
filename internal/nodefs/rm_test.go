//go:build unix

package nodefs

import (
	"os"
	"path/filepath"
	"testing"
)

// Expectations were captured from Node 22's rmSync(dir, { recursive: true,
// force: true }) run as an unprivileged user on the same tree.
func TestRemoveAllMatchesNodeRimraf(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	if err := RemoveAll(missing); err != nil {
		t.Fatalf("ENOENT must be ignored: %v", err)
	}
	a := filepath.Join(dir, "a")
	for _, d := range []string{"a/locked", "a/é"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	os.WriteFile(filepath.Join(a, "locked", "f"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(a, "z"), []byte("x"), 0o644)
	if os.Geteuid() != 0 {
		os.Chmod(filepath.Join(a, "locked"), 0o500)
		defer os.Chmod(filepath.Join(a, "locked"), 0o700)
		err := RemoveAll(a)
		if err == nil || err.Error() != "EACCES: permission denied, unlink '"+a+"/locked/f'" {
			t.Fatalf("got %v", err)
		}
		// Children after the failing one are left alone, as in Node.
		if _, err := os.Stat(filepath.Join(a, "z")); err != nil {
			t.Fatalf("z was removed: %v", err)
		}
		os.Chmod(filepath.Join(a, "locked"), 0o700)
	}
	if err := RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(a); !os.IsNotExist(err) {
		t.Fatalf("tree left behind: %v", err)
	}
	// A file and a dangling symlink are unlinked.
	f := filepath.Join(dir, "file")
	os.WriteFile(f, nil, 0o644)
	os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "link"))
	for _, p := range []string{f, filepath.Join(dir, "link")} {
		if err := RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
}
