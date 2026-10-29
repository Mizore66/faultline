//go:build unix

package nodefs

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadDirents(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "a-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b-file", filepath.Join(dir, "C-link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "_fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDirents(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Dirent{
		{Name: "C-link", IsSymlink: true},
		{Name: "_fifo"},
		{Name: "a-dir", IsDir: true},
		{Name: "b-file", IsFile: true},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	missing := filepath.Join(dir, "missing")
	if _, err := ReadDirents(missing); err == nil || err.Error() != "ENOENT: no such file or directory, scandir '"+missing+"'" {
		t.Fatalf("%v", err)
	}
}

func TestMtimeMs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1_700_000_000, 123_456_789)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
	info, err := Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	// Node: 1700000000123.4568 (sec*1e3 + nsec/1e6).
	if got := MtimeMs(info); got != 1_700_000_000*1e3+123_456_789/1e6 {
		t.Fatalf("%v", got)
	}
}

// TestAtomicWriteFileRenameOntoDirectory pins the rename error naming both
// paths, and the stage removed in `finally`. It lives in this unix-only file
// because the code is POSIX's (ENOTEMPTY or EEXIST; Windows reports another).
func TestAtomicWriteFileRenameOntoDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ledger.json")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := AtomicWriteFile(target, "x")
	if err == nil || !strings.HasSuffix(err.Error(), ".tmp' -> '"+target+"'") || !strings.Contains(err.Error(), ", rename '"+dir+"/.ledger.json.") {
		t.Fatalf("%v", err)
	}
	names, _ := os.ReadDir(dir)
	if len(names) != 1 {
		t.Fatalf("stage left behind: %v", names)
	}
}
