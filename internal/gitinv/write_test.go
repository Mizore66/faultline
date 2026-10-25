package gitinv

import (
	"os"
	"runtime"
	"testing"
)

func TestEnsureConfigCreatesThenUnchanged(t *testing.T) {
	repo := t.TempDir()
	r, err := EnsureConfig(EnsureConfigOptions{Repository: repo})
	if err != nil || r.Status != "CREATED" {
		t.Fatalf("%v %v", r.Status, err)
	}
	// Windows has no POSIX modes: Go and Node both report 0666 there.
	info, _ := os.Stat(r.Path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	r2, err := EnsureConfig(EnsureConfigOptions{Repository: repo})
	if err != nil || r2.Status != "UNCHANGED" {
		t.Fatalf("%v %v", r2.Status, err)
	}
}
