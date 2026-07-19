import { describe, expect, it } from "vitest";
import { digestJson } from "../src/canonical.js";
import { createRepairEvidencePacket, validateRepairBrief } from "../src/repair-brief.js";

const digest = (letter: string): `sha256:${string}` => `sha256:${letter.repeat(64)}`;

function completedInvestigation() {
  const executionIds = [digest("1"), digest("2"), digest("3")];
  const stablePass = { stateIndex: 0, commit: "a".repeat(40), tree: "b".repeat(40), verdict: "PASS", executionIds, runIds: executionIds };
  const stableFail = { stateIndex: 1, commit: "c".repeat(40), tree: "d".repeat(40), verdict: "FAIL", executionIds: [digest("4"), digest("5"), digest("6")], runIds: [digest("4"), digest("5"), digest("6")] };
  return {
    schemaVersion: "faultline.git-investigation.v1",
    recorder: "git-commit-range-replay",
    nativeCodexInterception: false,
    status: "COMPLETED",
    repository: "C:/intentionally-not-exported/repo",
    requestedRange: { ancestor: "HEAD~1", descendant: "HEAD" },
    resolvedRange: { ancestor: { index: 0, commit: stablePass.commit, tree: stablePass.tree }, descendant: { index: 1, commit: stableFail.commit, tree: stableFail.tree } },
    witness: { valid: true, errors: [], frozenDigest: digest("f"), witnessDigest: digest("e"), externalDigestStatus: "MATCH", approval: { actor: "reviewer", approvedAt: "2026-07-16T10:00:00.000Z" } },
    executionsPerState: 3,
    states: [
      { index: 0, commit: stablePass.commit, tree: stablePass.tree },
      { index: 1, commit: stableFail.commit, tree: stableFail.tree }
    ],
    runs: [],
    stableStates: [stablePass, stableFail],
    transitions: [{ kind: "PASS_TO_FAIL", before: stablePass, after: stableFail }],
    nonMonotonic: false,
    environment: { homogeneity: "HOMOGENEOUS", fingerprints: [], distinctDigests: [] },
    proof: {
      requiresDockerIsolation: true,
      dockerIsolated: true,
      executionTrust: "NATIVE_DOCKER",
      proofTransitions: 1,
      isProof: true,
      reason: "three Docker runs",
      evidenceGrade: "COMMIT_PROOF",
      evidenceLabel: "Proven at commit granularity — portable and offline-verifiable"
    },
    errors: []
  };
}

describe("post-localization GPT repair boundary", () => {
  it("minimizes a verified Git result into a citation-addressable private packet", () => {
    const packet = createRepairEvidencePacket(completedInvestigation());
    expect(packet.facts).toHaveLength(3);
    expect(packet.frozenWitnessDigest).toBe(digest("f"));
    const { packetDigest, ...unsigned } = packet;
    expect(packetDigest).toBe(digestJson(unsigned));
  });

  it("rejects model recommendations that cite missing facts or a different packet", () => {
    const packet = createRepairEvidencePacket(completedInvestigation());
    const base = {
      schemaVersion: "faultline.repair-brief.v1",
      evidencePacketDigest: packet.packetDigest,
      proposedInvariant: { statement: "Preserve the validated behavior.", evidenceIds: ["E1"] },
      repairDirections: [{ statement: "Inspect the transition before changing code.", evidenceIds: ["E2"] }],
      prevention: { hardEnforcement: [{ statement: "Keep a regression guard.", evidenceIds: ["E2"] }], softGuidance: [] },
      uncertainties: ["The packet does not prove model intent."]
    };
    expect(validateRepairBrief(packet, base)).toMatchObject({ valid: true });
    expect(validateRepairBrief(packet, { ...base, proposedInvariant: { ...base.proposedInvariant, evidenceIds: ["E999"] } })).toMatchObject({ valid: false });
    expect(validateRepairBrief(packet, { ...base, evidencePacketDigest: digest("a") })).toMatchObject({ valid: false });
  });

  it("refuses to turn an incomplete or unsafe-local result into a model repair request", () => {
    const incomplete = completedInvestigation();
    incomplete.proof.isProof = false;
    expect(() => createRepairEvidencePacket(incomplete)).toThrow(/requires a completed Docker-isolated investigation/);

    const injected = completedInvestigation();
    injected.proof.executionTrust = "INJECTED_RUNNER";
    injected.proof.dockerIsolated = false;
    expect(() => createRepairEvidencePacket(injected)).toThrow(/requires a completed Docker-isolated investigation/);
  });
});
