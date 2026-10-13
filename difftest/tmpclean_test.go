package difftest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The git verifier creates its temporary repository with mkdtemp in
// os.tmpdir() and removes it in a finally, as TS does; nothing in a golden
// can see a leftover directory (round 6, mutant M50). Run verify with a
// TMPDIR of its own and require it to be empty afterwards, after a clean
// run and after git failed half way.
func TestGitVerifierLeavesNoTemporaryRepository(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the failing case uses a POSIX fake git")
	}
	repo, _ := filepath.Abs("..")
	bundle := filepath.Join(repo, "difftest", "testdata", "bases", "git-two-states")
	for _, fake := range []string{"", "fail-verify", "fail-diff"} {
		t.Run("fake="+fake, func(t *testing.T) {
			tmp := t.TempDir()
			env := map[string]string{"TMPDIR": tmp}
			if fake != "" {
				env["PATH"] = filepath.Join(repo, "difftest", "testdata", "fakegit", fake) + string(os.PathListSeparator) + os.Getenv("PATH")
			}
			stdout, stderr, _ := runFl(t, repo, env, "verify", bundle)
			if left, _ := os.ReadDir(tmp); len(left) != 0 {
				t.Fatalf("%d entries left in TMPDIR (first %q)\n%s%s", len(left), left[0].Name(), stdout, stderr)
			}
		})
	}
}
