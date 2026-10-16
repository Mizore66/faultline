package witness

import (
	"bytes"
	"encoding/hex"

	"github.com/Mizore66/faultline/internal/witness/mldsa"
)

// The post-quantum key decoders of OpenSSL 3.5's default provider:
// ML-DSA and ML-KEM (providers/implementations/encode_decode/ml_dsa_codecs.c,
// ml_kem_codecs.c, ml_common_codecs.c) and SLH-DSA (decode_der2key.c).

type pqFamily int

const (
	pqMLDSA pqFamily = iota
	pqMLKEM
	pqSLHDSA
)

// pqParams are a PQ algorithm's sizes and its SPKI prefix (the 22 bytes
// before the key for ML-*).
type pqParams struct {
	family     pqFamily
	oid        []byte
	pubLen     int
	privLen    int
	seedLen    int
	spkiPrefix []byte
	p8         []pkcs8Format
}

// pkcs8Format is ML_COMMON_PKCS8_FMT: the accepted PKCS#8 private key
// layouts, in the default (compile-time) order.
type pkcs8Format struct {
	name                   string
	p8Bytes, p8Shift       int
	p8Magic                uint32
	seedMagic              uint16
	seedOffset, seedLength int
	privMagic              uint32
	privOffset, privLength int
	pubOffset, pubLength   int
}

// pqPending is the decoded, not yet loaded PQ private key.
type pqPending struct {
	seed, priv []byte
}

// mlFormats is ml_*_codecs.c's ML_COMMON_PKCS8_FMT table for the sizes.
func mlFormats(seedLen, privLen, pubLen int) []pkcs8Format {
	s4 := func(n int) uint32 { return 0x04820000 | uint32(n) }
	seedPriv := 4 + 2 + seedLen + 4 + privLen
	return []pkcs8Format{
		{"seed-priv", seedPriv, 0, 0x30820000 | uint32(seedPriv-4), 0x0400 | uint16(seedLen), 6, seedLen, s4(privLen), 6 + seedLen + 4, privLen, 0, 0},
		{"priv-only", 4 + privLen, 0, s4(privLen), 0, 0, 0, 0, 4, privLen, 0, 0},
		{"oqskeypair", 4 + privLen + pubLen, 0, s4(privLen + pubLen), 0, 0, 0, 0, 4, privLen, 4 + privLen, pubLen},
		{"seed-only", 2 + seedLen, 2, 0x8000 | uint32(seedLen), 0, 2, seedLen, 0, 0, 0, 0, 0},
		{"bare-priv", privLen, 4, 0, 0, 0, 0, 0, 0, privLen, 0, 0},
		{"bare-seed", seedLen, 4, 0, 0, 0, seedLen, 0, 0, 0, 0, 0},
	}
}

func mlPrefix(oidLastByte byte, family byte, pubLen int) []byte {
	total := pubLen + 22 - 4
	b := []byte{0x30, 0x82, byte(total >> 8), byte(total), 0x30, 0x0b, 0x06, 0x09,
		0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, family, oidLastByte,
		0x03, 0x82, byte((pubLen + 1) >> 8), byte(pubLen + 1), 0x00}
	return b
}

func newMLDSA(name string, last byte, priv, pub int) *keyDesc {
	return &keyDesc{name: name, names: []string{name, "MLDSA" + name[7:], "2.16.840.1.101.3.4.3." + itoa(int(last)), "id-ml-dsa-" + name[7:]},
		pqParams: &pqParams{family: pqMLDSA, oid: []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x03, last},
			pubLen: pub, privLen: priv, seedLen: 32, spkiPrefix: mlPrefix(last, 0x03, pub), p8: mlFormats(32, priv, pub)}}
}

func newMLKEM(name string, last byte, priv, pub int) *keyDesc {
	return &keyDesc{name: name, names: []string{name, "MLKEM" + name[7:], "id-alg-ml-kem-" + name[7:], "2.16.840.1.101.3.4.4." + itoa(int(last))},
		pqParams: &pqParams{family: pqMLKEM, oid: []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x04, last},
			pubLen: pub, privLen: priv, seedLen: 64, spkiPrefix: mlPrefix(last, 0x04, pub), p8: mlFormats(64, priv, pub)}}
}

func newSLH(name string, last byte, n int) *keyDesc {
	return &keyDesc{name: name, names: []string{name, "id-" + asciiLower(name), "2.16.840.1.101.3.4.3." + itoa(int(last))},
		pqParams: &pqParams{family: pqSLHDSA, oid: []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x03, last},
			pubLen: 2 * n, privLen: 4 * n}}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ML-KEM sizes: pub = 384*rank+32, priv = 768*rank+96.
var pqDescs = []*keyDesc{
	newMLDSA("ML-DSA-44", 0x11, 2560, 1312),
	newMLDSA("ML-DSA-65", 0x12, 4032, 1952),
	newMLDSA("ML-DSA-87", 0x13, 4896, 2592),
	newMLKEM("ML-KEM-512", 0x01, 1632, 800),
	newMLKEM("ML-KEM-768", 0x02, 2400, 1184),
	newMLKEM("ML-KEM-1024", 0x03, 3168, 1568),
	newSLH("SLH-DSA-SHA2-128s", 0x14, 16),
	newSLH("SLH-DSA-SHA2-128f", 0x15, 16),
	newSLH("SLH-DSA-SHA2-192s", 0x16, 24),
	newSLH("SLH-DSA-SHA2-192f", 0x17, 24),
	newSLH("SLH-DSA-SHA2-256s", 0x18, 32),
	newSLH("SLH-DSA-SHA2-256f", 0x19, 32),
	newSLH("SLH-DSA-SHAKE-128s", 0x1a, 16),
	newSLH("SLH-DSA-SHAKE-128f", 0x1b, 16),
	newSLH("SLH-DSA-SHAKE-192s", 0x1c, 24),
	newSLH("SLH-DSA-SHAKE-192f", 0x1d, 24),
	newSLH("SLH-DSA-SHAKE-256s", 0x1e, 32),
	newSLH("SLH-DSA-SHAKE-256f", 0x1f, 32),
}

// allDescs lists every der2key algorithm in decoders.inc order.
func allDescs() []*keyDesc { return append(append([]*keyDesc{}, keyDescs...), pqDescs...) }

// pqD2IPUBKEY ports the ML-DSA/ML-KEM d2i_PUBKEY (exact length and prefix,
// then the public key decode) and slh_dsa_d2i_PUBKEY.
func (e *osslEnv) pqD2IPUBKEY(desc *keyDesc, der []byte) *decodedKey {
	pp := desc.pqParams
	switch pp.family {
	case pqMLDSA, pqMLKEM:
		if len(der) != 22+pp.pubLen || !bytes.Equal(der[:22], pp.spkiPrefix) {
			return nil
		}
		if pp.family == pqMLKEM && !mlkemPubValid(der[22:]) {
			e.q.raise(libPROV, "PROV_R_INVALID_KEY")
			e.q.raise(libPROV, "PROV_R_BAD_ENCODING")
			return nil
		}
		return &decodedKey{desc: desc}
	}
	if len(der) != 18+pp.pubLen {
		e.q.raise(libPROV, "PROV_R_BAD_ENCODING")
		return nil
	}
	v, n, ok := e.d2i(barePubkeyItem, der)
	if !ok {
		e.q.raise(libPROV, "PROV_R_BAD_ENCODING")
		return nil
	}
	if v.fields[1].bits != 0 || n != len(der) {
		e.q.raise(libPROV, "PROV_R_BAD_ENCODING")
		return nil
	}
	if !bytes.Equal(v.fields[0].fields[0].data, pp.oid) {
		e.q.raise(libPROV, "PROV_R_BAD_ENCODING")
		return nil
	}
	if len(v.fields[1].data) != pp.pubLen {
		e.q.raise(libPROV, "PROV_R_BAD_ENCODING")
		return nil
	}
	return &decodedKey{desc: desc}
}

// pqD2IPKCS8 ports ossl_ml_dsa_d2i_PKCS8 / ossl_ml_kem_d2i_PKCS8 and
// slh_dsa_d2i_PKCS8.
func (e *osslEnv) pqD2IPKCS8(desc *keyDesc, der []byte) *decodedKey {
	pp := desc.pqParams
	p8, _, ok := e.d2i(pkcs8Item, der)
	if !ok {
		return nil
	}
	alg := p8.fields[1]
	buf := p8.fields[2].data
	if pp.family == pqSLHDSA {
		if alg.fields[1] != nil {
			e.q.raise(libPROV, "PROV_R_UNEXPECTED_KEY_PARAMETERS")
			return nil
		}
		if !bytes.Equal(alg.fields[0].data, pp.oid) || len(buf) != pp.privLen {
			return nil
		}
		return &decodedKey{desc: desc, private: true}
	}
	if !bytes.Equal(alg.fields[0].data, pp.oid) {
		return nil
	}
	if alg.fields[1] != nil {
		e.q.raise(libPROV, "PROV_R_UNEXPECTED_KEY_PARAMETERS")
		return nil
	}
	if len(buf) < 4 {
		return nil
	}
	magic := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
	var f *pkcs8Format
	pos := 4
	for i := range pp.p8 {
		c := &pp.p8[i]
		if len(buf) != c.p8Bytes {
			continue
		}
		if c.p8Shift == 4 || magic>>(uint(c.p8Shift)*8) == c.p8Magic {
			pos -= c.p8Shift
			f = c
			break
		}
	}
	if f == nil {
		if pp.family == pqMLDSA {
			e.q.raise(libPROV, "PROV_R_ML_DSA_NO_FORMAT")
		} else {
			e.q.raise(libPROV, "PROV_R_ML_KEM_NO_FORMAT")
		}
		return nil
	}
	k := &pqPending{}
	if f.seedLength > 0 {
		if pos+2 == f.seedOffset {
			if uint16(buf[pos])<<8|uint16(buf[pos+1]) != f.seedMagic {
				return nil
			}
			pos += 2
		} else if pos != f.seedOffset {
			return nil
		}
		k.seed = buf[f.seedOffset : f.seedOffset+f.seedLength]
		pos += f.seedLength
	}
	if f.privLength > 0 {
		if pos+4 == f.privOffset {
			m := uint32(buf[pos])<<24 | uint32(buf[pos+1])<<16 | uint32(buf[pos+2])<<8 | uint32(buf[pos+3])
			if m != f.privMagic {
				return nil
			}
			pos += 4
		} else if pos != f.privOffset {
			return nil
		}
		k.priv = buf[f.privOffset : f.privOffset+f.privLength]
		pos += f.privLength
	}
	if f.pubLength > 0 {
		if pos != f.pubOffset {
			return nil
		}
		pos += f.pubLength
	}
	if pos != len(buf) {
		return nil
	}
	return &decodedKey{desc: desc, private: true, pq: k}
}

// pqLoad is the keymgmt load of an ML-DSA/ML-KEM key: a seed-only key is
// generated, a private-key-only one decoded and checked against itself.
// What fails there needs the full ML-DSA/ML-KEM arithmetic; Go has the
// seed path only, so a private key given without its seed is accepted as
// OpenSSL accepts a well-formed one (see KNOWN_DIFFERENCES).
func (e *osslEnv) pqLoad(k *decodedKey) bool {
	if k.pq == nil {
		return true
	}
	pp := k.desc.pqParams
	if pp.family == pqMLKEM {
		return e.mlkemLoad(pp, k.pq)
	}
	return e.mldsaLoad(pp, k.pq)
}

// mldsaLoad ports ml_dsa_kmgmt.c ml_dsa_load: a seed (preferred) is
// expanded and must reproduce an explicit private key; a private key alone
// is decoded (ossl_ml_dsa_sk_decode) and must be consistent.
func (e *osslEnv) mldsaLoad(pp *pqParams, k *pqPending) bool {
	if k.seed != nil {
		if k.priv != nil && !bytes.Equal(mldsa.SemiExpandedFromSeed(k.seed, pp.privLen), k.priv) {
			e.q.raise(libPROV, "PROV_R_INVALID_KEY")
			return false
		}
		return true
	}
	switch mldsa.CheckSemiExpanded(k.priv) {
	case 1:
		e.q.raise(libPROV, "PROV_R_INVALID_KEY")
		return false
	case 2:
		e.q.raise(libPROV, "PROV_R_INVALID_KEY")
		e.q.raise(libPROV, "PROV_R_INVALID_KEY")
		return false
	}
	return true
}

// mlkemPubValid is ML-KEM's encapsulation-key check (every 12-bit
// coefficient of t below q = 3329).
func mlkemPubValid(pub []byte) bool {
	t := pub[:len(pub)-32]
	for i := 0; i+2 < len(t); i += 3 {
		a := uint16(t[i]) | uint16(t[i+1]&0x0F)<<8
		b := uint16(t[i+1]>>4) | uint16(t[i+2])<<4
		if a >= 3329 || b >= 3329 {
			return false
		}
	}
	return true
}

var _ = hex.EncodeToString
