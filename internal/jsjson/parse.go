package jsjson

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/jsexc"
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
	src, i := p.src, p.pos
	for i < len(src) {
		if c := src[i]; c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		i++
	}
	p.pos = i
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

// frame is one open container on the explicit parse stack.
type frame struct {
	obj   *Obj // nil for an array
	key   string
	items []Value
	count int // array elements parsed; past maxArrayElements-1 they are not kept
	// elements and maxIndex are an object's index-keyed entries parsed,
	// duplicates included, and the largest index among them.
	elements int
	maxIndex uint32
}

// maxArrayElements is the first array length V8's JSON.parse cannot
// allocate: NewJSArray aborts the process with "Fatal JavaScript invalid
// size error 134217728" once the closing bracket is reached.
const maxArrayElements = 1 << 27

// checkElements is how V8's JSON.parse stores an object's index-keyed
// entries (BuildJsonObject), when the object closes: in a NumberDictionary
// sized for all of them when that is smaller than a FixedArray up to the
// largest index (ShouldConvertToSlowElements), otherwise in that
// FixedArray. Either allocation can be impossible, and V8 then aborts the
// process: a dictionary of maxNamedEntries or more entries (its capacity
// limit is the NameDictionary's), or a FixedArray of 2^27 or more slots.
func checkElements(elements int, maxIndex uint32) {
	length := uint64(maxIndex) + 1
	if slowElements(elements, length) {
		if elements >= maxNamedEntries {
			jsexc.FatalOOM("invalid table size")
		}
	} else if length >= maxArrayElements {
		jsexc.FatalInvalidFixedArraySize(int(length))
	}
}

// maxGrownArrayLength is the first length an array grown one push at a time
// from empty cannot reach: V8 grows a full fast array's backing store to
// n + n/2 + 16 for the n elements the push needs
// (CalculateNewElementsCapacity), which from 0 runs 17, 43, ... 75,209,227,
// 112,813,858, then 169,220,804, past FixedArray::kMaxCapacity (2^27 - 1),
// so the push that would make the array 112,813,859 long throws instead
// (measured on Node 22.22.2).
const maxGrownArrayLength = 112_813_859

// ErrInvalidArrayLength is the catchable RangeError such a push throws.
var ErrInvalidArrayLength = &RangeError{"Invalid array length"}

// CheckGrownLength throws ErrInvalidArrayLength where building an array of n
// elements one push at a time (Array.prototype.filter, push in a loop)
// throws in V8.
func CheckGrownLength(n int) {
	if n >= maxGrownArrayLength {
		jsexc.Throw(ErrInvalidArrayLength)
	}
}

// CheckSplitParts is the allocation String.prototype.split makes for its
// result: n parts need a FixedArray of n slots, and 2^27 or more abort the
// process as checkElements does.
func CheckSplitParts(n int) {
	if n >= maxArrayElements {
		jsexc.FatalInvalidFixedArraySize(n)
	}
}

// slowElements is ShouldConvertToSlowElements(used, capacity):
// kPreferFastElementsSizeFactor (3) × ComputeCapacity(used) × kEntrySize
// (3) <= capacity, where ComputeCapacity rounds used + used/2 up to a power
// of two, at least 4.
func slowElements(used int, capacity uint64) bool {
	c := uint64(4)
	for c < uint64(used+used/2) {
		c <<= 1
	}
	return 3*c*3 <= capacity
}

// maxNamedEntries is the first named-entry count whose NameDictionary V8
// cannot allocate (capacity RoundUpPow2(n + n/2) past kMaxCapacity): Node
// dies with "FATAL ERROR: invalid table size Allocation failed - JavaScript
// heap out of memory" when the object closes.
const maxNamedEntries = 22_369_622

// parseValue is V8 JsonParser::ParseJsonValue. Like V8 it keeps open
// containers on an explicit stack instead of recursing, so nesting depth is
// limited only by memory; the token checks and error positions follow the
// same order as V8's iterative loop.
func (p *parser) parseValue() Value {
	var stack []frame
	for {
		// Parse the start of one value.
		p.skipWS()
		c := p.peekChar()
		var v Value
		switch tokenOf(c) {
		case tokString:
			p.pos++
			s, ok := p.scanString()
			if !ok {
				return Value{}
			}
			v = MakeString(s)
		case tokNumber:
			v = p.parseNumber()
			if p.err != nil {
				return Value{}
			}
		case tokLBrace:
			p.pos++
			if p.check(tokRBrace) {
				v = MakeObject(NewObj())
				break
			}
			if !p.expectNext(tokString, msgExpectedPropNameOrRBrace) {
				return Value{}
			}
			key, ok := p.scanString()
			if !ok || !p.expectNext(tokColon, msgExpectedColonAfterPropertyName) {
				return Value{}
			}
			stack = append(stack, frame{obj: NewObj(), key: key})
			continue
		case tokLBrack:
			p.pos++
			if p.check(tokRBrack) {
				v = MakeArray(nil)
				break
			}
			stack = append(stack, frame{})
			continue
		case tokTrue:
			if !p.scanLiteral("true") {
				return Value{}
			}
			v = MakeBool(true)
		case tokFalse:
			if !p.scanLiteral("false") {
				return Value{}
			}
			v = MakeBool(false)
		case tokNull:
			if !p.scanLiteral("null") {
				return Value{}
			}
			v = MakeNull()
		default:
			p.unexpectedChar(c)
			return Value{}
		}

		// Attach v to the open containers, closing those that end here.
	attach:
		for {
			if len(stack) == 0 {
				return v
			}
			top := &stack[len(stack)-1]
			if top.obj != nil {
				// V8 sizes an object's property dictionary from the named
				// entries it parsed, duplicates included (index keys are
				// elements).
				if i, index := arrayIndex(top.key); index {
					top.elements++
					top.maxIndex = max(top.maxIndex, i)
				} else {
					top.obj.namedEntries++
				}
				// Every member is kept: only an object that will abort when
				// it closes (maxNamedEntries or more named entries) can drop
				// them. Index entries past maxNamedEntries abort only in the
				// dictionary branch (checkElements); in the FixedArray branch
				// V8 keeps them and every member after them.
				if top.obj.namedEntries < maxNamedEntries {
					top.obj.Set(top.key, v)
				}
				if p.check(tokComma) {
					if !p.expectNext(tokString, msgExpectedDoubleQuotedPropertyName) {
						return Value{}
					}
					key, ok := p.scanString()
					if !ok || !p.expectNext(tokColon, msgExpectedColonAfterPropertyName) {
						return Value{}
					}
					top.key = key
					break attach
				}
				if !p.expect(tokRBrace, msgExpectedCommaOrRBrace) {
					return Value{}
				}
				if top.elements > 0 {
					checkElements(top.elements, top.maxIndex)
				}
				if top.obj.namedEntries >= maxNamedEntries {
					jsexc.FatalOOM("invalid table size")
				}
				v = MakeObject(top.obj)
			} else {
				// V8 parses to the ']' before allocating, then reports the
				// real length.
				top.count++
				if top.count < maxArrayElements {
					top.items = append(top.items, v)
				}
				if p.check(tokComma) {
					break attach
				}
				if !p.expect(tokRBrack, msgExpectedCommaOrRBrack) {
					return Value{}
				}
				if top.count >= maxArrayElements {
					jsexc.FatalInvalidSize(top.count)
				}
				v = MakeArray(top.items)
			}
			stack = stack[:len(stack)-1]
		}
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
	// Fast path: no escape before the closing quote, so the string is the
	// source units as they are.
	for end := p.pos; end < len(p.src); end++ {
		c := p.src[end]
		if c == '"' {
			s := jsstr.FromUTF16(p.src[p.pos:end])
			p.pos = end + 1
			return s, true
		}
		if c == '\\' || c < 0x20 {
			break
		}
	}
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
