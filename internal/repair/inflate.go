package repair

import "errors"

// A raw DEFLATE (RFC 1951) decoder with zlib's streaming semantics, as
// Node's zlib streams with finishFlush Z_SYNC_FLUSH run it (undici's
// response decoders): every symbol whose bits have all arrived is
// decoded, a stored block's bytes are copied as they arrive, and input
// that ends mid-stream yields what was decoded without an error.
// compress/flate stops short of that on truncated input. The structure
// follows zlib's contrib/puff/puff.c.

var errInflate = errors.New("invalid deflate data")

// errNeedInput is the end of the input before the stream ended.
var errNeedInput = errors.New("need input")

type inflater struct {
	in     []byte
	pos    int // next input byte
	bitbuf uint32
	bitcnt uint
	out    []byte
	final  bool // the last block has been decoded
}

// bits returns need bits, LSB first; errNeedInput when the input runs out.
func (s *inflater) bits(need uint) (int, error) {
	val := s.bitbuf
	for s.bitcnt < need {
		if s.pos == len(s.in) {
			return 0, errNeedInput
		}
		val |= uint32(s.in[s.pos]) << s.bitcnt
		s.pos++
		s.bitcnt += 8
	}
	s.bitbuf = val >> need
	s.bitcnt -= need
	return int(val & (1<<need - 1)), nil
}

// huffman is a canonical code: count[len] codes of each length, symbols in
// code order.
type huffman struct {
	count  [16]int
	symbol []int
	max    int // the longest code length; 0 for a code with no symbols
	// zeroForInvalid is zlib's CODELENS quirk: a code-length code with no
	// symbols decodes every 1-bit read as length 0 (its invalid table
	// entry has val 0, and that state never checks op).
	zeroForInvalid bool
}

// decode reads one symbol bit by bit (puff.c decode, the slow form): it
// needs only the code's own bits, so it decodes as far as zlib does.
func (s *inflater) decode(h *huffman) (int, error) {
	code, first, index := 0, 0, 0
	for l := 1; l <= 15; l++ {
		// zlib's tables mark the one missing code of an incomplete or
		// empty code invalid after a single bit.
		if l > max(h.max, 1) {
			return 0, errInflate
		}
		b, err := s.bits(1)
		if err != nil {
			return 0, err
		}
		code |= b
		count := h.count[l]
		if code-count < first {
			return h.symbol[index+(code-first)], nil
		}
		if h.max == 0 && h.zeroForInvalid {
			return 0, nil
		}
		index += count
		first += count
		first <<= 1
		code <<= 1
	}
	return 0, errInflate // ran out of codes
}

// construct builds h from code lengths. It returns the number of unused
// codes: 0 for a complete code, negative for an over-subscribed one.
func construct(h *huffman, length []int) int {
	h.count = [16]int{}
	h.max = 0
	for _, l := range length {
		h.count[l]++
		h.max = max(h.max, l)
	}
	if h.count[0] == len(length) {
		h.symbol = nil
		return 0
	}
	left := 1
	for l := 1; l <= 15; l++ {
		left <<= 1
		left -= h.count[l]
		if left < 0 {
			return left
		}
	}
	var offs [16]int
	for l := 1; l < 15; l++ {
		offs[l+1] = offs[l] + h.count[l]
	}
	h.symbol = make([]int, len(length))
	for sym, l := range length {
		if l != 0 {
			h.symbol[offs[l]] = sym
			offs[l]++
		}
	}
	return left
}

var (
	lengthBase  = [29]int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	lengthExtra = [29]uint{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	distBase    = [30]int{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	distExtra   = [30]uint{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}
)

// codes decodes one block's literals and matches. zlib copies a match only
// once its length and distance have both been read.
func (s *inflater) codes(lencode, distcode *huffman) error {
	for {
		sym, err := s.decode(lencode)
		if err != nil {
			return err
		}
		switch {
		case sym < 256:
			s.out = append(s.out, byte(sym))
			continue
		case sym == 256:
			return nil
		}
		sym -= 257
		if sym >= 29 {
			return errInflate // invalid literal/length code
		}
		extra, err := s.bits(lengthExtra[sym])
		if err != nil {
			return err
		}
		length := lengthBase[sym] + extra
		dsym, err := s.decode(distcode)
		if err != nil {
			return err
		}
		if dsym >= 30 {
			return errInflate // invalid distance code
		}
		dextra, err := s.bits(distExtra[dsym])
		if err != nil {
			return err
		}
		dist := distBase[dsym] + dextra
		if dist > len(s.out) {
			return errInflate // invalid distance too far back
		}
		for range length {
			s.out = append(s.out, s.out[len(s.out)-dist])
		}
	}
}

var fixedLen, fixedDist = func() (*huffman, *huffman) {
	var lengths [288]int
	for i := range lengths {
		switch {
		case i < 144:
			lengths[i] = 8
		case i < 256:
			lengths[i] = 9
		case i < 280:
			lengths[i] = 7
		default:
			lengths[i] = 8
		}
	}
	l := &huffman{}
	construct(l, lengths[:])
	var dl [30]int
	for i := range dl {
		dl[i] = 5
	}
	d := &huffman{}
	construct(d, dl[:])
	return l, d
}()

// stored copies a stored block's bytes as they arrive, as zlib does.
func (s *inflater) stored() error {
	s.bitbuf, s.bitcnt = 0, 0 // to a byte boundary
	if len(s.in)-s.pos < 4 {
		s.pos = len(s.in)
		return errNeedInput
	}
	n := int(s.in[s.pos]) | int(s.in[s.pos+1])<<8
	if s.in[s.pos+2] != ^s.in[s.pos] || s.in[s.pos+3] != ^s.in[s.pos+1] {
		return errInflate // invalid stored block lengths
	}
	s.pos += 4
	avail := min(n, len(s.in)-s.pos)
	s.out = append(s.out, s.in[s.pos:s.pos+avail]...)
	s.pos += avail
	if avail < n {
		return errNeedInput
	}
	return nil
}

var codeLengthOrder = [19]int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

// dynamic reads a dynamic block's code tables, then its codes. zlib checks
// each table as it completes (inflate.c CODELENS).
func (s *inflater) dynamic() error {
	nlen, err := s.bits(5)
	if err != nil {
		return err
	}
	ndist, err := s.bits(5)
	if err != nil {
		return err
	}
	ncode, err := s.bits(4)
	if err != nil {
		return err
	}
	nlen, ndist, ncode = nlen+257, ndist+1, ncode+4
	if nlen > 286 || ndist > 30 {
		return errInflate // too many length or distance symbols
	}
	var lengths [320]int
	for i := range ncode {
		v, err := s.bits(3)
		if err != nil {
			return err
		}
		lengths[codeLengthOrder[i]] = v
	}
	lencode := &huffman{zeroForInvalid: true}
	if left := construct(lencode, lengths[:19]); left < 0 || (left > 0 && lencode.max != 0) {
		return errInflate // invalid code lengths set
	}
	lengths = [320]int{}
	for index := 0; index < nlen+ndist; {
		sym, err := s.decode(lencode)
		if err != nil {
			return err
		}
		if sym < 16 {
			lengths[index] = sym
			index++
			continue
		}
		l, repeat := 0, 0
		switch sym {
		case 16:
			r, err := s.bits(2)
			if err != nil {
				return err
			}
			if index == 0 {
				return errInflate // invalid bit length repeat
			}
			l = lengths[index-1]
			repeat = 3 + r
		case 17:
			r, err := s.bits(3)
			if err != nil {
				return err
			}
			repeat = 3 + r
		default:
			r, err := s.bits(7)
			if err != nil {
				return err
			}
			repeat = 11 + r
		}
		if index+repeat > nlen+ndist {
			return errInflate // invalid bit length repeat
		}
		for range repeat {
			lengths[index] = l
			index++
		}
	}
	if lengths[256] == 0 {
		return errInflate // invalid code -- missing end-of-block
	}
	// zlib (inflate_table) accepts an incomplete code only when its longest
	// code is one bit long, and an empty one.
	litlen := &huffman{}
	if left := construct(litlen, lengths[:nlen]); left < 0 || (left > 0 && litlen.max > 1) {
		return errInflate // invalid literal/lengths set
	}
	dist := &huffman{}
	if left := construct(dist, lengths[nlen:nlen+ndist]); left < 0 || (left > 0 && dist.max > 1) {
		return errInflate // invalid distances set
	}
	return s.codes(litlen, dist)
}

// run decodes blocks until the last one ends, the input runs out
// (errNeedInput) or the data is invalid (errInflate).
func (s *inflater) run() error {
	for !s.final {
		last, err := s.bits(1)
		if err != nil {
			return err
		}
		typ, err := s.bits(2)
		if err != nil {
			return err
		}
		switch typ {
		case 0:
			err = s.stored()
		case 1:
			err = s.codes(fixedLen, fixedDist)
		case 2:
			err = s.dynamic()
		default:
			return errInflate // invalid block type
		}
		if err != nil {
			return err
		}
		s.final = last == 1
	}
	return nil
}

// rest is the input after the stream's last byte (the bit buffer drops the
// final partial byte, as zlib's BYTEBITS does).
func (s *inflater) rest() []byte { return s.in[s.pos:] }

// inflateRaw decodes raw DEFLATE data from in. done reports that the last
// block ended; err is errInflate for invalid data.
func inflateRaw(in []byte) (s *inflater, done bool, err error) {
	s = &inflater{in: in}
	switch err := s.run(); err {
	case nil:
		return s, true, nil
	case errNeedInput:
		return s, false, nil
	default:
		return s, false, err
	}
}
