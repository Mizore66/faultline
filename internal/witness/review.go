package witness

import (
	"encoding/base64"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

// WitnessReviewSchemaVersion is WITNESS_REVIEW_SCHEMA_VERSION
// (src/witness-review.ts).
const WitnessReviewSchemaVersion = "faultline.witness-review.v1"

// WitnessReviewError is src/witness-review.ts WitnessReviewError. Code is one
// of UNBLINDED_OR_INVALID_PROPOSAL, PROPOSAL_INTEGRITY_FAILURE,
// INVALID_REVIEW, REVIEW_CONFIRMATION_MISMATCH, or STALE_REVIEW.
type WitnessReviewError struct{ Code, Message string }

func (e *WitnessReviewError) Error() string { return e.Message }

// WitnessReviewVerification is WitnessReviewVerification.
type WitnessReviewVerification struct {
	Valid  bool
	Errors []string
}

var (
	reviewSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(WitnessReviewSchemaVersion)),
		s.F("mode", s.LiteralString("LOCAL_READ_ONLY")),
		s.F("proposal", gitproof.WitnessProposalSchema),
		s.F("safeguards", s.Object(
			s.F("packet", s.LiteralString("STRICTLY_BLINDED_PROTOCOL_SHAPE")),
			s.F("command", s.LiteralString("NOT_EXECUTED")),
			s.F("overlays", s.LiteralString("NOT_MATERIALIZED")),
			s.F("approval", s.LiteralString("EXPLICIT_HUMAN_ACTION_REQUIRED")),
			s.F("freeze", s.LiteralString("EXPLICIT_HUMAN_ACTION_REQUIRED")),
		).Strict()),
		s.F("reviewDigest", gitproof.WitnessDigestSchema),
	).Strict()

	reviewApprovalInputSchema = s.Object(
		s.F("reviewDigest", gitproof.WitnessDigestSchema),
		s.F("approvedBy", s.String(s.Trim(), s.MinLength(1), s.MaxLength(512))),
		s.F("approvedAt", s.Optional(s.String(s.Datetime()))),
		s.F("note", s.Optional(s.String(s.MaxLength(8_000)))),
	).Strict()

	reviewFreezeInputSchema = s.Object(
		s.F("reviewDigest", gitproof.WitnessDigestSchema),
		s.F("frozenAt", s.Optional(s.String(s.Datetime()))),
	).Strict()

	reviewApproveAndFreezeInputSchema = s.Object(
		s.F("reviewDigest", gitproof.WitnessDigestSchema),
		s.F("approvedBy", s.String(s.Trim(), s.MinLength(1), s.MaxLength(512))),
		s.F("approvedAt", s.Optional(s.String(s.Datetime()))),
		s.F("note", s.Optional(s.String(s.MaxLength(8_000)))),
		s.F("frozenAt", s.Optional(s.String(s.Datetime()))),
	).Strict()
)

func reviewError(code, message string) { jsexc.Throw(&WitnessReviewError{code, message}) }

// proposalIntegrityErrors ports src/witness-review.ts proposalIntegrityErrors
// for an already schema-validated proposal (so the strict-packet check cannot
// fail here), keeping the frozen error order and review-specific text.
func proposalIntegrityErrors(proposal jsjson.Value) []string {
	var errs []string
	if proposal.Get("incidentPacketDigest").Str() != jsexc.Must(canonical.DigestJSON(proposal.Get("incidentPacket"))) {
		errs = append(errs, "incident packet digest does not match the proposal packet")
	}
	if proposal.Get("witness", "commandDigest").Str() != gitproof.CommandDigest(proposal.Get("witness", "command").Str()) {
		errs = append(errs, "command digest does not match the exact UTF-8 command bytes")
	}
	overlays := proposal.Get("witness", "overlays").Items()
	seen := map[string]bool{}
	for _, overlay := range overlays {
		key := jscase.Lower(overlay.Get("path").Str())
		if seen[key] {
			errs = append(errs, jsexc.Concat("duplicate overlay path: ", overlay.Get("path").Str()))
		}
		jsexc.SetAdd(seen, key)
		decoded, _ := base64.StdEncoding.DecodeString(overlay.Get("bytesBase64").Str())
		if overlay.Get("bytesDigest").Str() != gitproof.ByteDigest(decoded) {
			errs = append(errs, jsexc.Concat("overlay byte digest does not match: ", overlay.Get("path").Str()))
		}
	}
	for i := 1; i < len(overlays); i++ {
		if canonical.LocaleCompare(overlays[i-1].Get("path").Str(), overlays[i].Get("path").Str()) > 0 {
			errs = append(errs, "overlay records are not in canonical path order")
			break
		}
	}
	if proposal.Get("witness", "overlayDigest").Str() != gitproof.OverlayDigest(overlays) {
		errs = append(errs, "overlay digest does not match the ordered overlay byte digests")
	}
	if proposal.Get("proposalDigest").Str() != jsexc.Must(canonical.DigestJSON(bundle.Without(proposal, "proposalDigest"))) {
		errs = append(errs, "proposal digest does not match the immutable proposal payload")
	}
	return errs
}

// readReviewableProposal collapses only read/schema/store errors into the
// generic refusal; integrity failures stay detailed.
func readReviewableProposal(storeDirectory, proposalID string) jsjson.Value {
	proposal, err := ReadProposal(storeDirectory, proposalID)
	if err != nil {
		reviewError("UNBLINDED_OR_INVALID_PROPOSAL",
			"Witness review refused: the stored proposal is not a valid strictly blinded protocol packet")
	}
	if errs := proposalIntegrityErrors(proposal); len(errs) > 0 {
		reviewError("PROPOSAL_INTEGRITY_FAILURE",
			jsexc.Concat("Witness review refused: proposal integrity verification failed: ", jsexc.Join(errs, "; ")))
	}
	return proposal
}

func parseAndVerifyReview(input jsjson.Value) jsjson.Value {
	review, issues, ok := s.Parse(reviewSchema, input)
	if !ok || len(issues) > 0 {
		reviewError("INVALID_REVIEW", "Witness review is not a valid local review snapshot")
	}
	if review.Get("reviewDigest").Str() != jsexc.Must(canonical.DigestJSON(bundle.Without(review, "reviewDigest"))) {
		reviewError("INVALID_REVIEW", "Witness review digest does not match its immutable review payload")
	}
	if errs := proposalIntegrityErrors(review.Get("proposal")); len(errs) > 0 {
		reviewError("INVALID_REVIEW",
			jsexc.Concat("Witness review contains an invalid proposed witness: ", jsexc.Join(errs, "; ")))
	}
	return review
}

func assertConfirmed(expected, supplied string) {
	if expected != supplied {
		reviewError("REVIEW_CONFIRMATION_MISMATCH", "Explicit review confirmation does not match the review snapshot")
	}
}

func assertFreshReview(storeDirectory string, review jsjson.Value) jsjson.Value {
	current := readReviewableProposal(storeDirectory, review.Get("proposal", "proposalId").Str())
	if jsexc.Must(canonical.CanonicalJSON(current)) != jsexc.Must(canonical.CanonicalJSON(review.Get("proposal"))) {
		reviewError("STALE_REVIEW",
			"Witness review is stale because the stored proposal no longer exactly matches the reviewed snapshot")
	}
	return current
}

// OpenWitnessReview ports openWitnessReview: a no-side-effect local review
// snapshot. It never approves, freezes, runs the command, or materializes an
// overlay.
func OpenWitnessReview(storeDirectory, proposalID string) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		proposal := readReviewableProposal(storeDirectory, proposalID)
		unsigned := jsjson.NewObj()
		unsigned.Set("schemaVersion", jsjson.MakeString(WitnessReviewSchemaVersion))
		unsigned.Set("mode", jsjson.MakeString("LOCAL_READ_ONLY"))
		unsigned.Set("proposal", proposal)
		safeguards := jsjson.NewObj()
		safeguards.Set("packet", jsjson.MakeString("STRICTLY_BLINDED_PROTOCOL_SHAPE"))
		safeguards.Set("command", jsjson.MakeString("NOT_EXECUTED"))
		safeguards.Set("overlays", jsjson.MakeString("NOT_MATERIALIZED"))
		safeguards.Set("approval", jsjson.MakeString("EXPLICIT_HUMAN_ACTION_REQUIRED"))
		safeguards.Set("freeze", jsjson.MakeString("EXPLICIT_HUMAN_ACTION_REQUIRED"))
		unsigned.Set("safeguards", jsjson.MakeObject(safeguards))
		return signed(unsigned, "reviewDigest")
	})
}

// VerifyWitnessReview ports verifyWitnessReview: it validates an exported
// snapshot without any store I/O.
func VerifyWitnessReview(input jsjson.Value) WitnessReviewVerification {
	if err := jsexc.Try(func() { parseAndVerifyReview(input) }); err != nil {
		return WitnessReviewVerification{Errors: []string{jsexc.Message(err)}}
	}
	return WitnessReviewVerification{Valid: true, Errors: []string{}}
}

func approvalFrom(parsed jsjson.Value) jsjson.Value {
	o := jsjson.NewObj()
	for _, key := range []string{"approvedBy", "approvedAt", "note"} {
		if v := parsed.Get(key); v.Kind() != jsjson.Undefined {
			o.Set(key, v)
		}
	}
	return jsjson.MakeObject(o)
}

func freezeOptionsFrom(parsed jsjson.Value) FreezeOptions {
	if at := parsed.Get("frozenAt"); at.Kind() != jsjson.Undefined {
		return FreezeOptions{FrozenAt: at.Str(), HasFrozenAt: true}
	}
	return FreezeOptions{}
}

func throwErr(err error) {
	if err != nil {
		jsexc.Throw(err)
	}
}

// ApproveWitnessReview ports approveWitnessReview: one human approval, only
// after the exact review digest is confirmed and the stored proposal is
// unchanged.
func ApproveWitnessReview(storeDirectory string, review, input jsjson.Value) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		parsedReview := parseAndVerifyReview(review)
		in := parse(input, reviewApprovalInputSchema)
		assertConfirmed(parsedReview.Get("reviewDigest").Str(), in.Get("reviewDigest").Str())
		proposal := assertFreshReview(storeDirectory, parsedReview)
		approval, err := Approve(storeDirectory, proposal.Get("proposalId").Str(), approvalFrom(in))
		throwErr(err)
		return approval
	})
}

// FreezeWitnessReview ports freezeWitnessReview.
func FreezeWitnessReview(storeDirectory string, review, input jsjson.Value) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		parsedReview := parseAndVerifyReview(review)
		in := parse(input, reviewFreezeInputSchema)
		assertConfirmed(parsedReview.Get("reviewDigest").Str(), in.Get("reviewDigest").Str())
		proposal := assertFreshReview(storeDirectory, parsedReview)
		frozen, err := Freeze(storeDirectory, proposal.Get("proposalId").Str(), freezeOptionsFrom(in))
		throwErr(err)
		return frozen
	})
}

// ApproveAndFreezeWitnessReview ports approveAndFreezeWitnessReview. Atomic
// means one user action: approval is written first and a later freeze failure
// leaves it in place (no rollback). On error both results are zero values.
func ApproveAndFreezeWitnessReview(storeDirectory string, review, input jsjson.Value) (approval, frozen jsjson.Value, err error) {
	err = jsexc.Try(func() {
		parsedReview := parseAndVerifyReview(review)
		in := parse(input, reviewApproveAndFreezeInputSchema)
		assertConfirmed(parsedReview.Get("reviewDigest").Str(), in.Get("reviewDigest").Str())
		assertFreshReview(storeDirectory, parsedReview)
		id := parsedReview.Get("proposal", "proposalId").Str()
		a, aerr := Approve(storeDirectory, id, approvalFrom(in))
		throwErr(aerr)
		f, ferr := Freeze(storeDirectory, id, freezeOptionsFrom(in))
		throwErr(ferr)
		approval, frozen = a, f
	})
	if err != nil {
		return jsjson.Value{}, jsjson.Value{}, err
	}
	return approval, frozen, nil
}
