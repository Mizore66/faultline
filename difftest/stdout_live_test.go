package difftest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// stdoutDriver runs `verify` with fd 1 set up per scenario and prints
// {rc, out, err} as JSON: rc is the wait status (negative for a signal),
// out what reached the other end of stdout.
const stdoutDriver = `import fcntl, json, os, resource, socket, subprocess, sys, threading, time
scenario, bundle, argv = sys.argv[1], sys.argv[2], sys.argv[3:]
argv = argv + ["verify", bundle]
res = {}
def run(stdout, preexec=None, reader=None):
    p = subprocess.Popen(argv, stdout=stdout, stderr=subprocess.PIPE, preexec_fn=preexec)
    if hasattr(stdout, "close"): stdout.close()
    elif isinstance(stdout, int) and stdout > 2: os.close(stdout)
    got = reader() if reader else b""
    err = p.stderr.read(); p.wait()
    res.update(rc=p.returncode, err=err.decode("utf-8", "replace"), out=got.decode("utf-8", "replace"))
tmp = os.environ["TMPDIR"]
if scenario == "devfull":
    run(open("/dev/full", "wb"))
elif scenario == "closedpipe":
    r, w = os.pipe(); os.close(r); run(w)
elif scenario.startswith("fsize"):
    limit = int(scenario[5:]); path = os.path.join(tmp, "out.txt")
    f = open(path, "wb")
    run(f, lambda: resource.setrlimit(resource.RLIMIT_FSIZE, (limit, limit)))
    res["out"] = open(path, "rb").read().decode()
elif scenario == "udp":
    rx = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); rx.bind(("127.0.0.1", 0)); rx.settimeout(0.5)
    tx = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); tx.connect(rx.getsockname())
    def recv():
        data = b""
        try:
            while True: data += rx.recv(65536)
        except OSError: return data
    run(tx.detach(), reader=recv)
elif scenario == "unixdgram":
    a, b = socket.socketpair(socket.AF_UNIX, socket.SOCK_DGRAM); b.settimeout(0.5)
    def recv():
        data = b""
        try:
            while True: data += b.recv(65536)
        except OSError: return data
    run(a.detach(), reader=recv)
elif scenario == "directory":
    run(os.open(tmp, os.O_RDONLY))
elif scenario == "tcpunconnected":
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM); run(s.detach())
elif scenario == "nonblocklater":
    r, w = os.pipe()
    def slow():
        time.sleep(0.05)
        fl = fcntl.fcntl(r, fcntl.F_GETFL); fcntl.fcntl(r, fcntl.F_SETFL, fl | os.O_NONBLOCK)
        data = b""
        fcntl.fcntl(r, fcntl.F_SETFL, fl)
        while True:
            time.sleep(0.001)
            chunk = os.read(r, 4096)
            if not chunk: return data
            data += chunk
    run(w, reader=slow)
elif scenario == "pipereadend":
    r, w = os.pipe(); run(r); os.close(w)
elif scenario == "fifordonly":
    path = os.path.join(tmp, "fifo"); os.mkfifo(path)
    keep = os.open(path, os.O_RDWR)
    fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
    fcntl.fcntl(fd, fcntl.F_SETFL, fcntl.fcntl(fd, fcntl.F_GETFL) & ~os.O_NONBLOCK)
    run(fd); os.close(keep)
elif scenario == "ptyrdonly":
    import pty
    m, sl = pty.openpty(); fd = os.open(os.ttyname(sl), os.O_RDONLY | os.O_NOCTTY); os.close(sl)
    run(fd); os.close(m)
elif scenario == "ptyhangup":
    # The first git call touches a marker and sleeps: the pty hangs up
    # after startup and before the first write.
    import pty
    gitdir = os.path.join(tmp, "bin"); os.mkdir(gitdir); marker = os.path.join(tmp, "marker")
    with open(os.path.join(gitdir, "git"), "w") as f:
        f.write("#!/bin/sh\nif [ ! -f %s ]; then touch %s; sleep 0.5; fi\nexec /usr/bin/git \"$@\"\n" % (marker, marker))
    os.chmod(os.path.join(gitdir, "git"), 0o755)
    os.environ["PATH"] = gitdir + ":" + os.environ["PATH"]
    m, sl = pty.openpty()
    def hangup():
        while not os.path.exists(marker): time.sleep(0.01)
        os.close(m); return b""
    run(sl, reader=hangup)
print(json.dumps(res))
`

// Node builds process.stdout from uv_guess_handle: file and character
// devices get a SyncWriteStream (one write per chunk, short counts ignored),
// pipes and sockets a libuv stream (not writable when the descriptor is
// read-only), UDP and unknown handles a dummy writer, all on first use.
// Compare what reaches stdout, stderr and the exit status.
func TestLiveStdoutMatchesNode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the scenarios use Linux devices and rlimits")
	}
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	demo := filepath.Join(repo, "difftest", "testdata", "bases", "demo-replay")
	// A bundle whose error list is about 1.5 MB: 20,000 undeclared files.
	big := filepath.Join(t.TempDir(), "big")
	if err := exec.Command("cp", "-r", demo, big).Run(); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(big, "extra"), 0o755)
	for i := range 20000 {
		os.WriteFile(filepath.Join(big, "extra", fmt.Sprintf("file-%05d-%s", i, strings.Repeat("x", 40))), nil, 0o644)
	}
	scenarios := map[string]string{
		"devfull": demo, "closedpipe": demo, "fsize0": demo, "fsize10": demo, "fsize32": demo,
		"fsize100": demo, "fsize170": demo, "udp": demo, "unixdgram": demo, "directory": demo,
		"tcpunconnected": demo, "nonblocklater": big, "pipereadend": demo, "fifordonly": demo,
		"ptyrdonly": demo, "ptyhangup": filepath.Join(repo, "difftest", "testdata", "bases", "git-two-states"),
	}
	for name, bundle := range scenarios {
		t.Run(name, func(t *testing.T) {
			run := func(argv ...string) map[string]any {
				tmp := t.TempDir()
				cmd := exec.Command("python3", append([]string{"-c", stdoutDriver, name, bundle}, argv...)...)
				cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "NODE_EXTRA_CA_CERTS=")
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				var res map[string]any
				if err := json.Unmarshal(out, &res); err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				return res
			}
			ts := run(node, filepath.Join(repo, "dist", "cli.js"))
			goRes := run(flBinary)
			// Node prints the unhandled 'error' event with a stack trace;
			// Go prints its error line only (KNOWN_DIFFERENCES.md).
			if e := ts["err"].(string); strings.Contains(e, "Unhandled 'error' event") {
				for _, line := range strings.Split(e, "\n") {
					if strings.HasPrefix(line, "Error: ") {
						ts["err"] = line + "\n"
						break
					}
				}
			}
			t.Logf("TS rc %v, %d bytes out, stderr %.80q", ts["rc"], len(ts["out"].(string)), ts["err"])
			if fmt.Sprint(ts) != fmt.Sprint(goRes) {
				t.Errorf("TS rc %v err %.300q out %d bytes\nGo rc %v err %.300q out %d bytes",
					ts["rc"], ts["err"], len(ts["out"].(string)), goRes["rc"], goRes["err"], len(goRes["out"].(string)))
			}
		})
	}
}
