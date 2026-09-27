package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// signalDriver starts `verify` with a slow fake git on PATH (which records
// the signal state it inherited), optionally blocks or ignores signals
// first, sends a signal 0.3 s in, and prints the wait status and git's
// SigBlk/SigIgn lines.
const signalDriver = `import os, signal, subprocess, sys, time
sig, mode, bundle, gitdir, argv = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5:]
def pre():
    if mode == "block": signal.pthread_sigmask(signal.SIG_BLOCK, [s for s in (sig, signal.SIGALRM, signal.SIGUSR2) if s])
    if mode == "ignore":
        for s in (signal.SIGTSTP, signal.SIGTTIN, signal.SIGTTOU, signal.SIGHUP, signal.SIGINT): signal.signal(s, signal.SIG_IGN)
status = os.path.join(gitdir, "status")
if os.path.exists(status): os.remove(status)
env = dict(os.environ, PATH=gitdir + ":/usr/bin:/bin")
p = subprocess.Popen(argv + ["verify", bundle], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env, preexec_fn=pre)
time.sleep(0.3)
if sig: p.send_signal(sig)
p.wait()
lines = open(status).read().split("\n")[:2] if os.path.exists(status) else []
print(p.returncode, " ".join(lines))
`

// Node resets every signal to its default action and clears the signal
// mask at startup, and libuv resets both in git. Compare how verify dies of
// each signal whose default action ends the process (also when the parent
// blocked it), and the dispositions and mask git inherits.
func TestLiveSignalsMatchNode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/self/status")
	}
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	gitdir := t.TempDir()
	// The first git call records its signal state and sleeps; the rest run
	// at once.
	fake := "#!/bin/sh\nif [ ! -f " + gitdir + "/status ]; then grep -E '^Sig(Blk|Ign)' /proc/self/status > " + gitdir +
		"/status; sleep 1; fi\nexec /usr/bin/git \"$@\"\n"
	if err := os.WriteFile(filepath.Join(gitdir, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(repo, "difftest", "testdata", "bases", "git-two-states")
	type tc struct{ sig, mode string }
	var cases []tc
	for _, sig := range strings.Fields("1 2 3 4 5 6 7 8 11 12 14 15 16 24 26 29 30 31 35 64") {
		cases = append(cases, tc{sig, "plain"})
	}
	for _, sig := range strings.Fields("1 3 12 14 15 26") {
		cases = append(cases, tc{sig, "block"})
	}
	cases = append(cases, tc{"0", "ignore"}, tc{"0", "block"})
	for _, c := range cases {
		t.Run(c.mode+"-"+c.sig, func(t *testing.T) {
			run := func(argv ...string) string {
				out, err := exec.Command("python3", append([]string{"-c", signalDriver, c.sig, c.mode, bundle, gitdir}, argv...)...).Output()
				if err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				return strings.TrimSpace(string(out))
			}
			ts := run(node, filepath.Join(repo, "dist", "cli.js"))
			goOut := run(flBinary)
			if ts != goOut {
				t.Errorf("TS %q\nGo %q", ts, goOut)
			}
		})
	}
}
