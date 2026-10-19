package gitinv

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/evidence"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/schema"
)

// Port of src/git-investigation.ts:62-68.
const (
	GitInvestigationSchemaVersion = gitproof.GitInvestigationSchemaVersion
	StableExecutionCount          = gitproof.StableExecutionCount
	evidenceLogPreviewBytes       = 16 * 1024
)

// GIT_OBJECT_ID (src/git-investigation.ts:66).
var gitObjectID = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

// commitState is a GitCommitState (src/git-investigation.ts:124).
type commitState struct {
	index        int
	commit, tree string
}

func (s commitState) value() jsjson.Value {
	return jsjson.MustObject("index", jsjson.MakeNumber(float64(s.index)), "commit", jsjson.MakeString(s.commit), "tree", jsjson.MakeString(s.tree))
}

func stringArray(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, s := range items {
		values[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(values)
}

// attempt runs fn as the body of a JS try block: a thrown exception (jsexc)
// and a returned error are both what the catch clause sees.
func attempt(fn func() error) (err error) {
	if thrown := jsexc.Try(func() { err = fn() }); thrown != nil {
		return thrown
	}
	return err
}

// sha256TextDigest ports src/git-investigation.ts:300 sha256Digest for a
// JS string (hashed as UTF-8).
func sha256TextDigest(value string) string { return "sha256:" + canonical.SHA256Hex(value) }

// evidencePreview is the result of redactedEvidencePreview.
type evidencePreview struct {
	value               string
	truncated, redacted bool
}

// redactedEvidencePreview ports src/git-investigation.ts:304
// redactedEvidencePreview: the first 16 KiB of the UTF-8 bytes, decoded
// lossily, then redacted.
func redactedEvidencePreview(value, stream string) evidencePreview {
	b := []byte(jsstr.ToUTF8(value))
	preview := jsstr.DecodeUTF8(b[:min(len(b), evidenceLogPreviewBytes)])
	safe, report := redaction.RedactText(preview, "$.run."+stream)
	return evidencePreview{safe, len(b) > evidenceLogPreviewBytes, report.Redacted}
}

// resolveRepositoryRoot ports src/git-investigation.ts:338
// resolveRepositoryRoot (gitText at :329 is hardenedGitText's body).
func resolveRepositoryRoot(repository string) (string, error) {
	requested := nodefs.Resolve(repository)
	root, err := HardenedGitText(requested, []string{"rev-parse", "--show-toplevel"})
	if err != nil {
		return "", err
	}
	if !nodefs.IsAbsolute(root) {
		return "", errors.New("Git did not return an absolute repository root.")
	}
	return nodefs.Resolve(root), nil
}

// resolveCommit ports src/git-investigation.ts:345 resolveCommit.
func resolveCommit(repository, revision string) (string, error) {
	value, err := HardenedGitText(repository, []string{"rev-parse", "--verify", "--end-of-options", revision + "^{commit}"})
	if err != nil {
		return "", err
	}
	if !gitObjectID.MatchString(value) {
		return "", errors.New(jsexc.Concat("Git returned an invalid commit object id for revision ", revision, "."))
	}
	return value, nil
}

// resolveTree ports src/git-investigation.ts:351 resolveTree.
func resolveTree(repository, commit string) (string, error) {
	value, err := HardenedGitText(repository, []string{"rev-parse", "--verify", "--end-of-options", commit + "^{tree}"})
	if err != nil {
		return "", err
	}
	if !gitObjectID.MatchString(value) {
		return "", errors.New(jsexc.Concat("Git returned an invalid tree object id for commit ", commit, "."))
	}
	return value, nil
}

// parseIntDecimal is Number.parseInt(text, 10) on an already-trimmed
// string: an optional sign and the leading decimal digits; NaN without any.
func parseIntDecimal(text string) float64 {
	sign, rest := 1.0, text
	if rest != "" && (rest[0] == '+' || rest[0] == '-') {
		if rest[0] == '-' {
			sign = -1
		}
		rest = rest[1:]
	}
	n := 0
	for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	v, _ := strconv.ParseFloat(rest[:n], 64)
	return sign * v
}

// isSafeInteger is Number.isSafeInteger.
func isSafeInteger(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v) && math.Abs(v) <= 9007199254740991
}

// splitLines is `text.split(/\r?\n/).filter(Boolean)`.
func splitLines(text string) []string {
	var out []string
	for _, line := range lineBreak.Split(text, -1) {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// resolveCommitRange ports src/git-investigation.ts:357 resolveCommitRange.
func resolveCommitRange(repository, ancestor, descendant string, maxStates int) ([]commitState, error) {
	ancestorCommit, err := resolveCommit(repository, ancestor)
	if err != nil {
		return nil, err
	}
	descendantCommit, err := resolveCommit(repository, descendant)
	if err != nil {
		return nil, err
	}
	ancestorCheck := RunHardenedGit([]string{"merge-base", "--is-ancestor", ancestorCommit, descendantCommit}, HardenedOptions{Repository: repository})
	if ancestorCheck.ExitCode == nil || *ancestorCheck.ExitCode != 0 {
		if ancestorCheck.ExitCode != nil && *ancestorCheck.ExitCode == 1 {
			return nil, errors.New("Range ancestor " + ancestorCommit + " is not an ancestor of descendant " + descendantCommit + ".")
		}
		return nil, errors.New(jsexc.Concat("Unable to verify ancestry: ", ancestorCheck.detail("Git failed.")))
	}

	limit := strconv.Itoa(maxStates)
	tooLarge := errors.New("Commit range exceeds the configured maxStates limit of " + limit + ".")
	// One more than the cap makes a too-large range explicit rather than silently truncated.
	listed, err := HardenedGitText(repository, []string{
		"rev-list",
		"--reverse",
		"--ancestry-path",
		"--max-count=" + limit,
		"--end-of-options",
		ancestorCommit + ".." + descendantCommit,
	})
	if err != nil {
		return nil, err
	}
	descendants := splitLines(listed)
	commits := []string{ancestorCommit}
	if ancestorCommit != descendantCommit {
		commits = append(commits, descendants...)
	}
	if len(commits) > maxStates {
		return nil, tooLarge
	}
	if ancestorCommit != descendantCommit && len(descendants) == maxStates {
		overflow, err := HardenedGitText(repository, []string{
			"rev-list",
			"--count",
			"--ancestry-path",
			"--end-of-options",
			ancestorCommit + ".." + descendantCommit,
		})
		if err != nil {
			return nil, err
		}
		count := parseIntDecimal(overflow)
		if !isSafeInteger(count) || count+1 > float64(maxStates) {
			return nil, tooLarge
		}
	}
	for _, commit := range commits {
		if !gitObjectID.MatchString(commit) {
			return nil, errors.New("Git range enumeration returned an invalid commit object id.")
		}
	}
	states := make([]commitState, 0, len(commits))
	for index, commit := range commits {
		tree, err := resolveTree(repository, commit)
		if err != nil {
			return nil, err
		}
		states = append(states, commitState{index, commit, tree})
	}
	return states, nil
}

// planRequestForState ports src/git-investigation.ts:407
// planRequestForState: sandboxValue is the parsed sandbox with its image
// replaced by the fingerprint's.
func planRequestForState(sandboxValue jsjson.Value, worktree string, frozenWitness jsjson.Value) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("witness", jsjson.MustObject(
		"digest", frozenWitness.Get("frozenDigest"),
		"command", frozenWitness.Get("proposal", "witness", "command"),
	))
	o.Set("sourceDirectory", jsjson.MakeString(worktree))
	o.Set("mode", sandboxValue.Get("mode"))
	for _, key := range []string{"image", "environment", "allowedEnvironment", "limits"} {
		if v := sandboxValue.Get(key); v.Kind() != jsjson.Undefined {
			o.Set(key, v)
		}
	}
	return jsjson.MakeObject(o)
}

func isPredicateReason(reason string) bool {
	return reason == "PREDICATE_PASS" || reason == "PREDICATE_FAIL"
}

// stableStates ports src/git-investigation.ts:423 stableStates for a
// Docker-isolated sandbox (the only mode a request can carry).
func stableStates(states []commitState, runs []jsjson.Value) []jsjson.Value {
	stable := []jsjson.Value{}
	for _, state := range states {
		var stateRuns []jsjson.Value
		for _, run := range runs {
			if run.Get("stateIndex").Num() == float64(state.index) {
				stateRuns = append(stateRuns, run)
			}
		}
		slices.SortStableFunc(stateRuns, func(a, b jsjson.Value) int {
			x, y := a.Get("executionAttempt").Num(), b.Get("executionAttempt").Num()
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		})
		if len(stateRuns) != StableExecutionCount {
			continue
		}
		attempts, executionIDs := map[float64]bool{}, map[string]bool{}
		for _, run := range stateRuns {
			attempts[run.Get("executionAttempt").Num()] = true
			executionIDs[run.Get("executionId").Str()] = true
		}
		if len(attempts) != StableExecutionCount || len(executionIDs) != StableExecutionCount {
			continue
		}
		verdict := stateRuns[0].Get("result", "verdict").Str()
		reason := stateRuns[0].Get("result", "reason").Str()
		// Proof-grade localization requires structured PREDICATE_* outcomes only.
		if verdict != "PASS" && verdict != "FAIL" || !isPredicateReason(reason) {
			continue
		}
		consistent := true
		for _, run := range stateRuns {
			result := run.Get("result")
			if result.Get("kind").Str() != "DOCKER_ISOLATED" || result.Get("verdict").Str() != verdict || !isPredicateReason(result.Get("reason").Str()) {
				consistent = false
				break
			}
		}
		if !consistent {
			continue
		}
		executionIDValues := make([]jsjson.Value, len(stateRuns))
		runIDValues := make([]jsjson.Value, len(stateRuns))
		for i, run := range stateRuns {
			executionIDValues[i] = run.Get("executionId")
			runIDValues[i] = run.Get("runId")
		}
		stable = append(stable, jsjson.MustObject(
			"stateIndex", jsjson.MakeNumber(float64(state.index)),
			"commit", jsjson.MakeString(state.commit),
			"tree", jsjson.MakeString(state.tree),
			"verdict", jsjson.MakeString(verdict),
			"executionIds", jsjson.MakeArray(executionIDValues),
			"runIds", jsjson.MakeArray(runIDValues),
		))
	}
	return stable
}

// findTransitions ports src/git-investigation.ts:460 findTransitions.
func findTransitions(states []jsjson.Value) []jsjson.Value {
	output := []jsjson.Value{}
	for index := 1; index < len(states); index++ {
		before, after := states[index-1], states[index]
		if after.Get("stateIndex").Num() != before.Get("stateIndex").Num()+1 || before.Get("verdict").Str() == after.Get("verdict").Str() {
			continue
		}
		kind := "FAIL_TO_PASS"
		if before.Get("verdict").Str() == "PASS" {
			kind = "PASS_TO_FAIL"
		}
		output = append(output, jsjson.MustObject("kind", jsjson.MakeString(kind), "before", before, "after", after))
	}
	return output
}

// hasNonMonotonicTransitions ports src/git-investigation.ts:475.
func hasNonMonotonicTransitions(transitions []jsjson.Value) bool {
	passToFail, failToPass := false, false
	for _, t := range transitions {
		switch t.Get("kind").Str() {
		case "PASS_TO_FAIL":
			passToFail = true
		case "FAIL_TO_PASS":
			failToPass = true
		}
	}
	return passToFail && failToPass
}

// witnessSummary ports src/git-investigation.ts:480 witnessSummary.
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

// executionTrustFor ports src/git-investigation.ts:491 executionTrustFor
// for DOCKER_ISOLATED, the only sandbox mode a request can carry.
func executionTrustFor(hasInjectedRunner bool) string {
	if hasInjectedRunner {
		return "INJECTED_RUNNER"
	}
	return "NATIVE_DOCKER"
}

// proofValue is the proof object of src/git-investigation.ts:496 and :846.
func proofValue(executionTrust string, proofTransitions int, isProof bool, reason string) jsjson.Value {
	grade, label := evidence.CommitPathEvidence(isProof)
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

// emptyProof ports src/git-investigation.ts:496 emptyProof.
func emptyProof(reason, executionTrust string) jsjson.Value {
	return proofValue(executionTrust, 0, false, reason)
}

// resultFields are the fields baseResult takes from its caller.
type resultFields struct{ repository, requestedRange, resolvedRange, witness jsjson.Value }

// baseResult ports src/git-investigation.ts:514 baseResult.
func baseResult(status string, errs []string, f resultFields, executionTrust string) (jsjson.Value, error) {
	reason := "Investigation did not produce proof."
	if len(errs) > 0 {
		reason = errs[0]
	}
	return bundle.ParseValue(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(GitInvestigationSchemaVersion),
		"recorder", jsjson.MakeString("git-commit-range-replay"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"status", jsjson.MakeString(status),
		"repository", f.repository,
		"requestedRange", f.requestedRange,
		"resolvedRange", f.resolvedRange,
		"witness", f.witness,
		"executionsPerState", jsjson.MakeNumber(StableExecutionCount),
		"states", jsjson.MakeArray(nil),
		"runs", jsjson.MakeArray(nil),
		"stableStates", jsjson.MakeArray(nil),
		"transitions", jsjson.MakeArray(nil),
		"nonMonotonic", jsjson.MakeBool(false),
		"environment", jsjson.MustObject(
			"homogeneity", jsjson.MakeString(Empty),
			"fingerprints", jsjson.MakeArray(nil),
			"distinctDigests", jsjson.MakeArray(nil),
		),
		"proof", emptyProof(reason, executionTrust),
		"errors", stringArray(errs),
	), gitproof.GitInvestigationResultSchema)
}

// runFact ports src/git-investigation.ts:543 runFact. Its randomUUID is
// the read after the attempt's finishing Date.now().
func runFact(state commitState, executionAttempt int, frozenWitness, overlays, sandboxAudit, result jsjson.Value,
	startedAt, finishedAt string, durationMs int64) (jsjson.Value, error) {
	executionNonce := clock.NewUUID()
	executionID, err := canonical.DigestJSON(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(GitInvestigationSchemaVersion),
		"executionNonce", jsjson.MakeString(executionNonce),
		"stateIndex", jsjson.MakeNumber(float64(state.index)),
		"commit", jsjson.MakeString(state.commit),
		"tree", jsjson.MakeString(state.tree),
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
		{"schemaVersion", jsjson.MakeString(GitInvestigationSchemaVersion)},
		{"executionId", jsjson.MakeString(executionID)},
		{"executionNonce", jsjson.MakeString(executionNonce)},
		{"stateIndex", jsjson.MakeNumber(float64(state.index))},
		{"executionAttempt", jsjson.MakeNumber(float64(executionAttempt))},
		{"commit", jsjson.MakeString(state.commit)},
		{"tree", jsjson.MakeString(state.tree)},
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
			"stdoutDigest", jsjson.MakeString(sha256TextDigest(stdout)),
			"stdoutBytes", jsjson.MakeNumber(float64(len(jsstr.ToUTF8(stdout)))),
			"stdoutPreview", jsjson.MakeString(stdoutPreview.value),
			"stdoutPreviewTruncated", jsjson.MakeBool(stdoutPreview.truncated),
			"stdoutRedacted", jsjson.MakeBool(stdoutPreview.redacted),
			"stderrDigest", jsjson.MakeString(sha256TextDigest(stderr)),
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
	return bundle.ParseValue(jsjson.MakeObject(unsigned), gitproof.GitInvestigationRunFactSchema)
}

// classifyStatus ports src/git-investigation.ts:604 classifyStatus.
func classifyStatus(runs []jsjson.Value, errs []string) string {
	if len(errs) > 0 {
		return "EXECUTION_ERROR"
	}
	for _, run := range runs {
		if run.Get("result", "reason").Str() == "SANDBOX_UNAVAILABLE" {
			return "SANDBOX_UNAVAILABLE"
		}
	}
	for _, run := range runs {
		if run.Get("result", "verdict").Str() == "ERROR" {
			return "EXECUTION_ERROR"
		}
	}
	return "COMPLETED"
}

// runtimeMappingOf reads the request's runtimeMapping object (a TS
// Record<string, string>) in key order; an absent one is {}.
func runtimeMappingOf(v jsjson.Value) []MappingEntry {
	var mapping []MappingEntry
	if v.Kind() != jsjson.Object {
		return mapping
	}
	for _, key := range v.Obj().Keys() {
		if image := v.Obj().Field(key); image.Kind() == jsjson.String {
			mapping = append(mapping, MappingEntry{key, image.Str()})
		}
	}
	return mapping
}

// distinctSorted is `[...new Set(values)].sort()` (UTF-16 code-unit order).
func distinctSorted(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	slices.SortFunc(out, jsstr.CompareUTF16)
	return out
}

// isoMillis is new Date(ms).toISOString().
func isoMillis(ms int64) string { return clock.ISO(time.UnixMilli(ms)) }

// InvestigateGitRange ports src/git-investigation.ts:617
// investigateGitRange. request is the JSON form of GitInvestigationRequest
// without its runner: runtimeMapping, when present, is an object of
// fingerprint digest to image strings (other values are skipped, as no TS
// caller passes them). runner nil is the native Docker runner; injected
// marks a caller-supplied runner (request.runner !== undefined). The
// result is the schema-parsed GitInvestigationResult; an error is what the
// TS promise rejects with.
func InvestigateGitRange(request jsjson.Value, runner sandbox.Runner, injected bool) (result jsjson.Value, err error) {
	if thrown := jsexc.Try(func() { result, err = investigate(request, runner, injected) }); thrown != nil {
		return jsjson.Value{}, thrown
	}
	return result, err
}

func investigate(request jsjson.Value, runner sandbox.Runner, injected bool) (jsjson.Value, error) {
	if runner == nil {
		runner = sandbox.NativeRunner()
	}
	raw := jsjson.NewObj()
	for _, key := range []string{"repository", "range", "frozenWitness", "expectedFrozenDigest", "sandbox", "maxStates"} {
		if v := request.Get(key); v.Kind() != jsjson.Undefined {
			raw.Set(key, v)
		}
	}
	trust := executionTrustFor(injected)
	input, issues, ok := schema.Parse(gitproof.GitInvestigationRequestSchema, jsjson.MakeObject(raw))
	if !ok {
		null := jsjson.MakeNull()
		return baseResult("INVALID_REQUEST", []string{"Invalid Git investigation request: " + schema.ErrorMessage(issues)},
			resultFields{null, null, null, null}, trust)
	}

	frozenWitness := input.Get("frozenWitness")
	requestedRange := input.Get("range")
	verification := gitproof.VerifyFrozenWitnessRecord(frozenWitness, input.Get("expectedFrozenDigest").Str(), true)
	summary := witnessSummary(frozenWitness, verification)
	if !verification.Valid || verification.ExternalDigestStatus != "MATCH" {
		errs := verification.Errors
		if len(errs) == 0 {
			errs = []string{"Frozen witness did not match the externally supplied digest."}
		}
		return baseResult("INVALID_WITNESS", errs, resultFields{jsjson.MakeNull(), requestedRange, jsjson.MakeNull(), summary}, trust)
	}

	var repository string
	var states []commitState
	err := attempt(func() error {
		var err error
		if repository, err = resolveRepositoryRoot(input.Get("repository").Str()); err != nil {
			return err
		}
		if states, err = resolveCommitRange(repository, requestedRange.Get("ancestor").Str(), requestedRange.Get("descendant").Str(),
			int(input.Get("maxStates").Num())); err != nil {
			return err
		}
		commits := make([]string, len(states))
		for i, state := range states {
			commits[i] = state.commit
		}
		return AssertSafeGitMaterialization(repository, commits)
	})
	if err != nil {
		return baseResult("RANGE_ERROR", []string{jsexc.Message(err)}, resultFields{jsjson.MakeNull(), requestedRange, jsjson.MakeNull(), summary}, trust)
	}
	if len(states) == 0 {
		return baseResult("RANGE_ERROR", []string{"Git range resolved to no states."},
			resultFields{jsjson.MakeString(repository), requestedRange, jsjson.MakeNull(), summary}, trust)
	}
	first, last := states[0], states[len(states)-1]
	resolvedRange := jsjson.MustObject("ancestor", first.value(), "descendant", last.value())
	fields := resultFields{jsjson.MakeString(repository), requestedRange, resolvedRange, summary}

	tempRoot, err := nodefs.MkdtempAsync(nodefs.Join(nodefs.Tmpdir(), "faultline-git-investigation-"))
	if err != nil {
		return jsjson.Value{}, err
	}
	var (
		runs                []jsjson.Value
		errs                []string
		fingerprints        []jsjson.Value
		fingerprintByCommit = map[string]jsjson.Value{}
		effective           []MappingEntry
		configurationError  bool
		early               *jsjson.Value
	)
	pushError := func(err error) { errs = append(errs, jsexc.Message(err)) }
	cleanup := func(tree *MaterializedTree) {
		if tree != nil {
			if err := tree.Cleanup(); err != nil {
				pushError(err)
			}
		}
	}
	bodyErr := attempt(func() error {
		// Phase 1: fingerprint every historical state before executing the witness.
		// This prevents a heterogeneous range from silently using one image.
		for _, state := range states {
			var tree *MaterializedTree
			if err := attempt(func() error {
				m, err := MaterializeGitTree(repository, state.commit, tempRoot, fmt.Sprintf("fp-%d-%s", state.index, state.commit[:16]))
				if err != nil {
					return err
				}
				tree = &m
				fingerprint, err := ComputeFingerprint(m.Worktree)
				if err != nil {
					return err
				}
				fingerprintByCommit[state.commit] = fingerprint
				return nil
			}); err != nil {
				pushError(err)
			}
			cleanup(tree)
		}
		if len(fingerprintByCommit) != len(states) {
			v, err := baseResult("EXECUTION_ERROR", errs, fields, trust)
			early = &v
			return err
		}
		preflight := make([]string, len(states))
		for i, state := range states {
			preflight[i] = fingerprintByCommit[state.commit].Get("digest").Str()
		}
		homogeneity := Homogeneity(preflight)
		mapping := runtimeMappingOf(request.Get("runtimeMapping"))
		if homogeneity == Heterogeneous && len(MissingRuntimeMappingDigests(preflight, mapping)) > 0 {
			v, err := baseResult("CONFIGURATION_ERROR", []string{EnvironmentChangedProofMessage}, fields, trust)
			early = &v
			return err
		}
		var err error
		if effective, err = BuildEffectiveRuntimeMapping(preflight, mapping, input.Get("sandbox", "image").Str(), homogeneity); err != nil {
			return err
		}
		for _, state := range states {
			var tree *MaterializedTree
			if err := attempt(func() error {
				m, err := MaterializeGitTree(repository, state.commit, tempRoot, fmt.Sprintf("state-%04d-%s", state.index, state.commit[:16]))
				if err != nil {
					return err
				}
				tree = &m
				fingerprint, err := ComputeFingerprint(m.Worktree)
				if err != nil {
					return err
				}
				fingerprints = append(fingerprints, jsjson.MustObject(
					"stateIndex", jsjson.MakeNumber(float64(state.index)),
					"commit", jsjson.MakeString(state.commit),
					"fingerprint", fingerprint,
				))
				overlays, err := MaterializeFrozenOverlays(m.Worktree, frozenWitness, m)
				if err != nil {
					return err
				}
				var plan sandbox.Plan
				if err := attempt(func() error {
					preflightFingerprint, ok := fingerprintByCommit[state.commit]
					if !ok {
						return errors.New("Missing preflight environment fingerprint for " + state.commit + ".")
					}
					digest := preflightFingerprint.Get("digest").Str()
					image, ok := lookup(effective, digest)
					if !ok || image == "" {
						return errors.New("Missing runtime image mapping for " + digest + ".")
					}
					sandboxValue := jsjson.NewObj()
					for _, key := range input.Get("sandbox").Obj().Keys() {
						sandboxValue.Set(key, input.Get("sandbox", key))
					}
					sandboxValue.Set("image", jsjson.MakeString(image))
					var err error
					plan, err = sandbox.CreatePlan(planRequestForState(jsjson.MakeObject(sandboxValue), m.Worktree, frozenWitness))
					return err
				}); err != nil {
					configurationError = true
					return errors.New(jsexc.Concat("Could not create sandbox plan for ", state.commit, ": ", jsexc.Message(err)))
				}
				audit := sandbox.AuditPlan(plan)
				for executionAttempt := 1; executionAttempt <= StableExecutionCount; executionAttempt++ {
					startedEpoch := clock.NowMillis()
					startedAt := isoMillis(startedEpoch)
					execution := sandbox.Execute(plan, runner, injected)
					finishedEpoch := clock.NowMillis()
					fact, err := runFact(state, executionAttempt, frozenWitness, overlays, audit, execution,
						startedAt, isoMillis(finishedEpoch), max(0, finishedEpoch-startedEpoch))
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
			// A malformed plan is global configuration, so no later worktree could
			// make it valid. Stop before generating a misleading partial timeline.
			if configurationError {
				break
			}
		}
		return nil
	})
	// mkdtemp created this exact directory beneath the OS temp root, never a caller-provided path.
	if err := nodefs.RemoveAllAsync(tempRoot); err != nil {
		return jsjson.Value{}, err
	}
	if bodyErr != nil {
		return jsjson.Value{}, bodyErr
	}
	if early != nil {
		return *early, nil
	}

	fingerprintDigests := make([]string, len(fingerprints))
	for i, entry := range fingerprints {
		fingerprintDigests[i] = entry.Get("fingerprint", "digest").Str()
	}
	homogeneity := Homogeneity(fingerprintDigests)
	distinctDigests := stringArray(distinctSorted(fingerprintDigests))
	statesValue := make([]jsjson.Value, len(states))
	for i, state := range states {
		statesValue[i] = state.value()
	}
	if configurationError {
		reason := "Sandbox configuration failed."
		if len(errs) > 0 {
			reason = errs[0]
		}
		return bundle.ParseValue(jsjson.MustObject(
			"schemaVersion", jsjson.MakeString(GitInvestigationSchemaVersion),
			"recorder", jsjson.MakeString("git-commit-range-replay"),
			"nativeCodexInterception", jsjson.MakeBool(false),
			"status", jsjson.MakeString("CONFIGURATION_ERROR"),
			"repository", fields.repository,
			"requestedRange", requestedRange,
			"resolvedRange", resolvedRange,
			"witness", summary,
			"executionsPerState", jsjson.MakeNumber(StableExecutionCount),
			"states", jsjson.MakeArray(statesValue),
			"runs", jsjson.MakeArray(runs),
			"stableStates", jsjson.MakeArray(nil),
			"transitions", jsjson.MakeArray(nil),
			"nonMonotonic", jsjson.MakeBool(false),
			"environment", jsjson.MustObject(
				"homogeneity", jsjson.MakeString(homogeneity),
				"fingerprints", jsjson.MakeArray(fingerprints),
				"distinctDigests", distinctDigests,
			),
			"proof", emptyProof(reason, trust),
			"errors", stringArray(errs),
		), gitproof.GitInvestigationResultSchema)
	}

	stable := stableStates(states, runs)
	transitions := findTransitions(stable)
	status := classifyStatus(runs, errs)
	dockerIsolated := trust == "NATIVE_DOCKER"
	environmentChanged := homogeneity == Heterogeneous
	mappingComplete := !environmentChanged || len(MissingRuntimeMappingDigests(fingerprintDigests, effective)) == 0
	var proofReason string
	switch {
	case trust == "INJECTED_RUNNER":
		proofReason = "An injected runner produced these observations; FaultLine refuses to certify it as native Docker proof."
	case !dockerIsolated:
		proofReason = "Unsafe local execution is explicitly inapplicable and cannot be proof."
	case status == "SANDBOX_UNAVAILABLE":
		proofReason = "Docker was unavailable; no execution result is proof."
	case status == "EXECUTION_ERROR":
		proofReason = "An execution or worktree error prevents an investigation-wide proof claim."
	case environmentChanged && !mappingComplete:
		proofReason = "Environment descriptors (lockfiles/toolchains) changed across the investigated range; FaultLine refuses a single-image proof grade. Provide per-fingerprint runtimes or narrow the range."
	case len(transitions) == 0:
		proofReason = "No adjacent states produced three matching Docker PASS/FAIL executions."
	case environmentChanged:
		proofReason = "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states with a complete per-fingerprint runtime mapping."
	default:
		proofReason = "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states."
	}
	isProof := dockerIsolated && status == "COMPLETED" && len(transitions) > 0 && mappingComplete
	return bundle.ParseValue(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(GitInvestigationSchemaVersion),
		"recorder", jsjson.MakeString("git-commit-range-replay"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"status", jsjson.MakeString(status),
		"repository", fields.repository,
		"requestedRange", requestedRange,
		"resolvedRange", resolvedRange,
		"witness", summary,
		"executionsPerState", jsjson.MakeNumber(StableExecutionCount),
		"states", jsjson.MakeArray(statesValue),
		"runs", jsjson.MakeArray(runs),
		"stableStates", jsjson.MakeArray(stable),
		"transitions", jsjson.MakeArray(transitions),
		"nonMonotonic", jsjson.MakeBool(hasNonMonotonicTransitions(transitions)),
		"environment", jsjson.MustObject(
			"homogeneity", jsjson.MakeString(homogeneity),
			"fingerprints", jsjson.MakeArray(fingerprints),
			"distinctDigests", distinctDigests,
		),
		"proof", proofValue(trust, len(transitions), isProof, proofReason),
		"errors", stringArray(errs),
	), gitproof.GitInvestigationResultSchema)
}
