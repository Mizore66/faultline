import { randomUUID } from "node:crypto";
import { lstatSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  defaultTurnSnapshotGitRunner,
  planTurnSnapshotPaths,
  turnSnapshotObjectWriteEnvironment,
  type TurnSnapshotGitRunner
} from "../../src/turn-snapshot.js";

/**
 * From-scratch oracle: stage every eligible existing path into a fresh empty
 * temp index via hash-object --stdin (no prior turn tree). Slow but exact.
 */
export function oracleFullStageTreeDigest(
  repositoryRoot: string,
  runGit: TurnSnapshotGitRunner = defaultTurnSnapshotGitRunner
): string {
  const plan = planTurnSnapshotPaths(repositoryRoot, runGit, { skipSecretScan: true });
  const temporaryIndexPath = join(tmpdir(), `faultline-oracle-${randomUUID()}.index`);
  const objectEnv = {
    ...turnSnapshotObjectWriteEnvironment(repositoryRoot),
    GIT_INDEX_FILE: temporaryIndexPath
  };
  try {
    for (const relativePath of plan.paths) {
      const absolutePath = join(repositoryRoot, ...relativePath.split("/"));
      let stat;
      try {
        stat = lstatSync(absolutePath);
      } catch {
        continue;
      }
      if (stat.isSymbolicLink() || !stat.isFile()) continue;
      const mode = (stat.mode & 0o111) !== 0 ? "100755" : "100644";
      const blob = runGit(
        repositoryRoot,
        ["hash-object", "-w", "--stdin"],
        objectEnv,
        { input: readFileSync(absolutePath) }
      ).trim();
      runGit(
        repositoryRoot,
        ["update-index", "--add", "--cacheinfo", `${mode},${blob},${relativePath}`],
        objectEnv
      );
    }
    return runGit(repositoryRoot, ["write-tree"], objectEnv).trim();
  } finally {
    rmSync(temporaryIndexPath, { force: true });
  }
}
