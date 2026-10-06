// A deterministic lifecycle ledger that uses every event type, including the
// baseline and turn tree snapshots and tool-use attribution, which the
// committed git bases never record.
import {
  appendLifecycleEvent, createCodexLifecycleLedger, GIT_CHECKPOINT_VERSION, hashLifecycleEvent, ledgerGenesisHash,
  signGitCheckpoint, type CodexLifecycleLedger, type LifecycleEventInput
} from "../../src/ledger.js";
import { signTurnTreeSnapshot, TURN_TREE_SNAPSHOT_VERSION } from "../../src/turn-snapshot.js";

const repositoryRoot = "/work/repository";
const d = (c: string) => `sha256:${c.repeat(64)}`;
const at = (second: number) => new Date(Date.UTC(2026, 6, 16, 10, 0, second)).toISOString();

function snapshot(head: string, tree: string, second: number, dirty: boolean) {
  return signTurnTreeSnapshot({
    schemaVersion: TURN_TREE_SNAPSHOT_VERSION, repositoryRoot, headCommit: head.repeat(40), treeDigest: tree.repeat(40),
    capturedAt: at(second), dirty, statusDigest: d(dirty ? "d" : "e")
  });
}

export function richLedger(): CodexLifecycleLedger {
  let ledger = createCodexLifecycleLedger({ ledgerId: "ledger-rich", sessionId: "session-rich", createdAt: at(0) });
  let second = 0;
  const add = (input: LifecycleEventInput) => {
    second += 1;
    ledger = appendLifecycleEvent(ledger, input, { occurredAt: at(second), eventId: `event-${second}` });
  };
  const turn = (turnId: string, turnOrdinal: number, outcome: "COMPLETED" | "FAILED", head: string, tree: string) => {
    add({ type: "TURN_STARTED", payload: { turnId, turnOrdinal, promptDigest: d("1") } });
    add({ type: "TOOL_USE_STARTED", payload: { turnId, turnOrdinal, toolName: "shell", toolCallId: `${turnId}-call`, allowlistedFieldsDigest: d("2") } });
    add({ type: "TOOL_USE_COMPLETED", payload: { turnId, turnOrdinal, toolName: "shell", toolCallId: `${turnId}-call`, allowlistedFieldsDigest: d("2") } });
    add({ type: "TURN_COMPLETED", payload: { turnId, turnOrdinal, outcome, outputDigest: d("3") } });
    add({ type: "TURN_TREE_SNAPSHOT", payload: { turnId, turnOrdinal, snapshot: snapshot(head, tree, second, outcome === "FAILED") } });
  };
  add({ type: "SESSION_STARTED", payload: { transport: "SIDE_CAR", workingDirectory: repositoryRoot, codexThreadId: "thread-rich", model: "model", actor: "tester" } });
  add({ type: "SESSION_BASELINE_SNAPSHOT", payload: { snapshot: snapshot("a", "b", second, true) } });
  turn("turn-1", 1, "COMPLETED", "a", "c");
  add({
    type: "WORKTREE_CHECKPOINT",
    payload: {
      checkpoint: signGitCheckpoint({
        schemaVersion: GIT_CHECKPOINT_VERSION, repositoryRoot, headCommit: "f".repeat(40), treeDigest: "c".repeat(40), capturedAt: at(second), clean: true
      }),
      afterTurnOrdinal: 1
    }
  });
  turn("turn-2", 2, "FAILED", "f", "9");
  add({ type: "SESSION_ENDED", payload: { reason: "COMPLETED", completedTurns: 2 } });
  return ledger;
}

// resignLedger recomputes the hash chain of a (mutated) ledger, so a
// mutation reaches the semantic checks instead of stopping at a hash error.
// Input it cannot sign is returned unchanged.
export function resignLedger(text: string): string {
  try {
    const ledger = JSON.parse(text) as CodexLifecycleLedger;
    let previous = ledgerGenesisHash(ledger);
    for (const event of ledger.events) {
      event.previousHash = previous;
      const { hash: _hash, ...unsigned } = event;
      event.hash = hashLifecycleEvent(unsigned);
      previous = event.hash;
    }
    return JSON.stringify(ledger);
  } catch {
    return text;
  }
}

