package cli

import (
	"math"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/ledger"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// recordLimitation is the attach result's fixed limitation
// (src/cli-app.ts:1556).
const recordLimitation = "Observed sidecar attribution only; FaultLine does not claim private Codex interception or turn-level blame."

// ledgerPathOption ports ledgerPath (src/cli-app.ts:256):
// resolve(option("--ledger") ?? `.faultline/recordings/${sessionId ?? "session"}.json`).
func ledgerPathOption(args []string, sessionID string, hasSession bool) string {
	file, ok := option(args, "--ledger")
	if !ok {
		if !hasSession {
			sessionID = "session"
		}
		file = jsexc.Concat(".faultline/recordings/", sessionID, ".json")
	}
	return nodefs.Resolve(file)
}

// parseObservedTransport ports src/cli-app.ts:1478.
func parseObservedTransport(raw string) (string, error) {
	parsed, err := bundle.ParseValue(jsjson.MakeString(raw), gitproof.CodexTransportSchema)
	if err != nil {
		return "", fail("--transport must be one of %s (got %s). Use OBSERVED_EXTERNAL_TRANSPORT for honestly labeled non-Codex checkpoints — never rebadge Cursor/editor history as SIDE_CAR or CODEX_*.",
			strings.Join(gitproof.CodexTransportOptions, ", "), jsjson.Quote(raw))
	}
	return parsed.Str(), nil
}

// jsIsInteger is Number.isInteger(n).
func jsIsInteger(n float64) bool {
	return !math.IsInf(n, 0) && !math.IsNaN(n) && n == math.Trunc(n)
}

// optionalRepository is `option(args, "--repo") ? resolve(requiredOption(args,
// "--repo")) : undefined` (src/cli-app.ts:1526, 1566): an empty value is
// absent, and a "--"-prefixed one fails requiredOption.
func optionalRepository(args []string) (repository string, has bool, err error) {
	if v, ok := option(args, "--repo"); !ok || v == "" {
		return "", false, nil
	}
	raw, err := requiredOption(args, "--repo")
	if err != nil {
		return "", false, err
	}
	return nodefs.Resolve(raw), true, nil
}

// headHashOf is `ledger.events.at(-1)?.hash` (undefined: left out).
func headHashOf(l jsjson.Value) jsjson.Value {
	events := l.Get("events").Items()
	if len(events) == 0 {
		return jsjson.Value{}
	}
	return events[len(events)-1].Get("hash")
}

// spreadVerification is `{...head, ...verification}`: the
// LedgerVerification keys last, in their order.
func spreadVerification(head jsjson.Value, verification gitproof.LedgerVerification) jsjson.Value {
	out := head.Obj()
	v := verification.JSON().Obj()
	for _, k := range v.Keys() {
		out.Set(k, v.Field(k))
	}
	return head
}

func (e *env) setValidExit(verification gitproof.LedgerVerification) {
	if verification.Valid {
		e.exitCode = 0
	} else {
		e.exitCode = 1
	}
}

// recordCommand ports src/cli-app.ts:1488 recordCommand, reached from
// `fl record` and `fl codex record`.
func recordCommand(e *env, args []string) error {
	action := ""
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "init":
		return e.recordInit(args)
	case "checkpoint":
		return e.recordCheckpoint(args)
	case "attach":
		return e.recordAttach(args)
	case "stdin":
		return e.recordStdin(args)
	case "verify":
		file := ledgerPathOption(args, "", false)
		verification := ledger.VerifyFile(file)
		e.printJSON(spreadVerification(obj("ledger", file), verification))
		e.setValidExit(verification)
		return nil
	}
	return fail("Usage: fl record init|stdin|checkpoint|attach|verify ...")
}

// recordInit ports the init case (src/cli-app.ts:1491-1509).
func (e *env) recordInit(args []string) error {
	sessionID, err := requiredOption(args, "--session")
	if err != nil {
		return err
	}
	file := ledgerPathOption(args, sessionID, true)
	if nodefs.Exists(file) {
		return fail("Refusing to overwrite an existing lifecycle ledger: %s", file)
	}
	repository := repoOption(args)
	l, err := ledger.Create(ledger.CreateOptions{SessionID: sessionID})
	if err != nil {
		return err
	}
	// The payload is evaluated inside appendLifecycleEvent's argument, after
	// the create.
	rawTransport, ok := option(args, "--transport")
	if !ok {
		rawTransport = "SIDE_CAR"
	}
	transport, err := parseObservedTransport(rawTransport)
	if err != nil {
		return err
	}
	payload := jsjson.NewObj()
	payload.Set("transport", jsjson.MakeString(transport))
	payload.Set("workingDirectory", jsjson.MakeString(repository))
	for _, f := range [][2]string{{"--thread", "codexThreadId"}, {"--model", "model"}, {"--actor", "actor"}} {
		if v, ok := option(args, f[0]); ok && v != "" {
			payload.Set(f[1], jsjson.MakeString(v))
		}
	}
	l, err = ledger.Append(l, obj("type", "SESSION_STARTED", "payload", jsjson.MakeObject(payload)), ledger.AppendOptions{})
	if err != nil {
		return err
	}
	if _, err := ledger.WriteAtomic(file, l); err != nil {
		return err
	}
	e.printJSON(obj("status", "RECORDING_STARTED", "ledger", file, "sessionId", sessionID, "headHash", headHashOf(l)))
	return nil
}

// recordCheckpoint ports the checkpoint case (src/cli-app.ts:1511-1522).
// The capture runs before appendLifecycleEventAtomic takes its lock.
func (e *env) recordCheckpoint(args []string) error {
	file := ledgerPathOption(args, "", false)
	rawRepo, err := requiredOption(args, "--repo")
	if err != nil {
		return err
	}
	repository := nodefs.Resolve(rawRepo)
	rawTurn, err := requiredOption(args, "--after-turn")
	if err != nil {
		return err
	}
	afterTurnOrdinal := jsjson.ToNumber(rawTurn)
	if !jsIsInteger(afterTurnOrdinal) || afterTurnOrdinal < 0 {
		return fail("--after-turn must be a non-negative integer.")
	}
	checkpoint, err := ledger.CaptureGitCleanCheckpoint(repository, ledger.CaptureOptions{})
	if err != nil {
		return err
	}
	l, err := ledger.AppendAtomic(file, obj("type", "WORKTREE_CHECKPOINT", "payload",
		obj("checkpoint", checkpoint, "afterTurnOrdinal", jsjson.MakeNumber(afterTurnOrdinal))), ledger.AppendOptions{})
	if err != nil {
		return err
	}
	e.printJSON(obj("status", "CHECKPOINT_RECORDED", "ledger", file, "headHash", headHashOf(l)))
	return nil
}

// recordAttach ports the attach case (src/cli-app.ts:1524-1562).
func (e *env) recordAttach(args []string) error {
	file := ledgerPathOption(args, "", false)
	repository, hasRepository, err := optionalRepository(args)
	if err != nil {
		return err
	}
	turnID, err := requiredOption(args, "--turn")
	if err != nil {
		return err
	}
	rawOrdinal, err := requiredOption(args, "--ordinal")
	if err != nil {
		return err
	}
	turnOrdinal := jsjson.ToNumber(rawOrdinal)
	if !jsIsInteger(turnOrdinal) || turnOrdinal <= 0 {
		return fail("--ordinal must be a positive integer.")
	}
	promptDigest, err := requiredOption(args, "--prompt-digest")
	if err != nil {
		return err
	}
	ordinal := jsjson.MakeNumber(turnOrdinal)
	l, err := ledger.AppendAtomic(file, obj("type", "TURN_STARTED", "payload",
		obj("turnId", turnID, "turnOrdinal", ordinal, "promptDigest", promptDigest)), ledger.AppendOptions{})
	if err != nil {
		return err
	}
	completed := jsjson.NewObj()
	completed.Set("turnId", jsjson.MakeString(turnID))
	completed.Set("turnOrdinal", ordinal)
	completed.Set("outcome", jsjson.MakeString(optionOr(args, "--outcome", "COMPLETED")))
	for _, f := range [][2]string{{"--output-digest", "outputDigest"}, {"--contribution", "contribution"}} {
		if v, ok := option(args, f[0]); ok && v != "" {
			completed.Set(f[1], jsjson.MakeString(v))
		}
	}
	l, err = ledger.AppendAtomic(file, obj("type", "TURN_COMPLETED", "payload", jsjson.MakeObject(completed)), ledger.AppendOptions{})
	if err != nil {
		return err
	}
	checkpointStatus := "NOT_REQUESTED"
	if hasFlag(args, "--checkpoint") {
		if !hasRepository {
			return fail("--repo is required when --checkpoint is used.")
		}
		checkpoint, err := ledger.CaptureGitCleanCheckpoint(repository, ledger.CaptureOptions{})
		if err != nil {
			return err
		}
		l, err = ledger.AppendAtomic(file, obj("type", "WORKTREE_CHECKPOINT", "payload",
			obj("checkpoint", checkpoint, "afterTurnOrdinal", ordinal)), ledger.AppendOptions{})
		if err != nil {
			return err
		}
		checkpointStatus = "RECORDED"
	}
	verification := ledger.Verify(l)
	status := "INVALID"
	if verification.Valid {
		status = "ATTACHED"
	}
	e.printJSON(spreadVerification(obj("status", status, "ledger", file, "turnId", turnID, "turnOrdinal", ordinal,
		"checkpoint", checkpointStatus, "limitation", recordLimitation), verification))
	e.setValidExit(verification)
	return nil
}

// recordStdin ports the stdin case (src/cli-app.ts:1564-1587): one locked
// append per NDJSON line, so a failure on line k leaves lines 1..k-1.
func (e *env) recordStdin(args []string) error {
	file := ledgerPathOption(args, "", false)
	repository, hasRepository, err := optionalRepository(args)
	if err != nil {
		return err
	}
	body, err := e.readStandardInput()
	if err != nil {
		return err
	}
	// split(/\r?\n/).map(trim).filter(Boolean): splitting on "\n" leaves a
	// "\r" only at a part's end, which the trim removes as the regex would.
	var lines []string
	for _, part := range strings.Split(body, "\n") {
		if line := jsstr.Trim(part); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return fail("Lifecycle stdin contained no NDJSON events.")
	}
	for _, line := range lines {
		envelope, err := jsjson.Parse(line)
		if err != nil {
			return fail("Lifecycle stdin contains invalid JSON: %s", err.Error())
		}
		candidate, err := lifecycleInputFromLine(envelope, repository, hasRepository)
		if err != nil {
			return err
		}
		input, err := bundle.ParseValue(candidate, gitproof.LifecycleEventInputSchema)
		if err != nil {
			return err
		}
		var options ledger.AppendOptions
		if envelope.Kind() == jsjson.Object {
			if id := envelope.Get("eventId"); id.Kind() == jsjson.String {
				options.EventID, options.HasEventID = id.Str(), true
			}
			if at := envelope.Get("occurredAt"); at.Kind() == jsjson.String {
				options.OccurredAt, options.HasOccurredAt = at.Str(), true
			}
		}
		if _, err := ledger.AppendAtomic(file, input, options); err != nil {
			return err
		}
	}
	verification := ledger.VerifyFile(file)
	status := "INVALID"
	if verification.Valid {
		status = "EVENTS_RECORDED"
	}
	e.printJSON(spreadVerification(obj("status", status, "ledger", file), verification))
	e.setValidExit(verification)
	return nil
}

// isJSObject is `typeof v === "object" && v !== null` (an array included).
func isJSObject(v jsjson.Value) bool {
	return v.Kind() == jsjson.Object || v.Kind() == jsjson.Array
}

// lifecycleInputFromLine ports src/cli-app.ts:1461. A parsed array has no
// "type", "payload" or "afterTurnOrdinal" (Get gives undefined), and
// `"checkpoint" in payload` is an own-key test for parsed JSON (an array's
// own keys are its indices and length).
func lifecycleInputFromLine(value jsjson.Value, repository string, hasRepository bool) (jsjson.Value, error) {
	if value.Kind() != jsjson.Object {
		return value, nil
	}
	event := value
	if candidate := value.Get("event"); isJSObject(candidate) {
		event = candidate
	}
	if t := event.Get("type"); t.Kind() != jsjson.String || t.Str() != "WORKTREE_CHECKPOINT" {
		return event, nil
	}
	payload := event.Get("payload")
	if !isJSObject(payload) {
		return event, nil
	}
	if payload.Kind() == jsjson.Object {
		if _, ok := payload.Obj().Get("checkpoint"); ok {
			return event, nil
		}
	}
	if !hasRepository {
		return jsjson.Value{}, fail("A --repo is required when stdin asks FaultLine to capture a WORKTREE_CHECKPOINT.")
	}
	afterTurnOrdinal := payload.Get("afterTurnOrdinal")
	checkpoint, err := ledger.CaptureGitCleanCheckpoint(repository, ledger.CaptureOptions{})
	if err != nil {
		return jsjson.Value{}, err
	}
	out := jsjson.NewObj()
	if afterTurnOrdinal.Kind() != jsjson.Undefined {
		out.Set("afterTurnOrdinal", afterTurnOrdinal)
	}
	out.Set("checkpoint", checkpoint)
	return obj("type", "WORKTREE_CHECKPOINT", "payload", jsjson.MakeObject(out)), nil
}
