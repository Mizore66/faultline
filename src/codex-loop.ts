import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { z } from "zod";
import { digestJson } from "./canonical.js";

export const CODEX_LOOP_SCHEMA_VERSION = "faultline.codex-loop.v1" as const;

const IncidentPacketSchema = z.object({
  symptom: z.string().min(1),
  ciLog: z.string().min(1),
  repositoryLanguage: z.string().min(1),
  repositorySummary: z.string().min(1)
}).strict();

export type CodexCommandRunner = {
  run(args: readonly string[], options: { cwd: string; input?: string }): Promise<{
    exitCode: number | null;
    stdout: string;
    stderr: string;
  }>;
};

export type ImplementWitnessResult = {
  schemaVersion: typeof CODEX_LOOP_SCHEMA_VERSION;
  status: "OVERLAY_DRAFTED" | "CODEX_UNAVAILABLE" | "TEMPLATE_ONLY";
  transport: "CODEX_CLI" | "NONE";
  proposalId: string;
  overlayPath: string;
  commandSuggestion: string;
  note: string;
  limitation: string;
};

export type RepairWithCodexResult = {
  schemaVersion: typeof CODEX_LOOP_SCHEMA_VERSION;
  status: "REPAIR_INSTRUCTIONS_PREPARED" | "CODEX_UNAVAILABLE" | "BUNDLE_INVALID";
  transport: "CODEX_CLI" | "NONE";
  worktreePath: string | null;
  instructionPath: string | null;
  nextCommands: readonly string[];
  note: string;
  limitation: string;
};

function defaultOverlayTemplate(behavior: string): string {
  const safeBehavior = behavior.replaceAll("\n", " ").slice(0, 200);
  return [
    "#!/usr/bin/env node",
    `// FaultLine witness overlay — behavior: ${safeBehavior}`,
    "// Emits a structured witness result. Edit the predicate, then human-review before freeze.",
    "const pass = false; // set true only when the frozen predicate holds",
    'const outcome = pass ? "PREDICATE_PASS" : "PREDICATE_FAIL";',
    'console.log(JSON.stringify({ protocol: "faultline.witness-result.v1", outcome }));',
    "process.exit(pass ? 0 : 1);",
    ""
  ].join("\n");
}

function writeOverlayTemplate(overlayOut: string, proposalId: string, behavior: string): string {
  mkdirSync(overlayOut, { recursive: true });
  const overlayPath = join(overlayOut, "witness.mjs");
  writeFileSync(overlayPath, defaultOverlayTemplate(behavior), "utf8");
  writeFileSync(join(overlayOut, "README.faultline.txt"), [
    `Proposal id: ${proposalId}`,
    "1. Review and edit witness.mjs",
    "2. fl witness propose --input proposal.json --overlay-root <this-dir> (or use incident intake)",
    "3. fl witness review <id> → Approve → Freeze",
    "FaultLine never auto-freezes Codex-drafted overlays.",
    ""
  ].join("\n"), "utf8");
  return overlayPath;
}

/**
 * Draft a witness overlay with Codex when available; otherwise write a template.
 * Never freezes. Never claims private Codex interception.
 */
export async function implementWitnessWithCodex(options: {
  proposalId: string;
  incident: unknown;
  behavior: string;
  overlayOut: string;
  repository: string;
  runner?: CodexCommandRunner;
}): Promise<ImplementWitnessResult> {
  const incident = IncidentPacketSchema.parse(options.incident);
  const overlayOut = resolve(options.overlayOut);
  const overlayPath = writeOverlayTemplate(overlayOut, options.proposalId, options.behavior);
  const limitation = "Codex may draft overlay bytes in an isolated review directory only. A human must approve and freeze before localization.";

  if (!options.runner) {
    return {
      schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
      status: "TEMPLATE_ONLY",
      transport: "NONE",
      proposalId: options.proposalId,
      overlayPath,
      commandSuggestion: "node witness.mjs",
      note: `Template written for blinded symptom digest ${digestJson(incident)}. Supply --with-codex runner to attempt Codex CLI drafting.`,
      limitation
    };
  }

  const prompt = [
    "Create or overwrite witness.mjs as an executable Node witness.",
    "It must print one JSON line: {\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"|\"PREDICATE_FAIL\"|...}.",
    `Behavior to test: ${options.behavior}`,
    `Symptom: ${incident.symptom}`,
    "Do not claim root cause. Do not modify unrelated files."
  ].join("\n");

  try {
    const result = await options.runner.run(["exec", "--full-auto", prompt], { cwd: overlayOut, input: prompt });
    if (result.exitCode !== 0) {
      return {
        schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
        status: "CODEX_UNAVAILABLE",
        transport: "CODEX_CLI",
        proposalId: options.proposalId,
        overlayPath,
        commandSuggestion: "node witness.mjs",
        note: `Codex CLI exited ${result.exitCode}. Template retained for human edit.`,
        limitation
      };
    }
    return {
      schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
      status: "OVERLAY_DRAFTED",
      transport: "CODEX_CLI",
      proposalId: options.proposalId,
      overlayPath,
      commandSuggestion: "node witness.mjs",
      note: "Codex CLI completed a draft overlay. Human review and freeze are still required.",
      limitation
    };
  } catch (error) {
    return {
      schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
      status: "CODEX_UNAVAILABLE",
      transport: "NONE",
      proposalId: options.proposalId,
      overlayPath,
      commandSuggestion: "node witness.mjs",
      note: `Codex CLI could not be started: ${error instanceof Error ? error.message : String(error)}`,
      limitation
    };
  }
}

/**
 * Prepare a repair worktree instruction pack from a verified proof bundle path.
 * Does not auto-merge repairs. Optional Codex runner may draft a patch.
 */
export async function repairWithCodex(options: {
  bundleDirectory: string;
  expectRoot: string;
  repository: string;
  outputDirectory: string;
  invariant?: string;
  withCodex?: boolean;
  runner?: CodexCommandRunner;
  verifyBundle: (directory: string, expectRoot?: string) => { valid: boolean; errors: readonly string[]; rootDigest: string | null };
}): Promise<RepairWithCodexResult> {
  const verification = options.verifyBundle(options.bundleDirectory, options.expectRoot);
  if (!verification.valid) {
    return {
      schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
      status: "BUNDLE_INVALID",
      transport: "NONE",
      worktreePath: null,
      instructionPath: null,
      nextCommands: [],
      note: verification.errors.join("; ") || "Proof bundle verification failed.",
      limitation: "Repair requires a verified proof bundle and externally retained root digest."
    };
  }

  const outputDirectory = resolve(options.outputDirectory);
  mkdirSync(outputDirectory, { recursive: true });
  const instructionPath = join(outputDirectory, "repair-instructions.md");
  const invariant = options.invariant ?? "Preserve the frozen witness predicate on the repaired state.";
  writeFileSync(instructionPath, [
    "# FaultLine repair worktree",
    "",
    `Verified proof root: ${verification.rootDigest ?? options.expectRoot}`,
    `Invariant (inferred / human-supplied): ${invariant}`,
    "",
    "Rules:",
    "- Use only verified evidence from the proof bundle.",
    "- Do not invent agent intent.",
    "- After editing, re-run the frozen witness on last-good, first-bad, and repaired states.",
    "",
    "Next:",
    "1. Apply a minimal fix in a fresh branch/worktree",
    "2. fl investigate git ... or fl incident continue with the same frozen witness",
    "3. Confirm three-state prevention: last-good PASS, first-bad FAIL, repaired PASS",
    ""
  ].join("\n"), "utf8");

  if (!options.withCodex || !options.runner) {
    return {
      schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
      status: "REPAIR_INSTRUCTIONS_PREPARED",
      transport: "NONE",
      worktreePath: outputDirectory,
      instructionPath,
      nextCommands: [
        `Review ${instructionPath}`,
        "Implement the repair in an isolated worktree",
        "Re-verify with the same frozen witness digest"
      ],
      note: "Repair instruction pack written. Codex was not invoked. This is not yet an isolated Git repair worktree.",
      limitation: "Automated Codex repair is opt-in via --with-codex and never auto-merges. A real repository worktree is tracked separately."
    };
  }

  try {
    const result = await options.runner.run([
      "exec",
      "--full-auto",
      `Using only the FaultLine proof at ${options.bundleDirectory}, implement a minimal repair that restores the frozen witness. Write a short patch summary to repair-summary.md. Do not claim model intent.`
    ], { cwd: outputDirectory });
    return {
      schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
      status: result.exitCode === 0 ? "REPAIR_INSTRUCTIONS_PREPARED" : "CODEX_UNAVAILABLE",
      transport: "CODEX_CLI",
      worktreePath: outputDirectory,
      instructionPath,
      nextCommands: [
        `Review ${instructionPath}`,
        "Human-review any Codex draft",
        "Re-run three-state witness verification before claiming prevention"
      ],
      note: result.exitCode === 0
        ? "Codex CLI was invoked for a draft repair. Human review and witness re-verification are required."
        : `Codex CLI exited ${result.exitCode}. Instructions remain for manual repair.`,
      limitation: "FaultLine does not intercept private Codex state; this uses an explicit CODEX_CLI transport only."
    };
  } catch (error) {
    return {
      schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
      status: "CODEX_UNAVAILABLE",
      transport: "NONE",
      worktreePath: outputDirectory,
      instructionPath,
      nextCommands: [`Review ${instructionPath}`],
      note: `Codex CLI could not be started: ${error instanceof Error ? error.message : String(error)}`,
      limitation: "FaultLine does not intercept private Codex state; this uses an explicit CODEX_CLI transport only."
    };
  }
}
