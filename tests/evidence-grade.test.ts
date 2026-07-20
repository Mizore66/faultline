import { describe, expect, it } from "vitest";
import {
  COMMIT_PATH_EVIDENCE_GRADE,
  COMMIT_PATH_EVIDENCE_LABEL,
  TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL,
  TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL,
  commitPathEvidence,
  turnPathEvidence
} from "../src/evidence-grade.js";
import {
  TURN_PROOF_PROMOTION_CHECKLIST,
  soakRowsSatisfyPromotion
} from "../src/turn-proof-promotion.js";

describe("evidence grade isolation", () => {
  it("reserves the highest portable grade for commit-path proof only", () => {
    expect(commitPathEvidence(true)).toEqual({
      evidenceGrade: COMMIT_PATH_EVIDENCE_GRADE,
      evidenceLabel: COMMIT_PATH_EVIDENCE_LABEL
    });
    expect(commitPathEvidence(false).evidenceGrade).toBe("NONE");
  });

  it("keeps turn localization experimental while multi-OS soak remains open", () => {
    expect(TURN_PROOF_PROMOTION_CHECKLIST.criteria.multiOsSoak.satisfied).toBe(false);
    expect(turnPathEvidence({ hasTransitions: true })).toEqual({
      evidenceGrade: TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL,
      evidenceLabel: TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL
    });
    expect(turnPathEvidence({ hasTransitions: false }).evidenceGrade).toBe("NONE");
    expect(turnPathEvidence({ hasTransitions: true }).evidenceGrade).not.toBe("TURN_PROOF");
    expect(turnPathEvidence({ hasTransitions: true }).evidenceLabel).toMatch(/not yet a portable proof/i);
  });

  it("rejects seeded or synthetic origins as soak promotion evidence", () => {
    expect(soakRowsSatisfyPromotion([
      { platform: "windows", origin: "organic" },
      { platform: "linux", origin: "seeded_real_ledger" },
      { platform: "macos", origin: "seeded_real_ledger" }
    ])).toBe(false);
    expect(soakRowsSatisfyPromotion([
      { platform: "windows", origin: "organic" },
      { platform: "linux", origin: "organic" },
      { platform: "macos", origin: "external" }
    ])).toBe(true);
    expect(soakRowsSatisfyPromotion([
      { platform: "windows", origin: "organic" }
    ])).toBe(false);
  });
});
