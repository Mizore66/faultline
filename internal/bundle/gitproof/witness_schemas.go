package gitproof

import s "github.com/Mizore66/faultline/internal/schema"

// Port of src/witness-lock.ts:7-117.
const (
	maxOverlayBytesBase64 = 1_400_000
	safeProposalIDSource  = `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`
)

var (
	// WitnessDigestSchema is DigestSchema (src/witness-lock.ts:10).
	WitnessDigestSchema = s.String(s.Regex(s.Pattern(sha256DigestSource), "expected sha256:<64 lowercase hex characters>"))
	witnessTimestamp    = s.String(s.Datetime())
	// ProposalIDSchema ports src/witness-lock.ts:12.
	ProposalIDSchema = s.String(s.Regex(s.Pattern(safeProposalIDSource), "proposalId must be a safe filename segment"))

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
		s.F("bytesDigest", WitnessDigestSchema),
	).Strict()

	// src/witness-lock.ts:63.
	lockedWitnessSchema = s.Object(
		s.F("behavior", s.String(s.MinLength(1), s.MaxLength(16_000))),
		s.F("command", s.String(s.MinLength(1), s.MaxLength(32_000))),
		s.F("commandDigest", WitnessDigestSchema),
		s.F("overlays", s.Array(hashedOverlaySchema, s.MaxItems(64))),
		s.F("overlayDigest", WitnessDigestSchema),
		s.F("policy", witnessPolicySchema),
	).Strict()

	// WitnessProposalSchema ports src/witness-lock.ts:76.
	WitnessProposalSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.witness-proposal.v1")),
		s.F("proposalId", ProposalIDSchema),
		s.F("proposedAt", witnessTimestamp),
		s.F("proposalOrigin", s.Enum("HUMAN", "MODEL")),
		s.F("incidentPacket", incidentPacketSchema),
		s.F("incidentPacketDigest", WitnessDigestSchema),
		s.F("witness", lockedWitnessSchema),
		s.F("proposalDigest", WitnessDigestSchema),
	).Strict()

	// WitnessApprovalSchema ports src/witness-lock.ts:93.
	WitnessApprovalSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.witness-approval.v1")),
		s.F("proposalId", ProposalIDSchema),
		s.F("proposalDigest", WitnessDigestSchema),
		s.F("incidentPacketDigest", WitnessDigestSchema),
		s.F("reviewedCommandDigest", WitnessDigestSchema),
		s.F("reviewedOverlayDigest", WitnessDigestSchema),
		s.F("reviewerType", s.LiteralString("HUMAN")),
		s.F("approvedBy", s.String(s.Trim(), s.MinLength(1), s.MaxLength(512))),
		s.F("approvedAt", witnessTimestamp),
		s.F("note", s.Optional(s.String(s.MaxLength(8_000)))),
		s.F("approvalDigest", WitnessDigestSchema),
	).Strict()

	// FrozenWitnessSchema ports src/witness-lock.ts:107.
	FrozenWitnessSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.frozen-witness.v1")),
		s.F("frozenAt", witnessTimestamp),
		s.F("proposal", WitnessProposalSchema),
		s.F("approval", WitnessApprovalSchema),
		s.F("witnessDigest", WitnessDigestSchema),
		s.F("frozenDigest", WitnessDigestSchema),
	).Strict()

	// WitnessProposalInputSchema ports src/witness-lock.ts:40.
	WitnessProposalInputSchema = s.Object(
		s.F("proposalId", ProposalIDSchema),
		s.F("incidentPacket", incidentPacketSchema),
		s.F("proposalOrigin", s.Optional(s.Enum("HUMAN", "MODEL"))),
		s.F("proposedAt", s.Optional(witnessTimestamp)),
		s.F("witness", s.Object(
			s.F("behavior", s.String(s.MinLength(1), s.MaxLength(16_000))),
			s.F("command", s.String(s.MinLength(1), s.MaxLength(32_000))),
			s.F("overlays", s.Array(s.Object(
				s.F("path", overlayPathSchema),
				s.F("bytesBase64", base64BytesSchema),
			).Strict(), s.MaxItems(64))),
			s.F("policy", witnessPolicySchema),
		).Strict()),
	).Strict()

	// HumanApprovalInputSchema ports src/witness-lock.ts:90.
	HumanApprovalInputSchema = s.Object(
		s.F("approvedBy", s.String(s.Trim(), s.MinLength(1), s.MaxLength(512))),
		s.F("approvedAt", s.Optional(witnessTimestamp)),
		s.F("note", s.Optional(s.String(s.MaxLength(8_000)))),
	).Strict()

	// FreezeOptionsSchema ports src/witness-lock.ts:119.
	FreezeOptionsSchema = s.Object(
		s.F("frozenAt", s.Optional(witnessTimestamp)),
	).Strict()
)
