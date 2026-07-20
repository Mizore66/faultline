import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  existsSync,
  rmSync,
  appendFileSync,
  closeSync,
  ftruncateSync,
  openSync,
  copyFileSync
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { commitRepairedWorktreeState, collectRepairedPreventionRuns } from "../src/prevention-from-artifacts.js";
import {
  TURN_SNAPSHOT_MAX_FILE_BYTES,
  TurnSnapshotError,
  captureTurnTreeSnapshot,
  computeDirtyContentFingerprint,
  fullFileContentDigest
} from "../src/turn-snapshot.js";
import { fileURLToPath } from "node:url";

const sampleBundle = fileURLToPath(
  new URL("../docs/samples/self-incident-commit-proof", import.meta.url)
);

function git(cwd: string, args: string[]): string {
  const result = spawnSync("git", ["-C", cwd, ...args], { encoding: "utf8" });
  if (result.status !== 0) {
    throw new Error(result.stderr || result.stdout || `git ${args.join(" ")} failed`);
  }
  return (result.stdout ?? "").trim();
}

describe("SEC P0: hardened repaired-worktree commit", () => {
  it("does not invoke repository clean filters when committing repaired state", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-repair-filter-"));
    try {
      const repo = join(root, "repo");
      mkdirSync(repo);
      git(repo, ["init"]);
      git(repo, ["config", "user.email", "repair@faultline.test"]);
      git(repo, ["config", "user.name", "FaultLine Repair"]);
      writeFileSync(join(repo, "app.js"), "console.log(1);\n", "utf8");
      git(repo, ["add", "."]);
      git(repo, ["commit", "-m", "base"]);
      const base = git(repo, ["rev-parse", "HEAD"]);

      const sentinel = join(root, "CLEAN_FILTER_RAN");
      const filterScript = join(root, "evil-clean.cjs");
      writeFileSync(
        filterScript,
        `require("node:fs").writeFileSync(${JSON.stringify(sentinel)}, "ran\\n"); process.stdin.pipe(process.stdout);\n`,
        "utf8"
      );
      // Local (repo) filter + attributes — must not run during hardened repair commit.
      git(repo, ["config", "filter.faultline-evil.clean", `node "${filterScript}"`]);
      writeFileSync(join(repo, ".gitattributes"), "*.js filter=faultline-evil\n", "utf8");
      writeFileSync(join(repo, "app.js"), "console.log(2);\n", "utf8");
      rmSync(sentinel, { force: true });

      const repaired = commitRepairedWorktreeState(repo, base);
      expect(repaired.commit).toMatch(/^[a-f0-9]{40}([a-f0-9]{24})?$/);
      expect(repaired.tree).toMatch(/^[a-f0-9]{40}([a-f0-9]{24})?$/);
      expect(existsSync(sentinel)).toBe(false);
      const blob = git(repo, ["show", `${repaired.tree}:app.js`]);
      expect(blob).toBe("console.log(2);");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});

describe("SEC P0: fingerprint caps before full reads", () => {
  it("refuses to fingerprint a file one byte over the per-file cap without reading it fully into a Buffer", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-fp-cap-"));
    try {
      const oversized = join(root, "big.bin");
      const limit = 64 * 1024;
      writeFileSync(oversized, Buffer.alloc(limit + 1, 7));
      expect(() => fullFileContentDigest(oversized, limit)).toThrow(TurnSnapshotError);
      expect(() => fullFileContentDigest(oversized, limit)).toThrow(/max 65536 per file/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses dirty fingerprinting when an untracked file exceeds the snapshot cap", () => {
    const repo = mkdtempSync(join(tmpdir(), "faultline-fp-dirty-"));
    try {
      git(repo, ["init"]);
      git(repo, ["config", "user.email", "fp@faultline.test"]);
      git(repo, ["config", "user.name", "FaultLine FP"]);
      writeFileSync(join(repo, "ok.txt"), "ok\n", "utf8");
      git(repo, ["add", "."]);
      git(repo, ["commit", "-m", "base"]);
      writeFileSync(join(repo, "huge.bin"), Buffer.alloc(TURN_SNAPSHOT_MAX_FILE_BYTES + 1, 1));
      const status = git(repo, ["status", "--porcelain=v1", "-z"]);
      expect(() =>
        computeDirtyContentFingerprint(repo, status, { maxFileBytes: TURN_SNAPSHOT_MAX_FILE_BYTES })
      ).toThrow(TurnSnapshotError);
      expect(() =>
        captureTurnTreeSnapshot(repo, { sleep: () => {} })
      ).toThrow(TurnSnapshotError);
    } finally {
      rmSync(repo, { recursive: true, force: true });
    }
  });

  it("streams accepted files and rejects growth past the cap mid-hash", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-fp-grow-"));
    try {
      const path = join(root, "growing.bin");
      writeFileSync(path, Buffer.alloc(1024, 2));
      // Simulate growth: digest with a tiny cap after appending.
      appendFileSync(path, Buffer.alloc(2048, 3));
      expect(() => fullFileContentDigest(path, 1500)).toThrow(/max 1500 per file|grew past/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses a sparse multi-gigabyte file after lstat without buffering contents", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-fp-sparse-"));
    try {
      const sparse = join(root, "sparse.bin");
      const fd = openSync(sparse, "w");
      try {
        ftruncateSync(fd, 3 * 1024 * 1024 * 1024);
      } finally {
        closeSync(fd);
      }
      expect(() => fullFileContentDigest(sparse, TURN_SNAPSHOT_MAX_FILE_BYTES)).toThrow(TurnSnapshotError);
      expect(() => fullFileContentDigest(sparse, TURN_SNAPSHOT_MAX_FILE_BYTES)).toThrow(/per file/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses an oversized protected lockfile during dirty fingerprinting", () => {
    const repo = mkdtempSync(join(tmpdir(), "faultline-fp-lock-"));
    try {
      git(repo, ["init"]);
      git(repo, ["config", "user.email", "fp@faultline.test"]);
      git(repo, ["config", "user.name", "FaultLine FP"]);
      writeFileSync(join(repo, "package.json"), "{}\n", "utf8");
      git(repo, ["add", "."]);
      git(repo, ["commit", "-m", "base"]);
      writeFileSync(join(repo, "package-lock.json"), Buffer.alloc(TURN_SNAPSHOT_MAX_FILE_BYTES + 1, 1));
      const status = git(repo, ["status", "--porcelain=v1", "-z"]);
      expect(() =>
        computeDirtyContentFingerprint(repo, status, { maxFileBytes: TURN_SNAPSHOT_MAX_FILE_BYTES })
      ).toThrow(TurnSnapshotError);
    } finally {
      rmSync(repo, { recursive: true, force: true });
    }
  });
});

describe("SEC P0: PREVENTION_VERIFIED requires content-bound execution IDs", () => {
  it("refuses repaired-run collection when verifyOnce omits executionId", async () => {
    const bundle = mkdtempSync(join(tmpdir(), "faultline-prevention-id-"));
    try {
      mkdirSync(join(bundle, "witness"), { recursive: true });
      copyFileSync(join(sampleBundle, "investigation.json"), join(bundle, "investigation.json"));
      copyFileSync(join(sampleBundle, "witness", "frozen.json"), join(bundle, "witness", "frozen.json"));
      const result = await collectRepairedPreventionRuns({
        worktreePath: tmpdir(),
        bundleDirectory: bundle,
        repairedCommit: "a".repeat(40),
        repairedTree: "b".repeat(40),
        verifyOnce: async () => ({ ok: true, detail: "predicate pass without id" })
      });
      expect(result.ok).toBe(false);
      if (!result.ok) {
        expect(result.reasons.join("\n")).toMatch(/omitted a content-bound executionId/);
      }
    } finally {
      rmSync(bundle, { recursive: true, force: true });
    }
  });
});
