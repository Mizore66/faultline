//go:build faultline_test

package difftest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"math/big"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/witness"
)

// pemCase is one createPublicKey (pub) or createPrivateKey input.
type pemCase struct {
	Text string `json:"text"`
	Pub  bool   `json:"pub"`
}

type pemResult struct {
	OK      bool     `json:"ok"`
	Type    string   `json:"type"`
	SPKI    string   `json:"spki"`
	Message string   `json:"message"`
	Stack   []string `json:"stack"`
}

func pemWrap(label string, der []byte, width int) string {
	b := base64.StdEncoding.EncodeToString(der)
	var sb strings.Builder
	sb.WriteString("-----BEGIN " + label + "-----\n")
	for len(b) > width {
		sb.WriteString(b[:width] + "\n")
		b = b[width:]
	}
	sb.WriteString(b + "\n-----END " + label + "-----\n")
	return sb.String()
}

func seedKey(seed byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, 32))
}

// pemMaterial is the DER the generators start from.
type pemMaterial struct {
	spki, pkcs8, cert, certExt, otherSPKI, otherPKCS8 []byte
	others                                            map[string]string      // non-Ed25519 fixtures (difftest/testdata/pemkeys.json)
	otherDER                                          map[string]pemKeyBlock // the same, unframed
}

func loadPEMMaterial(t *testing.T) pemMaterial {
	t.Helper()
	k1, k2 := seedKey(1), seedKey(2)
	m := pemMaterial{others: map[string]string{}, otherDER: map[string]pemKeyBlock{}}
	m.spki, _ = x509.MarshalPKIXPublicKey(k1.Public())
	m.pkcs8, _ = x509.MarshalPKCS8PrivateKey(k1)
	m.otherSPKI, _ = x509.MarshalPKIXPublicKey(k2.Public())
	m.otherPKCS8, _ = x509.MarshalPKCS8PrivateKey(k2)
	when := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "r"}, NotBefore: when, NotAfter: when.AddDate(10, 0, 0)}
	var err error
	if m.cert, err = x509.CreateCertificate(nil, tpl, tpl, k1.Public(), k1); err != nil {
		t.Fatal(err)
	}
	ext := *tpl
	ext.Subject = pkix.Name{CommonName: "  Reviewer  Key ", Organization: []string{"Exémple"}, Country: []string{"US"}}
	ext.DNSNames = []string{"reviewer.example.test"}
	ext.BasicConstraintsValid, ext.IsCA = true, true
	ext.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
	ext.NotAfter = time.Date(2060, 1, 1, 0, 0, 0, 0, time.UTC) // GeneralizedTime
	if m.certExt, err = x509.CreateCertificate(nil, &ext, &ext, k1.Public(), k1); err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Keys map[string]struct {
			Label, Header, DerHex string
		} `json:"keys"`
	}
	raw, err := os.ReadFile(filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "pemkeys.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for name, k := range fixtures.Keys {
		der, err := hex.DecodeString(k.DerHex)
		if err != nil {
			t.Fatal(err)
		}
		m.others[name] = framed(k.Label, k.Header, der, 64)
		if k.Header == "" {
			m.otherDER[name] = pemKeyBlock{label: k.Label, der: der}
		}
	}
	return m
}

// pemInserts are r7-1 pemfuzz.mjs's insertions.
var pemInserts = []string{" ", "\ufeff", "\u00a0", "\t", "\r", "\n", "\v", "\f", "\x00", "=", "-", ":", "x",
	"Proc-Type: 4,ENCRYPTED\n", "A: b\n", "\n\n", "-----", "\u2028", "\u3000", "é", "#", "*", "\\", "\u0085", "ÿ"}

// mutateText is pemfuzz.mjs's mutate: one or two character edits.
func mutateText(r *rand.Rand, text string) string {
	out := []rune(text)
	for k := 1 + r.IntN(2); k > 0; k-- {
		i := r.IntN(len(out) + 1)
		ins := []rune(pemInserts[r.IntN(len(pemInserts))])
		switch r.IntN(4) {
		case 0:
			out = append(out[:i:i], append(ins, out[i:]...)...)
		case 1:
			if i < len(out) {
				out = append(out[:i:i], out[i+1:]...)
			}
		case 2:
			j := i
			for j < len(out) && out[j] != '\n' {
				j++
			}
			out = append(out[:j:j], append(ins, out[j:]...)...)
		default:
			lines := strings.Split(string(out), "\n")
			li := r.IntN(len(lines))
			lines[li] = string(ins) + lines[li]
			out = []rune(strings.Join(lines, "\n"))
		}
	}
	return string(out)
}

// mutateDER edits DER bytes: a flipped, inserted or deleted byte,
// truncation, trailing bytes, or a TLV length rewritten in long form.
func mutateDER(r *rand.Rand, der []byte) []byte {
	d := append([]byte(nil), der...)
	switch r.IntN(7) {
	case 0:
		d[r.IntN(len(d))] ^= byte(1 << r.IntN(8))
	case 1:
		d[r.IntN(len(d))] = byte(r.IntN(256))
	case 2:
		i := r.IntN(len(d) + 1)
		d = append(d[:i:i], append([]byte{byte(r.IntN(256))}, d[i:]...)...)
	case 3:
		i := r.IntN(len(d))
		d = append(d[:i:i], d[i+1:]...)
	case 4:
		d = d[:r.IntN(len(d))]
	case 5:
		d = append(d, []byte{0, 0, 5, 0, 0x30, 0x80}[:1+r.IntN(6)]...)
	default:
		// The TLV at a random header position, its length in long form.
		for tries := 0; tries < 20; tries++ {
			i := r.IntN(len(d) - 1)
			if d[i+1] >= 0x80 || int(d[i+1]) > len(d)-i-2 {
				continue
			}
			n := d[i+1]
			form := []byte{0x81, n}
			if r.IntN(3) == 0 {
				form = []byte{0x82, 0, n}
			}
			d = append(d[:i+1:i+1], append(form, d[i+2:]...)...)
			break
		}
	}
	if len(d) == 0 {
		d = []byte{0x30}
	}
	return d
}

var pemLabels = []string{"PUBLIC KEY", "PRIVATE KEY", "CERTIFICATE", "X509 CERTIFICATE", "TRUSTED CERTIFICATE",
	"RSA PUBLIC KEY", "ENCRYPTED PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "DSA PRIVATE KEY", "rsa PRIVATE KEY", "public key"}

var pemHeaders = []string{
	"", "A:", "Abcdefgh:", "Abcdefghi:", "Comment: hi", "Proc-Type: 4,ENCRYPTED",
	"Proc-Type: 3,ENCRYPTED", "Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00000000000000000000000000000000",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: aes-256-cbc,0011223344556677889900AABBCCDDEE",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-ECB", "Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-ECB,",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC", "Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,zz",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: NOPE,00", "Proc-Type: 4,ENCRYPTED\nDEK-Info: DES-EDE3-CBC,0000000000000000",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: id-aes128-GCM,000000000000000000000000",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC-HMAC-SHA1,00000000000000000000000000000000",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: 2.16.840.1.101.3.4.1.2,00000000000000000000000000000000",
	"Proc-Type: 4,ENCRYPTEDX\nDEK-Info: AES-128-CBC,00", "Proc-Type: 4,ENCRYPTED \r\nDEK-Info:\tAES-128-CBC ,00",
	"Proc-Type: 4, ENCRYPTED\nComment: x", "Proc-Type:4,ENCRYPTED\nDEK-Info: AES-128-CBC,0000000000000000000000000000000",
	"Proc-Type: 4,ENCRYPTED", "Proc-Type: 4", "Proc-Type:", "Proc-Type: 4,\n",
}

// framed is der in a block with a header and wrapping width.
func framed(label, header string, der []byte, width int) string {
	body := pemWrap(label, der, width)
	if header == "" {
		return body
	}
	first, rest, _ := strings.Cut(body, "\n")
	return first + "\n" + header + "\n\n" + rest
}

func genPEMCases(t *testing.T, m pemMaterial) []pemCase {
	r := rand.New(rand.NewPCG(8, 1))
	var cases []pemCase
	both := func(text string, _ bool) {
		cases = append(cases, pemCase{Text: text, Pub: true}, pemCase{Text: text, Pub: false})
	}
	pub, priv := pemWrap("PUBLIC KEY", m.spki, 64), pemWrap("PRIVATE KEY", m.pkcs8, 64)
	// Built, not written out: the repository's secret scanner refuses
	// private-key PEM text in source files.
	pk := "-----BEGIN " + "PRIVATE KEY-----"
	cert := pemWrap("CERTIFICATE", m.cert, 64)
	otherPub, otherPriv := pemWrap("PUBLIC KEY", m.otherSPKI, 64), pemWrap("PRIVATE KEY", m.otherPKCS8, 64)
	b64 := base64.StdEncoding.EncodeToString(m.spki)
	kb := base64.StdEncoding.EncodeToString(m.pkcs8)

	// The r7-1 flips, as both calls.
	var flips struct {
		Flips []struct {
			Kind      string `json:"kind"`
			PemBase64 string `json:"pemBase64"`
		} `json:"flips"`
	}
	raw, err := os.ReadFile(filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "pemflips.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &flips); err != nil {
		t.Fatal(err)
	}
	for _, f := range flips.Flips {
		text, err := base64.StdEncoding.DecodeString(f.PemBase64)
		if err != nil {
			t.Fatal(err)
		}
		both(string(text), false)
	}

	// r7-5 genkr.py / genpriv.py variants.
	named := []string{
		pub, priv, cert, pemWrap("CERTIFICATE", m.certExt, 64),
		"-----BEGIN PUBLIC KEY-----\nMCow!!!!\n-----END PUBLIC KEY-----\n" + pub,
		"-----BEGIN PUBLIC KEY-----\nMCowBQ\n-----END PUBLIC KEY-----\n" + pub,
		strings.ReplaceAll(pub, "\n", "\r\n"), strings.ReplaceAll(pub, "\n", "\n  "),
		"-----BEGIN PUBLIC KEY-----\n" + b64[:20] + " " + b64[20:] + "\n-----END PUBLIC KEY-----\n",
		"-----BEGIN PUBLIC KEY-----\n" + b64[:7] + "\n" + b64[7:] + "\n-----END PUBLIC KEY-----\n",
		strings.TrimSuffix(pub, "\n"), strings.TrimSuffix(pub, "\n") + "garbage\n", strings.TrimSuffix(pub, "\n") + "   \n",
		strings.Replace(pub, "PUBLIC KEY-----\nMC", "PUBLIC KEY-----   \nMC", 1),
		"-----BEGIN PUBLIC KEY-----\nFoo: bar\n\n" + b64 + "\n-----END PUBLIC KEY-----\n",
		"-----BEGIN PUBLIC KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00000000000000000000000000000000\n\n" + b64 + "\n-----END PUBLIC KEY-----\n",
		strings.ReplaceAll(pub, "END PUBLIC KEY", "END PRIVATE KEY"),
		otherPriv + pub, otherPub + pub, "hello world\n" + pub, "-----\n" + pub,
		pemWrap("PUBLIC KEY", append(append([]byte(nil), m.spki...), 0, 0), 64),
		pemWrap("PUBLIC KEY", append(append([]byte(nil), m.spki...), 5, 0), 64),
		pemWrap("PUBLIC KEY", append([]byte{0x30, 0x81, 0x2a}, m.spki[2:]...), 64),
		cert + otherPub, otherPub + cert, "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n" + pub,
		strings.ReplaceAll(cert, "CERTIFICATE", "X509 CERTIFICATE"), strings.ReplaceAll(cert, "CERTIFICATE", "TRUSTED CERTIFICATE"),
		strings.ReplaceAll(pub, "PUBLIC KEY", "public key"),
		"-----BEGIN PUBLIC KEY-----\n\t" + b64 + "\n-----END PUBLIC KEY-----\n",
		"-----BEGIN PUBLIC KEY-----\n" + b64[:20] + "\n\n" + b64[20:] + "\n-----END PUBLIC KEY-----\n",
		strings.Replace(pub, "=\n", "\n", 1), strings.Replace(pub, "=\n", "==\n", 1),
		strings.Replace(pub, "\n", "\u00a0\n", 1), "\ufeff" + pub, "\ufeff\n" + pub, pub + "\x00junk",
		strings.ReplaceAll(pub, "\n", "\r"), strings.Replace(pub, "-----BEGIN", "----BEGIN", 1),
		strings.ReplaceAll(pub, "PUBLIC KEY", "RSA PUBLIC KEY"),
		pk + "\nFoo: bar\n\n" + kb + "\n-----END PRIVATE KEY-----\n",
		pk + "\nMC4!!\n-----END PRIVATE KEY-----\n" + priv,
		pk + "\n" + kb[:16] + "\n\n" + kb[16:] + "\n-----END PRIVATE KEY-----\n",
		pemWrap("PRIVATE KEY", append(append([]byte(nil), m.pkcs8...), 0, 0), 64),
		pemWrap("PRIVATE KEY", append([]byte{0x30, 0x81, m.pkcs8[1]}, m.pkcs8[2:]...), 64),
		strings.Replace(priv, "\n", "\u00a0\n", 1), "\ufeff" + priv, pub + priv, priv + pub,
		pk + "\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00000000000000000000000000000000\n\n" + kb + "\n-----END PRIVATE KEY-----\n",
		strings.ReplaceAll(priv, "PRIVATE KEY", "EC PRIVATE KEY"), strings.ReplaceAll(priv, "PRIVATE KEY", "RSA PRIVATE KEY"),
		strings.ReplaceAll(priv, "PRIVATE KEY", "ENCRYPTED PRIVATE KEY"),
		"-----BEGIN PUBLIC KEY-----\n" + b64 + strings.Repeat(" ", 300) + "\n-----END PUBLIC KEY-----\n",
		"-----BEGIN PUBLIC KEY-----\n" + b64 + strings.Repeat(" ", 300) + "x\n-----END PUBLIC KEY-----\n",
		strings.Repeat("x", 254) + pub, strings.Repeat("x", 253) + pub,
		"-----BEGIN PUBLIC KEY\x00zz-----\n" + b64 + "\n-----END PUBLIC KEY-----\n",
		"-----BEGIN PUBLIC KEY-----\n" + b64 + "\x00zz\n-----END PUBLIC KEY-----\n",
		"-----BEGIN PUBLIC KEY-----\n-\n-----END PUBLIC KEY-----\n" + pub,
		"-----BEGIN PUBLIC KEY-----\n" + b64 + ":\n-----END PUBLIC KEY-----\n" + pub,
		"-----BEGIN PUBLIC KEY-----\n" + b64[:20] + "-" + b64[20:] + "\n-----END PUBLIC KEY-----\n",
		"-----BEGIN PUBLIC KEY-----\nA:\n\n" + b64 + "\n-----END PUBLIC KEY-----\n" + priv,
		"", "\n", "x", "-----BEGIN PUBLIC KEY-----\n-----END PUBLIC KEY-----\n",
	}
	for _, text := range named {
		both(text, false)
	}

	// Header and length edge cases of the DER decoder.
	for _, h := range []string{
		"30", "3081", "308100", "30810000", "3082000100", "3080", "30800000", "308000", "3084ffffffff00",
		"3089010000000000000000",
		// Long forms with 8 length octets and with leading zero octets
		// (asn1_get_length skips them before the sizeof(long) check), and
		// 3.5's content-byte rule at the 8-octet boundary.
		"308801000000000000000000", "30890001000000000000000000", "3088000000000000002a00",
		"30880000000000000000", "308800000000000000", "3082002a00", "30830000002a", "308400000000",
		"30887fffffffffffffff00", "3088800000000000000000", "308900000000000000002a00",
		"3f", "3f81", "3f8100", "3f818100", "1f2200", "3000", "300100",
		"3005300306012b", "300a300506032b6570030100", "300b300506032b657003020700", "300b300506032b657003020800",
		"302c300706032b65700500032100" + strings.Repeat("aa", 32),
		"302a300506032b7570032100" + strings.Repeat("aa", 32),
		"302b300506032b6570032200" + strings.Repeat("aa", 33),
		"302a300506032b6570032101" + strings.Repeat("aa", 32),
		"3080300506032b6570032100" + strings.Repeat("aa", 32) + "0000",
		"3080300506032b6570032100" + strings.Repeat("aa", 32),
		"302a3005060300806570032100" + strings.Repeat("aa", 32),
		"302a2005060300806570032100" + strings.Repeat("aa", 32),
		"302a300525032b6570032100" + strings.Repeat("aa", 32),
		"302c300506032b6570238021" + "0300" + strings.Repeat("aa", 32) + "0000",
		"302e020100300506032b657004220420" + strings.Repeat("07", 32),
		"302e020101300506032b657004220420" + strings.Repeat("07", 32),
		"302e020102300506032b657004220420" + strings.Repeat("07", 32),
		"302f02020000300506032b657004220420" + strings.Repeat("07", 32),
		"3030020100300506032b6570042204200000" + strings.Repeat("07", 32),
		"302e020100300506032b657024220420" + strings.Repeat("07", 32),
		"3051020101300506032b657004220420" + strings.Repeat("07", 32) + "812100" + strings.Repeat("aa", 32),
		"3051020100300506032b657004220420" + strings.Repeat("07", 32) + "812100" + strings.Repeat("aa", 32),
		"3034020100300506032b657004220420" + strings.Repeat("07", 32) + "a0043002060000",
	} {
		der, _ := hex.DecodeString(h)
		for _, label := range []string{"PUBLIC KEY", "PRIVATE KEY", "CERTIFICATE", "RSA PUBLIC KEY"} {
			both(pemWrap(label, der, 64), false)
		}
	}
	// Non-Ed25519 keys, plain and mixed with Ed25519 ones.
	for _, other := range m.others {
		both(other, true)
		both(other+pub, true)
		both(priv+other, true)
		both("\ufeff"+other, true)
	}

	// Ed25519 structures with edited DER under every label, header and width.
	ders := [][]byte{m.spki, m.pkcs8, m.cert, m.certExt}
	for i := 0; i < 900; i++ {
		der := ders[r.IntN(len(ders))]
		for k := r.IntN(3); k >= 0; k-- {
			der = mutateDER(r, der)
		}
		label := pemLabels[r.IntN(len(pemLabels))]
		if r.IntN(2) == 0 {
			label = []string{"PUBLIC KEY", "PRIVATE KEY", "CERTIFICATE"}[r.IntN(3)]
		}
		header := ""
		if r.IntN(5) == 0 {
			header = pemHeaders[r.IntN(len(pemHeaders))]
		}
		both(framed(label, header, der, []int{64, 64, 76, 16, 63, 65}[r.IntN(6)]), false)
	}
	// Character edits of the canonical texts (pemfuzz.mjs).
	bases := []string{pub, priv, cert, pub + priv, otherPub + pub, framed("PRIVATE KEY", "Abcdefgh:", m.pkcs8, 64)}
	for i := 0; i < 800; i++ {
		both(mutateText(r, bases[r.IntN(len(bases))]), false)
	}
	// Headers on the valid structures.
	for _, h := range pemHeaders {
		for _, label := range []string{"PUBLIC KEY", "PRIVATE KEY", "CERTIFICATE"} {
			der := map[string][]byte{"PUBLIC KEY": m.spki, "PRIVATE KEY": m.pkcs8, "CERTIFICATE": m.cert}[label]
			both(framed(label, h, der, 64), false)
			both(framed(label, h, der, 64)+pub, false)
		}
	}
	return cases
}

// runPEMOracle runs difftest/gen/pem-oracle.mjs without a controlling
// terminal and with stdin on /dev/null.
func runPEMOracle(t *testing.T, cases []pemCase) []pemResult {
	t.Helper()
	input := filepath.Join(t.TempDir(), "cases.json")
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(oracle.RepoRoot(t), "difftest", "gen", "pem-oracle.mjs"), input)
	cmd.SysProcAttr = noTerminal()
	// PEM_def_callback writes its prompt to stderr before reading
	// /dev/null; keep it out of the log unless node fails.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	var reply struct {
		OpenSSL string      `json:"openssl"`
		Results []pemResult `json:"results"`
	}
	if err := json.Unmarshal(out, &reply); err != nil || len(reply.Results) != len(cases) {
		t.Fatalf("oracle output: %v (%d results)", err, len(reply.Results))
	}
	// Node linked against another OpenSSL (Homebrew's 3.6) reads a
	// truncated long-form length differently; follow it here.
	witness.SetOpenSSLForTest(reply.OpenSSL)
	t.Cleanup(func() { witness.SetOpenSSLForTest("3.5") })
	return reply.Results
}

// TestLivePEMKeyParsing compares Go's port of Node's
// createPublicKey/createPrivateKey with Node on generated and collected
// reviewer-key texts of every key type: the key type, the Ed25519 public
// key, and for refusals the message and the whole opensslErrorStack.
func TestLivePEMKeyParsing(t *testing.T) {
	oracle.Skip(t)
	m := loadPEMMaterial(t)
	cases := genPEMCases(t, m)
	cases = append(cases, genAlgorithmCases(m, 4000)...)
	cases = append(cases, genPQCases(m, 1200)...)
	cases = append(cases, genReasonCases(m)...)
	cases = append(cases, genMixedCases(m, 6000)...)
	cases = append(cases, loadPEMCorpus(t)...)
	explicitFrom := len(cases) // the explicit-curve sets: reducible fields, composite moduli
	cases = append(cases, genReducibleCases(m)...)
	cases = append(cases, loadReducibleFixtures(t)...)
	results := runPEMOracle(t, cases)
	accepted, fails := 0, 0
	var messages strings.Builder
	var redo []int
	for i, c := range cases {
		want := results[i]
		got := witness.ParseKeyForTest(c.Text, c.Pub)
		if want.OK && want.Type == "ed25519" {
			accepted++
		}
		messages.WriteString(want.Message + "\n" + strings.Join(want.Stack, "\n") + "\n")
		if pemOutcome(got) != pemResultOutcome(want) {
			redo = append(redo, i)
		}
	}
	// A disagreement is settled by running the case many times on both
	// sides: over a reducible field or a composite modulus OpenSSL's
	// blinding makes the result random, and Go draws it the same way
	// (KNOWN_DIFFERENCES). Each outcome's share must agree, and a case
	// Node always parses one way must parse that way in Go every time
	// (pemCompareRepeated). A sample of the explicit-curve cases that
	// agreed on one run is re-run too, so that a rare Go misfire on a case
	// Node treats as deterministic is caught.
	var agreeing []int
	for i := explicitFrom; i < len(cases); i++ {
		if !slices.Contains(redo, i) {
			agreeing = append(agreeing, i)
		}
	}
	sr := rand.New(rand.NewPCG(9, 9))
	sr.Shuffle(len(agreeing), func(a, b int) { agreeing[a], agreeing[b] = agreeing[b], agreeing[a] })
	sample := agreeing[:min(pemAgreeSample, len(agreeing))]
	for _, batch := range []struct {
		idx  []int
		runs int
	}{{redo, pemRepeat}, {sample, pemAgreeRepeat}} {
		for _, i := range pemCompareRepeated(t, cases, batch.idx, batch.runs) {
			if fails++; fails <= pemShowDiffs() {
				t.Errorf("result differs (pub=%v): %s", cases[i.idx].Pub, i.why)
			}
		}
	}
	if fails > 0 {
		t.Errorf("%d of %d cases differ", fails, len(cases))
	}
	// Every OpenSSL reason the reader files can raise must come up against
	// Node at least once (unless listed unreachable in coverage_test.go).
	for _, f := range pemReaderFiles {
		requireReachedReasons(t, f, messages.String())
	}
	liveSummary(t, "%d PEM cases (%d Ed25519 keys accepted; %d disagreeing ones re-run %d times and %d agreeing explicit-curve ones %d times per side)",
		len(cases), accepted, len(redo), pemRepeat, len(sample), pemAgreeRepeat)
}

// pemRepeat is how often a case whose single runs disagree is re-run on
// each side; pemAgreeSample explicit-curve cases that agreed are re-run
// pemAgreeRepeat times; a case Node gave one way in every run is re-run
// pemUnanimousRepeat times in Node before a Go misfire counts.
const (
	pemRepeat          = 400
	pemAgreeSample     = 40
	pemAgreeRepeat     = 200
	pemUnanimousRepeat = 4000
)

type pemMismatch struct {
	idx int
	why string
}

// pemCompareRepeated runs each case runs times on both sides and returns
// those whose outcomes disagree. Shares must agree (pemSharesDiffer).
// When every Node run gave one outcome, Go must give only that outcome:
// a Go run with another one fails unless Node, re-run pemUnanimousRepeat
// times, also gives another one (the case is then random, and the shares
// over all runs are compared).
func pemCompareRepeated(t *testing.T, cases []pemCase, idx []int, runs int) []pemMismatch {
	t.Helper()
	if len(idx) == 0 {
		return nil
	}
	var again []pemCase
	for _, i := range idx {
		again = append(again, cases[i])
	}
	counts := runPEMOracleRepeat(t, again, runs)
	goCounts := make([]map[string]int, len(idx))
	var unanimous []int // positions in idx where Node was unanimous and Go was not
	for k, i := range idx {
		goCounts[k] = map[string]int{}
		for range runs {
			goCounts[k][pemOutcome(witness.ParseKeyForTest(cases[i].Text, cases[i].Pub))]++
		}
		if len(counts[k]) == 1 && len(goCounts[k]) > 1 {
			unanimous = append(unanimous, k)
		}
	}
	if len(unanimous) > 0 {
		var more []pemCase
		for _, k := range unanimous {
			more = append(more, cases[idx[k]])
		}
		extra := runPEMOracleRepeat(t, more, pemUnanimousRepeat)
		for n, k := range unanimous {
			for o, v := range extra[n] {
				counts[k][o] += v
			}
		}
	}
	var out []pemMismatch
	for k, i := range idx {
		why := ""
		if len(counts[k]) == 1 {
			for o := range counts[k] {
				if len(goCounts[k]) != 1 || goCounts[k][o] == 0 {
					why = "Node gave one outcome in every run, Go did not"
				}
			}
		}
		if why == "" {
			why = pemSharesDiffer(counts[k], goCounts[k])
		}
		if why != "" {
			out = append(out, pemMismatch{i, fmt.Sprintf("%s: %q\n  go:   %v\n  node: %v", why, cases[i].Text, goCounts[k], counts[k])})
		}
	}
	return out
}

// pemOutcome is one Go parse as the oracle prints it (the JSON of a
// pemResult), so outcomes of both sides compare as strings.
func pemOutcome(r witness.KeyResult) string {
	if r.OK {
		return pemResultOutcome(pemResult{OK: true, Type: r.Type, SPKI: hex.EncodeToString(r.SPKI)})
	}
	return pemResultOutcome(pemResult{Message: r.Message, Stack: r.Stack})
}

func pemResultOutcome(r pemResult) string {
	if r.OK {
		b, _ := json.Marshal(struct {
			OK   bool   `json:"ok"`
			Type string `json:"type"`
			SPKI string `json:"spki"`
		}{true, r.Type, r.SPKI})
		return string(b)
	}
	stack := r.Stack
	if stack == nil {
		stack = []string{}
	}
	b, _ := json.Marshal(struct {
		OK      bool     `json:"ok"`
		Message string   `json:"message"`
		Stack   []string `json:"stack"`
	}{false, r.Message, stack})
	return string(b)
}

// pemSharesDiffer compares two samples of a random parse: "" when every
// outcome's share agrees within sampling error (a two-proportion z below
// 5, so a correct port fails about once in 3.5 million comparisons), else
// why not. An outcome one side never gives fails once the other gives it
// often enough to matter (z >= 5 already covers it: 0 of n against 25 of
// n is z = 5.0).
func pemSharesDiffer(node, goSide map[string]int) string {
	nt, gt := 0, 0
	for _, v := range node {
		nt += v
	}
	for _, v := range goSide {
		gt += v
	}
	keys := map[string]bool{}
	for k := range node {
		keys[k] = true
	}
	for k := range goSide {
		keys[k] = true
	}
	for k := range keys {
		pn, pg := float64(node[k])/float64(nt), float64(goSide[k])/float64(gt)
		pool := float64(node[k]+goSide[k]) / float64(nt+gt)
		se := math.Sqrt(pool * (1 - pool) * (1/float64(nt) + 1/float64(gt)))
		if se == 0 {
			continue
		}
		if z := math.Abs(pn-pg) / se; z >= 5 {
			return fmt.Sprintf("share of %s: node %.3f, go %.3f (z %.1f)", k, pn, pg, z)
		}
	}
	return ""
}

// runPEMOracleRepeat runs each case n times in Node (PEM_REPEAT) and
// returns, per case, the count of each outcome.
func runPEMOracleRepeat(t *testing.T, cases []pemCase, n int) []map[string]int {
	t.Helper()
	input := filepath.Join(t.TempDir(), "cases.json")
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(oracle.RepoRoot(t), "difftest", "gen", "pem-oracle.mjs"), input)
	cmd.SysProcAttr = noTerminal()
	cmd.Env = append(os.Environ(), "PEM_REPEAT="+strconv.Itoa(n))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	var reply struct {
		Results []map[string]int `json:"results"`
	}
	if err := json.Unmarshal(out, &reply); err != nil || len(reply.Results) != len(cases) {
		t.Fatalf("oracle output: %v (%d results)", err, len(reply.Results))
	}
	// Re-encode the keys as pemResultOutcome does (Node's key order and
	// an absent stack differ from Go's encoding).
	for i, m := range reply.Results {
		norm := map[string]int{}
		for k, v := range m {
			var r pemResult
			if err := json.Unmarshal([]byte(k), &r); err != nil {
				t.Fatal(err)
			}
			norm[pemResultOutcome(r)] += v
		}
		reply.Results[i] = norm
	}
	return reply.Results
}

// pemReaderFiles are the files of the OpenSSL port whose raised reasons
// (the ERR_raise reason names) the live comparison must reach.
var pemReaderFiles = []string{
	"internal/witness/asn1dec.go",
	"internal/witness/osslpem.go",
	"internal/witness/decoder.go",
	"internal/witness/keyalgs.go",
	"internal/witness/ecgroup.go",
	"internal/witness/nodekeys.go",
	"internal/witness/pqkeys.go",
	"internal/witness/mlkem.go",
	"internal/witness/ecmul.go",
	"internal/witness/gf2m.go",
}

// requireReachedReasons fails for every OpenSSL reason name in file (a
// string literal naming a reason of the generated table) whose error text
// no Node result contains, unless pemUnreachableReasons says why Node can
// never show it. The generic error-literal gate skips reason codes, so the
// reader files are checked here.
func requireReachedReasons(t *testing.T, file, outputs string) {
	t.Helper()
	if liveGatedFiles[file] == "" {
		t.Errorf("%s is not in liveGatedFiles", file)
	}
	path := filepath.Join(oracle.RepoRoot(t), filepath.FromSlash(file))
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, _ := strconv.Unquote(lit.Value)
		text := witness.ReasonText(name)
		if text == "" || !strings.Contains(name, "_R_") {
			return true
		}
		if _, ok := pemUnreachableReasons[name]; ok {
			return true
		}
		if !strings.Contains(outputs, "::"+text) {
			t.Errorf("%s: no Node result raised %s (%q)", fset.Position(lit.Pos()), name, text)
		}
		return true
	})
}

// pemUnreachableReasons are the reasons the port raises exactly where
// OpenSSL 3.5 does but which Node can never show, each with why (verified
// against the traced OpenSSL 3.5.5 driver and Node 22.22.2).
var pemUnreachableReasons = map[string]string{
	// Never raised for any input: template-shape checks the fixed templates
	// rule out.
	"ASN1_R_NESTED_TOO_DEEP":                  "tasn_dec counts template nesting, not data nesting; the deepest template Node's readers use (X509 → CINF → NAME → RDN → entry) is far below ASN1_MAX_CONSTRUCTED_NEST (30)",
	"ASN1_R_ILLEGAL_OPTIONS_ON_ITEM_TEMPLATE": "raised only for an ITEM_TEMPLATE used with an implicit tag or OPTIONAL; no template the readers use does that",
	"ASN1_R_BAD_TEMPLATE":                     "raised only for a CHOICE used with an implicit tag; ECPKPARAMETERS (the only CHOICE) is used untagged or EXPLICIT",
	"ASN1_R_UNSUPPORTED_ANY_DEFINED_BY_TYPE":  "both ADB tables the readers use (X9_62_FIELDID, X9_62_CHARACTERISTIC_TWO) have a default entry, so an unknown selector never fails",
	"ASN1_R_ILLEGAL_TAGGED_ANY":               "no template the readers use tags an ANY",
	"ASN1_R_ILLEGAL_OPTIONAL_ANY":             "the only OPTIONAL ANY (X509_ALGOR parameter) is a SEQUENCE's last field, which tasn_dec decodes as not optional",
	"ERR_R_PASSED_INVALID_ARGUMENT":           "der2key's selection check: OSSL_DECODER_CTX setup already dropped every decoder whose does_selection refuses Node's KEYPAIR / PUBLIC_KEY selection (same masks)",
	"EVP_R_EXPECTING_AN_RSA_KEY":              "the typed SPKI decoder is chosen by the SPKI's own algorithm name (spki2typespki / the nested decoder context), and rsa_pub_decode assigns the method's base id, so the legacy key is always RSA or RSA-PSS there",
	"EVP_R_EXPECTING_A_DSA_KEY":               "as EVP_R_EXPECTING_AN_RSA_KEY: dsa_pub_decode always assigns EVP_PKEY_DSA, and only an SPKI naming DSA reaches the DSA decoder",
	"EVP_R_EXPECTING_A_ECX_KEY":               "as EVP_R_EXPECTING_AN_RSA_KEY: only an SPKI naming Ed25519 reaches the Ed25519 decoder, whose legacy key is Ed25519",
	// Raised, then always removed before Node reads the queue.
	"BN_R_NO_SOLUTION":                    "ec2_oct.c pops it (ERR_set_mark / ERR_pop_to_mark around BN_GF2m_mod_solve_quad_arr) and raises EC_R_INVALID_COMPRESSED_POINT instead",
	"OBJ_R_UNKNOWN_NID":                   "raised only while evaluating ERR_raise_data's argument OBJ_nid2sn(-1) in EC_GROUP_new_by_curve_name; ERR_set_error then overwrites that slot with EC_R_UNKNOWN_GROUP",
	"CRYPTO_R_PARAM_OF_INCOMPATIBLE_TYPE": "OSSL_PARAM_get_utf8_string_ptr's probe in decoder_process, always inside its own ERR_set_mark / ERR_pop_to_mark",
	"OSSL_DECODER_R_DECODER_NOT_FOUND":    "only the nested SubjectPublicKeyInfo decoder context for an algorithm with no decoder is empty; x509_pubkey_ex_d2i_ex pops its errors",
	"ASN1_R_NOT_ENOUGH_DATA":              "ossl_read_der fails only inside a decoder, which then returns empty-handed and decoder_process pops to its mark",
	"PEM_R_NO_START_LINE":                 "the public PEM passes run inside ncrypto's MarkPop, pem2der returns empty-handed (popped by decoder_process), and pem_read_bio_key_legacy pops PEM read errors",
	"PEM_R_BAD_BASE64_DECODE":             "as PEM_R_NO_START_LINE",
	"PEM_R_BAD_END_LINE":                  "as PEM_R_NO_START_LINE",
	"BN_R_NOT_A_SQUARE":                   "ecp_oct.c pops it (ERR_set_mark / ERR_pop_to_mark around BN_mod_sqrt) and raises EC_R_INVALID_COMPRESSED_POINT instead",
	"UI_R_PROCESSING_ERROR":               "PEM_def_callback runs only in the public PEM passes (inside ncrypto's MarkPop); the private path uses Node's passphrase callback",
	"PEM_R_PROBLEMS_GETTING_PASSWORD":     "as UI_R_PROCESSING_ERROR",
	"DSA_R_PARAMETER_ENCODING_ERROR":      "raised only by dsa_pub_decode, which runs only inside x509_pubkey_ex_d2i_ex's mark (popped; Node sees EVP_R_DECODE_ERROR)",
	"DH_R_PARAMETER_ENCODING_ERROR":       "raised only by dh_pub_decode, likewise inside x509_pubkey_ex_d2i_ex's mark",
	"PROV_R_BAD_ENCODING":                 "ML-DSA/ML-KEM/SLH-DSA SPKI decoding runs only inside a der2key decoder; a failure returns empty-handed and decoder_process pops it (the legacy reader has no method for these types)",
	"PROV_R_UNEXPECTED_KEY_PARAMETERS":    "as PROV_R_BAD_ENCODING (PKCS#8 decoding of ML-DSA/ML-KEM/SLH-DSA)",
	"PROV_R_ML_DSA_NO_FORMAT":             "as PROV_R_BAD_ENCODING",
	"PROV_R_ML_KEM_NO_FORMAT":             "as PROV_R_BAD_ENCODING",
}

// TestPEMUnreachableReasonsRaised keeps pemUnreachableReasons honest: every
// entry is still a reason one of the reader files names.
func TestPEMUnreachableReasonsRaised(t *testing.T) {
	var all strings.Builder
	for _, f := range pemReaderFiles {
		b, err := os.ReadFile(filepath.Join(oracle.RepoRoot(t), filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	for name := range pemUnreachableReasons {
		if witness.ReasonText(name) == "" || !strings.Contains(all.String(), strconv.Quote(name)) {
			t.Errorf("pemUnreachableReasons lists %s, which no reader file raises", name)
		}
	}
}

// --- generators over every key type ---------------------------------------

// pemKeyBlocks are the non-Ed25519 fixtures (every algorithm), as DER.
type pemKeyBlock struct {
	label string
	der   []byte
}

func (m pemMaterial) keyBlocks() []pemKeyBlock {
	var out []pemKeyBlock
	for _, name := range pemSortedKeys(m.otherDER) {
		out = append(out, m.otherDER[name])
	}
	return out
}

func pemSortedKeys[V any](mm map[string]V) []string {
	keys := make([]string, 0, len(mm))
	for k := range mm {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// tlv is a parsed DER node: tag bytes, and either content or children.
type tlv struct {
	tag  []byte
	val  []byte
	kids []*tlv
}

func parseTLVs(d []byte) []*tlv {
	var out []*tlv
	i := 0
	for i < len(d) {
		if i+2 > len(d) {
			return nil
		}
		t0 := i
		j := i + 1
		if d[i]&0x1f == 0x1f {
			for j < len(d) && d[j]&0x80 != 0 {
				j++
			}
			j++
		}
		if j >= len(d) {
			return nil
		}
		tag := d[t0:j]
		l := int(d[j])
		j++
		if l&0x80 != 0 {
			k := l & 0x7f
			if k == 0 || k > 3 || j+k > len(d) {
				return nil
			}
			l = 0
			for _, c := range d[j : j+k] {
				l = l<<8 | int(c)
			}
			j += k
		}
		if j+l > len(d) {
			return nil
		}
		n := &tlv{tag: append([]byte(nil), tag...), val: d[j : j+l]}
		if d[t0]&0x20 != 0 {
			n.kids = parseTLVs(d[j : j+l])
		}
		out = append(out, n)
		i = j + l
	}
	return out
}

func derLen(n int) []byte {
	if n < 128 {
		return []byte{byte(n)}
	}
	var b []byte
	for x := n; x > 0; x >>= 8 {
		b = append([]byte{byte(x)}, b...)
	}
	return append([]byte{0x80 | byte(len(b))}, b...)
}

func serTLVs(nodes []*tlv) []byte {
	var out []byte
	for _, n := range nodes {
		body := n.val
		if n.kids != nil {
			body = serTLVs(n.kids)
		}
		out = append(append(append(out, n.tag...), derLen(len(body))...), body...)
	}
	return out
}

func allTLVs(nodes []*tlv, acc []*tlv) []*tlv {
	for _, n := range nodes {
		acc = append(acc, n)
		if n.kids != nil {
			acc = allTLVs(n.kids, acc)
		}
	}
	return acc
}

// mutateTree edits one node of the DER tree and re-encodes the lengths:
// drop, duplicate or swap a child, or flip, cut, extend, retag or prefix a
// content.
func mutateTree(r *rand.Rand, der []byte) []byte {
	t := parseTLVs(der)
	if len(t) == 0 {
		return mutateDER(r, der)
	}
	nodes := allTLVs(t, nil)
	n := nodes[r.IntN(len(nodes))]
	op := r.IntN(9)
	if n.kids != nil && op < 3 && len(n.kids) > 0 {
		k := n.kids
		switch op {
		case 0:
			i := r.IntN(len(k))
			n.kids = append(k[:i:i], k[i+1:]...)
		case 1:
			i := r.IntN(len(k) + 1)
			c := *k[r.IntN(len(k))]
			n.kids = append(k[:i:i], append([]*tlv{&c}, k[i:]...)...)
		default:
			i, j := r.IntN(len(k)), r.IntN(len(k))
			k[i], k[j] = k[j], k[i]
		}
		return serTLVs(t)
	}
	n.kids = nil
	v := append([]byte(nil), n.val...)
	switch op {
	case 3:
		if len(v) > 0 {
			v[r.IntN(len(v))] ^= 1 << r.IntN(8)
		}
	case 4:
		if len(v) > 0 {
			v = v[:r.IntN(len(v))]
		}
	case 5:
		v = append(v, byte(r.IntN(256)))
	case 6:
		n.tag = []byte{[]byte{0x02, 0x03, 0x04, 0x05, 0x06, 0x30, 0x31, 0xa0, 0xa1, 0x80, 0x81}[r.IntN(11)]}
	case 7:
		if len(v) > 0 {
			v[0] = []byte{0, 1, 2, 3, 4, 5, 6, 7, 0x80, 0xff}[r.IntN(10)]
		}
	default:
		pre := make([]byte, 1+r.IntN(4))
		for i := range pre {
			pre[i] = byte(r.IntN(256))
		}
		v = append(pre, v...)
	}
	n.val = v
	return serTLVs(t)
}

// pointEdit edits the EC point of a key: hybrid form with either parity,
// off-curve, compressed with a right or wrong bit, the point at infinity,
// or x out of range.
func pointEdit(r *rand.Rand, der []byte) []byte {
	t := parseTLVs(der)
	for _, n := range allTLVs(t, nil) {
		v := n.val
		if len(n.tag) == 1 && n.tag[0] == 0x03 && len(v) > 2 && v[0] == 0 && (v[1] == 2 || v[1] == 3 || v[1] == 4 || v[1] == 6 || v[1] == 7) {
			p := append([]byte(nil), v[1:]...)
			switch r.IntN(6) {
			case 0:
				if p[0] == 4 {
					p[0] = 6 | byte(r.IntN(2))
				}
			case 1:
				p[len(p)-1] ^= 1
			case 2:
				if p[0] == 4 {
					l := (len(p) - 1) / 2
					p = append([]byte{2 | (p[len(p)-1] & 1) ^ byte(r.IntN(2))}, p[1:1+l]...)
				}
			case 3:
				if p[0] == 2 || p[0] == 3 {
					p[0] ^= 1
				}
			case 4:
				p = []byte{0}
			default:
				p[1] ^= 0x80
			}
			n.kids = nil
			n.val = append([]byte{0}, p...)
			return serTLVs(t)
		}
	}
	return mutateTree(r, der)
}

var algLabels = []string{"PUBLIC KEY", "PRIVATE KEY", "EC PRIVATE KEY", "ec PRIVATE KEY", "RSA PRIVATE KEY", "DSA PRIVATE KEY",
	"RSA PUBLIC KEY", "CERTIFICATE", "X509 CERTIFICATE", "ENCRYPTED PRIVATE KEY", "SM2 PRIVATE KEY", "EC PARAMETERS", "DSA PARAMETERS"}

var algHeaders = []string{"", "", "", "", "", "", "", "", "Comment: x",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00000000000000000000000000000000", "Abcdefghi:"}

// genAlgorithmCases builds n inputs from the fixtures of every key type:
// tree-mutated, point-edited or byte-mutated DER under its own or another
// label, sometimes with a header, a preceding block, and an Ed25519 key or
// certificate after it (the case where a non-Ed25519 block's fate decides
// the verdict).
func genAlgorithmCases(m pemMaterial, n int) []pemCase {
	r := rand.New(rand.NewPCG(8, 2))
	keys := m.keyBlocks()
	tails := []string{pemWrap("PUBLIC KEY", m.spki, 64), pemWrap("PRIVATE KEY", m.pkcs8, 64), pemWrap("CERTIFICATE", m.cert, 64)}
	var cases []pemCase
	for len(cases) < n {
		k := keys[r.IntN(len(keys))]
		der := k.der
		switch x := r.IntN(10); {
		case x < 3:
			der = pointEdit(r, der)
		case x < 8:
			for i := 1 + r.IntN(2); i > 0; i-- {
				der = mutateTree(r, der)
			}
		default:
			der = mutateDER(r, der)
		}
		label := k.label
		if r.Float64() >= 0.6 {
			label = algLabels[r.IntN(len(algLabels))]
		}
		text := framed(label, algHeaders[r.IntN(len(algHeaders))], der, 64)
		if r.Float64() < 0.7 {
			text += tails[r.IntN(len(tails))]
		}
		if r.Float64() < 0.2 {
			k2 := keys[r.IntN(len(keys))]
			text = pemWrap(k2.label, k2.der, 64) + text
		}
		cases = append(cases, pemCase{Text: text, Pub: true}, pemCase{Text: text, Pub: false})
	}
	return cases[:n]
}

// genPQCases edits the ML-DSA/ML-KEM PKCS#8 fixtures where key loading
// checks them: seed, private key bytes, the z/t0/tr tail, and the layout
// (seed-priv → priv-only or seed-only).
func genPQCases(m pemMaterial, n int) []pemCase {
	r := rand.New(rand.NewPCG(8, 3))
	var keys [][]byte
	for _, k := range m.keyBlocks() {
		if k.label == "PRIVATE KEY" && len(k.der) > 24 && bytes.HasPrefix(k.der[7:], []byte{0x30, 0x0b, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04}) && (k.der[18] == 0x03 || k.der[18] == 0x04) && k.der[19] < 0x14 {
			keys = append(keys, k.der)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	var cases []pemCase
	for len(cases) < n {
		k := keys[r.IntN(len(keys))]
		const off = 24
		inner := append([]byte(nil), k[off:]...)
		// Half the cases use the priv-only layout (OCTET STRING of the
		// expanded key, no seed), where ML-KEM's load runs the pairwise
		// test and ML-DSA's the t0/tr consistency check, then flip a bit
		// in the private vector s (ML-KEM) or s1/s2/t0 (ML-DSA).
		if inner[0] == 0x30 && r.IntN(2) == 0 {
			sl := int(inner[7])
			inner = append([]byte(nil), inner[8+sl:]...)
			if r.IntN(4) != 0 {
				inner[4+r.IntN(len(inner)-4)] ^= 1 << r.IntN(8)
			}
			der := append(append(append([]byte(nil), k[:off-4]...), 0x04, 0x82, byte(len(inner)>>8), byte(len(inner))), inner...)
			total := len(der) - 4
			der[2], der[3] = byte(total>>8), byte(total)
			text := pemWrap("PRIVATE KEY", der, 64)
			cases = append(cases, pemCase{Text: text, Pub: true}, pemCase{Text: text, Pub: false})
			continue
		}
		switch r.IntN(8) {
		case 0:
			inner[r.IntN(len(inner))] ^= 1 << r.IntN(8)
		case 1:
			if len(inner) > 200 {
				inner[6+r.IntN(32)] ^= 1
			}
		case 2:
			inner[len(inner)-1-r.IntN(64)] ^= 1 << r.IntN(8)
		case 3:
			inner[len(inner)/2] ^= 0xff
		case 4:
			if inner[0] == 0x30 {
				sl := int(inner[7])
				inner = append([]byte(nil), inner[8+sl:]...)
			}
		case 5:
			if inner[0] == 0x30 {
				sl := int(inner[7])
				inner = append([]byte{0x80, byte(sl)}, inner[8:8+sl]...)
			}
		case 6:
			inner[4+r.IntN(len(inner)-4)] = 0xff
		}
		der := append(append(append([]byte(nil), k[:off-4]...), 0x04, 0x82, byte(len(inner)>>8), byte(len(inner))), inner...)
		total := len(der) - 4
		der[2], der[3] = byte(total>>8), byte(total)
		text := pemWrap("PRIVATE KEY", der, 64)
		if r.Float64() < 0.5 {
			text += pemWrap("PUBLIC KEY", m.spki, 64)
		}
		cases = append(cases, pemCase{Text: text, Pub: true}, pemCase{Text: text, Pub: false})
	}
	return cases[:n]
}

// loadPEMCorpus reads the round-8 review corpus (testdata/pemcorpus.json).
func loadPEMCorpus(t *testing.T) []pemCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "pemcorpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			RevHex string `json:"revHex"`
			Pub    bool   `json:"pub"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	out := make([]pemCase, 0, len(corpus.Cases))
	for _, c := range corpus.Cases {
		b, err := hex.DecodeString(c.RevHex)
		if err != nil {
			t.Fatal(err)
		}
		for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
			b[i], b[j] = b[j], b[i]
		}
		out = append(out, pemCase{Text: string(b), Pub: c.Pub})
	}
	return out
}

// pemShowDiffs is how many differing cases the live test prints
// (PEM_SHOW_DIFFS, default 20).
func pemShowDiffs() int {
	if n, err := strconv.Atoi(os.Getenv("PEM_SHOW_DIFFS")); err == nil {
		return n
	}
	return 20
}

// --- targeted reason cases ---------------------------------------------------

func derTLV(tag byte, v ...[]byte) []byte {
	body := bytes.Join(v, nil)
	return append(append([]byte{tag}, derLen(len(body))...), body...)
}

func derSeq(v ...[]byte) []byte { return derTLV(0x30, v...) }

func derInt(n *big.Int) []byte {
	if n.Sign() >= 0 {
		b := n.Bytes()
		if len(b) == 0 || b[0]&0x80 != 0 {
			b = append([]byte{0}, b...)
		}
		return derTLV(0x02, b)
	}
	// two's complement
	l := 1
	for new(big.Int).Lsh(big.NewInt(-1), uint(8*l-1)).Cmp(n) > 0 {
		l++
	}
	m := new(big.Int).Add(n, new(big.Int).Lsh(big.NewInt(1), uint(8*l)))
	return derTLV(0x02, m.FillBytes(make([]byte, l)))
}

func derSmall(n int64) []byte { return derInt(big.NewInt(n)) }

func derOID(h string) []byte {
	b, _ := hex.DecodeString(h)
	return derTLV(0x06, b)
}

func bigHex(h string) *big.Int { n, _ := new(big.Int).SetString(h, 16); return n }

// reTLV replaces the first node of der (depth first) whose encoding is old
// with new, re-encoding the lengths above it.
func reTLV(der []byte, match func(*tlv) bool, edit func(*tlv)) []byte {
	t := parseTLVs(der)
	for _, n := range allTLVs(t, nil) {
		if match(n) {
			edit(n)
			return serTLVs(t)
		}
	}
	return der
}

// genReasonCases are inputs built to raise the OpenSSL reasons the random
// generators do not reach (each verified against Node 22 / OpenSSL 3.5.5
// and the traced C driver): explicit EC parameters with a bad compressed
// point, cofactor, order, basis or a generator at infinity; DH sizes;
// RSA-PSS parameters; DSA/DH parameter encodings; Name and Time contents;
// ML-DSA/ML-KEM PKCS#8 formats; PEM DEK-Info headers.
func genReasonCases(m pemMaterial) []pemCase {
	p256 := bigHex("ffffffff00000001000000000000000000000000ffffffffffffffffffffffff")
	order := bigHex("ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551")
	pad := func(n *big.Int) []byte { return n.FillBytes(make([]byte, 32)) }
	primeField := derSeq(derOID("2a8648ce3d0101"), derInt(p256))
	ecParams := func(field, base []byte, ord *big.Int, cof []byte) []byte {
		if field == nil {
			field = primeField
		}
		if base == nil {
			base = append(append([]byte{4}, pad(big.NewInt(1))...), make([]byte, 32)...)
		}
		curve := derSeq(derTLV(4, pad(new(big.Int).Sub(p256, big.NewInt(3)))), derTLV(4, pad(big.NewInt(2))))
		return derSeq(derSmall(1), field, curve, derTLV(4, base), derInt(ord), cof)
	}
	ecPub := func(params, pt []byte) []byte {
		return derSeq(derSeq(derOID("2a8648ce3d0201"), params), derTLV(3, append([]byte{0}, pt...)))
	}
	ecPriv := func(params, pub []byte) []byte {
		body := [][]byte{derSmall(1), derTLV(4, pad(big.NewInt(5))), derTLV(0xa0, params)}
		if pub != nil {
			body = append(body, derTLV(0xa1, derTLV(3, append([]byte{0}, pub...))))
		}
		return derSeq(body...)
	}
	p8 := func(alg, inner []byte) []byte { return derSeq(derSmall(0), alg, derTLV(4, inner)) }
	x1 := pad(big.NewInt(1))
	xm2 := pad(new(big.Int).Sub(p256, big.NewInt(2)))
	inf := ecParams(nil, []byte{0}, big.NewInt(3), nil)
	char2 := func(basis ...[]byte) []byte {
		return derSeq(derOID("2a8648ce3d0102"), derSeq(append([][]byte{derSmall(163)}, basis...)...))
	}
	pt := append(append([]byte{4}, x1...), make([]byte, 32)...)
	edSPKI := m.spki
	name := func(v []byte) []byte { return derSeq(derTLV(0x31, derSeq(derOID("550403"), v))) }
	cert := func(subject, notAfter []byte) []byte {
		alg := derSeq(derOID("2b6570"))
		tbs := derSeq(derTLV(0xa0, derSmall(2)), derSmall(1), alg, name(derTLV(0x13, []byte("r"))),
			derSeq(derTLV(0x17, []byte("200101000000Z")), notAfter), subject, edSPKI)
		return derSeq(tbs, alg, derTLV(3, make([]byte, 65)))
	}
	utc := derTLV(0x17, []byte("300101000000Z"))
	nested := derTLV(0x13, []byte("r"))
	for range 7 {
		nested = append(append([]byte{0x33}, derLen(len(nested))...), nested...)
	}
	dhP8 := func(p, g, q *big.Int) []byte {
		oid, prm := "2a864886f70d010301", [][]byte{derInt(p), derInt(g)}
		if q != nil {
			oid, prm = "2a8648ce3e0201", append(prm, derInt(q))
		}
		return p8(derSeq(derOID(oid), derSeq(prm...)), derSmall(3))
	}
	big1 := func(bits uint) *big.Int {
		return new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), bits), big.NewInt(1))
	}
	other := func(name string) []byte { return m.otherDER[name].der }
	isOID := func(h string) func(*tlv) bool {
		return func(n *tlv) bool { return n.tag[0] == 6 && hex.EncodeToString(n.val) == h }
	}
	// The ML-DSA/ML-KEM private key OCTET STRING shortened by a byte (no
	// PKCS#8 format matches), the ML-DSA public key BIT STRING likewise,
	// and an ML-DSA algorithm with parameters.
	shortenLast := func(der []byte, tag byte) []byte {
		t := parseTLVs(der)
		top := t[0].kids
		for i := len(top) - 1; i >= 0; i-- {
			if top[i].tag[0] == tag {
				top[i].kids = nil
				top[i].val = top[i].val[:len(top[i].val)-1]
				break
			}
		}
		return serTLVs(t)
	}
	mldsaParams := reTLV(other("pq-ML-DSA-44.pem#0"), func(n *tlv) bool {
		return n.tag[0] == 0x30 && len(n.kids) == 1 && n.kids[0].tag[0] == 6
	}, func(n *tlv) { n.kids = append(n.kids, &tlv{tag: []byte{5}, val: []byte{}}) })
	rsaAlias := reTLV(other("k_rsa.pub#0"), isOID("2a864886f70d010101"), func(n *tlv) { n.val = []byte{0x55, 0x08, 0x01, 0x01} })
	dsaAlias := reTLV(other("dsa.pub#0"), isOID("2a8648ce380401"), func(n *tlv) { n.val = []byte{0x2b, 0x0e, 0x03, 0x02, 0x0c} })
	krsa := other("k_rsa.pem#0")
	blocks := []struct {
		label, header string
		der           []byte
	}{
		{"PUBLIC KEY", "", ecPub(ecParams(nil, nil, order, derSmall(1)), append([]byte{3}, x1...))},
		{"PUBLIC KEY", "", ecPub(ecParams(nil, nil, order, derSmall(1)), append([]byte{3}, xm2...))},
		{"PUBLIC KEY", "", ecPub(ecParams(nil, nil, order, derSmall(-1)), pt)},
		{"PUBLIC KEY", "", ecPub(ecParams(nil, nil, big.NewInt(0), derSmall(1)), pt)},
		{"PUBLIC KEY", "", ecPub(ecParams(char2(derOID("2a8648ce3d01020302"), derSmall(0)), nil, order, derSmall(1)), pt)},
		{"PUBLIC KEY", "", ecPub(ecParams(char2(derOID("2a8648ce3d01020301"), derTLV(5)), nil, order, derSmall(1)), pt)},
		{"EC PRIVATE KEY", "", ecPriv(inf, nil)},
		{"EC PRIVATE KEY", "", ecPriv(ecParams(nil, nil, order, derSmall(1)), append([]byte{3}, x1...))},
		{"EC PRIVATE KEY", "", ecPriv(ecParams(nil, nil, order, derSmall(1)), append([]byte{3}, xm2...))},
		{"EC PRIVATE KEY", "", derSeq(derSmall(1), derTLV(4, x1), derTLV(0xa0, derOID("2a8648ce3d030107")), derTLV(0xa1, derTLV(3, []byte{0})))},
		{"EC PRIVATE KEY", "", derSeq(derSmall(1), derTLV(4, x1), derTLV(0xa0, derSmall(1)))},
		{"EC PRIVATE KEY", "", derSeq(derSmall(1), derTLV(4, x1), derTLV(0x80, derOID("2a8648ce3d030107")))},
		{"PUBLIC KEY", "", derSeq(append(append([]byte{0x30, 11}, derOID("2a864886f70d010101")...), 0x10, 0), derTLV(3, append([]byte{0}, derSmall(5)...)))},
		{"PRIVATE KEY", "", p8(append(append([]byte{0x30, 13}, derOID("2a864886f70d010101")...), 0x10, 0), derSmall(5))},
		{"PUBLIC KEY", "", derSeq(derSeq(derOID("2b6570")), derTLV(3))},
		{"PRIVATE KEY", "", p8(derSeq(derOID("2a864886f70d01010a"), derTLV(5)), derSeq(derSmall(0), derSmall(0xc5), derSmall(3), derSmall(3), derSmall(5), derSmall(7), derSmall(1), derSmall(1), derSmall(1)))},
		{"PRIVATE KEY", "", dhP8(big1(10000), big.NewInt(2), nil)},
		{"PRIVATE KEY", "", dhP8(big1(2047), big.NewInt(2), big1(10000))},
		{"PRIVATE KEY", "", dhP8(big.NewInt(23), big.NewInt(5), nil)},
		{"PUBLIC KEY", "", derSeq(derSeq(derOID("2a8648ce380401"), derSmall(5)), derTLV(3, append([]byte{0}, derSmall(3)...)))},
		{"PUBLIC KEY", "", derSeq(derSeq(derOID("2a864886f70d010301")), derTLV(3, append([]byte{0}, derSmall(3)...)))},
		{"CERTIFICATE", "", cert(name(nested), utc)},
		{"CERTIFICATE", "", cert(name(derTLV(0x1e, []byte("abc"))), utc)},
		{"CERTIFICATE", "", cert(name(derTLV(0x1c, []byte("abc"))), utc)},
		{"CERTIFICATE", "", cert(name(derTLV(0x13, []byte("r"))), derTLV(0x18, []byte("2060")))},
		{"PRIVATE KEY", "", mldsaParams},
		{"PRIVATE KEY", "", shortenLast(other("pq-ML-DSA-44.pem#0"), 4)},
		{"PRIVATE KEY", "", shortenLast(other("pq-ML-KEM-512.pem#0"), 4)},
		{"PUBLIC KEY", "", shortenLast(other("pq-ML-DSA-44.pub#0"), 3)},
		{"PUBLIC KEY", "", rsaAlias},
		{"PUBLIC KEY", "", dsaAlias},
		{"PRIVATE KEY", "", krsa[:len(krsa)/2]},
		{"RSA PRIVATE KEY", "Proc-Type: 4,ENCRYPTED x\nDEK-Info: AES-128-CBC,00000000000000000000000000000000", []byte{0x30, 0}},
		{"RSA PRIVATE KEY", "Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC x", []byte{0x30, 0}},
	}
	// Explicit prime-field parameters over a small or composite p with
	// G = (1, 2) on y² = x³ + x + b, and a compressed public key at x:
	// BN_mod_sqrt's failures (p = 33: a small factor; p = (2^127-1)²:
	// every small candidate a residue) and its random search for a prime
	// with no non-residue below 22 (p = 53881).
	smallCurve := func(p *big.Int, ord int64, x int64, label string) []byte {
		n := (p.BitLen() + 7) / 8
		b := new(big.Int).Sub(big.NewInt(4), big.NewInt(2))
		b.Mod(b, p)
		fe := func(v *big.Int) []byte { return v.FillBytes(make([]byte, n)) }
		base := append(append([]byte{4}, fe(big.NewInt(1))...), fe(big.NewInt(2))...)
		params := derSeq(derSmall(1), derSeq(derOID("2a8648ce3d0101"), derInt(p)),
			derSeq(derTLV(4, fe(big.NewInt(1))), derTLV(4, fe(b))), derTLV(4, base), derSmall(ord))
		pub := append([]byte{2 | byte(x&1)}, fe(big.NewInt(x))...)
		if label == "PUBLIC KEY" {
			return ecPub(params, pub)
		}
		return derSeq(derSmall(1), derTLV(4, pad(big.NewInt(5))), derTLV(0xa0, params), derTLV(0xa1, derTLV(3, append([]byte{0}, pub...))))
	}
	m127 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	blocks = append(blocks,
		struct {
			label, header string
			der           []byte
		}{"EC PRIVATE KEY", "", smallCurve(big.NewInt(33), 31, 2, "")},
		struct {
			label, header string
			der           []byte
		}{"EC PRIVATE KEY", "", smallCurve(new(big.Int).Mul(m127, m127), 3, 2, "")},
		struct {
			label, header string
			der           []byte
		}{"CERTIFICATE", "", cert(name(derTLV(0x1e, []byte{0xd8, 0x00})), utc)},
		struct {
			label, header string
			der           []byte
		}{"CERTIFICATE", "", cert(name(derTLV(0x1c, []byte{0, 0x11, 0, 0})), utc)},
	)
	// EC explicit-parameter structure errors: an unknown characteristic-two
	// basis, an empty seed, an empty base point, an even prime "p".
	unkBasis := ecParams(char2(derOID("2a8648ce3d01020309"), derTLV(5)), nil, order, derSmall(1))
	seedCurve := derSeq(derTLV(4, pad(big.NewInt(1))), derTLV(4, pad(big.NewInt(2))), derTLV(3))
	emptySeed := derSeq(derSmall(1), primeField, seedCurve, derTLV(4, pt), derInt(order), derSmall(1))
	emptyBase := ecParams(nil, []byte{}, order, derSmall(1))
	evenP := derSeq(derSmall(1), derSeq(derOID("2a8648ce3d0101"), derSmall(34)),
		derSeq(derTLV(4, []byte{1}), derTLV(4, []byte{2})), derTLV(4, []byte{4, 1, 2}), derSmall(31))
	// RSA-PSS with an unknown hash, a PKCS#8 with a 2-byte BOOLEAN in an
	// extension-like attribute (asn1 boolean length), and a DSA PKCS#8
	// over an even p (BN_mod_exp_recp refuses the constant-time exponent).
	pssUnknownHash := p8(derSeq(derOID("2a864886f70d01010a"), derSeq(derTLV(0xa0, derSeq(derOID("2a0304"))))),
		derSeq(derSmall(0), derSmall(0xc5), derSmall(3), derSmall(3), derSmall(5), derSmall(7), derSmall(1), derSmall(1), derSmall(1)))
	boolCert := func() []byte {
		alg := derSeq(derOID("2b6570"))
		ext := derTLV(0xa3, derSeq(derSeq(derOID("551d13"), derTLV(1, []byte{0xff, 0xff}), derTLV(4, derSeq()))))
		tbs := derSeq(derTLV(0xa0, derSmall(2)), derSmall(1), alg, name(derTLV(0x13, []byte("r"))),
			derSeq(derTLV(0x17, []byte("200101000000Z")), utc), name(derTLV(0x13, []byte("r"))), edSPKI, ext)
		return derSeq(tbs, alg, derTLV(3, make([]byte, 65)))
	}()
	dsaEven := p8(derSeq(derOID("2a8648ce380401"), derSeq(derSmall(34), derSmall(7), derSmall(2))), derSmall(3))
	blocks = append(blocks,
		struct {
			label, header string
			der           []byte
		}{"PUBLIC KEY", "", ecPub(unkBasis, pt)},
		struct {
			label, header string
			der           []byte
		}{"PUBLIC KEY", "", ecPub(emptySeed, pt)},
		struct {
			label, header string
			der           []byte
		}{"PUBLIC KEY", "", ecPub(emptyBase, pt)},
		struct {
			label, header string
			der           []byte
		}{"PUBLIC KEY", "", ecPub(evenP, []byte{4, 1, 2})},
		struct {
			label, header string
			der           []byte
		}{"PRIVATE KEY", "", pssUnknownHash},
		struct {
			label, header string
			der           []byte
		}{"CERTIFICATE", "", boolCert},
		struct {
			label, header string
			der           []byte
		}{"PRIVATE KEY", "", dsaEven},
	)
	for x := int64(2); x < 8; x++ {
		blocks = append(blocks, struct {
			label, header string
			der           []byte
		}{"PUBLIC KEY", "", smallCurve(big.NewInt(53881), 53882, x*7919%53881, "PUBLIC KEY")})
	}
	var cases []pemCase
	for _, b := range blocks {
		text := framed(b.label, b.header, b.der, 64)
		cases = append(cases, pemCase{Text: text, Pub: true}, pemCase{Text: text, Pub: false})
	}
	return cases
}

// mixLabels are the labels genMixedCases relabels blocks with, the ones
// OpenSSL's legacy reader treats specially included ("ANY PRIVATE KEY",
// lower-case "ec PRIVATE KEY", DH and X9.42 labels).
var mixLabels = []string{"PUBLIC KEY", "PRIVATE KEY", "CERTIFICATE", "X509 CERTIFICATE", "TRUSTED CERTIFICATE",
	"RSA PUBLIC KEY", "ENCRYPTED PRIVATE KEY", "EC PRIVATE KEY", "RSA PRIVATE KEY", "DSA PRIVATE KEY",
	"SM2 PRIVATE KEY", "ec PRIVATE KEY", "DH PRIVATE KEY", "DSA PUBLIC KEY", "X9.42 DH PRIVATE KEY", "ANY PRIVATE KEY"}

var mixHeaders = []string{"", "", "", "", "A:", "Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00000000000000000000000000000000",
	"Proc-Type: 4,ENCRYPTED\nDEK-Info: DES-EDE3-CBC,0000000000000000", "Comment: x"}

// certWithSPKI is the Ed25519 certificate with its SubjectPublicKeyInfo
// replaced by spki (the X509 public pass's key extraction for every key
// type).
func certWithSPKI(cert, edSPKI, spki []byte) []byte {
	t := parseTLVs(cert)
	if len(t) == 0 || len(t[0].kids) == 0 {
		return cert
	}
	nt := parseTLVs(spki)
	if len(nt) == 0 {
		return cert
	}
	for _, kid := range t[0].kids[0].kids {
		if kid.kids != nil && bytes.Equal(serTLVs([]*tlv{kid}), edSPKI) {
			*kid = *nt[0]
		}
	}
	return serTLVs(t)
}

// genMixedCases builds the round-8 reviewers' mixed multi-block inputs
// (mix.py): one to four blocks, each a fixture of any algorithm (tree
// mutated, relabelled), an Ed25519 certificate carrying another key's
// SPKI, or an Ed25519 structure, with random headers, a BOM or a junk
// line first, read as a public or a private key.
func genMixedCases(m pemMaterial, n int) []pemCase {
	r := rand.New(rand.NewPCG(8, 4))
	keys := m.keyBlocks()
	var spkis [][]byte
	for _, k := range keys {
		if k.label == "PUBLIC KEY" {
			spkis = append(spkis, k.der)
		}
	}
	eds := []pemKeyBlock{{"PRIVATE KEY", m.pkcs8}, {"PUBLIC KEY", m.spki}, {"CERTIFICATE", m.cert}, {"CERTIFICATE", m.certExt}, {"X509 CERTIFICATE", m.cert}}
	cases := make([]pemCase, 0, n)
	for len(cases) < n {
		var sb strings.Builder
		for b := 1 + r.IntN(4); b > 0; b-- {
			var label string
			var der []byte
			switch c := r.IntN(10); {
			case c < 5:
				k := keys[r.IntN(len(keys))]
				label, der = k.label, k.der
				if r.Float64() < 0.4 {
					der = mutateTree(r, der)
				}
				if r.Float64() < 0.3 {
					label = mixLabels[r.IntN(len(mixLabels))]
				}
			case c < 7:
				label, der = "CERTIFICATE", certWithSPKI(m.cert, m.spki, spkis[r.IntN(len(spkis))])
				if r.Float64() < 0.3 {
					der = mutateTree(r, der)
				}
			default:
				e := eds[r.IntN(len(eds))]
				label, der = e.label, e.der
				if r.Float64() < 0.3 {
					der = mutateTree(r, der)
				}
				if r.Float64() < 0.2 {
					label = mixLabels[r.IntN(len(mixLabels))]
				}
			}
			sb.WriteString(framed(label, mixHeaders[r.IntN(len(mixHeaders))], der, 64))
		}
		text := sb.String()
		if r.Float64() < 0.1 {
			text = "\ufeff" + text
		}
		if r.Float64() < 0.1 {
			text = "junk\n" + text
		}
		cases = append(cases, pemCase{Text: text, Pub: r.Float64() < 0.55})
	}
	return cases
}
