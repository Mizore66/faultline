import { mkdirSync, mkdtempSync, writeFileSync, rmSync, readdirSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { spawnSync } from "node:child_process";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

async function loadCapture() {
  const distUrl = pathToFileURL(join(root, "dist", "turn-snapshot.js")).href;
  try {
    return await import(distUrl);
  } catch {
    return import(pathToFileURL(join(root, "src", "turn-snapshot.ts")).href);
  }
}

function git(cwd, args) {
  const result = spawnSync("git", ["-c", "core.autocrlf=false", ...args], {
    cwd,
    encoding: "utf8",
    env: { ...process.env, GIT_CONFIG_NOSYSTEM: "1" }
  });
  if (result.status !== 0) {
    throw new Error(`git ${args.join(" ")} failed: ${result.stderr || result.stdout}`);
  }
  return (result.stdout ?? "").trim();
}

function percentile(sorted, p) {
  if (sorted.length === 0) return null;
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1));
  return sorted[index];
}

function countObjects(cwd) {
  const raw = git(cwd, ["count-objects", "-v"]);
  const map = Object.fromEntries(raw.split(/\r?\n/).filter(Boolean).map((line) => {
    const [key, value] = line.split(":").map((part) => part.trim());
    return [key, Number(value)];
  }));
  return {
    count: Number.isFinite(map.count) ? map.count : 0,
    size: Number.isFinite(map.size) ? map.size * 1024 : 0,
    inPack: Number.isFinite(map["in-pack"]) ? map["in-pack"] : 0,
    sizePack: Number.isFinite(map["size-pack"]) ? map["size-pack"] * 1024 : 0
  };
}

function createFixtureRepo(fileCount, bytesPerFile) {
  const dir = mkdtempSync(join(tmpdir(), `faultline-snap-fix-${fileCount}-`));
  git(dir, ["init"]);
  git(dir, ["config", "user.email", "measure@faultline.test"]);
  git(dir, ["config", "user.name", "FaultLine Measure"]);
  const payload = "x".repeat(bytesPerFile);
  for (let i = 0; i < fileCount; i += 1) {
    writeFileSync(join(dir, `f${String(i).padStart(5, "0")}.txt`), `${payload}\n`, "utf8");
  }
  git(dir, ["add", "."]);
  git(dir, ["commit", "-m", `fixture ${fileCount}`]);
  return dir;
}

function walkEligibleEstimate(dir) {
  let files = 0;
  let bytes = 0;
  const skip = new Set([".git", "node_modules", ".faultline", "dist", "coverage"]);
  const stack = [dir];
  while (stack.length > 0) {
    const current = stack.pop();
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      if (skip.has(entry.name)) continue;
      const full = join(current, entry.name);
      if (entry.isDirectory()) stack.push(full);
      else if (entry.isFile()) {
        files += 1;
        bytes += statSync(full).size;
      }
    }
  }
  return { files, bytes };
}

function measureTarget(label, repoPath, trials, captureTurnTreeSnapshot, planTurnSnapshotPaths, runGit) {
  const plan = planTurnSnapshotPaths(repoPath, runGit);
  const samples = [];
  let secretRejections = 0;
  let quiescenceRetryHints = 0;
  let blobsAdded = 0;
  let bytesStored = 0;

  for (let i = 0; i < trials; i += 1) {
    const before = countObjects(repoPath);
    let sleepCalls = 0;
    const sleep = (ms) => {
      sleepCalls += 1;
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
    };
    const t0 = process.hrtime.bigint();
    try {
      captureTurnTreeSnapshot(repoPath, { sleep, maxQuiescenceAttempts: 4 });
      samples.push(Number(process.hrtime.bigint() - t0) / 1e6);
      if (sleepCalls > 0) quiescenceRetryHints += sleepCalls;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (/secret/i.test(message)) secretRejections += 1;
      else throw error;
    }
    const after = countObjects(repoPath);
    blobsAdded += Math.max(0, (after.count + after.inPack) - (before.count + before.inPack));
    bytesStored += Math.max(0, (after.size + after.sizePack) - (before.size + before.sizePack));
  }

  const sorted = [...samples].sort((a, b) => a - b);
  const estimate = walkEligibleEstimate(repoPath);
  return {
    repository: label,
    filesEligible: plan.paths.length,
    filesOnDiskEstimate: estimate.files,
    bytesOnDiskEstimate: estimate.bytes,
    trials: samples.length,
    snapshotP50Ms: percentile(sorted, 50),
    snapshotP95Ms: percentile(sorted, 95),
    blobsAddedTotal: blobsAdded,
    bytesStoredTotal: bytesStored,
    secretScanRejections: secretRejections,
    quiescenceRetryHints,
    planWarnings: plan.warnings.length
  };
}

async function main() {
  const mod = await loadCapture();
  const { captureTurnTreeSnapshot, planTurnSnapshotPaths } = mod;
  const runGit = mod.defaultTurnSnapshotGitRunner ?? ((repositoryRoot, args) => {
    const result = spawnSync("git", ["-c", "core.autocrlf=false", ...args], {
      cwd: repositoryRoot,
      encoding: "utf8",
      env: { ...process.env, GIT_CONFIG_NOSYSTEM: "1" }
    });
    if (result.status !== 0) throw new Error(result.stderr || result.stdout || `git failed: ${args.join(" ")}`);
    return (result.stdout ?? "").trim();
  });
  const trials = Number(process.env.FAULTLINE_SNAPSHOT_TRIALS ?? "6");
  const rows = [];
  try {
    rows.push(measureTarget("FaultLine", root, trials, captureTurnTreeSnapshot, planTurnSnapshotPaths, runGit));
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    const estimate = walkEligibleEstimate(root);
    rows.push({
      repository: "FaultLine",
      filesEligible: estimate.files,
      filesOnDiskEstimate: estimate.files,
      bytesOnDiskEstimate: estimate.bytes,
      trials: 0,
      snapshotP50Ms: null,
      snapshotP95Ms: null,
      blobsAddedTotal: 0,
      bytesStoredTotal: 0,
      secretScanRejections: /secret/i.test(message) ? 1 : 0,
      quiescenceRetryHints: 0,
      planWarnings: 0,
      note: message
    });
  }

  const fixtures = [];
  try {
    // Stay under TURN_SNAPSHOT_MAX_FILE_COUNT (2000); a 5k-file tree is rejected by design.
    const medium = createFixtureRepo(1000, 64);
    fixtures.push(medium);
    rows.push(measureTarget("Medium fixture (1,000)", medium, trials, captureTurnTreeSnapshot, planTurnSnapshotPaths, runGit));
    const large = createFixtureRepo(1800, 32);
    fixtures.push(large);
    rows.push(measureTarget("Large fixture (1,800 under cap)", large, Math.max(3, Math.floor(trials / 2)), captureTurnTreeSnapshot, planTurnSnapshotPaths, runGit));
  } finally {
    for (const fixture of fixtures) rmSync(fixture, { recursive: true, force: true });
  }

  const report = {
    schemaVersion: "faultline.turn-snapshot-overhead.v1",
    generatedAt: new Date().toISOString(),
    trialsDefault: trials,
    note: "Wall-clock captureTurnTreeSnapshot including quiescence. Blob/byte deltas from git count-objects before/after each trial.",
    rows
  };

  mkdirSync(join(root, "benchmarks"), { recursive: true });
  writeFileSync(join(root, "benchmarks", "turn-snapshot-report.json"), `${JSON.stringify(report, null, 2)}\n`, "utf8");

  const md = [
    "# Turn-tree snapshot overhead",
    "",
    "Measured with `pnpm measure:turn-snapshot` (`scripts/measure-turn-snapshot.mjs`).",
    "",
    "**Does recording slow Codex?** Each Stop that captures a turn tree pays this cost once (bounded quiescence retries, ignore filters, secret scan, then throwaway index + `write-tree`).",
    "",
    `| Repository | Eligible files | Snapshot p50 (ms) | Snapshot p95 (ms) | Blobs added (sum) | Bytes stored (sum) | Secret rejections | Quiescence sleep calls |`,
    `| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |`,
    ...rows.map((row) => `| ${row.repository} | ${row.filesEligible} | ${row.snapshotP50Ms?.toFixed(1) ?? "n/a"} | ${row.snapshotP95Ms?.toFixed(1) ?? "n/a"} | ${row.blobsAddedTotal} | ${row.bytesStoredTotal} | ${row.secretScanRejections} | ${row.quiescenceRetryHints} |`),
    "",
    "## Method",
    "",
    `- Trials per target: ${trials} (override with \`FAULTLINE_SNAPSHOT_TRIALS\`)`,
    "- Caps: 1 MiB/file, 32 MiB total, 2000 files (see `src/turn-snapshot.ts`)",
    "- Quiescence: up to 4 dual-tree attempts with 50 ms delay",
    "- FaultLine row uses this checkout with default ignore rules (`node_modules/`, `.faultline/`, `dist/`, …). If secret scanning refuses the tree, p50/p95 are omitted and the rejection is counted.",
    "- Synthetic fixtures are tracked-file-only temp repos under the 2000-file hard cap (a 5 000-file tree is rejected by design).",
    "",
    "Regenerate: `pnpm measure:turn-snapshot`",
    ""
  ].join("\n");
  writeFileSync(join(root, "docs", "turn-snapshot-overhead.md"), md, "utf8");
  process.stdout.write(`Wrote benchmarks/turn-snapshot-report.json and docs/turn-snapshot-overhead.md (${rows.length} rows).\n`);
}

main().catch((error) => {
  process.stderr.write(`${error instanceof Error ? error.stack ?? error.message : String(error)}\n`);
  process.exitCode = 1;
});
