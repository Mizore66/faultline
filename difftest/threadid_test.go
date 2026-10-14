//go:build unix

package difftest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThreadIDsBindUnderRoot(t *testing.T) {
	ws := scenarioWorkspace(t, "git-two-states")
	stdout, stderr, code := runFlIn(t, ws, nil, "investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD",
		"--proposal", "demo-1", "--image", pinnedImage, "--witness-draft-thread-id", "thr_01.draft", "--repair-thread-id", "thr:02")
	if code != 0 {
		t.Fatalf("%d %s %s", code, stdout, stderr)
	}
	dir := proofDirectory(t, stdout)
	manifest, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if !strings.Contains(string(manifest), `"codex":{"repairThreadId":"thr:02","witnessDraftThreadId":"thr_01.draft"}`) {
		t.Fatalf("manifest %s", manifest)
	}
	// TS checks the same bundle when the oracle is on; Go's run regardless.
	verifiers := []func(string) (string, int){goVerify(t)}
	if os.Getenv("FAULTLINE_NODE_ORACLE") == "1" {
		verifiers = append(verifiers, tsVerify(t))
	}
	for _, verifier := range verifiers {
		if out, code := verifier(dir); code != 0 || !strings.Contains(out, "Integrity: VALID") {
			t.Fatalf("fresh bundle: %d %s", code, out)
		}
	}
	tamperManifestField(t, dir, "codex.repairThreadId", "thr:03")
	for _, verifier := range verifiers {
		if _, code := verifier(dir); code == 0 {
			t.Fatal("edited thread id still verifies")
		}
	}
	_, stderr, code = runFlIn(t, ws, nil, "investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD",
		"--proposal", "demo-1", "--image", pinnedImage, "--repair-thread-id", "-bad")
	if code != 1 || !strings.Contains(stderr, "--repair-thread-id must be") {
		t.Fatalf("%d %s", code, stderr)
	}
}

// TestInvestigateRefusalLeavesNoStage: the writer fails after creating its
// stage (a SHA-256 repository: the verifier's `git init --bare` is SHA-1,
// so TS and Go both refuse to publish); neither the stage nor the
// temporary source clone survives.
func TestInvestigateRefusalLeavesNoStage(t *testing.T) {
	ws := scenarioWorkspace(t, "git-sha256")
	_, stderr, code := runFlIn(t, ws, nil, "investigate", "git", "--repo", "repo", "--from", "HEAD~2", "--to", "HEAD",
		"--proposal", "demo-1", "--image", pinnedImage)
	if code != 1 || !strings.Contains(stderr, "Refusing to publish an invalid Git proof bundle") {
		t.Fatalf("%d %s", code, stderr)
	}
	proofRoot := filepath.Join(ws, ".faultline", "git-proof-bundles")
	entries, err := os.ReadDir(proofRoot)
	if err != nil {
		t.Fatal(err) // the proof root itself was created before the stage
	}
	for _, e := range entries {
		t.Errorf("proof root keeps %s", e.Name())
	}
	tmp, err := os.ReadDir(rootsOf(ws).tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range tmp {
		t.Errorf("TMPDIR keeps %s", e.Name())
	}
}
