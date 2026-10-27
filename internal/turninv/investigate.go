package turninv

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/evidence"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
	"github.com/Mizore66/faultline/internal/sandbox"
)

// Request is investigateTurnTrees' options (turn-investigation.ts:613).
type Request struct {
	Repository, LedgerPath, ExpectedFrozenDigest, Image string
	FrozenWitness                                       jsjson.Value
	RuntimeMapping                                      []gitinv.MappingEntry // nil is undefined ({})
	Runner                                              sandbox.Runner        // nil: native
	Injected                                            bool                  // options.runner !== undefined
}

// attempt runs fn as the body of a JS try block: a thrown exception (jsexc)
// and a returned error are both what the catch clause sees.
func attempt(fn func() error) (err error) {
	if thrown := jsexc.Try(func() { err = fn() }); thrown != nil {
		return thrown
	}
	return err
}

func stringArray(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = jsjson.MakeString(item)
	}
	return jsjson.MakeArray(values)
}

// sha256Digest ports sha256Digest (:254) for a JS string (hashed as UTF-8).
func sha256Digest(value string) string { return "sha256:" + canonical.SHA256Hex(value) }

// evidencePreview is redactedEvidencePreview's result.
type evidencePreview struct {
	value               string
	truncated, redacted bool
}

// redactedEvidencePreview ports redactedEvidencePreview (:258): the first
// 16 KiB of the UTF-8 bytes, decoded lossily, then redacted at
// $.turn-run.<stream>.
func redactedEvidencePreview(value, stream string) evidencePreview {
	b := []byte(jsstr.ToUTF8(value))
	preview := jsstr.DecodeUTF8(b[:min(len(b), evidenceLogPreviewBytes)])
	safe, report := redaction.RedactText(preview, "$.turn-run."+stream)
	return evidencePreview{safe, len(b) > evidenceLogPreviewBytes, report.Redacted}
}

// witnessSummary ports witnessSummary (:273).
func witnessSummary(frozenWitness jsjson.Value, verification gitproof.WitnessVerification) jsjson.Value {
	frozenDigest := jsjson.MakeNull()
	if verification.FrozenDigest != nil {
		frozenDigest = jsjson.MakeString(*verification.FrozenDigest)
	}
	witnessDigest := jsjson.MakeNull()
	if verification.Valid {
		witnessDigest = frozenWitness.Get("witnessDigest")
	}
	return jsjson.MustObject(
		"valid", jsjson.MakeBool(verification.Valid),
		"errors", verification.ErrorsValue(),
		"frozenDigest", frozenDigest,
		"witnessDigest", witnessDigest,
		"externalDigestStatus", jsjson.MakeString(verification.ExternalDigestStatus),
		"approval", verification.ApprovalValue(),
	)
}

// executionTrustFor ports executionTrustFor (:284).
func executionTrustFor(hasInjectedRunner bool) string {
	if hasInjectedRunner {
		return "INJECTED_RUNNER"
	}
	return "NATIVE_DOCKER"
}

// proofValue is the proof object of emptyProof (:288) and of the final
// result (:852).
func proofValue(executionTrust string, proofTransitions int, isProof bool, reason string, hasTransitions bool) jsjson.Value {
	grade, label := evidence.TurnPathEvidence(hasTransitions)
	return jsjson.MustObject(
		"requiresDockerIsolation", jsjson.MakeBool(true),
		"dockerIsolated", jsjson.MakeBool(executionTrust == "NATIVE_DOCKER"),
		"executionTrust", jsjson.MakeString(executionTrust),
		"proofTransitions", jsjson.MakeNumber(float64(proofTransitions)),
		"isProof", jsjson.MakeBool(isProof),
		"reason", jsjson.MakeString(reason),
		"evidenceGrade", jsjson.MakeString(grade),
		"evidenceLabel", jsjson.MakeString(label),
	)
}

// emptyProof ports emptyProof (:288).
func emptyProof(reason, executionTrust string) jsjson.Value {
	return proofValue(executionTrust, 0, false, reason, false)
}

// emptyIntroduction ports emptyIntroduction (:302).
func emptyIntroduction(reason string) jsjson.Value {
	return introduction("NOT_APPLICABLE", jsjson.MakeNull(), jsjson.MakeNull(), reason)
}

// resultFields are baseResult's fields (:314). A zero (undefined)
// repository, ledgerDigest or witness is null, nil states or fingerprints
// are [], "" homogeneity is EMPTY; runtimeMapping is always {} (no caller
// passes another); proofReason nil is undefined.
type resultFields struct {
	repository, ledgerDigest, witness jsjson.Value
	states                            []State
	fingerprints                      []jsjson.Value
	homogeneity                       string
	proofReason                       *string
}

func orNull(v jsjson.Value) jsjson.Value {
	if v.Kind() == jsjson.Undefined {
		return jsjson.MakeNull()
	}
	return v
}

// baseResult ports baseResult (:311).
func baseResult(status string, errs []string, f resultFields, executionTrust string) (jsjson.Value, error) {
	reasonOr := func(fallback string) string {
		if f.proofReason != nil {
			return *f.proofReason
		}
		if len(errs) > 0 {
			return errs[0]
		}
		return fallback
	}
	homogeneity := f.homogeneity
	if homogeneity == "" {
		homogeneity = gitinv.Empty
	}
	return bundle.ParseValue(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"recorder", jsjson.MakeString("codex-turn-tree-replay"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"status", jsjson.MakeString(status),
		"repository", orNull(f.repository),
		"ledgerDigest", orNull(f.ledgerDigest),
		"witness", orNull(f.witness),
		"executionsPerState", jsjson.MakeNumber(gitproof.StableExecutionCount),
		"states", StatesValue(f.states),
		"runs", jsjson.MakeArray(nil),
		"stableStates", jsjson.MakeArray(nil),
		"transitions", jsjson.MakeArray(nil),
		"environmentFingerprints", jsjson.MakeArray(slices.Clone(f.fingerprints)),
		"environmentHomogeneity", jsjson.MakeString(homogeneity),
		"runtimeMapping", jsjson.MakeObject(jsjson.NewObj()),
		"introduction", emptyIntroduction(reasonOr("Turn investigation did not localize a transition.")),
		"nonMonotonic", jsjson.MakeBool(false),
		"proof", emptyProof(reasonOr("Turn investigation did not produce proof."), executionTrust),
		"errors", stringArray(errs),
	), ResultSchema)
}

// runFact ports runFact (:531). Its randomUUID is the read after the
// attempt's finishing Date.now().
func runFact(state State, executionAttempt int, frozenWitness, overlays, sandboxAudit, result jsjson.Value,
	startedAt, finishedAt string, durationMs int64, environmentFingerprintDigest string) (jsjson.Value, error) {
	executionNonce := clock.NewUUID()
	executionID, err := canonical.DigestJSON(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"executionNonce", jsjson.MakeString(executionNonce),
		"stateIndex", jsjson.MakeNumber(state.Index),
		"turnId", jsjson.MakeString(state.TurnID),
		"turnOrdinal", jsjson.MakeNumber(state.TurnOrdinal),
		"treeDigest", jsjson.MakeString(state.TreeDigest),
		"snapshotDigest", jsjson.MakeString(state.SnapshotDigest),
		"environmentFingerprintDigest", jsjson.MakeString(environmentFingerprintDigest),
		"frozenDigest", frozenWitness.Get("frozenDigest"),
		"executionAttempt", jsjson.MakeNumber(float64(executionAttempt)),
	))
	if err != nil {
		return jsjson.Value{}, err
	}
	stdout, stderr := result.Get("stdout").Str(), result.Get("stderr").Str()
	stdoutPreview := redactedEvidencePreview(stdout, "stdout")
	stderrPreview := redactedEvidencePreview(stderr, "stderr")
	unsigned := jsjson.NewObj()
	for _, kv := range []struct {
		k string
		v jsjson.Value
	}{
		{"schemaVersion", jsjson.MakeString(SchemaVersion)},
		{"executionId", jsjson.MakeString(executionID)},
		{"executionNonce", jsjson.MakeString(executionNonce)},
		{"stateIndex", jsjson.MakeNumber(state.Index)},
		{"executionAttempt", jsjson.MakeNumber(float64(executionAttempt))},
		{"turnId", jsjson.MakeString(state.TurnID)},
		{"turnOrdinal", jsjson.MakeNumber(state.TurnOrdinal)},
		{"role", jsjson.MakeString(state.Role)},
		{"treeDigest", jsjson.MakeString(state.TreeDigest)},
		{"snapshotDigest", jsjson.MakeString(state.SnapshotDigest)},
		{"environmentFingerprintDigest", jsjson.MakeString(environmentFingerprintDigest)},
		{"frozenDigest", frozenWitness.Get("frozenDigest")},
		{"witnessDigest", frozenWitness.Get("witnessDigest")},
		{"startedAt", jsjson.MakeString(startedAt)},
		{"finishedAt", jsjson.MakeString(finishedAt)},
		{"durationMs", jsjson.MakeNumber(float64(durationMs))},
		{"overlays", jsjson.MakeArray(slices.Clone(overlays.Items()))},
		{"sandbox", sandboxAudit},
		{"result", jsjson.MustObject(
			"kind", result.Get("kind"),
			"executor", result.Get("executor"),
			"verdict", result.Get("verdict"),
			"reason", result.Get("reason"),
			"exitCode", result.Get("exitCode"),
			"signal", result.Get("signal"),
			"outputTruncated", result.Get("outputTruncated"),
			"stdoutDigest", jsjson.MakeString(sha256Digest(stdout)),
			"stdoutBytes", jsjson.MakeNumber(float64(len(jsstr.ToUTF8(stdout)))),
			"stdoutPreview", jsjson.MakeString(stdoutPreview.value),
			"stdoutPreviewTruncated", jsjson.MakeBool(stdoutPreview.truncated),
			"stdoutRedacted", jsjson.MakeBool(stdoutPreview.redacted),
			"stderrDigest", jsjson.MakeString(sha256Digest(stderr)),
			"stderrBytes", jsjson.MakeNumber(float64(len(jsstr.ToUTF8(stderr)))),
			"stderrPreview", jsjson.MakeString(stderrPreview.value),
			"stderrPreviewTruncated", jsjson.MakeBool(stderrPreview.truncated),
			"stderrRedacted", jsjson.MakeBool(stderrPreview.redacted),
		)},
	} {
		unsigned.Set(kv.k, kv.v)
	}
	runID, err := canonical.DigestJSON(jsjson.MakeObject(unsigned))
	if err != nil {
		return jsjson.Value{}, err
	}
	unsigned.Set("runId", jsjson.MakeString(runID))
	return bundle.ParseValue(jsjson.MakeObject(unsigned), RunFactSchema)
}

// classifyStatus ports classifyStatus (:600).
func classifyStatus(runs []jsjson.Value, errs []string) string {
	if len(errs) > 0 {
		return "EXECUTION_ERROR"
	}
	for _, run := range runs {
		if isString(run.Get("result", "reason"), "SANDBOX_UNAVAILABLE") {
			return "SANDBOX_UNAVAILABLE"
		}
	}
	for _, run := range runs {
		if isString(run.Get("result", "verdict"), "ERROR") {
			return "EXECUTION_ERROR"
		}
	}
	return "COMPLETED"
}

// pad4 is String(n).padStart(4, "0"); String(n) is ASCII.
func pad4(n float64) string {
	s := jsjson.FormatNumber(n)
	return strings.Repeat("0", max(0, 4-len(s))) + s
}

func fingerprintDigests(fingerprints []jsjson.Value) []string {
	digests := make([]string, len(fingerprints))
	for i, fp := range fingerprints {
		digests[i] = fp.Get("digest").Str()
	}
	return digests
}

// mappingImage is `effectiveRuntimeMapping[digest]`; "" is undefined.
func mappingImage(mapping []gitinv.MappingEntry, digest string) string {
	for _, e := range mapping {
		if e.Fingerprint == digest {
			return e.Image
		}
	}
	return ""
}

func mappingValue(mapping []gitinv.MappingEntry) jsjson.Value {
	o := jsjson.NewObj()
	for _, e := range mapping {
		o.Set(e.Fingerprint, jsjson.MakeString(e.Image))
	}
	return jsjson.MakeObject(o)
}

// InvestigateTurnTrees ports investigateTurnTrees (:613); the result is the
// ResultSchema-parsed value; an error is what the TS promise rejects with
// (the ledger read, a schema parse, the temp root).
func InvestigateTurnTrees(r Request) (result jsjson.Value, err error) {
	if thrown := jsexc.Try(func() { result, err = investigate(r) }); thrown != nil {
		return jsjson.Value{}, thrown
	}
	return result, err
}

func investigate(r Request) (jsjson.Value, error) {
	runner := r.Runner
	if runner == nil {
		runner = sandbox.NativeRunner()
	}
	trust := executionTrustFor(r.Injected)
	callerMapping := r.RuntimeMapping
	repository := nodefs.Resolve(r.Repository)
	verification := gitproof.VerifyFrozenWitnessRecord(r.FrozenWitness, r.ExpectedFrozenDigest, true)
	summary := witnessSummary(r.FrozenWitness, verification)
	if !verification.Valid || verification.ExternalDigestStatus != "MATCH" {
		reason := "Frozen witness digest mismatch."
		return baseResult("INVALID_WITNESS", verification.Errors, resultFields{witness: summary, proofReason: &reason}, trust)
	}

	ledger, err := gitproof.ReadVerifiedCodexLifecycleLedger(nodefs.Resolve(r.LedgerPath))
	if err != nil {
		return jsjson.Value{}, err
	}
	ledgerDigestText, err := canonical.DigestJSON(ledger)
	if err != nil {
		return jsjson.Value{}, err
	}
	ledgerDigest := jsjson.MakeString(ledgerDigestText)
	states, err := StatesFromLedger(ledger)
	if err != nil {
		return jsjson.Value{}, err
	}
	repositoryValue := jsjson.MakeString(repository)
	if len(states) < 2 {
		reason := "Need at least two snapshot states (session baseline and/or TURN_TREE_SNAPSHOT events) for turn localization."
		return baseResult("NO_TURN_SNAPSHOTS", nil, resultFields{repository: repositoryValue, ledgerDigest: ledgerDigest, witness: summary,
			states: states, proofReason: &reason}, trust)
	}
	if duplicateError := DuplicateTurnOrdinal(states); duplicateError != "" {
		reason := "Multiple snapshot events share a turnOrdinal; refuse ambiguous localization."
		return baseResult("DUPLICATE_TURN_SNAPSHOTS", []string{duplicateError}, resultFields{repository: repositoryValue, ledgerDigest: ledgerDigest,
			witness: summary, states: states, proofReason: &reason}, trust)
	}

	tempRoot, err := nodefs.MkdtempAsync(nodefs.Join(nodefs.Tmpdir(), "faultline-turn-investigation-"))
	if err != nil {
		return jsjson.Value{}, err
	}
	var (
		runs         []jsjson.Value
		errs         []string
		fingerprints []jsjson.Value
		effective    []gitinv.MappingEntry
		early        *jsjson.Value
	)
	pushError := func(err error) { errs = append(errs, jsexc.Message(err)) }
	cleanup := func(tree *gitinv.MaterializedTree) {
		if tree != nil {
			if err := tree.Cleanup(); err != nil {
				pushError(err)
			}
		}
	}
	fields := func(proofReason string) resultFields {
		digests := fingerprintDigests(fingerprints)
		return resultFields{repository: repositoryValue, ledgerDigest: ledgerDigest, witness: summary, states: states,
			fingerprints: fingerprints, homogeneity: gitinv.Homogeneity(digests), proofReason: &proofReason}
	}
	bodyErr := attempt(func() error {
		// Phase 1: materialize each state only to fingerprint the environment.
		for _, state := range states {
			var tree *gitinv.MaterializedTree
			if err := attempt(func() error {
				m, err := gitinv.MaterializeGitTree(repository, state.TreeDigest, tempRoot, "fp-"+pad4(state.TurnOrdinal)+"-"+state.TreeDigest[:12])
				if err != nil {
					return err
				}
				tree = &m
				fingerprint, err := gitinv.ComputeFingerprint(m.Worktree)
				if err != nil {
					return err
				}
				fingerprints = append(fingerprints, fingerprint)
				return nil
			}); err != nil {
				// A failed materialization contributes no fingerprint and
				// blocks later proof.
				pushError(err)
			}
			cleanup(tree)
		}

		if len(fingerprints) != len(states) {
			errorList := errs
			if len(errorList) == 0 {
				errorList = []string{"Could not fingerprint every turn state."}
			}
			v, err := baseResult("EXECUTION_ERROR", errorList, fields("Could not compute an environment fingerprint for every turn state."), trust)
			early = &v
			return err
		}

		digests := fingerprintDigests(fingerprints)
		homogeneity := gitinv.Homogeneity(digests)
		if homogeneity == gitinv.Heterogeneous && len(gitinv.MissingRuntimeMappingDigests(digests, callerMapping)) > 0 {
			v, err := baseResult("ENVIRONMENT_CHANGED", append(slices.Clone(errs), gitinv.EnvironmentChangedProofMessage),
				fields(gitinv.EnvironmentChangedProofMessage), trust)
			early = &v
			return err
		}

		if err := attempt(func() error {
			var err error
			effective, err = gitinv.BuildEffectiveRuntimeMapping(digests, callerMapping, r.Image, homogeneity)
			return err
		}); err != nil {
			message := jsexc.Message(err)
			status := "EXECUTION_ERROR"
			if message == gitinv.EnvironmentChangedProofMessage {
				status = "ENVIRONMENT_CHANGED"
			}
			v, err := baseResult(status, append(slices.Clone(errs), message), fields(message), trust)
			early = &v
			return err
		}

		// Phase 2: rematerialize and execute with the fingerprint-selected image.
		for stateIndex := 0; stateIndex < len(states); stateIndex++ {
			state := states[stateIndex]
			if stateIndex >= len(fingerprints) {
				errs = append(errs, jsexc.Concat("Missing prepared state or fingerprint at index ", strconv.Itoa(stateIndex)))
				continue
			}
			fingerprintDigest := fingerprints[stateIndex].Get("digest").Str()
			image := mappingImage(effective, fingerprintDigest)
			if image == "" {
				errs = append(errs, jsexc.Concat("No resolved runtime image for fingerprint ", fingerprintDigest))
				continue
			}
			var tree *gitinv.MaterializedTree
			if err := attempt(func() error {
				m, err := gitinv.MaterializeGitTree(repository, state.TreeDigest, tempRoot, "run-"+pad4(state.TurnOrdinal)+"-"+state.TreeDigest[:12])
				if err != nil {
					return err
				}
				tree = &m
				overlays, err := gitinv.MaterializeFrozenOverlays(m.Worktree, r.FrozenWitness, m)
				if err != nil {
					return err
				}
				// :776-781, in this key order: no environment, allowedEnvironment
				// or limits, so the witness's timeoutSeconds never reaches the sandbox.
				plan, err := sandbox.CreatePlan(jsjson.MustObject(
					"witness", jsjson.MustObject(
						"digest", r.FrozenWitness.Get("frozenDigest"),
						"command", r.FrozenWitness.Get("proposal", "witness", "command"),
					),
					"sourceDirectory", jsjson.MakeString(m.Worktree),
					"mode", jsjson.MakeString("DOCKER_ISOLATED"),
					"image", jsjson.MakeString(image),
				))
				if err != nil {
					return err
				}
				audit := sandbox.AuditPlan(plan)
				for executionAttempt := 1; executionAttempt <= gitproof.StableExecutionCount; executionAttempt++ {
					startedEpoch := clock.NowMillis()
					startedAt := clock.ISO(time.UnixMilli(startedEpoch))
					execution := sandbox.Execute(plan, runner, r.Injected)
					finishedEpoch := clock.NowMillis()
					fact, err := runFact(state, executionAttempt, r.FrozenWitness, overlays, audit, execution,
						startedAt, clock.ISO(time.UnixMilli(finishedEpoch)), max(0, finishedEpoch-startedEpoch), fingerprintDigest)
					if err != nil {
						return err
					}
					runs = append(runs, fact)
				}
				return nil
			}); err != nil {
				pushError(err)
			}
			cleanup(tree)
		}
		return nil
	})
	// finally (:810): rm's rejection replaces the try block's outcome.
	if err := nodefs.RemoveAllAsync(tempRoot); err != nil {
		return jsjson.Value{}, err
	}
	if bodyErr != nil {
		return jsjson.Value{}, bodyErr
	}
	if early != nil {
		return *early, nil
	}

	homogeneity := gitinv.Homogeneity(fingerprintDigests(fingerprints))
	stable := ReconstructStable(states, runs, false)
	transitions := FindTransitions(stable)
	status := classifyStatus(runs, errs)
	introduction := AttributeFailureIntroduction(states, transitions, stable)
	dockerIsolated := trust == "NATIVE_DOCKER"
	isProof := dockerIsolated && status == "COMPLETED" && len(errs) == 0 && len(transitions) > 0
	var proofReason string
	switch {
	case trust == "INJECTED_RUNNER":
		proofReason = "An injected runner produced these observations; FaultLine refuses to certify it as native Docker proof."
	case status == "SANDBOX_UNAVAILABLE":
		proofReason = "Docker was unavailable; no execution result is proof."
	case status == "EXECUTION_ERROR":
		proofReason = "An execution or worktree error prevents an investigation-wide proof claim."
	case len(transitions) == 0:
		proofReason = "No adjacent turn states produced three matching Docker PASS/FAIL executions."
	default:
		proofReason = "Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states."
	}
	// isProof may be true for turn-bundle eligibility; the evidence grade
	// stays experimental until turn investigation meets the Git-path
	// portable contract.
	return bundle.ParseValue(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"recorder", jsjson.MakeString("codex-turn-tree-replay"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"status", jsjson.MakeString(status),
		"repository", repositoryValue,
		"ledgerDigest", ledgerDigest,
		"witness", summary,
		"executionsPerState", jsjson.MakeNumber(gitproof.StableExecutionCount),
		"states", StatesValue(states),
		"runs", jsjson.MakeArray(runs),
		"stableStates", jsjson.MakeArray(stable),
		"transitions", jsjson.MakeArray(transitions),
		"environmentFingerprints", jsjson.MakeArray(fingerprints),
		"environmentHomogeneity", jsjson.MakeString(homogeneity),
		"runtimeMapping", mappingValue(effective),
		"introduction", introduction,
		"nonMonotonic", jsjson.MakeBool(HasNonMonotonic(transitions)),
		"proof", proofValue(trust, len(transitions), isProof, proofReason, len(transitions) > 0),
		"errors", stringArray(errs),
	), ResultSchema)
}

// Digest ports turnInvestigationDigest (:866).
func Digest(result jsjson.Value) string {
	return jsexc.Must(canonical.DigestJSON(result))
}
