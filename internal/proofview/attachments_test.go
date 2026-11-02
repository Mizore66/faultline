package proofview

import (
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func serveBundleCorpus(t *testing.T) *servefixture.Corpus {
	t.Helper()
	corpus, err := servefixture.Load(filepath.Join("..", "..", "difftest", "testdata", "serve-bundle"))
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

func serveBundleCase(t *testing.T, corpus *servefixture.Corpus, id string) servefixture.Case {
	t.Helper()
	for _, c := range corpus.Cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("retained case %s is missing", id)
	return servefixture.Case{}
}

// attachmentInputs copies the case's fixture into a fresh directory and
// resolves the case's path options against that copy (not the retained tree),
// leaving expected digests literal. Retained paths and digests are dynamic
// metadata read from the corpus, never constants in this file.
func attachmentInputs(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case) (bundle string, expectedRoot *string, options AttachmentOptions) {
	t.Helper()
	copyRoot := filepath.Join(t.TempDir(), "fixture")
	if err := corpus.CopyFixture(c.Fixture, copyRoot); err != nil {
		t.Fatal(err)
	}
	in := func(p *string) *string {
		if p == nil {
			return nil
		}
		joined := filepath.Join(copyRoot, filepath.FromSlash(*p))
		return &joined
	}
	return filepath.Join(copyRoot, "bundle"), c.ExpectedRoot, AttachmentOptions{
		MinimizationFile:           in(c.Options.MinimizationFile),
		ExpectedMinimizationDigest: c.Options.ExpectedMinimizationDigest,
		RepairDirectory:            in(c.Options.RepairDirectory),
		ExpectedRepairDigest:       c.Options.ExpectedRepairDigest,
		PreventionDirectory:        in(c.Options.PreventionDirectory),
		ExpectedPreventionDigest:   c.Options.ExpectedPreventionDigest,
	}
}

func str(s string) jsjson.Value { return jsjson.MakeString(s) }

// projectView is an independent re-statement of the frozen TS member order of
// JSON.stringify(VerifiedGitProofView). It deliberately does not canonicalize:
// the member order inside every artifact is whatever the loader returned.
func projectView(v View) string {
	nullable := func(present bool, build func() jsjson.Value) jsjson.Value {
		if !present {
			return jsjson.MakeNull()
		}
		return build()
	}
	a := v.Attachments
	attachments := jsjson.MustObject(
		"minimization", nullable(a.Minimization != nil, func() jsjson.Value {
			return jsjson.MustObject(
				"result", a.Minimization.Result,
				"resultDigest", str(a.Minimization.ResultDigest),
				"externalDigestStatus", str(a.Minimization.ExternalDigestStatus))
		}),
		"repair", nullable(a.Repair != nil, func() jsjson.Value {
			return jsjson.MustObject(
				"manifest", a.Repair.Manifest,
				"packet", a.Repair.Packet,
				"brief", a.Repair.Brief,
				"externalDigestStatus", str(a.Repair.ExternalDigestStatus))
		}),
		"prevention", nullable(a.Prevention != nil, func() jsjson.Value {
			return jsjson.MustObject(
				"manifest", a.Prevention.Manifest,
				"prevention", a.Prevention.Prevention,
				"rootDigest", str(a.Prevention.RootDigest),
				"externalDigestStatus", str(a.Prevention.ExternalDigestStatus))
		}),
	)
	return jsjson.Stringify(jsjson.MustObject(
		"rootDigest", str(v.RootDigest),
		"checkedFiles", jsjson.MakeNumber(float64(v.CheckedFiles)),
		"externalRootStatus", str(v.ExternalRootStatus),
		"manifest", v.Manifest,
		"investigation", v.Investigation,
		"frozenWitness", v.FrozenWitness,
		"attachments", attachments,
	))
}

// Existing-API regression: the unchanged public loader must keep reporting
// that no external root was checked when none was supplied. Catches promoting
// the internally pinned root to an external MATCH.
func TestAttachmentBaseExternalStatusIsNotPromoted(t *testing.T) {
	dir := filepath.Join("..", "..", "difftest", "testdata", "serve-bundle", "inputs", "coherent", "bundle")
	v, err := LoadVerifiedGitProofView(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.ExternalRootStatus != "NOT_PROVIDED" {
		t.Fatalf("status %q", v.ExternalRootStatus)
	}
}

// The old two-argument loader must delegate with zero options: no attachment
// can ever appear from it, even beside a fixture that holds all of them.
func TestAttachmentOldLoaderAttachesNothing(t *testing.T) {
	corpus := serveBundleCorpus(t)
	bundle, root, _ := attachmentInputs(t, corpus, serveBundleCase(t, corpus, "view-combination-111"))
	v, err := LoadVerifiedGitProofView(bundle, root)
	if err != nil {
		t.Fatal(err)
	}
	if v.Attachments != (VerifiedAttachments{}) {
		t.Fatalf("old loader attached %+v", v.Attachments)
	}
}

// The new entry point with zero options must equal the old loader exactly, and
// expected digests that have no path must neither touch the filesystem nor
// attach anything or change the base view.
func TestAttachmentZeroAndDigestOnlyOptionsEqualOldLoader(t *testing.T) {
	corpus := serveBundleCorpus(t)
	c := serveBundleCase(t, corpus, "view-root-match")
	bundle, root, _ := attachmentInputs(t, corpus, c)
	old, err := LoadVerifiedGitProofView(bundle, root)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	for name, options := range map[string]AttachmentOptions{
		"zero": {},
		"digests-only": {
			ExpectedMinimizationDigest: &digest,
			ExpectedRepairDigest:       &digest,
			ExpectedPreventionDigest:   &digest,
		},
	} {
		got, err := LoadVerifiedGitProofViewWithAttachments(bundle, root, options)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Attachments != (VerifiedAttachments{}) {
			t.Fatalf("%s: unexpected attachments %+v", name, got.Attachments)
		}
		if projectView(got) != projectView(old) {
			t.Fatalf("%s: view differs from the old loader", name)
		}
	}
}

// Every one of the retained view cases (success and refusal) against the
// frozen TS capture: success/failure, the complete View projection with TS
// member order, and the exact thrown message. Catches a missing attachment,
// an unenforced binding, a wrong snapshot and a reordered or canonicalized
// artifact.
func TestAttachmentViewMatchesTSCapture(t *testing.T) {
	corpus := serveBundleCorpus(t)
	for _, c := range corpus.Cases {
		if c.Kind != "view" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			golden := corpus.Goldens[c.ID]
			bundle, root, options := attachmentInputs(t, corpus, c)
			view, err := LoadVerifiedGitProofViewWithAttachments(bundle, root, options)
			if golden.Error != nil {
				if err == nil {
					t.Fatalf("loader succeeded, TS refused with %q", *golden.Error)
				}
				if got := jsexc.Message(err); got != *golden.Error {
					t.Fatalf("message\n got %q\nwant %q", got, *golden.Error)
				}
				if view != (View{}) {
					t.Fatalf("a failed load must return the zero View")
				}
				return
			}
			if err != nil {
				t.Fatalf("loader failed, TS succeeded: %v", err)
			}
			if got := projectView(view); got != *golden.ValueJSON {
				t.Fatalf("View projection differs from TS JSON.stringify\n got %s\nwant %s", got, *golden.ValueJSON)
			}
		})
	}
}

// All eight presence combinations, bits meaning minimization/repair/prevention.
// The expectation is the bit pattern itself, not the loader's own output.
func TestAttachmentAllEightCombinations(t *testing.T) {
	corpus := serveBundleCorpus(t)
	for bits := 0; bits < 8; bits++ {
		id := "view-combination-" + string([]byte{'0' + byte(bits>>2&1), '0' + byte(bits>>1&1), '0' + byte(bits&1)})
		t.Run(id, func(t *testing.T) {
			bundle, root, options := attachmentInputs(t, corpus, serveBundleCase(t, corpus, id))
			view, err := LoadVerifiedGitProofViewWithAttachments(bundle, root, options)
			if err != nil {
				t.Fatal(err)
			}
			a := view.Attachments
			for name, present := range map[string][2]bool{
				"minimization": {a.Minimization != nil, bits&4 != 0},
				"repair":       {a.Repair != nil, bits&2 != 0},
				"prevention":   {a.Prevention != nil, bits&1 != 0},
			} {
				if present[0] != present[1] {
					t.Fatalf("%s attached = %v, want %v", name, present[0], present[1])
				}
			}
			if view.ExternalRootStatus != "MATCH" {
				t.Fatalf("base external status %q", view.ExternalRootStatus)
			}
		})
	}
}

// Expected digests are optional and independent. Absent never fails and is not
// reported as MATCH; the matching digest reports MATCH; a wrong digest refuses.
// Prevention's own RootDigest is the retained verified root either way.
func TestAttachmentDigestAsymmetry(t *testing.T) {
	corpus := serveBundleCorpus(t)
	load := func(t *testing.T, id string) (View, error) {
		bundle, root, options := attachmentInputs(t, corpus, serveBundleCase(t, corpus, id))
		return LoadVerifiedGitProofViewWithAttachments(bundle, root, options)
	}
	t.Run("minimization", func(t *testing.T) {
		absent, err := load(t, "view-min-expected-absent")
		if err != nil || absent.Attachments.Minimization == nil {
			t.Fatalf("absent digest must attach: %v", err)
		}
		match, err := load(t, "view-min-expected-match")
		if err != nil || match.Attachments.Minimization == nil {
			t.Fatalf("matching digest must attach: %v", err)
		}
		if match.Attachments.Minimization.ExternalDigestStatus != "MATCH" || absent.Attachments.Minimization.ExternalDigestStatus == "MATCH" {
			t.Fatalf("statuses absent=%q match=%q", absent.Attachments.Minimization.ExternalDigestStatus, match.Attachments.Minimization.ExternalDigestStatus)
		}
		want := serveBundleCase(t, corpus, "view-min-expected-match").Options.ExpectedMinimizationDigest
		if want == nil || absent.Attachments.Minimization.ResultDigest != *want || match.Attachments.Minimization.ResultDigest != *want {
			t.Fatalf("result digest is not the retained digest")
		}
		if _, err := load(t, "view-min-expected-wrong"); err == nil {
			t.Fatal("wrong minimization digest must refuse")
		}
	})
	t.Run("repair", func(t *testing.T) {
		absent, err := load(t, "view-repair-expected-absent")
		if err != nil || absent.Attachments.Repair == nil {
			t.Fatalf("absent digest must attach: %v", err)
		}
		match, err := load(t, "view-repair-expected-match")
		if err != nil || match.Attachments.Repair == nil {
			t.Fatalf("matching digest must attach: %v", err)
		}
		if match.Attachments.Repair.ExternalDigestStatus != "MATCH" || absent.Attachments.Repair.ExternalDigestStatus == "MATCH" {
			t.Fatalf("statuses absent=%q match=%q", absent.Attachments.Repair.ExternalDigestStatus, match.Attachments.Repair.ExternalDigestStatus)
		}
		if _, err := load(t, "view-repair-expected-wrong"); err == nil {
			t.Fatal("wrong repair digest must refuse")
		}
	})
	t.Run("prevention", func(t *testing.T) {
		absent, err := load(t, "view-prevention-expected-absent")
		if err != nil || absent.Attachments.Prevention == nil {
			t.Fatalf("absent digest must attach: %v", err)
		}
		match, err := load(t, "view-prevention-expected-match")
		if err != nil || match.Attachments.Prevention == nil {
			t.Fatalf("matching digest must attach: %v", err)
		}
		if match.Attachments.Prevention.ExternalDigestStatus != "MATCH" || absent.Attachments.Prevention.ExternalDigestStatus == "MATCH" {
			t.Fatalf("statuses absent=%q match=%q", absent.Attachments.Prevention.ExternalDigestStatus, match.Attachments.Prevention.ExternalDigestStatus)
		}
		want := serveBundleCase(t, corpus, "view-prevention-expected-match").Options.ExpectedPreventionDigest
		if want == nil || absent.Attachments.Prevention.RootDigest != *want || match.Attachments.Prevention.RootDigest != *want {
			t.Fatalf("prevention root digest is not the retained root")
		}
		if _, err := load(t, "view-prevention-expected-wrong"); err == nil {
			t.Fatal("wrong prevention digest must refuse")
		}
	})
}

// Valid-but-weaker artifacts stay attachable: no proof/status gate. Catches an
// invented isProof or classification requirement.
func TestAttachmentValidUncertifiedSummaryAndNoInvestigationAttach(t *testing.T) {
	corpus := serveBundleCorpus(t)
	load := func(t *testing.T, id string) View {
		bundle, root, options := attachmentInputs(t, corpus, serveBundleCase(t, corpus, id))
		view, err := LoadVerifiedGitProofViewWithAttachments(bundle, root, options)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return view
	}
	if m := load(t, "view-min-uncertified").Attachments.Minimization; m == nil || m.Result.Get("proof", "isProof").Bool() {
		t.Fatalf("uncertified minimization must attach with isProof=false: %+v", m)
	}
	if p := load(t, "view-prevention-summary").Attachments.Prevention; p == nil || p.Manifest.Get("classification").Str() != "PREVENTION_EVIDENCE_SUMMARY" {
		t.Fatalf("summary prevention must attach: %+v", p)
	}
	if p := load(t, "view-prevention-verified").Attachments.Prevention; p == nil || p.Manifest.Get("classification").Str() != "PREVENTION_VERIFIED" {
		t.Fatalf("verified prevention must attach: %+v", p)
	}
	p := load(t, "view-prevention-investigation-absent").Attachments.Prevention
	if p == nil {
		t.Fatal("prevention without investigationDigest must attach")
	}
	if got := p.Prevention.Get("investigationDigest").Kind(); got != jsjson.Undefined {
		t.Fatalf("investigationDigest kind %v, the retained body has none", got)
	}
}

// Library level: each attachment's path and digest are independently optional
// and a nil pointer is "undefined", not the empty string. A supplied empty
// path is a real path (it resolves, like TS) and so cannot silently attach
// nothing.
func TestAttachmentLibraryArgumentsAreIndependentlyOptional(t *testing.T) {
	corpus := serveBundleCorpus(t)
	bundle, root, all := attachmentInputs(t, corpus, serveBundleCase(t, corpus, "view-combination-111"))
	empty := ""
	for name, options := range map[string]AttachmentOptions{
		"minimization-only": {MinimizationFile: all.MinimizationFile},
		"repair-only":       {RepairDirectory: all.RepairDirectory},
		"prevention-only":   {PreventionDirectory: all.PreventionDirectory},
	} {
		view, err := LoadVerifiedGitProofViewWithAttachments(bundle, root, options)
		if err != nil {
			t.Fatalf("%s without any digest: %v", name, err)
		}
		a := view.Attachments
		if (a.Minimization != nil) != (options.MinimizationFile != nil) ||
			(a.Repair != nil) != (options.RepairDirectory != nil) ||
			(a.Prevention != nil) != (options.PreventionDirectory != nil) {
			t.Fatalf("%s: wrong attachments %+v", name, a)
		}
	}
	for name, options := range map[string]AttachmentOptions{
		"empty-minimization": {MinimizationFile: &empty},
		"empty-repair":       {RepairDirectory: &empty},
		"empty-prevention":   {PreventionDirectory: &empty},
	} {
		if _, err := LoadVerifiedGitProofViewWithAttachments(bundle, root, options); err == nil {
			t.Fatalf("%s: an empty path resolves to the working directory and must be refused, not ignored", name)
		}
	}
}
