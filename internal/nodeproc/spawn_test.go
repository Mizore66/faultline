//go:build unix

package nodeproc

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const fakeGit = `#!/bin/sh
case "$1" in
both) /usr/bin/head -c "$2" /dev/zero; /usr/bin/head -c "$3" /dev/zero >&2 ;;
abort) echo partial >&2; /bin/kill -ABRT $$ ;;
code) echo oops >&2; exit "$2" ;;
env) printf "%s" "$FOO" | /usr/bin/od -An -tx1 | /usr/bin/tr -d " \n"; [ -S /dev/stdin ] && printf " sock"; [ -S /dev/stdout ] && printf " osock"; echo ;;
*) echo "ran $0" ;;
esac
`

// Expectations were captured from Node 22's spawnSync (Linux) with the same
// scripts and PATH values.
func TestSpawnSyncMatchesNode(t *testing.T) {
	dir := t.TempDir()
	write := func(path string, mode os.FileMode) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(fakeGit), mode); err != nil {
			t.Fatal(err)
		}
	}
	bin, noexec, cwd := filepath.Join(dir, "bin"), filepath.Join(dir, "noexec"), filepath.Join(dir, "cwd")
	badint, eaccint := filepath.Join(dir, "badint"), filepath.Join(dir, "eaccint")
	write(filepath.Join(bin, "git"), 0o755)
	os.MkdirAll(badint, 0o755)
	os.MkdirAll(eaccint, 0o755)
	os.WriteFile(filepath.Join(badint, "git"), []byte("#!/nonexistent/interp\n"), 0o755)
	os.WriteFile(filepath.Join(eaccint, "git"), []byte("#!/etc/hosts\n"), 0o755)
	longComponent := "/" + strings.Repeat("a", 300)
	skipped := strings.Repeat("/aa", pathMax/3+10)         // >= PATH_MAX: libuv skips it
	tooLong := strings.Repeat("/aa", (pathMax-2)/3) + "/a" // PATH_MAX-2 long: tried, execve says ENAMETOOLONG
	write(filepath.Join(noexec, "git"), 0o644)
	noShebang, empty := filepath.Join(dir, "nsw"), filepath.Join(dir, "empty")
	os.MkdirAll(noShebang, 0o755)
	os.MkdirAll(empty, 0o755)
	os.WriteFile(filepath.Join(noShebang, "git"), []byte("echo \"ran-noshebang $1\"\n"), 0o755)
	os.WriteFile(filepath.Join(empty, "git"), nil, 0o755)
	write(filepath.Join(cwd, "git"), 0o755)
	const max = 4 * 1024 * 1024
	for _, tc := range []struct {
		name, path, chdir string
		args              []string
		status            string // "null" or a number
		err, stdout       string
	}{
		{"shared budget overflows", bin, "", []string{"both", "3000000", "1500000"}, "null", "spawnSync git ENOBUFS", ""},
		{"shared budget fits", bin, "", []string{"both", "3000000", "1000000"}, "0", "", ""},
		{"stdout alone overflows", bin, "", []string{"both", "5000000", "0"}, "null", "spawnSync git ENOBUFS", ""},
		{"signal is null status", bin, "", []string{"abort"}, "null", "", ""},
		{"exit code", bin, "", []string{"code", "3"}, "3", "", ""},
		{"dot PATH entry", ".:/usr/bin", cwd, []string{"x"}, "0", "", "ran ./git\n"},
		{"empty PATH entry", ":/nonexist", cwd, []string{"x"}, "0", "", "ran git\n"},
		{"only non-executable", noexec, "", []string{"x"}, "null", "spawnSync git EACCES", ""},
		{"EACCES then found", noexec + ":" + bin, "", []string{"x"}, "0", "", "ran " + filepath.Join(bin, "git") + "\n"},
		// EACCES is kept when a later entry is missing (glibc and libuv alike).
		{"EACCES then missing", noexec + ":/nonexist", "", []string{"x"}, "null", "spawnSync git EACCES", ""},
		{"not found", "/nonexist", "", []string{"x"}, "null", "spawnSync git ENOENT", ""},
		{"missing interpreter moves on", badint + ":" + bin, "", []string{"x"}, "0", "", "ran " + filepath.Join(bin, "git") + "\n"},
		{"EACCES interpreter moves on", eaccint + ":" + bin, "", []string{"x"}, "0", "", "ran " + filepath.Join(bin, "git") + "\n"},
		{"EACCES interpreter alone", eaccint, "", []string{"x"}, "null", "spawnSync git EACCES", ""},
		{"long component ends the search", longComponent + ":" + bin, "", []string{"x"}, "null", "spawnSync git ENAMETOOLONG", ""},
		{"PATH_MAX entry skipped", skipped + ":" + bin, "", []string{"x"}, "0", "", "ran " + filepath.Join(bin, "git") + "\n"},
		{"entry below PATH_MAX tried", tooLong, "", []string{"x"}, "null", "spawnSync git ENAMETOOLONG", ""},
		{"last errno reported", "/nonexistent:/etc/hosts", "", []string{"x"}, "null", "spawnSync git ENOTDIR", ""},
		{"env re-encoded, socket stdio", bin, "", []string{"env"}, "0", "", "61efbfbd62 sock osock\n"},
		// glibc's execvp runs an ENOEXEC file under /bin/sh and tries the cwd
		// after a skipped entry; libuv's posix_spawn loop on macOS does not.
		{"no shebang", noShebang, "", []string{"x"}, onLinux("0", "null"), onLinux("", "spawnSync git ENOEXEC"), onLinux("ran-noshebang x\n", "")},
		{"empty file", empty, "", []string{"x"}, onLinux("0", "null"), onLinux("", "spawnSync git ENOEXEC"), ""},
		{"PATH_MAX entry then the cwd", skipped + ":" + noShebang, cwd, []string{"x"}, onLinux("0", "null"), onLinux("", "spawnSync git ENOEXEC"), onLinux("ran git\n", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
			t.Setenv("FOO", "a\xffb")
			if tc.chdir != "" {
				t.Chdir(tc.chdir)
			}
			r := SpawnSync("git", tc.args, max)
			status := "null"
			if r.Status != nil {
				status = strconv.Itoa(*r.Status)
			}
			errText := ""
			if r.Err != nil {
				errText = r.Err.Error()
			}
			if status != tc.status || errText != tc.err || tc.stdout != "" && string(r.Stdout) != tc.stdout {
				t.Fatalf("status=%s err=%q stdout=%.60q", status, errText, r.Stdout)
			}
			if tc.args[0] == "abort" && strings.TrimSpace(string(r.Stderr)) != "partial" {
				t.Fatalf("stderr %q", r.Stderr)
			}
			if errText == "spawnSync git ENOBUFS" && len(r.Stdout)+len(r.Stderr) <= max {
				t.Fatalf("overflow reported with only %d bytes", len(r.Stdout)+len(r.Stderr))
			}
		})
	}
}

func onLinux(linux, other string) string {
	if runtime.GOOS == "linux" {
		return linux
	}
	return other
}
