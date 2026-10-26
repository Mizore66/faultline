//go:build faultline_test && unix

package difftest

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// TestEndToEndWithoutNode is done criterion 3 of the slice 2 spec (4.2): the
// slice 2 commands chain end to end on the Go binary with no `node` on PATH
// (only git, the sh recorders and the pass-fail fake Docker): witness
// propose, approve and freeze, investigate git, verify of the bundle it
// wrote, and judge-proof --export-only on that bundle. POSIX only: the
// recorders and the fake Docker are sh scripts.
func TestEndToEndWithoutNode(t *testing.T) {
	ws := scenarioWorkspace(t, "git-two-states")
	r := rootsOf(ws)
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := lookPathIn("node", dir); err != nil {
			kept = append(kept, dir)
		}
	}
	path := strings.Join(append([]string{r.recorder, filepath.Join(repoRoot(t), "difftest", "testdata", "fakedocker", "pass-fail")}, kept...), string(os.PathListSeparator))
	if p, err := lookPathIn("node", path); err == nil {
		t.Fatalf("node still on PATH: %s", p)
	}
	if _, err := lookPathIn("git", path); err != nil {
		t.Fatal("git is not on PATH")
	}
	env := map[string]string{"PATH": path}
	// A store of its own: the scenario already holds a frozen demo-1, and
	// its session binding (which investigate inherits) names that witness.
	const store = "e2e-store"
	if err := os.Remove(filepath.Join(ws, "repo", ".faultline", "incidents", "bindings", "demo-1.json")); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		stdout, stderr, code := runFlIn(t, ws, env, args...)
		if code != 0 {
			t.Fatalf("fl %s: exit %d\n%s%s", strings.Join(args, " "), code, stdout, stderr)
		}
		return stdout
	}
	run("witness", "propose", "--input", "proposal.json", "--store", store)
	run("witness", "approve", "demo-1", "--approved-by", "r@example.test", "--store", store)
	run("witness", "freeze", "demo-1", "--store", store, "--repo", "repo")
	bundle := proofDirectory(t, run("investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD",
		"--proposal", "demo-1", "--image", pinnedImage, "--store", store))
	root := expectedRoot(t, bundle)
	if out := run("verify", bundle, "--expect-root", root); !strings.Contains(out, "Integrity: VALID") || !strings.Contains(out, "External root: MATCH") {
		t.Fatalf("verify:\n%s", out)
	}
	if out := run("judge-proof", "--bundle", bundle, "--expect-root", root, "--export-only"); !strings.Contains(out, "Verified root summary: "+root) {
		t.Fatalf("judge-proof:\n%s", out)
	}
}

// TestEndToEndSlice3aWithoutNode is done criterion 3 of the slice 3 spec
// (4.2, PR 3a; plan correction 5): `fl init --yes`, `fl doctor
// --proof-ready` and `fl runtime prepare node --yes` on the Go binary in a
// repository, with PATH holding only git's own directory, the runtime-digest
// fake Docker and its hash tool, and no `node`. Without Node,
// doctor's node check is UNAVAILABLE and doctor exits 1 by design (slice 3
// spec 3.1), so --proof-ready prints NOT READY; init and runtime prepare
// succeed, and the config holds the prepared image. POSIX only: the fake
// Docker is a sh script.
func TestEndToEndSlice3aWithoutNode(t *testing.T) {
	ws := scenarioWorkspace(t, "init-fresh")
	env := map[string]string{"PATH": nodelessPath(t, "runtime-digest", "sha256sum|shasum")}

	stdout, stderr, code := runFlIn(t, ws, env, "init", "--yes")
	if code != 0 || jsonField(stdout, "status") != "INIT_APPLIED" {
		t.Fatalf("fl init --yes: exit %d\n%s%s", code, stdout, stderr)
	}
	if got := jsonField(stdout, "snapshotPrewarm", "status"); got != "PREWARMED" {
		t.Errorf("fl init --yes: snapshotPrewarm.status %q, want PREWARMED\n%s%s", got, stdout, stderr)
	}

	stdout, stderr, code = runFlIn(t, ws, env, "doctor", "--proof-ready")
	if code != 1 || !strings.Contains(stdout, "\n[UNAVAILABLE] node: Node.js is unavailable to FaultLine.") || !strings.Contains(stdout, "\n--proof-ready: NOT READY\n") {
		t.Fatalf("fl doctor --proof-ready: exit %d (want 1 with node UNAVAILABLE and NOT READY)\n%s%s", code, stdout, stderr)
	}

	stdout, stderr, code = runFlIn(t, ws, env, "runtime", "prepare", "node", "--yes")
	if code != 0 || jsonField(stdout, "status") != "PREPARED_LOCAL_DIGEST" {
		t.Fatalf("fl runtime prepare node --yes: exit %d\n%s%s", code, stdout, stderr)
	}
	image := jsonField(stdout, "image")
	if !strings.HasPrefix(image, "node@sha256:") {
		t.Fatalf("fl runtime prepare node --yes: image %q", image)
	}
	config, err := os.ReadFile(filepath.Join(ws, ".faultline", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(string(config), "image"); got != image {
		t.Errorf(".faultline/config.json image %q, want %q\n%s", got, image, config)
	}
	if got := jsonField(string(config), "runtime", "image"); got != image || jsonField(string(config), "runtime", "alias") != "node" {
		t.Errorf(".faultline/config.json runtime is not node at %s\n%s", image, config)
	}
}

// TestEndToEndSlice3bWithoutNode is done criterion 3 of the slice 3 spec
// (4.2, PR 3b): the incident and prevention commands chain end to end on
// the Go binary with no `node` on PATH (git's own directory, the pass-fail
// fake Docker and the one tool it needs). It runs on a real absolute
// repository path, which the writer goldens cannot hold (their drafts
// record a relative "repo"): incident start with a digest-pinned --image,
// witness approve and freeze, incident status (READY_TO_INVESTIGATE),
// incident continue (PROOF_BUNDLE_READY, inheriting the frozen digest from
// the session binding freeze wrote), fl verify of that bundle, prevention
// write --from-bundle with repaired runs built from the bundle's own third
// state (difftest/gen/scenarios.ts preventionRepairedRuns), and fl verify
// of the package (Prevention verified). POSIX only: the fake Docker is a
// sh script.
func TestEndToEndSlice3bWithoutNode(t *testing.T) {
	// incident-ledger-full's repository: good, bad, repaired, with
	// witness.mjs in every state. Its pre-made draft, proposal and ledger
	// are removed: incident start writes fresh records.
	ws := scenarioWorkspace(t, "incident-ledger-full")
	repo := filepath.Join(ws, "repo")
	for _, p := range []string{filepath.Join(repo, ".faultline"), filepath.Join(ws, "ledger.json")} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"PATH": nodelessPath(t, "pass-fail", "cat")}
	const id = "e2e-1"
	store := filepath.Join(repo, ".faultline", "witnesses")
	step := func(want int, args ...string) string {
		t.Helper()
		stdout, stderr, code := runFlIn(t, ws, env, args...)
		if code != want {
			t.Fatalf("fl %s: exit %d, want %d\n%s%s", strings.Join(args, " "), code, want, stdout, stderr)
		}
		return stdout
	}
	status := func(want, stdout string, args ...string) {
		t.Helper()
		if got := jsonField(stdout, "status"); got != want {
			t.Fatalf("fl %s: status %q, want %s\n%s", strings.Join(args, " "), got, want, stdout)
		}
	}

	args := []string{"incident", "start", "--repo", repo, "--command", "node witness.mjs", "--id", id, "--from", "HEAD~2", "--to", "HEAD", "--image", pinnedImage}
	out := step(0, args...)
	status("DRAFT_REQUIRES_HUMAN_REVIEW", out, args...)
	if got, want := jsonField(out, "draft", "path"), filepath.Join(repo, ".faultline", "incidents", "drafts", id+".json"); !samePath(t, got, want) {
		t.Errorf("draft path %q, want %q", got, want)
	}
	if got := jsonField(out, "witnessProposal", "store"); !samePath(t, got, store) {
		t.Errorf("witness store %q, want %q", got, store)
	}
	draft, err := os.ReadFile(filepath.Join(repo, ".faultline", "incidents", "drafts", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(string(draft), "repository"); !filepath.IsAbs(got) || !samePath(t, got, repo) {
		t.Errorf("draft repository %q, want the absolute %q", got, repo)
	}

	args = []string{"witness", "approve", id, "--approved-by", "reviewer@example.test", "--store", store}
	status("APPROVED", step(0, args...), args...)
	args = []string{"witness", "freeze", id, "--store", store, "--repo", repo}
	out = step(0, args...)
	status("FROZEN", out, args...)
	frozen := jsonField(out, "frozenDigest")

	args = []string{"incident", "status", id, "--repo", repo, "--expect-digest", frozen}
	status("READY_TO_INVESTIGATE", step(0, args...), args...)

	// No --expect-digest: continue inherits it from freeze's session binding.
	args = []string{"incident", "continue", id, "--repo", repo}
	out = step(0, args...)
	status("PROOF_BUNDLE_READY", out, args...)
	if got := jsonField(out, "incident", "frozenDigest"); got != frozen {
		t.Errorf("continue frozenDigest %q, want %q", got, frozen)
	}
	bundle := proofDirectory(t, out)
	if !strings.HasPrefix(bundle, string(os.PathSeparator)) {
		t.Fatalf("bundle directory %q is not absolute", bundle)
	}
	root := expectedRoot(t, bundle)
	if got := jsonField(out, "proofBundle", "rootDigest"); got != root {
		t.Errorf("continue rootDigest %q, ROOT.sha256 %q", got, root)
	}
	if out := step(0, "verify", bundle, "--expect-root", root); !strings.Contains(out, "Integrity: VALID") || !strings.Contains(out, "External root: MATCH") {
		t.Fatalf("verify bundle:\n%s", out)
	}

	repaired, commit, tree := repairedRunsFromBundle(t, bundle)
	runs := filepath.Join(ws, "repaired-runs.json")
	if err := os.WriteFile(runs, []byte(repaired), 0o644); err != nil {
		t.Fatal(err)
	}
	args = []string{"prevention", "write", "--from-bundle", bundle, "--repaired-runs", runs, "--expect-root", root,
		"--repaired-commit", commit, "--repaired-tree", tree, "--patch", filepath.Join(bundle, "source", "range.patch"), "--repo", repo}
	out = step(0, args...)
	pkg, pkgRoot, ok := writtenPrevention(out)
	if !ok || !strings.HasSuffix(out, "\nPrevention verified\n") {
		t.Fatalf("fl %s: no verified package\n%s", strings.Join(args, " "), out)
	}
	status("PREVENTION_VERIFIED", out[:len(out)-len("Prevention verified\n")], args...)
	if got := jsonField(out[:len(out)-len("Prevention verified\n")], "originalProofRoot"); got != root {
		t.Errorf("originalProofRoot %q, want %q", got, root)
	}
	if got := jsonField(out[:len(out)-len("Prevention verified\n")], "agentsMd", "status"); got != "CREATED" {
		t.Errorf("agentsMd.status %q, want CREATED\n%s", got, out)
	}
	if b, err := os.ReadFile(filepath.Join(repo, "AGENTS.md")); err != nil || !strings.Contains(string(b), "<!-- FAULTLINE:BEGIN -->") {
		t.Errorf("AGENTS.md: %v\n%s", err, b)
	}
	out = step(0, "verify", pkg, "--expect-root", pkgRoot)
	for _, want := range []string{"Integrity: VALID", "Classification: PREVENTION_VERIFIED", "External root: MATCH", "\nPrevention verified\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("verify package: no %q\n%s", want, out)
		}
	}
}

// repairedRunsFromBundle is difftest/gen/scenarios.ts preventionRepairedRuns
// on a bundle: the repaired-runs.json of its third state
// (transitions[1].after), bound to its frozen witness digest and its
// homogeneous environment digest, with that state's commit and tree.
func repairedRunsFromBundle(t *testing.T, bundle string) (text, commit, tree string) {
	t.Helper()
	read := func(name string) jsjson.Value {
		b, err := os.ReadFile(filepath.Join(bundle, name))
		if err != nil {
			t.Fatal(err)
		}
		v, err := jsjson.Parse(string(b))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return v
	}
	investigation := read("investigation.json")
	witness := read(filepath.Join("witness", "frozen.json")).Get("witnessDigest")
	transitions := investigation.Get("transitions").Items()
	if len(transitions) != 2 || transitions[0].Get("kind").Str() != "PASS_TO_FAIL" || transitions[1].Get("kind").Str() != "FAIL_TO_PASS" {
		t.Fatalf("bundle transitions are not PASS_TO_FAIL then FAIL_TO_PASS: %s", jsjson.Stringify(investigation.Get("transitions")))
	}
	state := transitions[1].Get("after")
	environments := investigation.Get("environment", "distinctDigests").Items()
	if len(environments) != 1 {
		t.Fatalf("environment is not homogeneous: %s", jsjson.Stringify(investigation.Get("environment")))
	}
	executions := state.Get("executionIds").Items()
	var items []jsjson.Value
	for i, runID := range state.Get("runIds").Items() {
		items = append(items, jsjson.MustObject(
			"runId", runID, "executionId", executions[i], "commit", state.Get("commit"), "tree", state.Get("tree"),
			"verdict", jsjson.MakeString("PASS"), "witnessDigest", witness, "environmentDigest", environments[0],
			"executionTrust", jsjson.MakeString("NATIVE_DOCKER"), "executionKind", jsjson.MakeString("EXECUTED")))
	}
	if len(items) != 3 {
		t.Fatalf("third state has %d runs, want 3", len(items))
	}
	return jsjson.StringifyIndent(jsjson.MustObject("runs", jsjson.MakeArray(items)), "  ") + "\n", state.Get("commit").Str(), state.Get("tree").Str()
}

// samePath reports whether two paths name the same directory entry
// (macOS's /var is /private/var).
func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}

// nodelessPath is a PATH with no `node` on it: git's own directory
// (/usr/bin/git on macOS is an xcrun shim), the named fake Docker, and a
// directory of its own linking the first of each tool group found on the
// test's PATH (a group is alternatives separated by "|", such as
// "sha256sum|shasum"), so no system directory (which may hold a node) is on
// PATH.
func nodelessPath(t *testing.T, fakeDocker string, tools ...string) string {
	t.Helper()
	exec, err := osexec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	for _, group := range tools {
		found := false
		for _, name := range strings.Split(group, "|") {
			if p, err := osexec.LookPath(name); err == nil {
				if err := os.Symlink(p, filepath.Join(linked, name)); err != nil {
					t.Fatal(err)
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("none of %s is on PATH", group)
		}
	}
	var dirs []string
	for _, dir := range []string{strings.TrimSpace(string(exec)), filepath.Join(repoRoot(t), "difftest", "testdata", "fakedocker", fakeDocker), linked} {
		if _, err := lookPathIn("node", dir); err == nil {
			t.Fatalf("%s holds a node, so PATH cannot leave Node out", dir)
		}
		dirs = append(dirs, dir)
	}
	path := strings.Join(dirs, string(os.PathListSeparator))
	if _, err := lookPathIn("git", path); err != nil {
		t.Fatal("git is not on PATH")
	}
	return path
}
