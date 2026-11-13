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

func TestExtractResponseTextDelegates(t *testing.T) {
	got, err := ExtractResponseText(mustParse(t, `{"output_text":"x"}`))
	if err != nil || got != "x" {
		t.Fatalf("%q %v", got, err)
	}
}
