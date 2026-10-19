//go:build darwin || linux

package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRemoveAllUnlinkEPERM pins rimraf's EPERM path off Windows: a child
// whose unlink fails with EPERM is tried with rmdir (r9-3 F6). Expected
// results from Node 22's fs/promises.rm and rmSync(dir, {recursive,
// force}) on a directory holding an immutable file (macOS chflags uchg,
// Linux chattr +i, Node 22.22.2).
func TestRemoveAllUnlinkEPERM(t *testing.T) {
	for _, rm := range []struct {
		name string
		fn   func(string) error
	}{{"RemoveAllAsync", RemoveAllAsync}, {"RemoveAll", RemoveAll}} {
		dir := t.TempDir()
		d := filepath.Join(dir, "d")
		if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"f", "sub/g", "z"} {
			if err := os.WriteFile(filepath.Join(d, f), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		f := filepath.Join(d, "f")
		if reason := setImmutable(f, true); reason != "" {
			t.Skip(reason)
		}
		err := rm.fn(d)
		setImmutable(f, false)
		// macOS: unlink EPERM, rmdir ENOTDIR, so the unlink error is
		// reported. Linux checks the immutable flag first, so rmdir fails
		// EPERM too and rimraf scans the file: ENOTDIR from scandir.
		want := "EPERM: operation not permitted, unlink '" + f + "'"
		if runtime.GOOS == "linux" {
			want = "ENOTDIR: not a directory, scandir '" + f + "'"
		}
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %s", rm.name, err, want)
		}
		if err := RemoveAll(d); err != nil {
			t.Fatal(err)
		}
	}
}
