/**
 * Evidence-grade vocabulary shared by commit-path and turn-path localization.
 *
 * Structural rule: the highest portable proof grade (`COMMIT_PROOF`) is reserved
 * for paths that meet the write-once Git proof-bundle contract. Turn localization
 * remains an explicitly lower tier (`EXPERIMENTAL_TURN`) until it reaches that
 * parity — even when a turn package is technically `isProof` under the turn
 * schema (bundle-eligible ≠ commit-path maturity).
 */

export const COMMIT_PATH_EVIDENCE_GRADE = "COMMIT_PROOF" as const;
export const COMMIT_PATH_EVIDENCE_LABEL = "Commit-path localization — portable proof" as const;
export const COMMIT_PATH_EVIDENCE_LABEL_UNCERTIFIED =
  "Commit-path localization — not certified as portable proof" as const;

export const TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL = "EXPERIMENTAL_TURN" as const;
export const TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL =
  "Turn localization — experimental evidence" as const;

/**
 * Reserved for a future turn/Git parity milestone. Must not be assigned while
 * turn investigation lacks full Git-path portable-proof hardening and product
 * surfaces (verify/serve parity).
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
 * Turn-path grade. Always experimental until parity lands — including when
 * `isProof` is true for the turn bundle writer/verifier.
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
