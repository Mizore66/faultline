package minimize

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	"github.com/Mizore66/faultline/internal/schema"
)

var sha256DigestPattern = regexp.MustCompile(sha256DigestSource)

// roleSemantics ports ROLE_SEMANTICS (src/git-minimization.ts:1504).
var roleSemantics = map[string][2]string{
	"BASELINE_BEFORE":           {"BASELINE", "FORWARD_FROM_BEFORE"},
	"FULL_PATCH":                {"FULL_RANGE", "FORWARD_FROM_BEFORE"},
	"DELTA_SUBSET":              {"DELTA_SUBSET", "FORWARD_FROM_BEFORE"},
	"DELTA_COMPLEMENT":          {"DELTA_COMPLEMENT", "FORWARD_FROM_BEFORE"},
	"ENUMERATION":               {"ENUMERATION", "FORWARD_FROM_BEFORE"},
	"ONE_MINIMAL":               {"ONE_MINIMAL", "FORWARD_FROM_BEFORE"},
	"HUNK_ONE_MINIMAL":          {"HUNK_ONE_MINIMAL", "FORWARD_FROM_BEFORE"},
	"SUFFICIENCY_CERTIFICATION": {"SUFFICIENCY_CERTIFICATION", "FORWARD_FROM_BEFORE"},
	"NECESSITY_CERTIFICATION":   {"NECESSITY_CERTIFICATION", "REVERSE_FROM_AFTER"},
}

// Verification ports GitMinimizationVerification (src/git-minimization.ts:368).
// A nil ResultDigest or Result is TS's null or absent member.
type Verification struct {
	Valid                bool
	ResultDigest         *string
	ExternalDigestStatus string
	Result               *jsjson.Value
	Errors               []string
}

// JSON is the whole TS verification object.
func (v Verification) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("valid", jsjson.MakeBool(v.Valid))
	o.Set("errors", stringArray(v.Errors))
	if v.ResultDigest == nil {
		o.Set("resultDigest", jsjson.MakeNull())
	} else {
		o.Set("resultDigest", jsjson.MakeString(*v.ResultDigest))
	}
	o.Set("externalDigestStatus", jsjson.MakeString(v.ExternalDigestStatus))
	if v.Result != nil {
		o.Set("result", *v.Result)
	}
	return jsjson.MakeObject(o)
}

// externalDigestStatus ports src/git-minimization.ts:1519.
func externalDigestStatus(expected string, provided bool, resultDigest *string, errs *[]string) string {
	if !provided {
		return "NOT_PROVIDED"
	}
	if !sha256DigestPattern.MatchString(expected) {
		*errs = append(*errs, "externally supplied minimization digest is not a sha256 digest")
		return "MISMATCH"
	}
	if resultDigest == nil || expected != *resultDigest {
		*errs = append(*errs, "externally supplied minimization digest does not match")
		return "MISMATCH"
	}
	return "MATCH"
}

// validateCandidateIds ports src/git-minimization.ts:1535.
func validateCandidateIDs(ids []string, units map[string]jsjson.Value, errs *[]string, label string) {
	seen := map[string]bool{}
	previousOrdinal := -1.0
	for _, id := range ids {
		if seen[id] {
			*errs = append(*errs, jsexc.Concat(label, " contains duplicate patch unit id: ", id))
		}
		seen[id] = true
		unit, ok := units[id]
		if !ok {
			*errs = append(*errs, jsexc.Concat(label, " references a patch unit that is not in this result: ", id))
			continue
		}
		ordinal := unit.Get("ordinal").Num()
		if ordinal <= previousOrdinal {
			*errs = append(*errs, label+" patch unit ids are not in canonical ordinal order")
		}
		previousOrdinal = ordinal
	}
}

// expectedOutcomeForRun ports src/git-minimization.ts:1556.
func expectedOutcomeForRun(run jsjson.Value) string {
	result := run.Get("result")
	if result.Kind() != jsjson.Object || result.Get("kind").Str() != "DOCKER_ISOLATED" {
		return "UNRESOLVED"
	}
	switch result.Get("verdict").Str() {
	case "PASS":
		return "PASS"
	case "FAIL":
		return "FAIL"
	}
	return "UNRESOLVED"
}

// validateCertificate ports src/git-minimization.ts:1563.
func validateCertificate(label string, certificate, result jsjson.Value, runs map[string]jsjson.Value, errs *[]string) {
	direction, outcome, role := "FORWARD_FROM_BEFORE", "FAIL", "SUFFICIENCY_CERTIFICATION"
	if label != "sufficiency" {
		direction, outcome, role = "REVERSE_FROM_AFTER", "PASS", "NECESSITY_CERTIFICATION"
	}
	runIDs := bundle.Strings(certificate.Get("runIds"))
	executionIDs := bundle.Strings(certificate.Get("executionIds"))
	if certificate.Get("direction").Str() != direction {
		*errs = append(*errs, label+" certificate has the wrong direction")
	}
	if certificate.Get("expectedOutcome").Str() != outcome {
		*errs = append(*errs, label+" certificate has the wrong expected outcome")
	}
	if len(runIDs) != len(executionIDs) {
		*errs = append(*errs, label+" certificate run and execution id counts differ")
	}
	if certificate.Get("status").Str() == "NOT_RUN" && (len(runIDs) != 0 || len(executionIDs) != 0) {
		*errs = append(*errs, label+" NOT_RUN certificate must not contain execution references")
	}

	completeEvidence := len(runIDs) == CertificationExecutions && len(executionIDs) == CertificationExecutions
	seenRuns, seenExecutions := map[string]bool{}, map[string]bool{}
	for index, runID := range runIDs {
		if seenRuns[runID] {
			*errs = append(*errs, jsexc.Concat(label, " certificate repeats run id: ", runID))
			completeEvidence = false
		}
		seenRuns[runID] = true
		run, ok := runs[runID]
		if !ok {
			*errs = append(*errs, jsexc.Concat(label, " certificate references an absent run: ", runID))
			completeEvidence = false
			continue
		}
		executionID := ""
		if index < len(executionIDs) {
			executionID = executionIDs[index]
		}
		if index >= len(executionIDs) || executionID != run.Get("executionId").Str() {
			*errs = append(*errs, jsexc.Concat(label, " certificate execution id does not match run ", runID))
			completeEvidence = false
		}
		if seenExecutions[run.Get("executionId").Str()] {
			*errs = append(*errs, label+" certificate executions are not distinct")
			completeEvidence = false
		}
		seenExecutions[run.Get("executionId").Str()] = true
		if run.Get("role").Str() != role || run.Get("application", "direction").Str() != direction {
			*errs = append(*errs, jsexc.Concat(label, " certificate references a run with the wrong role or direction: ", runID))
			completeEvidence = false
		}
		if !bundle.SameCanonical(run.Get("candidateUnitIds"), result.Get("candidateUnitIds")) {
			*errs = append(*errs, jsexc.Concat(label, " certificate run does not use the final candidate: ", runID))
			completeEvidence = false
		}
		runResult := run.Get("result")
		if run.Get("outcome").Str() != outcome || runResult.Kind() != jsjson.Object || runResult.Get("kind").Str() != "DOCKER_ISOLATED" ||
			runResult.Get("executor").Str() != "NATIVE_DOCKER" || runResult.Get("verdict").Str() != outcome {
			// A NOT_CERTIFIED record intentionally retains the failed or injected
			// attempt that prevented certification. It is structurally valid but
			// cannot contribute to the three-run native-Docker proof predicate.
			completeEvidence = false
		}
	}

	if certificate.Get("status").Str() == "CERTIFIED" && !completeEvidence {
		*errs = append(*errs, label+" certificate claims CERTIFIED without three distinct matching native Docker runs")
	}
	if certificate.Get("status").Str() != "CERTIFIED" && completeEvidence {
		*errs = append(*errs, label+" certificate with three distinct matching native Docker runs must be CERTIFIED")
	}
}

// proofGradeEvidence ports src/git-minimization.ts:1657.
//
// A proof cannot be reconstructed from repeat certification alone. Retain the
// ordinary search evidence that established its precondition (clean baseline),
// trigger (the full range), and one-minimality of the final candidate.
func proofGradeEvidence(result jsjson.Value, attemptsByRunID map[string]jsjson.Value) (bool, []string) {
	var evidenceErrors []string
	var allUnitIDs []string
	for _, unit := range result.Get("patchUnits").Items() {
		allUnitIDs = append(allUnitIDs, unit.Get("id").Str())
	}

	hasLinkedNativeDockerRun := func(run jsjson.Value, phase string, expectedCandidate []string, expectedOutcome string) bool {
		attempt, ok := attemptsByRunID[run.Get("runId").Str()]
		if !ok {
			return false
		}
		expectedApplicationStatus := "APPLIED"
		if len(expectedCandidate) == 0 {
			expectedApplicationStatus = "NO_PATCHES"
		}
		candidate := stringArray(expectedCandidate)
		runResult := run.Get("result")
		return attempt.Get("phase").Str() == phase &&
			attempt.Get("runId").Str() == run.Get("runId").Str() &&
			attempt.Get("outcome").Str() == expectedOutcome &&
			bundle.SameCanonical(attempt.Get("candidateUnitIds"), candidate) &&
			bundle.SameCanonical(run.Get("candidateUnitIds"), candidate) &&
			bundle.SameCanonical(run.Get("application", "selectedUnitIds"), candidate) &&
			run.Get("application", "direction").Str() == roleSemantics[run.Get("role").Str()][1] &&
			run.Get("application", "status").Str() == expectedApplicationStatus &&
			run.Get("outcome").Str() == expectedOutcome &&
			runResult.Kind() == jsjson.Object &&
			runResult.Get("kind").Str() == "DOCKER_ISOLATED" &&
			runResult.Get("executor").Str() == "NATIVE_DOCKER" &&
			runResult.Get("verdict").Str() == expectedOutcome
	}

	hasEvidence := func(label, role, phase string, expectedCandidate []string, expectedOutcome string) {
		found := false
		for _, run := range result.Get("runs").Items() {
			if run.Get("role").Str() == role && hasLinkedNativeDockerRun(run, phase, expectedCandidate, expectedOutcome) {
				found = true
				break
			}
		}
		if !found {
			evidenceErrors = append(evidenceErrors,
				jsexc.Concat(label, " requires a linked native-Docker ", expectedOutcome, " run with the exact candidate set"))
		}
	}

	candidateUnitIDs := bundle.Strings(result.Get("candidateUnitIds"))
	if len(allUnitIDs) == 0 {
		evidenceErrors = append(evidenceErrors, "proof-grade evidence requires at least one full-range patch unit")
	}
	if len(candidateUnitIDs) == 0 {
		evidenceErrors = append(evidenceErrors, "proof-grade evidence requires a non-empty final candidate")
	}

	hasEvidence("baseline-before [] evidence", "BASELINE_BEFORE", "BASELINE", nil, "PASS")
	hasEvidence("full-patch [all patch unit ids] evidence", "FULL_PATCH", "FULL_RANGE", allUnitIDs, "FAIL")

	for _, unitID := range candidateUnitIDs {
		hasEvidence(jsexc.Concat("one-minimal evidence after removing ", unitID), "ONE_MINIMAL", "ONE_MINIMAL",
			without(candidateUnitIDs, unitID), "PASS")
	}
	return len(evidenceErrors) == 0, evidenceErrors
}

// Verify ports verifyGitMinimizationResult (src/git-minimization.ts:1721):
// offline, self-consistency verification of one stored record. provided is
// whether an external digest was supplied.
func Verify(value jsjson.Value, expected string, provided bool) Verification {
	errs := []string{}
	var resultDigest *string
	var out *Verification
	thrown := jsexc.Try(func() { out = verify(value, expected, provided, &errs, &resultDigest) })
	if thrown != nil {
		errs = append(errs, "minimization result verification failed safely: "+jsexc.Message(thrown))
		status := externalDigestStatus(expected, provided, resultDigest, &errs)
		return Verification{Errors: errs, ResultDigest: resultDigest, ExternalDigestStatus: status}
	}
	return *out
}

func verify(value jsjson.Value, expected string, provided bool, errs *[]string, resultDigest **string) *Verification {
	parsed, issues, ok := schema.Parse(ResultSchema, value)
	if !ok {
		*errs = append(*errs, "minimization result schema validation failed: "+schema.ErrorMessage(issues))
		status := externalDigestStatus(expected, provided, *resultDigest, errs)
		return &Verification{Errors: *errs, ResultDigest: *resultDigest, ExternalDigestStatus: status}
	}
	result := parsed
	digest := digestJSON(result)
	*resultDigest = &digest

	units := map[string]jsjson.Value{}
	for index, unit := range result.Get("patchUnits").Items() {
		id := unit.Get("id").Str()
		if unit.Get("ordinal").Num() != float64(index) {
			*errs = append(*errs, "patch unit ordinal is not contiguous at offset "+strconv.Itoa(index))
		}
		if _, duplicate := units[id]; duplicate {
			*errs = append(*errs, "duplicate patch unit id: "+id)
		}
		units[id] = unit
		bytes := jsstr.DecodeBase64Lenient(unit.Get("pathBytesBase64").Str())
		if base64.StdEncoding.EncodeToString(bytes) != unit.Get("pathBytesBase64").Str() {
			*errs = append(*errs, "patch unit path bytes are not canonical base64: "+id)
		}
		path := jsstr.DecodeUTF8(bytes)
		if jsstr.ToUTF8(path) != string(bytes) || path != unit.Get("path").Str() {
			*errs = append(*errs, "patch unit path does not match its exact UTF-8 bytes: "+id)
		}
		if unit.Get("pathDigest").Str() != sha256BytesDigest(bytes) {
			*errs = append(*errs, "patch unit path digest does not match bytes: "+id)
		}
		expectedID := digestJSON(jsjson.MustObject(
			"schemaVersion", jsjson.MakeString(SchemaVersion),
			"ordinal", unit.Get("ordinal"),
			"pathDigest", unit.Get("pathDigest"),
			"patchDigest", unit.Get("patchDigest"),
		))
		if id != expectedID {
			*errs = append(*errs, "patch unit id does not match ordinal and digests: "+id)
		}
	}
	validateCandidateIDs(bundle.Strings(result.Get("candidateUnitIds")), units, errs, "result candidate")

	runs := map[string]jsjson.Value{}
	executionIDs, executionNonces := map[string]bool{}, map[string]bool{}
	attemptsByRole := map[string][]float64{}
	var roleOrder []string
	for _, run := range result.Get("runs").Items() {
		runID := run.Get("runId").Str()
		if _, duplicate := runs[runID]; duplicate {
			*errs = append(*errs, "duplicate run id: "+runID)
		} else {
			runs[runID] = run
		}
		if runID != expectedRunID(run) {
			*errs = append(*errs, "run id does not match canonical run fact: "+runID)
		}
		if run.Get("executionId").Str() != expectedExecutionID(run) {
			*errs = append(*errs, "execution id does not match nonce-bound run identity: "+runID)
		}
		if executionIDs[run.Get("executionId").Str()] {
			*errs = append(*errs, "duplicate execution id: "+run.Get("executionId").Str())
		}
		executionIDs[run.Get("executionId").Str()] = true
		if executionNonces[run.Get("executionNonce").Str()] {
			*errs = append(*errs, "duplicate execution nonce: "+run.Get("executionNonce").Str())
		}
		executionNonces[run.Get("executionNonce").Str()] = true
		role := run.Get("role").Str()
		if _, seen := attemptsByRole[role]; !seen {
			roleOrder = append(roleOrder, role)
		}
		attemptsByRole[role] = append(attemptsByRole[role], run.Get("roleAttempt").Num())
		if run.Get("application", "direction").Str() != roleSemantics[role][1] {
			*errs = append(*errs, jsexc.Concat("run direction does not match role ", role, ": ", runID))
		}
		candidateUnitIDs := bundle.Strings(run.Get("candidateUnitIds"))
		validateCandidateIDs(candidateUnitIDs, units, errs, "run candidate "+runID)
		if !bundle.SameCanonical(run.Get("application", "selectedUnitIds"), run.Get("candidateUnitIds")) {
			*errs = append(*errs, "run application selected units do not match candidate units: "+runID)
		}
		selectedBytes := 0.0
		for _, id := range candidateUnitIDs {
			if unit, ok := units[id]; ok {
				selectedBytes += unit.Get("patchBytes").Num()
			}
		}
		if run.Get("application", "candidatePatchBytes").Num() != selectedBytes {
			*errs = append(*errs, "run application patch byte count does not match selected patch units: "+runID)
		}
		if len(candidateUnitIDs) == 0 && (run.Get("application", "candidatePatchBytes").Num() != 0 ||
			run.Get("application", "candidatePatchDigest").Str() != sha256BytesDigest(nil)) {
			*errs = append(*errs, "empty candidate application does not use the empty patch digest: "+runID)
		}
		if run.Get("application", "status").Str() == "NO_PATCHES" && len(candidateUnitIDs) != 0 {
			*errs = append(*errs, "NO_PATCHES application has selected patch units: "+runID)
		}
		expectedBase := result.Get("before")
		if run.Get("application", "direction").Str() != "FORWARD_FROM_BEFORE" {
			expectedBase = result.Get("after")
		}
		if expectedBase.Kind() == jsjson.Null || !bundle.SameCanonical(run.Get("application", "base"), expectedBase) {
			*errs = append(*errs, "run application base does not match the recorded direction endpoint: "+runID)
		}
		sandboxAudit, runResult := run.Get("sandbox"), run.Get("result")
		if (sandboxAudit.Kind() == jsjson.Null) != (runResult.Kind() == jsjson.Null) {
			*errs = append(*errs, "run sandbox and execution result must be both present or both absent: "+runID)
		}
		if sandboxAudit.Kind() != jsjson.Null {
			for _, auditError := range gitproof.ValidateSandboxPlanAudit(sandboxAudit) {
				*errs = append(*errs, jsexc.Concat("run sandbox audit is invalid: ", runID, ": ", auditError))
			}
		}
		if runResult.Kind() == jsjson.Null {
			if run.Get("outcome").Str() != "UNRESOLVED" {
				*errs = append(*errs, "run without execution result must be UNRESOLVED: "+runID)
			}
		} else {
			if sandboxAudit.Kind() != jsjson.Object || sandboxAudit.Get("kind").Str() != runResult.Get("kind").Str() {
				*errs = append(*errs, "run sandbox kind does not match execution result: "+runID)
			}
			if sandboxAudit.Kind() != jsjson.Object || sandboxAudit.Get("witnessDigest").Str() != run.Get("frozenDigest").Str() {
				*errs = append(*errs, "run sandbox witness digest does not match run frozen digest: "+runID)
			}
			if runResult.Get("executor").Str() == "UNSAFE_LOCAL" && runResult.Get("kind").Str() != "UNSAFE_LOCAL" {
				*errs = append(*errs, "unsafe-local executor has a non-local result kind: "+runID)
			}
			if runResult.Get("executor").Str() != "UNSAFE_LOCAL" && runResult.Get("kind").Str() != "DOCKER_ISOLATED" {
				*errs = append(*errs, "Docker executor has a non-Docker result kind: "+runID)
			}
			exitCode := runResult.Get("exitCode")
			if runResult.Get("verdict").Str() == "PASS" && (!isPassVerdictReason(runResult.Get("reason").Str()) ||
				exitCode.Kind() != jsjson.Number || exitCode.Num() != 0) {
				*errs = append(*errs, "PASS execution result is inconsistent: "+runID)
			}
			if runResult.Get("verdict").Str() == "FAIL" && (!isFailVerdictReason(runResult.Get("reason").Str()) ||
				exitCode.Kind() == jsjson.Null || exitCode.Num() == 0) {
				*errs = append(*errs, "FAIL execution result is inconsistent: "+runID)
			}
			if runResult.Get("kind").Str() == "UNSAFE_LOCAL" && (runResult.Get("verdict").Str() != "INAPPLICABLE" ||
				runResult.Get("reason").Str() != "UNSAFE_LOCAL_NOT_PROOF") {
				*errs = append(*errs, "unsafe-local execution result is not explicitly inapplicable: "+runID)
			}
			if run.Get("outcome").Str() != expectedOutcomeForRun(run) {
				*errs = append(*errs, "run outcome contradicts its execution result: "+runID)
			}
		}
		if witness := result.Get("witness"); witness.Kind() == jsjson.Object {
			if run.Get("frozenDigest").Str() != witness.Get("frozenDigest").Str() ||
				run.Get("witnessDigest").Str() != witness.Get("witnessDigest").Str() {
				*errs = append(*errs, "run witness digests do not match the result witness summary: "+runID)
			}
		}
	}
	for _, role := range roleOrder {
		ordered := slices.Clone(attemptsByRole[role])
		slices.Sort(ordered)
		contiguous := true
		for index, value := range ordered {
			if value != float64(index+1) {
				contiguous = false
			}
		}
		if !contiguous {
			*errs = append(*errs, "role attempts are not contiguous for "+role)
		}
	}
	usedExecutions := 0
	for _, run := range result.Get("runs").Items() {
		if run.Get("result").Kind() != jsjson.Null {
			usedExecutions++
		}
	}
	if result.Get("budget", "usedExecutions").Num() != float64(usedExecutions) {
		*errs = append(*errs, "used execution budget does not match recorded sandbox executions")
	}
	if result.Get("budget", "usedExecutions").Num() > result.Get("budget", "maxExecutions").Num() {
		*errs = append(*errs, "used execution budget exceeds its maximum")
	}

	referencedRuns := map[string]bool{}
	attemptsByRunID := map[string]jsjson.Value{}
	for index, attempt := range result.Get("attempts").Items() {
		if attempt.Get("ordinal").Num() != float64(index+1) {
			*errs = append(*errs, "attempt ordinal is not contiguous at offset "+strconv.Itoa(index))
		}
		validateCandidateIDs(bundle.Strings(attempt.Get("candidateUnitIds")), units, errs,
			"attempt "+jsjson.FormatNumber(attempt.Get("ordinal").Num())+" candidate")
		runID := attempt.Get("runId")
		if runID.Kind() == jsjson.Null {
			if attempt.Get("outcome").Str() != "NOT_RUN" {
				*errs = append(*errs, "attempt without a run must be NOT_RUN: "+jsjson.FormatNumber(attempt.Get("ordinal").Num()))
			}
			continue
		}
		if referencedRuns[runID.Str()] {
			*errs = append(*errs, "multiple attempts reference run id: "+runID.Str())
		}
		referencedRuns[runID.Str()] = true
		if _, ok := attemptsByRunID[runID.Str()]; !ok {
			attemptsByRunID[runID.Str()] = attempt
		}
		run, ok := runs[runID.Str()]
		if !ok {
			*errs = append(*errs, "attempt references an absent run: "+runID.Str())
			continue
		}
		if attempt.Get("phase").Str() != roleSemantics[run.Get("role").Str()][0] {
			*errs = append(*errs, "attempt phase does not match run role: "+runID.Str())
		}
		if !bundle.SameCanonical(attempt.Get("candidateUnitIds"), run.Get("candidateUnitIds")) {
			*errs = append(*errs, "attempt candidate does not match run candidate: "+runID.Str())
		}
		if attempt.Get("outcome").Str() != run.Get("outcome").Str() {
			*errs = append(*errs, "attempt outcome does not match run outcome: "+runID.Str())
		}
		if attempt.Get("note").Str() != run.Get("note").Str() {
			*errs = append(*errs, "attempt note does not match run note: "+runID.Str())
		}
	}
	for _, run := range result.Get("runs").Items() {
		if !referencedRuns[run.Get("runId").Str()] {
			*errs = append(*errs, "recorded run is not referenced by an attempt: "+run.Get("runId").Str())
		}
	}

	var enumerationAttempts []jsjson.Value
	for _, attempt := range result.Get("attempts").Items() {
		if attempt.Get("phase").Str() == "ENUMERATION" {
			enumerationAttempts = append(enumerationAttempts, attempt)
		}
	}
	enumeration := result.Get("minimality", "enumeration")
	if enumeration.Get("searchedCandidateSets").Num() != float64(len(enumerationAttempts)) {
		*errs = append(*errs, "enumeration searched-candidate count does not match retained enumeration attempts")
	}
	enumerationOutcomes := map[string]string{}
	for _, attempt := range result.Get("attempts").Items() {
		var run jsjson.Value
		hasRun := false
		if runID := attempt.Get("runId"); runID.Kind() != jsjson.Null {
			run, hasRun = runs[runID.Str()]
		}
		// Reverse-direction necessity uses the same patch IDs but has the
		// opposite predicate. It is not part of the forward subset lattice.
		if hasRun && run.Get("application", "direction").Str() != "FORWARD_FROM_BEFORE" {
			continue
		}
		if !hasRun && attempt.Get("phase").Str() != "ENUMERATION" {
			continue
		}
		key := candidateKey(bundle.Strings(attempt.Get("candidateUnitIds")))
		if previous, ok := enumerationOutcomes[key]; ok && previous != attempt.Get("outcome").Str() {
			shown := key
			if shown == "" {
				shown = "[]"
			}
			*errs = append(*errs, "conflicting recorded outcomes for candidate set: "+shown)
		}
		enumerationOutcomes[key] = attempt.Get("outcome").Str()
	}
	distinctEnumeratedUnits := map[string]bool{}
	for _, attempt := range enumerationAttempts {
		for _, id := range bundle.Strings(attempt.Get("candidateUnitIds")) {
			distinctEnumeratedUnits[id] = true
		}
	}
	// TS materializes boundedProperSubsets(ids) only for its length, 2^n - 2;
	// only the count is computed here.
	if enumeration.Get("status").Str() == "COMPLETED" && len(distinctEnumeratedUnits) >= 2 &&
		float64(len(enumerationAttempts)) != math.Exp2(float64(len(distinctEnumeratedUnits)))-2 {
		*errs = append(*errs, "completed enumeration does not retain every proper subset attempt")
	}
	for _, minimalSet := range enumeration.Get("distinctMinimalSets").Items() {
		ids := bundle.Strings(minimalSet)
		if enumerationOutcomes[candidateKey(ids)] != "FAIL" {
			*errs = append(*errs, "enumerated minimal set lacks a retained FAIL outcome")
		}
		for _, removed := range ids {
			if enumerationOutcomes[candidateKey(without(ids, removed))] != "PASS" {
				*errs = append(*errs, "enumerated minimal set lacks a retained PASS single-removal outcome")
			}
		}
	}
	interaction := result.Get("minimality", "nonMonotonicInteraction")
	if interaction.Get("verdict").Str() == "NON_MONOTONIC_INTERACTION" {
		evidence := interaction.Get("evidence")
		if evidence.Kind() == jsjson.Null {
			*errs = append(*errs, "non-monotonic interaction verdict lacks evidence")
		} else if enumerationOutcomes[candidateKey(bundle.Strings(evidence.Get("failingLeft")))] != "FAIL" ||
			enumerationOutcomes[candidateKey(bundle.Strings(evidence.Get("failingRight")))] != "FAIL" ||
			enumerationOutcomes[candidateKey(bundle.Strings(evidence.Get("passingUnion")))] != "PASS" {
			*errs = append(*errs, "non-monotonic interaction evidence does not match retained outcomes")
		}
	} else if interaction.Get("evidence").Kind() != jsjson.Null {
		*errs = append(*errs, "non-non-monotonic interaction verdict must not retain interaction evidence")
	}

	validateCertificate("sufficiency", result.Get("certification", "sufficiency"), result, runs, errs)
	validateCertificate("necessity", result.Get("certification", "necessity"), result, runs, errs)
	for _, run := range result.Get("runs").Items() {
		runID := run.Get("runId").Str()
		if run.Get("role").Str() == "SUFFICIENCY_CERTIFICATION" &&
			!slices.Contains(bundle.Strings(result.Get("certification", "sufficiency", "runIds")), runID) {
			*errs = append(*errs, "sufficiency certification run is absent from its certificate: "+runID)
		}
		if run.Get("role").Str() == "NECESSITY_CERTIFICATION" &&
			!slices.Contains(bundle.Strings(result.Get("certification", "necessity", "runIds")), runID) {
			*errs = append(*errs, "necessity certification run is absent from its certificate: "+runID)
		}
	}

	evidenceValid, evidenceErrors := proofGradeEvidence(result, attemptsByRunID)

	sufficiencyCertified := result.Get("certification", "sufficiency", "status").Str() == "CERTIFIED"
	necessityCertified := result.Get("certification", "necessity", "status").Str() == "CERTIFIED"
	proof := result.Get("proof")
	if proof.Get("sufficiencyCertified").Bool() != sufficiencyCertified {
		*errs = append(*errs, "proof sufficiency flag does not match certificate status")
	}
	if proof.Get("necessityCertified").Bool() != necessityCertified {
		*errs = append(*errs, "proof necessity flag does not match certificate status")
	}
	if proof.Get("dockerIsolated").Bool() != (proof.Get("executionTrust").Str() == "NATIVE_DOCKER") {
		*errs = append(*errs, "proof Docker-isolated flag does not match execution provenance")
	}
	for _, run := range result.Get("runs").Items() {
		if runResult := run.Get("result"); runResult.Kind() != jsjson.Null &&
			runResult.Get("executor").Str() != proof.Get("executionTrust").Str() {
			*errs = append(*errs, "run executor does not match result execution provenance: "+run.Get("runId").Str())
		}
	}
	expectedProof := proof.Get("executionTrust").Str() == "NATIVE_DOCKER" &&
		result.Get("minimality", "oneMinimal").Bool() &&
		sufficiencyCertified &&
		necessityCertified &&
		evidenceValid &&
		len(*errs) == 0 &&
		len(result.Get("errors").Items()) == 0
	if (proof.Get("isProof").Bool() || result.Get("status").Str() == "COMPLETED") && !evidenceValid {
		*errs = append(*errs, evidenceErrors...)
	}
	if proof.Get("isProof").Bool() != expectedProof {
		*errs = append(*errs, "proof bit contradicts provenance, minimality, certificates, or recorder errors")
	}
	if expectedProof && result.Get("status").Str() != "COMPLETED" {
		*errs = append(*errs, "completed proof evidence has a non-COMPLETED status")
	}
	if result.Get("status").Str() == "COMPLETED" && !expectedProof {
		*errs = append(*errs, "COMPLETED status lacks complete proof evidence")
	}

	status := externalDigestStatus(expected, provided, *resultDigest, errs)
	return &Verification{
		Valid:                len(*errs) == 0,
		Errors:               *errs,
		ResultDigest:         *resultDigest,
		ExternalDigestStatus: status,
		Result:               &result,
	}
}

// VerifyFile ports verifyGitMinimizationResultFile (src/git-minimization.ts:1930):
// it reads and verifies a standalone result without running its repository code.
func VerifyFile(filePath, expected string, provided bool) Verification {
	errs := []string{}
	var out *Verification
	if thrown := jsexc.Try(func() {
		absolute := nodefs.Resolve(filePath)
		stat := jsexc.Must(nodefs.Lstat(absolute))
		switch {
		case stat.Mode()&fs.ModeSymlink != 0 || !stat.Mode().IsRegular():
			errs = append(errs, "minimization result must be a regular, non-symlink file")
		case stat.Size() > maxMinimizationResultBytes:
			errs = append(errs, "minimization result exceeds FaultLine's verification read limit")
		default:
			bytes := jsexc.Must(nodefs.ReadBytes(absolute))
			if len(bytes) > maxMinimizationResultBytes {
				errs = append(errs, "minimization result changed beyond FaultLine's verification read limit while reading")
				return
			}
			value, err := jsjson.Parse(jsstr.DecodeUTF8(bytes))
			if err != nil {
				errs = append(errs, "minimization result contains invalid JSON: "+jsexc.Message(err))
			}
			if len(errs) == 0 {
				verification := Verify(value, expected, provided)
				out = &verification
			}
		}
	}); thrown != nil {
		errs = append(errs, "minimization result cannot be read safely: "+jsexc.Message(thrown))
	}
	if out != nil {
		return *out
	}
	status := externalDigestStatus(expected, provided, nil, &errs)
	return Verification{Errors: errs, ExternalDigestStatus: status}
}

// lstatIfPresent ports src/git-minimization.ts:625: null for ENOENT.
func lstatIfPresent(path string) (fs.FileInfo, bool) {
	info, err := nodefs.Lstat(path)
	if err != nil {
		var e *nodefs.Error
		if errors.As(err, &e) && e.Code == "ENOENT" {
			return nil, false
		}
		jsexc.Throw(err)
	}
	return info, true
}

// safeSegment is resolveSafeDirectorySegment with its lstat error thrown.
func safeSegment(path string) (string, bool) {
	resolved, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		jsexc.Throw(err)
	}
	return resolved, ok
}

// ensureRealOutputDirectory ports src/git-minimization.ts:1998: create each
// output parent without traversing a symlink or special file.
func ensureRealOutputDirectory(directory string) {
	absolute := nodefs.Resolve(directory)
	root := nodefs.ParseRoot(absolute)
	var parts []string
	if suffix := nodefs.Relative(root, absolute); suffix != "" {
		parts = splitPathParts(suffix)
	}
	current := root
	_, rootPresent := lstatIfPresent(current)
	safeRoot, safeRootOK := "", false
	if rootPresent {
		safeRoot, safeRootOK = safeSegment(current)
	}
	if !safeRootOK {
		jsexc.Throw(errors.New(jsexc.Concat("Minimization output root is not a real directory: ", root)))
	}
	current = safeRoot
	for _, part := range parts {
		current = nodefs.Join(current, part)
		if _, present := lstatIfPresent(current); !present {
			if err := nodefs.Mkdir(current, 0o700); err != nil {
				jsexc.Throw(err)
			}
			safeCurrent, ok := safeSegment(current)
			if !ok {
				jsexc.Throw(errors.New(jsexc.Concat("Minimization output parent is not a real directory: ", current)))
			}
			current = safeCurrent
			continue
		}
		safeCurrent, ok := safeSegment(current)
		if !ok {
			jsexc.Throw(errors.New(jsexc.Concat("Minimization output cannot traverse a symbolic link or non-directory: ", current)))
		}
		current = safeCurrent
	}
}

// splitPathParts is `suffix.split(/[\\/]+/).filter(Boolean)`.
func splitPathParts(suffix string) []string {
	return strings.FieldsFunc(suffix, func(r rune) bool { return r == '/' || r == '\\' })
}

// WriteResult ports writeGitMinimizationResult (src/git-minimization.ts:2030):
// it writes a canonical, self-consistent result write-once and returns its
// absolute path and retained digest.
func WriteResult(filePath string, result jsjson.Value) (path string, resultDigest string, err error) {
	if thrown := jsexc.Try(func() { path, resultDigest, err = writeResult(filePath, result) }); thrown != nil {
		return "", "", thrown
	}
	return path, resultDigest, err
}

func writeResult(filePath string, result jsjson.Value) (string, string, error) {
	verification := Verify(result, "", false)
	if !verification.Valid || verification.Result == nil || verification.ResultDigest == nil {
		return "", "", errors.New(jsexc.Concat("Refusing to write an invalid Git minimization result: ",
			jsexc.Join(verification.Errors, "; ")))
	}
	absolute := nodefs.Resolve(filePath)
	directory := nodefs.Dirname(absolute)
	ensureRealOutputDirectory(directory)
	if _, present := lstatIfPresent(absolute); present {
		return "", "", errors.New(jsexc.Concat("Git minimization result already exists and will not be replaced: ", absolute))
	}
	temporary := nodefs.Join(directory, "."+clock.NewUUID()+".faultline-git-minimization.tmp")
	content := jsexc.Must(canonical.CanonicalJSON(*verification.Result)) + "\n"
	writeErr := attempt(func() error {
		if err := nodefs.WriteFileUTF8(temporary, content, 0o600, true); err != nil {
			return err
		}
		// link(2) creates the final pathname only if it is absent, unlike a
		// replace-capable rename on POSIX. The temporary lives in the same
		// verified directory, so this is an atomic write-once publish step.
		if err := nodefs.Link(temporary, absolute); err != nil {
			var e *nodefs.Error
			if errors.As(err, &e) && e.Code == "EEXIST" {
				return errors.New(jsexc.Concat("Git minimization result already exists and will not be replaced: ", absolute))
			}
			return err
		}
		return nil
	})
	// finally
	if err := removeForce(temporary); err != nil {
		jsexc.Throw(err)
	}
	if writeErr != nil {
		return "", "", writeErr
	}
	return absolute, *verification.ResultDigest, nil
}

// removeForce is fs.promises.rm(path, { force: true }) for a non-directory:
// ENOENT resolves, a directory rejects with ERR_FS_EISDIR.
func removeForce(path string) error {
	info, err := nodefs.Lstat(path)
	if err != nil {
		var e *nodefs.Error
		if errors.As(err, &e) && e.Code == "ENOENT" {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return errors.New(jsexc.Concat("Path is a directory: rm returned EISDIR (is a directory) ", path))
	}
	return nodefs.RemoveAll(path)
}
