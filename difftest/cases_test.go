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
		{"fatal: x; exit 999", "fatal: x; exit 999"},
		{"fatal: pack is corrupted\nerror: index-pack died; exit 1", "<GIT-STDERR>\nerror: index-pack died; exit 1"},
		{"PLANTED-BOGUS-DETAIL; exit 999", "PLANTED-BOGUS-DETAIL; exit 999"},
		{"The bundle records a complete history.\nfatal: y", "The bundle records a complete history.\n<GIT-STDERR>"},
	} {
		if got := maskGitDetail(tc.in); got != tc.want {
			t.Errorf("maskGitDetail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The writer masks: the case's directories, a loopback port, git's
// temporary verifier directory. A digest that happens to follow a mask is
// left alone.
func TestNormalizeWriter(t *testing.T) {
	r := writerRoots{ws: "/c/ws", tmp: "/c/tmp", home: "/c/home"}
	in := `{"store": "/c/ws/.faultline/witnesses"} /c/tmp/faultline-git-proof-verify-Ab12Cd/x HOME=/c/home ` +
		"http://127.0.0.1:41234/proof 127.0.0.1:x sha256:0123"
	want := `{"store": "<WS>/.faultline/witnesses"} <GITTMP>/x HOME=<HOME> ` +
		"http://127.0.0.1:<PORT>/proof 127.0.0.1:x sha256:0123"
	if got := normalizeWriter(in, r); got != want {
		t.Errorf("normalizeWriter:\n got %q\nwant %q", got, want)
	}
}

// On Windows the path after a mask keeps Node's "\" separators, escaped in
// JSON; both become "/" to compare with Linux goldens.
func TestSlashesAfter(t *testing.T) {
	got, slash := slashesAfter(`"<WS>\\a\\b.json" open '<WS>\c\d'`, "<WS>", true)
	if want := `"<WS>/a/b.json" open '<WS>/c/d'`; got != want || slash {
		t.Errorf("slashesAfter = %q, %v; want %q, false", got, slash, want)
	}
	if _, slash := slashesAfter("<WS>/a", "<WS>", true); !slash {
		t.Error("slashesAfter did not report a '/' after the mask")
	}
}
