package repair

import (
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func mustParse(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestExtractResponseText(t *testing.T) {
	cases := []struct{ in, text, err string }{
		{`{"output":[{"content":[{"type":"output_text","text":"{}"}]}]}`, "{}", ""},
		{`{"output_text":"x"}`, "x", ""},
		{`{"output_text":"x","output":[{"content":[{"type":"output_text","text":"y"}]}]}`, "", "OpenAI response output_text fast path disagrees with raw output content"},
		{`{"output":[{"content":[{"type":"output_text","text":"a"},{"type":"output_text","text":"b"}]}]}`, "", "OpenAI response contains multiple output_text values; refusing an ambiguous structured result"},
		{`{"output":{}}`, "", "OpenAI response output must be an array when present"},
		{`{"output_text":""}`, "", "OpenAI response output_text fast path is missing non-empty text"},
		{`[]`, "", "OpenAI response must be an object"},
		{`{}`, "", "OpenAI response did not contain exactly one output_text value"},
		{`{"output":[{"content":[{"type":"output_text","text":""}]}]}`, "", "OpenAI response output_text content is missing non-empty text"},
		{`{"output":[1]}`, "", "OpenAI response output contains a non-object item"},
	}
	for _, c := range cases {
		got, err := ExtractResponseText(mustParse(t, c.in))
		if (err == nil) != (c.err == "") || (err != nil && err.Error() != c.err) || got != c.text {
			t.Errorf("%s: %q %v", c.in, got, err)
		}
	}
}
