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

// Stringify is JSON.stringify(v).
func Stringify(v Value) string { return StringifyIndent(v, "") }

// StringifyIndent is JSON.stringify(v, null, indent). Undefined object members
// are omitted; Undefined array elements and non-finite numbers print null.
func StringifyIndent(v Value, indent string) string {
	var b strings.Builder
	write(&b, v, indent, "")
	return b.String()
}

func write(b *strings.Builder, v Value, indent, current string) {
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
			write(b, item, indent, inner)
		}
		if indent != "" {
			b.WriteString("\n" + current)
		}
		b.WriteByte(']')
	case Object:
		inner := current + indent
		wrote := false
		b.WriteByte('{')
		for _, k := range v.obj.keys {
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
			write(b, child, indent, inner)
			wrote = true
		}
		if wrote && indent != "" {
			b.WriteString("\n" + current)
		}
		b.WriteByte('}')
	}
}
