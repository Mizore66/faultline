// Package gen holds Go-side random input generators for live oracle tests.
package gen

import (
	"math"
	"math/rand/v2"
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
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

const asciiKeyAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:@ +#$%&*()[]{}!?,;=~^'\"<>|\\`"

var latin1Extras = []rune("éÉèàçñöÖüßøÆ¡¿ª·")

// RandomKeys returns 2–7 distinct keys; about a quarter include Latin-1 letters.
func RandomKeys(r *rand.Rand) []string {
	seen := map[string]bool{}
	var keys []string
	for count := 2 + r.IntN(6); len(keys) < count; {
		var b strings.Builder
		for n := 1 + r.IntN(12); n > 0; n-- {
			if r.IntN(4) == 0 {
				b.WriteRune(latin1Extras[r.IntN(len(latin1Extras))])
			} else {
				b.WriteByte(asciiKeyAlphabet[r.IntN(len(asciiKeyAlphabet))])
			}
		}
		if k := b.String(); !seen[k] {
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
		checks := pick(r, `[]`, `[{"k":"length","n":2}]`, `[{"k":"min","n":1}]`, `[{"k":"max","n":1}]`)
		return `{"t":"array","item":` + d + `,"checks":` + checks + `}`, "[" + strings.Repeat(v+",", r.IntN(3)) + v + "]"
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
