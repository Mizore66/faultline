package demofixture

import (
	"testing"
	"time"
)

// These dependencies reconstruct synthetic reference observations, not live runs.
type nativeContractCase struct {
	ID             string
	Approval       NativeApproval
	Deps           nativeDeps
	Expected       string
	AssertConsumed func(*testing.T)
}

func nativeContractCases(t *testing.T) []nativeContractCase {
	t.Helper()
	var cases []nativeContractCase
	for _, row := range nativeContractRows(t) {
		input := row.Get("input")
		e := &nativeScriptExecutor{t: t, environment: input.Get("environmentPreimage"), script: input.Get("captures").Items()}
		for _, capture := range e.script {
			e.captures = append(e.captures, nativeContractCapture(t, capture))
		}
		reads, factories, nonces := 0, 0, 0
		times := []time.Time{nativeContractTime(t, input.Get("startedAt").Str()), nativeContractTime(t, input.Get("generatedAt").Str())}
		cases = append(cases, nativeContractCase{
			ID: row.Get("id").Str(), Approval: nativeApprovalFromInput(input), Expected: row.Get("expected").Str(),
			Deps: nativeDeps{
				newExecutor: func() (nativeExecutor, error) { factories++; return e, nil },
				nonce:       func() string { nonces++; return input.Get("nonce").Str() },
				now: func() time.Time {
					if reads >= len(times) {
						t.Fatal("extra wall-time dependency call beyond entry and final timestamp")
					}
					v := times[reads]
					reads++
					return v
				},
			},
			AssertConsumed: func(t *testing.T) {
				t.Helper()
				if e.cursor != 39 || e.cursor != len(e.script) || reads != 2 || factories != 1 || nonces != 1 {
					t.Fatalf("transcript/dependencies not exactly consumed: runs=%d/%d time=%d factory=%d nonce=%d", e.cursor, len(e.script), reads, factories, nonces)
				}
			},
		})
	}
	return cases
}
