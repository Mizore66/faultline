package demofixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nativefixture"
	"github.com/Mizore66/faultline/internal/witnessresult"
)

func (s *nativeSession) acceptNativeID(id string, record jsjson.Value) error {
	if _, exists := s.seenIDs[id]; exists {
		return &NativeAnalysisError{Kind: "DUPLICATE_ID", Ordinal: s.ordinal, StateID: record.Get("stateId").Str(), Err: errors.New("native invocation identity was already used")}
	}
	s.seenIDs[id] = struct{}{}
	return nil
}

func (s *nativeSession) run(ctx context.Context, state jsjson.Value, attempt int) (record jsjson.Value, err error) {
	s.ordinal++
	ordinal, stateID := s.ordinal, state.Get("id").Str()
	var capture nativefixture.Capture
	var preimage jsjson.Value
	fail := func(kind string, cause error) (jsjson.Value, error) {
		return jsjson.Value{}, &NativeAnalysisError{Kind: kind, Ordinal: ordinal, StateID: stateID, Err: cause}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			cause, ok := recovered.(error)
			if !ok {
				cause = fmt.Errorf("%v", recovered)
			}
			record, err = fail("PANIC", cause)
		}
		kind := ""
		if err != nil {
			var typed *NativeAnalysisError
			if errors.As(err, &typed) {
				kind = typed.Kind
			}
		}
		s.receipts = append(s.receipts, nativeReceipt(ordinal, stateID, attempt, capture, record, preimage, kind))
	}()
	if stateID == "" || state.Get("hunks").Kind() != jsjson.Array || attempt < 0 || attempt > 3 {
		return fail("MATERIALIZE", errors.New("invalid native state or execution attempt"))
	}
	hunks := make([]string, len(state.Get("hunks").Items()))
	for i, v := range state.Get("hunks").Items() {
		if v.Kind() != jsjson.String {
			return fail("MATERIALIZE", errors.New("native hunk must be a string"))
		}
		hunks[i] = v.Str()
	}
	materialized, cause := nativefixture.Materialize(hunks)
	if cause != nil {
		return fail("MATERIALIZE", cause)
	}
	capture.Config = nativefixture.Materialized{Data: bytes.Clone(materialized.Data), Partial: materialized.Partial}
	if cause := s.checkNativeIdentity(); cause != nil {
		return fail("ENVIRONMENT", cause)
	}
	observed, cause := s.executor.Run(ctx, materialized)
	if observed.Config.Data == nil && (cause != nil || observed.FailureKind != "" || observed.CleanupError != "") {
		observed.Config = capture.Config
	}
	capture = observed
	if cause != nil || capture.FailureKind != "" || capture.CleanupError != "" {
		kind := capture.FailureKind
		if kind == "" {
			var execution *nativefixture.ExecutionError
			switch {
			case errors.As(cause, &execution):
				kind = execution.Kind
			case errors.Is(cause, context.DeadlineExceeded):
				kind = "DEADLINE"
			case errors.Is(cause, context.Canceled):
				kind = "CANCELLED"
			case capture.CleanupError != "":
				kind = "CLEANUP"
			default:
				kind = "PROTOCOL"
			}
		}
		if capture.CleanupError != "" {
			cause = errors.Join(cause, errors.New(capture.CleanupError))
		}
		if cause == nil {
			cause = errors.New("native capture reported " + kind)
		}
		return fail(kind, cause)
	}
	if !bytes.Equal(capture.Config.Data, materialized.Data) || capture.Config.Partial != materialized.Partial {
		return fail("MATERIALIZE", errors.New("captured configuration differs from the reviewed materialization"))
	}
	if jsjson.Stringify(strArray(capture.Environment...)) != jsjson.Stringify(s.environmentPreimage.Get("environment")) || capture.Executable == "" || len(capture.Argv) != 2 || capture.Argv[0] != capture.Executable || capture.Argv[1] != nativefixture.ChildArg || capture.Cwd == "" {
		return fail("ENVIRONMENT", errors.New("captured executable, argv, directory, or effective environment changed"))
	}
	for _, previous := range s.receipts {
		if exe := previous.Get("executable").Str(); exe != "" && exe != capture.Executable {
			return fail("ENVIRONMENT", errors.New("native executable changed between invocations"))
		}
	}
	if capture.Incomplete {
		return fail("PIPE_DRAIN", errors.New("native streams are incomplete"))
	}
	if capture.Signal != "" {
		return fail("SIGNAL", errors.New("native process terminated by "+capture.Signal))
	}
	if capture.Status == nil || capture.StartedAt.IsZero() || capture.ExecutedAt.IsZero() || capture.DurationMs < 0 || !utf8.Valid(capture.Stdout) || !utf8.Valid(capture.Stderr) {
		return fail("PROTOCOL", errors.New("missing process observations, invalid duration, or invalid UTF-8"))
	}
	status := *capture.Status
	classification := witnessresult.Classification{}
	if materialized.Partial {
		cause := strings.TrimSuffix(strings.TrimPrefix(string(capture.Stderr), "native fixture config: "), "\n")
		if json.Valid(materialized.Data) || status != 3 || len(capture.Stdout) != 0 || !strings.HasPrefix(string(capture.Stderr), "native fixture config: ") || !strings.HasSuffix(string(capture.Stderr), "\n") || strings.TrimSpace(cause) == "" || strings.ContainsAny(cause, "\r\n") {
			return fail("PROTOCOL", errors.New("partial configuration did not produce one complete native decoder diagnostic"))
		}
		classification = witnessresult.Classification{Verdict: "ERROR", Reason: "FIXTURE_PARSE_ERROR"}
	} else {
		if status == 3 {
			return fail("UNEXPECTED_PARSE", errors.New("valid reviewed configuration failed native decoding"))
		}
		pass := status == 0 && string(capture.Stdout) == s.program.Get("stdoutPass").Str() && len(capture.Stderr) == 0
		failed := status == 1 && string(capture.Stdout) == s.program.Get("stdoutFail").Str() && string(capture.Stderr) == s.program.Get("stderrFail").Str()
		resolved := witnessresult.Resolve(string(capture.Stdout))
		if (!pass && !failed) || !resolved.Ok || resolved.Result == nil || !witnessresult.ConsistentWithExitCode(*resolved.Result, status) {
			return fail("PROTOCOL", errors.New("native stdout/stderr/status do not match one exact predicate transcript"))
		}
		classification = witnessresult.Classify(resolved.Result.Outcome)
	}
	body := jsjson.MustObject(
		"stateId", jsjson.MakeString(stateID),
		"witnessDigest", s.witness.Get("digest"),
		"environmentDigest", jsjson.MakeString(s.environmentDigest),
		"verdict", jsjson.MakeString(classification.Verdict),
		"reasonCode", jsjson.MakeString(classification.Reason),
		"stdout", jsjson.MakeString(string(capture.Stdout)),
		"stderr", jsjson.MakeString(string(capture.Stderr)),
		"durationMs", jsjson.MakeNumber(float64(capture.DurationMs)),
		"executionKind", jsjson.MakeString("EXECUTED"),
		"executedAt", jsjson.MakeString(clock.ISO(capture.ExecutedAt)))
	preimage = jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.native-demo-run-id.v1"),
		"nonce", jsjson.MakeString(s.nonce),
		"ordinal", jsjson.MakeNumber(float64(ordinal)),
		"executionAttempt", jsjson.MakeNumber(float64(attempt)),
		"startedAt", jsjson.MakeString(clock.ISO(capture.StartedAt)),
		"configDigest", nativefixture.RawBlob(capture.Config.Data, true).Get("sha256"),
		"status", jsjson.MakeNumber(float64(status)), "signal", jsjson.MakeNull(), "record", body)
	id := digest(preimage)
	if err := s.acceptNativeID(id, body); err != nil {
		return jsjson.Value{}, err
	}
	out := jsjson.NewObj()
	out.Set("id", jsjson.MakeString(id))
	for _, key := range body.Obj().Keys() {
		out.Set(key, body.Get(key))
	}
	return jsjson.MakeObject(out), nil
}

func (s *nativeSession) checkNativeIdentity() error {
	proposalBody := jsjson.NewObj()
	for _, key := range s.proposal.Obj().Keys() {
		if key != "digest" {
			proposalBody.Set(key, s.proposal.Get(key))
		}
	}
	if digest(jsjson.MakeObject(proposalBody)) != s.proposal.Get("digest").Str() || s.approval.Get("proposalDigest").Str() != s.proposal.Get("digest").Str() || s.approval.Get("approvedAt").Str() != s.witness.Get("approvedAt").Str() {
		return errors.New("frozen native proposal or approval changed")
	}
	body := jsjson.NewObj()
	for _, key := range s.witness.Obj().Keys() {
		if key != "digest" {
			body.Set(key, s.witness.Get(key))
		}
	}
	if digest(jsjson.MakeObject(body)) != s.witness.Get("digest").Str() || digest(s.program) != s.programDigest || digest(s.environmentPreimage) != s.environmentDigest || jsjson.Stringify(s.executor.Environment()) != jsjson.Stringify(s.environmentPreimage) {
		return errors.New("frozen native witness, program, or environment identity changed")
	}
	environment := s.environmentPreimage.Get("environment")
	windows := s.environmentPreimage.Get("goos").Str() == "windows"
	if environment.Kind() != jsjson.Array || len(environment.Items()) != 1 && !windows || windows && len(environment.Items()) != 2 {
		return errors.New("unexpected effective environment size")
	}
	marker, root := false, false
	for _, value := range environment.Items() {
		switch {
		case value.Kind() == jsjson.String && value.Str() == nativefixture.ChildEnvName+"="+nativefixture.ChildEnvValue && !marker:
			marker = true
		case value.Kind() == jsjson.String && windows && strings.HasPrefix(value.Str(), "SYSTEMROOT=") && len(value.Str()) > len("SYSTEMROOT=") && !root:
			root = true
		default:
			return errors.New("unexpected effective environment entry")
		}
	}
	if !marker || windows && !root {
		return errors.New("required child environment missing")
	}
	return nil
}

func nativeOptionalString(value string) jsjson.Value {
	if value == "" {
		return jsjson.MakeNull()
	}
	return jsjson.MakeString(value)
}
func nativeOptionalTime(value time.Time) jsjson.Value {
	if value.IsZero() {
		return jsjson.MakeNull()
	}
	return jsjson.MakeString(clock.ISO(value))
}
func nativeOptionalStrings(values []string) jsjson.Value {
	if values == nil {
		return jsjson.MakeNull()
	}
	return strArray(values...)
}
func nativeReceipt(ordinal int, stateID string, attempt int, c nativefixture.Capture, record, preimage jsjson.Value, failureKind string) jsjson.Value {
	config, partial, duration, status := jsjson.MakeNull(), jsjson.MakeNull(), jsjson.MakeNull(), jsjson.MakeNull()
	if c.Config.Data != nil {
		config, partial = nativefixture.RawBlob(c.Config.Data, true), jsjson.MakeBool(c.Config.Partial)
	}
	if !c.StartedAt.IsZero() || !c.ExecutedAt.IsZero() {
		duration = jsjson.MakeNumber(float64(c.DurationMs))
	}
	if c.Status != nil {
		status = jsjson.MakeNumber(float64(*c.Status))
	}
	accepted := failureKind == "" && record.Kind() == jsjson.Object
	id := jsjson.MakeNull()
	if accepted {
		id = record.Get("id")
	} else {
		record, preimage = jsjson.MakeNull(), jsjson.MakeNull()
	}
	stdout, stderr := jsjson.MakeNull(), jsjson.MakeNull()
	if !c.StartedAt.IsZero() || !c.ExecutedAt.IsZero() || c.Stdout != nil {
		stdout = nativefixture.RawBlob(c.Stdout, !c.Incomplete)
	}
	if !c.StartedAt.IsZero() || !c.ExecutedAt.IsZero() || c.Stderr != nil {
		stderr = nativefixture.RawBlob(c.Stderr, !c.Incomplete)
	}
	return cloneNativeValue(jsjson.MustObject(
		"ordinal", jsjson.MakeNumber(float64(ordinal)), "stateId", jsjson.MakeString(stateID), "executionAttempt", jsjson.MakeNumber(float64(attempt)),
		"config", config, "expectedPartial", partial,
		"executable", nativeOptionalString(c.Executable), "argv", nativeOptionalStrings(c.Argv), "cwd", nativeOptionalString(c.Cwd), "environment", nativeOptionalStrings(c.Environment),
		"startedAt", nativeOptionalTime(c.StartedAt), "executedAt", nativeOptionalTime(c.ExecutedAt), "durationMs", duration, "status", status, "signal", nativeOptionalString(c.Signal),
		"stdout", stdout, "stderr", stderr,
		"accepted", jsjson.MakeBool(accepted), "runId", id, "runRecord", record, "runIdPreimage", preimage,
		"failureKind", nativeOptionalString(failureKind), "cleanupError", nativeOptionalString(c.CleanupError)))
}
func (s *nativeSession) provenance(status string, generatedAt time.Time) jsjson.Value {
	return cloneNativeValue(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.native-demo-execution.v1"), "status", jsjson.MakeString(status),
		"proposal", s.proposal, "program", s.program, "programDigest", jsjson.MakeString(s.programDigest), "approval", s.approval,
		"environmentPreimage", s.environmentPreimage, "environmentDigest", jsjson.MakeString(s.environmentDigest), "nonce", jsjson.MakeString(s.nonce),
		"generatedAt", nativeOptionalTime(generatedAt), "invocations", jsjson.MakeArray(s.receipts)))
}
