package witness

import (
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	s "github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/v8sort"
)

// Port of src/standing-approval-policy.ts:12.
const standingPolicySchemaVersion = "faultline.standing-approval-policy.v1"

var (
	// Port of src/standing-approval-policy.ts:17-26.
	policyTimestamp = s.String(s.Datetime())
	// idSchema is /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/ with zod's default message.
	idSchema        = s.String(s.Regex(s.Pattern(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`), ""))
	overlayTemplate = s.Object(
		s.F("path", s.String(s.MinLength(1), s.MaxLength(4_096))),
		s.F("bytesBase64", s.String(s.MaxLength(2_000_000))),
	).Strict()

	// policyFields are the StandingApprovalPolicySchema fields before
	// policyDigest (src/standing-approval-policy.ts:34).
	policyFields = []s.Field{
		s.F("schemaVersion", s.LiteralString(standingPolicySchemaVersion)),
		s.F("policyId", idSchema),
		s.F("frozenBy", s.String(s.Trim(), s.MinLength(1), s.MaxLength(512))),
		s.F("frozenAt", policyTimestamp),
		s.F("allowedCommands", s.Array(s.String(s.MinLength(1), s.MaxLength(32_000)), s.MinItems(1), s.MaxItems(64))),
		s.F("allowedOverlayTemplates", s.Array(overlayTemplate, s.MaxItems(64))),
		s.F("maxTimeoutSeconds", s.Number(s.Int(), s.Gte(1), s.Lte(3_600))),
		s.F("network", s.LiteralString("disabled")),
		s.F("credentials", s.LiteralString("redacted")),
	}
	standingPolicySchema = s.Object(append(append([]s.Field{}, policyFields...), s.F("policyDigest", gitproof.WitnessDigestSchema))...).Strict()
	// policyInputSchema is the schema minus policyDigest, extended with an optional one.
	policyInputSchema = s.Object(append(append([]s.Field{}, policyFields...), s.F("policyDigest", s.Optional(gitproof.WitnessDigestSchema)))...).Strict()
)

// BuildPolicy ports buildStandingApprovalPolicy
// (src/standing-approval-policy.ts:61): it validates, sorts the overlay
// templates and binds the digest.
func BuildPolicy(input jsjson.Value) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value { return buildPolicy(input) })
}

func buildPolicy(input jsjson.Value) jsjson.Value {
	parsed := parse(input, policyInputSchema)
	templates := append([]jsjson.Value{}, parsed.Get("allowedOverlayTemplates").Items()...)
	v8sort.Sort(templates, func(a, b jsjson.Value) int {
		if c := canonical.LocaleCompare(a.Get("path").Str(), b.Get("path").Str()); c != 0 {
			return c
		}
		return canonical.LocaleCompare(a.Get("bytesBase64").Str(), b.Get("bytesBase64").Str())
	})
	unsigned := jsjson.NewObj()
	for _, f := range policyFields {
		unsigned.Set(f.Key, parsed.Get(f.Key))
	}
	unsigned.Set("allowedCommands", jsjson.MakeArray(append([]jsjson.Value{}, parsed.Get("allowedCommands").Items()...)))
	unsigned.Set("allowedOverlayTemplates", jsjson.MakeArray(templates))
	return signed(unsigned, "policyDigest")
}

func overlayKey(path, bytesBase64 string) string { return jsexc.Concat(path, "\x00", bytesBase64) }

// MatchPolicy ports matchStandingApprovalPolicy
// (src/standing-approval-policy.ts:80). The reasons are nil on a match.
func MatchPolicy(proposal, policy jsjson.Value) (bool, []string) {
	var reasons []string
	policyOf := proposal.Get("witness", "policy")
	if policy.Get("network").Str() != "disabled" {
		reasons = append(reasons, "policy network must be disabled")
	}
	if policyOf.Get("network").Str() != "disabled" {
		reasons = append(reasons, "proposal network is not disabled")
	}
	if policyOf.Get("credentials").Str() != "redacted" {
		reasons = append(reasons, "proposal credentials are not redacted")
	}
	timeout, ceiling := policyOf.Get("timeoutSeconds").Num(), policy.Get("maxTimeoutSeconds").Num()
	if timeout > ceiling {
		reasons = append(reasons, jsexc.Concat("proposal timeout ", jsjson.FormatNumber(timeout), "s exceeds policy ceiling ", jsjson.FormatNumber(ceiling), "s"))
	}
	allowed := false
	for _, command := range policy.Get("allowedCommands").Items() {
		if command.Str() == proposal.Get("witness", "command").Str() {
			allowed = true
		}
	}
	if !allowed {
		reasons = append(reasons, "proposal command is not an exact allowlist match")
	}

	templates := policy.Get("allowedOverlayTemplates").Items()
	overlays := proposal.Get("witness", "overlays").Items()
	if len(templates) == 0 {
		if len(overlays) > 0 {
			reasons = append(reasons, "policy allows zero overlays but the proposal includes overlays")
		}
	} else if len(overlays) != len(templates) {
		reasons = append(reasons, jsexc.Concat("overlay count ", jsjson.FormatNumber(float64(len(overlays))),
			" does not match policy template count ", jsjson.FormatNumber(float64(len(templates)))))
	} else {
		set := map[string]bool{}
		for _, item := range templates {
			set[overlayKey(item.Get("path").Str(), item.Get("bytesBase64").Str())] = true
		}
		for _, overlay := range overlays {
			if !set[overlayKey(overlay.Get("path").Str(), overlay.Get("bytesBase64").Str())] {
				reasons = append(reasons, jsexc.Concat("overlay does not byte-match a policy template: ", overlay.Get("path").Str()))
			}
		}
	}
	return len(reasons) == 0, reasons
}

// PolicyApprovedBy ports policyApprovedBy (src/standing-approval-policy.ts:117).
func PolicyApprovedBy(policy jsjson.Value) string {
	return jsexc.Concat("policy:", policy.Get("policyDigest").Str())
}

// ApplyOptions is applyStandingApprovalPolicy's options object: nil is undefined.
type ApplyOptions struct {
	ApprovedAt, FrozenAt, Note *string
}

// ApplyPolicy ports applyStandingApprovalPolicy
// (src/standing-approval-policy.ts:126): the same approval and freeze records
// as the human path, with approvedBy set to policy:<digest>.
func ApplyPolicy(store, id string, policy jsjson.Value, o ApplyOptions) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value { return applyPolicy(store, id, policy, o) })
}

func applyPolicy(store, id string, policyInput jsjson.Value, o ApplyOptions) jsjson.Value {
	policy := parse(policyInput, standingPolicySchema)
	unsigned := jsjson.NewObj()
	for _, f := range policyFields {
		unsigned.Set(f.Key, policy.Get(f.Key))
	}
	recomputed := buildPolicy(jsjson.MakeObject(unsigned))
	if recomputed.Get("policyDigest").Str() != policy.Get("policyDigest").Str() {
		throw("Standing approval policy digest does not match its canonical contents")
	}
	proposal := jsexc.Must(ReadProposal(store, id))
	if matches, reasons := MatchPolicy(proposal, policy); !matches {
		throw(jsexc.Concat("Standing approval refused: ", jsexc.Join(reasons, "; ")))
	}
	approval := jsjson.NewObj()
	approval.Set("approvedBy", jsjson.MakeString(PolicyApprovedBy(policy)))
	if o.ApprovedAt != nil {
		approval.Set("approvedAt", jsjson.MakeString(*o.ApprovedAt))
	}
	if o.Note != nil {
		approval.Set("note", jsjson.MakeString(*o.Note))
	} else {
		approval.Set("note", jsjson.MakeString(jsexc.Concat("standing-approval ", policy.Get("policyId").Str())))
	}
	jsexc.Must(Approve(store, id, jsjson.MakeObject(approval)))
	freeze := FreezeOptions{}
	if o.FrozenAt != nil {
		freeze = FreezeOptions{FrozenAt: *o.FrozenAt, HasFrozenAt: true}
	}
	return jsexc.Must(Freeze(store, id, freeze))
}

// Port of src/standing-approval-policy.ts:151 ensurePolicyDirectory.
func ensurePolicyDirectory(store string) string {
	root := nodefs.Resolve(store)
	directory := nodefs.Join(root, "standing-policies")
	if !nodefs.Exists(directory) {
		if err := nodefs.MkdirAll(directory, 0o700); err != nil {
			jsexc.Throw(err)
		}
	}
	safe, ok := safeSegment(directory)
	if !ok {
		throw("Standing policy store must be a real directory")
	}
	return safe
}

// policyPath resolves <id>.json under directory, refusing an escape.
func policyPath(directory, policyID string) string {
	path := nodefs.Resolve(directory, jsexc.Concat(policyID, ".json"))
	nested := jsexc.Must(safedir.RelativeTrustedSystemPath(directory, path))
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) {
		throw("Standing policy path escaped its store")
	}
	return path
}

// WritePolicy ports writeStandingApprovalPolicy
// (src/standing-approval-policy.ts:159): the policy is rebuilt (so a built
// policy round-trips), then written once as pretty JSON with mode 0600. It
// returns the path written. An existing policy file is Node's raw EEXIST.
func WritePolicy(store string, policyInput jsjson.Value) (string, error) {
	var path string
	err := jsexc.Try(func() {
		policy := buildPolicy(policyInput)
		directory := ensurePolicyDirectory(store)
		path = policyPath(directory, policy.Get("policyId").Str())
		text := jsexc.Concat(jsjson.StringifyIndent(policy, "  "), "\n")
		if err := nodefs.WriteFileUTF8(path, text, 0o600, true); err != nil {
			jsexc.Throw(err)
		}
	})
	return path, err
}

// ReadPolicy ports readStandingApprovalPolicy
// (src/standing-approval-policy.ts:171). It creates the policy directory: a
// read that writes.
func ReadPolicy(store, policyID string) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		directory := ensurePolicyDirectory(store)
		path := policyPath(directory, policyID)
		info := jsexc.Must(nodefs.Lstat(path))
		if !info.Mode().IsRegular() {
			throw("Standing policy must be a regular file")
		}
		return parse(jsexc.Must(bundle.ParseJSONFile(path)), standingPolicySchema)
	})
}

// AutoFreeze is tryAutoFreezeFromStandingPolicy's result: Frozen is nil on a
// non-match, when Reasons says why.
type AutoFreeze struct {
	Frozen  *jsjson.Value
	Policy  jsjson.Value
	Reasons []string
}

// TryAutoFreeze ports tryAutoFreezeFromStandingPolicy
// (src/standing-approval-policy.ts:187).
func TryAutoFreeze(store, proposalID, policyID string) (AutoFreeze, error) {
	var result AutoFreeze
	err := jsexc.Try(func() {
		policy := jsexc.Must(ReadPolicy(store, policyID))
		proposal := jsexc.Must(ReadProposal(store, proposalID))
		if matches, reasons := MatchPolicy(proposal, policy); !matches {
			result = AutoFreeze{Reasons: reasons}
			return
		}
		frozen := jsexc.Must(ApplyPolicy(store, proposalID, policy, ApplyOptions{}))
		result = AutoFreeze{Frozen: &frozen, Policy: policy}
	})
	return result, err
}
