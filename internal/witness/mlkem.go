// The ML-KEM arithmetic below is adapted from Go's
// crypto/internal/fips140/mlkem (Copyright 2023 The Go Authors, BSD-style
// license), generalised to the three parameter sets OpenSSL decodes.

package witness

import (
	"bytes"
	"crypto/sha3"
	"encoding/binary"
)

// ML-KEM (FIPS 203) as OpenSSL 3.5 loads a decoded private key
// (providers/implementations/keymgmt/ml_kem_kmgmt.c ml_kem_load,
// crypto/ml_kem/ml_kem.c): only what decides whether the key loads.

const (
	kemN = 256
	kemQ = 3329
)

type kemField uint16

func kemReduceOnce(a uint16) kemField {
	x := a - kemQ
	x += (x >> 15) * kemQ
	return kemField(x)
}

func kemAdd(a, b kemField) kemField { return kemReduceOnce(uint16(a + b)) }

func kemSub(a, b kemField) kemField { return kemReduceOnce(uint16(a - b + kemQ)) }

func kemReduce(a uint32) kemField {
	quotient := uint32((uint64(a) * 5039) >> 24)
	return kemReduceOnce(uint16(a - quotient*kemQ))
}

func kemMul(a, b kemField) kemField { return kemReduce(uint32(a) * uint32(b)) }

func kemAddMul(a, b, c, d kemField) kemField {
	return kemReduce(uint32(a)*uint32(b) + uint32(c)*uint32(d))
}

type kemPoly [kemN]kemField

var kemGammas = [128]kemField{17, 3312, 2761, 568, 583, 2746, 2649, 680, 1637, 1692, 723, 2606, 2288, 1041, 1100, 2229, 1409, 1920, 2662, 667, 3281, 48, 233, 3096, 756, 2573, 2156, 1173, 3015, 314, 3050, 279, 1703, 1626, 1651, 1678, 2789, 540, 1789, 1540, 1847, 1482, 952, 2377, 1461, 1868, 2687, 642, 939, 2390, 2308, 1021, 2437, 892, 2388, 941, 733, 2596, 2337, 992, 268, 3061, 641, 2688, 1584, 1745, 2298, 1031, 2037, 1292, 3220, 109, 375, 2954, 2549, 780, 2090, 1239, 1645, 1684, 1063, 2266, 319, 3010, 2773, 556, 757, 2572, 2099, 1230, 561, 2768, 2466, 863, 2594, 735, 2804, 525, 1092, 2237, 403, 2926, 1026, 2303, 1143, 2186, 2150, 1179, 2775, 554, 886, 2443, 1722, 1607, 1212, 2117, 1874, 1455, 1029, 2300, 2110, 1219, 2935, 394, 885, 2444, 2154, 1175}

var kemZetas = [128]kemField{1, 1729, 2580, 3289, 2642, 630, 1897, 848, 1062, 1919, 193, 797, 2786, 3260, 569, 1746, 296, 2447, 1339, 1476, 3046, 56, 2240, 1333, 1426, 2094, 535, 2882, 2393, 2879, 1974, 821, 289, 331, 3253, 1756, 1197, 2304, 2277, 2055, 650, 1977, 2513, 632, 2865, 33, 1320, 1915, 2319, 1435, 807, 452, 1438, 2868, 1534, 2402, 2647, 2617, 1481, 648, 2474, 3110, 1227, 910, 17, 2761, 583, 2649, 1637, 723, 2288, 1100, 1409, 2662, 3281, 233, 756, 2156, 3015, 3050, 1703, 1651, 2789, 1789, 1847, 952, 1461, 2687, 939, 2308, 2437, 2388, 733, 2337, 268, 641, 1584, 2298, 2037, 3220, 375, 2549, 2090, 1645, 1063, 319, 2773, 757, 2099, 561, 2466, 2594, 2804, 1092, 403, 1026, 1143, 2150, 2775, 886, 1722, 1212, 1874, 1029, 2110, 2935, 885, 2154}

func kemNTTMul(f, g kemPoly) kemPoly {
	var h kemPoly
	for i := 0; i < 256; i += 2 {
		a0, a1 := f[i], f[i+1]
		b0, b1 := g[i], g[i+1]
		h[i] = kemAddMul(a0, b0, kemMul(a1, b1), kemGammas[i/2])
		h[i+1] = kemAddMul(a0, b1, a1, b0)
	}
	return h
}

func kemNTT(f kemPoly) kemPoly {
	k := 1
	for l := 128; l >= 2; l /= 2 {
		for start := 0; start < 256; start += 2 * l {
			zeta := kemZetas[k]
			k++
			for j := 0; j < l; j++ {
				t := kemMul(zeta, f[start+l+j])
				f[start+l+j] = kemSub(f[start+j], t)
				f[start+j] = kemAdd(f[start+j], t)
			}
		}
	}
	return f
}

func kemPolyAdd(a, b kemPoly) (s kemPoly) {
	for i := range s {
		s[i] = kemAdd(a[i], b[i])
	}
	return s
}

// kemSampleNTT is FIPS 203 Algorithm 7 (SampleNTT).
func kemSampleNTT(rho []byte, ii, jj byte) kemPoly {
	B := sha3.NewSHAKE128()
	B.Write(rho)
	B.Write([]byte{ii, jj})
	var a kemPoly
	j := 0
	var buf [24]byte
	off := len(buf)
	for {
		if off >= len(buf) {
			B.Read(buf[:])
			off = 0
		}
		d1 := binary.LittleEndian.Uint16(buf[off:]) & 0xFFF
		d2 := binary.LittleEndian.Uint16(buf[off+1:]) >> 4
		off += 3
		if d1 < kemQ {
			a[j] = kemField(d1)
			j++
		}
		if j >= len(a) {
			break
		}
		if d2 < kemQ {
			a[j] = kemField(d2)
			j++
		}
		if j >= len(a) {
			break
		}
	}
	return a
}

// kemSampleCBD is SamplePolyCBD_η(PRF_η(s, b)) for η = 2 or 3.
func kemSampleCBD(s []byte, b byte, eta int) kemPoly {
	prf := sha3.NewSHAKE256()
	prf.Write(s)
	prf.Write([]byte{b})
	B := make([]byte, 64*eta)
	prf.Read(B)
	var f kemPoly
	bit := func(i int) kemField { return kemField(B[i/8] >> (i % 8) & 1) }
	for i := 0; i < kemN; i++ {
		var x, y kemField
		for j := 0; j < eta; j++ {
			x += bit(2*i*eta + j)
			y += bit(2*i*eta + eta + j)
		}
		f[i] = kemSub(x, y)
	}
	return f
}

func kemEncode12(b []byte, f kemPoly) []byte {
	for i := 0; i < kemN; i += 2 {
		x := uint32(f[i]) | uint32(f[i+1])<<12
		b = append(b, byte(x), byte(x>>8), byte(x>>16))
	}
	return b
}

// kemDecode12 is OpenSSL's scalar_decode_12: false if a coefficient >= q.
func kemDecode12(b []byte) (kemPoly, bool) {
	var f kemPoly
	for i := 0; i < kemN; i += 2 {
		b1, b2, b3 := uint16(b[3*i/2]), uint16(b[3*i/2+1]), uint16(b[3*i/2+2])
		f[i] = kemField(b1 | (b2&0x0F)<<8)
		f[i+1] = kemField(b2>>4 | b3<<4)
		if f[i] >= kemQ || f[i+1] >= kemQ {
			return f, false
		}
	}
	return f, true
}

// kemKeyGen is ML-KEM.KeyGen_internal(d, z): the expanded decapsulation key
// encoding (s || ek || H(ek) || z) OpenSSL compares with an explicit one.
func kemKeyGen(rank int, d, z []byte) []byte {
	eta1 := 2
	if rank == 2 {
		eta1 = 3
	}
	g := sha3.New512()
	g.Write(d)
	g.Write([]byte{byte(rank)})
	G := g.Sum(nil)
	rho, sigma := G[:32], G[32:]
	A := make([]kemPoly, rank*rank)
	for i := 0; i < rank; i++ {
		for j := 0; j < rank; j++ {
			A[i*rank+j] = kemSampleNTT(rho, byte(j), byte(i))
		}
	}
	var N byte
	s := make([]kemPoly, rank)
	for i := range s {
		s[i] = kemNTT(kemSampleCBD(sigma, N, eta1))
		N++
	}
	e := make([]kemPoly, rank)
	for i := range e {
		e[i] = kemNTT(kemSampleCBD(sigma, N, eta1))
		N++
	}
	ek := []byte{}
	for i := 0; i < rank; i++ {
		t := e[i]
		for j := 0; j < rank; j++ {
			t = kemPolyAdd(t, kemNTTMul(A[i*rank+j], s[j]))
		}
		ek = kemEncode12(ek, t)
	}
	ek = append(ek, rho...)
	h := sha3.Sum256(ek)
	dk := []byte{}
	for i := range s {
		dk = kemEncode12(dk, s[i])
	}
	dk = append(dk, ek...)
	dk = append(dk, h[:]...)
	return append(dk, z...)
}

// kemParsePrivate is ml_kem.c parse_prvkey: the checks that can fail
// (coefficients of s and t below q, the stored H(ek)).
func kemParsePrivate(rank int, dk []byte) bool {
	vec := 384 * rank
	for i := 0; i < rank; i++ {
		if _, ok := kemDecode12(dk[384*i : 384*(i+1)]); !ok {
			return false
		}
	}
	ek := dk[vec : vec+vec+32]
	if !mlkemPubValid(ek) {
		return false
	}
	h := sha3.Sum256(ek)
	return bytes.Equal(h[:], dk[vec+vec+32:vec+vec+64])
}

// mlkemLoad ports ml_kem_load for a decoded PKCS#8 key: with a seed the
// key is generated from it (after the seed's z is checked against an
// explicit key's), and an explicit key must then match; without a seed the
// explicit key is parsed and must pass the pairwise test.
func (e *osslEnv) mlkemLoad(pp *pqParams, k *pqPending) bool {
	rank := (pp.pubLen - 32) / 384
	if k.seed != nil {
		if k.priv != nil && !bytes.Equal(k.seed[32:], k.priv[len(k.priv)-32:]) {
			e.q.raise(libPROV, "PROV_R_INVALID_KEY")
			return false
		}
		if k.priv != nil && !bytes.Equal(kemKeyGen(rank, k.seed[:32], k.seed[32:]), k.priv) {
			e.q.raise(libPROV, "PROV_R_INVALID_KEY")
			return false
		}
		return true
	}
	if k.priv != nil && !kemParsePrivate(rank, k.priv) {
		e.q.raise(libPROV, "PROV_R_INVALID_KEY")
		e.q.raise(libPROV, "PROV_R_INVALID_KEY")
		return false
	}
	if k.priv != nil && !kemPairwise(rank, k.priv) {
		e.q.raise(libPROV, "PROV_R_INVALID_KEY") // ml_kem_pairwise_test
		return false
	}
	return true
}

// kemInvNTT is FIPS 203 Algorithm 10 (NTT⁻¹).
func kemInvNTT(f kemPoly) kemPoly {
	k := 127
	for l := 2; l <= 128; l *= 2 {
		for start := 0; start < 256; start += 2 * l {
			zeta := kemZetas[k]
			k--
			for j := start; j < start+l; j++ {
				t := f[j]
				f[j] = kemAdd(t, f[j+l])
				f[j+l] = kemMul(zeta, kemSub(f[j+l], t))
			}
		}
	}
	for i := range f {
		f[i] = kemMul(f[i], 3303) // 128⁻¹ mod q
	}
	return f
}

func kemCompress(x kemField, d uint) uint16 {
	return uint16(((uint32(x)<<(d+1) + kemQ) / (2 * kemQ)) & (1<<d - 1))
}

func kemDecompress(y uint16, d uint) kemField {
	return kemField((uint32(y)*kemQ*2 + 1<<d) >> (d + 1))
}

// kemEncodeD is ByteEncode_d∘Compress_d of f.
func kemEncodeD(b []byte, f kemPoly, d uint) []byte {
	var acc uint32
	var bits uint
	for _, x := range f {
		acc |= uint32(kemCompress(x, d)) << bits
		bits += d
		for bits >= 8 {
			b = append(b, byte(acc))
			acc >>= 8
			bits -= 8
		}
	}
	return b
}

// kemDecodeD is Decompress_d∘ByteDecode_d of b (32·d bytes).
func kemDecodeD(b []byte, d uint) kemPoly {
	var f kemPoly
	var acc uint32
	var bits uint
	i := 0
	for j := range f {
		for bits < d {
			acc |= uint32(b[i]) << bits
			i++
			bits += 8
		}
		f[j] = kemDecompress(uint16(acc&(1<<d-1)), d)
		acc >>= d
		bits -= d
	}
	return f
}

func kemCompressBits(rank int) (du, dv uint) {
	if rank == 4 {
		return 11, 5
	}
	return 10, 4
}

// kemEncrypt is K-PKE.Encrypt(ek, m, r) (FIPS 203 Algorithm 14).
func kemEncrypt(rank int, ek, m, r []byte) []byte {
	eta1 := 2
	if rank == 2 {
		eta1 = 3
	}
	du, dv := kemCompressBits(rank)
	that := make([]kemPoly, rank)
	for i := range that {
		that[i], _ = kemDecode12(ek[384*i : 384*(i+1)])
	}
	rho := ek[384*rank:]
	var N byte
	yhat := make([]kemPoly, rank)
	for i := range yhat {
		yhat[i] = kemNTT(kemSampleCBD(r, N, eta1))
		N++
	}
	e1 := make([]kemPoly, rank)
	for i := range e1 {
		e1[i] = kemSampleCBD(r, N, 2)
		N++
	}
	e2 := kemSampleCBD(r, N, 2)
	var c []byte
	for i := 0; i < rank; i++ {
		var acc kemPoly
		for j := 0; j < rank; j++ {
			acc = kemPolyAdd(acc, kemNTTMul(kemSampleNTT(rho, byte(i), byte(j)), yhat[j])) // Âᵀ[i][j] = Â[j][i]
		}
		c = kemEncodeD(c, kemPolyAdd(kemInvNTT(acc), e1[i]), du)
	}
	var acc kemPoly
	for j := 0; j < rank; j++ {
		acc = kemPolyAdd(acc, kemNTTMul(that[j], yhat[j]))
	}
	v := kemPolyAdd(kemInvNTT(acc), e2)
	for i := range v {
		if m[i/8]>>(i%8)&1 == 1 {
			v[i] = kemAdd(v[i], (kemQ+1)/2)
		}
	}
	return kemEncodeD(c, v, dv)
}

// kemDecrypt is K-PKE.Decrypt(dk_pke, c) (FIPS 203 Algorithm 15).
func kemDecrypt(rank int, dkPKE, c []byte) []byte {
	du, dv := kemCompressBits(rank)
	var acc kemPoly
	for i := 0; i < rank; i++ {
		u := kemDecodeD(c[32*int(du)*i:], du)
		s, _ := kemDecode12(dkPKE[384*i : 384*(i+1)])
		acc = kemPolyAdd(acc, kemNTTMul(s, kemNTT(u)))
	}
	v := kemDecodeD(c[32*int(du)*rank:], dv)
	w := kemInvNTT(acc)
	m := make([]byte, 32)
	for i := range v {
		if kemCompress(kemSub(v[i], w[i]), 1) == 1 {
			m[i/8] |= 1 << (i % 8)
		}
	}
	return m
}

// kemPairwise ports ml_kem_kmgmt.c ml_kem_pairwise_test for a parsed
// expanded key dk: encapsulate to its public key, decapsulate with its
// private key (implicit rejection on a mismatch) and compare the secrets.
// OpenSSL's default provider uses random entropy here; for a consistent
// key the test passes for every entropy, and for a key whose private
// vector does not match the public key it fails for all but a negligible
// fraction, so a fixed message (OpenSSL's FIXED_PCT value) decides it.
func kemPairwise(rank int, dk []byte) bool {
	vec := 384 * rank
	ek := dk[vec : 2*vec+32]
	h, z := dk[2*vec+32:2*vec+64], dk[2*vec+64:]
	m := bytes.Repeat([]byte{0x55}, 32)
	g := sha3.New512()
	g.Write(m)
	g.Write(h)
	kr := g.Sum(nil)
	c := kemEncrypt(rank, ek, m, kr[32:])
	// Decapsulation.
	m2 := kemDecrypt(rank, dk[:vec], c)
	g.Reset()
	g.Write(m2)
	g.Write(h)
	kr2 := g.Sum(nil)
	secret := kr2[:32]
	if !bytes.Equal(kemEncrypt(rank, ek, m2, kr2[32:]), c) {
		j := sha3.NewSHAKE256()
		j.Write(z)
		j.Write(c)
		secret = make([]byte, 32)
		j.Read(secret)
	}
	return bytes.Equal(secret, kr[:32])
}
