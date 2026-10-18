package witness

import (
	"bytes"
	"crypto/ed25519"
	"math/big"
)

// The key decoders of OpenSSL 3.5's default provider
// (providers/implementations/encode_decode/decode_der2key.c and the
// crypto/<alg> functions they call), with the errors they raise.

// keyType is an EVP_PKEY type (NID) as the legacy code tracks it.
type keyType int

const (
	ktNone    keyType = 0
	ktRSA     keyType = 6
	ktRSA2    keyType = 19 // the X.500 "rsa" alias of RSA
	ktDH      keyType = 28
	ktDSA2    keyType = 67 // dsaEncryption-old, an alias of DSA
	ktDSA     keyType = 116
	ktEC      keyType = 408
	ktRSAPSS  keyType = 912
	ktDHX     keyType = 920
	ktX25519  keyType = 1034
	ktX448    keyType = 1035
	ktED25519 keyType = 1087
	ktED448   keyType = 1088
	ktSM2     keyType = 1172
)

// baseType is EVP_PKEY_type: aliases resolve to their base (SM2 is an alias
// of EC in the legacy method table).
func baseType(t keyType) keyType {
	switch t {
	case ktRSA2:
		return ktRSA
	case ktDSA2:
		return ktDSA
	case ktSM2:
		return ktEC
	}
	return t
}

// legacyTypeForOID is OBJ_obj2nid for the OIDs that have a legacy method
// (EVP_PKEY_set_type succeeds); ktNone otherwise.
func legacyTypeForOID(oid []byte) keyType {
	switch string(oid) {
	case string(oidRSA):
		return ktRSA
	case "\x55\x08\x01\x01":
		return ktRSA2
	case string(oidDH):
		return ktDH
	case "\x2b\x0e\x03\x02\x0c":
		return ktDSA2
	case string(oidDSA):
		return ktDSA
	case string(oidEC):
		return ktEC
	case string(oidRSAPSS):
		return ktRSAPSS
	case string(oidDHX):
		return ktDHX
	case string(oidX25519):
		return ktX25519
	case string(oidX448):
		return ktX448
	case string(oidEd25519):
		return ktED25519
	case string(oidEd448):
		return ktED448
	case string(oidSM2):
		return ktSM2
	}
	return ktNone
}

// pubKeyData is a decoded public key: its type and, for Ed25519, the key.
type pubKeyData struct {
	typ     keyType
	name    string // Node's asymmetricKeyType
	alg     string // OpenSSL type name
	ed25519 ed25519.PublicKey
}

// decodedKey is the provider-side key object a der2key decoder produced
// (before keymgmt load).
type decodedKey struct {
	desc    *keyDesc
	private bool
	ed      ed25519.PrivateKey // Ed25519 private key
	edPub   ed25519.PublicKey  // Ed25519 public key
	sm2     bool               // EC key on the SM2 curve
	rsaType int                // RSA_FLAG_TYPE_* of an RSA key
	pq      *pqPending         // ML-DSA/ML-KEM key to finish at load
}

// keyDesc is a der2key keytype_desc_st: the key type name, its namemap
// names (decoder is_a), and the decode functions per structure.
type keyDesc struct {
	name     string   // keymgmt / decoder name (OSSL_DECODER name)
	names    []string // all namemap names of the algorithm
	nodeName string   // Node's asymmetricKeyType ("" = undefined)
	evpType  keyType
	pqParams *pqParams // ML-DSA/ML-KEM/SLH-DSA parameters
	// typeSpecific: d2i_private_key / d2i_public_key / d2i_key_params.
	tsPriv, tsPub, tsParams bool
	ecLike                  bool // EC/SM2: ec_check
	rsaLike                 bool // RSA/RSA-PSS: rsa_check
}

func (d *keyDesc) String() string { return d.name }

var (
	descRSA    = &keyDesc{name: "RSA", names: []string{"RSA", "rsaEncryption", "1.2.840.113549.1.1.1", "2.5.8.1.1"}, nodeName: "rsa", evpType: ktRSA, tsPriv: true, tsPub: true, rsaLike: true}
	descDH     = &keyDesc{name: "DH", names: []string{"DH", "dhKeyAgreement", "1.2.840.113549.1.3.1"}, nodeName: "dh", evpType: ktDH, tsParams: true}
	descDSA    = &keyDesc{name: "DSA", names: []string{"DSA", "dsaEncryption", "DSA-old", "dsaEncryption-old", "1.3.14.3.2.12", "1.2.840.10040.4.1"}, nodeName: "dsa", evpType: ktDSA, tsPriv: true, tsPub: true, tsParams: true}
	descEC     = &keyDesc{name: "EC", names: []string{"EC", "id-ecPublicKey", "1.2.840.10045.2.1"}, nodeName: "ec", evpType: ktEC, tsPriv: true, tsParams: true, ecLike: true}
	descRSAPSS = &keyDesc{name: "RSA-PSS", names: []string{"RSA-PSS", "RSASSA-PSS", "rsassaPss", "1.2.840.113549.1.1.10"}, nodeName: "rsa-pss", evpType: ktRSAPSS, rsaLike: true}
	descDHX    = &keyDesc{name: "DHX", names: []string{"DHX", "X9.42 DH", "dhpublicnumber", "1.2.840.10046.2.1"}, nodeName: "", evpType: ktDHX, tsParams: true}
	descX25519 = &keyDesc{name: "X25519", names: []string{"X25519", "1.3.101.110"}, nodeName: "x25519", evpType: ktX25519}
	descX448   = &keyDesc{name: "X448", names: []string{"X448", "1.3.101.111"}, nodeName: "x448", evpType: ktX448}
	descED     = &keyDesc{name: "ED25519", names: []string{"ED25519", "1.3.101.112"}, nodeName: "ed25519", evpType: ktED25519}
	descED448  = &keyDesc{name: "ED448", names: []string{"ED448", "1.3.101.113"}, nodeName: "ed448", evpType: ktED448}
	descSM2    = &keyDesc{name: "SM2", names: []string{"SM2", "1.2.156.10197.1.301"}, nodeName: "", evpType: ktSM2, tsPriv: true, tsParams: true, ecLike: true}
)

// keyDescs lists the algorithms in decoders.inc order; pqDescs (pqkeys.go)
// follow them.
var keyDescs = []*keyDesc{descRSA, descDH, descDSA, descEC, descRSAPSS, descDHX, descX25519, descX448, descED, descED448, descSM2}

// --- the KEYPAIR decoder context PEM_read_bio_PrivateKey builds ----------

// privateKeyCtx is OSSL_DECODER_CTX_new_for_pkey(pkey, "PEM", NULL, NULL,
// EVP_PKEY_KEYPAIR): every decoder whose selection mask has the private
// key (decoders.inc order, does_selection), then the DER decoders.
func privateKeyCtx() *decoderCtx {
	var insts []decoderInst
	for _, d := range allDescs() {
		insts = append(insts, decoderInst{kind: decDer2Key, name: d.name, structure: "PrivateKeyInfo", desc: d})
		if d.tsPriv {
			insts = append(insts, decoderInst{kind: decDer2Key, name: d.name, structure: "type-specific", desc: d})
			if d != descSM2 {
				insts = append(insts, decoderInst{kind: decDer2Key, name: d.name, structure: asciiLower(d.name), desc: d})
			}
		}
	}
	insts = append(insts,
		decoderInst{kind: decSPKI2T, name: "DER", structure: "SubjectPublicKeyInfo"},
		decoderInst{kind: decEPKI2PKI, name: "DER", structure: "EncryptedPrivateKeyInfo"},
		decoderInst{kind: decPEM2DER, name: "DER"},
	)
	return &decoderCtx{insts: insts, startInputType: "PEM", selection: selKeypair}
}

// publicKeyCtx is the context x509_pubkey_ex_d2i_ex builds:
// OSSL_DECODER_CTX_new_for_pkey(pkey, "DER", "SubjectPublicKeyInfo",
// <OBJ_obj2txt of the OID>, EVP_PKEY_PUBLIC_KEY). Only the decoders of the
// keymgmt matching the name (and SM2 for EC) are collected.
func publicKeyCtx(keytype string) *decoderCtx {
	var insts []decoderInst
	for _, d := range allDescs() {
		match := descHasName(d, keytype)
		if !match && d == descSM2 && descHasName(descEC, keytype) && (keytype == "id-ecPublicKey" || keytype == "1.2.840.10045.2.1") {
			match = true
		}
		if !match {
			continue
		}
		insts = append(insts, decoderInst{kind: decDer2Key, name: d.name, structure: "SubjectPublicKeyInfo", desc: d})
		if d.tsPub {
			insts = append(insts, decoderInst{kind: decDer2Key, name: d.name, structure: "type-specific", desc: d})
			insts = append(insts, decoderInst{kind: decDer2Key, name: d.name, structure: asciiLower(d.name), desc: d})
		}
	}
	if len(insts) > 0 {
		insts = append(insts,
			decoderInst{kind: decSPKI2T, name: "DER", structure: "SubjectPublicKeyInfo"},
			decoderInst{kind: decEPKI2PKI, name: "DER", structure: "EncryptedPrivateKeyInfo"},
			decoderInst{kind: decPEM2DER, name: "DER"},
		)
	}
	return &decoderCtx{insts: insts, startInputType: "DER", inputStructure: "SubjectPublicKeyInfo", selection: selPublic}
}

func descHasName(d *keyDesc, name string) bool {
	for _, n := range d.names {
		if equalFoldASCII(n, name) {
			return true
		}
	}
	return false
}

func equalFoldASCII(a, b string) bool { return asciiLower(a) == asciiLower(b) }

// --- der2key_decode -----------------------------------------------------

// der2keyStructure ports der2key_decode for one decoder instance: read the
// DER from the BIO, decode it with the functions the structure and
// selection pick, check the key type, and hand a key reference on.
func (e *osslEnv) der2keyStructure(desc *keyDesc, data *procState, cb func(*decodeData) bool) bool {
	return e.der2keyInst(desc, data, cb, data.ctx.insts[data.currentIndex].structure)
}

func (e *osslEnv) der2keyInst(desc *keyDesc, data *procState, cb func(*decodeData) bool, structure string) bool {
	q := e.q
	ctx := data.ctx
	selection := ctx.selection
	// ctx->selection is the instance's caller selection; selection_mask per
	// structure (DO_<kind>).
	mask := 0
	switch structure {
	case "PrivateKeyInfo":
		mask = selPrivate
	case "SubjectPublicKeyInfo":
		mask = selPublic
	case "type-specific":
		switch {
		case desc == descRSA:
			mask = selKeypair
		case desc == descEC || desc == descSM2:
			mask = selPrivate | selAllParams
		case desc == descDSA:
			mask = selPrivate | selPublic | selAllParams
		default:
			mask = selAllParams
		}
	default: // the type's own name: DO_RSA, DO_DSA, DO_EC, DO_DH, DO_DHX
		switch {
		case desc == descRSA:
			mask = selKeypair
		case desc == descEC:
			mask = selPrivate | selAllParams
		case desc == descDSA:
			mask = selPrivate | selPublic | selAllParams
		default:
			mask = selAllParams
		}
	}
	if selection&mask == 0 {
		q.raise(57, "ERR_R_PASSED_INVALID_ARGUMENT")
		return false
	}
	der, ok := e.readDER(data.bio[data.pos:])
	data.pos += len(der)
	var key *decodedKey
	if ok {
		q.setMark()
		next := false
		if selection&selPrivate != 0 {
			if structure == "PrivateKeyInfo" {
				var fatal bool
				key, fatal = e.d2iPKCS8Key(desc, der)
				if fatal {
					q.clearLastMark()
					return true
				}
			} else if mask&selPrivate != 0 {
				key = e.d2iTypeSpecificPrivate(desc, der)
			}
			if key == nil {
				q.clearLastMark()
				next = true
			}
		}
		if !next && key == nil && selection&selPublic != 0 {
			if structure == "SubjectPublicKeyInfo" {
				key = e.d2iTypedPUBKEY(desc, der)
			} else if mask&selPublic != 0 {
				key = e.d2iTypeSpecificPublic(desc, der)
			}
			if key == nil {
				q.clearLastMark()
				next = true
			}
		}
		if !next && key == nil && selection&selAllParams != 0 {
			// d2i_key_params: never reached with KEYPAIR or PUBLIC selection
			// (the private/public branch above always jumps to next).
			q.clearLastMark()
			next = true
		}
		if !next {
			if key == nil {
				q.clearLastMark()
			} else {
				q.popToMark()
			}
			if key != nil && !e.checkKey(desc, key) {
				key = nil
			}
		}
	}
	if key != nil {
		dataType := desc.name
		if desc == descEC && key.sm2 {
			dataType = "SM2"
		}
		key.desc = descByName(dataType)
		return cb(&decodeData{dataType: dataType, hasType: true, key: key})
	}
	return true
}

func descByName(name string) *keyDesc {
	for _, d := range allDescs() {
		if d.name == name {
			return d
		}
	}
	return nil
}

// checkKey is the der2key check_key hook (rsa_check, ec_check).
func (e *osslEnv) checkKey(desc *keyDesc, key *decodedKey) bool {
	switch {
	case desc.rsaLike:
		want := 0
		if desc == descRSAPSS {
			want = 1
		}
		return key.rsaType == want // factors were checked at decode
	case desc.ecLike:
		// ec_check: an SM2-curve key passes the SM2 decoders and the EC
		// ones (whose evp_type is NID_X9_62_id_ecPublicKey).
		if key.sm2 {
			return true
		}
		return desc != descSM2
	}
	return true
}

// --- PKCS#8 --------------------------------------------------------------

// d2iPKCS8Key is the der2key d2i_PKCS8 function of desc: a PrivateKeyInfo
// whose algorithm is desc's type, then the type's key_from_pkcs8. fatal is
// der2key's flag_fatal (never set by these key types).
func (e *osslEnv) d2iPKCS8Key(desc *keyDesc, der []byte) (*decodedKey, bool) {
	if desc.pqParams != nil {
		return e.pqD2IPKCS8(desc, der), false
	}
	p8, _, ok := e.d2i(pkcs8Item, der)
	if !ok {
		return nil, false
	}
	alg := p8.fields[1]
	oid := alg.fields[0].data
	t := legacyTypeForOID(oid)
	if t != desc.evpType && !(t == ktEC && desc == descSM2) {
		return nil, false
	}
	return e.keyFromPKCS8(desc, p8), false
}

// keyFromPKCS8 ports the key_from_pkcs8 functions (ossl_rsa_key_from_pkcs8,
// ossl_dh_key_from_pkcs8, ossl_dsa_key_from_pkcs8, ossl_ec_key_from_pkcs8,
// ossl_ecx_key_from_pkcs8).
func (e *osslEnv) keyFromPKCS8(desc *keyDesc, p8 *asn1Value) *decodedKey {
	alg := p8.fields[1]
	inner := p8.fields[2].data
	switch desc {
	case descRSA, descRSAPSS:
		rsa, _, ok := e.d2i(rsaPrivateKeyItem, inner)
		if !ok {
			e.q.raise(libRSA, "ERR_R_RSA_LIB")
			return nil
		}
		if !e.rsaParamDecode(alg) {
			return nil
		}
		k := &decodedKey{desc: desc, private: true, rsaType: rsaFlagForOID(alg.fields[0].data)}
		if !rsaCheckFactors(rsa) {
			k.rsaType = -1
		}
		return k
	case descDH, descDHX:
		return e.dhKeyFromPKCS8(desc, alg, inner)
	case descDSA:
		return e.dsaKeyFromPKCS8(alg, inner)
	case descEC, descSM2:
		g := e.ecGroupFromAlgor(alg)
		if g == nil {
			return nil
		}
		if !e.d2iECPrivateKey(&g, inner) {
			e.q.raise(libEC, "EC_R_DECODE_ERROR")
			return nil
		}
		return &decodedKey{desc: desc, private: true, sm2: g.isSM2()}
	}
	// ECX: ossl_ecx_key_from_pkcs8.
	oct, _, ok := e.d2i(octetStringItem, inner)
	var p []byte
	if ok {
		p = oct.data
	}
	if !e.ecxKeyOp(alg, p, ok, desc.evpType) {
		return nil
	}
	k := &decodedKey{desc: desc, private: true}
	if desc == descED {
		k.ed = ed25519.NewKeyFromSeed(p)
	}
	return k
}

// rsaFlagForOID is the RSA_FLAG_TYPE_* the PKCS#8/SPKI decoders set: RSA
// for rsaEncryption, RSASSA-PSS for rsassaPss, 0 type bits otherwise.
func rsaFlagForOID(oid []byte) int {
	switch string(oid) {
	case string(oidRSA):
		return 0
	case string(oidRSAPSS):
		return 1
	}
	return 2
}

// rsaCheckFactors ports ossl_rsa_check_factors: every CRT component and
// d fit in n's bit length.
func rsaCheckFactors(rsa *asn1Value) bool {
	n := new(big.Int).SetBytes(rsa.fields[1].data).BitLen()
	for _, i := range []int{3, 4, 5, 6, 7, 8} {
		if new(big.Int).SetBytes(rsa.fields[i].data).BitLen() > n {
			return false
		}
	}
	if pi := rsa.fields[9]; pi != nil {
		for _, info := range pi.items {
			for _, f := range info.fields {
				if new(big.Int).SetBytes(f.data).BitLen() > n {
					return false
				}
			}
		}
	}
	return true
}

// rsaParamDecode ports rsa_backend.c ossl_rsa_param_decode (and the PSS
// parameter sync, ossl_rsa_pss_get_param_unverified).
func (e *osslEnv) rsaParamDecode(alg *asn1Value) bool {
	if string(alg.fields[0].data) != string(oidRSAPSS) {
		return true
	}
	param := alg.fields[1]
	if param == nil {
		return true
	}
	if param.utype != tagSequence {
		e.q.raise(libRSA, "RSA_R_INVALID_PSS_PARAMETERS")
		return false
	}
	pss, _, ok := e.d2i(rsaPSSParamsItem, param.data)
	if !ok {
		e.q.raise(libASN1, "ASN1_R_DECODE_ERROR")
		return false
	}
	if mg := pss.fields[1]; mg != nil {
		// ossl_x509_algor_mgf1_decode: an MGF1 algorithm whose parameter
		// is an AlgorithmIdentifier SEQUENCE.
		if string(mg.fields[0].data) != string(oidMGF1) {
			return false
		}
		mp := mg.fields[1]
		if mp == nil || mp.utype != tagSequence {
			return false
		}
		mh, _, ok := e.d2i(algorItem, mp.data)
		if !ok {
			e.q.raise(libASN1, "ASN1_R_DECODE_ERROR")
			return false
		}
		if !e.digestByObj(pss.fields[0]) {
			return false
		}
		return e.digestByObj(mh)
	}
	if !e.digestByObj(pss.fields[0]) {
		return false
	}
	return e.digestByObj(nil)
}

// digestByObj ports ossl_x509_algor_get_md: SHA-1 for an absent algorithm,
// else EVP_get_digestbyobj (a name the namemap does not know tries a fetch
// whose error it pops), raising ASN1_R_UNKNOWN_DIGEST when none.
func (e *osslEnv) digestByObj(alg *asn1Value) bool {
	if alg == nil {
		return true
	}
	oid := alg.fields[0].data
	if _, ok := digestByOID[string(oid)]; ok {
		return true
	}
	if !digestSilentMiss[string(oid)] {
		e.q.setMark()
		e.q.raise(libEVP, "ERR_R_UNSUPPORTED")
		e.q.popToMark()
	}
	e.q.raise(libASN1, "ASN1_R_UNKNOWN_DIGEST")
	return false
}

// --- type-specific structures -------------------------------------------

// d2iTypeSpecificPrivate is d2i_private_key of desc (d2i_RSAPrivateKey,
// d2i_DSAPrivateKey, d2i_ECPrivateKey).
func (e *osslEnv) d2iTypeSpecificPrivate(desc *keyDesc, der []byte) *decodedKey {
	switch desc {
	case descRSA:
		rsa, _, ok := e.d2i(rsaPrivateKeyItem, der)
		if !ok {
			return nil
		}
		k := &decodedKey{desc: desc, private: true}
		if !rsaCheckFactors(rsa) {
			k.rsaType = -1
		}
		return k
	case descDSA:
		if _, _, ok := e.d2i(dsaPrivateKeyItem, der); !ok {
			return nil
		}
		return &decodedKey{desc: desc, private: true}
	case descEC, descSM2:
		var g *ecGroup
		if !e.d2iECPrivateKey(&g, der) {
			return nil
		}
		return &decodedKey{desc: desc, private: true, sm2: g.isSM2()}
	}
	return nil
}

// d2iTypeSpecificPublic is d2i_public_key of desc (d2i_RSAPublicKey,
// d2i_DSAPublicKey).
func (e *osslEnv) d2iTypeSpecificPublic(desc *keyDesc, der []byte) *decodedKey {
	switch desc {
	case descRSA:
		if _, _, ok := e.d2i(rsaPublicKeyItem, der); !ok {
			return nil
		}
		return &decodedKey{desc: desc}
	case descDSA:
		if _, _, ok := e.d2i(dsaPublicKeyItem, der); !ok {
			return nil
		}
		return &decodedKey{desc: desc}
	}
	return nil
}

// --- SubjectPublicKeyInfo -------------------------------------------------

// d2iTypedPUBKEY is the der2key d2i_PUBKEY function of desc: the typed
// legacy wrappers (d2i_RSA_PUBKEY, d2i_EC_PUBKEY, ossl_d2i_*_PUBKEY), which
// parse with flag_force_legacy and check the key's type.
func (e *osslEnv) d2iTypedPUBKEY(desc *keyDesc, der []byte) *decodedKey {
	if desc.pqParams != nil {
		return e.pqD2IPUBKEY(desc, der)
	}
	d := &asn1Dec{q: e.q, env: e}
	v, _, ret := d.itemD2I(der, pubkeyInternalItem, -1, 0, false, 0)
	if ret <= 0 {
		return nil
	}
	// x509_pubkey_ex_d2i_ex with flag_force_legacy: the legacy decode's
	// errors are popped.
	e.q.setMark()
	pub := e.legacyPublicDecode(v)
	e.q.popToMark()
	if pub == nil {
		e.q.raise(libEVP, "EVP_R_DECODE_ERROR") // X509_PUBKEY_get0
		return nil
	}
	// The typed getters check the key type (EVP_PKEY_get1_RSA ...).
	switch desc {
	case descRSA, descRSAPSS:
		if pub.typ != ktRSA && pub.typ != ktRSAPSS {
			e.q.raise(libEVP, "EVP_R_EXPECTING_AN_RSA_KEY")
			return nil
		}
		return &decodedKey{desc: desc, rsaType: pub.rsaType()}
	case descDSA:
		if pub.typ != ktDSA {
			e.q.raise(libEVP, "EVP_R_EXPECTING_A_DSA_KEY")
			return nil
		}
		// ossl_d2i_DSA_PUBKEY: a key without p, q and g (absent or NULL
		// parameters) is refused without an error.
		if pub.noPQG {
			return nil
		}
	case descDH:
		// ossl_d2i_DH_PUBKEY checks EVP_PKEY_get_id first (no error).
		if pub.typ != ktDH {
			return nil
		}
	case descDHX:
		if pub.typ != ktDHX {
			return nil
		}
	case descEC, descSM2:
		// d2i_EC_PUBKEY checks the id (EC or SM2) without an error.
		if pub.typ != ktEC && pub.typ != ktSM2 {
			return nil
		}
		return &decodedKey{desc: desc, sm2: pub.sm2}
	case descED:
		// ossl_d2i_ED25519_PUBKEY calls the getter directly, which checks
		// the base type and raises.
		if baseType(pub.typ) != ktED25519 {
			e.q.raise(libEVP, "EVP_R_EXPECTING_A_ECX_KEY")
			return nil
		}
		return &decodedKey{desc: desc, edPub: pub.ed25519}
	default:
		// X25519, X448, ED448: the id is checked first (no error).
		if pub.typ != desc.evpType {
			return nil
		}
	}
	return &decodedKey{desc: desc}
}

// legacyKey is a public key the legacy pub_decode methods produced.
type legacyKey struct {
	typ     keyType
	noPQG   bool // a DSA key decoded without parameters (p, q, g unset)
	sm2     bool
	rsaFlag int
	ed25519 ed25519.PublicKey
}

func (k *legacyKey) rsaType() int { return k.rsaFlag }

// legacyPublicDecode ports x509_pubkey_decode with flag_force_legacy:
// EVP_PKEY_set_type(OBJ_obj2nid(algorithm)) then the method's pub_decode.
func (e *osslEnv) legacyPublicDecode(v *asn1Value) *legacyKey {
	alg := v.fields[0]
	oid := alg.fields[0].data
	t := legacyTypeForOID(oid)
	if t == ktNone {
		e.q.raise(libEVP, "EVP_R_UNSUPPORTED_ALGORITHM") // pkey_set_type
		e.q.raise(libX509, "X509_R_UNSUPPORTED_ALGORITHM")
		return nil
	}
	key := v.fields[1]
	switch baseType(t) {
	case ktRSA, ktRSAPSS:
		if _, _, ok := e.d2i(rsaPublicKeyItem, key.data); !ok {
			return nil
		}
		if !e.rsaParamDecode(alg) {
			return nil
		}
		flag := 0
		if baseType(t) == ktRSAPSS {
			flag = 1
		}
		// rsa_pub_decode assigns pkey->ameth->pkey_id: an alias OID
		// (2.5.8.1.1) resolves to the RSA method, so the key is RSA.
		return &legacyKey{typ: baseType(t), rsaFlag: flag}
	case ktDSA:
		if !e.dsaPubDecode(alg, key.data) {
			return nil
		}
		p := alg.fields[1]
		return &legacyKey{typ: ktDSA, noPQG: p == nil || p.utype != tagSequence} // EVP_PKEY_assign_DSA
	case ktDH, ktDHX:
		if !e.dhPubDecode(baseType(t) == ktDHX, alg, key.data) {
			return nil
		}
		return &legacyKey{typ: t}
	case ktEC:
		g := e.ecGroupFromAlgor(alg)
		if g == nil {
			return nil
		}
		if !e.ecPointDecode(g, key.data) {
			e.q.raise(libEC, "ERR_R_EC_LIB") // o2i_ECPublicKey
			e.q.raise(libEC, "EC_R_DECODE_ERROR")
			return nil
		}
		return &legacyKey{typ: t, sm2: g.isSM2()}
	}
	// ECX: ecx_pub_decode → ossl_ecx_key_op(KEY_OP_PUBLIC) with the method's
	// own id.
	if !e.ecxKeyOp(alg, key.data, true, t) {
		return nil
	}
	k := &legacyKey{typ: t}
	if t == ktED25519 {
		k.ed25519 = ed25519.PublicKey(append([]byte(nil), key.data...))
	}
	return k
}

// ecxKeyOp ports ecx_backend.c ossl_ecx_key_op's checks: parameters
// absent, the algorithm matches id, and the key length is the type's.
func (e *osslEnv) ecxKeyOp(alg *asn1Value, p []byte, present bool, id keyType) bool {
	if alg != nil {
		if alg.fields[1] != nil {
			e.q.raise(libEC, "EC_R_INVALID_ENCODING")
			return false
		}
		if id != legacyTypeForOID(alg.fields[0].data) {
			e.q.raise(libEC, "EC_R_INVALID_ENCODING")
			return false
		}
	}
	n := map[keyType]int{ktX25519: 32, ktED25519: 32, ktX448: 56, ktED448: 57}[id]
	if !present || len(p) != n {
		e.q.raise(libEC, "EC_R_INVALID_ENCODING")
		return false
	}
	return true
}

// --- DSA and DH ---------------------------------------------------------

// dsaPubDecode ports dsa_ameth.c dsa_pub_decode.
func (e *osslEnv) dsaPubDecode(alg *asn1Value, key []byte) bool {
	param := alg.fields[1]
	switch {
	case param != nil && param.utype == tagSequence:
		if _, _, ok := e.d2i(dsaParamsItem, param.data); !ok {
			e.q.raise(libDSA, "DSA_R_DECODE_ERROR")
			return false
		}
	case param == nil || param.utype == tagNull:
	default:
		e.q.raise(libDSA, "DSA_R_PARAMETER_ENCODING_ERROR")
		return false
	}
	if _, _, ok := e.d2i(integerItem, key); !ok {
		e.q.raise(libDSA, "DSA_R_DECODE_ERROR")
		return false
	}
	return true
}

// dsaKeyFromPKCS8 ports dsa_backend.c ossl_dsa_key_from_pkcs8.
func (e *osslEnv) dsaKeyFromPKCS8(alg *asn1Value, inner []byte) *decodedKey {
	fail := func() *decodedKey {
		e.q.raise(libDSA, "DSA_R_DECODE_ERROR")
		return nil
	}
	priv, _, ok := e.d2i(integerItem, inner)
	if !ok {
		return fail()
	}
	param := alg.fields[1]
	if priv.neg || param == nil || param.utype != tagSequence {
		return fail()
	}
	params, _, ok := e.d2i(dsaParamsItem, param.data)
	if !ok {
		return fail()
	}
	// BN_mod_exp(g, priv, p) with priv flagged BN_FLG_CONSTTIME: an odd p
	// takes the constant-time Montgomery path (which cannot fail here), an
	// even one (0 included) BN_mod_exp_recp, which refuses a CONSTTIME
	// operand before anything else.
	if p := params.fields[0].data; len(p) == 0 || p[len(p)-1]&1 == 0 {
		e.q.raise(libBN, "ERR_R_SHOULD_NOT_HAVE_BEEN_CALLED")
		e.q.raise(libDSA, "DSA_R_BN_ERROR")
		return nil
	}
	return &decodedKey{desc: descDSA, private: true}
}

// dhPubDecode ports dh_ameth.c dh_pub_decode.
func (e *osslEnv) dhPubDecode(dhx bool, alg *asn1Value, key []byte) bool {
	param := alg.fields[1]
	if param == nil || param.utype != tagSequence {
		e.q.raise(libDH, "DH_R_PARAMETER_ENCODING_ERROR")
		return false
	}
	it := dhParamsItem
	if dhx {
		it = dhxParamsItem
	}
	if _, _, ok := e.d2i(it, param.data); !ok {
		e.q.raise(libDH, "DH_R_DECODE_ERROR")
		return false
	}
	if _, _, ok := e.d2i(integerItem, key); !ok {
		e.q.raise(libDH, "DH_R_DECODE_ERROR")
		return false
	}
	return true
}

// dhKeyDecodeReason is the reason ossl_dh_key_from_pkcs8 raises on a
// decoding failure: OpenSSL 3.5 raises EVP_R_DECODE_ERROR under ERR_LIB_DH
// (DH's reason 114, "invalid parameter nid"); 3.6.5 fixed it to
// DH_R_DECODE_ERROR.
var dhKeyDecodeReason = "EVP_R_DECODE_ERROR"

// dhKeyFromPKCS8 ports dh_backend.c ossl_dh_key_from_pkcs8.
func (e *osslEnv) dhKeyFromPKCS8(desc *keyDesc, alg *asn1Value, inner []byte) *decodedKey {
	decerr := func() *decodedKey {
		e.q.raise(libDH, dhKeyDecodeReason)
		return nil
	}
	param := alg.fields[1]
	if param == nil || param.utype != tagSequence {
		return decerr()
	}
	if _, _, ok := e.d2i(integerItem, inner); !ok {
		return decerr()
	}
	var params *asn1Value
	var ok bool
	switch string(alg.fields[0].data) {
	case string(oidDH):
		params, _, ok = e.d2i(dhParamsItem, param.data)
	case string(oidDHX):
		params, _, ok = e.d2i(dhxParamsItem, param.data)
	default:
		return decerr()
	}
	if !ok {
		return decerr()
	}
	if !e.dhGenerateKey(params, string(alg.fields[0].data) == string(oidDHX)) {
		return nil
	}
	return &decodedKey{desc: desc, private: true}
}

// dhGenerateKey ports dh_key.c generate_key with an existing private key:
// the modulus size checks and the public key computation (BN_mod_exp).
func (e *osslEnv) dhGenerateKey(params *asn1Value, dhx bool) bool {
	p := new(big.Int).SetBytes(params.fields[0].data)
	var q *big.Int
	if dhx {
		q = new(big.Int).SetBytes(params.fields[2].data)
	}
	if p.BitLen() > 10000 { // OPENSSL_DH_MAX_MODULUS_BITS
		e.q.raise(libDH, "DH_R_MODULUS_TOO_LARGE")
		return false
	}
	if q != nil && q.BitLen() > 10000 {
		e.q.raise(libDH, "DH_R_Q_TOO_LARGE")
		return false
	}
	if p.BitLen() < 512 { // DH_MIN_MODULUS_BITS
		e.q.raise(libDH, "DH_R_MODULUS_TOO_SMALL")
		return false
	}
	// ossl_dh_generate_public_key: dh_init sets DH_FLAG_CACHE_MONT_P, so
	// BN_MONT_CTX_set(p) runs first; for an even p the inverse of R modulo
	// p's low word does not exist (BN_mod_inverse). generate_key then adds
	// its ERR_R_BN_LIB. With an odd p, BN_mod_exp_mont (constant time, as
	// the private key is flagged) cannot fail.
	if p.Bit(0) == 0 {
		e.q.raise(libBN, "BN_R_NO_INVERSE")
		e.q.raise(libDH, "ERR_R_BN_LIB")
		return false
	}
	return true
}

// --- keymgmt load ---------------------------------------------------------

// loadKey ports decoder_construct_pkey for a key reference: find the
// keymgmt by data type (EVP_KEYMGMT_is_a over its names), load the key,
// then evp_keymgmt_util_make_pkey (EVP_PKEY_set_type_by_keymgmt, whose
// find_ameth tries pkey_set_type on every name, popping its errors).
func (e *osslEnv) loadKey(k *decodedKey) *decodedKey {
	desc := k.desc
	if desc.pqParams != nil {
		if !e.pqLoad(k) {
			return nil
		}
	}
	if desc.rsaLike {
		want := 0
		if desc == descRSAPSS {
			want = 1
		}
		if k.rsaType != want {
			return nil
		}
	}
	for _, n := range keymgmtNames(desc) {
		e.q.setMark()
		if !nameHasAmeth(n) {
			e.q.raise(libEVP, "EVP_R_UNSUPPORTED_ALGORITHM")
		}
		e.q.popToMark()
	}
	return k
}

// keymgmtNames are the keymgmt's names in EVP_KEYMGMT_names_do_all order,
// and nameHasAmeth whether EVP_PKEY_asn1_find_str finds a legacy method for
// one (pkey_set_type by name).
func keymgmtNames(d *keyDesc) []string {
	if n, ok := keymgmtNameOrder[d.name]; ok {
		return n
	}
	return d.names
}

var keymgmtNameOrder = map[string][]string{
	"RSA":     {"rsaEncryption", "1.2.840.113549.1.1.1", "RSA", "2.5.8.1.1"},
	"DH":      {"dhKeyAgreement", "1.2.840.113549.1.3.1", "DH"},
	"DSA":     {"DSA", "dsaEncryption", "DSA-old", "dsaEncryption-old", "1.3.14.3.2.12", "1.2.840.10040.4.1"},
	"EC":      {"id-ecPublicKey", "1.2.840.10045.2.1", "EC"},
	"RSA-PSS": {"RSASSA-PSS", "rsassaPss", "1.2.840.113549.1.1.10", "RSA-PSS"},
	"DHX":     {"dhpublicnumber", "X9.42 DH", "1.2.840.10046.2.1", "DHX"},
	"X25519":  {"X25519", "1.3.101.110"},
	"X448":    {"X448", "1.3.101.111"},
	"ED25519": {"ED25519", "1.3.101.112"},
	"ED448":   {"ED448", "1.3.101.113"},
	"SM2":     {"SM2", "1.2.156.10197.1.301"},
}

func nameHasAmeth(n string) bool {
	switch n {
	case "RSA", "DH", "DSA", "EC", "RSA-PSS", "X9.42 DH", "X25519", "X448", "ED25519", "ED448":
		return true
	}
	return false
}

// --- the opportunistic X509_PUBKEY key decode -----------------------------

// spkiTrailingReason is the reason x509_pubkey_ex_d2i_ex raises (under
// ERR_LIB_ASN1) when the decoder succeeded without consuming every byte:
// EVP_R_DECODE_ERROR in OpenSSL 3.5 and 3.6.0, ASN1_R_DECODE_ERROR in
// 3.6.5 (openssl-3.5.5, 3.6.0 and 3.6.5 sources, crypto/x509/x_pubkey.c).
var spkiTrailingReason = "EVP_R_DECODE_ERROR"

// decodeSPKIKey ports the provider path of x509_pubkey_ex_d2i_ex (the
// legacy path finds no ENGINE): a DER/SubjectPublicKeyInfo decoder context
// for the algorithm's name, OSSL_DECODER_from_data, and the "must consume
// all bytes" check. On success (ok, with or without a key) the caller pops
// the errors to its mark. ok is false where OpenSSL fails the whole d2i:
// OBJ_obj2txt refusing the OID (more than 586 content octets; nothing is
// raised) and a decode that left bytes over (its reason stays queued).
// Both clear the caller's mark first, keeping what was raised since.
func (e *osslEnv) decodeSPKIKey(v *asn1Value, der []byte) (*pubKeyData, bool) {
	oid := v.fields[0].fields[0].data
	name, ok := obj2txt(oid)
	if !ok || name == "" {
		e.q.clearLastMark()
		return nil, false
	}
	ctx := publicKeyCtx(name)
	ok, pos := e.fromBIO(ctx, der, 0)
	if !ok || ctx.object == nil {
		return nil, true
	}
	// Ported for exactness; Node cannot reach it. The decoders read der
	// with asn1_d2i_read_bio, which returns a prefix of der, and the key
	// decoder parses that prefix with the same templates that consumed
	// all of der in the caller, so a key comes out only when the prefix
	// is der itself (round 11, r11-fix-report.md).
	if pos != len(der) {
		e.q.clearLastMark()
		e.q.raise(libASN1, spkiTrailingReason)
		return nil, false
	}
	k := ctx.object
	pd := &pubKeyData{typ: k.desc.evpType, name: k.desc.nodeName, alg: k.desc.name}
	if k.desc == descED {
		pd.ed25519 = k.edPub
	}
	return pd, true
}

// algorIsSM2 ports ec_backend.c ossl_x509_algor_is_sm2.
func algorIsSM2(e *osslEnv, alg *asn1Value) bool {
	param := alg.fields[1]
	if param == nil {
		return false
	}
	switch param.utype {
	case tagObject:
		return bytes.Equal(param.data, oidSM2)
	case tagSequence:
		g := e.d2iECPKParameters(param.data)
		return g != nil && g.isSM2()
	}
	return false
}
