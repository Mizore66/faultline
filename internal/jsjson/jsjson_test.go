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
// V8's native stack; the thresholds are calibrated against Node 22.
func TestStringifyStackOverflow(t *testing.T) {
	nest := func(depth int, wrap func(Value) Value) Value {
		v := MakeNull()
		for range depth {
			v = wrap(v)
		}
		return v
	}
	arr := func(v Value) Value { return MakeArray([]Value{v}) }
	obj := func(v Value) Value { o := NewObj(); o.Set("a", v); return MakeObject(o) }
	for _, tc := range []struct {
		name  string
		v     Value
		fails bool
	}{
		{"array ok", nest(2000, arr), false},
		{"array overflow", nest(3000, arr), true},
		{"object ok", nest(4000, obj), false},
		{"object overflow", nest(5000, obj), true},
	} {
		_, err := StringifyIndentChecked(tc.v, "  ")
		if tc.fails != (err == ErrStackOverflow) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}
