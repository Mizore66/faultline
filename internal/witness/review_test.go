package witness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

const (
	reviewGenericRefusal = "Witness review refused: the stored proposal is not a valid strictly blinded protocol packet"
	reviewIntegrityHead  = "Witness review refused: proposal integrity verification failed: "
	zeroDigest           = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	existsPrefix         = "Immutable witness lock record already exists: "
)

func reviewProposalInput(id string) jsjson.Value {
	v, err := jsjson.Parse(`{"proposalId":"` + id + `","proposedAt":"2026-01-02T03:04:05.000Z","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.",
		"repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},
		"witness":{"behavior":"b","command":"node witness.mjs",
		"overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
		"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`)
	if err != nil {
		panic(err)
	}
	return v
}

func newReviewStore(t *testing.T, id string) (string, jsjson.Value) {
	t.Helper()
	store := t.TempDir()
	p, err := Propose(store, reviewProposalInput(id))
	if err != nil {
		t.Fatal(err)
	}
	return store, p
}

func parseJSON(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func copyObj(v jsjson.Value) *jsjson.Obj {
	o := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		o.Set(k, v.Obj().Field(k))
	}
	return o
}

func withoutKey(o *jsjson.Obj, key string) jsjson.Value {
	out := jsjson.NewObj()
	for _, k := range o.Keys() {
		if k != key {
			out.Set(k, o.Field(k))
		}
	}
	return jsjson.MakeObject(out)
}

func digestOf(v jsjson.Value) string { return jsexc.Must(canonical.DigestJSON(v)) }

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func requireFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != want {
		t.Fatalf("%s: mode %v, want %v", path, info.Mode().Perm(), want)
	}
}

// rewriteProposal rewrites the stored proposal. Digests named in keep are
// left as mutate set them; every other derived digest is recomputed, so a
// case breaks exactly one invariant.
func rewriteProposal(t *testing.T, store, id string, mutate func(p, w *jsjson.Obj), keep ...string) {
	t.Helper()
	path := filepath.Join(store, "proposals", id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := copyObj(parseJSON(t, string(raw)))
	w := copyObj(p.Field("witness"))
	mutate(p, w)
	kept := map[string]bool{}
	for _, k := range keep {
		kept[k] = true
	}
	if !kept["overlayDigest"] {
		var items []jsjson.Value
		for _, o := range w.Field("overlays").Items() {
			items = append(items, jsjson.MustObject("path", o.Get("path"), "bytesDigest", o.Get("bytesDigest")))
		}
		w.Set("overlayDigest", jsjson.MakeString(digestOf(jsjson.MakeArray(items))))
	}
	p.Set("witness", jsjson.MakeObject(w))
	if !kept["proposalDigest"] {
		p.Set("proposalDigest", jsjson.MakeString(digestOf(withoutKey(p, "proposalDigest"))))
	}
	text := jsexc.Must(canonical.CanonicalJSON(jsjson.MakeObject(p)))
	if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func overlay(path, b64, digest string) jsjson.Value {
	return jsjson.MustObject("path", jsjson.MakeString(path), "bytesBase64", jsjson.MakeString(b64), "bytesDigest", jsjson.MakeString(digest))
}

func requireReviewError(t *testing.T, err error, code, message string) {
	t.Helper()
	var re *WitnessReviewError
	if !errors.As(err, &re) {
		t.Fatalf("error %v is not a *WitnessReviewError", err)
	}
	if re.Code != code || re.Error() != message {
		t.Fatalf("got %s: %q\nwant %s: %q", re.Code, re.Error(), code, message)
	}
}

func TestOpenWitnessReview(t *testing.T) {
	store, proposal := newReviewStore(t, "r-1")
	review, err := OpenWitnessReview(store, proposal.Get("proposalId").Str())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range [][]string{{"schemaVersion"}, {"mode"}, {"safeguards", "packet"}, {"safeguards", "command"}, {"safeguards", "overlays"}, {"safeguards", "approval"}, {"safeguards", "freeze"}} {
		if review.Get(path...).Kind() != jsjson.String {
			t.Fatalf("missing %v", path)
		}
	}
	unsigned := jsjson.NewObj()
	for _, key := range review.Obj().Keys() {
		if key != "reviewDigest" {
			unsigned.Set(key, review.Obj().Field(key))
		}
	}
	want := jsexc.Must(canonical.DigestJSON(jsjson.MakeObject(unsigned)))
	if review.Get("reviewDigest").Str() != want {
		t.Fatalf("digest=%q want %q", review.Get("reviewDigest").Str(), want)
	}
	wantJSON := `{"schemaVersion":"faultline.witness-review.v1","mode":"LOCAL_READ_ONLY","proposal":` + jsjson.Stringify(proposal) +
		`,"safeguards":{"packet":"STRICTLY_BLINDED_PROTOCOL_SHAPE","command":"NOT_EXECUTED","overlays":"NOT_MATERIALIZED","approval":"EXPLICIT_HUMAN_ACTION_REQUIRED","freeze":"EXPLICIT_HUMAN_ACTION_REQUIRED"},"reviewDigest":"` + want + `"}`
	if got := jsjson.Stringify(review); got != wantJSON {
		t.Fatalf("review JSON\n got %s\nwant %s", got, wantJSON)
	}
	for _, d := range []string{"approvals", "frozen"} {
		entries, _ := os.ReadDir(filepath.Join(store, d))
		if len(entries) != 0 {
			t.Fatalf("%s written by open", d)
		}
	}
	if v := VerifyWitnessReview(review); !v.Valid || len(v.Errors) != 0 {
		t.Fatalf("verify %+v", v)
	}
}

func TestOpenWitnessReviewRefusals(t *testing.T) {
	t.Run("missing proposal", func(t *testing.T) {
		_, err := OpenWitnessReview(t.TempDir(), "nope")
		requireReviewError(t, err, "UNBLINDED_OR_INVALID_PROPOSAL", reviewGenericRefusal)
	})
	t.Run("malformed stored JSON", func(t *testing.T) {
		store, _ := newReviewStore(t, "bad")
		if err := os.WriteFile(filepath.Join(store, "proposals", "bad.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := OpenWitnessReview(store, "bad")
		requireReviewError(t, err, "UNBLINDED_OR_INVALID_PROPOSAL", reviewGenericRefusal)
	})
	t.Run("schema violation", func(t *testing.T) {
		store, _ := newReviewStore(t, "sv")
		rewriteProposal(t, store, "sv", func(p, w *jsjson.Obj) { p.Set("extra", jsjson.MakeString("x")) })
		_, err := OpenWitnessReview(store, "sv")
		requireReviewError(t, err, "UNBLINDED_OR_INVALID_PROPOSAL", reviewGenericRefusal)
	})
	t.Run("invalid id", func(t *testing.T) {
		_, err := OpenWitnessReview(t.TempDir(), "../x")
		requireReviewError(t, err, "UNBLINDED_OR_INVALID_PROPOSAL", reviewGenericRefusal)
	})
}

func TestVerifyWitnessReview(t *testing.T) {
	const b64 = "Y29uc29sZS5sb2coMSk="
	cases := []struct {
		name   string
		mutate func(p, w *jsjson.Obj)
		keep   []string
		want   string
	}{
		{"packet digest mutation", func(p, w *jsjson.Obj) { p.Set("incidentPacketDigest", jsjson.MakeString(zeroDigest)) }, nil,
			"incident packet digest does not match the proposal packet"},
		{"command digest mutation", func(p, w *jsjson.Obj) { w.Set("commandDigest", jsjson.MakeString(zeroDigest)) }, nil,
			"command digest does not match the exact UTF-8 command bytes"},
		{"case-insensitive duplicate overlay", func(p, w *jsjson.Obj) {
			good := w.Field("overlays").Items()[0].Get("bytesDigest").Str()
			w.Set("overlays", jsjson.MakeArray([]jsjson.Value{overlay("a.txt", b64, good), overlay("A.txt", b64, good)}))
		}, nil, "duplicate overlay path: A.txt"},
		{"wrong overlay bytes digest", func(p, w *jsjson.Obj) {
			w.Set("overlays", jsjson.MakeArray([]jsjson.Value{overlay("witness.mjs", b64, zeroDigest)}))
		}, nil, "overlay byte digest does not match: witness.mjs"},
		{"noncanonical overlay order", func(p, w *jsjson.Obj) {
			good := w.Field("overlays").Items()[0].Get("bytesDigest").Str()
			w.Set("overlays", jsjson.MakeArray([]jsjson.Value{overlay("b.txt", b64, good), overlay("a.txt", b64, good)}))
		}, nil, "overlay records are not in canonical path order"},
		{"wrong ordered overlay digest", func(p, w *jsjson.Obj) { w.Set("overlayDigest", jsjson.MakeString(zeroDigest)) }, []string{"overlayDigest"},
			"overlay digest does not match the ordered overlay byte digests"},
		{"wrong proposal digest", func(p, w *jsjson.Obj) { p.Set("proposalDigest", jsjson.MakeString(zeroDigest)) }, []string{"proposalDigest"},
			"proposal digest does not match the immutable proposal payload"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, _ := newReviewStore(t, "v")
			rewriteProposal(t, store, "v", c.mutate, c.keep...)
			_, err := OpenWitnessReview(store, "v")
			requireReviewError(t, err, "PROPOSAL_INTEGRITY_FAILURE", reviewIntegrityHead+c.want)
		})
	}

	t.Run("errors are joined in frozen order", func(t *testing.T) {
		store, _ := newReviewStore(t, "v")
		rewriteProposal(t, store, "v", func(p, w *jsjson.Obj) {
			p.Set("incidentPacketDigest", jsjson.MakeString(zeroDigest))
			w.Set("commandDigest", jsjson.MakeString(zeroDigest))
			w.Set("overlayDigest", jsjson.MakeString(zeroDigest))
		}, "overlayDigest")
		_, err := OpenWitnessReview(store, "v")
		requireReviewError(t, err, "PROPOSAL_INTEGRITY_FAILURE", reviewIntegrityHead+
			"incident packet digest does not match the proposal packet; command digest does not match the exact UTF-8 command bytes; overlay digest does not match the ordered overlay byte digests")
	})

	t.Run("exported snapshots are verified without a store", func(t *testing.T) {
		store, _ := newReviewStore(t, "v")
		review := jsexc.Must(OpenWitnessReview(store, "v"))
		check := func(name string, v jsjson.Value, want string) {
			t.Helper()
			got := VerifyWitnessReview(v)
			if got.Valid || len(got.Errors) != 1 || got.Errors[0] != want {
				t.Fatalf("%s: %+v", name, got)
			}
		}
		check("not an object", jsjson.MakeString("x"), "Witness review is not a valid local review snapshot")
		extra := copyObj(review)
		extra.Set("extra", jsjson.MakeNumber(1))
		check("extra key", jsjson.MakeObject(extra), "Witness review is not a valid local review snapshot")
		bad := copyObj(review)
		bad.Set("reviewDigest", jsjson.MakeString(zeroDigest))
		check("digest", jsjson.MakeObject(bad), "Witness review digest does not match its immutable review payload")

		// A self-consistent review of a corrupted proposal.
		prop := copyObj(review.Get("proposal"))
		w := copyObj(prop.Field("witness"))
		w.Set("commandDigest", jsjson.MakeString(zeroDigest))
		prop.Set("witness", jsjson.MakeObject(w))
		signedReview := copyObj(review)
		signedReview.Set("proposal", jsjson.MakeObject(prop))
		signedReview.Set("reviewDigest", jsjson.MakeString(digestOf(withoutKey(signedReview, "reviewDigest"))))
		check("invalid proposal", jsjson.MakeObject(signedReview),
			"Witness review contains an invalid proposed witness: command digest does not match the exact UTF-8 command bytes; proposal digest does not match the immutable proposal payload")
	})
}

func approvalInput(t *testing.T, review jsjson.Value, extra string) jsjson.Value {
	t.Helper()
	return parseJSON(t, `{"reviewDigest":"`+review.Get("reviewDigest").Str()+`","approvedBy":"  reviewer@example.test  ","approvedAt":"2026-01-02T03:05:00.000Z","note":"looks right"`+extra+`}`)
}

func TestWitnessReviewActions(t *testing.T) {
	t.Run("two-step approve then freeze writes verified write-once records", func(t *testing.T) {
		store, proposal := newReviewStore(t, "a1")
		review := jsexc.Must(OpenWitnessReview(store, "a1"))
		approval, err := ApproveWitnessReview(store, review, approvalInput(t, review, ""))
		if err != nil {
			t.Fatal(err)
		}
		if approval.Get("approvedBy").Str() != "reviewer@example.test" || approval.Get("proposalDigest").Str() != proposal.Get("proposalDigest").Str() ||
			approval.Get("approvedAt").Str() != "2026-01-02T03:05:00.000Z" || approval.Get("note").Str() != "looks right" || approval.Get("reviewerType").Str() != "HUMAN" {
			t.Fatalf("approval %s", jsjson.Stringify(approval))
		}
		requireFileMode(t, filepath.Join(store, "approvals", "a1.json"), 0o600)
		frozen, err := FreezeWitnessReview(store, review, parseJSON(t, `{"reviewDigest":"`+review.Get("reviewDigest").Str()+`","frozenAt":"2026-01-02T03:06:00.000Z"}`))
		if err != nil {
			t.Fatal(err)
		}
		if frozen.Get("frozenAt").Str() != "2026-01-02T03:06:00.000Z" || jsjson.Stringify(frozen.Get("proposal")) != jsjson.Stringify(proposal) {
			t.Fatalf("frozen %s", jsjson.Stringify(frozen))
		}
		requireFileMode(t, filepath.Join(store, "frozen", "a1.json"), 0o600)
		if v := VerifyFrozen(store, "a1", frozen.Get("frozenDigest").Str(), true); !v.Valid || v.ExternalDigestStatus != "MATCH" {
			t.Fatalf("%+v", v)
		}
		if _, err := ApproveWitnessReview(store, review, approvalInput(t, review, "")); err == nil || !strings.HasPrefix(err.Error(), existsPrefix) {
			t.Fatalf("second approve: %v", err)
		}
	})

	t.Run("atomic approve and freeze", func(t *testing.T) {
		store, _ := newReviewStore(t, "a2")
		review := jsexc.Must(OpenWitnessReview(store, "a2"))
		approval, frozen, err := ApproveAndFreezeWitnessReview(store, review, approvalInput(t, review, `,"frozenAt":"2026-01-02T03:07:00.000Z"`))
		if err != nil {
			t.Fatal(err)
		}
		if frozen.Get("frozenAt").Str() != "2026-01-02T03:07:00.000Z" || jsjson.Stringify(frozen.Get("approval")) != jsjson.Stringify(approval) {
			t.Fatalf("frozen %s", jsjson.Stringify(frozen))
		}
		stored := jsexc.Must(ReadFrozen(store, "a2"))
		if jsjson.Stringify(stored) != jsjson.Stringify(frozen) {
			t.Fatal("stored frozen differs")
		}
	})

	t.Run("freeze without approval is refused by the store", func(t *testing.T) {
		store, _ := newReviewStore(t, "a3")
		review := jsexc.Must(OpenWitnessReview(store, "a3"))
		_, err := FreezeWitnessReview(store, review, parseJSON(t, `{"reviewDigest":"`+review.Get("reviewDigest").Str()+`"}`))
		if err == nil || err.Error() != "Cannot freeze an unapproved proposal: a3" {
			t.Fatalf("%v", err)
		}
	})

	t.Run("malformed exported review performs no store I/O", func(t *testing.T) {
		store := filepath.Join(t.TempDir(), "never-created")
		bad := parseJSON(t, `{"schemaVersion":"nope"}`)
		in := parseJSON(t, `{"reviewDigest":"`+zeroDigest+`","approvedBy":"r"}`)
		const want = "Witness review is not a valid local review snapshot"
		_, err := ApproveWitnessReview(store, bad, in)
		requireReviewError(t, err, "INVALID_REVIEW", want)
		_, err = FreezeWitnessReview(store, bad, in)
		requireReviewError(t, err, "INVALID_REVIEW", want)
		_, _, err = ApproveAndFreezeWitnessReview(store, bad, in)
		requireReviewError(t, err, "INVALID_REVIEW", want)
		if exists(store) {
			t.Fatal("store touched")
		}
	})

	t.Run("wrong review digest", func(t *testing.T) {
		store, _ := newReviewStore(t, "a4")
		review := jsexc.Must(OpenWitnessReview(store, "a4"))
		in := parseJSON(t, `{"reviewDigest":"`+zeroDigest+`","approvedBy":"r"}`)
		const want = "Explicit review confirmation does not match the review snapshot"
		_, err := ApproveWitnessReview(store, review, in)
		requireReviewError(t, err, "REVIEW_CONFIRMATION_MISMATCH", want)
		_, err = FreezeWitnessReview(store, review, parseJSON(t, `{"reviewDigest":"`+zeroDigest+`"}`))
		requireReviewError(t, err, "REVIEW_CONFIRMATION_MISMATCH", want)
		_, _, err = ApproveAndFreezeWitnessReview(store, review, in)
		requireReviewError(t, err, "REVIEW_CONFIRMATION_MISMATCH", want)
		if exists(filepath.Join(store, "approvals", "a4.json")) {
			t.Fatal("approval written")
		}
	})

	t.Run("changed current proposal is stale", func(t *testing.T) {
		store, _ := newReviewStore(t, "a5")
		review := jsexc.Must(OpenWitnessReview(store, "a5"))
		rewriteProposal(t, store, "a5", func(p, w *jsjson.Obj) { p.Set("proposedAt", jsjson.MakeString("2026-02-02T00:00:00.000Z")) })
		const want = "Witness review is stale because the stored proposal no longer exactly matches the reviewed snapshot"
		_, err := ApproveWitnessReview(store, review, approvalInput(t, review, ""))
		requireReviewError(t, err, "STALE_REVIEW", want)
		_, err = FreezeWitnessReview(store, review, parseJSON(t, `{"reviewDigest":"`+review.Get("reviewDigest").Str()+`"}`))
		requireReviewError(t, err, "STALE_REVIEW", want)
		_, _, err = ApproveAndFreezeWitnessReview(store, review, approvalInput(t, review, ""))
		requireReviewError(t, err, "STALE_REVIEW", want)
		if exists(filepath.Join(store, "approvals", "a5.json")) {
			t.Fatal("approval written")
		}
	})

	t.Run("removed current proposal is refused generically", func(t *testing.T) {
		store, _ := newReviewStore(t, "a6")
		review := jsexc.Must(OpenWitnessReview(store, "a6"))
		if err := os.Remove(filepath.Join(store, "proposals", "a6.json")); err != nil {
			t.Fatal(err)
		}
		_, err := ApproveWitnessReview(store, review, approvalInput(t, review, ""))
		requireReviewError(t, err, "UNBLINDED_OR_INVALID_PROPOSAL", reviewGenericRefusal)
	})

	t.Run("input refusals create no record", func(t *testing.T) {
		store, _ := newReviewStore(t, "a7")
		review := jsexc.Must(OpenWitnessReview(store, "a7"))
		digest := review.Get("reviewDigest").Str()
		for name, body := range map[string]string{
			"empty reviewer": `{"reviewDigest":"` + digest + `","approvedBy":""}`,
			"blank reviewer": `{"reviewDigest":"` + digest + `","approvedBy":"   "}`,
			"long reviewer":  `{"reviewDigest":"` + digest + `","approvedBy":"` + strings.Repeat("r", 513) + `"}`,
			"long note":      `{"reviewDigest":"` + digest + `","approvedBy":"r","note":"` + strings.Repeat("n", 8001) + `"}`,
			"unknown key":    `{"reviewDigest":"` + digest + `","approvedBy":"r","x":1}`,
			"missing digest": `{"approvedBy":"r"}`,
			"bad approvedAt": `{"reviewDigest":"` + digest + `","approvedBy":"r","approvedAt":"yesterday"}`,
			"bad frozenAt":   `{"reviewDigest":"` + digest + `","approvedBy":"r","frozenAt":"yesterday"}`,
		} {
			in := parseJSON(t, body)
			if _, err := ApproveWitnessReview(store, review, in); err == nil {
				t.Fatalf("%s: approve accepted", name)
			}
			if _, _, err := ApproveAndFreezeWitnessReview(store, review, in); err == nil {
				t.Fatalf("%s: atomic accepted", name)
			}
		}
		if _, err := FreezeWitnessReview(store, review, parseJSON(t, `{"reviewDigest":"`+digest+`","approvedBy":"r"}`)); err == nil {
			t.Fatal("freeze accepted unknown key")
		}
		for _, d := range []string{"approvals", "frozen"} {
			if entries, _ := os.ReadDir(filepath.Join(store, d)); len(entries) != 0 {
				t.Fatalf("%s has records", d)
			}
		}
		ok := parseJSON(t, `{"reviewDigest":"`+digest+`","approvedBy":"`+strings.Repeat("r", 512)+`","note":"`+strings.Repeat("n", 8000)+`"}`)
		if _, err := ApproveWitnessReview(store, review, ok); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("atomic partial failure leaves approval and preserves existing frozen bytes", func(t *testing.T) {
		store, _ := newReviewStore(t, "p1")
		review := jsexc.Must(OpenWitnessReview(store, "p1"))
		frozenPath := filepath.Join(store, "frozen", "p1.json")
		pre := []byte("pre-existing frozen bytes\n")
		if err := os.WriteFile(frozenPath, pre, 0o600); err != nil {
			t.Fatal(err)
		}
		approval, frozen, err := ApproveAndFreezeWitnessReview(store, review, approvalInput(t, review, ""))
		if err == nil || !strings.HasPrefix(err.Error(), existsPrefix) {
			t.Fatalf("err %v", err)
		}
		if approval.Kind() != jsjson.Undefined || frozen.Kind() != jsjson.Undefined {
			t.Fatal("results returned on failure")
		}
		stored, rerr := ReadApproval(store, "p1")
		if rerr != nil || stored.Get("approvedBy").Str() != "reviewer@example.test" {
			t.Fatalf("approval not kept: %v", rerr)
		}
		got, _ := os.ReadFile(frozenPath)
		if string(got) != string(pre) {
			t.Fatalf("frozen changed: %q", got)
		}
	})

	t.Run("atomic with existing approval fails write-once and keeps it", func(t *testing.T) {
		store, _ := newReviewStore(t, "p2")
		review := jsexc.Must(OpenWitnessReview(store, "p2"))
		if _, err := Approve(store, "p2", parseJSON(t, `{"approvedBy":"first","approvedAt":"2026-01-02T03:05:00.000Z"}`)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(store, "approvals", "p2.json")
		before, _ := os.ReadFile(path)
		_, _, err := ApproveAndFreezeWitnessReview(store, review, approvalInput(t, review, ""))
		if err == nil || !strings.HasPrefix(err.Error(), existsPrefix) {
			t.Fatalf("err %v", err)
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("approval changed")
		}
		if exists(filepath.Join(store, "frozen", "p2.json")) {
			t.Fatal("frozen written")
		}
	})
}
