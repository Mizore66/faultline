// Package nodefs reproduces the Node fs/path behavior the verifiers observe.
package nodefs

import "strings"

// DecodeUTF8 is Buffer.toString("utf8") (WHATWG UTF-8 decode with replacement).
func DecodeUTF8(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	cp, needed, seen := rune(0), 0, 0
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c <= 0x7F:
				out.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				} else if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				} else if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x07)
			default:
				out.WriteRune(0xFFFD)
			}
			continue
		}
		if c < lower || c > upper {
			cp, needed, seen = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(0xFFFD)
			i-- // reprocess this byte
			continue
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		if seen == needed {
			out.WriteRune(cp)
			cp, needed, seen = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(0xFFFD)
	}
	return out.String()
}

// decodedUTF16Length is len(ToUTF16(DecodeUTF8(b))) without building the string.
func decodedUTF16Length(b []byte) int {
	n := 0
	needed, seen := 0, 0
	first := byte(0)
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c <= 0x7F:
				n++
			case c >= 0xC2 && c <= 0xDF:
				needed, first = 1, c
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				} else if c == 0xED {
					upper = 0x9F
				}
				needed, first = 2, c
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				} else if c == 0xF4 {
					upper = 0x8F
				}
				needed, first = 3, c
			default:
				n++
			}
			continue
		}
		if c < lower || c > upper {
			needed, seen = 0, 0
			lower, upper = 0x80, 0xBF
			n++
			i--
			continue
		}
		lower, upper = 0x80, 0xBF
		seen++
		if seen == needed {
			if first >= 0xF0 {
				n += 2
			} else {
				n++
			}
			needed, seen = 0, 0
		}
	}
	if needed != 0 {
		n++
	}
	return n
}
