package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRepairExitCodes(t *testing.T) {
	for status, want := range map[string]int{
		"BUNDLE_INVALID": 1, "REPAIR_SETUP_FAILED": 1, "REPAIR_VERIFICATION_FAILED": 1, "WORKTREE_CLEANUP_FAILED": 1,
		"CODEX_UNAVAILABLE": 0, "REPAIR_INSTRUCTIONS_PREPARED": 0, "REPAIR_WORKTREE_READY": 0, "CODEX_DRAFT_RECORDED": 0, "REPAIR_CANDIDATE_VERIFIED": 0,
	} {
		if got := repairExitCode(status); got != want {
			t.Errorf("%s: %d", status, got)
		}
	}
}

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
