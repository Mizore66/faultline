import { fixtureStateById } from "./fixture.js";
import { escapeHtml } from "./incident-page.js";
import type { DemoAnalysis, RunRecord, Verdict } from "./domain.js";

function verdictClass(verdict: Verdict): string {
  return verdict.toLowerCase().replaceAll("_", "-");
}

function verdictBadge(verdict: Verdict): string {
  const label = verdict === "PASS"
    ? "WORKING"
    : verdict === "FAIL"
      ? "BROKEN"
      : verdict.replaceAll("_", " ");
  return `<span class="verdict ${verdictClass(verdict)}">${escapeHtml(label)}</span>`;
}

function friendlyReason(reasonCode: string): string {
  if (reasonCode === "WITNESS_SATISFIED") return "Passed the approved test";
  if (reasonCode === "WITNESS_ASSERTION_FAILED") return "Failed the approved test";
  return "Could not finish this check";
}

function friendlyCheckpointName(label: string): string {
  const session = label.match(/^(\w+)\s+contribution/i)?.[1];
  return session ? `${session} version` : label.replace(/\s+contribution boundary$/i, " version");
}

function friendlyTimelineName(label: string): string {
  if (/last good/i.test(label)) return "Step 4 — last version that still worked";
  if (/first bad/i.test(label)) return "Step 5 — first version that broke";
  if (/repaired/i.test(label)) return "Step 7 — version after a repair";
  if (/reintroduced/i.test(label)) return "Step 8 — bug came back later";
  const turn = label.match(/Turn\s+(\d+)/i)?.[1];
  return turn ? `Step ${turn}` : label;
}

function friendlyStateCaption(label: string): string {
  if (/last good/i.test(label)) return "Last good version";
  if (/first bad/i.test(label)) return "First broken version";
  if (/repair/i.test(label)) return "Repaired version";
  return friendlyTimelineName(label);
}

function friendlyChangeName(name: string): string {
  if (name.includes("settlement-display-default")) return "display default change";
  if (name.includes("settlement-display-boundary")) return "settlement boundary change";
  if (name.includes("partial")) return "incomplete change (could not apply)";
  return name;
}

function friendlyAttemptNote(note: string): string {
  if (/No implicated edits/i.test(note)) return "With no changes, the test still passes.";
  if (/display default alone/i.test(note)) return "Changing only the display currency does not break the test.";
  if (/boundary assignment alone/i.test(note)) return "Changing only the settlement side is not enough by itself.";
  if (/partial patch|unresolved/i.test(note)) return "This incomplete change could not be applied, so it does not count as proof.";
  if (/reproduces the frozen witness failure/i.test(note)) return "Adding both changes to the working version makes the test fail.";
  if (/restores the frozen witness pass/i.test(note)) return "Removing both changes from the broken version makes the test pass again.";
  return note;
}

function friendlyClaimStatement(statement: string): string {
  if (/passes before and fails after/i.test(statement)) {
    return "The approved test passed before Cedar step 5 and failed afterward.";
  }
  if (/first stable pass-to-fail/i.test(statement)) {
    return "Cedar step 5 is the first clear place the test flips from working to broken.";
  }
  if (/two-edit candidate is sufficient/i.test(statement)) {
    return "Two specific changes are enough to break the working version, and both are needed to keep the failure.";
  }
  if (/Settlement and display-currency/i.test(statement)) {
    return "It looks like settlement currency and display currency got mixed together at that point.";
  }
  if (/Whether an agent intended/i.test(statement)) {
    return "We do not know whether anyone meant to make that tradeoff — the tests do not show intent.";
  }
  if (/Cached replay does not establish/i.test(statement)) {
    return "This demo replay did not fully prove a working-to-broken change.";
  }
  if (/No stable boundary is certified/i.test(statement)) {
    return "This demo could not certify exactly where the bug starts.";
  }
  if (/Cached counterfactual results are not a proof/i.test(statement)) {
    return "These demo checks are helpful, but they are not a full proof package.";
  }
  return statement;
}

function timelineRunRow(run: RunRecord): string {
  const state = fixtureStateById(run.stateId);
  const name = friendlyTimelineName(state?.label ?? run.stateId);
  return `<div class="run-row"><div><strong>${escapeHtml(name)}</strong><small>${escapeHtml(friendlyReason(run.reasonCode))}</small></div><div class="run-meta">${verdictBadge(run.verdict)}<small>${run.executionKind === "CACHED" ? "saved result" : `${run.durationMs} ms`}</small></div></div>`;
}

function contributionRunRow(run: RunRecord): string {
  const state = fixtureStateById(run.stateId);
  const name = friendlyCheckpointName(state?.label ?? run.stateId);
  return `<div class="run-row"><div><strong>${escapeHtml(name)}</strong><small>${escapeHtml(friendlyReason(run.reasonCode))}</small></div><div class="run-meta">${verdictBadge(run.verdict)}<small>${run.executionKind === "CACHED" ? "saved result" : `${run.durationMs} ms`}</small></div></div>`;
}

function evidenceRow(kind: string, statement: string): string {
  const friendlyKind = kind === "EXECUTED"
    ? "Measured"
    : kind === "DERIVED"
      ? "Confirmed"
      : kind === "INFERRED"
        ? "Possible clue"
        : kind === "UNKNOWN"
          ? "Not shown"
          : kind;
  return `<div class="evidence-row"><span>${escapeHtml(friendlyKind)}</span><p>${escapeHtml(friendlyClaimStatement(statement))}</p></div>`;
}

/**
 * Deterministic sample page in a calm white-paper product walkthrough style
 * (inspired by whereisrussell.com). Content stays fixture-scoped so it cannot
 * be mistaken for a live Docker proof or customer incident.
 */
export function renderIncidentPage(analysis: DemoAnalysis): string {
  const firstRegression = analysis.transitions.find((item) => item.kind === "PASS_TO_FAIL" && item.stable);
  const lastGood = fixtureStateById(analysis.prevention.lastGood.stateId)?.label ?? analysis.prevention.lastGood.stateId;
  const firstBad = fixtureStateById(analysis.prevention.firstBad.stateId)?.label ?? analysis.prevention.firstBad.stateId;
  // Keep the working→broken story in three rows so the checkpoint card stays compact.
  const contributionRows = analysis.contributionRuns.slice(0, 3).map(contributionRunRow).join("");
  const timelineRows = analysis.timelineRuns.map(timelineRunRow).join("");
  const attemptRows = analysis.minimization.attempts.map((attempt) => {
    const changes = attempt.subset.length
      ? attempt.subset.map(friendlyChangeName).join(" + ")
      : "No changes";
    const outcome = attempt.outcome === "PASS"
      ? "WORKING"
      : attempt.outcome === "FAIL"
        ? "BROKEN"
        : "COULD NOT CHECK";
    return `<div class="attempt"><span class="mono">${escapeHtml(changes)}</span><b class="attempt-${attempt.outcome.toLowerCase()}">${escapeHtml(outcome)}</b><small>${escapeHtml(friendlyAttemptNote(attempt.note))}</small></div>`;
  }).join("");
  const claimRows = analysis.claims.map((claim) => evidenceRow(claim.kind, claim.statement)).join("");
  const candidateLabel = analysis.minimization.candidate.map(friendlyChangeName).join(" + ");
  const lastGoodCaption = friendlyStateCaption(lastGood);
  const firstBadCaption = friendlyStateCaption(firstBad);
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>FaultLine — ${escapeHtml(analysis.fixture.title)}</title>
<style>
:root{color-scheme:light;--ink:#333;--muted:#777;--canvas:#fff;--panel:#fff;--line:#ddd;--cyan:#333;--lime:#28603a;--orange:#8a6030;--rose:#8a3030;--violet:#555}*{box-sizing:border-box}body{margin:0;background:var(--canvas);color:var(--ink);font-family:Circular,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.shell{max-width:1240px;margin:0 auto;padding:24px 22px 80px}.topbar{display:flex;justify-content:space-between;gap:24px;align-items:center;padding-bottom:22px;border-bottom:1px solid var(--line)}.brand{display:flex;align-items:center;gap:12px;letter-spacing:-.02em;font-weight:800;font-size:24px}.brand-mark{width:28px;height:28px;border:2px solid var(--cyan);border-radius:7px;position:relative;transform:rotate(45deg)}.brand-mark::after{content:"";position:absolute;background:var(--rose);width:2px;height:34px;left:11px;top:-5px;transform:rotate(18deg)}.command,.mono{font-family:"SFMono-Regular",Consolas,monospace}.command{color:var(--lime);background:rgba(180,243,123,.08);border:1px solid rgba(180,243,123,.19);padding:8px 10px;border-radius:8px}.beat-nav{display:grid;grid-template-columns:repeat(5,1fr);gap:6px;margin:22px 0}.beat-nav span{color:var(--muted);font-size:11px;letter-spacing:.13em;font-weight:750;padding:8px 0;border-bottom:2px solid var(--line)}.beat-nav span:nth-child(1){color:var(--rose);border-color:var(--rose)}.beat-nav span:nth-child(2){color:var(--cyan);border-color:var(--cyan)}.beat-nav span:nth-child(3){color:var(--orange);border-color:var(--orange)}.beat-nav span:nth-child(4){color:var(--violet);border-color:var(--violet)}.beat-nav span:nth-child(5){color:var(--lime);border-color:var(--lime)}.notice{border:1px solid rgba(255,189,114,.28);color:#ffdda6;background:rgba(255,189,114,.07);padding:12px 14px;border-radius:10px;font-size:14px}.hero{display:grid;grid-template-columns:1.45fr .85fr;gap:18px;padding:34px 0 22px}.eyebrow{color:var(--cyan);font-size:12px;font-weight:800;letter-spacing:.12em}h1{font-size:clamp(34px,5vw,68px);line-height:.98;letter-spacing:-.06em;max-width:760px;margin:12px 0 16px}h1 em{color:var(--cyan);font-style:normal}.lede{color:var(--muted);max-width:720px;line-height:1.6;font-size:17px}.card{border:1px solid var(--line);border-radius:16px;background:var(--panel);box-shadow:0 18px 42px rgba(0,0,0,.14)}.metric-card{display:grid;grid-template-columns:repeat(2,1fr);padding:10px;align-content:start}.metric{padding:16px;border-bottom:1px solid var(--line)}.metric:nth-child(odd){border-right:1px solid var(--line)}.metric:nth-last-child(-n+2){border-bottom:0}.metric b{display:block;font-size:26px;letter-spacing:-.05em}small{color:var(--muted);display:block;margin-top:4px;line-height:1.35}.section{padding:30px 0 6px}.section-head{display:flex;gap:12px;align-items:baseline;margin-bottom:14px}.number{font-family:"SFMono-Regular",Consolas,monospace;color:var(--cyan);font-size:12px}h2{font-size:22px;letter-spacing:-.03em;margin:0}.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}.panel{padding:19px}.panel h3{font-size:13px;text-transform:uppercase;letter-spacing:.11em;color:var(--muted);margin:0 0 12px}.run-row{display:flex;justify-content:space-between;gap:16px;padding:11px 0;border-top:1px solid var(--line)}.run-row:first-of-type{border-top:0}.run-row strong{font-size:14px}.run-meta{text-align:right;display:flex;flex-direction:column;align-items:end;gap:3px}.verdict{display:inline-block;padding:3px 7px;border-radius:999px;font-size:11px;font-family:"SFMono-Regular",Consolas,monospace;font-weight:800;letter-spacing:.04em}.verdict.pass{background:rgba(180,243,123,.12);color:var(--lime)}.verdict.fail{background:rgba(255,127,156,.14);color:var(--rose)}.verdict.error,.verdict.unstable{background:rgba(255,189,114,.14);color:var(--orange)}.verdict.inapplicable{background:rgba(173,155,255,.14);color:var(--violet)}.witness{border-left:3px solid var(--cyan);padding-left:16px}.hash{color:var(--cyan);font-family:"SFMono-Regular",Consolas,monospace;word-break:break-all;font-size:12px}.pill{display:inline-block;border:1px solid rgba(101,228,223,.3);background:rgba(101,228,223,.08);color:var(--cyan);padding:5px 8px;border-radius:99px;font-size:11px;margin:3px 3px 0 0}.transition{display:flex;align-items:center;gap:12px;padding:14px;margin-top:12px;background:rgba(101,228,223,.06);border:1px solid rgba(101,228,223,.2);border-radius:10px}.arrow{color:var(--cyan);font-size:22px}.attempt{border-top:1px solid var(--line);padding:11px 0;display:grid;grid-template-columns:minmax(110px,.9fr) 90px 2fr;gap:12px;align-items:start}.attempt:first-of-type{border-top:0}.attempt-pass{color:var(--lime);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.attempt-fail{color:var(--rose);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.attempt-unresolved{color:var(--orange);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.evidence-row{display:grid;grid-template-columns:96px 1fr;gap:14px;border-top:1px solid var(--line);padding:12px 0}.evidence-row span{font-family:"SFMono-Regular",Consolas,monospace;font-size:11px;color:var(--cyan);font-weight:700}.evidence-row p{margin:0;line-height:1.45;font-size:14px}.proof{display:grid;grid-template-columns:1fr auto;gap:9px;align-items:center;padding:13px 0;border-top:1px solid var(--line)}.proof:first-of-type{border-top:0}.callout{border:1px solid rgba(180,243,123,.27);background:rgba(180,243,123,.06);padding:17px;border-radius:12px}.callout strong{color:var(--lime);letter-spacing:.05em}button{appearance:none;border:1px solid rgba(101,228,223,.45);background:var(--cyan);color:#071118;padding:11px 14px;border-radius:9px;font:inherit;font-weight:800;cursor:pointer}button:hover{filter:brightness(1.08)}button:disabled{opacity:.7;cursor:wait}.action-row{display:flex;justify-content:space-between;gap:16px;align-items:center;margin-top:18px}.action-status{color:var(--muted);font-size:13px}details{margin-top:14px;border-top:1px solid var(--line);padding-top:12px}summary{cursor:pointer;color:var(--muted);font-size:13px}pre{white-space:pre-wrap;margin:10px 0 0;font:12px/1.45 "SFMono-Regular",Consolas,monospace;color:#b9d2d8}.footer{color:var(--muted);font-size:13px;border-top:1px solid var(--line);margin-top:40px;padding-top:16px;line-height:1.5}@media(max-width:780px){.hero,.grid{grid-template-columns:1fr}.beat-nav{overflow-x:auto;grid-template-columns:repeat(5,115px)}.attempt{grid-template-columns:1fr;gap:4px}.topbar{align-items:flex-start;flex-direction:column}}
:root{--paper:#fff;--ink-reference:#333;--muted-reference:#777;--line-reference:#ddd;--soft-reference:#f5f5f2;--highlight-background:#f2f2ef;--highlight-border:#deded8;--working-background:#eef7ef;--working-border:#cce3d0;--working-text:#28603a;--broken-background:#fff0f0;--broken-border:#efcccc;--broken-text:#8a3030;--text-xs:11px;--text-sm:14px;--text-base:16px;--text-lg:22px;--space-sm:12px;--space-md:20px}
html{scroll-snap-type:none;scroll-behavior:auto}
body{background:var(--paper);color:var(--ink-reference);font-family:Circular,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;font-size:16px}
.shell{max-width:1280px;padding:32px 32px 0}
.topbar{position:sticky;top:0;z-index:30;justify-content:center;border:0;padding:16px 0 24px;background:rgba(255,255,255,.92);backdrop-filter:blur(8px)}
.brand{color:var(--ink-reference);font-size:21px;font-weight:700}
.brand-mark{border-color:var(--ink-reference)}
.brand-mark::after{background:var(--muted-reference)}
.command{position:absolute;right:0;color:var(--ink-reference);background:transparent;border:1px solid var(--line-reference);font-size:12px;font-weight:500}
.beat-nav{max-width:1020px;margin:0 auto 28px}
.beat-nav span,.beat-nav span:nth-child(1),.beat-nav span:nth-child(2),.beat-nav span:nth-child(3),.beat-nav span:nth-child(4),.beat-nav span:nth-child(5){color:var(--muted-reference);border-color:var(--line-reference);font-weight:600}
.notice{max-width:1020px;margin:0 auto;color:var(--muted-reference);background:transparent;border-color:var(--line-reference);border-radius:0;padding:14px 0}
.hero,.section{scroll-snap-align:center;scroll-snap-stop:always;min-height:100svh;box-sizing:border-box}
.hero{max-width:1020px;margin:0 auto;grid-template-columns:minmax(0,1fr) minmax(320px,.72fr);gap:80px;padding:32px 0 56px;align-items:center;display:grid}
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
.section{border-top:1px solid var(--line-reference);padding:88px 0;margin-top:0;display:flex;flex-direction:column;justify-content:center}
.section+.section{margin-top:0}
.footer{scroll-snap-align:end;scroll-snap-stop:normal;border-color:var(--line-reference);margin-top:0;padding:48px 0 80px}
.section-head{width:100%;max-width:1020px;margin:0 auto 56px;gap:18px;justify-content:flex-start;align-self:center;text-align:left}
.section-summary{width:100%;max-width:1020px;margin:0 auto 48px;align-self:center;text-align:left}
.number{color:var(--muted-reference);font-size:11px}
h2{color:var(--ink-reference);font-size:42px;font-weight:400;line-height:1.08;text-align:left}
.grid{width:100%;max-width:1020px;margin:0 auto;align-self:center;grid-template-columns:minmax(280px,.72fr) minmax(0,1.28fr);gap:80px}
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
.compact-section{justify-content:center;padding:88px 0}
.find-version-section{justify-content:center}
.section.pin-start{justify-content:flex-start;padding-top:96px;padding-bottom:64px}
.compact-section>.section-head,.compact-section>.grid,.section>.section-summary{width:100%}
.compact-section .section-head{margin-bottom:36px}
.compact-section+.section{margin-top:0}
.timeline-scroll{overscroll-behavior:contain}
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
.section-summary{color:var(--muted-reference);font-size:var(--text-base);line-height:1.55}
.package-intro{color:var(--muted-reference);line-height:1.55}
.package-benefits{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin:0 0 20px}
.package-benefits>div{border-top:1px solid var(--line-reference);padding-top:12px}
.package-benefits strong{display:block;font-size:var(--text-sm);margin-bottom:4px}
.package-benefits small{font-size:var(--text-xs);margin:0}
.claims-scroll{max-height:320px}
@media(max-width:780px){.shell{padding:24px 20px 0}.topbar{align-items:center;flex-direction:row;padding-bottom:16px}.command{position:static;margin-left:auto}.beat-nav{overflow:visible;grid-template-columns:repeat(5,minmax(0,1fr))}.beat-nav span{font-size:9px;letter-spacing:.04em}.hero,.section{min-height:100svh}.hero{grid-template-columns:1fr;gap:48px;padding:48px 0;align-items:start}.metric-card{grid-template-columns:1fr}.metric,.metric:nth-last-child(-n+2){border-bottom:1px solid var(--line-reference)}.metric:last-child{border-bottom:0}.section{padding:72px 0;justify-content:flex-start}.section+.section{margin-top:0}.section-head{margin-bottom:44px}.grid{grid-template-columns:1fr;gap:64px}.attempt,.evidence-row,.proof{grid-template-columns:1fr;gap:6px}h2{font-size:34px}.compact-section{padding:72px 0}.package-benefits{grid-template-columns:1fr}}
</style>
</head>
<body>
<main class="shell">
<header class="topbar"><div class="brand"><span class="brand-mark" aria-hidden="true"></span><span>FaultLine</span></div><div class="command">DEMO MODE</div></header>
<section class="hero" id="break"><div><div class="eyebrow">A STEP-BY-STEP BUG INVESTIGATION</div><h1>Find where a bug started.<br><em>Prove it with the same test.</em></h1><p class="lede">FaultLine checks saved versions of a project with one approved test. It finds the point where the test changed from working to broken, double-checks the result, and prepares clear information for the person fixing it.</p></div><aside class="card metric-card" aria-label="Investigation size"><div class="metric"><b>${analysis.metrics.sessions}</b><small>work sessions checked</small></div><div class="metric"><b>${analysis.metrics.turns}</b><small>saved steps checked</small></div><div class="metric"><b>${analysis.metrics.files}</b><small>files involved</small></div><div class="metric"><b>${analysis.metrics.changedLines.toLocaleString()}</b><small>lines changed</small></div></aside></section>
<section class="section pin-start" id="find"><div class="section-head"><span class="number">01</span><h2>Pick one simple test</h2></div><div class="grid focus-grid"><article class="card panel focus-card"><span class="card-kicker">STEP 1 - WRITE THE RULE</span><h3>What should stay true?</h3><p class="test-question"><strong>After a refund finishes, should it keep the same money currency it started with?</strong></p><p class="plain-example">Example: if the refund began in US dollars, finishing it should not quietly switch it to euros.</p><div class="test-tags"><span class="pill">one clear yes/no check</span><span class="pill">agreed on before digging</span></div><details><summary>Show a plain-English test</summary><pre class="simple-test">Start:  Refund is in USD
Action: Finish the refund
Expect: Refund is still in USD</pre><small>FaultLine runs this same check on every saved version of the project.</small></details></article><article class="card panel focus-card"><span class="card-kicker">STEP 2 - TRY A FEW VERSIONS</span><h3>Which versions pass or fail?</h3><p>Read top to bottom. You’re looking for the first time the result flips from working to broken.</p><div class="status-guide" aria-label="Result meanings"><div class="status-item working"><strong>✓ Working</strong><small>This version still passes</small></div><div class="status-item broken"><strong>! Broken</strong><small>This version fails the test</small></div></div>${contributionRows}</article></div></section>
<section class="section compact-section find-version-section"><div class="section-head"><span class="number">02</span><h2>Find when it first broke</h2></div><div class="grid"><article class="card panel timeline-panel"><h3>Saved steps, in order</h3><p class="timeline-intro">Scroll through the timeline. Each step is one saved point in the work.</p><div class="scroll-frame"><div class="timeline-scroll">${timelineRows}</div><div class="scroll-hint">SCROLL TO SEE MORE ↓</div></div></article><article class="card panel change-point-card"><h3>Where the bug starts</h3><p>We run the same test three times on both sides. That helps prove the change is real — not a one-off fluke.</p><div class="transition"><div>${verdictBadge(analysis.prevention.lastGood.verdict)}<small>${escapeHtml(lastGoodCaption)}</small></div><div class="arrow">→</div><div>${verdictBadge(analysis.prevention.firstBad.verdict)}<small>${escapeHtml(firstBadCaption)}</small></div></div><p><strong>${firstRegression ? "Confirmed: the bug first shows up between these two versions." : "We could not confirm exactly where the bug starts."}</strong></p><small>${firstRegression ? "Here, the same test reliably changes from working to broken." : "If the results are unclear, FaultLine will not guess."}</small></article></div></section>
<section class="section compact-section" id="prove"><div class="section-head"><span class="number">03</span><h2>Check which changes matter</h2></div><div class="grid proof-grid"><article class="card panel timeline-panel"><h3>Try smaller sets of changes</h3><p class="timeline-intro"><strong>${analysis.minimization.budget.used} of ${analysis.minimization.budget.max} checks used.</strong> Scroll to see each combination FaultLine tried.</p><div class="scroll-frame"><div class="timeline-scroll">${attemptRows}</div><div class="scroll-hint">SCROLL TO SEE MORE ↓</div></div></article><article class="card panel change-point-card"><h3>Smallest set that still breaks it</h3><p class="mono">${escapeHtml(candidateLabel)}</p><div class="proof"><span>Add these to the working version</span>${verdictBadge(analysis.minimization.sufficiency.verdict)}</div><div class="proof"><span>Take them out of the broken version</span>${verdictBadge(analysis.minimization.necessity.verdict)}</div><p><strong>Checked both ways</strong><small>This shows what breaks this test. It does not claim to know what the author intended.</small></p></article></div></section>
<section class="section"><div class="section-head"><span class="number">04</span><h2>Write a clear handoff</h2></div><div class="grid"><article class="card panel timeline-panel"><h3>What we can say so far</h3><p class="timeline-intro">Scroll through plain notes based only on the test results.</p><div class="scroll-frame"><div class="timeline-scroll claims-scroll">${claimRows}</div><div class="scroll-hint">SCROLL TO SEE MORE ↓</div></div></article><article class="card panel"><h3>What a coding helper gets</h3><p>FaultLine shares the approved test and the results it measured. A helper can suggest a fix, but it cannot invent new “facts.”</p><div class="callout"><strong>Must do</strong><small>Add a repeatable test and re-check the working → broken point.</small></div><div class="callout" style="margin-top:10px"><strong>Nice to do</strong><small>Write down the rule so the next person does not repeat the bug.</small></div></article></div></section>
<section class="section pin-start" id="prevent"><div class="section-head"><span class="number">05</span><h2>Confirm the fix and save the proof</h2></div><p class="section-summary">First make sure the repair works. Then save a package so someone else can review the same evidence.</p><div class="grid focus-grid"><article class="card panel focus-card"><h3>Before, at the bug, and after the fix</h3><div class="proof"><span>Version before the bug</span>${verdictBadge(analysis.prevention.lastGood.verdict)}</div><div class="proof"><span>Version where it broke</span>${verdictBadge(analysis.prevention.firstBad.verdict)}</div><div class="proof"><span>Version after the repair</span>${verdictBadge(analysis.prevention.repaired.verdict)}</div><div class="callout" style="margin-top:16px"><strong>THE FIX CHECKS OUT</strong><small>The same approved test was used on all three versions.</small></div></article><article class="card panel focus-card"><span class="card-kicker">KEEP A RECORD</span><h3>Shareable result package</h3><p class="package-intro">This package stores the approved test and its results so another person can review the same story.</p><div class="package-summary"><strong>Result grade ${escapeHtml(analysis.grade.value)}</strong><small>${analysis.grade.value === "A" ? "Strong: the important checks all passed." : "This grade shows how complete and repeatable the checks were."}</small></div><div class="package-benefits"><div><strong>Share it</strong><small>Send a teammate the same evidence.</small></div><div><strong>Double-check it</strong><small>Confirm the saved results were not edited.</small></div></div><button id="rerun" type="button">Run the demo again</button><span class="action-status" id="action-status">uses the safe built-in example</span></article></div></section>
<footer class="footer">FaultLine only reports what its tests can show. It does not guess who caused a bug, why they changed the code, or what they were thinking.</footer>
</main>
<script>
const rerun=document.getElementById("rerun");const status=document.getElementById("action-status");rerun?.addEventListener("click",async()=>{rerun.disabled=true;status.textContent="running the example again…";try{const response=await fetch("/api/rerun",{method:"POST"});if(!response.ok)throw new Error(await response.text());status.textContent="finished; refreshing the page…";window.location.reload()}catch(error){status.textContent="the demo could not run: "+(error instanceof Error?error.message:String(error));rerun.disabled=false}});
(()=>{const panels=[...document.querySelectorAll(".hero,.section")];if(panels.length<2)return;let animating=false;let touchY=null;let softRaf=null;let softFrom=0;let softTo=0;let softT0=0;const duration=1100;const softDur=520;const ease=t=>t<0.5?4*t*t*t:1-Math.pow(-2*t+2,3)/2;const softEase=t=>1-Math.pow(1-t,3);const isPinStart=el=>el.classList.contains("pin-start");const topbarH=()=>{const bar=document.querySelector(".topbar");return (bar instanceof HTMLElement?bar.getBoundingClientRect().height:72)+12;};const maxScroll=()=>Math.max(0,document.documentElement.scrollHeight-window.innerHeight);const pinRange=el=>{const head=el.querySelector(".section-head")||el;const pinY=Math.max(0,head.getBoundingClientRect().top+window.scrollY-topbarH());const rect=el.getBoundingClientRect();const bottom=rect.top+window.scrollY+rect.height;const maxY=Math.max(pinY,bottom-window.innerHeight);return{pinY,maxY:Math.min(maxY,maxScroll())};};const targetY=el=>{if(isPinStart(el))return pinRange(el).pinY;const rect=el.getBoundingClientRect();const mid=rect.top+window.scrollY+rect.height/2;return Math.max(0,Math.min(mid-window.innerHeight/2,maxScroll()));};const nearestIndex=()=>{const viewMid=window.scrollY+window.innerHeight/2;let best=0,bestDist=Infinity;panels.forEach((el,i)=>{const rect=el.getBoundingClientRect();const mid=rect.top+window.scrollY+rect.height/2;const dist=Math.abs(mid-viewMid);if(dist<bestDist){bestDist=dist;best=i;}});return best;};const stopSoft=()=>{if(softRaf){cancelAnimationFrame(softRaf);softRaf=null;}};const runSoft=now=>{const t=Math.min(1,(now-softT0)/softDur);window.scrollTo(0,softFrom+(softTo-softFrom)*softEase(t));if(t<1)softRaf=requestAnimationFrame(runSoft);else softRaf=null;};const freeScroll=(el,delta)=>{const{pinY,maxY}=pinRange(el);const base=softRaf?softTo:window.scrollY;const next=Math.max(pinY,Math.min(base+delta*0.72,maxY));if(Math.abs(next-window.scrollY)<0.5&&!softRaf)return;softFrom=window.scrollY;softTo=next;softT0=performance.now();if(!softRaf)softRaf=requestAnimationFrame(runSoft);};const goTo=index=>{const clamped=Math.max(0,Math.min(panels.length-1,index));const startY=window.scrollY;const end=targetY(panels[clamped]);if(Math.abs(end-startY)<2||animating)return;stopSoft();animating=true;const t0=performance.now();const step=now=>{const t=Math.min(1,(now-t0)/duration);window.scrollTo(0,startY+(end-startY)*ease(t));if(t<1)requestAnimationFrame(step);else animating=false;};requestAnimationFrame(step);};const nestedAllows=(el,delta)=>{const scroller=el?.closest?.(".timeline-scroll");if(!scroller)return false;const top=scroller.scrollTop;const max=scroller.scrollHeight-scroller.clientHeight;if(delta>0&&top<max-1)return true;if(delta<0&&top>1)return true;return false;};const sectionAllows=(el,delta)=>{if(!isPinStart(el))return false;const{pinY,maxY}=pinRange(el);const y=softRaf?softTo:window.scrollY;if(delta>0&&y<maxY-2)return true;if(delta<0&&y>pinY+2)return true;return false;};const onDelta=delta=>{if(!delta||animating)return;const current=nearestIndex();const el=panels[current];if(sectionAllows(el,delta)){freeScroll(el,Math.sign(delta)*Math.min(Math.abs(delta),180));return;}goTo(current+(delta>0?1:-1));};window.addEventListener("wheel",e=>{if(nestedAllows(e.target,e.deltaY))return;e.preventDefault();if(animating)return;const current=nearestIndex();const el=panels[current];if(sectionAllows(el,e.deltaY)){freeScroll(el,e.deltaY);return;}onDelta(e.deltaY);},{passive:false});window.addEventListener("keydown",e=>{if(e.defaultPrevented||e.altKey||e.ctrlKey||e.metaKey)return;const tag=(e.target instanceof Element?e.target.tagName:"");if(tag==="INPUT"||tag==="TEXTAREA"||tag==="SELECT"||(e.target instanceof Element&&e.target.isContentEditable))return;if(["ArrowDown","PageDown"," "].includes(e.key)){e.preventDefault();onDelta(Math.round(window.innerHeight*0.32));}else if(["ArrowUp","PageUp"].includes(e.key)){e.preventDefault();onDelta(-Math.round(window.innerHeight*0.32));}});window.addEventListener("touchstart",e=>{touchY=e.changedTouches[0]?.clientY??null},{passive:true});window.addEventListener("touchend",e=>{if(touchY==null||animating)return;const y=e.changedTouches[0]?.clientY??touchY;const delta=touchY-y;touchY=null;if(Math.abs(delta)<48)return;if(nestedAllows(e.target,delta))return;onDelta(delta);},{passive:true});})();
</script>


</body>
</html>`;
}
