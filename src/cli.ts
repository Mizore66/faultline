#!/usr/bin/env node
import { resolve } from "node:path";
import { defaultWitnessProposal, proposeWitnessWithGpt } from "./ai.js";
import { createDemoAnalysis } from "./engine.js";
import { captureCleanGitSnapshot, writeGitSidecarSnapshot } from "./git-snapshot.js";
import { describeBundlePath, verifyProofBundle, writeProofBundle } from "./proof-bundle.js";
import { startFaultLineServer } from "./server.js";
import type { RunMode } from "./domain.js";

const usage = `FaultLine — executable evidence for agent-assisted code

Usage:
  fl judge-demo [--replay | --rerun-all] [--output <directory>] [--export-only]
  fl verify <proof-bundle-directory> [--expect-root <sha256:...>]
  fl serve [--port <number>]
  fl codex --dry-run | --snapshot [--repo <directory>]
  fl witness propose [--live] [--model <model>]

The judge demo is a reviewed, deterministic Node fixture. It does not require an OpenAI API key.
Use --live for a GPT-5.6 witness proposal after setting OPENAI_API_KEY.`;

function hasFlag(args: string[], flag: string): boolean {
  return args.includes(flag);
}

function option(args: string[], flag: string): string | undefined {
  const index = args.indexOf(flag);
  return index >= 0 ? args[index + 1] : undefined;
}

async function judgeDemo(args: string[]): Promise<void> {
  const mode: RunMode = hasFlag(args, "--rerun-all") ? "RERUN" : "REPLAY";
  const outputDirectory = resolve(option(args, "--output") ?? ".faultline/judge-demo");
  const analysis = createDemoAnalysis(mode);
  const bundle = writeProofBundle(outputDirectory, analysis);
  const verification = verifyProofBundle(bundle.directory);
  if (!verification.valid) throw new Error(`Generated proof bundle failed integrity verification: ${verification.errors.join("; ")}`);
  process.stdout.write(`FaultLine ${mode === "RERUN" ? "executed" : "cached replay"} investigation prepared.\n`);
  process.stdout.write("Stable sample boundary: cedar-turn-4 → cedar-turn-5\n");
  process.stdout.write(`Proof bundle self-consistency: VALID (${verification.checkedFiles} declared files)\n`);
  process.stdout.write(`Bundle root: ${bundle.rootDigest}\n`);
  process.stdout.write(`Proof bundle: ${describeBundlePath(bundle.directory)}\n`);
  if (hasFlag(args, "--export-only")) return;
  const server = await startFaultLineServer({ analysis, outputDirectory: bundle.directory, port: Number(option(args, "--port") ?? "4173") });
  process.stdout.write(`Open ${server.url} to inspect the incident page. Press Ctrl+C to stop.\n`);
  await new Promise<void>((resolveExit) => {
    process.once("SIGINT", () => {
      void server.close().finally(resolveExit);
    });
  });
}

async function witnessProposal(args: string[]): Promise<void> {
  const packet = {
    symptom: "A completed refund may overwrite settlement currency with display currency.",
    ciLog: "Expected settlement currency USD; received EUR.",
    repositoryLanguage: "JavaScript",
    repositorySummary: "A Node service that completes refunds."
  };
  if (!hasFlag(args, "--live")) {
    process.stdout.write(`${JSON.stringify({ source: "sample", proposal: defaultWitnessProposal(), note: "Pass --live to call GPT-5.6 with a blinded incident packet." }, null, 2)}\n`);
    return;
  }
  const result = await proposeWitnessWithGpt(packet, { model: option(args, "--model") ?? "gpt-5.6" });
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
}

async function main(): Promise<void> {
  const [command, ...args] = process.argv.slice(2);
  switch (command) {
    case undefined:
    case "help":
    case "--help":
    case "-h":
      process.stdout.write(`${usage}\n`);
      return;
    case "judge-demo":
      await judgeDemo(args);
      return;
    case "verify": {
      const directory = args[0];
      if (!directory) throw new Error("Usage: fl verify <proof-bundle-directory>");
      const result = verifyProofBundle(resolve(directory), option(args, "--expect-root"));
      process.stdout.write(`${result.externalRootStatus === "NOT_PROVIDED" ? "Bundle self-consistency" : "Integrity"}: ${result.valid ? "VALID" : "INVALID"}\n`);
      process.stdout.write(`Declared files checked: ${result.checkedFiles}\nBundle root: ${result.rootDigest ?? "unavailable"}\nExternal root: ${result.externalRootStatus}\n`);
      if (!result.valid) process.stdout.write(`${result.errors.map((error) => `- ${error}`).join("\n")}\n`);
      process.exitCode = result.valid ? 0 : 1;
      return;
    }
    case "serve": {
      const outputDirectory = resolve(option(args, "--output") ?? ".faultline/judge-demo");
      const analysis = createDemoAnalysis("REPLAY");
      const server = await startFaultLineServer({ analysis, outputDirectory, port: Number(option(args, "--port") ?? "4173") });
      process.stdout.write(`FaultLine incident page: ${server.url}\nPress Ctrl+C to stop.\n`);
      await new Promise<void>((resolveExit) => process.once("SIGINT", () => { void server.close().finally(resolveExit); }));
      return;
    }
    case "codex":
      if (hasFlag(args, "--dry-run")) {
        process.stdout.write(`${JSON.stringify({ adapter: "git-sidecar", status: "DRY_RUN", records: ["clean Git HEAD tree", "content-addressed snapshot manifest"], limitation: "No live Codex transport or turn lifecycle capture is claimed by this build." }, null, 2)}\n`);
        return;
      }
      if (hasFlag(args, "--snapshot")) {
        const repository = resolve(option(args, "--repo") ?? process.cwd());
        const snapshot = captureCleanGitSnapshot(repository);
        const file = writeGitSidecarSnapshot(repository, snapshot);
        process.stdout.write(`${JSON.stringify({ snapshot, file, limitation: "This is a clean Git sidecar snapshot, not a live Codex transport event." }, null, 2)}\n`);
        return;
      }
      throw new Error("The current build exposes an honest Git sidecar. Run: fl codex --dry-run or fl codex --snapshot --repo <directory>");
    case "witness":
      if (args[0] !== "propose") throw new Error("Usage: fl witness propose [--live] [--model <model>]");
      await witnessProposal(args.slice(1));
      return;
    default:
      throw new Error(`Unknown command: ${command}\n\n${usage}`);
  }
}

main().catch((error: unknown) => {
  process.stderr.write(`FaultLine error: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
});
