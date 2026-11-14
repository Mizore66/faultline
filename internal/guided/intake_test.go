package guided

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

func taskIntakeUnits(s string, n int) string { return jsstr.FromUTF16(jsstr.ToUTF16(s)[:n]) }

// taskIntakeLone is prefix followed by a lone high surrogate (U+D83D).
func taskIntakeLone(prefix string) string {
	return jsstr.FromUTF16(append(jsstr.ToUTF16(prefix), 0xD83D))
}

func TestGuidedSymptom(t *testing.T) {
	got := deriveSymptom("first error\r\n fine \n ASSERT last \r\n trailing\n")
	if got != "ASSERT last" {
		t.Fatalf("got %q", got)
	}
	const fb = "CI reported a failure; review the attached log."
	cases := []struct{ name, in, want string }{
		{"last matching beats last nonblank", "Expected x\nlater line\n", "Expected x"},
		{"case insensitive", "ok\nfAiLuRe here\nzzz", "fAiLuRe here"},
		{"last nonblank when none match", "alpha\n beta \n\n \n", "beta"},
		{"fallback empty", "", fb},
		{"fallback blank only", " \r\n\t\n\u00a0\u2028\ufeff\n", fb},
		{"js trim nbsp bom", "\u00a0\ufeffsome ERROR\u3000\u2003", "some ERROR"},
		{"zero width space is not whitespace", "\u200bERROR\u200b", "\u200bERROR\u200b"},
		{"u2028 is not a line break", "ok\u2028error\u2029", "ok\u2028error"},
		{"lone cr is not a line break", "a\rb error", "a\rb error"},
		{"long sliced at 500 units", strings.Repeat("e", 600), strings.Repeat("e", 500)},
		{"surrogate pair split", strings.Repeat("a", 499) + "\U0001F600 error", taskIntakeLone(strings.Repeat("a", 499))},
		{"dotless i is not i", "fa\u0131l", "fa\u0131l"},
		{"kelvin sign is not k", "x\nfa\u212A", "fa\u212A"},
	}
	for _, c := range cases {
		if got := deriveSymptom(c.in); got != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	// The "none match" cases above end on the last nonblank line; confirm the
	// non-matching variants really are the fallback path, not the match path.
	if got := deriveSymptom("x\nfa\u0131l\nlast"); got != "last" {
		t.Fatalf("got %q", got)
	}
}

func TestGuidedIntakePacketLog(t *testing.T) {
	if got := taskIntakeLogPrefix("abc"); got != "abc" {
		t.Fatal(got)
	}
	in := " " + strings.Repeat("y", 3998) + "\U0001F600z"
	if got, want := taskIntakeLogPrefix(in), taskIntakeLone(" "+strings.Repeat("y", 3998)); got != want {
		t.Fatalf("surrogate boundary %q", got[len(got)-6:])
	}
	if got := taskIntakeLogPrefix(strings.Repeat("q", 4001)); len(got) != 4000 {
		t.Fatal(len(got))
	}
}

// taskIntakeFake is a controlled dependency set that records ordering.
type taskIntakeFake struct {
	calls     []string
	millis    int64
	nowCalls  int
	runtime   jsjson.Value
	rtErr     error
	spawn     nodeproc.Result
	proposals []jsjson.Value
	proposeFn func(string, jsjson.Value) (jsjson.Value, error)
	writeErr  error
	drafts    []incident.DraftInput
	logText   string
	logErr    error
	paths     []string
}

func (f *taskIntakeFake) deps() intakeDeps {
	d := defaultIntakeDeps()
	d.nowMillis = func() int64 { f.calls = append(f.calls, "millis"); return f.millis }
	d.now = func() time.Time {
		f.calls = append(f.calls, "now")
		f.nowCalls++
		return time.UnixMilli(1784203200000 + int64(f.nowCalls))
	}
	d.readLog = func(p string) (string, error) {
		f.calls = append(f.calls, "readLog")
		f.paths = append(f.paths, p)
		return f.logText, f.logErr
	}
	d.resolveRuntime = func(alias string) (jsjson.Value, error) {
		f.calls = append(f.calls, "runtime:"+alias)
		return f.runtime, f.rtErr
	}
	d.spawn = func(string, []string, nodeproc.SpawnSyncOptions) nodeproc.Result {
		f.calls = append(f.calls, "head")
		return f.spawn
	}
	realCreate := d.createDraft
	d.createDraft = func(in incident.DraftInput) (jsjson.Value, error) {
		f.calls = append(f.calls, "createDraft")
		f.drafts = append(f.drafts, in)
		return realCreate(in)
	}
	d.propose = func(store string, in jsjson.Value) (jsjson.Value, error) {
		f.calls = append(f.calls, "propose")
		f.proposals = append(f.proposals, in)
		if f.proposeFn != nil {
			return f.proposeFn(store, in)
		}
		return taskIntakeStubProposal(in), nil
	}
	d.writeDraft = func(store string, draft jsjson.Value) (string, error) {
		f.calls = append(f.calls, "writeDraft")
		if f.writeErr != nil {
			return "", f.writeErr
		}
		return store + "/drafts/x.json", nil
	}
	return d
}

func taskIntakeStubProposal(in jsjson.Value) jsjson.Value {
	d := func(c string) jsjson.Value { return jsjson.MakeString("sha256:" + strings.Repeat(c, 64)) }
	return jsjson.MustObject(
		"proposalId", in.Get("proposalId"),
		"proposalDigest", d("a"),
		"incidentPacketDigest", d("b"),
		"witness", jsjson.MustObject("commandDigest", jsjson.MakeString(gitproof.CommandDigest(in.Get("witness", "command").Str()))),
	)
}

func taskIntakeStr(s string) *string { return &s }

func taskIntakeBase() (IntakeOptions, *taskIntakeFake) {
	zero := 0
	f := &taskIntakeFake{
		millis:  1784203200000,
		spawn:   nodeproc.Result{Status: &zero, Stdout: []byte(strings.Repeat("2", 40) + "\n" + strings.Repeat("1", 40) + "\n")},
		logText: "boom\n",
	}
	return IntakeOptions{Repository: "/repo", CILogPath: "ci.log", WitnessStore: "/w", DraftStore: "/d"}, f
}

func TestGuidedIntakePairedRangeBeforeLog(t *testing.T) {
	for _, c := range []struct{ from, to *string }{{taskIntakeStr("a"), nil}, {nil, taskIntakeStr("b")}, {taskIntakeStr(""), nil}, {nil, taskIntakeStr("")}} {
		o, f := taskIntakeBase()
		o.From, o.To = c.from, c.to
		_, err := createIntake(o, f.deps())
		if err == nil || err.Error() != "Guided investigate accepts --from and --to together, or neither for the conservative local HEAD-parent fallback." {
			t.Fatalf("%v", err)
		}
		if len(f.calls) != 0 {
			t.Fatalf("calls %v", f.calls)
		}
	}
}

func TestGuidedIntakeOrderAndDefaults(t *testing.T) {
	o, f := taskIntakeBase()
	o.CILogPath = "rel/ci.log"
	o.RuntimeAlias = taskIntakeStr("node")
	f.runtime = jsjson.MustObject("requested", jsjson.MakeString("node"), "image", jsjson.MakeString("node@sha256:"+strings.Repeat("d", 64)))
	res, err := createIntake(o, f.deps())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"readLog", "millis", "runtime:node", "head", "now", "createDraft", "propose", "createDraft", "writeDraft"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("order %v", f.calls)
	}
	if !filepath.IsAbs(f.paths[0]) || !strings.HasSuffix(filepath.ToSlash(f.paths[0]), "rel/ci.log") {
		t.Fatalf("path %q", f.paths[0])
	}
	if id := "guided-" + strconv.FormatInt(1784203200000, 36); res.Draft.Get("draftId").Str() != id {
		t.Fatalf("id %q want %q", res.Draft.Get("draftId").Str(), id)
	}
	if res.Draft.Get("command").Str() != taskIntakeDefaultCommand {
		t.Fatalf("command %q", res.Draft.Get("command").Str())
	}
	if res.DraftPath != "/d/drafts/x.json" {
		t.Fatal(res.DraftPath)
	}
	// createdAt is the single post-id clock read and is shared by both drafts.
	if f.drafts[0].CreatedAt != "2026-07-16T12:00:00.001Z" || f.drafts[1].CreatedAt != f.drafts[0].CreatedAt || f.nowCalls != 1 {
		t.Fatalf("createdAt %q %q now=%d", f.drafts[0].CreatedAt, f.drafts[1].CreatedAt, f.nowCalls)
	}
	if res.Draft.Get("createdAt").Str() != f.drafts[0].CreatedAt {
		t.Fatal(res.Draft.Get("createdAt").Str())
	}
	if f.drafts[0].Proposal.Kind() != jsjson.Undefined || f.drafts[1].Proposal.Kind() != jsjson.Object {
		t.Fatalf("proposal binding kinds %v %v", f.drafts[0].Proposal.Kind(), f.drafts[1].Proposal.Kind())
	}
	if !reflect.DeepEqual(f.drafts[1].Proposal.Obj().Keys(), []string{"proposalId", "proposalDigest", "incidentPacketDigest", "commandDigest"}) {
		t.Fatalf("binding keys %v", f.drafts[1].Proposal.Obj().Keys())
	}
	if f.drafts[1].Proposal.Get("commandDigest").Str() != gitproof.CommandDigest(taskIntakeDefaultCommand) {
		t.Fatal("commandDigest must come from proposal.witness")
	}
	if !reflect.DeepEqual(f.drafts[0].Runtime.Obj().Keys(), []string{"requested", "image"}) {
		t.Fatal("runtime keys")
	}
}

func TestGuidedIntakeIDRules(t *testing.T) {
	o, f := taskIntakeBase()
	o.IncidentID = taskIntakeStr("")
	if _, err := createIntake(o, f.deps()); err == nil {
		t.Fatal("empty supplied id must be retained and fail the draft schema")
	}
	for _, c := range f.calls {
		if c == "millis" {
			t.Fatal("supplied empty id must not read the clock for an id")
		}
	}
	if len(f.drafts) == 0 || f.drafts[0].ID != "" {
		t.Fatalf("drafts %+v", f.drafts)
	}
	o, f = taskIntakeBase()
	o.IncidentID = taskIntakeStr("mine-1")
	if res, err := createIntake(o, f.deps()); err != nil || res.Draft.Get("draftId").Str() != "mine-1" {
		t.Fatalf("%v", err)
	}
	if len(f.calls) < 2 || f.calls[1] == "millis" {
		t.Fatalf("%v", f.calls)
	}
}

func TestGuidedIntakeCommandTrim(t *testing.T) {
	for _, c := range []struct {
		in   *string
		want string
	}{
		{nil, taskIntakeDefaultCommand},
		{taskIntakeStr(""), taskIntakeDefaultCommand},
		{taskIntakeStr(" \u00a0\ufeff\n"), taskIntakeDefaultCommand},
		{taskIntakeStr("\u2003 go test ./... \u3000"), "go test ./..."},
		{taskIntakeStr("\u200bgo\u200b"), "\u200bgo\u200b"},
	} {
		o, f := taskIntakeBase()
		o.Command = c.in
		res, err := createIntake(o, f.deps())
		if err != nil || res.Draft.Get("command").Str() != c.want {
			t.Fatalf("%v %q", err, res.Draft.Get("command").Str())
		}
		if got := f.proposals[0].Get("witness", "command").Str(); got != c.want {
			t.Fatalf("witness command %q", got)
		}
	}
}

func TestGuidedIntakeProposalShape(t *testing.T) {
	o, f := taskIntakeBase()
	f.logText = "line one\r\nERROR: bad \u00e9\r\n" + strings.Repeat("z", 5000)
	o.IncidentID = taskIntakeStr("pid-1")
	if _, err := createIntake(o, f.deps()); err != nil {
		t.Fatal(err)
	}
	p := f.proposals[0]
	if !reflect.DeepEqual(p.Obj().Keys(), []string{"proposalId", "proposalOrigin", "incidentPacket", "witness"}) {
		t.Fatal(p.Obj().Keys())
	}
	pk := p.Get("incidentPacket")
	if !reflect.DeepEqual(pk.Obj().Keys(), []string{"symptom", "ciLog", "repositoryLanguage", "repositorySummary"}) {
		t.Fatal(pk.Obj().Keys())
	}
	if pk.Get("symptom").Str() != "ERROR: bad \u00e9" {
		t.Fatalf("symptom %q", pk.Get("symptom").Str())
	}
	if pk.Get("ciLog").Str() != taskIntakeUnits(f.logText, 4000) || pk.Get("repositoryLanguage").Str() != "Unknown" {
		t.Fatal("log or language")
	}
	wantSummary := "Guided CI-log intake for local range " + strings.Repeat("1", 40) + " \u2192 " + strings.Repeat("2", 40) +
		". Review-only until human approval and freeze; nothing executed at intake."
	if pk.Get("repositorySummary").Str() != wantSummary {
		t.Fatalf("summary %q", pk.Get("repositorySummary").Str())
	}
	w := p.Get("witness")
	if !reflect.DeepEqual(w.Obj().Keys(), []string{"behavior", "command", "overlays", "policy"}) ||
		w.Get("behavior").Str() != "The human-supplied command must exit successfully at the selected immutable Git states." ||
		w.Get("overlays").Kind() != jsjson.Array || len(w.Get("overlays").Items()) != 0 {
		t.Fatal("witness")
	}
	pol := w.Get("policy")
	if !reflect.DeepEqual(pol.Obj().Keys(), []string{"network", "credentials", "timeoutSeconds"}) ||
		pol.Get("network").Str() != "disabled" || pol.Get("credentials").Str() != "redacted" || pol.Get("timeoutSeconds").Num() != 300 {
		t.Fatal("policy")
	}
	if p.Get("proposalId").Str() != "pid-1" || p.Get("proposalOrigin").Str() != "HUMAN" {
		t.Fatal("ids")
	}
}

func TestGuidedIntakeExplicitRangeStillReadsHead(t *testing.T) {
	one, two := 128, 0
	o, f := taskIntakeBase()
	o.From, o.To = taskIntakeStr("aaaa111"), taskIntakeStr("bbbb222")
	f.spawn = nodeproc.Result{Status: &one, Stderr: []byte("fatal: not a git repository\n")}
	_, err := createIntake(o, f.deps())
	if err == nil || err.Error() != "FaultLine could not read a local Git HEAD for guided intake: fatal: not a git repository" {
		t.Fatalf("%v", err)
	}
	if !reflect.DeepEqual(f.calls, []string{"readLog", "millis", "head"}) {
		t.Fatalf("%v", f.calls)
	}
	o, f = taskIntakeBase()
	_ = two
	o.From, o.To = taskIntakeStr(strings.Repeat("3", 40)), taskIntakeStr(strings.Repeat("4", 40))
	res, err := createIntake(o, f.deps())
	if err != nil {
		t.Fatal(err)
	}
	r := res.Draft.Get("range")
	if r.Get("ancestor").Str() != strings.Repeat("3", 40) || r.Get("source").Str() != "EXPLICIT" {
		t.Fatalf("range %v", r)
	}
}

func TestGuidedIntakeEmptyFromToFallsBackToHead(t *testing.T) {
	o, f := taskIntakeBase()
	o.From, o.To = taskIntakeStr(""), taskIntakeStr("")
	res, err := createIntake(o, f.deps())
	if err != nil {
		t.Fatal(err)
	}
	if res.Draft.Get("range", "source").Str() != "LOCAL_HEAD_PARENT" || f.drafts[0].Range.Kind() != jsjson.Undefined {
		t.Fatal("expected local head parent")
	}
	// A truthy from with an empty to is paired (both supplied) yet falls back.
	o, f = taskIntakeBase()
	o.From, o.To = taskIntakeStr("x"), taskIntakeStr("")
	if _, err := createIntake(o, f.deps()); err != nil || f.drafts[0].Range.Kind() != jsjson.Undefined {
		t.Fatalf("one empty side must fall back: %v", err)
	}
}

func TestGuidedIntakeRangeRefusals(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	cases := []struct {
		name, out, want string
		from, to        *string
	}{
		{"root", a + "\n\n", "HEAD is a root commit; select an explicit range instead of guessing a base", nil, nil},
		{"merge", a + "\n" + b + " " + strings.Repeat("c", 40) + "\n", "HEAD has multiple parents; select an explicit range instead of guessing a parent", nil, nil},
		{"self parent", a + "\n" + a + "\n", "", nil, nil},
		{"equal explicit", a + "\n" + b + "\n", "incident range ancestor and descendant must be different", taskIntakeStr(a), taskIntakeStr(a)},
	}
	zero := 0
	for _, c := range cases {
		o, f := taskIntakeBase()
		o.From, o.To = c.from, c.to
		f.spawn = nodeproc.Result{Status: &zero, Stdout: []byte(c.out)}
		_, err := createIntake(o, f.deps())
		if err == nil || (c.want != "" && err.Error() != c.want) {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, call := range f.calls {
			if call == "propose" || call == "writeDraft" {
				t.Fatalf("%s: wrote after refusal: %v", c.name, f.calls)
			}
		}
	}
}

func TestGuidedIntakeErrorOrdering(t *testing.T) {
	o, f := taskIntakeBase()
	f.logErr = errors.New("ENOENT: no such file")
	if _, err := createIntake(o, f.deps()); err == nil || err.Error() != "ENOENT: no such file" || !reflect.DeepEqual(f.calls, []string{"readLog"}) {
		t.Fatalf("%v %v", err, f.calls)
	}
	o, f = taskIntakeBase()
	o.RuntimeAlias = taskIntakeStr("nope")
	f.rtErr = errors.New("runtime unavailable")
	if _, err := createIntake(o, f.deps()); err == nil || err.Error() != "runtime unavailable" ||
		!reflect.DeepEqual(f.calls, []string{"readLog", "millis", "runtime:nope"}) {
		t.Fatalf("%v %v", err, f.calls)
	}
	// An empty alias pointer is still resolved.
	o, f = taskIntakeBase()
	o.RuntimeAlias = taskIntakeStr("")
	f.rtErr = errors.New("empty alias")
	if _, err := createIntake(o, f.deps()); err == nil || f.calls[2] != "runtime:" {
		t.Fatalf("%v %v", err, f.calls)
	}
	// Proposal failure (as an empty log fails the real packet schema): no draft write.
	o, f = taskIntakeBase()
	f.proposeFn = func(string, jsjson.Value) (jsjson.Value, error) { return jsjson.Value{}, errors.New("schema") }
	if _, err := createIntake(o, f.deps()); err == nil || err.Error() != "schema" ||
		!reflect.DeepEqual(f.calls, []string{"readLog", "millis", "head", "now", "createDraft", "propose"}) {
		t.Fatalf("%v %v", err, f.calls)
	}
	o, f = taskIntakeBase()
	f.writeErr = errors.New("Immutable incident draft already exists: x")
	res, err := createIntake(o, f.deps())
	if err == nil || err.Error() != f.writeErr.Error() || res.DraftPath != "" || res.Draft.Kind() != jsjson.Undefined {
		t.Fatalf("%v %+v", err, res)
	}
}

func taskIntakeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@example.invalid", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// taskIntakeGitMissing returns a failure message when git cannot be found.
// Git is a required prerequisite of the real-Git intake tests: they fail
// loudly rather than skip.
func taskIntakeGitMissing(lookPath func(string) (string, error)) string {
	if _, err := lookPath("git"); err != nil {
		return "git is required for the real-Git intake tests: " + err.Error()
	}
	return ""
}

func TestGuidedIntakeGitPrerequisiteIsLoud(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if got := taskIntakeGitMissing(missing); got != "git is required for the real-Git intake tests: not found" {
		t.Fatalf("%q", got)
	}
	if got := taskIntakeGitMissing(func(string) (string, error) { return "/usr/bin/git", nil }); got != "" {
		t.Fatalf("%q", got)
	}
}

func taskIntakeRepo(t *testing.T, commits int) (dir string, shas []string) {
	t.Helper()
	if msg := taskIntakeGitMissing(exec.LookPath); msg != "" {
		t.Fatalf("%s", msg)
	}
	dir = t.TempDir()
	taskIntakeGit(t, dir, "init", "-q")
	for i := 0; i < commits; i++ {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		taskIntakeGit(t, dir, "add", "f.txt")
		taskIntakeGit(t, dir, "commit", "-q", "-m", "c")
		shas = append(shas, taskIntakeGit(t, dir, "rev-parse", "HEAD"))
	}
	return
}

func TestGuidedIntakeRealGitBaseline(t *testing.T) {
	repo, shas := taskIntakeRepo(t, 2)
	logPath := filepath.Join(t.TempDir(), "ci.log")
	logText := "step 1\r\nFAIL: TestThing\r\nnoise after\r\n"
	if err := os.WriteFile(logPath, []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, ds := filepath.Join(repo, ".faultline", "witnesses"), filepath.Join(repo, ".faultline", "incidents")
	res, err := CreateIntake(IntakeOptions{Repository: repo, CILogPath: logPath, WitnessStore: ws, DraftStore: ds,
		Command: taskIntakeStr("  go test ./...  "), IncidentID: taskIntakeStr("real-1")})
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(filepath.Join(ds, "drafts", "real-1.json")); res.DraftPath != want {
		t.Fatalf("%q want %q", res.DraftPath, want)
	}
	rng := res.Draft.Get("range")
	if rng.Get("ancestor").Str() != shas[0] || rng.Get("descendant").Str() != shas[1] || rng.Get("source").Str() != "LOCAL_HEAD_PARENT" {
		t.Fatalf("range %v", rng)
	}
	draftBytes, err := os.ReadFile(res.DraftPath)
	if err != nil {
		t.Fatal(err)
	}
	canon, err := canonical.CanonicalJSON(res.Draft)
	if err != nil || string(draftBytes) != canon+"\n" {
		t.Fatalf("draft bytes differ: %v", err)
	}
	propPath := filepath.Join(ws, "proposals", "real-1.json")
	propBytes, err := os.ReadFile(propPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(propBytes), "\n") != 1 || !strings.HasSuffix(string(propBytes), "\n") {
		t.Fatal("proposal not single canonical line")
	}
	if filepath.Separator == '/' {
		for _, p := range []string{res.DraftPath, propPath} {
			if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
				t.Fatalf("mode %s %v", p, err)
			}
		}
	}
	for _, need := range []string{`"symptom":"FAIL: TestThing"`, `"ciLog":"step 1\r\nFAIL: TestThing\r\nnoise after\r\n"`,
		`"command":"go test ./..."`, `"timeoutSeconds":300`, `"proposalOrigin":"HUMAN"`, `"network":"disabled"`,
		`"overlays":[]`, `"repositoryLanguage":"Unknown"`,
		`"commandDigest":"` + res.Draft.Get("commandDigest").Str() + `"`} {
		if !strings.Contains(string(propBytes), need) {
			t.Fatalf("proposal lacks %s:\n%s", need, propBytes)
		}
	}
	for _, k := range []string{"proposalId", "proposalDigest", "incidentPacketDigest"} {
		if !strings.Contains(string(propBytes), `"`+k+`":"`+res.Draft.Get("review", "proposal", k).Str()+`"`) {
			t.Fatalf("binding %s not in proposal", k)
		}
	}
	if got := taskIntakeGit(t, repo, "status", "--porcelain", "--untracked-files=no"); got != "" {
		t.Fatalf("intake changed tracked files: %q", got)
	}
}

func TestGuidedIntakeRealGitRootAndOrphan(t *testing.T) {
	repo, _ := taskIntakeRepo(t, 1)
	log := filepath.Join(t.TempDir(), "ci.log")
	if err := os.WriteFile(log, []byte("error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, ds := filepath.Join(repo, ".faultline", "w"), filepath.Join(repo, ".faultline", "d")
	o := IntakeOptions{Repository: repo, CILogPath: log, WitnessStore: ws, DraftStore: ds, IncidentID: taskIntakeStr("root-1")}
	if _, err := CreateIntake(o); err == nil || err.Error() != "HEAD is a root commit; select an explicit range instead of guessing a base" {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "proposals", "root-1.json")); !os.IsNotExist(err) {
		t.Fatal("root refusal must not persist a proposal")
	}
	// An explicit range succeeds even in a root repository (HEAD is read only).
	o.From, o.To = taskIntakeStr(strings.Repeat("1", 40)), taskIntakeStr(strings.Repeat("2", 40))
	o.IncidentID = taskIntakeStr("orph-1")
	if _, err := CreateIntake(o); err != nil {
		t.Fatal(err)
	}
	// Re-running collides on the proposal before any draft write.
	if _, err := CreateIntake(o); err == nil || !strings.HasPrefix(err.Error(), "Immutable witness lock record already exists: ") {
		t.Fatalf("%v", err)
	}
	// New witness store, existing draft: proposal persists, draft collides.
	o.WitnessStore = filepath.Join(repo, ".faultline", "w2")
	draft := filepath.Join(ds, "drafts", "orph-1.json")
	before, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateIntake(o)
	if err == nil || !strings.HasPrefix(err.Error(), "Immutable incident draft already exists: ") {
		t.Fatalf("%v", err)
	}
	if st, err := os.Stat(filepath.Join(o.WitnessStore, "proposals", "orph-1.json")); err != nil || st.Size() == 0 {
		t.Fatalf("orphan proposal missing: %v", err)
	}
	if after, _ := os.ReadFile(draft); string(after) != string(before) {
		t.Fatal("existing draft must be untouched")
	}
}
