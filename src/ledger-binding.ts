import { randomUUID } from "node:crypto";
import {
  closeSync,
  existsSync,
  fsyncSync,
  mkdirSync,
  openSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync
} from "node:fs";
import { basename, dirname, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson } from "./canonical.js";
import {
  CodexLifecycleLedgerSchema,
  verifyCodexLifecycleLedger,
  type CodexLifecycleEvent,
  type CodexLifecycleLedger,
  type LedgerVerification
} from "./ledger.js";
import {
  GitCommitStateSchema,
  GitInvestigationResultSchema,
  type GitCommitState,
  type GitInvestigationResult
} from "./git-investigation.js";

/**
 * A LedgerBoundInvestigation is an integrity bridge, not a Codex recorder.
 * It binds states already replayed by the Git investigation to checkpoints
 * supplied by an independently verified lifecycle ledger.  In particular, it
 * does not claim native Codex interception, private event access, or agent
 * provenance beyond the observed ledger supplied by its caller.
 */
export const LEDGER_BOUND_INVESTIGATION_SCHEMA_VERSION = "faultline.ledger-bound-investigation.v1" as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const GIT_OBJECT_ID = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const GitObjectIdSchema = z.string().regex(GIT_OBJECT_ID, "expected a 40- or 64-character lowercase Git object id");

/** A ledger event is named directly so its position remains independently auditable. */
export const LedgerCheckpointReferenceSchema = z.object({
  checkpointEventSequence: z.number().int().positive(),
  checkpointEventId: z.string().min(1),
  checkpointEventHash: DigestSchema,
  checkpointDigest: DigestSchema,
  repositoryRoot: z.string().min(1),
  commit: GitObjectIdSchema,
  tree: GitObjectIdSchema,
  afterTurnOrdinal: z.number().int().nonnegative(),
  /** Null is meaningful: a checkpoint may be taken immediately after session start. */
  turnId: z.string().min(1).nullable(),
  turnCompletedEventSequence: z.number().int().positive().nullable()
}).strict();

/** Exact state-to-checkpoint correspondence retained in the signed bridge. */
export const LedgerStateBindingSchema = LedgerCheckpointReferenceSchema.extend({
  stateIndex: z.number().int().nonnegative()
}).strict();

export const LedgerCheckpointIssueSchema = LedgerCheckpointReferenceSchema.extend({
  reason: z.enum([
    "DUPLICATE_CHECKPOINT",
    "OUT_OF_RANGE_CHECKPOINT",
    "MISMATCHED_CHECKPOINT"
  ])
}).strict();

const LedgerVerificationSummarySchema = z.object({
  valid: z.boolean(),
  errors: z.array(z.string()),
  eventCount: z.number().int().nonnegative(),
  headHash: DigestSchema.nullable()
}).strict();

/**
 * Diagnostic output is intentionally exhaustive.  A caller can show every
 * missing or unusable fact instead of accepting a partial state mapping.
 */
export const LedgerBindingReportSchema = z.object({
  valid: z.boolean(),
  errors: z.array(z.string()),
  ledgerVerification: LedgerVerificationSummarySchema,
  investigationDigest: DigestSchema.nullable(),
  checkpointCount: z.number().int().nonnegative(),
  investigatedStateCount: z.number().int().nonnegative(),
  matchedStateCount: z.number().int().nonnegative(),
  bindings: z.array(LedgerStateBindingSchema),
  missingStates: z.array(GitCommitStateSchema),
  duplicateStates: z.array(GitCommitStateSchema),
  unmatchedCheckpoints: z.array(LedgerCheckpointIssueSchema),
  duplicateCheckpoints: z.array(LedgerCheckpointIssueSchema),
  outOfRangeCheckpoints: z.array(LedgerCheckpointIssueSchema)
}).strict();

const UnsignedLedgerBoundInvestigationSchema = z.object({
  schemaVersion: z.literal(LEDGER_BOUND_INVESTIGATION_SCHEMA_VERSION),
  recorder: z.literal("ledger-bound-git-investigation"),
  nativeCodexInterception: z.literal(false),
  /** The full ledger is retained so later verification can re-check its chain. */
  ledger: CodexLifecycleLedgerSchema,
  ledgerHeadHash: DigestSchema,
  /** The exact Git-replay result whose state list is being bound. */
  investigation: GitInvestigationResultSchema,
  investigationDigest: DigestSchema,
  stateBindings: z.array(LedgerStateBindingSchema).min(1).max(512)
}).strict();

/**
 * This digest makes a stored bridge tamper-evident.  As with other local
 * FaultLine records, an external retention point is needed to detect an
 * attacker able to rewrite both the record and its digest.
 */
export const LedgerBoundInvestigationSchema = UnsignedLedgerBoundInvestigationSchema.extend({
  bindingDigest: DigestSchema
}).strict();

export type LedgerCheckpointReference = z.infer<typeof LedgerCheckpointReferenceSchema>;
export type LedgerStateBinding = z.infer<typeof LedgerStateBindingSchema>;
export type LedgerCheckpointIssue = z.infer<typeof LedgerCheckpointIssueSchema>;
export type LedgerBindingReport = z.infer<typeof LedgerBindingReportSchema>;
export type LedgerBoundInvestigation = z.infer<typeof LedgerBoundInvestigationSchema>;
export type UnsignedLedgerBoundInvestigation = z.infer<typeof UnsignedLedgerBoundInvestigationSchema>;

export interface LedgerBoundInvestigationVerification {
  readonly valid: boolean;
  readonly errors: readonly string[];
  readonly bindingDigest: string | null;
  readonly externalDigestStatus: "NOT_PROVIDED" | "MATCH" | "MISMATCH";
  readonly ledgerBinding: LedgerBindingReport | null;
  readonly investigation?: GitInvestigationResult;
}

export class LedgerBindingError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "LedgerBindingError";
  }
}

type CompletedTurn = {
  readonly turnId: string;
  readonly completedEventSequence: number;
};

function ledgerVerificationSummary(verification: LedgerVerification): z.infer<typeof LedgerVerificationSummarySchema> {
  return {
    valid: verification.valid,
    errors: [...verification.errors],
    eventCount: verification.eventCount,
    headHash: verification.headHash
  };
}

function emptyReport(
  errors: readonly string[],
  ledgerVerification: LedgerVerification,
  investigationDigest: string | null
): LedgerBindingReport {
  return LedgerBindingReportSchema.parse({
    valid: false,
    errors: [...errors],
    ledgerVerification: ledgerVerificationSummary(ledgerVerification),
    investigationDigest,
    checkpointCount: 0,
    investigatedStateCount: 0,
    matchedStateCount: 0,
    bindings: [],
    missingStates: [],
    duplicateStates: [],
    unmatchedCheckpoints: [],
    duplicateCheckpoints: [],
    outOfRangeCheckpoints: []
  });
}

function checkpointReference(event: CodexLifecycleEvent, completedTurns: ReadonlyMap<number, CompletedTurn>): LedgerCheckpointReference | null {
  if (event.event.type !== "WORKTREE_CHECKPOINT") return null;
  const { checkpoint, afterTurnOrdinal } = event.event.payload;
  const completed = afterTurnOrdinal === 0 ? undefined : completedTurns.get(afterTurnOrdinal);
  return LedgerCheckpointReferenceSchema.parse({
    checkpointEventSequence: event.sequence,
    checkpointEventId: event.eventId,
    checkpointEventHash: event.hash,
    checkpointDigest: checkpoint.digest,
    repositoryRoot: checkpoint.repositoryRoot,
    commit: checkpoint.headCommit,
    tree: checkpoint.treeDigest,
    afterTurnOrdinal,
    turnId: completed?.turnId ?? null,
    turnCompletedEventSequence: completed?.completedEventSequence ?? null
  });
}

function checkpointReferences(ledger: CodexLifecycleLedger): LedgerCheckpointReference[] {
  const completedTurns = new Map<number, CompletedTurn>();
  const checkpoints: LedgerCheckpointReference[] = [];
  for (const event of ledger.events) {
    if (event.event.type === "TURN_COMPLETED") {
      completedTurns.set(event.event.payload.turnOrdinal, {
        turnId: event.event.payload.turnId,
        completedEventSequence: event.sequence
      });
    }
    const checkpoint = checkpointReference(event, completedTurns);
    if (checkpoint) checkpoints.push(checkpoint);
  }
  return checkpoints;
}

function stateKey(commit: string, tree: string): string {
  return `${commit}\u0000${tree}`;
}

function pathsEqual(left: string, right: string): boolean {
  const normalizedLeft = resolve(left);
  const normalizedRight = resolve(right);
  return process.platform === "win32"
    ? normalizedLeft.toLocaleLowerCase() === normalizedRight.toLocaleLowerCase()
    : normalizedLeft === normalizedRight;
}

function issue(reference: LedgerCheckpointReference, reason: z.infer<typeof LedgerCheckpointIssueSchema>["reason"]): LedgerCheckpointIssue {
  return LedgerCheckpointIssueSchema.parse({ ...reference, reason });
}

function sameState(left: Pick<GitCommitState, "commit" | "tree">, right: Pick<GitCommitState, "commit" | "tree">): boolean {
  return left.commit === right.commit && left.tree === right.tree;
}

function resultSemanticErrors(investigation: GitInvestigationResult): { errors: string[]; duplicateStates: GitCommitState[] } {
  const errors: string[] = [];
  const duplicateStates: GitCommitState[] = [];
  const seenIndices = new Set<number>();
  const seenFacts = new Set<string>();
  for (let position = 0; position < investigation.states.length; position += 1) {
    const state = investigation.states[position];
    if (!state) continue;
    if (state.index !== position) {
      errors.push(`Investigation state at position ${position} has index ${state.index}; expected ${position}.`);
    }
    if (seenIndices.has(state.index)) errors.push(`Investigation repeats state index ${state.index}.`);
    seenIndices.add(state.index);
    const key = stateKey(state.commit, state.tree);
    if (seenFacts.has(key)) {
      errors.push(`Investigation repeats Git state ${state.commit}/${state.tree}.`);
      duplicateStates.push(state);
    }
    seenFacts.add(key);
  }

  if (investigation.states.length === 0) {
    errors.push("Investigation has no Git states to bind.");
  }
  if (investigation.repository === null) {
    errors.push("Investigation has no resolved repository root to bind.");
  }
  const first = investigation.states[0];
  const last = investigation.states.at(-1);
  if (investigation.resolvedRange === null) {
    errors.push("Investigation has no resolved Git range to bind.");
  } else if (!first || !last
    || !sameState(first, investigation.resolvedRange.ancestor)
    || !sameState(last, investigation.resolvedRange.descendant)) {
    errors.push("Investigation state endpoints do not match its resolved Git range.");
  }
  return { errors, duplicateStates };
}

/**
 * Examine a ledger/result pair without accepting a partial mapping.  A valid
 * report requires every Git investigation state to have exactly one verified,
 * in-order WORKTREE_CHECKPOINT from the same repository.  Checkpoints outside
 * the result, duplicate checkpoints, and commit/tree mismatches are retained
 * as diagnostics rather than silently ignored.
 */
export function bindVerifiedLedgerToGitInvestigation(
  ledgerValue: unknown,
  investigationValue: unknown
): LedgerBindingReport {
  const parsedLedger = CodexLifecycleLedgerSchema.safeParse(ledgerValue);
  const parsedInvestigation = GitInvestigationResultSchema.safeParse(investigationValue);
  const errors: string[] = [];
  const fallbackLedgerVerification: LedgerVerification = {
    valid: false,
    errors: [],
    eventCount: 0,
    headHash: null
  };

  if (!parsedLedger.success) {
    const ledgerErrors = parsedLedger.error.issues.map((entry) => `Invalid lifecycle ledger at ${entry.path.join(".") || "root"}: ${entry.message}`);
    return emptyReport(ledgerErrors, { ...fallbackLedgerVerification, errors: ledgerErrors }, parsedInvestigation.success ? digestJson(parsedInvestigation.data) : null);
  }

  const ledger = parsedLedger.data;
  const ledgerVerification = verifyCodexLifecycleLedger(ledger);
  if (!ledgerVerification.valid) {
    errors.push(...ledgerVerification.errors.map((message) => `Lifecycle ledger is invalid: ${message}`));
  }

  if (!parsedInvestigation.success) {
    errors.push(...parsedInvestigation.error.issues.map((entry) => `Invalid Git investigation at ${entry.path.join(".") || "root"}: ${entry.message}`));
    return emptyReport(errors, ledgerVerification, null);
  }

  const investigation = parsedInvestigation.data;
  const investigationDigest = digestJson(investigation);
  const { errors: semanticErrors, duplicateStates } = resultSemanticErrors(investigation);
  errors.push(...semanticErrors);

  const checkpoints = checkpointReferences(ledger);
  const stateByFact = new Map<string, GitCommitState[]>();
  const stateByCommit = new Map<string, GitCommitState[]>();
  const stateByTree = new Map<string, GitCommitState[]>();
  for (const state of investigation.states) {
    const factKey = stateKey(state.commit, state.tree);
    stateByFact.set(factKey, [...(stateByFact.get(factKey) ?? []), state]);
    stateByCommit.set(state.commit, [...(stateByCommit.get(state.commit) ?? []), state]);
    stateByTree.set(state.tree, [...(stateByTree.get(state.tree) ?? []), state]);
  }

  const checkpointsByFact = new Map<string, LedgerCheckpointReference[]>();
  for (const checkpoint of checkpoints) {
    const key = stateKey(checkpoint.commit, checkpoint.tree);
    checkpointsByFact.set(key, [...(checkpointsByFact.get(key) ?? []), checkpoint]);
  }

  const duplicateCheckpoints: LedgerCheckpointIssue[] = [];
  for (const references of checkpointsByFact.values()) {
    if (references.length < 2) continue;
    for (const duplicate of references.slice(1)) {
      duplicateCheckpoints.push(issue(duplicate, "DUPLICATE_CHECKPOINT"));
      errors.push(`Checkpoint event ${duplicate.checkpointEventSequence} duplicates commit/tree facts already recorded in this ledger.`);
    }
  }

  const unmatchedCheckpoints: LedgerCheckpointIssue[] = [];
  const outOfRangeCheckpoints: LedgerCheckpointIssue[] = [];
  const candidateByStateIndex = new Map<number, LedgerCheckpointReference>();
  const repository = investigation.repository;
  for (const checkpoint of checkpoints) {
    const key = stateKey(checkpoint.commit, checkpoint.tree);
    const matchingStates = stateByFact.get(key) ?? [];
    const matchingCheckpoints = checkpointsByFact.get(key) ?? [];

    if (matchingCheckpoints.length !== 1) continue;

    if (repository === null || !pathsEqual(checkpoint.repositoryRoot, repository)) {
      outOfRangeCheckpoints.push(issue(checkpoint, "OUT_OF_RANGE_CHECKPOINT"));
      errors.push(`Checkpoint event ${checkpoint.checkpointEventSequence} belongs to a repository outside the investigation root.`);
      continue;
    }
    if (matchingStates.length === 0) {
      const hasPartialMatch = (stateByCommit.get(checkpoint.commit)?.length ?? 0) > 0
        || (stateByTree.get(checkpoint.tree)?.length ?? 0) > 0;
      if (hasPartialMatch) {
        unmatchedCheckpoints.push(issue(checkpoint, "MISMATCHED_CHECKPOINT"));
        errors.push(`Checkpoint event ${checkpoint.checkpointEventSequence} has a commit/tree pair that does not match any investigated state.`);
      } else {
        outOfRangeCheckpoints.push(issue(checkpoint, "OUT_OF_RANGE_CHECKPOINT"));
        errors.push(`Checkpoint event ${checkpoint.checkpointEventSequence} is outside the investigated Git range.`);
      }
      continue;
    }
    if (matchingStates.length !== 1) {
      unmatchedCheckpoints.push(issue(checkpoint, "MISMATCHED_CHECKPOINT"));
      errors.push(`Checkpoint event ${checkpoint.checkpointEventSequence} ambiguously matches duplicate investigated Git states.`);
      continue;
    }
    const state = matchingStates[0];
    if (!state) continue;
    if (candidateByStateIndex.has(state.index)) {
      // This should already be covered by duplicate commit/tree facts, but it
      // remains a safety check if a malicious result repeats only an index.
      unmatchedCheckpoints.push(issue(checkpoint, "MISMATCHED_CHECKPOINT"));
      errors.push(`Checkpoint event ${checkpoint.checkpointEventSequence} would bind state index ${state.index} more than once.`);
      continue;
    }
    candidateByStateIndex.set(state.index, checkpoint);
  }

  const missingStates: GitCommitState[] = [];
  const bindings: LedgerStateBinding[] = [];
  for (const state of investigation.states) {
    const checkpoint = candidateByStateIndex.get(state.index);
    if (!checkpoint) {
      missingStates.push(state);
      errors.push(`Investigation state ${state.index} (${state.commit}/${state.tree}) has no unique verified checkpoint.`);
      continue;
    }
    bindings.push(LedgerStateBindingSchema.parse({ ...checkpoint, stateIndex: state.index }));
  }

  for (let index = 1; index < bindings.length; index += 1) {
    const previous = bindings[index - 1];
    const current = bindings[index];
    if (!previous || !current) continue;
    if (current.checkpointEventSequence <= previous.checkpointEventSequence) {
      errors.push(`Checkpoint ordering does not preserve investigation state order between states ${previous.stateIndex} and ${current.stateIndex}.`);
    }
    if (current.afterTurnOrdinal < previous.afterTurnOrdinal) {
      errors.push(`Checkpoint turn ordering regresses between states ${previous.stateIndex} and ${current.stateIndex}.`);
    }
    if (previous.turnCompletedEventSequence !== null && current.turnCompletedEventSequence !== null
      && current.turnCompletedEventSequence < previous.turnCompletedEventSequence) {
      errors.push(`Completed-turn event ordering regresses between states ${previous.stateIndex} and ${current.stateIndex}.`);
    }
  }

  return LedgerBindingReportSchema.parse({
    valid: errors.length === 0,
    errors,
    ledgerVerification: ledgerVerificationSummary(ledgerVerification),
    investigationDigest,
    checkpointCount: checkpoints.length,
    investigatedStateCount: investigation.states.length,
    matchedStateCount: bindings.length,
    bindings,
    missingStates,
    duplicateStates,
    unmatchedCheckpoints,
    duplicateCheckpoints,
    outOfRangeCheckpoints
  });
}

function unsignedBoundInvestigation(value: LedgerBoundInvestigation | UnsignedLedgerBoundInvestigation): UnsignedLedgerBoundInvestigation {
  const { bindingDigest: _bindingDigest, ...unsigned } = value as LedgerBoundInvestigation;
  return UnsignedLedgerBoundInvestigationSchema.parse(unsigned);
}

/** Canonically digest every binding-relevant field except the digest itself. */
export function ledgerBoundInvestigationDigest(value: LedgerBoundInvestigation | UnsignedLedgerBoundInvestigation): string {
  return digestJson(unsignedBoundInvestigation(value));
}

/**
 * Construct a portable binding only after the ledger and every state mapping
 * verify.  There is no partial-success form: callers receive diagnostics via
 * bindVerifiedLedgerToGitInvestigation, or a fully bound record here.
 */
export function createLedgerBoundInvestigation(
  ledger: CodexLifecycleLedger,
  investigation: GitInvestigationResult
): LedgerBoundInvestigation {
  const report = bindVerifiedLedgerToGitInvestigation(ledger, investigation);
  if (!report.valid) {
    throw new LedgerBindingError(`Cannot bind lifecycle ledger to Git investigation: ${report.errors.join("; ")}`);
  }
  const headHash = report.ledgerVerification.headHash;
  const investigationDigest = report.investigationDigest;
  if (!headHash || !investigationDigest) {
    throw new LedgerBindingError("Cannot bind without a verified ledger head and investigation digest.");
  }
  const unsigned = UnsignedLedgerBoundInvestigationSchema.parse({
    schemaVersion: LEDGER_BOUND_INVESTIGATION_SCHEMA_VERSION,
    recorder: "ledger-bound-git-investigation",
    nativeCodexInterception: false,
    ledger,
    ledgerHeadHash: headHash,
    investigation,
    investigationDigest,
    stateBindings: report.bindings
  });
  return LedgerBoundInvestigationSchema.parse({ ...unsigned, bindingDigest: ledgerBoundInvestigationDigest(unsigned) });
}

/**
 * Recompute all nested integrity and correspondence checks from a persisted
 * bridge.  Recomputing its local digest alone is not enough: every state is
 * re-matched to a valid checkpoint sequence on every verification.
 */
export function verifyLedgerBoundInvestigation(
  value: unknown,
  expectedBindingDigest?: string
): LedgerBoundInvestigationVerification {
  const parsed = LedgerBoundInvestigationSchema.safeParse(value);
  if (!parsed.success) {
    return {
      valid: false,
      errors: parsed.error.issues.map((entry) => `Invalid ledger-bound investigation at ${entry.path.join(".") || "root"}: ${entry.message}`),
      bindingDigest: null,
      externalDigestStatus: expectedBindingDigest === undefined ? "NOT_PROVIDED" : "MISMATCH",
      ledgerBinding: null
    };
  }

  const bound = parsed.data;
  const errors: string[] = [];
  const calculatedDigest = ledgerBoundInvestigationDigest(bound);
  if (bound.bindingDigest !== calculatedDigest) {
    errors.push("bindingDigest does not match the canonical ledger-bound investigation contents.");
  }
  const report = bindVerifiedLedgerToGitInvestigation(bound.ledger, bound.investigation);
  if (!report.valid) {
    errors.push(...report.errors.map((message) => `Ledger/Git binding is invalid: ${message}`));
  }
  if (report.ledgerVerification.headHash === null || bound.ledgerHeadHash !== report.ledgerVerification.headHash) {
    errors.push("ledgerHeadHash does not match the verified lifecycle ledger head.");
  }
  const calculatedInvestigationDigest = digestJson(bound.investigation);
  if (bound.investigationDigest !== calculatedInvestigationDigest) {
    errors.push("investigationDigest does not match the canonical Git investigation result.");
  }
  if (canonicalJson(bound.stateBindings) !== canonicalJson(report.bindings)) {
    errors.push("stateBindings do not exactly match the verified ledger checkpoint mapping.");
  }
  let externalDigestStatus: LedgerBoundInvestigationVerification["externalDigestStatus"] = "NOT_PROVIDED";
  if (expectedBindingDigest !== undefined) {
    const expected = DigestSchema.safeParse(expectedBindingDigest);
    if (!expected.success) {
      errors.push("externally supplied binding digest is not a sha256 digest.");
      externalDigestStatus = "MISMATCH";
    } else if (expected.data !== calculatedDigest) {
      errors.push("externally supplied binding digest does not match.");
      externalDigestStatus = "MISMATCH";
    } else {
      externalDigestStatus = "MATCH";
    }
  }
  return {
    valid: errors.length === 0,
    errors,
    bindingDigest: calculatedDigest,
    externalDigestStatus,
    ledgerBinding: report,
    investigation: bound.investigation
  };
}

function atomicWrite(filePath: string, body: string): string {
  const destination = resolve(filePath);
  const directory = dirname(destination);
  mkdirSync(directory, { recursive: true });
  const staged = resolve(directory, `.${basename(destination)}.${process.pid}.${randomUUID()}.tmp`);
  let descriptor: number | undefined;
  try {
    descriptor = openSync(staged, "wx", 0o600);
    writeFileSync(descriptor, body, "utf8");
    fsyncSync(descriptor);
    closeSync(descriptor);
    descriptor = undefined;
    renameSync(staged, destination);
    try {
      const directoryDescriptor = openSync(directory, "r");
      try {
        fsyncSync(directoryDescriptor);
      } finally {
        closeSync(directoryDescriptor);
      }
    } catch {
      // Not every Windows filesystem permits fsync on a directory. The staged
      // file itself has already been synchronized before the rename.
    }
    return destination;
  } finally {
    if (descriptor !== undefined) closeSync(descriptor);
    if (existsSync(staged)) rmSync(staged, { force: true });
  }
}

/**
 * Persist only a fully valid record. Canonical JSON keeps a retained digest
 * portable across platforms, while the temp-file rename avoids a torn record.
 */
export function writeLedgerBoundInvestigationAtomic(filePath: string, value: LedgerBoundInvestigation): string {
  const verification = verifyLedgerBoundInvestigation(value);
  if (!verification.valid) {
    throw new LedgerBindingError(`Refusing to write an invalid ledger-bound investigation: ${verification.errors.join("; ")}`);
  }
  const parsed = LedgerBoundInvestigationSchema.parse(value);
  return atomicWrite(filePath, `${canonicalJson(parsed)}\n`);
}

/** Safe file-level verification for a CLI or CI job. */
export function verifyLedgerBoundInvestigationFile(
  filePath: string,
  expectedBindingDigest?: string
): LedgerBoundInvestigationVerification {
  try {
    const parsed = JSON.parse(readFileSync(resolve(filePath), "utf8")) as unknown;
    return verifyLedgerBoundInvestigation(parsed, expectedBindingDigest);
  } catch (error) {
    return {
      valid: false,
      errors: [`Unable to read ledger-bound investigation: ${error instanceof Error ? error.message : String(error)}`],
      bindingDigest: null,
      externalDigestStatus: expectedBindingDigest === undefined ? "NOT_PROVIDED" : "MISMATCH",
      ledgerBinding: null
    };
  }
}

/** Read and reject a record unless both its internal binding and optional external receipt agree. */
export function readVerifiedLedgerBoundInvestigation(
  filePath: string,
  expectedBindingDigest?: string
): LedgerBoundInvestigation {
  const verification = verifyLedgerBoundInvestigationFile(filePath, expectedBindingDigest);
  if (!verification.valid) {
    throw new LedgerBindingError(`Ledger-bound investigation verification failed: ${verification.errors.join("; ")}`);
  }
  try {
    return LedgerBoundInvestigationSchema.parse(JSON.parse(readFileSync(resolve(filePath), "utf8")) as unknown);
  } catch (error) {
    throw new LedgerBindingError(`Unable to read verified ledger-bound investigation: ${error instanceof Error ? error.message : String(error)}`);
  }
}
