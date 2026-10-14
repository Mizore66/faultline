package difftest

import (
	"bufio"
	"bytes"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// writerGoldenFile is the writer replay's golden in difftest/testdata/golden
// (difftest/gen/gen-writers.ts writes it).
const writerGoldenFile = "writers.jsonl"

var windows = runtime.GOOS == "windows"

func repoRoot(t *testing.T) string { return oracle.RepoRoot(t) }

// writerTemplate is one entry of difftest/testdata/writers.json: argv runs
// in order in one workspace built from Scenario and then Mutations.
type writerTemplate struct {
	ID, Scenario, FakeDocker string
	Argv                     [][]string
	Env                      [][2]string
	PosixOnly                bool
	Mutations                []template
}

// writerResult is one invocation, normalized like its golden line.
type writerResult struct {
	Inv            int
	Stdout, Stderr string
	Exit           int
	Tree, Modes    string
	Files          map[string]string
	Argv           []string
	// contents are the bytes of the regular files in Files, for the report.
	contents map[string][]byte
}

var (
	writerTemplateKeys = []string{"id", "scenario", "argv", "fakeDocker", "env", "posixOnly", "mutations"}
	writerIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// fileEntry is a regular file's tree entry: its SHA-256, then its mode
	// except on Windows.
	fileEntry = regexp.MustCompile(`^[0-9a-f]{64}(?: [0-7]{1,4})?$`)
)

func loadWriterTemplates(t *testing.T, repo string) []writerTemplate {
	text, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", "writers.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(text))
	if err != nil {
		t.Fatal(err)
	}
	var out []writerTemplate
	ids := map[string]bool{}
	for _, item := range v.Items() {
		o := item.Obj()
		for _, k := range o.Keys() {
			if !slices.Contains(writerTemplateKeys, k) {
				t.Fatalf("writer template %s: unknown key %s", o.Field("id").Str(), k)
			}
		}
		tpl := writerTemplate{ID: o.Field("id").Str(), Scenario: o.Field("scenario").Str(), FakeDocker: o.Field("fakeDocker").Str(),
			PosixOnly: o.Field("posixOnly").Bool()}
		if ids[tpl.ID] || !writerIDPattern.MatchString(tpl.ID) {
			t.Fatalf("writer template id %q is duplicated or not lowercase kebab-case", tpl.ID)
		}
		ids[tpl.ID] = true
		for _, inv := range o.Field("argv").Items() {
			var args []string
			for _, a := range inv.Items() {
				args = append(args, a.Str())
			}
			tpl.Argv = append(tpl.Argv, args)
		}
		if len(tpl.Argv) == 0 {
			t.Fatalf("writer template %s has no invocation", tpl.ID)
		}
		if env, ok := o.Get("env"); ok {
			for _, k := range env.Obj().Keys() {
				tpl.Env = append(tpl.Env, [2]string{k, env.Obj().Field(k).Str()})
			}
		}
		for _, m := range o.Field("mutations").Items() {
			tpl.Mutations = append(tpl.Mutations, template{m.Obj()})
		}
		out = append(out, tpl)
	}
	return out
}

func loadWriterGoldens(t *testing.T, repo string) map[string]*jsjson.Obj {
	f, err := os.Open(filepath.Join(repo, "difftest", "testdata", "golden", writerGoldenFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]*jsjson.Obj{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<26)
	for scanner.Scan() {
		v, err := jsjson.Parse(scanner.Text())
		if err != nil {
			t.Fatal(err)
		}
		if err := checkWriterGolden(v); err != "" {
			t.Fatalf("%s line %q: %s", writerGoldenFile, scanner.Text(), err)
		}
		key := v.Get("case").Str() + "|" + itoa(int64(v.Get("inv").Num()))
		if out[key] != nil {
			t.Fatalf("%s: duplicate golden %s", writerGoldenFile, key)
		}
		out[key] = v.Obj()
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// writerGoldenKinds is the shape of a golden line (gen-writers.ts runCase).
var writerGoldenKinds = []struct {
	key  string
	kind jsjson.Kind
}{
	{"case", jsjson.String}, {"inv", jsjson.Number}, {"stdout", jsjson.String}, {"stderr", jsjson.String},
	{"exit", jsjson.Number}, {"tree", jsjson.String}, {"modes", jsjson.String}, {"files", jsjson.Object}, {"argv", jsjson.Array},
}

// checkWriterGolden reports a golden line that lacks a field, has one of
// the wrong type, or has one more; "" when the line is well formed.
func checkWriterGolden(v jsjson.Value) string {
	if v.Kind() != jsjson.Object {
		return "not an object"
	}
	for _, f := range writerGoldenKinds {
		field, ok := v.Obj().Get(f.key)
		if !ok || field.Kind() != f.kind {
			return "missing or mistyped " + f.key
		}
	}
	if v.Obj().Len() != len(writerGoldenKinds) {
		return "unexpected field"
	}
	for _, k := range v.Get("files").Obj().Keys() {
		if v.Get("files", k).Kind() != jsjson.String {
			return "files entry " + k + " is not a string"
		}
	}
	for _, a := range v.Get("argv").Items() {
		if a.Kind() != jsjson.String {
			return "argv record is not a string"
		}
	}
	return ""
}

// makeWriterRoots makes the workspace (the command's cwd), HOME and TMPDIR
// under one physical temporary directory, so the paths fl prints are the
// ones normalizeWriter masks.
func makeWriterRoots(t *testing.T) writerRoots {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := writerRoots{root: root, ws: filepath.Join(root, "ws"), home: filepath.Join(root, "home"), tmp: filepath.Join(root, "tmp"),
		argvLog: filepath.Join(root, "argv.log")}
	for _, dir := range []string{r.ws, r.home, r.tmp} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.home, "empty.gitconfig"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return r
}

// inheritedOut names the variables a writer case never inherits: git's own,
// FaultLine's, and those src/ reads to detect CI or a live model
// (difftest/gen/scenarios.ts inheritedOut).
func inheritedOut(name string) bool {
	return strings.HasPrefix(name, "GIT_") || strings.HasPrefix(name, "FAULTLINE_") ||
		name == "CI" || name == "GITHUB_ACTIONS" || name == "OPENAI_API_KEY"
}

// writerEnv is difftest/gen/scenarios.ts writerEnv: the whole environment
// of one invocation.
func writerEnv(repo string, r writerRoots, seed string, tpl writerTemplate) []string {
	// Windows names are case-insensitive (its PATH is often "Path"): one
	// entry per name, so a value set here replaces the inherited one.
	vars := map[string]string{}
	names := map[string]string{}
	var order []string
	set := func(k, v string) {
		id := k
		if windows {
			id = strings.ToUpper(k)
		}
		if _, ok := vars[id]; !ok {
			order = append(order, id)
			names[id] = k
		}
		vars[id] = v
	}
	for _, kv := range os.Environ() {
		if k, v, _ := strings.Cut(kv, "="); k != "" && !inheritedOut(k) {
			set(k, v)
		}
	}
	path := []string{filepath.Join(repo, "difftest", "testdata", "recorder")}
	if tpl.FakeDocker != "" {
		path = append(path, filepath.Join(repo, "difftest", "testdata", "fakedocker", tpl.FakeDocker))
	}
	path = append(path, os.Getenv("PATH"))
	for _, kv := range [][2]string{
		{"HOME", r.home}, {"USERPROFILE", r.home}, {"TMPDIR", r.tmp}, {"TEMP", r.tmp}, {"TMP", r.tmp},
		{"GIT_CONFIG_NOSYSTEM", "1"}, {"GIT_CONFIG_GLOBAL", filepath.Join(r.home, "empty.gitconfig")}, {"LC_ALL", "C.UTF-8"},
		{"LANGUAGE", ""}, {"GIT_DEFAULT_HASH", "sha1"}, {"FAULTLINE_TEST_SEED", seed}, {"FAULTLINE_NO_BROWSER", "1"},
		{"FAULTLINE_ARGV_LOG", r.argvLog}, {"PATH", strings.Join(path, string(os.PathListSeparator))},
	} {
		set(kv[0], kv[1])
	}
	for _, kv := range tpl.Env {
		set(kv[0], substituteRoots(kv[1], r))
	}
	out := make([]string, len(order))
	for i, k := range order {
		out[i] = names[k] + "=" + vars[k]
	}
	return out
}

// substituteRoots replaces <WS>, <TMP> and <HOME> in argv and env values.
func substituteRoots(s string, r writerRoots) string {
	return strings.NewReplacer("<WS>", r.ws, "<TMP>", r.tmp, "<HOME>", r.home).Replace(s)
}

// treeEntries describes every path under root the way treeDigest walks it
// (difftest/gen/gen-writers.ts treeEntries): entries but symlinks end in
// their octal permission bits, except on Windows, which has none to compare.
func treeEntries(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	var walk func(rel string)
	walk = func(rel string) {
		names, err := nodefs.ReadDirNames(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("listing %s: %v", rel, err)
		}
		for _, name := range names {
			r := name
			if rel != "" {
				r = rel + "/" + name
			}
			path := filepath.Join(root, filepath.FromSlash(r))
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			mode := ""
			if !windows {
				mode = " " + strconv.FormatUint(uint64(info.Mode().Perm()), 8)
			}
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					t.Fatal(err)
				}
				out[r] = "symlink:" + filepath.ToSlash(target)
			case info.IsDir():
				out[r] = "dir" + mode
				walk(r)
			case !info.Mode().IsRegular():
				out[r] = "special" + mode
			default:
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				out[r] = canonical.SHA256HexBytes(b) + mode
			}
		}
	}
	walk("")
	return out
}

// modesDigest hashes "<path> <mode>" for every entry but symlinks
// (gen-writers.ts modesDigest); "" on Windows, where it is not compared.
func modesDigest(entries map[string]string) string {
	if windows {
		return ""
	}
	var lines []string
	for _, path := range slices.Sorted(maps.Keys(entries)) {
		if e := entries[path]; !strings.HasPrefix(e, "symlink:") {
			lines = append(lines, path+" "+e[strings.LastIndex(e, " ")+1:])
		}
	}
	return canonical.SHA256Hex(strings.Join(lines, "\n"))
}

// withoutMode drops the permission bits from a golden entry, for Windows.
func withoutMode(entry string) string {
	if strings.HasPrefix(entry, "symlink:") || entry == "deleted" {
		return entry
	}
	if i := strings.LastIndex(entry, " "); i >= 0 {
		return entry[:i]
	}
	return entry
}

// changedFiles lists what one invocation changed: the new entry, or "deleted".
func changedFiles(before, after map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range after {
		if before[k] != v {
			out[k] = v
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			out[k] = "deleted"
		}
	}
	return out
}

// readArgvLog splits the recorders' log into records ("git a\x1fb").
func readArgvLog(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	records := strings.Split(string(b), "\x1e")
	return records[:len(records)-1]
}

// normalizeRecord normalizes each argument of a record on its own, so a mask
// that widens to the previous boundary (<GITTMP>) stays inside its argument.
func normalizeRecord(record string, r writerRoots) string {
	args := strings.Split(record, "\x1f")
	for i, a := range args {
		args[i] = normalizeWriter(a, r)
	}
	return strings.Join(args, "\x1f")
}

// runWriterCase builds the case's workspace and runs its invocations in
// order, like difftest/gen/gen-writers.ts runCase.
func runWriterCase(t *testing.T, repo string, tpl writerTemplate) []writerResult {
	r := makeWriterRoots(t)
	copyScenario(t, repo, tpl.Scenario, r.ws)
	for _, m := range tpl.Mutations {
		file := m.str("file")
		if pathOps[m.str("op")] {
			file = "."
			if p, ok := m.fields.Get("path"); ok {
				file = p.Str()
			}
		}
		if !applyMutation(r.ws, testCase{id: tpl.ID, base: "writer", file: file, tpl: &m}) {
			t.Fatalf("could not apply mutation %s", m.str("op"))
		}
	}
	resetModes(t, r.ws)
	var out []writerResult
	for inv, argv := range tpl.Argv {
		before := treeEntries(t, r.ws)
		os.Remove(r.argvLog)
		args := make([]string, len(argv))
		for i, a := range argv {
			args[i] = substituteRoots(a, r)
		}
		cmd := exec.Command(flBinary, args...)
		cmd.Dir = r.ws
		cmd.Env = writerEnv(repo, r, tpl.ID, tpl)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		var exitErr *exec.ExitError
		if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("running fl: %v", err)
		}
		after := treeEntries(t, r.ws)
		res := writerResult{Inv: inv, Stdout: normalizeWriter(stdout.String(), r), Stderr: normalizeWriter(stderr.String(), r),
			Exit: cmd.ProcessState.ExitCode(), Tree: treeDigest(r.ws), Modes: modesDigest(after), Files: changedFiles(before, after),
			contents: map[string][]byte{}}
		for _, record := range readArgvLog(r.argvLog) {
			res.Argv = append(res.Argv, normalizeRecord(record, r))
		}
		for rel, entry := range res.Files {
			if fileEntry.MatchString(entry) {
				res.contents[rel], _ = os.ReadFile(filepath.Join(r.ws, filepath.FromSlash(rel)))
			}
		}
		out = append(out, res)
	}
	return out
}

// compareWriter fails on the first difference from the golden line; a tree
// difference names the files the invocation wrote differently and shows
// what Go wrote.
func compareWriter(t *testing.T, key string, got writerResult, want *jsjson.Obj) {
	t.Helper()
	if got.Stdout != want.Field("stdout").Str() || got.Stderr != want.Field("stderr").Str() || float64(got.Exit) != want.Field("exit").Num() {
		t.Fatalf("%s:\n--- Go (exit %d)\n%s%s\n--- TS (exit %v)\n%s%s", key, got.Exit, got.Stdout, got.Stderr,
			want.Field("exit").Num(), want.Field("stdout").Str(), want.Field("stderr").Str())
	}
	var wantArgv []string
	for _, a := range want.Field("argv").Items() {
		wantArgv = append(wantArgv, a.Str())
	}
	if !slices.Equal(got.Argv, wantArgv) {
		t.Fatalf("%s: git/docker argv differ\n--- Go\n%s\n--- TS\n%s", key, strings.Join(got.Argv, "\n"), strings.Join(wantArgv, "\n"))
	}
	if got.Tree == want.Field("tree").Str() && (windows || got.Modes == want.Field("modes").Str()) {
		return
	}
	wantFiles := map[string]string{}
	for _, k := range want.Field("files").Obj().Keys() {
		wantFiles[k] = want.Field("files").Obj().Field(k).Str()
		if windows {
			wantFiles[k] = withoutMode(wantFiles[k])
		}
	}
	var report strings.Builder
	keys := slices.Sorted(func(yield func(string) bool) {
		seen := map[string]bool{}
		for _, m := range []map[string]string{got.Files, wantFiles} {
			for k := range m {
				if !seen[k] && !yield(k) {
					return
				}
				seen[k] = true
			}
		}
	})
	for _, k := range keys {
		if got.Files[k] == wantFiles[k] {
			continue
		}
		report.WriteString("\n" + k + ": Go " + orNone(got.Files[k]) + ", TS " + orNone(wantFiles[k]))
		if b, ok := got.contents[k]; ok {
			if len(b) > 4096 {
				b = b[:4096]
			}
			report.WriteString("\n--- Go wrote\n" + string(b))
		}
	}
	if report.Len() == 0 {
		report.WriteString("\n(this invocation changed the same files; an earlier one, or the scenario and mutations, differ)")
	}
	t.Fatalf("%s: workspace tree or permission bits differ%s", key, report.String())
}

func orNone(s string) string {
	if s == "" {
		return "unchanged"
	}
	return s
}

func TestWriterReplay(t *testing.T) {
	setWriterUmask(t)
	repo := repoRoot(t)
	templates := loadWriterTemplates(t, repo)
	goldens := loadWriterGoldens(t, repo)
	checkScenarioDirs(t, repo)
	seen := map[string]bool{}
	for _, tpl := range templates {
		// Marked before running, so a case that stops early or skips does
		// not report its later goldens as orphans.
		for inv := range tpl.Argv {
			key := tpl.ID + "|" + itoa(int64(inv))
			seen[key] = true
			// Windows runs no sh recorder, so a case that records git or
			// docker argv must not run there.
			if want := goldens[key]; want != nil && len(want.Field("argv").Items()) > 0 && !tpl.PosixOnly {
				t.Errorf("%s records git/docker argv but is not posixOnly", key)
			}
		}
		t.Run(writerSubtest(tpl), func(t *testing.T) {
			if tpl.PosixOnly && windows {
				t.Skip("POSIX fake git/docker recorders")
			}
			for _, got := range runWriterCase(t, repo, tpl) {
				key := tpl.ID + "|" + itoa(int64(got.Inv))
				want, ok := goldens[key]
				if !ok {
					t.Fatalf("no golden for %s", key)
				}
				compareWriter(t, key, got, want) // stdout, stderr, exit, argv, tree and modes (naming changed files)
			}
		})
	}
	for key := range goldens {
		if !seen[key] {
			t.Errorf("golden %s has no template", key)
		}
	}
	t.Run("posix/recorder-fake-docker", func(t *testing.T) {
		if windows {
			t.Skip("POSIX fake git/docker recorders")
		}
		testRecorderFakeDocker(t, repo)
	})
}

// writerSubtest names a case's subtest: posixOnly ones are under posix/, the
// only writer subtests the Windows job may see skipped.
func writerSubtest(tpl writerTemplate) string {
	if tpl.PosixOnly {
		return "posix/" + tpl.ID
	}
	return tpl.ID
}

// testRecorderFakeDocker drives `docker run` through the recorder into the
// pass-fail fake Docker, which no ported command reaches yet: the record,
// and the fixture's output for a bad and a good state (difftest/gen/fixtures.ts
// deterministicDockerRunner).
func testRecorderFakeDocker(t *testing.T, repo string) {
	r := makeWriterRoots(t)
	tpl := writerTemplate{ID: "recorder-fake-docker", FakeDocker: "pass-fail"}
	result := func(outcome string) string {
		return `FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"` + outcome + `"}`
	}
	for _, tc := range []struct {
		state, stdout, stderr string
		exit                  int
	}{
		{"bad", "state=bad\n" + result("PREDICATE_FAIL") + "\n", "witness failed", 1},
		{"good", "state=good\n" + result("PREDICATE_PASS") + "\n", "", 0},
	} {
		if err := os.WriteFile(filepath.Join(r.ws, "state.txt"), []byte(tc.state+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Remove(r.argvLog)
		args := []string{"run", "--rm", "--mount", "type=bind,src=" + r.ws + ",dst=/workspace/source,readonly", "img", "-lc", "a b\nc"}
		env := writerEnv(repo, r, tpl.ID, tpl)
		var docker string
		for _, kv := range env {
			if list, ok := strings.CutPrefix(kv, "PATH="); ok {
				// Looked up on the case's PATH, as fl's spawn would.
				var err error
				if docker, err = lookPathIn("docker", list); err != nil {
					t.Fatal(err)
				}
			}
		}
		cmd := exec.Command(docker, args...)
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		var exitErr *exec.ExitError
		if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
		if stdout.String() != tc.stdout || stderr.String() != tc.stderr || cmd.ProcessState.ExitCode() != tc.exit {
			t.Errorf("state %s: exit %d\nstdout %q\nstderr %q", tc.state, cmd.ProcessState.ExitCode(), stdout.String(), stderr.String())
		}
		wantLog := "docker " + strings.Join(args, "\x1f") + "\x1e"
		if b, _ := os.ReadFile(r.argvLog); string(b) != wantLog {
			t.Errorf("state %s: recorder log %q, want %q", tc.state, b, wantLog)
		}
		if got := readArgvLog(r.argvLog); len(got) != 1 || normalizeRecord(got[0], r) != "docker run\x1f--rm\x1f--mount\x1ftype=bind,src=<WS>,dst=/workspace/source,readonly\x1fimg\x1f-lc\x1fa b\nc" {
			t.Errorf("state %s: normalized record %q", tc.state, got)
		}
	}
}

// lookPathIn finds an executable file named name in the PATH list.
func lookPathIn(name, list string) (string, error) {
	for _, dir := range filepath.SplitList(list) {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New(name + " not found on PATH")
}
