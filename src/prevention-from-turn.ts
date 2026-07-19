import { bridgeTurnTransitionToMinimizationCommits } from "./turn-minimization-bridge.js";
import type { TurnInvestigationResult } from "./turn-investigation.js";
import {
  writePreventionProofFromVerifiedArtifacts,
  type PreventionExportResult,
  type RepairedPreventionRun
} from "./prevention-from-artifacts.js";

export type TurnPreventionMaterializationFailure = {
  readonly ok: false;
  readonly classification: "PREVENTION_EVIDENCE_SUMMARY";
  readonly reasons: readonly string[];
};

export type TurnPreventionMaterializationSuccess = {
  readonly ok: true;
  readonly classification: "PREVENTION_VERIFIED";
  readonly materialization: {
    readonly beforeCommit: string;
    readonly afterCommit: string;
    readonly beforeTree: string;
    readonly afterTree: string;
    readonly transitionIndex: number;
    readonly note: string;
  };
  readonly written: Extract<PreventionExportResult, { ok: true }>["written"];
};

export type TurnPreventionMaterializationResult =
  | TurnPreventionMaterializationFailure
  | TurnPreventionMaterializationSuccess;

/**
 * Reach PREVENTION_VERIFIED from a turn investigation only by materialising
 * PASS→FAIL turn trees into Git-shaped orphan commits, then delegating to the
 * existing Git proof-bundle prevention verifier. Failures stay summary-only.
 */
export function writePreventionProofFromTurnMaterialization(options: {
  repository: string;
  turnResult: Pick<TurnInvestigationResult, "transitions">;
  transitionIndex?: number;
  bundleDirectory: string;
  expectRoot: string;
  outputDirectory: string;
  repaired: {
    commit: string;
    tree: string;
    runs: readonly RepairedPreventionRun[];
  };
  repairPatchDigest: string;
  repairBaseTree?: string;
  codexThreadId?: string;
  hardGuardArtifactDigests?: readonly string[];
}): TurnPreventionMaterializationResult {
  let materialization: TurnPreventionMaterializationSuccess["materialization"];
  try {
    const bridged = bridgeTurnTransitionToMinimizationCommits(
      options.repository,
      options.turnResult,
      options.transitionIndex ?? 0
    );
    materialization = {
      beforeCommit: bridged.beforeCommit,
      afterCommit: bridged.afterCommit,
      beforeTree: bridged.beforeTree,
      afterTree: bridged.afterTree,
      transitionIndex: bridged.transitionIndex,
      note: bridged.note
    };
  } catch (error) {
    return {
      ok: false,
      classification: "PREVENTION_EVIDENCE_SUMMARY",
      reasons: [
        `Turn→Git materialization refused: ${error instanceof Error ? error.message : String(error)}`,
        "PREVENTION_VERIFIED requires successful PASS→FAIL materialization into Git-shaped commits before the existing Git prevention verifier runs."
      ]
    };
  }

  const exportResult = writePreventionProofFromVerifiedArtifacts({
    bundleDirectory: options.bundleDirectory,
    expectRoot: options.expectRoot,
    outputDirectory: options.outputDirectory,
    repaired: options.repaired,
    repairPatchDigest: options.repairPatchDigest,
    ...(options.repairBaseTree === undefined ? {} : { repairBaseTree: options.repairBaseTree }),
    ...(options.codexThreadId === undefined ? {} : { codexThreadId: options.codexThreadId }),
    ...(options.hardGuardArtifactDigests === undefined
      ? {}
      : { hardGuardArtifactDigests: options.hardGuardArtifactDigests })
  });

  if (!exportResult.ok) {
    return {
      ok: false,
      classification: "PREVENTION_EVIDENCE_SUMMARY",
      reasons: [
        ...exportResult.reasons,
        "Turn trees were materialised, but the existing Git prevention verifier did not accept the package."
      ]
    };
  }

  return {
    ok: true,
    classification: "PREVENTION_VERIFIED",
    materialization,
    written: exportResult.written
  };
}
