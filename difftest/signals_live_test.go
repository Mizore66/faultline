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
// the signal state and descriptor limit it inherited), optionally blocks or
// ignores signals or lowers the soft RLIMIT_NOFILE first, sends a signal
// while git runs (or at once, left pending under the blocked mask), or
// raises a blocked signal in the child before exec with a given
// disposition (raise-dfl, raise-ign), and prints
// the wait status, the size and hash of stdout and stderr, and git's
// SigBlk/SigIgn and open-files lines.
const signalDriver = `import hashlib, os, resource, signal, subprocess, sys, time
sig, mode, bundle, gitdir, argv = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5:]
def pre():
    if mode in ("block", "pending"): signal.pthread_sigmask(signal.SIG_BLOCK, [s for s in (sig, signal.SIGALRM, signal.SIGUSR2) if s])
    if mode == "ignore":
        for s in (signal.SIGTSTP, signal.SIGTTIN, signal.SIGTTOU, signal.SIGHUP, signal.SIGINT): signal.signal(s, signal.SIG_IGN)
    if mode in ("raise-dfl", "raise-ign"):
        os.setpgid(0, 0)  # a group of its own whose parent is outside it: not orphaned, so stop signals stop it
        signal.signal(sig, signal.SIG_IGN if mode == "raise-ign" else signal.SIG_DFL)
        signal.pthread_sigmask(signal.SIG_BLOCK, [sig]); os.kill(os.getpid(), sig)
    if mode == "nofile":
        hard = resource.getrlimit(resource.RLIMIT_NOFILE)[1]
        resource.setrlimit(resource.RLIMIT_NOFILE, (64, hard))
status = os.path.join(gitdir, "status")
if os.path.exists(status): os.remove(status)
env = dict(os.environ, PATH=gitdir + ":/usr/bin:/bin")
if mode == "pending": signal.pthread_sigmask(signal.SIG_BLOCK, [sig])
p = subprocess.Popen(argv + ["verify", bundle], stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, preexec_fn=pre)
if mode == "pending":
    signal.pthread_sigmask(signal.SIG_UNBLOCK, [sig])
    p.send_signal(sig)
else:
    deadline = time.time() + 20
    while not os.path.exists(status) and p.poll() is None and time.time() < deadline: time.sleep(0.01)
    time.sleep(0.1)
    if sig and not mode.startswith("raise"): p.send_signal(sig)
try: out, err = p.communicate(timeout=30)
except subprocess.TimeoutExpired: p.kill(); out, err = p.communicate()
lines = open(status).read().split("\n")[:3] if os.path.exists(status) else []
print(p.returncode, len(out), hashlib.sha256(out).hexdigest()[:16], len(err), hashlib.sha256(err).hexdigest()[:16], " ".join(lines))
`

// Node resets every signal to its default action and clears the signal
// mask at startup, and libuv resets both in git. Compare how verify dies of
// each signal whose default action ends the process (also when the parent
// blocked it, or left it pending), what verify prints (a blocked mask must
// not cost any output), and the dispositions, mask and descriptor limit
// git inherits.
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
	fake := "#!/bin/sh\nif [ ! -f " + gitdir + "/status ]; then { grep -E '^Sig(Blk|Ign)' /proc/self/status; grep 'open files' /proc/self/limits; } > " + gitdir +
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
	for _, sig := range strings.Fields("12 14 26") {
		cases = append(cases, tc{sig, "pending"})
	}
	// Pending from before exec, under the disposition fl inherits (round 5,
	// row 8): Node meets it with that disposition, before resetting any.
	for _, sig := range strings.Fields("12 13 14 25") {
		cases = append(cases, tc{sig, "raise-dfl"})
	}
	for _, sig := range strings.Fields("20 21 22") {
		cases = append(cases, tc{sig, "raise-ign"})
	}
	cases = append(cases, tc{"0", "ignore"}, tc{"0", "block"}, tc{"0", "nofile"})
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
