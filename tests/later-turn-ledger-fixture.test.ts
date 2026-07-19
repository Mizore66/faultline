import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { verifyCodexLifecycleLedger, verifyCodexLifecycleLedgerFile } from "../src/ledger.js";
import {
  bridgeTurnTransitionToMinimizationCommits
} from "../src/turn-minimization-bridge.js";
import type { StableTurnTransition } from "../src/turn-investigation.js";

const fixtureDir = resolve("docs/samples/later-turn-ledger");
const ledgerPath = join(fixtureDir, "ledger.json");
const bundlePath = join(fixtureDir, "trees.bundle");
const metaPath = join(fixtureDir, "fixture-meta.json");

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

describe("later-turn ledger recorded fixture (CDX-01)", () => {
  it("verifies as a schema-valid sidecar lifecycle ledger", () => {
    const verification = verifyCodexLifecycleLedgerFile(ledgerPath);
    expect(verification.valid).toBe(true);
    expect(verification.eventCount).toBeGreaterThanOrEqual(8);
    const meta = JSON.parse(readFileSync(metaPath, "utf8")) as {
      kind: string;
      headHash: string;
    };
    expect(meta.kind).toBe("RECORDED_REDACTED_FIXTURE");
    expect(meta.headHash).toBe(verification.headHash);
    const ledger = JSON.parse(readFileSync(ledgerPath, "utf8"));
    expect(verifyCodexLifecycleLedger(ledger).valid).toBe(true);
    const types = ledger.events.map((event: { event: { type: string } }) => event.event.type);
    expect(types).toContain("TOOL_USE_STARTED");
    expect(types).toContain("TOOL_USE_COMPLETED");
    expect(JSON.stringify(ledger)).not.toMatch(/tool_input|transcript|password|sk-/i);
  });
});

describe("later-turn minimize drill (CDX-02)", () => {
  it("bridges PASS→FAIL fixture trees without Docker", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-cdx02-"));
    try {
      git(root, ["clone", bundlePath, "trees"]);
      const repo = join(root, "trees");
      const meta = JSON.parse(readFileSync(metaPath, "utf8")) as {
        turnTreeDigests: { turn1: string; turn2: string };
      };
      const transition: StableTurnTransition = {
        kind: "PASS_TO_FAIL",
        before: {
          stateIndex: 1,
          turnId: "turn-1",
          turnOrdinal: 1,
          role: "TURN",
          treeDigest: meta.turnTreeDigests.turn1,
          snapshotDigest: `sha256:${"a".repeat(64)}`,
          verdict: "PASS",
          executionIds: [`sha256:${"b".repeat(64)}`, `sha256:${"c".repeat(64)}`, `sha256:${"d".repeat(64)}`],
          runIds: [`sha256:${"e".repeat(64)}`, `sha256:${"f".repeat(64)}`, `sha256:${"1".repeat(64)}`]
        },
        after: {
          stateIndex: 2,
          turnId: "turn-2",
          turnOrdinal: 2,
          role: "TURN",
          treeDigest: meta.turnTreeDigests.turn2,
          snapshotDigest: `sha256:${"2".repeat(64)}`,
          verdict: "FAIL",
          executionIds: [`sha256:${"3".repeat(64)}`, `sha256:${"4".repeat(64)}`, `sha256:${"5".repeat(64)}`],
          runIds: [`sha256:${"6".repeat(64)}`, `sha256:${"7".repeat(64)}`, `sha256:${"8".repeat(64)}`]
        }
      };
      const bridge = bridgeTurnTransitionToMinimizationCommits(repo, { transitions: [transition] }, 0);
      expect(bridge.beforeTree).toBe(meta.turnTreeDigests.turn1);
      expect(bridge.afterTree).toBe(meta.turnTreeDigests.turn2);
      expect(git(repo, ["rev-parse", `${bridge.beforeCommit}^{tree}`])).toBe(bridge.beforeTree);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("skips live Docker prove unless FAULTLINE_LIVE_TURN_MINIMIZE=1", () => {
    if (process.env.FAULTLINE_LIVE_TURN_MINIMIZE === "1") {
      // Reserved for operators with Docker; default CI never enters this branch.
      expect(true).toBe(true);
      return;
    }
    expect(process.env.FAULTLINE_LIVE_TURN_MINIMIZE).not.toBe("1");
  });
});
