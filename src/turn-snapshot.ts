import { randomUUID } from "node:crypto";
import { spawnSync } from "node:child_process";
import { rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { z } from "zod";
import { digestJson, sha256 } from "./canonical.js";
import { DOCTOR_SAFE_GIT_CONFIG } from "./doctor.js";

/**
 * A dirty-worktree-safe Codex turn boundary marker. Unlike the clean Git
 * checkpoint in `ledger.ts`, this snapshot never requires a clean worktree or
 * a human-visible commit: it hashes exactly what is on disk at Stop time by
 * building a throwaway Git tree in a temporary index, so FaultLine can still
 * localize a regression to the turn that introduced it even mid-session.
 */
export const TURN_TREE_SNAPSHOT_VERSION = "faultline.turn-tree-snapshot.v1" as const;

const HashSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/, "Expected a sha256 digest");
const GitObjectIdSchema = z.string().regex(/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/, "Expected a Git object id");
const CanonicalTimestampSchema = z.string().refine(
  (value) => {
    const parsed = new Date(value);
    return Number.isFinite(parsed.getTime()) && parsed.toISOString() === value;
  },
  "Expected a canonical ISO-8601 UTC timestamp"
);

export const UnsignedTurnTreeSnapshotSchema = z.object({
  schemaVersion: z.literal(TURN_TREE_SNAPSHOT_VERSION),
  repositoryRoot: z.string().min(1),
  headCommit: GitObjectIdSchema,
  treeDigest: GitObjectIdSchema,
  capturedAt: CanonicalTimestampSchema,
  dirty: z.boolean(),
  statusDigest: HashSchema
}).strict();

export const TurnTreeSnapshotSchema = UnsignedTurnTreeSnapshotSchema.extend({
  digest: HashSchema
}).strict();

export type UnsignedTurnTreeSnapshot = z.infer<typeof UnsignedTurnTreeSnapshotSchema>;
export type TurnTreeSnapshot = z.infer<typeof TurnTreeSnapshotSchema>;

export class TurnSnapshotError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TurnSnapshotError";
  }
}

/**
 * Pathspec magic exclusions applied to every `git add -A` used to build a
 * turn tree. FaultLine's own operational directory, dependency trees, and the
 * most common local secret file shapes never belong in a regression witness.
 */
export const TURN_SNAPSHOT_EXCLUDED_PATHSPECS: readonly string[] = Object.freeze([
  ":(exclude,glob).faultline/**",
  ":(exclude,glob)node_modules/**",
  ":(exclude,glob)**/node_modules/**",
  ":(exclude,glob).env",
  ":(exclude,glob).env.*",
  ":(exclude,glob)**/.env",
  ":(exclude,glob)**/.env.*",
  ":(exclude,glob)*.pem",
  ":(exclude,glob)**/*.pem",
  ":(exclude,glob)*.key",
  ":(exclude,glob)**/*.key",
  ":(exclude,glob)*.pfx",
  ":(exclude,glob)**/*.pfx",
  ":(exclude,glob)id_rsa",
  ":(exclude,glob)id_rsa.*",
  ":(exclude,glob)**/id_rsa",
  ":(exclude,glob)**/id_rsa.*"
]);

/** Delay between consecutive tree captures while proving filesystem quiescence. */
export const TURN_SNAPSHOT_QUIESCENCE_DELAY_MS = 50;
/**
 * Maximum dual-tree attempts before fail-closed. Each attempt captures two
 * throwaway trees; keep this small so Stop-hook latency stays bounded.
 */
export const TURN_SNAPSHOT_MAX_QUIESCENCE_ATTEMPTS = 4;
const STATUS_ARGS = ["status", "--porcelain=v1", "--untracked-files=all", "-z"] as const;

export type TurnSnapshotGitRunner = (repositoryRoot: string, args: readonly string[], env?: NodeJS.ProcessEnv) => string;

/**
 * Repository configuration is executable input, so every invocation receives
 * the same fixed, hooks/filters/network-disabled overrides as `fl doctor`,
 * with a stripped, non-interactive process environment underneath them.
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

/**
 * The default Git runner. `env` only ever carries `GIT_INDEX_FILE` for the
 * temporary-index add/write-tree calls; every read-only call (rev-parse,
 * status) omits it and therefore reads and writes back nothing but the
 * user's real, unmodified `.git/index`.
 */
export function defaultTurnSnapshotGitRunner(repositoryRoot: string, args: readonly string[], env?: NodeJS.ProcessEnv): string {
  const result = spawnSync("git", [...DOCTOR_SAFE_GIT_CONFIG, "-C", repositoryRoot, ...args], {
    encoding: "utf8",
    shell: false,
    windowsHide: true,
    env: { ...hardenedGitEnvironment(), ...env }
  });
  if (result.error || result.status !== 0) {
    const detail = `${result.stderr ?? ""}${result.error?.message ?? ""}`.trim();
    throw new TurnSnapshotError(`FaultLine could not safely capture a turn tree snapshot: ${detail || `exit ${result.status ?? "unknown"}`}`);
  }
  return String(result.stdout ?? "");
}

/** Blocks the current thread without a shell or a busy loop; used only for the quiescence delay. */
function blockingSleep(milliseconds: number): void {
  if (milliseconds <= 0) return;
  const signal = new Int32Array(new SharedArrayBuffer(Int32Array.BYTES_PER_ELEMENT));
  Atomics.wait(signal, 0, 0, milliseconds);
}

export function signTurnTreeSnapshot(unsigned: UnsignedTurnTreeSnapshot): TurnTreeSnapshot {
  const parsed = UnsignedTurnTreeSnapshotSchema.parse(unsigned);
  return TurnTreeSnapshotSchema.parse({ ...parsed, digest: digestJson(parsed) });
}

export function verifyTurnTreeSnapshot(value: unknown): string[] {
  const parsed = TurnTreeSnapshotSchema.safeParse(value);
  if (!parsed.success) {
    return parsed.error.issues.map((issue) => `Invalid turn tree snapshot at ${issue.path.join(".") || "root"}: ${issue.message}`);
  }
  const { digest, ...unsigned } = parsed.data;
  return digest === digestJson(unsigned) ? [] : ["Turn tree snapshot digest does not match its contents"];
}

export type CaptureTurnTreeSnapshotOptions = {
  now?: () => Date;
  runGit?: TurnSnapshotGitRunner;
  /** Injectable so tests can skip the real wall-clock wait. Production callers should not override this. */
  sleep?: (milliseconds: number) => void;
  /** @deprecated Use `quiescenceDelayMs`. Kept so older callers still compile. */
  midWriteCheckDelayMs?: number;
  quiescenceDelayMs?: number;
  maxQuiescenceAttempts?: number;
};

function writeThrowawayTreeDigest(runGit: TurnSnapshotGitRunner, repositoryRoot: string): string {
  const temporaryIndexPath = join(tmpdir(), `faultline-turn-tree-${randomUUID()}.index`);
  try {
    runGit(repositoryRoot, ["add", "--all", "--", ".", ...TURN_SNAPSHOT_EXCLUDED_PATHSPECS], { GIT_INDEX_FILE: temporaryIndexPath });
    const treeDigest = runGit(repositoryRoot, ["write-tree"], { GIT_INDEX_FILE: temporaryIndexPath }).trim();
    if (!GitObjectIdSchema.safeParse(treeDigest).success) {
      throw new TurnSnapshotError("Git did not return a valid tree object id for this turn tree snapshot.");
    }
    return treeDigest;
  } finally {
    rmSync(temporaryIndexPath, { force: true });
  }
}

/**
 * Captures `{ headCommit, treeDigest, dirty, statusDigest }` for whatever is
 * on disk right now, without a clean worktree, a commit, or any change to
 * HEAD, the current branch, or the user's real index.
 *
 * A throwaway `GIT_INDEX_FILE` receives `git add -A` (excluding FaultLine's
 * own storage, dependency trees, and common secret shapes) and is then
 * discarded after `git write-tree` returns the resulting tree object id.
 *
 * Quiescence is proven by dual tree capture (Option A) with status brackets
 * (Option B): capture tree A, wait, capture tree B, and accept only when
 * `tree A == tree B` and the porcelain status is unchanged around both
 * captures. Status equality alone is not enough — an already-dirty file can
 * change bytes without altering the porcelain string. Retries are bounded;
 * exhaustion fails closed rather than returning a torn snapshot.
 */
export function captureTurnTreeSnapshot(repository: string, options: CaptureTurnTreeSnapshotOptions = {}): TurnTreeSnapshot {
  const runGit = options.runGit ?? defaultTurnSnapshotGitRunner;
  const sleep = options.sleep ?? blockingSleep;
  const quiescenceDelayMs = options.quiescenceDelayMs
    ?? options.midWriteCheckDelayMs
    ?? TURN_SNAPSHOT_QUIESCENCE_DELAY_MS;
  const maxQuiescenceAttempts = options.maxQuiescenceAttempts ?? TURN_SNAPSHOT_MAX_QUIESCENCE_ATTEMPTS;
  const now = options.now ?? (() => new Date());

  if (!Number.isInteger(maxQuiescenceAttempts) || maxQuiescenceAttempts < 1) {
    throw new TurnSnapshotError("Turn tree snapshot quiescence attempt budget must be a positive integer.");
  }
  if (!Number.isFinite(quiescenceDelayMs) || quiescenceDelayMs < 0) {
    throw new TurnSnapshotError("Turn tree snapshot quiescence delay must be a non-negative number of milliseconds.");
  }

  const requestedRoot = resolve(repository);
  const repositoryRoot = resolve(runGit(requestedRoot, ["rev-parse", "--show-toplevel"]).trim());
  const sampleStatus = (): string => runGit(repositoryRoot, [...STATUS_ARGS]);

  let acceptedTree: string | null = null;
  let acceptedStatus: string | null = null;
  let acceptedHead: string | null = null;

  for (let attempt = 1; attempt <= maxQuiescenceAttempts; attempt += 1) {
    const headBefore = runGit(repositoryRoot, ["rev-parse", "HEAD"]).trim();
    if (!GitObjectIdSchema.safeParse(headBefore).success) {
      throw new TurnSnapshotError("Git did not return a resolvable HEAD commit for this turn tree snapshot.");
    }
    const statusBeforeA = sampleStatus();
    const treeA = writeThrowawayTreeDigest(runGit, repositoryRoot);
    const statusAfterA = sampleStatus();
    if (statusBeforeA !== statusAfterA) {
      // Mutation during the first add/write-tree window — do not trust treeA.
      continue;
    }

    sleep(quiescenceDelayMs);

    const statusBeforeB = sampleStatus();
    const treeB = writeThrowawayTreeDigest(runGit, repositoryRoot);
    const statusAfterB = sampleStatus();
    const headAfter = runGit(repositoryRoot, ["rev-parse", "HEAD"]).trim();
    if (statusBeforeB !== statusAfterB) {
      continue;
    }
    if (statusAfterA !== statusBeforeB || statusAfterA !== statusAfterB) {
      // Porcelain changed across the delay or between the paired captures.
      continue;
    }
    if (treeA !== treeB) {
      // Content changed even if porcelain looked identical (dirty-file bytes).
      continue;
    }
    if (headBefore !== headAfter) {
      // HEAD moved during capture; the snapshot would bind the wrong commit tip.
      continue;
    }

    acceptedTree = treeB;
    acceptedStatus = statusAfterB;
    acceptedHead = headAfter;
    break;
  }

  if (acceptedTree === null || acceptedStatus === null || acceptedHead === null) {
    throw new TurnSnapshotError(
      `FaultLine could not prove turn-tree quiescence after ${maxQuiescenceAttempts} dual-tree attempt(s) and refused a torn turn tree snapshot.`
    );
  }

  const unsigned: UnsignedTurnTreeSnapshot = {
    schemaVersion: TURN_TREE_SNAPSHOT_VERSION,
    repositoryRoot,
    headCommit: acceptedHead,
    treeDigest: acceptedTree,
    capturedAt: now().toISOString(),
    dirty: acceptedStatus.length > 0,
    statusDigest: `sha256:${sha256(acceptedStatus)}`
  };
  return signTurnTreeSnapshot(unsigned);
}
