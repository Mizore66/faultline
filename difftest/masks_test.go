//go:build faultline_test && unix

package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// gitIn runs the harness's git in dir (identity from the arguments only).
func gitIn(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=FaultLine Test", "-c", "user.email=faultline@example.test"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestCommitNamesIgnoreTimeButNotTreeParentOrMessage(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "", "init", "-q")
	base := gitIn(t, repo, "", "commit-tree", gitIn(t, repo, "", "write-tree"), "-m", "base")
	before := map[string]map[string]bool{".": objectSet(t, repo)}
	blob := gitIn(t, repo, "/case/ws/x\n", "hash-object", "-w", "--stdin")
	tree := gitIn(t, repo, "100644 blob "+blob+"\tREADME.md\n", "mktree")
	c1 := gitIn(t, repo, "", "commit-tree", tree, "-p", base, "-m", "faultline: repaired prevention state")
	time.Sleep(1100 * time.Millisecond) // a different committer second: a different id, the same name
	c2 := gitIn(t, repo, "", "commit-tree", tree, "-p", base, "-m", "faultline: repaired prevention state")
	c3 := gitIn(t, repo, "", "commit-tree", tree, "-p", base, "-m", "other message")
	created := createdObjects(t, repo, before)["."]
	names := objectNames(t, repo, created, func(s string) string { return strings.ReplaceAll(s, "/case/ws", "<WS>") })
	if c1 == c2 || names[c1] == "" || names[c1] != names[c2] || !strings.HasPrefix(names[c1], "<COMMIT:") {
		t.Fatalf("c1 %q c2 %q", names[c1], names[c2])
	}
	if names[c3] == names[c1] {
		t.Fatal("a different message must give a different name")
	}
	if _, ok := names[base]; ok {
		t.Fatal("an object that existed before the case is never masked")
	}
	if !strings.HasPrefix(names[blob], "<BLOB:") || !strings.HasPrefix(names[tree], "<TREE:") {
		t.Fatal(names[blob], names[tree])
	}
}

// An object whose loose file lives in the turn-snapshot store is listed by
// cat-file through the alternates entry, but it already gets
// snapshotObjectDigest: it is never a created object.
func TestCreatedObjectsLeaveOutTheSnapshotStore(t *testing.T) {
	ws := t.TempDir()
	repo := filepath.Join(ws, "main")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "", "init", "-q")
	store := filepath.Join(repo, ".git", "faultline", "objects")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "objects", "info", "alternates"), []byte(store+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := map[string]map[string]bool{"main": objectSet(t, repo)}
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader("snapshot\n")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_OBJECT_DIRECTORY=" + store}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	snap := strings.TrimSpace(string(out))
	own := gitIn(t, repo, "own\n", "hash-object", "-w", "--stdin")
	created := createdObjects(t, ws, before)["main"]
	if _, ok := created[snap]; ok {
		t.Fatal("a snapshot-store object must not be a created object")
	}
	if created[own] != "blob" || len(created) != 1 {
		t.Fatalf("created %v", created)
	}
}

// A reflog that is new or changed is hashed with its times masked; one that
// existed before keeps its byte digest.
func TestReflogTimesAreMaskedOnlyWhenNewOrChanged(t *testing.T) {
	r := makeWriterRoots(t)
	logs := filepath.Join(r.ws, ".git", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sec string) []byte {
		return []byte("0000000000000000000000000000000000000000 1111111111111111111111111111111111111111 A <a@b> " + sec + " +0000\tcommit: x\n")
	}
	if err := os.WriteFile(filepath.Join(logs, "HEAD"), line("100"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := snapshotGit(t, r.ws)
	rel := relocation(t, r, nil, base)
	if got := rel.fileDigest(".git/logs/HEAD", line("100")); got != canonical.SHA256HexBytes(line("100")) {
		t.Fatal("an unchanged reflog keeps its byte digest")
	}
	a, b := rel.fileDigest(".git/logs/HEAD", line("200")), rel.fileDigest(".git/logs/HEAD", line("300"))
	if a != b || a == canonical.SHA256HexBytes(line("200")) {
		t.Fatal("a changed reflog is compared with its times masked")
	}
	if rel.fileDigest(".git/logs/refs/heads/new", line("1")) != rel.fileDigest(".git/logs/refs/heads/new", line("2")) {
		t.Fatal("a new reflog is compared with its times masked")
	}
}

// repairedRunSeeds recomputes the executionId and the three runIds from the
// bundle's PASS run, and drops a candidate that appears in no file.
func TestRepairedRunSeedsNameExecutionAndRuns(t *testing.T) {
	r := makeWriterRoots(t)
	worktree := filepath.Join(r.root, "managed", "worktree")
	records := []string{"git -C\x1f" + worktree}
	d := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	run := `{"schemaVersion":"faultline.git-investigation.v1","frozenDigest":"` + d("a") + `","witnessDigest":"` + d("b") +
		`","result":{"verdict":"PASS","reason":"PREDICATE_PASS"},"sandbox":{"policyDigest":"` + d("c") + `","environmentPolicyDigest":"` + d("d") + `"}}`
	inv := `{"environment":{"distinctDigests":["` + d("e") + `"]}}`
	texts := map[string]string{"bundle/runs/x.json": run, "bundle/investigation.json": inv}
	commits := map[string]string{strings.Repeat("1", 40): strings.Repeat("2", 40)}
	if got := repairedRunSeeds(t, r, records, commits, texts); len(got) != 0 {
		t.Fatalf("a candidate that appears nowhere must be dropped: %v", got)
	}
	// The executionId, computed here from the brief's field list.
	execution := canonicalDigest(t, `{"kind":"faultline.repair-verify-execution.v1","executor":"NATIVE_DOCKER","verdict":"PASS","reason":"PREDICATE_PASS","policyDigest":"`+d("c")+
		`","environmentPolicyDigest":"`+d("d")+`","exitCode":0,"stdoutDigest":"sha256:773bb529c40df319318e7267118a3290e3fe3d67bac13808329f2e9a465b6a1f","stderrDigest":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","worktree":"`+worktree+`","frozenDigest":"`+d("a")+`"}`)
	texts["prevention.json"] = execution
	got := repairedRunSeeds(t, r, records, commits, texts)
	if len(got) != 4 || got[0][0] != execution[7:] || got[0][1] != "exec:"+normalizeWriter(worktree, r) {
		t.Fatalf("exec seed: %v (want %s)", got, execution)
	}
	runID := canonicalDigest(t, `{"schemaVersion":"faultline.prevention-repaired-run.v1","attempt":2,"commit":"`+strings.Repeat("1", 40)+`","tree":"`+strings.Repeat("2", 40)+
		`","witnessDigest":"`+d("b")+`","environmentDigest":"`+d("e")+`","executionId":"`+execution+`","detail":"Frozen witness PREDICATE_PASS on repaired worktree (NATIVE_DOCKER)."}`)
	if got[2][0] != runID[7:] || got[2][1] != "run:2" {
		t.Fatalf("run seeds: %v (want %s as run:2)", got, runID)
	}
}

func canonicalDigest(t *testing.T, text string) string {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	d, err := canonical.DigestJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
