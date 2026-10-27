package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestAttestCreateVerifiesBundleBeforeOptions(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"attest", "create", "--bundle", t.TempDir(), "--receipt", "r1"}, &out, &errb)
	if code != 1 || !strings.HasPrefix(errb.String(), "FaultLine error: Proof bundle is invalid: ") {
		t.Fatalf("%d %q", code, errb.String())
	}
}
