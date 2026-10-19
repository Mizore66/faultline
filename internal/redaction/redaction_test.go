package redaction

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
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

func TestRedactValueUndefined(t *testing.T) {
	out, r := RedactValue(jsjson.Value{}, "$")
	if out.Str() != "<FAULTLINE_UNSUPPORTED_VALUE:undefined>" || len(r.Warnings) != 2 || r.Warnings[1] != "UNSUPPORTED_VALUE: non-JSON values are replaced before event capture." {
		t.Fatalf("%q %+v", out.Str(), r.Warnings)
	}
}
