import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
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

  it("runs live Docker turn minimize when FAULTLINE_LIVE_TURN_MINIMIZE=1", async () => {
    if (process.env.FAULTLINE_LIVE_TURN_MINIMIZE !== "1") {
      expect(process.env.FAULTLINE_LIVE_TURN_MINIMIZE).not.toBe("1");
      return;
    }
    if (process.env.FAULTLINE_DOCKER_INTEGRATION !== "1") {
      expect(process.env.FAULTLINE_DOCKER_INTEGRATION).not.toBe("1");
      return;
    }

    const { minimizeGitDiff } = await import("../src/git-minimization.js");
    const {
      approveWitnessProposal,
      freezeApprovedWitness,
      proposeWitness
    } = await import("../src/witness-lock.js");
    const { formatWitnessResult } = await import("../src/witness-result.js");

    execFileSync("docker", ["pull", "node:22-alpine"], { stdio: "inherit" });
    const image = execFileSync(
      "docker",
      ["image", "inspect", "node:22-alpine", "--format", "{{index .RepoDigests 0}}"],
      { encoding: "utf8" }
    ).trim();

    const root = mkdtempSync(join(tmpdir(), "faultline-cdx02-live-"));
    try {
      git(root, ["clone", bundlePath, "trees"]);
      const repo = join(root, "trees");
      const store = join(root, "store");
      mkdirSync(store, { recursive: true });
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

      const passLine = formatWitnessResult("PREDICATE_PASS");
      const failLine = formatWitnessResult("PREDICATE_FAIL");
      const proposal = proposeWitness(store, {
        proposalId: "live-turn-minimize",
        proposalOrigin: "HUMAN",
        proposedAt: "2026-07-19T15:00:00.000Z",
        incidentPacket: {
          symptom: "Later-turn fixture PASS→FAIL minimize drill",
          ciLog: "CDX-02 live Docker",
          repositoryLanguage: "Text fixture",
          repositorySummary: "Turn minimize live drill"
        },
        witness: {
          behavior: "Turn 2 tree fails the predicate.",
          command: "node witness.mjs",
          overlays: [{
            path: "witness.mjs",
            bytesBase64: Buffer.from([
              'import { readFileSync } from "node:fs";',
              // Fixture trees: turn1 state.txt=known-good, turn2 state.txt=regressed.
              // Counterfactual worktrees are synthetic commits — never key off HEAD.
              'const state = readFileSync("state.txt", "utf8").trim();',
              'if (state === "regressed") {',
              `  console.log(${JSON.stringify(failLine)});`,
              "  process.exit(1);",
              "}",
              'if (state !== "known-good") {',
              '  console.error(`unexpected state: ${state}`);',
              "  process.exit(2);",
              "}",
              `console.log(${JSON.stringify(passLine)});`,
              "process.exit(0);",
              ""
            ].join("\n"), "utf8").toString("base64")
          }],
          policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
        }
      });
      approveWitnessProposal(store, proposal.proposalId, {
        approvedBy: "live-turn-minimize@faultline.test",
        approvedAt: "2026-07-19T15:01:00.000Z"
      });
      const frozen = freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-19T15:02:00.000Z" });

      const result = await minimizeGitDiff({
        repository: repo,
        before: bridge.beforeCommit,
        after: bridge.afterCommit,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        sandbox: { mode: "DOCKER_ISOLATED", image },
        budget: { maxExecutions: 64 }
      });
      expect(["COMPLETED", "CERTIFICATION_FAILED", "BUDGET_EXHAUSTED", "UNSAFE_LOCAL_INAPPLICABLE"]).toContain(result.status);
      expect(result.attempts.length).toBeGreaterThan(0);
      expect(result.proof.executionTrust === "NATIVE_DOCKER" || result.runs.length > 0).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  }, 180_000);
});
