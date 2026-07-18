import { cpSync, existsSync, lstatSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { hardenedGitText, runHardenedGit, toGitPath } from "./git-materialization.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";

const GIT_OBJECT_ID = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/;

export type RepairWorktreeSession = {
  readonly repairId: string;
  readonly managedRoot: string;
  readonly worktreePath: string;
  readonly proofReadonlyPath: string;
  readonly instructionsPath: string;
  readonly artifactsPath: string;
  readonly baseCommit: string;
  readonly repository: string;
};

export type RepairWorktreeCleanupResult = {
  readonly cleaned: boolean;
  readonly errors: readonly string[];
};

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function assertManagedChild(root: string, candidate: string, label: string): string {
  const nested = relativeTrustedSystemPath(root, candidate);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error(`${label} must be a child of the managed repair root.`);
  }
  return resolve(candidate);
}

function assertNotSymlink(path: string, label: string): void {
  if (!existsSync(path)) return;
  const stat = lstatSync(path);
  if (stat.isSymbolicLink()) throw new Error(`${label} must not be a symbolic link: ${path}`);
}

/**
 * Create a detached Git worktree at the selected repair base commit under a
 * managed repair directory. Proof bytes are copied beside the worktree as a
 * read-only companion tree (never written into tracked paths).
 */
export async function createRepairWorktree(options: {
  readonly repository: string;
  readonly baseCommit: string;
  readonly bundleDirectory: string;
  readonly outputDirectory: string;
  readonly repairId?: string;
}): Promise<RepairWorktreeSession> {
  if (!GIT_OBJECT_ID.test(options.baseCommit)) {
    throw new Error(`Repair base commit is not a Git object id: ${options.baseCommit}`);
  }
  const repository = resolve(options.repository);
  const repoRoot = await hardenedGitText(repository, ["rev-parse", "--show-toplevel"]);
  if (!isAbsolute(repoRoot)) throw new Error("Git did not return an absolute repository root.");
  const absoluteRepo = resolve(repoRoot);

  const repairId = options.repairId ?? randomUUID().slice(0, 12);
  const managedRoot = resolve(options.outputDirectory);
  const parent = dirname(managedRoot);
  if (!existsSync(parent)) mkdirSync(parent, { recursive: true, mode: 0o700 });
  const safeParent = resolveSafeDirectorySegment(parent);
  if (safeParent === null) throw new Error(`Repair output parent is not a real directory: ${parent}`);
  if (existsSync(managedRoot)) {
    throw new Error(`Repair output already exists and will not be replaced: ${managedRoot}`);
  }
  mkdirSync(managedRoot, { recursive: false, mode: 0o700 });
  const safeRoot = resolveSafeDirectorySegment(managedRoot);
  if (safeRoot === null) throw new Error(`Repair managed root is not a real directory: ${managedRoot}`);

  const worktreePath = assertManagedChild(safeRoot, join(safeRoot, "worktree"), "repair worktree");
  const proofReadonlyPath = assertManagedChild(safeRoot, join(safeRoot, "proof-ro"), "proof mount");
  const instructionsPath = assertManagedChild(safeRoot, join(safeRoot, "instructions"), "instructions");
  const artifactsPath = assertManagedChild(safeRoot, join(safeRoot, "artifacts"), "artifacts");
  for (const path of [instructionsPath, artifactsPath]) {
    mkdirSync(path, { recursive: false, mode: 0o700 });
  }

  const verifyCommit = await runHardenedGit(absoluteRepo, [
    "rev-parse",
    "--verify",
    "--end-of-options",
    `${options.baseCommit}^{commit}`
  ]);
  if (verifyCommit.exitCode !== 0 || verifyCommit.error !== undefined) {
    rmSync(safeRoot, { recursive: true, force: true });
    throw new Error(`Repair base commit is not available in the repository: ${options.baseCommit}`);
  }

  const add = await runHardenedGit(absoluteRepo, [
    "worktree",
    "add",
    "--detach",
    toGitPath(worktreePath),
    options.baseCommit
  ]);
  if (add.exitCode !== 0 || add.error !== undefined) {
    rmSync(safeRoot, { recursive: true, force: true });
    throw new Error(
      `Could not create detached repair worktree: ${add.stderr.toString("utf8").trim() || add.error || "Git failed."}`
    );
  }
  assertNotSymlink(worktreePath, "repair worktree");

  try {
    cpSync(resolve(options.bundleDirectory), proofReadonlyPath, {
      recursive: true,
      dereference: false,
      errorOnExist: true,
      force: false
    });

    const pointerDir = join(worktreePath, ".faultline-repair");
    mkdirSync(pointerDir, { mode: 0o700 });
    writeFileSync(join(pointerDir, "README.md"), [
      "# FaultLine isolated repair worktree",
      "",
      `Base commit: ${options.baseCommit}`,
      `Proof (read-only companion): ${proofReadonlyPath}`,
      `Instructions: ${instructionsPath}`,
      "",
      "Do not commit `.faultline-repair/`. Produce a minimal patch that restores the frozen witness.",
      ""
    ].join("\n"), "utf8");
  } catch (error) {
    await removeRepairWorktree({ repository: absoluteRepo, worktreePath, managedRoot: safeRoot });
    throw new Error(
      error instanceof Error && error.message.startsWith("Could not")
        ? error.message
        : `Could not finish repair worktree setup: ${errorMessage(error)}`
    );
  }

  return {
    repairId,
    managedRoot: safeRoot,
    worktreePath,
    proofReadonlyPath,
    instructionsPath,
    artifactsPath,
    baseCommit: options.baseCommit,
    repository: absoluteRepo
  };
}

/**
 * Remove a registered detached worktree and prune leftovers. Always safe to
 * call from a finally block; returns cleanup errors instead of throwing.
 */
export async function removeRepairWorktree(options: {
  readonly repository: string;
  readonly worktreePath: string;
  readonly managedRoot?: string;
  readonly keepManagedRoot?: boolean;
}): Promise<RepairWorktreeCleanupResult> {
  const errors: string[] = [];
  const repository = resolve(options.repository);
  const worktreePath = resolve(options.worktreePath);

  if (existsSync(worktreePath)) {
    const removed = await runHardenedGit(repository, ["worktree", "remove", "--force", toGitPath(worktreePath)]);
    if (removed.exitCode !== 0 || removed.error !== undefined) {
      errors.push(
        `git worktree remove failed: ${removed.stderr.toString("utf8").trim() || removed.error || "Git failed."}`
      );
      try {
        rmSync(worktreePath, { recursive: true, force: true });
      } catch (error) {
        errors.push(`Could not delete worktree directory after remove failure: ${errorMessage(error)}`);
      }
    }
  }

  const pruned = await runHardenedGit(repository, ["worktree", "prune"]);
  if (pruned.exitCode !== 0 || pruned.error !== undefined) {
    errors.push(`git worktree prune failed: ${pruned.stderr.toString("utf8").trim() || pruned.error || "Git failed."}`);
  }

  if (options.managedRoot && options.keepManagedRoot !== true) {
    const managedRoot = resolve(options.managedRoot);
    if (existsSync(managedRoot)) {
      try {
        // Keep instructions/artifacts by removing only the worktree child if present;
        // for full cleanup of an aborted session, delete the whole managed root when
        // the caller did not request keepManagedRoot and no artifacts were published.
        const worktreeChild = join(managedRoot, "worktree");
        if (existsSync(worktreeChild)) {
          rmSync(worktreeChild, { recursive: true, force: true });
        }
      } catch (error) {
        errors.push(`Could not remove leftover worktree directory: ${errorMessage(error)}`);
      }
    }
  }

  return { cleaned: errors.length === 0, errors };
}

/** Collect a binary-capable patch from the isolated repair worktree. */
export async function collectRepairPatch(worktreePath: string): Promise<{ patch: string; bytes: number }> {
  const absolute = resolve(worktreePath);
  const diff = await runHardenedGit(absolute, [
    "diff",
    "--binary",
    "--full-index",
    "--no-ext-diff",
    "--no-textconv",
    "--no-renames"
  ]);
  if (diff.exitCode !== 0 || diff.error !== undefined) {
    throw new Error(`Could not collect repair patch: ${diff.stderr.toString("utf8").trim() || diff.error || "Git failed."}`);
  }
  const patch = diff.stdout.toString("utf8");
  return { patch, bytes: Buffer.byteLength(patch, "utf8") };
}

export function relativeInside(root: string, candidate: string): string {
  return relative(resolve(root), resolve(candidate)).replaceAll("\\", "/");
}
