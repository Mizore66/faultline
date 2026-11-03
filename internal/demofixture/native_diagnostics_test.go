package demofixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nativefixture"
)

// A changed rejection kind, lost cause, accepted failed run, or mismatched
// diagnostic must not count as evidence for the library coverage gate.
func nativeDiagnostic(t *testing.T, err error, kind string, ordinal int, state, message string) {
	t.Helper()
	want := message
	if kind != "" {
		want = fmt.Sprintf("native analysis %s (invocation %d, state %q): %s", kind, ordinal, state, message)
		var typed *NativeAnalysisError
		if !errors.As(err, &typed) || typed.Kind != kind || typed.Ordinal != ordinal || typed.StateID != state {
			t.Fatalf("wrong native error fields: %v", err)
		}
	}
	if err == nil || err.Error() != want {
		t.Fatalf("diagnostic differs: want %q, got %v", want, err)
	}
	observed, marshalErr := json.Marshal(err.Error())
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	t.Logf("NATIVE-DIAGNOSTIC %s", observed)
}

func TestNativeDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, kind, message string }{
		{"nil-context", "CANCELLED", "native analysis requires a context"},
		{"factory", "ENVIRONMENT", "native execution dependencies are unavailable"},
		{"nonce-dependency", "ENVIRONMENT", "native execution dependencies are unavailable"},
		{"nil-executor", "ENVIRONMENT", "native executor is unavailable"},
		{"clock", "ENVIRONMENT", "native wall clock is unavailable"},
		{"empty-nonce", "ENVIRONMENT", "native analysis nonce is empty"},
		{"environment", "ENVIRONMENT", "native environment observation is not an object"},
		{"approval", "APPROVAL", "approval must match the proposal and record a canonical UTC millisecond timestamp no later than execution entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := nativeContractRows(t)[0]
			s, e := nativeScriptSession(t, row)
			input := row.Get("input")
			ctx := context.Background()
			approval := nativeApprovalFromInput(input)
			deps := nativeDeps{
				newExecutor: func() (nativeExecutor, error) { return e, nil },
				nonce:       func() string { return s.nonce },
				now:         func() time.Time { return nativeContractTime(t, input.Get("startedAt").Str()) },
			}
			switch tc.name {
			case "nil-context":
				ctx = nil
			case "factory":
				deps.newExecutor = nil
			case "nonce-dependency":
				deps.nonce = nil
			case "nil-executor":
				deps.newExecutor = func() (nativeExecutor, error) { return nil, nil }
			case "clock":
				deps.now = nil
			case "empty-nonce":
				deps.nonce = func() string { return "" }
			case "environment":
				e.environment = jsjson.Value{}
			case "approval":
				approval = NativeApproval{}
			}
			got, err := runNativeAnalysis(ctx, approval, deps)
			nativeDiagnostic(t, err, tc.kind, 0, "", tc.message)
			if got.Analysis.Kind() != jsjson.Undefined || got.Provenance.Get("status").Str() != "ERROR" || len(got.Provenance.Get("invocations").Items()) != 0 || e.cursor != 0 {
				t.Fatal("construction rejection invented analysis or execution")
			}
		})
	}
	for _, tc := range []struct{ name, kind, message string }{
		{"state", "MATERIALIZE", "invalid native state or execution attempt"},
		{"hunk", "MATERIALIZE", "native hunk must be a string"},
		{"capture-kind", "PIPE_DRAIN", "native capture reported PIPE_DRAIN"},
		{"config", "MATERIALIZE", "captured configuration differs from the reviewed materialization"},
		{"argv", "ENVIRONMENT", "captured executable, argv, directory, or effective environment changed"},
		{"executable", "ENVIRONMENT", "native executable changed between invocations"},
		{"streams", "PIPE_DRAIN", "native streams are incomplete"},
		{"signal", "SIGNAL", "native process terminated by killed"},
		{"observations", "PROTOCOL", "missing process observations, invalid duration, or invalid UTF-8"},
		{"partial", "PROTOCOL", "partial configuration did not produce one complete native decoder diagnostic"},
		{"decoder", "UNEXPECTED_PARSE", "valid reviewed configuration failed native decoding"},
		{"transcript", "PROTOCOL", "native stdout/stderr/status do not match one exact predicate transcript"},
		{"duplicate", "DUPLICATE_ID", "native invocation identity was already used"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := nativeContractRows(t)[0]
			s, e := nativeScriptSession(t, row)
			if tc.name == "partial" {
				e.script, e.captures = e.script[32:33], e.captures[32:33]
			}
			state := cloneNativeValue(e.script[0].Get("state"))
			stateID := state.Get("id").Str()
			c := &e.captures[0]
			switch tc.name {
			case "state":
				state.Obj().Set("hunks", jsjson.MakeNull())
			case "hunk":
				state.Obj().Set("hunks", jsjson.MakeArray([]jsjson.Value{jsjson.MakeNumber(1)}))
			case "capture-kind":
				c.FailureKind = "PIPE_DRAIN"
			case "config":
				c.Config.Data = append(c.Config.Data, '\n')
			case "argv":
				c.Argv[1] = "wrong-program"
			case "executable":
				s.receipts = append(s.receipts, jsjson.MustObject("executable", jsjson.MakeString("different-executable")))
			case "streams":
				c.Incomplete = true
			case "signal":
				c.Signal = "killed"
			case "observations":
				c.Status = nil
			case "partial":
				c.Stdout = []byte("unexpected output\n")
			case "decoder":
				status := 3
				c.Status = &status
			case "transcript":
				c.Stdout = []byte("invalid transcript\n")
			case "duplicate":
				id := nativeContractExpected(t, row).Get("provenance", "invocations").Items()[0].Get("runId").Str()
				s.seenIDs[id] = struct{}{}
			}
			before := len(s.receipts)
			run, err := s.run(context.Background(), state, 0)
			nativeDiagnostic(t, err, tc.kind, 1, stateID, tc.message)
			if run.Kind() != jsjson.Undefined || len(s.receipts) != before+1 {
				t.Fatal("failed run returned analysis or lost its receipt")
			}
			receipt := s.receipts[before]
			if receipt.Get("accepted").Bool() || receipt.Get("runRecord").Kind() != jsjson.Null || receipt.Get("failureKind").Str() != tc.kind {
				t.Fatal("failed invocation was accepted")
			}
		})
	}
	for _, tc := range []struct{ name, message string }{
		{"proposal", "frozen native proposal or approval changed"},
		{"witness", "frozen native witness, program, or environment identity changed"},
		{"environment-size", "unexpected effective environment size"},
		{"environment-entry", "unexpected effective environment entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e := nativeScriptSession(t, nativeContractRows(t)[0])
			switch tc.name {
			case "proposal":
				s.proposal.Obj().Set("digest", jsjson.MakeString("changed"))
			case "witness":
				s.witness.Obj().Set("digest", jsjson.MakeString("changed"))
			case "environment-size":
				nativeDiagnosticEnvironment(s, e, false, nil)
			case "environment-entry":
				nativeDiagnosticEnvironment(s, e, false, []jsjson.Value{jsjson.MakeString("PATH=unexpected")})
			}
			nativeDiagnostic(t, s.checkNativeIdentity(), "", 0, "", tc.message)
		})
	}
	for _, tc := range []struct {
		name, message string
		ordinal       int
	}{
		{"timeline", "native timeline does not establish the three expected boundaries", 12},
		{"verdict", "native observation does not establish the required state, identity, or verdict", 13},
		{"catalog-count", "native catalog and invocation evidence do not cover exactly39 unique executions", 39},
		{"receipt-link", "catalog execution is not linked to exactly one complete accepted receipt", 39},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := nativeContractRows(t)[0]
			s, e := nativeScriptSession(t, row)
			stateID := ""
			if tc.name == "timeline" || tc.name == "verdict" {
				index := 4
				if tc.name == "verdict" {
					index = 12
				}
				failed := nativeContractCapture(t, row.Get("input", "captures").Items()[2])
				e.captures[index].Status, e.captures[index].Stdout, e.captures[index].Stderr = failed.Status, failed.Stdout, failed.Stderr
				stateIndex := index
				if tc.name == "timeline" {
					stateIndex = 5
				}
				stateID = e.script[stateIndex].Get("state", "id").Str()
			} else {
				s.executor = &nativeDiagnosticExecutor{nativeScriptExecutor: e, after: func() {
					if e.cursor != 39 {
						return
					}
					if tc.name == "catalog-count" {
						for id := range s.seenIDs {
							delete(s.seenIDs, id)
							break
						}
					} else {
						s.receipts[0].Obj().Set("runId", jsjson.MakeString("broken-link"))
					}
				}}
				if tc.name == "receipt-link" {
					stateID = e.script[0].Get("state", "id").Str()
				}
			}
			analysis, err := s.analyze(context.Background())
			nativeDiagnostic(t, err, "PROOF", tc.ordinal, stateID, tc.message)
			if analysis.Kind() != jsjson.Undefined || e.cursor != tc.ordinal || len(s.receipts) != tc.ordinal || !s.receipts[tc.ordinal-1].Get("accepted").Bool() {
				t.Fatal("proof failure did not stop with its actual observations")
			}
		})
	}
}

type nativeDiagnosticExecutor struct {
	*nativeScriptExecutor
	after func()
}

func (e *nativeDiagnosticExecutor) Run(ctx context.Context, m nativefixture.Materialized) (nativefixture.Capture, error) {
	c, err := e.nativeScriptExecutor.Run(ctx, m)
	e.after()
	return c, err
}

func nativeDiagnosticEnvironment(s *nativeSession, e *nativeScriptExecutor, windows bool, entries []jsjson.Value) {
	s.environmentPreimage.Obj().Set("goos", jsjson.MakeString("linux"))
	if windows {
		s.environmentPreimage.Obj().Set("goos", jsjson.MakeString("windows"))
	}
	s.environmentPreimage.Obj().Set("environment", jsjson.MakeArray(entries))
	s.environmentDigest = digest(s.environmentPreimage)
	e.environment = cloneNativeValue(s.environmentPreimage)
}

// These checks pin the producer implications used by the four individually
// justified unreachable literals. They do not claim to execute those guards.
func TestNativeDiagnosticInvariants(t *testing.T) {
	t.Run("timeline", func(t *testing.T) {
		row := nativeContractRows(t)[0]
		pass := nativeContractCapture(t, row.Get("input", "captures").Items()[0])
		fail := nativeContractCapture(t, row.Get("input", "captures").Items()[2])
		for mask := 0; mask < 256; mask++ {
			s, e := nativeScriptSession(t, row)
			for i := 0; i < 8; i++ {
				capture := pass
				if mask&(1<<i) != 0 {
					capture = fail
				}
				e.captures[4+i].Status, e.captures[4+i].Stdout, e.captures[4+i].Stderr = capture.Status, capture.Stdout, capture.Stderr
			}
			result, err := s.analyze(context.Background())
			if mask == 176 { // FAIL at turns 5, 6, 8 only.
				if err != nil || len(result.Get("transitions").Items()) != 3 {
					t.Fatalf("valid timeline: %v", err)
				}
			} else {
				var typed *NativeAnalysisError
				if !errors.As(err, &typed) || typed.Kind != "PROOF" || typed.Ordinal != 12 || e.cursor != 12 {
					t.Fatalf("timeline %08b reached stable-boundary construction: %v", mask, err)
				}
			}
		}
	})
	t.Run("partial", func(t *testing.T) {
		s, e := nativeScriptSession(t, nativeContractRows(t)[0])
		e.script, e.captures = e.script[32:33], e.captures[32:33]
		run, err := s.run(context.Background(), e.script[0].Get("state"), 0)
		if err != nil || run.Get("verdict").Str() != "ERROR" || run.Get("reasonCode").Str() != "FIXTURE_PARSE_ERROR" {
			t.Fatalf("partial decoder classification changed: %v", err)
		}
	})
	t.Run("subsets", func(t *testing.T) {
		row := nativeContractRows(t)[0]
		states := fixtureStates()
		requireNativeValue(t, "candidate input", states[idxCandidate].Get("hunks"), row.Get("input", "captures").Items()[33].Get("state", "hunks"))
		if len(states[idxCandidate].Get("hunks").Items()) != 2 || len(states[idxReverted].Get("hunks").Items()) != 0 || len(states[idxLastGood].Get("hunks").Items()) != 0 {
			t.Fatal("fixed counterfactual subsets no longer imply the guard")
		}
		s, _ := nativeScriptSession(t, row)
		if _, err := s.analyze(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("environment", func(t *testing.T) {
		values := []jsjson.Value{jsjson.MakeString("FAULTLINE_NATIVE_DEMO_CHILD=v1"), jsjson.MakeString("SYSTEMROOT=X"), jsjson.MakeString("SYSTEMROOT="), jsjson.MakeString("PATH=unexpected"), jsjson.MakeNumber(1)}
		row := nativeContractRows(t)[0]
		for _, windows := range []bool{false, true} {
			for length := 0; length <= 3; length++ {
				n := 1
				for i := 0; i < length; i++ {
					n *= len(values)
				}
				for sequence := 0; sequence < n; sequence++ {
					entries := make([]jsjson.Value, length)
					index := sequence
					for i := range entries {
						entries[i] = values[index%len(values)]
						index /= len(values)
					}
					s, e := nativeScriptSession(t, row)
					nativeDiagnosticEnvironment(s, e, windows, entries)
					err := s.checkNativeIdentity()
					want := !windows && length == 1 && entries[0].Str() == "FAULTLINE_NATIVE_DEMO_CHILD=v1" || windows && length == 2 && (entries[0].Str() == "FAULTLINE_NATIVE_DEMO_CHILD=v1" && entries[1].Str() == "SYSTEMROOT=X" || entries[1].Str() == "FAULTLINE_NATIVE_DEMO_CHILD=v1" && entries[0].Str() == "SYSTEMROOT=X")
					if (err == nil) != want || err != nil && err.Error() != "unexpected effective environment size" && err.Error() != "unexpected effective environment entry" {
						t.Fatalf("environment windows=%v entries=%s: %v", windows, jsjson.Stringify(jsjson.MakeArray(entries)), err)
					}
				}
			}
		}
	})
}
