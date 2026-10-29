//go:build faultline_test

package difftest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// safedir.EnsureRealDirectoryTree throws its rootMessage only when the
// filesystem root itself is not a real directory; every other component, the
// user-managed ones included, gets the traverse message (internal/safedir,
// src/github-provenance.ts:266, src/repair-brief-store.ts:108). So the two
// root texts below are unreachable, the same as the Git, prevention, turn and
// attestation ones, while the traverse texts stay reachable and must be
// produced by the live tests. This reads the real error literals of the two
// files and the real unreachableLiterals map, the inputs of
// TestGoldensReachEveryErrorLiteral and requireReached.
func TestEnsureRealDirectoryRootLiteralsAreClassified(t *testing.T) {
	repo := oracle.RepoRoot(t)
	for _, c := range []struct{ file, root, traverse string }{
		{"internal/provenance/provenance.go", "Provenance root must be a real directory:", "Provenance output cannot traverse a symbolic link or non-directory:"},
		{"internal/repair/store.go", "Repair brief root is not a real directory:", "Repair brief output cannot traverse a symbolic link or non-directory:"},
	} {
		literals := errorLiterals(t, filepath.Join(repo, filepath.FromSlash(c.file)))
		if _, ok := literals[c.root]; !ok {
			t.Errorf("%s no longer has the error literal %q", c.file, c.root)
		}
		if _, ok := literals[c.traverse]; !ok {
			t.Errorf("%s no longer has the error literal %q", c.file, c.traverse)
		}
		if reason, ok := unreachableLiterals[c.root]; !ok || !strings.Contains(reason, "filesystem root") {
			t.Errorf("%s: %q must be waived as the filesystem-root case, got %q (%v)", c.file, c.root, reason, ok)
		}
		// Nothing reachable is waived: no traverse text, and no other
		// real-directory or symlink text of the file.
		for lit := range literals {
			if lit == c.root {
				continue
			}
			if _, waived := unreachableLiterals[lit]; waived && (strings.Contains(lit, "traverse") || strings.Contains(lit, "symbolic link") || strings.Contains(lit, "real directory")) {
				t.Errorf("%s: %q is directory-traversal text and must stay reachable, but unreachableLiterals waives it", c.file, lit)
			}
		}
	}
}
