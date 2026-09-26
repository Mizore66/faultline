package jsjson

import (
	"fmt"

	"github.com/Mizore66/faultline/internal/jsstr"
)

const (
	msgUnexpectedEOS                    = "Unexpected end of JSON input"
	msgUnexpectedNumber                 = "Unexpected number in JSON at position %d (line %d column %d)"
	msgUnexpectedString                 = "Unexpected string in JSON at position %d (line %d column %d)"
	msgUnterminatedString               = "Unterminated string in JSON at position %d (line %d column %d)"
	msgExpectedPropNameOrRBrace         = "Expected property name or '}' in JSON at position %d (line %d column %d)"
	msgExpectedCommaOrRBrack            = "Expected ',' or ']' after array element in JSON at position %d (line %d column %d)"
	msgExpectedCommaOrRBrace            = "Expected ',' or '}' after property value in JSON at position %d (line %d column %d)"
	msgExpectedDoubleQuotedPropertyName = "Expected double-quoted property name in JSON at position %d (line %d column %d)"
	msgExponentPartMissingNumber        = "Exponent part is missing a number in JSON at position %d (line %d column %d)"
	msgExpectedColonAfterPropertyName   = "Expected ':' after property name in JSON at position %d (line %d column %d)"
	msgUnterminatedFractionalNumber     = "Unterminated fractional number in JSON at position %d (line %d column %d)"
	msgNonWhitespace                    = "Unexpected non-whitespace character after JSON at position %d (line %d column %d)"
	msgBadEscapedCharacter              = "Bad escaped character in JSON at position %d (line %d column %d)"
	msgBadControlCharacter              = "Bad control character in string literal in JSON at position %d (line %d column %d)"
	msgBadUnicodeEscape                 = "Bad Unicode escape in JSON at position %d (line %d column %d)"
	msgNoNumberAfterMinusSign           = "No number after minus sign in JSON at position %d (line %d column %d)"
)

const (
	maxContextCharacters              = 10
	minOriginalSourceLengthForContext = maxContextCharacters*2 + 1
)

func (p *parser) setError(message string) {
	if p.err == nil {
		p.err = &SyntaxError{message}
	}
	p.pos = len(p.src) // V8 moves the cursor to the end
}

// location is V8 CalculateFileLocation: \r\n counts once.
func (p *parser) location(pos int) (line, column int) {
	line, lastBreak := 1, 0
	for i := 0; i < pos; i++ {
		if p.src[i] == '\r' && i < pos-1 && p.src[i+1] == '\n' {
			i++
		}
		if p.src[i] == '\r' || p.src[i] == '\n' {
			line++
			lastBreak = i + 1
		}
	}
	return line, 1 + pos - lastBreak
}

func (p *parser) failAt(tmpl string) {
	if p.err != nil {
		return
	}
	line, column := p.location(p.pos)
	p.setError(fmt.Sprintf(tmpl, p.pos, line, column))
}

// unexpectedChar is V8 ReportUnexpectedCharacter.
func (p *parser) unexpectedChar(c int) {
	t := tokIllegal
	if c == eos {
		t = tokEOS
	} else if c <= 0xFF {
		t = tokenOf(c)
	}
	p.unexpectedToken(t)
}

// unexpectedToken is V8 ReportUnexpectedToken without an explicit message.
func (p *parser) unexpectedToken(t token) {
	if p.err != nil {
		return
	}
	switch t {
	case tokEOS:
		p.setError(msgUnexpectedEOS)
	case tokNumber:
		p.failAt(msgUnexpectedNumber)
	case tokString:
		p.failAt(msgUnexpectedString)
	default:
		if p.isSpecialString() {
			p.setError(`"` + jsstr.FromUTF16(p.src) + `" is not valid JSON`)
			return
		}
		p.setError(p.withEllipses())
	}
}

func (p *parser) isSpecialString() bool {
	switch jsstr.FromUTF16(p.src) {
	case "[object Object]", "undefined", "Infinity", "NaN":
		return true
	}
	return false
}

// withEllipses is V8 GetErrorMessageWithEllipses.
func (p *parser) withEllipses() string {
	tok := jsstr.FromUTF16(p.src[p.pos : p.pos+1])
	n := len(p.src)
	if n < minOriginalSourceLengthForContext {
		return "Unexpected token '" + tok + "', \"" + jsstr.FromUTF16(p.src) + "\" is not valid JSON"
	}
	switch {
	case p.pos < maxContextCharacters:
		return "Unexpected token '" + tok + "', \"" + jsstr.FromUTF16(p.src[:p.pos+maxContextCharacters]) + "\"... is not valid JSON"
	case p.pos < n-maxContextCharacters:
		return "Unexpected token '" + tok + "', ...\"" + jsstr.FromUTF16(p.src[p.pos-maxContextCharacters:p.pos+maxContextCharacters]) + "\"... is not valid JSON"
	default:
		return "Unexpected token '" + tok + "', ...\"" + jsstr.FromUTF16(p.src[p.pos-maxContextCharacters:]) + "\" is not valid JSON"
	}
}
