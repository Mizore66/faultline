// Live-oracle ops for the slice 4a snapgc tests: node-oracle.ts spreads them
// into its handlers after its own entries, so each slice 4a task edits only
// its own op file. The Go side is difftest/snapgc_live_test.go.
import { createHash } from "node:crypto";
import { lstatSync, readFileSync, readdirSync, readlinkSync } from "node:fs";
import { join } from "node:path";
import { findQuarantinedObjectReferences, listQuarantinedObjectIds, purgeFaultlineSnapshotObjects } from "../../src/snapshot-gc.js";

// Handler is node-oracle.ts's: it takes the request's args and may return a
// Promise. (Not imported: node-oracle.ts starts reading stdin when loaded.)
type Handler = (args: any) => unknown;

// treeListing lists every entry under root, names in code-unit order: "D
// <rel> <mode>" for a directory ("X <rel>" after it when it cannot be
// listed), "F <rel> <mode> <size> <sha256>" for a file ("?" when it cannot
// be read; "-" past 1 MiB), "L <rel> <target>" for a link, "S <rel> <mode>"
// for anything else. The Go twin is snapgcTreeListing.
function treeListing(root: string): string[] {
  const lines: string[] = [];
  const walk = (rel: string) => {
    let names: string[];
    try {
      names = readdirSync(rel ? join(root, rel) : root);
    } catch {
      lines.push(`X ${rel}`);
      return;
    }
    for (const name of names.sort()) {
      const r = rel ? `${rel}/${name}` : name;
      const path = join(root, r);
      const stat = lstatSync(path);
      const mode = (stat.mode & 0o777).toString(8);
      if (stat.isSymbolicLink()) lines.push(`L ${r} ${readlinkSync(path)}`);
      else if (stat.isDirectory()) { lines.push(`D ${r} ${mode}`); walk(r); }
      else if (!stat.isFile()) lines.push(`S ${r} ${mode}`);
      else {
        let digest = "-";
        if (stat.size <= 1024 * 1024) {
          try { digest = createHash("sha256").update(readFileSync(path)).digest("hex"); } catch { digest = "?"; }
        }
        lines.push(`F ${r} ${mode} ${stat.size} ${digest}`);
      }
    }
  };
  walk("");
  return lines;
}

const outcome = (fn: () => unknown): { value: unknown } | { threw: string } => {
  try {
    return { value: fn() };
  } catch (error) {
    return { threw: (error as Error).message };
  }
};

export const ops: Record<string, Handler> = {
  // purgeFaultlineSnapshotObjects, then the tree under root (the directory
  // holding the repository and whatever its links point to).
  purgeFaultlineSnapshotObjects: ({ repository, force, root }: { repository: string; force: boolean; root: string }) =>
    JSON.stringify({ ...outcome(() => purgeFaultlineSnapshotObjects(repository, { force })), tree: treeListing(root) }),
  listQuarantinedObjectIds: ({ dir }: { dir: string }) => JSON.stringify(outcome(() => listQuarantinedObjectIds(dir))),
  findQuarantinedObjectReferences: ({ repository, ids }: { repository: string; ids: string[] }) =>
    JSON.stringify(outcome(() => findQuarantinedObjectReferences(repository, new Set(ids))))
};
