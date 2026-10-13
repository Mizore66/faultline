package redaction

import (
	"strings"
	"testing"
)

func TestRedactTextOpenAIKey(t *testing.T) {
	out, r := RedactText("key sk-proj-abcdefghijklmnop1234 end", "$.run.stdout")
	if !r.Redacted || r.Count != 1 || r.Kinds[0] != "OPENAI_API_KEY" {
		t.Fatalf("%+v", r)
	}
	if !strings.HasPrefix(out, "key <FAULTLINE_REDACTED:OPENAI_API_KEY:sha256:") || !strings.HasSuffix(out, "> end") {
		t.Fatalf("%q", out)
	}
}
