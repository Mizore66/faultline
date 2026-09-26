//go:build unix

package gitproof

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// gitWrapper puts a git on PATH that runs script for `bundle list-heads` and
// defers everything else to the real git.
func gitWrapper(t *testing.T, script string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\nif [ \"$1\" = bundle ] && [ \"$2\" = list-heads ]; then\n" + script + "\nfi\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

var twoStates = filepath.Join("..", "..", "..", "difftest", "testdata", "bases", "git-two-states")

// The shared stdout+stderr budget is covered in internal/nodeproc.
// Expected lines were captured from `node dist/cli.js verify` with the same
// wrappers.
func TestGitSpawnMatchesNode(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"output over 4 MiB", "echo warn >&2; /usr/bin/head -c 5000000 /dev/zero; exit 0",
			"portable Git source verification failed: Git bundle head listing failed: warn; spawnSync git ENOBUFS"},
		{"killed by a signal", "/bin/kill -ABRT $$",
			"portable Git source verification failed: Git bundle head listing failed: exit null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gitWrapper(t, tc.script)
			r := Verify(twoStates, "", false)
			if r.Valid || !slices.Contains(r.Errors, tc.want) {
				t.Fatalf("errors %q", r.Errors)
			}
		})
	}
}

func TestMkdtempErrorMatchesNode(t *testing.T) {
	t.Setenv("TMPDIR", "")
	t.Setenv("TMP", "/nonexistent/dir/")
	r := Verify(twoStates, "", false)
	want := "Git proof bundle verification failed safely: ENOENT: no such file or directory, mkdtemp '/nonexistent/dir/faultline-git-proof-verify-XXXXXX'"
	if r.Valid || !slices.Contains(r.Errors, want) {
		t.Fatalf("errors %q", r.Errors)
	}
}
