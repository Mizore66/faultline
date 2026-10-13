// Package jsre runs JavaScript regular expressions (no `u` flag) on WTF-8
// strings by translating the JS source to RE2. Only the constructs the
// ported modules use are translated; anything else panics at compile time so
// a later pattern cannot be silently mistranslated.
//
// The translation preserves JS semantics where RE2's differ:
//   - \s and . use the ECMAScript sets (RE2's \s is ASCII, its . excludes
//     only \n);
//   - the i flag folds ASCII letters only, as JS does without u (RE2's (?i)
//     folds ſ to s and K (U+212A) to k);
//   - offsets are UTF-16 code units.
//
// RE2 matches code points, JS (without u) code units. They agree whenever a
// class that can match a surrogate also matches every astral character and
// U+FFFD (a lone surrogate in WTF-8 reaches RE2 as three invalid bytes, each
// read as U+FFFD), and such a class never stands where the number of units
// it consumes matters: under a bounded quantifier, next to another such
// class, or repeated around a capture. Compile checks all of that.
package jsre

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Match is one result of String.prototype.matchAll: Start and End are UTF-16
// offsets of the whole match, Groups the captures (nil = undefined).
type Match struct {
	Start, End int
	Groups     []*string
}

type Regexp struct {
	source string
	re     *regexp.Regexp
}

// MustCompile translates the JS literal /source/flags. Flags g and i are
// supported; g only matters to callers (FindAll is matchAll, Test ignores
// lastIndex).
func MustCompile(source, flags string) *Regexp {
	p := &parser{src: source}
	for _, f := range flags {
		switch {
		case f == 'g' && !p.global:
			p.global = true
		case f == 'i' && !p.fold:
			p.fold = true
		default:
			panic(fmt.Sprintf("jsre: /%s/%s: unsupported flag %q", source, flags, f))
		}
	}
	for i := 0; i < len(source); i++ {
		if source[i] >= 0x80 {
			p.fail("non-ASCII character in pattern")
		}
	}
	t := p.alternation()
	if p.pos < len(source) {
		p.fail("unbalanced )")
	}
	if t.nullable {
		p.fail("pattern can match the empty string (matchAll's empty-match stepping differs from RE2)")
	}
	return &Regexp{source: source, re: regexp.MustCompile(t.re2)}
}

// Test is re.test(s) for a non-global regexp.
func (r *Regexp) Test(s string) bool { return r.re.MatchString(s) }

// FindAll is [...s.matchAll(re)].
func (r *Regexp) FindAll(s string) []Match {
	all := r.re.FindAllStringSubmatchIndex(s, -1)
	if len(all) == 0 {
		return nil
	}
	units := unitOffsets(s)
	at := func(b int) int {
		if units[b] < 0 {
			panic(fmt.Sprintf("jsre: /%s/: match boundary inside a code point", r.source))
		}
		return units[b]
	}
	out := make([]Match, len(all))
	for i, m := range all {
		groups := make([]*string, len(m)/2-1)
		for g := range groups {
			if a := m[2+2*g]; a >= 0 {
				v := s[a:m[3+2*g]]
				at(a)
				at(m[3+2*g])
				groups[g] = &v
			}
		}
		out[i] = Match{Start: at(m[0]), End: at(m[1]), Groups: groups}
	}
	return out
}

// unitOffsets maps each byte offset of the WTF-8 string s to the UTF-16
// length of the prefix before it, or -1 inside a code point. A lone
// surrogate (three bytes) is one unit, an astral character (four) two.
func unitOffsets(s string) []int {
	out := make([]int, len(s)+1)
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c < 0x80 || c >= 0xC0 && c < 0xF0:
			out[i] = n
			n++
		case c >= 0xF0:
			out[i] = n
			n += 2
		default:
			out[i] = -1
		}
	}
	out[len(s)] = n
	return out
}

// term is a translated piece: its RE2 source, whether it can match the
// empty string, whether it can consume a surrogate code unit, and whether
// it contains a capture.
type term struct {
	re2      string
	nullable bool
	wide     bool
	captures bool
}

type parser struct {
	src          string
	pos          int
	global, fold bool
}

func (p *parser) fail(msg string) {
	panic(fmt.Sprintf("jsre: /%s/ at %d: %s", p.src, p.pos, msg))
}

func (p *parser) more() bool { return p.pos < len(p.src) }
func (p *parser) peek() byte { return p.src[p.pos] }

func (p *parser) alternation() term {
	var parts []string
	out := term{}
	for i := 0; ; i++ {
		seq := p.sequence()
		parts = append(parts, seq.re2)
		out.nullable = out.nullable || seq.nullable
		out.wide = out.wide || seq.wide
		out.captures = out.captures || seq.captures
		if !p.more() || p.peek() != '|' {
			break
		}
		p.pos++
	}
	out.re2 = strings.Join(parts, "|")
	return out
}

func (p *parser) sequence() term {
	out := term{nullable: true}
	var b strings.Builder
	lastWide := false
	for p.more() && p.peek() != '|' && p.peek() != ')' {
		t := p.quantified()
		if t.wide {
			if lastWide {
				p.fail("two classes that match surrogates are adjacent")
			}
			lastWide = true
		} else if !t.nullable {
			lastWide = false
		}
		b.WriteString(t.re2)
		out.nullable = out.nullable && t.nullable
		out.wide = out.wide || t.wide
		out.captures = out.captures || t.captures
	}
	out.re2 = b.String()
	return out
}

func (p *parser) quantified() term {
	t, assertion := p.atom()
	if !p.more() {
		return t
	}
	min, max := -1, -1 // max -1 = unbounded
	switch p.peek() {
	case '*':
		min = 0
		p.pos++
	case '+':
		min = 1
		p.pos++
	case '?':
		min, max = 0, 1
		p.pos++
	case '{':
		min, max = p.braces()
	default:
		return t
	}
	if assertion {
		p.fail("quantified assertion")
	}
	lazy := ""
	if p.more() && p.peek() == '?' {
		lazy = "?"
		p.pos++
	}
	if t.wide && (min > 1 || max > 1) {
		p.fail("bounded repetition of a class that matches surrogates")
	}
	if t.wide && t.captures && max != 1 {
		p.fail("repeated capture around a class that matches surrogates")
	}
	var q string
	switch {
	case min == 0 && max == -1:
		q = "*"
	case min == 1 && max == -1:
		q = "+"
	case min == 0 && max == 1:
		q = "?"
	case max == -1:
		q = "{" + strconv.Itoa(min) + ",}"
	case min == max:
		q = "{" + strconv.Itoa(min) + "}"
	default:
		q = "{" + strconv.Itoa(min) + "," + strconv.Itoa(max) + "}"
	}
	return term{re2: "(?:" + t.re2 + ")" + q + lazy, nullable: min == 0 || t.nullable, wide: t.wide, captures: t.captures}
}

// braces parses {n}, {n,} or {n,m}. JS (Annex B) reads any other { as a
// literal; that is not translated.
func (p *parser) braces() (int, int) {
	start := p.pos
	p.pos++
	number := func() (int, bool) {
		from := p.pos
		for p.more() && p.peek() >= '0' && p.peek() <= '9' {
			p.pos++
		}
		n, err := strconv.Atoi(p.src[from:p.pos])
		return n, err == nil && n <= 1000
	}
	min, ok := number()
	max := min
	if ok && p.more() && p.peek() == ',' {
		p.pos++
		if p.more() && p.peek() == '}' {
			max = -1
		} else {
			max, ok = number()
		}
	}
	if !ok || !p.more() || p.peek() != '}' || max != -1 && max < min {
		p.pos = start
		p.fail("{ that is not a quantifier")
	}
	p.pos++
	return min, max
}

// atom returns the next atom and whether it is an assertion.
func (p *parser) atom() (term, bool) {
	c := p.peek()
	switch c {
	case '^', '$':
		p.pos++
		return term{re2: string(c), nullable: true}, true
	case '.':
		p.pos++
		return p.class(complement(set{{'\n', '\n'}, {'\r', '\r'}, {0x2028, 0x2029}})), false
	case '[':
		return p.bracket(), false
	case '(':
		return p.group(), false
	case '\\':
		p.pos++
		if !p.more() {
			p.fail("trailing backslash")
		}
		e := p.peek()
		p.pos++
		switch e {
		case 'b', 'B':
			return term{re2: `\` + string(e), nullable: true}, true
		}
		if s, ok := classEscape(e); ok {
			return p.class(s), false
		}
		return p.literal(p.escapedChar(e)), false
	case '*', '+', '?', '{', '}', ']', ')', '|':
		p.fail("unexpected " + string(c))
	}
	p.pos++
	return p.literal(rune(c)), false
}

// escapedChar is a character escape: a control escape or an identity escape
// of ASCII punctuation. Octal, back-references, \c, \x, \u, \k and \p are
// not translated.
func (p *parser) escapedChar(e byte) rune {
	switch e {
	case 'r':
		return '\r'
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'v':
		return '\v'
	case 'f':
		return '\f'
	}
	if e >= '0' && e <= '9' || e >= 'a' && e <= 'z' || e >= 'A' && e <= 'Z' || e == '_' {
		p.pos--
		p.fail(`unsupported escape \` + string(e))
	}
	return rune(e)
}

func (p *parser) literal(c rune) term {
	if p.fold && isLetter(c) {
		return p.class(set{{c, c}})
	}
	return term{re2: regexp.QuoteMeta(string(c))}
}

func (p *parser) group() term {
	p.pos++
	capture := true
	if strings.HasPrefix(p.src[p.pos:], "?:") {
		capture = false
		p.pos += 2
	} else if p.more() && p.peek() == '?' {
		p.fail("lookaround and named groups are not translated")
	}
	inner := p.alternation()
	if !p.more() || p.peek() != ')' {
		p.fail("missing )")
	}
	p.pos++
	if capture {
		inner.re2 = "(" + inner.re2 + ")"
		inner.captures = true
	} else {
		inner.re2 = "(?:" + inner.re2 + ")"
	}
	return inner
}

func (p *parser) bracket() term {
	p.pos++
	negate := p.more() && p.peek() == '^'
	if negate {
		p.pos++
	}
	if p.more() && p.peek() == ']' {
		p.fail("empty class")
	}
	var s set
	for {
		if !p.more() {
			p.fail("missing ]")
		}
		if p.peek() == ']' {
			p.pos++
			break
		}
		lo, loSet := p.classAtom()
		if loSet == nil && p.pos+1 < len(p.src) && p.peek() == '-' && p.src[p.pos+1] != ']' {
			p.pos++
			hi, hiSet := p.classAtom()
			if hiSet != nil || hi < lo {
				p.fail("bad class range")
			}
			s = append(s, rng{lo, hi})
			continue
		}
		if loSet != nil {
			s = append(s, loSet...)
		} else {
			s = append(s, rng{lo, lo})
		}
	}
	if negate {
		return p.classNegated(s)
	}
	return p.class(s)
}

// classAtom returns one class character, or a set for \s \S \d \D \w \W.
func (p *parser) classAtom() (rune, set) {
	c := p.peek()
	p.pos++
	if c != '\\' {
		return rune(c), nil
	}
	if !p.more() {
		p.fail("trailing backslash")
	}
	e := p.peek()
	p.pos++
	if s, ok := classEscape(e); ok {
		return 0, s
	}
	if e == 'b' {
		p.pos--
		p.fail(`\b in a class`)
	}
	return p.escapedChar(e), nil
}

func (p *parser) classNegated(s set) term {
	return p.class(complement(p.folded(s)))
}

func (p *parser) class(s set) term {
	s = normalize(p.folded(s))
	// Lone surrogates arrive as U+FFFD bytes, astral characters as one
	// code point; both must stand or fall with the surrogate units.
	surrogates := coverage(s, 0xD800, 0xDFFF)
	astral := coverage(s, 0x10000, 0x10FFFF)
	replacement := coverage(s, 0xFFFD, 0xFFFD)
	if surrogates == partial || astral == partial || surrogates != astral || surrogates != replacement {
		p.fail("class treats surrogates, U+FFFD and astral characters differently")
	}
	var b strings.Builder
	b.WriteByte('[')
	empty := true
	for _, r := range complement(complement(s).union(set{{0xD800, 0xDFFF}})) {
		empty = false
		fmt.Fprintf(&b, `\x{%x}`, r.lo)
		if r.hi != r.lo {
			fmt.Fprintf(&b, `-\x{%x}`, r.hi)
		}
	}
	if empty {
		p.fail("empty class")
	}
	b.WriteByte(']')
	return term{re2: b.String(), wide: surrogates == all}
}

// folded adds the other case of every ASCII letter (JS i without u: no
// non-ASCII character folds to ASCII, and these patterns have no non-ASCII
// letters of their own).
func (p *parser) folded(s set) set {
	if !p.fold {
		return s
	}
	out := slices.Clone(s)
	for _, r := range s {
		for _, band := range []struct{ lo, hi, delta rune }{{'a', 'z', 'A' - 'a'}, {'A', 'Z', 'a' - 'A'}} {
			lo, hi := max(r.lo, band.lo), min(r.hi, band.hi)
			if lo <= hi {
				out = append(out, rng{lo + band.delta, hi + band.delta})
			}
		}
	}
	return out
}

func isLetter(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

type rng struct{ lo, hi rune }
type set []rng

var (
	space = set{{'\t', '\r'}, {' ', ' '}, {0xA0, 0xA0}, {0x1680, 0x1680}, {0x2000, 0x200A},
		{0x2028, 0x2029}, {0x202F, 0x202F}, {0x205F, 0x205F}, {0x3000, 0x3000}, {0xFEFF, 0xFEFF}}
	digit = set{{'0', '9'}}
	word  = set{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}
)

func classEscape(e byte) (set, bool) {
	switch e {
	case 's':
		return space, true
	case 'S':
		return complement(space), true
	case 'd':
		return digit, true
	case 'D':
		return complement(digit), true
	case 'w':
		return word, true
	case 'W':
		return complement(word), true
	}
	return nil, false
}

func normalize(s set) set {
	s = slices.Clone(s)
	slices.SortFunc(s, func(a, b rng) int { return int(a.lo - b.lo) })
	var out set
	for _, r := range s {
		if n := len(out); n > 0 && r.lo <= out[n-1].hi+1 {
			out[n-1].hi = max(out[n-1].hi, r.hi)
			continue
		}
		out = append(out, r)
	}
	return out
}

func (s set) union(t set) set { return normalize(append(slices.Clone(s), t...)) }

func complement(s set) set {
	var out set
	next := rune(0)
	for _, r := range normalize(s) {
		if r.lo > next {
			out = append(out, rng{next, r.lo - 1})
		}
		next = r.hi + 1
	}
	if next <= 0x10FFFF {
		out = append(out, rng{next, 0x10FFFF})
	}
	return out
}

type cover int

const (
	none cover = iota
	partial
	all
)

// coverage reports how much of [lo, hi] the normalized set s contains.
func coverage(s set, lo, hi rune) cover {
	n := rune(0)
	for _, r := range s {
		if a, b := max(r.lo, lo), min(r.hi, hi); a <= b {
			n += b - a + 1
		}
	}
	switch n {
	case 0:
		return none
	case hi - lo + 1:
		return all
	}
	return partial
}
