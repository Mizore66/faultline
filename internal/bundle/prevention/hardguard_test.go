package prevention

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
)

func TestHardGuardDigestsDedupeAndSort(t *testing.T) {
	got := HardGuardDigestsFromStrings([]string{"b", "a", "b"})
	want := []string{"sha256:" + canonical.SHA256Hex("a"), "sha256:" + canonical.SHA256Hex("b")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
	if got := HardGuardDigestsFromStrings(nil); got == nil || len(got) != 0 {
		t.Fatalf("%#v", got)
	}
}

func TestHardGuardSidecarDigestIsTheFileBytes(t *testing.T) {
	dir := t.TempDir()
	digest, err := WriteHardGuardDigestSidecar(dir, []string{"sha256:" + canonical.SHA256Hex("a")})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "hard-guard-digests.json"))
	if digest != "sha256:"+canonical.SHA256HexBytes(b) || string(b[:14]) != "{\n  \"digests\":" {
		t.Fatalf("%s %q", digest, b)
	}
	if _, err := WriteHardGuardDigestSidecar(filepath.Join(dir, "missing"), nil); err == nil {
		t.Fatal("wrote into a missing directory")
	}
}
