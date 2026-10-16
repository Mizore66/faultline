package witness

import (
	"bytes"
	"runtime"
	"strings"
)

// How Node 22 (ncrypto over OpenSSL 3.5/3.6) reads a PEM string in
// createPublicKey and createPrivateKey, ported so that Go accepts and
// refuses the same reviewer keys with the same error text:
//
//   - pemBIO.gets, sanitizeLine, readBlock: crypto/pem/pem_lib.c
//     PEM_read_bio_ex (PEM_FLAG_EAY_COMPATIBLE) over a read-only memory BIO
//     (crypto/bio/bss_mem.c mem_gets), with crypto/evp/encode.c
//     EVP_DecodeUpdate/EVP_DecodeFinal.
//   - cipherInfo: pem_lib.c PEM_get_EVP_CIPHER_INFO.
//   - parsePublic: ncrypto TryParsePublicKeyPEM and its three passes.
//   - parsePrivate: crypto/pem/pem_pkey.c pem_read_bio_key (the decoder loop,
//     then the legacy reader), as Node's TryParsePrivateKey calls it.
//
// Node reports the oldest error in OpenSSL's queue; the comments say which
// errors are popped on the way. A read failure is never reported (every
// caller pops PEM read errors), so readBlock only says whether it worked.

// charSigned is whether C char is signed on this platform: sanitize_line
// compares bytes as char, so where char is signed every byte >= 0x80
// counts as trailing whitespace. Signed on x86, on Apple and Windows ABIs,
// unsigned on Linux/BSD arm, arm64, ppc64, s390x and riscv64.
var charSigned = func() bool {
	switch runtime.GOOS {
	case "darwin", "ios", "windows":
		return true
	}
	switch runtime.GOARCH {
	case "arm", "arm64", "ppc64", "ppc64le", "s390x", "riscv64":
		return false
	}
	return true
}()

// pemBIO is a read-only memory BIO: BIO_gets reads one line at a time.
type pemBIO struct {
	data []byte
	pos  int
}

// gets ports bss_mem.c mem_gets as PEM_read_bio_ex calls it (LINESIZE 255):
// at most 254 bytes, up to and including the first newline. nil at the end.
func (b *pemBIO) gets() []byte {
	rest := b.data[b.pos:]
	j := min(len(rest), 254)
	i := 0
	for i < j {
		i++
		if rest[i-1] == '\n' {
			break
		}
	}
	b.pos += i
	return rest[:i]
}

func (b *pemBIO) eof() bool { return b.pos >= len(b.data) }

// cstr is a C string's view of s: the bytes before the first NUL.
func cstr(s []byte) []byte {
	if i := bytes.IndexByte(s, 0); i >= 0 {
		return s[:i]
	}
	return s
}

// Port of pem_lib.c sanitize_line (EAY_COMPATIBLE): strip a UTF-8 BOM from
// the first line read by each PEM_read_bio_ex call, strip trailing bytes
// <= ' ' (as char), end the line with "\n".
func sanitizeLine(line []byte, firstCall bool) []byte {
	buf := append([]byte(nil), line...)
	if firstCall && len(buf) > 3 && buf[0] == 0xEF && buf[1] == 0xBB && buf[2] == 0xBF {
		buf = buf[3:]
	}
	// The loop starts at linebuf[len], BIO_gets' terminating NUL.
	n := len(buf)
	i := n
	for i >= 0 && (i == n || buf[i] <= ' ' || (charSigned && buf[i] >= 0x80)) {
		i--
	}
	return append(buf[:i+1], '\n')
}

// b64 is encode.c data_ascii2bin: 0xE0 whitespace (space, tab), 0xF0 LF,
// 0xF1 CR, 0xF2 '-' (end of data), 0xFF error, else the 6-bit value ('='
// is 0).
var b64 = func() (t [256]byte) {
	for i := range t {
		t[i] = 0xFF
	}
	for i, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/" {
		t[c] = byte(i)
	}
	t['='] = 0
	t[' '], t['\t'], t['\n'], t['\r'], t['-'] = 0xE0, 0xE0, 0xF0, 0xF1, 0xF2
	return t
}()

func b64Data(v byte) bool { return v|0x13 != 0xF3 } // B64_BASE64

// Port of encode.c EVP_DecodeUpdate then EVP_DecodeFinal over the whole data.
func evpDecode(in []byte) ([]byte, bool) {
	var out []byte
	var d []byte
	eof := 0
	seof := false
	for _, c := range in {
		v := b64[c]
		if v == 0xFF {
			return nil, false
		}
		if c == '=' {
			eof++
		} else if eof > 0 && b64Data(v) {
			return nil, false // data after padding
		}
		if eof > 2 {
			return nil, false
		}
		if v == 0xF2 {
			seof = true
			break
		}
		if b64Data(v) {
			d = append(d, c)
		}
		if len(d) == 64 {
			block, ok := decodeBlock(d, eof)
			if !ok {
				return nil, false
			}
			out = append(out, block...)
			d = d[:0]
		}
	}
	if len(d) > 0 {
		if len(d)%4 == 0 {
			block, ok := decodeBlock(d, eof)
			if !ok {
				return nil, false
			}
			out = append(out, block...)
			d = d[:0]
		} else if seof {
			return nil, false // end of data inside a group
		}
	}
	// EVP_DecodeFinal: leftover characters are never a whole group.
	if len(d) != 0 {
		return nil, false
	}
	return out, true
}

// Port of encode.c evp_decodeblock_int for a block of base64 characters
// (Update only stores those): eof is the number of '=' seen.
func decodeBlock(f []byte, eof int) ([]byte, bool) {
	out := make([]byte, 0, len(f)/4*3)
	for i := 0; i < len(f); i += 4 {
		l := uint32(b64[f[i]])<<18 | uint32(b64[f[i+1]])<<12 | uint32(b64[f[i+2]])<<6 | uint32(b64[f[i+3]])
		out = append(out, byte(l>>16), byte(l>>8), byte(l))
	}
	out = out[:len(out)-eof]
	if len(out) == 0 && eof > 0 {
		return nil, false
	}
	return out, true
}

// strspn and strcspn over a C string held in s (the end acts as NUL).
func strspn(s, set string) int {
	i := 0
	for i < len(s) && strings.IndexByte(set, s[i]) >= 0 {
		i++
	}
	return i
}

func strcspn(s, set string) int {
	i := 0
	for i < len(s) && strings.IndexByte(set, s[i]) < 0 {
		i++
	}
	return i
}

func at(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// anyPrivateKey is pem_lib.c check_pem(name, "ANY PRIVATE KEY"): PKCS#8 and
// the "<X> PRIVATE KEY" names of the methods with an old_priv_decode (RSA,
// DSA, EC, compared without case: EVP_PKEY_asn1_find_str).
func anyPrivateKey(name string) bool {
	if name == "PRIVATE KEY" || name == "ENCRYPTED PRIVATE KEY" {
		return true
	}
	prefix, found := strings.CutSuffix(name, " PRIVATE KEY")
	if !found {
		return false
	}
	switch asciiLower(prefix) {
	case "rsa", "dsa", "ec":
		return true
	}
	return false
}
