package difftest

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// TestLiveLowerCase compares toLocaleLowerCase("en-US") with Node for every
// code point (lone surrogates included) alone and in the Final_Sigma
// contexts around Σ, where ICU tests Case_Ignorable before Cased.
func TestLiveLowerCase(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	cp := func(r rune) string {
		if r >= 0xD800 && r <= 0xDFFF {
			return jsstr.FromUTF16([]uint16{uint16(r)})
		}
		return jsstr.FromUTF16(utf16.Encode([]rune{r}))
	}
	const batch = 1 << 14
	compared := 0
	for start := rune(0); start <= 0x10FFFF; start += batch {
		var texts []string
		for r := start; r < start+batch && r <= 0x10FFFF; r++ {
			x := cp(r)
			texts = append(texts, x, x+"Σ", "Σ"+x, "a"+x+"Σ", "aΣ"+x, x+"Σ"+x, "Σ"+x+"a")
		}
		raw, err := c.Call("lowerMany", `{"texts":`+quoteList(texts)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		var bad []string
		for i, s := range texts {
			if got := jscase.Lower(s); got != want.Items()[i].Str() {
				bad = append(bad, jsjson.Quote(s)+": got "+jsjson.Quote(got)+", Node "+jsjson.Quote(want.Items()[i].Str()))
			}
		}
		if len(bad) > 0 {
			t.Fatalf("%d differences, first: %s", len(bad), strings.Join(bad[:min(5, len(bad))], "; "))
		}
		compared += len(texts)
	}
	t.Logf("%d strings compared", compared)
}
