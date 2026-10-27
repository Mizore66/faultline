//go:build faultline_test

package difftest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
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
	// The turn verifier's and writer's labels: maskGitDetail runs on the text
	// after each, up to the next "\n- "; with maskGit false nothing changes.
	tree := strings.Repeat("0123456789", 4)
	for _, label := range []string{
		"Turn tree pack is not a valid Git pack: ",
		"Could not create temporary bare repository for pack verification: ",
		"Could not pack turn trees: ",
		"Could not enumerate objects for turn tree " + tree + ": ",
		"Could not enumerate objects for turn tree " + tree + strings.Repeat("a", 24) + ": ",
	} {
		in := "- " + label + "error: x\nfatal: y\n- " + label + "exit 3\n"
		want := "- " + label + "<GIT-STDERR>\n- " + label + "exit 3\n"
		if got := maskGitOutput(in, true); got != want {
			t.Errorf("maskGitOutput(%q) = %q, want %q", in, got, want)
		}
		if got := maskGitOutput(in, false); got != in {
			t.Errorf("maskGitOutput(%q, false) = %q", in, got)
		}
	}
	// A tree id that is not 40 or 64 hex digits is not the label.
	in := "Could not enumerate objects for turn tree " + strings.Repeat("g", 40) + ": fatal: y"
	if got := maskGitOutput(in, true); got != in {
		t.Errorf("maskGitOutput(%q) = %q", in, got)
	}
}

func TestTurnGitLabelsMasked(t *testing.T) {
	in := "- Turn tree pack is not a valid Git pack: fatal: early EOF\nfatal: index-pack failed\n- next"
	if got := maskGitOutput(in, true); got != "- Turn tree pack is not a valid Git pack: <GIT-STDERR>\n- next" {
		t.Fatalf("%q", got)
	}
	in = "x /tmp/faultline-turn-proof-pack-Ab12Cd/objects y"
	if got := maskGitOutput(in, true); got != "x <GITTMP>/objects y" {
		t.Fatalf("%q", got)
	}
	in = "Could not enumerate objects for turn tree " + strings.Repeat("a", 40) + ": fatal: bad object"
	if got := maskGitOutput(in, true); !strings.HasSuffix(got, ": <GIT-STDERR>") {
		t.Fatalf("%q", got)
	}
}

func TestPadFile(t *testing.T) {
	repo := repoRoot(t)
	work := t.TempDir()
	copyTree(t, filepath.Join(repo, "difftest", "testdata", "bases", "turn-two-states"), work)
	v, err := jsjson.Parse(`{"id":"x","op":"pad-file","file":"hashes.txt","total":4194304}`)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &template{fields: v.Obj()}
	if !applyMutation(work, testCase{id: "x", base: "turn-two-states", tpl: tpl, file: "hashes.txt"}) {
		t.Fatal("not applied")
	}
	if info, err := os.Stat(filepath.Join(work, "hashes.txt")); err != nil || info.Size() != 4194304 {
		t.Fatal(info, err)
	}
}

// resignTurn rewrites a turn bundle consistently: after a semantic edit of
// investigation.json the run artifacts, the manifest's index and digest and
// (with rehash) the catalog all follow, so only the semantic checks fail.
func TestResignTurn(t *testing.T) {
	repo := repoRoot(t)
	work := t.TempDir()
	copyTree(t, filepath.Join(repo, "difftest", "testdata", "bases", "turn-two-states"), work)
	v, err := jsjson.Parse(`{"id":"x","op":"json-edit","file":"investigation.json","jsonPath":["runs",3,"result","exitCode"],"value":0,"resignTurn":true,"rehash":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !applyMutation(work, testCase{id: "x", base: "turn-two-states", tpl: &template{fields: v.Obj()}, file: "investigation.json"}) {
		t.Fatal("not applied")
	}
	investigation, _ := parseObjectFile(filepath.Join(work, "investigation.json"))
	manifest, _ := parseObjectFile(filepath.Join(work, "manifest.json"))
	runs := investigation.Field("runs").Items()
	index := manifest.Field("artifacts").Get("runs").Items()
	if len(index) != len(runs) {
		t.Fatalf("%d runs, %d indexed", len(runs), len(index))
	}
	for i, run := range runs {
		id := run.Get("runId").Str()
		if want, _ := canonical.DigestJSON(withoutKey(run.Obj(), "runId")); id != want {
			t.Errorf("run %d: runId %s, want %s", i, id, want)
		}
		if index[i].Get("runId").Str() != id {
			t.Errorf("run %d: indexed as %s", i, index[i].Get("runId").Str())
		}
		if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(index[i].Get("path").Str()))); err != nil {
			t.Error(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(work, "runs"))
	if len(entries) != len(runs) {
		t.Errorf("%d run files, want %d", len(entries), len(runs))
	}
	if d, _ := canonical.DigestJSON(jsjson.MakeObject(investigation)); manifest.Field("investigationDigest").Str() != d {
		t.Errorf("investigationDigest %s, want %s", manifest.Field("investigationDigest").Str(), d)
	}
	stable := investigation.Field("stableStates").Items()[1].Get("runIds").Items()
	if stable[0].Str() != runs[3].Get("runId").Str() {
		t.Errorf("stableStates[1].runIds[0] = %s, want the re-signed %s", stable[0].Str(), runs[3].Get("runId").Str())
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

// A minimization run fact hashes its worktree path; when no recorder logged
// that path (a fault before git ran there), relocation still names the
// digest by where it appears, so it is masked whatever the case directory
// (round 7 #15).
func TestRelocationNamesUnloggedWorktreeDigest(t *testing.T) {
	for _, root := range []string{"/tmp/case-one", "/var/case-two"} {
		r := makeWriterRoots(t)
		digest := canonical.SHA256Hex(filepath.Join(root, "tmp", "faultline-git-minimization-x", "worktrees", "worktree-1"))
		if err := os.MkdirAll(filepath.Join(r.ws, ".faultline", "minimizations"), 0o755); err != nil {
			t.Fatal(err)
		}
		text := "{\n  \"runs\": [\n    {\n      \"worktreeDigest\": \"sha256:" + digest + "\"\n    }\n  ]\n}\n"
		if err := os.WriteFile(filepath.Join(r.ws, ".faultline", "minimizations", "m.json"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		got := relocation(t, r, nil).text(text)
		if want := "<PATHDIGEST:worktree:.faultline/minimizations/m.json#0>"; !strings.Contains(got, want) || strings.Contains(got, digest) {
			t.Errorf("%s: %s", root, got)
		}
	}
}

// A chmod mutation sets the permission bits it names, and a writer case
// applies it after resetModes (which would set the directory back to 0755);
// the restore cleanup makes the directory writable again. gen-writers.ts
// runCase does the same.
func TestChmodMutationAfterResetModes(t *testing.T) {
	if windows {
		t.Skip("POSIX permission bits")
	}
	repo := repoRoot(t)
	mutations, err := jsjson.Parse(`[{"op":"add-file","path":"ro/keep.txt","content":"keep\n"},{"op":"chmod","file":"ro","mode":"555"},{"op":"chmod","file":"ro/keep.txt","mode":"600"}]`)
	if err != nil {
		t.Fatal(err)
	}
	tpl := writerTemplate{ID: "chmod-self-test", Scenario: "empty"}
	for _, m := range mutations.Items() {
		tpl.Mutations = append(tpl.Mutations, template{m.Obj()})
	}
	r := makeWriterRoots(t)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	prepareWriterCase(t, repo, tpl, r)
	for rel, want := range map[string]os.FileMode{"ro": 0o555, "ro/keep.txt": 0o600} {
		info, err := os.Lstat(filepath.Join(r.ws, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", rel, got, want)
		}
	}
	if err := os.WriteFile(filepath.Join(r.ws, "ro", "new.txt"), nil, 0o644); err == nil {
		t.Error("a file was created in the 0555 directory")
	}
	restoreWritable(r.root)
	if info, err := os.Lstat(filepath.Join(r.ws, "ro")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("restoreWritable left ro at %v (%v)", info.Mode().Perm(), err)
	}
}
