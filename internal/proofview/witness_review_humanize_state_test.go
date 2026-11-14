package proofview

import (
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/witness"
)

type gitCall struct {
	repo string
	args []string
}

// stubReviewGit replaces the read-only runner; no host command runs.
func stubReviewGit(t *testing.T) func() []gitCall {
	t.Helper()
	var mu sync.Mutex
	var calls []gitCall
	old := reviewGit
	reviewGit = func(repo string, args ...string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, gitCall{repo, append([]string(nil), args...)})
		switch args[0] {
		case "rev-list":
			return "1", true
		case "diff-tree":
			return "a.txt", true
		}
		return strings.Repeat("c", 40) + "\tAuthor\t2026-01-01T00:00:00Z\tsubject", true
	}
	t.Cleanup(func() { reviewGit = old })
	return func() []gitCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]gitCall(nil), calls...)
	}
}

func linkedDraftStore(t *testing.T) (store, repo string) {
	t.Helper()
	stored, err := witness.Propose(t.TempDir(), reviewProposal(t, reviewTestID))
	if err != nil {
		t.Fatal(err)
	}
	b := jsjson.NewObj()
	b.Set("proposalId", stored.Get("proposalId"))
	b.Set("proposalDigest", stored.Get("proposalDigest"))
	b.Set("incidentPacketDigest", stored.Get("incidentPacketDigest"))
	b.Set("commandDigest", jsjson.MakeString(gitproof.CommandDigest("node witness.mjs")))
	repo = filepath.Join(t.TempDir(), "owned-repo")
	draft, err := incident.CreateDraft(incident.DraftInput{
		ID: reviewTestID, CreatedAt: "2026-07-16T14:00:00.000Z", Repository: repo, Command: "node witness.mjs",
		LocalHead: incident.LocalHeadFacts(strings.Repeat("b", 40), []string{strings.Repeat("a", 40)}),
		Proposal:  jsjson.MakeObject(b),
	})
	if err != nil {
		t.Fatal(err)
	}
	store = t.TempDir()
	if _, err := incident.WriteDraft(store, draft); err != nil {
		t.Fatal(err)
	}
	return store, repo
}

func wantTriples(repo string, n int) []gitCall {
	rng := strings.Repeat("a", 40) + ".." + strings.Repeat("b", 40)
	var out []gitCall
	for i := 0; i < n; i++ {
		out = append(out,
			gitCall{repo, []string{"rev-list", "--count", rng}},
			gitCall{repo, []string{"diff-tree", "--no-commit-id", "--name-only", "-r", rng}},
			gitCall{repo, []string{"log", "--format=%H%x09%an%x09%aI%x09%s", "--no-decorate", rng}})
	}
	return out
}

func requireCalls(t *testing.T, got, want []gitCall) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestWitnessReviewServerHumanizesWhenReadingState(t *testing.T) {
	t.Run("get reads one triple", func(t *testing.T) {
		calls := stubReviewGit(t)
		ds, repo := linkedDraftStore(t)
		f := newReviewFixture(t, false, ds)
		if r := reviewDo(t, "GET", f.server.URL()+"/", ""); r.status != 200 {
			t.Fatalf("get %d", r.status)
		}
		requireCalls(t, calls(), wantTriples(repo, 1))
	})
	t.Run("valid token acknowledgement refusal humanizes before validation", func(t *testing.T) {
		calls := stubReviewGit(t)
		ds, repo := linkedDraftStore(t)
		f := newReviewFixture(t, false, ds)
		r := f.post("/approve-freeze", f.form("acknowledged", "\x00delete"))
		if r.status != 409 {
			t.Fatalf("status %d", r.status)
		}
		requireCalls(t, calls(), wantTriples(repo, 1))
		f.requireRecords(0, 0)
	})
	t.Run("success humanizes before action and once after with new state", func(t *testing.T) {
		calls := stubReviewGit(t)
		ds, repo := linkedDraftStore(t)
		f := newReviewFixture(t, false, ds)
		var atHook []gitCall
		f.server.setBetweenOpenAndAction(func() { atHook = calls() })
		r := f.post("/approve-freeze", f.form())
		if r.status != 200 || !strings.Contains(r.body, "PROPOSED → APPROVED → FROZEN") {
			t.Fatalf("post %d", r.status)
		}
		requireCalls(t, atHook, wantTriples(repo, 1))
		requireCalls(t, calls(), wantTriples(repo, 2))
		f.requireRecords(1, 1)
	})
	t.Run("two step approve renders approved state from one later humanization", func(t *testing.T) {
		calls := stubReviewGit(t)
		ds, repo := linkedDraftStore(t)
		f := newReviewFixture(t, true, ds)
		r := f.post("/approve", f.form())
		if r.status != 200 || !strings.Contains(r.body, "PROPOSED → APPROVED → awaiting freeze") {
			t.Fatalf("post %d", r.status)
		}
		requireCalls(t, calls(), wantTriples(repo, 2))
	})
	t.Run("invalid token reads nothing", func(t *testing.T) {
		calls := stubReviewGit(t)
		ds, _ := linkedDraftStore(t)
		f := newReviewFixture(t, false, ds)
		v := url.Values{"token": {"wrong"}, "reviewDigest": {f.digest()}}
		if r := f.post("/approve-freeze", v.Encode()); r.status != 403 {
			t.Fatalf("status %d", r.status)
		}
		requireCalls(t, calls(), nil)
	})
	t.Run("standalone without draft runs nothing", func(t *testing.T) {
		calls := stubReviewGit(t)
		f := newReviewFixture(t, false, "")
		reviewDo(t, "GET", f.server.URL()+"/", "")
		if r := f.post("/approve-freeze", f.form()); r.status != 200 {
			t.Fatalf("status %d", r.status)
		}
		requireCalls(t, calls(), nil)
	})
	t.Run("public standalone render humanizes exactly once", func(t *testing.T) {
		calls := stubReviewGit(t)
		ds, repo := linkedDraftStore(t)
		f := newReviewFixture(t, false, "")
		draft := readBoundDraft(ds, f.review)
		RenderWitnessReviewHTML(f.review, draft, reviewTestToken, false)
		RenderWitnessReviewText(f.review, draft)
		requireCalls(t, calls(), wantTriples(repo, 2))
	})
}
