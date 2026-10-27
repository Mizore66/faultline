package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

func mustJSON(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

const started = `{"type":"SESSION_STARTED","payload":{"transport":"SIDE_CAR","workingDirectory":"/w"}}`

func TestCreateAppendWriteVerify(t *testing.T) {
	l, err := Create(CreateOptions{SessionID: "s-1", LedgerID: "ledger-1", CreatedAt: "2026-01-01T00:00:00.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	l, err = Append(l, mustJSON(t, started), AppendOptions{EventID: "e-1", OccurredAt: "2026-01-01T00:00:01.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rec", "l.json")
	if _, err := WriteAtomic(path, l); err != nil {
		t.Fatal(err)
	}
	if v := VerifyFile(path); !v.Valid || v.EventCount != 1 {
		t.Fatalf("%+v", v)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "{\n  \"schemaVersion\": \"faultline.codex-lifecycle-ledger.v1\",\n  \"ledgerId\": \"ledger-1\",") || !strings.HasSuffix(string(b), "}\n") {
		t.Fatalf("%s", b)
	}
}

func TestAppendRefusesSecondStart(t *testing.T) {
	l, _ := Create(CreateOptions{SessionID: "s-1", LedgerID: "ledger-1", CreatedAt: "2026-01-01T00:00:00.000Z"})
	l, _ = Append(l, mustJSON(t, started), AppendOptions{EventID: "e-1", OccurredAt: "2026-01-01T00:00:01.000Z"})
	_, err := Append(l, mustJSON(t, started), AppendOptions{EventID: "e-2", OccurredAt: "2026-01-01T00:00:02.000Z"})
	var ie *IntegrityError
	if !errors.As(err, &ie) || ie.Message != "Refusing an invalid lifecycle transition: Event 2 (SESSION_STARTED) starts a session that has already started; Event 2 (SESSION_STARTED) must be the first event" {
		t.Fatalf("%v", err)
	}
}

func TestAppendAtomicHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.json")
	os.WriteFile(path+".lock", nil, 0o600)
	_, err := AppendAtomic(path, mustJSON(t, started), AppendOptions{})
	want := "Could not acquire lifecycle ledger append lock " + path + ".lock: EEXIST: file already exists, open '" + path + ".lock'"
	if err == nil || err.Error() != want {
		t.Fatalf("%v", err)
	}
}

// The tests/ledger.test.ts fixtures (:23-103).
const createdAt = "2026-07-16T00:00:00.000Z"

var times = []string{
	"2026-07-16T00:00:01.000Z", "2026-07-16T00:00:02.000Z", "2026-07-16T00:00:03.000Z",
	"2026-07-16T00:00:04.000Z", "2026-07-16T00:00:05.000Z",
}

func digest(c string) string { return "sha256:" + strings.Repeat(c, 64) }

func fixtureCheckpoint(t *testing.T) jsjson.Value {
	t.Helper()
	cp, err := SignGitCheckpoint(mustJSON(t, `{"schemaVersion":"faultline.git-checkpoint.v1","repositoryRoot":"C:/work/faultline",`+
		`"headCommit":"`+strings.Repeat("a", 40)+`","treeDigest":"`+strings.Repeat("b", 40)+`","capturedAt":"2026-07-16T00:00:03.500Z","clean":true}`))
	if err != nil {
		t.Fatal(err)
	}
	return cp
}

func appendAt(t *testing.T, l jsjson.Value, input string, index int) jsjson.Value {
	t.Helper()
	next, err := Append(l, mustJSON(t, input), AppendOptions{EventID: "event-" + strconv.Itoa(index+1), OccurredAt: times[index]})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func validLedger(t *testing.T, transport string) jsjson.Value {
	t.Helper()
	l, err := Create(CreateOptions{LedgerID: "ledger-demo", SessionID: "session-demo", CreatedAt: createdAt})
	if err != nil {
		t.Fatal(err)
	}
	l = appendAt(t, l, `{"type":"SESSION_STARTED","payload":{"transport":"`+transport+`","workingDirectory":"C:/work/faultline","codexThreadId":"thread-demo","model":"gpt-5.6"}}`, 0)
	l = appendAt(t, l, `{"type":"TURN_STARTED","payload":{"turnId":"turn-1","turnOrdinal":1,"promptDigest":"`+digest("c")+`"}}`, 1)
	l = appendAt(t, l, `{"type":"TURN_COMPLETED","payload":{"turnId":"turn-1","turnOrdinal":1,"outcome":"COMPLETED","outputDigest":"`+digest("d")+`"}}`, 2)
	l = appendAt(t, l, `{"type":"WORKTREE_CHECKPOINT","payload":{"checkpoint":`+jsjson.Stringify(fixtureCheckpoint(t))+`,"afterTurnOrdinal":1}}`, 3)
	return appendAt(t, l, `{"type":"SESSION_ENDED","payload":{"reason":"COMPLETED","completedTurns":1}}`, 4)
}

// resign recomputes the chain (tests/ledger.test.ts:86).
func resign(t *testing.T, l jsjson.Value) jsjson.Value {
	t.Helper()
	previous := gitproof.LedgerGenesisHash(l)
	for _, e := range l.Get("events").Items() {
		e.Obj().Set("previousHash", jsjson.MakeString(previous))
		e.Obj().Delete("hash")
		h := gitproof.HashLifecycleEvent(e)
		e.Obj().Set("hash", jsjson.MakeString(h))
		previous = h
	}
	return l
}

func clone(t *testing.T, v jsjson.Value) jsjson.Value { return mustJSON(t, jsjson.Stringify(v)) }

func contains(errs []string, want string) bool {
	for _, e := range errs {
		if e == want {
			return true
		}
	}
	return false
}

// tests/ledger.test.ts:112.
func TestSignedLifecycleWritesAndReadsBack(t *testing.T) {
	l := validLedger(t, "CODEX_CLI")
	if v := Verify(l); !v.Valid || v.EventCount != 5 {
		t.Fatalf("%+v", v)
	}
	if got := l.Get("events").Items()[0].Get("previousHash").Str(); got != gitproof.LedgerGenesisHash(l) {
		t.Fatalf("first previousHash %s", got)
	}
	file := filepath.Join(t.TempDir(), "session.json")
	written, err := WriteAtomic(file, l)
	if err != nil || written != file {
		t.Fatalf("%q %v", written, err)
	}
	back, err := ReadVerified(file)
	if err != nil || jsjson.Stringify(back) != jsjson.Stringify(l) {
		t.Fatalf("%v\n%s", err, jsjson.Stringify(back))
	}
	if v := VerifyFile(file); !v.Valid || v.EventCount != 5 {
		t.Fatalf("%+v", v)
	}
	if b, _ := os.ReadFile(file); string(b) != jsjson.StringifyIndent(l, "  ")+"\n" || !strings.Contains(string(b), "SESSION_STARTED") {
		t.Fatalf("%s", b)
	}
}

// tests/ledger.test.ts:132.
func TestObservedExternalTransport(t *testing.T) {
	l := validLedger(t, "OBSERVED_EXTERNAL_TRANSPORT")
	if v := Verify(l); !v.Valid || v.EventCount != 5 {
		t.Fatalf("%+v", v)
	}
	if got := l.Get("events").Items()[0].Get("event", "payload", "transport").Str(); got != "OBSERVED_EXTERNAL_TRANSPORT" {
		t.Fatal(got)
	}
}

// tests/ledger.test.ts:142.
func TestDetectsPayloadTampering(t *testing.T) {
	l := clone(t, validLedger(t, "CODEX_CLI"))
	l.Get("events").Items()[1].Get("event", "payload").Obj().Set("promptDigest", jsjson.MakeString(digest("e")))
	if v := Verify(l); v.Valid || !contains(v.Errors, "Event 2 hash does not match its contents") {
		t.Fatalf("%+v", v)
	}
}

// tests/ledger.test.ts:151.
func TestDetectsReorderedRecords(t *testing.T) {
	l := clone(t, validLedger(t, "CODEX_CLI"))
	events := l.Get("events").Items()
	events[0], events[1] = events[1], events[0]
	v := Verify(l)
	found := false
	for _, e := range v.Errors {
		found = found || strings.Contains(e, "sequence") || strings.Contains(e, "previous hash")
	}
	if v.Valid || !found {
		t.Fatalf("%+v", v)
	}
}

// tests/ledger.test.ts:163.
func TestRejectsDuplicateSequence(t *testing.T) {
	l := clone(t, validLedger(t, "CODEX_CLI"))
	l.Get("events").Items()[1].Obj().Set("sequence", jsjson.MakeNumber(1))
	if v := Verify(resign(t, l)); v.Valid || !contains(v.Errors, "Event at index 1 has sequence 1; expected 2") {
		t.Fatalf("%+v", v)
	}
}

// tests/ledger.test.ts:174.
func TestRejectsSessionMismatch(t *testing.T) {
	l := clone(t, validLedger(t, "CODEX_CLI"))
	l.Get("events").Items()[1].Obj().Set("sessionId", jsjson.MakeString("session-forged"))
	if v := Verify(resign(t, l)); v.Valid || !contains(v.Errors, "Event 2 has session id session-forged; expected session-demo") {
		t.Fatalf("%+v", v)
	}
}

// tests/ledger.test.ts:185.
func TestEnforcesLifecycleOrder(t *testing.T) {
	l, _ := Create(CreateOptions{LedgerID: "ledger-order", SessionID: "session-order", CreatedAt: createdAt})
	turn := `{"type":"TURN_STARTED","payload":{"turnId":"turn-1","turnOrdinal":1,"promptDigest":"` + digest("a") + `"}}`
	if _, err := Append(l, mustJSON(t, turn), AppendOptions{EventID: "event-1", OccurredAt: times[0]}); err == nil || !strings.Contains(err.Error(), "before SESSION_STARTED") {
		t.Fatalf("%v", err)
	}
	l = appendAt(t, l, `{"type":"SESSION_STARTED","payload":{"transport":"SIDE_CAR","workingDirectory":"C:/work/faultline"}}`, 0)
	active := appendAt(t, l, turn, 1)
	_, err := Append(active, mustJSON(t, `{"type":"SESSION_ENDED","payload":{"reason":"ABANDONED","completedTurns":0}}`), AppendOptions{EventID: "event-3", OccurredAt: times[2]})
	var ie *IntegrityError
	if !errors.As(err, &ie) || !strings.Contains(ie.Message, "ends while turn turn-1 is active") {
		t.Fatalf("%v", err)
	}
	// Append never mutates its input.
	if n := len(active.Get("events").Items()); n != 2 {
		t.Fatalf("%d events", n)
	}
}

// The event is the parsed input: its payload keys follow the schema, and a
// bad occurredAt or eventId is a ZodError, an explicit "" included.
func TestAppendParsesInputAndOptions(t *testing.T) {
	l, _ := Create(CreateOptions{LedgerID: "ledger-x", SessionID: "session-x", CreatedAt: createdAt})
	l, err := Append(l, mustJSON(t, `{"payload":{"workingDirectory":"/w","transport":"SIDE_CAR"},"type":"SESSION_STARTED"}`), AppendOptions{EventID: "e-1", OccurredAt: times[0]})
	if err != nil {
		t.Fatal(err)
	}
	if got := jsjson.Stringify(l.Get("events").Items()[0].Get("event")); got != `{"type":"SESSION_STARTED","payload":{"transport":"SIDE_CAR","workingDirectory":"/w"}}` {
		t.Fatal(got)
	}
	var zod *s.Error
	if _, err := Append(l, mustJSON(t, `{"type":"SESSION_ENDED","payload":{"reason":"COMPLETED","completedTurns":0}}`), AppendOptions{EventID: "e-2", OccurredAt: "2026-07-16"}); !errors.As(err, &zod) || !strings.Contains(err.Error(), "Expected a canonical ISO-8601 UTC timestamp") {
		t.Fatalf("%v", err)
	}
	if _, err := Append(l, mustJSON(t, `{"type":"SESSION_ENDED","payload":{"reason":"COMPLETED","completedTurns":0}}`), AppendOptions{HasEventID: true, OccurredAt: times[1]}); !errors.As(err, &zod) || !strings.Contains(err.Error(), `"eventId"`) {
		t.Fatalf("%v", err)
	}
	if _, err := Append(l, mustJSON(t, `{"type":"NOPE"}`), AppendOptions{}); !errors.As(err, &zod) {
		t.Fatalf("%v", err)
	}
}

func TestAppendAtomicReleasesLockOnFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "missing", "session.json")
	_, err := AppendAtomic(file, mustJSON(t, started), AppendOptions{})
	var ie *IntegrityError
	if !errors.As(err, &ie) || !strings.HasPrefix(ie.Message, "Unable to read lifecycle ledger "+file+": ENOENT") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Lstat(file + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock left behind: %v", err)
	}
}

// tests/ledger.test.ts:223, run concurrently: eight goroutines append
// through the lock (retrying while it is held), so the persisted sequence
// stays contiguous only if the lock excludes.
func TestSerializedAtomicAppends(t *testing.T) {
	file := filepath.Join(t.TempDir(), "session.json")
	l, _ := Create(CreateOptions{LedgerID: "ledger-append", SessionID: "session-append", CreatedAt: createdAt})
	if _, err := WriteAtomic(file, l); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendAtomic(file, mustJSON(t, `{"type":"SESSION_STARTED","payload":{"transport":"SIDE_CAR","workingDirectory":"C:/work/faultline"}}`), AppendOptions{EventID: "event-1", OccurredAt: times[0]}); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendAtomic(file, mustJSON(t, `{"type":"TURN_STARTED","payload":{"turnId":"turn-1","turnOrdinal":1,"promptDigest":"`+digest("a")+`"}}`), AppendOptions{EventID: "event-2", OccurredAt: times[1]}); err != nil {
		t.Fatal(err)
	}
	tool := mustJSON(t, `{"type":"TOOL_USE_STARTED","payload":{"turnId":"turn-1","turnOrdinal":1,"toolName":"shell","allowlistedFieldsDigest":"`+digest("b")+`"}}`)
	const workers, each = 8, 5
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for g := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range each {
				for {
					_, err := AppendAtomic(file, tool, AppendOptions{EventID: "tool-" + strconv.Itoa(g) + "-" + strconv.Itoa(k), OccurredAt: times[2]})
					if err != nil && strings.HasPrefix(err.Error(), "Could not acquire lifecycle ledger append lock") {
						time.Sleep(time.Millisecond)
						continue
					}
					if err != nil {
						errs <- err
					}
					break
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	persisted, err := ReadVerified(file)
	if err != nil {
		t.Fatal(err)
	}
	events := persisted.Get("events").Items()
	if len(events) != 2+workers*each {
		t.Fatalf("%d events", len(events))
	}
	for i, e := range events {
		if e.Get("sequence").Num() != float64(i+1) {
			t.Fatalf("event %d has sequence %v", i, e.Get("sequence").Num())
		}
	}
	if _, err := os.Lstat(file + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock left behind: %v", err)
	}
}
