package demofixture

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestExecuteFixtureState(t *testing.T) {
	states := fixtureStates()
	witness := CreateFrozenWitness()
	before := jsjson.Stringify(witness)
	root := t.TempDir()
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
		record = executeFixtureStateWith(states[c.index], witness, 0, root, evaluateBuiltInSample, fixtureExecutionTimeout)
		if record.Get("verdict").Str() != c.verdict {
			t.Fatalf("injected root record %s", jsjson.Stringify(record))
		}
		entries, err := os.ReadDir(filepath.Join(root, "faultline-sample"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("per-execution directory left behind: %v %v", entries, err)
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

func TestFixtureRunIDsDistinctForIdenticalObservations(t *testing.T) {
	witness := CreateFrozenWitness()
	started := time.Unix(1784203200, 0)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := fixtureRunID("s1", witness.Get("digest"), 0, started, "out\n", "")
		if seen[id] {
			t.Fatalf("duplicate run id %s at execution %d", id, i)
		}
		seen[id] = true
	}
}

func TestExecuteFixtureStateTimeout(t *testing.T) {
	states := fixtureStates()
	witness := CreateFrozenWitness()
	root := t.TempDir()
	release := make(chan struct{})
	finished := make(chan struct{})
	start := time.Now()
	record := executeFixtureStateWith(states[idxLastGood], witness, 0, root,
		func(map[string]bool) builtInOutcome {
			defer close(finished)
			<-release
			return builtInOutcome{"PASS", "WITNESS_SATISFIED", "late\n", ""}
		}, fixtureExecutionTimeout)
	elapsed := time.Since(start)
	close(release)
	<-finished
	if record.Get("verdict").Str() != "ERROR" || record.Get("reasonCode").Str() != "RUNNER_ERROR" ||
		record.Get("stderr").Str() == "" || record.Get("stdout").Str() != "" {
		t.Fatalf("timeout record %s", jsjson.Stringify(record))
	}
	if elapsed < fixtureExecutionTimeout || elapsed > fixtureExecutionTimeout+time.Second {
		t.Fatalf("timeout elapsed %s", elapsed)
	}
	entries, err := os.ReadDir(filepath.Join(root, "faultline-sample"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("directory left behind: %v %v", entries, err)
	}
}
