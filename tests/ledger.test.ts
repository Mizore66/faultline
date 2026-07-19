import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import {
  appendLifecycleEvent,
  appendLifecycleEventAtomic,
  captureGitCleanCheckpoint,
  createCodexLifecycleLedger,
  hashLifecycleEvent,
  ledgerGenesisHash,
  readVerifiedCodexLifecycleLedger,
  signGitCheckpoint,
  verifyCodexLifecycleLedger,
  verifyCodexLifecycleLedgerFile,
  verifyGitCheckpoint,
  writeCodexLifecycleLedgerAtomic,
  type CodexLifecycleLedger,
  type CodexLifecycleEvent
} from "../src/ledger.js";

const createdAt = "2026-07-16T00:00:00.000Z";
const times = [
  "2026-07-16T00:00:01.000Z",
  "2026-07-16T00:00:02.000Z",
  "2026-07-16T00:00:03.000Z",
  "2026-07-16T00:00:04.000Z",
  "2026-07-16T00:00:05.000Z"
];

function checkpoint(capturedAt = "2026-07-16T00:00:03.500Z") {
  return signGitCheckpoint({
    schemaVersion: "faultline.git-checkpoint.v1",
    repositoryRoot: "C:/work/faultline",
    headCommit: "a".repeat(40),
    treeDigest: "b".repeat(40),
    capturedAt,
    clean: true
  });
}

function appendAt(
  ledger: CodexLifecycleLedger,
  input: Parameters<typeof appendLifecycleEvent>[1],
  index: number
): CodexLifecycleLedger {
  return appendLifecycleEvent(ledger, input, { eventId: `event-${index + 1}`, occurredAt: times[index] });
}

function validLedger(): CodexLifecycleLedger {
  let ledger = createCodexLifecycleLedger({ ledgerId: "ledger-demo", sessionId: "session-demo", createdAt });
  ledger = appendAt(ledger, { type: "SESSION_STARTED", payload: { transport: "CODEX_CLI", workingDirectory: "C:/work/faultline", codexThreadId: "thread-demo", model: "gpt-5.6" } }, 0);
  ledger = appendAt(ledger, { type: "TURN_STARTED", payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: "c".repeat(64).replace(/^/, "sha256:") } }, 1);
  ledger = appendAt(ledger, { type: "TURN_COMPLETED", payload: { turnId: "turn-1", turnOrdinal: 1, outcome: "COMPLETED", outputDigest: "d".repeat(64).replace(/^/, "sha256:") } }, 2);
  ledger = appendAt(ledger, { type: "WORKTREE_CHECKPOINT", payload: { checkpoint: checkpoint(), afterTurnOrdinal: 1 } }, 3);
  ledger = appendAt(ledger, { type: "SESSION_ENDED", payload: { reason: "COMPLETED", completedTurns: 1 } }, 4);
  return ledger;
}

function validObservedExternalLedger(): CodexLifecycleLedger {
  let ledger = createCodexLifecycleLedger({ ledgerId: "ledger-observed", sessionId: "session-observed", createdAt });
  ledger = appendAt(ledger, {
    type: "SESSION_STARTED",
    payload: {
      transport: "OBSERVED_EXTERNAL_TRANSPORT",
      workingDirectory: "C:/work/faultline",
      actor: "reviewer@example.test"
    }
  }, 0);
  ledger = appendAt(ledger, { type: "TURN_STARTED", payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: "c".repeat(64).replace(/^/, "sha256:") } }, 1);
  ledger = appendAt(ledger, {
    type: "TURN_COMPLETED",
    payload: {
      turnId: "turn-1",
      turnOrdinal: 1,
      outcome: "COMPLETED",
      outputDigest: "d".repeat(64).replace(/^/, "sha256:"),
      contribution: "non-Codex editor checkpoint"
    }
  }, 2);
  ledger = appendAt(ledger, { type: "WORKTREE_CHECKPOINT", payload: { checkpoint: checkpoint(), afterTurnOrdinal: 1 } }, 3);
  ledger = appendAt(ledger, { type: "SESSION_ENDED", payload: { reason: "COMPLETED", completedTurns: 1 } }, 4);
  return ledger;
}

function resign(events: CodexLifecycleEvent[], ledger: CodexLifecycleLedger): CodexLifecycleEvent[] {
  let previousHash = ledgerGenesisHash(ledger);
  return events.map((event) => {
    const unsigned = {
      schemaVersion: event.schemaVersion,
      ledgerId: event.ledgerId,
      sessionId: event.sessionId,
      sequence: event.sequence,
      eventId: event.eventId,
      occurredAt: event.occurredAt,
      event: event.event,
      previousHash
    };
    const signed = { ...unsigned, hash: hashLifecycleEvent(unsigned) };
    previousHash = signed.hash;
    return signed;
  });
}

function git(repository: string, args: string[]): void {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
}

describe("Codex lifecycle ledger", () => {
  it("creates an immutable signed lifecycle, writes atomically, and verifies it after a read", () => {
    const ledger = validLedger();
    expect(verifyCodexLifecycleLedger(ledger)).toMatchObject({ valid: true, eventCount: 5 });
    expect(ledger.events).toHaveLength(5);
    expect(ledger.events[0]?.previousHash).toBe(ledgerGenesisHash(ledger));

    const directory = mkdtempSync(join(tmpdir(), "faultline-ledger-"));
    try {
      const file = join(directory, "session.json");
      const written = writeCodexLifecycleLedgerAtomic(file, ledger);
      expect(written).toBe(file);
      expect(existsSync(file)).toBe(true);
      expect(readVerifiedCodexLifecycleLedger(file)).toEqual(ledger);
      expect(verifyCodexLifecycleLedgerFile(file)).toMatchObject({ valid: true, eventCount: 5 });
      expect(readFileSync(file, "utf8")).toContain("SESSION_STARTED");
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("accepts OBSERVED_EXTERNAL_TRANSPORT as an honest non-Codex transport label", () => {
    const ledger = validObservedExternalLedger();
    expect(verifyCodexLifecycleLedger(ledger)).toMatchObject({ valid: true, eventCount: 5 });
    expect(ledger.events[0]?.event).toMatchObject({
      type: "SESSION_STARTED",
      payload: { transport: "OBSERVED_EXTERNAL_TRANSPORT" }
    });
  });

  it("detects payload tampering even when the JSON remains schema-valid", () => {
    const ledger = structuredClone(validLedger());
    const event = ledger.events[1];
    if (!event || event.event.type !== "TURN_STARTED") throw new Error("test fixture missing turn start");
    event.event.payload.promptDigest = `sha256:${"e".repeat(64)}`;
    const verification = verifyCodexLifecycleLedger(ledger);
    expect(verification.valid).toBe(false);
    expect(verification.errors).toContain("Event 2 hash does not match its contents");
  });

  it("detects reordered records rather than treating an array as a trustworthy timeline", () => {
    const ledger = structuredClone(validLedger());
    const first = ledger.events[0];
    const second = ledger.events[1];
    if (!first || !second) throw new Error("test fixture missing events");
    ledger.events[0] = second;
    ledger.events[1] = first;
    const verification = verifyCodexLifecycleLedger(ledger);
    expect(verification.valid).toBe(false);
    expect(verification.errors.some((error) => error.includes("sequence") || error.includes("previous hash"))).toBe(true);
  });

  it("rejects duplicate sequence numbers even if an attacker recomputes a valid hash chain", () => {
    const ledger = structuredClone(validLedger());
    const duplicate = ledger.events[1];
    if (!duplicate) throw new Error("test fixture missing second event");
    duplicate.sequence = 1;
    ledger.events = resign(ledger.events, ledger);
    const verification = verifyCodexLifecycleLedger(ledger);
    expect(verification.valid).toBe(false);
    expect(verification.errors).toContain("Event at index 1 has sequence 1; expected 2");
  });

  it("rejects a session mismatch even if the event chain is recomputed", () => {
    const ledger = structuredClone(validLedger());
    const second = ledger.events[1];
    if (!second) throw new Error("test fixture missing second event");
    second.sessionId = "session-forged";
    ledger.events = resign(ledger.events, ledger);
    const verification = verifyCodexLifecycleLedger(ledger);
    expect(verification.valid).toBe(false);
    expect(verification.errors).toContain("Event 2 has session id session-forged; expected session-demo");
  });

  it("enforces lifecycle order before signing a new event", () => {
    const ledger = createCodexLifecycleLedger({ ledgerId: "ledger-order", sessionId: "session-order", createdAt });
    expect(() => appendAt(ledger, { type: "TURN_STARTED", payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: `sha256:${"a".repeat(64)}` } }, 0)).toThrow(/before SESSION_STARTED/);

    const started = appendAt(ledger, { type: "SESSION_STARTED", payload: { transport: "SIDE_CAR", workingDirectory: "C:/work/faultline" } }, 0);
    const active = appendAt(started, { type: "TURN_STARTED", payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: `sha256:${"a".repeat(64)}` } }, 1);
    expect(() => appendAt(active, { type: "SESSION_ENDED", payload: { reason: "ABANDONED", completedTurns: 0 } }, 2)).toThrow(/ends while turn turn-1 is active/);
  });

  it("captures and signs a real clean temporary Git worktree checkpoint", () => {
    const repository = mkdtempSync(join(tmpdir(), "faultline-ledger-git-"));
    try {
      git(repository, ["init"]);
      git(repository, ["config", "user.email", "faultline@example.invalid"]);
      git(repository, ["config", "user.name", "FaultLine test"]);
      writeFileSync(join(repository, "tracked.txt"), "stable\n", "utf8");
      git(repository, ["add", "tracked.txt"]);
      git(repository, ["commit", "-m", "checkpoint fixture"]);

      const capturedAt = "2026-07-16T00:00:01.500Z";
      const actualCheckpoint = captureGitCleanCheckpoint(repository, { now: () => new Date(capturedAt) });
      expect(actualCheckpoint.clean).toBe(true);
      expect(actualCheckpoint.headCommit).toMatch(/^[a-f0-9]{40}$/);
      expect(actualCheckpoint.treeDigest).toMatch(/^[a-f0-9]{40}$/);
      expect(verifyGitCheckpoint(actualCheckpoint)).toEqual([]);

      let ledger = createCodexLifecycleLedger({ ledgerId: "ledger-git", sessionId: "session-git", createdAt });
      ledger = appendAt(ledger, { type: "SESSION_STARTED", payload: { transport: "CODEX_CLI", workingDirectory: repository } }, 0);
      ledger = appendAt(ledger, { type: "WORKTREE_CHECKPOINT", payload: { checkpoint: actualCheckpoint, afterTurnOrdinal: 0 } }, 1);
      expect(verifyCodexLifecycleLedger(ledger).valid).toBe(true);

      writeFileSync(join(repository, "untracked.txt"), "dirty\n", "utf8");
      expect(() => captureGitCleanCheckpoint(repository)).toThrow(/clean worktree/);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("serializes atomic appends so the persisted sequence remains contiguous", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-ledger-append-"));
    try {
      const file = join(directory, "session.json");
      const ledger = createCodexLifecycleLedger({ ledgerId: "ledger-append", sessionId: "session-append", createdAt });
      writeCodexLifecycleLedgerAtomic(file, ledger);
      appendLifecycleEventAtomic(file, { type: "SESSION_STARTED", payload: { transport: "SIDE_CAR", workingDirectory: "C:/work/faultline" } }, { eventId: "event-1", occurredAt: times[0] });
      appendLifecycleEventAtomic(file, { type: "TURN_STARTED", payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: `sha256:${"a".repeat(64)}` } }, { eventId: "event-2", occurredAt: times[1] });
      const persisted = readVerifiedCodexLifecycleLedger(file);
      expect(persisted.events.map((event) => event.sequence)).toEqual([1, 2]);
      expect(existsSync(`${file}.lock`)).toBe(false);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});
