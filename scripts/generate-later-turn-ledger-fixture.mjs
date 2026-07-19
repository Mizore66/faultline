#!/usr/bin/env node
/**
 * Generate docs/samples/later-turn-ledger/ as a carefully redacted recorded
 * fixture (not a live dogfood capture).
 *
 *   node --import tsx scripts/generate-later-turn-ledger-fixture.mjs
 */
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { digestJson } from "../src/canonical.ts";
import {
  appendLifecycleEvent,
  createCodexLifecycleLedger,
  writeCodexLifecycleLedgerAtomic,
  verifyCodexLifecycleLedger
} from "../src/ledger.ts";
import { captureTurnTreeSnapshot } from "../src/turn-snapshot.ts";

const __dirname = dirname(fileURLToPath(import.meta.url));
const outDir = resolve(__dirname, "../docs/samples/later-turn-ledger");
const REDACTED_ROOT = "/redacted/application";

function git(repo, args) {
  return execFileSync("git", ["-C", repo, ...args], { encoding: "utf8" }).trim();
}

function resignSnapshot(snapshot) {
  const { digest: _digest, ...rest } = snapshot;
  const unsigned = { ...rest, repositoryRoot: REDACTED_ROOT };
  return { ...unsigned, digest: digestJson(unsigned) };
}

const root = mkdtempSync(join(tmpdir(), "faultline-later-turn-fixture-"));
try {
  git(root, ["init"]);
  git(root, ["config", "user.email", "fixture@faultline.example"]);
  git(root, ["config", "user.name", "FaultLine Fixture"]);
  writeFileSync(join(root, "state.txt"), "known-good\n", "utf8");
  git(root, ["add", "state.txt"]);
  git(root, ["commit", "-m", "known good"]);
  // Reachable commits keep turn-tree objects inside trees.bundle (--all).
  const pinTree = (name) => {
    const tree = git(root, ["write-tree"]);
    const commit = git(root, [
      "-c", "user.name=FaultLine Fixture",
      "-c", "user.email=fixture@faultline.example",
      "commit-tree", tree, "-m", `fixture pin ${name}`
    ]);
    git(root, ["update-ref", `refs/faultline/${name}`, commit]);
    return tree;
  };
  pinTree("baseline");

  let ledger = createCodexLifecycleLedger({
    sessionId: "later-turn-fixture-session",
    ledgerId: "ledger-later-turn-fixture"
  });
  ledger = appendLifecycleEvent(ledger, {
    type: "SESSION_STARTED",
    payload: {
      transport: "SIDE_CAR",
      workingDirectory: REDACTED_ROOT,
      model: "gpt-5.6"
    }
  }, { eventId: "fixture-session-start" });

  const baseline = resignSnapshot(captureTurnTreeSnapshot(root).snapshot);
  ledger = appendLifecycleEvent(ledger, {
    type: "SESSION_BASELINE_SNAPSHOT",
    payload: { snapshot: baseline }
  }, { eventId: "fixture-baseline" });

  writeFileSync(join(root, "note.txt"), "investigation note\n", "utf8");
  git(root, ["add", "note.txt"]);
  pinTree("turn-1");
  const snap1 = resignSnapshot(captureTurnTreeSnapshot(root).snapshot);
  ledger = appendLifecycleEvent(ledger, {
    type: "TURN_STARTED",
    payload: {
      turnId: "turn-1",
      turnOrdinal: 1,
      promptDigest: `sha256:${"a".repeat(64)}`
    }
  }, { eventId: "fixture-t1-start" });
  ledger = appendLifecycleEvent(ledger, {
    type: "TOOL_USE_STARTED",
    payload: {
      turnId: "turn-1",
      turnOrdinal: 1,
      toolName: "Read",
      toolCallId: "call-read-1",
      allowlistedFieldsDigest: digestJson({
        sessionId: "later-turn-fixture-session",
        turnId: "turn-1",
        toolName: "Read",
        toolCallId: "call-read-1"
      })
    }
  }, { eventId: "fixture-t1-tool-pre" });
  ledger = appendLifecycleEvent(ledger, {
    type: "TOOL_USE_COMPLETED",
    payload: {
      turnId: "turn-1",
      turnOrdinal: 1,
      toolName: "Read",
      toolCallId: "call-read-1",
      allowlistedFieldsDigest: digestJson({
        sessionId: "later-turn-fixture-session",
        turnId: "turn-1",
        toolName: "Read",
        toolCallId: "call-read-1"
      })
    }
  }, { eventId: "fixture-t1-tool-post" });
  ledger = appendLifecycleEvent(ledger, {
    type: "TURN_COMPLETED",
    payload: { turnId: "turn-1", turnOrdinal: 1, outcome: "COMPLETED" }
  }, { eventId: "fixture-t1-stop" });
  ledger = appendLifecycleEvent(ledger, {
    type: "TURN_TREE_SNAPSHOT",
    payload: { turnId: "turn-1", turnOrdinal: 1, snapshot: snap1 }
  }, { eventId: "fixture-t1-snap" });

  writeFileSync(join(root, "state.txt"), "regressed\n", "utf8");
  git(root, ["add", "state.txt"]);
  pinTree("turn-2");
  const snap2 = resignSnapshot(captureTurnTreeSnapshot(root).snapshot);
  ledger = appendLifecycleEvent(ledger, {
    type: "TURN_STARTED",
    payload: {
      turnId: "turn-2",
      turnOrdinal: 2,
      promptDigest: `sha256:${"c".repeat(64)}`
    }
  }, { eventId: "fixture-t2-start" });
  ledger = appendLifecycleEvent(ledger, {
    type: "TURN_COMPLETED",
    payload: { turnId: "turn-2", turnOrdinal: 2, outcome: "COMPLETED" }
  }, { eventId: "fixture-t2-stop" });
  ledger = appendLifecycleEvent(ledger, {
    type: "TURN_TREE_SNAPSHOT",
    payload: { turnId: "turn-2", turnOrdinal: 2, snapshot: snap2 }
  }, { eventId: "fixture-t2-snap" });

  ledger = appendLifecycleEvent(ledger, {
    type: "SESSION_ENDED",
    payload: { reason: "COMPLETED", completedTurns: 2 }
  }, { eventId: "fixture-session-end" });

  const verification = verifyCodexLifecycleLedger(ledger);
  if (!verification.valid) {
    throw new Error(`Fixture ledger invalid: ${verification.errors.join("; ")}`);
  }

  mkdirSync(outDir, { recursive: true });
  const ledgerPath = join(outDir, "ledger.json");
  writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

  const bundlePath = join(outDir, "trees.bundle");
  git(root, ["bundle", "create", bundlePath, "--all"]);

  writeFileSync(join(outDir, "fixture-meta.json"), `${JSON.stringify({
    schemaVersion: "faultline.later-turn-ledger-fixture.v1",
    kind: "RECORDED_REDACTED_FIXTURE",
    sessionId: ledger.sessionId,
    ledgerId: ledger.ledgerId,
    headHash: verification.headHash,
    eventCount: verification.eventCount,
    turnTreeDigests: {
      baseline: baseline.treeDigest,
      turn1: snap1.treeDigest,
      turn2: snap2.treeDigest
    },
    note: "Not a live organic dogfood capture. Produced by scripts/generate-later-turn-ledger-fixture.mjs."
  }, null, 2)}\n`, "utf8");

  writeFileSync(join(outDir, "README.md"), `# Later-turn ledger sample (recorded fixture)

This directory holds a **carefully redacted recorded fixture**, not a live organic
dogfood capture from a production Codex session.

## Provenance

- Generator: \`scripts/generate-later-turn-ledger-fixture.mjs\`
- Shape: sidecar-compatible multi-turn lifecycle ledger (\`faultline.codex-lifecycle-ledger.v1\`)
- Privacy: prompt/tool argument text is never stored — only digests and tool names
- Path labels are redacted to \`/redacted/application\`

## Contents

| File | Role |
| --- | --- |
| \`ledger.json\` | Schema-valid lifecycle ledger (2 turns + tool attribution) |
| \`trees.bundle\` | Git bundle containing tree objects referenced by snapshots |
| \`fixture-meta.json\` | Digests / head hash for quick integrity checks |

## Use with FaultLine

\`\`\`bash
# Verify the ledger offline
pnpm exec tsx -e "import { verifyCodexLifecycleLedgerFile } from './src/ledger.ts'; console.log(verifyCodexLifecycleLedgerFile('docs/samples/later-turn-ledger/ledger.json'))"

# Clone trees for minimize / prove drills
git clone docs/samples/later-turn-ledger/trees.bundle /tmp/later-turn-trees

# Point investigate turns at the fixture ledger (still needs a frozen witness + image)
pnpm fl investigate turns --repo /tmp/later-turn-trees \\
  --ledger docs/samples/later-turn-ledger/ledger.json \\
  --proposal <id> --expect-digest <digest> --image <digest-pinned-image>
\`\`\`

This sample supports CI drills for turn minimization (PASS→FAIL) without claiming
\`TURN_PROOF\` or a live external impact case.
`, "utf8");

  console.log(`Wrote ${ledgerPath}`);
  console.log(`headHash=${verification.headHash} events=${verification.eventCount}`);
} finally {
  rmSync(root, { recursive: true, force: true });
}
