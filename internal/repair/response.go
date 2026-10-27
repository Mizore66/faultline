package repair

import (
	"errors"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// isRecord ports src/openai-response.ts:13 isRecord: a non-null, non-array
// object.
func isRecord(v jsjson.Value) bool { return v.Kind() == jsjson.Object }

// own ports src/openai-response.ts:17 own. Every member of a parsed JSON
// object is an own property (`__proto__` included), so it is the member or
// undefined.
func own(v jsjson.Value, key string) jsjson.Value { return v.Obj().Field(key) }

// nonEmptyText ports src/openai-response.ts:21 nonEmptyText: a string of at
// least one UTF-16 unit (a lone surrogate counts).
func nonEmptyText(v jsjson.Value) (string, bool) {
	if v.Kind() == jsjson.String && v.Str() != "" {
		return v.Str(), true
	}
	return "", false
}

// Port of src/openai-response.ts:25 rawOutputTexts.
func rawOutputTexts(response jsjson.Value) ([]string, error) {
	output := own(response, "output")
	if output.Kind() == jsjson.Undefined {
		return nil, nil
	}
	if output.Kind() != jsjson.Array {
		return nil, errors.New("OpenAI response output must be an array when present")
	}
	var texts []string
	for _, item := range output.Items() {
		if !isRecord(item) {
			return nil, errors.New("OpenAI response output contains a non-object item")
		}
		content := own(item, "content")
		if content.Kind() == jsjson.Undefined {
			continue
		}
		if content.Kind() != jsjson.Array {
			return nil, errors.New("OpenAI response message content must be an array when present")
		}
		for _, part := range content.Items() {
			if !isRecord(part) {
				return nil, errors.New("OpenAI response content contains a non-object item")
			}
			if typ := own(part, "type"); typ.Kind() != jsjson.String || typ.Str() != "output_text" {
				continue
			}
			text, ok := nonEmptyText(own(part, "text"))
			if !ok {
				return nil, errors.New("OpenAI response output_text content is missing non-empty text")
			}
			texts = append(texts, text)
		}
	}
	return texts, nil
}

// ExtractResponseText ports src/openai-response.ts:52
// extractOpenAiResponseText: the one structured-output JSON string in a
// Responses API response. An error is the message TS throws.
func ExtractResponseText(response jsjson.Value) (string, error) {
	if !isRecord(response) {
		return "", errors.New("OpenAI response must be an object")
	}
	raw, err := rawOutputTexts(response)
	if err != nil {
		return "", err
	}
	if len(raw) > 1 {
		return "", errors.New("OpenAI response contains multiple output_text values; refusing an ambiguous structured result")
	}
	_, hasFastPath := response.Obj().Get("output_text")
	fastPath, haveFastPath := "", false
	if hasFastPath {
		fastPath, haveFastPath = nonEmptyText(own(response, "output_text"))
	}
	if hasFastPath && !haveFastPath {
		return "", errors.New("OpenAI response output_text fast path is missing non-empty text")
	}
	if len(raw) == 1 {
		text := raw[0]
		if haveFastPath && fastPath != text {
			return "", errors.New("OpenAI response output_text fast path disagrees with raw output content")
		}
		return text, nil
	}
	if haveFastPath {
		return fastPath, nil
	}
	return "", errors.New("OpenAI response did not contain exactly one output_text value")
}
