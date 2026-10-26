package cli

import (
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/witness"
)

// writeContextDraft writes an unbound draft for demo-1 whose repository is
// repository into <lookup>/.faultline/incidents (the default draft store of
// the lookup repository).
func writeContextDraft(t *testing.T, lookup, repository string) string {
	t.Helper()
	draft, err := incident.CreateDraft(incident.DraftInput{ID: "demo-1", Repository: repository, Command: "node witness.mjs",
		CreatedAt: "2026-07-16T10:30:00.000Z", Range: jsjson.MustObject("ancestor", jsjson.MakeString("HEAD~1"), "descendant", jsjson.MakeString("HEAD"))})
	if err != nil {
		t.Fatal(err)
	}
	store := witness.DefaultIncidentDraftStore(lookup)
	if _, err := incident.WriteDraft(store, draft); err != nil {
		t.Fatal(err)
	}
	return store
}

// The witness store defaults under the draft's repository, not under the
// working directory or the lookup repository (src/cli-app.ts:459).
func TestIncidentContextWitnessStoreIsDraftRepository(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cwd := filepath.Join(root, "cwd")
	repository := filepath.Join(root, "repo")
	draftStore := writeContextDraft(t, cwd, repository)
	t.Chdir(cwd)
	for _, args := range [][]string{
		{"status", "demo-1"},
		{"status", "demo-1", "--repo"}, // last: option() is undefined
		{"continue", "demo-1", "--draft-store", draftStore},
	} {
		ctx, err := loadIncidentCommandContext(args, "demo-1")
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if want := filepath.Join(repository, ".faultline", "witnesses"); ctx.witnessStore != want {
			t.Errorf("%v: witness store %s, want %s", args, ctx.witnessStore, want)
		}
		if ctx.repository != repository || ctx.draftStore != draftStore {
			t.Errorf("%v: repository %s draft store %s", args, ctx.repository, ctx.draftStore)
		}
	}
	ctx, err := loadIncidentCommandContext([]string{"status", "demo-1", "--store", "mine"}, "demo-1")
	if err != nil || ctx.witnessStore != filepath.Join(cwd, "mine") {
		t.Errorf("--store: %v %s", err, ctx.witnessStore)
	}
}

// A --repo naming another directory than the draft's repository is refused
// with the continuation text, for status too (src/cli-app.ts:454).
func TestIncidentContextRefusesRepoMismatch(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	other := filepath.Join(root, "other")
	draftStore := writeContextDraft(t, other, filepath.Join(root, "repo"))
	_, err := loadIncidentCommandContext([]string{"status", "demo-1", "--repo", other, "--draft-store", draftStore}, "demo-1")
	const want = "Incident continuation refuses a --repo value that differs from the immutable incident draft repository."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	if _, err := loadIncidentCommandContext([]string{"status", "demo-1", "--repo", filepath.Join(root, "repo", "."), "--draft-store", draftStore}, "demo-1"); err != nil {
		t.Errorf("an equal resolved --repo was refused: %v", err)
	}
}

func TestIncidentStatusContinueUsage(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"incident", "status"}, "FaultLine error: " + incidentStatusUsage + "\nNext: pnpm fl help\n"},
		{[]string{"incident", "continue", ""}, "FaultLine error: " + incidentContinueUsage + "\nNext: pnpm fl help\n"},
	} {
		_, stderr, code := run(c.args...)
		if code != 1 || stderr != c.want {
			t.Errorf("%v: exit %d stderr %q", c.args, code, stderr)
		}
	}
}
