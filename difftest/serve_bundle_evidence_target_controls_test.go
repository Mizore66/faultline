//go:build faultline_test

package difftest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review I3, Phase G controls for the intended-target binding. They sit beside
// (never inside) the immutable Phase R regression. Every identity here states
// its own target; nothing depends on the workflow environment except the cases
// that set it explicitly with t.Setenv.

func coherentIdentity(runnerOS, runnerArch, goos, goarch string) serveBundleIdentity {
	head := strings.Repeat("a", 40)
	cgo := "1"
	if goos != "linux" {
		cgo = "0"
	}
	return serveBundleIdentity{
		Schema: serveBundleEvidenceSchema, RunnerOS: runnerOS, RunnerArch: runnerArch, GOOS: goos, GOARCH: goarch, GoVersion: "go1.27",
		GoEnvGOOS: goos, GoEnvGOARCH: goarch, GoEnvCGO: cgo, NodeVersion: "v22.22.2", CLIVersion: "FaultLine 0.1.4",
		FlSHA256: strings.Repeat("b", 64), FlBytes: 10, FlBuildOS: goos, FlBuildArch: goarch, FlBuildCGO: cgo,
		Head: head, Event: "push", EventHead: head, GithubSHA: head,
		IntendedGOOS: goos, IntendedGOARCH: goarch,
	}
}

// Catches a binding that misses one of the six required targets: each coherent
// identity is structurally valid and equals exactly its own intended pair, and
// no other pair.
func TestServeBundleTargetBindsAllSixLegs(t *testing.T) {
	legs := []struct{ runnerOS, runnerArch, goos, goarch string }{
		{"Linux", "X64", "linux", "amd64"}, {"Linux", "ARM64", "linux", "arm64"},
		{"macOS", "ARM64", "darwin", "arm64"}, {"macOS", "X64", "darwin", "amd64"},
		{"Windows", "X64", "windows", "amd64"}, {"Windows", "ARM64", "windows", "arm64"},
	}
	for _, leg := range legs {
		id := coherentIdentity(leg.runnerOS, leg.runnerArch, leg.goos, leg.goarch)
		if err := checkServeBundleIdentity(id, ""); err != nil {
			t.Fatalf("%s/%s: the control identity is not structurally valid: %v", leg.goos, leg.goarch, err)
		}
		for _, other := range legs {
			err := checkServeBundleTarget(id, other.goos, other.goarch)
			same := other.goos == leg.goos && other.goarch == leg.goarch
			if same && err != nil {
				t.Errorf("%s/%s: rejected its own intended target: %v", leg.goos, leg.goarch, err)
			}
			if !same && err == nil {
				t.Errorf("%s/%s evidence accepted for the intended leg %s/%s", leg.goos, leg.goarch, other.goos, other.goarch)
			}
		}
	}
}

// Catches a binding that trusts recorded fields: tampering the recorded intended
// pair (alone, or together with every actual dimension) must not turn evidence
// of another target into evidence for the independently expected one.
func TestServeBundleTargetRejectsTamperedIntended(t *testing.T) {
	// Actual Linux/ARM64 evidence; the audit expects Linux/AMD64.
	arm := coherentIdentity("Linux", "ARM64", "linux", "arm64")
	t.Run("intended fields claim the expected target but the actual run differs", func(t *testing.T) {
		id := arm
		id.IntendedGOOS, id.IntendedGOARCH = "linux", "amd64"
		if err := checkServeBundleTarget(id, "linux", "amd64"); err == nil {
			t.Fatal("accepted: the recorded intended fields were trusted over the actual dimensions")
		}
	})
	t.Run("actual run is the expected target but the recorded intended fields differ", func(t *testing.T) {
		id := coherentIdentity("Linux", "X64", "linux", "amd64")
		id.IntendedGOOS, id.IntendedGOARCH = "linux", "arm64"
		if err := checkServeBundleTarget(id, "linux", "amd64"); err == nil {
			t.Fatal("accepted: the recorded intended target differs from the expected one")
		}
	})
	t.Run("intended fields absent", func(t *testing.T) {
		id := coherentIdentity("Linux", "X64", "linux", "amd64")
		id.IntendedGOOS, id.IntendedGOARCH = "", ""
		if err := checkServeBundleTarget(id, "linux", "amd64"); err == nil {
			t.Fatal("accepted evidence without an intended target")
		}
	})
	t.Run("expected target unknown", func(t *testing.T) {
		id := coherentIdentity("Linux", "X64", "linux", "amd64")
		for _, pair := range [][2]string{{"plan9", "amd64"}, {"linux", "riscv64"}, {"", ""}, {"linux", ""}} {
			if err := checkServeBundleTarget(id, pair[0], pair[1]); err == nil {
				t.Errorf("accepted the expected target %q/%q", pair[0], pair[1])
			}
		}
	})
	t.Run("audit of the synthetic evidence with tampered recorded intended fields", func(t *testing.T) {
		dir, want := syntheticEvidence(t)
		path := filepath.Join(dir, "identity.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var id serveBundleIdentity
		if err := json.Unmarshal(data, &id); err != nil {
			t.Fatal(err)
		}
		id.IntendedGOARCH = "arm64"
		out, _ := json.Marshal(id)
		if err := os.WriteFile(path, out, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := auditServeBundleEvidence(dir, want); err == nil {
			t.Fatal("audit accepted tampered recorded intended fields")
		}
	})
	t.Run("audit of the synthetic evidence with an unknown expected target", func(t *testing.T) {
		dir, want := syntheticEvidence(t)
		want.TargetGOOS, want.TargetGOARCH = "linux", ""
		if err := auditServeBundleEvidence(dir, want); err == nil {
			t.Fatal("audit accepted a partial expected target")
		}
	})
}

// Catches a live expectation that falls back to the actual platform or accepts a
// partial or unknown pair: absent, partial and unknown contexts all fail, and
// the six valid pairs are read back exactly.
func TestServeBundleTargetFromEnv(t *testing.T) {
	set := func(t *testing.T, goos, goarch string) {
		t.Setenv("FAULTLINE_EVIDENCE_EXPECT_GOOS", goos)
		t.Setenv("FAULTLINE_EVIDENCE_EXPECT_GOARCH", goarch)
	}
	for _, bad := range [][2]string{{"", ""}, {"linux", ""}, {"", "amd64"}, {"plan9", "amd64"}, {"linux", "riscv64"}, {"Linux", "amd64"}, {"linux", "x64"}} {
		t.Run("rejects "+bad[0]+"/"+bad[1], func(t *testing.T) {
			set(t, bad[0], bad[1])
			if goos, goarch, err := evidenceTargetFromEnv(); err == nil {
				t.Fatalf("accepted %q/%q as %s/%s", bad[0], bad[1], goos, goarch)
			}
		})
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			t.Run("accepts "+goos+"/"+goarch, func(t *testing.T) {
				set(t, goos, goarch)
				gotOS, gotArch, err := evidenceTargetFromEnv()
				if err != nil || gotOS != goos || gotArch != goarch {
					t.Fatalf("read back %q/%q (%v)", gotOS, gotArch, err)
				}
			})
		}
	}
}
