package schema

import (
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func mustParse(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func expectMessage(t *testing.T, s Schema, input, want string) {
	t.Helper()
	_, issues, ok := Parse(s, mustParse(t, input))
	if ok {
		t.Fatalf("Parse(%s) succeeded", input)
	}
	if got := ErrorMessage(issues); got != want {
		t.Fatalf("Parse(%s) message =\n%s\nwant\n%s", input, got, want)
	}
}

func TestCapturedZodMessages(t *testing.T) {
	expectMessage(t, Object(Field{"a", String()}), `{"a":1}`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"string\",\n    \"received\": \"number\",\n    \"path\": [\n      \"a\"\n    ],\n    \"message\": \"Expected string, received number\"\n  }\n]")
	expectMessage(t, Object(Field{"a", String()}), `{}`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"string\",\n    \"received\": \"undefined\",\n    \"path\": [\n      \"a\"\n    ],\n    \"message\": \"Required\"\n  }\n]")
	expectMessage(t, LiteralString("x"), `"y"`, "[\n  {\n    \"received\": \"y\",\n    \"code\": \"invalid_literal\",\n    \"expected\": \"x\",\n    \"path\": [],\n    \"message\": \"Invalid literal value, expected \\\"x\\\"\"\n  }\n]")
	expectMessage(t, Object(Field{"a", String()}).Strict(), `{"a":"","z":1,"b":2,"1":3}`, "[\n  {\n    \"code\": \"unrecognized_keys\",\n    \"keys\": [\n      \"1\",\n      \"z\",\n      \"b\"\n    ],\n    \"path\": [],\n    \"message\": \"Unrecognized key(s) in object: '1', 'z', 'b'\"\n  }\n]")
	expectMessage(t, String(Regex(Pattern(`^a$`), "")), `"b"`, "[\n  {\n    \"validation\": \"regex\",\n    \"code\": \"invalid_string\",\n    \"message\": \"Invalid\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(Regex(Pattern(`^a$`), "custom msg")), `"b"`, "[\n  {\n    \"validation\": \"regex\",\n    \"code\": \"invalid_string\",\n    \"message\": \"custom msg\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(Datetime()), `"nope"`, "[\n  {\n    \"code\": \"invalid_string\",\n    \"validation\": \"datetime\",\n    \"message\": \"Invalid datetime\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(UUID()), `"nope"`, "[\n  {\n    \"validation\": \"uuid\",\n    \"code\": \"invalid_string\",\n    \"message\": \"Invalid uuid\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(MinLength(1)), `""`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 1,\n    \"type\": \"string\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"String must contain at least 1 character(s)\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(MaxLength(2)), `"abc"`, "[\n  {\n    \"code\": \"too_big\",\n    \"maximum\": 2,\n    \"type\": \"string\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"String must contain at most 2 character(s)\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Array(String(), Length(3)), `["a"]`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 3,\n    \"type\": \"array\",\n    \"inclusive\": true,\n    \"exact\": true,\n    \"message\": \"Array must contain exactly 3 element(s)\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Array(String(), Length(3)), `["a","b","c","d"]`, "[\n  {\n    \"code\": \"too_big\",\n    \"maximum\": 3,\n    \"type\": \"array\",\n    \"inclusive\": true,\n    \"exact\": true,\n    \"message\": \"Array must contain exactly 3 element(s)\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Number(Int()), `1.5`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"integer\",\n    \"received\": \"float\",\n    \"message\": \"Expected integer, received float\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Number(Int(), Positive()), `0`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 0,\n    \"type\": \"number\",\n    \"inclusive\": false,\n    \"exact\": false,\n    \"message\": \"Number must be greater than 0\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Number(Nonnegative()), `-1`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 0,\n    \"type\": \"number\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"Number must be greater than or equal to 0\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Number(Int(), Lte(3)), `4`, "[\n  {\n    \"code\": \"too_big\",\n    \"maximum\": 3,\n    \"type\": \"number\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"Number must be less than or equal to 3\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Enum("A", "B"), `"C"`, "[\n  {\n    \"received\": \"C\",\n    \"code\": \"invalid_enum_value\",\n    \"options\": [\n      \"A\",\n      \"B\"\n    ],\n    \"path\": [],\n    \"message\": \"Invalid enum value. Expected 'A' | 'B', received 'C'\"\n  }\n]")
	expectMessage(t, DiscriminatedUnion("s", Object(Field{"s", LiteralString("A")}), Object(Field{"s", LiteralString("B")})), `{"s":"C"}`, "[\n  {\n    \"code\": \"invalid_union_discriminator\",\n    \"options\": [\n      \"A\",\n      \"B\"\n    ],\n    \"path\": [\n      \"s\"\n    ],\n    \"message\": \"Invalid discriminator value. Expected 'A' | 'B'\"\n  }\n]")
	expectMessage(t, Refine(String(), func(jsjson.Value) bool { return false }, "refine msg"), `"x"`, "[\n  {\n    \"code\": \"custom\",\n    \"message\": \"refine msg\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Object(Field{"a", Array(Object(Field{"b", Number()}))}), `{"a":[{"b":"x"}]}`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"number\",\n    \"received\": \"string\",\n    \"path\": [\n      \"a\",\n      0,\n      \"b\"\n    ],\n    \"message\": \"Expected number, received string\"\n  }\n]")
	expectMessage(t, Object(Field{"a", String()}), `"str"`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"object\",\n    \"received\": \"string\",\n    \"path\": [],\n    \"message\": \"Expected object, received string\"\n  }\n]")
}

func TestStripDropsUnknownKeysAndOptionalAbsence(t *testing.T) {
	s := Object(Field{"a", String()}, Field{"o", Optional(Number())})
	out, _, ok := Parse(s, mustParse(t, `{"z":1,"a":"x"}`))
	if !ok || jsjson.Stringify(out) != `{"a":"x"}` {
		t.Fatalf("out = %s ok=%v", jsjson.Stringify(out), ok)
	}
}

func TestTrimRewritesOutput(t *testing.T) {
	out, _, ok := Parse(String(Trim(), MinLength(1)), mustParse(t, `" \u00a0x\t"`))
	if !ok || out.Str() != "x" {
		t.Fatalf("out = %q ok=%v", out.Str(), ok)
	}
	expectMessage(t, String(Trim(), MinLength(1)), `"   "`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 1,\n    \"type\": \"string\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"String must contain at least 1 character(s)\",\n    \"path\": []\n  }\n]")
}

func TestRecordDropsProtoKey(t *testing.T) {
	out, _, ok := Parse(Record(String()), mustParse(t, `{"__proto__":"x","a":"y"}`))
	if !ok || jsjson.Stringify(out) != `{"a":"y"}` {
		t.Fatalf("out = %s ok=%v", jsjson.Stringify(out), ok)
	}
}
