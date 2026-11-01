package proofview

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// panelsRenderCase loads one retained view case through the real
// attachment-aware loader (fresh fixture copy, real verifiers) and returns the
// complete rendered page bytes together with the retained TS page bytes.
func panelsRenderCase(t *testing.T, id string) (got, want []byte) {
	t.Helper()
	corpus := serveBundleCorpus(t)
	c := serveBundleCase(t, corpus, id)
	bundle, root, options := attachmentInputs(t, corpus, c)
	view, err := LoadVerifiedGitProofViewWithAttachments(bundle, root, options)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	want, err = corpus.Page(id)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return []byte(jsstr.ToUTF8(RenderGitProofIncidentPage(view))), want
}

// panelsFirstDiff reports the first differing byte offset with a short window of
// each side, so a failure never prints a whole page.
func panelsFirstDiff(got, want []byte) string {
	n := min(len(got), len(want))
	i := 0
	for i < n && got[i] == want[i] {
		i++
	}
	window := func(b []byte) string {
		lo := max(i-40, 0)
		hi := min(i+80, len(b))
		return fmt.Sprintf("%q", b[lo:hi])
	}
	return fmt.Sprintf("first difference at byte %d (got %d bytes, want %d bytes)\n got ...%s\nwant ...%s", i, len(got), len(want), window(got), window(want))
}

func panelsRequireExact(t *testing.T, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatal(panelsFirstDiff(got, want))
	}
}

// All eight attachment presence combinations rendered through the real loader
// against the retained TS page bytes. Catches renderers that still emit the
// absent panel (or a wrong hero summary) when an attachment is supplied, and
// any drift in the shared chrome. The IDs are enumerated here, independently of
// the corpus order, so a dropped case cannot make the loop vacuous.
func TestAttachmentPanelsFullPage(t *testing.T) {
	for _, bits := range []string{"000", "001", "010", "011", "100", "101", "110", "111"} {
		id := "view-combination-" + bits
		t.Run(id, func(t *testing.T) {
			got, want := panelsRenderCase(t, id)
			panelsRequireExact(t, got, want)
		})
	}
}

// Six selected single-attachment/no-attachment success pages beyond the eight
// combinations (not every retained success page; full replay of all fifty view
// cases belongs to the tagged TestServeBundleReplay): root absent/match,
// minimization-uncertified, both prevention classifications and the
// no-investigation prevention. Catches classification-specific panel text.
func TestAttachmentPanelsFullPageSingleAttachmentVariants(t *testing.T) {
	for _, id := range []string{
		"view-root-absent",
		"view-root-match",
		"view-min-uncertified",
		"view-prevention-summary",
		"view-prevention-verified",
		"view-prevention-investigation-absent",
	} {
		t.Run(id, func(t *testing.T) {
			got, want := panelsRenderCase(t, id)
			panelsRequireExact(t, got, want)
		})
	}
}

// The fourteen private strings retained for the coherent fixture. Provenance:
// copied verbatim from "privateSentinels" in
// difftest/testdata/serve-bundle/fixtures.json. The list is deliberately fixed
// for the current immutable corpus (the corpus loader exports no accessor and
// this test must not reconstruct the list from the artifacts it is auditing).
// They are free-form artifact text, paths and diagnostics that a shareable
// proof page must never carry.
var panelsPrivateSentinels = []string{
	"state.txt must not be bad",
	"node witness.mjs",
	"FL_PRIVATE_OVERLAY_5C_NOT_FOR_HTML",
	"reviewer@example.test",
	"The repository state must not be bad.",
	"state.txt became bad",
	"/tmp/faultline-git-proof-repository-zUChrR",
	"FL_PRIVATE_INVARIANT_5C_NOT_FOR_HTML",
	"FL_PRIVATE_REPAIR_5C_NOT_FOR_HTML",
	"FL_PRIVATE_HARD_5C_NOT_FOR_HTML",
	"FL_PRIVATE_SOFT_5C_NOT_FOR_HTML",
	"FL_PRIVATE_DIAGNOSTIC_5C_NOT_FOR_HTML",
	"witness failed",
	"state=bad",
}

// Private cases: full retained bytes (all three attachments with verified
// prevention; repair plus verified prevention for precedence; prevention alone
// for the hero quirk) and, independently of the byte comparison, absence of
// every private sentinel from the rendered page. Catches projecting free-form
// invariant, repair, hard/soft guidance or diagnostic text, and the wrong
// precedence between prevention and repair guidance.
func TestAttachmentPanelsPrivateCases(t *testing.T) {
	for _, id := range []string{
		"view-private-sentinels",
		"view-private-prevention-priority",
		"view-private-prevention-only-hero",
	} {
		t.Run(id, func(t *testing.T) {
			got, want := panelsRenderCase(t, id)
			for _, sentinel := range panelsPrivateSentinels {
				if strings.Contains(string(got), sentinel) {
					t.Errorf("rendered page leaks private sentinel %q", sentinel)
				}
			}
			panelsRequireExact(t, got, want)
		})
	}
}

// ---- Phase B: pure rendering of synthetic Views -----------------------------
//
// These Views reuse the one real verified base load (loadFacts) and replace only
// Attachments with hand-built values. They are renderer-defense tests, NOT
// claims that such artifacts could pass the real verifiers. Every expected
// string is written out literally (never produced by EscapeHTML, shortDigest or
// the panel code under test). They render through the public
// RenderGitProofIncidentPage and do not touch the filesystem beyond the shared
// base load.

const (
	panelsAbsentHero   = "It distinguishes an observed boundary from downstream artifacts that were not supplied."
	panelsSuppliedHero = "It separately verifies each supplied downstream artifact and its applicable evidence binding before rendering it."
)

func panelsStrings(ss ...string) jsjson.Value {
	items := make([]jsjson.Value, len(ss))
	for i, s := range ss {
		items[i] = str(s)
	}
	return jsjson.MakeArray(items)
}

func panelsCiting(ids ...string) jsjson.Value {
	return jsjson.MustObject("evidenceIds", panelsStrings(ids...))
}

func panelsPureView(t *testing.T, a VerifiedAttachments) View {
	t.Helper()
	v := loadFacts(t).view
	v.Attachments = a
	return v
}

// panelsRequireFragment fails with a concise first-difference window when the
// exact expected panel is not in the page.
func panelsRequireFragment(t *testing.T, page, want string) {
	t.Helper()
	if strings.Contains(page, want) {
		return
	}
	head, _, _ := strings.Cut(want, "\n")
	i := strings.Index(page, head)
	if i < 0 {
		t.Fatalf("expected fragment start not found in page: %q", head)
	}
	end := min(i+len(want), len(page))
	t.Fatal(panelsFirstDiff([]byte(page[i:end]), []byte(want)))
}

func panelsRequireAbsent(t *testing.T, page string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(page, u) {
			t.Errorf("page unexpectedly contains %q", u)
		}
	}
}

// Direct citation summary. Catches natural/numeric/ICU ordering (E1,E2,E10),
// UTF-8 byte ordering (BMP U+FF5E before U+1F600), missing deduplication across
// items, and unescaped ids.
func TestAttachmentPanelsCitationSummary(t *testing.T) {
	const open, close = `<div class="proof"><span>Validated citations</span><small>[`, `]</small></div>` // close shadows the builtin only in this function
	for _, tc := range []struct {
		name  string
		items []jsjson.Value
		want  string
	}{
		{"empty", nil, open + close},
		{"no evidenceIds member", []jsjson.Value{jsjson.MustObject("other", panelsStrings("E9"))}, open + close},
		{"utf16 code unit order, not natural", []jsjson.Value{panelsCiting("E2", "E10", "E1")}, open + "E1, E10, E2" + close},
		{"duplicates collapse within and across items", []jsjson.Value{panelsCiting("E1", "E10", "E1"), panelsCiting("E2", "E10", "E1")}, open + "E1, E10, E2" + close},
		{"astral sorts before U+FF5E in UTF-16", []jsjson.Value{panelsCiting("～", "\U0001F600")}, open + "\U0001F600, ～" + close},
		{"escaped", []jsjson.Value{panelsCiting("a<&>\"'")}, open + "a&lt;&amp;&gt;&quot;&#039;" + close},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := citationSummary(tc.items); got != tc.want {
				t.Fatalf("citationSummary\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// Minimization panel, certified and 1-minimal, with every projected string
// carrying HTML metacharacters, so each is escaped exactly once; the digest
// title keeps the whole value while the visible digest is a short one.
func TestAttachmentPanelsPureMinimizationCertified(t *testing.T) {
	result := jsjson.MustObject(
		"candidateUnitIds", panelsStrings("u1", "u2", "u3"),
		"proof", jsjson.MustObject("isProof", jsjson.MakeBool(true)),
		"minimality", jsjson.MustObject("oneMinimal", jsjson.MakeBool(true)),
		"certification", jsjson.MustObject(
			"sufficiency", jsjson.MustObject("status", str("SUFF_<&>\"'")),
			"necessity", jsjson.MustObject("status", str("NEC_<&>\"'")),
		),
	)
	// Short digest: 22 UTF-16 units or fewer is shown whole, astral pair and a
	// lone surrogate (WTF-8) included.
	digest := "d<>&\"'\U0001F600\xed\xa0\x80"
	page := RenderGitProofIncidentPage(panelsPureView(t, VerifiedAttachments{
		Minimization: &VerifiedMinimizationAttachment{Result: result, ResultDigest: digest, ExternalDigestStatus: "EXT<&>\"'"},
	}))
	nl := "\n    "
	want := `<article class="card panel"><h3>Counterfactual minimization</h3>` +
		nl + `<p><strong>BIDIRECTIONALLY CERTIFIED</strong><br><small>The attached record certifies both counterfactual directions for its selected units.</small></p>` +
		nl + `<div class="proof"><span>Candidate units</span><strong>3</strong></div>` +
		nl + `<div class="proof"><span>1-minimal</span><small>recorded</small></div>` +
		nl + `<div class="proof"><span>Sufficiency</span><small>SUFF &lt;&amp;&gt;&quot;&#039;</small></div>` +
		nl + `<div class="proof"><span>Necessity</span><small>NEC &lt;&amp;&gt;&quot;&#039;</small></div>` +
		nl + `<div class="proof"><span>Attachment digest</span><code title="d&lt;&gt;&amp;&quot;&#039;` + "\U0001F600\xed\xa0\x80" + `">d&lt;&gt;&amp;&quot;&#039;` + "\U0001F600\xed\xa0\x80" + `</code></div>` +
		nl + `<small>Separately verified record; external digest: EXT&lt;&amp;&gt;&quot;&#039;. File paths, patch bytes, raw output, and free-form diagnostic text are intentionally omitted.</small>` +
		"\n  </article>"
	panelsRequireFragment(t, page, want)
	panelsRequireAbsent(t, page, `<strong>Not attached</strong><br>No minimization record`, "NOT CERTIFIED AS COUNTERFACTUAL PROOF")
	panelsRequireFragment(t, page, panelsSuppliedHero)
}

// Uncertified, not 1-minimal. A long digest is cut by UTF-16 units: the 15-unit
// head splits an astral pair (leaving a lone high surrogate) and the 8-unit tail
// is taken from the end. Catches code-point or byte slicing and inverted states.
func TestAttachmentPanelsPureMinimizationUncertifiedAndLongDigest(t *testing.T) {
	result := jsjson.MustObject(
		"candidateUnitIds", panelsStrings(),
		"proof", jsjson.MustObject("isProof", jsjson.MakeBool(false)),
		"minimality", jsjson.MustObject("oneMinimal", jsjson.MakeBool(false)),
		"certification", jsjson.MustObject(
			"sufficiency", jsjson.MustObject("status", str("A_B")),
			"necessity", jsjson.MustObject("status", str("C_D_E")),
		),
	)
	digest := "0123456789ABCD\U0001F600mmmm<>&\"'XYZ" // 14 + 2 + 12 = 28 units
	page := RenderGitProofIncidentPage(panelsPureView(t, VerifiedAttachments{
		Minimization: &VerifiedMinimizationAttachment{Result: result, ResultDigest: digest, ExternalDigestStatus: "NOT_PROVIDED"},
	}))
	nl := "\n    "
	want := `<article class="card panel"><h3>Counterfactual minimization</h3>` +
		nl + `<p><strong>NOT CERTIFIED AS COUNTERFACTUAL PROOF</strong><br><small>The attached record did not establish both counterfactual directions.</small></p>` +
		nl + `<div class="proof"><span>Candidate units</span><strong>0</strong></div>` +
		nl + `<div class="proof"><span>1-minimal</span><small>not established</small></div>` +
		nl + `<div class="proof"><span>Sufficiency</span><small>A B</small></div>` +
		nl + `<div class="proof"><span>Necessity</span><small>C D E</small></div>` +
		nl + `<div class="proof"><span>Attachment digest</span><code title="0123456789ABCD` + "\U0001F600" + `mmmm&lt;&gt;&amp;&quot;&#039;XYZ">0123456789ABCD` + "\xed\xa0\xbd" + `...&lt;&gt;&amp;&quot;&#039;XYZ</code></div>` +
		nl + `<small>Separately verified record; external digest: NOT_PROVIDED. File paths, patch bytes, raw output, and free-form diagnostic text are intentionally omitted.</small>` +
		"\n  </article>"
	panelsRequireFragment(t, page, want)
	panelsRequireAbsent(t, page, "BIDIRECTIONALLY CERTIFIED")
}

func panelsRepair(invariant jsjson.Value, directions []jsjson.Value, hard, soft []jsjson.Value) *VerifiedRepairAttachment {
	return &VerifiedRepairAttachment{
		Manifest: jsjson.MustObject("manifestDigest", str("sha256:"+strings.Repeat("a", 64))),
		Packet:   jsjson.MakeNull(),
		Brief: jsjson.MustObject(
			"proposedInvariant", invariant,
			"repairDirections", jsjson.MakeArray(directions),
			"prevention", jsjson.MustObject("hardEnforcement", jsjson.MakeArray(hard), "softGuidance", jsjson.MakeArray(soft)),
		),
		ExternalDigestStatus: "MATCH<&>",
	}
}

// Repair-only: the invariant count is the ORIGINAL reference count (three,
// duplicates kept) while the summary is deduplicated and UTF-16 sorted; the
// prevention panel falls back to repair guidance; the hero uses the supplied
// summary. Catches counting the deduplicated set and wrong ordering.
func TestAttachmentPanelsPureRepairGuidanceAndPrecedenceFallback(t *testing.T) {
	repairAttachment := panelsRepair(
		panelsCiting("E1", "E1", "E10"),
		[]jsjson.Value{panelsCiting("E2"), panelsCiting("E1")},
		[]jsjson.Value{panelsCiting("E10", "E2"), panelsCiting("E1")},
		[]jsjson.Value{panelsCiting("～", "\U0001F600", "E<&>")},
	)
	page := RenderGitProofIncidentPage(panelsPureView(t, VerifiedAttachments{Repair: repairAttachment}))
	nl := "\n    "
	wantRepair := `<article class="card panel"><h3>Evidence-bounded repair guidance</h3>` +
		nl + `<p><span class="pill">INFERRED</span> Citation-validated guidance from a separately verified repair artifact; it is not an executed repair or proof of model intent.</p>` +
		nl + `<div class="proof"><span>Cited invariant</span><small>3 evidence references</small></div>` +
		nl + `<div class="proof"><span>Cited repair directions</span><strong>2</strong></div>` +
		nl + `<div class="proof"><span>Validated citations</span><small>[E1, E10, E2]</small></div>` +
		nl + `<small>Artifact sha256:aaaaaaaa...aaaaaaaa is bound to this proof's investigation and frozen-witness digests; external digest: MATCH&lt;&amp;&gt;. Free-form guidance remains in the private repair artifact and is deliberately not rendered in this shareable proof view.</small>` +
		"\n  </article>"
	panelsRequireFragment(t, page, wantRepair)
	wantGuidance := `<article class="card panel"><h3>Prevention guidance</h3>` +
		nl + `<p><span class="pill">INFERRED</span> These are cited recommendations, not a three-state prevention proof.</p>` +
		nl + `<div class="proof"><span>Hard enforcement directions</span><strong>2</strong></div>` +
		nl + `<div class="proof"><span>Soft guidance directions</span><strong>1</strong></div>` +
		nl + `<div class="proof"><span>Validated citations</span><small>[E1, E10, E2, E&lt;&amp;&gt;, ` + "\U0001F600, ～" + `]</small></div>` +
		nl + `<small>Free-form prevention text remains in the private repair artifact. A repaired state must still be executed under the same frozen witness and packaged via <code>fl prevention write --from-bundle</code> for Prevention verified.</small>` +
		"\n  </article>"
	panelsRequireFragment(t, page, wantGuidance)
	panelsRequireFragment(t, page, panelsSuppliedHero)
}

// Shared synthetic digest for the two escaping tests below: HTML
// metacharacters, a lone surrogate (WTF-8 ED A0 80) and an astral pair that the
// 15-unit shortDigest head cut splits. UTF-16 units: d < > & " ' = 6, lone
// surrogate = 7, ABCDEFG = 14, then U+1F600 = units 15-16, mmmm, TAIL<&> (the
// last 8 units are "mTAIL<&>"). Total 27 > 22, so it is shortened.
const panelsEscapingDigest = "d<>&\"'\xed\xa0\x80ABCDEFG\U0001F600mmmmTAIL<&>"

// Literal expectations, written independently of EscapeHTML/shortDigest. The
// 15-unit head ends with the lone HIGH surrogate of U+1F600 (ED A0 BD).
const (
	panelsEscapingVisible = "d&lt;&gt;&amp;&quot;&#039;\xed\xa0\x80ABCDEFG\xed\xa0\xbd...mTAIL&lt;&amp;&gt;"
	panelsEscapingTitle   = "d&lt;&gt;&amp;&quot;&#039;\xed\xa0\x80ABCDEFG\U0001F600mmmmTAIL&lt;&amp;&gt;"
)

// The repair artifact digest is projected escaped and UTF-16 shortened. Catches
// an unescaped manifestDigest and byte/code-point slicing.
func TestAttachmentPanelsPureRepairDigestEscaping(t *testing.T) {
	attachment := panelsRepair(panelsCiting("E1"), nil, nil, nil)
	attachment.Manifest = jsjson.MustObject("manifestDigest", str(panelsEscapingDigest))
	page := RenderGitProofIncidentPage(panelsPureView(t, VerifiedAttachments{Repair: attachment}))
	panelsRequireFragment(t, page, "<small>Artifact "+panelsEscapingVisible+" is bound to this proof's investigation and frozen-witness digests; external digest: MATCH&lt;&amp;&gt;.")
	panelsRequireAbsent(t, page, panelsEscapingDigest, "Artifact d<")
}

// The prevention root appears in both a title attribute (whole value, escaped)
// and as a UTF-16 shortened visible digest. Catches an unescaped title or body.
func TestAttachmentPanelsPurePreventionRootEscaping(t *testing.T) {
	attachment := panelsPrevention("PREVENTION_VERIFIED")
	attachment.RootDigest = panelsEscapingDigest
	page := RenderGitProofIncidentPage(panelsPureView(t, VerifiedAttachments{Prevention: attachment}))
	panelsRequireFragment(t, page, `<div class="proof"><span>Prevention root</span><code title="`+panelsEscapingTitle+`">`+panelsEscapingVisible+`</code></div>`)
	panelsRequireAbsent(t, page, panelsEscapingDigest)
}

// A single invariant reference is singular; zero references are plural.
func TestAttachmentPanelsPureRepairReferenceCountPlurality(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
		want string
	}{
		{"one", []string{"E1"}, "<small>1 evidence reference</small>"},
		{"zero", nil, "<small>0 evidence references</small>"},
		{"duplicate pair", []string{"E1", "E1"}, "<small>2 evidence references</small>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := RenderGitProofIncidentPage(panelsPureView(t, VerifiedAttachments{
				Repair: panelsRepair(panelsCiting(tc.ids...), nil, nil, nil),
			}))
			panelsRequireFragment(t, page, `<div class="proof"><span>Cited invariant</span>`+tc.want+`</div>`)
		})
	}
}

func panelsPrevention(classification string) *VerifiedPreventionAttachment {
	state := func(commit, verdict string) jsjson.Value {
		return jsjson.MustObject("commit", str(commit), "verdict", str(verdict))
	}
	return &VerifiedPreventionAttachment{
		Manifest: jsjson.MustObject("classification", str(classification)),
		Prevention: jsjson.MustObject(
			"lastGood", state("abcdef012345678<>&\"'", "PASS"),
			"firstBad", state("\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600", "FAIL_<&>"),
			"repaired", state("short\xed\xa0\x80", "PASS"),
		),
		RootDigest:           "sha256:" + strings.Repeat("b", 64),
		ExternalDigestStatus: "NOT_PROVIDED",
	}
}

func panelsExpectedPrevention(heading, pill, summary string) string {
	nl := "\n      "
	return `<article class="card panel"><h3>` + heading + `</h3>` +
		nl + `<p><span class="pill">` + pill + `</span> ` + summary + `</p>` +
		nl + `<div class="proof"><span>Last good</span><code title="abcdef012345678&lt;&gt;&amp;&quot;&#039;">abcdef012345</code> <span class="verdict pass">PASS</span></div>` +
		// 12 UTF-16 units of seven astral characters are six whole characters.
		nl + `<div class="proof"><span>First bad</span><code title="` + strings.Repeat("\U0001F600", 7) + `">` + strings.Repeat("\U0001F600", 6) + `</code> <span class="verdict fail-&lt;&amp;&gt;">FAIL_&lt;&amp;&gt;</span></div>` +
		nl + `<div class="proof"><span>Repaired</span><code title="short` + "\xed\xa0\x80" + `">short` + "\xed\xa0\x80" + `</code> <span class="verdict pass">PASS</span></div>` +
		nl + `<div class="proof"><span>Prevention root</span><code title="sha256:` + strings.Repeat("b", 64) + `">sha256:bbbbbbbb...bbbbbbbb</code></div>` +
		nl + `<small>Claims only PASS→FAIL→PASS under the same frozen witness in NATIVE_DOCKER. Does not claim model intent or a unique semantic cause.</small>` +
		"\n    </article>"
}

// Both prevention classifications, projected strings escaped, commits cut by
// UTF-16 units, and the prevention-only hero keeps the ABSENT summary (hero
// selection looks at minimization OR repair only).
func TestAttachmentPanelsPurePreventionClassificationsAndPreventionOnlyHero(t *testing.T) {
	for _, tc := range []struct {
		name, classification, want string
	}{
		{"summary", "PREVENTION_EVIDENCE_SUMMARY", panelsExpectedPrevention("Prevention evidence summary", "PREVENTION_EVIDENCE_SUMMARY",
			"Offline-checked three-state package bound to this proof root and frozen witness. Prefer a grounded package from <code>fl prevention write --from-bundle</code>.")},
		{"verified", "PREVENTION_VERIFIED", panelsExpectedPrevention("Prevention verified", "Prevention verified",
			"Grounded three-state package: last-good / first-bad run IDs from this proof root plus repaired-state NATIVE_DOCKER bindings under the same frozen witness.")},
		{"unknown classification is the summary form", "SOMETHING_ELSE", panelsExpectedPrevention("Prevention evidence summary", "PREVENTION_EVIDENCE_SUMMARY",
			"Offline-checked three-state package bound to this proof root and frozen witness. Prefer a grounded package from <code>fl prevention write --from-bundle</code>.")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := RenderGitProofIncidentPage(panelsPureView(t, VerifiedAttachments{Prevention: panelsPrevention(tc.classification)}))
			panelsRequireFragment(t, page, tc.want)
			panelsRequireFragment(t, page, panelsAbsentHero)
			panelsRequireAbsent(t, page, panelsSuppliedHero, "<h3>Prevention guidance</h3>")
		})
	}
}

// Prevention wins over repair guidance when both are supplied, while the hero
// still takes the supplied summary because repair is present.
func TestAttachmentPanelsPurePreventionPrecedenceOverRepair(t *testing.T) {
	page := RenderGitProofIncidentPage(panelsPureView(t, VerifiedAttachments{
		Repair:     panelsRepair(panelsCiting("E1"), nil, []jsjson.Value{panelsCiting("E2")}, nil),
		Prevention: panelsPrevention("PREVENTION_VERIFIED"),
	}))
	panelsRequireFragment(t, page, "<h3>Prevention verified</h3>")
	panelsRequireFragment(t, page, "<h3>Evidence-bounded repair guidance</h3>")
	panelsRequireAbsent(t, page, "<h3>Prevention guidance</h3>", "These are cited recommendations")
	panelsRequireFragment(t, page, panelsSuppliedHero)
}

// Hero selection truth table: supplied summary iff minimization OR repair.
func TestAttachmentPanelsPureHeroSelection(t *testing.T) {
	minimization := &VerifiedMinimizationAttachment{
		Result: jsjson.MustObject("candidateUnitIds", panelsStrings(), "proof", jsjson.MustObject("isProof", jsjson.MakeBool(false)),
			"minimality", jsjson.MustObject("oneMinimal", jsjson.MakeBool(false)), "certification", jsjson.MustObject()),
		ResultDigest: "d", ExternalDigestStatus: "x",
	}
	rep := panelsRepair(panelsCiting(), nil, nil, nil)
	prev := panelsPrevention("PREVENTION_VERIFIED")
	for _, tc := range []struct {
		name string
		a    VerifiedAttachments
		want string
	}{
		{"none", VerifiedAttachments{}, panelsAbsentHero},
		{"prevention only", VerifiedAttachments{Prevention: prev}, panelsAbsentHero},
		{"minimization only", VerifiedAttachments{Minimization: minimization}, panelsSuppliedHero},
		{"repair only", VerifiedAttachments{Repair: rep}, panelsSuppliedHero},
		{"minimization and prevention", VerifiedAttachments{Minimization: minimization, Prevention: prev}, panelsSuppliedHero},
		{"repair and prevention", VerifiedAttachments{Repair: rep, Prevention: prev}, panelsSuppliedHero},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := RenderGitProofIncidentPage(panelsPureView(t, tc.a))
			panelsRequireFragment(t, page, tc.want+"</p>")
			other := panelsSuppliedHero
			if tc.want == panelsSuppliedHero {
				other = panelsAbsentHero
			}
			panelsRequireAbsent(t, page, other)
		})
	}
}

// Every combination page, not only the private ones, must stay free of the
// private strings. Catches a leak that appears only for one presence pattern.
func TestAttachmentPanelsNoCombinationLeaksPrivateText(t *testing.T) {
	for _, bits := range []string{"000", "001", "010", "011", "100", "101", "110", "111"} {
		id := "view-combination-" + bits
		t.Run(id, func(t *testing.T) {
			got, _ := panelsRenderCase(t, id)
			for _, sentinel := range panelsPrivateSentinels {
				if strings.Contains(string(got), sentinel) {
					t.Errorf("rendered page leaks private sentinel %q", sentinel)
				}
			}
		})
	}
}
