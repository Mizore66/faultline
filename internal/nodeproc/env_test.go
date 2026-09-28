package nodeproc

import (
	"slices"
	"testing"
)

// On Windows, normalizeSpawnArguments sorts the names and keeps the first
// of those that uppercase alike.
func TestWindowsEnvDedupe(t *testing.T) {
	got := windowsEnvDedupe([]string{"tmp=a", "TMP=b", "Path=x", "PATH=y", "b=1", "=C:=C:\\x"})
	want := []string{"=C:=C:\\x", "PATH=y", "TMP=b", "b=1"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
