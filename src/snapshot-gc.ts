import { existsSync, lstatSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import {
  FAULTLINE_SNAPSHOT_ALTERNATES_ENTRY,
  faultlineSnapshotObjectDirectory,
  primaryGitObjectDirectory
} from "./turn-snapshot.js";

export type SnapshotGcResult = {
  readonly repositoryRoot: string;
  readonly objectDirectory: string;
  readonly removedEntries: number;
  readonly status: "PURGED" | "ALREADY_EMPTY";
};

function countEntriesRecursive(directory: string): number {
  if (!existsSync(directory)) return 0;
  let count = 0;
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      count += countEntriesRecursive(path);
      continue;
    }
    if (entry.isFile()) count += 1;
  }
  return count;
}

function removeAlternatesEntry(repositoryRoot: string): void {
  const alternatesPath = join(primaryGitObjectDirectory(repositoryRoot), "info", "alternates");
  if (!existsSync(alternatesPath)) return;
  const stat = lstatSync(alternatesPath);
  if (stat.isSymbolicLink() || !stat.isFile()) {
    throw new Error(`Refusing to edit non-regular Git alternates file: ${alternatesPath}`);
  }
  const remaining = readFileSync(alternatesPath, "utf8")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && line !== FAULTLINE_SNAPSHOT_ALTERNATES_ENTRY);
  if (remaining.length === 0) {
    rmSync(alternatesPath, { force: true });
    return;
  }
  writeFileSync(alternatesPath, `${remaining.join("\n")}\n`, "utf8");
}

/**
 * Purge the quarantined turn-snapshot object store at `.git/faultline/objects`
 * and detach it from the primary object database alternates list.
 */
export function purgeFaultlineSnapshotObjects(repository: string): SnapshotGcResult {
  const repositoryRoot = resolve(repository);
  const objectDirectory = faultlineSnapshotObjectDirectory(repositoryRoot);
  if (!existsSync(objectDirectory)) {
    removeAlternatesEntry(repositoryRoot);
    return {
      repositoryRoot,
      objectDirectory,
      removedEntries: 0,
      status: "ALREADY_EMPTY"
    };
  }
  const removedEntries = countEntriesRecursive(objectDirectory);
  rmSync(objectDirectory, { recursive: true, force: true });
  removeAlternatesEntry(repositoryRoot);
  return {
    repositoryRoot,
    objectDirectory,
    removedEntries,
    status: removedEntries === 0 ? "ALREADY_EMPTY" : "PURGED"
  };
}
