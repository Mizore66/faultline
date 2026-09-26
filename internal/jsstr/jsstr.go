// Package jsstr implements JavaScript string semantics on Go strings.
//
// Strings that hold JS values are WTF-8: UTF-8 that may additionally contain
// lone UTF-16 surrogates (U+D800–U+DFFF) encoded as three-byte sequences, so
// any JS string round-trips exactly.
package jsstr

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// FromUTF16 encodes JS code units as WTF-8, joining valid surrogate pairs.
func FromUTF16(units []uint16) string {
	var b strings.Builder
	b.Grow(len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) {
			if lo := rune(units[i+1]); lo >= 0xDC00 && lo <= 0xDFFF {
				b.WriteRune(utf16.DecodeRune(u, lo))
				i++
				continue
			}
		}
		if u >= 0xD800 && u <= 0xDFFF {
			b.WriteByte(byte(0xE0 | u>>12))
			b.WriteByte(byte(0x80 | (u>>6)&0x3F))
			b.WriteByte(byte(0x80 | u&0x3F))
			continue
		}
		b.WriteRune(u)
	}
	return b.String()
}

// ToUTF16 decodes WTF-8 into JS code units.
func ToUTF16(s string) []uint16 {
	units := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		if len(s)-i >= 3 && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
			units = append(units, uint16(0xD000|rune(s[i+1]&0x3F)<<6|rune(s[i+2]&0x3F)))
			i += 3
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if r >= 0x10000 {
			hi, lo := utf16.EncodeRune(r)
			units = append(units, uint16(hi), uint16(lo))
			continue
		}
		units = append(units, uint16(r))
	}
	return units
}

// Length is JS `string.length`.
func Length(s string) int { return len(ToUTF16(s)) }

// ToUTF8 is `Buffer.from(s, "utf8")` / `process.stdout.write(s)`:
// lone surrogates become U+FFFD. JS strings reach Go already decoded (argv,
// file names and contents go through nodefs.DecodeUTF8), but any other byte
// that is not valid UTF-8 is replaced too, so output is always valid UTF-8.
func ToUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string(utf16.Decode(ToUTF16(s)))
}

// IsWhitespace reports ECMAScript WhiteSpace or LineTerminator (the set
// String.prototype.trim and the regex class \s use).
func IsWhitespace(u uint16) bool {
	switch u {
	case 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return u >= 0x2000 && u <= 0x200A
}

// Trim is JS `String.prototype.trim`.
func Trim(s string) string {
	units := ToUTF16(s)
	start, end := 0, len(units)
	for start < end && IsWhitespace(units[start]) {
		start++
	}
	for end > start && IsWhitespace(units[end-1]) {
		end--
	}
	return FromUTF16(units[start:end])
}
