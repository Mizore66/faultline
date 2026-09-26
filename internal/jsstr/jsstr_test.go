package jsstr

import (
	"slices"
	"testing"
	"unicode/utf8"
)

func TestRoundTripKeepsLoneSurrogates(t *testing.T) {
	units := []uint16{'a', 0xD800, 'b', 0xDC00, 0xD83D, 0xDE00}
	s := FromUTF16(units)
	if got := ToUTF16(s); !slices.Equal(got, units) {
		t.Fatalf("round trip = %x, want %x", got, units)
	}
	if Length(s) != 6 {
		t.Fatalf("Length = %d, want 6", Length(s))
	}
}

func TestToUTF8ReplacesLoneSurrogates(t *testing.T) {
	s := FromUTF16([]uint16{'a', 0xD800, 0xD83D, 0xDE00})
	if got, want := ToUTF8(s), "a\ufffd\U0001F600"; got != want {
		t.Fatalf("ToUTF8 = %q, want %q", got, want)
	}
}

func TestToUTF8JoinsSeparatelyEncodedPair(t *testing.T) {
	// JS concatenation of a lone high and a lone low surrogate forms a pair.
	s := FromUTF16([]uint16{0xD83D}) + FromUTF16([]uint16{0xDE00})
	if got := ToUTF8(s); got != "\U0001F600" {
		t.Fatalf("ToUTF8 = %q", got)
	}
}

func TestTrimMatchesJavaScript(t *testing.T) {
	in := "\ufeff\u00a0\u2028 \t x y \r\n\u3000\u200a"
	if got := Trim(in); got != "x y" {
		t.Fatalf("Trim = %q", got)
	}
	if Trim("\u0085x") != "\u0085x" { // NEL is not JS whitespace
		t.Fatal("NEL must not be trimmed")
	}
}

func TestToUTF8AlwaysValid(t *testing.T) {
	for _, s := range []string{"a\xffb", "\xed\xa0\x80", "\xc0\xaf", "ok"} {
		if out := ToUTF8(s); !utf8.ValidString(out) {
			t.Errorf("ToUTF8(%q) = %q", s, out)
		}
	}
}
