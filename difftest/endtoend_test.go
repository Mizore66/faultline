//go:build faultline_test && unix

package difftest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEndWithoutNode is done criterion 3 of the slice 2 spec (4.2): the
// slice 2 commands chain end to end on the Go binary with no `node` on PATH
// (only git, the sh recorders and the pass-fail fake Docker): witness
// propose, approve and freeze, investigate git, verify of the bundle it
// wrote, and judge-proof --export-only on that bundle. POSIX only: the
// recorders and the fake Docker are sh scripts.
func TestEndToEndWithoutNode(t *testing.T) {
	ws := scenarioWorkspace(t, "git-two-states")
	r := rootsOf(ws)
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := lookPathIn("node", dir); err != nil {
			kept = append(kept, dir)
		}
	}
	path := strings.Join(append([]string{r.recorder, filepath.Join(repoRoot(t), "difftest", "testdata", "fakedocker", "pass-fail")}, kept...), string(os.PathListSeparator))
	if p, err := lookPathIn("node", path); err == nil {
		t.Fatalf("node still on PATH: %s", p)
	}
	if _, err := lookPathIn("git", path); err != nil {
		t.Fatal("git is not on PATH")
	}
	env := map[string]string{"PATH": path}
	// A store of its own: the scenario already holds a frozen demo-1, and
	// its session binding (which investigate inherits) names that witness.
	const store = "e2e-store"
	if err := os.Remove(filepath.Join(ws, "repo", ".faultline", "incidents", "bindings", "demo-1.json")); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		stdout, stderr, code := runFlIn(t, ws, env, args...)
		if code != 0 {
			t.Fatalf("fl %s: exit %d\n%s%s", strings.Join(args, " "), code, stdout, stderr)
		}
		return stdout
	}
	run("witness", "propose", "--input", "proposal.json", "--store", store)
	run("witness", "approve", "demo-1", "--approved-by", "r@example.test", "--store", store)
	run("witness", "freeze", "demo-1", "--store", store, "--repo", "repo")
	bundle := proofDirectory(t, run("investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD",
		"--proposal", "demo-1", "--image", pinnedImage, "--store", store))
	root := expectedRoot(t, bundle)
	if out := run("verify", bundle, "--expect-root", root); !strings.Contains(out, "Integrity: VALID") || !strings.Contains(out, "External root: MATCH") {
		t.Fatalf("verify:\n%s", out)
	}
	if out := run("judge-proof", "--bundle", bundle, "--expect-root", root, "--export-only"); !strings.Contains(out, "Verified root summary: "+root) {
		t.Fatalf("judge-proof:\n%s", out)
	}
}
