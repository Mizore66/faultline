package guided

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/witness"
)

const (
	grDigest  = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	grImage   = "node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	grOther   = "node@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	grRepo    = "/work/repo"
	grDraftID = "guided-1"
)

func grParse(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func grStr(s string) *string { return &s }

func grDoctor(t *testing.T, node, docker string) jsjson.Value {
	return grParse(t, `{"diagnostics":[{"id":"node","status":"`+node+`"}],"dockerInvestigationPreflight":"`+docker+`"}`)
}

type grFake struct {
	t         *testing.T
	calls     []string
	phases    []string
	doctor    jsjson.Value
	draft     jsjson.Value
	draftPath string
	// verify results are consumed in order; the last repeats.
	verifies   []gitproof.WitnessVerification
	verifyArgs [][3]string
	wait       FreezeResult
	waitErr    error
	waitOpts   []FreezeOptions
	rtImage    string
	rtErr      error
	cont       jsjson.Value
	contErr    error
	contOpts   []incident.ContinueOptions
	intakeOpts []IntakeOptions
	intakeErr  error
	readErr    error
}

func grFrozenOK() gitproof.WitnessVerification {
	d := grDigest
	return gitproof.WitnessVerification{Valid: true, FrozenDigest: &d, ExternalDigestStatus: "MATCH"}
}

func grUnfrozen() gitproof.WitnessVerification {
	return gitproof.WitnessVerification{Errors: []string{"Witness proposal is missing: x"}, ExternalDigestStatus: "NOT_PROVIDED"}
}

func newGrFake(t *testing.T) *grFake {
	return &grFake{t: t, doctor: grDoctor(t, "READY", "READY"),
		draft:     grParse(t, `{"draftId":"`+grDraftID+`","repository":"`+grRepo+`","note":"d"}`),
		draftPath: "/work/repo/.faultline/incidents/drafts/guided-1.json",
		verifies:  []gitproof.WitnessVerification{grFrozenOK()},
		wait:      FreezeResult{FrozenDigest: grDigest},
		rtImage:   grImage,
		cont:      grParse(t, `{"status":"INVESTIGATION_NOT_PROOF","next":["a","b"]}`)}
}

func (f *grFake) options() (Options, runDeps) {
	o := Options{Repository: grRepo, CILogPath: grStr("ci.log"),
		OnPhase: func(p string, d *string) {
			if d != nil {
				p += " (" + *d + ")"
			}
			f.phases = append(f.phases, p)
			f.calls = append(f.calls, "phase:"+p)
		}}
	d := runDeps{
		doctor: func(string) jsjson.Value { f.calls = append(f.calls, "doctor"); return f.doctor },
		intake: func(io IntakeOptions) (IntakeResult, error) {
			f.calls = append(f.calls, "intake")
			f.intakeOpts = append(f.intakeOpts, io)
			if f.intakeErr != nil {
				return IntakeResult{Draft: f.draft, DraftPath: "ignored"}, f.intakeErr
			}
			return IntakeResult{Draft: f.draft, DraftPath: f.draftPath}, nil
		},
		readDraft: func(store, id string) (jsjson.Value, string, error) {
			f.calls = append(f.calls, "readDraft:"+store+":"+id)
			return f.draft, f.draftPath, f.readErr
		},
		verify: func(store, id, expected string, provided bool) gitproof.WitnessVerification {
			f.calls = append(f.calls, "verify")
			prov := "absent"
			if provided {
				prov = "provided"
			}
			f.verifyArgs = append(f.verifyArgs, [3]string{store + "|" + id, expected, prov})
			i := len(f.verifyArgs) - 1
			if i >= len(f.verifies) {
				i = len(f.verifies) - 1
			}
			return f.verifies[i]
		},
		waitForFreeze: func(fo FreezeOptions) (FreezeResult, error) {
			f.calls = append(f.calls, "wait")
			f.waitOpts = append(f.waitOpts, fo)
			return f.wait, f.waitErr
		},
		resolveRuntime: func(alias string) (jsjson.Value, error) {
			f.calls = append(f.calls, "runtime:"+alias)
			return jsjson.MustObject("image", jsjson.MakeString(f.rtImage)), f.rtErr
		},
		cont: func(co incident.ContinueOptions) (jsjson.Value, error) {
			f.calls = append(f.calls, "continue")
			f.contOpts = append(f.contOpts, co)
			return f.cont, f.contErr
		},
	}
	return o, d
}

func TestGuidedRunBlockedPreflightOnlyPhase(t *testing.T) {
	f := newGrFake(t)
	f.doctor = grDoctor(t, "BLOCKED", "NOT_READY")
	o, d := f.options()
	got, err := run(o, d)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PREFLIGHT"}; !reflect.DeepEqual(f.phases, want) {
		t.Fatalf("phases=%q", f.phases)
	}
	if want := []string{"phase:PREFLIGHT", "doctor"}; !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls=%q", f.calls)
	}
	want := `{
  "status": "PREFLIGHT_BLOCKED",
  "doctor": {
    "diagnostics": [
      {
        "id": "node",
        "status": "BLOCKED"
      }
    ],
    "dockerInvestigationPreflight": "NOT_READY"
  },
  "proofReady": false,
  "draft": null,
  "draftPath": null,
  "reviewUrl": null,
  "retainedFrozenDigest": null,
  "continuation": null,
  "nextCommands": [
    "Install Node.js 22+",
    "fl doctor --repo ."
  ],
  "note": "Local CLI preflight failed; fix Node before continuing the guided investigate flow."
}`
	if s := jsjson.StringifyIndent(got, "  "); s != want {
		t.Fatalf("got\n%s", s)
	}
}

func TestGuidedRunFreshNonproofExactOutputAndOrder(t *testing.T) {
	f := newGrFake(t)
	f.verifies = []gitproof.WitnessVerification{grUnfrozen(), grFrozenOK()}
	f.wait = FreezeResult{FrozenDigest: grDigest, ReviewURL: grStr("http://127.0.0.1:9/")}
	o, d := f.options()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := 0.0
	var ready func(string) = func(string) {}
	o.Signal, o.ReviewPort, o.OnReviewReady = ctx, &port, ready
	o.Runner = func(sandbox.Plan) (sandbox.RunResult, error) { return sandbox.RunResult{}, nil }
	o.Image = grStr(grImage)
	o.LedgerFile, o.Output = grStr(""), grStr("")
	max := 4.0
	o.MaxStates = &max
	got, err := run(o, d)
	if err != nil {
		t.Fatal(err)
	}
	wantPhases := []string{"PREFLIGHT", "INTAKE (ci-log)", "WITNESS_REVIEW", "RUNTIME_SELECTION", "LOCALIZATION"}
	if !reflect.DeepEqual(f.phases, wantPhases) {
		t.Fatalf("phases=%q", f.phases)
	}
	wantCalls := []string{"phase:PREFLIGHT", "doctor", "phase:INTAKE (ci-log)", "intake", "phase:WITNESS_REVIEW", "verify", "wait",
		"verify", "phase:RUNTIME_SELECTION", "phase:LOCALIZATION", "continue"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Fatalf("calls=%q", f.calls)
	}
	// Repository and default stores are resolved before intake.
	io := f.intakeOpts[0]
	if io.Repository != grRepo || io.CILogPath != "ci.log" ||
		io.WitnessStore != nodefs.Join(grRepo, ".faultline", "witnesses") || io.DraftStore != nodefs.Join(grRepo, ".faultline", "incidents") {
		t.Fatalf("intake %+v", io)
	}
	w := f.waitOpts[0]
	if w.ProposalID != grDraftID || w.Signal != ctx || w.Port != &port || w.WitnessStore != io.WitnessStore || w.DraftStore != io.DraftStore ||
		reflect.ValueOf(w.OnReviewReady).Pointer() != reflect.ValueOf(ready).Pointer() {
		t.Fatalf("wait %+v", w)
	}
	if !reflect.DeepEqual(f.verifyArgs[0], [3]string{io.WitnessStore + "|" + grDraftID, "", "absent"}) ||
		!reflect.DeepEqual(f.verifyArgs[1], [3]string{io.WitnessStore + "|" + grDraftID, grDigest, "provided"}) {
		t.Fatalf("verify args %v", f.verifyArgs)
	}
	c := f.contOpts[0]
	if c.Repository != grRepo || c.Store != io.WitnessStore || c.ExpectedFrozenDigest != grDigest || *c.Image != grImage ||
		c.LedgerFile != o.LedgerFile || *c.LedgerFile != "" || c.Output != o.Output || *c.Output != "" || c.MaxStates != &max ||
		c.Runner == nil || !c.Injected || c.Draft.Get("draftId").Str() != grDraftID {
		t.Fatalf("continue %+v", c)
	}
	want := `{
  "status": "GUIDED_INVESTIGATION_NOT_PROOF",
  "doctor": {
    "diagnostics": [
      {
        "id": "node",
        "status": "READY"
      }
    ],
    "dockerInvestigationPreflight": "READY"
  },
  "proofReady": true,
  "draft": {
    "draftId": "guided-1",
    "repository": "/work/repo",
    "note": "d"
  },
  "draftPath": "/work/repo/.faultline/incidents/drafts/guided-1.json",
  "reviewUrl": "http://127.0.0.1:9/",
  "retainedFrozenDigest": "` + grDigest + `",
  "continuation": {
    "status": "INVESTIGATION_NOT_PROOF",
    "next": [
      "a",
      "b"
    ]
  },
  "nextCommands": [
    "a",
    "b",
    "fl investigate --resume guided-1 --repo /work/repo --expect-digest ` + grDigest + ` --image ` + grImage + `"
  ],
  "note": "Guided investigate reached localization but did not establish a portable proof package. Fix the environment or witness, then resume with a new reviewed draft rather than altering the frozen record."
}`
	if s := jsjson.StringifyIndent(got, "  "); s != want {
		t.Fatalf("got\n%s", s)
	}
	if compact := jsjson.Stringify(got.Get("nextCommands")); !strings.HasPrefix(compact, `["a","b","fl investigate`) {
		t.Fatal(compact)
	}
}

func TestGuidedRunProofExportAfterVerifiedContinuationOnly(t *testing.T) {
	f := newGrFake(t)
	f.cont = grParse(t, `{"status":"PROOF_BUNDLE_READY","next":["fl serve --bundle x --expect-root y","keep"]}`)
	o, d := f.options()
	o.Image = grStr(grImage)
	got, err := run(o, d)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PREFLIGHT", "INTAKE (ci-log)", "WITNESS_REVIEW", "RUNTIME_SELECTION", "LOCALIZATION", "PROOF_EXPORT"}
	if !reflect.DeepEqual(f.phases, want) {
		t.Fatalf("phases=%q", f.phases)
	}
	if f.calls[len(f.calls)-2] != "continue" || f.calls[len(f.calls)-1] != "phase:PROOF_EXPORT" {
		t.Fatalf("calls=%q", f.calls)
	}
	if got.Get("status").Str() != "GUIDED_PROOF_BUNDLE_READY" ||
		jsjson.Stringify(got.Get("nextCommands")) != `["fl serve --bundle x --expect-root y","keep"]` ||
		got.Get("note").Str() != "Guided investigate completed intake → human freeze → runtime selection → localization → proof export as one resumable command. FaultLine did not auto-approve or auto-freeze." || got.Get("reviewUrl").Kind() != jsjson.Null {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
	if keys := strings.Join(got.Obj().Keys(), ","); keys != "status,doctor,proofReady,draft,draftPath,reviewUrl,retainedFrozenDigest,continuation,nextCommands,note" {
		t.Fatal(keys)
	}
	// A failed continuation emits no PROOF_EXPORT and keeps earlier phases.
	f2 := newGrFake(t)
	f2.contErr = errors.New("publication failed")
	o2, d2 := f2.options()
	o2.Image = grStr(grImage)
	res, err := run(o2, d2)
	if err == nil || err.Error() != "publication failed" || res.Kind() != jsjson.Undefined {
		t.Fatalf("%v %v", err, res)
	}
	if got := f2.phases[len(f2.phases)-1]; got != "LOCALIZATION" {
		t.Fatalf("phases=%q", f2.phases)
	}
}

func TestGuidedRunResume(t *testing.T) {
	f := newGrFake(t)
	o, d := f.options()
	o.CILogPath, o.ResumeID = nil, grStr(grDraftID)
	o.DraftStore, o.WitnessStore = grStr("/ds"), grStr("/ws")
	o.Image = grStr(grImage)
	o.ExpectDigest = grStr(grDigest)
	got, err := run(o, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.intakeOpts) != 0 {
		t.Fatal("resume must not run intake")
	}
	wantCalls := []string{"phase:PREFLIGHT", "doctor", "phase:INTAKE (resume guided-1)", "readDraft:/ds:guided-1", "phase:WITNESS_REVIEW",
		"verify", "verify", "phase:RUNTIME_SELECTION", "phase:LOCALIZATION", "continue"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Fatalf("calls=%q", f.calls)
	}
	if got.Get("draftPath").Str() != f.draftPath {
		t.Fatal("draft path")
	}
	if f.verifyArgs[0] != ([3]string{"/ws|guided-1", grDigest, "provided"}) {
		t.Fatalf("%v", f.verifyArgs)
	}
}

func TestGuidedRunResumeRepositoryMismatchBeforeWitness(t *testing.T) {
	f := newGrFake(t)
	f.draft = grParse(t, `{"draftId":"guided-1","repository":"/elsewhere"}`)
	o, d := f.options()
	o.CILogPath, o.ResumeID = nil, grStr(grDraftID)
	res, err := run(o, d)
	if err == nil || err.Error() != "Guided --resume refuses a --repo value that differs from the immutable incident draft repository." ||
		res.Kind() != jsjson.Undefined {
		t.Fatalf("%v", err)
	}
	if len(f.verifyArgs) != 0 || f.phases[len(f.phases)-1] != "INTAKE (resume guided-1)" {
		t.Fatalf("calls=%q", f.calls)
	}
	// An unreadable draft propagates the reader error.
	f = newGrFake(t)
	f.readErr = errors.New("no draft")
	o, d = f.options()
	o.CILogPath, o.ResumeID = nil, grStr(grDraftID)
	if _, err := run(o, d); err == nil || err.Error() != "no draft" {
		t.Fatal(err)
	}
}

func TestGuidedRunRequiresCILogOrResume(t *testing.T) {
	f := newGrFake(t)
	o, d := f.options()
	o.CILogPath = nil
	if _, err := run(o, d); err == nil || err.Error() != "Guided investigate requires --ci-log <file> or --resume <incident-id>." {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.phases, []string{"PREFLIGHT"}) {
		t.Fatalf("phases=%q", f.phases)
	}
}

func TestGuidedRunIntakeErrorIgnoresResult(t *testing.T) {
	f := newGrFake(t)
	f.intakeErr = errors.New("intake boom")
	o, d := f.options()
	res, err := run(o, d)
	if err == nil || err.Error() != "intake boom" || res.Kind() != jsjson.Undefined {
		t.Fatalf("%v", err)
	}
	if len(f.verifyArgs) != 0 {
		t.Fatal("verified after failed intake")
	}
}

func TestGuidedRunIntakeOptionsPreservePointers(t *testing.T) {
	f := newGrFake(t)
	o, d := f.options()
	o.Command, o.From, o.To, o.IncidentID, o.Runtime = grStr(""), grStr(""), grStr(""), grStr(""), grStr("")
	o.Image = grStr(grImage)
	if _, err := run(o, d); err != nil {
		t.Fatal(err)
	}
	io := f.intakeOpts[0]
	for name, p := range map[string]*string{"command": io.Command, "from": io.From, "to": io.To, "id": io.IncidentID, "runtime": io.RuntimeAlias} {
		if p == nil || *p != "" {
			t.Fatalf("%s lost empty pointer", name)
		}
	}
	f = newGrFake(t)
	o, d = f.options()
	o.Image = grStr(grImage)
	if _, err := run(o, d); err != nil {
		t.Fatal(err)
	}
	io = f.intakeOpts[0]
	if io.Command != nil || io.From != nil || io.To != nil || io.IncidentID != nil || io.RuntimeAlias != nil {
		t.Fatalf("absent became present: %+v", io)
	}
}

func TestGuidedRunAlreadyFrozenSkipsWaiterAndIgnoresCancelledSignal(t *testing.T) {
	f := newGrFake(t)
	o, d := f.options()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o.Signal, o.Image = ctx, grStr(grImage)
	got, err := run(o, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.waitOpts) != 0 || got.Get("reviewUrl").Kind() != jsjson.Null || got.Get("retainedFrozenDigest").Str() != grDigest {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
	// Supplied expected digest is retained verbatim even with different observed digest.
	f = newGrFake(t)
	other := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	f.verifies = []gitproof.WitnessVerification{grFrozenOK(), grFrozenOK()}
	o, d = f.options()
	o.ExpectDigest, o.Image = &other, grStr(grImage)
	if _, err := run(o, d); err != nil {
		t.Fatal(err)
	}
	if f.contOpts[0].ExpectedFrozenDigest != other || f.verifyArgs[1][1] != other {
		t.Fatalf("retained %q", f.contOpts[0].ExpectedFrozenDigest)
	}
}

func TestGuidedRunExpectDigestMismatchAfterWaitBeforeRuntime(t *testing.T) {
	f := newGrFake(t)
	f.verifies = []gitproof.WitnessVerification{grUnfrozen()}
	other := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	o, d := f.options()
	o.ExpectDigest = &other
	_, err := run(o, d)
	if err == nil || err.Error() != "--expect-digest does not match the frozen witness observed by the guided flow." {
		t.Fatal(err)
	}
	if len(f.verifyArgs) != 1 || f.phases[len(f.phases)-1] != "WITNESS_REVIEW" {
		t.Fatalf("calls=%q", f.calls)
	}
}

func TestGuidedRunWaiterBroadMissingClassification(t *testing.T) {
	for _, msg := range []string{"Witness approval is MISSING", "Frozen witness has Not Been Frozen yet", "x missing companion y"} {
		f := newGrFake(t)
		f.verifies = []gitproof.WitnessVerification{{Errors: []string{"other", msg}}, grFrozenOK()}
		o, d := f.options()
		o.Image = grStr(grImage)
		if _, err := run(o, d); err != nil || len(f.waitOpts) != 1 {
			t.Fatalf("%q: %v waits=%d", msg, err, len(f.waitOpts))
		}
	}
}

func TestGuidedRunInvalidChainWithoutMissingThrows(t *testing.T) {
	f := newGrFake(t)
	f.verifies = []gitproof.WitnessVerification{{Errors: []string{"digest tampered", "bad hash"}}}
	o, d := f.options()
	res, err := run(o, d)
	if err == nil || err.Error() != "Guided investigate cannot continue with an invalid frozen witness: digest tampered; bad hash" ||
		res.Kind() != jsjson.Undefined {
		t.Fatalf("%v", err)
	}
	if len(f.waitOpts) != 0 {
		t.Fatal("waited on corrupt chain")
	}
	// Valid without a frozen digest and without errors is also an invalid chain.
	f = newGrFake(t)
	f.verifies = []gitproof.WitnessVerification{{Valid: true}}
	o, d = f.options()
	if _, err := run(o, d); err == nil || err.Error() != "Guided investigate cannot continue with an invalid frozen witness: " {
		t.Fatal(err)
	}
}

func TestGuidedRunWaiterErrorPropagates(t *testing.T) {
	f := newGrFake(t)
	f.verifies = []gitproof.WitnessVerification{grUnfrozen()}
	wantErr := newFreezeCancelledError()
	f.waitErr = wantErr
	o, d := f.options()
	res, err := run(o, d)
	if err != wantErr || res.Kind() != jsjson.Undefined {
		t.Fatal(err)
	}
}

func TestGuidedRunRetainedBindingFailures(t *testing.T) {
	cases := []struct {
		v    gitproof.WitnessVerification
		want string
	}{
		{gitproof.WitnessVerification{Errors: []string{"e1", "e2"}, ExternalDigestStatus: "MATCH"}, "e1; e2"},
		{gitproof.WitnessVerification{Valid: true, ExternalDigestStatus: "MISMATCH"}, "MISMATCH"},
		{gitproof.WitnessVerification{Valid: true, ExternalDigestStatus: "NOT_PROVIDED"}, "NOT_PROVIDED"},
	}
	for _, c := range cases {
		f := newGrFake(t)
		f.verifies = []gitproof.WitnessVerification{grFrozenOK(), c.v}
		o, d := f.options()
		_, err := run(o, d)
		if err == nil || err.Error() != "Guided investigate failed to bind a retained freeze digest: "+c.want {
			t.Fatalf("%v", err)
		}
		if f.phases[len(f.phases)-1] != "WITNESS_REVIEW" {
			t.Fatalf("phases=%q", f.phases)
		}
	}
}

func TestGuidedRunImagePrecedence(t *testing.T) {
	const noRuntime = `{"draftId":"guided-1","repository":"/work/repo"}`
	withRuntime := `{"draftId":"guided-1","repository":"/work/repo","runtime":{"requested":"node","image":"` + grImage + `"}}`
	notReady := grDoctor(t, "READY", "NOT_READY")
	cases := []struct {
		name    string
		draft   string
		image   *string
		runtime *string
		doctor  jsjson.Value
		runner  bool
		want    string // image on success
		err     string
		calls   string // runtime call expected
	}{
		{"draft wins, no image", withRuntime, nil, nil, jsjson.Value{}, false, grImage, "", ""},
		{"draft matching image", withRuntime, grStr(grImage), nil, jsjson.Value{}, false, grImage, "", ""},
		{"draft mismatch beats docker", withRuntime, grStr(grOther), nil, notReady, false, "",
			"--image must match the digest-pinned runtime recorded in the immutable incident draft.", ""},
		{"empty image mismatches draft", withRuntime, grStr(""), nil, jsjson.Value{}, false, "",
			"--image must match the digest-pinned runtime recorded in the immutable incident draft.", ""},
		{"image used", noRuntime, grStr(grOther), grStr("go"), jsjson.Value{}, false, grOther, "", ""},
		{"empty image is present", noRuntime, grStr(""), nil, jsjson.Value{}, false, "", "", ""},
		{"alias resolved", noRuntime, nil, grStr("node"), jsjson.Value{}, false, grImage, "", "runtime:node"},
		{"missing beats docker", noRuntime, nil, nil, notReady, false, "",
			"Guided investigate needs a digest-pinned runtime before localization. Pass --runtime <node|python|go> after pulling the curated tag, or --image <digest-pinned-image>.", ""},
		{"docker after image", noRuntime, grStr(grImage), nil, notReady, false, "",
			"Guided investigate requires a READY Docker investigation preflight. Run `fl doctor --repo .` and start Docker before continuing.", ""},
		{"runner bypasses docker", noRuntime, grStr(grImage), nil, notReady, true, grImage, "", ""},
	}
	for _, c := range cases {
		f := newGrFake(t)
		f.draft = grParse(t, c.draft)
		if c.doctor.Kind() != jsjson.Undefined {
			f.doctor = c.doctor
		}
		o, d := f.options()
		o.Image, o.Runtime = c.image, c.runtime
		if c.runner {
			o.Runner = func(sandbox.Plan) (sandbox.RunResult, error) { return sandbox.RunResult{}, nil }
		}
		got, err := run(o, d)
		if c.err != "" {
			if err == nil || err.Error() != c.err || got.Kind() != jsjson.Undefined {
				t.Fatalf("%s: %v", c.name, err)
			}
			if len(f.contOpts) != 0 || f.phases[len(f.phases)-1] != "RUNTIME_SELECTION" {
				t.Fatalf("%s: phases=%q", c.name, f.phases)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if *f.contOpts[0].Image != c.want {
			t.Fatalf("%s: image %q", c.name, *f.contOpts[0].Image)
		}
		hasRuntime := false
		for _, call := range f.calls {
			hasRuntime = hasRuntime || strings.HasPrefix(call, "runtime:")
		}
		if hasRuntime != (c.calls != "") {
			t.Fatalf("%s: runtime calls %q", c.name, f.calls)
		}
		if c.runner != f.contOpts[0].Injected {
			t.Fatalf("%s: injected=%v", c.name, f.contOpts[0].Injected)
		}
	}
	// A runtime resolution failure propagates after RUNTIME_SELECTION.
	f := newGrFake(t)
	f.draft = grParse(t, noRuntime)
	f.rtErr = errors.New("inspect failed")
	o, d := f.options()
	o.Runtime = grStr("node")
	if _, err := run(o, d); err == nil || err.Error() != "inspect failed" || f.phases[len(f.phases)-1] != "RUNTIME_SELECTION" {
		t.Fatalf("%v %q", err, f.phases)
	}
}

func TestGuidedRunNativeNamedRunnerIsStillInjected(t *testing.T) {
	f := newGrFake(t)
	o, d := f.options()
	o.Image = grStr(grImage)
	o.Runner = sandbox.NativeRunner()
	if _, err := run(o, d); err != nil {
		t.Fatal(err)
	}
	if !f.contOpts[0].Injected || f.contOpts[0].Runner == nil {
		t.Fatal("supplied runner must be injected")
	}
	f = newGrFake(t)
	o, d = f.options()
	o.Image = grStr(grImage)
	if _, err := run(o, d); err != nil {
		t.Fatal(err)
	}
	if f.contOpts[0].Injected || f.contOpts[0].Runner != nil {
		t.Fatal("native default must not be injected")
	}
}

func TestGuidedRunPublicWaiterOverride(t *testing.T) {
	// Options.WaitForFreeze replaces only the waiter; other deps are kept.
	f := newGrFake(t)
	f.verifies = []gitproof.WitnessVerification{grUnfrozen(), grFrozenOK()}
	o, d := f.options()
	o.Image = grStr(grImage)
	override := 0
	o.WaitForFreeze = func(FreezeOptions) (FreezeResult, error) {
		override++
		return FreezeResult{FrozenDigest: grDigest}, nil
	}
	d = withOptionOverrides(o, d)
	if _, err := run(o, d); err != nil {
		t.Fatal(err)
	}
	if override != 1 || len(f.waitOpts) != 0 {
		t.Fatalf("override=%d default waits=%d", override, len(f.waitOpts))
	}
	if len(f.intakeOpts) != 1 || len(f.contOpts) != 1 {
		t.Fatal("other dependencies must stay injected")
	}
	// Without an override the base waiter is retained.
	f = newGrFake(t)
	f.verifies = []gitproof.WitnessVerification{grUnfrozen(), grFrozenOK()}
	o, d = f.options()
	o.Image = grStr(grImage)
	if _, err := run(o, withOptionOverrides(o, d)); err != nil || len(f.waitOpts) != 1 {
		t.Fatalf("%v waits=%d", err, len(f.waitOpts))
	}
}

// Real witness records and the real continuation, with an injected observation
// runner: the result is non-proof and never publishes a bundle.

func grGit(t *testing.T, dir string, args ...string) string { return taskIntakeGit(t, dir, args...) }

func grRealRepo(t *testing.T) (repo string, shas []string) {
	t.Helper()
	repo, _ = taskIntakeRepo(t, 0)
	for i, state := range []string{"good", "bad"} {
		if err := os.WriteFile(filepath.Join(repo, "state.txt"), []byte(state+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		grGit(t, repo, "add", "state.txt")
		grGit(t, repo, "commit", "-q", "-m", "c"+string(rune('0'+i)))
		shas = append(shas, grGit(t, repo, "rev-parse", "HEAD"))
	}
	return
}

func grInjectedRunner(code *int) sandbox.Runner {
	return func(p sandbox.Plan) (sandbox.RunResult, error) {
		state, _ := os.ReadFile(filepath.Join(p.Value.Get("sourceDirectory").Str(), "state.txt"))
		outcome, exit := "PREDICATE_PASS", 0
		if strings.TrimSpace(string(state)) == "bad" {
			outcome, exit = "PREDICATE_FAIL", 1
		}
		*code++
		return sandbox.RunResult{ExitCode: &exit,
			Stdout: `FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"` + outcome + `"}` + "\n"}, nil
	}
}

// grHumanFreeze plays the human: approve then freeze the proposal in the store.
func grHumanFreeze(t *testing.T, calls *int) func(FreezeOptions) (FreezeResult, error) {
	return func(fo FreezeOptions) (FreezeResult, error) {
		*calls++
		if _, err := witness.Approve(fo.WitnessStore, fo.ProposalID, jsjson.MustObject("approvedBy", jsjson.MakeString("reviewer@example.test"))); err != nil {
			t.Fatal(err)
		}
		if _, err := witness.Freeze(fo.WitnessStore, fo.ProposalID, witness.FreezeOptions{}); err != nil {
			t.Fatal(err)
		}
		v := witness.VerifyFrozen(fo.WitnessStore, fo.ProposalID, "", false)
		if !v.Valid || v.FrozenDigest == nil {
			t.Fatalf("not frozen: %+v", v)
		}
		url := "http://127.0.0.1:1/"
		return FreezeResult{FrozenDigest: *v.FrozenDigest, ReviewURL: &url}, nil
	}
}

func TestGuidedRunRealWitnessInjectedRunnerIsNonProofThenResume(t *testing.T) {
	repo, shas := grRealRepo(t)
	logPath := filepath.Join(t.TempDir(), "ci.log")
	if err := os.WriteFile(logPath, []byte("FAIL: state is bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := defaultRunDeps()
	d.doctor = func(string) jsjson.Value { return grDoctor(t, "READY", "NOT_READY") }
	var waits, runs int
	d.waitForFreeze = grHumanFreeze(t, &waits)
	var phases []string
	o := Options{Repository: repo, CILogPath: grStr(logPath), IncidentID: grStr("real-guided"), Image: grStr(grImage),
		Command: grStr("node -e \"process.exit(0)\""), From: grStr(shas[0]), To: grStr(shas[1]),
		Runner:  grInjectedRunner(&runs),
		OnPhase: func(p string, _ *string) { phases = append(phases, p) }}
	got, err := run(o, d)
	if err != nil {
		t.Fatal(err)
	}
	if waits != 1 || runs == 0 {
		t.Fatalf("waits=%d runs=%d", waits, runs)
	}
	if want := []string{"PREFLIGHT", "INTAKE", "WITNESS_REVIEW", "RUNTIME_SELECTION", "LOCALIZATION"}; !reflect.DeepEqual(phases, want) {
		t.Fatalf("phases=%q", phases)
	}
	cont := got.Get("continuation")
	if got.Get("status").Str() != "GUIDED_INVESTIGATION_NOT_PROOF" || got.Get("proofReady").Bool() ||
		cont.Get("status").Str() != "INVESTIGATION_NOT_PROOF" || cont.Get("investigation", "proof", "isProof").Bool() ||
		cont.Get("investigation", "proof", "executionTrust").Str() != "INJECTED_RUNNER" ||
		cont.Get("proofBundle").Kind() != jsjson.Null || cont.Get("incident", "frozenDigestExternalStatus").Str() != "MATCH" {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
	digest := got.Get("retainedFrozenDigest").Str()
	items := got.Get("nextCommands").Items()
	wantResume := "fl investigate --resume real-guided --repo " + nodefs.Resolve(repo) + " --expect-digest " + digest + " --image " + grImage
	if len(items) != 3 || items[2].Str() != wantResume || got.Get("reviewUrl").Str() != "http://127.0.0.1:1/" {
		t.Fatalf("%s", jsjson.Stringify(got.Get("nextCommands")))
	}
	if _, err := os.Stat(filepath.Join(repo, ".faultline", "git-proof-bundles")); !os.IsNotExist(err) {
		t.Fatalf("non-proof published: %v", err)
	}
	draftBefore, err := os.ReadFile(got.Get("draftPath").Str())
	if err != nil {
		t.Fatal(err)
	}

	// Resume: frozen chain bypasses the waiter; expected digest is retained.
	waits, runs, phases = 0, 0, nil
	r := Options{Repository: repo, ResumeID: grStr("real-guided"), ExpectDigest: &digest, Image: grStr(grImage), Runner: grInjectedRunner(&runs),
		OnPhase: func(p string, det *string) {
			if det != nil {
				p += " (" + *det + ")"
			}
			phases = append(phases, p)
		}}
	got2, err := run(r, d)
	if err != nil {
		t.Fatal(err)
	}
	if waits != 0 || runs == 0 || got2.Get("reviewUrl").Kind() != jsjson.Null || got2.Get("retainedFrozenDigest").Str() != digest ||
		phases[1] != "INTAKE (resume real-guided)" {
		t.Fatalf("waits=%d phases=%q %s", waits, phases, jsjson.Stringify(got2))
	}
	if after, _ := os.ReadFile(got.Get("draftPath").Str()); string(after) != string(draftBefore) {
		t.Fatal("stored draft mutated after freeze")
	}
	// The wrong external digest is refused before any runtime/continuation phase.
	wrong := "sha256:" + strings.Repeat("0", 64)
	phases = nil
	r.ExpectDigest = &wrong
	if res, err := run(r, d); err == nil || res.Kind() != jsjson.Undefined {
		t.Fatalf("wrong digest accepted: %v", err)
	} else if !strings.Contains(err.Error(), "Guided investigate") {
		t.Fatalf("%v", err)
	}
	for _, p := range phases {
		if p == "RUNTIME_SELECTION" || p == "LOCALIZATION" {
			t.Fatalf("phases=%q", phases)
		}
	}
}

func TestGuidedRunRealResumeRepositoryMismatch(t *testing.T) {
	repo, shas := grRealRepo(t)
	logPath := filepath.Join(t.TempDir(), "ci.log")
	if err := os.WriteFile(logPath, []byte("error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateIntake(IntakeOptions{Repository: repo, CILogPath: logPath, IncidentID: grStr("mm-1"),
		From: grStr(shas[0]), To: grStr(shas[1]),
		WitnessStore: filepath.Join(repo, ".faultline", "witnesses"), DraftStore: filepath.Join(repo, ".faultline", "incidents")}); err != nil {
		t.Fatal(err)
	}
	other, _ := grRealRepo(t)
	d := defaultRunDeps()
	d.doctor = func(string) jsjson.Value { return grDoctor(t, "READY", "READY") }
	d.waitForFreeze = func(FreezeOptions) (FreezeResult, error) { t.Fatal("waited"); return FreezeResult{}, nil }
	_, err := run(Options{Repository: other, ResumeID: grStr("mm-1"),
		DraftStore: grStr(filepath.Join(repo, ".faultline", "incidents"))}, d)
	if err == nil || err.Error() != "Guided --resume refuses a --repo value that differs from the immutable incident draft repository." {
		t.Fatal(err)
	}
}
