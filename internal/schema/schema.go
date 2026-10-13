// Package schema reproduces the subset of zod v3 (3.25.76) that FaultLine's
// verifiers use: parsing, normalized output, issues, and ZodError.message.
package schema

import "github.com/Mizore66/faultline/internal/jsjson"

type status uint8

const (
	valid status = iota
	dirty
	aborted
)

func worst(a, b status) status { return max(a, b) }

type ctx struct{ issues []Issue }

// Schema is a zod schema. path elements are string keys or int indexes.
type Schema interface {
	parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status)
}

// Parse is zod safeParse.
func Parse(s Schema, v jsjson.Value) (jsjson.Value, []Issue, bool) {
	c := &ctx{}
	out, st := s.parse(c, v, nil)
	if st != valid || len(c.issues) > 0 {
		return jsjson.Value{}, c.issues, false
	}
	return out, nil, true
}

// receivedType is zod getParsedType for JSON values.
func receivedType(v jsjson.Value) string {
	switch v.Kind() {
	case jsjson.Undefined:
		return "undefined"
	case jsjson.Null:
		return "null"
	case jsjson.Bool:
		return "boolean"
	case jsjson.Number:
		if v.Num() != v.Num() {
			return "nan"
		}
		return "number"
	case jsjson.String:
		return "string"
	case jsjson.Array:
		return "array"
	}
	return "object"
}

func extend(path []any, elem any) []any {
	out := make([]any, len(path)+1)
	copy(out, path)
	out[len(path)] = elem
	return out
}
