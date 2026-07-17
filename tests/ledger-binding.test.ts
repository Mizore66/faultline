import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  appendLifecycleEvent,
  captureGitCleanCheckpoint,
  createCodexLifecycleLedger,
  signGitCheckpoint,
  type CodexLifecycleLedger,
  type GitCheckpoint
} from "../src/ledger.js";
import {
  bindVerifiedLedgerToGitInvestigation,
  createLedgerBoundInvestigation,
  readVerifiedLedgerBoundInvestigation,
  verifyLedgerBoundInvestigation,
  verifyLedgerBoundInvestigationFile,
  writeLedgerBoundInvestigationAtomic
} from "../src/ledger-binding.js";
import { investigateGitRange, type GitInvestigationResult } from "../src/git-investigation.js";
import type { SandboxCommandRunner } from "../src/sandbox.js";
import { formatWitnessResult } from "../src/witness-result.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../src/witness-lock.js";

const PINNED_IMAGE = `registry.example/faultline-node@sha256:${"a".repeat(64)}`;
const DATE = "2026-07-16";

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function commit(repository: string, state: string, message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

type RepositoryFixture = {
  root: string;
  ancestor: string;
  descendant: string;
  outside: string;
};

function createRepository(): RepositoryFixture {
  const root = mkdtempSync(join(tmpdir(), "faultline-ledger-binding-repo-"));
  git(root, ["init"]);
  git(root, ["config", "user.email", "faultline@example.test"]);
  git(root, ["config", "user.name", "FaultLine Test"]);
  const ancestor = commit(root, "good", "known good");
  const descendant = commit(root, "bad", "known bad");
  const outside = commit(root, "later", "outside requested range");
  return { root, ancestor, descendant, outside };
}

function captureAt(repository: string, revision: string, second: number): GitCheckpoint {
  git(repository, ["checkout", "--detach", revision]);
  return captureGitCleanCheckpoint(repository, {
    now: () => new Date(`${DATE}T00:00:${String(second).padStart(2, "0")}.500Z`)
  });
}

function timestamp(second: number): string {
  return `${DATE}T00:00:${String(second).padStart(2, "0")}.000Z`;
}

function appendAt(
  ledger: CodexLifecycleLedger,
  input: Parameters<typeof appendLifecycleEvent>[1],
  eventNumber: number,
  second: number
): CodexLifecycleLedger {
  return appendLifecycleEvent(ledger, input, {
    eventId: `event-${eventNumber}`,
    occurredAt: timestamp(second)
  });
}

/** Each checkpoint follows a completed turn, mirroring the only lifecycle form the ledger accepts. */
function createLedger(checkpoints: readonly GitCheckpoint[]): CodexLifecycleLedger {
  let eventNumber = 1;
  let ledger = createCodexLifecycleLedger({
    ledgerId: "ledger-real-git-binding",
    sessionId: "session-real-git-binding",
    createdAt: timestamp(0)
  });
  ledger = appendAt(ledger, {
    type: "SESSION_STARTED",
    payload: { transport: "CODEX_CLI", workingDirectory: checkpoints[0]?.repositoryRoot ?? "C:/unused" }
  }, eventNumber++, 1);
  for (let index = 0; index < checkpoints.length; index += 1) {
    const checkpoint = checkpoints[index];
    if (!checkpoint) throw new Error("fixture omitted a checkpoint");
    const turnOrdinal = index + 1;
    ledger = appendAt(ledger, {
      type: "TURN_STARTED",
      payload: {
        turnId: `turn-${turnOrdinal}`,
        turnOrdinal,
        promptDigest: `sha256:${"c".repeat(64)}`
      }
    }, eventNumber++, 2 + (index * 4));
    ledger = appendAt(ledger, {
      type: "TURN_COMPLETED",
      payload: {
        turnId: `turn-${turnOrdinal}`,
        turnOrdinal,
        outcome: "COMPLETED",
        outputDigest: `sha256:${"d".repeat(64)}`
      }
    }, eventNumber++, 3 + (index * 4));
    ledger = appendAt(ledger, {
      type: "WORKTREE_CHECKPOINT",
      payload: { checkpoint, afterTurnOrdinal: turnOrdinal }
    }, eventNumber++, 4 + (index * 4));
  }
  return appendAt(ledger, {
    type: "SESSION_ENDED",
    payload: { reason: "COMPLETED", completedTurns: checkpoints.length }
  }, eventNumber, 1 + (checkpoints.length * 4));
}

function createFrozenWitness(store: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId: "binding-witness",
    proposalOrigin: "HUMAN",
    proposedAt: `${DATE}T01:00:00.000Z`,
    incidentPacket: {
      symptom: "The fixture should reject the bad state.",
      ciLog: "state.txt was bad",
      repositoryLanguage: "Text",
      repositorySummary: "A real temporary Git repository for lifecycle binding tests."
    },
    witness: {
      behavior: "state.txt must not say bad",
      command: "node witness.mjs",
      overlays: [],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: `${DATE}T01:01:00.000Z`
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: `${DATE}T01:02:00.000Z` });
}

function stateRunner(): SandboxCommandRunner {
  return {
    async run(invocation) {
      const state = readFileSync(join(invocation.cwd, "state.txt"), "utf8").trim();
      return state === "bad"
        ? { exitCode: 1, stdout: `state=${state}\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "witness failed" }
        : { exitCode: 0, stdout: `state=${state}\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
    }
  };
}

async function actualInvestigation(repository: RepositoryFixture, witness: FrozenWitness): Promise<GitInvestigationResult> {
  return investigateGitRange({
    repository: repository.root,
    range: { ancestor: repository.ancestor, descendant: repository.descendant },
    frozenWitness: witness,
    expectedFrozenDigest: witness.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image: PINNED_IMAGE },
    runner: stateRunner()
  });
}

describe("ledger-bound Git investigations", () => {
  it("binds every actual Git replay state to a real, ordered lifecycle checkpoint and persists it canonically", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-ledger-binding-store-"));
    const repository = createRepository();
    try {
      const ancestorCheckpoint = captureAt(repository.root, repository.ancestor, 3);
      const descendantCheckpoint = captureAt(repository.root, repository.descendant, 7);
      const ledger = createLedger([ancestorCheckpoint, descendantCheckpoint]);
      const investigation = await actualInvestigation(repository, createFrozenWitness(store));

      const report = bindVerifiedLedgerToGitInvestigation(ledger, investigation);
      expect(report).toMatchObject({
        valid: true,
        checkpointCount: 2,
        investigatedStateCount: 2,
        matchedStateCount: 2,
        missingStates: [],
        unmatchedCheckpoints: [],
        duplicateCheckpoints: [],
        outOfRangeCheckpoints: []
      });
      expect(report.bindings.map((binding) => [binding.stateIndex, binding.afterTurnOrdinal, binding.turnId]))
        .toEqual([[0, 1, "turn-1"], [1, 2, "turn-2"]]);
      expect(report.bindings.map((binding) => binding.checkpointEventSequence)).toEqual([4, 7]);

      const bound = createLedgerBoundInvestigation(ledger, investigation);
      expect(bound.nativeCodexInterception).toBe(false);
      expect(verifyLedgerBoundInvestigation(bound, bound.bindingDigest)).toMatchObject({
        valid: true,
        externalDigestStatus: "MATCH"
      });

      const stored = join(store, "bound.json");
      writeLedgerBoundInvestigationAtomic(stored, bound);
      expect(readFileSync(stored, "utf8")).toBe(`${JSON.stringify(JSON.parse(readFileSync(stored, "utf8")))}\n`);
      expect(verifyLedgerBoundInvestigationFile(stored, bound.bindingDigest)).toMatchObject({ valid: true, externalDigestStatus: "MATCH" });
      expect(readVerifiedLedgerBoundInvestigation(stored, bound.bindingDigest)).toEqual(bound);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("rejects tampered ledger contents and refuses a missing checkpoint state", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-ledger-binding-store-"));
    const repository = createRepository();
    try {
      const ancestorCheckpoint = captureAt(repository.root, repository.ancestor, 3);
      const descendantCheckpoint = captureAt(repository.root, repository.descendant, 7);
      const investigation = await actualInvestigation(repository, createFrozenWitness(store));
      const bound = createLedgerBoundInvestigation(createLedger([ancestorCheckpoint, descendantCheckpoint]), investigation);

      const tampered = structuredClone(bound);
      const checkpointEvent = tampered.ledger.events.find((event) => event.event.type === "WORKTREE_CHECKPOINT");
      if (!checkpointEvent || checkpointEvent.event.type !== "WORKTREE_CHECKPOINT") throw new Error("fixture missing checkpoint event");
      checkpointEvent.event.payload.checkpoint.digest = `sha256:${"f".repeat(64)}`;
      const tamperedVerification = verifyLedgerBoundInvestigation(tampered);
      expect(tamperedVerification.valid).toBe(false);
      expect(tamperedVerification.errors.join("\n")).toMatch(/Lifecycle ledger is invalid|bindingDigest/);

      const missingReport = bindVerifiedLedgerToGitInvestigation(createLedger([ancestorCheckpoint]), investigation);
      expect(missingReport.valid).toBe(false);
      expect(missingReport.missingStates).toHaveLength(1);
      expect(missingReport.missingStates[0]).toMatchObject({ commit: repository.descendant });
      expect(() => createLedgerBoundInvestigation(createLedger([ancestorCheckpoint]), investigation)).toThrow(/no unique verified checkpoint/);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("reports mismatched, duplicate, and out-of-range checkpoint facts rather than silently dropping them", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-ledger-binding-store-"));
    const repository = createRepository();
    try {
      const ancestorCheckpoint = captureAt(repository.root, repository.ancestor, 3);
      const descendantCheckpoint = captureAt(repository.root, repository.descendant, 7);
      const outsideCheckpoint = captureAt(repository.root, repository.outside, 11);
      const investigation = await actualInvestigation(repository, createFrozenWitness(store));

      const mismatchedCheckpoint = signGitCheckpoint({
        schemaVersion: ancestorCheckpoint.schemaVersion,
        repositoryRoot: ancestorCheckpoint.repositoryRoot,
        headCommit: ancestorCheckpoint.headCommit,
        treeDigest: descendantCheckpoint.treeDigest,
        capturedAt: `${DATE}T00:00:07.500Z`,
        clean: true
      });
      const mismatchReport = bindVerifiedLedgerToGitInvestigation(createLedger([ancestorCheckpoint, mismatchedCheckpoint]), investigation);
      expect(mismatchReport.valid).toBe(false);
      expect(mismatchReport.unmatchedCheckpoints).toHaveLength(1);
      expect(mismatchReport.unmatchedCheckpoints[0]).toMatchObject({ reason: "MISMATCHED_CHECKPOINT" });

      const duplicateReport = bindVerifiedLedgerToGitInvestigation(createLedger([ancestorCheckpoint, ancestorCheckpoint, descendantCheckpoint]), investigation);
      expect(duplicateReport.valid).toBe(false);
      expect(duplicateReport.duplicateCheckpoints).toHaveLength(1);
      expect(duplicateReport.duplicateCheckpoints[0]).toMatchObject({ reason: "DUPLICATE_CHECKPOINT" });

      const rangeReport = bindVerifiedLedgerToGitInvestigation(createLedger([ancestorCheckpoint, descendantCheckpoint, outsideCheckpoint]), investigation);
      expect(rangeReport.valid).toBe(false);
      expect(rangeReport.outOfRangeCheckpoints).toHaveLength(1);
      expect(rangeReport.outOfRangeCheckpoints[0]).toMatchObject({
        reason: "OUT_OF_RANGE_CHECKPOINT",
        commit: repository.outside
      });
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });
});
