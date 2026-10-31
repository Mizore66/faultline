package proofview

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// judgePreviewThresholds are the five proofThresholds of src/judge-preview.ts:50.
var judgePreviewThresholds = []string{
	"One approved frozen witness defines the behavior under review.",
	"Stability requires independently recorded isolated executions.",
	"A candidate needs counterfactual validation in both directions.",
	"Cached replay remains non-evidentiary.",
	"A deterministic fixture is not a claim about an external repository.",
}

// The static sections of the template literal in src/judge-preview.ts:61-71,
// split at its ${...} interpolations. Each section ends where the source has
// a line break or an interpolation.
const (
	judgePreviewHero = `<section class="hero"><div><div class="eyebrow">FAULTLINE · FIXTURE SANDBOX</div><h1>Freeze the question.<br><em>Inspect the boundary.</em></h1><p class="lede">FaultLine turns one reviewed behavioral witness into a bounded investigation. This preview shows the product contract and its honesty boundary without presenting fixture statistics as production evidence.</p><span class="readonly-badge">STATIC / READ-ONLY</span></div><aside class="card metric-card" aria-label="FaultLine product guarantees"><div class="metric"><b>HUMAN</b><small>approval + freeze</small></div><div class="metric"><b>LOCAL</b><small>no auto-range</small></div><div class="metric"><b>3×</b><small>runs per Git state</small></div><div class="metric"><b>DIGEST</b><small>retained proof root</small></div></aside></section>`

	judgePreviewBreakHead     = `<section class="section" id="break"><div class="section-head"><span class="number">01</span><h2>APPROVE</h2></div><div class="grid"><article class="card panel witness"><h3>One frozen witness</h3><p><strong>`
	judgePreviewBreakMid      = `</strong></p><p class="hash">`
	judgePreviewBreakBoundary = `</p><small>The fixture models a witness approved before localization, with networking disabled and credentials redacted.</small></article><article class="card panel"><h3>Sample boundary</h3><div class="transition"><span>PASS</span><span class="arrow" aria-hidden="true">→</span><span>FAIL</span></div><p><strong>`
	judgePreviewBreakTail     = `</strong></p><small>Cached fixture data only — not commit-path COMMIT_PROOF, and not experimental turn-path evidence.</small></article></div></section>`

	judgePreviewFindHead = `<section class="section" id="find"><div class="section-head"><span class="number">02</span><h2>REVIEW</h2></div><article class="card panel"><h3>Three-state sample</h3><div class="table-wrap"><table class="data-table"><thead><tr><th>State</th><th>Fixture verdict</th><th>Reason</th></tr></thead><tbody>`
	judgePreviewFindTail = `</tbody></table></div><small>The values are replayed sample outcomes, not a record of a fresh execution.</small></article></section>`

	judgePreviewProveHead = `<section class="section" id="prove"><div class="section-head"><span class="number">03</span><h2>EVIDENCE BOUNDARY</h2></div><div class="grid"><article class="card panel"><h3>What the fixture does and does not say</h3>`
	judgePreviewProveMid  = `</article><article class="card panel"><h3>Why this stays a preview</h3><p>A real FaultLine <code>COMMIT_PROOF</code> requires an approved frozen witness, isolated recorded executions across immutable Git commit states, and an independently verifiable Git proof package. This page contains none of those live artifacts.</p><ul>`
	judgePreviewProveTail = `</ul></article></div></section>`

	judgePreviewFix = `<section class="section" id="fix"><div id="prevent" class="section-anchor" aria-hidden="true"></div><div class="section-head"><span class="number">04</span><h2>NEXT</h2></div><article class="card panel"><h3>Open the product Idea path</h3><p>Run <code>pnpm fl judge-proof</code> (installed live-git sample root <code>sha256:f85c446d…</code>) or open <code>docs/self-incident-proof-preview.html</code>. Historical self-incident root <code>sha256:f6a391b3…</code> is a separate dogfood package — see <code>docs/samples/COMMIT_PROOF_SAMPLE.md</code>.</p></article></section>`
)

func judgePreviewBadge(run jsjson.Value) string {
	verdict := run.Get("verdict").Str()
	return `<span class="verdict ` + strings.ToLower(verdict) + `">` + EscapeHTML(verdict) + `</span>`
}

// Port of src/judge-preview.ts:50 renderJudgePreviewPage. A nil analysis is
// the default deterministic REPLAY fixture; a supplied one must be REPLAY.
func RenderJudgePreviewPage(analysis *jsjson.Value) (string, error) {
	var a jsjson.Value
	if analysis == nil {
		a = demofixture.CreateReplayAnalysis()
	} else {
		a = *analysis
	}
	if a.Get("mode").Str() != "REPLAY" {
		return "", errors.New("Judge preview only renders the deterministic REPLAY fixture.")
	}

	boundaryCopy := "No sample pass-to-fail boundary was recorded."
	for _, t := range a.Get("transitions").Items() {
		if t.Get("kind").Str() == "PASS_TO_FAIL" {
			boundaryCopy = demofixture.StateLabel(t.Get("beforeStateId").Str()) + " to " +
				demofixture.StateLabel(t.Get("afterStateId").Str())
			break
		}
	}

	var states strings.Builder
	for _, key := range []string{"lastGood", "firstBad", "repaired"} {
		run := a.Get("prevention", key)
		states.WriteString(`<tr><td>` + EscapeHTML(demofixture.StateLabel(run.Get("stateId").Str())) +
			`</td><td>` + judgePreviewBadge(run) + `</td><td>` + EscapeHTML(run.Get("reasonCode").Str()) + `</td></tr>`)
	}
	var claims strings.Builder
	for _, c := range a.Get("claims").Items() {
		claims.WriteString(`<div class="evidence-row"><span>` + EscapeHTML(c.Get("kind").Str()) +
			`</span><p>` + EscapeHTML(c.Get("statement").Str()) + `</p></div>`)
	}
	var thresholds strings.Builder
	for _, th := range judgePreviewThresholds {
		thresholds.WriteString(`<li>` + EscapeHTML(th) + `</li>`)
	}

	idea := "Fixture sandbox only — not COMMIT_PROOF. Product Idea: pnpm fl judge-proof (sample root sha256:f85c446d…) or docs/self-incident-proof-preview.html."
	notice := "NOT A LIVE DOCKER PROOF. This self-contained page is a read-only snapshot of FaultLine's built-in replay fixture. It did not run Docker, execute repository code, call an API, or verify a live repository. It makes no certified regression, provenance, or prevention claim."
	start := renderIncidentPageDocumentStart("deterministic judge preview")
	chrome := renderIncidentPageProductChrome(chromeOptions{
		command: "fl judge-preview", ideaBeat: &idea, notice: &notice,
	})
	end := renderIncidentPageDocumentEnd("FaultLine static preview schema: faultline.judge-preview.v1. No code executes when this file is opened.", nil)

	return start + "\n" +
		chrome + "\n" +
		judgePreviewHero + "\n" +
		judgePreviewBreakHead + EscapeHTML(a.Get("witness", "behavior").Str()) +
		judgePreviewBreakMid + EscapeHTML(a.Get("witness", "digest").Str()) +
		judgePreviewBreakBoundary + EscapeHTML(boundaryCopy) + judgePreviewBreakTail + "\n" +
		judgePreviewFindHead + states.String() + judgePreviewFindTail + "\n" +
		judgePreviewProveHead + claims.String() + judgePreviewProveMid + thresholds.String() + judgePreviewProveTail + "\n" +
		judgePreviewFix + "\n" +
		end, nil
}
