package cli

import (
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/witness"
)

const (
	incidentStatusUsage   = "Usage: fl incident status <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--expect-digest <sha256:...>]"
	incidentContinueUsage = "Usage: fl incident continue <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--image <digest-pinned-image>] [--expect-digest <sha256:...>] [--ledger <ledger.json>] [--max-states <count>] [--output <managed-bundle-directory>]"
)

// incidentContext ports IncidentCommandContext (src/cli-app.ts:439).
type incidentContext struct {
	repository, draftStore, witnessStore string
	draft                                jsjson.Value
}

// loadIncidentCommandContext ports src/cli-app.ts:447: the durable incident
// draft is resolved first, and continuation never lets it swap its
// repository. The witness store defaults under the draft's repository, not
// the working directory (witnessStore(args) is not used).
func loadIncidentCommandContext(args []string, incidentID string) (incidentContext, error) {
	requested, hasRequested := option(args, "--repo")
	lookup := requested
	if !hasRequested {
		lookup = jsexc.Must(nodefs.Cwd())
	}
	lookupRepository := nodefs.Resolve(lookup)
	draftStore, ok := option(args, "--draft-store")
	if !ok {
		draftStore = witness.DefaultIncidentDraftStore(lookupRepository)
	}
	draftStore = nodefs.Resolve(draftStore)
	draft, err := incident.ReadDraft(draftStore, incidentID)
	if err != nil {
		return incidentContext{}, err
	}
	repository := nodefs.Resolve(draft.Get("repository").Str())
	if hasRequested && nodefs.Resolve(requested) != repository {
		return incidentContext{}, fail("Incident continuation refuses a --repo value that differs from the immutable incident draft repository.")
	}
	store, ok := option(args, "--store")
	if !ok {
		store = nodefs.Join(repository, ".faultline", "witnesses")
	}
	return incidentContext{repository: repository, draftStore: draftStore, witnessStore: nodefs.Resolve(store), draft: draft}, nil
}

// reviewRequiredError is /missing|not been frozen/i (src/cli-app.ts:475).
var reviewRequiredError = jsre.MustCompile("missing|not been frozen", "i")

func nullableJSONString(s *string) jsjson.Value {
	if s == nil {
		return jsjson.MakeNull()
	}
	return jsjson.MakeString(*s)
}

// incidentStatusCommand ports incidentStatusCommand (src/cli-app.ts:464).
// It reads immutable records only and writes nothing.
func incidentStatusCommand(e *env, args []string) error {
	if len(args) < 2 || args[1] == "" {
		return fail("%s", incidentStatusUsage)
	}
	incidentID := args[1]
	context, err := loadIncidentCommandContext(args, incidentID)
	if err != nil {
		return err
	}
	expected, provided := option(args, "--expect-digest")
	verification := witness.VerifyFrozen(context.witnessStore, incidentID, expected, provided)
	var state string
	var bindingError *string
	if !verification.Valid {
		state = "INVALID_FROZEN_WITNESS"
		for _, message := range verification.Errors {
			if reviewRequiredError.Test(message) {
				state = "REVIEW_REQUIRED"
				break
			}
		}
	} else {
		thrown := jsexc.Try(func() {
			frozen := jsexc.Must(witness.ReadFrozen(context.witnessStore, incidentID))
			if err := incident.AssertFrozenWitnessBinding(context.draft, frozen); err != nil {
				jsexc.Throw(err)
			}
		})
		if thrown != nil {
			message := jsexc.Message(thrown)
			bindingError = &message
			state = "INVALID_FROZEN_WITNESS"
		} else if verification.ExternalDigestStatus == "MATCH" {
			state = "READY_TO_INVESTIGATE"
		} else {
			state = "RETAIN_DIGEST_REQUIRED"
		}
	}
	draft := context.draft
	runtime := draft.Get("runtime")
	if runtime.Kind() == jsjson.Undefined {
		runtime = jsjson.MakeNull()
	}
	frozenWitness := obj("valid", jsjson.MakeBool(verification.Valid),
		"frozenDigest", nullableJSONString(verification.FrozenDigest),
		"externalDigestStatus", verification.ExternalDigestStatus,
		"approval", verification.ApprovalValue(),
		"errors", verification.ErrorsValue())
	if bindingError != nil {
		frozenWitness.Obj().Set("bindingError", jsjson.MakeString(*bindingError))
	}
	var next jsjson.Value
	switch state {
	case "REVIEW_REQUIRED":
		next = stringList(jsexc.Concat("fl witness review ", incidentID, " --store ", context.witnessStore, " --draft-store ", context.draftStore))
	case "RETAIN_DIGEST_REQUIRED":
		next = stringList("Retain the frozen digest shown by the human review/freeze flow outside the witness store.",
			jsexc.Concat("fl incident status ", incidentID, " --repo ", context.repository, " --expect-digest <retained-frozen-digest>"))
	case "READY_TO_INVESTIGATE":
		hint := ""
		if draft.Get("runtime").Kind() == jsjson.Undefined {
			hint = " --image <digest-pinned-image>"
		}
		next = stringList(jsexc.Concat("fl incident continue ", incidentID, " --repo ", context.repository, " --expect-digest <retained-frozen-digest>", hint))
	default:
		next = stringList("Inspect the immutable draft and witness records; FaultLine will not run a mismatched or invalid witness.")
	}
	e.printJSON(obj("status", state,
		"incident", obj("id", draft.Get("draftId"), "draftDigest", draft.Get("draftDigest"), "range", draft.Get("range"),
			"runtime", runtime, "witnessStore", context.witnessStore, "draftStore", context.draftStore),
		"frozenWitness", frozenWitness,
		"next", next,
		"limitations", stringList(
			"Status reads immutable local records and never executes the stored command, approves a witness, freezes a witness, pulls an image, or runs Docker.",
			"A supplied --expect-digest checks a separately retained frozen-witness digest; without it, status reports local self-consistency only.")))
	if state == "INVALID_FROZEN_WITNESS" {
		e.exitCode = 1
	} else {
		e.exitCode = 0
	}
	return nil
}

// incidentContinueCommand ports continueIncidentCommand
// (src/cli-app.ts:522). Unknown flags (--unsafe-local) are ignored, as in
// TS. The CLI passes no injected runner (cli-app.ts:549).
func incidentContinueCommand(e *env, args []string) error {
	if len(args) < 2 || args[1] == "" {
		return fail("%s", incidentContinueUsage)
	}
	incidentID := args[1]
	context, err := loadIncidentCommandContext(args, incidentID)
	if err != nil {
		return err
	}
	inherited := witness.ResolveInheritedSessionFacts(sessionFlagOverrides(args, context.draftStore, incidentID))
	if inherited.ExpectDigest == nil {
		return fail("Missing required option: --expect-digest (no local session binding for %s). Freeze a witness first or pass --expect-digest explicitly.", incidentID)
	}
	expectedFrozenDigest := *inherited.ExpectDigest
	// resolveConfiguredImage(repository, option("--image")) ?? draft.runtime?.image
	var image *string
	if v, ok := option(args, "--image"); ok {
		image = &v
	} else {
		configured, ok, err := gitinv.ResolveConfiguredImage(context.repository)
		if err != nil {
			return err
		}
		if ok {
			image = &configured
		} else if runtime := context.draft.Get("runtime"); runtime.Kind() != jsjson.Undefined {
			draftImage := runtime.Get("image").Str()
			image = &draftImage
		}
	}
	ledgerFile := inherited.LedgerPath
	var maxStates *float64
	if v, ok := option(args, "--max-states"); ok {
		n := jsjson.ToNumber(v)
		maxStates = &n
	}
	var output *string
	if v, ok := option(args, "--output"); ok {
		output = &v
	}
	var resolvedLedger *string
	if ledgerFile != nil {
		resolved := nodefs.Resolve(*ledgerFile)
		resolvedLedger = &resolved
	}
	if _, err := witness.WriteSessionBinding(context.draftStore, witness.BindingInput{IncidentID: incidentID,
		ExpectDigest: &expectedFrozenDigest, LedgerPath: resolvedLedger}); err != nil {
		return err
	}
	result, err := incident.Continue(incident.ContinueOptions{
		Repository:           context.repository,
		Store:                context.witnessStore,
		Draft:                context.draft,
		ExpectedFrozenDigest: expectedFrozenDigest,
		Image:                image,
		LedgerFile:           ledgerFile,
		Output:               output,
		MaxStates:            maxStates,
		Runner:               sandbox.NativeRunner(),
		Injected:             false,
	})
	if err != nil {
		return err
	}
	if result.Get("status").Str() == "INVESTIGATION_NOT_PROOF" {
		e.printJSON(result)
		e.exitCode = 1
		return nil
	}
	e.printJSON(obj("status", result.Get("status"), "incident", result.Get("incident"),
		"proofBundle", result.Get("proofBundle"), "next", result.Get("next"), "limitations", result.Get("limitations")))
	e.exitCode = 0
	return nil
}
