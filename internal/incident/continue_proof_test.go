//go:build faultline_test && unix

package incident

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/sandbox"
)

// twoStateIncident is a draft over gitinv.GoodThenBadRepoForTest whose
// bound witness runs `node witness.mjs` (state.txt bad fails), approved
// and frozen in a temporary store, with the pass-fail fake Docker on PATH.
func twoStateIncident(t *testing.T, runtime jsjson.Value) ContinueOptions {
	t.Helper()
	t.Setenv("PATH", gitinv.FakeDockerDirForTest(t, "pass-fail")+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo, ancestor, descendant := gitinv.GoodThenBadRepoForTest(t)
	in := DraftInput{ID: "demo-1", CreatedAt: "2026-07-16T14:00:00.000Z", Repository: repo, Command: "node witness.mjs",
		Range: jsjson.MustObject("ancestor", str(ancestor), "descendant", str(descendant)), Runtime: runtime}
	store := t.TempDir()
	proposal := proposeApproveFreeze(t, store, proposalInput(in.ID, in.Command, "two states",
		map[string]string{"witness.mjs": gitinv.WitnessScriptForTest}))
	draft := boundDraft(t, in, proposal)
	return ContinueOptions{Repository: repo, Store: store, Draft: draft,
		ExpectedFrozenDigest: readFrozen(t, store, in.ID).Get("frozenDigest").Str()}
}

func TestContinueWritesVerifiedIncidentBundle(t *testing.T) {
	o := twoStateIncident(t, jsjson.MustObject("requested", str("node"), "image", str(gitinv.PinnedImageForTest)))
	clock.ResetForTest("incident-continue-proof")
	got, err := Continue(o)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("status").Str() != "PROOF_BUNDLE_READY" {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
	if keys := strings.Join(got.Obj().Keys(), ","); keys != "status,incident,investigation,proofBundle,next,limitations" {
		t.Fatalf("keys %s", keys)
	}
	incident := got.Get("incident")
	if keys := strings.Join(incident.Obj().Keys(), ","); keys != "id,draftDigest,range,runtime,frozenDigest,frozenDigestExternalStatus" ||
		incident.Get("frozenDigestExternalStatus").Str() != "MATCH" || incident.Get("runtime", "requested").Str() != "node" ||
		incident.Get("range", "source").Str() != "EXPLICIT" {
		t.Fatalf("incident %s", jsjson.Stringify(incident))
	}
	descendant := o.Draft.Get("range", "descendant").Str()
	bundle := got.Get("proofBundle")
	dir := bundle.Get("directory").Str()
	// incident-<id>-<desc12>-<Date.now()>: the investigation reads the seeded
	// clock first, so only the prefix is fixed here.
	prefix := filepath.Join(o.Repository, ".faultline", "git-proof-bundles", "incident-demo-1-"+descendant[:12]+"-")
	if !strings.HasPrefix(dir, prefix) || strings.Join(bundle.Obj().Keys(), ",") != "directory,rootDigest,externalRootStatus,lifecycle" ||
		bundle.Get("externalRootStatus").Str() != "MATCH" {
		t.Fatalf("bundle %s", jsjson.Stringify(bundle))
	}
	if v := gitproof.Verify(dir, bundle.Get("rootDigest").Str(), true); !v.Valid {
		t.Fatalf("verify %v", v.Errors)
	}
	next := got.Get("next").Items()
	if next[0].Str() != "fl serve --bundle "+dir+" --expect-root "+bundle.Get("rootDigest").Str() {
		t.Fatalf("next %s", jsjson.Stringify(got.Get("next")))
	}

	// A second run with --output naming the written bundle refuses to
	// replace it; one inside the proof root but elsewhere is written there.
	o.Output = &dir
	if _, err := Continue(o); err == nil || err.Error() != "Git proof bundle output already exists and will not be replaced: "+dir {
		t.Fatalf("exists: %v", err)
	}
	custom := filepath.Join(o.Repository, ".faultline", "git-proof-bundles", "custom")
	o.Output = &custom
	if got, err := Continue(o); err != nil || got.Get("proofBundle", "directory").Str() != custom {
		t.Fatalf("custom: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	o.Output = &outside
	if _, err := Continue(o); err == nil || !strings.HasPrefix(err.Error(), "Git proof bundle output must be a child directory of ") {
		t.Fatalf("outside: %v", err)
	}
}

func TestContinueExplicitImageRuntime(t *testing.T) {
	o := twoStateIncident(t, jsjson.Value{})
	image := gitinv.PinnedImageForTest
	o.Image = &image
	one := float64(1)
	o.MaxStates = &one
	got, err := Continue(o)
	if err != nil {
		t.Fatal(err)
	}
	// maxStates is forwarded: 1 is below the request schema's minimum of 2,
	// so the investigation is an invalid request and the result is not proof.
	if got.Get("status").Str() != "INVESTIGATION_NOT_PROOF" || got.Get("proofBundle").Kind() != jsjson.Null ||
		jsjson.Stringify(got.Get("incident", "runtime")) != `{"requested":"explicit","image":"`+image+`"}` ||
		got.Get("investigation", "status").Str() != "INVALID_REQUEST" ||
		!strings.Contains(got.Get("investigation", "errors").Items()[0].Str(), `"maxStates"`) {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
	if keys := strings.Join(got.Obj().Keys(), ","); keys != "status,incident,investigation,proofBundle,next" {
		t.Fatalf("keys %s", keys)
	}
}

func TestContinueInjectedRunnerIsNeverProof(t *testing.T) {
	o := twoStateIncident(t, jsjson.MustObject("requested", str("node"), "image", str(gitinv.PinnedImageForTest)))
	o.Runner, o.Injected = sandbox.NativeRunner(), true
	got, err := Continue(o)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("status").Str() != "INVESTIGATION_NOT_PROOF" || got.Get("investigation", "proof", "executionTrust").Str() != "INJECTED_RUNNER" {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
	if _, err := os.Stat(filepath.Join(o.Repository, ".faultline")); !os.IsNotExist(err) {
		t.Fatalf("not-proof wrote under the repository: %v", err)
	}
}
