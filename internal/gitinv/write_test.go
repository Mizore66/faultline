package gitinv

import (
	"os"
	"testing"
)

func TestEnsureConfigCreatesThenUnchanged(t *testing.T) {
	repo := t.TempDir()
	r, err := EnsureConfig(EnsureConfigOptions{Repository: repo})
	if err != nil || r.Status != "CREATED" {
		t.Fatalf("%v %v", r.Status, err)
	}
	info, _ := os.Stat(r.Path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	r2, err := EnsureConfig(EnsureConfigOptions{Repository: repo})
	if err != nil || r2.Status != "UNCHANGED" {
		t.Fatalf("%v %v", r2.Status, err)
	}
}
