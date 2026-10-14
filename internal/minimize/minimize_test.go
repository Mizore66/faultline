//go:build faultline_test && unix

package minimize

import (
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/sandbox"
)

// unitPath is the path of the patch unit id names in result.
func unitPath(result jsjson.Value, id string) string {
	for _, unit := range result.Get("patchUnits").Items() {
		if unit.Get("id").Str() == id {
			return unit.Get("path").Str()
		}
	}
	return ""
}

func TestMinimizeFindsTheBreakingFile(t *testing.T) {
	clock.ResetForTest("minimize")
	// before: state.txt=good, other.txt=a ; after: state.txt=bad, other.txt=b
	req := gitinv.TwoFileMinimizationRequestForTest(t, "pass-fail")
	res, err := MinimizeGitDiff(req, sandbox.NativeRunner(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Get("proof").Get("isProof").Bool() {
		t.Fatalf("not certified: %v", jsjson.Stringify(res.Get("proof")))
	}
	ids := res.Get("candidateUnitIds").Items()
	if len(ids) != 1 || unitPath(res, ids[0].Str()) != "state.txt" {
		t.Fatalf("minimal set %v", jsjson.Stringify(res.Get("candidateUnitIds")))
	}
	path, digest, err := WriteResult(t.TempDir()+"/r.json", res)
	if err != nil {
		t.Fatal(err)
	}
	if v := VerifyFile(path, digest, true); !v.Valid || v.ExternalDigestStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
}
