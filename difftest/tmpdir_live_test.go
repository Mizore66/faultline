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
	// setid: real gid nogroup, effective gid root: getgid() != getegid(),
	// while the real uid stays root, so git (whose access checks use the
	// real ids) can still use the temporary repository.
	// setcap: copies of node and fl with CAP_NET_BIND_SERVICE, run as
	// nobody. AT_SECURE is set, but SafeGetenv reads the environment (the
	// only capability is that one); glibc's loader has deleted TMPDIR before
	// Node starts, so os.tmpdir() falls back to /tmp (round 6).
	const driver = `import os, sys
if sys.argv[1] == "setid": os.setresgid(65534, 0, 0)
if sys.argv[1] == "setcap": os.setgroups([]); os.setresgid(65534, 65534, 65534); os.setresuid(65534, 65534, 65534)
os.execv(sys.argv[2], sys.argv[2:])`
	bundle := filepath.Join(repo, "difftest", "testdata", "bases", "git-two-states")
	setcap := func(t *testing.T, bin string) string {
		dir, err := os.MkdirTemp("/tmp", "fl-setcap-") // reachable by nobody
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		os.Chmod(dir, 0o755)
		path := filepath.Join(dir, filepath.Base(bin))
		copyFile(t, bin, path)
		os.Chmod(path, 0o755)
		if out, err := exec.Command("setcap", "cap_net_bind_service+ep", path).CombinedOutput(); err != nil {
			t.Fatalf("setcap: %v: %s", err, out)
		}
		return path
	}
	for _, mode := range []string{"setid", "setcap", "plain"} {
		t.Run(mode, func(t *testing.T) {
			node, fl := node, flBinary
			env := []string{"TMPDIR=/nonexistent-tmpdir", "TMP=/nonexistent-tmp", "TEMP=/nonexistent-temp"}
			if mode == "setcap" {
				node, fl = setcap(t, node), setcap(t, flBinary)
				env = []string{"TMPDIR=/nonexistent-tmpdir", "TMP=", "TEMP=", "HOME=/nonexistent-home"}
			}
			run := func(argv ...string) (string, int) {
				cmd := exec.Command("python3", append([]string{"-c", driver, mode}, append(argv, "verify", bundle)...)...)
				cmd.Env = append(os.Environ(), env...)
				out, _ := cmd.CombinedOutput()
				return strings.ReplaceAll(string(out), bundle, "<BUNDLE>"), cmd.ProcessState.ExitCode()
			}
			tsOut, tsCode := run(node, filepath.Join(repo, "dist", "cli.js"))
			goOut, goCode := run(fl)
			if tsOut != goOut || tsCode != goCode {
				t.Fatalf("TS exit %d:\n%.600s\nGo exit %d:\n%.600s", tsCode, tsOut, goCode, goOut)
			}
			// /tmp is used only if TMPDIR was deleted: the git verifier
			// fails on /nonexistent-tmpdir otherwise.
			if mode == "setcap" && !strings.HasPrefix(tsOut, "Git proof self-consistency: VALID\n") {
				t.Fatalf("setcap run did not verify (exit %d):\n%.600s", tsCode, tsOut)
			}
			t.Logf("both exit %d: %.100q", tsCode, tsOut)
		})
	}
}
