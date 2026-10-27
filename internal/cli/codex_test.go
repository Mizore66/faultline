package cli

import (
	"testing"
)

const codexUsage = "The current build exposes an observed Codex hook sidecar, quarantined turn-snapshot GC, and a clean Git snapshot. Run: fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes, fl codex snapshot gc --repo <directory>, fl codex --dry-run, or fl codex --snapshot --repo <directory>"

func TestCodexRouterFallsThrough(t *testing.T) {
	for _, args := range [][]string{{"codex"}, {"codex", "snapshot"}, {"codex", "nope", "gc"}} {
		_, stderr, code := run(args...)
		if code != 1 || stderr != "FaultLine error: "+codexUsage+"\nNext: pnpm fl help\n" {
			t.Errorf("%q: %d %q", args, code, stderr)
		}
	}
}

// TestCodexRouterOrder pins src/cli-app.ts:2994-3028's order through the
// stubs each branch reaches (the snapshot branches: TestCodexSnapshotRouter).
func TestCodexRouterOrder(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"codex", "sidecar", "--dry-run"}, "codex sidecar"},
	}
	for _, c := range cases {
		_, stderr, code := run(c.args...)
		if want := "FaultLine error: not yet ported: " + c.want + "\nNext: pnpm fl help\n"; code != 1 || stderr != want {
			t.Errorf("%q: %d %q", c.args, code, stderr)
		}
	}
}
