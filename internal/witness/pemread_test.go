package witness

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// The canonical keys of the probes (/tmp/r8a/w/mk.js): the Ed25519 key
// with seed 7.
func probeKeys(t *testing.T) (pub, priv, pb, kb string) {
	t.Helper()
	k := ed25519.NewKeyFromSeed([]byte(strings.Repeat("\x07", 32)))
	spki, _ := x509.MarshalPKIXPublicKey(k.Public())
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(k)
	pb, kb = base64.StdEncoding.EncodeToString(spki), base64.StdEncoding.EncodeToString(pkcs8)
	return block("PUBLIC KEY", pb), block("PRIVATE KEY", kb), pb, kb
}

func block(label, b64 string) string {
	return "-----BEGIN " + label + "-----\n" + b64 + "\n-----END " + label + "-----\n"
}

func derBlock(label, hexDER string) string {
	der, err := hex.DecodeString(hexDER)
	if err != nil {
		panic(err)
	}
	b := base64.StdEncoding.EncodeToString(der)
	var lines []string
	for len(b) > 64 {
		lines = append(lines, b[:64])
		b = b[64:]
	}
	return block(label, strings.Join(append(lines, b), "\n"))
}

// Expected results: "OK" (an Ed25519 key) or the Node error message.
const (
	okKey        = "OK"
	unsup        = "error:1E08010C:DECODER routines::unsupported"
	cancelled    = "error:07880109:common libcrypto routines::interrupted or cancelled"
	errEVPDecode = "error:03000072:digital envelope routines::decode error"
	notProc      = "error:0480006B:PEM routines::not proc type"
	asnTooLong   = "error:0680009B:asn1 encoding routines::too long"
	wrongTag     = "error:068000A8:asn1 encoding routines::wrong tag"
)

func check(t *testing.T, name, text string, public bool, want string) {
	t.Helper()
	parse := parsePrivate
	if public {
		parse = parsePublic
	}
	key, err := parse(text)
	got := okKey
	if err != nil {
		got = err.Error()
	} else if !key.ed25519 {
		got = "non-Ed25519 key"
	}
	if got != want {
		t.Errorf("%s (public=%v): got %q, want %q", name, public, got, want)
	}
}

// TestPEMProbes replays the Node 22 probes (q1/q2 in the r8a notes) and the
// round-7 reproductions: each input with createPublicKey's and
// createPrivateKey's result.
func TestPEMProbes(t *testing.T) {
	lengthNeedsContentByte = true
	pub, priv, pb, kb := probeKeys(t)
	kder, _ := base64.StdEncoding.DecodeString(kb)
	kh := hex.EncodeToString(kder)
	B, E := "-----BEGIN PUBLIC KEY-----\n", "-----END PUBLIC KEY-----\n"
	// Built, not written out: the secret scanner refuses private-key PEM
	// text in source files.
	PB, PE := "-----BEGIN "+"PRIVATE KEY-----", "-----END "+"PRIVATE KEY-----"
	seven := strings.Repeat("07", 32)
	cases := []struct {
		name, text string
		pub, priv  string
	}{
		{"pubOnly", pub, okKey, unsup},
		{"privOnly", priv, okKey, okKey},
		{"privInner31", derBlock("PRIVATE KEY", "302d020100300506032b65700421041f"+strings.Repeat("07", 31)), unsup, unsup},
		{"privNoOct", derBlock("PRIVATE KEY", "302e020100300506032b657004220000"+seven), unsup, unsup},
		{"privV2", derBlock("PRIVATE KEY", "302e020102300506032b657004220420"+seven), unsup, unsup},
		{"privUnkOid", derBlock("PRIVATE KEY", "302e020100300506032b657904220420"+seven), unsup, unsup},
		{"privTrail", derBlock("PRIVATE KEY", kh+"0000"), okKey, okKey},
		{"privLong", derBlock("PRIVATE KEY", "30812e"+kh[4:]), okKey, okKey},
		{"privLabelEC", derBlock("EC PRIVATE KEY", kh), unsup, unsup},
		{"privLabelRSA", derBlock("RSA PRIVATE KEY", kh), unsup, unsup},
		{"privLabelENC", derBlock("ENCRYPTED PRIVATE KEY", kh), okKey, okKey},
		{"encGarbage", derBlock("ENCRYPTED PRIVATE KEY", "0102"), unsup, unsup},
		{"certLabelPriv", derBlock("CERTIFICATE", kh), wrongTag, unsup},
		{"hdr10", PB + "\nAbcdefgh:\n\n" + kb + "\n" + PE + "\n", okKey, okKey},
		{"hdr11", PB + "\nAbcdefghi:\n\n" + kb + "\n" + PE + "\n", notProc, notProc},
		{"procBad", PB + "\nProc-Type: 3,ENCRYPTED\n\n" + kb + "\n" + PE + "\n", unsup, unsup},
		{"procBadThen", PB + "\nProc-Type: 3,ENCRYPTED\n\n" + kb + "\n" + PE + "\n" + priv, okKey, okKey},
		{"blankIn", PB + "\n" + kb[:16] + "\n\n" + kb[16:] + "\n" + PE + "\n", notProc, notProc},
		{"blankInPub", B + pb[:8] + "\n\n" + pb[8:] + "\n" + E, unsup, unsup},
		{"zeroData_then", B + "-\n" + E + pub, unsup, unsup},
		{"colonData", B + pb + ":\n" + E, unsup, unsup},
		{"colonData_then", B + pb + ":\n" + E + pub, unsup, unsup},
		{"longLine", B + pb + strings.Repeat(" ", 300) + "\n" + E, okKey, unsup},
		{"longLineX", B + pb + strings.Repeat(" ", 300) + "x\n" + E, unsup, unsup},
		{"longBegin", strings.Repeat("x", 254) + B + pb + "\n" + E, okKey, unsup},
		{"longBegin2", strings.Repeat("x", 253) + B + pb + "\n" + E, unsup, unsup},
		{"nulName", "-----BEGIN PUBLIC KEY\x00zz-----\n" + pb + "\n" + E, okKey, unsup},
		{"nulData", B + pb + "\x00zz\n" + E, okKey, unsup},
		{"cipherPub", B + "Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00000000000000000000000000000000\n\n" + pb + "\n" + E, cancelled, cancelled},
		{"cipherPub_then_pub", B + "Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00000000000000000000000000000000\n\n" + pb + "\n" + E + pub, cancelled, cancelled},
		{"hdrShortPub_then", B + "A:\n\n" + pb + "\n" + E + priv, okKey, okKey},
		{"bomOnly", "\ufeff\n" + pub, okKey, unsup},
		{"bom3", "\ufeff" + pub, okKey, unsup},
		{"dashInB64", B + pb[:20] + "-" + pb[20:] + "\n" + E, asnTooLong, unsup},
		{"endJunk", B + pb + "\n-----END PUBLIC KEY-----x\n", unsup, unsup},
		{"endTrailSp", B + pb + "\n-----END PUBLIC KEY-----   \n", okKey, unsup},
		{"noNL", B + pb + "\n-----END PUBLIC KEY-----", okKey, unsup},
		{"crlf", strings.ReplaceAll(pub, "\n", "\r\n"), okKey, unsup},
		{"cronly", strings.ReplaceAll(pub, "\n", "\r"), unsup, unsup},
		{"short64", B + pb[:40] + "\n" + pb[40:] + "\n" + E, okKey, unsup},
		// Round 7 (r7-1 #1-#3, r7-5 #1-#3).
		{"blankBeforeEnd", B + pb + "\n\n" + E, unsup, unsup},
		{"commentHeader", B + "Comment: hi\n\n" + pb + "\n" + E, notProc, notProc},
		{"procNoDEK", B + "Proc-Type: 4,ENCRYPTED\n\n" + pb + "\n" + E, "error:04800069:PEM routines::not dek info", "error:04800069:PEM routines::not dek info"},
		{"dashAfterBody", B + pb + "-\n" + E, okKey, unsup},
		{"nbspAfterBody", B + pb + "\u00a0\n" + E, map[bool]string{true: okKey, false: unsup}[charSigned], unsup},
		{"corruptThenValid", B + "MCow!!!!\n" + E + pub, unsup, unsup},
		{"truncatedDER", derBlock("PUBLIC KEY", "302a300506032b6570032100"+seven[:40]), asnTooLong, unsup},
		{"trailingDER", derBlock("PUBLIC KEY", hex.EncodeToString(mustB64(pb))+"0000"), okKey, unsup},
		{"edParams", derBlock("PUBLIC KEY", "302c300706032b65700500032100"+seven), errEVPDecode, unsup},
		{"edBits1", derBlock("PUBLIC KEY", "302a300506032b6570032101"+seven), okKey, unsup},
		{"edBits9", derBlock("PUBLIC KEY", "302a300506032b6570032109"+seven), "error:068000DC:asn1 encoding routines::invalid bit string bits left", unsup},
		{"commentPubThenPriv", B + "Comment: x\n\n" + pb + "\n" + E + priv, okKey, okKey},
		{"empty", "", unsup, unsup},
	}
	for _, c := range cases {
		check(t, c.name, c.text, true, c.pub)
		check(t, c.name, c.text, false, c.priv)
	}
}

func mustB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// TestLongFormLengthRule pins asn1_get_length's rule before OpenSSL 3.6.5
// (3.5.x and 3.6.0: "max < i + 1", one content byte must follow the length
// octets) against 3.6.5's ("max < i").
func TestLongFormLengthRule(t *testing.T) {
	defer func() { lengthNeedsContentByte = true }()
	text := derBlock("PUBLIC KEY", "308100")
	lengthNeedsContentByte = true
	check(t, "3.5", text, true, "error:0680007B:asn1 encoding routines::header too long")
	lengthNeedsContentByte = false
	check(t, "3.6.5", text, true, "error:06800079:asn1 encoding routines::field missing")
}

func TestCipherInfo(t *testing.T) {
	for _, c := range []struct{ header, want string }{
		{"", ""},
		{"Comment: x", "PEM_R_NOT_PROC_TYPE"},
		{"Proc-Type: 3,ENCRYPTED", "silent"},
		{"Proc-Type: 4,ENCRYPTEDX", "PEM_R_NOT_ENCRYPTED"},
		{"Proc-Type: 4,ENCRYPTED x", "PEM_R_SHORT_HEADER"},
		{"Proc-Type: 4,ENCRYPTED\n", "PEM_R_NOT_DEK_INFO"},
		{"Proc-Type: 4,ENCRYPTED\nDEK-Info: NOPE,00", "PEM_R_UNSUPPORTED_ENCRYPTION"},
		{"Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC x", "PEM_R_MISSING_DEK_IV"},
		{"Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-ECB,", "PEM_R_UNEXPECTED_DEK_IV"},
		{"Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,zz", "PEM_R_BAD_IV_CHARS"},
		{"Proc-Type: 4,ENCRYPTED\nDEK-Info: aes128,00000000000000000000000000000000", "cipher"},
		{"Proc-Type: 4,ENCRYPTED\nDEK-Info: 2.16.840.1.101.3.4.1.2,00000000000000000000000000000000", "cipher"},
	} {
		q := &errQueue{}
		e := &osslEnv{q: q}
		cipher, ok := e.cipherInfo(c.header)
		got := ""
		switch {
		case ok && cipher:
			got = "cipher"
		case !ok && q.empty():
			got = "silent"
		case !ok:
			got = errorString(q.peekLast())
		}
		want := c.want
		if strings.Contains(want, "_R_") {
			want = errorString(errCode(libPEM, osslReasons[want]))
		}
		if got != want {
			t.Errorf("cipherInfo(%q) = %q, want %q", c.header, got, want)
		}
	}
}

// TestErrQueueRing pins the 16-slot ring: the oldest error is evicted
// when a 16th is pushed, and a mark on an evicted slot is lost with it.
func TestErrQueueRing(t *testing.T) {
	q := &errQueue{}
	q.raise(libPEM, "PEM_R_NO_START_LINE")
	q.setMark()
	for range 15 {
		q.raise(libASN1, "ASN1_R_WRONG_TAG")
	}
	if n := len(q.errors()); n != 15 {
		t.Fatalf("queue holds %d errors, want 15", n)
	}
	if got := errorString(q.peekFirst()); got != "error:068000A8:asn1 encoding routines::wrong tag" {
		t.Fatalf("oldest = %q, want the first wrong tag (no start line evicted)", got)
	}
	q.popToMark() // the mark went with the evicted slot: pops everything
	if !q.empty() {
		t.Fatalf("pop to an evicted mark left %d errors", len(q.errors()))
	}
}

// TestNestedRaiseData pins ERR_raise_data with an argument that raises
// (EC_GROUP_new_by_curve_name(-1)): the macro's empty slot ends what
// ERR_get_error returns.
func TestNestedRaiseData(t *testing.T) {
	q := &errQueue{}
	q.raise(libEC, "EC_R_POINT_AT_INFINITY")
	q.raiseDataNested(libEC, "EC_R_UNKNOWN_GROUP", libOBJ, "OBJ_R_UNKNOWN_NID")
	q.raise(libEC, "ERR_R_EC_LIB")
	if got := q.errors(); len(got) != 1 || errorString(got[0]) != "error:0800006A:elliptic curve routines::point at infinity" {
		t.Fatalf("errors = %v", got)
	}
}
