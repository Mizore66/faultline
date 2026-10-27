package turninv

import (
	"math"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func mustJSON(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func run(state State, attempt int, verdict, reason, executor string) jsjson.Value {
	return jsjson.MustObject(
		"stateIndex", jsjson.MakeNumber(float64(state.Index)),
		"executionAttempt", jsjson.MakeNumber(float64(attempt)),
		"executionId", jsjson.MakeString("sha256:"+strings.Repeat(string(rune('a'+state.Index)), 63)+string(rune('0'+attempt))),
		"runId", jsjson.MakeString("sha256:"+strings.Repeat("f", 63)+string(rune('0'+attempt))),
		"turnId", jsjson.MakeString(state.TurnID),
		"turnOrdinal", jsjson.MakeNumber(float64(state.TurnOrdinal)),
		"role", jsjson.MakeString(state.Role),
		"treeDigest", jsjson.MakeString(state.TreeDigest),
		"snapshotDigest", jsjson.MakeString(state.SnapshotDigest),
		"result", jsjson.MustObject("kind", jsjson.MakeString("DOCKER_ISOLATED"), "executor", jsjson.MakeString(executor),
			"verdict", jsjson.MakeString(verdict), "reason", jsjson.MakeString(reason)),
	)
}

func states() []State {
	return []State{
		{Index: 0, TurnID: SessionBaselineTurnID, TurnOrdinal: 0, Role: "SESSION_BASELINE", TreeDigest: strings.Repeat("a", 40), SnapshotDigest: "sha256:" + strings.Repeat("1", 64)},
		{Index: 1, TurnID: "turn-1", TurnOrdinal: 1, Role: "TURN", TreeDigest: strings.Repeat("b", 40), SnapshotDigest: "sha256:" + strings.Repeat("2", 64), Dirty: true},
	}
}

func TestPassToFailIsAttributed(t *testing.T) {
	s := states()
	var runs []jsjson.Value
	for a := 3; a >= 1; a-- { // unsorted on purpose
		runs = append(runs, run(s[0], a, "PASS", "PREDICATE_PASS", "NATIVE_DOCKER"), run(s[1], a, "FAIL", "PREDICATE_FAIL", "NATIVE_DOCKER"))
	}
	stable := ReconstructStable(s, runs, true)
	if len(stable) != 2 || stable[0].Get("executionIds").Items()[0].Str()[70:] != "1" {
		t.Fatalf("%v", stable)
	}
	transitions := FindTransitions(stable)
	if len(transitions) != 1 || transitions[0].Get("kind").Str() != "PASS_TO_FAIL" {
		t.Fatal(transitions)
	}
	got := jsjson.StringifyIndent(AttributeFailureIntroduction(s, transitions, stable), "")
	if got != `{"status":"ATTRIBUTED","turnOrdinal":1,"turnId":"turn-1","reason":"Earliest recorded stable PASS→FAIL occurred at turn 1."}` {
		t.Fatal(got)
	}
	if HasNonMonotonic(transitions) {
		t.Fatal("one PASS_TO_FAIL is monotonic")
	}
}

func TestLegacyExitReasonIsNotDecisive(t *testing.T) {
	s := states()
	var runs []jsjson.Value
	for a := 1; a <= 3; a++ {
		runs = append(runs, run(s[0], a, "PASS", "EXIT_ZERO", "NATIVE_DOCKER"))
	}
	if got := ReconstructStable(s, runs, false); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestInjectedRunnerNeedsNativeOnlyWhenRequired(t *testing.T) {
	s := states()
	var runs []jsjson.Value
	for a := 1; a <= 3; a++ {
		runs = append(runs, run(s[0], a, "PASS", "PREDICATE_PASS", "INJECTED_RUNNER"))
	}
	if got := ReconstructStable(s, runs, false); len(got) != 1 {
		t.Fatal(got)
	}
	if got := ReconstructStable(s, runs, true); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestStatesFromLedgerSortsByOrdinalThenIndex(t *testing.T) {
	if DuplicateTurnOrdinal([]State{{TurnOrdinal: 1}, {TurnOrdinal: 1}}) != DuplicateOrdinalError {
		t.Fatal("duplicate not reported")
	}
	if DuplicateTurnOrdinal(states()) != "" {
		t.Fatal("false duplicate")
	}
	// Turn 2's snapshot is recorded before turn 1's and the baseline last:
	// the sort orders by ordinal and keeps each state's original index.
	ledger := mustJSON(t, `{"events":[`+
		`{"event":{"type":"SESSION_STARTED","payload":{}}},`+
		turnEvent(2, "c")+`,`+turnEvent(1, "b")+`,`+
		`{"event":{"type":"TURN_COMPLETED","payload":{"turnId":"turn-9","turnOrdinal":9}}},`+
		`{"event":{"type":"SESSION_BASELINE_SNAPSHOT","payload":{"snapshot":`+snapshotJSON("a", true)+`}}}]}`)
	got, err := StatesFromLedger(ledger)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"index":2,"turnId":"session-baseline","turnOrdinal":0,"role":"SESSION_BASELINE","treeDigest":"` + strings.Repeat("a", 40) + `","snapshotDigest":"sha256:` + strings.Repeat("9", 64) + `","dirty":true},` +
		`{"index":1,"turnId":"turn-1","turnOrdinal":1,"role":"TURN","treeDigest":"` + strings.Repeat("b", 40) + `","snapshotDigest":"sha256:` + strings.Repeat("9", 64) + `","dirty":false},` +
		`{"index":0,"turnId":"turn-2","turnOrdinal":2,"role":"TURN","treeDigest":"` + strings.Repeat("c", 40) + `","snapshotDigest":"sha256:` + strings.Repeat("9", 64) + `","dirty":false}]`
	if s := jsjson.Stringify(StatesValue(got)); s != want {
		t.Fatalf("got  %s\nwant %s", s, want)
	}
	if s := jsjson.Stringify(StatesValue(StatesFromValue(StatesValue(got)))); s != want {
		t.Fatalf("StatesFromValue round trip: %s", s)
	}
}

func TestStatesFromLedgerThrowsOnBadSnapshot(t *testing.T) {
	ledger := mustJSON(t, `{"events":[{"event":{"type":"SESSION_BASELINE_SNAPSHOT","payload":{"snapshot":{"dirty":1}}}}]}`)
	_, err := StatesFromLedger(ledger)
	if err == nil || !strings.Contains(jsexc.Message(err), `"code": "invalid_literal"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestAttributionReasons(t *testing.T) {
	s := states()
	pass := stableValue(s[0], "PASS")
	fail := stableValue(s[1], "FAIL")
	p2f := jsjson.MustObject("kind", jsjson.MakeString("PASS_TO_FAIL"), "before", pass, "after", fail)
	cases := []struct {
		states      []State
		transitions []jsjson.Value
		stable      []jsjson.Value
		reason      string
	}{
		{s, nil, nil, "No PASS→FAIL transition is available to attribute."},
		{s[1:], []jsjson.Value{p2f}, nil, "No SESSION_BASELINE_SNAPSHOT; cannot claim a turn introduced the failure."},
		{s, []jsjson.Value{p2f}, []jsjson.Value{stableValue(s[0], "FAIL"), fail}, "No stable passing session baseline; cannot claim a turn introduced the failure."},
		{s, []jsjson.Value{jsjson.MustObject("kind", jsjson.MakeString("PASS_TO_FAIL"), "before", pass, "after", stableValue(State{Index: 3, TurnID: "turn-3", TurnOrdinal: 3, Role: "TURN"}, "FAIL"))}, nil,
			"The earliest PASS→FAIL transition crosses an observation gap or is not a stable adjacent pair."},
		{s, []jsjson.Value{p2f}, nil, "Earliest recorded stable PASS→FAIL occurred at turn 1."},
	}
	for i, c := range cases {
		if got := AttributeFailureIntroduction(c.states, c.transitions, c.stable).Get("reason").Str(); got != c.reason {
			t.Errorf("case %d: %s", i, got)
		}
	}
}

func stableValue(st State, verdict string) jsjson.Value {
	return jsjson.MustObject(
		"stateIndex", jsjson.MakeNumber(float64(st.Index)),
		"turnId", jsjson.MakeString(st.TurnID),
		"turnOrdinal", jsjson.MakeNumber(float64(st.TurnOrdinal)),
		"role", jsjson.MakeString(st.Role),
		"verdict", jsjson.MakeString(verdict),
	)
}

func snapshotJSON(tree string, dirty bool) string {
	d := "false"
	if dirty {
		d = "true"
	}
	return `{"schemaVersion":"faultline.turn-tree-snapshot.v1","repositoryRoot":"/r","headCommit":"` + strings.Repeat("f", 40) +
		`","treeDigest":"` + strings.Repeat(tree, 40) + `","capturedAt":"2026-07-16T10:00:00.000Z","dirty":` + d +
		`,"statusDigest":"sha256:` + strings.Repeat("e", 64) + `","digest":"sha256:` + strings.Repeat("9", 64) + `"}`
}

func turnEvent(ordinal int, tree string) string {
	n := string(rune('0' + ordinal))
	return `{"event":{"type":"TURN_TREE_SNAPSHOT","payload":{"turnId":"turn-` + n + `","turnOrdinal":` + n + `,"snapshot":` + snapshotJSON(tree, false) + `}}}`
}

// TestIntegersPastInt64 pins the review's case: schema-valid integers of
// 2^63 and beyond (1e19, 2e19) keep their value, so TS's ===/Set results
// hold (an int field saturated differently on amd64 and arm64).
func TestIntegersPastInt64(t *testing.T) {
	if DuplicateTurnOrdinal([]State{{TurnOrdinal: 1e19}, {TurnOrdinal: 2e19}}) != "" {
		t.Fatal("1e19 and 2e19 are distinct ordinals")
	}
	if DuplicateTurnOrdinal([]State{{TurnOrdinal: 0}, {TurnOrdinal: math.Copysign(0, -1)}}) != DuplicateOrdinalError {
		t.Fatal("-0 and 0 are one ordinal (SameValueZero)")
	}
	st := State{Index: 1e19, TurnID: "turn-x", TurnOrdinal: 2e19, Role: "TURN", TreeDigest: strings.Repeat("b", 40), SnapshotDigest: "sha256:" + strings.Repeat("2", 64)}
	var runs []jsjson.Value
	for a := 1; a <= 3; a++ {
		r := run(State{Index: 1, TurnID: st.TurnID, TurnOrdinal: 2e19, Role: st.Role, TreeDigest: st.TreeDigest, SnapshotDigest: st.SnapshotDigest}, a, "FAIL", "PREDICATE_FAIL", "NATIVE_DOCKER")
		r.Obj().Set("stateIndex", jsjson.MakeNumber(1e19))
		runs = append(runs, r)
	}
	stable := ReconstructStable([]State{st}, runs, true)
	if len(stable) != 1 || jsjson.Stringify(stable[0].Get("stateIndex")) != "10000000000000000000" || jsjson.Stringify(st.Value().Get("turnOrdinal")) != "20000000000000000000" {
		t.Fatalf("%v", stable)
	}
	got := StatesFromValue(mustJSON(t, `[{"index":1e19,"turnId":"t","turnOrdinal":9007199254740993,"role":"TURN","treeDigest":"x","snapshotDigest":"y","dirty":false}]`))
	if got[0].Index != 1e19 || got[0].TurnOrdinal != 9007199254740992 {
		t.Fatalf("%v", got)
	}
}
