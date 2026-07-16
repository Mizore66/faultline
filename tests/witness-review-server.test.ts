import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { createIncidentDraft } from "../src/incident.js";
import { writeIncidentDraft } from "../src/incident-store.js";
import { proposeWitness, verifyFrozenWitness } from "../src/witness-lock.js";
import { startWitnessReviewServer } from "../src/witness-review-server.js";

function formValue(page: string, name: string): string {
  const match = new RegExp(`<input type="hidden" name="${name}" value="([^"]+)"`).exec(page);
  if (!match?.[1]) throw new Error(`missing ${name} in review page`);
  return match[1];
}

describe("local witness review server", () => {
  it("renders exact witness material locally and requires an explicit tokened approval then freeze", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-witness-review-server-"));
    const proposal = proposeWitness(store, {
      proposalId: "review-server",
      proposalOrigin: "HUMAN",
      proposedAt: "2026-07-16T16:00:00.000Z",
      incidentPacket: {
        symptom: "Checkout must preserve a currency value.",
        ciLog: "private failing CI log text",
        repositoryLanguage: "TypeScript",
        repositorySummary: "Temporary local review-server test."
      },
      witness: {
        behavior: "Checkout preserves the currency value.",
        command: "node witness.mjs --review-only",
        overlays: [{
          path: "witness.mjs",
          bytesBase64: Buffer.from("console.log('review server bytes')\n", "utf8").toString("base64")
        }],
        policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
      }
    });
    const draftStore = join(store, "incidents");
    const draft = createIncidentDraft({
      draftId: proposal.proposalId,
      createdAt: "2026-07-16T16:00:01.000Z",
      repository: "/private/review-server-fixture",
      command: proposal.witness.command,
      range: { ancestor: "known-good-commit", descendant: "known-bad-commit" },
      runtime: {
        requested: "node:22-alpine",
        image: `registry.example/faultline@sha256:${"a".repeat(64)}`
      },
      proposal: {
        proposalId: proposal.proposalId,
        proposalDigest: proposal.proposalDigest,
        incidentPacketDigest: proposal.incidentPacketDigest,
        commandDigest: proposal.witness.commandDigest
      }
    });
    writeIncidentDraft(draftStore, draft);
    const server = await startWitnessReviewServer({ store, proposalId: proposal.proposalId, draftStore });
    try {
      const initial = await fetch(server.url);
      const initialPage = await initial.text();
      expect(initial.status).toBe(200);
      expect(initial.headers.get("content-security-policy")).toContain("default-src 'none'");
      expect(initial.headers.get("cache-control")).toBe("no-store");
      expect(initialPage).toContain("node witness.mjs --review-only");
      expect(initialPage).toContain(proposal.witness.overlays[0]!.bytesBase64);
      expect(initialPage).toContain("known-good-commit");
      expect(initialPage).toContain("known-bad-commit");
      expect(initialPage).toContain("registry.example/faultline@sha256");
      expect(initialPage).not.toContain("private failing CI log text");
      expect(initialPage).not.toMatch(/<script\b/i);

      const token = formValue(initialPage, "token");
      const reviewDigest = formValue(initialPage, "reviewDigest");
      const forged = await fetch(`${server.url}/approve`, {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ token: "wrong", reviewDigest, approvedBy: "reviewer@example.test" })
      });
      expect(forged.status).toBe(403);
      expect(existsSync(join(store, "approvals", `${proposal.proposalId}.json`))).toBe(false);

      const unacknowledged = await fetch(`${server.url}/approve`, {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ token, reviewDigest, approvedBy: "reviewer@example.test" })
      });
      expect(unacknowledged.status).toBe(409);
      expect(await unacknowledged.text()).toContain("requires acknowledgement");
      expect(existsSync(join(store, "approvals", `${proposal.proposalId}.json`))).toBe(false);

      const approved = await fetch(`${server.url}/approve`, {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ token, reviewDigest, acknowledged: "exact-witness-reviewed", approvedBy: "reviewer@example.test", note: "I reviewed the local screen." })
      });
      expect(approved.status).toBe(200);
      const approvedPage = await approved.text();
      expect(approvedPage).toContain("Human approval was recorded");
      expect(approvedPage).toContain("Freeze this approved witness");

      const frozen = await fetch(`${server.url}/freeze`, {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ token, reviewDigest })
      });
      expect(frozen.status).toBe(200);
      expect(await frozen.text()).toContain("The approved witness is now frozen");
      expect(verifyFrozenWitness(store, proposal.proposalId)).toMatchObject({ valid: true });
    } finally {
      await server.close();
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("refuses a persisted incident draft whose proposal binding does not match the reviewable witness", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-witness-review-draft-mismatch-"));
    const proposal = proposeWitness(store, {
      proposalId: "mismatched-draft",
      proposalOrigin: "HUMAN",
      proposedAt: "2026-07-16T16:10:00.000Z",
      incidentPacket: {
        symptom: "A witness must remain reviewed.",
        ciLog: "strictly blinded test input",
        repositoryLanguage: "TypeScript",
        repositorySummary: "Temporary mismatch test."
      },
      witness: {
        behavior: "The reviewed witness remains intact.",
        command: "node witness.mjs",
        overlays: [],
        policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
      }
    });
    const draftStore = join(store, "incidents");
    writeIncidentDraft(draftStore, createIncidentDraft({
      draftId: proposal.proposalId,
      createdAt: "2026-07-16T16:10:01.000Z",
      repository: "/private/mismatch-fixture",
      command: proposal.witness.command,
      range: { ancestor: "known-good", descendant: "known-bad" },
      proposal: {
        proposalId: proposal.proposalId,
        proposalDigest: `sha256:${"b".repeat(64)}`,
        incidentPacketDigest: proposal.incidentPacketDigest,
        commandDigest: proposal.witness.commandDigest
      }
    }));
    const server = await startWitnessReviewServer({ store, proposalId: proposal.proposalId, draftStore });
    try {
      const response = await fetch(server.url);
      expect(response.status).toBe(409);
      expect(await response.text()).toContain("does not match the stored witness proposal");
      expect(existsSync(join(store, "approvals", `${proposal.proposalId}.json`))).toBe(false);
      expect(existsSync(join(store, "frozen", `${proposal.proposalId}.json`))).toBe(false);
    } finally {
      await server.close();
      rmSync(store, { recursive: true, force: true });
    }
  });
});
