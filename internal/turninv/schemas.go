// Package turninv ports src/turn-investigation.ts: the turn investigation
// schemas, state extraction from a Codex lifecycle ledger, stable-state
// reconstruction, transitions and failure attribution.
package turninv

import (
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/evidence"
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

const (
	// SchemaVersion ports TURN_INVESTIGATION_SCHEMA_VERSION (:47).
	SchemaVersion = "faultline.turn-investigation.v1"
	// SessionBaselineTurnID ports SESSION_BASELINE_TURN_ID (:58).
	SessionBaselineTurnID = "session-baseline"
	// DuplicateOrdinalError is duplicateTurnOrdinalError's text (:383).
	DuplicateOrdinalError = "Duplicate turnOrdinal in session baseline / TURN_TREE_SNAPSHOT sequence."

	// EVIDENCE_LOG_PREVIEW_BYTES (:55).
	evidenceLogPreviewBytes = 16 * 1024
)

func nonnegativeInt() s.Schema { return s.Number(s.Int(), s.Nonnegative()) }
func positiveInt() s.Schema    { return s.Number(s.Int(), s.Positive()) }

var (
	// :51-54. Unlike gitproof's, these regexes carry no message, so zod's
	// text is "Invalid".
	digestSchema            = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), ""))
	digestPinnedImageSchema = s.String(s.Regex(s.Pattern(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`), ""))
	gitObjectIDSchema       = s.String(s.Regex(s.Pattern(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`), ""))
	timestampSchema         = s.String(s.Datetime())

	roleSchema = s.Enum("SESSION_BASELINE", "TURN")

	// TurnStateSchema ports TurnStateSchema (:60).
	TurnStateSchema = s.Object(
		s.F("index", nonnegativeInt()),
		s.F("turnId", s.String(s.MinLength(1))),
		s.F("turnOrdinal", nonnegativeInt()),
		s.F("role", roleSchema),
		s.F("treeDigest", gitObjectIDSchema),
		s.F("snapshotDigest", digestSchema),
		s.F("dirty", s.Boolean()),
	).Strict()

	// sandboxAuditSchema ports the local SandboxAuditSchema (:73-103).
	sandboxAuditSchema = s.Object(
		s.F("kind", s.Enum("DOCKER_ISOLATED", "UNSAFE_LOCAL")),
		s.F("witnessDigest", digestSchema),
		s.F("commandDigest", digestSchema),
		s.F("environmentPolicyDigest", digestSchema),
		s.F("policyDigest", digestSchema),
		s.F("environment", s.Object(
			s.F("fixedKeys", s.Array(s.String())),
			s.F("allowedKeys", s.Array(s.String())),
			s.F("passed", s.Array(s.Object(s.F("key", s.String()), s.F("valueDigest", digestSchema)).Strict())),
			s.F("redactedKeys", s.Array(s.String())),
		).Strict()),
		s.F("runtime", s.Object(
			s.F("image", s.Nullable(s.String())),
			s.F("entrypoint", s.Nullable(s.String())),
			s.F("network", s.Nullable(s.LiteralString("none"))),
			s.F("rootFilesystemReadOnly", s.Boolean()),
			s.F("user", s.Nullable(s.String())),
			s.F("capDropAll", s.Boolean()),
			s.F("noNewPrivileges", s.Boolean()),
			s.F("pull", s.Nullable(s.LiteralString("never"))),
			s.F("limits", s.Object(
				s.F("timeoutMs", positiveInt()),
				s.F("maxOutputBytes", positiveInt()),
				s.F("cpuCount", positiveInt()),
				s.F("memoryBytes", positiveInt()),
				s.F("pidsLimit", positiveInt()),
				s.F("tmpfsBytes", positiveInt()),
			).Strict()),
		).Strict()),
	).Strict()

	// RunFactSchema ports TurnInvestigationRunFactSchema (:105).
	RunFactSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("runId", digestSchema),
		s.F("executionId", digestSchema),
		s.F("executionNonce", s.String(s.UUID())),
		s.F("stateIndex", nonnegativeInt()),
		s.F("executionAttempt", s.Number(s.Int(), s.Gte(1), s.Lte(gitproof.StableExecutionCount))),
		s.F("turnId", s.String(s.MinLength(1))),
		s.F("turnOrdinal", nonnegativeInt()),
		s.F("role", roleSchema),
		s.F("treeDigest", gitObjectIDSchema),
		s.F("snapshotDigest", digestSchema),
		s.F("environmentFingerprintDigest", digestSchema),
		s.F("frozenDigest", digestSchema),
		s.F("witnessDigest", digestSchema),
		s.F("startedAt", timestampSchema),
		s.F("finishedAt", timestampSchema),
		s.F("durationMs", nonnegativeInt()),
		s.F("overlays", s.Array(gitproof.MaterializedOverlaySchema)),
		s.F("sandbox", sandboxAuditSchema),
		s.F("result", s.Object(
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
		).Strict()),
	).Strict()

	// StableStateSchema ports StableTurnStateSchema (:161).
	StableStateSchema = s.Object(
		s.F("stateIndex", nonnegativeInt()),
		s.F("turnId", s.String(s.MinLength(1))),
		s.F("turnOrdinal", nonnegativeInt()),
		s.F("role", roleSchema),
		s.F("treeDigest", gitObjectIDSchema),
		s.F("snapshotDigest", digestSchema),
		s.F("verdict", s.Enum("PASS", "FAIL")),
		s.F("executionIds", s.Array(digestSchema, s.Length(gitproof.StableExecutionCount))),
		s.F("runIds", s.Array(digestSchema, s.Length(gitproof.StableExecutionCount))),
	).Strict()

	// StableTransitionSchema ports StableTurnTransitionSchema (:175).
	StableTransitionSchema = s.Object(
		s.F("kind", s.Enum("PASS_TO_FAIL", "FAIL_TO_PASS")),
		s.F("before", StableStateSchema),
		s.F("after", StableStateSchema),
	).Strict()

	// IntroductionSchema ports TurnIntroductionSchema (:186).
	IntroductionSchema = s.Object(
		s.F("status", s.Enum("ATTRIBUTED", "UNATTRIBUTED", "NOT_APPLICABLE")),
		s.F("turnOrdinal", s.Nullable(positiveInt())),
		s.F("turnId", s.Nullable(s.String(s.MinLength(1)))),
		s.F("reason", s.String(s.MinLength(1))),
	).Strict()

	// witnessVerificationSummarySchema ports the local
	// WitnessVerificationSummarySchema (:195-202).
	witnessVerificationSummarySchema = s.Object(
		s.F("valid", s.Boolean()),
		s.F("errors", s.Array(s.String())),
		s.F("frozenDigest", s.Nullable(digestSchema)),
		s.F("witnessDigest", s.Nullable(digestSchema)),
		s.F("externalDigestStatus", s.Enum("NOT_PROVIDED", "MATCH", "MISMATCH")),
		// Not strict in TS (:201): unknown approval keys are stripped.
		s.F("approval", s.Nullable(s.Object(s.F("actor", s.String()), s.F("approvedAt", timestampSchema)))),
	).Strict()

	// ResultSchema ports TurnInvestigationResultSchema (:204).
	ResultSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("recorder", s.LiteralString("codex-turn-tree-replay")),
		s.F("nativeCodexInterception", s.Literal(jsjson.MakeBool(false))),
		s.F("status", s.Enum(
			"COMPLETED",
			"INVALID_WITNESS",
			"NO_TURN_SNAPSHOTS",
			"EXECUTION_ERROR",
			"ENVIRONMENT_CHANGED",
			"DUPLICATE_TURN_SNAPSHOTS",
			"SANDBOX_UNAVAILABLE",
		)),
		s.F("repository", s.Nullable(s.String())),
		s.F("ledgerDigest", s.Nullable(digestSchema)),
		s.F("witness", s.Nullable(witnessVerificationSummarySchema)),
		s.F("executionsPerState", s.Literal(jsjson.MakeNumber(gitproof.StableExecutionCount))),
		s.F("states", s.Array(TurnStateSchema)),
		s.F("runs", s.Array(RunFactSchema)),
		s.F("stableStates", s.Array(StableStateSchema)),
		s.F("transitions", s.Array(StableTransitionSchema)),
		s.F("environmentFingerprints", s.Array(gitproof.EnvironmentFingerprintSchema)),
		s.F("environmentHomogeneity", s.Enum("HOMOGENEOUS", "HETEROGENEOUS", "EMPTY")),
		s.F("runtimeMapping", s.RecordOf(digestSchema, digestPinnedImageSchema)),
		s.F("introduction", IntroductionSchema),
		s.F("nonMonotonic", s.Boolean()),
		s.F("proof", s.Object(
			s.F("requiresDockerIsolation", s.Literal(jsjson.MakeBool(true))),
			s.F("dockerIsolated", s.Boolean()),
			s.F("executionTrust", s.Enum("NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL")),
			s.F("proofTransitions", nonnegativeInt()),
			s.F("isProof", s.Boolean()),
			s.F("reason", s.String(s.MinLength(1))),
			// :242-246, in this order (it shows in invalid_enum_value's options).
			s.F("evidenceGrade", s.Enum(
				evidence.TurnPathEvidenceGradeParityReserved,
				evidence.TurnPathEvidenceGradeExperimental,
				"NONE",
			)),
			s.F("evidenceLabel", s.String(s.MinLength(1))),
		).Strict()),
		s.F("errors", s.Array(s.String())),
	).Strict()
)
