//go:build linux

package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// TestLiveFaultInjectionMatchesNode fails one syscall on one path with
// strace and compares TS and Go byte for byte. strace -P restricts tracing
// and injection to calls on that path (fd arguments included), and when=1
// fails only the first. Round 5, rows 3 and 4:
//   - libuv retries every synchronous fs call on EINTR except read and
//     close (uv__fs_work), so an interrupted read fails in Node, and an
//     interrupted getdents64 or openat re-runs the whole scandir;
//   - readFileSync(p, "utf8") never calls fstat (ReadFileUtf8), so the first
//     stat-family call on manifest.json is the walker's lstat in both;
//   - existsSync(p) is access(p, F_OK), which a filesystem can fail where
//     stat succeeds.
func TestLiveFaultInjectionMatchesNode(t *testing.T) {
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	strace, err := exec.LookPath("strace")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("strace is required in CI")
		}
		t.Skip("no strace")
	}
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	const stats = "fstat,newfstatat,lstat,stat,statx"
	for _, tc := range []struct {
		name, base, path, calls, errno string
	}{
		{"read-hashes-EINTR", "demo-replay", "hashes.txt", "read,pread64", "EINTR"},
		{"read-manifest-EINTR", "prevention-verified", "manifest.json", "read,pread64", "EINTR"},
		{"getdents-root-EINTR", "demo-replay", "", "getdents64", "EINTR"},
		{"open-runs-EINTR", "git-two-states", "runs", "openat", "EINTR"},
		{"read-hashes-EIO", "demo-replay", "hashes.txt", "read,pread64", "EIO"},
		{"stat-manifest-EIO", "prevention-verified", "manifest.json", stats, "EIO"},
		{"stat-hashes-EIO", "demo-replay", "hashes.txt", stats, "EIO"},
		// existsSync is access(F_OK), not stat (row 10).
		{"access-hashes-EIO", "demo-replay", "hashes.txt", "access,faccessat,faccessat2", "EIO"},
		{"access-root-EACCES", "git-two-states", "", "access,faccessat,faccessat2", "EACCES"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(argv ...string) (string, int) {
				dir := filepath.Join(t.TempDir(), "b")
				copyTree(t, filepath.Join("testdata", "bases", tc.base), dir)
				target := dir
				if tc.path != "" {
					target = filepath.Join(dir, tc.path)
				}
				trace := filepath.Join(t.TempDir(), "trace")
				args := []string{"-f", "-qq", "-o", trace, "-P", target, "-e", "trace=" + tc.calls,
					"-e", "inject=" + tc.calls + ":error=" + tc.errno + ":when=1"}
				cmd := exec.Command(strace, append(append(args, argv...), "verify", dir)...)
				out, _ := cmd.CombinedOutput()
				if b, _ := os.ReadFile(trace); !strings.Contains(string(b), "INJECTED") {
					t.Fatalf("%s: nothing injected on %s\n%s", argv[0], target, out)
				}
				return strings.ReplaceAll(string(out), dir, "<BUNDLE>"), cmd.ProcessState.ExitCode()
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
