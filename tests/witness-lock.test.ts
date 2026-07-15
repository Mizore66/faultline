import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { sha256 } from "../src/canonical.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  verifyFrozenWitness,
  verifyFrozenWitnessRecord
} from "../src/witness-lock.js";

function proposalInput(id = "settlement-currency"): unknown {
  return {
    proposalId: id,
    proposalOrigin: "MODEL",
    proposedAt: "2026-07-16T09:00:00.000Z",
    incidentPacket: {
      symptom: "Completed refunds select an unexpected settlement currency.",
      ciLog: "AssertionError: expected settlement currency to remain USD",
      repositoryLanguage: "TypeScript",
      repositorySummary: "Settlement renderer and refund reconciliation service."
    },
    witness: {
      behavior: "Completed refunds preserve settlement currency.",
      command: "node witness.mjs\r\n",
      overlays: [
        { path: "scripts/fixture.bin", bytesBase64: Buffer.from([0, 13, 10, 255]).toString("base64") },
        { path: "witness.mjs", bytesBase64: Buffer.from("console.log('reviewed')\r\n", "utf8").toString("base64") }
      ],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  };
}

describe("persistent witness lock", () => {
  it("binds a human approval to exact command and overlay bytes before a write-once freeze", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-witness-lock-"));
    try {
      const proposal = proposeWitness(store, proposalInput());
      expect(proposal.witness.commandDigest).toBe(`sha256:${sha256(Buffer.from("node witness.mjs\r\n", "utf8"))}`);
      expect(proposal.witness.overlays[0]?.bytesDigest).toBe(`sha256:${sha256(Buffer.from([0, 13, 10, 255]))}`);

      const approval = approveWitnessProposal(store, proposal.proposalId, {
        approvedBy: "ada@example.test",
        approvedAt: "2026-07-16T09:02:00.000Z",
        note: "I reviewed the exact command and overlays."
      });
      const frozen = freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T09:03:00.000Z" });
      const verified = verifyFrozenWitness(store, proposal.proposalId, frozen.frozenDigest);

      expect(approval.reviewerType).toBe("HUMAN");
      expect(frozen.approval.approvedBy).toBe("ada@example.test");
      expect(verified).toMatchObject({ valid: true, externalDigestStatus: "MATCH" });
      expect(() => freezeApprovedWitness(store, proposal.proposalId)).toThrow(/already exists/);
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("refuses to freeze or verify a proposal without a human approval", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-witness-lock-"));
    try {
      const proposal = proposeWitness(store, proposalInput("needs-review"));
      expect(() => freezeApprovedWitness(store, proposal.proposalId)).toThrow(/unapproved/);
      expect(verifyFrozenWitness(store, proposal.proposalId)).toMatchObject({
        valid: false,
        errors: ["human approval is missing; no witness may be frozen"]
      });
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("rejects incident localization material and detects frozen-record mutation or an external digest mismatch", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-witness-lock-"));
    try {
      const input = proposalInput("tamper-check") as Record<string, unknown>;
      const packet = input.incidentPacket as Record<string, unknown>;
      packet.candidateTurn = "cedar-turn-5";
      expect(() => proposeWitness(store, input)).toThrow(/Unrecognized key/);

      const proposal = proposeWitness(store, proposalInput("tamper-check"));
      approveWitnessProposal(store, proposal.proposalId, { approvedBy: "reviewer", approvedAt: "2026-07-16T09:02:00.000Z" });
      const frozen = freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T09:03:00.000Z" });
      expect(verifyFrozenWitness(store, proposal.proposalId, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")).toMatchObject({
        valid: false,
        externalDigestStatus: "MISMATCH"
      });

      const frozenPath = join(store, "frozen", `${proposal.proposalId}.json`);
      const tampered = JSON.parse(readFileSync(frozenPath, "utf8")) as { proposal: { witness: { command: string } } };
      tampered.proposal.witness.command = "node changed-witness.mjs\r\n";
      writeFileSync(frozenPath, `${JSON.stringify(tampered)}\n`, "utf8");

      const verified = verifyFrozenWitness(store, proposal.proposalId, frozen.frozenDigest);
      expect(verified.valid).toBe(false);
      expect(verified.errors.join("\n")).toMatch(/command digest|frozen digest/);
      expect(verifyFrozenWitnessRecord(tampered, frozen.frozenDigest).valid).toBe(false);
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });
});
