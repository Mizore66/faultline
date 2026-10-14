//go:build faultline_test && unix

package gitproof_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/sandbox"
)

func TestWrittenBundleVerifies(t *testing.T) {
	clock.ResetForTest("writer")
	req, frozen := gitinv.TwoStateRequestForTest(t, "pass-fail")
	result, err := gitinv.InvestigateGitRange(req, sandbox.NativeRunner(), false)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := gitproof.WriteGitInvestigationProofBundle(filepath.Join(root, "out"), result, frozen, gitproof.WriteOptions{ProofRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if v := gitproof.Verify(w.Directory, w.RootDigest, true); !v.Valid {
		t.Fatalf("%v", v.Errors)
	}
	info, _ := os.Stat(filepath.Join(w.Directory, "manifest.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode %v", info.Mode())
	}
	if w.Manifest.Get("codex").Kind() != jsjson.Undefined || w.Manifest.Get("lifecycle", "status").Str() != "UNBOUND" {
		t.Fatalf("manifest %s", jsjson.Stringify(w.Manifest))
	}
	verify, _ := os.ReadFile(filepath.Join(w.Directory, "VERIFY.md"))
	if strings.HasSuffix(string(verify), "\n") {
		t.Fatal("VERIFY.md must not end with a newline")
	}
	rootFile, _ := os.ReadFile(filepath.Join(w.Directory, "ROOT.sha256"))
	if string(rootFile) != w.RootDigest+"\n" {
		t.Fatalf("ROOT.sha256 %q", rootFile)
	}
	_, err = gitproof.WriteGitInvestigationProofBundle(filepath.Join(root, "out"), result, frozen, gitproof.WriteOptions{ProofRoot: root})
	if err == nil || err.Error() != "Git proof bundle output already exists and will not be replaced: "+filepath.Join(root, "out") {
		t.Fatalf("second write into an existing output must fail: %v", err)
	}

	// Codex identifiers appear only when present; an empty object adds no key.
	id := "thread-1"
	w2, err := gitproof.WriteGitInvestigationProofBundle(filepath.Join(root, "codex"), result, frozen,
		gitproof.WriteOptions{ProofRoot: root, Codex: &gitproof.CodexThreadIDs{RepairThreadID: &id}})
	if err != nil || jsjson.Stringify(w2.Manifest.Get("codex")) != `{"repairThreadId":"thread-1"}` {
		t.Fatalf("%v %s", err, jsjson.Stringify(w2.Manifest))
	}
	w3, err := gitproof.WriteGitInvestigationProofBundle(filepath.Join(root, "empty-codex"), result, frozen,
		gitproof.WriteOptions{ProofRoot: root, Codex: &gitproof.CodexThreadIDs{}})
	if err != nil || w3.Manifest.Get("codex").Kind() != jsjson.Undefined {
		t.Fatalf("%v %s", err, jsjson.Stringify(w3.Manifest))
	}

	// Refusals leave no staging directory behind.
	if _, err := gitproof.WriteGitInvestigationProofBundle(filepath.Join(t.TempDir(), "elsewhere"), result, frozen, gitproof.WriteOptions{ProofRoot: root}); err == nil ||
		!strings.HasPrefix(err.Error(), "Git proof bundle output must be a child directory of ") {
		t.Fatalf("outside root: %v", err)
	}
	bad, _ := jsjson.Parse(jsjson.Stringify(result))
	bad.Obj().Set("status", jsjson.MakeString("RANGE_ERROR"))
	if _, err := gitproof.WriteGitInvestigationProofBundle(filepath.Join(root, "bad"), bad, frozen, gitproof.WriteOptions{ProofRoot: root}); err == nil ||
		!strings.HasPrefix(err.Error(), "Cannot write semantically inconsistent Git proof bundle: Git proof bundle only accepts a COMPLETED investigation") {
		t.Fatalf("semantic: %v", err)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".staging-") {
			t.Fatalf("stage left behind: %s", e.Name())
		}
	}
}
