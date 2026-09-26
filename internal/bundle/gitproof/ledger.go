package gitproof

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/ledger.ts:27-28.
const (
	codexLifecycleLedgerVersion = "faultline.codex-lifecycle-ledger.v1"
	gitCheckpointVersion        = "faultline.git-checkpoint.v1"
)

// Port of src/turn-snapshot.ts:54.
const turnTreeSnapshotVersion = "faultline.turn-tree-snapshot.v1"

var isoTimestamp = regexp.MustCompile(`^([+-]\d{6}|\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})\.(\d{3})Z$`)

const maxTimeMs = 8.64e15

func daysInMonth(year, month int64) int64 {
	switch month {
	case 2:
		if (year%4 == 0 && year%100 != 0) || year%400 == 0 {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// daysFromCivil is the proleptic Gregorian day number relative to 1970-01-01.
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// canonicalTimestamp ports the refine in src/ledger.ts:40:
// `new Date(value).toISOString() === value`. toISOString always emits this
// regex's shape, so a string round-trips only when it has that shape, its
// fields need no rollover, its year uses the form toISOString would choose,
// and the time value is within ±8.64e15 ms.
func canonicalTimestamp(value string) bool {
	m := isoTimestamp.FindStringSubmatch(value)
	if m == nil || m[1] == "-000000" {
		return false
	}
	year, _ := strconv.ParseInt(m[1], 10, 64)
	if len(m[1]) == 7 && year >= 0 && year <= 9999 {
		return false // toISOString writes these years with four digits
	}
	month, _ := strconv.ParseInt(m[2], 10, 64)
	day, _ := strconv.ParseInt(m[3], 10, 64)
	hour, _ := strconv.ParseInt(m[4], 10, 64)
	minute, _ := strconv.ParseInt(m[5], 10, 64)
	second, _ := strconv.ParseInt(m[6], 10, 64)
	ms, _ := strconv.ParseInt(m[7], 10, 64)
	if month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) || hour > 23 || minute > 59 || second > 59 {
		return false
	}
	t := daysFromCivil(year, month, day)*86_400_000 + ((hour*60+minute)*60+second)*1000 + ms
	return t >= -maxTimeMs && t <= maxTimeMs
}

// timestampMs is Date.parse for a value that already passed canonicalTimestamp.
func timestampMs(value string) int64 {
	m := isoTimestamp.FindStringSubmatch(value)
	if m == nil {
		return 0
	}
	n := func(i int) int64 { v, _ := strconv.ParseInt(m[i], 10, 64); return v }
	return daysFromCivil(n(1), n(2), n(3))*86_400_000 + ((n(4)*60+n(5))*60+n(6))*1000 + n(7)
}

func canonicalTimestampValue(v jsjson.Value) bool { return canonicalTimestamp(v.Str()) }

var (
	// src/ledger.ts:30-36.
	ledgerIdentifierSchema = s.String(s.MinLength(1), s.MaxLength(160), s.Regex(s.Pattern(codexThreadIDSource), "Identifier contains unsupported characters"))
	ledgerHashSchema       = s.String(s.Regex(s.Pattern(sha256DigestSource), "Expected a sha256 digest"))
	ledgerGitObjectID      = s.String(s.Regex(s.Pattern(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`), "Expected a Git object id"))

	// CanonicalTimestampSchema ports src/ledger.ts:40 (and src/turn-snapshot.ts:58).
	canonicalTimestampSchema = s.Refine(s.String(), canonicalTimestampValue, "Expected a canonical ISO-8601 UTC timestamp")

	codexTransportSchema  = s.Enum("CODEX_CLI", "CODEX_APP", "SIDE_CAR", "OBSERVED_EXTERNAL_TRANSPORT")
	turnOutcomeSchema     = s.Enum("COMPLETED", "FAILED", "INTERRUPTED")
	sessionEndReasonSchem = s.Enum("COMPLETED", "FAILED", "INTERRUPTED", "ABANDONED")

	// src/turn-snapshot.ts:66-78.
	unsignedTurnTreeSnapshotSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(turnTreeSnapshotVersion)),
		s.F("repositoryRoot", s.String(s.MinLength(1))),
		s.F("headCommit", ledgerGitObjectID),
		s.F("treeDigest", ledgerGitObjectID),
		s.F("capturedAt", canonicalTimestampSchema),
		s.F("dirty", s.Boolean()),
		s.F("statusDigest", ledgerHashSchema),
	).Strict()
	turnTreeSnapshotSchema = unsignedTurnTreeSnapshotSchema.Extend(s.F("digest", ledgerHashSchema)).Strict()

	// src/ledger.ts:59-155.
	sessionStartedPayloadSchema = s.Object(
		s.F("transport", codexTransportSchema),
		s.F("workingDirectory", s.String(s.MinLength(1))),
		s.F("codexThreadId", s.Optional(ledgerIdentifierSchema)),
		s.F("model", s.Optional(s.String(s.MinLength(1), s.MaxLength(200)))),
		s.F("actor", s.Optional(s.String(s.MinLength(1), s.MaxLength(200)))),
	).Strict()

	turnStartedPayloadSchema = s.Object(
		s.F("turnId", ledgerIdentifierSchema),
		s.F("turnOrdinal", positiveInt()),
		s.F("promptDigest", ledgerHashSchema),
	).Strict()

	turnCompletedPayloadSchema = s.Object(
		s.F("turnId", ledgerIdentifierSchema),
		s.F("turnOrdinal", positiveInt()),
		s.F("outcome", turnOutcomeSchema),
		s.F("outputDigest", s.Optional(ledgerHashSchema)),
		s.F("contribution", s.Optional(s.String(s.MinLength(1), s.MaxLength(240)))),
	).Strict()

	unsignedGitCheckpointSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(gitCheckpointVersion)),
		s.F("repositoryRoot", s.String(s.MinLength(1))),
		s.F("headCommit", ledgerGitObjectID),
		s.F("treeDigest", ledgerGitObjectID),
		s.F("capturedAt", canonicalTimestampSchema),
		s.F("clean", s.Literal(jsjson.MakeBool(true))),
	).Strict()

	gitCheckpointSchema = unsignedGitCheckpointSchema.Extend(s.F("digest", ledgerHashSchema)).Strict()

	worktreeCheckpointPayloadSchema = s.Object(
		s.F("checkpoint", gitCheckpointSchema),
		s.F("afterTurnOrdinal", nonnegativeInt()),
	).Strict()

	turnTreeSnapshotPayloadSchema = s.Object(
		s.F("turnId", ledgerIdentifierSchema),
		s.F("turnOrdinal", positiveInt()),
		s.F("snapshot", turnTreeSnapshotSchema),
	).Strict()

	sessionBaselineSnapshotPayloadSchema = s.Object(
		s.F("snapshot", turnTreeSnapshotSchema),
	).Strict()

	sessionEndedPayloadSchema = s.Object(
		s.F("reason", sessionEndReasonSchem),
		s.F("completedTurns", nonnegativeInt()),
	).Strict()

	toolUseAttributionPayloadSchema = s.Object(
		s.F("turnId", ledgerIdentifierSchema),
		s.F("turnOrdinal", positiveInt()),
		s.F("toolName", s.String(s.MinLength(1), s.MaxLength(160), s.Regex(s.Pattern(codexThreadIDSource), "toolName contains unsupported characters"))),
		s.F("toolCallId", s.Optional(ledgerIdentifierSchema)),
		s.F("allowlistedFieldsDigest", ledgerHashSchema),
	).Strict()

	lifecycleEventInputSchema = s.DiscriminatedUnion("type",
		eventOption("SESSION_STARTED", sessionStartedPayloadSchema),
		eventOption("SESSION_BASELINE_SNAPSHOT", sessionBaselineSnapshotPayloadSchema),
		eventOption("TURN_STARTED", turnStartedPayloadSchema),
		eventOption("TURN_COMPLETED", turnCompletedPayloadSchema),
		eventOption("TOOL_USE_STARTED", toolUseAttributionPayloadSchema),
		eventOption("TOOL_USE_COMPLETED", toolUseAttributionPayloadSchema),
		eventOption("WORKTREE_CHECKPOINT", worktreeCheckpointPayloadSchema),
		eventOption("TURN_TREE_SNAPSHOT", turnTreeSnapshotPayloadSchema),
		eventOption("SESSION_ENDED", sessionEndedPayloadSchema),
	)

	// src/ledger.ts:157.
	unsignedCodexLifecycleEventSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(codexLifecycleLedgerVersion)),
		s.F("ledgerId", ledgerIdentifierSchema),
		s.F("sessionId", ledgerIdentifierSchema),
		s.F("sequence", positiveInt()),
		s.F("eventId", ledgerIdentifierSchema),
		s.F("occurredAt", canonicalTimestampSchema),
		s.F("event", lifecycleEventInputSchema),
		s.F("previousHash", ledgerHashSchema),
	).Strict()

	codexLifecycleEventSchema = unsignedCodexLifecycleEventSchema.Extend(s.F("hash", ledgerHashSchema)).Strict()

	// CodexLifecycleLedgerSchema ports src/ledger.ts:172.
	CodexLifecycleLedgerSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(codexLifecycleLedgerVersion)),
		s.F("ledgerId", ledgerIdentifierSchema),
		s.F("sessionId", ledgerIdentifierSchema),
		s.F("createdAt", canonicalTimestampSchema),
		s.F("events", s.Array(codexLifecycleEventSchema)),
	).Strict()
)

func eventOption(eventType string, payload s.Schema) *s.ObjectSchema {
	return s.Object(s.F("type", s.LiteralString(eventType)), s.F("payload", payload)).Strict()
}

// LedgerVerification ports the LedgerVerification type (src/ledger.ts:205).
type LedgerVerification struct {
	Valid      bool
	Errors     []string
	EventCount int
	HeadHash   *string
}

// JSON is the TS result object.
func (l LedgerVerification) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("valid", jsjson.MakeBool(l.Valid))
	o.Set("errors", stringList(l.Errors))
	o.Set("eventCount", jsjson.MakeNumber(float64(l.EventCount)))
	o.Set("headHash", nullableString(l.HeadHash))
	return jsjson.MakeObject(o)
}

// issuePath is `issue.path.join(".") || "root"`.
func issuePath(issue s.Issue) string {
	var parts []string
	for _, p := range issue.Fields.Field("path").Items() {
		if p.Kind() == jsjson.Number {
			parts = append(parts, jsjson.FormatNumber(p.Num()))
		} else {
			parts = append(parts, p.Str())
		}
	}
	if joined := strings.Join(parts, "."); joined != "" {
		return joined
	}
	return "root"
}

func issueLines(prefix string, issues []s.Issue) []string {
	out := make([]string, len(issues))
	for i, issue := range issues {
		out[i] = prefix + issuePath(issue) + ": " + issue.Fields.Field("message").Str()
	}
	return out
}

// ledgerGenesisHash ports src/ledger.ts:222.
func ledgerGenesisHash(ledger jsjson.Value) string {
	o := jsjson.NewObj()
	o.Set("kind", jsjson.MakeString("FAULTLINE_CODEX_LIFECYCLE_GENESIS"))
	o.Set("schemaVersion", ledger.Get("schemaVersion"))
	o.Set("ledgerId", ledger.Get("ledgerId"))
	o.Set("sessionId", ledger.Get("sessionId"))
	o.Set("createdAt", ledger.Get("createdAt"))
	return bundle.Must(canonical.DigestJSON(jsjson.MakeObject(o)))
}

// hashLifecycleEvent ports src/ledger.ts:233.
func hashLifecycleEvent(event jsjson.Value) string {
	return bundle.Must(canonical.DigestJSON(bundle.Must(bundle.ParseValue(event, unsignedCodexLifecycleEventSchema))))
}

// verifyGitCheckpoint ports src/ledger.ts:242.
func verifyGitCheckpoint(value jsjson.Value) []string {
	parsed, issues, ok := s.Parse(gitCheckpointSchema, value)
	if !ok {
		return issueLines("Invalid Git checkpoint at ", issues)
	}
	if parsed.Get("digest").Str() == bundle.Must(canonical.DigestJSON(bundle.Without(parsed, "digest"))) {
		return nil
	}
	return []string{"Git checkpoint digest does not match its contents"}
}

// verifyTurnTreeSnapshot ports src/turn-snapshot.ts:295.
func verifyTurnTreeSnapshot(value jsjson.Value) []string {
	parsed, issues, ok := s.Parse(turnTreeSnapshotSchema, value)
	if !ok {
		return issueLines("Invalid turn tree snapshot at ", issues)
	}
	if parsed.Get("digest").Str() == bundle.Must(canonical.DigestJSON(bundle.Without(parsed, "digest"))) {
		return nil
	}
	return []string{"Turn tree snapshot digest does not match its contents"}
}

type activeTurn struct {
	id      string
	ordinal float64
}

// lifecycleState ports LifecycleState (src/ledger.ts:340).
type lifecycleState struct {
	started, ended, hasBaseline bool
	active                      *activeTurn
	completedTurnOrdinal        float64
	seenTurnIDs                 map[string]bool
	seenEventIDs                map[string]bool
}

func num(v jsjson.Value) string { return jsjson.FormatNumber(v.Num()) }

// validateLifecycleState ports src/ledger.ts:351.
func validateLifecycleState(event jsjson.Value, state *lifecycleState, index int, errs *[]string) {
	add := func(msg string) { *errs = append(*errs, msg) }
	eventType := event.Get("event", "type").Str()
	payload := event.Get("event", "payload")
	prefix := "Event " + num(event.Get("sequence")) + " (" + eventType + ")"
	eventID := event.Get("eventId").Str()
	if state.seenEventIDs[eventID] {
		add(prefix + " reuses event id " + eventID)
	}
	state.seenEventIDs[eventID] = true
	if state.ended {
		add(prefix + " occurs after SESSION_ENDED")
		return
	}
	occurredAt := timestampMs(event.Get("occurredAt").Str())
	switch eventType {
	case "SESSION_STARTED":
		if state.started {
			add(prefix + " starts a session that has already started")
		}
		if index != 0 {
			add(prefix + " must be the first event")
		}
		state.started = true
	case "SESSION_BASELINE_SNAPSHOT":
		switch {
		case !state.started:
			add(prefix + " occurs before SESSION_STARTED")
			return
		case state.hasBaseline:
			add(prefix + " repeats SESSION_BASELINE_SNAPSHOT")
			return
		case state.active != nil:
			add(prefix + " is not permitted while turn " + state.active.id + " is active")
			return
		case len(state.seenTurnIDs) > 0 || state.completedTurnOrdinal != 0:
			add(prefix + " must occur before any turn events")
			return
		}
		snapshot := payload.Get("snapshot")
		if timestampMs(snapshot.Get("capturedAt").Str()) > occurredAt {
			add(prefix + " is timestamped before its baseline tree snapshot was captured")
		}
		for _, e := range verifyTurnTreeSnapshot(snapshot) {
			add(prefix + ": " + e)
		}
		state.hasBaseline = true
	case "TURN_STARTED":
		if !state.started {
			add(prefix + " occurs before SESSION_STARTED")
			return
		}
		if state.active != nil {
			add(prefix + " starts turn " + payload.Get("turnId").Str() + " while " + state.active.id + " is active")
			return
		}
		turnID, turnOrdinal := payload.Get("turnId").Str(), payload.Get("turnOrdinal").Num()
		if state.seenTurnIDs[turnID] {
			add(prefix + " reuses turn id " + turnID)
		}
		expected := state.completedTurnOrdinal + 1
		if turnOrdinal != expected {
			add(prefix + " has turn ordinal " + jsjson.FormatNumber(turnOrdinal) + "; expected " + jsjson.FormatNumber(expected))
		}
		state.seenTurnIDs[turnID] = true
		state.active = &activeTurn{turnID, turnOrdinal}
	case "TURN_COMPLETED":
		if !state.started {
			add(prefix + " occurs before SESSION_STARTED")
			return
		}
		if state.active == nil {
			add(prefix + " has no matching active turn")
			return
		}
		turnID, turnOrdinal := payload.Get("turnId").Str(), payload.Get("turnOrdinal").Num()
		if turnID != state.active.id || turnOrdinal != state.active.ordinal {
			add(prefix + " does not match active turn " + state.active.id + "/" + jsjson.FormatNumber(state.active.ordinal))
			return
		}
		state.completedTurnOrdinal = turnOrdinal
		state.active = nil
	case "TOOL_USE_STARTED", "TOOL_USE_COMPLETED":
		if !state.started {
			add(prefix + " occurs before SESSION_STARTED")
			return
		}
		if state.active == nil {
			add(prefix + " requires an active turn")
			return
		}
		turnID, turnOrdinal := payload.Get("turnId").Str(), payload.Get("turnOrdinal").Num()
		if turnID != state.active.id || turnOrdinal != state.active.ordinal {
			add(prefix + " does not match active turn " + state.active.id + "/" + jsjson.FormatNumber(state.active.ordinal))
		}
	case "WORKTREE_CHECKPOINT":
		if !state.started {
			add(prefix + " occurs before SESSION_STARTED")
			return
		}
		if state.active != nil {
			add(prefix + " is not permitted while turn " + state.active.id + " is active")
			return
		}
		checkpoint, afterTurnOrdinal := payload.Get("checkpoint"), payload.Get("afterTurnOrdinal").Num()
		if afterTurnOrdinal != state.completedTurnOrdinal {
			add(prefix + " claims after turn " + jsjson.FormatNumber(afterTurnOrdinal) + "; expected " + jsjson.FormatNumber(state.completedTurnOrdinal))
		}
		if timestampMs(checkpoint.Get("capturedAt").Str()) > occurredAt {
			add(prefix + " is timestamped before its Git checkpoint was captured")
		}
		for _, e := range verifyGitCheckpoint(checkpoint) {
			add(prefix + ": " + e)
		}
	case "TURN_TREE_SNAPSHOT":
		if !state.started {
			add(prefix + " occurs before SESSION_STARTED")
			return
		}
		if state.active != nil {
			add(prefix + " is not permitted while turn " + state.active.id + " is active")
			return
		}
		turnID, turnOrdinal, snapshot := payload.Get("turnId").Str(), payload.Get("turnOrdinal").Num(), payload.Get("snapshot")
		if !state.seenTurnIDs[turnID] {
			add(prefix + " references a turn id that was never started: " + turnID)
		}
		if turnOrdinal != state.completedTurnOrdinal {
			add(prefix + " claims turn ordinal " + jsjson.FormatNumber(turnOrdinal) + "; expected " + jsjson.FormatNumber(state.completedTurnOrdinal))
		}
		if timestampMs(snapshot.Get("capturedAt").Str()) > occurredAt {
			add(prefix + " is timestamped before its turn tree snapshot was captured")
		}
		for _, e := range verifyTurnTreeSnapshot(snapshot) {
			add(prefix + ": " + e)
		}
	case "SESSION_ENDED":
		if !state.started {
			add(prefix + " occurs before SESSION_STARTED")
			return
		}
		if state.active != nil {
			add(prefix + " ends while turn " + state.active.id + " is active")
			return
		}
		if completed := payload.Get("completedTurns").Num(); completed != state.completedTurnOrdinal {
			add(prefix + " reports " + jsjson.FormatNumber(completed) + " completed turns; expected " + jsjson.FormatNumber(state.completedTurnOrdinal))
		}
		state.ended = true
	}
}

// VerifyCodexLifecycleLedger ports verifyCodexLifecycleLedger (src/ledger.ts:505).
func VerifyCodexLifecycleLedger(value jsjson.Value) LedgerVerification {
	ledger, issues, ok := s.Parse(CodexLifecycleLedgerSchema, value)
	if !ok {
		return LedgerVerification{Errors: issueLines("Invalid ledger at ", issues)}
	}
	var errs []string
	expectedPreviousHash := ledgerGenesisHash(ledger)
	previousTimestamp := ledger.Get("createdAt").Str()
	state := &lifecycleState{seenTurnIDs: map[string]bool{}, seenEventIDs: map[string]bool{}}
	events := ledger.Get("events").Items()
	for index, event := range events {
		sequence := num(event.Get("sequence"))
		expectedSequence := strconv.Itoa(index + 1)
		if event.Get("sequence").Num() != float64(index+1) {
			errs = append(errs, "Event at index "+strconv.Itoa(index)+" has sequence "+sequence+"; expected "+expectedSequence)
		}
		if event.Get("ledgerId").Str() != ledger.Get("ledgerId").Str() {
			errs = append(errs, "Event "+sequence+" has ledger id "+event.Get("ledgerId").Str()+"; expected "+ledger.Get("ledgerId").Str())
		}
		if event.Get("sessionId").Str() != ledger.Get("sessionId").Str() {
			errs = append(errs, "Event "+sequence+" has session id "+event.Get("sessionId").Str()+"; expected "+ledger.Get("sessionId").Str())
		}
		if event.Get("previousHash").Str() != expectedPreviousHash {
			errs = append(errs, "Event "+sequence+" previous hash does not match its predecessor")
		}
		unsigned := jsjson.NewObj()
		for _, k := range []string{"schemaVersion", "ledgerId", "sessionId", "sequence", "eventId", "occurredAt", "event", "previousHash"} {
			unsigned.Set(k, event.Get(k))
		}
		if event.Get("hash").Str() != hashLifecycleEvent(jsjson.MakeObject(unsigned)) {
			errs = append(errs, "Event "+sequence+" hash does not match its contents")
		}
		if timestampMs(event.Get("occurredAt").Str()) < timestampMs(previousTimestamp) {
			errs = append(errs, "Event "+sequence+" occurs before the preceding ledger timestamp")
		}
		validateLifecycleState(event, state, index, &errs)
		expectedPreviousHash = event.Get("hash").Str()
		previousTimestamp = event.Get("occurredAt").Str()
	}
	head := ledgerGenesisHash(ledger)
	if len(events) > 0 {
		head = events[len(events)-1].Get("hash").Str()
	}
	return LedgerVerification{Valid: len(errs) == 0, Errors: errs, EventCount: len(events), HeadHash: &head}
}
