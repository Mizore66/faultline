import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  bridgeTurnTransitionToMinimizationCommits,
  selectTurnTransition
} from "../src/turn-minimization-bridge.js";
import type { StableTurnTransition } from "../src/turn-investigation.js";

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function commit(repository: string, message: string): { commit: string; tree: string } {
  git(repository, ["add", "-A"]);
  git(repository, ["commit", "-m", message]);
  const commitOid = git(repository, ["rev-parse", "HEAD"]);
  const tree = git(repository, ["rev-parse", "HEAD^{tree}"]);
  return { commit: commitOid, tree };
}

describe("turn minimization bridge", () => {
  it("selects transition 0 and synthesizes orphan commits from turn trees", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-min-bridge-"));
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
          stateIndex: 2,
          turnId: "turn-2",
          turnOrdinal: 2,
          role: "TURN",
          treeDigest: before.tree,
          snapshotDigest: `sha256:${"a".repeat(64)}`,
          verdict: "PASS",
          executionIds: [`sha256:${"b".repeat(64)}`, `sha256:${"c".repeat(64)}`, `sha256:${"d".repeat(64)}`],
          runIds: [`sha256:${"e".repeat(64)}`, `sha256:${"f".repeat(64)}`, `sha256:${"1".repeat(64)}`]
        },
        after: {
          stateIndex: 3,
          turnId: "turn-3",
          turnOrdinal: 3,
          role: "TURN",
          treeDigest: after.tree,
          snapshotDigest: `sha256:${"2".repeat(64)}`,
          verdict: "FAIL",
          executionIds: [`sha256:${"3".repeat(64)}`, `sha256:${"4".repeat(64)}`, `sha256:${"5".repeat(64)}`],
          runIds: [`sha256:${"6".repeat(64)}`, `sha256:${"7".repeat(64)}`, `sha256:${"8".repeat(64)}`]
        }
      };

      expect(selectTurnTransition({ transitions: [transition] }, 0)).toEqual(transition);
      const bridge = bridgeTurnTransitionToMinimizationCommits(root, { transitions: [transition] }, 0);
      expect(bridge.beforeTree).toBe(before.tree);
      expect(bridge.afterTree).toBe(after.tree);
      expect(git(root, ["rev-parse", `${bridge.beforeCommit}^{tree}`])).toBe(before.tree);
      expect(git(root, ["rev-parse", `${bridge.afterCommit}^{tree}`])).toBe(after.tree);
      expect(bridge.note).toMatch(/Synthetic orphan commits/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses FAIL→PASS for the first-cut minimizer bridge", () => {
    const transition = {
      kind: "FAIL_TO_PASS" as const,
      before: {
        stateIndex: 0,
        turnId: "t0",
        turnOrdinal: 0,
        role: "SESSION_BASELINE" as const,
        treeDigest: "a".repeat(40),
        snapshotDigest: `sha256:${"a".repeat(64)}`,
        verdict: "FAIL" as const,
        executionIds: [`sha256:${"b".repeat(64)}`, `sha256:${"c".repeat(64)}`, `sha256:${"d".repeat(64)}`],
        runIds: [`sha256:${"e".repeat(64)}`, `sha256:${"f".repeat(64)}`, `sha256:${"1".repeat(64)}`]
      },
      after: {
        stateIndex: 1,
        turnId: "t1",
        turnOrdinal: 1,
        role: "TURN" as const,
        treeDigest: "b".repeat(40),
        snapshotDigest: `sha256:${"2".repeat(64)}`,
        verdict: "PASS" as const,
        executionIds: [`sha256:${"3".repeat(64)}`, `sha256:${"4".repeat(64)}`, `sha256:${"5".repeat(64)}`],
        runIds: [`sha256:${"6".repeat(64)}`, `sha256:${"7".repeat(64)}`, `sha256:${"8".repeat(64)}`]
      }
    };
    expect(() => selectTurnTransition({ transitions: [transition] }, 1)).toThrow(/no transition at index 1/);
    expect(() =>
      bridgeTurnTransitionToMinimizationCommits(".", { transitions: [transition] }, 0)
    ).toThrow(/PASS→FAIL/);
  });
});
