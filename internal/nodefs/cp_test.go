package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCpSyncTreeCopiesModesAndRefusesExisting(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	os.MkdirAll(filepath.Join(src, "runs"), 0o750)
	os.WriteFile(filepath.Join(src, "runs", "a.json"), []byte("{}\n"), 0o640)
	if err := CpSyncTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dst, "runs", "a.json")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", info.Mode())
	}
	// Node 22.22.2 copies a directory in C++ (cpSyncCopyDir), which creates
	// it with the default mode and never chmods it: the source's 0750 is not
	// kept, the umask's mode is.
	ref := filepath.Join(t.TempDir(), "ref")
	os.Mkdir(ref, 0o777)
	refInfo, _ := os.Stat(ref)
	if info, _ := os.Stat(filepath.Join(dst, "runs")); info.Mode().Perm() != refInfo.Mode().Perm() {
		t.Fatalf("dir mode %v, want %v", info.Mode(), refInfo.Mode())
	}
	err := CpSyncTree(src, dst)
	if err == nil || !strings.HasPrefix(err.Error(), "[ERR_FS_CP_EEXIST]: Target already exists: cp returned EEXIST (") {
		t.Fatalf("%v", err)
	}
}

func TestCpSyncTreeRefusals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file types")
	}
	root := t.TempDir()
	src := filepath.Join(root, "s")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644)
	cases := []struct{ name, src, dest, want string }{
		{"same", src, src, "src and dest cannot be the same " + src},
		{"subdir", src, filepath.Join(src, "sub", "x"), "Cannot copy " + src + "/ to a subdirectory of self " + filepath.Join(src, "sub", "x")},
		{"dirToFile", src, filepath.Join(root, "f"), "Cannot overwrite non-directory " + filepath.Join(root, "f") + " with directory " + src},
		{"fileToDir", filepath.Join(root, "f"), src, "Cannot overwrite directory " + src + " with non-directory " + filepath.Join(root, "f")},
		{"missing", filepath.Join(root, "none"), filepath.Join(root, "z"), "ENOENT: no such file or directory, lstat '" + filepath.Join(root, "none") + "'"},
	}
	for _, c := range cases {
		err := CpSyncTree(c.src, c.dest)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: got %v, want %s", c.name, err, c.want)
		}
	}
}

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
