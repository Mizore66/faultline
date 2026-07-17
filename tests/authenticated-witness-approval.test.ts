import { createHash, generateKeyPairSync } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { digestJson } from "../src/canonical.js";
import {
  signAuthenticatedWitnessApproval,
  verifyAuthenticatedWitnessApproval,
  type AuthenticatedWitnessApproval,
  type ReviewerKeyring
} from "../src/authenticated-witness-approval.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness
} from "../src/witness-lock.js";

function createFrozenWitness(store: string, proposalId = "signed-witness") {
  const proposal = proposeWitness(store, {
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T10:00:00.000Z",
    incidentPacket: {
      symptom: "A regression witness is needed.",
      ciLog: "Expected green but observed red.",
      repositoryLanguage: "TypeScript",
      repositorySummary: "Authenticated witness approval test fixture."
    },
    witness: {
      behavior: "The fixture stays green.",
      command: "node witness.mjs",
      overlays: [{ path: "witness.mjs", bytesBase64: Buffer.from("process.exit(0)\n", "utf8").toString("base64") }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 10 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: "2026-07-16T10:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T10:02:00.000Z" });
}

function reviewerKey(): { privateKeyPem: string; keyring: ReviewerKeyring } {
  const pair = generateKeyPairSync("ed25519");
  const publicKeyPem = pair.publicKey.export({ type: "spki", format: "pem" }).toString();
  const keyFingerprint = `sha256:${createHash("sha256").update(pair.publicKey.export({ type: "spki", format: "der" })).digest("hex")}`;
  return {
    privateKeyPem: pair.privateKey.export({ type: "pkcs8", format: "pem" }).toString(),
    keyring: {
      schemaVersion: "faultline.reviewer-keyring.v1",
      reviewers: [{
        approvedBy: "reviewer@example.test",
        keyId: keyFingerprint,
        algorithm: "ED25519",
        publicKeyPem
      }]
    }
  };
}

function rehash(receipt: AuthenticatedWitnessApproval): AuthenticatedWitnessApproval {
  const { receiptDigest: _receiptDigest, ...payload } = receipt;
  return { ...payload, receiptDigest: digestJson(payload) };
}

describe("authenticated frozen-witness approvals", () => {
  it("optionally signs the exact frozen witness with a trusted reviewer-held Ed25519 key", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-authenticated-witness-"));
    try {
      const frozen = createFrozenWitness(store);
      const reviewer = reviewerKey();
      expect(verifyAuthenticatedWitnessApproval(store, frozen.proposal.proposalId)).toMatchObject({
        valid: true,
        signatureStatus: "NOT_PRESENT"
      });
      expect(verifyAuthenticatedWitnessApproval(store, frozen.proposal.proposalId, {
        keyring: reviewer.keyring,
        requireSignature: true
      })).toMatchObject({
        valid: false,
        signatureStatus: "NOT_PRESENT"
      });

      const receipt = signAuthenticatedWitnessApproval(store, frozen.proposal.proposalId, {
        privateKeyPem: reviewer.privateKeyPem,
        keyring: reviewer.keyring,
        signedAt: "2026-07-16T10:03:00.000Z"
      });
      expect(receipt).toMatchObject({
        frozenDigest: frozen.frozenDigest,
        witnessDigest: frozen.witnessDigest,
        approvalDigest: frozen.approval.approvalDigest,
        approvedBy: frozen.approval.approvedBy,
        algorithm: "ED25519"
      });
      const verification = verifyAuthenticatedWitnessApproval(store, frozen.proposal.proposalId, {
        expectedFrozenDigest: frozen.frozenDigest,
        keyring: reviewer.keyring,
        requireSignature: true
      });
      expect(verification).toMatchObject({ valid: true, signatureStatus: "VERIFIED" });
      const persisted = readFileSync(join(store, "authenticated-approvals", `${frozen.proposal.proposalId}.json`), "utf8");
      expect(persisted).not.toContain(reviewer.privateKeyPem);
      expect(() => signAuthenticatedWitnessApproval(store, frozen.proposal.proposalId, {
        privateKeyPem: reviewer.privateKeyPem,
        keyring: reviewer.keyring
      })).toThrow(/already exists/);
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("fails closed when a rehashed receipt no longer has a valid signature or trusted key", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-authenticated-witness-tamper-"));
    try {
      const frozen = createFrozenWitness(store, "tampered-signed-witness");
      const reviewer = reviewerKey();
      signAuthenticatedWitnessApproval(store, frozen.proposal.proposalId, {
        privateKeyPem: reviewer.privateKeyPem,
        keyring: reviewer.keyring,
        signedAt: "2026-07-16T10:03:00.000Z"
      });
      const path = join(store, "authenticated-approvals", `${frozen.proposal.proposalId}.json`);
      const tampered = JSON.parse(readFileSync(path, "utf8")) as AuthenticatedWitnessApproval;
      tampered.frozenDigest = `sha256:${"a".repeat(64)}`;
      writeFileSync(path, `${JSON.stringify(rehash(tampered))}\n`, "utf8");
      expect(verifyAuthenticatedWitnessApproval(store, frozen.proposal.proposalId, {
        keyring: reviewer.keyring,
        requireSignature: true
      })).toMatchObject({ valid: false, signatureStatus: "INVALID" });

      const otherReviewer = reviewerKey();
      expect(verifyAuthenticatedWitnessApproval(store, frozen.proposal.proposalId, {
        keyring: otherReviewer.keyring,
        requireSignature: true
      })).toMatchObject({ valid: false, signatureStatus: "UNTRUSTED" });
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });
});
