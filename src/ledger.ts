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
  unlinkSync,
  writeFileSync
} from "node:fs";
import { basename, dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { z } from "zod";
import { digestJson } from "./canonical.js";
import { TurnTreeSnapshotSchema, verifyTurnTreeSnapshot } from "./turn-snapshot.js";

/**
 * A small, explicit transport boundary for records obtained from Codex.  The
 * ledger does not claim that it can intercept private Codex internals: callers
 * supply lifecycle facts they have observed, and FaultLine makes their order
 * and integrity independently checkable.
 */
export const CODEX_LIFECYCLE_LEDGER_VERSION = "faultline.codex-lifecycle-ledger.v1" as const;
export const GIT_CHECKPOINT_VERSION = "faultline.git-checkpoint.v1" as const;

const IdentifierSchema = z
  .string()
  .min(1)
  .max(160)
  .regex(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/, "Identifier contains unsupported characters");
const HashSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/, "Expected a sha256 digest");
const GitObjectIdSchema = z.string().regex(/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/, "Expected a Git object id");

/** ISO-8601 in its canonical UTC form prevents semantically-equal timestamps
 * from hashing differently. */
export const CanonicalTimestampSchema = z.string().refine(
  (value) => {
    const parsed = new Date(value);
    return Number.isFinite(parsed.getTime()) && parsed.toISOString() === value;
  },
  "Expected a canonical ISO-8601 UTC timestamp"
);

export const CodexTransportSchema = z.enum(["CODEX_CLI", "CODEX_APP", "SIDE_CAR"]);
export const TurnOutcomeSchema = z.enum(["COMPLETED", "FAILED", "INTERRUPTED"]);
export const SessionEndReasonSchema = z.enum(["COMPLETED", "FAILED", "INTERRUPTED", "ABANDONED"]);

const StrictObject = <T extends z.ZodRawShape>(shape: T) => z.object(shape).strict();

export const SessionStartedPayloadSchema = StrictObject({
  transport: CodexTransportSchema,
  workingDirectory: z.string().min(1),
  codexThreadId: IdentifierSchema.optional(),
  model: z.string().min(1).max(200).optional(),
  /** Human-readable attribution for the observed session, not proof of authorship. */
  actor: z.string().min(1).max(200).optional()
});

export const TurnStartedPayloadSchema = StrictObject({
  turnId: IdentifierSchema,
  turnOrdinal: z.number().int().positive(),
  /** Digest only: prompts may contain credentials or proprietary code. */
  promptDigest: HashSchema
});

export const TurnCompletedPayloadSchema = StrictObject({
  turnId: IdentifierSchema,
  turnOrdinal: z.number().int().positive(),
  outcome: TurnOutcomeSchema,
  /** Optional because an interrupted turn may not produce a final result. */
  outputDigest: HashSchema.optional(),
  /** Optional reviewer-supplied contribution/session attribution; not turn-level blame. */
  contribution: z.string().min(1).max(240).optional()
});

export const UnsignedGitCheckpointSchema = StrictObject({
  schemaVersion: z.literal(GIT_CHECKPOINT_VERSION),
  repositoryRoot: z.string().min(1),
  headCommit: GitObjectIdSchema,
  treeDigest: GitObjectIdSchema,
  capturedAt: CanonicalTimestampSchema,
  clean: z.literal(true)
});

export const GitCheckpointSchema = UnsignedGitCheckpointSchema.extend({
  digest: HashSchema
}).strict();

export const WorktreeCheckpointPayloadSchema = StrictObject({
  checkpoint: GitCheckpointSchema,
  /** The checkpoint must describe the fully completed turn immediately before it. */
  afterTurnOrdinal: z.number().int().nonnegative()
});

/**
 * A dirty-worktree-safe companion to WORKTREE_CHECKPOINT. It never requires a
 * clean tree, so it can be recorded for every completed turn, letting
 * FaultLine localize a regression to its turn even without a human commit.
 */
export const TurnTreeSnapshotPayloadSchema = StrictObject({
  turnId: IdentifierSchema,
  turnOrdinal: z.number().int().positive(),
  snapshot: TurnTreeSnapshotSchema
});

export const SessionEndedPayloadSchema = StrictObject({
  reason: SessionEndReasonSchema,
  completedTurns: z.number().int().nonnegative()
});

export const LifecycleEventInputSchema = z.discriminatedUnion("type", [
  StrictObject({ type: z.literal("SESSION_STARTED"), payload: SessionStartedPayloadSchema }),
  StrictObject({ type: z.literal("TURN_STARTED"), payload: TurnStartedPayloadSchema }),
  StrictObject({ type: z.literal("TURN_COMPLETED"), payload: TurnCompletedPayloadSchema }),
  StrictObject({ type: z.literal("WORKTREE_CHECKPOINT"), payload: WorktreeCheckpointPayloadSchema }),
  StrictObject({ type: z.literal("TURN_TREE_SNAPSHOT"), payload: TurnTreeSnapshotPayloadSchema }),
  StrictObject({ type: z.literal("SESSION_ENDED"), payload: SessionEndedPayloadSchema })
]);

export const UnsignedCodexLifecycleEventSchema = StrictObject({
  schemaVersion: z.literal(CODEX_LIFECYCLE_LEDGER_VERSION),
  ledgerId: IdentifierSchema,
  sessionId: IdentifierSchema,
  sequence: z.number().int().positive(),
  eventId: IdentifierSchema,
  occurredAt: CanonicalTimestampSchema,
  event: LifecycleEventInputSchema,
  previousHash: HashSchema
});

export const CodexLifecycleEventSchema = UnsignedCodexLifecycleEventSchema.extend({
  hash: HashSchema
}).strict();

export const CodexLifecycleLedgerSchema = StrictObject({
  schemaVersion: z.literal(CODEX_LIFECYCLE_LEDGER_VERSION),
  ledgerId: IdentifierSchema,
  sessionId: IdentifierSchema,
  createdAt: CanonicalTimestampSchema,
  events: z.array(CodexLifecycleEventSchema)
});

export type CodexTransport = z.infer<typeof CodexTransportSchema>;
export type TurnOutcome = z.infer<typeof TurnOutcomeSchema>;
export type SessionEndReason = z.infer<typeof SessionEndReasonSchema>;
export type GitCheckpoint = z.infer<typeof GitCheckpointSchema>;
export type UnsignedGitCheckpoint = z.infer<typeof UnsignedGitCheckpointSchema>;
export type TurnTreeSnapshotPayload = z.infer<typeof TurnTreeSnapshotPayloadSchema>;
export type LifecycleEventInput = z.infer<typeof LifecycleEventInputSchema>;
export type UnsignedCodexLifecycleEvent = z.infer<typeof UnsignedCodexLifecycleEventSchema>;
export type CodexLifecycleEvent = z.infer<typeof CodexLifecycleEventSchema>;
export type CodexLifecycleLedger = z.infer<typeof CodexLifecycleLedgerSchema>;

export type CreateLedgerOptions = {
  sessionId: string;
  ledgerId?: string;
  createdAt?: string;
};

export type AppendLifecycleEventOptions = {
  eventId?: string;
  occurredAt?: string;
};

export type GitCommandRunner = (repository: string, args: readonly string[]) => string;

export type CaptureGitCheckpointOptions = {
  now?: () => Date;
  runGit?: GitCommandRunner;
};

export type LedgerVerification = {
  valid: boolean;
  errors: string[];
  eventCount: number;
  headHash: string | null;
};

export class LedgerIntegrityError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "LedgerIntegrityError";
  }
}

/** Returns the immutable anchor that binds the first event to the ledger header. */
export function ledgerGenesisHash(ledger: Pick<CodexLifecycleLedger, "schemaVersion" | "ledgerId" | "sessionId" | "createdAt">): string {
  return digestJson({
    kind: "FAULTLINE_CODEX_LIFECYCLE_GENESIS",
    schemaVersion: ledger.schemaVersion,
    ledgerId: ledger.ledgerId,
    sessionId: ledger.sessionId,
    createdAt: ledger.createdAt
  });
}

/** Hashes exactly the unsigned envelope. The event payload and predecessor hash are both covered. */
export function hashLifecycleEvent(event: UnsignedCodexLifecycleEvent): string {
  return digestJson(UnsignedCodexLifecycleEventSchema.parse(event));
}

export function signGitCheckpoint(checkpoint: UnsignedGitCheckpoint): GitCheckpoint {
  const unsigned = UnsignedGitCheckpointSchema.parse(checkpoint);
  return GitCheckpointSchema.parse({ ...unsigned, digest: digestJson(unsigned) });
}

export function verifyGitCheckpoint(value: unknown): string[] {
  const parsed = GitCheckpointSchema.safeParse(value);
  if (!parsed.success) {
    return parsed.error.issues.map((issue) => `Invalid Git checkpoint at ${issue.path.join(".") || "root"}: ${issue.message}`);
  }
  const { digest, ...unsigned } = parsed.data;
  return digest === digestJson(unsigned) ? [] : ["Git checkpoint digest does not match its contents"];
}

function defaultGitRunner(repository: string, args: readonly string[]): string {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.error || result.status !== 0) {
    const detail = `${result.stderr ?? ""}${result.error ? result.error.message : ""}`.trim();
    throw new Error(`Git checkpoint capture failed for ${args.join(" ")}: ${detail || `exit ${result.status ?? "unknown"}`}`);
  }
  return String(result.stdout ?? "").trim();
}

/**
 * Captures a factual, clean Git worktree checkpoint.  The runner can be
 * injected for integration adapters, but the default path invokes Git itself.
 */
export function captureGitCleanCheckpoint(repository: string, options: CaptureGitCheckpointOptions = {}): GitCheckpoint {
  const requestedRoot = resolve(repository);
  const runGit = options.runGit ?? defaultGitRunner;
  const repositoryRoot = resolve(runGit(requestedRoot, ["rev-parse", "--show-toplevel"]));
  const status = runGit(repositoryRoot, ["status", "--porcelain=v1", "--untracked-files=all"]);
  if (status) {
    throw new LedgerIntegrityError("Git checkpoint capture requires a clean worktree.");
  }
  const headCommit = runGit(repositoryRoot, ["rev-parse", "HEAD"]);
  const treeDigest = runGit(repositoryRoot, ["rev-parse", "HEAD^{tree}"]);
  const capturedAt = (options.now ?? (() => new Date()))().toISOString();
  return signGitCheckpoint({
    schemaVersion: GIT_CHECKPOINT_VERSION,
    repositoryRoot,
    headCommit,
    treeDigest,
    capturedAt,
    clean: true
  });
}

export function createCodexLifecycleLedger(options: CreateLedgerOptions): CodexLifecycleLedger {
  return CodexLifecycleLedgerSchema.parse({
    schemaVersion: CODEX_LIFECYCLE_LEDGER_VERSION,
    ledgerId: options.ledgerId ?? `ledger-${randomUUID()}`,
    sessionId: options.sessionId,
    createdAt: options.createdAt ?? new Date().toISOString(),
    events: []
  });
}

function errorSummary(errors: readonly string[]): string {
  return errors.length === 1 ? errors[0] ?? "invalid ledger" : errors.join("; ");
}

function ensureAppendableLedger(ledger: CodexLifecycleLedger): CodexLifecycleLedger {
  const parsed = CodexLifecycleLedgerSchema.parse(ledger);
  const verification = verifyCodexLifecycleLedger(parsed);
  if (!verification.valid) {
    throw new LedgerIntegrityError(`Refusing to append to an invalid ledger: ${errorSummary(verification.errors)}`);
  }
  return parsed;
}

/**
 * Appends one signed event by returning a new ledger value.  It never mutates
 * the caller's array, and refuses to extend an invalid or already-ended log.
 */
export function appendLifecycleEvent(
  ledger: CodexLifecycleLedger,
  input: LifecycleEventInput,
  options: AppendLifecycleEventOptions = {}
): CodexLifecycleLedger {
  const current = ensureAppendableLedger(ledger);
  const event = LifecycleEventInputSchema.parse(input);
  const occurredAt = options.occurredAt ?? new Date().toISOString();
  CanonicalTimestampSchema.parse(occurredAt);
  const previous = current.events.at(-1);
  const unsigned: UnsignedCodexLifecycleEvent = {
    schemaVersion: CODEX_LIFECYCLE_LEDGER_VERSION,
    ledgerId: current.ledgerId,
    sessionId: current.sessionId,
    sequence: current.events.length + 1,
    eventId: options.eventId ?? `event-${randomUUID()}`,
    occurredAt,
    event,
    previousHash: previous?.hash ?? ledgerGenesisHash(current)
  };
  const signed: CodexLifecycleEvent = { ...unsigned, hash: hashLifecycleEvent(unsigned) };
  const next = CodexLifecycleLedgerSchema.parse({ ...current, events: [...current.events, signed] });
  const verification = verifyCodexLifecycleLedger(next);
  if (!verification.valid) {
    throw new LedgerIntegrityError(`Refusing an invalid lifecycle transition: ${errorSummary(verification.errors)}`);
  }
  return next;
}

type LifecycleState = {
  started: boolean;
  ended: boolean;
  activeTurn: { id: string; ordinal: number } | null;
  completedTurnOrdinal: number;
  seenTurnIds: Set<string>;
  seenEventIds: Set<string>;
};

function validateLifecycleState(event: CodexLifecycleEvent, state: LifecycleState, index: number, errors: string[]): void {
  const prefix = `Event ${event.sequence} (${event.event.type})`;
  if (state.seenEventIds.has(event.eventId)) errors.push(`${prefix} reuses event id ${event.eventId}`);
  state.seenEventIds.add(event.eventId);
  if (state.ended) {
    errors.push(`${prefix} occurs after SESSION_ENDED`);
    return;
  }
  switch (event.event.type) {
    case "SESSION_STARTED":
      if (state.started) errors.push(`${prefix} starts a session that has already started`);
      if (index !== 0) errors.push(`${prefix} must be the first event`);
      state.started = true;
      return;
    case "TURN_STARTED": {
      if (!state.started) {
        errors.push(`${prefix} occurs before SESSION_STARTED`);
        return;
      }
      if (state.activeTurn) {
        errors.push(`${prefix} starts turn ${event.event.payload.turnId} while ${state.activeTurn.id} is active`);
        return;
      }
      const { turnId, turnOrdinal } = event.event.payload;
      if (state.seenTurnIds.has(turnId)) errors.push(`${prefix} reuses turn id ${turnId}`);
      const expectedOrdinal = state.completedTurnOrdinal + 1;
      if (turnOrdinal !== expectedOrdinal) errors.push(`${prefix} has turn ordinal ${turnOrdinal}; expected ${expectedOrdinal}`);
      state.seenTurnIds.add(turnId);
      state.activeTurn = { id: turnId, ordinal: turnOrdinal };
      return;
    }
    case "TURN_COMPLETED": {
      if (!state.started) {
        errors.push(`${prefix} occurs before SESSION_STARTED`);
        return;
      }
      if (!state.activeTurn) {
        errors.push(`${prefix} has no matching active turn`);
        return;
      }
      const { turnId, turnOrdinal } = event.event.payload;
      if (turnId !== state.activeTurn.id || turnOrdinal !== state.activeTurn.ordinal) {
        errors.push(`${prefix} does not match active turn ${state.activeTurn.id}/${state.activeTurn.ordinal}`);
        return;
      }
      state.completedTurnOrdinal = turnOrdinal;
      state.activeTurn = null;
      return;
    }
    case "WORKTREE_CHECKPOINT": {
      if (!state.started) {
        errors.push(`${prefix} occurs before SESSION_STARTED`);
        return;
      }
      if (state.activeTurn) {
        errors.push(`${prefix} is not permitted while turn ${state.activeTurn.id} is active`);
        return;
      }
      const { checkpoint, afterTurnOrdinal } = event.event.payload;
      if (afterTurnOrdinal !== state.completedTurnOrdinal) {
        errors.push(`${prefix} claims after turn ${afterTurnOrdinal}; expected ${state.completedTurnOrdinal}`);
      }
      if (Date.parse(checkpoint.capturedAt) > Date.parse(event.occurredAt)) {
        errors.push(`${prefix} is timestamped before its Git checkpoint was captured`);
      }
      for (const checkpointError of verifyGitCheckpoint(checkpoint)) errors.push(`${prefix}: ${checkpointError}`);
      return;
    }
    case "TURN_TREE_SNAPSHOT": {
      if (!state.started) {
        errors.push(`${prefix} occurs before SESSION_STARTED`);
        return;
      }
      if (state.activeTurn) {
        errors.push(`${prefix} is not permitted while turn ${state.activeTurn.id} is active`);
        return;
      }
      const { turnId, turnOrdinal, snapshot } = event.event.payload;
      if (!state.seenTurnIds.has(turnId)) {
        errors.push(`${prefix} references a turn id that was never started: ${turnId}`);
      }
      if (turnOrdinal !== state.completedTurnOrdinal) {
        errors.push(`${prefix} claims turn ordinal ${turnOrdinal}; expected ${state.completedTurnOrdinal}`);
      }
      if (Date.parse(snapshot.capturedAt) > Date.parse(event.occurredAt)) {
        errors.push(`${prefix} is timestamped before its turn tree snapshot was captured`);
      }
      for (const snapshotError of verifyTurnTreeSnapshot(snapshot)) errors.push(`${prefix}: ${snapshotError}`);
      return;
    }
    case "SESSION_ENDED":
      if (!state.started) {
        errors.push(`${prefix} occurs before SESSION_STARTED`);
        return;
      }
      if (state.activeTurn) {
        errors.push(`${prefix} ends while turn ${state.activeTurn.id} is active`);
        return;
      }
      if (event.event.payload.completedTurns !== state.completedTurnOrdinal) {
        errors.push(`${prefix} reports ${event.event.payload.completedTurns} completed turns; expected ${state.completedTurnOrdinal}`);
      }
      state.ended = true;
      return;
  }
}

/**
 * Verifies schema, contiguous sequence, per-session envelope identity, hash
 * chaining, canonical time order, lifecycle state, and embedded checkpoints.
 * It intentionally returns all discovered errors so a reviewer can see why a
 * claimed trace is not admissible.
 */
export function verifyCodexLifecycleLedger(value: unknown): LedgerVerification {
  const parsed = CodexLifecycleLedgerSchema.safeParse(value);
  if (!parsed.success) {
    return {
      valid: false,
      errors: parsed.error.issues.map((issue) => `Invalid ledger at ${issue.path.join(".") || "root"}: ${issue.message}`),
      eventCount: 0,
      headHash: null
    };
  }

  const ledger = parsed.data;
  const errors: string[] = [];
  let expectedPreviousHash = ledgerGenesisHash(ledger);
  let previousTimestamp = ledger.createdAt;
  const state: LifecycleState = {
    started: false,
    ended: false,
    activeTurn: null,
    completedTurnOrdinal: 0,
    seenTurnIds: new Set<string>(),
    seenEventIds: new Set<string>()
  };

  ledger.events.forEach((event, index) => {
    const expectedSequence = index + 1;
    if (event.sequence !== expectedSequence) errors.push(`Event at index ${index} has sequence ${event.sequence}; expected ${expectedSequence}`);
    if (event.ledgerId !== ledger.ledgerId) errors.push(`Event ${event.sequence} has ledger id ${event.ledgerId}; expected ${ledger.ledgerId}`);
    if (event.sessionId !== ledger.sessionId) errors.push(`Event ${event.sequence} has session id ${event.sessionId}; expected ${ledger.sessionId}`);
    if (event.previousHash !== expectedPreviousHash) errors.push(`Event ${event.sequence} previous hash does not match its predecessor`);
    const computedHash = hashLifecycleEvent({
      schemaVersion: event.schemaVersion,
      ledgerId: event.ledgerId,
      sessionId: event.sessionId,
      sequence: event.sequence,
      eventId: event.eventId,
      occurredAt: event.occurredAt,
      event: event.event,
      previousHash: event.previousHash
    });
    if (event.hash !== computedHash) errors.push(`Event ${event.sequence} hash does not match its contents`);
    if (Date.parse(event.occurredAt) < Date.parse(previousTimestamp)) {
      errors.push(`Event ${event.sequence} occurs before the preceding ledger timestamp`);
    }
    validateLifecycleState(event, state, index, errors);
    expectedPreviousHash = event.hash;
    previousTimestamp = event.occurredAt;
  });

  // A recorder may persist between TURN_STARTED and TURN_COMPLETED. That is a
  // valid live prefix, not corruption; later events are still constrained by
  // the active-turn state above.
  return {
    valid: errors.length === 0,
    errors,
    eventCount: ledger.events.length,
    headHash: ledger.events.at(-1)?.hash ?? ledgerGenesisHash(ledger)
  };
}

function atomicWriteFile(filePath: string, content: string): string {
  const absolutePath = resolve(filePath);
  const directory = dirname(absolutePath);
  mkdirSync(directory, { recursive: true });
  const temporaryPath = resolve(directory, `.${basename(absolutePath)}.${process.pid}.${randomUUID()}.tmp`);
  let descriptor: number | undefined;
  try {
    descriptor = openSync(temporaryPath, "wx", 0o600);
    writeFileSync(descriptor, content, "utf8");
    fsyncSync(descriptor);
    closeSync(descriptor);
    descriptor = undefined;
    renameSync(temporaryPath, absolutePath);
    // Directory fsync is unavailable on some Windows filesystems; the file
    // itself is fsynced before rename, so lack of directory support is safe.
    try {
      const directoryDescriptor = openSync(directory, "r");
      try {
        fsyncSync(directoryDescriptor);
      } finally {
        closeSync(directoryDescriptor);
      }
    } catch {
      // Best-effort durability enhancement only.
    }
    return absolutePath;
  } finally {
    if (descriptor !== undefined) closeSync(descriptor);
    if (existsSync(temporaryPath)) rmSync(temporaryPath, { force: true });
  }
}

/** Writes only a schema-valid, integrity-valid ledger via a temp file + rename. */
export function writeCodexLifecycleLedgerAtomic(filePath: string, ledger: CodexLifecycleLedger): string {
  const parsed = ensureAppendableLedger(ledger);
  return atomicWriteFile(filePath, `${JSON.stringify(parsed, null, 2)}\n`);
}

export function readCodexLifecycleLedger(filePath: string): CodexLifecycleLedger {
  const absolutePath = resolve(filePath);
  let parsedJson: unknown;
  try {
    parsedJson = JSON.parse(readFileSync(absolutePath, "utf8"));
  } catch (error) {
    throw new LedgerIntegrityError(`Unable to read lifecycle ledger ${absolutePath}: ${error instanceof Error ? error.message : String(error)}`);
  }
  const parsed = CodexLifecycleLedgerSchema.safeParse(parsedJson);
  if (!parsed.success) {
    throw new LedgerIntegrityError(`Lifecycle ledger ${absolutePath} has invalid schema: ${errorSummary(parsed.error.issues.map((issue) => `${issue.path.join(".") || "root"}: ${issue.message}`))}`);
  }
  return parsed.data;
}

/** Reads a ledger and rejects it unless all cryptographic and lifecycle checks pass. */
export function readVerifiedCodexLifecycleLedger(filePath: string): CodexLifecycleLedger {
  const ledger = readCodexLifecycleLedger(filePath);
  const verification = verifyCodexLifecycleLedger(ledger);
  if (!verification.valid) {
    throw new LedgerIntegrityError(`Lifecycle ledger integrity check failed: ${errorSummary(verification.errors)}`);
  }
  return ledger;
}

/** Safe file-level verification for commands and CI; malformed files return an invalid result. */
export function verifyCodexLifecycleLedgerFile(filePath: string): LedgerVerification {
  try {
    return verifyCodexLifecycleLedger(readCodexLifecycleLedger(filePath));
  } catch (error) {
    return {
      valid: false,
      errors: [error instanceof Error ? error.message : String(error)],
      eventCount: 0,
      headHash: null
    };
  }
}

function acquireLedgerLock(lockPath: string): number {
  try {
    return openSync(lockPath, "wx", 0o600);
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    throw new LedgerIntegrityError(`Could not acquire lifecycle ledger append lock ${lockPath}: ${detail}`);
  }
}

/**
 * Serializes a read-verify-append-write transaction with an exclusive lock so
 * two local recorders cannot allocate the same next sequence number silently.
 */
export function appendLifecycleEventAtomic(
  filePath: string,
  input: LifecycleEventInput,
  options: AppendLifecycleEventOptions = {}
): CodexLifecycleLedger {
  const absolutePath = resolve(filePath);
  const lockPath = `${absolutePath}.lock`;
  mkdirSync(dirname(absolutePath), { recursive: true });
  const lock = acquireLedgerLock(lockPath);
  try {
    const ledger = readVerifiedCodexLifecycleLedger(absolutePath);
    const next = appendLifecycleEvent(ledger, input, options);
    writeCodexLifecycleLedgerAtomic(absolutePath, next);
    return next;
  } finally {
    closeSync(lock);
    try {
      unlinkSync(lockPath);
    } catch {
      // A successful append is still durable if cleanup races with a caller.
    }
  }
}
