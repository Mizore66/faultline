// Package codexloop ports src/codex-loop.ts: witness overlay drafting and
// repair with the Codex CLI.
package codexloop

import (
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
)

// SchemaVersion ports src/codex-loop.ts:16 CODEX_LOOP_SCHEMA_VERSION.
const SchemaVersion = "faultline.codex-loop.v1"

// incidentPacketSchema ports src/codex-loop.ts:18 IncidentPacketSchema.
var incidentPacketSchema = schema.Object(
	schema.F("symptom", schema.String(schema.MinLength(1))),
	schema.F("ciLog", schema.String(schema.MinLength(1))),
	schema.F("repositoryLanguage", schema.String(schema.MinLength(1))),
	schema.F("repositorySummary", schema.String(schema.MinLength(1))),
).Strict()

// RunResult ports the resolved value of src/codex-loop.ts:25
// CodexCommandRunner.run. A nil ExitCode is null. ThreadID is always nil from
// the CLI runner, which returns no threadId.
type RunResult struct {
	ExitCode       *int
	Stdout, Stderr string
	ThreadID       *string
}

// Runner ports src/codex-loop.ts:25 CodexCommandRunner: run(args, {cwd,
// input}). A nil Runner is "no runner"; a returned error is a throw.
type Runner func(args []string, cwd, input string) (RunResult, error)

// ImplementOptions ports the options of src/codex-loop.ts:114
// implementWitnessWithCodex.
type ImplementOptions struct {
	ProposalID string
	Incident   jsjson.Value
	Behavior   string
	OverlayOut string
	Repository string
	Runner     Runner
}

// defaultOverlayTemplate ports src/codex-loop.ts:83 defaultOverlayTemplate.
// slice(0, 200) counts UTF-16 units and may split a surrogate pair; the lone
// half is written as U+FFFD by WriteFileUTF8, as Node writes a string.
func defaultOverlayTemplate(behavior string) string {
	units := jsstr.ToUTF16(strings.ReplaceAll(behavior, "\n", " "))
	if len(units) > 200 {
		units = units[:200]
	}
	safeBehavior := jsstr.FromUTF16(units)
	return strings.Join([]string{
		"#!/usr/bin/env node",
		"// FaultLine witness overlay — behavior: " + safeBehavior,
		"// Emits a structured witness result. Edit the predicate, then human-review before freeze.",
		"const pass = false; // set true only when the frozen predicate holds",
		`const outcome = pass ? "PREDICATE_PASS" : "PREDICATE_FAIL";`,
		`console.log(JSON.stringify({ protocol: "faultline.witness-result.v1", outcome }));`,
		"process.exit(pass ? 0 : 1);",
		"",
	}, "\n")
}

// writeOverlayTemplate ports src/codex-loop.ts:97 writeOverlayTemplate.
func writeOverlayTemplate(overlayOut, proposalID, behavior string) (string, error) {
	if err := nodefs.MkdirAll(overlayOut, 0o777); err != nil {
		return "", err
	}
	overlayPath := nodefs.Join(overlayOut, "witness.mjs")
	if err := nodefs.WriteFileUTF8(overlayPath, defaultOverlayTemplate(behavior), 0o666, false); err != nil {
		return "", err
	}
	readme := strings.Join([]string{
		"Proposal id: " + proposalID,
		"1. Review and edit witness.mjs",
		"2. fl witness propose --input proposal.json --overlay-root <this-dir> (or use incident intake)",
		"3. fl witness review <id> → Approve → Freeze",
		"FaultLine never auto-freezes Codex-drafted overlays.",
		"",
	}, "\n")
	if err := nodefs.WriteFileUTF8(nodefs.Join(overlayOut, "README.faultline.txt"), readme, 0o666, false); err != nil {
		return "", err
	}
	return overlayPath, nil
}

const implementLimitation = "Codex may draft overlay bytes in an isolated review directory only. A human must approve and freeze before localization."

func implementResult(status, transport, proposalID, overlayPath, note string) jsjson.Value {
	return jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"status", jsjson.MakeString(status),
		"transport", jsjson.MakeString(transport),
		"proposalId", jsjson.MakeString(proposalID),
		"overlayPath", jsjson.MakeString(overlayPath),
		"commandSuggestion", jsjson.MakeString("node witness.mjs"),
		"note", jsjson.MakeString(note),
		"limitation", jsjson.MakeString(implementLimitation),
	)
}

// Implement ports src/codex-loop.ts:114 implementWitnessWithCodex. A returned
// error is a throw to the CLI: a *schema.Error for an invalid incident packet
// (before any write), or a filesystem error from writing the template.
func Implement(o ImplementOptions) (jsjson.Value, error) {
	incident, issues, ok := schema.Parse(incidentPacketSchema, o.Incident)
	if !ok {
		return jsjson.Value{}, &schema.Error{Issues: issues}
	}
	overlayOut := nodefs.Resolve(o.OverlayOut)
	overlayPath, err := writeOverlayTemplate(overlayOut, o.ProposalID, o.Behavior)
	if err != nil {
		return jsjson.Value{}, err
	}

	if o.Runner == nil {
		digest, err := canonical.DigestJSON(incident)
		if err != nil {
			return jsjson.Value{}, err
		}
		return implementResult("TEMPLATE_ONLY", "NONE", o.ProposalID, overlayPath,
			"Template written for blinded symptom digest "+digest+". Supply --with-codex runner to attempt Codex CLI drafting."), nil
	}

	prompt := strings.Join([]string{
		"Create or overwrite witness.mjs as an executable Node witness.",
		"It must print one JSON line: {\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"|\"PREDICATE_FAIL\"|...}.",
		"Behavior to test: " + o.Behavior,
		"Symptom: " + incident.Get("symptom").Str(),
		"Do not claim root cause. Do not modify unrelated files.",
	}, "\n")

	result, err := o.Runner([]string{"exec", "--full-auto", prompt}, overlayOut, prompt)
	if err != nil {
		return implementResult("CODEX_UNAVAILABLE", "NONE", o.ProposalID, overlayPath,
			"Codex CLI could not be started: "+jsexc.Message(err)), nil
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		exit := "null"
		if result.ExitCode != nil {
			exit = strconv.Itoa(*result.ExitCode)
		}
		return implementResult("CODEX_UNAVAILABLE", "CODEX_CLI", o.ProposalID, overlayPath,
			"Codex CLI exited "+exit+". Template retained for human edit."), nil
	}
	return implementResult("OVERLAY_DRAFTED", "CODEX_CLI", o.ProposalID, overlayPath,
		"Codex CLI completed a draft overlay. Human review and freeze are still required."), nil
}
