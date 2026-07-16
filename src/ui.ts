import { fixtureStateById } from "./fixture.js";
import type { DemoAnalysis, RunRecord, Verdict } from "./domain.js";

function escapeHtml(value: string): string {
  return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function verdictClass(verdict: Verdict): string {
  return verdict.toLowerCase().replaceAll("_", "-");
}

function verdictLabel(verdict: Verdict): string {
  return {
    PASS: "✓ WORKING",
    FAIL: "! BROKEN",
    UNSTABLE: "INCONSISTENT",
    ERROR: "COULD NOT RUN",
    INAPPLICABLE: "NOT CHECKED"
  }[verdict];
}

function reasonLabel(reasonCode: string): string {
  const labels: Record<string, string> = {
    WITNESS_SATISFIED: "The test passed",
    WITNESS_ASSERTION_FAILED: "The test failed",
    FIXTURE_COMPILE_ERROR: "The example could not be prepared",
    RUNNER_ERROR: "The test could not be run"
  };
  return labels[reasonCode] ?? reasonCode.replaceAll("_", " ").toLowerCase();
}

function stateLabel(stateId: string, fallback: string): string {
  const labels: Record<string, string> = {
    "contribution-atlas": "Checkpoint 1 · After Atlas's work",
    "contribution-birch": "Checkpoint 2 · After Birch's work",
    "contribution-cedar": "Checkpoint 3 · After Cedar's work",
    "contribution-delta": "Checkpoint 4 · After Delta's work",
    "cedar-turn-1": "Saved step 1",
    "cedar-turn-2": "Saved step 2",
    "cedar-turn-3": "Saved step 3",
    "cedar-turn-4": "Saved step 4 · Last working version",
    "cedar-turn-5": "Saved step 5 · First broken version",
    "cedar-turn-6": "Saved step 6",
    "cedar-turn-7": "Saved step 7 · Fixed",
    "cedar-turn-8": "Saved step 8 · Bug returned"
  };
  return labels[stateId] ?? fallback;
}

function verdictBadge(verdict: Verdict): string {
  return `<span class="verdict ${verdictClass(verdict)}">${escapeHtml(verdictLabel(verdict))}</span>`;
}

function runRow(run: RunRecord): string {
  const state = fixtureStateById(run.stateId);
  return `<div class="run-row"><div><strong>${escapeHtml(stateLabel(run.stateId, state?.label ?? run.stateId))}</strong><small>${escapeHtml(reasonLabel(run.reasonCode))}</small></div><div class="run-meta">${verdictBadge(run.verdict)}<small>${run.executionKind === "CACHED" ? "saved result" : `finished in ${run.durationMs} ms`}</small></div></div>`;
}

function evidenceRow(kind: string, statement: string): string {
  return `<div class="evidence-row"><span>${escapeHtml(kind)}</span><p>${escapeHtml(statement)}</p></div>`;
}

export function renderIncidentPage(analysis: DemoAnalysis): string {
  const firstRegression = analysis.transitions.find((item) => item.kind === "PASS_TO_FAIL" && item.stable);
  const lastGood = firstRegression
    ? stateLabel(firstRegression.beforeStateId, firstRegression.beforeStateId)
    : "Last working version";
  const firstBad = firstRegression
    ? stateLabel(firstRegression.afterStateId, firstRegression.afterStateId)
    : "First broken version";
  const contributionRows = analysis.contributionRuns.slice(0, 3).map(runRow).join("");
  const timelineRows = analysis.timelineRuns.map(runRow).join("");
  const attemptRows = analysis.minimization.attempts.map((attempt) => `<div class="attempt"><span class="mono">${escapeHtml(attempt.subset.length ? attempt.subset.join(" + ") : "No changes")}</span><b class="attempt-${attempt.outcome.toLowerCase()}">${escapeHtml(attempt.outcome === "PASS" ? "WORKING" : attempt.outcome === "FAIL" ? "BROKEN" : "COULD NOT CHECK")}</b><small>${escapeHtml(attempt.note)}</small></div>`).join("");
  const claimRows = analysis.claims.map((claim) => evidenceRow(claim.kind, claim.statement)).join("");
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>FaultLine — ${escapeHtml(analysis.fixture.title)}</title>
<style>
:root{color-scheme:dark;--ink:#ecf4f7;--muted:#94a8b1;--canvas:#071118;--panel:rgba(14,30,39,.88);--line:rgba(149,191,200,.18);--cyan:#65e4df;--lime:#b4f37b;--orange:#ffbd72;--rose:#ff7f9c;--violet:#ad9bff}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at top right,#123442 0,transparent 34%),radial-gradient(circle at 8% 22%,#15293d 0,transparent 30%),var(--canvas);color:var(--ink);font-family:Inter,ui-sans-serif,system-ui,sans-serif}.shell{max-width:1240px;margin:0 auto;padding:24px 22px 80px}.topbar{display:flex;justify-content:space-between;gap:24px;align-items:center;padding-bottom:22px;border-bottom:1px solid var(--line)}.brand{display:flex;align-items:center;gap:12px;letter-spacing:-.02em;font-weight:800;font-size:24px}.brand-mark{width:28px;height:28px;border:2px solid var(--cyan);border-radius:7px;position:relative;transform:rotate(45deg)}.brand-mark::after{content:"";position:absolute;background:var(--rose);width:2px;height:34px;left:11px;top:-5px;transform:rotate(18deg)}.command,.mono{font-family:"SFMono-Regular",Consolas,monospace}.command{color:var(--lime);background:rgba(180,243,123,.08);border:1px solid rgba(180,243,123,.19);padding:8px 10px;border-radius:8px}.beat-nav{display:grid;grid-template-columns:repeat(5,1fr);gap:6px;margin:22px 0}.beat-nav span{color:var(--muted);font-size:11px;letter-spacing:.13em;font-weight:750;padding:8px 0;border-bottom:2px solid var(--line)}.beat-nav span:nth-child(1){color:var(--rose);border-color:var(--rose)}.beat-nav span:nth-child(2){color:var(--cyan);border-color:var(--cyan)}.beat-nav span:nth-child(3){color:var(--orange);border-color:var(--orange)}.beat-nav span:nth-child(4){color:var(--violet);border-color:var(--violet)}.beat-nav span:nth-child(5){color:var(--lime);border-color:var(--lime)}.notice{border:1px solid rgba(255,189,114,.28);color:#ffdda6;background:rgba(255,189,114,.07);padding:12px 14px;border-radius:10px;font-size:14px}.hero{display:grid;grid-template-columns:1.45fr .85fr;gap:18px;padding:34px 0 22px}.eyebrow{color:var(--cyan);font-size:12px;font-weight:800;letter-spacing:.12em}h1{font-size:clamp(34px,5vw,68px);line-height:.98;letter-spacing:-.06em;max-width:760px;margin:12px 0 16px}h1 em{color:var(--cyan);font-style:normal}.lede{color:var(--muted);max-width:720px;line-height:1.6;font-size:17px}.card{border:1px solid var(--line);border-radius:16px;background:var(--panel);box-shadow:0 18px 42px rgba(0,0,0,.14)}.metric-card{display:grid;grid-template-columns:repeat(2,1fr);padding:10px;align-content:start}.metric{padding:16px;border-bottom:1px solid var(--line)}.metric:nth-child(odd){border-right:1px solid var(--line)}.metric:nth-last-child(-n+2){border-bottom:0}.metric b{display:block;font-size:26px;letter-spacing:-.05em}small{color:var(--muted);display:block;margin-top:4px;line-height:1.35}.section{padding:30px 0 6px}.section-head{display:flex;gap:12px;align-items:baseline;margin-bottom:14px}.number{font-family:"SFMono-Regular",Consolas,monospace;color:var(--cyan);font-size:12px}h2{font-size:22px;letter-spacing:-.03em;margin:0}.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}.panel{padding:19px}.panel h3{font-size:13px;text-transform:uppercase;letter-spacing:.11em;color:var(--muted);margin:0 0 12px}.run-row{display:flex;justify-content:space-between;gap:16px;padding:11px 0;border-top:1px solid var(--line)}.run-row:first-of-type{border-top:0}.run-row strong{font-size:14px}.run-meta{text-align:right;display:flex;flex-direction:column;align-items:end;gap:3px}.verdict{display:inline-block;padding:3px 7px;border-radius:999px;font-size:11px;font-family:"SFMono-Regular",Consolas,monospace;font-weight:800;letter-spacing:.04em}.verdict.pass{background:rgba(180,243,123,.12);color:var(--lime)}.verdict.fail{background:rgba(255,127,156,.14);color:var(--rose)}.verdict.error,.verdict.unstable{background:rgba(255,189,114,.14);color:var(--orange)}.verdict.inapplicable{background:rgba(173,155,255,.14);color:var(--violet)}.witness{border-left:3px solid var(--cyan);padding-left:16px}.hash{color:var(--cyan);font-family:"SFMono-Regular",Consolas,monospace;word-break:break-all;font-size:12px}.pill{display:inline-block;border:1px solid rgba(101,228,223,.3);background:rgba(101,228,223,.08);color:var(--cyan);padding:5px 8px;border-radius:99px;font-size:11px;margin:3px 3px 0 0}.transition{display:flex;align-items:center;gap:12px;padding:14px;margin-top:12px;background:rgba(101,228,223,.06);border:1px solid rgba(101,228,223,.2);border-radius:10px}.arrow{color:var(--cyan);font-size:22px}.attempt{border-top:1px solid var(--line);padding:11px 0;display:grid;grid-template-columns:minmax(110px,.9fr) 90px 2fr;gap:12px;align-items:start}.attempt:first-of-type{border-top:0}.attempt-pass{color:var(--lime);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.attempt-fail{color:var(--rose);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.attempt-unresolved{color:var(--orange);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.evidence-row{display:grid;grid-template-columns:96px 1fr;gap:14px;border-top:1px solid var(--line);padding:12px 0}.evidence-row span{font-family:"SFMono-Regular",Consolas,monospace;font-size:11px;color:var(--cyan);font-weight:700}.evidence-row p{margin:0;line-height:1.45;font-size:14px}.proof{display:grid;grid-template-columns:1fr auto;gap:9px;align-items:center;padding:13px 0;border-top:1px solid var(--line)}.proof:first-of-type{border-top:0}.callout{border:1px solid rgba(180,243,123,.27);background:rgba(180,243,123,.06);padding:17px;border-radius:12px}.callout strong{color:var(--lime);letter-spacing:.05em}button{appearance:none;border:1px solid rgba(101,228,223,.45);background:var(--cyan);color:#071118;padding:11px 14px;border-radius:9px;font:inherit;font-weight:800;cursor:pointer}button:hover{filter:brightness(1.08)}button:disabled{opacity:.7;cursor:wait}.action-row{display:flex;justify-content:space-between;gap:16px;align-items:center;margin-top:18px}.action-status{color:var(--muted);font-size:13px}details{margin-top:14px;border-top:1px solid var(--line);padding-top:12px}summary{cursor:pointer;color:var(--muted);font-size:13px}pre{white-space:pre-wrap;margin:10px 0 0;font:12px/1.45 "SFMono-Regular",Consolas,monospace;color:#b9d2d8}.footer{color:var(--muted);font-size:13px;border-top:1px solid var(--line);margin-top:40px;padding-top:16px;line-height:1.5}@media(max-width:780px){.hero,.grid{grid-template-columns:1fr}.beat-nav{overflow-x:auto;grid-template-columns:repeat(5,115px)}.attempt{grid-template-columns:1fr;gap:4px}.topbar{align-items:flex-start;flex-direction:column}}
:root{--paper:#fff;--ink-reference:#333;--muted-reference:#777;--line-reference:#ddd;--soft-reference:#f5f5f2;--highlight-background:#f2f2ef;--highlight-border:#deded8;--working-background:#eef7ef;--working-border:#cce3d0;--working-text:#28603a;--broken-background:#fff0f0;--broken-border:#efcccc;--broken-text:#8a3030;--text-xs:11px;--text-sm:14px;--text-base:16px;--text-lg:22px;--space-sm:12px;--space-md:20px}
body{background:var(--paper);color:var(--ink-reference);font-family:Circular,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;font-size:16px}
.shell{max-width:1280px;padding:32px 32px 120px}
.topbar{position:relative;justify-content:center;border:0;padding-bottom:24px}
.brand{color:var(--ink-reference);font-size:21px;font-weight:700}
.brand-mark{border-color:var(--ink-reference)}
.brand-mark::after{background:var(--muted-reference)}
.command{position:absolute;right:0;color:var(--ink-reference);background:transparent;border:1px solid var(--line-reference);font-size:12px;font-weight:500}
.beat-nav{max-width:1020px;margin:0 auto 28px}
.beat-nav span,.beat-nav span:nth-child(1),.beat-nav span:nth-child(2),.beat-nav span:nth-child(3),.beat-nav span:nth-child(4),.beat-nav span:nth-child(5){color:var(--muted-reference);border-color:var(--line-reference);font-weight:600}
.notice{max-width:1020px;margin:0 auto;color:var(--muted-reference);background:transparent;border-color:var(--line-reference);border-radius:0;padding:14px 0}
.hero{max-width:1020px;min-height:calc(100vh - 88px);margin:0 auto;grid-template-columns:minmax(0,1fr) minmax(320px,.72fr);gap:80px;padding:32px 0 56px;align-items:center}
.eyebrow{color:var(--muted-reference);font-size:11px;font-weight:600}
h1,h1 em{color:var(--ink-reference);font-style:normal;font-weight:400}
h1{font-size:clamp(44px,5vw,68px);line-height:1.02;letter-spacing:-.055em;margin-top:20px}
.lede{color:var(--muted-reference);font-size:18px;line-height:1.65;margin-top:28px}
.card{color:var(--ink-reference);background:transparent;border:0;border-radius:0;box-shadow:none}
.metric-card{grid-template-columns:repeat(2,1fr);padding:0;border-top:1px solid var(--line-reference)}
.metric{padding:22px 0;border-color:var(--line-reference)}
.metric:nth-child(odd){border-right:0}
.metric:nth-last-child(-n+2){border-bottom:0}
.metric b{color:var(--ink-reference);font-size:32px;font-weight:400}
small,.lede,.panel h3,.action-status,summary,pre,.footer{color:var(--muted-reference)}
.section{border-top:1px solid var(--line-reference);padding:112px 0 0;margin-top:0}
.section+.section{margin-top:128px}
.section-head{max-width:1020px;margin:0 auto 56px;gap:18px}
.number{color:var(--muted-reference);font-size:11px}
h2{color:var(--ink-reference);font-size:42px;font-weight:400;line-height:1.08}
.grid{max-width:1020px;margin:0 auto;grid-template-columns:minmax(280px,.72fr) minmax(0,1.28fr);gap:80px}
.panel{padding:0}
.panel h3{color:var(--ink-reference);font-size:26px;font-weight:400;line-height:1.15;letter-spacing:-.025em;text-transform:none;margin-bottom:28px}
.witness{border-left:0;padding-left:0}
.run-row,.attempt,.evidence-row,.proof{border-color:var(--line-reference);padding:16px 0}
.run-row strong{font-size:15px;font-weight:600}
.run-meta{text-align:right}
.hash{color:var(--muted-reference)}
.pill,.verdict{color:var(--ink-reference)!important;background:transparent!important;border:1px solid var(--line-reference);font-weight:600}
.transition,.callout{color:var(--ink-reference);background:var(--soft-reference)!important;border-color:var(--line-reference)!important;border-radius:0}
.arrow,.attempt-pass,.attempt-fail,.attempt-unresolved,.evidence-row span,.callout strong{color:var(--ink-reference)!important}
.attempt{grid-template-columns:minmax(140px,.9fr) 100px 2fr}
.evidence-row{grid-template-columns:110px 1fr}
.proof{grid-template-columns:1fr auto}
button{color:#fff;background:var(--ink-reference);border-color:var(--ink-reference);border-radius:0;font-weight:600}
button:hover{background:#555;filter:none}
.footer{border-color:var(--line-reference);margin-top:128px;padding-top:24px}
.focus-grid{grid-template-columns:minmax(0,.9fr) minmax(0,1.35fr);gap:28px;align-items:start}
.focus-card{border:1px solid var(--line-reference);border-radius:16px;padding:30px;background:#fff;box-shadow:0 12px 32px rgba(0,0,0,.045);font-size:var(--text-sm)}
.focus-card h3{font-size:var(--text-lg);margin:0 0 var(--space-md)}
.card-kicker{display:block;color:var(--muted-reference);font-size:var(--text-xs);font-weight:700;letter-spacing:.1em;text-transform:uppercase;margin:0 0 var(--space-sm)}
.test-question{font-size:18px;line-height:1.45;margin:0 0 var(--space-sm)}
.plain-example{color:var(--muted-reference);font-size:var(--text-sm);line-height:1.55;margin:0 0 var(--space-md)}
.test-tags{display:flex;flex-wrap:wrap;gap:8px;margin:0 0 var(--space-md)}
.test-tags .pill{margin:0}
.pill,.verdict{background:var(--highlight-background)!important;border-color:var(--highlight-border);color:var(--ink-reference)!important}
.focus-card details{margin-top:var(--space-md)}
.focus-card summary{font-size:var(--text-sm)}
.simple-test{padding:16px;background:var(--highlight-background);border:1px solid var(--highlight-border);border-radius:10px;color:var(--ink-reference);font-size:var(--text-sm);line-height:1.7}
.status-guide{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin:var(--space-md) 0}
.status-item{border:1px solid;border-radius:10px;padding:12px}
.status-item strong{display:block;font-size:var(--text-sm);margin-bottom:3px}
.status-item small{font-size:var(--text-xs);margin:0}
.status-item.working{background:var(--working-background);border-color:var(--working-border);color:var(--working-text)}
.status-item.broken{background:var(--broken-background);border-color:var(--broken-border);color:var(--broken-text)}
.verdict.pass{background:var(--working-background)!important;border-color:var(--working-border);color:var(--working-text)!important}
.verdict.fail{background:var(--broken-background)!important;border-color:var(--broken-border);color:var(--broken-text)!important}
.focus-card .run-row{border:1px solid var(--highlight-border);border-radius:12px;margin-top:var(--space-sm);padding:16px;background:var(--highlight-background)}
.focus-card .run-row:first-of-type{border-top:1px solid var(--line-reference)}
.focus-card .run-row strong{font-size:14px}
.focus-card .run-meta{min-width:112px}
.proof-grid{grid-template-columns:minmax(0,1.2fr) minmax(300px,.8fr);gap:28px}
.compact-section{min-height:100vh;min-height:100svh;display:flex;flex-direction:column;justify-content:center;padding:64px 0}
.compact-section>.section-head,.compact-section>.grid{width:100%}
.compact-section .section-head{margin-bottom:36px}
.compact-section+.section{margin-top:0}
.timeline-panel h3{margin-bottom:8px}
.timeline-intro{color:var(--muted-reference);font-size:var(--text-sm);margin:0 0 16px}
.scroll-frame{position:relative;border:1px solid var(--line-reference);border-radius:14px;overflow:hidden;background:#fff}
.timeline-scroll{max-height:390px;overflow-y:auto;overscroll-behavior:contain;padding:0 18px 58px;scrollbar-width:thin;scrollbar-color:#bbb transparent}
.scroll-hint{position:absolute;right:0;bottom:0;left:0;padding:30px 16px 12px;background:linear-gradient(to bottom,rgba(255,255,255,0),#fff 48%);color:var(--muted-reference);font-size:var(--text-xs);font-weight:700;letter-spacing:.05em;text-align:center;pointer-events:none}
.change-point-card{border:1px solid var(--line-reference);border-radius:14px;padding:28px;background:#fff}
.change-point-card h3{margin-bottom:16px}
.change-point-card>p{line-height:1.55}
.change-point-card .transition{margin:24px 0}
.package-summary{border:1px solid var(--highlight-border);border-radius:12px;background:var(--highlight-background);padding:20px;margin:20px 0}
.package-summary strong{display:block;font-size:var(--text-lg);font-weight:600;margin-bottom:6px}
.package-summary small{font-size:var(--text-sm);margin:0}
.package-summary~.action-status{display:block;margin-top:10px}
.section-summary{max-width:1020px;margin:-28px auto 48px;color:var(--muted-reference);font-size:var(--text-base);line-height:1.55}
.package-intro{color:var(--muted-reference);line-height:1.55}
.package-benefits{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin:0 0 20px}
.package-benefits>div{border-top:1px solid var(--line-reference);padding-top:12px}
.package-benefits strong{display:block;font-size:var(--text-sm);margin-bottom:4px}
.package-benefits small{font-size:var(--text-xs);margin:0}
@media(max-width:780px){.shell{padding:24px 20px 72px}.topbar{align-items:center;flex-direction:row;padding-bottom:16px}.command{position:static;margin-left:auto}.beat-nav{overflow:visible;grid-template-columns:repeat(5,minmax(0,1fr))}.beat-nav span{font-size:9px;letter-spacing:.04em}.hero{min-height:auto;grid-template-columns:1fr;gap:48px;padding:48px 0 80px;align-items:start}.metric-card{grid-template-columns:1fr}.metric,.metric:nth-last-child(-n+2){border-bottom:1px solid var(--line-reference)}.metric:last-child{border-bottom:0}.section{padding-top:80px}.section+.section{margin-top:96px}.section-head{margin-bottom:44px}.grid{grid-template-columns:1fr;gap:64px}.attempt,.evidence-row,.proof{grid-template-columns:1fr;gap:6px}h2{font-size:34px}}
@media(max-width:780px){.compact-section{min-height:auto;display:block;padding:80px 0 0}.compact-section+.section{margin-top:96px}}
@media(max-width:520px){.package-benefits{grid-template-columns:1fr}}
</style>
</head>
<body>
<main class="shell">
<header class="topbar"><div class="brand"><span class="brand-mark" aria-hidden="true"></span><span>FaultLine</span></div><div class="command">DEMO MODE</div></header>
<section class="hero" id="break"><div><div class="eyebrow">A STEP-BY-STEP BUG INVESTIGATION</div><h1>Find where a bug started.<br><em>Prove it with the same test.</em></h1><p class="lede">FaultLine checks saved versions of a project with one approved test. It finds the point where the test changed from working to broken, double-checks the result, and prepares clear information for the person fixing it.</p></div><aside class="card metric-card" aria-label="Investigation size"><div class="metric"><b>${analysis.metrics.sessions}</b><small>work sessions checked</small></div><div class="metric"><b>${analysis.metrics.turns}</b><small>saved steps checked</small></div><div class="metric"><b>${analysis.metrics.files}</b><small>files involved</small></div><div class="metric"><b>${analysis.metrics.changedLines.toLocaleString()}</b><small>lines changed</small></div></aside></section>
<section class="section" id="find"><div class="section-head"><span class="number">01</span><h2>Choose one test</h2></div><div class="grid focus-grid"><article class="card panel focus-card"><span class="card-kicker">FIRST - DEFINE SUCCESS</span><h3>What are we checking?</h3><p class="test-question"><strong>Should a completed refund keep its original settlement currency?</strong></p><p class="plain-example">For example: if a refund started in USD, completing it should not accidentally change it to EUR.</p><div class="test-tags"><span class="pill">same test every time</span><span class="pill">approved before searching</span></div><details><summary>Show a simple test example</summary><pre class="simple-test">Start:  The refund uses USD
Action: Complete the refund
Expect: The refund still uses USD</pre><small>FaultLine repeats this exact check for every saved version.</small></details></article><article class="card panel focus-card"><span class="card-kicker">NEXT - COMPARE CHECKPOINTS</span><h3>Where does the bug first appear?</h3><p>Read the saved checkpoints from top to bottom.</p><div class="status-guide" aria-label="Result meanings"><div class="status-item working"><strong>✓ Working</strong><small>No bug found</small></div><div class="status-item broken"><strong>! Broken</strong><small>Bug is present</small></div></div>${contributionRows}</article></div></section>
<section class="section compact-section"><div class="section-head"><span class="number">02</span><h2>Find the first broken version</h2></div><div class="grid"><article class="card panel timeline-panel"><h3>Saved versions, in order</h3><p class="timeline-intro">Scroll through the checkpoints to see when the result changes.</p><div class="scroll-frame"><div class="timeline-scroll">${timelineRows}</div><div class="scroll-hint">SCROLL TO SEE MORE ↓</div></div></article><article class="card panel change-point-card"><h3>The exact point where it broke</h3><p>We test both versions three times. Repeating the test confirms this is a real change—not a one-time glitch.</p><div class="transition"><div>${verdictBadge(analysis.prevention.lastGood.verdict)}<small>${escapeHtml(lastGood)}</small></div><div class="arrow">→</div><div>${verdictBadge(analysis.prevention.firstBad.verdict)}<small>${escapeHtml(firstBad)}</small></div></div><p><strong>${firstRegression ? "Confirmed: the bug first appears between these two versions." : "We could not confirm where the bug first appears."}</strong></p><small>${firstRegression ? "The same test consistently changes from working to broken here." : "FaultLine will not guess when results are unclear."}</small></article></div></section>
<section class="section compact-section" id="prove"><div class="section-head"><span class="number">03</span><h2>Double-check the suspected changes</h2></div><div class="grid proof-grid"><article class="card panel timeline-panel"><h3>Test smaller combinations</h3><p class="timeline-intro"><strong>${analysis.minimization.budget.used} of ${analysis.minimization.budget.max} test runs used.</strong> Scroll through each combination FaultLine checked.</p><div class="scroll-frame"><div class="timeline-scroll">${attemptRows}</div><div class="scroll-hint">SCROLL TO SEE MORE ↓</div></div></article><article class="card panel change-point-card"><h3>Smallest confirmed set of changes</h3><p class="mono">${escapeHtml(analysis.minimization.candidate.join(" + "))}</p><div class="proof"><span>Add these changes to the working version</span>${verdictBadge(analysis.minimization.sufficiency.verdict)}</div><div class="proof"><span>Remove them from the broken version</span>${verdictBadge(analysis.minimization.necessity.verdict)}</div><p><strong>Confirmed in both directions</strong><small>This proves what happens to this specific test. It does not claim to know what the original author intended.</small></p></article></div></section>
<section class="section"><div class="section-head"><span class="number">04</span><h2>Prepare a clear fix</h2></div><div class="grid"><article class="card panel"><h3>What the evidence tells us</h3>${claimRows}</article><article class="card panel"><h3>What the coding assistant receives</h3><p>FaultLine shares the approved test and the results it actually measured. The assistant can suggest a repair, but it cannot rewrite the evidence.</p><div class="callout"><strong>Required check</strong><small>Add a regression test and verify the working-to-broken boundary.</small></div><div class="callout" style="margin-top:10px"><strong>Helpful note</strong><small>Record the rule future contributors should follow.</small></div></article></div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">05</span><h2>Make sure the bug stays fixed</h2></div><p class="section-summary">First, confirm the repair works. Then save the results so another person can review the same evidence.</p><div class="grid"><article class="card panel"><h3>Check before, during, and after</h3><div class="proof"><span>Version before the bug</span>${verdictBadge(analysis.prevention.lastGood.verdict)}</div><div class="proof"><span>Version where the bug appeared</span>${verdictBadge(analysis.prevention.firstBad.verdict)}</div><div class="proof"><span>Version after the repair</span>${verdictBadge(analysis.prevention.repaired.verdict)}</div><div class="callout" style="margin-top:16px"><strong>THE FIX IS VERIFIED</strong><small>The same approved test was used for all three versions.</small></div></article><article class="card panel"><span class="card-kicker">SAVE THE RESULT</span><h3>Shareable proof package</h3><p class="package-intro">This package keeps the approved test and its results together as one reviewable record.</p><div class="package-summary"><strong>Evidence grade ${escapeHtml(analysis.grade.value)}</strong><small>${analysis.grade.value === "A" ? "Strong evidence: every required check passed." : "The grade shows how complete and repeatable the evidence is."}</small></div><div class="package-benefits"><div><strong>Share it</strong><small>Give a teammate the same evidence.</small></div><div><strong>Verify it</strong><small>Check that the saved results were not changed.</small></div></div><button id="rerun" type="button">Run the demo again</button><span class="action-status" id="action-status">uses the safe built-in example</span></article></div></section>
<footer class="footer">FaultLine reports only what its tests can show. It does not guess who caused a bug, why they made a change, or what they were thinking.</footer>
</main>
<script>
const rerun=document.getElementById("rerun");const status=document.getElementById("action-status");rerun?.addEventListener("click",async()=>{rerun.disabled=true;status.textContent="running the example again…";try{const response=await fetch("/api/rerun",{method:"POST"});if(!response.ok)throw new Error(await response.text());status.textContent="finished; refreshing the page…";window.location.reload()}catch(error){status.textContent="the demo could not run: "+(error instanceof Error?error.message:String(error));rerun.disabled=false}});
</script>
</body>
</html>`;
}
