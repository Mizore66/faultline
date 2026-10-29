//go:build unix

package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The symlink-tree copy is modelled for POSIX only (cppCanonical and
// cppWeaklyCanonical walk "/"-rooted paths); the Windows model is a known
// unimplemented limit (difftest/KNOWN_DIFFERENCES.md, "cpSync is a Linux
// model"), not a Windows parity pass. The test also pins the "/etc/hosts"
// absolute target and a 0o400 mode, which Windows lacks.
func TestCpSyncTreeLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root := t.TempDir()
	src := filepath.Join(root, "s")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o400)
	os.Symlink("f", filepath.Join(src, "rel"))
	os.Symlink("/etc/hosts", filepath.Join(src, "abs"))
	dst := filepath.Join(root, "d")
	if err := CpSyncTree(src, dst); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(src)
	if got, _ := os.Readlink(filepath.Join(dst, "rel")); got != filepath.Join(real, "f") {
		t.Errorf("rel -> %q", got)
	}
	if got, _ := os.Readlink(filepath.Join(dst, "abs")); got != "/etc/hosts" && got != "/private/etc/hosts" {
		t.Errorf("abs -> %q", got)
	}
	if info, _ := os.Stat(filepath.Join(dst, "f")); info.Mode().Perm() != 0o400 {
		t.Errorf("mode %v", info.Mode())
	}
}
