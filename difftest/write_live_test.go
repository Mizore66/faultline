//go:build unix

package difftest

import (
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/sandbox"
)

// writerCase is one investigation to package: its result, frozen witness
// and the writer options (the ledger as JSON).
type writerCase struct {
	label   string
	result  jsjson.Value
	frozen  jsjson.Value
	options jsjson.Value
}

// firstDiffering names the first file whose bytes differ between two trees.
func firstDiffering(t *testing.T, a, b string) string {
	var found string
	filepath.WalkDir(a, func(path string, d os.DirEntry, err error) error {
		if found != "" || err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(a, path)
		x, _ := os.ReadFile(path)
		y, errB := os.ReadFile(filepath.Join(b, rel))
		if errB != nil || string(x) != string(y) {
			found = rel + ":\ngo " + string(x) + "\nts " + string(y)
		}
		return nil
	})
	if found == "" {
		return "(a file present only in the TS bundle)"
	}
	return found
}

// TestLiveProofWriter packages 30 investigations from Task 13's generator
// (computed once in Go, which TestLiveInvestigate compares with TS) with
// frozen TS's writeGitInvestigationProofBundle and the Go port, both seeded,
// into sibling roots (ts/, go/), without a ledger, with one bound to the
// descendant, to every state or to the first state only (refused unless
// the range has one state), with and without Codex ids and a pinned
// generatedAt. It compares the returned digests and manifest (or the error),
// the bundle trees, and the recorded git argv (the roots normalized).
func TestLiveProofWriter(t *testing.T) {
	root := oracle.RepoRoot(t)
	oracle.Skip(t)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	clock.ResetForTest("live-writer-witnesses")
	witnesses := []jsjson.Value{
		gitinv.FrozenWitnessForTest(t),
		gitinv.FrozenWitnessWithOverlaysForTest(t, []string{"notes.txt", "中文 目录/新 文件.txt", "dir with space/é.txt"},
			[]string{"overlay notes\n", "叠加\n", "é\n"}),
	}
	hostPath := os.Getenv("PATH")
	ledgers := oracle.Start(t)
	defer ledgers.Close()
	r := rand.New(rand.NewPCG(14, 1))
	clock.ResetForTest("live-writer-investigations")
	var cases []writerCase
	for i := 0; i < 30; i++ {
		ic := randomInvestigateCase(t, r, witnesses)
		t.Setenv("PATH", filepath.Join(root, "difftest/testdata/fakedocker", ic.fake)+":"+filepath.Dir(gitPath)+":/usr/bin:/bin")
		result, err := gitinv.InvestigateGitRange(ic.request, sandbox.NativeRunner(), false)
		if err != nil {
			continue
		}
		options := jsjson.NewObj()
		label := ic.label + " " + result.Get("status").Str()
		repository := ic.request.Get("repository").Str()
		if result.Get("status").Str() == "COMPLETED" {
			var commits []jsjson.Value
			for _, s := range result.Get("states").Items() {
				commits = append(commits, s.Get("commit"))
			}
			args := ""
			switch r.IntN(4) {
			case 1:
				args = `{"repository":` + jsjson.Quote(repository) + `}`
				label += " ledger:descendant"
			case 2:
				args = `{"repository":` + jsjson.Quote(repository) + `,"commits":` + jsjson.Stringify(jsjson.MakeArray(commits)) + `}`
				label += " ledger:every"
			case 3:
				if r.IntN(3) == 0 {
					args = `{"repository":` + jsjson.Quote(repository) + `,"commits":` + jsjson.Stringify(jsjson.MakeArray(commits[:1])) + `}`
					label += " ledger:first"
				}
			}
			if args != "" {
				text := oracleText(t, ledgers, "gitLifecycleLedger", args)
				ledger, err := jsjson.Parse(text)
				if err != nil {
					t.Fatal(err)
				}
				options.Set("lifecycleLedger", ledger)
			}
		}
		switch r.IntN(4) {
		case 0:
			options.Set("codex", jsjson.MustObject("witnessDraftThreadId", jsjson.MakeString("thread-draft"), "repairThreadId", jsjson.MakeString("thread:repair-1")))
			label += " codex"
		case 1:
			options.Set("codex", jsjson.MustObject("repairThreadId", jsjson.MakeString("thread-repair")))
			label += " codex:repair"
		case 2:
			options.Set("codex", jsjson.MustObject())
			label += " codex:empty"
		}
		if r.IntN(2) == 0 {
			options.Set("generatedAt", jsjson.MakeString("2026-07-16T11:03:00.000Z"))
		}
		cases = append(cases, writerCase{label: label, result: result, frozen: ic.request.Get("frozenWitness"), options: jsjson.MakeObject(options)})
	}

	t.Setenv("PATH", hostPath)
	const seed = "live-writer"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	// tsx's startup Date read (see TestLiveInvestigate).
	oracleCall(t, c, "echo", "null")
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tsRoot, goRoot := filepath.Join(work, "ts"), filepath.Join(work, "go")
	path := strings.Join([]string{filepath.Join(root, "difftest/testdata/recorder"), filepath.Dir(gitPath), "/usr/bin", "/bin"}, ":")
	var outputs strings.Builder
	var published []gitproof.Written
	n := 0
	// compare writes one case on both sides: output and proofRoot are the
	// TS paths (under tsRoot, or named tsRoot+suffix); the Go side uses
	// them with goRoot in place of tsRoot.
	compare := func(label, output, proofRoot string, result, frozen, options jsjson.Value) (gitproof.Written, error) {
		i := n
		n++
		if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
			t.Fatalf("case %d: oracle clock at %s, Go clock at %d", i, ts, goTick)
		}
		toGo := func(s string) string { return strings.ReplaceAll(s, tsRoot, goRoot) }
		name := strconv.Itoa(i)
		tsLog, goLog := filepath.Join(work, name+"-ts.log"), filepath.Join(work, name+"-go.log")
		tsOptions := jsjson.NewObj()
		tsOptions.Set("proofRoot", jsjson.MakeString(proofRoot))
		for _, k := range options.Obj().Keys() {
			tsOptions.Set(k, options.Obj().Field(k))
		}
		want := oracleText(t, c, "writeGitInvestigationProofBundle", `{"output":`+jsjson.Quote(output)+
			`,"result":`+jsjson.Stringify(result)+`,"frozen":`+jsjson.Stringify(frozen)+`,"options":`+jsjson.Stringify(jsjson.MakeObject(tsOptions))+
			`,"path":`+jsjson.Quote(path)+`,"argvLog":`+jsjson.Quote(tsLog)+`}`)

		o := gitproof.WriteOptions{ProofRoot: toGo(proofRoot)}
		if v := options.Get("generatedAt"); v.Kind() == jsjson.String {
			s := v.Str()
			o.GeneratedAt = &s
		}
		if v := options.Get("lifecycleLedger"); v.Kind() == jsjson.Object {
			o.LifecycleLedger = &v
		}
		if v := options.Get("codex"); v.Kind() == jsjson.Object {
			o.Codex = &gitproof.CodexThreadIDs{}
			if d := v.Get("witnessDraftThreadId"); d.Kind() == jsjson.String {
				s := d.Str()
				o.Codex.WitnessDraftThreadID = &s
			}
			if d := v.Get("repairThreadId"); d.Kind() == jsjson.String {
				s := d.Str()
				o.Codex.RepairThreadID = &s
			}
		}
		t.Setenv("PATH", path)
		t.Setenv("FAULTLINE_ARGV_LOG", goLog)
		w, err := gitproof.WriteGitInvestigationProofBundle(toGo(output), result, frozen, o)
		os.Unsetenv("FAULTLINE_ARGV_LOG")
		var got string
		if err != nil {
			got = "THREW:" + err.Error()
		} else {
			view := jsjson.NewObj()
			view.Set("directory", jsjson.MakeString(w.Directory))
			view.Set("manifestDigest", jsjson.MakeString(w.ManifestDigest))
			view.Set("rootDigest", jsjson.MakeString(w.RootDigest))
			view.Set("manifest", w.Manifest)
			got = jsjson.Stringify(jsjson.MakeObject(view))
		}
		outputs.WriteString(jsjson.Quote(got))
		if norm := toGo(want); got != norm {
			t.Fatalf("case %d (%s):\ngot  %s\nwant %s", i, label, got, norm)
		}
		tsRecords, _ := os.ReadFile(tsLog)
		goRecords, _ := os.ReadFile(goLog)
		if norm := toGo(string(tsRecords)); norm != string(goRecords) {
			g, w := strings.Split(string(goRecords), "\x1e"), strings.Split(norm, "\x1e")
			for k := 0; k < min(len(g), len(w)); k++ {
				if g[k] != w[k] {
					t.Fatalf("case %d (%s): git argv record %d differs:\ngo %q\nts %q", i, label, k, g[k], w[k])
				}
			}
			t.Fatalf("case %d (%s): git argv: go %d records, ts %d; last go %q, last ts %q", i, label, len(g), len(w), g[len(g)-1], w[len(w)-1])
		}
		if err != nil {
			t.Logf("case %d (%s): %s", i, label, got)
			return w, err
		}
		if a, b := treeDigest(w.Directory), treeDigest(output); a != b {
			t.Fatalf("case %d (%s): bundle trees differ at %s", i, label, firstDiffering(t, w.Directory, output))
		}
		// Verify draws a seeded mkdtemp suffix the oracle does not, so the
		// written bundles are re-verified after the comparisons.
		published = append(published, w)
		t.Logf("case %d (%s): written, lifecycle %s, %d runs", i, label, w.Manifest.Get("lifecycle", "status").Str(), len(result.Get("runs").Items()))
		return w, nil
	}

	written, refused := 0, map[string]int{}
	var base *writerCase // a written case, reused by the refusals below
	baseOutput := ""
	var longest *writerCase // a written case with the most states
	for i := range cases {
		wc := &cases[i]
		output := filepath.Join(tsRoot, strconv.Itoa(i), "out")
		if _, err := compare(wc.label, output, tsRoot, wc.result, wc.frozen, wc.options); err != nil {
			refused[strings.SplitN(err.Error(), ":", 2)[0]]++
			continue
		}
		written++
		if base == nil {
			base, baseOutput = wc, output
		}
		if longest == nil || len(wc.result.Get("states").Items()) > len(longest.result.Get("states").Items()) {
			longest = wc
		}
	}
	t.Logf("%d written, refused %v", written, refused)
	if base == nil {
		t.Fatal("no generated case was written")
	}

	// Refusals the generator does not produce.
	none := jsjson.MustObject()
	compare("existing output", baseOutput, tsRoot, base.result, base.frozen, none)
	compare("outside the proof root", filepath.Join(tsRoot+"-outside", "out"), tsRoot, base.result, base.frozen, none)
	for _, r := range []string{tsRoot, goRoot} {
		if err := os.Symlink(r, r+"-link"); err != nil {
			t.Fatal(err)
		}
	}
	compare("symlinked proof root", filepath.Join(tsRoot+"-link", "nested", "out"), filepath.Join(tsRoot+"-link", "nested"), base.result, base.frozen, none)
	unsafe, _ := jsjson.Parse(jsjson.Stringify(base.result))
	unsafe.Get("runs").Items()[0].Get("result").Obj().Set("executor", jsjson.MakeString("UNSAFE_LOCAL"))
	compare("unsafe run", filepath.Join(tsRoot, "unsafe", "out"), tsRoot, unsafe, base.frozen, none)
	// Replacement objects change what the source repository reports for the
	// recorded range (Git honours refs/replace in the writer's own calls).
	gitIn := func(repo string, args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, rc := range []struct {
		label string
		wc    *writerCase
		args  func(a, d string) []string
	}{
		{"descendant grafted without parents", base, func(a, d string) []string { return []string{"replace", "--graft", d} }},
		{"descendant grafted onto the ancestor", longest, func(a, d string) []string { return []string{"replace", "--graft", d, a} }},
		{"descendant replaced by the ancestor", base, func(a, d string) []string { return []string{"replace", d, a} }},
	} {
		if len(rc.wc.result.Get("states").Items()) < 3 && rc.wc == longest {
			continue // grafting a child onto its own parent changes nothing
		}
		repo := rc.wc.result.Get("repository").Str()
		a, d := rc.wc.result.Get("resolvedRange", "ancestor", "commit").Str(), rc.wc.result.Get("resolvedRange", "descendant", "commit").Str()
		gitIn(repo, rc.args(a, d)...)
		compare(rc.label, filepath.Join(tsRoot, strings.ReplaceAll(rc.label, " ", "-"), "out"), tsRoot, rc.wc.result, rc.wc.frozen, none)
		gitIn(repo, "replace", "-d", d)
	}
	if len(longest.result.Get("states").Items()) < 3 {
		t.Errorf("no written case has three states, so the grafted range keeps its states")
	}
	if written == 0 || len(refused) == 0 {
		t.Errorf("want both written and refused cases")
	}
	for _, w := range published {
		if v := gitproof.Verify(w.Directory, w.RootDigest, true); !v.Valid {
			t.Errorf("%s: %v", w.Directory, v.Errors)
		}
	}
	requireReached(t, "gitproof/write.go", outputs.String())
}
