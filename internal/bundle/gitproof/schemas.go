// Package gitproof ports the faultline.git-proof-bundle.v1 verifier in
// src/git-proof-bundle.ts and the schemas and verification functions it uses.
package gitproof

import (
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/git-investigation.ts:62-78.
const (
	GitInvestigationSchemaVersion = "faultline.git-investigation.v1"
	StableExecutionCount          = 3
	evidenceLogPreviewBytes       = 16 * 1024
)

// Port of src/git-proof-bundle.ts:47-56.
const (
	GitProofBundleSchemaVersion = "faultline.git-proof-bundle.v1"
	GitProofSourceSchemaVersion = "faultline.git-proof-source.v1"
	BundleHeadRef               = "refs/faultline/portable-descendant"
	maxSourceArtifactBytes      = 128 * 1024 * 1024
	maxHashCatalogBytes         = 4 * 1024 * 1024
	maxGitProofArtifacts        = 2048
	maxGitProofTotalBytes       = 512 * 1024 * 1024
)

// Port of src/evidence-grade.ts:31.
const commitPathEvidenceGrade = "COMMIT_PROOF"

// Port of src/environment-fingerprint.ts:8.
const environmentFingerprintVersion = "faultline.environment-fingerprint.v2"

const (
	sha256DigestSource  = `^sha256:[a-f0-9]{64}$`
	gitObjectIDSource   = `^(?:[a-f0-9]{40}|[a-f0-9]{64})$`
	digestPinnedImageRe = `^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`
	codexThreadIDSource = `^[A-Za-z0-9][A-Za-z0-9._:-]*$`
)

func nonnegativeInt() s.Schema { return s.Number(s.Int(), s.Nonnegative()) }
func positiveInt() s.Schema    { return s.Number(s.Int(), s.Positive()) }

var (
	// src/git-investigation.ts:70-73.
	digestSchema      = s.String(s.Regex(s.Pattern(sha256DigestSource), "expected sha256:<64 lowercase hex characters>"))
	gitObjectIDSchema = s.String(s.Regex(s.Pattern(gitObjectIDSource), "expected a 40- or 64-character lowercase Git object id"))
	gitRevisionSchema = s.String(s.Regex(safeGitRevision, "revision must be non-empty, cannot start with '-', and cannot contain NUL or line breaks"))
	timestampSchema   = s.String(s.Datetime())

	// src/git-investigation.ts:75.
	gitRangeSchema = s.Object(
		s.F("ancestor", gitRevisionSchema),
		s.F("descendant", gitRevisionSchema),
	).Strict()

	// GitCommitStateSchema ports src/git-investigation.ts:124.
	GitCommitStateSchema = s.Object(
		s.F("index", nonnegativeInt()),
		s.F("commit", gitObjectIDSchema),
		s.F("tree", gitObjectIDSchema),
	).Strict()

	// src/git-investigation.ts:130.
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

	// MaterializedOverlaySchema ports src/safe-overlay.ts:12.
	MaterializedOverlaySchema = s.Object(
		s.F("path", s.String(s.MinLength(1))),
		s.F("bytesDigest", s.String(s.Regex(s.Pattern(sha256DigestSource), "expected sha256:<64 lowercase hex characters>"))),
		s.F("bytesLength", nonnegativeInt()),
	).Strict()

	// GitInvestigationRunFactSchema ports src/git-investigation.ts:163.
	GitInvestigationRunFactSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(GitInvestigationSchemaVersion)),
		s.F("runId", digestSchema),
		s.F("executionId", digestSchema),
		s.F("executionNonce", s.String(s.UUID())),
		s.F("stateIndex", nonnegativeInt()),
		s.F("executionAttempt", s.Number(s.Int(), s.Gte(1), s.Lte(StableExecutionCount))),
		s.F("commit", gitObjectIDSchema),
		s.F("tree", gitObjectIDSchema),
		s.F("frozenDigest", digestSchema),
		s.F("witnessDigest", digestSchema),
		s.F("startedAt", timestampSchema),
		s.F("finishedAt", timestampSchema),
		s.F("durationMs", nonnegativeInt()),
		s.F("overlays", s.Array(MaterializedOverlaySchema)),
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
		).Strict()),
	).Strict()

	// StableGitStateSchema ports src/git-investigation.ts:216.
	StableGitStateSchema = s.Object(
		s.F("stateIndex", nonnegativeInt()),
		s.F("commit", gitObjectIDSchema),
		s.F("tree", gitObjectIDSchema),
		s.F("verdict", s.Enum("PASS", "FAIL")),
		s.F("executionIds", s.Array(digestSchema, s.Length(StableExecutionCount))),
		s.F("runIds", s.Array(digestSchema, s.Length(StableExecutionCount))),
	).Strict()

	// StableGitTransitionSchema ports src/git-investigation.ts:225.
	StableGitTransitionSchema = s.Object(
		s.F("kind", s.Enum("PASS_TO_FAIL", "FAIL_TO_PASS")),
		s.F("before", StableGitStateSchema),
		s.F("after", StableGitStateSchema),
	).Strict()

	// src/git-investigation.ts:231.
	witnessVerificationSummarySchema = s.Object(
		s.F("valid", s.Boolean()),
		s.F("errors", s.Array(s.String())),
		s.F("frozenDigest", s.Nullable(digestSchema)),
		s.F("witnessDigest", s.Nullable(digestSchema)),
		s.F("externalDigestStatus", s.Enum("NOT_PROVIDED", "MATCH", "MISMATCH")),
		s.F("approval", s.Nullable(s.Object(s.F("actor", s.String()), s.F("approvedAt", timestampSchema)))),
	).Strict()

	// EnvironmentFingerprintSchema ports src/environment-fingerprint.ts:47-57.
	// Its digest regex has no custom message.
	fingerprintDigestSchema      = s.String(s.Regex(s.Pattern(sha256DigestSource), ""))
	EnvironmentFingerprintSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(environmentFingerprintVersion)),
		s.F("files", s.Record(fingerprintDigestSchema)),
		s.F("digest", fingerprintDigestSchema),
	).Strict()

	// GitInvestigationResultSchema ports src/git-investigation.ts:240.
	GitInvestigationResultSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(GitInvestigationSchemaVersion)),
		s.F("recorder", s.LiteralString("git-commit-range-replay")),
		s.F("nativeCodexInterception", s.Literal(jsjson.MakeBool(false))),
		s.F("status", s.Enum(
			"COMPLETED",
			"INVALID_REQUEST",
			"INVALID_WITNESS",
			"RANGE_ERROR",
			"CONFIGURATION_ERROR",
			"SANDBOX_UNAVAILABLE",
			"EXECUTION_ERROR",
		)),
		s.F("repository", s.Nullable(s.String())),
		s.F("requestedRange", s.Nullable(gitRangeSchema)),
		s.F("resolvedRange", s.Nullable(s.Object(
			s.F("ancestor", GitCommitStateSchema),
			s.F("descendant", GitCommitStateSchema),
		).Strict())),
		s.F("witness", s.Nullable(witnessVerificationSummarySchema)),
		s.F("executionsPerState", s.Literal(jsjson.MakeNumber(StableExecutionCount))),
		s.F("states", s.Array(GitCommitStateSchema)),
		s.F("runs", s.Array(GitInvestigationRunFactSchema)),
		s.F("stableStates", s.Array(StableGitStateSchema)),
		s.F("transitions", s.Array(StableGitTransitionSchema)),
		s.F("nonMonotonic", s.Boolean()),
		s.F("environment", s.Object(
			s.F("homogeneity", s.Enum("HOMOGENEOUS", "HETEROGENEOUS", "EMPTY")),
			s.F("fingerprints", s.Array(s.Object(
				s.F("stateIndex", nonnegativeInt()),
				s.F("commit", gitObjectIDSchema),
				s.F("fingerprint", EnvironmentFingerprintSchema),
			).Strict())),
			s.F("distinctDigests", s.Array(digestSchema)),
		).Strict()),
		s.F("proof", s.Object(
			s.F("requiresDockerIsolation", s.Literal(jsjson.MakeBool(true))),
			s.F("dockerIsolated", s.Boolean()),
			s.F("executionTrust", s.Enum("NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL")),
			s.F("proofTransitions", nonnegativeInt()),
			s.F("isProof", s.Boolean()),
			s.F("reason", s.String()),
			s.F("evidenceGrade", s.Enum(commitPathEvidenceGrade, "NONE")),
			s.F("evidenceLabel", s.String(s.MinLength(1))),
		).Strict()),
		s.F("errors", s.Array(s.String())),
	).Strict()
)

// Port of src/git-proof-bundle.ts:58-165.
var (
	gitDigest = digestSchema

	artifactPathSchema = s.Refine(s.String(s.MinLength(1), s.MaxLength(1024)), safePathValue, "artifact path must be a safe POSIX-relative path")

	runArtifactSchema        = s.Object(s.F("runId", gitDigest), s.F("path", artifactPathSchema)).Strict()
	transitionArtifactSchema = s.Object(s.F("index", nonnegativeInt()), s.F("path", artifactPathSchema)).Strict()

	lifecycleUnboundSchema = s.Object(
		s.F("status", s.LiteralString("UNBOUND")),
		s.F("limitation", s.LiteralString("No caller-supplied Codex lifecycle ledger is bound to this Git investigation package.")),
	).Strict()

	lifecycleBindingFields = s.Object(
		s.F("path", s.LiteralString("lifecycle/ledger.json")),
		s.F("ledgerDigest", gitDigest),
		s.F("headHash", gitDigest),
		s.F("transport", s.Enum("CODEX_CLI", "CODEX_APP", "SIDE_CAR", "OBSERVED_EXTERNAL_TRANSPORT")),
		s.F("checkpointBindings", s.Array(s.Object(
			s.F("sequence", positiveInt()),
			s.F("stateIndex", nonnegativeInt()),
			s.F("checkpointDigest", gitDigest),
		).Strict(), s.MinItems(1))),
	).Strict()

	lifecycleLegacyBoundSchema    = lifecycleBindingFields.Extend(s.F("status", s.LiteralString("BOUND"))).Strict()
	lifecyclePartiallyBoundSchema = lifecycleBindingFields.Extend(s.F("status", s.LiteralString("PARTIALLY_BOUND"))).Strict()
	lifecycleFullyBoundSchema     = lifecycleBindingFields.Extend(s.F("status", s.LiteralString("FULLY_BOUND"))).Strict()

	lifecycleBindingSchema = s.DiscriminatedUnion("status", lifecycleUnboundSchema, lifecycleLegacyBoundSchema, lifecyclePartiallyBoundSchema, lifecycleFullyBoundSchema)

	resolvedRangeSchema = s.Object(
		s.F("ancestor", GitCommitStateSchema),
		s.F("descendant", GitCommitStateSchema),
	).Strict()

	// GitProofSourceMetadataSchema ports src/git-proof-bundle.ts:115.
	GitProofSourceMetadataSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(GitProofSourceSchemaVersion)),
		s.F("objectFormat", s.Enum("sha1", "sha256")),
		s.F("ancestor", GitCommitStateSchema),
		s.F("descendant", GitCommitStateSchema),
		s.F("bundle", s.Object(
			s.F("path", s.LiteralString("source/descendant.bundle")),
			s.F("digest", gitDigest),
			s.F("bytes", positiveInt()),
			s.F("headRef", s.LiteralString(BundleHeadRef)),
			s.F("headCommit", s.String(s.Regex(s.Pattern(gitObjectIDSource), "expected Git object id"))),
		).Strict()),
		s.F("rangePatch", s.Object(
			s.F("path", s.LiteralString("source/range.patch")),
			s.F("digest", gitDigest),
			s.F("bytes", nonnegativeInt()),
			s.F("format", s.LiteralString("git-diff --binary --full-index --no-ext-diff --no-textconv --no-renames")),
		).Strict()),
	).Strict()

	codexThreadIDSchema = s.Optional(s.String(s.MinLength(1), s.MaxLength(160), s.Regex(s.Pattern(codexThreadIDSource), "")))

	// GitProofBundleManifestSchema ports src/git-proof-bundle.ts:136.
	GitProofBundleManifestSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(GitProofBundleSchemaVersion)),
		s.F("generatedAt", timestampSchema),
		s.F("integrityScope", s.LiteralString("complete-declared-file-set")),
		s.F("investigationDigest", gitDigest),
		s.F("frozenDigest", gitDigest),
		s.F("witnessDigest", gitDigest),
		s.F("lifecycle", lifecycleBindingSchema),
		s.F("resolvedRange", resolvedRangeSchema),
		s.F("codex", s.Optional(s.Object(
			s.F("witnessDraftThreadId", codexThreadIDSchema),
			s.F("repairThreadId", codexThreadIDSchema),
		).Strict())),
		s.F("artifacts", s.Object(
			s.F("investigation", s.LiteralString("investigation.json")),
			s.F("frozenWitness", s.LiteralString("witness/frozen.json")),
			s.F("runs", s.Array(runArtifactSchema)),
			s.F("transitions", s.Array(transitionArtifactSchema)),
			s.F("sourceMetadata", s.LiteralString("source/metadata.json")),
			s.F("gitBundle", s.LiteralString("source/descendant.bundle")),
			s.F("rangePatch", s.LiteralString("source/range.patch")),
			s.F("verification", s.LiteralString("VERIFY.md")),
			s.F("lifecycleLedger", s.Optional(s.LiteralString("lifecycle/ledger.json"))),
		).Strict()),
	).Strict()
)
