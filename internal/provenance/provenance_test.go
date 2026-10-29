package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestIdentityRequiresGitHubActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	if _, err := IdentityFromEnvironment(t.TempDir()); err == nil ||
		err.Error() != "GitHub provenance receipts can only be created inside GitHub Actions; local metadata is not a signed CI receipt." {
		t.Fatalf("%v", err)
	}
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SHA", "  ")
	if _, err := IdentityFromEnvironment(t.TempDir()); err == nil || err.Error() != "GitHub Actions provenance requires GITHUB_SHA." {
		t.Fatalf("%v", err)
	}
	t.Setenv("GITHUB_SHA", strings.Repeat("a", 40))
	if _, err := IdentityFromEnvironment(t.TempDir()); err == nil ||
		!strings.HasPrefix(err.Error(), "GitHub provenance could not resolve the checked-out commit tree: Command failed: git rev-parse "+strings.Repeat("a", 40)+"^{tree}\n") {
		t.Fatalf("%v", err)
	}
}

func TestVerifySignedUnreadableReceipt(t *testing.T) {
	r := VerifySigned(t.TempDir(), "/nonexistent/receipt.json", "/nonexistent/b.json", jsjson.Value{})
	if r.Get("valid").Bool() || r.Get("assurance").Str() != "NOT_VERIFIED" ||
		r.Get("artifactAttestation", "errors").Items()[0].Str() != "artifact attestation was not attempted" {
		t.Fatalf("%s", r.Get("errors").Items()[0].Str())
	}
	if got := strings.Join(r.Obj().Keys(), ","); got != "valid,assurance,errors,binding,artifactAttestation,limitation" {
		t.Fatalf("keys %s", got)
	}
	if got := strings.Join(r.Get("binding").Obj().Keys(), ","); got != "valid,errors,receiptDigest,proofRootDigest" {
		t.Fatalf("binding keys %s", got)
	}
}

func TestWriteReceiptRefusesOutsideRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".faultline", "provenance")
	for _, output := range []string{root, filepath.Join(root, "..", "x.json")} {
		if _, err := WriteReceipt(output, jsjson.Value{}, root); err == nil || err.Error() != "GitHub provenance output must be a file beneath "+root {
			t.Fatalf("%s: %v", output, err)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the refused write created the root: %v", err)
	}
}

func TestGhCommandIsBuiltInOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"receipt.json", "bundle.json", "root.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	trust := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(TrustSchemaVersion),
		"repository", jsjson.MakeString("o/r"),
		"signerWorkflow", jsjson.MakeString("o/r/.github/workflows/w.yml"),
		"sourceRef", jsjson.MakeString("refs/heads/main"),
		"eventName", jsjson.MakeString("push"),
		"denySelfHostedRunners", jsjson.MakeBool(true),
		"trustedRootFile", jsjson.MakeString(" root.json "),
	)
	commit := strings.Repeat("b", 40)
	r := verifyArtifactAttestation(filepath.Join(dir, "receipt.json"), filepath.Join(dir, "bundle.json"), trust, commit, filepath.Join(dir, "missing-gh"))
	var command []string
	for _, v := range r.Get("command").Items() {
		command = append(command, v.Str())
	}
	want := []string{filepath.Join(dir, "missing-gh"), "attestation", "verify", filepath.Join(dir, "receipt.json"),
		"--repo", "o/r", "--bundle", filepath.Join(dir, "bundle.json"), "--custom-trusted-root", filepath.Join(dir, "root.json"),
		"--signer-workflow", "o/r/.github/workflows/w.yml", "--source-ref", "refs/heads/main", "--source-digest", commit, "--deny-self-hosted-runners"}
	if strings.Join(command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command %q", command)
	}
	if e := r.Get("errors").Items()[0].Str(); e != "Could not run "+filepath.Join(dir, "missing-gh")+": spawnSync "+filepath.Join(dir, "missing-gh")+" ENOENT" {
		t.Fatalf("%s", e)
	}
}
