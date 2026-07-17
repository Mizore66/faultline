import { mkdtemp, rm } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { tmpdir } from "node:os";
import { isAbsolute, join, resolve } from "node:path";
import { z } from "zod";
import { digestJson, sha256 } from "./canonical.js";
import {
  assertSafeGitMaterialization,
  materializeGitTree,
  runHardenedGit
} from "./git-materialization.js";
import { materializeFrozenOverlays } from "./safe-overlay.js";
import { redactText } from "./redaction.js";
import {
  auditSandboxPlan,
  createSandboxPlan,
  executeSandboxPlan,
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

/**
 * This recorder investigates ordinary Git commits.  It deliberately does not
 * claim to observe, intercept, or reconstruct native Codex lifecycle events.
 * A caller supplies the commit range and an already human-approved frozen
 * witness; FaultLine replays that witness against immutable Git tree states.
 */
export const GIT_INVESTIGATION_SCHEMA_VERSION = "faultline.git-investigation.v1" as const;
export const STABLE_EXECUTION_COUNT = 3 as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const GIT_OBJECT_ID = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/;
const SAFE_GIT_REVISION = /^(?!-)[^\0\r\n]{1,512}$/;
const EVIDENCE_LOG_PREVIEW_BYTES = 16 * 1024;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const GitObjectIdSchema = z.string().regex(GIT_OBJECT_ID, "expected a 40- or 64-character lowercase Git object id");
const GitRevisionSchema = z.string().regex(SAFE_GIT_REVISION, "revision must be non-empty, cannot start with '-', and cannot contain NUL or line breaks");
const TimestampSchema = z.string().datetime({ offset: true });

export const GitRangeSchema = z.object({
  ancestor: GitRevisionSchema,
  descendant: GitRevisionSchema
}).strict();

export const GitInvestigationSandboxSchema = z.object({
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

/** Serializable portion of an investigation request. The runner stays injected, never serialized. */
export const GitInvestigationRequestSchema = z.object({
  repository: z.string().min(1).max(16_384),
  range: GitRangeSchema,
  frozenWitness: FrozenWitnessSchema,
  /** A digest recorded outside this in-memory request is required for proof-grade replay. */
  expectedFrozenDigest: DigestSchema,
  sandbox: GitInvestigationSandboxSchema,
  /** Caps Git enumeration; the ancestor itself counts as one state. */
  maxStates: z.number().int().min(2).max(512).default(128)
}).strict();

export type GitInvestigationRequestData = z.output<typeof GitInvestigationRequestSchema>;
export type GitInvestigationSandbox = z.output<typeof GitInvestigationSandboxSchema>;

export interface GitInvestigationRequest {
  readonly repository: string;
  readonly range: z.input<typeof GitRangeSchema>;
  readonly frozenWitness: FrozenWitness;
  readonly expectedFrozenDigest: string;
  readonly sandbox: z.input<typeof GitInvestigationSandboxSchema>;
  readonly maxStates?: number;
  /** Injectable for deterministic tests or a controlled production runner. */
  readonly runner?: SandboxCommandRunner;
}

export const GitCommitStateSchema = z.object({
  index: z.number().int().nonnegative(),
  commit: GitObjectIdSchema,
  tree: GitObjectIdSchema
}).strict();

export const MaterializedOverlaySchema = z.object({
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

export const GitInvestigationRunFactSchema = z.object({
  schemaVersion: z.literal(GIT_INVESTIGATION_SCHEMA_VERSION),
  runId: DigestSchema,
  executionId: DigestSchema,
  /** Random recorder nonce distinguishes separate invocations of the same state/attempt. */
  executionNonce: z.string().uuid(),
  stateIndex: z.number().int().nonnegative(),
  executionAttempt: z.number().int().min(1).max(STABLE_EXECUTION_COUNT),
  commit: GitObjectIdSchema,
  tree: GitObjectIdSchema,
  frozenDigest: DigestSchema,
  witnessDigest: DigestSchema,
  startedAt: TimestampSchema,
  finishedAt: TimestampSchema,
  durationMs: z.number().int().nonnegative(),
  overlays: z.array(MaterializedOverlaySchema),
  sandbox: SandboxAuditSchema,
  result: z.object({
    kind: z.enum(["DOCKER_ISOLATED", "UNSAFE_LOCAL"]),
    executor: z.enum(["NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL"]),
    verdict: z.enum(["PASS", "FAIL", "ERROR", "INAPPLICABLE"]),
    reason: z.enum([
      "EXIT_ZERO",
      "EXIT_NONZERO",
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
  }).strict()
}).strict();

export const StableGitStateSchema = z.object({
  stateIndex: z.number().int().nonnegative(),
  commit: GitObjectIdSchema,
  tree: GitObjectIdSchema,
  verdict: z.enum(["PASS", "FAIL"]),
  executionIds: z.array(DigestSchema).length(STABLE_EXECUTION_COUNT),
  runIds: z.array(DigestSchema).length(STABLE_EXECUTION_COUNT)
}).strict();

export const StableGitTransitionSchema = z.object({
  kind: z.enum(["PASS_TO_FAIL", "FAIL_TO_PASS"]),
  before: StableGitStateSchema,
  after: StableGitStateSchema
}).strict();

const WitnessVerificationSummarySchema = z.object({
  valid: z.boolean(),
  errors: z.array(z.string()),
  frozenDigest: DigestSchema.nullable(),
  witnessDigest: DigestSchema.nullable(),
  externalDigestStatus: z.enum(["NOT_PROVIDED", "MATCH", "MISMATCH"]),
  approval: z.object({ actor: z.string(), approvedAt: TimestampSchema }).nullable()
}).strict();

export const GitInvestigationResultSchema = z.object({
  schemaVersion: z.literal(GIT_INVESTIGATION_SCHEMA_VERSION),
  recorder: z.literal("git-commit-range-replay"),
  nativeCodexInterception: z.literal(false),
  status: z.enum([
    "COMPLETED",
    "INVALID_REQUEST",
    "INVALID_WITNESS",
    "RANGE_ERROR",
    "CONFIGURATION_ERROR",
    "SANDBOX_UNAVAILABLE",
    "EXECUTION_ERROR"
  ]),
  repository: z.string().nullable(),
  requestedRange: GitRangeSchema.nullable(),
  resolvedRange: z.object({
    ancestor: GitCommitStateSchema,
    descendant: GitCommitStateSchema
  }).strict().nullable(),
  witness: WitnessVerificationSummarySchema.nullable(),
  executionsPerState: z.literal(STABLE_EXECUTION_COUNT),
  states: z.array(GitCommitStateSchema),
  runs: z.array(GitInvestigationRunFactSchema),
  stableStates: z.array(StableGitStateSchema),
  transitions: z.array(StableGitTransitionSchema),
  nonMonotonic: z.boolean(),
  proof: z.object({
    requiresDockerIsolation: z.literal(true),
    dockerIsolated: z.boolean(),
    executionTrust: z.enum(["NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL"]),
    proofTransitions: z.number().int().nonnegative(),
    isProof: z.boolean(),
    reason: z.string()
  }).strict(),
  errors: z.array(z.string())
}).strict();

export type GitCommitState = z.infer<typeof GitCommitStateSchema>;
export type MaterializedOverlay = z.infer<typeof MaterializedOverlaySchema>;
export type GitInvestigationRunFact = z.infer<typeof GitInvestigationRunFactSchema>;
export type StableGitState = z.infer<typeof StableGitStateSchema>;
export type StableGitTransition = z.infer<typeof StableGitTransitionSchema>;
export type GitInvestigationResult = z.infer<typeof GitInvestigationResultSchema>;

type ProcessResult = {
  exitCode: number | null;
  stdout: string;
  stderr: string;
  error?: string;
};

function sha256Digest(value: string | Buffer): string {
  return `sha256:${sha256(value)}`;
}

function redactedEvidencePreview(value: string, stream: "stdout" | "stderr"): {
  value: string;
  truncated: boolean;
  redacted: boolean;
} {
  const bytes = Buffer.from(value, "utf8");
  const preview = bytes.subarray(0, EVIDENCE_LOG_PREVIEW_BYTES).toString("utf8");
  const safe = redactText(preview, `$.run.${stream}`);
  return { value: safe.value, truncated: bytes.length > EVIDENCE_LOG_PREVIEW_BYTES, redacted: safe.report.redacted };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function runGit(repository: string, args: readonly string[]): Promise<ProcessResult> {
  const result = await runHardenedGit(repository, args);
  return {
    exitCode: result.exitCode,
    stdout: result.stdout.toString("utf8"),
    stderr: result.stderr.toString("utf8"),
    ...(result.error === undefined ? {} : { error: result.error })
  };
}

async function gitText(repository: string, args: readonly string[]): Promise<string> {
  const result = await runGit(repository, args);
  if (result.exitCode !== 0 || result.error !== undefined) {
    const detail = [result.stderr.trim(), result.error].filter((value): value is string => Boolean(value)).join("; ");
    throw new Error(`Git ${args.join(" ")} failed: ${detail || `exit ${result.exitCode ?? "unknown"}`}`);
  }
  return result.stdout.trim();
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

async function resolveTree(repository: string, commit: string): Promise<string> {
  const value = await gitText(repository, ["rev-parse", "--verify", "--end-of-options", `${commit}^{tree}`]);
  if (!GIT_OBJECT_ID.test(value)) throw new Error(`Git returned an invalid tree object id for commit ${commit}.`);
  return value;
}

async function resolveCommitRange(repository: string, range: z.output<typeof GitRangeSchema>, maxStates: number): Promise<GitCommitState[]> {
  const ancestorCommit = await resolveCommit(repository, range.ancestor);
  const descendantCommit = await resolveCommit(repository, range.descendant);
  const ancestorCheck = await runGit(repository, ["merge-base", "--is-ancestor", ancestorCommit, descendantCommit]);
  if (ancestorCheck.exitCode !== 0) {
    if (ancestorCheck.exitCode === 1) {
      throw new Error(`Range ancestor ${ancestorCommit} is not an ancestor of descendant ${descendantCommit}.`);
    }
    throw new Error(`Unable to verify ancestry: ${ancestorCheck.stderr.trim() || ancestorCheck.error || "Git failed."}`);
  }

  // One more than the cap makes a too-large range explicit rather than silently truncated.
  const listed = await gitText(repository, [
    "rev-list",
    "--reverse",
    "--ancestry-path",
    `--max-count=${maxStates}`,
    "--end-of-options",
    `${ancestorCommit}..${descendantCommit}`
  ]);
  const descendants = listed ? listed.split(/\r?\n/).filter(Boolean) : [];
  const commits = ancestorCommit === descendantCommit ? [ancestorCommit] : [ancestorCommit, ...descendants];
  if (commits.length > maxStates) {
    throw new Error(`Commit range exceeds the configured maxStates limit of ${maxStates}.`);
  }
  if (ancestorCommit !== descendantCommit && descendants.length === maxStates) {
    const overflow = await gitText(repository, [
      "rev-list",
      "--count",
      "--ancestry-path",
      "--end-of-options",
      `${ancestorCommit}..${descendantCommit}`
    ]);
    const count = Number.parseInt(overflow, 10);
    if (!Number.isSafeInteger(count) || count + 1 > maxStates) {
      throw new Error(`Commit range exceeds the configured maxStates limit of ${maxStates}.`);
    }
  }
  if (commits.length < 1 || commits.some((commit) => !GIT_OBJECT_ID.test(commit))) {
    throw new Error("Git range enumeration returned an invalid commit object id.");
  }
  const states: GitCommitState[] = [];
  for (let index = 0; index < commits.length; index += 1) {
    const commit = commits[index];
    if (!commit) throw new Error("Git range enumeration unexpectedly omitted a commit.");
    states.push({ index, commit, tree: await resolveTree(repository, commit) });
  }
  return states;
}

function planRequestForState(
  sandbox: GitInvestigationSandbox,
  worktree: string,
  frozenWitness: FrozenWitness
): SandboxPlanRequest {
  return {
    witness: { digest: frozenWitness.frozenDigest, command: frozenWitness.proposal.witness.command },
    sourceDirectory: worktree,
    mode: sandbox.mode,
    ...(sandbox.image === undefined ? {} : { image: sandbox.image }),
    ...(sandbox.environment === undefined ? {} : { environment: sandbox.environment }),
    ...(sandbox.allowedEnvironment === undefined ? {} : { allowedEnvironment: sandbox.allowedEnvironment }),
    ...(sandbox.limits === undefined ? {} : { limits: sandbox.limits as Partial<SandboxLimits> }),
    ...(sandbox.allowUnsafeLocal === undefined ? {} : { allowUnsafeLocal: sandbox.allowUnsafeLocal })
  };
}

function stableStates(
  states: readonly GitCommitState[],
  runs: readonly GitInvestigationRunFact[],
  sandboxKind: GitInvestigationSandbox["mode"]
): StableGitState[] {
  if (sandboxKind !== "DOCKER_ISOLATED") return [];
  const stable: StableGitState[] = [];
  for (const state of states) {
    const stateRuns = runs
      .filter((run) => run.stateIndex === state.index)
      .sort((left, right) => left.executionAttempt - right.executionAttempt);
    if (stateRuns.length !== STABLE_EXECUTION_COUNT) continue;
    const attempts = new Set(stateRuns.map((run) => run.executionAttempt));
    const executionIds = new Set(stateRuns.map((run) => run.executionId));
    if (attempts.size !== STABLE_EXECUTION_COUNT || executionIds.size !== STABLE_EXECUTION_COUNT) continue;
    const verdict = stateRuns[0]?.result.verdict;
    if ((verdict !== "PASS" && verdict !== "FAIL") || !stateRuns.every((run) => run.result.kind === "DOCKER_ISOLATED"
      && run.result.verdict === verdict)) {
      continue;
    }
    stable.push({
      stateIndex: state.index,
      commit: state.commit,
      tree: state.tree,
      verdict,
      executionIds: stateRuns.map((run) => run.executionId),
      runIds: stateRuns.map((run) => run.runId)
    });
  }
  return stable;
}

function findTransitions(states: readonly StableGitState[]): StableGitTransition[] {
  const output: StableGitTransition[] = [];
  for (let index = 1; index < states.length; index += 1) {
    const before = states[index - 1];
    const after = states[index];
    if (!before || !after || after.stateIndex !== before.stateIndex + 1 || before.verdict === after.verdict) continue;
    output.push({
      kind: before.verdict === "PASS" ? "PASS_TO_FAIL" : "FAIL_TO_PASS",
      before,
      after
    });
  }
  return output;
}

function hasNonMonotonicTransitions(transitions: readonly StableGitTransition[]): boolean {
  return transitions.some((transition) => transition.kind === "PASS_TO_FAIL")
    && transitions.some((transition) => transition.kind === "FAIL_TO_PASS");
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
    proofTransitions: 0,
    isProof: false,
    reason
  };
}

function baseResult(
  status: z.infer<typeof GitInvestigationResultSchema>["status"],
  sandboxMode: "DOCKER_ISOLATED" | "UNSAFE_LOCAL",
  errors: readonly string[],
  fields: Pick<GitInvestigationResult, "repository" | "requestedRange" | "resolvedRange" | "witness">,
  executionTrust = executionTrustFor(sandboxMode, false)
): GitInvestigationResult {
  return GitInvestigationResultSchema.parse({
    schemaVersion: GIT_INVESTIGATION_SCHEMA_VERSION,
    recorder: "git-commit-range-replay",
    nativeCodexInterception: false,
    status,
    ...fields,
    executionsPerState: STABLE_EXECUTION_COUNT,
    states: [],
    runs: [],
    stableStates: [],
    transitions: [],
    nonMonotonic: false,
    proof: emptyProof(sandboxMode, errors[0] ?? "Investigation did not produce proof.", executionTrust),
    errors: [...errors]
  });
}

function runFact(
  state: GitCommitState,
  executionAttempt: number,
  frozenWitness: FrozenWitness,
  overlays: readonly MaterializedOverlay[],
  sandboxAudit: SandboxPlanAudit,
  result: SandboxExecutionResult,
  startedAt: string,
  finishedAt: string,
  durationMs: number
): GitInvestigationRunFact {
  const executionNonce = randomUUID();
  const executionId = digestJson({
    schemaVersion: GIT_INVESTIGATION_SCHEMA_VERSION,
    executionNonce,
    stateIndex: state.index,
    commit: state.commit,
    tree: state.tree,
    frozenDigest: frozenWitness.frozenDigest,
    executionAttempt
  });
  const stdoutPreview = redactedEvidencePreview(result.stdout, "stdout");
  const stderrPreview = redactedEvidencePreview(result.stderr, "stderr");
  const unsigned = {
    schemaVersion: GIT_INVESTIGATION_SCHEMA_VERSION,
    executionId,
    executionNonce,
    stateIndex: state.index,
    executionAttempt,
    commit: state.commit,
    tree: state.tree,
    frozenDigest: frozenWitness.frozenDigest,
    witnessDigest: frozenWitness.witnessDigest,
    startedAt,
    finishedAt,
    durationMs,
    overlays: [...overlays],
    sandbox: sandboxAudit,
    result: {
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
    }
  };
  return GitInvestigationRunFactSchema.parse({ ...unsigned, runId: digestJson(unsigned) });
}

function classifyStatus(runs: readonly GitInvestigationRunFact[], errors: readonly string[]): GitInvestigationResult["status"] {
  if (errors.length > 0) return "EXECUTION_ERROR";
  if (runs.some((run) => run.result.reason === "SANDBOX_UNAVAILABLE")) return "SANDBOX_UNAVAILABLE";
  if (runs.some((run) => run.result.verdict === "ERROR")) return "EXECUTION_ERROR";
  return "COMPLETED";
}

/**
 * Resolve an ancestor commit range and replay an independently frozen witness
 * three times per commit in detached temporary worktrees. Docker-isolated
 * records can become proof; explicitly unsafe-local records are always
 * INAPPLICABLE and never yield stable transitions.
 */
export async function investigateGitRange(request: GitInvestigationRequest): Promise<GitInvestigationResult> {
  const parsedRequest = GitInvestigationRequestSchema.safeParse({
    repository: request.repository,
    range: request.range,
    frozenWitness: request.frozenWitness,
    expectedFrozenDigest: request.expectedFrozenDigest,
    sandbox: request.sandbox,
    ...(request.maxStates === undefined ? {} : { maxStates: request.maxStates })
  });
  const requestedSandboxMode = request.sandbox.mode === "UNSAFE_LOCAL" ? "UNSAFE_LOCAL" : "DOCKER_ISOLATED";
  const requestedExecutionTrust = executionTrustFor(requestedSandboxMode, request.runner !== undefined);
  if (!parsedRequest.success) {
    return baseResult("INVALID_REQUEST", requestedSandboxMode, [`Invalid Git investigation request: ${parsedRequest.error.message}`], {
      repository: null,
      requestedRange: null,
      resolvedRange: null,
      witness: null
    }, requestedExecutionTrust);
  }

  const input = parsedRequest.data;
  const verification = verifyFrozenWitnessRecord(input.frozenWitness, input.expectedFrozenDigest);
  const summary = witnessSummary(input.frozenWitness, verification);
  if (!verification.valid || verification.externalDigestStatus !== "MATCH") {
    return baseResult("INVALID_WITNESS", input.sandbox.mode, verification.errors.length > 0
      ? verification.errors
      : ["Frozen witness did not match the externally supplied digest."], {
      repository: null,
      requestedRange: input.range,
      resolvedRange: null,
      witness: summary
    }, executionTrustFor(input.sandbox.mode, request.runner !== undefined));
  }

  let repository: string;
  let states: GitCommitState[];
  try {
    repository = await resolveRepositoryRoot(input.repository);
    states = await resolveCommitRange(repository, input.range, input.maxStates);
    await assertSafeGitMaterialization(repository, states.map((state) => state.commit));
  } catch (error) {
    return baseResult("RANGE_ERROR", input.sandbox.mode, [errorMessage(error)], {
      repository: null,
      requestedRange: input.range,
      resolvedRange: null,
      witness: summary
    }, executionTrustFor(input.sandbox.mode, request.runner !== undefined));
  }

  const first = states[0];
  const last = states[states.length - 1];
  if (!first || !last) {
    return baseResult("RANGE_ERROR", input.sandbox.mode, ["Git range resolved to no states."], {
      repository,
      requestedRange: input.range,
      resolvedRange: null,
      witness: summary
    }, executionTrustFor(input.sandbox.mode, request.runner !== undefined));
  }

  const tempRoot = await mkdtemp(join(tmpdir(), "faultline-git-investigation-"));
  const runs: GitInvestigationRunFact[] = [];
  const errors: string[] = [];
  let configurationError = false;
  try {
    for (const state of states) {
      let materialized: Awaited<ReturnType<typeof materializeGitTree>> | null = null;
      try {
        materialized = await materializeGitTree({
          repository,
          commit: state.commit,
          tempRoot,
          name: `state-${String(state.index).padStart(4, "0")}-${state.commit.slice(0, 16)}`
        });
        const overlays = await materializeFrozenOverlays(materialized.worktree, input.frozenWitness, materialized);
        let plan;
        try {
          plan = createSandboxPlan(planRequestForState(input.sandbox, materialized.worktree, input.frozenWitness));
        } catch (error) {
          configurationError = true;
          throw new Error(`Could not create sandbox plan for ${state.commit}: ${errorMessage(error)}`);
        }
        const audit = auditSandboxPlan(plan);
        for (let attempt = 1; attempt <= STABLE_EXECUTION_COUNT; attempt += 1) {
          const startedEpoch = Date.now();
          const startedAt = new Date(startedEpoch).toISOString();
          const execution = await executeSandboxPlan(plan, request.runner);
          const finishedEpoch = Date.now();
          runs.push(runFact(
            state,
            attempt,
            input.frozenWitness,
            overlays,
            audit,
            execution,
            startedAt,
            new Date(finishedEpoch).toISOString(),
            Math.max(0, finishedEpoch - startedEpoch)
          ));
        }
      } catch (error) {
        errors.push(errorMessage(error));
      } finally {
        if (materialized) {
          const cleanupError = await materialized.cleanup();
          if (cleanupError) errors.push(cleanupError);
        }
      }
      // A malformed plan is global configuration, so no later worktree could
      // make it valid. Stop before generating a misleading partial timeline.
      if (configurationError) break;
    }
  } finally {
    // mkdtemp created this exact directory beneath the OS temp root, never a caller-provided path.
    await rm(tempRoot, { recursive: true, force: true });
  }

  if (configurationError) {
    const result = {
      schemaVersion: GIT_INVESTIGATION_SCHEMA_VERSION,
      recorder: "git-commit-range-replay" as const,
      nativeCodexInterception: false as const,
      status: "CONFIGURATION_ERROR" as const,
      repository,
      requestedRange: input.range,
      resolvedRange: { ancestor: first, descendant: last },
      witness: summary,
      executionsPerState: STABLE_EXECUTION_COUNT,
      states,
      runs,
      stableStates: [],
      transitions: [],
      nonMonotonic: false,
      proof: emptyProof(input.sandbox.mode, errors[0] ?? "Sandbox configuration failed.", executionTrustFor(input.sandbox.mode, request.runner !== undefined)),
      errors
    };
    return GitInvestigationResultSchema.parse(result);
  }

  const executionTrust = executionTrustFor(input.sandbox.mode, request.runner !== undefined);
  const stable = stableStates(states, runs, input.sandbox.mode);
  const transitions = findTransitions(stable);
  const status = classifyStatus(runs, errors);
  const dockerIsolated = executionTrust === "NATIVE_DOCKER";
  const proofReason = executionTrust === "INJECTED_RUNNER"
    ? "An injected runner produced these observations; FaultLine refuses to certify it as native Docker proof."
    : !dockerIsolated
    ? "Unsafe local execution is explicitly inapplicable and cannot be proof."
    : status === "SANDBOX_UNAVAILABLE"
      ? "Docker was unavailable; no execution result is proof."
      : status === "EXECUTION_ERROR"
        ? "An execution or worktree error prevents an investigation-wide proof claim."
        : transitions.length === 0
          ? "No adjacent states produced three matching Docker PASS/FAIL executions."
          : "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states.";
  const result = {
    schemaVersion: GIT_INVESTIGATION_SCHEMA_VERSION,
    recorder: "git-commit-range-replay" as const,
    nativeCodexInterception: false as const,
    status,
    repository,
    requestedRange: input.range,
    resolvedRange: { ancestor: first, descendant: last },
    witness: summary,
    executionsPerState: STABLE_EXECUTION_COUNT,
    states,
    runs,
    stableStates: stable,
    transitions,
    nonMonotonic: hasNonMonotonicTransitions(transitions),
    proof: {
      requiresDockerIsolation: true as const,
      dockerIsolated,
      executionTrust,
      proofTransitions: transitions.length,
      isProof: dockerIsolated && status === "COMPLETED" && transitions.length > 0,
      reason: proofReason
    },
    errors
  };
  return GitInvestigationResultSchema.parse(result);
}
