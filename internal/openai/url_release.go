//go:build !faultline_test

package openai

// ResponsesURL is the fixed OpenAI Responses endpoint of src/repair-brief.ts:208.
// A release build has no override.
func ResponsesURL() string { return responsesEndpoint }
