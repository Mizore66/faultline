import { randomUUID } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, lstatSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { z } from "zod";
import { digestJson, sha256 } from "./canonical.js";
import { DOCTOR_SAFE_GIT_CONFIG } from "./doctor.js";
import { redactText } from "./redaction.js";

/**
 * A dirty-worktree-safe Codex turn boundary marker. Unlike the clean Git
 * checkpoint in `ledger.ts`, this snapshot never requires a clean worktree or
 * a human-visible commit: it hashes exactly what is on disk at Stop time by
 * building a throwaway Git tree in a temporary index, so FaultLine can still
 * localize a regression to the turn that introduced it even mid-session.
 *
 * Staging is not storage-neutral: `git add` / `write-tree` write blobs into
 * the repository object database. Every path is therefore filtered before any
 * Git object write.
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

/** Hard caps enforced before any blob is written into the object database. */
export const TURN_SNAPSHOT_MAX_FILE_BYTES = 1_048_576;
export const TURN_SNAPSHOT_MAX_TOTAL_BYTES = 32 * 1_048_576;
export const TURN_SNAPSHOT_MAX_FILE_COUNT = 2_000;
const TURN_SNAPSHOT_ADD_CHUNK_SIZE = 64;
const TURN_SNAPSHOT_SECRET_SCAN_BYTES = 256 * 1024;

/**
 * Built-in path exclusions (gitignore semantics) applied before `.faultlineignore`.
 * Covers FaultLine storage, secrets, dependencies, and common build/cache trees.
 */
export const TURN_SNAPSHOT_DEFAULT_IGNORE_PATTERNS: readonly string[] = Object.freeze([
  ".faultline/",
  "node_modules/",
  ".env",
  ".env.*",
  "*.pem",
  "*.key",
  "*.pfx",
  "id_rsa",
  "id_rsa.*",
  "dist/",
  "build/",
  "out/",
  ".next/",
  ".nuxt/",
  ".turbo/",
  ".cache/",
  "coverage/",
  "target/",
  "__pycache__/",
  "*.pyc",
  ".venv/",
  "venv/",
  "vendor/",
  "tmp/",
  "temp/",
  "*.log",
  ".git/"
]);

/** @deprecated Prefer `TURN_SNAPSHOT_DEFAULT_IGNORE_PATTERNS`; retained for older imports. */
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

export type TurnTreeSnapshotCaptureResult = {
  snapshot: TurnTreeSnapshot;
  warnings: readonly string[];
};

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
  /** When true, never stage untracked paths (mode filter). */
  trackedFilesOnly?: boolean;
  maxFileBytes?: number;
  maxTotalBytes?: number;
  maxFileCount?: number;
  onWarning?: (warning: string) => void;
};

type IgnoreRule = {
  readonly raw: string;
  readonly negated: boolean;
  readonly directoryOnly: boolean;
  readonly regex: RegExp;
};

type SnapshotCandidate = {
  readonly relativePath: string;
  readonly tracked: boolean;
  readonly exists: boolean;
  readonly bytes: number;
};

function normalizeRepoRelativePath(value: string): string {
  return value.replaceAll("\\", "/").replace(/^\.\/+/, "").replace(/^\/+/, "");
}

function parseIgnoreLines(lines: readonly string[]): IgnoreRule[] {
  const rules: IgnoreRule[] = [];
  for (const rawLine of lines) {
    const trimmed = rawLine.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    let negated = false;
    let body = trimmed;
    if (body.startsWith("!")) {
      negated = true;
      body = body.slice(1);
    }
    body = body.replaceAll("\\", "/");
    const directoryOnly = body.endsWith("/");
    if (directoryOnly) body = body.slice(0, -1);
    if (!body) continue;
    const anchored = body.startsWith("/");
    if (anchored) body = body.slice(1);
    const escaped = body
      .replace(/[.+^${}()|[\]\\]/g, "\\$&")
      .replace(/\*\*/g, "\0DOUBLE\0")
      .replace(/\*/g, "[^/]*")
      .replace(/\?/g, "[^/]")
      .replaceAll("\0DOUBLE\0", ".*");
    const prefix = anchored ? "^" : "(?:^|/)";
    const suffix = directoryOnly ? "(?:/.*)?$" : "$";
    rules.push({
      raw: trimmed,
      negated,
      directoryOnly,
      regex: new RegExp(`${prefix}${escaped}${suffix}`, "i")
    });
  }
  return rules;
}

function isIgnoredByRules(relativePath: string, rules: readonly IgnoreRule[]): boolean {
  const path = normalizeRepoRelativePath(relativePath);
  let ignored = false;
  for (const rule of rules) {
    if (rule.regex.test(path)) ignored = !rule.negated;
  }
  return ignored;
}

function loadFaultlineIgnoreRules(repositoryRoot: string): IgnoreRule[] {
  const defaults = parseIgnoreLines(TURN_SNAPSHOT_DEFAULT_IGNORE_PATTERNS);
  const ignorePath = join(repositoryRoot, ".faultlineignore");
  if (!existsSync(ignorePath)) return defaults;
  let text: string;
  try {
    const stat = lstatSync(ignorePath);
    if (stat.isSymbolicLink() || !stat.isFile()) {
      throw new TurnSnapshotError(".faultlineignore must be a regular non-symlink file.");
    }
    if (stat.size > 256 * 1024) {
      throw new TurnSnapshotError(".faultlineignore exceeds FaultLine's 256 KiB parse limit.");
    }
    text = readFileSync(ignorePath, "utf8");
  } catch (error) {
    if (error instanceof TurnSnapshotError) throw error;
    throw new TurnSnapshotError(
      `Could not read .faultlineignore: ${error instanceof Error ? error.message : String(error)}`
    );
  }
  return [...defaults, ...parseIgnoreLines(text.split(/\r?\n/))];
}

function splitNullPaths(value: string): string[] {
  return value.split("\0").map((entry) => normalizeRepoRelativePath(entry)).filter(Boolean);
}

function shannonEntropy(value: string): number {
  if (value.length === 0) return 0;
  const counts = new Map<string, number>();
  for (const char of value) counts.set(char, (counts.get(char) ?? 0) + 1);
  let entropy = 0;
  for (const count of counts.values()) {
    const probability = count / value.length;
    entropy -= probability * Math.log2(probability);
  }
  return entropy;
}

function looksBinary(bytes: Buffer): boolean {
  const sample = bytes.subarray(0, Math.min(bytes.length, 8_192));
  return sample.includes(0);
}

function hasHighEntropyToken(text: string): boolean {
  for (const match of text.matchAll(/[A-Za-z0-9_+\-\/=]{40,}/g)) {
    const token = match[0];
    if (shannonEntropy(token) >= 4.5) return true;
  }
  return false;
}

function assertNoSnapshotSecrets(relativePath: string, absolutePath: string, bytes: number): void {
  if (bytes === 0) return;
  const readBytes = Math.min(bytes, TURN_SNAPSHOT_SECRET_SCAN_BYTES);
  let buffer: Buffer;
  try {
    buffer = readFileSync(absolutePath).subarray(0, readBytes);
  } catch (error) {
    throw new TurnSnapshotError(
      `Could not read ${relativePath} for snapshot secret scanning: ${error instanceof Error ? error.message : String(error)}`
    );
  }
  if (looksBinary(buffer)) return;
  const text = buffer.toString("utf8");
  const report = redactText(text, relativePath).report;
  if (report.highConfidenceCount > 0) {
    const kinds = [...new Set(report.occurrences.filter((item) => item.confidence === "HIGH").map((item) => item.kind))];
    throw new TurnSnapshotError(
      `Refusing turn-tree snapshot: high-confidence secret material detected in ${relativePath} (${kinds.join(", ")}).`
    );
  }
  if (hasHighEntropyToken(text)) {
    throw new TurnSnapshotError(
      `Refusing turn-tree snapshot: high-entropy secret-like token detected in ${relativePath}.`
    );
  }
}

/**
 * Pre-blob intercept: mode → path → caps → secret scan.
 * No `git add` / `write-tree` runs until this returns.
 */
export function planTurnSnapshotPaths(
  repositoryRoot: string,
  runGit: TurnSnapshotGitRunner,
  options: {
    trackedFilesOnly?: boolean;
    maxFileBytes?: number;
    maxTotalBytes?: number;
    maxFileCount?: number;
  } = {}
): { paths: readonly string[]; warnings: readonly string[] } {
  const trackedFilesOnly = options.trackedFilesOnly === true;
  const maxFileBytes = options.maxFileBytes ?? TURN_SNAPSHOT_MAX_FILE_BYTES;
  const maxTotalBytes = options.maxTotalBytes ?? TURN_SNAPSHOT_MAX_TOTAL_BYTES;
  const maxFileCount = options.maxFileCount ?? TURN_SNAPSHOT_MAX_FILE_COUNT;
  const ignoreRules = loadFaultlineIgnoreRules(repositoryRoot);

  const tracked = new Set(splitNullPaths(runGit(repositoryRoot, ["ls-files", "-z"])));
  const untracked = trackedFilesOnly
    ? []
    : splitNullPaths(runGit(repositoryRoot, ["ls-files", "-z", "--others", "--exclude-standard"]));

  const warnings: string[] = [];
  const candidates: SnapshotCandidate[] = [];

  for (const relativePath of [...tracked, ...untracked]) {
    if (isIgnoredByRules(relativePath, ignoreRules)) continue;
    const absolutePath = join(repositoryRoot, ...relativePath.split("/"));
    let exists = false;
    let bytes = 0;
    try {
      const stat = lstatSync(absolutePath);
      if (stat.isSymbolicLink()) {
        throw new TurnSnapshotError(`Refusing turn-tree snapshot: symbolic link is not snapshot-eligible: ${relativePath}`);
      }
      if (stat.isFile()) {
        exists = true;
        bytes = stat.size;
      } else if (stat.isDirectory()) {
        continue;
      }
    } catch (error) {
      if (error instanceof TurnSnapshotError) throw error;
      // Missing path: allow tracked deletions to be staged; skip vanished untracked noise.
      if (!tracked.has(relativePath)) continue;
      exists = false;
      bytes = 0;
    }

    if (!tracked.has(relativePath) && exists) {
      warnings.push(`Including previously untracked file in turn-tree snapshot: ${relativePath}`);
    }
    candidates.push({
      relativePath,
      tracked: tracked.has(relativePath),
      exists,
      bytes
    });
  }

  if (candidates.length > maxFileCount) {
    throw new TurnSnapshotError(
      `Refusing turn-tree snapshot: ${candidates.length} eligible files exceed the maximum of ${maxFileCount}.`
    );
  }

  let totalBytes = 0;
  for (const candidate of candidates) {
    if (!candidate.exists) continue;
    if (candidate.bytes > maxFileBytes) {
      throw new TurnSnapshotError(
        `Refusing turn-tree snapshot: ${candidate.relativePath} is ${candidate.bytes} bytes (max ${maxFileBytes} per file).`
      );
    }
    totalBytes += candidate.bytes;
    if (totalBytes > maxTotalBytes) {
      throw new TurnSnapshotError(
        `Refusing turn-tree snapshot: eligible content exceeds the maximum total of ${maxTotalBytes} bytes.`
      );
    }
  }

  for (const candidate of candidates) {
    if (!candidate.exists) continue;
    assertNoSnapshotSecrets(
      candidate.relativePath,
      join(repositoryRoot, ...candidate.relativePath.split("/")),
      candidate.bytes
    );
  }

  return {
    paths: candidates.map((candidate) => candidate.relativePath).sort((left, right) => left.localeCompare(right)),
    warnings
  };
}

function chunkPaths(paths: readonly string[], size: number): string[][] {
  const chunks: string[][] = [];
  for (let index = 0; index < paths.length; index += size) {
    chunks.push([...paths.slice(index, index + size)]);
  }
  return chunks;
}

function writeThrowawayTreeDigest(
  runGit: TurnSnapshotGitRunner,
  repositoryRoot: string,
  options: CaptureTurnTreeSnapshotOptions
): { treeDigest: string; warnings: readonly string[] } {
  // Gates 1–4 run here — before any temporary-index `git add` can write blobs.
  const plan = planTurnSnapshotPaths(repositoryRoot, runGit, {
    ...(options.trackedFilesOnly === undefined ? {} : { trackedFilesOnly: options.trackedFilesOnly }),
    ...(options.maxFileBytes === undefined ? {} : { maxFileBytes: options.maxFileBytes }),
    ...(options.maxTotalBytes === undefined ? {} : { maxTotalBytes: options.maxTotalBytes }),
    ...(options.maxFileCount === undefined ? {} : { maxFileCount: options.maxFileCount })
  });

  const temporaryIndexPath = join(tmpdir(), `faultline-turn-tree-${randomUUID()}.index`);
  try {
    for (const chunk of chunkPaths(plan.paths, TURN_SNAPSHOT_ADD_CHUNK_SIZE)) {
      if (chunk.length === 0) continue;
      runGit(repositoryRoot, ["add", "--", ...chunk], { GIT_INDEX_FILE: temporaryIndexPath });
    }
    const treeDigest = runGit(repositoryRoot, ["write-tree"], { GIT_INDEX_FILE: temporaryIndexPath }).trim();
    if (!GitObjectIdSchema.safeParse(treeDigest).success) {
      throw new TurnSnapshotError("Git did not return a valid tree object id for this turn tree snapshot.");
    }
    return { treeDigest, warnings: plan.warnings };
  } finally {
    rmSync(temporaryIndexPath, { force: true });
  }
}

/**
 * Captures `{ headCommit, treeDigest, dirty, statusDigest }` for whatever is
 * on disk right now, without a clean worktree, a commit, or any change to
 * HEAD, the current branch, or the user's real index.
 *
 * Pre-blob intercept order (storage-neutral until acceptance):
 * 1. Mode filter (`trackedFilesOnly`)
 * 2. Path filter (defaults + `.faultlineignore`)
 * 3. Caps filter (count / per-file / total size via lstat)
 * 4. Secret scan (regex + entropy on eligible buffers)
 * Only then: temporary-index `git add` + `write-tree`.
 *
 * Quiescence is proven by dual tree capture with status brackets and bounded
 * retries; exhaustion fails closed rather than returning a torn snapshot.
 */
export function captureTurnTreeSnapshot(
  repository: string,
  options: CaptureTurnTreeSnapshotOptions = {}
): TurnTreeSnapshotCaptureResult {
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
  let acceptedWarnings: readonly string[] = [];

  for (let attempt = 1; attempt <= maxQuiescenceAttempts; attempt += 1) {
    const headBefore = runGit(repositoryRoot, ["rev-parse", "HEAD"]).trim();
    if (!GitObjectIdSchema.safeParse(headBefore).success) {
      throw new TurnSnapshotError("Git did not return a resolvable HEAD commit for this turn tree snapshot.");
    }
    const statusBeforeA = sampleStatus();
    const first = writeThrowawayTreeDigest(runGit, repositoryRoot, options);
    const statusAfterA = sampleStatus();
    if (statusBeforeA !== statusAfterA) {
      continue;
    }

    sleep(quiescenceDelayMs);

    const statusBeforeB = sampleStatus();
    const second = writeThrowawayTreeDigest(runGit, repositoryRoot, options);
    const statusAfterB = sampleStatus();
    const headAfter = runGit(repositoryRoot, ["rev-parse", "HEAD"]).trim();
    if (statusBeforeB !== statusAfterB) {
      continue;
    }
    if (statusAfterA !== statusBeforeB || statusAfterA !== statusAfterB) {
      continue;
    }
    if (first.treeDigest !== second.treeDigest) {
      continue;
    }
    if (headBefore !== headAfter) {
      continue;
    }

    acceptedTree = second.treeDigest;
    acceptedStatus = statusAfterB;
    acceptedHead = headAfter;
    acceptedWarnings = second.warnings;
    break;
  }

  if (acceptedTree === null || acceptedStatus === null || acceptedHead === null) {
    throw new TurnSnapshotError(
      `FaultLine could not prove turn-tree quiescence after ${maxQuiescenceAttempts} dual-tree attempt(s) and refused a torn turn tree snapshot.`
    );
  }

  for (const warning of acceptedWarnings) {
    options.onWarning?.(warning);
  }

  const snapshot = signTurnTreeSnapshot({
    schemaVersion: TURN_TREE_SNAPSHOT_VERSION,
    repositoryRoot,
    headCommit: acceptedHead,
    treeDigest: acceptedTree,
    capturedAt: now().toISOString(),
    dirty: acceptedStatus.length > 0,
    statusDigest: `sha256:${sha256(acceptedStatus)}`
  });
  return { snapshot, warnings: acceptedWarnings };
}
