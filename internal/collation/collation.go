// Package collation compares JS strings the way Node 22's
// String.prototype.localeCompare does under the en-US locale: ICU's CLDR
// root collation (ICU 78, Unicode 17) at tertiary strength, alternate
// non-ignorable, case-first off, and normalization on the way ICU does it:
// only text that fails the FCD check is decomposed.
//
// root.bin is generated from ICU's FractionalUCA.txt by gen/main.go. The
// runtime follows ICU's comparison: collation elements come from the table
// (longest contraction, discontiguous contractions over unblocked
// non-starters, prefix contexts, algorithmic Hangul), Han characters get
// radical-stroke primaries, and every other code point, including lone
// surrogates, gets an implicit primary in code point order that sorts after
// Han and before U+FFFD. Weights are then compared level by level, ignoring
// zero weights and the tertiary case bits.
package collation

import (
	_ "embed"
	"encoding/binary"
	"slices"
	"sync"
	"unicode/utf8"

	"github.com/Mizore66/faultline/internal/jsstr"
)

//go:embed root.bin
var rootData []byte

type ce struct {
	p    uint32
	s, t uint16
}

type prefixed struct {
	prefix []rune
	ces    []ce
}

type table struct {
	ccc         map[rune]uint8
	decomp      map[rune][]rune // full canonical decompositions
	singleRunes []rune          // sorted; singleCEs[singleAt[i]:singleAt[i+1]]
	singleAt    []uint32
	singleCEs   []ce
	multi       map[string][]ce // contraction keys (runes as string(runes))
	multiPrefix map[string]bool // proper prefixes of contraction keys
	maxKey      int
	prefixed    map[rune][]prefixed
	hanRunes    []rune // sorted
	hanRank     []uint32
	unsafe      map[rune]bool // unsafe-backward code points
	unsafeLead  [0x400]bool   // lead surrogates of unsafe supplementary code points
}

var (
	loadOnce sync.Once
	tab      *table
)

func load() *table {
	loadOnce.Do(func() {
		b := rootData
		if len(b) < 4 || string(b[:3]) != "UCA" || b[3] != 3 {
			panic("collation: bad root.bin")
		}
		b = b[4:]
		uv := func() uint64 {
			v, n := binary.Uvarint(b)
			if n <= 0 {
				panic("collation: truncated root.bin")
			}
			b = b[n:]
			return v
		}
		t := &table{
			ccc:         map[rune]uint8{},
			decomp:      map[rune][]rune{},
			multi:       map[string][]ce{},
			multiPrefix: map[string]bool{},
			prefixed:    map[rune][]prefixed{},
		}
		prev := rune(0)
		for n := uv(); n > 0; n-- {
			prev += rune(uv())
			t.ccc[prev] = b[0]
			b = b[1:]
		}
		prev = 0
		for n := uv(); n > 0; n-- {
			prev += rune(uv())
			d := make([]rune, uv())
			for i := range d {
				d[i] = rune(uv())
			}
			t.decomp[prev] = d
		}
		prev = 0
		n := uv()
		t.hanRunes = make([]rune, n)
		t.hanRank = make([]uint32, n)
		for i := range t.hanRunes {
			prev += rune(uv())
			t.hanRunes[i] = prev
			t.hanRank[i] = uint32(uv())
		}
		readCEs := func(dst []ce) []ce {
			for n := uv(); n > 0; n-- {
				dst = append(dst, ce{p: uint32(uv()), s: uint16(uv()), t: uint16(uv())})
			}
			return dst
		}
		prev = 0
		n = uv()
		t.singleRunes = make([]rune, n)
		t.singleAt = make([]uint32, n+1)
		t.singleCEs = make([]ce, 0, n+n/8)
		for i := range t.singleRunes {
			prev += rune(uv())
			t.singleRunes[i] = prev
			t.singleCEs = readCEs(t.singleCEs)
			t.singleAt[i+1] = uint32(len(t.singleCEs))
		}
		for n := uv(); n > 0; n-- {
			prefix := make([]rune, uv())
			for i := range prefix {
				prefix[i] = rune(uv())
			}
			key := make([]rune, uv())
			for i := range key {
				key[i] = rune(uv())
			}
			ces := readCEs(nil)
			if len(prefix) > 0 {
				t.prefixed[key[0]] = append(t.prefixed[key[0]], prefixed{prefix, ces})
				continue
			}
			t.multi[string(key)] = ces
			for i := 1; i < len(key); i++ {
				t.multiPrefix[string(key[:i])] = true
			}
			t.maxKey = max(t.maxKey, len(key))
		}
		t.unsafe = map[rune]bool{}
		for c := range t.ccc {
			t.unsafe[c] = true
		}
		for c, d := range t.decomp {
			if t.ccc[d[0]] != 0 {
				t.unsafe[c] = true
			}
		}
		for key := range t.multi {
			for i, c := range []rune(key) {
				if i > 0 {
					t.unsafe[c] = true
				}
			}
		}
		for c := range t.unsafe {
			if c > 0xFFFF {
				t.unsafeLead[(c-0x10000)>>10] = true
			}
		}
		tab = t
	})
	return tab
}

// codePoints splits a WTF-8 JS string into code points; lone surrogates stay
// as surrogate code points, as ICU's UTF-16 iterator sees them.
func codePoints(s string) []rune {
	units := jsstr.ToUTF16(s)
	out := make([]rune, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			out = append(out, 0x10000+(u-0xD800)<<10+(rune(units[i+1])-0xDC00))
			i++
			continue
		}
		out = append(out, u)
	}
	return out
}

const (
	hangulBase  = 0xAC00
	hangulCount = 11172
	jamoLBase   = 0x1100
	jamoVBase   = 0x1161
	jamoTBase   = 0x11A7
	jamoVCount  = 21
	jamoTCount  = 28
)

// nfd is canonical decomposition plus canonical ordering (Unicode 17). V8
// turns on ICU's normalization mode for localeCompare, so collation sees the
// NFD form; lone surrogates pass through unchanged.
func (t *table) nfd(cps []rune) []rune {
	out := make([]rune, 0, len(cps))
	for _, c := range expandHangul(cps) {
		if d, ok := t.decomp[c]; ok {
			out = append(out, expandHangul(d)...)
		} else {
			out = append(out, c)
		}
	}
	// Canonical ordering: stable-sort each run of non-starters by class.
	for i := 0; i < len(out); {
		if t.ccc[out[i]] == 0 {
			i++
			continue
		}
		j := i
		for j < len(out) && t.ccc[out[j]] != 0 {
			j++
		}
		// Stable by combining class (O(n log n): a run can be very long).
		slices.SortStableFunc(out[i:j], func(a, b rune) int { return int(t.ccc[a]) - int(t.ccc[b]) })
		i = j
	}
	return out
}

// expandHangul decomposes precomposed Hangul syllables into conjoining jamo.
func expandHangul(cps []rune) []rune {
	var out []rune
	for i, c := range cps {
		if c < hangulBase || c >= hangulBase+hangulCount {
			if out != nil {
				out = append(out, c)
			}
			continue
		}
		if out == nil {
			out = append(make([]rune, 0, len(cps)+8), cps[:i]...)
		}
		idx := c - hangulBase
		out = append(out, jamoLBase+idx/(jamoVCount*jamoTCount), jamoVBase+(idx%(jamoVCount*jamoTCount))/jamoTCount)
		if t := idx % jamoTCount; t != 0 {
			out = append(out, jamoTBase+t)
		}
	}
	if out == nil {
		return cps
	}
	return out
}

// hanPrimaryBase puts Han above FDD1 5B57 ("HAN first primary", 81 02 02),
// which ICU keeps below every Han primary.
const hanPrimaryBase = 0x81030000

func (t *table) implicit(c rune) []ce {
	if i, ok := slices.BinarySearch(t.hanRunes, c); ok {
		return []ce{{p: hanPrimaryBase + t.hanRank[i], s: 0x0500, t: 0x0500}}
	}
	// Unassigned, private-use and surrogate code points: implicit primaries
	// in code point order after Han, before the trailing U+FFFD block.
	return []ce{{p: 0xE4000000 | uint32(c+1), s: 0x0500, t: 0x0500}}
}

func (t *table) lookupSingle(c rune) []ce {
	if i, ok := slices.BinarySearch(t.singleRunes, c); ok {
		return t.singleCEs[t.singleAt[i]:t.singleAt[i+1]]
	}
	if c >= hangulBase && c < hangulBase+hangulCount {
		// ICU's HANGUL_TAG: the syllable's jamo, each with its own CEs (the
		// root data has no jamo contractions).
		var out []ce
		for _, j := range expandHangul([]rune{c}) {
			out = append(out, t.lookupSingle(j)...)
		}
		return out
	}
	return t.implicit(c)
}

// fcd16 is ICU's (lccc << 8 | tccc): the combining classes of the first and
// last code points of c's canonical decomposition.
func (t *table) fcd16(c rune) (lccc, tccc uint8) {
	if d, ok := t.decomp[c]; ok {
		return t.ccc[d[0]], t.ccc[d[len(d)-1]]
	}
	cc := t.ccc[c]
	return cc, cc
}

// isTibetanCompositeVowel is CollationFCD::isFCD16OfTibetanCompositeVowel:
// U+0F73, U+0F75 and U+0F81 always need normalization.
func isTibetanCompositeVowel(c rune) bool { return c == 0x0F73 || c == 0x0F75 || c == 0x0F81 }

// fcd is the text ICU's FCDUTF16CollationIterator collates: segments that
// pass the FCD check stay as they are (precomposed characters, Hangul
// syllables, contraction keys matched literally); a failing segment, up to
// the next character with lccc 0, is replaced by its NFD.
func (t *table) fcd(cps []rune) []rune {
	out := make([]rune, 0, len(cps))
	for pos := 0; pos < len(cps); {
		i := pos
		var prevCC uint8
		fail := false
		for i < len(cps) {
			lead, trail := t.fcd16(cps[i])
			if lead == 0 && i != pos {
				break // FCD boundary before cps[i]
			}
			if lead != 0 && (prevCC > lead || isTibetanCompositeVowel(cps[i])) {
				fail = true
				break
			}
			prevCC = trail
			i++
			if prevCC == 0 {
				break // FCD boundary after the last character
			}
		}
		if !fail {
			out = append(out, cps[pos:i]...)
			pos = i
			continue
		}
		q := i + 1
		for q < len(cps) {
			if lead, _ := t.fcd16(cps[q]); lead == 0 {
				break
			}
			q++
		}
		out = append(out, t.nfd(cps[pos:q])...)
		pos = q
	}
	return out
}

// elements returns the collation elements of s.
func (t *table) elements(s string) []ce { return t.elementsFrom(codePoints(s), 0) }

// elementsFrom returns the collation elements of raw[start:], the way ICU's
// FCD iterator produces them when it starts at start: the FCD check begins
// there, contractions start at or after it, and prefix contexts may still
// look at the text before it.
func (t *table) elementsFrom(raw []rune, start int) []ce {
	cps := append(append(make([]rune, 0, len(raw)), raw[:start]...), t.fcd(raw[start:])...)
	var out []ce
	used := make([]bool, len(cps))
	for i := start; i < len(cps); i++ {
		if used[i] {
			continue
		}
		c := cps[i]
		// Prefix (context-before) mappings, e.g. L | U+00B7.
		if ps, ok := t.prefixed[c]; ok && i > 0 {
			matched := false
			for _, p := range ps {
				if len(p.prefix) <= i && runesEqual(cps[i-len(p.prefix):i], p.prefix) {
					out = append(out, p.ces...)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		// Longest contiguous contraction starting at i.
		key := []rune{c}
		ces := t.lookupSingle(c)
		end := i + 1
		if t.maxKey > 0 {
			stop := i + 1 // the first code point the contiguous scan did not take
			for j := i + 1; j < len(cps) && j-i < t.maxKey; j++ {
				stop = j
				if used[j] {
					break
				}
				cand := string(cps[i : j+1])
				if m, ok := t.multi[cand]; ok {
					key = append(key[:0:0], cps[i:j+1]...)
					ces, end = m, j+1
					stop = j + 1
				} else if !t.multiPrefix[cand] {
					break
				}
			}
			// Discontiguous contractions (UCA S2.1.1): only right after a
			// match (ICU's sinceMatch == 1), over non-starters that are not
			// blocked: the tccc of the last skipped character must be below
			// the next one's lccc.
			if stop == end && end < len(cps) && t.multiPrefix[string(key)] {
				var prevCC uint8
				for j := end; j < len(cps); j++ {
					if used[j] {
						continue
					}
					lead, trail := t.fcd16(cps[j])
					if lead == 0 {
						break
					}
					if prevCC < lead {
						cand := append(append([]rune(nil), key...), cps[j])
						if m, ok := t.multi[string(cand)]; ok {
							key, ces = cand, m
							used[j] = true
							continue
						}
					}
					prevCC = trail
				}
			}
		}
		for k := i; k < end; k++ {
			used[k] = true
		}
		out = append(out, ces...)
		i = end - 1
	}
	return out
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Key holds the weights of one string, level by level, zeros removed.
type Key struct {
	primary             []uint32
	secondary, tertiary []uint16
}

// onlyTertiaryMask drops the case bits stored in the top two bits of each
// tertiary byte (ICU Collation::ONLY_TERTIARY_MASK).
const onlyTertiaryMask = 0x3F3F

// MakeKey computes the comparison key of a JS string.
func MakeKey(s string) Key { return makeKey(load().elements(s)) }

func makeKey(ces []ce) Key {
	var k Key
	for _, e := range ces {
		if e.p != 0 {
			k.primary = append(k.primary, e.p)
		}
		if e.s != 0 {
			k.secondary = append(k.secondary, e.s)
		}
		if t := e.t & onlyTertiaryMask; t != 0 {
			k.tertiary = append(k.tertiary, t)
		}
	}
	return k
}

func compareLevel[T uint16 | uint32](a, b []T) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// CompareKeys compares two keys: -1, 0 or 1.
func CompareKeys(a, b Key) int {
	if c := compareLevel(a.primary, b.primary); c != 0 {
		return c
	}
	if c := compareLevel(a.secondary, b.secondary); c != 0 {
		return c
	}
	return compareLevel(a.tertiary, b.tertiary)
}

// Compare is a.localeCompare(b) (sign only): ICU's
// RuleBasedCollator::doCompare. It skips the identical prefix (in UTF-16
// units), backs up while the code unit there is unsafe-backward (it could be
// inside a contraction or a combining sequence), and collates both strings
// from that point. A contraction that starts inside the skipped prefix is
// therefore never used, so Compare is not always the same as comparing
// MakeKey results (and is not always transitive, as in ICU).
func Compare(a, b string) int {
	if a == b {
		return 0
	}
	t := load()
	ua, ub := jsstr.ToUTF16(a), jsstr.ToUTF16(b)
	eq := 0
	for eq < len(ua) && eq < len(ub) && ua[eq] == ub[eq] {
		eq++
	}
	if eq > 0 && ((eq != len(ua) && t.unsafeBackward(ua[eq])) || (eq != len(ub) && t.unsafeBackward(ub[eq]))) {
		for {
			eq--
			if eq <= 0 || !t.unsafeBackward(ua[eq]) {
				break
			}
		}
	}
	ra, sa := codePointsFrom(ua, eq)
	rb, sb := codePointsFrom(ub, eq)
	return CompareKeys(makeKey(t.elementsFrom(ra, sa)), makeKey(t.elementsFrom(rb, sb)))
}

// codePointsFrom splits UTF-16 units into code points (lone surrogates kept)
// and returns the index of the code point that starts at unit offset.
func codePointsFrom(units []uint16, offset int) ([]rune, int) {
	out := make([]rune, 0, len(units))
	start := -1
	for i := 0; i < len(units); i++ {
		if i == offset {
			start = len(out)
		}
		u := rune(units[i])
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			out = append(out, 0x10000+(u-0xD800)<<10+(rune(units[i+1])-0xDC00))
			i++
			continue
		}
		out = append(out, u)
	}
	if start < 0 {
		start = len(out)
	}
	return out, start
}

// unsafeBackward is CollationData::isUnsafeBackward for a UTF-16 unit: code
// points with lccc != 0, every non-initial code point of a contraction, all
// trail surrogates, and a lead surrogate when any supplementary code point
// behind it is unsafe.
func (t *table) unsafeBackward(u uint16) bool {
	switch {
	case u >= 0xDC00 && u <= 0xDFFF:
		return true
	case u >= 0xD800 && u <= 0xDBFF:
		return t.unsafeLead[u-0xD800]
	}
	return t.unsafe[rune(u)]
}

// Prepared is a string with its key, for sorting many strings.
type Prepared struct {
	S      string
	key    Key
	simple bool
}

// Prepare computes s's key. A string is simple when no code point in it
// starts or continues a contraction, has a prefix context, is
// unsafe-backward or is a surrogate: then ICU's identical-prefix skip cannot
// change the result, and comparing keys equals Compare.
func Prepare(s string) Prepared {
	t := load()
	simple := true
	for _, c := range s {
		if c == utf8.RuneError || c >= 0xD800 && c <= 0xDFFF || t.unsafe[c] || t.multiPrefix[string(c)] || t.prefixed[c] != nil {
			simple = false
			break
		}
	}
	if !utf8.ValidString(s) { // WTF-8 lone surrogates
		simple = false
	}
	return Prepared{S: s, key: MakeKey(s), simple: simple}
}

// ComparePrepared is Compare(a.S, b.S).
func ComparePrepared(a, b Prepared) int {
	if a.simple && b.simple {
		return CompareKeys(a.key, b.key)
	}
	return Compare(a.S, b.S)
}

// Rules lists what the root data does beyond single code points, for tests
// that build strings around it: contraction keys, prefix contexts
// (prefix, key) and the non-starters (lccc != 0).
func Rules() (contractions [][]rune, prefixes [][2][]rune, nonStarters []rune) {
	t := load()
	for key := range t.multi {
		contractions = append(contractions, []rune(key))
	}
	for c, ps := range t.prefixed {
		for _, p := range ps {
			prefixes = append(prefixes, [2][]rune{p.prefix, {c}})
		}
	}
	for c := range t.unsafe {
		if lead, _ := t.fcd16(c); lead != 0 {
			nonStarters = append(nonStarters, c)
		}
	}
	slices.SortFunc(contractions, slices.Compare)
	slices.SortFunc(prefixes, func(a, b [2][]rune) int {
		if c := slices.Compare(a[1], b[1]); c != 0 {
			return c
		}
		return slices.Compare(a[0], b[0])
	})
	slices.Sort(nonStarters)
	return contractions, prefixes, nonStarters
}
