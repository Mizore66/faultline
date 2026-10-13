//go:build linux || darwin

package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// TestLiveNofileMatchesNode compares the RLIMIT_NOFILE git inherits. Node's
// PlatformInit raises the soft limit to the hard one, or bisects it below
// 2^20 when the hard limit is RLIM_INFINITY (2^63 - 1 on darwin, all ones
// on Linux). It reads no /proc, so it also runs on macOS, where the hard
// limit is infinite by default (round 5, row 9). Go's runtime has replaced
// the inherited soft limit by the time fl runs, so with an infinite hard
// limit fl asks a shell for it before the first git (boot.BeforeSpawn).
func TestLiveNofileMatchesNode(t *testing.T) {
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, limits string }{
		{"soft-256", "ulimit -Sn 256"},
		{"hard-4096", "ulimit -Sn 256 && ulimit -Hn 4096"},
		{"soft-equals-hard", "ulimit -Sn \"$(ulimit -Hn)\""},
		// At or above 2^20 Node's bisection starts above its ceiling and
		// wraps (round 6); with a finite hard limit the shell refuses these
		// and the case repeats the default limits.
		{"soft-2^20", "{ ulimit -Sn 1048576 2>/dev/null || true; }"},
		{"soft-2^20+1", "{ ulimit -Sn 1048577 2>/dev/null || true; }"},
		{"soft-2000000", "{ ulimit -Sn 2000000 2>/dev/null || true; }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(argv ...string) string {
				dir := t.TempDir()
				bin := filepath.Join(dir, "bin")
				log := filepath.Join(dir, "log")
				os.Mkdir(bin, 0o755)
				script := "#!/bin/sh\necho \"$(ulimit -Sn) $(ulimit -Hn)\" >> '" + log + "'\nexec '" + git + "' \"$@\"\n"
				if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("/bin/sh", append([]string{"-c", tc.limits + ` && exec "$@"`, "sh"},
					append(argv, "verify", filepath.Join("testdata", "bases", "git-two-states"))...)...)
				cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				out, _ := cmd.CombinedOutput()
				b, _ := os.ReadFile(log)
				if len(b) == 0 {
					t.Fatalf("%s ran no git:\n%s", argv[0], out)
				}
				lines := strings.Split(strings.TrimSpace(string(b)), "\n")
				return lines[0] + " (" + strings.Fields(string(out))[0] + ")"
			}
			ts := run(node, filepath.Join(repo, "dist", "cli.js"))
			gol := run(flBinary)
			if ts != gol {
				t.Fatalf("git's soft/hard NOFILE: TS %s, Go %s", ts, gol)
			}
			t.Logf("git sees %s in both", ts)
		})
	}
}
