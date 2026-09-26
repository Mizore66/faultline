package jsjson

import (
	"fmt"
	"math"
	"strings"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// Quote is JSON.stringify(string) with ES2019 well-formed output.
func Quote(s string) string {
	units := jsstr.ToUTF16(s)
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u == '"':
			b.WriteString(`\"`)
		case u == '\\':
			b.WriteString(`\\`)
		case u == '\b':
			b.WriteString(`\b`)
		case u == '\f':
			b.WriteString(`\f`)
		case u == '\n':
			b.WriteString(`\n`)
		case u == '\r':
			b.WriteString(`\r`)
		case u == '\t':
			b.WriteString(`\t`)
		case u < 0x20:
			fmt.Fprintf(&b, `\u%04x`, u)
		case u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF:
			b.WriteString(jsstr.FromUTF16(units[i : i+2]))
			i++
		case u >= 0xD800 && u <= 0xDFFF:
			fmt.Fprintf(&b, `\u%04x`, u)
		default:
			b.WriteString(jsstr.FromUTF16(units[i : i+1]))
		}
	}
	b.WriteByte('"')
	return b.String()
}

// RangeError is a JS RangeError.
type RangeError struct{ Message string }

func (e *RangeError) Error() string { return e.Message }

// ErrStackOverflow is the RangeError V8 throws when JSON.stringify recurses
// past the native stack limit.
var ErrStackOverflow = &RangeError{"Maximum call stack size exceeded"}

// ErrInvalidStringLength is the RangeError V8 throws when the result would
// be longer than String::kMaxLength.
var ErrInvalidStringLength = &RangeError{jsexc.ErrInvalidStringLength.Error()}

// Stringify is JSON.stringify(v).
func Stringify(v Value) string { return StringifyIndent(v, "") }

// StringifyIndent is JSON.stringify(v, null, indent). Undefined object members
// are omitted; Undefined array elements and non-finite numbers print null.
// It has no stack or length limit; use StringifyIndentChecked for values from
// input.
func StringifyIndent(v Value, indent string) string {
	w := &writer{indent: indent, budget: -1, maxUnits: -1}
	w.write(v, "")
	return w.b.String()
}

// StringifyIndentChecked is StringifyIndent with V8's limits: it fails with
// ErrStackOverflow where the native stack would run out (see stackModel)
// and, if serialization completes, with ErrInvalidStringLength when the
// result exceeds String::kMaxLength. V8 keeps serializing after the string
// overflows, so a later stack overflow still wins.
func StringifyIndentChecked(v Value, indent string) (string, error) {
	w := &writer{indent: indent, budget: v8Stack.budget + siteOffset, maxUnits: jsexc.MaxStringLength}
	w.write(v, "")
	switch {
	case w.overflow:
		return "", ErrStackOverflow
	case w.tooLong:
		return "", ErrInvalidStringLength
	}
	return w.b.String(), nil
}

type writer struct {
	b        strings.Builder
	indent   string
	budget   int // remaining stack; -1 means unlimited
	overflow bool
	units    int // UTF-16 length written so far
	maxUnits int // -1 means unlimited
	tooLong  bool
}

// str appends s, tracking the JS length. Past the limit nothing more is
// kept, but serialization continues for the stack check.
func (w *writer) str(s string) {
	if w.tooLong {
		return
	}
	if w.maxUnits >= 0 {
		w.units += jsstr.UTF16Len(s)
		if w.units > w.maxUnits {
			w.tooLong = true
			w.b.Reset()
			return
		}
	}
	w.b.WriteString(s)
}

// enter charges one native frame; it reports false once the stack is exhausted.
func (w *writer) enter(cost int) bool {
	if w.budget < 0 {
		return true
	}
	if w.overflow || w.budget < cost {
		w.overflow = true
		return false
	}
	w.budget -= cost
	return true
}

func (w *writer) leave(cost int) {
	if w.budget >= 0 {
		w.budget += cost
	}
}

// write serializes v. Following V8's JsonStringifier, only a non-empty
// array or object pushes a native frame (`[]` and `{}` return first);
// primitives and empty containers cost nothing beyond the replacer call the
// budget already accounts for.
func (w *writer) write(v Value, current string) {
	indent := w.indent
	if w.overflow {
		return
	}
	switch v.kind {
	case Undefined, Null:
		w.str("null")
	case Bool:
		if v.b {
			w.str("true")
		} else {
			w.str("false")
		}
	case Number:
		if math.IsNaN(v.n) || math.IsInf(v.n, 0) {
			w.str("null")
		} else {
			w.str(FormatNumber(v.n))
		}
	case String:
		w.str(Quote(v.s))
	case Array:
		if len(v.arr) == 0 {
			w.str("[]")
			return
		}
		if !w.enter(v8Stack.array) {
			return
		}
		defer w.leave(v8Stack.array)
		inner := current + indent
		w.str("[")
		for i, item := range v.arr {
			if i > 0 {
				w.str(",")
			}
			if indent != "" {
				w.str("\n" + inner)
			}
			w.write(item, inner)
			if w.overflow {
				return
			}
		}
		if indent != "" {
			w.str("\n" + current)
		}
		w.str("]")
	case Object:
		keys := v.obj.orderedKeys()
		if len(keys) == 0 {
			w.str("{}")
			return
		}
		cost := v8Stack.object
		if v.obj.slowForStringify() {
			cost = v8Stack.slowObject
		}
		if !w.enter(cost) {
			return
		}
		defer w.leave(cost)
		inner := current + indent
		wrote := false
		w.str("{")
		for _, k := range keys {
			child := v.obj.values[k]
			if child.kind == Undefined {
				continue
			}
			if wrote {
				w.str(",")
			}
			if indent != "" {
				w.str("\n" + inner)
			}
			w.str(Quote(k))
			w.str(":")
			if indent != "" {
				w.str(" ")
			}
			w.write(child, inner)
			if w.overflow {
				return
			}
			wrote = true
		}
		if wrote && indent != "" {
			w.str("\n" + current)
		}
		w.str("}")
	}
}

// slowForStringify reports whether V8 serializes the object through
// SerializeJSReceiverSlow, whose frame is larger: objects with array-index
// keys (elements) and objects JSON.parse built in dictionary mode, which
// happens from 128 properties on (measured through the CLI: 127 members
// stay on the fast path).
func (o *Obj) slowForStringify() bool { return len(o.idx) > 0 || o.Len() >= 128 }
