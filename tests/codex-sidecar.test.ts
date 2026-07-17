import { existsSync, readFileSync, readdirSync, rmSync, unlinkSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { spawnSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { sha256 } from "../src/canonical.js";
import {
  codexSidecarLedgerPath,
  inspectObservedCodexSidecar,
  recordObservedCodexHook,
  sidecarEventId
} from "../src/codex-sidecar.js";
import {
  appendLifecycleEvent,
  readVerifiedCodexLifecycleLedger,
  verifyCodexLifecycleLedgerFile,
  writeCodexLifecycleLedgerAtomic
} from "../src/ledger.js";
import { captureTurnTreeSnapshot } from "../src/turn-snapshot.js";

function git(repository: string, args: string[]): void {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
}

function repositoryFixture(): string {
  const repository = mkdtempSync(join(tmpdir(), "faultline-codex-sidecar-"));
  git(repository, ["init"]);
  git(repository, ["config", "user.email", "faultline@example.invalid"]);
  git(repository, ["config", "user.name", "FaultLine sidecar test"]);
  writeFileSync(join(repository, "tracked.txt"), "clean\n", "utf8");
  git(repository, ["add", "tracked.txt"]);
  git(repository, ["commit", "-m", "clean fixture"]);
  return repository;
}

function sessionStart(repository: string, sessionId = "codex-session-1") {
  return {
    hook_event_name: "SessionStart" as const,
    session_id: sessionId,
    cwd: repository,
    model: "gpt-5.6",
    // Real hook envelopes may also supply this. The sidecar must discard it.
    transcript_path: "C:/private/transcript.jsonl"
  };
}

function promptEvent(repository: string, prompt: string, sessionId = "codex-session-1", turnId = "codex-turn-1") {
  return {
    hook_event_name: "UserPromptSubmit" as const,
    session_id: sessionId,
    cwd: repository,
    model: "gpt-5.6",
    turn_id: turnId,
    prompt,
    transcript_path: "C:/private/transcript.jsonl"
  };
}

function stopEvent(repository: string, sessionId = "codex-session-1", turnId = "codex-turn-1") {
  return {
    hook_event_name: "Stop" as const,
    session_id: sessionId,
    cwd: repository,
    model: "gpt-5.6",
    turn_id: turnId,
    // The adapter schema intentionally ignores this potentially sensitive
    // official hook field rather than persisting it.
    last_assistant_message: "private assistant output must never be retained"
  };
}

function quoteFsmonitorCommandPart(value: string): string {
  return `"${value.replaceAll("\\", "/").replaceAll('"', '\\"')}"`;
}

describe("Codex observed hook sidecar", () => {
  it("records SESSION_BASELINE_SNAPSHOT at SessionStart even when the worktree is already dirty", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, "preexisting-dirty.txt"), "dirty-before-session\n", "utf8");
      const started = recordObservedCodexHook(sessionStart(repository, "codex-session-baseline-dirty"));
      expect(started).toMatchObject({ status: "SESSION_STARTED", idempotent: false });
      const ledger = readVerifiedCodexLifecycleLedger(started.ledgerPath);
      expect(ledger.events.map((event) => event.event.type)).toEqual([
        "SESSION_STARTED",
        "SESSION_BASELINE_SNAPSHOT"
      ]);
      expect(ledger.events[1]?.event).toMatchObject({
        type: "SESSION_BASELINE_SNAPSHOT",
        payload: { snapshot: { dirty: true } }
      });
      expect(ledger.events[1]?.eventId).toBe(sidecarEventId("codex-session-baseline-dirty", null, "session-baseline"));
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("records public lifecycle facts, a clean Git checkpoint, and no prompt/output/transcript text", () => {
    const repository = repositoryFixture();
    const prompt = "Fix the issue with secret marker SIDE_CAR_PROMPT_DO_NOT_PERSIST";
    try {
      const started = recordObservedCodexHook(sessionStart(repository));
      const turn = recordObservedCodexHook(promptEvent(repository, prompt));
      const stopped = recordObservedCodexHook(stopEvent(repository));

      expect(started).toMatchObject({ status: "SESSION_STARTED", idempotent: false, sessionId: "codex-session-1" });
      expect(turn).toMatchObject({ status: "TURN_STARTED", idempotent: false, turnId: "codex-turn-1" });
      expect(stopped).toMatchObject({ status: "CHECKPOINT_RECORDED", idempotent: false, turnId: "codex-turn-1" });
      expect(stopped.checkpointDigest).toMatch(/^sha256:[a-f0-9]{64}$/);
      expect(stopped.turnSnapshot).toMatchObject({ dirty: false, headCommit: expect.stringMatching(/^[a-f0-9]{40}$/) });
      expect(stopped.turnSnapshot?.digest).toMatch(/^sha256:[a-f0-9]{64}$/);

      const ledgerPath = codexSidecarLedgerPath(repository, "codex-session-1");
      expect(ledgerPath).toBe(started.ledgerPath);
      expect(existsSync(ledgerPath)).toBe(true);
      expect(verifyCodexLifecycleLedgerFile(ledgerPath)).toMatchObject({ valid: true, eventCount: 6 });
      const ledger = readVerifiedCodexLifecycleLedger(ledgerPath);
      expect(ledger.sessionId).toBe("codex-session-1");
      expect(ledger.events.map((event) => event.event.type)).toEqual([
        "SESSION_STARTED",
        "SESSION_BASELINE_SNAPSHOT",
        "TURN_STARTED",
        "TURN_COMPLETED",
        "TURN_TREE_SNAPSHOT",
        "WORKTREE_CHECKPOINT"
      ]);
      expect(ledger.events[0]?.event).toMatchObject({
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository, model: "gpt-5.6" }
      });
      expect(ledger.events[1]?.event).toMatchObject({
        type: "SESSION_BASELINE_SNAPSHOT",
        payload: { snapshot: { dirty: false } }
      });
      expect(ledger.events[2]?.event).toMatchObject({
        type: "TURN_STARTED",
        payload: { turnId: "codex-turn-1", turnOrdinal: 1, promptDigest: `sha256:${sha256(prompt)}` }
      });
      expect(ledger.events[4]?.event).toMatchObject({
        type: "TURN_TREE_SNAPSHOT",
        payload: { turnId: "codex-turn-1", turnOrdinal: 1, snapshot: { dirty: false } }
      });
      expect(ledger.events[5]?.event).toMatchObject({
        type: "WORKTREE_CHECKPOINT",
        payload: { afterTurnOrdinal: 1, checkpoint: { clean: true } }
      });

      const stored = readFileSync(ledgerPath, "utf8");
      expect(stored).not.toContain(prompt);
      expect(stored).not.toContain("private assistant output must never be retained");
      expect(stored).not.toContain("C:/private/transcript.jsonl");
      // Operational artifacts live in Git metadata, not a worktree path or a
      // broad ignore rule that could hide behavior-affecting local files.
      expect(existsSync(join(repository, ".faultline"))).toBe(false);
      expect(spawnSync("git", ["-C", repository, "status", "--porcelain=v1", "--untracked-files=all"], { encoding: "utf8" }).stdout).toBe("");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("is idempotent for repeated SessionStart, UserPromptSubmit, and Stop hook deliveries", () => {
    const repository = repositoryFixture();
    const prompt = "Repeat the lifecycle event without duplicate records.";
    try {
      const initialStart = recordObservedCodexHook(sessionStart(repository));
      const initialTurn = recordObservedCodexHook(promptEvent(repository, prompt));
      const initialStop = recordObservedCodexHook(stopEvent(repository));
      const repeatedStart = recordObservedCodexHook(sessionStart(repository));
      const repeatedTurn = recordObservedCodexHook(promptEvent(repository, prompt));
      const repeatedStop = recordObservedCodexHook(stopEvent(repository));

      expect(initialStart.idempotent).toBe(false);
      expect(initialTurn.idempotent).toBe(false);
      expect(initialStop).toMatchObject({ status: "CHECKPOINT_RECORDED", idempotent: false });
      expect(repeatedStart).toMatchObject({ status: "SESSION_STARTED", idempotent: true });
      expect(repeatedTurn).toMatchObject({ status: "TURN_STARTED", idempotent: true });
      expect(repeatedStop).toMatchObject({
        status: "CHECKPOINT_RECORDED",
        idempotent: true,
        checkpointDigest: initialStop.checkpointDigest,
        turnSnapshot: { digest: initialStop.turnSnapshot?.digest }
      });
      expect(readVerifiedCodexLifecycleLedger(initialStart.ledgerPath).events).toHaveLength(6);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("records the completed turn but explicitly skips a dirty worktree checkpoint", () => {
    const repository = repositoryFixture();
    try {
      const started = recordObservedCodexHook(sessionStart(repository, "codex-session-dirty"));
      recordObservedCodexHook(promptEvent(repository, "Do work that leaves a dirty tree.", "codex-session-dirty", "codex-turn-dirty"));
      writeFileSync(join(repository, "untracked-by-agent.txt"), "dirty\n", "utf8");

      const skipped = recordObservedCodexHook(stopEvent(repository, "codex-session-dirty", "codex-turn-dirty"));
      const repeated = recordObservedCodexHook(stopEvent(repository, "codex-session-dirty", "codex-turn-dirty"));
      expect(skipped).toMatchObject({
        status: "CHECKPOINT_SKIPPED_DIRTY",
        reason: "DIRTY_WORKTREE",
        idempotent: false
      });
      // A dirty worktree still yields a real tree digest: the turn tree
      // snapshot never requires a clean checkout, unlike WORKTREE_CHECKPOINT.
      expect(skipped.turnSnapshot).toMatchObject({ dirty: true });
      expect(skipped.turnSnapshot?.treeDigest).toMatch(/^[a-f0-9]{40}$/);
      expect(repeated).toMatchObject({
        status: "CHECKPOINT_SKIPPED_DIRTY",
        reason: "DIRTY_WORKTREE",
        idempotent: true,
        turnSnapshot: { digest: skipped.turnSnapshot?.digest }
      });
      const ledger = readVerifiedCodexLifecycleLedger(started.ledgerPath);
      expect(ledger.events.map((event) => event.event.type)).toEqual([
        "SESSION_STARTED",
        "SESSION_BASELINE_SNAPSHOT",
        "TURN_STARTED",
        "TURN_COMPLETED",
        "TURN_TREE_SNAPSHOT"
      ]);
      expect(ledger.events[4]?.event).toMatchObject({
        type: "TURN_TREE_SNAPSHOT",
        payload: { turnId: "codex-turn-dirty", turnOrdinal: 1, snapshot: { dirty: true } }
      });
      expect(verifyCodexLifecycleLedgerFile(started.ledgerPath)).toMatchObject({ valid: true, eventCount: 5 });
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("uses hardened Git probes so a repository fsmonitor command is never dispatched", () => {
    const repository = repositoryFixture();
    try {
      const hook = join(repository, "forbidden-fsmonitor.cjs");
      const marker = join(repository, "fsmonitor-ran");
      writeFileSync(hook, 'require("node:fs").writeFileSync(process.argv[2], "invoked", "utf8");\n', "utf8");
      git(repository, ["add", "forbidden-fsmonitor.cjs"]);
      git(repository, ["commit", "-m", "add hostile fsmonitor fixture"]);
      const hostileHook = [
        quoteFsmonitorCommandPart(process.execPath),
        quoteFsmonitorCommandPart(hook),
        quoteFsmonitorCommandPart(marker)
      ].join(" ");
      git(repository, ["config", "core.fsmonitor", hostileHook]);

      recordObservedCodexHook(sessionStart(repository, "codex-session-safe-git"));
      recordObservedCodexHook(promptEvent(repository, "Capture safely.", "codex-session-safe-git", "codex-turn-safe-git"));
      expect(recordObservedCodexHook(stopEvent(repository, "codex-session-safe-git", "codex-turn-safe-git")))
        .toMatchObject({ status: "CHECKPOINT_RECORDED" });
      expect(existsSync(marker)).toBe(false);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("recovers a missing stop receipt from an existing checkpoint instead of capturing a second timestamped state", () => {
    const repository = repositoryFixture();
    try {
      recordObservedCodexHook(sessionStart(repository, "codex-session-recover"));
      recordObservedCodexHook(promptEvent(repository, "Record one checkpoint.", "codex-session-recover", "codex-turn-recover"));
      const initial = recordObservedCodexHook(stopEvent(repository, "codex-session-recover", "codex-turn-recover"));
      expect(initial).toMatchObject({ status: "CHECKPOINT_RECORDED", idempotent: false });
      const ledgerPath = codexSidecarLedgerPath(repository, "codex-session-recover");
      const receipt = readdirSync(dirname(ledgerPath)).find((name) => /^codex-stop-[a-f0-9]{64}\.json$/.test(name));
      expect(receipt).toBeDefined();
      unlinkSync(join(dirname(ledgerPath), receipt as string));

      const recovered = recordObservedCodexHook(stopEvent(repository, "codex-session-recover", "codex-turn-recover"));
      expect(recovered).toMatchObject({
        status: "CHECKPOINT_RECORDED",
        idempotent: true,
        checkpointDigest: initial.checkpointDigest,
        turnSnapshot: { digest: initial.turnSnapshot?.digest }
      });
      expect(readVerifiedCodexLifecycleLedger(ledgerPath).events).toHaveLength(6);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("fails closed after an interrupted completed-turn transaction rather than attaching a later checkpoint", () => {
    const repository = repositoryFixture();
    try {
      recordObservedCodexHook(sessionStart(repository, "codex-session-interrupted"));
      recordObservedCodexHook(promptEvent(repository, "Prepare an interrupted stop.", "codex-session-interrupted", "codex-turn-interrupted"));
      const ledgerPath = codexSidecarLedgerPath(repository, "codex-session-interrupted");
      const ledger = readVerifiedCodexLifecycleLedger(ledgerPath);
      const interrupted = appendLifecycleEvent(ledger, {
        type: "TURN_COMPLETED",
        payload: { turnId: "codex-turn-interrupted", turnOrdinal: 1, outcome: "COMPLETED" }
      }, { eventId: "simulated-interrupted-stop" });
      writeCodexLifecycleLedgerAtomic(ledgerPath, interrupted);

      const recovered = recordObservedCodexHook(stopEvent(repository, "codex-session-interrupted", "codex-turn-interrupted"));
      expect(recovered).toMatchObject({
        status: "CHECKPOINT_SKIPPED_UNAVAILABLE",
        reason: "CHECKPOINT_UNAVAILABLE",
        idempotent: true
      });
      expect(readVerifiedCodexLifecycleLedger(ledgerPath).events).toHaveLength(4);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("recovers a durable turn tree snapshot when the checkpoint decision itself was interrupted", () => {
    const repository = repositoryFixture();
    try {
      recordObservedCodexHook(sessionStart(repository, "codex-session-snapshot-only"));
      recordObservedCodexHook(promptEvent(repository, "Prepare a snapshot-only interrupted stop.", "codex-session-snapshot-only", "codex-turn-snapshot-only"));
      const ledgerPath = codexSidecarLedgerPath(repository, "codex-session-snapshot-only");
      const snapshot = captureTurnTreeSnapshot(repository);

      let ledger = readVerifiedCodexLifecycleLedger(ledgerPath);
      ledger = appendLifecycleEvent(ledger, {
        type: "TURN_COMPLETED",
        payload: { turnId: "codex-turn-snapshot-only", turnOrdinal: 1, outcome: "COMPLETED" }
      }, { eventId: sidecarEventId("codex-session-snapshot-only", "codex-turn-snapshot-only", "turn-stop") });
      ledger = appendLifecycleEvent(ledger, {
        type: "TURN_TREE_SNAPSHOT",
        payload: { turnId: "codex-turn-snapshot-only", turnOrdinal: 1, snapshot }
      }, { eventId: sidecarEventId("codex-session-snapshot-only", "codex-turn-snapshot-only", "turn-snapshot") });
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const recovered = recordObservedCodexHook(stopEvent(repository, "codex-session-snapshot-only", "codex-turn-snapshot-only"));
      expect(recovered).toMatchObject({
        status: "TURN_SNAPSHOT_RECORDED",
        idempotent: true,
        turnSnapshot: { digest: snapshot.digest }
      });
      expect(recovered.checkpointDigest).toBeUndefined();
      // Recovery must not recapture: the ledger keeps exactly the two
      // manually-simulated events, with no additional WORKTREE_CHECKPOINT.
      expect(readVerifiedCodexLifecycleLedger(ledgerPath).events.map((event) => event.event.type)).toEqual([
        "SESSION_STARTED",
        "SESSION_BASELINE_SNAPSHOT",
        "TURN_STARTED",
        "TURN_COMPLETED",
        "TURN_TREE_SNAPSHOT"
      ]);

      const repeated = recordObservedCodexHook(stopEvent(repository, "codex-session-snapshot-only", "codex-turn-snapshot-only"));
      expect(repeated).toMatchObject({
        status: "TURN_SNAPSHOT_RECORDED",
        idempotent: true,
        turnSnapshot: { digest: snapshot.digest }
      });
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("recovers a stale sidecar lock and reports recorder health without prompt material", () => {
    const repository = repositoryFixture();
    try {
      const started = recordObservedCodexHook(sessionStart(repository, "codex-session-status"));
      const lock = `${started.ledgerPath}.sidecar.lock`;
      writeFileSync(lock, "interrupted\n", "utf8");
      expect(inspectObservedCodexSidecar(repository, "codex-session-status").recordings[0])
        .toMatchObject({ lock: { state: "ACTIVE" } });
      const stale = new Date(Date.now() - 60_000);
      utimesSync(lock, stale, stale);

      expect(recordObservedCodexHook(promptEvent(repository, "Never surface this prompt.", "codex-session-status", "codex-turn-status")))
        .toMatchObject({ status: "TURN_STARTED" });
      const status = inspectObservedCodexSidecar(repository, "codex-session-status");
      expect(status.recordings).toHaveLength(1);
      expect(status.recordings[0]).toMatchObject({
        valid: true,
        sessionId: "codex-session-status",
        errors: []
      });
      expect(status.recordings[0]).not.toHaveProperty("latestStop");
      expect(JSON.stringify(status)).not.toContain("Never surface this prompt.");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("marks a schema-valid but contradictory stop receipt invalid during status inspection", () => {
    const repository = repositoryFixture();
    try {
      recordObservedCodexHook(sessionStart(repository, "codex-session-receipt-integrity"));
      recordObservedCodexHook(promptEvent(repository, "Record a checkpoint without storing this prompt.", "codex-session-receipt-integrity", "codex-turn-receipt-integrity"));
      expect(recordObservedCodexHook(stopEvent(repository, "codex-session-receipt-integrity", "codex-turn-receipt-integrity")))
        .toMatchObject({ status: "CHECKPOINT_RECORDED" });

      const ledgerPath = codexSidecarLedgerPath(repository, "codex-session-receipt-integrity");
      const receiptName = readdirSync(dirname(ledgerPath)).find((name) => /^codex-stop-[a-f0-9]{64}\.json$/.test(name));
      expect(receiptName).toBeDefined();
      const receiptPath = join(dirname(ledgerPath), receiptName as string);
      const receipt = JSON.parse(readFileSync(receiptPath, "utf8")) as Record<string, unknown>;
      receipt.checkpointDigest = `sha256:${"f".repeat(64)}`;
      writeFileSync(receiptPath, `${JSON.stringify(receipt)}\n`, "utf8");

      const status = inspectObservedCodexSidecar(repository, "codex-session-receipt-integrity");
      expect(status.recordings).toHaveLength(1);
      expect(status.recordings[0]).toMatchObject({ valid: false });
      expect(status.recordings[0]?.errors.join("\n")).toContain("checkpoint digest does not match the ledger");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });
});
