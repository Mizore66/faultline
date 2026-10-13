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
//   - offsets are UTF-16 code units;
//   - a capture under a quantifier is not translated (JS clears it at each
//     iteration and refuses an empty iteration of a min-0 quantifier; RE2
//     keeps the last value and allows the empty one);
//   - a quantifier over something that can match the empty string is not
//     translated (JS refuses an empty iteration and backtracks into the
//     body; RE2 accepts it, so the two can choose different matches);
//   - \B is not translated (it holds between the two units of a pair, where
//     RE2 has no position).
//
// Code units against code points. JS without u matches code units, RE2 code
// points: an astral character is two JS units (a surrogate pair) but one
// RE2 rune, and a lone surrogate is one JS unit but three invalid WTF-8
// bytes, which RE2 reads as three U+FFFD runes. A class is "wide" when it
// can match a surrogate unit. Compile requires every wide class to treat all
// surrogates, U+FFFD and all astral characters alike (so a JS unit run and
// an RE2 rune run over the same text cover the same extent), and accepts a
// wide class only in a "wide run" (see wideRun) whose follow set cannot
// consume a surrogate unit or end between the two units of a pair (see
// checkFollow).
//
// What the gate guarantees. Given that the only assertions are ^, $ and \b
// (none of which holds between the two units of a pair, since both are
// non-word characters), that no capture is under a quantifier, and that no
// quantified body can match the empty string (so JS's refusal of empty
// iterations never changes the outcome), JS and RE2 explore the same
// alternatives in the same order. JS never starts or ends a match or a
// capture between the two units of a pair, nor ends a wide run there, so
// every JS match boundary is a code-point boundary and JS and RE2 return the
// same matches and groups. A match cannot start on the second unit:
// whatever precedes the run there matches the empty string with only ^ or
// \b, which fail between the units, or through nullable non-wide terms, in
// which case the same match also starts one unit earlier and wins as
// leftmost. Anything else that touches a wide class (a single wide atom, a
// bounded repetition, a lazy run at the end of the pattern, two runs back to
// back) panics.
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
	root := &node{kind: nGroup, alts: p.alternation()}
	if p.more() {
		p.fail("unbalanced )")
	}
	if root.nullable() {
		p.fail("pattern can match the empty string (matchAll's empty-match stepping differs from RE2)")
	}
	p.check(root, nil, false)
	return &Regexp{source: source, re: regexp.MustCompile(root.emitAlts())}
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

type nodeKind int

const (
	nLit    nodeKind = iota // one character, emitted as is
	nSet                    // a class, after case folding and negation
	nAssert                 // ^ $ \b
	nGroup                  // (...) or (?:...)
	nRepeat                 // sub quantified
)

type node struct {
	kind     nodeKind
	lit      rune
	set      set
	wide     bool // nSet: matches the surrogates
	assert   string
	capture  bool
	alts     [][]*node
	sub      *node
	min, max int // max -1 = unbounded
	lazy     bool
}

func (n *node) nullable() bool {
	switch n.kind {
	case nAssert:
		return true
	case nGroup:
		for _, alt := range n.alts {
			if seqNullable(alt) {
				return true
			}
		}
		return false
	case nRepeat:
		return n.min == 0 || n.sub.nullable()
	}
	return false
}

func seqNullable(seq []*node) bool {
	for _, n := range seq {
		if !n.nullable() {
			return false
		}
	}
	return true
}

// hasWide reports whether n contains a wide class.
func (n *node) hasWide() bool {
	return n.any(func(m *node) bool { return m.kind == nSet && m.wide })
}

func (n *node) hasCapture() bool {
	return n.any(func(m *node) bool { return m.kind == nGroup && m.capture })
}

func (n *node) any(f func(*node) bool) bool {
	if f(n) {
		return true
	}
	if n.sub != nil && n.sub.any(f) {
		return true
	}
	for _, alt := range n.alts {
		for _, m := range alt {
			if m.any(f) {
				return true
			}
		}
	}
	return false
}

// wideRun reports whether n is one of the two shapes in which a wide class
// is translated:
//
//  1. W* or W+ (greedy or lazy) for a wide class W. A JS run of W over a
//     stretch of text and an RE2 run over the same stretch cover the same
//     extent, since W takes both units of a pair (and RE2 takes the
//     character) and all three U+FFFD bytes of a lone surrogate. It can
//     start where RE2 can: from the first unit of a pair, W* reaches
//     whatever it reaches from the second.
//  2. (?:A1|...|An)* or + over alternatives that are each a single class or
//     non-nullable non-wide atoms ending in one wide class, with at least one
//     alternative a single wide class C, and no capture. It is case 1 with
//     some runs broken up by the non-wide prefixes: when JS ends an
//     iteration on the first unit of a pair (the trailing wide class takes
//     it), the next iteration takes the second unit through C, while RE2
//     takes the whole character in the trailing class. The follow check
//     keeps JS from stopping in between. This is `(?:\\.|[^"\\\r\n])*` in
//     the quoted-value alternatives of redaction.ts:150-151.
func (n *node) wideRun() bool {
	if n.kind != nRepeat || n.min > 1 || n.max != -1 {
		return false
	}
	if n.sub.kind == nSet {
		return n.sub.wide
	}
	g := n.sub
	if g.kind != nGroup || g.capture {
		return false
	}
	continuation := false
	for _, alt := range g.alts {
		last := len(alt) - 1
		if last < 0 || alt[last].kind != nSet {
			return false
		}
		for _, m := range alt[:last] {
			if m.nullable() || m.hasWide() || m.hasCapture() {
				return false
			}
		}
		if last == 0 && alt[0].wide {
			continuation = true
		}
	}
	return continuation
}

func (p *parser) check(n *node, follow [][]*node, repeated bool) {
	switch n.kind {
	case nSet:
		if n.wide {
			p.fail("a class that matches surrogates outside W*, W+ or an escaped-run group")
		}
	case nGroup:
		for _, alt := range n.alts {
			for i, m := range alt {
				p.check(m, append([][]*node{alt[i+1:]}, follow...), repeated)
			}
		}
	case nRepeat:
		if n.sub.nullable() {
			p.fail("quantifier over something that can match the empty string (JS refuses an empty iteration)")
		}
		if n.hasCapture() {
			p.fail("capture under a quantifier (JS clears it at each iteration and refuses an empty one)")
		}
		if n.wideRun() {
			if repeated {
				p.fail("a run of a class that matches surrogates inside a repetition")
			}
			p.checkFollow(follow, n.lazy)
			return
		}
		p.check(n.sub, follow, repeated || n.max != 1)
	}
}

// checkFollow requires that what may come after a wide run cannot consume a
// surrogate unit or hold between the two units of a pair, so JS has no
// reason to end the run inside a code point. A lazy run must be followed
// by something that consumes or by $: at the end of the pattern it would
// stop after one unit.
func (p *parser) checkFollow(follow [][]*node, lazy bool) {
	for _, seq := range follow {
		for _, m := range seq {
			switch {
			case m.kind == nAssert && (m.assert == "$" || m.assert == `\b`):
				return // only at the end, or between a word character and a non-word one
			case m.kind == nAssert && m.assert == "^":
				continue
			case m.hasWide():
				p.fail("a run of a class that matches surrogates may be followed by one")
			case !m.nullable():
				return
			}
		}
	}
	if lazy {
		p.fail("a lazy run of a class that matches surrogates at the end of the pattern")
	}
}

func (n *node) emitAlts() string {
	parts := make([]string, len(n.alts))
	for i, alt := range n.alts {
		var b strings.Builder
		for _, m := range alt {
			b.WriteString(m.emit())
		}
		parts[i] = b.String()
	}
	return strings.Join(parts, "|")
}

func (n *node) emit() string {
	switch n.kind {
	case nLit:
		return regexp.QuoteMeta(string(n.lit))
	case nSet:
		var b strings.Builder
		b.WriteByte('[')
		// Surrogates never reach RE2 as code points; leave them out.
		for _, r := range complement(complement(n.set).union(set{{0xD800, 0xDFFF}})) {
			fmt.Fprintf(&b, `\x{%x}`, r.lo)
			if r.hi != r.lo {
				fmt.Fprintf(&b, `-\x{%x}`, r.hi)
			}
		}
		b.WriteByte(']')
		return b.String()
	case nAssert:
		return n.assert
	case nGroup:
		if n.capture {
			return "(" + n.emitAlts() + ")"
		}
		return "(?:" + n.emitAlts() + ")"
	}
	var q string
	switch {
	case n.min == 0 && n.max == -1:
		q = "*"
	case n.min == 1 && n.max == -1:
		q = "+"
	case n.min == 0 && n.max == 1:
		q = "?"
	case n.max == -1:
		q = "{" + strconv.Itoa(n.min) + ",}"
	case n.min == n.max:
		q = "{" + strconv.Itoa(n.min) + "}"
	default:
		q = "{" + strconv.Itoa(n.min) + "," + strconv.Itoa(n.max) + "}"
	}
	if n.lazy {
		q += "?"
	}
	return "(?:" + n.sub.emit() + ")" + q
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

func (p *parser) alternation() [][]*node {
	var alts [][]*node
	for {
		var seq []*node
		for p.more() && p.peek() != '|' && p.peek() != ')' {
			seq = append(seq, p.quantified())
		}
		alts = append(alts, seq)
		if !p.more() || p.peek() != '|' {
			return alts
		}
		p.pos++
	}
}

func (p *parser) quantified() *node {
	n := p.atom()
	if !p.more() {
		return n
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
		return n
	}
	if n.kind == nAssert {
		p.fail("quantified assertion")
	}
	lazy := p.more() && p.peek() == '?'
	if lazy {
		p.pos++
	}
	return &node{kind: nRepeat, sub: n, min: min, max: max, lazy: lazy}
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

func (p *parser) atom() *node {
	c := p.peek()
	switch c {
	case '^', '$':
		p.pos++
		return &node{kind: nAssert, assert: string(c)}
	case '.':
		p.pos++
		return p.class(complement(set{{'\n', '\n'}, {'\r', '\r'}, {0x2028, 0x2029}}))
	case '[':
		return p.bracket()
	case '(':
		return p.group()
	case '\\':
		p.pos++
		if !p.more() {
			p.fail("trailing backslash")
		}
		e := p.peek()
		p.pos++
		switch e {
		case 'b':
			return &node{kind: nAssert, assert: `\b`}
		case 'B':
			p.pos--
			p.fail(`\B is not translated (it holds inside a surrogate pair)`)
		}
		if s, ok := classEscape(e); ok {
			return p.class(s)
		}
		return p.literal(p.escapedChar(e))
	case '*', '+', '?', '{', '}', ']', ')', '|':
		p.fail("unexpected " + string(c))
	}
	p.pos++
	return p.literal(rune(c))
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

func (p *parser) literal(c rune) *node {
	if p.fold && isLetter(c) {
		return p.class(set{{c, c}})
	}
	return &node{kind: nLit, lit: c}
}

func (p *parser) group() *node {
	p.pos++
	capture := true
	if strings.HasPrefix(p.src[p.pos:], "?:") {
		capture = false
		p.pos += 2
	} else if p.more() && p.peek() == '?' {
		p.fail("lookaround and named groups are not translated")
	}
	alts := p.alternation()
	if !p.more() || p.peek() != ')' {
		p.fail("missing )")
	}
	p.pos++
	return &node{kind: nGroup, capture: capture, alts: alts}
}

func (p *parser) bracket() *node {
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
		return p.class(complement(p.folded(s)))
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

func (p *parser) class(s set) *node {
	s = normalize(p.folded(s))
	// Lone surrogates arrive as U+FFFD bytes, astral characters as one
	// code point; both must stand or fall with the surrogate units.
	surrogates := coverage(s, 0xD800, 0xDFFF)
	astral := coverage(s, 0x10000, 0x10FFFF)
	replacement := coverage(s, 0xFFFD, 0xFFFD)
	if surrogates == partial || astral == partial || surrogates != astral || surrogates != replacement {
		p.fail("class treats surrogates, U+FFFD and astral characters differently")
	}
	if len(complement(complement(s).union(set{{0xD800, 0xDFFF}}))) == 0 {
		p.fail("empty class")
	}
	return &node{kind: nSet, set: s, wide: surrogates == all}
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
