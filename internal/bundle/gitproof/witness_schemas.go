package gitproof

import s "github.com/Mizore66/faultline/internal/schema"

// Port of src/witness-lock.ts:7-117.
const (
	maxOverlayBytesBase64 = 1_400_000
	safeProposalIDSource  = `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`
)

var (
	witnessDigestSchema = s.String(s.Regex(s.Pattern(sha256DigestSource), "expected sha256:<64 lowercase hex characters>"))
	witnessTimestamp    = s.String(s.Datetime())
	proposalIDSchema    = s.String(s.Regex(s.Pattern(safeProposalIDSource), "proposalId must be a safe filename segment"))

	overlayPathSchema = s.Refine(s.String(s.MinLength(1), s.MaxLength(512)), safePathValue, "overlay path must be a safe POSIX-relative path")
	base64BytesSchema = s.Refine(s.String(s.MaxLength(maxOverlayBytesBase64)), canonicalBase64Value, "overlay bytes must use canonical base64")

	// src/witness-lock.ts:32.
	incidentPacketSchema = s.Object(
		s.F("symptom", s.String(s.MinLength(1), s.MaxLength(32_000))),
		s.F("ciLog", s.String(s.MinLength(1), s.MaxLength(500_000))),
		s.F("repositoryLanguage", s.String(s.MinLength(1), s.MaxLength(200))),
		s.F("repositorySummary", s.String(s.MinLength(1), s.MaxLength(8_000))),
	).Strict()

	witnessPolicySchema = s.Object(
		s.F("network", s.LiteralString("disabled")),
		s.F("credentials", s.LiteralString("redacted")),
		s.F("timeoutSeconds", s.Number(s.Int(), s.Gte(1), s.Lte(3_600))),
	).Strict()

	// src/witness-lock.ts:57.
	hashedOverlaySchema = s.Object(
		s.F("path", overlayPathSchema),
		s.F("bytesBase64", base64BytesSchema),
		s.F("bytesDigest", witnessDigestSchema),
	).Strict()

	// src/witness-lock.ts:63.
	lockedWitnessSchema = s.Object(
		s.F("behavior", s.String(s.MinLength(1), s.MaxLength(16_000))),
		s.F("command", s.String(s.MinLength(1), s.MaxLength(32_000))),
		s.F("commandDigest", witnessDigestSchema),
		s.F("overlays", s.Array(hashedOverlaySchema, s.MaxItems(64))),
		s.F("overlayDigest", witnessDigestSchema),
		s.F("policy", witnessPolicySchema),
	).Strict()

	// src/witness-lock.ts:76.
	witnessProposalSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.witness-proposal.v1")),
		s.F("proposalId", proposalIDSchema),
		s.F("proposedAt", witnessTimestamp),
		s.F("proposalOrigin", s.Enum("HUMAN", "MODEL")),
		s.F("incidentPacket", incidentPacketSchema),
		s.F("incidentPacketDigest", witnessDigestSchema),
		s.F("witness", lockedWitnessSchema),
		s.F("proposalDigest", witnessDigestSchema),
	).Strict()

	// src/witness-lock.ts:93.
	witnessApprovalSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.witness-approval.v1")),
		s.F("proposalId", proposalIDSchema),
		s.F("proposalDigest", witnessDigestSchema),
		s.F("incidentPacketDigest", witnessDigestSchema),
		s.F("reviewedCommandDigest", witnessDigestSchema),
		s.F("reviewedOverlayDigest", witnessDigestSchema),
		s.F("reviewerType", s.LiteralString("HUMAN")),
		s.F("approvedBy", s.String(s.Trim(), s.MinLength(1), s.MaxLength(512))),
		s.F("approvedAt", witnessTimestamp),
		s.F("note", s.Optional(s.String(s.MaxLength(8_000)))),
		s.F("approvalDigest", witnessDigestSchema),
	).Strict()

	// FrozenWitnessSchema ports src/witness-lock.ts:107.
	FrozenWitnessSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.frozen-witness.v1")),
		s.F("frozenAt", witnessTimestamp),
		s.F("proposal", witnessProposalSchema),
		s.F("approval", witnessApprovalSchema),
		s.F("witnessDigest", witnessDigestSchema),
		s.F("frozenDigest", witnessDigestSchema),
	).Strict()
)
