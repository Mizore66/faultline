//go:build faultline_test

package difftest

import "testing"

// faultLastInvocation are multi-invocation writer cases whose last invocation
// alone is faulted and traced; the earlier ones run unfaulted, because the
// last names their output (repair-bundle-codex-ok investigates, then
// repairs; the stop receipt is written by the last hook of
// sidecar-hook-full-turn).
var faultLastInvocation = map[string]bool{"sidecar-hook-full-turn": true, "repair-bundle-codex-ok": true}

// faultInvocation is the invocation of tpl that receives the fault, is traced
// and is compared: the last for a faultLastInvocation case, otherwise the
// first (the others of a multi-invocation template are not faulted).
func faultInvocation(tpl writerTemplate) int {
	if faultLastInvocation[tpl.ID] {
		return len(tpl.Argv) - 1
	}
	return 0
}

// faultResultOf picks the result of the faulted invocation from all results.
func faultResultOf(tpl writerTemplate, results []writerResult) writerResult {
	return results[faultInvocation(tpl)]
}

// TestFaultInvocationSelection: an ordinary case compares its first (only
// faulted) invocation, a faultLastInvocation case its last, and the traced
// invocation is the compared one; first and final results differ here, so a
// wrong pick fails.
func TestFaultInvocationSelection(t *testing.T) {
	results := []writerResult{{Inv: 0, Stdout: "investigate"}, {Inv: 1, Stdout: "repair"}}
	ordinary := writerTemplate{ID: "prevention-write-bundle-ok", Argv: [][]string{{"a"}, {"b"}}}
	flagged := writerTemplate{ID: "repair-bundle-codex-ok", Argv: [][]string{{"a"}, {"b"}}}
	if got := faultResultOf(ordinary, results); got.Stdout != "investigate" || faultInvocation(ordinary) != 0 {
		t.Fatalf("ordinary: %+v", got)
	}
	if got := faultResultOf(flagged, results); got.Stdout != "repair" || faultInvocation(flagged) != 1 {
		t.Fatalf("flagged: %+v", got)
	}
	three := writerTemplate{ID: "repair-bundle-codex-ok", Argv: [][]string{{"a"}, {"b"}, {"c"}}}
	if faultInvocation(three) != 2 {
		t.Fatal("the last of three")
	}
}
