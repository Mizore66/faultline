package nodeproc

import (
	"slices"
	"testing"
)

// On Windows, normalizeSpawnArguments sorts the names and keeps the first
// of those that uppercase alike.
func TestWindowsEnvDedupe(t *testing.T) {
	got := windowsEnvDedupe([]string{"tmp", "TMP", "Path", "PATH", "b", "=C:"})
	want := []string{"=C:", "PATH", "TMP", "b"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Node reads a Windows value through uv_os_getenv (WTF-8) and V8's UTF-8
// decoder: a lone surrogate becomes three U+FFFD (round 5, row 13).
func TestDecodeEnvValue(t *testing.T) {
	if got := decodeEnvValue([]uint16{'a', 0xD800, 'b', 0xD83D, 0xDE00}); got != "a\uFFFD\uFFFD\uFFFDb\U0001F600" {
		t.Errorf("got %+q", got)
	}
}
