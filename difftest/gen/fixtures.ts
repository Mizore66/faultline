// Test fixtures copied from tests/git-proof-bundle.test.ts:32-204 and
// tests/prevention-proof.test.ts:15-62 (frozen TS), exported for difftest generators.
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

export function createFrozenWitness(store: string, extraOverlays: ExtraOverlay[] = []): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId: "portable-git-proof",
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
