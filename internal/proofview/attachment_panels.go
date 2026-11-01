package proofview

import (
	"slices"
	"strconv"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

func citationSummary(items []jsjson.Value) string {
	seen := map[string]bool{}
	var ids []string
	for _, item := range items {
		for _, v := range item.Get("evidenceIds").Items() {
			id := v.Str()
			if !seen[id] {
				jsexc.SetAdd(seen, id)
				ids = append(ids, id)
			}
		}
	}
	slices.SortFunc(ids, jsstr.CompareUTF16)
	return `<div class="proof"><span>Validated citations</span><small>[` + EscapeHTML(jsexc.Join(ids, ", ")) + `]</small></div>`
}

func minimizationPanel(view View) string {
	attachment := view.Attachments.Minimization
	if attachment == nil {
		return absentMinimizationPanel
	}
	result := attachment.Result
	proof, summary := "NOT CERTIFIED AS COUNTERFACTUAL PROOF", "The attached record did not establish both counterfactual directions."
	if result.Get("proof", "isProof").Bool() {
		proof, summary = "BIDIRECTIONALLY CERTIFIED", "The attached record certifies both counterfactual directions for its selected units."
	}
	oneMinimal := "not established"
	if result.Get("minimality", "oneMinimal").Bool() {
		oneMinimal = "recorded"
	}
	return `<article class="card panel"><h3>Counterfactual minimization</h3>
    <p><strong>` + EscapeHTML(proof) + `</strong><br><small>` + EscapeHTML(summary) + `</small></p>
    <div class="proof"><span>Candidate units</span><strong>` + count(result.Get("candidateUnitIds")) + `</strong></div>
    <div class="proof"><span>1-minimal</span><small>` + oneMinimal + `</small></div>
    <div class="proof"><span>Sufficiency</span><small>` + EscapeHTML(underscoresToSpaces(result.Get("certification", "sufficiency", "status").Str())) + `</small></div>
    <div class="proof"><span>Necessity</span><small>` + EscapeHTML(underscoresToSpaces(result.Get("certification", "necessity", "status").Str())) + `</small></div>
    <div class="proof"><span>Attachment digest</span><code title="` + EscapeHTML(attachment.ResultDigest) + `">` + EscapeHTML(shortDigest(attachment.ResultDigest)) + `</code></div>
    <small>Separately verified record; external digest: ` + EscapeHTML(attachment.ExternalDigestStatus) + `. File paths, patch bytes, raw output, and free-form diagnostic text are intentionally omitted.</small>
  </article>`
}

func repairPanel(view View) string {
	attachment := view.Attachments.Repair
	if attachment == nil {
		return absentRepairPanel
	}
	brief := attachment.Brief
	refs := len(brief.Get("proposedInvariant", "evidenceIds").Items())
	suffix := "s"
	if refs == 1 {
		suffix = ""
	}
	items := []jsjson.Value{brief.Get("proposedInvariant")}
	items = append(items, brief.Get("repairDirections").Items()...)
	return `<article class="card panel"><h3>Evidence-bounded repair guidance</h3>
    <p><span class="pill">INFERRED</span> Citation-validated guidance from a separately verified repair artifact; it is not an executed repair or proof of model intent.</p>
    <div class="proof"><span>Cited invariant</span><small>` + strconv.Itoa(refs) + ` evidence reference` + suffix + `</small></div>
    <div class="proof"><span>Cited repair directions</span><strong>` + count(brief.Get("repairDirections")) + `</strong></div>
    ` + citationSummary(items) + `
    <small>Artifact ` + EscapeHTML(shortDigest(attachment.Manifest.Get("manifestDigest").Str())) + ` is bound to this proof's investigation and frozen-witness digests; external digest: ` + EscapeHTML(attachment.ExternalDigestStatus) + `. Free-form guidance remains in the private repair artifact and is deliberately not rendered in this shareable proof view.</small>
  </article>`
}

func preventionGuidancePanel(view View) string {
	prevention := view.Attachments.Prevention
	if prevention != nil {
		body := prevention.Prevention
		heading, pill := "Prevention evidence summary", "PREVENTION_EVIDENCE_SUMMARY"
		summary := "Offline-checked three-state package bound to this proof root and frozen witness. Prefer a grounded package from <code>fl prevention write --from-bundle</code>."
		if prevention.Manifest.Get("classification").Str() == "PREVENTION_VERIFIED" {
			heading, pill = "Prevention verified", "Prevention verified"
			summary = "Grounded three-state package: last-good / first-bad run IDs from this proof root plus repaired-state NATIVE_DOCKER bindings under the same frozen witness."
		}
		return `<article class="card panel"><h3>` + heading + `</h3>
      <p><span class="pill">` + pill + `</span> ` + summary + `</p>
      <div class="proof"><span>Last good</span><code title="` + EscapeHTML(body.Get("lastGood", "commit").Str()) + `">` + EscapeHTML(shortCommit(body.Get("lastGood", "commit").Str())) + `</code> ` + verdictBadge(body.Get("lastGood", "verdict").Str()) + `</div>
      <div class="proof"><span>First bad</span><code title="` + EscapeHTML(body.Get("firstBad", "commit").Str()) + `">` + EscapeHTML(shortCommit(body.Get("firstBad", "commit").Str())) + `</code> ` + verdictBadge(body.Get("firstBad", "verdict").Str()) + `</div>
      <div class="proof"><span>Repaired</span><code title="` + EscapeHTML(body.Get("repaired", "commit").Str()) + `">` + EscapeHTML(shortCommit(body.Get("repaired", "commit").Str())) + `</code> ` + verdictBadge(body.Get("repaired", "verdict").Str()) + `</div>
      <div class="proof"><span>Prevention root</span><code title="` + EscapeHTML(prevention.RootDigest) + `">` + EscapeHTML(shortDigest(prevention.RootDigest)) + `</code></div>
      <small>Claims only PASS→FAIL→PASS under the same frozen witness in NATIVE_DOCKER. Does not claim model intent or a unique semantic cause.</small>
    </article>`
	}
	repair := view.Attachments.Repair
	if repair == nil {
		return absentPreventionGuidancePanel
	}
	guidance := repair.Brief.Get("prevention")
	items := append([]jsjson.Value{}, guidance.Get("hardEnforcement").Items()...)
	items = append(items, guidance.Get("softGuidance").Items()...)
	return `<article class="card panel"><h3>Prevention guidance</h3>
    <p><span class="pill">INFERRED</span> These are cited recommendations, not a three-state prevention proof.</p>
    <div class="proof"><span>Hard enforcement directions</span><strong>` + count(guidance.Get("hardEnforcement")) + `</strong></div>
    <div class="proof"><span>Soft guidance directions</span><strong>` + count(guidance.Get("softGuidance")) + `</strong></div>
    ` + citationSummary(items) + `
    <small>Free-form prevention text remains in the private repair artifact. A repaired state must still be executed under the same frozen witness and packaged via <code>fl prevention write --from-bundle</code> for Prevention verified.</small>
  </article>`
}
