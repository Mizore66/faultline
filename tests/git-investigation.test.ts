import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  STABLE_EXECUTION_COUNT,
  investigateGitRange,
  type GitInvestigationRequest
} from "../src/git-investigation.js";
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

/**
 * core.fsmonitor stores a shell command. Git accepts double-quoted absolute
 * paths on the supported POSIX and Git-for-Windows shells; slash-normalizing
 * keeps a Windows drive path executable when Git dispatches that command.
 */
function quoteFsmonitorCommandPart(value: string): string {
  return `"${value.replaceAll("\\", "/").replaceAll('"', '\\"')}"`;
}

function commit(repository: string, state: string, message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

function createFrozenWitness(store: string, proposalId: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T10:00:00.000Z",
    incidentPacket: {
      symptom: "A previously passing commit now fails the approved regression witness.",
      ciLog: "state.txt reports bad",
      repositoryLanguage: "Text fixture",
      repositorySummary: "Temporary real Git repository used to test commit-range replay."
    },
    witness: {
      behavior: "State must not be bad.",
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
    approvedAt: "2026-07-16T10:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T10:02:00.000Z" });
}

function createRepository(): { root: string; ancestor: string; descendant: string } {
  const root = mkdtempSync(join(tmpdir(), "faultline-git-investigation-repo-"));
  git(root, ["init"]);
  git(root, ["config", "user.email", "faultline@example.test"]);
  git(root, ["config", "user.name", "FaultLine Test"]);
  const ancestor = commit(root, "good", "good state");
  commit(root, "bad", "bad state");
  const descendant = commit(root, "good-again", "repaired state");
  return { root, ancestor, descendant };
}

function stateReadingRunner(observedDirectories: string[]): SandboxCommandRunner {
  return {
    async run(invocation) {
      observedDirectories.push(invocation.cwd);
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

function requestFor(
  repository: string,
  frozenWitness: FrozenWitness,
  runner: SandboxCommandRunner,
  sandbox: GitInvestigationRequest["sandbox"] = { mode: "DOCKER_ISOLATED", image: pinnedImage }
): GitInvestigationRequest {
  return {
    repository,
    range: { ancestor: "HEAD~2", descendant: "HEAD" },
    frozenWitness,
    expectedFrozenDigest: frozenWitness.frozenDigest,
    sandbox,
    runner
  };
}

describe("Git commit-range investigation", () => {
  it("records repeated detached-worktree observations but never lets an injected runner certify Docker proof", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-investigation-store-"));
    const repository = createRepository();
    try {
      const frozenWitness = createFrozenWitness(store, "real-git-range");
      const observedDirectories: string[] = [];
      const result = await investigateGitRange(requestFor(repository.root, frozenWitness, stateReadingRunner(observedDirectories)));

      expect(result).toMatchObject({
        status: "COMPLETED",
        recorder: "git-commit-range-replay",
        nativeCodexInterception: false,
        nonMonotonic: true,
        proof: { dockerIsolated: false, executionTrust: "INJECTED_RUNNER", isProof: false, proofTransitions: 2 }
      });
      expect(result.states).toHaveLength(3);
      expect(result.states[0]).toMatchObject({ commit: repository.ancestor });
      expect(result.states[2]).toMatchObject({ commit: repository.descendant });
      expect(result.runs).toHaveLength(3 * STABLE_EXECUTION_COUNT);
      expect(result.runs.every((run) => run.result.executor === "INJECTED_RUNNER")).toBe(true);
      expect(result.runs.every((run) => run.commit.length === 40 && run.tree.length === 40)).toBe(true);
      expect(result.runs.every((run) => run.overlays[0]?.bytesLength === Buffer.byteLength("export const frozenWitness = 'exact-approved-bytes';\n"))).toBe(true);
      expect(result.transitions.map((transition) => transition.kind)).toEqual(["PASS_TO_FAIL", "FAIL_TO_PASS"]);
      expect(result.transitions.every((transition) => transition.before.executionIds.length === STABLE_EXECUTION_COUNT
        && new Set(transition.before.executionIds).size === STABLE_EXECUTION_COUNT
        && transition.after.executionIds.length === STABLE_EXECUTION_COUNT
        && new Set(transition.after.executionIds).size === STABLE_EXECUTION_COUNT)).toBe(true);
      expect(observedDirectories).toHaveLength(3 * STABLE_EXECUTION_COUNT);
      expect(new Set(observedDirectories).size).toBe(3);
      expect(git(repository.root, ["worktree", "list", "--porcelain"]).split("\n").filter((line) => line.startsWith("worktree "))).toHaveLength(1);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("rejects unsafe-local replay even if an injected runner returns zero", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-investigation-store-"));
    const repository = createRepository();
    try {
      const frozenWitness = createFrozenWitness(store, "unsafe-local-range");
      const result = await investigateGitRange(requestFor(
        repository.root,
        frozenWitness,
        stateReadingRunner([]),
        { mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }
      ));

      expect(result.status).toBe("INVALID_REQUEST");
      expect(result.runs).toEqual([]);
      expect(result.errors.join("\n")).toMatch(/DOCKER_ISOLATED|UNSAFE_LOCAL/i);
      expect(result.proof).toMatchObject({ dockerIsolated: false, isProof: false });
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("records Docker unavailability as errors rather than inventing a pass/fail boundary", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-investigation-store-"));
    const repository = createRepository();
    try {
      const frozenWitness = createFrozenWitness(store, "docker-unavailable-range");
      const unavailableRunner: SandboxCommandRunner = {
        async run() {
          return { exitCode: 125, stdout: "", stderr: "Docker daemon is unavailable" };
        }
      };
      const result = await investigateGitRange(requestFor(repository.root, frozenWitness, unavailableRunner));

      expect(result.status).toBe("SANDBOX_UNAVAILABLE");
      expect(result.runs).toHaveLength(3 * STABLE_EXECUTION_COUNT);
      expect(result.runs.every((run) => run.result.verdict === "ERROR" && run.result.reason === "SANDBOX_UNAVAILABLE")).toBe(true);
      expect(result.transitions).toEqual([]);
      expect(result.proof).toMatchObject({ isProof: false, proofTransitions: 0 });
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("preserves a witness setup failure as execution evidence instead of dropping the run", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-investigation-store-"));
    const repository = createRepository();
    try {
      const frozenWitness = createFrozenWitness(store, "setup-error-range");
      const setupErrorRunner: SandboxCommandRunner = {
        async run() {
          return { exitCode: 127, stdout: "", stderr: "node: not found" };
        }
      };
      const result = await investigateGitRange(requestFor(repository.root, frozenWitness, setupErrorRunner));

      expect(result.status).toBe("EXECUTION_ERROR");
      expect(result.runs).toHaveLength(3 * STABLE_EXECUTION_COUNT);
      expect(result.runs.every((run) => run.result.verdict === "ERROR" && run.result.reason === "WITNESS_SETUP_ERROR")).toBe(true);
      expect(result.proof.isProof).toBe(false);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("rejects a mutated frozen witness before it reads Git or invokes the runner", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-investigation-store-"));
    const repository = createRepository();
    try {
      const frozenWitness = createFrozenWitness(store, "invalid-witness-range");
      const mutated = JSON.parse(JSON.stringify(frozenWitness)) as FrozenWitness;
      mutated.proposal.witness.command = "node altered-witness.mjs";
      let calls = 0;
      const runner: SandboxCommandRunner = { async run() { calls += 1; return { exitCode: 0, stdout: "", stderr: "" }; } };
      const result = await investigateGitRange(requestFor(repository.root, mutated, runner));

      expect(result.status).toBe("INVALID_WITNESS");
      expect(result.runs).toEqual([]);
      expect(result.errors.join("\n")).toMatch(/command digest|frozen digest/);
      expect(calls).toBe(0);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("refuses checkout-time Git filters before materializing an untrusted worktree", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-investigation-store-"));
    const repository = createRepository();
    try {
      writeFileSync(join(repository.root, ".gitattributes"), "*.txt filter=hostile\n", "utf8");
      git(repository.root, ["add", ".gitattributes"]);
      git(repository.root, ["commit", "-m", "hostile filter attribute"]);
      const frozenWitness = createFrozenWitness(store, "hostile-filter-range");
      let calls = 0;
      const runner: SandboxCommandRunner = { async run() { calls += 1; return { exitCode: 0, stdout: "", stderr: "" }; } };
      const result = await investigateGitRange({
        repository: repository.root,
        range: { ancestor: "HEAD~3", descendant: "HEAD" },
        frozenWitness,
        expectedFrozenDigest: frozenWitness.frozenDigest,
        sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
        runner
      });

      expect(result.status).toBe("RANGE_ERROR");
      expect(result.errors.join("\n")).toMatch(/filter attribute/);
      expect(calls).toBe(0);
      expect(git(repository.root, ["worktree", "list", "--porcelain"]).split("\n").filter((line) => line.startsWith("worktree "))).toHaveLength(1);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });

  it("neutralizes a repository-local fsmonitor hook before Git materialization", async () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-git-investigation-store-"));
    const repository = createRepository();
    try {
      // A repo-local fsmonitor command is executable Git configuration. The
      // investigation wrapper must override it before any pre-Docker Git
      // command touches the repository or its detached worktrees.
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
      expect(git(repository.root, ["config", "--local", "--get", "core.fsmonitor"])).toBe(hostileHook);
      expect(existsSync(marker)).toBe(false);
      const frozenWitness = createFrozenWitness(store, "hostile-fsmonitor-range");
      const result = await investigateGitRange(requestFor(repository.root, frozenWitness, stateReadingRunner([])));

      expect(result.status).toBe("COMPLETED");
      expect(result.runs).toHaveLength(3 * STABLE_EXECUTION_COUNT);
      expect(result.errors).toEqual([]);
      // A nonexistent command only proves Git can fall back. This executable
      // hook writes a marker before returning, so its absence proves every
      // FaultLine host-side Git command received core.fsmonitor=false.
      expect(existsSync(marker)).toBe(false);
    } finally {
      rmSync(store, { recursive: true, force: true });
      rmSync(repository.root, { recursive: true, force: true });
    }
  });
});
