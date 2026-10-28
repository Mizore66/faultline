//go:build faultline_test

package repair

// DecodeResponseBodyForTest runs one of fetch's response decoders
// ("gzip" or "deflate") on b, for TestLiveResponseDecoders.
func DecodeResponseBodyForTest(kind string, b []byte) ([]byte, error) {
	if kind == "gzip" {
		return gunzip(b)
	}
	return inflate(b)
}
