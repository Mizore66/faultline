package collation

import (
	"testing"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// Signs captured from Node 22.22.2 (ICU 78.2) "a".localeCompare("b").
func TestMatchesNodeICU(t *testing.T) {
	lone := func(u uint16) string { return jsstr.FromUTF16([]uint16{u}) }
	cases := []struct {
		a, b string
		want int
	}{
		{"a", "A", -1},
		{"a", "á", -1},
		{"á", "á", 0},
		{"áb", "ab", 1},
		{"a", "\U0001F923", 1}, // emoji sorts before letters
		{"视图", "神经", -1},       // radical-stroke Han order, not code point order
		{"٣", "३", 0},          // Arabic-Indic three vs Devanagari three
		{"₿", "a", -1},
		{lone(0xD800), lone(0xDC00), -1},
		// ICU normalizes only segments that fail the FCD check and matches
		// contractions on that text (Gurung Khema, Tulu-Tigalari, Kirat Rai,
		// out-of-order supplementary marks).
		{"\U0001611E\U00016121", "\U00016121", -1},
		{"\U0001611E\U00016121", "\U0001611E\U0001611E\U0001611E", -1},
		{"\U000113C2\U000113C5", "\U000113C2\U000113C2\U000113B8", -1},
		{"\u0DD9\u0300\U0001612F\u0DCA", "\u0DD9\u0DCA\u0300\U0001612F", -1},
		// FDD0 contractions are not in the collation data; FDD1 ones match
		// the literal text, and FDD1 5B57 sorts below every Han character.
		{"\uFDD0A", "a", 1},
		{"\uFDD04", "5", 1},
		{"\uFDD1\u5B57", "\u4E00", -1},
		{"\uFDD1\u2126", "\u03A9", 1},
		{"\uFDD1\u03A9", "\u03A9", -1},
		{"\uFDD1\uAC00", "\uAC00", -1},
		// The identical prefix is skipped back to a safe code point, so the
		// FDD1 contraction is not used here (and ICU's compare is not
		// transitive: both are 0/1 against the NFD form).
		{"\uFDD1\u1FAF", "\uFDD1\u1FAF\u05A3", 0},
		{"\uFDD1\u1FAF", "\uFDD1\u03A9\u0314\u0342\u0345", 1},
		{"\u4E00", "\U00020000", -1},
		{"\u6958", "\U00026F92", -1},
		{lone(0xD800), "�", -1},
		{lone(0xD800), "͸", 1},
		{"͸", "一", 1}, // unassigned after Han
		{"͸", "�", -1},
		{"￿", "�", 1},
		{"￾", "\u0000", 1},
		{"root_digest", "root-digest", -1},
		{"root-digest", "rootDigest", -1},
		{"_x", "-x", -1},
		{"1", "10", -1},
		{"10", "2", -1},
		{"ٙ", "͍ٙ", 1}, // NFD reorders the lower-class mark first
		{"L·", "L", 1},
		{"가", "가", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%+q, %+q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%+q, %+q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}
