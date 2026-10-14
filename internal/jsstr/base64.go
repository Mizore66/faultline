package jsstr

// DecodeBase64Lenient is Buffer.from(s, "base64") in Node 22. The JS side
// sizes the buffer with base64ByteLength (lib/buffer.js) over UTF-16
// units; the C++ side decodes with nbytes' Base64Decode whenever simdutf's
// forgiving-base64 check rejects the input, and that decoder agrees with
// simdutf on every input simdutf accepts. Base64Decode skips characters
// outside both alphabets ('+' '/' and '-' '_'), stops at the first '='
// read outside a whole group, and reads each UTF-16 unit of a two-byte
// string by its low byte. Trailing bits of a short final group are dropped.
func DecodeBase64Lenient(s string) []byte {
	src := ToUTF16(s)
	dst := make([]byte, base64ByteLength(src))
	return dst[:base64Decode(dst, src)]
}

// base64ByteLength ports lib/buffer.js base64ByteLength.
func base64ByteLength(src []uint16) int {
	n := len(src)
	if n > 0 && src[n-1] == '=' {
		n--
	}
	if n > 1 && src[n-1] == '=' {
		n--
	}
	return int(uint32(n*3) >> 2)
}

// unbase64 is nbytes' unbase64_table: both alphabets, everything else 255.
var unbase64 = func() (table [256]byte) {
	for i := range table {
		table[i] = 255
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for i := 0; i < len(alphabet); i++ {
		table[alphabet[i]] = byte(i)
	}
	table['-'], table['_'] = 62, 63
	return table
}()

// base64DecodedSize ports nbytes Base64DecodedSize (its '=' checks compare
// whole units).
func base64DecodedSize(src []uint16) int {
	size := len(src)
	if size < 2 {
		return 0
	}
	if src[size-1] == '=' {
		size--
		if src[size-1] == '=' {
			size--
		}
	}
	if size <= 1 {
		return 0
	}
	return size/4*3 + (size%4+1)/2
}

// base64Decode ports nbytes Base64Decode / Base64DecodeFast.
func base64Decode(dst []byte, src []uint16) int {
	available := min(len(dst), base64DecodedSize(src))
	maxK := available / 3 * 3
	maxI := len(src) / 4 * 4
	i, k := 0, 0
	for i < maxI && k < maxK {
		a, b, c, d := unbase64[byte(src[i])], unbase64[byte(src[i+1])], unbase64[byte(src[i+2])], unbase64[byte(src[i+3])]
		if (a|b|c|d)&0x80 != 0 {
			if !base64DecodeGroupSlow(dst, src, &i, &k) {
				return k
			}
			maxI = i + (len(src)-i)/4*4
			continue
		}
		dst[k] = a<<2 | b>>4
		dst[k+1] = b<<4 | c>>2
		dst[k+2] = c<<6 | d
		i += 4
		k += 3
	}
	if i < len(src) && k < len(dst) {
		base64DecodeGroupSlow(dst, src, &i, &k)
	}
	return k
}

// base64DecodeGroupSlow ports nbytes Base64DecodeGroupSlow: one group of
// four legal characters, skipping illegal ones; false stops decoding.
func base64DecodeGroupSlow(dst []byte, src []uint16, i, k *int) bool {
	var hi, lo byte
	next := func() bool {
		for {
			c := byte(src[*i])
			lo = unbase64[c]
			*i++
			if lo < 64 {
				return true
			}
			if c == '=' || *i >= len(src) {
				return false
			}
		}
	}
	after := func() bool { return *i < len(src) && *k < len(dst) }
	if !next() || !after() {
		return false
	}
	hi = lo
	if !next() {
		return false
	}
	dst[*k] = (hi&0x3F)<<2 | (lo&0x30)>>4
	*k++
	if !after() {
		return false
	}
	hi = lo
	if !next() {
		return false
	}
	dst[*k] = (hi&0x0F)<<4 | (lo&0x3C)>>2
	*k++
	if !after() {
		return false
	}
	hi = lo
	if !next() {
		return false
	}
	dst[*k] = (hi&0x03)<<6 | lo&0x3F
	*k++
	if !after() {
		return false
	}
	return true
}
