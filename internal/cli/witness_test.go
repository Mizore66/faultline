//go:build faultline_test

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWitnessUsageErrors(t *testing.T) {
	for _, c := range []struct{ args, stderr string }{
		{"witness approve", "FaultLine error: Usage: fl witness approve <proposal-id> --approved-by <actor>\nNext: pnpm fl help\n"},
		{"witness freeze", "FaultLine error: Usage: fl witness freeze <proposal-id> [--repo <directory>] [--draft-store <directory>]\nNext: pnpm fl help\n"},
		{"witness approve x --approved-by --note", "FaultLine error: Missing required option: --approved-by\nNext: pnpm fl help\n"},
	} {
		var out, errb bytes.Buffer
		if code := Run(strings.Fields(c.args), &out, &errb); code != 1 || errb.String() != c.stderr {
			t.Fatalf("%s: %d %q", c.args, code, errb.String())
		}
	}
}

func TestWitnessProposeSampleAndUnported(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"witness", "propose"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "{\n  \"source\": \"sample\",\n  \"proposal\": {\n    \"behavior\": ") {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
	for _, args := range []string{"witness sign x", "witness propose --live", "witness verify x --keyring k.json"} {
		var out, errb bytes.Buffer
		if code := Run(strings.Fields(args), &out, &errb); code != 1 || !strings.HasPrefix(errb.String(), "FaultLine error: not yet ported: witness ") {
			t.Fatalf("%s: %q", args, errb.String())
		}
	}
}
