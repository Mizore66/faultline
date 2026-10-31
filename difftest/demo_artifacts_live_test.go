//go:build faultline_test

package difftest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/demoartifactstest"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Live reconstruction of the Slice 5b.1 cached-artifact corpus against the
// frozen TS (actual pinned Node, the Task 1 oracle adapter). Each test first
// requires TS to reproduce the committed capture, then Go to reproduce TS.
// The permanent replay (demo_artifacts_test.go) needs none of this.

const demoLiveSeed = "demo-artifacts-v1"

type demoLiveEnv struct{ repo, node string }

// demoLivePreflight fails (it never skips: the caller already passed
// oracle.Skip) unless the pinned runtime, manifest, built CLI and adapter are
// exactly the ones the evidence was captured with.
func demoLivePreflight(t *testing.T) demoLiveEnv {
	t.Helper()
	repo := oracle.RepoRoot(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	stdout, stderr, code := runCLIHelpProcess(t, node, outside, os.Environ(), "--version")
	if stdout != "v22.22.2\n" || stderr != "" || code != 0 {
		t.Fatalf("oracle Node drift: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
	}
	raw, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Version any }
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Version != "0.1.4" {
		t.Fatalf("oracle manifest drift: %#v (%v)", manifest.Version, err)
	}
	for _, rel := range []string{"dist/cli.js", "dist/engine.js", "dist/proof-bundle.js", "dist/judge-preview.js", "difftest/gen/demo-artifacts-oracle.mjs", "difftest/gen/seed.mjs"} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("oracle input missing: %v", err)
		}
	}
	stdout, stderr, code = runCLIHelpProcess(t, node, outside, os.Environ(), filepath.Join(repo, "dist", "cli.js"), "--version")
	if stdout != "FaultLine 0.1.4\n" || stderr != "" || code != 0 {
		t.Fatalf("oracle CLI-version drift: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
	}
	return demoLiveEnv{repo: repo, node: node}
}

func demoFileURL(p string) string { return "file://" + filepath.ToSlash(p) }

func demoLiveNodeCommand(env demoLiveEnv, dir string, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	gen := filepath.Join(env.repo, "difftest", "gen")
	full := append([]string{"--import", demoFileURL(filepath.Join(gen, "seed.mjs"))}, args...)
	cmd := exec.CommandContext(ctx, env.node, full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FAULTLINE_TEST_SEED="+demoLiveSeed)
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out: %w", err)
	}
	return out.Bytes(), errb.Bytes(), err
}

// demoLiveMask masks only what the generator masks: the case-root prefix in
// the call error, in the writer result's directory member, and in the CLI
// streams. Nothing else is touched.
func demoLiveMask(c demoartifactstest.Capture, roots ...string) (demoartifactstest.Capture, error) {
	mask := func(s string) string {
		for _, r := range roots {
			s = strings.ReplaceAll(s, r, demoTmp)
		}
		return s
	}
	out := c
	if c.Call != nil {
		call := *c.Call
		if call.Error != nil {
			m := mask(*call.Error)
			call.Error = &m
		}
		if call.ValueJSON != nil && c.Request.Kind == "writer" {
			v, err := jsjson.Parse(*call.ValueJSON)
			if err != nil {
				return out, err
			}
			if err := demoObjectKeys(v, "writer result", []string{"directory", "manifestDigest", "rootDigest"}); err != nil {
				return out, err
			}
			dir := mask(v.Get("directory").Str())
			if !strings.HasPrefix(dir, demoTmp) {
				return out, fmt.Errorf("%s: directory %q has no case-root prefix", c.Request.ID, v.Get("directory").Str())
			}
			v.Obj().Set("directory", jsjson.MakeString(dir))
			s := jsjson.Stringify(v)
			call.ValueJSON = &s
		}
		out.Call = &call
	}
	if c.Process != nil {
		p := *c.Process
		p.Stdout, p.Stderr = mask(p.Stdout), mask(p.Stderr)
		out.Process = &p
	}
	return out, nil
}

// demoLiveRunTS performs one request with the frozen TS adapter in a fresh
// case root and returns its masked capture and the (unmasked) root.
func demoLiveRunTS(t *testing.T, env demoLiveEnv, req demoartifactstest.Request) (demoartifactstest.Capture, string) {
	t.Helper()
	caseDir := t.TempDir()
	root := filepath.Join(caseDir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	reqFile := filepath.Join(caseDir, "request.json")
	if err := os.WriteFile(reqFile, body, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := demoLiveNodeCommand(env, caseDir, filepath.Join(env.repo, "difftest", "gen", "demo-artifacts-oracle.mjs"), reqFile, real)
	if err != nil {
		t.Fatalf("TS adapter %s: %v\n%s", req.ID, err, stderr)
	}
	v, err := jsjson.Parse(string(stdout))
	if err != nil {
		t.Fatalf("TS adapter %s output: %v", req.ID, err)
	}
	raw, err := demoCaptureFromValue(v, false)
	if err != nil {
		t.Fatalf("TS adapter %s capture: %v", req.ID, err)
	}
	if demoRequestText(raw.Request) != demoRequestText(req) {
		t.Fatalf("TS adapter echoed a different request: %s", demoRequestText(raw.Request))
	}
	masked, err := demoLiveMask(raw, real, root)
	if err != nil {
		t.Fatal(err)
	}
	return masked, real
}

// demoLiveCompare is the shared two-step live check for one captured request:
// TS must equal the committed capture, then Go must equal TS (except the one
// approved dangling-link policy). It returns the masked TS capture, the TS
// root, the Go root and Go's capture.
func demoLiveCompare(t *testing.T, env demoLiveEnv, committed demoartifactstest.Capture) (ts demoartifactstest.Capture, tsRoot, goRoot string, got demoartifactstest.Capture) {
	t.Helper()
	req := committed.Request
	ts, tsRoot = demoLiveRunTS(t, env, req)
	if d := diffDemoCaptures(committed, ts); len(d) != 0 {
		t.Fatalf("TS oracle drift for %s (live TS != committed capture):\n%s", req.ID, strings.Join(d, "\n"))
	}
	goRoot = demoCaseRoot(t)
	setup := demoPrepare(t, goRoot, env.repo, req)
	if d := diffDemoInventory("setup", ts.Before, setup); len(d) != 0 {
		t.Fatalf("Go setup differs from TS before: %v", d)
	}
	got = invokeDemoCase(t, goRoot, env.repo, req)
	if req.ID == "cli-dangling-output" {
		// Raw TS followed the link; Go refuses by approved policy. Both are
		// asserted exactly and never merged.
		checkDemoDanglingGo(t, goRoot, ts, got)
		return
	}
	want, err := materializeDemoCapture(ts, goRoot)
	if err != nil {
		t.Fatal(err)
	}
	if d := diffDemoCaptures(want, got); len(d) != 0 {
		t.Fatalf("Go differs from live TS for %s:\n%s", req.ID, strings.Join(d, "\n"))
	}
	return
}

// TestLiveDemoArtifacts owns the 26 analysis/render/CLI requests. Its own
// renderer refusals feed the reach check of the preview renderer, which has no
// CLI input for its non-REPLAY guard.
func TestLiveDemoArtifacts(t *testing.T) {
	oracle.Skip(t)
	env := demoLivePreflight(t)
	var outputs strings.Builder
	ran := 0
	for _, committed := range loadDemoArtifactCases(t) {
		if committed.Request.Kind == "writer" {
			continue
		}
		committed := committed
		ran++
		t.Run(committed.Request.ID, func(t *testing.T) {
			ts, _, _, _ := demoLiveCompare(t, env, committed)
			if ts.Request.Kind == "render" && ts.Call != nil && ts.Call.Error != nil {
				outputs.WriteString(jsjson.Quote(*ts.Call.Error) + "\n")
			}
		})
	}
	if ran != 26 {
		t.Fatalf("live non-writer requests = %d, want 26", ran)
	}
	requireReached(t, "internal/proofview/judge_preview.go", outputs.String())
	liveSummary(t, "%d analysis/render/CLI requests: TS equals the committed capture and Go equals TS (dangling-link policy asserted separately)", ran)
}

// ---- writer verification ----------------------------------------------------

type demoVerdict struct {
	out  string
	code int
}

func (v demoVerdict) String() string { return fmt.Sprintf("exit %d: %q", v.code, v.out) }

// demoVerifyAll verifies one bundle with every verifier and returns each
// verdict with the bundle path masked: the frozen TS verifier and the Go fl
// verifier (both with the bundle's own retained root as --expect-root) and the
// library demo.Verify.
func demoVerifyAll(t *testing.T, ts func(string) (string, int), dir string) (tsV, goV demoVerdict, lib bool, libErrors []string) {
	t.Helper()
	mask := func(s string) string { return strings.ReplaceAll(s, dir, "<BUNDLE>") }
	o, c := ts(dir)
	tsV = demoVerdict{mask(o), c}
	o, c = goVerify(t)(dir)
	goV = demoVerdict{mask(o), c}
	root := expectedRoot(t, dir)
	r := demo.Verify(dir, root, true)
	return tsV, goV, r.Valid, r.Errors
}

// demoWriterVerdict is the verifier outcome pinned for one successful writer
// request: the exact `fl verify --expect-root <the bundle's own root>` text
// (same for the frozen TS and the Go verifier) and the library result.
type demoWriterVerdict struct {
	code      int
	out       string
	libValid  bool
	libErrors []string
}

func demoVerdictValid(files int, root string) demoWriterVerdict {
	return demoWriterVerdict{0, fmt.Sprintf("Integrity: VALID\nDeclared files checked: %d\nBundle root: %s\nExternal root: MATCH\n", files, root), true, nil}
}

func demoVerdictInvalid(files int, root, reason string) demoWriterVerdict {
	return demoWriterVerdict{1, fmt.Sprintf("Integrity: INVALID\nDeclared files checked: %d\nBundle root: %s\nExternal root: MATCH\n- %s\n", files, root, reason), false, []string{reason}}
}

// demoExpectedWriterVerdicts are the already observed pinned-Node v22.22.2
// verdicts (final stream 1793395078410290000-t5-live-writer-container3.stdout:
// frozen TS on its own bundle, TS on the Go bundle, Go on the Go bundle and
// the library all gave these texts). They are literal assertions against
// independently observed outcomes, not a new permanent frozen-TS capture, and
// nothing here claims validity for the two inputs the verifier rejects.
var demoExpectedWriterVerdicts = map[string]demoWriterVerdict{
	"writer-replay":      demoVerdictValid(67, "sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa"),
	"writer-rerun":       demoVerdictValid(124, "sha256:408bc1c3ebdf71c785b6192b34f465d4e00ec481c91a2283f62c7517ebcb0a37"),
	"writer-unicode":     demoVerdictValid(124, "sha256:150a1d251d5f72ca81680b7716c47950f130c3d5eaa9cb7298285bb981e1453c"),
	"writer-replace":     demoVerdictValid(124, "sha256:408bc1c3ebdf71c785b6192b34f465d4e00ec481c91a2283f62c7517ebcb0a37"),
	"writer-custom-root": demoVerdictValid(67, "sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa"),
	"writer-empty-root":  demoVerdictValid(67, "sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa"),
	"writer-lone-surrogate": demoVerdictInvalid(124, "sha256:64f79a696a9c7c5bc6d6e7b39b9e752ffcc685457167b4f0586fea2e4a983fef",
		"stdout does not match catalog for sha256:0000dcd7125bda852aa1fa33d69b42f900c462e79568d2a6f95f74d4051334f1"),
	"writer-extra-keys": demoVerdictInvalid(67, "sha256:8f8b11b7dd3b1dafb14a74fb7fd8d93e7df865430d9a1f98c0fe6940e8e866b0",
		"manifest analysisDigest does not match analysis.json"),
}

// checkDemoWriterVerdicts compares every verifier's actual outcome for one
// request with the pinned literal. tsOnTS and goOnTS verify the TS-written
// bundle, tsOnGo, goOnGo and libGo the Go-written one (libTS is the library on
// the TS-written bundle). A request without a pinned entry fails.
func checkDemoWriterVerdicts(id string, tsOnTS, goOnTS, tsOnGo, goOnGo demoVerdict, libTS, libGo bool, libGoErrors []string) error {
	want, ok := demoExpectedWriterVerdicts[id]
	if !ok {
		return fmt.Errorf("%s has no pinned verifier verdict", id)
	}
	for name, got := range map[string]demoVerdict{"TS on TS bundle": tsOnTS, "Go on TS bundle": goOnTS, "TS on Go bundle": tsOnGo, "Go on Go bundle": goOnGo} {
		if got.code != want.code || got.out != want.out {
			return fmt.Errorf("%s: %s gave %v, pinned exit %d %q", id, name, got, want.code, want.out)
		}
	}
	if libTS != want.libValid || libGo != want.libValid {
		return fmt.Errorf("%s: library valid TS-bundle=%v Go-bundle=%v, pinned %v", id, libTS, libGo, want.libValid)
	}
	if strings.Join(libGoErrors, "\n") != strings.Join(want.libErrors, "\n") {
		return fmt.Errorf("%s: library errors %q, pinned %q", id, libGoErrors, want.libErrors)
	}
	return nil
}

// TestDemoWriterVerdictTable needs no Node. It checks that the pinned verdict
// table covers exactly the successful writer requests of the committed corpus
// and that the checker fails on each kind of drift, so the live assertions
// cannot pass vacuously.
func TestDemoWriterVerdictTable(t *testing.T) {
	var successful []string
	for _, c := range loadDemoArtifactCases(t) {
		if c.Request.Kind == "writer" && c.Call.ValueJSON != nil {
			successful = append(successful, c.Request.ID)
		}
	}
	if len(successful) != len(demoExpectedWriterVerdicts) || len(successful) != 8 {
		t.Fatalf("successful writer requests %v vs %d pinned verdicts", successful, len(demoExpectedWriterVerdicts))
	}
	for _, id := range successful {
		if _, ok := demoExpectedWriterVerdicts[id]; !ok {
			t.Errorf("%s has no pinned verdict", id)
		}
	}
	asVerdict := func(w demoWriterVerdict) demoVerdict { return demoVerdict{w.out, w.code} }
	pass := func(id string, mutate func(ts, gs, tg, gg *demoVerdict, libTS, libGo *bool, errs *[]string)) error {
		w := demoExpectedWriterVerdicts[id]
		a, b, c, d := asVerdict(w), asVerdict(w), asVerdict(w), asVerdict(w)
		libTS, libGo := w.libValid, w.libValid
		errs := append([]string(nil), w.libErrors...)
		mutate(&a, &b, &c, &d, &libTS, &libGo, &errs)
		return checkDemoWriterVerdicts(id, a, b, c, d, libTS, libGo, errs)
	}
	none := func(*demoVerdict, *demoVerdict, *demoVerdict, *demoVerdict, *bool, *bool, *[]string) {}
	for _, id := range successful {
		if err := pass(id, none); err != nil {
			t.Fatalf("the pinned outcome itself must pass: %v", err)
		}
	}
	// custom-root and empty-root are pinned VALID, lone-surrogate and extra-keys INVALID.
	for id, valid := range map[string]bool{"writer-custom-root": true, "writer-empty-root": true, "writer-lone-surrogate": false, "writer-extra-keys": false} {
		if demoExpectedWriterVerdicts[id].libValid != valid || (demoExpectedWriterVerdicts[id].code == 0) != valid {
			t.Errorf("%s pinned validity is not %v", id, valid)
		}
	}
	drifts := map[string]func(ts, gs, tg, gg *demoVerdict, libTS, libGo *bool, errs *[]string){
		"TS-on-TS exit flipped": func(ts, _, _, _ *demoVerdict, _, _ *bool, _ *[]string) { ts.code ^= 1 },
		"Go-on-TS text changed": func(_, gs, _, _ *demoVerdict, _, _ *bool, _ *[]string) { gs.out += " " },
		"TS-on-Go text changed": func(_, _, tg, _ *demoVerdict, _, _ *bool, _ *[]string) { tg.out = "Integrity: VALID\n" },
		"Go-on-Go exit flipped": func(_, _, _, gg *demoVerdict, _, _ *bool, _ *[]string) { gg.code ^= 1 },
		"library on Go flipped": func(_, _, _, _ *demoVerdict, _, libGo *bool, _ *[]string) { *libGo = !*libGo },
		"library on TS flipped": func(_, _, _, _ *demoVerdict, libTS, _ *bool, _ *[]string) { *libTS = !*libTS },
		"library error added":   func(_, _, _, _ *demoVerdict, _, _ *bool, errs *[]string) { *errs = append(*errs, "extra") },
		"both sides drift in step": func(ts, gs, tg, gg *demoVerdict, _, _ *bool, _ *[]string) {
			for _, v := range []*demoVerdict{ts, gs, tg, gg} {
				v.code, v.out = 0, "Integrity: VALID\nDeclared files checked: 1\n"
			}
		},
	}
	for _, id := range []string{"writer-empty-root", "writer-lone-surrogate"} {
		for name, drift := range drifts {
			if pass(id, drift) == nil {
				t.Errorf("%s: %q was accepted", id, name)
			}
		}
	}
	if checkDemoWriterVerdicts("writer-unpinned", demoVerdict{}, demoVerdict{}, demoVerdict{}, demoVerdict{}, true, true, nil) == nil {
		t.Error("an unpinned request was accepted")
	}
}

// ---- supplemental supplied-value table --------------------------------------

type demoOp struct {
	Op    string          `json:"op"`
	Path  []string        `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
	From  int             `json:"from"` // index 0 must reach the fixed script
	Key   string          `json:"key,omitempty"`
}

type demoSupplement struct {
	Name string   `json:"name"`
	Ops  []demoOp `json:"ops"`
}

func opSet(path, valueJSON string) demoOp {
	return demoOp{Op: "set", Path: strings.Split(path, "."), Value: json.RawMessage(valueJSON)}
}

func opDel(path string) demoOp { return demoOp{Op: "delete", Path: strings.Split(path, ".")} }

// opClone appends a copy of catalog[from] with key set to valueJSON (deleted
// when valueJSON is empty).
func opClone(path string, from int, key, valueJSON string) demoOp {
	op := demoOp{Op: "clone-push", Path: strings.Split(path, "."), From: from, Key: key}
	if valueJSON != "" {
		op.Value = json.RawMessage(valueJSON)
	}
	return op
}

// sup builds a case; Ops is never nil so it encodes as [] for the fixed script.
func sup(name string, ops ...demoOp) demoSupplement {
	if ops == nil {
		ops = []demoOp{}
	}
	return demoSupplement{Name: name, Ops: ops}
}

// demoSupplements are malformed or unusual supplied analyses (the retained
// historical RERUN input, one JSON-derived operation set each) that reach the
// writer's own property reads, iteration, coercion and hashing. They are the
// operations Task 3's native tests pin; here the frozen writeProofBundle and
// the Go writer both run them and must agree on the exact error text (or
// result) and on the complete resulting tree.
var demoSupplements = []demoSupplement{
	sup("baseline"),
	sup("state-ids-array-object", opSet("transitions.0.beforeStateId", `["cedar","t4",null,["n",1]]`), opSet("transitions.0.afterStateId", `{"a":1}`)),
	sup("state-ids-numbers", opSet("transitions.0.beforeStateId", `1e21`), opSet("transitions.0.afterStateId", `-0`)),
	sup("state-ids-null-bool", opSet("transitions.0.beforeStateId", `null`), opSet("transitions.0.afterStateId", `true`)),
	sup("state-ids-missing", opDel("transitions.0.beforeStateId"), opSet("transitions.0.afterStateId", `"str"`)),
	sup("stable-truthy-string", opSet("transitions.0.stable", `"yes"`), opSet("transitions.0.beforeStateId", `"first-chosen"`)),
	sup("stable-truthy-array", opSet("transitions.0.stable", `[]`), opSet("transitions.0.beforeStateId", `"first-chosen"`)),
	sup("stable-falsy-empty-string", opSet("transitions.0.stable", `""`), opSet("transitions.0.beforeStateId", `"skipped"`)),
	sup("stable-falsy-null", opSet("transitions.0.stable", `null`), opSet("transitions.0.beforeStateId", `"skipped"`)),
	sup("kind-nonstring", opSet("transitions.0.kind", `["PASS_TO_FAIL"]`), opSet("transitions.0.beforeStateId", `"skipped"`)),
	sup("title-mode-digest-verdicts", opSet("fixture.title", `["a","b"]`), opSet("mode", `null`), opSet("witness.digest", `["d"]`),
		opSet("prevention.lastGood.verdict", `7`), opSet("prevention.firstBad.verdict", `[1,[2]]`), opSet("prevention.repaired.verdict", `{}`)),
	sup("title-missing", opDel("fixture.title")),
	sup("fixture-id-number", opSet("fixture.id", `5`)),
	sup("fixture-id-array", opSet("fixture.id", `["a","b"]`)),
	sup("fixture-id-object", opSet("fixture.id", `{"k":1}`)),
	sup("fixture-id-null", opSet("fixture.id", `null`)),
	sup("fixture-id-missing", opDel("fixture.id")),
	sup("fixture-deleted", opDel("fixture")),
	sup("fixture-null", opSet("fixture", `null`)),
	sup("witness-deleted", opDel("witness")),
	sup("witness-null", opSet("witness", `null`)),
	sup("stdout-number", opSet("runCatalog.0.stdout", `5`)),
	sup("stdout-missing", opDel("runCatalog.0.stdout")),
	sup("stdout-null", opSet("runCatalog.0.stdout", `null`)),
	sup("stdout-array", opSet("runCatalog.0.stdout", `["x"]`)),
	sup("stderr-object", opSet("runCatalog.0.stderr", `{"x":1}`)),
	sup("stderr-bool-true", opSet("runCatalog.0.stderr", `true`)),
	sup("stderr-bool-false", opSet("runCatalog.0.stderr", `false`)),
	sup("prevention-deleted", opDel("prevention")),
	sup("prevention-null", opSet("prevention", `null`)),
	sup("lastGood-deleted", opDel("prevention.lastGood")),
	sup("firstBad-deleted", opDel("prevention.firstBad")),
	sup("firstBad-null", opSet("prevention.firstBad", `null`)),
	sup("repaired-deleted", opDel("prevention.repaired")),
	sup("minimization-deleted", opDel("minimization")),
	sup("minimization-null", opSet("minimization", `null`)),
	sup("sufficiency-deleted", opDel("minimization.sufficiency")),
	sup("necessity-null", opSet("minimization.necessity", `null`)),
	sup("contributionRuns-deleted", opDel("contributionRuns")),
	sup("contributionRuns-null", opSet("contributionRuns", `null`)),
	sup("contributionRuns-number", opSet("contributionRuns", `5`)),
	sup("contributionRuns-object", opSet("contributionRuns", `{}`)),
	sup("contributionRuns-string", opSet("contributionRuns", `"ab"`)),
	sup("contributionRuns-null-element", opSet("contributionRuns.0", `null`)),
	// A referenced run whose id is an object or an array is reported by its
	// ToString in the coverage refusal ("[object Object]", "a,b").
	sup("contribution-id-object", opSet("contributionRuns.0.id", `{"k":1}`)),
	sup("contribution-id-array", opSet("contributionRuns.0.id", `["a",null,["b"]]`)),
	sup("timelineRuns-deleted", opDel("timelineRuns")),
	sup("timelineRuns-bool", opSet("timelineRuns", `true`)),
	sup("transitions-deleted", opDel("transitions")),
	sup("transitions-null", opSet("transitions", `null`)),
	sup("transitions-number", opSet("transitions", `7`)),
	// A string is iterable, so the empty one passes coverage collection; the
	// report's transitions.find then throws before any stage or write.
	sup("transitions-empty-string", opSet("transitions", `""`)),
	sup("transition-null-element", opSet("transitions.0", `null`)),
	sup("boundaryRunIds-deleted", opDel("transitions.0.boundaryRunIds")),
	sup("boundaryRunIds-number", opSet("transitions.0.boundaryRunIds", `3`)),
	sup("boundaryRunIds-string", opSet("transitions.0.boundaryRunIds", `"xy"`)),
	sup("attempts-deleted", opDel("minimization.attempts")),
	sup("attempts-null", opSet("minimization.attempts", `null`)),
	sup("attempts-number", opSet("minimization.attempts", `2`)),
	sup("attempt-null-element", opSet("minimization.attempts.0", `null`)),
	sup("attempt-runId-number", opSet("minimization.attempts.0.runId", `5`)),
	sup("attempt-runId-zero", opSet("minimization.attempts.0.runId", `0`)),
	sup("runCatalog-deleted", opDel("runCatalog")),
	sup("runCatalog-null", opSet("runCatalog", `null`)),
	sup("runCatalog-number", opSet("runCatalog", `5`)),
	sup("runCatalog-object", opSet("runCatalog", `{}`)),
	sup("catalog-null-element", opSet("runCatalog.0", `null`)),
	sup("catalog-id-deleted", opDel("runCatalog.0.id")),
	sup("catalog-id-number", opSet("runCatalog.0.id", `5`)),
	sup("catalog-id-array", opSet("runCatalog.0.id", `["a"]`)),
	sup("catalog-id-object", opSet("runCatalog.0.id", `{"k":1}`)),
	sup("order-prevention-vs-null-contribution", opDel("prevention"), opSet("contributionRuns.0", `null`)),
	sup("order-notiterable-vs-prevention", opSet("contributionRuns", `5`), opDel("prevention")),
	sup("order-timeline-vs-contribution", opSet("contributionRuns", `5`), opSet("timelineRuns", `6`)),
	sup("order-catalog-vs-contribution", opSet("runCatalog", `null`), opSet("contributionRuns", `5`)),
	sup("order-firstBad-vs-attempt", opSet("prevention.firstBad", `null`), opSet("minimization.attempts.0", `null`)),
	sup("order-firstBad-vs-transition", opSet("prevention.firstBad", `null`), opSet("transitions.0", `null`)),
	sup("order-transition-vs-attempts", opSet("transitions.0", `null`), opSet("minimization.attempts", `2`)),
	sup("order-catalog-element-vs-prevention", opSet("runCatalog.0", `null`), opDel("prevention")),
	sup("order-fixture-vs-coverage", opDel("fixture"), opDel("prevention.firstBad")),
	sup("order-coverage-vs-body", opDel("prevention.firstBad"), opSet("runCatalog.0.stdout", `5`)),
	sup("order-id-vs-body", opSet("runCatalog.0.id", `5`), opSet("runCatalog.1.stdout", `5`)),
	sup("extra-id-ok-string", opClone("runCatalog", 0, "id", `"zz-extra"`)),
	sup("extra-id-deleted", opClone("runCatalog", 0, "id", "")),
	sup("extra-id-null", opClone("runCatalog", 0, "id", `null`)),
	sup("extra-id-number", opClone("runCatalog", 0, "id", `5`)),
	sup("extra-id-array", opClone("runCatalog", 0, "id", `["a"]`)),
	sup("extra-id-object", opClone("runCatalog", 0, "id", `{"k":1}`)),
	sup("extra-id-bool", opClone("runCatalog", 0, "id", `true`)),
	sup("extra-id-number-then-bad-body-later", opClone("runCatalog", 0, "id", `5`), opSet("runCatalog.1.stdout", `5`)),
	sup("bad-body-early-then-id-number", opSet("runCatalog.0.stdout", `5`), opClone("runCatalog", 0, "id", `5`)),
	sup("extra-id-number-and-duplicate", opClone("runCatalog", 0, "id", `5`), opClone("runCatalog", 0, "id", `5`)),
}

// demoSupplementTS is a fixed, test-private script. It reads one data file
// (the table above), applies each case's operations (set, delete, clone-push)
// to the retained historical RERUN analysis and calls the real frozen
// writeProofBundle once per case in that case's own fresh root. It runs no
// command and no code from the data.
const demoSupplementTS = `import { readFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const [, , dataPath, baseDir, repo] = process.argv;
const data = JSON.parse(readFileSync(dataPath, "utf8"));
const { writeProofBundle } = await import(pathToFileURL(repo + "/dist/proof-bundle.js").href);
const baseText = readFileSync(repo + "/difftest/testdata/bases/demo-rerun/analysis.json", "utf8");
const navigate = (analysis, path) => path.reduce((value, key) => value[key], analysis);
const results = [];
data.cases.forEach((c, i) => {
  const root = join(baseDir, String(i), "root");
  mkdirSync(root, { recursive: true });
  process.chdir(root);
  const analysis = JSON.parse(baseText);
  for (const op of c.ops) {
    if (op.op === "set") navigate(analysis, op.path.slice(0, -1))[op.path.at(-1)] = op.value;
    else if (op.op === "delete") delete navigate(analysis, op.path.slice(0, -1))[op.path.at(-1)];
    else if (op.op === "clone-push") {
      const list = navigate(analysis, op.path);
      const copy = { ...structuredClone(list[op.from]) };
      if ("value" in op) copy[op.key] = op.value; else delete copy[op.key];
      list.push(copy);
    } else throw new Error("unknown op " + op.op);
  }
  try {
    results.push({ valueJSON: JSON.stringify(writeProofBundle(".faultline/bundles/demo", analysis, {})), error: null });
  } catch (error) {
    results.push({ valueJSON: null, error: error instanceof Error ? error.message : String(error) });
  }
});
process.stdout.write(JSON.stringify(results));
`

// applyDemoOps is the Go side of the same fixed operations on the ordered
// supplied analysis.
func applyDemoOps(t *testing.T, analysis jsjson.Value, ops []demoOp) {
	t.Helper()
	nav := func(path []string) jsjson.Value {
		v := analysis
		for _, k := range path {
			if v.Kind() == jsjson.Array {
				var i int
				if _, err := fmt.Sscanf(k, "%d", &i); err != nil {
					t.Fatalf("array step %q", k)
				}
				v = v.Items()[i]
			} else {
				v = v.Get(k)
			}
		}
		return v
	}
	parse := func(raw json.RawMessage) jsjson.Value {
		v, err := jsjson.Parse(string(raw))
		if err != nil {
			t.Fatalf("op value %s: %v", raw, err)
		}
		return v
	}
	for _, op := range ops {
		switch op.Op {
		case "set":
			parent, last := nav(op.Path[:len(op.Path)-1]), op.Path[len(op.Path)-1]
			if parent.Kind() == jsjson.Array {
				var i int
				fmt.Sscanf(last, "%d", &i)
				parent.Items()[i] = parse(op.Value)
			} else {
				parent.Obj().Set(last, parse(op.Value))
			}
		case "delete":
			nav(op.Path[:len(op.Path)-1]).Obj().Delete(op.Path[len(op.Path)-1])
		case "clone-push":
			list := nav(op.Path)
			source := list.Items()[op.From]
			copy := jsjson.NewObj()
			for _, k := range source.Obj().Keys() {
				copy.Set(k, source.Obj().Field(k))
			}
			if op.Value != nil {
				copy.Set(op.Key, parse(op.Value))
			} else {
				copy.Delete(op.Key)
			}
			// The parent object holds the array: replace it with the extended one.
			parent := nav(op.Path[:len(op.Path)-1])
			parent.Obj().Set(op.Path[len(op.Path)-1], jsjson.MakeArray(append(append([]jsjson.Value(nil), list.Items()...), jsjson.MakeObject(copy))))
		default:
			t.Fatalf("unknown op %q", op.Op)
		}
	}
}

// goWriterCall runs the Go writer once in the current directory.
func goWriterCall(analysis jsjson.Value) demoartifactstest.Call {
	call := demoartifactstest.Call{}
	written, err := demo.Write(".faultline/bundles/demo", analysis, demo.WriteOptions{})
	if err != nil {
		m := jsexc.Message(err)
		call.Error = &m
		return call
	}
	s := jsjson.Stringify(jsjson.MustObject(
		"directory", jsjson.MakeString(written.Directory),
		"manifestDigest", jsjson.MakeString(written.ManifestDigest),
		"rootDigest", jsjson.MakeString(written.RootDigest)))
	call.ValueJSON = &s
	return call
}

// demoSupplementalWriter runs the supplemental table on the real frozen
// writer and the Go writer and returns the TS refusal messages as JSON-quoted
// output text for the reach check.
func demoSupplementalWriter(t *testing.T, env demoLiveEnv, outputs *strings.Builder) {
	t.Helper()
	data, err := json.Marshal(struct {
		Cases []demoSupplement `json:"cases"`
	}{demoSupplements})
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	dataFile, script, base := filepath.Join(scratch, "cases.json"), filepath.Join(scratch, "supplement.mjs"), filepath.Join(scratch, "ts")
	for path, body := range map[string][]byte{dataFile: data, script: []byte(demoSupplementTS)} {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	realBase, err := filepath.EvalSymlinks(scratch)
	if err != nil {
		t.Fatal(err)
	}
	base = filepath.Join(realBase, "ts")
	stdout, stderr, err := demoLiveNodeCommand(env, scratch, script, dataFile, base, env.repo)
	if err != nil {
		t.Fatalf("supplemental TS writer: %v\n%s", err, stderr)
	}
	var results []struct {
		ValueJSON *string `json:"valueJSON"`
		Error     *string `json:"error"`
	}
	if err := json.Unmarshal(stdout, &results); err != nil || len(results) != len(demoSupplements) {
		t.Fatalf("supplemental TS writer results: %v (%d)", err, len(results))
	}
	for i, c := range demoSupplements {
		i, c := i, c
		t.Run(c.Name, func(t *testing.T) {
			tsRoot := filepath.Join(base, fmt.Sprint(i), "root")
			ts := demoartifactstest.Call{ValueJSON: results[i].ValueJSON, Error: results[i].Error}
			tsTree, err := demoartifactstest.Inventory(tsRoot)
			if err != nil {
				t.Fatal(err)
			}
			goRoot := demoCaseRoot(t)
			analysis, err := jsjson.Parse(mustReadDemoBase(t, env.repo))
			if err != nil {
				t.Fatal(err)
			}
			applyDemoOps(t, analysis, c.Ops)
			t.Chdir(goRoot) // serial: cwd is process-wide, restored when the subtest ends
			got := goWriterCall(analysis)
			goTree, err := demoartifactstest.Inventory(goRoot)
			if err != nil {
				t.Fatal(err)
			}
			maskCall := func(call demoartifactstest.Call, root string) demoartifactstest.Capture {
				m, merr := demoLiveMask(demoartifactstest.Capture{Request: demoartifactstest.Request{ID: c.Name, Kind: "writer"}, Call: &call}, root)
				if merr != nil {
					t.Fatal(merr)
				}
				return m
			}
			want, have := maskCall(ts, tsRoot), maskCall(got, goRoot)
			if d := diffDemoCaptures(demoartifactstest.Capture{Request: want.Request, Call: want.Call, After: tsTree},
				demoartifactstest.Capture{Request: have.Request, Call: have.Call, After: goTree}); len(d) != 0 {
				t.Fatalf("Go differs from the frozen writeProofBundle for %s:\n%s", c.Name, strings.Join(d, "\n"))
			}
			if want.Call.Error != nil {
				outputs.WriteString(jsjson.Quote(*want.Call.Error) + "\n")
			}
		})
	}
}

func mustReadDemoBase(t *testing.T, repo string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", "bases", "demo-rerun", "analysis.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestLiveDemoWriter owns the 19 writer requests: TS equals the committed
// capture, Go equals TS, and every bundle Go wrote is verified by Go and by
// the frozen TS verifier with the same verdict TS gives its own bundle. It also
// owns the supplemental supplied-value table of the writer's own report and
// property reads.
func TestLiveDemoWriter(t *testing.T) {
	oracle.Skip(t)
	env := demoLivePreflight(t)
	tsVerifier := tsVerify(t)
	var outputs strings.Builder
	ran := 0
	for _, committed := range loadDemoArtifactCases(t) {
		if committed.Request.Kind != "writer" {
			continue
		}
		committed := committed
		ran++
		t.Run(committed.Request.ID, func(t *testing.T) {
			ts, tsRoot, _, got := demoLiveCompare(t, env, committed)
			if ts.Call.Error != nil {
				outputs.WriteString(jsjson.Quote(*ts.Call.Error) + "\n")
				return
			}
			// The returned directory in each root, from the masked results.
			tsWant, err := materializeDemoCapture(ts, tsRoot)
			if err != nil {
				t.Fatal(err)
			}
			tsDir, goDir := directoryOf(t, tsWant), directoryOf(t, got)
			tsTS, goTS, tsLib, _ := demoVerifyAll(t, tsVerifier, tsDir)
			goBundleTS, goBundleGo, goLib, goLibErrors := demoVerifyAll(t, tsVerifier, goDir)
			t.Logf("%s: TS verifies the TS bundle: %v; Go verifies the TS bundle: %v; TS verifies the Go bundle: %v; Go verifies the Go bundle: %v; library valid=%v errors=%v",
				committed.Request.ID, tsTS, goTS, goBundleTS, goBundleGo, goLib, goLibErrors)
			if err := checkDemoWriterVerdicts(committed.Request.ID, tsTS, goTS, goBundleTS, goBundleGo, tsLib, goLib, goLibErrors); err != nil {
				t.Fatal(err)
			}
		})
	}
	if ran != 19 {
		t.Fatalf("live writer requests = %d, want 19", ran)
	}
	t.Run("supplemental-supplied-values", func(t *testing.T) { demoSupplementalWriter(t, env, &outputs) })

	for _, file := range []string{"internal/bundle/demo/write.go", "internal/bundle/demo/write_output.go", "internal/bundle/demo/write_report.go"} {
		requireReached(t, file, outputs.String())
	}
	liveSummary(t, "%d captured writer requests and %d supplemental supplied-value cases compared with the frozen writer; Go bundles verified by Go and by TS", ran, len(demoSupplements))
}

func directoryOf(t *testing.T, c demoartifactstest.Capture) string {
	t.Helper()
	v, err := jsjson.Parse(*c.Call.ValueJSON)
	if err != nil {
		t.Fatal(err)
	}
	return v.Get("directory").Str()
}
