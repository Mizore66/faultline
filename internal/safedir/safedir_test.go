package safedir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Mizore66/faultline/internal/jsexc"
)

func TestEnsureRealDirectoryTree(t *testing.T) {
	dir, rerr := filepath.EvalSymlinks(t.TempDir())
	if rerr != nil {
		t.Fatal(rerr)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := jsexc.Try(func() { EnsureRealDirectoryTree(nested, "root: ", "walk: ") }); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(nested); err != nil || !st.IsDir() {
		t.Fatalf("not created: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(nested, link); err != nil {
		t.Skip("symlink unavailable")
	}
	err := jsexc.Try(func() { EnsureRealDirectoryTree(filepath.Join(link, "c"), "root: ", "walk: ") })
	if err == nil || err.Error() != "walk: "+link {
		t.Fatalf("symlink: %v", err)
	}
	file := filepath.Join(dir, "file")
	if werr := os.WriteFile(file, nil, 0o600); werr != nil {
		t.Fatal(werr)
	}
	err = jsexc.Try(func() { EnsureRealDirectoryTree(filepath.Join(file, "c"), "root: ", "walk: ") })
	if err == nil || err.Error() != "walk: "+file {
		t.Fatalf("file: %v", err)
	}
}

func TestResolveSafeDirectorySegment(t *testing.T) {
	dir := t.TempDir()
	if got, ok, err := ResolveSafeDirectorySegment(dir); err != nil || !ok || got != dir {
		t.Fatalf("%q %v %v", got, ok, err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := ResolveSafeDirectorySegment(link); ok || got != "" {
		t.Fatalf("symlink accepted: %q", got)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ResolveSafeDirectorySegment(file); ok {
		t.Fatal("regular file accepted")
	}
	if runtime.GOOS == "darwin" {
		if got, ok, _ := ResolveSafeDirectorySegment("/tmp"); !ok || got != "/private/tmp" {
			t.Fatalf("%q %v", got, ok)
		}
		if got, _ := ResolveTrustedSystemPath("/tmp/a/b"); got != "/private/tmp/a/b" {
			t.Fatal(got)
		}
		if got, _ := RelativeTrustedSystemPath("/tmp", "/private/tmp/x"); got != "x" {
			t.Fatal(got)
		}
	}
}

func TestResolveSafeDirectorySegmentPropagatesNodeError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := t.TempDir()
	// A 0000 directory refuses lstat beneath it on POSIX only: Windows
	// ignores the mode (no EACCES to propagate there), so only the ENOENT
	// half runs on Windows.
	if runtime.GOOS != "windows" {
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(locked, 0o755)
		path := filepath.Join(locked, "child")
		_, ok, err := ResolveSafeDirectorySegment(path)
		want := "EACCES: permission denied, lstat '" + path + "'"
		if ok || err == nil || err.Error() != want {
			t.Fatalf("ok=%v err=%v, want %s", ok, err, want)
		}
	}
	if _, _, err := ResolveSafeDirectorySegment(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("ENOENT must propagate")
	}
}
