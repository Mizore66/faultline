//go:build faultline_test

package difftest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writerScenarios are the scenarios difftest/gen/scenarios.ts builds (its
// SCENARIOS) into difftest/testdata/scenarios/<name>/. Go never rebuilds a
// scenario: both sides copy the committed files, which frozen TS wrote under
// the seed preload.
var writerScenarios = map[string]bool{
	"empty":                      true,
	"commit-proof-sample":        true,
	"proof-bases":                true,
	"witness-proposed":           true,
	"witness-approved":           true,
	"witness-frozen":             true,
	"witness-approved-keys":      true,
	"witness-frozen-keys":        true,
	"witness-signed":             true,
	"witness-signed-forged":      true,
	"witness-overlay-policies":   true,
	"witness-nooverlay-policies": true,
	"git-two-states-bundle":      true,
	"ledger-bound":               true,
	"git-two-states":             true,
	"git-many-states":            true,
	"git-max-states-overflow":    true,
	"git-heterogeneous":          true,
	"git-ledger-partial":         true,
	"git-ledger-full":            true,
	"git-unbound":                true,
	"git-sha256":                 true,
	"git-cjk-overlays":           true,
	"git-non-monotonic":          true,
	"git-all-pass":               true,
	"git-all-fail":               true,
	"git-short-timeout":          true,
	"git-hostile-trees":          true,
	"git-descriptor-change":      true,
	"git-many-files":             true,
	"git-unstable-middle":        true,
	"git-two-recoveries":         true,
	"git-overlay-symlink-middle": true,
	"git-overlay-symlink-pair":   true,
	"repo-clean":                 true,
	"repo-dirty":                 true,
	"repo-rename":                true,
	"not-a-repo":                 true,
	"runtime-markers-multiple":   true,
	"runtime-markers-go":         true,
	"hostile-fsmonitor":          true,
	"project-context":            true,
	"repo-with-config":           true,
	"init-fresh":                 true,
	"init-existing-config":       true,
	"init-existing-ignore":       true,
	"init-existing-hooks":        true,
	"init-dirty-secret":          true,
	"init-worktree":              true,
	"init-sidecar":               true,
	"incident-repo":              true,
	"incident-upstream":          true,
	"incident-root":              true,
	"incident-merge":             true,
	"incident-command-file":      true,
	"incident-two-states":        true,
	"incident-two-states-frozen": true,
	"incident-no-runtime":        true,
	"incident-all-pass":          true,
	"incident-binding-mismatch":  true,
	"incident-unbound":           true,
	"incident-foreign-proposal":  true,
	"incident-ledger-full":       true,
	"prevention-input":           true,
	"prevention-from-bundle":     true,
	// Slice 4a: fl record, codex sidecar, codex snapshot gc.
	"record-repo":      true,
	"record-ledgers":   true,
	"record-dirty":     true,
	"record-fsmonitor": true,
	"sidecar-repo":     true,
	"sidecar-dirty":    true,
	"sidecar-no-head":  true,
	"sidecar-worktree": true,
	"sidecar-recorded": true,
	"snapgc-repo":      true,
}

// committedGitDir is what a scenario repository's .git directory is
// committed as: git refuses a path with a .git component (scenarios.ts
// COMMITTED_GIT_DIR). dirs.txt keeps the real names.
const committedGitDir = "dot-git"

// copyScenario fills the workspace ws from a committed scenario:
// <name>/dirs.txt lists every directory, one per line (git keeps no empty
// directories, and the witness store makes some), <name>/tree/ holds the
// files.
func copyScenario(t testing.TB, repo, name, ws string) {
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
		copyScenarioTree(t, filepath.Join(dir, "tree"), ws)
	}
}

// copyScenarioTree copies a committed tree/ into ws, turning each
// committedGitDir component back into .git.
func copyScenarioTree(t testing.TB, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for i, p := range parts {
			if p == committedGitDir {
				parts[i] = ".git"
			}
		}
		dest := filepath.Join(to, filepath.FromSlash(strings.Join(parts, "/")))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// refreshIndexFile lists, one per line, the repositories of a scenario
// whose index a case refreshes (scenarios.ts REFRESH_INDEX_FILE).
const refreshIndexFile = "refresh-index.txt"

// refreshScenarioIndexes refreshes the stat data of the indexes the
// scenario lists in refresh-index.txt, once the case's workspace is in
// place: a scenario index records none, so a clean-checkpoint capture would
// see every file as modified (scenarios.ts refreshScenarioIndexes). Git's
// own configuration and the repository's fsmonitor are out of play.
func refreshScenarioIndexes(t testing.TB, repo, name, ws string) {
	t.Helper()
	list, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", "scenarios", name, refreshIndexFile))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range strings.Split(string(list), "\n") {
		if rel == "" {
			continue
		}
		cmd := exec.Command("git", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/nonexistent/faultline-hooks", "-C", filepath.Join(ws, filepath.FromSlash(rel)), "update-index", "-q", "--refresh")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
		var exitErr *exec.ExitError
		if out, err := cmd.CombinedOutput(); err != nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 1) {
			t.Fatalf("git update-index --refresh in %s: %v\n%s", rel, err, out)
		}
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
func resetModes(t testing.TB, root string) {
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
