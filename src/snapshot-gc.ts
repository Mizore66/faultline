import { existsSync, lstatSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import {
  FAULTLINE_SNAPSHOT_ALTERNATES_ENTRY,
  faultlineSnapshotObjectDirectory,
  primaryGitObjectDirectory
} from "./turn-snapshot.js";

export type SnapshotGcOptions = {
  /** When true, purge even if `.faultline` ledgers/turn bundles still reference quarantined objects. */
  readonly force?: boolean;
};

export type SnapshotGcResult = {
  readonly repositoryRoot: string;
  readonly objectDirectory: string;
  readonly removedEntries: number;
  readonly status: "PURGED" | "ALREADY_EMPTY";
  readonly referencingPaths: readonly string[];
};

const GIT_OBJECT_ID = /\b([0-9a-f]{40})\b/gi;

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

/** List loose object IDs stored under the quarantined FaultLine object directory. */
export function listQuarantinedObjectIds(objectDirectory: string): readonly string[] {
  if (!existsSync(objectDirectory)) return [];
  const ids: string[] = [];
  for (const entry of readdirSync(objectDirectory, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    if (entry.name === "info" || entry.name === "pack") continue;
    if (!/^[0-9a-f]{2}$/i.test(entry.name)) continue;
    const prefix = entry.name.toLowerCase();
    const bucket = join(objectDirectory, entry.name);
    for (const name of readdirSync(bucket)) {
      if (name.endsWith(".tmp")) continue;
      const rest = name.toLowerCase();
      if (!/^[0-9a-f]{38}$/.test(rest)) continue;
      ids.push(`${prefix}${rest}`);
    }
  }
  return ids;
}

function walkRegularFiles(directory: string, into: string[] = []): string[] {
  if (!existsSync(directory)) return into;
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    try {
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) continue;
      if (stat.isDirectory()) {
        walkRegularFiles(path, into);
        continue;
      }
      if (stat.isFile()) into.push(path);
    } catch {
      // Ignore raced/unreadable entries under .faultline.
    }
  }
  return into;
}

/**
 * Find `.faultline` ledger / turn-bundle files that still mention quarantined
 * object IDs (typically `treeDigest` values from turn-tree snapshots).
 */
export function findQuarantinedObjectReferences(
  repositoryRoot: string,
  quarantinedObjectIds: ReadonlySet<string>
): readonly string[] {
  if (quarantinedObjectIds.size === 0) return [];
  const faultlineRoot = join(resolve(repositoryRoot), ".faultline");
  if (!existsSync(faultlineRoot)) return [];

  const referencing = new Set<string>();
  for (const filePath of walkRegularFiles(faultlineRoot)) {
    const relativePath = relative(resolve(repositoryRoot), filePath).replaceAll("\\", "/");
    // Ledgers and turn/git proof bundles under .faultline are the retention surface.
    const isLedgerOrBundle = /(?:^|\/)(?:recordings|turn-proof-bundles|git-proof-bundles|bundles|proofs|turn-proofs|git-proofs)\//.test(relativePath)
      || /(?:ledger|investigation|manifest|ROOT\.sha256|hashes\.txt)$/i.test(relativePath)
      || relativePath.endsWith(".json");
    if (!isLedgerOrBundle) continue;

    let body: string;
    try {
      const stat = lstatSync(filePath);
      if (stat.size > 8 * 1024 * 1024) continue;
      body = readFileSync(filePath, "utf8");
    } catch {
      continue;
    }
    GIT_OBJECT_ID.lastIndex = 0;
    for (const match of body.matchAll(GIT_OBJECT_ID)) {
      const id = match[1]?.toLowerCase();
      if (id && quarantinedObjectIds.has(id)) {
        referencing.add(relativePath);
        break;
      }
    }
  }
  return [...referencing].sort((left, right) => left.localeCompare(right));
}

/**
 * Purge the quarantined turn-snapshot object store at `.git/faultline/objects`
 * and detach it from the primary object database alternates list.
 *
 * Before purge, scans `.faultline` for ledger/turn-bundle references to
 * quarantined object IDs. When references remain, refuses unless `force: true`.
 */
export function purgeFaultlineSnapshotObjects(
  repository: string,
  options: SnapshotGcOptions = {}
): SnapshotGcResult {
  const repositoryRoot = resolve(repository);
  const objectDirectory = faultlineSnapshotObjectDirectory(repositoryRoot);
  if (!existsSync(objectDirectory)) {
    removeAlternatesEntry(repositoryRoot);
    return {
      repositoryRoot,
      objectDirectory,
      removedEntries: 0,
      status: "ALREADY_EMPTY",
      referencingPaths: []
    };
  }

  const quarantinedIds = listQuarantinedObjectIds(objectDirectory);
  const referencingPaths = findQuarantinedObjectReferences(
    repositoryRoot,
    new Set(quarantinedIds)
  );
  if (referencingPaths.length > 0 && options.force !== true) {
    const named = referencingPaths.slice(0, 5).join(", ");
    const more = referencingPaths.length > 5 ? ` (+${referencingPaths.length - 5} more)` : "";
    throw new Error(
      `Refusing to purge quarantined turn-snapshot objects: still referenced by ${referencingPaths.length} path(s) under .faultline (${named}${more}). ` +
        "Re-run with --force after confirming those ledgers/turn bundles no longer need the trees, or archive them first."
    );
  }

  const removedEntries = countEntriesRecursive(objectDirectory);
  rmSync(objectDirectory, { recursive: true, force: true });
  removeAlternatesEntry(repositoryRoot);
  return {
    repositoryRoot,
    objectDirectory,
    removedEntries,
    status: removedEntries === 0 ? "ALREADY_EMPTY" : "PURGED",
    referencingPaths
  };
}
