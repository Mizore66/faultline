//go:build linux

package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// os.tmpdir() reads TMPDIR, TMP and TEMP through SafeGetenv, which ignores
// them when the real and effective user ids differ (setuid) or AT_SECURE is
// set (src/node_credentials.cc), so the git verifier's temporary repository
// goes to /tmp. Round 5, row 10. Needs root to give the process a real gid
// other than its effective one.
func TestLiveTmpdirSetidMatchesNode(t *testing.T) {
	startRootOracle(t)
	if os.Geteuid() != 0 {
		if os.Getenv("FAULTLINE_REQUIRE_ROOT") != "" {
			t.Fatal("FAULTLINE_REQUIRE_ROOT is set but the test is not root")
		}
		t.Skip("needs root (setresuid)")
	}
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	// Real gid nogroup, effective gid root: getgid() != getegid(), while the
	// real uid stays root, so git (whose access checks use the real ids)
	// can still use the temporary repository.
	const driver = `import os, sys
if sys.argv[1] == "setid": os.setresgid(65534, 0, 0)
os.execv(sys.argv[2], sys.argv[2:])`
	bundle := filepath.Join(repo, "difftest", "testdata", "bases", "git-two-states")
	for _, mode := range []string{"setid", "plain"} {
		t.Run(mode, func(t *testing.T) {
			run := func(argv ...string) (string, int) {
				cmd := exec.Command("python3", append([]string{"-c", driver, mode}, append(argv, "verify", bundle)...)...)
				cmd.Env = append(os.Environ(), "TMPDIR=/nonexistent-tmpdir", "TMP=/nonexistent-tmp", "TEMP=/nonexistent-temp")
				out, _ := cmd.CombinedOutput()
				return strings.ReplaceAll(string(out), bundle, "<BUNDLE>"), cmd.ProcessState.ExitCode()
			}
			tsOut, tsCode := run(node, filepath.Join(repo, "dist", "cli.js"))
			goOut, goCode := run(flBinary)
			if tsOut != goOut || tsCode != goCode {
				t.Fatalf("TS exit %d:\n%.600s\nGo exit %d:\n%.600s", tsCode, tsOut, goCode, goOut)
			}
			t.Logf("both exit %d: %.100q", tsCode, tsOut)
		})
	}
}
