// HARNESS-ONLY tests: they check capture orchestration and owned-child cleanup
// using a tiny local stand-in CLI inside an independently created temporary
// repository. Nothing here is oracle evidence and nothing is expected golden
// data; the real frozen dist/cli.js is never used. Each test bounds and cleans
// up any child it leaks, even when the harness under test fails.
import assert from "node:assert/strict";
import { spawn, type ChildProcess, type SpawnOptions } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { captureAll } from "./serve-bundle-orchestrate.js";
import { captureServeBundleCaseDetailed, type CaptureHooks, type CapturedCase } from "./serve-bundle-capture.js";
import type { ServeBundleCase } from "./serve-bundle-cases.js";

const row = (id: string, argv: string[] = []): ServeBundleCase => ({
  id, kind: argv.length === 0 ? "view" : "cli-serve", fixture: "coherent", expectedRoot: null, options: {}, argv
});

test("captureAll preserves every capture's scratch directory and its files", async () => {
  // Catches a loop that deletes evidence (scratch) after recording each golden.
  const scratches: string[] = [];
  const stand = async (_root: string, r: ServeBundleCase): Promise<CapturedCase> => {
    const scratch = mkdtempSync(join(tmpdir(), "faultline-harness-scratch-"));
    scratches.push(scratch);
    writeFileSync(join(scratch, "evidence.txt"), `evidence for ${r.id}`);
    return { golden: { id: r.id, valueJSON: null, error: "e", page: null, process: null }, pageBytes: null, scratch };
  };
  // The synthetic evidence is deliberately left in place (retention outranks cleanup).
  const rendered = await captureAll("unused-root", [row("a"), row("b")], stand);
  assert.deepEqual(rendered.map((r) => r.scratch), scratches, "each scratch path is returned for the caller to log");
  for (const scratch of scratches) {
    assert.equal(readFileSync(join(scratch, "evidence.txt"), "utf8").startsWith("evidence for "), true, scratch);
  }
});

// ---- stand-in CLI ----

const STAND_IN = String.raw`
const fs = require("node:fs");
const net = require("node:net");
const mode = process.argv[2];
fs.writeFileSync(process.env.HARNESS_PID_FILE, String(process.pid));
const hold = () => setInterval(() => {}, 1 << 30);
if (mode === "bad-url") {
  process.stdout.write("not the startup line\nPress Ctrl+C to stop.\n");
  hold();
} else if (mode === "fetch-fails") {
  const server = net.createServer();
  server.listen(0, "127.0.0.1", () => {
    const port = server.address().port;
    server.close(() => {
      process.stdout.write("FaultLine read-only Git proof page: http://127.0.0.1:" + port + "\nPress Ctrl+C to stop.\n");
      hold();
    });
  });
} else if (mode === "silent") {
  hold();
} else if (mode === "early-exit") {
  process.stderr.write("boom\n");
  process.exit(3);
} else if (mode === "bad-second-line") {
  server = net.createServer();
  server.listen(0, "127.0.0.1", () => {
    process.stdout.write("FaultLine read-only Git proof page: http://127.0.0.1:" + server.address().port + "\nunexpected second line\n");
    hold();
  });
}
`;

function fakeRepository(): { repo: string; corpus: string; pidFile: string } {
  const base = mkdtempSync(join(tmpdir(), "faultline-harness-repo-"));
  mkdirSync(join(base, "repo", "dist"), { recursive: true });
  writeFileSync(join(base, "repo", "dist", "cli.js"), STAND_IN);
  mkdirSync(join(base, "corpus", "inputs", "coherent"), { recursive: true });
  writeFileSync(join(base, "corpus", "inputs", "coherent", "marker.txt"), "marker");
  return { repo: join(base, "repo"), corpus: join(base, "corpus"), pidFile: join(base, "child.pid") };
}

function isAlive(pid: number): boolean {
  try { process.kill(pid, 0); return true; } catch (error) { return (error as NodeJS.ErrnoException).code === "EPERM"; }
}

/** Runs one capture against the stand-in and always reaps its own leaked child. */
async function runStandIn(mode: string, hooks: CaptureHooks = {}): Promise<{ error: unknown; pid: number; aliveAfterReturn: boolean }> {
  const { repo, corpus, pidFile } = fakeRepository();
  const originalCwd = process.cwd();
  const originalPidFile = process.env.HARNESS_PID_FILE;
  process.env.HARNESS_PID_FILE = pidFile;
  process.chdir(repo);
  let error: unknown;
  try {
    await captureServeBundleCaseDetailed(corpus, row(`stand-in-${mode}`, [mode]), hooks);
  } catch (caught) {
    error = caught;
  } finally {
    process.chdir(originalCwd);
    if (originalPidFile === undefined) delete process.env.HARNESS_PID_FILE; else process.env.HARNESS_PID_FILE = originalPidFile;
  }
  assert.ok(existsSync(pidFile), "the stand-in CLI never started");
  const pid = Number(readFileSync(pidFile, "utf8"));
  const aliveAfterReturn = isAlive(pid);
  if (aliveAfterReturn) { try { process.kill(pid, "SIGKILL"); } catch { /* already gone */ } }
  return { error, pid, aliveAfterReturn };
}

for (const mode of ["bad-url", "bad-second-line", "fetch-fails"]) {
  test(`capture failure (${mode}) kills and reaps the still-running owned CLI child before returning`, { timeout: 120_000 }, async () => {
    // Catches a validation/fetch failure path that rejects but leaves the server child running.
    const result = await runStandIn(mode);
    assert.ok(result.error instanceof Error, "the capture must fail");
    assert.equal(result.aliveAfterReturn, false, `child ${result.pid} was still alive when the capture returned its failure`);
  });
}

test("a startup that never completes hits its bounded deadline, fails, and the child is killed and reaped", { timeout: 120_000 }, async () => {
  // Catches a timeout path that rejects without reaping, or that waits forever.
  const result = await runStandIn("silent", { processDeadlineMs: 500, cleanupDeadlineMs: 10_000 });
  assert.ok(result.error instanceof Error);
  assert.match((result.error as Error).message, /did not finish within 500 ms/);
  assert.equal(result.aliveAfterReturn, false);
});

test("a spawn failure surfaces as the capture error with no unhandled rejection and nothing left to reap", { timeout: 120_000 }, async () => {
  // Catches an 'error' event that becomes an unhandled rejection or hangs the startup wait.
  const unhandled: unknown[] = [];
  const listener = (reason: unknown): void => { unhandled.push(reason); };
  process.on("unhandledRejection", listener);
  const { repo, corpus } = fakeRepository();
  const originalCwd = process.cwd();
  process.chdir(repo);
  try {
    for (const kind of ["cli-serve", "cli-error"] as const) {
      const failing = (_file: string, args: string[], options: SpawnOptions): ChildProcess =>
        spawn(join(repo, "no-such-node-executable"), args, options);
      const c: ServeBundleCase = { ...row("spawn-failure", ["serve", "--bundle", "{ROOT}/bundle"]), kind };
      await assert.rejects(captureServeBundleCaseDetailed(corpus, c, { spawnFn: failing, processDeadlineMs: 10_000 }), /ENOENT/, kind);
    }
    await new Promise<void>((resolveTick) => setImmediate(resolveTick));
    assert.deepEqual(unhandled, []);
  } finally {
    process.chdir(originalCwd);
    process.off("unhandledRejection", listener);
  }
});

test("a child that exits before startup fails the capture with its output and no unhandled rejection", { timeout: 120_000 }, async () => {
  // Catches a derived early-exit promise that rejects with nobody listening.
  const unhandled: unknown[] = [];
  const listener = (reason: unknown): void => { unhandled.push(reason); };
  process.on("unhandledRejection", listener);
  try {
    const result = await runStandIn("early-exit");
    assert.ok(result.error instanceof Error);
    assert.match((result.error as Error).message, /exited before startup/);
    assert.equal(result.aliveAfterReturn, false);
    // Let any stray rejection surface through the event loop before judging.
    await new Promise<void>((resolveTick) => setImmediate(resolveTick));
    assert.deepEqual(unhandled, [], "an unhandled promise rejection escaped the capture");
  } finally {
    process.off("unhandledRejection", listener);
  }
});
