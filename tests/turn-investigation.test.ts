import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
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
import { captureTurnTreeSnapshot, signTurnTreeSnapshot, TURN_TREE_SNAPSHOT_VERSION } from "../src/turn-snapshot.js";
import { sha256 } from "../src/canonical.js";
import { ENVIRONMENT_CHANGED_PROOF_MESSAGE } from "../src/environment-fingerprint.js";
import {
  attributeFailureIntroduction,
  duplicateTurnOrdinalError,
  investigateTurnTrees,
  SESSION_BASELINE_TURN_ID,
  TURN_EVIDENCE_LABEL_EXPERIMENTAL,
  turnInvestigationDigest,
  turnStatesFromLedger,
  type StableTurnState,
  type StableTurnTransition,
  type TurnState
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
        // Avoid WITNESS_SETUP_ERROR stderr signatures so structured INCOMPATIBLE_STATE wins.
        return {
          exitCode: 1,
          stdout: `${formatWitnessResult("INCOMPATIBLE_STATE", "api missing")}\n`,
          stderr: "api surface unavailable in this turn state"
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

function appendBaselineSnapshot(
  ledger: ReturnType<typeof createCodexLifecycleLedger>,
  repository: string
) {
  const snapshot = captureTurnTreeSnapshot(repository).snapshot;
  return appendLifecycleEvent(ledger, {
    type: "SESSION_BASELINE_SNAPSHOT",
    payload: { snapshot }
  });
}

function appendTurnSnapshot(
  ledger: ReturnType<typeof createCodexLifecycleLedger>,
  repository: string,
  turnOrdinal: number,
  turnId: string
) {
  const snapshot = captureTurnTreeSnapshot(repository).snapshot;
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

function turnState(partial: Omit<TurnState, "index"> & { index?: number }): TurnState {
  return {
    index: partial.index ?? 0,
    turnId: partial.turnId,
    turnOrdinal: partial.turnOrdinal,
    role: partial.role,
    treeDigest: partial.treeDigest,
    snapshotDigest: partial.snapshotDigest,
    dirty: partial.dirty
  };
}

function stableTurnState(partial: {
  stateIndex: number;
  turnId: string;
  turnOrdinal: number;
  role: "SESSION_BASELINE" | "TURN";
  treeDigest: string;
  snapshotDigest: string;
  verdict: "PASS" | "FAIL";
}): StableTurnState {
  return {
    stateIndex: partial.stateIndex,
    turnId: partial.turnId,
    turnOrdinal: partial.turnOrdinal,
    role: partial.role,
    treeDigest: partial.treeDigest,
    snapshotDigest: partial.snapshotDigest,
    verdict: partial.verdict,
    executionIds: [`sha256:${"1".repeat(64)}`, `sha256:${"2".repeat(64)}`, `sha256:${"3".repeat(64)}`],
    runIds: [`sha256:${"4".repeat(64)}`, `sha256:${"5".repeat(64)}`, `sha256:${"6".repeat(64)}`]
  };
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
      const snapshot = captureTurnTreeSnapshot(repository).snapshot;
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
      const snapshot2 = captureTurnTreeSnapshot(repository).snapshot;
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
      expect(states[0]?.role).toBe("TURN");
      expect(states[1]?.turnOrdinal).toBe(2);
      expect(states[0]?.treeDigest).not.toBe(states[1]?.treeDigest);
      expect(states.some((state) => state.dirty)).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("includes SESSION_BASELINE_SNAPSHOT as synthetic turn-zero before turn snapshots", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-baseline-states-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "a.txt"), "baseline\n", "utf8");
      git(repository, ["add", "a.txt"]);
      git(repository, ["commit", "-m", "base"]);
      writeFileSync(join(repository, "dirty.txt"), "preexisting\n", "utf8");

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-baseline-states" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendBaselineSnapshot(ledger, repository);
      writeFileSync(join(repository, "a.txt"), "turn-1\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");

      const states = turnStatesFromLedger(ledger);
      expect(states).toHaveLength(2);
      expect(states[0]).toMatchObject({
        turnId: SESSION_BASELINE_TURN_ID,
        turnOrdinal: 0,
        role: "SESSION_BASELINE",
        dirty: true
      });
      expect(states[1]).toMatchObject({ turnId: "turn-1", turnOrdinal: 1, role: "TURN" });
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
      // Without a session baseline, localization can still find PASS→FAIL, but
      // must not claim a turn introduced the failure.
      expect(result.introduction).toMatchObject({
        status: "UNATTRIBUTED",
        turnOrdinal: null,
        turnId: null
      });
      expect(result.proof.evidenceGrade).toBe("EXPERIMENTAL_TURN");
      expect(result.proof.evidenceLabel).toBe(TURN_EVIDENCE_LABEL_EXPERIMENTAL);
      expect(result.proof.isProof).toBe(false);
      expect(result.proof.executionTrust).toBe("INJECTED_RUNNER");
      expect(result.runs).toHaveLength(result.states.length * 3);
      expect(result.runs.every((run) => run.executionAttempt >= 1 && run.result.stdoutDigest.startsWith("sha256:"))).toBe(true);
      expect(result.stableStates).toHaveLength(2);
      expect(turnInvestigationDigest(result)).toMatch(/^sha256:[a-f0-9]{64}$/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("attributes introduction to Turn 1 when the session baseline passes and Turn 1 fails", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-intro-attributed-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      git(repository, ["add", "state.txt"]);
      git(repository, ["commit", "-m", "good"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-intro-attributed" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendBaselineSnapshot(ledger, repository);
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-intro-attributed");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner()
      });

      expect(result.status).toBe("COMPLETED");
      expect(result.states[0]).toMatchObject({
        turnOrdinal: 0,
        role: "SESSION_BASELINE",
        turnId: SESSION_BASELINE_TURN_ID
      });
      expect(result.transitions).toHaveLength(1);
      expect(result.transitions[0]).toMatchObject({ kind: "PASS_TO_FAIL" });
      expect(result.introduction).toMatchObject({
        status: "ATTRIBUTED",
        turnOrdinal: 1,
        turnId: "turn-1"
      });
      expect(result.introduction.reason).toMatch(/Earliest recorded stable PASS→FAIL occurred at turn 1/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("attributes introduction to a later turn when the baseline is a stable PASS (need not be adjacent)", () => {
    const baseline = turnState({
      index: 0,
      turnId: SESSION_BASELINE_TURN_ID,
      turnOrdinal: 0,
      role: "SESSION_BASELINE",
      treeDigest: "a".repeat(40),
      snapshotDigest: `sha256:${"b".repeat(64)}`,
      dirty: false
    });
    const turn1 = turnState({
      index: 1,
      turnId: "turn-1",
      turnOrdinal: 1,
      role: "TURN",
      treeDigest: "c".repeat(40),
      snapshotDigest: `sha256:${"d".repeat(64)}`,
      dirty: false
    });
    const turn2 = turnState({
      index: 2,
      turnId: "turn-2",
      turnOrdinal: 2,
      role: "TURN",
      treeDigest: "e".repeat(40),
      snapshotDigest: `sha256:${"f".repeat(64)}`,
      dirty: false
    });
    const turn3 = turnState({
      index: 3,
      turnId: "turn-3",
      turnOrdinal: 3,
      role: "TURN",
      treeDigest: "1".repeat(40),
      snapshotDigest: `sha256:${"2".repeat(64)}`,
      dirty: true
    });
    const stable: StableTurnState[] = [
      stableTurnState({
        stateIndex: 0,
        turnId: SESSION_BASELINE_TURN_ID,
        turnOrdinal: 0,
        role: "SESSION_BASELINE",
        treeDigest: "a".repeat(40),
        snapshotDigest: `sha256:${"b".repeat(64)}`,
        verdict: "PASS"
      }),
      stableTurnState({
        stateIndex: 1,
        turnId: "turn-1",
        turnOrdinal: 1,
        role: "TURN",
        treeDigest: "c".repeat(40),
        snapshotDigest: `sha256:${"d".repeat(64)}`,
        verdict: "PASS"
      }),
      stableTurnState({
        stateIndex: 2,
        turnId: "turn-2",
        turnOrdinal: 2,
        role: "TURN",
        treeDigest: "e".repeat(40),
        snapshotDigest: `sha256:${"f".repeat(64)}`,
        verdict: "PASS"
      }),
      stableTurnState({
        stateIndex: 3,
        turnId: "turn-3",
        turnOrdinal: 3,
        role: "TURN",
        treeDigest: "1".repeat(40),
        snapshotDigest: `sha256:${"2".repeat(64)}`,
        verdict: "FAIL"
      })
    ];
    const before = stable[2]!;
    const after = stable[3]!;
    const transitions: StableTurnTransition[] = [{ kind: "PASS_TO_FAIL", before, after }];
    expect(attributeFailureIntroduction([baseline, turn1, turn2, turn3], transitions, stable)).toMatchObject({
      status: "ATTRIBUTED",
      turnOrdinal: 3,
      turnId: "turn-3"
    });
    expect(attributeFailureIntroduction([baseline, turn1, turn2, turn3], transitions, stable).reason)
      .toMatch(/Earliest recorded stable PASS→FAIL occurred at turn 3/);
  });

  it("does not attribute when the session baseline is not a stable PASS", () => {
    const baseline = turnState({
      index: 0,
      turnId: SESSION_BASELINE_TURN_ID,
      turnOrdinal: 0,
      role: "SESSION_BASELINE",
      treeDigest: "a".repeat(40),
      snapshotDigest: `sha256:${"b".repeat(64)}`,
      dirty: false
    });
    const turn1 = turnState({
      index: 1,
      turnId: "turn-1",
      turnOrdinal: 1,
      role: "TURN",
      treeDigest: "c".repeat(40),
      snapshotDigest: `sha256:${"d".repeat(64)}`,
      dirty: true
    });
    const before = stableTurnState({
      stateIndex: 0,
      turnId: SESSION_BASELINE_TURN_ID,
      turnOrdinal: 0,
      role: "SESSION_BASELINE",
      treeDigest: "a".repeat(40),
      snapshotDigest: `sha256:${"b".repeat(64)}`,
      verdict: "FAIL"
    });
    const after = stableTurnState({
      stateIndex: 1,
      turnId: "turn-1",
      turnOrdinal: 1,
      role: "TURN",
      treeDigest: "c".repeat(40),
      snapshotDigest: `sha256:${"d".repeat(64)}`,
      verdict: "FAIL"
    });
    // No PASS→FAIL — NOT_APPLICABLE. Force a synthetic PASS→FAIL that skips a non-PASS baseline.
    const passBefore = stableTurnState({
      stateIndex: 0,
      turnId: "ghost-pass",
      turnOrdinal: 1,
      role: "TURN",
      treeDigest: "e".repeat(40),
      snapshotDigest: `sha256:${"f".repeat(64)}`,
      verdict: "PASS"
    });
    const failAfter = stableTurnState({
      stateIndex: 1,
      turnId: "turn-1",
      turnOrdinal: 1,
      role: "TURN",
      treeDigest: "c".repeat(40),
      snapshotDigest: `sha256:${"d".repeat(64)}`,
      verdict: "FAIL"
    });
    expect(attributeFailureIntroduction(
      [baseline, turn1],
      [{ kind: "PASS_TO_FAIL", before: passBefore, after: failAfter }],
      [before, after]
    )).toMatchObject({
      status: "UNATTRIBUTED",
      turnOrdinal: null,
      turnId: null
    });
  });

  it("attributes Turn 3 when baseline and Turns 1–2 pass then Turn 3 fails (end-to-end)", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-later-attr-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      git(repository, ["add", "state.txt"]);
      git(repository, ["commit", "-m", "good"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-later-attr" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendBaselineSnapshot(ledger, repository);
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      ledger = appendTurnSnapshot(ledger, repository, 2, "turn-2");
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 3, "turn-3");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-later-attr");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner()
      });

      expect(result.status).toBe("COMPLETED");
      expect(result.states).toHaveLength(4);
      expect(result.transitions).toHaveLength(1);
      expect(result.transitions[0]).toMatchObject({ kind: "PASS_TO_FAIL" });
      expect(result.introduction).toMatchObject({
        status: "ATTRIBUTED",
        turnOrdinal: 3,
        turnId: "turn-3"
      });
      expect(result.introduction.reason).toMatch(/Earliest recorded stable PASS→FAIL occurred at turn 3/);
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
      // Match Windows Git's default: checkout materializes mode 120000 as a plain
      // file. The product must still refuse using the index mode, not lstat alone.
      git(repository, ["config", "core.symlinks", "false"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      writeFileSync(join(repository, "real-witness.mjs"), "export const x = 1;\n", "utf8");
      git(repository, ["add", "state.txt", "real-witness.mjs"]);
      // Create a Git symlink without requiring OS symlink privilege.
      const symlinkBlob = spawnSync("git", ["-C", repository, "hash-object", "-w", "--stdin"], {
        encoding: "utf8",
        input: "real-witness.mjs"
      });
      if (symlinkBlob.status !== 0) throw new Error(symlinkBlob.stderr || "hash-object failed");
      git(repository, ["update-index", "--add", "--cacheinfo", `120000,${symlinkBlob.stdout.trim()},witness.mjs`]);
      git(repository, ["commit", "-m", "symlink overlay target"]);

      const appendCommittedTurnSnapshot = (
        ledger: ReturnType<typeof createCodexLifecycleLedger>,
        turnOrdinal: number,
        turnId: string
      ) => {
        // Use the committed tree so mode 120000 is preserved. A worktree
        // capture under core.symlinks=false would flatten the symlink to a file.
        const snapshot = signTurnTreeSnapshot({
          schemaVersion: TURN_TREE_SNAPSHOT_VERSION,
          repositoryRoot: repository,
          headCommit: git(repository, ["rev-parse", "HEAD"]),
          treeDigest: git(repository, ["rev-parse", "HEAD^{tree}"]),
          // Must be at-or-before the lifecycle event timestamp that records it.
          capturedAt: new Date(Date.now() - 1_000).toISOString(),
          dirty: false,
          statusDigest: `sha256:${sha256(`turn-symlink-${turnOrdinal}`)}`
        });
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
      };

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-symlink" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendCommittedTurnSnapshot(ledger, 1, "turn-1");
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      git(repository, ["add", "state.txt"]);
      git(repository, ["commit", "-m", "state became bad"]);
      ledger = appendCommittedTurnSnapshot(ledger, 2, "turn-2");
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
      expect(result.proof.evidenceGrade).toBe("NONE");
      expect(result.transitions).toEqual([]);
      expect(result.runs).toEqual([]);
      expect(result.errors).toContain(ENVIRONMENT_CHANGED_PROOF_MESSAGE);
      expect(result.proof.reason).toBe(ENVIRONMENT_CHANGED_PROOF_MESSAGE);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("fails closed when package.json changes even if the lockfile does not", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-package-json-"));
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\nSAME\n", "utf8");
      writeFileSync(join(repository, "package.json"), JSON.stringify({ name: "fixture", scripts: { test: "node a" } }), "utf8");
      git(repository, ["add", "state.txt", "pnpm-lock.yaml", "package.json"]);
      git(repository, ["commit", "-m", "good"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-package-json" });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: { transport: "SIDE_CAR", workingDirectory: repository }
      });
      ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
      writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
      writeFileSync(join(repository, "package.json"), JSON.stringify({ name: "fixture", scripts: { test: "node b" } }), "utf8");
      ledger = appendTurnSnapshot(ledger, repository, 2, "turn-2");
      const ledgerPath = join(root, "ledger.json");
      writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-package-json");
      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner()
      });

      expect(result.status).toBe("ENVIRONMENT_CHANGED");
      expect(result.environmentHomogeneity).toBe("HETEROGENEOUS");
      expect(result.errors).toContain(ENVIRONMENT_CHANGED_PROOF_MESSAGE);
      expect(result.proof.isProof).toBe(false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("selects per-fingerprint images from runtimeMapping when environments diverge", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-runtime-map-"));
    const imageA = `registry.example/faultline-a@sha256:${"a".repeat(64)}`;
    const imageB = `registry.example/faultline-b@sha256:${"b".repeat(64)}`;
    try {
      const repository = join(root, "repo");
      git(root, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine"]);
      writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
      writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\nA\n", "utf8");
      git(repository, ["add", "state.txt", "pnpm-lock.yaml"]);
      git(repository, ["commit", "-m", "good"]);

      let ledger = createCodexLifecycleLedger({ sessionId: "turn-runtime-map" });
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

      const frozen = createFrozenWitness(join(root, "witnesses"), "turn-runtime-map");
      const blocked = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runner: stateReadingRunner()
      });
      expect(blocked.status).toBe("ENVIRONMENT_CHANGED");
      expect(blocked.environmentFingerprints).toHaveLength(2);
      const digestA = blocked.environmentFingerprints[0]?.digest;
      const digestB = blocked.environmentFingerprints[1]?.digest;
      expect(digestA).toMatch(/^sha256:/);
      expect(digestB).toMatch(/^sha256:/);
      expect(digestA).not.toBe(digestB);

      const result = await investigateTurnTrees({
        repository,
        ledgerPath,
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        image: pinnedImage,
        runtimeMapping: {
          [digestA as string]: imageA,
          [digestB as string]: imageB
        },
        runner: stateReadingRunner()
      });

      expect(result.status).toBe("COMPLETED");
      expect(result.environmentHomogeneity).toBe("HETEROGENEOUS");
      expect(result.runtimeMapping).toEqual({
        [digestA as string]: imageA,
        [digestB as string]: imageB
      });
      expect(result.runs).toHaveLength(6);
      expect(result.runs.filter((run) => run.stateIndex === 0).every((run) => (
        run.environmentFingerprintDigest === digestA && run.sandbox.runtime.image === imageA
      ))).toBe(true);
      expect(result.runs.filter((run) => run.stateIndex === 1).every((run) => (
        run.environmentFingerprintDigest === digestB && run.sandbox.runtime.image === imageB
      ))).toBe(true);
      expect(result.transitions).toHaveLength(1);
      expect(result.proof.isProof).toBe(false);
      expect(result.proof.evidenceGrade).toBe("EXPERIMENTAL_TURN");
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
        role: "TURN",
        treeDigest: "a".repeat(40),
        snapshotDigest: `sha256:${"b".repeat(64)}`,
        dirty: true
      },
      {
        index: 1,
        turnId: "b",
        turnOrdinal: 1,
        role: "TURN",
        treeDigest: "c".repeat(40),
        snapshotDigest: `sha256:${"d".repeat(64)}`,
        dirty: true
      }
    ])).toMatch(/Duplicate turnOrdinal/);
    expect(duplicateTurnOrdinalError([
      {
        index: 0,
        turnId: SESSION_BASELINE_TURN_ID,
        turnOrdinal: 0,
        role: "SESSION_BASELINE",
        treeDigest: "a".repeat(40),
        snapshotDigest: `sha256:${"b".repeat(64)}`,
        dirty: false
      },
      {
        index: 1,
        turnId: "b",
        turnOrdinal: 1,
        role: "TURN",
        treeDigest: "c".repeat(40),
        snapshotDigest: `sha256:${"d".repeat(64)}`,
        dirty: false
      }
    ])).toBeNull();
  });
});
