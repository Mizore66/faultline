//go:build faultline_test

package difftest

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"flag"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// writerGoldenFile is the writer replay's golden in difftest/testdata/golden
// (difftest/gen/gen-writers.ts writes it). The templates of
// difftest/testdata/writers.d/<name>.json have theirs in
// golden/writers.d/<name>.jsonl (writerGoldenDir), one pair per task.
const (
	writerGoldenFile = "writers.jsonl"
	writerGoldenDir  = "writers.d"
)

var windows = runtime.GOOS == "windows"

func repoRoot(t *testing.T) string { return oracle.RepoRoot(t) }

// writerTemplate is one entry of difftest/testdata/writers.json (or of a
// writers.d/*.json file): argv runs in order in one workspace built from
// Scenario and then Mutations.
type writerTemplate struct {
	ID, Scenario, FakeDocker, FakeTools string
	// PathTools, when set, replaces the inherited PATH with links to
	// these tools only, and drops the recorder docker (linkPathTools).
	PathTools []string
	Argv      [][]string
	Env       [][2]string
	PosixOnly bool
	Mutations []template
	// Input is each invocation's stdin (template keys input and inputHex);
	// an invocation past its end, or a null entry, gets an empty, closed
	// stdin.
	Input []writerInput
	// AdvanceClock runs invocation n >= 1 under seed <id>/<n> with the clock
	// n hours later (FAULTLINE_TEST_CLOCK_OFFSET_MS), so each fl process
	// writes times after the previous one's.
	AdvanceClock bool
	// File is the template file the entry came from, relative to
	// difftest/testdata ("writers.json", "writers.d/4a-harness.json").
	File string
}

// writerInput is one invocation's stdin: text (input, where <WS>, <TMP>,
// <HOME> and <REPEAT:c:n> are expanded when the case runs) or raw bytes
// (inputHex); neither set is an empty stdin.
type writerInput struct {
	text  *string
	bytes []byte
}

// stdin is the invocation's stdin bytes (gen-writers.ts invocationInput):
// text is substituted, its repeats expanded, then encoded as UTF-8 the way
// Buffer.from(text, "utf8") does.
func (in writerInput) stdin(t testing.TB, r writerRoots) []byte {
	if in.text == nil {
		return in.bytes
	}
	text, ok := expandRepeats(substituteRoots(*in.text, r))
	if !ok {
		t.Fatalf("input %q: a <REPEAT> count is over %d", *in.text, maxRepeat)
	}
	return []byte(jsstr.ToUTF8(text))
}

// mutationInv is the invocation a mutation is applied just before (its inv
// key), or -1 for one applied at case setup.
func mutationInv(m template) int {
	if v, ok := m.fields.Get("inv"); ok {
		return int(v.Num())
	}
	return -1
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
	// tmpLeft is what the invocation left in the case's TMPDIR, right after
	// it exited (TestWriterReplay requires it empty where checksTmpdir).
	tmpLeft []string
}

var (
	writerTemplateKeys = []string{"id", "scenario", "argv", "fakeDocker", "fakeTools", "pathTools", "env", "posixOnly", "mutations", "input", "inputHex", "advanceClock"}
	writerIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// fileEntry is a regular file's tree entry: its SHA-256, then its mode
	// except on Windows.
	fileEntry = regexp.MustCompile(`^[0-9a-f]{64}(?: [0-7]{1,4})?$`)
)

// writerTemplateFiles are the template files, relative to difftest/testdata:
// writers.json, then each writers.d/*.json in byte order (gen-writers.ts
// templateFiles).
func writerTemplateFiles(t testing.TB, repo string) []string {
	files := []string{"writers.json"}
	names, err := nodefs.ReadDirNames(filepath.Join(repo, "difftest", "testdata", writerGoldenDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".json") {
			files = append(files, writerGoldenDir+"/"+name)
		}
	}
	return files
}

// writerGoldenPath is the golden file, relative to difftest/testdata/golden,
// that holds the lines of the templates in template file file:
// writers.json's in writers.jsonl, writers.d/<name>.json's in
// writers.d/<name>.jsonl (gen-writers.ts goldenFileFor).
func writerGoldenPath(file string) string {
	if file == "writers.json" {
		return writerGoldenFile
	}
	return strings.TrimSuffix(file, ".json") + ".jsonl"
}

func loadWriterTemplates(t testing.TB, repo string) []writerTemplate {
	var out []writerTemplate
	ids := map[string]bool{}
	for _, file := range writerTemplateFiles(t, repo) {
		out = append(out, loadWriterTemplateFile(t, repo, file, ids)...)
	}
	return out
}

// loadWriterTemplateFile loads one template file; ids are the ids seen so
// far, so an id repeated across files fails too.
func loadWriterTemplateFile(t testing.TB, repo, file string, ids map[string]bool) []writerTemplate {
	text, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(text))
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	var out []writerTemplate
	for _, item := range v.Items() {
		o := item.Obj()
		for _, k := range o.Keys() {
			if !slices.Contains(writerTemplateKeys, k) {
				t.Fatalf("writer template %s: unknown key %s", o.Field("id").Str(), k)
			}
		}
		tpl := writerTemplate{ID: o.Field("id").Str(), Scenario: o.Field("scenario").Str(), FakeDocker: o.Field("fakeDocker").Str(),
			FakeTools: o.Field("fakeTools").Str(), PosixOnly: o.Field("posixOnly").Bool(), AdvanceClock: o.Field("advanceClock").Bool(), File: file}
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
		for _, name := range o.Field("pathTools").Items() {
			tpl.PathTools = append(tpl.PathTools, name.Str())
		}
		if env, ok := o.Get("env"); ok {
			for _, k := range env.Obj().Keys() {
				tpl.Env = append(tpl.Env, [2]string{k, env.Obj().Field(k).Str()})
			}
		}
		for _, m := range o.Field("mutations").Items() {
			tpl.Mutations = append(tpl.Mutations, template{m.Obj()})
			if inv, ok := m.Obj().Get("inv"); ok {
				if n := inv.Num(); inv.Kind() != jsjson.Number || n != float64(int(n)) || n < 0 || int(n) >= len(tpl.Argv) {
					t.Fatalf("writer template %s: mutation inv %s is not one of its invocations", tpl.ID, jsjson.Stringify(inv))
				}
			}
		}
		tpl.Input = loadWriterInputs(t, tpl.ID, o)
		out = append(out, tpl)
	}
	return out
}

// loadWriterInputs reads a template's input (strings) or inputHex (hex
// bytes) list, one entry per invocation, null for an empty stdin
// (gen-writers.ts checkTemplates refuses the same).
func loadWriterInputs(t testing.TB, id string, o *jsjson.Obj) []writerInput {
	text, hasText := o.Get("input")
	hexes, hasHex := o.Get("inputHex")
	if hasText && hasHex {
		t.Fatalf("writer template %s has both input and inputHex", id)
	}
	list := text
	if hasHex {
		list = hexes
	}
	if !hasText && !hasHex {
		return nil
	}
	if list.Kind() != jsjson.Array {
		t.Fatalf("writer template %s: input is not an array", id)
	}
	var out []writerInput
	for _, item := range list.Items() {
		switch {
		case item.Kind() == jsjson.Null:
			out = append(out, writerInput{})
		case item.Kind() != jsjson.String:
			t.Fatalf("writer template %s: an input entry is neither a string nor null", id)
		case hasHex:
			b, err := hex.DecodeString(item.Str())
			if err != nil {
				t.Fatalf("writer template %s: inputHex %q: %v", id, item.Str(), err)
			}
			out = append(out, writerInput{bytes: b})
		default:
			s := item.Str()
			out = append(out, writerInput{text: &s})
		}
	}
	return out
}

// loadWriterGoldens reads golden/writers.jsonl and every
// golden/writers.d/*.jsonl; a key in two files fails.
func loadWriterGoldens(t *testing.T, repo string) map[string]*jsjson.Obj {
	out := map[string]*jsjson.Obj{}
	files := []string{writerGoldenFile}
	names, err := nodefs.ReadDirNames(filepath.Join(repo, "difftest", "testdata", "golden", writerGoldenDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".jsonl") {
			files = append(files, writerGoldenDir+"/"+name)
		}
	}
	for _, file := range files {
		loadWriterGoldenFile(t, repo, file, out)
	}
	return out
}

func loadWriterGoldenFile(t *testing.T, repo, file string, out map[string]*jsjson.Obj) {
	f, err := os.Open(filepath.Join(repo, "difftest", "testdata", "golden", filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<26)
	for scanner.Scan() {
		v, err := jsjson.Parse(scanner.Text())
		if err != nil {
			t.Fatal(err)
		}
		if err := checkWriterGolden(v); err != "" {
			t.Fatalf("%s line %q: %s", file, scanner.Text(), err)
		}
		key := v.Get("case").Str() + "|" + itoa(int64(v.Get("inv").Num()))
		if out[key] != nil {
			t.Fatalf("%s: duplicate golden %s", file, key)
		}
		out[key] = v.Obj()
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
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
func makeWriterRoots(t testing.TB) writerRoots {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := writerRoots{root: root, ws: filepath.Join(root, "ws"), home: filepath.Join(root, "home"), tmp: filepath.Join(root, "tmp"),
		argvLog: filepath.Join(root, "argv.log"), recorder: filepath.Join(root, "recorder")}
	for _, dir := range []string{r.ws, r.home, r.tmp, r.recorder} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.home, "empty.gitconfig"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writeRecorders(t, oracle.RepoRoot(t), r)
	return r
}

// writeRecorders copies the recorder templates (difftest/testdata/recorder)
// into the case's own recorder directory with the argv log path written into
// the script: hardened git runs with PATH as its only environment variable,
// so a recorder that read FAULTLINE_ARGV_LOG would miss every git call fl
// hardens (scenarios.ts writeRecorders).
func writeRecorders(t testing.TB, repo string, r writerRoots) {
	t.Helper()
	const line = "log=$FAULTLINE_ARGV_LOG\n"
	for _, name := range []string{"git", "docker"} {
		template, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", "recorder", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(template), line) || strings.Contains(r.argvLog, "'") {
			t.Fatalf("recorder template %s cannot take %s", name, r.argvLog)
		}
		script := strings.Replace(string(template), line, "log='"+r.argvLog+"'\n", 1)
		path := filepath.Join(r.recorder, name)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
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
	// The recorders, the fake toolchain (difftest/testdata/faketools), the
	// fake Docker, then the inherited PATH or only the template's
	// pathTools (scenarios.ts writerEnv).
	path := []string{r.recorder}
	if tpl.FakeTools != "" {
		path = append(path, filepath.Join(repo, "difftest", "testdata", "faketools", tpl.FakeTools))
	}
	if tpl.FakeDocker != "" {
		path = append(path, filepath.Join(repo, "difftest", "testdata", "fakedocker", tpl.FakeDocker))
	}
	if tpl.PathTools != nil {
		path = append(path, linkPathTools(r, tpl.PathTools))
	} else {
		path = append(path, os.Getenv("PATH"))
	}
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

// linkPathTools gives a case whose template lists pathTools a PATH with no
// docker on it (difftest/testdata/faketools/README.md): <case>/bin holds a
// symlink to the first executable of each listed name on the inherited
// PATH, and the case's recorder docker is removed unless docker is listed.
// It returns the bin directory (scenarios.ts linkPathTools). A missing
// tool panics: writerEnv has no testing.TB.
func linkPathTools(r writerRoots, tools []string) string {
	bin := filepath.Join(r.root, "bin")
	if _, err := os.Lstat(bin); err == nil {
		return bin
	}
	if err := os.Mkdir(bin, 0o755); err != nil {
		panic(err)
	}
	for _, name := range tools {
		found, err := lookPathIn(name, os.Getenv("PATH"))
		if err != nil {
			panic("pathTools: " + err.Error())
		}
		if err := os.Symlink(found, filepath.Join(bin, name)); err != nil {
			panic(err)
		}
	}
	if !slices.Contains(tools, "docker") {
		if err := os.Remove(filepath.Join(r.recorder, "docker")); err != nil {
			panic(err)
		}
	}
	return bin
}

// tmpdirFakeDockers are the fake Dockers that keep state in $TMPDIR on
// purpose (difftest/testdata/fakedocker/README.md).
var tmpdirFakeDockers = []string{"timeout-once", "tmpdir-swap"}

// checksTmpdir says whether a case's TMPDIR (<TMP>) must be empty after
// each invocation (spec 3.3, "same files left behind"): every case but
// those whose fake Docker or env puts files there itself (gen-writers.ts
// checksTmpdir).
func checksTmpdir(tpl writerTemplate) bool {
	if slices.Contains(tmpdirFakeDockers, tpl.FakeDocker) {
		return false
	}
	for _, kv := range tpl.Env {
		if strings.Contains(kv[1], "<TMP>") {
			return false
		}
	}
	return true
}

// tmpLeftovers lists what an invocation left in the case's TMPDIR, sorted
// (gen-writers.ts tmpLeftovers).
func tmpLeftovers(r writerRoots) []string {
	names, err := nodefs.ReadDirNames(r.tmp)
	if err != nil {
		return nil
	}
	return names
}

// substituteDeep applies substituteRoots to every string inside a mutation
// (gen-writers.ts substituteDeep).
func substituteDeep(v jsjson.Value, r writerRoots) jsjson.Value {
	switch v.Kind() {
	case jsjson.String:
		return jsjson.MakeString(substituteRoots(v.Str(), r))
	case jsjson.Array:
		items := make([]jsjson.Value, len(v.Items()))
		for i, item := range v.Items() {
			items[i] = substituteDeep(item, r)
		}
		return jsjson.MakeArray(items)
	case jsjson.Object:
		o := jsjson.NewObj()
		for _, k := range v.Obj().Keys() {
			o.Set(k, substituteDeep(v.Obj().Field(k), r))
		}
		return jsjson.MakeObject(o)
	}
	return v
}

// substituteRoots replaces <WS>, <TMP> and <HOME> in argv and env values.
func substituteRoots(s string, r writerRoots) string {
	return strings.NewReplacer("<WS>", r.ws, "<TMP>", r.tmp, "<HOME>", r.home).Replace(s)
}

// treeEntries describes every path under root the way treeDigest walks it
// (difftest/gen/gen-writers.ts treeEntries): entries but symlinks end in
// their octal permission bits, except on Windows, which has none to compare.
func treeEntries(t *testing.T, root string, fileDigest func(rel string, b []byte) string) map[string]string {
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
				out[r] = fileDigest(r, b) + mode
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

// relocated is gen-writers.ts relocation: see there.
type relocated struct {
	roots  writerRoots
	names  map[string]string // digest hex -> provenance name
	sorted []string
}

var selfDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// pointerToken escapes a JSON pointer reference token.
func pointerToken(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// loggedCasePaths are the paths under the case root the recorders logged
// (every `git -C <worktree>` and `--mount src=<worktree>` argument), in log
// order. gen-writers.ts has the twin.
func loggedCasePaths(records []string, r writerRoots) []string {
	var out []string
	seen := map[string]bool{}
	consider := func(s string) {
		if strings.HasPrefix(s, r.root) && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, record := range records {
		for _, arg := range strings.Split(record, "\x1f") {
			consider(arg)
			for _, part := range strings.Split(arg, ",") {
				if i := strings.Index(part, "="); i >= 0 {
					consider(part[i+1:])
				}
			}
		}
	}
	return out
}

// worktreeDigestMember is a minimization run fact's worktreeDigest member,
// in canonical or indented JSON (gen-writers.ts WORKTREE_DIGEST).
var worktreeDigestMember = regexp.MustCompile(`"worktreeDigest":\s*"sha256:([0-9a-f]{64})"`)

// relocation ports gen-writers.ts relocation: the digests in the tree that
// depend on the case paths, each named by where it was first derived.
func relocation(t *testing.T, r writerRoots, records []string) *relocated {
	t.Helper()
	type file struct {
		rel   string
		bytes []byte
		text  string
	}
	var files []file
	var walk func(rel string)
	walk = func(rel string) {
		names, err := nodefs.ReadDirNames(filepath.Join(r.ws, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("listing %s: %v", rel, err)
		}
		for _, name := range names {
			rr := name
			if rel != "" {
				rr = rel + "/" + name
			}
			path := filepath.Join(r.ws, filepath.FromSlash(rr))
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case info.IsDir() && name == ".git":
				// Only the sidecar recordings of a .git are walked: they hold
				// the case path (gen-writers.ts relocation).
				if isRealDir(filepath.Join(path, "faultline")) && isRealDir(filepath.Join(path, "faultline", "recordings")) {
					walk(rr + "/faultline/recordings")
				}
			case info.IsDir():
				walk(rr)
			case info.Mode().IsRegular():
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				files = append(files, file{rr, b, jsstr.DecodeUTF8(b)})
			}
		}
	}
	if _, err := os.Stat(r.ws); err == nil {
		walk("")
	}
	rel := &relocated{roots: r, names: map[string]string{}}
	var found []string
	add := func(hex, name string) {
		if _, ok := rel.names[hex]; !ok {
			rel.names[hex] = name
			found = append(found, hex)
		}
	}
	tainted := func(text string) bool {
		if strings.Contains(text, r.root) {
			return true
		}
		for d := range rel.names {
			if strings.Contains(text, d) {
				return true
			}
		}
		return false
	}
	digestOf := func(v jsjson.Value) string {
		d, err := canonical.DigestJSON(v)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	var visit func(v jsjson.Value, path, pointer string)
	visit = func(v jsjson.Value, path, pointer string) {
		if v.Kind() != jsjson.Object && v.Kind() != jsjson.Array {
			return
		}
		text, err := canonical.CanonicalJSON(v)
		if err != nil {
			t.Fatal(err)
		}
		if !tainted(text) {
			return
		}
		add(digestOf(v)[7:], "json:"+path+"#"+pointer)
		if v.Kind() == jsjson.Array {
			for i, item := range v.Items() {
				visit(item, path, pointer+"/"+strconv.Itoa(i))
			}
			return
		}
		o := v.Obj()
		for _, key := range o.Keys() {
			member := o.Field(key)
			if member.Kind() != jsjson.String || !selfDigest.MatchString(member.Str()) {
				continue
			}
			rest := jsjson.NewObj()
			for _, k := range o.Keys() {
				if k != key {
					rest.Set(k, o.Field(k))
				}
			}
			if digestOf(jsjson.MakeObject(rest)) == member.Str() {
				add(member.Str()[7:], "self:"+path+"#"+pointer+"/"+pointerToken(key))
			}
		}
		for _, key := range o.Keys() {
			visit(o.Field(key), path, pointer+"/"+pointerToken(key))
		}
	}
	// A minimization run fact hashes its materialized worktree path, which
	// appears in no file: name those digests after the masked path.
	seeds := []string{r.root}
	for _, path := range loggedCasePaths(records, r) {
		digest := canonical.SHA256Hex(path)
		add(digest, "path:"+normalizeWriter(path, r))
		seeds = append(seeds, digest)
	}
	// A worktree no recorder logged (a fault before git ran in it) is still
	// hashed into its run fact: name each worktreeDigest value not named
	// above after where it appears (round 7 #15).
	for _, f := range files {
		for k, m := range worktreeDigestMember.FindAllStringSubmatch(f.text, -1) {
			if _, ok := rel.names[m[1]]; !ok {
				add(m[1], "worktree:"+f.rel+"#"+strconv.Itoa(k))
				seeds = append(seeds, m[1])
			}
		}
	}
	for frontier := seeds; len(frontier) > 0; {
		found = nil
		for _, f := range files {
			if !slices.ContainsFunc(frontier, func(needle string) bool { return strings.Contains(f.text, needle) }) {
				continue
			}
			add(canonical.SHA256HexBytes(f.bytes), "file:"+f.rel)
			if v, err := jsjson.Parse(f.text); err == nil {
				visit(v, f.rel, "")
			}
		}
		frontier = found
	}
	rel.sorted = slices.Sorted(maps.Keys(rel.names))
	return rel
}

// isRealDir is lstat(path).isDirectory().
func isRealDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// text masks the case paths, then every path-dependent digest by its name
// (gen-writers.ts relocation).
func (rel *relocated) text(s string) string {
	out := normalizeWriter(s, rel.roots)
	for _, d := range rel.sorted {
		out = strings.ReplaceAll(out, d, "<PATHDIGEST:"+rel.names[d]+">")
	}
	// The proof page also prints a digest abbreviated (git-proof-view.ts
	// shortDigest: its first and last 8 hex digits).
	for _, d := range rel.sorted {
		out = strings.ReplaceAll(out, d[:8]+"..."+d[len(d)-8:], "<PATHDIGEST:"+rel.names[d]+":short>")
	}
	return out
}

// fileDigest hashes a Git index by what it records (gitIndexDigest), a
// path-dependent file's masked text, and any other file's bytes.
func (rel *relocated) fileDigest(path string, b []byte) string {
	if isGitIndex(path) {
		return gitIndexDigest(filepath.Dir(filepath.Join(rel.roots.ws, filepath.FromSlash(path))), b)
	}
	if d, ok := snapshotObjectDigest(path); ok {
		return d
	}
	d := canonical.SHA256HexBytes(b)
	if _, ok := rel.names[d]; !ok {
		return d
	}
	return canonical.SHA256Hex(rel.text(jsstr.DecodeUTF8(b)))
}

// crossVerify is the spec 2.4 cross-check of a bundle Go wrote: Go `fl
// verify --expect-root <printed root>` must report it valid, and so must
// frozen TS when FAULTLINE_NODE_ORACLE=1.
// tsCrossVerified counts the bundles Go wrote that frozen TS verified.
var tsCrossVerified atomic.Int64

func crossVerify(t *testing.T, repo string, inv int, id, bundle, root string) {
	t.Helper()
	ok := func(out string, code int) bool {
		return code == 0 && strings.HasPrefix(out, "Integrity: VALID\n") && strings.Contains(out, "\nExternal root: MATCH\n")
	}
	stdout, stderr, code := runFlOnce(t, repo, nil, "verify", bundle, "--expect-root", root)
	if !ok(stdout, code) {
		t.Fatalf("%s|%d: Go verify of the bundle Go wrote failed (exit %d)\n%s%s", id, inv, code, stdout, stderr)
	}
	if os.Getenv("FAULTLINE_NODE_ORACLE") == "1" {
		if out, code := runTSVerify(t, repo, bundle, root); !ok(out, code) {
			t.Fatalf("%s|%d: TS verify of the bundle Go wrote failed (exit %d)\n%s", id, inv, code, out)
		}
		tsCrossVerified.Add(1)
	}
}

// runTSVerify runs frozen TS `verify <dir> --expect-root <root>`, giving
// its stdout then stderr and its exit code.
func runTSVerify(t *testing.T, repo, dir, root string) (string, int) {
	t.Helper()
	cmd := exec.Command("node", filepath.Join(repo, "dist", "cli.js"), "verify", dir, "--expect-root", root)
	cmd.Dir = filepath.Dir(dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("running node: %v", err)
	}
	return stdout.String() + stderr.String(), cmd.ProcessState.ExitCode()
}

// jsonField is the string at path in a JSON text, "" when absent.
func jsonField(text string, path ...string) string {
	v, err := jsjson.Parse(text)
	if err != nil {
		return ""
	}
	if f := v.Get(path...); f.Kind() == jsjson.String {
		return f.Str()
	}
	return ""
}

// isGitIndex is gen-writers.ts isGitIndex: .git/index at any depth, and a
// linked worktree's index (.git/worktrees/<name>/index).
func isGitIndex(rel string) bool {
	return rel == ".git/index" || strings.HasSuffix(rel, "/.git/index") || worktreeIndex.MatchString(rel)
}

var worktreeIndex = regexp.MustCompile(`(?:^|/)\.git/worktrees/[^/]+/index$`)

// gitIndexDigest ports gen-writers.ts gitIndexDigest: a Git index holds
// stat data (times, inode, device) that differs between the TS and the Go
// copy of a scenario, and git rewrites it on a plain `git status`, so it is
// compared by the SHA-256 of `git ls-files --stage -z` read from it (the
// raw bytes' SHA-256 when git cannot read it).
func gitIndexDigest(gitDir string, b []byte) string {
	cmd := exec.Command("git", "-c", "core.fsmonitor=false", "--git-dir="+gitDir, "ls-files", "--stage", "-z")
	cmd.Dir = gitDir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	listing, err := cmd.Output()
	if err != nil {
		return canonical.SHA256HexBytes(b)
	}
	return canonical.SHA256HexBytes(append([]byte("git-index:"), listing...))
}

// snapshotObjectDigest is gen-writers.ts snapshotObjectDigest: a loose
// object in the turn-snapshot store (.git/faultline/objects/xx/…) is
// zlib-compressed by git, so its bytes depend on the zlib build git links;
// it is compared by its name, the id of the object it holds.
func snapshotObjectDigest(rel string) (string, bool) {
	m := snapshotObject.FindStringSubmatch(rel)
	if m == nil {
		return "", false
	}
	return canonical.SHA256Hex("git-object:" + m[1] + m[2]), true
}

var snapshotObject = regexp.MustCompile(`(?:^|/)\.git/faultline/objects/([0-9a-f]{2})/([0-9a-f]{38}|[0-9a-f]{62})$`)

// isVersionRecord is gen-writers.ts isVersionRecord: a record whose last
// argument is --version.
func isVersionRecord(record string) bool {
	args := strings.Split(record[strings.Index(record, " ")+1:], "\x1f")
	return args[len(args)-1] == "--version"
}

// sortVersionRecordRuns is gen-writers.ts sortVersionRecordRuns: doctor
// starts git, node and docker --version together (Promise.all), so the
// order the recorders append them in is racy on both sides, and `fl init
// --yes --cli` runs doctor twice; each run of consecutive --version records
// is sorted by code unit, and nothing else is reordered.
func sortVersionRecordRuns(records []string) []string {
	out := slices.Clone(records)
	for start := 0; start < len(out); {
		end := start
		for end < len(out) && isVersionRecord(out[end]) {
			end++
		}
		slices.SortFunc(out[start:end], jsstr.CompareUTF16)
		start = end + 1
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
func normalizeRecord(record string, r writerRoots, reloc *relocated) string {
	args := strings.Split(record, "\x1f")
	for i, a := range args {
		if reloc != nil {
			args[i] = reloc.text(a)
		} else {
			args[i] = normalizeWriter(a, r)
		}
	}
	return strings.Join(args, "\x1f")
}

// runWriterCase builds the case's workspace and runs its invocations in
// order, like difftest/gen/gen-writers.ts runCase.
func runWriterCase(t *testing.T, repo string, tpl writerTemplate) []writerResult {
	return runWriterCaseWith(t, repo, tpl, writerRun{})
}

// writerRun changes how runWriterCaseWith runs an invocation; the zero
// value runs the Go fl and cross-verifies every bundle it writes.
type writerRun struct {
	// command turns fl's arguments into the command line to run (nil: flBinary).
	command func(r writerRoots, inv int, args []string) []string
	// mask rewrites an invocation's stdout and stderr before normalization,
	// and may rename what the invocation left in the workspace.
	mask func(t *testing.T, r writerRoots, inv int, stdout, stderr string) (string, string)
	// noVerify skips crossVerify.
	noVerify bool
}

// runWriterCaseWith is runWriterCase with run's command, masks and checks.
func runWriterCaseWith(t *testing.T, repo string, tpl writerTemplate, run writerRun) []writerResult {
	r := makeWriterRoots(t)
	busyPort := holdBusyPort(t, tpl)
	prepareWriterCase(t, repo, tpl, r)
	var out []writerResult
	// The recorded paths accumulate over the case's invocations: a later
	// invocation must keep masking the worktree digests an earlier one
	// wrote into a file (gen-writers.ts runCase).
	var records, stdouts []string
	for inv, argv := range tpl.Argv {
		// This invocation's mutations: the others, then the new entries'
		// modes reset, then the chmods (gen-writers.ts runCase).
		if slices.ContainsFunc(tpl.Mutations, func(m template) bool { return mutationInv(m) == inv }) {
			existing := pathsUnder(t, r.ws)
			applyWriterMutations(t, tpl, r, func(m template) bool { return mutationInv(m) == inv && m.str("op") != "chmod" })
			resetNewModes(t, r.ws, existing)
			applyWriterMutations(t, tpl, r, func(m template) bool { return mutationInv(m) == inv && m.str("op") == "chmod" })
		}
		before := treeEntries(t, r.ws, relocation(t, r, records).fileDigest)
		os.Remove(r.argvLog)
		args := make([]string, len(argv))
		for i, a := range argv {
			args[i] = stdoutArgument(t, fileArgument(t, strings.ReplaceAll(substituteRoots(a, r), busyPortMarker, busyPort), r), stdouts)
		}
		command := append([]string{flBinary}, args...)
		if run.command != nil {
			command = run.command(r, inv, args)
		}
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Dir = r.ws
		cmd.Env = invocationEnv(repo, r, tpl, inv)
		// An empty or given stdin, written to a pipe that is then closed
		// (gen-writers.ts runCase; runCli ends the child's stdin the same
		// way). exec ignores EPIPE when fl exits without reading it.
		cmd.Stdin = bytes.NewReader(invocationInput(t, tpl, inv, r))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		var exitErr *exec.ExitError
		if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("running fl: %v", err)
		}
		outText, errText := stdout.String(), stderr.String()
		tmpLeft := tmpLeftovers(r)
		stdouts = append(stdouts, outText)
		plans := planDigests(outText)
		if run.mask != nil {
			outText, errText = run.mask(t, r, inv, outText, errText)
		}
		invocationRecords := readArgvLog(r.argvLog)
		records = append(records, invocationRecords...)
		reloc := relocation(t, r, records)
		after := treeEntries(t, r.ws, reloc.fileDigest)
		res := writerResult{Inv: inv, Stdout: maskPlanDigests(reloc.text(outText), plans), Stderr: maskPlanDigests(reloc.text(errText), plans),
			Exit: cmd.ProcessState.ExitCode(), Tree: treeDigestWith(r.ws, reloc.fileDigest), Modes: modesDigest(after), Files: changedFiles(before, after),
			contents: map[string][]byte{}, tmpLeft: tmpLeft}
		for _, record := range invocationRecords {
			res.Argv = append(res.Argv, normalizeRecord(record, r, reloc))
		}
		res.Argv = sortVersionRecordRuns(res.Argv)
		// After the tree and the argv log are read (gen-writers.ts runCase).
		if bundle, ok := writtenBundle(outText); ok && !run.noVerify {
			crossVerify(t, repo, inv, tpl.ID, bundle, jsonField(outText, "proofBundle", "rootDigest"))
		}
		if pkg, root, ok := writtenPrevention(outText); ok && !run.noVerify {
			crossVerify(t, repo, inv, tpl.ID, pkg, root)
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

// invocationEnv is writerEnv for invocation inv: with AdvanceClock, an
// invocation n >= 1 runs under seed <id>/<n> with the clock n hours on
// (FAULTLINE_TEST_CLOCK_OFFSET_MS); invocation 0, and every invocation of
// any other case, under seed <id> (gen-writers.ts invocationEnv).
func invocationEnv(repo string, r writerRoots, tpl writerTemplate, inv int) []string {
	if !tpl.AdvanceClock || inv == 0 {
		return writerEnv(repo, r, tpl.ID, tpl)
	}
	return append(writerEnv(repo, r, tpl.ID+"/"+strconv.Itoa(inv), tpl), "FAULTLINE_TEST_CLOCK_OFFSET_MS="+strconv.Itoa(inv*3_600_000))
}

// invocationInput is invocation inv's stdin bytes: its input entry, or none.
func invocationInput(t testing.TB, tpl writerTemplate, inv int, r writerRoots) []byte {
	if inv >= len(tpl.Input) {
		return []byte{}
	}
	if b := tpl.Input[inv].stdin(t, r); b != nil {
		return b
	}
	return []byte{}
}

// fileArgument replaces an argv string that is exactly <FILE:rel> with the
// trimmed text of the workspace file rel when the invocation starts: a
// bundle's ROOT.sha256 depends on the case directory, so a later
// invocation that must pass --expect-root reads it (gen-writers.ts
// fileArgument).
func fileArgument(t *testing.T, arg string, r writerRoots) string {
	rel, ok := strings.CutPrefix(arg, "<FILE:")
	if !ok || !strings.HasSuffix(rel, ">") || strings.Contains(rel[:len(rel)-1], ">") {
		return arg
	}
	b, err := os.ReadFile(filepath.Join(r.ws, filepath.FromSlash(rel[:len(rel)-1])))
	if err != nil {
		t.Fatal(err)
	}
	return jsstr.Trim(string(b))
}

// stdoutArgument replaces an argv string that is exactly
// <STDOUT:n:/json/pointer> with the string at that JSON pointer of the
// case's invocation n's stdout (gen-writers.ts stdoutArgument): a project
// build plan's digest covers the absolute context directory, so `fl runtime
// project build --expect-plan` takes it from the plan invocation.
func stdoutArgument(t *testing.T, arg string, stdouts []string) string {
	m := stdoutArgumentPattern.FindStringSubmatch(arg)
	if m == nil {
		return arg
	}
	n, _ := strconv.Atoi(m[1])
	if n >= len(stdouts) {
		t.Fatalf("%s: no invocation %d before this one", arg, n)
	}
	v, err := jsjson.Parse(stdouts[n])
	if err != nil {
		t.Fatalf("%s: invocation %d's stdout is not JSON: %v", arg, n, err)
	}
	for _, token := range strings.Split(m[2][1:], "/") {
		v = v.Get(strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~"))
	}
	if v.Kind() != jsjson.String {
		t.Fatalf("%s: not a string in invocation %d's stdout", arg, n)
	}
	return v.Str()
}

var stdoutArgumentPattern = regexp.MustCompile(`^<STDOUT:([0-9]+):(/[^>]*)>$`)

// planDigests are the project build plan digests (src/runtime.ts:481) in an
// invocation's stdout that equal the digestJson of the plan fields printed
// beside them (every object whose kind is PROJECT_RUNTIME_IMAGE_BUILD). The
// digest covers the absolute context directory and Dockerfile, so it
// differs between case directories; maskPlanDigests replaces exactly these
// with <PLANDIGEST>, and a wrong digest stays unmasked and fails the
// comparison (gen-writers.ts planDigests).
func planDigests(stdout string) []string {
	parsed, err := jsjson.Parse(stdout)
	if err != nil {
		return nil
	}
	var out []string
	var visit func(v jsjson.Value)
	visit = func(v jsjson.Value) {
		switch v.Kind() {
		case jsjson.Array:
			for _, item := range v.Items() {
				visit(item)
			}
			return
		case jsjson.Object:
		default:
			return
		}
		review := v.Get("review")
		if v.Get("kind").Kind() == jsjson.String && v.Get("kind").Str() == "PROJECT_RUNTIME_IMAGE_BUILD" &&
			review.Kind() == jsjson.Object && review.Get("planDigest").Kind() == jsjson.String {
			o := jsjson.NewObj()
			o.Set("schema", jsjson.MakeString("faultline.project-build-plan.v1"))
			for _, k := range []string{"contextDirectory", "dockerfile", "imageTag", "network"} {
				if f, ok := v.Obj().Get(k); ok {
					o.Set(k, f)
				}
			}
			for _, k := range []string{"contextDigest", "dockerfileDigest"} {
				if f, ok := review.Obj().Get(k); ok {
					o.Set(k, f)
				}
			}
			if d, err := canonical.DigestJSON(jsjson.MakeObject(o)); err == nil && d == review.Get("planDigest").Str() {
				out = append(out, d)
			}
		}
		for _, k := range v.Obj().Keys() {
			visit(v.Obj().Field(k))
		}
	}
	visit(parsed)
	return out
}

// maskPlanDigests replaces each of digests in text with <PLANDIGEST>.
func maskPlanDigests(text string, digests []string) string {
	for _, d := range digests {
		text = strings.ReplaceAll(text, d, "<PLANDIGEST>")
	}
	return text
}

// busyPortMarker in an argv string is the port of a listener the case holds
// on 127.0.0.1 while it runs (gen-writers.ts BUSY_PORT).
const busyPortMarker = "<BUSYPORT>"

// holdPortVariable in a template's env names a fixed port the case holds on
// 127.0.0.1 while it runs (gen-writers.ts HOLD_PORT).
const holdPortVariable = "FAULTLINE_TEST_HOLD_PORT"

// holdBusyPort listens on 127.0.0.1:0 for the duration of the test when the
// template uses busyPortMarker, and returns the port ("" otherwise); the
// TS twin is holdBusyPort in gen-writers.ts.
func holdBusyPort(t *testing.T, tpl writerTemplate) string {
	// A fixed port named by the template's FAULTLINE_TEST_HOLD_PORT (a
	// command's default port, judge-proof's 4174) is held too; if another
	// process already holds it, it is busy all the same.
	for _, kv := range tpl.Env {
		if kv[0] == holdPortVariable {
			if l, err := net.Listen("tcp4", "127.0.0.1:"+kv[1]); err == nil {
				t.Cleanup(func() { l.Close() })
			}
		}
	}
	for _, argv := range tpl.Argv {
		for _, a := range argv {
			if strings.Contains(a, busyPortMarker) {
				l, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { l.Close() })
				return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
			}
		}
	}
	return ""
}

// prepareWriterCase fills the case's workspace: the scenario, then the
// template's setup mutations (those without inv, with <WS>, <TMP>, <HOME>
// substituted), then resetModes, then the setup chmod mutations, which
// resetModes would undo (gen-writers.ts runCase). A mutation with inv is
// applied just before that invocation (runWriterCaseWith) and never reset.
// A cleanup makes every directory writable again, so the case directory
// can be removed.
func prepareWriterCase(t testing.TB, repo string, tpl writerTemplate, r writerRoots) {
	t.Helper()
	copyScenario(t, repo, tpl.Scenario, r.ws)
	setup := func(chmod bool) func(template) bool {
		return func(m template) bool { return mutationInv(m) < 0 && (m.str("op") == "chmod") == chmod }
	}
	applyWriterMutations(t, tpl, r, setup(false))
	resetModes(t, r.ws)
	refreshScenarioIndexes(t, repo, tpl.Scenario, r.ws)
	// A read-only directory refuses writes only to a non-root user.
	if os.Geteuid() == 0 && slices.ContainsFunc(tpl.Mutations, func(m template) bool { return m.str("op") == "chmod" }) {
		t.Fatalf("%s: chmod cases must run as a non-root user", tpl.ID)
	}
	// Registered first, so a chmod that leaves a directory read-only and then
	// fails the case is still undone before t.TempDir's cleanup.
	t.Cleanup(func() { restoreWritable(r.root) })
	applyWriterMutations(t, tpl, r, setup(true))
}

// pathsUnder lists every entry under root, not following links
// (gen-writers.ts pathsUnder).
func pathsUnder(t testing.TB, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root {
			rel, _ := filepath.Rel(root, path)
			out[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// resetNewModes gives the entries a mutation made before an invocation the
// modes resetModes gives (0755 directories, 0644 files); entries that
// existed keep theirs (gen-writers.ts resetNewModes).
func resetNewModes(t testing.TB, root string, before map[string]bool) {
	t.Helper()
	for rel := range pathsUnder(t, root) {
		if before[rel] {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case info.IsDir():
			err = os.Chmod(path, 0o755)
		case info.Mode().IsRegular():
			err = os.Chmod(path, 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// applyWriterMutations applies, in template order, the mutations of tpl
// that pick selects (gen-writers.ts applyMutations).
func applyWriterMutations(t testing.TB, tpl writerTemplate, r writerRoots, pick func(template) bool) {
	t.Helper()
	for _, raw := range tpl.Mutations {
		if !pick(raw) {
			continue
		}
		m := template{substituteDeep(jsjson.MakeObject(raw.fields), r).Obj()}
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
}

// restoreWritable makes every directory under root writable again (a chmod
// mutation may leave one read-only); gen-writers.ts restoreWritable.
func restoreWritable(root string) {
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(path, 0o755)
		}
		return nil
	})
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
	ran, selected := 0, 0
	defer func() {
		liveSummary(t, "%d of %d writer cases run (%d golden invocations), %d Go bundles verified by TS", ran, len(templates), len(goldens), tsCrossVerified.Load())
		// A -run pattern that names no writer case (posixOnly cases sit
		// under posix/, so "TestWriterReplay/investigate-x" matches
		// nothing) would otherwise pass having compared nothing (round 11,
		// r11-3).
		if selected == 0 && runFilter() != "" {
			t.Errorf("-run %q selects none of the %d writer cases nor the recorder self-test (posixOnly cases are TestWriterReplay/posix/<id>)", runFilter(), len(templates))
		}
	}()
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
			selected++
			if tpl.PosixOnly && windows {
				t.Skip("POSIX fake git/docker recorders")
			}
			ran++
			for _, got := range runWriterCase(t, repo, tpl) {
				key := tpl.ID + "|" + itoa(int64(got.Inv))
				want, ok := goldens[key]
				if !ok {
					t.Fatalf("no golden for %s", key)
				}
				compareWriter(t, key, got, want) // stdout, stderr, exit, argv, tree and modes (naming changed files)
				// Temporary files fl makes (mkdtemp roots, the snapshot
				// index, the doctor --security fixture) must be gone, as
				// gen-writers.ts requires of TS (spec 3.3).
				if checksTmpdir(tpl) && len(got.tmpLeft) > 0 {
					t.Errorf("%s: Go left %s in TMPDIR", key, strings.Join(got.tmpLeft, ", "))
				}
			}
		})
	}
	for key := range goldens {
		if !seen[key] {
			t.Errorf("golden %s has no template", key)
		}
	}
	t.Run("posix/recorder-fake-docker", func(t *testing.T) {
		selected++
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
		if got := readArgvLog(r.argvLog); len(got) != 1 || normalizeRecord(got[0], r, nil) != "docker run\x1f--rm\x1f--mount\x1ftype=bind,src=<WS>,dst=/workspace/source,readonly\x1fimg\x1f-lc\x1fa b\nc" {
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

// pinnedImage is the digest-pinned image the investigate cases name
// (difftest/gen/scenarios.ts PINNED_IMAGE).
const pinnedImage = "registry.example/faultline-node@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// scenarioWorkspace builds a writer case workspace from a committed
// scenario, as runWriterCase does, and returns it; runFlIn runs fl there.
func scenarioWorkspace(t *testing.T, name string) string {
	t.Helper()
	setWriterUmask(t)
	r := makeWriterRoots(t)
	copyScenario(t, repoRoot(t), name, r.ws)
	resetModes(t, r.ws)
	refreshScenarioIndexes(t, repoRoot(t), name, r.ws)
	return r.ws
}

// rootsOf recovers the case directories of a workspace scenarioWorkspace made.
func rootsOf(ws string) writerRoots {
	root := filepath.Dir(ws)
	return writerRoots{root: root, ws: ws, home: filepath.Join(root, "home"), tmp: filepath.Join(root, "tmp"),
		argvLog: filepath.Join(root, "argv.log"), recorder: filepath.Join(root, "recorder")}
}

// runFlIn runs fl in a scenarioWorkspace with the writer environment (the
// pass-fail fake Docker) plus env.
func runFlIn(t *testing.T, ws string, env map[string]string, args ...string) (string, string, int) {
	t.Helper()
	tpl := writerTemplate{ID: t.Name(), FakeDocker: "pass-fail"}
	for k, v := range env {
		tpl.Env = append(tpl.Env, [2]string{k, v})
	}
	cmd := exec.Command(flBinary, args...)
	cmd.Dir = ws
	cmd.Env = writerEnv(repoRoot(t), rootsOf(ws), t.Name(), tpl)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("running fl: %v", err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// writtenBundle is the proofBundle.directory an `fl investigate git` run
// printed (gen-writers.ts writtenBundle); ok is false when it wrote none.
func writtenBundle(stdout string) (string, bool) {
	v, err := jsjson.Parse(stdout)
	if err != nil || v.Kind() != jsjson.Object {
		return "", false
	}
	if d := v.Get("proofBundle", "directory"); d.Kind() == jsjson.String {
		return d.Str(), true
	}
	return "", false
}

// writtenPrevention is the package directory and root digest an `fl
// prevention write` run printed (its JSON, then the claim line;
// gen-writers.ts writtenPrevention); ok is false when it wrote none.
func writtenPrevention(stdout string) (dir, root string, ok bool) {
	var text string
	for _, claim := range []string{"\nPrevention verified\n", "\nPrevention evidence summary\n"} {
		if strings.HasSuffix(stdout, claim) {
			text = stdout[:len(stdout)-len(claim)+1]
		}
	}
	v, err := jsjson.Parse(text)
	if text == "" || err != nil || v.Kind() != jsjson.Object {
		return "", "", false
	}
	status, d, r := v.Get("status"), v.Get("directory"), v.Get("rootDigest")
	if status.Kind() != jsjson.String || !strings.HasPrefix(status.Str(), "PREVENTION_") || d.Kind() != jsjson.String || r.Kind() != jsjson.String {
		return "", "", false
	}
	return d.Str(), r.Str(), true
}

// proofDirectory is the bundle directory a successful investigate printed.
func proofDirectory(t *testing.T, stdout string) string {
	t.Helper()
	dir, ok := writtenBundle(stdout)
	if !ok {
		t.Fatalf("no proof bundle in %s", stdout)
	}
	return dir
}

// retainedRoots are the ROOT.sha256 values of bundles first verified in
// this test binary: a later edit that rewrites the catalog and ROOT.sha256
// is still caught, since verification runs against this external root.
var retainedRoots sync.Map

func expectedRoot(t *testing.T, dir string) string {
	t.Helper()
	if root, ok := retainedRoots.Load(dir); ok {
		return root.(string)
	}
	b, err := os.ReadFile(filepath.Join(dir, "ROOT.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	root, _ := retainedRoots.LoadOrStore(dir, strings.TrimSpace(string(b)))
	return root.(string)
}

// goVerify returns a verifier running Go `fl verify <dir> --expect-root
// <retained root>`, giving its output and exit code.
func goVerify(t *testing.T) func(string) (string, int) {
	t.Helper()
	return func(dir string) (string, int) {
		stdout, stderr, code := runFlOnce(t, filepath.Dir(dir), nil, "verify", dir, "--expect-root", expectedRoot(t, dir))
		return stdout + stderr, code
	}
}

// tsVerify is goVerify with frozen TS (node dist/cli.js); it skips unless
// FAULTLINE_NODE_ORACLE=1.
func tsVerify(t *testing.T) func(string) (string, int) {
	t.Helper()
	oracle.Skip(t)
	repo := repoRoot(t)
	return func(dir string) (string, int) { return runTSVerify(t, repo, dir, expectedRoot(t, dir)) }
}

// tamperManifestField sets the dotted field of the bundle's manifest.json to
// value and rewrites hashes.txt and ROOT.sha256 to match, so only the
// external root can catch the edit.
func tamperManifestField(t *testing.T, dir, field, value string) {
	t.Helper()
	path := filepath.Join(dir, "manifest.json")
	manifest, ok := parseObjectFile(path)
	if !ok {
		t.Fatalf("unreadable %s", path)
	}
	keys := strings.Split(field, ".")
	o := manifest
	for _, k := range keys[:len(keys)-1] {
		next := o.Field(k)
		if next.Kind() != jsjson.Object {
			t.Fatalf("manifest has no object %s", k)
		}
		o = next.Obj()
	}
	o.Set(keys[len(keys)-1], jsjson.MakeString(value))
	text, err := canonical.CanonicalJSON(jsjson.MakeObject(manifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rehash(dir, "writer")
}

// asGolden is res as a writer golden line, for compareWriter.
func asGolden(res writerResult) *jsjson.Obj {
	o := jsjson.NewObj()
	o.Set("stdout", jsjson.MakeString(res.Stdout))
	o.Set("stderr", jsjson.MakeString(res.Stderr))
	o.Set("exit", jsjson.MakeNumber(float64(res.Exit)))
	o.Set("tree", jsjson.MakeString(res.Tree))
	o.Set("modes", jsjson.MakeString(res.Modes))
	files := jsjson.NewObj()
	for k, v := range res.Files {
		files.Set(k, jsjson.MakeString(v))
	}
	o.Set("files", jsjson.MakeObject(files))
	argv := make([]jsjson.Value, len(res.Argv))
	for i, a := range res.Argv {
		argv[i] = jsjson.MakeString(a)
	}
	o.Set("argv", jsjson.MakeArray(argv))
	o.Set("inv", jsjson.MakeNumber(float64(res.Inv)))
	return o
}

// runFilter is the -test.run pattern the test binary was started with, ""
// when none was given.
func runFilter() string {
	if f := flag.Lookup("test.run"); f != nil {
		return f.Value.String()
	}
	return ""
}

// The .git/index rule (gitIndexDigest): two indexes that record the same
// entries with different stat data compare equal, a different entry
// differs, and an unreadable index falls back to its bytes. Only a path
// named .git/index takes the rule.
func TestGitIndexRule(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r := makeWriterRoots(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = r.ws
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "HOME=" + r.home}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", ".")
	if err := os.WriteFile(filepath.Join(r.ws, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	reloc := relocation(t, r, nil)
	index := filepath.Join(r.ws, ".git", "index")
	first, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	before := reloc.fileDigest(".git/index", first)
	// New stat data for the same entry: touch the file, refresh the index.
	if err := os.Chtimes(filepath.Join(r.ws, "a.txt"), time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	git("update-index", "--really-refresh")
	second, _ := os.ReadFile(index)
	if bytes.Equal(first, second) {
		t.Fatal("the refreshed index has the same bytes; the test proves nothing")
	}
	if after := reloc.fileDigest(".git/index", second); after != before {
		t.Errorf("same entries, new stat data: %s != %s", after, before)
	}
	if err := os.WriteFile(filepath.Join(r.ws, "a.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	third, _ := os.ReadFile(index)
	if reloc.fileDigest(".git/index", third) == before {
		t.Error("a changed entry kept the digest")
	}
	if got := reloc.fileDigest("sub/index", third); got != canonical.SHA256HexBytes(third) {
		t.Error("a file not named .git/index took the rule")
	}
	if err := os.WriteFile(index, []byte("not an index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := reloc.fileDigest(".git/index", []byte("not an index")); got != canonical.SHA256HexBytes([]byte("not an index")) {
		t.Error("an unreadable index is not compared by its bytes")
	}
	if _, ok := snapshotObjectDigest("main/.git/faultline/objects/ab/" + strings.Repeat("c", 38)); !ok {
		t.Error("a snapshot object is not compared by name")
	}
	if _, ok := snapshotObjectDigest(".git/objects/ab/" + strings.Repeat("c", 38)); ok {
		t.Error("a repository object took the snapshot object rule")
	}
	if !isGitIndex("repo/.git/index") || isGitIndex(".git/index.lock") || isGitIndex("x.git/index") ||
		!isGitIndex("main/.git/worktrees/wt/index") || isGitIndex("main/.git/worktrees/wt/x/index") || isGitIndex("x.git/worktrees/wt/index") {
		t.Error("isGitIndex")
	}
}

// Each run of consecutive --version records is sorted (Promise.all in
// doctor, which `fl init --yes --cli` runs twice); every other record keeps
// its place.
func TestSortVersionRecordRuns(t *testing.T) {
	in := []string{"node --version", "git -c\x1fa=b\x1f--version", "docker --version", "git -C\x1f<WS>\x1fstatus", "docker --version", "docker info",
		"node --version", "docker --version", "git -c\x1fa=b\x1f--version", "docker info"}
	want := []string{"docker --version", "git -c\x1fa=b\x1f--version", "node --version", "git -C\x1f<WS>\x1fstatus", "docker --version", "docker info",
		"docker --version", "git -c\x1fa=b\x1f--version", "node --version", "docker info"}
	if got := sortVersionRecordRuns(in); !slices.Equal(got, want) {
		t.Errorf("%q", got)
	}
	if in[0] != "node --version" {
		t.Error("the input was reordered in place")
	}
	if got := sortVersionRecordRuns(nil); len(got) != 0 {
		t.Error(got)
	}
}

// doctor-hostile-fsmonitor (tests/cli.test.ts:664): the scenario's
// repository-local core.fsmonitor command would write fsmonitor-ran into
// the workspace; doctor's hardened git calls must never run it, so the
// only file the invocation changes is the index git refreshes. The tree
// comparison then holds Go to the same.
func TestDoctorHostileFsmonitorGolden(t *testing.T) {
	want := loadWriterGoldens(t, repoRoot(t))["doctor-hostile-fsmonitor|0"]
	if want == nil {
		t.Fatal("no golden for doctor-hostile-fsmonitor")
	}
	if keys := want.Field("files").Obj().Keys(); !slices.Equal(keys, []string{".git/index"}) {
		t.Fatalf("doctor-hostile-fsmonitor changed %q", keys)
	}
	if want.Field("exit").Num() != 0 || !strings.Contains(want.Field("stdout").Str(), "[READY] worktree:") {
		t.Fatalf("%s", want.Field("stdout").Str())
	}
}
