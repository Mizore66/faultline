package jsjson

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/jsstr"
)

type SyntaxError struct{ Message string }

func (e *SyntaxError) Error() string { return e.Message }

const eos = -1

type token uint8

const (
	tokEOS token = iota
	tokString
	tokNumber
	tokLBrace
	tokRBrace
	tokLBrack
	tokRBrack
	tokTrue
	tokFalse
	tokNull
	tokColon
	tokComma
	tokWhitespace
	tokIllegal
)

// tokenOf is V8's one_char_json_tokens; code units above 0xFF are ILLEGAL.
func tokenOf(c int) token {
	switch {
	case c == eos:
		return tokEOS
	case c == '"':
		return tokString
	case c == '-' || (c >= '0' && c <= '9'):
		return tokNumber
	case c == '{':
		return tokLBrace
	case c == '}':
		return tokRBrace
	case c == '[':
		return tokLBrack
	case c == ']':
		return tokRBrack
	case c == 't':
		return tokTrue
	case c == 'f':
		return tokFalse
	case c == 'n':
		return tokNull
	case c == ':':
		return tokColon
	case c == ',':
		return tokComma
	case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		return tokWhitespace
	}
	return tokIllegal
}

type parser struct {
	src []uint16
	pos int
	err *SyntaxError
}

// Parse is JSON.parse(text) without a reviver.
func Parse(text string) (Value, error) {
	p := &parser{src: jsstr.ToUTF16(text)}
	v := p.parseValue()
	if p.err == nil && !p.check(tokEOS) {
		p.failAt(msgNonWhitespace)
	}
	if p.err != nil {
		return Value{}, p.err
	}
	return v, nil
}

func (p *parser) peekChar() int {
	if p.pos < len(p.src) {
		return int(p.src[p.pos])
	}
	return eos
}

func (p *parser) skipWS() {
	for p.pos < len(p.src) && tokenOf(int(p.src[p.pos])) == tokWhitespace {
		p.pos++
	}
}

// check is V8 Check: skip whitespace, consume token if it is next.
func (p *parser) check(t token) bool {
	p.skipWS()
	if tokenOf(p.peekChar()) != t {
		return false
	}
	if t != tokEOS {
		p.pos++
	}
	return true
}

// expect is V8 Expect with an explicit message.
func (p *parser) expect(t token, tmpl string) bool {
	if tokenOf(p.peekChar()) == t {
		p.pos++
		return true
	}
	p.failAt(tmpl)
	return false
}

func (p *parser) expectNext(t token, tmpl string) bool {
	p.skipWS()
	return p.expect(t, tmpl)
}

func (p *parser) parseValue() Value {
	p.skipWS()
	c := p.peekChar()
	switch tokenOf(c) {
	case tokString:
		p.pos++
		if s, ok := p.scanString(); ok {
			return MakeString(s)
		}
	case tokNumber:
		return p.parseNumber()
	case tokLBrace:
		p.pos++
		return p.parseObject()
	case tokLBrack:
		p.pos++
		return p.parseArray()
	case tokTrue:
		if p.scanLiteral("true") {
			return MakeBool(true)
		}
	case tokFalse:
		if p.scanLiteral("false") {
			return MakeBool(false)
		}
	case tokNull:
		if p.scanLiteral("null") {
			return MakeNull()
		}
	default:
		p.unexpectedChar(c)
	}
	return Value{}
}

func (p *parser) parseObject() Value {
	obj := NewObj()
	if p.check(tokRBrace) {
		return MakeObject(obj)
	}
	if !p.expectNext(tokString, msgExpectedPropNameOrRBrace) {
		return Value{}
	}
	for {
		key, ok := p.scanString()
		if !ok || !p.expectNext(tokColon, msgExpectedColonAfterPropertyName) {
			return Value{}
		}
		v := p.parseValue()
		if p.err != nil {
			return Value{}
		}
		obj.Set(key, v)
		if p.check(tokComma) {
			if !p.expectNext(tokString, msgExpectedDoubleQuotedPropertyName) {
				return Value{}
			}
			continue
		}
		if !p.expect(tokRBrace, msgExpectedCommaOrRBrace) {
			return Value{}
		}
		return MakeObject(obj)
	}
}

func (p *parser) parseArray() Value {
	if p.check(tokRBrack) {
		return MakeArray(nil)
	}
	var items []Value
	for {
		v := p.parseValue()
		if p.err != nil {
			return Value{}
		}
		items = append(items, v)
		if p.check(tokComma) {
			continue
		}
		if !p.expect(tokRBrack, msgExpectedCommaOrRBrack) {
			return Value{}
		}
		return MakeArray(items)
	}
}

// scanLiteral is V8 ScanLiteral; the first character already matched.
func (p *parser) scanLiteral(lit string) bool {
	remaining := len(p.src) - p.pos
	if remaining >= len(lit) {
		match := true
		for i := 1; i < len(lit); i++ {
			if p.src[p.pos+i] != uint16(lit[i]) {
				match = false
				break
			}
		}
		if match {
			p.pos += len(lit)
			return true
		}
	}
	p.pos++
	for i := 0; i < min(len(lit)-1, remaining-1); i++ {
		if uint16(lit[1+i]) != p.src[p.pos] {
			p.unexpectedChar(int(p.src[p.pos]))
			return false
		}
		p.pos++
	}
	p.unexpectedToken(tokEOS)
	return false
}

func isDigit(c int) bool { return c >= '0' && c <= '9' }

// isNumberPart is V8 NumberPartField: digits . e E + -.
func isNumberPart(c int) bool {
	return isDigit(c) || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

func (p *parser) parseNumber() Value {
	start := p.pos
	sign := 1
	c := p.peekChar()
	if c == '-' {
		sign = -1
		p.pos++
		c = p.peekChar()
	}
	if c == '0' {
		p.pos++
		c = p.peekChar()
		if isNumberPart(c) {
			if isDigit(c) {
				p.unexpectedToken(tokNumber)
				return Value{}
			}
		} else if sign > 0 {
			return MakeNumber(0)
		}
	} else {
		digits := p.pos
		for isDigit(p.peekChar()) {
			p.pos++
		}
		if p.pos == digits {
			p.failAt(msgNoNumberAfterMinusSign)
			return Value{}
		}
	}
	if p.peekChar() == '.' {
		p.pos++
		if !isDigit(p.peekChar()) {
			p.failAt(msgUnterminatedFractionalNumber)
			return Value{}
		}
		for isDigit(p.peekChar()) {
			p.pos++
		}
	}
	if c := p.peekChar(); c == 'e' || c == 'E' {
		p.pos++
		if c := p.peekChar(); c == '-' || c == '+' {
			p.pos++
		}
		if !isDigit(p.peekChar()) {
			p.failAt(msgExponentPartMissingNumber)
			return Value{}
		}
		for isDigit(p.peekChar()) {
			p.pos++
		}
	}
	text := make([]byte, 0, p.pos-start)
	for _, u := range p.src[start:p.pos] {
		text = append(text, byte(u))
	}
	f, _ := strconv.ParseFloat(string(text), 64) // ±Inf on overflow, like V8
	return MakeNumber(f)
}

func hexValue(c int) int {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return -1
}

// scanString is V8 ScanJsonString; the opening quote is consumed.
func (p *parser) scanString() (string, bool) {
	var units []uint16
	for {
		if p.pos >= len(p.src) {
			p.failAt(msgUnterminatedString)
			return "", false
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			return jsstr.FromUTF16(units), true
		case c == '\\':
			p.pos++
			e := p.peekChar()
			if e == eos || e > 0xFF {
				p.unexpectedChar(e)
				return "", false
			}
			switch e {
			case '"', '\\', '/':
				units = append(units, uint16(e))
			case 'b':
				units = append(units, '\b')
			case 'f':
				units = append(units, '\f')
			case 'n':
				units = append(units, '\n')
			case 'r':
				units = append(units, '\r')
			case 't':
				units = append(units, '\t')
			case 'u':
				v := 0
				for i := 0; i < 4; i++ {
					p.pos++
					d := hexValue(p.peekChar())
					if d < 0 {
						p.failAt(msgBadUnicodeEscape)
						return "", false
					}
					v = v*16 + d
				}
				units = append(units, uint16(v))
			default:
				p.failAt(msgBadEscapedCharacter)
				return "", false
			}
			p.pos++
		case c < 0x20:
			p.failAt(msgBadControlCharacter)
			return "", false
		default:
			units = append(units, c)
			p.pos++
		}
	}
}
