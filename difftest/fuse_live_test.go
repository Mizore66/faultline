//go:build linux

package difftest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestLiveFuseMatchesNode mounts a bundle through lyingfs (testdata/fuse), a
// FUSE filesystem that misreports what the glibc and libuv ports in
// internal/nodefs must follow: d_ino 0 and unknown d_types in readdir,
// getattr failing while a file is open (fstat), sizes that differ from the
// content, access(2) failures and one-shot EINTR. Each engine gets a fresh
// mount. Needs root, /dev/fuse and fusepy; CI runs it in the root step with
// FAULTLINE_REQUIRE_ROOT, where a missing prerequisite fails it.
//
// glibc 2.28 to 2.36's readdir skips d_ino 0 entries and 2.37+ lists them;
// Go follows 2.36 (KNOWN_DIFFERENCES.md). For an entry only the walk can
// see (an extra file), TS runs on a copy without it, which is what Node on
// glibc 2.36 sees, whatever C library this host's Node uses; an entry that
// is also opened by name needs Node on glibc 2.36 or older.
func TestLiveFuseMatchesNode(t *testing.T) {
	startRootOracle(t)
	require := os.Getenv("FAULTLINE_REQUIRE_ROOT") != ""
	skip := func(why string) {
		if require {
			t.Fatal(why)
		}
		t.Skip(why)
	}
	if os.Geteuid() != 0 {
		skip("needs root (FUSE mounts with allow_other)")
	}
	if _, err := os.Stat("/dev/fuse"); err != nil {
		skip("no /dev/fuse")
	}
	if exec.Command("python3", "-c", "import importlib.util as u, sys; sys.exit(0 if u.find_spec('fusepy') or u.find_spec('fuse') else 1)").Run() != nil {
		skip("no fusepy")
	}
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	firstFile := func(base, dir string) string {
		ents, err := os.ReadDir(filepath.Join("testdata", "bases", base, dir))
		if err != nil || len(ents) == 0 {
			t.Fatalf("%s/%s: %v", base, dir, err)
		}
		return ents[0].Name()
	}
	extra := func(dir string) {
		os.Symlink("/etc/hostname", filepath.Join(dir, "a"))
		os.WriteFile(filepath.Join(dir, "zz"), nil, 0o644)
	}
	cases := []struct {
		name, base string
		prep       func(dir string)
		env        []string
		hidden     []string // d_ino 0 names, which glibc 2.36 Node does not see
	}{
		{"zero-extra-demo", "demo-replay", func(d string) { os.WriteFile(filepath.Join(d, "zz.json"), []byte("{}"), 0o644) }, []string{"ZERO=zz.json"}, []string{"zz.json"}},
		{"zero-extra-git", "git-two-states", func(d string) { os.WriteFile(filepath.Join(d, "zz.json"), []byte("{}"), 0o644) }, []string{"ZERO=zz.json"}, []string{"zz.json"}},
		{"zero-declared-demo", "demo-replay", nil, []string{"ZERO=report.md"}, nil}, // glibc <= 2.36 only, below
		{"untyped-lstat-fail-git", "git-two-states", extra, []string{"UNTYPED=1", "GETATTR_FAIL=zz"}, nil},
		{"untyped-lstat-fail-prevention", "prevention-verified", extra, []string{"UNTYPED=1", "GETATTR_FAIL=zz"}, nil},
		{"dtype-wht-lstat-fail-demo", "demo-replay", extra, []string{"DTYPE=zz:160644", "GETATTR_FAIL=zz"}, nil},
		{"dtype-7-lstat-fail-git", "git-two-states", extra, []string{"DTYPE=zz:070644", "GETATTR_FAIL=zz"}, nil},
		{"fstat-eio-utf8-prevention", "prevention-verified", nil, []string{"OPEN_FAIL=manifest.json", "ATTR0=1", "DIRECT=1"}, nil},
		{"fstat-eio-utf8-demo", "demo-replay", nil, []string{"OPEN_FAIL=hashes.txt", "ATTR0=1", "DIRECT=1"}, nil},
		{"size-at-string-limit-demo", "demo-replay", nil, []string{"BIGSIZE=manifest.json:536870888", "ATTR0=1", "DIRECT=1"}, nil},
		{"size-short-buffer-demo", "demo-replay", nil, []string{"BIGSIZE=" + firstFile("demo-replay", "runs") + ":10", "ATTR0=1", "DIRECT=1"}, nil},
		{"size-short-buffer-git", "git-two-states", nil, []string{"BIGSIZE=" + firstFile("git-two-states", "runs") + ":10", "ATTR0=1", "DIRECT=1"}, nil},
		{"read-eintr-demo", "demo-replay", nil, []string{"ONCE=read:hashes.txt:4", "DIRECT=1"}, nil},
		{"readdir-eintr-demo", "demo-replay", nil, []string{"ONCE=readdir:/:4"}, nil},
		{"opendir-eintr-git", "git-two-states", nil, []string{"ONCE=opendir:runs:4"}, nil},
		{"access-eio-demo", "demo-replay", nil, []string{"ACCESS_FAIL=hashes.txt:5"}, nil},
	}
	fs := filepath.Join(repo, "difftest", "testdata", "fuse", "lyingfs.py")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.HasPrefix(tc.name, "zero-") && tc.hidden == nil && !glibcSkipsInodeZero() {
				// A d_ino 0 entry that is also opened by name: only Node on
				// glibc 2.28 to 2.36 can show Go's behaviour here.
				t.Skip("Node's glibc lists d_ino 0 entries (2.37+ or musl): documented difference")
			}
			work := t.TempDir()
			src := filepath.Join(work, "src")
			copyTree(t, filepath.Join("testdata", "bases", tc.base), src)
			if tc.prep != nil {
				tc.prep(src)
			}
			run := func(dir string, argv ...string) string {
				cmd := exec.Command(argv[0], append(argv[1:], "verify", dir)...)
				cmd.Dir = work
				var out, errb bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errb
				cmd.Run()
				s := out.String() + "\n--- stderr\n" + errb.String()
				return strings.ReplaceAll(s, dir, "<BUNDLE>") + "\n--- exit " + strconv.Itoa(cmd.ProcessState.ExitCode())
			}
			mounted := func(argv ...string) string {
				mnt := filepath.Join(work, "mnt")
				os.MkdirAll(mnt, 0o755)
				fsCmd := exec.Command("python3", fs, src, mnt)
				fsCmd.Env = append(os.Environ(), tc.env...)
				var fsErr bytes.Buffer
				fsCmd.Stderr = &fsErr
				if err := fsCmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if exec.Command("fusermount", "-u", mnt).Run() != nil {
						exec.Command("umount", "-l", mnt).Run()
					}
					fsCmd.Process.Kill()
					fsCmd.Wait()
				}()
				for i := 0; ; i++ {
					if b, _ := os.ReadFile("/proc/self/mounts"); bytes.Contains(b, []byte(" "+mnt+" ")) {
						break
					}
					if i == 100 {
						t.Fatalf("lyingfs did not mount: %s", fsErr.String())
					}
					time.Sleep(50 * time.Millisecond)
				}
				return run(mnt, argv...)
			}
			var ts string
			if tc.hidden != nil {
				plain := filepath.Join(work, "plain")
				copyTree(t, src, plain)
				for _, name := range tc.hidden {
					os.Remove(filepath.Join(plain, name))
				}
				ts = run(plain, node, filepath.Join(repo, "dist", "cli.js"))
			} else {
				ts = mounted(node, filepath.Join(repo, "dist", "cli.js"))
			}
			goOut := mounted(flBinary)
			if ts != goOut {
				t.Fatalf("TS:\n%.1500s\nGo:\n%.1500s", ts, goOut)
			}
			t.Logf("%.120q", ts)
		})
	}
}

// glibcSkipsInodeZero reports whether this host's glibc (which the official
// Node binary links against) is 2.28 to 2.36.
func glibcSkipsInodeZero() bool {
	out, err := exec.Command("getconf", "GNU_LIBC_VERSION").Output()
	if err != nil {
		return false
	}
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "glibc %d.%d", &major, &minor); err != nil {
		return false
	}
	return major == 2 && minor >= 28 && minor <= 36
}
