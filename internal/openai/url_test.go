//go:build faultline_test

package openai_test

import (
	"testing"

	"github.com/Mizore66/faultline/internal/openai"
)

func TestResponsesURLTestOverride(t *testing.T) {
	const prod = "https://api.openai.com/v1/responses"
	t.Setenv("FAULTLINE_TEST_OPENAI_URL", "")
	if got := openai.ResponsesURL(); got != prod {
		t.Fatalf("empty: %q", got)
	}
	for _, u := range []string{"http://127.0.0.1:1234/v1/responses", "http://example.test/arbitrary"} {
		t.Setenv("FAULTLINE_TEST_OPENAI_URL", u)
		if got := openai.ResponsesURL(); got != u {
			t.Fatalf("%q: %q", u, got)
		}
	}
}
