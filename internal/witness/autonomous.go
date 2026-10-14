package witness

import (
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/evidence"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	s "github.com/Mizore66/faultline/internal/schema"
	"strings"
)

// Port of src/autonomous-session.ts:28-30.
const (
	approvedAfterExecution       = "APPROVED_AFTER_EXECUTION"
	parkedIncidentSchemaVersion  = "faultline.parked-incident.v1"
	ratificationSchemaVersion    = "faultline.witness-ratification.v1"
	defaultRatificationNote      = "human owned the question after execution; digests identical"
	parkedNotificationFormatHead = "Parked AGENT_DRAFT "
)

var (
	// Port of src/autonomous-session.ts:36-55.
	parkedIncidentSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(parkedIncidentSchemaVersion)),
		s.F("status", s.LiteralString("PARKED")),
		s.F("evidenceGrade", s.LiteralString(evidence.AgentDraftEvidenceGrade)),
		s.F("evidenceLabel", s.LiteralString(evidence.AgentDraftEvidenceLabel)),
		s.F("proposalId", idSchema),
		s.F("proposalDigest", gitproof.WitnessDigestSchema),
		s.F("witnessDigest", gitproof.WitnessDigestSchema),
		s.F("parkedAt", policyTimestamp),
		s.F("notification", s.String(s.MinLength(1), s.MaxLength(4_096))),
		s.F("blocking", s.Literal(jsjson.MakeBool(false))),
		s.F("parkDigest", gitproof.WitnessDigestSchema),
	).Strict()

	witnessRatificationSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(ratificationSchemaVersion)),
		s.F("recordKind", s.LiteralString(approvedAfterExecution)),
		s.F("proposalId", idSchema),
		s.F("executedWitnessDigest", gitproof.WitnessDigestSchema),
		s.F("ratifiedWitnessDigest", gitproof.WitnessDigestSchema),
		s.F("ratifiedBy", s.String(s.Trim(), s.MinLength(1), s.MaxLength(512))),
		s.F("ratifiedAt", policyTimestamp),
		s.F("note", s.Optional(s.String(s.MaxLength(8_000)))),
		s.F("ratificationDigest", gitproof.WitnessDigestSchema),
	).Strict()
)

// AutonomousSessionError ports the class of the same name
// (src/autonomous-session.ts:67); Code is DIGEST_MISMATCH,
// PROOF_EXPORT_REFUSED, MISSING_DRAFT or ALREADY_RATIFIED.
type AutonomousSessionError struct{ Code, Message string }

func (e *AutonomousSessionError) Error() string { return e.Message }

// Port of src/autonomous-session.ts:92 ensureStoreDir.
func ensureStoreDir(store, segment string) string {
	root := nodefs.Resolve(store)
	directory := nodefs.Join(root, segment)
	if !nodefs.Exists(directory) {
		if err := nodefs.MkdirAll(directory, 0o700); err != nil {
			jsexc.Throw(err)
		}
	}
	safe, ok := safeSegment(directory)
	if !ok {
		throw(jsexc.Concat("Autonomous session store must be a real directory: ", directory))
	}
	return safe
}

// Port of src/autonomous-session.ts:100 recordPath. The directory is created
// before the id is validated.
func recordPath(store, segment, id string) string {
	directory := ensureStoreDir(store, segment)
	path := nodefs.Resolve(directory, jsexc.Concat(parse(jsjson.MakeString(id), idSchema).Str(), ".json"))
	nested := jsexc.Must(safedir.RelativeTrustedSystemPath(directory, path))
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) {
		throw("Autonomous session path escaped its store")
	}
	return path
}

// Port of src/autonomous-session.ts:109 witnessDigestFromProposal.
func witnessDigestFromProposal(proposal jsjson.Value) string {
	return jsexc.Must(canonical.DigestJSON(gitproof.WitnessPayload(proposal)))
}

// Port of src/autonomous-session.ts:143 agentDraftAnnotation: the keys are
// spread into the caller's object.
func setAgentDraftAnnotation(o *jsjson.Obj) {
	o.Set("evidenceGrade", jsjson.MakeString(evidence.AgentDraftEvidenceGrade))
	o.Set("evidenceLabel", jsjson.MakeString(evidence.AgentDraftEvidenceLabel))
}

// writePretty writes `${JSON.stringify(v, null, 2)}\n` with flag wx, mode 0600.
func writePretty(path string, v jsjson.Value) error {
	return nodefs.WriteFileUTF8(path, jsexc.Concat(jsjson.StringifyIndent(v, "  "), "\n"), 0o600, true)
}

// Port of src/autonomous-session.ts:154 parkUnmatchedProposal.
func parkUnmatchedProposal(store, proposalID string) jsjson.Value {
	proposal := jsexc.Must(ReadProposal(store, proposalID))
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", jsjson.MakeString(parkedIncidentSchemaVersion))
	unsigned.Set("status", jsjson.MakeString("PARKED"))
	setAgentDraftAnnotation(unsigned)
	unsigned.Set("proposalId", proposal.Get("proposalId"))
	unsigned.Set("proposalDigest", proposal.Get("proposalDigest"))
	unsigned.Set("witnessDigest", jsjson.MakeString(witnessDigestFromProposal(proposal)))
	unsigned.Set("parkedAt", jsjson.MakeString(now()))
	unsigned.Set("notification", jsjson.MakeString(jsexc.Concat(parkedNotificationFormatHead, proposal.Get("proposalId").Str(),
		": no standing-policy match. Session continues; human review required before proof.")))
	unsigned.Set("blocking", jsjson.MakeBool(false))
	parked := parse(signed(unsigned, "parkDigest"), parkedIncidentSchema)
	path := recordPath(store, "parked", proposalID)
	if err := writePretty(path, parked); err != nil {
		jsexc.Throw(err)
	}
	return parked
}

// ResolveAutonomous ports resolveAutonomousProposal
// (src/autonomous-session.ts:196): auto-freeze on a standing-policy match,
// otherwise park as AGENT_DRAFT without blocking the session. policy nil is
// TS's null.
func ResolveAutonomous(store, id string, policy *jsjson.Value) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		proposal := jsexc.Must(ReadProposal(store, id))
		if policy != nil {
			if matches, _ := MatchPolicy(proposal, *policy); matches {
				approval := jsjson.NewObj()
				approval.Set("approvedBy", jsjson.MakeString(PolicyApprovedBy(*policy)))
				approval.Set("note", jsjson.MakeString(jsexc.Concat("standing-approval ", policy.Get("policyId").Str())))
				jsexc.Must(Approve(store, id, jsjson.MakeObject(approval)))
				frozen := jsexc.Must(Freeze(store, id, FreezeOptions{}))
				return obj3("outcome", jsjson.MakeString("FROZEN_BY_POLICY"), "frozen", frozen,
					"notification", jsjson.MakeString(jsexc.Concat("Standing policy ", policy.Get("policyId").Str(), " auto-froze ", id, ".")))
			}
		}
		parked := parkUnmatchedProposal(store, id)
		return obj3("outcome", jsjson.MakeString("PARKED_AGENT_DRAFT"), "parked", parked, "notification", parked.Get("notification"))
	})
}

func obj3(k1 string, v1 jsjson.Value, k2 string, v2 jsjson.Value, k3 string, v3 jsjson.Value) jsjson.Value {
	o := jsjson.NewObj()
	o.Set(k1, v1)
	o.Set(k2, v2)
	o.Set(k3, v3)
	return jsjson.MakeObject(o)
}

// RatifyOptions is ratifyExecutedWitness's input: nil is undefined.
type RatifyOptions struct {
	ExecutedWitnessDigest string
	RatifiedBy            string
	RatifiedAt            *string
	Note                  *string
}

// Ratify ports ratifyExecutedWitness (src/autonomous-session.ts:238). The
// result is { ratification, frozen }.
func Ratify(store, id string, o RatifyOptions) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		proposal := jsexc.Must(ReadProposal(store, id))
		currentDigest := witnessDigestFromProposal(proposal)
		if o.ExecutedWitnessDigest != currentDigest {
			jsexc.Throw(&AutonomousSessionError{"DIGEST_MISMATCH",
				"Ratification refused: executed witness digest does not match the stored proposal witness digest."})
		}
		if _, _, ok := s.Parse(gitproof.WitnessDigestSchema, jsjson.MakeString(o.ExecutedWitnessDigest)); !ok {
			jsexc.Throw(&AutonomousSessionError{"DIGEST_MISMATCH", "Ratification refused: executed witness digest is malformed."})
		}

		unsigned := jsjson.NewObj()
		unsigned.Set("schemaVersion", jsjson.MakeString(ratificationSchemaVersion))
		unsigned.Set("recordKind", jsjson.MakeString(approvedAfterExecution))
		unsigned.Set("proposalId", jsjson.MakeString(id))
		unsigned.Set("executedWitnessDigest", jsjson.MakeString(o.ExecutedWitnessDigest))
		unsigned.Set("ratifiedWitnessDigest", jsjson.MakeString(currentDigest))
		unsigned.Set("ratifiedBy", jsjson.MakeString(o.RatifiedBy))
		if o.RatifiedAt != nil {
			unsigned.Set("ratifiedAt", jsjson.MakeString(*o.RatifiedAt))
		} else {
			unsigned.Set("ratifiedAt", jsjson.MakeString(now()))
		}
		if o.Note != nil {
			unsigned.Set("note", jsjson.MakeString(*o.Note))
		}
		ratification := parse(signed(unsigned, "ratificationDigest"), witnessRatificationSchema)

		path := recordPath(store, "ratifications", id)
		if err := writePretty(path, ratification); err != nil {
			if nodefs.IsExist(err) {
				jsexc.Throw(&AutonomousSessionError{"ALREADY_RATIFIED", jsexc.Concat("Ratification already exists for ", id)})
			}
			jsexc.Throw(err)
		}

		note := defaultRatificationNote
		if o.Note != nil {
			note = *o.Note
		}
		approval := jsjson.NewObj()
		approval.Set("approvedBy", jsjson.MakeString(o.RatifiedBy))
		approval.Set("approvedAt", ratification.Get("ratifiedAt"))
		approval.Set("note", jsjson.MakeString(jsexc.Concat(approvedAfterExecution, ": ", note)))
		jsexc.Must(Approve(store, id, jsjson.MakeObject(approval)))
		frozen := jsexc.Must(Freeze(store, id, FreezeOptions{FrozenAt: ratification.Get("ratifiedAt").Str(), HasFrozenAt: true}))
		o2 := jsjson.NewObj()
		o2.Set("ratification", ratification)
		o2.Set("frozen", frozen)
		return jsjson.MakeObject(o2)
	})
}
