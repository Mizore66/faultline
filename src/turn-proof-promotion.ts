/**
 * Honest scoreboard for EXPERIMENTAL_TURN → TURN_PROOF promotion criteria.
 *
 * All four criteria must be satisfied before `turnPathEvidence` may emit
 * `TURN_PROOF`. Multi-OS soak requires per-OS rows with origin `organic` or
 * `external` only — seeded/synthetic origins never satisfy the soak gate.
 *
 * Do not self-grant criteria. Do not invent soak stats.
 */

export const TURN_PROOF_PROMOTION_CHECKLIST = Object.freeze({
  schemaVersion: "faultline.turn-proof-promotion.v1",
  criteria: {
    externalValidation: {
      satisfied: true,
      artifact: "docs/external-case-study-mumbcs.md"
    },
    turnBoundaryCounterfactuals: {
      satisfied: true,
      artifact: "src/turn-minimization-bridge.ts + fl investigate turns --minimize"
    },
    preventionIntegration: {
      satisfied: true,
      artifact: "src/prevention-from-turn.ts"
    },
    multiOsSoak: {
      /** Windows organic dogfood exists; macOS/Linux + second developer remain open (#152). */
      satisfied: false,
      artifact: "https://github.com/Mizore66/faultline/issues/152"
    }
  }
} as const);

export const TURN_PATH_EVIDENCE_LABEL_PROOF =
  "Turn-level portable proof — promotion criteria retained" as const;

export type SoakRowOrigin = "organic" | "external" | "seeded_real_ledger" | "synthetic" | string;

export type SoakStabilityRow = {
  readonly platform: string;
  readonly origin: SoakRowOrigin;
};

/**
 * Returns true only when every row is organic or external.
 * Seeded/synthetic/scripted origins never satisfy the multi-OS soak gate.
 */
export function soakRowsSatisfyPromotion(
  rows: readonly SoakStabilityRow[] | undefined
): boolean {
  if (!rows || rows.length === 0) return false;
  const platforms = new Set(rows.map((row) => row.platform.toLowerCase()));
  const required = ["windows", "linux", "macos"];
  if (!required.every((platform) => platforms.has(platform))) return false;
  return rows.every((row) => row.origin === "organic" || row.origin === "external");
}

export function promotionCriteriaSatisfied(
  checklist: typeof TURN_PROOF_PROMOTION_CHECKLIST = TURN_PROOF_PROMOTION_CHECKLIST
): boolean {
  const criteria = checklist.criteria;
  return (
    criteria.externalValidation.satisfied
    && criteria.turnBoundaryCounterfactuals.satisfied
    && criteria.preventionIntegration.satisfied
    && criteria.multiOsSoak.satisfied
  );
}
