import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import fc from "fast-check";
import { describe, expect, it } from "vitest";
import { materializeFrozenOverlays, safeOverlayTarget } from "../src/safe-overlay.js";
import {
  isProtectedEnvironmentDescriptorPath,
  planTurnSnapshotPaths
} from "../src/turn-snapshot.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness
} from "../src/witness-lock.js";

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function createFrozenWitness(store: string, overlayPath: string) {
  const proposal = proposeWitness(store, {
    proposalId: "fuzz-overlay",
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-19T00:00:00.000Z",
    incidentPacket: {
      symptom: "fuzz",
      ciLog: "fuzz",
      repositoryLanguage: "Text",
      repositorySummary: "fuzz"
    },
    witness: {
      behavior: "fuzz",
      command: "node -e process.exit(0)",
      overlays: [{ path: overlayPath, bytesBase64: Buffer.from("ok\n", "utf8").toString("base64") }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 5 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "fuzz@test",
    approvedAt: "2026-07-19T00:00:01.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-19T00:00:02.000Z" });
}

describe("overlay path safety (fast-check)", () => {
  it("refuses traversal and absolute overlay paths at resolve and propose", async () => {
    await fc.assert(
      fc.asyncProperty(
        fc.constantFrom(
          "../escape.mjs",
          "../../etc/passwd",
          "..\\escape.mjs",
          "/tmp/evil.mjs",
          "nested/../../outside.mjs",
          "./../escape.mjs"
        ),
        async (path) => {
          const root = mkdtempSync(join(tmpdir(), "faultline-overlay-fuzz-"));
          try {
            await expect(safeOverlayTarget(root, path)).rejects.toThrow(/unsafe|escap|path/i);
            const store = join(root, "store");
            mkdirSync(store, { recursive: true });
            // Proposal schema also refuses unsafe overlay paths (fail closed before freeze).
            expect(() => createFrozenWitness(store, path)).toThrow(/safe POSIX-relative|overlay path/i);
          } finally {
            rmSync(root, { recursive: true, force: true });
          }
        }
      ),
      { numRuns: 200 }
    );
  });

  it("materialize refuses when a frozen overlay path escapes the worktree", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-overlay-materialize-"));
    try {
      const store = join(root, "store");
      mkdirSync(store, { recursive: true });
      const frozen = createFrozenWitness(store, "witness.mjs");
      // Tamper post-freeze: swap in a traversal path (defense in materialize).
      const tampered = structuredClone(frozen) as typeof frozen & {
        proposal: { witness: { overlays: Array<{ path: string }> } };
      };
      tampered.proposal.witness.overlays[0]!.path = "../escape.mjs";
      await expect(materializeFrozenOverlays(root, tampered)).rejects.toThrow(/unsafe|escap|path/i);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});

describe("ignore / protected descriptors (fast-check)", () => {
  it("never treats protected environment descriptors as suppressible", () => {
    fc.assert(
      fc.property(
        fc.constantFrom(
          "package.json",
          "pnpm-lock.yaml",
          "package-lock.json",
          "yarn.lock",
          "go.mod",
          "go.sum",
          "Cargo.toml",
          "Cargo.lock",
          "Dockerfile"
        ),
        (protectedPath) => {
          expect(isProtectedEnvironmentDescriptorPath(protectedPath)).toBe(true);
        }
      ),
      { numRuns: 10_000 }
    );
  });

  it("hostile ignore patterns do not hang path planning", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-ignore-catastrophic-"));
    try {
      git(root, ["init"]);
      mkdirSync(join(root, "src"), { recursive: true });
      writeFileSync(join(root, "src", "a.ts"), "export {}\n", "utf8");
      writeFileSync(join(root, "package.json"), "{}\n", "utf8");
      writeFileSync(
        join(root, ".faultlineignore"),
        ["********************", "src", "package.json"].join("\n"),
        "utf8"
      );
      const runGit = (repositoryRoot: string, args: readonly string[]) =>
        execFileSync("git", ["-C", repositoryRoot, ...args], { encoding: "utf8" });
      const started = Date.now();
      const plan = planTurnSnapshotPaths(root, runGit, { trackedFilesOnly: false });
      expect(Date.now() - started).toBeLessThan(5_000);
      expect(plan.paths.some((p) => p.replace(/\\/g, "/").endsWith("package.json"))).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
