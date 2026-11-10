package demofixture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

const fixtureExecutionTimeout = 2 * time.Second

type builtInOutcome struct{ verdict, reason, stdout, stderr string }

// evaluateBuiltInSample is the pure, reviewed sample model. It never reads
// input repositories, the network, credentials, or external executables.
func evaluateBuiltInSample(hunks map[string]bool) builtInOutcome {
	if hunks[unresolvedHunk] {
		return builtInOutcome{"ERROR", "FIXTURE_COMPILE_ERROR", "", "partial fixture does not compile\n"}
	}
	if hunks[sourceHunk] && hunks[rendererHunk] {
		return builtInOutcome{"FAIL", "WITNESS_ASSERTION_FAILED", "", "expected settlement USD; received EUR\n"}
	}
	return builtInOutcome{"PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""}
}

func executedEnvironment() jsjson.Value {
	return jsjson.MustObject(
		"runtime", jsjson.MakeString(runtime.Version()),
		"packageManager", jsjson.MakeString("none"),
		"runner", jsjson.MakeString("built-in-sample-v1"),
		"network", jsjson.MakeString("disabled"),
		"credentials", jsjson.MakeString("redacted"))
}

// executeFixtureState runs the built-in evaluator once and returns an EXECUTED
// record with actual observations. The witness is read, never modified.
func executeFixtureState(input, witness jsjson.Value, executionAttempt int) jsjson.Value {
	timer := clock.StartEvidenceTimer()
	started := clock.Now()
	stateID := input.Get("id").Str()

	var outcome builtInOutcome
	sampleDir := filepath.Join(os.TempDir(), "faultline-sample",
		fmt.Sprintf("%s-%d-%d-%s", stateID, os.Getpid(), clock.NowMillis(), clock.TempSuffix()))
	if err := os.MkdirAll(sampleDir, 0o700); err != nil {
		outcome = builtInOutcome{"ERROR", "RUNNER_ERROR", "", "sample workspace unavailable: " + err.Error() + "\n"}
	} else {
		defer func() {
			_ = os.RemoveAll(sampleDir)
			_ = os.Remove(filepath.Dir(sampleDir)) // succeeds only when empty
		}()
		hunks := make(map[string]bool)
		for _, h := range input.Get("hunks").Items() {
			hunks[h.Str()] = true
		}
		ctx, cancel := context.WithTimeout(context.Background(), fixtureExecutionTimeout)
		defer cancel()
		done := make(chan builtInOutcome, 1)
		go func() { done <- evaluateBuiltInSample(hunks) }()
		select {
		case outcome = <-done:
		case <-ctx.Done():
			outcome = builtInOutcome{"ERROR", "RUNNER_ERROR", "", "built-in sample exceeded 2 second timeout\n"}
		}
	}

	id := digest(jsjson.MustObject(
		"state", jsjson.MakeString(stateID),
		"witness", witness.Get("digest"),
		"executionAttempt", jsjson.MakeNumber(float64(executionAttempt)),
		"started", jsjson.MakeString(strconv.FormatInt(started.UnixNano(), 10)),
		"stdout", jsjson.MakeString(outcome.stdout),
		"stderr", jsjson.MakeString(outcome.stderr)))
	return jsjson.MustObject(
		"id", jsjson.MakeString(id),
		"stateId", jsjson.MakeString(stateID),
		"witnessDigest", witness.Get("digest"),
		"environmentDigest", jsjson.MakeString(digest(executedEnvironment())),
		"verdict", jsjson.MakeString(outcome.verdict),
		"reasonCode", jsjson.MakeString(outcome.reason),
		"stdout", jsjson.MakeString(outcome.stdout),
		"stderr", jsjson.MakeString(outcome.stderr),
		"durationMs", jsjson.MakeNumber(float64(timer.Milliseconds())),
		"executionKind", jsjson.MakeString("EXECUTED"),
		"executedAt", jsjson.MakeString(clock.ISO(started)))
}
