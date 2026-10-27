//go:build faultline_test

package repair

import "os"

// responsesURL is src/repair-brief.ts:208's endpoint, or the test server
// named by FAULTLINE_TEST_OPENAI_URL when that is set (the Go side of
// difftest/gen/fetch-shim.mjs). Only faultline_test builds hold this name.
func responsesURL() string {
	if u := os.Getenv("FAULTLINE_TEST_OPENAI_URL"); u != "" {
		return u
	}
	return openAIResponsesURL
}
