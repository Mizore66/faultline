import { describe, expect, it } from "vitest";
import { appendLifecycleEvent, createCodexLifecycleLedger } from "../src/ledger.js";
import { captureTurnTreeSnapshot } from "../src/turn-snapshot.js";
import { turnStatesFromLedger } from "../src/turn-investigation.js";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";

function git(repository: string, args: string[]): void {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
}

describe("turn-tree localization", () => {
  it("extracts ordered turn states from ledger TURN_TREE_SNAPSHOT events", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-states-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "a.txt"), "one\n", "utf8");
      git(repository, ["add", "a.txt"]);
      git(repository, ["commit", "-m", "base"]);
      writeFileSync(join(repository, "a.txt"), "two\n", "utf8");
      const snapshot = captureTurnTreeSnapshot(repository);
      let ledger = createCodexLifecycleLedger({ sessionId: "turn-local-session" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendLifecycleEvent(ledger, {
        type: "TURN_STARTED",
        payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: `sha256:${"a".repeat(64)}` }
      });
      ledger = appendLifecycleEvent(ledger, {
        type: "TURN_COMPLETED",
        payload: { turnId: "turn-1", turnOrdinal: 1, outcome: "COMPLETED" }
      });
      ledger = appendLifecycleEvent(ledger, {
        type: "TURN_TREE_SNAPSHOT",
        payload: { turnId: "turn-1", turnOrdinal: 1, snapshot }
      });
      writeFileSync(join(repository, "a.txt"), "three\n", "utf8");
      const snapshot2 = captureTurnTreeSnapshot(repository);
      ledger = appendLifecycleEvent(ledger, {
        type: "TURN_STARTED",
        payload: { turnId: "turn-2", turnOrdinal: 2, promptDigest: `sha256:${"b".repeat(64)}` }
      });
      ledger = appendLifecycleEvent(ledger, {
        type: "TURN_COMPLETED",
        payload: { turnId: "turn-2", turnOrdinal: 2, outcome: "COMPLETED" }
      });
      ledger = appendLifecycleEvent(ledger, {
        type: "TURN_TREE_SNAPSHOT",
        payload: { turnId: "turn-2", turnOrdinal: 2, snapshot: snapshot2 }
      });
      const states = turnStatesFromLedger(ledger);
      expect(states).toHaveLength(2);
      expect(states[0]?.turnOrdinal).toBe(1);
      expect(states[1]?.turnOrdinal).toBe(2);
      expect(states[0]?.treeDigest).not.toBe(states[1]?.treeDigest);
      expect(states.some((state) => state.dirty)).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
