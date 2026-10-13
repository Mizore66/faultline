// Package jscase is JavaScript's String.prototype.toLowerCase and
// toLocaleLowerCase("en-US") (Node 22, ICU's root case mapping) on WTF-8
// strings. Full mappings come from ICU (tables.go); the one context rule
// the root locale applies is Final_Sigma, evaluated as ICU does: when
// scanning away from Σ, Case_Ignorable is tested before Cased, so a
// character that is both (a modifier letter such as ʰ) is skipped.
package jscase

import (
	"sort"
	"strings"
	"unicode/utf8"
)

func inRanges(ranges [][2]rune, c rune) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i][1] >= c })
	return i < len(ranges) && ranges[i][0] <= c
}

func isCased(c rune) bool     { return inRanges(casedRanges, c) }
func isIgnorable(c rune) bool { return inRanges(ignorableRanges, c) }

// cp is one code point of a WTF-8 string: a lone surrogate is its
// three-byte sequence and never maps.
type cp struct {
	r    rune
	text string
}

func split(s string) []cp {
	var out []cp
	for i := 0; i < len(s); {
		if s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF {
			out = append(out, cp{-1, s[i : i+3]}) // lone surrogate: uncased, not ignorable
			i += 3
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		out = append(out, cp{r, s[i : i+n]})
		i += n
	}
	return out
}

// casedNeighbour is ICU's isFollowedByCasedLetter in one direction: skip
// Case_Ignorable, then report whether the next code point is Cased.
func casedNeighbour(cps []cp, i, dir int) bool {
	for j := i + dir; j >= 0 && j < len(cps); j += dir {
		c := cps[j].r
		switch {
		case c >= 0 && isIgnorable(c):
			continue
		case c >= 0 && isCased(c):
			return true
		default:
			return false
		}
	}
	return false
}

// Lower is s.toLowerCase(), which for the en-US locale is also
// s.toLocaleLowerCase("en-US").
func Lower(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	cps := split(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, c := range cps {
		if c.r == 0x03A3 && casedNeighbour(cps, i, -1) && !casedNeighbour(cps, i, 1) {
			b.WriteRune(0x03C2) // final sigma
			continue
		}
		if m, ok := lowerMap[c.r]; ok {
			for _, r := range m {
				b.WriteRune(r)
			}
			continue
		}
		b.WriteString(c.text)
	}
	return b.String()
}
