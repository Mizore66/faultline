package jsexc

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// MaxStringLength is V8's String::kMaxLength on 64-bit platforms, in UTF-16
// units.
const MaxStringLength = 0x1fffffe8

// ErrInvalidStringLength is the RangeError V8 throws when a string would
// exceed MaxStringLength.
var ErrInvalidStringLength = errors.New("Invalid string length")

// Concat is JS string concatenation (+ or a template literal): it throws
// ErrInvalidStringLength when the result would be too long.
func Concat(parts ...string) string {
	n := 0
	for _, p := range parts {
		n += jsstr.UTF16Len(p)
	}
	if n > MaxStringLength {
		Throw(ErrInvalidStringLength)
	}
	return strings.Join(parts, "")
}

// Join is Array.prototype.join(sep), with the same length limit.
func Join(parts []string, sep string) string {
	n := 0
	for i, p := range parts {
		if i > 0 {
			n += jsstr.UTF16Len(sep)
		}
		n += jsstr.UTF16Len(p)
		if n > MaxStringLength {
			Throw(ErrInvalidStringLength)
		}
	}
	return strings.Join(parts, sep)
}

// Message reads err.message the way TS does. Errors whose JS message is a
// getter that can throw (zod's ZodError) implement Message; reading it may
// throw, like the getter.
func Message(err error) string {
	if m, ok := err.(interface{ Message() string }); ok {
		return m.Message()
	}
	return err.Error()
}
