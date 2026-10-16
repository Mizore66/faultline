package nodefs

import (
	"os"
	"path/filepath"
	"testing"
)

// statRaw answers from statx as libuv does (round 7 #12), with os's mode
// bits, sizes and names, for a file, a directory and a symlink.
func TestStatRawUsesStatx(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	os.WriteFile(file, []byte("abc"), 0o640)
	os.Symlink("f", filepath.Join(dir, "l"))
	os.Mkdir(filepath.Join(dir, "d"), 0o1755)
	for _, c := range []struct {
		name   string
		follow bool
	}{{"f", false}, {"f", true}, {"l", false}, {"l", true}, {"d", false}} {
		path := filepath.Join(dir, c.name)
		got, err := statRaw(path, c.follow)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := os.Lstat(path)
		if c.follow {
			want, _ = os.Stat(path)
		}
		if _, ok := got.(*statInfo); !ok && !noStatx.Load() {
			t.Errorf("%s: not a statx result: %T", c.name, got)
		}
		if got.Name() != want.Name() || got.Mode() != want.Mode() || got.IsDir() != want.IsDir() ||
			!want.IsDir() && got.Size() != want.Size() || !got.ModTime().Equal(want.ModTime()) {
			t.Errorf("%s follow=%v: got %v %v %d %v, want %v %v %d %v", c.name, c.follow,
				got.Name(), got.Mode(), got.Size(), got.ModTime(), want.Name(), want.Mode(), want.Size(), want.ModTime())
		}
	}
	if _, err := statRaw(filepath.Join(dir, "missing"), false); codeOf(err) != "ENOENT" {
		t.Errorf("missing: %v", err)
	}
	f, _ := os.Open(file)
	defer f.Close()
	if info, err := fstatFile(f); err != nil || info.Size() != 3 || info.Name() != "f" {
		t.Errorf("fstatFile: %v %v", info, err)
	}
	fd, _ := os.Open(dir)
	defer fd.Close()
	if typ, err := fstatDir(int(fd.Fd())); err != nil || typ != 0o040000 {
		t.Errorf("fstatDir: %o %v", typ, err)
	}
}
