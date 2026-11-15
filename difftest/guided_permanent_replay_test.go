//go:build faultline_test

package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// The permanent guided replay: every case of the retained frozen-TypeScript
// corpus is run against the built Go fl alone and compared, in full, with the
// capture. It needs no Node, no dist and no pnpm: the expected values are the
// committed capture text, and the only digests derived at replay time come from
// the capture's own validated, Node-derived preimage templates expanded for the
// replay's own owned slot. The fake docker/node/git toolchain never executes a
// target-repository command, so none of this is native Docker proof.
//
// FAULTLINE_GUIDED_EVIDENCE names the persistent evidence root.

func TestGuidedPermanentReplayFull(t *testing.T) {
	root, dir := guidedCorpusDir(t)
	corpus, err := loadGuidedCorpus(root, dir)
	if err != nil {
		t.Fatalf("corpus rejected: %v", err)
	}
	// Every declaration must be a complete one: nothing is deferred to the
	// strict same-slot Node comparison any more.
	for _, c := range corpus.Cases.Cases {
		if got := corpus.Captures[c.ID].Replay; got != guidedReplayFull {
			t.Errorf("case %s is %s, not %s: the permanent replay cannot compare it", c.ID, got, guidedReplayFull)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	if realGit, err = filepath.Abs(realGit); err != nil {
		t.Fatal(err)
	}
	// Evidence is never deleted by the harness: the default root is a persistent
	// os.MkdirTemp directory and each case's slot is retained whole in it.
	evidenceRoot := os.Getenv("FAULTLINE_GUIDED_EVIDENCE")
	if evidenceRoot == "" {
		if evidenceRoot, err = os.MkdirTemp("", "guided-replay-evidence-"); err != nil {
			t.Fatal(err)
		}
	}
	work, evidence := guidedOwnedWork(t, evidenceRoot, "r")
	guidedCheckDoctorBound(t, work, corpus.Cases.Cases)
	helper := liveConsoleHelper(t)
	plan := &guidedPlan{root: work, fakebox: guidedBuildFakebox(t), realGit: realGit, repo: root}
	steps := 0
	for _, c := range corpus.Cases.Cases {
		t.Run(c.ID, func(t *testing.T) {
			capture := corpus.Captures[c.ID]
			plan.slot = guidedCaseSlot(work, c.ID)
			plan.side = "go"
			// Registered before any process cleanup, so a failed case is retained only
			// after its children were disposed.
			t.Cleanup(func() { guidedRetainFailed(t, plan.slot, filepath.Join(evidence, c.ID), "go") })
			got := guidedRun(t, plan, "go", &corpus.Cases, c, helper, capture.Fixture.Commits)
			if len(got) != len(c.Steps) {
				t.Fatalf("%s ran %d steps, expected %d", c.ID, len(got), len(c.Steps))
			}
			steps += len(got)
			guidedCheckCapture(t, plan, "go", capture, got)
			guidedRetain(t, plan, filepath.Join(evidence, c.ID), "go")
		})
	}
	if len(corpus.Cases.Cases) != corpus.Baseline.Counts.Cases || steps != corpus.Baseline.Counts.Steps {
		t.Errorf("replayed %d cases / %d steps, the baseline pins %d / %d", len(corpus.Cases.Cases), steps, corpus.Baseline.Counts.Cases, corpus.Baseline.Counts.Steps)
	}
}

// TestGuidedReplayComparatorRejectsSilentDigestTampering is the comparator
// sensitivity control. A real Go-only run produces ordinary successful
// observations (exit status, actions and tool records untouched); then ONE
// digest value of the observation is changed and the unchanged production
// comparator (guidedCompareCapture, the code TestGuidedPermanentReplayFull
// uses) must reject it. The unmutated observation is the accepted baseline, so
// an always-rejecting comparator fails here too.
func TestGuidedReplayComparatorRejectsSilentDigestTampering(t *testing.T) {
	root, dir := guidedCorpusDir(t)
	corpus, err := loadGuidedCorpus(root, dir)
	if err != nil {
		t.Fatalf("corpus rejected: %v", err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	if realGit, err = filepath.Abs(realGit); err != nil {
		t.Fatal(err)
	}
	evidenceRoot := os.Getenv("FAULTLINE_GUIDED_EVIDENCE")
	if evidenceRoot == "" {
		if evidenceRoot, err = os.MkdirTemp("", "guided-tamper-evidence-"); err != nil {
			t.Fatal(err)
		}
	}
	work, evidence := guidedOwnedWork(t, evidenceRoot, "t")
	guidedCheckDoctorBound(t, work, corpus.Cases.Cases)
	helper := liveConsoleHelper(t)
	plan := &guidedPlan{root: work, fakebox: guidedBuildFakebox(t), realGit: realGit, repo: root, side: "go"}
	ran := 0
	for _, c := range corpus.Cases.Cases {
		if c.ID != "continuation-configured-image" && c.ID != "continuation-fakebox-clean-resume" {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
			capture := corpus.Captures[c.ID]
			plan.slot = guidedCaseSlot(work, c.ID)
			t.Cleanup(func() { guidedRetainFailed(t, plan.slot, filepath.Join(evidence, c.ID), "go") })
			got := guidedRun(t, plan, "go", &corpus.Cases, c, helper, capture.Fixture.Commits)
			guidedRetain(t, plan, filepath.Join(evidence, c.ID), "go")
			if diffs, err := guidedCompareCapture(plan.slot, "go", capture, got); err != nil || len(diffs) != 0 {
				t.Fatalf("accepted baseline rejected: %v %v", err, diffs)
			}
			refs, err := guidedExpectedRefs(plan.slot, capture.Templates)
			if err != nil {
				t.Fatal(err)
			}
			literalOf := func(token string) string {
				for _, r := range refs {
					if r.Token == token {
						return r.Literal
					}
				}
				t.Fatalf("no expected value for %s", token)
				return ""
			}
			bundle := "" // the first proof bundle directory in the final tree
			for _, f := range got[len(got)-1].Files {
				if f.Kind == "file" && strings.HasSuffix(f.Path, "/ROOT.sha256") {
					bundle = strings.TrimSuffix(f.Path, "/ROOT.sha256")
					break
				}
			}
			if bundle == "" {
				t.Fatal("no proof bundle in the final tree")
			}
			flip := func(h string) string {
				if h[0] == '0' {
					return "1" + h[1:]
				}
				return "0" + h[1:]
			}
			// clone copies the observation so one mutation never leaks into the next control.
			clone := func() []guidedObservedStep {
				out := make([]guidedObservedStep, len(got))
				copy(out, got)
				last := &out[len(out)-1]
				files := make([]guidedFileEntry, len(last.Files))
				copy(files, last.Files)
				for i := range files {
					files[i].Bytes = append([]byte(nil), files[i].Bytes...)
				}
				last.Files = files
				return out
			}
			editFile := func(steps []guidedObservedStep, path string, edit func(string) string) {
				t.Helper()
				for i, f := range steps[len(steps)-1].Files {
					if f.Path != path {
						continue
					}
					after := edit(string(f.Bytes))
					if after == string(f.Bytes) {
						t.Fatalf("edit of %s changed nothing", path)
					}
					steps[len(steps)-1].Files[i].Bytes = []byte(after)
					return
				}
				t.Fatalf("no %s in the observation", path)
			}
			rootDigest := literalOf("{DIGEST:text:" + bundle + "/hashes.txt}")
			invDigest := literalOf("{DIGEST:json:" + bundle + "/investigation.json}")
			invFile := literalOf("{FILE:json:" + bundle + "/investigation.json}")
			manFile := literalOf("{FILE:json:" + bundle + "/manifest.json}")
			flipDigest := func(d string) string { return "sha256:" + flip(strings.TrimPrefix(d, "sha256:")) }
			controls := []struct {
				name string
				edit func(steps []guidedObservedStep)
			}{
				{"ROOT.sha256 digest changed", func(s []guidedObservedStep) {
					editFile(s, bundle+"/ROOT.sha256", func(x string) string { return strings.Replace(x, rootDigest, flipDigest(rootDigest), 1) })
				}},
				{"manifest investigationDigest changed", func(s []guidedObservedStep) {
					editFile(s, bundle+"/manifest.json", func(x string) string { return strings.Replace(x, invDigest, flipDigest(invDigest), 1) })
				}},
				{"hashes.txt investigation entry changed", func(s []guidedObservedStep) {
					editFile(s, bundle+"/hashes.txt", func(x string) string { return strings.Replace(x, invFile, flip(invFile), 1) })
				}},
				{"hashes.txt entries swapped", func(s []guidedObservedStep) {
					editFile(s, bundle+"/hashes.txt", func(x string) string {
						return strings.NewReplacer(invFile, manFile, manFile, invFile).Replace(x)
					})
				}},
				{"hashes.txt manifest entry missing", func(s []guidedObservedStep) {
					editFile(s, bundle+"/hashes.txt", func(x string) string { return strings.Replace(x, manFile+"  manifest.json\n", "", 1) })
				}},
				{"stdout digest changed", func(s []guidedObservedStep) {
					// The first step whose stdout carries any expected digest.
					for i := range s {
						for _, r := range refs {
							if strings.HasPrefix(r.Literal, "sha256:") && strings.Contains(s[i].Stdout, r.Literal) {
								s[i].Stdout = strings.Replace(s[i].Stdout, r.Literal, flipDigest(r.Literal), 1)
								return
							}
						}
					}
					t.Fatal("no step stdout carries an expected digest")
				}},
			}
			for _, ctl := range controls {
				steps := clone()
				ctl.edit(steps)
				for i := range steps {
					if guidedPtrString(steps[i].Exit) != guidedPtrString(got[i].Exit) {
						t.Fatalf("%s: a control must keep the exit status", ctl.name)
					}
				}
				diffs, err := guidedCompareCapture(plan.slot, "go", capture, steps)
				if err != nil || len(diffs) == 0 {
					t.Errorf("%s: the comparator accepted the tampered observation (%v)", ctl.name, err)
					continue
				}
				t.Logf("%s: rejected at %.140s", ctl.name, diffs[0])
			}
		})
	}
	if ran != 2 {
		t.Fatalf("ran %d tamper cases, want 2", ran)
	}
}

// TestGuidedOwnedNamespaceNeverReuses proves the namespace helper: distinct existing directories on one
// evidence root, earlier contents untouched, a pre-existing name skipped, nothing deleted.
func TestGuidedOwnedNamespaceNeverReuses(t *testing.T) {
	evidence := t.TempDir()
	w1, e1 := guidedOwnedWork(t, evidence, "r")
	marker := filepath.Join(w1, "slot-x.tools-go")
	if err := os.WriteFile(marker, []byte("retained"), 0o644); err != nil {
		t.Fatal(err)
	}
	w2, e2 := guidedOwnedWork(t, evidence, "r")
	if w1 == w2 || e1 == e2 {
		t.Fatalf("two calls share a namespace: %s %s", w1, w2)
	}
	if err := os.Mkdir(filepath.Join(evidence, "r3"), 0o755); err != nil {
		t.Fatal(err)
	}
	w4, _ := guidedOwnedWork(t, evidence, "r") // r3 exists: the next free name is r4
	if !strings.Contains(w4, string(filepath.Separator)+"r4"+string(filepath.Separator)) {
		t.Fatalf("a pre-existing r3 was not skipped: %s", w4)
	}
	for _, d := range []string{w1, e1, w2, e2, w4} {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			t.Fatalf("%s is not an existing directory: %v", d, err)
		}
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "retained" {
		t.Fatalf("earlier retained data was touched: %q %v", b, err)
	}
}

// TestGuidedDoctorBoundCountsUTF16 proves the repository-path guard measures UTF-16 units like the frozen
// JavaScript string (not bytes) against the frozen doctor's 160-unit line, for the real case slots.
func TestGuidedDoctorBoundCountsUTF16(t *testing.T) {
	cases := []guidedCase{{ID: "c"}}
	suffix := filepath.Join("slot-c", "repo")
	base := strings.Repeat("a", guidedDoctorRepoBound-len(suffix)-1) // exactly 160 units with the separator
	if err := guidedDoctorBoundError(base, cases); err != nil {
		t.Fatalf("a repository path exactly at the bound must be accepted: %v", err)
	}
	if err := guidedDoctorBoundError(base+"a", cases); err == nil || !strings.Contains(err.Error(), "161 UTF-16 units") {
		t.Fatalf("one unit over the bound must be rejected with its measured length, got %v", err)
	}
	// An astral rune is 2 UTF-16 units but 4 bytes: 40 of them are 80 units (accepted) yet 160 bytes.
	astral := strings.Repeat("\U0001F600", 40) + strings.Repeat("a", guidedDoctorRepoBound-len(suffix)-1-80)
	if len(astral) <= guidedDoctorRepoBound || jsstr.UTF16Len(filepath.Join(astral, suffix)) != guidedDoctorRepoBound {
		t.Fatalf("control is not discriminating: %d bytes, %d units", len(astral), jsstr.UTF16Len(filepath.Join(astral, suffix)))
	}
	if err := guidedDoctorBoundError(astral, cases); err != nil {
		t.Fatalf("units, not bytes, are the measure: %v", err)
	}
	if err := guidedDoctorBoundError(astral+"\U0001F600", cases); err == nil {
		t.Fatal("two more units from one astral rune must be rejected")
	}
}

// TestGuidedSharedEvidenceTwoParents runs the replay-style and tamper-style namespaces on ONE evidence root
// twice in a row, building the real per-side slot/tools layout of the same case each time. This is the exact
// collision (slot, retained per-side tools, retained evidence destination) that two top-level tests sharing
// FAULTLINE_GUIDED_EVIDENCE, or a repeated -test.count, used to hit.
func TestGuidedSharedEvidenceTwoParents(t *testing.T) {
	root, dir := guidedCorpusDir(t)
	corpus, err := loadGuidedCorpus(root, dir)
	if err != nil {
		t.Fatalf("corpus rejected: %v", err)
	}
	evidence := t.TempDir()
	var slots []string
	for _, tag := range []string{"r", "t", "r", "t"} {
		work, ev := guidedOwnedWork(t, evidence, tag)
		plan := &guidedPlan{root: work, side: "go"}
		plan.slot = guidedCaseSlot(work, corpus.Cases.Cases[0].ID)
		for _, p := range []string{plan.slot, plan.toolsDir(), plan.homeDir(), plan.tmpDir()} {
			if _, err := os.Lstat(p); err == nil {
				t.Fatalf("%s exists in a fresh namespace (tag %s)", p, tag)
			}
			slots = append(slots, p)
		}
		// Create and retain exactly like a finished case: the per-side tools directory stays in place.
		for _, d := range []string{plan.slot, plan.homeDir(), plan.tmpDir(), plan.toolsDir()} {
			if err := os.Mkdir(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		guidedRetain(t, plan, filepath.Join(ev, "case"), "go")
		if _, err := os.Lstat(plan.toolsDir()); err != nil {
			t.Fatalf("the per-side tools directory must stay in place as evidence: %v", err)
		}
	}
	seen := map[string]bool{}
	for _, s := range slots {
		if seen[s] {
			t.Fatalf("path %s was used twice", s)
		}
		seen[s] = true
	}
}
