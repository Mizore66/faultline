package schema

import (
	"math"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// StringCheck runs one zod string check; it may rewrite *s (zod .trim()).
type StringCheck func(c *ctx, s *string, path []any) bool

type stringSchema struct{ checks []StringCheck }

func String(checks ...StringCheck) Schema { return &stringSchema{checks} }

func (s *stringSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.String {
		c.invalidType(path, "string", v)
		return jsjson.Value{}, aborted
	}
	value := v.Str()
	st := valid
	for _, check := range s.checks {
		if !check(c, &value, path) {
			st = dirty
		}
	}
	return jsjson.MakeString(value), st
}

// Trim is zod .trim(): later checks and the output see the trimmed string.
func Trim() StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		*s = jsstr.Trim(*s)
		return true
	}
}

func MinLength(n int) StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if jsstr.Length(*s) >= n {
			return true
		}
		c.tooSmall(path, "string", float64(n), true, false)
		return false
	}
}

func MaxLength(n int) StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if jsstr.Length(*s) <= n {
			return true
		}
		c.tooBig(path, "string", float64(n), true, false)
		return false
	}
}

func Regex(m Matcher, message string) StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if m(*s) {
			return true
		}
		msg := jsjson.Value{}
		if message != "" {
			msg = str(message)
		}
		c.addIssue(path, []kv{{"validation", str("regex")}, {"code", str("invalid_string")}, {"message", msg}}, "Invalid")
		return false
	}
}

func Datetime() StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if datetimePattern.MatchString(*s) {
			return true
		}
		c.addIssue(path, []kv{{"code", str("invalid_string")}, {"validation", str("datetime")}, {"message", jsjson.Value{}}}, "Invalid datetime")
		return false
	}
}

func UUID() StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if uuidPattern.MatchString(*s) {
			return true
		}
		c.addIssue(path, []kv{{"validation", str("uuid")}, {"code", str("invalid_string")}, {"message", jsjson.Value{}}}, "Invalid uuid")
		return false
	}
}

type NumberCheck func(c *ctx, n float64, path []any) bool

type numberSchema struct{ checks []NumberCheck }

func Number(checks ...NumberCheck) Schema { return &numberSchema{checks} }

func (s *numberSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Number || math.IsNaN(v.Num()) {
		c.invalidType(path, "number", v)
		return jsjson.Value{}, aborted
	}
	st := valid
	for _, check := range s.checks {
		if !check(c, v.Num(), path) {
			st = dirty
		}
	}
	return v, st
}

func Int() NumberCheck {
	return func(c *ctx, n float64, path []any) bool {
		if !math.IsInf(n, 0) && n == math.Trunc(n) {
			return true
		}
		c.addIssue(path, []kv{{"code", str("invalid_type")}, {"expected", str("integer")}, {"received", str("float")}, {"message", jsjson.Value{}}}, "Expected integer, received float")
		return false
	}
}

func Gte(minimum float64) NumberCheck {
	return func(c *ctx, n float64, path []any) bool {
		if n >= minimum {
			return true
		}
		c.tooSmall(path, "number", minimum, true, false)
		return false
	}
}

func Nonnegative() NumberCheck { return Gte(0) }

func Positive() NumberCheck {
	return func(c *ctx, n float64, path []any) bool {
		if n > 0 {
			return true
		}
		c.tooSmall(path, "number", 0, false, false)
		return false
	}
}

func Lte(maximum float64) NumberCheck {
	return func(c *ctx, n float64, path []any) bool {
		if n <= maximum {
			return true
		}
		c.tooBig(path, "number", maximum, true, false)
		return false
	}
}

type booleanSchema struct{}

func Boolean() Schema { return booleanSchema{} }

func (booleanSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Bool {
		c.invalidType(path, "boolean", v)
		return jsjson.Value{}, aborted
	}
	return v, valid
}

type literalSchema struct{ value jsjson.Value }

func Literal(v jsjson.Value) Schema { return &literalSchema{v} }
func LiteralString(s string) Schema { return &literalSchema{str(s)} }

// strictEqual is JS === for the primitive literals zod schemas use.
func strictEqual(a, b jsjson.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case jsjson.String:
		return a.Str() == b.Str()
	case jsjson.Number:
		return a.Num() == b.Num()
	case jsjson.Bool:
		return a.Bool() == b.Bool()
	case jsjson.Null, jsjson.Undefined:
		return true
	}
	return false
}

// parse ports ZodLiteral: `input.data !== this._def.value`. A missing key has
// received undefined, which JSON.stringify drops from the issue.
func (s *literalSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if !strictEqual(v, s.value) {
		c.addIssue(path, []kv{{"received", v}, {"code", str("invalid_literal")}, {"expected", s.value}}, "Invalid literal value, expected "+jsjson.Stringify(s.value))
		return jsjson.Value{}, aborted
	}
	return v, valid
}

type enumSchema struct{ values []string }

func Enum(values ...string) Schema { return &enumSchema{values} }

func (s *enumSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.String {
		expected, received := joinValues(s.values, " | "), receivedType(v)
		message := "Expected " + expected + ", received " + received
		if received == "undefined" {
			message = "Required"
		}
		c.addIssue(path, []kv{{"expected", str(expected)}, {"received", str(received)}, {"code", str("invalid_type")}}, message)
		return jsjson.Value{}, aborted
	}
	for _, option := range s.values {
		if option == v.Str() {
			return v, valid
		}
	}
	c.addIssue(path, []kv{{"received", v}, {"code", str("invalid_enum_value")}, {"options", strList(s.values)}},
		"Invalid enum value. Expected "+joinValues(s.values, " | ")+", received '"+v.Str()+"'")
	return jsjson.Value{}, aborted
}
