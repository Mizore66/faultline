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
