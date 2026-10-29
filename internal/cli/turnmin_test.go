package cli

import "testing"

func TestTurnMinRejectsBadTransitionIndex(t *testing.T) {
	for _, v := range []string{"-1", "1.5", "abc", "Infinity"} {
		_, err := minimizeFromTurnInvestigation(turnMinOptions{transitionIndex: &v})
		if err == nil || err.Error() != "--transition must be a non-negative integer." {
			t.Errorf("%s: %v", v, err)
		}
	}
}

// TestTurnMinIndexForms: Number("") is 0 and " 1 ", "1e0" and "0x1" are 1,
// so each reaches the bridge's index lookup.
func TestTurnMinIndexForms(t *testing.T) {
	for v, want := range map[string]string{"": "0", " 1 ": "1", "1e0": "1", "0x1": "1", "-0": "0"} {
		_, err := minimizeFromTurnInvestigation(turnMinOptions{transitionIndex: &v})
		if err == nil || err.Error() != "Turn investigation has no transition at index "+want+" (0 available)." {
			t.Errorf("%q: %v", v, err)
		}
	}
}
