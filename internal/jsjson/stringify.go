package jsjson

import (
	"fmt"
	"math"
	"strings"

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

// V8's JsonStringifier recurses natively once per nested array or object and
// throws ErrStackOverflow when the stack runs out. The budget below reproduces
// where that happens for zod's ZodError.message (JSON.stringify(issues,
// replacer, 2)) in `node dist/cli.js verify` on Node 22.22 (Linux x64): an
// array level costs about 1.87 object levels. Calibrated by bisection: a
// literal field nested 2,233 arrays or 4,166 single-key objects deep still
// stringifies; one more level overflows. The limit depends on V8's frame
// sizes, so other platforms can differ by a few levels (KNOWN_DIFFERENCES.md).
const (
	stringifyStackBudget = 4_169_700
	arrayFrameCost       = 1866
	objectFrameCost      = 1000
)

// Stringify is JSON.stringify(v).
func Stringify(v Value) string { return StringifyIndent(v, "") }

// StringifyIndent is JSON.stringify(v, null, indent). Undefined object members
// are omitted; Undefined array elements and non-finite numbers print null.
// It has no stack limit; use StringifyIndentChecked for values from input.
func StringifyIndent(v Value, indent string) string {
	w := &writer{indent: indent, budget: -1}
	w.write(v, "")
	return w.b.String()
}

// StringifyIndentChecked is StringifyIndent that fails with ErrStackOverflow
// where V8 would (see stringifyStackBudget).
func StringifyIndentChecked(v Value, indent string) (string, error) {
	w := &writer{indent: indent, budget: stringifyStackBudget}
	w.write(v, "")
	if w.overflow {
		return "", ErrStackOverflow
	}
	return w.b.String(), nil
}

type writer struct {
	b        strings.Builder
	indent   string
	budget   int // remaining stack; -1 means unlimited
	overflow bool
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

func (w *writer) write(v Value, current string) {
	b, indent := &w.b, w.indent
	if w.overflow {
		return
	}
	switch v.kind {
	case Undefined, Null:
		b.WriteString("null")
	case Bool:
		if v.b {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case Number:
		if math.IsNaN(v.n) || math.IsInf(v.n, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(FormatNumber(v.n))
		}
	case String:
		b.WriteString(Quote(v.s))
	case Array:
		if !w.enter(arrayFrameCost) {
			return
		}
		defer w.leave(arrayFrameCost)
		if len(v.arr) == 0 {
			b.WriteString("[]")
			return
		}
		inner := current + indent
		b.WriteByte('[')
		for i, item := range v.arr {
			if i > 0 {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteString("\n" + inner)
			}
			w.write(item, inner)
		}
		if indent != "" {
			b.WriteString("\n" + current)
		}
		b.WriteByte(']')
	case Object:
		if !w.enter(objectFrameCost) {
			return
		}
		defer w.leave(objectFrameCost)
		inner := current + indent
		wrote := false
		b.WriteByte('{')
		for _, k := range v.obj.orderedKeys() {
			child := v.obj.values[k]
			if child.kind == Undefined {
				continue
			}
			if wrote {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteString("\n" + inner)
			}
			b.WriteString(Quote(k))
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			w.write(child, inner)
			wrote = true
		}
		if wrote && indent != "" {
			b.WriteString("\n" + current)
		}
		b.WriteByte('}')
	}
}
