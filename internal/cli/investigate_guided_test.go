package cli

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/internal/guided"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/witness"
)

const (
	guidedTestDigest  = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	guidedTestDigest2 = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	guidedTestImage   = "example.test/app@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	guidedUsage       = "FaultLine error: Usage: fl investigate"
)

// guidedHarness drives guidedInvestigate with a recording guided.Run seam and
// a recording signal seam. events is the ordered log of seam activity, with
// stdout and stderr writes as "write:" entries.
type guidedHarness struct {
	mu        sync.Mutex
	events    []string
	options   []guided.Options
	cleanups  int
	cancelled bool // the signal context handed to Run is already cancelled
	run       func(guided.Options) (jsjson.Value, error)
	stdout    bytes.Buffer
	stderr    bytes.Buffer
}

func (h *guidedHarness) log(e string) {
	h.mu.Lock()
	h.events = append(h.events, e)
	h.mu.Unlock()
}

type recordingWriter struct {
	h    *guidedHarness
	name string
	buf  *bytes.Buffer
}

func (w recordingWriter) Write(p []byte) (int, error) {
	w.h.log("write:" + w.name)
	return w.buf.Write(p)
}

func (h *guidedHarness) invoke(args ...string) (stdout, stderr string, code int) {
	e := &env{stdout: recordingWriter{h, "stdout", &h.stdout}, stderr: recordingWriter{h, "stderr", &h.stderr}}
	deps := guidedDeps{
		interrupt: func() (context.Context, func()) {
			h.log("interrupt")
			ctx, cancel := context.WithCancel(context.Background())
			if h.cancelled {
				cancel()
			}
			return ctx, func() { h.log("cleanup"); h.cleanups++; cancel() }
		},
		run: func(o guided.Options) (jsjson.Value, error) {
			h.log("run")
			h.options = append(h.options, o)
			if o.OnPhase != nil {
				o.OnPhase("PREFLIGHT", nil)
			}
			return h.run(o)
		},
	}
	var err error
	if thrown := jsexc.Try(func() { err = guidedInvestigate(e, args, deps) }); thrown != nil {
		err = thrown // a JS exception reaches the CLI's catch, as in RunWithStdin
	}
	if err != nil {
		message := formatFailure(err.Error())
		e.stderr.Write([]byte(message + "\n"))
		return h.stdout.String(), h.stderr.String(), 1
	}
	return h.stdout.String(), h.stderr.String(), e.exitCode
}

func guidedResult(status string) jsjson.Value {
	return jsjson.MustObject("status", jsjson.MakeString(status),
		"retainedFrozenDigest", jsjson.MakeString(guidedTestDigest2), "note", jsjson.MakeString("n"))
}

func okRun(status string) func(guided.Options) (jsjson.Value, error) {
	return func(guided.Options) (jsjson.Value, error) { return guidedResult(status), nil }
}

func guidedRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func ptrTo[T any](v T) *T { return &v }

func wantStdout(v jsjson.Value) string { return jsjson.StringifyIndent(v, "  ") + "\n" }

func TestGuidedInvestigateDispatchGuardsKeepOrder(t *testing.T) {
	guard := "FaultLine error: Guided investigate (--ci-log / --resume) cannot be combined with `fl investigate git` or `fl investigate turns`.\nNext: pnpm fl help\n"
	both := "FaultLine error: Guided investigate accepts either --ci-log <file> or --resume <incident-id>, not both.\nNext: pnpm fl help\n"
	paired := "FaultLine error: Guided investigate accepts --from and --to together, or neither for the conservative local HEAD-parent fallback.\nNext: pnpm fl help\n"
	for _, c := range []struct {
		args   []string
		stderr string
	}{
		// git/turns conflict wins over both-mode refusal and unpaired range.
		{[]string{"investigate", "git", "--ci-log", "a", "--resume", "b", "--from", "x"}, guard},
		{[]string{"investigate", "turns", "--ci-log", "a", "--resume", "b"}, guard},
		{[]string{"investigate", "--ci-log", "a", "--resume", "b", "--from", "x"}, both},
		// Paired range is required even on resume, where the values are ignored.
		{[]string{"investigate", "--resume", "r1", "--from", "x"}, paired},
		{[]string{"investigate", "--ci-log", "a", "--to", "y"}, paired},
		// The following token is the value even when it is a flag: both are defined.
		{[]string{"investigate", "--ci-log", "--resume", "b"}, both},
		{[]string{"investigate", "--resume", "--ci-log", "b"}, both},
	} {
		h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
		stdout, stderr, code := h.invokeViaDispatch(t, c.args)
		if code != 1 || stdout != "" || stderr != c.stderr || len(h.options) != 0 || len(h.events) != 0 {
			t.Errorf("%q: code=%d stdout=%q stderr=%q options=%d events=%v", c.args, code, stdout, stderr, len(h.options), h.events)
		}
	}
}

// invokeViaDispatch runs the real dispatch and so the real guided.Run only for
// refusals that must occur before anything is invoked.
func (h *guidedHarness) invokeViaDispatch(t *testing.T, args []string) (string, string, int) {
	t.Helper()
	return run(args...)
}

func TestGuidedInvestigateEqualsSpellingAndEmptyValues(t *testing.T) {
	// "--ci-log=x" is not the flag: no value follows, so the usage text applies.
	for _, args := range [][]string{{"investigate", "--ci-log=x"}, {"investigate", "--resume=x"}} {
		stdout, stderr, code := run(args...)
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, guidedUsage) {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	// An empty following token is a defined value and is preserved.
	h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	if _, _, code := h.invoke("--ci-log", ""); code != 0 || h.options[0].CILogPath == nil || *h.options[0].CILogPath != "" || h.options[0].ResumeID != nil {
		t.Fatalf("empty ci-log: code=%d options=%+v", code, h.options)
	}
	// An empty resume id is defined too: it becomes the (invalid) session id,
	// so the binding lookup refuses it before the handler or guided run.
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	stdout, stderr, code := h.invoke("--resume", "")
	want := "FaultLine error: [\n  {\n    \"validation\": \"regex\",\n    \"code\": \"invalid_string\",\n    \"message\": \"draftId must be a safe identifier\",\n    \"path\": []\n  }\n]\nNext: pnpm fl help\n"
	if code != 1 || stdout != "" || stderr != want || len(h.options) != 0 || !reflect.DeepEqual(h.events, []string{"write:stderr"}) {
		t.Fatalf("empty resume: code=%d stdout=%q stderr=%q options=%d events=%v", code, stdout, stderr, len(h.options), h.events)
	}
}

func TestGuidedInvestigateTrailingFlagIsNotGuided(t *testing.T) {
	// A trailing flag has no value (TS undefined), so the usage text applies.
	for _, args := range [][]string{{"investigate", "--ci-log"}, {"investigate", "x", "--resume"}} {
		stdout, stderr, code := run(args...)
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, guidedUsage) {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestGuidedInvestigateOptionsFresh(t *testing.T) {
	repo := guidedRepo(t)
	t.Chdir(repo) // relative store/ledger arguments resolve inside the owned directory
	h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	stdout, stderr, code := h.invoke("--ci-log", "ci.log", "--repo", repo, "--command", "", "--from", "a", "--to", "b", "--id", "inc-1",
		"--runtime", "node", "--image", guidedTestImage, "--expect-digest", guidedTestDigest, "--ledger", "l.json",
		"--max-states", "0x10", "--output", "out", "--port", "", "--store", "ws", "--draft-store", "ds",
		// ignored flags
		"--timeout-seconds", "1", "--runtime-mapping", "m.json", "--latest", "--minimize", "--two-step", "--tty", "--browser", "--json")
	result := guidedResult("GUIDED_PROOF_BUNDLE_READY")
	if code != 0 || stdout != wantStdout(result) || stderr != "FaultLine guided investigate: PREFLIGHT\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(h.options) != 1 {
		t.Fatalf("runs %d", len(h.options))
	}
	got := h.options[0]
	want := guided.Options{
		Repository: repo, CILogPath: ptrTo("ci.log"), Command: ptrTo(""), From: ptrTo("a"), To: ptrTo("b"), IncidentID: ptrTo("inc-1"),
		Runtime: ptrTo("node"), Image: ptrTo(guidedTestImage), ExpectDigest: ptrTo(guidedTestDigest), LedgerFile: ptrTo("l.json"),
		MaxStates: ptrTo(16.0), Output: ptrTo("out"), ReviewPort: ptrTo(0.0),
		WitnessStore: ptrTo(nodefs.Resolve("ws")), DraftStore: ptrTo(nodefs.Resolve("ds")),
	}
	if got.Signal == nil || got.OnPhase == nil || got.OnReviewReady == nil || got.Runner != nil || got.WaitForFreeze != nil {
		t.Fatalf("seams: signal=%v phase=%v ready=%v runner=%v wait=%v", got.Signal, got.OnPhase != nil, got.OnReviewReady != nil, got.Runner != nil, got.WaitForFreeze != nil)
	}
	got.Signal, got.OnPhase, got.OnReviewReady = nil, nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options\n got %+v\nwant %+v", got, want)
	}
	if want := []string{"interrupt", "run", "write:stderr", "write:stdout", "cleanup"}; !reflect.DeepEqual(h.events, want) {
		t.Fatalf("events %v", h.events)
	}
}

func TestGuidedInvestigateOptionsResumeAndDefaults(t *testing.T) {
	repo := guidedRepo(t)
	h := &guidedHarness{run: okRun("GUIDED_INVESTIGATION_NOT_PROOF")}
	stdout, _, code := h.invoke("--resume", "r1", "--repo", repo, "--id", "other", "--max-states", "", "--port", "abc")
	if code != 1 || stdout != wantStdout(guidedResult("GUIDED_INVESTIGATION_NOT_PROOF")) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	got := h.options[0]
	if got.ResumeID == nil || *got.ResumeID != "r1" || got.IncidentID != nil || got.CILogPath != nil {
		t.Fatalf("resume=%v incident=%v ci=%v", got.ResumeID, got.IncidentID, got.CILogPath)
	}
	if got.MaxStates == nil || *got.MaxStates != 0 || got.ReviewPort == nil || !math.IsNaN(*got.ReviewPort) {
		t.Fatalf("maxStates=%v port=%v", got.MaxStates, got.ReviewPort)
	}
	if got.WitnessStore != nil || got.DraftStore != nil || got.Image != nil || got.Command != nil || got.Runtime != nil || got.Output != nil {
		t.Fatalf("absent options became defined: %+v", got)
	}
}

func TestGuidedInvestigateRepoDefaultsToCwd(t *testing.T) {
	h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	if _, _, code := h.invoke("--ci-log", "ci.log"); code != 0 {
		t.Fatal(code)
	}
	if want := nodefs.Resolve(""); h.options[0].Repository != want {
		t.Fatalf("repository %q want %q", h.options[0].Repository, want)
	}
	// An empty or flag-valued --repo is the following token, resolved verbatim.
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--ci-log", "ci.log", "--repo", "--store")
	if want := nodefs.Resolve("--store"); h.options[0].Repository != want || h.options[0].WitnessStore != nil {
		t.Fatalf("repository %q store %v", h.options[0].Repository, h.options[0].WitnessStore)
	}
}

func writeGuidedConfig(t *testing.T, repo, image string) {
	t.Helper()
	dir := filepath.Join(repo, ".faultline")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"schemaVersion":"faultline.config.v1","image":"` + image + `","snapshot":{"trackedFilesOnly":true},"stores":{"incidents":"a","witnesses":"b","recordings":"c"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedInvestigateConfigImagePrecedence(t *testing.T) {
	repo := guidedRepo(t)
	writeGuidedConfig(t, repo, guidedTestImage)
	h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--ci-log", "x", "--repo", repo)
	if got := h.options[0].Image; got == nil || *got != guidedTestImage {
		t.Fatalf("config image %v", got)
	}
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--ci-log", "x", "--repo", repo, "--image", "")
	if got := h.options[0].Image; got == nil || *got != "" {
		t.Fatalf("explicit empty image must win: %v", got)
	}
	// A malformed config surfaces before the signal handler or guided run.
	if err := os.WriteFile(filepath.Join(repo, ".faultline", "config.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	stdout, stderr, code := h.invoke("--ci-log", "x", "--repo", repo)
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine error: ") || !reflect.DeepEqual(h.events, []string{"write:stderr"}) {
		t.Fatalf("code=%d stdout=%q stderr=%q events=%v", code, stdout, stderr, h.events)
	}
	// An explicit image bypasses reading the config at all.
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	if _, _, code := h.invoke("--ci-log", "x", "--repo", repo, "--image", "i"); code != 0 {
		t.Fatalf("explicit image with bad config: %d", code)
	}
}

func readBinding(t *testing.T, store, id string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(store, "bindings", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func seedBinding(t *testing.T, store, id, digest string, ledger *string) string {
	t.Helper()
	if _, err := witness.WriteSessionBinding(store, witness.BindingInput{IncidentID: id, ExpectDigest: &digest, LedgerPath: ledger,
		UpdatedAt: ptrTo("2026-01-02T03:04:05.000Z")}); err != nil {
		t.Fatal(err)
	}
	text, ok := readBinding(t, store, id)
	if !ok {
		t.Fatal("seed binding missing")
	}
	return text
}

func TestGuidedInvestigateBindingAfterBlockedResult(t *testing.T) {
	repo := guidedRepo(t)
	store := filepath.Join(repo, "ds")
	seedBinding(t, store, "inc-1", guidedTestDigest, nil)
	blocked := jsjson.MustObject("status", jsjson.MakeString("PREFLIGHT_BLOCKED"), "retainedFrozenDigest", jsjson.MakeNull())
	h := &guidedHarness{run: func(guided.Options) (jsjson.Value, error) { return blocked, nil }}
	stdout, _, code := h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store, "--id", "inc-1", "--ledger", "led.json")
	if code != 1 || stdout != wantStdout(blocked) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	if got := h.options[0]; got.ExpectDigest == nil || *got.ExpectDigest != guidedTestDigest || got.LedgerFile == nil || *got.LedgerFile != "led.json" {
		t.Fatalf("inherited facts %+v", got)
	}
	// The binding is rewritten with the resolved ledger path; the digest is the
	// inherited one, and updatedAt is the only member that moves.
	text, _ := readBinding(t, store, "inc-1")
	parsed, err := jsjson.Parse(text)
	if err != nil || !strings.HasSuffix(text, "\n") {
		t.Fatalf("binding %q %v", text, err)
	}
	updated := parsed.Get("updatedAt").Str()
	want := `{"expectDigest":"` + guidedTestDigest + `","incidentId":"inc-1","ledgerPath":"` + nodefs.Resolve("led.json") +
		`","schemaVersion":"faultline.incident-session-binding.v1","updatedAt":"` + updated + `"}` + "\n"
	if text != want || updated == "2026-01-02T03:04:05.000Z" {
		t.Fatalf("binding bytes\n got %q\nwant %q", text, want)
	}
}

func TestGuidedInvestigateBindingUsesWrapperIDOnResumeAndSkipsOtherCases(t *testing.T) {
	repo := guidedRepo(t)
	store := filepath.Join(repo, "ds")
	before := seedBinding(t, store, "wrapper", guidedTestDigest, nil)
	h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	if _, _, code := h.invoke("--resume", "r1", "--id", "wrapper", "--repo", repo, "--draft-store", store); code != 0 {
		t.Fatalf("code %d", code)
	}
	if got := h.options[0]; got.ResumeID == nil || *got.ResumeID != "r1" || got.IncidentID != nil ||
		got.ExpectDigest == nil || *got.ExpectDigest != guidedTestDigest {
		t.Fatalf("options %+v", got)
	}
	after, _ := readBinding(t, store, "wrapper")
	if after == before {
		t.Fatal("binding was not rewritten for the wrapper id")
	}
	if _, ok := readBinding(t, store, "r1"); ok {
		t.Fatal("binding written under the resume id")
	}

	// No id: nothing is bound even with an explicit digest, and generated ids
	// are not bound; a newly observed retainedFrozenDigest is never bound.
	store2 := filepath.Join(repo, "ds2")
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store2, "--expect-digest", guidedTestDigest)
	if _, err := os.Lstat(filepath.Join(store2, "bindings")); err == nil {
		t.Fatal("bindings written without an id")
	}
	// An id without any expected digest: observed digest is not bound.
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store2, "--id", "fresh")
	if _, ok := readBinding(t, store2, "fresh"); ok {
		t.Fatal("observed digest was bound")
	}
	// Explicit digest and id on a fresh store: bound with that digest only.
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store2, "--id", "explicit", "--expect-digest", guidedTestDigest)
	text, ok := readBinding(t, store2, "explicit")
	if !ok || !strings.Contains(text, `"expectDigest":"`+guidedTestDigest+`"`) || strings.Contains(text, "ledgerPath") ||
		strings.Contains(text, guidedTestDigest2) {
		t.Fatalf("explicit binding %q", text)
	}
}

func TestGuidedInvestigateExplicitOverridesWinOverBindingButMalformedBindingFails(t *testing.T) {
	repo := guidedRepo(t)
	store := filepath.Join(repo, "ds")
	seedBinding(t, store, "inc-1", guidedTestDigest, ptrTo("/bound/ledger.json"))
	h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store, "--id", "inc-1", "--expect-digest", guidedTestDigest2, "--ledger", "mine.json")
	// With an id, the session facts replace the flags entirely: the explicit
	// override is the first argument to the inherit step.
	got := h.options[0]
	if *got.ExpectDigest != guidedTestDigest2 || *got.LedgerFile != "mine.json" {
		t.Fatalf("explicit overrides lost: %v %v", *got.ExpectDigest, *got.LedgerFile)
	}

	if err := os.WriteFile(filepath.Join(store, "bindings", "inc-1.json"), []byte("{\"bad\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, readErr := witness.ReadSessionBinding(store, "inc-1")
	if readErr == nil {
		t.Fatal("fixture binding is not malformed")
	}
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	stdout, stderr, code := h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store, "--id", "inc-1",
		"--expect-digest", guidedTestDigest, "--ledger", "l.json")
	if code != 1 || stdout != "" || stderr != formatFailure(readErr.Error())+"\n" || !reflect.DeepEqual(h.events, []string{"write:stderr"}) {
		t.Fatalf("code=%d stdout=%q stderr=%q events=%v want %q", code, stdout, stderr, h.events, readErr)
	}
}

func TestGuidedInvestigateStoredBindingIdentityOnResume(t *testing.T) {
	repo := guidedRepo(t)
	store := filepath.Join(repo, "ds")
	// --id selects the binding; the resume id's own binding is never read.
	seedBinding(t, store, "r1", guidedTestDigest, nil)
	h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--resume", "r1", "--id", "empty-id", "--repo", repo, "--draft-store", store)
	if h.options[0].ExpectDigest != nil {
		t.Fatalf("resume id binding leaked: %v", *h.options[0].ExpectDigest)
	}
	h = &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	h.invoke("--resume", "r1", "--repo", repo, "--draft-store", store)
	if got := h.options[0].ExpectDigest; got == nil || *got != guidedTestDigest {
		t.Fatalf("resume id binding: %v", got)
	}
}

func TestGuidedInvestigateErrorsPrintPhaseThenFailureWithoutBindingOrOutput(t *testing.T) {
	repo := guidedRepo(t)
	store := filepath.Join(repo, "ds")
	before := seedBinding(t, store, "inc-1", guidedTestDigest, nil)
	h := &guidedHarness{run: func(o guided.Options) (jsjson.Value, error) {
		return jsjson.Value{}, errors.New("scripted guided refusal")
	}}
	stdout, stderr, code := h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store, "--id", "inc-1")
	want := "FaultLine guided investigate: PREFLIGHT\nFaultLine error: scripted guided refusal\nNext: pnpm fl help\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if after, _ := readBinding(t, store, "inc-1"); after != before {
		t.Fatal("binding rewritten after a propagated error")
	}
	if !reflect.DeepEqual(h.events, []string{"interrupt", "run", "write:stderr", "cleanup", "write:stderr"}) {
		t.Fatalf("events %v", h.events)
	}
}

func TestGuidedInvestigateDetailedPhaseAndReadyBytes(t *testing.T) {
	h := &guidedHarness{}
	h.run = func(o guided.Options) (jsjson.Value, error) {
		d := "resume xé"
		o.OnPhase("INTAKE", &d)
		o.OnReviewReady("http://127.0.0.1:1234")
		return guidedResult("GUIDED_PROOF_BUNDLE_READY"), nil
	}
	_, stderr, code := h.invoke("--resume", "x")
	want := "FaultLine guided investigate: PREFLIGHT\nFaultLine guided investigate: INTAKE (resume xé)\n" +
		"FaultLine local witness review: http://127.0.0.1:1234\nApprove, then Freeze (separate clicks). This command continues automatically after freeze. Press Ctrl+C to cancel and resume later with --resume <id>.\n"
	if code != 0 || stderr != want {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestGuidedInvestigateAbortedSignalDoesNotSuppressResult(t *testing.T) {
	repo := guidedRepo(t)
	store := filepath.Join(repo, "ds")
	seedBinding(t, store, "inc-1", guidedTestDigest, nil)
	h := &guidedHarness{cancelled: true}
	h.run = func(o guided.Options) (jsjson.Value, error) {
		if o.Signal.Err() == nil {
			t.Error("signal is not aborted")
		}
		return guidedResult("GUIDED_PROOF_BUNDLE_READY"), nil
	}
	stdout, _, code := h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store, "--id", "inc-1")
	if code != 0 || stdout != wantStdout(guidedResult("GUIDED_PROOF_BUNDLE_READY")) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	text, _ := readBinding(t, store, "inc-1")
	if strings.Contains(text, "2026-01-02T03:04:05.000Z") {
		t.Fatalf("binding not rewritten: %q", text)
	}
}

func TestGuidedInvestigateBindingFailureIsStandardFailureWithoutOutput(t *testing.T) {
	repo := guidedRepo(t)
	h := &guidedHarness{run: okRun("GUIDED_PROOF_BUNDLE_READY")}
	// A regular file where the bindings directory belongs makes the write throw after guided returns.
	store := filepath.Join(repo, "ds")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	seedBinding(t, store, "inc-1", guidedTestDigest, nil)
	h.run = func(guided.Options) (jsjson.Value, error) {
		// Replace the bindings directory by a regular file while guided runs.
		if err := os.RemoveAll(filepath.Join(store, "bindings")); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(filepath.Join(store, "bindings"), []byte("x"), 0o600); err != nil {
			t.Error(err)
		}
		return guidedResult("GUIDED_PROOF_BUNDLE_READY"), nil
	}
	stdout, stderr, code := h.invoke("--ci-log", "x", "--repo", repo, "--draft-store", store, "--id", "inc-1")
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine guided investigate: PREFLIGHT\nFaultLine error: ") || !strings.HasSuffix(stderr, "\nNext: pnpm fl help\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if h.cleanups != 1 {
		t.Fatalf("cleanups %d", h.cleanups)
	}
}

func TestGuidedInvestigateExitCodes(t *testing.T) {
	for status, want := range map[string]int{"GUIDED_PROOF_BUNDLE_READY": 0, "GUIDED_INVESTIGATION_NOT_PROOF": 1, "PREFLIGHT_BLOCKED": 1, "other": 1} {
		h := &guidedHarness{run: okRun(status)}
		stdout, stderr, code := h.invoke("--ci-log", "x")
		if code != want || stdout != wantStdout(guidedResult(status)) || stderr != "FaultLine guided investigate: PREFLIGHT\n" || h.cleanups != 1 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q cleanups=%d", status, code, stdout, stderr, h.cleanups)
		}
	}
}
