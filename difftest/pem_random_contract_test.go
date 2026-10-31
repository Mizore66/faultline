//go:build faultline_test

package difftest

import (
	"fmt"
	"math/big"
)

// This exact public input has a squarefree, reducible binary modulus
// x^163+x^138+1, with irreducible factor degrees 13,34,55,61. Its base x is
// a unit. OpenSSL's nonzero 163-bit inverse blinding is a nonunit with
// probability (2^163 - product(2^degree-1) - 1)/(2^163-1). The third draw
// survives the decoder's error marks; a nonunit omits invalid-compressed-point.
// The eligibility and seeded tests independently pin the DER and this mechanism.
// Node 22.22.2/OpenSSL 3.5.5 emitted both exact full outcomes in a fixed sample.
// Do not extend eligibility to sibling keys without their own evidence.
const (
	pemRandomNumerator   = "1427248373187946522121644534739646695254007806"
	pemRandomDenominator = "11692013098647223345629478661730264157247460343807"
)

func pemHasRandomContract(c pemCase) bool {
	return c.Pub && c.Text == pemRandomBinaryKey
}

func pemRandomCountsDiffer(counts map[string]int) string {
	n, rare := 0, 0
	for outcome, count := range counts {
		if outcome != pemRandomCommon && outcome != pemRandomRare {
			return fmt.Sprintf("unproved random PEM outcome: %s", outcome)
		}
		if count <= 0 {
			return "nonpositive random PEM outcome count"
		}
		n += count
		if outcome == pemRandomRare {
			rare = count
		}
	}
	if n <= 0 {
		return "empty random PEM sample"
	}
	if rare == 0 {
		return ""
	}
	// Exact binomial upper tail, no floating-point or normal approximation:
	// P(X >= rare) = 1 - sum(i=0..rare-1) C(n,i)*a^i*(b-a)^(n-i)/b^n.
	// Reject below alpha=1e-9 per implementation per comparison. Even across
	// both CI events and both repeat batches this exact-key budget is <=8e-9.
	// Zero observations are legitimate at these sample sizes; deterministic
	// seeded tests separately require both branches, including their full stacks.
	a, _ := new(big.Int).SetString(pemRandomNumerator, 10)
	b, _ := new(big.Int).SetString(pemRandomDenominator, 10)
	common := new(big.Int).Sub(b, a)
	total := new(big.Int).Exp(b, big.NewInt(int64(n)), nil)
	term := new(big.Int).Exp(common, big.NewInt(int64(n)), nil)
	sum := new(big.Int).Set(term)
	for i := 1; i < rare; i++ {
		term.Mul(term, big.NewInt(int64(n-i+1)))
		term.Mul(term, a)
		divisor := new(big.Int).Mul(big.NewInt(int64(i)), common)
		term.Quo(term, divisor)
		sum.Add(sum, term)
	}
	tail := new(big.Int).Sub(total, sum)
	if tail.Mul(tail, big.NewInt(1_000_000_000)).Cmp(total) < 0 {
		return fmt.Sprintf("random PEM rare outcome occurred %d of %d times (exact binomial upper tail < 1e-9)", rare, n)
	}
	return ""
}
