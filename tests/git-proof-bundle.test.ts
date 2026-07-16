import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";
import { digestJson, sha256 } from "../src/canonical.js";
import {
  GitInvestigationResultSchema,
  investigateGitRange,
  type GitInvestigationRequest,
  type GitInvestigationResult
} from "../src/git-investigation.js";
import {
  verifyGitInvestigationProofBundle,
  writeGitInvestigationProofBundle
} from "../src/git-proof-bundle.js";
import type { SandboxCommandRunner } from "../src/sandbox.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../src/witness-lock.js";
import {
  appendLifecycleEvent,
  captureGitCleanCheckpoint,
  createCodexLifecycleLedger,
  type CodexLifecycleLedger
} from "../src/ledger.js";

const pinnedImage = `registry.example/faultline-node@sha256:${"b".repeat(64)}`;

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function commit(repository: string, state: string, message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

function createRepository(): { root: string; ancestor: string; descendant: string } {
  const root = mkdtempSync(join(tmpdir(), "faultline-git-proof-repository-"));
  git(root, ["init"]);
  git(root, ["config", "user.email", "faultline@example.test"]);
  git(root, ["config", "user.name", "FaultLine Test"]);
  const ancestor = commit(root, "good", "known good");
  commit(root, "bad", "regression");
  const descendant = commit(root, "repaired", "repair");
  return { root, ancestor, descendant };
}

function createFrozenWitness(store: string): FrozenWitness {
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
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: "2026-07-16T11:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T11:02:00.000Z" });
}

function deterministicDockerRunner(): SandboxCommandRunner {
  return {
    async run(invocation) {
      const state = readFileSync(join(invocation.cwd, "state.txt"), "utf8").trim();
      const overlay = readFileSync(join(invocation.cwd, "witness.mjs"), "utf8");
      if (overlay !== "export const faultLineWitness = 'approved-exact-bytes';\n") {
        return { exitCode: 2, stdout: "", stderr: "approved overlay bytes changed" };
      }
      return state === "bad"
        ? { exitCode: 1, stdout: `state=${state}\n`, stderr: "witness failed" }
        : { exitCode: 0, stdout: `state=${state}\n`, stderr: "" };
    }
  };
}

/**
 * The bundle verifier needs a native-Docker-shaped serialized fixture, while
 * `investigateGitRange` intentionally marks injected runners non-proof. This
 * promotes only a local test fixture after the test has asserted that the
 * real API rejected the injected observation as proof.
 */
function nativeDockerFixture(observed: GitInvestigationResult): GitInvestigationResult {
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
      reason: "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states."
    }
  });
}

function lifecycleBoundToDescendant(repository: string): CodexLifecycleLedger {
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

/** Simulate an editor who updates every mutable checksum after changing a run. */
function rehashWholeBundle(root: string): void {
  const hashes = `${collectArtifacts(root)
    .sort((left, right) => left.localeCompare(right))
    .map((artifact) => `${sha256(readFileSync(join(root, artifact)))}  ${artifact}`)
    .join("\n")}\n`;
  writeFileSync(join(root, "hashes.txt"), hashes, "utf8");
  writeFileSync(join(root, "ROOT.sha256"), `sha256:${sha256(hashes)}\n`, "utf8");
}

describe("portable Git investigation proof bundles", () => {
  it("packages and independently verifies a real Git range, frozen witness, raw runs, source bundle, and binary patch", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-git-proof-root-"));
    const store = join(root, "witness-lock");
    const repository = createRepository();
    try {
      const frozen = createFrozenWitness(store);
      const request: GitInvestigationRequest = {
        repository: repository.root,
        range: { ancestor: "HEAD~2", descendant: "HEAD" },
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
        runner: deterministicDockerRunner()
      };
      const observed = await investigateGitRange(request);
      expect(observed.proof).toMatchObject({ executionTrust: "INJECTED_RUNNER", isProof: false });
      expect(() => writeGitInvestigationProofBundle(join(root, "proofs", "rejected-injected"), observed, frozen, {
        proofRoot: join(root, "proofs")
      })).toThrow(/native Docker executor provenance/);
      const result = nativeDockerFixture(observed);
      expect(result).toMatchObject({
        status: "COMPLETED",
        proof: { isProof: true, dockerIsolated: true, proofTransitions: 2 }
      });

      const output = join(root, "proofs", "portable-range");
      const lifecycleLedger = lifecycleBoundToDescendant(repository.root);
      const written = writeGitInvestigationProofBundle(output, result, frozen, {
        proofRoot: join(root, "proofs"),
        generatedAt: "2026-07-16T11:03:00.000Z",
        lifecycleLedger
      });
      const verified = verifyGitInvestigationProofBundle(written.directory, written.rootDigest);

      expect(verified).toMatchObject({ valid: true, externalRootStatus: "MATCH", rootDigest: written.rootDigest });
      expect(verified.checkedFiles).toBeGreaterThan(12);
      expect(verified.manifest?.lifecycle).toMatchObject({ status: "BOUND", transport: "SIDE_CAR" });
      expect(readFileSync(join(output, "lifecycle", "ledger.json"), "utf8")).toContain("SIDE_CAR");
      expect(readFileSync(join(output, "source", "descendant.bundle")).subarray(0, 16).toString("utf8")).toMatch(/# v[23] git bundle/);
      expect(readFileSync(join(output, "source", "range.patch"), "utf8")).toContain("state.txt");
      expect(() => writeGitInvestigationProofBundle(output, result, frozen, { proofRoot: join(root, "proofs") }))
        .toThrow(/already exists and will not be replaced/);
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("rejects a rehashed semantic contradiction instead of trusting the catalog or rewritten root", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-git-proof-tamper-root-"));
    const store = join(root, "witness-lock");
    const repository = createRepository();
    try {
      const frozen = createFrozenWitness(store);
      const observed = await investigateGitRange({
        repository: repository.root,
        range: { ancestor: "HEAD~2", descendant: "HEAD" },
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
        runner: deterministicDockerRunner()
      });
      const result = nativeDockerFixture(observed);
      const output = join(root, "proofs", "tamper-range");
      writeGitInvestigationProofBundle(output, result, frozen, {
        proofRoot: join(root, "proofs"),
        generatedAt: "2026-07-16T11:03:00.000Z"
      });

      const investigationPath = join(output, "investigation.json");
      const investigation = JSON.parse(readFileSync(investigationPath, "utf8")) as {
        runs: Array<Record<string, unknown>>;
        stableStates: unknown;
      };
      const target = investigation.runs.find((run) => run.stateIndex === 1 && run.executionAttempt === 1);
      if (!target) throw new Error("test fixture did not generate the intended bad-state run");
      const oldRunId = String(target.runId);
      const resultFact = target.result as Record<string, unknown>;
      target.result = { ...resultFact, verdict: "PASS", reason: "EXIT_ZERO", exitCode: 0, signal: null };
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
      const descriptor = manifest.artifacts.runs.find((run) => run.runId === oldRunId);
      if (!descriptor) throw new Error("test fixture did not find the raw run descriptor");
      descriptor.runId = newRunId;
      descriptor.path = `runs/${newRunId.slice("sha256:".length)}.json`;
      writeFileSync(manifestPath, `${JSON.stringify(manifest)}\n`, "utf8");
      rehashWholeBundle(output);

      const verified = verifyGitInvestigationProofBundle(output);
      expect(verified.valid).toBe(false);
      expect(verified.externalRootStatus).toBe("NOT_PROVIDED");
      expect(verified.errors.join("\n")).toMatch(/stable states contradict the reconstructed run facts/);
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("reconstructs every recorded state from the bundled Git ancestry path", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-git-proof-state-path-root-"));
    const store = join(root, "witness-lock");
    const repository = createRepository();
    try {
      const frozen = createFrozenWitness(store);
      const observed = await investigateGitRange({
        repository: repository.root,
        range: { ancestor: "HEAD~2", descendant: "HEAD" },
        frozenWitness: frozen,
        expectedFrozenDigest: frozen.frozenDigest,
        sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
        runner: deterministicDockerRunner()
      });
      const result = nativeDockerFixture(observed);
      const output = join(root, "proofs", "state-path-range");
      writeGitInvestigationProofBundle(output, result, frozen, {
        proofRoot: join(root, "proofs"),
        generatedAt: "2026-07-16T11:03:00.000Z"
      });

      const investigationPath = join(output, "investigation.json");
      const investigation = JSON.parse(readFileSync(investigationPath, "utf8")) as {
        states: Array<{ index: number; commit: string; tree: string }>;
      };
      const middle = investigation.states[1];
      if (!middle) throw new Error("test fixture did not produce a middle state");
      middle.commit = "f".repeat(40);
      writeFileSync(investigationPath, `${JSON.stringify(investigation)}\n`, "utf8");
      const manifestPath = join(output, "manifest.json");
      const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as { investigationDigest: string };
      manifest.investigationDigest = digestJson(investigation);
      writeFileSync(manifestPath, `${JSON.stringify(manifest)}\n`, "utf8");
      rehashWholeBundle(output);

      const verified = verifyGitInvestigationProofBundle(output);
      expect(verified.valid).toBe(false);
      expect(verified.errors.join("\n")).toMatch(/state sequence does not exactly match the bundled ancestor-to-descendant Git path/);
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });
});
