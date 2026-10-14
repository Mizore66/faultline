//go:build faultline_test

package sandbox

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestDockerArgvAndClassification(t *testing.T) {
	clock.ResetForTest("sandbox")
	src := t.TempDir()
	req, _ := jsjson.Parse(`{"mode":"DOCKER_ISOLATED","sourceDirectory":` + jsjson.Quote(src) + `,
		"image":"node@sha256:` + strings.Repeat("a", 64) + `",
		"witness":{"digest":"sha256:` + strings.Repeat("b", 64) + `","command":"node witness.mjs"}}`)
	p, err := CreatePlan(req)
	if err != nil {
		t.Fatal(err)
	}
	// The brief wrote "args"; the TS plan key is "arguments".
	args := p.Value.Get("arguments").Items()
	if args[0].Str() != "run" || args[1].Str() != "--rm" || args[2].Str() != "--name" || !strings.HasPrefix(args[3].Str(), "faultline-") || len(args[3].Str()) != 42 {
		t.Fatalf("%v", args[:4])
	}
	code := 1
	got := Classify(p, RunResult{ExitCode: &code, Stdout: "state=bad\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n"})
	if got.Get("verdict").Str() != "FAIL" || got.Get("reason").Str() == "" {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
}

func TestTruncateOutputReplacesCutCodePoint(t *testing.T) {
	out, errOut, cut := TruncateOutput([]byte("ab\xe2\x82\xac"), []byte("zz"), 3)
	if out != "ab�" || errOut != "" || !cut {
		t.Fatalf("%q %q %v", out, errOut, cut)
	}
}
