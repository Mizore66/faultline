package cli

import (
	"strings"
	"testing"
)

func TestTurnsLatestAndLedgerConflict(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := run("investigate", "turns", "--proposal", "p", "--repo", dir, "--latest", "--ledger", "l.json")
	if code != 1 || stderr != "FaultLine error: fl investigate turns accepts only one of --ledger <ledger.json> or --latest.\nNext: pnpm fl help\n" {
		t.Fatalf("%d %q", code, stderr)
	}
}

func TestTurnsRequiresALedger(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := run("investigate", "turns", "--proposal", "p", "--repo", dir)
	if code != 1 || !strings.Contains(stderr, "fl investigate turns requires --ledger <ledger.json>, --latest, or a local session binding ledger path.") {
		t.Fatalf("%d %q", code, stderr)
	}
}
