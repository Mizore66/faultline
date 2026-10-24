//go:build faultline_test && unix

package difftest

import (
	"os"
	osexec "os/exec"
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

// TestEndToEndSlice3aWithoutNode is done criterion 3 of the slice 3 spec
// (4.2, PR 3a; plan correction 5): `fl init --yes`, `fl doctor
// --proof-ready` and `fl runtime prepare node --yes` on the Go binary in a
// repository, with PATH holding only git's own directory, the runtime-digest
// fake Docker and its hash tool, and no `node`. Without Node,
// doctor's node check is UNAVAILABLE and doctor exits 1 by design (slice 3
// spec 3.1), so --proof-ready prints NOT READY; init and runtime prepare
// succeed, and the config holds the prepared image. POSIX only: the fake
// Docker is a sh script.
func TestEndToEndSlice3aWithoutNode(t *testing.T) {
	ws := scenarioWorkspace(t, "init-fresh")
	exec, err := osexec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	// The fake Docker runs under #!/bin/sh and needs only sha256sum (or
	// shasum) from PATH: a directory of its own links that one tool, so no
	// system directory (which may hold a node) is on PATH.
	tools := t.TempDir()
	linked := false
	for _, name := range []string{"sha256sum", "shasum"} {
		if p, err := osexec.LookPath(name); err == nil {
			if err := os.Symlink(p, filepath.Join(tools, name)); err != nil {
				t.Fatal(err)
			}
			linked = true
			break
		}
	}
	if !linked {
		t.Fatal("neither sha256sum nor shasum is on PATH")
	}
	// git's own directory: /usr/bin/git on macOS is an xcrun shim.
	var dirs []string
	for _, dir := range []string{strings.TrimSpace(string(exec)), filepath.Join(repoRoot(t), "difftest", "testdata", "fakedocker", "runtime-digest"), tools} {
		if _, err := lookPathIn("node", dir); err == nil {
			t.Fatalf("%s holds a node, so PATH cannot leave Node out", dir)
		}
		dirs = append(dirs, dir)
	}
	path := strings.Join(dirs, string(os.PathListSeparator))
	if _, err := lookPathIn("git", path); err != nil {
		t.Fatal("git is not on PATH")
	}
	env := map[string]string{"PATH": path}

	stdout, stderr, code := runFlIn(t, ws, env, "init", "--yes")
	if code != 0 || jsonField(stdout, "status") != "INIT_APPLIED" {
		t.Fatalf("fl init --yes: exit %d\n%s%s", code, stdout, stderr)
	}
	if got := jsonField(stdout, "snapshotPrewarm", "status"); got != "PREWARMED" {
		t.Errorf("fl init --yes: snapshotPrewarm.status %q, want PREWARMED\n%s%s", got, stdout, stderr)
	}

	stdout, stderr, code = runFlIn(t, ws, env, "doctor", "--proof-ready")
	if code != 1 || !strings.Contains(stdout, "\n[UNAVAILABLE] node: Node.js is unavailable to FaultLine.") || !strings.Contains(stdout, "\n--proof-ready: NOT READY\n") {
		t.Fatalf("fl doctor --proof-ready: exit %d (want 1 with node UNAVAILABLE and NOT READY)\n%s%s", code, stdout, stderr)
	}

	stdout, stderr, code = runFlIn(t, ws, env, "runtime", "prepare", "node", "--yes")
	if code != 0 || jsonField(stdout, "status") != "PREPARED_LOCAL_DIGEST" {
		t.Fatalf("fl runtime prepare node --yes: exit %d\n%s%s", code, stdout, stderr)
	}
	image := jsonField(stdout, "image")
	if !strings.HasPrefix(image, "node@sha256:") {
		t.Fatalf("fl runtime prepare node --yes: image %q", image)
	}
	config, err := os.ReadFile(filepath.Join(ws, ".faultline", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(string(config), "image"); got != image {
		t.Errorf(".faultline/config.json image %q, want %q\n%s", got, image, config)
	}
	if got := jsonField(string(config), "runtime", "image"); got != image || jsonField(string(config), "runtime", "alias") != "node" {
		t.Errorf(".faultline/config.json runtime is not node at %s\n%s", image, config)
	}
}
