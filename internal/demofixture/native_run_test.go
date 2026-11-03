package demofixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nativefixture"
)

func nativeContractBlob(t *testing.T, value jsjson.Value) []byte {
	t.Helper()
	data, err := base64.StdEncoding.Strict().DecodeString(value.Get("data").Str())
	if err != nil || value.Get("encoding").Str() != "base64" || value.Get("sha256").Str() != fmt.Sprintf("sha256:%x", sha256.Sum256(data)) || !value.Get("complete").Bool() {
		t.Fatal("invalid independent raw blob", err)
	}
	return data
}
func nativeContractStrings(value jsjson.Value) []string {
	out := make([]string, len(value.Items()))
	for i, item := range value.Items() {
		out[i] = item.Str()
	}
	return out
}
func nativeContractCapture(t *testing.T, v jsjson.Value) nativefixture.Capture {
	t.Helper()
	status := int(v.Get("status").Num())
	return nativefixture.Capture{
		Config:     nativefixture.Materialized{Data: nativeContractBlob(t, v.Get("config")), Partial: v.Get("expectedPartial").Bool()},
		Executable: v.Get("executable").Str(), Argv: nativeContractStrings(v.Get("argv")), Environment: nativeContractStrings(v.Get("environment")), Cwd: v.Get("cwd").Str(),
		StartedAt: nativeContractTime(t, v.Get("startedAt").Str()), ExecutedAt: nativeContractTime(t, v.Get("executedAt").Str()), DurationMs: int64(v.Get("durationMs").Num()),
		Status: &status, Signal: v.Get("signal").Str(), Stdout: nativeContractBlob(t, v.Get("stdout")), Stderr: nativeContractBlob(t, v.Get("stderr")),
		Incomplete: v.Get("incomplete").Bool(), FailureKind: v.Get("failureKind").Str(), CleanupError: v.Get("cleanupError").Str(),
	}
}

type nativeScriptExecutor struct {
	t           *testing.T
	environment jsjson.Value
	script      []jsjson.Value
	captures    []nativefixture.Capture
	cursor      int
	err         error
	panicValue  any
}

func (e *nativeScriptExecutor) Environment() jsjson.Value { return e.environment }
func (e *nativeScriptExecutor) Run(_ context.Context, input nativefixture.Materialized) (nativefixture.Capture, error) {
	e.t.Helper()
	if e.cursor >= len(e.script) {
		e.t.Fatal("unexpected execution beyond independent transcript")
	}
	v := e.script[e.cursor]
	if !bytes.Equal(input.Data, nativeContractBlob(e.t, v.Get("config"))) || input.Partial != v.Get("expectedPartial").Bool() {
		e.t.Fatalf("invocation %d materialized config differs from complete independent input: %q partial=%v", e.cursor+1, input.Data, input.Partial)
	}
	c := e.captures[e.cursor]
	e.cursor++
	if e.panicValue != nil {
		panic(e.panicValue)
	}
	return c, e.err
}
func nativeScriptSession(t *testing.T, row jsjson.Value) (*nativeSession, *nativeScriptExecutor) {
	t.Helper()
	input := row.Get("input")
	e := &nativeScriptExecutor{t: t, environment: input.Get("environmentPreimage"), script: input.Get("captures").Items()}
	for _, v := range e.script {
		e.captures = append(e.captures, nativeContractCapture(t, v))
	}
	s, err := newNativeSession(nativeApprovalFromInput(input), nativeDeps{
		newExecutor: func() (nativeExecutor, error) { return e, nil },
		nonce:       func() string { return input.Get("nonce").Str() },
		now: func() time.Time {
			t.Fatal("run identity must use observed capture times, not reread wall time")
			return time.Time{}
		},
	}, nativeContractTime(t, input.Get("startedAt").Str()))
	if err != nil {
		t.Fatal(err)
	}
	return s, e
}
func requireNativeValue(t *testing.T, label string, got, want jsjson.Value) {
	t.Helper()
	if g, w := jsjson.Stringify(got), jsjson.Stringify(want); g != w {
		t.Fatalf("complete %s differs\nwant=%s\ngot=%s", label, w, g)
	}
}

// A missing field, changed order/preimage/blob byte, wall-time uniqueness trick,
// or reused REPLAY verdict is detected by full independent 39-call comparisons.
func TestNativeRunCompleteReference(t *testing.T) {
	for _, row := range nativeContractRows(t) {
		t.Run(row.Get("id").Str(), func(t *testing.T) {
			s, e := nativeScriptSession(t, row)
			want := nativeContractExpected(t, row).Get("provenance")
			seen := make(map[string]bool)
			for i, input := range e.script {
				run, err := s.run(context.Background(), input.Get("state"), int(input.Get("executionAttempt").Num()))
				if err != nil {
					t.Fatalf("invocation %d: %v", i+1, err)
				}
				receipt := want.Get("invocations").Items()[i]
				requireNativeValue(t, "RunRecord", run, receipt.Get("runRecord"))
				requireNativeValue(t, "receipt including full ID preimage and raw blob hashes", s.receipts[i], receipt)
				d, err := canonical.DigestJSON(receipt.Get("runIdPreimage"))
				if err != nil || d != run.Get("id").Str() || seen[d] {
					t.Fatalf("identity is not unique canonical preimage digest: %s %v", d, err)
				}
				seen[d] = true
			}
			if e.cursor != 39 || len(e.script) != 39 || s.ordinal != 39 || len(s.receipts) != 39 || len(s.seenIDs) != 39 {
				t.Fatal("independent transcript not consumed exactly once")
			}
			requireNativeValue(t, "entire provenance envelope", s.provenance("COMPLETE", nativeContractTime(t, row.Get("input", "generatedAt").Str())), want)
		})
	}
}
func TestNativeRunRejectsDuplicateIdentity(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflicting-record-%v", conflict), func(t *testing.T) {
			row := nativeContractRows(t)[0]
			s, _ := nativeScriptSession(t, row)
			record := nativeContractExpected(t, row).Get("provenance", "invocations").Items()[0].Get("runRecord")
			id := record.Get("id").Str()
			if err := s.acceptNativeID(id, record); err != nil {
				t.Fatal(err)
			}
			if conflict {
				record.Obj().Set("stdout", jsjson.MakeString("conflicting"))
			}
			err := s.acceptNativeID(id, record)
			var typed *NativeAnalysisError
			if !errors.As(err, &typed) || typed.Kind != "DUPLICATE_ID" || len(s.seenIDs) != 1 {
				t.Fatalf("duplicate identity accepted or misclassified: %v", err)
			}
		})
	}
}
func TestNativeRunReturnsSnapshots(t *testing.T) {
	row := nativeContractRows(t)[0]
	s, e := nativeScriptSession(t, row)
	input := e.script[0]
	run, err := s.run(context.Background(), input.Get("state"), 0)
	if err != nil {
		t.Fatal(err)
	}
	generated := nativeContractTime(t, row.Get("input", "generatedAt").Str())
	before := jsjson.Stringify(s.provenance("ERROR", generated))
	run.Obj().Set("stdout", jsjson.MakeString("caller mutation"))
	envelope := s.provenance("ERROR", generated)
	envelope.Get("proposal", "program", "fields", "displayDefault").Items()[0] = jsjson.MakeString("caller mutation")
	envelope.Get("environmentPreimage", "environment").Items()[0] = jsjson.MakeString("PATH=caller")
	envelope.Get("invocations").Items()[0].Get("runRecord").Obj().Set("id", jsjson.MakeString("caller mutation"))
	envelope.Get("invocations").Items()[0].Get("runIdPreimage", "record").Obj().Set("stdout", jsjson.MakeString("caller mutation"))
	e.captures[0].Stdout[0] = 'X'
	e.captures[0].Config.Data[0] = 'X'
	e.captures[0].Environment[0] = "PATH=executor"
	if after := jsjson.Stringify(s.provenance("ERROR", generated)); after != before {
		t.Fatal("caller/executor mutation changed retained provenance")
	}
}

// Generic Resolve tolerates absent/agreeing duplicate records. Native execution
// must instead reject every nonexact transcript and retain the original bytes.
func TestNativeRunRejectsInvalidCaptures(t *testing.T) {
	cause := errors.New("scripted execution infrastructure failure")
	tests := []struct {
		name, kind string
		partial    bool
		mutate     func(*nativefixture.Capture) error
	}{
		{"missing-protocol", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.Stdout = []byte("settlement currency preserved\n"); return nil }},
		{"malformed-protocol", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.Stdout = []byte("{broken}\n"); return nil }},
		{"conflicting-protocol", "PROTOCOL", false, func(c *nativefixture.Capture) error {
			c.Stdout = append(c.Stdout, []byte("{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n")...)
			return nil
		}},
		{"agreeing-duplicate", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.Stdout = append(c.Stdout, c.Stdout...); return nil }},
		{"marker", "PROTOCOL", false, func(c *nativefixture.Capture) error {
			c.Stdout = []byte(strings.Replace(string(c.Stdout), "{\"protocol\"", "FAULTLINE_WITNESS_RESULT={\"protocol\"", 1))
			return nil
		}},
		{"trailing-noise", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.Stdout = append(c.Stdout, '\n'); return nil }},
		{"stderr-noise", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.Stderr = []byte("noise\n"); return nil }},
		{"zero-fail", "PROTOCOL", false, func(c *nativefixture.Capture) error {
			c.Stdout = []byte("{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n")
			c.Stderr = []byte("expected settlement USD; received EUR\n")
			return nil
		}},
		{"one-pass", "PROTOCOL", false, func(c *nativefixture.Capture) error { *c.Status = 1; return nil }},
		{"unexpected-parse", "UNEXPECTED_PARSE", false, func(c *nativefixture.Capture) error {
			*c.Status = 3
			c.Stdout = nil
			c.Stderr = []byte("native fixture config: real decoder failure\n")
			return nil
		}},
		{"wrong-config-bytes", "MATERIALIZE", false, func(c *nativefixture.Capture) error { c.Config.Data = append(c.Config.Data, '\n'); return nil }},
		{"wrong-partial-flag", "MATERIALIZE", false, func(c *nativefixture.Capture) error { c.Config.Partial = true; return nil }},
		{"invalid-utf8", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.Stdout = append(c.Stdout, 0xff); return nil }},
		{"invalid-stderr-utf8", "PROTOCOL", true, func(c *nativefixture.Capture) error { c.Stderr = append(c.Stderr, 0xff); return nil }},
		{"overflow-prefix", "OUTPUT_LIMIT", false, func(c *nativefixture.Capture) error {
			c.Incomplete = true
			c.FailureKind = "OUTPUT_LIMIT"
			return cause
		}},
		{"incomplete-pipe", "PIPE_DRAIN", false, func(c *nativefixture.Capture) error { c.Incomplete = true; return nil }},
		{"changed-effective-env", "ENVIRONMENT", false, func(c *nativefixture.Capture) error {
			c.Environment = append(c.Environment, "PATH=not-allowed")
			return nil
		}},
		{"wrong-argv", "ENVIRONMENT", false, func(c *nativefixture.Capture) error { c.Argv[1] = "another-program"; return nil }},
		{"missing-status", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.Status = nil; return nil }},
		{"other-status", "PROTOCOL", false, func(c *nativefixture.Capture) error { *c.Status = 2; return nil }},
		{"missing-start", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.StartedAt = time.Time{}; return nil }},
		{"negative-duration", "PROTOCOL", false, func(c *nativefixture.Capture) error { c.DurationMs = -1; return nil }},
		{"signal", "SIGNAL", false, func(c *nativefixture.Capture) error { c.Status = nil; c.Signal = "killed"; return nil }},
		{"spawn", "SPAWN", false, func(c *nativefixture.Capture) error {
			c.FailureKind = "SPAWN"
			return &nativefixture.ExecutionError{Kind: "SPAWN", Err: cause}
		}},
		{"deadline", "DEADLINE", false, func(c *nativefixture.Capture) error { c.FailureKind = "DEADLINE"; return context.DeadlineExceeded }},
		{"cancelled", "CANCELLED", false, func(c *nativefixture.Capture) error { c.FailureKind = "CANCELLED"; return context.Canceled }},
		{"cleanup", "CLEANUP", false, func(c *nativefixture.Capture) error { c.CleanupError = "scripted owned cleanup failure"; return cause }},
		{"primary-plus-cleanup", "DEADLINE", false, func(c *nativefixture.Capture) error {
			c.FailureKind = "DEADLINE"
			c.CleanupError = "cleanup secondary"
			return cause
		}},
		{"partial-with-stdout", "PROTOCOL", true, func(c *nativefixture.Capture) error { c.Stdout = []byte("noise\n"); return nil }},
		{"partial-missing-cause", "PROTOCOL", true, func(c *nativefixture.Capture) error { c.Stderr = []byte("native fixture config: \n"); return nil }},
		{"partial-wrong-diagnostic", "PROTOCOL", true, func(c *nativefixture.Capture) error {
			c.Stderr = []byte("SyntaxError: missing assignment value\n")
			return nil
		}},
		{"partial-multiline-diagnostic", "PROTOCOL", true, func(c *nativefixture.Capture) error {
			c.Stderr = append(c.Stderr, []byte("trailing noise\n")...)
			return nil
		}},
		{"partial-predicate-pass", "PROTOCOL", true, func(c *nativefixture.Capture) error {
			*c.Status = 0
			c.Stdout = []byte("settlement currency preserved\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n")
			c.Stderr = nil
			return nil
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := nativeContractRows(t)[0]
			s, e := nativeScriptSession(t, row)
			index := 0
			if tc.partial {
				index = 32
			}
			e.script = e.script[index : index+1]
			e.captures = e.captures[index : index+1]
			e.err = tc.mutate(&e.captures[0])
			c := e.captures[0]
			run, err := s.run(context.Background(), e.script[0].Get("state"), 0)
			var typed *NativeAnalysisError
			stateID := e.script[0].Get("state", "id").Str()
			if run.Kind() != jsjson.Undefined || !errors.As(err, &typed) || typed.Kind != tc.kind || typed.Ordinal != 1 || typed.StateID != stateID || e.cursor != 1 || len(s.receipts) != 1 || len(s.seenIDs) != 0 {
				t.Fatalf("capture accepted/lost/misclassified: run=%s error=%v calls=%d receipts=%d", jsjson.Stringify(run), err, e.cursor, len(s.receipts))
			}
			if e.err != nil && !errors.Is(err, e.err) {
				t.Fatal("execution cause not retained", err)
			}
			r := s.receipts[0]
			if r.Get("accepted").Bool() || r.Get("failureKind").Str() != tc.kind || r.Get("runId").Kind() != jsjson.Null || r.Get("runRecord").Kind() != jsjson.Null || r.Get("runIdPreimage").Kind() != jsjson.Null {
				t.Fatal("rejected receipt masquerades as accepted record")
			}
			requireNativeValue(t, "rejected config bytes/hash", r.Get("config"), nativefixture.RawBlob(c.Config.Data, true))
			requireNativeValue(t, "rejected stdout prefix/hash", r.Get("stdout"), nativefixture.RawBlob(c.Stdout, !c.Incomplete))
			requireNativeValue(t, "rejected stderr prefix/hash", r.Get("stderr"), nativefixture.RawBlob(c.Stderr, !c.Incomplete))
			if c.CleanupError != "" && r.Get("cleanupError").Str() != c.CleanupError {
				t.Fatal("secondary cleanup cause lost")
			}
		})
	}
}

// A zero capture on a dependency error must not discard the already prepared
// input or turn unavailable streams into an observed complete empty transcript.
func TestNativeRunRetainsAttemptedConfigOnEmptyCapture(t *testing.T) {
	row := nativeContractRows(t)[0]
	s, e := nativeScriptSession(t, row)
	e.captures[0] = nativefixture.Capture{}
	cause := errors.New("spawn did not yield any capture facts")
	e.err = &nativefixture.ExecutionError{Kind: "SPAWN", Err: cause}
	run, err := s.run(context.Background(), e.script[0].Get("state"), 0)
	var typed *NativeAnalysisError
	if run.Kind() != jsjson.Undefined || !errors.As(err, &typed) || typed.Kind != "SPAWN" || !errors.Is(err, cause) || len(s.receipts) != 1 {
		t.Fatalf("lost empty-capture failure: %v", err)
	}
	r := s.receipts[0]
	requireNativeValue(t, "attempted raw configuration", r.Get("config"), e.script[0].Get("config"))
	if r.Get("expectedPartial").Kind() != jsjson.Bool || r.Get("expectedPartial").Bool() {
		t.Fatal("lost attempted partial flag")
	}
	for _, key := range []string{"stdout", "stderr", "startedAt", "executedAt", "status"} {
		if r.Get(key).Kind() != jsjson.Null {
			t.Fatalf("invented unavailable %s", key)
		}
	}
}

func TestNativeRunRetainsPreparationAndPanicFailures(t *testing.T) {
	for _, mode := range []string{"unknown-hunk", "executor-panic", "changed-witness", "changed-proposal", "changed-approval", "changed-environment", "changed-executable"} {
		t.Run(mode, func(t *testing.T) {
			row := nativeContractRows(t)[0]
			s, e := nativeScriptSession(t, row)
			state := e.script[0].Get("state")
			wantKind, wantCalls, ordinal := "MATERIALIZE", 0, 1
			switch mode {
			case "unknown-hunk":
				state.Obj().Set("hunks", strArray("not-reviewed"))
			case "executor-panic":
				e.panicValue = errors.New("private dependency panic")
				wantKind, wantCalls = "PANIC", 1
			case "changed-witness":
				s.witness.Get("policy").Obj().Set("note", jsjson.MakeString("changed"))
				wantKind = "ENVIRONMENT"
			case "changed-proposal":
				s.proposal.Get("incidentPacket").Obj().Set("model", jsjson.MakeString("changed"))
				wantKind = "ENVIRONMENT"
			case "changed-approval":
				s.approval.Obj().Set("approvedAt", jsjson.MakeString("2026-11-02T00:00:00.000Z"))
				wantKind = "ENVIRONMENT"
			case "changed-environment":
				e.environment.Obj().Set("runtime", jsjson.MakeString("changed"))
				wantKind = "ENVIRONMENT"
			case "changed-executable":
				if _, err := s.run(context.Background(), state, 0); err != nil {
					t.Fatal(err)
				}
				state = e.script[1].Get("state")
				e.captures[1].Executable = "/synthetic/another/fl"
				e.captures[1].Argv[0] = e.captures[1].Executable
				wantKind, wantCalls, ordinal = "ENVIRONMENT", 2, 2
			}
			run, err := s.run(context.Background(), state, 0)
			var typed *NativeAnalysisError
			if run.Kind() != jsjson.Undefined || !errors.As(err, &typed) || typed.Kind != wantKind || typed.Ordinal != ordinal || e.cursor != wantCalls || s.ordinal != ordinal || len(s.receipts) != ordinal {
				t.Fatalf("failure not retained: %v calls=%d receipts=%d", err, e.cursor, len(s.receipts))
			}
			r := s.receipts[ordinal-1]
			if r.Get("accepted").Bool() || r.Get("runRecord").Kind() != jsjson.Null {
				t.Fatal("failed invocation accepted")
			}
			if wantCalls == 0 || mode == "executor-panic" {
				for _, key := range []string{"executable", "argv", "cwd", "environment", "startedAt", "executedAt", "durationMs", "status", "signal", "stdout", "stderr"} {
					if r.Get(key).Kind() != jsjson.Null {
						t.Fatalf("invented unavailable %s: %s", key, jsjson.Stringify(r.Get(key)))
					}
				}
			}
		})
	}
}
