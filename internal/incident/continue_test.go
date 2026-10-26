package incident

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/witness"
)

func mustParse(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

const continueImage = "node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// proposalInput is the witness proposal `incident start` sends
// (src/cli-app.ts:630-646), with overlays.
func proposalInput(id, command, summary string, overlays map[string]string) jsjson.Value {
	var items []jsjson.Value
	for path, content := range overlays {
		items = append(items, jsjson.MustObject("path", str(path), "bytesBase64", str(base64.StdEncoding.EncodeToString([]byte(content)))))
	}
	return jsjson.MustObject(
		"proposalId", str(id),
		"proposalOrigin", str("HUMAN"),
		"incidentPacket", jsjson.MustObject(
			"symptom", str("A human-reported command is failing."),
			"ciLog", str("The exact user-supplied command is stored in the reviewable witness."),
			"repositoryLanguage", str("Unknown"),
			"repositorySummary", str(summary)),
		"witness", jsjson.MustObject(
			"behavior", str("The command must exit successfully."),
			"command", str(command),
			"overlays", jsjson.MakeArray(items),
			"policy", jsjson.MustObject("network", str("disabled"), "credentials", str("redacted"), "timeoutSeconds", jsjson.MakeNumber(300))))
}

// proposeApproveFreeze proposes, approves and freezes input in store and
// returns the proposal.
func proposeApproveFreeze(t *testing.T, store string, input jsjson.Value) jsjson.Value {
	t.Helper()
	proposal, err := witness.Propose(store, input)
	if err != nil {
		t.Fatal(err)
	}
	id := proposal.Get("proposalId").Str()
	if _, err := witness.Approve(store, id, jsjson.MustObject("approvedBy", str("reviewer@example.test"))); err != nil {
		t.Fatal(err)
	}
	if _, err := witness.Freeze(store, id, witness.FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	return proposal
}

// boundDraft is `incident start`'s second createIncidentDraft: in plus the
// proposal binding (cli-app.ts:647).
func boundDraft(t *testing.T, in DraftInput, proposal jsjson.Value) jsjson.Value {
	t.Helper()
	in.Proposal = jsjson.MustObject(
		"proposalId", proposal.Get("proposalId"),
		"proposalDigest", proposal.Get("proposalDigest"),
		"incidentPacketDigest", proposal.Get("incidentPacketDigest"),
		"commandDigest", proposal.Get("witness", "commandDigest"))
	draft, err := CreateDraft(in)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

// boundFixture is a draft bound to a proposal that is approved and frozen in
// a temporary store.
func boundFixture(t *testing.T) (draft jsjson.Value, store string) {
	t.Helper()
	in := sampleInput()
	store = t.TempDir()
	proposal := proposeApproveFreeze(t, store, proposalInput(in.ID, in.Command, "the reviewed range", nil))
	return boundDraft(t, in, proposal), store
}

func readFrozen(t *testing.T, store, id string) jsjson.Value {
	t.Helper()
	frozen, err := witness.ReadFrozen(store, id)
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

// mismatchedBindingFixture is a draft bound to one proposal and the frozen
// witness of another with the same identifier, a different packet and a
// different command.
func mismatchedBindingFixture(t *testing.T) (draft, frozen jsjson.Value) {
	t.Helper()
	draft, other := mismatchedStoreFixture(t)
	return draft, readFrozen(t, other, draft.Get("draftId").Str())
}

// mismatchedStoreFixture is mismatchedBindingFixture's draft and the store
// holding the other frozen witness.
func mismatchedStoreFixture(t *testing.T) (draft jsjson.Value, other string) {
	t.Helper()
	draft, _ = boundFixture(t)
	other = t.TempDir()
	proposeApproveFreeze(t, other, proposalInput(draft.Get("draftId").Str(), "pnpm test -- --watch", "another range", nil))
	return draft, other
}

func mustProposal(t *testing.T, store, id string) jsjson.Value {
	t.Helper()
	proposal, err := witness.ReadProposal(store, id)
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func TestAssertBindingRefusesUnbound(t *testing.T) {
	draft := mustParse(t, `{"review":{"witnessState":"NOT_PROPOSED"}}`)
	err := AssertFrozenWitnessBinding(draft, mustParse(t, `{}`))
	if err == nil || err.Error() != "Incident draft has no proposal binding and cannot continue to investigation." {
		t.Fatalf("%v", err)
	}
}

func TestAssertBindingListsMismatchesInOrder(t *testing.T) {
	draft, frozen := mismatchedBindingFixture(t)
	err := AssertFrozenWitnessBinding(draft, frozen)
	if err == nil || !strings.Contains(err.Error(), "proposal digest, blinded incident-packet digest") {
		t.Fatalf("%v", err)
	}
	want := "Frozen witness does not match the immutable incident draft binding: proposal digest, blinded incident-packet digest, exact command digest, exact command bytes."
	if err.Error() != want {
		t.Fatalf("%v", err)
	}
}

func TestAssertBindingAcceptsAndChecksEachMember(t *testing.T) {
	draft, store := boundFixture(t)
	frozen := readFrozen(t, store, draft.Get("draftId").Str())
	if err := AssertFrozenWitnessBinding(draft, frozen); err != nil {
		t.Fatal(err)
	}
	const prefix = "Frozen witness does not match the immutable incident draft binding: "
	for _, c := range []struct {
		name   string
		mutate func(draft, frozen jsjson.Value)
		want   string
	}{
		{"frozen id", func(_, f jsjson.Value) { f.Get("proposal").Obj().Set("proposalId", str("other")) }, "proposal identifier"},
		{"binding id", func(d, _ jsjson.Value) { d.Get("review", "proposal").Obj().Set("proposalId", str("other")) }, "proposal identifier"},
		{"binding command digest", func(d, _ jsjson.Value) {
			d.Get("review", "proposal").Obj().Set("commandDigest", str("sha256:"+strings.Repeat("0", 64)))
		}, "exact command digest"},
		{"draft command digest", func(d, _ jsjson.Value) { d.Obj().Set("commandDigest", str("sha256:"+strings.Repeat("0", 64))) }, "exact command digest"},
		{"command bytes", func(d, _ jsjson.Value) { d.Obj().Set("command", str("pnpm test")) }, "exact command bytes"},
	} {
		d, f := mustParse(t, jsjson.Stringify(draft)), mustParse(t, jsjson.Stringify(frozen))
		c.mutate(d, f)
		if err := AssertFrozenWitnessBinding(d, f); err == nil || err.Error() != prefix+c.want+"." {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func strPtr(s string) *string { return &s }

func TestContinueRefusals(t *testing.T) {
	draft, store := boundFixture(t)
	id := draft.Get("draftId").Str()
	digest := readFrozen(t, store, id).Get("frozenDigest").Str()
	base := ContinueOptions{Repository: t.TempDir(), Store: store, Draft: draft, ExpectedFrozenDigest: digest}

	o := base
	o.ExpectedFrozenDigest = "sha256:" + strings.Repeat("0", 64)
	if _, err := Continue(o); err == nil || err.Error() !=
		"Incident cannot continue until its witness is human-approved and frozen intact: externally supplied frozen digest does not match" {
		t.Fatalf("digest: %v", err)
	}

	o = base
	o.Store = t.TempDir()
	if _, err := Continue(o); err == nil || err.Error() !=
		"Incident cannot continue until its witness is human-approved and frozen intact: human approval is missing; no witness may be frozen" {
		t.Fatalf("unapproved: %v", err)
	}

	mismatched, other := mismatchedStoreFixture(t)
	o = base
	o.Draft, o.Store = mismatched, other
	o.ExpectedFrozenDigest = readFrozen(t, other, id).Get("frozenDigest").Str()
	if _, err := Continue(o); err == nil || !strings.HasPrefix(err.Error(), "Frozen witness does not match the immutable incident draft binding: ") {
		t.Fatalf("binding: %v", err)
	}

	unbound := sampleDraft(t)
	o = base
	o.Draft = unbound
	if _, err := Continue(o); err == nil || err.Error() != "Incident draft has no proposal binding and cannot continue to investigation." {
		t.Fatalf("unbound: %v", err)
	}

	if _, err := Continue(base); err == nil || err.Error() !=
		"This incident has no selected runtime. Resolve a reviewed local runtime before intake, or supply --image <digest-pinned-image> for proof-grade replay." {
		t.Fatalf("no image: %v", err)
	}

	in := sampleInput()
	in.Runtime = jsjson.MustObject("requested", str("node"), "image", str(continueImage))
	withRuntime := boundDraft(t, in, mustProposal(t, store, id))
	o = base
	o.Draft, o.Image = withRuntime, strPtr("")
	if _, err := Continue(o); err == nil || err.Error() != "--image must match the digest-pinned runtime recorded in the immutable incident draft." {
		t.Fatalf("image mismatch: %v", err)
	}

	o = base
	o.Image, o.LedgerFile = strPtr(continueImage), strPtr(filepath.Join(t.TempDir(), "missing.json"))
	if _, err := Continue(o); err == nil || !strings.Contains(err.Error(), "missing.json") {
		t.Fatalf("ledger: %v", err)
	}
}
