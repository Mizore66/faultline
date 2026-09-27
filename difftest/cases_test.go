package difftest

import "testing"

// Only git's own diagnostic lines are masked; FaultLine's text and anything
// else around them must match exactly.
func TestMaskGitDetail(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"exit 128", "exit 128"},
		{"exit null", "exit null"},
		{"spawnSync git ENOENT", "spawnSync git ENOENT"},
		{"fatal: pack is corrupted (SHA1 mismatch)", "<GIT-STDERR>"},
		{"error: Repository lacks these prerequisite commits:\nerror: 0123 x", "<GIT-STDERR>"},
		{"fatal: x; spawnSync git ENOBUFS", "<GIT-STDERR>; spawnSync git ENOBUFS"},
		{"<GITTMP>/b is okay; spawnSync git ENOBUFS", "<GITTMP>/b is okay; spawnSync git ENOBUFS"},
		{"fatal: x\n  [go-only trailer]", "<GIT-STDERR>\n  [go-only trailer]"},
		{"fatal: x; exit 999", "<GIT-STDERR>"},
		{"PLANTED-BOGUS-DETAIL; exit 999", "PLANTED-BOGUS-DETAIL; exit 999"},
		{"The bundle records a complete history.\nfatal: y", "The bundle records a complete history.\n<GIT-STDERR>"},
	} {
		if got := maskGitDetail(tc.in); got != tc.want {
			t.Errorf("maskGitDetail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
