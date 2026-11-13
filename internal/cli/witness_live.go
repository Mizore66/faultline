package cli

import (
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/redaction"
	"github.com/Mizore66/faultline/internal/witness"
)

// liveModelOptions is `{ model: option(args, "--model") ?? <default> }`: an
// absent option keeps the boundary default, an explicit empty one stays empty.
func liveModelOptions(args []string) witness.LiveWitnessOptions {
	return witness.LiveWitnessOptions{Model: optionPtr(args, "--model")}
}

// witnessProposeLivePreview ports the --live branch of witnessProposal
// (src/cli-app.ts:980): the fixed built-in incident, nothing written.
func (e *env) witnessProposeLivePreview(args []string) error {
	packet := obj("symptom", "A completed refund may overwrite settlement currency with display currency.",
		"ciLog", "Expected settlement currency USD; received EUR.",
		"repositoryLanguage", "JavaScript",
		"repositorySummary", "A Node service that completes refunds.")
	result, err := witness.ProposeWitnessWithGpt(packet, liveModelOptions(args))
	if err != nil {
		return err
	}
	e.printJSON(result)
	return nil
}

// witnessProposeLive ports the persisted --live --incident branch
// (src/cli-app.ts:1348): read incident, require proposal ID and overlay root,
// call the model, redact the original again, read overlays, propose, print.
// expectedExitCode and safetyNotes are validated by the model boundary and
// then dropped here.
func witnessProposeLive(e *env, args []string, storeDirectory string) error {
	incidentFile, _ := option(args, "--incident")
	incident, err := readJSONInput(incidentFile)
	if err != nil {
		return err
	}
	proposalID, err := requiredOption(args, "--proposal-id")
	if err != nil {
		return err
	}
	overlayRoot, err := requiredOption(args, "--overlay-root")
	if err != nil {
		return err
	}
	generated, err := witness.ProposeWitnessWithGpt(incident, liveModelOptions(args))
	if err != nil {
		return err
	}
	redactedIncident, _ := redaction.RedactValue(incident, "$")
	generatedProposal := generated.Get("proposal")
	var paths []string
	for _, item := range generatedProposal.Get("overlayFiles").Items() {
		paths = append(paths, item.Str())
	}
	read, err := witness.ReadModelOverlayInput(overlayRoot, paths)
	if err != nil {
		return err
	}
	overlays := make([]jsjson.Value, len(read))
	for i, o := range read {
		overlays[i] = obj("path", o.Path, "bytesBase64", o.BytesBase64)
	}
	policy := obj("network", "disabled", "credentials", "redacted", "timeoutSeconds", generatedProposal.Get("timeoutSeconds"))
	input := obj("proposalId", proposalID, "proposalOrigin", "MODEL", "incidentPacket", redactedIncident,
		"witness", obj("behavior", generatedProposal.Get("behavior"), "command", generatedProposal.Get("command"),
			"overlays", jsjson.MakeArray(overlays), "policy", policy))
	proposal, err := witness.Propose(storeDirectory, input)
	if err != nil {
		return err
	}
	e.printJSON(obj("status", "PROPOSED", "source", "GPT-6.1", "store", storeDirectory, "proposalId", proposal.Get("proposalId"),
		"proposalDigest", proposal.Get("proposalDigest"), "incidentPacketDigest", proposal.Get("incidentPacketDigest"),
		"privacy", generated.Get("redaction")))
	return nil
}
