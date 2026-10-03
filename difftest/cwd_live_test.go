package difftest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// process.cwd() errors come from libc's getcwd through libuv's uv_cwd. Run
// `verify` from broken working directories under both runtimes and compare
// stdout, stderr and the exit code: deleted and recreated directories,
// paths around PATH_MAX with short and long segments (glibc's generic walk
// and uv_cwd's ENOBUFS retry), unreadable ancestors (as an unprivileged
// user), and a cwd outside the process root after chroot(2).
func TestLiveCwdErrorsMatchNode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the scenarios use Linux mounts, chroot and setpriv")
	}
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	if os.Getenv("FAULTLINE_REQUIRE_ROOT") != "" && os.Geteuid() != 0 {
		t.Fatal("FAULTLINE_REQUIRE_ROOT is set but the test is not running as root")
	}
	repo, _ := filepath.Abs("..")
	cli := filepath.Join(repo, "dist", "cli.js")
	base, err := os.MkdirTemp("/tmp", "fl-cwd-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		exec.Command("chmod", "-R", "u+rwx", base).Run()
		os.RemoveAll(base)
	}()
	os.Chmod(base, 0o777)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	fl := filepath.Join(base, "fl")
	copyFile(t, flBinary, fl)
	os.Chmod(fl, 0o755)

	// segments splits the n-n0 bytes below a root of n0 bytes into one
	// leading segment and segments of seg bytes, so the deepest directory's
	// path is exactly n bytes long.
	segments := func(n0, n, seg int) []string {
		r := n - n0
		k := (r - 2) / (1 + seg)
		lead := r - k*(1+seg) - 1
		if lead > 255 || k < 0 || lead < 1 {
			t.Fatalf("cannot build a %d-byte cwd", n)
		}
		out := []string{strings.Repeat("a", lead)}
		for i := 1; i <= k; i++ {
			out = append(out, strings.Repeat(string(rune('a'+i%26)), seg))
		}
		return out
	}
	type scenario struct {
		name   string
		cwdLen int // 0: a short cwd
		seg    int
		post   []any // driver operations, run in the deepest directory
		nobody bool
		chroot bool
	}
	scenarios := []scenario{
		{name: "deleted", post: []any{[]any{"delete", false}}},
		{name: "recreated", post: []any{[]any{"delete", true}}},
		{name: "chroot", chroot: true},
		{name: "self0600", nobody: true, post: []any{[]any{"chmod", ".", 0o600}}},
		{name: "parent0311", nobody: true, post: []any{[]any{"chmod", "..", 0o311}}},
	}
	for _, n := range []int{4094, 4095, 4096, 4097, 4200} {
		for _, seg := range []int{1, 2, 200} {
			scenarios = append(scenarios, scenario{name: "len" + strconv.Itoa(n) + "seg" + strconv.Itoa(seg), cwdLen: n, seg: seg})
		}
		scenarios = append(scenarios,
			scenario{name: "gone" + strconv.Itoa(n), cwdLen: n, seg: 200, post: []any{[]any{"delete", false}}},
			scenario{name: "locked" + strconv.Itoa(n), cwdLen: n, seg: 200, nobody: true, post: []any{[]any{"chmod", "..", 0o311}}},
		)
	}
	// The driver builds the tree with relative mkdir/chdir (shells cannot cd
	// past PATH_MAX), applies the operations, optionally chroots and drops to
	// uid 65534, and execs the command in place.
	const driver = `import json, os, sys
spec = json.loads(sys.argv[1]); argv = sys.argv[2:]
uid = spec["uid"]
os.makedirs(spec["root"]); os.chdir(spec["root"])
if uid is not None: os.chown(".", uid, uid)
for s in spec["segs"]:
    os.mkdir(s)
    if uid is not None: os.chown(s, uid, uid)
    os.chdir(s)
for op in spec["post"]:
    if op[0] == "delete":
        parent = os.open("..", os.O_RDONLY)
        os.rmdir(spec["segs"][-1], dir_fd=parent)
        if op[1]: os.mkdir(spec["segs"][-1], dir_fd=parent)
        os.close(parent)
    elif op[0] == "chmod":
        os.chmod(op[1], op[2])
if spec["chroot"]: os.chroot(spec["chroot"])
if uid is not None:
    os.setgroups([]); os.setgid(uid); os.setuid(uid)
os.execv(argv[0], argv)
`
	runs := 0
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			if (sc.nobody || sc.chroot) && os.Geteuid() != 0 {
				// CI runs these in a separate step as root, which sets
				// FAULTLINE_REQUIRE_ROOT so that they cannot skip there.
				t.Skip("needs root (setuid, chroot and mount namespaces)")
			}
			run := func(argv ...string) [3]any {
				runs++
				root := filepath.Join(base, "r"+strconv.Itoa(runs))
				segs := []string{"c"}
				if sc.cwdLen > 0 {
					segs = segments(len(root), sc.cwdLen, sc.seg)
				}
				spec := map[string]any{"root": root, "segs": segs, "post": sc.post, "uid": nil, "chroot": nil}
				if sc.post == nil {
					spec["post"] = []any{}
				}
				if sc.nobody {
					spec["uid"] = 65534
				}
				if sc.chroot {
					spec["chroot"] = base + "/nr"
				}
				js, _ := json.Marshal(spec)
				args := append([]string{"python3", "-c", driver, string(js)}, argv...)
				cmd := exec.Command(args[0], args[1:]...)
				if sc.chroot {
					cmd = exec.Command("unshare", append([]string{"-m", "sh", "-c",
						"mkdir -p " + base + "/nr && mount --rbind / " + base + `/nr && exec "$@"`, "sh"}, args...)...)
				}
				cmd.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS=")
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				code := 0
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					code = exit.ExitCode()
				} else if err != nil {
					t.Fatal(err)
				}
				return [3]any{stdout.String(), stderr.String(), code}
			}
			ts := run(node, cli, "verify", "../bundle")
			goOut := run(fl, "verify", "../bundle")
			t.Logf("exit %v, stderr %q", ts[2], ts[1])
			if ts != goOut {
				t.Errorf("TS %q\nGo %q", ts, goOut)
			}
		})
	}
}

func copyFile(t *testing.T, from, to string) {
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
}
