import { existsSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { verifyFrozenWitness, proposeWitness } from "../src/witness-lock.js";
import {
  WitnessReviewError,
  approveWitnessReview,
  freezeWitnessReview,
  openWitnessReview,
  verifyWitnessReview
} from "../src/witness-review.js";

function proposalInput(id = "reviewable-witness"): unknown {
  return {
    proposalId: id,
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T15:00:00.000Z",
    incidentPacket: {
      symptom: "CI reports a failing checkout test.",
      ciLog: "Expected checkout to preserve the currency value.",
      repositoryLanguage: "TypeScript",
      repositorySummary: "A local checkout service under incident review."
    },
    witness: {
      behavior: "Checkout preserves the currency value.",
      command: "node witness.mjs\r\n",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from("console.log('review only')\r\n", "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  };
}

describe("local witness-review workbench", () => {
  it("opens a read-only snapshot of exact command and overlay bytes without approving, freezing, or executing", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-witness-review-"));
    try {
      const proposal = proposeWitness(store, proposalInput());
      const review = openWitnessReview(store, proposal.proposalId);

      expect(review).toMatchObject({
        mode: "LOCAL_READ_ONLY",
        proposal: {
          proposalId: proposal.proposalId,
          witness: {
            command: "node witness.mjs\r\n",
            commandDigest: proposal.witness.commandDigest,
            overlays: [{ path: "witness.mjs", bytesDigest: proposal.witness.overlays[0]?.bytesDigest }]
          }
        },
        safeguards: {
          packet: "STRICTLY_BLINDED_PROTOCOL_SHAPE",
          command: "NOT_EXECUTED",
          overlays: "NOT_MATERIALIZED",
          approval: "EXPLICIT_HUMAN_ACTION_REQUIRED",
          freeze: "EXPLICIT_HUMAN_ACTION_REQUIRED"
        }
      });
      expect(review.proposal.witness.overlays[0]?.bytesBase64).toBe(
        Buffer.from("console.log('review only')\r\n", "utf8").toString("base64")
      );
      expect(Object.isFrozen(review)).toBe(true);
      expect(verifyWitnessReview(review)).toEqual({ valid: true, errors: [] });
      expect(existsSync(join(store, "approvals", `${proposal.proposalId}.json`))).toBe(false);
      expect(existsSync(join(store, "frozen", `${proposal.proposalId}.json`))).toBe(false);
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("requires matching human confirmation before write-once approval and freeze", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-witness-review-confirm-"));
    try {
      const proposal = proposeWitness(store, proposalInput("confirm-review"));
      const review = openWitnessReview(store, proposal.proposalId);

      expect(() => approveWitnessReview(store, review, {
        reviewDigest: `sha256:${"a".repeat(64)}`,
        approvedBy: "reviewer@example.test"
      })).toThrow(/confirmation/);
      expect(existsSync(join(store, "approvals", `${proposal.proposalId}.json`))).toBe(false);

      const approval = approveWitnessReview(store, review, {
        reviewDigest: review.reviewDigest,
        approvedBy: "reviewer@example.test",
        approvedAt: "2026-07-16T15:01:00.000Z",
        note: "I reviewed the exact command and base64 overlay bytes."
      });
      expect(approval.approvedBy).toBe("reviewer@example.test");
      const frozen = freezeWitnessReview(store, review, {
        reviewDigest: review.reviewDigest,
        frozenAt: "2026-07-16T15:02:00.000Z"
      });
      expect(verifyFrozenWitness(store, proposal.proposalId, frozen.frozenDigest)).toMatchObject({
        valid: true,
        externalDigestStatus: "MATCH"
      });
      expect(() => approveWitnessReview(store, review, {
        reviewDigest: review.reviewDigest,
        approvedBy: "reviewer@example.test"
      })).toThrow(/already exists/);
      expect(() => freezeWitnessReview(store, review, { reviewDigest: review.reviewDigest })).toThrow(/already exists/);
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("refuses an unblinded packet before it can be frozen", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-witness-review-unblinded-"));
    try {
      const proposal = proposeWitness(store, proposalInput("unblinded-review"));
      const review = openWitnessReview(store, proposal.proposalId);
      approveWitnessReview(store, review, {
        reviewDigest: review.reviewDigest,
        approvedBy: "reviewer@example.test",
        approvedAt: "2026-07-16T15:01:00.000Z"
      });

      const proposalPath = join(store, "proposals", `${proposal.proposalId}.json`);
      const unblinded = JSON.parse(readFileSync(proposalPath, "utf8")) as { incidentPacket: Record<string, unknown> };
      unblinded.incidentPacket.candidateCommit = "secret-localization-detail";
      writeFileSync(proposalPath, `${JSON.stringify(unblinded)}\n`, "utf8");

      expect(() => freezeWitnessReview(store, review, {
        reviewDigest: review.reviewDigest,
        frozenAt: "2026-07-16T15:02:00.000Z"
      })).toThrow(/strictly blinded protocol packet/);
      expect(existsSync(join(store, "frozen", `${proposal.proposalId}.json`))).toBe(false);

      try {
        openWitnessReview(store, proposal.proposalId);
        throw new Error("expected an unblinded packet to be refused");
      } catch (error) {
        expect(error).toBeInstanceOf(WitnessReviewError);
        expect((error as WitnessReviewError).code).toBe("UNBLINDED_OR_INVALID_PROPOSAL");
      }
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });
});
