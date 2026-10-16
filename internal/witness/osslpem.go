package witness

import (
	"bytes"
	"runtime"
	"strings"
)

// The PEM layer of OpenSSL 3.5 (crypto/pem/pem_lib.c, crypto/evp/names.c)
// with the errors each step raises. The line reading itself is in
// pemread.go (sanitizeLine, pemBIO.gets).

// pemBlock is what PEM_read_bio_ex returns: name and header are C strings.
type pemBlock struct {
	name   string
	header string
	data   []byte
}

// readBlockQ ports PEM_read_bio_ex: the next block, or false when none can
// be read, with the error raised (the lines it consumed stay consumed).
func (e *osslEnv) readBlock(b *pemBIO) (pemBlock, bool) {
	name, ok := getName(b)
	if !ok {
		e.q.raise(libPEM, "PEM_R_NO_START_LINE")
		return pemBlock{}, false
	}
	header, data, reason := getHeaderAndData(b, name)
	if reason == "unspecified" {
		return pemBlock{}, false
	}
	if reason != "" {
		e.q.raise(libPEM, reason)
		return pemBlock{}, false
	}
	if len(data) == 0 {
		return pemBlock{}, false
	}
	der, ok := evpDecode(data)
	if !ok {
		e.q.raise(libPEM, "PEM_R_BAD_BASE64_DECODE")
		return pemBlock{}, false
	}
	// No decoded bytes: PEM_MALLOC(0) is NULL, which fails the read
	// without an error.
	if len(der) == 0 {
		return pemBlock{}, false
	}
	return pemBlock{name: string(name), header: string(header), data: der}, true
}

// Port of pem_lib.c get_name: skip lines until "-----BEGIN <name>-----".
// The name is a C string, so a NUL inside it ends it.
func getName(b *pemBIO) ([]byte, bool) {
	first := true
	for {
		line := b.gets()
		if len(line) == 0 {
			return nil, false
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
// (then all of them are header and the data is empty). The reason is the
// PEM_R_* raised, "unspecified" for the 65-column failure (no error).
func getHeaderAndData(b *pemBIO, name []byte) (header, data []byte, reason string) {
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
			return nil, nil, "PEM_R_BAD_END_LINE"
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
					return nil, nil, "PEM_R_BAD_END_LINE" // a second blank line
				}
				state = postHeader
				tmp = &dat
			}
			continue
		}
		if rest, isEnd := bytes.CutPrefix(s, []byte("-----END ")); isEnd {
			if !bytes.HasPrefix(rest, name) || !bytes.HasPrefix(rest[len(name):], []byte("-----\n")) {
				return nil, nil, "PEM_R_BAD_END_LINE"
			}
			if state == maybeHeader {
				return nil, hdr, ""
			}
			return hdr, dat, ""
		} else if end {
			return nil, nil, "PEM_R_BAD_END_LINE" // a short line not at the end of the data
		}
		*tmp = append(*tmp, cstr(s)...) // BIO_puts
		if state == postHeader {
			// The 64-column check of encrypted (headed) data.
			if len(s) > 65 {
				return nil, nil, "unspecified"
			}
			if len(s) < 65 {
				end = true
			}
		}
	}
}

// cipherByName ports evp/names.c evp_get_cipherbyname_ex: the IV length,
// false if none. A name the namemap does not know triggers a fetch whose
// "unsupported" error is popped again.
func (e *osslEnv) cipherByName(name string) (int, bool) {
	lower := asciiLower(name)
	if iv, ok := pemCipherIVLength[lower]; ok {
		return iv, true
	}
	// The AES-NI stitched CBC-HMAC ciphers are registered only where the
	// CPU can run them (x86 with AES-NI); elsewhere the name is unknown
	// and the fetch below fails.
	if iv, ok := pemCipherIVLengthX86[lower]; ok && runtime.GOARCH == "amd64" {
		return iv, true
	}
	if !pemKnownNonCiphers[lower] {
		e.q.setMark()
		e.q.raise(libEVP, "ERR_R_UNSUPPORTED")
		e.q.popToMark()
	}
	return 0, false
}

// Port of pem_lib.c PEM_get_EVP_CIPHER_INFO: whether the header names a
// cipher; false on failure (with the reason raised, if any).
func (e *osslEnv) cipherInfo(h string) (cipher bool, ok bool) {
	if h == "" || h[0] == '\n' {
		return false, true
	}
	fail := func(reason string) (bool, bool) {
		if reason != "" {
			e.q.raise(libPEM, reason)
		}
		return false, false
	}
	h, found := strings.CutPrefix(h, "Proc-Type:")
	if !found {
		return fail("PEM_R_NOT_PROC_TYPE")
	}
	h = h[strspn(h, " \t"):]
	if at(h, 0) != '4' || at(h, 1) != ',' {
		return fail("")
	}
	h = h[2:]
	h = h[strspn(h, " \t"):]
	h, found = strings.CutPrefix(h, "ENCRYPTED")
	if !found || strspn(h, " \t\r\n") == 0 {
		return fail("PEM_R_NOT_ENCRYPTED")
	}
	h = h[strspn(h, " \t\r"):]
	if at(h, 0) != '\n' {
		return fail("PEM_R_SHORT_HEADER")
	}
	h, found = strings.CutPrefix(h[1:], "DEK-Info:")
	if !found {
		return fail("PEM_R_NOT_DEK_INFO")
	}
	h = h[strspn(h, " \t"):]
	name := h[:strcspn(h, " \t,")]
	h = h[len(name):]
	h = h[strspn(h, " \t"):]
	ivlen, known := e.cipherByName(name)
	if !known {
		return fail("PEM_R_UNSUPPORTED_ENCRYPTION")
	}
	if ivlen > 0 {
		if at(h, 0) != ',' {
			return fail("PEM_R_MISSING_DEK_IV")
		}
		h = h[1:]
	} else if at(h, 0) == ',' {
		return fail("PEM_R_UNEXPECTED_DEK_IV")
	}
	for i := 0; i < 2*ivlen; i++ {
		if !isHex(at(h, i)) {
			return fail("PEM_R_BAD_IV_CHARS")
		}
	}
	return true, true
}

// checkPEM ports pem_lib.c check_pem for the names Node asks for.
func checkPEM(nm, name string) bool {
	if nm == name {
		return true
	}
	switch name {
	case "ANY PRIVATE KEY":
		return anyPrivateKey(nm)
	case "CERTIFICATE":
		return nm == "X509 CERTIFICATE"
	}
	return false
}

// bytesRead ports pem_lib.c pem_bytes_read_bio_flags: the first block whose
// name matches (a block that cannot be read ends the search), refused if
// its header does not parse or names a cipher. pw is the passphrase
// callback: nil is PEM_def_callback, which with no terminal fails as
// UI_process does ("processing error" then "problems getting password");
// legacy is ossl_pw_pem_password, which raises "interrupted or cancelled".
func (e *osslEnv) bytesRead(b *pemBIO, name string, pw func()) (pemBlock, bool) {
	for {
		blk, ok := e.readBlock(b)
		if !ok {
			return pemBlock{}, false
		}
		if !checkPEM(blk.name, name) {
			continue
		}
		cipher, ok := e.cipherInfo(blk.header)
		if !ok {
			return pemBlock{}, false
		}
		if cipher {
			// PEM_do_header: the callback fails, then PEM_R_BAD_PASSWORD_READ.
			pw()
			e.q.raise(libPEM, "PEM_R_BAD_PASSWORD_READ")
			return pemBlock{}, false
		}
		return blk, true
	}
}

// defCallback is PEM_def_callback with no terminal and stdin on
// /dev/null: EVP_read_pw_string_min fails in UI_process.
func (e *osslEnv) defCallback() {
	e.q.raise(40, "UI_R_PROCESSING_ERROR")
	e.q.raise(libPEM, "PEM_R_PROBLEMS_GETTING_PASSWORD")
}

// pwCallback is ossl_pw_pem_password over Node's PasswordCallback (no
// passphrase): do_ui_passphrase's UI_process returns -2.
func (e *osslEnv) pwCallback() {
	e.q.raise(libCRYPTO, "ERR_R_INTERRUPTED_OR_CANCELLED")
}
