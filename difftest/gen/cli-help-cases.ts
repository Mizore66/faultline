import assert from "node:assert/strict";

export const cliHelpBaseline = {
  schema: "faultline.cli-help-oracle.v1",
  sourceCommit: "ce99a793af0b020bf1b9cf21114f8d7897b37207",
  manifestCommit: "6f2450549acb002f04a73dd3c1e3091299d7d59f",
  manifestVersion: "0.1.4",
  nodeVersion: "22.22.2",
} as const;

export type CLIHelpResult = { stdout: string; stderr: string; exit: number };

export function assertCLIHelpOracle(nodeVersion: string, manifestVersion: unknown,
  result: CLIHelpResult): void {
  assert.equal(nodeVersion, cliHelpBaseline.nodeVersion, "CLI-help oracle Node drift");
  assert.equal(manifestVersion, cliHelpBaseline.manifestVersion, "CLI-help manifest drift");
  assert.deepEqual(result, { stdout: "FaultLine 0.1.4\n", stderr: "", exit: 0 },
    "CLI-help oracle version-output drift");
}

export function cliHelpCases(): { case: string; args: string[] }[] {
  const rows = [{ case: "no-args", args: [] as string[] }];
  const aliases = [["word", "help"], ["long", "--help"], ["short", "-h"]] as const;
  const variants: [string, string[]][] = [
    ["normal", []], ["ignored", ["ignored"]],
    ["advanced-first", ["advanced"]], ["advanced-extra", ["advanced", "ignored"]],
    ["advanced-flag", ["--advanced"]], ["advanced-flag-late", ["ignored", "--advanced"]],
    ["advanced-not-first", ["ignored", "advanced"]],
    ["case-name", ["Advanced"]], ["case-flag", ["--Advanced"]],
    ["equals-flag", ["--advanced=false"]], ["bare-dash", ["--"]],
    ["empty", [""]], ["space", ["two words"]], ["unicode", ["é😀"]],
    ["advanced-empty", ["", "--advanced"]],
    ["advanced-space", ["two words", "--advanced"]],
    ["advanced-unicode", ["é😀", "--advanced"]],
  ];
  for (const [label, alias] of aliases) {
    for (const [variant, args] of variants) {
      rows.push({ case: `help-${label}-${variant}`, args: [alias, ...args] });
    }
  }
  for (const [label, command] of [["advanced", "advanced"], ["quickstart", "quickstart"],
    ["version-long", "--version"], ["version-short", "-V"]]) {
    for (const [suffix, args] of [["plain", []], ["extra", ["ignored", "--advanced"]],
      ["unicode", ["é😀"]]] as [string, string[]][]) {
      rows.push({ case: `${label}-${suffix}`, args: [command, ...args] });
    }
  }
  rows.push({ case: "unknown-advanced-flag", args: ["--advanced"] },
    { case: "unknown-case", args: ["Help"] },
    { case: "existing-verify-usage", args: ["verify"] });
  return rows;
}
