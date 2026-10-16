package witness

// The ASN.1 templates of OpenSSL 3.5 that Node's key parsing reaches
// (crypto/asn1/x_algor.c, x_sig.c, p8_pkey.c, crypto/x509/x_pubkey.c,
// x_attrib.c, x_name.c, x_exten.c, x_x509.c, crypto/rsa/rsa_asn1.c,
// crypto/dsa/dsa_asn1.c, crypto/dh/dh_asn1.c, crypto/ec/ec_asn1.c,
// providers/implementations/encode_decode/decode_der2key.c).

func prim(utype int) *asn1Item { return &asn1Item{kind: itemPrimitive, utype: utype} }

func seq(tt ...asn1Template) *asn1Item { return &asn1Item{kind: itemSequence, tt: tt} }

func field(item *asn1Item) asn1Template { return asn1Template{item: item} }

func optional(item *asn1Item) asn1Template { return asn1Template{flags: tOptional, item: item} }

var (
	objectItem      = prim(tagObject)
	integerItem     = prim(tagInteger)
	bitStringItem   = prim(tagBitString)
	octetStringItem = prim(tagOctetString)
	nullItem        = prim(tagNull)
	anyItem         = prim(tagAny)
	booleanItem     = prim(tagBoolean)
	// BIGNUM and CBIGNUM: an INTEGER read with BN_bin2bn (the sign bit is
	// ignored).
	bignumItem = &asn1Item{kind: itemPrimitive, utype: tagInteger, prim: primBignum}
	// INT32 and ZINT32: a signed 32-bit INTEGER.
	int32Item = &asn1Item{kind: itemPrimitive, utype: tagInteger, prim: primInt32}

	algorItem          = seq(field(objectItem), optional(anyItem))
	pubkeyInternalItem = seq(field(algorItem), field(bitStringItem))
	x509SigItem        = seq(field(algorItem), field(octetStringItem))
	attributeItem      = seq(field(objectItem), asn1Template{flags: tSetOf, item: anyItem})
	pkcs8Item          = &asn1Item{kind: itemSequence, tt: []asn1Template{
		field(integerItem),
		field(algorItem),
		field(octetStringItem),
		{flags: tSetOf | tImplicit | tOptional, tag: 0, item: attributeItem},
		{flags: tImplicit | tOptional, tag: 1, item: bitStringItem},
	}, post: pkcs8Version}

	nameEntryItem    = seq(field(objectItem), field(&asn1Item{kind: itemMString, mask: bPrintable}))
	nameEntriesItem  = &asn1Item{kind: itemTemplate, tt: []asn1Template{{flags: tSetOf, item: nameEntryItem}}}
	nameInternalItem = &asn1Item{kind: itemTemplate, tt: []asn1Template{{flags: tSeqOf, item: nameEntriesItem}}}
	timeItem         = &asn1Item{kind: itemMString, mask: bTime}
	extensionItem    = seq(field(objectItem), optional(booleanItem), field(octetStringItem))

	// RSA (rsa_asn1.c): RSAPrivateKey with optional multi-prime infos,
	// RSAPublicKey, RSA_PSS_PARAMS.
	rsaPrimeInfoItem  = seq(field(bignumItem), field(bignumItem), field(bignumItem))
	rsaPrivateKeyItem = &asn1Item{kind: itemSequence, tt: []asn1Template{
		field(int32Item), field(bignumItem), field(bignumItem), field(bignumItem),
		field(bignumItem), field(bignumItem), field(bignumItem), field(bignumItem), field(bignumItem),
		{flags: tSeqOf | tOptional, item: rsaPrimeInfoItem},
	}, post: rsaMultiPrime}
	rsaPublicKeyItem = seq(field(bignumItem), field(bignumItem))
	rsaPSSParamsItem = seq(
		asn1Template{flags: tExplicit | tOptional, tag: 0, item: algorItem},
		asn1Template{flags: tExplicit | tOptional, tag: 1, item: algorItem},
		asn1Template{flags: tExplicit | tOptional, tag: 2, item: integerItem},
		asn1Template{flags: tExplicit | tOptional, tag: 3, item: integerItem},
	)

	// DSA (dsa_asn1.c).
	dsaPrivateKeyItem = seq(field(int32Item), field(bignumItem), field(bignumItem), field(bignumItem),
		field(bignumItem), field(bignumItem))
	dsaParamsItem    = seq(field(bignumItem), field(bignumItem), field(bignumItem))
	dsaPublicKeyItem = seq(field(bignumItem), field(bignumItem), field(bignumItem), field(bignumItem))

	// DH (dh_asn1.c): DHparams {p, g, length OPTIONAL ZINT32} and the X9.42
	// DHxparams {p, g, q, j OPTIONAL, vparams OPTIONAL}.
	dhParamsItem  = seq(field(bignumItem), field(bignumItem), optional(int32Item))
	dhvParamsItem = seq(field(bitStringItem), field(bignumItem))
	dhxParamsItem = seq(field(bignumItem), field(bignumItem), field(bignumItem), optional(bignumItem), optional(dhvParamsItem))

	// EC (ec_asn1.c).
	x962PentanomialItem = seq(field(int32Item), field(int32Item), field(int32Item))
	x962CharTwoItem     = seq(field(int32Item), field(objectItem), asn1Template{flags: tADB, adbField: 1,
		adb: map[string]*asn1Template{
			string(oidOnBasis): {item: nullItem},
			string(oidTpBasis): {item: integerItem},
			string(oidPpBasis): {item: x962PentanomialItem},
		},
		adbDefault: &asn1Template{item: anyItem},
	})
	x962FieldIDItem = seq(field(objectItem), asn1Template{flags: tADB, adbField: 0,
		adb: map[string]*asn1Template{
			string(oidPrimeField): {item: integerItem},
			string(oidCharTwo):    {item: x962CharTwoItem},
		},
		adbDefault: &asn1Template{item: anyItem},
	})
	x962CurveItem      = seq(field(octetStringItem), field(octetStringItem), optional(bitStringItem))
	ecParametersItem   = seq(field(int32Item), field(x962FieldIDItem), field(x962CurveItem), field(octetStringItem), field(integerItem), optional(integerItem))
	ecpkParametersItem = &asn1Item{kind: itemChoice, tt: []asn1Template{
		field(objectItem), field(ecParametersItem), field(nullItem),
	}}
	ecPrivateKeyItem = seq(field(int32Item), field(octetStringItem),
		asn1Template{flags: tExplicit | tOptional, tag: 0, item: ecpkParametersItem},
		asn1Template{flags: tExplicit | tOptional, tag: 1, item: bitStringItem})

	// SLH-DSA's BARE_PUBKEY (decode_der2key.c).
	bareAlgorItem  = seq(field(objectItem))
	barePubkeyItem = seq(field(bareAlgorItem), field(bitStringItem))
)

// pubkeyItem, nameItem, x509CinfItem and x509Item refer to the EXTERN decode
// functions, so they are built at init.
var (
	pubkeyItem   *asn1Item
	nameItem     *asn1Item
	x509CinfItem *asn1Item
	x509Item     *asn1Item
)

func init() {
	pubkeyItem = &asn1Item{kind: itemPubkey, extern: (*asn1Dec).pubkeyD2I}
	nameItem = &asn1Item{kind: itemName, extern: (*asn1Dec).nameD2I}
	x509CinfItem = seq(
		asn1Template{flags: tExplicit | tOptional, tag: 0, item: integerItem},
		field(integerItem),
		field(algorItem),
		field(nameItem),
		field(seq(field(timeItem), field(timeItem))),
		field(nameItem),
		field(pubkeyItem),
		asn1Template{flags: tImplicit | tOptional, tag: 1, item: bitStringItem},
		asn1Template{flags: tImplicit | tOptional, tag: 2, item: bitStringItem},
		asn1Template{flags: tExplicit | tSeqOf | tOptional, tag: 3, item: extensionItem},
	)
	x509Item = seq(field(x509CinfItem), field(algorItem), field(bitStringItem))
}

// pkcs8Version is p8_pkey.c pkey_cb's ASN1_OP_D2I_POST: the version must be
// 0 or 1 (ASN1_INTEGER_get), and a version 0 key has no public key.
func pkcs8Version(d *asn1Dec, v *asn1Value) bool {
	version := d.integerGet(v.fields[0])
	if version < 0 || version > 1 {
		return false
	}
	return version == 1 || v.fields[4] == nil
}

// rsaMultiPrime is rsa_asn1.c rsa_cb's ASN1_OP_D2I_POST: a version-1
// (multi-prime) key needs at least one extra prime
// (ossl_rsa_multip_calc_product fails on none).
func rsaMultiPrime(_ *asn1Dec, v *asn1Value) bool {
	if v.fields[0].int32() != 1 {
		return true
	}
	return v.fields[9] != nil && len(v.fields[9].items) > 0
}

// OID content octets.
var (
	oidEd25519    = []byte{0x2B, 0x65, 0x70}
	oidX25519     = []byte{0x2B, 0x65, 0x6E}
	oidX448       = []byte{0x2B, 0x65, 0x6F}
	oidEd448      = []byte{0x2B, 0x65, 0x71}
	oidRSA        = []byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x01}
	oidRSAPSS     = []byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x0A}
	oidMGF1       = []byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x08}
	oidEC         = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01}
	oidSM2        = []byte{0x2A, 0x81, 0x1C, 0xCF, 0x55, 0x01, 0x82, 0x2D}
	oidDSA        = []byte{0x2A, 0x86, 0x48, 0xCE, 0x38, 0x04, 0x01}
	oidDH         = []byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x03, 0x01}
	oidDHX        = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3E, 0x02, 0x01}
	oidPrimeField = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x01, 0x01}
	oidCharTwo    = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x01, 0x02}
	oidOnBasis    = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x01, 0x02, 0x03, 0x01}
	oidTpBasis    = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x01, 0x02, 0x03, 0x02}
	oidPpBasis    = []byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x01, 0x02, 0x03, 0x03}
)
