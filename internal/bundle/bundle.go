// Package bundle holds helpers shared by the proof bundle verifier ports.
package bundle

import (
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
)

// Result mirrors the TS verification results. Nil pointers are TS null.
type Result struct {
	Valid              bool
	CheckedFiles       int
	Errors             []string
	RootDigest         *string
	ExternalRootStatus string
	Classification     *string
}

// Thrown carries a JS exception through Go panics (see internal/jsexc).
type Thrown = jsexc.Thrown

func Throw(err error) { jsexc.Throw(err) }

func Must[T any](v T, err error) T { return jsexc.Must(v, err) }

// Try runs fn like a JS try block and returns the caught exception.
func Try(fn func()) error { return jsexc.Try(fn) }

// Catch recovers a Thrown panic; use as `defer bundle.Catch(handler)`.
// It must be deferred directly so recover sees the panic.
func Catch(handler func(error)) {
	if r := recover(); r != nil {
		t, ok := r.(Thrown)
		if !ok {
			panic(r)
		}
		handler(t.Err)
	}
}

// ParseValue is zod `Schema.parse(value)`; failures are *schema.Error.
func ParseValue(v jsjson.Value, s schema.Schema) (jsjson.Value, error) {
	out, issues, ok := schema.Parse(s, v)
	if !ok {
		// The message is computed when read, like zod's getter, so a
		// RangeError from it escapes the enclosing catch handler as in TS.
		return jsjson.Value{}, &schema.Error{Issues: issues}
	}
	return out, nil
}

// ParseJSONFile is JSON.parse(readFileSync(path, "utf8")).
func ParseJSONFile(path string) (jsjson.Value, error) {
	text, err := nodefs.ReadText(path)
	if err != nil {
		return jsjson.Value{}, err
	}
	return jsjson.Parse(text)
}

// ParseFile is Schema.parse(JSON.parse(readFileSync(path, "utf8"))).
func ParseFile(path string, s schema.Schema) (jsjson.Value, error) {
	v, err := ParseJSONFile(path)
	if err != nil {
		return jsjson.Value{}, err
	}
	return ParseValue(v, s)
}

// SameCanonical is canonicalJson(a) === canonicalJson(b); it throws like TS.
func SameCanonical(a, b jsjson.Value) bool {
	return Must(canonical.CanonicalJSON(a)) == Must(canonical.CanonicalJSON(b))
}

// Without is `const { key: _, ...rest } = v`.
func Without(v jsjson.Value, key string) jsjson.Value {
	out := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		if k != key {
			out.Set(k, v.Obj().Field(k))
		}
	}
	return jsjson.MakeObject(out)
}

func Strings(v jsjson.Value) []string {
	out := make([]string, 0, len(v.Items()))
	for _, item := range v.Items() {
		out = append(out, item.Str())
	}
	return out
}

// DistinctCount is new Set(values).size.
func DistinctCount(values []string) int {
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	return len(seen)
}
