//go:build faultline_test

package difftest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Review I3, Phase R: the audit must bind the evidence to the INTENDED native
// target of its matrix leg, not merely to an internally consistent recorded one.
// syntheticEvidence is independently Linux/AMD64. Each case rewrites
// identity.json's runner, process, go env and launched-fl build target together
// (cgo, versions, head, hash, records and inventory stay as they were), so the
// recorded identity is coherent for the OTHER target; the audit, told that the
// intended leg is Linux/AMD64, must reject it. The intended pair is supplied
// as explicit test context (FAULTLINE_EVIDENCE_EXPECT_GOOS/GOARCH through
// t.Setenv); a future audit may take it from its own expectation instead. This
// is a validator regression only, never native identity evidence.

type evidenceTarget struct{ runnerOS, runnerArch, goos, goarch string }

// retarget rewrites the recorded identity to t coherently.
func retargetEvidenceIdentity(t *testing.T, dir string, target evidenceTarget) {
	t.Helper()
	path := filepath.Join(dir, "identity.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var id serveBundleIdentity
	if err := json.Unmarshal(data, &id); err != nil {
		t.Fatal(err)
	}
	id.RunnerOS, id.RunnerArch = target.runnerOS, target.runnerArch
	id.GOOS, id.GOARCH = target.goos, target.goarch
	id.GoEnvGOOS, id.GoEnvGOARCH = target.goos, target.goarch
	id.FlBuildOS, id.FlBuildArch = target.goos, target.goarch
	out, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	// The rewritten identity is coherent on its own: the existing identity check
	// accepts it, so a rejection below can only come from the intended target.
	if err := checkServeBundleIdentity(id, ""); err != nil {
		t.Fatalf("the rewritten identity is not coherent (%v); the regression would test another invariant", err)
	}
}

func TestServeBundleEvidenceIntendedTarget(t *testing.T) {
	t.Setenv("FAULTLINE_EVIDENCE_EXPECT_GOOS", "linux")
	t.Setenv("FAULTLINE_EVIDENCE_EXPECT_GOARCH", "amd64")
	cases := []struct {
		name   string
		target *evidenceTarget // nil: the unmodified Linux/AMD64 synthetic evidence
		reject bool
	}{
		{name: "valid control (Linux/AMD64 evidence for the Linux/AMD64 leg)", target: nil, reject: false},
		{name: "same target rewritten coherently is still accepted", target: &evidenceTarget{"Linux", "X64", "linux", "amd64"}, reject: false},
		{name: "coherent Linux/ARM64 evidence for the Linux/AMD64 leg", target: &evidenceTarget{"Linux", "ARM64", "linux", "arm64"}, reject: true},
		{name: "coherent Darwin/ARM64 evidence for the Linux/AMD64 leg", target: &evidenceTarget{"macOS", "ARM64", "darwin", "arm64"}, reject: true},
		{name: "coherent Darwin/AMD64 evidence for the Linux/AMD64 leg", target: &evidenceTarget{"macOS", "X64", "darwin", "amd64"}, reject: true},
		{name: "coherent Windows/AMD64 evidence for the Linux/AMD64 leg", target: &evidenceTarget{"Windows", "X64", "windows", "amd64"}, reject: true},
		{name: "coherent Windows/ARM64 evidence for the Linux/AMD64 leg", target: &evidenceTarget{"Windows", "ARM64", "windows", "arm64"}, reject: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, want := syntheticEvidence(t)
			if c.target != nil {
				retargetEvidenceIdentity(t, dir, *c.target)
			}
			err := auditServeBundleEvidence(dir, want)
			switch {
			case c.reject && err == nil:
				t.Fatal("audit accepted evidence recorded on a different target than the intended leg")
			case !c.reject && err != nil:
				t.Fatalf("audit rejected the valid evidence: %v", err)
			}
		})
	}
}
