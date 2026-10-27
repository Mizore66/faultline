// Test fixtures copied from tests/git-proof-bundle.test.ts:32-204,
// tests/prevention-proof.test.ts:15-62 and tests/turn-proof-bundle.test.ts:114-159
// (frozen TS), exported for difftest generators.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { digestJson, sha256 } from "../../src/canonical.js";
import {
  GitInvestigationResultSchema,
  type GitInvestigationResult
} from "../../src/git-investigation.js";
import type { SandboxCommandRunner } from "../../src/sandbox.js";
import { formatWitnessResult } from "../../src/witness-result.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../../src/witness-lock.js";
import {
  appendLifecycleEvent,
  captureGitCleanCheckpoint,
  createCodexLifecycleLedger,
  type CodexLifecycleLedger
} from "../../src/ledger.js";
import type { PreventionProofWriteInput } from "../../src/prevention-proof.js";
import { TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL } from "../../src/evidence-grade.js";
import {
  attributeFailureIntroduction,
  findStableTurnTransitions,
  reconstructStableTurnStates,
  TURN_INVESTIGATION_SCHEMA_VERSION,
  TurnInvestigationResultSchema,
  type TurnInvestigationResult
} from "../../src/turn-investigation.js";
import { signTurnTreeSnapshot, TURN_TREE_SNAPSHOT_VERSION } from "../../src/turn-snapshot.js";

export const pinnedImage = `registry.example/faultline-node@sha256:${"b".repeat(64)}`;

export function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

export function commit(repository: string, state: string, message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

export function createRepository(objectFormat: "sha1" | "sha256" = "sha1"): { root: string; ancestor: string; descendant: string } {
  const root = mkdtempSync(join(tmpdir(), "faultline-git-proof-repository-"));
  git(root, ["init", `--object-format=${objectFormat}`]);
  git(root, ["config", "user.email", "faultline@example.test"]);
  git(root, ["config", "user.name", "FaultLine Test"]);
  const ancestor = commit(root, "good", "known good");
  commit(root, "bad", "regression");
  const descendant = commit(root, "repaired", "repair");
  return { root, ancestor, descendant };
}

export type ExtraOverlay = { path: string; text: string };

export function createFrozenWitness(
  store: string, extraOverlays: ExtraOverlay[] = [], proposalId = "portable-git-proof"
): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T11:00:00.000Z",
    incidentPacket: {
      symptom: "The repository state must not be bad.",
      ciLog: "state.txt became bad",
      repositoryLanguage: "Text",
      repositorySummary: "Real temporary Git repository for a portable FaultLine evidence package."
    },
    witness: {
      behavior: "state.txt must not be bad",
      command: "node witness.mjs",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from("export const faultLineWitness = 'approved-exact-bytes';\n", "utf8").toString("base64")
      }, ...extraOverlays.map((overlay) => ({ path: overlay.path, bytesBase64: Buffer.from(overlay.text, "utf8").toString("base64") }))],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: "2026-07-16T11:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T11:02:00.000Z" });
}

export function deterministicDockerRunner(): SandboxCommandRunner {
  return {
    async run(invocation) {
      const state = readFileSync(join(invocation.cwd, "state.txt"), "utf8").trim();
      const overlay = readFileSync(join(invocation.cwd, "witness.mjs"), "utf8");
      if (overlay !== "export const faultLineWitness = 'approved-exact-bytes';\n") {
        return { exitCode: 2, stdout: "", stderr: "approved overlay bytes changed" };
      }
      return state === "bad"
        ? { exitCode: 1, stdout: `state=${state}\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "witness failed" }
        : { exitCode: 0, stdout: `state=${state}\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
    }
  };
}

/**
 * The bundle verifier needs a native-Docker-shaped serialized fixture, while
 * `investigateGitRange` intentionally marks injected runners non-proof. This
 * promotes only a local test fixture after the test has asserted that the
 * real API rejected the injected observation as proof.
 */
export function nativeDockerFixture(observed: GitInvestigationResult): GitInvestigationResult {
  const runs = observed.runs.map((run) => {
    const { runId: _runId, ...unsigned } = {
      ...run,
      result: { ...run.result, executor: "NATIVE_DOCKER" as const }
    };
    return { ...unsigned, runId: digestJson(unsigned) };
  });
  const stableStates = observed.states.map((state) => {
    const stateRuns = runs.filter((run) => run.stateIndex === state.index)
      .sort((left, right) => left.executionAttempt - right.executionAttempt);
    const verdict = stateRuns[0]?.result.verdict;
    if (verdict !== "PASS" && verdict !== "FAIL") throw new Error("fixture did not create a decisive state");
    return {
      stateIndex: state.index,
      commit: state.commit,
      tree: state.tree,
      verdict,
      executionIds: stateRuns.map((run) => run.executionId),
      runIds: stateRuns.map((run) => run.runId)
    };
  });
  const transitions = stableStates.slice(1).flatMap((after, index) => {
    const before = stableStates[index];
    if (!before || before.verdict === after.verdict) return [];
    return [{ kind: before.verdict === "PASS" ? "PASS_TO_FAIL" as const : "FAIL_TO_PASS" as const, before, after }];
  });
  return GitInvestigationResultSchema.parse({
    ...observed,
    runs,
    stableStates,
    transitions,
    nonMonotonic: transitions.some((transition) => transition.kind === "PASS_TO_FAIL")
      && transitions.some((transition) => transition.kind === "FAIL_TO_PASS"),
    proof: {
      requiresDockerIsolation: true,
      dockerIsolated: true,
      executionTrust: "NATIVE_DOCKER",
      proofTransitions: transitions.length,
      isProof: transitions.length > 0,
      reason: "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states.",
      evidenceGrade: transitions.length > 0 ? "COMMIT_PROOF" : "NONE",
      evidenceLabel: transitions.length > 0
        ? "Proven at commit granularity — portable and offline-verifiable"
        : "Commit-path localization — not certified as portable proof"
    }
  });
}

export function lifecycleBoundToDescendant(repository: string): CodexLifecycleLedger {
  let ledger = createCodexLifecycleLedger({ sessionId: "portable-proof-session" });
  ledger = appendLifecycleEvent(ledger, {
    type: "SESSION_STARTED",
    payload: { transport: "SIDE_CAR", workingDirectory: repository, model: "gpt-5.6" }
  });
  ledger = appendLifecycleEvent(ledger, {
    type: "TURN_STARTED",
    payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: `sha256:${"c".repeat(64)}` }
  });
  ledger = appendLifecycleEvent(ledger, {
    type: "TURN_COMPLETED",
    payload: { turnId: "turn-1", turnOrdinal: 1, outcome: "COMPLETED", outputDigest: `sha256:${"d".repeat(64)}` }
  });
  ledger = appendLifecycleEvent(ledger, {
    type: "WORKTREE_CHECKPOINT",
    payload: { checkpoint: captureGitCleanCheckpoint(repository), afterTurnOrdinal: 1 }
  });
  return appendLifecycleEvent(ledger, {
    type: "SESSION_ENDED",
    payload: { reason: "COMPLETED", completedTurns: 1 }
  });
}

export function lifecycleBoundToEveryInvestigatedState(repository: string, commits: readonly string[]): CodexLifecycleLedger {
  let ledger = createCodexLifecycleLedger({ sessionId: "portable-proof-full-session" });
  ledger = appendLifecycleEvent(ledger, {
    type: "SESSION_STARTED",
    payload: { transport: "SIDE_CAR", workingDirectory: repository, model: "gpt-5.6" }
  });
  for (const [offset, commit] of commits.entries()) {
    git(repository, ["checkout", "--detach", commit]);
    const turnOrdinal = offset + 1;
    ledger = appendLifecycleEvent(ledger, {
      type: "TURN_STARTED",
      payload: { turnId: `full-turn-${turnOrdinal}`, turnOrdinal, promptDigest: `sha256:${"e".repeat(64)}` }
    });
    ledger = appendLifecycleEvent(ledger, {
      type: "TURN_COMPLETED",
      payload: { turnId: `full-turn-${turnOrdinal}`, turnOrdinal, outcome: "COMPLETED" }
    });
    ledger = appendLifecycleEvent(ledger, {
      type: "WORKTREE_CHECKPOINT",
      payload: { checkpoint: captureGitCleanCheckpoint(repository), afterTurnOrdinal: turnOrdinal }
    });
  }
  return appendLifecycleEvent(ledger, {
    type: "SESSION_ENDED",
    payload: { reason: "COMPLETED", completedTurns: commits.length }
  });
}

export const digest = (label: string) => `sha256:${sha256(label)}`;
export const hexCommit = (n: number) => `${n.toString(16).padStart(40, "0")}`;

export function runIds(prefix: string): [string, string, string] {
  return [digest(`${prefix}-1`), digest(`${prefix}-2`), digest(`${prefix}-3`)];
}

export function sampleInput(overrides: Partial<PreventionProofWriteInput> = {}): PreventionProofWriteInput {
  const witness = digest("witness");
  const environment = digest("environment");
  return {
    originalProofRoot: digest("proof-root"),
    frozenWitnessDigest: witness,
    investigationDigest: digest("investigation"),
    lastGood: {
      commit: hexCommit(1),
      tree: hexCommit(11),
      witnessDigest: witness,
      environmentDigest: environment,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED",
      distinctExecutionCount: 3,
      runIds: runIds("lg")
    },
    firstBad: {
      commit: hexCommit(2),
      tree: hexCommit(22),
      witnessDigest: witness,
      environmentDigest: environment,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED",
      distinctExecutionCount: 3,
      runIds: runIds("fb")
    },
    repaired: {
      commit: hexCommit(3),
      tree: hexCommit(33),
      witnessDigest: witness,
      environmentDigest: environment,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED",
      distinctExecutionCount: 3,
      runIds: runIds("rp")
    },
    repairPatchDigest: digest("patch"),
    codexThreadId: "thread_prevention_test",
    ...overrides
  };
}

/** The grounded input from tests/prevention-proof.test.ts:104-130. */
export function groundedPreventionInput(): PreventionProofWriteInput {
  const ids = runIds("rp");
  const witness = digest("witness");
  const environment = digest("environment");
  return sampleInput({
    grounding: "VERIFIED",
    repairBaseTree: hexCommit(22),
    repairPatchDigest: digest("repair-patch"),
    repaired: {
      commit: hexCommit(3), tree: hexCommit(33), witnessDigest: witness, environmentDigest: environment,
      executionTrust: "NATIVE_DOCKER", executionKind: "EXECUTED", distinctExecutionCount: 3, runIds: ids
    },
    repairedRunBindings: ids.map((runId, index) => ({
      runId, executionId: digest(`exec-${index}`), commit: hexCommit(3), tree: hexCommit(33), verdict: "PASS" as const,
      witnessDigest: witness, environmentDigest: environment, executionTrust: "NATIVE_DOCKER" as const, executionKind: "EXECUTED" as const
    }))
  });
}

// The turn proof bases (gen-bases.ts turnBase). Everything investigateTurnTrees
// reads from the clock or the host is fixed, so a rerun writes the same bytes;
// only the tree pack follows git (plan correction 8).

/** The repository path the turn bases record; the writer packs from the real one. */
export const TURN_REPOSITORY = "/work/turn-repository";

/**
 * createFrozenWitness's witness under its own proposal id: the overlay bytes
 * stay the ones deterministicDockerRunner checks.
 */
export function createTurnFrozenWitness(store: string): FrozenWitness {
  return createFrozenWitness(store, [], "portable-turn-proof");
}

/** One commit per state ("good", "bad", ...): state.txt holds the state. */
export function createTurnRepository(states: readonly string[]): { root: string; commits: string[]; trees: string[] } {
  const root = mkdtempSync(join(tmpdir(), "faultline-turn-proof-repository-"));
  git(root, ["init", "--object-format=sha1"]);
  git(root, ["config", "user.email", "faultline@example.test"]);
  git(root, ["config", "user.name", "FaultLine Test"]);
  const commits = states.map((state, index) => commit(root, state, `turn state ${index}: ${state}`));
  return { root, commits, trees: commits.map((c) => git(root, ["rev-parse", `${c}^{tree}`])) };
}

export type TurnLedgerState = { ordinal: number; tree: string; dirty: boolean; commit?: string };

/**
 * A closed Codex session with fixed ids and times: SESSION_STARTED, the
 * baseline snapshot (states[0], ordinal 0) when baseline is set, then
 * TURN_STARTED, TURN_COMPLETED and TURN_TREE_SNAPSHOT for each other state,
 * then SESSION_ENDED. Every event is one second after the one before; a
 * snapshot is captured when its event occurs.
 */
export function turnLedger(options: { states: readonly TurnLedgerState[]; baseline: boolean }): CodexLifecycleLedger {
  let second = 0;
  let sequence = 0;
  const at = () => new Date(Date.UTC(2026, 6, 16, 10, 30, second++)).toISOString();
  const createdAt = at();
  let ledger = createCodexLifecycleLedger({ ledgerId: "ledger-turn-proof", sessionId: "turn-proof-session", createdAt });
  const append = (input: Parameters<typeof appendLifecycleEvent>[1], occurredAt = at()) => {
    sequence += 1;
    ledger = appendLifecycleEvent(ledger, input, { eventId: `event-${String(sequence).padStart(4, "0")}`, occurredAt });
  };
  const snapshot = (state: TurnLedgerState, capturedAt: string) => signTurnTreeSnapshot({
    schemaVersion: TURN_TREE_SNAPSHOT_VERSION,
    repositoryRoot: TURN_REPOSITORY,
    headCommit: state.commit ?? state.tree,
    treeDigest: state.tree,
    capturedAt,
    dirty: state.dirty,
    statusDigest: `sha256:${sha256(state.dirty ? "dirty" : "")}`
  });
  append({ type: "SESSION_STARTED", payload: { transport: "SIDE_CAR", workingDirectory: TURN_REPOSITORY } });
  const [first, ...rest] = options.states;
  const turns = options.baseline ? rest : [...options.states];
  if (options.baseline && first) {
    const occurredAt = at();
    append({ type: "SESSION_BASELINE_SNAPSHOT", payload: { snapshot: snapshot(first, occurredAt) } }, occurredAt);
  }
  for (const state of turns) {
    const turnId = `turn-${state.ordinal}`;
    append({ type: "TURN_STARTED", payload: { turnId, turnOrdinal: state.ordinal, promptDigest: `sha256:${"a".repeat(64)}` } });
    append({ type: "TURN_COMPLETED", payload: { turnId, turnOrdinal: state.ordinal, outcome: "COMPLETED" } });
    const occurredAt = at();
    append({ type: "TURN_TREE_SNAPSHOT", payload: { turnId, turnOrdinal: state.ordinal, snapshot: snapshot(state, occurredAt) } }, occurredAt);
  }
  append({ type: "SESSION_ENDED", payload: { reason: "COMPLETED", completedTurns: turns.length } });
  return ledger;
}

/**
 * The turn twin of nativeDockerFixture: investigateTurnTrees marks injected
 * runners non-proof, so a base promotes its observation to native Docker,
 * after replacing what the investigation read from the clock (nonces and
 * times) and the scratch repository path with fixed values, and rebuilding
 * the derived fields with the frozen TS reconstruction.
 */
export function nativeTurnFixture(observed: TurnInvestigationResult): TurnInvestigationResult {
  const runs = observed.runs.map((run, i) => {
    const executionNonce = `00000000-0000-4000-8000-${i.toString(16).padStart(12, "0")}`;
    // turn-investigation.ts:544-555
    const executionId = digestJson({
      schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
      executionNonce,
      stateIndex: run.stateIndex,
      turnId: run.turnId,
      turnOrdinal: run.turnOrdinal,
      treeDigest: run.treeDigest,
      snapshotDigest: run.snapshotDigest,
      environmentFingerprintDigest: run.environmentFingerprintDigest,
      frozenDigest: run.frozenDigest,
      executionAttempt: run.executionAttempt
    });
    const { runId: _runId, ...unsigned } = {
      ...run,
      executionId,
      executionNonce,
      startedAt: "2026-07-16T11:03:00.000Z",
      finishedAt: "2026-07-16T11:03:00.000Z",
      durationMs: 0,
      result: { ...run.result, executor: "NATIVE_DOCKER" as const }
    };
    return { ...unsigned, runId: digestJson(unsigned) };
  });
  const stableStates = reconstructStableTurnStates(observed.states, runs);
  const transitions = findStableTurnTransitions(stableStates);
  return TurnInvestigationResultSchema.parse({
    ...observed,
    repository: TURN_REPOSITORY,
    runs,
    stableStates,
    transitions,
    nonMonotonic: transitions.some((transition) => transition.kind === "PASS_TO_FAIL")
      && transitions.some((transition) => transition.kind === "FAIL_TO_PASS"),
    introduction: attributeFailureIntroduction(observed.states, transitions, stableStates),
    proof: {
      requiresDockerIsolation: true,
      dockerIsolated: true,
      executionTrust: "NATIVE_DOCKER",
      proofTransitions: transitions.length,
      isProof: transitions.length > 0,
      reason: "Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states.",
      evidenceGrade: "EXPERIMENTAL_TURN",
      evidenceLabel: TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL
    }
  });
}
