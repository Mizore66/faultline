package schema

import (
	"strings"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

type Issue struct{ Fields *jsjson.Obj }

func (i Issue) Code() string { return i.Fields.Field("code").Str() }

// kv is one issueData entry. A "message" entry with an Undefined value is
// zod's `message: check.message` when no custom message was given.
type kv struct {
	key   string
	value jsjson.Value
}

func str(s string) jsjson.Value   { return jsjson.MakeString(s) }
func num(n float64) jsjson.Value  { return jsjson.MakeNumber(n) }
func boolean(b bool) jsjson.Value { return jsjson.MakeBool(b) }
func strList(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, s := range items {
		values[i] = str(s)
	}
	return jsjson.MakeArray(values)
}

func pathValue(path []any) jsjson.Value {
	values := make([]jsjson.Value, len(path))
	for i, p := range path {
		switch p := p.(type) {
		case string:
			values[i] = str(p)
		case int:
			values[i] = num(float64(p))
		}
	}
	return jsjson.MakeArray(values)
}

// addIssue ports zod makeIssue: {...issueData, path} then message, where keys
// already present in issueData keep their position.
func (c *ctx) addIssue(path []any, data []kv, defaultMessage string) {
	fields := jsjson.NewObj()
	for _, e := range data {
		fields.Set(e.key, e.value)
	}
	fields.Set("path", pathValue(path))
	if m := fields.Field("message"); m.Kind() == jsjson.Undefined {
		fields.Set("message", str(defaultMessage))
	}
	c.issues = append(c.issues, Issue{fields})
}

// joinValues is zod util.joinValues: strings become 'value'.
func joinValues(values []string, sep string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + v + "'"
	}
	return strings.Join(quoted, sep)
}

func (c *ctx) invalidType(path []any, expected string, v jsjson.Value) {
	received := receivedType(v)
	message := "Expected " + expected + ", received " + received
	if received == "undefined" {
		message = "Required"
	}
	c.addIssue(path, []kv{{"code", str("invalid_type")}, {"expected", str(expected)}, {"received", str(received)}}, message)
}

// ErrorMessage is the ZodError.message getter: JSON.stringify(issues,
// replacer, 2). Like the getter, it throws (jsexc) a RangeError when a deeply
// nested `received` value overflows V8's stack.
func ErrorMessage(issues []Issue) string {
	values := make([]jsjson.Value, len(issues))
	for i, issue := range issues {
		values[i] = jsjson.MakeObject(issue.Fields)
	}
	return jsexc.Must(jsjson.StringifyIndentChecked(jsjson.MakeArray(values), "  "))
}

// Error is a ZodError thrown by `Schema.parse`. Its message is computed when
// read, so a RangeError from the getter surfaces where TS reads `.message`.
type Error struct{ Issues []Issue }

func (e *Error) Error() string { return ErrorMessage(e.Issues) }

func tooSmallMessage(kind string, minimum float64, inclusive, exact bool) string {
	n := jsjson.FormatNumber(minimum)
	switch kind {
	case "array":
		return "Array must contain " + pick(exact, "exactly", pick(inclusive, "at least", "more than")) + " " + n + " element(s)"
	case "string":
		return "String must contain " + pick(exact, "exactly", pick(inclusive, "at least", "over")) + " " + n + " character(s)"
	}
	return "Number must be " + pick(exact, "exactly equal to ", pick(inclusive, "greater than or equal to ", "greater than ")) + n
}

func tooBigMessage(kind string, maximum float64, inclusive, exact bool) string {
	n := jsjson.FormatNumber(maximum)
	switch kind {
	case "array":
		return "Array must contain " + pick(exact, "exactly", pick(inclusive, "at most", "less than")) + " " + n + " element(s)"
	case "string":
		return "String must contain " + pick(exact, "exactly", pick(inclusive, "at most", "under")) + " " + n + " character(s)"
	}
	return "Number must be " + pick(exact, "exactly", pick(inclusive, "less than or equal to", "less than")) + " " + n
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (c *ctx) tooSmall(path []any, kind string, minimum float64, inclusive, exact bool) {
	c.addIssue(path, []kv{{"code", str("too_small")}, {"minimum", num(minimum)}, {"type", str(kind)}, {"inclusive", boolean(inclusive)}, {"exact", boolean(exact)}, {"message", jsjson.Value{}}}, tooSmallMessage(kind, minimum, inclusive, exact))
}

func (c *ctx) tooBig(path []any, kind string, maximum float64, inclusive, exact bool) {
	c.addIssue(path, []kv{{"code", str("too_big")}, {"maximum", num(maximum)}, {"type", str(kind)}, {"inclusive", boolean(inclusive)}, {"exact", boolean(exact)}, {"message", jsjson.Value{}}}, tooBigMessage(kind, maximum, inclusive, exact))
}
