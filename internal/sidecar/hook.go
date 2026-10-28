package sidecar

import (
	"errors"
	"io"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/ledger"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	s "github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/turnsnap"
)

// turnSnapshotObjectDBWarning is TURN_SNAPSHOT_OBJECT_DB_WARNING
// (codex-sidecar.ts:39), the three lines joined with " ".
const turnSnapshotObjectDBWarning = "FaultLine: turn-tree snapshots write Git objects into this repository's object database" +
	" and may include eligible untracked files. Prefer .faultlineignore exclusions or set" +
	" FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY=1 for tracked-files-only capture."

// The hook schemas of codex-sidecar.ts:68-113. z.object (no .strict())
// drops unknown keys, as s.Object does.
var (
	hookBaseSchema = s.Object(
		s.F("session_id", IdentifierSchema),
		s.F("cwd", s.String(s.MinLength(1))),
		s.F("model", s.String(s.MinLength(1), s.MaxLength(200))),
	)
	sessionStartHookSchema = hookBaseSchema.Extend(
		s.F("hook_event_name", s.LiteralString("SessionStart")),
	)
	userPromptSubmitHookSchema = hookBaseSchema.Extend(
		s.F("hook_event_name", s.LiteralString("UserPromptSubmit")),
		s.F("turn_id", IdentifierSchema),
		// This value is digested in memory and is never written to disk.
		s.F("prompt", s.String(s.MaxLength(4*1024*1024))),
	)
	stopHookSchema = hookBaseSchema.Extend(
		s.F("hook_event_name", s.LiteralString("Stop")),
		s.F("turn_id", IdentifierSchema),
	)
	toolNameSchema       = s.String(s.MinLength(1), s.MaxLength(160), s.Regex(s.Pattern(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`), ""))
	preToolUseHookSchema = hookBaseSchema.Extend(
		s.F("hook_event_name", s.LiteralString("PreToolUse")),
		s.F("turn_id", IdentifierSchema),
		s.F("tool_name", toolNameSchema),
		s.F("tool_call_id", s.Optional(IdentifierSchema)),
	)
	postToolUseHookSchema = hookBaseSchema.Extend(
		s.F("hook_event_name", s.LiteralString("PostToolUse")),
		s.F("turn_id", IdentifierSchema),
		s.F("tool_name", toolNameSchema),
		s.F("tool_call_id", s.Optional(IdentifierSchema)),
	)
)

// HookInputSchema ports CodexSidecarHookInputSchema (codex-sidecar.ts:107).
var HookInputSchema s.Schema = s.DiscriminatedUnion("hook_event_name",
	sessionStartHookSchema, userPromptSubmitHookSchema, stopHookSchema, preToolUseHookSchema, postToolUseHookSchema)

// hookInput is a parsed CodexSidecarHookInput; toolCallID nil is undefined.
type hookInput struct {
	eventName, sessionID, cwd, model string
	turnID, prompt, toolName         string
	toolCallID                       *string
}

func parseHookInput(value jsjson.Value) hookInput {
	v := bundle.Must(bundle.ParseValue(value, HookInputSchema))
	in := hookInput{
		eventName: v.Get("hook_event_name").Str(),
		sessionID: v.Get("session_id").Str(),
		cwd:       v.Get("cwd").Str(),
		model:     v.Get("model").Str(),
		turnID:    v.Get("turn_id").Str(),
		prompt:    v.Get("prompt").Str(),
		toolName:  v.Get("tool_name").Str(),
	}
	if id := v.Get("tool_call_id"); id.Kind() == jsjson.String {
		value := id.Str()
		in.toolCallID = &value
	}
	return in
}

// Result ports CodexSidecarResult (codex-sidecar.ts:128). Optional members
// are omitted from JSON() when empty / nil.
type Result struct {
	Status           string // SESSION_STARTED, TURN_STARTED, TOOL_USE_STARTED, TOOL_USE_COMPLETED, CHECKPOINT_RECORDED, CHECKPOINT_SKIPPED_DIRTY, CHECKPOINT_SKIPPED_UNAVAILABLE, TURN_SNAPSHOT_RECORDED
	LedgerPath       string
	SessionID        string
	TurnID           string // "" is absent
	ToolName         string // "" is absent
	Idempotent       bool
	CheckpointDigest string       // "" is absent
	Reason           string       // "" is absent: DIRTY_WORKTREE or CHECKPOINT_UNAVAILABLE
	TurnSnapshot     jsjson.Value // Undefined kind is absent
}

// JSON is the result object in TS key order: status, ledgerPath,
// sessionId, turnId?, toolName?, idempotent, checkpointDigest?, reason?,
// turnSnapshot?. Every result literal of codex-sidecar.ts (:569, :571,
// :590, :598, :612, :765, :777, :795, :807, :957) is a subsequence of it.
func (r Result) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("status", jsjson.MakeString(r.Status))
	o.Set("ledgerPath", jsjson.MakeString(r.LedgerPath))
	o.Set("sessionId", jsjson.MakeString(r.SessionID))
	if r.TurnID != "" {
		o.Set("turnId", jsjson.MakeString(r.TurnID))
	}
	if r.ToolName != "" {
		o.Set("toolName", jsjson.MakeString(r.ToolName))
	}
	o.Set("idempotent", jsjson.MakeBool(r.Idempotent))
	if r.CheckpointDigest != "" {
		o.Set("checkpointDigest", jsjson.MakeString(r.CheckpointDigest))
	}
	if r.Reason != "" {
		o.Set("reason", jsjson.MakeString(r.Reason))
	}
	if r.TurnSnapshot.Kind() != jsjson.Undefined {
		o.Set("turnSnapshot", r.TurnSnapshot)
	}
	return jsjson.MakeObject(o)
}

// captureSidecarTurnTreeSnapshot ports codex-sidecar.ts:45. The cache path
// is computed first (its Git failure is a TurnSnapshotError too).
func captureSidecarTurnTreeSnapshot(cwd string, stderr io.Writer) (jsjson.Value, error) {
	value, ok := nodeproc.Getenv("FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY")
	trackedFilesOnly := ok && value == "1"
	cachePath, err := turnsnap.RepoCachePath(cwd)
	if err != nil {
		return jsjson.Value{}, err
	}
	snapshot, _, err := turnsnap.Capture(cwd, turnsnap.CaptureOptions{
		TrackedFilesOnly: trackedFilesOnly,
		SessionCachePath: cachePath,
		OnWarning: func(warning string) {
			io.WriteString(stderr, jsstr.ToUTF8(jsexc.Concat("FaultLine snapshot warning: ", warning, "\n")))
		},
	})
	return snapshot, err
}

func isTurnSnapshotError(err error) bool {
	var e *turnsnap.Error
	return errors.As(err, &e)
}

// EventID ports sidecarEventId (codex-sidecar.ts:344); turnID nil is null.
func EventID(sessionID string, turnID *string, phase string) string {
	sessionKey := canonical.SHA256Hex(sessionID)[:24]
	turnKey := "session"
	if turnID != nil {
		turnKey = canonical.SHA256Hex(*turnID)[:24]
	}
	return jsexc.Concat("codex-sidecar-", sessionKey, "-", turnKey, "-", phase)
}

// DigestToolAllowlistedFields ports digestToolAllowlistedFields
// (codex-sidecar.ts:363); toolCallID nil is undefined.
func DigestToolAllowlistedFields(sessionID, turnID, toolName string, toolCallID *string) string {
	o := jsjson.NewObj()
	o.Set("sessionId", jsjson.MakeString(sessionID))
	o.Set("turnId", jsjson.MakeString(turnID))
	o.Set("toolName", jsjson.MakeString(toolName))
	if toolCallID != nil {
		o.Set("toolCallId", jsjson.MakeString(*toolCallID))
	}
	return bundle.Must(canonical.DigestJSON(jsjson.MakeObject(o)))
}

// events is ledger.events.
func events(l jsjson.Value) []jsjson.Value { return l.Get("events").Items() }

// eventByID ports codex-sidecar.ts:381: Undefined when absent.
func eventByID(l jsjson.Value, eventID string) jsjson.Value {
	for _, e := range events(l) {
		if e.Get("eventId").Str() == eventID {
			return e
		}
	}
	return jsjson.Value{}
}

// firstEvent is ledger.events.find(event => event.event.type === type && pred).
func firstEvent(l jsjson.Value, eventType string, pred func(jsjson.Value) bool) jsjson.Value {
	for _, e := range events(l) {
		if e.Get("event", "type").Str() == eventType && (pred == nil || pred(e)) {
			return e
		}
	}
	return jsjson.Value{}
}

func turnIDIs(turnID string) func(jsjson.Value) bool {
	return func(e jsjson.Value) bool { return e.Get("event", "payload", "turnId").Str() == turnID }
}

// sessionStartedEvent ports codex-sidecar.ts:385.
func sessionStartedEvent(l jsjson.Value) jsjson.Value {
	e := firstEvent(l, "SESSION_STARTED", nil)
	if e.Kind() == jsjson.Undefined {
		fail("The sidecar ledger has no SESSION_STARTED event.")
	}
	return e
}

// sessionStartedPayload is {transport: "SIDE_CAR", workingDirectory, model}.
func sessionStartedPayload(cwd, model string) jsjson.Value {
	return jsjson.MustObject("transport", jsjson.MakeString("SIDE_CAR"), "workingDirectory", jsjson.MakeString(cwd), "model", jsjson.MakeString(model))
}

// assertExistingSession ports codex-sidecar.ts:393.
func assertExistingSession(l jsjson.Value, in hookInput, cwd string) {
	if l.Get("sessionId").Str() != in.sessionID {
		fail("The existing sidecar ledger belongs to a different Codex session.")
	}
	started := sessionStartedEvent(l)
	if !bundle.SameCanonical(started.Get("event", "payload"), sessionStartedPayload(cwd, in.model)) {
		fail("The existing sidecar ledger does not match this Codex session's cwd or model.")
	}
}

// createStartedLedger ports codex-sidecar.ts:408.
func createStartedLedger(in hookInput, cwd string) jsjson.Value {
	l := bundle.Must(ledger.Create(ledger.CreateOptions{
		LedgerID:  jsexc.Concat("ledger-codex-sidecar-", canonical.SHA256Hex(in.sessionID)[:32]),
		SessionID: in.sessionID,
	}))
	input := jsjson.MustObject("type", jsjson.MakeString("SESSION_STARTED"), "payload", sessionStartedPayload(cwd, in.model))
	return bundle.Must(ledger.Append(l, input, ledger.AppendOptions{EventID: EventID(in.sessionID, nil, "session-start")}))
}

// appendIfAbsent ports codex-sidecar.ts:419: an event with the id must
// carry the same lifecycle facts as input (the raw input, canonically).
func appendIfAbsent(l, input jsjson.Value, eventID string) (jsjson.Value, bool) {
	if existing := eventByID(l, eventID); existing.Kind() != jsjson.Undefined {
		if !bundle.SameCanonical(existing.Get("event"), input) {
			fail(jsexc.Concat("A sidecar event id is already bound to different lifecycle facts: ", eventID))
		}
		return l, true
	}
	return bundle.Must(ledger.Append(l, input, ledger.AppendOptions{EventID: eventID})), false
}

// turnOrdinalForStartedTurn ports codex-sidecar.ts:437.
func turnOrdinalForStartedTurn(l jsjson.Value, turnID string) float64 {
	e := firstEvent(l, "TURN_STARTED", turnIDIs(turnID))
	if e.Kind() == jsjson.Undefined {
		fail("Stop was observed before the matching UserPromptSubmit event.")
	}
	return e.Get("event", "payload", "turnOrdinal").Num()
}

// turnCompletionEvent ports codex-sidecar.ts:445.
func turnCompletionEvent(l jsjson.Value, turnID string) jsjson.Value {
	return firstEvent(l, "TURN_COMPLETED", turnIDIs(turnID))
}

// turnCompletedInput is {type: "TURN_COMPLETED", payload: {turnId, turnOrdinal, outcome: "COMPLETED"}}.
func turnCompletedInput(turnID string, turnOrdinal float64) jsjson.Value {
	return jsjson.MustObject("type", jsjson.MakeString("TURN_COMPLETED"), "payload", jsjson.MustObject(
		"turnId", jsjson.MakeString(turnID), "turnOrdinal", jsjson.MakeNumber(turnOrdinal), "outcome", jsjson.MakeString("COMPLETED")))
}

// readSidecarLedger ports codex-sidecar.ts:547.
func readSidecarLedger(ledgerPath string) jsjson.Value {
	safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger")
	return bundle.Must(ledger.ReadVerified(ledgerPath))
}

// writeSidecarLedger ports codex-sidecar.ts:552.
func writeSidecarLedger(ledgerPath string, l jsjson.Value) {
	safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger")
	bundle.Must(ledger.WriteAtomic(ledgerPath, l))
}

// checkpointResult ports codex-sidecar.ts:557; checkpoint and turnSnapshot
// Undefined are absent.
func checkpointResult(status, ledgerPath, sessionID, turnID string, idempotent bool, checkpoint, turnSnapshot jsjson.Value) Result {
	r := Result{Status: status, LedgerPath: ledgerPath, SessionID: sessionID, TurnID: turnID, Idempotent: idempotent, TurnSnapshot: turnSnapshot}
	if status == "CHECKPOINT_RECORDED" {
		if checkpoint.Kind() == jsjson.Undefined {
			fail("A recorded checkpoint result requires a checkpoint digest.")
		}
		r.CheckpointDigest = checkpoint.Get("digest").Str()
		return r
	}
	r.Reason = "CHECKPOINT_UNAVAILABLE"
	if status == "CHECKPOINT_SKIPPED_DIRTY" {
		r.Reason = "DIRTY_WORKTREE"
	}
	return r
}

// turnSnapshotOnlyResult ports codex-sidecar.ts:583.
func turnSnapshotOnlyResult(ledgerPath, sessionID, turnID string, idempotent bool, turnSnapshot jsjson.Value) Result {
	return Result{Status: "TURN_SNAPSHOT_RECORDED", LedgerPath: ledgerPath, SessionID: sessionID, TurnID: turnID, Idempotent: idempotent, TurnSnapshot: turnSnapshot}
}

// checkpointForTurn ports codex-sidecar.ts:661: Undefined when absent.
func checkpointForTurn(l jsjson.Value, in hookInput) jsjson.Value {
	checkpoint := eventByID(l, EventID(in.sessionID, &in.turnID, "checkpoint"))
	if checkpoint.Kind() == jsjson.Undefined {
		return jsjson.Value{}
	}
	turnOrdinal := turnOrdinalForStartedTurn(l, in.turnID)
	if checkpoint.Get("event", "type").Str() != "WORKTREE_CHECKPOINT" || checkpoint.Get("event", "payload", "afterTurnOrdinal").Num() != turnOrdinal {
		fail("The sidecar checkpoint does not match its completed Codex turn.")
	}
	return checkpoint.Get("event", "payload", "checkpoint")
}

// turnSnapshotForTurn ports codex-sidecar.ts:674: Undefined when absent.
func turnSnapshotForTurn(l jsjson.Value, in hookInput) jsjson.Value {
	e := eventByID(l, EventID(in.sessionID, &in.turnID, "turn-snapshot"))
	if e.Kind() == jsjson.Undefined {
		return jsjson.Value{}
	}
	turnOrdinal := turnOrdinalForStartedTurn(l, in.turnID)
	if e.Get("event", "type").Str() != "TURN_TREE_SNAPSHOT" || e.Get("event", "payload", "turnOrdinal").Num() != turnOrdinal {
		fail("The sidecar turn tree snapshot does not match its completed Codex turn.")
	}
	return e.Get("event", "payload", "snapshot")
}

// hasMatchingCompletedTurn ports codex-sidecar.ts:687.
func hasMatchingCompletedTurn(l jsjson.Value, in hookInput) bool {
	completion := turnCompletionEvent(l, in.turnID)
	if completion.Kind() == jsjson.Undefined {
		return false
	}
	if !bundle.SameCanonical(completion.Get("event"), turnCompletedInput(in.turnID, turnOrdinalForStartedTurn(l, in.turnID))) {
		fail("The existing completed-turn record does not match this Codex Stop event.")
	}
	return true
}

// recordSessionStart ports codex-sidecar.ts:745.
func recordSessionStart(in hookInput, cwd, ledgerPath string, stderr io.Writer) Result {
	if !safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger") {
		l := createStartedLedger(in, cwd)
		// Capture turn-zero with the same dirty-safe protocol later turns
		// use at Stop; idempotent redeliveries do not recapture.
		io.WriteString(stderr, turnSnapshotObjectDBWarning+"\n")
		baselineSnapshot, err := captureSidecarTurnTreeSnapshot(cwd, stderr)
		if err != nil {
			if isTurnSnapshotError(err) {
				fail(jsexc.Concat("SessionStart baseline snapshot failed: ", jsexc.Message(err)))
			}
			jsexc.Throw(err)
		}
		l = bundle.Must(ledger.Append(l, jsjson.MustObject(
			"type", jsjson.MakeString("SESSION_BASELINE_SNAPSHOT"),
			"payload", jsjson.MustObject("snapshot", baselineSnapshot),
		), ledger.AppendOptions{EventID: EventID(in.sessionID, nil, "session-baseline")}))
		writeSidecarLedger(ledgerPath, l)
		return Result{Status: "SESSION_STARTED", LedgerPath: ledgerPath, SessionID: in.sessionID, Idempotent: false}
	}
	l := readSidecarLedger(ledgerPath)
	assertExistingSession(l, in, cwd)
	expected := jsjson.MustObject("type", jsjson.MakeString("SESSION_STARTED"), "payload", sessionStartedPayload(cwd, in.model))
	existing := eventByID(l, EventID(in.sessionID, nil, "session-start"))
	if existing.Kind() == jsjson.Undefined || !bundle.SameCanonical(existing.Get("event"), expected) {
		fail("The existing ledger's SESSION_STARTED event is not the expected Codex sidecar observation.")
	}
	return Result{Status: "SESSION_STARTED", LedgerPath: ledgerPath, SessionID: in.sessionID, Idempotent: true}
}

// recordTurnStart ports codex-sidecar.ts:780.
func recordTurnStart(in hookInput, cwd, ledgerPath string) Result {
	if !safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger") {
		fail("UserPromptSubmit was observed before SessionStart initialized the sidecar ledger.")
	}
	l := readSidecarLedger(ledgerPath)
	assertExistingSession(l, in, cwd)
	previousActive := firstEvent(l, "TURN_STARTED", func(e jsjson.Value) bool {
		return turnCompletionEvent(l, e.Get("event", "payload", "turnId").Str()).Kind() == jsjson.Undefined
	})
	promptDigest := jsexc.Concat("sha256:", canonical.SHA256Hex(in.prompt))
	turnStartID := EventID(in.sessionID, &in.turnID, "turn-start")
	turnStarted := func(ordinal float64) jsjson.Value {
		return jsjson.MustObject("type", jsjson.MakeString("TURN_STARTED"), "payload", jsjson.MustObject(
			"turnId", jsjson.MakeString(in.turnID), "turnOrdinal", jsjson.MakeNumber(ordinal), "promptDigest", jsjson.MakeString(promptDigest)))
	}
	if existingStart := eventByID(l, turnStartID); existingStart.Kind() != jsjson.Undefined {
		ordinal := -1.0
		if existingStart.Get("event", "type").Str() == "TURN_STARTED" {
			ordinal = existingStart.Get("event", "payload", "turnOrdinal").Num()
		}
		if !bundle.SameCanonical(existingStart.Get("event"), turnStarted(ordinal)) {
			fail("The existing TURN_STARTED record does not match this Codex hook observation.")
		}
		return Result{Status: "TURN_STARTED", LedgerPath: ledgerPath, SessionID: in.sessionID, TurnID: in.turnID, Idempotent: true}
	}
	if previousActive.Kind() != jsjson.Undefined {
		fail("A different Codex turn is still active in this sidecar ledger.")
	}
	completedTurns := 0
	for _, e := range events(l) {
		if e.Get("event", "type").Str() == "TURN_COMPLETED" {
			completedTurns++
		}
	}
	l, idempotent := appendIfAbsent(l, turnStarted(float64(completedTurns+1)), turnStartID)
	writeSidecarLedger(ledgerPath, l)
	return Result{Status: "TURN_STARTED", LedgerPath: ledgerPath, SessionID: in.sessionID, TurnID: in.turnID, Idempotent: idempotent}
}

// isDirtyCheckpointError ports codex-sidecar.ts:810.
func isDirtyCheckpointError(err error) bool {
	var e *ledger.IntegrityError
	return errors.As(err, &e) && e.Message == ledger.ErrDirtyWorktree
}

// recordTurnStop ports codex-sidecar.ts:814.
func recordTurnStop(in hookInput, cwd, ledgerPath, directory string, stderr io.Writer) Result {
	if !safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger") {
		fail("Stop was observed before SessionStart initialized the sidecar ledger.")
	}
	l := readSidecarLedger(ledgerPath)
	assertExistingSession(l, in, cwd)
	if receipt := readStopReceipt(stopReceiptPath(directory, in.sessionID, in.turnID)); receipt.Kind() != jsjson.Undefined {
		if receipt.Get("sessionId").Str() != in.sessionID || receipt.Get("turnId").Str() != in.turnID {
			fail("The sidecar stop receipt belongs to different Codex lifecycle facts.")
		}
		assertReceiptMatchesLedger(receipt, l, in.sessionID, in.turnID)
		return resultFromReceipt(receipt, l, ledgerPath, in)
	}

	if existingCheckpoint := checkpointForTurn(l, in); existingCheckpoint.Kind() != jsjson.Undefined {
		// The ledger is the durable proof record: never capture a later
		// state for the same observed Stop event.
		recovered := checkpointResult("CHECKPOINT_RECORDED", ledgerPath, in.sessionID, in.turnID, true, existingCheckpoint, turnSnapshotForTurn(l, in))
		writeReceipt(directory, in, recovered)
		return recovered
	}
	if hasMatchingCompletedTurn(l, in) {
		if existingSnapshot := turnSnapshotForTurn(l, in); existingSnapshot.Kind() != jsjson.Undefined {
			recovered := turnSnapshotOnlyResult(ledgerPath, in.sessionID, in.turnID, true, existingSnapshot)
			writeReceipt(directory, in, recovered)
			return recovered
		}
		// An interrupted transaction: preserve an unavailable outcome.
		recovered := checkpointResult("CHECKPOINT_SKIPPED_UNAVAILABLE", ledgerPath, in.sessionID, in.turnID, true, jsjson.Value{}, jsjson.Value{})
		writeReceipt(directory, in, recovered)
		return recovered
	}

	turnOrdinal := turnOrdinalForStartedTurn(l, in.turnID)
	l, completionIdempotent := appendIfAbsent(l, turnCompletedInput(in.turnID, turnOrdinal), EventID(in.sessionID, &in.turnID, "turn-stop"))

	// A turn tree snapshot never requires a clean worktree, so it is
	// always attempted first; its failure does not block the checkpoint.
	snapshotIdempotent := true
	turnSnapshot, err := captureSidecarTurnTreeSnapshot(cwd, stderr)
	if err != nil {
		if !isTurnSnapshotError(err) {
			jsexc.Throw(err)
		}
		io.WriteString(stderr, jsstr.ToUTF8(jsexc.Concat("FaultLine turn-tree snapshot skipped: ", jsexc.Message(err), "\n")))
		turnSnapshot = jsjson.Value{}
	}
	if turnSnapshot.Kind() != jsjson.Undefined {
		snapshotInput := jsjson.MustObject("type", jsjson.MakeString("TURN_TREE_SNAPSHOT"), "payload", jsjson.MustObject(
			"turnId", jsjson.MakeString(in.turnID), "turnOrdinal", jsjson.MakeNumber(turnOrdinal), "snapshot", turnSnapshot))
		l, snapshotIdempotent = appendIfAbsent(l, snapshotInput, EventID(in.sessionID, &in.turnID, "turn-snapshot"))
	}

	checkpoint, err := ledger.CaptureGitCleanCheckpoint(cwd, ledger.CaptureOptions{
		RunGit: func(repository string, args []string) (string, error) { return runHardenedGit(repository, args...) },
	})
	if err != nil {
		// Persist the completed turn (and any captured snapshot) even when
		// no clean Git state can be observed.
		writeSidecarLedger(ledgerPath, l)
		idempotent := completionIdempotent && snapshotIdempotent
		status := "CHECKPOINT_SKIPPED_UNAVAILABLE"
		if isDirtyCheckpointError(err) {
			status = "CHECKPOINT_SKIPPED_DIRTY"
		}
		result := checkpointResult(status, ledgerPath, in.sessionID, in.turnID, idempotent, jsjson.Value{}, turnSnapshot)
		writeReceipt(directory, in, result)
		return result
	}
	checkpointInput := jsjson.MustObject("type", jsjson.MakeString("WORKTREE_CHECKPOINT"), "payload", jsjson.MustObject(
		"checkpoint", checkpoint, "afterTurnOrdinal", jsjson.MakeNumber(turnOrdinal)))
	l, checkpointIdempotent := appendIfAbsent(l, checkpointInput, EventID(in.sessionID, &in.turnID, "checkpoint"))
	writeSidecarLedger(ledgerPath, l)
	result := checkpointResult("CHECKPOINT_RECORDED", ledgerPath, in.sessionID, in.turnID,
		completionIdempotent && checkpointIdempotent && snapshotIdempotent, checkpoint, turnSnapshot)
	writeReceipt(directory, in, result)
	return result
}

// recordToolUse ports codex-sidecar.ts:923.
func recordToolUse(in hookInput, cwd, ledgerPath string) Result {
	if !safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger") {
		fail(jsexc.Concat(in.eventName, " was observed before SessionStart initialized the sidecar ledger."))
	}
	l := readSidecarLedger(ledgerPath)
	assertExistingSession(l, in, cwd)
	turnOrdinal := turnOrdinalForStartedTurn(l, in.turnID)
	allowlistedFieldsDigest := DigestToolAllowlistedFields(in.sessionID, in.turnID, in.toolName, in.toolCallID)
	eventType, prefix := "TOOL_USE_COMPLETED", "tool-post-"
	if in.eventName == "PreToolUse" {
		eventType, prefix = "TOOL_USE_STARTED", "tool-pre-"
	}
	var callKey string
	if in.toolCallID != nil {
		callKey = *in.toolCallID
	} else {
		callKey = jsexc.Concat("anon-", canonical.SHA256Hex(jsexc.Concat(in.toolName, "\x00", in.turnID))[:16])
	}
	payload := jsjson.NewObj()
	payload.Set("turnId", jsjson.MakeString(in.turnID))
	payload.Set("turnOrdinal", jsjson.MakeNumber(turnOrdinal))
	payload.Set("toolName", jsjson.MakeString(in.toolName))
	payload.Set("allowlistedFieldsDigest", jsjson.MakeString(allowlistedFieldsDigest))
	if in.toolCallID != nil {
		payload.Set("toolCallId", jsjson.MakeString(*in.toolCallID))
	}
	input := jsjson.MustObject("type", jsjson.MakeString(eventType), "payload", jsjson.MakeObject(payload))
	l, idempotent := appendIfAbsent(l, input, EventID(in.sessionID, &in.turnID, jsexc.Concat(prefix, callKey)))
	writeSidecarLedger(ledgerPath, l)
	return Result{Status: eventType, LedgerPath: ledgerPath, SessionID: in.sessionID, TurnID: in.turnID, ToolName: in.toolName, Idempotent: idempotent}
}

// RecordObservedHook ports recordObservedCodexHook (codex-sidecar.ts:972).
// stderr receives what TS writes to process.stderr during the call (the
// object-database warning, snapshot warnings, a skipped turn snapshot), in
// order. Unknown hook properties are discarded before any record is made.
func RecordObservedHook(value jsjson.Value, stderr io.Writer) (result Result, err error) {
	defer jsexc.Catch(func(e error) { result, err = Result{}, e })
	in := parseHookInput(value)
	cwd := nodefs.Resolve(in.cwd)
	directory := recordingDirectory(cwd, true)
	ledgerPath := nodefs.Join(directory, ledgerFileName(in.sessionID))
	return withSidecarLock(ledgerPath, func() Result {
		switch in.eventName {
		case "SessionStart":
			return recordSessionStart(in, cwd, ledgerPath, stderr)
		case "UserPromptSubmit":
			return recordTurnStart(in, cwd, ledgerPath)
		case "Stop":
			return recordTurnStop(in, cwd, ledgerPath, directory, stderr)
		}
		return recordToolUse(in, cwd, ledgerPath)
	}), nil
}
