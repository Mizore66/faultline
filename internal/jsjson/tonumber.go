package jsjson

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// jsWhitespace reports the code points StringToNumber trims: WhiteSpace and
// LineTerminator (ECMAScript 7.2, 7.3).
func jsWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// ToNumber is ECMAScript StringToNumber, the string case of Number(s). s is
// WTF-8; a lone surrogate is never whitespace or a numeric character.
func ToNumber(s string) float64 {
	start, end := 0, len(s)
	for start < end {
		r, size := utf8.DecodeRuneInString(s[start:end])
		if !jsWhitespace(r) {
			break
		}
		start += size
	}
	for end > start {
		r, size := utf8.DecodeLastRuneInString(s[start:end])
		if !jsWhitespace(r) {
			break
		}
		end -= size
	}
	t := s[start:end]
	if t == "" {
		return 0
	}
	if len(t) > 2 && t[0] == '0' {
		radix := 0
		switch t[1] {
		case 'x', 'X':
			radix = 16
		case 'o', 'O':
			radix = 8
		case 'b', 'B':
			radix = 2
		}
		if radix != 0 {
			return nonDecimal(t[2:], radix)
		}
	}
	return decimal(t)
}

func nonDecimal(digits string, radix int) float64 {
	for i := 0; i < len(digits); i++ {
		d := digitValue(digits[i])
		if d < 0 || d >= radix {
			return math.NaN()
		}
	}
	n, _ := new(big.Int).SetString(digits, radix)
	f, _ := new(big.Float).SetInt(n).Float64()
	return f
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// decimal parses StrDecimalLiteral. The syntax is checked by hand because
// strconv.ParseFloat also accepts forms JS rejects (inf, nan, hex floats).
func decimal(t string) float64 {
	i := 0
	if t[i] == '+' || t[i] == '-' {
		i++
	}
	if t[i:] == "Infinity" {
		if t[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	digits := 0
	for i < len(t) && isDigit(int(t[i])) {
		i++
		digits++
	}
	if i < len(t) && t[i] == '.' {
		i++
		for i < len(t) && isDigit(int(t[i])) {
			i++
			digits++
		}
	}
	if digits == 0 {
		return math.NaN()
	}
	if i < len(t) && (t[i] == 'e' || t[i] == 'E') {
		i++
		if i < len(t) && (t[i] == '+' || t[i] == '-') {
			i++
		}
		exponent := 0
		for i < len(t) && isDigit(int(t[i])) {
			i++
			exponent++
		}
		if exponent == 0 {
			return math.NaN()
		}
	}
	if i != len(t) || strings.ContainsAny(t, "_") {
		return math.NaN()
	}
	f, _ := strconv.ParseFloat(t, 64) // out of range gives ±Inf, like JS
	return f
}
