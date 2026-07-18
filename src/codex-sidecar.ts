import { spawnSync } from "node:child_process";
import {
  closeSync,
  existsSync,
  lstatSync,
  mkdirSync,
  openSync,
  readdirSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  unlinkSync,
  writeFileSync
} from "node:fs";
import { dirname, join, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, sha256 } from "./canonical.js";
import { DOCTOR_SAFE_GIT_CONFIG } from "./doctor.js";
import {
  appendLifecycleEvent,
  captureGitCleanCheckpoint,
  createCodexLifecycleLedger,
  readVerifiedCodexLifecycleLedger,
  verifyCodexLifecycleLedgerFile,
  writeCodexLifecycleLedgerAtomic,
  LedgerIntegrityError,
  type CodexLifecycleLedger,
  type GitCheckpoint,
  type LifecycleEventInput
} from "./ledger.js";
import { captureTurnTreeSnapshot, TurnSnapshotError, type TurnTreeSnapshot } from "./turn-snapshot.js";

/**
 * This adapter deliberately consumes only the stable, documented Codex hook
 * fields. It is an observer of public lifecycle hooks, not an interception of
 * private model state, transcripts, or reasoning.
 */
const IdentifierSchema = z
  .string()
  .min(1)
  .max(160)
  .regex(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/, "Identifier contains unsupported characters");

const HookBaseSchema = z.object({
  session_id: IdentifierSchema,
  cwd: z.string().min(1),
  model: z.string().min(1).max(200)
});

const SessionStartHookSchema = HookBaseSchema.extend({
  hook_event_name: z.literal("SessionStart")
});

const UserPromptSubmitHookSchema = HookBaseSchema.extend({
  hook_event_name: z.literal("UserPromptSubmit"),
  turn_id: IdentifierSchema,
  // This value is digested in memory and is never written to disk.
  prompt: z.string().max(4 * 1024 * 1024)
});

const StopHookSchema = HookBaseSchema.extend({
  hook_event_name: z.literal("Stop"),
  turn_id: IdentifierSchema
});

export const CodexSidecarHookInputSchema = z.discriminatedUnion("hook_event_name", [
  SessionStartHookSchema,
  UserPromptSubmitHookSchema,
  StopHookSchema
]);

export type CodexSidecarHookInput = z.infer<typeof CodexSidecarHookInputSchema>;

export type CodexSidecarStatus =
  | "SESSION_STARTED"
  | "TURN_STARTED"
  | "CHECKPOINT_RECORDED"
  | "CHECKPOINT_SKIPPED_DIRTY"
  | "CHECKPOINT_SKIPPED_UNAVAILABLE"
  /** A turn tree snapshot was recorded, but no clean-worktree checkpoint decision exists yet for this turn. */
  | "TURN_SNAPSHOT_RECORDED";

export type CodexSidecarResult = {
  readonly status: CodexSidecarStatus;
  readonly ledgerPath: string;
  readonly sessionId: string;
  readonly turnId?: string;
  readonly idempotent: boolean;
  readonly checkpointDigest?: string;
  readonly reason?: "DIRTY_WORKTREE" | "CHECKPOINT_UNAVAILABLE";
  /** Present whenever a dirty-worktree-safe turn tree snapshot was captured for this Stop, regardless of checkpoint outcome. */
  readonly turnSnapshot?: TurnTreeSnapshot;
};

export type CodexSidecarInspection = {
  readonly storageDirectory: string;
  readonly recordings: readonly {
    readonly ledgerPath: string;
    readonly sessionId?: string;
    readonly valid: boolean;
    readonly eventCount: number;
    readonly headHash: string | null;
    readonly lastEvent?: { readonly type: string; readonly occurredAt: string } | undefined;
    readonly latestCheckpoint?: {
      readonly digest: string;
      readonly capturedAt: string;
      readonly headCommit: string;
      readonly treeDigest: string;
      readonly afterTurnOrdinal: number;
    } | undefined;
    readonly latestStop?: {
      readonly turnId: string;
      readonly status: CodexSidecarStatus | "CHECKPOINT_PENDING_RECOVERY";
      readonly reason?: "DIRTY_WORKTREE" | "CHECKPOINT_UNAVAILABLE" | undefined;
      readonly receipt: "DURABLE" | "RECOVERABLE_FROM_LEDGER" | "MISSING";
    } | undefined;
    readonly lock?: {
      readonly state: "ACTIVE" | "STALE" | "INVALID";
      readonly ageMilliseconds?: number | undefined;
    } | undefined;
    readonly errors: readonly string[];
  }[];
};

type StopReceiptStatus = Extract<CodexSidecarStatus, "CHECKPOINT_RECORDED" | "CHECKPOINT_SKIPPED_DIRTY" | "CHECKPOINT_SKIPPED_UNAVAILABLE" | "TURN_SNAPSHOT_RECORDED">;

type StopReceipt = {
  readonly schemaVersion: "faultline.codex-sidecar-stop-receipt.v1";
  readonly sessionId: string;
  readonly turnId: string;
  readonly status: StopReceiptStatus;
  readonly checkpointDigest?: string | undefined;
  readonly reason?: "DIRTY_WORKTREE" | "CHECKPOINT_UNAVAILABLE" | undefined;
  /** Present whenever this Stop also produced a dirty-worktree-safe turn tree snapshot. */
  readonly turnSnapshotDigest?: string | undefined;
};

const StopReceiptSchema = z.object({
  schemaVersion: z.literal("faultline.codex-sidecar-stop-receipt.v1"),
  sessionId: IdentifierSchema,
  turnId: IdentifierSchema,
  status: z.enum(["CHECKPOINT_RECORDED", "CHECKPOINT_SKIPPED_DIRTY", "CHECKPOINT_SKIPPED_UNAVAILABLE", "TURN_SNAPSHOT_RECORDED"]),
  checkpointDigest: z.string().regex(/^sha256:[a-f0-9]{64}$/).optional(),
  reason: z.enum(["DIRTY_WORKTREE", "CHECKPOINT_UNAVAILABLE"]).optional(),
  turnSnapshotDigest: z.string().regex(/^sha256:[a-f0-9]{64}$/).optional()
}).strict();

export class CodexSidecarError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CodexSidecarError";
  }
}

const SIDECAR_LOCK_WAIT_MS = 2_000;
const SIDECAR_LOCK_RETRY_MS = 25;
// Codex gives the configured command 30 seconds. A holder older than this is
// no longer credible as a live hook process, so a later hook may recover it.
const SIDECAR_STALE_LOCK_MS = 45_000;

function filesystemErrorCode(error: unknown): string {
  return error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
}

/**
 * Hooks run in a repository, so Git configuration is executable input. Use
 * the same fixed overrides as `fl doctor`, strip inherited Git environment,
 * and never invoke a shell before asking Git for local facts.
 */
function hardenedGitEnvironment(): NodeJS.ProcessEnv {
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
    ...(process.platform === "win32" && process.env.SystemRoot ? { SystemRoot: process.env.SystemRoot } : {}),
    ...(process.platform === "win32" && process.env.ComSpec ? { ComSpec: process.env.ComSpec } : {}),
    ...(process.platform === "win32" && process.env.PATHEXT ? { PATHEXT: process.env.PATHEXT } : {})
  };
}

function runHardenedGit(repository: string, args: readonly string[]): string {
  const result = spawnSync("git", [...DOCTOR_SAFE_GIT_CONFIG, "-C", repository, ...args], {
    encoding: "utf8",
    shell: false,
    windowsHide: true,
    env: hardenedGitEnvironment()
  });
  if (result.error || result.status !== 0) {
    const detail = `${result.stderr ?? ""}${result.error?.message ?? ""}`.trim();
    throw new CodexSidecarError(`FaultLine could not safely inspect this Git worktree: ${detail || `exit ${result.status ?? "unknown"}`}`);
  }
  return String(result.stdout ?? "").trim();
}

function assertSafeDirectory(path: string, label: string): void {
  let stat: ReturnType<typeof lstatSync>;
  try {
    stat = lstatSync(path);
  } catch (error) {
    throw new CodexSidecarError(`${label} is unavailable: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (stat.isSymbolicLink() || !stat.isDirectory()) {
    throw new CodexSidecarError(`${label} must be a real directory, not a symlink or file.`);
  }
}

function safeDirectoryExists(path: string, label: string): boolean {
  try {
    const stat = lstatSync(path);
    if (stat.isSymbolicLink() || !stat.isDirectory()) {
      throw new CodexSidecarError(`${label} must be a real directory, not a symlink or file.`);
    }
    return true;
  } catch (error) {
    if (filesystemErrorCode(error) === "ENOENT") return false;
    throw error;
  }
}

function ensureSafeChildDirectory(parent: string, name: string, label: string, create: boolean): string {
  assertSafeDirectory(parent, "FaultLine Git metadata directory");
  const child = join(parent, name);
  if (!safeDirectoryExists(child, label)) {
    if (!create) return child;
    try {
      mkdirSync(child, { mode: 0o700 });
    } catch (error) {
      // A second hook/session may initialize the same Git metadata directory
      // concurrently. Accept only a now-verified directory, never a file or
      // symlink substituted during that race.
      if (filesystemErrorCode(error) !== "EEXIST" || !safeDirectoryExists(child, label)) {
        throw new CodexSidecarError(`FaultLine could not create ${label}: ${error instanceof Error ? error.message : String(error)}`);
      }
    }
  }
  assertSafeDirectory(child, label);
  return child;
}

/**
 * Sidecar data lives under the actual Git common directory, never in the
 * worktree. This keeps recorder files out of status by construction and
 * refuses a repository-controlled `.faultline` symlink.
 */
function recordingDirectory(cwd: string, create: boolean): string {
  const repositoryRoot = resolve(runHardenedGit(cwd, ["rev-parse", "--show-toplevel"]));
  const reportedGitDirectory = runHardenedGit(repositoryRoot, ["rev-parse", "--git-common-dir"]);
  if (!reportedGitDirectory) throw new CodexSidecarError("Git did not report a common metadata directory for this worktree.");
  let gitDirectory: string;
  try {
    gitDirectory = realpathSync(resolve(repositoryRoot, reportedGitDirectory));
  } catch (error) {
    throw new CodexSidecarError(`FaultLine could not resolve Git metadata storage: ${error instanceof Error ? error.message : String(error)}`);
  }
  assertSafeDirectory(gitDirectory, "FaultLine Git metadata directory");
  const faultLinePath = join(gitDirectory, "faultline");
  if (!create && !safeDirectoryExists(faultLinePath, "FaultLine sidecar storage")) return join(faultLinePath, "recordings");
  const faultLineDirectory = ensureSafeChildDirectory(gitDirectory, "faultline", "FaultLine sidecar storage", create);
  const recordingsPath = join(faultLineDirectory, "recordings");
  if (!create && !safeDirectoryExists(recordingsPath, "FaultLine sidecar recordings")) return recordingsPath;
  return ensureSafeChildDirectory(faultLineDirectory, "recordings", "FaultLine sidecar recordings", create);
}

function safeRegularFileExists(path: string, label: string): boolean {
  try {
    const stat = lstatSync(path);
    if (stat.isSymbolicLink() || !stat.isFile()) {
      throw new CodexSidecarError(`${label} must be a regular file, not a symlink or directory.`);
    }
    return true;
  } catch (error) {
    if (filesystemErrorCode(error) === "ENOENT") return false;
    throw error;
  }
}

/**
 * File names are opaque so an on-disk path never repeats a potentially
 * sensitive external session identifier. The identifier remains in the
 * signed ledger header for legitimate correlation.
 */
export function codexSidecarLedgerPath(cwd: string, sessionId: string): string {
  IdentifierSchema.parse(sessionId);
  return join(recordingDirectory(cwd, false), `codex-${sha256(sessionId)}.json`);
}

function stopReceiptPath(directory: string, sessionId: string, turnId: string): string {
  const key = sha256(`${sessionId}\u0000${turnId}`);
  return join(directory, `codex-stop-${key}.json`);
}

export function sidecarEventId(sessionId: string, turnId: string | null, phase: "session-start" | "turn-start" | "turn-stop" | "checkpoint" | "turn-snapshot"): string {
  const sessionKey = sha256(sessionId).slice(0, 24);
  const turnKey = turnId === null ? "session" : sha256(turnId).slice(0, 24);
  return `codex-sidecar-${sessionKey}-${turnKey}-${phase}`;
}

function sameJson(left: unknown, right: unknown): boolean {
  return canonicalJson(left) === canonicalJson(right);
}

function eventById(ledger: CodexLifecycleLedger, eventId: string) {
  return ledger.events.find((event) => event.eventId === eventId);
}

function sessionStartedEvent(ledger: CodexLifecycleLedger) {
  const event = ledger.events.find((candidate) => candidate.event.type === "SESSION_STARTED");
  if (!event || event.event.type !== "SESSION_STARTED") {
    throw new CodexSidecarError("The sidecar ledger has no SESSION_STARTED event.");
  }
  return event;
}

function assertExistingSession(ledger: CodexLifecycleLedger, input: CodexSidecarHookInput, cwd: string): void {
  if (ledger.sessionId !== input.session_id) {
    throw new CodexSidecarError("The existing sidecar ledger belongs to a different Codex session.");
  }
  const started = sessionStartedEvent(ledger);
  const expected = {
    transport: "SIDE_CAR" as const,
    workingDirectory: cwd,
    model: input.model
  };
  if (!sameJson(started.event.payload, expected)) {
    throw new CodexSidecarError("The existing sidecar ledger does not match this Codex session's cwd or model.");
  }
}

function createStartedLedger(input: z.infer<typeof SessionStartHookSchema>, cwd: string): CodexLifecycleLedger {
  const ledger = createCodexLifecycleLedger({
    ledgerId: `ledger-codex-sidecar-${sha256(input.session_id).slice(0, 32)}`,
    sessionId: input.session_id
  });
  return appendLifecycleEvent(ledger, {
    type: "SESSION_STARTED",
    payload: { transport: "SIDE_CAR", workingDirectory: cwd, model: input.model }
  }, { eventId: sidecarEventId(input.session_id, null, "session-start") });
}

function appendIfAbsent(
  ledger: CodexLifecycleLedger,
  input: LifecycleEventInput,
  eventId: string
): { ledger: CodexLifecycleLedger; idempotent: boolean } {
  const existing = eventById(ledger, eventId);
  if (existing) {
    if (!sameJson(existing.event, input)) {
      throw new CodexSidecarError(`A sidecar event id is already bound to different lifecycle facts: ${eventId}`);
    }
    return { ledger, idempotent: true };
  }
  return {
    ledger: appendLifecycleEvent(ledger, input, { eventId }),
    idempotent: false
  };
}

function turnOrdinalForStartedTurn(ledger: CodexLifecycleLedger, turnId: string): number {
  const event = ledger.events.find((candidate) => candidate.event.type === "TURN_STARTED" && candidate.event.payload.turnId === turnId);
  if (!event || event.event.type !== "TURN_STARTED") {
    throw new CodexSidecarError("Stop was observed before the matching UserPromptSubmit event.");
  }
  return event.event.payload.turnOrdinal;
}

function turnCompletionEvent(ledger: CodexLifecycleLedger, turnId: string) {
  const event = ledger.events.find((candidate) => candidate.event.type === "TURN_COMPLETED" && candidate.event.payload.turnId === turnId);
  return event && event.event.type === "TURN_COMPLETED" ? event : undefined;
}

function sidecarLockPath(ledgerPath: string): string {
  return `${ledgerPath}.sidecar.lock`;
}

function sidecarLockHealth(ledgerPath: string): { state: "ACTIVE" | "STALE" | "INVALID"; ageMilliseconds?: number } | undefined {
  try {
    const stat = lstatSync(sidecarLockPath(ledgerPath));
    if (stat.isSymbolicLink() || !stat.isFile()) return { state: "INVALID" };
    const ageMilliseconds = Math.max(0, Math.round(Date.now() - stat.mtimeMs));
    return {
      state: ageMilliseconds > SIDECAR_STALE_LOCK_MS ? "STALE" : "ACTIVE",
      ageMilliseconds
    };
  } catch (error) {
    if (filesystemErrorCode(error) === "ENOENT") return undefined;
    throw error;
  }
}

function waitForSidecarLock(milliseconds: number): void {
  const signal = new Int32Array(new SharedArrayBuffer(Int32Array.BYTES_PER_ELEMENT));
  Atomics.wait(signal, 0, 0, milliseconds);
}

function clearStaleSidecarLock(lockPath: string): boolean {
  let stat: ReturnType<typeof lstatSync>;
  try {
    stat = lstatSync(lockPath);
  } catch (error) {
    if (filesystemErrorCode(error) === "ENOENT") return true;
    throw error;
  }
  if (stat.isSymbolicLink() || !stat.isFile()) {
    throw new CodexSidecarError("FaultLine sidecar lock must be a regular file, not a symlink or directory.");
  }
  if (Date.now() - stat.mtimeMs <= SIDECAR_STALE_LOCK_MS) return false;
  try {
    unlinkSync(lockPath);
    return true;
  } catch (error) {
    if (filesystemErrorCode(error) === "ENOENT") return true;
    throw new CodexSidecarError(`FaultLine could not recover a stale sidecar lock: ${error instanceof Error ? error.message : String(error)}`);
  }
}

/** Serializes sidecar lifecycle/receipt transactions with bounded stale-lock recovery. */
function withSidecarLock<T>(ledgerPath: string, action: () => T): T {
  assertSafeDirectory(dirname(ledgerPath), "FaultLine sidecar recordings");
  const lockPath = sidecarLockPath(ledgerPath);
  let descriptor: number | undefined;
  const deadline = Date.now() + SIDECAR_LOCK_WAIT_MS;
  while (descriptor === undefined) {
    try {
      descriptor = openSync(lockPath, "wx", 0o600);
    } catch (error) {
      if (filesystemErrorCode(error) !== "EEXIST") throw error;
      if (clearStaleSidecarLock(lockPath)) continue;
      if (Date.now() >= deadline) {
        throw new CodexSidecarError("Another FaultLine sidecar event is still recording for this Codex session; inspect fl codex sidecar status after the current turn.");
      }
      waitForSidecarLock(Math.min(SIDECAR_LOCK_RETRY_MS, Math.max(1, deadline - Date.now())));
    }
  }
  try {
    return action();
  } finally {
    if (descriptor !== undefined) closeSync(descriptor);
    try {
      unlinkSync(lockPath);
    } catch {
      // A durable ledger update remains valid if lock cleanup is interrupted.
    }
  }
}

function writeJsonAtomic(path: string, value: unknown): void {
  const directory = dirname(path);
  assertSafeDirectory(directory, "FaultLine sidecar recordings");
  safeRegularFileExists(path, "FaultLine sidecar receipt");
  const staged = join(directory, `.${sha256(path).slice(0, 24)}.${process.pid}.tmp`);
  try {
    writeFileSync(staged, `${JSON.stringify(value, null, 2)}\n`, { encoding: "utf8", flag: "wx", mode: 0o600 });
    renameSync(staged, path);
  } finally {
    if (existsSync(staged)) rmSync(staged, { force: true });
  }
}

function readStopReceipt(path: string): StopReceipt | undefined {
  if (!safeRegularFileExists(path, "FaultLine sidecar receipt")) return undefined;
  try {
    return StopReceiptSchema.parse(JSON.parse(readFileSync(path, "utf8")));
  } catch (error) {
    throw new CodexSidecarError(`Unable to read the sidecar stop receipt: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function readSidecarLedger(ledgerPath: string): CodexLifecycleLedger {
  safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger");
  return readVerifiedCodexLifecycleLedger(ledgerPath);
}

function writeSidecarLedger(ledgerPath: string, ledger: CodexLifecycleLedger): void {
  safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger");
  writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);
}

function checkpointResult(
  status: Extract<CodexSidecarStatus, "CHECKPOINT_RECORDED" | "CHECKPOINT_SKIPPED_DIRTY" | "CHECKPOINT_SKIPPED_UNAVAILABLE">,
  ledgerPath: string,
  sessionId: string,
  turnId: string,
  idempotent: boolean,
  checkpoint?: GitCheckpoint,
  turnSnapshot?: TurnTreeSnapshot
): CodexSidecarResult {
  const turnSnapshotField = turnSnapshot === undefined ? {} : { turnSnapshot };
  if (status === "CHECKPOINT_RECORDED") {
    if (!checkpoint) throw new CodexSidecarError("A recorded checkpoint result requires a checkpoint digest.");
    return { status, ledgerPath, sessionId, turnId, idempotent, checkpointDigest: checkpoint.digest, ...turnSnapshotField };
  }
  return {
    status,
    ledgerPath,
    sessionId,
    turnId,
    idempotent,
    reason: status === "CHECKPOINT_SKIPPED_DIRTY" ? "DIRTY_WORKTREE" : "CHECKPOINT_UNAVAILABLE",
    ...turnSnapshotField
  };
}

/** A recovery-only outcome for a completed turn that has a durable turn tree snapshot but no checkpoint decision yet. */
function turnSnapshotOnlyResult(
  ledgerPath: string,
  sessionId: string,
  turnId: string,
  idempotent: boolean,
  turnSnapshot: TurnTreeSnapshot
): CodexSidecarResult {
  return { status: "TURN_SNAPSHOT_RECORDED", ledgerPath, sessionId, turnId, idempotent, turnSnapshot };
}

function resultFromReceipt(receipt: StopReceipt, ledger: CodexLifecycleLedger, ledgerPath: string, input: z.infer<typeof StopHookSchema>): CodexSidecarResult {
  const turnSnapshot = receipt.turnSnapshotDigest === undefined ? undefined : turnSnapshotForTurn(ledger, input);
  const turnSnapshotField = turnSnapshot === undefined ? {} : { turnSnapshot };
  if (receipt.status === "CHECKPOINT_RECORDED") {
    if (!receipt.checkpointDigest) throw new CodexSidecarError("The sidecar stop receipt omitted its checkpoint digest.");
    return {
      status: receipt.status,
      ledgerPath,
      sessionId: receipt.sessionId,
      turnId: receipt.turnId,
      idempotent: true,
      checkpointDigest: receipt.checkpointDigest,
      ...turnSnapshotField
    };
  }
  if (receipt.status === "TURN_SNAPSHOT_RECORDED") {
    if (!turnSnapshot) throw new CodexSidecarError("The sidecar stop receipt claims a turn tree snapshot that is absent from the ledger.");
    return turnSnapshotOnlyResult(ledgerPath, receipt.sessionId, receipt.turnId, true, turnSnapshot);
  }
  return {
    status: receipt.status,
    ledgerPath,
    sessionId: receipt.sessionId,
    turnId: receipt.turnId,
    idempotent: true,
    reason: receipt.reason ?? (receipt.status === "CHECKPOINT_SKIPPED_DIRTY" ? "DIRTY_WORKTREE" : "CHECKPOINT_UNAVAILABLE"),
    ...turnSnapshotField
  };
}

function assertReceiptMatchesLedger(
  receipt: StopReceipt,
  ledger: CodexLifecycleLedger,
  input: Pick<z.infer<typeof StopHookSchema>, "session_id" | "turn_id">
): void {
  const turnOrdinal = turnOrdinalForStartedTurn(ledger, input.turn_id);
  const completion = eventById(ledger, sidecarEventId(input.session_id, input.turn_id, "turn-stop"));
  const expectedCompletion: LifecycleEventInput = {
    type: "TURN_COMPLETED",
    payload: { turnId: input.turn_id, turnOrdinal, outcome: "COMPLETED" }
  };
  if (!completion || !sameJson(completion.event, expectedCompletion)) {
    throw new CodexSidecarError("The sidecar stop receipt has no matching completed-turn ledger event.");
  }
  const snapshotEvent = eventById(ledger, sidecarEventId(input.session_id, input.turn_id, "turn-snapshot"));
  if (receipt.turnSnapshotDigest === undefined) {
    if (snapshotEvent) throw new CodexSidecarError("A sidecar stop receipt without a turn tree snapshot contradicts a recorded one.");
  } else {
    if (!snapshotEvent || snapshotEvent.event.type !== "TURN_TREE_SNAPSHOT" || snapshotEvent.event.payload.turnOrdinal !== turnOrdinal) {
      throw new CodexSidecarError("The sidecar stop receipt has no matching turn tree snapshot ledger event.");
    }
    if (receipt.turnSnapshotDigest !== snapshotEvent.event.payload.snapshot.digest) {
      throw new CodexSidecarError("The sidecar stop receipt turn tree snapshot digest does not match the ledger.");
    }
  }
  const checkpoint = eventById(ledger, sidecarEventId(input.session_id, input.turn_id, "checkpoint"));
  if (receipt.status !== "CHECKPOINT_RECORDED") {
    if (checkpoint) throw new CodexSidecarError("A skipped sidecar stop receipt contradicts a recorded checkpoint.");
    return;
  }
  if (!checkpoint || checkpoint.event.type !== "WORKTREE_CHECKPOINT" || checkpoint.event.payload.afterTurnOrdinal !== turnOrdinal) {
    throw new CodexSidecarError("The sidecar stop receipt has no matching checkpoint ledger event.");
  }
  if (receipt.checkpointDigest !== checkpoint.event.payload.checkpoint.digest) {
    throw new CodexSidecarError("The sidecar stop receipt checkpoint digest does not match the ledger.");
  }
}

function checkpointForTurn(
  ledger: CodexLifecycleLedger,
  input: z.infer<typeof StopHookSchema>
): GitCheckpoint | undefined {
  const checkpoint = eventById(ledger, sidecarEventId(input.session_id, input.turn_id, "checkpoint"));
  if (!checkpoint) return undefined;
  const turnOrdinal = turnOrdinalForStartedTurn(ledger, input.turn_id);
  if (checkpoint.event.type !== "WORKTREE_CHECKPOINT" || checkpoint.event.payload.afterTurnOrdinal !== turnOrdinal) {
    throw new CodexSidecarError("The sidecar checkpoint does not match its completed Codex turn.");
  }
  return checkpoint.event.payload.checkpoint;
}

function turnSnapshotForTurn(
  ledger: CodexLifecycleLedger,
  input: z.infer<typeof StopHookSchema>
): TurnTreeSnapshot | undefined {
  const event = eventById(ledger, sidecarEventId(input.session_id, input.turn_id, "turn-snapshot"));
  if (!event) return undefined;
  const turnOrdinal = turnOrdinalForStartedTurn(ledger, input.turn_id);
  if (event.event.type !== "TURN_TREE_SNAPSHOT" || event.event.payload.turnOrdinal !== turnOrdinal) {
    throw new CodexSidecarError("The sidecar turn tree snapshot does not match its completed Codex turn.");
  }
  return event.event.payload.snapshot;
}

function hasMatchingCompletedTurn(
  ledger: CodexLifecycleLedger,
  input: z.infer<typeof StopHookSchema>
): boolean {
  const completion = turnCompletionEvent(ledger, input.turn_id);
  if (!completion) return false;
  const expected: LifecycleEventInput = {
    type: "TURN_COMPLETED",
    payload: {
      turnId: input.turn_id,
      turnOrdinal: turnOrdinalForStartedTurn(ledger, input.turn_id),
      outcome: "COMPLETED"
    }
  };
  if (!sameJson(completion.event, expected)) {
    throw new CodexSidecarError("The existing completed-turn record does not match this Codex Stop event.");
  }
  return true;
}

function writeReceipt(directory: string, input: z.infer<typeof StopHookSchema>, result: CodexSidecarResult): void {
  const turnSnapshotDigest = result.turnSnapshot?.digest;
  const turnSnapshotField = turnSnapshotDigest === undefined ? {} : { turnSnapshotDigest };
  let receipt: StopReceipt;
  if (result.status === "CHECKPOINT_RECORDED") {
    if (!result.checkpointDigest) throw new CodexSidecarError("A checkpoint receipt requires a checkpoint digest.");
    receipt = {
      schemaVersion: "faultline.codex-sidecar-stop-receipt.v1",
      sessionId: input.session_id,
      turnId: input.turn_id,
      status: result.status,
      checkpointDigest: result.checkpointDigest,
      ...turnSnapshotField
    };
  } else if (result.status === "CHECKPOINT_SKIPPED_DIRTY" || result.status === "CHECKPOINT_SKIPPED_UNAVAILABLE") {
    receipt = {
      schemaVersion: "faultline.codex-sidecar-stop-receipt.v1",
      sessionId: input.session_id,
      turnId: input.turn_id,
      status: result.status,
      reason: result.status === "CHECKPOINT_SKIPPED_DIRTY" ? "DIRTY_WORKTREE" : "CHECKPOINT_UNAVAILABLE",
      ...turnSnapshotField
    };
  } else if (result.status === "TURN_SNAPSHOT_RECORDED") {
    if (!turnSnapshotDigest) throw new CodexSidecarError("A turn snapshot receipt requires a turn tree snapshot digest.");
    receipt = {
      schemaVersion: "faultline.codex-sidecar-stop-receipt.v1",
      sessionId: input.session_id,
      turnId: input.turn_id,
      status: result.status,
      turnSnapshotDigest
    };
  } else {
    throw new CodexSidecarError("Only Stop checkpoint outcomes can be persisted as a sidecar receipt.");
  }
  writeJsonAtomic(stopReceiptPath(directory, input.session_id, input.turn_id), StopReceiptSchema.parse(receipt));
}

function recordSessionStart(input: z.infer<typeof SessionStartHookSchema>, cwd: string, ledgerPath: string): CodexSidecarResult {
  if (!safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger")) {
    const ledger = createStartedLedger(input, cwd);
    writeSidecarLedger(ledgerPath, ledger);
    return { status: "SESSION_STARTED", ledgerPath, sessionId: input.session_id, idempotent: false };
  }
  const ledger = readSidecarLedger(ledgerPath);
  assertExistingSession(ledger, input, cwd);
  const expected = {
    type: "SESSION_STARTED" as const,
    payload: { transport: "SIDE_CAR" as const, workingDirectory: cwd, model: input.model }
  };
  const existing = eventById(ledger, sidecarEventId(input.session_id, null, "session-start"));
  if (!existing || !sameJson(existing.event, expected)) {
    throw new CodexSidecarError("The existing ledger's SESSION_STARTED event is not the expected Codex sidecar observation.");
  }
  return { status: "SESSION_STARTED", ledgerPath, sessionId: input.session_id, idempotent: true };
}

function recordTurnStart(input: z.infer<typeof UserPromptSubmitHookSchema>, cwd: string, ledgerPath: string): CodexSidecarResult {
  if (!safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger")) throw new CodexSidecarError("UserPromptSubmit was observed before SessionStart initialized the sidecar ledger.");
  let ledger = readSidecarLedger(ledgerPath);
  assertExistingSession(ledger, input, cwd);
  const previousActive = ledger.events.find((event) => event.event.type === "TURN_STARTED" && !turnCompletionEvent(ledger, event.event.payload.turnId));
  const promptDigest = `sha256:${sha256(input.prompt)}`;
  const existingStart = eventById(ledger, sidecarEventId(input.session_id, input.turn_id, "turn-start"));
  if (existingStart) {
    const expected = {
      type: "TURN_STARTED" as const,
      payload: { turnId: input.turn_id, turnOrdinal: existingStart.event.type === "TURN_STARTED" ? existingStart.event.payload.turnOrdinal : -1, promptDigest }
    };
    if (!sameJson(existingStart.event, expected)) {
      throw new CodexSidecarError("The existing TURN_STARTED record does not match this Codex hook observation.");
    }
    return { status: "TURN_STARTED", ledgerPath, sessionId: input.session_id, turnId: input.turn_id, idempotent: true };
  }
  if (previousActive) {
    throw new CodexSidecarError("A different Codex turn is still active in this sidecar ledger.");
  }
  const completedTurns = ledger.events.filter((event) => event.event.type === "TURN_COMPLETED").length;
  const appended = appendIfAbsent(ledger, {
    type: "TURN_STARTED",
    payload: { turnId: input.turn_id, turnOrdinal: completedTurns + 1, promptDigest }
  }, sidecarEventId(input.session_id, input.turn_id, "turn-start"));
  ledger = appended.ledger;
  writeSidecarLedger(ledgerPath, ledger);
  return { status: "TURN_STARTED", ledgerPath, sessionId: input.session_id, turnId: input.turn_id, idempotent: appended.idempotent };
}

function isDirtyCheckpointError(error: unknown): boolean {
  return error instanceof LedgerIntegrityError && error.message === "Git checkpoint capture requires a clean worktree.";
}

function recordTurnStop(
  input: z.infer<typeof StopHookSchema>,
  cwd: string,
  ledgerPath: string,
  directory: string
): CodexSidecarResult {
  if (!safeRegularFileExists(ledgerPath, "FaultLine sidecar ledger")) throw new CodexSidecarError("Stop was observed before SessionStart initialized the sidecar ledger.");
  let ledger = readSidecarLedger(ledgerPath);
  assertExistingSession(ledger, input, cwd);
  const receipt = readStopReceipt(stopReceiptPath(directory, input.session_id, input.turn_id));
  if (receipt) {
    if (receipt.sessionId !== input.session_id || receipt.turnId !== input.turn_id) {
      throw new CodexSidecarError("The sidecar stop receipt belongs to different Codex lifecycle facts.");
    }
    assertReceiptMatchesLedger(receipt, ledger, input);
    return resultFromReceipt(receipt, ledger, ledgerPath, input);
  }

  const existingCheckpoint = checkpointForTurn(ledger, input);
  if (existingCheckpoint) {
    // The ledger is the durable proof record. A process can stop after it is
    // written but before its operational receipt; never capture a later state
    // for the same observed Stop event.
    const recovered = checkpointResult("CHECKPOINT_RECORDED", ledgerPath, input.session_id, input.turn_id, true, existingCheckpoint, turnSnapshotForTurn(ledger, input));
    writeReceipt(directory, input, recovered);
    return recovered;
  }
  if (hasMatchingCompletedTurn(ledger, input)) {
    const existingSnapshot = turnSnapshotForTurn(ledger, input);
    if (existingSnapshot) {
      // The turn tree snapshot was durably written but the checkpoint
      // decision was interrupted before its receipt. The snapshot remains
      // valid evidence for this exact turn; recover it without recapturing.
      const recovered = turnSnapshotOnlyResult(ledgerPath, input.session_id, input.turn_id, true, existingSnapshot);
      writeReceipt(directory, input, recovered);
      return recovered;
    }
    // A completed turn without a receipt/checkpoint/snapshot is an
    // interrupted transaction. Preserve an unavailable outcome instead of
    // attaching a later state to this older turn.
    const recovered = checkpointResult("CHECKPOINT_SKIPPED_UNAVAILABLE", ledgerPath, input.session_id, input.turn_id, true);
    writeReceipt(directory, input, recovered);
    return recovered;
  }

  const turnOrdinal = turnOrdinalForStartedTurn(ledger, input.turn_id);
  const completionInput: LifecycleEventInput = {
    type: "TURN_COMPLETED",
    payload: { turnId: input.turn_id, turnOrdinal, outcome: "COMPLETED" }
  };
  const completion = appendIfAbsent(ledger, completionInput, sidecarEventId(input.session_id, input.turn_id, "turn-stop"));
  ledger = completion.ledger;

  // A turn tree snapshot never requires a clean worktree, so it is always
  // attempted first. A capture failure (e.g. a filesystem changing
  // mid-write) must not block the existing clean-checkpoint path below.
  let turnSnapshot: TurnTreeSnapshot | undefined;
  let snapshotIdempotent = true;
  try {
    turnSnapshot = captureTurnTreeSnapshot(cwd);
  } catch (error) {
    if (!(error instanceof TurnSnapshotError)) throw error;
    turnSnapshot = undefined;
  }
  if (turnSnapshot) {
    const snapshotInput: LifecycleEventInput = {
      type: "TURN_TREE_SNAPSHOT",
      payload: { turnId: input.turn_id, turnOrdinal, snapshot: turnSnapshot }
    };
    const snapshotAppend = appendIfAbsent(ledger, snapshotInput, sidecarEventId(input.session_id, input.turn_id, "turn-snapshot"));
    ledger = snapshotAppend.ledger;
    snapshotIdempotent = snapshotAppend.idempotent;
  }

  let checkpoint: GitCheckpoint;
  try {
    checkpoint = captureGitCleanCheckpoint(cwd, { runGit: runHardenedGit });
  } catch (error) {
    // Persist the completed turn (and any captured snapshot) even when no
    // clean Git state can be observed.
    writeSidecarLedger(ledgerPath, ledger);
    const idempotent = completion.idempotent && snapshotIdempotent;
    const result = isDirtyCheckpointError(error)
      ? checkpointResult("CHECKPOINT_SKIPPED_DIRTY", ledgerPath, input.session_id, input.turn_id, idempotent, undefined, turnSnapshot)
      : checkpointResult("CHECKPOINT_SKIPPED_UNAVAILABLE", ledgerPath, input.session_id, input.turn_id, idempotent, undefined, turnSnapshot);
    writeReceipt(directory, input, result);
    return result;
  }
  const checkpointInput: LifecycleEventInput = {
    type: "WORKTREE_CHECKPOINT",
    payload: { checkpoint, afterTurnOrdinal: turnOrdinal }
  };
  const checkpointAppend = appendIfAbsent(ledger, checkpointInput, sidecarEventId(input.session_id, input.turn_id, "checkpoint"));
  ledger = checkpointAppend.ledger;
  writeSidecarLedger(ledgerPath, ledger);
  const result = checkpointResult(
    "CHECKPOINT_RECORDED",
    ledgerPath,
    input.session_id,
    input.turn_id,
    completion.idempotent && checkpointAppend.idempotent && snapshotIdempotent,
    checkpoint,
    turnSnapshot
  );
  writeReceipt(directory, input, result);
  return result;
}

/**
 * Record one public Codex hook event. Unknown hook properties are discarded
 * before any record is created, so transcript paths, assistant messages, and
 * other non-allowlisted hook data cannot enter the FaultLine ledger.
 */
export function recordObservedCodexHook(value: unknown): CodexSidecarResult {
  const input = CodexSidecarHookInputSchema.parse(value);
  const cwd = resolve(input.cwd);
  const directory = recordingDirectory(cwd, true);
  const ledgerPath = join(directory, `codex-${sha256(input.session_id)}.json`);
  return withSidecarLock(ledgerPath, () => {
    switch (input.hook_event_name) {
      case "SessionStart":
        return recordSessionStart(input, cwd, ledgerPath);
      case "UserPromptSubmit":
        return recordTurnStart(input, cwd, ledgerPath);
      case "Stop":
        return recordTurnStop(input, cwd, ledgerPath, directory);
    }
  });
}

function stopReceiptsForSession(directory: string, sessionId: string): {
  readonly receipts: ReadonlyMap<string, StopReceipt>;
  readonly errors: readonly string[];
} {
  const receipts = new Map<string, StopReceipt>();
  const errors: string[] = [];
  if (!safeDirectoryExists(directory, "FaultLine sidecar recordings")) return { receipts, errors };
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (!/^codex-stop-[a-f0-9]{64}\.json$/.test(entry.name)) continue;
    try {
      const receipt = readStopReceipt(join(directory, entry.name));
      if (receipt?.sessionId === sessionId) receipts.set(receipt.turnId, receipt);
    } catch (error) {
      errors.push(`Invalid sidecar stop receipt ${entry.name}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  return { receipts, errors };
}

/**
 * Read local recorder health without exposing prompts, assistant messages, or
 * transcript paths. The returned `ledgerPath` is the exact value that can be
 * supplied to `fl incident continue --ledger` after human review/freeze.
 */
export function inspectObservedCodexSidecar(cwd: string, sessionId?: string): CodexSidecarInspection {
  if (sessionId !== undefined) IdentifierSchema.parse(sessionId);
  const directory = recordingDirectory(resolve(cwd), false);
  if (!safeDirectoryExists(directory, "FaultLine sidecar recordings")) {
    return { storageDirectory: directory, recordings: [] };
  }
  const requestedPath = sessionId === undefined ? undefined : join(directory, `codex-${sha256(sessionId)}.json`);
  const ledgerPaths = requestedPath === undefined
    ? readdirSync(directory, { withFileTypes: true })
      .filter((entry) => /^codex-[a-f0-9]{64}\.json$/.test(entry.name))
      .map((entry) => join(directory, entry.name))
      .filter((path) => safeRegularFileExists(path, "FaultLine sidecar ledger"))
      .sort((left, right) => left.localeCompare(right))
    : safeRegularFileExists(requestedPath, "FaultLine sidecar ledger") ? [requestedPath] : [];

  return {
    storageDirectory: directory,
    recordings: ledgerPaths.map((ledgerPath) => {
      const verification = verifyCodexLifecycleLedgerFile(ledgerPath);
      if (!verification.valid) {
        return {
          ledgerPath,
          valid: false,
          eventCount: verification.eventCount,
          headHash: verification.headHash,
          errors: verification.errors
        };
      }
      const ledger = readSidecarLedger(ledgerPath);
      const lastEvent = ledger.events.at(-1);
      const lock = sidecarLockHealth(ledgerPath);
      const lastCheckpointEvent = [...ledger.events].reverse().find((event) => event.event.type === "WORKTREE_CHECKPOINT");
      const latestCheckpoint = lastCheckpointEvent?.event.type === "WORKTREE_CHECKPOINT"
        ? {
          digest: lastCheckpointEvent.event.payload.checkpoint.digest,
          capturedAt: lastCheckpointEvent.event.payload.checkpoint.capturedAt,
          headCommit: lastCheckpointEvent.event.payload.checkpoint.headCommit,
          treeDigest: lastCheckpointEvent.event.payload.checkpoint.treeDigest,
          afterTurnOrdinal: lastCheckpointEvent.event.payload.afterTurnOrdinal
        }
        : undefined;
      const lastCompletion = [...ledger.events].reverse().find((event) => event.event.type === "TURN_COMPLETED");
      const receiptScan = stopReceiptsForSession(directory, ledger.sessionId);
      const receiptErrors = [...receiptScan.errors];
      for (const receipt of receiptScan.receipts.values()) {
        try {
          assertReceiptMatchesLedger(receipt, ledger, { session_id: receipt.sessionId, turn_id: receipt.turnId });
        } catch (error) {
          receiptErrors.push(`Contradictory sidecar stop receipt for ${receipt.turnId}: ${error instanceof Error ? error.message : String(error)}`);
        }
      }
      if (receiptErrors.length > 0) {
        return {
          ledgerPath,
          sessionId: ledger.sessionId,
          valid: false,
          eventCount: verification.eventCount,
          headHash: verification.headHash,
          ...(lastEvent === undefined ? {} : { lastEvent: { type: lastEvent.event.type, occurredAt: lastEvent.occurredAt } }),
          ...(latestCheckpoint === undefined ? {} : { latestCheckpoint }),
          ...(lock === undefined ? {} : { lock }),
          errors: receiptErrors
        };
      }
      let latestStop: CodexSidecarInspection["recordings"][number]["latestStop"];
      if (lastCompletion?.event.type === "TURN_COMPLETED") {
        const { turnId, turnOrdinal } = lastCompletion.event.payload;
        const receipt = receiptScan.receipts.get(turnId);
        const checkpointForCompletion = [...ledger.events].reverse().find((event) => event.event.type === "WORKTREE_CHECKPOINT"
          && event.event.payload.afterTurnOrdinal === turnOrdinal);
        latestStop = receipt === undefined
          ? checkpointForCompletion === undefined
            ? { turnId, status: "CHECKPOINT_PENDING_RECOVERY", receipt: "MISSING" }
            : { turnId, status: "CHECKPOINT_RECORDED", receipt: "RECOVERABLE_FROM_LEDGER" }
          : {
            turnId,
            status: receipt.status,
            ...(receipt.reason === undefined ? {} : { reason: receipt.reason }),
            receipt: "DURABLE"
          };
      }
      return {
        ledgerPath,
        sessionId: ledger.sessionId,
        valid: true,
        eventCount: verification.eventCount,
        headHash: verification.headHash,
        ...(lastEvent === undefined ? {} : { lastEvent: { type: lastEvent.event.type, occurredAt: lastEvent.occurredAt } }),
        ...(latestCheckpoint === undefined ? {} : { latestCheckpoint }),
        ...(latestStop === undefined ? {} : { latestStop }),
        ...(lock === undefined ? {} : { lock }),
        errors: []
      };
    })
  };
}
