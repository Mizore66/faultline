// Package incident is the Go port of the incident intake path: the
// review-only draft (src/incident.ts), its write-once store
// (src/incident-store.ts), and the CLI-facing helpers built on them.
//
// Internally the port keeps TS's throw/catch control flow with jsexc; the
// exported functions return the thrown error instead.
package incident

import (
	"strings"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	s "github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/witness"
)

// DraftSchemaVersion ports INCIDENT_DRAFT_SCHEMA_VERSION (src/incident.ts:9).
const DraftSchemaVersion = "faultline.incident-draft.v1"

// SafeGitRevision ports SAFE_GIT_REVISION (src/incident.ts:13),
// /^(?!-)[^\0\r\n]{1,512}$/: the length is counted in UTF-16 units and the
// first unit must not be '-'.
func SafeGitRevision(value string) bool {
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

// isTagFreeDigestImage ports src/incident.ts:20. DIGEST_PINNED_IMAGE admits
// ASCII only, so byte and UTF-16 indexes agree.
func isTagFreeDigestImage(value string) bool {
	if !gitinv.DigestPinnedImage(value) {
		return false
	}
	repository := value[:strings.IndexByte(value, '@')]
	// A colon after the last slash is a mutable image tag. A registry port is
	// before the first slash and remains valid in a digest-only reference.
	return strings.LastIndexByte(repository, ':') <= strings.LastIndexByte(repository, '/')
}

// hasNonWhitespace is /\S/.test(value).
func hasNonWhitespace(value string) bool {
	for _, u := range jsstr.ToUTF16(value) {
		if !jsstr.IsWhitespace(u) {
			return true
		}
	}
	return false
}

func strValue(pred func(string) bool) func(jsjson.Value) bool {
	return func(v jsjson.Value) bool { return pred(v.Str()) }
}

var (
	digestSchema    = gitproof.WitnessDigestSchema // DigestSchema, src/incident.ts:16
	timestampSchema = s.String(s.Datetime())       // TimestampSchema, src/incident.ts:17

	// CommandSchema ports IncidentCommandSchema (src/incident.ts:33): a
	// stored opaque shell command, not an argv parser.
	CommandSchema = s.Refine(
		s.Refine(s.String(s.MinLength(1), s.MaxLength(32_000)),
			strValue(func(v string) bool { return !strings.Contains(v, "\x00") }), "command must not contain a NUL byte"),
		strValue(hasNonWhitespace), "command must contain a non-whitespace character")

	// RepositorySchema ports IncidentRepositorySchema (src/incident.ts:40).
	RepositorySchema = s.Refine(
		s.Refine(s.String(s.MinLength(1), s.MaxLength(16_384)),
			strValue(func(v string) bool { return !strings.ContainsAny(v, "\x00\r\n") }), "repository must not contain NUL or line-break characters"),
		strValue(hasNonWhitespace), "repository must contain a non-whitespace character")

	// RevisionSchema ports IncidentRevisionSchema (src/incident.ts:47).
	RevisionSchema = s.String(s.Regex(SafeGitRevision, "revision must be non-empty, cannot start with '-', and cannot contain NUL or line breaks"))

	// explicitRangeSchema ports ExplicitIncidentRangeSchema (src/incident.ts:50).
	explicitRangeSchema = s.Object(
		s.F("ancestor", RevisionSchema),
		s.F("descendant", RevisionSchema),
	).Strict()

	// RuntimeSchema ports IncidentRuntimeSchema (src/incident.ts:56).
	RuntimeSchema = s.Object(
		s.F("requested", s.String(s.MinLength(1), s.MaxLength(256))),
		s.F("image", s.Refine(s.String(), strValue(isTagFreeDigestImage), "runtime image must be a tag-free digest-pinned reference")),
	).Strict()

	// ProposalBindingSchema ports IncidentProposalBindingSchema (src/incident.ts:62).
	ProposalBindingSchema = s.Object(
		s.F("proposalId", witness.IncidentDraftIDSchema),
		s.F("proposalDigest", digestSchema),
		s.F("incidentPacketDigest", digestSchema),
		s.F("commandDigest", digestSchema),
	).Strict()

	// localHeadFactsSchema ports LocalHeadFactsSchema (src/incident.ts:74).
	localHeadFactsSchema = s.Object(
		s.F("head", RevisionSchema),
		s.F("parents", s.Array(RevisionSchema, s.MaxItems(16))),
	).Strict()

	// rangeFactsSchema ports IncidentRangeFactsSchema (src/incident.ts:79).
	rangeFactsSchema = s.Object(
		s.F("range", s.Optional(explicitRangeSchema)),
		s.F("localHead", s.Optional(localHeadFactsSchema)),
	).Strict()

	// ResolvedRangeSchema ports ResolvedIncidentRangeSchema (src/incident.ts:84).
	ResolvedRangeSchema = explicitRangeSchema.Extend(
		s.F("source", s.Enum("EXPLICIT", "LOCAL_HEAD_PARENT")),
	).Strict()

	// draftInputSchema ports IncidentDraftInputSchema (src/incident.ts:88).
	draftInputSchema = s.Object(
		s.F("draftId", witness.IncidentDraftIDSchema),
		s.F("createdAt", timestampSchema),
		s.F("repository", RepositorySchema),
		s.F("command", CommandSchema),
		s.F("range", s.Optional(explicitRangeSchema)),
		s.F("localHead", s.Optional(localHeadFactsSchema)),
		s.F("runtime", s.Optional(RuntimeSchema)),
		s.F("proposal", s.Optional(ProposalBindingSchema)),
	).Strict()

	// reviewWithoutProposalSchema ports src/incident.ts:99.
	reviewWithoutProposalSchema = s.Object(
		s.F("required", s.Literal(jsjson.MakeBool(true))),
		s.F("state", s.LiteralString("PENDING_HUMAN_REVIEW")),
		s.F("witnessState", s.LiteralString("NOT_PROPOSED")),
		s.F("freezeState", s.LiteralString("NOT_FROZEN")),
		s.F("autoFreeze", s.Literal(jsjson.MakeBool(false))),
	).Strict()

	// reviewWithProposalSchema ports src/incident.ts:107.
	reviewWithProposalSchema = s.Object(
		s.F("required", s.Literal(jsjson.MakeBool(true))),
		s.F("state", s.LiteralString("PENDING_HUMAN_REVIEW")),
		s.F("witnessState", s.LiteralString("PROPOSED")),
		s.F("freezeState", s.LiteralString("NOT_FROZEN")),
		s.F("autoFreeze", s.Literal(jsjson.MakeBool(false))),
		s.F("proposal", ProposalBindingSchema),
	).Strict()

	// reviewSchema ports IncidentReviewSchema (src/incident.ts:116), a plain
	// z.union.
	reviewSchema = s.Union(reviewWithoutProposalSchema, reviewWithProposalSchema)

	// DraftSchema ports IncidentDraftSchema (src/incident.ts:118).
	DraftSchema s.Schema = s.Object(
		s.F("schemaVersion", s.LiteralString(DraftSchemaVersion)),
		s.F("draftId", witness.IncidentDraftIDSchema),
		s.F("createdAt", timestampSchema),
		s.F("status", s.LiteralString("DRAFT_REQUIRES_HUMAN_REVIEW")),
		s.F("repository", RepositorySchema),
		// Exact pasted command bytes, stored without shell escaping or execution.
		s.F("command", CommandSchema),
		s.F("commandDigest", digestSchema),
		s.F("range", ResolvedRangeSchema),
		s.F("runtime", s.Optional(RuntimeSchema)),
		s.F("review", reviewSchema),
		s.F("draftDigest", digestSchema),
	).Strict()
)
