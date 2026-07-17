import { randomUUID } from "node:crypto";
import { spawn } from "node:child_process";
import { lstatSync, readFileSync } from "node:fs";
import { link, lstat, mkdir, mkdtemp, readFile, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, isAbsolute, join, parse, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson, sha256 } from "./canonical.js";
import { redactText } from "./redaction.js";
import {
  auditSandboxPlan,
  createSandboxPlan,
  executeSandboxPlan,
  validateSandboxPlanAudit,
  type SandboxCommandRunner,
  type SandboxExecutionResult,
  type SandboxLimits,
  type SandboxPlanAudit,
  type SandboxPlanRequest
} from "./sandbox.js";
import {
  FrozenWitnessSchema,
  verifyFrozenWitnessRecord,
  type FrozenWitness,
  type WitnessLockVerification
} from "./witness-lock.js";
import { resolveSafeDirectorySegment } from "./safe-directory.js";

/**
 * A Git-only counterfactual recorder.  It intentionally says nothing about
 * native Codex lifecycle interception: its evidence is the supplied commits,
 * an independently frozen witness, and actual detached-worktree executions.
 */
export const GIT_MINIMIZATION_SCHEMA_VERSION = "faultline.git-minimization.v1" as const;
export const MINIMIZATION_CERTIFICATION_EXECUTIONS = 3 as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const GIT_OBJECT_ID = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/;
const SAFE_GIT_REVISION = /^(?!-)[^\0\r\n]{1,512}$/;
const SAFE_OVERLAY_PATH = /^[^\\/\0]+(?:\/[^\\/\0]+)*$/;
const MAX_GIT_OUTPUT_BYTES = 64 * 1024 * 1024;
const EVIDENCE_LOG_PREVIEW_BYTES = 16 * 1024;
const MAX_MINIMIZATION_RESULT_BYTES = 128 * 1024 * 1024;
const DANGEROUS_GIT_ATTRIBUTE = /(?:^|\s)filter=[^\s]+/m;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const GitObjectIdSchema = z.string().regex(GIT_OBJECT_ID, "expected a 40- or 64-character lowercase Git object id");
const GitRevisionSchema = z.string().regex(SAFE_GIT_REVISION, "revision must be non-empty, cannot start with '-', and cannot contain NUL or line breaks");
const TimestampSchema = z.string().datetime({ offset: true });

/** A PASS verdict is legitimate either as a legacy unstructured exit-zero or a structured witness-result predicate pass. */
const PASS_VERDICT_REASONS = new Set(["PREDICATE_PASS"]);
/** Certified minimization accepts structured predicate failures only — never legacy EXIT_NONZERO. */
const FAIL_VERDICT_REASONS = new Set(["PREDICATE_FAIL"]);

export const GitMinimizationSandboxSchema = z.object({
  mode: z.enum(["DOCKER_ISOLATED", "UNSAFE_LOCAL"]).default("DOCKER_ISOLATED"),
  image: z.string().min(1).max(1_024).optional(),
  environment: z.record(z.string()).optional(),
  allowedEnvironment: z.array(z.string().min(1).max(128)).max(64).optional(),
  limits: z.object({
    timeoutMs: z.number().int().positive().optional(),
    maxOutputBytes: z.number().int().positive().optional(),
    cpuCount: z.number().int().positive().optional(),
    memoryBytes: z.number().int().positive().optional(),
    pidsLimit: z.number().int().positive().optional(),
    tmpfsBytes: z.number().int().positive().optional()
  }).strict().optional(),
  allowUnsafeLocal: z.literal(true).optional()
}).strict();

/** The execution budget covers actual sandbox invocations, not Git apply attempts. */
export const GitMinimizationBudgetSchema = z.object({
  maxExecutions: z.number().int().min(1).max(1_024).default(128)
}).strict().default({ maxExecutions: 128 });

/** Serializable request data. A runner is injected separately and is never persisted. */
export const GitMinimizationRequestSchema = z.object({
  repository: z.string().min(1).max(16_384),
  before: GitRevisionSchema,
  after: GitRevisionSchema,
  frozenWitness: FrozenWitnessSchema,
  /** A separately recorded freeze digest is mandatory for proof-grade use. */
  expectedFrozenDigest: DigestSchema,
  sandbox: GitMinimizationSandboxSchema,
  budget: GitMinimizationBudgetSchema
}).strict();

export type GitMinimizationSandbox = z.output<typeof GitMinimizationSandboxSchema>;
export type GitMinimizationBudget = z.output<typeof GitMinimizationBudgetSchema>;
export type GitMinimizationRequestData = z.output<typeof GitMinimizationRequestSchema>;

export interface GitMinimizationRequest {
  readonly repository: string;
  readonly before: string;
  readonly after: string;
  readonly frozenWitness: FrozenWitness;
  readonly expectedFrozenDigest: string;
  readonly sandbox: z.input<typeof GitMinimizationSandboxSchema>;
  readonly budget?: z.input<typeof GitMinimizationBudgetSchema>;
  /** Tests and controlled deployments can inject a Docker-command runner. */
  readonly runner?: SandboxCommandRunner;
}

export const GitCounterfactualStateSchema = z.object({
  commit: GitObjectIdSchema,
  tree: GitObjectIdSchema
}).strict();

export const GitPatchUnitSchema = z.object({
  /** Stable digest of ordinal, exact path bytes, and exact --binary patch bytes. */
  id: DigestSchema,
  ordinal: z.number().int().nonnegative(),
  changeKind: z.enum(["ADD", "DELETE", "MODIFY", "TYPECHANGE", "UNKNOWN"]),
  /** Display-only UTF-8 path. Non-UTF-8 paths are rejected before minimization. */
  path: z.string().min(1),
  /** Keeps the path evidence byte-exact even when a consumer re-renders it. */
  pathBytesBase64: z.string().min(1),
  pathDigest: DigestSchema,
  patchDigest: DigestSchema,
  patchBytes: z.number().int().positive(),
  binarySafe: z.literal(true)
}).strict();

export const MaterializedFrozenOverlaySchema = z.object({
  path: z.string().min(1),
  bytesDigest: DigestSchema,
  bytesLength: z.number().int().nonnegative()
}).strict();

const SandboxAuditSchema = z.object({
  kind: z.enum(["DOCKER_ISOLATED", "UNSAFE_LOCAL"]),
  witnessDigest: DigestSchema,
  commandDigest: DigestSchema,
  environmentPolicyDigest: DigestSchema,
  policyDigest: DigestSchema,
  environment: z.object({
    fixedKeys: z.array(z.string()),
    allowedKeys: z.array(z.string()),
    passed: z.array(z.object({ key: z.string(), valueDigest: DigestSchema }).strict()),
    redactedKeys: z.array(z.string())
  }).strict(),
  runtime: z.object({
    image: z.string().nullable(),
    entrypoint: z.string().nullable(),
    network: z.literal("none").nullable(),
    rootFilesystemReadOnly: z.boolean(),
    user: z.string().nullable(),
    capDropAll: z.boolean(),
    noNewPrivileges: z.boolean(),
    pull: z.literal("never").nullable(),
    limits: z.object({
      timeoutMs: z.number().int().positive(),
      maxOutputBytes: z.number().int().positive(),
      cpuCount: z.number().int().positive(),
      memoryBytes: z.number().int().positive(),
      pidsLimit: z.number().int().positive(),
      tmpfsBytes: z.number().int().positive()
    }).strict()
  }).strict()
}).strict();

export const GitPatchApplicationSchema = z.object({
  direction: z.enum(["FORWARD_FROM_BEFORE", "REVERSE_FROM_AFTER"]),
  base: GitCounterfactualStateSchema,
  selectedUnitIds: z.array(DigestSchema),
  candidatePatchDigest: DigestSchema,
  candidatePatchBytes: z.number().int().nonnegative(),
  status: z.enum(["NO_PATCHES", "APPLIED", "CONFLICT", "ERROR"]),
  stdoutDigest: DigestSchema,
  stdoutBytes: z.number().int().nonnegative(),
  stderrDigest: DigestSchema,
  stderrBytes: z.number().int().nonnegative(),
  error: z.string().nullable()
}).strict();

export const GitMinimizationRunFactSchema = z.object({
  schemaVersion: z.literal(GIT_MINIMIZATION_SCHEMA_VERSION),
  runId: DigestSchema,
  /** Unique nonce-bound identifier proving the certification attempts are distinct. */
  executionId: DigestSchema,
  /** Random recorder nonce retained so offline verification can reconstruct executionId. */
  executionNonce: z.string().uuid(),
  role: z.enum([
    "BASELINE_BEFORE",
    "FULL_PATCH",
    "DELTA_SUBSET",
    "DELTA_COMPLEMENT",
    "ONE_MINIMAL",
    "SUFFICIENCY_CERTIFICATION",
    "NECESSITY_CERTIFICATION"
  ]),
  roleAttempt: z.number().int().positive(),
  candidateUnitIds: z.array(DigestSchema),
  worktreeDigest: DigestSchema,
  frozenDigest: DigestSchema,
  witnessDigest: DigestSchema,
  startedAt: TimestampSchema,
  finishedAt: TimestampSchema,
  durationMs: z.number().int().nonnegative(),
  overlays: z.array(MaterializedFrozenOverlaySchema),
  application: GitPatchApplicationSchema,
  sandbox: SandboxAuditSchema.nullable(),
  result: z.object({
    kind: z.enum(["DOCKER_ISOLATED", "UNSAFE_LOCAL"]),
    executor: z.enum(["NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL"]),
    verdict: z.enum(["PASS", "FAIL", "ERROR", "INAPPLICABLE"]),
    reason: z.enum([
      "EXIT_ZERO",
      "EXIT_NONZERO",
      "PREDICATE_PASS",
      "PREDICATE_FAIL",
      "INCOMPATIBLE_STATE",
      "HARNESS_ERROR",
      "EXIT_NONZERO_UNSTRUCTURED",
      "EXIT_ZERO_UNSTRUCTURED",
      "TIMEOUT",
      "OUTPUT_LIMIT_EXCEEDED",
      "SANDBOX_UNAVAILABLE",
      "WITNESS_SETUP_ERROR",
      "RUNNER_FAILURE",
      "UNSAFE_LOCAL_NOT_PROOF"
    ]),
    exitCode: z.number().int().nullable(),
    signal: z.string().nullable(),
    outputTruncated: z.boolean(),
    stdoutDigest: DigestSchema,
    stdoutBytes: z.number().int().nonnegative(),
    stdoutPreview: z.string().max(EVIDENCE_LOG_PREVIEW_BYTES * 2),
    stdoutPreviewTruncated: z.boolean(),
    stdoutRedacted: z.boolean(),
    stderrDigest: DigestSchema,
    stderrBytes: z.number().int().nonnegative(),
    stderrPreview: z.string().max(EVIDENCE_LOG_PREVIEW_BYTES * 2),
    stderrPreviewTruncated: z.boolean(),
    stderrRedacted: z.boolean()
  }).strict().nullable(),
  outcome: z.enum(["PASS", "FAIL", "UNRESOLVED"]),
  note: z.string()
}).strict();

export const GitMinimizationAttemptSchema = z.object({
  ordinal: z.number().int().positive(),
  phase: z.enum([
    "BASELINE",
    "FULL_RANGE",
    "DELTA_SUBSET",
    "DELTA_COMPLEMENT",
    "ONE_MINIMAL",
    "SUFFICIENCY_CERTIFICATION",
    "NECESSITY_CERTIFICATION"
  ]),
  candidateUnitIds: z.array(DigestSchema),
  outcome: z.enum(["PASS", "FAIL", "UNRESOLVED", "NOT_RUN"]),
  runId: DigestSchema.nullable(),
  note: z.string()
}).strict();

const WitnessVerificationSummarySchema = z.object({
  valid: z.boolean(),
  errors: z.array(z.string()),
  frozenDigest: DigestSchema.nullable(),
  witnessDigest: DigestSchema.nullable(),
  externalDigestStatus: z.enum(["NOT_PROVIDED", "MATCH", "MISMATCH"]),
  approval: z.object({ actor: z.string(), approvedAt: TimestampSchema }).nullable()
}).strict();

export const GitMinimizationCertificateSchema = z.object({
  direction: z.enum(["FORWARD_FROM_BEFORE", "REVERSE_FROM_AFTER"]),
  expectedOutcome: z.enum(["PASS", "FAIL"]),
  requiredExecutions: z.literal(MINIMIZATION_CERTIFICATION_EXECUTIONS),
  runIds: z.array(DigestSchema).max(MINIMIZATION_CERTIFICATION_EXECUTIONS),
  executionIds: z.array(DigestSchema).max(MINIMIZATION_CERTIFICATION_EXECUTIONS),
  status: z.enum(["CERTIFIED", "NOT_CERTIFIED", "NOT_RUN"]),
  note: z.string()
}).strict();

export const GitMinimizationResultSchema = z.object({
  schemaVersion: z.literal(GIT_MINIMIZATION_SCHEMA_VERSION),
  recorder: z.literal("git-diff-counterfactual-minimization"),
  nativeCodexInterception: z.literal(false),
  status: z.enum([
    "COMPLETED",
    "INVALID_REQUEST",
    "INVALID_WITNESS",
    "RANGE_ERROR",
    "PATCH_ERROR",
    "NO_PATCHES",
    "BASELINE_NOT_PASS",
    "FULL_PATCH_NOT_FAIL",
    "BUDGET_EXHAUSTED",
    "SANDBOX_UNAVAILABLE",
    "UNSAFE_LOCAL_INAPPLICABLE",
    "EXECUTION_ERROR",
    "CERTIFICATION_FAILED"
  ]),
  repository: z.string().nullable(),
  before: GitCounterfactualStateSchema.nullable(),
  after: GitCounterfactualStateSchema.nullable(),
  witness: WitnessVerificationSummarySchema.nullable(),
  budget: z.object({ usedExecutions: z.number().int().nonnegative(), maxExecutions: z.number().int().positive() }).strict(),
  patchUnits: z.array(GitPatchUnitSchema),
  candidateUnitIds: z.array(DigestSchema),
  attempts: z.array(GitMinimizationAttemptSchema),
  runs: z.array(GitMinimizationRunFactSchema),
  minimality: z.object({
    oneMinimal: z.boolean(),
    reason: z.string()
  }).strict(),
  certification: z.object({
    sufficiency: GitMinimizationCertificateSchema,
    necessity: GitMinimizationCertificateSchema
  }).strict(),
  proof: z.object({
    requiresDockerIsolation: z.literal(true),
    dockerIsolated: z.boolean(),
    executionTrust: z.enum(["NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL"]),
    sufficiencyCertified: z.boolean(),
    necessityCertified: z.boolean(),
    isProof: z.boolean(),
    reason: z.string()
  }).strict(),
  errors: z.array(z.string())
}).strict();

export type GitCounterfactualState = z.infer<typeof GitCounterfactualStateSchema>;
export type GitPatchUnit = z.infer<typeof GitPatchUnitSchema>;
export type MaterializedFrozenOverlay = z.infer<typeof MaterializedFrozenOverlaySchema>;
export type GitPatchApplication = z.infer<typeof GitPatchApplicationSchema>;
export type GitMinimizationRunFact = z.infer<typeof GitMinimizationRunFactSchema>;
export type GitMinimizationAttempt = z.infer<typeof GitMinimizationAttemptSchema>;
export type GitMinimizationCertificate = z.infer<typeof GitMinimizationCertificateSchema>;
export type GitMinimizationResult = z.infer<typeof GitMinimizationResultSchema>;

export type GitMinimizationExternalDigestStatus = "NOT_PROVIDED" | "MATCH" | "MISMATCH";

/** Offline, self-consistency verification for one standalone minimization record. */
export interface GitMinimizationVerification {
  readonly valid: boolean;
  readonly errors: readonly string[];
  /** Canonical digest of the parsed record; retain it outside the record for tamper detection. */
  readonly resultDigest: string | null;
  readonly externalDigestStatus: GitMinimizationExternalDigestStatus;
  readonly result?: GitMinimizationResult;
}

export interface WrittenGitMinimizationResult {
  readonly path: string;
  readonly resultDigest: string;
}

type ProcessResult = {
  exitCode: number | null;
  stdout: Buffer;
  stderr: Buffer;
  error?: string;
};

type PatchUnitInternal = GitPatchUnit & { readonly bytes: Buffer };
type Direction = GitPatchApplication["direction"];
type RunRole = GitMinimizationRunFact["role"];
type AttemptPhase = GitMinimizationAttempt["phase"];
type ProbeOutcome = GitMinimizationAttempt["outcome"];

type Probe = {
  readonly outcome: ProbeOutcome;
  readonly run: GitMinimizationRunFact | null;
  readonly note: string;
};

/** Keep host-side Git inspection and worktree materialization non-interactive and configuration-isolated. */
function hardenedGitEnvironment(): NodeJS.ProcessEnv {
  const environment: NodeJS.ProcessEnv = {
    PATH: process.env.PATH ?? "",
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: join(tmpdir(), `faultline-empty-git-config-${randomUUID()}`),
    GIT_TERMINAL_PROMPT: "0",
    GIT_OPTIONAL_LOCKS: "0",
    GIT_NO_REPLACE_OBJECTS: "1",
    GIT_LFS_SKIP_SMUDGE: "1",
    // FaultLine never fetches/clones while preparing a counterfactual. Leave
    // no usable transport protocol for repository-local configuration.
    GIT_ALLOW_PROTOCOL: "none"
  };
  if (process.platform === "win32") {
    if (process.env.SystemRoot) environment.SystemRoot = process.env.SystemRoot;
    if (process.env.ComSpec) environment.ComSpec = process.env.ComSpec;
    if (process.env.PATHEXT) environment.PATHEXT = process.env.PATHEXT;
  }
  return environment;
}

function hardenedGitArguments(repository: string, args: readonly string[]): string[] {
  return [
    "-c", "core.hooksPath=/nonexistent/faultline-hooks",
    "-c", "core.autocrlf=false",
    // Do not let a repository's cached file-system watcher/index state affect
    // a counterfactual materialization or the Git facts we persist.
    "-c", "core.fsmonitor=false",
    "-c", "core.useBuiltinFSMonitor=false",
    "-c", "core.untrackedCache=false",
    "-c", "core.preloadIndex=false",
    "-c", "filter.lfs.process=",
    "-c", "filter.lfs.smudge=",
    "-c", "filter.lfs.required=false",
    "-c", "diff.external=",
    // Git submodules and transports are outside this offline replay contract;
    // do not inherit repository-local recursion or protocol preferences.
    "-c", "submodule.recurse=false",
    "-c", "fetch.recurseSubmodules=false",
    "-c", "protocol.allow=never",
    "-c", "protocol.file.allow=never",
    "-c", "protocol.ext.allow=never",
    "-c", "protocol.git.allow=never",
    "-c", "protocol.ssh.allow=never",
    "-c", "protocol.http.allow=never",
    "-c", "protocol.https.allow=never",
    "-C", repository,
    ...args
  ];
}

function sha256Digest(value: string | Buffer): string {
  return `sha256:${sha256(value)}`;
}

function sameCanonical(left: unknown, right: unknown): boolean {
  return canonicalJson(left) === canonicalJson(right);
}

function expectedMinimizationExecutionId(run: GitMinimizationRunFact): string {
  return digestJson({
    schemaVersion: GIT_MINIMIZATION_SCHEMA_VERSION,
    nonce: run.executionNonce,
    role: run.role,
    roleAttempt: run.roleAttempt,
    direction: run.application.direction,
    base: run.application.base,
    candidateUnitIds: run.candidateUnitIds,
    frozenDigest: run.frozenDigest
  });
}

function expectedMinimizationRunId(run: GitMinimizationRunFact): string {
  const { runId: _runId, ...unsigned } = run;
  return digestJson(unsigned);
}

function redactedEvidencePreview(value: string, stream: "stdout" | "stderr"): {
  value: string;
  truncated: boolean;
  redacted: boolean;
} {
  const bytes = Buffer.from(value, "utf8");
  const preview = bytes.subarray(0, EVIDENCE_LOG_PREVIEW_BYTES).toString("utf8");
  const safe = redactText(preview, `$.minimization.${stream}`);
  return { value: safe.value, truncated: bytes.length > EVIDENCE_LOG_PREVIEW_BYTES, redacted: safe.report.redacted };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function boundedAppend(current: Buffer, next: Buffer): Buffer {
  if (current.length >= MAX_GIT_OUTPUT_BYTES) return current;
  const room = MAX_GIT_OUTPUT_BYTES - current.length;
  return Buffer.concat([current, next.subarray(0, room)]);
}

/** Run a command as argv, optionally writing exact bytes to stdin. No shell is involved. */
async function runProcess(executable: string, argumentsList: readonly string[], stdin?: Buffer): Promise<ProcessResult> {
  return new Promise((resolveResult) => {
    let stdout: Buffer = Buffer.alloc(0);
    let stderr: Buffer = Buffer.alloc(0);
    let outputExceeded = false;
    let settled = false;

    const settle = (result: ProcessResult): void => {
      if (settled) return;
      settled = true;
      resolveResult(result);
    };

    let child;
    try {
      child = spawn(executable, [...argumentsList], {
        shell: false,
        windowsHide: true,
        stdio: [stdin === undefined ? "ignore" : "pipe", "pipe", "pipe"],
        env: hardenedGitEnvironment()
      });
    } catch (error) {
      settle({ exitCode: null, stdout, stderr, error: errorMessage(error) });
      return;
    }

    const capture = (chunk: Buffer, stream: "stdout" | "stderr"): void => {
      const bytes = Buffer.from(chunk);
      if (stream === "stdout") stdout = boundedAppend(stdout, bytes);
      else stderr = boundedAppend(stderr, bytes);
      if (stdout.length + stderr.length >= MAX_GIT_OUTPUT_BYTES && !outputExceeded) {
        outputExceeded = true;
        child.kill();
      }
    };

    child.stdout?.on("data", (chunk: Buffer) => capture(chunk, "stdout"));
    child.stderr?.on("data", (chunk: Buffer) => capture(chunk, "stderr"));
    child.once("error", (error) => settle({ exitCode: null, stdout, stderr, error: errorMessage(error) }));
    child.once("close", (exitCode) => settle({
      exitCode,
      stdout,
      stderr: outputExceeded
        ? Buffer.concat([stderr, Buffer.from("\nFaultLine stopped Git after its bounded output limit.", "utf8")])
        : stderr,
      ...(outputExceeded ? { error: "Git command exceeded bounded output limit" } : {})
    }));
    if (stdin !== undefined) {
      child.stdin?.once("error", () => {
        // `git apply` can close stdin after identifying a malformed patch.
        // The exit status/stderr remains the authoritative failure fact.
      });
      child.stdin?.end(stdin);
    }
  });
}

async function runGit(repository: string, args: readonly string[], stdin?: Buffer): Promise<ProcessResult> {
  return runProcess("git", hardenedGitArguments(repository, args), stdin);
}

async function gitText(repository: string, args: readonly string[]): Promise<string> {
  const result = await runGit(repository, args);
  if (result.exitCode !== 0 || result.error !== undefined) {
    const detail = [result.stderr.toString("utf8").trim(), result.error].filter((value): value is string => Boolean(value)).join("; ");
    throw new Error(`Git ${args.join(" ")} failed: ${detail || `exit ${result.exitCode ?? "unknown"}`}`);
  }
  return result.stdout.toString("utf8").trim();
}

async function resolveRepositoryRoot(repository: string): Promise<string> {
  const requested = resolve(repository);
  const root = await gitText(requested, ["rev-parse", "--show-toplevel"]);
  if (!isAbsolute(root)) throw new Error("Git did not return an absolute repository root.");
  return resolve(root);
}

async function resolveCommit(repository: string, revision: string): Promise<string> {
  const value = await gitText(repository, ["rev-parse", "--verify", "--end-of-options", `${revision}^{commit}`]);
  if (!GIT_OBJECT_ID.test(value)) throw new Error(`Git returned an invalid commit object id for revision ${revision}.`);
  return value;
}

async function resolveState(repository: string, revision: string): Promise<GitCounterfactualState> {
  const commit = await resolveCommit(repository, revision);
  const tree = await gitText(repository, ["rev-parse", "--verify", "--end-of-options", `${commit}^{tree}`]);
  if (!GIT_OBJECT_ID.test(tree)) throw new Error(`Git returned an invalid tree object id for revision ${revision}.`);
  return { commit, tree };
}

/** Reject checkout filters before a revision is materialized on the host. */
async function assertSafeGitMaterialization(repository: string, states: readonly GitCounterfactualState[]): Promise<void> {
  const localFilters = await runGit(repository, ["config", "--local", "--get-regexp", "^filter\\."]);
  if (localFilters.exitCode === 0 && localFilters.stdout.toString("utf8").trim()) {
    throw new Error("Refusing host worktree materialization: repository local Git filter configuration is present.");
  }
  for (const state of states) {
    const attributes = await runGit(repository, ["show", "--no-textconv", "--end-of-options", `${state.commit}:.gitattributes`]);
    if (attributes.exitCode === 0 && DANGEROUS_GIT_ATTRIBUTE.test(attributes.stdout.toString("utf8"))) {
      throw new Error(`Refusing host worktree materialization: ${state.commit} declares a Git filter attribute.`);
    }
  }
}

function nulParts(value: Buffer): Buffer[] {
  const parts: Buffer[] = [];
  let offset = 0;
  while (offset < value.length) {
    const ending = value.indexOf(0, offset);
    if (ending < 0) throw new Error("Git -z output ended without a NUL terminator.");
    parts.push(value.subarray(offset, ending));
    offset = ending + 1;
  }
  return parts;
}

function changeKind(status: string): GitPatchUnit["changeKind"] {
  switch (status[0]) {
    case "A": return "ADD";
    case "D": return "DELETE";
    case "M": return "MODIFY";
    case "T": return "TYPECHANGE";
    default: return "UNKNOWN";
  }
}

function assertUtf8Path(pathBytes: Buffer): string {
  const path = pathBytes.toString("utf8");
  if (!path || !Buffer.from(path, "utf8").equals(pathBytes)) {
    throw new Error("Git contains a non-UTF-8 or empty path; FaultLine refuses to pass it through a lossy argv conversion.");
  }
  if (path.includes("\0")) throw new Error("Git path unexpectedly contains a NUL byte.");
  return path;
}

/**
 * Build each unit independently with `git diff --binary`, retaining raw patch
 * bytes. That avoids line-oriented parsing of binary-patch payloads entirely.
 */
async function derivePatchUnits(repository: string, before: GitCounterfactualState, after: GitCounterfactualState): Promise<PatchUnitInternal[]> {
  const status = await runGit(repository, [
    "diff",
    "--name-status",
    "-z",
    "--no-renames",
    "--no-ext-diff",
    before.commit,
    after.commit
  ]);
  if (status.exitCode !== 0 || status.error !== undefined) {
    throw new Error(`Could not enumerate changed Git paths: ${status.stderr.toString("utf8").trim() || status.error || "Git failed."}`);
  }
  const parts = nulParts(status.stdout);
  if (parts.length % 2 !== 0) throw new Error("Git --name-status -z output was malformed.");

  const output: PatchUnitInternal[] = [];
  for (let index = 0; index < parts.length; index += 2) {
    const statusBytes = parts[index];
    const pathBytes = parts[index + 1];
    if (!statusBytes || !pathBytes) throw new Error("Git --name-status -z omitted a changed path.");
    const path = assertUtf8Path(pathBytes);
    const pathspec = `:(literal)${path}`;
    const patch = await runGit(repository, [
      "diff",
      "--binary",
      "--full-index",
      "--no-color",
      "--no-renames",
      "--no-ext-diff",
      before.commit,
      after.commit,
      "--",
      pathspec
    ]);
    if (patch.exitCode !== 0 || patch.error !== undefined || patch.stdout.length === 0) {
      throw new Error(`Could not derive an exact binary patch for ${path}: ${patch.stderr.toString("utf8").trim() || patch.error || "Git returned no patch."}`);
    }
    const patchDigest = sha256Digest(patch.stdout);
    const pathDigest = sha256Digest(pathBytes);
    const ordinal = output.length;
    const id = digestJson({
      schemaVersion: GIT_MINIMIZATION_SCHEMA_VERSION,
      ordinal,
      pathDigest,
      patchDigest
    });
    output.push({
      id,
      ordinal,
      changeKind: changeKind(statusBytes.toString("ascii")),
      path,
      pathBytesBase64: pathBytes.toString("base64"),
      pathDigest,
      patchDigest,
      patchBytes: patch.stdout.length,
      binarySafe: true,
      bytes: patch.stdout
    });
  }
  return output;
}

function safeOverlayParts(value: string): string[] {
  if (!SAFE_OVERLAY_PATH.test(value) || value.startsWith("/") || value.includes("\\")) {
    throw new Error(`Frozen overlay path is unsafe: ${value}`);
  }
  const parts = value.split("/");
  if (parts.some((part) => !part || part === "." || part === "..")) throw new Error(`Frozen overlay path is unsafe: ${value}`);
  return parts;
}

async function lstatIfPresent(path: string): Promise<Awaited<ReturnType<typeof lstat>> | null> {
  try {
    return await lstat(path);
  } catch (error) {
    const code = error instanceof Error && "code" in error ? (error as NodeJS.ErrnoException).code : undefined;
    if (code === "ENOENT") return null;
    throw error;
  }
}

/** Construct only non-symlink ancestors below this exact temporary worktree. */
async function safeOverlayTarget(worktree: string, overlayPath: string): Promise<string> {
  const parts = safeOverlayParts(overlayPath);
  const target = resolve(worktree, ...parts);
  const containment = relative(worktree, target);
  if (!containment || containment === ".." || containment.startsWith("..\\") || containment.startsWith("../") || isAbsolute(containment)) {
    throw new Error(`Frozen overlay path escaped its worktree: ${overlayPath}`);
  }
  let current = worktree;
  for (const part of parts.slice(0, -1)) {
    current = join(current, part);
    const entry = await lstatIfPresent(current);
    if (entry === null) {
      await mkdir(current, { mode: 0o755 });
      const created = await lstat(current);
      if (!created.isDirectory() || created.isSymbolicLink()) throw new Error(`Could not safely create overlay directory: ${overlayPath}`);
    } else if (!entry.isDirectory() || entry.isSymbolicLink()) {
      throw new Error(`Frozen overlay parent is not a safe directory: ${overlayPath}`);
    }
  }
  const existing = await lstatIfPresent(target);
  if (existing?.isSymbolicLink()) throw new Error(`Frozen overlay target is a symbolic link: ${overlayPath}`);
  return target;
}

/** Materialize and reread the exact approved bytes; no overlay may traverse a link. */
async function materializeFrozenOverlays(worktree: string, frozenWitness: FrozenWitness): Promise<MaterializedFrozenOverlay[]> {
  const facts: MaterializedFrozenOverlay[] = [];
  for (const overlay of frozenWitness.proposal.witness.overlays) {
    const bytes = Buffer.from(overlay.bytesBase64, "base64");
    if (sha256Digest(bytes) !== overlay.bytesDigest) {
      throw new Error(`Frozen overlay digest does not match its bytes: ${overlay.path}`);
    }
    const target = await safeOverlayTarget(worktree, overlay.path);
    const staged = join(dirname(target), `.faultline-overlay-${randomUUID()}`);
    try {
      await writeFile(staged, bytes, { encoding: undefined, flag: "wx", mode: 0o644 });
      await rename(staged, target);
    } finally {
      await rm(staged, { force: true });
    }
    const reread = await readFile(target);
    if (!reread.equals(bytes) || sha256Digest(reread) !== overlay.bytesDigest) {
      throw new Error(`FaultLine could not verify exact materialized overlay bytes: ${overlay.path}`);
    }
    facts.push({ path: overlay.path, bytesDigest: overlay.bytesDigest, bytesLength: bytes.length });
  }
  return facts;
}

function planRequestForWorktree(sandbox: GitMinimizationSandbox, worktree: string, witness: FrozenWitness): SandboxPlanRequest {
  return {
    witness: { digest: witness.frozenDigest, command: witness.proposal.witness.command },
    sourceDirectory: worktree,
    mode: sandbox.mode,
    ...(sandbox.image === undefined ? {} : { image: sandbox.image }),
    ...(sandbox.environment === undefined ? {} : { environment: sandbox.environment }),
    ...(sandbox.allowedEnvironment === undefined ? {} : { allowedEnvironment: sandbox.allowedEnvironment }),
    ...(sandbox.limits === undefined ? {} : { limits: sandbox.limits as Partial<SandboxLimits> }),
    ...(sandbox.allowUnsafeLocal === undefined ? {} : { allowUnsafeLocal: sandbox.allowUnsafeLocal })
  };
}

function applicationFact(
  direction: Direction,
  base: GitCounterfactualState,
  selectedUnitIds: readonly string[],
  patchBytes: Buffer,
  status: GitPatchApplication["status"],
  result?: ProcessResult,
  error?: string
): GitPatchApplication {
  const stdout = result?.stdout ?? Buffer.alloc(0);
  const stderr = result?.stderr ?? Buffer.alloc(0);
  return GitPatchApplicationSchema.parse({
    direction,
    base,
    selectedUnitIds: [...selectedUnitIds],
    candidatePatchDigest: sha256Digest(patchBytes),
    candidatePatchBytes: patchBytes.length,
    status,
    stdoutDigest: sha256Digest(stdout),
    stdoutBytes: stdout.length,
    stderrDigest: sha256Digest(stderr),
    stderrBytes: stderr.length,
    error: error ?? null
  });
}

function persistedResult(result: SandboxExecutionResult) {
  const stdoutPreview = redactedEvidencePreview(result.stdout, "stdout");
  const stderrPreview = redactedEvidencePreview(result.stderr, "stderr");
  return {
    kind: result.kind,
    executor: result.executor,
    verdict: result.verdict,
    reason: result.reason,
    exitCode: result.exitCode,
    signal: result.signal,
    outputTruncated: result.outputTruncated,
    stdoutDigest: sha256Digest(result.stdout),
    stdoutBytes: Buffer.byteLength(result.stdout, "utf8"),
    stdoutPreview: stdoutPreview.value,
    stdoutPreviewTruncated: stdoutPreview.truncated,
    stdoutRedacted: stdoutPreview.redacted,
    stderrDigest: sha256Digest(result.stderr),
    stderrBytes: Buffer.byteLength(result.stderr, "utf8"),
    stderrPreview: stderrPreview.value,
    stderrPreviewTruncated: stderrPreview.truncated,
    stderrRedacted: stderrPreview.redacted
  };
}

function outcomeForSandbox(result: SandboxExecutionResult): Extract<ProbeOutcome, "PASS" | "FAIL" | "UNRESOLVED"> {
  // Injected runners can support a developer's observed counterfactual search,
  // but `finalize` and certification still refuse to label them proof.
  if (result.kind !== "DOCKER_ISOLATED") return "UNRESOLVED";
  if (result.verdict === "PASS") return "PASS";
  if (result.verdict === "FAIL") return "FAIL";
  return "UNRESOLVED";
}

function outcomeNote(result: SandboxExecutionResult): string {
  if (result.kind === "UNSAFE_LOCAL") return "Unsafe local execution is explicitly inapplicable and cannot be proof.";
  if (result.reason === "SANDBOX_UNAVAILABLE") return "Docker was unavailable; this run is unresolved.";
  if (result.verdict === "ERROR") return `Sandbox execution is unresolved: ${result.reason}.`;
  return `Docker witness returned ${result.verdict}.`;
}

async function removeWorktree(repository: string, worktree: string): Promise<string | null> {
  const removal = await runGit(repository, ["worktree", "remove", "--force", worktree]);
  if (removal.exitCode === 0) return null;
  try {
    await rm(worktree, { recursive: true, force: true });
    await runGit(repository, ["worktree", "prune"]);
  } catch (error) {
    return `Could not remove temporary worktree: ${errorMessage(error)}`;
  }
  return `Git could not deregister a temporary worktree: ${removal.stderr.toString("utf8").trim() || removal.error || "unknown error"}`;
}

function chunk<T>(values: readonly T[], count: number): T[][] {
  const size = Math.ceil(values.length / count);
  const output: T[][] = [];
  for (let index = 0; index < values.length; index += size) output.push([...values.slice(index, index + size)]);
  return output;
}

function selectedPatch(units: readonly PatchUnitInternal[], candidateIds: readonly string[]): { units: PatchUnitInternal[]; bytes: Buffer } {
  const selected = new Set(candidateIds);
  const unitsSelected = units.filter((unit) => selected.has(unit.id));
  if (unitsSelected.length !== selected.size) throw new Error("Candidate references a patch unit that does not belong to this Git diff.");
  return { units: unitsSelected, bytes: Buffer.concat(unitsSelected.map((unit) => unit.bytes)) };
}

/**
 * `git apply` can resolve a file/directory obstruction by changing a parent
 * path that the selected patch did not declare. That is not a valid subset
 * counterfactual, so inspect the detached index before the witness runs.
 */
async function verifyAppliedPatchScope(
  worktree: string,
  base: GitCounterfactualState,
  selectedPaths: readonly string[]
): Promise<string | null> {
  const changed = await runGit(worktree, ["diff", "--cached", "--name-only", "-z", "--no-renames", base.commit]);
  if (changed.exitCode !== 0 || changed.error !== undefined) {
    return `Could not verify applied patch scope: ${changed.stderr.toString("utf8").trim() || changed.error || "Git failed."}`;
  }
  let paths: string[];
  try {
    paths = nulParts(changed.stdout).map(assertUtf8Path);
  } catch (error) {
    return `Could not verify applied patch scope: ${errorMessage(error)}`;
  }
  const allowed = new Set(selectedPaths);
  const unexpected = paths.filter((path) => !allowed.has(path));
  return unexpected.length > 0
    ? `Git apply changed paths outside the selected patch units: ${unexpected.join(", ")}`
    : null;
}

function witnessSummary(frozenWitness: FrozenWitness, verification: WitnessLockVerification) {
  return {
    valid: verification.valid,
    errors: verification.errors,
    frozenDigest: verification.frozenDigest,
    witnessDigest: verification.valid ? frozenWitness.witnessDigest : null,
    externalDigestStatus: verification.externalDigestStatus,
    approval: verification.approval
  };
}

function emptyCertificate(direction: Direction, expectedOutcome: "PASS" | "FAIL", note: string): GitMinimizationCertificate {
  return {
    direction,
    expectedOutcome,
    requiredExecutions: MINIMIZATION_CERTIFICATION_EXECUTIONS,
    runIds: [],
    executionIds: [],
    status: "NOT_RUN",
    note
  };
}

function executionTrustFor(sandboxMode: "DOCKER_ISOLATED" | "UNSAFE_LOCAL", hasInjectedRunner: boolean): "NATIVE_DOCKER" | "INJECTED_RUNNER" | "UNSAFE_LOCAL" {
  if (sandboxMode === "UNSAFE_LOCAL") return "UNSAFE_LOCAL";
  return hasInjectedRunner ? "INJECTED_RUNNER" : "NATIVE_DOCKER";
}

function emptyProof(
  sandboxMode: "DOCKER_ISOLATED" | "UNSAFE_LOCAL",
  reason: string,
  executionTrust = executionTrustFor(sandboxMode, false)
) {
  return {
    requiresDockerIsolation: true as const,
    dockerIsolated: executionTrust === "NATIVE_DOCKER",
    executionTrust,
    sufficiencyCertified: false,
    necessityCertified: false,
    isProof: false,
    reason
  };
}

function baseResult(
  status: GitMinimizationResult["status"],
  sandboxMode: "DOCKER_ISOLATED" | "UNSAFE_LOCAL",
  maxExecutions: number,
  errors: readonly string[],
  fields: Pick<GitMinimizationResult, "repository" | "before" | "after" | "witness">,
  executionTrust = executionTrustFor(sandboxMode, false)
): GitMinimizationResult {
  return GitMinimizationResultSchema.parse({
    schemaVersion: GIT_MINIMIZATION_SCHEMA_VERSION,
    recorder: "git-diff-counterfactual-minimization",
    nativeCodexInterception: false,
    status,
    ...fields,
    budget: { usedExecutions: 0, maxExecutions },
    patchUnits: [],
    candidateUnitIds: [],
    attempts: [],
    runs: [],
    minimality: { oneMinimal: false, reason: "No candidate was established." },
    certification: {
      sufficiency: emptyCertificate("FORWARD_FROM_BEFORE", "FAIL", "No sufficiency certification was run."),
      necessity: emptyCertificate("REVERSE_FROM_AFTER", "PASS", "No necessity certification was run.")
    },
    proof: emptyProof(sandboxMode, errors[0] ?? "Minimization did not produce proof.", executionTrust),
    errors: [...errors]
  });
}

/**
 * Derive and minimize a real binary-safe Git diff. Candidate experiments are
 * always reconstructed in a fresh detached worktree. A success claim requires
 * three distinct Docker executions forward from `before` and three distinct
 * Docker executions reverse from `after`; unsafe-local runs are never proof.
 */
export async function minimizeGitDiff(request: GitMinimizationRequest): Promise<GitMinimizationResult> {
  const rawMode = request.sandbox && typeof request.sandbox === "object" && "mode" in request.sandbox && request.sandbox.mode === "UNSAFE_LOCAL"
    ? "UNSAFE_LOCAL" as const
    : "DOCKER_ISOLATED" as const;
  const requestedExecutionTrust = executionTrustFor(rawMode, request.runner !== undefined);
  const parsedRequest = GitMinimizationRequestSchema.safeParse({
    repository: request.repository,
    before: request.before,
    after: request.after,
    frozenWitness: request.frozenWitness,
    expectedFrozenDigest: request.expectedFrozenDigest,
    sandbox: request.sandbox,
    budget: request.budget
  });
  if (!parsedRequest.success) {
    return baseResult("INVALID_REQUEST", rawMode, 128, [parsedRequest.error.message], {
      repository: null,
      before: null,
      after: null,
      witness: null
    }, requestedExecutionTrust);
  }
  const input = parsedRequest.data;
  const verification = verifyFrozenWitnessRecord(input.frozenWitness, input.expectedFrozenDigest);
  const summary = witnessSummary(input.frozenWitness, verification);
  if (!verification.valid || verification.externalDigestStatus !== "MATCH") {
    return baseResult("INVALID_WITNESS", input.sandbox.mode, input.budget.maxExecutions, verification.errors.length > 0
      ? verification.errors
      : ["A separately supplied frozen witness digest did not match."], {
      repository: null,
      before: null,
      after: null,
      witness: summary
    }, executionTrustFor(input.sandbox.mode, request.runner !== undefined));
  }

  let repository: string;
  let before: GitCounterfactualState;
  let after: GitCounterfactualState;
  try {
    repository = await resolveRepositoryRoot(input.repository);
    before = await resolveState(repository, input.before);
    after = await resolveState(repository, input.after);
    await assertSafeGitMaterialization(repository, [before, after]);
  } catch (error) {
    return baseResult("RANGE_ERROR", input.sandbox.mode, input.budget.maxExecutions, [errorMessage(error)], {
      repository: null,
      before: null,
      after: null,
      witness: summary
    }, executionTrustFor(input.sandbox.mode, request.runner !== undefined));
  }

  let units: PatchUnitInternal[];
  try {
    units = await derivePatchUnits(repository, before, after);
  } catch (error) {
    return baseResult("PATCH_ERROR", input.sandbox.mode, input.budget.maxExecutions, [errorMessage(error)], {
      repository,
      before,
      after,
      witness: summary
    }, executionTrustFor(input.sandbox.mode, request.runner !== undefined));
  }
  if (units.length === 0) {
    return baseResult("NO_PATCHES", input.sandbox.mode, input.budget.maxExecutions, ["The supplied Git states have no file-level diff units."], {
      repository,
      before,
      after,
      witness: summary
    });
  }

  const runs: GitMinimizationRunFact[] = [];
  const attempts: GitMinimizationAttempt[] = [];
  const errors: string[] = [];
  let usedExecutions = 0;
  let roleAttempts = new Map<RunRole, number>();
  const nextRoleAttempt = (role: RunRole): number => {
    const next = (roleAttempts.get(role) ?? 0) + 1;
    roleAttempts.set(role, next);
    return next;
  };

  const executeCandidate = async (
    direction: Direction,
    candidateIds: readonly string[],
    role: RunRole
  ): Promise<Probe> => {
    if (usedExecutions >= input.budget.maxExecutions) {
      return { outcome: "NOT_RUN", run: null, note: "Execution budget is exhausted." };
    }
    const roleAttempt = nextRoleAttempt(role);
    const base = direction === "FORWARD_FROM_BEFORE" ? before : after;
    const patch = selectedPatch(units, candidateIds);
    const startedEpoch = Date.now();
    const startedAt = new Date(startedEpoch).toISOString();
    const tempRoot = await mkdtemp(join(tmpdir(), "faultline-git-minimization-"));
    const worktree = join(tempRoot, `worktree-${randomUUID()}`);
    const worktreeDigest = sha256Digest(worktree);
    let created = false;
    let overlays: MaterializedFrozenOverlay[] = [];
    let application: GitPatchApplication | undefined;
    let sandbox: SandboxPlanAudit | null = null;
    let result: ReturnType<typeof persistedResult> | null = null;
    let outcome: Extract<ProbeOutcome, "PASS" | "FAIL" | "UNRESOLVED"> = "UNRESOLVED";
    let note = "";
    try {
      const added = await runGit(repository, ["worktree", "add", "--detach", worktree, base.commit]);
      if (added.exitCode !== 0 || added.error !== undefined) {
        const detail = added.stderr.toString("utf8").trim() || added.error || "Git failed.";
        application = applicationFact(direction, base, patch.units.map((unit) => unit.id), patch.bytes, "ERROR", added, `Could not create detached worktree: ${detail}`);
        note = application.error ?? "Could not create detached worktree.";
      } else {
        created = true;
        if (patch.bytes.length === 0) {
          application = applicationFact(direction, base, [], patch.bytes, "NO_PATCHES");
        } else {
          const applied = await runGit(worktree, [
            "apply",
            ...(direction === "REVERSE_FROM_AFTER" ? ["--reverse"] : []),
            "--binary",
            // The detached worktree index starts at the exact base commit.
            // Requiring index application prevents Git from silently resolving
            // a selected path's file/directory obstruction outside the subset.
            "--index",
            "--whitespace=nowarn",
            "--"
          ], patch.bytes);
          if (applied.exitCode !== 0 || applied.error !== undefined) {
            const detail = applied.stderr.toString("utf8").trim() || applied.error || "Git apply failed.";
            application = applicationFact(direction, base, patch.units.map((unit) => unit.id), patch.bytes, "CONFLICT", applied, detail);
            note = `Patch application is unresolved: ${detail}`;
          } else {
            const scopeError = await verifyAppliedPatchScope(worktree, base, patch.units.map((unit) => unit.path));
            if (scopeError) {
              application = applicationFact(direction, base, patch.units.map((unit) => unit.id), patch.bytes, "CONFLICT", applied, scopeError);
              note = `Patch application is unresolved: ${scopeError}`;
            } else {
              application = applicationFact(direction, base, patch.units.map((unit) => unit.id), patch.bytes, "APPLIED", applied);
            }
          }
        }

        if (application.status === "APPLIED" || application.status === "NO_PATCHES") {
          overlays = await materializeFrozenOverlays(worktree, input.frozenWitness);
          let plan;
          try {
            plan = createSandboxPlan(planRequestForWorktree(input.sandbox, worktree, input.frozenWitness));
          } catch (error) {
            note = `Sandbox plan could not be created: ${errorMessage(error)}`;
          }
          if (plan !== undefined) {
            sandbox = auditSandboxPlan(plan);
            usedExecutions += 1;
            const execution = await executeSandboxPlan(plan, request.runner);
            result = persistedResult(execution);
            outcome = outcomeForSandbox(execution);
            note = outcomeNote(execution);
          }
        }
      }
    } catch (error) {
      const message = errorMessage(error);
      application = application ?? applicationFact(direction, base, patch.units.map((unit) => unit.id), patch.bytes, "ERROR", undefined, message);
      note = message;
    } finally {
      if (created) {
        const cleanupError = await removeWorktree(repository, worktree);
        if (cleanupError) errors.push(cleanupError);
      }
      await rm(tempRoot, { recursive: true, force: true });
    }
    const finishedEpoch = Date.now();
    const finishedAt = new Date(finishedEpoch).toISOString();
    const executionNonce = randomUUID();
    const executionId = digestJson({
      schemaVersion: GIT_MINIMIZATION_SCHEMA_VERSION,
      nonce: executionNonce,
      role,
      roleAttempt,
      direction,
      base,
      candidateUnitIds: patch.units.map((unit) => unit.id),
      frozenDigest: input.frozenWitness.frozenDigest
    });
    const unsigned = {
      schemaVersion: GIT_MINIMIZATION_SCHEMA_VERSION,
      executionId,
      executionNonce,
      role,
      roleAttempt,
      candidateUnitIds: patch.units.map((unit) => unit.id),
      worktreeDigest,
      frozenDigest: input.frozenWitness.frozenDigest,
      witnessDigest: input.frozenWitness.witnessDigest,
      startedAt,
      finishedAt,
      durationMs: Math.max(0, finishedEpoch - startedEpoch),
      overlays,
      application: application ?? applicationFact(
        direction,
        base,
        patch.units.map((unit) => unit.id),
        patch.bytes,
        "ERROR",
        undefined,
        "FaultLine did not produce a patch application result."
      ),
      sandbox,
      result,
      outcome,
      note: note || "No sandbox execution was produced."
    };
    const run = GitMinimizationRunFactSchema.parse({ ...unsigned, runId: digestJson(unsigned) });
    runs.push(run);
    return { outcome, run, note: run.note };
  };

  const recordProbe = async (
    phase: AttemptPhase,
    direction: Direction,
    candidateIds: readonly string[],
    role: RunRole
  ): Promise<Probe> => {
    const probe = await executeCandidate(direction, candidateIds, role);
    attempts.push(GitMinimizationAttemptSchema.parse({
      ordinal: attempts.length + 1,
      phase,
      candidateUnitIds: [...candidateIds],
      outcome: probe.outcome,
      runId: probe.run?.runId ?? null,
      note: probe.note
    }));
    return probe;
  };

  const statusFromProbe = (probe: Probe, expected: "PASS" | "FAIL"): GitMinimizationResult["status"] | null => {
    const result = probe.run?.result;
    if (result?.kind === "UNSAFE_LOCAL") return "UNSAFE_LOCAL_INAPPLICABLE";
    if (result?.reason === "SANDBOX_UNAVAILABLE") return "SANDBOX_UNAVAILABLE";
    if (result?.verdict === "ERROR" || probe.outcome === "UNRESOLVED") return "EXECUTION_ERROR";
    if (probe.outcome === "NOT_RUN") return "BUDGET_EXHAUSTED";
    return expected === "PASS" ? "BASELINE_NOT_PASS" : "FULL_PATCH_NOT_FAIL";
  };

  const finalize = (
    status: GitMinimizationResult["status"],
    candidateIds: readonly string[],
    minimality: GitMinimizationResult["minimality"],
    sufficiency: GitMinimizationCertificate,
    necessity: GitMinimizationCertificate,
    reason: string
  ): GitMinimizationResult => {
    const sufficiencyCertified = sufficiency.status === "CERTIFIED";
    const necessityCertified = necessity.status === "CERTIFIED";
    const executionTrust = executionTrustFor(input.sandbox.mode, request.runner !== undefined);
    const proof = {
      requiresDockerIsolation: true as const,
      dockerIsolated: executionTrust === "NATIVE_DOCKER",
      executionTrust,
      sufficiencyCertified,
      necessityCertified,
      isProof: executionTrust === "NATIVE_DOCKER" && minimality.oneMinimal && sufficiencyCertified && necessityCertified && errors.length === 0,
      reason
    };
    return GitMinimizationResultSchema.parse({
      schemaVersion: GIT_MINIMIZATION_SCHEMA_VERSION,
      recorder: "git-diff-counterfactual-minimization",
      nativeCodexInterception: false,
      status,
      repository,
      before,
      after,
      witness: summary,
      budget: { usedExecutions, maxExecutions: input.budget.maxExecutions },
      patchUnits: units.map(({ bytes: _bytes, ...unit }) => unit),
      candidateUnitIds: [...candidateIds],
      attempts,
      runs,
      minimality,
      certification: { sufficiency, necessity },
      proof,
      errors
    });
  };

  const defaultSufficiency = () => emptyCertificate("FORWARD_FROM_BEFORE", "FAIL", "No sufficiency certification was run.");
  const defaultNecessity = () => emptyCertificate("REVERSE_FROM_AFTER", "PASS", "No necessity certification was run.");
  const allIds = units.map((unit) => unit.id);

  const baseline = await recordProbe("BASELINE", "FORWARD_FROM_BEFORE", [], "BASELINE_BEFORE");
  if (baseline.outcome !== "PASS") {
    return finalize(
      statusFromProbe(baseline, "PASS") ?? "EXECUTION_ERROR",
      [],
      { oneMinimal: false, reason: "The before state did not produce a Docker PASS." },
      defaultSufficiency(),
      defaultNecessity(),
      baseline.note
    );
  }

  const full = await recordProbe("FULL_RANGE", "FORWARD_FROM_BEFORE", allIds, "FULL_PATCH");
  if (full.outcome !== "FAIL") {
    return finalize(
      statusFromProbe(full, "FAIL") ?? "EXECUTION_ERROR",
      allIds,
      { oneMinimal: false, reason: "The full Git diff did not produce a Docker FAIL from the before state." },
      defaultSufficiency(),
      defaultNecessity(),
      full.note
    );
  }

  let candidate = [...allIds];
  let granularity = 2;
  let budgetStopped = false;
  while (candidate.length >= 2 && !budgetStopped) {
    const partitions = chunk(candidate, Math.min(granularity, candidate.length));
    let reduced = false;
    for (const partition of partitions) {
      const probe = await recordProbe("DELTA_SUBSET", "FORWARD_FROM_BEFORE", partition, "DELTA_SUBSET");
      if (probe.outcome === "NOT_RUN") {
        budgetStopped = true;
        break;
      }
      if (probe.outcome === "FAIL") {
        candidate = partition;
        granularity = Math.max(2, granularity - 1);
        reduced = true;
        break;
      }
    }
    if (budgetStopped || reduced) continue;

    for (const partition of partitions) {
      const partitionSet = new Set(partition);
      const complement = candidate.filter((id) => !partitionSet.has(id));
      const probe = await recordProbe("DELTA_COMPLEMENT", "FORWARD_FROM_BEFORE", complement, "DELTA_COMPLEMENT");
      if (probe.outcome === "NOT_RUN") {
        budgetStopped = true;
        break;
      }
      if (probe.outcome === "FAIL") {
        candidate = complement;
        granularity = Math.max(2, granularity - 1);
        reduced = true;
        break;
      }
    }
    if (budgetStopped || reduced) continue;
    if (granularity >= candidate.length) break;
    granularity = Math.min(candidate.length, granularity * 2);
  }

  let oneMinimal = !budgetStopped;
  let minimalityReason = budgetStopped ? "The execution budget was exhausted before one-minimality could be established." : "Every single-unit removal was tested.";
  let index = 0;
  while (index < candidate.length && !budgetStopped) {
    const removed = candidate[index];
    if (!removed) break;
    const without = candidate.filter((id) => id !== removed);
    const probe = await recordProbe("ONE_MINIMAL", "FORWARD_FROM_BEFORE", without, "ONE_MINIMAL");
    if (probe.outcome === "NOT_RUN") {
      budgetStopped = true;
      oneMinimal = false;
      minimalityReason = "The execution budget was exhausted before one-minimality could be established.";
      break;
    }
    if (probe.outcome === "FAIL") {
      candidate = without;
      index = 0;
      // The smaller candidate needs a fresh complete removal pass. Earlier
      // removal outcomes belonged to a different candidate and must not keep
      // a genuinely one-minimal reduced candidate from being certified.
      oneMinimal = true;
      minimalityReason = "A smaller failing candidate was found; retesting its removals.";
      continue;
    }
    if (probe.outcome === "UNRESOLVED") {
      oneMinimal = false;
      minimalityReason = "At least one single-unit removal could not be applied or executed, so one-minimality is unproven.";
    }
    index += 1;
  }
  if (!budgetStopped && oneMinimal) minimalityReason = "Every single-unit removal produced a Docker PASS.";

  const certify = async (
    phase: Extract<AttemptPhase, "SUFFICIENCY_CERTIFICATION" | "NECESSITY_CERTIFICATION">,
    direction: Direction,
    expectedOutcome: "PASS" | "FAIL",
    role: Extract<RunRole, "SUFFICIENCY_CERTIFICATION" | "NECESSITY_CERTIFICATION">
  ): Promise<GitMinimizationCertificate> => {
    const runIds: string[] = [];
    const executionIds: string[] = [];
    for (let attempt = 1; attempt <= MINIMIZATION_CERTIFICATION_EXECUTIONS; attempt += 1) {
      const probe = await recordProbe(phase, direction, candidate, role);
      if (probe.run) {
        runIds.push(probe.run.runId);
        executionIds.push(probe.run.executionId);
      }
      if (probe.outcome !== expectedOutcome || probe.run?.result?.kind !== "DOCKER_ISOLATED"
        || probe.run.result.executor !== "NATIVE_DOCKER") {
        return {
          direction,
          expectedOutcome,
          requiredExecutions: MINIMIZATION_CERTIFICATION_EXECUTIONS,
          runIds,
          executionIds,
          status: "NOT_CERTIFIED",
          note: `Attempt ${attempt} was ${probe.outcome}; required Docker ${expectedOutcome}. ${probe.note}`
        };
      }
    }
    const distinct = new Set(executionIds).size === MINIMIZATION_CERTIFICATION_EXECUTIONS;
    return {
      direction,
      expectedOutcome,
      requiredExecutions: MINIMIZATION_CERTIFICATION_EXECUTIONS,
      runIds,
      executionIds,
      status: distinct && runIds.length === MINIMIZATION_CERTIFICATION_EXECUTIONS ? "CERTIFIED" : "NOT_CERTIFIED",
      note: distinct
        ? "Three distinct Docker-isolated executions produced the required outcome."
        : "Certification executions were not distinct."
    };
  };

  if (budgetStopped || usedExecutions >= input.budget.maxExecutions) {
    return finalize(
      "BUDGET_EXHAUSTED",
      candidate,
      { oneMinimal, reason: minimalityReason },
      defaultSufficiency(),
      defaultNecessity(),
      "The minimization execution budget was exhausted before bidirectional certification."
    );
  }

  const sufficiency = await certify("SUFFICIENCY_CERTIFICATION", "FORWARD_FROM_BEFORE", "FAIL", "SUFFICIENCY_CERTIFICATION");
  const necessity = usedExecutions >= input.budget.maxExecutions
    ? emptyCertificate("REVERSE_FROM_AFTER", "PASS", "Execution budget was exhausted before necessity certification.")
    : await certify("NECESSITY_CERTIFICATION", "REVERSE_FROM_AFTER", "PASS", "NECESSITY_CERTIFICATION");
  const certificationsComplete = sufficiency.status === "CERTIFIED" && necessity.status === "CERTIFIED";
  const certificationRun = [...runs].reverse().find((run) => run.role === "SUFFICIENCY_CERTIFICATION" || run.role === "NECESSITY_CERTIFICATION");
  const terminal = certificationRun?.result;
  const completedProof = certificationsComplete && oneMinimal && errors.length === 0;
  const status = usedExecutions >= input.budget.maxExecutions && !certificationsComplete
    ? "BUDGET_EXHAUSTED"
    : terminal?.kind === "UNSAFE_LOCAL"
      ? "UNSAFE_LOCAL_INAPPLICABLE"
      : terminal?.reason === "SANDBOX_UNAVAILABLE"
        ? "SANDBOX_UNAVAILABLE"
        : completedProof
          ? "COMPLETED"
          : "CERTIFICATION_FAILED";
  const reason = status === "COMPLETED"
    ? "One-minimal candidate has independently certified Docker sufficiency and necessity."
    : status === "BUDGET_EXHAUSTED"
      ? "Execution budget was exhausted before all required certification executions completed."
      : status === "UNSAFE_LOCAL_INAPPLICABLE"
        ? "Unsafe local execution is explicitly inapplicable and cannot support proof."
        : status === "SANDBOX_UNAVAILABLE"
          ? "Docker was unavailable; no counterfactual result is proof."
          : oneMinimal
            ? "Bidirectional Docker certification did not complete with the required outcomes."
            : minimalityReason;
  return finalize(status, candidate, { oneMinimal, reason: minimalityReason }, sufficiency, necessity, reason);
}

const ROLE_SEMANTICS = {
  BASELINE_BEFORE: { phase: "BASELINE", direction: "FORWARD_FROM_BEFORE" },
  FULL_PATCH: { phase: "FULL_RANGE", direction: "FORWARD_FROM_BEFORE" },
  DELTA_SUBSET: { phase: "DELTA_SUBSET", direction: "FORWARD_FROM_BEFORE" },
  DELTA_COMPLEMENT: { phase: "DELTA_COMPLEMENT", direction: "FORWARD_FROM_BEFORE" },
  ONE_MINIMAL: { phase: "ONE_MINIMAL", direction: "FORWARD_FROM_BEFORE" },
  SUFFICIENCY_CERTIFICATION: { phase: "SUFFICIENCY_CERTIFICATION", direction: "FORWARD_FROM_BEFORE" },
  NECESSITY_CERTIFICATION: { phase: "NECESSITY_CERTIFICATION", direction: "REVERSE_FROM_AFTER" }
} as const satisfies Record<GitMinimizationRunFact["role"], {
  readonly phase: GitMinimizationAttempt["phase"];
  readonly direction: GitPatchApplication["direction"];
}>;

function externalDigestStatus(
  expectedDigest: string | undefined,
  resultDigest: string | null,
  errors: string[]
): GitMinimizationExternalDigestStatus {
  if (expectedDigest === undefined) return "NOT_PROVIDED";
  if (!DigestSchema.safeParse(expectedDigest).success) {
    errors.push("externally supplied minimization digest is not a sha256 digest");
    return "MISMATCH";
  }
  if (resultDigest === null || expectedDigest !== resultDigest) {
    errors.push("externally supplied minimization digest does not match");
    return "MISMATCH";
  }
  return "MATCH";
}

function validateCandidateIds(
  ids: readonly string[],
  units: ReadonlyMap<string, GitPatchUnit>,
  errors: string[],
  label: string
): void {
  const seen = new Set<string>();
  let previousOrdinal = -1;
  for (const id of ids) {
    if (seen.has(id)) errors.push(`${label} contains duplicate patch unit id: ${id}`);
    seen.add(id);
    const unit = units.get(id);
    if (!unit) {
      errors.push(`${label} references a patch unit that is not in this result: ${id}`);
      continue;
    }
    if (unit.ordinal <= previousOrdinal) errors.push(`${label} patch unit ids are not in canonical ordinal order`);
    previousOrdinal = unit.ordinal;
  }
}

function expectedOutcomeForRun(run: GitMinimizationRunFact): GitMinimizationRunFact["outcome"] {
  if (run.result === null || run.result.kind !== "DOCKER_ISOLATED") return "UNRESOLVED";
  if (run.result.verdict === "PASS") return "PASS";
  if (run.result.verdict === "FAIL") return "FAIL";
  return "UNRESOLVED";
}

function validateCertificate(
  label: "sufficiency" | "necessity",
  certificate: GitMinimizationCertificate,
  result: GitMinimizationResult,
  runs: ReadonlyMap<string, GitMinimizationRunFact>,
  errors: string[]
): void {
  const expected = label === "sufficiency"
    ? { direction: "FORWARD_FROM_BEFORE" as const, outcome: "FAIL" as const, role: "SUFFICIENCY_CERTIFICATION" as const }
    : { direction: "REVERSE_FROM_AFTER" as const, outcome: "PASS" as const, role: "NECESSITY_CERTIFICATION" as const };
  if (certificate.direction !== expected.direction) errors.push(`${label} certificate has the wrong direction`);
  if (certificate.expectedOutcome !== expected.outcome) errors.push(`${label} certificate has the wrong expected outcome`);
  if (certificate.runIds.length !== certificate.executionIds.length) errors.push(`${label} certificate run and execution id counts differ`);
  if (certificate.status === "NOT_RUN" && (certificate.runIds.length !== 0 || certificate.executionIds.length !== 0)) {
    errors.push(`${label} NOT_RUN certificate must not contain execution references`);
  }

  let completeEvidence = certificate.runIds.length === MINIMIZATION_CERTIFICATION_EXECUTIONS
    && certificate.executionIds.length === MINIMIZATION_CERTIFICATION_EXECUTIONS;
  const seenRuns = new Set<string>();
  const seenExecutions = new Set<string>();
  for (const [index, runId] of certificate.runIds.entries()) {
    if (seenRuns.has(runId)) {
      errors.push(`${label} certificate repeats run id: ${runId}`);
      completeEvidence = false;
    }
    seenRuns.add(runId);
    const run = runs.get(runId);
    if (!run) {
      errors.push(`${label} certificate references an absent run: ${runId}`);
      completeEvidence = false;
      continue;
    }
    const executionId = certificate.executionIds[index];
    if (executionId !== run.executionId) {
      errors.push(`${label} certificate execution id does not match run ${runId}`);
      completeEvidence = false;
    }
    if (seenExecutions.has(run.executionId)) {
      errors.push(`${label} certificate executions are not distinct`);
      completeEvidence = false;
    }
    seenExecutions.add(run.executionId);
    if (run.role !== expected.role || run.application.direction !== expected.direction) {
      errors.push(`${label} certificate references a run with the wrong role or direction: ${runId}`);
      completeEvidence = false;
    }
    if (!sameCanonical(run.candidateUnitIds, result.candidateUnitIds)) {
      errors.push(`${label} certificate run does not use the final candidate: ${runId}`);
      completeEvidence = false;
    }
    if (run.outcome !== expected.outcome || run.result?.kind !== "DOCKER_ISOLATED"
      || run.result.executor !== "NATIVE_DOCKER" || run.result.verdict !== expected.outcome) {
      // A NOT_CERTIFIED record intentionally retains the failed or injected
      // attempt that prevented certification. It is structurally valid but
      // cannot contribute to the three-run native-Docker proof predicate.
      completeEvidence = false;
    }
  }

  if (certificate.status === "CERTIFIED" && !completeEvidence) {
    errors.push(`${label} certificate claims CERTIFIED without three distinct matching native Docker runs`);
  }
  if (certificate.status !== "CERTIFIED" && completeEvidence) {
    errors.push(`${label} certificate with three distinct matching native Docker runs must be CERTIFIED`);
  }
}

type ProofGradeEvidence = {
  readonly valid: boolean;
  readonly errors: readonly string[];
};

/**
 * A proof cannot be reconstructed from repeat certification alone. Retain the
 * ordinary search evidence that established its precondition (clean baseline),
 * trigger (the full range), and one-minimality of the final candidate.
 *
 * This intentionally does not make an incomplete/non-proof search invalid:
 * callers may retain those records for diagnosis. The evidence only becomes a
 * mandatory predicate when a record claims proof or COMPLETED status.
 */
function proofGradeEvidence(
  result: GitMinimizationResult,
  attemptsByRunId: ReadonlyMap<string, GitMinimizationAttempt>
): ProofGradeEvidence {
  const evidenceErrors: string[] = [];
  const allUnitIds = result.patchUnits.map((unit) => unit.id);

  const hasLinkedNativeDockerRun = (
    run: GitMinimizationRunFact,
    phase: GitMinimizationAttempt["phase"],
    expectedCandidate: readonly string[],
    expectedOutcome: "PASS" | "FAIL"
  ): boolean => {
    const attempt = attemptsByRunId.get(run.runId);
    const expectedApplicationStatus = expectedCandidate.length === 0 ? "NO_PATCHES" : "APPLIED";
    return attempt !== undefined
      && attempt.phase === phase
      && attempt.runId === run.runId
      && attempt.outcome === expectedOutcome
      && sameCanonical(attempt.candidateUnitIds, expectedCandidate)
      && sameCanonical(run.candidateUnitIds, expectedCandidate)
      && sameCanonical(run.application.selectedUnitIds, expectedCandidate)
      && run.application.direction === ROLE_SEMANTICS[run.role].direction
      && run.application.status === expectedApplicationStatus
      && run.outcome === expectedOutcome
      && run.result?.kind === "DOCKER_ISOLATED"
      && run.result.executor === "NATIVE_DOCKER"
      && run.result.verdict === expectedOutcome;
  };

  const hasEvidence = (
    label: string,
    role: GitMinimizationRunFact["role"],
    phase: GitMinimizationAttempt["phase"],
    expectedCandidate: readonly string[],
    expectedOutcome: "PASS" | "FAIL"
  ): void => {
    const found = result.runs.some((run) => run.role === role
      && hasLinkedNativeDockerRun(run, phase, expectedCandidate, expectedOutcome));
    if (!found) {
      evidenceErrors.push(`${label} requires a linked native-Docker ${expectedOutcome} run with the exact candidate set`);
    }
  };

  if (allUnitIds.length === 0) {
    evidenceErrors.push("proof-grade evidence requires at least one full-range patch unit");
  }
  if (result.candidateUnitIds.length === 0) {
    evidenceErrors.push("proof-grade evidence requires a non-empty final candidate");
  }

  hasEvidence("baseline-before [] evidence", "BASELINE_BEFORE", "BASELINE", [], "PASS");
  hasEvidence("full-patch [all patch unit ids] evidence", "FULL_PATCH", "FULL_RANGE", allUnitIds, "FAIL");

  for (const unitId of result.candidateUnitIds) {
    const withoutUnit = result.candidateUnitIds.filter((candidateId) => candidateId !== unitId);
    hasEvidence(
      `one-minimal evidence after removing ${unitId}`,
      "ONE_MINIMAL",
      "ONE_MINIMAL",
      withoutUnit,
      "PASS"
    );
  }

  return { valid: evidenceErrors.length === 0, errors: evidenceErrors };
}

/**
 * Verify one stored minimization result without invoking Git, Docker, a model,
 * or repository code. This establishes schema and internal consistency only;
 * an optional digest retained outside the JSON detects a rewritten record but
 * is not a signature, identity assertion, or host-attestation claim.
 */
export function verifyGitMinimizationResult(value: unknown, expectedDigest?: string): GitMinimizationVerification {
  const errors: string[] = [];
  let resultDigest: string | null = null;
  try {
    const parsed = GitMinimizationResultSchema.safeParse(value);
    if (!parsed.success) {
      errors.push(`minimization result schema validation failed: ${parsed.error.message}`);
      return {
        valid: false,
        errors,
        resultDigest,
        externalDigestStatus: externalDigestStatus(expectedDigest, resultDigest, errors)
      };
    }
    const result = parsed.data;
    resultDigest = digestJson(result);
    const units = new Map<string, GitPatchUnit>();
    for (const [index, unit] of result.patchUnits.entries()) {
      if (unit.ordinal !== index) errors.push(`patch unit ordinal is not contiguous at offset ${index}`);
      if (units.has(unit.id)) errors.push(`duplicate patch unit id: ${unit.id}`);
      units.set(unit.id, unit);
      const bytes = Buffer.from(unit.pathBytesBase64, "base64");
      if (bytes.toString("base64") !== unit.pathBytesBase64) errors.push(`patch unit path bytes are not canonical base64: ${unit.id}`);
      const path = bytes.toString("utf8");
      if (!Buffer.from(path, "utf8").equals(bytes) || path !== unit.path) {
        errors.push(`patch unit path does not match its exact UTF-8 bytes: ${unit.id}`);
      }
      if (unit.pathDigest !== sha256Digest(bytes)) errors.push(`patch unit path digest does not match bytes: ${unit.id}`);
      const expectedId = digestJson({
        schemaVersion: GIT_MINIMIZATION_SCHEMA_VERSION,
        ordinal: unit.ordinal,
        pathDigest: unit.pathDigest,
        patchDigest: unit.patchDigest
      });
      if (unit.id !== expectedId) errors.push(`patch unit id does not match ordinal and digests: ${unit.id}`);
    }
    validateCandidateIds(result.candidateUnitIds, units, errors, "result candidate");

    const runs = new Map<string, GitMinimizationRunFact>();
    const executionIds = new Set<string>();
    const executionNonces = new Set<string>();
    const attemptsByRole = new Map<GitMinimizationRunFact["role"], number[]>();
    for (const run of result.runs) {
      if (runs.has(run.runId)) errors.push(`duplicate run id: ${run.runId}`);
      else runs.set(run.runId, run);
      if (run.runId !== expectedMinimizationRunId(run)) errors.push(`run id does not match canonical run fact: ${run.runId}`);
      if (run.executionId !== expectedMinimizationExecutionId(run)) errors.push(`execution id does not match nonce-bound run identity: ${run.runId}`);
      if (executionIds.has(run.executionId)) errors.push(`duplicate execution id: ${run.executionId}`);
      executionIds.add(run.executionId);
      if (executionNonces.has(run.executionNonce)) errors.push(`duplicate execution nonce: ${run.executionNonce}`);
      executionNonces.add(run.executionNonce);
      const roleAttempts = attemptsByRole.get(run.role) ?? [];
      roleAttempts.push(run.roleAttempt);
      attemptsByRole.set(run.role, roleAttempts);
      const roleSemantics = ROLE_SEMANTICS[run.role];
      if (run.application.direction !== roleSemantics.direction) {
        errors.push(`run direction does not match role ${run.role}: ${run.runId}`);
      }
      validateCandidateIds(run.candidateUnitIds, units, errors, `run candidate ${run.runId}`);
      if (!sameCanonical(run.application.selectedUnitIds, run.candidateUnitIds)) {
        errors.push(`run application selected units do not match candidate units: ${run.runId}`);
      }
      const selectedBytes = run.candidateUnitIds.reduce((total, id) => total + (units.get(id)?.patchBytes ?? 0), 0);
      if (run.application.candidatePatchBytes !== selectedBytes) {
        errors.push(`run application patch byte count does not match selected patch units: ${run.runId}`);
      }
      if (run.candidateUnitIds.length === 0
        && (run.application.candidatePatchBytes !== 0 || run.application.candidatePatchDigest !== sha256Digest(Buffer.alloc(0)))) {
        errors.push(`empty candidate application does not use the empty patch digest: ${run.runId}`);
      }
      if (run.application.status === "NO_PATCHES" && run.candidateUnitIds.length !== 0) {
        errors.push(`NO_PATCHES application has selected patch units: ${run.runId}`);
      }
      const expectedBase = run.application.direction === "FORWARD_FROM_BEFORE" ? result.before : result.after;
      if (expectedBase === null || !sameCanonical(run.application.base, expectedBase)) {
        errors.push(`run application base does not match the recorded direction endpoint: ${run.runId}`);
      }
      if ((run.sandbox === null) !== (run.result === null)) {
        errors.push(`run sandbox and execution result must be both present or both absent: ${run.runId}`);
      }
      if (run.sandbox !== null) {
        for (const auditError of validateSandboxPlanAudit(run.sandbox)) {
          errors.push(`run sandbox audit is invalid: ${run.runId}: ${auditError}`);
        }
      }
      if (run.result === null) {
        if (run.outcome !== "UNRESOLVED") errors.push(`run without execution result must be UNRESOLVED: ${run.runId}`);
      } else {
        if (run.sandbox?.kind !== run.result.kind) errors.push(`run sandbox kind does not match execution result: ${run.runId}`);
        if (run.sandbox?.witnessDigest !== run.frozenDigest) errors.push(`run sandbox witness digest does not match run frozen digest: ${run.runId}`);
        if (run.result.executor === "UNSAFE_LOCAL" && run.result.kind !== "UNSAFE_LOCAL") {
          errors.push(`unsafe-local executor has a non-local result kind: ${run.runId}`);
        }
        if (run.result.executor !== "UNSAFE_LOCAL" && run.result.kind !== "DOCKER_ISOLATED") {
          errors.push(`Docker executor has a non-Docker result kind: ${run.runId}`);
        }
        if (run.result.verdict === "PASS" && (!PASS_VERDICT_REASONS.has(run.result.reason) || run.result.exitCode !== 0)) {
          errors.push(`PASS execution result is inconsistent: ${run.runId}`);
        }
        if (run.result.verdict === "FAIL" && (!FAIL_VERDICT_REASONS.has(run.result.reason) || run.result.exitCode === null || run.result.exitCode === 0)) {
          errors.push(`FAIL execution result is inconsistent: ${run.runId}`);
        }
        if (run.result.kind === "UNSAFE_LOCAL" && (run.result.verdict !== "INAPPLICABLE" || run.result.reason !== "UNSAFE_LOCAL_NOT_PROOF")) {
          errors.push(`unsafe-local execution result is not explicitly inapplicable: ${run.runId}`);
        }
        if (run.outcome !== expectedOutcomeForRun(run)) errors.push(`run outcome contradicts its execution result: ${run.runId}`);
      }
      if (result.witness) {
        if (run.frozenDigest !== result.witness.frozenDigest || run.witnessDigest !== result.witness.witnessDigest) {
          errors.push(`run witness digests do not match the result witness summary: ${run.runId}`);
        }
      }
    }
    for (const [role, values] of attemptsByRole) {
      const ordered = [...values].sort((left, right) => left - right);
      if (!ordered.every((value, index) => value === index + 1)) {
        errors.push(`role attempts are not contiguous for ${role}`);
      }
    }
    const usedExecutions = result.runs.filter((run) => run.result !== null).length;
    if (result.budget.usedExecutions !== usedExecutions) {
      errors.push("used execution budget does not match recorded sandbox executions");
    }
    if (result.budget.usedExecutions > result.budget.maxExecutions) errors.push("used execution budget exceeds its maximum");

    const referencedRuns = new Set<string>();
    const attemptsByRunId = new Map<string, GitMinimizationAttempt>();
    for (const [index, attempt] of result.attempts.entries()) {
      if (attempt.ordinal !== index + 1) errors.push(`attempt ordinal is not contiguous at offset ${index}`);
      validateCandidateIds(attempt.candidateUnitIds, units, errors, `attempt ${attempt.ordinal} candidate`);
      if (attempt.runId === null) {
        if (attempt.outcome !== "NOT_RUN") errors.push(`attempt without a run must be NOT_RUN: ${attempt.ordinal}`);
        continue;
      }
      if (referencedRuns.has(attempt.runId)) errors.push(`multiple attempts reference run id: ${attempt.runId}`);
      referencedRuns.add(attempt.runId);
      if (!attemptsByRunId.has(attempt.runId)) attemptsByRunId.set(attempt.runId, attempt);
      const run = runs.get(attempt.runId);
      if (!run) {
        errors.push(`attempt references an absent run: ${attempt.runId}`);
        continue;
      }
      const semantics = ROLE_SEMANTICS[run.role];
      if (attempt.phase !== semantics.phase) errors.push(`attempt phase does not match run role: ${attempt.runId}`);
      if (!sameCanonical(attempt.candidateUnitIds, run.candidateUnitIds)) errors.push(`attempt candidate does not match run candidate: ${attempt.runId}`);
      if (attempt.outcome !== run.outcome) errors.push(`attempt outcome does not match run outcome: ${attempt.runId}`);
      if (attempt.note !== run.note) errors.push(`attempt note does not match run note: ${attempt.runId}`);
    }
    for (const run of result.runs) {
      if (!referencedRuns.has(run.runId)) errors.push(`recorded run is not referenced by an attempt: ${run.runId}`);
    }

    validateCertificate("sufficiency", result.certification.sufficiency, result, runs, errors);
    validateCertificate("necessity", result.certification.necessity, result, runs, errors);
    for (const run of result.runs) {
      if (run.role === "SUFFICIENCY_CERTIFICATION" && !result.certification.sufficiency.runIds.includes(run.runId)) {
        errors.push(`sufficiency certification run is absent from its certificate: ${run.runId}`);
      }
      if (run.role === "NECESSITY_CERTIFICATION" && !result.certification.necessity.runIds.includes(run.runId)) {
        errors.push(`necessity certification run is absent from its certificate: ${run.runId}`);
      }
    }

    const proofEvidence = proofGradeEvidence(result, attemptsByRunId);

    const sufficiencyCertified = result.certification.sufficiency.status === "CERTIFIED";
    const necessityCertified = result.certification.necessity.status === "CERTIFIED";
    if (result.proof.sufficiencyCertified !== sufficiencyCertified) errors.push("proof sufficiency flag does not match certificate status");
    if (result.proof.necessityCertified !== necessityCertified) errors.push("proof necessity flag does not match certificate status");
    if (result.proof.dockerIsolated !== (result.proof.executionTrust === "NATIVE_DOCKER")) {
      errors.push("proof Docker-isolated flag does not match execution provenance");
    }
    for (const run of result.runs) {
      if (run.result !== null && run.result.executor !== result.proof.executionTrust) {
        errors.push(`run executor does not match result execution provenance: ${run.runId}`);
      }
    }
    const expectedProof = result.proof.executionTrust === "NATIVE_DOCKER"
      && result.minimality.oneMinimal
      && sufficiencyCertified
      && necessityCertified
      && proofEvidence.valid
      && errors.length === 0
      && result.errors.length === 0;
    if ((result.proof.isProof || result.status === "COMPLETED") && !proofEvidence.valid) {
      errors.push(...proofEvidence.errors);
    }
    if (result.proof.isProof !== expectedProof) errors.push("proof bit contradicts provenance, minimality, certificates, or recorder errors");
    if (expectedProof && result.status !== "COMPLETED") errors.push("completed proof evidence has a non-COMPLETED status");
    if (result.status === "COMPLETED" && !expectedProof) errors.push("COMPLETED status lacks complete proof evidence");

    const status = externalDigestStatus(expectedDigest, resultDigest, errors);
    return { valid: errors.length === 0, errors, resultDigest, externalDigestStatus: status, result };
  } catch (error) {
    errors.push(`minimization result verification failed safely: ${errorMessage(error)}`);
    return {
      valid: false,
      errors,
      resultDigest,
      externalDigestStatus: externalDigestStatus(expectedDigest, resultDigest, errors)
    };
  }
}

/** Safely read and verify a standalone minimization result without running its repository code. */
export function verifyGitMinimizationResultFile(filePath: string, expectedDigest?: string): GitMinimizationVerification {
  const errors: string[] = [];
  try {
    const absolute = resolve(filePath);
    const stat = lstatSync(absolute);
    if (stat.isSymbolicLink() || !stat.isFile()) {
      errors.push("minimization result must be a regular, non-symlink file");
    } else if (stat.size > MAX_MINIMIZATION_RESULT_BYTES) {
      errors.push("minimization result exceeds FaultLine's verification read limit");
    } else {
      const bytes = readFileSync(absolute);
      if (bytes.length > MAX_MINIMIZATION_RESULT_BYTES) {
        errors.push("minimization result changed beyond FaultLine's verification read limit while reading");
      } else {
        let value: unknown;
        try {
          value = JSON.parse(bytes.toString("utf8"));
        } catch (error) {
          errors.push(`minimization result contains invalid JSON: ${errorMessage(error)}`);
          value = undefined;
        }
        if (errors.length === 0) return verifyGitMinimizationResult(value, expectedDigest);
      }
    }
  } catch (error) {
    errors.push(`minimization result cannot be read safely: ${errorMessage(error)}`);
  }
  return {
    valid: false,
    errors,
    resultDigest: null,
    externalDigestStatus: externalDigestStatus(expectedDigest, null, errors)
  };
}

/** Create each output parent without traversing a symlink or special file. */
async function ensureRealOutputDirectory(directory: string): Promise<void> {
  const absolute = resolve(directory);
  const root = parse(absolute).root;
  const suffix = relative(root, absolute);
  const parts = suffix ? suffix.split(/[\\/]+/).filter(Boolean) : [];
  let current = root;
  const rootEntry = await lstatIfPresent(current);
  const safeRoot = rootEntry === null ? null : resolveSafeDirectorySegment(current);
  if (safeRoot === null) {
    throw new Error(`Minimization output root is not a real directory: ${root}`);
  }
  current = safeRoot;
  for (const part of parts) {
    current = join(current, part);
    const entry = await lstatIfPresent(current);
    if (entry === null) {
      await mkdir(current, { mode: 0o700 });
      const safeCurrent = resolveSafeDirectorySegment(current);
      if (safeCurrent === null) {
        throw new Error(`Minimization output parent is not a real directory: ${current}`);
      }
      current = safeCurrent;
      continue;
    }
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) {
      throw new Error(`Minimization output cannot traverse a symbolic link or non-directory: ${current}`);
    }
    current = safeCurrent;
  }
}

/**
 * Writes a canonical, self-consistent result atomically enough for local
 * evidence bundles. The caller must retain its returned digest externally to
 * detect a record that is rewritten together with its internal fields.
 */
export async function writeGitMinimizationResult(filePath: string, result: GitMinimizationResult): Promise<WrittenGitMinimizationResult> {
  const verification = verifyGitMinimizationResult(result);
  if (!verification.valid || !verification.result || !verification.resultDigest) {
    throw new Error(`Refusing to write an invalid Git minimization result: ${verification.errors.join("; ")}`);
  }
  const absolute = resolve(filePath);
  const directory = dirname(absolute);
  await ensureRealOutputDirectory(directory);
  if (await lstatIfPresent(absolute)) {
    throw new Error(`Git minimization result already exists and will not be replaced: ${absolute}`);
  }
  const temporary = join(directory, `.${randomUUID()}.faultline-git-minimization.tmp`);
  const content = `${canonicalJson(verification.result)}\n`;
  try {
    await writeFile(temporary, content, { encoding: "utf8", flag: "wx", mode: 0o600 });
    try {
      // link(2) creates the final pathname only if it is absent, unlike a
      // replace-capable rename on POSIX. The temporary lives in the same
      // verified directory, so this is an atomic write-once publish step.
      await link(temporary, absolute);
    } catch (error) {
      const code = error instanceof Error && "code" in error ? (error as NodeJS.ErrnoException).code : undefined;
      if (code === "EEXIST") {
        throw new Error(`Git minimization result already exists and will not be replaced: ${absolute}`);
      }
      throw error;
    }
  } finally {
    await rm(temporary, { force: true });
  }
  return { path: absolute, resultDigest: verification.resultDigest };
}
