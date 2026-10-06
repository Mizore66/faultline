//go:build linux || darwin

package difftest

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// stderrDriver runs the command with fd 2 set to a socket of the given kind
// and prints the exit code, the stdout size and the bytes the socket's peer
// received.
const stderrDriver = `import os, socket, subprocess, sys
kind, argv = sys.argv[1], sys.argv[2:]
if kind == "udp":
    peer = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); peer.bind(("127.0.0.1", 0))
    end = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); end.connect(peer.getsockname())
elif kind == "tcp":
    srv = socket.socket(); srv.bind(("127.0.0.1", 0)); srv.listen(1)
    end = socket.create_connection(srv.getsockname()); peer, _ = srv.accept()
else:
    t = {"unixdgram": socket.SOCK_DGRAM, "seqpacket": socket.SOCK_SEQPACKET, "unixstream": socket.SOCK_STREAM}[kind]
    peer, end = socket.socketpair(socket.AF_UNIX, t)
p = subprocess.run(argv, stdout=subprocess.PIPE, stderr=end.fileno())
end.close(); peer.setblocking(False); got = b""
while True:
    try:
        chunk = peer.recv(65536)
    except (BlockingIOError, ConnectionResetError):
        break
    if not chunk: break
    got += chunk
print(p.returncode, len(p.stdout), repr(got))
`

// TestLiveStderrMatchesNode: Node writes the CLI's errors through
// process.stderr, the dummy Writable for UDP and unclassified sockets, so
// nothing reaches them (round 5, row 12).
func TestLiveStderrMatchesNode(t *testing.T) {
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"udp", "unixdgram", "unixstream", "tcp"}
	if runtime.GOOS == "linux" {
		kinds = append(kinds, "seqpacket")
	}
	for _, kind := range kinds {
		for _, args := range [][]string{{"frob"}, {"verify"}} {
			t.Run(kind+"-"+strings.Join(args, "-"), func(t *testing.T) {
				run := func(argv ...string) string {
					out, err := exec.Command("python3", append([]string{"-c", stderrDriver, kind}, append(argv, args...)...)...).Output()
					if err != nil {
						t.Fatalf("%v: %s", err, out)
					}
					return strings.TrimSpace(string(out))
				}
				ts := run(node, filepath.Join(repo, "dist", "cli.js"))
				goOut := run(flBinary)
				if ts != goOut {
					t.Errorf("TS %s\nGo %s", ts, goOut)
				}
			})
		}
	}
}
