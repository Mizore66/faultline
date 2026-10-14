// Package minimize ports the Git-only counterfactual minimization recorder
// in src/git-minimization.ts: its schemas, the search, offline verification
// and the write-once result writer.
package minimize

import (
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/git-minimization.ts:41-52.
const (
	SchemaVersion = "faultline.git-minimization.v1"
	// CertificationExecutions is MINIMIZATION_CERTIFICATION_EXECUTIONS.
	CertificationExecutions = 3
	// MaxEnumerationPatchUnits and MaxEnumeratedMinimalSets bound the
	// exhaustive subset enumeration.
	MaxEnumerationPatchUnits = 10
	MaxEnumeratedMinimalSets = 8

	sha256DigestSource         = `^sha256:[a-f0-9]{64}$`
	gitObjectIDSource          = `^(?:[a-f0-9]{40}|[a-f0-9]{64})$`
	evidenceLogPreviewBytes    = 16 * 1024
	maxMinimizationResultBytes = 128 * 1024 * 1024
)

// safeGitRevision is SAFE_GIT_REVISION (src/git-minimization.ts:50),
// /^(?!-)[^\0\r\n]{1,512}$/ counted in UTF-16 units.
func safeGitRevision(value string) bool {
	units := jsstr.ToUTF16(value)
	if len(units) < 1 || len(units) > 512 || units[0] == '-' {
		return false
	}
	for _, u := range units {
		if u == 0 || u == '\r' || u == '\n' {
			return false
		}
	}
	return true
}

func nonnegativeInt() s.Schema { return s.Number(s.Int(), s.Nonnegative()) }
func positiveInt() s.Schema    { return s.Number(s.Int(), s.Positive()) }

// PASS_VERDICT_REASONS and FAIL_VERDICT_REASONS (src/git-minimization.ts:60-63).
func isPassVerdictReason(reason string) bool { return reason == "PREDICATE_PASS" }
func isFailVerdictReason(reason string) bool { return reason == "PREDICATE_FAIL" }

var (
	// src/git-minimization.ts:54-58.
	digestSchema      = s.String(s.Regex(s.Pattern(sha256DigestSource), "expected sha256:<64 lowercase hex characters>"))
	gitObjectIDSchema = s.String(s.Regex(s.Pattern(gitObjectIDSource), "expected a 40- or 64-character lowercase Git object id"))
	gitRevisionSchema = s.String(s.Regex(safeGitRevision, "revision must be non-empty, cannot start with '-', and cannot contain NUL or line breaks"))
	timestampSchema   = s.String(s.Datetime())

	// GitMinimizationSandboxSchema (src/git-minimization.ts:64) has the
	// shape of GitInvestigationSandboxSchema (src/git-investigation.ts:80).
	sandboxSchema = gitproof.GitSandboxSchema

	// GitMinimizationBudgetSchema ports src/git-minimization.ts:81.
	budgetSchema = s.Default(s.Object(
		s.F("maxExecutions", s.Default(s.Number(s.Int(), s.Gte(1), s.Lte(1_024)), jsjson.MakeNumber(128))),
	).Strict(), jsjson.MustObject("maxExecutions", jsjson.MakeNumber(128)))

	// RequestSchema ports GitMinimizationRequestSchema (src/git-minimization.ts:86).
	RequestSchema = s.Object(
		s.F("repository", s.String(s.MinLength(1), s.MaxLength(16_384))),
		s.F("before", gitRevisionSchema),
		s.F("after", gitRevisionSchema),
		s.F("frozenWitness", gitproof.FrozenWitnessSchema),
		s.F("expectedFrozenDigest", digestSchema),
		s.F("sandbox", sandboxSchema),
		s.F("budget", budgetSchema),
	).Strict()

	// counterfactualStateSchema ports src/git-minimization.ts:113.
	counterfactualStateSchema = s.Object(
		s.F("commit", gitObjectIDSchema),
		s.F("tree", gitObjectIDSchema),
	).Strict()

	// PatchUnitSchema ports GitPatchUnitSchema (src/git-minimization.ts:118).
	PatchUnitSchema = s.Object(
		s.F("id", digestSchema),
		s.F("ordinal", nonnegativeInt()),
		s.F("changeKind", s.Enum("ADD", "DELETE", "MODIFY", "TYPECHANGE", "UNKNOWN")),
		s.F("path", s.String(s.MinLength(1))),
		s.F("pathBytesBase64", s.String(s.MinLength(1))),
		s.F("pathDigest", digestSchema),
		s.F("patchDigest", digestSchema),
		s.F("patchBytes", positiveInt()),
		s.F("binarySafe", s.Literal(jsjson.MakeBool(true))),
	).Strict()

	// MaterializedFrozenOverlaySchema (src/git-minimization.ts:133) is the
	// shape of src/safe-overlay.ts:12.
	materializedOverlaySchema = gitproof.MaterializedOverlaySchema

	// SandboxAuditSchema (src/git-minimization.ts:139) is the shape of
	// src/git-investigation.ts:130.
	sandboxAuditSchema = gitproof.SandboxAuditSchema

	// PatchApplicationSchema ports GitPatchApplicationSchema (src/git-minimization.ts:171).
	PatchApplicationSchema = s.Object(
		s.F("direction", s.Enum("FORWARD_FROM_BEFORE", "REVERSE_FROM_AFTER")),
		s.F("base", counterfactualStateSchema),
		s.F("selectedUnitIds", s.Array(digestSchema)),
		s.F("candidatePatchDigest", digestSchema),
		s.F("candidatePatchBytes", nonnegativeInt()),
		s.F("status", s.Enum("NO_PATCHES", "APPLIED", "CONFLICT", "ERROR")),
		s.F("stdoutDigest", digestSchema),
		s.F("stdoutBytes", nonnegativeInt()),
		s.F("stderrDigest", digestSchema),
		s.F("stderrBytes", nonnegativeInt()),
		s.F("error", s.Nullable(s.String())),
	).Strict()

	// RunFactSchema ports GitMinimizationRunFactSchema (src/git-minimization.ts:185).
	RunFactSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("runId", digestSchema),
		s.F("executionId", digestSchema),
		s.F("executionNonce", s.String(s.UUID())),
		s.F("role", s.Enum(
			"BASELINE_BEFORE",
			"FULL_PATCH",
			"DELTA_SUBSET",
			"DELTA_COMPLEMENT",
			"ENUMERATION",
			"ONE_MINIMAL",
			"HUNK_ONE_MINIMAL",
			"SUFFICIENCY_CERTIFICATION",
			"NECESSITY_CERTIFICATION",
		)),
		s.F("roleAttempt", positiveInt()),
		s.F("candidateUnitIds", s.Array(digestSchema)),
		s.F("worktreeDigest", digestSchema),
		s.F("frozenDigest", digestSchema),
		s.F("witnessDigest", digestSchema),
		s.F("startedAt", timestampSchema),
		s.F("finishedAt", timestampSchema),
		s.F("durationMs", nonnegativeInt()),
		s.F("overlays", s.Array(materializedOverlaySchema)),
		s.F("application", PatchApplicationSchema),
		s.F("sandbox", s.Nullable(sandboxAuditSchema)),
		s.F("result", s.Nullable(s.Object(
			s.F("kind", s.Enum("DOCKER_ISOLATED", "UNSAFE_LOCAL")),
			s.F("executor", s.Enum("NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL")),
			s.F("verdict", s.Enum("PASS", "FAIL", "ERROR", "INAPPLICABLE")),
			s.F("reason", s.Enum(
				"EXIT_ZERO",
				"EXIT_NONZERO",
				"PREDICATE_PASS",
				"PREDICATE_FAIL",
				"INCOMPATIBLE_STATE",
				"HARNESS_ERROR",
				"EXIT_NONZERO_UNSTRUCTURED",
				"EXIT_ZERO_UNSTRUCTURED",
				"TIMEOUT",
				"OUTPUT_LIMIT_EXCEEDED",
				"SANDBOX_UNAVAILABLE",
				"WITNESS_SETUP_ERROR",
				"RUNNER_FAILURE",
				"UNSAFE_LOCAL_NOT_PROOF",
			)),
			s.F("exitCode", s.Nullable(s.Number(s.Int()))),
			s.F("signal", s.Nullable(s.String())),
			s.F("outputTruncated", s.Boolean()),
			s.F("stdoutDigest", digestSchema),
			s.F("stdoutBytes", nonnegativeInt()),
			s.F("stdoutPreview", s.String(s.MaxLength(evidenceLogPreviewBytes*2))),
			s.F("stdoutPreviewTruncated", s.Boolean()),
			s.F("stdoutRedacted", s.Boolean()),
			s.F("stderrDigest", digestSchema),
			s.F("stderrBytes", nonnegativeInt()),
			s.F("stderrPreview", s.String(s.MaxLength(evidenceLogPreviewBytes*2))),
			s.F("stderrPreviewTruncated", s.Boolean()),
			s.F("stderrRedacted", s.Boolean()),
		).Strict())),
		s.F("outcome", s.Enum("PASS", "FAIL", "UNRESOLVED")),
		s.F("note", s.String()),
	).Strict()

	// AttemptSchema ports GitMinimizationAttemptSchema (src/git-minimization.ts:252).
	AttemptSchema = s.Object(
		s.F("ordinal", positiveInt()),
		s.F("phase", s.Enum(
			"BASELINE",
			"FULL_RANGE",
			"DELTA_SUBSET",
			"DELTA_COMPLEMENT",
			"ENUMERATION",
			"ONE_MINIMAL",
			"HUNK_ONE_MINIMAL",
			"SUFFICIENCY_CERTIFICATION",
			"NECESSITY_CERTIFICATION",
		)),
		s.F("candidateUnitIds", s.Array(digestSchema)),
		s.F("outcome", s.Enum("PASS", "FAIL", "UNRESOLVED", "NOT_RUN")),
		s.F("runId", s.Nullable(digestSchema)),
		s.F("note", s.String()),
	).Strict()

	// src/git-minimization.ts:271.
	witnessVerificationSummarySchema = s.Object(
		s.F("valid", s.Boolean()),
		s.F("errors", s.Array(s.String())),
		s.F("frozenDigest", s.Nullable(digestSchema)),
		s.F("witnessDigest", s.Nullable(digestSchema)),
		s.F("externalDigestStatus", s.Enum("NOT_PROVIDED", "MATCH", "MISMATCH")),
		s.F("approval", s.Nullable(s.Object(s.F("actor", s.String()), s.F("approvedAt", timestampSchema)))),
	).Strict()

	// CertificateSchema ports GitMinimizationCertificateSchema (src/git-minimization.ts:280).
	CertificateSchema = s.Object(
		s.F("direction", s.Enum("FORWARD_FROM_BEFORE", "REVERSE_FROM_AFTER")),
		s.F("expectedOutcome", s.Enum("PASS", "FAIL")),
		s.F("requiredExecutions", s.Literal(jsjson.MakeNumber(CertificationExecutions))),
		s.F("runIds", s.Array(digestSchema, s.MaxItems(CertificationExecutions))),
		s.F("executionIds", s.Array(digestSchema, s.MaxItems(CertificationExecutions))),
		s.F("status", s.Enum("CERTIFIED", "NOT_CERTIFIED", "NOT_RUN")),
		s.F("note", s.String()),
	).Strict()

	// src/git-minimization.ts:290.
	enumerationSchema = s.Object(
		s.F("status", s.Enum("COMPLETED", "BUDGET_EXHAUSTED", "SKIPPED_MAX_UNITS", "SET_LIMIT_REACHED", "NOT_RUN")),
		s.F("searchedCandidateSets", nonnegativeInt()),
		s.F("maxPatchUnits", s.Literal(jsjson.MakeNumber(MaxEnumerationPatchUnits))),
		s.F("maxDistinctMinimalSets", s.Literal(jsjson.MakeNumber(MaxEnumeratedMinimalSets))),
		s.F("distinctMinimalSets", s.Array(s.Array(digestSchema, s.MinItems(1)), s.MaxItems(MaxEnumeratedMinimalSets))),
		s.F("note", s.String()),
	).Strict()

	// src/git-minimization.ts:299.
	interactionSchema = s.Object(
		s.F("verdict", s.Enum("NOT_OBSERVED", "NON_MONOTONIC_INTERACTION", "INCONCLUSIVE")),
		s.F("evidence", s.Nullable(s.Object(
			s.F("failingLeft", s.Array(digestSchema, s.MinItems(1))),
			s.F("failingRight", s.Array(digestSchema, s.MinItems(1))),
			s.F("passingUnion", s.Array(digestSchema, s.MinItems(1))),
		).Strict())),
		s.F("note", s.String()),
	).Strict()

	// src/git-minimization.ts:309.
	minimalitySchema = s.Object(
		s.F("oneMinimal", s.Boolean()),
		s.F("reason", s.String()),
		s.F("enumeration", enumerationSchema),
		s.F("nonMonotonicInteraction", interactionSchema),
	).Strict()

	// ResultSchema ports GitMinimizationResultSchema (src/git-minimization.ts:316).
	ResultSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("recorder", s.LiteralString("git-diff-counterfactual-minimization")),
		s.F("nativeCodexInterception", s.Literal(jsjson.MakeBool(false))),
		s.F("status", s.Enum(
			"COMPLETED",
			"INVALID_REQUEST",
			"INVALID_WITNESS",
			"RANGE_ERROR",
			"PATCH_ERROR",
			"NO_PATCHES",
			"BASELINE_NOT_PASS",
			"FULL_PATCH_NOT_FAIL",
			"BUDGET_EXHAUSTED",
			"SANDBOX_UNAVAILABLE",
			"UNSAFE_LOCAL_INAPPLICABLE",
			"EXECUTION_ERROR",
			"CERTIFICATION_FAILED",
		)),
		s.F("repository", s.Nullable(s.String())),
		s.F("before", s.Nullable(counterfactualStateSchema)),
		s.F("after", s.Nullable(counterfactualStateSchema)),
		s.F("witness", s.Nullable(witnessVerificationSummarySchema)),
		s.F("budget", s.Object(s.F("usedExecutions", nonnegativeInt()), s.F("maxExecutions", positiveInt())).Strict()),
		s.F("patchUnits", s.Array(PatchUnitSchema)),
		s.F("candidateUnitIds", s.Array(digestSchema)),
		s.F("attempts", s.Array(AttemptSchema)),
		s.F("runs", s.Array(RunFactSchema)),
		s.F("minimality", minimalitySchema),
		s.F("certification", s.Object(
			s.F("sufficiency", CertificateSchema),
			s.F("necessity", CertificateSchema),
		).Strict()),
		s.F("proof", s.Object(
			s.F("requiresDockerIsolation", s.Literal(jsjson.MakeBool(true))),
			s.F("dockerIsolated", s.Boolean()),
			s.F("executionTrust", s.Enum("NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL")),
			s.F("sufficiencyCertified", s.Boolean()),
			s.F("necessityCertified", s.Boolean()),
			s.F("isProof", s.Boolean()),
			s.F("reason", s.String()),
		).Strict()),
		s.F("errors", s.Array(s.String())),
	).Strict()
)
