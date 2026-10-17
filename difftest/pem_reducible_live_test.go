//go:build faultline_test

package difftest

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// Explicit EC parameters OpenSSL accepts without checking the field: a
// reducible trinomial or pentanomial (GF(2)[x]/(p) is then a ring with
// zero divisors) or a composite "prime". OpenSSL's arithmetic fails there
// where an inverse does not exist; round 9 (r9-1 #1, #2) found Go assuming
// a field. Its random blinding and solve_quad's random rho can change the
// outcome only when a random value shares a factor with p, so these
// polynomials and moduli have only factors of degree (bits) above 30 and
// every case gives the same Node result on every run; the inputs whose
// outcome needs a non-unit (x, y or priv·G sharing a factor with p) are
// built from the factors themselves.

// reduciblePolys are reducible trinomials and pentanomials with factors of
// degree 32 or more only (odd and even m; the even ones exercise
// solve_quad's random rho path): m, the middle exponents and one factor.
var reduciblePolys = []struct {
	m      int
	k      []int
	factor string
}{
	{69, []int{10}, "1a691081d"},          // factors of degree 32, 37
	{75, []int{37}, "111ce2d015"},         // 36, 39
	{83, []int{3}, "12cfa9e22f"},          // 36, 47
	{163, []int{1}, "1d9eabf2b3b"},        // 40, 123
	{71, []int{17, 37, 45}, "2c0d00759"},  // 33, 38
	{73, []int{14, 44, 69}, "169b52773"},  // 32, 41
	{81, []int{18, 64, 76}, "174b3b8c5b"}, // 36, 45
	{66, []int{20}, "200000401"},          // 33, 33
	{70, []int{4}, "800000005"},           // 35, 35
	{78, []int{8}, "8000000011"},          // 39, 39
	{68, []int{2, 12, 56}, "410000043"},   // 34, 34
	{78, []int{14, 38, 66}, "8200080081"}, // 39, 39
	{72, []int{32, 39, 54}, "44094b93b"},  // 34, 38
	{80, []int{17, 28, 52}, "ce285c8b9f"}, // 39, 41
}

// gf2 helpers for building the cases (schoolbook; values are small).
func gf2Mod(a, p *big.Int) *big.Int {
	r := new(big.Int).Set(a)
	d := p.BitLen() - 1
	for r.BitLen()-1 >= d {
		r.Xor(r, new(big.Int).Lsh(p, uint(r.BitLen()-1-d)))
	}
	return r
}

func gf2Mul(a, b, p *big.Int) *big.Int {
	r := new(big.Int)
	for i := 0; i < b.BitLen(); i++ {
		if b.Bit(i) == 1 {
			r.Xor(r, new(big.Int).Lsh(a, uint(i)))
		}
	}
	return gf2Mod(r, p)
}

func randBits(r *rand.Rand, n int) *big.Int {
	v := new(big.Int)
	for i := 0; i < n; i++ {
		if r.IntN(2) == 1 {
			v.SetBit(v, i, 1)
		}
	}
	return v
}

// genReducibleCases builds EC keys over reducible binary polynomials and
// composite prime moduli: SPKIs with uncompressed, compressed and hybrid
// points (both parity bits), compressed and hybrid generators in the
// parameters, x or y a zero divisor, and EC private keys without the
// public point (priv·G on the ladder, cofactor given, and on wNAF, cofactor
// 0), some followed by the Ed25519 key a keyring would hold.
func genReducibleCases(m pemMaterial) []pemCase {
	r := rand.New(rand.NewPCG(9, 1))
	oidEC := derOID("2a8648ce3d0201")
	tails := []string{"", pemWrap("PUBLIC KEY", m.spki, 64), pemWrap("CERTIFICATE", m.cert, 64), pemWrap("PRIVATE KEY", m.pkcs8, 64)}
	var cases []pemCase
	add := func(label string, der []byte) {
		text := pemWrap(label, der, 64) + tails[r.IntN(len(tails))]
		cases = append(cases, pemCase{Text: text, Pub: true}, pemCase{Text: text, Pub: false})
	}
	for _, rp := range reduciblePolys {
		p := new(big.Int).SetBit(new(big.Int), rp.m, 1)
		p.SetBit(p, 0, 1)
		for _, k := range rp.k {
			p.SetBit(p, k, 1)
		}
		factor := bigHex(rp.factor)
		n := (rp.m + 7) / 8
		fe := func(v *big.Int) []byte { return v.FillBytes(make([]byte, n)) }
		var basis []byte
		if len(rp.k) == 1 {
			basis = derSeq(derSmall(int64(rp.m)), derOID("2a8648ce3d01020302"), derSmall(int64(rp.k[0])))
		} else {
			basis = derSeq(derSmall(int64(rp.m)), derOID("2a8648ce3d01020303"),
				derSeq(derSmall(int64(rp.k[0])), derSmall(int64(rp.k[1])), derSmall(int64(rp.k[2]))))
		}
		field := derSeq(derOID("2a8648ce3d0102"), basis)
		for trial := 0; trial < 6; trial++ {
			a := []*big.Int{big.NewInt(0), big.NewInt(1), randBits(r, rp.m)}[r.IntN(3)]
			gx, gy := randBits(r, rp.m), randBits(r, rp.m)
			switch trial {
			case 4: // x a zero divisor: the hybrid parity's y/x has no inverse
				gx = gf2Mul(factor, randBits(r, rp.m-factor.BitLen()+1), p)
			case 5: // y a zero divisor
				gy = gf2Mul(factor, randBits(r, rp.m-factor.BitLen()+1), p)
			}
			x2 := gf2Mul(gx, gx, p)
			b := new(big.Int).Xor(gf2Mul(gy, gy, p), gf2Mul(gx, gy, p))
			b.Xor(b, gf2Mul(x2, gx, p)).Xor(b, gf2Mul(a, x2, p))
			curve := derSeq(derTLV(4, fe(a)), derTLV(4, fe(b)))
			unc := append(append([]byte{4}, fe(gx)...), fe(gy)...)
			order := new(big.Int).SetBit(new(big.Int), rp.m-1, 1)
			order.Add(order, big.NewInt(1+2*int64(r.IntN(1000))))
			if trial == 3 {
				// A small order: ec_guess_cofactor gives 0, so priv·G
				// takes ossl_ec_wNAF_mul instead of the ladder.
				order = big.NewInt(int64(1000003 + 2*r.IntN(1000)))
			}
			params := func(base []byte, cof []byte) []byte {
				return derSeq(derSmall(1), field, curve, derTLV(4, base), derInt(order), cof)
			}
			spki := func(prm, pt []byte) []byte {
				return derSeq(derSeq(oidEC, prm), derTLV(3, append([]byte{0}, pt...)))
			}
			prm := params(unc, nil)
			add("PUBLIC KEY", spki(prm, unc))
			for yb := byte(0); yb < 2; yb++ {
				add("PUBLIC KEY", spki(prm, append([]byte{6 | yb}, unc[1:]...)))
				add("PUBLIC KEY", spki(prm, append([]byte{2 | yb}, fe(gx)...)))
				add("PUBLIC KEY", spki(params(append([]byte{2 | yb}, fe(gx)...), nil), unc))
				add("PUBLIC KEY", spki(params(append([]byte{6 | yb}, unc[1:]...), nil), unc))
			}
			for _, cof := range [][]byte{nil, derSmall(2), derSmall(0)} {
				priv := new(big.Int).Add(big.NewInt(2), randBits(r, 40))
				ecpriv := derSeq(derSmall(1), derTLV(4, fe(priv)), derTLV(0xa0, params(unc, cof)))
				add("EC PRIVATE KEY", ecpriv)
				add("PRIVATE KEY", derSeq(derSmall(0), derSeq(oidEC, params(unc, cof)),
					derTLV(4, derSeq(derSmall(1), derTLV(4, fe(priv))))))
			}
		}
	}
	// Composite "primes": two primes above 2^40 (p ≡ 3 mod 4, so BN_mod_sqrt
	// takes the deterministic exponent path), with x a multiple of one of
	// them for the decompression and priv·G cases.
	// p = q1·q2, and p = q1² (a prime power: ossl_ec_GFp_mont_field_inv's
	// a^(p-2) is 0, "cannot invert", once a shares the factor; BN_mod_sqrt
	// sees every candidate as a residue and gives up).
	q1, q2 := bigHex("100000000007"), bigHex("800000000005")
	for trial := 0; trial < 24; trial++ {
		pc := new(big.Int).Mul(q1, q2)
		if trial >= 12 {
			pc.Mul(q1, q1)
		}
		n := (pc.BitLen() + 7) / 8
		fe := func(v *big.Int) []byte { return v.FillBytes(make([]byte, n)) }
		randMod := func() *big.Int { return new(big.Int).Mod(randBits(r, pc.BitLen()+8), pc) }
		a, gx, gy := randMod(), randMod(), randMod()
		if trial%3 == 1 {
			gx.Mul(q1, big.NewInt(int64(1+r.IntN(1000))))
		}
		if trial%3 == 2 {
			gy.Mul([]*big.Int{q2, q1}[trial/12], big.NewInt(int64(1+r.IntN(1000))))
		}
		b := new(big.Int).Mul(gy, gy)
		b.Sub(b, new(big.Int).Exp(gx, big.NewInt(3), nil)).Sub(b, new(big.Int).Mul(a, gx)).Mod(b, pc)
		unc := append(append([]byte{4}, fe(gx)...), fe(gy)...)
		order := new(big.Int).Add(pc, big.NewInt(int64(1+r.IntN(1000))))
		if trial%12 >= 6 {
			order = big.NewInt(int64(1000003 + 2*r.IntN(1000))) // wNAF, as above
		}
		params := func(base, cof []byte) []byte {
			return derSeq(derSmall(1), derSeq(derOID("2a8648ce3d0101"), derInt(pc)),
				derSeq(derTLV(4, fe(a)), derTLV(4, fe(b))), derTLV(4, base), derInt(order), cof)
		}
		spki := func(prm, pt []byte) []byte {
			return derSeq(derSeq(oidEC, prm), derTLV(3, append([]byte{0}, pt...)))
		}
		for yb := byte(0); yb < 2; yb++ {
			add("PUBLIC KEY", spki(params(unc, nil), append([]byte{2 | yb}, fe(gx)...)))
			add("PUBLIC KEY", spki(params(unc, nil), append([]byte{6 | yb}, unc[1:]...)))
		}
		for _, cof := range [][]byte{nil, derSmall(1), derSmall(0)} {
			priv := new(big.Int).Add(big.NewInt(2), randBits(r, 40))
			add("EC PRIVATE KEY", derSeq(derSmall(1), derTLV(4, fe(priv)), derTLV(0xa0, params(unc, cof))))
		}
	}
	return cases
}

// loadReducibleFixtures reads testdata/pemreducible.json: the round-9
// review's reproductions and the best case of each of its generators'
// (r9-1 g/gen*.py) result classes, each with a Node result that is the
// same on every run. Stored as the hex of the text's bytes in reverse
// order, as pemcorpus.json is.
func loadReducibleFixtures(t *testing.T) []pemCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "pemreducible.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			RevHex string `json:"revHex"`
			Pub    bool   `json:"pub"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	out := make([]pemCase, 0, len(corpus.Cases))
	for _, c := range corpus.Cases {
		b, err := hex.DecodeString(c.RevHex)
		if err != nil {
			t.Fatal(err)
		}
		for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
			b[i], b[j] = b[j], b[i]
		}
		out = append(out, pemCase{Text: string(b), Pub: c.Pub})
	}
	return out
}
