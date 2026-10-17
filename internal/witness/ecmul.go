package witness

import "math/big"

// priv·G as d2i_ECPrivateKey computes the missing public key
// (ossl_ec_key_simple_generate_public_key → EC_POINT_mul), for the generic
// methods an unnamed explicit group gets: EC_GFp_mont_method (prime field)
// and EC_GF2m_simple_method (binary field). On a field nothing here fails.
// With a composite "prime" or a reducible polynomial, inversions can fail,
// and EC_POINT_mul fails where OpenSSL's does:
//
//   - ossl_ec_scalar_mul_ladder (order and cofactor nonzero): the final
//     inversion in ladder_post (EC_R_CANNOT_INVERT, EC_R_LADDER_POST_FAILURE).
//   - ossl_ec_wNAF_mul (cofactor 0): GF(p): points_make_affine's inversion
//     (EC_R_CANNOT_INVERT, ERR_R_BN_LIB); GF(2^m): the affine additions'
//     divisions (nothing raised) and their on-curve checks.
//
// ladder_pre blinds the ladder's projective coordinates with two random
// nonzero field elements, and BN_GF2m_mod_inv blinds every inversion
// (gf2Inv). On a field the outcome does not depend on them; with a value
// that shares a factor with p it can, and Go draws them as OpenSSL does
// (bnrand.go), so its outcome is as random as OpenSSL's (KNOWN_DIFFERENCES).

// ecPoint is an EC_POINT: projective (X, Y, Z) for the GF(p) Jacobian and
// ladder code, affine (Z = 1) for GF(2^m) outside the ladder; Z = 0 is the
// point at infinity.
type ecPoint struct {
	x, y, z *big.Int
	zIsOne  bool
}

func (p *ecPoint) copy() *ecPoint {
	return &ecPoint{new(big.Int).Set(p.x), new(big.Int).Set(p.y), new(big.Int).Set(p.z), p.zIsOne}
}

func infinityPoint() *ecPoint { return &ecPoint{new(big.Int), new(big.Int), new(big.Int), false} }

func (p *ecPoint) atInfinity() bool { return p.z.Sign() == 0 }

// ecKeygenPub ports ossl_ec_key_simple_generate_public_key: priv·G, false
// (with OpenSSL's errors raised) where EC_POINT_mul fails.
//
// A decoded group always has a generator: one at infinity fails
// d2i_ECPKParameters (groupFromECParameters), so EC_R_UNDEFINED_GENERATOR
// cannot come up. A named group (including explicit parameters that
// matched one) is over a field, where the multiplication cannot fail.
func (e *osslEnv) ecKeygenPub(g *ecGroup, priv *big.Int) bool {
	if g.nid != 0 {
		return true
	}
	gen := &ecPoint{new(big.Int).Set(g.gx), new(big.Int).Set(g.gy), big.NewInt(1), true}
	// The group order is never 0 here (EC_GROUP_set_generator refuses it).
	if g.cofactor.Sign() == 0 {
		return e.wnafMul(g, priv, gen)
	}
	return e.ladderMul(g, priv, gen)
}

// --- ossl_ec_scalar_mul_ladder ---------------------------------------------

// ladderMul ports ossl_ec_scalar_mul_ladder(group, r, scalar, NULL).
func (e *osslEnv) ladderMul(g *ecGroup, scalar *big.Int, p *ecPoint) bool {
	cardinality := new(big.Int).Mul(g.order, g.cofactor)
	cardBits := cardinality.BitLen()
	k := new(big.Int).Set(scalar)
	if k.BitLen() > cardBits {
		k.Mod(k, cardinality)
	}
	lambda := new(big.Int).Add(k, cardinality)
	k.Add(lambda, cardinality)
	if lambda.Bit(cardBits) == 1 {
		k, lambda = lambda, k
	}
	r, s := &ecPoint{}, &ecPoint{}
	if g.binary {
		gf2LadderPre(g, r, s, p)
	} else {
		gfpLadderPre(g, r, s, p)
	}
	pbit := uint(1)
	for i := cardBits - 1; i >= 0; i-- {
		kbit := k.Bit(i) ^ pbit
		if kbit == 1 {
			*r, *s = *s, *r
		}
		if g.binary {
			gf2LadderStep(g, r, s, p)
		} else {
			gfpLadderStep(g, r, s, p)
		}
		pbit ^= kbit
	}
	if pbit == 1 {
		*r, *s = *s, *r
	}
	var ok bool
	if g.binary {
		ok = e.gf2LadderPost(g, r, s, p)
	} else {
		ok = e.gfpLadderPost(g, r, s, p)
	}
	if !ok {
		e.q.raise(libEC, "EC_R_LADDER_POST_FAILURE")
	}
	return ok
}

// gfpLadderPre ports ossl_ec_GFp_simple_ladder_pre: s := p, r := 2p, both
// blinded by random nonzero values below p (BN_priv_rand_range_ex). The
// Montgomery encoding of a blinding value is a bijection on [1, p), so the
// standard-representation values here are distributed as OpenSSL's.
func gfpLadderPre(g *ecGroup, r, s, p *ecPoint) {
	m := g.modP()
	t3 := m.mul(p.x, p.x)
	t4 := m.sub(t3, g.a)
	t4 = m.mul(t4, t4)
	t5 := m.mul(p.x, g.b)
	t5 = m.lsh(t5, 3)
	r.x = m.sub(t4, t5)
	t1 := m.add(t3, g.a)
	t2 := m.mul(p.x, t1)
	t2 = m.add(g.b, t2)
	r.z = m.lsh(t2, 2)
	r.y = bnPrivRandRangeNonzero(g.p) // lambda_r
	s.z = bnPrivRandRangeNonzero(g.p) // lambda_s
	r.z = m.mul(r.z, r.y)
	r.x = m.mul(r.x, r.y)
	s.x = m.mul(p.x, s.z)
	s.y = new(big.Int)
	r.zIsOne, s.zIsOne = false, false
}

// gfpLadderStep ports ossl_ec_GFp_simple_ladder_step: s := r + s, r := 2r.
func gfpLadderStep(g *ecGroup, r, s, p *ecPoint) {
	m := g.modP()
	t6 := m.mul(r.x, s.x)
	t0 := m.mul(r.z, s.z)
	t4 := m.mul(r.x, s.z)
	t3 := m.mul(r.z, s.x)
	t5 := m.mul(g.a, t0)
	t5 = m.add(t6, t5)
	t6 = m.add(t3, t4)
	t5 = m.mul(t6, t5)
	t0 = m.mul(t0, t0)
	t2 := m.lsh(g.b, 2)
	t0 = m.mul(t2, t0)
	t5 = m.lsh(t5, 1)
	t3 = m.sub(t4, t3)
	s.z = m.mul(t3, t3)
	t4 = m.mul(s.z, p.x)
	t0 = m.add(t0, t5)
	s.x = m.sub(t0, t4)
	t4 = m.mul(r.x, r.x)
	t5 = m.mul(r.z, r.z)
	t6 = m.mul(t5, g.a)
	t1 := m.add(r.x, r.z)
	t1 = m.mul(t1, t1)
	t1 = m.sub(t1, t4)
	t1 = m.sub(t1, t5)
	t3 = m.sub(t4, t6)
	t3 = m.mul(t3, t3)
	t0 = m.mul(t5, t1)
	t0 = m.mul(t2, t0)
	r.x = m.sub(t3, t0)
	t3 = m.add(t4, t6)
	t4 = m.mul(t5, t5)
	t4 = m.mul(t4, t2)
	t1 = m.mul(t1, t3)
	t1 = m.lsh(t1, 1)
	r.z = m.add(t4, t1)
}

// gfpLadderPost ports ossl_ec_GFp_simple_ladder_post up to its one
// failure: ossl_ec_GFp_mont_field_inv (Fermat: t1^(p-2), EC_R_CANNOT_INVERT
// when that is 0).
func (e *osslEnv) gfpLadderPost(g *ecGroup, r, s, p *ecPoint) bool {
	if r.z.Sign() == 0 || s.z.Sign() == 0 {
		return true // infinity, or -p
	}
	m := g.modP()
	t4 := m.lsh(p.y, 1)
	t1 := m.mul(s.z, t4)
	t3 := m.mul(r.z, r.z)
	t1 = m.mul(t3, t1)
	if !e.gfpMontInv(g, t1) {
		return false
	}
	return true
}

// gfpMontInv ports ossl_ec_GFp_mont_field_inv's failure: a^(p-2) mod p = 0.
func (e *osslEnv) gfpMontInv(g *ecGroup, a *big.Int) bool {
	ex := new(big.Int).Sub(g.p, big.NewInt(2))
	if new(big.Int).Exp(a, ex, g.p).Sign() == 0 {
		e.q.raise(libEC, "EC_R_CANNOT_INVERT")
		return false
	}
	return true
}

// gf2LadderPre ports ec_GF2m_simple_ladder_pre (Lopez-Dahab): s := p,
// r := 2p, blinded by random nonzero values of BN_num_bits(p)-1 bits.
func gf2LadderPre(g *ecGroup, r, s, p *ecPoint) {
	f := g.gf
	s.z = bnPrivRandNonzero(g.p.BitLen() - 1) // lambda_s
	s.x = f.mul(p.x, s.z)
	r.y = bnPrivRandNonzero(g.p.BitLen() - 1) // lambda_r
	r.z = f.sqr(p.x)
	r.x = f.sqr(r.z)
	r.x = gf2Add(r.x, g.b)
	r.z = f.mul(r.z, r.y)
	r.x = f.mul(r.x, r.y)
	s.y = new(big.Int)
	r.zIsOne, s.zIsOne = false, false
}

// gf2LadderStep ports ec_GF2m_simple_ladder_step: s := r + s, r := 2r.
func gf2LadderStep(g *ecGroup, r, s, p *ecPoint) {
	f := g.gf
	r.y = f.mul(r.z, s.x)
	s.x = f.mul(r.x, s.z)
	s.y = f.sqr(r.z)
	r.z = f.sqr(r.x)
	s.z = gf2Add(r.y, s.x)
	s.z = f.sqr(s.z)
	s.x = f.mul(r.y, s.x)
	r.y = f.mul(s.z, p.x)
	s.x = gf2Add(s.x, r.y)
	r.y = f.sqr(r.z)
	r.z = f.mul(r.z, s.y)
	s.y = f.sqr(s.y)
	s.y = f.mul(s.y, g.b)
	r.x = gf2Add(r.y, s.y)
}

// gf2LadderPost ports ec_GF2m_simple_ladder_post up to its one failure:
// ec_GF2m_simple_field_inv (EC_R_CANNOT_INVERT).
func (e *osslEnv) gf2LadderPost(g *ecGroup, r, s, p *ecPoint) bool {
	if r.z.Sign() == 0 || s.z.Sign() == 0 {
		return true // infinity, or -p
	}
	f := g.gf
	t0 := f.mul(r.z, s.z)
	t2 := f.mul(p.x, t0)
	if _, ok := gf2Inv(t2, f); !ok {
		e.q.raise(libEC, "EC_R_CANNOT_INVERT")
		return false
	}
	return true
}

// --- ossl_ec_wNAF_mul --------------------------------------------------------

// windowBits is EC_window_bits_for_scalar_size.
func windowBits(b int) int {
	switch {
	case b >= 2000:
		return 6
	case b >= 800:
		return 5
	case b >= 300:
		return 4
	case b >= 70:
		return 3
	case b >= 20:
		return 2
	}
	return 1
}

// computeWNAF ports bn_compute_wNAF for a nonnegative scalar.
func computeWNAF(scalar *big.Int, w int) []int {
	if scalar.Sign() == 0 {
		return []int{0}
	}
	bit := 1 << w
	next := bit << 1
	mask := next - 1
	n := scalar.BitLen()
	var low int
	if words := scalar.Bits(); len(words) > 0 {
		low = int(uint(words[0]) & uint(mask))
	}
	window := low
	var r []int
	j := 0
	for window != 0 || j+w+1 < n {
		digit := 0
		if window&1 == 1 {
			if window&bit != 0 {
				digit = window - next
				if j+w+1 >= n {
					digit = window & (mask >> 1)
				}
			} else {
				digit = window
			}
			window -= digit
		}
		r = append(r, digit)
		j++
		window >>= 1
		window += bit * int(scalar.Bit(j+w))
	}
	return r
}

// wnafMul ports ossl_ec_wNAF_mul(group, r, scalar, 0, ...) with no
// precomputation (a decoded group has none).
func (e *osslEnv) wnafMul(g *ecGroup, scalar *big.Int, gen *ecPoint) bool {
	w := windowBits(scalar.BitLen())
	wnaf := computeWNAF(scalar, w)
	n := 1 << (w - 1)
	val := make([]*ecPoint, n)
	val[0] = gen.copy()
	if w > 1 {
		tmp, ok := e.pointDbl(g, val[0])
		if !ok {
			return false
		}
		for j := 1; j < n; j++ {
			if val[j], ok = e.pointAdd(g, val[j-1], tmp); !ok {
				return false
			}
		}
	}
	if !g.binary {
		// ossl_ec_GFp_simple_points_make_affine: one inversion of the
		// product of the nonzero Z. Nothing after it can fail.
		m := g.modP()
		prod := big.NewInt(1)
		for _, v := range val {
			if v.z.Sign() != 0 {
				prod = m.mul(prod, v.z)
			}
		}
		if !e.gfpMontInv(g, prod) {
			e.q.raise(libEC, "ERR_R_BN_LIB")
			return false
		}
		return true
	}
	// GF(2^m): every point is affine already (points_make_affine does
	// nothing); the main loop's additions can still fail.
	r := infinityPoint()
	rInf, rInv := true, false
	var ok bool
	for k := len(wnaf) - 1; k >= 0; k-- {
		if !rInf {
			if r, ok = e.pointDbl(g, r); !ok {
				return false
			}
		}
		digit := wnaf[k]
		if digit == 0 {
			continue
		}
		neg := digit < 0
		if neg {
			digit = -digit
		}
		if neg != rInv {
			if !rInf {
				r = gf2Invert(r)
			}
			rInv = !rInv
		}
		if rInf {
			r = val[digit>>1].copy()
			rInf = false
		} else if r, ok = e.pointAdd(g, r, val[digit>>1]); !ok {
			return false
		}
	}
	return true
}

func gf2Invert(p *ecPoint) *ecPoint {
	if p.atInfinity() || p.y.Sign() == 0 {
		return p
	}
	q := p.copy()
	q.y = gf2Add(q.x, q.y)
	return q
}

// pointDbl is EC_POINT_dbl.
func (e *osslEnv) pointDbl(g *ecGroup, a *ecPoint) (*ecPoint, bool) {
	if g.binary {
		return e.gf2PointAdd(g, a, a)
	}
	return gfpDbl(g, a), true
}

// pointAdd is EC_POINT_add (a and b distinct objects).
func (e *osslEnv) pointAdd(g *ecGroup, a, b *ecPoint) (*ecPoint, bool) {
	if g.binary {
		return e.gf2PointAdd(g, a, b)
	}
	return gfpAdd(g, a, b), true
}

// gf2PointAdd ports ossl_ec_GF2m_simple_add on affine points: false where a
// division fails (nothing raised) or the sum is not on the curve
// (EC_POINT_set_affine_coordinates).
func (e *osslEnv) gf2PointAdd(g *ecGroup, a, b *ecPoint) (*ecPoint, bool) {
	if a.atInfinity() {
		return b.copy(), true
	}
	if b.atInfinity() {
		return a.copy(), true
	}
	f := g.gf
	x0, y0, x1, y1 := a.x, a.y, b.x, b.y
	var s, x2 *big.Int
	var ok bool
	if x0.Cmp(x1) != 0 {
		t := gf2Add(x0, x1)
		if s, ok = gf2Div(gf2Add(y0, y1), t, f); !ok {
			return nil, false
		}
		x2 = gf2Add(gf2Add(gf2Add(f.sqr(s), g.a), s), t)
	} else {
		if y0.Cmp(y1) != 0 || x1.Sign() == 0 {
			return infinityPoint(), true
		}
		if s, ok = gf2Div(y1, x1, f); !ok {
			return nil, false
		}
		s = gf2Add(s, x1)
		x2 = gf2Add(gf2Add(f.sqr(s), s), g.a)
	}
	y2 := f.mul(gf2Add(x1, x2), s)
	y2 = gf2Add(gf2Add(y2, x2), y1)
	if !g.onCurve(x2, y2) {
		e.q.raise(libEC, "EC_R_POINT_IS_NOT_ON_CURVE")
		return nil, false
	}
	return &ecPoint{x2, y2, big.NewInt(1), true}, true
}

// gfpDbl ports ossl_ec_GFp_simple_dbl (Jacobian coordinates).
func gfpDbl(g *ecGroup, a *ecPoint) *ecPoint {
	if a.atInfinity() {
		return infinityPoint()
	}
	m := g.modP()
	var n0, n1 *big.Int
	if a.zIsOne {
		n0 = m.mul(a.x, a.x)
		n1 = m.lsh(n0, 1)
		n0 = m.add(n0, n1)
		n1 = m.add(n0, g.a)
	} else {
		n0 = m.mul(a.x, a.x)
		n1 = m.lsh(n0, 1)
		n0 = m.add(n0, n1)
		n1 = m.mul(a.z, a.z)
		n1 = m.mul(n1, n1)
		n1 = m.mul(n1, g.a)
		n1 = m.add(n1, n0)
	}
	if a.zIsOne {
		n0 = new(big.Int).Set(a.y)
	} else {
		n0 = m.mul(a.y, a.z)
	}
	r := &ecPoint{}
	r.z = m.lsh(n0, 1)
	n3 := m.mul(a.y, a.y)
	n2 := m.mul(a.x, n3)
	n2 = m.lsh(n2, 2)
	n0 = m.lsh(n2, 1)
	r.x = m.mul(n1, n1)
	r.x = m.sub(r.x, n0)
	n0 = m.mul(n3, n3)
	n3 = m.lsh(n0, 3)
	n0 = m.sub(n2, r.x)
	n0 = m.mul(n1, n0)
	r.y = m.sub(n0, n3)
	return r
}

// gfpAdd ports ossl_ec_GFp_simple_add (Jacobian coordinates).
func gfpAdd(g *ecGroup, a, b *ecPoint) *ecPoint {
	if a.atInfinity() {
		return b.copy()
	}
	if b.atInfinity() {
		return a.copy()
	}
	m := g.modP()
	var n0, n1, n2, n3, n4 *big.Int
	if b.zIsOne {
		n1, n2 = a.x, a.y
	} else {
		n0 = m.mul(b.z, b.z)
		n1 = m.mul(a.x, n0)
		n0 = m.mul(n0, b.z)
		n2 = m.mul(a.y, n0)
	}
	if a.zIsOne {
		n3, n4 = b.x, b.y
	} else {
		n0 = m.mul(a.z, a.z)
		n3 = m.mul(b.x, n0)
		n0 = m.mul(n0, a.z)
		n4 = m.mul(b.y, n0)
	}
	n5 := m.sub(n1, n3)
	n6 := m.sub(n2, n4)
	if n5.Sign() == 0 {
		if n6.Sign() == 0 {
			return gfpDbl(g, a)
		}
		return infinityPoint()
	}
	n1 = m.add(n1, n3)
	n2 = m.add(n2, n4)
	r := &ecPoint{}
	switch {
	case a.zIsOne && b.zIsOne:
		r.z = new(big.Int).Set(n5)
	default:
		switch {
		case a.zIsOne:
			n0 = b.z
		case b.zIsOne:
			n0 = a.z
		default:
			n0 = m.mul(a.z, b.z)
		}
		r.z = m.mul(n0, n5)
	}
	n0 = m.mul(n6, n6)
	n4 = m.mul(n5, n5)
	n3 = m.mul(n1, n4)
	r.x = m.sub(n0, n3)
	n0 = m.lsh(r.x, 1)
	n0 = m.sub(n3, n0)
	n0 = m.mul(n0, n6)
	n5 = m.mul(n4, n5)
	n1 = m.mul(n2, n5)
	n0 = m.sub(n0, n1)
	if n0.Bit(0) == 1 {
		n0.Add(n0, g.p)
	}
	r.y = n0.Rsh(n0, 1)
	return r
}

// modArith is arithmetic modulo the prime-field p (standard
// representation: the Montgomery form's values decoded, which leaves every
// zero test unchanged).
type modArith struct{ p *big.Int }

func (g *ecGroup) modP() modArith { return modArith{g.p} }

func (m modArith) mul(a, b *big.Int) *big.Int {
	r := new(big.Int).Mul(a, b)
	return r.Mod(r, m.p)
}

func (m modArith) add(a, b *big.Int) *big.Int {
	r := new(big.Int).Add(a, b)
	return r.Mod(r, m.p)
}

func (m modArith) sub(a, b *big.Int) *big.Int {
	r := new(big.Int).Sub(a, b)
	return r.Mod(r, m.p)
}

func (m modArith) lsh(a *big.Int, n uint) *big.Int {
	r := new(big.Int).Lsh(a, n)
	return r.Mod(r, m.p)
}
