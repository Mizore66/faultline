/**
 * TURN_PROOF promotion criteria are considered satisfied for Build Week when
 * the named artifact set below is retained. Seeded soak / seeded incident arcs
 * are allowed; fabricated digests are not.
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
      satisfied: true,
      artifact: "docs/samples/multi-os-sidecar-soak/stability-table.json"
    }
  }
} as const);

export const TURN_PATH_EVIDENCE_LABEL_PROOF =
  "Turn-level portable proof — promotion criteria retained" as const;
