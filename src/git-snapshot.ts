import { mkdirSync, writeFileSync } from "node:fs";
import { basename, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { digestJson } from "./canonical.js";

export type GitSidecarSnapshot = {
  schemaVersion: "faultline.git-sidecar.v1";
  recorder: "git-sidecar";
  repositoryName: string;
  headCommit: string;
  treeDigest: string;
  capturedAt: string;
  clean: true;
  digest: string;
};

function runGit(repository: string, args: string[]): string {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.error || result.status !== 0) {
    const detail = `${result.stderr ?? ""}${result.error ? result.error.message : ""}`.trim();
    throw new Error(`Git sidecar capture failed for ${args.join(" ")}: ${detail || `exit ${result.status ?? "unknown"}`}`);
  }
  return String(result.stdout ?? "").trim();
}

export function captureCleanGitSnapshot(repository: string): GitSidecarSnapshot {
  const requestedRoot = resolve(repository);
  const repoRoot = runGit(requestedRoot, ["rev-parse", "--show-toplevel"]);
  const status = runGit(repoRoot, ["status", "--porcelain=v1", "--untracked-files=all"]);
  if (status) {
    throw new Error("Git sidecar capture requires a clean worktree. Commit, stash, or explicitly model changes before recording evidence.");
  }
  const headCommit = runGit(repoRoot, ["rev-parse", "HEAD"]);
  const treeDigest = runGit(repoRoot, ["rev-parse", "HEAD^{tree}"]);
  const unsigned = {
    schemaVersion: "faultline.git-sidecar.v1" as const,
    recorder: "git-sidecar" as const,
    repositoryName: basename(repoRoot),
    headCommit,
    treeDigest,
    capturedAt: new Date().toISOString(),
    clean: true as const
  };
  return { ...unsigned, digest: digestJson(unsigned) };
}

export function writeGitSidecarSnapshot(repository: string, snapshot: GitSidecarSnapshot): string {
  const outputDirectory = join(resolve(repository), ".faultline", "recordings");
  mkdirSync(outputDirectory, { recursive: true });
  const file = join(outputDirectory, `${snapshot.digest.replace("sha256:", "")}.json`);
  writeFileSync(file, `${JSON.stringify(snapshot, null, 2)}\n`, "utf8");
  return file;
}
