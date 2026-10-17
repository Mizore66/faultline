package witness

import (
	"math/big"
	"strings"
	"testing"
)

// checkKey compares one parse with Node's result: "OK <type>" (type ""
// for an undefined asymmetricKeyType) or the message and the stack.
func checkKey(t *testing.T, name, hexDER, label string, public bool, want string, stack ...string) {
	t.Helper()
	text := derBlock(label, hexDER)
	parse := parsePrivate
	if public {
		parse = parsePublic
	}
	key, err := parse(text)
	if err != nil {
		var got []string
		if ke, ok := err.(*keyError); ok {
			got = ke.stack
		}
		if err.Error() != want || strings.Join(got, "\n") != strings.Join(stack, "\n") {
			t.Errorf("%s (public=%v): got %q %q, want %q %q", name, public, err.Error(), got, want, stack)
		}
		return
	}
	if got := "OK " + key.typ; got != want {
		t.Errorf("%s (public=%v): got %q, want %q", name, public, got, want)
	}
}

// Node 22 (OpenSSL 3.5.5 and 3.6.5) results for the r9-1 reproductions;
// each is the same on every run (checked 200 times).
const (
	// x^163 + x + 1 (reducible) with a hybrid point whose y/x parity is
	// right for the true inverse modulo the polynomial (06) or for the
	// field-only formula a^(2^m-2) (07).
	hybTrue      = "3081d33081a206072a8648ce3d0201308196020101301d06072a8648ce3d01023012020200a306092a8648ce3d01020302020101302e04150000000000000000000000000000000000000000010415001007d54490e7b7e68fda435f1f2b908cc540c413042b0401f9ebdacc0cb1e29c658cda1495e60af593bd04cf034a23d5962217beaddbc496cb8e81973e0becd7b00215040000000000000000000000000000000000000001032c000601f9ebdacc0cb1e29c658cda1495e60af593bd04cf034a23d5962217beaddbc496cb8e81973e0becd7b0"
	hybFieldOnly = "3081d33081a206072a8648ce3d0201308196020101301d06072a8648ce3d01023012020200a306092a8648ce3d01020302020101302e04150000000000000000000000000000000000000000010415001007d54490e7b7e68fda435f1f2b908cc540c413042b0401f9ebdacc0cb1e29c658cda1495e60af593bd04cf034a23d5962217beaddbc496cb8e81973e0becd7b00215040000000000000000000000000000000000000001032c000701f9ebdacc0cb1e29c658cda1495e60af593bd04cf034a23d5962217beaddbc496cb8e81973e0becd7b0"
	// An EC private key without its public point over x^113 + x^27 + x^12
	// + x^10 + 1 (reducible): priv·G fails in the ladder's last inversion.
	ecPrivLadderFails = "308198020101040f000000000000000000000000000005a08181307f020101302406072a8648ce3d0102301902017106092a8648ce3d01020303300902010a02010c02011b3022040f000000000000000000000000000000040f018e2c28da7286fa8832f6e610c835041f040118b2a07313a14fcd8a97eff6475b01c65b7c193d3c42adcbd2da77c86b020f010000000000000000000000000001"
	// SM2's explicit parameters with cofactor 2 and with a 9-byte cofactor.
	sm2Cofactor2 = "3081f33081cc06072a8648ce3d02013081c0020101302c06072a8648ce3d0101022100fffffffeffffffffffffffffffffffffffffffff00000000ffffffffffffffff30440420fffffffeffffffffffffffffffffffffffffffff00000000fffffffffffffffc042028e9fa9e9d9f5e344d5a9e4bcf6509a7f39789f515ab8f92ddbcbd414d940e9304210232c4ae2c1f1981195f9904466a39c9948fe30bbff2660be1715a4589334c74c7022100fffffffeffffffffffffffffffffffff7203df6b21c6052b53bbf40939d54123020102032200024aaf92925938e1777c3b23c3d5de323ff976ca01421ea2edd3ebe2acb2e6f0ae"
	sm2Cofactor9 = "3081fb3081d406072a8648ce3d02013081c8020101302c06072a8648ce3d0101022100fffffffeffffffffffffffffffffffffffffffff00000000ffffffffffffffff30440420fffffffeffffffffffffffffffffffffffffffff00000000fffffffffffffffc042028e9fa9e9d9f5e344d5a9e4bcf6509a7f39789f515ab8f92ddbcbd414d940e9304210232c4ae2c1f1981195f9904466a39c9948fe30bbff2660be1715a4589334c74c7022100fffffffeffffffffffffffffffffffff7203df6b21c6052b53bbf40939d5412302097f7f7f7f7f7f7f7f7f032200024aaf92925938e1777c3b23c3d5de323ff976ca01421ea2edd3ebe2acb2e6f0ae"
)

// TestReducibleBinaryFieldKeys pins the r9-1 #1, #2 and #5 reproductions.
func TestReducibleBinaryFieldKeys(t *testing.T) {
	const decodeErr = "error:03000072:digital envelope routines::decode error"
	const unsupported = "error:1E08010C:DECODER routines::unsupported"
	checkKey(t, "hybTrue", hybTrue, "PUBLIC KEY", true, "OK ec")
	checkKey(t, "hybTrue", hybTrue, "PUBLIC KEY", false, unsupported, unsupported)
	checkKey(t, "hybFieldOnly", hybFieldOnly, "PUBLIC KEY", true, decodeErr, unsupported, decodeErr)
	ladder := []string{
		"error:0688010A:asn1 encoding routines::nested asn1 error",
		"error:0688010A:asn1 encoding routines::nested asn1 error",
		"error:068000A8:asn1 encoding routines::wrong tag",
		"error:08000088:elliptic curve routines::ladder post failure",
		"error:080000A5:elliptic curve routines::cannot invert",
		unsupported,
	}
	checkKey(t, "ecPrivLadderFails", ecPrivLadderFails, "EC PRIVATE KEY", true, unsupported, ladder...)
	checkKey(t, "ecPrivLadderFails", ecPrivLadderFails, "EC PRIVATE KEY", false, unsupported, ladder...)
	checkKey(t, "sm2Cofactor2", sm2Cofactor2, "PUBLIC KEY", true, "OK ")
	checkKey(t, "sm2Cofactor9", sm2Cofactor9, "PUBLIC KEY", true, "OK ")
}

// gf2Ref is schoolbook GF(2)[x] arithmetic for the checks below.
func gf2RefMulMod(a, b, p *big.Int) *big.Int {
	r := new(big.Int)
	for i := 0; i < b.BitLen(); i++ {
		if b.Bit(i) == 1 {
			r.Xor(r, new(big.Int).Lsh(a, uint(i)))
		}
	}
	d := p.BitLen() - 1
	for r.BitLen()-1 >= d {
		r.Xor(r, new(big.Int).Lsh(p, uint(r.BitLen()-1-d)))
	}
	return r
}

func gf2RefGCD(a, b *big.Int) *big.Int {
	a, b = new(big.Int).Set(a), new(big.Int).Set(b)
	for b.Sign() != 0 {
		for a.BitLen() >= b.BitLen() {
			a.Xor(a, new(big.Int).Lsh(b, uint(a.BitLen()-b.BitLen())))
		}
		a, b = b, a
	}
	return a
}

// TestGF2Arithmetic checks bn_gf2m.c's algorithms modulo reducible and
// irreducible polynomials: the inverse exists exactly when gcd(a, p) = 1
// and is then the true inverse (r9-1 #1: a^(2^m-2) is not, modulo a
// reducible p); sqrt is a^(2^(m-1)); solve_quad's z satisfies z² + z = a.
func TestGF2Arithmetic(t *testing.T) {
	polys := []*big.Int{
		big.NewInt(1<<8 | 1<<4 | 1<<3 | 1<<1 | 1), // irreducible (AES)
		big.NewInt(1<<8 | 1<<4 | 1),               // (x^4+x^2+1)^2... reducible
		big.NewInt(1<<7 | 1<<1 | 1),               // irreducible
		big.NewInt(1<<9 | 1<<3 | 1<<2 | 1<<1 | 1), // reducible
		big.NewInt(1<<6 | 1<<3 | 1),               // reducible
	}
	one := big.NewInt(1)
	for _, p := range polys {
		f := newGF2Field(p)
		m := f.poly[0]
		for v := int64(0); v < 1<<m; v++ {
			a := big.NewInt(v)
			inv, ok := gf2InvVartime(a, f)
			coprime := v != 0 && gf2RefGCD(a, p).Cmp(one) == 0
			if ok != coprime {
				t.Fatalf("p=%b a=%b: inverse ok=%v, gcd=1 is %v", p, a, ok, coprime)
			}
			if ok && gf2RefMulMod(a, inv, p).Cmp(one) != 0 {
				t.Fatalf("p=%b a=%b: a·inv = %b", p, a, gf2RefMulMod(a, inv, p))
			}
			// The blinded inverse: the same inverse when it succeeds,
			// never a success without one.
			if binv, bok := gf2Inv(a, f); bok && (!coprime || binv.Cmp(inv) != 0) {
				t.Fatalf("p=%b a=%b: blinded inverse %b (ok %v), exact %b", p, a, binv, bok, inv)
			}
			if got := f.mul(a, big.NewInt(v^0x55)); got.Cmp(gf2RefMulMod(a, big.NewInt(v^0x55), p)) != 0 {
				t.Fatalf("p=%b: mul(%b) = %b", p, a, got)
			}
			want := new(big.Int).Set(a)
			for i := 0; i < m-1; i++ {
				want = gf2RefMulMod(want, want, p)
			}
			if got := gf2Sqrt(a, f); got.Cmp(want) != 0 {
				t.Fatalf("p=%b: sqrt(%b) = %b, want %b", p, a, got, want)
			}
			if z, reason := gf2SolveQuad(a, f); reason == "" {
				if gf2Add(gf2RefMulMod(z, z, p), z).Cmp(a) != 0 {
					t.Fatalf("p=%b a=%b: solve_quad gave %b", p, a, z)
				}
			} else if reason != "BN_R_NO_SOLUTION" && reason != "BN_R_TOO_MANY_ITERATIONS" {
				t.Fatalf("p=%b a=%b: reason %q", p, a, reason)
			}
		}
	}
}

// TestKeygenOnField checks that priv·G succeeds on an irreducible binary
// field and a prime field for any scalar (both ladder and wNAF paths).
func TestKeygenOnField(t *testing.T) {
	q := &errQueue{}
	e := &osslEnv{q: q}
	// sect163k1's polynomial with an arbitrary curve through (gx, gy).
	p := new(big.Int).SetBit(new(big.Int), 163, 1)
	p.SetBit(p, 7, 1).SetBit(p, 6, 1).SetBit(p, 3, 1).SetBit(p, 0, 1)
	f := newGF2Field(p)
	gx, gy := big.NewInt(0x1234567), big.NewInt(0x7654321)
	a := big.NewInt(1)
	x2 := f.sqr(gx)
	b := gf2Add(gf2Add(gf2Add(f.sqr(gy), f.mul(gx, gy)), f.mul(x2, gx)), f.mul(a, x2))
	g := &ecGroup{binary: true, p: p, gf: f, a: a, b: b, gx: gx, gy: gy,
		order: new(big.Int).Lsh(one(), 162), cofactor: big.NewInt(2)}
	for _, cof := range []int64{2, 0} {
		g.cofactor = big.NewInt(cof)
		for k := int64(1); k < 40; k++ {
			if !e.ecKeygenPub(g, big.NewInt(k*7919)) || !q.empty() {
				t.Fatalf("binary cofactor %d: priv %d failed: %v", cof, k*7919, q.errors())
			}
		}
	}
	// P-256's field with a curve through (gx, gy).
	pp := hexInt("ffffffff00000001000000000000000000000000ffffffffffffffffffffffff")
	pa := big.NewInt(-3)
	pa.Mod(pa, pp)
	pb := new(big.Int).Mul(gy, gy)
	pb.Sub(pb, new(big.Int).Exp(gx, big.NewInt(3), nil)).Sub(pb, new(big.Int).Mul(pa, gx)).Mod(pb, pp)
	gp := &ecGroup{p: pp, a: pa, b: pb, gx: gx, gy: gy, order: new(big.Int).Rsh(pp, 1)}
	for _, cof := range []int64{1, 0} {
		gp.cofactor = big.NewInt(cof)
		for k := int64(1); k < 40; k++ {
			if !e.ecKeygenPub(gp, big.NewInt(k*104729)) || !q.empty() {
				t.Fatalf("prime cofactor %d: priv %d failed: %v", cof, k*104729, q.errors())
			}
		}
	}
}

func one() *big.Int { return big.NewInt(1) }
