//go:build faultline_test

package difftest

import "testing"

// TestInjectedOrdinal pins the check that keeps an ordinal fault row on the
// operation it names (synthetic traces, not oracle output).
func TestInjectedOrdinal(t *testing.T) {
	ok := "1 close(18) = 0\n1 close(18) = 0\n1 close(18) = -1 EIO (Input/output error) (INJECTED)\n"
	reader := "1 close(4) = -1 EIO (Input/output error) (INJECTED)\n1 --- SIGABRT {si_signo=SIGABRT} ---\n"
	early := "1 close(18) = -1 EIO (Input/output error) (INJECTED)\n"
	for name, c := range map[string]struct {
		trace string
		want  bool
	}{
		"third close":       {ok, true},
		"first close":       {early, false},
		"reader abort":      {reader, false},
		"nothing failed":    {"1 close(18) = 0\n", false},
		"empty":             {"", false},
		"signal lines skip": {"1 --- SIGCHLD {} ---\n" + ok, true},
	} {
		if got := injectedOrdinal(c.trace, 3); got != c.want {
			t.Errorf("%s: injectedOrdinal = %v, want %v", name, got, c.want)
		}
	}
	if o := faultOrdinals["turns-proof/turn-binding/close"]; o.ts != 3 || o.goSide != 3 || !o.onlyCalls {
		t.Errorf("binding close ordinal wiring changed: %+v", o)
	}
	if o := faultOrdinals["turns-proof/turn-pack-verify-tmp/mkdir"]; !o.from || o.ts != 19 {
		t.Errorf("verifier mkdir wiring changed: %+v", o)
	}
}
