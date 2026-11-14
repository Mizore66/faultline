package guided

import (
	"context"
	"errors"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/doctor"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/runtimeimage"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/witness"
)

// Options is guidedInvestigateFromCiLog's options (src/guided-investigate.ts:301).
// A nil pointer is an absent option; a pointer to "" is a supplied empty
// string. Signal is consumed only by the freeze waiter.
type Options struct {
	Repository                                         string
	CILogPath, ResumeID, Command, From, To, IncidentID *string
	Runtime, Image, ExpectDigest, LedgerFile, Output   *string
	WitnessStore, DraftStore                           *string
	MaxStates, ReviewPort                              *float64
	Runner                                             sandbox.Runner
	WaitForFreeze                                      func(FreezeOptions) (FreezeResult, error)
	OnPhase                                            func(string, *string)
	OnReviewReady                                      func(string)
	Signal                                             context.Context
}

// runDeps are the seams for deterministic dependency and phase-order tests;
// the defaults call the real modules.
type runDeps struct {
	doctor         func(repository string) jsjson.Value
	intake         func(IntakeOptions) (IntakeResult, error)
	readDraft      func(store, id string) (jsjson.Value, string, error)
	verify         func(store, id, expected string, provided bool) gitproof.WitnessVerification
	waitForFreeze  func(FreezeOptions) (FreezeResult, error)
	resolveRuntime func(alias string) (jsjson.Value, error)
	cont           func(incident.ContinueOptions) (jsjson.Value, error)
}

func defaultRunDeps() runDeps {
	return runDeps{
		doctor:        func(repository string) jsjson.Value { return doctor.Run(repository, doctor.NodeRunner()) },
		intake:        CreateIntake,
		readDraft:     incident.ReadDraftPath,
		verify:        witness.VerifyFrozen,
		waitForFreeze: WaitForFreeze,
		resolveRuntime: func(alias string) (jsjson.Value, error) {
			return runtimeimage.ResolveCuratedRuntime(alias, runtimeimage.NodeRunner())
		},
		cont: incident.Continue,
	}
}

var missingOrUnfrozen = jsre.MustCompile("missing|not been frozen", "i")

// Run is guidedInvestigateFromCiLog. On error the result is the zero Value.
func Run(o Options) (jsjson.Value, error) {
	return run(o, withOptionOverrides(o, defaultRunDeps()))
}

// withOptionOverrides applies the only public dependency override: the
// freeze waiter.
func withOptionOverrides(o Options, d runDeps) runDeps {
	if o.WaitForFreeze != nil {
		d.waitForFreeze = o.WaitForFreeze
	}
	return d
}

func run(o Options, d runDeps) (result jsjson.Value, err error) {
	if thrown := jsexc.Try(func() { result, err = runGuided(o, d) }); thrown != nil {
		return jsjson.Value{}, thrown
	}
	if err != nil {
		return jsjson.Value{}, err
	}
	return result, nil
}

func strv(s string) jsjson.Value { return jsjson.MakeString(s) }

func strArray(items ...string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, s := range items {
		values[i] = strv(s)
	}
	return jsjson.MakeArray(values)
}

func runGuided(o Options, d runDeps) (jsjson.Value, error) {
	repository := nodefs.Resolve(o.Repository)
	draftStore := witness.DefaultIncidentDraftStore(repository)
	if o.DraftStore != nil {
		draftStore = nodefs.Resolve(*o.DraftStore)
	}
	witnessStore := nodefs.Join(repository, ".faultline", "witnesses")
	if o.WitnessStore != nil {
		witnessStore = nodefs.Resolve(*o.WitnessStore)
	}
	phase := func(name string, detail *string) {
		if o.OnPhase != nil {
			o.OnPhase(name, detail)
		}
	}
	detail := func(s string) *string { return &s }

	phase("PREFLIGHT", nil)
	report := d.doctor(repository)
	proofReady := doctor.ProofReadyStatus(report) == "READY"
	if doctor.CliExitCode(report) != 0 {
		return jsjson.MustObject(
			"status", strv("PREFLIGHT_BLOCKED"),
			"doctor", report,
			"proofReady", jsjson.MakeBool(proofReady),
			"draft", jsjson.MakeNull(),
			"draftPath", jsjson.MakeNull(),
			"reviewUrl", jsjson.MakeNull(),
			"retainedFrozenDigest", jsjson.MakeNull(),
			"continuation", jsjson.MakeNull(),
			"nextCommands", strArray("Install Node.js 22+", "fl doctor --repo ."),
			"note", strv("Local CLI preflight failed; fix Node before continuing the guided investigate flow.")), nil
	}

	var draft jsjson.Value
	var draftPath string
	if o.ResumeID != nil {
		phase("INTAKE", detail("resume "+*o.ResumeID))
		stored, path, err := d.readDraft(draftStore, *o.ResumeID)
		if err != nil {
			return jsjson.Value{}, err
		}
		if nodefs.Resolve(stored.Get("repository").Str()) != repository {
			return jsjson.Value{}, errors.New("Guided --resume refuses a --repo value that differs from the immutable incident draft repository.")
		}
		draft, draftPath = stored, path
	} else {
		if o.CILogPath == nil {
			return jsjson.Value{}, errors.New("Guided investigate requires --ci-log <file> or --resume <incident-id>.")
		}
		phase("INTAKE", detail("ci-log"))
		created, err := d.intake(IntakeOptions{
			Repository: repository, CILogPath: *o.CILogPath, WitnessStore: witnessStore, DraftStore: draftStore,
			Command: o.Command, From: o.From, To: o.To, IncidentID: o.IncidentID, RuntimeAlias: o.Runtime,
		})
		if err != nil {
			return jsjson.Value{}, err
		}
		draft, draftPath = created.Draft, created.DraftPath
	}
	draftID := draft.Get("draftId").Str()

	phase("WITNESS_REVIEW", nil)
	expectedProvided := o.ExpectDigest != nil
	expected := ""
	if expectedProvided {
		expected = *o.ExpectDigest
	}
	prior := d.verify(witnessStore, draftID, expected, expectedProvided)
	missing := false
	for _, e := range prior.Errors {
		if missingOrUnfrozen.Test(e) {
			missing = true
			break
		}
	}
	var freeze FreezeResult
	switch {
	case prior.Valid && prior.FrozenDigest != nil:
		digest := *prior.FrozenDigest
		if expectedProvided {
			digest = expected
		}
		freeze = FreezeResult{FrozenDigest: digest, AlreadyFrozen: true}
	case missing:
		var err error
		freeze, err = d.waitForFreeze(FreezeOptions{
			ProposalID: draftID, WitnessStore: witnessStore, DraftStore: draftStore,
			Port: o.ReviewPort, OnReviewReady: o.OnReviewReady, Signal: o.Signal,
		})
		if err != nil {
			return jsjson.Value{}, err
		}
	default:
		return jsjson.Value{}, errors.New(jsexc.Concat("Guided investigate cannot continue with an invalid frozen witness: ", jsexc.Join(prior.Errors, "; ")))
	}

	retained := freeze.FrozenDigest
	if expectedProvided && expected != retained {
		return jsjson.Value{}, errors.New("--expect-digest does not match the frozen witness observed by the guided flow.")
	}
	check := d.verify(witnessStore, draftID, retained, true)
	if !check.Valid || check.ExternalDigestStatus != "MATCH" {
		reason := jsexc.Join(check.Errors, "; ")
		if reason == "" {
			reason = check.ExternalDigestStatus
		}
		return jsjson.Value{}, errors.New(jsexc.Concat("Guided investigate failed to bind a retained freeze digest: ", reason))
	}

	phase("RUNTIME_SELECTION", nil)
	var image string
	switch runtime := draft.Get("runtime"); {
	case runtime.Kind() != jsjson.Undefined:
		if o.Image != nil && *o.Image != runtime.Get("image").Str() {
			return jsjson.Value{}, errors.New("--image must match the digest-pinned runtime recorded in the immutable incident draft.")
		}
		image = runtime.Get("image").Str()
	case o.Image != nil:
		image = *o.Image
	case o.Runtime != nil:
		resolved, err := d.resolveRuntime(*o.Runtime)
		if err != nil {
			return jsjson.Value{}, err
		}
		image = resolved.Get("image").Str()
	default:
		return jsjson.Value{}, errors.New("Guided investigate needs a digest-pinned runtime before localization. Pass --runtime <node|python|go> after pulling the curated tag, or --image <digest-pinned-image>.")
	}
	if !proofReady && o.Runner == nil {
		return jsjson.Value{}, errors.New("Guided investigate requires a READY Docker investigation preflight. Run `fl doctor --repo .` and start Docker before continuing.")
	}

	phase("LOCALIZATION", nil)
	continuation, err := d.cont(incident.ContinueOptions{
		Repository: repository, Store: witnessStore, Draft: draft, ExpectedFrozenDigest: retained,
		Image: &image, LedgerFile: o.LedgerFile, Output: o.Output, MaxStates: o.MaxStates,
		Runner: o.Runner, Injected: o.Runner != nil,
	})
	if err != nil {
		return jsjson.Value{}, err
	}

	proof := continuation.Get("status").Str() == "PROOF_BUNDLE_READY"
	if proof {
		phase("PROOF_EXPORT", nil)
	}
	nextCommands := continuation.Get("next")
	if !proof {
		items := append([]jsjson.Value{}, nextCommands.Items()...)
		items = append(items, strv(jsexc.Concat("fl investigate --resume ", draftID, " --repo ", repository,
			" --expect-digest ", retained, " --image ", image)))
		nextCommands = jsjson.MakeArray(items)
	}
	status, note := "GUIDED_INVESTIGATION_NOT_PROOF", "Guided investigate reached localization but did not establish a portable proof package. Fix the environment or witness, then resume with a new reviewed draft rather than altering the frozen record."
	if proof {
		status, note = "GUIDED_PROOF_BUNDLE_READY", "Guided investigate completed intake → human freeze → runtime selection → localization → proof export as one resumable command. FaultLine did not auto-approve or auto-freeze."
	}
	reviewURL := jsjson.MakeNull()
	if freeze.ReviewURL != nil {
		reviewURL = strv(*freeze.ReviewURL)
	}
	return jsjson.MustObject(
		"status", strv(status),
		"doctor", report,
		"proofReady", jsjson.MakeBool(proofReady),
		"draft", draft,
		"draftPath", strv(draftPath),
		"reviewUrl", reviewURL,
		"retainedFrozenDigest", strv(retained),
		"continuation", continuation,
		"nextCommands", nextCommands,
		"note", strv(note)), nil
}
