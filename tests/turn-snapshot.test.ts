import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import {
  TURN_SNAPSHOT_MAX_QUIESCENCE_ATTEMPTS,
  TURN_SNAPSHOT_QUIESCENCE_DELAY_MS,
  TURN_TREE_SNAPSHOT_VERSION,
  captureTurnTreeSnapshot,
  defaultTurnSnapshotGitRunner,
  signTurnTreeSnapshot,
  verifyTurnTreeSnapshot,
  TurnSnapshotError,
  type TurnSnapshotGitRunner
} from "../src/turn-snapshot.js";

function git(repository: string, args: string[]): string {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
  return String(result.stdout ?? "").trim();
}

function repositoryFixture(): string {
  const repository = mkdtempSync(join(tmpdir(), "faultline-turn-snapshot-"));
  git(repository, ["init"]);
  git(repository, ["config", "user.email", "faultline@example.invalid"]);
  git(repository, ["config", "user.name", "FaultLine turn snapshot test"]);
  writeFileSync(join(repository, "tracked.txt"), "stable\n", "utf8");
  git(repository, ["add", "tracked.txt"]);
  git(repository, ["commit", "-m", "clean fixture"]);
  return repository;
}

function realIndexBytes(repository: string): Buffer {
  return readFileSync(join(repository, ".git", "index"));
}

describe("Turn tree snapshot capture", () => {
  it("captures a real tree digest for a clean worktree and signs/verifies it", () => {
    const repository = repositoryFixture();
    try {
      const snapshot = captureTurnTreeSnapshot(repository, { now: () => new Date("2026-07-17T00:00:00.000Z") });
      expect(snapshot.schemaVersion).toBe(TURN_TREE_SNAPSHOT_VERSION);
      expect(snapshot.dirty).toBe(false);
      expect(snapshot.headCommit).toMatch(/^[a-f0-9]{40}$/);
      expect(snapshot.treeDigest).toMatch(/^[a-f0-9]{40}$/);
      expect(snapshot.statusDigest).toMatch(/^sha256:[a-f0-9]{64}$/);
      expect(snapshot.capturedAt).toBe("2026-07-17T00:00:00.000Z");
      expect(verifyTurnTreeSnapshot(snapshot)).toEqual([]);
      expect(snapshot.treeDigest).toBe(git(repository, ["rev-parse", "HEAD^{tree}"]));
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("still produces a real tree digest for a dirty worktree with a modified tracked file", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, "tracked.txt"), "modified by the turn\n", "utf8");
      writeFileSync(join(repository, "untracked.txt"), "new from the turn\n", "utf8");

      const snapshot = captureTurnTreeSnapshot(repository);
      expect(snapshot.dirty).toBe(true);
      expect(snapshot.treeDigest).toMatch(/^[a-f0-9]{40}$/);
      // The dirty tree must differ from HEAD's tree: it reflects the actual
      // modified/untracked content, not merely a copy of the last commit.
      expect(snapshot.treeDigest).not.toBe(git(repository, ["rev-parse", "HEAD^{tree}"]));

      const lsTree = git(repository, ["ls-tree", "-r", "--name-only", snapshot.treeDigest]);
      expect(lsTree.split("\n").sort()).toEqual(["tracked.txt", "untracked.txt"]);
      const trackedBlob = git(repository, ["show", `${snapshot.treeDigest}:tracked.txt`]);
      expect(trackedBlob).toBe("modified by the turn");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("excludes .faultline/, node_modules/, and .env-shaped secret files from the captured tree", () => {
    const repository = repositoryFixture();
    try {
      mkdirSync(join(repository, ".faultline"));
      writeFileSync(join(repository, ".faultline", "recording.json"), "{}", "utf8");
      mkdirSync(join(repository, "node_modules", "some-pkg"), { recursive: true });
      writeFileSync(join(repository, "node_modules", "some-pkg", "index.js"), "module.exports = {};\n", "utf8");
      writeFileSync(join(repository, ".env"), "SECRET=do-not-capture\n", "utf8");
      writeFileSync(join(repository, "included.txt"), "keep me\n", "utf8");

      const snapshot = captureTurnTreeSnapshot(repository);
      const lsTree = git(repository, ["ls-tree", "-r", "--name-only", snapshot.treeDigest]);
      const paths = lsTree.split("\n").filter(Boolean);
      expect(paths).toContain("tracked.txt");
      expect(paths).toContain("included.txt");
      expect(paths.some((path) => path.startsWith(".faultline/"))).toBe(false);
      expect(paths.some((path) => path.startsWith("node_modules/"))).toBe(false);
      expect(paths).not.toContain(".env");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("leaves the user's real Git index and HEAD completely unchanged after capture", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, "tracked.txt"), "modified but never staged\n", "utf8");
      writeFileSync(join(repository, "untracked.txt"), "also never staged\n", "utf8");
      const indexBefore = realIndexBytes(repository);
      const headBefore = git(repository, ["rev-parse", "HEAD"]);
      const branchBefore = git(repository, ["rev-parse", "--abbrev-ref", "HEAD"]);
      const statusBefore = git(repository, ["status", "--porcelain=v1", "--untracked-files=all"]);

      captureTurnTreeSnapshot(repository);

      expect(realIndexBytes(repository)).toEqual(indexBefore);
      expect(git(repository, ["rev-parse", "HEAD"])).toBe(headBefore);
      expect(git(repository, ["rev-parse", "--abbrev-ref", "HEAD"])).toBe(branchBefore);
      expect(git(repository, ["status", "--porcelain=v1", "--untracked-files=all"])).toBe(statusBefore);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("refuses a torn snapshot when the porcelain status changes around a tree capture", () => {
    const repository = repositoryFixture();
    try {
      const torn = (): { runGit: TurnSnapshotGitRunner; calls: () => number } => {
        let statusCalls = 0;
        const runGit: TurnSnapshotGitRunner = (root, args, env) => {
          if (args[0] === "status") {
            statusCalls += 1;
            // Alternate so every capture bracket sees a status change.
            return statusCalls % 2 === 1 ? "" : "?? changed-mid-write.txt\0";
          }
          return defaultTurnSnapshotGitRunner(root, args, env);
        };
        return { runGit, calls: () => statusCalls };
      };

      const first = torn();
      expect(() => captureTurnTreeSnapshot(repository, {
        runGit: first.runGit,
        sleep: () => {},
        maxQuiescenceAttempts: 2
      })).toThrow(TurnSnapshotError);
      // Two attempts × (status before + status after) around the first tree.
      expect(first.calls()).toBe(4);

      const second = torn();
      expect(() => captureTurnTreeSnapshot(repository, {
        runGit: second.runGit,
        sleep: () => {},
        maxQuiescenceAttempts: 2
      })).toThrow(/quiescence|torn/i);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("refuses a torn snapshot when consecutive tree digests disagree even if status is unchanged", () => {
    const repository = repositoryFixture();
    try {
      let writeTreeCalls = 0;
      const runGit: TurnSnapshotGitRunner = (root, args, env) => {
        if (args[0] === "write-tree") {
          writeTreeCalls += 1;
          // Distinct valid-looking tree ids so quiescence never converges.
          return writeTreeCalls % 2 === 1 ? "a".repeat(40) : "b".repeat(40);
        }
        if (args[0] === "add") return "";
        return defaultTurnSnapshotGitRunner(root, args, env);
      };

      expect(() => captureTurnTreeSnapshot(repository, {
        runGit,
        sleep: () => {},
        maxQuiescenceAttempts: 3
      })).toThrow(/quiescence|torn/i);
      expect(writeTreeCalls).toBe(6);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("refuses a snapshot when the worktree mutates during the quiescence delay", () => {
    const repository = repositoryFixture();
    try {
      expect(() => captureTurnTreeSnapshot(repository, {
        maxQuiescenceAttempts: 2,
        sleep: () => {
          writeFileSync(join(repository, "tracked.txt"), `mutated-${Date.now()}\n`, "utf8");
        }
      })).toThrow(/quiescence|torn/i);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("accepts only after two consecutive tree digests match", () => {
    const repository = repositoryFixture();
    try {
      let sleepCalls = 0;
      const snapshot = captureTurnTreeSnapshot(repository, {
        sleep: (milliseconds) => {
          sleepCalls += 1;
          expect(milliseconds).toBe(TURN_SNAPSHOT_QUIESCENCE_DELAY_MS);
        }
      });
      expect(sleepCalls).toBe(1);
      expect(snapshot.dirty).toBe(false);
      expect(snapshot.treeDigest).toBe(git(repository, ["rev-parse", "HEAD^{tree}"]));
      expect(TURN_SNAPSHOT_MAX_QUIESCENCE_ATTEMPTS).toBeGreaterThanOrEqual(2);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("rejects a signed snapshot whose digest was tampered with", () => {
    const repository = repositoryFixture();
    try {
      const snapshot = captureTurnTreeSnapshot(repository);
      const tampered = { ...snapshot, dirty: !snapshot.dirty };
      expect(verifyTurnTreeSnapshot(tampered)).toEqual(["Turn tree snapshot digest does not match its contents"]);
      expect(verifyTurnTreeSnapshot({ ...snapshot, digest: "not-a-digest" }).length).toBeGreaterThan(0);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("round-trips signTurnTreeSnapshot deterministically for identical inputs", () => {
    const unsigned = {
      schemaVersion: TURN_TREE_SNAPSHOT_VERSION,
      repositoryRoot: "C:/work/faultline",
      headCommit: "a".repeat(40),
      treeDigest: "b".repeat(40),
      capturedAt: "2026-07-17T00:00:00.000Z",
      dirty: false,
      statusDigest: `sha256:${"c".repeat(64)}`
    } as const;
    const first = signTurnTreeSnapshot(unsigned);
    const second = signTurnTreeSnapshot(unsigned);
    expect(first.digest).toBe(second.digest);
    expect(verifyTurnTreeSnapshot(first)).toEqual([]);
  });
});
