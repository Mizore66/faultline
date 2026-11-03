package demofixture

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nativefixture"
)

// Changing any call order, attempt, identity, copy, field order, or envelope
// byte must fail this complete independent result comparison.
func TestNativeAnalysisFullContract(t *testing.T) {
	for _, tc := range nativeContractCases(t) {
		t.Run(tc.ID, func(t *testing.T) {
			got, err := runNativeAnalysis(context.Background(), tc.Approval, tc.Deps)
			if err != nil {
				t.Fatal(err)
			}
			actual := jsjson.MustObject("analysis", got.Analysis, "provenance", got.Provenance)
			encoded := []byte(jsstr.ToUTF8(jsjson.StringifyIndent(actual, "  ") + "\n"))
			if !bytes.Equal(encoded, []byte(tc.Expected)) {
				t.Fatalf("complete native result differs\nwant=%s\ngot=%s", tc.Expected, encoded)
			}
			tc.AssertConsumed(t)
		})
	}
}

func TestNativeAnalysisConstructionFailures(t *testing.T) {
	row := nativeContractRows(t)[0]
	input := row.Get("input")
	cause := errors.New("construction failure")
	for _, mode := range []string{"nil-context", "cancelled", "deadline", "approval", "factory", "nil-executor", "missing-factory", "missing-nonce", "missing-clock", "clock-panic", "nonce-panic", "empty-nonce", "environment-panic", "environment-undefined"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			approval := nativeApprovalFromInput(input)
			e := &nativeWitnessTestExecutor{environment: input.Get("environmentPreimage")}
			factories, nonces, reads := 0, 0, 0
			deps := nativeDeps{
				newExecutor: func() (nativeExecutor, error) { factories++; return e, nil },
				nonce:       func() string { nonces++; return input.Get("nonce").Str() },
				now:         func() time.Time { reads++; return nativeContractTime(t, input.Get("startedAt").Str()) },
			}
			kind, wantFactories, wantNonces := "ENVIRONMENT", 0, 0
			var wantCause error
			switch mode {
			case "nil-context":
				ctx, kind = nil, "CANCELLED"
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
				kind, wantCause = "CANCELLED", context.Canceled
			case "deadline":
				c, cancel := context.WithDeadline(ctx, time.Unix(1, 0))
				defer cancel()
				ctx = c
				kind, wantCause = "DEADLINE", context.DeadlineExceeded
			case "approval":
				approval = NativeApproval{}
				kind = "APPROVAL"
			case "factory":
				deps.newExecutor = func() (nativeExecutor, error) { factories++; return nil, cause }
				wantFactories, wantCause = 1, cause
			case "nil-executor":
				deps.newExecutor = func() (nativeExecutor, error) { factories++; return nil, nil }
				wantFactories = 1
			case "missing-factory":
				deps.newExecutor = nil
			case "missing-nonce":
				deps.nonce = nil
			case "missing-clock":
				deps.now = nil
			case "clock-panic":
				deps.now = func() time.Time { reads++; panic(cause) }
				kind, wantCause = "PANIC", cause
			case "nonce-panic":
				deps.nonce = func() string { nonces++; panic(cause) }
				kind, wantFactories, wantNonces, wantCause = "PANIC", 1, 1, cause
			case "empty-nonce":
				deps.nonce = func() string { nonces++; return "" }
				wantFactories, wantNonces = 1, 1
			case "environment-panic":
				deps.newExecutor = func() (nativeExecutor, error) {
					factories++
					return &nativeConstructionTestExecutor{panicValue: cause}, nil
				}
				kind, wantFactories, wantCause = "PANIC", 1, cause
			case "environment-undefined":
				e.environment = jsjson.Value{}
				wantFactories = 1
			}
			got, err := runNativeAnalysis(ctx, approval, deps)
			var typed *NativeAnalysisError
			if got.Analysis.Kind() != jsjson.Undefined || !errors.As(err, &typed) || typed.Kind != kind || typed.Ordinal != 0 || typed.StateID != "" {
				t.Fatalf("construction returned analysis or wrong error: %v analysis=%s", err, jsjson.Stringify(got.Analysis))
			}
			if wantCause != nil && !errors.Is(err, wantCause) {
				t.Fatalf("lost cause: %v", err)
			}
			if factories != wantFactories || nonces != wantNonces || e.runs != 0 {
				t.Fatalf("unexpected dependency execution: factory=%d nonce=%d runs=%d", factories, nonces, e.runs)
			}
			p := got.Provenance
			if p.Get("status").Str() != "ERROR" || p.Get("invocations").Kind() != jsjson.Array || len(p.Get("invocations").Items()) != 0 || p.Get("nonce").Kind() != jsjson.Null {
				t.Fatal("missing failure envelope or fabricated execution")
			}
			requireNativeValue(t, "known proposal", p.Get("proposal"), input.Get("proposal"))
			requireNativeValue(t, "caller approval", p.Get("approval"), jsjson.MustObject("proposalDigest", jsjson.MakeString(approval.ProposalDigest), "approvedAt", jsjson.MakeString(approval.ApprovedAt)))
			if mode == "nonce-panic" || mode == "empty-nonce" {
				requireNativeValue(t, "available environment before nonce failure", p.Get("environmentPreimage"), input.Get("environmentPreimage"))
			} else if p.Get("environmentPreimage").Kind() != jsjson.Null || p.Get("environmentDigest").Kind() != jsjson.Null {
				t.Fatal("fabricated environment")
			}
			if mode == "missing-clock" || mode == "clock-panic" {
				if p.Get("generatedAt").Kind() != jsjson.Null {
					t.Fatal("fabricated final time")
				}
			} else if p.Get("generatedAt").Str() != input.Get("startedAt").Str() || reads != 2 {
				t.Fatalf("failure exit did not retain final observed time: reads=%d", reads)
			}
		})
	}
}

// A valid but wrong predicate transcript must fail proof, not earn grade A.
func TestNativeAnalysisStopsOnFailure(t *testing.T) {
	row := nativeContractRows(t)[0]
	input := row.Get("input")
	pass := nativeContractCapture(t, input.Get("captures").Items()[0])
	fail := nativeContractCapture(t, input.Get("captures").Items()[2])
	for _, tc := range []struct {
		name, kind string
		ordinal    int
		verdict    string
	}{
		{"contribution-protocol", "PROTOCOL", 1, "invalid"},
		{"contribution-panic", "PANIC", 2, "panic"},
		{"timeline-cancel", "CANCELLED", 8, "cancel"},
		{"unstable-before", "PROOF", 13, "FAIL"},
		{"unstable-after", "PROOF", 16, "PASS"},
		{"source-necessity", "PROOF", 31, "FAIL"},
		{"renderer-necessity", "PROOF", 32, "FAIL"},
		{"partial-unexpected-pass", "PROTOCOL", 33, "PASS"},
		{"insufficient-candidate", "PROOF", 34, "PASS"},
		{"failed-necessity", "PROOF", 35, "FAIL"},
		{"failed-empty", "PROOF", 36, "FAIL"},
		{"failed-last-good", "PROOF", 37, "FAIL"},
		{"failed-first-bad", "PROOF", 38, "PASS"},
		{"failed-repair", "PROOF", 39, "FAIL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &nativeScriptExecutor{t: t, environment: input.Get("environmentPreimage"), script: input.Get("captures").Items()}
			for _, v := range base.script {
				base.captures = append(base.captures, nativeContractCapture(t, v))
			}
			executor := &nativeAnalysisTestExecutor{nativeScriptExecutor: base, ordinal: tc.ordinal, action: tc.verdict, pass: pass, fail: fail}
			got, err := runNativeAnalysis(context.Background(), nativeApprovalFromInput(input), nativeDeps{
				newExecutor: func() (nativeExecutor, error) { return executor, nil },
				nonce:       func() string { return input.Get("nonce").Str() },
				now:         func() time.Time { return nativeContractTime(t, input.Get("startedAt").Str()) },
			})
			var typed *NativeAnalysisError
			if got.Analysis.Kind() != jsjson.Undefined || !errors.As(err, &typed) || typed.Kind != tc.kind || typed.Ordinal != tc.ordinal || typed.StateID != base.script[tc.ordinal-1].Get("state", "id").Str() {
				t.Fatalf("failed execution/proof returned analysis or wrong error: %v", err)
			}
			if base.cursor != tc.ordinal || len(got.Provenance.Get("invocations").Items()) != tc.ordinal || got.Provenance.Get("status").Str() != "ERROR" {
				t.Fatalf("did not stop immediately: runs=%d receipts=%d", base.cursor, len(got.Provenance.Get("invocations").Items()))
			}
			if !executor.bounded {
				t.Fatal("executor did not receive whole-analysis deadline")
			}
			if tc.kind == "PROOF" && !got.Provenance.Get("invocations").Items()[tc.ordinal-1].Get("accepted").Bool() {
				t.Fatal("proof failure erased actual accepted execution")
			}
		})
	}
}

type nativeAnalysisTestExecutor struct {
	*nativeScriptExecutor
	ordinal    int
	action     string
	pass, fail nativefixture.Capture
	bounded    bool
}

func (e *nativeAnalysisTestExecutor) Run(ctx context.Context, m nativefixture.Materialized) (nativefixture.Capture, error) {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		e.bounded = remaining > 0 && remaining <= 90*time.Second
	}
	c, err := e.nativeScriptExecutor.Run(ctx, m)
	if e.cursor != e.ordinal {
		return c, err
	}
	switch e.action {
	case "panic":
		panic("scripted non-error panic")
	case "cancel":
		return c, context.Canceled
	case "invalid":
		c.Stdout = []byte("invalid protocol\n")
	case "PASS":
		c.Status = e.pass.Status
		c.Stdout = e.pass.Stdout
		c.Stderr = e.pass.Stderr
	case "FAIL":
		c.Status = e.fail.Status
		c.Stdout = e.fail.Stdout
		c.Stderr = e.fail.Stderr
	}
	return c, err
}

type nativeConstructionTestExecutor struct {
	nativeExecutor
	panicValue any
}

func (e *nativeConstructionTestExecutor) Environment() jsjson.Value { panic(e.panicValue) }

// Cancelling between accepted calls must retain the rejected next invocation
// without executing it or inventing unavailable process observations.
func TestNativeAnalysisCancellationBetweenCalls(t *testing.T) {
	input := nativeContractRows(t)[0].Get("input")
	base := &nativeScriptExecutor{t: t, environment: input.Get("environmentPreimage"), script: input.Get("captures").Items()}
	for _, v := range base.script {
		base.captures = append(base.captures, nativeContractCapture(t, v))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := &nativeBetweenCallsTestExecutor{nativeScriptExecutor: base, cancel: cancel}
	got, err := runNativeAnalysis(ctx, nativeApprovalFromInput(input), nativeDeps{
		newExecutor: func() (nativeExecutor, error) { return executor, nil },
		nonce:       func() string { return input.Get("nonce").Str() },
		now:         func() time.Time { return nativeContractTime(t, input.Get("startedAt").Str()) },
	})
	var typed *NativeAnalysisError
	if got.Analysis.Kind() != jsjson.Undefined || !errors.As(err, &typed) || typed.Kind != "CANCELLED" || typed.Ordinal != 2 || typed.StateID != "contribution-birch" || !errors.Is(err, context.Canceled) {
		t.Fatalf("wrong cancellation result: %v", err)
	}
	receipts := got.Provenance.Get("invocations").Items()
	if base.cursor != 1 || len(receipts) != 2 || got.Provenance.Get("status").Str() != "ERROR" {
		t.Fatalf("cancellation lacks a rejection receipt or executed again: calls=%d receipts=%d", base.cursor, len(receipts))
	}
	if !receipts[0].Get("accepted").Bool() || receipts[1].Get("accepted").Bool() || receipts[1].Get("ordinal").Num() != 2 || receipts[1].Get("stateId").Str() != typed.StateID || receipts[1].Get("executionAttempt").Num() != 0 || receipts[1].Get("failureKind").Str() != "CANCELLED" {
		t.Fatal("rejected invocation is not bound to the cancellation")
	}
	for _, key := range []string{"config", "expectedPartial", "executable", "argv", "cwd", "environment", "startedAt", "executedAt", "durationMs", "status", "signal", "stdout", "stderr", "runId", "runRecord", "runIdPreimage", "cleanupError"} {
		if receipts[1].Get(key).Kind() != jsjson.Null {
			t.Fatalf("pre-execution rejection fabricated %s", key)
		}
	}
}

type nativeBetweenCallsTestExecutor struct {
	*nativeScriptExecutor
	cancel context.CancelFunc
}

func (e *nativeBetweenCallsTestExecutor) Run(ctx context.Context, m nativefixture.Materialized) (nativefixture.Capture, error) {
	capture, err := e.nativeScriptExecutor.Run(ctx, m)
	e.cancel()
	return capture, err
}

// An invalid complete timeline must stop before any boundary probes.
func TestNativeAnalysisRejectsUnexpectedTimelineBeforeProbes(t *testing.T) {
	input := nativeContractRows(t)[0].Get("input")
	for _, ordinal := range []int{5, 9} {
		t.Run(input.Get("captures").Items()[ordinal-1].Get("state", "id").Str(), func(t *testing.T) {
			base := &nativeScriptExecutor{t: t, environment: input.Get("environmentPreimage"), script: input.Get("captures").Items()}
			for _, v := range base.script {
				base.captures = append(base.captures, nativeContractCapture(t, v))
			}
			action := "FAIL"
			if ordinal == 9 {
				action = "PASS"
			}
			executor := &nativeTimelineTestExecutor{nativeAnalysisTestExecutor: &nativeAnalysisTestExecutor{
				nativeScriptExecutor: base, ordinal: ordinal, action: action,
				pass: nativeContractCapture(t, input.Get("captures").Items()[0]),
				fail: nativeContractCapture(t, input.Get("captures").Items()[2]),
			}}
			got, err := runNativeAnalysis(context.Background(), nativeApprovalFromInput(input), nativeDeps{
				newExecutor: func() (nativeExecutor, error) { return executor, nil },
				nonce:       func() string { return input.Get("nonce").Str() },
				now:         func() time.Time { return nativeContractTime(t, input.Get("startedAt").Str()) },
			})
			var typed *NativeAnalysisError
			if got.Analysis.Kind() != jsjson.Undefined || !errors.As(err, &typed) || typed.Kind != "PROOF" || typed.Ordinal != 12 {
				t.Fatalf("wrong unexpected-timeline result: %v calls=%d", err, executor.calls)
			}
			if executor.calls != 12 || base.cursor != 12 || len(got.Provenance.Get("invocations").Items()) != 12 || got.Provenance.Get("status").Str() != "ERROR" {
				t.Fatalf("invalid timeline continued executing: calls=%d consumed=%d", executor.calls, base.cursor)
			}
		})
	}
}

type nativeTimelineTestExecutor struct {
	*nativeAnalysisTestExecutor
	calls int
}

func (e *nativeTimelineTestExecutor) Run(ctx context.Context, m nativefixture.Materialized) (nativefixture.Capture, error) {
	e.calls++
	if e.calls > 12 {
		return nativefixture.Capture{}, errors.New("unexpected boundary probe after an invalid complete timeline")
	}
	return e.nativeAnalysisTestExecutor.Run(ctx, m)
}
