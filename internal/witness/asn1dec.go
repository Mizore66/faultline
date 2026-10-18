package witness

import (
	"math"
	"math/big"
	"runtime"
)

// A port of OpenSSL's table-driven DER/BER decoder (crypto/asn1/tasn_dec.c,
// asn1_lib.c and the c2i functions it calls, OpenSSL 3.5) for the templates
// Node's createPublicKey and createPrivateKey reach. Every ERR_raise lands
// in the error queue in the order OpenSSL raises it, including the "nested
// asn1 error" each caller adds on the way out: when the queue is full the
// oldest error is overwritten, so the exact count matters for the message
// Node prints.
//
// The TLC header cache (ASN1_TLC) is left out: it only memoises a header
// already parsed at the same position and length, so it never changes the
// outcome or the errors.

// Universal tags (V_ASN1_*) and the pseudo types the decoder uses.
const (
	tagEOC             = 0
	tagBoolean         = 1
	tagInteger         = 2
	tagBitString       = 3
	tagOctetString     = 4
	tagNull            = 5
	tagObject          = 6
	tagEnumerated      = 10
	tagUTF8String      = 12
	tagSequence        = 16
	tagSet             = 17
	tagPrintableString = 19
	tagT61String       = 20
	tagIA5String       = 22
	tagUTCTime         = 23
	tagGeneralizedTime = 24
	tagVisibleString   = 26
	tagUniversalString = 28
	tagBMPString       = 30
	tagOther           = -3 // V_ASN1_OTHER: an ANY with a non-universal tag
	tagAny             = -4 // V_ASN1_ANY

	classUniversal = 0x00
	classContext   = 0x80
)

// C long is 32 bits on Windows (LLP64), which bounds asn1_get_length.
var cLongSize, cLongMax = func() (int, uint64) {
	if runtime.GOOS == "windows" {
		return 4, math.MaxInt32
	}
	return 8, math.MaxInt64
}()

// lengthNeedsContentByte is asn1_get_length's "max < i + 1" (the long
// form needs one byte after the length octets) in OpenSSL 3.5 and up to
// 3.6.0; 3.6.5 checks "max < i" (openssl-3.6.0 and openssl-3.6.5 sources,
// crypto/asn1/asn1_lib.c). Node 22.22.2 bundles 3.5.5. Only the error of a
// truncated header differs (header too long or field missing).
var lengthNeedsContentByte = true

// Item kinds (ASN1_ITYPE_*, with the EXTERN items and ITEM_TEMPLATE split
// out).
const (
	itemPrimitive = iota
	itemMString
	itemSequence
	itemChoice
	itemTemplate // ASN1_ITEM_TEMPLATE: a PRIMITIVE item holding one template
	itemPubkey   // X509_PUBKEY (crypto/x509/x_pubkey.c x509_pubkey_ex_d2i_ex)
	itemName     // X509_NAME (crypto/x509/x_name.c x509_name_ex_d2i)
)

// Primitive content functions (ASN1_PRIMITIVE_FUNCS prim_c2i).
const (
	primPlain  = iota
	primBignum // x_bignum.c bn_c2i: BN_bin2bn, any content
	primInt32  // x_int64.c uint32_c2i with INTxx_FLAG_SIGNED (INT32, ZINT32)
)

// Template flags (ASN1_TFLG_*).
const (
	tOptional = 1 << iota
	tExplicit
	tImplicit
	tSetOf
	tSeqOf
	tADB // ASN1_ADB_OBJECT: the template is chosen by the OID in field adbField
)

type asn1Item struct {
	kind   int
	utype  int    // primitive: V_ASN1_* (tagAny for ASN1_ANY)
	mask   uint32 // mstring: B_ASN1_* bits
	prim   int
	tt     []asn1Template
	post   func(*asn1Dec, *asn1Value) bool // ASN1_OP_D2I_POST
	extern func(d *asn1Dec, in []byte, tag, aclass int, opt bool) (*asn1Value, int, int)
}

type asn1Template struct {
	flags int
	tag   int
	item  *asn1Item
	// adb: for a tADB template, the field holding the selector OID and
	// the template per OID content (hex), with adbDefault for any other.
	adbField   int
	adb        map[string]*asn1Template
	adbDefault *asn1Template
}

// asn1Value is a decoded item: a primitive's type and content, a
// SEQUENCE's fields (nil when an OPTIONAL one is absent), a SET OF's items,
// a CHOICE's selected field.
type asn1Value struct {
	utype  int
	data   []byte // content octets; a bit string's without the unused-bits octet, masked
	bits   int    // a bit string's unused bits
	fields []*asn1Value
	items  []*asn1Value
	choice int
	pub    *pubKeyData // X509_PUBKEY: the key OpenSSL decoded opportunistically, nil if none
	raw    []byte      // the whole TLV (SEQUENCE/SET/OTHER kept encoded, and every SEQUENCE)
	neg    bool        // INTEGER: negative
}

// int32 is an INT32/ZINT32 field's value.
func (v *asn1Value) int32() int64 {
	n := new(big.Int).SetBytes(v.data)
	if v.neg {
		n.Neg(n)
	}
	return n.Int64()
}

type asn1Dec struct {
	q   *errQueue
	env *osslEnv
}

func (d *asn1Dec) raise(reason string) { d.q.raise(libASN1, reason) }

func (d *asn1Dec) nested() { d.q.raise(libASN1, "ERR_R_NESTED_ASN1_ERROR") }

// d2i ports ASN1_item_d2i: false (and the errors queued) when the DER does
// not decode. Bytes after the top-level object are ignored, as in OpenSSL.
func (e *osslEnv) d2i(it *asn1Item, der []byte) (*asn1Value, int, bool) {
	d := &asn1Dec{q: e.q, env: e}
	v, n, ret := d.itemD2I(der, it, -1, 0, false, 0)
	return v, n, ret > 0
}

// Port of asn1_lib.c ASN1_get_object: ret carries 0x20 (constructed),
// 0x01 (indefinite) and 0x80 (error).
func (d *asn1Dec) getObject(in []byte) (ret, hdr int, plen int64, tag, class int) {
	omax := len(in)
	if omax <= 0 {
		d.raise("ASN1_R_TOO_SMALL")
		return 0x80, 0, 0, 0, 0
	}
	fail := func() (int, int, int64, int, int) {
		d.raise("ASN1_R_HEADER_TOO_LONG")
		return 0x80, 0, 0, 0, 0
	}
	max := omax
	ret = int(in[0] & 0x20)
	class = int(in[0] & 0xC0)
	p := 0
	if i := int(in[0] & 0x1F); i == 0x1F {
		p++
		if max--; max == 0 {
			return fail()
		}
		var l int64
		for in[p]&0x80 != 0 {
			l = l<<7 | int64(in[p]&0x7F)
			p++
			if max--; max == 0 {
				return fail()
			}
			if l > math.MaxInt32>>7 {
				return fail()
			}
		}
		l = l<<7 | int64(in[p]&0x7F)
		p++
		tag = int(l)
		if max--; max == 0 {
			return fail()
		}
	} else {
		tag = i
		p++
		if max--; max == 0 {
			return fail()
		}
	}
	inf, length, n, ok := getLength(in[p:])
	if !ok {
		return fail()
	}
	p += n
	if inf && ret&0x20 == 0 {
		return fail()
	}
	if length > int64(omax-p) {
		d.raise("ASN1_R_TOO_LONG")
		ret |= 0x80
	}
	if inf {
		ret |= 1
	}
	return ret, p, length, tag, class
}

// Port of asn1_lib.c asn1_get_length; max is len(in).
func getLength(in []byte) (inf bool, length int64, n int, ok bool) {
	max := len(in)
	if max < 1 {
		return false, 0, 0, false
	}
	max--
	if in[0] == 0x80 {
		return true, 0, 1, true
	}
	i := int(in[0] & 0x7F)
	if in[0]&0x80 == 0 {
		return false, int64(i), 1, true
	}
	need := i
	if lengthNeedsContentByte {
		need++
	}
	if max < need {
		return false, 0, 0, false
	}
	p := 1
	for i > 0 && in[p] == 0 {
		p++
		i--
	}
	if i > cLongSize {
		return false, 0, 0, false
	}
	var r uint64
	for ; i > 0; i-- {
		r = r<<8 | uint64(in[p])
		p++
	}
	if r > cLongMax {
		return false, 0, 0, false
	}
	return false, int64(r), p, true
}

type tlen struct {
	hdr        int
	plen       int
	tag, class int
	inf, cst   bool
}

// Port of tasn_dec.c asn1_check_tlen: 1 matched, 0 error, -1 an OPTIONAL
// item's tag did not match.
func (d *asn1Dec) checkTLen(in []byte, exptag, expclass int, opt bool) (tlen, int) {
	if len(in) <= 0 {
		d.raise("ASN1_R_TOO_SMALL")
		return tlen{}, 0
	}
	i, hdr, plen, ptag, pclass := d.getObject(in)
	if i&0x80 != 0 {
		d.raise("ASN1_R_BAD_OBJECT_HEADER")
		return tlen{}, 0
	}
	if exptag >= 0 && (exptag != ptag || expclass != pclass) {
		if opt {
			return tlen{}, -1
		}
		d.raise("ASN1_R_WRONG_TAG")
		return tlen{}, 0
	}
	if i&1 != 0 {
		plen = int64(len(in) - hdr)
	}
	return tlen{hdr: hdr, plen: int(plen), tag: ptag, class: pclass, inf: i&1 != 0, cst: i&0x20 != 0}, 1
}

// Port of tasn_dec.c asn1_check_eoc.
func checkEOC(in []byte) bool {
	return len(in) >= 2 && in[0] == 0 && in[1] == 0
}

// Port of tasn_dec.c asn1_item_embed_d2i: 1 decoded (n bytes), 0 error,
// -1 absent OPTIONAL item.
func (d *asn1Dec) itemD2I(in []byte, it *asn1Item, tag, aclass int, opt bool, depth int) (*asn1Value, int, int) {
	if len(in) <= 0 {
		d.raise("ASN1_R_TOO_SMALL")
		return nil, 0, 0
	}
	if it.extern != nil {
		return it.extern(d, in, tag, aclass, opt)
	}
	if depth++; depth > 30 { // ASN1_MAX_CONSTRUCTED_NEST
		d.raise("ASN1_R_NESTED_TOO_DEEP")
		return nil, 0, 0
	}
	switch it.kind {
	case itemTemplate:
		if tag != -1 || opt {
			d.raise("ASN1_R_ILLEGAL_OPTIONS_ON_ITEM_TEMPLATE")
			return nil, 0, 0
		}
		return d.templateD2I(in, &it.tt[0], opt, depth)
	case itemPrimitive:
		return d.primitiveD2I(in, it, tag, aclass, opt)
	case itemMString:
		if tag != -1 {
			d.raise("ASN1_R_BAD_TEMPLATE")
			return nil, 0, 0
		}
		r, ret := d.checkTLen(in, -1, 0, true)
		if ret == 0 {
			d.nested()
			return nil, 0, 0
		}
		if r.class != classUniversal {
			if opt {
				return nil, 0, -1
			}
			d.raise("ASN1_R_MSTRING_NOT_UNIVERSAL")
			return nil, 0, 0
		}
		if tag2bit(r.tag)&it.mask == 0 {
			if opt {
				return nil, 0, -1
			}
			d.raise("ASN1_R_MSTRING_WRONG_TAG")
			return nil, 0, 0
		}
		return d.primitiveD2I(in, it, r.tag, 0, false)
	case itemChoice:
		if tag != -1 {
			d.raise("ASN1_R_BAD_TEMPLATE")
			return nil, 0, 0
		}
		for i := range it.tt {
			v, n, ret := d.templateD2I(in, &it.tt[i], true, depth)
			if ret == -1 {
				continue
			}
			if ret > 0 {
				return &asn1Value{choice: i, fields: []*asn1Value{v}}, n, 1
			}
			d.nested()
			return nil, 0, 0
		}
		if opt {
			return nil, 0, -1
		}
		d.raise("ASN1_R_NO_MATCHING_CHOICE_TYPE")
		return nil, 0, 0
	}
	return d.sequenceD2I(in, it, tag, aclass, opt, depth)
}

// The SEQUENCE case of asn1_item_embed_d2i.
func (d *asn1Dec) sequenceD2I(in []byte, it *asn1Item, tag, aclass int, opt bool, depth int) (*asn1Value, int, int) {
	if tag == -1 {
		tag, aclass = tagSequence, classUniversal
	}
	r, ret := d.checkTLen(in, tag, aclass, opt)
	if ret == 0 {
		d.nested()
		return nil, 0, 0
	}
	if ret == -1 {
		return nil, 0, -1
	}
	seqEOC := r.inf
	seqNolen := seqEOC
	if !r.cst {
		d.raise("ASN1_R_SEQUENCE_NOT_CONSTRUCTED")
		return nil, 0, 0
	}
	p, length := r.hdr, r.plen
	v := &asn1Value{utype: tagSequence, fields: make([]*asn1Value, len(it.tt))}
	i := 0
	for ; i < len(it.tt); i++ {
		seqtt, ok := d.doADB(v, &it.tt[i], true)
		if !ok {
			return nil, 0, 0
		}
		if length == 0 {
			break
		}
		if checkEOC(in[p : p+length]) {
			if !seqEOC {
				d.raise("ASN1_R_UNEXPECTED_EOC")
				return nil, 0, 0
			}
			p += 2
			length -= 2
			seqEOC = false
			break
		}
		// The last field is never OPTIONAL while data remains.
		isopt := i != len(it.tt)-1 && seqtt.flags&tOptional != 0
		fv, n, ret := d.templateD2I(in[p:p+length], seqtt, isopt, depth)
		if ret == 0 {
			return nil, 0, 0
		}
		if ret == -1 {
			continue
		}
		v.fields[i] = fv
		p += n
		length -= n
	}
	if seqEOC {
		if !checkEOC(in[p : p+length]) {
			d.raise("ASN1_R_MISSING_EOC")
			return nil, 0, 0
		}
		p += 2
		length -= 2
	}
	if !seqNolen && length != 0 {
		d.raise("ASN1_R_SEQUENCE_LENGTH_MISMATCH")
		return nil, 0, 0
	}
	for ; i < len(it.tt); i++ {
		seqtt, ok := d.doADB(v, &it.tt[i], true)
		if !ok {
			return nil, 0, 0
		}
		if seqtt.flags&tOptional == 0 {
			d.raise("ASN1_R_FIELD_MISSING")
			return nil, 0, 0
		}
	}
	if it.post != nil && !it.post(d, v) {
		d.raise("ASN1_R_AUX_ERROR")
		return nil, 0, 0
	}
	v.raw = in[:p]
	return v, p, 1
}

// Port of tasn_utl.c ossl_asn1_do_adb.
func (d *asn1Dec) doADB(v *asn1Value, tt *asn1Template, nullerr bool) (*asn1Template, bool) {
	if tt.flags&tADB == 0 {
		return tt, true
	}
	sel := v.fields[tt.adbField]
	if sel != nil {
		if t, ok := tt.adb[string(sel.data)]; ok {
			return t, true
		}
	}
	if tt.adbDefault != nil {
		return tt.adbDefault, true
	}
	if nullerr {
		d.raise("ASN1_R_UNSUPPORTED_ANY_DEFINED_BY_TYPE")
	}
	return nil, false
}

// Port of tasn_dec.c asn1_template_ex_d2i (the EXPLICIT tag).
func (d *asn1Dec) templateD2I(in []byte, tt *asn1Template, opt bool, depth int) (*asn1Value, int, int) {
	if tt.flags&tExplicit == 0 {
		return d.noexpD2I(in, tt, opt, depth)
	}
	r, ret := d.checkTLen(in, tt.tag, classContext, opt)
	if ret == 0 {
		d.nested()
		return nil, 0, 0
	}
	if ret == -1 {
		return nil, 0, -1
	}
	if !r.cst {
		d.raise("ASN1_R_EXPLICIT_TAG_NOT_CONSTRUCTED")
		return nil, 0, 0
	}
	v, n, ret := d.noexpD2I(in[r.hdr:r.hdr+r.plen], tt, false, depth)
	if ret <= 0 {
		d.nested()
		return nil, 0, 0
	}
	p, length := r.hdr+n, r.plen-n
	if r.inf {
		if !checkEOC(in[p : p+length]) {
			d.raise("ASN1_R_MISSING_EOC")
			return nil, 0, 0
		}
		p += 2
	} else if length != 0 {
		d.raise("ASN1_R_EXPLICIT_LENGTH_MISMATCH")
		return nil, 0, 0
	}
	return v, p, 1
}

// Port of tasn_dec.c asn1_template_noexp_d2i.
func (d *asn1Dec) noexpD2I(in []byte, tt *asn1Template, opt bool, depth int) (*asn1Value, int, int) {
	if tt.flags&(tSetOf|tSeqOf) != 0 {
		sktag, skclass := tagSequence, classUniversal
		if tt.flags&tImplicit != 0 {
			sktag, skclass = tt.tag, classContext
		} else if tt.flags&tSetOf != 0 {
			sktag = tagSet
		}
		r, ret := d.checkTLen(in, sktag, skclass, opt)
		if ret == 0 {
			d.nested()
			return nil, 0, 0
		}
		if ret == -1 {
			return nil, 0, -1
		}
		p, length, skEOC := r.hdr, r.plen, r.inf
		v := &asn1Value{utype: sktag}
		for length > 0 {
			if checkEOC(in[p : p+length]) {
				if !skEOC {
					d.raise("ASN1_R_UNEXPECTED_EOC")
					return nil, 0, 0
				}
				p += 2
				skEOC = false
				break
			}
			iv, n, ret := d.itemD2I(in[p:p+length], tt.item, -1, 0, false, depth)
			if ret <= 0 {
				d.nested()
				return nil, 0, 0
			}
			v.items = append(v.items, iv)
			p += n
			length -= n
		}
		if skEOC {
			d.raise("ASN1_R_MISSING_EOC")
			return nil, 0, 0
		}
		return v, p, 1
	}
	tag, aclass := -1, 0
	if tt.flags&tImplicit != 0 {
		tag, aclass = tt.tag, classContext
	}
	v, n, ret := d.itemD2I(in, tt.item, tag, aclass, opt, depth)
	if ret == 0 {
		d.nested()
	}
	return v, n, ret
}

// Port of tasn_dec.c asn1_d2i_ex_primitive.
func (d *asn1Dec) primitiveD2I(in []byte, it *asn1Item, tag, aclass int, opt bool) (*asn1Value, int, int) {
	utype := it.utype
	if it.kind == itemMString {
		utype, tag = tag, -1
	}
	if utype == tagAny {
		if tag >= 0 {
			d.raise("ASN1_R_ILLEGAL_TAGGED_ANY")
			return nil, 0, 0
		}
		if opt {
			d.raise("ASN1_R_ILLEGAL_OPTIONAL_ANY")
			return nil, 0, 0
		}
		r, ret := d.checkTLen(in, -1, 0, false)
		if ret == 0 {
			d.nested()
			return nil, 0, 0
		}
		utype = r.tag
		if r.class != classUniversal {
			utype = tagOther
		}
	}
	if tag == -1 {
		tag, aclass = utype, classUniversal
	}
	r, ret := d.checkTLen(in, tag, aclass, opt)
	if ret == 0 {
		d.nested()
		return nil, 0, 0
	}
	if ret == -1 {
		return nil, 0, -1
	}
	p := r.hdr
	var cont []byte
	switch {
	case utype == tagSequence || utype == tagSet || utype == tagOther:
		// Left in encoded form.
		if utype != tagOther && !r.cst {
			d.raise("ASN1_R_TYPE_NOT_CONSTRUCTED")
			return nil, 0, 0
		}
		if r.inf {
			n, ok := d.findEnd(in[p : p+r.plen])
			if !ok {
				return nil, 0, 0
			}
			p += n
		} else {
			p += r.plen
		}
		cont = in[:p]
	case r.cst:
		switch utype {
		case tagNull, tagBoolean, tagObject, tagInteger, tagEnumerated:
			d.raise("ASN1_R_TYPE_NOT_PRIMITIVE")
			return nil, 0, 0
		}
		buf, n, ok := d.collect(in[p:p+r.plen], r.inf, 0)
		if !ok {
			return nil, 0, 0
		}
		cont = buf
		p += n
	default:
		cont = in[p : p+r.plen]
		p += r.plen
	}
	v, ok := d.c2i(cont, utype, it)
	if !ok {
		return nil, 0, 0
	}
	v.raw = in[:p]
	return v, p, 1
}

// Port of tasn_dec.c asn1_find_end for indefinite-length content.
func (d *asn1Dec) findEnd(in []byte) (int, bool) {
	expected := 1
	p, length := 0, len(in)
	for length > 0 {
		if checkEOC(in[p : p+length]) {
			p += 2
			if expected--; expected == 0 {
				break
			}
			length -= 2
			continue
		}
		q := p
		r, ret := d.checkTLen(in[p:p+length], -1, 0, false)
		if ret == 0 {
			d.nested()
			return 0, false
		}
		p += r.hdr
		if r.inf {
			expected++
		} else {
			p += r.plen
		}
		length -= p - q
	}
	if expected != 0 {
		d.raise("ASN1_R_MISSING_EOC")
		return 0, false
	}
	return p, true
}

// Port of tasn_dec.c asn1_collect: the content of a constructed string.
// OpenSSL passes tag -1 here, so the inner tags are not checked.
func (d *asn1Dec) collect(in []byte, inf bool, depth int) ([]byte, int, bool) {
	var buf []byte
	p, length := 0, len(in)
	for length > 0 {
		q := p
		if checkEOC(in[p : p+length]) {
			if !inf {
				d.raise("ASN1_R_UNEXPECTED_EOC")
				return nil, 0, false
			}
			p += 2
			inf = false
			break
		}
		r, ret := d.checkTLen(in[p:p+length], -1, classUniversal, false)
		if ret == 0 {
			d.nested()
			return nil, 0, false
		}
		p += r.hdr
		if r.cst {
			if depth >= 5 { // ASN1_MAX_STRING_NEST
				d.raise("ASN1_R_NESTED_ASN1_STRING")
				return nil, 0, false
			}
			sub, n, ok := d.collect(in[p:p+r.plen], r.inf, depth+1)
			if !ok {
				return nil, 0, false
			}
			buf = append(buf, sub...)
			p += n
		} else {
			buf = append(buf, in[p:p+r.plen]...)
			p += r.plen
		}
		length -= p - q
	}
	if inf {
		d.raise("ASN1_R_MISSING_EOC")
		return nil, 0, false
	}
	return buf, p, true
}

// c2iInteger ports a_int.c c2i_ibuf's checks: the content length without
// the pad byte, or false (with the reason raised) for a malformed INTEGER.
func (d *asn1Dec) c2iInteger(cont []byte) (int, bool, bool) {
	if len(cont) == 0 {
		d.raise("ASN1_R_ILLEGAL_ZERO_CONTENT")
		return 0, false, false
	}
	neg := cont[0]&0x80 != 0
	if len(cont) == 1 {
		return 1, neg, true
	}
	pad := cont[0] == 0
	if cont[0] == 0xFF {
		pad = false
		for _, c := range cont[1:] {
			pad = pad || c != 0
		}
	}
	if pad && neg == (cont[1]&0x80 != 0) {
		d.raise("ASN1_R_ILLEGAL_PADDING")
		return 0, false, false
	}
	n := len(cont)
	if pad {
		n--
	}
	return n, neg, true
}

// Port of tasn_dec.c asn1_ex_c2i and the c2i functions it calls
// (a_object.c ossl_c2i_ASN1_OBJECT, a_bitstr.c ossl_c2i_ASN1_BIT_STRING,
// a_int.c ossl_c2i_ASN1_INTEGER, x_int64.c uint32_c2i, x_long.c long_c2i,
// x_bignum.c bn_c2i).
func (d *asn1Dec) c2i(cont []byte, utype int, it *asn1Item) (*asn1Value, bool) {
	v := &asn1Value{utype: utype, data: cont}
	switch it.prim {
	case primBignum:
		return v, true
	case primInt32:
		// Zero-length content is read as 0 (long_c2i compatibility).
		if len(cont) == 0 {
			return v, true
		}
		n, neg, ok := d.c2iInteger(cont)
		if !ok {
			return nil, false
		}
		if n > 8 {
			d.raise("ASN1_R_TOO_LARGE")
			return nil, false
		}
		mag := twosMagnitude(cont, neg)
		if neg {
			if mag.Cmp(big.NewInt(1<<31)) > 0 {
				d.raise("ASN1_R_TOO_SMALL")
				return nil, false
			}
		} else if mag.Cmp(big.NewInt(math.MaxInt32)) > 0 {
			d.raise("ASN1_R_TOO_LARGE")
			return nil, false
		}
		v.data, v.neg = mag.Bytes(), neg
		return v, true
	}
	switch utype {
	case tagObject:
		if len(cont) == 0 || cont[len(cont)-1]&0x80 != 0 {
			d.raise("ASN1_R_INVALID_OBJECT_ENCODING")
			return nil, false
		}
		if objNames[string(cont)] == "" {
			for i, c := range cont {
				if c == 0x80 && (i == 0 || cont[i-1]&0x80 == 0) {
					d.raise("ASN1_R_INVALID_OBJECT_ENCODING")
					return nil, false
				}
			}
		}
	case tagNull:
		if len(cont) != 0 {
			d.raise("ASN1_R_NULL_IS_WRONG_LENGTH")
			return nil, false
		}
	case tagBoolean:
		if len(cont) != 1 {
			d.raise("ASN1_R_BOOLEAN_IS_WRONG_LENGTH")
			return nil, false
		}
	case tagBitString:
		if len(cont) < 1 {
			d.raise("ASN1_R_STRING_TOO_SHORT")
			return nil, false
		}
		unused := int(cont[0])
		if unused > 7 {
			d.raise("ASN1_R_INVALID_BIT_STRING_BITS_LEFT")
			return nil, false
		}
		data := append([]byte(nil), cont[1:]...)
		if len(data) > 0 {
			data[len(data)-1] &= byte(0xFF << unused)
		}
		v.data, v.bits = data, unused
	case tagInteger, tagEnumerated:
		_, neg, ok := d.c2iInteger(cont)
		if !ok {
			return nil, false
		}
		v.neg = neg
	case tagBMPString:
		if len(cont)&1 != 0 {
			d.raise("ASN1_R_BMPSTRING_IS_WRONG_LENGTH")
			return nil, false
		}
	case tagUniversalString:
		if len(cont)&3 != 0 {
			d.raise("ASN1_R_UNIVERSALSTRING_IS_WRONG_LENGTH")
			return nil, false
		}
	case tagGeneralizedTime:
		if len(cont) < 15 {
			d.raise("ASN1_R_GENERALIZEDTIME_IS_TOO_SHORT")
			return nil, false
		}
	case tagUTCTime:
		if len(cont) < 13 {
			d.raise("ASN1_R_UTCTIME_IS_TOO_SHORT")
			return nil, false
		}
	}
	return v, true
}

// twosMagnitude is the absolute value of a two's-complement INTEGER.
func twosMagnitude(cont []byte, neg bool) *big.Int {
	n := new(big.Int).SetBytes(cont)
	if neg {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(cont))))
		n.Neg(n)
	}
	return n
}

// integerValue is an ASN1_INTEGER's value (ASN1_INTEGER_to_BN keeps the
// sign).
func integerValue(v *asn1Value) *big.Int {
	n := twosMagnitude(v.data, v.neg)
	if v.neg {
		n.Neg(n)
	}
	return n
}

// asn1IntegerGet ports ASN1_INTEGER_get without the error queue: -1 when
// the value does not fit a long (or is -1).
func asn1IntegerGet(v *asn1Value) int64 {
	n := integerValue(v)
	if !n.IsInt64() || (cLongSize == 4 && (n.Int64() > math.MaxInt32 || n.Int64() < math.MinInt32)) {
		return -1
	}
	return n.Int64()
}

// integerGet ports ASN1_INTEGER_get with its errors: asn1_get_int64
// raises TOO_LARGE for more than 8 content bytes or a positive value above
// INT64_MAX, TOO_SMALL for a negative one below INT64_MIN.
func (d *asn1Dec) integerGet(v *asn1Value) int64 {
	mag := twosMagnitude(v.data, v.neg)
	if len(mag.Bytes()) > 8 {
		d.raise("ASN1_R_TOO_LARGE")
		return -1
	}
	if v.neg {
		if mag.Cmp(new(big.Int).Lsh(big.NewInt(1), 63)) > 0 {
			d.raise("ASN1_R_TOO_SMALL")
			return -1
		}
	} else if !mag.IsInt64() {
		d.raise("ASN1_R_TOO_LARGE")
		return -1
	}
	return asn1IntegerGet(v)
}

// B_ASN1_* bits (include/openssl/asn1.h) and tasn_dec.c tag2bit.
const (
	bNumericString   = 0x0001
	bPrintableString = 0x0002
	bT61String       = 0x0004
	bVideotexString  = 0x0008
	bIA5String       = 0x0010
	bGraphicString   = 0x0020
	bVisibleString   = 0x0040
	bGeneralString   = 0x0080
	bUniversalString = 0x0100
	bOctetString     = 0x0200
	bBitString       = 0x0400
	bBMPString       = 0x0800
	bUnknown         = 0x1000
	bUTF8String      = 0x2000
	bUTCTime         = 0x4000
	bGeneralizedTime = 0x8000
	bSequence        = 0x10000

	bPrintable = bNumericString | bPrintableString | bT61String | bIA5String | bBitString |
		bUniversalString | bBMPString | bUTF8String | bSequence | bUnknown
	bTime = bUTCTime | bGeneralizedTime
	// x_name.c ASN1_MASK_CANON
	bCanon = bUTF8String | bBMPString | bUniversalString | bPrintableString | bT61String | bIA5String | bVisibleString
)

var tag2bitTable = [31]uint32{
	0, 0, 0, bBitString, bOctetString, 0, 0, bUnknown,
	bUnknown, bUnknown, 0, bUnknown, bUTF8String, bUnknown, bUnknown, bUnknown,
	bSequence, 0, bNumericString, bPrintableString, bT61String, bVideotexString, bIA5String, bUTCTime,
	bGeneralizedTime, bGraphicString, bVisibleString, bGeneralString, bUniversalString, bUnknown, bBMPString,
}

func tag2bit(tag int) uint32 {
	if tag < 0 || tag > 30 {
		return 0
	}
	return tag2bitTable[tag]
}

// pubkeyD2I ports x509_pubkey_ex_d2i_ex: X509_PUBKEY_INTERNAL, then an
// opportunistic key decode whose errors are popped (X509_PUBKEY_get0 later
// reports EVP decode error when it found no key). Two outcomes of the
// decode fail the whole d2i instead (ret is still x509_pubkey_decode's 0
// when they "goto end"): an algorithm OID OBJ_obj2txt refuses, which
// leaves nothing queued, and a decoder that did not consume every byte,
// which leaves its decode error queued. decodeSPKIKey clears the mark on
// those paths, as OpenSSL's ERR_clear_last_mark does.
func (d *asn1Dec) pubkeyD2I(in []byte, tag, aclass int, opt bool) (*asn1Value, int, int) {
	v, n, ret := d.itemD2I(in, pubkeyInternalItem, tag, aclass, opt, 0)
	if ret <= 0 {
		return v, n, ret
	}
	d.q.setMark()
	der := in[:n]
	if aclass != classUniversal {
		der = append([]byte{0x30}, in[1:n]...)
	}
	pub, ok := d.env.decodeSPKIKey(v, der)
	if !ok {
		return nil, 0, 0
	}
	d.q.popToMark()
	v.pub = pub
	return v, n, 1
}

// nameD2I ports x509_name_ex_d2i: X509_NAME_INTERNAL, then x509_name_canon,
// whose ASN1_STRING_to_UTF8 rejects malformed strings (raising nothing
// itself but "nested asn1 error" on the way out).
func (d *asn1Dec) nameD2I(in []byte, tag, aclass int, opt bool) (*asn1Value, int, int) {
	if len(in) > 1024*1024 { // X509_NAME_MAX
		in = in[:1024*1024]
	}
	v, n, ret := d.itemD2I(in, nameInternalItem, tag, aclass, opt, 0)
	if ret <= 0 {
		return v, n, ret
	}
	for _, rdn := range v.items {
		for _, entry := range rdn.items {
			if reason := canonString(entry.fields[1]); reason != "" {
				d.raise(reason)
				d.nested()
				return nil, 0, 0
			}
		}
	}
	return v, n, 1
}

// canonString is the reason x_name.c asn1_string_canon (via a_strex.c
// ASN1_STRING_to_UTF8 and a_mbstr.c ASN1_mbstring_ncopy) raises, "" when it
// succeeds. Only UTF8String, BMPString and UniversalString can fail; the
// one-byte types convert whatever their bytes.
func canonString(v *asn1Value) string {
	if tag2bit(v.utype)&bCanon == 0 {
		return ""
	}
	s := v.data
	switch v.utype {
	case tagUTF8String:
		for len(s) > 0 {
			value, n := utf8Getc(s)
			if n < 0 || !unicodeValid(value) {
				return "ASN1_R_INVALID_UTF8STRING"
			}
			s = s[n:]
		}
	case tagBMPString:
		for i := 0; i+1 < len(s); i += 2 {
			if !unicodeValid(uint32(s[i])<<8 | uint32(s[i+1])) {
				return "ASN1_R_ILLEGAL_CHARACTERS"
			}
		}
	case tagUniversalString:
		for i := 0; i+3 < len(s); i += 4 {
			if !unicodeValid(uint32(s[i])<<24 | uint32(s[i+1])<<16 | uint32(s[i+2])<<8 | uint32(s[i+3])) {
				return "ASN1_R_ILLEGAL_CHARACTERS"
			}
		}
	}
	return ""
}

func unicodeValid(v uint32) bool {
	return v <= 0x10FFFF && (v < 0xD800 || v > 0xDFFF)
}

// Port of a_utf8.c UTF8_getc: the value and its length, n < 0 when malformed.
func utf8Getc(s []byte) (uint32, int) {
	c := s[0]
	cont := func(k int) bool {
		for _, b := range s[1:k] {
			if b&0xC0 != 0x80 {
				return false
			}
		}
		return true
	}
	switch {
	case c&0x80 == 0:
		return uint32(c), 1
	case c&0xE0 == 0xC0:
		if len(s) < 2 || !cont(2) {
			return 0, -1
		}
		v := uint32(c&0x1F)<<6 | uint32(s[1]&0x3F)
		if v < 0x80 {
			return 0, -1
		}
		return v, 2
	case c&0xF0 == 0xE0:
		if len(s) < 3 || !cont(3) {
			return 0, -1
		}
		v := uint32(c&0x0F)<<12 | uint32(s[1]&0x3F)<<6 | uint32(s[2]&0x3F)
		if v < 0x800 || (v >= 0xD800 && v <= 0xDFFF) {
			return 0, -1
		}
		return v, 3
	case c&0xF8 == 0xF0:
		if len(s) < 4 || !cont(4) {
			return 0, -1
		}
		v := uint32(c&0x07)<<18 | uint32(s[1]&0x3F)<<12 | uint32(s[2]&0x3F)<<6 | uint32(s[3]&0x3F)
		if v < 0x10000 || v >= 0x110000 {
			return 0, -1
		}
		return v, 4
	}
	return 0, -1
}
