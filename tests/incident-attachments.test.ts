import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { digestJson } from "../src/canonical.js";
import type { GitInvestigationResult, StableGitTransition } from "../src/git-investigation.js";
import type { GitMinimizationResult } from "../src/git-minimization.js";
import type { RepairBriefArtifactManifest } from "../src/repair-brief-store.js";
import type { FrozenWitness } from "../src/witness-lock.js";

const verifyMinimization = vi.hoisted(() => vi.fn());
const verifyRepair = vi.hoisted(() => vi.fn());

vi.mock("../src/git-minimization.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../src/git-minimization.js")>();
  return { ...actual, verifyGitMinimizationResultFile: verifyMinimization };
});

vi.mock("../src/repair-brief-store.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../src/repair-brief-store.js")>();
  return { ...actual, verifyRepairBriefArtifact: verifyRepair };
});

const { loadVerifiedIncidentAttachments } = await import("../src/incident-attachments.js");

function digest(character: string): string {
  return `sha256:${character.repeat(64)}`;
}

function proofFixture(): { investigation: GitInvestigationResult; frozen: FrozenWitness; transition: StableGitTransition } {
  const before = {
    stateIndex: 0,
    commit: "1".repeat(40),
    tree: "2".repeat(40),
    verdict: "PASS" as const,
    executionIds: [digest("a"), digest("b"), digest("c")],
    runIds: [digest("d"), digest("e"), digest("f")]
  };
  const after = {
    stateIndex: 1,
    commit: "3".repeat(40),
    tree: "4".repeat(40),
    verdict: "FAIL" as const,
    executionIds: [digest("1"), digest("2"), digest("3")],
    runIds: [digest("4"), digest("5"), digest("6")]
  };
  const transition: StableGitTransition = { kind: "PASS_TO_FAIL", before, after };
  const investigation = {
    schemaVersion: "faultline.git-investigation.v1",
    transitions: [transition]
  } as unknown as GitInvestigationResult;
  const frozen = {
    frozenDigest: digest("9"),
    witnessDigest: digest("8")
  } as FrozenWitness;
  return { investigation, frozen, transition };
}

function minimizationFixture(frozen: FrozenWitness, transition: StableGitTransition): GitMinimizationResult {
  return {
    witness: { frozenDigest: frozen.frozenDigest, witnessDigest: frozen.witnessDigest },
    before: { commit: transition.before.commit, tree: transition.before.tree },
    after: { commit: transition.after.commit, tree: transition.after.tree },
    proof: { isProof: true, reason: "Both directions were certified." },
    candidateUnitIds: [digest("7")],
    minimality: { oneMinimal: true },
    certification: { sufficiency: { status: "CERTIFIED" }, necessity: { status: "CERTIFIED" } }
  } as unknown as GitMinimizationResult;
}

function repairFiles(root: string, investigation: GitInvestigationResult, frozen: FrozenWitness): { packet: Record<string, unknown>; brief: Record<string, unknown> } {
  mkdirSync(root, { recursive: true });
  const packetUnsigned = {
    schemaVersion: "faultline.repair-evidence.v2",
    investigationDigest: digestJson(investigation),
    frozenWitnessDigest: frozen.frozenDigest,
    recorder: "git-commit-range-replay",
    nativeCodexInterception: false,
    facts: [
      { id: "E1", kind: "EXECUTED", statement: "The frozen witness was replayed." },
      { id: "E2", kind: "DERIVED", statement: "A stable boundary was recorded." }
    ],
    limitations: ["The packet does not prove a unique semantic cause."]
  };
  const packet = { ...packetUnsigned, packetDigest: digestJson(packetUnsigned) };
  const brief = {
    schemaVersion: "faultline.repair-brief.v1",
    evidencePacketDigest: packet.packetDigest,
    proposedInvariant: { statement: "Keep the proven behavior guarded.", evidenceIds: ["E1"] },
    repairDirections: [{ statement: "Repair the violated boundary.", evidenceIds: ["E2"] }],
    prevention: {
      hardEnforcement: [{ statement: "Keep a regression test in CI.", evidenceIds: ["E1"] }],
      softGuidance: []
    },
    uncertainties: ["The evidence does not establish model intent."]
  };
  writeFileSync(join(root, "evidence-packet.json"), JSON.stringify(packet), "utf8");
  writeFileSync(join(root, "repair-brief.json"), JSON.stringify(brief), "utf8");
  return { packet, brief };
}

function repairManifest(packet: Record<string, unknown>, brief: Record<string, unknown>): RepairBriefArtifactManifest {
  return {
    schemaVersion: "faultline.repair-brief-artifact.v1",
    classification: "INFERRED",
    source: { kind: "OFFLINE_INPUT" },
    evidencePacket: { path: "evidence-packet.json", digest: String(packet.packetDigest) },
    repairBrief: { path: "repair-brief.json", digest: digestJson(brief) },
    limitations: ["Guidance is inferred."],
    manifestDigest: digest("c")
  };
}

describe("verified incident attachments", () => {
  it("renders only independently verified artifacts bound to the exact proof and frozen witness", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-incident-attachments-"));
    try {
      const { investigation, frozen, transition } = proofFixture();
      const result = minimizationFixture(frozen, transition);
      const resultDigest = digestJson(result);
      verifyMinimization.mockReturnValue({
        valid: true,
        errors: [],
        result,
        resultDigest,
        externalDigestStatus: "MATCH"
      });
      const repair = join(root, "repair");
      const { packet, brief } = repairFiles(repair, investigation, frozen);
      const manifest = repairManifest(packet, brief);
      verifyRepair.mockReturnValue({ valid: true, errors: [], manifest, externalDigestStatus: "MATCH" });

      const attachments = loadVerifiedIncidentAttachments({
        minimizationFile: join(root, "minimization.json"),
        expectedMinimizationDigest: resultDigest,
        repairDirectory: repair,
        expectedRepairDigest: manifest.manifestDigest
      }, investigation, frozen);

      expect(attachments.minimization).toMatchObject({ resultDigest, externalDigestStatus: "MATCH" });
      expect(attachments.repair).toMatchObject({ manifest, brief: { proposedInvariant: { statement: "Keep the proven behavior guarded." } } });
      expect(verifyMinimization).toHaveBeenCalledTimes(2);
      expect(verifyRepair).toHaveBeenCalledTimes(3);
    } finally {
      verifyMinimization.mockReset();
      verifyRepair.mockReset();
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("rejects a separately valid minimization from a different frozen witness", () => {
    const { investigation, frozen, transition } = proofFixture();
    const result = minimizationFixture(frozen, transition);
    result.witness = { ...result.witness!, frozenDigest: digest("0") };
    verifyMinimization.mockReturnValue({
      valid: true,
      errors: [],
      result,
      resultDigest: digestJson(result),
      externalDigestStatus: "NOT_PROVIDED"
    });
    try {
      expect(() => loadVerifiedIncidentAttachments({ minimizationFile: "different.json" }, investigation, frozen))
        .toThrow(/different incident.*frozen digest/i);
    } finally {
      verifyMinimization.mockReset();
    }
  });

  it("rejects repair guidance whose evidence packet belongs to another investigation", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-incident-attachments-repair-"));
    try {
      const { investigation, frozen } = proofFixture();
      const repair = join(root, "repair");
      const { brief } = repairFiles(repair, investigation, frozen);
      const packetFile = join(repair, "evidence-packet.json");
      const packet = JSON.parse(String(readFileSync(packetFile))) as Record<string, unknown>;
      const unsigned = { ...packet, investigationDigest: digest("0") };
      delete unsigned.packetDigest;
      const changedPacket = { ...unsigned, packetDigest: digestJson(unsigned) };
      writeFileSync(packetFile, JSON.stringify(changedPacket), "utf8");
      const changedBrief = { ...brief, evidencePacketDigest: changedPacket.packetDigest };
      writeFileSync(join(repair, "repair-brief.json"), JSON.stringify(changedBrief), "utf8");
      verifyRepair.mockReturnValue({ valid: true, errors: [], manifest: repairManifest(changedPacket, changedBrief), externalDigestStatus: "NOT_PROVIDED" });

      expect(() => loadVerifiedIncidentAttachments({ repairDirectory: repair }, investigation, frozen))
        .toThrow(/different Git investigation/i);
    } finally {
      verifyRepair.mockReset();
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("rejects a repair payload that does not match the verifier's manifest snapshot", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-incident-attachments-snapshot-"));
    try {
      const { investigation, frozen } = proofFixture();
      const repair = join(root, "repair");
      const { packet, brief } = repairFiles(repair, investigation, frozen);
      const manifest = repairManifest(packet, brief);
      verifyRepair.mockReturnValue({
        valid: true,
        errors: [],
        manifest: { ...manifest, repairBrief: { ...manifest.repairBrief, digest: digest("0") } },
        externalDigestStatus: "MATCH"
      });

      expect(() => loadVerifiedIncidentAttachments({ repairDirectory: repair }, investigation, frozen))
        .toThrow(/in-memory payload does not match its verified manifest/i);
    } finally {
      verifyRepair.mockReset();
      rmSync(root, { recursive: true, force: true });
    }
  });
});
