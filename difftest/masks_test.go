//go:build faultline_test && unix

package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// gitIn runs the harness's git in dir (identity from the arguments only).
func gitIn(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=FaultLine", "-c", "user.email=faultline@example.invalid"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestBridgeCommitNamesIgnoreTimeButNotTreeOrMessage(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "", "init", "-q")
	blob := gitIn(t, repo, "bad\n", "hash-object", "-w", "--stdin")
	tree := gitIn(t, repo, "100644 blob "+blob+"\tstate.txt\n", "mktree")
	before := map[string]map[string]bool{".": objectSet(t, repo)}
	msg := "faultline-turn-min after t1 (" + tree[:12] + ")"
	c1 := gitIn(t, repo, "", "commit-tree", tree, "-m", msg)
	time.Sleep(1100 * time.Millisecond) // a different committer second
	c2 := gitIn(t, repo, "", "commit-tree", tree, "-m", msg)
	c3 := gitIn(t, repo, "", "commit-tree", tree, "-m", "faultline-turn-min before t0 ("+tree[:12]+")")
	created := createdObjects(t, repo, before)["."]
	names := objectNames(t, repo, created, func(s string) string { return s })
	if c1 == c2 || names[c1] == "" || names[c1] != names[c2] || !strings.HasPrefix(names[c1], "<COMMIT:") {
		t.Fatalf("c1 %q c2 %q", names[c1], names[c2])
	}
	if names[c3] == names[c1] {
		t.Fatal("a different message must give a different name")
	}
	if _, ok := names[tree]; ok {
		t.Fatal("a tree that existed before the case is never masked")
	}
}

func TestTurnScenariosVerify(t *testing.T) {
	repo := repoRoot(t)
	for _, name := range []string{"turn-two-states", "turn-non-monotonic", "turn-pass-only", "turn-baseline-only", "turn-duplicate-ordinal", "turn-heterogeneous"} {
		r := makeWriterRoots(t)
		copyScenario(t, repo, name, r.ws)
		text, err := nodefs.ReadText(filepath.Join(r.ws, "ledger.json"))
		if err != nil {
			t.Fatal(err)
		}
		v, err := jsjson.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		if got := gitproof.VerifyCodexLifecycleLedger(v); !got.Valid {
			t.Errorf("%s: %v", name, got.Errors)
		}
	}
}

// The names are pinned (gen-writers.ts checkObjectNames pins the same
// three): every object created, the commit's message holding a case path
// that text() masks. Then the loose-object and reflog rules and the
// relocation seed: the created commit's loose file is compared by its
// name, a new reflog without its times, and a file that names the commit
// gets a <PATHDIGEST> name while its id is masked in text.
func TestCreatedObjectNamesArePinned(t *testing.T) {
	r := makeWriterRoots(t)
	repo := filepath.Join(r.ws, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "", "init", "-q")
	start := caseObjectsBefore(t, r.ws)
	blob := gitIn(t, repo, "bad\n", "hash-object", "-w", "--stdin")
	tree := gitIn(t, repo, "100644 blob "+blob+"\tstate.txt\n", "mktree")
	commit := gitIn(t, repo, "", "commit-tree", tree, "-m", "m "+r.ws)
	objects := caseObjects(t, r, start)
	want := map[string]string{blob: "<BLOB:bf9ddff4bbf5>", tree: "<TREE:b352e6817bf5>", commit: "<COMMIT:c43d2f3c0662>"}
	for id, name := range want {
		if objects.byRepo["repo"][id] != name {
			t.Errorf("%s: %q, want %q", id, objects.byRepo["repo"][id], name)
		}
	}
	reflog := "repo/.git/logs/refs/heads/x"
	if err := os.MkdirAll(filepath.Join(r.ws, filepath.FromSlash(filepath.Dir(reflog))), 0o755); err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("0", 40) + " " + commit + " FaultLine <f@x> %s\tm\n"
	if err := os.WriteFile(filepath.Join(r.ws, "names.txt"), []byte(commit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reloc := relocationWith(t, r, nil, objects)
	loose := "repo/.git/objects/" + commit[:2] + "/" + commit[2:]
	if got := reloc.fileDigest(loose, []byte("zlib bytes")); got != canonical.SHA256Hex("git-object:<COMMIT:c43d2f3c0662>") {
		t.Errorf("loose object digest %s", got)
	}
	if a, b := reloc.fileDigest(reflog, []byte(strings.Replace(line, "%s", "1700000000 +0000", 1))), reloc.fileDigest(reflog, []byte(strings.Replace(line, "%s", "1800000000 -0500", 1))); a != b {
		t.Error("a new reflog's times must not change its digest")
	}
	if got := reloc.text("id " + commit); got != "id <COMMIT:c43d2f3c0662>" {
		t.Errorf("text: %q", got)
	}
	if name := reloc.names[canonical.SHA256Hex(commit+"\n")]; name != "file:names.txt" {
		t.Errorf("a file naming a created object is not path-dependent: %q", name)
	}
	if got := relocation(t, r, nil).fileDigest(loose, []byte("zlib bytes")); got != canonical.SHA256Hex("zlib bytes") {
		t.Error("without created objects, a loose object keeps its byte digest")
	}
}
