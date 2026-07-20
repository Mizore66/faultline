/**
 * Evidence-grade vocabulary shared by commit-path and turn-path localization.
 *
 * `COMMIT_PROOF` — mature Git commit-range portable proof.
 * `EXPERIMENTAL_TURN` — legacy label retained for older packages.
 * `TURN_PROOF` — turn-path grade when promotion checklist criteria are met.
 *
 * ## EXPERIMENTAL_TURN → TURN_PROOF promotion criteria (all required)
 *
 * 1. External repository validation (permissioned case study beyond dogfood)
 * 2. Counterfactual edit isolation chained from a selected turn boundary
 * 3. Prevention-proof integration for the turn→repair arc
 * 4. Operational soak across supported platforms (Windows / macOS / Linux)
 *
 * See `turn-proof-promotion.ts` for the retained checklist artifacts.
 */

import {
  TURN_PATH_EVIDENCE_LABEL_PROOF,
  TURN_PROOF_PROMOTION_CHECKLIST
} from "./turn-proof-promotion.js";

export const COMMIT_PATH_EVIDENCE_GRADE = "COMMIT_PROOF" as const;
export const COMMIT_PATH_EVIDENCE_LABEL =
  "Proven at commit granularity — portable and offline-verifiable" as const;
export const COMMIT_PATH_EVIDENCE_LABEL_UNCERTIFIED =
  "Commit-path localization — not certified as portable proof" as const;

export const TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL = "EXPERIMENTAL_TURN" as const;
export const TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL =
  "Experimental turn-level evidence — not yet a portable proof" as const;

/** Pre-rename label retained so older EXPERIMENTAL_TURN packages still verify. */
export const TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL_LEGACY =
  "Turn localization — experimental evidence" as const;

export const TURN_PATH_EVIDENCE_LABELS_EXPERIMENTAL = Object.freeze([
  TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL,
  TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL_LEGACY
] as const);

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

function promotionCriteriaSatisfied(): boolean {
  const criteria = TURN_PROOF_PROMOTION_CHECKLIST.criteria;
  return (
    criteria.externalValidation.satisfied
    && criteria.turnBoundaryCounterfactuals.satisfied
    && criteria.preventionIntegration.satisfied
    && criteria.multiOsSoak.satisfied
  );
}

/**
 * Turn-path grade. Returns TURN_PROOF when promotion checklist criteria are
 * satisfied and transitions exist; otherwise EXPERIMENTAL_TURN / NONE.
 */
export function turnPathEvidence(options: {
  readonly hasTransitions: boolean;
}): PathEvidenceAnnotation {
  if (!options.hasTransitions) {
    return {
      evidenceGrade: "NONE",
      evidenceLabel: TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL
    };
  }
  if (promotionCriteriaSatisfied()) {
    return {
      evidenceGrade: TURN_PATH_EVIDENCE_GRADE_PARITY_RESERVED,
      evidenceLabel: TURN_PATH_EVIDENCE_LABEL_PROOF
    };
  }
  return {
    evidenceGrade: TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL,
    evidenceLabel: TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL
  };
}
