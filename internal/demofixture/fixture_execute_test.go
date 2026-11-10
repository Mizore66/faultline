package demofixture

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestExecuteFixtureState(t *testing.T) {
	states := fixtureStates()
	witness := CreateFrozenWitness()
	before := jsjson.Stringify(witness)
	for _, c := range []struct {
		index                           int
		verdict, reason, stdout, stderr string
	}{
		{idxLastGood, "PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""},
		{idxFirstBad, "FAIL", "WITNESS_ASSERTION_FAILED", "", "expected settlement USD; received EUR\n"},
		{idxUnresolved, "ERROR", "FIXTURE_COMPILE_ERROR", "", "partial fixture does not compile\n"},
	} {
		record := executeFixtureState(states[c.index], witness, 0)
		if record.Get("verdict").Str() != c.verdict || record.Get("reasonCode").Str() != c.reason ||
			record.Get("stdout").Str() != c.stdout || record.Get("stderr").Str() != c.stderr {
			t.Fatalf("record %s", jsjson.Stringify(record))
		}
		if record.Get("executionKind").Str() != "EXECUTED" || record.Get("environmentDigest").Str() == digest(historicalEnvironment()) {
			t.Fatalf("execution identity %s", jsjson.Stringify(record))
		}
		if record.Get("id").Str() == "" || record.Get("executedAt").Str() == replayExecutedAt || record.Get("durationMs").Num() < 0 {
			t.Fatalf("observations %s", jsjson.Stringify(record))
		}
		left, _ := filepath.Glob(filepath.Join(os.TempDir(), "faultline-sample", states[c.index].Get("id").Str()+"-*"))
		if len(left) != 0 {
			t.Fatalf("temp sample left behind: %v", left)
		}
	}
	first := executeFixtureState(states[idxLastGood], witness, 0)
	second := executeFixtureState(states[idxLastGood], witness, 0)
	if first.Get("id").Str() == second.Get("id").Str() {
		t.Fatal("fresh executions reused an ID")
	}
	if jsjson.Stringify(witness) != before || witness.Get("command").Str() != "node witness.mjs" {
		t.Fatal("witness was modified")
	}
}
