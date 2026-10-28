package incident

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestResolveRangeRefusals(t *testing.T) {
	head := "1111111111111111111111111111111111111111"
	if _, err := ResolveRange(head, nil, nil); err == nil || err.Error() != "HEAD is a root commit; select an explicit range instead of guessing a base" {
		t.Fatalf("root: %v", err)
	}
	two := []string{"2222222222222222222222222222222222222222", "3333333333333333333333333333333333333333"}
	if _, err := ResolveRange(head, two, nil); err == nil || err.Error() != "HEAD has multiple parents; select an explicit range instead of guessing a parent" {
		t.Fatalf("merge: %v", err)
	}
	if _, err := ResolveRange(head, nil, &ExplicitRange{"abc", "abc"}); err == nil || err.Error() != "incident range ancestor and descendant must be different" {
		t.Fatalf("degenerate: %v", err)
	}
	if _, err := ResolveRange(head, []string{head}, nil); err == nil || err.(*RangeError).Code != "DEGENERATE_RANGE" {
		t.Fatalf("self parent: %v", err)
	}
}

func TestResolveRangeSources(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	got, err := ResolveRange(b, []string{a}, nil)
	if err != nil || jsjson.Stringify(got) != `{"ancestor":"`+a+`","descendant":"`+b+`","source":"LOCAL_HEAD_PARENT"}` {
		t.Fatalf("parent: %s %v", jsjson.Stringify(got), err)
	}
	// An explicit bracket wins over the observed facts, even a root HEAD.
	got, err = ResolveRange(b, nil, &ExplicitRange{"HEAD~1", "HEAD"})
	if err != nil || jsjson.Stringify(got) != `{"ancestor":"HEAD~1","descendant":"HEAD","source":"EXPLICIT"}` {
		t.Fatalf("explicit: %s %v", jsjson.Stringify(got), err)
	}
}

// TestSafeGitRevision checks the hand-written matcher on the cases where
// /^(?!-)[^\0\r\n]{1,512}$/ differs from a naive byte check: UTF-16
// counting of astral characters and a lone surrogate.
func TestSafeGitRevision(t *testing.T) {
	astral := "\U0001F600"
	for s, want := range map[string]bool{
		"HEAD~1": true, "-x": false, "a\nb": false, "": false, "a\rb": false, "a\x00b": false, "x-": true,
		strings.Repeat("a", 512): true, strings.Repeat("a", 513): false,
		strings.Repeat(astral, 256): true, strings.Repeat(astral, 256) + "a": false,
		"\xed\xa0\x80": true, // a lone surrogate (WTF-8)
	} {
		if SafeGitRevision(s) != want {
			t.Errorf("%q", s)
		}
	}
}

func sampleInput() DraftInput {
	return DraftInput{
		ID:         "first-incident",
		CreatedAt:  "2026-07-16T14:00:00.000Z",
		Repository: `C:\work\example`,
		Command:    "pnpm test -- --runInBand",
		LocalHead:  LocalHeadFacts(strings.Repeat("b", 40), []string{strings.Repeat("a", 40)}),
	}
}

func TestCreateAndVerifyDraft(t *testing.T) {
	draft, err := CreateDraft(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	keys := strings.Join(draft.Obj().Keys(), ",")
	if keys != "schemaVersion,draftId,createdAt,status,repository,command,commandDigest,range,review,draftDigest" {
		t.Fatalf("keys %s", keys)
	}
	if draft.Get("commandDigest").Str() != gitproof.CommandDigest("pnpm test -- --runInBand") {
		t.Fatal("command digest")
	}
	v, err := VerifyDraft(draft)
	if err != nil || jsjson.Stringify(v) != `{"valid":true,"errors":[]}` {
		t.Fatalf("verify: %s %v", jsjson.Stringify(v), err)
	}
	draft.Obj().Set("createdAt", jsjson.MakeString("2026-07-16T14:00:01.000Z"))
	v, _ = VerifyDraft(draft)
	if jsjson.Stringify(v) != `{"valid":false,"errors":["draft digest does not match the immutable draft payload"]}` {
		t.Fatalf("tampered: %s", jsjson.Stringify(v))
	}
}

func TestCreateDraftProposalBinding(t *testing.T) {
	in := sampleInput()
	digest := jsjson.MakeString("sha256:" + strings.Repeat("0", 64))
	in.Proposal = jsjson.MustObject("proposalId", jsjson.MakeString("other"), "proposalDigest", digest, "incidentPacketDigest", digest,
		"commandDigest", jsjson.MakeString(gitproof.CommandDigest(in.Command)))
	if _, err := CreateDraft(in); err == nil || err.Error() != "incident proposal binding must use the same identifier as its draft" {
		t.Fatalf("id: %v", err)
	}
	in.Proposal.Obj().Set("proposalId", jsjson.MakeString(in.ID))
	in.Proposal.Obj().Set("commandDigest", digest)
	if _, err := CreateDraft(in); err == nil || err.Error() != "incident proposal binding does not match the exact draft command" {
		t.Fatalf("command: %v", err)
	}
	in.Proposal.Obj().Set("commandDigest", jsjson.MakeString(gitproof.CommandDigest(in.Command)))
	draft, err := CreateDraft(in)
	if err != nil || draft.Get("review", "witnessState").Str() != "PROPOSED" {
		t.Fatalf("bound: %v", err)
	}
	if v, _ := VerifyDraft(draft); !v.Get("valid").Bool() {
		t.Fatalf("bound verify: %s", jsjson.Stringify(v))
	}
}

// TestCreateDraftRejectsResolvedRange pins that a resolved range (with its
// `source` member) is not a draft input: createIncidentDraft's strict schema
// rejects the unrecognized key, and CreateDraft no longer remaps it.
func TestCreateDraftRejectsResolvedRange(t *testing.T) {
	want, err := CreateDraft(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if want.Get("range").Get("source").Kind() == jsjson.Undefined {
		t.Fatal("draft range has no source")
	}
	in := sampleInput()
	in.Range, in.LocalHead = want.Get("range"), jsjson.Value{}
	_, err = CreateDraft(in)
	if err == nil || !strings.Contains(err.Error(), "source") || !strings.Contains(err.Error(), "nrecognized") {
		t.Fatalf("resolved range accepted or wrong error: %v", err)
	}
}
