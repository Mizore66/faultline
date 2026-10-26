package prevention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// gitUnbound is the --from-bundle recipe on the slice 2 base git-unbound:
// its third state as the repaired state.
func gitUnbound(t *testing.T) ArtifactOptions {
	t.Helper()
	dir := filepath.Join(basesDir, "git-unbound")
	read := func(rel string) jsjson.Value {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		v, err := jsjson.Parse(string(b))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	investigation, frozen := read("investigation.json"), read("witness/frozen.json")
	state := investigation.Get("transitions").Items()[1].Get("after")
	var runs []jsjson.Value
	for i, id := range state.Get("runIds").Items() {
		runs = append(runs, jsjson.MustObject(
			"runId", id, "executionId", state.Get("executionIds").Items()[i],
			"commit", state.Get("commit"), "tree", state.Get("tree"), "verdict", jsjson.MakeString("PASS"),
			"witnessDigest", frozen.Get("witnessDigest"),
			"environmentDigest", investigation.Get("environment", "distinctDigests").Items()[0],
			"executionTrust", jsjson.MakeString("NATIVE_DOCKER"), "executionKind", jsjson.MakeString("EXECUTED"),
		))
	}
	root, _ := os.ReadFile(filepath.Join(dir, "ROOT.sha256"))
	return ArtifactOptions{
		Bundle: dir, ExpectedRoot: strings.TrimSpace(string(root)), RepairedRuns: jsjson.MakeArray(runs),
		RepairedCommit: state.Get("commit").Str(), RepairedTree: state.Get("tree").Str(),
		PatchDigest: "sha256:" + strings.Repeat("1", 64),
	}
}

func TestWriteFromArtifactsGitUnbound(t *testing.T) {
	workspace(t)
	o := gitUnbound(t)
	o.HasThreadID, o.CodexThreadID = true, "thread-1"
	w, reasons := WriteFromArtifacts(o, defaultOutput("from-bundle"))
	if reasons != nil {
		t.Fatal(reasons)
	}
	if w.Manifest.Get("classification").Str() != "PREVENTION_VERIFIED" || w.Body.Get("originalProofRoot").Str() != o.ExpectedRoot ||
		w.Body.Get("codexThreadId").Str() != "thread-1" || w.Body.Get("repairBaseTree").Str() != "a1d82cc5cc0c3976e651069980545f9fc85b0f25" {
		t.Fatal(jsjson.Stringify(w.Body))
	}
	if v := Verify(w.Directory, w.RootDigest, true); !v.Valid {
		t.Fatalf("%+v", v)
	}
	// The same output again: the writer's refusal becomes the reason.
	if _, reasons := WriteFromArtifacts(o, defaultOutput("from-bundle")); len(reasons) != 1 || !strings.HasPrefix(reasons[0], "Prevention proof output already exists") {
		t.Fatal(reasons)
	}
}

func TestBuildFromArtifactsRefusals(t *testing.T) {
	// Each refusal re-verifies the same bundle: verify it once.
	real := verifyGitBundle
	defer func() { verifyGitBundle = real }()
	cached := map[string]bundle.Result{}
	verifyGitBundle = func(dir, root string, provided bool) bundle.Result {
		if r, ok := cached[root]; ok {
			return r
		}
		cached[root] = real(dir, root, provided)
		return cached[root]
	}
	for _, c := range []struct {
		edit func(o *ArtifactOptions)
		want string
	}{
		{func(o *ArtifactOptions) { o.ExpectedRoot = "sha256:" + strings.Repeat("0", 64) }, "Proof bundle verification failed"},
		{func(o *ArtifactOptions) { o.RepairedRuns = jsjson.MakeArray(o.RepairedRuns.Items()[:2]) }, "Repaired state requires exactly 3 NATIVE_DOCKER run bindings."},
		{func(o *ArtifactOptions) {
			items := o.RepairedRuns.Items()
			items[1] = items[0]
		}, "Repaired run IDs must be distinct."},
		{func(o *ArtifactOptions) { o.RepairedTree = strings.Repeat("b", 40) }, "commit/tree does not match the repaired state."},
		{func(o *ArtifactOptions) {
			o.RepairedRuns.Items()[2].Obj().Set("executionKind", jsjson.MakeString("CACHED"))
		}, "is not a NATIVE_DOCKER EXECUTED PASS."},
		{func(o *ArtifactOptions) {
			o.RepairedRuns.Items()[0].Obj().Set("witnessDigest", jsjson.MakeString("x"))
		}, "witness digest does not match the frozen witness."},
		{func(o *ArtifactOptions) {
			o.RepairedRuns.Items()[0].Obj().Set("environmentDigest", jsjson.MakeString("x"))
		}, "environment digest does not match the proof-bundle environment."},
		{func(o *ArtifactOptions) {
			o.RepairedCommit = "60a88823e480d41398e0986eb30f6479e7ee705f"
			for _, run := range o.RepairedRuns.Items() {
				run.Obj().Set("commit", jsjson.MakeString(o.RepairedCommit))
			}
		}, "Repaired commit must be distinct from last-good and first-bad commits."},
	} {
		o := gitUnbound(t)
		c.edit(&o)
		_, reasons := BuildFromArtifacts(o)
		if len(reasons) == 0 || !strings.Contains(reasons[0], c.want) {
			t.Errorf("%s: %v", c.want, reasons)
		}
	}
	o := gitUnbound(t)
	o.RepairedRuns.Items()[1] = jsjson.MakeNull()
	if err := tryBuild(o); err == nil || err.Error() != "Cannot read properties of null (reading 'runId')" {
		t.Fatal(err)
	}
	o = gitUnbound(t)
	o.RepairedRuns.Items()[0].Obj().Set("runId", jsjson.MustObject("toString", jsjson.MakeNumber(1)))
	o.RepairedTree = "x"
	if err := tryBuild(o); err == nil || err.Error() != "Cannot convert object to primitive value" {
		t.Fatal(err)
	}
}

// tryBuild is BuildFromArtifacts with the TS exception it lets escape.
func tryBuild(o ArtifactOptions) error {
	return jsexc.Try(func() { BuildFromArtifacts(o) })
}

func TestDigestRepairPatchFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := DigestRepairPatchFile(filepath.Join(dir, "none")); err == nil || err.Error() != "Repair patch does not exist: "+filepath.Join(dir, "none") {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "p"), []byte("abc"), 0o644)
	if d, err := DigestRepairPatchFile(filepath.Join(dir, "p")); err != nil || d != "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal(d, err)
	}
}
