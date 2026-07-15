import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { captureCleanGitSnapshot, writeGitSidecarSnapshot } from "../src/git-snapshot.js";

function git(repository: string, args: string[]): void {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
}

describe("Git sidecar snapshots", () => {
  it("records an immutable clean HEAD tree and refuses a dirty worktree", () => {
    const repository = mkdtempSync(join(tmpdir(), "faultline-git-"));
    try {
      git(repository, ["init"]);
      git(repository, ["config", "user.email", "faultline@example.invalid"]);
      git(repository, ["config", "user.name", "FaultLine test"]);
      writeFileSync(join(repository, "sample.txt"), "stable\n", "utf8");
      git(repository, ["add", "sample.txt"]);
      git(repository, ["commit", "-m", "fixture"]);
      const snapshot = captureCleanGitSnapshot(repository);
      expect(snapshot.clean).toBe(true);
      expect(snapshot.headCommit).toMatch(/^[a-f0-9]{40}$/);
      expect(snapshot.treeDigest).toMatch(/^[a-f0-9]{40}$/);
      expect(snapshot.digest).toMatch(/^sha256:[a-f0-9]{64}$/);
      expect(existsSync(writeGitSidecarSnapshot(repository, snapshot))).toBe(true);
      writeFileSync(join(repository, "untracked.txt"), "dirty\n", "utf8");
      expect(() => captureCleanGitSnapshot(repository)).toThrow(/clean worktree/);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });
});
