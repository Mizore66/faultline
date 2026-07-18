import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { sha256 } from "../src/canonical.js";
import { computeEnvironmentFingerprint } from "../src/environment-fingerprint.js";
import { STARTER_FAULTLINEIGNORE } from "../src/project-init.js";
import { fileContentDigest, SECRET_ALLOWLIST_SCHEMA_VERSION } from "../src/secret-allowlist.js";
import {
  TURN_SNAPSHOT_MAX_QUIESCENCE_ATTEMPTS,
  TURN_SNAPSHOT_QUIESCENCE_DELAY_MS,
  TURN_TREE_SNAPSHOT_VERSION,
  captureTurnTreeSnapshot,
  defaultTurnSnapshotGitRunner,
  signTurnTreeSnapshot,
  verifyTurnTreeSnapshot,
  TurnSnapshotError,
  type CaptureTurnTreeSnapshotOptions,
  type TurnSnapshotGitRunner,
  type TurnTreeSnapshot
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
        const runGit: TurnSnapshotGitRunner = (root, args, env) => {
          if (args[0] === "status") {
            statusCalls += 1;
            return statusCalls % 2 === 1 ? "" : "?? changed-mid-write.txt\0";
          }
          return defaultTurnSnapshotGitRunner(root, args, env);
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
      let writeTreeCalls = 0;
      const runGit: TurnSnapshotGitRunner = (root, args, env) => {
        if (args[0] === "write-tree") {
          writeTreeCalls += 1;
          return writeTreeCalls % 2 === 1 ? "a".repeat(40) : "b".repeat(40);
        }
        if (args[0] === "add") return "";
        return defaultTurnSnapshotGitRunner(root, args, env);
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
      const runGit: TurnSnapshotGitRunner = (root, args, env) => {
        if (args[0] === "write-tree") writeTreeCalls += 1;
        return defaultTurnSnapshotGitRunner(root, args, env);
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
      const runGit: TurnSnapshotGitRunner = (root, args, env) => {
        if (args[0] === "write-tree") writeTreeCalls += 1;
        return defaultTurnSnapshotGitRunner(root, args, env);
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
      const runGit: TurnSnapshotGitRunner = (root, args, env) => {
        if (args[0] === "write-tree") writeTreeCalls += 1;
        return defaultTurnSnapshotGitRunner(root, args, env);
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
});
