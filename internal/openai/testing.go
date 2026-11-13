//go:build faultline_test

package openai

// DecodeResponseBodyForTest runs one of fetch's response decoders
// ("gzip" or "deflate") on b.
func DecodeResponseBodyForTest(kind string, b []byte) ([]byte, error) {
	if kind == "gzip" {
		return gunzip(b)
	}
	return inflate(b)
}
