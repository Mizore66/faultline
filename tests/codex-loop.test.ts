import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { digestJson } from "../src/canonical.js";
import {
  repairWithCodex,
  selectRepairBaseCommit
} from "../src/codex-loop.js";
import {
  GitInvestigationResultSchema,
  investigateGitRange,
  type GitInvestigationRequest,
  type GitInvestigationResult
} from "../src/git-investigation.js";
import {
  writeGitInvestigationProofBundle
} from "../src/git-proof-bundle.js";
import {
  collectRepairPatch,
  createRepairWorktree,
  removeRepairWorktree
} from "../src/repair-worktree.js";
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

function createFrozenWitness(store: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId: "repair-loop-witness",
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-17T12:00:00.000Z",
    incidentPacket: {
      symptom: "state.txt became bad",
      ciLog: "fixture",
      repositoryLanguage: "Text",
      repositorySummary: "repair worktree fixture"
    },
    witness: {
      behavior: "state.txt must remain good",
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

function deterministicDockerRunner() {
  return {
    async run(invocation: { cwd: string }) {
      const state = readFileSync(join(invocation.cwd, "state.txt"), "utf8").trim();
      return state === "bad"
        ? { exitCode: 1, stdout: "FAIL\n", stderr: "bad" }
        : { exitCode: 0, stdout: "PASS\n", stderr: "" };
    }
  };
}

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
    if (verdict !== "PASS" && verdict !== "FAIL") throw new Error("fixture missing decisive state");
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
    nonMonotonic: transitions.some((t) => t.kind === "PASS_TO_FAIL") && transitions.some((t) => t.kind === "FAIL_TO_PASS"),
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

async function createProofFixture(root: string): Promise<{
  repository: string;
  bundleDirectory: string;
  rootDigest: string;
  investigation: GitInvestigationResult;
  frozen: FrozenWitness;
}> {
  const repository = join(root, "repo");
  git(root, ["init", "repo"]);
  git(repository, ["config", "user.email", "faultline@example.test"]);
  git(repository, ["config", "user.name", "FaultLine"]);
  writeFileSync(join(repository, "state.txt"), "good\n", "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", "good"]);
  const ancestor = git(repository, ["rev-parse", "HEAD"]);
  writeFileSync(join(repository, "state.txt"), "bad\n", "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", "bad"]);
  const descendant = git(repository, ["rev-parse", "HEAD"]);
  const frozen = createFrozenWitness(join(root, "witnesses"));
  const request: GitInvestigationRequest = {
    repository,
    range: { ancestor, descendant },
    frozenWitness: frozen,
    expectedFrozenDigest: frozen.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
    runner: deterministicDockerRunner()
  };
  const observed = await investigateGitRange(request);
  const investigation = nativeDockerFixture(observed);
  const written = writeGitInvestigationProofBundle(join(root, "proofs", "range"), investigation, frozen, {
    proofRoot: join(root, "proofs"),
    generatedAt: "2026-07-18T03:00:00.000Z"
  });
  return {
    repository,
    bundleDirectory: written.directory,
    rootDigest: written.rootDigest,
    investigation,
    frozen
  };
}

describe("repair worktree isolation", () => {
  it("creates a detached worktree, copies proof read-only, and cleans up without leaving registrations", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-repair-wt-"));
    try {
      const fixture = await createProofFixture(root);
      const base = selectRepairBaseCommit(fixture.investigation);
      const session = await createRepairWorktree({
        repository: fixture.repository,
        baseCommit: base,
        bundleDirectory: fixture.bundleDirectory,
        outputDirectory: join(root, "managed-repair")
      });
      expect(existsSync(session.worktreePath)).toBe(true);
      expect(existsSync(join(session.proofReadonlyPath, "investigation.json"))).toBe(true);
      expect(existsSync(join(session.worktreePath, ".faultline-repair", "README.md"))).toBe(true);
      expect(git(session.worktreePath, ["rev-parse", "HEAD"])).toBe(base);

      writeFileSync(join(session.worktreePath, "state.txt"), "repaired\n", "utf8");
      const patch = await collectRepairPatch(session.worktreePath);
      expect(patch.bytes).toBeGreaterThan(0);
      expect(patch.patch).toContain("state.txt");

      const cleanup = await removeRepairWorktree({
        repository: session.repository,
        worktreePath: session.worktreePath,
        managedRoot: session.managedRoot,
        keepManagedRoot: true
      });
      expect(cleanup.cleaned).toBe(true);
      expect(existsSync(session.worktreePath)).toBe(false);
      const listed = spawnSync("git", ["-C", fixture.repository, "worktree", "list", "--porcelain"], { encoding: "utf8" });
      expect(listed.stdout).not.toContain("managed-repair");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("returns REPAIR_WORKTREE_READY for a verified bundle and keeps instructions after cleanup", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-repair-ready-"));
    try {
      const fixture = await createProofFixture(root);
      const result = await repairWithCodex({
        bundleDirectory: fixture.bundleDirectory,
        expectRoot: fixture.rootDigest,
        repository: fixture.repository,
        outputDirectory: join(root, "repair-out"),
        verifyBundle: () => ({ valid: true, errors: [], rootDigest: fixture.rootDigest })
      });
      expect(result.status).toBe("REPAIR_WORKTREE_READY");
      expect(result.worktreePath).toBeNull();
      expect(result.baseCommit).toBe(selectRepairBaseCommit(fixture.investigation));
      expect(result.instructionPath && existsSync(result.instructionPath)).toBe(true);
      expect(result.managedRoot && existsSync(join(result.managedRoot, "proof-ro", "investigation.json"))).toBe(true);
      expect(result.note).not.toMatch(/REPAIR_WORKTREE_PREPARED/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("records a Codex draft patch inside the isolated worktree without claiming verified repair", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-repair-codex-"));
    try {
      const fixture = await createProofFixture(root);
      const result = await repairWithCodex({
        bundleDirectory: fixture.bundleDirectory,
        expectRoot: fixture.rootDigest,
        repository: fixture.repository,
        outputDirectory: join(root, "repair-codex"),
        withCodex: true,
        keepWorktree: true,
        runner: {
          async run(_args, options) {
            writeFileSync(join(options.cwd, "state.txt"), "fixed-by-codex\n", "utf8");
            return { exitCode: 0, stdout: "ok", stderr: "", threadId: "thread-test-1" };
          }
        },
        verifyBundle: () => ({ valid: true, errors: [], rootDigest: fixture.rootDigest })
      });
      expect(result.status).toBe("CODEX_DRAFT_RECORDED");
      expect(result.codexThreadId).toBe("thread-test-1");
      expect(result.patchPath && existsSync(result.patchPath)).toBe(true);
      expect(readFileSync(result.patchPath as string, "utf8")).toMatch(/state\.txt/);
      expect(result.worktreePath && existsSync(result.worktreePath)).toBe(true);
      if (result.worktreePath) {
        await removeRepairWorktree({
          repository: fixture.repository,
          worktreePath: result.worktreePath,
          managedRoot: result.managedRoot ?? undefined,
          keepManagedRoot: true
        });
      }
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("promotes to REPAIR_CANDIDATE_VERIFIED only when verifyCandidate reports ok", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-repair-verified-"));
    try {
      const fixture = await createProofFixture(root);
      const result = await repairWithCodex({
        bundleDirectory: fixture.bundleDirectory,
        expectRoot: fixture.rootDigest,
        repository: fixture.repository,
        outputDirectory: join(root, "repair-verified"),
        withCodex: true,
        runner: {
          async run(_args, options) {
            writeFileSync(join(options.cwd, "state.txt"), "good\n", "utf8");
            return { exitCode: 0, stdout: "ok", stderr: "" };
          }
        },
        verifyCandidate: async () => ({ ok: true, detail: "frozen witness PASS on repaired worktree" }),
        verifyBundle: () => ({ valid: true, errors: [], rootDigest: fixture.rootDigest })
      });
      expect(result.status).toBe("REPAIR_CANDIDATE_VERIFIED");
      expect(result.note).toMatch(/verified/i);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("keeps REPAIR_INSTRUCTIONS_PREPARED honest when --instructions-only is set", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-repair-instructions-"));
    try {
      const fixture = await createProofFixture(root);
      const result = await repairWithCodex({
        bundleDirectory: fixture.bundleDirectory,
        expectRoot: fixture.rootDigest,
        repository: fixture.repository,
        outputDirectory: join(root, "instructions-only"),
        instructionsOnly: true,
        verifyBundle: () => ({ valid: true, errors: [], rootDigest: fixture.rootDigest })
      });
      expect(result.status).toBe("REPAIR_INSTRUCTIONS_PREPARED");
      expect(result.worktreePath).toBeNull();
      expect(result.note).toMatch(/not an isolated Git repair worktree/i);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
