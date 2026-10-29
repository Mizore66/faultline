package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexDryRun(t *testing.T) {
	stdout, stderr, code := run("codex", "--dry-run")
	want := "{\n  \"adapter\": \"observed-codex-hook-sidecar\",\n  \"status\": \"DRY_RUN\",\n  \"records\": [\n    \"public session and turn identifiers\",\n    \"prompt digest only\",\n    \"clean Git checkpoint when available\"\n  ],\n  \"limitation\": \"Install and trust the emitted hook configuration to observe public lifecycle metadata. FaultLine does not intercept private model state, transcripts, or reasoning.\"\n}\n"
	if code != 0 || stderr != "" || stdout != want {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
}

// TestCodexSnapshotRouter pins the snapshot branches of
// src/cli-app.ts:2994-3028: gc wins over --dry-run, --dry-run over
// --snapshot, and `snapshot` without gc falls through to the flags.
func TestCodexSnapshotRouter(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"codex", "snapshot", "--dry-run"}, {"codex", "--snapshot", "--dry-run"}, {"codex", "foo", "--snapshot", "--dry-run"}} {
		stdout, _, code := run(args...)
		if code != 0 || !strings.Contains(stdout, `"status": "DRY_RUN"`) {
			t.Errorf("%q: %d %q", args, code, stdout)
		}
	}
	stdout, stderr, code := run("codex", "snapshot", "gc", "--dry-run", "--repo", dir)
	want := "{\n  \"status\": \"ALREADY_EMPTY\",\n  \"repositoryRoot\": " + quote(dir) + ",\n  \"objectDirectory\": " + quote(filepath.Join(dir, ".git", "faultline", "objects")) +
		",\n  \"removedEntries\": 0,\n  \"referencingPaths\": [],\n  \"next\": \"fl doctor --security\"\n}\n"
	if code != 0 || stderr != "" || stdout != want {
		t.Errorf("gc: %d %q %q", code, stdout, stderr)
	}
	// Not a repository: the snapshot branch runs git and reports its failure.
	_, stderr, code = run("codex", "gc", "snapshot", "--snapshot", "--repo", dir)
	if code != 1 || !strings.HasPrefix(stderr, "FaultLine error: Git sidecar capture failed for rev-parse --show-toplevel: ") {
		t.Errorf("snapshot: %d %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".faultline")); !os.IsNotExist(err) {
		t.Errorf("a failed capture wrote .faultline: %v", err)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
