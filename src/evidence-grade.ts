/**
 * Evidence-grade vocabulary shared by commit-path and turn-path localization.
 *
 * `COMMIT_PROOF` — mature Git commit-range portable proof.
 * `EXPERIMENTAL_TURN` — Codex turn-tree localization with write-once turn packages.
 * `TURN_PROOF` — reserved. Do not assign for marketing.
 *
 * Turn packages already provide write-once bundles, lifecycle ledger binding,
 * tree packs, external root verification, run reconstruction, and PREDICATE_*
 * outcome validation. Portability alone is therefore **not** the remaining
 * promotion blocker.
 *
 * ## EXPERIMENTAL_TURN → TURN_PROOF promotion criteria (all required)
 *
 * 1. External repository validation (permissioned case study beyond dogfood)
 * 2. Counterfactual edit isolation chained from a selected turn boundary
 *    (`fl investigate turns --minimize` / `fl prove transition`)
 * 3. Prevention-proof integration for the turn→repair arc
 * 4. Operational soak across supported platforms (Windows / macOS / Linux)
 *
 * Promote the grade only when these named criteria are met.
 */

export const COMMIT_PATH_EVIDENCE_GRADE = "COMMIT_PROOF" as const;
export const COMMIT_PATH_EVIDENCE_LABEL =
  "Proven at commit granularity — portable and offline-verifiable" as const;
export const COMMIT_PATH_EVIDENCE_LABEL_UNCERTIFIED =
  "Commit-path localization — not certified as portable proof" as const;

export const TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL = "EXPERIMENTAL_TURN" as const;
export const TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL =
  "Experimental turn-level evidence — not yet a portable proof" as const;

/**
 * Reserved until the promotion criteria above are met. Must not be assigned
 * while any criterion remains open.
 */
export const TURN_PATH_EVIDENCE_GRADE_PARITY_RESERVED = "TURN_PROOF" as const;

export type CommitPathEvidenceGrade = typeof COMMIT_PATH_EVIDENCE_GRADE | "NONE";
export type TurnPathEvidenceGrade =
  | typeof TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL
  | typeof TURN_PATH_EVIDENCE_GRADE_PARITY_RESERVED
  | "NONE";

export type PathEvidenceAnnotation = {
  readonly evidenceGrade: string;
  readonly evidenceLabel: string;
};

/** Mature commit-path grade: only when Docker-isolated Git localization establishes proof. */
export function commitPathEvidence(isProof: boolean): PathEvidenceAnnotation {
  return isProof
    ? { evidenceGrade: COMMIT_PATH_EVIDENCE_GRADE, evidenceLabel: COMMIT_PATH_EVIDENCE_LABEL }
    : { evidenceGrade: "NONE", evidenceLabel: COMMIT_PATH_EVIDENCE_LABEL_UNCERTIFIED };
}

/**
 * Turn-path grade. Always experimental until TURN_PROOF promotion criteria land —
 * including when a turn bundle is technically `isProof` under the turn schema.
 */
export function turnPathEvidence(options: {
  readonly hasTransitions: boolean;
}): PathEvidenceAnnotation {
  return {
    evidenceGrade: options.hasTransitions
      ? TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL
      : "NONE",
    evidenceLabel: TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL
  };
}
