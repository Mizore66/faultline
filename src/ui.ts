import { fixtureStateById } from "./fixture.js";
import {
  escapeHtml,
  renderIncidentPageDocumentEnd,
  renderIncidentPageDocumentStart,
  renderIncidentPageProductChrome
} from "./incident-page.js";
import type { DemoAnalysis, RunRecord, Verdict } from "./domain.js";

function verdictClass(verdict: Verdict): string {
  return verdict.toLowerCase().replaceAll("_", "-");
}

function verdictBadge(verdict: Verdict): string {
  return `<span class="verdict ${verdictClass(verdict)}">${escapeHtml(verdict)}</span>`;
}

function runRow(run: RunRecord): string {
  const state = fixtureStateById(run.stateId);
  return `<div class="run-row"><div><strong>${escapeHtml(state?.label ?? run.stateId)}</strong><small>${escapeHtml(run.reasonCode)}</small></div><div class="run-meta">${verdictBadge(run.verdict)}<small>${run.executionKind === "CACHED" ? "cached replay" : `${run.durationMs} ms`}</small></div></div>`;
}

function evidenceRow(kind: string, statement: string): string {
  return `<div class="evidence-row"><span>${escapeHtml(kind)}</span><p>${escapeHtml(statement)}</p></div>`;
}

/**
 * The deterministic walkthrough intentionally shares the same document shell
 * as a real verified Git package. Its content stays visibly fixture-scoped so
 * it cannot be mistaken for a customer incident or a Docker proof.
 */
export function renderIncidentPage(analysis: DemoAnalysis): string {
  const firstRegression = analysis.transitions.find((item) => item.kind === "PASS_TO_FAIL" && item.stable);
  const lastGood = fixtureStateById(analysis.prevention.lastGood.stateId)?.label ?? analysis.prevention.lastGood.stateId;
  const firstBad = fixtureStateById(analysis.prevention.firstBad.stateId)?.label ?? analysis.prevention.firstBad.stateId;
  const contributionRows = analysis.contributionRuns.map(runRow).join("");
  const timelineRows = analysis.timelineRuns.map(runRow).join("");
  const attemptRows = analysis.minimization.attempts.map((attempt) => `<div class="attempt"><span class="mono">${escapeHtml(attempt.subset.length ? attempt.subset.join(" + ") : "(empty)")}</span><b class="attempt-${attempt.outcome.toLowerCase()}">${escapeHtml(attempt.outcome)}</b><small>${escapeHtml(attempt.note)}</small></div>`).join("");
  const claimRows = analysis.claims
    .map((claim) => evidenceRow(claim.kind, claim.statement.replaceAll("Session Cedar", "the fixture")))
    .join("");
  const minimizationClaim = analysis.minimization.termination === "BIDIRECTIONALLY_VALIDATED"
    ? "Bidirectionally validated"
    : "Not executed - cached replay only";
  const preventionClaim = analysis.prevention.verified
    ? "SAMPLE RESULT - NOT A CUSTOMER PREVENTION CLAIM"
    : "SAMPLE ONLY - cached replay is not evidence";
  const preventionDetail = analysis.prevention.verified
    ? "The built-in fixture has three executable states. Real prevention needs a retained proof package for the user's own witness."
    : "Run all fixture evidence for the sample, then use fl incident start for a real review and proof path.";

  return `${renderIncidentPageDocumentStart("deterministic sample")}
${renderIncidentPageProductChrome({
  command: "fl judge-demo",
  notice: "Deterministic built-in fixture - useful for a fast product walkthrough, but not a customer incident, native Docker proof, or record of this repository's history."
})}
<section class="hero" id="break"><div><div class="eyebrow">DETERMINISTIC SAMPLE - EXECUTABLE EVIDENCE FOR AGENT-ASSISTED CODE</div><h1>Freeze the question.<br><em>Then prove</em> the boundary.</h1><p class="lede">This included sample demonstrates FaultLine's five beats. For a real incident, start with <code>fl doctor</code> and <code>fl incident start</code>; the same product structure then renders a verified Git proof package.</p></div><aside class="card metric-card" aria-label="Sample boundary"><div class="metric"><b>FIXTURE</b><small>deterministic source states</small></div><div class="metric"><b>NO DOCKER</b><small>required for this walkthrough</small></div><div class="metric"><b>NO REMOTE</b><small>or API key used</small></div><div class="metric"><b>REAL PATH</b><small>fl incident start</small></div></aside></section>
<section class="section" id="find"><div class="section-head"><span class="number">01</span><h2>BREAK &rarr; FIND</h2></div><div class="grid"><article class="card panel witness"><h3>Sample frozen witness</h3><p><strong>${escapeHtml(analysis.witness.behavior)}</strong></p><p class="mono">$ ${escapeHtml(analysis.witness.command)}</p><span class="pill">fixture proposal</span><span class="pill">sample approval record</span><span class="pill">network disabled</span><p class="hash">${escapeHtml(analysis.witness.digest)}</p><small>The fixture uses one immutable witness version across every comparable sample state.</small></article><article class="card panel"><h3>Fixture contribution outcomes</h3>${contributionRows}</article></div></section>
<section class="section"><div class="section-head"><span class="number">02</span><h2>FIND</h2></div><div class="grid"><article class="card panel"><h3>Sample executable states</h3>${timelineRows}</article><article class="card panel"><h3>Stable sample boundary</h3><p>Three reruns on either side prevent a single noisy result from becoming attribution.</p><div class="transition"><div>${verdictBadge(analysis.prevention.lastGood.verdict)}<small>${escapeHtml(lastGood)}</small></div><div class="arrow">&rarr;</div><div>${verdictBadge(analysis.prevention.firstBad.verdict)}<small>${escapeHtml(firstBad)}</small></div></div><p><strong>${firstRegression ? "First stable pass-to-fail transition" : "No stable transition"}</strong></p><small>${firstRegression ? `${firstRegression.beforeStateId} -> ${firstRegression.afterStateId}` : "FaultLine refuses to name a first bad state without a stable bracket."}</small></article></div></section>
<section class="section" id="prove"><div class="section-head"><span class="number">03</span><h2>PROVE</h2></div><div class="grid"><article class="card panel"><h3>Fixture counterfactual minimization</h3><p><strong>${analysis.minimization.budget.used} / ${analysis.minimization.budget.max} test-run budget</strong><small>Unresolved subsets are explicitly excluded from pass/fail evidence.</small></p>${attemptRows}</article><article class="card panel"><h3>Sample 1-minimal failure-inducing set</h3><p class="mono">${escapeHtml(analysis.minimization.candidate.join(" + "))}</p><div class="proof"><span>Apply to last-good</span>${verdictBadge(analysis.minimization.sufficiency.verdict)}</div><div class="proof"><span>Remove from first-bad</span>${verdictBadge(analysis.minimization.necessity.verdict)}</div><p><strong>${minimizationClaim}</strong><small>The result is scoped to this frozen witness, not model intent or a unique semantic root cause.</small></p></article></div></section>
<section class="section"><div class="section-head"><span class="number">04</span><h2>FIX</h2></div><div class="grid"><article class="card panel"><h3>Evidence-bounded explanation</h3>${claimRows}</article><article class="card panel"><h3>Repair packet for Codex</h3><p>FaultLine sends the frozen witness, executed evidence, and bounded invariant - not a model-selected culprit.</p><div class="callout"><strong>Hard enforcement</strong><small>Regression test · boundary validation</small></div><div class="callout" style="margin-top:10px;border-color:rgba(173,155,255,.27);background:rgba(173,155,255,.06)"><strong style="color:var(--violet)">Soft guidance</strong><small>Repository invariant for future agents</small></div></article></div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">05</span><h2>PREVENT</h2></div><div class="grid"><article class="card panel"><h3>Three-state fixture outcome</h3><div class="proof"><span>Last-good + frozen witness</span>${verdictBadge(analysis.prevention.lastGood.verdict)}</div><div class="proof"><span>First-bad + frozen witness</span>${verdictBadge(analysis.prevention.firstBad.verdict)}</div><div class="proof"><span>Repaired + frozen witness</span>${verdictBadge(analysis.prevention.repaired.verdict)}</div><div class="callout" style="margin-top:16px"><strong>${preventionClaim}</strong><small>${preventionDetail}</small></div></article><article class="card panel"><h3>Portable sample bundle</h3><p><strong>Fixture grade ${escapeHtml(analysis.grade.value)}</strong></p>${analysis.grade.reasons.map((reason) => `<small>• ${escapeHtml(reason)}</small>`).join("")}<div class="action-row"><button id="rerun" type="button">Re-run fixture evidence</button><span class="action-status" id="action-status">runs the reviewed built-in sample only</span></div><details><summary>Integrity boundary</summary><pre>fl verify .faultline/bundles/judge-demo --expect-root sha256:&lt;recorded-root&gt;

Verifies the complete declared file set. An externally recorded root detects a rewritten bundle. It does not execute repository code.</pre></details></article></div></section>
${renderIncidentPageDocumentEnd({
  footer: "This is a deterministic sample. FaultLine is a safety protocol for a falsifiable, approved predicate; a real claim needs a reviewed witness, recorded isolated executions, and a verified portable package.",
  script: `<script>
const rerun=document.getElementById("rerun");const status=document.getElementById("action-status");rerun?.addEventListener("click",async()=>{rerun.disabled=true;status.textContent="executing the reviewed fixture...";try{const response=await fetch("/api/rerun",{method:"POST"});if(!response.ok)throw new Error(await response.text());status.textContent="evidence re-executed; refreshing...";window.location.reload()}catch(error){status.textContent="rerun failed: "+(error instanceof Error?error.message:String(error));rerun.disabled=false}});
</script>`
})}`;
}
