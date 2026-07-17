import { existsSync, lstatSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, extname, join, parse, relative, resolve } from "node:path";
import { createDemoAnalysis } from "./engine.js";
import { fixtureStateById } from "./fixture.js";
import { resolveSafeDirectorySegment } from "./safe-directory.js";
import type { DemoAnalysis, RunRecord } from "./domain.js";

const previewFileName = "judge-preview.html";

export type JudgePreviewWriteResult = {
  path: string;
  bytes: number;
};

function escapeHtml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function verdictClass(verdict: RunRecord["verdict"]): string {
  return verdict.toLowerCase();
}

function verdictBadge(run: RunRecord): string {
  return `<span class="verdict ${verdictClass(run.verdict)}">${escapeHtml(run.verdict)}</span>`;
}

function sampleStateLabel(stateId: string): string {
  return fixtureStateById(stateId)?.label ?? stateId;
}

/**
 * The static page intentionally accepts cached replay data only. A preview
 * must never inherit an executed-run claim or act as a substitute for a
 * verified Git/Docker proof package.
 */
export function renderJudgePreviewPage(analysis: DemoAnalysis = createDemoAnalysis("REPLAY")): string {
  if (analysis.mode !== "REPLAY") {
    throw new Error("Judge preview only renders the deterministic REPLAY fixture.");
  }
  const boundary = analysis.transitions.find((transition) => transition.kind === "PASS_TO_FAIL");
  const boundaryCopy = boundary === undefined
    ? "No sample pass-to-fail boundary was recorded."
    : `${sampleStateLabel(boundary.beforeStateId)} to ${sampleStateLabel(boundary.afterStateId)}`;
  const sampleStates = [analysis.prevention.lastGood, analysis.prevention.firstBad, analysis.prevention.repaired];
  const stateRows = sampleStates.map((run) => `<tr><td>${escapeHtml(sampleStateLabel(run.stateId))}</td><td>${verdictBadge(run)}</td><td>${escapeHtml(run.reasonCode)}</td></tr>`).join("");
  const claimRows = analysis.claims.map((claim) => `<li><span>${escapeHtml(claim.kind)}</span>${escapeHtml(claim.statement)}</li>`).join("");
  const proofThresholds = [
    "One approved frozen witness defines the behavior under review.",
    "Stability requires independently recorded isolated executions.",
    "A candidate needs counterfactual validation in both directions.",
    "Cached replay remains non-evidentiary.",
    "A deterministic fixture is not a claim about an external repository."
  ].map((threshold) => `<li>${escapeHtml(threshold)}</li>`).join("");

  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'">
<title>FaultLine - deterministic judge preview</title>
<style>
:root{color-scheme:dark;--ink:#e9f4f4;--muted:#9bb2b5;--canvas:#071116;--panel:#0e2028;--line:#25414a;--cyan:#63ded8;--lime:#b4ef7b;--rose:#ff8da7;--amber:#ffc675}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 85% 0,#163947 0,transparent 34%),var(--canvas);color:var(--ink);font:16px/1.55 Inter,ui-sans-serif,system-ui,sans-serif}.shell{max-width:1080px;margin:auto;padding:34px 22px 72px}.eyebrow{color:var(--cyan);font:700 12px/1.2 ui-monospace,SFMono-Regular,Consolas,monospace;letter-spacing:.12em}.brand{display:flex;justify-content:space-between;align-items:center;gap:18px;border-bottom:1px solid var(--line);padding-bottom:22px}.brand strong{font-size:23px;letter-spacing:-.04em}.tag{border:1px solid #3d7a7b;border-radius:999px;color:var(--cyan);padding:5px 9px;font:700 11px/1 ui-monospace,SFMono-Regular,Consolas,monospace}.notice{margin:25px 0;border:1px solid #765d32;background:#2a2114;color:#ffe1a8;padding:17px 19px;border-radius:13px}.notice strong{display:block;letter-spacing:.05em}.notice p{margin:7px 0 0}.hero{display:grid;grid-template-columns:1.35fr .8fr;gap:20px;padding:16px 0 28px}h1{font-size:clamp(36px,6vw,68px);line-height:.96;letter-spacing:-.065em;margin:14px 0}h1 em{color:var(--cyan);font-style:normal}.lede{color:var(--muted);max-width:690px}.card{background:linear-gradient(145deg,rgba(18,42,52,.97),rgba(10,25,32,.97));border:1px solid var(--line);border-radius:16px;padding:20px;box-shadow:0 18px 44px rgba(0,0,0,.18)}.metrics{display:grid;grid-template-columns:repeat(2,1fr);gap:0;padding:0;overflow:hidden}.metric{padding:17px;border-bottom:1px solid var(--line)}.metric:nth-child(odd){border-right:1px solid var(--line)}.metric:nth-last-child(-n+2){border-bottom:0}.metric b{display:block;font-size:25px;letter-spacing:-.05em}.metric small{color:var(--muted)}.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px;margin-top:20px}.wide{grid-column:1/-1}h2{margin:0 0 9px;font-size:19px;letter-spacing:-.03em}p{margin:0}.muted{color:var(--muted)}.witness{margin:14px 0 10px;border-left:3px solid var(--cyan);padding-left:14px}.mono{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;word-break:break-all;color:var(--cyan);font-size:12px}.boundary{display:flex;align-items:center;gap:12px;margin:16px 0;padding:15px;border:1px solid #2d6364;background:#0d2b31;border-radius:11px}.arrow{color:var(--cyan);font-size:24px}table{width:100%;border-collapse:collapse;margin-top:12px;font-size:14px}th,td{text-align:left;padding:10px 0;border-top:1px solid var(--line)}th{color:var(--muted);font-size:11px;letter-spacing:.08em;text-transform:uppercase}.verdict{display:inline-block;border-radius:999px;padding:3px 7px;font:800 11px/1 ui-monospace,SFMono-Regular,Consolas,monospace}.verdict.pass{background:#213b25;color:var(--lime)}.verdict.fail{background:#44212c;color:var(--rose)}.verdict.error{background:#49391f;color:var(--amber)}ul{margin:12px 0 0;padding-left:20px}.claims{list-style:none;padding:0}.claims li{display:grid;grid-template-columns:92px 1fr;gap:10px;border-top:1px solid var(--line);padding:11px 0}.claims span{color:var(--cyan);font:700 11px/1.35 ui-monospace,SFMono-Regular,Consolas,monospace}.footer{border-top:1px solid var(--line);margin-top:30px;padding-top:16px;color:var(--muted);font-size:13px}@media(max-width:760px){.brand,.hero,.grid{grid-template-columns:1fr}.brand{align-items:flex-start;flex-direction:column}.wide{grid-column:auto}.claims li{grid-template-columns:1fr;gap:3px}}
</style>
</head>
<body>
<main class="shell">
  <header class="brand"><div><div class="eyebrow">FAULTLINE</div><strong>Deterministic judge preview</strong></div><span class="tag">STATIC / READ-ONLY</span></header>
  <aside class="notice" aria-label="Preview limitation"><strong>NOT A LIVE DOCKER PROOF</strong><p>This self-contained page is a read-only snapshot of FaultLine's built-in replay fixture. It did not run Docker, execute repository code, call an API, or verify a live repository. It makes no certified regression, provenance, or prevention claim.</p></aside>
  <section class="hero"><div><div class="eyebrow">EXECUTABLE-EVIDENCE PRODUCT WALKTHROUGH</div><h1>Freeze the question.<br><em>Inspect the boundary.</em></h1><p class="lede">FaultLine turns one reviewed behavioral witness into a bounded investigation. This preview shows the product contract and its honesty boundary without presenting fixture statistics as production evidence.</p></div><aside class="card metrics" aria-label="FaultLine product guarantees"><div class="metric"><b>HUMAN</b><small>approval + freeze</small></div><div class="metric"><b>LOCAL</b><small>no auto-range</small></div><div class="metric"><b>3&times;</b><small>runs per Git state</small></div><div class="metric"><b>DIGEST</b><small>retained proof root</small></div></aside></section>
  <section class="grid">
    <article class="card"><div class="eyebrow">01 / APPROVE</div><h2>One frozen witness</h2><p class="witness"><strong>${escapeHtml(analysis.witness.behavior)}</strong></p><p class="mono">${escapeHtml(analysis.witness.digest)}</p><p class="muted">The fixture models a witness approved before localization, with networking disabled and credentials redacted.</p></article>
    <article class="card"><div class="eyebrow">02 / LOCALIZE</div><h2>Sample boundary</h2><div class="boundary"><span>PASS</span><span class="arrow" aria-hidden="true">&rarr;</span><span>FAIL</span></div><p><strong>${escapeHtml(boundaryCopy)}</strong></p><p class="muted">Cached fixture data only — not commit-path <code>COMMIT_PROOF</code>, and not even experimental turn-path evidence. The preview deliberately does not label the boundary stable or proved.</p></article>
    <article class="card wide"><div class="eyebrow">03 / REVIEW</div><h2>Three-state sample</h2><table><thead><tr><th>State</th><th>Fixture verdict</th><th>Reason</th></tr></thead><tbody>${stateRows}</tbody></table><p class="muted">The values are replayed sample outcomes, not a record of a fresh execution.</p></article>
    <article class="card"><div class="eyebrow">04 / EVIDENCE BOUNDARY</div><h2>What the fixture does and does not say</h2><ul class="claims">${claimRows}</ul></article>
    <article class="card"><div class="eyebrow">05 / PROOF THRESHOLD</div><h2>Why this stays a preview</h2><p class="muted">A real FaultLine <code>COMMIT_PROOF</code> requires an approved frozen witness, isolated recorded executions across immutable Git commit states, and an independently verifiable Git proof package. Turn localization is a separate experimental evidence tier. This page contains none of those live artifacts.</p><ul>${proofThresholds}</ul></article>
  </section>
  <footer class="footer">FaultLine static preview schema: faultline.judge-preview.v1. No code executes when this file is opened.</footer>
</main>
</body>
</html>
`;
}

function ensureRealDirectoryTree(directory: string): void {
  const absolute = resolve(directory);
  const parsed = parse(absolute);
  const parts = relative(parsed.root, absolute).split(/[\\/]+/).filter(Boolean);
  let current = parsed.root;
  if (existsSync(current)) {
    const safe = resolveSafeDirectorySegment(current);
    if (safe === null) throw new Error(`Judge preview output must use a real directory: ${current}`);
    current = safe;
  }
  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    const safe = resolveSafeDirectorySegment(current);
    if (safe === null) throw new Error(`Judge preview output cannot traverse a symbolic link or non-directory: ${current}`);
    current = safe;
  }
}

function safePreviewOutput(outputFile: string): string {
  const output = resolve(outputFile);
  if (extname(output).toLowerCase() !== ".html") {
    throw new Error("Judge preview output must be an .html file.");
  }
  ensureRealDirectoryTree(dirname(output));
  if (existsSync(output)) {
    const stat = lstatSync(output);
    if (!stat.isFile() || stat.isSymbolicLink()) {
      throw new Error(`Judge preview output must be a regular non-symlink file: ${output}`);
    }
  }
  return output;
}

export function defaultJudgePreviewPath(workspace = process.cwd()): string {
  return resolve(workspace, "docs", previewFileName);
}

/** Write an intentionally deterministic, self-contained, static review page. */
export function writeJudgePreview(outputFile = defaultJudgePreviewPath()): JudgePreviewWriteResult {
  const output = safePreviewOutput(outputFile);
  const page = renderJudgePreviewPage();
  writeFileSync(output, page, { encoding: "utf8", mode: 0o644 });
  return { path: output, bytes: Buffer.byteLength(page, "utf8") };
}
