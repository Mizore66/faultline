import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { chmodSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { sha256 } from "../src/canonical.js";
import { computeEnvironmentFingerprint } from "../src/environment-fingerprint.js";
import { STARTER_FAULTLINEIGNORE } from "../src/project-init.js";
import { fileContentDigest, SECRET_ALLOWLIST_SCHEMA_VERSION } from "../src/secret-allowlist.js";
import { purgeFaultlineSnapshotObjects } from "../src/snapshot-gc.js";
import {
  TURN_SNAPSHOT_MAX_QUIESCENCE_ATTEMPTS,
  TURN_SNAPSHOT_QUIESCENCE_DELAY_MS,
  TURN_TREE_SNAPSHOT_VERSION,
  captureTurnTreeSnapshot,
  defaultTurnSnapshotGitRunner,
  faultlineSnapshotObjectDirectory,
  primaryGitObjectDirectory,
  signTurnTreeSnapshot,
  verifyTurnTreeSnapshot,
  TurnSnapshotError,
  type CaptureTurnTreeSnapshotOptions,
  type TurnSnapshotGitRunner,
  type TurnTreeSnapshot
} from "../src/turn-snapshot.js";

function countLooseObjects(objectsDirectory: string): number {
  if (!existsSync(objectsDirectory)) return 0;
  let count = 0;
  for (const entry of readdirSync(objectsDirectory, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    if (entry.name === "info" || entry.name === "pack" || entry.name === "faultline") continue;
    if (!/^[0-9a-f]{2}$/i.test(entry.name)) continue;
    count += readdirSync(join(objectsDirectory, entry.name)).filter((name) => !name.endsWith(".tmp")).length;
  }
  return count;
}

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

function capture(repository: string, options?: CaptureTurnTreeSnapshotOptions): TurnTreeSnapshot {
  return captureTurnTreeSnapshot(repository, options).snapshot;
}

function realIndexBytes(repository: string): Buffer {
  return readFileSync(join(repository, ".git", "index"));
}

describe("Turn tree snapshot capture", () => {
  it("captures a real tree digest for a clean worktree and signs/verifies it", () => {
    const repository = repositoryFixture();
    try {
      const snapshot = capture(repository, { now: () => new Date("2026-07-17T00:00:00.000Z") });
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

      const result = captureTurnTreeSnapshot(repository);
      expect(result.snapshot.dirty).toBe(true);
      expect(result.warnings.some((warning) => warning.includes("untracked.txt"))).toBe(true);
      expect(result.snapshot.treeDigest).toMatch(/^[a-f0-9]{40}$/);
      expect(result.snapshot.treeDigest).not.toBe(git(repository, ["rev-parse", "HEAD^{tree}"]));

      const lsTree = git(repository, ["ls-tree", "-r", "--name-only", result.snapshot.treeDigest]);
      expect(lsTree.split("\n").sort()).toEqual(["tracked.txt", "untracked.txt"]);
      const trackedBlob = git(repository, ["show", `${result.snapshot.treeDigest}:tracked.txt`]);
      expect(trackedBlob).toBe("modified by the turn");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("excludes .faultline/, node_modules/, build caches, and .env-shaped secret files", () => {
    const repository = repositoryFixture();
    try {
      mkdirSync(join(repository, ".faultline"));
      writeFileSync(join(repository, ".faultline", "recording.json"), "{}", "utf8");
      mkdirSync(join(repository, "node_modules", "some-pkg"), { recursive: true });
      writeFileSync(join(repository, "node_modules", "some-pkg", "index.js"), "module.exports = {};\n", "utf8");
      mkdirSync(join(repository, "dist"), { recursive: true });
      writeFileSync(join(repository, "dist", "bundle.js"), "console.log(1);\n", "utf8");
      writeFileSync(join(repository, ".env"), "SECRET=do-not-capture\n", "utf8");
      writeFileSync(join(repository, "included.txt"), "keep me\n", "utf8");

      const snapshot = capture(repository);
      const lsTree = git(repository, ["ls-tree", "-r", "--name-only", snapshot.treeDigest]);
      const paths = lsTree.split("\n").filter(Boolean);
      expect(paths).toContain("tracked.txt");
      expect(paths).toContain("included.txt");
      expect(paths.some((path) => path.startsWith(".faultline/"))).toBe(false);
      expect(paths.some((path) => path.startsWith("node_modules/"))).toBe(false);
      expect(paths.some((path) => path.startsWith("dist/"))).toBe(false);
      expect(paths).not.toContain(".env");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("honors .faultlineignore before writing any Git objects", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, ".faultlineignore"), "scratch/\nsecret-notes.txt\n", "utf8");
      mkdirSync(join(repository, "scratch"), { recursive: true });
      writeFileSync(join(repository, "scratch", "tmp.txt"), "ignore me\n", "utf8");
      writeFileSync(join(repository, "secret-notes.txt"), "ignore me too\n", "utf8");
      writeFileSync(join(repository, "kept.txt"), "visible\n", "utf8");

      const snapshot = capture(repository);
      const paths = git(repository, ["ls-tree", "-r", "--name-only", snapshot.treeDigest]).split("\n").filter(Boolean);
      expect(paths).toContain("tracked.txt");
      expect(paths).toContain("kept.txt");
      expect(paths).not.toContain("secret-notes.txt");
      expect(paths.some((path) => path.startsWith("scratch/"))).toBe(false);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("rejects oversized files before git add writes blobs", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, "huge.bin"), Buffer.alloc(2_048));
      expect(() => capture(repository, { maxFileBytes: 1_024 })).toThrow(/max 1024 per file|exceed/i);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("rejects high-confidence secrets before snapshot acceptance", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, "leaked.txt"), "token = sk-proj-abcdefghijklmnopqrstuvwxyz012345\n", "utf8");
      expect(() => capture(repository)).toThrow(/secret material|high-entropy/i);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("supports tracked-files-only mode and skips untracked paths", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, "tracked.txt"), "modified\n", "utf8");
      writeFileSync(join(repository, "untracked-only.txt"), "should not appear\n", "utf8");
      const result = captureTurnTreeSnapshot(repository, { trackedFilesOnly: true });
      const paths = git(repository, ["ls-tree", "-r", "--name-only", result.snapshot.treeDigest]).split("\n").filter(Boolean);
      expect(paths).toEqual(["tracked.txt"]);
      expect(result.warnings.some((warning) => warning.includes("untracked-only.txt"))).toBe(false);
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

      capture(repository);

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
        const runGit: TurnSnapshotGitRunner = (root, args, env, options) => {
          if (args[0] === "status") {
            statusCalls += 1;
            return statusCalls % 2 === 1 ? "" : "?? changed-mid-write.txt\0";
          }
          return defaultTurnSnapshotGitRunner(root, args, env, options);
        };
        return { runGit, calls: () => statusCalls };
      };

      const first = torn();
      expect(() => capture(repository, {
        runGit: first.runGit,
        sleep: () => {},
        maxQuiescenceAttempts: 2
      })).toThrow(TurnSnapshotError);
      expect(first.calls()).toBe(4);

      const second = torn();
      expect(() => capture(repository, {
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
      // Dirty the worktree so the clean HEAD^{tree} fast path does not skip write-tree.
      writeFileSync(join(repository, "tracked.txt"), "dirty-for-torn-tree-test\n", "utf8");
      let writeTreeCalls = 0;
      const runGit: TurnSnapshotGitRunner = (root, args, env, options) => {
        if (args[0] === "write-tree") {
          writeTreeCalls += 1;
          return writeTreeCalls % 2 === 1 ? "a".repeat(40) : "b".repeat(40);
        }
        if (args[0] === "add") return "";
        return defaultTurnSnapshotGitRunner(root, args, env, options);
      };

      expect(() => capture(repository, {
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
      expect(() => capture(repository, {
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
      const snapshot = capture(repository, {
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
      const snapshot = capture(repository);
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

  it("reports path and rule kind when rejecting secrets", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, "leaked.txt"), "token = sk-proj-abcdefghijklmnopqrstuvwxyz012345\n", "utf8");
      expect(() => capture(repository)).toThrow(/leaked\.txt.*OPENAI_API_KEY|OPENAI_API_KEY.*leaked\.txt/is);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("allows a digest-bound secret allowlist entry and rejects wrong digests", () => {
    const repository = repositoryFixture();
    try {
      const token = "sk-proj-abcdefghijklmnopqrstuvwxyz012345";
      const content = `token = ${token}\n`;
      writeFileSync(join(repository, "fixture-key.txt"), content, "utf8");
      const absolute = join(repository, "fixture-key.txt");
      writeFileSync(join(repository, ".faultline-secret-allowlist.json"), `${JSON.stringify({
        schemaVersion: SECRET_ALLOWLIST_SCHEMA_VERSION,
        entries: [{
          path: "fixture-key.txt",
          kind: "OPENAI_API_KEY",
          occurrenceDigest: `sha256:${sha256(token)}`,
          fileDigest: fileContentDigest(absolute, 256 * 1024),
          note: "Reviewed test fixture"
        }]
      }, null, 2)}\n`, "utf8");
      const snapshot = capture(repository);
      expect(snapshot.dirty).toBe(true);
      expect(git(repository, ["ls-tree", "-r", "--name-only", snapshot.treeDigest])).toContain("fixture-key.txt");

      writeFileSync(join(repository, ".faultline-secret-allowlist.json"), `${JSON.stringify({
        schemaVersion: SECRET_ALLOWLIST_SCHEMA_VERSION,
        entries: [{
          path: "fixture-key.txt",
          kind: "OPENAI_API_KEY",
          occurrenceDigest: `sha256:${"0".repeat(64)}`
        }]
      }, null, 2)}\n`, "utf8");
      expect(() => capture(repository)).toThrow(/OPENAI_API_KEY|secret material/i);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("reuses a session cache only when dirty-path content fingerprints match", () => {
    const repository = repositoryFixture();
    // Cache must live outside the worktree so writing it does not change porcelain status.
    const cachePath = join(tmpdir(), `faultline-cache-${Date.now()}.turn-snapshot-cache.json`);
    try {
      writeFileSync(join(repository, "tracked.txt"), "dirty once\n", "utf8");
      const first = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      expect(existsSync(cachePath)).toBe(true);
      expect(JSON.parse(readFileSync(cachePath, "utf8")).schemaVersion).toBe("faultline.turn-snapshot-cache.v2");

      let writeTreeCalls = 0;
      const runGit: TurnSnapshotGitRunner = (root, args, env, options) => {
        if (args[0] === "write-tree") writeTreeCalls += 1;
        return defaultTurnSnapshotGitRunner(root, args, env, options);
      };
      const second = capture(repository, { sessionCachePath: cachePath, sleep: () => {}, runGit });
      expect(second.treeDigest).toBe(first.treeDigest);
      expect(writeTreeCalls).toBe(0);
    } finally {
      rmSync(repository, { recursive: true, force: true });
      rmSync(cachePath, { force: true });
    }
  });

  it("does not reuse cache when a modified tracked file keeps the same status but changes contents", () => {
    const repository = repositoryFixture();
    const cachePath = join(tmpdir(), `faultline-cache-content-${Date.now()}.json`);
    try {
      writeFileSync(join(repository, "tracked.txt"), "version A\n", "utf8");
      const first = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });

      writeFileSync(join(repository, "tracked.txt"), "version B — still modified, different bytes\n", "utf8");
      let writeTreeCalls = 0;
      const runGit: TurnSnapshotGitRunner = (root, args, env, options) => {
        if (args[0] === "write-tree") writeTreeCalls += 1;
        return defaultTurnSnapshotGitRunner(root, args, env, options);
      };
      const second = capture(repository, { sessionCachePath: cachePath, sleep: () => {}, runGit });
      expect(second.treeDigest).not.toBe(first.treeDigest);
      expect(writeTreeCalls).toBeGreaterThan(0);
      const blob = git(repository, ["show", `${second.treeDigest}:tracked.txt`]);
      expect(blob).toContain("version B");
    } finally {
      rmSync(repository, { recursive: true, force: true });
      rmSync(cachePath, { force: true });
    }
  });

  it("does not reuse cache when an untracked file keeps the same path but changes contents", () => {
    const repository = repositoryFixture();
    const cachePath = join(tmpdir(), `faultline-cache-untracked-${Date.now()}.json`);
    try {
      writeFileSync(join(repository, "generated-config.json"), "{\"v\":1}\n", "utf8");
      const first = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      writeFileSync(join(repository, "generated-config.json"), "{\"v\":2,\"changed\":true}\n", "utf8");
      const second = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      expect(second.treeDigest).not.toBe(first.treeDigest);
      expect(git(repository, ["show", `${second.treeDigest}:generated-config.json`])).toContain("\"v\":2");
    } finally {
      rmSync(repository, { recursive: true, force: true });
      rmSync(cachePath, { force: true });
    }
  });

  it("invalidates session cache when .faultlineignore or secret allowlist changes", () => {
    const repository = repositoryFixture();
    const cachePath = join(tmpdir(), `faultline-cache-policy-${Date.now()}.json`);
    try {
      writeFileSync(join(repository, "tracked.txt"), "dirty\n", "utf8");
      writeFileSync(join(repository, "scratch.txt"), "keep\n", "utf8");
      // Keep allowlist out of the turn tree so policy-only changes are isolated.
      writeFileSync(join(repository, ".faultlineignore"), ".faultline-secret-allowlist.json\n", "utf8");
      writeFileSync(join(repository, ".faultline-secret-allowlist.json"), `${JSON.stringify({
        schemaVersion: SECRET_ALLOWLIST_SCHEMA_VERSION,
        entries: []
      })}\n`, "utf8");
      const first = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      const policyFirst = JSON.parse(readFileSync(cachePath, "utf8")).policyDigest as string;

      writeFileSync(
        join(repository, ".faultlineignore"),
        ".faultline-secret-allowlist.json\nscratch.txt\n",
        "utf8"
      );
      let writeTreeCalls = 0;
      const runGit: TurnSnapshotGitRunner = (root, args, env, options) => {
        if (args[0] === "write-tree") writeTreeCalls += 1;
        return defaultTurnSnapshotGitRunner(root, args, env, options);
      };
      const afterIgnore = capture(repository, { sessionCachePath: cachePath, sleep: () => {}, runGit });
      expect(afterIgnore.treeDigest).not.toBe(first.treeDigest);
      expect(writeTreeCalls).toBeGreaterThan(0);
      expect(git(repository, ["ls-tree", "-r", "--name-only", afterIgnore.treeDigest])).not.toContain("scratch.txt");

      writeTreeCalls = 0;
      writeFileSync(join(repository, ".faultline-secret-allowlist.json"), `${JSON.stringify({
        schemaVersion: SECRET_ALLOWLIST_SCHEMA_VERSION,
        entries: [{
          path: "never-matches.txt",
          kind: "OPENAI_API_KEY",
          occurrenceDigest: `sha256:${"a".repeat(64)}`
        }]
      })}\n`, "utf8");
      const afterAllowlist = capture(repository, { sessionCachePath: cachePath, sleep: () => {}, runGit });
      const policyAfterAllowlist = JSON.parse(readFileSync(cachePath, "utf8")).policyDigest as string;
      expect(policyAfterAllowlist).not.toBe(policyFirst);
      expect(writeTreeCalls).toBeGreaterThan(0);
      // Worktree contents unchanged aside from ignored allowlist → same tree as afterIgnore.
      expect(afterAllowlist.treeDigest).toBe(afterIgnore.treeDigest);
    } finally {
      rmSync(repository, { recursive: true, force: true });
      rmSync(cachePath, { force: true });
    }
  });

  it("invalidates session cache when tracked-files-only mode changes", () => {
    const repository = repositoryFixture();
    const cachePath = join(tmpdir(), `faultline-cache-mode-${Date.now()}.json`);
    try {
      writeFileSync(join(repository, "tracked.txt"), "dirty\n", "utf8");
      writeFileSync(join(repository, "untracked-only.txt"), "noise\n", "utf8");
      const all = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      const trackedOnly = capture(repository, {
        sessionCachePath: cachePath,
        sleep: () => {},
        trackedFilesOnly: true
      });
      expect(trackedOnly.treeDigest).not.toBe(all.treeDigest);
      expect(git(repository, ["ls-tree", "-r", "--name-only", trackedOnly.treeDigest])).not.toContain("untracked-only.txt");
    } finally {
      rmSync(repository, { recursive: true, force: true });
      rmSync(cachePath, { force: true });
    }
  });

  it("records distinct trees across delete and recreate of the same path", () => {
    const repository = repositoryFixture();
    const cachePath = join(tmpdir(), `faultline-cache-del-${Date.now()}.json`);
    try {
      writeFileSync(join(repository, "ephemeral.txt"), "first\n", "utf8");
      const withFile = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      rmSync(join(repository, "ephemeral.txt"), { force: true });
      const deleted = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      expect(deleted.treeDigest).not.toBe(withFile.treeDigest);
      writeFileSync(join(repository, "ephemeral.txt"), "recreated\n", "utf8");
      const recreated = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      expect(recreated.treeDigest).not.toBe(deleted.treeDigest);
      expect(recreated.treeDigest).not.toBe(withFile.treeDigest);
      expect(git(repository, ["show", `${recreated.treeDigest}:ephemeral.txt`])).toBe("recreated");
    } finally {
      rmSync(repository, { recursive: true, force: true });
      rmSync(cachePath, { force: true });
    }
  });

  it("keeps lockfiles snapshot-eligible even when .faultlineignore lists them", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, ".faultlineignore"), "pnpm-lock.yaml\npackage.json\n", "utf8");
      writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n", "utf8");
      writeFileSync(join(repository, "package.json"), "{\"name\":\"demo\",\"version\":\"1.0.0\"}\n", "utf8");
      const warnings: string[] = [];
      const snapshot = capture(repository, {
        sleep: () => {},
        onWarning: (warning) => warnings.push(warning)
      });
      const paths = git(repository, ["ls-tree", "-r", "--name-only", snapshot.treeDigest]).split("\n");
      expect(paths).toContain("pnpm-lock.yaml");
      expect(paths).toContain("package.json");
      expect(warnings.some((warning) => /Protected environment descriptor/i.test(warning))).toBe(true);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("quarantines snapshot blobs outside .git/objects and gc leaves primary loose objects unchanged", () => {
    const repository = repositoryFixture();
    try {
      const primaryObjects = primaryGitObjectDirectory(repository);
      const beforeLoose = countLooseObjects(primaryObjects);
      writeFileSync(join(repository, "tracked.txt"), "quarantine-me\n", "utf8");
      writeFileSync(join(repository, "brand-new.txt"), "new blob for quarantine\n", "utf8");

      const snapshot = capture(repository, { sleep: () => {} });
      expect(snapshot.treeDigest).toMatch(/^[a-f0-9]{40}$/);
      expect(existsSync(faultlineSnapshotObjectDirectory(repository))).toBe(true);
      expect(countLooseObjects(primaryObjects)).toBe(beforeLoose);
      expect(countLooseObjects(faultlineSnapshotObjectDirectory(repository))).toBeGreaterThan(0);

      const gc = purgeFaultlineSnapshotObjects(repository);
      expect(gc.status).toBe("PURGED");
      expect(existsSync(faultlineSnapshotObjectDirectory(repository))).toBe(false);
      expect(countLooseObjects(primaryObjects)).toBe(beforeLoose);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("refuses snapshot gc when a .faultline ledger still references quarantined trees unless --force", () => {
    const repository = repositoryFixture();
    try {
      writeFileSync(join(repository, "tracked.txt"), "gc-ref-safety\n", "utf8");
      const snapshot = capture(repository, { sleep: () => {} });
      expect(existsSync(faultlineSnapshotObjectDirectory(repository))).toBe(true);

      const recordings = join(repository, ".faultline", "recordings");
      mkdirSync(recordings, { recursive: true });
      const ledgerPath = join(recordings, "session-ledger.json");
      writeFileSync(
        ledgerPath,
        `${JSON.stringify({
          schemaVersion: "faultline.codex-lifecycle-ledger.v1",
          note: "test fixture retaining a quarantined turn tree",
          treeDigest: snapshot.treeDigest
        }, null, 2)}\n`,
        "utf8"
      );

      expect(() => purgeFaultlineSnapshotObjects(repository)).toThrow(/Refusing to purge|referenced|\.faultline/i);
      expect(() => purgeFaultlineSnapshotObjects(repository)).toThrow(/recordings\/session-ledger\.json/);
      expect(existsSync(faultlineSnapshotObjectDirectory(repository))).toBe(true);

      const forced = purgeFaultlineSnapshotObjects(repository, { force: true });
      expect(forced.status).toBe("PURGED");
      expect(forced.referencingPaths.some((path) => path.includes("session-ledger.json"))).toBe(true);
      expect(existsSync(faultlineSnapshotObjectDirectory(repository))).toBe(false);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("starter ignore does not suppress lockfiles, and lockfile edits change environment fingerprints", () => {
    const repository = repositoryFixture();
    try {
      expect(STARTER_FAULTLINEIGNORE).not.toMatch(/^pnpm-lock\.yaml$/m);
      expect(STARTER_FAULTLINEIGNORE).not.toMatch(/^package-lock\.json$/m);
      writeFileSync(join(repository, ".faultlineignore"), STARTER_FAULTLINEIGNORE, "utf8");
      writeFileSync(join(repository, "package.json"), "{\"name\":\"demo\"}\n", "utf8");
      writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\nA\n", "utf8");
      git(repository, ["add", "package.json", "pnpm-lock.yaml", ".faultlineignore"]);
      git(repository, ["commit", "-m", "lock A"]);

      const baselineFp = computeEnvironmentFingerprint(repository);
      const snapA = capture(repository, { sleep: () => {} });
      expect(git(repository, ["ls-tree", "-r", "--name-only", snapA.treeDigest])).toContain("pnpm-lock.yaml");

      writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\nB-changed\n", "utf8");
      const snapB = capture(repository, { sleep: () => {} });
      expect(snapB.treeDigest).not.toBe(snapA.treeDigest);
      expect(git(repository, ["show", `${snapB.treeDigest}:pnpm-lock.yaml`])).toContain("B-changed");
      const turnFp = computeEnvironmentFingerprint(repository);
      expect(turnFp.digest).not.toBe(baselineFp.digest);
      expect(turnFp.files["pnpm-lock.yaml"]).not.toBe(baselineFp.files["pnpm-lock.yaml"]);
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("stages dirty trees incrementally without git add of the full eligible set", () => {
    const repository = repositoryFixture();
    try {
      for (let index = 0; index < 40; index += 1) {
        writeFileSync(join(repository, `bulk-${index}.txt`), `bulk ${index}\n`, "utf8");
      }
      git(repository, ["add", "."]);
      git(repository, ["commit", "-m", "bulk"]);
      writeFileSync(join(repository, "tracked.txt"), "one dirty edit\n", "utf8");

      const commands: string[] = [];
      let hashObjectCalls = 0;
      let updateIndexCalls = 0;
      const runGit: TurnSnapshotGitRunner = (root, args, env, options) => {
        commands.push(args[0] ?? "");
        if (args[0] === "hash-object") hashObjectCalls += 1;
        if (args[0] === "update-index") updateIndexCalls += 1;
        return defaultTurnSnapshotGitRunner(root, args, env, options);
      };
      const snapshot = capture(repository, { sleep: () => {}, runGit });
      expect(snapshot.dirty).toBe(true);
      expect(commands).toContain("read-tree");
      expect(commands).not.toContain("add");
      // Dual-tree quiescence runs staging twice; only the dirty path is hashed each time.
      expect(hashObjectCalls).toBe(2);
      expect(updateIndexCalls).toBe(2);
      expect(git(repository, ["show", `${snapshot.treeDigest}:tracked.txt`])).toBe("one dirty edit");
      expect(git(repository, ["ls-tree", "-r", "--name-only", snapshot.treeDigest]).split("\n")).toContain("bulk-0.txt");
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("does not invoke repository clean filters while staging a dirty turn tree", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-turn-filter-"));
    const repository = join(root, "repo");
    try {
      mkdirSync(repository);
      git(repository, ["init"]);
      git(repository, ["config", "user.email", "turn@faultline.test"]);
      git(repository, ["config", "user.name", "FaultLine Turn"]);
      writeFileSync(join(repository, "app.js"), "console.log(1);\n", "utf8");
      git(repository, ["add", "."]);
      git(repository, ["commit", "-m", "base"]);

      const filterScript = join(root, "evil-clean.cjs");
      writeFileSync(
        filterScript,
        [
          "let data = Buffer.alloc(0);",
          "process.stdin.on('data', (chunk) => { data = Buffer.concat([data, chunk]); });",
          "process.stdin.on('end', () => { process.stdout.write(Buffer.concat([Buffer.from('CLEANED:'), data])); });",
          ""
        ].join("\n"),
        "utf8"
      );
      git(repository, ["config", "filter.faultline-evil.clean", `node "${filterScript}"`]);
      writeFileSync(join(repository, ".gitattributes"), "*.js filter=faultline-evil\n", "utf8");
      writeFileSync(join(repository, "app.js"), "console.log(2);\n", "utf8");

      const commands: string[] = [];
      const runGit: TurnSnapshotGitRunner = (repoRoot, args, env, options) => {
        commands.push(args[0] ?? "");
        return defaultTurnSnapshotGitRunner(repoRoot, args, env, options);
      };
      const snapshot = capture(repository, { sleep: () => {}, runGit });
      expect(commands).not.toContain("add");
      // Path-based clean filters would prefix CLEANED:; stdin hashing must preserve bytes.
      expect(git(repository, ["show", `${snapshot.treeDigest}:app.js`])).toBe("console.log(2);");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("removes newly ignored tracked paths via incremental index delta", () => {
    const repository = repositoryFixture();
    const cachePath = join(tmpdir(), `faultline-cache-ignore-delta-${Date.now()}.json`);
    try {
      writeFileSync(join(repository, "noise.tmp"), "noise\n", "utf8");
      git(repository, ["add", "noise.tmp"]);
      git(repository, ["commit", "-m", "noise"]);
      writeFileSync(join(repository, "tracked.txt"), "dirty\n", "utf8");
      const before = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      expect(git(repository, ["ls-tree", "-r", "--name-only", before.treeDigest])).toContain("noise.tmp");

      writeFileSync(join(repository, ".faultlineignore"), "noise.tmp\n", "utf8");
      // Worktree content for noise.tmp is unchanged; policy must force removal.
      const after = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
      expect(git(repository, ["ls-tree", "-r", "--name-only", after.treeDigest])).not.toContain("noise.tmp");
      expect(git(repository, ["show", `${after.treeDigest}:tracked.txt`])).toBe("dirty");
    } finally {
      rmSync(repository, { recursive: true, force: true });
      rmSync(cachePath, { force: true });
    }
  });

  describe("restored-clean staleness (preferredBaseTree P0)", () => {
    it("restores a previously dirty tracked file to HEAD bytes when another file stays dirty", () => {
      const repository = repositoryFixture();
      const cachePath = join(tmpdir(), `faultline-cache-restore-${Date.now()}.json`);
      try {
        const headBytes = "stable\n";
        expect(readFileSync(join(repository, "tracked.txt"), "utf8")).toBe(headBytes);
        writeFileSync(join(repository, "tracked.txt"), "dirty at turn N\n", "utf8");
        writeFileSync(join(repository, "other.txt"), "other dirty\n", "utf8");
        const turnN = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${turnN.treeDigest}:tracked.txt`])).toBe("dirty at turn N");

        writeFileSync(join(repository, "tracked.txt"), headBytes, "utf8");
        writeFileSync(join(repository, "other.txt"), "other still dirty\n", "utf8");
        const turnN1 = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${turnN1.treeDigest}:tracked.txt`])).toBe("stable");
        expect(git(repository, ["show", `${turnN1.treeDigest}:other.txt`])).toBe("other still dirty");
        expect(turnN1.treeDigest).not.toBe(turnN.treeDigest);
      } finally {
        rmSync(repository, { recursive: true, force: true });
        rmSync(cachePath, { force: true });
      }
    });

    it("restores file A to HEAD while file B remains dirty", () => {
      const repository = repositoryFixture();
      const cachePath = join(tmpdir(), `faultline-cache-restore-ab-${Date.now()}.json`);
      try {
        writeFileSync(join(repository, "a.txt"), "a-head\n", "utf8");
        writeFileSync(join(repository, "b.txt"), "b-head\n", "utf8");
        git(repository, ["add", "a.txt", "b.txt"]);
        git(repository, ["commit", "-m", "a and b"]);

        writeFileSync(join(repository, "a.txt"), "a-dirty\n", "utf8");
        writeFileSync(join(repository, "b.txt"), "b-dirty\n", "utf8");
        const turnN = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${turnN.treeDigest}:a.txt`])).toBe("a-dirty");
        expect(git(repository, ["show", `${turnN.treeDigest}:b.txt`])).toBe("b-dirty");

        writeFileSync(join(repository, "a.txt"), "a-head\n", "utf8");
        writeFileSync(join(repository, "b.txt"), "b-still-dirty\n", "utf8");
        const turnN1 = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${turnN1.treeDigest}:a.txt`])).toBe("a-head");
        expect(git(repository, ["show", `${turnN1.treeDigest}:b.txt`])).toBe("b-still-dirty");
      } finally {
        rmSync(repository, { recursive: true, force: true });
        rmSync(cachePath, { force: true });
      }
    });

    it("restores executable bit to HEAD mode after a dirty turn", () => {
      const repository = repositoryFixture();
      const cachePath = join(tmpdir(), `faultline-cache-restore-mode-${Date.now()}.json`);
      try {
        writeFileSync(join(repository, "script.sh"), "#!/bin/sh\necho ok\n", "utf8");
        git(repository, ["add", "script.sh"]);
        git(repository, ["commit", "-m", "script"]);
        const headMode = git(repository, ["ls-tree", "HEAD", "script.sh"]).slice(0, 6);
        expect(headMode).toBe("100644");

        writeFileSync(join(repository, "script.sh"), "#!/bin/sh\necho dirty\n", "utf8");
        chmodSync(join(repository, "script.sh"), 0o755);
        writeFileSync(join(repository, "other.txt"), "anchor dirty\n", "utf8");
        const turnN = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        const modeN = git(repository, ["ls-tree", turnN.treeDigest, "script.sh"]).slice(0, 6);

        writeFileSync(join(repository, "script.sh"), "#!/bin/sh\necho ok\n", "utf8");
        chmodSync(join(repository, "script.sh"), 0o644);
        writeFileSync(join(repository, "other.txt"), "anchor still dirty\n", "utf8");
        const turnN1 = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${turnN1.treeDigest}:script.sh`])).toBe("#!/bin/sh\necho ok");
        expect(git(repository, ["ls-tree", turnN1.treeDigest, "script.sh"]).slice(0, 6)).toBe("100644");
        if (modeN === "100755") {
          expect(turnN1.treeDigest).not.toBe(turnN.treeDigest);
        }
      } finally {
        rmSync(repository, { recursive: true, force: true });
        rmSync(cachePath, { force: true });
      }
    });

    it("records distinct trees across PASS → FAIL → PASS content revert", () => {
      const repository = repositoryFixture();
      const cachePath = join(tmpdir(), `faultline-cache-pfp-${Date.now()}.json`);
      try {
        writeFileSync(join(repository, "noise.tmp"), "noise\n", "utf8");
        git(repository, ["add", "noise.tmp"]);
        git(repository, ["commit", "-m", "noise"]);
        writeFileSync(join(repository, ".faultlineignore"), "noise.tmp\n", "utf8");
        git(repository, ["add", ".faultlineignore"]);
        git(repository, ["commit", "-m", "ignore noise"]);

        const baseline = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${baseline.treeDigest}:tracked.txt`])).toBe("stable");

        writeFileSync(join(repository, "tracked.txt"), "breaking edit\n", "utf8");
        const fail = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(fail.treeDigest).not.toBe(baseline.treeDigest);
        expect(git(repository, ["show", `${fail.treeDigest}:tracked.txt`])).toBe("breaking edit");

        writeFileSync(join(repository, "tracked.txt"), "stable\n", "utf8");
        expect(git(repository, ["status", "--porcelain"])).toBe("");
        const pass = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${pass.treeDigest}:tracked.txt`])).toBe("stable");
        expect(pass.treeDigest).not.toBe(fail.treeDigest);
        // HEAD content under the same ignore policy: tracked.txt only (noise.tmp omitted).
        expect(pass.treeDigest).toBe(baseline.treeDigest);
      } finally {
        rmSync(repository, { recursive: true, force: true });
        rmSync(cachePath, { force: true });
      }
    });

    it("restores cleaned tracked bytes when porcelain is clean but ignore-filtered tracked paths force the slow path", () => {
      const repository = repositoryFixture();
      const cachePath = join(tmpdir(), `faultline-cache-ignore-slow-${Date.now()}.json`);
      try {
        writeFileSync(join(repository, "noise.tmp"), "noise\n", "utf8");
        git(repository, ["add", "noise.tmp"]);
        git(repository, ["commit", "-m", "noise"]);
        writeFileSync(join(repository, ".faultlineignore"), "noise.tmp\n", "utf8");
        git(repository, ["add", ".faultlineignore"]);
        git(repository, ["commit", "-m", "ignore noise"]);

        writeFileSync(join(repository, "tracked.txt"), "dirty turn N\n", "utf8");
        const turnN = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${turnN.treeDigest}:tracked.txt`])).toBe("dirty turn N");
        expect(git(repository, ["ls-tree", "-r", "--name-only", turnN.treeDigest])).not.toContain("noise.tmp");

        writeFileSync(join(repository, "tracked.txt"), "stable\n", "utf8");
        // Porcelain is fully clean; ignored tracked noise.tmp forces non-RIG-09 staging.
        expect(git(repository, ["status", "--porcelain"])).toBe("");
        const turnN1 = capture(repository, { sessionCachePath: cachePath, sleep: () => {} });
        expect(git(repository, ["show", `${turnN1.treeDigest}:tracked.txt`])).toBe("stable");
        expect(turnN1.treeDigest).not.toBe(turnN.treeDigest);
      } finally {
        rmSync(repository, { recursive: true, force: true });
        rmSync(cachePath, { force: true });
      }
    });
  });
});
