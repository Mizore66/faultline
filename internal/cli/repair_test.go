package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRepairBriefSourceRules(t *testing.T) {
	cases := map[string][]string{
		"Choose exactly one repair brief source: --live or --input <repair-brief.json>.":                                 {"repair", "brief", "--input", ""},
		"Choose exactly one proof source: --bundle <directory> or --investigation <verified-bundle>/investigation.json.": {"repair", "brief", "--live"},
		"Usage: fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]":                                {"repair", "verify"},
	}
	for want, args := range cases {
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != 1 || !strings.HasPrefix(errb.String(), "FaultLine error: "+want+"\n") {
			t.Errorf("%v: %d %q", args, code, errb.String())
		}
	}
}
