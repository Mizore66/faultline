// Package evidence holds the evidence grades and labels attached to path
// localization results.
package evidence

// Port of src/evidence-grade.ts:31-58 constants.
const (
	CommitPathEvidenceGrade            = "COMMIT_PROOF"
	CommitPathEvidenceLabel            = "Proven at commit granularity — portable and offline-verifiable"
	CommitPathEvidenceLabelUncertified = "Commit-path localization — not certified as portable proof"

	TurnPathEvidenceGradeExperimental = "EXPERIMENTAL_TURN"
	TurnPathEvidenceLabelExperimental = "Experimental turn-level evidence — not yet a portable proof"

	// TurnPathEvidenceLabelExperimentalLegacy is the pre-rename label retained
	// so older EXPERIMENTAL_TURN packages still verify.
	TurnPathEvidenceLabelExperimentalLegacy = "Turn localization — experimental evidence"

	// TurnPathEvidenceGradeParityReserved is reserved until the promotion
	// criteria are met.
	TurnPathEvidenceGradeParityReserved = "TURN_PROOF"

	AgentDraftEvidenceGrade = "AGENT_DRAFT"
	AgentDraftEvidenceLabel = "Exploratory agent draft — not human-owned; cannot export a proof bundle"

	// Port of src/turn-proof-promotion.ts:34 TURN_PATH_EVIDENCE_LABEL_PROOF.
	TurnPathEvidenceLabelProof = "Turn-level portable proof — promotion criteria retained"
)

// TurnPathEvidenceLabelsExperimental is TURN_PATH_EVIDENCE_LABELS_EXPERIMENTAL.
var TurnPathEvidenceLabelsExperimental = []string{
	TurnPathEvidenceLabelExperimental,
	TurnPathEvidenceLabelExperimentalLegacy,
}

// The checklist in src/turn-proof-promotion.ts:11-32, kept as documentation:
// externalValidation, turnBoundaryCounterfactuals and preventionIntegration
// are satisfied; multiOsSoak is open (issue #152).
// Port of src/turn-proof-promotion.ts:58 promotionCriteriaSatisfied. Only the
// default-checklist call is reachable from turnPathEvidence, and the checklist
// is frozen with multiOsSoak unsatisfied, so this is the constant false.
// (soakRowsSatisfyPromotion,
// 48-56, is unreachable and not ported).
func promotionCriteriaSatisfied() bool { return false }

// Port of src/evidence-grade.ts:74 commitPathEvidence.
func CommitPathEvidence(isProof bool) (grade, label string) {
	if isProof {
		return CommitPathEvidenceGrade, CommitPathEvidenceLabel
	}
	return "NONE", CommitPathEvidenceLabelUncertified
}

// Port of src/evidence-grade.ts:84 turnPathEvidence.
func TurnPathEvidence(hasTransitions bool) (grade, label string) {
	if !hasTransitions {
		return "NONE", TurnPathEvidenceLabelExperimental
	}
	if promotionCriteriaSatisfied() {
		return TurnPathEvidenceGradeParityReserved, TurnPathEvidenceLabelProof
	}
	return TurnPathEvidenceGradeExperimental, TurnPathEvidenceLabelExperimental
}
