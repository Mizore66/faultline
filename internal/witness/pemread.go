package witness

import (
	"bytes"
	"crypto/ed25519"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
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

// Error messages Node prints for the OpenSSL errors that can surface.
const (
	opensslUnsupported = "error:1E08010C:DECODER routines::unsupported"
	opensslCancelled   = "error:07880109:common libcrypto routines::interrupted or cancelled"
)

func pemErr(reason int, text string) string {
	return fmt.Sprintf("error:%08X:PEM routines::%s", 9<<23|reason, text)
}

// PEM_R_* reasons PEM_get_EVP_CIPHER_INFO raises.
var (
	errPEMNotProcType   = pemErr(107, "not proc type")
	errPEMNotEncrypted  = pemErr(106, "not encrypted")
	errPEMShortHeader   = pemErr(112, "short header")
	errPEMNotDEKInfo    = pemErr(105, "not dek info")
	errPEMUnsupportedEn = pemErr(114, "unsupported encryption")
	errPEMMissingDEKIV  = pemErr(129, "missing dek iv")
	errPEMUnexpectedIV  = pemErr(130, "unexpected dek iv")
	errPEMBadIVChars    = pemErr(103, "bad iv chars")
)

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

// Port of pem_lib.c get_name: skip lines until "-----BEGIN <name>-----".
// The name is a C string, so a NUL inside it ends it.
func getName(b *pemBIO) ([]byte, bool) {
	first := true
	for {
		line := b.gets()
		if len(line) == 0 {
			return nil, false // PEM_R_NO_START_LINE
		}
		s := sanitizeLine(line, first)
		first = false
		if bytes.HasPrefix(s, []byte("-----BEGIN ")) && len(s) >= 17 && bytes.HasSuffix(s, []byte("-----\n")) {
			return cstr(s[11 : len(s)-6]), true
		}
	}
}

// Port of pem_lib.c get_header_and_data. Lines before a blank line are the
// header; without a blank line, every line is data unless one held a ':'
// (then all of them are header and the data is empty).
func getHeaderAndData(b *pemBIO, name []byte) (header, data []byte, ok bool) {
	const (
		maybeHeader = iota
		inHeader
		postHeader
	)
	state := maybeHeader
	var hdr, dat []byte
	tmp := &hdr
	partial, end := false, false
	for {
		line := b.gets()
		if len(line) == 0 {
			return nil, nil, false // PEM_R_BAD_END_LINE
		}
		// A line longer than 253 bytes comes in chunks; a newline that only
		// ends the previous chunk is not a blank line.
		prevPartial := partial
		partial = len(line) == 254 && line[253] != '\n'
		if state == maybeHeader && bytes.IndexByte(line, ':') >= 0 {
			state = inHeader
		}
		s := sanitizeLine(line, false)
		if s[0] == '\n' {
			if !prevPartial {
				if state == postHeader {
					return nil, nil, false // a second blank line
				}
				state = postHeader
				tmp = &dat
			}
			continue
		}
		if rest, isEnd := bytes.CutPrefix(s, []byte("-----END ")); isEnd {
			if !bytes.HasPrefix(rest, name) || !bytes.HasPrefix(rest[len(name):], []byte("-----\n")) {
				return nil, nil, false
			}
			if state == maybeHeader {
				return nil, hdr, true
			}
			return hdr, dat, true
		} else if end {
			return nil, nil, false // a short line not at the end of the data
		}
		*tmp = append(*tmp, cstr(s)...) // BIO_puts
		if state == postHeader {
			// The 64-column check of encrypted (headed) data.
			if len(s) > 65 {
				return nil, nil, false
			}
			if len(s) < 65 {
				end = true
			}
		}
	}
}

// pemBlock is what PEM_read_bio_ex returns: name and header are C strings.
type pemBlock struct {
	name   string
	header string
	data   []byte
}

// readBlock ports PEM_read_bio_ex: the next block, or false when none can
// be read (the lines it consumed stay consumed).
func readBlock(b *pemBIO) (pemBlock, bool) {
	name, ok := getName(b)
	if !ok {
		return pemBlock{}, false
	}
	header, data, ok := getHeaderAndData(b, name)
	if !ok || len(data) == 0 {
		return pemBlock{}, false
	}
	der, ok := evpDecode(data)
	// No decoded bytes: PEM_MALLOC(0) is NULL, which fails the read.
	if !ok || len(der) == 0 {
		return pemBlock{}, false
	}
	return pemBlock{name: string(name), header: string(header), data: der}, true
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

// Port of pem_lib.c PEM_get_EVP_CIPHER_INFO: whether the header names a
// cipher; ok false is a failure, with reason "" when OpenSSL raises nothing.
func cipherInfo(h string) (cipher bool, reason string, ok bool) {
	if h == "" || h[0] == '\n' {
		return false, "", true
	}
	h, found := strings.CutPrefix(h, "Proc-Type:")
	if !found {
		return false, errPEMNotProcType, false
	}
	h = h[strspn(h, " \t"):]
	if at(h, 0) != '4' || at(h, 1) != ',' {
		return false, "", false
	}
	h = h[2:]
	h = h[strspn(h, " \t"):]
	h, found = strings.CutPrefix(h, "ENCRYPTED")
	if !found || strspn(h, " \t\r\n") == 0 {
		return false, errPEMNotEncrypted, false
	}
	h = h[strspn(h, " \t\r"):]
	if at(h, 0) != '\n' {
		return false, errPEMShortHeader, false
	}
	h, found = strings.CutPrefix(h[1:], "DEK-Info:")
	if !found {
		return false, errPEMNotDEKInfo, false
	}
	h = h[strspn(h, " \t"):]
	name := h[:strcspn(h, " \t,")]
	h = h[len(name):]
	h = h[strspn(h, " \t"):]
	ivlen, known := pemCipherIVLength[asciiLower(name)]
	if !known {
		return false, errPEMUnsupportedEn, false
	}
	if ivlen > 0 {
		if at(h, 0) != ',' {
			return false, errPEMMissingDEKIV, false
		}
		h = h[1:]
	} else if at(h, 0) == ',' {
		return false, errPEMUnexpectedIV, false
	}
	for i := 0; i < 2*ivlen; i++ {
		if !isHex(at(h, i)) {
			return false, errPEMBadIVChars, false
		}
	}
	return true, "", true
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

// bytesRead ports pem_lib.c pem_bytes_read_bio_flags: the first block whose
// name matches (a block that cannot be read ends the search), refused if
// its header does not parse or names a cipher. A cipher would need a
// passphrase: Node gives none (the legacy reader's callback returns -1),
// and in the public passes PEM_def_callback prompts on the terminal, which
// Go does not (KNOWN_DIFFERENCES.md); without a terminal both fail.
func bytesRead(in []byte, match func(name string) bool) (pemBlock, bool) {
	b := &pemBIO{data: in}
	for {
		blk, ok := readBlock(b)
		if !ok {
			return pemBlock{}, false
		}
		if match(blk.name) {
			cipher, _, ok := cipherInfo(blk.header)
			return blk, ok && !cipher
		}
	}
}

func nameIs(names ...string) func(string) bool {
	return func(name string) bool {
		for _, n := range names {
			if name == n {
				return true
			}
		}
		return false
	}
}

// parsePublic is createPublicKey(pem) (ncrypto TryParsePublicKeyPEM): a
// "PUBLIC KEY", then an "RSA PUBLIC KEY", then a "CERTIFICATE" pass, each
// from the start. A pass whose block is missing or unreadable is skipped;
// one whose DER does not decode leaves its error queued and the next pass
// runs. The first pass that decodes wins; otherwise a queued decode error is
// the message (oldest first), and with none Node falls back to
// createPrivateKey.
func parsePublic(text string) (keyObject, error) {
	in := []byte(jsstr.ToUTF8(text))
	passes := []struct {
		match func(string) bool
		parse func([]byte) (keyObject, string)
	}{
		{nameIs("PUBLIC KEY"), parseSPKI},
		{nameIs("RSA PUBLIC KEY"), parseRSAPublicKey},
		{nameIs("CERTIFICATE", "X509 CERTIFICATE"), parseCertificate},
	}
	queued := ""
	for _, pass := range passes {
		blk, ok := bytesRead(in, pass.match)
		if !ok {
			continue
		}
		key, reason := pass.parse(blk.data)
		if reason == "" {
			return key, nil
		}
		if queued == "" {
			queued = reason
		}
	}
	if queued != "" {
		// createPrivateKey's keyOrError peeks the queue first: the queued
		// error is the result whatever the private reader finds.
		return keyObject{}, errors.New(queued)
	}
	return parsePrivate(text)
}

// parseSPKI is d2i_PUBKEY: X509_PUBKEY, then its key (X509_PUBKEY_get0).
func parseSPKI(der []byte) (keyObject, string) {
	v, reason := d2i(pubkeyItem, der)
	if reason != "" {
		return keyObject{}, reason
	}
	if v.pub == nil {
		return keyObject{}, errEVPDecode
	}
	return *v.pub, ""
}

// parseRSAPublicKey is d2i_PublicKey(EVP_PKEY_RSA): an RSAPublicKey.
func parseRSAPublicKey(der []byte) (keyObject, string) {
	if _, reason := d2i(rsaPublicKeyItem, der); reason != "" {
		return keyObject{}, reason
	}
	return keyObject{}, ""
}

// parseCertificate is d2i_X509 then X509_get_pubkey.
func parseCertificate(der []byte) (keyObject, string) {
	v, reason := d2i(x509Item, der)
	if reason != "" {
		return keyObject{}, reason
	}
	if pub := v.fields[0].fields[6].pub; pub != nil {
		return *pub, ""
	}
	return keyObject{}, errEVPDecode
}

// decodeOutcome is one OSSL_DECODER_from_bio call of the decoder loop.
type decodeOutcome int

const (
	decodeUnsupported decodeOutcome = iota // nothing decoded: try the next block
	decodeKey
	decodeFatal // an error other than "unsupported": the loop stops
)

// parsePrivate is createPrivateKey(pem) (pem_pkey.c pem_read_bio_key with
// selection EVP_PKEY_KEYPAIR). The decoder loop reads one block per
// attempt and moves on while the attempt only reports "unsupported"; then,
// if no key came out, the legacy reader starts again from the beginning.
// A legacy key clears every error; otherwise the message is the last
// decoder attempt's error.
func parsePrivate(text string) (keyObject, error) {
	in := []byte(jsstr.ToUTF8(text))
	b := &pemBIO{data: in}
	message := opensslUnsupported
	for {
		pos := b.pos
		key, outcome, reason := decodeOnePrivate(b)
		if outcome == decodeKey {
			return key, nil
		}
		if outcome == decodeFatal {
			message = reason
			break
		}
		message = opensslUnsupported
		if b.eof() || b.pos <= pos {
			break
		}
	}
	if key, ok := legacyPrivate(in); ok {
		return key, nil
	}
	return keyObject{}, errors.New(message)
}

// decodeOnePrivate is one decoder attempt: pem2der (providers/.../
// decode_pem2der.c) reads a block and hands its DER to the decoder for its
// name. A failed read, a name no decoder takes and a structure that does
// not decode all end "empty handed" (DECODER unsupported).
func decodeOnePrivate(b *pemBIO) (keyObject, decodeOutcome, string) {
	blk, ok := readBlock(b)
	if !ok {
		return keyObject{}, decodeUnsupported, ""
	}
	// pem2der only looks at a header longer than "Proc-Type:".
	if len(blk.header) > 10 {
		cipher, reason, ok := cipherInfo(blk.header)
		switch {
		case !ok && reason != "":
			return keyObject{}, decodeFatal, reason
		case !ok:
			return keyObject{}, decodeUnsupported, ""
		case cipher:
			// The passphrase callback fails: Node passes no passphrase.
			return keyObject{}, decodeFatal, opensslCancelled
		}
	}
	switch blk.name {
	case "PRIVATE KEY", "ENCRYPTED PRIVATE KEY":
		// decode_epki2pki.c: an EncryptedPrivateKeyInfo asks for the
		// passphrase; otherwise a PrivateKeyInfo for a known algorithm.
		if _, reason := d2i(x509SigItem, blk.data); reason == "" {
			return keyObject{}, decodeFatal, opensslCancelled
		}
		if key, ok := pkcs8Key(blk.data); ok {
			return key, decodeKey, ""
		}
	case "RSA PRIVATE KEY", "EC PRIVATE KEY", "DSA PRIVATE KEY", "SM2 PRIVATE KEY":
		// Type-specific structures (der2key d2i_private_key).
		if typeSpecificPrivate(blk.name[:len(blk.name)-len(" PRIVATE KEY")], blk.data) {
			return keyObject{}, decodeKey, ""
		}
	}
	return keyObject{}, decodeUnsupported, ""
}

// legacyPrivate ports pem_pkey.c pem_read_bio_key_legacy: the first block
// check_pem matches to "ANY PRIVATE KEY" (an unreadable block before it
// fails the read), decoded by its name.
func legacyPrivate(in []byte) (keyObject, bool) {
	blk, ok := bytesRead(in, anyPrivateKey)
	if !ok {
		return keyObject{}, false
	}
	switch blk.name {
	case "PRIVATE KEY":
		return pkcs8Key(blk.data)
	case "ENCRYPTED PRIVATE KEY":
		// An X509_SIG asks for the passphrase, which fails; anything else
		// does not decode.
		return keyObject{}, false
	}
	// "<RSA|DSA|EC> PRIVATE KEY": the old type-specific decoder, else a
	// PKCS#8 key of the same type, never Ed25519.
	return keyObject{}, typeSpecificPrivate(strings.ToUpper(blk.name[:len(blk.name)-len(" PRIVATE KEY")]), blk.data)
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

// OID content octets.
var (
	oidEd25519 = []byte{0x2B, 0x65, 0x70}
	oidX25519  = []byte{0x2B, 0x65, 0x6E}
	oidX448    = []byte{0x2B, 0x65, 0x6F}
	oidEd448   = []byte{0x2B, 0x65, 0x71}
	oidRSA     = []byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x01}
	oidRSAPSS  = []byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x0A}
	oidEC      = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01}
	oidDSA     = []byte{0x2A, 0x86, 0x48, 0xCE, 0x38, 0x04, 0x01}
	oidDH      = []byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x03, 0x01}
	oidDHX     = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3E, 0x02, 0x01}
)

// ecxLength is the key length of the ECX algorithms (ecx_backend.c KEYLENID).
func ecxLength(oid []byte) int {
	switch {
	case bytes.Equal(oid, oidEd25519), bytes.Equal(oid, oidX25519):
		return 32
	case bytes.Equal(oid, oidX448):
		return 56
	case bytes.Equal(oid, oidEd448):
		return 57
	}
	return 0
}

// decodePublicKey is the opportunistic key decode of an X509_PUBKEY
// (x509_pubkey_ex_d2i_ex): Ed25519 exactly (ecx_backend.c ossl_ecx_key_op:
// parameters absent, 32 bytes); other algorithms approximately (otherKey).
func decodePublicKey(oid []byte, params *asn1Value, key []byte) (keyObject, bool) {
	if bytes.Equal(oid, oidEd25519) {
		if params != nil || len(key) != 32 {
			return keyObject{}, false
		}
		return keyObject{ed25519: true, public: ed25519.PublicKey(append([]byte(nil), key...))}, true
	}
	return keyObject{}, otherKey(oid, params, key, false)
}

// pkcs8Key decodes a PrivateKeyInfo and its key: Ed25519 exactly
// (ossl_ecx_key_from_pkcs8: the key is an OCTET STRING, bytes after it are
// ignored, parameters absent, 32 bytes), other algorithms approximately.
func pkcs8Key(der []byte) (keyObject, bool) {
	v, reason := d2i(pkcs8Item, der)
	if reason != "" {
		return keyObject{}, false
	}
	alg := v.fields[1]
	oid, params, inner := alg.fields[0].data, alg.fields[1], v.fields[2].data
	if !bytes.Equal(oid, oidEd25519) {
		return keyObject{}, otherKey(oid, params, inner, true)
	}
	oct, reason := d2i(octetStringItem, inner)
	if reason != "" || params != nil || len(oct.data) != 32 {
		return keyObject{}, false
	}
	priv := ed25519.NewKeyFromSeed(oct.data)
	return keyObject{ed25519: true, private: priv, public: priv.Public().(ed25519.PublicKey)}, true
}

// otherKey approximates whether OpenSSL decodes a key that is not Ed25519
// (a public key's bit string, or a PKCS#8 key's inner octets). Both answers
// refuse the reviewer key; only the message differs ("is not Ed25519" or
// "must use Ed25519" when OpenSSL decodes it, its error otherwise), which
// KNOWN_DIFFERENCES.md lists.
func otherKey(oid []byte, params *asn1Value, key []byte, private bool) bool {
	if n := ecxLength(oid); n > 0 {
		if private {
			oct, reason := d2i(octetStringItem, key)
			return reason == "" && params == nil && len(oct.data) == n
		}
		return params == nil && len(key) == n
	}
	switch {
	case bytes.Equal(oid, oidRSA), bytes.Equal(oid, oidRSAPSS):
		if private {
			return typeSpecificPrivate("RSA", key)
		}
		_, reason := d2i(rsaPublicKeyItem, key)
		return reason == ""
	case bytes.Equal(oid, oidEC):
		if params == nil || (params.utype != tagObject && params.utype != tagSequence) {
			return false
		}
		if private {
			return typeSpecificPrivate("EC", key)
		}
		return len(key) > 1 && (key[0] == 2 || key[0] == 3 || key[0] == 4)
	case bytes.Equal(oid, oidDSA), bytes.Equal(oid, oidDH), bytes.Equal(oid, oidDHX):
		var n *big.Int
		rest, err := asn1.Unmarshal(key, &n)
		return err == nil && len(rest) == 0 && params != nil
	}
	return false
}

// typeSpecificPrivate approximates the type-specific private key decoders
// (d2i_RSAPrivateKey, d2i_ECPrivateKey, d2i_DSAPrivateKey) by their outer
// structure.
func typeSpecificPrivate(kind string, der []byte) bool {
	var err error
	switch kind {
	case "RSA":
		var k struct {
			Version                     int
			N, E, D, P, Q, Dp, Dq, Qinv *big.Int
			Rest                        asn1.RawValue `asn1:"optional"`
		}
		_, err = asn1.Unmarshal(der, &k)
	case "EC", "SM2":
		var k struct {
			Version int
			Key     []byte
			Params  asn1.RawValue  `asn1:"optional,explicit,tag:0"`
			Public  asn1.BitString `asn1:"optional,explicit,tag:1"`
		}
		_, err = asn1.Unmarshal(der, &k)
	case "DSA":
		var k struct{ Version, P, Q, G, Y, X *big.Int }
		_, err = asn1.Unmarshal(der, &k)
	default:
		return false
	}
	return err == nil
}

// pemCipherIVLength maps the DEK-Info cipher names EVP_get_cipherbyname
// knows in Node 22 (lower case: the lookup ignores case) to their IV
// length. Derived by probing createPrivateKey on Node 22.23.3 with every
// name and alias `openssl list -cipher-algorithms` prints; it only decides
// the error text ("unsupported encryption" or a passphrase failure).
var pemCipherIVLength = map[string]int{
	// IV length 0
	"0.3.4401.5.3.1.9.1": 0, "0.3.4401.5.3.1.9.21": 0, "0.3.4401.5.3.1.9.41": 0, "1.2.156.10197.1.104.1": 0,
	"1.2.410.200046.1.1.1": 0, "1.2.410.200046.1.1.11": 0, "1.2.410.200046.1.1.6": 0,
	"1.2.840.113549.1.9.16.3.6": 0, "1.3.14.3.2.17": 0, "2.16.840.1.101.3.4.1.1": 0,
	"2.16.840.1.101.3.4.1.21": 0, "2.16.840.1.101.3.4.1.41": 0, "aes-128-ecb": 0, "aes-192-ecb": 0,
	"aes-256-ecb": 0, "aria-128-ecb": 0, "aria-192-ecb": 0, "aria-256-ecb": 0, "bf-ecb": 0,
	"camellia-128-ecb": 0, "camellia-192-ecb": 0, "camellia-256-ecb": 0, "cast5-ecb": 0, "des-ecb": 0,
	"des-ede": 0, "des-ede-ecb": 0, "des-ede3": 0, "des-ede3-ecb": 0, "des3-wrap": 0,
	"id-smime-alg-cms3deswrap": 0, "idea-ecb": 0, "rc2-ecb": 0, "rc4": 0, "rc4-40": 0, "rc4-hmac-md5": 0,
	"seed-ecb": 0, "sm4-ecb": 0,
	// IV length 4
	"2.16.840.1.101.3.4.1.28": 4, "2.16.840.1.101.3.4.1.48": 4, "2.16.840.1.101.3.4.1.8": 4,
	"aes-128-wrap-pad": 4, "aes-192-wrap-pad": 4, "aes-256-wrap-pad": 4, "aes128-wrap-pad": 4,
	"aes192-wrap-pad": 4, "aes256-wrap-pad": 4, "id-aes128-wrap-pad": 4, "id-aes192-wrap-pad": 4,
	"id-aes256-wrap-pad": 4,
	// IV length 8
	"1.2.840.113549.3.7": 8, "2.16.840.1.101.3.4.1.25": 8, "2.16.840.1.101.3.4.1.45": 8,
	"2.16.840.1.101.3.4.1.5": 8, "aes-128-wrap": 8, "aes-192-wrap": 8, "aes-256-wrap": 8, "aes128-wrap": 8,
	"aes192-wrap": 8, "aes256-wrap": 8, "bf": 8, "bf-cbc": 8, "bf-cfb": 8, "bf-ofb": 8, "blowfish": 8,
	"cast": 8, "cast-cbc": 8, "cast5-cbc": 8, "cast5-cfb": 8, "cast5-ofb": 8, "des": 8, "des-cbc": 8,
	"des-cfb": 8, "des-cfb1": 8, "des-cfb8": 8, "des-ede-cbc": 8, "des-ede-cfb": 8, "des-ede-ofb": 8,
	"des-ede3-cbc": 8, "des-ede3-cfb": 8, "des-ede3-cfb1": 8, "des-ede3-cfb8": 8, "des-ede3-ofb": 8,
	"des-ofb": 8, "des3": 8, "desx": 8, "desx-cbc": 8, "id-aes128-wrap": 8, "id-aes192-wrap": 8,
	"id-aes256-wrap": 8, "idea": 8, "idea-cbc": 8, "idea-cfb": 8, "idea-ofb": 8, "rc2": 8, "rc2-128": 8,
	"rc2-40": 8, "rc2-40-cbc": 8, "rc2-64": 8, "rc2-64-cbc": 8, "rc2-cbc": 8, "rc2-cfb": 8, "rc2-ofb": 8,
	// IV length 12
	"1.2.410.200046.1.1.34": 12, "1.2.410.200046.1.1.35": 12, "1.2.410.200046.1.1.36": 12,
	"1.2.410.200046.1.1.37": 12, "1.2.410.200046.1.1.38": 12, "1.2.410.200046.1.1.39": 12,
	"2.16.840.1.101.3.4.1.26": 12, "2.16.840.1.101.3.4.1.27": 12, "2.16.840.1.101.3.4.1.46": 12,
	"2.16.840.1.101.3.4.1.47": 12, "2.16.840.1.101.3.4.1.6": 12, "2.16.840.1.101.3.4.1.7": 12,
	"aes-128-ccm": 12, "aes-128-gcm": 12, "aes-128-ocb": 12, "aes-192-ccm": 12, "aes-192-gcm": 12,
	"aes-192-ocb": 12, "aes-256-ccm": 12, "aes-256-gcm": 12, "aes-256-ocb": 12, "aria-128-ccm": 12,
	"aria-128-gcm": 12, "aria-192-ccm": 12, "aria-192-gcm": 12, "aria-256-ccm": 12, "aria-256-gcm": 12,
	"chacha20-poly1305": 12, "id-aes128-ccm": 12, "id-aes128-gcm": 12, "id-aes192-ccm": 12, "id-aes192-gcm": 12,
	"id-aes256-ccm": 12, "id-aes256-gcm": 12,
	// IV length 16
	"0.3.4401.5.3.1.9.23": 16, "0.3.4401.5.3.1.9.24": 16, "0.3.4401.5.3.1.9.29": 16, "0.3.4401.5.3.1.9.3": 16,
	"0.3.4401.5.3.1.9.4": 16, "0.3.4401.5.3.1.9.43": 16, "0.3.4401.5.3.1.9.44": 16, "0.3.4401.5.3.1.9.49": 16,
	"0.3.4401.5.3.1.9.9": 16, "1.2.156.10197.1.104.2": 16, "1.2.156.10197.1.104.3": 16,
	"1.2.156.10197.1.104.4": 16, "1.2.156.10197.1.104.7": 16, "1.2.392.200011.61.1.1.1.2": 16,
	"1.2.392.200011.61.1.1.1.3": 16, "1.2.392.200011.61.1.1.1.4": 16, "1.2.410.200046.1.1.10": 16,
	"1.2.410.200046.1.1.12": 16, "1.2.410.200046.1.1.13": 16, "1.2.410.200046.1.1.14": 16,
	"1.2.410.200046.1.1.15": 16, "1.2.410.200046.1.1.2": 16, "1.2.410.200046.1.1.3": 16,
	"1.2.410.200046.1.1.4": 16, "1.2.410.200046.1.1.5": 16, "1.2.410.200046.1.1.7": 16,
	"1.2.410.200046.1.1.8": 16, "1.2.410.200046.1.1.9": 16, "1.3.111.2.1619.0.1.1": 16,
	"1.3.111.2.1619.0.1.2": 16, "2.16.840.1.101.3.4.1.2": 16, "2.16.840.1.101.3.4.1.22": 16,
	"2.16.840.1.101.3.4.1.23": 16, "2.16.840.1.101.3.4.1.24": 16, "2.16.840.1.101.3.4.1.3": 16,
	"2.16.840.1.101.3.4.1.4": 16, "2.16.840.1.101.3.4.1.42": 16, "2.16.840.1.101.3.4.1.43": 16,
	"2.16.840.1.101.3.4.1.44": 16, "aes-128-cbc": 16, "aes-128-cfb": 16, "aes-128-cfb1": 16, "aes-128-cfb8": 16,
	"aes-128-ctr": 16, "aes-128-ofb": 16, "aes-128-xts": 16, "aes-192-cbc": 16, "aes-192-cfb": 16,
	"aes-192-cfb1": 16, "aes-192-cfb8": 16, "aes-192-ctr": 16, "aes-192-ofb": 16, "aes-256-cbc": 16,
	"aes-256-cfb": 16, "aes-256-cfb1": 16, "aes-256-cfb8": 16, "aes-256-ctr": 16, "aes-256-ofb": 16,
	"aes-256-xts": 16, "aes128": 16, "aes192": 16, "aes256": 16, "aria-128-cbc": 16, "aria-128-cfb": 16,
	"aria-128-cfb1": 16, "aria-128-cfb8": 16, "aria-128-ctr": 16, "aria-128-ofb": 16, "aria-192-cbc": 16,
	"aria-192-cfb": 16, "aria-192-cfb1": 16, "aria-192-cfb8": 16, "aria-192-ctr": 16, "aria-192-ofb": 16,
	"aria-256-cbc": 16, "aria-256-cfb": 16, "aria-256-cfb1": 16, "aria-256-cfb8": 16, "aria-256-ctr": 16,
	"aria-256-ofb": 16, "aria128": 16, "aria192": 16, "aria256": 16, "camellia-128-cbc": 16,
	"camellia-128-cfb": 16, "camellia-128-cfb1": 16, "camellia-128-cfb8": 16, "camellia-128-ctr": 16,
	"camellia-128-ofb": 16, "camellia-192-cbc": 16, "camellia-192-cfb": 16, "camellia-192-cfb1": 16,
	"camellia-192-cfb8": 16, "camellia-192-ctr": 16, "camellia-192-ofb": 16, "camellia-256-cbc": 16,
	"camellia-256-cfb": 16, "camellia-256-cfb1": 16, "camellia-256-cfb8": 16, "camellia-256-ctr": 16,
	"camellia-256-ofb": 16, "camellia128": 16, "camellia192": 16, "camellia256": 16, "chacha20": 16, "seed": 16,
	"seed-cbc": 16, "seed-cfb": 16, "seed-ofb": 16, "sm4": 16, "sm4-cbc": 16, "sm4-cfb": 16, "sm4-cfb128": 16,
	"sm4-ctr": 16, "sm4-ofb": 16, "sm4-ofb128": 16,
}
