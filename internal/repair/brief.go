// Package repair ports the post-localization repair brief: the evidence
// packet and brief schemas, citation validation and the OpenAI response
// text extraction (src/repair-brief.ts, src/openai-response.ts).
package repair

import (
	"errors"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/repair-brief.ts:17-18.
const (
	EvidencePacketVersion = "faultline.repair-evidence.v2"
	BriefVersion          = "faultline.repair-brief.v1"
)

// Port of src/repair-brief.ts:84 (the refusal text).
const packetRefusal = "A post-localization repair brief requires a completed Docker-isolated investigation with at least one stable transition."

// Port of src/repair-brief.ts:20-55.
var (
	digestSchema     = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), ""))
	evidenceIDSchema = s.String(s.Regex(s.Pattern(`^E[1-9][0-9]*$`), ""))

	evidenceFactSchema = s.Object(
		s.F("id", evidenceIDSchema),
		s.F("kind", s.Enum("EXECUTED", "DERIVED")),
		s.F("statement", s.String(s.MinLength(1), s.MaxLength(2_000))),
	).Strict()

	// EvidencePacketSchema ports src/repair-brief.ts:29 RepairEvidencePacketSchema.
	EvidencePacketSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(EvidencePacketVersion)),
		s.F("investigationDigest", digestSchema),
		s.F("frozenWitnessDigest", digestSchema),
		s.F("recorder", s.LiteralString("git-commit-range-replay")),
		s.F("nativeCodexInterception", s.Literal(jsjson.MakeBool(false))),
		s.F("facts", s.Array(evidenceFactSchema, s.MinItems(2), s.MaxItems(256))),
		s.F("limitations", s.Array(s.String(s.MinLength(1), s.MaxLength(1_000)), s.MinItems(1), s.MaxItems(16))),
		s.F("packetDigest", digestSchema),
	).Strict()

	citedRecommendationSchema = s.Object(
		s.F("statement", s.String(s.MinLength(1), s.MaxLength(2_000))),
		s.F("evidenceIds", s.Array(evidenceIDSchema, s.MinItems(1), s.MaxItems(32))),
	).Strict()

	// BriefSchema ports src/repair-brief.ts:45 RepairBriefSchema.
	BriefSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(BriefVersion)),
		s.F("evidencePacketDigest", digestSchema),
		s.F("proposedInvariant", citedRecommendationSchema),
		s.F("repairDirections", s.Array(citedRecommendationSchema, s.MinItems(1), s.MaxItems(8))),
		s.F("prevention", s.Object(
			s.F("hardEnforcement", s.Array(citedRecommendationSchema, s.MinItems(1), s.MaxItems(8))),
			s.F("softGuidance", s.Array(citedRecommendationSchema, s.MaxItems(8))),
		).Strict()),
		s.F("uncertainties", s.Array(s.String(s.MinLength(1), s.MaxLength(1_000)), s.MinItems(1), s.MaxItems(8))),
	).Strict()
)

// Port of src/repair-brief.ts:120-123 (the packet limitations).
var packetLimitations = []string{
	"This packet establishes only predicate-specific Git commit-range transitions; it does not prove model intent, a unique semantic cause, or native Codex interception.",
	"No source text, raw command output, overlay bytes, repository path, credentials, or filesystem metadata are sent in this repair-planning packet.",
}

// Validation ports src/repair-brief.ts:60 RepairBriefValidation. An
// Undefined Brief is TS null.
type Validation struct {
	Valid  bool
	Errors []string
	Brief  jsjson.Value
}

// JSON is the TS object { valid, errors, brief }.
func (v Validation) JSON() jsjson.Value {
	errs := make([]jsjson.Value, len(v.Errors))
	for i, e := range v.Errors {
		errs[i] = jsjson.MakeString(e)
	}
	brief := v.Brief
	if brief.Kind() == jsjson.Undefined {
		brief = jsjson.MakeNull()
	}
	return jsjson.MustObject("valid", jsjson.MakeBool(v.Valid), "errors", jsjson.MakeArray(errs), "brief", brief)
}

// withoutDigest ports src/repair-brief.ts:66 withoutDigest.
func withoutDigest(packet jsjson.Value) jsjson.Value { return bundle.Without(packet, "packetDigest") }

// PacketDigestValid is `packet.packetDigest === digestJson(withoutDigest(packet))`
// (src/repair-brief.ts:141, 200) for an object packet.
func PacketDigestValid(packet jsjson.Value) bool {
	d := packet.Get("packetDigest")
	return d.Kind() == jsjson.String && d.Str() == jsexc.Must(canonical.DigestJSON(withoutDigest(packet)))
}

func str(v string) jsjson.Value { return jsjson.MakeString(v) }

// CreateEvidencePacket ports src/repair-brief.ts:80
// createRepairEvidencePacket. An error is what TS throws: the input's
// ZodError, the refusal, or the packet's ZodError.
func CreateEvidencePacket(input jsjson.Value) (jsjson.Value, error) {
	result, err := bundle.ParseValue(input, gitproof.GitInvestigationResultSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	proof, witness, transitions := result.Get("proof"), result.Get("witness"), result.Get("transitions").Items()
	witnessDigest := witness.Get("witnessDigest")
	if proof.Get("executionTrust").Str() != "NATIVE_DOCKER" || !proof.Get("dockerIsolated").Bool() || !proof.Get("isProof").Bool() ||
		result.Get("status").Str() != "COMPLETED" || len(transitions) == 0 ||
		witnessDigest.Kind() != jsjson.String || witnessDigest.Str() == "" {
		return jsjson.Value{}, errors.New(packetRefusal)
	}

	var facts []jsjson.Value
	add := func(kind, statement string) {
		id := "E" + jsjson.FormatNumber(float64(len(facts)+1))
		facts = append(facts, jsjson.MustObject("id", str(id), "kind", str(kind), "statement", str(statement)))
	}
	add("EXECUTED", "The frozen witness was executed "+jsjson.FormatNumber(result.Get("executionsPerState").Num())+
		" times for each of "+jsjson.FormatNumber(float64(len(result.Get("states").Items())))+" recorded Git states in Docker-isolated plans.")
	for _, transition := range transitions {
		before, after := transition.Get("before"), transition.Get("after")
		add("DERIVED", transition.Get("kind").Str()+" transition: "+before.Get("commit").Str()+" ("+before.Get("verdict").Str()+") to "+
			after.Get("commit").Str()+" ("+after.Get("verdict").Str()+"), each supported by "+
			jsjson.FormatNumber(float64(len(before.Get("executionIds").Items())))+" distinct Docker executions.")
	}
	add("DERIVED", "The investigation uses frozen witness "+witnessDigest.Str()+" and retained a non-native Codex recorder boundary.")

	limitations := make([]jsjson.Value, len(packetLimitations))
	for i, l := range packetLimitations {
		limitations[i] = str(l)
	}
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", str(EvidencePacketVersion))
	unsigned.Set("investigationDigest", str(jsexc.Must(canonical.DigestJSON(result))))
	unsigned.Set("frozenWitnessDigest", witness.Get("frozenDigest"))
	unsigned.Set("recorder", result.Get("recorder"))
	unsigned.Set("nativeCodexInterception", jsjson.MakeBool(false))
	unsigned.Set("facts", jsjson.MakeArray(facts))
	unsigned.Set("limitations", jsjson.MakeArray(limitations))
	packetDigest := jsexc.Must(canonical.DigestJSON(jsjson.MakeObject(unsigned)))
	unsigned.Set("packetDigest", str(packetDigest))
	return bundle.ParseValue(jsjson.MakeObject(unsigned), EvidencePacketSchema)
}

// citedRecommendations ports src/repair-brief.ts:127 citedRecommendations.
func citedRecommendations(brief jsjson.Value) []jsjson.Value {
	out := []jsjson.Value{brief.Get("proposedInvariant")}
	out = append(out, brief.Get("repairDirections").Items()...)
	out = append(out, brief.Get("prevention", "hardEnforcement").Items()...)
	return append(out, brief.Get("prevention", "softGuidance").Items()...)
}

func refused(message string) Validation {
	return Validation{Errors: []string{message}}
}

// ValidateBrief ports src/repair-brief.ts:137 validateRepairBrief. Like TS
// it throws (jsexc) only when reading a ZodError message throws.
func ValidateBrief(packetInput, briefInput jsjson.Value) Validation {
	packet, issues, ok := s.Parse(EvidencePacketSchema, packetInput)
	if !ok {
		return refused(jsexc.Concat("repair evidence packet is invalid: ", s.ErrorMessage(issues)))
	}
	if !PacketDigestValid(packet) {
		return refused("repair evidence packet digest does not match its canonical contents")
	}
	brief, issues, ok := s.Parse(BriefSchema, briefInput)
	if !ok {
		return refused(jsexc.Concat("repair brief schema validation failed: ", s.ErrorMessage(issues)))
	}
	errs := []string{}
	if brief.Get("evidencePacketDigest").Str() != packet.Get("packetDigest").Str() {
		errs = append(errs, "repair brief references a different evidence packet digest")
	}
	validIDs := map[string]bool{}
	for _, fact := range packet.Get("facts").Items() {
		validIDs[fact.Get("id").Str()] = true
	}
	for _, recommendation := range citedRecommendations(brief) {
		for _, id := range recommendation.Get("evidenceIds").Items() {
			if !validIDs[id.Str()] {
				errs = append(errs, "repair brief cites an unknown evidence ID: "+id.Str())
			}
		}
	}
	if len(errs) > 0 {
		return Validation{Errors: errs}
	}
	return Validation{Valid: true, Errors: errs, Brief: brief}
}

func stringType() jsjson.Value { return jsjson.MustObject("type", str("string")) }

func citedRecommendationJSONSchema() jsjson.Value {
	return jsjson.MustObject(
		"type", str("object"),
		"additionalProperties", jsjson.MakeBool(false),
		"required", jsjson.MakeArray([]jsjson.Value{str("statement"), str("evidenceIds")}),
		"properties", jsjson.MustObject(
			"statement", stringType(),
			"evidenceIds", jsjson.MustObject("type", str("array"), "items", stringType()),
		),
	)
}

func arrayOf(items jsjson.Value) jsjson.Value {
	return jsjson.MustObject("type", str("array"), "items", items)
}

// BriefJSONSchema ports src/repair-brief.ts:162 repairBriefJsonSchema, the
// strict json_schema sent to OpenAI, in TS key order.
func BriefJSONSchema() jsjson.Value {
	required := []jsjson.Value{}
	for _, k := range []string{"schemaVersion", "evidencePacketDigest", "proposedInvariant", "repairDirections", "prevention", "uncertainties"} {
		required = append(required, str(k))
	}
	return jsjson.MustObject(
		"type", str("object"),
		"additionalProperties", jsjson.MakeBool(false),
		"required", jsjson.MakeArray(required),
		"properties", jsjson.MustObject(
			"schemaVersion", jsjson.MustObject("const", str(BriefVersion)),
			"evidencePacketDigest", stringType(),
			"proposedInvariant", citedRecommendationJSONSchema(),
			"repairDirections", arrayOf(citedRecommendationJSONSchema()),
			"prevention", jsjson.MustObject(
				"type", str("object"),
				"additionalProperties", jsjson.MakeBool(false),
				"required", jsjson.MakeArray([]jsjson.Value{str("hardEnforcement"), str("softGuidance")}),
				"properties", jsjson.MustObject(
					"hardEnforcement", arrayOf(citedRecommendationJSONSchema()),
					"softGuidance", arrayOf(citedRecommendationJSONSchema()),
				),
			),
			"uncertainties", arrayOf(stringType()),
		),
	)
}
