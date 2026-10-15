//go:build faultline_test

package difftest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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

func TestNormalizeWriterMasksStagedPID(t *testing.T) {
	r := writerRoots{ws: "/c/ws", tmp: "/c/tmp", home: "/c/home"}
	in := "rename '/c/ws/.out.json.48213.00000000-0000-4000-8000-000000000001.tmp' -> '/c/ws/out.json'"
	want := "rename '<WS>/.out.json.<pid>.00000000-0000-4000-8000-000000000001.tmp' -> '<WS>/out.json'"
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

// The Windows go job's skip guard lists the writer cases it may skip by
// name (.github/scripts/windows-writer-skips.txt), so a case that is not
// posixOnly cannot be skipped there unnoticed; the list must name exactly
// the posixOnly cases.
func TestWindowsWriterSkipsList(t *testing.T) {
	repo := repoRoot(t)
	text, err := os.ReadFile(filepath.Join(repo, ".github", "scripts", "windows-writer-skips.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(text), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			listed = append(listed, line)
		}
	}
	want := []string{"TestWriterReplay/posix/recorder-fake-docker"}
	for _, tpl := range loadWriterTemplates(t, repo) {
		if tpl.PosixOnly {
			want = append(want, "TestWriterReplay/"+writerSubtest(tpl))
		}
	}
	slices.Sort(listed)
	slices.Sort(want)
	if !slices.Equal(listed, want) {
		t.Errorf("windows-writer-skips.txt lists\n%s\nthe posixOnly writer cases are\n%s", strings.Join(listed, "\n"), strings.Join(want, "\n"))
	}
}
