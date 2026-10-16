//go:build faultline_test

package witness

// KeyResult is what Node's createPublicKey/createPrivateKey gives for a PEM
// string: the key type and, for Ed25519, the SPKI DER; or the error message
// and opensslErrorStack (newest first).
type KeyResult struct {
	OK      bool
	Type    string
	SPKI    []byte
	Message string
	Stack   []string
}

// ParseKeyForTest is createPublicKey(text) (public) or createPrivateKey(text).
func ParseKeyForTest(text string, public bool) KeyResult {
	parse := parsePrivate
	if public {
		parse = parsePublic
	}
	key, err := parse(text)
	if err != nil {
		r := KeyResult{Message: err.Error()}
		if ke, ok := err.(*keyError); ok {
			r.Stack = ke.stack
		}
		return r
	}
	r := KeyResult{OK: true, Type: key.typ}
	if key.ed25519 {
		r.SPKI = publicKeyDER(key)
	}
	return r
}

// SetOpenSSLForTest makes the readers follow the given OpenSSL version
// ("3.5.5", "3.6.5", ...) where 3.6.5 changed them: asn1_get_length's long
// form needs a content byte after the length octets before 3.6.5, and
// ossl_dh_key_from_pkcs8's decode error reason.
func SetOpenSSLForTest(version string) {
	lengthNeedsContentByte = !versionAtLeast(version, 3, 6, 5)
	dhKeyDecodeReason = "EVP_R_DECODE_ERROR"
	if versionAtLeast(version, 3, 6, 5) {
		dhKeyDecodeReason = "DH_R_DECODE_ERROR"
	}
}

func versionAtLeast(v string, major, minor, patch int) bool {
	var a, b, c int
	n := 0
	for _, part := range []*int{&a, &b, &c} {
		for n < len(v) && v[n] >= '0' && v[n] <= '9' {
			*part = *part*10 + int(v[n]-'0')
			n++
		}
		if n < len(v) && v[n] == '.' {
			n++
		}
	}
	if a != major {
		return a > major
	}
	if b != minor {
		return b > minor
	}
	return c >= patch
}

// ReasonText is the error text OpenSSL prints for a reason name
// ("ASN1_R_WRONG_TAG" → "wrong tag"), "" for a name it does not know.
func ReasonText(name string) string {
	if r, ok := osslReasons[name]; ok {
		return r.text
	}
	if r, ok := osslCommon[name]; ok {
		return r.text
	}
	return ""
}
