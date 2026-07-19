#!/usr/bin/env node
/**
 * Generate docs/samples/observed-external-transport/ — honestly labeled
 * non-Codex observed checkpoints (NOT Codex SessionStart/Stop hook events).
 *
 *   node --import tsx scripts/generate-observed-external-transport-sample.mjs
 */
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { digestJson } from "../src/canonical.ts";
import {
  appendLifecycleEvent,
  captureGitCleanCheckpoint,
  createCodexLifecycleLedger,
  verifyCodexLifecycleLedger,
  writeCodexLifecycleLedgerAtomic
} from "../src/ledger.ts";

const __dirname = dirname(fileURLToPath(import.meta.url));
const outDir = resolve(__dirname, "../docs/samples/observed-external-transport");
const REDACTED_ROOT = "/redacted/observed-external-workspace";

function git(repo, args) {
  return execFileSync("git", ["-C", repo, ...args], { encoding: "utf8" }).trim();
}

function resignCheckpoint(checkpoint) {
  const { digest: _digest, ...rest } = checkpoint;
  const unsigned = { ...rest, repositoryRoot: REDACTED_ROOT };
  return { ...unsigned, digest: digestJson(unsigned) };
}

const root = mkdtempSync(join(tmpdir(), "faultline-observed-external-"));
try {
  git(root, ["init"]);
  git(root, ["config", "user.email", "observed@faultline.example"]);
  git(root, ["config", "user.name", "FaultLine Observed"]);

  writeFileSync(join(root, "README.md"), "# observed external sample\n");
  git(root, ["add", "README.md"]);
  git(root, ["commit", "-m", "baseline"]);

  writeFileSync(join(root, "note.txt"), "checkpoint-1\n");
  git(root, ["add", "note.txt"]);
  git(root, ["commit", "-m", "observed stop 1"]);
  const checkpoint1 = captureGitCleanCheckpoint(root);

  writeFileSync(join(root, "note.txt"), "checkpoint-2\n");
  git(root, ["add", "note.txt"]);
  git(root, ["commit", "-m", "observed stop 2"]);
  const checkpoint2 = captureGitCleanCheckpoint(root);

  const t0 = "2026-07-19T14:30:00.000Z";
  let ledger = createCodexLifecycleLedger({
    sessionId: "observed-external-sample-session",
    ledgerId: "ledger-observed-external-sample",
    createdAt: t0
  });

  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "SESSION_STARTED",
      payload: {
        transport: "OBSERVED_EXTERNAL_TRANSPORT",
        workingDirectory: REDACTED_ROOT,
        actor: "submitter@faultline.example"
      }
    },
    { eventId: "observed-session-start", occurredAt: t0 }
  );

  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "TURN_STARTED",
      payload: {
        turnId: "observed-turn-1",
        turnOrdinal: 1,
        promptDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111"
      }
    },
    { eventId: "observed-t1-start", occurredAt: "2026-07-19T14:30:10.000Z" }
  );
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "TURN_COMPLETED",
      payload: {
        turnId: "observed-turn-1",
        turnOrdinal: 1,
        outcome: "COMPLETED",
        outputDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
        contribution: "Honest non-Codex editor checkpoint (not Codex hooks)"
      }
    },
    { eventId: "observed-t1-end", occurredAt: "2026-07-19T14:30:11.000Z" }
  );
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "WORKTREE_CHECKPOINT",
      payload: {
        afterTurnOrdinal: 1,
        checkpoint: resignCheckpoint(checkpoint1)
      }
    },
    { eventId: "observed-t1-checkpoint", occurredAt: "2026-07-19T14:30:12.000Z" }
  );

  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "TURN_STARTED",
      payload: {
        turnId: "observed-turn-2",
        turnOrdinal: 2,
        promptDigest: "sha256:3333333333333333333333333333333333333333333333333333333333333333"
      }
    },
    { eventId: "observed-t2-start", occurredAt: "2026-07-19T14:30:20.000Z" }
  );
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "TURN_COMPLETED",
      payload: {
        turnId: "observed-turn-2",
        turnOrdinal: 2,
        outcome: "COMPLETED",
        outputDigest: "sha256:4444444444444444444444444444444444444444444444444444444444444444",
        contribution: "Second honestly labeled observed stop"
      }
    },
    { eventId: "observed-t2-end", occurredAt: "2026-07-19T14:30:21.000Z" }
  );
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "WORKTREE_CHECKPOINT",
      payload: {
        afterTurnOrdinal: 2,
        checkpoint: resignCheckpoint(checkpoint2)
      }
    },
    { eventId: "observed-t2-checkpoint", occurredAt: "2026-07-19T14:30:22.000Z" }
  );

  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "SESSION_ENDED",
      payload: { reason: "COMPLETED", completedTurns: 2 }
    },
    { eventId: "observed-session-end", occurredAt: "2026-07-19T14:30:30.000Z" }
  );

  const verification = verifyCodexLifecycleLedger(ledger);
  if (!verification.valid) {
    throw new Error(`Generated ledger invalid: ${verification.errors.join("; ")}`);
  }

  mkdirSync(outDir, { recursive: true });
  writeCodexLifecycleLedgerAtomic(join(outDir, "ledger.json"), ledger);
  writeFileSync(
    join(outDir, "sample-meta.json"),
    `${JSON.stringify(
      {
        schemaVersion: "faultline.observed-external-transport-sample.v1",
        transport: "OBSERVED_EXTERNAL_TRANSPORT",
        notCodexHooks: true,
        headHash: ledger.events.at(-1)?.hash,
        eventCount: ledger.events.length,
        generatedAt: new Date().toISOString()
      },
      null,
      2
    )}\n`,
    "utf8"
  );

  process.stdout.write(`Wrote ${outDir} (${ledger.events.length} events, valid=${verification.valid}).\n`);
} finally {
  rmSync(root, { recursive: true, force: true });
}
