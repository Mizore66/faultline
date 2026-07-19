import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { writePreventionProofFromTurnMaterialization } from "../src/prevention-from-turn.js";
import type { StableTurnTransition } from "../src/turn-investigation.js";

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function commit(repository: string, message: string): { commit: string; tree: string } {
  git(repository, ["add", "-A"]);
  git(repository, ["commit", "-m", message]);
  return {
    commit: git(repository, ["rev-parse", "HEAD"]),
    tree: git(repository, ["rev-parse", "HEAD^{tree}"])
  };
}

describe("prevention from turn materialization (CDX-03)", () => {
  it("stays on PREVENTION_EVIDENCE_SUMMARY when PASS→FAIL materialization is refused", () => {
    const transition: StableTurnTransition = {
      kind: "FAIL_TO_PASS",
      before: {
        stateIndex: 0,
        turnId: "t0",
        turnOrdinal: 1,
        role: "TURN",
        treeDigest: "a".repeat(40),
        snapshotDigest: `sha256:${"a".repeat(64)}`,
        verdict: "FAIL",
        executionIds: [`sha256:${"b".repeat(64)}`, `sha256:${"c".repeat(64)}`, `sha256:${"d".repeat(64)}`],
        runIds: [`sha256:${"e".repeat(64)}`, `sha256:${"f".repeat(64)}`, `sha256:${"1".repeat(64)}`]
      },
      after: {
        stateIndex: 1,
        turnId: "t1",
        turnOrdinal: 2,
        role: "TURN",
        treeDigest: "b".repeat(40),
        snapshotDigest: `sha256:${"2".repeat(64)}`,
        verdict: "PASS",
        executionIds: [`sha256:${"3".repeat(64)}`, `sha256:${"4".repeat(64)}`, `sha256:${"5".repeat(64)}`],
        runIds: [`sha256:${"6".repeat(64)}`, `sha256:${"7".repeat(64)}`, `sha256:${"8".repeat(64)}`]
      }
    };
    const result = writePreventionProofFromTurnMaterialization({
      repository: process.cwd(),
      turnResult: { transitions: [transition] },
      bundleDirectory: process.cwd(),
      expectRoot: `sha256:${"9".repeat(64)}`,
      outputDirectory: join(tmpdir(), "faultline-prevention-turn-refuse"),
      repaired: {
        commit: "c".repeat(40),
        tree: "d".repeat(40),
        runs: []
      },
      repairPatchDigest: `sha256:${"e".repeat(64)}`
    });
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.classification).toBe("PREVENTION_EVIDENCE_SUMMARY");
      expect(result.reasons.join(" ")).toMatch(/PASS→FAIL|materialization/i);
    }
  });

  it("materializes Git-shaped commits then defers to the Git prevention verifier", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-prevention-turn-"));
    try {
      git(root, ["init"]);
      git(root, ["config", "user.email", "faultline@example.test"]);
      git(root, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(root, "state.txt"), "good\n", "utf8");
      const before = commit(root, "good");
      writeFileSync(join(root, "state.txt"), "bad\n", "utf8");
      const after = commit(root, "bad");
      const transition: StableTurnTransition = {
        kind: "PASS_TO_FAIL",
        before: {
          stateIndex: 0,
          turnId: "turn-good",
          turnOrdinal: 1,
          role: "TURN",
          treeDigest: before.tree,
          snapshotDigest: `sha256:${"a".repeat(64)}`,
          verdict: "PASS",
          executionIds: [`sha256:${"b".repeat(64)}`, `sha256:${"c".repeat(64)}`, `sha256:${"d".repeat(64)}`],
          runIds: [`sha256:${"e".repeat(64)}`, `sha256:${"f".repeat(64)}`, `sha256:${"1".repeat(64)}`]
        },
        after: {
          stateIndex: 1,
          turnId: "turn-bad",
          turnOrdinal: 2,
          role: "TURN",
          treeDigest: after.tree,
          snapshotDigest: `sha256:${"2".repeat(64)}`,
          verdict: "FAIL",
          executionIds: [`sha256:${"3".repeat(64)}`, `sha256:${"4".repeat(64)}`, `sha256:${"5".repeat(64)}`],
          runIds: [`sha256:${"6".repeat(64)}`, `sha256:${"7".repeat(64)}`, `sha256:${"8".repeat(64)}`]
        }
      };
      const result = writePreventionProofFromTurnMaterialization({
        repository: root,
        turnResult: { transitions: [transition] },
        bundleDirectory: join(root, "missing-bundle"),
        expectRoot: `sha256:${"9".repeat(64)}`,
        outputDirectory: join(root, "prevention-out"),
        repaired: {
          commit: "c".repeat(40),
          tree: "d".repeat(40),
          runs: []
        },
        repairPatchDigest: `sha256:${"e".repeat(64)}`
      });
      expect(result.ok).toBe(false);
      if (!result.ok) {
        expect(result.classification).toBe("PREVENTION_EVIDENCE_SUMMARY");
        expect(result.reasons.join(" ")).toMatch(/Git prevention verifier|materialised|bundle|proof/i);
      }
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
