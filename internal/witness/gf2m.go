package witness

import (
	"math/big"
	"math/bits"
	"math/rand/v2"
)

// Binary polynomial arithmetic as crypto/bn/bn_gf2m.c does it: elements of
// GF(2)[x] reduced modulo the curve's trinomial or pentanomial. OpenSSL
// never checks that the polynomial is irreducible, so explicit parameters
// can make this a ring with zero divisors; every function here follows
// bn_gf2m.c's algorithm, not field identities, so it fails and succeeds
// exactly where OpenSSL's does.
//
// Two of OpenSSL's functions draw random numbers:
//   - BN_GF2m_mod_inv blinds its input with a random nonzero b (inverting
//     a·b, then multiplying by b). Over a field every such b is a unit and
//     the result does not depend on it; modulo a reducible polynomial a b
//     sharing a factor with it makes the inversion fail. gf2Inv takes
//     b = 1, the outcome for every unit b (KNOWN_DIFFERENCES).
//   - BN_GF2m_mod_solve_quad_arr for even m tries random rho until its
//     trace-like sum w is nonzero. Over a field every such rho gives a
//     solution with the same parity behaviour; modulo a reducible
//     polynomial the outcome can depend on rho. gf2SolveQuad draws rho from
//     a fixed-seed generator (KNOWN_DIFFERENCES).

// gf2Field is a binary polynomial modulus: p and its nonzero exponents,
// highest first (OpenSSL's group->poly; poly[0] = m).
type gf2Field struct {
	p    *big.Int
	poly []int
}

func newGF2Field(p *big.Int) *gf2Field { return &gf2Field{p: p, poly: polyExponents(p)} }

func polyExponents(p *big.Int) []int {
	var out []int
	for i := p.BitLen() - 1; i >= 0; i-- {
		if p.Bit(i) == 1 {
			out = append(out, i)
		}
	}
	return out
}

func gf2Add(a, b *big.Int) *big.Int { return new(big.Int).Xor(a, b) }

// gf2Words is a's bits as 64-bit words, least significant first, with
// room for n words at least.
func gf2Words(a *big.Int, n int) []uint64 {
	nw := (a.BitLen() + 63) / 64
	if nw > n {
		n = nw
	}
	w := make([]uint64, n)
	if bits.UintSize == 64 {
		for i, d := range a.Bits() {
			w[i] = uint64(d)
		}
		return w
	}
	for i, d := range a.Bits() {
		w[i/2] |= uint64(d) << (32 * (i % 2))
	}
	return w
}

func gf2FromWords(w []uint64) *big.Int {
	b := make([]byte, 8*len(w))
	for i, d := range w {
		for j := 0; j < 8; j++ {
			b[len(b)-1-8*i-j] = byte(d >> (8 * j))
		}
	}
	return new(big.Int).SetBytes(b)
}

// reduce ports BN_GF2m_mod_arr on a word array: each set bit at or above
// m is cleared by adding x^(i-m)·p.
func (f *gf2Field) reduce(w []uint64) *big.Int {
	m := f.poly[0]
	if m == 0 {
		return new(big.Int) // reduction mod 1
	}
	for i := 64*len(w) - 1; i >= m; i-- {
		if w[i/64]>>(i%64)&1 == 0 {
			continue
		}
		d := i - m
		for _, e := range f.poly {
			j := e + d
			w[j/64] ^= 1 << (j % 64)
		}
	}
	return gf2FromWords(w)
}

// mod ports BN_GF2m_mod_arr.
func (f *gf2Field) mod(a *big.Int) *big.Int { return f.reduce(gf2Words(a, 0)) }

// mul ports BN_GF2m_mod_mul_arr (and BN_GF2m_mod_sqr_arr for a = b).
func (f *gf2Field) mul(a, b *big.Int) *big.Int {
	aw, bw := gf2Words(a, 0), gf2Words(b, 0)
	r := make([]uint64, len(aw)+len(bw)+1)
	for j, y := range bw {
		for k := 0; y != 0; k++ {
			if y&1 == 1 {
				for i, x := range aw {
					r[i+j] ^= x << k
					if k != 0 {
						r[i+j+1] ^= x >> (64 - k)
					}
				}
			}
			y >>= 1
		}
	}
	return f.reduce(r)
}

func (f *gf2Field) sqr(a *big.Int) *big.Int { return f.mul(a, a) }

// gf2Inv ports BN_GF2m_mod_inv with the blinding value b = 1, so
// BN_GF2m_mod_inv_vartime (the Modified Almost Inverse Algorithm, the
// word-level branch) on a mod p: false, with no error raised, when a has
// no inverse ("poly was reducible", or a ≡ 0).
func gf2Inv(a *big.Int, f *gf2Field) (*big.Int, bool) {
	if f.p.BitLen() <= 1 {
		return nil, false
	}
	u := f.mod(a)
	if u.Sign() == 0 {
		return nil, false
	}
	v := new(big.Int).Set(f.p)
	b, c := big.NewInt(1), new(big.Int)
	ubits, vbits := u.BitLen(), v.BitLen()
	one := big.NewInt(1)
	for {
		for ubits != 0 && u.Bit(0) == 0 {
			u.Rsh(u, 1)
			if b.Bit(0) == 1 {
				b.Xor(b, f.p)
			}
			b.Rsh(b, 1)
			ubits--
		}
		if ubits <= 64 {
			if u.Sign() == 0 {
				return nil, false
			}
			if u.Cmp(one) == 0 {
				break
			}
		}
		if ubits < vbits {
			ubits, vbits = vbits, ubits
			u, v = v, u
			b, c = c, b
		}
		u.Xor(u, v)
		b.Xor(b, c)
		if ubits == vbits {
			ubits = u.BitLen()
		}
	}
	return b, true
}

// gf2Div ports BN_GF2m_mod_div: y·x⁻¹, false (nothing raised) when the
// inversion fails.
func gf2Div(y, x *big.Int, f *gf2Field) (*big.Int, bool) {
	xinv, ok := gf2Inv(x, f)
	if !ok {
		return nil, false
	}
	return f.mul(y, xinv), true
}

// gf2Sqrt ports BN_GF2m_mod_sqrt_arr: a^(2^(m-1)) by BN_GF2m_mod_exp_arr's
// square-and-multiply (m-1 squarings of a mod p), the square root only
// when p is irreducible.
func gf2Sqrt(a *big.Int, f *gf2Field) *big.Int {
	m := f.poly[0]
	if m == 0 {
		return new(big.Int)
	}
	u := f.mod(a)
	for i := m - 2; i >= 0; i-- {
		u = f.sqr(u)
	}
	return u
}

// gf2SolveQuad ports BN_GF2m_mod_solve_quad_arr: z with z² + z = a, or the
// BN reason it fails with (BN_R_NO_SOLUTION, BN_R_TOO_MANY_ITERATIONS).
func gf2SolveQuad(a *big.Int, f *gf2Field) (*big.Int, string) {
	m := f.poly[0]
	if m == 0 {
		return new(big.Int), ""
	}
	a = f.mod(a)
	if a.Sign() == 0 {
		return new(big.Int), ""
	}
	var z *big.Int
	if m&1 == 1 {
		// half-trace of a
		z = new(big.Int).Set(a)
		for j := 1; j <= (m-1)/2; j++ {
			z = f.sqr(f.sqr(z))
			z = gf2Add(z, a)
		}
	} else {
		rng := gf2QuadRand()
		w := new(big.Int)
		for count := 0; ; {
			// BN_priv_rand_ex(rho, m, BN_RAND_TOP_ONE, BN_RAND_BOTTOM_ANY)
			rho := new(big.Int)
			for i := 0; i < m-1; i++ {
				if rng.Uint64()&1 == 1 {
					rho.SetBit(rho, i, 1)
				}
			}
			rho.SetBit(rho, m-1, 1)
			rho = f.mod(rho)
			z = new(big.Int)
			w.Set(rho)
			for j := 1; j <= m-1; j++ {
				z = f.sqr(z)
				w2 := f.sqr(w)
				z = gf2Add(z, f.mul(w2, a))
				w = gf2Add(w2, rho)
			}
			count++
			if w.Sign() != 0 || count >= 50 { // MAX_ITERATIONS
				break
			}
		}
		if w.Sign() == 0 {
			return nil, "BN_R_TOO_MANY_ITERATIONS"
		}
	}
	if gf2Add(z, f.sqr(z)).Cmp(a) != 0 {
		return nil, "BN_R_NO_SOLUTION"
	}
	return z, ""
}

// gf2QuadRand stands in for BN_priv_rand_ex in gf2SolveQuad: a fixed
// sequence, so that a reducible even-degree polynomial, where OpenSSL's
// outcome is random, gets one reproducible outcome.
func gf2QuadRand() *rand.ChaCha8 {
	return rand.NewChaCha8([32]byte{'f', 'a', 'u', 'l', 't', 'l', 'i', 'n', 'e', ' ', 's', 'o', 'l', 'v', 'e', '_', 'q', 'u', 'a', 'd'})
}
