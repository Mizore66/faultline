// Package gen holds Go-side random input generators for live oracle tests.
package gen

import (
	"math"
	"math/rand/v2"
	"strings"
)

var stringPieces = []string{
	"a", "Z", "_", "-", ".", "/", "0", "9", " ", "é", "ß", "中", "😀",
	`\n`, `\t`, `\"`, `\\`, `\/`, `\u00e9`, `\ud800`, `\udc00`, `\ud83d\ude00`, `\u2028`,
}

var corruptions = []string{"{", "}", "[", "]", ",", ":", `"`, `\`, "-", "+", ".", "e", "0", "t", "n", " ", "\t", "\r", "\n", "\x00", "\x1f", "é", "\ufeff", "\u2028", "😀"}

func randomString(r *rand.Rand) string {
	var b strings.Builder
	b.WriteByte('"')
	for n := r.IntN(8); n > 0; n-- {
		b.WriteString(stringPieces[r.IntN(len(stringPieces))])
	}
	b.WriteByte('"')
	return b.String()
}

func randomNumber(r *rand.Rand) string {
	forms := []string{"0", "-0", "7", "-12", "3.25", "1e21", "1E-7", "123456789012345678901", "1e400", "-1e400", "1e-400", "0.1", "2.5e+3"}
	return forms[r.IntN(len(forms))]
}

func randomValue(r *rand.Rand, depth int) string {
	switch k := r.IntN(10); {
	case depth > 3 || k < 2:
		return randomNumber(r)
	case k < 4:
		return randomString(r)
	case k == 4:
		return []string{"true", "false", "null"}[r.IntN(3)]
	case k < 7:
		items := make([]string, r.IntN(4))
		for i := range items {
			items[i] = randomValue(r, depth+1)
		}
		return "[" + strings.Join(items, ",") + "]"
	default:
		keys := []string{`"b"`, `"a"`, `"A"`, `"_"`, `"-"`, `"0"`, `"10"`, `"01"`, `"4294967295"`, `"__proto__"`, `"é"`}
		members := make([]string, r.IntN(5))
		for i := range members {
			members[i] = keys[r.IntN(len(keys))] + ":" + randomValue(r, depth+1)
		}
		return "{" + strings.Join(members, ",") + "}"
	}
}

// RandomJSONText returns JSON text; about a third of results are corrupted.
func RandomJSONText(r *rand.Rand) string {
	text := randomValue(r, 0)
	if r.IntN(3) != 0 || text == "" {
		return text
	}
	runes := []rune(text)
	i := r.IntN(len(runes) + 1)
	switch r.IntN(3) {
	case 0:
		return string(runes[:i])
	case 1:
		return string(runes[:i]) + corruptions[r.IntN(len(corruptions))] + string(runes[i:])
	default:
		if i == len(runes) {
			i--
		}
		return string(runes[:i]) + string(runes[i+1:])
	}
}

// RandomFloat returns a finite float64 drawn from its bit patterns.
func RandomFloat(r *rand.Rand) float64 {
	for {
		f := math.Float64frombits(r.Uint64())
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	}
}
