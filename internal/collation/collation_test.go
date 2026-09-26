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
