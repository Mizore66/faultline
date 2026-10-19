package witness

import (
	"math/big"
	"runtime"
	"strconv"
	"strings"
)

// osslEnv is one Node createPublicKey/createPrivateKey call's view of
// OpenSSL 3.5: its error queue. Every port below raises into it exactly
// where OpenSSL raises, marks and pops where OpenSSL does, so that the
// queue (and with it Node's message and opensslErrorStack) comes out the
// same, including the errors OpenSSL pushes and pops again internally,
// which can overwrite the oldest entries of a full queue.
type osslEnv struct {
	q *errQueue
}

var x86 = runtime.GOARCH == "amd64" || runtime.GOARCH == "386"

// selection bits (OSSL_KEYMGMT_SELECT_*).
const (
	selPrivate   = 0x01
	selPublic    = 0x02
	selDomain    = 0x04
	selOther     = 0x80
	selAllParams = selDomain | selOther
	selKeypair   = selPrivate | selPublic
)

// --- asn1_d2i_read_bio (crypto/asn1/a_d2i_fp.c) over a memory BIO -------

// readDER ports asn1_d2i_read_bio as ossl_read_der and epki2pki call it on
// the decoder's memory BIO holding der: the bytes of one top-level object
// (header read 8 bytes at a time), or false. The errors raised while
// reading are kept (the function's mark is cleared, not popped) except a
// TOO_LONG from ASN1_get_object on a short buffer, which it pops and
// retries.
func (e *osslEnv) readDER(der []byte) ([]byte, bool) {
	const headerSize = 8
	q := e.q
	q.setMark()
	defer q.clearLastMark()
	var b []byte
	pos := 0 // BIO read position
	read := func(n int) int {
		k := min(n, len(der)-pos)
		b = append(b, der[pos:pos+k]...)
		pos += k
		return k
	}
	want := headerSize
	off, eos := 0, 0
	for {
		diff := len(b) - off
		if want >= diff {
			want -= diff
			i := read(want)
			// mem_read of an empty read-only BIO returns 0, not -1.
			if i > 0 && i < want {
				continue
			}
		}
		diff = len(b) - off
		if diff == 0 {
			return nil, false
		}
		d := &asn1Dec{q: q, env: e}
		inf, hdr, slen, tag, _ := d.getObject(b[off:])
		if inf&0x80 != 0 {
			if code := q.peekLast(); code&0x7FFFFF != osslReasons["ASN1_R_TOO_LONG"].reason || code>>23 != libASN1 {
				return nil, false
			}
			q.popToMark()
			q.setMark()
		}
		off += hdr
		switch {
		case inf&1 != 0:
			eos++
			want = headerSize
		case eos > 0 && slen == 0 && tag == tagEOC:
			eos--
			if eos == 0 {
				return b[:off], true
			}
			want = headerSize
		default:
			want = int(slen)
			if int64(want) != slen {
				d.raise("ASN1_R_TOO_LONG")
				return nil, false
			}
			if want > len(b)-off {
				want -= len(b) - off
				if want > 0x7FFFFFFF { // BIO_read takes an int length
					d.raise("ASN1_R_TOO_LONG")
					return nil, false
				}
				if want > len(der)-pos {
					read(want)
					d.raise("ASN1_R_NOT_ENOUGH_DATA")
					return nil, false
				}
				read(want)
			}
			off += int(slen)
			if eos == 0 {
				return b[:off], true
			}
			want = headerSize
		}
	}
}

// --- OBJ_obj2txt --------------------------------------------------------

// obj2txt ports OBJ_obj2txt(buf, OSSL_MAX_NAME_SIZE, obj, 0): the long name
// of a known OID, else the dotted form (truncated to 49 characters).
func obj2txt(oid []byte) (string, bool) {
	if n, ok := objNames[string(oid)]; ok {
		return n, true
	}
	if len(oid) > 586 {
		return "", false
	}
	var sb strings.Builder
	first := true
	for p := 0; p < len(oid); {
		v := new(big.Int)
		for {
			c := oid[p]
			p++
			if p == len(oid) && c&0x80 != 0 {
				return "", false
			}
			v.Lsh(v, 7).Or(v, big.NewInt(int64(c&0x7F)))
			if c&0x80 == 0 {
				break
			}
		}
		if first {
			first = false
			if v.Cmp(big.NewInt(80)) >= 0 {
				sb.WriteString("2")
				v.Sub(v, big.NewInt(80))
			} else {
				i := v.Int64() / 40
				sb.WriteString(strconv.FormatInt(i, 10))
				v.SetInt64(v.Int64() - i*40)
			}
		}
		sb.WriteString(".")
		sb.WriteString(v.String())
	}
	s := sb.String()
	if len(s) > 49 {
		s = s[:49]
	}
	return s, true
}

// --- the decoder framework (crypto/encode_decode/decoder_lib.c) ---------

// decoderKind is a provider decoder implementation.
type decoderKind int

const (
	decPEM2DER  decoderKind = iota // decode_pem2der.c
	decEPKI2PKI                    // decode_epki2pki.c (EncryptedPrivateKeyInfo)
	decSPKI2T                      // decode_spki2typespki.c (SubjectPublicKeyInfo)
	decDer2Key                     // decode_der2key.c and the ML/SLH codecs
)

// decoderInst is an OSSL_DECODER_INSTANCE: its name (the key type or
// "DER"), input structure and, for der2key, the key type's description.
type decoderInst struct {
	kind      decoderKind
	name      string // "DER" or the key type (keymgmt name)
	structure string // input structure ("" none)
	desc      *keyDesc
}

// decodeData is the data a decoder hands to decoder_process (its
// OSSL_PARAM array): data type and structure, a new input type, the DER,
// or a key reference.
type decodeData struct {
	dataType, structure, inputType  string
	hasType, hasStructure, hasInput bool
	der                             []byte
	key                             *decodedKey
}

// decoderCtx is an OSSL_DECODER_CTX for keys.
type decoderCtx struct {
	insts          []decoderInst
	startInputType string // "PEM" or "DER"
	inputStructure string // "" or e.g. "SubjectPublicKeyInfo"
	selection      int
	object         *decodedKey
	keymgmts       []string // keymgmt names the constructor accepts (all for nil filter)
}

// procState is decoder_process_data_st for one level of the walk.
type procState struct {
	ctx                          *decoderCtx
	bio                          []byte // the data at this level
	pos                          int    // pem2der reads move the position
	currentIndex                 int
	flagNextLevelCalled          bool
	flagConstructCalled          bool
	flagInputStructureChecked    bool
	startInputTypeSaved, current string
}

// fromBIO ports OSSL_DECODER_from_bio over a memory BIO holding in at pos:
// it returns whether a key came out and the BIO position afterwards.
func (e *osslEnv) fromBIO(ctx *decoderCtx, in []byte, pos int) (bool, int) {
	q := e.q
	if len(ctx.insts) == 0 {
		q.raise(libDECODER, "OSSL_DECODER_R_DECODER_NOT_FOUND")
		return false, pos
	}
	lasterr := q.peekLast()
	data := &procState{ctx: ctx, bio: in, pos: pos}
	ok := e.decoderProcess(nil, data, nil)
	if !data.flagConstructCalled {
		if q.peekLast() == lasterr || q.peekFirst() == 0 {
			q.raise(libDECODER, "ERR_R_UNSUPPORTED")
		}
		ok = false
	}
	return ok, data.pos
}

// decoderProcess ports decoder_lib.c decoder_process. params nil is the
// first call; otherwise prev is the decoder that produced params.
func (e *osslEnv) decoderProcess(params *decodeData, data *procState, prev *decoderInst) bool {
	q := e.q
	ctx := data.ctx
	data.flagNextLevelCalled = true
	newData := &procState{ctx: ctx}
	var dataType, dataStructure string
	// hasType/hasStructure: the previous decoder passed the parameter (a
	// non-NULL pointer, possibly to ""), as distinct from dataType "".
	var hasType, hasStructure bool
	startInputType := ctx.startInputType
	bio := data.bio
	pos := data.pos
	isTop := params == nil
	ok := false
	if isTop {
		data.currentIndex = len(ctx.insts)
	} else {
		data.flagConstructCalled = false
		if e.constructKey(ctx, params) {
			data.flagConstructCalled = true
			return true
		}
		if params.der == nil {
			return false
		}
		bio, pos = params.der, 0
		newData.bio = bio
		// OSSL_PARAM_get_utf8_string_ptr tries the pointer form first and
		// pops the "incompatible type" error it raises.
		if params.hasType {
			q.setMark()
			q.raise(libCRYPTO, "CRYPTO_R_PARAM_OF_INCOMPATIBLE_TYPE")
			q.popToMark()
			dataType, hasType = params.dataType, true
		}
		if params.hasStructure {
			q.setMark()
			q.raise(libCRYPTO, "CRYPTO_R_PARAM_OF_INCOMPATIBLE_TYPE")
			q.popToMark()
			dataStructure, hasStructure = params.structure, true
		}
		if params.hasInput {
			q.setMark()
			q.raise(libCRYPTO, "CRYPTO_R_PARAM_OF_INCOMPATIBLE_TYPE")
			q.popToMark()
			ctx.startInputType = params.inputType
			if ctx.inputStructure != "" && (strings.EqualFold(ctx.inputStructure, "SubjectPublicKeyInfo") ||
				strings.EqualFold(dataStructure, "PrivateKeyInfo") || strings.EqualFold(ctx.inputStructure, "PrivateKeyInfo")) {
				data.flagInputStructureChecked = true
			}
		}
		if hasType && hasStructure && strings.EqualFold(dataStructure, "type-specific") {
			dataStructure, hasStructure = "", false
		}
	}
	defer func() { ctx.startInputType = startInputType }()
	if data.currentIndex == 0 {
		return false
	}
	loc := pos
	for i := data.currentIndex - 1; i >= 0; i-- {
		inst := &ctx.insts[i]
		inputType := "DER"
		if inst.kind == decPEM2DER {
			inputType = "PEM"
		}
		if prev == nil && ctx.startInputType != "" && !strings.EqualFold(ctx.startInputType, inputType) {
			continue
		}
		if prev != nil && !strings.EqualFold(prev.name, inputType) {
			continue
		}
		// OSSL_DECODER_is_a: no decoder has the name "" (the type
		// epki2pki passes when OBJ_obj2txt refuses the OID).
		if hasType && !decoderIsA(inst, dataType) {
			continue
		}
		if hasStructure && (inst.structure == "" || !strings.EqualFold(dataStructure, inst.structure)) {
			continue
		}
		if !data.flagInputStructureChecked && ctx.inputStructure != "" && inst.structure != "" {
			data.flagInputStructureChecked = true
			if !strings.EqualFold(inst.structure, ctx.inputStructure) {
				continue
			}
		}
		pos = loc
		q.setMark()
		newData.currentIndex = i
		newData.flagInputStructureChecked = data.flagInputStructureChecked
		newData.flagNextLevelCalled = false
		newData.bio = bio
		newData.pos = pos
		ok = e.runDecoder(inst, newData)
		pos = newData.pos
		data.flagConstructCalled = newData.flagConstructCalled
		if !ok || data.flagConstructCalled {
			q.clearLastMark()
			break
		}
		q.popToMark()
		if newData.flagNextLevelCalled {
			break
		}
	}
	if isTop {
		data.pos = pos
	}
	return ok
}

// decoderIsA is OSSL_DECODER_is_a(decoder, name): the decoder's names are
// its key type's names (DER decoders are only "DER").
func decoderIsA(inst *decoderInst, name string) bool {
	if inst.desc == nil {
		return strings.EqualFold(name, "DER")
	}
	for _, n := range inst.desc.names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// runDecoder calls inst's decode function with data.bio at data.pos; the
// callback recurses into decoderProcess.
func (e *osslEnv) runDecoder(inst *decoderInst, data *procState) bool {
	cb := func(p *decodeData) bool { return e.decoderProcess(p, data, inst) }
	switch inst.kind {
	case decPEM2DER:
		return e.pem2der(data, cb)
	case decEPKI2PKI:
		der, ok := e.readDER(data.bio[data.pos:])
		data.pos += len(der)
		if !ok {
			return true
		}
		return e.epki2pki(der, data.ctx.selection, cb)
	case decSPKI2T:
		der, ok := e.readDER(data.bio[data.pos:])
		data.pos += len(der)
		if !ok {
			return true
		}
		return e.spki2typespki(der, cb)
	}
	return e.der2key(inst.desc, data, cb)
}

// constructKey ports decoder_pkey.c decoder_construct_pkey: a key
// reference loads into a keymgmt of the data type (keymgmt load).
func (e *osslEnv) constructKey(ctx *decoderCtx, p *decodeData) bool {
	if p.key == nil {
		return false
	}
	key := e.loadKey(p.key)
	ctx.object = key
	return key != nil
}

// pem2der ports decode_pem2der.c pem2der_decode with selection KEYPAIR.
func (e *osslEnv) pem2der(data *procState, cb func(*decodeData) bool) bool {
	b := &pemBIO{data: data.bio, pos: data.pos}
	blk, ok := e.readBlock(b)
	data.pos = b.pos
	if !ok {
		return true // empty handed
	}
	if len(blk.header) > 10 {
		cipher, ok := e.cipherInfo(blk.header)
		if !ok {
			return false
		}
		if cipher {
			// PEM_do_header via pem2der_pass_helper: the passphrase
			// callback fails (Node passes none).
			e.q.raise(libCRYPTO, "ERR_R_INTERRUPTED_OR_CANCELLED")
			e.q.raise(libCRYPTO, "ERR_R_INTERRUPTED_OR_CANCELLED")
			e.q.raise(libPEM, "PEM_R_BAD_PASSWORD_READ")
			return false
		}
	}
	m, known := pemNameMap[blk.name]
	if !known {
		return true
	}
	if m.pkcs8 {
		return e.epki2pki(blk.data, data.ctx.selection, cb)
	}
	if m.spki {
		// Only with a public-key selection or SPKI structure: not taken
		// for KEYPAIR? KEYPAIR includes PUBLIC.
		return e.spki2typespki(blk.data, cb)
	}
	return cb(&decodeData{dataType: m.dataType, hasType: m.dataType != "",
		structure: m.structure, hasStructure: m.structure != "", der: blk.data})
}

type pemName struct {
	pkcs8, spki         bool
	dataType, structure string
}

// pemNameMap is decode_pem2der.c pem_name_map.
var pemNameMap = map[string]pemName{
	"ENCRYPTED PRIVATE KEY": {pkcs8: true},
	"PRIVATE KEY":           {pkcs8: true},
	"PUBLIC KEY":            {spki: true},
	"DH PARAMETERS":         {dataType: "DH", structure: "type-specific"},
	"X9.42 DH PARAMETERS":   {dataType: "X9.42 DH", structure: "type-specific"},
	"DSA PRIVATE KEY":       {dataType: "DSA", structure: "type-specific"},
	"DSA PUBLIC KEY":        {dataType: "DSA", structure: "type-specific"},
	"DSA PARAMETERS":        {dataType: "DSA", structure: "type-specific"},
	"EC PRIVATE KEY":        {dataType: "EC", structure: "type-specific"},
	"EC PARAMETERS":         {dataType: "EC", structure: "type-specific"},
	"SM2 PRIVATE KEY":       {dataType: "SM2", structure: "type-specific"},
	"SM2 PARAMETERS":        {dataType: "SM2", structure: "type-specific"},
	"RSA PRIVATE KEY":       {dataType: "RSA", structure: "type-specific"},
	"RSA PUBLIC KEY":        {dataType: "RSA", structure: "type-specific"},
	"CERTIFICATE":           {structure: "Certificate"},
	"TRUSTED CERTIFICATE":   {structure: "Certificate"},
	"X509 CERTIFICATE":      {structure: "Certificate"},
	"X509 CRL":              {structure: "CertificateList"},
}

// epki2pki ports decode_epki2pki.c ossl_epki2pki_der_decode.
func (e *osslEnv) epki2pki(der []byte, selection int, cb func(*decodeData) bool) bool {
	q := e.q
	ok := true
	q.setMark()
	if _, _, decoded := e.d2i(x509SigItem, der); decoded {
		q.clearLastMark()
		// pw_cb (ossl_pw_passphrase_callback_dec → do_ui_passphrase) fails.
		q.raise(libCRYPTO, "ERR_R_INTERRUPTED_OR_CANCELLED")
		q.raise(libCRYPTO, "ERR_R_INTERRUPTED_OR_CANCELLED")
		q.raise(libPROV, "PROV_R_UNABLE_TO_GET_PASSPHRASE")
		ok = false
	} else {
		q.popToMark()
	}
	q.setMark()
	p8, _, decoded := e.d2i(pkcs8Item, der)
	q.popToMark()
	if decoded {
		// OBJ_obj2txt's result is not checked: an OID it refuses (more than
		// 586 content octets) leaves keytype "", which names no decoder.
		oid := p8.fields[1].fields[0].data
		name, _ := obj2txt(oid)
		ok = cb(&decodeData{dataType: name, hasType: true, inputType: "DER", hasInput: true,
			structure: "PrivateKeyInfo", hasStructure: true, der: der})
	}
	return ok
}

// spki2typespki ports decode_spki2typespki.c ossl_spki2typespki_der_decode.
func (e *osslEnv) spki2typespki(der []byte, cb func(*decodeData) bool) bool {
	xpub, _, decoded := e.d2i(pubkeyInternalItem, der)
	if !decoded {
		return true
	}
	oid := xpub.fields[0].fields[0].data
	name := ""
	if string(oid) == string(oidEC) && algorIsSM2(e, xpub.fields[0]) {
		name = "SM2"
	} else if n, ok := obj2txt(oid); ok && n != "" {
		name = n
	} else {
		return false
	}
	return cb(&decodeData{dataType: name, hasType: true, inputType: "DER", hasInput: true,
		structure: "SubjectPublicKeyInfo", hasStructure: true, der: der})
}

// der2key ports decode_der2key.c der2key_decode for desc and the input
// structure the instance was registered with.
func (e *osslEnv) der2key(desc *keyDesc, data *procState, cb func(*decodeData) bool) bool {
	return e.der2keyStructure(desc, data, cb)
}
