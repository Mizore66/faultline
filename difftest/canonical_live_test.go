package difftest

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
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
