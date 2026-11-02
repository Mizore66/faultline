//go:build faultline_test

package difftest

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/proofview"
)

// Permanent replay of the Slice 5c serve-bundle corpus
// (difftest/testdata/serve-bundle). It needs no Node, Git history, src/ or
// manifest: the built Go library is compared with committed frozen-TS captures.
// This file owns the library/panel replay of the view rows only. The cli-error
// and cli-serve rows are validated here as retained evidence (strict Corpus
// load and shape checks) but are NOT executed and are NOT reported as feature
// passes until a separate TestServeBundleCLIReplay drives the real fl binary.

const (
	serveBundleDir       = "testdata/serve-bundle"
	serveBundleViewCases = 50 // view-* rows, written by hand, not derived from the corpus
)

func loadServeBundleCorpus(t *testing.T) *servefixture.Corpus {
	t.Helper()
	corpus, err := servefixture.Load(serveBundleDir)
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

// TestServeBundleMetadata validates the entire corpus: the strict loader
// (pinned provenance, exact pinned case IDs, fixture inventories, golden shapes,
// referenced pages), plus an independently written count of view rows and
// nonblank CLI evidence. It executes no case.
func TestServeBundleMetadata(t *testing.T) {
	corpus := loadServeBundleCorpus(t)
	views, clis := 0, 0
	for _, c := range corpus.Cases {
		g, ok := corpus.Goldens[c.ID]
		if !ok || g.ID != c.ID {
			t.Fatalf("case %s lacks its golden", c.ID)
		}
		switch c.Kind {
		case "view":
			views++
		case "cli-error", "cli-serve":
			clis++
			if g.Process == nil || len(c.Argv) == 0 || !strings.HasPrefix(c.ID, "cli-") {
				t.Errorf("CLI case %s has no retained process/argv evidence", c.ID)
			}
			if c.Kind == "cli-serve" {
				if _, err := corpus.Page(c.ID); err != nil {
					t.Errorf("cli-serve case %s: %v", c.ID, err)
				}
			}
		default:
			t.Fatalf("case %s has unexpected kind %q", c.ID, c.Kind)
		}
	}
	if views != serveBundleViewCases {
		t.Fatalf("%d view cases, want %d", views, serveBundleViewCases)
	}
	if clis == 0 || views+clis != len(corpus.Cases) || len(corpus.Goldens) != len(corpus.Cases) {
		t.Fatalf("cases=%d views=%d cli=%d goldens=%d", len(corpus.Cases), views, clis, len(corpus.Goldens))
	}
}

// serveBundleValueJSON re-states JSON.stringify(VerifiedGitProofView) in TS
// member order with the JS serializer (never encoding/json and never
// canonicalization), every attachment being null or its record.
func serveBundleValueJSON(v proofview.View) string {
	str := jsjson.MakeString
	a := v.Attachments
	minimization, rep, prev := jsjson.MakeNull(), jsjson.MakeNull(), jsjson.MakeNull()
	if m := a.Minimization; m != nil {
		minimization = jsjson.MustObject("result", m.Result, "resultDigest", str(m.ResultDigest), "externalDigestStatus", str(m.ExternalDigestStatus))
	}
	if r := a.Repair; r != nil {
		rep = jsjson.MustObject("manifest", r.Manifest, "packet", r.Packet, "brief", r.Brief, "externalDigestStatus", str(r.ExternalDigestStatus))
	}
	if p := a.Prevention; p != nil {
		prev = jsjson.MustObject("manifest", p.Manifest, "prevention", p.Prevention, "rootDigest", str(p.RootDigest), "externalDigestStatus", str(p.ExternalDigestStatus))
	}
	return jsjson.Stringify(jsjson.MustObject(
		"rootDigest", str(v.RootDigest),
		"checkedFiles", jsjson.MakeNumber(float64(v.CheckedFiles)),
		"externalRootStatus", str(v.ExternalRootStatus),
		"manifest", v.Manifest,
		"investigation", v.Investigation,
		"frozenWitness", v.FrozenWitness,
		"attachments", jsjson.MustObject("minimization", minimization, "repair", rep, "prevention", prev),
	))
}

// serveBundleDiff is a short first-difference report for two byte strings.
func serveBundleDiff(got, want []byte) string {
	n := min(len(got), len(want))
	i := 0
	for i < n && got[i] == want[i] {
		i++
	}
	win := func(b []byte) string { return fmt.Sprintf("%q", b[max(i-40, 0):min(i+80, len(b))]) }
	return fmt.Sprintf("first difference at byte %d (got %d bytes, want %d)\n got ...%s\nwant ...%s", i, len(got), len(want), win(got), win(want))
}

// TestServeBundleReplay executes every one of the view rows through the direct
// loader against a fresh copy of its fixture and compares the complete ordered
// View JSON, the complete page bytes, or the exact thrown message. Cases run
// serially: the verifiers share process-global jsjson call-site state.
func TestServeBundleReplay(t *testing.T) {
	corpus := loadServeBundleCorpus(t)
	replayed := 0
	for _, c := range corpus.Cases {
		if c.Kind != "view" {
			continue
		}
		replayed++
		t.Run(c.ID, func(t *testing.T) {
			golden := corpus.Goldens[c.ID]
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
			view, err := proofview.LoadVerifiedGitProofViewWithAttachments(filepath.Join(copyRoot, "bundle"), c.ExpectedRoot, proofview.AttachmentOptions{
				MinimizationFile:           in(c.Options.MinimizationFile),
				ExpectedMinimizationDigest: c.Options.ExpectedMinimizationDigest,
				RepairDirectory:            in(c.Options.RepairDirectory),
				ExpectedRepairDigest:       c.Options.ExpectedRepairDigest,
				PreventionDirectory:        in(c.Options.PreventionDirectory),
				ExpectedPreventionDigest:   c.Options.ExpectedPreventionDigest,
			})
			if golden.Error != nil {
				if err == nil {
					t.Fatalf("loader succeeded, TS refused with %q", *golden.Error)
				}
				if got := jsexc.Message(err); got != *golden.Error {
					t.Fatalf("message\n got %q\nwant %q", got, *golden.Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("loader failed, TS succeeded: %v", err)
			}
			if got := serveBundleValueJSON(view); got != *golden.ValueJSON {
				t.Fatal("View JSON differs: " + serveBundleDiff([]byte(got), []byte(*golden.ValueJSON)))
			}
			want, err := corpus.Page(c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := []byte(jsstr.ToUTF8(proofview.RenderGitProofIncidentPage(view))); !bytes.Equal(got, want) {
				t.Fatal("page differs: " + serveBundleDiff(got, want))
			}
		})
	}
	if replayed != serveBundleViewCases {
		t.Fatalf("replayed %d view cases, want %d", replayed, serveBundleViewCases)
	}
}

// TestServeBundleCLIReplay executes every retained cli-error and cli-serve row
// against the actual tagged fl binary (TestMain's flBinary, GOCOVERDIR as set
// there), each as a named subtest. Errors compare the complete stdout, stderr
// and exit status; servers compare the complete startup streams, the fetched
// page and a completed SIGINT exit. The counts are written independently of the
// corpus so a loader that dropped rows cannot make the replay pass vacuously.
func TestServeBundleCLIReplay(t *testing.T) {
	corpus := loadServeBundleCorpus(t)
	errorRows, serverRows, executed := runServeBundleCLIRows(t, corpus)
	if errorRows != serveBundleCLIErrorRows || serverRows != serveBundleCLIServerRows {
		t.Fatalf("enumerated %d cli-error and %d cli-serve rows, want %d and %d", errorRows, serverRows, serveBundleCLIErrorRows, serveBundleCLIServerRows)
	}
	// With -run selecting subtests only some rows run; the complete selector
	// must run all of them.
	if executed != errorRows+serverRows && !subtestFilter() {
		t.Fatalf("executed %d of %d rows", executed, errorRows+serverRows)
	}
}

// TestServeBundleHTTP runs the one portable HTTP verifier against the real
// proofview server and against the actual CLI process, then the actual-CLI
// render-once snapshot, privacy, no-execution, ignored-option, occupied-port
// precedence, fallback and held-client contracts. It never runs in parallel:
// the verifiers share package-global jsjson call-site state.
func TestServeBundleHTTP(t *testing.T) {
	corpus := loadServeBundleCorpus(t)
	helper := serveBundleConsoleHelper(t)
	t.Run("direct-server", func(t *testing.T) { checkServeBundleDirectHTTP(t, corpus) })
	t.Run("actual-cli", func(t *testing.T) { testServeBundleCLIHTTP(t, corpus, helper) })
	t.Run("render-once", func(t *testing.T) { testServeBundleRenderOnce(t, corpus, helper) })
	t.Run("ignored-options", func(t *testing.T) { testServeBundleIgnoredOptions(t, corpus, helper) })
	t.Run("occupied-port-precedence", func(t *testing.T) { testServeBundleOccupiedPortPrecedence(t, corpus) })
	t.Run("fallbacks", func(t *testing.T) { testServeBundleFallbacksActual(t) })
	t.Run("held-clients", func(t *testing.T) { testServeBundleHeldClients(t, corpus, helper) })
}
