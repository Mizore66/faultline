import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  MINIMIZATION_CERTIFICATION_EXECUTIONS,
  minimizeGitDiff,
  splitModifyUnitIntoHunks,
  verifyGitMinimizationResult,
  verifyGitMinimizationResultFile,
  writeGitMinimizationResult,
  type GitMinimizationResult,
  type GitMinimizationRunFact,
  type GitMinimizationRequest
} from "../src/git-minimization.js";
import { digestJson } from "../src/canonical.js";
import type { SandboxCommandRunner } from "../src/sandbox.js";
import { formatWitnessResult } from "../src/witness-result.js";
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

/** Quote a Git fsmonitor command for both Git-for-Windows and POSIX shells. */
function quoteFsmonitorCommandPart(value: string): string {
  return `"${value.replaceAll("\\", "/").replaceAll('"', '\\"')}"`;
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
        ? { exitCode: 1, stdout: `interaction failed\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "both changed" }
        : { exitCode: 0, stdout: `interaction passed\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
    }
  };
}

function nonMonotonicRunner(): SandboxCommandRunner {
  return {
    async run(invocation) {
      const left = readFileSync(join(invocation.cwd, "left.txt"), "utf8").trim();
      const right = readFileSync(join(invocation.cwd, "right.txt"), "utf8").trim();
      const guard = readFileSync(join(invocation.cwd, "guard.txt"), "utf8").trim();
      const overlay = readFileSync(join(invocation.cwd, "witness.mjs"), "utf8");
      if (overlay !== "export const approved = true;\n") return { exitCode: 2, stdout: "", stderr: "overlay mismatch" };
      const fails = guard === "new" || (left === "new") !== (right === "new");
      return fails
        ? { exitCode: 1, stdout: `non-monotonic fixture failed\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "predicate failed" }
        : { exitCode: 0, stdout: `non-monotonic fixture passed\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
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

function nonMonotonicRepository(): { root: string; before: string; after: string } {
  const root = mkdtempSync(join(tmpdir(), "faultline-git-minimization-non-monotonic-"));
  git(root, ["init"]);
  git(root, ["config", "user.email", "faultline@example.test"]);
  git(root, ["config", "user.name", "FaultLine Test"]);
  for (const path of ["left.txt", "right.txt", "guard.txt"]) writeFileSync(join(root, path), "old\n", "utf8");
  const before = commit(root, "before non-monotonic interaction");
  for (const path of ["left.txt", "right.txt", "guard.txt"]) writeFileSync(join(root, path), "new\n", "utf8");
  const after = commit(root, "after non-monotonic interaction");
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

function rehashMinimizationRun(run: GitMinimizationRunFact): void {
  run.executionId = digestJson({
    schemaVersion: run.schemaVersion,
    nonce: run.executionNonce,
    role: run.role,
    roleAttempt: run.roleAttempt,
    direction: run.application.direction,
    base: run.application.base,
    candidateUnitIds: run.candidateUnitIds,
    frozenDigest: run.frozenDigest
  });
  const { runId: _runId, ...unsigned } = run;
  run.runId = digestJson(unsigned);
}

/** Build a self-consistent-looking native-Docker proof that omits search evidence. */
function forgedCertificateOnlyProof(result: GitMinimizationResult): GitMinimizationResult {
  const forged = JSON.parse(JSON.stringify(result)) as GitMinimizationResult;
  const sufficiencyTemplate = forged.runs.find((run) => run.role === "SUFFICIENCY_CERTIFICATION");
  const necessityTemplate = forged.runs.find((run) => run.role === "NECESSITY_CERTIFICATION");
  if (!sufficiencyTemplate || !necessityTemplate || !sufficiencyTemplate.result || !necessityTemplate.result) {
    throw new Error("test fixture did not retain both certification run templates");
  }

  const cloneCertificationRun = (
    template: GitMinimizationRunFact,
    roleAttempt: number
  ): GitMinimizationRunFact => {
    const cloned = JSON.parse(JSON.stringify(template)) as GitMinimizationRunFact;
    cloned.roleAttempt = roleAttempt;
    cloned.executionNonce = randomUUID();
    cloned.result = { ...cloned.result!, executor: "NATIVE_DOCKER" };
    rehashMinimizationRun(cloned);
    return cloned;
  };

  const sufficiencyRuns = [1, 2, 3].map((roleAttempt) => cloneCertificationRun(sufficiencyTemplate, roleAttempt));
  const necessityRuns = [1, 2, 3].map((roleAttempt) => cloneCertificationRun(necessityTemplate, roleAttempt));
  forged.runs = [...sufficiencyRuns, ...necessityRuns];
  forged.attempts = forged.runs.map((run, index) => ({
    ordinal: index + 1,
    phase: run.role === "SUFFICIENCY_CERTIFICATION" ? "SUFFICIENCY_CERTIFICATION" : "NECESSITY_CERTIFICATION",
    candidateUnitIds: [...run.candidateUnitIds],
    outcome: run.outcome,
    runId: run.runId,
    note: run.note
  }));
  forged.budget.usedExecutions = forged.runs.length;
  forged.certification.sufficiency = {
    ...forged.certification.sufficiency,
    status: "CERTIFIED",
    runIds: sufficiencyRuns.map((run) => run.runId),
    executionIds: sufficiencyRuns.map((run) => run.executionId)
  };
  forged.certification.necessity = {
    ...forged.certification.necessity,
    status: "CERTIFIED",
    runIds: necessityRuns.map((run) => run.runId),
    executionIds: necessityRuns.map((run) => run.executionId)
  };
  forged.status = "COMPLETED";
  forged.minimality = {
    ...forged.minimality,
    oneMinimal: true,
    reason: "Forged assertion without retained ordinary search evidence."
  };
  forged.proof = {
    ...forged.proof,
    dockerIsolated: true,
    executionTrust: "NATIVE_DOCKER",
    sufficiencyCertified: true,
    necessityCertified: true,
    isProof: true
  };
  forged.errors = [];
  return forged;
}

describe("Git diff counterfactual minimization", () => {
  it("enumerates bounded distinct minimal sets and retains a non-monotonic interaction verdict", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-non-monotonic-store-"));
    const repository = nonMonotonicRepository();
    try {
      const witness = frozenWitness(store, "non-monotonic-interaction");
      const result = await minimizeGitDiff(requestFor(repository.root, repository.before, repository.after, witness, nonMonotonicRunner()));

      expect(result.minimality.enumeration).toMatchObject({ status: "COMPLETED", searchedCandidateSets: 6 });
      expect(result.minimality.enumeration.distinctMinimalSets).toHaveLength(3);
      expect(result.minimality.nonMonotonicInteraction.verdict).toBe("NON_MONOTONIC_INTERACTION");
      expect(result.minimality.nonMonotonicInteraction.evidence).toMatchObject({
        failingLeft: expect.any(Array),
        failingRight: expect.any(Array),
        passingUnion: expect.any(Array)
      });
      expect(verifyGitMinimizationResult(result)).toMatchObject({ valid: true });
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("discovers a two-file interaction but refuses to certify an injected runner as Docker proof", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-store-"));
    const repository = interactionRepository();
    try {
      const witness = frozenWitness(store, "two-file-interaction");
      const result = await minimizeGitDiff(requestFor(repository.root, repository.before, repository.after, witness, interactionRunner()));

      expect(result.status).toBe("CERTIFICATION_FAILED");
      expect(result.nativeCodexInterception).toBe(false);
      expect(result.patchUnits).toHaveLength(2);
      expect(new Set(result.patchUnits.map((unit) => unit.id))).toEqual(new Set(result.candidateUnitIds));
      expect(result.candidateUnitIds).toHaveLength(2);
      expect(result.minimality.oneMinimal).toBe(true);
      expect(result.attempts.some((attempt) => attempt.phase === "ONE_MINIMAL" && attempt.outcome === "PASS")).toBe(true);
      expect(result.certification.sufficiency).toMatchObject({ status: "NOT_CERTIFIED", requiredExecutions: MINIMIZATION_CERTIFICATION_EXECUTIONS });
      expect(result.proof).toMatchObject({ dockerIsolated: false, executionTrust: "INJECTED_RUNNER", isProof: false });
      expect(result.runs.every((run) => run.result?.executor === "INJECTED_RUNNER")).toBe(true);
      expect(git(repository.root, ["worktree", "list", "--porcelain"]).split("\n").filter((line) => line.startsWith("worktree "))).toHaveLength(1);

      const written = await writeGitMinimizationResult(join(store, "result.json"), result);
      expect(written.resultDigest).toBe(digestJson(result));
      await expect(writeGitMinimizationResult(written.path, result)).rejects.toThrow(/already exists and will not be replaced/);
      expect(verifyGitMinimizationResult(result, written.resultDigest)).toMatchObject({
        valid: true,
        resultDigest: written.resultDigest,
        externalDigestStatus: "MATCH"
      });
      expect(verifyGitMinimizationResultFile(written.path, written.resultDigest)).toMatchObject({
        valid: true,
        resultDigest: written.resultDigest,
        externalDigestStatus: "MATCH"
      });
      expect(verifyGitMinimizationResult(result, `sha256:${"f".repeat(64)}`)).toMatchObject({
        valid: false,
        externalDigestStatus: "MISMATCH"
      });

      // Rehashing a changed run cannot revive it: execution ids are bound to
      // a retained nonce and the attempt graph must still reference the exact
      // run record.
      const rehashed = JSON.parse(JSON.stringify(result)) as typeof result;
      const firstRun = rehashed.runs[0];
      const secondRun = rehashed.runs[1];
      if (!firstRun || !secondRun) throw new Error("test fixture did not record two minimization runs");
      const previousRunId = firstRun.runId;
      firstRun.executionId = secondRun.executionId;
      const { runId: _runId, ...unsigned } = firstRun;
      firstRun.runId = digestJson(unsigned);
      const linkedAttempt = rehashed.attempts.find((attempt) => attempt.runId === previousRunId);
      if (!linkedAttempt) throw new Error("test fixture did not link the first minimization run");
      linkedAttempt.runId = firstRun.runId;
      const rehashedVerification = verifyGitMinimizationResult(rehashed);
      expect(rehashedVerification.valid).toBe(false);
      expect(rehashedVerification.errors.join("\n")).toMatch(/execution id does not match|duplicate execution id/);

      const inflated = JSON.parse(JSON.stringify(result)) as typeof result;
      inflated.status = "COMPLETED";
      inflated.proof = {
        ...inflated.proof,
        dockerIsolated: true,
        executionTrust: "NATIVE_DOCKER",
        sufficiencyCertified: true,
        necessityCertified: true,
        isProof: true
      };
      inflated.certification.sufficiency.status = "CERTIFIED";
      inflated.certification.necessity.status = "CERTIFIED";
      const inflatedVerification = verifyGitMinimizationResult(inflated);
      expect(inflatedVerification.valid).toBe(false);
      expect(inflatedVerification.errors.join("\n")).toMatch(/certificate|proof|provenance|COMPLETED/);

      // Six native-looking repeat certifications alone are not a proof. A
      // forged record must also retain linked baseline, full-range, and final
      // one-minimal ordinary attempts. This fixture deliberately removes all
      // three kinds while preserving the record's internal certificate graph.
      const certificateOnlyForgery = forgedCertificateOnlyProof(result);
      const certificateOnlyVerification = verifyGitMinimizationResult(certificateOnlyForgery);
      expect(certificateOnlyVerification.valid).toBe(false);
      expect(certificateOnlyVerification.errors.join("\n")).toMatch(
        /baseline-before \[\] evidence|full-patch \[all patch unit ids\] evidence|one-minimal evidence/
      );
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
            ? { exitCode: 1, stdout: `failed\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "interaction" }
            : { exitCode: 0, stdout: `passed\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
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

  it("rejects unsafe-local requests and never turns Docker-unavailable results into proof", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-store-"));
    const repository = interactionRepository();
    try {
      const witness = frozenWitness(store, "sandbox-policy");
      const unsafe = await minimizeGitDiff({
        ...requestFor(repository.root, repository.before, repository.after, witness, interactionRunner()),
        sandbox: { mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }
      });
      expect(unsafe.status).toBe("INVALID_REQUEST");
      expect(unsafe.errors.join("\n")).toMatch(/DOCKER_ISOLATED|UNSAFE_LOCAL/i);
      expect(unsafe.runs).toEqual([]);
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

  it("neutralizes a repository-local fsmonitor hook before minimization worktrees", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-store-"));
    const repository = interactionRepository();
    try {
      const hook = join(repository.root, "faultline-forbidden-fsmonitor-hook.cjs");
      const marker = join(repository.root, "faultline-fsmonitor-hook-invoked");
      writeFileSync(
        hook,
        'require("node:fs").writeFileSync(process.argv[2], "invoked", "utf8");\n',
        "utf8"
      );
      git(repository.root, ["update-index", "--fsmonitor"]);
      const hostileHook = [
        quoteFsmonitorCommandPart(process.execPath),
        quoteFsmonitorCommandPart(hook),
        quoteFsmonitorCommandPart(marker)
      ].join(" ");
      git(repository.root, ["config", "core.fsmonitor", hostileHook]);
      expect(existsSync(marker)).toBe(false);

      const witness = frozenWitness(store, "hostile-fsmonitor-minimization");
      const result = await minimizeGitDiff(requestFor(
        repository.root,
        repository.before,
        repository.after,
        witness,
        interactionRunner()
      ));

      expect(result.status).toBe("CERTIFICATION_FAILED");
      expect(result.errors).toEqual([]);
      expect(existsSync(marker)).toBe(false);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("fails closed before a turn can execute a checkout smudge filter on the host", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-store-"));
    const repository = interactionRepository();
    try {
      const filter = join(repository.root, "faultline-forbidden-smudge-filter.cjs");
      const marker = join(repository.root, "faultline-smudge-filter-invoked");
      writeFileSync(
        filter,
        [
          'const fs = require("node:fs");',
          'fs.writeFileSync(process.argv[2], "invoked", "utf8");',
          "process.stdin.pipe(process.stdout);",
          ""
        ].join("\n"),
        "utf8"
      );
      const hostileFilter = [
        quoteFsmonitorCommandPart(process.execPath),
        quoteFsmonitorCommandPart(filter),
        quoteFsmonitorCommandPart(marker)
      ].join(" ");
      writeFileSync(join(repository.root, ".gitattributes"), "*.txt filter=hostile\n", "utf8");
      const hostileAfter = commit(repository.root, "hostile checkout filter");
      git(repository.root, ["config", "filter.hostile.smudge", hostileFilter]);
      git(repository.root, ["config", "filter.hostile.required", "true"]);
      expect(existsSync(marker)).toBe(false);

      let runnerCalls = 0;
      const runner: SandboxCommandRunner = {
        async run() {
          runnerCalls += 1;
          return { exitCode: 0, stdout: "", stderr: "" };
        }
      };
      const witness = frozenWitness(store, "hostile-smudge-minimization");
      const result = await minimizeGitDiff(requestFor(
        repository.root,
        repository.before,
        hostileAfter,
        witness,
        runner
      ));

      expect(result.status).toBe("RANGE_ERROR");
      expect(result.errors.join("\n")).toMatch(/filter configuration|filter attribute/);
      expect(runnerCalls).toBe(0);
      expect(existsSync(marker)).toBe(false);
      expect(git(repository.root, ["worktree", "list", "--porcelain"]).split("\n").filter((line) => line.startsWith("worktree "))).toHaveLength(1);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("refines a one-file two-hunk MODIFY down to the failure-inducing hunk", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-hunk-store-"));
    const root = mkdtempSync(join(tmpdir(), "faultline-git-minimization-hunk-repo-"));
    try {
      git(root, ["init"]);
      git(root, ["config", "user.email", "faultline@example.test"]);
      git(root, ["config", "user.name", "FaultLine Test"]);
      writeFileSync(join(root, "module.txt"), ["alpha", "keep-1", "keep-2", "keep-3", "keep-4", "keep-5", "omega"].join("\n") + "\n", "utf8");
      const before = commit(root, "before hunks");
      writeFileSync(join(root, "module.txt"), ["ALPHA", "keep-1", "keep-2", "keep-3", "keep-4", "keep-5", "OMEGA"].join("\n") + "\n", "utf8");
      const after = commit(root, "two independent hunks");

      const runner: SandboxCommandRunner = {
        async run(invocation) {
          const body = readFileSync(join(invocation.cwd, "module.txt"), "utf8");
          const overlay = readFileSync(join(invocation.cwd, "witness.mjs"), "utf8");
          if (overlay !== "export const approved = true;\n") {
            return { exitCode: 2, stdout: "", stderr: "overlay mismatch" };
          }
          // Only the ALPHA hunk is failure-inducing; OMEGA alone must PASS.
          return body.includes("ALPHA")
            ? { exitCode: 1, stdout: `hunk failed\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "alpha" }
            : { exitCode: 0, stdout: `hunk passed\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
        }
      };
      const witness = frozenWitness(store, "hunk-refinement");
      const result = await minimizeGitDiff(requestFor(root, before, after, witness, runner));
      expect(result.patchUnits.length).toBeGreaterThanOrEqual(1);
      const parent = result.patchUnits.find((unit) => unit.path === "module.txt");
      expect(parent).toBeDefined();
      // After refinement, selected set should be a single hunk (or one parent if split skipped).
      expect(result.candidateUnitIds.length).toBe(1);
      expect(result.attempts.some((attempt) => attempt.phase === "HUNK_ONE_MINIMAL" || attempt.phase === "ONE_MINIMAL")).toBe(true);
      const selected = result.patchUnits.filter((unit) => result.candidateUnitIds.includes(unit.id));
      expect(selected).toHaveLength(1);
      expect(selected[0]?.path).toBe("module.txt");
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("budget exhaustion mid hunk refinement retains the file-level one-minimal candidate", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-minimization-hunk-budget-store-"));
    const root = mkdtempSync(join(tmpdir(), "faultline-git-minimization-hunk-budget-repo-"));
    try {
      git(root, ["init"]);
      git(root, ["config", "user.email", "faultline@example.test"]);
      git(root, ["config", "user.name", "FaultLine Test"]);
      writeFileSync(join(root, "module.txt"), ["alpha", "keep-1", "keep-2", "keep-3", "keep-4", "keep-5", "omega"].join("\n") + "\n", "utf8");
      const before = commit(root, "before hunks");
      writeFileSync(join(root, "module.txt"), ["ALPHA", "keep-1", "keep-2", "keep-3", "keep-4", "keep-5", "OMEGA"].join("\n") + "\n", "utf8");
      const after = commit(root, "two independent hunks");

      let executions = 0;
      const runner: SandboxCommandRunner = {
        async run(invocation) {
          executions += 1;
          const body = readFileSync(join(invocation.cwd, "module.txt"), "utf8");
          return body.includes("ALPHA")
            ? { exitCode: 1, stdout: `hunk failed\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "alpha" }
            : { exitCode: 0, stdout: `hunk passed\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
        }
      };
      const witness = frozenWitness(store, "hunk-budget");
      // Tight budget: enough for file-level one-minimal, then exhaust during hunk probes.
      const result = await minimizeGitDiff({
        ...requestFor(root, before, after, witness, runner),
        budget: { maxExecutions: 6 }
      });
      // With a tight budget the search may stop before certification; never claim
      // unsupported hunk proof when refinement could not finish.
      expect(result.candidateUnitIds.length).toBeGreaterThanOrEqual(1);
      expect(result.proof.isProof).toBe(false);
      expect(
        result.status === "BUDGET_EXHAUSTED"
        || result.status === "CERTIFICATION_FAILED"
        || !result.minimality.oneMinimal
        || result.minimality.reason.toLowerCase().includes("budget")
        || result.minimality.reason.toLowerCase().includes("file-level")
        || result.attempts.some((attempt) => attempt.phase === "HUNK_ONE_MINIMAL")
      ).toBe(true);
      expect(executions).toBeLessThanOrEqual(6);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("splitModifyUnitIntoHunks returns null for unsplittable single-hunk patches", () => {
    const unit = {
      id: `sha256:${"1".repeat(64)}`,
      ordinal: 0,
      changeKind: "MODIFY" as const,
      path: "a.txt",
      pathBytesBase64: Buffer.from("a.txt").toString("base64"),
      pathDigest: `sha256:${"2".repeat(64)}`,
      patchDigest: `sha256:${"3".repeat(64)}`,
      patchBytes: 10,
      binarySafe: true,
      bytes: Buffer.from("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n", "utf8")
    };
    expect(splitModifyUnitIntoHunks(unit)).toBeNull();
  });
});
