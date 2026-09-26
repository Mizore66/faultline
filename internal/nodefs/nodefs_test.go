package nodefs

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Mizore66/faultline/internal/jsstr"
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

// process.cwd() is the physical directory (getcwd), not the shell's $PWD.
func TestResolveUsesPhysicalCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinked cwd is a POSIX shell concern")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	phys := filepath.Join(dir, "phys", "sub")
	if err := os.MkdirAll(phys, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "logical", "link")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(filepath.Join("..", "phys", "sub"), link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)
	t.Setenv("PWD", link)
	if got, want := Resolve("../bundle"), filepath.Join(dir, "phys", "bundle"); got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

func TestDecodedUTF16LengthMatchesDecode(t *testing.T) {
	interesting := []byte{0x00, 0x41, 0x7F, 0x80, 0xBF, 0xC0, 0xC2, 0xDF, 0xE0, 0xE1, 0xED, 0xEF, 0xF0, 0xF1, 0xF4, 0xF5, 0xFF, 0xA0, 0x9F, 0x90, 0x8F}
	r := rand.New(rand.NewPCG(3, 4))
	for range 200_000 {
		b := make([]byte, r.IntN(12))
		for j := range b {
			b[j] = interesting[r.IntN(len(interesting))]
		}
		if got, want := decodedUTF16Length(b), jsstr.Length(DecodeUTF8(b)); got != want {
			t.Fatalf("% x: got %d, want %d", b, got, want)
		}
	}
}

// readdirSync decodes each raw name as UTF-8, so a name with invalid bytes
// comes back with U+FFFD and no longer names the file.
func TestReadDirNamesDecodesLikeNode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a filesystem that accepts arbitrary name bytes")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad\xffname"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := ReadDirNames(dir)
	if err != nil || len(names) != 1 || names[0] != "bad�name" {
		t.Fatalf("%q %v", names, err)
	}
	_, err = Lstat(Join(dir, names[0]))
	if err == nil || err.Error() != "ENOENT: no such file or directory, lstat '"+Join(dir, "bad�name")+"'" {
		t.Fatalf("lstat: %v", err)
	}
}

func TestReadSizeLimitsMatchNode(t *testing.T) {
	dir := t.TempDir()
	huge := filepath.Join(dir, "huge")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 31); err != nil { // sparse
		t.Skip(err)
	}
	f.Close()
	if _, err := ReadBytes(huge); err == nil || err.Error() != "File size (2147483648) is greater than 2 GiB" {
		t.Fatalf("ReadBytes: %v", err)
	}
	if _, err := ReadText(huge); err != ErrStringTooLong {
		t.Fatalf("ReadText: %v", err)
	}
}
