//go:build faultline_test && unix

package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// describeTree lists every entry under root with its mode, and each regular
// file's SHA-256, sorted, relative to root (so two sibling roots compare).
func describeTree(root string) string {
	var lines []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, "L "+rel+" "+target)
		case info.IsDir():
			lines = append(lines, "D "+rel+" "+info.Mode().Perm().String())
		default:
			b, _ := os.ReadFile(path)
			lines = append(lines, "F "+rel+" "+info.Mode().Perm().String()+" "+canonical.SHA256HexBytes(b))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// unlockTree gives every directory under root (root included) mode 0755,
// parents first, so that an unreadable stage can be listed and removed.
func unlockTree(root string) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return
	}
	if info.Mode().Perm()&0o700 != 0o700 {
		os.Chmod(root, 0o755)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		unlockTree(filepath.Join(root, e.Name()))
	}
}

func parseOrFatal(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return v
}

func reparse(t *testing.T, v jsjson.Value) jsjson.Value { return parseOrFatal(t, jsjson.Stringify(v)) }

// gitUnboundOptions is the --from-bundle recipe for the slice 2 base
// git-unbound: the bundle's third state (transitions[1].after) as the
// repaired state, its three runs' ids, the frozen witness digest and the
// homogeneous environment digest.
func gitUnboundOptions(t *testing.T, base string) prevention.ArtifactOptions {
	t.Helper()
	dir := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases", base)
	read := func(rel string) jsjson.Value {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return parseOrFatal(t, string(b))
	}
	investigation, frozen := read("investigation.json"), read("witness/frozen.json")
	stable := investigation.Get("stableStates").Items()
	if len(stable) == 0 {
		t.Fatalf("%s has no stable state", base)
	}
	state := stable[len(stable)-1]
	if ts := investigation.Get("transitions").Items(); len(ts) > 1 {
		state = ts[1].Get("after")
	}
	var runs []jsjson.Value
	for i, id := range state.Get("runIds").Items() {
		runs = append(runs, jsjson.MustObject(
			"runId", id,
			"executionId", state.Get("executionIds").Items()[i],
			"commit", state.Get("commit"),
			"tree", state.Get("tree"),
			"verdict", jsjson.MakeString("PASS"),
			"witnessDigest", frozen.Get("witnessDigest"),
			"environmentDigest", investigation.Get("environment", "distinctDigests").Items()[0],
			"executionTrust", jsjson.MakeString("NATIVE_DOCKER"),
			"executionKind", jsjson.MakeString("EXECUTED"),
		))
	}
	root, err := os.ReadFile(filepath.Join(dir, "ROOT.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	return prevention.ArtifactOptions{
		Bundle: dir, ExpectedRoot: strings.TrimSpace(string(root)), RepairedRuns: jsjson.MakeArray(runs),
		RepairedCommit: state.Get("commit").Str(), RepairedTree: state.Get("tree").Str(),
		PatchDigest: "sha256:" + strings.Repeat("1", 64),
	}
}

// preventionWriteInputs are the writer's seed inputs: the slice 1 bases'
// sampleInput and groundedPreventionInput (from fixtures.ts itself) and
// the input built from git-unbound.
func preventionWriteInputs(t *testing.T, c *oracle.Client) []jsjson.Value {
	t.Helper()
	fixtures := parseOrFatal(t, oracleText(t, c, "preventionFixtures", "null"))
	built, reasons := prevention.BuildFromArtifacts(gitUnboundOptions(t, "git-unbound"))
	if reasons != nil {
		t.Fatal(reasons)
	}
	return []jsjson.Value{fixtures.Get("summary"), fixtures.Get("grounded"), built}
}

var loneSurrogate = jsstr.FromUTF16([]uint16{0xD800})

// mutatePreventionInput applies one of the writer's quirk mutations.
func mutatePreventionInput(r *rand.Rand, input jsjson.Value) jsjson.Value {
	text := jsjson.Stringify(input)
	switch r.IntN(14) {
	case 0:
		return parseText(gen.CorruptLeaf(r, text))
	case 1, 2:
		return parseText(gen.SwapLeaf(r, text))
	case 3:
		return parseText(gen.CorruptDeep(r, text))
	case 4:
		return []jsjson.Value{jsjson.MakeNull(), jsjson.MakeNumber(5), jsjson.MakeString("str"), jsjson.MakeArray(nil), jsjson.MustObject()}[r.IntN(5)]
	}
	o := input.Obj()
	states := []string{"lastGood", "firstBad", "repaired"}
	switch r.IntN(9) {
	case 0: // a state that is not an object spreads its indices or nothing
		o.Set(states[r.IntN(3)], []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString("ab"), jsjson.MakeNumber(1), jsjson.MakeArray([]jsjson.Value{jsjson.MakeString("x")}), jsjson.MakeBool(true)}[r.IntN(5)])
	case 1:
		var many []jsjson.Value
		for i := range 65 {
			many = append(many, jsjson.MakeString("sha256:"+canonical.SHA256Hex(strconv.Itoa(i))))
		}
		o.Set("hardGuardArtifactDigests", []jsjson.Value{
			jsjson.MakeNull(), jsjson.MakeNumber(1), jsjson.MustObject(), jsjson.MakeString("ab"), jsjson.MakeBool(false),
			jsjson.MakeArray(many[:1]), jsjson.MakeArray(many), jsjson.MakeArray(nil), jsjson.MakeString(""),
		}[r.IntN(9)])
	case 2: // lone surrogates do not survive the UTF-8 write: refused at publish
		o.Set("codexThreadId", jsjson.MakeString([]string{"t" + loneSurrogate, "", strings.Repeat("x", 257), "é😀", loneSurrogate}[r.IntN(5)]))
	case 3:
		o.Set("grounding", []jsjson.Value{jsjson.MakeString("VERIFIED"), jsjson.MakeString("SUMMARY"), jsjson.MakeString("verified"), jsjson.MakeNull(), jsjson.MakeBool(true)}[r.IntN(5)])
	case 4:
		for _, k := range []string{"repairPatchDigest", "repairBaseTree", "repairedRunBindings", "investigationDigest", "codexThreadId"} {
			if r.IntN(3) == 0 {
				o.Delete(k)
			}
		}
	case 5:
		v := []jsjson.Value{jsjson.MakeString(""), jsjson.MakeNumber(0), jsjson.MakeNull(), jsjson.MakeBool(false), jsjson.MakeString("x")}[r.IntN(5)]
		o.Set([]string{"repairPatchDigest", "repairBaseTree"}[r.IntN(2)], v)
	case 6:
		bindings := o.Field("repairedRunBindings")
		if bindings.Kind() != jsjson.Array {
			bindings = jsjson.MakeArray(nil)
		}
		items := bindings.Items()
		o.Set("repairedRunBindings", []jsjson.Value{
			jsjson.MustObject("length", jsjson.MakeNumber(3)), jsjson.MakeString("abc"), jsjson.MakeArray(items[:min(2, len(items))]),
			jsjson.MakeArray(append(append([]jsjson.Value(nil), items...), items...)), jsjson.MakeNull(),
		}[r.IntN(5)])
	case 7: // a SUMMARY with bindings still writes repaired-runs.json
		o.Delete("grounding")
	case 8:
		st := o.Field(states[r.IntN(3)])
		if st.Kind() == jsjson.Object {
			st.Obj().Set([]string{"role", "verdict", "extra", "tree"}[r.IntN(4)], jsjson.MakeString("FAIL"))
		}
	}
	return input
}

func parseText(text string) jsjson.Value {
	v, err := jsjson.Parse(text)
	if err != nil {
		return jsjson.MakeString(text)
	}
	return v
}

// writtenView is TS's JSON.stringify of a WrittenPreventionProof.
func writtenView(w prevention.Written) string {
	o := jsjson.NewObj()
	o.Set("directory", jsjson.MakeString(w.Directory))
	o.Set("manifest", w.Manifest)
	o.Set("prevention", w.Body)
	o.Set("rootDigest", jsjson.MakeString(w.RootDigest))
	return jsjson.Stringify(jsjson.MakeObject(o))
}

// TestLivePreventionWrite writes 800 generated inputs (the summary and
// grounded slice 1 inputs and the git-unbound build, with valid and invalid
// semantics and the spread quirks) into fresh workspaces with frozen TS's
// writePreventionProof and the Go port, both seeded, and compares the
// result (or THREW: text) and the workspace trees, modes included. Some
// cases change the output (outside or equal to the root, existing, under a
// symbolic link or a file, a symlinked root) or the umask (an unwritable
// stage, which must be removed).
func TestLivePreventionWrite(t *testing.T) {
	oracle.Skip(t)
	const seed = "live-prevention-write"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	inputs := preventionWriteInputs(t, c)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	r := rand.New(rand.NewPCG(73, 21))
	written, refused := 0, 0
	for i := 0; i < 800; i++ {
		input := reparse(t, inputs[r.IntN(len(inputs))])
		label := "valid"
		if r.IntN(5) != 0 {
			input = mutatePreventionInput(r, input)
			label = "mutated"
		}
		ts, gs := filepath.Join(work, "ts", strconv.Itoa(i)), filepath.Join(work, "go", strconv.Itoa(i))
		output := filepath.Join(".faultline", "prevention-proofs", "out")
		umask := -1
		prepare := func(dir string) {
			os.MkdirAll(dir, 0o755)
		}
		var setup func(dir string)
		switch r.IntN(16) {
		case 0:
			output = filepath.Join(".faultline", "prevention-proofs")
		case 1:
			output = "elsewhere"
		case 2:
			output = filepath.Join(".faultline", "prevention-proofs", "a", "b", "out")
		case 3:
			setup = func(dir string) { os.MkdirAll(filepath.Join(dir, ".faultline", "prevention-proofs", "out"), 0o755) }
		case 4:
			output = filepath.Join(".faultline", "prevention-proofs", "link", "out")
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, ".faultline", "prevention-proofs"), 0o755)
				os.Symlink("..", filepath.Join(dir, ".faultline", "prevention-proofs", "link"))
			}
		case 5:
			output = filepath.Join(".faultline", "prevention-proofs", "file", "out")
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, ".faultline", "prevention-proofs"), 0o755)
				os.WriteFile(filepath.Join(dir, ".faultline", "prevention-proofs", "file"), []byte("x"), 0o644)
			}
		case 6:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, "real"), 0o755)
				os.Symlink("real", filepath.Join(dir, ".faultline"))
			}
		case 7, 8:
			// Under 0277 the stage is created 0500, so its first file cannot
			// be opened; under 0477 it is 0300, so the files are written but
			// the stage cannot be read back to verify it. Either way the
			// stage is removed (or its removal fails as in TS).
			umask = []int{0o277, 0o477, 0o077}[r.IntN(3)]
			setup = func(dir string) { os.MkdirAll(filepath.Join(dir, ".faultline", "prevention-proofs"), 0o700) }
		case 9:
			output = filepath.Join(".faultline", "prevention-proofs", "ro", "out")
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, ".faultline", "prevention-proofs", "ro"), 0o755)
				os.Chmod(filepath.Join(dir, ".faultline", "prevention-proofs", "ro"), 0o555)
			}
		}
		prepare(ts)
		prepare(gs)
		if setup != nil {
			setup(ts)
			setup(gs)
		}
		args := jsjson.NewObj()
		args.Set("cwd", jsjson.MakeString(ts))
		args.Set("output", jsjson.MakeString(output))
		args.Set("input", input)
		if umask >= 0 {
			args.Set("umask", jsjson.MakeNumber(float64(umask)))
		}
		want := strings.ReplaceAll(oracleText(t, c, "writePreventionProof", jsjson.Stringify(jsjson.MakeObject(args))), ts, gs)
		os.Chdir(gs)
		old := 0
		if umask >= 0 {
			old = syscall.Umask(umask)
		}
		w, err := prevention.Write(input, output)
		if umask >= 0 {
			syscall.Umask(old)
		}
		os.Chdir(cwd)
		got := ""
		if err != nil {
			got = "THREW:" + jsexc.Message(err)
			refused++
		} else {
			got = writtenView(w)
			written++
		}
		recordOutput(t, want)
		if got != want {
			t.Fatalf("case %d (%s, %s): %s\ngot  %s\nwant %s", i, label, output, jsjson.Stringify(input), got, want)
		}
		// A stage whose removal failed (unreadable under umask 0477) and the
		// read-only parent: compare their modes, then make them removable.
		a, b := describeTree(gs), describeTree(ts)
		for _, dir := range []string{ts, gs} {
			unlockTree(dir)
		}
		if a != b {
			t.Fatalf("case %d (%s): workspace trees differ:\ngo\n%s\nts\n%s", i, label, a, b)
		}
	}
	liveSummary(t, "prevention writer: %d written, %d refused", written, refused)
	requireReached(t, "internal/bundle/prevention/write.go", recordedOutputs(t))
}

// TestLiveAgentsMd upserts the AGENTS.md block into 1,000 generated
// repositories with frozen TS's upsertFaultLineAgentsMd and the Go port:
// existing contents with the markers in every order, nested and repeated,
// CRLF, whitespace-only, invalid UTF-8 and the current block; a missing
// file, a directory, a symlink, a repository that is a file or missing;
// and non-digest fields. It compares {path, status} (or THREW:) and the
// repository trees.
func TestLiveAgentsMd(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	b, err := os.ReadFile(filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases", "prevention-verified", "prevention.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := parseOrFatal(t, string(b))
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The block of the base body, rendered by the Go port (TS compares it).
	scratch := filepath.Join(work, "scratch")
	if _, err := prevention.UpsertAgentsMd(scratch, base); err != nil {
		t.Fatal(err)
	}
	block, _ := os.ReadFile(filepath.Join(scratch, "AGENTS.md"))
	const begin, end = "<!-- FAULTLINE:BEGIN -->", "<!-- FAULTLINE:END -->"
	tokens := []string{begin, end, begin, end, "\n", "\r\n", "\r", " ", "\t", "\u00a0", "\u2028", "\ufeff", "\u3000", "\u180e",
		"text", "# Project\n\nKeep me.\n", "\xff", "\xed\xa0\x80", "\xc3", "é", "😀", string(block), string(block), "<!-- FAULTLINE:BEGIN", "-->"}
	r := rand.New(rand.NewPCG(33, 9))
	statuses := map[string]int{}
	for i := 0; i < 1000; i++ {
		body := reparse(t, base)
		label := "base"
		if r.IntN(12) == 0 {
			k := []string{"originalProofRoot", "frozenWitnessDigest"}[r.IntN(2)]
			body.Obj().Set(k, jsjson.MakeString([]string{"not-a-digest", strings.Repeat("a", 40), strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 63), strings.Repeat("b", 65)}[r.IntN(5)]))
			label = k
		}
		if r.IntN(12) == 0 {
			st := []string{"lastGood", "firstBad", "repaired"}[r.IntN(3)]
			ids := body.Get(st, "runIds").Items()
			ids[r.IntN(len(ids))] = jsjson.MakeString([]string{strings.Repeat("c", 40), "run-1", "", "sha256:" + strings.Repeat("d", 64)}[r.IntN(4)])
			label += " " + st
		}
		var content []byte
		for n := r.IntN(9); n > 0; n-- {
			content = append(content, tokens[r.IntN(len(tokens))]...)
		}
		ts, gs := filepath.Join(work, "ts", strconv.Itoa(i)), filepath.Join(work, "go", strconv.Itoa(i))
		repoArg := "repo"
		mode := r.IntN(20)
		for _, dir := range []string{ts, gs} {
			repo := filepath.Join(dir, "repo")
			os.MkdirAll(repo, 0o755)
			switch mode {
			case 0: // no AGENTS.md
			case 1:
				os.Mkdir(filepath.Join(repo, "AGENTS.md"), 0o755)
			case 2:
				os.WriteFile(filepath.Join(dir, "target.md"), content, 0o600)
				os.Symlink(filepath.Join("..", "target.md"), filepath.Join(repo, "AGENTS.md"))
			case 3:
				os.WriteFile(filepath.Join(dir, "file"), nil, 0o644)
			case 4: // a repository that does not exist yet
			case 5:
				os.WriteFile(filepath.Join(repo, "AGENTS.md"), content, 0o600)
			default:
				os.WriteFile(filepath.Join(repo, "AGENTS.md"), content, 0o644)
			}
		}
		switch mode {
		case 3:
			repoArg = "file"
		case 4:
			repoArg = filepath.Join("missing", "nested")
		}
		input := jsjson.MustObject(
			"originalProofRoot", body.Get("originalProofRoot"),
			"frozenWitnessDigest", body.Get("frozenWitnessDigest"),
			"preventionRootDigest", jsjson.MakeString(prevention.VerifiedRootDigestForTest(body)),
			"lastGoodRunIds", body.Get("lastGood", "runIds"),
			"firstBadRunIds", body.Get("firstBad", "runIds"),
			"repairedRunIds", body.Get("repaired", "runIds"),
		)
		want := oracleText(t, c, "upsertFaultLineAgentsMd", `{"repository":`+jsjson.Quote(filepath.Join(ts, repoArg))+`,"input":`+jsjson.Stringify(input)+`}`)
		want = strings.ReplaceAll(want, ts, gs)
		goRepo := filepath.Join(gs, repoArg)
		status, err := prevention.UpsertAgentsMd(goRepo, body)
		got := ""
		if err != nil {
			got = "THREW:" + jsexc.Message(err)
			statuses["THREW"]++
		} else {
			got = jsjson.Stringify(jsjson.MustObject("path", jsjson.MakeString(filepath.Join(goRepo, "AGENTS.md")), "status", jsjson.MakeString(status)))
			statuses[status]++
		}
		recordOutput(t, want)
		if got != want {
			t.Fatalf("case %d (%s, mode %d, %q):\ngot  %s\nwant %s", i, label, mode, content, got, want)
		}
		if a, b := describeTree(gs), describeTree(ts); a != b {
			t.Fatalf("case %d (%s, mode %d, %q): trees differ:\ngo\n%s\nts\n%s", i, label, mode, content, a, b)
		}
	}
	liveSummary(t, "AGENTS.md upserts: %v", statuses)
	requireReached(t, "internal/bundle/prevention/agentsmd.go", recordedOutputs(t))
}

// artifactCase is one --from-bundle build: the options and the patch file.
type artifactCase struct {
	label string
	o     prevention.ArtifactOptions
	patch string
}

func (ac artifactCase) oracleArgs() string {
	args := jsjson.NewObj()
	args.Set("bundle", jsjson.MakeString(ac.o.Bundle))
	args.Set("expectRoot", jsjson.MakeString(ac.o.ExpectedRoot))
	args.Set("commit", jsjson.MakeString(ac.o.RepairedCommit))
	args.Set("tree", jsjson.MakeString(ac.o.RepairedTree))
	args.Set("runs", ac.o.RepairedRuns)
	args.Set("patch", jsjson.MakeString(ac.patch))
	if ac.o.HasThreadID {
		args.Set("codexThreadId", jsjson.MakeString(ac.o.CodexThreadID))
	}
	return jsjson.Stringify(jsjson.MakeObject(args))
}

// goArtifactResult is the Go side of the oracle op: the patch digest, then
// the build; a thrown TS error (a null run) is THREW:.
func goArtifactResult(ac artifactCase) string {
	var out string
	if err := jsexc.Try(func() {
		digest, err := prevention.DigestRepairPatchFile(ac.patch)
		if err != nil {
			jsexc.Throw(err)
		}
		ac.o.PatchDigest = digest
		input, reasons := prevention.BuildFromArtifacts(ac.o)
		if reasons != nil {
			out = jsjson.Stringify(jsjson.MustObject("reasons", stringArray(reasons)))
			return
		}
		out = jsjson.Stringify(input)
	}); err != nil {
		return "THREW:" + jsexc.Message(err)
	}
	return out
}

func stringArray(values []string) jsjson.Value {
	items := make([]jsjson.Value, len(values))
	for i, v := range values {
		items[i] = jsjson.MakeString(v)
	}
	return jsjson.MakeArray(items)
}

// mutateRepairedRuns varies the repaired-runs document's runs array.
func mutateRepairedRuns(r *rand.Rand, runs jsjson.Value, label *string) jsjson.Value {
	items := runs.Items()
	set := func(k string, v jsjson.Value) {
		run := items[r.IntN(len(items))]
		if run.Kind() == jsjson.Object {
			run.Obj().Set(k, v)
		}
		*label += " " + k
	}
	switch r.IntN(16) {
	case 0:
		*label += " count"
		return jsjson.MakeArray(items[:r.IntN(3)])
	case 1:
		*label += " count"
		return jsjson.MakeArray(append(items, items[0]))
	case 2:
		*label += " duplicate"
		items[2] = reparseValue(items[r.IntN(2)])
	case 3:
		set("commit", jsjson.MakeString(strings.Repeat("a", 40)))
	case 4:
		set("tree", jsjson.MakeNull())
	case 5:
		set([]string{"verdict", "executionTrust", "executionKind"}[r.IntN(3)], jsjson.MakeString("FAIL"))
	case 6:
		set("witnessDigest", jsjson.MakeString("sha256:"+strings.Repeat("e", 64)))
	case 7:
		set("environmentDigest", jsjson.MakeString("sha256:"+strings.Repeat("f", 64)))
	case 8: // the run ID quoted in a refusal: a template literal's ToString
		items[0].Obj().Set("runId", []jsjson.Value{
			jsjson.MakeNumber(-0.5), jsjson.MakeNull(), jsjson.MustObject("toString", jsjson.MakeNumber(1)), jsjson.MustObject("a", jsjson.MakeNumber(1)),
			jsjson.MakeArray([]jsjson.Value{jsjson.MakeNumber(1), jsjson.MakeNull(), jsjson.MakeArray([]jsjson.Value{jsjson.MakeString("x"), jsjson.MustObject()})}),
			jsjson.MakeArray([]jsjson.Value{jsjson.MustObject("toString", jsjson.MakeString("x"))}), jsjson.MakeBool(true), jsjson.MakeNumber(1e21),
		}[r.IntN(8)])
		items[0].Obj().Set("commit", jsjson.MakeString("x"))
		*label += " runId-tostring"
	case 9:
		*label += " not-object"
		items[r.IntN(3)] = []jsjson.Value{jsjson.MakeNull(), jsjson.MakeNumber(1), jsjson.MakeString("run"), jsjson.MakeArray(nil)}[r.IntN(4)]
	case 10:
		set("runId", jsjson.MakeString("sha256:"+strings.Repeat("9", 64)))
	case 11:
		set("executionId", jsjson.MakeNull())
	case 12:
		set("extra", jsjson.MakeString("kept out"))
	}
	return jsjson.MakeArray(items)
}

func reparseValue(v jsjson.Value) jsjson.Value {
	out, _ := jsjson.Parse(jsjson.Stringify(v))
	return out
}

// TestLivePreventionFromArtifacts builds 300 distinct grounded prevention inputs
// from the slice 2 base bundle git-unbound (and a few other bases, whose
// first refusal differs) with generated repaired runs and flag variations
// (the repaired commit and tree, the expected root, the patch, the Codex
// thread id, a tampered bundle copy) with frozen TS's
// buildPreventionProofFromVerifiedArtifacts (after digestRepairPatchFile,
// as the CLI calls them) and the Go port, and compares the input object or
// the reasons (or THREW:). TS verifies the bundle on every call (about a
// second), so its calls are spread over several oracles; Go verifies each
// bundle and root once.
func TestLivePreventionFromArtifacts(t *testing.T) {
	oracle.Skip(t)
	defer prevention.MemoizeGitVerifyForTest()()
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(work, "repair.patch")
	os.WriteFile(patch, []byte("diff --git a/x b/x\n"), 0o644)
	tampered := filepath.Join(work, "tampered")
	baseDir := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases", "git-unbound")
	if err := os.CopyFS(tampered, os.DirFS(baseDir)); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(tampered, "VERIFY.md"), []byte("tampered\n"), 0o644)
	others := []string{"git-two-states", "git-shape-heterogeneous-proof", "git-fully-bound", "git-shape-same-verdict", "git-shape-run-frozen-digest"}
	r := rand.New(rand.NewPCG(37, 4))
	var cases []artifactCase
	seen := map[string]bool{}
	for attempts := 0; len(cases) < 300 && attempts < 1500; attempts++ {
		base := "git-unbound"
		if r.IntN(8) == 0 {
			base = others[r.IntN(len(others))]
		}
		o := gitUnboundOptions(t, base)
		o.RepairedRuns = reparseValue(o.RepairedRuns)
		label := base
		if r.IntN(4) != 0 {
			o.RepairedRuns = mutateRepairedRuns(r, o.RepairedRuns, &label)
		}
		ac := artifactCase{o: o, patch: patch}
		switch r.IntN(14) {
		case 0:
			ac.o.ExpectedRoot = "sha256:" + strings.Repeat("0", 64)
			label += " wrong-root"
		case 1:
			ac.o.ExpectedRoot = "not-a-digest"
			label += " bad-root"
		case 2:
			ac.o.Bundle = tampered
			label += " tampered"
		case 3:
			ac.patch = filepath.Join(work, "missing.patch")
			label += " missing-patch"
		case 4: // the runs and the flag name the last-good or first-bad commit
			ac.o.RepairedCommit = []string{"a6bd1829b171854025f790718a9b141f616c62b4", "60a88823e480d41398e0986eb30f6479e7ee705f"}[r.IntN(2)]
			for _, run := range ac.o.RepairedRuns.Items() {
				if run.Kind() == jsjson.Object && r.IntN(8) != 0 {
					run.Obj().Set("commit", jsjson.MakeString(ac.o.RepairedCommit))
				}
			}
			label += " repaired-commit-same"
		case 5:
			ac.o.RepairedTree = strings.Repeat("b", 40)
			label += " tree-flag"
		case 6, 7:
			ac.o.HasThreadID, ac.o.CodexThreadID = true, []string{"thread-1", "", strings.Repeat("t", 300)}[r.IntN(3)]
			label += " thread"
		case 8:
			ac.o.Bundle = filepath.Join(work, "nowhere")
			label += " missing-bundle"
		}
		ac.label = label
		if args := ac.oracleArgs(); !seen[args] { // distinct inputs only
			seen[args] = true
			cases = append(cases, ac)
		}
	}

	// TS results, over several oracles.
	const workers = 8
	want := make([]string, len(cases))
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		c := oracle.Start(t)
		defer c.Close()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; i < len(cases); i += workers {
				raw, err := c.Call("buildPreventionProofFromVerifiedArtifacts", cases[i].oracleArgs())
				if err != nil {
					errs[w] = err
					return
				}
				v, err := jsjson.Parse(raw)
				if err != nil {
					errs[w] = err
					return
				}
				want[i] = v.Str()
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	outcomes := map[string]int{}
	for i, ac := range cases {
		got := goArtifactResult(ac)
		recordOutput(t, want[i])
		if got != want[i] {
			t.Fatalf("case %d (%s): runs %s\ngot  %s\nwant %s", i, ac.label, jsjson.Stringify(ac.o.RepairedRuns), got, want[i])
		}
		switch {
		case strings.HasPrefix(got, "THREW:"):
			outcomes["threw"]++
		case strings.HasPrefix(got, `{"reasons"`):
			outcomes["refused"]++
		default:
			outcomes["built"]++
		}
	}
	liveSummary(t, "--from-bundle builds: %v", outcomes)
	requireReached(t, "internal/bundle/prevention/artifacts.go", recordedOutputs(t))
}
