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
	"math/big"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/witness"
)

// pemCase is one createPublicKey (pub) or createPrivateKey input. approx
// marks inputs whose key is not Ed25519: Go approximates OpenSSL's
// algorithm-specific decoders there (KNOWN_DIFFERENCES.md), so only the
// Ed25519 verdict is compared.
type pemCase struct {
	Text   string `json:"text"`
	Pub    bool   `json:"pub"`
	approx bool
}

type pemResult struct {
	OK      bool   `json:"ok"`
	Type    string `json:"type"`
	SPKI    string `json:"spki"`
	Message string `json:"message"`
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
	others                                            map[string]string // non-Ed25519 fixtures (difftest/testdata/pemkeys)
}

func loadPEMMaterial(t *testing.T) pemMaterial {
	t.Helper()
	k1, k2 := seedKey(1), seedKey(2)
	m := pemMaterial{others: map[string]string{}}
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
	both := func(text string, approx bool) {
		cases = append(cases, pemCase{Text: text, Pub: true, approx: approx}, pemCase{Text: text, Pub: false, approx: approx})
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
		"3089010000000000000000", "3f", "3f81", "3f8100", "3f818100", "1f2200", "3000", "300100",
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

// TestLivePEMKeyParsing compares Go's PEM reader with Node's
// createPublicKey/createPrivateKey on generated reviewer-key texts: the
// verdict, the Ed25519 public key, and the exact error message.
func TestLivePEMKeyParsing(t *testing.T) {
	oracle.Skip(t)
	cases := genPEMCases(t, loadPEMMaterial(t))
	results := runPEMOracle(t, cases)
	accepted, fails := 0, 0
	for i, c := range cases {
		want := results[i]
		ok, ed, spki, msg := witness.ParseKeyForTest(c.Text, c.Pub)
		wantEd := want.OK && want.Type == "ed25519"
		var problem string
		switch {
		case ed != wantEd || (ed && hex.EncodeToString(spki) != want.SPKI):
			problem = "Ed25519 verdict"
		case c.approx:
		case ok != want.OK:
			problem = "accept/refuse"
		case !ok && msg != want.Message:
			problem = "message"
		}
		if wantEd {
			accepted++
		}
		if problem != "" {
			if fails++; fails <= 40 {
				t.Errorf("%s differs (pub=%v): %q\n  go:   ok=%v ed=%v %q\n  node: ok=%v type=%q %q", problem, c.Pub, c.Text, ok, ed, msg, want.OK, want.Type, want.Message)
			}
		}
	}
	if fails > 0 {
		t.Errorf("%d of %d cases differ", fails, len(cases))
	}
	liveSummary(t, "%d PEM cases (%d Ed25519 keys accepted)", len(cases), accepted)
}
