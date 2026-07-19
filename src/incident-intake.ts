import { spawn } from "node:child_process";
import { lstatSync, realpathSync, statSync } from "node:fs";
import { dirname, isAbsolute, relative, resolve } from "node:path";
import { DOCTOR_SAFE_GIT_CONFIG } from "./doctor.js";

/**
 * Read-only local Git observations used to help a human start an incident.
 * This module deliberately does not fetch, query a forge, parse a CI log, or
 * select a range. Its small candidate set is a starting point for review.
 */
export const INCIDENT_INTAKE_SCHEMA_VERSION = "faultline.incident-intake.v1" as const;
export const INCIDENT_INTAKE_COMMAND_TIMEOUT_MS = 5_000;
export const INCIDENT_INTAKE_MAX_OUTPUT_BYTES = 64 * 1024;
export const INCIDENT_INTAKE_MAX_CANDIDATES = 2;

const GIT_OBJECT_ID = /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/;
const SAFE_REF_NAME = /^refs\/(?!.*(?:\.\.|@\{|[~^:?*\[\\\s]|\/{2}|\/$|\.$))[A-Za-z0-9!#$%&'()+,./;=@_-]+$/;

export type IncidentIntakeErrorCode =
  | "UNSAFE_REPOSITORY_PATH"
  | "NOT_A_GIT_WORKTREE"
  | "GIT_UNAVAILABLE"
  | "INVALID_GIT_RESPONSE"
  | "HEAD_UNAVAILABLE";

export class IncidentIntakeError extends Error {
  readonly code: IncidentIntakeErrorCode;

  constructor(code: IncidentIntakeErrorCode, message: string) {
    super(message);
    this.name = "IncidentIntakeError";
    this.code = code;
  }
}

export type IncidentIntakeGitCommand = {
  readonly executable: "git";
  readonly arguments: readonly string[];
  readonly timeoutMs: number;
  readonly maxOutputBytes: number;
};

export type IncidentIntakeGitCommandResult = {
  readonly exitCode: number | null;
  readonly stdout: string;
  readonly stderr: string;
  readonly timedOut: boolean;
  readonly outputLimitExceeded: boolean;
};

/** Injectable for tests and product hosts. FaultLine still constructs every Git argv itself. */
export type IncidentIntakeGitRunner = {
  run(command: IncidentIntakeGitCommand): Promise<IncidentIntakeGitCommandResult> | IncidentIntakeGitCommandResult;
};

export type IncidentRangeConfidence = "MEDIUM" | "LOW";

export type IncidentRangeCandidate = {
  readonly id: "LOCAL_UPSTREAM_MERGE_BASE" | "HEAD_PARENT";
  /** Directly maps to `fl investigate --from`. Always a verified local commit id. */
  readonly from: string;
  /** Directly maps to `fl investigate --to`. Always the observed local HEAD. */
  readonly to: string;
  readonly confidence: IncidentRangeConfidence;
  readonly reason: string;
  /** A caller must render this as a review/approval decision, not an automatic choice. */
  readonly requiresHumanApproval: true;
  readonly evidence:
    | {
      readonly source: "LOCAL_UPSTREAM_MERGE_BASE";
      readonly upstreamRef: string;
      readonly upstreamCommit: string;
      readonly mergeBase: string;
      readonly remoteContacted: false;
    }
    | {
      readonly source: "HEAD_PARENT";
      readonly parent: string;
    };
};

export type IncidentIntakeSourceObservation = {
  readonly source: "LOCAL_UPSTREAM_MERGE_BASE" | "HEAD_PARENT";
  readonly status: "CANDIDATE_AVAILABLE" | "NOT_AVAILABLE" | "NO_RANGE" | "DUPLICATE_RANGE";
  readonly reason: string;
};

export type FaultLineIncidentIntake = {
  readonly schemaVersion: typeof INCIDENT_INTAKE_SCHEMA_VERSION;
  /** Absolute requested directory after real-directory validation. */
  readonly repository: string;
  /** Absolute Git worktree root after real-directory validation. */
  readonly repositoryRoot: string;
  /** Observed local commit id for the current HEAD. */
  readonly head: string;
  /** FaultLine intentionally never picks one of the candidates. */
  readonly automaticSelection: "NONE";
  readonly selectionReason: string;
  readonly candidates: readonly IncidentRangeCandidate[];
  readonly observations: readonly IncidentIntakeSourceObservation[];
  readonly limitations: readonly string[];
};

export type SuggestIncidentRangesOptions = {
  repository?: string;
  runner?: IncidentIntakeGitRunner;
};

type CommandOutcome = {
  readonly result: IncidentIntakeGitCommandResult;
  readonly usable: boolean;
};

function hardenedGitEnvironment(): NodeJS.ProcessEnv {
  // Do not spread process.env: inherited GIT_* variables can redirect a
  // seemingly-local command to another worktree, object database, or helper.
  return {
    PATH: process.env.PATH ?? "",
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: process.platform === "win32" ? "NUL" : "/dev/null",
    GIT_ATTR_NOSYSTEM: "1",
    GIT_TERMINAL_PROMPT: "0",
    GIT_OPTIONAL_LOCKS: "0",
    GIT_NO_REPLACE_OBJECTS: "1",
    GIT_LFS_SKIP_SMUDGE: "1",
    GIT_ALLOW_PROTOCOL: "none",
    GIT_PROTOCOL_FROM_USER: "0",
    ...(process.platform === "win32" && process.env.SystemRoot ? { SystemRoot: process.env.SystemRoot } : {}),
    ...(process.platform === "win32" && process.env.ComSpec ? { ComSpec: process.env.ComSpec } : {}),
    ...(process.platform === "win32" && process.env.PATHEXT ? { PATHEXT: process.env.PATHEXT } : {})
  };
}

function fixedGitCommand(repository: string, argumentsList: readonly string[]): IncidentIntakeGitCommand {
  return Object.freeze({
    executable: "git",
    arguments: Object.freeze([...DOCTOR_SAFE_GIT_CONFIG, "-C", repository, ...argumentsList]),
    timeoutMs: INCIDENT_INTAKE_COMMAND_TIMEOUT_MS,
    maxOutputBytes: INCIDENT_INTAKE_MAX_OUTPUT_BYTES
  });
}

/**
 * Production runner for fixed local Git reads. It invokes no shell, preserves
 * no inherited Git environment, and kills a command that exceeds its bounded
 * output or time allowance.
 */
export function createNodeIncidentIntakeRunner(): IncidentIntakeGitRunner {
  return {
    run(command: IncidentIntakeGitCommand): Promise<IncidentIntakeGitCommandResult> {
      return new Promise((resolveResult) => {
        let settled = false;
        let timedOut = false;
        let outputLimitExceeded = false;
        let stdout = Buffer.alloc(0);
        let stderr = Buffer.alloc(0);
        let timeout: NodeJS.Timeout | undefined;

        const finish = (exitCode: number | null): void => {
          if (settled) return;
          settled = true;
          if (timeout !== undefined) clearTimeout(timeout);
          resolveResult({
            exitCode,
            stdout: stdout.toString("utf8"),
            stderr: stderr.toString("utf8"),
            timedOut,
            outputLimitExceeded
          });
        };

        let child;
        try {
          child = spawn(command.executable, [...command.arguments], {
            shell: false,
            windowsHide: true,
            stdio: ["ignore", "pipe", "pipe"],
            env: hardenedGitEnvironment()
          });
        } catch {
          finish(null);
          return;
        }

        const appendOutput = (stream: "stdout" | "stderr", chunk: Buffer): void => {
          const bytes = Buffer.from(chunk);
          const used = stdout.length + stderr.length;
          const remaining = command.maxOutputBytes - used;
          if (remaining <= 0) {
            outputLimitExceeded = true;
            child.kill("SIGKILL");
            return;
          }
          const retained = bytes.subarray(0, remaining);
          if (stream === "stdout") stdout = Buffer.concat([stdout, retained]);
          else stderr = Buffer.concat([stderr, retained]);
          if (bytes.length > remaining) {
            outputLimitExceeded = true;
            child.kill("SIGKILL");
          }
        };

        child.stdout?.on("data", (chunk: Buffer) => appendOutput("stdout", chunk));
        child.stderr?.on("data", (chunk: Buffer) => appendOutput("stderr", chunk));
        child.once("error", () => finish(null));
        child.once("close", (exitCode) => finish(exitCode));
        if (!settled) {
          timeout = setTimeout(() => {
            timedOut = true;
            child.kill("SIGKILL");
          }, command.timeoutMs);
        }
      });
    }
  };
}

function filesystemErrorCode(error: unknown): string {
  return error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
}

/**
 * Reject a terminal symlink supplied by a caller, then canonicalize platform
 * aliases such as macOS `/var` -> `/private/var`. Git reports canonical
 * worktree roots on those systems, so comparison must use the same form.
 */
function resolveRealDirectory(path: string, label: string): string {
  try {
    const stat = lstatSync(path);
    if (stat.isSymbolicLink() || !stat.isDirectory()) {
      throw new IncidentIntakeError("UNSAFE_REPOSITORY_PATH", `${label} must be a real directory, not a symlink or file.`);
    }
    return realpathSync(path);
  } catch (error) {
    if (error instanceof IncidentIntakeError) throw error;
    const availability = filesystemErrorCode(error) === "ENOENT" ? "does not exist" : "is unavailable";
    throw new IncidentIntakeError("UNSAFE_REPOSITORY_PATH", `${label} ${availability}.`);
  }
}

function isInsideDirectory(child: string, parent: string): boolean {
  // Git for Windows, Node's realpath implementation, and the Win32 API can
  // spell one local directory as `C:\\…`, `C:/…`, or `\\\\?\\C:\\…`. Normalize
  // those equivalent forms before asking path.relative() about containment.
  // Without this, a legitimate worktree is rejected only on some Node/runner
  // combinations even though Git just confirmed it is inside that worktree.
  const comparable = (value: string): string => {
    if (process.platform !== "win32") return value;
    return value
      .replace(/^\\\\\?\\UNC\\/i, "\\\\")
      .replace(/^\\\\\?\\/i, "")
      .replaceAll("/", "\\")
      .toLocaleLowerCase("en-US");
  };
  const pathFromParent = relative(comparable(parent), comparable(child));
  if (pathFromParent === "" || (!pathFromParent.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`) && pathFromParent !== ".." && !isAbsolute(pathFromParent))) {
    return true;
  }

  // Some Windows APIs preserve a short-name or volume-device spelling through
  // realpath. Compare directory identity while walking the actual requested
  // path as a final safe fallback; this is stricter than accepting Git's
  // string alone and still rejects an unrelated reported root.
  //
  // Skip the inode fallback when `ino` is 0: Win32 often reports `ino === 0`
  // for every path on a volume, which would otherwise treat unrelated temp
  // directories as identical and skip the UNSAFE_REPOSITORY_PATH rejection.
  try {
    const parentStat = statSync(parent);
    if (parentStat.ino === 0) return false;
    let current = child;
    for (let depth = 0; depth < 256; depth += 1) {
      const currentStat = statSync(current);
      if (currentStat.dev === parentStat.dev && currentStat.ino === parentStat.ino) return true;
      const next = dirname(current);
      if (next === current) return false;
      current = next;
    }
  } catch {
    // A path that cannot be inspected cannot establish the containment claim.
  }
  return false;
}

function normalizeRunnerResult(value: IncidentIntakeGitCommandResult): CommandOutcome {
  const stdout = typeof value.stdout === "string" ? value.stdout : "";
  const stderr = typeof value.stderr === "string" ? value.stderr : "";
  const outputLimitExceeded = Boolean(value.outputLimitExceeded)
    || Buffer.byteLength(stdout, "utf8") + Buffer.byteLength(stderr, "utf8") > INCIDENT_INTAKE_MAX_OUTPUT_BYTES;
  const validExitCode = value.exitCode === null || (typeof value.exitCode === "number" && Number.isInteger(value.exitCode));
  const result: IncidentIntakeGitCommandResult = {
    exitCode: validExitCode ? value.exitCode : null,
    stdout,
    stderr,
    timedOut: Boolean(value.timedOut),
    outputLimitExceeded
  };
  return {
    result,
    usable: result.exitCode === 0 && !result.timedOut && !result.outputLimitExceeded
  };
}

async function runCommand(runner: IncidentIntakeGitRunner, command: IncidentIntakeGitCommand): Promise<CommandOutcome> {
  try {
    return normalizeRunnerResult(await runner.run(command));
  } catch {
    return {
      result: { exitCode: null, stdout: "", stderr: "", timedOut: false, outputLimitExceeded: false },
      usable: false
    };
  }
}

function singleLine(value: string): string | null {
  const normalized = value.trim();
  return normalized && !/[\r\n]/.test(normalized) ? normalized : null;
}

async function requiredGitText(
  runner: IncidentIntakeGitRunner,
  repository: string,
  args: readonly string[],
  code: IncidentIntakeErrorCode,
  operation: string
): Promise<string> {
  const outcome = await runCommand(runner, fixedGitCommand(repository, args));
  if (!outcome.usable) {
    const unavailable = outcome.result.timedOut || outcome.result.outputLimitExceeded || outcome.result.exitCode === null;
    throw new IncidentIntakeError(
      unavailable ? "GIT_UNAVAILABLE" : code,
      `FaultLine could not safely inspect local Git state while ${operation}.`
    );
  }
  const value = singleLine(outcome.result.stdout);
  if (value === null) {
    throw new IncidentIntakeError("INVALID_GIT_RESPONSE", `Git returned an invalid response while ${operation}.`);
  }
  return value;
}

async function optionalGitText(
  runner: IncidentIntakeGitRunner,
  repository: string,
  args: readonly string[]
): Promise<{ readonly value: string | null; readonly unavailable: boolean }> {
  const outcome = await runCommand(runner, fixedGitCommand(repository, args));
  if (!outcome.usable) {
    return { value: null, unavailable: outcome.result.timedOut || outcome.result.outputLimitExceeded || outcome.result.exitCode === null };
  }
  return { value: singleLine(outcome.result.stdout), unavailable: false };
}

function assertObjectId(value: string, operation: string): string {
  if (!GIT_OBJECT_ID.test(value)) {
    throw new IncidentIntakeError("INVALID_GIT_RESPONSE", `Git returned an invalid commit id while ${operation}.`);
  }
  return value;
}

async function resolveRequiredCommit(
  runner: IncidentIntakeGitRunner,
  repository: string,
  revision: string,
  operation: string,
  code: IncidentIntakeErrorCode = "INVALID_GIT_RESPONSE"
): Promise<string> {
  const commit = await requiredGitText(runner, repository, ["rev-parse", "--verify", "--end-of-options", `${revision}^{commit}`], code, operation);
  return assertObjectId(commit, operation);
}

async function isVerifiedAncestor(
  runner: IncidentIntakeGitRunner,
  repository: string,
  ancestor: string,
  descendant: string
): Promise<boolean> {
  const outcome = await runCommand(runner, fixedGitCommand(repository, ["merge-base", "--is-ancestor", ancestor, descendant]));
  return outcome.usable;
}

async function inspectLocalUpstream(
  runner: IncidentIntakeGitRunner,
  repository: string,
  head: string
): Promise<{ readonly candidate: IncidentRangeCandidate | null; readonly observation: IncidentIntakeSourceObservation }> {
  // Do not abbreviate this result: a fully qualified local ref lets us reject
  // ambiguous shorthand before it is used as a later Git revision argument.
  const upstreamOutput = await optionalGitText(runner, repository, ["rev-parse", "--symbolic-full-name", "@{upstream}"]);
  if (upstreamOutput.value === null) {
    return {
      candidate: null,
      observation: {
        source: "LOCAL_UPSTREAM_MERGE_BASE",
        status: "NOT_AVAILABLE",
        reason: upstreamOutput.unavailable
          ? "FaultLine could not safely read a local upstream reference; no upstream range was proposed."
          : "No resolvable local upstream reference was configured for the current HEAD; no upstream range was proposed."
      }
    };
  }
  const upstreamRef = upstreamOutput.value;
  if (!SAFE_REF_NAME.test(upstreamRef)) {
    return {
      candidate: null,
      observation: {
        source: "LOCAL_UPSTREAM_MERGE_BASE",
        status: "NOT_AVAILABLE",
        reason: "Git returned an unsafe local upstream reference; FaultLine refused to use it."
      }
    };
  }

  const upstreamCommitOutput = await optionalGitText(
    runner,
    repository,
    ["rev-parse", "--verify", "--end-of-options", `${upstreamRef}^{commit}`]
  );
  if (upstreamCommitOutput.value === null || !GIT_OBJECT_ID.test(upstreamCommitOutput.value)) {
    return {
      candidate: null,
      observation: {
        source: "LOCAL_UPSTREAM_MERGE_BASE",
        status: "NOT_AVAILABLE",
        reason: "The locally configured upstream could not be resolved to a safe local commit; no upstream range was proposed."
      }
    };
  }
  const upstreamCommit = upstreamCommitOutput.value;

  const mergeBaseOutput = await optionalGitText(runner, repository, ["merge-base", head, upstreamCommit]);
  if (mergeBaseOutput.value === null || !GIT_OBJECT_ID.test(mergeBaseOutput.value)) {
    return {
      candidate: null,
      observation: {
        source: "LOCAL_UPSTREAM_MERGE_BASE",
        status: "NOT_AVAILABLE",
        reason: "FaultLine could not resolve a safe local merge-base for the configured upstream; no upstream range was proposed."
      }
    };
  }
  const mergeBase = mergeBaseOutput.value;
  if (mergeBase === head) {
    return {
      candidate: null,
      observation: {
        source: "LOCAL_UPSTREAM_MERGE_BASE",
        status: "NO_RANGE",
        reason: "The local upstream merge-base is the current HEAD, so it does not bound any newer commits."
      }
    };
  }
  if (!await isVerifiedAncestor(runner, repository, mergeBase, head)) {
    return {
      candidate: null,
      observation: {
        source: "LOCAL_UPSTREAM_MERGE_BASE",
        status: "NOT_AVAILABLE",
        reason: "FaultLine could not verify the local merge-base as an ancestor of HEAD; no upstream range was proposed."
      }
    };
  }

  return {
    candidate: {
      id: "LOCAL_UPSTREAM_MERGE_BASE",
      from: mergeBase,
      to: head,
      confidence: "MEDIUM",
      reason: `This range starts at the locally resolved merge-base with ${upstreamRef}. FaultLine did not contact that remote; review that potentially stale local ref before freezing a witness.`,
      requiresHumanApproval: true,
      evidence: {
        source: "LOCAL_UPSTREAM_MERGE_BASE",
        upstreamRef,
        upstreamCommit,
        mergeBase,
        remoteContacted: false
      }
    },
    observation: {
      source: "LOCAL_UPSTREAM_MERGE_BASE",
      status: "CANDIDATE_AVAILABLE",
      reason: `A locally resolved upstream merge-base candidate was found from ${upstreamRef}; it still requires human review.`
    }
  };
}

async function inspectHeadParent(
  runner: IncidentIntakeGitRunner,
  repository: string,
  head: string,
  existingCandidates: readonly IncidentRangeCandidate[]
): Promise<{ readonly candidate: IncidentRangeCandidate | null; readonly observation: IncidentIntakeSourceObservation }> {
  // `HEAD^` means its first parent. `HEAD^{}` would merely peel a tag and can
  // resolve back to HEAD itself, which is not a usable incident boundary.
  const parentOutput = await optionalGitText(runner, repository, ["rev-parse", "--verify", "--end-of-options", "HEAD^"]);
  if (parentOutput.value === null || !GIT_OBJECT_ID.test(parentOutput.value)) {
    return {
      candidate: null,
      observation: {
        source: "HEAD_PARENT",
        status: parentOutput.unavailable ? "NOT_AVAILABLE" : "NO_RANGE",
        reason: parentOutput.unavailable
          ? "FaultLine could not safely read HEAD's first parent; no parent fallback was proposed."
          : "The current HEAD has no resolvable first parent, so no parent fallback was proposed."
      }
    };
  }
  const parent = parentOutput.value;
  if (!await isVerifiedAncestor(runner, repository, parent, head)) {
    return {
      candidate: null,
      observation: {
        source: "HEAD_PARENT",
        status: "NOT_AVAILABLE",
        reason: "FaultLine could not verify HEAD's first parent as an ancestor; no parent fallback was proposed."
      }
    };
  }
  if (existingCandidates.some((candidate) => candidate.from === parent && candidate.to === head)) {
    return {
      candidate: null,
      observation: {
        source: "HEAD_PARENT",
        status: "DUPLICATE_RANGE",
        reason: "HEAD's parent fallback is identical to an already listed local-upstream candidate."
      }
    };
  }
  return {
    candidate: {
      id: "HEAD_PARENT",
      from: parent,
      to: head,
      confidence: "LOW",
      reason: "This is only the immediately preceding local commit. It is a conservative fallback, not evidence that the regression began there.",
      requiresHumanApproval: true,
      evidence: { source: "HEAD_PARENT", parent }
    },
    observation: {
      source: "HEAD_PARENT",
      status: "CANDIDATE_AVAILABLE",
      reason: "A one-commit parent fallback was found; it still requires human review."
    }
  };
}

/**
 * Suggest, but never choose, at most two locally provable Git ranges. The
 * only possible inputs are the current local HEAD, its locally configured
 * upstream ref, and HEAD's first parent. No command can fetch or contact a
 * remote, and all returned revisions are resolved commit object ids.
 */
export async function suggestIncidentRanges(options: SuggestIncidentRangesOptions = {}): Promise<FaultLineIncidentIntake> {
  const repository = resolveRealDirectory(resolve(options.repository ?? process.cwd()), "Requested repository directory");
  const runner = options.runner ?? createNodeIncidentIntakeRunner();

  const insideWorkTree = await requiredGitText(
    runner,
    repository,
    ["rev-parse", "--is-inside-work-tree"],
    "NOT_A_GIT_WORKTREE",
    "checking whether the requested directory is a Git worktree"
  );
  if (insideWorkTree !== "true") {
    throw new IncidentIntakeError("NOT_A_GIT_WORKTREE", "The requested directory is not inside a non-bare Git worktree.");
  }

  const reportedRoot = await requiredGitText(
    runner,
    repository,
    ["rev-parse", "--show-toplevel"],
    "NOT_A_GIT_WORKTREE",
    "resolving the Git worktree root"
  );
  if (!isAbsolute(reportedRoot)) {
    throw new IncidentIntakeError("INVALID_GIT_RESPONSE", "Git did not return an absolute worktree root.");
  }
  const repositoryRoot = resolveRealDirectory(resolve(reportedRoot), "Git worktree root");
  if (!isInsideDirectory(repository, repositoryRoot)) {
    throw new IncidentIntakeError("UNSAFE_REPOSITORY_PATH", "The requested directory falls outside Git's reported worktree root.");
  }

  const head = await resolveRequiredCommit(runner, repositoryRoot, "HEAD", "resolving current HEAD", "HEAD_UNAVAILABLE");
  const upstream = await inspectLocalUpstream(runner, repositoryRoot, head);
  const candidates = upstream.candidate === null ? [] : [upstream.candidate];
  const parent = await inspectHeadParent(runner, repositoryRoot, head, candidates);
  if (parent.candidate !== null) candidates.push(parent.candidate);

  return {
    schemaVersion: INCIDENT_INTAKE_SCHEMA_VERSION,
    repository,
    repositoryRoot,
    head,
    automaticSelection: "NONE",
    selectionReason: "FaultLine found local Git candidates only. A human must review and explicitly choose --from and --to before any witness is frozen or investigation runs.",
    candidates: candidates.slice(0, INCIDENT_INTAKE_MAX_CANDIDATES),
    observations: [upstream.observation, parent.observation],
    limitations: [
      "FaultLine inspected only local Git metadata and did not contact a remote, fetch, inspect a pull request, parse CI, or execute a witness.",
      "A local upstream ref can be stale or unrelated to the failure. A parent fallback covers only one commit and is not a diagnosis.",
      "No suggested range is selected automatically; a human must review the exact commits and freeze an approved witness separately."
    ]
  };
}
