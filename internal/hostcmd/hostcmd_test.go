//go:build !windows

package hostcmd

import (
	"strings"
	"testing"
	"time"
)

// The child outlives the overflow (exec sleep), so the SIGKILL lands and
// 'close' reports no exit code; a child that exits first reports its code,
// in Node too.
func TestBudgetDecodesEachChunk(t *testing.T) {
	r, err := Run("sh", []string{"-c", "printf 'aaaa'; sleep 0.1; printf 'bbbbbbbb'; exec sleep 1"}, Options{Timeout: 5 * time.Second, MaxOutputBytes: 6})
	if err != nil {
		t.Fatal(err)
	}
	if r.Stdout != "aaaabb" || !r.OutputLimitExceeded || r.ExitCode != nil {
		t.Fatalf("%+v", r)
	}
}

func TestTimeoutKills(t *testing.T) {
	r, _ := Run("sh", []string{"-c", "sleep 5"}, Options{Timeout: 100 * time.Millisecond, MaxOutputBytes: 64})
	if !r.TimedOut || r.ExitCode != nil {
		t.Fatalf("%+v", r)
	}
}

// A code point split across chunks decodes to U+FFFD on each side (one
// for the truncated lead, one per orphan continuation byte), and the
// budget counts the decoded strings (3 bytes per U+FFFD).
func TestSplitCodePoint(t *testing.T) {
	r, _ := Run("sh", []string{"-c", `printf '\360\237'; sleep 0.1; printf '\230\200'; sleep 0.1; printf 'xy' >&2`}, Options{Timeout: 5 * time.Second, MaxOutputBytes: 64})
	if r.Stdout != "\ufffd\ufffd\ufffd" || r.Stderr != "xy" || r.ExitCode == nil || *r.ExitCode != 0 || r.OutputLimitExceeded {
		t.Fatalf("%+v", r)
	}
	r, _ = Run("sh", []string{"-c", `printf '\360\237'; sleep 0.1; printf 'abcd'; exec sleep 1`}, Options{Timeout: 5 * time.Second, MaxOutputBytes: 5})
	if r.Stdout != "\ufffdab" || !r.OutputLimitExceeded || r.ExitCode != nil {
		t.Fatalf("%+v", r)
	}
}

func TestMissingExecutable(t *testing.T) {
	r, _ := Run("faultline-no-such-tool", nil, Options{Timeout: time.Second, MaxOutputBytes: 64, ErrorJoin: "\n"})
	if r.ExitCode != nil || !strings.Contains(r.Stderr, "spawn faultline-no-such-tool ENOENT") {
		t.Fatalf("%+v", r)
	}
}
