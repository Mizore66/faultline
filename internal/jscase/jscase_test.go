package jscase

import "testing"

// Expectations from Node 22.22.2: "…".toLocaleLowerCase("en-US").
func TestLowerMatchesNode(t *testing.T) {
	for in, want := range map[string]string{
		"ABC":            "abc",
		"ʰΣ":             "ʰσ", // ʰ is Cased and Case_Ignorable: skipped, so no cased letter before Σ
		"aΣ":             "aς",
		"aΣb":            "aσb",
		"aΣ.":            "aς.",
		"a'Σ":            "a'ς", // ' is Case_Ignorable
		"ᵃΣᵃ":            "ᵃσᵃ",
		"İ":              "i̇",
		"ΑΣ\xed\xa0\x80": "ας\xed\xa0\x80", // a lone surrogate after Σ is uncased: final
	} {
		if got := Lower(in); got != want {
			t.Errorf("Lower(%q) = %q, want %q", in, got, want)
		}
	}
}
