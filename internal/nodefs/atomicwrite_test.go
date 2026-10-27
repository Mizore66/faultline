package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicWriteFileLeavesNoStage(t *testing.T) {
	dir := t.TempDir()
	path, err := AtomicWriteFile(filepath.Join(dir, "a", "ledger.json"), "x\n")
	if err != nil || path != filepath.Join(dir, "a", "ledger.json") {
		t.Fatal(path, err)
	}
	names, _ := os.ReadDir(filepath.Join(dir, "a"))
	if len(names) != 1 {
		t.Fatalf("%v", names)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("%v", info.Mode())
	}
	if data, _ := os.ReadFile(path); string(data) != "x\n" {
		t.Fatalf("%q", data)
	}
}

func TestOpenExclusiveText(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	f, err := OpenExclusive(p, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer Close(f)
	if _, err := OpenExclusive(p, 0o600); err == nil || err.Error() != "EEXIST: file already exists, open '"+p+"'" {
		t.Fatalf("%v", err)
	}
}

func TestUnlinkAndClose(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	f, err := OpenExclusive(p, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := Close(f); err != nil {
		t.Fatal(err)
	}
	if err := Unlink(p); err != nil {
		t.Fatal(err)
	}
	if err := Unlink(p); err == nil || err.Error() != "ENOENT: no such file or directory, unlink '"+p+"'" {
		t.Fatalf("%v", err)
	}
}
