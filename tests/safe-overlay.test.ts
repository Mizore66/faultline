import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { materializeFrozenOverlays, safeOverlayTarget } from "../src/safe-overlay.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../src/witness-lock.js";

function createFrozenWitness(store: string, proposalId: string, overlayPath: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-18T01:00:00.000Z",
    incidentPacket: {
      symptom: "Overlay path safety fixture.",
      ciLog: "safe-overlay containment checks",
      repositoryLanguage: "Text fixture",
      repositorySummary: "Temporary worktree used only for overlay writer tests."
    },
    witness: {
      behavior: "Overlays must stay inside the worktree.",
      command: "node witness.mjs",
      overlays: [{
        path: overlayPath,
        bytesBase64: Buffer.from("export const frozenWitness = 'exact-approved-bytes';\n", "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: "2026-07-18T01:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-18T01:02:00.000Z" });
}

describe("shared safe overlay writer", () => {
  it("rejects overlay paths that attempt ../escape", async () => {
    const worktree = mkdtempSync(join(tmpdir(), "faultline-safe-overlay-escape-"));
    try {
      await expect(safeOverlayTarget(worktree, "../escape.mjs")).rejects.toThrow(/unsafe|escap/i);
      await expect(safeOverlayTarget(worktree, "/tmp/escape.mjs")).rejects.toThrow(/unsafe/i);
      await expect(safeOverlayTarget(worktree, "nested/../../outside.mjs")).rejects.toThrow(/unsafe|escap/i);
    } finally {
      rmSync(worktree, { recursive: true, force: true });
    }
  });

  it("rejects overlay writes when the target path is a symlink", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-safe-overlay-symlink-"));
    const store = join(root, "store");
    const worktree = join(root, "worktree");
    try {
      mkdirSync(store, { recursive: true });
      mkdirSync(worktree, { recursive: true });
      writeFileSync(join(root, "outside-target.mjs"), "export const outside = true;\n", "utf8");
      try {
        symlinkSync(join(root, "outside-target.mjs"), join(worktree, "witness.mjs"));
      } catch {
        // Symlink creation can be denied on some Windows images.
        return;
      }

      const frozen = createFrozenWitness(store, "symlink-overlay-target", "witness.mjs");
      await expect(materializeFrozenOverlays(worktree, frozen)).rejects.toThrow(/symbolic link/i);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
