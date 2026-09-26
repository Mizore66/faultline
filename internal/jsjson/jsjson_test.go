package jsjson

import (
	"fmt"
	"math"
	"runtime"
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
// (an issues array holding an issue object whose `received` is the value);
// the exact thresholds were bisected through `node dist/cli.js verify` on
// Node 22.22 linux/amd64 (see stackModel).
func TestStringifyStackOverflow(t *testing.T) {
	nest := func(depth int, leaf Value, wrap func(Value) Value) Value {
		v := leaf
		for range depth {
			v = wrap(v)
		}
		return v
	}
	arr := func(v Value) Value { return MakeArray([]Value{v}) }
	objKey := func(key string, extra int) func(Value) Value {
		return func(v Value) Value {
			o := NewObj()
			for i := range extra {
				o.Set("k"+strconv.Itoa(i), MakeNumber(0))
			}
			o.Set(key, v)
			return MakeObject(o)
		}
	}
	zero, emptyArr, emptyObj := MakeNumber(0), MakeArray(nil), MakeObject(NewObj())
	overflows := func(site int, v Value) bool {
		defer UseCallSite(site)()
		issue := NewObj()
		issue.Set("received", v)
		_, err := StringifyIndentChecked(MakeArray([]Value{MakeObject(issue)}), "") // indentation does not change the stack
		return err == ErrStackOverflow
	}
	type shape struct {
		name string
		make func(n int) Value // n containers in total
	}
	shapes := map[string]shape{
		"arr":  {"[]-terminated arrays", func(n int) Value { return nest(n-1, emptyArr, arr) }},
		"arr0": {"[0]-terminated arrays", func(n int) Value { return nest(n, zero, arr) }},
		"obj":  {`{"a":0} objects`, func(n int) Value { return nest(n, zero, objKey("a", 0)) }},
		"objE": {"{}-terminated objects", func(n int) Value { return nest(n-1, emptyObj, objKey("a", 0)) }},
		"idx":  {`{"0":0} objects (slow path)`, func(n int) Value { return nest(n, zero, objKey("0", 0)) }},
		"d127": {"127-member objects (fast)", func(n int) Value { return nest(n, zero, objKey("a", 126)) }},
		"d128": {"128-member objects (dictionary)", func(n int) Value { return nest(n, zero, objKey("a", 127)) }},
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		for _, sh := range shapes {
			if overflows(SiteDemo, sh.make(1000)) || !overflows(SiteDemo, sh.make(9000)) {
				t.Errorf("%s: threshold outside 1000..9000", sh.name)
			}
		}
		return
	}
	for _, tc := range []struct {
		site  int
		shape string
		last  int // deepest value that still stringifies
	}{
		{SiteDemo, "arr", 2233}, {SiteDemo, "arr0", 2232}, {SiteDemo, "obj", 4166}, {SiteDemo, "objE", 4167},
		{SiteDemo, "idx", 2232}, {SiteDemo, "d127", 4166}, {SiteDemo, "d128", 2232},
		{SitePrevention, "arr", 2233}, {SitePrevention, "obj", 4167}, {SitePrevention, "idx", 2232},
		{SiteGitProof, "arr", 2232}, {SiteGitProof, "obj", 4165}, {SiteGitProof, "idx", 2231},
	} {
		sh := shapes[tc.shape]
		if overflows(tc.site, sh.make(tc.last)) || !overflows(tc.site, sh.make(tc.last+1)) {
			t.Errorf("site %d, %s: last depth that prints should be %d", tc.site, sh.name, tc.last)
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
