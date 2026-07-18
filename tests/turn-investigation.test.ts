import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import {
  appendLifecycleEvent,
  createCodexLifecycleLedger,
  writeCodexLifecycleLedgerAtomic
} from "../src/ledger.js";
import type { SandboxCommandRunner } from "../src/sandbox.js";
import { formatWitnessResult } from "../src/witness-result.js";
import { captureTurnTreeSnapshot } from "../src/turn-snapshot.js";
import {
  duplicateTurnOrdinalError,
  investigateTurnTrees,
  turnInvestigationDigest,
  turnStatesFromLedger
} from "../src/turn-investigation.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../src/witness-lock.js";

const pinnedImage = `registry.example/faultline-node@sha256:${"c".repeat(64)}`;

function git(repository: string, args: string[]): string {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
  return (result.stdout ?? "").trim();
}

function createFrozenWitness(store: string, proposalId: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-17T12:00:00.000Z",
    incidentPacket: {
      symptom: "Turn-tree regression under a frozen witness.",
      ciLog: "state.txt became bad across Codex turns",
      repositoryLanguage: "Text fixture",
      repositorySummary: "Temporary turn-investigation fixture."
    },
    witness: {
      behavior: "state.txt must remain good.",
      command: "node witness.mjs",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from("export const frozenWitness = 'exact-approved-bytes';\n", "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: "2026-07-17T12:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-17T12:02:00.000Z" });
}

function stateReadingRunner(options?: {
  incompatibleAt?: string;
  unstableAt?: string;
}): SandboxCommandRunner {
  let attempt = 0;
  return {
    async run(invocation) {
      const state = readFileSync(join(invocation.cwd, "state.txt"), "utf8").trim();
      const overlay = readFileSync(join(invocation.cwd, "witness.mjs"), "utf8");
      if (overlay !== "export const frozenWitness = 'exact-approved-bytes';\n") {
        return { exitCode: 2, stdout: "", stderr: "frozen overlay bytes changed" };
      }
      if (options?.incompatibleAt === state) {
        return {
          exitCode: 1,
          stdout: `${formatWitnessResult("INCOMPATIBLE_STATE", "api missing")}\n`,
          stderr: "Cannot find module"
        };
      }
      if (options?.unstableAt === state) {
        attempt += 1;
        return attempt % 2 === 0
          ? { exitCode: 0, stdout: `${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" }
          : { exitCode: 1, stdout: `${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "flaky" };
      }
      return state === "bad"
        ? { exitCode: 1, stdout: `state=${state}\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "witness failed" }
        : { exitCode: 0, stdout: `state=${state}\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
    }
  };
}

function appendTurnSnapshot(
  ledger: ReturnType<typeof createCodexLifecycleLedger>,
  repository: string,
  turnOrdinal: number,
  turnId: string
) {
  const snapshot = captureTurnTreeSnapshot(repository);
  let next = appendLifecycleEvent(ledger, {
    type: "TURN_STARTED",
    payload: { turnId, turnOrdinal, promptDigest: `sha256:${"a".repeat(64)}` }
  });
  next = appendLifecycleEvent(next, {
    type: "TURN_COMPLETED",
    payload: { turnId, turnOrdinal, outcome: "COMPLETED" }
  });
  return appendLifecycleEvent(next, {
    type: "TURN_TREE_SNAPSHOT",
    payload: { turnId, turnOrdinal, snapshot }
  });
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

  it("localizes PASS→FAIL when an earlier turn snapshot passes and a later one fails", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-pass-fail-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      git(repository, ["add", "state.txt"]);
      git(repository, ["commit", "-m", "good"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-pass-fail" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 2, "turn-2");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-pass-fail");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner()
      });

      expect(result.status).toBe("COMPLETED");
      expect(result.transitions).toHaveLength(1);
      expect(result.transitions[0]).toMatchObject({ kind: "PASS_TO_FAIL" });
      expect(result.proof.evidenceGrade).toBe("EXPERIMENTAL_TURN");
      expect(result.proof.isProof).toBe(false);
      expect(turnInvestigationDigest(result)).toMatch(/^sha256:[a-f0-9]{64}$/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("skips incompatible turn boundaries and does not invent a PASS→FAIL across them", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-incompatible-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      git(repository, ["add", "state.txt"]);
      git(repository, ["commit", "-m", "good"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-incompatible" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      writeFileSync(join(repository, "state.txt"), "incompatible\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 2, "turn-2");
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 3, "turn-3");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-incompatible");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner({ incompatibleAt: "incompatible" })
      });

      expect(result.status).toBe("COMPLETED");
      expect(result.transitions).toEqual([]);
      expect(result.proof.isProof).toBe(false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses a stable boundary when the witness is unstable at a turn", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-unstable-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      git(repository, ["add", "state.txt"]);
      git(repository, ["commit", "-m", "good"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-unstable" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      writeFileSync(join(repository, "state.txt"), "flaky\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 2, "turn-2");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-unstable");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner({ unstableAt: "flaky" })
      });

      expect(result.transitions).toEqual([]);
      expect(result.proof.isProof).toBe(false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("rejects overlay paths that attempt ../escape via the shared safe overlay writer", async () => {
    const { safeOverlayTarget } = await import("../src/safe-overlay.js");
    const worktree = mkdtempSync(join(tmpdir(), "faultline-turn-escape-"));
    try {
      await expect(safeOverlayTarget(worktree, "../escape.mjs")).rejects.toThrow(/unsafe|escap/i);
      await expect(safeOverlayTarget(worktree, "/tmp/escape.mjs")).rejects.toThrow(/unsafe/i);
    } finally {
      rmSync(worktree, { recursive: true, force: true });
    }
  });

  it("rejects overlay writes when the tracked target path is a symlink", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-symlink-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      writeFileSync(join(repository, "real-witness.mjs"), "export const x = 1;\n", "utf8");
      try {
        symlinkSync("real-witness.mjs", join(repository, "witness.mjs"));
      } catch {
        // Symlink creation can be denied on some Windows CI images.
        return;
      }
      git(repository, ["add", "state.txt", "real-witness.mjs", "witness.mjs"]);
      git(repository, ["commit", "-m", "symlink overlay target"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-symlink" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 2, "turn-2");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-symlink");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner()
      });

      expect(result.status).toBe("EXECUTION_ERROR");
      expect(result.errors.some((error) => /symbolic link/i.test(error))).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses host materialization when a turn tree declares a Git clean filter", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-filter-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      writeFileSync(join(repository, ".gitattributes"), "*.txt filter=faultline-evil\n", "utf8");
      git(repository, ["add", "state.txt", ".gitattributes"]);
      git(repository, ["commit", "-m", "filter attributes"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-filter" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 2, "turn-2");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-filter");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner()
      });

      expect(result.status).toBe("EXECUTION_ERROR");
      expect(result.errors.some((error) => /filter attribute/i.test(error))).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("fails closed when lockfiles change between turn states", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-lockfile-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\nA\n", "utf8");
      git(repository, ["add", "state.txt", "pnpm-lock.yaml"]);
      git(repository, ["commit", "-m", "good with lock"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-lock" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\nB\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 2, "turn-2");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-lock");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner()
      });

      expect(result.status).toBe("ENVIRONMENT_CHANGED");
      expect(result.proof.isProof).toBe(false);
      expect(result.transitions).toEqual([]);
      expect(result.errors.some((error) => /fingerprint/i.test(error))).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses ambiguous localization when two snapshots share one turnOrdinal", () => {
    expect(duplicateTurnOrdinalError([
      {
        index: 0,
        turnId: "a",
        turnOrdinal: 1,
        treeDigest: "a".repeat(40),
        snapshotDigest: `sha256:${"b".repeat(64)}`,
        dirty: true
      },
      {
        index: 1,
        turnId: "b",
        turnOrdinal: 1,
        treeDigest: "c".repeat(40),
        snapshotDigest: `sha256:${"d".repeat(64)}`,
        dirty: true
      }
    ])).toMatch(/Duplicate turnOrdinal/);
    expect(duplicateTurnOrdinalError([
      {
        index: 0,
        turnId: "a",
        turnOrdinal: 1,
        treeDigest: "a".repeat(40),
        snapshotDigest: `sha256:${"b".repeat(64)}`,
        dirty: false
      },
      {
        index: 1,
        turnId: "b",
        turnOrdinal: 2,
        treeDigest: "c".repeat(40),
        snapshotDigest: `sha256:${"d".repeat(64)}`,
        dirty: false
      }
    ])).toBeNull();
  });
});
