package difftest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writerScenarios are the scenarios difftest/gen/scenarios.ts builds (its
// SCENARIOS) into difftest/testdata/scenarios/<name>/. Go never rebuilds a
// scenario: both sides copy the committed files, which frozen TS wrote under
// the seed preload.
var writerScenarios = map[string]bool{
	"empty":                 true,
	"witness-proposed":      true,
	"witness-approved":      true,
	"witness-frozen":        true,
	"witness-approved-keys": true,
	"witness-frozen-keys":   true,
	"witness-signed":        true,
	"git-two-states-bundle": true,
}

// copyScenario fills the workspace ws from a committed scenario:
// <name>/dirs.txt lists every directory, one per line (git keeps no empty
// directories, and the witness store makes some), <name>/tree/ holds the
// files.
func copyScenario(t *testing.T, repo, name, ws string) {
	t.Helper()
	if !writerScenarios[name] {
		t.Fatalf("unknown scenario %q (add it to difftest/gen/scenarios.ts and writerScenarios)", name)
	}
	dir := filepath.Join(repo, "difftest", "testdata", "scenarios", name)
	dirs, err := os.ReadFile(filepath.Join(dir, "dirs.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range strings.Split(string(dirs), "\n") {
		if rel != "" {
			if err := os.MkdirAll(filepath.Join(ws, filepath.FromSlash(rel)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tree")); err == nil {
		copyTree(t, filepath.Join(dir, "tree"), ws)
	}
}

// checkScenarioDirs fails when a committed scenario directory has no
// builder: scenarios.ts removes those, so one left over is stale.
func checkScenarioDirs(t *testing.T, repo string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, "difftest", "testdata", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !writerScenarios[e.Name()] {
			t.Errorf("difftest/testdata/scenarios/%s has no builder: run difftest/gen/scenarios.ts", e.Name())
		}
	}
}

// resetModes sets directories to 0755 and files to 0644 under root, once a
// case's scenario and mutations are in place (scenarios.ts resetModes), so
// the workspace fl starts from never depends on the checkout or on how the
// harness wrote it.
func resetModes(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case path == root:
			return nil
		case d.IsDir():
			return os.Chmod(path, 0o755)
		case d.Type().IsRegular():
			return os.Chmod(path, 0o644)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
