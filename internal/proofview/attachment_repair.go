package proofview

import (
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/repair"
)

// readRepairContents reads, schema-parses and citation-validates the packet
// and brief; it returns the validated brief.
func readRepairContents(root string) repairContents {
	packet := bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(root, "evidence-packet.json"), "repair evidence packet"), repair.EvidencePacketSchema))
	brief := bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(root, "repair-brief.json"), "repair brief"), repair.BriefSchema))
	validation := repair.ValidateBrief(packet, brief)
	if !validation.Valid || validation.Brief.Kind() == jsjson.Undefined || validation.Brief.Kind() == jsjson.Null {
		attachmentFailure(jsexc.Concat("Refusing to attach an invalid repair brief payload: ", jsexc.Join(validation.Errors, "; ")))
	}
	return repairContents{packet: packet, brief: validation.Brief}
}

func manifestMatchesRepairContents(manifest jsjson.Value, contents repairContents) bool {
	return manifest.Get("evidencePacket", "digest").Str() == contents.packet.Get("packetDigest").Str() &&
		manifest.Get("repairBrief", "digest").Str() == bundle.Must(canonical.DigestJSON(contents.brief))
}

func sameRepairContents(a, b repairContents) bool {
	return bundle.SameCanonical(a.packet, b.packet) && bundle.SameCanonical(a.brief, b.brief)
}

// loadRepair ports the repair half of loadVerifiedIncidentAttachments
// (src/incident-attachments.ts:173-218): three verifications around two reads.
// Repair binds to frozenDigest, unlike prevention's witnessDigest.
func loadRepair(directory string, expected *string, investigation, frozenWitness jsjson.Value, deps attachmentDependencies) *VerifiedRepairAttachment {
	root := nodefs.Resolve(directory)
	first := deps.repair(root, expected)
	if !first.Valid || absent(first.Manifest) {
		attachmentFailure(jsexc.Concat("Refusing to attach an invalid repair brief: ", jsexc.Join(first.Errors, "; ")))
	}
	one := deps.readRepair(root)
	if !manifestMatchesRepairContents(first.Manifest, one) {
		attachmentFailure("Refusing to attach repair guidance whose in-memory payload does not match its verified manifest.")
	}
	if one.packet.Get("investigationDigest").Str() != bundle.Must(canonical.DigestJSON(investigation)) {
		attachmentFailure("Refusing to attach repair guidance from a different Git investigation.")
	}
	if one.packet.Get("frozenWitnessDigest").Str() != frozenWitness.Get("frozenDigest").Str() {
		attachmentFailure("Refusing to attach repair guidance for a different frozen witness.")
	}
	const changed = "Refusing to attach repair guidance that changed during verification: "
	second := deps.repair(root, expected)
	if !second.Valid || absent(second.Manifest) || !bundle.SameCanonical(second.Manifest, first.Manifest) {
		detail := jsexc.Join(second.Errors, "; ")
		if detail == "" {
			detail = "manifest changed"
		}
		attachmentFailure(jsexc.Concat(changed, detail))
	}
	two := deps.readRepair(root)
	third := deps.repair(root, expected)
	if !third.Valid || absent(third.Manifest) || !bundle.SameCanonical(third.Manifest, first.Manifest) ||
		!sameRepairContents(one, two) || !manifestMatchesRepairContents(third.Manifest, two) {
		detail := jsexc.Join(third.Errors, "; ")
		if detail == "" {
			detail = "artifact contents changed"
		}
		attachmentFailure(jsexc.Concat(changed, detail))
	}
	return &VerifiedRepairAttachment{Manifest: third.Manifest, Packet: two.packet, Brief: two.brief, ExternalDigestStatus: third.ExternalDigestStatus}
}
