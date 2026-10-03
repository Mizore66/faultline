package difftest

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/collation"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = jsjson.Quote(s)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func TestLiveSortLocale(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < liveCases; i++ {
		keys := gen.RandomKeys(r)
		raw, err := c.Call("sort", `{"keys":`+quoteList(keys)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := jsjson.Parse(raw)
		var want []string
		for _, item := range v.Items() {
			want = append(want, item.Str())
		}
		if got := canonical.SortLocale(keys); !slices.Equal(got, want) {
			t.Fatalf("SortLocale(%q) = %q, want %q", keys, got, want)
		}
	}
}

func TestLiveCanonical(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(7, 8))
	for i := 0; i < liveCases; i++ {
		text := gen.RandomJSONText(r)
		v, err := jsjson.Parse(text)
		if err != nil {
			continue // parse parity is TestLiveParse's job
		}
		want := call(t, c, "canonical", `{"text":`+jsjson.Quote(text)+`}`)
		got, cerr := canonical.CanonicalJSON(v)
		if !want.Field("ok").Bool() {
			if cerr == nil || cerr.Error() != want.Field("message").Str() {
				t.Fatalf("CanonicalJSON(%q) error = %v, want %q", text, cerr, want.Field("message").Str())
			}
			continue
		}
		if cerr != nil || got != want.Field("canonical").Str() {
			t.Fatalf("CanonicalJSON(%q) = %q (%v), want %q", text, got, cerr, want.Field("canonical").Str())
		}
		if d, _ := canonical.DigestJSON(v); d != want.Field("digest").Str() {
			t.Fatalf("DigestJSON(%q) = %s, want %s", text, d, want.Field("digest").Str())
		}
	}
}

// TestLiveCollationAllCodePoints sorts every code point (lone surrogates as
// single UTF-16 units) in Node and in Go and requires the same order and the
// same ties.
func TestLiveCollationAllCodePoints(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	want := call(t, c, "sortCodePoints", `{}`)
	strs := make([]string, 0, 0x110000)
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			strs = append(strs, jsstr.FromUTF16([]uint16{uint16(cp)}))
		} else {
			strs = append(strs, string(cp))
		}
	}
	got := canonical.SortLocale(strs)
	order := want.Field("order").Items()
	ties := want.Field("ties").Str()
	if len(order) != len(got) {
		t.Fatalf("Node returned %d code points, Go %d", len(order), len(got))
	}
	bad := 0
	for i, s := range got {
		if units := jsstr.ToUTF16(s); len(units) > 0 {
			gotCP := codePointOf(units)
			if float64(gotCP) != order[i].Num() {
				bad++
				if bad <= 20 {
					t.Errorf("position %d: Go U+%04X, Node U+%04X", i, gotCP, int(order[i].Num()))
				}
			}
		}
		if i > 0 {
			tie := canonical.LocaleCompare(got[i-1], s) == 0
			if tie != (ties[i-1] == '1') {
				bad++
				if bad <= 20 {
					t.Errorf("tie at %d: Go %v, Node %v", i, tie, ties[i-1] == '1')
				}
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d mismatches in the full code point order", bad)
	}
}

func codePointOf(units []uint16) rune {
	if len(units) == 2 {
		return 0x10000 + (rune(units[0])-0xD800)<<10 + (rune(units[1]) - 0xDC00)
	}
	return rune(units[0])
}

// TestLiveCollationRules builds strings around every contraction and prefix
// rule of the root data (marks inserted, reordered and dropped, precomposed
// and decomposed forms, FDD0/FDD1 sequences, shared prefixes so ICU's
// identical-prefix skip matters) and compares LocaleCompare and SortLocale
// with Node.
func TestLiveCollationRules(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	contractions, prefixes, nonStarters := collation.Rules()
	r := rand.New(rand.NewPCG(31, 32))
	mark := func() rune { return nonStarters[r.IntN(len(nonStarters))] }
	piece := func() []rune {
		switch r.IntN(6) {
		case 0:
			p := prefixes[r.IntN(len(prefixes))]
			return append(slices.Clone(p[0]), p[1]...)
		case 1:
			return []rune{mark()}
		case 2:
			return []rune{[]rune{0xFDD0, 0xFDD1, 'L', 'a', 0x4E00, 0xAC00, 0x1100, 0xD800, 0x00B7, 0x2126}[r.IntN(10)]}
		default:
			k := slices.Clone(contractions[r.IntN(len(contractions))])
			switch r.IntN(4) {
			case 0:
				k = slices.Insert(k, 1+r.IntN(len(k)), mark())
			case 1:
				r.Shuffle(len(k), func(i, j int) { k[i], k[j] = k[j], k[i] })
			case 2:
				k = k[:1+r.IntN(len(k))]
			}
			return k
		}
	}
	str := func(rs []rune) string {
		var units []uint16
		for _, x := range rs {
			if x >= 0xD800 && x <= 0xDFFF {
				units = append(units, uint16(x))
			} else {
				units = append(units, utf16.Encode([]rune{x})...)
			}
		}
		return jsstr.FromUTF16(units)
	}
	for round := 0; round < 40; round++ {
		var pairs [][2]string
		var items []string
		for range 500 {
			var p []rune
			for n := r.IntN(3); n > 0; n-- {
				p = append(p, piece()...)
			}
			a, b := slices.Clone(p), slices.Clone(p)
			for n := 1 + r.IntN(2); n > 0; n-- {
				a = append(a, piece()...)
			}
			for n := r.IntN(3); n > 0; n-- {
				b = append(b, piece()...)
			}
			pairs = append(pairs, [2]string{str(a), str(b)})
			items = append(items, str(a), str(b))
		}
		var list []string
		for _, p := range pairs {
			list = append(list, "["+jsjson.Quote(p[0])+","+jsjson.Quote(p[1])+"]")
		}
		raw, err := c.Call("compareMany", `{"pairs":[`+strings.Join(list, ",")+`]}`)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		for i, p := range pairs {
			if got := canonical.LocaleCompare(p[0], p[1]); float64(got) != want.Items()[i].Num() {
				t.Fatalf("LocaleCompare(%+q, %+q) = %d, Node %v", p[0], p[1], got, want.Items()[i].Num())
			}
		}
		raw, err = c.Call("sort", `{"keys":`+quoteList(items)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		sorted, _ := jsjson.Parse(raw)
		got := canonical.SortLocale(items)
		for i, v := range sorted.Items() {
			if got[i] != v.Str() {
				t.Fatalf("SortLocale differs from Node at %d: %+q vs %+q", i, got[i], v.Str())
			}
		}
	}
}

// TestLiveCollationMarkRuns targets ICU's FCD iterator: a contraction
// starter (or a plain letter) followed by a run of non-starters mixing
// supplementary and BMP marks in and out of canonical order, optionally a
// trailing starter; each string is compared with its NFD and NFC forms (from
// Node), with a reordering of its marks, and with strings sharing a prefix.
// Composites whose NFD starts with a contraction's second code point cover
// canonical closure.
func TestLiveCollationMarkRuns(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	contractions, _, nonStarters := collation.Rules()
	var starters []rune
	seen := map[rune]bool{}
	for _, k := range contractions {
		if !seen[k[0]] {
			seen[k[0]] = true
			starters = append(starters, k[0])
		}
	}
	starters = append(starters, 'a', 'L', 0x0438, 0xAC00, 0x1100, 0x4E00)
	var supp, bmp []rune
	for _, m := range nonStarters {
		if m > 0xFFFF {
			supp = append(supp, m)
		} else {
			bmp = append(bmp, m)
		}
	}
	r := rand.New(rand.NewPCG(41, 42))
	mark := func() rune {
		if r.IntN(2) == 0 {
			return supp[r.IntN(len(supp))]
		}
		return bmp[r.IntN(len(bmp))]
	}
	build := func() []rune {
		s := []rune{starters[r.IntN(len(starters))]}
		if r.IntN(4) == 0 {
			k := contractions[r.IntN(len(contractions))]
			s = slices.Clone(k)
		}
		for n := 1 + r.IntN(4); n > 0; n-- {
			s = append(s, mark())
		}
		if r.IntN(2) == 0 {
			s = append(s, []rune{'a', '.', 'x', 0x0438, 0x0F71}[r.IntN(5)])
		}
		return s
	}
	normalize := func(texts []string, form string) []string {
		raw, err := c.Call("normalizeMany", `{"form":"`+form+`","texts":`+quoteList(texts)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := jsjson.Parse(raw)
		out := make([]string, len(v.Items()))
		for i, x := range v.Items() {
			out[i] = x.Str()
		}
		return out
	}
	compared := 0
	for round := 0; round < 60; round++ {
		var base []string
		var shuffled []string
		for range 300 {
			s := build()
			base = append(base, string(s))
			m := slices.Clone(s)
			if len(m) > 2 {
				tail := m[1:]
				r.Shuffle(len(tail), func(i, j int) { tail[i], tail[j] = tail[j], tail[i] })
			}
			shuffled = append(shuffled, string(m))
		}
		nfd, nfc := normalize(base, "NFD"), normalize(base, "NFC")
		var pairs [][2]string
		for i := range base {
			other := base[r.IntN(len(base))]
			pairs = append(pairs, [2]string{base[i], nfd[i]}, [2]string{base[i], nfc[i]}, [2]string{nfd[i], shuffled[i]},
				[2]string{base[i], shuffled[i]}, [2]string{base[i] + "a", nfd[i] + "b"}, [2]string{base[i], other})
		}
		var list []string
		for _, p := range pairs {
			list = append(list, "["+jsjson.Quote(p[0])+","+jsjson.Quote(p[1])+"]")
		}
		raw, err := c.Call("compareMany", `{"pairs":[`+strings.Join(list, ",")+`]}`)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		for i, p := range pairs {
			if got := canonical.LocaleCompare(p[0], p[1]); float64(got) != want.Items()[i].Num() {
				t.Fatalf("LocaleCompare(%+q, %+q) = %d, Node %v", p[0], p[1], got, want.Items()[i].Num())
			}
		}
		compared += len(pairs)
		items := append(append(slices.Clone(base), nfd...), shuffled...)
		raw, err = c.Call("sort", `{"keys":`+quoteList(items)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		sorted, _ := jsjson.Parse(raw)
		got := canonical.SortLocale(items)
		for i, v := range sorted.Items() {
			if got[i] != v.Str() {
				t.Fatalf("SortLocale differs from Node at %d: %+q vs %+q", i, got[i], v.Str())
			}
		}
	}
	t.Logf("%d pairs compared", compared)
}

// Tibetan composite vowels (U+0F73, U+0F75, U+0F81 decompose to U+0F71 plus
// a vowel sign, and are not FCD) exercise ICU's incremental FCD check, which
// normalizes some of these runs and leaves others as they are.
func TestLiveCollationTibetan(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	alphabet := []rune{'a', 0x0F40, 0x0F71, 0x0F72, 0x0F73, 0x0F74, 0x0F75, 0x0F7A, 0x0F80, 0x0F81, 0x0F90, 0x0FB2, 0x0FB3, 0x0F77, 0x0F79, 0x0334}
	r := rand.New(rand.NewPCG(71, 72))
	word := func() string {
		s := []rune{alphabet[r.IntN(3)]}
		for n := 1 + r.IntN(6); n > 0; n-- {
			s = append(s, alphabet[r.IntN(len(alphabet))])
		}
		return string(s)
	}
	compared := 0
	for range 40 {
		var list []string
		var pairs [][2]string
		for range 2000 {
			a := word()
			b := a + string(alphabet[r.IntN(len(alphabet))])
			if r.IntN(2) == 0 {
				b = word()
			}
			pairs = append(pairs, [2]string{a, b})
			list = append(list, "["+jsjson.Quote(a)+","+jsjson.Quote(b)+"]")
		}
		raw, err := c.Call("compareMany", `{"pairs":[`+strings.Join(list, ",")+`]}`)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		for i, p := range pairs {
			if got := canonical.LocaleCompare(p[0], p[1]); float64(got) != want.Items()[i].Num() {
				t.Fatalf("LocaleCompare(%+q, %+q) = %d, Node %v", p[0], p[1], got, want.Items()[i].Num())
			}
		}
		compared += len(pairs)
	}
	t.Logf("%d pairs compared", compared)
}

// Long, nearly sorted inputs make V8's TimSort gallop (runs merged with
// minGallop adaptation), which short random key lists never reach.
func TestLiveSortLocaleLong(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(81, 82))
	for round := range 12 {
		n := 2000 + r.IntN(3000)
		keys := make([]string, n)
		for i := range keys {
			keys[i] = fmt.Sprintf("k%06d", i*7)
		}
		switch round % 4 {
		case 0: // a few swaps
			for range 20 {
				i, j := r.IntN(n), r.IntN(n)
				keys[i], keys[j] = keys[j], keys[i]
			}
		case 1: // interleaved sorted runs
			slices.SortFunc(keys, func(a, b string) int { return strings.Compare(a[len(a)-1:], b[len(b)-1:]) })
		case 2: // reversed blocks
			for i := 0; i+100 <= n; i += 100 {
				slices.Reverse(keys[i : i+100])
			}
		case 3: // mixed-case and accented variants (locale order differs from code units)
			for i := range keys {
				switch r.IntN(4) {
				case 0:
					keys[i] = strings.ToUpper(keys[i])
				case 1:
					keys[i] = "é" + keys[i]
				}
			}
		}
		raw, err := c.Call("sort", `{"keys":`+quoteList(keys)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := jsjson.Parse(raw)
		got := canonical.SortLocale(keys)
		for i, item := range v.Items() {
			if got[i] != item.Str() {
				t.Fatalf("round %d: SortLocale differs from Node at %d: %q vs %q", round, i, got[i], item.Str())
			}
		}
	}
}

// TestLiveSortLocaleSharedPrefix: simple keys that share long prefixes, with
// first differences around the 64-code-point chunks of the lazy keys and
// after runs of 64 or more zero-primary code points. Sorted and compared
// pairwise against Node (round 5, row 11 and the §1 P3 test gap).
func TestLiveSortLocaleSharedPrefix(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(111, 112))
	prefixes := []string{
		strings.Repeat("a", 1000),
		strings.Repeat("ﷺ", 300),   // 18 collation elements each
		strings.Repeat("\x00", 65), // completely ignorable
		strings.Repeat("\u00ad", 130),
		strings.Repeat("x\U0001D504", 100),
		strings.Repeat("\u200b", 64) + "k",
	}
	tails := []string{"", "b", "B", "é", "\x00b", "\u00adb", "1", "10", "2", strings.Repeat("\x00", 70) + "c", "ﷺ", "z\u00ad"}
	var keys []string
	for _, prefix := range prefixes {
		cps := []rune(prefix)
		for _, cut := range []int{62, 63, 64, 65, 66, 126, 127, 128, 129, 130, len(cps)} {
			if cut > len(cps) {
				continue
			}
			head := string(cps[:cut])
			for _, tail := range tails {
				keys = append(keys, head+tail)
			}
		}
	}
	keys = append(keys, "\x00b", strings.Repeat("\x00", 65))
	for _, k := range keys {
		if !collation.Prepare(k).Simple() {
			t.Fatalf("%+q is not simple: the test would not reach the lazy keys", k)
		}
	}
	for round := range 4 {
		order := slices.Clone(keys)
		switch round {
		case 1:
			slices.Reverse(order)
		case 2, 3:
			r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		}
		raw, err := c.Call("sort", `{"keys":`+quoteList(order)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := jsjson.Parse(raw)
		got := canonical.SortLocale(order)
		for i, item := range v.Items() {
			if got[i] != item.Str() {
				t.Fatalf("round %d: SortLocale differs from Node at %d: %+q vs %+q", round, i, got[i], item.Str())
			}
		}
	}
	var pairs [][2]string
	for range 3000 {
		pairs = append(pairs, [2]string{keys[r.IntN(len(keys))], keys[r.IntN(len(keys))]})
	}
	var b strings.Builder
	b.WriteString(`{"pairs":[`)
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("[" + jsjson.Quote(p[0]) + "," + jsjson.Quote(p[1]) + "]")
	}
	b.WriteString("]}")
	raw, err := c.Call("compareMany", b.String())
	if err != nil {
		t.Fatal(err)
	}
	v, _ := jsjson.Parse(raw)
	for i, item := range v.Items() {
		want := int(item.Num())
		a, bb := pairs[i][0], pairs[i][1]
		if got := collation.ComparePrepared(collation.Prepare(a), collation.Prepare(bb)); got != want {
			t.Fatalf("ComparePrepared(%+q, %+q) = %d, Node %d", a, bb, got, want)
		}
		if got := collation.Compare(a, bb); got != want {
			t.Fatalf("Compare(%+q, %+q) = %d, Node %d", a, bb, got, want)
		}
	}
}
