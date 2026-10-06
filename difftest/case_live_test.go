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
	// Final_Sigma looks past any run of case-ignorable code points on either
	// side: Σ between letters and 0 to 3 ignorables (BMP, supplementary, and
	// ones that are also cased) each way.
	ignorables := []string{"'", "­", "́", "‍", "ʰ", "\U0001d167", "ͅ", ".", "·"}
	var runs []string
	var build func(prefix string, n int)
	build = func(prefix string, n int) {
		runs = append(runs, prefix)
		if n == 3 {
			return
		}
		for _, ig := range ignorables {
			build(prefix+ig, n+1)
		}
	}
	build("", 0)
	var texts []string
	for _, left := range []string{"", "a", "A", "\U00010400", "1"} {
		for _, before := range runs {
			for _, after := range []string{"", "'", "́", "́‍", "ʰ'­"} {
				for _, right := range []string{"", "b", " ", "\U00010428"} {
					texts = append(texts, left+before+"Σ"+after+right)
				}
			}
		}
	}
	for start := 0; start < len(texts); start += batch {
		chunk := texts[start:min(start+batch, len(texts))]
		raw, err := c.Call("lowerMany", `{"texts":`+quoteList(chunk)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		for i, s := range chunk {
			if got := jscase.Lower(s); got != want.Items()[i].Str() {
				t.Fatalf("%s: got %s, Node %s", jsjson.Quote(s), jsjson.Quote(got), jsjson.Quote(want.Items()[i].Str()))
			}
		}
		compared += len(chunk)
	}
	t.Logf("%d strings compared", compared)
}
