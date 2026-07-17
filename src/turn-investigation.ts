import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawn } from "node:child_process";
import { z } from "zod";
import { digestJson } from "./canonical.js";
import {
  computeEnvironmentFingerprint,
  environmentHomogeneity
} from "./environment-fingerprint.js";
import {
  readVerifiedCodexLifecycleLedger,
  type CodexLifecycleLedger
} from "./ledger.js";
import {
  auditSandboxPlan,
  createSandboxPlan,
  executeSandboxPlan,
  type SandboxCommandRunner
} from "./sandbox.js";
import { materializeFrozenOverlays } from "./safe-overlay.js";
import { TurnTreeSnapshotSchema } from "./turn-snapshot.js";
import {
  verifyFrozenWitnessRecord,
  type FrozenWitness
} from "./witness-lock.js";

export const TURN_INVESTIGATION_SCHEMA_VERSION = "faultline.turn-investigation.v1" as const;

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);
const GitObjectIdSchema = z.string().regex(/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/);
const DANGEROUS_GIT_ATTRIBUTE = /(?:^|\s)filter=[^\s]+/m;

export const TurnStateSchema = z.object({
  index: z.number().int().nonnegative(),
  turnId: z.string().min(1),
  turnOrdinal: z.number().int().positive(),
  treeDigest: GitObjectIdSchema,
  snapshotDigest: DigestSchema,
  dirty: z.boolean()
}).strict();

export type TurnState = z.infer<typeof TurnStateSchema>;

function runGit(repository: string, args: readonly string[], env: NodeJS.ProcessEnv = {}): Promise<{ exitCode: number | null; stdout: string; stderr: string }> {
  return new Promise((resolveResult) => {
    const child = spawn("git", ["-C", repository, "-c", "core.hooksPath=/nonexistent/faultline-hooks", "-c", "core.fsmonitor=false", ...args], {
      env: { ...process.env, ...env, GIT_TERMINAL_PROMPT: "0" },
      windowsHide: true
    });
    let stdout = "";
    let stderr = "";
    child.stdout?.on("data", (chunk: Buffer) => { stdout += chunk.toString("utf8"); });
    child.stderr?.on("data", (chunk: Buffer) => { stderr += chunk.toString("utf8"); });
    child.on("close", (exitCode) => resolveResult({ exitCode, stdout, stderr }));
    child.on("error", (error) => resolveResult({ exitCode: null, stdout, stderr: error.message }));
  });
}

/**
 * Extract ordered turn-tree snapshots from an observed Codex lifecycle ledger.
 */
export function turnStatesFromLedger(ledger: CodexLifecycleLedger): TurnState[] {
  const states: TurnState[] = [];
  for (const record of ledger.events) {
    if (record.event.type !== "TURN_TREE_SNAPSHOT") continue;
    const snapshot = TurnTreeSnapshotSchema.parse(record.event.payload.snapshot);
    states.push({
      index: states.length,
      turnId: record.event.payload.turnId,
      turnOrdinal: record.event.payload.turnOrdinal,
      treeDigest: snapshot.treeDigest,
      snapshotDigest: snapshot.digest,
      dirty: snapshot.dirty
    });
  }
  return states.sort((left, right) => left.turnOrdinal - right.turnOrdinal || left.index - right.index);
}

/** Returns an error when more than one snapshot claims the same turnOrdinal. */
export function duplicateTurnOrdinalError(states: readonly TurnState[]): string | null {
  const ordinals = states.map((state) => state.turnOrdinal);
  return new Set(ordinals).size === ordinals.length
    ? null
    : "Duplicate turnOrdinal in TURN_TREE_SNAPSHOT sequence.";
}

async function materializeTree(repository: string, treeDigest: string, worktree: string): Promise<void> {
  await mkdir(worktree, { recursive: true });
  const indexFile = join(worktree, ".faultline-turn-index");
  const env = { GIT_INDEX_FILE: indexFile, GIT_WORK_TREE: worktree };
  const read = await runGit(repository, ["read-tree", treeDigest], env);
  if (read.exitCode !== 0) throw new Error(`read-tree failed for ${treeDigest}: ${read.stderr}`);
  const checkout = await runGit(repository, ["checkout-index", "-a", "-f"], env);
  if (checkout.exitCode !== 0) throw new Error(`checkout-index failed for ${treeDigest}: ${checkout.stderr}`);
}

async function assertNoDangerousAttributes(worktree: string): Promise<void> {
  try {
    const attributes = await readFile(join(worktree, ".gitattributes"), "utf8");
    if (DANGEROUS_GIT_ATTRIBUTE.test(attributes)) {
      throw new Error("Refusing turn materialization: tree declares a Git filter attribute.");
    }
  } catch (error) {
    const code = error instanceof Error && "code" in error ? (error as NodeJS.ErrnoException).code : undefined;
    if (code === "ENOENT") return;
    throw error;
  }
}

export type TurnInvestigationResult = {
  schemaVersion: typeof TURN_INVESTIGATION_SCHEMA_VERSION;
  recorder: "codex-turn-tree-replay";
  nativeCodexInterception: false;
  status: "COMPLETED" | "INVALID_WITNESS" | "NO_TURN_SNAPSHOTS" | "EXECUTION_ERROR" | "ENVIRONMENT_CHANGED" | "DUPLICATE_TURN_SNAPSHOTS";
  states: TurnState[];
  transitions: Array<{ kind: "PASS_TO_FAIL" | "FAIL_TO_PASS"; before: TurnState; after: TurnState; beforeVerdict: "PASS" | "FAIL"; afterVerdict: "PASS" | "FAIL" }>;
  proof: { isProof: boolean; reason: string; evidenceGrade: "EXPERIMENTAL_TURN" | "NONE" };
  errors: string[];
};

/**
 * Replay a frozen witness across Codex turn-tree snapshots (including dirty Stops).
 * This is the Codex-native localization path; Git commit-range replay remains the mature proof fallback.
 * Evidence grade is EXPERIMENTAL_TURN until the turn path exports a full portable proof bundle (#21).
 */
export async function investigateTurnTrees(options: {
  repository: string;
  ledgerPath: string;
  frozenWitness: FrozenWitness;
  expectedFrozenDigest: string;
  image: string;
  runner?: SandboxCommandRunner;
}): Promise<TurnInvestigationResult> {
  const emptyProof = (reason: string): TurnInvestigationResult["proof"] => ({
    isProof: false,
    reason,
    evidenceGrade: "NONE"
  });

  const repository = resolve(options.repository);
  const verification = verifyFrozenWitnessRecord(options.frozenWitness, options.expectedFrozenDigest);
  if (!verification.valid || verification.externalDigestStatus !== "MATCH") {
    return {
      schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
      recorder: "codex-turn-tree-replay",
      nativeCodexInterception: false,
      status: "INVALID_WITNESS",
      states: [],
      transitions: [],
      proof: emptyProof("Frozen witness digest mismatch."),
      errors: verification.errors
    };
  }

  const ledger = readVerifiedCodexLifecycleLedger(resolve(options.ledgerPath));
  const states = turnStatesFromLedger(ledger);
  if (states.length < 2) {
    return {
      schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
      recorder: "codex-turn-tree-replay",
      nativeCodexInterception: false,
      status: "NO_TURN_SNAPSHOTS",
      states,
      transitions: [],
      proof: emptyProof("Need at least two TURN_TREE_SNAPSHOT events for turn localization."),
      errors: []
    };
  }

  const duplicateError = duplicateTurnOrdinalError(states);
  if (duplicateError) {
    return {
      schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
      recorder: "codex-turn-tree-replay",
      nativeCodexInterception: false,
      status: "DUPLICATE_TURN_SNAPSHOTS",
      states,
      transitions: [],
      proof: emptyProof("Multiple TURN_TREE_SNAPSHOT events share a turnOrdinal; refuse ambiguous localization."),
      errors: [duplicateError]
    };
  }

  const tempRoot = await mkdtemp(join(tmpdir(), "faultline-turn-investigation-"));
  const verdicts: Array<"PASS" | "FAIL" | "OTHER"> = [];
  const errors: string[] = [];
  const fingerprints = [];
  try {
    for (const state of states) {
      const worktree = join(tempRoot, `turn-${state.turnOrdinal}-${state.treeDigest.slice(0, 12)}`);
      try {
        await materializeTree(repository, state.treeDigest, worktree);
        await assertNoDangerousAttributes(worktree);
        fingerprints.push(computeEnvironmentFingerprint(worktree));
        await materializeFrozenOverlays(worktree, options.frozenWitness);
        const plan = createSandboxPlan({
          witness: { digest: options.frozenWitness.witnessDigest, command: options.frozenWitness.proposal.witness.command },
          sourceDirectory: worktree,
          mode: "DOCKER_ISOLATED",
          image: options.image
        });
        auditSandboxPlan(plan);
        const attempts: Array<"PASS" | "FAIL" | "OTHER"> = [];
        for (let attempt = 0; attempt < 3; attempt += 1) {
          const execution = await executeSandboxPlan(plan, options.runner);
          if (
            (execution.verdict === "PASS" && execution.reason === "PREDICATE_PASS")
            || (execution.verdict === "FAIL" && execution.reason === "PREDICATE_FAIL")
          ) {
            attempts.push(execution.verdict);
          } else {
            attempts.push("OTHER");
          }
        }
        const firstAttempt = attempts[0];
        const stable = firstAttempt !== undefined && firstAttempt !== "OTHER" && attempts.every((value) => value === firstAttempt)
          ? firstAttempt
          : "OTHER";
        verdicts.push(stable);
      } catch (error) {
        errors.push(error instanceof Error ? error.message : String(error));
        verdicts.push("OTHER");
      }
    }
  } finally {
    await rm(tempRoot, { recursive: true, force: true });
  }

  if (environmentHomogeneity(fingerprints) === "HETEROGENEOUS") {
    return {
      schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
      recorder: "codex-turn-tree-replay",
      nativeCodexInterception: false,
      status: "ENVIRONMENT_CHANGED",
      states,
      transitions: [],
      proof: emptyProof("Environment changed between turn states. Supply a runtime mapping for each fingerprint before proof can continue."),
      errors: [
        ...errors,
        "Environment fingerprint changed across turn snapshots; refuse cross-environment proof."
      ]
    };
  }

  const transitions: TurnInvestigationResult["transitions"] = [];
  for (let index = 1; index < states.length; index += 1) {
    const beforeVerdict = verdicts[index - 1];
    const afterVerdict = verdicts[index];
    const before = states[index - 1];
    const after = states[index];
    if (!before || !after || beforeVerdict === undefined || afterVerdict === undefined) continue;
    if (beforeVerdict === "OTHER" || afterVerdict === "OTHER" || beforeVerdict === afterVerdict) continue;
    transitions.push({
      kind: beforeVerdict === "PASS" ? "PASS_TO_FAIL" : "FAIL_TO_PASS",
      before,
      after,
      beforeVerdict,
      afterVerdict
    });
  }

  const certified = options.runner === undefined && transitions.length > 0 && errors.length === 0;
  return {
    schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
    recorder: "codex-turn-tree-replay",
    nativeCodexInterception: false,
    status: errors.length > 0 ? "EXECUTION_ERROR" : "COMPLETED",
    states,
    transitions,
    proof: {
      // Native Docker turn localization can set isProof when transitions are
      // stable, but evidenceGrade remains EXPERIMENTAL_TURN until a portable
      // turn proof bundle exists (#21). Do not equate with Git-path A-grade.
      isProof: certified,
      reason: certified
        ? "Stable structured PREDICATE_* transitions were observed across Codex turn-tree snapshots (experimental turn evidence; not a portable Git-grade proof bundle)."
        : transitions.length === 0
          ? "No stable structured turn-to-turn PASS/FAIL transitions were established."
          : "Turn replay completed without native Docker proof certification.",
      evidenceGrade: certified || transitions.length > 0 ? "EXPERIMENTAL_TURN" : "NONE"
    },
    errors
  };
}

export function turnInvestigationDigest(result: TurnInvestigationResult): string {
  return digestJson(result);
}
