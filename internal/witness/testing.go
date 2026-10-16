//go:build faultline_test

package witness

// ParseKeyForTest is createPublicKey(text) (public) or createPrivateKey(text):
// whether it gives a key, whether that key is Ed25519 with its SPKI DER, or
// the error message.
func ParseKeyForTest(text string, public bool) (ok, ed bool, spki []byte, msg string) {
	parse := parsePrivate
	if public {
		parse = parsePublic
	}
	key, err := parse(text)
	if err != nil {
		return false, false, nil, err.Error()
	}
	if !key.ed25519 {
		return true, false, nil, ""
	}
	return true, true, publicKeyDER(key), ""
}

// SetOpenSSLForTest makes the DER decoder follow the asn1_get_length rule of
// the given OpenSSL version ("3.5.5", "3.6.5", ...).
func SetOpenSSLForTest(version string) {
	lengthNeedsContentByte = version < "3.6"
}
