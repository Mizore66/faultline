package repair

import (
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/openai"
)

// ExtractResponseText ports src/openai-response.ts:52; the neutral
// implementation lives in internal/openai.
func ExtractResponseText(response jsjson.Value) (string, error) {
	return openai.ExtractResponseText(response)
}
