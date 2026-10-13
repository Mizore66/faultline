package safedir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveSafeDirectorySegment(t *testing.T) {
	dir := t.TempDir()
	if got, ok := ResolveSafeDirectorySegment(dir); !ok || got != dir {
		t.Fatalf("%q %v", got, ok)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if got, ok := ResolveSafeDirectorySegment(link); ok || got != "" {
		t.Fatalf("symlink accepted: %q", got)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ResolveSafeDirectorySegment(file); ok {
		t.Fatal("regular file accepted")
	}
	if runtime.GOOS == "darwin" {
		if got, ok := ResolveSafeDirectorySegment("/tmp"); !ok || got != "/private/tmp" {
			t.Fatalf("%q %v", got, ok)
		}
		if got := ResolveTrustedSystemPath("/tmp/a/b"); got != "/private/tmp/a/b" {
			t.Fatal(got)
		}
		if got := RelativeTrustedSystemPath("/tmp", "/private/tmp/x"); got != "x" {
			t.Fatal(got)
		}
	}
}
