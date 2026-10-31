package demo

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// basesDir is absolute: writer tests chdir into a fresh case root.
var basesDir = func() string {
	abs, err := filepath.Abs("../../../difftest/testdata/bases")
	if err != nil {
		panic(err)
	}
	return abs + "/"
}()

// readTree returns every regular file below root keyed by slash path, and
// fails on any other entry kind.
func readTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("non-regular artifact: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func sameTree(t *testing.T, label string, got, want map[string][]byte) {
	t.Helper()
	var paths []string
	for p := range want {
		paths = append(paths, p)
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		g, gok := got[p]
		w, wok := want[p]
		if gok != wok || string(g) != string(w) {
			t.Errorf("%s: %s differs (got present=%v len=%d, want present=%v len=%d)", label, p, gok, len(g), wok, len(w))
		}
	}
}

func parseBaseAnalysis(t *testing.T, base string) jsjson.Value {
	t.Helper()
	text, err := os.ReadFile(basesDir + base + "/analysis.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWriteReplayExact(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	out := filepath.Join(root, "demo")
	written, err := Write(out, demofixture.CreateReplayAnalysis(), WriteOptions{ProofRoot: &root})
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := "sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa"
	if written.RootDigest != wantRoot {
		t.Fatal(written.RootDigest)
	}
	if written.Directory != out {
		t.Fatalf("directory %q want %q", written.Directory, out)
	}
	if written.ManifestDigest != "sha256:8ca247f159e5f4b19104146c0be893c8ee96c1bf7f8f9e62451466189768409b" {
		t.Fatal(written.ManifestDigest)
	}
	want := readTree(t, basesDir+"demo-replay")
	got := readTree(t, out)
	if len(want) != 69 || !reflect.DeepEqual(got, want) {
		sameTree(t, "replay", got, want)
		t.Fatal("complete demo artifact tree differs")
	}
	verified := Verify(out, written.RootDigest, true)
	if !verified.Valid || verified.CheckedFiles != 67 {
		t.Fatalf("%+v", verified)
	}
}

// The three retained historical inputs are supplied analyses: nothing is
// executed or schema-reconstructed, and each tree must equal its retained
// frozen-TS base. Digests are the pinned TS return values (golden writer-*).
func TestWriteHistoricalInputsExact(t *testing.T) {
	rows := []struct {
		base, manifest, root string
		files                int
		valid                bool
		firstError           string
	}{
		{"demo-rerun", "sha256:06d59cfc8c73e6b267ea563f2858833ec865496abf038faad18bb718bf808cab",
			"sha256:408bc1c3ebdf71c785b6192b34f465d4e00ec481c91a2283f62c7517ebcb0a37", 124, true, ""},
		{"demo-rerun-unicode-stdout", "sha256:ab374aa8f46913b56bd5cfb2f2afdfb53eda6102eb810f959cc9723a51475ced",
			"sha256:150a1d251d5f72ca81680b7716c47950f130c3d5eaa9cb7298285bb981e1453c", 124, true, ""},
		// Node encodes the lone surrogate as U+FFFD, so the TS verifier of the
		// bundle TS wrote reports the stdout mismatch below; that is the
		// recorded verdict, not a Go choice.
		{"demo-rerun-lone-surrogate-stdout", "sha256:f76c05d1a83255580630d708f89b4fb798f2dae711ba1431c45230a737e723a3",
			"sha256:64f79a696a9c7c5bc6d6e7b39b9e752ffcc685457167b4f0586fea2e4a983fef", 124, false,
			"stdout does not match catalog for sha256:0000dcd7125bda852aa1fa33d69b42f900c462e79568d2a6f95f74d4051334f1"},
	}
	for _, row := range rows {
		t.Run(row.base, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "managed")
			out := filepath.Join(root, "demo")
			written, err := Write(out, parseBaseAnalysis(t, row.base), WriteOptions{ProofRoot: &root})
			if err != nil {
				t.Fatal(err)
			}
			if written.ManifestDigest != row.manifest || written.RootDigest != row.root {
				t.Fatalf("digests %+v", written)
			}
			want := readTree(t, basesDir+row.base)
			sameTree(t, row.base, readTree(t, out), want)
			verified := Verify(out, "", false)
			if verified.Valid != row.valid || verified.CheckedFiles != row.files {
				t.Fatalf("%+v", verified)
			}
			if !row.valid && !reflect.DeepEqual(verified.Errors, []string{row.firstError}) {
				t.Fatalf("%q", verified.Errors)
			}
		})
	}
}

func TestWriteManifestDigest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	out := filepath.Join(root, "demo")
	written, err := Write(out, demofixture.CreateReplayAnalysis(), WriteOptions{ProofRoot: &root})
	if err != nil {
		t.Fatal(err)
	}
	pretty, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jsjson.Parse(string(pretty))
	if err != nil {
		t.Fatal(err)
	}
	canon, err := canonical.DigestJSON(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if written.ManifestDigest != canon {
		t.Fatalf("returned %s, canonical %s", written.ManifestDigest, canon)
	}
	if prettyDigest := "sha256:" + canonical.SHA256HexBytes(pretty); written.ManifestDigest == prettyDigest {
		t.Fatal("manifest digest is the digest of the pretty file")
	}
	keys := strings.Join(parsed.Obj().Keys(), ",")
	if keys != "schemaVersion,investigationId,fixtureId,generatedAt,analysisDigest,witnessDigest,environmentDigest,mode,integrityScope" {
		t.Fatal(keys)
	}
}

// goldenRow is one frozen-TS capture (difftest/testdata/demo-artifacts).
// Only the control envelope is standard JSON; bodies are base64 bytes.
type goldenRow struct {
	Request struct {
		ID        string  `json:"id"`
		Base      string  `json:"base"`
		Mutation  string  `json:"mutation"`
		Setup     string  `json:"setup"`
		Output    *string `json:"output"`
		ProofRoot *string `json:"proofRoot"`
	} `json:"request"`
	Call struct {
		ValueJSON *string `json:"valueJSON"`
		Error     *string `json:"error"`
	} `json:"call"`
	Before []goldenEntry `json:"before"`
	After  []goldenEntry `json:"after"`
}

type goldenEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Base64 string `json:"base64"`
	Target string `json:"target"`
}

func loadGolden(t *testing.T, id string) goldenRow {
	t.Helper()
	f, err := os.Open("../../../difftest/testdata/demo-artifacts/golden.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var row goldenRow
			if jerr := json.Unmarshal(line, &row); jerr != nil {
				t.Fatal(jerr)
			}
			if row.Request.ID == id {
				return row
			}
		}
		if err != nil {
			t.Fatalf("golden row %s missing", id)
		}
	}
}

// inventory is lstat-based, byte-path sorted and does not follow links.
func inventory(t *testing.T, root string) []goldenEntry {
	t.Helper()
	var out []goldenEntry
	var walk func(dir, prefix string)
	walk = func(dir, prefix string) {
		names, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			p := filepath.Join(dir, n.Name())
			rel := n.Name()
			if prefix != "" {
				rel = prefix + "/" + n.Name()
			}
			info, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				target, err := os.Readlink(p)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, goldenEntry{Path: rel, Kind: "symlink", Target: portableTarget(target, runtime.GOOS == "windows")})
			case info.IsDir():
				out = append(out, goldenEntry{Path: rel, Kind: "directory"})
				walk(p, rel)
			case info.Mode().IsRegular():
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, goldenEntry{Path: rel, Kind: "file", Base64: base64.StdEncoding.EncodeToString(b)})
			default:
				t.Fatalf("unsupported entry %s", rel)
			}
		}
	}
	walk(root, "")
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func compareInventory(t *testing.T, label string, got, want []goldenEntry) {
	t.Helper()
	if d := inventoryDiff(got, want); d != "" {
		t.Errorf("%s: %s", label, d)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	for rel, body := range readTree(t, from) {
		dst := filepath.Join(to, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// link makes an in-fixture relative symlink; a filesystem that cannot is a
// narrowly named eligibility skip, never a pass.
func link(t *testing.T, root, rel, target string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Skipf("symlink creation unavailable in the fixture filesystem: %v", err)
	}
}

func relTo(fromRel, toRel string) string {
	r, err := filepath.Rel(filepath.Dir(filepath.FromSlash(fromRel)), filepath.FromSlash(toRel))
	if err != nil {
		panic(err)
	}
	return filepath.ToSlash(r)
}

func cloneObj(v jsjson.Value) *jsjson.Obj {
	o := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		o.Set(k, v.Obj().Field(k))
	}
	return o
}

func mutateAnalysis(t *testing.T, a jsjson.Value, mutation string) jsjson.Value {
	t.Helper()
	catalog := a.Get("runCatalog").Items()
	switch mutation {
	case "":
	case "extra-keys":
		a.Obj().Set("z-extra", jsjson.MakeString("z-extra"))
		a.Obj().Set("a-extra", jsjson.MakeString("a-extra"))
	case "missing-catalog":
		a.Obj().Set("runCatalog", jsjson.MakeArray(append([]jsjson.Value(nil), catalog[1:]...)))
	case "duplicate-id":
		a.Obj().Set("runCatalog", jsjson.MakeArray(append(append([]jsjson.Value(nil), catalog...), catalog[0])))
	case "sanitized-collision":
		items := append([]jsjson.Value(nil), catalog...)
		for _, id := range []string{"collision:", "collision?"} {
			o := cloneObj(catalog[0])
			o.Set("id", jsjson.MakeString(id))
			items = append(items, jsjson.MakeObject(o))
		}
		a.Obj().Set("runCatalog", jsjson.MakeArray(items))
	default:
		t.Fatalf("unknown mutation %q", mutation)
	}
	return a
}

var baseDirNames = map[string]string{"rerun": "demo-rerun", "unicode": "demo-rerun-unicode-stdout", "lone-surrogate": "demo-rerun-lone-surrogate-stdout"}

// runGoldenWriter performs a captured writer request in a fresh root and
// compares the call and both complete inventories with the frozen-TS row.
func runGoldenWriter(t *testing.T, id string) {
	t.Helper()
	row := loadGolden(t, id)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	out := ".faultline/bundles/demo"
	if row.Request.Output != nil {
		out = *row.Request.Output
	}
	// Setup never calls the writer; the writer itself creates default parents.
	switch row.Request.Setup {
	case "":
	case "parent-file":
		put(t, root, filepath.ToSlash(filepath.Dir(out)), "blocker\n")
	case "output-file":
		put(t, root, out, "old output\n")
	case "invalid-bundle":
		put(t, root, out+"/sentinel.txt", "keep\n")
	case "existing-bundle":
		copyTree(t, basesDir+"demo-replay", filepath.Join(root, out))
	case "linked-root":
		if err := os.MkdirAll(filepath.Join(root, "actual-managed"), 0o755); err != nil {
			t.Fatal(err)
		}
		link(t, root, ".faultline/bundles", "../actual-managed")
	case "linked-output":
		copyTree(t, basesDir+"demo-replay", filepath.Join(root, "target-bundle"))
		link(t, root, out, relTo(out, "target-bundle"))
	case "linked-bundle-file":
		copyTree(t, basesDir+"demo-replay", filepath.Join(root, out))
		file := out + "/witness/witness.json"
		orig, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		put(t, root, "original-witness.json", string(orig))
		if err := os.Remove(filepath.Join(root, file)); err != nil {
			t.Fatal(err)
		}
		link(t, root, file, relTo(file, "original-witness.json"))
	default:
		t.Fatalf("unsupported setup %q", row.Request.Setup)
	}
	compareInventory(t, id+" before", inventory(t, root), row.Before)

	var analysis jsjson.Value
	if row.Request.Base == "" {
		analysis = demofixture.CreateReplayAnalysis()
	} else {
		analysis = parseBaseAnalysis(t, baseDirNames[row.Request.Base])
	}
	analysis = mutateAnalysis(t, analysis, row.Request.Mutation)
	written, err := Write(out, analysis, WriteOptions{ProofRoot: row.Request.ProofRoot})
	// The expectation, not the actual result, is adapted to the native root and
	// separator; the actual error and value are compared unmasked.
	switch {
	case row.Call.Error != nil:
		if err == nil {
			t.Fatalf("%s: wrote %+v, want error %q", id, written, *row.Call.Error)
		}
		want, merr := materializeTmp(*row.Call.Error, root, filepath.Separator)
		if merr != nil {
			t.Fatalf("%s: %v", id, merr)
		}
		if got := err.Error(); got != want {
			t.Errorf("%s: error\n got %q\nwant %q", id, got, want)
		}
	default:
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		want, merr := expectedValue(*row.Call.ValueJSON, root, filepath.Separator)
		if merr != nil {
			t.Fatalf("%s: %v", id, merr)
		}
		got := jsjson.Stringify(jsjson.MustObject(
			"directory", jsjson.MakeString(written.Directory),
			"manifestDigest", jsjson.MakeString(written.ManifestDigest),
			"rootDigest", jsjson.MakeString(written.RootDigest)))
		if got != want {
			t.Errorf("%s: value\n got %s\nwant %s", id, got, want)
		}
	}
	compareInventory(t, id+" after", inventory(t, root), row.After)
}

// materializeTmp turns a golden's single declared <TMP> path slot into the
// actual case root plus the slash-delimited tail converted to sep. Goldens
// were captured on POSIX with only the root prefix replaced by <TMP>, so the
// tail after it is always '/'-delimited. Exactly one token is accepted (none
// leaves the golden literal) and its tail must run to the end of the string
// and use only ASCII path characters: quotes, whitespace, backslashes or a
// second token are rejected rather than rewritten.
func materializeTmp(golden, root string, sep byte) (string, error) {
	n := strings.Count(golden, "<TMP>")
	if n == 0 {
		return golden, nil
	}
	if n > 1 {
		return "", fmt.Errorf("golden has %d <TMP> tokens: %q", n, golden)
	}
	i := strings.Index(golden, "<TMP>")
	tail := golden[i+len("<TMP>"):]
	for j := 0; j < len(tail); j++ {
		c := tail[j]
		ok := c == '/' || c == '.' || c == '-' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !ok {
			return "", fmt.Errorf("golden <TMP> tail %q has unexpected byte %q", tail, c)
		}
	}
	if tail != "" && tail[0] != '/' {
		return "", fmt.Errorf("golden <TMP> tail %q does not start a path", tail)
	}
	return golden[:i] + root + strings.ReplaceAll(tail, "/", string(sep)), nil
}

// expectedValue materializes a writer-result golden: it must be exactly the
// ordered object {directory, manifestDigest, rootDigest} of strings; only
// directory changes (its <TMP> slot), and the result is re-serialized.
func expectedValue(golden, root string, sep byte) (string, error) {
	v, err := jsjson.Parse(golden)
	if err != nil {
		return "", err
	}
	if v.Kind() != jsjson.Object || strings.Join(v.Obj().Keys(), ",") != "directory,manifestDigest,rootDigest" {
		return "", fmt.Errorf("golden value is not exactly {directory,manifestDigest,rootDigest}: %s", golden)
	}
	for _, k := range v.Obj().Keys() {
		if v.Get(k).Kind() != jsjson.String {
			return "", fmt.Errorf("golden value member %s is not a string: %s", k, golden)
		}
	}
	dir, err := materializeTmp(v.Get("directory").Str(), root, sep)
	if err != nil {
		return "", err
	}
	return jsjson.Stringify(jsjson.MustObject(
		"directory", jsjson.MakeString(dir),
		"manifestDigest", v.Get("manifestDigest"),
		"rootDigest", v.Get("rootDigest"))), nil
}

// portableTarget converts a Windows-native relative symlink target to the
// slash form the POSIX goldens use. On other platforms a backslash is a
// literal name character and is preserved.
func portableTarget(target string, windows bool) string {
	if windows {
		return strings.ReplaceAll(target, `\`, "/")
	}
	return target
}

func describeEntry(e goldenEntry) string {
	body := 0
	if e.Base64 != "" {
		body = len(e.Base64)
	}
	return fmt.Sprintf("path %q kind %q target %q body %d base64 chars", e.Path, e.Kind, e.Target, body)
}

// inventoryDiff is the first difference between two inventories ("" if none),
// naming path, kind, link target and body size of both entries.
func inventoryDiff(got, want []goldenEntry) string {
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			return fmt.Sprintf("entry %d: got %s; want %s", i, describeEntry(got[i]), describeEntry(want[i]))
		}
	}
	if len(got) != len(want) {
		return fmt.Sprintf("%d entries, want %d", len(got), len(want))
	}
	return ""
}

// The helpers below are the ones runGoldenWriter and inventory use; the tests
// call them with an explicit separator/platform so Windows behavior is checked
// on every OS. Only the declared <TMP> path slot of a golden is materialized;
// actual errors, JSON and artifact bodies are never rewritten.
func TestMaterializeTmpSlot(t *testing.T) {
	win := `C:\Users\r\t\001`
	rows := []struct {
		name, golden, root string
		sep                byte
		want               string
		bad                bool
	}{
		{"windows error", "Proof bundle output must be a directory: <TMP>/.faultline/bundles/demo", win, '\\',
			`Proof bundle output must be a directory: ` + win + `\.faultline\bundles\demo`, false},
		{"windows nested", "Proof bundle root cannot traverse a symbolic link or non-directory: <TMP>/.faultline/bundles/blocked", win, '\\',
			`Proof bundle root cannot traverse a symbolic link or non-directory: ` + win + `\.faultline\bundles\blocked`, false},
		{"windows exact root", "child of <TMP>", win, '\\', "child of " + win, false},
		{"posix unchanged", "x: <TMP>/.faultline/bundles/demo", "/tmp/r", '/', "x: /tmp/r/.faultline/bundles/demo", false},
		{"no token stays literal", "Cannot write incomplete proof bundle: duplicate run id in catalog: 5", win, '\\',
			"Cannot write incomplete proof bundle: duplicate run id in catalog: 5", false},
		{"two tokens", "a <TMP>/x b <TMP>/y", win, '\\', "", true},
		{"quote in tail", `a <TMP>/x"y`, win, '\\', "", true},
		{"whitespace tail", "a <TMP>/x y", win, '\\', "", true},
		{"backslash in golden tail", `a <TMP>\x`, win, '\\', "", true},
		{"token mid-sentence", "a <TMP>/x then more", win, '\\', "", true},
	}
	for _, row := range rows {
		got, err := materializeTmp(row.golden, row.root, row.sep)
		if row.bad {
			if err == nil {
				t.Errorf("%s: accepted, got %q", row.name, got)
			}
			continue
		}
		if err != nil || got != row.want {
			t.Errorf("%s: got %q (%v), want %q", row.name, got, err, row.want)
		}
	}
	// The old prefix-only mask accepted a wrong root; the materialized
	// expectation must differ from an actual path under another root.
	other, _ := materializeTmp("x <TMP>/a", `D:\other`, '\\')
	right, _ := materializeTmp("x <TMP>/a", win, '\\')
	if other == right {
		t.Error("distinct roots materialize equal")
	}
}

func TestPortableTarget(t *testing.T) {
	rows := []struct {
		name, target string
		windows      bool
		want         string
	}{
		{"windows backslashes", `..\..\..\original-witness.json`, true, "../../../original-witness.json"},
		{"windows forward kept", "../actual-managed", true, "../actual-managed"},
		{"posix literal backslash preserved", `..\odd`, false, `..\odd`},
		{"posix plain", "../target-bundle", false, "../target-bundle"},
	}
	for _, row := range rows {
		if got := portableTarget(row.target, row.windows); got != row.want {
			t.Errorf("%s: %q, want %q", row.name, got, row.want)
		}
	}
	// A different target must stay different after conversion.
	if portableTarget(`..\a`, true) == portableTarget(`..\b`, true) {
		t.Error("distinct targets converted equal")
	}
}

func TestValueExpectationKeepsOrderedFields(t *testing.T) {
	golden := `{"directory":"<TMP>/.faultline/bundles/demo","manifestDigest":"sha256:aa","rootDigest":"sha256:bb"}`
	root := `C:\r`
	got, err := expectedValue(golden, root, '\\')
	if err != nil {
		t.Fatal(err)
	}
	want := `{"directory":"C:\\r\\.faultline\\bundles\\demo","manifestDigest":"sha256:aa","rootDigest":"sha256:bb"}`
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
	for name, bad := range map[string]string{
		"extra field":   `{"directory":"<TMP>/d","manifestDigest":"a","rootDigest":"b","extra":1}`,
		"missing field": `{"directory":"<TMP>/d","manifestDigest":"a"}`,
		"order":         `{"manifestDigest":"a","directory":"<TMP>/d","rootDigest":"b"}`,
		"non-string":    `{"directory":5,"manifestDigest":"a","rootDigest":"b"}`,
		"not json":      `{`,
	} {
		if _, err := expectedValue(bad, root, '\\'); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCompareInventoryReportsTargetAndBodyDifferences(t *testing.T) {
	a := []goldenEntry{{Path: "l", Kind: "symlink", Target: "../x"}, {Path: "f", Kind: "file", Base64: "QQ=="}}
	if d := inventoryDiff(a, append([]goldenEntry(nil), a...)); d != "" {
		t.Fatalf("equal inventories differ: %s", d)
	}
	b := append([]goldenEntry(nil), a...)
	b[0].Target = "../y"
	if d := inventoryDiff(a, b); !strings.Contains(d, `"../x"`) || !strings.Contains(d, `"../y"`) {
		t.Errorf("target difference not shown: %q", d)
	}
	// Same-length body change: detected (non-empty diff).
	c := append([]goldenEntry(nil), a...)
	c[1].Base64 = "QUI="
	if d := inventoryDiff(a, c); d == "" {
		t.Error("same-length body change not detected")
	}
	// Different-length body: the message states both body sizes (base64
	// characters), got first and want second, in describeEntry's format.
	e := append([]goldenEntry(nil), a...)
	e[1].Base64 = "QUJDRA=="
	wantDiff := `entry 1: got path "f" kind "file" target "" body 4 base64 chars; want path "f" kind "file" target "" body 8 base64 chars`
	if d := inventoryDiff(a, e); d != wantDiff {
		t.Errorf("body-size diagnostic\n got %q\nwant %q", d, wantDiff)
	}
}

func TestWriteOutputSafety(t *testing.T) {
	for _, id := range []string{
		"writer-root", "writer-outside", "writer-custom-root", "writer-empty-root", "writer-replay",
		"writer-output-file", "writer-parent-file", "writer-invalid-existing", "writer-replace",
	} {
		t.Run(id, func(t *testing.T) { runGoldenWriter(t, id) })
	}
}

// The default root is the cwd's .faultline/bundles only when the option is
// omitted; an explicit empty string is the cwd itself.
func TestWriteProofRootOmittedVersusEmpty(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if got, want := DefaultProofRoot(), filepath.Join(root, ".faultline", "bundles"); got != want {
		t.Fatalf("default root %q want %q", got, want)
	}
	if _, err := Write("demo", demofixture.CreateReplayAnalysis(), WriteOptions{}); err == nil ||
		err.Error() != "Proof bundle output must be a child directory of "+filepath.Join(root, ".faultline", "bundles") {
		t.Fatalf("omitted root: %v", err)
	}
	empty := ""
	if w, err := Write("demo", demofixture.CreateReplayAnalysis(), WriteOptions{ProofRoot: &empty}); err != nil || w.Directory != filepath.Join(root, "demo") {
		t.Fatalf("explicit empty root: %+v %v", w, err)
	}
}

func TestWriteCatalogAndCollision(t *testing.T) {
	for _, id := range []string{"writer-missing-catalog", "writer-duplicate-id", "writer-sanitized-collision", "writer-extra-keys"} {
		t.Run(id, func(t *testing.T) { runGoldenWriter(t, id) })
	}
}

func TestDemoWriterSymlinks(t *testing.T) {
	for _, id := range []string{"writer-linked-root", "writer-linked-output", "writer-linked-bundle-file"} {
		t.Run(id, func(t *testing.T) { runGoldenWriter(t, id) })
	}
}

// Replacement of a verified bundle by a different valid supplied analysis
// installs the complete new tree and leaves no staging or backup sibling.
// This is a success-path check only; rollback is asserted by the Linux fault
// differential test.
func TestWriteReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	out := filepath.Join(root, "demo")
	if _, err := Write(out, demofixture.CreateReplayAnalysis(), WriteOptions{ProofRoot: &root}); err != nil {
		t.Fatal(err)
	}
	written, err := Write(out, parseBaseAnalysis(t, "demo-rerun"), WriteOptions{ProofRoot: &root})
	if err != nil {
		t.Fatal(err)
	}
	sameTree(t, "replacement", readTree(t, out), readTree(t, basesDir+"demo-rerun"))
	if written.RootDigest != "sha256:408bc1c3ebdf71c785b6192b34f465d4e00ec481c91a2283f62c7517ebcb0a37" {
		t.Fatal(written.RootDigest)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "demo" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("siblings left behind: %v", names)
	}
	if bytes.Contains([]byte(written.Directory), []byte(".staging-")) {
		t.Fatal(written.Directory)
	}
}

// scratchRerun is the retained historical RERUN analysis, mutated in place.
func scratchRerun(t *testing.T, mutate func(a jsjson.Value)) jsjson.Value {
	t.Helper()
	a := parseBaseAnalysis(t, "demo-rerun")
	mutate(a)
	return a
}

// step follows an object key or, for an array, a decimal index.
func step(v jsjson.Value, k string) jsjson.Value {
	if v.Kind() == jsjson.Array {
		i, err := strconv.Atoi(k)
		if err != nil {
			panic(err)
		}
		return v.Items()[i]
	}
	return v.Get(k)
}

func setPath(v jsjson.Value, value jsjson.Value, keys ...string) {
	for _, k := range keys[:len(keys)-1] {
		v = step(v, k)
	}
	v.Obj().Set(keys[len(keys)-1], value)
}

func strv(s string) jsjson.Value { return jsjson.MakeString(s) }

func arr(items ...jsjson.Value) jsjson.Value { return jsjson.MakeArray(items) }

func obj1(k string, v jsjson.Value) jsjson.Value { return jsjson.MustObject(k, v) }

// The supplied analysis is not schema-parsed, so the writer must coerce
// non-string values exactly as the frozen JS does. Expectations are the
// actual pinned Node v22.22.2 results of writeProofBundle on the same scratch
// mutations of the retained demo-rerun input (t3f1-probe.mjs, container
// receipt 1793383515222913000-t3f1-probe-container): the conclusion/report
// line the JS template produced, and the manifest and root digests, which
// cover every artifact byte.
func TestWriteJSCoercionOfSuppliedValues(t *testing.T) {
	negZero := jsjson.MakeNumber(math.Copysign(0, -1))
	rows := []struct {
		name         string
		mutate       func(a jsjson.Value)
		line         string // exact report.md line expected
		manifest     string
		root         string
		fixtureIDKey string // "" = leave unchecked, "omitted" = key absent
	}{
		{"baseline", func(jsjson.Value) {}, "The earliest stable recorded transition for the frozen sample witness is cedar-turn-4 -> cedar-turn-5.",
			"sha256:06d59cfc8c73e6b267ea563f2858833ec865496abf038faad18bb718bf808cab", "sha256:408bc1c3ebdf71c785b6192b34f465d4e00ec481c91a2283f62c7517ebcb0a37", ""},
		{"state-ids-array-object", func(a jsjson.Value) {
			setPath(a, arr(strv("cedar"), strv("t4"), jsjson.MakeNull(), arr(strv("n"), jsjson.MakeNumber(1))), "transitions", "0", "beforeStateId")
			setPath(a, obj1("a", jsjson.MakeNumber(1)), "transitions", "0", "afterStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is cedar,t4,,n,1 -> [object Object].",
			"sha256:cb6a21ccbc92ac408f83249b57b3d6e3f57b659faa8896b309688f1b8910d826", "sha256:f5202bbefc5a2489b9ceac4ce15f8ea671649ce39064ef66c04e1cce40d72ac1", ""},
		{"state-ids-numbers", func(a jsjson.Value) {
			setPath(a, jsjson.MakeNumber(1e21), "transitions", "0", "beforeStateId")
			setPath(a, negZero, "transitions", "0", "afterStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is 1e+21 -> 0.",
			"sha256:e65beadc817a204dfd3f9c1d11e8b4129ff0158bc8cbe2dce1f77a7734286947", "sha256:a7b4535579e37e11d9cf27a03eaf87adc8c830a6ba7f30252a1fb3df1a0d508d", ""},
		{"state-ids-null-bool", func(a jsjson.Value) {
			setPath(a, jsjson.MakeNull(), "transitions", "0", "beforeStateId")
			setPath(a, jsjson.MakeBool(true), "transitions", "0", "afterStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is null -> true.",
			"sha256:afebb8a998578eee4edb6943b8a62d4f80be1a11770066747158a2a2032b4635", "sha256:0527f01ce9e9e5e9d1b33aee6129814a5dcdad209a367e0ae3a22d6ce78bd63e", ""},
		{"state-ids-missing", func(a jsjson.Value) {
			a.Get("transitions").Items()[0].Obj().Delete("beforeStateId")
			setPath(a, strv("str"), "transitions", "0", "afterStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is undefined -> str.",
			"sha256:cd6ccee89431694fc4444b163dbc3f699133d6a2f4c2f557cfcff4e15602f137", "sha256:f49cea2acec76f2fe63583482f971ff67c52212987208f6e883d55584618427f", ""},
		{"stable-truthy-string", func(a jsjson.Value) {
			setPath(a, strv("yes"), "transitions", "0", "stable")
			setPath(a, strv("first-chosen"), "transitions", "0", "beforeStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is first-chosen -> cedar-turn-5.",
			"sha256:e719d10e05ca64e140cb3426402a457dd84fde68c47f31e0f0574e0f7095b3b9", "sha256:99e5f14f39ef386122f4ae35bee1213b1b806441e094b0a1438cf5cdc21f650f", ""},
		{"stable-truthy-array", func(a jsjson.Value) {
			setPath(a, arr(), "transitions", "0", "stable")
			setPath(a, strv("first-chosen"), "transitions", "0", "beforeStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is first-chosen -> cedar-turn-5.",
			"sha256:fc61d3de950eeeefe7e74614467dc038cd3da0126c516ee480dfb6afa2171162", "sha256:10e347f7de7ef386c962d38c21d6fdfc11d982d8eda21877a49ee1980ed8865c", ""},
		{"stable-falsy-empty-string", func(a jsjson.Value) {
			setPath(a, strv(""), "transitions", "0", "stable")
			setPath(a, strv("skipped"), "transitions", "0", "beforeStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is cedar-turn-7 -> cedar-turn-8.",
			"sha256:113656b6f8343ab31eecc4e4bc863d1f337be62c3ab8ee910c5eddbd81363792", "sha256:ac15ce0b99ddcde03f9699c0959351d70e679ab5972c339802a0c68155c19082", ""},
		{"stable-falsy-null", func(a jsjson.Value) {
			setPath(a, jsjson.MakeNull(), "transitions", "0", "stable")
			setPath(a, strv("skipped"), "transitions", "0", "beforeStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is cedar-turn-7 -> cedar-turn-8.",
			"sha256:3f4cdd5b6a27ed8e72126c41db01ef2d074ec534d17a09caa2f25acbb8fab7bf", "sha256:008b1c50541d0269f5765183da212248f317cadfe23ebbdeb16122fbfef44aa1", ""},
		{"kind-nonstring", func(a jsjson.Value) {
			setPath(a, arr(strv("PASS_TO_FAIL")), "transitions", "0", "kind")
			setPath(a, strv("skipped"), "transitions", "0", "beforeStateId")
		}, "The earliest stable recorded transition for the frozen sample witness is cedar-turn-7 -> cedar-turn-8.",
			"sha256:7eafde84a09e59c10735051fc7330380da50475aa605de3ffbe66a33d903bdcb", "sha256:9eeae0d09f52a1d71dc8f9c16847b001f7db1270c63835062d9ff9ce30105f2b", ""},
		{"title-mode-digest-verdicts", func(a jsjson.Value) {
			setPath(a, arr(strv("a"), strv("b")), "fixture", "title")
			setPath(a, jsjson.MakeNull(), "mode")
			setPath(a, arr(strv("d")), "witness", "digest")
			setPath(a, jsjson.MakeNumber(7), "prevention", "lastGood", "verdict")
			setPath(a, arr(jsjson.MakeNumber(1), arr(jsjson.MakeNumber(2))), "prevention", "firstBad", "verdict")
			setPath(a, jsjson.MustObject(), "prevention", "repaired", "verdict")
		}, "- Last-good: 7\n- First-bad: 1,2\n- Repaired: [object Object]",
			"sha256:f0d182bca403b1592b2f558e7a87ba984b4128c5e8cbc5be4ada680035875bd0", "sha256:edb1119af45e544062d2c636284935ea7eabb58767168ceab277014d38bbd421", ""},
		{"title-missing", func(a jsjson.Value) { a.Get("fixture").Obj().Delete("title") }, "Fixture: undefined",
			"sha256:81d58318e9eff697c3d675a70b9a266a194b56101e2fa2c6aa137599851c300d", "sha256:72a3fdbe80af95ff0b03cc3c3a46ae82b780ee5fd10596d8be198ea3217ddf92", ""},
		{"fixture-id-number", func(a jsjson.Value) { setPath(a, jsjson.MakeNumber(5), "fixture", "id") }, "Mode: RERUN",
			"sha256:68416c5e2e5ddb87bf68382506a67a7668ceba1d939e3d563a35aefc145a9624", "sha256:4999a274478c69ec58f99a061f1bee5af7caced5cbef35de6d2d03abd28cf6e3", "sample-5"},
		{"fixture-id-array", func(a jsjson.Value) { setPath(a, arr(strv("a"), strv("b")), "fixture", "id") }, "Mode: RERUN",
			"sha256:76463afb8f75d74fe165da19344d75489c00e835ce7eac055930e959e5d92334", "sha256:4a011b93b7be6c786b41e2750d63c1f9ae113ff34589f9cf988b6c14e5ce1309", "sample-a,b"},
		{"fixture-id-object", func(a jsjson.Value) { setPath(a, obj1("k", jsjson.MakeNumber(1)), "fixture", "id") }, "Mode: RERUN",
			"sha256:b46de6cbe8a0534a80c568b96ed9c5152dbc30c1bfd152c1bb436605eb14085b", "sha256:028ee553a6de391f14f869505d8c5746712cc1f389b096c86b8dbcb1fa7e4446", "sample-[object Object]"},
		{"fixture-id-null", func(a jsjson.Value) { setPath(a, jsjson.MakeNull(), "fixture", "id") }, "Mode: RERUN",
			"sha256:e61f604114cbb82bf679616996ab8b48fbe15e05ce5b799bbc128318cf864b79", "sha256:297425875156c3c992c0ff39262b1c7208d64fa6abb75be7e4bd129465c7b61a", "sample-null"},
		{"fixture-id-missing", func(a jsjson.Value) { a.Get("fixture").Obj().Delete("id") }, "Mode: RERUN",
			"sha256:57b45c379c2a200f69e06b6f56b83948141bbfff773dea1af03a9151153af0e9", "sha256:2ad9ecead43135e3eac0c44ce31c67af54dc27bb9e7e6bdeb342c4bc48010bc7", "sample-undefined"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "managed")
			out := filepath.Join(root, "demo")
			written, err := Write(out, scratchRerun(t, row.mutate), WriteOptions{ProofRoot: &root})
			if err != nil {
				t.Fatal(err)
			}
			if written.ManifestDigest != row.manifest || written.RootDigest != row.root {
				t.Errorf("digests\n got %s %s\nwant %s %s", written.ManifestDigest, written.RootDigest, row.manifest, row.root)
			}
			report, rerr := os.ReadFile(filepath.Join(out, "report.md"))
			if rerr != nil {
				t.Fatal(rerr)
			}
			if !strings.Contains(string(report), "\n"+row.line) {
				t.Errorf("report lacks %q:\n%s", row.line, report)
			}
			if row.fixtureIDKey != "" {
				m, merr := os.ReadFile(filepath.Join(out, "manifest.json"))
				if merr != nil {
					t.Fatal(merr)
				}
				if !strings.Contains(string(m), `"investigationId": "`+row.fixtureIDKey+`"`) {
					t.Errorf("manifest investigationId: %s", m)
				}
			}
		})
	}
}

// Missing intermediate objects and non-string log bodies throw in the frozen
// writer at specific points; the Go writer throws the same TypeErrors, and
// for a bad log body (thrown while hashing) before any stage is created.
func TestWriteJSThrowsOnMalformedSuppliedValues(t *testing.T) {
	const dataArg = `The "data" argument must be of type string or an instance of Buffer, TypedArray, or DataView. Received `
	rows := []struct {
		name    string
		mutate  func(a jsjson.Value)
		want    string
		staging bool // whether a stage could have been created (never for these)
	}{
		{"fixture-deleted", func(a jsjson.Value) { a.Obj().Delete("fixture") }, "Cannot read properties of undefined (reading 'id')", false},
		{"fixture-null", func(a jsjson.Value) { a.Obj().Set("fixture", jsjson.MakeNull()) }, "Cannot read properties of null (reading 'id')", false},
		{"witness-deleted", func(a jsjson.Value) { a.Obj().Delete("witness") }, "Cannot read properties of undefined (reading 'digest')", false},
		{"witness-null", func(a jsjson.Value) { a.Obj().Set("witness", jsjson.MakeNull()) }, "Cannot read properties of null (reading 'digest')", false},
		{"stdout-number", func(a jsjson.Value) { setPath(a.Get("runCatalog").Items()[0], jsjson.MakeNumber(5), "stdout") }, dataArg + "type number (5)", false},
		{"stdout-missing", func(a jsjson.Value) { a.Get("runCatalog").Items()[0].Obj().Delete("stdout") }, dataArg + "undefined", false},
		{"stdout-null", func(a jsjson.Value) { setPath(a.Get("runCatalog").Items()[0], jsjson.MakeNull(), "stdout") }, dataArg + "null", false},
		{"stdout-array", func(a jsjson.Value) { setPath(a.Get("runCatalog").Items()[0], arr(strv("x")), "stdout") }, dataArg + "an instance of Array", false},
		{"stderr-object", func(a jsjson.Value) {
			setPath(a.Get("runCatalog").Items()[0], obj1("x", jsjson.MakeNumber(1)), "stderr")
		}, dataArg + "an instance of Object", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "managed")
			out := filepath.Join(root, "demo")
			_, err := Write(out, scratchRerun(t, row.mutate), WriteOptions{ProofRoot: &root})
			if err == nil || err.Error() != row.want {
				t.Fatalf("error %v, want %q", err, row.want)
			}
			entries, rerr := os.ReadDir(root)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if len(entries) != 0 {
				t.Errorf("managed root not empty after a pre-stage failure: %d entries", len(entries))
			}
		})
	}
}

// deleteAt removes the last key of a path (JS `delete`).
func deleteAt(v jsjson.Value, keys ...string) {
	for _, k := range keys[:len(keys)-1] {
		v = step(v, k)
	}
	v.Obj().Delete(keys[len(keys)-1])
}

// setAt replaces an array element or object member in place.
func setAt(v jsjson.Value, value jsjson.Value, keys ...string) {
	parent := v
	for _, k := range keys[:len(keys)-1] {
		parent = step(parent, k)
	}
	last := keys[len(keys)-1]
	if parent.Kind() == jsjson.Array {
		i, err := strconv.Atoi(last)
		if err != nil {
			panic(err)
		}
		parent.Items()[i] = value
		return
	}
	parent.Obj().Set(last, value)
}

// extraCatalogRecord appends an unreferenced copy of the first catalog record
// (so coverage passes) with the given id; a nil id deletes the member.
func extraCatalogRecord(a jsjson.Value, id *jsjson.Value) {
	o := cloneObj(a.Get("runCatalog").Items()[0])
	if id == nil {
		o.Delete("id")
	} else {
		o.Set("id", *id)
	}
	a.Obj().Set("runCatalog", jsjson.MakeArray(append(append([]jsjson.Value(nil), a.Get("runCatalog").Items()...), jsjson.MakeObject(o))))
}

const (
	notRef = "analysis references a run not present in the catalog: "
	sha0   = "sha256:0182fe957409a85a244136c3add1a7b4cc25f83828f28520284d90b331b7e3e3"
)

// The frozen writer's reference collection and coverage check run before the
// manifest, with JS property reads, iteration and throw order. Expectations
// are actual pinned Node v22.22.2 outputs of writeProofBundle on the same
// scratch mutations of the retained demo-rerun input: raw probe output in
// 1793385...-t3f2-probe-container.stdout and ...-t3f2b-probe-container.stdout
// (inner job and runner exit 0, Node v22.22.2, FaultLine 0.1.4). Every
// refusal happens before a stage exists, so the managed root stays empty.
func TestWriteCoverageJSSemantics(t *testing.T) {
	num := func(n float64) *jsjson.Value { v := jsjson.MakeNumber(n); return &v }
	nul := jsjson.MakeNull()
	rows := []struct {
		name   string
		mutate func(a jsjson.Value)
		want   string // exact error; "" means the write succeeds
		root   string // expected root digest when it succeeds
	}{
		{"prevention-deleted", func(a jsjson.Value) { deleteAt(a, "prevention") }, "Cannot read properties of undefined (reading 'lastGood')", ""},
		{"prevention-null", func(a jsjson.Value) { setAt(a, nul, "prevention") }, "Cannot read properties of null (reading 'lastGood')", ""},
		{"lastGood-deleted", func(a jsjson.Value) { deleteAt(a, "prevention", "lastGood") }, "Cannot read properties of undefined (reading 'id')", ""},
		{"firstBad-deleted", func(a jsjson.Value) { deleteAt(a, "prevention", "firstBad") }, "Cannot read properties of undefined (reading 'id')", ""},
		{"firstBad-null", func(a jsjson.Value) { setAt(a, nul, "prevention", "firstBad") }, "Cannot read properties of null (reading 'id')", ""},
		{"repaired-deleted", func(a jsjson.Value) { deleteAt(a, "prevention", "repaired") }, "Cannot read properties of undefined (reading 'id')", ""},
		{"minimization-deleted", func(a jsjson.Value) { deleteAt(a, "minimization") }, "Cannot read properties of undefined (reading 'sufficiency')", ""},
		{"minimization-null", func(a jsjson.Value) { setAt(a, nul, "minimization") }, "Cannot read properties of null (reading 'sufficiency')", ""},
		{"sufficiency-deleted", func(a jsjson.Value) { deleteAt(a, "minimization", "sufficiency") }, "Cannot read properties of undefined (reading 'id')", ""},
		{"necessity-null", func(a jsjson.Value) { setAt(a, nul, "minimization", "necessity") }, "Cannot read properties of null (reading 'id')", ""},
		{"contributionRuns-deleted", func(a jsjson.Value) { deleteAt(a, "contributionRuns") }, "analysis.contributionRuns is not iterable", ""},
		{"contributionRuns-null", func(a jsjson.Value) { setAt(a, nul, "contributionRuns") }, "analysis.contributionRuns is not iterable", ""},
		{"contributionRuns-number", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(5), "contributionRuns") }, "analysis.contributionRuns is not iterable", ""},
		{"contributionRuns-object", func(a jsjson.Value) { setAt(a, jsjson.MustObject(), "contributionRuns") }, "analysis.contributionRuns is not iterable", ""},
		// A string IS iterable in JS: its characters are runs whose id is undefined.
		{"contributionRuns-string", func(a jsjson.Value) { setAt(a, strv("ab"), "contributionRuns") }, "Cannot write incomplete proof bundle: " + notRef + "undefined", ""},
		{"contributionRuns-null-element", func(a jsjson.Value) { setAt(a, nul, "contributionRuns", "0") }, "Cannot read properties of null (reading 'id')", ""},
		{"timelineRuns-deleted", func(a jsjson.Value) { deleteAt(a, "timelineRuns") }, "analysis.timelineRuns is not iterable", ""},
		{"timelineRuns-bool", func(a jsjson.Value) { setAt(a, jsjson.MakeBool(true), "timelineRuns") }, "analysis.timelineRuns is not iterable", ""},
		{"transitions-deleted", func(a jsjson.Value) { deleteAt(a, "transitions") }, "analysis.transitions is not iterable", ""},
		{"transitions-null", func(a jsjson.Value) { setAt(a, nul, "transitions") }, "analysis.transitions is not iterable", ""},
		{"transitions-number", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(7), "transitions") }, "analysis.transitions is not iterable", ""},
		{"transition-null-element", func(a jsjson.Value) { setAt(a, nul, "transitions", "0") }, "Cannot read properties of null (reading 'boundaryRunIds')", ""},
		{"boundaryRunIds-deleted", func(a jsjson.Value) { deleteAt(a, "transitions", "0", "boundaryRunIds") }, "transition.boundaryRunIds is not iterable", ""},
		{"boundaryRunIds-number", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(3), "transitions", "0", "boundaryRunIds") }, "transition.boundaryRunIds is not iterable", ""},
		{"boundaryRunIds-string", func(a jsjson.Value) { setAt(a, strv("xy"), "transitions", "0", "boundaryRunIds") }, "Cannot write incomplete proof bundle: " + notRef + "x; " + notRef + "y", ""},
		{"attempts-deleted", func(a jsjson.Value) { deleteAt(a, "minimization", "attempts") }, "analysis.minimization.attempts is not iterable", ""},
		{"attempts-null", func(a jsjson.Value) { setAt(a, nul, "minimization", "attempts") }, "analysis.minimization.attempts is not iterable", ""},
		{"attempts-number", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(2), "minimization", "attempts") }, "analysis.minimization.attempts is not iterable", ""},
		{"attempt-null-element", func(a jsjson.Value) { setAt(a, nul, "minimization", "attempts", "0") }, "Cannot read properties of null (reading 'runId')", ""},
		// A truthy non-string runId is a reference ("5"); a falsy one (0) is skipped.
		{"attempt-runId-number", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(5), "minimization", "attempts", "0", "runId") }, "Cannot write incomplete proof bundle: " + notRef + "5", ""},
		{"attempt-runId-zero", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(0), "minimization", "attempts", "0", "runId") }, "", "sha256:f99a2075d13cf525e471b307319b7a3ce6b5934eff4d791a17e3ae8e55cf646b"},
		{"runCatalog-deleted", func(a jsjson.Value) { deleteAt(a, "runCatalog") }, "analysis.runCatalog is not iterable", ""},
		{"runCatalog-null", func(a jsjson.Value) { setAt(a, nul, "runCatalog") }, "analysis.runCatalog is not iterable", ""},
		{"runCatalog-number", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(5), "runCatalog") }, "analysis.runCatalog is not iterable", ""},
		{"runCatalog-object", func(a jsjson.Value) { setAt(a, jsjson.MustObject(), "runCatalog") }, "analysis.runCatalog is not iterable", ""},
		{"catalog-null-element", func(a jsjson.Value) { setAt(a, nul, "runCatalog", "0") }, "Cannot read properties of null (reading 'id')", ""},
		// The first record loses its id, so the run that references it is missing.
		{"catalog-id-deleted", func(a jsjson.Value) { deleteAt(a, "runCatalog", "0", "id") }, "Cannot write incomplete proof bundle: " + notRef + sha0, ""},
		{"catalog-id-number", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(5), "runCatalog", "0", "id") }, "Cannot write incomplete proof bundle: " + notRef + sha0, ""},
		{"catalog-id-array", func(a jsjson.Value) { setAt(a, arr(strv("a")), "runCatalog", "0", "id") }, "Cannot write incomplete proof bundle: " + notRef + sha0, ""},
		{"catalog-id-object", func(a jsjson.Value) { setAt(a, obj1("k", jsjson.MakeNumber(1)), "runCatalog", "0", "id") }, "Cannot write incomplete proof bundle: " + notRef + sha0, ""},
		// Competing malformed members: the order TS evaluates them in.
		{"order-prevention-vs-null-contribution", func(a jsjson.Value) { deleteAt(a, "prevention"); setAt(a, nul, "contributionRuns", "0") }, "Cannot read properties of undefined (reading 'lastGood')", ""},
		{"order-notiterable-vs-prevention", func(a jsjson.Value) { setAt(a, jsjson.MakeNumber(5), "contributionRuns"); deleteAt(a, "prevention") }, "analysis.contributionRuns is not iterable", ""},
		{"order-timeline-vs-contribution", func(a jsjson.Value) {
			setAt(a, jsjson.MakeNumber(5), "contributionRuns")
			setAt(a, jsjson.MakeNumber(6), "timelineRuns")
		}, "analysis.contributionRuns is not iterable", ""},
		{"order-catalog-vs-contribution", func(a jsjson.Value) { setAt(a, nul, "runCatalog"); setAt(a, jsjson.MakeNumber(5), "contributionRuns") }, "analysis.runCatalog is not iterable", ""},
		{"order-firstBad-vs-attempt", func(a jsjson.Value) {
			setAt(a, nul, "prevention", "firstBad")
			setAt(a, nul, "minimization", "attempts", "0")
		}, "Cannot read properties of null (reading 'id')", ""},
		{"order-firstBad-vs-transition", func(a jsjson.Value) { setAt(a, nul, "prevention", "firstBad"); setAt(a, nul, "transitions", "0") }, "Cannot read properties of null (reading 'id')", ""},
		{"order-transition-vs-attempts", func(a jsjson.Value) {
			setAt(a, nul, "transitions", "0")
			setAt(a, jsjson.MakeNumber(2), "minimization", "attempts")
		}, "Cannot read properties of null (reading 'boundaryRunIds')", ""},
		{"order-catalog-element-vs-prevention", func(a jsjson.Value) { setAt(a, nul, "runCatalog", "0"); deleteAt(a, "prevention") }, "Cannot read properties of null (reading 'id')", ""},
		{"order-fixture-vs-coverage", func(a jsjson.Value) { deleteAt(a, "fixture"); deleteAt(a, "prevention", "firstBad") }, "Cannot read properties of undefined (reading 'id')", ""},
		{"order-coverage-vs-body", func(a jsjson.Value) {
			deleteAt(a, "prevention", "firstBad")
			setAt(a, jsjson.MakeNumber(5), "runCatalog", "0", "stdout")
		}, "Cannot read properties of undefined (reading 'id')", ""},
		{"order-id-vs-body", func(a jsjson.Value) {
			setAt(a, jsjson.MakeNumber(5), "runCatalog", "0", "id")
			setAt(a, jsjson.MakeNumber(5), "runCatalog", "1", "stdout")
		}, "Cannot write incomplete proof bundle: " + notRef + sha0, ""},
		// Non-string ids reach safeRunFileName (TS run.id.replace) only on an
		// unreferenced record; they are never coerced into a file name.
		{"extra-id-ok-string", func(a jsjson.Value) { s := strv("zz-extra"); extraCatalogRecord(a, &s) }, "", "sha256:74ff44c4a9078204bbe7482bf5a1677a6fa88885100ed61db285f4e54fdf3d1a"},
		{"extra-id-deleted", func(a jsjson.Value) { extraCatalogRecord(a, nil) }, "Cannot read properties of undefined (reading 'replace')", ""},
		{"extra-id-null", func(a jsjson.Value) { extraCatalogRecord(a, &nul) }, "Cannot read properties of null (reading 'replace')", ""},
		{"extra-id-number", func(a jsjson.Value) { extraCatalogRecord(a, num(5)) }, "run.id.replace is not a function", ""},
		{"extra-id-array", func(a jsjson.Value) { v := arr(strv("a")); extraCatalogRecord(a, &v) }, "run.id.replace is not a function", ""},
		{"extra-id-object", func(a jsjson.Value) { v := obj1("k", jsjson.MakeNumber(1)); extraCatalogRecord(a, &v) }, "run.id.replace is not a function", ""},
		{"extra-id-bool", func(a jsjson.Value) { v := jsjson.MakeBool(true); extraCatalogRecord(a, &v) }, "run.id.replace is not a function", ""},
		{"extra-id-number-then-bad-body-later", func(a jsjson.Value) {
			extraCatalogRecord(a, num(5))
			setAt(a, jsjson.MakeNumber(5), "runCatalog", "1", "stdout")
		}, "run.id.replace is not a function", ""},
		{"bad-body-early-then-id-number", func(a jsjson.Value) {
			setAt(a, jsjson.MakeNumber(5), "runCatalog", "0", "stdout")
			extraCatalogRecord(a, num(5))
		}, "run.id.replace is not a function", ""},
		{"extra-id-number-and-duplicate", func(a jsjson.Value) { extraCatalogRecord(a, num(5)); extraCatalogRecord(a, num(5)) }, "Cannot write incomplete proof bundle: duplicate run id in catalog: 5", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "managed")
			out := filepath.Join(root, "demo")
			written, err := Write(out, scratchRerun(t, row.mutate), WriteOptions{ProofRoot: &root})
			if row.want == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				if written.RootDigest != row.root {
					t.Errorf("root %s, want %s", written.RootDigest, row.root)
				}
				return
			}
			if err == nil || err.Error() != row.want {
				t.Fatalf("error %v, want %q", err, row.want)
			}
			entries, rerr := os.ReadDir(root)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if len(entries) != 0 {
				t.Errorf("managed root not empty after a pre-stage failure: %d entries", len(entries))
			}
		})
	}
}

// A supplied `transitions: ""` passes coverage (a JS string is iterable and
// the empty one yields nothing), then the frozen report's
// `analysis.transitions.find` throws before any stage or file exists. The
// physical tree, including a pre-existing managed root with a sentinel, is
// unchanged. Pinned message: see difftest supplemental "transitions-empty-string".
func TestWriteEmptyStringTransitionsThrowsBeforeAnyWrite(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed")
	put(t, managed, "sentinel.txt", "keep\n")
	before := inventory(t, root)
	_, werr := Write(filepath.Join(managed, "demo"), scratchRerun(t, func(a jsjson.Value) { setAt(a, strv(""), "transitions") }), WriteOptions{ProofRoot: &managed})
	if werr == nil || werr.Error() != "analysis.transitions.find is not a function" {
		t.Fatalf("error %v, want %q", werr, "analysis.transitions.find is not a function")
	}
	compareInventory(t, "empty-string transitions", inventory(t, root), before)
}
