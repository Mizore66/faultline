package witness

import (
	"bytes"
	"encoding/hex"
	"math/big"
)

// Elliptic-curve groups and points as OpenSSL 3.5 decodes them
// (crypto/ec/ec_asn1.c, ec_curve.c, ec_lib.c, ecp_oct.c, ec2_oct.c,
// ecp_smpl.c, ec2_smpl.c, crypto/bn/bn_sqrt.c, bn_gf2m.c), with their
// errors. Only what decoding a key needs: group construction from a named
// curve or explicit parameters, point decoding with the on-curve check,
// private key scalars and the derived public key's existence.

type namedCurve struct {
	nid                                    int
	name, oid                              string
	binary                                 bool
	p, a, b, gx, gy, order, cofactor, seed string
}

type curveListEntry struct{ nid, seedLen, paramLen, cofactor int }

// ecGroup is an EC_GROUP: prime field (p) or binary field (poly p, with
// its exponents), curve a, b, generator, order, cofactor.
type ecGroup struct {
	nid      int // NID_undef (0) for an unnamed explicit group
	binary   bool
	p, a, b  *big.Int
	gf       *gf2Field // binary: p as a reduction polynomial
	gx, gy   *big.Int
	order    *big.Int
	cofactor *big.Int
	seed     []byte
	explicit bool
}

func (g *ecGroup) isSM2() bool { return g != nil && g.nid == 1172 }

func hexInt(s string) *big.Int {
	n, _ := new(big.Int).SetString(s, 16)
	return n
}

// groupByNID ports EC_GROUP_new_by_curve_name: false (with
// EC_R_UNKNOWN_GROUP raised) for a NID without curve data.
func (e *osslEnv) groupByNID(nid int) *ecGroup {
	for _, c := range namedCurves {
		if c.nid != nid {
			continue
		}
		g := &ecGroup{nid: nid, binary: c.binary, p: hexInt(c.p), a: hexInt(c.a), b: hexInt(c.b),
			gx: hexInt(c.gx), gy: hexInt(c.gy), order: hexInt(c.order), cofactor: hexInt(c.cofactor)}
		if c.seed != "" {
			g.seed, _ = hex.DecodeString(c.seed)
		}
		if g.binary {
			g.gf = newGF2Field(g.p)
		}
		return g
	}
	e.q.raise(libEC, "EC_R_UNKNOWN_GROUP")
	return nil
}

// nidForCurveOID is OBJ_obj2nid for the named curves' OIDs (0 otherwise).
func nidForCurveOID(oid []byte) int {
	h := hex.EncodeToString(oid)
	for _, c := range namedCurves {
		if c.oid != "" && c.oid == h {
			return c.nid
		}
	}
	return 0
}

// ecGroupFromAlgor ports ec_backend.c ossl_ec_key_param_from_x509_algor.
func (e *osslEnv) ecGroupFromAlgor(alg *asn1Value) *ecGroup {
	param := alg.fields[1]
	switch {
	case param != nil && param.utype == tagSequence:
		g, ok := e.d2iECParameters(param.data)
		if !ok {
			e.q.raise(libEC, "EC_R_DECODE_ERROR")
			return nil
		}
		return g
	case param != nil && param.utype == tagObject:
		return e.groupByNID(nidForCurveOID(param.data))
	}
	e.q.raise(libEC, "EC_R_DECODE_ERROR")
	return nil
}

// d2iECParameters ports d2i_ECParameters (via d2i_ECPKParameters, with
// the EC_KEY wrapper raising nothing more).
func (e *osslEnv) d2iECParameters(der []byte) (*ecGroup, bool) {
	g := e.d2iECPKParameters(der)
	return g, g != nil
}

// d2iECPKParameters ports d2i_ECPKParameters.
func (e *osslEnv) d2iECPKParameters(der []byte) *ecGroup {
	v, _, ok := e.d2i(ecpkParametersItem, der)
	if !ok {
		return nil
	}
	return e.groupFromECPKParameters(v)
}

// groupFromECPKParameters ports EC_GROUP_new_from_ecpkparameters.
func (e *osslEnv) groupFromECPKParameters(v *asn1Value) *ecGroup {
	switch v.choice {
	case 0:
		g := e.groupByNID(nidForCurveOID(v.fields[0].data))
		if g == nil {
			e.q.raise(libEC, "EC_R_EC_GROUP_NEW_BY_NAME_FAILURE")
		}
		return g
	case 1:
		g := e.groupFromECParameters(v.fields[0])
		if g == nil {
			e.q.raise(libEC, "ERR_R_EC_LIB")
			return nil
		}
		g.explicit = true
		return g
	}
	return nil // implicitlyCA: unsupported, no error
}

// groupFromECParameters ports EC_GROUP_new_from_ecparameters.
func (e *osslEnv) groupFromECParameters(v *asn1Value) *ecGroup {
	q := e.q
	fieldID := v.fields[1]
	curve := v.fields[2]
	a := new(big.Int).SetBytes(curve.fields[0].data)
	b := new(big.Int).SetBytes(curve.fields[1].data)
	fieldType := fieldID.fields[0].data
	g := &ecGroup{a: a, b: b}
	var fieldBits int
	switch string(fieldType) {
	case string(oidCharTwo):
		ct := fieldID.fields[1]
		m := ct.fields[0].int32()
		fieldBits = int(m)
		if m > 661 { // OPENSSL_ECC_MAX_FIELD_BITS
			q.raise(libEC, "EC_R_FIELD_TOO_LARGE")
			return nil
		}
		p := new(big.Int)
		switch string(ct.fields[1].data) {
		case string(oidTpBasis):
			k := (&asn1Dec{q: q, env: e}).integerGet(ct.fields[2])
			if !(m > k && k > 0) {
				q.raise(libEC, "EC_R_INVALID_TRINOMIAL_BASIS")
				return nil
			}
			p.SetBit(p, int(m), 1).SetBit(p, int(k), 1).SetBit(p, 0, 1)
		case string(oidPpBasis):
			pp := ct.fields[2]
			k1, k2, k3 := pp.fields[0].int32(), pp.fields[1].int32(), pp.fields[2].int32()
			if !(m > k3 && k3 > k2 && k2 > k1 && k1 > 0) {
				q.raise(libEC, "EC_R_INVALID_PENTANOMIAL_BASIS")
				return nil
			}
			p.SetBit(p, int(m), 1).SetBit(p, int(k1), 1).SetBit(p, int(k2), 1).SetBit(p, int(k3), 1).SetBit(p, 0, 1)
		case string(oidOnBasis):
			q.raise(libEC, "EC_R_NOT_IMPLEMENTED")
			return nil
		default:
			q.raise(libEC, "EC_R_ASN1_ERROR")
			return nil
		}
		// EC_GROUP_new_curve_GF2m → ossl_ec_GF2m_simple_group_set_curve.
		g.binary = true
		g.p = p
		// ossl_ec_GF2m_simple_group_set_curve's EC_R_UNSUPPORTED_FIELD needs
		// a polynomial of other than 3 or 5 terms; the basis checks above
		// leave exactly 3 (trinomial) or 5 (pentanomial) distinct exponents.
		g.gf = newGF2Field(p)
		g.a = g.gf.mod(a)
		g.b = g.gf.mod(b)
	case string(oidPrimeField):
		p := integerValue(fieldID.fields[1])
		if p.Sign() <= 0 {
			q.raise(libEC, "EC_R_INVALID_FIELD")
			return nil
		}
		fieldBits = p.BitLen()
		if fieldBits > 661 {
			q.raise(libEC, "EC_R_FIELD_TOO_LARGE")
			return nil
		}
		// EC_GROUP_new_curve_GFp → the Montgomery method's set_curve.
		if p.Bit(0) == 0 {
			// BN_MONT_CTX_set needs an odd modulus (BN_mod_inverse of R
			// modulo the low word fails).
			q.raise(libBN, "BN_R_NO_INVERSE")
			q.raise(libEC, "ERR_R_BN_LIB")
			q.raise(libEC, "ERR_R_EC_LIB")
			return nil
		}
		if p.BitLen() <= 2 {
			q.raise(libEC, "EC_R_INVALID_FIELD")
			q.raise(libEC, "ERR_R_EC_LIB")
			return nil
		}
		g.p = p
		g.a = new(big.Int).Mod(a, p)
		g.b = new(big.Int).Mod(b, p)
	default:
		q.raise(libEC, "EC_R_INVALID_FIELD")
		return nil
	}
	if seed := curve.fields[2]; seed != nil {
		if len(seed.data) == 0 && seed.bits == 0 {
			q.raise(libEC, "EC_R_ASN1_ERROR")
			return nil
		}
		g.seed = seed.data
	}
	base := v.fields[3].data
	if len(base) == 0 {
		q.raise(libEC, "EC_R_ASN1_ERROR")
		return nil
	}
	x, y, inf, ok := e.oct2point(g, base)
	if !ok {
		q.raise(libEC, "ERR_R_EC_LIB")
		return nil
	}
	order := integerValue(v.fields[4])
	if order.Sign() <= 0 {
		q.raise(libEC, "EC_R_INVALID_GROUP_ORDER")
		return nil
	}
	if order.BitLen() > fieldBits+1 {
		q.raise(libEC, "EC_R_INVALID_GROUP_ORDER")
		return nil
	}
	var cofactor *big.Int
	if c := v.fields[5]; c != nil {
		cofactor = integerValue(c)
	}
	if !e.setGenerator(g, x, y, inf, order, cofactor) {
		q.raise(libEC, "ERR_R_EC_LIB")
		return nil
	}
	// Replace with the named curve the parameters match. With the
	// generator at infinity EC_POINT_get_affine_coordinates fails and
	// ossl_ec_curve_nid_from_params returns its initial -1, which is not
	// NID_undef: EC_GROUP_new_by_curve_name(-1) fails (OBJ_nid2sn(-1) for
	// the error data, then EC_R_UNKNOWN_GROUP).
	if g.gx == nil {
		q.raise(libEC, "EC_R_POINT_AT_INFINITY")
		q.raiseDataNested(libEC, "EC_R_UNKNOWN_GROUP", libOBJ, "OBJ_R_UNKNOWN_NID")
		q.raise(libEC, "ERR_R_EC_LIB")
		return nil
	}
	if nid := curveNIDFromParams(g); nid != 0 {
		named := e.groupByNID(nid)
		named.explicit = true
		if curve.fields[2] == nil {
			named.seed = nil
		}
		return named
	}
	return g
}

// setGenerator ports EC_GROUP_set_generator.
func (e *osslEnv) setGenerator(g *ecGroup, x, y *big.Int, inf bool, order, cofactor *big.Int) bool {
	if g.p.Sign() <= 0 {
		e.q.raise(libEC, "EC_R_INVALID_FIELD")
		return false
	}
	fieldBits := g.p.BitLen()
	if order.Sign() <= 0 || order.BitLen() > fieldBits+1 {
		e.q.raise(libEC, "EC_R_INVALID_GROUP_ORDER")
		return false
	}
	if cofactor != nil && cofactor.Sign() < 0 {
		e.q.raise(libEC, "EC_R_UNKNOWN_COFACTOR")
		return false
	}
	if inf {
		g.gx, g.gy = nil, nil
	} else {
		g.gx, g.gy = x, y
	}
	g.order = order
	if cofactor != nil && cofactor.Sign() != 0 {
		g.cofactor = cofactor
	} else {
		g.cofactor = guessCofactor(g)
	}
	return true
}

// guessCofactor ports ec_lib.c ec_guess_cofactor.
func guessCofactor(g *ecGroup) *big.Int {
	if g.order.BitLen() <= (g.p.BitLen()+1)/2+3 {
		return new(big.Int)
	}
	q := new(big.Int)
	if g.binary {
		q.SetBit(q, g.p.BitLen()-1, 1)
	} else {
		q.Set(g.p)
	}
	h := new(big.Int).Rsh(g.order, 1)
	h.Add(h, q).Add(h, big.NewInt(1))
	return h.Div(h, g.order)
}

// curveNIDFromParams ports ec_curve.c ossl_ec_curve_nid_from_params as
// EC_GROUP_new_from_ecparameters calls it: on a copy of the group with the
// seed removed (EC_GROUP_set_seed(dup, NULL, 0)) and the generator set
// again with no cofactor (EC_GROUP_set_generator(dup, point, order, NULL)),
// so the cofactor compared is ec_guess_cofactor's and the seed is never
// compared. The parameters' own cofactor and seed do not stop a match.
func curveNIDFromParams(g *ecGroup) int {
	paramLen := (g.order.BitLen() + 7) / 8
	if l := (g.p.BitLen() + 7) / 8; l > paramLen {
		paramLen = l
	}
	cofactor := guessCofactor(g)
	pad := func(n *big.Int) []byte { return n.FillBytes(make([]byte, paramLen)) }
	want := bytes.Join([][]byte{pad(g.p), pad(g.a), pad(g.b), pad(g.gx), pad(g.gy), pad(g.order)}, nil)
	for _, entry := range curveList {
		c := curveByNID(entry.nid)
		if c == nil || c.binary != g.binary || entry.paramLen != paramLen {
			continue
		}
		if cofactor.Sign() != 0 && cofactor.Cmp(big.NewInt(int64(entry.cofactor))) != 0 {
			continue
		}
		have := bytes.Join([][]byte{pad(hexInt(c.p)), pad(hexInt(c.a)), pad(hexInt(c.b)), pad(hexInt(c.gx)), pad(hexInt(c.gy)), pad(hexInt(c.order))}, nil)
		if bytes.Equal(have, want) {
			return entry.nid
		}
	}
	return 0
}

func curveByNID(nid int) *namedCurve {
	for i := range namedCurves {
		if namedCurves[i].nid == nid {
			return &namedCurves[i]
		}
	}
	return nil
}

// ecPointDecode ports o2i_ECPublicKey's EC_KEY_oct2key: a point on g.
func (e *osslEnv) ecPointDecode(g *ecGroup, buf []byte) bool {
	_, _, _, ok := e.oct2point(g, buf)
	return ok
}

// oct2point ports EC_POINT_oct2point (ecp_oct.c and ec2_oct.c
// ossl_ec_GF*_simple_oct2point).
func (e *osslEnv) oct2point(g *ecGroup, buf []byte) (x, y *big.Int, inf, ok bool) {
	q := e.q
	if len(buf) == 0 {
		q.raise(libEC, "EC_R_BUFFER_TOO_SMALL")
		return nil, nil, false, false
	}
	form := buf[0] &^ 1
	ybit := buf[0] & 1
	if form != 0 && form != 2 && form != 4 && form != 6 {
		q.raise(libEC, "EC_R_INVALID_ENCODING")
		return nil, nil, false, false
	}
	if (form == 0 || form == 4) && ybit != 0 {
		q.raise(libEC, "EC_R_INVALID_ENCODING")
		return nil, nil, false, false
	}
	if form == 0 {
		if len(buf) != 1 {
			q.raise(libEC, "EC_R_INVALID_ENCODING")
			return nil, nil, false, false
		}
		return nil, nil, true, true
	}
	var fieldLen int
	if g.binary {
		fieldLen = (g.degree() + 7) / 8
	} else {
		fieldLen = (g.p.BitLen() + 7) / 8
	}
	encLen := 1 + 2*fieldLen
	if form == 2 {
		encLen = 1 + fieldLen
	}
	if len(buf) != encLen {
		q.raise(libEC, "EC_R_INVALID_ENCODING")
		return nil, nil, false, false
	}
	x = new(big.Int).SetBytes(buf[1 : 1+fieldLen])
	if g.binary {
		if x.BitLen() > g.degree() {
			q.raise(libEC, "EC_R_INVALID_ENCODING")
			return nil, nil, false, false
		}
	} else if x.Cmp(g.p) >= 0 {
		q.raise(libEC, "EC_R_INVALID_ENCODING")
		return nil, nil, false, false
	}
	if form == 2 {
		y, ok = e.decompress(g, x, int(ybit))
		if !ok {
			return nil, nil, false, false
		}
	} else {
		y = new(big.Int).SetBytes(buf[1+fieldLen:])
		if g.binary {
			if y.BitLen() > g.degree() {
				q.raise(libEC, "EC_R_INVALID_ENCODING")
				return nil, nil, false, false
			}
		} else if y.Cmp(g.p) >= 0 {
			q.raise(libEC, "EC_R_INVALID_ENCODING")
			return nil, nil, false, false
		}
		if form == 6 {
			if g.binary {
				if x.Sign() == 0 {
					if ybit != 0 {
						q.raise(libEC, "EC_R_INVALID_ENCODING")
						return nil, nil, false, false
					}
				} else {
					// field_div: BN_GF2m_mod_div fails, raising nothing,
					// when x has no inverse modulo a reducible polynomial.
					yxi, ok := gf2Div(y, x, g.gf)
					if !ok {
						return nil, nil, false, false
					}
					if uint(yxi.Bit(0)) != uint(ybit) {
						q.raise(libEC, "EC_R_INVALID_ENCODING")
						return nil, nil, false, false
					}
				}
			} else if uint(y.Bit(0)) != uint(ybit) {
				q.raise(libEC, "EC_R_INVALID_ENCODING")
				return nil, nil, false, false
			}
		}
	}
	if !g.onCurve(x, y) {
		q.raise(libEC, "EC_R_POINT_IS_NOT_ON_CURVE")
		return nil, nil, false, false
	}
	return x, y, false, true
}

func (g *ecGroup) degree() int { return g.p.BitLen() - 1 }

// onCurve is the simple is_on_curve check for an affine point.
func (g *ecGroup) onCurve(x, y *big.Int) bool {
	if g.binary {
		// ossl_ec_GF2m_simple_is_on_curve: ((x + a)·x + y)·x + b + y² = 0.
		f := g.gf
		lh := f.mul(gf2Add(x, g.a), x)
		lh = f.mul(gf2Add(lh, y), x)
		lh = gf2Add(gf2Add(lh, g.b), f.sqr(y))
		return lh.Sign() == 0
	}
	lhs := new(big.Int).Mul(y, y)
	lhs.Mod(lhs, g.p)
	rhs := new(big.Int).Mul(x, x)
	rhs.Mul(rhs, x)
	ax := new(big.Int).Mul(g.a, x)
	rhs.Add(rhs, ax).Add(rhs, g.b).Mod(rhs, g.p)
	return lhs.Cmp(rhs) == 0
}

// decompress ports EC_POINT_set_compressed_coordinates (GFp: BN_mod_sqrt;
// GF2m: BN_GF2m_mod_solve_quad).
func (e *osslEnv) decompress(g *ecGroup, x *big.Int, ybit int) (*big.Int, bool) {
	q := e.q
	if g.binary {
		// ossl_ec_GF2m_simple_set_compressed_coordinates.
		f := g.gf
		var y *big.Int
		xr := f.mod(x)
		if xr.Sign() == 0 {
			y = gf2Sqrt(g.b, f)
		} else {
			t := f.sqr(xr)
			t, ok := gf2Div(g.b, t, f)
			if !ok {
				return nil, false // field_div fails silently
			}
			t = gf2Add(gf2Add(g.a, t), xr)
			q.setMark()
			z, reason := gf2SolveQuad(t, f)
			switch reason {
			case "":
				q.clearLastMark()
			case "BN_R_NO_SOLUTION":
				q.raise(libBN, reason)
				q.popToMark()
				q.raise(libEC, "EC_R_INVALID_COMPRESSED_POINT")
				return nil, false
			default:
				q.raise(libBN, reason)
				q.clearLastMark()
				q.raise(libEC, "ERR_R_BN_LIB")
				return nil, false
			}
			y = f.mul(xr, z)
			if int(z.Bit(0)) != ybit {
				y = gf2Add(y, xr)
			}
		}
		if !g.onCurve(xr, y) {
			q.raise(libEC, "EC_R_POINT_IS_NOT_ON_CURVE")
			return nil, false
		}
		return y, true
	}
	p := g.p
	xr := new(big.Int).Mod(x, p)
	t := new(big.Int).Mul(xr, xr)
	t.Mul(t, xr)
	t.Add(t, new(big.Int).Mul(g.a, xr))
	t.Add(t, g.b).Mod(t, p)
	// ERR_set_mark around BN_mod_sqrt: a BN "not a square" is popped and
	// replaced by EC_R_INVALID_COMPRESSED_POINT; any other BN error is
	// kept, followed by ERR_R_BN_LIB.
	q.setMark()
	y, reason := modSqrt(t, p)
	switch reason {
	case "":
		q.clearLastMark()
	case "BN_R_NOT_A_SQUARE":
		q.raise(libBN, reason)
		q.popToMark()
		q.raise(libEC, "EC_R_INVALID_COMPRESSED_POINT")
		return nil, false
	default:
		q.raise(libBN, reason)
		q.clearLastMark()
		q.raise(libEC, "ERR_R_BN_LIB")
		return nil, false
	}
	if int(y.Bit(0)) != ybit {
		if y.Sign() == 0 {
			if big.Jacobi(xr, p) == 1 {
				q.raise(libEC, "EC_R_INVALID_COMPRESSION_BIT")
			} else {
				q.raise(libEC, "EC_R_INVALID_COMPRESSED_POINT")
			}
			return nil, false
		}
		y.Sub(p, y)
	}
	if !g.onCurve(xr, y) {
		q.raise(libEC, "EC_R_POINT_IS_NOT_ON_CURVE")
		return nil, false
	}
	return y, true
}

// modSqrt ports bn_sqrt.c BN_mod_sqrt for an odd p > 1: the root, or the
// BN reason it fails with. It runs OpenSSL's algorithm, which for a
// composite p (explicit parameters) can find a "root" or fail in its own
// ways, including its random search for a non-residue.
func modSqrt(a, p *big.Int) (*big.Int, string) {
	if p.Bit(0) == 0 || p.Cmp(big.NewInt(1)) == 0 {
		if p.Cmp(big.NewInt(2)) == 0 {
			return big.NewInt(int64(a.Bit(0))), ""
		}
		return nil, "BN_R_P_IS_NOT_PRIME"
	}
	if a.Sign() == 0 || a.Cmp(big.NewInt(1)) == 0 {
		return new(big.Int).Set(a), ""
	}
	A := new(big.Int).Mod(a, p)
	e := 1
	for p.Bit(e) == 0 {
		e++
	}
	var ret *big.Int
	modmul := func(x, y *big.Int) *big.Int { r := new(big.Int).Mul(x, y); return r.Mod(r, p) }
	if e == 1 {
		q := new(big.Int).Rsh(p, 2)
		q.Add(q, big.NewInt(1))
		ret = new(big.Int).Exp(A, q, p)
	} else if e == 2 {
		t := new(big.Int).Lsh(A, 1)
		t.Mod(t, p)
		q := new(big.Int).Rsh(p, 3)
		b := new(big.Int).Exp(t, q, p)
		y := modmul(b, b)
		t = modmul(t, y)
		t.Sub(t, big.NewInt(1))
		x := modmul(A, b)
		x = modmul(x, t)
		ret = x
	} else {
		q := new(big.Int).Set(p)
		var y *big.Int
		r := 1
		i := 2
		for {
			if i < 22 {
				y = big.NewInt(int64(i))
			} else {
				// BN_priv_rand_ex(y, BN_num_bits(p), 0, 0): a random
				// candidate below 2^bits, reduced once below p (0 → i).
				// For a prime p any non-residue gives the same point; for a
				// composite p the outcome depends on the candidates, and
				// Go's is as random as OpenSSL's (KNOWN_DIFFERENCES).
				y = bnPrivRand(p.BitLen(), bnRandTopOne)
				if y.Cmp(p) >= 0 {
					y.Sub(y, p)
				}
				if y.Sign() == 0 {
					y.SetInt64(int64(i))
				}
			}
			r = kronecker(y, q)
			if r == 0 {
				return nil, "BN_R_P_IS_NOT_PRIME"
			}
			if !(r == 1 && func() bool { i++; return i < 82 }()) {
				break
			}
		}
		if r != -1 {
			return nil, "BN_R_TOO_MANY_ITERATIONS"
		}
		q.Rsh(q, uint(e))
		y.Exp(y, q, p)
		if y.Cmp(big.NewInt(1)) == 0 {
			return nil, "BN_R_P_IS_NOT_PRIME"
		}
		t := new(big.Int).Rsh(q, 1)
		var x *big.Int
		if t.Sign() == 0 {
			t.Mod(A, p)
			if t.Sign() == 0 {
				return new(big.Int), ""
			}
			x = big.NewInt(1)
		} else {
			x = new(big.Int).Exp(A, t, p)
			if x.Sign() == 0 {
				return new(big.Int), ""
			}
		}
		b := modmul(x, x)
		b = modmul(b, A)
		x = modmul(x, A)
		for {
			if b.Cmp(big.NewInt(1)) == 0 {
				ret = x
				break
			}
			i := 1
			var tt *big.Int
			for ; i < e; i++ {
				if i == 1 {
					tt = modmul(b, b)
				} else {
					tt = modmul(tt, tt)
				}
				if tt.Cmp(big.NewInt(1)) == 0 {
					break
				}
			}
			if i >= e {
				return nil, "BN_R_NOT_A_SQUARE"
			}
			tt = new(big.Int).Set(y)
			for j := e - i - 1; j > 0; j-- {
				tt = modmul(tt, tt)
			}
			y = modmul(tt, tt)
			x = modmul(x, tt)
			b = modmul(b, y)
			e = i
		}
	}
	if modmul(ret, ret).Cmp(A) != 0 {
		return nil, "BN_R_NOT_A_SQUARE"
	}
	return ret, ""
}

// kronecker is BN_kronecker for a small positive a and odd positive b.
func kronecker(a, b *big.Int) int {
	if new(big.Int).GCD(nil, nil, a, b).Cmp(big.NewInt(1)) != 0 {
		return 0
	}
	return big.Jacobi(a, b)
}

// --- EC private keys -------------------------------------------------------

// d2iECPrivateKey ports d2i_ECPrivateKey into *g (the group the PKCS#8
// algorithm gave, or nil): the structure, its parameters, the private
// scalar and the public point (decoded, or derived as priv*G).
func (e *osslEnv) d2iECPrivateKey(g **ecGroup, der []byte) bool {
	q := e.q
	v, _, ok := e.d2i(ecPrivateKeyItem, der)
	if !ok {
		return false
	}
	if params := v.fields[2]; params != nil {
		ng := e.groupFromECPKParameters(params)
		*g = ng
	}
	if *g == nil {
		q.raise(libEC, "ERR_R_EC_LIB")
		return false
	}
	grp := *g
	priv := new(big.Int).SetBytes(v.fields[1].data)
	if pub := v.fields[3]; pub != nil {
		if !e.ecPointDecode(grp, pub.data) {
			q.raise(libEC, "ERR_R_EC_LIB")
			return false
		}
		return true
	}
	// keygenpub: EC_POINT_mul(priv * G).
	return e.ecKeygenPub(grp, priv)
}
