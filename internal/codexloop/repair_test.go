package codexloop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/witnessresult"
)

// bundleBase is the committed git-unbound proof bundle (investigation.json,
// witness/frozen.json). It is only read.
const bundleBase = "../../difftest/testdata/bases/git-unbound"

func validBundle(string, string) (bool, []string, *string) { return true, nil, nil }

func TestRepairInstructionsOnly(t *testing.T) {
	out := t.TempDir() + "/out"
	r, err := RepairWithCodex(RepairOptions{BundleDirectory: bundleBase, ExpectRoot: "sha256:root", Repository: "/nonexistent-cwd", OutputDirectory: out,
		Invariant: "", HasInvariant: true, InstructionsOnly: true, VerifyBundle: validBundle})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"schemaVersion", "cleanupErrors", "status", "transport", "worktreePath", "instructionPath", "managedRoot", "baseCommit", "patchPath", "codexThreadId", "nextCommands", "note", "limitation"}
	if got := r.Obj().Keys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("keys %v", got)
	}
	if r.Get("status").Str() != "REPAIR_INSTRUCTIONS_PREPARED" || r.Get("baseCommit").Str() != "60a88823e480d41398e0986eb30f6479e7ee705f" || r.Get("worktreePath").Kind() != jsjson.Null {
		t.Fatal(jsjson.Stringify(r))
	}
	text, err := os.ReadFile(out + "/repair-instructions.md")
	if err != nil {
		t.Fatal(err)
	}
	// An empty invariant is kept (invariant ?? default), and a null rootDigest falls back to expectRoot.
	if !strings.Contains(string(text), "Verified proof root: sha256:root\n") || !strings.Contains(string(text), "Invariant (inferred / human-supplied): \n") ||
		!strings.Contains(string(text), "Isolated worktree: not created (instructions only)\n") {
		t.Fatalf("%s", text)
	}
}

func TestRepairUnreadableInvestigationIsBundleInvalid(t *testing.T) {
	r, err := RepairWithCodex(RepairOptions{BundleDirectory: t.TempDir(), ExpectRoot: "sha256:x", Repository: ".", OutputDirectory: t.TempDir() + "/o", VerifyBundle: validBundle})
	if err != nil {
		t.Fatal(err)
	}
	if r.Get("status").Str() != "BUNDLE_INVALID" || !strings.HasPrefix(r.Get("note").Str(), "ENOENT: no such file or directory, open '") ||
		r.Get("limitation").Str() != "Repair requires a completed investigation artifact inside the verified proof bundle." {
		t.Fatal(jsjson.Stringify(r))
	}
	r, _ = RepairWithCodex(RepairOptions{BundleDirectory: t.TempDir(), ExpectRoot: "sha256:x", OutputDirectory: "o",
		VerifyBundle: func(string, string) (bool, []string, *string) { return false, []string{"a", "b"}, nil }})
	if r.Get("note").Str() != "a; b" || r.Get("limitation").Str() != "Repair requires a verified proof bundle and externally retained root digest." {
		t.Fatal(jsjson.Stringify(r))
	}
}

func scriptedSandbox(kind string) sandbox.Runner {
	return func(sandbox.Plan) (sandbox.RunResult, error) {
		zero, one := 0, 1
		switch kind {
		case "PASS":
			return sandbox.RunResult{ExitCode: &zero, Stdout: "state=good\n" + witnessresult.Format("PREDICATE_PASS", "", false) + "\n"}, nil
		case "FAIL":
			return sandbox.RunResult{ExitCode: &one, Stdout: "state=bad\n" + witnessresult.Format("PREDICATE_FAIL", "", false) + "\n", Stderr: "witness failed"}, nil
		}
		return sandbox.RunResult{}, errors.New("scripted sandbox failure")
	}
}

func TestFrozenWitnessVerifierInjected(t *testing.T) {
	for kind, want := range map[string]string{
		"PASS":  "Frozen witness PREDICATE_PASS on repaired worktree (INJECTED_RUNNER).",
		"FAIL":  "Frozen witness did not PREDICATE_PASS (verdict=FAIL, reason=PREDICATE_FAIL).",
		"ERROR": "Frozen witness did not PREDICATE_PASS (verdict=ERROR, reason=RUNNER_FAILURE).",
	} {
		worktree := t.TempDir()
		v, err := FrozenWitnessVerifier(scriptedSandbox(kind), true)(worktree, "base", "", bundleBase)
		if err != nil || v.Detail != want || v.OK != (kind == "PASS") || v.HasExecutionID != (kind == "PASS") {
			t.Fatalf("%s: %+v %v", kind, v, err)
		}
		if kind == "PASS" && !strings.HasPrefix(v.ExecutionID, "sha256:") {
			t.Fatal(v.ExecutionID)
		}
		if _, err := os.Stat(worktree + "/witness.mjs"); err != nil {
			t.Fatalf("%s: overlay not materialized: %v", kind, err)
		}
	}
}

func TestFrozenWitnessVerifierFailsClosed(t *testing.T) {
	// The reads come before the verifier's try: a missing bundle rejects.
	if _, err := FrozenWitnessVerifier(nil, false)(t.TempDir(), "b", "", t.TempDir()); err == nil || !strings.HasPrefix(err.Error(), "ENOENT: no such file or directory, open '") {
		t.Fatal(err)
	}
	// No digest-pinned image anywhere: a verdict, not a rejection.
	bundle := t.TempDir()
	copyTree(t, bundleBase, bundle)
	raw, err := os.ReadFile(bundle + "/investigation.json")
	if err != nil {
		t.Fatal(err)
	}
	investigation, err := jsjson.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range investigation.Get("runs").Items() {
		run.Get("sandbox", "runtime").Obj().Set("image", jsjson.MakeString("node:22"))
	}
	if err := os.WriteFile(bundle+"/investigation.json", []byte(jsjson.Stringify(investigation)), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := FrozenWitnessVerifier(nil, false)(t.TempDir(), "b", "", bundle)
	if err != nil || v.OK || v.Detail != "Repair verification fail-closed: proof bundle runs do not record a digest-pinned Docker image." {
		t.Fatalf("%+v %v", v, err)
	}
	// A throw inside the try is a verdict: the worktree does not exist.
	v, err = FrozenWitnessVerifier(nil, false)(t.TempDir()+"/missing", "b", "", bundleBase)
	if err != nil || v.OK || !strings.HasPrefix(v.Detail, "Repair verification fail-closed: ") {
		t.Fatalf("%+v %v", v, err)
	}
}

// The repair verifier derives policyDigest and environmentPolicyDigest from
// its own sandbox plan (src/codex-loop.ts:234-249); the writer harness names
// the verifier's executionId from the investigation run's digests instead
// (difftest repairedRunSeeds). They agree when the plan inputs agree: same
// image, frozen digest, default limits and no environment.
func TestRepairPlanDigestsEqualInvestigationRunDigests(t *testing.T) {
	read := func(rel string) jsjson.Value {
		raw, err := os.ReadFile(bundleBase + "/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		v, err := jsjson.Parse(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	investigation, frozen := read("investigation.json"), read("witness/frozen.json")
	run := investigation.Get("runs").Items()[0]
	plan, err := sandbox.CreatePlan(jsjson.MustObject(
		"witness", jsjson.MustObject("digest", frozen.Get("frozenDigest"), "command", frozen.Get("proposal", "witness", "command")),
		"sourceDirectory", jsjson.MakeString(t.TempDir()),
		"mode", jsjson.MakeString("DOCKER_ISOLATED"),
		"image", run.Get("sandbox", "runtime", "image"),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"policyDigest", "environmentPolicyDigest"} {
		if got, want := plan.Value.Get(key).Str(), run.Get("sandbox", key).Str(); got != want || got == "" {
			t.Fatalf("%s: plan %q, investigation run %q", key, got, want)
		}
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSelectRepairBaseCommit(t *testing.T) {
	inv, _ := jsjson.Parse(`{"transitions":[{"kind":"FAIL_TO_PASS","after":{"commit":"a"}},{"kind":"PASS_TO_FAIL","after":{"commit":"b"}}],"resolvedRange":{"descendant":{"commit":"c"}}}`)
	if c, err := SelectRepairBaseCommit(inv); err != nil || c != "b" {
		t.Fatal(c, err)
	}
	inv, _ = jsjson.Parse(`{"transitions":[],"resolvedRange":null}`)
	if _, err := SelectRepairBaseCommit(inv); err == nil || err.Error() != "Verified investigation does not identify a repair base commit." {
		t.Fatal(err)
	}
}

func TestRepairBundleInvalidKeyOrder(t *testing.T) {
	r, err := RepairWithCodex(RepairOptions{BundleDirectory: t.TempDir(), ExpectRoot: "sha256:x", Repository: t.TempDir(), OutputDirectory: t.TempDir(),
		VerifyBundle: func(string, string) (bool, []string, *string) { return false, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	keys := r.Obj().Keys()
	if keys[0] != "schemaVersion" || keys[1] != "cleanupErrors" || keys[2] != "status" || r.Get("note").Str() != "Proof bundle verification failed." {
		t.Fatalf("%v %s", keys, jsjson.Stringify(r))
	}
}
