// Package gen holds Go-side random input generators for live oracle tests.
package gen

import (
	"math"
	"math/rand/v2"
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
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
		keys := []string{`"b"`, `"a"`, `"A"`, `"_"`, `"-"`, `"0"`, `"10"`, `"01"`, `"4294967295"`, `"__proto__"`, `"é"`,
			`"中"`, `"神经"`, `"视图"`, `"🤣"`, `"\ud800"`, `"\udc00"`, `"a\u0301"`, `"á"`, `"٣"`, `"३"`, `"₿"`, `"\ufffd"`, `"가"`}
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

const asciiKeyAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:@ +#$%&*()[]{}!?,;=~^'\"<>|\\`"

var latin1Extras = []rune("éÉèàçñöÖüßøÆ¡¿ª·")

// keyRanges are code point ranges where collation is easy to get wrong:
// CJK (radical-stroke order), CJK Ext B, emoji, combining marks, non-ASCII
// digits, default ignorables, Hangul syllables and jamo, Arabic, Thai
// prevowels (contractions), private use, unassigned, and the U+FFFx specials.
var keyRanges = [][2]rune{
	{0x4E00, 0x9FFF}, {0x20000, 0x2A6DF}, {0x1F300, 0x1FAFF}, {0x0300, 0x036F}, {0x1DC0, 0x1DFF},
	{0x0660, 0x0669}, {0x0966, 0x096F}, {0xFF10, 0xFF19}, {0x00AD, 0x00AD}, {0x200B, 0x200F},
	{0x2060, 0x2064}, {0xFEFF, 0xFEFF}, {0xAC00, 0xD7A3}, {0x1100, 0x11FF}, {0x0600, 0x06FF},
	{0x0E00, 0x0E7F}, {0xE000, 0xF8FF}, {0x0378, 0x0379}, {0xFFFC, 0xFFFF}, {0x20A0, 0x20C1},
}

func randomKeyRune(r *rand.Rand) string {
	switch k := r.IntN(10); {
	case k < 4:
		return string(asciiKeyAlphabet[r.IntN(len(asciiKeyAlphabet))])
	case k == 4:
		return string(latin1Extras[r.IntN(len(latin1Extras))])
	case k == 5:
		// A lone surrogate (WTF-8), as JSON "\udXXX" escapes produce.
		return jsstr.FromUTF16([]uint16{uint16(0xD800 + r.IntN(0x800))})
	default:
		rg := keyRanges[r.IntN(len(keyRanges))]
		return string(rg[0] + rune(r.IntN(int(rg[1]-rg[0]+1))))
	}
}

// RandomKeys returns 2–7 distinct keys drawn from ASCII, Latin-1 and the
// harder ranges above, plus occasional near-duplicates that differ only by a
// combining mark or case, so ties and secondary/tertiary levels are exercised.
func RandomKeys(r *rand.Rand) []string {
	seen := map[string]bool{}
	var keys []string
	for count := 2 + r.IntN(6); len(keys) < count; {
		var b strings.Builder
		if len(keys) > 0 && r.IntN(4) == 0 {
			b.WriteString(keys[r.IntN(len(keys))])
			b.WriteString(pick(r, "\u0301", "\u0323", "A", "a", "\u00b7", "\u200b", ""))
		} else {
			for n := 1 + r.IntN(8); n > 0; n-- {
				b.WriteString(randomKeyRune(r))
			}
		}
		// Re-encode so adjacent lone surrogates that form a JS pair become
		// one canonical WTF-8 code point.
		if k := jsstr.FromUTF16(jsstr.ToUTF16(b.String())); !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

func pick[T any](r *rand.Rand, items ...T) T { return items[r.IntN(len(items))] }

// randomNode returns a DSL schema and a value text that usually satisfies it.
func randomNode(r *rand.Rand, depth int) (string, string) {
	kinds := []string{"string", "number", "boolean", "literal", "enum", "array", "record", "optional", "nullable", "object", "disc", "refine", "default"}
	if depth >= 3 {
		kinds = kinds[:5]
	}
	switch pick(r, kinds...) {
	case "string":
		switch r.IntN(7) {
		case 6:
			return `{"t":"string","checks":[{"k":"trim"},{"k":"min","n":1},{"k":"max","n":3}]}`, pick(r, `"  ab  "`, `"   "`, `"\u2028abcd\ufeff"`, `" x "`)
		case 0:
			return `{"t":"string","checks":[{"k":"min","n":1},{"k":"max","n":4}]}`, pick(r, `"ab"`, `""`, `"abcdef"`, `"😀😀😀"`)
		case 1:
			return `{"t":"string","checks":[{"k":"regex","name":"sha256","msg":null}]}`, pick(r, `"sha256:`+strings.Repeat("a", 64)+`"`, `"sha256:zz"`)
		case 2:
			return `{"t":"string","checks":[{"k":"regex","name":"identifier","msg":"Identifier contains unsupported characters"},{"k":"max","n":8}]}`, pick(r, `"abc"`, `"-bad"`, `"waytoolong-identifier"`)
		case 3:
			return `{"t":"string","checks":[{"k":"datetime"}]}`, pick(r, `"2026-07-16T11:00:00.000Z"`, `"2026-02-30T00:00:00Z"`, `"2026-07-16T11:00:00+05:30"`, `"2026-07-16"`)
		case 4:
			return `{"t":"string","checks":[{"k":"uuid"}]}`, pick(r, `"123e4567-e89b-12d3-a456-426614174000"`, `"123E4567-E89B-12D3-A456-426614174000"`, `"nope"`)
		default:
			return `{"t":"string","checks":[]}`, `"x"`
		}
	case "number":
		return pick(r, `{"t":"number","checks":[{"k":"int"},{"k":"positive"}]}`, `{"t":"number","checks":[{"k":"nonnegative"}]}`, `{"t":"number","checks":[{"k":"int"},{"k":"min","n":1},{"k":"max","n":3}]}`),
			pick(r, "1", "0", "2.5", "-1", "3", "4", "1e400")
	case "boolean":
		return `{"t":"boolean"}`, pick(r, "true", "false")
	case "literal":
		return pick(r, `{"t":"literal","v":"x"}`, `{"t":"literal","v":false}`, `{"t":"literal","v":3}`), pick(r, `"x"`, "false", "3", `"y"`)
	case "enum":
		return `{"t":"enum","values":["A","B"]}`, pick(r, `"A"`, `"B"`, `"C"`, "1")
	case "array":
		d, v := randomNode(r, depth+1)
		// zod keeps one exact/min/max setting each (last call wins) and checks
		// them in that order, whatever the call order.
		checks := pick(r, `[]`, `[{"k":"length","n":2}]`, `[{"k":"min","n":1}]`, `[{"k":"max","n":1}]`,
			`[{"k":"max","n":1},{"k":"min","n":3}]`, `[{"k":"min","n":3},{"k":"length","n":2}]`,
			`[{"k":"length","n":2},{"k":"length","n":3}]`, `[{"k":"max","n":0},{"k":"max","n":2},{"k":"length","n":1}]`)
		return `{"t":"array","item":` + d + `,"checks":` + checks + `}`, "[" + strings.Repeat(v+",", r.IntN(4)) + v + "]"
	case "record":
		d, v := randomNode(r, depth+1)
		return `{"t":"record","value":` + d + `}`, `{"a":` + v + `,"__proto__":` + v + `}`
	case "optional":
		d, v := randomNode(r, depth+1)
		return `{"t":"optional","inner":` + d + `}`, v
	case "nullable":
		d, v := randomNode(r, depth+1)
		return `{"t":"nullable","inner":` + d + `}`, pick(r, v, "null")
	case "object":
		d1, v1 := randomNode(r, depth+1)
		d2, v2 := randomNode(r, depth+1)
		if r.IntN(3) == 0 {
			// Integer-like names: zod walks Object.keys(shape), index names first.
			return `{"t":"object","strict":` + pick(r, "true", "false") + `,"shape":[["b",` + d1 + `],["1",` + d2 + `],["a",` + d1 + `],["0",` + d2 + `],["4294967295",` + d1 + `]]}`,
				pick(r, `{"z":1}`, `{"b":`+v1+`,"1":`+v2+`}`, `{"0":`+v2+`,"a":`+v1+`,"4294967295":`+v1+`,"1":`+v2+`,"b":`+v1+`}`)
		}
		return `{"t":"object","strict":` + pick(r, "true", "false") + `,"shape":[["a",` + d1 + `],["b",{"t":"optional","inner":` + d2 + `}],["c",{"t":"default","inner":{"t":"string","checks":[]},"v":"dflt"}]]}`,
			`{"a":` + v1 + `,"b":` + v2 + `}`
	case "disc":
		d, v := randomNode(r, depth+1)
		return `{"t":"disc","key":"s","options":[{"t":"object","strict":true,"shape":[["s",{"t":"literal","v":"A"}],["a",` + d + `]]},{"t":"object","strict":false,"shape":[["s",{"t":"literal","v":"B"}]]}]}`,
			pick(r, `{"s":"A","a":`+v+`}`, `{"s":"B","z":1}`, `{"s":"C"}`, `{"a":`+v+`}`, `"s"`)
	case "refine":
		return pick(r, `{"t":"refine","inner":{"t":"string","checks":[{"k":"max","n":3}]},"pred":"nonEmpty","msg":"must be non-empty"}`, `{"t":"refine","inner":{"t":"string","checks":[]},"pred":"alwaysFalse","msg":"never"}`),
			pick(r, `""`, `"ab"`, `"abcd"`, "1")
	default:
		d, v := randomNode(r, depth+1)
		return `{"t":"default","inner":` + d + `,"v":` + v + `}`, v
	}
}

var replacements = []string{"1", `"x"`, "null", "[]", "{}", "true", "1.5", "-1", `""`, `"sha256:zz"`}

// corruptValue replaces the whole value, or deletes, adds, or retypes one member.
func corruptValue(r *rand.Rand, text string) string {
	v, err := jsjson.Parse(text)
	if err != nil || v.Kind() != jsjson.Object || v.Obj().Len() == 0 || r.IntN(3) == 0 {
		return pick(r, replacements...)
	}
	o, keys := v.Obj(), v.Obj().Keys()
	switch r.IntN(3) {
	case 0:
		o.Delete(keys[r.IntN(len(keys))])
	case 1:
		o.Set("zzUnknown", jsjson.MakeNumber(1))
	default:
		repl, _ := jsjson.Parse(pick(r, replacements...))
		o.Set(keys[r.IntN(len(keys))], repl)
	}
	return jsjson.Stringify(v)
}

// RandomDSL returns a DSL schema and a JSON value text for it (about half invalid).
func RandomDSL(r *rand.Rand) (dsl string, value string) {
	d, v := randomNode(r, 0)
	if r.IntN(2) == 0 {
		v = corruptValue(r, v)
	}
	return d, v
}
