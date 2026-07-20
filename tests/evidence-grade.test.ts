import { describe, expect, it } from "vitest";
import {
  COMMIT_PATH_EVIDENCE_GRADE,
  COMMIT_PATH_EVIDENCE_LABEL,
  TURN_PATH_EVIDENCE_GRADE_PARITY_RESERVED,
  commitPathEvidence,
  turnPathEvidence
} from "../src/evidence-grade.js";
import { TURN_PATH_EVIDENCE_LABEL_PROOF } from "../src/turn-proof-promotion.js";

describe("evidence grade isolation", () => {
  it("reserves the highest portable commit grade for commit-path proof only", () => {
    expect(commitPathEvidence(true)).toEqual({
      evidenceGrade: COMMIT_PATH_EVIDENCE_GRADE,
      evidenceLabel: COMMIT_PATH_EVIDENCE_LABEL
    });
    expect(commitPathEvidence(false).evidenceGrade).toBe("NONE");
  });

  it("promotes turn localization to TURN_PROOF when checklist criteria are satisfied", () => {
    expect(turnPathEvidence({ hasTransitions: true })).toEqual({
      evidenceGrade: TURN_PATH_EVIDENCE_GRADE_PARITY_RESERVED,
      evidenceLabel: TURN_PATH_EVIDENCE_LABEL_PROOF
    });
    expect(turnPathEvidence({ hasTransitions: false }).evidenceGrade).toBe("NONE");
  });
});
