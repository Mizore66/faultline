package turnproof

import (
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/turn-proof-bundle.ts:62-63.
const (
	sha256DigestSource      = `^sha256:[a-f0-9]{64}$`
	digestPinnedImageSource = `^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`
	codexThreadIDSource     = `^[A-Za-z0-9][A-Za-z0-9._:-]*$`
	treeDigestSource        = `^[a-f0-9]{40}(?:[a-f0-9]{24})?$`
)

func nonnegativeInt() s.Schema { return s.Number(s.Int(), s.Nonnegative()) }
func positiveInt() s.Schema    { return s.Number(s.Int(), s.Positive()) }

func safePathValue(v jsjson.Value) bool { return IsSafeRelativeArtifactPath(v.Str()) }

var (
	// :69-70. Unlike turninv's, this digest regex carries a message.
	digestSchema    = s.String(s.Regex(s.Pattern(sha256DigestSource), "expected sha256:<64 lowercase hex characters>"))
	timestampSchema = s.String(s.Datetime())

	// :78. zod 3 runs the refinement after a failed min or max too (the
	// status is dirty, not aborted).
	artifactPathSchema = s.Refine(s.String(s.MinLength(1), s.MaxLength(1024)), safePathValue, "artifact path must be a safe POSIX-relative path")

	// :80.
	runArtifactSchema = s.Object(
		s.F("runId", digestSchema),
		s.F("path", artifactPathSchema),
	).Strict()

	// :85.
	transitionArtifactSchema = s.Object(
		s.F("index", nonnegativeInt()),
		s.F("path", artifactPathSchema),
	).Strict()

	// LifecycleBoundSchema ports LifecycleBoundSchema (:90).
	LifecycleBoundSchema = s.Object(
		s.F("status", s.LiteralString("BOUND")),
		s.F("path", s.LiteralString("lifecycle/ledger.json")),
		s.F("ledgerDigest", digestSchema),
		s.F("headHash", digestSchema),
		s.F("transport", s.Enum("CODEX_CLI", "CODEX_APP", "SIDE_CAR", "OBSERVED_EXTERNAL_TRANSPORT")),
	).Strict()

	// SourceMetadataSchema ports TurnProofSourceMetadataSchema (:98).
	SourceMetadataSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SourceSchemaVersion)),
		s.F("trees", s.Array(s.Object(
			s.F("stateIndex", nonnegativeInt()),
			s.F("turnOrdinal", nonnegativeInt()),
			s.F("treeDigest", s.String(s.Regex(s.Pattern(treeDigestSource), ""))),
			s.F("snapshotDigest", digestSchema),
		).Strict(), s.MinItems(2))),
		s.F("pack", s.Object(
			s.F("path", s.LiteralString("source/trees.pack")),
			s.F("digest", digestSchema),
			s.F("bytes", positiveInt()),
		).Strict()),
	).Strict()

	// :123-124: no message, so zod's text is "Invalid".
	codexThreadIDSchema = s.Optional(s.String(s.MinLength(1), s.MaxLength(160), s.Regex(s.Pattern(codexThreadIDSource), "")))

	// ManifestSchema ports TurnProofBundleManifestSchema (:113).
	ManifestSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(BundleSchemaVersion)),
		s.F("generatedAt", timestampSchema),
		s.F("integrityScope", s.LiteralString("complete-declared-file-set")),
		s.F("investigationDigest", digestSchema),
		s.F("frozenDigest", digestSchema),
		s.F("witnessDigest", digestSchema),
		s.F("lifecycle", LifecycleBoundSchema),
		s.F("codex", s.Optional(s.Object(
			s.F("witnessDraftThreadId", codexThreadIDSchema),
			s.F("repairThreadId", codexThreadIDSchema),
		).Strict())),
		s.F("artifacts", s.Object(
			s.F("investigation", s.LiteralString("investigation.json")),
			s.F("frozenWitness", s.LiteralString("witness/frozen.json")),
			s.F("runs", s.Array(runArtifactSchema)),
			s.F("transitions", s.Array(transitionArtifactSchema)),
			s.F("sourceMetadata", s.LiteralString("source/trees.metadata.json")),
			s.F("treesPack", s.LiteralString("source/trees.pack")),
			s.F("verification", s.LiteralString("VERIFY.md")),
			s.F("lifecycleLedger", s.LiteralString("lifecycle/ledger.json")),
		).Strict()),
	).Strict()
)
