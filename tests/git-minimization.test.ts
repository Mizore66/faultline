import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  MINIMIZATION_CERTIFICATION_EXECUTIONS,
  minimizeGitDiff,
  type GitMinimizationRequest
} from "../src/git-minimization.js";
import type { SandboxCommandRunner } from "../src/sandbox.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../src/witness-lock.js";

const pinnedImage = `registry.example/faultline-node@sha256:${"a".repeat(64)}`;

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function commit(repository: string, message: string): string {
  git(repository, ["add", "-A"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

function frozenWitness(store: string, proposalId: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T10:00:00.000Z",
    incidentPacket: {
      symptom: "Two changed files jointly create a failing state.",
      ciLog: "approved witness reports an interaction failure",
      repositoryLanguage: "Text fixture",
      repositorySummary: "Temporary real Git repository used only for diff counterfactual tests."
    },
    witness: {
      behavior: "The interaction must not be present.",
      command: "node witness.mjs",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from("export const approved = true;\n", "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: "2026-07-16T10:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T10:02:00.000Z" });
}

function interactionRunner(): SandboxCommandRunner {
  return {
    async run(invocation) {
      const left = readFileSync(join(invocation.cwd, "left.txt"), "utf8").trim();
      const right = readFileSync(join(invocation.cwd, "right.txt"), "utf8").trim();
      const overlay = readFileSync(join(invocation.cwd, "witness.mjs"), "utf8");
      if (overlay !== "export const approved = true;\n") return { exitCode: 2, stdout: "", stderr: "overlay mismatch" };
      return left === "new" && right === "new"
        ? { exitCode: 1, stdout: "interaction failed\n", stderr: "both changed" }
        : { exitCode: 0, stdout: "interaction passed\n", stderr: "" };
    }
  };
}

function interactionRepository(): { root: string; before: string; after: string } {
  const root = mkdtempSync(join(tmpdir(), "faultline-git-minimization-interaction-"));
  git(root, ["init"]);
  git(root, ["config", "user.email", "faultline@example.test"]);
  git(root, ["config", "user.name", "FaultLine Test"]);
  writeFileSync(join(root, "left.txt"), "old\n", "utf8");
  writeFileSync(join(root, "right.txt"), "old\n", "utf8");
  const before = commit(root, "before interaction");
  writeFileSync(join(root, "left.txt"), "new\n", "utf8");
  writeFileSync(join(root, "right.txt"), "new\n", "utf8");
  const after = commit(root, "after interaction");
  return { root, before, after };
}

function requestFor(repository: string, before: string, after: string, witness: FrozenWitness, runner: SandboxCommandRunner): GitMinimizationRequest {
  return {
    repository,
    before,
    after,
    frozenWitness: witness,
    expectedFrozenDigest: witness.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
    budget: { maxExecutions: 40 },
    runner
  };
}

describe("Git diff counterfactual minimization", () => {
  it("discovers a two-file interaction in a real Git diff and certifies it with distinct Docker executions", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-store-"));
    const repository = interactionRepository();
    try {
      const witness = frozenWitness(store, "two-file-interaction");
      const result = await minimizeGitDiff(requestFor(repository.root, repository.before, repository.after, witness, interactionRunner()));

      expect(result.status).toBe("COMPLETED");
      expect(result.nativeCodexInterception).toBe(false);
      expect(result.patchUnits).toHaveLength(2);
      expect(new Set(result.patchUnits.map((unit) => unit.id))).toEqual(new Set(result.candidateUnitIds));
      expect(result.candidateUnitIds).toHaveLength(2);
      expect(result.minimality.oneMinimal).toBe(true);
      expect(result.attempts.some((attempt) => attempt.phase === "ONE_MINIMAL" && attempt.outcome === "PASS")).toBe(true);
      expect(result.certification.sufficiency).toMatchObject({ status: "CERTIFIED", requiredExecutions: MINIMIZATION_CERTIFICATION_EXECUTIONS });
      expect(result.certification.necessity).toMatchObject({ status: "CERTIFIED", requiredExecutions: MINIMIZATION_CERTIFICATION_EXECUTIONS });
      expect(new Set(result.certification.sufficiency.executionIds)).toHaveLength(MINIMIZATION_CERTIFICATION_EXECUTIONS);
      expect(new Set(result.certification.necessity.executionIds)).toHaveLength(MINIMIZATION_CERTIFICATION_EXECUTIONS);
      expect(result.proof).toMatchObject({ dockerIsolated: true, isProof: true });
      expect(git(repository.root, ["worktree", "list", "--porcelain"]).split("\n").filter((line) => line.startsWith("worktree "))).toHaveLength(1);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("records a real Git apply conflict as UNRESOLVED rather than treating it as a pass", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-store-"));
    const root = mkdtempSync(join(tmpdir(), "faultline-git-minimization-conflict-"));
    git(root, ["init"]);
    git(root, ["config", "user.email", "faultline@example.test"]);
    git(root, ["config", "user.name", "FaultLine Test"]);
    writeFileSync(join(root, "switch"), "file\n", "utf8");
    writeFileSync(join(root, "flag.txt"), "old\n", "utf8");
    const before = commit(root, "file before directory");
    git(root, ["rm", "switch"]);
    git(root, ["checkout", "--", "flag.txt"]);
    writeFileSync(join(root, "flag.txt"), "new\n", "utf8");
    // The directory/file transition creates an add patch that cannot apply unless
    // its paired deletion is also selected; ddmin must surface that as unresolved.
    const switchDirectory = join(root, "switch");
    mkdirSync(switchDirectory);
    writeFileSync(join(switchDirectory, "nested.txt"), "new\n", "utf8");
    const after = commit(root, "directory after file");
    try {
      const witness = frozenWitness(store, "apply-conflict");
      const runner: SandboxCommandRunner = {
        async run(invocation) {
          const hasNested = (() => {
            try { return readFileSync(join(invocation.cwd, "switch", "nested.txt"), "utf8").trim() === "new"; } catch { return false; }
          })();
          const flag = readFileSync(join(invocation.cwd, "flag.txt"), "utf8").trim();
          return hasNested && flag === "new"
            ? { exitCode: 1, stdout: "failed\n", stderr: "interaction" }
            : { exitCode: 0, stdout: "passed\n", stderr: "" };
        }
      };
      const result = await minimizeGitDiff(requestFor(root, before, after, witness, runner));

      expect(result.runs.some((run) => run.application.status === "CONFLICT" && run.outcome === "UNRESOLVED")).toBe(true);
      expect(result.attempts.some((attempt) => attempt.outcome === "UNRESOLVED")).toBe(true);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("never turns unsafe-local or Docker-unavailable results into proof", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-store-"));
    const repository = interactionRepository();
    try {
      const witness = frozenWitness(store, "sandbox-policy");
      const unsafe = await minimizeGitDiff({
        ...requestFor(repository.root, repository.before, repository.after, witness, interactionRunner()),
        sandbox: { mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }
      });
      expect(unsafe.status).toBe("UNSAFE_LOCAL_INAPPLICABLE");
      expect(unsafe.runs.every((run) => run.result?.verdict === "INAPPLICABLE")).toBe(true);
      expect(unsafe.proof.isProof).toBe(false);

      const unavailable: SandboxCommandRunner = {
        async run() { return { exitCode: 125, stdout: "", stderr: "Docker daemon unavailable" }; }
      };
      const dockerUnavailable = await minimizeGitDiff(requestFor(repository.root, repository.before, repository.after, witness, unavailable));
      expect(dockerUnavailable.status).toBe("SANDBOX_UNAVAILABLE");
      expect(dockerUnavailable.runs[0]?.result).toMatchObject({ verdict: "ERROR", reason: "SANDBOX_UNAVAILABLE" });
      expect(dockerUnavailable.proof.isProof).toBe(false);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });
});
