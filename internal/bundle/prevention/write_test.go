package prevention

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Fixtures of tests/prevention-proof.test.ts (difftest/gen/fixtures.ts).
func fixtureDigest(label string) string { return "sha256:" + canonical.SHA256Hex(label) }

func fixtureCommit(n int) string {
	return strings.Repeat("0", 40-len(hexOf(n))) + hexOf(n)
}

func hexOf(n int) string {
	const digits = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 16 {
		s = string(digits[n%16]) + s
	}
	return s
}

func fixtureRunIDs(prefix string) jsjson.Value {
	return stringValues([]string{fixtureDigest(prefix + "-1"), fixtureDigest(prefix + "-2"), fixtureDigest(prefix + "-3")})
}

func fixtureState(commit, tree int, runs string) jsjson.Value {
	return jsjson.MustObject(
		"commit", jsjson.MakeString(fixtureCommit(commit)),
		"tree", jsjson.MakeString(fixtureCommit(tree)),
		"witnessDigest", jsjson.MakeString(fixtureDigest("witness")),
		"environmentDigest", jsjson.MakeString(fixtureDigest("environment")),
		"executionTrust", jsjson.MakeString("NATIVE_DOCKER"),
		"executionKind", jsjson.MakeString("EXECUTED"),
		"distinctExecutionCount", jsjson.MakeNumber(3),
		"runIds", fixtureRunIDs(runs),
	)
}

// sampleInput is fixtures.ts sampleInput.
func sampleInput() *jsjson.Obj {
	return jsjson.MustObject(
		"originalProofRoot", jsjson.MakeString(fixtureDigest("proof-root")),
		"frozenWitnessDigest", jsjson.MakeString(fixtureDigest("witness")),
		"investigationDigest", jsjson.MakeString(fixtureDigest("investigation")),
		"lastGood", fixtureState(1, 11, "lg"),
		"firstBad", fixtureState(2, 22, "fb"),
		"repaired", fixtureState(3, 33, "rp"),
		"repairPatchDigest", jsjson.MakeString(fixtureDigest("patch")),
		"codexThreadId", jsjson.MakeString("thread_prevention_test"),
	).Obj()
}

// groundedInput is fixtures.ts groundedPreventionInput.
func groundedInput() *jsjson.Obj {
	o := sampleInput()
	o.Set("grounding", jsjson.MakeString("VERIFIED"))
	o.Set("repairBaseTree", jsjson.MakeString(fixtureCommit(22)))
	o.Set("repairPatchDigest", jsjson.MakeString(fixtureDigest("repair-patch")))
	var bindings []jsjson.Value
	for i, id := range fixtureRunIDs("rp").Items() {
		bindings = append(bindings, jsjson.MustObject(
			"runId", id,
			"executionId", jsjson.MakeString(fixtureDigest("exec-"+hexOf(i))),
			"commit", jsjson.MakeString(fixtureCommit(3)),
			"tree", jsjson.MakeString(fixtureCommit(33)),
			"verdict", jsjson.MakeString("PASS"),
			"witnessDigest", jsjson.MakeString(fixtureDigest("witness")),
			"environmentDigest", jsjson.MakeString(fixtureDigest("environment")),
			"executionTrust", jsjson.MakeString("NATIVE_DOCKER"),
			"executionKind", jsjson.MakeString("EXECUTED"),
		))
	}
	o.Set("repairedRunBindings", jsjson.MakeArray(bindings))
	return o
}

// basesDir is difftest/testdata/bases, resolved before any test changes
// directory.
var basesDir, _ = filepath.Abs(filepath.Join("..", "..", "..", "difftest", "testdata", "bases"))

// groundedBodyFixture is the PREVENTION_VERIFIED body of the slice 1 base.
func groundedBodyFixture(t *testing.T) jsjson.Value {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(basesDir, "prevention-verified", "prevention.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// workspace runs the test in a fresh working directory (the writer's
// default root is resolved from it).
func workspace(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

func defaultOutput(name string) string { return filepath.Join(defaultRoot(), name) }

// TestWriteMatchesCommittedBases writes the slice 1 base inputs and
// compares every file with the bases frozen TS wrote (gen-bases.ts).
func TestWriteMatchesCommittedBases(t *testing.T) {
	for base, input := range map[string]*jsjson.Obj{"prevention-summary": sampleInput(), "prevention-verified": groundedInput()} {
		want := filepath.Join(basesDir, base)
		workspace(t)
		w, err := Write(jsjson.MakeObject(input), defaultOutput(base))
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}
		names, _ := os.ReadDir(w.Directory)
		wantNames, _ := os.ReadDir(want)
		if len(names) != len(wantNames) {
			t.Fatalf("%s: %d files, want %d", base, len(names), len(wantNames))
		}
		for _, e := range names {
			got, _ := os.ReadFile(filepath.Join(w.Directory, e.Name()))
			expected, err := os.ReadFile(filepath.Join(want, e.Name()))
			if err != nil || string(got) != string(expected) {
				t.Errorf("%s/%s:\ngot  %s\nwant %s", base, e.Name(), got, expected)
			}
			if info, _ := e.Info(); info.Mode().Perm() != 0o600 {
				t.Errorf("%s/%s mode %v", base, e.Name(), info.Mode())
			}
		}
	}
}

// TestWriteSummary ports "writes and verifies a content-addressed prevention package".
func TestWriteSummary(t *testing.T) {
	workspace(t)
	w, err := Write(jsjson.MakeObject(sampleInput()), defaultOutput("case-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(w.RootDigest) ||
		w.Body.Get("schemaVersion").Str() != SchemaVersion ||
		w.Manifest.Get("classification").Str() != "PREVENTION_EVIDENCE_SUMMARY" ||
		len(w.Body.Get("lastGood", "runIds").Items()) != 3 {
		t.Fatalf("%+v", w)
	}
	if v := Verify(w.Directory, w.RootDigest, true); !v.Valid || v.ExternalRootStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
	if w.Body.Get("codexThreadId").Str() != "thread_prevention_test" || w.Body.Get("repairPatchDigest").Str() != fixtureDigest("patch") {
		t.Fatal(jsjson.Stringify(w.Body))
	}
	if info, err := os.Stat(w.Directory); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("%v %v", info, err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Directory, "README.md")); !strings.HasPrefix(string(b), "# FaultLine prevention proof\n\nStatus: **PREVENTION_EVIDENCE_SUMMARY**") || !strings.HasSuffix(string(b), "root cause.\n") {
		t.Fatal(string(b))
	}
}

// TestWriteGrounded ports "writes PREVENTION_VERIFIED when grounded with repaired run bindings".
func TestWriteGrounded(t *testing.T) {
	workspace(t)
	w, err := Write(jsjson.MakeObject(groundedInput()), defaultOutput("grounded"))
	if err != nil {
		t.Fatal(err)
	}
	if w.Manifest.Get("classification").Str() != "PREVENTION_VERIFIED" {
		t.Fatal(jsjson.Stringify(w.Manifest))
	}
	b, _ := os.ReadFile(filepath.Join(w.Directory, "repaired-runs.json"))
	if !strings.Contains(string(b), fixtureDigest("rp-1")) {
		t.Fatal(string(b))
	}
	if v := Verify(w.Directory, w.RootDigest, true); !v.Valid || *v.Classification != "PREVENTION_VERIFIED" {
		t.Fatalf("%+v", v)
	}
	if got := verifiedRootDigest(w.Body); got != w.RootDigest {
		t.Fatalf("verifiedRootDigest %s, written %s", got, w.RootDigest)
	}
}

func tamper(t *testing.T, dir string, edit func(body *jsjson.Obj)) {
	t.Helper()
	path := filepath.Join(dir, "prevention.json")
	b, _ := os.ReadFile(path)
	v, err := jsjson.Parse(string(b))
	if err != nil {
		t.Fatal(err)
	}
	edit(v.Obj())
	if err := os.WriteFile(path, []byte(jsjson.StringifyIndent(v, "  ")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestWriteTamperAndRootMismatch ports "fails closed on tamper and mismatched external root".
func TestWriteTamperAndRootMismatch(t *testing.T) {
	workspace(t)
	w, err := Write(jsjson.MakeObject(sampleInput()), defaultOutput("case-2"))
	if err != nil {
		t.Fatal(err)
	}
	tamper(t, w.Directory, func(body *jsjson.Obj) { body.Field("repaired").Obj().Set("verdict", jsjson.MakeString("FAIL")) })
	if v := Verify(w.Directory, "", false); v.Valid || !regexp.MustCompile(`(?i)digest|PASS|schema|verdict`).MatchString(strings.Join(v.Errors, "\n")) {
		t.Fatalf("%+v", v)
	}
	os.RemoveAll(w.Directory)
	clean, err := Write(jsjson.MakeObject(sampleInput()), defaultOutput("case-2b"))
	if err != nil {
		t.Fatal(err)
	}
	if v := Verify(clean.Directory, fixtureDigest("wrong-root"), true); v.Valid || v.ExternalRootStatus != "MISMATCH" {
		t.Fatalf("%+v", v)
	}
}

// TestWriteCodexThreadTamper ports "fails closed when codexThreadId is tampered (CDX-09)".
func TestWriteCodexThreadTamper(t *testing.T) {
	workspace(t)
	input := sampleInput()
	input.Set("codexThreadId", jsjson.MakeString("thread_offline_binding_001"))
	w, err := Write(jsjson.MakeObject(input), defaultOutput("thread-bind"))
	if err != nil {
		t.Fatal(err)
	}
	if v := Verify(w.Directory, w.RootDigest, true); !v.Valid {
		t.Fatalf("%+v", v)
	}
	if regexp.MustCompile(`(?i)transcript|promptText|messages`).MatchString(jsjson.Stringify(w.Body)) {
		t.Fatal(jsjson.Stringify(w.Body))
	}
	tamper(t, w.Directory, func(body *jsjson.Obj) {
		body.Set("codexThreadId", jsjson.MakeString("thread_offline_binding_TAMPERED"))
	})
	if v := Verify(w.Directory, w.RootDigest, true); v.Valid || !regexp.MustCompile(`(?i)digest|root|mismatch|schema`).MatchString(strings.Join(v.Errors, "\n")) {
		t.Fatalf("%+v", v)
	}
}

// TestWriteRefusals ports "refuses to write non-PASS/FAIL/PASS or non-Docker evidence".
func TestWriteRefusals(t *testing.T) {
	workspace(t)
	badCount := sampleInput()
	lastGood := spreadObject(badCount.Field("lastGood"))
	lastGood.Set("distinctExecutionCount", jsjson.MakeNumber(2))
	lastGood.Set("runIds", stringValues([]string{fixtureDigest("a"), fixtureDigest("b"), fixtureDigest("c")}))
	badCount.Set("lastGood", jsjson.MakeObject(lastGood))
	if _, err := Write(jsjson.MakeObject(badCount), defaultOutput("bad-count")); err == nil || !regexp.MustCompile(`(?i)Refusing|distinctExecutionCount|runIds`).MatchString(err.Error()) {
		t.Fatal(err)
	}
	badTrust := sampleInput()
	repaired := spreadObject(badTrust.Field("repaired"))
	repaired.Set("executionTrust", jsjson.MakeString("UNSAFE_LOCAL"))
	badTrust.Set("repaired", jsjson.MakeObject(repaired))
	if _, err := Write(jsjson.MakeObject(badTrust), defaultOutput("bad-trust")); err == nil || !regexp.MustCompile(`(?i)Refusing|Invalid|NATIVE_DOCKER|three distinct`).MatchString(err.Error()) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(defaultRoot())
	if len(entries) != 0 {
		t.Fatalf("a refused write left %v", entries)
	}
}

// TestWriteGroundedWithoutBindings ports "fail-closes grounded write without repaired bindings".
func TestWriteGroundedWithoutBindings(t *testing.T) {
	workspace(t)
	input := sampleInput()
	input.Set("grounding", jsjson.MakeString("VERIFIED"))
	input.Set("repairBaseTree", jsjson.MakeString(fixtureCommit(22)))
	if _, err := Write(jsjson.MakeObject(input), defaultOutput("noground")); err == nil || err.Error() != "Grounded prevention write requires three repairedRunBindings." {
		t.Fatal(err)
	}
}

func TestWriteOutputRefusals(t *testing.T) {
	dir := workspace(t)
	root := defaultRoot()
	for output, want := range map[string]string{
		root:                            "Prevention proof output must be a child directory of " + root,
		filepath.Join(dir, "elsewhere"): "Prevention proof output must be a child directory of " + root,
	} {
		if _, err := Write(jsjson.MakeObject(sampleInput()), output); err == nil || err.Error() != want {
			t.Fatalf("%s: %v", output, err)
		}
	}
	if _, err := Write(jsjson.MakeObject(sampleInput()), defaultOutput("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(jsjson.MakeObject(sampleInput()), defaultOutput("a")); err == nil || err.Error() != "Prevention proof output already exists and will not be replaced: "+defaultOutput("a") {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, ".faultline")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("root parents are created 0700: %v %v", info, err)
	}
	os.Symlink(dir, filepath.Join(root, "link"))
	if _, err := Write(jsjson.MakeObject(sampleInput()), filepath.Join(root, "link", "x")); err == nil || err.Error() != "Prevention proof output cannot traverse a symbolic link or non-directory: "+filepath.Join(root, "link") {
		t.Fatal(err)
	}
}

// TestWriteSpreadQuirks covers the object literal's spreads.
func TestWriteSpreadQuirks(t *testing.T) {
	workspace(t)
	for _, c := range []struct {
		name  string
		edit  func(o *jsjson.Obj)
		check func(t *testing.T, w Written, err error)
	}{
		{"null input", nil, func(t *testing.T, _ Written, err error) {
			if err == nil || err.Error() != "Cannot read properties of null (reading 'grounding')" {
				t.Fatal(err)
			}
		}},
		{"hard guard object", func(o *jsjson.Obj) { o.Set("hardGuardArtifactDigests", jsjson.MustObject()) }, func(t *testing.T, _ Written, err error) {
			if err == nil || err.Error() != "input.hardGuardArtifactDigests is not iterable" {
				t.Fatal(err)
			}
		}},
		{"hard guard null", func(o *jsjson.Obj) { o.Set("hardGuardArtifactDigests", jsjson.MakeNull()) }, func(t *testing.T, _ Written, err error) {
			if err == nil || err.Error() != "input.hardGuardArtifactDigests is not iterable" {
				t.Fatal(err)
			}
		}},
		{"hard guard digests", func(o *jsjson.Obj) {
			o.Set("hardGuardArtifactDigests", stringValues([]string{fixtureDigest("g")}))
		}, func(t *testing.T, w Written, err error) {
			if err != nil || len(w.Body.Get("hardGuardArtifactDigests").Items()) != 1 {
				t.Fatal(err)
			}
		}},
		{"state null spreads to {}", func(o *jsjson.Obj) { o.Set("lastGood", jsjson.MakeNull()) }, func(t *testing.T, _ Written, err error) {
			if err == nil || !strings.Contains(err.Error(), `"lastGood",`) || !strings.Contains(err.Error(), `"commit"`) {
				t.Fatal(err)
			}
		}},
		{"state string spreads to indices", func(o *jsjson.Obj) { o.Set("lastGood", jsjson.MakeString("ab")) }, func(t *testing.T, _ Written, err error) {
			if err == nil || !strings.Contains(err.Error(), `"0",`) || !strings.Contains(err.Error(), "unrecognized_keys") {
				t.Fatal(err)
			}
		}},
	} {
		input := jsjson.MakeNull()
		if c.edit != nil {
			o := sampleInput()
			c.edit(o)
			input = jsjson.MakeObject(o)
		}
		w, err := Write(input, defaultOutput(strings.ReplaceAll(c.name, " ", "-")))
		t.Run(c.name, func(t *testing.T) { c.check(t, w, err) })
	}
}

// TestWriteRemovesStageOnFailure makes the stage unwritable after prepare
// (a read-only output parent) and checks that no .*.tmp is left behind.
func TestWriteRemovesStageOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	workspace(t)
	parent := filepath.Join(defaultRoot(), "ro")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(parent, 0o500)
	defer os.Chmod(parent, 0o700)
	_, err := Write(jsjson.MakeObject(sampleInput()), filepath.Join(parent, "out"))
	if err == nil || !strings.HasPrefix(err.Error(), "EACCES: permission denied, mkdir '"+parent+"/.out.") {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}

}
