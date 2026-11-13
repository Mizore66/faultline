//go:build !faultline_test

package openai_test

import (
	"testing"

	"github.com/Mizore66/faultline/internal/openai"
)

func TestResponsesURLProductionIgnoresEnv(t *testing.T) {
	t.Setenv("FAULTLINE_TEST_OPENAI_URL", "http://127.0.0.1:1/x")
	if got := openai.ResponsesURL(); got != "https://api.openai.com/v1/responses" {
		t.Fatal(got)
	}
}
