package gitproof

import "testing"

func TestCanonicalTimestamp(t *testing.T) {
	for in, want := range map[string]bool{
		"2026-07-16T11:00:00.000Z":    true,
		"2021-02-30T00:00:00.000Z":    false,
		"2021-02-28T24:00:00.000Z":    false,
		"+275760-09-13T00:00:00.000Z": true,
		"0000-01-01T00:00:00.000Z":    true,
		"2021-04-31T00:00:00.000Z":    false,
		"2026-07-16T11:00:00Z":        false,
		"-000000-01-01T00:00:00.000Z": false,
	} {
		if canonicalTimestamp(in) != want {
			t.Errorf("canonicalTimestamp(%q) != %v", in, want)
		}
	}
}

func TestCommittedLedgersVerify(t *testing.T) {
	for _, base := range []string{"git-partially-bound", "git-fully-bound"} {
		v := VerifyCodexLifecycleLedger(baseFile(t, base, "lifecycle/ledger.json"))
		if !v.Valid || v.HeadHash == nil {
			t.Fatalf("%s: %+v", base, v)
		}
	}
}
