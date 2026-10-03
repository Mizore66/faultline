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

// groupSignalDriver runs verify in a session of its own with a fake git that
// sends sig to its whole process group, as Ctrl-C or a terminal hang-up
// does: git and verify get it at the same instant, and git dies of it. With
// a second signal, git instead sends sig to verify alone and the second one
// 5 ms later. It prints the wait status and stdout/stderr sizes of each run.
const groupSignalDriver = `import os, signal, subprocess, sys
sig, second, runs, gitdir, bundle, argv = int(sys.argv[1]), sys.argv[2], int(sys.argv[3]), sys.argv[4], sys.argv[5], sys.argv[6:]
def pre(): signal.signal(sig, signal.SIG_DFL)
env = dict(os.environ, PATH=gitdir + ":" + os.environ["PATH"], FAKE_GIT_SIGNAL=str(sig), FAKE_GIT_SECOND=second)
for _ in range(runs):
    p = subprocess.Popen(argv + ["verify", bundle], stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env,
                         start_new_session=True, preexec_fn=pre)
    out, err = p.communicate(timeout=60)
    print(p.returncode, len(out), len(err))
`

// TestLiveGroupSignalMatchesNode: Node dies of a group signal the moment it
// arrives and prints nothing; fl used to print the verifier's report on the
// child's death and exit 1 before its own copy of the signal ended it
// (round 5, row 5: 1 in 20 to 4 in 25 runs on macOS).
func TestLiveGroupSignalMatchesNode(t *testing.T) {
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
	gitdir := t.TempDir()
	fake := "#!/bin/sh\nif [ -n \"$FAKE_GIT_SECOND\" ]; then kill -$FAKE_GIT_SIGNAL $PPID; sleep 0.005; kill -$FAKE_GIT_SECOND $PPID\n" +
		"else kill -$FAKE_GIT_SIGNAL 0; fi\nsleep 5\nexec '" + git + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(gitdir, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(repo, "difftest", "testdata", "bases", "git-two-states")
	for _, c := range []struct{ name, sig, second string }{
		{"group-INT", "2", ""}, {"group-HUP", "1", ""}, {"group-TERM", "15", ""},
		// Node dies of the first signal; the second must not replace it
		// while fl is dying (round 5, row 5).
		{"TERM-then-INT", "15", "2"},
	} {
		sig := c.sig
		t.Run(c.name, func(t *testing.T) {
			run := func(runs string, argv ...string) []string {
				out, err := exec.Command("python3", append([]string{"-c", groupSignalDriver, sig, c.second, runs, gitdir, bundle}, argv...)...).Output()
				if err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				return strings.Split(strings.TrimSpace(string(out)), "\n")
			}
			want := run("1", node, filepath.Join(repo, "dist", "cli.js"))[0]
			if want != "-"+sig+" 0 0" {
				t.Fatalf("TS: %q, want death by signal %s with no output", want, sig)
			}
			for i, got := range run("15", flBinary) {
				if got != want {
					t.Errorf("Go run %d: %q, TS %q", i, got, want)
				}
			}
		})
	}
}
