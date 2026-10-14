//go:build faultline_test

package ledgerbind

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
)

func TestBindCommittedBase(t *testing.T) {
	ledger := gitproof.BaseFileForTest(t, "git-fully-bound", "lifecycle/ledger.json")
	inv := gitproof.BaseFileForTest(t, "git-fully-bound", "investigation.json")
	bound, err := Create(ledger, inv, "lifecycle/ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	v := Verify(bound, bound.Get("bindingDigest").Str(), true)
	if !v.Get("valid").Bool() || v.Get("externalDigestStatus").Str() != "MATCH" {
		t.Fatalf("%v", v)
	}
	if r := Bind(ledger, inv); !r.Get("valid").Bool() {
		t.Fatalf("%v", r.Get("errors"))
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	ledger := gitproof.BaseFileForTest(t, "git-fully-bound", "lifecycle/ledger.json")
	inv := gitproof.BaseFileForTest(t, "git-fully-bound", "investigation.json")
	bound, err := Create(ledger, inv, "")
	if err != nil {
		t.Fatal(err)
	}
	if v := Verify(bound, "nope", true); v.Get("valid").Bool() || v.Get("externalDigestStatus").Str() != "MATCH" && v.Get("externalDigestStatus").Str() != "MISMATCH" {
		t.Fatalf("%v", v)
	}
	bound.Obj().Set("bindingDigest", bound.Get("ledgerHeadHash"))
	v := Verify(bound, "", false)
	if v.Get("valid").Bool() || !strings.Contains(v.Get("errors").Items()[0].Str(), "bindingDigest does not match") {
		t.Fatalf("%v", v)
	}
}
