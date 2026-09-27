package collation

// This file ports ICU 78's forward collation iteration over UTF-16 text with
// normalization on, as RuleBasedCollator::doCompare runs it:
// FCDUTF16CollationIterator (utf16collationiterator.cpp), the parts of
// CollationIterator that the root data reaches (collationiterator.cpp:
// contractions, discontiguous contractions with SkippedState, prefixes), and
// CollationCompare::compareUpToQuaternary for tertiary strength without
// variable weighting. The structure follows ICU line by line, because ICU's
// results depend on it: the FCD check is incremental, contraction look-ahead
// can switch the iterator back from a normalized segment to raw text, and
// the backward FCD check skips the boundary after a supplementary character
// (hasTccc(trail) is false), so some mark runs are collated un-normalized.

// noCE is Collation::NO_CE: primary 1, secondary and tertiary 0x0100. It
// ends a string's CEs and terminates each level's comparison.
var noCE = ce{p: 1, s: 0x0100, t: 0x0100}

const noCEWeight16 = 0x0100

// trieResult is UStringTrieResult.
type trieResult uint8

const (
	noMatch trieResult = iota
	noValue
	finalValue
	intermediateValue
)

func hasValue(r trieResult) bool { return r >= finalValue }
func hasNext(r trieResult) bool  { return r == noValue || r == intermediateValue }

// suffixTrie stands in for the UCharsTrie of a contraction starter's
// suffixes: its state is the key matched so far.
type suffixTrie struct {
	t       *table
	key     []rune // starter followed by the matched suffix code points
	stopped bool
}

func (s *suffixTrie) reset() { s.key, s.stopped = s.key[:1], false }

func (s *suffixTrie) next(c rune) trieResult {
	if s.stopped {
		return noMatch
	}
	s.key = append(s.key, c)
	k := string(s.key)
	_, value := s.t.multi[k]
	more := s.t.multiPrefix[k]
	switch {
	case value && more:
		return intermediateValue
	case value:
		return finalValue
	case more:
		return noValue
	}
	s.stopped = true
	return noMatch
}

func (s *suffixTrie) first(c rune) trieResult {
	s.reset()
	return s.next(c)
}

func (s *suffixTrie) value() []ce { return s.t.multi[string(s.key)] }

type trieState struct {
	key     []rune
	stopped bool
}

func (s *suffixTrie) save() trieState { return trieState{append([]rune(nil), s.key...), s.stopped} }
func (s *suffixTrie) restore(st trieState) {
	s.key = append(s.key[:0], st.key...)
	s.stopped = st.stopped
}

// skippedState is CollationIterator's SkippedState: combining marks skipped
// in discontiguous contraction matching.
type skippedState struct {
	oldBuffer, newBuffer []rune
	pos                  int
	skipLengthAtMatch    int
	state                trieState
}

func (s *skippedState) clear()        { s.oldBuffer, s.pos = s.oldBuffer[:0], 0 }
func (s *skippedState) isEmpty() bool { return len(s.oldBuffer) == 0 }
func (s *skippedState) hasNext() bool { return s.pos < len(s.oldBuffer) }
func (s *skippedState) next() rune    { c := s.oldBuffer[s.pos]; s.pos++; return c }
func (s *skippedState) incBeyond()    { s.pos++ }

func (s *skippedState) backwardNumCodePoints(n int) int {
	length := len(s.oldBuffer)
	beyond := s.pos - length
	if beyond > 0 {
		if beyond >= n {
			s.pos -= n
			return n
		}
		s.pos = max(0, length-(n-beyond))
		return beyond
	}
	s.pos = max(0, s.pos-n)
	return 0
}

func (s *skippedState) setFirstSkipped(c rune) {
	s.skipLengthAtMatch = 0
	s.newBuffer = append(s.newBuffer[:0], c)
}
func (s *skippedState) skip(c rune)  { s.newBuffer = append(s.newBuffer, c) }
func (s *skippedState) recordMatch() { s.skipLengthAtMatch = len(s.newBuffer) }

func (s *skippedState) replaceMatch() {
	rest := s.oldBuffer[min(s.pos, len(s.oldBuffer)):]
	s.oldBuffer = append(append([]rune(nil), s.newBuffer[:s.skipLengthAtMatch]...), rest...)
	s.pos = 0
}

// iter is an FCDUTF16CollationIterator over one string, with the
// CollationIterator state it needs.
type iter struct {
	t *table

	raw        []uint16
	normalized []uint16
	norm       bool // start, pos and limit index normalized, not raw
	start      int
	pos        int
	limit      int
	// rawStart is always 0.
	segmentStart, segmentLimit, rawLimit int
	checkDir                             int

	ceBuffer []ce
	cesIndex int
	skipped  *skippedState
}

// newIter is FCDUTF16CollationIterator(data, numeric, s, p, lim) with the
// text s, p = s+offset and lim = s+len.
func (t *table) newIter(units []uint16, offset int) *iter {
	return &iter{t: t, raw: units, start: 0, pos: offset, limit: len(units),
		segmentStart: offset, rawLimit: len(units), checkDir: 1}
}

func (it *iter) unit(i int) uint16 {
	if it.norm {
		return it.normalized[i]
	}
	return it.raw[i]
}

func isLead(u uint16) bool  { return u >= 0xD800 && u <= 0xDBFF }
func isTrail(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

func supplementary(lead, trail uint16) rune {
	return 0x10000 + (rune(lead)-0xD800)<<10 + rune(trail) - 0xDC00
}

// hasLccc and hasTccc are the CollationFCD fast-path tests on one code unit:
// exact for BMP characters; a lead surrogate stands for its 1024
// supplementary code points; every trail surrogate has lccc and none has
// tccc.
func (t *table) hasLccc(u uint16) bool {
	switch {
	case isTrail(u):
		return true
	case isLead(u):
		return t.leadLccc[u-0xD800]
	}
	lead, _ := t.fcd16(rune(u))
	return lead != 0
}

func (t *table) hasTccc(u uint16) bool {
	switch {
	case isTrail(u):
		return false
	case isLead(u):
		return t.leadFCD[u-0xD800]
	}
	_, trail := t.fcd16(rune(u))
	return trail != 0
}

// mayHaveLccc is CollationFCD::mayHaveLccc for a code point.
func (t *table) mayHaveLccc(c rune) bool {
	if c < 0x300 {
		return false
	}
	if c > 0xFFFF {
		return t.leadLccc[(c-0x10000)>>10]
	}
	return t.hasLccc(uint16(c))
}

func maybeTibetanCompositeVowel(u uint16) bool { return rune(u)&0x1fff01 == 0xf01 }

// fcd16v is getFCD16 as one value, lccc<<8 | tccc.
func (t *table) fcd16v(c rune) uint16 {
	lead, trail := t.fcd16(c)
	return uint16(lead)<<8 | uint16(trail)
}

func isFCD16OfTibetanCompositeVowel(v uint16) bool { return v == 0x8182 || v == 0x8184 }

// nextFCD16 and previousFCD16 read one code point of the raw text.
func (it *iter) nextFCD16(p *int) uint16 {
	c := rune(it.raw[*p])
	*p++
	if isLead(uint16(c)) && *p != it.rawLimit && isTrail(it.raw[*p]) {
		c = supplementary(uint16(c), it.raw[*p])
		*p++
	}
	return it.t.fcd16v(c)
}

func (it *iter) previousFCD16(p *int) uint16 {
	*p--
	c := rune(it.raw[*p])
	if isTrail(uint16(c)) && *p != 0 && isLead(it.raw[*p-1]) {
		c = supplementary(it.raw[*p-1], uint16(c))
		*p--
	}
	return it.t.fcd16v(c)
}

func (it *iter) nextCodePoint() rune {
	var c uint16
	for {
		if it.checkDir > 0 {
			if it.pos == it.limit {
				return -1
			}
			c = it.unit(it.pos)
			it.pos++
			if it.t.hasTccc(c) {
				if maybeTibetanCompositeVowel(c) || (it.pos != it.limit && it.t.hasLccc(it.unit(it.pos))) {
					it.pos--
					it.nextSegment()
					c = it.unit(it.pos)
					it.pos++
				}
			}
			break
		} else if it.checkDir == 0 && it.pos != it.limit {
			c = it.unit(it.pos)
			it.pos++
			break
		} else {
			it.switchToForward()
		}
	}
	if isLead(c) && it.pos != it.limit && isTrail(it.unit(it.pos)) {
		t := it.unit(it.pos)
		it.pos++
		return supplementary(c, t)
	}
	return rune(c)
}

func (it *iter) previousCodePoint() rune {
	var c uint16
	for {
		if it.checkDir < 0 {
			if it.pos == it.start {
				return -1
			}
			it.pos--
			c = it.unit(it.pos)
			if it.t.hasLccc(c) {
				if maybeTibetanCompositeVowel(c) || (it.pos != it.start && it.t.hasTccc(it.unit(it.pos-1))) {
					it.pos++
					it.previousSegment()
					it.pos--
					c = it.unit(it.pos)
				}
			}
			break
		} else if it.checkDir == 0 && it.pos != it.start {
			it.pos--
			c = it.unit(it.pos)
			break
		} else {
			it.switchToBackward()
		}
	}
	if isTrail(c) && it.pos != it.start && isLead(it.unit(it.pos-1)) {
		it.pos--
		return supplementary(it.unit(it.pos), c)
	}
	return rune(c)
}

func (it *iter) forwardNumCodePoints(n int) {
	for n > 0 && it.nextCodePoint() >= 0 {
		n--
	}
}

func (it *iter) backwardNumCodePoints(n int) {
	for n > 0 && it.previousCodePoint() >= 0 {
		n--
	}
}

func (it *iter) switchToForward() {
	if it.checkDir < 0 {
		// Turn around from backward checking.
		it.start, it.segmentStart = it.pos, it.pos
		if it.pos == it.segmentLimit {
			it.limit = it.rawLimit
			it.checkDir = 1
		} else {
			it.checkDir = 0
		}
		return
	}
	// Reached the end of the FCD segment.
	if it.norm {
		// The segment was normalized: continue checking from its end.
		it.norm = false
		it.pos, it.start, it.segmentStart = it.segmentLimit, it.segmentLimit, it.segmentLimit
	}
	it.limit = it.rawLimit
	it.checkDir = 1
}

func (it *iter) nextSegment() {
	// The raw text [segmentStart, pos) passes the FCD check.
	p := it.pos
	var prevCC uint8
	for {
		q := p
		fcd := it.nextFCD16(&p)
		leadCC := uint8(fcd >> 8)
		if leadCC == 0 && q != it.pos {
			it.limit, it.segmentLimit = q, q
			break
		}
		if leadCC != 0 && (prevCC > leadCC || isFCD16OfTibetanCompositeVowel(fcd)) {
			// Fails the FCD check: normalize up to the next FCD boundary.
			for {
				q = p
				if p == it.rawLimit || it.nextFCD16(&p) <= 0xff {
					break
				}
			}
			it.normalize(it.pos, q)
			it.pos = it.start
			break
		}
		prevCC = uint8(fcd)
		if p == it.rawLimit || prevCC == 0 {
			it.limit, it.segmentLimit = p, p
			break
		}
	}
	it.checkDir = 0
}

func (it *iter) switchToBackward() {
	if it.checkDir > 0 {
		// Turn around from forward checking.
		it.limit, it.segmentLimit = it.pos, it.pos
		if it.pos == it.segmentStart {
			it.start = 0
			it.checkDir = -1
		} else {
			it.checkDir = 0
		}
		return
	}
	// Reached the start of the FCD segment.
	if it.norm {
		it.norm = false
		it.pos, it.limit, it.segmentLimit = it.segmentStart, it.segmentStart, it.segmentStart
	}
	it.start = 0
	it.checkDir = -1
}

func (it *iter) previousSegment() {
	// The raw text [pos, segmentLimit) passes the FCD check.
	p := it.pos
	var nextCC uint8
	for {
		q := p
		fcd := it.previousFCD16(&p)
		trailCC := uint8(fcd)
		if trailCC == 0 && q != it.pos {
			it.start, it.segmentStart = q, q
			break
		}
		if trailCC != 0 && ((nextCC != 0 && trailCC > nextCC) || isFCD16OfTibetanCompositeVowel(fcd)) {
			// Fails the FCD check: normalize back to the previous FCD boundary.
			for {
				q = p
				if !(fcd > 0xff && p != 0) {
					break
				}
				if fcd = it.previousFCD16(&p); fcd == 0 {
					break
				}
			}
			it.normalize(q, it.pos)
			it.pos = it.limit
			break
		}
		nextCC = uint8(fcd >> 8)
		if p == 0 || nextCC == 0 {
			it.start, it.segmentStart = p, p
			break
		}
	}
	it.checkDir = 0
}

// normalize replaces the raw text [from, to) by its NFD for collation.
func (it *iter) normalize(from, to int) {
	cps, _ := codePointsFrom(it.raw[from:to], 0)
	it.normalized = it.normalized[:0]
	for _, c := range it.t.nfd(cps) {
		if c > 0xFFFF {
			c -= 0x10000
			it.normalized = append(it.normalized, uint16(0xD800+c>>10), uint16(0xDC00+c&0x3FF))
		} else {
			it.normalized = append(it.normalized, uint16(c))
		}
	}
	it.segmentStart, it.segmentLimit = from, to
	it.norm = true
	it.start, it.limit = 0, len(it.normalized)
}

// nextSkippedCodePoint and backwardNumSkipped are CollationIterator's, with
// numCpFwd always -1 (forward iteration only).
func (it *iter) nextSkippedCodePoint() rune {
	if it.skipped != nil && it.skipped.hasNext() {
		return it.skipped.next()
	}
	c := it.nextCodePoint()
	if it.skipped != nil && !it.skipped.isEmpty() && c >= 0 {
		it.skipped.incBeyond()
	}
	return c
}

func (it *iter) backwardNumSkipped(n int) {
	if it.skipped != nil && !it.skipped.isEmpty() {
		n = it.skipped.backwardNumCodePoints(n)
	}
	it.backwardNumCodePoints(n)
}

// nextCE is CollationIterator::nextCE.
func (it *iter) nextCE() ce {
	if it.cesIndex < len(it.ceBuffer) {
		e := it.ceBuffer[it.cesIndex]
		it.cesIndex++
		return e
	}
	c := it.nextCodePoint()
	if c < 0 {
		it.ceBuffer = append(it.ceBuffer, noCE)
	} else {
		it.appendCEs(c, nil)
	}
	e := it.ceBuffer[it.cesIndex]
	it.cesIndex++
	return e
}

// appendCEs is appendCEsFromCE32 (forward) for code point c. A non-nil
// mapped is a CE32 already resolved to plain CEs (a contraction result).
func (it *iter) appendCEs(c rune, mapped []ce) {
	if mapped != nil {
		it.ceBuffer = append(it.ceBuffer, mapped...)
		return
	}
	t := it.t
	// PREFIX_TAG: the root prefixes are single code points.
	if ps, ok := t.prefixed[c]; ok {
		it.backwardNumCodePoints(1)
		var hit []ce
		lookBehind := 0
		if prev := it.previousCodePoint(); prev >= 0 {
			lookBehind++
			for _, p := range ps {
				if len(p.prefix) == 1 && p.prefix[0] == prev {
					hit = p.ces
					break
				}
			}
		}
		it.forwardNumCodePoints(lookBehind)
		it.forwardNumCodePoints(1)
		if hit != nil {
			it.ceBuffer = append(it.ceBuffer, hit...)
			return
		}
	}
	// CONTRACTION_TAG.
	if flags, ok := t.contractFlags[c]; ok {
		def := t.lookupSingle(c)
		var nextCp rune
		if it.skipped == nil {
			nextCp = it.nextCodePoint()
			if nextCp < 0 {
				it.ceBuffer = append(it.ceBuffer, def...)
				return
			}
			if flags&contractNextCCC != 0 && !t.mayHaveLccc(nextCp) {
				it.backwardNumCodePoints(1)
				it.ceBuffer = append(it.ceBuffer, def...)
				return
			}
		} else {
			nextCp = it.nextSkippedCodePoint()
			if nextCp < 0 {
				it.ceBuffer = append(it.ceBuffer, def...)
				return
			}
			if flags&contractNextCCC != 0 && !t.mayHaveLccc(nextCp) {
				it.backwardNumSkipped(1)
				it.ceBuffer = append(it.ceBuffer, def...)
				return
			}
		}
		if ces, done := it.nextCE32FromContraction(c, flags, def, nextCp); !done {
			it.ceBuffer = append(it.ceBuffer, ces...)
		}
		return
	}
	it.ceBuffer = append(it.ceBuffer, t.lookupSingle(c)...)
}

const (
	contractNextCCC     = 0x200
	contractTrailingCCC = 0x400
)

// nextCE32FromContraction returns the CEs for the longest match, or done
// when a discontiguous contraction already appended its CEs.
func (it *iter) nextCE32FromContraction(starter rune, flags uint16, ces []ce, c rune) ([]ce, bool) {
	t := it.t
	lookAhead, sinceMatch := 1, 1
	suffixes := &suffixTrie{t: t, key: []rune{starter}}
	if it.skipped != nil && !it.skipped.isEmpty() {
		it.skipped.state = suffixes.save()
	}
	match := suffixes.first(c)
	for {
		var nextCp rune
		if hasValue(match) {
			ces = suffixes.value()
			if !hasNext(match) {
				return ces, false
			}
			if c = it.nextSkippedCodePoint(); c < 0 {
				return ces, false
			}
			if it.skipped != nil && !it.skipped.isEmpty() {
				it.skipped.state = suffixes.save()
			}
			sinceMatch = 1
		} else if match == noMatch || func() bool { nextCp = it.nextSkippedCodePoint(); return nextCp < 0 }() {
			// No match for c, or a partial match and no more text: back
			// up if necessary and try a discontiguous contraction.
			if flags&contractTrailingCCC != 0 {
				if sinceMatch > 1 {
					it.backwardNumSkipped(sinceMatch)
					c = it.nextSkippedCodePoint()
					lookAhead -= sinceMatch - 1
					sinceMatch = 1
				}
				if t.fcd16v(c) > 0xff {
					return it.nextCE32FromDiscontiguousContraction(suffixes, ces, lookAhead, c)
				}
			}
			break
		} else {
			c = nextCp
			sinceMatch++
		}
		lookAhead++
		match = suffixes.next(c)
	}
	it.backwardNumSkipped(sinceMatch)
	return ces, false
}

func (it *iter) nextCE32FromDiscontiguousContraction(suffixes *suffixTrie, ces []ce, lookAhead int, c rune) ([]ce, bool) {
	t := it.t
	fcd := t.fcd16v(c)
	nextCp := it.nextSkippedCodePoint()
	if nextCp < 0 {
		it.backwardNumSkipped(1)
		return ces, false
	}
	lookAhead++
	prevCC := uint8(fcd)
	fcd = t.fcd16v(nextCp)
	if fcd <= 0xff {
		// The next code point after c is a starter.
		it.backwardNumSkipped(2)
		return ces, false
	}
	if it.skipped == nil || it.skipped.isEmpty() {
		if it.skipped == nil {
			it.skipped = &skippedState{}
		}
		suffixes.reset()
		if lookAhead > 2 {
			// Replay the partial match so far.
			it.backwardNumCodePoints(lookAhead)
			suffixes.first(it.nextCodePoint())
			for i := 3; i < lookAhead; i++ {
				suffixes.next(it.nextCodePoint())
			}
			// Skip c (which did not match) and nextCp (which we try now).
			it.forwardNumCodePoints(2)
		}
		it.skipped.state = suffixes.save()
	} else {
		suffixes.restore(it.skipped.state)
	}
	it.skipped.setFirstSkipped(c)
	sinceMatch := 2
	c = nextCp
	for {
		matched := false
		if prevCC < uint8(fcd>>8) {
			if m := suffixes.next(c); hasValue(m) {
				// S2.1.3: replace S by S + C and remove C; prevCC stays.
				matched = true
				ces = suffixes.value()
				sinceMatch = 0
				it.skipped.recordMatch()
				if !hasNext(m) {
					break
				}
				it.skipped.state = suffixes.save()
			}
		}
		if !matched {
			it.skipped.skip(c)
			suffixes.restore(it.skipped.state)
			prevCC = uint8(fcd)
		}
		if c = it.nextSkippedCodePoint(); c < 0 {
			break
		}
		sinceMatch++
		fcd = t.fcd16v(c)
		if fcd <= 0xff {
			break
		}
	}
	it.backwardNumSkipped(sinceMatch)
	isTopDiscontiguous := it.skipped.isEmpty()
	it.skipped.replaceMatch()
	if isTopDiscontiguous && !it.skipped.isEmpty() {
		// A match after skipping marks, not nested in another one: the
		// contraction's CEs, then the CEs of the skipped marks.
		it.appendCEs(-1, ces)
		for it.skipped.hasNext() {
			// A nested discontiguous match replaces the consumed marks and
			// resets the reading position, so re-check each time.
			it.appendCEs(it.skipped.next(), nil)
		}
		it.skipped.clear()
		return nil, true
	}
	return ces, false
}

// compare is CollationCompare::compareUpToQuaternary at tertiary strength,
// alternate non-ignorable, no case level or case-first: primaries are
// fetched lazily and compared first; secondaries and tertiaries only after
// both strings ran out with equal primaries.
func compare(l, r *iter) int {
	for {
		var lp, rp uint32
		for lp == 0 {
			lp = l.nextCE().p
		}
		for rp == 0 {
			rp = r.nextCE().p
		}
		if lp != rp {
			if lp < rp {
				return -1
			}
			return 1
		}
		if lp == noCE.p {
			break
		}
	}
	for li, ri := 0, 0; ; {
		var ls, rs uint16
		for ls == 0 {
			ls = l.ceBuffer[li].s
			li++
		}
		for rs == 0 {
			rs = r.ceBuffer[ri].s
			ri++
		}
		if ls != rs {
			if ls < rs {
				return -1
			}
			return 1
		}
		if ls == noCEWeight16 {
			break
		}
	}
	for li, ri := 0, 0; ; {
		var lt, rt uint16
		for lt == 0 {
			lt = l.ceBuffer[li].t & onlyTertiaryMask
			li++
		}
		for rt == 0 {
			rt = r.ceBuffer[ri].t & onlyTertiaryMask
			ri++
		}
		if lt != rt {
			if lt < rt {
				return -1
			}
			return 1
		}
		if lt == noCEWeight16 {
			break
		}
	}
	return 0
}

// allCEs is fetchCEs from the start of the text, without the NO_CE.
func (t *table) allCEs(units []uint16) []ce {
	it := t.newIter(units, 0)
	for it.nextCE() != noCE {
		it.cesIndex = len(it.ceBuffer)
	}
	return it.ceBuffer[:len(it.ceBuffer)-1]
}
