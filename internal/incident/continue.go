package incident

import (
	"errors"
	"strconv"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/witness"
)

// sameString is TS `a === b` on two members a schema has already made
// strings; an absent member equals only another absent member.
func sameString(a, b jsjson.Value) bool {
	return a.Kind() == b.Kind() && a.Str() == b.Str()
}

// AssertFrozenWitnessBinding ports assertIncidentFrozenWitnessBinding
// (src/incident-continue.ts:53): the frozen witness must be the exact
// proposal, command, and packet bound by intake.
func AssertFrozenWitnessBinding(draft, frozen jsjson.Value) (err error) {
	return jsexc.Try(func() { assertFrozenWitnessBinding(draft, frozen) })
}

func assertFrozenWitnessBinding(draft, frozen jsjson.Value) {
	if state := draft.Get("review", "witnessState"); state.Kind() != jsjson.String || state.Str() != "PROPOSED" {
		throw("Incident draft has no proposal binding and cannot continue to investigation.")
	}
	binding := draft.Get("review", "proposal")
	proposal := frozen.Get("proposal")
	var errs []string
	if !sameString(proposal.Get("proposalId"), draft.Get("draftId")) || !sameString(binding.Get("proposalId"), proposal.Get("proposalId")) {
		errs = append(errs, "proposal identifier")
	}
	if !sameString(binding.Get("proposalDigest"), proposal.Get("proposalDigest")) {
		errs = append(errs, "proposal digest")
	}
	if !sameString(binding.Get("incidentPacketDigest"), proposal.Get("incidentPacketDigest")) {
		errs = append(errs, "blinded incident-packet digest")
	}
	if !sameString(binding.Get("commandDigest"), proposal.Get("witness", "commandDigest")) ||
		!sameString(draft.Get("commandDigest"), proposal.Get("witness", "commandDigest")) {
		errs = append(errs, "exact command digest")
	}
	if !sameString(draft.Get("command"), proposal.Get("witness", "command")) {
		errs = append(errs, "exact command bytes")
	}
	if len(errs) > 0 {
		throw(jsexc.Concat("Frozen witness does not match the immutable incident draft binding: ", jsexc.Join(errs, ", "), "."))
	}
}

// ContinueOptions is runFrozenIncidentContinuation's options object
// (src/incident-continue.ts:79). A nil pointer is an absent (undefined)
// option; a pointer to "" is a supplied empty string, which TS treats as
// present (`--image ""` must still match the draft runtime, `--output ""`
// resolves to the working directory, `--ledger ""` reads it).
//
// ExpectedFrozenDigest is always supplied: both TS callers (`fl incident
// continue`, cli-app.ts:549, and guided investigate,
// guided-investigate.ts:437) pass a string.
//
// Runner is the sandbox runner; nil is sandbox.NativeRunner(), as in
// gitinv.InvestigateGitRange. Injected is TS's `runner !== undefined` (a
// test or controlled-host seam, guided-investigate.ts:443): gitinv then
// classifies the runs INJECTED_RUNNER and never certifies them as proof.
// The CLI passes no injected runner (cli-app.ts:549), so Injected is false
// there, whether Runner is nil or sandbox.NativeRunner().
type ContinueOptions struct {
	Repository, Store    string
	Draft                jsjson.Value
	ExpectedFrozenDigest string
	Image                *string
	LedgerFile           *string
	Output               *string
	MaxStates            *float64
	Runner               sandbox.Runner
	Injected             bool
}

// Continue ports runFrozenIncidentContinuation (src/incident-continue.ts:76),
// the shared freeze → runtime → localization → proof-export path. It
// returns the INVESTIGATION_NOT_PROOF result or the PROOF_BUNDLE_READY
// object (with investigation and limitations), which `fl incident
// continue` reshapes. An error is what the TS promise rejects with.
func Continue(o ContinueOptions) (result jsjson.Value, err error) {
	if thrown := jsexc.Try(func() { result, err = continueIncident(o) }); thrown != nil {
		return jsjson.Value{}, thrown
	}
	return result, err
}

func continueIncident(o ContinueOptions) (jsjson.Value, error) {
	draftID := o.Draft.Get("draftId").Str()
	witnessVerification := witness.VerifyFrozen(o.Store, draftID, o.ExpectedFrozenDigest, true)
	if !witnessVerification.Valid {
		return jsjson.Value{}, errors.New(jsexc.Concat("Incident cannot continue until its witness is human-approved and frozen intact: ",
			jsexc.Join(witnessVerification.Errors, "; ")))
	}
	frozenWitness, err := witness.ReadFrozen(o.Store, draftID)
	if err != nil {
		return jsjson.Value{}, err
	}
	if err := AssertFrozenWitnessBinding(o.Draft, frozenWitness); err != nil {
		return jsjson.Value{}, err
	}

	// incident-continue.ts:101-105 refuses an undefined expectedFrozenDigest
	// ("Proof-grade incident continuation requires --expect-digest ...").
	// Neither TS caller can omit it (see ContinueOptions), so the check is
	// not ported.
	runtime := o.Draft.Get("runtime")
	if runtime.Kind() != jsjson.Undefined && o.Image != nil && *o.Image != runtime.Get("image").Str() {
		return jsjson.Value{}, errors.New("--image must match the digest-pinned runtime recorded in the immutable incident draft.")
	}
	var image string
	switch {
	case o.Image != nil:
		image = *o.Image
	case runtime.Kind() != jsjson.Undefined:
		image = runtime.Get("image").Str()
	default:
		return jsjson.Value{}, errors.New("This incident has no selected runtime. Resolve a reviewed local runtime before intake, or supply --image <digest-pinned-image> for proof-grade replay.")
	}

	var lifecycleLedger *jsjson.Value
	if o.LedgerFile != nil {
		ledger, err := gitproof.ReadVerifiedCodexLifecycleLedger(nodefs.Resolve(*o.LedgerFile))
		if err != nil {
			return jsjson.Value{}, err
		}
		lifecycleLedger = &ledger
	}

	draftRange := o.Draft.Get("range")
	request := jsjson.NewObj()
	request.Set("repository", jsjson.MakeString(o.Repository))
	request.Set("range", jsjson.MustObject("ancestor", draftRange.Get("ancestor"), "descendant", draftRange.Get("descendant")))
	request.Set("frozenWitness", frozenWitness)
	request.Set("expectedFrozenDigest", jsjson.MakeString(o.ExpectedFrozenDigest))
	request.Set("sandbox", jsjson.MustObject("mode", str("DOCKER_ISOLATED"), "image", str(image)))
	if o.MaxStates != nil {
		request.Set("maxStates", jsjson.MakeNumber(*o.MaxStates))
	}
	investigation, err := gitinv.InvestigateGitRange(jsjson.MakeObject(request), o.Runner, o.Injected)
	if err != nil {
		return jsjson.Value{}, err
	}

	// runtime: draft.runtime ?? { requested: "explicit", image }; image is
	// always defined here, so the `null` arm of incident-continue.ts:137 is
	// unreachable.
	if runtime.Kind() == jsjson.Undefined {
		runtime = jsjson.MustObject("requested", str("explicit"), "image", str(image))
	}
	incident := jsjson.MustObject(
		"id", o.Draft.Get("draftId"),
		"draftDigest", o.Draft.Get("draftDigest"),
		"range", draftRange,
		"runtime", runtime,
		"frozenDigest", frozenWitness.Get("frozenDigest"),
		"frozenDigestExternalStatus", str(witnessVerification.ExternalDigestStatus))

	if !investigation.Get("proof", "isProof").Bool() {
		return jsjson.MustObject(
			"status", str("INVESTIGATION_NOT_PROOF"),
			"incident", incident,
			"investigation", investigation,
			"proofBundle", jsjson.MakeNull(),
			"next", stringArray(
				"Fix the recorded environment or witness condition, then create a new reviewed incident draft rather than altering this frozen witness.",
				"Only native Docker execution can publish a portable proof bundle.")), nil
	}

	proofRoot := nodefs.Resolve(o.Repository, ".faultline", "git-proof-bundles")
	var output string
	if o.Output != nil {
		output = *o.Output
	} else {
		descendant := "unknown"
		if commit := investigation.Get("resolvedRange", "descendant", "commit"); commit.Kind() == jsjson.String {
			descendant = commit.Str()[:min(12, len(commit.Str()))] // a hex commit id
		}
		output = nodefs.Join(proofRoot, jsexc.Concat("incident-", draftID, "-", descendant, "-", strconv.FormatInt(clock.NowMillis(), 10)))
	}
	output = nodefs.Resolve(output)
	written, err := gitproof.WriteGitInvestigationProofBundle(output, investigation, frozenWitness,
		gitproof.WriteOptions{ProofRoot: proofRoot, LifecycleLedger: lifecycleLedger})
	if err != nil {
		return jsjson.Value{}, err
	}
	bundleVerification := gitproof.Verify(written.Directory, written.RootDigest, true)
	if !bundleVerification.Valid {
		return jsjson.Value{}, errors.New(jsexc.Concat("Generated incident proof bundle failed verification: ",
			jsexc.Join(bundleVerification.Errors, "; ")))
	}

	return jsjson.MustObject(
		"status", str("PROOF_BUNDLE_READY"),
		"incident", incident,
		"investigation", investigation,
		"proofBundle", jsjson.MustObject(
			"directory", str(written.Directory),
			"rootDigest", str(written.RootDigest),
			"externalRootStatus", str(bundleVerification.ExternalRootStatus),
			"lifecycle", written.Manifest.Get("lifecycle")),
		"next", stringArray(
			jsexc.Concat("fl serve --bundle ", written.Directory, " --expect-root ", written.RootDigest),
			"Retain the bundle root outside the package before relying on rewrite detection or sharing the incident."),
		// The second limitation's NOT_PROVIDED arm (incident-continue.ts:186)
		// needs an undefined expectedFrozenDigest, which no caller passes.
		"limitations", stringArray(
			"FaultLine executed only the human-frozen witness. It did not infer a remote base, modify the draft, approve a witness, or alter the frozen record.",
			"The supplied external frozen-witness digest matched the immutable review chain.")), nil
}

func stringArray(items ...string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = str(item)
	}
	return jsjson.MakeArray(values)
}
