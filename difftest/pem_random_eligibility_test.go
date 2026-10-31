//go:build faultline_test

package difftest

import (
	"crypto/sha256"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
)

// Catches a stale probability attached to a different key or a wrong modulus,
// repeated factor, nonunit base coordinate, or reducibility assumption.
func TestPEMRandomEligibility(t *testing.T) {
	block, rest := pem.Decode([]byte(pemRandomBinaryKey))
	if block == nil || len(rest) != 0 || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 {
		t.Fatal("random contract must bind one exact public PEM")
	}
	hash := sha256.Sum256(block.Bytes)
	if hex.EncodeToString(hash[:]) != "6c6c70e59ea4a37c729d5721d2ebb71c701c4eced6575d7703eb5dda86d18a02" {
		t.Fatal("random contract key differs from the observed oracle input")
	}
	var spki struct {
		Algorithm pkix.AlgorithmIdentifier
		PublicKey asn1.BitString
	}
	if rest, err := asn1.Unmarshal(block.Bytes, &spki); err != nil || len(rest) != 0 {
		t.Fatalf("SPKI: %v", err)
	}
	var params struct {
		Version int
		Field   struct {
			OID        asn1.ObjectIdentifier
			Parameters struct {
				M     int
				Basis asn1.ObjectIdentifier
				K     int
			}
		}
		Curve    asn1.RawValue
		Base     []byte
		Order    *big.Int
		Cofactor int `asn1:"optional"`
	}
	if rest, err := asn1.Unmarshal(spki.Algorithm.Parameters.FullBytes, &params); err != nil || len(rest) != 0 {
		t.Fatalf("explicit parameters: %v", err)
	}
	if spki.Algorithm.Algorithm.String() != "1.2.840.10045.2.1" || params.Version != 1 ||
		params.Field.OID.String() != "1.2.840.10045.1.2" || params.Field.Parameters.Basis.String() != "1.2.840.10045.1.2.3.2" ||
		params.Field.Parameters.M != 163 || params.Field.Parameters.K != 138 {
		t.Fatal("key no longer has the proven binary trinomial")
	}
	modulus := new(big.Int).SetBit(new(big.Int), params.Field.Parameters.M, 1)
	modulus.SetBit(modulus, params.Field.Parameters.K, 1).SetBit(modulus, 0, 1)
	product, units := big.NewInt(1), big.NewInt(1)
	factors := []*big.Int{}
	for _, h := range []string{"390b", "562786093", "ef6db5e8a5b747", "209acdf2f3357f7f"} {
		factor, ok := new(big.Int).SetString(h, 16)
		if !ok {
			t.Fatal("invalid factor")
		}
		for _, other := range factors {
			if pemPolynomialGCD(factor, other).Cmp(big.NewInt(1)) != 0 {
				t.Fatal("factorization is not squarefree")
			}
		}
		factors = append(factors, factor)
		degree := factor.BitLen() - 1
		// Irreducibility over GF(2): no factor of degree <= degree/2,
		// and x^(2^degree) == x modulo the polynomial.
		x := big.NewInt(2)
		for i := 1; i <= degree; i++ {
			x = pemPolynomialMod(pemPolynomialMul(x, x), factor)
			if i <= degree/2 && pemPolynomialGCD(new(big.Int).Xor(x, big.NewInt(2)), factor).Cmp(big.NewInt(1)) != 0 {
				t.Fatalf("factor %s is reducible", h)
			}
		}
		if x.Cmp(big.NewInt(2)) != 0 {
			t.Fatalf("factor %s fails Frobenius identity", h)
		}
		product = pemPolynomialMul(product, factor)
		units.Mul(units, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(degree)), big.NewInt(1)))
	}
	if product.Cmp(modulus) != 0 {
		t.Fatal("factors do not reconstruct the key's modulus")
	}
	if len(params.Base) != 22 || params.Base[0] != 3 ||
		pemPolynomialGCD(new(big.Int).SetBytes(params.Base[1:]), modulus).Cmp(big.NewInt(1)) != 0 {
		t.Fatal("actual compressed base x must be a unit")
	}
	denominator := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(params.Field.Parameters.M)), big.NewInt(1))
	numerator := new(big.Int).Sub(denominator, units)
	if numerator.String() != pemRandomNumerator || denominator.String() != pemRandomDenominator {
		t.Fatalf("nonzero uniform blinding probability differs: %s/%s", numerator, denominator)
	}
}

func pemPolynomialMod(a, b *big.Int) *big.Int {
	a = new(big.Int).Set(a)
	for a.BitLen() >= b.BitLen() {
		a.Xor(a, new(big.Int).Lsh(b, uint(a.BitLen()-b.BitLen())))
	}
	return a
}

func pemPolynomialGCD(a, b *big.Int) *big.Int {
	for b.Sign() != 0 {
		a, b = b, pemPolynomialMod(a, b)
	}
	return a
}

func pemPolynomialMul(a, b *big.Int) *big.Int {
	out := new(big.Int)
	for i := 0; i < b.BitLen(); i++ {
		if b.Bit(i) != 0 {
			out.Xor(out, new(big.Int).Lsh(a, uint(i)))
		}
	}
	return out
}

// Boundaries calculated independently by summing the upper tail at 90 decimal
// digits, not by the lower-tail subtraction used in the decision function.
// Catches an off-by-one tail or a widened/narrowed probability budget.
func TestPEMRandomBinomialBoundaries(t *testing.T) {
	c := pemCase{Text: pemRandomBinaryKey, Pub: true}
	for _, tc := range []struct{ n, allowed, refused int }{
		{200, 4, 5}, {400, 5, 6}, {4200, 9, 10}, {4400, 9, 10},
	} {
		t.Run(fmt.Sprint(tc.n), func(t *testing.T) {
			for _, k := range []int{tc.allowed, tc.refused} {
				counts := map[string]int{pemRandomCommon: tc.n - k, pemRandomRare: k}
				other := map[string]int{pemRandomCommon: tc.n}
				for _, sides := range [][2]map[string]int{{counts, other}, {other, counts}} {
					why := pemRepeatedDifference(c, sides[0], sides[1])
					if (why != "") != (k == tc.refused) {
						t.Fatalf("rare=%d n=%d: %s", k, tc.n, why)
					}
				}
			}
		})
	}
}
