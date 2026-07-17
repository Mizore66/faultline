import { mkdtemp, rm } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { z } from "zod";
import { digestJson, sha256 } from "./canonical.js";
import {
  buildEffectiveRuntimeMapping,
  computeEnvironmentFingerprint,
  ENVIRONMENT_CHANGED_PROOF_MESSAGE,
  EnvironmentFingerprintSchema,
  environmentHomogeneity,
  missingRuntimeMappingDigests,
  type EnvironmentFingerprint,
  type RuntimeMapping
} from "./environment-fingerprint.js";
import { STABLE_EXECUTION_COUNT } from "./git-investigation.js";
import { materializeGitTree } from "./git-materialization.js";
import {
  readVerifiedCodexLifecycleLedger,
  type CodexLifecycleLedger
} from "./ledger.js";
import { redactText } from "./redaction.js";
import {
  auditSandboxPlan,
  createSandboxPlan,
  executeSandboxPlan,
  type SandboxCommandRunner,
  type SandboxExecutionResult,
  type SandboxPlanAudit
} from "./sandbox.js";
import { MaterializedOverlaySchema, materializeFrozenOverlays, type MaterializedOverlay } from "./safe-overlay.js";
import { TurnTreeSnapshotSchema } from "./turn-snapshot.js";
import {
  verifyFrozenWitnessRecord,
  type FrozenWitness,
  type WitnessLockVerification
} from "./witness-lock.js";

export const TURN_INVESTIGATION_SCHEMA_VERSION = "faultline.turn-investigation.v1" as const;
export const TURN_EVIDENCE_LABEL_EXPERIMENTAL = "Turn localization — experimental evidence" as const;

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);
const DigestPinnedImageSchema = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/);
const GitObjectIdSchema = z.string().regex(/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/);
const TimestampSchema = z.string().datetime({ offset: true });
const EVIDENCE_LOG_PREVIEW_BYTES = 16 * 1024;

/** Synthetic turn id for the SessionStart baseline (turn zero). */
export const SESSION_BASELINE_TURN_ID = "session-baseline" as const;

export const TurnStateSchema = z.object({
  index: z.number().int().nonnegative(),
  turnId: z.string().min(1),
  /** 0 is the session baseline; positive values are completed Codex turns. */
  turnOrdinal: z.number().int().nonnegative(),
  role: z.enum(["SESSION_BASELINE", "TURN"]),
  treeDigest: GitObjectIdSchema,
  snapshotDigest: DigestSchema,
  dirty: z.boolean()
}).strict();

export type TurnState = z.infer<typeof TurnStateSchema>;

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

export const TurnInvestigationRunFactSchema = z.object({
  schemaVersion: z.literal(TURN_INVESTIGATION_SCHEMA_VERSION),
  runId: DigestSchema,
  executionId: DigestSchema,
  executionNonce: z.string().uuid(),
  stateIndex: z.number().int().nonnegative(),
  executionAttempt: z.number().int().min(1).max(STABLE_EXECUTION_COUNT),
  turnId: z.string().min(1),
  turnOrdinal: z.number().int().nonnegative(),
  role: z.enum(["SESSION_BASELINE", "TURN"]),
  treeDigest: GitObjectIdSchema,
  snapshotDigest: DigestSchema,
  environmentFingerprintDigest: DigestSchema,
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
      "PREDICATE_PASS",
      "PREDICATE_FAIL",
      "INCOMPATIBLE_STATE",
      "HARNESS_ERROR",
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

export type TurnInvestigationRunFact = z.infer<typeof TurnInvestigationRunFactSchema>;

export const StableTurnStateSchema = z.object({
  stateIndex: z.number().int().nonnegative(),
  turnId: z.string().min(1),
  turnOrdinal: z.number().int().nonnegative(),
  role: z.enum(["SESSION_BASELINE", "TURN"]),
  treeDigest: GitObjectIdSchema,
  snapshotDigest: DigestSchema,
  verdict: z.enum(["PASS", "FAIL"]),
  executionIds: z.array(DigestSchema).length(STABLE_EXECUTION_COUNT),
  runIds: z.array(DigestSchema).length(STABLE_EXECUTION_COUNT)
}).strict();

export type StableTurnState = z.infer<typeof StableTurnStateSchema>;

export const StableTurnTransitionSchema = z.object({
  kind: z.enum(["PASS_TO_FAIL", "FAIL_TO_PASS"]),
  before: StableTurnStateSchema,
  after: StableTurnStateSchema
}).strict();

export type StableTurnTransition = z.infer<typeof StableTurnTransitionSchema>;

/** @deprecated Prefer StableTurnTransition; kept as a structural alias for call sites. */
export type TurnTransition = StableTurnTransition;

export const TurnIntroductionSchema = z.object({
  status: z.enum(["ATTRIBUTED", "UNATTRIBUTED", "NOT_APPLICABLE"]),
  turnOrdinal: z.number().int().positive().nullable(),
  turnId: z.string().min(1).nullable(),
  reason: z.string().min(1)
}).strict();

export type TurnIntroduction = z.infer<typeof TurnIntroductionSchema>;

const WitnessVerificationSummarySchema = z.object({
  valid: z.boolean(),
  errors: z.array(z.string()),
  frozenDigest: DigestSchema.nullable(),
  witnessDigest: DigestSchema.nullable(),
  externalDigestStatus: z.enum(["NOT_PROVIDED", "MATCH", "MISMATCH"]),
  approval: z.object({ actor: z.string(), approvedAt: TimestampSchema }).nullable()
}).strict();

export const TurnInvestigationResultSchema = z.object({
  schemaVersion: z.literal(TURN_INVESTIGATION_SCHEMA_VERSION),
  recorder: z.literal("codex-turn-tree-replay"),
  nativeCodexInterception: z.literal(false),
  status: z.enum([
    "COMPLETED",
    "INVALID_WITNESS",
    "NO_TURN_SNAPSHOTS",
    "EXECUTION_ERROR",
    "ENVIRONMENT_CHANGED",
    "DUPLICATE_TURN_SNAPSHOTS",
    "SANDBOX_UNAVAILABLE"
  ]),
  repository: z.string().nullable(),
  ledgerDigest: DigestSchema.nullable(),
  witness: WitnessVerificationSummarySchema.nullable(),
  executionsPerState: z.literal(STABLE_EXECUTION_COUNT),
  states: z.array(TurnStateSchema),
  runs: z.array(TurnInvestigationRunFactSchema),
  stableStates: z.array(StableTurnStateSchema),
  transitions: z.array(StableTurnTransitionSchema),
  environmentFingerprints: z.array(EnvironmentFingerprintSchema),
  environmentHomogeneity: z.enum(["HOMOGENEOUS", "HETEROGENEOUS", "EMPTY"]),
  /** Effective fingerprint digest → digest-pinned image used for execution. */
  runtimeMapping: z.record(DigestSchema, DigestPinnedImageSchema),
  introduction: TurnIntroductionSchema,
  nonMonotonic: z.boolean(),
  proof: z.object({
    requiresDockerIsolation: z.literal(true),
    dockerIsolated: z.boolean(),
    executionTrust: z.enum(["NATIVE_DOCKER", "INJECTED_RUNNER", "UNSAFE_LOCAL"]),
    proofTransitions: z.number().int().nonnegative(),
    isProof: z.boolean(),
    reason: z.string().min(1),
    evidenceGrade: z.enum(["TURN_PROOF", "EXPERIMENTAL_TURN", "NONE"]),
    evidenceLabel: z.string().min(1)
  }).strict(),
  errors: z.array(z.string())
}).strict();

export type TurnInvestigationResult = z.infer<typeof TurnInvestigationResultSchema>;

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
  const safe = redactText(preview, `$.turn-run.${stream}`);
  return { value: safe.value, truncated: bytes.length > EVIDENCE_LOG_PREVIEW_BYTES, redacted: safe.report.redacted };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
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

function executionTrustFor(hasInjectedRunner: boolean): "NATIVE_DOCKER" | "INJECTED_RUNNER" {
  return hasInjectedRunner ? "INJECTED_RUNNER" : "NATIVE_DOCKER";
}

function emptyProof(reason: string, executionTrust: "NATIVE_DOCKER" | "INJECTED_RUNNER" = "NATIVE_DOCKER"): TurnInvestigationResult["proof"] {
  return {
    requiresDockerIsolation: true,
    dockerIsolated: executionTrust === "NATIVE_DOCKER",
    executionTrust,
    proofTransitions: 0,
    isProof: false,
    reason,
    evidenceGrade: "NONE",
    evidenceLabel: TURN_EVIDENCE_LABEL_EXPERIMENTAL
  };
}

function emptyIntroduction(reason: string): TurnIntroduction {
  return {
    status: "NOT_APPLICABLE",
    turnOrdinal: null,
    turnId: null,
    reason
  };
}

function baseResult(
  status: TurnInvestigationResult["status"],
  errors: readonly string[],
  fields: Partial<Pick<TurnInvestigationResult, "repository" | "ledgerDigest" | "witness" | "states" | "runs" | "environmentFingerprints" | "environmentHomogeneity" | "runtimeMapping">> & {
    executionTrust?: "NATIVE_DOCKER" | "INJECTED_RUNNER";
    proofReason?: string;
  } = {}
): TurnInvestigationResult {
  const executionTrust = fields.executionTrust ?? "NATIVE_DOCKER";
  return TurnInvestigationResultSchema.parse({
    schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
    recorder: "codex-turn-tree-replay",
    nativeCodexInterception: false,
    status,
    repository: fields.repository ?? null,
    ledgerDigest: fields.ledgerDigest ?? null,
    witness: fields.witness ?? null,
    executionsPerState: STABLE_EXECUTION_COUNT,
    states: fields.states ?? [],
    runs: fields.runs ?? [],
    stableStates: [],
    transitions: [],
    environmentFingerprints: fields.environmentFingerprints ?? [],
    environmentHomogeneity: fields.environmentHomogeneity ?? "EMPTY",
    runtimeMapping: fields.runtimeMapping ?? {},
    introduction: emptyIntroduction(fields.proofReason ?? errors[0] ?? "Turn investigation did not localize a transition."),
    nonMonotonic: false,
    proof: emptyProof(fields.proofReason ?? errors[0] ?? "Turn investigation did not produce proof.", executionTrust),
    errors: [...errors]
  });
}

/**
 * Extract ordered snapshot states from an observed Codex lifecycle ledger.
 * Session baseline (turn zero) precedes completed-turn tree snapshots.
 */
export function turnStatesFromLedger(ledger: CodexLifecycleLedger): TurnState[] {
  const states: TurnState[] = [];
  for (const record of ledger.events) {
    if (record.event.type === "SESSION_BASELINE_SNAPSHOT") {
      const snapshot = TurnTreeSnapshotSchema.parse(record.event.payload.snapshot);
      states.push({
        index: states.length,
        turnId: SESSION_BASELINE_TURN_ID,
        turnOrdinal: 0,
        role: "SESSION_BASELINE",
        treeDigest: snapshot.treeDigest,
        snapshotDigest: snapshot.digest,
        dirty: snapshot.dirty
      });
      continue;
    }
    if (record.event.type !== "TURN_TREE_SNAPSHOT") continue;
    const snapshot = TurnTreeSnapshotSchema.parse(record.event.payload.snapshot);
    states.push({
      index: states.length,
      turnId: record.event.payload.turnId,
      turnOrdinal: record.event.payload.turnOrdinal,
      role: "TURN",
      treeDigest: snapshot.treeDigest,
      snapshotDigest: snapshot.digest,
      dirty: snapshot.dirty
    });
  }
  return states.sort((left, right) => left.turnOrdinal - right.turnOrdinal || left.index - right.index);
}

/** Returns an error when more than one snapshot claims the same turnOrdinal. */
export function duplicateTurnOrdinalError(states: readonly TurnState[]): string | null {
  const ordinals = states.map((state) => state.turnOrdinal);
  return new Set(ordinals).size === ordinals.length
    ? null
    : "Duplicate turnOrdinal in session baseline / TURN_TREE_SNAPSHOT sequence.";
}

/**
 * Attribute "introduced the failure" only when a session baseline proves the
 * repository passed before the first failing turn.
 */
export function attributeFailureIntroduction(
  states: readonly TurnState[],
  transitions: readonly StableTurnTransition[]
): TurnIntroduction {
  const hasBaseline = states.some((state) => state.role === "SESSION_BASELINE" && state.turnOrdinal === 0);
  const firstPassToFail = transitions.find((transition) => transition.kind === "PASS_TO_FAIL");
  if (!firstPassToFail) {
    return {
      status: "NOT_APPLICABLE",
      turnOrdinal: null,
      turnId: null,
      reason: "No PASS→FAIL transition is available to attribute."
    };
  }
  if (!hasBaseline) {
    return {
      status: "UNATTRIBUTED",
      turnOrdinal: null,
      turnId: null,
      reason: "No SESSION_BASELINE_SNAPSHOT; cannot claim a turn introduced the failure."
    };
  }
  if (firstPassToFail.before.role !== "SESSION_BASELINE" || firstPassToFail.before.turnOrdinal !== 0) {
    return {
      status: "UNATTRIBUTED",
      turnOrdinal: null,
      turnId: null,
      reason: "The first PASS→FAIL transition is not anchored at the session baseline."
    };
  }
  return {
    status: "ATTRIBUTED",
    turnOrdinal: firstPassToFail.after.turnOrdinal,
    turnId: firstPassToFail.after.turnId,
    reason: `Failure introduced at turn ${firstPassToFail.after.turnOrdinal}.`
  };
}

function isDecisiveRun(run: TurnInvestigationRunFact): boolean {
  if (run.result.kind !== "DOCKER_ISOLATED") return false;
  if (run.result.verdict === "PASS") {
    return run.result.reason === "PREDICATE_PASS" || run.result.reason === "EXIT_ZERO";
  }
  if (run.result.verdict === "FAIL") {
    return run.result.reason === "PREDICATE_FAIL" || run.result.reason === "EXIT_NONZERO";
  }
  return false;
}

export function reconstructStableTurnStates(
  states: readonly TurnState[],
  runs: readonly TurnInvestigationRunFact[],
  options: { requireNativeExecutor?: boolean } = {}
): StableTurnState[] {
  const requireNative = options.requireNativeExecutor === true;
  const stable: StableTurnState[] = [];
  for (const state of states) {
    const stateRuns = runs
      .filter((run) => run.stateIndex === state.index)
      .sort((left, right) => left.executionAttempt - right.executionAttempt);
    if (stateRuns.length !== STABLE_EXECUTION_COUNT) continue;
    const attempts = new Set(stateRuns.map((run) => run.executionAttempt));
    const executionIds = new Set(stateRuns.map((run) => run.executionId));
    if (attempts.size !== STABLE_EXECUTION_COUNT || executionIds.size !== STABLE_EXECUTION_COUNT) continue;
    const verdict = stateRuns[0]?.result.verdict;
    if (
      (verdict !== "PASS" && verdict !== "FAIL")
      || !stateRuns.every((run) => run.turnId === state.turnId
        && run.turnOrdinal === state.turnOrdinal
        && run.role === state.role
        && run.treeDigest === state.treeDigest
        && run.snapshotDigest === state.snapshotDigest
        && run.result.verdict === verdict
        && isDecisiveRun(run)
        && (!requireNative || run.result.executor === "NATIVE_DOCKER"))
    ) {
      continue;
    }
    stable.push({
      stateIndex: state.index,
      turnId: state.turnId,
      turnOrdinal: state.turnOrdinal,
      role: state.role,
      treeDigest: state.treeDigest,
      snapshotDigest: state.snapshotDigest,
      verdict,
      executionIds: stateRuns.map((run) => run.executionId),
      runIds: stateRuns.map((run) => run.runId)
    });
  }
  return stable;
}

export function findStableTurnTransitions(stable: readonly StableTurnState[]): StableTurnTransition[] {
  const output: StableTurnTransition[] = [];
  for (let index = 1; index < stable.length; index += 1) {
    const before = stable[index - 1];
    const after = stable[index];
    if (!before || !after || after.stateIndex !== before.stateIndex + 1 || before.verdict === after.verdict) continue;
    output.push({
      kind: before.verdict === "PASS" ? "PASS_TO_FAIL" : "FAIL_TO_PASS",
      before,
      after
    });
  }
  return output;
}

function hasNonMonotonicTransitions(transitions: readonly StableTurnTransition[]): boolean {
  return transitions.some((transition) => transition.kind === "PASS_TO_FAIL")
    && transitions.some((transition) => transition.kind === "FAIL_TO_PASS");
}

function runFact(
  state: TurnState,
  executionAttempt: number,
  frozenWitness: FrozenWitness,
  overlays: readonly MaterializedOverlay[],
  sandboxAudit: SandboxPlanAudit,
  result: SandboxExecutionResult,
  startedAt: string,
  finishedAt: string,
  durationMs: number,
  environmentFingerprintDigest: string
): TurnInvestigationRunFact {
  const executionNonce = randomUUID();
  const executionId = digestJson({
    schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
    executionNonce,
    stateIndex: state.index,
    turnId: state.turnId,
    turnOrdinal: state.turnOrdinal,
    treeDigest: state.treeDigest,
    snapshotDigest: state.snapshotDigest,
    environmentFingerprintDigest,
    frozenDigest: frozenWitness.frozenDigest,
    executionAttempt
  });
  const stdoutPreview = redactedEvidencePreview(result.stdout, "stdout");
  const stderrPreview = redactedEvidencePreview(result.stderr, "stderr");
  const unsigned = {
    schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION as typeof TURN_INVESTIGATION_SCHEMA_VERSION,
    executionId,
    executionNonce,
    stateIndex: state.index,
    executionAttempt,
    turnId: state.turnId,
    turnOrdinal: state.turnOrdinal,
    role: state.role,
    treeDigest: state.treeDigest,
    snapshotDigest: state.snapshotDigest,
    environmentFingerprintDigest,
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
  return TurnInvestigationRunFactSchema.parse({ ...unsigned, runId: digestJson(unsigned) });
}

function classifyStatus(runs: readonly TurnInvestigationRunFact[], errors: readonly string[]): TurnInvestigationResult["status"] {
  if (errors.length > 0) return "EXECUTION_ERROR";
  if (runs.some((run) => run.result.reason === "SANDBOX_UNAVAILABLE")) return "SANDBOX_UNAVAILABLE";
  if (runs.some((run) => run.result.verdict === "ERROR")) return "EXECUTION_ERROR";
  return "COMPLETED";
}

/**
 * Replay a frozen witness across Codex turn-tree snapshots (including dirty Stops).
 * Retains per-attempt run facts matching the Git investigation evidence model.
 * Environment fingerprints drive per-state runtime image selection; heterogeneous
 * histories require an explicit runtimeMapping for every distinct fingerprint.
 */
export async function investigateTurnTrees(options: {
  repository: string;
  ledgerPath: string;
  frozenWitness: FrozenWitness;
  expectedFrozenDigest: string;
  /** Homogeneous fallback image; insufficient alone when fingerprints diverge. */
  image: string;
  /**
   * fingerprint digest → digest-pinned image. Required for every distinct
   * fingerprint whenever environmentHomogeneity is HETEROGENEOUS.
   */
  runtimeMapping?: RuntimeMapping;
  runner?: SandboxCommandRunner;
}): Promise<TurnInvestigationResult> {
  const executionTrust = executionTrustFor(options.runner !== undefined);
  const callerMapping: RuntimeMapping = options.runtimeMapping ?? {};
  const repository = resolve(options.repository);
  const verification = verifyFrozenWitnessRecord(options.frozenWitness, options.expectedFrozenDigest);
  const summary = witnessSummary(options.frozenWitness, verification);
  if (!verification.valid || verification.externalDigestStatus !== "MATCH") {
    return baseResult("INVALID_WITNESS", verification.errors, {
      witness: summary,
      executionTrust,
      proofReason: "Frozen witness digest mismatch."
    });
  }

  const ledger = readVerifiedCodexLifecycleLedger(resolve(options.ledgerPath));
  const ledgerDigest = digestJson(ledger);
  const states = turnStatesFromLedger(ledger);
  if (states.length < 2) {
    return baseResult("NO_TURN_SNAPSHOTS", [], {
      repository,
      ledgerDigest,
      witness: summary,
      states,
      executionTrust,
      proofReason: "Need at least two snapshot states (session baseline and/or TURN_TREE_SNAPSHOT events) for turn localization."
    });
  }

  const duplicateError = duplicateTurnOrdinalError(states);
  if (duplicateError) {
    return baseResult("DUPLICATE_TURN_SNAPSHOTS", [duplicateError], {
      repository,
      ledgerDigest,
      witness: summary,
      states,
      executionTrust,
      proofReason: "Multiple snapshot events share a turnOrdinal; refuse ambiguous localization."
    });
  }

  const tempRoot = await mkdtemp(join(tmpdir(), "faultline-turn-investigation-"));
  const runs: TurnInvestigationRunFact[] = [];
  const errors: string[] = [];
  const fingerprints: EnvironmentFingerprint[] = [];
  let effectiveRuntimeMapping: Record<string, string> = {};

  try {
    // Phase 1: materialize each state only to fingerprint the environment.
    for (const state of states) {
      let materialized: Awaited<ReturnType<typeof materializeGitTree>> | null = null;
      try {
        materialized = await materializeGitTree({
          repository,
          commit: state.treeDigest,
          tempRoot,
          name: `fp-${String(state.turnOrdinal).padStart(4, "0")}-${state.treeDigest.slice(0, 12)}`
        });
        fingerprints.push(computeEnvironmentFingerprint(materialized.worktree));
      } catch (error) {
        errors.push(errorMessage(error));
        // Keep fingerprint list aligned with states when possible; a failed
        // materialization contributes no fingerprint and blocks later proof.
      } finally {
        if (materialized) {
          const cleanupError = await materialized.cleanup();
          if (cleanupError) errors.push(cleanupError);
        }
      }
    }

    if (fingerprints.length !== states.length) {
      return baseResult("EXECUTION_ERROR", errors.length > 0 ? errors : ["Could not fingerprint every turn state."], {
        repository,
        ledgerDigest,
        witness: summary,
        states,
        environmentFingerprints: fingerprints,
        environmentHomogeneity: environmentHomogeneity(fingerprints),
        runtimeMapping: {},
        executionTrust,
        proofReason: "Could not compute an environment fingerprint for every turn state."
      });
    }

    const homogeneity = environmentHomogeneity(fingerprints);
    if (homogeneity === "HETEROGENEOUS") {
      const missing = missingRuntimeMappingDigests(fingerprints, callerMapping);
      if (missing.length > 0) {
        return baseResult("ENVIRONMENT_CHANGED", [
          ...errors,
          ENVIRONMENT_CHANGED_PROOF_MESSAGE
        ], {
          repository,
          ledgerDigest,
          witness: summary,
          states,
          environmentFingerprints: fingerprints,
          environmentHomogeneity: homogeneity,
          runtimeMapping: {},
          executionTrust,
          proofReason: ENVIRONMENT_CHANGED_PROOF_MESSAGE
        });
      }
    }

    try {
      effectiveRuntimeMapping = buildEffectiveRuntimeMapping(
        fingerprints,
        callerMapping,
        options.image,
        homogeneity
      );
    } catch (error) {
      const message = errorMessage(error);
      const status = message === ENVIRONMENT_CHANGED_PROOF_MESSAGE ? "ENVIRONMENT_CHANGED" as const : "EXECUTION_ERROR" as const;
      return baseResult(status, [...errors, message], {
        repository,
        ledgerDigest,
        witness: summary,
        states,
        environmentFingerprints: fingerprints,
        environmentHomogeneity: homogeneity,
        runtimeMapping: {},
        executionTrust,
        proofReason: message
      });
    }

    // Phase 2: rematerialize and execute with the fingerprint-selected image.
    for (let stateIndex = 0; stateIndex < states.length; stateIndex += 1) {
      const state = states[stateIndex];
      const fingerprint = fingerprints[stateIndex];
      if (!state || !fingerprint) {
        errors.push(`Missing prepared state or fingerprint at index ${stateIndex}`);
        continue;
      }
      const image = effectiveRuntimeMapping[fingerprint.digest];
      if (!image) {
        errors.push(`No resolved runtime image for fingerprint ${fingerprint.digest}`);
        continue;
      }
      let materialized: Awaited<ReturnType<typeof materializeGitTree>> | null = null;
      try {
        materialized = await materializeGitTree({
          repository,
          commit: state.treeDigest,
          tempRoot,
          name: `run-${String(state.turnOrdinal).padStart(4, "0")}-${state.treeDigest.slice(0, 12)}`
        });
        const overlays = await materializeFrozenOverlays(materialized.worktree, options.frozenWitness);
        const plan = createSandboxPlan({
          witness: { digest: options.frozenWitness.frozenDigest, command: options.frozenWitness.proposal.witness.command },
          sourceDirectory: materialized.worktree,
          mode: "DOCKER_ISOLATED",
          image
        });
        const audit = auditSandboxPlan(plan);
        for (let attempt = 1; attempt <= STABLE_EXECUTION_COUNT; attempt += 1) {
          const startedEpoch = Date.now();
          const startedAt = new Date(startedEpoch).toISOString();
          const execution = await executeSandboxPlan(plan, options.runner);
          const finishedEpoch = Date.now();
          runs.push(runFact(
            state,
            attempt,
            options.frozenWitness,
            overlays,
            audit,
            execution,
            startedAt,
            new Date(finishedEpoch).toISOString(),
            Math.max(0, finishedEpoch - startedEpoch),
            fingerprint.digest
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
    }
  } finally {
    await rm(tempRoot, { recursive: true, force: true });
  }

  const homogeneity = environmentHomogeneity(fingerprints);
  const stableStates = reconstructStableTurnStates(states, runs);
  const transitions = findStableTurnTransitions(stableStates);
  const status = classifyStatus(runs, errors);
  const introduction = attributeFailureIntroduction(states, transitions);
  const dockerIsolated = executionTrust === "NATIVE_DOCKER";
  const isProof = dockerIsolated && status === "COMPLETED" && errors.length === 0 && transitions.length > 0;
  const proofReason = executionTrust === "INJECTED_RUNNER"
    ? "An injected runner produced these observations; FaultLine refuses to certify it as native Docker proof."
    : status === "SANDBOX_UNAVAILABLE"
      ? "Docker was unavailable; no execution result is proof."
      : status === "EXECUTION_ERROR"
        ? "An execution or worktree error prevents an investigation-wide proof claim."
        : transitions.length === 0
          ? "No adjacent turn states produced three matching Docker PASS/FAIL executions."
          : "Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states.";
  const evidenceGrade = isProof
    ? "TURN_PROOF"
    : transitions.length > 0
      ? "EXPERIMENTAL_TURN"
      : "NONE";

  return TurnInvestigationResultSchema.parse({
    schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
    recorder: "codex-turn-tree-replay",
    nativeCodexInterception: false,
    status,
    repository,
    ledgerDigest,
    witness: summary,
    executionsPerState: STABLE_EXECUTION_COUNT,
    states,
    runs,
    stableStates,
    transitions,
    environmentFingerprints: fingerprints,
    environmentHomogeneity: homogeneity,
    runtimeMapping: effectiveRuntimeMapping,
    introduction,
    nonMonotonic: hasNonMonotonicTransitions(transitions),
    proof: {
      requiresDockerIsolation: true,
      dockerIsolated,
      executionTrust,
      proofTransitions: transitions.length,
      isProof,
      reason: proofReason,
      evidenceGrade,
      evidenceLabel: isProof
        ? "Turn localization — portable proof bundle eligible"
        : TURN_EVIDENCE_LABEL_EXPERIMENTAL
    },
    errors
  });
}

export function turnInvestigationDigest(result: TurnInvestigationResult): string {
  return digestJson(result);
}
