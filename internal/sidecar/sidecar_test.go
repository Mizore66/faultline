package sidecar

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/ledger"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}

func repo(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.test")
	runGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-q", "-m", "clean fixture")
	return dir
}

func hook(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func q(s string) string { return jsjson.Stringify(jsjson.MakeString(s)) }

func ev(name, dir, session string, extra string) string {
	s := `{"hook_event_name":` + q(name) + `,"session_id":` + q(session) + `,"cwd":` + q(dir) + `,"model":"gpt-5.6"`
	if extra != "" {
		s += "," + extra
	}
	return s + "}"
}

func start(dir, session string) string {
	return ev("SessionStart", dir, session, `"transcript_path":"C:/private/transcript.jsonl"`)
}
func prompt(dir, session, turn, text string) string {
	return ev("UserPromptSubmit", dir, session, `"turn_id":`+q(turn)+`,"prompt":`+q(text)+`,"transcript_path":"C:/private/transcript.jsonl"`)
}
func stop(dir, session, turn string) string {
	return ev("Stop", dir, session, `"turn_id":`+q(turn)+`,"last_assistant_message":"private assistant output must never be retained"`)
}

func rec(t *testing.T, s string) Result {
	t.Helper()
	r, err := RecordObservedHook(hook(t, s), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return r
}

func eventTypes(t *testing.T, path string) []string {
	t.Helper()
	l, err := ledger.ReadVerified(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range l.Get("events").Items() {
		out = append(out, e.Get("event", "type").Str())
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestEventID(t *testing.T) {
	turn := "t-1"
	if got := EventID("s-1", nil, "session-start"); got != "codex-sidecar-6a840baf5d8c3ff241688aeb-session-session-start" {
		t.Fatal(got)
	}
	if got := EventID("s-1", &turn, "turn-stop"); !strings.HasPrefix(got, "codex-sidecar-6a840baf5d8c3ff241688aeb-") || !strings.HasSuffix(got, "-turn-stop") {
		t.Fatal(got)
	}
}

func TestSessionTurnStopIsIdempotent(t *testing.T) {
	dir := repo(t)
	base := `"session_id":"s-1","cwd":` + q(dir) + `,"model":"m"`
	var stderr bytes.Buffer
	steps := []string{
		`{"hook_event_name":"SessionStart",` + base + `}`,
		`{"hook_event_name":"UserPromptSubmit",` + base + `,"turn_id":"t-1","prompt":"secret prompt","transcript_path":"/x"}`,
		`{"hook_event_name":"Stop",` + base + `,"turn_id":"t-1"}`,
	}
	for _, s := range steps {
		if _, err := RecordObservedHook(hook(t, s), &stderr); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	r, err := RecordObservedHook(hook(t, steps[2]), &stderr)
	if err != nil || r.Status != "CHECKPOINT_RECORDED" || !r.Idempotent {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(r.LedgerPath)
	if bytes.Contains(b, []byte("secret prompt")) || bytes.Contains(b, []byte("transcript_path")) {
		t.Fatal("prompt or transcript path reached the ledger")
	}
}

func TestHeldLockTimesOut(t *testing.T) {
	dir := repo(t)
	rec, err := RecordingDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(rec, "codex-6a840baf5d8c3ff241688aeb14546e653774cd5387faf1cb982b0fbbf1fbb810.json")
	os.WriteFile(ledger+".sidecar.lock", nil, 0o600)
	begin := time.Now()
	_, err = RecordObservedHook(hook(t, `{"hook_event_name":"SessionStart","session_id":"s-1","cwd":`+q(dir)+`,"model":"m"}`), &bytes.Buffer{})
	if err == nil || err.Error() != "Another FaultLine sidecar event is still recording for this Codex session; inspect fl codex sidecar status after the current turn." {
		t.Fatalf("%v", err)
	}
	if time.Since(begin) < 1900*time.Millisecond {
		t.Fatalf("gave up after %v", time.Since(begin))
	}
}

// tests/codex-sidecar.test.ts:80
func TestBaselineSnapshotOnDirtyWorktree(t *testing.T) {
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, "preexisting-dirty.txt"), []byte("dirty-before-session\n"), 0o644)
	var stderr bytes.Buffer
	r, err := RecordObservedHook(hook(t, start(dir, "codex-session-baseline-dirty")), &stderr)
	if err != nil || r.Status != "SESSION_STARTED" || r.Idempotent {
		t.Fatalf("%+v %v", r, err)
	}
	if !strings.HasPrefix(stderr.String(), turnSnapshotObjectDBWarning+"\n") {
		t.Fatalf("stderr %q", stderr.String())
	}
	eq(t, eventTypes(t, r.LedgerPath), []string{"SESSION_STARTED", "SESSION_BASELINE_SNAPSHOT"})
	l, _ := ledger.ReadVerified(r.LedgerPath)
	e := l.Get("events").Items()[1]
	if !e.Get("event", "payload", "snapshot", "dirty").Bool() {
		t.Fatal("baseline snapshot is not dirty")
	}
	if e.Get("eventId").Str() != EventID("codex-session-baseline-dirty", nil, "session-baseline") {
		t.Fatal(e.Get("eventId").Str())
	}
}

// tests/codex-sidecar.test.ts:105
func TestRecordsLifecycleFactsAndNoPrivateText(t *testing.T) {
	dir := repo(t)
	secret := "Fix the issue with secret marker SIDE_CAR_PROMPT_DO_NOT_PERSIST"
	started := rec(t, start(dir, "codex-session-1"))
	turn := rec(t, prompt(dir, "codex-session-1", "codex-turn-1", secret))
	stopped := rec(t, stop(dir, "codex-session-1", "codex-turn-1"))
	if started.Status != "SESSION_STARTED" || started.Idempotent || started.SessionID != "codex-session-1" {
		t.Fatalf("%+v", started)
	}
	if turn.Status != "TURN_STARTED" || turn.Idempotent || turn.TurnID != "codex-turn-1" {
		t.Fatalf("%+v", turn)
	}
	if stopped.Status != "CHECKPOINT_RECORDED" || stopped.Idempotent || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(stopped.CheckpointDigest) {
		t.Fatalf("%+v", stopped)
	}
	if stopped.TurnSnapshot.Get("dirty").Bool() || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(stopped.TurnSnapshot.Get("headCommit").Str()) {
		t.Fatalf("%s", jsjson.Stringify(stopped.TurnSnapshot))
	}
	path, err := LedgerPath(dir, "codex-session-1")
	if err != nil || path != started.LedgerPath {
		t.Fatalf("%q %v", path, err)
	}
	if v := ledger.VerifyFile(path); !v.Valid || v.EventCount != 6 {
		t.Fatalf("%+v", v)
	}
	eq(t, eventTypes(t, path), []string{"SESSION_STARTED", "SESSION_BASELINE_SNAPSHOT", "TURN_STARTED", "TURN_COMPLETED", "TURN_TREE_SNAPSHOT", "WORKTREE_CHECKPOINT"})
	l, _ := ledger.ReadVerified(path)
	evs := l.Get("events").Items()
	p := evs[0].Get("event", "payload")
	if p.Get("transport").Str() != "SIDE_CAR" || p.Get("workingDirectory").Str() != dir || p.Get("model").Str() != "gpt-5.6" {
		t.Fatal(jsjson.Stringify(p))
	}
	if evs[2].Get("event", "payload", "promptDigest").Str() != "sha256:"+canonical.SHA256Hex(secret) || evs[2].Get("event", "payload", "turnOrdinal").Num() != 1 {
		t.Fatal(jsjson.Stringify(evs[2]))
	}
	if evs[5].Get("event", "payload", "afterTurnOrdinal").Num() != 1 || !evs[5].Get("event", "payload", "checkpoint", "clean").Bool() {
		t.Fatal(jsjson.Stringify(evs[5]))
	}
	b, _ := os.ReadFile(path)
	for _, s := range []string{secret, "private assistant output must never be retained", "C:/private/transcript.jsonl"} {
		if bytes.Contains(b, []byte(s)) {
			t.Fatalf("ledger holds %q", s)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".faultline")); err == nil {
		t.Fatal(".faultline exists")
	}
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain=v1", "--untracked-files=all").Output()
	if len(out) != 0 {
		t.Fatalf("status %q", out)
	}
}

// tests/codex-sidecar.test.ts:157
func TestRepeatedDeliveriesAreIdempotent(t *testing.T) {
	dir := repo(t)
	text := "Repeat the lifecycle event without duplicate records."
	a := rec(t, start(dir, "codex-session-1"))
	b := rec(t, prompt(dir, "codex-session-1", "codex-turn-1", text))
	c := rec(t, stop(dir, "codex-session-1", "codex-turn-1"))
	a2 := rec(t, start(dir, "codex-session-1"))
	b2 := rec(t, prompt(dir, "codex-session-1", "codex-turn-1", text))
	c2 := rec(t, stop(dir, "codex-session-1", "codex-turn-1"))
	if a.Idempotent || b.Idempotent || c.Idempotent || c.Status != "CHECKPOINT_RECORDED" {
		t.Fatal(a, b, c)
	}
	if a2.Status != "SESSION_STARTED" || !a2.Idempotent || b2.Status != "TURN_STARTED" || !b2.Idempotent {
		t.Fatal(a2, b2)
	}
	if c2.Status != "CHECKPOINT_RECORDED" || !c2.Idempotent || c2.CheckpointDigest != c.CheckpointDigest ||
		c2.TurnSnapshot.Get("digest").Str() != c.TurnSnapshot.Get("digest").Str() {
		t.Fatal(c2, c)
	}
	if n := len(eventTypes(t, a.LedgerPath)); n != 6 {
		t.Fatal(n)
	}
}

// tests/codex-sidecar.test.ts:187
func TestDirtyWorktreeSkipsCheckpoint(t *testing.T) {
	dir := repo(t)
	s, turn := "codex-session-dirty", "codex-turn-dirty"
	started := rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "Do work that leaves a dirty tree."))
	os.WriteFile(filepath.Join(dir, "untracked-by-agent.txt"), []byte("dirty\n"), 0o644)
	skipped := rec(t, stop(dir, s, turn))
	repeated := rec(t, stop(dir, s, turn))
	if skipped.Status != "CHECKPOINT_SKIPPED_DIRTY" || skipped.Reason != "DIRTY_WORKTREE" || skipped.Idempotent {
		t.Fatalf("%+v", skipped)
	}
	if !skipped.TurnSnapshot.Get("dirty").Bool() || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(skipped.TurnSnapshot.Get("treeDigest").Str()) {
		t.Fatal(jsjson.Stringify(skipped.TurnSnapshot))
	}
	if repeated.Status != "CHECKPOINT_SKIPPED_DIRTY" || repeated.Reason != "DIRTY_WORKTREE" || !repeated.Idempotent ||
		repeated.TurnSnapshot.Get("digest").Str() != skipped.TurnSnapshot.Get("digest").Str() {
		t.Fatalf("%+v", repeated)
	}
	eq(t, eventTypes(t, started.LedgerPath), []string{"SESSION_STARTED", "SESSION_BASELINE_SNAPSHOT", "TURN_STARTED", "TURN_COMPLETED", "TURN_TREE_SNAPSHOT"})
	if v := ledger.VerifyFile(started.LedgerPath); !v.Valid || v.EventCount != 5 {
		t.Fatalf("%+v", v)
	}
}

// tests/codex-sidecar.test.ts:234: a repository fsmonitor command is never run.
func TestFsmonitorIsNeverDispatched(t *testing.T) {
	dir := repo(t)
	hookFile := filepath.Join(dir, "forbidden-fsmonitor.sh")
	marker := filepath.Join(dir, "fsmonitor-ran")
	os.WriteFile(hookFile, []byte("#!/bin/sh\necho invoked > \"$1\"\n"), 0o755)
	runGit(t, dir, "add", "forbidden-fsmonitor.sh")
	runGit(t, dir, "commit", "-q", "-m", "add hostile fsmonitor fixture")
	runGit(t, dir, "config", "core.fsmonitor", `"`+hookFile+`" "`+marker+`"`)
	s, turn := "codex-session-safe-git", "codex-turn-safe-git"
	rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "Capture safely."))
	if r := rec(t, stop(dir, s, turn)); r.Status != "CHECKPOINT_RECORDED" {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("fsmonitor ran")
	}
}

func receiptFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if regexp.MustCompile(`^codex-stop-[a-f0-9]{64}\.json$`).MatchString(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// tests/codex-sidecar.test.ts:261
func TestMissingReceiptRecoversFromCheckpoint(t *testing.T) {
	dir := repo(t)
	s, turn := "codex-session-recover", "codex-turn-recover"
	rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "Record one checkpoint."))
	initial := rec(t, stop(dir, s, turn))
	if initial.Status != "CHECKPOINT_RECORDED" || initial.Idempotent {
		t.Fatalf("%+v", initial)
	}
	files := receiptFiles(t, filepath.Dir(initial.LedgerPath))
	if len(files) != 1 {
		t.Fatal(files)
	}
	os.Remove(files[0])
	recovered := rec(t, stop(dir, s, turn))
	if recovered.Status != "CHECKPOINT_RECORDED" || !recovered.Idempotent || recovered.CheckpointDigest != initial.CheckpointDigest ||
		recovered.TurnSnapshot.Get("digest").Str() != initial.TurnSnapshot.Get("digest").Str() {
		t.Fatalf("%+v", recovered)
	}
	if n := len(eventTypes(t, initial.LedgerPath)); n != 6 {
		t.Fatal(n)
	}
	if len(receiptFiles(t, filepath.Dir(initial.LedgerPath))) != 1 {
		t.Fatal("receipt not rewritten")
	}
}

func appendEvent(t *testing.T, path string, input string, id string) {
	t.Helper()
	l, err := ledger.ReadVerified(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err = ledger.Append(l, hook(t, input), ledger.AppendOptions{EventID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.WriteAtomic(path, l); err != nil {
		t.Fatal(err)
	}
}

// tests/codex-sidecar.test.ts:287
func TestInterruptedCompletedTurnFailsClosed(t *testing.T) {
	dir := repo(t)
	s, turn := "codex-session-interrupted", "codex-turn-interrupted"
	rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "Prepare an interrupted stop."))
	path, _ := LedgerPath(dir, s)
	appendEvent(t, path, `{"type":"TURN_COMPLETED","payload":{"turnId":"`+turn+`","turnOrdinal":1,"outcome":"COMPLETED"}}`, "simulated-interrupted-stop")
	r := rec(t, stop(dir, s, turn))
	if r.Status != "CHECKPOINT_SKIPPED_UNAVAILABLE" || r.Reason != "CHECKPOINT_UNAVAILABLE" || !r.Idempotent {
		t.Fatalf("%+v", r)
	}
	if n := len(eventTypes(t, path)); n != 4 {
		t.Fatal(n)
	}
}

// tests/codex-sidecar.test.ts:313
func TestDurableSnapshotRecoversWithoutRecapture(t *testing.T) {
	dir := repo(t)
	s, turn := "codex-session-snapshot-only", "codex-turn-snapshot-only"
	rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "Prepare a snapshot-only interrupted stop."))
	path, _ := LedgerPath(dir, s)
	// The baseline snapshot is a valid snapshot of the same clean tree.
	l, _ := ledger.ReadVerified(path)
	snapshot := l.Get("events").Items()[1].Get("event", "payload", "snapshot")
	turnID := turn
	appendEvent(t, path, `{"type":"TURN_COMPLETED","payload":{"turnId":"`+turn+`","turnOrdinal":1,"outcome":"COMPLETED"}}`, EventID(s, &turnID, "turn-stop"))
	appendEvent(t, path, `{"type":"TURN_TREE_SNAPSHOT","payload":{"turnId":"`+turn+`","turnOrdinal":1,"snapshot":`+jsjson.Stringify(snapshot)+`}}`, EventID(s, &turnID, "turn-snapshot"))
	for i := 0; i < 2; i++ {
		r := rec(t, stop(dir, s, turn))
		if r.Status != "TURN_SNAPSHOT_RECORDED" || !r.Idempotent || r.CheckpointDigest != "" ||
			r.TurnSnapshot.Get("digest").Str() != snapshot.Get("digest").Str() {
			t.Fatalf("%d %+v", i, r)
		}
	}
	eq(t, eventTypes(t, path), []string{"SESSION_STARTED", "SESSION_BASELINE_SNAPSHOT", "TURN_STARTED", "TURN_COMPLETED", "TURN_TREE_SNAPSHOT"})
}

// tests/codex-sidecar.test.ts:355
func TestStaleLockRecoveredAndStatusHidesPrompt(t *testing.T) {
	dir := repo(t)
	s, turn := "codex-session-status", "codex-turn-status"
	started := rec(t, start(dir, s))
	lock := started.LedgerPath + ".sidecar.lock"
	os.WriteFile(lock, []byte("interrupted\n"), 0o644)
	in, err := Inspect(dir, s)
	if err != nil || len(in.Recordings) != 1 || in.Recordings[0].Lock.Get("state").Str() != "ACTIVE" {
		t.Fatalf("%+v %v", in, err)
	}
	old := time.Now().Add(-60 * time.Second)
	os.Chtimes(lock, old, old)
	if r := rec(t, prompt(dir, s, turn, "Never surface this prompt.")); r.Status != "TURN_STARTED" {
		t.Fatalf("%+v", r)
	}
	in, err = Inspect(dir, s)
	if err != nil || len(in.Recordings) != 1 {
		t.Fatalf("%+v %v", in, err)
	}
	r := in.Recordings[0]
	if !r.Valid || r.SessionID != s || len(r.Errors) != 0 || r.LatestStop.Kind() != jsjson.Undefined {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(jsjson.Stringify(in.JSON()), "Never surface this prompt.") {
		t.Fatal("prompt in status")
	}
}

// tests/codex-sidecar.test.ts:384
func TestContradictoryReceiptInvalidatesStatus(t *testing.T) {
	dir := repo(t)
	s, turn := "codex-session-receipt-integrity", "codex-turn-receipt-integrity"
	rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "Record a checkpoint without storing this prompt."))
	if r := rec(t, stop(dir, s, turn)); r.Status != "CHECKPOINT_RECORDED" {
		t.Fatalf("%+v", r)
	}
	path, _ := LedgerPath(dir, s)
	files := receiptFiles(t, filepath.Dir(path))
	b, _ := os.ReadFile(files[0])
	re := regexp.MustCompile(`"checkpointDigest": "sha256:[a-f0-9]{64}"`)
	if !re.Match(b) {
		t.Fatalf("%s", b)
	}
	os.WriteFile(files[0], re.ReplaceAll(b, []byte(`"checkpointDigest": "sha256:`+strings.Repeat("f", 64)+`"`)), 0o644)
	in, err := Inspect(dir, s)
	if err != nil || len(in.Recordings) != 1 || in.Recordings[0].Valid {
		t.Fatalf("%+v %v", in, err)
	}
	if !strings.Contains(strings.Join(in.Recordings[0].Errors, "\n"), "checkpoint digest does not match the ledger") {
		t.Fatal(in.Recordings[0].Errors)
	}
}

// tests/codex-sidecar.test.ts:421
func TestToolUseIsDigestOnly(t *testing.T) {
	dir := repo(t)
	s, turn := "codex-session-tools", "codex-turn-tools"
	rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "secret prompt text"))
	tool := func(name string, withTranscript bool) Result {
		extra := `"turn_id":"` + turn + `","tool_name":"Bash","tool_call_id":"call-1","tool_input":{"command":"cat /etc/shadow"}`
		if withTranscript {
			extra += `,"transcript_path":"C:/private/transcript.jsonl"`
		}
		return rec(t, ev(name, dir, s, extra))
	}
	pre := tool("PreToolUse", true)
	if pre.Status != "TOOL_USE_STARTED" || pre.ToolName != "Bash" || pre.Idempotent {
		t.Fatalf("%+v", pre)
	}
	post := tool("PostToolUse", false)
	if post.Status != "TOOL_USE_COMPLETED" || post.ToolName != "Bash" {
		t.Fatalf("%+v", post)
	}
	l, _ := ledger.ReadVerified(pre.LedgerPath)
	callID := "call-1"
	want := DigestToolAllowlistedFields(s, turn, "Bash", &callID)
	n := 0
	for _, e := range l.Get("events").Items() {
		if ty := e.Get("event", "type").Str(); ty == "TOOL_USE_STARTED" || ty == "TOOL_USE_COMPLETED" {
			n++
			if e.Get("event", "payload", "allowlistedFieldsDigest").Str() != want || e.Get("event", "payload", "toolName").Str() != "Bash" {
				t.Fatal(jsjson.Stringify(e))
			}
		}
	}
	if n != 2 {
		t.Fatal(n)
	}
	b, _ := os.ReadFile(pre.LedgerPath)
	for _, x := range []string{"cat /etc/shadow", "secret prompt text", "transcript"} {
		if bytes.Contains(b, []byte(x)) {
			t.Fatalf("ledger holds %q", x)
		}
	}
}

func TestAnonymousToolCallKeyAndKeyOrder(t *testing.T) {
	dir := repo(t)
	s, turn := "s-anon", "t-anon"
	rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "p"))
	r := rec(t, ev("PreToolUse", dir, s, `"turn_id":"t-anon","tool_name":"shell"`))
	l, _ := ledger.ReadVerified(r.LedgerPath)
	last := l.Get("events").Items()[3]
	key := canonical.SHA256Hex("shell\x00t-anon")[:16]
	if last.Get("eventId").Str() != EventID(s, &turn, "tool-pre-anon-"+key) {
		t.Fatal(last.Get("eventId").Str())
	}
	if got := strings.Join(last.Get("event", "payload").Obj().Keys(), ","); got != "turnId,turnOrdinal,toolName,allowlistedFieldsDigest" {
		t.Fatal(got)
	}
	// With a tool_call_id the schema parse reorders toolCallId before the digest.
	rec(t, ev("PreToolUse", dir, s, `"turn_id":"t-anon","tool_name":"shell","tool_call_id":"c-1"`))
	l, _ = ledger.ReadVerified(r.LedgerPath)
	last = l.Get("events").Items()[4]
	if got := strings.Join(last.Get("event", "payload").Obj().Keys(), ","); got != "turnId,turnOrdinal,toolName,toolCallId,allowlistedFieldsDigest" {
		t.Fatal(got)
	}
}

func TestHookInputValidation(t *testing.T) {
	cases := map[string]string{
		`{"hook_event_name":"SessionEnd"}`:                                                                              "invalid_union_discriminator",
		`{"hook_event_name":"Stop","session_id":"s","cwd":"/x","model":"m"}`:                                            "turn_id",
		`{"hook_event_name":"PreToolUse","session_id":"s","cwd":"/x","model":"m","turn_id":"t","tool_name":"bad tool"}`: "Invalid",
	}
	for in, want := range cases {
		_, err := RecordObservedHook(hook(t, in), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", in, err)
		}
	}
}

func TestSessionStartNotStartedBeforeOthers(t *testing.T) {
	dir := repo(t)
	for name, want := range map[string]string{
		"UserPromptSubmit": "UserPromptSubmit was observed before SessionStart initialized the sidecar ledger.",
		"Stop":             "Stop was observed before SessionStart initialized the sidecar ledger.",
	} {
		extra := `"turn_id":"t-1"`
		if name == "UserPromptSubmit" {
			extra += `,"prompt":"p"`
		}
		_, err := RecordObservedHook(hook(t, ev(name, dir, "s-none", extra)), &bytes.Buffer{})
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err := RecordObservedHook(hook(t, ev("PostToolUse", dir, "s-none", `"turn_id":"t-1","tool_name":"x"`)), &bytes.Buffer{})
	if err == nil || err.Error() != "PostToolUse was observed before SessionStart initialized the sidecar ledger." {
		t.Error(err)
	}
}

func TestResultJSONKeyOrders(t *testing.T) {
	snap := jsjson.MustObject("digest", jsjson.MakeString("sha256:s"))
	cases := []struct {
		r    Result
		keys string
	}{
		{Result{Status: "SESSION_STARTED", LedgerPath: "/l", SessionID: "s"}, "status,ledgerPath,sessionId,idempotent"},
		{Result{Status: "TURN_STARTED", LedgerPath: "/l", SessionID: "s", TurnID: "t", Idempotent: true}, "status,ledgerPath,sessionId,turnId,idempotent"},
		{Result{Status: "TOOL_USE_STARTED", LedgerPath: "/l", SessionID: "s", TurnID: "t", ToolName: "x"}, "status,ledgerPath,sessionId,turnId,toolName,idempotent"},
		{Result{Status: "CHECKPOINT_RECORDED", LedgerPath: "/l", SessionID: "s", TurnID: "t", CheckpointDigest: "sha256:c"}, "status,ledgerPath,sessionId,turnId,idempotent,checkpointDigest"},
		{Result{Status: "CHECKPOINT_RECORDED", LedgerPath: "/l", SessionID: "s", TurnID: "t", CheckpointDigest: "sha256:c", TurnSnapshot: snap}, "status,ledgerPath,sessionId,turnId,idempotent,checkpointDigest,turnSnapshot"},
		{Result{Status: "CHECKPOINT_SKIPPED_DIRTY", LedgerPath: "/l", SessionID: "s", TurnID: "t", Reason: "DIRTY_WORKTREE"}, "status,ledgerPath,sessionId,turnId,idempotent,reason"},
		{Result{Status: "CHECKPOINT_SKIPPED_UNAVAILABLE", LedgerPath: "/l", SessionID: "s", TurnID: "t", Reason: "CHECKPOINT_UNAVAILABLE", TurnSnapshot: snap}, "status,ledgerPath,sessionId,turnId,idempotent,reason,turnSnapshot"},
		{Result{Status: "TURN_SNAPSHOT_RECORDED", LedgerPath: "/l", SessionID: "s", TurnID: "t", TurnSnapshot: snap}, "status,ledgerPath,sessionId,turnId,idempotent,turnSnapshot"},
	}
	for _, c := range cases {
		if got := strings.Join(c.r.JSON().Obj().Keys(), ","); got != c.keys {
			t.Errorf("%s: got %s want %s", c.r.Status, got, c.keys)
		}
	}
}

// resultFromReceipt and checkpointResult build the same three shapes.
func TestResultFromReceiptShapes(t *testing.T) {
	r := checkpointResult("CHECKPOINT_SKIPPED_DIRTY", "/l", "s", "t", true, jsjson.Value{}, jsjson.Value{})
	if r.Reason != "DIRTY_WORKTREE" || r.CheckpointDigest != "" {
		t.Fatalf("%+v", r)
	}
	r = checkpointResult("CHECKPOINT_SKIPPED_UNAVAILABLE", "/l", "s", "t", true, jsjson.Value{}, jsjson.Value{})
	if r.Reason != "CHECKPOINT_UNAVAILABLE" {
		t.Fatalf("%+v", r)
	}
	// A skipped receipt without a reason: DIRTY keeps DIRTY_WORKTREE, the
	// others are CHECKPOINT_UNAVAILABLE.
	for status, want := range map[string]string{"CHECKPOINT_SKIPPED_DIRTY": "DIRTY_WORKTREE", "CHECKPOINT_SKIPPED_UNAVAILABLE": "CHECKPOINT_UNAVAILABLE"} {
		receipt := jsjson.MustObject("status", jsjson.MakeString(status), "sessionId", jsjson.MakeString("s"), "turnId", jsjson.MakeString("t"))
		got := resultFromReceipt(receipt, jsjson.Value{}, "/l", hookInput{sessionID: "s", turnID: "t"})
		if got.Reason != want || !got.Idempotent {
			t.Errorf("%s: %+v", status, got)
		}
	}
}

// The receipt's atomic-write stage is `.<sha256(path)[0:24]>.<pid>.tmp`
// (codex-sidecar.ts:529), the name the live test's plant-stage creates.
func TestReceiptStageKey(t *testing.T) {
	path := "/repo/.git/faultline/recordings/codex-stop-" + strings.Repeat("a", 64) + ".json"
	// The key hashes the path as given; path.join/dirname use the platform's
	// separator (win32 on Windows, as Node does), so the stage keeps the
	// separator style of the platform, not the POSIX literal.
	dir := "/repo/.git/faultline/recordings/"
	if runtime.GOOS == "windows" {
		dir = `\repo\.git\faultline\recordings\`
	}
	want := dir + "." + canonical.SHA256Hex(path)[:24] + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	if got := receiptStagePath(path); got != want {
		t.Fatalf("%s != %s", got, want)
	}
	if !regexp.MustCompile(`[\\/]\.[a-f0-9]{24}\.\d+\.tmp$`).MatchString(receiptStagePath(path)) {
		t.Fatal(receiptStagePath(path))
	}
}

// A leftover stage with this process's pid makes the exclusive write fail.
func TestLeftoverReceiptStageFailsWithEEXIST(t *testing.T) {
	dir := repo(t)
	s, turn := "s-stage", "t-stage"
	started := rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "p"))
	recDir := filepath.Dir(started.LedgerPath)
	receipt := stopReceiptPath(recDir, s, turn)
	stage := receiptStagePath(receipt)
	os.WriteFile(stage, []byte("leftover"), 0o600)
	_, err := RecordObservedHook(hook(t, stop(dir, s, turn)), &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "EEXIST: file already exists, open '"+stage+"'") {
		t.Fatalf("%v", err)
	}
	// The ledger holds the completed turn; the leftover stage is untouched
	// by the failed exclusive open, then removed by the finally block.
	if _, err := os.Stat(stage); err == nil {
		t.Fatal("stage not removed")
	}
	if n := len(eventTypes(t, started.LedgerPath)); n != 6 {
		t.Fatal(n)
	}
}

// Math.round(0.5) is 1 and Math.round(-0.5) is -0; max(0, -0) gives 0.
func TestJSRound(t *testing.T) {
	for in, want := range map[float64]float64{0.5: 1, 1.5: 2, 2.5: 3, 0.49: 0, -1.5: -1, -2.5: -2, -0.4: 0, 7: 7} {
		if got := jsRound(in); got != want {
			t.Errorf("round(%v) = %v want %v", in, got, want)
		}
	}
	if got := jsRound(-0.5); got != 0 || !math.Signbit(got) {
		t.Errorf("round(-0.5) = %v, want -0", got)
	}
	if got := math.Max(0, jsRound(-0.5)); got != 0 || math.Signbit(got) {
		t.Errorf("max(0, round(-0.5)) = %v, want +0", got)
	}
	if got := math.Max(0, jsRound(-100.4)); got != 0 {
		t.Errorf("%v", got)
	}
}

func TestStorageErrors(t *testing.T) {
	dir := repo(t)
	// Not a repository.
	if _, err := RecordingDirectory(t.TempDir(), false); err == nil || !strings.HasPrefix(err.Error(), "FaultLine could not safely inspect this Git worktree: ") {
		t.Fatalf("%v", err)
	}
	// create=false never creates storage.
	got, err := RecordingDirectory(dir, false)
	if err != nil || filepath.Base(got) != "recordings" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "faultline")); err == nil {
		t.Fatal("storage created")
	}
	if _, err := LedgerPath(dir, "bad id"); err == nil {
		t.Fatal("bad id accepted")
	}
	// create=true makes both 0700.
	if _, err := RecordingDirectory(dir, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"faultline", "faultline/recordings"} {
		info, err := os.Stat(filepath.Join(dir, ".git", p))
		// Windows has no POSIX permission bits: it reports directories as
		// 0777 whatever the requested mode, so only there the 0700 mode
		// assertion is not made; existence and type are still asserted.
		if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
			t.Fatalf("%s %v %v", p, info, err)
		}
	}
	// A symlinked faultline is refused for both.
	os.RemoveAll(filepath.Join(dir, ".git", "faultline"))
	os.Symlink(t.TempDir(), filepath.Join(dir, ".git", "faultline"))
	for _, create := range []bool{false, true} {
		if _, err := RecordingDirectory(dir, create); err == nil || err.Error() != "FaultLine sidecar storage must be a real directory, not a symlink or file." {
			t.Fatalf("%v %v", create, err)
		}
	}
}
