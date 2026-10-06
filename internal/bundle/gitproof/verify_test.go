package gitproof

import (
	"path/filepath"
	"testing"
)

func TestVerifiesCommittedSample(t *testing.T) {
	r := Verify(filepath.Join("..", "..", "..", "docs", "samples", "self-incident-commit-proof"), "", false)
	want := "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9"
	if !r.Valid || r.CheckedFiles != 18 || r.RootDigest == nil || *r.RootDigest != want || r.ExternalRootStatus != "NOT_PROVIDED" {
		t.Fatalf("%+v", r)
	}
	if r := Verify(filepath.Join("..", "..", "..", "docs", "samples", "self-incident-commit-proof"), "", true); r.Valid || r.Errors[0] != "externally supplied Git proof root is not a sha256 digest" {
		t.Fatalf("%+v", r)
	}
}
