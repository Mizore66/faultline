// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mldsa is the part of Go's crypto/internal/fips140/mldsa that
// OpenSSL 3.5's ML-DSA key load needs: key generation from a seed, and the
// semi-expanded private key decoding and consistency check
// (ossl_ml_dsa_sk_decode). Signing and verification are left out.
package mldsa

import (
	"crypto/sha3"
	"math/bits"
)

const (
	q        = 8380417    // 2²³ - 2¹³ + 1
	R        = 4294967296 // 2³²
	RR       = 2365951    // R² mod q, aka R in the Montgomery domain
	qNegInv  = 4236238847 // -q⁻¹ mod R (q * qNegInv ≡ -1 mod R)
	one      = 4193792    // R mod q, aka 1 in the Montgomery domain
	minusOne = 4186625    // (q - 1) * R mod q, aka -1 in the Montgomery domain
)

// fieldElement is an element n of ℤ_q in the Montgomery domain, represented as
// an integer x in [0, q) such that x ≡ n * R (mod q) where R = 2³².
type fieldElement uint32

// fieldSubToMontgomery converts a difference a - b to Montgomery form.
// a and b must be < q. (This bound can probably be relaxed.)
func fieldSubToMontgomery(a, b uint32) fieldElement {
	x := a - b + q
	return fieldMontgomeryMul(fieldElement(x), RR)
}

// fieldFromMontgomery converts a value a in Montgomery form back to
// standard representation.
func fieldFromMontgomery(a fieldElement) uint32 {
	// (a * R) * 1 * R⁻¹ ≡ a (mod q)
	return uint32(fieldMontgomeryReduce(uint64(a)))
}

// fieldCenteredMod returns r mod± q, the value r reduced to the range
// [−(q−1)/2, (q−1)/2].
func fieldCenteredMod(r fieldElement) int32 {
	x := int32(fieldFromMontgomery(r))
	// x <= q / 2 ? x : x - q
	if x <= q/2 {
		return x
	}
	return x - q
}

// fieldReduceOnce reduces a value a < 2q.
func fieldReduceOnce(a uint32) fieldElement {
	x, b := bits.Sub64(uint64(a), uint64(q), 0)
	return fieldElement(x + b*q)
}

// fieldAdd returns a + b mod q.
func fieldAdd(a, b fieldElement) fieldElement {
	x := uint32(a + b)
	return fieldReduceOnce(x)
}

// fieldSub returns a - b mod q.
func fieldSub(a, b fieldElement) fieldElement {
	x := uint32(a - b + q)
	return fieldReduceOnce(x)
}

// fieldMontgomeryMul returns a * b * R⁻¹ mod q.
func fieldMontgomeryMul(a, b fieldElement) fieldElement {
	x := uint64(a) * uint64(b)
	return fieldMontgomeryReduce(x)
}

// fieldMontgomeryReduce returns x * R⁻¹ mod q for x < q * R.
func fieldMontgomeryReduce(x uint64) fieldElement {
	t := uint32(x) * qNegInv
	u := (x + uint64(t)*q) >> 32
	return fieldReduceOnce(uint32(u))
}

// fieldMontgomeryMulSub returns a * (b - c). This operation is fused to save a
// fieldReduceOnce after the subtraction.
func fieldMontgomeryMulSub(a, b, c fieldElement) fieldElement {
	x := uint64(a) * uint64(b-c+q)
	return fieldMontgomeryReduce(x)
}

const n = 256

// ringElement is a polynomial, an element of R_q.
type ringElement [n]fieldElement

// polyAdd adds two ringElements or nttElements.
func polyAdd[T ~[n]fieldElement](a, b T) (s T) {
	for i := range s {
		s[i] = fieldAdd(a[i], b[i])
	}
	return s
}

// nttElement is an NTT representation, an element of T_q.
type nttElement [n]fieldElement

// zetas are the values ζ^BitRev₈(k) mod q for each index k, converted to the
// Montgomery domain.
var zetas = [256]fieldElement{4193792, 25847, 5771523, 7861508, 237124, 7602457, 7504169, 466468, 1826347, 2353451, 8021166, 6288512, 3119733, 5495562, 3111497, 2680103, 2725464, 1024112, 7300517, 3585928, 7830929, 7260833, 2619752, 6271868, 6262231, 4520680, 6980856, 5102745, 1757237, 8360995, 4010497, 280005, 2706023, 95776, 3077325, 3530437, 6718724, 4788269, 5842901, 3915439, 4519302, 5336701, 3574422, 5512770, 3539968, 8079950, 2348700, 7841118, 6681150, 6736599, 3505694, 4558682, 3507263, 6239768, 6779997, 3699596, 811944, 531354, 954230, 3881043, 3900724, 5823537, 2071892, 5582638, 4450022, 6851714, 4702672, 5339162, 6927966, 3475950, 2176455, 6795196, 7122806, 1939314, 4296819, 7380215, 5190273, 5223087, 4747489, 126922, 3412210, 7396998, 2147896, 2715295, 5412772, 4686924, 7969390, 5903370, 7709315, 7151892, 8357436, 7072248, 7998430, 1349076, 1852771, 6949987, 5037034, 264944, 508951, 3097992, 44288, 7280319, 904516, 3958618, 4656075, 8371839, 1653064, 5130689, 2389356, 8169440, 759969, 7063561, 189548, 4827145, 3159746, 6529015, 5971092, 8202977, 1315589, 1341330, 1285669, 6795489, 7567685, 6940675, 5361315, 4499357, 4751448, 3839961, 2091667, 3407706, 2316500, 3817976, 5037939, 2244091, 5933984, 4817955, 266997, 2434439, 7144689, 3513181, 4860065, 4621053, 7183191, 5187039, 900702, 1859098, 909542, 819034, 495491, 6767243, 8337157, 7857917, 7725090, 5257975, 2031748, 3207046, 4823422, 7855319, 7611795, 4784579, 342297, 286988, 5942594, 4108315, 3437287, 5038140, 1735879, 203044, 2842341, 2691481, 5790267, 1265009, 4055324, 1247620, 2486353, 1595974, 4613401, 1250494, 2635921, 4832145, 5386378, 1869119, 1903435, 7329447, 7047359, 1237275, 5062207, 6950192, 7929317, 1312455, 3306115, 6417775, 7100756, 1917081, 5834105, 7005614, 1500165, 777191, 2235880, 3406031, 7838005, 5548557, 6709241, 6533464, 5796124, 4656147, 594136, 4603424, 6366809, 2432395, 2454455, 8215696, 1957272, 3369112, 185531, 7173032, 5196991, 162844, 1616392, 3014001, 810149, 1652634, 4686184, 6581310, 5341501, 3523897, 3866901, 269760, 2213111, 7404533, 1717735, 472078, 7953734, 1723600, 6577327, 1910376, 6712985, 7276084, 8119771, 4546524, 5441381, 6144432, 7959518, 6094090, 183443, 7403526, 1612842, 4834730, 7826001, 3919660, 8332111, 7018208, 3937738, 1400424, 7534263, 1976782}

// ntt maps a ringElement to its nttElement representation.
//
// It implements NTT, according to FIPS 203, Algorithm 9.
func ntt(f ringElement) nttElement {
	var m uint8

	for len := 128; len >= 8; len /= 2 {
		for start := 0; start < 256; start += 2 * len {
			m++
			zeta := zetas[m]

			// Bounds check elimination hint.
			f, flen := f[start:start+len], f[start+len:start+len+len]
			for j := 0; j < len; j += 2 {
				t := fieldMontgomeryMul(zeta, flen[j])
				flen[j] = fieldSub(f[j], t)
				f[j] = fieldAdd(f[j], t)

				// Unroll by 2 for performance.
				t = fieldMontgomeryMul(zeta, flen[j+1])
				flen[j+1] = fieldSub(f[j+1], t)
				f[j+1] = fieldAdd(f[j+1], t)
			}
		}
	}

	// Unroll len = 4, 2, and 1.
	for start := 0; start < 256; start += 8 {
		m++
		zeta := zetas[m]

		t := fieldMontgomeryMul(zeta, f[start+4])
		f[start+4] = fieldSub(f[start], t)
		f[start] = fieldAdd(f[start], t)

		t = fieldMontgomeryMul(zeta, f[start+5])
		f[start+5] = fieldSub(f[start+1], t)
		f[start+1] = fieldAdd(f[start+1], t)

		t = fieldMontgomeryMul(zeta, f[start+6])
		f[start+6] = fieldSub(f[start+2], t)
		f[start+2] = fieldAdd(f[start+2], t)

		t = fieldMontgomeryMul(zeta, f[start+7])
		f[start+7] = fieldSub(f[start+3], t)
		f[start+3] = fieldAdd(f[start+3], t)
	}
	for start := 0; start < 256; start += 4 {
		m++
		zeta := zetas[m]

		t := fieldMontgomeryMul(zeta, f[start+2])
		f[start+2] = fieldSub(f[start], t)
		f[start] = fieldAdd(f[start], t)

		t = fieldMontgomeryMul(zeta, f[start+3])
		f[start+3] = fieldSub(f[start+1], t)
		f[start+1] = fieldAdd(f[start+1], t)
	}
	for start := 0; start < 256; start += 2 {
		m++
		zeta := zetas[m]

		t := fieldMontgomeryMul(zeta, f[start+1])
		f[start+1] = fieldSub(f[start], t)
		f[start] = fieldAdd(f[start], t)
	}

	return nttElement(f)
}

// inverseNTT maps a nttElement back to the ringElement it represents.
//
// It implements NTT⁻¹, according to FIPS 203, Algorithm 10.
func inverseNTT(f nttElement) ringElement {
	var m uint8 = 255

	// Unroll len = 1, 2, and 4.
	for start := 0; start < 256; start += 2 {
		zeta := zetas[m]
		m--

		t := f[start]
		f[start] = fieldAdd(t, f[start+1])
		f[start+1] = fieldMontgomeryMulSub(zeta, f[start+1], t)
	}
	for start := 0; start < 256; start += 4 {
		zeta := zetas[m]
		m--

		t := f[start]
		f[start] = fieldAdd(t, f[start+2])
		f[start+2] = fieldMontgomeryMulSub(zeta, f[start+2], t)

		t = f[start+1]
		f[start+1] = fieldAdd(t, f[start+3])
		f[start+3] = fieldMontgomeryMulSub(zeta, f[start+3], t)
	}
	for start := 0; start < 256; start += 8 {
		zeta := zetas[m]
		m--

		t := f[start]
		f[start] = fieldAdd(t, f[start+4])
		f[start+4] = fieldMontgomeryMulSub(zeta, f[start+4], t)

		t = f[start+1]
		f[start+1] = fieldAdd(t, f[start+5])
		f[start+5] = fieldMontgomeryMulSub(zeta, f[start+5], t)

		t = f[start+2]
		f[start+2] = fieldAdd(t, f[start+6])
		f[start+6] = fieldMontgomeryMulSub(zeta, f[start+6], t)

		t = f[start+3]
		f[start+3] = fieldAdd(t, f[start+7])
		f[start+7] = fieldMontgomeryMulSub(zeta, f[start+7], t)
	}

	for len := 8; len < 256; len *= 2 {
		for start := 0; start < 256; start += 2 * len {
			zeta := zetas[m]
			m--

			// Bounds check elimination hint.
			f, flen := f[start:start+len], f[start+len:start+len+len]
			for j := 0; j < len; j += 2 {
				t := f[j]
				f[j] = fieldAdd(t, flen[j])
				// -z * (t - flen[j]) = z * (flen[j] - t)
				flen[j] = fieldMontgomeryMulSub(zeta, flen[j], t)

				// Unroll by 2 for performance.
				t = f[j+1]
				f[j+1] = fieldAdd(t, flen[j+1])
				flen[j+1] = fieldMontgomeryMulSub(zeta, flen[j+1], t)
			}
		}
	}

	for i := range f {
		f[i] = fieldMontgomeryMul(f[i], 16382) // 16382 = 256⁻¹ * R mod q
	}
	return ringElement(f)
}

// nttMul multiplies two nttElements.
func nttMul(a, b nttElement) (p nttElement) {
	for i := range p {
		p[i] = fieldMontgomeryMul(a[i], b[i])
	}
	return p
}

// sampleNTT samples an nttElement uniformly at random from the seed rho and the
// indices s and r. It implements Step 3 of ExpandA, RejNTTPoly, and
// CoeffFromThreeBytes from FIPS 204, passing in ρ, s, and r instead of ρ'.
func sampleNTT(rho []byte, s, r byte) nttElement {
	G := sha3.NewSHAKE128()
	G.Write(rho)
	G.Write([]byte{s, r})

	var a nttElement
	var j int         // index into a
	var buf [168]byte // buffered reads from B, matching the rate of SHAKE-128
	off := len(buf)   // index into buf, starts in a "buffer fully consumed" state
	for j < n {
		if off >= len(buf) {
			G.Read(buf[:])
			off = 0
		}
		v := uint32(buf[off]) | uint32(buf[off+1])<<8 | uint32(buf[off+2])<<16
		off += 3
		v &= 0b01111111_11111111_11111111 // 23 bits
		if v >= q {
			continue
		}
		a[j] = fieldMontgomeryMul(fieldElement(v), RR)
		j++
	}
	return a
}

// sampleBoundedPoly samples a ringElement with coefficients in [−η, η] from the
// seed rho and the index r. It implements RejBoundedPoly and CoeffFromHalfByte
// from FIPS 204, passing in ρ and r separately from ExpandS.
func sampleBoundedPoly(rho []byte, r byte, p parameters) ringElement {
	H := sha3.NewSHAKE256()
	H.Write(rho)
	H.Write([]byte{r, 0}) // IntegerToBytes(r, 2)

	var a ringElement
	var j int
	var buf [136]byte // buffered reads from H, matching the rate of SHAKE-256
	off := len(buf)   // index into buf, starts in a "buffer fully consumed" state
	for {
		if off >= len(buf) {
			H.Read(buf[:])
			off = 0
		}
		z0 := buf[off] & 0x0F
		z1 := buf[off] >> 4
		off++
		coeff, ok := coeffFromHalfByte(z0, p)
		if ok {
			a[j] = coeff
			j++
		}
		if j >= len(a) {
			break
		}
		coeff, ok = coeffFromHalfByte(z1, p)
		if ok {
			a[j] = coeff
			j++
		}
		if j >= len(a) {
			break
		}
	}
	return a
}

// coeffFromHalfByte implements CoeffFromHalfByte from FIPS 204.
//
// It maps a value in [0, 15] to a coefficient in [−η, η]
func coeffFromHalfByte(b byte, p parameters) (fieldElement, bool) {
	if b > 15 {
		panic(errInvariant)
	}
	switch p.η {
	case 2:
		// Return z = 2 − (b mod 5), which maps from
		//
		//     b = ( 14, 13, 12, 11, 10,  9,  8,  7,  6,  5,  4,  3,  2,  1,  0 )
		//
		// to
		//
		//   b%5 = (  4,  3,  2,  1,  0,  4,  3,  2,  1,  0,  4,  3,  2,  1,  0 )
		//
		// to
		//
		//     z = ( -2, -1,  0,  1,  2, -2, -1,  0,  1,  2, -2, -1,  0,  1,  2 )
		//
		if b > 14 {
			return 0, false
		}
		// Calculate b % 5 with Barrett reduction, to avoid a potentially
		// variable-time division.
		const barrettMultiplier = 0x3334 // ⌈2¹⁶ / 5⌉
		const barrettShift = 16          // log₂(2¹⁶)
		quotient := (uint32(b) * barrettMultiplier) >> barrettShift
		remainder := uint32(b) - quotient*5
		return fieldSubToMontgomery(2, remainder), true
	case 4:
		// Return z = 4 − b, which maps from
		//
		//   b = (  8,  7,  6,  5,  4,  3,  2,  1,  0 )
		//
		// to
		//
		//   z = ( −4, -3, -2, -1,  0,  1,  2,  3,  4 )
		//
		if b > 8 {
			return 0, false
		}
		return fieldSubToMontgomery(4, uint32(b)), true
	default:
		panic(errInvariant)
	}
}

// power2Round implements Power2Round from FIPS 204.
//
// It separates the bottom d = 13 bits of each 23-bit coefficient, rounding the
// high part based on the low part, and correcting the low part accordingly.
func power2Round(r fieldElement) (hi uint16, lo fieldElement) {
	rr := fieldFromMontgomery(r)
	// Add 2¹² - 1 to round up r1 by one if r0 > 2¹².
	// r is at most 2²³ - 2¹³ + 1, so rr + (2¹² - 1) won't overflow 23 bits.
	r1 := rr + 1<<12 - 1
	r1 >>= 13
	// r1 <= 2¹⁰ - 1
	// r1 * 2¹³ <= (2¹⁰ - 1) * 2¹³ = 2²³ - 2¹³ < q
	r0 := fieldSubToMontgomery(rr, r1<<13)
	return uint16(r1), r0
}

// sliceForAppend takes a slice and a requested number of bytes. It returns a
// slice with the contents of the given slice followed by that many bytes and a
// second slice that aliases into it and contains only the extra bytes. If the
// original slice has sufficient capacity then no allocation is performed.
func sliceForAppend(in []byte, n int) (head, tail []byte) {
	if total := len(in) + n; cap(in) >= total {
		head = in[:total]
	} else {
		head = make([]byte, total)
		copy(head, in)
	}
	tail = head[len(in):]
	return
}

type parameters struct {
	k, l int // dimensions of A
	η    int // bound for secret coefficients
	γ1   int // log₂(γ₁), where [-γ₁+1, γ₁] is the bound of y
	γ2   int // denominator of γ₂ = (q - 1) / γ2
	λ    int // collison strength
	τ    int // number of non-zero coefficients in challenge
	ω    int // max number of hints in MakeHint
}

var (
	params44 = parameters{k: 4, l: 4, η: 2, γ1: 17, γ2: 88, λ: 128, τ: 39, ω: 80}
	params65 = parameters{k: 6, l: 5, η: 4, γ1: 19, γ2: 32, λ: 192, τ: 49, ω: 55}
	params87 = parameters{k: 8, l: 7, η: 2, γ1: 19, γ2: 32, λ: 256, τ: 60, ω: 75}
)

func pubKeySize(p parameters) int {
	// ρ + k × n × 10-bit coefficients of t₁
	return 32 + p.k*n*10/8
}

const maxK, maxL = 8, 7
const maxPubKeySize = 32 + 8*n*10/8

type privateKey struct {
	p   parameters
	raw [maxPubKeySize]byte // public key
	tr  [64]byte            // public key hash
	a   [maxK * maxL]nttElement
	s1  [maxL]nttElement
	s2  [maxK]nttElement
	t0  [maxK]nttElement
	k   [32]byte
}

func newPrivateKey(seed *[32]byte, p parameters) *privateKey {
	k, l := p.k, p.l

	priv := &privateKey{p: p}

	ξ := sha3.NewSHAKE256()
	ξ.Write(seed[:])
	ξ.Write([]byte{byte(k), byte(l)})
	ρ, ρs := make([]byte, 32), make([]byte, 64)
	ξ.Read(ρ)
	ξ.Read(ρs)
	ξ.Read(priv.k[:])

	A := priv.a[:k*l]
	computeMatrixA(A, ρ, p)

	s1 := priv.s1[:l]
	for r := range l {
		s1[r] = ntt(sampleBoundedPoly(ρs, byte(r), p))
	}
	s2 := priv.s2[:k]
	for r := range k {
		s2[r] = ntt(sampleBoundedPoly(ρs, byte(l+r), p))
	}

	// ˆt = Â ∘ ŝ₁ + ŝ₂
	tHat := make([]nttElement, k, maxK)
	for i := range tHat {
		tHat[i] = s2[i]
		for j := range s1 {
			tHat[i] = polyAdd(tHat[i], nttMul(A[i*l+j], s1[j]))
		}
	}
	// t = NTT⁻¹(ˆt)
	t := make([]ringElement, k, maxK)
	for i := range tHat {
		t[i] = inverseNTT(tHat[i])
	}
	// (t₁, _) = Power2Round(t)
	// (_, ˆt₀) = NTT(Power2Round(t))
	t1, t0 := make([][n]uint16, k, maxK), priv.t0[:k]
	for i := range t {
		var w ringElement
		for j := range t[i] {
			t1[i][j], w[j] = power2Round(t[i][j])
		}
		t0[i] = ntt(w)
	}

	pk := pkEncode(priv.raw[:0], ρ, t1, p)
	priv.tr = computePublicKeyHash(pk)

	return priv
}

func computeMatrixA(A []nttElement, ρ []byte, p parameters) {
	k, l := p.k, p.l
	for r := range k {
		for s := range l {
			A[r*l+s] = sampleNTT(ρ, byte(s), byte(r))
		}
	}
}

func computePublicKeyHash(pk []byte) [64]byte {
	H := sha3.NewSHAKE256()
	H.Write(pk)
	var tr [64]byte
	H.Read(tr[:])
	return tr
}

func pkEncode(buf []byte, ρ []byte, t1 [][n]uint16, p parameters) []byte {
	pk := append(buf, ρ...)
	for _, w := range t1[:p.k] {
		// Encode four at a time into 4 * 10 bits = 5 bytes.
		for i := 0; i < n; i += 4 {
			c0 := w[i]
			c1 := w[i+1]
			c2 := w[i+2]
			c3 := w[i+3]
			b0 := byte(c0 >> 0)
			b1 := byte((c0 >> 8) | (c1 << 2))
			b2 := byte((c1 >> 6) | (c2 << 4))
			b3 := byte((c2 >> 4) | (c3 << 6))
			b4 := byte(c3 >> 2)
			pk = append(pk, b0, b1, b2, b3, b4)
		}
	}
	return pk
}

func semiExpandedPrivKeySize(p parameters) int {
	k, l := p.k, p.l
	ηBitlen := bits.Len(uint(p.η)) + 1
	// ρ + K + tr + l × n × η-bit coefficients of s₁ +
	// k × n × η-bit coefficients of s₂ + k × n × 13-bit coefficients of t₀
	return 32 + 32 + 64 + l*n*ηBitlen/8 + k*n*ηBitlen/8 + k*n*13/8
}

func skDecode(sk []byte, p parameters) (ρ, K [32]byte, tr [64]byte, s1, s2, t0 []ringElement, ok bool) {
	k, l, η := p.k, p.l, p.η
	copy(ρ[:], sk[:32])
	sk = sk[32:]
	copy(K[:], sk[:32])
	sk = sk[32:]
	copy(tr[:], sk[:64])
	sk = sk[64:]

	s1 = make([]ringElement, l)
	for i := range l {
		length := n * bits.Len(uint(η)*2) / 8
		if s1[i], ok = bitUnpackSlow(sk[:length], η, η); !ok {
			return
		}
		sk = sk[length:]
	}

	s2 = make([]ringElement, k)
	for i := range k {
		length := n * bits.Len(uint(η)*2) / 8
		if s2[i], ok = bitUnpackSlow(sk[:length], η, η); !ok {
			return
		}
		sk = sk[length:]
	}

	const bound = 1 << (13 - 1) // 2^(d-1)
	t0 = make([]ringElement, k)
	for i := range k {
		length := n * 13 / 8
		if t0[i], ok = bitUnpackSlow(sk[:length], bound-1, bound); !ok {
			return
		}
		sk = sk[length:]
	}

	return ρ, K, tr, s1, s2, t0, true
}

func bitPackSlow(buf []byte, r ringElement, a, b int) []byte {
	bitlen := bits.Len(uint(a + b))
	if bitlen <= 0 || bitlen > 16 {
		panic(errInvariant)
	}
	out, v := sliceForAppend(buf, n*bitlen/8)
	var acc uint32
	var accBits uint
	for i := range r {
		w := int32(b) - fieldCenteredMod(r[i])
		acc |= uint32(w) << accBits
		accBits += uint(bitlen)
		for accBits >= 8 {
			v[0] = byte(acc)
			v = v[1:]
			acc >>= 8
			accBits -= 8
		}
	}
	if accBits > 0 {
		v[0] = byte(acc)
	}
	return out
}

func bitUnpackSlow(v []byte, a, b int) (ringElement, bool) {
	bitlen := bits.Len(uint(a + b))
	if bitlen <= 0 || bitlen > 16 {
		panic(errInvariant)
	}

	mask := uint32((1 << bitlen) - 1)
	maxValue := uint32(a + b)

	var r ringElement
	var acc uint32
	var accBits uint
	vIdx := 0

	for i := range r {
		for accBits < uint(bitlen) {
			if vIdx < len(v) {
				acc |= uint32(v[vIdx]) << accBits
				vIdx++
				accBits += 8
			}
		}
		w := acc & mask
		if w > maxValue {
			return ringElement{}, false
		}
		r[i] = fieldSubToMontgomery(uint32(b), w)
		acc >>= bitlen
		accBits -= uint(bitlen)
	}

	return r, true
}

// checkSemiExpanded reports how ossl_ml_dsa_sk_decode ends for an sk of
// parameter set p: 0 when it loads, 1 when a coefficient is out of range
// (decoding fails without an error), 2 when the recomputed t0 or public key
// hash disagree (OpenSSL's "private key does not match its pubkey part").
func checkSemiExpanded(sk []byte, p parameters) int {
	k, l := p.k, p.l
	ρ, _, tr, s1, s2, t0, ok := skDecode(sk, p)
	if !ok {
		return 1
	}
	var a [maxK * maxL]nttElement
	A := a[:k*l]
	computeMatrixA(A, ρ[:], p)
	var s1Hat [maxL]nttElement
	for r := range l {
		s1Hat[r] = ntt(s1[r])
	}
	t1 := make([][n]uint16, k, maxK)
	for i := range k {
		tHat := ntt(s2[i])
		for j := range l {
			tHat = polyAdd(tHat, nttMul(A[i*l+j], s1Hat[j]))
		}
		t := inverseNTT(tHat)
		for j := range n {
			r1, r0 := power2Round(t[j])
			t1[i][j] = r1
			if r0 != t0[i][j] {
				return 2
			}
		}
	}
	var raw [maxPubKeySize]byte
	if computePublicKeyHash(pkEncode(raw[:0], ρ[:], t1, p)) != tr {
		return 2
	}
	return 0
}

func semiExpandedBytes(priv *privateKey) []byte {
	k, l, η := priv.p.k, priv.p.l, priv.p.η
	sk := make([]byte, 0, semiExpandedPrivKeySize(priv.p))
	sk = append(sk, priv.raw[:32]...) // ρ
	sk = append(sk, priv.k[:]...)     // K
	sk = append(sk, priv.tr[:]...)    // tr
	for i := range l {
		sk = bitPackSlow(sk, inverseNTT(priv.s1[i]), η, η)
	}
	for i := range k {
		sk = bitPackSlow(sk, inverseNTT(priv.s2[i]), η, η)
	}
	const bound = 1 << (13 - 1) // 2^(d-1)
	for i := range k {
		sk = bitPackSlow(sk, inverseNTT(priv.t0[i]), bound-1, bound)
	}
	return sk
}

// errInvariant is raised on an internal invariant that the callers'
// fixed parameters rule out.
var errInvariant = invariantError{}

type invariantError struct{}

func (invariantError) Error() string { return "mldsa invariant" }

// SemiExpandedFromSeed is the semi-expanded private key encoding OpenSSL's
// keygen produces from seed for the parameter set with that private key
// length.
func SemiExpandedFromSeed(seed []byte, privLen int) []byte {
	p, _ := params(privLen)
	return semiExpandedBytes(newPrivateKey((*[32]byte)(seed), p))
}

// CheckSemiExpanded is checkSemiExpanded for the parameter set with sk's
// length (1 when there is none).
func CheckSemiExpanded(sk []byte) int {
	p, ok := params(len(sk))
	if !ok {
		return 1
	}
	return checkSemiExpanded(sk, p)
}

func params(privLen int) (parameters, bool) {
	for _, p := range []parameters{params44, params65, params87} {
		if semiExpandedPrivKeySize(p) == privLen {
			return p, true
		}
	}
	return parameters{}, false
}
