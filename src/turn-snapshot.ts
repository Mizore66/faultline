import { createHash, randomUUID } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  closeSync,
  existsSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  readSync,
  rmSync,
  writeFileSync
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { z } from "zod";
import { digestJson, sha256 } from "./canonical.js";
import { DOCTOR_SAFE_GIT_CONFIG } from "./doctor.js";
import { ENVIRONMENT_DESCRIPTOR_FILES } from "./environment-fingerprint.js";
import { redactText } from "./redaction.js";
import {
  allowlistConfigDigest,
  fileContentDigest,
  filterAllowlistedHighOccurrences,
  isEntropyTokenAllowlisted,
  isHighOccurrenceAllowlisted,
  loadSecretAllowlist,
  type SecretAllowlist
} from "./secret-allowlist.js";

/**
 * A dirty-worktree-safe Codex turn boundary marker. Unlike the clean Git
 * checkpoint in `ledger.ts`, this snapshot never requires a clean worktree or
 * a human-visible commit: it hashes exactly what is on disk at Stop time by
 * building a throwaway Git tree in a temporary index, so FaultLine can still
 * localize a regression to the turn that introduced it even mid-session.
 *
 * Staging is not storage-neutral: `hash-object` / `write-tree` write blobs.
 * FaultLine quarantines those writes into `.git/faultline/objects` via
 * `GIT_OBJECT_DIRECTORY` so the user's primary `.git/objects` database is not
 * polluted. Every path is filtered before any Git object write.
 *
 * Dirty trees are staged incrementally: the temporary index is seeded from the
 * previous accepted turn tree (same HEAD + policy) or `HEAD^{tree}`, then only
 * modified, newly untracked, deleted, and policy-removed paths are updated —
 * never a full-repo `git add` of every eligible file.
 */

/** Relative to the repository `.git` directory. */
export const FAULTLINE_SNAPSHOT_OBJECT_DIR = "faultline/objects" as const;
/** Alternates entry relative to `.git/objects` so normal Git reads can resolve quarantined trees. */
export const FAULTLINE_SNAPSHOT_ALTERNATES_ENTRY = "../faultline/objects" as const;
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

export type TurnSnapshotGitRunner = (
  repositoryRoot: string,
  args: readonly string[],
  env?: NodeJS.ProcessEnv,
  options?: { readonly input?: string | Buffer }
) => string;

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

export function faultlineSnapshotObjectDirectory(repositoryRoot: string): string {
  return join(resolve(repositoryRoot), ".git", ...FAULTLINE_SNAPSHOT_OBJECT_DIR.split("/"));
}

export function primaryGitObjectDirectory(repositoryRoot: string): string {
  return join(resolve(repositoryRoot), ".git", "objects");
}

/**
 * Ensure the quarantine object store exists and is registered as an alternate
 * of the primary object database so later reads (`ls-tree`, materialization)
 * can resolve snapshot trees without copying blobs into `.git/objects`.
 */
export function ensureFaultlineSnapshotObjectStore(repositoryRoot: string): string {
  const root = resolve(repositoryRoot);
  const quarantine = faultlineSnapshotObjectDirectory(root);
  mkdirSync(join(quarantine, "info"), { recursive: true, mode: 0o700 });
  mkdirSync(join(quarantine, "pack"), { recursive: true, mode: 0o700 });
  const alternatesPath = join(primaryGitObjectDirectory(root), "info", "alternates");
  mkdirSync(join(primaryGitObjectDirectory(root), "info"), { recursive: true, mode: 0o700 });
  let existing = "";
  if (existsSync(alternatesPath)) {
    existing = readFileSync(alternatesPath, "utf8");
  }
  const lines = existing.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  if (!lines.includes(FAULTLINE_SNAPSHOT_ALTERNATES_ENTRY)) {
    writeFileSync(
      alternatesPath,
      `${[...lines, FAULTLINE_SNAPSHOT_ALTERNATES_ENTRY].join("\n")}\n`,
      "utf8"
    );
  }
  return quarantine;
}

/**
 * Environment for quarantined object writes (`hash-object` / `write-tree` /
 * cache `cat-file -e`) so new objects land in `.git/faultline/objects` while
 * existing primary objects remain readable.
 */
export function turnSnapshotObjectWriteEnvironment(repositoryRoot: string): NodeJS.ProcessEnv {
  const root = resolve(repositoryRoot);
  const quarantine = ensureFaultlineSnapshotObjectStore(root);
  const primary = primaryGitObjectDirectory(root);
  return {
    GIT_OBJECT_DIRECTORY: quarantine,
    GIT_ALTERNATE_OBJECT_DIRECTORIES: primary
  };
}

/**
 * The default Git runner. `env` may carry `GIT_INDEX_FILE` and/or quarantined
 * object-directory variables for staging/write-tree/cat-file. Read-only calls
 * (rev-parse, status) omit those and therefore leave the user's real index and
 * primary object database alone.
 *
 * Staging calls may pass `{ input }` for `hash-object --stdin` so repository
 * clean filters never see path-based content.
 */
export function defaultTurnSnapshotGitRunner(
  repositoryRoot: string,
  args: readonly string[],
  env?: NodeJS.ProcessEnv,
  options?: { readonly input?: string | Buffer }
): string {
  const result = spawnSync(
    "git",
    [
      ...DOCTOR_SAFE_GIT_CONFIG,
      "-c", "core.attributesFile=/nonexistent/faultline-attributes",
      "-C",
      repositoryRoot,
      ...args
    ],
    {
      encoding: "utf8",
      shell: false,
      windowsHide: true,
      env: { ...hardenedGitEnvironment(), ...env },
      ...(options?.input === undefined ? {} : { input: options.input })
    }
  );
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
  /**
   * Optional session cache path (sidecar recordings dir). Cache is a performance
   * hint only: reuse requires HEAD + policy + dirty-path content fingerprints,
   * not porcelain status alone (status does not encode file contents).
   */
  sessionCachePath?: string;
};

const PathCacheEntrySchema = z.object({
  status: z.string().min(1).max(8),
  contentDigest: HashSchema
}).strict();

/** v2: content-aware cache; v1 caches are ignored (status-only was unsafe). */
const TurnSnapshotSessionCacheSchema = z.object({
  schemaVersion: z.literal("faultline.turn-snapshot-cache.v2"),
  headCommit: GitObjectIdSchema,
  treeDigest: GitObjectIdSchema,
  policyDigest: HashSchema,
  allowlistDigest: HashSchema,
  contentFingerprint: HashSchema,
  deletedPaths: z.array(z.string()).max(2_000),
  paths: z.record(z.string(), PathCacheEntrySchema),
  pathDigests: z.record(z.string(), HashSchema),
  capturedAt: CanonicalTimestampSchema
}).strict();

type TurnSnapshotSessionCache = z.infer<typeof TurnSnapshotSessionCacheSchema>;
type DirtyContentFingerprint = {
  readonly contentFingerprint: string;
  readonly deletedPaths: readonly string[];
  readonly paths: Readonly<Record<string, { status: string; contentDigest: string }>>;
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

/**
 * Environment descriptors must remain snapshot-eligible even when listed in
 * `.faultlineignore`. Suppressing them would hide lockfile/tooling changes from
 * turn trees and falsely report environment homogeneity.
 */
export function isProtectedEnvironmentDescriptorPath(relativePath: string): boolean {
  const normalized = normalizeRepoRelativePath(relativePath);
  const base = normalized.includes("/") ? normalized.slice(normalized.lastIndexOf("/") + 1) : normalized;
  if ((ENVIRONMENT_DESCRIPTOR_FILES as readonly string[]).includes(base)) return true;
  if (/^Dockerfile(?:\..+)?$/i.test(base)) return true;
  if (/^(?:docker-)?compose\.(?:ya?ml)$/i.test(base)) return true;
  return false;
}

function pathIsSnapshotExcluded(relativePath: string, rules: readonly IgnoreRule[]): boolean {
  if (isProtectedEnvironmentDescriptorPath(relativePath)) return false;
  return isIgnoredByRules(relativePath, rules);
}

/**
 * Stream a file into a sha256 digest. Caps are enforced via lstat before any
 * bytes are read, and again while streaming if the file grows past the limit.
 */
export function fullFileContentDigest(
  absolutePath: string,
  maxFileBytes: number = TURN_SNAPSHOT_MAX_FILE_BYTES
): `sha256:${string}` {
  if (!Number.isInteger(maxFileBytes) || maxFileBytes < 1) {
    throw new TurnSnapshotError("Turn snapshot max file bytes must be a positive integer.");
  }
  let stat;
  try {
    stat = lstatSync(absolutePath);
  } catch {
    throw new TurnSnapshotError(`FaultLine could not stat ${absolutePath} for content fingerprinting.`);
  }
  if (stat.isSymbolicLink() || !stat.isFile()) {
    throw new TurnSnapshotError(`Refusing to fingerprint non-regular path: ${absolutePath}`);
  }
  if (stat.size > maxFileBytes) {
    throw new TurnSnapshotError(
      `Refusing turn-tree fingerprint: ${absolutePath} is ${stat.size} bytes (max ${maxFileBytes} per file).`
    );
  }
  const hash = createHash("sha256");
  const fd = openSync(absolutePath, "r");
  try {
    const buffer = Buffer.alloc(64 * 1024);
    let total = 0;
    for (;;) {
      const bytesRead = readSync(fd, buffer, 0, buffer.length, null);
      if (bytesRead === 0) break;
      total += bytesRead;
      if (total > maxFileBytes) {
        throw new TurnSnapshotError(
          `Refusing turn-tree fingerprint: ${absolutePath} grew past ${maxFileBytes} bytes while hashing.`
        );
      }
      hash.update(buffer.subarray(0, bytesRead));
    }
  } finally {
    closeSync(fd);
  }
  return `sha256:${hash.digest("hex")}`;
}

function ignorePolicyDigest(repositoryRoot: string): string {
  const ignorePath = join(repositoryRoot, ".faultlineignore");
  const userIgnore = existsSync(ignorePath) ? readFileSync(ignorePath, "utf8") : "";
  return digestJson({
    defaults: TURN_SNAPSHOT_DEFAULT_IGNORE_PATTERNS,
    userIgnore,
    protectedDescriptors: ENVIRONMENT_DESCRIPTOR_FILES
  });
}

function snapshotPolicyDigest(options: {
  repositoryRoot: string;
  allowlistDigest: string;
  trackedFilesOnly: boolean;
  maxFileBytes: number;
  maxTotalBytes: number;
  maxFileCount: number;
}): string {
  return digestJson({
    ignorePolicy: ignorePolicyDigest(options.repositoryRoot),
    allowlistDigest: options.allowlistDigest,
    trackedFilesOnly: options.trackedFilesOnly,
    maxFileBytes: options.maxFileBytes,
    maxTotalBytes: options.maxTotalBytes,
    maxFileCount: options.maxFileCount
  });
}

type PorcelainPath = { readonly status: string; readonly path: string };

/** Parse `git status --porcelain=v1 -z` into path/status pairs (renames use the destination path). */
export function parsePorcelainStatusZ(status: string): readonly PorcelainPath[] {
  const parts = status.split("\0").filter(Boolean);
  const out: PorcelainPath[] = [];
  for (let index = 0; index < parts.length; index += 1) {
    const record = parts[index]!;
    if (record.length < 3) continue;
    const xy = record.slice(0, 2);
    const pathPart = record.slice(3);
    if (xy.startsWith("R") || xy.startsWith("C")) {
      const destination = parts[index + 1];
      if (destination === undefined) continue;
      out.push({ status: xy, path: normalizeRepoRelativePath(destination) });
      index += 1;
      continue;
    }
    out.push({ status: xy, path: normalizeRepoRelativePath(pathPart) });
  }
  return out;
}

/**
 * Content fingerprint over dirty / untracked / deleted eligible paths.
 * Porcelain status alone is never sufficient for cache validity.
 */
export function computeDirtyContentFingerprint(
  repositoryRoot: string,
  statusPorcelain: string,
  options: {
    trackedFilesOnly?: boolean;
    ignoreRules?: readonly IgnoreRule[];
    maxFileBytes?: number;
  } = {}
): DirtyContentFingerprint {
  const ignoreRules = options.ignoreRules ?? loadFaultlineIgnoreRules(repositoryRoot);
  const trackedFilesOnly = options.trackedFilesOnly === true;
  const maxFileBytes = options.maxFileBytes ?? TURN_SNAPSHOT_MAX_FILE_BYTES;
  const paths: Record<string, { status: string; contentDigest: string }> = {};
  const deletedPaths: string[] = [];

  for (const entry of parsePorcelainStatusZ(statusPorcelain)) {
    if (trackedFilesOnly && entry.status === "??") continue;
    if (pathIsSnapshotExcluded(entry.path, ignoreRules)) continue;
    const absolutePath = join(repositoryRoot, ...entry.path.split("/"));
    const missing = entry.status.includes("D") || !existsSync(absolutePath);
    if (missing) {
      deletedPaths.push(entry.path);
      continue;
    }
    try {
      const stat = lstatSync(absolutePath);
      if (stat.isSymbolicLink() || !stat.isFile()) continue;
      paths[entry.path] = {
        status: entry.status,
        contentDigest: fullFileContentDigest(absolutePath, maxFileBytes)
      };
    } catch (error) {
      if (error instanceof TurnSnapshotError) throw error;
      deletedPaths.push(entry.path);
    }
  }

  deletedPaths.sort((left, right) => left.localeCompare(right));
  const sortedPaths = Object.fromEntries(
    Object.entries(paths).sort(([left], [right]) => left.localeCompare(right))
  );
  return {
    contentFingerprint: digestJson({ paths: sortedPaths, deletedPaths }),
    deletedPaths,
    paths: sortedPaths
  };
}

function gitTreeObjectExists(runGit: TurnSnapshotGitRunner, repositoryRoot: string, treeDigest: string): boolean {
  try {
    // Inspect the quarantine object store (with primary as alternate) so cache
    // reuse sees trees written by prior snapshot captures.
    runGit(
      repositoryRoot,
      ["cat-file", "-e", `${treeDigest}^{tree}`],
      turnSnapshotObjectWriteEnvironment(repositoryRoot)
    );
    return true;
  } catch {
    return false;
  }
}

function listTreePaths(
  runGit: TurnSnapshotGitRunner,
  repositoryRoot: string,
  treeDigest: string
): ReadonlySet<string> {
  const listed = runGit(
    repositoryRoot,
    ["ls-tree", "-r", "--name-only", "-z", treeDigest],
    turnSnapshotObjectWriteEnvironment(repositoryRoot)
  );
  return new Set(splitNullPaths(listed));
}

export type TurnSnapshotIndexDelta = {
  readonly baseTree: string;
  readonly updatePaths: readonly string[];
  readonly removePaths: readonly string[];
};

/**
 * Compute the minimal temp-index updates needed to materialize `planPaths`
 * from `baseTree`. Unchanged paths already present in the base tree are left alone.
 */
export function computeTurnSnapshotIndexDelta(options: {
  readonly baseTree: string;
  readonly basePaths: ReadonlySet<string>;
  readonly planPaths: readonly string[];
  readonly repositoryRoot: string;
  /** Paths whose worktree bytes differ from the last fingerprint / porcelain dirty set. */
  readonly dirtyPaths: ReadonlySet<string>;
}): TurnSnapshotIndexDelta {
  const desiredExisting: string[] = [];
  for (const relativePath of options.planPaths) {
    const absolutePath = join(options.repositoryRoot, ...relativePath.split("/"));
    try {
      const stat = lstatSync(absolutePath);
      if (stat.isSymbolicLink() || !stat.isFile()) continue;
      desiredExisting.push(relativePath);
    } catch {
      // Missing path: treat as removal if present in the base tree.
    }
  }
  const desiredSet = new Set(desiredExisting);
  const removePaths = [...options.basePaths]
    .filter((path) => !desiredSet.has(path))
    .sort((left, right) => left.localeCompare(right));
  const updatePaths = desiredExisting
    .filter((path) => !options.basePaths.has(path) || options.dirtyPaths.has(path))
    .sort((left, right) => left.localeCompare(right));
  return {
    baseTree: options.baseTree,
    updatePaths,
    removePaths
  };
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

function assertNoSnapshotSecrets(
  relativePath: string,
  absolutePath: string,
  bytes: number,
  allowlist: SecretAllowlist
): void {
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
  const fileDigest = fileContentDigest(absolutePath, TURN_SNAPSHOT_SECRET_SCAN_BYTES);
  const { remainingKinds } = filterAllowlistedHighOccurrences(allowlist, relativePath, report, fileDigest);
  if (remainingKinds.length > 0) {
    throw new TurnSnapshotError(
      `Refusing turn-tree snapshot: high-confidence secret material detected in ${relativePath} (rules/kinds: ${remainingKinds.join(", ")}). ` +
        "Exclude the path via .faultlineignore after human review, remove the secret, or add a path+kind+occurrenceDigest " +
        "entry in .faultline-secret-allowlist.json (never a bare path allow)."
    );
  }
  const allowlistedOccurrenceDigests = new Set(
    report.occurrences
      .filter((occurrence) =>
        occurrence.confidence === "HIGH"
        && isHighOccurrenceAllowlisted(
          allowlist,
          relativePath,
          occurrence.kind,
          occurrence.digest,
          fileDigest
        ))
      .map((occurrence) => occurrence.digest)
  );
  for (const match of text.matchAll(/[A-Za-z0-9_+\-\/=]{40,}/g)) {
    const token = match[0];
    if (!isSuspiciousHighEntropyToken(token)) continue;
    const tokenDigest = `sha256:${sha256(token)}` as const;
    // A reviewed HIGH allowlist entry already covers this exact occurrence.
    if (allowlistedOccurrenceDigests.has(tokenDigest)) continue;
    if (isEntropyTokenAllowlisted(allowlist, relativePath, token, fileDigest)) continue;
    throw new TurnSnapshotError(
      `Refusing turn-tree snapshot: high-entropy secret-like token detected in ${relativePath} (rule/kind: HIGH_ENTROPY_TOKEN). ` +
        "Exclude via .faultlineignore after human review, remove the token, or add a digest-bound allowlist entry " +
        "(path + kind HIGH_ENTROPY_TOKEN + occurrenceDigest)."
    );
  }
}

/**
 * Entropy alone is a weak secret signal. Skip path-like strings, lockfile
 * integrity digests, and bare hex object ids so turn capture remains usable
 * on normal application repositories without a broad scan bypass.
 */
function isSuspiciousHighEntropyToken(token: string): boolean {
  if (shannonEntropy(token) < 4.5) return false;
  if (token.includes("/")) return false;
  if (/^sha(?:256|512)-/i.test(token)) return false;
  if (/^[a-f0-9]{40,}$/i.test(token)) return false;
  return true;
}

function loadSessionCache(path: string | undefined): TurnSnapshotSessionCache | null {
  if (path === undefined || !existsSync(path)) return null;
  try {
    return TurnSnapshotSessionCacheSchema.parse(JSON.parse(readFileSync(path, "utf8")));
  } catch {
    return null;
  }
}

function writeSessionCache(path: string | undefined, cache: TurnSnapshotSessionCache): void {
  if (path === undefined) return;
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(cache, null, 2)}\n`, "utf8");
}

/** Sidecar recordings store the session cache beside the ledger JSON. */
export function turnSnapshotSessionCachePath(ledgerPath: string): string {
  return ledgerPath.endsWith(".json")
    ? `${ledgerPath.slice(0, -".json".length)}.turn-snapshot-cache.json`
    : `${ledgerPath}.turn-snapshot-cache.json`;
}

/**
 * Repository-level content-fingerprint cache shared by `fl init` pre-warm and
 * the Codex sidecar. Reuse is still gated by HEAD + policy + dirty fingerprints.
 */
export function turnSnapshotRepoCachePath(repositoryRoot: string): string {
  const root = resolve(repositoryRoot);
  const reportedGitDirectory = defaultTurnSnapshotGitRunner(root, ["rev-parse", "--git-common-dir"]);
  return join(resolve(root, reportedGitDirectory.trim()), "faultline", "turn-snapshot-cache.json");
}

function computePathDigests(
  repositoryRoot: string,
  candidates: readonly SnapshotCandidate[]
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const candidate of candidates) {
    if (!candidate.exists) continue;
    const absolutePath = join(repositoryRoot, ...candidate.relativePath.split("/"));
    out[candidate.relativePath] = fileContentDigest(absolutePath, TURN_SNAPSHOT_SECRET_SCAN_BYTES);
  }
  return out;
}

function pathsNeedingSecretScan(
  pathDigests: Readonly<Record<string, string>>,
  previousPathDigests: Readonly<Record<string, string>> | undefined,
  previousAllowlistDigest: string | undefined,
  allowlistDigest: string
): ReadonlySet<string> | "all" {
  if (previousPathDigests === undefined || previousAllowlistDigest !== allowlistDigest) {
    return "all";
  }
  const needing = new Set<string>();
  for (const [path, digest] of Object.entries(pathDigests)) {
    if (previousPathDigests[path] !== digest) needing.add(path);
  }
  return needing;
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
    allowlist?: SecretAllowlist;
    /** When omitted, load `.faultline-secret-allowlist.json` from the repository. */
    previousPathDigests?: Readonly<Record<string, string>>;
    previousAllowlistDigest?: string;
    allowlistDigest?: string;
    skipSecretScan?: boolean;
  } = {}
): {
  paths: readonly string[];
  warnings: readonly string[];
  pathDigests: Readonly<Record<string, string>>;
  secretScanPathCount: number;
} {
  const trackedFilesOnly = options.trackedFilesOnly === true;
  const maxFileBytes = options.maxFileBytes ?? TURN_SNAPSHOT_MAX_FILE_BYTES;
  const maxTotalBytes = options.maxTotalBytes ?? TURN_SNAPSHOT_MAX_TOTAL_BYTES;
  const maxFileCount = options.maxFileCount ?? TURN_SNAPSHOT_MAX_FILE_COUNT;
  const ignoreRules = loadFaultlineIgnoreRules(repositoryRoot);
  let allowlist = options.allowlist;
  if (allowlist === undefined) {
    try {
      allowlist = loadSecretAllowlist(repositoryRoot);
    } catch (error) {
      throw new TurnSnapshotError(
        `Could not load secret allowlist: ${error instanceof Error ? error.message : String(error)}`
      );
    }
  }
  const allowlistDigest = options.allowlistDigest ?? allowlistConfigDigest(allowlist);

  const tracked = new Set(splitNullPaths(runGit(repositoryRoot, ["ls-files", "-z"])));
  const untracked = trackedFilesOnly
    ? []
    : splitNullPaths(runGit(repositoryRoot, ["ls-files", "-z", "--others", "--exclude-standard"]));

  const warnings: string[] = [];
  const candidates: SnapshotCandidate[] = [];

  for (const relativePath of [...tracked, ...untracked]) {
    const ignored = isIgnoredByRules(relativePath, ignoreRules);
    const protectedDescriptor = isProtectedEnvironmentDescriptorPath(relativePath);
    if (ignored && !protectedDescriptor) continue;
    if (ignored && protectedDescriptor) {
      warnings.push(
        `Protected environment descriptor remains snapshot-eligible despite .faultlineignore: ${relativePath}`
      );
    }
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

  const pathDigests = computePathDigests(repositoryRoot, candidates);
  let secretScanPathCount = 0;
  if (!options.skipSecretScan) {
    const needing = pathsNeedingSecretScan(
      pathDigests,
      options.previousPathDigests,
      options.previousAllowlistDigest,
      allowlistDigest
    );
    for (const candidate of candidates) {
      if (!candidate.exists) continue;
      if (needing !== "all" && !needing.has(candidate.relativePath)) continue;
      secretScanPathCount += 1;
      assertNoSnapshotSecrets(
        candidate.relativePath,
        join(repositoryRoot, ...candidate.relativePath.split("/")),
        candidate.bytes,
        allowlist
      );
    }
  }

  return {
    paths: candidates.map((candidate) => candidate.relativePath).sort((left, right) => left.localeCompare(right)),
    warnings,
    pathDigests,
    secretScanPathCount
  };
}

type WriteThrowawayResult = {
  treeDigest: string;
  warnings: readonly string[];
  pathDigests: Readonly<Record<string, string>>;
  secretScanPathCount: number;
};

function writeThrowawayTreeDigest(
  runGit: TurnSnapshotGitRunner,
  repositoryRoot: string,
  options: CaptureTurnTreeSnapshotOptions,
  scanContext: {
    allowlist: SecretAllowlist;
    allowlistDigest: string;
    previousPathDigests?: Readonly<Record<string, string>>;
    previousAllowlistDigest?: string;
    /** Prefer this tree as the incremental base when it still exists under quarantine. */
    preferredBaseTree?: string;
  },
  statusPorcelain: string
): WriteThrowawayResult {
  const trackedFilesOnly = options.trackedFilesOnly === true;
  const ignoreRules = loadFaultlineIgnoreRules(repositoryRoot);
  // RIG-09: clean worktrees reuse HEAD^{tree} when no tracked path is ignore-filtered.
  // Cap remains on files that must be re-hashed; repository size alone is not a refuse reason.
  if (statusPorcelain.length === 0) {
    const tracked = splitNullPaths(runGit(repositoryRoot, ["ls-files", "-z"]));
    const ignoredTracked = tracked.filter((relativePath) => {
      const ignored = isIgnoredByRules(relativePath, ignoreRules);
      const protectedDescriptor = isProtectedEnvironmentDescriptorPath(relativePath);
      return ignored && !protectedDescriptor;
    });
    if (ignoredTracked.length === 0) {
      const headTree = runGit(repositoryRoot, ["rev-parse", "HEAD^{tree}"]).trim();
      if (!GitObjectIdSchema.safeParse(headTree).success) {
        throw new TurnSnapshotError("Git did not return a valid HEAD tree object id for the clean worktree fast path.");
      }
      return {
        treeDigest: headTree,
        warnings: [
          trackedFilesOnly
            ? "Reused HEAD^{tree} for clean tracked worktree (scale fast path)."
            : "Reused HEAD^{tree} for clean worktree with no ignore-filtered tracked paths."
        ],
        pathDigests: {},
        secretScanPathCount: 0
      };
    }
  }

  // Gates 1–4 run here — before any temporary-index object write.
  const plan = planTurnSnapshotPaths(repositoryRoot, runGit, {
    ...(options.trackedFilesOnly === undefined ? {} : { trackedFilesOnly: options.trackedFilesOnly }),
    ...(options.maxFileBytes === undefined ? {} : { maxFileBytes: options.maxFileBytes }),
    ...(options.maxTotalBytes === undefined ? {} : { maxTotalBytes: options.maxTotalBytes }),
    ...(options.maxFileCount === undefined ? {} : { maxFileCount: options.maxFileCount }),
    allowlist: scanContext.allowlist,
    allowlistDigest: scanContext.allowlistDigest,
    ...(scanContext.previousPathDigests === undefined ? {} : { previousPathDigests: scanContext.previousPathDigests }),
    ...(scanContext.previousAllowlistDigest === undefined
      ? {}
      : { previousAllowlistDigest: scanContext.previousAllowlistDigest })
  });

  const maxFileBytes = options.maxFileBytes ?? TURN_SNAPSHOT_MAX_FILE_BYTES;
  const dirty = computeDirtyContentFingerprint(repositoryRoot, statusPorcelain, {
    trackedFilesOnly,
    ignoreRules,
    maxFileBytes
  });
  const dirtyPaths = new Set(Object.keys(dirty.paths));

  const headTree = runGit(repositoryRoot, ["rev-parse", "HEAD^{tree}"]).trim();
  if (!GitObjectIdSchema.safeParse(headTree).success) {
    throw new TurnSnapshotError("Git did not return a valid HEAD tree object id for incremental turn staging.");
  }
  let baseTree = headTree;
  if (
    typeof scanContext.preferredBaseTree === "string"
    && GitObjectIdSchema.safeParse(scanContext.preferredBaseTree).success
    && gitTreeObjectExists(runGit, repositoryRoot, scanContext.preferredBaseTree)
  ) {
    baseTree = scanContext.preferredBaseTree;
  }

  const basePaths = listTreePaths(runGit, repositoryRoot, baseTree);
  const delta = computeTurnSnapshotIndexDelta({
    baseTree,
    basePaths,
    planPaths: plan.paths,
    repositoryRoot,
    dirtyPaths
  });

  const temporaryIndexPath = join(tmpdir(), `faultline-turn-tree-${randomUUID()}.index`);
  const objectEnv = {
    ...turnSnapshotObjectWriteEnvironment(repositoryRoot),
    GIT_INDEX_FILE: temporaryIndexPath
  };
  try {
    runGit(repositoryRoot, ["read-tree", delta.baseTree], objectEnv);

    for (const relativePath of delta.removePaths) {
      runGit(repositoryRoot, ["update-index", "--force-remove", "--", relativePath], objectEnv);
    }

    for (const relativePath of delta.updatePaths) {
      const absolutePath = join(repositoryRoot, ...relativePath.split("/"));
      const stat = lstatSync(absolutePath);
      if (stat.isSymbolicLink() || !stat.isFile()) {
        throw new TurnSnapshotError(`Refusing turn-tree snapshot: path is not a regular file: ${relativePath}`);
      }
      if (stat.size > maxFileBytes) {
        throw new TurnSnapshotError(
          `Refusing turn-tree snapshot: ${relativePath} is ${stat.size} bytes (max ${maxFileBytes} per file).`
        );
      }
      const mode = (stat.mode & 0o111) !== 0 ? "100755" : "100644";
      // Hash via stdin so path-based clean filters never run.
      const content = readFileSync(absolutePath);
      const blob = runGit(
        repositoryRoot,
        ["hash-object", "-w", "--stdin"],
        objectEnv,
        { input: content }
      ).trim();
      if (!GitObjectIdSchema.safeParse(blob).success) {
        throw new TurnSnapshotError(`git hash-object --stdin did not return a blob id for ${relativePath}`);
      }
      runGit(
        repositoryRoot,
        ["update-index", "--add", "--cacheinfo", `${mode},${blob},${relativePath}`],
        objectEnv
      );
    }

    const treeDigest = runGit(repositoryRoot, ["write-tree"], objectEnv).trim();
    if (!GitObjectIdSchema.safeParse(treeDigest).success) {
      throw new TurnSnapshotError("Git did not return a valid tree object id for this turn tree snapshot.");
    }
    return {
      treeDigest,
      warnings: plan.warnings,
      pathDigests: plan.pathDigests,
      secretScanPathCount: plan.secretScanPathCount
    };
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
 * 4. Secret scan (regex + entropy on eligible buffers; incremental when a
 *    session cache is supplied)
 * Only then: temporary-index incremental staging (`read-tree` + delta
 * `hash-object` / `update-index`) + `write-tree`.
 *
 * Quiescence is proven by dual tree capture with status brackets and bounded
 * retries; exhaustion fails closed rather than returning a torn snapshot.
 * When `sessionCachePath` matches HEAD + policyDigest + dirty content
 * fingerprint (and the cached tree object still exists), the previous tree
 * digest is reused after a delayed content re-check (no dual write-tree).
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
  const trackedFilesOnly = options.trackedFilesOnly === true;
  const maxFileBytes = options.maxFileBytes ?? TURN_SNAPSHOT_MAX_FILE_BYTES;
  const maxTotalBytes = options.maxTotalBytes ?? TURN_SNAPSHOT_MAX_TOTAL_BYTES;
  const maxFileCount = options.maxFileCount ?? TURN_SNAPSHOT_MAX_FILE_COUNT;

  if (!Number.isInteger(maxQuiescenceAttempts) || maxQuiescenceAttempts < 1) {
    throw new TurnSnapshotError("Turn tree snapshot quiescence attempt budget must be a positive integer.");
  }
  if (!Number.isFinite(quiescenceDelayMs) || quiescenceDelayMs < 0) {
    throw new TurnSnapshotError("Turn tree snapshot quiescence delay must be a non-negative number of milliseconds.");
  }

  const requestedRoot = resolve(repository);
  const repositoryRoot = resolve(runGit(requestedRoot, ["rev-parse", "--show-toplevel"]).trim());
  const sampleStatus = (): string => runGit(repositoryRoot, [...STATUS_ARGS]);

  let allowlist: SecretAllowlist;
  try {
    allowlist = loadSecretAllowlist(repositoryRoot);
  } catch (error) {
    throw new TurnSnapshotError(
      `Could not load secret allowlist: ${error instanceof Error ? error.message : String(error)}`
    );
  }
  const allowlistDigest = allowlistConfigDigest(allowlist);
  const policyDigest = snapshotPolicyDigest({
    repositoryRoot,
    allowlistDigest,
    trackedFilesOnly,
    maxFileBytes,
    maxTotalBytes,
    maxFileCount
  });
  const previousCache = loadSessionCache(options.sessionCachePath);
  const ignoreRules = loadFaultlineIgnoreRules(repositoryRoot);

  let acceptedTree: string | null = null;
  let acceptedStatus: string | null = null;
  let acceptedHead: string | null = null;
  let acceptedWarnings: readonly string[] = [];
  let acceptedPathDigests: Readonly<Record<string, string>> = {};
  let acceptedDirtyFingerprint: DirtyContentFingerprint | null = null;

  for (let attempt = 1; attempt <= maxQuiescenceAttempts; attempt += 1) {
    const headBefore = runGit(repositoryRoot, ["rev-parse", "HEAD"]).trim();
    if (!GitObjectIdSchema.safeParse(headBefore).success) {
      throw new TurnSnapshotError("Git did not return a resolvable HEAD commit for this turn tree snapshot.");
    }
    const statusBeforeA = sampleStatus();
    const dirtyBefore = computeDirtyContentFingerprint(repositoryRoot, statusBeforeA, {
      trackedFilesOnly,
      ignoreRules,
      maxFileBytes
    });

    if (
      previousCache !== null
      && previousCache.headCommit === headBefore
      && previousCache.policyDigest === policyDigest
      && previousCache.contentFingerprint === dirtyBefore.contentFingerprint
      && gitTreeObjectExists(runGit, repositoryRoot, previousCache.treeDigest)
    ) {
      sleep(quiescenceDelayMs);
      const statusAfterCache = sampleStatus();
      const headAfterCache = runGit(repositoryRoot, ["rev-parse", "HEAD"]).trim();
      const dirtyAfter = computeDirtyContentFingerprint(repositoryRoot, statusAfterCache, {
        trackedFilesOnly,
        ignoreRules,
        maxFileBytes
      });
      if (
        headAfterCache === headBefore
        && dirtyAfter.contentFingerprint === dirtyBefore.contentFingerprint
        && gitTreeObjectExists(runGit, repositoryRoot, previousCache.treeDigest)
      ) {
        acceptedTree = previousCache.treeDigest;
        acceptedStatus = statusAfterCache;
        acceptedHead = headAfterCache;
        acceptedWarnings = [];
        acceptedPathDigests = previousCache.pathDigests;
        acceptedDirtyFingerprint = dirtyAfter;
        break;
      }
    }

    const preferredBaseTree =
      previousCache !== null
      && previousCache.headCommit === headBefore
      && previousCache.policyDigest === policyDigest
      && gitTreeObjectExists(runGit, repositoryRoot, previousCache.treeDigest)
        ? previousCache.treeDigest
        : undefined;

    const first = writeThrowawayTreeDigest(runGit, repositoryRoot, options, {
      allowlist,
      allowlistDigest,
      ...(previousCache === null
        ? {}
        : {
          previousPathDigests: previousCache.pathDigests,
          previousAllowlistDigest: previousCache.allowlistDigest
        }),
      ...(preferredBaseTree === undefined ? {} : { preferredBaseTree })
    }, statusBeforeA);
    const statusAfterA = sampleStatus();
    if (statusBeforeA !== statusAfterA) {
      continue;
    }
    const dirtyAfterA = computeDirtyContentFingerprint(repositoryRoot, statusAfterA, {
      trackedFilesOnly,
      ignoreRules,
      maxFileBytes
    });
    if (dirtyAfterA.contentFingerprint !== dirtyBefore.contentFingerprint) {
      continue;
    }

    sleep(quiescenceDelayMs);

    const statusBeforeB = sampleStatus();
    const second = writeThrowawayTreeDigest(runGit, repositoryRoot, options, {
      allowlist,
      allowlistDigest,
      previousPathDigests: first.pathDigests,
      previousAllowlistDigest: allowlistDigest,
      // Second capture of the same quiescence attempt must use the same base as
      // the first (HEAD or prior accepted tree) — never the first's new tree —
      // so mid-write drift cannot be laundered into a false dual-tree match.
      ...(preferredBaseTree === undefined ? {} : { preferredBaseTree })
    }, statusBeforeB);
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
    const dirtyAfterB = computeDirtyContentFingerprint(repositoryRoot, statusAfterB, {
      trackedFilesOnly,
      ignoreRules,
      maxFileBytes
    });
    if (dirtyAfterB.contentFingerprint !== dirtyBefore.contentFingerprint) {
      continue;
    }

    acceptedTree = second.treeDigest;
    acceptedStatus = statusAfterB;
    acceptedHead = headAfter;
    acceptedWarnings = second.warnings;
    acceptedPathDigests = second.pathDigests;
    acceptedDirtyFingerprint = dirtyAfterB;
    break;
  }

  if (acceptedTree === null || acceptedStatus === null || acceptedHead === null || acceptedDirtyFingerprint === null) {
    throw new TurnSnapshotError(
      `FaultLine could not prove turn-tree quiescence after ${maxQuiescenceAttempts} dual-tree attempt(s) and refused a torn turn tree snapshot.`
    );
  }

  for (const warning of acceptedWarnings) {
    options.onWarning?.(warning);
  }

  const capturedAt = now().toISOString();
  writeSessionCache(options.sessionCachePath, {
    schemaVersion: "faultline.turn-snapshot-cache.v2",
    headCommit: acceptedHead,
    treeDigest: acceptedTree,
    policyDigest,
    allowlistDigest,
    contentFingerprint: acceptedDirtyFingerprint.contentFingerprint,
    deletedPaths: [...acceptedDirtyFingerprint.deletedPaths],
    paths: { ...acceptedDirtyFingerprint.paths },
    pathDigests: { ...acceptedPathDigests },
    capturedAt
  });

  const snapshot = signTurnTreeSnapshot({
    schemaVersion: TURN_TREE_SNAPSHOT_VERSION,
    repositoryRoot,
    headCommit: acceptedHead,
    treeDigest: acceptedTree,
    capturedAt,
    dirty: acceptedStatus.length > 0,
    statusDigest: `sha256:${sha256(acceptedStatus)}`
  });
  return { snapshot, warnings: acceptedWarnings };
}
