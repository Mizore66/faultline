package witness

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/openai"
	"github.com/Mizore66/faultline/internal/redaction"
	s "github.com/Mizore66/faultline/internal/schema"
)

// LiveWitnessOptions is proposeWitnessWithGpt's options object; a nil field
// is an absent (nullish) option, a pointer to "" is an explicit empty one.
type LiveWitnessOptions struct{ APIKey, Model *string }

// Port of src/ai.ts BlindIncidentPacketSchema: the provider-boundary
// schema, with none of the storage limits.
var blindIncidentPacketSchema = s.Object(
	s.F("symptom", s.String(s.MinLength(1))),
	s.F("ciLog", s.String(s.MinLength(1))),
	s.F("repositoryLanguage", s.String(s.MinLength(1))),
	s.F("repositorySummary", s.String(s.MinLength(1))),
).Strict()

// Port of src/ai.ts WitnessProposalSchema.
var liveWitnessProposalSchema = s.Object(
	s.F("behavior", s.String(s.MinLength(1))),
	s.F("command", s.String(s.MinLength(1))),
	s.F("overlayFiles", s.Array(s.String(), s.MaxItems(5))),
	s.F("expectedExitCode", s.Number(s.Int(), s.Gte(0), s.Lte(255))),
	s.F("timeoutSeconds", s.Number(s.Int(), s.Gte(1), s.Lte(120))),
	s.F("safetyNotes", s.Array(s.String(), s.MaxItems(8))),
).Strict()

// Port of src/ai.ts instructions (joined by " ").
var liveWitnessInstructions = strings.Join([]string{
	"Propose an executable witness from the blinded incident packet.",
	"You have not been given candidate sessions, turns, diffs, timeline data, or localization results.",
	"Do not assign verdicts, name a culprit, or make causal claims.",
	"Return only the required structured proposal.",
}, " ")

func strArray(maxItems int) jsjson.Value {
	return jsjson.MustObject(
		"type", jsjson.MakeString("array"),
		"items", jsjson.MustObject("type", jsjson.MakeString("string")),
		"maxItems", jsjson.MakeNumber(float64(maxItems)),
	)
}

func boundedInteger(min, max float64) jsjson.Value {
	return jsjson.MustObject(
		"type", jsjson.MakeString("integer"),
		"minimum", jsjson.MakeNumber(min),
		"maximum", jsjson.MakeNumber(max),
	)
}

// Port of src/ai.ts witnessProposalJsonSchema.
func liveWitnessProviderSchema() jsjson.Value {
	str := jsjson.MustObject("type", jsjson.MakeString("string"))
	required := []jsjson.Value{}
	for _, k := range []string{"behavior", "command", "overlayFiles", "expectedExitCode", "timeoutSeconds", "safetyNotes"} {
		required = append(required, jsjson.MakeString(k))
	}
	return jsjson.MustObject(
		"type", jsjson.MakeString("object"),
		"additionalProperties", jsjson.MakeBool(false),
		"required", jsjson.MakeArray(required),
		"properties", jsjson.MustObject(
			"behavior", str,
			"command", str,
			"overlayFiles", strArray(5),
			"expectedExitCode", boundedInteger(0, 255),
			"timeoutSeconds", boundedInteger(1, 120),
			"safetyNotes", strArray(8),
		),
	)
}

// liveWitnessRequestBody is JSON.stringify of src/ai.ts's request body.
func liveWitnessRequestBody(model string, redactedPacket jsjson.Value) string {
	return jsjson.Stringify(jsjson.MustObject(
		"model", jsjson.MakeString(model),
		"instructions", jsjson.MakeString(liveWitnessInstructions),
		"input", jsjson.MakeString(jsjson.Stringify(redactedPacket)),
		"text", jsjson.MustObject("format", jsjson.MustObject(
			"type", jsjson.MakeString("json_schema"),
			"name", jsjson.MakeString("faultline_witness_proposal"),
			"strict", jsjson.MakeBool(true),
			"schema", liveWitnessProviderSchema(),
		)),
	))
}

// ProposeWitnessWithGpt ports src/ai.ts proposeWitnessWithGpt, except that
// the default model is gpt-6.1 (approved deviation). It returns
// {proposal, incidentPacketDigest, redaction}; an error is the message TS
// throws.
func ProposeWitnessWithGpt(packetInput jsjson.Value, options LiveWitnessOptions) (jsjson.Value, error) {
	packet, err := bundle.ParseValue(packetInput, blindIncidentPacketSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	redacted, report := redaction.RedactValue(packet, "$")
	redactedPacket, err := bundle.ParseValue(redacted, blindIncidentPacketSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	apiKey, _ := nodeproc.Getenv("OPENAI_API_KEY")
	if options.APIKey != nil {
		apiKey = *options.APIKey
	}
	if apiKey == "" {
		return jsjson.Value{}, errors.New("OPENAI_API_KEY is required for a live GPT-5.6 witness proposal. The judge demo does not require one.")
	}
	model := "gpt-6.1"
	if options.Model != nil {
		model = *options.Model
	}
	body := jsstr.ToUTF8(liveWitnessRequestBody(model, redactedPacket))
	status, text, err := openai.PostJSON(openai.ResponsesURL(), "Bearer "+apiKey, []byte(body))
	if err != nil {
		return jsjson.Value{}, err
	}
	if status < 200 || status > 299 {
		return jsjson.Value{}, errors.New(jsexc.Concat("OpenAI Responses request failed: ", strconv.Itoa(status), " ", text))
	}
	parsed, err := jsjson.Parse(text)
	if err != nil {
		return jsjson.Value{}, err
	}
	outputText, err := openai.ExtractResponseText(parsed)
	if err != nil {
		return jsjson.Value{}, err
	}
	output, err := jsjson.Parse(outputText)
	if err != nil {
		return jsjson.Value{}, err
	}
	proposal, err := bundle.ParseValue(output, liveWitnessProposalSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	digest, err := canonical.DigestJSON(redactedPacket)
	if err != nil {
		return jsjson.Value{}, err
	}
	return jsjson.MustObject(
		"proposal", proposal,
		"incidentPacketDigest", jsjson.MakeString(digest),
		"redaction", report.JSON(),
	), nil
}
