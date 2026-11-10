package demofixture

import (
	"path/filepath"

	"bytes"
	"github.com/Mizore66/faultline/internal/bundle/demo"
	"os"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

const (
	srcHunk      = "settlement-display-default"
	rendHunk     = "settlement-display-boundary"
	partialHunk  = "partial-renderer-hunk"
	analysisHash = "sha256:3abe08e047655ce6b42bac9e50d847a750f4d5b76d9ccacffdc986c96e6b0863"
)

func retainedAnalysis(t *testing.T) []byte {
	t.Helper()
	want, err := os.ReadFile("../../difftest/testdata/bases/demo-replay/analysis.json")
	if err != nil {
		t.Fatal(err)
	}
	return want
}

func analysisBytes(a jsjson.Value) []byte {
	return []byte(jsstr.ToUTF8(jsjson.StringifyIndent(a, "  ") + "\n"))
}

func TestCreateReplayAnalysisExact(t *testing.T) {
	want := retainedAnalysis(t)
	a := CreateReplayAnalysis()
	got := analysisBytes(a)
	if !bytes.Equal(got, want) {
		t.Fatalf("analysis bytes differ\nwant=%s\ngot=%s", want, got)
	}
	digest, err := canonical.DigestJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	if digest != analysisHash {
		t.Fatal(digest)
	}
	if len(a.Get("runCatalog").Items()) != 20 {
		t.Fatal("catalog count")
	}
	for _, r := range a.Get("runCatalog").Items() {
		if r.Get("executionKind").Str() != "CACHED" || r.Get("durationMs").Num() != 0 ||
			r.Get("executedAt").Str() != "2026-07-14T09:05:00.000Z" {
			t.Fatal(jsjson.Stringify(r))
		}
	}
}

func TestReplayRunOutcomes(t *testing.T) {
	w := CreateFrozenWitness()
	cases := []struct {
		name            string
		hunks           []string
		verdict, reason string
		stdout, stderr  string
	}{
		{"empty", nil, "PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""},
		{"source only", []string{srcHunk}, "PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""},
		{"renderer only", []string{rendHunk}, "PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""},
		{"combined", []string{srcHunk, rendHunk}, "FAIL", "WITNESS_ASSERTION_FAILED", "", "expected settlement USD; received EUR\n"},
		{"combined reversed", []string{rendHunk, srcHunk}, "FAIL", "WITNESS_ASSERTION_FAILED", "", "expected settlement USD; received EUR\n"},
		{"partial", []string{partialHunk}, "ERROR", "FIXTURE_COMPILE_ERROR", "", "synthetic partial hunk does not compile\n"},
		{"partial wins over combined", []string{srcHunk, rendHunk, partialHunk}, "ERROR", "FIXTURE_COMPILE_ERROR", "", "synthetic partial hunk does not compile\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := makeState("probe", "contrib", "Session", 1, c.hunks, "Probe")
			r := replayRun(s, w)
			if r.Get("verdict").Str() != c.verdict || r.Get("reasonCode").Str() != c.reason ||
				r.Get("stdout").Str() != c.stdout || r.Get("stderr").Str() != c.stderr {
				t.Fatalf("%s", jsjson.Stringify(r))
			}
			if r.Get("stateId").Str() != "probe" || r.Get("witnessDigest").Str() != frozenWitnessDigest ||
				r.Get("executionKind").Str() != "CACHED" || r.Get("durationMs").Num() != 0 {
				t.Fatalf("%s", jsjson.Stringify(r))
			}
		})
	}
}

func TestUniqueRunCatalog(t *testing.T) {
	rec := func(id, note string) jsjson.Value {
		return jsjson.MustObject("id", jsjson.MakeString(id), "note", jsjson.MakeString(note))
	}
	in := []jsjson.Value{rec("b", "1"), rec("a", "1"), rec("B", "1"), rec("A", "1"), rec("b", "1"), rec("a", "1")}
	out := uniqueRunCatalog(in)
	var ids []string
	for _, r := range out {
		ids = append(ids, r.Get("id").Str())
	}
	// ICU en-US collation: lower before upper within a letter, not code-point order.
	if strings.Join(ids, ",") != "a,A,b,B" {
		t.Fatalf("order %v", ids)
	}

	err := jsexc.Try(func() { uniqueRunCatalog([]jsjson.Value{rec("x", "1"), rec("x", "2")}) })
	if err == nil || err.Error() != "Run id collision with conflicting evidence: x" {
		t.Fatalf("collision error = %v", err)
	}
}

func TestReplayAnalysisFreshValues(t *testing.T) {
	want := retainedAnalysis(t)
	first := CreateReplayAnalysis()
	first.Get("runCatalog").Items()[0].Obj().Set("verdict", jsjson.MakeString("MUTATED"))
	first.Get("witness").Obj().Set("behavior", jsjson.MakeString("MUTATED"))
	first.Get("claims").Items()[0].Obj().Set("statement", jsjson.MakeString("MUTATED"))
	w := CreateFrozenWitness()
	w.Obj().Set("id", jsjson.MakeString("MUTATED"))
	if got := analysisBytes(CreateReplayAnalysis()); !bytes.Equal(got, want) {
		t.Fatalf("later analysis not fresh\n%s", got)
	}
	if CreateFrozenWitness().Get("id").Str() != "refund-settlement-currency" {
		t.Fatal("witness shared")
	}
}

func TestReplayAnalysisSemantics(t *testing.T) {
	a := CreateReplayAnalysis()
	m := a.Get("metrics")
	if m.Get("sessions").Num() != 4 || m.Get("turns").Num() != 8 || m.Get("files").Num() != 2 ||
		m.Get("changedLines").Num() != 12 || m.Get("implicatedHunks").Num() != 2 {
		t.Fatal(jsjson.Stringify(m))
	}
	for _, tr := range a.Get("transitions").Items() {
		if tr.Get("stable").Bool() {
			t.Fatal("cached transition marked stable")
		}
	}
	if a.Get("minimization", "termination").Str() != "NOT_EXECUTED" || a.Get("prevention", "verified").Bool() ||
		a.Get("grade", "value").Str() != "D" {
		t.Fatal("cached replay overstated")
	}
	claims := a.Get("claims").Items()
	kinds := []string{"UNKNOWN", "UNKNOWN", "UNKNOWN", "INFERRED", "UNKNOWN"}
	for i, k := range kinds {
		if claims[i].Get("kind").Str() != k {
			t.Fatalf("claim %d kind %s", i, claims[i].Get("kind").Str())
		}
	}
	if claims[3].Get("evidenceIds").Items()[0].Str() != a.Get("prevention", "firstBad", "id").Str() {
		t.Fatal("inferred claim must cite first-bad run")
	}
}

func TestCreateRerunAnalysis(t *testing.T) {
	replayBefore := jsjson.Stringify(CreateReplayAnalysis())
	analysis := CreateRerunAnalysis()
	if analysis.Get("mode").Str() != "RERUN" || analysis.Get("grade", "value").Str() != "A" {
		t.Fatal("RERUN grade/mode")
	}
	if analysis.Get("minimization", "termination").Str() != "BIDIRECTIONALLY_VALIDATED" || !analysis.Get("prevention", "verified").Bool() {
		t.Fatal("validation not complete")
	}
	if analysis.Get("generatedAt").Str() == replayExecutedAt {
		t.Fatal("stale generatedAt")
	}
	if analysis.Get("witness", "command").Str() != "built-in-go-sample" {
		t.Fatal("descriptor claims Node")
	}
	passToFail := 0
	for _, tr := range analysis.Get("transitions").Items() {
		if tr.Get("kind").Str() != "PASS_TO_FAIL" {
			continue
		}
		passToFail++
		if !tr.Get("stable").Bool() || len(tr.Get("boundaryRunIds").Items()) != 6 {
			t.Fatalf("unstable transition %s", jsjson.Stringify(tr))
		}
	}
	if passToFail == 0 {
		t.Fatal("no PASS_TO_FAIL transition")
	}
	if analysis.Get("warning").Str() != "Executed built-in sample. The runner only materializes the reviewed fixture; it is not a general-purpose sandbox." {
		t.Fatal("wrong warning")
	}
	seen := map[string]bool{}
	envDigest := digest(executedEnvironment())
	for _, r := range analysis.Get("runCatalog").Items() {
		id := r.Get("id").Str()
		if id == "" || seen[id] || r.Get("executionKind").Str() != "EXECUTED" || r.Get("environmentDigest").Str() != envDigest {
			t.Fatalf("catalog record %s", jsjson.Stringify(r))
		}
		seen[id] = true
	}
	claims := analysis.Get("claims").Items()
	for i, k := range []string{"EXECUTED", "DERIVED", "DERIVED"} {
		ev := claims[i].Get("evidenceIds").Items()
		if claims[i].Get("kind").Str() != k || len(ev) == 0 {
			t.Fatalf("claim %d: %s", i, jsjson.Stringify(claims[i]))
		}
		for _, e := range ev {
			if i > 0 && !seen[e.Str()] {
				t.Fatalf("claim %d evidence %s not in catalog", i, e.Str())
			}
		}
	}
	for _, f := range []string{"native", "nativeRuns", "receipts", "executionTrust", "nativeProof"} {
		if _, ok := analysis.Obj().Get(f); ok {
			t.Fatalf("native field %s", f)
		}
	}
	if jsjson.Stringify(CreateReplayAnalysis()) != replayBefore {
		t.Fatal("replay changed")
	}
}

func TestRerunAnalysisBundleVerifies(t *testing.T) {
	analysis := CreateRerunAnalysis()
	root := t.TempDir()
	dir := filepath.Join(root, "bundle")
	written, err := demo.Write(dir, analysis, demo.WriteOptions{ProofRoot: &root})
	if err != nil {
		t.Fatal(err)
	}
	result := demo.Verify(dir, written.RootDigest, true)
	if !result.Valid || len(result.Errors) != 0 || result.RootDigest == nil || *result.RootDigest != written.RootDigest {
		t.Fatalf("verify failed: %+v", result)
	}
	w := analysis.Get("witness")
	body := jsjson.MustObject()
	for _, k := range w.Obj().Keys() {
		if k != "digest" {
			body.Obj().Set(k, w.Get(k))
		}
	}
	d, err := canonical.DigestJSON(body)
	if err != nil || d != w.Get("digest").Str() {
		t.Fatalf("witness digest stale: %s %v", d, err)
	}
}
