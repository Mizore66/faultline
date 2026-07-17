import { mkdtempSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { digestJson, sha256 } from "../src/canonical.js";
import {
  appendLifecycleEvent,
  createCodexLifecycleLedger,
  readVerifiedCodexLifecycleLedger,
  writeCodexLifecycleLedgerAtomic
} from "../src/ledger.js";
import type { SandboxCommandRunner } from "../src/sandbox.js";
import { captureTurnTreeSnapshot } from "../src/turn-snapshot.js";
import {
  investigateTurnTrees,
  TurnInvestigationResultSchema,
  type TurnInvestigationResult
} from "../src/turn-investigation.js";
import {
  validateTurnInvestigationProofSemantics,
  verifyTurnInvestigationProofBundle,
  writeTurnInvestigationProofBundle
} from "../src/turn-proof-bundle.js";
import { formatWitnessResult } from "../src/witness-result.js";
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
      repositorySummary: "Temporary turn proof-bundle fixture."
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

function stateReadingRunner(): SandboxCommandRunner {
  return {
    async run(invocation) {
      const state = readFileSync(join(invocation.cwd, "state.txt"), "utf8").trim();
      const overlay = readFileSync(join(invocation.cwd, "witness.mjs"), "utf8");
      if (overlay !== "export const frozenWitness = 'exact-approved-bytes';\n") {
        return { exitCode: 2, stdout: "", stderr: "frozen overlay bytes changed" };
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

/**
 * investigateTurnTrees marks injected runners non-proof. Tests promote a local
 * fixture only after asserting the real API refused injected observations.
 */
function nativeDockerFixture(observed: TurnInvestigationResult): TurnInvestigationResult {
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
      turnId: state.turnId,
      turnOrdinal: state.turnOrdinal,
      role: state.role,
      treeDigest: state.treeDigest,
      snapshotDigest: state.snapshotDigest,
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
  return TurnInvestigationResultSchema.parse({
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
      reason: "Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states.",
      evidenceGrade: "TURN_PROOF",
      evidenceLabel: "Turn localization — portable proof bundle eligible"
    }
  });
}

function collectArtifacts(root: string, current = root): string[] {
  const artifacts: string[] = [];
  for (const entry of readdirSync(current, { withFileTypes: true })) {
    const path = join(current, entry.name);
    if (entry.isDirectory()) artifacts.push(...collectArtifacts(root, path));
    else if (entry.isFile() && entry.name !== "hashes.txt" && entry.name !== "ROOT.sha256") {
      artifacts.push(relative(root, path).replaceAll("\\", "/"));
    }
  }
  return artifacts;
}

function rehashWholeBundle(root: string): void {
  const hashes = `${collectArtifacts(root)
    .sort((left, right) => left.localeCompare(right))
    .map((artifact) => `${sha256(readFileSync(join(root, artifact)))}  ${artifact}`)
    .join("\n")}\n`;
  writeFileSync(join(root, "hashes.txt"), hashes, "utf8");
  writeFileSync(join(root, "ROOT.sha256"), `sha256:${sha256(hashes)}\n`, "utf8");
}

async function investigateBaselinePassFail(root: string): Promise<{
  repository: string;
  ledgerPath: string;
  frozen: FrozenWitness;
  observed: TurnInvestigationResult;
}> {
  const repository = join(root, "repo");
  git(root, ["init", "repo"]);
  git(repository, ["config", "user.email", "faultline@example.test"]);
  git(repository, ["config", "user.name", "FaultLine"]);
  writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", "good"]);

  let ledger = createCodexLifecycleLedger({ sessionId: "turn-proof-session" });
  ledger = appendLifecycleEvent(ledger, {
    type: "SESSION_STARTED",
    payload: { transport: "SIDE_CAR", workingDirectory: repository }
  });
  ledger = appendBaselineSnapshot(ledger, repository);
  writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
  ledger = appendTurnSnapshot(ledger, repository, 1, "turn-1");
  const ledgerPath = join(root, "ledger.json");
  writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);

  const frozen = createFrozenWitness(join(root, "witnesses"), "turn-proof");
  const observed = await investigateTurnTrees({
    repository,
    ledgerPath,
    frozenWitness: frozen,
    expectedFrozenDigest: frozen.frozenDigest,
    image: pinnedImage,
    runner: stateReadingRunner()
  });
  return { repository, ledgerPath, frozen, observed };
}

describe("portable turn investigation proof bundles", () => {
  it("rejects injected-runner observations and packages a native-docker fixture with ledger + tree pack", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-proof-root-"));
    try {
      const { repository, ledgerPath, frozen, observed } = await investigateBaselinePassFail(root);
      expect(observed.proof).toMatchObject({
        executionTrust: "INJECTED_RUNNER",
        isProof: false,
        evidenceGrade: "EXPERIMENTAL_TURN"
      });
      expect(observed.runs).toHaveLength(6);
      await expect(writeTurnInvestigationProofBundle(join(root, "proofs", "rejected-injected"), observed, frozen, {
        proofRoot: join(root, "proofs"),
        lifecycleLedger: readVerifiedCodexLifecycleLedger(ledgerPath),
        repository
      })).rejects.toThrow(/native Docker executor provenance/);

      const result = nativeDockerFixture(observed);
      expect(result).toMatchObject({
        status: "COMPLETED",
        proof: { isProof: true, dockerIsolated: true, proofTransitions: 1, evidenceGrade: "TURN_PROOF" }
      });
      expect(validateTurnInvestigationProofSemantics(result, frozen)).toEqual([]);

      const output = join(root, "proofs", "portable-turns");
      const written = await writeTurnInvestigationProofBundle(output, result, frozen, {
        proofRoot: join(root, "proofs"),
        generatedAt: "2026-07-18T02:00:00.000Z",
        lifecycleLedger: readVerifiedCodexLifecycleLedger(ledgerPath),
        repository
      });
      const verified = await verifyTurnInvestigationProofBundle(written.directory, written.rootDigest);

      expect(verified).toMatchObject({ valid: true, externalRootStatus: "MATCH", rootDigest: written.rootDigest });
      expect(verified.checkedFiles).toBeGreaterThan(10);
      expect(verified.manifest?.lifecycle).toMatchObject({ status: "BOUND", transport: "SIDE_CAR" });
      expect(readFileSync(join(output, "lifecycle", "ledger.json"), "utf8")).toContain("SESSION_BASELINE_SNAPSHOT");
      expect(readFileSync(join(output, "source", "trees.pack")).length).toBeGreaterThan(32);
      expect(readFileSync(join(output, "VERIFY.md"), "utf8")).toContain("turn investigation package");
      await expect(writeTurnInvestigationProofBundle(output, result, frozen, {
        proofRoot: join(root, "proofs"),
        lifecycleLedger: readVerifiedCodexLifecycleLedger(ledgerPath),
        repository
      })).rejects.toThrow(/already exists and will not be replaced/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("rejects a rehashed semantic contradiction instead of trusting the catalog or rewritten root", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-proof-tamper-"));
    try {
      const { repository, ledgerPath, frozen, observed } = await investigateBaselinePassFail(root);
      const result = nativeDockerFixture(observed);
      const output = join(root, "proofs", "tamper-turns");
      await writeTurnInvestigationProofBundle(output, result, frozen, {
        proofRoot: join(root, "proofs"),
        generatedAt: "2026-07-18T02:00:00.000Z",
        lifecycleLedger: readVerifiedCodexLifecycleLedger(ledgerPath),
        repository
      });

      const investigationPath = join(output, "investigation.json");
      const investigation = JSON.parse(readFileSync(investigationPath, "utf8")) as {
        runs: Array<Record<string, unknown>>;
        stableStates: unknown;
      };
      const target = investigation.runs.find((run) => run.stateIndex === 1 && run.executionAttempt === 1);
      if (!target) throw new Error("test fixture did not generate the intended failing-state run");
      const oldRunId = String(target.runId);
      const resultFact = target.result as Record<string, unknown>;
      target.result = { ...resultFact, verdict: "PASS", reason: "PREDICATE_PASS", exitCode: 0, signal: null };
      const { runId: _oldRunId, ...unsignedRun } = target;
      target.runId = digestJson(unsignedRun);
      const newRunId = String(target.runId);

      const oldRunPath = join(output, "runs", `${oldRunId.slice("sha256:".length)}.json`);
      const newRunPath = join(output, "runs", `${newRunId.slice("sha256:".length)}.json`);
      writeFileSync(oldRunPath, `${JSON.stringify(target)}\n`, "utf8");
      renameSync(oldRunPath, newRunPath);
      writeFileSync(investigationPath, `${JSON.stringify(investigation)}\n`, "utf8");

      const manifestPath = join(output, "manifest.json");
      const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as {
        investigationDigest: string;
        artifacts: { runs: Array<{ runId: string; path: string }> };
      };
      manifest.investigationDigest = digestJson(investigation);
      manifest.artifacts.runs = manifest.artifacts.runs.map((entry) => (
        entry.runId === oldRunId
          ? { runId: newRunId, path: `runs/${newRunId.slice("sha256:".length)}.json` }
          : entry
      ));
      writeFileSync(manifestPath, `${JSON.stringify(manifest)}\n`, "utf8");
      rehashWholeBundle(output);

      const verified = await verifyTurnInvestigationProofBundle(output);
      expect(verified.valid).toBe(false);
      expect(verified.errors.some((error) => /stable states|transitions|proof/i.test(error))).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
