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

// inheritDriver starts verify with descriptors 3 to 15 and 18 open and stdin
// a pipe (non-blocking with "nb", which makes Go register it with netpoll),
// and a fake git that records which of 0 to 40 it inherited.
const inheritDriver = `import os, subprocess, sys
mode, gitdir, bundle, argv = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
fds = []
while not fds or fds[-1] < 15:
    fds.append(os.open("/dev/null", os.O_RDONLY))
os.dup2(fds[0], 18); fds = [f for f in fds if f <= 15] + [18]
r, w = os.pipe()
if mode == "nb": os.set_blocking(r, False)
log = os.path.join(gitdir, "fds")
if os.path.exists(log): os.remove(log)
env = dict(os.environ, PATH=gitdir + ":" + os.environ["PATH"], FAKE_GIT_LOG=log)
p = subprocess.run(argv + ["verify", bundle], stdin=r, capture_output=True, env=env, pass_fds=fds)
print(p.returncode, open(log).read().splitlines()[0] if os.path.exists(log) else "no git")
`

// TestLiveInheritedFdsMatchNode: libuv's uv_disable_stdio_inheritance marks
// 0 to 15 close-on-exec and goes on until the first descriptor that is not
// open; descriptors the Go runtime holds by then must not extend that scan
// (round 5, row 13).
func TestLiveInheritedFdsMatchNode(t *testing.T) {
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
	fake := "#!/bin/sh\nif [ ! -f \"$FAKE_GIT_LOG\" ]; then fds=; for fd in $(seq 0 40); do [ -e /dev/fd/$fd ] && fds=\"$fds$fd,\"; done; echo \"$fds\" > \"$FAKE_GIT_LOG\"; fi\nexec '" + git + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(gitdir, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(repo, "difftest", "testdata", "bases", "git-two-states")
	for _, mode := range []string{"block", "nb"} {
		t.Run(mode, func(t *testing.T) {
			run := func(argv ...string) string {
				out, err := exec.Command("python3", append([]string{"-c", inheritDriver, mode, gitdir, bundle}, argv...)...).Output()
				if err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				return strings.TrimSpace(string(out))
			}
			ts := run(node, filepath.Join(repo, "dist", "cli.js"))
			goOut := run(flBinary)
			if ts != goOut {
				t.Fatalf("TS %s\nGo %s", ts, goOut)
			}
			t.Logf("git inherits %s in both", ts)
		})
	}
}
