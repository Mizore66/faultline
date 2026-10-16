package witness

import (
	"crypto/ed25519"
	"errors"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// Node 22's createPublicKey(pem) and createPrivateKey(pem) (src/crypto/
// crypto_keys.cc, deps/ncrypto TryParsePublicKeyPEM / TryParsePrivateKey)
// over OpenSSL 3.5's PEM_bytes_read_bio, the d2i functions and
// PEM_read_bio_PrivateKey (crypto/pem/pem_pkey.c pem_read_bio_key).

// keyObject is the part of a Node KeyObject this module reads.
type keyObject struct {
	ed25519 bool
	public  ed25519.PublicKey
	private ed25519.PrivateKey
	typ     string // asymmetricKeyType ("" for undefined)
	alg     string // the OpenSSL key type name (EVP_PKEY_get0_type_name)
}

// errMissingPassphrase is Node's ERR_MISSING_PASSPHRASE message.
const errMissingPassphrase = "Passphrase required for encrypted key"

// parsePublic is createPublicKey(pem).
func parsePublic(text string) (keyObject, error) {
	in := []byte(jsstr.ToUTF8(text))
	e := &osslEnv{q: &errQueue{}}
	return e.parsePublic(in)
}

// parsePrivate is createPrivateKey(pem).
func parsePrivate(text string) (keyObject, error) {
	in := []byte(jsstr.ToUTF8(text))
	e := &osslEnv{q: &errQueue{}}
	return e.parsePrivate(in)
}

// parsePublic ports TryParsePublicKeyPEM and its fallback: three passes
// ("PUBLIC KEY" d2i_PUBKEY, "RSA PUBLIC KEY" d2i_PublicKey(RSA),
// "CERTIFICATE" d2i_X509 + X509_get_pubkey), each reading from the start
// with its PEM errors popped; a pass whose d2i fails leaves its errors
// queued. If no pass gives a key the result is NOT_RECOGNIZED and Node
// calls createPrivateKey's parser on the same text and queue.
func (e *osslEnv) parsePublic(in []byte) (keyObject, error) {
	q := e.q
	passes := []struct {
		name  string
		parse func([]byte) *keyObject
	}{
		{"PUBLIC KEY", e.d2iPUBKEY},
		{"RSA PUBLIC KEY", e.d2iPublicKeyRSA},
		{"CERTIFICATE", e.d2iX509PublicKey},
	}
	for _, pass := range passes {
		q.setMark()
		blk, ok := e.bytesRead(&pemBIO{data: in}, pass.name, e.defCallback)
		q.popToMark()
		if !ok {
			continue
		}
		if key := pass.parse(blk.data); key != nil {
			return *key, nil
		}
	}
	return e.parsePrivate(in)
}

// d2iPUBKEY is d2i_PUBKEY: X509_PUBKEY then X509_PUBKEY_get0.
func (e *osslEnv) d2iPUBKEY(der []byte) *keyObject {
	v, _, ok := e.d2i(pubkeyItem, der)
	if !ok {
		return nil
	}
	return e.pubkeyGet(v.pub)
}

// pubkeyGet is X509_PUBKEY_get0/get: the decoded key or EVP decode error.
func (e *osslEnv) pubkeyGet(pub *pubKeyData) *keyObject {
	if pub == nil {
		e.q.raise(libEVP, "EVP_R_DECODE_ERROR")
		return nil
	}
	k := &keyObject{typ: pub.name, alg: pub.alg}
	if pub.typ == ktED25519 {
		k.ed25519 = true
		k.public = pub.ed25519
	}
	return k
}

// d2iPublicKeyRSA is d2i_PublicKey(EVP_PKEY_RSA): d2i_RSAPublicKey.
func (e *osslEnv) d2iPublicKeyRSA(der []byte) *keyObject {
	if _, _, ok := e.d2i(rsaPublicKeyItem, der); !ok {
		e.q.raise(libASN1, "ERR_R_ASN1_LIB")
		return nil
	}
	return &keyObject{typ: "rsa", alg: "RSA"}
}

// d2iX509PublicKey is d2i_X509 then X509_get_pubkey.
func (e *osslEnv) d2iX509PublicKey(der []byte) *keyObject {
	v, _, ok := e.d2i(x509Item, der)
	if !ok {
		return nil
	}
	return e.pubkeyGet(v.fields[0].fields[6].pub)
}

// parsePrivate ports TryParsePrivateKey (PEM): PEM_read_bio_PrivateKey
// with Node's PasswordCallback and no passphrase, then keyOrError, which
// reports the oldest queued error even when a key came out.
func (e *osslEnv) parsePrivate(in []byte) (keyObject, error) {
	q := e.q
	key := e.pemReadBioKey(in)
	if err := q.peekFirst(); err != 0 {
		if err == errCode(libPEM, osslReasons["PEM_R_BAD_PASSWORD_READ"]) {
			return keyObject{}, errors.New(errMissingPassphrase)
		}
		return keyObject{}, nodeError(q, err)
	}
	if key == nil {
		return keyObject{}, nodeError(q, 0)
	}
	return *key, nil
}

// pemReadBioKey ports pem_pkey.c pem_read_bio_key (selection
// EVP_PKEY_KEYPAIR): the decoder loop, then the legacy reader from the
// start; a legacy key pops the errors the decoder loop left.
func (e *osslEnv) pemReadBioKey(in []byte) *keyObject {
	q := e.q
	q.setMark()
	key := e.readKeyDecoder(in)
	if key == nil {
		key = e.readKeyLegacy(in)
	}
	if key == nil {
		q.clearLastMark()
	} else {
		q.popToMark()
	}
	return key
}

// readKeyDecoder ports pem_read_bio_key_decoder.
func (e *osslEnv) readKeyDecoder(in []byte) *keyObject {
	q := e.q
	pos := 0
	q.setMark()
	for {
		ctx := privateKeyCtx()
		ok, newpos := e.fromBIO(ctx, in, pos)
		if ok && ctx.object != nil {
			q.popToMark()
			return e.keyFromDecoded(ctx.object)
		}
		if newpos >= len(in) || newpos <= pos {
			q.clearLastMark()
			return nil
		}
		if code := q.peekFirst(); code&0x7FFFFF == osslCommon["ERR_R_UNSUPPORTED"].reason {
			q.popToMark()
			q.setMark()
		} else {
			q.clearLastMark()
			return nil
		}
		pos = newpos
	}
}

// keyFromDecoded turns the loaded provider key into the KeyObject view.
func (e *osslEnv) keyFromDecoded(k *decodedKey) *keyObject {
	ko := &keyObject{typ: k.desc.nodeName, alg: k.desc.name}
	if k.desc == descED && k.ed != nil {
		ko.ed25519 = true
		ko.private = k.ed
		ko.public = k.ed.Public().(ed25519.PublicKey)
	}
	return ko
}

// readKeyLegacy ports pem_read_bio_key_legacy for a private key.
func (e *osslEnv) readKeyLegacy(in []byte) *keyObject {
	q := e.q
	q.setMark()
	blk, ok := e.bytesRead(&pemBIO{data: in}, "ANY PRIVATE KEY", e.pwCallback)
	if !ok {
		q.popToMark()
		return nil
	}
	q.clearLastMark()
	var ret *keyObject
	switch {
	case blk.name == "PRIVATE KEY":
		if p8, _, ok := e.d2i(pkcs8Item, blk.data); ok {
			ret = e.pkcs82pkeyLegacy(p8)
		}
	case blk.name == "ENCRYPTED PRIVATE KEY":
		if _, _, ok := e.d2i(x509SigItem, blk.data); ok {
			e.pwCallback()
			q.raise(libPEM, "PEM_R_BAD_PASSWORD_READ")
			return nil
		}
	default:
		ret = e.d2iPrivateKeyLegacy(blk.name, blk.data)
	}
	if ret == nil && q.peekLast() == 0 {
		q.raise(libPEM, "ERR_R_ASN1_LIB")
	}
	return ret
}

// pkcs82pkeyLegacy ports evp_pkey.c evp_pkcs82pkey_legacy.
func (e *osslEnv) pkcs82pkeyLegacy(p8 *asn1Value) *keyObject {
	oid := p8.fields[1].fields[0].data
	t := legacyTypeForOID(oid)
	desc := descForType(baseType(t))
	if desc == nil {
		// EVP_PKEY_set_type → pkey_set_type raises first.
		e.q.raise(libEVP, "EVP_R_UNSUPPORTED_ALGORITHM")
		e.q.raise(libEVP, "EVP_R_UNSUPPORTED_PRIVATE_KEY_ALGORITHM")
		return nil
	}
	k := e.legacyPrivDecode(desc, p8)
	if k == nil {
		// Methods with priv_decode (not priv_decode_ex: RSA, RSA-PSS, DH,
		// DHX, DSA) get EVP_R_PRIVATE_KEY_DECODE_ERROR added.
		if desc == descRSA || desc == descRSAPSS || desc == descDH || desc == descDHX || desc == descDSA {
			e.q.raise(libEVP, "EVP_R_PRIVATE_KEY_DECODE_ERROR")
		}
		return nil
	}
	return e.keyFromDecoded(k)
}

// legacyPrivDecode is the method's priv_decode(_ex) (the same key_from_pkcs8
// functions the provider uses; RSA's does not check the type flags).
//
// eckey_priv_decode assigns the key with EVP_PKEY_assign_EC_KEY, and
// EVP_PKEY_assign makes a key on the SM2 curve an SM2 key whatever type
// was asked for.
func (e *osslEnv) legacyPrivDecode(desc *keyDesc, p8 *asn1Value) *decodedKey {
	if desc == descSM2 {
		desc = descEC
	}
	k := e.keyFromPKCS8(desc, p8)
	if k != nil && desc == descEC && k.sm2 {
		k.desc = descSM2
	}
	return k
}

func descForType(t keyType) *keyDesc {
	for _, d := range keyDescs {
		if d.evpType == t && d != descSM2 {
			return d
		}
	}
	return nil
}

// d2iPrivateKeyLegacy ports the "<X> PRIVATE KEY" branch: the method found
// by name (EVP_PKEY_asn1_find_str: RSA, DSA, EC) decodes with
// old_priv_decode, else as PKCS#8 of the same type.
func (e *osslEnv) d2iPrivateKeyLegacy(name string, der []byte) *keyObject {
	prefix := name[:len(name)-len(" PRIVATE KEY")]
	var desc *keyDesc
	switch asciiLower(prefix) {
	case "rsa":
		desc = descRSA
	case "dsa":
		desc = descDSA
	case "ec":
		desc = descEC
	}
	if desc == nil {
		// EVP_PKEY_asn1_find_str finds no method with an old_priv_decode
		// (a block named exactly "ANY PRIVATE KEY" passes check_pem by
		// name): goto p8err.
		return nil
	}
	q := e.q
	q.setMark()
	if k := e.d2iTypeSpecificPrivate(desc, der); k != nil {
		q.clearLastMark()
		return e.keyFromDecoded(k)
	}
	if desc == descDSA {
		q.raise(libDSA, "ERR_R_DSA_LIB")
	}
	p8, _, ok := e.d2i(pkcs8Item, der)
	if !ok {
		q.clearLastMark()
		return nil
	}
	oid := p8.fields[1].fields[0].data
	pdesc := descForType(baseType(legacyTypeForOID(oid)))
	ko := e.pkcs82pkeyLegacy(p8)
	if ko == nil {
		q.clearLastMark()
		return nil
	}
	q.popToMark()
	if baseType(pdesc.evpType) != baseType(desc.evpType) {
		return nil
	}
	return ko
}
