import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { z } from "zod";
import { digestJson } from "./canonical.js";
import {
  GitInvestigationResultSchema,
  type GitInvestigationResult
} from "./git-investigation.js";
import {
  collectRepairPatch,
  createRepairWorktree,
  removeRepairWorktree,
  type RepairWorktreeSession
} from "./repair-worktree.js";

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
    threadId?: string | null;
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

export type RepairWithCodexStatus =
  | "BUNDLE_INVALID"
  | "REPAIR_INSTRUCTIONS_PREPARED"
  | "REPAIR_WORKTREE_READY"
  | "REPAIR_SETUP_FAILED"
  | "CODEX_DRAFT_RECORDED"
  | "REPAIR_VERIFICATION_FAILED"
  | "REPAIR_CANDIDATE_VERIFIED"
  | "CODEX_UNAVAILABLE"
  | "WORKTREE_CLEANUP_FAILED";

export type RepairWithCodexResult = {
  schemaVersion: typeof CODEX_LOOP_SCHEMA_VERSION;
  status: RepairWithCodexStatus;
  transport: "CODEX_CLI" | "NONE";
  worktreePath: string | null;
  instructionPath: string | null;
  managedRoot: string | null;
  baseCommit: string | null;
  patchPath: string | null;
  codexThreadId: string | null;
  nextCommands: readonly string[];
  note: string;
  limitation: string;
  cleanupErrors: readonly string[];
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

function readInvestigationFromBundle(bundleDirectory: string): GitInvestigationResult {
  const raw = JSON.parse(readFileSync(join(resolve(bundleDirectory), "investigation.json"), "utf8")) as unknown;
  return GitInvestigationResultSchema.parse(raw);
}

/** Prefer first PASS→FAIL after-state; otherwise the resolved descendant. */
export function selectRepairBaseCommit(investigation: GitInvestigationResult): string {
  const introduction = investigation.transitions.find((transition) => transition.kind === "PASS_TO_FAIL");
  if (introduction) return introduction.after.commit;
  if (investigation.resolvedRange?.descendant.commit) return investigation.resolvedRange.descendant.commit;
  throw new Error("Verified investigation does not identify a repair base commit.");
}

export type RepairCandidateVerifier = (context: {
  worktreePath: string;
  baseCommit: string;
  patchPath: string | null;
  bundleDirectory: string;
}) => Promise<{ ok: boolean; detail: string }>;

/**
 * Default CLI verifier for Codex repair drafts: materialize the frozen witness
 * into the isolated worktree and require a structured PREDICATE_PASS under Docker.
 * Fail closed when Docker/image/witness execution cannot prove the predicate.
 */
export function createFrozenWitnessRepairVerifier(options?: {
  runner?: import("./sandbox.js").SandboxCommandRunner;
}): RepairCandidateVerifier {
  return async (context) => {
    const { createSandboxPlan, executeSandboxPlan, auditSandboxPlan } = await import("./sandbox.js");
    const { materializeFrozenOverlays } = await import("./safe-overlay.js");
    const { FrozenWitnessSchema } = await import("./witness-lock.js");
    const investigation = readInvestigationFromBundle(context.bundleDirectory);
    const frozen = FrozenWitnessSchema.parse(
      JSON.parse(readFileSync(join(resolve(context.bundleDirectory), "witness", "frozen.json"), "utf8"))
    );
    const image = investigation.runs
      .map((run) => run.sandbox.runtime.image)
      .find((value): value is string => typeof value === "string" && /@sha256:[a-f0-9]{64}$/.test(value));
    if (!image) {
      return {
        ok: false,
        detail: "Repair verification fail-closed: proof bundle runs do not record a digest-pinned Docker image."
      };
    }
    try {
      await materializeFrozenOverlays(context.worktreePath, frozen);
      const plan = createSandboxPlan({
        witness: { digest: frozen.frozenDigest, command: frozen.proposal.witness.command },
        sourceDirectory: context.worktreePath,
        mode: "DOCKER_ISOLATED",
        image
      });
      auditSandboxPlan(plan);
      const execution = await executeSandboxPlan(plan, options?.runner);
      if (execution.verdict === "PASS" && execution.reason === "PREDICATE_PASS") {
        return { ok: true, detail: `Frozen witness PREDICATE_PASS on repaired worktree (${execution.executor}).` };
      }
      return {
        ok: false,
        detail: `Frozen witness did not PREDICATE_PASS (verdict=${execution.verdict}, reason=${execution.reason}).`
      };
    } catch (error) {
      return {
        ok: false,
        detail: `Repair verification fail-closed: ${error instanceof Error ? error.message : String(error)}`
      };
    }
  };
}

function writeRepairInstructions(options: {
  instructionPath: string;
  rootDigest: string;
  baseCommit: string;
  invariant: string;
  proofReadonlyPath: string;
  worktreePath: string | null;
}): void {
  writeFileSync(options.instructionPath, [
    "# FaultLine repair",
    "",
    `Verified proof root: ${options.rootDigest}`,
    `Repair base commit: ${options.baseCommit}`,
    `Invariant (inferred / human-supplied): ${options.invariant}`,
    options.worktreePath ? `Isolated worktree: ${options.worktreePath}` : "Isolated worktree: not created (instructions only)",
    `Proof companion (read-only): ${options.proofReadonlyPath}`,
    "",
    "Rules:",
    "- Use only verified evidence from the proof bundle.",
    "- Do not invent agent intent.",
    "- After editing, re-run the frozen witness on last-good, first-bad, and repaired states.",
    "- FaultLine never auto-merges a Codex draft.",
    "",
    "Next:",
    "1. Apply a minimal fix in the isolated worktree",
    "2. Re-verify with the same frozen witness digest",
    "3. Confirm three-state prevention: last-good PASS, first-bad FAIL, repaired PASS",
    ""
  ].join("\n"), "utf8");
}

function baseRepairResult(
  partial: Omit<RepairWithCodexResult, "schemaVersion" | "cleanupErrors"> & { cleanupErrors?: readonly string[] }
): RepairWithCodexResult {
  return {
    schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
    cleanupErrors: partial.cleanupErrors ?? [],
    ...partial
  };
}

/**
 * Prepare an isolated repair Git worktree from a verified proof bundle.
 * Optional Codex drafting runs only inside that worktree; never auto-merges.
 */
export async function repairWithCodex(options: {
  bundleDirectory: string;
  expectRoot: string;
  repository: string;
  outputDirectory: string;
  invariant?: string;
  withCodex?: boolean;
  /** When true, skip Git worktree creation and only write instructions. */
  instructionsOnly?: boolean;
  /** Retain the worktree directory after success for debugging. */
  keepWorktree?: boolean;
  runner?: CodexCommandRunner;
  /**
   * Optional post-draft verification hook (frozen witness / tests).
   * Returning ok:true may promote status to REPAIR_CANDIDATE_VERIFIED.
   */
  verifyCandidate?: (context: {
    worktreePath: string;
    baseCommit: string;
    patchPath: string | null;
    bundleDirectory: string;
  }) => Promise<{ ok: boolean; detail: string }>;
  verifyBundle: (directory: string, expectRoot?: string) => {
    valid: boolean;
    errors: readonly string[];
    rootDigest: string | null;
  };
}): Promise<RepairWithCodexResult> {
  const verification = options.verifyBundle(options.bundleDirectory, options.expectRoot);
  if (!verification.valid) {
    return baseRepairResult({
      status: "BUNDLE_INVALID",
      transport: "NONE",
      worktreePath: null,
      instructionPath: null,
      managedRoot: null,
      baseCommit: null,
      patchPath: null,
      codexThreadId: null,
      nextCommands: [],
      note: verification.errors.join("; ") || "Proof bundle verification failed.",
      limitation: "Repair requires a verified proof bundle and externally retained root digest."
    });
  }

  let investigation: GitInvestigationResult;
  let baseCommit: string;
  try {
    investigation = readInvestigationFromBundle(options.bundleDirectory);
    baseCommit = selectRepairBaseCommit(investigation);
  } catch (error) {
    return baseRepairResult({
      status: "BUNDLE_INVALID",
      transport: "NONE",
      worktreePath: null,
      instructionPath: null,
      managedRoot: null,
      baseCommit: null,
      patchPath: null,
      codexThreadId: null,
      nextCommands: [],
      note: error instanceof Error ? error.message : String(error),
      limitation: "Repair requires a completed investigation artifact inside the verified proof bundle."
    });
  }

  const outputDirectory = resolve(options.outputDirectory);
  const invariant = options.invariant ?? "Preserve the frozen witness predicate on the repaired state.";
  const rootDigest = verification.rootDigest ?? options.expectRoot;
  const limitation = "Automated Codex repair is opt-in via --with-codex and never auto-merges.";
  const repository = resolve(investigation.repository ?? options.repository);

  if (options.instructionsOnly === true) {
    mkdirSync(outputDirectory, { recursive: true, mode: 0o700 });
    const instructionPath = join(outputDirectory, "repair-instructions.md");
    writeRepairInstructions({
      instructionPath,
      rootDigest,
      baseCommit,
      invariant,
      proofReadonlyPath: resolve(options.bundleDirectory),
      worktreePath: null
    });
    return baseRepairResult({
      status: "REPAIR_INSTRUCTIONS_PREPARED",
      transport: "NONE",
      worktreePath: null,
      instructionPath,
      managedRoot: outputDirectory,
      baseCommit,
      patchPath: null,
      codexThreadId: null,
      nextCommands: [
        `Review ${instructionPath}`,
        "Re-run without --instructions-only to create an isolated Git worktree",
        "Re-verify with the same frozen witness digest"
      ],
      note: "Repair instruction pack written. Codex was not invoked. This is not an isolated Git repair worktree.",
      limitation
    });
  }

  let session: RepairWorktreeSession | null = null;
  const cleanupErrors: string[] = [];
  let status: RepairWithCodexStatus = "REPAIR_WORKTREE_READY";
  let transport: "CODEX_CLI" | "NONE" = "NONE";
  let patchPath: string | null = null;
  let codexThreadId: string | null = null;
  let note = "Isolated detached Git worktree prepared at the selected repair base; companion proof and instructions retained under the managed repair root.";
  let instructionPath: string | null = null;

  try {
    session = await createRepairWorktree({
      repository,
      baseCommit,
      bundleDirectory: options.bundleDirectory,
      outputDirectory
    });
    instructionPath = join(session.instructionsPath, "repair-instructions.md");
    writeRepairInstructions({
      instructionPath,
      rootDigest,
      baseCommit,
      invariant,
      proofReadonlyPath: session.proofReadonlyPath,
      worktreePath: session.worktreePath
    });
    writeFileSync(join(session.artifactsPath, "repair-meta.json"), `${JSON.stringify({
      schemaVersion: CODEX_LOOP_SCHEMA_VERSION,
      baseCommit,
      rootDigest,
      repairId: session.repairId
    }, null, 2)}\n`, "utf8");

    if (options.withCodex === true && options.runner) {
      transport = "CODEX_CLI";
      try {
        const prompt = [
          `Using only the verified FaultLine proof companion at ${session.proofReadonlyPath},`,
          "implement a minimal repair in this Git worktree that restores the frozen witness.",
          "Do not claim model intent. Prefer the smallest safe change.",
          "When finished, leave the worktree dirty with your edits so FaultLine can collect git diff."
        ].join(" ");
        const result = await options.runner.run(["exec", "--full-auto", prompt], {
          cwd: session.worktreePath,
          input: prompt
        });
        codexThreadId = result.threadId ?? null;
        writeFileSync(join(session.artifactsPath, "codex-thread.json"), `${JSON.stringify({
          threadId: codexThreadId,
          exitCode: result.exitCode,
          stdoutPreview: result.stdout.slice(0, 4_096),
          stderrPreview: result.stderr.slice(0, 4_096)
        }, null, 2)}\n`, "utf8");

        if (result.exitCode !== 0) {
          status = "CODEX_UNAVAILABLE";
          note = `Codex CLI exited ${result.exitCode}. Isolated worktree and instructions remain for manual repair.`;
        } else {
          const collected = await collectRepairPatch(session.worktreePath);
          patchPath = join(session.artifactsPath, "repair.patch");
          writeFileSync(patchPath, collected.patch, "utf8");
          status = "CODEX_DRAFT_RECORDED";
          note = "Codex CLI drafted repairs inside the isolated worktree. Patch collected; human review required. Not auto-merged.";

          if (options.verifyCandidate) {
            const verdict = await options.verifyCandidate({
              worktreePath: session.worktreePath,
              baseCommit,
              patchPath,
              bundleDirectory: options.bundleDirectory
            });
            writeFileSync(join(session.artifactsPath, "verification.json"), `${JSON.stringify(verdict, null, 2)}\n`, "utf8");
            if (verdict.ok) {
              status = "REPAIR_CANDIDATE_VERIFIED";
              note = `Repair candidate verified in the isolated worktree: ${verdict.detail}`;
            } else {
              status = "REPAIR_VERIFICATION_FAILED";
              note = `Repair candidate failed verification: ${verdict.detail}`;
            }
          }
        }
      } catch (error) {
        status = "CODEX_UNAVAILABLE";
        note = `Codex CLI could not be started: ${error instanceof Error ? error.message : String(error)}`;
        transport = "NONE";
      }
    }
  } catch (error) {
    return baseRepairResult({
      status: "REPAIR_SETUP_FAILED",
      transport: "NONE",
      worktreePath: null,
      instructionPath: null,
      managedRoot: existsSync(outputDirectory) ? outputDirectory : null,
      baseCommit,
      patchPath: null,
      codexThreadId: null,
      nextCommands: [],
      note: error instanceof Error ? error.message : String(error),
      limitation,
      cleanupErrors
    });
  } finally {
    if (session && options.keepWorktree !== true) {
      const cleanup = await removeRepairWorktree({
        repository: session.repository,
        worktreePath: session.worktreePath,
        managedRoot: session.managedRoot,
        keepManagedRoot: true
      });
      cleanupErrors.push(...cleanup.errors);
      if (!cleanup.cleaned) {
        if (
          status === "REPAIR_WORKTREE_READY"
          || status === "CODEX_DRAFT_RECORDED"
          || status === "REPAIR_CANDIDATE_VERIFIED"
        ) {
          status = "WORKTREE_CLEANUP_FAILED";
          note = `${note} Worktree cleanup reported errors: ${cleanup.errors.join("; ")}`;
        }
      }
    }
  }

  return baseRepairResult({
    status,
    transport,
    worktreePath: options.keepWorktree === true && session ? session.worktreePath : null,
    instructionPath,
    managedRoot: session?.managedRoot ?? null,
    baseCommit,
    patchPath,
    codexThreadId,
    nextCommands: [
      ...(instructionPath ? [`Review ${instructionPath}`] : []),
      ...(patchPath ? [`Inspect ${patchPath}`] : []),
      "Human-review any Codex draft before merge",
      "Collect NATIVE_DOCKER last-good PASS / first-bad FAIL / repaired PASS facts, then: fl prevention write --input <prevention-input.json>",
      "After three-state facts exist: fl prevention write --input <prevention-input.json>",
      "fl prevention verify currently yields a Prevention evidence summary (not fully grounded Prevention verified)"
    ],
    note,
    limitation,
    cleanupErrors
  });
}
