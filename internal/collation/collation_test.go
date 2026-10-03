package collation

import (
	"math/rand/v2"
	"strings"
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

// A prepared simple string's key is built a chunk at a time; comparing
// prepared strings must equal Compare, however far the keys got.
func TestComparePreparedMatchesCompare(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var alphabet []rune
	for _, c := range "aAbBzZ09 -_./\u00e9\u20ac\u4e2d\ud55c\u017f\u00df\u00c6\u00e6\u00a0\u3000\U0001d504\U0001f600\u00ff\u0300\u1e00\u01c4\x00\u00ad\u200b\ufdfa" {
		if Prepare(string(c)).key != nil {
			alphabet = append(alphabet, c)
		}
	}
	if len(alphabet) < 20 {
		t.Fatalf("simple alphabet %q", string(alphabet))
	}
	ignorable := []string{"\x00", "\u00ad", "\u200b"}
	gen := func() string {
		var b strings.Builder
		if rng.IntN(4) == 0 { // a run of zero-primary code points across a chunk edge
			for range 60 + rng.IntN(80) {
				b.WriteString(ignorable[rng.IntN(len(ignorable))])
			}
		}
		n := rng.IntN(3)
		switch n {
		case 0:
			n = rng.IntN(8)
		case 1:
			n = 60 + rng.IntN(10) // around a chunk boundary
		default:
			n = 200 + rng.IntN(100)
		}
		for range n {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		return b.String()
	}
	for range 20000 {
		a := gen()
		b := a
		if rng.IntN(2) == 0 {
			b = gen()
		} else if len(b) > 0 {
			r := []rune(b)
			r[rng.IntN(len(r))] = alphabet[rng.IntN(len(alphabet))]
			b = string(r)
		}
		pa, pb := Prepare(a), Prepare(b)
		if pa.key == nil || pb.key == nil {
			t.Fatalf("%q or %q not simple", a, b)
		}
		if got, want := ComparePrepared(pa, pb), Compare(a, b); got != want {
			t.Fatalf("ComparePrepared(%q, %q) = %d, Compare = %d", a, b, got, want)
		}
		if got, want := ComparePrepared(pa, pb), CompareKeys(MakeKey(a), MakeKey(b)); got != want {
			t.Fatalf("ComparePrepared(%q, %q) = %d, keys = %d", a, b, got, want)
		}
	}
}
