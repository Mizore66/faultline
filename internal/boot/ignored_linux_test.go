package boot

import (
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Mizore66/faultline/internal/sigexit"
)

// The FAULTLINE_BOOT_IGNORED record is the set's words in hex, comma
// separated, as the C constructor parses it back.
func TestFormatIgnored(t *testing.T) {
	var s sigexit.Set
	s.Add(syscall.SIGUSR2)
	s.Add(sigexit.MaxSignal)
	words := strings.Split(formatIgnored(s), ",")
	if len(words) != len(s) {
		t.Fatalf("%d words for a %d-word set", len(words), len(s))
	}
	var back sigexit.Set
	for i, w := range words {
		v, err := strconv.ParseUint(w, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		back[i] = uintptr(v)
	}
	if back != s {
		t.Fatalf("round trip %v, want %v", back, s)
	}
	for _, sig := range kernelDefault {
		if sig <= 0 || int(sig) > sigexit.MaxSignal {
			t.Errorf("kernelDefault holds %d, outside 1..%d", sig, sigexit.MaxSignal)
		}
	}
}
