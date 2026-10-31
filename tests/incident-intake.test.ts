import { execFileSync } from "node:child_process";
import { mkdtempSync, realpathSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative } from "node:path";
import { inspect } from "node:util";
import { describe, expect, it } from "vitest";
import { DOCTOR_SAFE_GIT_CONFIG } from "../src/doctor.js";
import {
  INCIDENT_INTAKE_SCHEMA_VERSION,
  IncidentIntakeError,
  suggestIncidentRanges,
  type IncidentIntakeGitCommand,
  type IncidentIntakeGitCommandResult,
  type IncidentIntakeGitRunner
} from "../src/incident-intake.js";

const head = "a".repeat(40);
const mergeBase = "b".repeat(40);
const parent = "c".repeat(40);
const upstreamCommit = "d".repeat(40);

function response(overrides: Partial<IncidentIntakeGitCommandResult> = {}): IncidentIntakeGitCommandResult {
  return {
    exitCode: 0,
    stdout: "",
    stderr: "",
    timedOut: false,
    outputLimitExceeded: false,
    ...overrides
  };
}

function fakeRunner(respond: (command: IncidentIntakeGitCommand) => IncidentIntakeGitCommandResult): {
  runner: IncidentIntakeGitRunner;
  calls: IncidentIntakeGitCommand[];
} {
  const calls: IncidentIntakeGitCommand[] = [];
  return {
    calls,
    runner: {
      run(command) {
        calls.push(command);
        return respond(command);
      }
    }
  };
}

function commandTail(command: IncidentIntakeGitCommand): string[] {
  return [...command.arguments.slice(DOCTOR_SAFE_GIT_CONFIG.length + 2)];
}

function fakeRepositoryDirectory(label: string): string {
  return realpathSync(mkdtempSync(join(tmpdir(), `faultline-incident-intake-${label}-`)));
}

// Failure-only evidence: keep exact IDs as strings as well as Node's Number IDs.
function emitIntakeIdentityDiagnostics(context: {
  root: string;
  reportedRoot: string;
  calls: readonly Pick<IncidentIntakeGitCommand, "arguments" | "timeoutMs" | "maxOutputBytes">[];
  error: unknown;
}, write: (line: string) => void = (line) => { process.stderr.write(line); }): void {
  try {
    const paths = new Set([context.reportedRoot]);
    let current = context.root;
    for (let depth = 0; depth < 256; depth += 1) {
      paths.add(current);
      const next = dirname(current);
      if (next === current) break;
      current = next;
    }
    const identities = [...paths].map((path) => {
      try {
        const number = statSync(path);
        const bigint = statSync(path, { bigint: true });
        return {
          path,
          realpath: realpathSync(path),
          number: { dev: number.dev, ino: number.ino },
          bigint: { dev: bigint.dev.toString(), ino: bigint.ino.toString() },
          devSafeInteger: Number.isSafeInteger(number.dev),
          inoSafeInteger: Number.isSafeInteger(number.ino),
          inoZero: number.ino === 0,
          devConversionLossless: BigInt(number.dev) === bigint.dev,
          inoConversionLossless: BigInt(number.ino) === bigint.ino
        };
      } catch (error) {
        return { path, error: inspect(error, { depth: 4, maxStringLength: null }) };
      }
    });
    const reported = identities.find((identity) => identity.path === context.reportedRoot);
    for (const identity of identities) {
      if (identity.number && identity.bigint && reported?.number && reported.bigint) {
        Object.assign(identity, {
          numberMatchesReported: identity.number.dev === reported.number.dev && identity.number.ino === reported.number.ino,
          bigintMatchesReported: identity.bigint.dev === reported.bigint.dev && identity.bigint.ino === reported.bigint.ino
        });
      }
    }
    // Diagnostic replay of isInsideDirectory's string branch, not its decision.
    const comparable = (path: string): string => process.platform === "win32"
      ? path.replace(/^\\\\\?\\UNC\\/i, "\\\\").replace(/^\\\\\?\\/i, "").replaceAll("/", "\\").toLocaleLowerCase("en-US")
      : path;
    write(`FL-DIAG-INCIDENT-208 ${JSON.stringify({
      platform: process.platform,
      arch: process.arch,
      node: process.version,
      root: context.root,
      reportedRoot: context.reportedRoot,
      comparableRoot: comparable(context.root),
      comparableReportedRoot: comparable(context.reportedRoot),
      pathFromParent: relative(comparable(context.reportedRoot), comparable(context.root)),
      callCount: context.calls.length,
      calls: context.calls,
      error: inspect(context.error, { depth: 6, maxStringLength: null }),
      identities
    })}\n`);
  } catch {
    // Diagnostics must never replace the assertion failure or prevent cleanup.
  }
}

function fullCandidateResponse(repository: string, overrides: {
  upstream?: "missing" | "head" | "invalid";
  parent?: "missing" | "invalid";
} = {}) {
  return (command: IncidentIntakeGitCommand): IncidentIntakeGitCommandResult => {
    const tail = commandTail(command);
    if (tail.join(" ") === "rev-parse --is-inside-work-tree") return response({ stdout: "true\n" });
    if (tail.join(" ") === "rev-parse --show-toplevel") return response({ stdout: `${repository}\n` });
    if (tail.join(" ") === "rev-parse --verify --end-of-options HEAD^{commit}") return response({ stdout: `${head}\n` });
    if (tail.join(" ") === "rev-parse --symbolic-full-name @{upstream}") {
      if (overrides.upstream === "missing") return response({ exitCode: 128, stderr: "no upstream" });
      if (overrides.upstream === "invalid") return response({ stdout: "--unsafe\n" });
      return response({ stdout: "refs/remotes/origin/main\n" });
    }
    if (tail.join(" ") === "rev-parse --verify --end-of-options refs/remotes/origin/main^{commit}") return response({ stdout: `${upstreamCommit}\n` });
    if (tail.join(" ") === `merge-base ${head} ${upstreamCommit}`) return response({ stdout: overrides.upstream === "head" ? `${head}\n` : `${mergeBase}\n` });
    if (tail.join(" ") === `merge-base --is-ancestor ${mergeBase} ${head}`) return response();
    if (tail.join(" ") === "rev-parse --verify --end-of-options HEAD^") {
      if (overrides.parent === "missing") return response({ exitCode: 128, stderr: "root commit" });
      if (overrides.parent === "invalid") return response({ stdout: "not-an-object\n" });
      return response({ stdout: `${parent}\n` });
    }
    if (tail.join(" ") === `merge-base --is-ancestor ${parent} ${head}`) return response();
    throw new Error(`Unexpected Git command: ${tail.join(" ")}`);
  };
}

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function commit(repository: string, content: string, message: string): string {
  writeFileSync(join(repository, "state.txt"), `${content}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

describe("incident intake failure diagnostics", () => {
  it("retains exact filesystem IDs, full argv and the original rejection", () => {
    const root = fakeRepositoryDirectory("diagnostic-root");
    const reportedRoot = fakeRepositoryDirectory("diagnostic-reported-root");
    const lines: string[] = [];
    const error = new IncidentIntakeError("GIT_UNAVAILABLE", "diagnostic original rejection");
    try {
      emitIntakeIdentityDiagnostics({
        root,
        reportedRoot,
        calls: [{
          arguments: ["-C", root, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"],
          timeoutMs: 1234,
          maxOutputBytes: 5678
        }],
        error
      }, (line) => { lines.push(line); });
      expect(lines).toHaveLength(1);
      expect(lines[0]).toMatch(/^FL-DIAG-INCIDENT-208 /);
      const record = JSON.parse(lines[0]!.slice("FL-DIAG-INCIDENT-208 ".length));
      expect(record.calls).toEqual([{
        arguments: ["-C", root, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"],
        timeoutMs: 1234,
        maxOutputBytes: 5678
      }]);
      expect(record.error).toContain("diagnostic original rejection");
      expect(record.error).toContain("GIT_UNAVAILABLE");
      expect(record.identities).toContainEqual(expect.objectContaining({
        path: root,
        number: { dev: statSync(root).dev, ino: statSync(root).ino },
        bigint: { dev: statSync(root, { bigint: true }).dev.toString(), ino: statSync(root, { bigint: true }).ino.toString() }
      }));
      expect(record.identities).toContainEqual(expect.objectContaining({ path: reportedRoot }));
      expect(record.identities).toContainEqual(expect.objectContaining({ path: dirname(root) }));
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(reportedRoot, { recursive: true, force: true });
    }
  });

  it("does not let a missing path or failed diagnostic write replace the failure", () => {
    const root = fakeRepositoryDirectory("diagnostic-missing");
    const missing = join(root, "missing");
    const context = { root: missing, reportedRoot: missing, calls: [], error: new Error("original failure") };
    const lines: string[] = [];
    try {
      expect(() => emitIntakeIdentityDiagnostics(context, (line) => { lines.push(line); })).not.toThrow();
      expect(lines).toHaveLength(1);
      const record = JSON.parse(lines[0]!.slice("FL-DIAG-INCIDENT-208 ".length));
      expect(record.identities).toContainEqual(expect.objectContaining({ path: missing, error: expect.stringContaining("ENOENT") }));
      expect(() => emitIntakeIdentityDiagnostics(context, () => { throw new Error("diagnostic write failed"); })).not.toThrow();
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});

describe("local incident range suggestions", () => {
  it("returns transparent local-upstream and parent candidates without selecting either", async () => {
    const repository = fakeRepositoryDirectory("full");
    const fake = fakeRunner(fullCandidateResponse(repository));
    try {
      const report = await suggestIncidentRanges({ repository, runner: fake.runner });

      expect(report).toMatchObject({
        schemaVersion: INCIDENT_INTAKE_SCHEMA_VERSION,
        repository,
        repositoryRoot: repository,
        head,
        automaticSelection: "NONE"
      });
      expect(report.selectionReason).toMatch(/human/i);
      expect(report.candidates).toEqual([
        expect.objectContaining({
          id: "LOCAL_UPSTREAM_MERGE_BASE",
          from: mergeBase,
          to: head,
          confidence: "MEDIUM",
          requiresHumanApproval: true,
          evidence: expect.objectContaining({
            upstreamRef: "refs/remotes/origin/main",
            upstreamCommit,
            remoteContacted: false
          })
        }),
        expect.objectContaining({
          id: "HEAD_PARENT",
          from: parent,
          to: head,
          confidence: "LOW",
          requiresHumanApproval: true
        })
      ]);
      expect(report.observations.map((observation) => observation.status)).toEqual(["CANDIDATE_AVAILABLE", "CANDIDATE_AVAILABLE"]);
      expect(report.limitations.join(" ")).toMatch(/did not contact a remote/i);

      const gitCalls = fake.calls.map((command) => [...command.arguments]);
      expect(gitCalls.every((args) => DOCTOR_SAFE_GIT_CONFIG.every((value, index) => args[index] === value))).toBe(true);
      expect(gitCalls.every((args) => args[DOCTOR_SAFE_GIT_CONFIG.length] === "-C" && args[DOCTOR_SAFE_GIT_CONFIG.length + 1] === repository)).toBe(true);
      expect(gitCalls.flat()).not.toContain("fetch");
      expect(gitCalls.flat()).not.toContain("ls-remote");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("falls back to HEAD's parent when no local upstream is configured", async () => {
    const repository = fakeRepositoryDirectory("parent-fallback");
    const fake = fakeRunner(fullCandidateResponse(repository, { upstream: "missing" }));
    try {
      const report = await suggestIncidentRanges({ repository, runner: fake.runner });

      expect(report.candidates).toEqual([expect.objectContaining({ id: "HEAD_PARENT", from: parent, to: head })]);
      expect(report.observations).toEqual([
        expect.objectContaining({ source: "LOCAL_UPSTREAM_MERGE_BASE", status: "NOT_AVAILABLE" }),
        expect.objectContaining({ source: "HEAD_PARENT", status: "CANDIDATE_AVAILABLE" })
      ]);
      expect(fake.calls.some((command) => commandTail(command).some((argument) => argument.includes("refs/remotes/origin/main")))).toBe(false);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("does not invent a range when HEAD has no parent and the upstream merge-base is HEAD", async () => {
    const repository = fakeRepositoryDirectory("no-range");
    const fake = fakeRunner(fullCandidateResponse(repository, { upstream: "head", parent: "missing" }));
    try {
      const report = await suggestIncidentRanges({ repository, runner: fake.runner });

      expect(report.candidates).toEqual([]);
      expect(report.automaticSelection).toBe("NONE");
      expect(report.observations).toEqual([
        expect.objectContaining({ source: "LOCAL_UPSTREAM_MERGE_BASE", status: "NO_RANGE" }),
        expect.objectContaining({ source: "HEAD_PARENT", status: "NO_RANGE" })
      ]);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("rejects unsafe directories and non-worktree responses before offering candidates", async () => {
    const root = fakeRepositoryDirectory("reject");
    const unrelatedRoot = fakeRepositoryDirectory("unrelated-root");
    const file = join(root, "not-a-directory");
    writeFileSync(file, "not a directory", "utf8");
    const neverCalled = fakeRunner(() => {
      throw new Error("runner should not be called");
    });
    let escapedRootCalls: readonly IncidentIntakeGitCommand[] | undefined;
    let escapedRootError: unknown;
    try {
      await expect(suggestIncidentRanges({ repository: file, runner: neverCalled.runner })).rejects.toMatchObject({
        name: "IncidentIntakeError",
        code: "UNSAFE_REPOSITORY_PATH"
      } satisfies Partial<IncidentIntakeError>);
      expect(neverCalled.calls).toEqual([]);

      const nonGit = fakeRunner((command) => {
        const tail = commandTail(command);
        if (tail.join(" ") === "rev-parse --is-inside-work-tree") return response({ stdout: "false\n" });
        throw new Error(`Unexpected Git command: ${tail.join(" ")}`);
      });
      await expect(suggestIncidentRanges({ repository: root, runner: nonGit.runner })).rejects.toMatchObject({
        name: "IncidentIntakeError",
        code: "NOT_A_GIT_WORKTREE"
      } satisfies Partial<IncidentIntakeError>);

      const escapedRoot = fakeRunner((command) => {
        const tail = commandTail(command);
        if (tail.join(" ") === "rev-parse --is-inside-work-tree") return response({ stdout: "true\n" });
        if (tail.join(" ") === "rev-parse --show-toplevel") return response({ stdout: `${unrelatedRoot}\n` });
        throw new Error(`Unexpected Git command: ${tail.join(" ")}`);
      });
      escapedRootCalls = escapedRoot.calls;
      await expect(suggestIncidentRanges({ repository: root, runner: escapedRoot.runner }).catch((error: unknown) => {
        escapedRootError = error;
        throw error;
      })).rejects.toMatchObject({
        name: "IncidentIntakeError",
        code: "UNSAFE_REPOSITORY_PATH"
      } satisfies Partial<IncidentIntakeError>);
    } catch (error) {
      if (escapedRootCalls !== undefined) {
        emitIntakeIdentityDiagnostics({ root, reportedRoot: unrelatedRoot, calls: escapedRootCalls, error: { assertion: error, rejection: escapedRootError } });
      }
      throw error;
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(unrelatedRoot, { recursive: true, force: true });
    }
  });

  it("rejects unavailable or oversized Git responses instead of treating them as repository facts", async () => {
    const repository = fakeRepositoryDirectory("unavailable");
    const fake = fakeRunner(() => response({ outputLimitExceeded: true, stdout: "x".repeat(70 * 1024) }));
    try {
      await expect(suggestIncidentRanges({ repository, runner: fake.runner })).rejects.toMatchObject({
        name: "IncidentIntakeError",
        code: "GIT_UNAVAILABLE"
      } satisfies Partial<IncidentIntakeError>);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("uses an existing local tracking ref with the hardened default runner and never needs a reachable remote", async () => {
    const repository = fakeRepositoryDirectory("real-git");
    try {
      git(repository, ["init"]);
      git(repository, ["config", "user.email", "faultline@example.invalid"]);
      git(repository, ["config", "user.name", "FaultLine intake test"]);
      const base = commit(repository, "good", "base");
      const current = commit(repository, "bad", "current");
      const branch = git(repository, ["branch", "--show-current"]);
      git(repository, ["remote", "add", "origin", "https://127.0.0.1:1/never-contacted.git"]);
      git(repository, ["update-ref", `refs/remotes/origin/${branch}`, base]);
      git(repository, ["config", `branch.${branch}.remote`, "origin"]);
      git(repository, ["config", `branch.${branch}.merge`, `refs/heads/${branch}`]);

      const report = await suggestIncidentRanges({ repository });
      expect(report.head).toBe(current);
      expect(report.candidates).toContainEqual(expect.objectContaining({
        id: "LOCAL_UPSTREAM_MERGE_BASE",
        from: base,
        to: current,
        evidence: expect.objectContaining({ remoteContacted: false })
      }));
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("keeps a malformed local upstream out of the argv for later Git commands", async () => {
    const repository = fakeRepositoryDirectory("unsafe-upstream");
    const fake = fakeRunner(fullCandidateResponse(repository, { upstream: "invalid" }));
    try {
      const report = await suggestIncidentRanges({ repository, runner: fake.runner });
      expect(report.candidates).toEqual([expect.objectContaining({ id: "HEAD_PARENT" })]);
      expect(report.observations[0]).toMatchObject({ status: "NOT_AVAILABLE" });
      expect(fake.calls.flatMap((command) => commandTail(command))).not.toContain("--unsafe^{commit}");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });
});
