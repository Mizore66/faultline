package jsjson

import (
	"math"
	"slices"
	"testing"
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
