#!/usr/bin/env node
/**
 * RIG-04: reproducible scale benchmark (≥10k files).
 * Measures file-walk / refuse-at-cap behavior. Does not invent conclusions.
 */
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { performance } from "node:perf_hooks";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const outDir = join(root, "benchmarks", "scale");
const FILE_COUNT = Number(process.env.FAULTLINE_SCALE_FILE_COUNT ?? "10000");

mkdirSync(outDir, { recursive: true });

const work = mkdtempSync(join(tmpdir(), "faultline-scale-"));
const repository = join(work, "repo");
mkdirSync(repository, { recursive: true });

function git(args) {
  return execFileSync("git", ["-C", repository, ...args], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
    maxBuffer: 64 * 1024 * 1024
  }).trim();
}

git(["init"]);
git(["config", "user.email", "scale@faultline.test"]);
git(["config", "user.name", "FaultLine Scale"]);
git(["config", "core.autocrlf", "false"]);
git(["config", "advice.ignoredHook", "false"]);

const generateStarted = performance.now();
for (let i = 0; i < FILE_COUNT; i += 1) {
  const dir = join(repository, "files", String(Math.floor(i / 500)));
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, `f-${i}.txt`), `scale-file-${i}\n`, "utf8");
}
const generateMs = performance.now() - generateStarted;

let walked = 0;
function walkSync(dir) {
  for (const name of readdirSync(dir)) {
    const absolute = join(dir, name);
    const st = statSync(absolute);
    if (st.isDirectory()) walkSync(absolute);
    else walked += 1;
  }
}
const walkStarted = performance.now();
walkSync(repository);
const walkMs = performance.now() - walkStarted;

git(["add", "."]);
const commitStarted = performance.now();
git(["commit", "-m", `scale fixture ${FILE_COUNT} files`]);
const commitMs = performance.now() - commitStarted;
const head = git(["rev-parse", "HEAD"]);

const snapshotMod = await import(pathToFileURL(join(root, "dist", "turn-snapshot.js")).href);
let snapshotOutcome = null;
const snapStarted = performance.now();
try {
  snapshotMod.captureTurnTreeSnapshot(repository, {
    turnId: "scale-turn",
    turnOrdinal: 1,
    occurredAt: new Date().toISOString(),
    trackedFilesOnly: true
  });
  snapshotOutcome = { status: "CAPTURED" };
} catch (error) {
  snapshotOutcome = {
    status: "REFUSED_OR_FAILED",
    message: error instanceof Error ? error.message : String(error)
  };
}
const snapMs = performance.now() - snapStarted;

const report = {
  schemaVersion: "faultline.scale-benchmark.v1",
  generatedAt: new Date().toISOString(),
  protocol: "docs/scale-benchmark-protocol.md",
  fixture: {
    fileCountRequested: FILE_COUNT,
    filesWalked: walked,
    head,
    worktree: "ephemeral (not retained)",
    mode: "trackedFilesOnly + clean HEAD^{tree} reuse"
  },
  timingsMs: {
    generate: Number(generateMs.toFixed(3)),
    walk: Number(walkMs.toFixed(3)),
    gitCommit: Number(commitMs.toFixed(3)),
    turnSnapshotAttempt: Number(snapMs.toFixed(3))
  },
  turnSnapshot: snapshotOutcome,
  nonClaims: [
    "Does not claim production localization latency at this scale.",
    "Dirty worktrees still refuse above the 2000 dirty-eligible-file cap.",
    "Clean worktrees reuse HEAD^{tree}; numbers are machine-local."
  ]
};

writeFileSync(join(outDir, "report.json"), `${JSON.stringify(report, null, 2)}\n`, "utf8");
const summary = [
  "# Scale benchmark summary (RIG-04)",
  "",
  `Generated: ${report.generatedAt}`,
  `Files requested: ${FILE_COUNT}`,
  `Files walked: ${walked}`,
  `Generate ms: ${report.timingsMs.generate}`,
  `Walk ms: ${report.timingsMs.walk}`,
  `Git commit ms: ${report.timingsMs.gitCommit}`,
  `Turn snapshot attempt ms: ${report.timingsMs.turnSnapshotAttempt}`,
  `Turn snapshot outcome: ${snapshotOutcome.status}`,
  snapshotOutcome.message ? `Detail: ${snapshotOutcome.message}` : null,
  "",
  "See docs/scale-benchmark-protocol.md for methodology and non-claims.",
  ""
].filter((line) => line !== null).join("\n");
writeFileSync(join(outDir, "SUMMARY.md"), summary, "utf8");

rmSync(work, { recursive: true, force: true });
process.stdout.write(`${summary}\n`);
