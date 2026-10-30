import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { assertCLIHelpOracle, cliHelpBaseline, cliHelpCases, type CLIHelpResult } from "./cli-help-cases.js";

const repo = resolve(".");
const cli = join(repo, "dist", "cli.js");
assert.equal(process.versions.node, cliHelpBaseline.nodeVersion, "CLI-help oracle Node drift");
function newestMtime(dir: string): number {
  let newest = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    newest = Math.max(newest, entry.isDirectory() ? newestMtime(path) : statSync(path).mtimeMs);
  }
  return newest;
}
assert.ok(existsSync(cli) && statSync(cli).mtimeMs >= newestMtime(join(repo, "src")),
  "dist/cli.js is missing or older than src/: run pnpm build first");
const manifest: { version?: unknown } = JSON.parse(readFileSync(join(repo, "package.json"), "utf8"));
function run(args: string[]): CLIHelpResult {
  const child = spawnSync(process.execPath, [cli, ...args], {
    cwd: repo, encoding: "utf8", timeout: 5000, maxBuffer: 1024 * 1024,
  });
  if (child.error) throw child.error;
  assert.equal(child.signal, null, `CLI-help child signalled: ${JSON.stringify(args)}`);
  assert.notEqual(child.status, null, `CLI-help child has no exit: ${JSON.stringify(args)}`);
  return { stdout: child.stdout, stderr: child.stderr, exit: child.status! };
}
assertCLIHelpOracle(process.versions.node, manifest.version, run(["--version"]));
const cases = cliHelpCases();
assert.equal(cases.length, 67);
const rows = cases.map((c) => ({ case: c.case, args: c.args, ...run(c.args) }));
for (const row of rows) {
  const failure = row.case.startsWith("unknown-") || row.case === "existing-verify-usage";
  assert.equal(row.exit, failure ? 1 : 0, row.case);
  assert.equal(failure ? row.stdout : row.stderr, "", row.case);
}
// All preflight and capture succeed before either committed output is touched.
const directory = join(repo, "difftest", "testdata", "cli-help");
mkdirSync(directory, { recursive: true });
writeFileSync(join(directory, "baseline.json"), `${JSON.stringify(cliHelpBaseline, null, 2)}\n`, "utf8");
writeFileSync(join(directory, "golden.jsonl"), `${rows.map((r) => JSON.stringify(r)).join("\n")}\n`, "utf8");
console.log(`wrote ${rows.length} CLI-help goldens`);
