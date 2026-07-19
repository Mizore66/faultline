#!/usr/bin/env node
/**
 * Generate docs/samples/cursor-observed-transport/ — Cursor-port testimony
 * recorded as OBSERVED_EXTERNAL_TRANSPORT (NOT Codex SIDE_CAR / SessionStart hooks).
 *
 * Cursor agent turns are similar in shape to Codex stops (prompt → edit → stop),
 * but FaultLine must not mint SIDE_CAR ledgers for them. This sample is the
 * honest Path B when dogfooding FaultLine from Cursor.
 *
 *   node --import tsx scripts/generate-cursor-observed-transport-sample.mjs
 */
import { createHash } from "node:crypto";
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
const outDir = resolve(__dirname, "../docs/samples/cursor-observed-transport");
const REDACTED_ROOT = "/redacted/cursor-observed-workspace";

function git(repo, args) {
  return execFileSync("git", ["-C", repo, ...args], { encoding: "utf8" }).trim();
}

function resignCheckpoint(checkpoint, capturedAt) {
  const { digest: _digest, ...rest } = checkpoint;
  const unsigned = { ...rest, repositoryRoot: REDACTED_ROOT, capturedAt };
  return { ...unsigned, digest: digestJson(unsigned) };
}

function digestText(value) {
  return `sha256:${createHash("sha256").update(value, "utf8").digest("hex")}`;
}

const root = mkdtempSync(join(tmpdir(), "faultline-cursor-observed-"));
try {
  git(root, ["init"]);
  git(root, ["config", "user.email", "cursor-observed@faultline.example"]);
  git(root, ["config", "user.name", "FaultLine Cursor Observed"]);

  writeFileSync(join(root, "README.md"), "# cursor observed sample\n");
  git(root, ["add", "README.md"]);
  git(root, ["commit", "-m", "baseline"]);

  writeFileSync(join(root, "latency-note.txt"), "cli-entrypoint-warm\n");
  git(root, ["add", "latency-note.txt"]);
  git(root, ["commit", "-m", "cursor stop 1: compile entry"]);
  const checkpoint1 = captureGitCleanCheckpoint(root);

  writeFileSync(join(root, "latency-note.txt"), "cli-entrypoint-warm\ninit-prewarm\n");
  git(root, ["add", "latency-note.txt"]);
  git(root, ["commit", "-m", "cursor stop 2: init prewarm"]);
  const checkpoint2 = captureGitCleanCheckpoint(root);

  const t0 = "2026-07-19T16:20:00.000Z";
  let ledger = createCodexLifecycleLedger({
    sessionId: "cursor-observed-sample-session",
    ledgerId: "ledger-cursor-observed-sample",
    createdAt: t0
  });

  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "SESSION_STARTED",
      payload: {
        transport: "OBSERVED_EXTERNAL_TRANSPORT",
        workingDirectory: REDACTED_ROOT,
        actor: "cursor-agent@faultline.example"
      }
    },
    { eventId: "cursor-session-start", occurredAt: t0 }
  );

  const prompt1 = digestText("Cursor turn: ship node dist/cli.js entry + thin help path");
  const output1 = digestText("Applied package.json fl→node dist/cli.js; cli-help fast path");
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "TURN_STARTED",
      payload: {
        turnId: "cursor-turn-1",
        turnOrdinal: 1,
        promptDigest: prompt1
      }
    },
    { eventId: "cursor-t1-start", occurredAt: "2026-07-19T16:20:10.000Z" }
  );
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "TURN_COMPLETED",
      payload: {
        turnId: "cursor-turn-1",
        turnOrdinal: 1,
        outcome: "COMPLETED",
        outputDigest: output1,
        contribution: "Cursor-observed stop (OBSERVED_EXTERNAL_TRANSPORT; not Codex SIDE_CAR hooks)"
      }
    },
    { eventId: "cursor-t1-end", occurredAt: "2026-07-19T16:20:11.000Z" }
  );
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "WORKTREE_CHECKPOINT",
      payload: {
        afterTurnOrdinal: 1,
        checkpoint: resignCheckpoint(checkpoint1, "2026-07-19T16:20:11.500Z")
      }
    },
    { eventId: "cursor-t1-checkpoint", occurredAt: "2026-07-19T16:20:12.000Z" }
  );

  const prompt2 = digestText("Cursor turn: fl init snapshot pre-warm + CI pin/test fixes");
  const output2 = digestText("Repo cache prewarm + v0.1.1 quickstart pin + Windows inode guard");
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "TURN_STARTED",
      payload: {
        turnId: "cursor-turn-2",
        turnOrdinal: 2,
        promptDigest: prompt2
      }
    },
    { eventId: "cursor-t2-start", occurredAt: "2026-07-19T16:21:00.000Z" }
  );
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "TURN_COMPLETED",
      payload: {
        turnId: "cursor-turn-2",
        turnOrdinal: 2,
        outcome: "COMPLETED",
        outputDigest: output2,
        contribution: "Second Cursor-observed stop; still not Codex hook provenance"
      }
    },
    { eventId: "cursor-t2-end", occurredAt: "2026-07-19T16:21:01.000Z" }
  );
  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "WORKTREE_CHECKPOINT",
      payload: {
        afterTurnOrdinal: 2,
        checkpoint: resignCheckpoint(checkpoint2, "2026-07-19T16:21:01.500Z")
      }
    },
    { eventId: "cursor-t2-checkpoint", occurredAt: "2026-07-19T16:21:02.000Z" }
  );

  ledger = appendLifecycleEvent(
    ledger,
    {
      type: "SESSION_ENDED",
      payload: { reason: "COMPLETED", completedTurns: 2 }
    },
    { eventId: "cursor-session-end", occurredAt: "2026-07-19T16:21:30.000Z" }
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
        schemaVersion: "faultline.cursor-observed-transport-sample.v1",
        transport: "OBSERVED_EXTERNAL_TRANSPORT",
        editor: "cursor",
        notCodexHooks: true,
        notSideCar: true,
        claimBoundary:
          "Cursor agent turns observed via fl record / sample generator. Transport remains OBSERVED_EXTERNAL_TRANSPORT. Do not cite as Codex SIDE_CAR.",
        headHash: ledger.events.at(-1)?.hash,
        eventCount: ledger.events.length,
        generatedAt: new Date().toISOString()
      },
      null,
      2
    )}\n`,
    "utf8"
  );

  writeFileSync(
    join(outDir, "README.md"),
    `# Cursor-observed transport sample (honest non-Codex)

This directory is **Cursor-port testimony**: lifecycle checkpoints shaped like
agent stops, recorded under \`OBSERVED_EXTERNAL_TRANSPORT\`.

It is **not** a Codex \`SIDE_CAR\` / \`SessionStart\` / \`Stop\` hook capture.
Do not rebadge this ledger as Codex nativity.

## Provenance

- Generator: \`scripts/generate-cursor-observed-transport-sample.mjs\`
- Transport: \`OBSERVED_EXTERNAL_TRANSPORT\`
- Editor context: Cursor (agent turns → digest-only prompt/output + clean Git checkpoints)
- Checkpoints: real clean worktree captures, redacted to \`${REDACTED_ROOT}\`

## Why this exists

Codex hook capture and Cursor agent sessions look similar operationally, but
FaultLine's claim boundary forbids minting \`SIDE_CAR\` ledgers from Cursor.
When dogfooding FaultLine from Cursor (this Build Week workflow), use this
path — or \`fl record … --transport OBSERVED_EXTERNAL_TRANSPORT\` — so judges
see honest provenance.

Related:

- Generic non-Codex sample: [../observed-external-transport/](../observed-external-transport/)
- Real Codex sidecar excerpt: [../mumbcs-sidecar-ledger/](../mumbcs-sidecar-ledger/)
- Claim rules: [../../security-model.md](../../security-model.md#provenance-honesty-never-fake-codex-ledgers)

## Verify

\`\`\`powershell
pnpm fl record verify --ledger docs/samples/cursor-observed-transport/ledger.json
\`\`\`
`,
    "utf8"
  );

  process.stdout.write(`Wrote ${outDir} (${ledger.events.length} events, valid=${verification.valid}).\n`);
} finally {
  rmSync(root, { recursive: true, force: true });
}
