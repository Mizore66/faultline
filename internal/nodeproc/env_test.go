package nodeproc

import (
	"runtime"
	"slices"
	"strings"
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

// TestArrayIndexEnvKeys pins which inherited environment names
// {...process.env} drops: canonical array indices 0 to 4294967294 only
// (r9-3 N6). Node 22 with 4294967295=a 4294967294=b 01=c 0=d 10=e in its
// environment passes 01=c and 4294967295=a to the child, nothing else.
func TestArrayIndexEnvKeys(t *testing.T) {
	for key, want := range map[string]bool{
		"0": true, "10": true, "4294967294": true, "4294967295": false, "4294967296": false,
		"01": false, "": false, "-1": false, "99999999999": false, "1a": false,
	} {
		if got := isArrayIndex(key); got != want {
			t.Errorf("isArrayIndex(%q) = %v, want %v", key, got, want)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	for k, v := range map[string]string{"4294967295": "a", "4294967294": "b", "01": "c", "0": "d", "10": "e"} {
		t.Setenv(k, v)
	}
	var got []string
	for _, entry := range nodeEnv() {
		for _, k := range []string{"4294967295=", "4294967294=", "01=", "0=", "10="} {
			if strings.HasPrefix(entry, k) {
				got = append(got, entry)
			}
		}
	}
	slices.Sort(got)
	if want := []string{"01=c", "4294967295=a"}; !slices.Equal(got, want) {
		t.Errorf("child env keeps %q, want %q", got, want)
	}
}
