// Frozen witnesses with several overlays, built by frozen TS, and a re-signer
// so a reordered or edited witness reaches the semantic checks.
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { digestJson } from "../../src/canonical.js";
import { createFrozenWitness } from "./fixtures.js";

export function makeWitness(paths: string[]): string {
  const store = mkdtempSync(join(tmpdir(), "faultline-oracle-witness-"));
  try {
    return JSON.stringify(createFrozenWitness(store, paths.map((path, i) => ({ path, text: `overlay ${i}\n` }))));
  } finally {
    rmSync(store, { recursive: true, force: true });
  }
}

// resignWitness recomputes every digest of the review chain over the record
// as it is (overlay order included), mirroring buildProposal,
// approveWitnessProposal and freezeApprovedWitness. Input it cannot sign is
// returned unchanged.
export function resignWitness(text: string): string {
  try {
    const frozen = JSON.parse(text);
    const { proposal, approval } = frozen;
    const w = proposal.witness;
    w.overlayDigest = digestJson(w.overlays.map((o: { path: string; bytesDigest: string }) => ({ path: o.path, bytesDigest: o.bytesDigest })));
    delete proposal.proposalDigest;
    proposal.proposalDigest = digestJson(proposal);
    approval.proposalDigest = proposal.proposalDigest;
    approval.reviewedOverlayDigest = w.overlayDigest;
    delete approval.approvalDigest;
    approval.approvalDigest = digestJson(approval);
    frozen.witnessDigest = digestJson({
      schemaVersion: "faultline.locked-witness.v1", proposalId: proposal.proposalId, proposalDigest: proposal.proposalDigest,
      incidentPacketDigest: proposal.incidentPacketDigest, behavior: w.behavior, command: w.command, commandDigest: w.commandDigest,
      overlays: w.overlays, overlayDigest: w.overlayDigest, policy: w.policy
    });
    delete frozen.frozenDigest;
    frozen.frozenDigest = digestJson(frozen);
    return JSON.stringify(frozen);
  } catch {
    return text;
  }
}
