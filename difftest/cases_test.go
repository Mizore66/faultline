//go:build faultline_test

package difftest

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// Slice 4a masks: a sidecar receipt stage's key and pid (<STAGEKEY>), after
// the .<pid>.<uuid>.tmp mask, and sidecar status's lock age (<LOCKAGE>).
func TestSlice4aMasks(t *testing.T) {
	r := writerRoots{ws: "/c/ws", tmp: "/c/tmp", home: "/c/home"}
	in := "rename '/c/ws/.git/faultline/recordings/.0123456789abcdef01234567.4242.tmp' -> 'x'\n" +
		`"lock": {"state": "ACTIVE", "ageMilliseconds": 17}`
	want := "rename '<WS>/.git/faultline/recordings/.<STAGEKEY>.<pid>.tmp' -> 'x'\n" +
		`"lock": {"state": "ACTIVE", "ageMilliseconds": "<LOCKAGE>"}`
	if got := normalizeWriter(in, r); got != want {
		t.Fatalf("%q", got)
	}
	// The ledger's own stage keeps the slice 2 mask.
	ledger := "/c/ws/.l.json.48213.00000000-0000-4000-8000-000000000001.tmp"
	if got := normalizeWriter(ledger, r); got != "<WS>/.l.json.<pid>.00000000-0000-4000-8000-000000000001.tmp" {
		t.Fatalf("%q", got)
	}
}

func TestWriterTemplateInputs(t *testing.T) {
	repo := repoRoot(t)
	for _, tpl := range loadWriterTemplates(t, repo) {
		if len(tpl.Input) > len(tpl.Argv) {
			t.Errorf("%s: %d inputs for %d invocations", tpl.ID, len(tpl.Input), len(tpl.Argv))
		}
	}
}

// writers.d: the templates load after writers.json, each from its file, and
// every template's goldens are in the golden file named after its template
// file (none elsewhere).
func TestWriterTemplateFiles(t *testing.T) {
	repo := repoRoot(t)
	files := writerTemplateFiles(t, repo)
	if files[0] != "writers.json" || !slices.IsSorted(files[1:]) {
		t.Fatalf("%q", files)
	}
	if got := writerGoldenPath("writers.d/4a-harness.json"); got != "writers.d/4a-harness.jsonl" {
		t.Fatal(got)
	}
	inFile := map[string]string{}
	for _, tpl := range loadWriterTemplates(t, repo) {
		inFile[tpl.ID] = writerGoldenPath(tpl.File)
	}
	for _, file := range append([]string{writerGoldenFile}, func() []string {
		var out []string
		for _, f := range files[1:] {
			out = append(out, writerGoldenPath(f))
		}
		return out
	}()...) {
		b, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", "golden", filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line == "" {
				continue
			}
			v, err := jsjson.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			if id := v.Get("case").Str(); inFile[id] != file {
				t.Errorf("%s: golden of %s, whose template is in %s", file, id, inFile[id])
			}
		}
	}
}

// A template id repeated in another template file fails the loader, and
// input with inputHex, a malformed inputHex and a mutation inv past the
// last invocation fail too (gen-writers.ts checkTemplates).
func TestWriterTemplateLoaderRefuses(t *testing.T) {
	for name, tc := range map[string]struct{ main, extra, want string }{
		"duplicate across files": {`[{"id":"a","scenario":"empty","argv":[["help"]]}]`, `[{"id":"a","scenario":"empty","argv":[["help"]]}]`, "duplicated"},
		"input and inputHex":     {`[]`, `[{"id":"b","scenario":"empty","argv":[["help"]],"input":["x"],"inputHex":["00"]}]`, "both input and inputHex"},
		"bad hex":                {`[]`, `[{"id":"b","scenario":"empty","argv":[["help"]],"inputHex":["zz"]}]`, "inputHex"},
		"inv past the end":       {`[]`, `[{"id":"b","scenario":"empty","argv":[["help"]],"mutations":[{"op":"add-file","path":"x","content":"","inv":1}]}]`, "not one of its invocations"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			dir := filepath.Join(repo, "difftest", "testdata")
			if err := os.MkdirAll(filepath.Join(dir, writerGoldenDir), 0o755); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, "writers.json"), []byte(tc.main), 0o644)
			os.WriteFile(filepath.Join(dir, writerGoldenDir, "x.json"), []byte(tc.extra), 0o644)
			ft := &fatalRecorder{TB: t}
			func() {
				defer func() { recover() }()
				loadWriterTemplates(ft, repo)
			}()
			if !strings.Contains(ft.msg, tc.want) {
				t.Errorf("loader said %q, want %q", ft.msg, tc.want)
			}
		})
	}
}

// fatalRecorder records a Fatalf instead of ending the test.
type fatalRecorder struct {
	testing.TB
	msg string
}

func (f *fatalRecorder) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	panic(f)
}

func (f *fatalRecorder) Fatal(args ...any) {
	f.msg = fmt.Sprint(args...)
	panic(f)
}

// <REPEAT:c:n> expands to n copies of c, at most maxRepeat.
func TestExpandRepeats(t *testing.T) {
	if got, ok := expandRepeats("{\"p\":\"<REPEAT:a:3>\",\"q\":\"<REPEAT:é:2><REPEAT:\n:1><REPEAT:ab:1>\"}"); !ok || got != "{\"p\":\"aaa\",\"q\":\"éé\n<REPEAT:ab:1>\"}" {
		t.Fatalf("%q %v", got, ok)
	}
	if got, ok := expandRepeats("<REPEAT:a:8388608>"); !ok || len(got) != maxRepeat {
		t.Fatal(len(got), ok)
	}
	if _, ok := expandRepeats("<REPEAT:a:8388609>"); ok {
		t.Fatal("over the bound")
	}
}

// add-symlink replaces a file or a dangling link and makes a missing path;
// set-mtime sets a symbolic link's own times, not its target's.
func TestSymlinkAndMtimeMutations(t *testing.T) {
	if windows {
		t.Skip("symbolic links")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "target.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("y"), 0o644)
	apply := func(js string) bool {
		v, err := jsjson.Parse(js)
		if err != nil {
			t.Fatal(err)
		}
		m := template{v.Obj()}
		return applyMutation(root, testCase{id: "x", base: "writer", file: m.str("path"), tpl: &m})
	}
	for _, p := range []string{"file.txt", "dir/new-link", "dangling"} {
		if !apply(`{"op":"add-symlink","path":"` + p + `","target":"target.txt"}`) {
			t.Fatalf("add-symlink %s", p)
		}
		if got, err := os.Readlink(filepath.Join(root, p)); err != nil || got != "target.txt" {
			t.Fatalf("%s -> %q %v", p, got, err)
		}
	}
	os.Mkdir(filepath.Join(root, "adir"), 0o755)
	if apply(`{"op":"add-symlink","path":"adir","target":"target.txt"}`) {
		t.Error("add-symlink replaced a directory")
	}
	if !apply(`{"op":"set-mtime","path":"file.txt","iso":"2000-01-01T00:00:00.000Z"}`) {
		t.Fatal("set-mtime")
	}
	link, _ := os.Lstat(filepath.Join(root, "file.txt"))
	target, _ := os.Stat(filepath.Join(root, "target.txt"))
	if want := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC); !link.ModTime().Equal(want) || target.ModTime().Equal(want) {
		t.Errorf("link %v, target %v", link.ModTime(), target.ModTime())
	}
}

// Relocation walks a .git's faultline/recordings, and nothing else of it.
func TestRelocationWalksSidecarRecordings(t *testing.T) {
	r := makeWriterRoots(t)
	for rel, text := range map[string]string{
		".git/faultline/recordings/codex-x.json":  `{"workingDirectory": "` + r.ws + `"}`,
		".git/faultline/turn-snapshot-cache.json": `{"repositoryRoot": "` + r.ws + `"}`,
		".git/config": r.ws,
	} {
		path := filepath.Join(r.ws, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(text), 0o644)
	}
	reloc := relocation(t, r, nil)
	var names []string
	for _, n := range reloc.names {
		names = append(names, n)
	}
	slices.Sort(names)
	if want := []string{"file:.git/faultline/recordings/codex-x.json", "json:.git/faultline/recordings/codex-x.json#"}; !slices.Equal(names, want) {
		t.Errorf("%q", names)
	}
}
