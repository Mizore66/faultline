package gitproof

import (
	"cmp"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

// expectedExecutionId ports git-proof-bundle.ts:502.
func expectedExecutionID(run jsjson.Value) string {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(GitInvestigationSchemaVersion))
	for _, k := range []string{"executionNonce", "stateIndex", "commit", "tree", "frozenDigest", "executionAttempt"} {
		o.Set(k, run.Get(k))
	}
	return bundle.Must(canonical.DigestJSON(jsjson.MakeObject(o)))
}

// expectedRunId ports git-proof-bundle.ts:514.
func expectedRunID(run jsjson.Value) string {
	return bundle.Must(canonical.DigestJSON(bundle.Without(run, "runId")))
}

func numSet(values []float64) map[float64]bool {
	out := map[float64]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

// reconstructStableStates ports git-proof-bundle.ts:519.
func reconstructStableStates(result jsjson.Value) []jsjson.Value {
	stable := []jsjson.Value{}
	proof := result.Get("proof")
	if !proof.Get("dockerIsolated").Bool() || proof.Get("executionTrust").Str() != "NATIVE_DOCKER" {
		return stable
	}
	for _, state := range result.Get("states").Items() {
		var runs []jsjson.Value
		for _, run := range result.Get("runs").Items() {
			if run.Get("stateIndex").Num() == state.Get("index").Num() {
				runs = append(runs, run)
			}
		}
		slices.SortStableFunc(runs, func(a, b jsjson.Value) int {
			return cmp.Compare(a.Get("executionAttempt").Num(), b.Get("executionAttempt").Num())
		})
		if len(runs) != StableExecutionCount {
			continue
		}
		var attempts []float64
		var executionIDs, runIDs []string
		for _, run := range runs {
			attempts = append(attempts, run.Get("executionAttempt").Num())
			executionIDs = append(executionIDs, run.Get("executionId").Str())
			runIDs = append(runIDs, run.Get("runId").Str())
		}
		verdict := runs[0].Get("result", "verdict").Str()
		if len(numSet(attempts)) != StableExecutionCount || bundle.DistinctCount(executionIDs) != StableExecutionCount ||
			(verdict != "PASS" && verdict != "FAIL") ||
			!all(runs, func(run jsjson.Value) bool {
				reason := run.Get("result", "reason").Str()
				return run.Get("commit").Str() == state.Get("commit").Str() && run.Get("tree").Str() == state.Get("tree").Str() &&
					run.Get("result", "kind").Str() == "DOCKER_ISOLATED" && run.Get("result", "executor").Str() == "NATIVE_DOCKER" &&
					run.Get("result", "verdict").Str() == verdict && (reason == "PREDICATE_PASS" || reason == "PREDICATE_FAIL")
			}) {
			continue
		}
		o := jsjson.NewObj()
		o.Set("stateIndex", state.Get("index"))
		o.Set("commit", state.Get("commit"))
		o.Set("tree", state.Get("tree"))
		o.Set("verdict", jsjson.MakeString(verdict))
		o.Set("executionIds", stringList(executionIDs))
		o.Set("runIds", stringList(runIDs))
		stable = append(stable, jsjson.MakeObject(o))
	}
	return stable
}

func all(items []jsjson.Value, pred func(jsjson.Value) bool) bool {
	for _, item := range items {
		if !pred(item) {
			return false
		}
	}
	return true
}

// reconstructTransitions ports git-proof-bundle.ts:548.
func reconstructTransitions(states []jsjson.Value) []jsjson.Value {
	transitions := []jsjson.Value{}
	for i := 1; i < len(states); i++ {
		before, after := states[i-1], states[i]
		if after.Get("stateIndex").Num() != before.Get("stateIndex").Num()+1 || before.Get("verdict").Str() == after.Get("verdict").Str() {
			continue
		}
		kind := "FAIL_TO_PASS"
		if before.Get("verdict").Str() == "PASS" {
			kind = "PASS_TO_FAIL"
		}
		o := jsjson.NewObj()
		o.Set("kind", jsjson.MakeString(kind))
		o.Set("before", before)
		o.Set("after", after)
		transitions = append(transitions, jsjson.MakeObject(o))
	}
	return transitions
}

// hasLifecycleLedger ports git-proof-bundle.ts:565.
func hasLifecycleLedger(binding jsjson.Value) bool { return binding.Get("status").Str() != "UNBOUND" }

// bindLifecycleLedger ports git-proof-bundle.ts:581 for a supplied ledger.
func bindLifecycleLedger(ledgerInput, result jsjson.Value) jsjson.Value {
	ledger := bundle.Must(bundle.ParseValue(ledgerInput, CodexLifecycleLedgerSchema))
	verification := VerifyCodexLifecycleLedger(ledger)
	if !verification.Valid || verification.HeadHash == nil {
		bundle.Throw(errors.New("Cannot bind an invalid Codex lifecycle ledger: " + strings.Join(verification.Errors, "; ")))
	}
	events := ledger.Get("events").Items()
	i := slices.IndexFunc(events, func(e jsjson.Value) bool { return e.Get("event", "type").Str() == "SESSION_STARTED" })
	if i < 0 {
		bundle.Throw(errors.New("Cannot bind a lifecycle ledger that has no SESSION_STARTED observation."))
	}
	sessionStarted := events[i]
	byCommit := map[string]jsjson.Value{} // new Map: the last duplicate wins
	for _, state := range result.Get("states").Items() {
		byCommit[state.Get("commit").Str()] = state
	}
	var bindings []jsjson.Value
	var covered []float64
	descendantBinding := false
	resolved := result.Get("resolvedRange")
	for _, event := range events {
		if event.Get("event", "type").Str() != "WORKTREE_CHECKPOINT" {
			continue
		}
		checkpoint := event.Get("event", "payload", "checkpoint")
		state, ok := byCommit[checkpoint.Get("headCommit").Str()]
		if !ok {
			continue
		}
		if checkpoint.Get("treeDigest").Str() != state.Get("tree").Str() {
			bundle.Throw(errors.New("Lifecycle checkpoint " + num(event.Get("sequence")) + " has a tree that disagrees with investigated commit " + checkpoint.Get("headCommit").Str() + "."))
		}
		o := jsjson.NewObj()
		o.Set("sequence", event.Get("sequence"))
		o.Set("stateIndex", state.Get("index"))
		o.Set("checkpointDigest", checkpoint.Get("digest"))
		bindings = append(bindings, jsjson.MakeObject(o))
		covered = append(covered, state.Get("index").Num())
		if resolved.Kind() == jsjson.Object && state.Get("index").Num() == resolved.Get("descendant", "index").Num() {
			descendantBinding = true
		}
	}
	if len(bindings) == 0 {
		bundle.Throw(errors.New("Cannot bind a lifecycle ledger without a clean checkpoint matching an investigated Git state."))
	}
	if !descendantBinding {
		bundle.Throw(errors.New("Lifecycle ledger must include a clean checkpoint for the investigated descendant state."))
	}
	status, target := "PARTIALLY_BOUND", lifecyclePartiallyBoundSchema
	if len(numSet(covered)) == len(result.Get("states").Items()) {
		status, target = "FULLY_BOUND", lifecycleFullyBoundSchema
	}
	binding := jsjson.NewObj()
	binding.Set("status", jsjson.MakeString(status))
	binding.Set("path", jsjson.MakeString("lifecycle/ledger.json"))
	binding.Set("ledgerDigest", jsjson.MakeString(bundle.Must(canonical.DigestJSON(ledger))))
	binding.Set("headHash", jsjson.MakeString(*verification.HeadHash))
	binding.Set("transport", sessionStarted.Get("event", "payload", "transport"))
	binding.Set("checkpointBindings", jsjson.MakeArray(bindings))
	return bundle.Must(bundle.ParseValue(jsjson.MakeObject(binding), target))
}

// verifyLifecycleBinding ports git-proof-bundle.ts:984.
func verifyLifecycleBinding(root string, manifest, result jsjson.Value, errs *[]string) {
	lifecycle := manifest.Get("lifecycle")
	declared := manifest.Get("artifacts", "lifecycleLedger")
	if lifecycle.Get("status").Str() == "UNBOUND" {
		if declared.Kind() != jsjson.Undefined {
			*errs = append(*errs, "unbound manifest unexpectedly declares a lifecycle ledger artifact")
		}
		return
	}
	if declared.Kind() != jsjson.String || declared.Str() != lifecycle.Get("path").Str() {
		*errs = append(*errs, "bound manifest lifecycle artifact path does not match its lifecycle binding")
		return
	}
	payload := parseJSONFile(root, lifecycle.Get("path").Str(), "lifecycle ledger", errs)
	ledger, issues, ok := schema.Parse(CodexLifecycleLedgerSchema, payload)
	if !ok {
		*errs = append(*errs, "lifecycle ledger schema validation failed: "+schema.ErrorMessage(issues))
		return
	}
	verification := VerifyCodexLifecycleLedger(ledger)
	if !verification.Valid || verification.HeadHash == nil {
		for _, e := range verification.Errors {
			*errs = append(*errs, "lifecycle ledger verification failed: "+e)
		}
		return
	}
	if err := bundle.Try(func() {
		rebound := bindLifecycleLedger(ledger, result)
		expected := rebound
		if lifecycle.Get("status").Str() == "BOUND" {
			legacy := jsjson.NewObj()
			for _, k := range rebound.Obj().Keys() {
				legacy.Set(k, rebound.Obj().Field(k))
			}
			legacy.Set("status", jsjson.MakeString("BOUND"))
			expected = bundle.Must(bundle.ParseValue(jsjson.MakeObject(legacy), lifecycleLegacyBoundSchema))
		}
		if !bundle.SameCanonical(expected, lifecycle) {
			*errs = append(*errs, "lifecycle ledger binding does not match its valid checkpoint-to-state reconstruction")
		}
	}); err != nil {
		*errs = append(*errs, "lifecycle ledger cannot bind to the investigation: "+err.Error())
	}
}

var (
	digestPinnedImage = regexp.MustCompile(digestPinnedImageRe)
	imageDigestSuffix = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)
)

// imageKey maps runtime.image (or its absence) to a distinct Set member.
func imageKey(v jsjson.Value) string {
	switch v.Kind() {
	case jsjson.String:
		return "s" + v.Str()
	case jsjson.Null:
		return "\x00null"
	}
	return "\x00undefined"
}

func fmtNum(v jsjson.Value) string { return jsjson.FormatNumber(v.Num()) }

// validateGitInvestigationProofSemantics ports git-proof-bundle.ts:634.
func validateGitInvestigationProofSemantics(result, frozen jsjson.Value) []string {
	errs := []string{}
	add := func(s string) { errs = append(errs, s) }
	proof := result.Get("proof")
	states := result.Get("states").Items()
	runs := result.Get("runs").Items()
	resolved := result.Get("resolvedRange")
	witness := result.Get("witness")

	if result.Get("status").Str() != "COMPLETED" {
		add("Git proof bundle only accepts a COMPLETED investigation")
	}
	if len(result.Get("errors").Items()) != 0 {
		add("completed Git investigation must not contain recorder errors")
	}
	if result.Get("repository").Kind() == jsjson.Null || resolved.Kind() == jsjson.Null {
		add("completed Git investigation must include repository and resolved range")
	}
	if len(states) < 2 {
		add("Git proof bundle requires at least two resolved commit states")
	}
	if !proof.Get("dockerIsolated").Bool() {
		add("Git proof bundle requires Docker-isolated execution")
	}
	if proof.Get("executionTrust").Str() != "NATIVE_DOCKER" {
		add("Git proof bundle requires native Docker executor provenance")
	}
	if !proof.Get("isProof").Bool() {
		add("Git proof bundle requires at least one reconstructed stable transition")
	}
	if witness.Kind() != jsjson.Object || !witness.Get("valid").Bool() || witness.Get("externalDigestStatus").Str() != "MATCH" {
		add("investigation does not attest a valid externally matched frozen witness")
	}
	if proof.Get("evidenceGrade").Str() == "AGENT_DRAFT" {
		add("AGENT_DRAFT evidence cannot be exported as a Git proof bundle")
	}
	note := frozen.Get("approval", "note").Str() // note ?? ""
	if strings.Contains(note, "AGENT_DRAFT") && !strings.Contains(note, "APPROVED_AFTER_EXECUTION") {
		add("AGENT_DRAFT approvals cannot be exported as a Git proof bundle without APPROVED_AFTER_EXECUTION ratification")
	}
	for _, run := range runs {
		if run.Get("result", "kind").Str() == "UNSAFE_LOCAL" || run.Get("sandbox", "kind").Str() == "UNSAFE_LOCAL" || run.Get("result", "executor").Str() == "UNSAFE_LOCAL" {
			add("UNSAFE_LOCAL run facts are structurally unexportable and refuse package compilation: " + run.Get("runId").Str())
		}
	}

	verification := VerifyFrozenWitnessRecord(frozen, frozen.Get("frozenDigest").Str(), true)
	if !verification.Valid || verification.ExternalDigestStatus != "MATCH" {
		for _, e := range verification.Errors {
			add("frozen witness is invalid: " + e)
		}
	}
	if witness.Kind() == jsjson.Object {
		if witness.Get("frozenDigest").Kind() != jsjson.String || witness.Get("frozenDigest").Str() != frozen.Get("frozenDigest").Str() {
			add("investigation witness frozen digest does not match frozen witness artifact")
		}
		if witness.Get("witnessDigest").Kind() != jsjson.String || witness.Get("witnessDigest").Str() != frozen.Get("witnessDigest").Str() {
			add("investigation witness digest does not match frozen witness artifact")
		}
		if !bundle.SameCanonical(witness.Get("errors"), verification.ErrorsValue()) {
			add("investigation witness errors do not match frozen witness verification")
		}
		if !bundle.SameCanonical(witness.Get("approval"), verification.ApprovalValue()) {
			add("investigation witness approval does not match frozen witness verification")
		}
	}

	stateByIndex := map[float64]jsjson.Value{}
	commits := map[string]bool{}
	for offset, state := range states {
		index := state.Get("index").Num()
		if index != float64(offset) {
			add("state index is not contiguous at offset " + strconv.Itoa(offset))
		}
		if _, dup := stateByIndex[index]; dup {
			add("duplicate state index: " + fmtNum(state.Get("index")))
		}
		if commits[state.Get("commit").Str()] {
			add("duplicate commit in Git state sequence: " + state.Get("commit").Str())
		}
		stateByIndex[index] = state
		commits[state.Get("commit").Str()] = true
	}
	if resolved.Kind() == jsjson.Object && len(states) > 0 {
		if !bundle.SameCanonical(states[0], resolved.Get("ancestor")) || !bundle.SameCanonical(states[len(states)-1], resolved.Get("descendant")) {
			add("resolved range endpoints do not match the first and last state")
		}
	}

	runIDs, executionIDs, nonces := map[string]bool{}, map[string]bool{}, map[string]bool{}
	attemptsByState := map[float64]map[float64]bool{}
	commandDigestValue := commandDigest(frozen.Get("proposal", "witness", "command").Str())
	for _, run := range runs {
		runID := run.Get("runId").Str()
		if runID != expectedRunID(run) {
			add("runId does not match the canonical run fact: " + runID)
		}
		if run.Get("executionId").Str() != expectedExecutionID(run) {
			add("executionId does not match its state and attempt: " + runID)
		}
		if runIDs[runID] {
			add("duplicate runId: " + runID)
		}
		if executionIDs[run.Get("executionId").Str()] {
			add("duplicate executionId: " + run.Get("executionId").Str())
		}
		if nonces[run.Get("executionNonce").Str()] {
			add("duplicate execution nonce: " + run.Get("executionNonce").Str())
		}
		runIDs[runID] = true
		executionIDs[run.Get("executionId").Str()] = true
		nonces[run.Get("executionNonce").Str()] = true
		state, ok := stateByIndex[run.Get("stateIndex").Num()]
		if !ok || state.Get("commit").Str() != run.Get("commit").Str() || state.Get("tree").Str() != run.Get("tree").Str() {
			add("run state does not match the state sequence: " + runID)
		}
		if run.Get("frozenDigest").Str() != frozen.Get("frozenDigest").Str() || run.Get("witnessDigest").Str() != frozen.Get("witnessDigest").Str() {
			add("run witness digest does not match the frozen witness: " + runID)
		}
		if run.Get("sandbox", "kind").Str() != run.Get("result", "kind").Str() {
			add("run sandbox kind does not match result kind: " + runID)
		}
		for _, auditError := range ValidateSandboxPlanAudit(run.Get("sandbox")) {
			add("run sandbox audit is invalid: " + runID + ": " + auditError)
		}
		if run.Get("sandbox", "witnessDigest").Str() != frozen.Get("frozenDigest").Str() {
			add("run sandbox witness digest does not match frozen witness: " + runID)
		}
		if run.Get("sandbox", "commandDigest").Str() != commandDigestValue {
			add("run sandbox command digest does not match frozen command bytes: " + runID)
		}
		runtime := run.Get("sandbox", "runtime")
		if !digestPinnedImage.MatchString(runtime.Get("image").Str()) { // runtime.image ?? ""
			add("run sandbox image is not digest-pinned: " + runID)
		}
		strIs := func(v jsjson.Value, want string) bool { return v.Kind() == jsjson.String && v.Str() == want }
		if !strIs(runtime.Get("entrypoint"), "/bin/sh") || !strIs(runtime.Get("network"), "none") || !runtime.Get("rootFilesystemReadOnly").Bool() ||
			!strIs(runtime.Get("user"), "65534:65534") || !runtime.Get("capDropAll").Bool() || !runtime.Get("noNewPrivileges").Bool() || !strIs(runtime.Get("pull"), "never") {
			add("run sandbox runtime policy is not a locked Docker plan: " + runID)
		}
		limits := runtime.Get("limits")
		if limits.Get("timeoutMs").Num() <= 0 || limits.Get("maxOutputBytes").Num() <= 0 || limits.Get("cpuCount").Num() <= 0 ||
			limits.Get("memoryBytes").Num() <= 0 || limits.Get("pidsLimit").Num() <= 0 || limits.Get("tmpfsBytes").Num() <= 0 {
			add("run sandbox limits are invalid: " + runID)
		}
		r := run.Get("result")
		if r.Get("kind").Str() != "DOCKER_ISOLATED" {
			add("non-Docker run cannot support this proof bundle: " + runID)
		}
		if r.Get("executor").Str() != "NATIVE_DOCKER" {
			add("non-native Docker executor cannot support this proof bundle: " + runID)
		}
		verdict, reason, exitCode := r.Get("verdict").Str(), r.Get("reason").Str(), r.Get("exitCode")
		if verdict != "PASS" && verdict != "FAIL" {
			add("non-decisive run cannot support this proof bundle: " + runID)
		}
		exitZero := exitCode.Kind() == jsjson.Number && exitCode.Num() == 0
		if verdict == "PASS" && (reason != "PREDICATE_PASS" || !exitZero) {
			add("PASS run has inconsistent execution result: " + runID)
		}
		if verdict == "FAIL" && (reason != "PREDICATE_FAIL" || exitZero || exitCode.Kind() == jsjson.Null) {
			add("FAIL run has inconsistent execution result: " + runID)
		}
		stateIndex := run.Get("stateIndex").Num()
		attempts := attemptsByState[stateIndex]
		if attempts == nil {
			attempts = map[float64]bool{}
		}
		if attempts[run.Get("executionAttempt").Num()] {
			add("duplicate execution attempt for state " + fmtNum(run.Get("stateIndex")))
		}
		attempts[run.Get("executionAttempt").Num()] = true
		attemptsByState[stateIndex] = attempts
	}
	if len(runs) != len(states)*StableExecutionCount {
		add("completed proof investigation does not contain exactly three runs per state")
	}
	for _, state := range states {
		attempts := attemptsByState[state.Get("index").Num()]
		if attempts == nil || len(attempts) != StableExecutionCount || !attempts[1] || !attempts[2] || !attempts[3] {
			add("state " + fmtNum(state.Get("index")) + " does not contain attempts 1, 2, and 3 exactly once")
		}
	}

	stableStates := reconstructStableStates(result)
	if !bundle.SameCanonical(result.Get("stableStates"), jsjson.MakeArray(stableStates)) {
		add("persisted stable states contradict the reconstructed run facts")
	}
	transitions := reconstructTransitions(stableStates)
	if !bundle.SameCanonical(result.Get("transitions"), jsjson.MakeArray(transitions)) {
		add("persisted transitions contradict the reconstructed stable states")
	}
	hasKind := func(kind string) bool {
		return slices.ContainsFunc(transitions, func(t jsjson.Value) bool { return t.Get("kind").Str() == kind })
	}
	if result.Get("nonMonotonic").Bool() != (hasKind("PASS_TO_FAIL") && hasKind("FAIL_TO_PASS")) {
		add("persisted nonMonotonic flag contradicts reconstructed transitions")
	}
	if proof.Get("proofTransitions").Num() != float64(len(transitions)) {
		add("proof transition count contradicts reconstructed transitions")
	}

	env := result.Get("environment")
	heterogeneous := env.Get("homogeneity").Str() == "HETEROGENEOUS"
	findImage := func(commit string) jsjson.Value {
		for _, run := range runs {
			if run.Get("commit").Str() == commit {
				return run.Get("sandbox", "runtime", "image")
			}
		}
		return jsjson.Value{}
	}
	heterogeneousMapped := false
	if heterogeneous {
		fingerprints := env.Get("fingerprints").Items()
		heterogeneousMapped = all(fingerprints, func(entry jsjson.Value) bool {
			image := findImage(entry.Get("commit").Str())
			return image.Kind() == jsjson.String && imageDigestSuffix.MatchString(image.Str())
		})
		if heterogeneousMapped {
			images := map[string]bool{}
			for _, entry := range fingerprints {
				images[imageKey(findImage(entry.Get("commit").Str()))] = true
			}
			heterogeneousMapped = len(images) >= min(2, len(env.Get("distinctDigests").Items()))
		}
	}
	expectedProof := proof.Get("dockerIsolated").Bool() && proof.Get("executionTrust").Str() == "NATIVE_DOCKER" &&
		result.Get("status").Str() == "COMPLETED" && len(transitions) > 0 && (!heterogeneous || heterogeneousMapped)
	if proof.Get("isProof").Bool() != expectedProof {
		add("proof isProof flag contradicts reconstructed transitions, status, and environment homogeneity")
	}
	if expectedProof && !heterogeneous && proof.Get("reason").Str() != "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states." {
		add("proof reason does not match a completed Docker transition proof")
	}
	if expectedProof && heterogeneous && !strings.Contains(proof.Get("reason").Str(), "per-fingerprint runtime mapping") {
		add("heterogeneous proof reason must cite per-fingerprint runtime mapping")
	}
	if heterogeneous && proof.Get("isProof").Bool() && !heterogeneousMapped {
		add("heterogeneous environment fingerprints cannot certify proof without per-state digest-pinned images")
	}
	if expectedProof && proof.Get("evidenceGrade").Str() != "COMMIT_PROOF" {
		add("proof evidenceGrade must be COMMIT_PROOF when isProof is true")
	}
	if !expectedProof && proof.Get("evidenceGrade").Str() == "COMMIT_PROOF" {
		add("COMMIT_PROOF evidence grade requires a completed Docker-isolated proof")
	}
	return errs
}
