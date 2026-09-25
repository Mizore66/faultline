package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDecodeUTF8MatchesNode(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0xE2, 0x82}, "\ufffd"},
		{[]byte{0xF0, 0x9F, 0x98}, "\ufffd"},
		{[]byte{0xC0, 0xAF}, "\ufffd\ufffd"},
		{[]byte{0xED, 0xA0, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, "\ufffd\ufffd\ufffd\ufffd"},
		{[]byte{0xE2, 0x82, 0x41}, "\ufffdA"},
		{[]byte{0xFF}, "\ufffd"},
		{[]byte{0xEF, 0xBB, 0xBF, 0x41}, "\ufeffA"},
		{[]byte{0xF0, 0x9F, 0x98, 0x80}, "\U0001F600"},
		{[]byte{0xE0, 0x80, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0x61, 0xF1, 0x80, 0x80, 0xE1, 0x80, 0xC2, 0x62}, "a\ufffd\ufffd\ufffdb"},
	}
	for _, c := range cases {
		if got := DecodeUTF8(c.in); got != c.want {
			t.Errorf("DecodeUTF8(% x) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNodeErrorText(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	if _, err := ReadText(missing); err == nil || err.Error() != "ENOENT: no such file or directory, open '"+missing+"'" {
		t.Fatalf("ReadText error = %v", err)
	}
	if _, err := ReadBytes(dir); err == nil || err.Error() != "EISDIR: illegal operation on a directory, read" {
		t.Fatalf("ReadBytes(dir) error = %v", err)
	}
	if _, err := Lstat(missing); err == nil || err.Error() != "ENOENT: no such file or directory, lstat '"+missing+"'" {
		t.Fatalf("Lstat error = %v", err)
	}
	if _, err := ReadDirNames(missing); err == nil || err.Error() != "ENOENT: no such file or directory, scandir '"+missing+"'" {
		t.Fatalf("ReadDirNames error = %v", err)
	}
	file := filepath.Join(dir, "f")
	os.WriteFile(file, nil, 0o600)
	if _, err := ReadText(filepath.Join(file, "x")); err == nil || err.Error() != "ENOTDIR: not a directory, open '"+filepath.Join(file, "x")+"'" {
		if runtime.GOOS != "windows" {
			t.Fatalf("ENOTDIR error = %v", err)
		}
	}
}

func TestReadDirNamesIsByteSorted(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b", "Z", "_", "1", "é"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o600)
	}
	got, _ := ReadDirNames(dir)
	want := []string{"1", "Z", "_", "b", "é"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReadDirNames = %q", got)
		}
	}
}

func TestRelative(t *testing.T) {
	if Relative("/a/b", "/a/b") != "" {
		t.Fatal(`Relative of equal paths must be ""`)
	}
	if runtime.GOOS != "windows" && Relative("/a/b", "/a/c/d") != "../c/d" {
		t.Fatal("Relative")
	}
}

// A name Windows rejects outright (ERROR_INVALID_NAME) must still read as
// ENOENT, as Node reports it there and as Linux reports it for the same name.
func TestInvalidWindowsNameIsENOENT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frozen.json\v")
	if _, err := Lstat(path); err == nil || err.Error() != "ENOENT: no such file or directory, lstat '"+path+"'" {
		t.Fatalf("Lstat error = %v", err)
	}
	if _, err := ReadText(path); err == nil || err.Error() != "ENOENT: no such file or directory, open '"+path+"'" {
		t.Fatalf("ReadText error = %v", err)
	}
}
