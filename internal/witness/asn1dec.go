package witness

import (
	"fmt"
	"math"
	"runtime"
)

// A port of OpenSSL's table-driven DER/BER decoder (crypto/asn1/tasn_dec.c,
// asn1_lib.c and the c2i functions it calls, OpenSSL 3.5/3.6) for the
// templates Node's createPublicKey and createPrivateKey reach. OpenSSL
// raises an error at the point of failure and its callers add "nested asn1
// error" on the way out; Node reports the oldest error in the queue, so the
// message is the first reason raised (asn1Dec.first).
//
// The TLC header cache (ASN1_TLC) is left out: it only memoises a header
// already parsed at the same position and length, so it never changes the
// outcome or the first error.

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

// asn1Err is the Node message of an ASN1 library error (ERR_LIB_ASN1 = 13).
func asn1Err(reason int, text string) string {
	return fmt.Sprintf("error:%08X:asn1 encoding routines::%s", 13<<23|reason, text)
}

// ASN1_R_* reasons (crypto/asn1/asn1_err.c) the templates below can raise.
var (
	errAsn1TooSmall            = asn1Err(224, "too small")
	errAsn1HeaderTooLong       = asn1Err(123, "header too long")
	errAsn1TooLong             = asn1Err(155, "too long")
	errAsn1BadObjectHeader     = asn1Err(102, "bad object header")
	errAsn1WrongTag            = asn1Err(168, "wrong tag")
	errAsn1SeqNotConstructed   = asn1Err(149, "sequence not constructed")
	errAsn1UnexpectedEOC       = asn1Err(159, "unexpected eoc")
	errAsn1MissingEOC          = asn1Err(137, "missing eoc")
	errAsn1SeqLengthMismatch   = asn1Err(148, "sequence length mismatch")
	errAsn1FieldMissing        = asn1Err(121, "field missing")
	errAsn1AuxError            = asn1Err(100, "aux error")
	errAsn1ExplicitNotCons     = asn1Err(120, "explicit tag not constructed")
	errAsn1ExplicitLenMismatch = asn1Err(119, "explicit length mismatch")
	errAsn1TypeNotConstructed  = asn1Err(156, "type not constructed")
	errAsn1TypeNotPrimitive    = asn1Err(195, "type not primitive")
	errAsn1NestedString        = asn1Err(197, "nested asn1 string")
	errAsn1MstringNotUniversal = asn1Err(139, "mstring not universal")
	errAsn1MstringWrongTag     = asn1Err(140, "mstring wrong tag")
	errAsn1InvalidObject       = asn1Err(216, "invalid object encoding")
	errAsn1NullWrongLength     = asn1Err(144, "null is wrong length")
	errAsn1BooleanWrongLength  = asn1Err(106, "boolean is wrong length")
	errAsn1StringTooShort      = asn1Err(152, "string too short")
	errAsn1BitsLeft            = asn1Err(220, "invalid bit string bits left")
	errAsn1ZeroContent         = asn1Err(222, "illegal zero content")
	errAsn1IllegalPadding      = asn1Err(221, "illegal padding")
	errAsn1BMPWrongLength      = asn1Err(214, "bmpstring is wrong length")
	errAsn1UniversalWrongLen   = asn1Err(215, "universalstring is wrong length")
	errAsn1GenTimeTooShort     = asn1Err(232, "generalizedtime is too short")
	errAsn1UTCTimeTooShort     = asn1Err(233, "utctime is too short")
	errAsn1InvalidUTF8         = asn1Err(134, "invalid utf8string")
	errAsn1IllegalCharacters   = asn1Err(124, "illegal characters")
	errAsn1IllegalTaggedAny    = asn1Err(127, "illegal tagged any")
	errAsn1IllegalOptionalAny  = asn1Err(126, "illegal optional any")
	errAsn1IllegalOptions      = asn1Err(170, "illegal options on item template")
	errAsn1BadTemplate         = asn1Err(230, "bad template")
	// ERR_R_NESTED_ASN1_ERROR is a common reason: callers raise it after the
	// real error, so it is never the message.
	errAsn1Nested = "error:0688010A:asn1 encoding routines::nested asn1 error"
	// EVP_R_DECODE_ERROR from X509_PUBKEY_get0: the SPKI structure decoded
	// but its key did not.
	errEVPDecode = "error:03000072:digital envelope routines::decode error"
)

// C long is 32 bits on Windows (LLP64), which bounds asn1_get_length.
var cLongSize, cLongMax = func() (int, uint64) {
	if runtime.GOOS == "windows" {
		return 4, math.MaxInt32
	}
	return 8, math.MaxInt64
}()

// Item kinds (ASN1_ITYPE_*, with the two EXTERN items and ITEM_TEMPLATE
// split out).
const (
	itemPrimitive = iota
	itemMString
	itemSequence
	itemTemplate // ASN1_ITEM_TEMPLATE: a PRIMITIVE item holding one template
	itemPubkey   // X509_PUBKEY (crypto/x509/x_pubkey.c x509_pubkey_ex_d2i_ex)
	itemName     // X509_NAME (crypto/x509/x_name.c x509_name_ex_d2i)
)

// Template flags (ASN1_TFLG_*).
const (
	tOptional = 1 << iota
	tExplicit
	tImplicit
	tSetOf
	tSeqOf
)

type asn1Item struct {
	kind   int
	utype  int    // primitive: V_ASN1_* (tagAny for ASN1_ANY)
	mask   uint32 // mstring: B_ASN1_* bits
	bignum bool   // BIGNUM's prim_c2i: BN_bin2bn, no content checks
	tt     []asn1Template
	post   func(*asn1Value) bool // ASN1_OP_D2I_POST
}

type asn1Template struct {
	flags int
	tag   int
	item  *asn1Item
}

// asn1Value is a decoded item: a primitive's type and content, a
// SEQUENCE's fields (nil when an OPTIONAL one is absent), a SET OF's items.
type asn1Value struct {
	utype  int
	data   []byte // content octets; a bit string's without the unused-bits octet, masked
	fields []*asn1Value
	items  []*asn1Value
	pub    *keyObject // X509_PUBKEY: the key OpenSSL decoded opportunistically, nil if none
}

type asn1Dec struct {
	first string
}

func (d *asn1Dec) raise(reason string) {
	if d.first == "" {
		d.first = reason
	}
}

// d2i ports ASN1_item_d2i: the first error raised is returned as the reason.
// Bytes after the top-level object are ignored, as in OpenSSL.
func d2i(it *asn1Item, der []byte) (*asn1Value, string) {
	d := &asn1Dec{}
	v, _, ret := d.itemD2I(der, it, -1, 0, false)
	if ret <= 0 {
		if d.first == "" {
			d.first = errAsn1Nested
		}
		return nil, d.first
	}
	return v, ""
}

// Port of asn1_lib.c ASN1_get_object: ret carries 0x20 (constructed),
// 0x01 (indefinite) and 0x80 (error).
func (d *asn1Dec) getObject(in []byte) (ret, hdr int, plen int64, tag, class int) {
	omax := len(in)
	if omax <= 0 {
		d.raise(errAsn1TooSmall)
		return 0x80, 0, 0, 0, 0
	}
	fail := func() (int, int, int64, int, int) {
		d.raise(errAsn1HeaderTooLong)
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
		d.raise(errAsn1TooLong)
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
	if max < i+1 {
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
		d.raise(errAsn1TooSmall)
		return tlen{}, 0
	}
	i, hdr, plen, ptag, pclass := d.getObject(in)
	if i&0x80 != 0 {
		d.raise(errAsn1BadObjectHeader)
		return tlen{}, 0
	}
	if exptag >= 0 && (exptag != ptag || expclass != pclass) {
		if opt {
			return tlen{}, -1
		}
		d.raise(errAsn1WrongTag)
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
func (d *asn1Dec) itemD2I(in []byte, it *asn1Item, tag, aclass int, opt bool) (*asn1Value, int, int) {
	if len(in) <= 0 {
		d.raise(errAsn1TooSmall)
		return nil, 0, 0
	}
	switch it.kind {
	case itemTemplate:
		if tag != -1 || opt {
			d.raise(errAsn1IllegalOptions)
			return nil, 0, 0
		}
		return d.templateD2I(in, &it.tt[0], opt)
	case itemPrimitive:
		return d.primitiveD2I(in, it, tag, aclass, opt)
	case itemMString:
		if tag != -1 {
			d.raise(errAsn1BadTemplate)
			return nil, 0, 0
		}
		r, ret := d.checkTLen(in, -1, 0, true)
		if ret == 0 {
			d.raise(errAsn1Nested)
			return nil, 0, 0
		}
		if r.class != classUniversal {
			if opt {
				return nil, 0, -1
			}
			d.raise(errAsn1MstringNotUniversal)
			return nil, 0, 0
		}
		if tag2bit(r.tag)&it.mask == 0 {
			if opt {
				return nil, 0, -1
			}
			d.raise(errAsn1MstringWrongTag)
			return nil, 0, 0
		}
		return d.primitiveD2I(in, it, r.tag, 0, false)
	case itemPubkey:
		return d.pubkeyD2I(in, tag, aclass, opt)
	case itemName:
		return d.nameD2I(in, tag, aclass, opt)
	}
	return d.sequenceD2I(in, it, tag, aclass, opt)
}

// The SEQUENCE case of asn1_item_embed_d2i.
func (d *asn1Dec) sequenceD2I(in []byte, it *asn1Item, tag, aclass int, opt bool) (*asn1Value, int, int) {
	if tag == -1 {
		tag, aclass = tagSequence, classUniversal
	}
	r, ret := d.checkTLen(in, tag, aclass, opt)
	if ret == 0 {
		d.raise(errAsn1Nested)
		return nil, 0, 0
	}
	if ret == -1 {
		return nil, 0, -1
	}
	seqEOC := r.inf
	seqNolen := seqEOC
	if !r.cst {
		d.raise(errAsn1SeqNotConstructed)
		return nil, 0, 0
	}
	p, length := r.hdr, r.plen
	v := &asn1Value{utype: tagSequence, fields: make([]*asn1Value, len(it.tt))}
	i := 0
	for ; i < len(it.tt); i++ {
		if length == 0 {
			break
		}
		if checkEOC(in[p : p+length]) {
			if !seqEOC {
				d.raise(errAsn1UnexpectedEOC)
				return nil, 0, 0
			}
			p += 2
			length -= 2
			seqEOC = false
			break
		}
		// The last field is never OPTIONAL while data remains.
		isopt := i != len(it.tt)-1 && it.tt[i].flags&tOptional != 0
		fv, n, ret := d.templateD2I(in[p:p+length], &it.tt[i], isopt)
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
			d.raise(errAsn1MissingEOC)
			return nil, 0, 0
		}
		p += 2
		length -= 2
	}
	if !seqNolen && length != 0 {
		d.raise(errAsn1SeqLengthMismatch)
		return nil, 0, 0
	}
	for ; i < len(it.tt); i++ {
		if it.tt[i].flags&tOptional == 0 {
			d.raise(errAsn1FieldMissing)
			return nil, 0, 0
		}
	}
	if it.post != nil && !it.post(v) {
		d.raise(errAsn1AuxError)
		return nil, 0, 0
	}
	return v, p, 1
}

// Port of tasn_dec.c asn1_template_ex_d2i (the EXPLICIT tag).
func (d *asn1Dec) templateD2I(in []byte, tt *asn1Template, opt bool) (*asn1Value, int, int) {
	if tt.flags&tExplicit == 0 {
		return d.noexpD2I(in, tt, opt)
	}
	r, ret := d.checkTLen(in, tt.tag, classContext, opt)
	if ret == 0 {
		d.raise(errAsn1Nested)
		return nil, 0, 0
	}
	if ret == -1 {
		return nil, 0, -1
	}
	if !r.cst {
		d.raise(errAsn1ExplicitNotCons)
		return nil, 0, 0
	}
	v, n, ret := d.noexpD2I(in[r.hdr:r.hdr+r.plen], tt, false)
	if ret <= 0 {
		d.raise(errAsn1Nested)
		return nil, 0, 0
	}
	p, length := r.hdr+n, r.plen-n
	if r.inf {
		if !checkEOC(in[p : p+length]) {
			d.raise(errAsn1MissingEOC)
			return nil, 0, 0
		}
		p += 2
	} else if length != 0 {
		d.raise(errAsn1ExplicitLenMismatch)
		return nil, 0, 0
	}
	return v, p, 1
}

// Port of tasn_dec.c asn1_template_noexp_d2i.
func (d *asn1Dec) noexpD2I(in []byte, tt *asn1Template, opt bool) (*asn1Value, int, int) {
	if tt.flags&(tSetOf|tSeqOf) != 0 {
		sktag, skclass := tagSequence, classUniversal
		if tt.flags&tImplicit != 0 {
			sktag, skclass = tt.tag, classContext
		} else if tt.flags&tSetOf != 0 {
			sktag = tagSet
		}
		r, ret := d.checkTLen(in, sktag, skclass, opt)
		if ret == 0 {
			d.raise(errAsn1Nested)
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
					d.raise(errAsn1UnexpectedEOC)
					return nil, 0, 0
				}
				p += 2
				skEOC = false
				break
			}
			iv, n, ret := d.itemD2I(in[p:p+length], tt.item, -1, 0, false)
			if ret <= 0 {
				d.raise(errAsn1Nested)
				return nil, 0, 0
			}
			v.items = append(v.items, iv)
			p += n
			length -= n
		}
		if skEOC {
			d.raise(errAsn1MissingEOC)
			return nil, 0, 0
		}
		return v, p, 1
	}
	tag, aclass := -1, 0
	if tt.flags&tImplicit != 0 {
		tag, aclass = tt.tag, classContext
	}
	v, n, ret := d.itemD2I(in, tt.item, tag, aclass, opt)
	if ret == 0 {
		d.raise(errAsn1Nested)
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
			d.raise(errAsn1IllegalTaggedAny)
			return nil, 0, 0
		}
		if opt {
			d.raise(errAsn1IllegalOptionalAny)
			return nil, 0, 0
		}
		r, ret := d.checkTLen(in, -1, 0, false)
		if ret == 0 {
			d.raise(errAsn1Nested)
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
		d.raise(errAsn1Nested)
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
			d.raise(errAsn1TypeNotConstructed)
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
			d.raise(errAsn1TypeNotPrimitive)
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
			d.raise(errAsn1Nested)
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
		d.raise(errAsn1MissingEOC)
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
				d.raise(errAsn1UnexpectedEOC)
				return nil, 0, false
			}
			p += 2
			inf = false
			break
		}
		r, ret := d.checkTLen(in[p:p+length], -1, classUniversal, false)
		if ret == 0 {
			d.raise(errAsn1Nested)
			return nil, 0, false
		}
		p += r.hdr
		if r.cst {
			if depth >= 5 { // ASN1_MAX_STRING_NEST
				d.raise(errAsn1NestedString)
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
		d.raise(errAsn1MissingEOC)
		return nil, 0, false
	}
	return buf, p, true
}

// Port of tasn_dec.c asn1_ex_c2i and the c2i functions it calls
// (a_object.c ossl_c2i_ASN1_OBJECT, a_bitstr.c ossl_c2i_ASN1_BIT_STRING,
// a_int.c c2i_ibuf).
func (d *asn1Dec) c2i(cont []byte, utype int, it *asn1Item) (*asn1Value, bool) {
	v := &asn1Value{utype: utype, data: cont}
	if it.bignum {
		return v, true
	}
	switch utype {
	case tagObject:
		if len(cont) == 0 || cont[len(cont)-1]&0x80 != 0 {
			d.raise(errAsn1InvalidObject)
			return nil, false
		}
		for i, c := range cont {
			if c == 0x80 && (i == 0 || cont[i-1]&0x80 == 0) {
				d.raise(errAsn1InvalidObject)
				return nil, false
			}
		}
	case tagNull:
		if len(cont) != 0 {
			d.raise(errAsn1NullWrongLength)
			return nil, false
		}
	case tagBoolean:
		if len(cont) != 1 {
			d.raise(errAsn1BooleanWrongLength)
			return nil, false
		}
	case tagBitString:
		if len(cont) < 1 {
			d.raise(errAsn1StringTooShort)
			return nil, false
		}
		unused := int(cont[0])
		if unused > 7 {
			d.raise(errAsn1BitsLeft)
			return nil, false
		}
		data := append([]byte(nil), cont[1:]...)
		if len(data) > 0 {
			data[len(data)-1] &= byte(0xFF << unused)
		}
		v.data = data
	case tagInteger, tagEnumerated:
		if len(cont) == 0 {
			d.raise(errAsn1ZeroContent)
			return nil, false
		}
		if len(cont) > 1 {
			neg := cont[0] & 0x80
			pad := cont[0] == 0
			if cont[0] == 0xFF {
				for _, c := range cont[1:] {
					pad = pad || c != 0
				}
			}
			if pad && neg == cont[1]&0x80 {
				d.raise(errAsn1IllegalPadding)
				return nil, false
			}
		}
	case tagBMPString:
		if len(cont)&1 != 0 {
			d.raise(errAsn1BMPWrongLength)
			return nil, false
		}
	case tagUniversalString:
		if len(cont)&3 != 0 {
			d.raise(errAsn1UniversalWrongLen)
			return nil, false
		}
	case tagGeneralizedTime:
		if len(cont) < 15 {
			d.raise(errAsn1GenTimeTooShort)
			return nil, false
		}
	case tagUTCTime:
		if len(cont) < 13 {
			d.raise(errAsn1UTCTimeTooShort)
			return nil, false
		}
	}
	return v, true
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
// opportunistic key decode whose failure is not an error (the errors are
// popped; X509_PUBKEY_get0 later reports EVP decode error).
func (d *asn1Dec) pubkeyD2I(in []byte, tag, aclass int, opt bool) (*asn1Value, int, int) {
	v, n, ret := d.itemD2I(in, pubkeyInternalItem, tag, aclass, opt)
	if ret <= 0 {
		return v, n, ret
	}
	alg := v.fields[0]
	if key, ok := decodePublicKey(alg.fields[0].data, alg.fields[1], v.fields[1].data); ok {
		v.pub = &key
	}
	return v, n, 1
}

// nameD2I ports x509_name_ex_d2i: X509_NAME_INTERNAL, then x509_name_canon,
// whose ASN1_STRING_to_UTF8 rejects malformed strings.
func (d *asn1Dec) nameD2I(in []byte, tag, aclass int, opt bool) (*asn1Value, int, int) {
	if len(in) > 1024*1024 { // X509_NAME_MAX
		in = in[:1024*1024]
	}
	v, n, ret := d.itemD2I(in, nameInternalItem, tag, aclass, opt)
	if ret <= 0 {
		return v, n, ret
	}
	for _, rdn := range v.items {
		for _, entry := range rdn.items {
			if reason := canonString(entry.fields[1]); reason != "" {
				d.raise(reason)
				d.raise(errAsn1Nested)
				return nil, 0, 0
			}
		}
	}
	return v, n, 1
}

// canonString is the failure reason of x_name.c asn1_string_canon (via
// a_strex.c ASN1_STRING_to_UTF8 and a_mbstr.c ASN1_mbstring_ncopy), "" when
// it succeeds. Only UTF8String, BMPString and UniversalString can fail; the
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
				return errAsn1InvalidUTF8
			}
			s = s[n:]
		}
	case tagBMPString:
		for i := 0; i+1 < len(s); i += 2 {
			if !unicodeValid(uint32(s[i])<<8 | uint32(s[i+1])) {
				return errAsn1IllegalCharacters
			}
		}
	case tagUniversalString:
		for i := 0; i+3 < len(s); i += 4 {
			if !unicodeValid(uint32(s[i])<<24 | uint32(s[i+1])<<16 | uint32(s[i+2])<<8 | uint32(s[i+3])) {
				return errAsn1IllegalCharacters
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

// The templates (crypto/asn1/x_algor.c, x_sig.c, p8_pkey.c,
// crypto/x509/x_pubkey.c, x_attrib.c, x_name.c, x_exten.c, x_x509.c,
// x_val.c, crypto/rsa/rsa_asn1.c).
func prim(utype int) *asn1Item { return &asn1Item{kind: itemPrimitive, utype: utype} }

func seq(tt ...asn1Template) *asn1Item { return &asn1Item{kind: itemSequence, tt: tt} }

func field(item *asn1Item) asn1Template { return asn1Template{item: item} }

var (
	objectItem      = prim(tagObject)
	integerItem     = prim(tagInteger)
	bitStringItem   = prim(tagBitString)
	octetStringItem = prim(tagOctetString)
	anyItem         = prim(tagAny)
	booleanItem     = prim(tagBoolean)
	bignumItem      = &asn1Item{kind: itemPrimitive, utype: tagInteger, bignum: true}

	algorItem          = seq(field(objectItem), asn1Template{flags: tOptional, item: anyItem})
	pubkeyInternalItem = seq(field(algorItem), field(bitStringItem))
	pubkeyItem         = &asn1Item{kind: itemPubkey}
	x509SigItem        = seq(field(algorItem), field(octetStringItem))
	attributeItem      = seq(field(objectItem), asn1Template{flags: tSetOf, item: anyItem})
	pkcs8Item          = &asn1Item{kind: itemSequence, tt: []asn1Template{
		field(integerItem),
		field(algorItem),
		field(octetStringItem),
		{flags: tSetOf | tImplicit | tOptional, tag: 0, item: attributeItem},
		{flags: tImplicit | tOptional, tag: 1, item: bitStringItem},
	}, post: pkcs8Version}
	rsaPublicKeyItem = seq(field(bignumItem), field(bignumItem))

	nameEntryItem    = seq(field(objectItem), field(&asn1Item{kind: itemMString, mask: bPrintable}))
	nameEntriesItem  = &asn1Item{kind: itemTemplate, tt: []asn1Template{{flags: tSetOf, item: nameEntryItem}}}
	nameInternalItem = &asn1Item{kind: itemTemplate, tt: []asn1Template{{flags: tSeqOf, item: nameEntriesItem}}}
	nameItem         = &asn1Item{kind: itemName}
	timeItem         = &asn1Item{kind: itemMString, mask: bTime}
	extensionItem    = seq(field(objectItem), asn1Template{flags: tOptional, item: booleanItem}, field(octetStringItem))
	x509CinfItem     = seq(
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
)

// pkcs8Version is p8_pkey.c pkey_cb's ASN1_OP_D2I_POST: the version must be
// 0 or 1 (ASN1_INTEGER_get), and a version 0 key has no public key. c2i
// already refused padded integers, so 0 and 1 have one encoding each.
func pkcs8Version(v *asn1Value) bool {
	version := v.fields[0].data
	if len(version) != 1 || version[0] > 1 {
		return false
	}
	return version[0] == 1 || v.fields[4] == nil
}
