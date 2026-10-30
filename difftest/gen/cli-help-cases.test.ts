import assert from "node:assert/strict";
import test from "node:test";
import { assertCLIHelpOracle, cliHelpBaseline, cliHelpCases } from "./cli-help-cases.js";

const good = { stdout: "FaultLine 0.1.4\n", stderr: "", exit: 0 };

test("CLI-help inputs cover exact aliases, positions and argv boundaries", () => {
  const rows = cliHelpCases();
  assert.equal(rows.length, 67);
  assert.equal(new Set(rows.map((r) => r.case)).size, 67);
  const args = rows.map((r) => JSON.stringify(r.args));
  for (const argv of [[], ["help", "ignored", "advanced"],
    ["help", "ignored", "--advanced"], ["-h", "Advanced"],
    ["--help", "--advanced=false"], ["help", ""],
    ["help", "two words"], ["help", "é😀"],
    ["advanced", "ignored", "--advanced"], ["--version", "é😀"],
    ["--advanced"], ["Help"], ["verify"]]) {
    assert.ok(args.includes(JSON.stringify(argv)), JSON.stringify(argv));
  }
});

test("oracle preflight refuses drift rather than changing its baseline", () => {
  assert.doesNotThrow(() => assertCLIHelpOracle("22.22.2", "0.1.4", good));
  assert.throws(() => assertCLIHelpOracle("22.23.4", "0.1.4", good));
  for (const bad of ["0.1.3", "9.9.9", null, 14]) {
    assert.throws(() => assertCLIHelpOracle("22.22.2", bad, good));
  }
  for (const bad of [
    { ...good, stdout: "FaultLine 0.1.3\n" },
    { ...good, stdout: "FaultLine 0.1.4" },
    { ...good, stderr: "unexpected\n" },
    { ...good, exit: 1 },
  ]) assert.throws(() => assertCLIHelpOracle("22.22.2", "0.1.4", bad));
  assert.equal(cliHelpBaseline.sourceCommit, "ce99a793af0b020bf1b9cf21114f8d7897b37207");
});
