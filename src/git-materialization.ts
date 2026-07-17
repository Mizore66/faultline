import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { lstat, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { sha256 } from "./canonical.js";
import type { FrozenWitness } from "./witness-lock.js";

export const GIT_MATERIALIZATION_TIMEOUT_MS = 30_000;
export const GIT_MATERIALIZATION_MAX_OUTPUT_BYTES = 64 * 1024 * 1024;

export const MATERIALIZATION_SAFE_GIT_CONFIG = Object.freeze([
  "-c", "core.hooksPath=/nonexistent/faultline-hooks",
  "-c", "core.fsmonitor=false",
  "-c", "core.useBuiltinFSMonitor=false",
  "-c", "core.untrackedCache=false",
  "-c", "core.preloadIndex=false",
  "-c", "core.autocrlf=false",
  "-c", "core.sparseCheckout=false",
  "-c", "core.sparseCheckoutCone=false",
  "-c", "filter.lfs.process=",
  "-c", "filter.lfs.smudge=",
  "-c", "filter.lfs.clean=",
  "-c", "filter.lfs.required=false",
  "-c", "diff.external=",
  "-c", "submodule.recurse=false",
  "-c", "fetch.recurseSubmodules=false",
  "-c", "protocol.allow=never",
  "-c", "protocol.file.allow=never",
  "-c", "protocol.ext.allow=never",
  "-c", "protocol.git.allow=never",
  "-c", "protocol.ssh.allow=never",
  "-c", "protocol.http.allow=never",
  "-c", "protocol.https.allow=never"
]);

const SAFE_OVERLAY_PATH = /^[^\\/\0]+(?:\/[^\\/\0]+)*$/;

export type HardenedGitResult = {
  readonly exitCode: number | null;
  readonly stdout: Buffer;
  readonly stderr: Buffer;
  readonly error?: string;
};

export type MaterializedOverlay = {
  readonly path: string;
  readonly bytesDigest: string;
  readonly bytesLength: number;
};

export type HardenedGitOptions = {
  readonly stdin?: Buffer;
  readonly gitDir?: string;
  readonly worktree?: string;
  readonly indexFile?: string;
};

export type MaterializedGitTree = {
  readonly worktree: string;
  readonly indexFile: string;
  runGit(args: readonly string[], stdin?: Buffer): Promise<HardenedGitResult>;
  cleanup(): Promise<string | null>;
};

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function hardenedGitEnvironment(options: HardenedGitOptions): NodeJS.ProcessEnv {
  const environment: NodeJS.ProcessEnv = {
    PATH: process.env.PATH ?? "",
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: join(tmpdir(), `faultline-empty-git-config-${randomUUID()}`),
    GIT_TERMINAL_PROMPT: "0",
    GIT_OPTIONAL_LOCKS: "0",
    GIT_NO_REPLACE_OBJECTS: "1",
    GIT_LFS_SKIP_SMUDGE: "1",
    GIT_ALLOW_PROTOCOL: "none",
    ...(options.gitDir === undefined ? {} : { GIT_DIR: options.gitDir }),
    ...(options.worktree === undefined ? {} : { GIT_WORK_TREE: options.worktree }),
    ...(options.indexFile === undefined ? {} : { GIT_INDEX_FILE: options.indexFile })
  };
  if (process.platform === "win32") {
    if (process.env.SystemRoot) environment.SystemRoot = process.env.SystemRoot;
    if (process.env.ComSpec) environment.ComSpec = process.env.ComSpec;
    if (process.env.PATHEXT) environment.PATHEXT = process.env.PATHEXT;
  }
  return environment;
}

function boundedAppend(current: Buffer, next: Buffer): Buffer {
  if (current.length >= GIT_MATERIALIZATION_MAX_OUTPUT_BYTES) return current;
  const room = GIT_MATERIALIZATION_MAX_OUTPUT_BYTES - current.length;
  return Buffer.concat([current, next.subarray(0, room)]);
}

/**
 * Run Git without a shell, inherited configuration, transports, or unbounded
 * time/output. Callers may bind a plain worktree and external temporary index.
 */
export async function runHardenedGit(
  repository: string,
  args: readonly string[],
  options: HardenedGitOptions = {}
): Promise<HardenedGitResult> {
  const argumentsList = [
    ...MATERIALIZATION_SAFE_GIT_CONFIG,
    ...(options.gitDir === undefined ? [] : [`--git-dir=${options.gitDir}`]),
    ...(options.worktree === undefined ? [] : [`--work-tree=${options.worktree}`]),
    "-C",
    repository,
    ...args
  ];
  return new Promise((resolveResult) => {
    let stdout: Buffer = Buffer.alloc(0);
    let stderr: Buffer = Buffer.alloc(0);
    let outputExceeded = false;
    let timedOut = false;
    let settled = false;

    const settle = (result: HardenedGitResult): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolveResult(result);
    };

    let child;
    try {
      child = spawn("git", argumentsList, {
        shell: false,
        windowsHide: true,
        stdio: [options.stdin === undefined ? "ignore" : "pipe", "pipe", "pipe"],
        env: hardenedGitEnvironment(options)
      });
    } catch (error) {
      resolveResult({ exitCode: null, stdout, stderr, error: errorMessage(error) });
      return;
    }

    const timer = setTimeout(() => {
      timedOut = true;
      child.kill();
    }, GIT_MATERIALIZATION_TIMEOUT_MS);
    timer.unref();

    const capture = (chunk: Buffer, stream: "stdout" | "stderr"): void => {
      const bytes = Buffer.from(chunk);
      if (stream === "stdout") stdout = boundedAppend(stdout, bytes);
      else stderr = boundedAppend(stderr, bytes);
      if (stdout.length + stderr.length >= GIT_MATERIALIZATION_MAX_OUTPUT_BYTES && !outputExceeded) {
        outputExceeded = true;
        child.kill();
      }
    };

    child.stdout?.on("data", (chunk: Buffer) => capture(chunk, "stdout"));
    child.stderr?.on("data", (chunk: Buffer) => capture(chunk, "stderr"));
    child.once("error", (error) => settle({
      exitCode: null,
      stdout,
      stderr,
      error: errorMessage(error)
    }));
    child.once("close", (exitCode) => {
      const reason = timedOut
        ? "Git command exceeded bounded execution time"
        : outputExceeded
          ? "Git command exceeded bounded output limit"
          : undefined;
      const suffix = reason === undefined ? Buffer.alloc(0) : Buffer.from(`\nFaultLine stopped Git: ${reason}.`, "utf8");
      settle({
        exitCode,
        stdout,
        stderr: boundedAppend(stderr, suffix),
        ...(reason === undefined ? {} : { error: reason })
      });
    });
    if (options.stdin !== undefined) {
      child.stdin?.once("error", () => {
        // Git may close stdin after rejecting a malformed patch.
      });
      child.stdin?.end(options.stdin);
    }
  });
}

export async function hardenedGitText(repository: string, args: readonly string[]): Promise<string> {
  const result = await runHardenedGit(repository, args);
  if (result.exitCode !== 0 || result.error !== undefined) {
    const detail = [result.stderr.toString("utf8").trim(), result.error]
      .filter((value): value is string => Boolean(value))
      .join("; ");
    throw new Error(`Git ${args.join(" ")} failed: ${detail || `exit ${result.exitCode ?? "unknown"}`}`);
  }
  return result.stdout.toString("utf8").trim();
}

/**
 * Reject every configured filter and every committed filter attribute before
 * checkout-index can process repository-controlled bytes on the host.
 */
export async function assertSafeGitMaterialization(repository: string, commits: readonly string[]): Promise<void> {
  const localFilters = await runHardenedGit(repository, ["config", "--local", "--get-regexp", "^filter\\."]);
  if (localFilters.exitCode === 0 && localFilters.stdout.toString("utf8").trim()) {
    throw new Error("Refusing host tree materialization: repository local Git filter configuration is present.");
  }
  if (localFilters.exitCode !== 0 && localFilters.exitCode !== 1) {
    throw new Error(`Could not inspect repository Git filters: ${localFilters.stderr.toString("utf8").trim() || localFilters.error || "Git failed."}`);
  }

  for (const commit of new Set(commits)) {
    const attributes = await runHardenedGit(repository, [
      "grep",
      "-I",
      "-n",
      "-E",
      "(^|[[:space:]])filter(=|[[:space:]]|$)",
      commit,
      "--",
      ":(literal).gitattributes",
      ":(glob)**/.gitattributes"
    ]);
    if (attributes.exitCode === 0) {
      throw new Error(`Refusing host tree materialization: ${commit} declares a Git filter attribute.`);
    }
    if (attributes.exitCode !== 1 || attributes.error !== undefined) {
      throw new Error(`Could not inspect Git attributes for ${commit}: ${attributes.stderr.toString("utf8").trim() || attributes.error || "Git failed."}`);
    }
  }
}

/**
 * Materialize an immutable commit through an external temporary index. The
 * returned directory is deliberately not a registered Git worktree.
 */
export async function materializeGitTree(options: {
  readonly repository: string;
  readonly commit: string;
  readonly tempRoot: string;
  readonly name: string;
}): Promise<MaterializedGitTree> {
  await assertSafeGitMaterialization(options.repository, [options.commit]);
  const worktrees = join(options.tempRoot, "worktrees");
  const indexes = join(options.tempRoot, "indexes");
  const gitDir = await hardenedGitText(options.repository, ["rev-parse", "--absolute-git-dir"]);
  if (!isAbsolute(gitDir)) throw new Error("Git did not return an absolute repository metadata directory.");
  await mkdir(worktrees, { recursive: true, mode: 0o700 });
  await mkdir(indexes, { recursive: true, mode: 0o700 });
  const worktree = join(worktrees, options.name);
  const indexFile = join(indexes, `${randomUUID()}.index`);
  // The sandbox runs as an unprivileged uid against a read-only bind mount.
  await mkdir(worktree, { mode: 0o755 });
  const sessionOptions = { gitDir, worktree, indexFile };

  const cleanup = async (): Promise<string | null> => {
    try {
      await rm(worktree, { recursive: true, force: true });
      await rm(indexFile, { force: true });
      return null;
    } catch (error) {
      return `Could not remove temporary materialized tree: ${errorMessage(error)}`;
    }
  };

  try {
    const readTree = await runHardenedGit(worktree, ["read-tree", "--reset", options.commit], sessionOptions);
    if (readTree.exitCode !== 0 || readTree.error !== undefined) {
      throw new Error(`Could not prepare external Git index for ${options.commit}: ${readTree.stderr.toString("utf8").trim() || readTree.error || "Git failed."}`);
    }
    const checkout = await runHardenedGit(worktree, ["checkout-index", "--all", "--force"], sessionOptions);
    if (checkout.exitCode !== 0 || checkout.error !== undefined) {
      throw new Error(`Could not materialize Git tree ${options.commit}: ${checkout.stderr.toString("utf8").trim() || checkout.error || "Git failed."}`);
    }
    // read-tree creates entries without worktree stat data. Refresh the
    // external index so later `git apply --index` can require an exact match.
    const refresh = await runHardenedGit(worktree, ["update-index", "--refresh"], sessionOptions);
    if (refresh.exitCode !== 0 || refresh.error !== undefined) {
      throw new Error(`Could not verify materialized Git tree ${options.commit}: ${refresh.stderr.toString("utf8").trim() || refresh.error || "Git failed."}`);
    }
    const verification = await runHardenedGit(worktree, ["diff-files", "--quiet", "--"], sessionOptions);
    if (verification.exitCode !== 0 || verification.error !== undefined) {
      throw new Error(`Materialized Git tree does not match its external index for ${options.commit}: ${verification.stderr.toString("utf8").trim() || verification.error || "content mismatch"}`);
    }
    return {
      worktree,
      indexFile,
      runGit(args, stdin) {
        return runHardenedGit(worktree, args, { ...sessionOptions, ...(stdin === undefined ? {} : { stdin }) });
      },
      cleanup
    };
  } catch (error) {
    await cleanup();
    throw error;
  }
}

function sha256Digest(value: Buffer): string {
  return `sha256:${sha256(value)}`;
}

function safeOverlayParts(value: string): string[] {
  if (!SAFE_OVERLAY_PATH.test(value) || value.startsWith("/") || value.includes("\\")) {
    throw new Error(`Frozen overlay path is unsafe: ${value}`);
  }
  const parts = value.split("/");
  if (parts.some((part) => !part || part === "." || part === "..")) {
    throw new Error(`Frozen overlay path is unsafe: ${value}`);
  }
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

async function safeOverlayTarget(worktree: string, overlayPath: string): Promise<string> {
  const parts = safeOverlayParts(overlayPath);
  const target = resolve(worktree, ...parts);
  const containment = relative(worktree, target);
  if (!containment || containment === ".." || containment.startsWith("../") || containment.startsWith("..\\") || isAbsolute(containment)) {
    throw new Error(`Frozen overlay path escaped its worktree: ${overlayPath}`);
  }
  let current = worktree;
  for (const part of parts.slice(0, -1)) {
    current = join(current, part);
    const entry = await lstatIfPresent(current);
    if (entry === null) {
      await mkdir(current, { mode: 0o755 });
      const created = await lstat(current);
      if (!created.isDirectory() || created.isSymbolicLink()) {
        throw new Error(`Could not safely create overlay directory: ${overlayPath}`);
      }
    } else if (!entry.isDirectory() || entry.isSymbolicLink()) {
      throw new Error(`Frozen overlay parent is not a safe directory: ${overlayPath}`);
    }
  }
  const existing = await lstatIfPresent(target);
  if (existing?.isSymbolicLink()) throw new Error(`Frozen overlay target is a symbolic link: ${overlayPath}`);
  return target;
}

export async function materializeFrozenOverlays(
  worktree: string,
  frozenWitness: FrozenWitness
): Promise<MaterializedOverlay[]> {
  const facts: MaterializedOverlay[] = [];
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
