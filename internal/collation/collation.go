// Package collation compares JS strings the way Node 22's
// String.prototype.localeCompare does under the en-US locale: ICU's CLDR
// root collation (ICU 78, Unicode 17) at tertiary strength, alternate
// non-ignorable, case-first off, normalization off.
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
		run := out[i:j]
		for a := 1; a < len(run); a++ {
			for b := a; b > 0 && t.ccc[run[b-1]] > t.ccc[run[b]]; b-- {
				run[b-1], run[b] = run[b], run[b-1]
			}
		}
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

func (t *table) implicit(c rune) []ce {
	if i, ok := slices.BinarySearch(t.hanRunes, c); ok {
		return []ce{{p: 0x81000000 | t.hanRank[i], s: 0x0500, t: 0x0500}}
	}
	// Unassigned, private-use and surrogate code points: implicit primaries
	// in code point order after Han, before the trailing U+FFFD block.
	return []ce{{p: 0xE4000000 | uint32(c+1), s: 0x0500, t: 0x0500}}
}

func (t *table) lookupSingle(c rune) []ce {
	if i, ok := slices.BinarySearch(t.singleRunes, c); ok {
		return t.singleCEs[t.singleAt[i]:t.singleAt[i+1]]
	}
	return t.implicit(c)
}

// elements returns the collation elements of s.
func (t *table) elements(s string) []ce {
	cps := t.nfd(codePoints(s))
	var out []ce
	used := make([]bool, len(cps))
	for i := 0; i < len(cps); i++ {
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
			for j := i + 1; j < len(cps) && j-i < t.maxKey; j++ {
				if used[j] {
					break
				}
				cand := string(cps[i : j+1])
				if m, ok := t.multi[cand]; ok {
					key = append(key[:0:0], cps[i:j+1]...)
					ces, end = m, j+1
				} else if !t.multiPrefix[cand] {
					break
				}
			}
			// Discontiguous contractions: extend with unblocked non-starters.
			if t.multiPrefix[string(key)] {
				maxSkipped := uint8(0)
				for j := end; j < len(cps); j++ {
					if used[j] {
						continue
					}
					cc := t.ccc[cps[j]]
					if cc == 0 {
						break
					}
					if maxSkipped < cc {
						cand := append(append([]rune(nil), key...), cps[j])
						if m, ok := t.multi[string(cand)]; ok {
							key, ces = cand, m
							used[j] = true
							continue
						}
					}
					maxSkipped = max(maxSkipped, cc)
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
func MakeKey(s string) Key {
	var k Key
	for _, e := range load().elements(s) {
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

// Compare is a.localeCompare(b) (sign only).
func Compare(a, b string) int {
	if a == b {
		return 0
	}
	return CompareKeys(MakeKey(a), MakeKey(b))
}
