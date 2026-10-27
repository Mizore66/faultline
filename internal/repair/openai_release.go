//go:build !faultline_test

package repair

// responsesURL is the fixed OpenAI Responses endpoint of src/repair-brief.ts:208.
// A release build has no override.
func responsesURL() string { return openAIResponsesURL }
