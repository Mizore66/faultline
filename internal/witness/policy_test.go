//go:build faultline_test

package witness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func proposedStoreWithCommand(t *testing.T, command string) string {
	t.Helper()
	store := t.TempDir()
	in := proposalInput(t)
	witnessIn := in.Get("witness").Obj()
	witnessIn.Set("command", jsjson.MakeString(command))
	if _, err := Propose(store, in); err != nil {
		t.Fatal(err)
	}
	return store
}

func proposedStore(t *testing.T) string { return proposedStoreWithCommand(t, "node witness.mjs") }

func testPolicy(t *testing.T) jsjson.Value {
	t.Helper()
	in, err := jsjson.Parse(`{"schemaVersion":"faultline.standing-approval-policy.v1","policyId":"p1",
		"frozenBy":"ops@example.test","frozenAt":"2026-07-16T11:00:00.000Z","allowedCommands":["node witness.mjs"],
		"allowedOverlayTemplates":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
		"maxTimeoutSeconds":60,"network":"disabled","credentials":"redacted"}`)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := BuildPolicy(in)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestPolicyMatchFreezesAndMismatchParks(t *testing.T) {
	clock.ResetForTest("policy")
	store := proposedStore(t)
	policy := testPolicy(t)
	r, err := ResolveAutonomous(store, "demo-1", &policy)
	if err != nil || r.Get("outcome").Str() != "FROZEN_BY_POLICY" || r.Get("notification").Str() != "Standing policy p1 auto-froze demo-1." {
		t.Fatalf("%v %s", err, jsjson.Stringify(r))
	}
	other := proposedStoreWithCommand(t, "rm -rf /")
	r, err = ResolveAutonomous(other, "demo-1", &policy)
	if err != nil || r.Get("outcome").Str() != "PARKED_AGENT_DRAFT" {
		t.Fatalf("%v %s", err, jsjson.Stringify(r))
	}
	if !strings.HasPrefix(r.Get("notification").Str(), "Parked AGENT_DRAFT demo-1: no standing-policy match.") {
		t.Fatalf("%s", r.Get("notification").Str())
	}
	if keys := r.Get("parked").Obj().Keys(); keys[1] != "status" || keys[2] != "evidenceGrade" || keys[3] != "evidenceLabel" {
		t.Fatalf("annotation keys follow status: %v", keys)
	}
}

func TestBuildPolicySortsTemplatesAndBindsDigest(t *testing.T) {
	in, _ := jsjson.Parse(`{"schemaVersion":"faultline.standing-approval-policy.v1","policyId":"p1","frozenBy":" ops ","frozenAt":"2026-07-16T11:00:00.000Z",
		"allowedCommands":["a"],"allowedOverlayTemplates":[{"path":"b","bytesBase64":""},{"path":"a","bytesBase64":""}],
		"maxTimeoutSeconds":60,"network":"disabled","credentials":"redacted"}`)
	policy, err := BuildPolicy(in)
	if err != nil {
		t.Fatal(err)
	}
	templates := policy.Get("allowedOverlayTemplates").Items()
	if templates[0].Get("path").Str() != "a" || policy.Get("frozenBy").Str() != "ops" {
		t.Fatalf("%s", jsjson.Stringify(policy))
	}
	again, err := BuildPolicy(policy)
	if err != nil || jsjson.Stringify(again) != jsjson.Stringify(policy) {
		t.Fatalf("round trip: %v", err)
	}
}

func TestApplyPolicyRefusesTamperedDigest(t *testing.T) {
	store := proposedStore(t)
	policy := testPolicy(t)
	policy.Obj().Set("maxTimeoutSeconds", jsjson.MakeNumber(61))
	_, err := ApplyPolicy(store, "demo-1", policy, ApplyOptions{})
	if err == nil || err.Error() != "Standing approval policy digest does not match its canonical contents" {
		t.Fatalf("%v", err)
	}
}

func TestApplyPolicyRefusesOnMismatchWithReasons(t *testing.T) {
	store := proposedStoreWithCommand(t, "rm -rf /")
	_, err := ApplyPolicy(store, "demo-1", testPolicy(t), ApplyOptions{})
	if err == nil || err.Error() != "Standing approval refused: proposal command is not an exact allowlist match" {
		t.Fatalf("%v", err)
	}
}

func TestReadPolicyCreatesDirectoryAndWriteIsOnce(t *testing.T) {
	store := t.TempDir()
	if _, err := ReadPolicy(store, "p1"); err == nil || !strings.HasPrefix(err.Error(), "ENOENT: no such file or directory, lstat ") {
		t.Fatalf("%v", err)
	}
	if info, err := os.Stat(filepath.Join(store, "standing-policies")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("a read must create the directory: %v", err)
	}
	policy := testPolicy(t)
	path, err := WritePolicy(store, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WritePolicy(store, policy); err == nil || err.Error() != "EEXIST: file already exists, open '"+path+"'" {
		t.Fatalf("%v", err)
	}
	read, err := ReadPolicy(store, "p1")
	if err != nil || jsjson.Stringify(read) != jsjson.Stringify(policy) {
		t.Fatalf("%v", err)
	}
	if _, err := ReadPolicy(store, "../x"); err == nil || err.Error() != "Standing policy path escaped its store" {
		t.Fatalf("%v", err)
	}
}

func TestRatifyChecksDigestAndRecordsOnce(t *testing.T) {
	clock.ResetForTest("ratify")
	store := proposedStore(t)
	proposal, _ := ReadProposal(store, "demo-1")
	digest := witnessDigestFromProposal(proposal)

	_, err := Ratify(store, "demo-1", RatifyOptions{ExecutedWitnessDigest: "sha256:bad", RatifiedBy: "r"})
	var mismatch *AutonomousSessionError
	if e, ok := err.(*AutonomousSessionError); !ok || e.Code != "DIGEST_MISMATCH" {
		t.Fatalf("%v %v", err, mismatch)
	}
	r, err := Ratify(store, "demo-1", RatifyOptions{ExecutedWitnessDigest: digest, RatifiedBy: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Get("frozen", "approval", "approvedAt").Str() != r.Get("ratification", "ratifiedAt").Str() ||
		r.Get("frozen", "frozenAt").Str() != r.Get("ratification", "ratifiedAt").Str() {
		t.Fatalf("ratify reads the clock once: %s", jsjson.Stringify(r))
	}
	if !strings.Contains(r.Get("frozen", "approval", "note").Str(), "APPROVED_AFTER_EXECUTION: human owned the question after execution; digests identical") {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
	_, err = Ratify(store, "demo-1", RatifyOptions{ExecutedWitnessDigest: digest, RatifiedBy: "r"})
	if e, ok := err.(*AutonomousSessionError); !ok || e.Code != "ALREADY_RATIFIED" || e.Message != "Ratification already exists for demo-1" {
		t.Fatalf("%v", err)
	}
}
