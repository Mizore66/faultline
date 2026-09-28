package jsjson

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseErrorsMatchV8(t *testing.T) {
	cases := map[string]string{
		"":                                     "Unexpected end of JSON input",
		" ":                                    "Unexpected end of JSON input",
		"{":                                    "Expected property name or '}' in JSON at position 1 (line 1 column 2)",
		`{"a"`:                                 "Expected ':' after property name in JSON at position 4 (line 1 column 5)",
		`{"a":`:                                "Unexpected end of JSON input",
		`{"a":1`:                               "Expected ',' or '}' after property value in JSON at position 6 (line 1 column 7)",
		`{"a":1,`:                              "Expected double-quoted property name in JSON at position 7 (line 1 column 8)",
		"[1,":                                  "Unexpected end of JSON input",
		"[1 2]":                                "Expected ',' or ']' after array element in JSON at position 3 (line 1 column 4)",
		"{a:1}":                                "Expected property name or '}' in JSON at position 1 (line 1 column 2)",
		"tru":                                  "Unexpected end of JSON input",
		"01":                                   "Unexpected number in JSON at position 1 (line 1 column 2)",
		"-":                                    "No number after minus sign in JSON at position 1 (line 1 column 2)",
		"1.":                                   "Unterminated fractional number in JSON at position 2 (line 1 column 3)",
		"1e":                                   "Exponent part is missing a number in JSON at position 2 (line 1 column 3)",
		`"abc`:                                 "Unterminated string in JSON at position 4 (line 1 column 5)",
		"\"a\x01\"":                            "Bad control character in string literal in JSON at position 2 (line 1 column 3)",
		`"\x"`:                                 "Bad escaped character in JSON at position 2 (line 1 column 3)",
		`"\u12G4"`:                             "Bad Unicode escape in JSON at position 5 (line 1 column 6)",
		"{} x":                                 "Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)",
		"\ufeff{}":                             "Unexpected token '\ufeff', \"\ufeff{}\" is not valid JSON",
		`{"a":1}}`:                             "Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)",
		"x":                                    "Unexpected token 'x', \"x\" is not valid JSON",
		"xyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyz": "Unexpected token 'x', \"xyzxyzxyzx\"... is not valid JSON",
		"{\n  \"a\": 1\n  \"b\": 2\n}":         "Expected ',' or '}' after property value in JSON at position 13 (line 3 column 3)",
		"é":                                    "Unexpected token 'é', \"é\" is not valid JSON",
		"NaN":                                  "\"NaN\" is not valid JSON",
		"[object Object]":                      "\"[object Object]\" is not valid JSON",
	}
	for text, want := range cases {
		_, err := Parse(text)
		if err == nil || err.Error() != want {
			t.Errorf("Parse(%q) error = %v, want %q", text, err, want)
		}
	}
}

func TestObjectPropertyOrder(t *testing.T) {
	v, err := Parse(`{"b":1,"1":2,"a":3,"0":4,"01":5,"4294967295":6,"4294967294":7,"b":8}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0", "1", "4294967294", "b", "a", "01", "4294967295"}
	if got := v.Obj().Keys(); !slices.Equal(got, want) {
		t.Fatalf("keys = %q, want %q", got, want)
	}
	if b, _ := v.Obj().Get("b"); b.Num() != 8 {
		t.Fatal("duplicate key: last value wins")
	}
}

func TestNumbers(t *testing.T) {
	cases := map[float64]string{
		0: "0", math.Copysign(0, -1): "0", 1e21: "1e+21", 1e20: "100000000000000000000",
		123456789012345680000: "123456789012345680000", 1e-7: "1e-7", 1.5e-7: "1.5e-7",
		0.000001: "0.000001", 0.30000000000000004: "0.30000000000000004", 5e-324: "5e-324", -1.5: "-1.5",
	}
	for f, want := range cases {
		if got := FormatNumber(f); got != want {
			t.Errorf("FormatNumber(%v) = %q, want %q", f, got, want)
		}
	}
	v, _ := Parse("1e400")
	if !math.IsInf(v.Num(), 1) {
		t.Fatal("1e400 must parse to +Infinity like V8")
	}
}

func TestStringify(t *testing.T) {
	v, err := Parse(`{"s":" <>&\u0001\ud800\u2028","a":[],"o":{},"n":[1,{"x":null}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Stringify(v), "{\"s\":\" <>&\\u0001\\ud800\u2028\",\"a\":[],\"o\":{},\"n\":[1,{\"x\":null}]}"; got != want {
		t.Fatalf("Stringify = %q", got)
	}
	want := "{\n  \"s\": \" <>&\\u0001\\ud800\u2028\",\n  \"a\": [],\n  \"o\": {},\n  \"n\": [\n    1,\n    {\n      \"x\": null\n    }\n  ]\n}"
	if got := StringifyIndent(v, "  "); got != want {
		t.Fatalf("StringifyIndent = %q", got)
	}
}

// V8's JSON.parse is iterative; a hostile file nested millions of levels deep
// must not overflow the Go stack.
func TestParseDeepNestingIsIterative(t *testing.T) {
	const depth = 2_000_000 // the recursive parser died at this depth
	open := strings.Repeat("[", depth)
	if _, err := Parse(open); err == nil || err.Error() != "Unexpected end of JSON input" {
		t.Fatalf("unbalanced: got %v", err)
	}
	v, err := Parse(open + strings.Repeat("]", depth))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < depth; i++ {
		if v.Kind() != Array || len(v.Items()) != 1 {
			t.Fatalf("level %d: not a one-element array", i)
		}
		v = v.Items()[0]
	}
	if v.Kind() != Array || len(v.Items()) != 0 {
		t.Fatal("innermost value is not []")
	}
	objs := strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth)
	if _, err := Parse(objs); err != nil {
		t.Fatal(err)
	}
}

// Integer-like keys arriving in descending order used to cost O(n²) inserts.
func TestManyDescendingIndexKeys(t *testing.T) {
	const n = 400_000
	var b strings.Builder
	b.WriteByte('{')
	for i := n - 1; i >= 0; i-- {
		fmt.Fprintf(&b, `"%d":0`, i)
		if i > 0 {
			b.WriteByte(',')
		}
	}
	b.WriteByte('}')
	start := time.Now()
	v, err := Parse(b.String())
	if err != nil {
		t.Fatal(err)
	}
	keys := v.Obj().Keys()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %v", elapsed)
	}
	if len(keys) != n || keys[0] != "0" || keys[n-1] != strconv.Itoa(n-1) {
		t.Fatalf("keys out of order: first %q last %q", keys[0], keys[len(keys)-1])
	}
}

// JSON.stringify throws RangeError("Maximum call stack size exceeded") past
// V8's native stack. The values are wrapped the way zod's message is built
// (an issues array holding an issue object whose `received` is the value)
// and parsed from text, so dictionary mode follows the parsed entries. The
// thresholds are the last depths that print through `node dist/cli.js
// verify` (see stackModel); every platform model is checked on every host.
func TestStringifyStackOverflow(t *testing.T) {
	dup := strings.Repeat(`"a":0,`, 127)
	many := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, `"k%d":0,`, i)
		}
		return b.String()
	}
	m126, m127 := many(126), many(127)
	shapes := map[string]func(n int) string{
		"arr":  func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) },
		"arr0": func(n int) string { return strings.Repeat("[", n) + "0" + strings.Repeat("]", n) },
		"obj":  func(n int) string { return strings.Repeat(`{"a":`, n) + "0" + strings.Repeat("}", n) },
		"objE": func(n int) string { return strings.Repeat(`{"a":`, n-1) + "{}" + strings.Repeat("}", n-1) },
		"idx":  func(n int) string { return strings.Repeat(`{"0":`, n) + "0" + strings.Repeat("}", n) },
		"alt": func(n int) string {
			var open, closing strings.Builder
			for i := range n {
				if i%2 == 1 {
					open.WriteString(`{"a":`)
				} else {
					open.WriteString("[")
				}
			}
			for i := n - 1; i >= 0; i-- {
				if i%2 == 1 {
					closing.WriteString("}")
				} else {
					closing.WriteString("]")
				}
			}
			return open.String() + "0" + closing.String()
		},
		// 127 distinct members plus the nested one: fast.
		"d127": func(n int) string { return strings.Repeat("{"+m126+`"a":`, n) + "0" + strings.Repeat("}", n) },
		// 128 distinct members: dictionary mode.
		"d128": func(n int) string { return strings.Repeat("{"+m127+`"a":`, n) + "0" + strings.Repeat("}", n) },
		// 128 entries of one key: V8 counts entries, so dictionary mode too.
		"dup": func(n int) string { return strings.Repeat("{"+dup+`"a":`, n) + "0" + strings.Repeat("}", n) },
	}
	type row struct {
		site  Site
		shape string
		last  int
	}
	platforms := []struct {
		goos, goarch string
		rows         []row
	}{
		{"linux", "amd64", []row{
			{SiteDemo, "arr", 2233}, {SiteDemo, "arr0", 2232}, {SiteDemo, "obj", 4166}, {SiteDemo, "objE", 4167},
			{SiteDemo, "idx", 2232}, {SiteDemo, "alt", 2906}, {SiteDemo, "d127", 4166}, {SiteDemo, "d128", 2232}, {SiteDemo, "dup", 2232},
			{SitePrevention, "arr", 2233}, {SitePrevention, "obj", 4167}, {SitePrevention, "idx", 2232}, {SitePrevention, "alt", 2907},
			{SiteGitProof, "arr", 2232}, {SiteGitProof, "obj", 4165}, {SiteGitProof, "idx", 2231}, {SiteGitProof, "alt", 2906},
			{SiteGitLedger, "arr", 2232}, {SiteGitLedger, "obj", 4165}, {SiteGitLedger, "idx", 2231}, {SiteGitLedger, "alt", 2905},
			{SiteDemoWitness, "arr", 2232}, {SiteDemoWitness, "obj", 4164}, {SiteDemoWitness, "idx", 2231}, {SiteDemoWitness, "alt", 2905},
		}},
		{"darwin", "arm64", []row{
			{SiteDemo, "arr", 2610}, {SiteDemo, "arr0", 2609}, {SiteDemo, "obj", 6089}, {SiteDemo, "idx", 2491}, {SiteDemo, "alt", 3653}, {SiteDemo, "dup", 2491},
			{SitePrevention, "arr", 2611}, {SitePrevention, "obj", 6091}, {SitePrevention, "idx", 2491}, {SitePrevention, "alt", 3654},
			{SiteGitProof, "arr", 2610}, {SiteGitProof, "obj", 6088}, {SiteGitProof, "idx", 2490}, {SiteGitProof, "alt", 3652},
			{SiteGitLedger, "arr", 2609}, {SiteGitLedger, "obj", 6087}, {SiteGitLedger, "idx", 2490}, {SiteGitLedger, "alt", 3652},
			{SiteDemoWitness, "arr", 2609}, {SiteDemoWitness, "obj", 6086}, {SiteDemoWitness, "idx", 2490}, {SiteDemoWitness, "alt", 3652},
		}},
		{"linux", "arm64", []row{
			{SiteDemo, "arr", 1827}, {SiteDemo, "arr0", 1826}, {SiteDemo, "obj", 3653}, {SiteDemo, "idx", 1956}, {SiteDemo, "alt", 2434}, {SiteDemo, "dup", 1956},
			{SitePrevention, "arr", 1827}, {SitePrevention, "obj", 3653}, {SitePrevention, "idx", 1957}, {SitePrevention, "alt", 2435},
			{SiteGitProof, "arr", 1827}, {SiteGitProof, "obj", 3652}, {SiteGitProof, "idx", 1956}, {SiteGitProof, "alt", 2434},
			{SiteGitLedger, "arr", 1826}, {SiteGitLedger, "obj", 3651}, {SiteGitLedger, "idx", 1956}, {SiteGitLedger, "alt", 2434},
			{SiteDemoWitness, "arr", 1826}, {SiteDemoWitness, "obj", 3651}, {SiteDemoWitness, "idx", 1955}, {SiteDemoWitness, "alt", 2434},
		}},
	}
	saved := v8Stack
	defer func() { v8Stack = saved }()
	for _, p := range platforms {
		v8Stack = stackModelFor(p.goos, p.goarch)
		overflows := func(site Site, text string) bool {
			v, err := Parse(text)
			if err != nil {
				t.Fatal(err)
			}
			defer UseCallSite(site)()
			issue := NewObj()
			issue.Set("received", v)
			_, err = StringifyIndentChecked(MakeArray([]Value{MakeObject(issue)}), "") // indentation does not change the stack
			return err == ErrStackOverflow
		}
		for _, r := range p.rows {
			make := shapes[r.shape]
			if overflows(r.site, make(r.last)) || !overflows(r.site, make(r.last+1)) {
				t.Errorf("%s/%s site %d, %s: last depth that prints should be %d", p.goos, p.goarch, r.site, r.shape, r.last)
			}
		}
	}
}

// V8 throws RangeError("Invalid string length") once the result would pass
// String::kMaxLength (counted in UTF-16 units), but only after serializing
// everything: a stack overflow later in the value still wins.
func TestStringifyStringLength(t *testing.T) {
	check := func(v Value, max int) (string, bool, bool) {
		w := &writer{budget: v8Stack.budget, maxUnits: max}
		w.write(v, "")
		return w.b.String(), w.tooLong, w.overflow
	}
	v := MakeArray([]Value{MakeString("ab😀")}) // ["ab😀"]: 8 units
	if out, long, _ := check(v, 8); long || out != `["ab😀"]` {
		t.Fatalf("at the limit: %q %v", out, long)
	}
	if _, long, _ := check(v, 7); !long {
		t.Fatal("one unit over the limit must fail")
	}
	deep := MakeArray(nil)
	for range 5000 {
		deep = MakeArray([]Value{deep})
	}
	if _, long, overflow := check(MakeArray([]Value{MakeString("xxxxxxxx"), deep}), 4); !long || !overflow {
		t.Fatal("serialization must continue past the length limit to the stack overflow")
	}
}

// Spreading a list into call arguments overflows V8's stack past a limit
// that depends on the call site and the platform, bisected through
// `node dist/cli.js verify` on Node 22.22.2 (see v8Spread);
// TestLiveSpreadLimitsMatchNode checks them against Node in CI.
func TestSpreadLimits(t *testing.T) {
	for _, p := range []struct {
		goos, goarch                          string
		collect1, collect5, semantics, ledger int
	}{
		{"linux", "amd64", 125576, 125452, 125587, 125562},
		{"linux", "arm64", 110189, 110061, 110201, 110175},
		{"darwin", "arm64", 110181, 110053, 110193, 110167},
		{"windows", "arm64", 110189, 110061, 110201, 110175},
		{"darwin", "amd64", 125576, 125452, 125587, 125562},
	} {
		saved := v8Spread
		v8Spread = spreadModelFor(p.goos, p.goarch)
		for _, tc := range []struct {
			name string
			fits func(int) bool
			last int
		}{
			{"demo walker, depth 1", func(n int) bool { return CollectSpreadFits(n, 1) }, p.collect1},
			{"demo walker, depth 5", func(n int) bool { return CollectSpreadFits(n, 5) }, p.collect5},
			{"git semantics errors", func(n int) bool { return SpreadFits(n, SpreadGitSemantics) }, p.semantics},
			{"git lifecycle ledger errors", func(n int) bool { return SpreadFits(n, SpreadGitLedger) }, p.ledger},
		} {
			if !tc.fits(tc.last) || tc.fits(tc.last+1) {
				t.Errorf("%s/%s %s: last list length that fits should be %d", p.goos, p.goarch, tc.name, tc.last)
			}
		}
		v8Spread = saved
	}
}

// JSON.parse stores an object's index-keyed entries in a NumberDictionary
// or a FixedArray up to the largest index (ShouldConvertToSlowElements);
// the cases were probed against Node 22.22.2, where the FixedArray ones of
// 2^27 or more slots abort.
func TestSlowElements(t *testing.T) {
	for _, tc := range []struct {
		used   int
		length uint64
		slow   bool
	}{
		{5592405, 134217728, true},   // Node survives
		{5592406, 134217728, false},  // Node aborts: invalid size error 134217728
		{5592406, 134217727, false},  // fits
		{5592406, 150000000, false},  // aborts
		{11184811, 200000000, true},  // survives
		{11184812, 200000000, false}, // aborts
		{2, 134217728, true},
		{1, 1, false},
	} {
		if got := slowElements(tc.used, tc.length); got != tc.slow {
			t.Errorf("slowElements(%d, %d) = %v", tc.used, tc.length, got)
		}
	}
}
