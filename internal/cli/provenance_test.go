package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvenanceVerifyReadsTrustFirst(t *testing.T) {
	dir := t.TempDir()
	trust := filepath.Join(dir, "trust.json")
	os.WriteFile(trust, []byte(`{"schemaVersion":"faultline.github-artifact-attestation-trust.v1"}`), 0o644)
	var out, errb bytes.Buffer
	code := Run([]string{"provenance", "verify", "--trust", trust}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), `"path": [`) || strings.Contains(errb.String(), "Missing required option: --bundle") {
		t.Fatalf("%d %q", code, errb.String())
	}
}

func TestProvenanceUsageAndMissingBundle(t *testing.T) {
	for args, want := range map[string]string{
		"provenance":        "FaultLine error: Usage: fl provenance create|verify ...",
		"provenance create": "FaultLine error: Missing required option: --bundle",
	} {
		var out, errb bytes.Buffer
		code := Run(strings.Fields(args), &out, &errb)
		if code != 1 || !strings.HasPrefix(errb.String(), want) {
			t.Fatalf("%q: %d %q", args, code, errb.String())
		}
	}
}
