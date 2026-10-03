package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// 22,369,622 index-keyed entries abort V8 only when they go into a
// dictionary; a dense range stays in a FixedArray, and V8 keeps every member
// of the object. Round-5 §2 P0, both directions: the entries before the
// manifest's members (TS VALID), and before a forged schemaVersion (TS
// INVALID).
func TestLiveIndexEntriesKeepMembers(t *testing.T) {
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	entries := strings.Repeat(`"0":0,`, 22_369_622)
	for _, tc := range []struct {
		name  string
		build func(members string) string // members: the manifest's own, without braces
	}{
		{"entries-first", func(m string) string { return "{" + entries + m + "}" }},
		{"forged-last", func(m string) string { return "{" + m + "," + entries + `"schemaVersion":"evil"}` }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "b")
			copyTree(t, filepath.Join("testdata", "bases", "demo-replay"), dir)
			path := filepath.Join(dir, "manifest.json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			members := strings.TrimSpace(string(b))
			members = strings.TrimSpace(members[1 : len(members)-1])
			if err := os.WriteFile(path, []byte(tc.build(members)), 0o644); err != nil {
				t.Fatal(err)
			}
			rehash(dir, "demo-replay")
			run := func(argv ...string) (string, int) {
				cmd := exec.Command(argv[0], append(argv[1:], "verify", dir)...)
				out, _ := cmd.CombinedOutput()
				return strings.ReplaceAll(string(out), dir, "<BUNDLE>"), cmd.ProcessState.ExitCode()
			}
			tsOut, tsCode := run(node, filepath.Join(repo, "dist", "cli.js"))
			goOut, goCode := run(flBinary)
			if tsOut != goOut || tsCode != goCode {
				t.Fatalf("TS exit %d:\n%.600s\nGo exit %d:\n%.600s", tsCode, tsOut, goCode, goOut)
			}
			t.Logf("both exit %d: %.80q", tsCode, tsOut)
		})
	}
}
