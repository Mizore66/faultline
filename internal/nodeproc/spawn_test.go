//go:build unix

package nodeproc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fakeGit = `#!/bin/sh
case "$1" in
both) /usr/bin/head -c "$2" /dev/zero; /usr/bin/head -c "$3" /dev/zero >&2 ;;
abort) echo partial >&2; /bin/kill -ABRT $$ ;;
code) echo oops >&2; exit "$2" ;;
*) echo "ran $0" ;;
esac
`

// Expectations were captured from Node 22's spawnSync with the same script.
func TestSpawnSyncMatchesNode(t *testing.T) {
	dir := t.TempDir()
	write := func(path string, mode os.FileMode) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(fakeGit), mode); err != nil {
			t.Fatal(err)
		}
	}
	bin, noexec, cwd := filepath.Join(dir, "bin"), filepath.Join(dir, "noexec"), filepath.Join(dir, "cwd")
	write(filepath.Join(bin, "git"), 0o755)
	write(filepath.Join(noexec, "git"), 0o644)
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
		{"not found", "/nonexist", "", []string{"x"}, "null", "spawnSync git ENOENT", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
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
