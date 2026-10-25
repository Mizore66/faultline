package turnsnap

import (
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/allowlist"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// fixtureSecret is the doctor --security / tests/turn-snapshot.test.ts:325
// OpenAI-shaped key, assembled at run time so no secret-shaped literal is
// committed.
func fixtureSecret() string {
	return strings.Join([]string{"sk", "proj", "abcdefghijklmnopqrstuvwxyz012345"}, "-")
}

const (
	highRefusal    = "Refusing turn-tree snapshot: high-confidence secret material detected in "
	highAdvice     = "Exclude the path via .faultlineignore after human review, remove the secret, or add a path+kind+occurrenceDigest entry in .faultline-secret-allowlist.json (never a bare path allow)."
	entropyRefusal = "Refusing turn-tree snapshot: high-entropy secret-like token detected in "
	entropyAdvice  = " (rule/kind: HIGH_ENTROPY_TOKEN). Exclude via .faultlineignore after human review, remove the token, or add a digest-bound allowlist entry (path + kind HIGH_ENTROPY_TOKEN + occurrenceDigest)."
)

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func randomToken(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return string(b)
}

func TestAssertNoSecretsRefusesHighConfidence(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "leaked.txt", "token = "+fixtureSecret()+"\n")
	err := AssertNoSecrets(dir, []string{"leaked.txt"}, allowlist.Empty())
	want := highRefusal + "leaked.txt (rules/kinds: OPENAI_API_KEY). " + highAdvice
	if _, ok := err.(*Error); !ok || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	// Kinds appear once each, in first-seen order.
	write(t, dir, "two.txt", "a "+fixtureSecret()+" "+"AKIA"+"ABCDEFGHIJKLMNOP"+" "+fixtureSecret()+"x\n")
	err = AssertNoSecrets(dir, []string{"two.txt"}, allowlist.Empty())
	if err == nil || !strings.Contains(err.Error(), "(rules/kinds: OPENAI_API_KEY, AWS_ACCESS_KEY_ID).") {
		t.Fatalf("got %v", err)
	}
}

func TestAssertNoSecretsEntropy(t *testing.T) {
	dir := t.TempDir()
	r := rand.New(rand.NewPCG(1, 2))
	hex40 := randomToken(r, "0123456789abcdef", 40)
	write(t, dir, "hex.txt", "commit "+hex40+"\n")
	if err := AssertNoSecrets(dir, []string{"hex.txt"}, allowlist.Empty()); err != nil {
		t.Fatalf("hex token refused: %v", err)
	}
	b64 := randomToken(r, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+=", 48)
	if !IsSuspiciousHighEntropyToken(b64) {
		t.Fatalf("token %s entropy %v", b64, shannonEntropy(b64))
	}
	write(t, dir, "b64.txt", "value: "+b64+"\n")
	err := AssertNoSecrets(dir, []string{"b64.txt"}, allowlist.Empty())
	if _, ok := err.(*Error); !ok || err.Error() != entropyRefusal+"b64.txt"+entropyAdvice {
		t.Fatalf("got %v", err)
	}
	// Path-like, integrity and hex tokens are not suspicious.
	for _, tok := range []string{"a/" + b64, "sha512-" + b64, "SHA256-" + b64, strings.ToUpper(hex40) + "abc"} {
		if IsSuspiciousHighEntropyToken(tok) {
			t.Errorf("%s flagged", tok)
		}
	}
	// A digest-bound HIGH_ENTROPY_TOKEN entry lets it through; a wrong file
	// digest does not.
	fileDigest, _ := allowlist.FileContentDigest(filepath.Join(dir, "b64.txt"), SecretScanBytes)
	entry := func(fd string) allowlist.Allowlist {
		o := jsjson.NewObj()
		o.Set("path", jsjson.MakeString("b64.txt"))
		o.Set("kind", jsjson.MakeString("HIGH_ENTROPY_TOKEN"))
		o.Set("occurrenceDigest", jsjson.MakeString("sha256:"+canonical.SHA256Hex(b64)))
		o.Set("fileDigest", jsjson.MakeString(fd))
		doc := jsjson.NewObj()
		doc.Set("schemaVersion", jsjson.MakeString(allowlist.SchemaVersion))
		doc.Set("entries", jsjson.MakeArray([]jsjson.Value{jsjson.MakeObject(o)}))
		a, err := allowlist.FromValue(jsjson.MakeObject(doc))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	if err := AssertNoSecrets(dir, []string{"b64.txt"}, entry(fileDigest)); err != nil {
		t.Fatalf("allowlisted: %v", err)
	}
	if err := AssertNoSecrets(dir, []string{"b64.txt"}, entry("sha256:"+strings.Repeat("0", 64))); err == nil {
		t.Fatal("wrong file digest accepted")
	}
}

func TestAssertNoSecretsAllowlistedHigh(t *testing.T) {
	dir := t.TempDir()
	body := "key " + fixtureSecret() + "\n"
	write(t, dir, "k.txt", body)
	o := jsjson.NewObj()
	o.Set("path", jsjson.MakeString("k.txt"))
	o.Set("kind", jsjson.MakeString("OPENAI_API_KEY"))
	o.Set("occurrenceDigest", jsjson.MakeString("sha256:"+canonical.SHA256Hex(fixtureSecret())))
	doc := jsjson.NewObj()
	doc.Set("schemaVersion", jsjson.MakeString(allowlist.SchemaVersion))
	doc.Set("entries", jsjson.MakeArray([]jsjson.Value{jsjson.MakeObject(o)}))
	a, err := allowlist.FromValue(jsjson.MakeObject(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := AssertNoSecrets(dir, []string{"k.txt"}, a); err != nil {
		t.Fatalf("allowlisted HIGH occurrence refused: %v", err)
	}
}

func TestAssertNoSecretsSkipsAndCaps(t *testing.T) {
	dir := t.TempDir()
	r := rand.New(rand.NewPCG(3, 4))
	b64 := randomToken(r, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", 48)
	write(t, dir, "empty", "")
	write(t, dir, "bin", "\x00"+fixtureSecret())
	// A NUL after the first 8 KiB does not make the file binary.
	write(t, dir, "late-nul", strings.Repeat("x", 8192)+"\x00 "+b64)
	// Content past 256 KiB is not scanned.
	write(t, dir, "big", strings.Repeat("x\n", SecretScanBytes/2)+fixtureSecret())
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	if err := AssertNoSecrets(dir, []string{"missing", "empty", "bin", "big", "sub"}, allowlist.Empty()); err != nil {
		t.Fatal(err)
	}
	if err := AssertNoSecrets(dir, []string{"late-nul"}, allowlist.Empty()); err == nil {
		t.Fatal("late NUL treated as binary")
	}
	// chmod 000 denies reads only on POSIX: on Windows it sets the read-only
	// attribute, and the file stays readable (for Node too).
	if os.Getuid() != 0 && runtime.GOOS != "windows" {
		write(t, dir, "locked", "x")
		os.Chmod(filepath.Join(dir, "locked"), 0o000)
		err := AssertNoSecrets(dir, []string{"locked"}, allowlist.Empty())
		if err == nil || err.Error() != "Could not read locked for snapshot secret scanning: EACCES: permission denied, open '"+filepath.Join(dir, "locked")+"'" {
			t.Fatalf("unreadable: %v", err)
		}
	}
}

// TestJSLog2 checks the V8 port against values from Node 22.22.2's
// Math.log2 where Go's math.Log2 rounds differently.
func TestJSLog2(t *testing.T) {
	for _, c := range []struct {
		num, den float64
		bits     uint64
	}{
		{11, 40, 0xbffdccc91ae68c5e},
		{12, 40, 0xbffbca9c6f53897b},
		{21, 40, 0xbfedbf5ee1570241},
		{39, 40, 0xbfa2b38505f8a291},
		{35, 41, 0xbfcd37eb0e5f9fda},
	} {
		if got := math.Float64bits(jsLog2(c.num / c.den)); got != c.bits {
			t.Errorf("log2(%v/%v) = %x, want %x", c.num, c.den, got, c.bits)
		}
	}
	if jsLog2(1) != 0 || !math.IsInf(jsLog2(0), -1) || jsLog2(8) != 3 || jsLog2(math.SmallestNonzeroFloat64) != -1074 {
		t.Fatal("special values")
	}
}

func TestShannonEntropyCountsCharacters(t *testing.T) {
	// for...of yields a surrogate pair as one character; length counts
	// units: "😀😀" has one character of probability 2/4.
	if got := shannonEntropy("\U0001F600\U0001F600"); got != 0.5 {
		t.Fatalf("pair entropy %v", got)
	}
	if shannonEntropy("") != 0 || shannonEntropy("aaaa") != 0 || shannonEntropy("ab") != 1 {
		t.Fatal("basic entropy")
	}
}
