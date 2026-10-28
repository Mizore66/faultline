package turnproof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func readBase(t *testing.T, rel string) jsjson.Value {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(base(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWriteRefusesInjectedRunnerResult(t *testing.T) {
	result := readBase(t, "investigation.json")
	proof := result.Get("proof").Obj()
	proof.Set("executionTrust", jsjson.MakeString("INJECTED_RUNNER"))
	proofRoot := t.TempDir()
	_, err := Write(filepath.Join(proofRoot, "x"), result, readBase(t, "witness/frozen.json"),
		WriteOptions{ProofRoot: proofRoot, LifecycleLedger: readBase(t, "lifecycle/ledger.json"), Repository: t.TempDir()})
	if err == nil || !strings.HasPrefix(err.Error(), "Cannot write semantically inconsistent turn proof bundle: ") ||
		!strings.Contains(err.Error(), "Turn proof bundle requires native Docker executor provenance") {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(proofRoot); len(entries) != 0 {
		t.Fatal("nothing may be written before the semantics pass")
	}
}

func TestVerifyMarkdownHasNoTrailingNewline(t *testing.T) {
	b := verificationReadme()
	if strings.HasSuffix(string(b), "\n") || !strings.Contains(string(b), "Experimental turn-level evidence — not yet a portable proof (`EXPERIMENTAL_TURN`).") {
		t.Fatalf("%q", b)
	}
	raw, _ := os.ReadFile(filepath.Join(base(t), "VERIFY.md"))
	if string(raw) != string(b) {
		t.Fatal("VERIFY.md differs from the base")
	}
}
