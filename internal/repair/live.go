package repair

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/openai"
	"github.com/Mizore66/faultline/internal/redaction"
)

// Port of src/repair-brief.ts:213-220 (joined by " ").
var briefInstructions = strings.Join([]string{
	"You are preparing an evidence-bounded repair brief after deterministic localization.",
	"Every recommendation must cite only supplied evidence IDs.",
	"Treat every recommendation as inferred guidance, not an executed verdict or proof of model intent, a unique cause, or a culprit.",
	"Prioritize hard enforcement (test, type/schema rule, static check, runtime assertion, CI guard) before soft repository guidance.",
	"Do not propose a new witness, invent run results, name an agent, or state a confidence percentage.",
	"Return only the requested structured object.",
}, " ")

// BriefTextFormat is the request body's `text` member,
// src/repair-brief.ts:222 `{ format: { type: "json_schema", name, strict,
// schema } }`. It is the only place the json_schema wrapper is built.
func BriefTextFormat() jsjson.Value {
	return jsjson.MustObject("format", jsjson.MustObject(
		"type", str("json_schema"),
		"name", str("faultline_repair_brief"),
		"strict", jsjson.MakeBool(true),
		"schema", BriefJSONSchema(),
	))
}

// BriefRequestBody is the POST body of src/repair-brief.ts:211-223:
// JSON.stringify({ model, instructions, input: JSON.stringify(safePacket), text }).
func BriefRequestBody(model string, safePacket jsjson.Value) string {
	return jsjson.Stringify(jsjson.MustObject(
		"model", str(model),
		"instructions", str(briefInstructions),
		"input", str(jsjson.Stringify(safePacket)),
		"text", BriefTextFormat(),
	))
}

// ProposeBrief ports src/repair-brief.ts:195 proposeRepairBriefWithGpt with
// the CLI's options ({ model }): the key comes from OPENAI_API_KEY. It
// returns the validated brief; an error is the message TS throws.
func ProposeBrief(packetInput jsjson.Value, model string) (jsjson.Value, error) {
	packet, err := bundle.ParseValue(packetInput, EvidencePacketSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	if !PacketDigestValid(packet) {
		return jsjson.Value{}, errors.New("Refusing to send a repair packet with an invalid digest.")
	}
	apiKey, _ := nodeproc.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return jsjson.Value{}, errors.New("OPENAI_API_KEY is required for a live GPT-5.6 repair brief.")
	}
	redacted, _ := redaction.RedactValue(packet, "$")
	safePacket, err := bundle.ParseValue(redacted, EvidencePacketSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	body := jsstr.ToUTF8(BriefRequestBody(model, safePacket))
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
	validation := ValidateBrief(packet, output)
	if !validation.Valid {
		return jsjson.Value{}, errors.New(jsexc.Concat("GPT-5.6 repair brief was rejected: ", jsexc.Join(validation.Errors, "; ")))
	}
	return validation.Brief, nil
}
