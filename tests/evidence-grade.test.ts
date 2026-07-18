import { describe, expect, it } from "vitest";
import {
  COMMIT_PATH_EVIDENCE_GRADE,
  COMMIT_PATH_EVIDENCE_LABEL,
  TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL,
  TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL,
  commitPathEvidence,
  turnPathEvidence
} from "../src/evidence-grade.js";

describe("evidence grade isolation", () => {
  it("reserves the highest portable grade for commit-path proof only", () => {
    expect(commitPathEvidence(true)).toEqual({
      evidenceGrade: COMMIT_PATH_EVIDENCE_GRADE,
      evidenceLabel: COMMIT_PATH_EVIDENCE_LABEL
    });
    expect(commitPathEvidence(false).evidenceGrade).toBe("NONE");
  });

  it("keeps turn localization experimental even when transitions exist", () => {
    expect(turnPathEvidence({ hasTransitions: true })).toEqual({
      evidenceGrade: TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL,
      evidenceLabel: TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL
    });
    expect(turnPathEvidence({ hasTransitions: false }).evidenceGrade).toBe("NONE");
    expect(turnPathEvidence({ hasTransitions: true }).evidenceGrade).not.toBe("TURN_PROOF");
    expect(turnPathEvidence({ hasTransitions: true }).evidenceLabel).not.toMatch(/portable proof/i);
  });
});
