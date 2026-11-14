package guided

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

// ngdEmit records one checked diagnostic. The expected text is a literal copied
// from the frozen TypeScript (src/guided-investigate.ts), never from Go output;
// the record is emitted only after the produced error equals it exactly. The
// difftest native collector parses the record and binds it to this child.
func ngdEmit(t *testing.T, got error, want string) {
	t.Helper()
	if got == nil || got.Error() != want {
		t.Fatalf("diagnostic mismatch: got %v want %q", got, want)
	}
	quoted, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("NATIVE-DIAGNOSTIC %s", quoted)
}

// ngdDoneOnReadContext is a valid context whose cancellation becomes visible
// exactly when Done is first read: Err is nil before, context.Canceled after,
// and the Done channel is closed by that first read.
type ngdDoneOnReadContext struct {
	context.Context
	done chan struct{}
	read atomic.Bool
	once sync.Once
}

func (c *ngdDoneOnReadContext) Done() <-chan struct{} {
	c.once.Do(func() { c.read.Store(true); close(c.done) })
	return c.done
}

func (c *ngdDoneOnReadContext) Err() error {
	if c.read.Load() {
		return context.Canceled
	}
	return nil
}

// ngdEmitResult asserts a success projection's status and exact note, then
// emits one record holding the complete result JSON.
//
// nextCommands is asserted exactly against the frozen format applied to the
// scripted inputs (never read back from the result or a production constant),
// and doctor/draft/continuation are asserted to pass the scripted inputs
// through unchanged (a nil entry means the frozen result holds JSON null).
func ngdEmitResult(t *testing.T, got jsjson.Value, err error, status, note string, nextCommands []string, passthrough map[string]jsjson.Value) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("status").Str() != status || got.Get("note").Str() != note {
		t.Fatalf("projection mismatch: status=%q note=%q", got.Get("status").Str(), got.Get("note").Str())
	}
	items := got.Get("nextCommands").Items()
	if len(items) != len(nextCommands) {
		t.Fatalf("nextCommands: got %s want %q", jsjson.Stringify(got.Get("nextCommands")), nextCommands)
	}
	for i, want := range nextCommands {
		if items[i].Str() != want {
			t.Fatalf("nextCommands[%d]: got %q want %q", i, items[i].Str(), want)
		}
	}
	for key, scripted := range passthrough {
		want := "null"
		if scripted.Kind() != jsjson.Undefined {
			want = jsjson.Stringify(scripted)
		}
		if have := jsjson.Stringify(got.Get(key)); have != want {
			t.Fatalf("%s: got %s want scripted %s", key, have, want)
		}
	}
	quoted, mErr := json.Marshal(jsjson.StringifyIndent(got, "  "))
	if mErr != nil {
		t.Fatal(mErr)
	}
	t.Logf("NATIVE-DIAGNOSTIC %s", quoted)
}

const (
	// src/guided-investigate.ts lines noted per case.
	ngdPaired     = "Guided investigate accepts --from and --to together, or neither for the conservative local HEAD-parent fallback."                                                       // :210
	ngdHeadFail   = "FaultLine could not read a local Git HEAD for guided intake: fatal: not a git repository"                                                                               // :141
	ngdHeadBlank  = "FaultLine could not read a local Git HEAD for guided intake: supply --from and --to explicitly"                                                                         // :141 (blank-detail fallback)
	ngdHeadEmpty  = "FaultLine could not read a local Git HEAD for guided intake; supply --from and --to explicitly."                                                                        // :146
	ngdMismatch   = "Guided --resume refuses a --repo value that differs from the immutable incident draft repository."                                                                      // :354
	ngdNoInput    = "Guided investigate requires --ci-log <file> or --resume <incident-id>."                                                                                                 // :360
	ngdInvalid    = "Guided investigate cannot continue with an invalid frozen witness: digest tampered; bad hash"                                                                           // :403
	ngdExpect     = "--expect-digest does not match the frozen witness observed by the guided flow."                                                                                         // :411
	ngdBind       = "Guided investigate failed to bind a retained freeze digest: e1; e2"                                                                                                     // :416
	ngdBindStatus = "Guided investigate failed to bind a retained freeze digest: MISMATCH"                                                                                                   // :416 status fallback
	ngdImage      = "--image must match the digest-pinned runtime recorded in the immutable incident draft."                                                                                 // :272
	ngdRuntime    = "Guided investigate needs a digest-pinned runtime before localization. Pass --runtime <node|python|go> after pulling the curated tag, or --image <digest-pinned-image>." // :282
	ngdDocker     = "Guided investigate requires a READY Docker investigation preflight. Run `fl doctor --repo .` and start Docker before continuing."                                       // :432
	ngdCancelled  = "Guided investigate cancelled before freeze."                                                                                                                            // :96/:105/:180
)

// TestGuidedNativeDiagnostics reaches, through the real private branches of
// intake.go, guided.go and freeze.go, every error literal of those files. Each
// child emits exactly one checked record.
func TestGuidedNativeDiagnostics(t *testing.T) {
	t.Run("intake_paired_range", func(t *testing.T) {
		o, f := taskIntakeBase()
		o.From = taskIntakeStr("a")
		_, err := createIntake(o, f.deps())
		ngdEmit(t, err, ngdPaired)
	})
	t.Run("head_command_failure", func(t *testing.T) {
		status := 128
		spawn := func(string, []string, nodeproc.SpawnSyncOptions) nodeproc.Result {
			return nodeproc.Result{Status: &status, Stderr: []byte("\n fatal: not a git repository \n")}
		}
		_, _, err := taskLocalHead(spawn, "/repo")
		ngdEmit(t, err, ngdHeadFail)
	})
	t.Run("head_command_failure_blank_detail", func(t *testing.T) {
		status := 2
		spawn := func(string, []string, nodeproc.SpawnSyncOptions) nodeproc.Result {
			return nodeproc.Result{Status: &status, Stderr: []byte("  \n")}
		}
		_, _, err := taskLocalHead(spawn, "/repo")
		ngdEmit(t, err, ngdHeadBlank)
	})
	t.Run("head_empty", func(t *testing.T) {
		status := 0
		spawn := func(string, []string, nodeproc.SpawnSyncOptions) nodeproc.Result {
			return nodeproc.Result{Status: &status, Stdout: []byte("  \n p\n")}
		}
		_, _, err := taskLocalHead(spawn, "/repo")
		ngdEmit(t, err, ngdHeadEmpty)
	})
	t.Run("resume_repository_mismatch", func(t *testing.T) {
		f := newGrFake(t)
		f.draft = grParse(t, `{"draftId":"guided-1","repository":"/elsewhere"}`)
		o, d := f.options()
		o.CILogPath, o.ResumeID = nil, grStr(grDraftID)
		_, err := run(o, d)
		ngdEmit(t, err, ngdMismatch)
	})
	t.Run("missing_inputs", func(t *testing.T) {
		f := newGrFake(t)
		o, d := f.options()
		o.CILogPath = nil
		_, err := run(o, d)
		ngdEmit(t, err, ngdNoInput)
	})
	t.Run("invalid_frozen_witness", func(t *testing.T) {
		f := newGrFake(t)
		f.verifies = []gitproof.WitnessVerification{{Errors: []string{"digest tampered", "bad hash"}}}
		o, d := f.options()
		_, err := run(o, d)
		ngdEmit(t, err, ngdInvalid)
	})
	t.Run("expect_digest_mismatch", func(t *testing.T) {
		f := newGrFake(t)
		f.verifies = []gitproof.WitnessVerification{grUnfrozen()}
		other := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
		o, d := f.options()
		o.ExpectDigest = &other
		_, err := run(o, d)
		ngdEmit(t, err, ngdExpect)
	})
	t.Run("retained_binding_failure", func(t *testing.T) {
		f := newGrFake(t)
		f.verifies = []gitproof.WitnessVerification{grFrozenOK(),
			{Errors: []string{"e1", "e2"}, ExternalDigestStatus: "MATCH"}}
		o, d := f.options()
		_, err := run(o, d)
		ngdEmit(t, err, ngdBind)
	})
	t.Run("retained_binding_status_fallback", func(t *testing.T) {
		f := newGrFake(t)
		f.verifies = []gitproof.WitnessVerification{grFrozenOK(),
			{Valid: true, ExternalDigestStatus: "MISMATCH"}}
		o, d := f.options()
		_, err := run(o, d)
		ngdEmit(t, err, ngdBindStatus)
	})
	t.Run("image_mismatch", func(t *testing.T) {
		f := newGrFake(t)
		f.draft = grParse(t, `{"draftId":"guided-1","repository":"/work/repo","runtime":{"requested":"node","image":"`+grImage+`"}}`)
		o, d := f.options()
		o.Image = grStr(grOther)
		_, err := run(o, d)
		ngdEmit(t, err, ngdImage)
	})
	t.Run("missing_runtime", func(t *testing.T) {
		f := newGrFake(t)
		f.draft = grParse(t, `{"draftId":"guided-1","repository":"/work/repo"}`)
		o, d := f.options()
		_, err := run(o, d)
		ngdEmit(t, err, ngdRuntime)
	})
	t.Run("docker_not_ready", func(t *testing.T) {
		f := newGrFake(t)
		f.doctor = grDoctor(t, "READY", "NOT_READY")
		f.draft = grParse(t, `{"draftId":"guided-1","repository":"/work/repo","runtime":{"requested":"node","image":"`+grImage+`"}}`)
		o, d := f.options()
		_, err := run(o, d)
		ngdEmit(t, err, ngdDocker)
	})
	// Scripted success projections (private fake dependencies; no Docker or
	// provider). These are scripted projections, not native proof: they exist so
	// the status, nextCommands and note sinks of guided.go have produced text.
	// Expected status and note bytes are copied from the frozen TypeScript
	// (src/guided-investigate.ts:344 and :471-474), never from the constants.
	t.Run("projection_preflight_blocked", func(t *testing.T) {
		f := newGrFake(t)
		f.doctor = grDoctor(t, "BLOCKED", "NOT_READY")
		o, d := f.options()
		got, err := run(o, d)
		ngdEmitResult(t, got, err, "PREFLIGHT_BLOCKED",
			"Local CLI preflight failed; fix Node before continuing the guided investigate flow.",
			[]string{"Install Node.js 22+", "fl doctor --repo ."},
			map[string]jsjson.Value{"doctor": f.doctor})
	})
	t.Run("projection_nonproof", func(t *testing.T) {
		f := newGrFake(t)
		o, d := f.options()
		o.Image = grStr(grImage)
		got, err := run(o, d)
		ngdEmitResult(t, got, err, "GUIDED_INVESTIGATION_NOT_PROOF",
			"Guided investigate reached localization but did not establish a portable proof package. Fix the environment or witness, then resume with a new reviewed draft rather than altering the frozen record.",
			// src/guided-investigate.ts:455-458: continuation.next, then the resume
			// command from the draft id, resolved repository, retained digest, image.
			[]string{"a", "b", "fl investigate --resume " + grDraftID + " --repo " + grRepo + " --expect-digest " + grDigest + " --image " + grImage},
			map[string]jsjson.Value{"doctor": f.doctor, "draft": f.draft, "continuation": f.cont})
	})
	t.Run("projection_proof", func(t *testing.T) {
		f := newGrFake(t)
		f.cont = grParse(t, `{"status":"PROOF_BUNDLE_READY","next":["fl serve --bundle x --expect-root y","keep"]}`)
		o, d := f.options()
		o.Image = grStr(grImage)
		got, err := run(o, d)
		ngdEmitResult(t, got, err, "GUIDED_PROOF_BUNDLE_READY",
			"Guided investigate completed intake → human freeze → runtime selection → localization → proof export as one resumable command. FaultLine did not auto-approve or auto-freeze.",
			// Proof: continuation.next is passed through unchanged.
			[]string{"fl serve --bundle x --expect-root y", "keep"},
			map[string]jsjson.Value{"doctor": f.doctor, "draft": f.draft, "continuation": f.cont})
	})
	t.Run("freeze_cancelled_before_poll", func(t *testing.T) {
		log := &taskFreezeLog{}
		srv := &taskFakeServer{log: log}
		deps := taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid()}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w", Signal: ctx}, deps)
		ngdEmit(t, err, ngdCancelled)
	})
	t.Run("freeze_sleep_already_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ngdEmit(t, freezeSleep(ctx, time.Hour), ngdCancelled)
	})
	t.Run("freeze_cancelled_during_sleep", func(t *testing.T) {
		// Deterministic: Err() stays nil until Done() is first read, so the
		// precheck passes and the select must take the ctx.Done() branch.
		ctx := &ngdDoneOnReadContext{Context: context.Background(), done: make(chan struct{})}
		ngdEmit(t, freezeSleep(ctx, time.Hour), ngdCancelled)
		if !ctx.read.Load() {
			t.Fatal("freezeSleep never reached its select on ctx.Done()")
		}
	})
}
