//go:build faultline_test

package repair

import "github.com/Mizore66/faultline/internal/openai"

// DecodeResponseBodyForTest runs one of fetch's response decoders
// ("gzip" or "deflate") on b, for TestLiveResponseDecoders.
func DecodeResponseBodyForTest(kind string, b []byte) ([]byte, error) {
	return openai.DecodeResponseBodyForTest(kind, b)
}
