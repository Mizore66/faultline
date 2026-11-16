//go:build linux || darwin || windows

package proofview

import (
	"errors"
	"fmt"
	"testing"
)

// Test-first for the proposed comparable enum unixRemainderError (uint8-based;
// 0 is not a valid member). Strings are independent literals copied from the
// current sentinel declarations, so a refactor cannot change them silently.

var urErrCases = []struct {
	name string
	enum unixRemainderError
	old  error
	text string
}{
	{"Busy", urErrBusy, errUnixRemainderBusy, "unix remainder: busy"},
	{"NotReady", urErrNotReady, errUnixRemainderNotReady, "unix remainder: token not ready"},
	{"TooManyVectors", urErrTooManyVectors, errUnixRemainderTooManyVectors, "unix remainder: too many vectors"},
	{"TooLarge", urErrTooLarge, errUnixRemainderTooLarge, "unix remainder: input too large"},
	{"Closed", urErrClosed, errUnixRemainderClosed, "unix remainder: closed"},
	{"InvalidNative", urErrInvalidNative, errUnixRemainderInvalidNative, "unix remainder: invalid native write result"},
	{"NoAttempt", urErrNoAttempt, errUnixRemainderNoAttempt, "unix remainder: raw write returned without an attempt"},
}

// Mutant caught: any Error() text drift, swapped/duplicated strings in the table.
func TestUnixRemainderErrorEnumStrings(t *testing.T) {
	for _, c := range urErrCases {
		if got := c.enum.Error(); got != c.text {
			t.Errorf("%s: Error()=%q, want %q", c.name, got, c.text)
		}
	}
}

// Mutant caught: sentinels left as distinct errors.New values (== fails), enum
// not comparable as an interface value, or members aliasing one another.
func TestUnixRemainderErrorSentinelsEqualEnum(t *testing.T) {
	for i, c := range urErrCases {
		if c.old != error(c.enum) {
			t.Errorf("%s: sentinel %v != error(enum) %v", c.name, c.old, c.enum)
		}
		if error(c.enum) != c.old {
			t.Errorf("%s: error(enum) != sentinel (asymmetric)", c.name)
		}
		for j, d := range urErrCases {
			if i != j && error(c.enum) == error(d.enum) {
				t.Errorf("%s and %s compare equal", c.name, d.name)
			}
		}
	}
}

// Mutant caught: Is-less wrapping behaviour regressions: errors.Is must match
// the sentinel and the enum directly, through fmt.Errorf %w, and must not
// match a different member.
func TestUnixRemainderErrorIsDirectAndWrapped(t *testing.T) {
	for i, c := range urErrCases {
		other := urErrCases[(i+1)%len(urErrCases)]
		wrapped := fmt.Errorf("context: %w", c.old)
		wrappedEnum := fmt.Errorf("context: %w", error(c.enum))
		for label, err := range map[string]error{"direct sentinel": c.old, "direct enum": c.enum, "wrapped sentinel": wrapped, "wrapped enum": wrappedEnum} {
			if !errors.Is(err, c.old) || !errors.Is(err, c.enum) {
				t.Errorf("%s/%s: errors.Is failed for its own member", c.name, label)
			}
			if errors.Is(err, other.old) || errors.Is(err, other.enum) {
				t.Errorf("%s/%s: errors.Is matched %s", c.name, label, other.name)
			}
		}
	}
}

// Mutant caught: panic/empty/derived text for out-of-range values; 0 treated as
// a valid member.
func TestUnixRemainderErrorUnknownValues(t *testing.T) {
	const want = "unix remainder: unknown error"
	for _, v := range []unixRemainderError{0, 255} {
		if got := v.Error(); got != want {
			t.Errorf("unixRemainderError(%d).Error()=%q, want %q", uint8(v), got, want)
		}
		for _, c := range urErrCases {
			if error(v) == error(c.enum) {
				t.Errorf("unknown value %d equals %s", uint8(v), c.name)
			}
		}
	}
}
