package gitproof

import (
	"encoding/base64"
	"regexp"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

// WitnessApproval is the `approval` member of WitnessLockVerification.
type WitnessApproval struct{ Actor, ApprovedAt string }

// WitnessVerification ports WitnessLockVerification (src/witness-lock.ts:128).
type WitnessVerification struct {
	Valid                bool
	Errors               []string
	ProposalDigest       *string
	FrozenDigest         *string
	Approval             *WitnessApproval
	ExternalDigestStatus string
}

func stringList(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = jsjson.MakeString(item)
	}
	return jsjson.MakeArray(values)
}

func nullableString(s *string) jsjson.Value {
	if s == nil {
		return jsjson.MakeNull()
	}
	return jsjson.MakeString(*s)
}

// ErrorsValue is `errors` as a JS array.
func (w WitnessVerification) ErrorsValue() jsjson.Value { return stringList(w.Errors) }

// ApprovalValue is `approval`: {actor, approvedAt} or null.
func (w WitnessVerification) ApprovalValue() jsjson.Value {
	if w.Approval == nil {
		return jsjson.MakeNull()
	}
	o := jsjson.NewObj()
	o.Set("actor", jsjson.MakeString(w.Approval.Actor))
	o.Set("approvedAt", jsjson.MakeString(w.Approval.ApprovedAt))
	return jsjson.MakeObject(o)
}

// JSON is the whole TS result object.
func (w WitnessVerification) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("valid", jsjson.MakeBool(w.Valid))
	o.Set("errors", w.ErrorsValue())
	o.Set("proposalDigest", nullableString(w.ProposalDigest))
	o.Set("frozenDigest", nullableString(w.FrozenDigest))
	o.Set("approval", w.ApprovalValue())
	o.Set("externalDigestStatus", jsjson.MakeString(w.ExternalDigestStatus))
	return jsjson.MakeObject(o)
}

// byteDigest ports src/witness-lock.ts:143 for Buffer input.
func byteDigest(b []byte) string { return "sha256:" + canonical.SHA256HexBytes(b) }

// commandDigest ports src/witness-lock.ts:147: Buffer.from(command, "utf8").
func commandDigest(command string) string { return "sha256:" + canonical.SHA256Hex(command) }

// overlayDigest ports src/witness-lock.ts:151.
func overlayDigest(overlays []jsjson.Value) string {
	items := make([]jsjson.Value, len(overlays))
	for i, overlay := range overlays {
		o := jsjson.NewObj()
		o.Set("path", overlay.Get("path"))
		o.Set("bytesDigest", overlay.Get("bytesDigest"))
		items[i] = jsjson.MakeObject(o)
	}
	return bundle.Must(canonical.DigestJSON(jsjson.MakeArray(items)))
}

// witnessPayload ports src/witness-lock.ts:170.
func witnessPayload(proposal jsjson.Value) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString("faultline.locked-witness.v1"))
	o.Set("proposalId", proposal.Get("proposalId"))
	o.Set("proposalDigest", proposal.Get("proposalDigest"))
	o.Set("incidentPacketDigest", proposal.Get("incidentPacketDigest"))
	o.Set("behavior", proposal.Get("witness", "behavior"))
	o.Set("command", proposal.Get("witness", "command"))
	o.Set("commandDigest", proposal.Get("witness", "commandDigest"))
	o.Set("overlays", proposal.Get("witness", "overlays"))
	o.Set("overlayDigest", proposal.Get("witness", "overlayDigest"))
	o.Set("policy", proposal.Get("witness", "policy"))
	return jsjson.MakeObject(o)
}

// localeLower is toLocaleLowerCase("en-US"); lone surrogates (three-byte
// WTF-8 sequences ED A0–BF xx) pass through unchanged.
func localeLower(s string) string {
	lower := cases.Lower(language.AmericanEnglish)
	var b strings.Builder
	last := 0
	for i := 0; i+2 < len(s); i++ {
		if s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF {
			b.WriteString(lower.String(s[last:i]))
			b.WriteString(s[i : i+3])
			last = i + 3
			i += 2
		}
	}
	b.WriteString(lower.String(s[last:]))
	return b.String()
}

// assertUniqueOverlayPathsForVerification ports src/witness-lock.ts:207.
func assertUniqueOverlayPathsForVerification(overlays []jsjson.Value, errs *[]string) {
	seen := map[string]bool{}
	for _, overlay := range overlays {
		key := localeLower(overlay.Get("path").Str())
		if seen[key] {
			*errs = append(*errs, "duplicate overlay path: "+overlay.Get("path").Str())
		}
		seen[key] = true
	}
}

// assertSortedOverlayPaths ports src/witness-lock.ts:196.
func assertSortedOverlayPaths(overlays []jsjson.Value, errs *[]string) {
	assertUniqueOverlayPathsForVerification(overlays, errs)
	for i := 1; i < len(overlays); i++ {
		if canonical.LocaleCompare(overlays[i-1].Get("path").Str(), overlays[i].Get("path").Str()) > 0 {
			*errs = append(*errs, "overlay records are not in canonical path order")
			return
		}
	}
}

// verifyProposal ports src/witness-lock.ts:317.
func verifyProposal(proposal jsjson.Value, errs *[]string) {
	if proposal.Get("incidentPacketDigest").Str() != bundle.Must(canonical.DigestJSON(proposal.Get("incidentPacket"))) {
		*errs = append(*errs, "incident packet digest does not match the persisted packet")
	}
	if proposal.Get("witness", "commandDigest").Str() != commandDigest(proposal.Get("witness", "command").Str()) {
		*errs = append(*errs, "command digest does not match the exact UTF-8 command bytes")
	}
	overlays := proposal.Get("witness", "overlays").Items()
	assertSortedOverlayPaths(overlays, errs)
	for _, overlay := range overlays {
		decoded, _ := base64.StdEncoding.DecodeString(overlay.Get("bytesBase64").Str())
		if overlay.Get("bytesDigest").Str() != byteDigest(decoded) {
			*errs = append(*errs, "overlay byte digest does not match: "+overlay.Get("path").Str())
		}
	}
	if proposal.Get("witness", "overlayDigest").Str() != overlayDigest(overlays) {
		*errs = append(*errs, "overlay digest does not match the ordered overlay byte digests")
	}
	if proposal.Get("proposalDigest").Str() != bundle.Must(canonical.DigestJSON(bundle.Without(proposal, "proposalDigest"))) {
		*errs = append(*errs, "proposal digest does not match the immutable proposal payload")
	}
}

// verifyApproval ports src/witness-lock.ts:339.
func verifyApproval(approval, proposal jsjson.Value, errs *[]string) {
	add := func(s string) { *errs = append(*errs, s) }
	if approval.Get("proposalId").Str() != proposal.Get("proposalId").Str() {
		add("approval proposalId does not match proposal")
	}
	if approval.Get("proposalDigest").Str() != proposal.Get("proposalDigest").Str() {
		add("approval proposal digest does not match proposal")
	}
	if approval.Get("incidentPacketDigest").Str() != proposal.Get("incidentPacketDigest").Str() {
		add("approval incident packet digest does not match proposal")
	}
	if approval.Get("reviewedCommandDigest").Str() != proposal.Get("witness", "commandDigest").Str() {
		add("approval command digest does not match proposal")
	}
	if approval.Get("reviewedOverlayDigest").Str() != proposal.Get("witness", "overlayDigest").Str() {
		add("approval overlay digest does not match proposal")
	}
	if approval.Get("approvalDigest").Str() != bundle.Must(canonical.DigestJSON(bundle.Without(approval, "approvalDigest"))) {
		add("approval digest does not match the human review record")
	}
}

var sha256DigestPattern = regexp.MustCompile(sha256DigestSource)

// verifyFrozenRecord ports src/witness-lock.ts:350.
func verifyFrozenRecord(frozen jsjson.Value, expected string, expectedProvided bool) WitnessVerification {
	var errs []string
	verifyProposal(frozen.Get("proposal"), &errs)
	verifyApproval(frozen.Get("approval"), frozen.Get("proposal"), &errs)
	if frozen.Get("witnessDigest").Str() != bundle.Must(canonical.DigestJSON(witnessPayload(frozen.Get("proposal")))) {
		errs = append(errs, "frozen witness digest does not match the reviewed command and overlay bytes")
	}
	if frozen.Get("frozenDigest").Str() != bundle.Must(canonical.DigestJSON(bundle.Without(frozen, "frozenDigest"))) {
		errs = append(errs, "frozen digest does not match the immutable freeze record")
	}
	status := "NOT_PROVIDED"
	if expectedProvided {
		switch {
		case !sha256DigestPattern.MatchString(expected):
			errs = append(errs, "expected frozen digest is not a valid sha256 digest")
			status = "MISMATCH"
		case expected == frozen.Get("frozenDigest").Str():
			status = "MATCH"
		default:
			errs = append(errs, "externally supplied frozen digest does not match")
			status = "MISMATCH"
		}
	}
	proposalDigest := frozen.Get("proposal", "proposalDigest").Str()
	frozenDigest := frozen.Get("frozenDigest").Str()
	return WitnessVerification{
		Valid:                len(errs) == 0,
		Errors:               errs,
		ProposalDigest:       &proposalDigest,
		FrozenDigest:         &frozenDigest,
		Approval:             &WitnessApproval{Actor: frozen.Get("approval", "approvedBy").Str(), ApprovedAt: frozen.Get("approval", "approvedAt").Str()},
		ExternalDigestStatus: status,
	}
}

// VerifyFrozenWitnessRecord ports verifyFrozenWitnessRecord (src/witness-lock.ts:467).
func VerifyFrozenWitnessRecord(input jsjson.Value, expected string, expectedProvided bool) WitnessVerification {
	parsed, issues, ok := schema.Parse(FrozenWitnessSchema, input)
	if !ok {
		status := "NOT_PROVIDED"
		if expectedProvided {
			status = "MISMATCH"
		}
		return WitnessVerification{
			Errors:               []string{"frozen witness schema validation failed: " + schema.ErrorMessage(issues)},
			ExternalDigestStatus: status,
		}
	}
	return verifyFrozenRecord(parsed, expected, expectedProvided)
}
