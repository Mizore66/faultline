package cli

import (
	"errors"
	"strconv"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/proofview"
	"github.com/Mizore66/faultline/internal/redaction"
	"github.com/Mizore66/faultline/internal/repair"
)

// Port of src/cli-app.ts:2672 repairCommand. The --bundle (Codex) branch is
// Task 20; until then it stays unported.
func repairCommand(e *env, args []string) error {
	if len(args) > 0 && args[0] == "verify" {
		var directory string
		if len(args) > 1 {
			directory = args[1]
		}
		if directory == "" {
			return fail("Usage: fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]")
		}
		verification := repair.VerifyBriefArtifact(nodefs.Resolve(directory), optionPtr(args, "--expect-digest"))
		e.printJSON(verification.JSON())
		if verification.Valid {
			e.exitCode = 0
		} else {
			e.exitCode = 1
		}
		return nil
	}
	if hasFlag(args, "--bundle") && (len(args) == 0 || args[0] != "brief") {
		return fail("not yet ported: repair --bundle")
	}
	if len(args) == 0 || args[0] != "brief" {
		return fail("Usage: fl repair --bundle <dir> --expect-root <digest> [--with-codex] | fl repair brief|verify ...")
	}
	return e.repairBrief(args)
}

// repairBrief ports the "brief" branch (src/cli-app.ts:2736).
func (e *env) repairBrief(args []string) error {
	live := hasFlag(args, "--live")
	input, _ := option(args, "--input")
	if live == (input != "") {
		return fail("Choose exactly one repair brief source: --live or --input <repair-brief.json>.")
	}
	bundleDir, hasBundle := option(args, "--bundle")
	investigationFile, hasInvestigation := option(args, "--investigation")
	if (bundleDir != "") == (investigationFile != "") {
		return fail("Choose exactly one proof source: --bundle <directory> or --investigation <verified-bundle>/investigation.json.")
	}
	var bundleDirectory string
	if !hasBundle {
		bundleDirectory = nodefs.Dirname(nodefs.Resolve(investigationFile))
	} else {
		bundleDirectory = nodefs.Resolve(bundleDir)
	}
	proof, err := proofview.LoadVerifiedGitProofView(bundleDirectory, optionPtr(args, "--expect-root"))
	if err != nil {
		return err
	}
	if hasInvestigation {
		expected := nodefs.Resolve(bundleDirectory, proof.Manifest.Get("artifacts", "investigation").Str())
		if nodefs.Resolve(investigationFile) != expected {
			return fail("--investigation must identify the investigation.json artifact in the verified Git proof bundle.")
		}
	}
	generated, err := repair.CreateEvidencePacket(proof.Investigation)
	if err != nil {
		return err
	}
	packetRedaction, packetReport := redaction.RedactValue(generated, "$")
	packet, err := bundle.ParseValue(packetRedaction, repair.EvidencePacketSchema)
	if err != nil {
		return err
	}
	if packet.Get("packetDigest").Str() != generated.Get("packetDigest").Str() {
		return fail("Repair evidence packet changed during redaction and cannot be safely correlated to the verified investigation.")
	}

	model, ok := option(args, "--model")
	if !ok {
		model = "gpt-5.6"
	}
	var candidate jsjson.Value
	if live {
		if candidate, err = repair.ProposeBrief(packet, model); err != nil {
			return err
		}
	} else if candidate, err = readJSONInput(input); err != nil {
		return err
	}
	// Never retain unredacted model or offline input.
	redactedCandidate, candidateReport := redaction.RedactValue(candidate, "$")
	validation := repair.ValidateBrief(packet, redactedCandidate)
	if !validation.Valid || validation.Brief.Kind() == jsjson.Undefined {
		return errors.New(jsexc.Concat("Repair brief was rejected: ", jsexc.Join(validation.Errors, "; ")))
	}

	output, ok := option(args, "--output")
	if !ok {
		digest := packet.Get("packetDigest").Str()
		output = nodefs.Join(repair.DefaultBriefRoot(), "repair-"+digest[len("sha256:"):len("sha256:")+16]+"-"+strconv.FormatInt(clock.NowMillis(), 10))
	}
	source := repair.Source{Kind: "OFFLINE_INPUT"}
	if live {
		source = repair.Source{Kind: "GPT-5.6", Model: model, HasModel: true}
	}
	written, err := repair.WriteBriefArtifact(output, packet, validation.Brief, source)
	if err != nil {
		return err
	}
	manifest := written.Manifest
	e.printJSON(obj("status", "PERSISTED", "classification", "INFERRED", "source", manifest.Get("source"),
		"directory", written.Directory, "artifactDigest", manifest.Get("manifestDigest"),
		"evidencePacketDigest", packet.Get("packetDigest"), "repairBriefDigest", manifest.Get("repairBrief", "digest"),
		"privacy", obj("packet", packetReport.JSON(), "candidate", candidateReport.JSON()),
		"limitation", "INFERRED repair guidance is not an executed verdict, proof of model intent, a unique semantic cause, or an identified culprit."))
	return nil
}
