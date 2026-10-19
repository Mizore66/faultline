package evidence

import "testing"

func TestEvidenceGrades(t *testing.T) {
	if g, l := CommitPathEvidence(true); g != "COMMIT_PROOF" || l != "Proven at commit granularity — portable and offline-verifiable" {
		t.Fatalf("%s %s", g, l)
	}
	if g, l := CommitPathEvidence(false); g != "NONE" || l != "Commit-path localization — not certified as portable proof" {
		t.Fatalf("%s %s", g, l)
	}
	if g, _ := TurnPathEvidence(true); g != "EXPERIMENTAL_TURN" {
		t.Fatalf("promotion criteria are not met today, got %s", g)
	}
	if g, l := TurnPathEvidence(false); g != "NONE" || l != TurnPathEvidenceLabelExperimental {
		t.Fatalf("%s %s", g, l)
	}
}
