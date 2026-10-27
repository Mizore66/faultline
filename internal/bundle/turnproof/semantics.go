package turnproof

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/evidence"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/turninv"
)

// RunArtifactPath ports runArtifactPath (turn-proof-bundle.ts:303).
func RunArtifactPath(run jsjson.Value) string {
	return "runs/" + strings.TrimPrefix(run.Get("runId").Str(), "sha256:") + ".json"
}

// TransitionArtifactPath ports transitionArtifactPath (:307).
func TransitionArtifactPath(index int) string { return fmt.Sprintf("transitions/%04d.json", index) }

// RequiredArtifactPaths ports requiredArtifactPaths (:311): localeCompare-sorted.
func RequiredArtifactPaths(result jsjson.Value) []string {
	paths := []string{
		"manifest.json",
		"investigation.json",
		"witness/frozen.json",
		"source/trees.metadata.json",
		"source/trees.pack",
		"VERIFY.md",
		"lifecycle/ledger.json",
	}
	for _, run := range result.Get("runs").Items() {
		paths = append(paths, RunArtifactPath(run))
	}
	for i := range result.Get("transitions").Items() {
		paths = append(paths, TransitionArtifactPath(i))
	}
	return canonical.SortLocale(paths)
}

// ExpectedManifestArtifacts ports expectedManifestArtifacts (:325).
func ExpectedManifestArtifacts(result jsjson.Value) jsjson.Value {
	runs := []jsjson.Value{}
	for _, run := range result.Get("runs").Items() {
		runs = append(runs, jsjson.MustObject("runId", run.Get("runId"), "path", jsjson.MakeString(RunArtifactPath(run))))
	}
	transitions := []jsjson.Value{}
	for i := range result.Get("transitions").Items() {
		transitions = append(transitions, jsjson.MustObject("index", jsjson.MakeNumber(float64(i)), "path", jsjson.MakeString(TransitionArtifactPath(i))))
	}
	return jsjson.MustObject(
		"investigation", jsjson.MakeString("investigation.json"),
		"frozenWitness", jsjson.MakeString("witness/frozen.json"),
		"runs", jsjson.MakeArray(runs),
		"transitions", jsjson.MakeArray(transitions),
		"sourceMetadata", jsjson.MakeString("source/trees.metadata.json"),
		"treesPack", jsjson.MakeString("source/trees.pack"),
		"verification", jsjson.MakeString("VERIFY.md"),
		"lifecycleLedger", jsjson.MakeString("lifecycle/ledger.json"),
	)
}

// expectedExecutionID ports expectedExecutionId (:338): the turn formula,
// not gitproof's.
func expectedExecutionID(run jsjson.Value) string {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(turninv.SchemaVersion))
	for _, k := range []string{"executionNonce", "stateIndex", "turnId", "turnOrdinal", "treeDigest", "snapshotDigest",
		"environmentFingerprintDigest", "frozenDigest", "executionAttempt"} {
		o.Set(k, run.Get(k))
	}
	return bundle.Must(canonical.DigestJSON(jsjson.MakeObject(o)))
}

// expectedRunID ports expectedRunId (:353).
func expectedRunID(run jsjson.Value) string {
	return bundle.Must(canonical.DigestJSON(bundle.Without(run, "runId")))
}

// BindTurnLifecycle ports bindTurnLifecycle (:497): the binding (in
// LifecycleBoundSchema key order) and the parsed ledger, or what TS throws.
// A schema failure is a *schema.Error whose message is read lazily, as
// zod's getter is.
func BindTurnLifecycle(ledgerInput, result jsjson.Value) (binding, ledger jsjson.Value, err error) {
	ledger, err = bundle.ParseValue(ledgerInput, gitproof.CodexLifecycleLedgerSchema)
	if err != nil {
		return jsjson.Value{}, jsjson.Value{}, err
	}
	verification := gitproof.VerifyCodexLifecycleLedger(ledger)
	if !verification.Valid || verification.HeadHash == nil {
		return jsjson.Value{}, jsjson.Value{}, errors.New(jsexc.Concat("Cannot bind an invalid Codex lifecycle ledger: ", jsexc.Join(verification.Errors, "; ")))
	}
	ledgerDigest := result.Get("ledgerDigest")
	if ledgerDigest.Kind() == jsjson.Null || bundle.Must(canonical.DigestJSON(ledger)) != ledgerDigest.Str() {
		return jsjson.Value{}, jsjson.Value{}, errors.New("Lifecycle ledger digest does not match the turn investigation ledgerDigest.")
	}
	rebound, err := turninv.StatesFromLedger(ledger)
	if err != nil {
		return jsjson.Value{}, jsjson.Value{}, err
	}
	if !bundle.SameCanonical(turninv.StatesValue(rebound), result.Get("states")) {
		return jsjson.Value{}, jsjson.Value{}, errors.New("Lifecycle ledger turn states do not canonically match the investigation state sequence.")
	}
	events := ledger.Get("events").Items()
	i := slices.IndexFunc(events, func(e jsjson.Value) bool { return e.Get("event", "type").Str() == "SESSION_STARTED" })
	if i < 0 {
		return jsjson.Value{}, jsjson.Value{}, errors.New("Cannot bind a lifecycle ledger that has no SESSION_STARTED observation.")
	}
	binding, err = bundle.ParseValue(jsjson.MustObject(
		"status", jsjson.MakeString("BOUND"),
		"path", jsjson.MakeString("lifecycle/ledger.json"),
		"ledgerDigest", ledgerDigest,
		"headHash", jsjson.MakeString(*verification.HeadHash),
		"transport", events[i].Get("event", "payload", "transport"),
	), LifecycleBoundSchema)
	if err != nil {
		return jsjson.Value{}, jsjson.Value{}, err
	}
	return binding, ledger, nil
}

var digestPinnedImage = jsre.MustCompile(digestPinnedImageSource, "")

func strIs(v jsjson.Value, want string) bool { return v.Kind() == jsjson.String && v.Str() == want }

func fmtNum(v jsjson.Value) string { return jsjson.FormatNumber(v.Num()) }

// numberSet is a JS Set<number> of integers: float64 keys are SameValueZero
// for them (+0 and -0 are one key).
type numberSet = map[float64]bool

// ValidateProofSemantics ports validateTurnInvestigationProofSemantics
// (:533). result and frozenWitness are schema-parsed.
func ValidateProofSemantics(result, frozenWitness jsjson.Value) []string {
	errs := []string{}
	add := func(s string) { errs = append(errs, s) }
	proof := result.Get("proof")
	states := result.Get("states").Items()
	runs := result.Get("runs").Items()
	witness := result.Get("witness")
	frozenDigest := frozenWitness.Get("frozenDigest").Str()
	witnessDigest := frozenWitness.Get("witnessDigest").Str()

	if result.Get("status").Str() != "COMPLETED" {
		add("Turn proof bundle only accepts a COMPLETED investigation")
	}
	if len(result.Get("errors").Items()) != 0 {
		add("completed turn investigation must not contain recorder errors")
	}
	if result.Get("repository").Kind() == jsjson.Null || result.Get("ledgerDigest").Kind() == jsjson.Null {
		add("completed turn investigation must include repository and ledgerDigest")
	}
	if len(states) < 2 {
		add("Turn proof bundle requires at least two recorded turn states")
	}
	if !proof.Get("dockerIsolated").Bool() {
		add("Turn proof bundle requires Docker-isolated execution")
	}
	if proof.Get("executionTrust").Str() != "NATIVE_DOCKER" {
		add("Turn proof bundle requires native Docker executor provenance")
	}
	if !proof.Get("isProof").Bool() {
		add("Turn proof bundle requires at least one reconstructed stable transition")
	}
	// :547 !result.witness?.valid || result.witness.externalDigestStatus !== "MATCH"
	if witness.Kind() != jsjson.Object || !witness.Get("valid").Bool() || witness.Get("externalDigestStatus").Str() != "MATCH" {
		add("investigation does not attest a valid externally matched frozen witness")
	}
	fingerprints := result.Get("environmentFingerprints").Items()
	if len(fingerprints) != len(states) {
		add("turn proof requires one environment fingerprint per recorded state")
	}
	runtimeMapping := result.Get("runtimeMapping")
	for index, fingerprint := range fingerprints {
		digest := fingerprint.Get("digest").Str()
		mappedImage := runtimeMapping.Get(digest) // a digest-pinned image string, or undefined
		mapped := mappedImage.Kind() == jsjson.String && mappedImage.Str() != ""
		if !mapped {
			add(jsexc.Concat("runtimeMapping is missing fingerprint ", digest))
		}
		// :558 result.states[index]: by position, not by state.index.
		if index < len(states) {
			stateIndex := states[index].Get("index").Num()
			for _, run := range runs {
				if run.Get("stateIndex").Num() != stateIndex {
					continue
				}
				if run.Get("environmentFingerprintDigest").Str() != digest {
					add(jsexc.Concat("run fingerprint digest does not match state fingerprint: ", run.Get("runId").Str()))
				}
				if mapped && !strIs(run.Get("sandbox", "runtime", "image"), mappedImage.Str()) {
					add(jsexc.Concat("run image does not match runtimeMapping for its fingerprint: ", run.Get("runId").Str()))
				}
			}
		}
	}
	if result.Get("environmentHomogeneity").Str() == "HETEROGENEOUS" {
		mappedKeys := map[string]bool{}
		for _, k := range runtimeMapping.Obj().Keys() {
			jsexc.SetAdd(mappedKeys, k)
		}
		required := map[string]bool{}
		for _, fingerprint := range fingerprints {
			digest := fingerprint.Get("digest").Str()
			if required[digest] {
				continue
			}
			jsexc.SetAdd(required, digest)
			if !mappedKeys[digest] {
				add(jsexc.Concat("heterogeneous proof missing runtimeMapping entry: ", digest))
			}
		}
	}

	witnessVerification := gitproof.VerifyFrozenWitnessRecord(frozenWitness, frozenDigest, true)
	if !witnessVerification.Valid || witnessVerification.ExternalDigestStatus != "MATCH" {
		for _, e := range witnessVerification.Errors {
			add(jsexc.Concat("frozen witness is invalid: ", e))
		}
	}
	if witness.Kind() == jsjson.Object {
		if !strIs(witness.Get("frozenDigest"), frozenDigest) {
			add("investigation witness frozen digest does not match frozen witness artifact")
		}
		if !strIs(witness.Get("witnessDigest"), witnessDigest) {
			add("investigation witness digest does not match frozen witness artifact")
		}
	}

	stateByIndex := map[float64]jsjson.Value{}
	for offset, state := range states {
		index := state.Get("index").Num()
		if index != float64(offset) {
			add(jsexc.Concat("state index is not contiguous at offset ", strconv.Itoa(offset)))
		}
		if _, dup := stateByIndex[index]; dup {
			add(jsexc.Concat("duplicate state index: ", fmtNum(state.Get("index"))))
		}
		jsexc.MapSet(stateByIndex, index, state)
	}

	runIDs, executionIDs, nonces := map[string]bool{}, map[string]bool{}, map[string]bool{}
	attemptsByState := map[float64]numberSet{}
	commandDigest := gitproof.CommandDigest(frozenWitness.Get("proposal", "witness", "command").Str())
	for _, run := range runs {
		runID := run.Get("runId").Str()
		executionID := run.Get("executionId").Str()
		nonce := run.Get("executionNonce").Str()
		if runID != expectedRunID(run) {
			add(jsexc.Concat("runId does not match the canonical run fact: ", runID))
		}
		if executionID != expectedExecutionID(run) {
			add(jsexc.Concat("executionId does not match its state and attempt: ", runID))
		}
		if runIDs[runID] {
			add(jsexc.Concat("duplicate runId: ", runID))
		}
		if executionIDs[executionID] {
			add(jsexc.Concat("duplicate executionId: ", executionID))
		}
		if nonces[nonce] {
			add(jsexc.Concat("duplicate execution nonce: ", nonce))
		}
		jsexc.SetAdd(runIDs, runID)
		jsexc.SetAdd(executionIDs, executionID)
		jsexc.SetAdd(nonces, nonce)
		state, ok := stateByIndex[run.Get("stateIndex").Num()]
		if !ok ||
			state.Get("turnId").Str() != run.Get("turnId").Str() ||
			state.Get("turnOrdinal").Num() != run.Get("turnOrdinal").Num() ||
			state.Get("role").Str() != run.Get("role").Str() ||
			state.Get("treeDigest").Str() != run.Get("treeDigest").Str() ||
			state.Get("snapshotDigest").Str() != run.Get("snapshotDigest").Str() {
			add(jsexc.Concat("run state does not match the state sequence: ", runID))
		}
		if run.Get("frozenDigest").Str() != frozenDigest || run.Get("witnessDigest").Str() != witnessDigest {
			add(jsexc.Concat("run witness digest does not match the frozen witness: ", runID))
		}
		sandbox, r := run.Get("sandbox"), run.Get("result")
		if sandbox.Get("kind").Str() != r.Get("kind").Str() {
			add(jsexc.Concat("run sandbox kind does not match result kind: ", runID))
		}
		for _, auditError := range gitproof.ValidateSandboxPlanAudit(sandbox) {
			add(jsexc.Concat("run sandbox audit is invalid: ", runID, ": ", auditError))
		}
		if sandbox.Get("witnessDigest").Str() != frozenDigest {
			add(jsexc.Concat("run sandbox witness digest does not match frozen witness: ", runID))
		}
		if sandbox.Get("commandDigest").Str() != commandDigest {
			add(jsexc.Concat("run sandbox command digest does not match frozen command bytes: ", runID))
		}
		runtime := sandbox.Get("runtime")
		if !digestPinnedImage.Test(runtime.Get("image").Str()) { // runtime.image ?? ""
			add(jsexc.Concat("run sandbox image is not digest-pinned: ", runID))
		}
		// :632-633, one message for all seven.
		if !strIs(runtime.Get("entrypoint"), "/bin/sh") || !strIs(runtime.Get("network"), "none") || !runtime.Get("rootFilesystemReadOnly").Bool() ||
			!strIs(runtime.Get("user"), "65534:65534") || !runtime.Get("capDropAll").Bool() || !runtime.Get("noNewPrivileges").Bool() || !strIs(runtime.Get("pull"), "never") {
			add(jsexc.Concat("run sandbox runtime policy is not a locked Docker plan: ", runID))
		}
		if r.Get("kind").Str() != "DOCKER_ISOLATED" {
			add(jsexc.Concat("non-Docker run cannot support this proof bundle: ", runID))
		}
		if r.Get("executor").Str() != "NATIVE_DOCKER" {
			add(jsexc.Concat("non-native Docker executor cannot support this proof bundle: ", runID))
		}
		verdict, reason := r.Get("verdict").Str(), r.Get("reason").Str()
		if verdict != "PASS" && verdict != "FAIL" {
			add(jsexc.Concat("non-decisive run cannot support this proof bundle: ", runID))
		}
		if verdict == "PASS" && reason != "PREDICATE_PASS" {
			add(jsexc.Concat("PASS run must use PREDICATE_PASS (legacy EXIT_ZERO is not decisive): ", runID))
		}
		if verdict == "FAIL" && reason != "PREDICATE_FAIL" {
			add(jsexc.Concat("FAIL run must use PREDICATE_FAIL (legacy EXIT_NONZERO is not decisive): ", runID))
		}
		stateIndex := run.Get("stateIndex").Num()
		attempts := attemptsByState[stateIndex]
		if attempts == nil {
			attempts = numberSet{}
		}
		attempt := run.Get("executionAttempt").Num()
		if attempts[attempt] {
			add(jsexc.Concat("duplicate execution attempt for state ", fmtNum(run.Get("stateIndex"))))
		}
		jsexc.SetAdd(attempts, attempt)
		jsexc.MapSet(attemptsByState, stateIndex, attempts)
	}
	if len(runs) != len(states)*gitproof.StableExecutionCount {
		add("completed proof investigation does not contain exactly three runs per state")
	}
	for _, state := range states {
		attempts := attemptsByState[state.Get("index").Num()]
		if attempts == nil || len(attempts) != gitproof.StableExecutionCount || !attempts[1] || !attempts[2] || !attempts[3] {
			add(jsexc.Concat("state ", fmtNum(state.Get("index")), " does not contain attempts 1, 2, and 3 exactly once"))
		}
	}

	stableStates := turninv.ReconstructStable(turninv.StatesFromValue(result.Get("states")), runs, true)
	if !bundle.SameCanonical(result.Get("stableStates"), jsjson.MakeArray(stableStates)) {
		add("persisted stable states contradict the reconstructed run facts")
	}
	transitions := turninv.FindTransitions(stableStates)
	if !bundle.SameCanonical(result.Get("transitions"), jsjson.MakeArray(transitions)) {
		add("persisted transitions contradict the reconstructed stable states")
	}
	if result.Get("nonMonotonic").Bool() != turninv.HasNonMonotonic(transitions) {
		add("persisted nonMonotonic flag contradicts reconstructed transitions")
	}
	if proof.Get("proofTransitions").Num() != float64(len(transitions)) {
		add("proof transition count contradicts reconstructed transitions")
	}
	expectedProof := proof.Get("dockerIsolated").Bool() && proof.Get("executionTrust").Str() == "NATIVE_DOCKER" &&
		result.Get("status").Str() == "COMPLETED" && len(transitions) > 0
	if proof.Get("isProof").Bool() != expectedProof {
		add("proof isProof flag contradicts reconstructed transitions and status")
	}
	if expectedProof && proof.Get("reason").Str() != "Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states." {
		add("proof reason does not match a completed Docker transition proof")
	}
	grade := proof.Get("evidenceGrade").Str()
	if grade == evidence.TurnPathEvidenceGradeParityReserved {
		add("TURN_PROOF is reserved until external validation, turn-boundary counterfactuals, prevention integration, and platform soak are met; use EXPERIMENTAL_TURN")
	}
	if expectedProof && grade != evidence.TurnPathEvidenceGradeExperimental {
		add("until turn/Git proof parity, isProof turn packages must remain graded EXPERIMENTAL_TURN")
	}
	if expectedProof && !slices.Contains(evidence.TurnPathEvidenceLabelsExperimental, proof.Get("evidenceLabel").Str()) {
		add(`isProof turn packages must carry evidenceLabel "` + evidence.TurnPathEvidenceLabelExperimental +
			`" (or the legacy wording "` + evidence.TurnPathEvidenceLabelsExperimental[1] + `")`)
	}
	return errs
}
