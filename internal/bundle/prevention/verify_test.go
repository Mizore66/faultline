package prevention

import (
	"path/filepath"
	"testing"
)

func TestVerifiesCommittedBases(t *testing.T) {
	for base, classification := range map[string]string{
		"prevention-summary":  "PREVENTION_EVIDENCE_SUMMARY",
		"prevention-verified": "PREVENTION_VERIFIED",
	} {
		r := Verify(filepath.Join("..", "..", "..", "difftest", "testdata", "bases", base), "", false)
		if !r.Valid || r.Classification == nil || *r.Classification != classification || r.ExternalRootStatus != "NOT_PROVIDED" {
			t.Fatalf("%s: %+v", base, r)
		}
		if r := Verify(filepath.Join("..", "..", "..", "difftest", "testdata", "bases", base), "", true); r.Valid || r.ExternalRootStatus != "MISMATCH" {
			t.Fatalf(`%s: TS treats --expect-root "" as provided (=== undefined check)`, base)
		}
	}
}
