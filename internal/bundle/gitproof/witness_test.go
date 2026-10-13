package gitproof

import "testing"

func TestCommittedFrozenWitnessVerifies(t *testing.T) {
	frozen := baseFile(t, "git-unbound", "witness/frozen.json")
	digest := frozen.Get("frozenDigest").Str()
	v := VerifyFrozenWitnessRecord(frozen, digest, true)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" || v.Approval == nil || v.Approval.Actor != "reviewer@example.test" {
		t.Fatalf("%+v", v)
	}
	if v := VerifyFrozenWitnessRecord(frozen, "nope", true); v.Valid || v.ExternalDigestStatus != "MISMATCH" || v.Errors[0] != "expected frozen digest is not a valid sha256 digest" {
		t.Fatalf("%+v", v)
	}
}
