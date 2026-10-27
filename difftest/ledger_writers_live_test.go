//go:build faultline_test && unix

package difftest

import (
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/ledger"
	"github.com/Mizore66/faultline/internal/schema"
)

// ledgerWriterGatedFiles are the files TestLiveLedgerWriters gates. Task 9
// moves them into liveGatedFiles; until then the test adds them for its
// own run.
var ledgerWriterGatedFiles = []string{"internal/ledger/ledger.go", "internal/ledger/checkpoint.go"}

// ledgerOutcome renders a Go result like oracle-ledger.ts's outcome():
// {value} or {threw, zod, integrity}.
func ledgerOutcome(v jsjson.Value, err error) *jsjson.Obj {
	o := jsjson.NewObj()
	if err != nil {
		var zod *schema.Error
		var integrity *ledger.IntegrityError
		o.Set("threw", jsjson.MakeString(jsexc.Message(err)))
		o.Set("zod", jsjson.MakeBool(errors.As(err, &zod)))
		o.Set("integrity", jsjson.MakeBool(errors.As(err, &integrity)))
		return o
	}
	o.Set("value", v)
	return o
}

func ledgerOutcomeText(v jsjson.Value, err error) string {
	return jsjson.Stringify(jsjson.MakeObject(ledgerOutcome(v, err)))
}

// appendOptions turns the options object both sides get into AppendOptions.
func appendOptions(options jsjson.Value) ledger.AppendOptions {
	var o ledger.AppendOptions
	if v, ok := options.Obj().Get("eventId"); ok {
		o.EventID, o.HasEventID = v.Str(), true
	}
	if v, ok := options.Obj().Get("occurredAt"); ok {
		o.OccurredAt, o.HasOccurredAt = v.Str(), true
	}
	return o
}

// Identifiers around IdentifierSchema: lengths 1, 160 and 161, a leading
// punctuation character, a space, an empty string, a lone surrogate and
// non-ASCII.
var ledgerIdentifiers = []string{
	"t", "turn-1", "turn-2", "a:b.c_d-e", strings.Repeat("x", 160), strings.Repeat("y", 161), "-x", ".x", "a b", "",
	"\xed\xa0\x80", "t\xed\xb0\x80", "é", "turn/1", "T9",
}

// ledgerCalendarEdges are canonical-timestamp edge cases (as
// difftest/gen/ledger.go's calendarEdges): leap days, rollover fields and
// the expanded-year forms.
var ledgerCalendarEdges = []string{
	"2000-02-29T00:00:00.000Z", "1900-02-29T00:00:00.000Z", "2023-02-29T00:00:00.000Z", "2026-12-31T23:59:60.000Z",
	"2026-01-01T24:00:00.000Z", "2026-04-31T00:00:00.000Z", "2026-13-01T00:00:00.000Z", "+002026-01-01T00:00:00.000Z",
	"-000001-12-31T23:59:59.999Z", "+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.001Z", "9999-12-31T23:59:59.999Z",
	"0000-01-01T00:00:00.000Z", "2026-07-16T12:00:00.000Z",
}

func pickIdentifier(r *rand.Rand, valid string) string {
	if r.IntN(10) != 0 {
		return valid
	}
	return ledgerIdentifiers[r.IntN(len(ledgerIdentifiers))]
}

func ledgerDigestFor(r *rand.Rand) string {
	switch r.IntN(30) {
	case 0:
		return "sha256:" + strings.Repeat("A", 64)
	case 1:
		return "sha256:abc"
	case 2:
		return strings.Repeat("a", 64)
	}
	return "sha256:" + strings.Repeat(string("0123456789abcdef"[r.IntN(16)]), 64)
}

// ordinalValue is a turn ordinal near want: the expected value, its
// neighbours, the schema bounds (0, -1, fractions, -0, 1e21, 2^53), and
// values of the wrong type.
func ordinalValue(r *rand.Rand, want float64) jsjson.Value {
	if r.IntN(8) != 0 {
		return jsjson.MakeNumber(want)
	}
	switch r.IntN(12) {
	case 0:
		return jsjson.MakeNumber(want + 1)
	case 1:
		return jsjson.MakeNumber(0)
	case 2:
		return jsjson.MakeNumber(-1)
	case 3:
		return jsjson.MakeNumber(1.5)
	case 4:
		return jsjson.MakeNumber(math.Copysign(0, -1))
	case 5:
		return jsjson.MakeNumber(1e21)
	case 6:
		return jsjson.MakeNumber(9007199254740992)
	case 7:
		return jsjson.MakeString("1")
	case 8:
		return jsjson.MakeString("NaN")
	case 9:
		return jsjson.MakeNull()
	case 10:
		return jsjson.MakeNumber(want - 1)
	}
	return jsjson.MakeNumber(2)
}

func sizedText(r *rand.Rand, limit int) string {
	switch r.IntN(16) {
	case 0:
		return strings.Repeat("m", limit)
	case 1:
		return strings.Repeat("m", limit+1)
	case 2:
		return ""
	case 3:
		return "\xed\xa0\x80 lone"
	case 4:
		return strings.Repeat("é", limit)
	}
	return "value " + strconv.Itoa(r.IntN(100))
}

// ledgerState is what the lifecycle machine would see after a ledger's
// events (for choosing plausible next events); garbage reads as empty.
type ledgerState struct {
	started, ended, baseline bool
	activeID                 string
	activeOrdinal            float64
	active                   bool
	completed                float64
	turnIDs                  []string
	eventIDs                 []string
	lastTime                 string
}

func stateOf(l jsjson.Value) ledgerState {
	st := ledgerState{lastTime: l.Get("createdAt").Str()}
	for _, e := range l.Get("events").Items() {
		st.eventIDs = append(st.eventIDs, e.Get("eventId").Str())
		if t := e.Get("occurredAt"); t.Kind() == jsjson.String {
			st.lastTime = t.Str()
		}
		p := e.Get("event", "payload")
		switch e.Get("event", "type").Str() {
		case "SESSION_STARTED":
			st.started = true
		case "SESSION_BASELINE_SNAPSHOT":
			st.baseline = true
		case "TURN_STARTED":
			st.active, st.activeID, st.activeOrdinal = true, p.Get("turnId").Str(), p.Get("turnOrdinal").Num()
			st.turnIDs = append(st.turnIDs, st.activeID)
		case "TURN_COMPLETED":
			st.active, st.completed = false, p.Get("turnOrdinal").Num()
		case "SESSION_ENDED":
			st.ended = true
		}
	}
	return st
}

// shiftTime is ts moved by ms (ts when it is not canonical).
func shiftTime(ts string, ms int64) string {
	base := gitproof.TimestampMs(ts)
	if math.IsNaN(base) {
		return ts
	}
	t := time.UnixMilli(int64(base) + ms)
	if t.Year() < 0 || t.Year() > 9999 {
		return ts
	}
	return clock.ISO(t)
}

var ledgerEventTypes = []string{
	"SESSION_STARTED", "SESSION_BASELINE_SNAPSHOT", "TURN_STARTED", "TURN_COMPLETED", "TOOL_USE_STARTED",
	"TOOL_USE_COMPLETED", "WORKTREE_CHECKPOINT", "TURN_TREE_SNAPSHOT", "SESSION_ENDED",
}

// nextType is the event a well-behaved recorder would append next.
func nextType(r *rand.Rand, st ledgerState) string {
	switch {
	case !st.started:
		return "SESSION_STARTED"
	case st.active:
		return pick(r, "TOOL_USE_STARTED", "TOOL_USE_COMPLETED", "TURN_COMPLETED", "TURN_COMPLETED")
	case !st.baseline && len(st.turnIDs) == 0 && r.IntN(3) == 0:
		return "SESSION_BASELINE_SNAPSHOT"
	}
	return pick(r, "TURN_STARTED", "TURN_STARTED", "WORKTREE_CHECKPOINT", "TURN_TREE_SNAPSHOT", "SESSION_ENDED")
}

func signedSnapshot(r *rand.Rand, capturedAt string) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString("faultline.turn-tree-snapshot.v1"))
	o.Set("repositoryRoot", jsjson.MakeString(pick(r, "/work/repository", "/w", "/w", "/w", "")))
	o.Set("headCommit", jsjson.MakeString(strings.Repeat(pick(r, "a", "f", "a", "f", "A"), 40)))
	o.Set("treeDigest", jsjson.MakeString(strings.Repeat(pick(r, "b", "c"), pick2(r, 40, 64))))
	o.Set("capturedAt", jsjson.MakeString(capturedAt))
	o.Set("dirty", jsjson.MakeBool(r.IntN(2) == 0))
	o.Set("statusDigest", jsjson.MakeString(ledgerDigestFor(r)))
	d, _ := canonical.DigestJSON(jsjson.MakeObject(o))
	if r.IntN(10) == 0 {
		d = "sha256:" + strings.Repeat("1", 64)
	}
	o.Set("digest", jsjson.MakeString(d))
	return jsjson.MakeObject(o)
}

func signedCheckpoint(r *rand.Rand, capturedAt string) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(pick(r, "faultline.git-checkpoint.v1", "faultline.git-checkpoint.v1", "faultline.git-checkpoint.v1", "faultline.git-checkpoint.v1", "faultline.git-checkpoint.v2")))
	o.Set("repositoryRoot", jsjson.MakeString(pick(r, "/work/repository", "C:/work")))
	o.Set("headCommit", jsjson.MakeString(strings.Repeat(pick(r, "a", "f"), pick2(r, 40, 64))))
	o.Set("treeDigest", jsjson.MakeString(strings.Repeat("c", 40)))
	o.Set("capturedAt", jsjson.MakeString(capturedAt))
	o.Set("clean", jsjson.MakeBool(r.IntN(12) != 0))
	d, _ := canonical.DigestJSON(jsjson.MakeObject(o))
	if r.IntN(10) == 0 {
		d = "sha256:" + strings.Repeat("2", 64)
	}
	o.Set("digest", jsjson.MakeString(d))
	return jsjson.MakeObject(o)
}

func pick2(r *rand.Rand, a, b int) int {
	if r.IntN(3) == 0 {
		return b
	}
	return a
}

// randomEventInput is a lifecycle event input for st: usually the next
// plausible event with plausible values, otherwise any event type, with
// values at their schema bounds, keys reordered, dropped or added, or a
// non-object.
func randomEventInput(r *rand.Rand, st ledgerState, occurredAt string) jsjson.Value {
	switch r.IntN(100) {
	case 0:
		return jsjson.MakeNull()
	case 1:
		return jsjson.MakeArray(nil)
	case 2:
		return jsjson.MakeString("SESSION_STARTED")
	case 3:
		return jsjson.MustObject("type", jsjson.MakeString("SESSION_PAUSED"), "payload", jsjson.MustObject())
	}
	typ := nextType(r, st)
	if r.IntN(8) == 0 {
		typ = ledgerEventTypes[r.IntN(len(ledgerEventTypes))]
	}
	turnID, ordinal := st.activeID, st.activeOrdinal
	if !st.active {
		turnID, ordinal = "turn-"+strconv.Itoa(int(st.completed)+1), st.completed+1
	}
	captured := st.lastTime
	if r.IntN(6) == 0 {
		captured = shiftTime(occurredAt, 3_600_000)
	}
	p := jsjson.NewObj()
	switch typ {
	case "SESSION_STARTED":
		p.Set("transport", jsjson.MakeString(pick(r, "SIDE_CAR", "CODEX_CLI", "CODEX_APP", "OBSERVED_EXTERNAL_TRANSPORT", "SIDE_CAR", "CODEX_CLI", "CODEX_APP", "codex_cli")))
		p.Set("workingDirectory", jsjson.MakeString(pick(r, "/w", "/work/repository", "/w", "/w", "/w", "", "\xed\xa0\x80")))
		if r.IntN(3) == 0 {
			p.Set("codexThreadId", jsjson.MakeString(pickIdentifier(r, "thread-1")))
		}
		if r.IntN(3) == 0 {
			p.Set("model", jsjson.MakeString(sizedText(r, 200)))
		}
		if r.IntN(3) == 0 {
			p.Set("actor", jsjson.MakeString(sizedText(r, 200)))
		}
	case "SESSION_BASELINE_SNAPSHOT":
		p.Set("snapshot", signedSnapshot(r, captured))
	case "TURN_STARTED":
		id := turnID
		if r.IntN(8) == 0 && len(st.turnIDs) > 0 {
			id = st.turnIDs[r.IntN(len(st.turnIDs))]
		}
		p.Set("turnId", jsjson.MakeString(pickIdentifier(r, id)))
		p.Set("turnOrdinal", ordinalValue(r, ordinal))
		p.Set("promptDigest", jsjson.MakeString(ledgerDigestFor(r)))
	case "TURN_COMPLETED":
		p.Set("turnId", jsjson.MakeString(pickIdentifier(r, turnID)))
		p.Set("turnOrdinal", ordinalValue(r, ordinal))
		p.Set("outcome", jsjson.MakeString(pick(r, "COMPLETED", "FAILED", "INTERRUPTED", "COMPLETED", "FAILED", "INTERRUPTED", "ABANDONED")))
		if r.IntN(2) == 0 {
			p.Set("outputDigest", jsjson.MakeString(ledgerDigestFor(r)))
		}
		if r.IntN(3) == 0 {
			p.Set("contribution", jsjson.MakeString(sizedText(r, 240)))
		}
	case "TOOL_USE_STARTED", "TOOL_USE_COMPLETED":
		p.Set("turnId", jsjson.MakeString(pickIdentifier(r, turnID)))
		p.Set("turnOrdinal", ordinalValue(r, ordinal))
		p.Set("toolName", jsjson.MakeString(pick(r, "shell", "shell", "shell", "apply_patch", "apply_patch", "sh ell!", strings.Repeat("t", 160), strings.Repeat("t", 161), "")))
		if r.IntN(2) == 0 {
			p.Set("toolCallId", jsjson.MakeString(pickIdentifier(r, "call-1")))
		}
		p.Set("allowlistedFieldsDigest", jsjson.MakeString(ledgerDigestFor(r)))
	case "WORKTREE_CHECKPOINT":
		p.Set("checkpoint", signedCheckpoint(r, captured))
		p.Set("afterTurnOrdinal", ordinalValue(r, st.completed))
	case "TURN_TREE_SNAPSHOT":
		id := "turn-" + strconv.Itoa(int(st.completed))
		if len(st.turnIDs) > 0 {
			id = st.turnIDs[len(st.turnIDs)-1]
		}
		p.Set("turnId", jsjson.MakeString(pickIdentifier(r, id)))
		p.Set("turnOrdinal", ordinalValue(r, st.completed))
		p.Set("snapshot", signedSnapshot(r, captured))
	case "SESSION_ENDED":
		p.Set("reason", jsjson.MakeString(pick(r, "COMPLETED", "FAILED", "INTERRUPTED", "ABANDONED", "COMPLETED", "ABANDONED", "DONE")))
		p.Set("completedTurns", ordinalValue(r, st.completed))
	}
	// Key-level changes: reversed payload order (the parse restores the
	// schema's), a dropped or an unknown key.
	switch r.IntN(16) {
	case 0:
		keys := p.Keys()
		rev := jsjson.NewObj()
		for i := len(keys) - 1; i >= 0; i-- {
			rev.Set(keys[i], p.Field(keys[i]))
		}
		p = rev
	case 1:
		if keys := p.Keys(); len(keys) > 0 {
			p.Delete(keys[r.IntN(len(keys))])
		}
	case 2:
		p.Set(pick(r, "extra", "0", "hash"), jsjson.MakeNumber(1))
	}
	e := jsjson.NewObj()
	if r.IntN(8) == 0 {
		e.Set("payload", jsjson.MakeObject(p))
		e.Set("type", jsjson.MakeString(typ))
	} else {
		e.Set("type", jsjson.MakeString(typ))
		e.Set("payload", jsjson.MakeObject(p))
	}
	switch r.IntN(40) {
	case 0:
		e.Delete("payload")
	case 1:
		e.Set("eventId", jsjson.MakeString("e"))
	}
	return jsjson.MakeObject(e)
}

// randomAppendOptions: absent ids (the seeded clock and UUIDs), fresh and
// reused event ids, ids that fail IdentifierSchema, and occurredAt later
// than, equal to or earlier than the last event, non-canonical or on a
// calendar edge.
func randomAppendOptions(r *rand.Rand, st ledgerState, i int) (jsjson.Value, string) {
	o := jsjson.NewObj()
	switch r.IntN(16) {
	case 0, 1:
	case 2:
		if len(st.eventIDs) > 0 {
			o.Set("eventId", jsjson.MakeString(st.eventIDs[r.IntN(len(st.eventIDs))]))
			break
		}
		fallthrough
	case 3:
		o.Set("eventId", jsjson.MakeString(ledgerIdentifiers[r.IntN(len(ledgerIdentifiers))]))
	default:
		o.Set("eventId", jsjson.MakeString("ev-"+strconv.Itoa(i)))
	}
	occurredAt := ""
	switch r.IntN(16) {
	case 0, 1:
	case 2:
		occurredAt = st.lastTime
	case 3:
		occurredAt = shiftTime(st.lastTime, -1)
	case 4:
		occurredAt = pick(r, "2026-07-16", "2026-07-16T10:00:00Z", "2026-07-16T10:00:00.000+00:00", "", "not a time", "\xed\xa0\x80")
	case 5:
		occurredAt = pick(r, ledgerCalendarEdges...)
	default:
		occurredAt = shiftTime(st.lastTime, int64(1+r.IntN(5000)))
	}
	if occurredAt != "" || r.IntN(20) == 0 {
		o.Set("occurredAt", jsjson.MakeString(occurredAt))
	}
	if occurredAt == "" {
		occurredAt = clock.ISO(time.UnixMilli(seededEpochMillis))
	}
	return jsjson.MakeObject(o), occurredAt
}

// ledgerSources builds the pool of starting ledgers: empty ledgers, every
// prefix of the oracle's rich ledger (active turns, a baseline, an ended
// session), the committed bases, and re-signed structural mutations.
func ledgerSources(t *testing.T, c *oracle.Client, r *rand.Rand) []string {
	rich := oracleText(t, c, "richLedger", "{}")
	richValue, err := jsjson.Parse(rich)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	events := richValue.Get("events").Items()
	for k := 0; k <= len(events); k++ {
		o := jsjson.NewObj()
		for _, key := range richValue.Obj().Keys() {
			o.Set(key, richValue.Obj().Field(key))
		}
		o.Set("events", jsjson.MakeArray(events[:k]))
		out = append(out, jsjson.Stringify(jsjson.MakeObject(o)))
	}
	for _, text := range seedTexts(t, "git-partially-bound/lifecycle/ledger.json", "git-fully-bound/lifecycle/ledger.json") {
		out = append(out, jsjson.Stringify(mustParse(t, text))) // one line, for the request
	}
	base := len(out)
	for range 40 {
		text := out[r.IntN(base)]
		switch r.IntN(3) {
		case 0:
			text = oracleText(t, c, "resignLedger", `{"text":`+jsjson.Quote(gen.MutateLedgerEvents(r, text))+"}")
		case 1:
			text = gen.CorruptDeep(r, text)
		default:
			// A schema-valid edit behind the old hashes: the integrity check
			// fails (Refusing to append to an invalid ledger).
			text = strings.Replace(text, `"workingDirectory":"`, `"workingDirectory":"/tampered`, 1)
		}
		out = append(out, text)
	}
	return out
}

func emptyLedgerText(r *rand.Rand) string {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(pick(r, "faultline.codex-lifecycle-ledger.v1", "faultline.codex-lifecycle-ledger.v1", "faultline.codex-lifecycle-ledger.v0")))
	o.Set("ledgerId", jsjson.MakeString(pickIdentifier(r, "ledger-1")))
	o.Set("sessionId", jsjson.MakeString(pickIdentifier(r, "session-1")))
	o.Set("createdAt", jsjson.MakeString(pick(r, "2026-07-16T09:00:00.000Z", "2026-07-16T09:00:00.000Z", "2026-07-16T13:00:00.000Z", "2026-07-16")))
	o.Set("events", jsjson.MakeArray([]jsjson.Value{}))
	if r.IntN(15) == 0 {
		o.Set("extra", jsjson.MakeBool(true))
	}
	return jsjson.Stringify(jsjson.MakeObject(o))
}

// dirListing lists every entry under dir with its kind, permission bits
// and bytes (or the read error), sorted.
func dirListing(dir string) string {
	var lines []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(dir, path)
		if err != nil {
			lines = append(lines, strconv.Quote(rel)+" ERR "+err.Error())
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			lines = append(lines, strconv.Quote(rel)+" ERR "+err.Error())
			return nil
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, strconv.Quote(rel)+" L "+target)
		case info.IsDir():
			lines = append(lines, strconv.Quote(rel)+" D "+info.Mode().Perm().String())
		default:
			b, err := os.ReadFile(path)
			content := strconv.Quote(string(b))
			if err != nil {
				content = "unreadable"
			}
			lines = append(lines, strconv.Quote(rel)+" F "+info.Mode().Perm().String()+" "+content)
		}
		return nil
	})
	return strings.Join(lines, "\n")
}

// fileCase is one atomic-file case: a setup applied to a fresh directory,
// then ledgerAppendAtomic or ledgerWriteAtomic on file.
type fileCase struct {
	label  string
	setup  func(dir string)
	file   string // relative to the case directory
	op     string // "append" or "write"
	ledger string // the write op's ledger
	input  jsjson.Value
	opts   jsjson.Value
}

func randomFileCase(r *rand.Rand, sources []string, i int) fileCase {
	valid := sources[r.IntN(9)] // a rich-ledger prefix: always valid
	st := func() ledgerState { v, _ := jsjson.Parse(valid); return stateOf(v) }()
	opts, occurredAt := randomAppendOptions(r, st, 100000+i)
	fc := fileCase{file: "l.json", op: pick(r, "append", "append", "write"), ledger: valid, input: randomEventInput(r, st, occurredAt), opts: opts}
	write := func(rel, content string, mode os.FileMode) func(string) {
		return func(dir string) {
			p := filepath.Join(dir, rel)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(content), mode)
			os.Chmod(p, mode)
		}
	}
	both := func(fs ...func(string)) func(string) {
		return func(dir string) {
			for _, f := range fs {
				f(dir)
			}
		}
	}
	ledgerFile := write("l.json", valid, 0o644)
	if r.IntN(3) == 0 {
		// The file as `fl` writes it.
		v, _ := jsjson.Parse(valid)
		ledgerFile = write("l.json", jsjson.StringifyIndent(v, "  ")+"\n", 0o600)
	}
	kinds := []string{"valid", "valid", "valid", "missing-dir", "missing-file", "held-lock", "invalid-json", "invalid-schema",
		"invalid-ledger", "dir-at-path", "parent-is-file", "lock-is-dir", "stale-stage", "symlink"}
	if os.Geteuid() != 0 {
		kinds = append(kinds, "unreadable", "read-only-dir")
	}
	kind := kinds[r.IntN(len(kinds))]
	fc.label = "file case " + strconv.Itoa(i) + " " + fc.op + " " + kind
	switch kind {
	case "valid":
		fc.setup = ledgerFile
	case "missing-dir":
		fc.file = "missing/sub/l.json"
		fc.setup = func(string) {}
	case "missing-file":
		fc.setup = func(string) {}
	case "held-lock":
		fc.setup = both(ledgerFile, write("l.json.lock", "", 0o600))
	case "invalid-json":
		fc.setup = write("l.json", pick(r, "{", "", "null", "[1,2]", `{"schemaVersion":`), 0o644)
	case "invalid-schema":
		fc.setup = write("l.json", gen.CorruptDeep(r, valid), 0o644)
		fc.ledger = gen.CorruptDeep(r, valid)
	case "invalid-ledger":
		tampered := strings.Replace(valid, `"workingDirectory":"`, `"workingDirectory":"/x`, 1)
		if tampered == valid {
			tampered = strings.Replace(valid, `"createdAt":"2026-07-16T10:00:00.000Z"`, `"createdAt":"2026-07-16T10:00:00.500Z"`, 1)
		}
		fc.setup = write("l.json", tampered, 0o644)
		fc.ledger = tampered
	case "dir-at-path":
		fc.setup = func(dir string) { os.MkdirAll(filepath.Join(dir, "l.json", "inner"), 0o755) }
	case "parent-is-file":
		// One level: mkdir's EEXIST; two levels: ENOTDIR.
		fc.file = pick(r, "plain/l.json", "plain/sub/l.json")
		fc.setup = write("plain", "x", 0o644)
	case "lock-is-dir":
		fc.setup = both(ledgerFile, func(dir string) { os.Mkdir(filepath.Join(dir, "l.json.lock"), 0o755) })
	case "stale-stage":
		fc.setup = both(ledgerFile, write(".l.json.1.x.tmp", "old", 0o600))
	case "symlink":
		fc.setup = both(write("target.json", valid, 0o644), func(dir string) { os.Symlink("target.json", filepath.Join(dir, "l.json")) })
	case "unreadable":
		fc.setup = both(ledgerFile, func(dir string) { os.Chmod(filepath.Join(dir, "l.json"), 0o000) })
	case "read-only-dir":
		fc.setup = both(ledgerFile, func(dir string) { os.Chmod(dir, 0o555) })
	}
	return fc
}

// captureRepoCase is a real repository (under work/repo) and the path
// captured from.
type captureRepoCase struct {
	label, tmpl, from string // from is relative to work
}

func ledgerGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	captureGit(t, dir, args...)
}

func randomCaptureRepo(t *testing.T, r *rand.Rand, i int) captureRepoCase {
	kinds := []string{"clean", "tracked-change", "crlf-only", "untracked", "ignored-only", "no-head", "subdirectory",
		"symlinked-path", "fsmonitor", "staged", "deleted", "not-a-repository", "sha256", "lf-to-crlf-content"}
	kind := kinds[i%len(kinds)]
	if i >= len(kinds) {
		kind = kinds[r.IntN(len(kinds))]
	}
	tmpl := filepath.Join(t.TempDir(), "w")
	repo := filepath.Join(tmpl, "repo")
	os.MkdirAll(filepath.Join(repo, "sub"), 0o755)
	c := captureRepoCase{label: "repo case " + strconv.Itoa(i) + " " + kind, tmpl: tmpl, from: "repo"}
	if kind == "not-a-repository" {
		os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644)
		return c
	}
	format := "sha1"
	if kind == "sha256" {
		format = "sha256"
	}
	ledgerGit(t, repo, "init", "-q", "--object-format="+format)
	if kind == "no-head" {
		os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644)
		return c
	}
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("line one\nline two\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "sub", "b.txt"), []byte("b\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.log\n"), 0o644)
	ledgerGit(t, repo, "add", "-A")
	ledgerGit(t, repo, "commit", "-q", "-m", "base")
	switch kind {
	case "tracked-change":
		os.WriteFile(filepath.Join(repo, "a.txt"), []byte("line one\nchanged\n"), 0o644)
	case "crlf-only":
		os.WriteFile(filepath.Join(repo, "a.txt"), []byte("line one\r\nline two\r\n"), 0o644)
	case "lf-to-crlf-content":
		os.WriteFile(filepath.Join(repo, "a.txt"), []byte("line one\r\nline 2\r\n"), 0o644)
	case "untracked":
		os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644)
	case "ignored-only":
		os.WriteFile(filepath.Join(repo, "debug.log"), []byte("log\n"), 0o644)
	case "subdirectory":
		c.from = "repo/sub"
	case "symlinked-path":
		os.Symlink("repo", filepath.Join(tmpl, "link"))
		c.from = "link"
	case "fsmonitor":
		ledgerGit(t, repo, "config", "core.fsmonitor", "printf fsmonitor-fired > FSMONITOR_RAN #")
	case "staged":
		os.WriteFile(filepath.Join(repo, "sub", "b.txt"), []byte("b2\n"), 0o644)
		ledgerGit(t, repo, "add", "sub/b.txt")
	case "deleted":
		os.Remove(filepath.Join(repo, "sub", "b.txt"))
	}
	return c
}

// scriptStep is one scripted runner reply: an output or a thrown message.
type scriptStep struct {
	out   string
	throw string
}

func randomScript(r *rand.Rand, root string) []scriptStep {
	head := strings.Repeat("a", 40)
	outputs := []func() string{
		func() string { return pick(r, root, root, "  "+root+"\n", "", "relative/top", root+"/") },
		func() string { return pick(r, "", "", "", " ", "\n", ":100644 100644 abc 000 M\ta.txt") },
		func() string { return pick(r, "", "", "", "new.txt", "\t") },
		func() string {
			return pick(r, head, head, strings.Repeat("e", 64), head+"\n", strings.ToUpper(head), "", "HEAD")
		},
		func() string {
			return pick(r, strings.Repeat("b", 40), strings.Repeat("b", 40), strings.Repeat("b", 64), " "+strings.Repeat("b", 40), "")
		},
	}
	throwAt := -1
	if r.IntN(2) == 0 {
		throwAt = r.IntN(5)
	}
	n := 5
	if r.IntN(10) == 0 {
		n = r.IntN(5) // an exhausted script
	}
	var steps []scriptStep
	for k := range n {
		if k == throwAt {
			steps = append(steps, scriptStep{throw: pick(r,
				"Git checkpoint capture failed for rev-parse HEAD: spawnSync git ETIMEDOUT",
				"Git checkpoint capture failed for diff-index --ignore-cr-at-eol HEAD: fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.",
				"Git checkpoint capture failed for rev-parse --show-toplevel: spawnSync git ENOENT",
				"Git checkpoint capture failed for ls-files --others --exclude-standard: exit 128",
				"Git checkpoint capture failed for rev-parse HEAD^{tree}: spawnSync git ENOBUFS")})
			continue
		}
		steps = append(steps, scriptStep{out: outputs[k]()})
	}
	return steps
}

func scriptJSON(steps []scriptStep) jsjson.Value {
	items := make([]jsjson.Value, len(steps))
	for i, s := range steps {
		if s.throw != "" {
			items[i] = jsjson.MustObject("throw", jsjson.MakeString(s.throw))
		} else {
			items[i] = jsjson.MustObject("out", jsjson.MakeString(s.out))
		}
	}
	return jsjson.MakeArray(items)
}

// goScriptedCapture is the oracle's scripted capture on the Go side.
func goScriptedCapture(repository string, steps []scriptStep, scripted bool) string {
	calls := []jsjson.Value{}
	var o ledger.CaptureOptions
	if scripted {
		queue := append([]scriptStep(nil), steps...)
		o.RunGit = func(root string, args []string) (string, error) {
			calls = append(calls, jsjson.MakeArray([]jsjson.Value{jsjson.MakeString(root), stringValues(args)}))
			if len(queue) == 0 {
				return "", errors.New("script exhausted")
			}
			s := queue[0]
			queue = queue[1:]
			if s.throw != "" {
				return "", errors.New(s.throw)
			}
			return s.out, nil
		}
	}
	out := ledgerOutcome(ledger.CaptureGitCleanCheckpoint(repository, o))
	out.Set("calls", jsjson.MakeArray(calls))
	return jsjson.Stringify(jsjson.MakeObject(out))
}

// TestLiveLedgerWriters runs the src/ledger.ts writers and internal/ledger
// under one seed, TS first and then Go on each case (so both read the same
// clock ticks and UUIDs), and compares results: 200 creations and 2,000
// appends on generated (ledger, input, options) triples; 200 atomic append
// or write cases on generated directories, compared with the directory
// afterwards; 60 clean-checkpoint captures on real repositories, compared
// with the work directory afterwards; and 300 captures through a scripted
// runner.
func TestLiveLedgerWriters(t *testing.T) {
	oracle.Skip(t)
	for _, f := range ledgerWriterGatedFiles {
		if liveGatedFiles[f] == "" {
			liveGatedFiles[f] = "TestLiveLedgerWriters"
			defer delete(liveGatedFiles, f)
		}
	}
	gitDir, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(string(gitDir)) + ":/usr/bin:/bin"
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(73, 4))

	// Real repositories are built before the oracle starts.
	var repos []captureRepoCase
	for i := range 60 {
		repos = append(repos, randomCaptureRepo(t, r, i))
	}

	const seed = "live-ledger-writers"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	oracleCall(t, c, "echo", "null")
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	t.Setenv("PATH", path)

	outcomes := map[string]int{}
	tally := func(kind, result string) {
		v, _ := jsjson.Parse(result)
		switch {
		case v.Get("threw").Kind() != jsjson.String:
			outcomes[kind+" ok"]++
		case v.Get("zod").Bool():
			outcomes[kind+" zod"]++
		case v.Get("integrity").Bool():
			outcomes[kind+" integrity"]++
		default:
			outcomes[kind+" error"]++
		}
		recordOutput(t, result)
	}
	compare := func(label, args, got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s(%s):\ngot  %s\nwant %s", label, args, got, want)
		}
	}

	// Creation.
	for i := range 200 {
		o := jsjson.NewObj()
		o.Set("sessionId", jsjson.MakeString(pickIdentifier(r, "session-"+strconv.Itoa(i))))
		var co ledger.CreateOptions
		co.SessionID = o.Field("sessionId").Str()
		if r.IntN(2) == 0 {
			if id := pickIdentifier(r, "ledger-"+strconv.Itoa(i)); id != "" {
				o.Set("ledgerId", jsjson.MakeString(id))
				co.LedgerID = id
			}
		}
		if r.IntN(2) == 0 {
			if at := pick(r, "2026-07-16T09:00:00.000Z", pick(r, ledgerCalendarEdges...), "2026-07-16"); at != "" {
				o.Set("createdAt", jsjson.MakeString(at))
				co.CreatedAt = at
			}
		}
		args := jsjson.Stringify(jsjson.MustObject("options", jsjson.MakeObject(o)))
		want := ledgerOracle(t, c, "ledgerCreate", args)
		compare("ledgerCreate", args, ledgerOutcomeText(ledger.Create(co)), want)
		tally("create", want)
	}

	// Appends.
	sources := ledgerSources(t, c, r)
	var chains []string
	for i := range 2000 {
		var text string
		switch n := r.IntN(20); {
		case n < 6 && len(chains) > 0:
			text = chains[r.IntN(len(chains))]
		case n < 9:
			text = emptyLedgerText(r)
		default:
			text = sources[r.IntN(len(sources))]
		}
		l, _ := jsjson.Parse(text)
		st := stateOf(l)
		opts, occurredAt := randomAppendOptions(r, st, i)
		input := randomEventInput(r, st, occurredAt)
		args := `{"ledger":` + text + `,"input":` + jsjson.Stringify(input) + `,"options":` + jsjson.Stringify(opts) + "}"
		want := ledgerOracle(t, c, "ledgerAppend", args)
		before := jsjson.Stringify(l)
		compare("ledgerAppend", args, ledgerOutcomeText(ledger.Append(l, input, appendOptions(opts))), want)
		if jsjson.Stringify(l) != before {
			t.Fatalf("Append mutated its input ledger")
		}
		tally("append", want)
		if v, _ := jsjson.Parse(want); v.Get("value").Kind() == jsjson.Object {
			chains = append(chains, jsjson.Stringify(v.Get("value")))
			if len(chains) > 60 {
				chains = chains[1:]
			}
		}
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Atomic file cases: the same directory for both sides, one after the
	// other.
	for i := range 200 {
		fc := randomFileCase(r, sources, i)
		dir := filepath.Join(root, "f"+strconv.Itoa(i))
		file := filepath.Join(dir, fc.file)
		var args string
		if fc.op == "append" {
			args = `{"file":` + jsjson.Quote(file) + `,"input":` + jsjson.Stringify(fc.input) + `,"options":` + jsjson.Stringify(fc.opts) + `,"cwd":` + jsjson.Quote(cwd) + "}"
		} else {
			args = `{"file":` + jsjson.Quote(file) + `,"ledger":` + fc.ledger + `,"cwd":` + jsjson.Quote(cwd) + "}"
		}
		run := func(side string) (string, string) {
			os.MkdirAll(dir, 0o755)
			fc.setup(dir)
			var result string
			switch {
			case side == "ts" && fc.op == "append":
				result = ledgerOracle(t, c, "ledgerAppendAtomic", args)
			case side == "ts":
				result = ledgerOracle(t, c, "ledgerWriteAtomic", args)
			case fc.op == "append":
				result = ledgerOutcomeText(ledger.AppendAtomic(file, fc.input, appendOptions(fc.opts)))
			default:
				written, err := ledger.WriteAtomic(file, mustParse(t, fc.ledger))
				result = ledgerOutcomeText(jsjson.MakeString(written), err)
			}
			listing := dirListing(dir)
			unlockTree(dir)
			os.RemoveAll(dir)
			return result, listing
		}
		want, wantDir := run("ts")
		got, gotDir := run("go")
		want, wantDir, got, gotDir = maskStagePID(want), maskStagePID(wantDir), maskStagePID(got), maskStagePID(gotDir)
		compare(fc.label, args, got, want)
		if gotDir != wantDir {
			t.Fatalf("%s: directories differ:\ngo\n%s\nts\n%s", fc.label, gotDir, wantDir)
		}
		tally(fc.op+"-atomic", want)
	}

	// Real repositories: TS then Go on a copy at the same path.
	for i, rc := range repos {
		work := filepath.Join(root, "r"+strconv.Itoa(i))
		from := filepath.Join(work, rc.from)
		args := jsjson.Stringify(jsjson.MustObject("repository", jsjson.MakeString(from), "path", jsjson.MakeString(path), "cwd", jsjson.MakeString(cwd)))
		run := func(side string) (string, string) {
			copyWork(t, rc.tmpl, work)
			var result string
			if side == "ts" {
				result = ledgerOracle(t, c, "captureGitCleanCheckpoint", args)
			} else {
				result = goScriptedCapture(from, nil, false)
			}
			manifest := workManifest(t, work)
			os.RemoveAll(work)
			return result, manifest
		}
		want, wantDir := run("ts")
		got, gotDir := run("go")
		compare(rc.label, args, got, want)
		if gotDir != wantDir {
			t.Fatalf("%s: work directories differ:\ngo\n%s\nts\n%s", rc.label, gotDir, wantDir)
		}
		if strings.Contains(wantDir, "FSMONITOR_RAN") {
			t.Fatalf("%s: the repository's fsmonitor ran", rc.label)
		}
		tally("capture-repo", want)
	}

	// Scripted runners.
	for i := range 300 {
		repository := pick(r, "/r/top", "/r/top/sub", "", "rel/dir", "  /r ")
		steps := randomScript(r, pick(r, "/r/top", root))
		args := jsjson.Stringify(jsjson.MustObject("repository", jsjson.MakeString(repository), "steps", scriptJSON(steps), "cwd", jsjson.MakeString(cwd)))
		want := ledgerOracle(t, c, "captureGitCleanCheckpoint", args)
		compare("scripted capture "+strconv.Itoa(i), args, goScriptedCapture(repository, steps, true), want)
		tally("capture-scripted", want)
	}

	if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
		t.Fatalf("oracle clock at %s, Go clock at %d", ts, goTick)
	}
	outputs := recordedOutputs(t)
	for _, marker := range []string{
		"Refusing to append to an invalid ledger", "Refusing an invalid lifecycle transition", "Could not acquire lifecycle ledger append lock",
		"Git checkpoint capture requires a clean worktree.", "Git checkpoint capture failed for", "Expected a canonical ISO-8601 UTC timestamp",
		"Unable to read lifecycle ledger", "has invalid schema", "Lifecycle ledger integrity check failed", "script exhausted",
		"occurs before the preceding ledger timestamp", "reuses event id", "occurs after SESSION_ENDED", `"eventId":"event-`,
		"Identifier contains unsupported characters", "EISDIR", "ENOTDIR", `"value":{"schemaVersion":"faultline.git-checkpoint.v1"`,
	} {
		if !strings.Contains(outputs, marker) {
			t.Errorf("no generated case reached %q", marker)
		}
	}
	for _, f := range ledgerWriterGatedFiles {
		requireReached(t, f, outputs)
	}
	liveSummary(t, "200 creations, 2000 appends, 200 atomic file cases, %d repositories, 300 scripted captures; outcomes %v", len(repos), outcomes)
}

// stagePID is the process id in an atomic write's stage name
// (".<base>.<pid>.<uuid>.tmp"), which differs between the two sides.
var stagePID = regexp.MustCompile(`(\.[^/'"]*?\.)[0-9]+(\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.tmp)`)

func maskStagePID(s string) string { return stagePID.ReplaceAllString(s, "${1}<PID>${2}") }

// ledgerOracle is oracleText with the request in the failure.
func ledgerOracle(t *testing.T, c *oracle.Client, op, args string) string {
	t.Helper()
	raw, err := c.Call(op, args)
	if err != nil {
		os.WriteFile(filepath.Join(os.TempDir(), "ledger-writers-failed-args.json"), []byte(args), 0o644)
		t.Fatalf("%s: %v (arguments in %s)", op, err, filepath.Join(os.TempDir(), "ledger-writers-failed-args.json"))
	}
	v, err := jsjson.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v.Str()
}

func mustParse(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
