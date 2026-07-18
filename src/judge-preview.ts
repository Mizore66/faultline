import { existsSync, lstatSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, extname, join, parse, relative, resolve } from "node:path";
import { createDemoAnalysis } from "./engine.js";
import { fixtureStateById } from "./fixture.js";
import {
  escapeHtml,
  renderIncidentPageDocumentEnd,
  renderIncidentPageDocumentStart,
  renderIncidentPageProductChrome
} from "./incident-page.js";
import { resolveSafeDirectorySegment } from "./safe-directory.js";
import type { DemoAnalysis, RunRecord } from "./domain.js";

const previewFileName = "judge-preview.html";

export type JudgePreviewWriteResult = {
  path: string;
  bytes: number;
};

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
 * verified Git/Docker proof package. Uses the same light paper chrome as
 * live incident / COMMIT_PROOF pages.
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
  const claimRows = analysis.claims.map((claim) => `<div class="evidence-row"><span>${escapeHtml(claim.kind)}</span><p>${escapeHtml(claim.statement)}</p></div>`).join("");
  const proofThresholds = [
    "One approved frozen witness defines the behavior under review.",
    "Stability requires independently recorded isolated executions.",
    "A candidate needs counterfactual validation in both directions.",
    "Cached replay remains non-evidentiary.",
    "A deterministic fixture is not a claim about an external repository."
  ].map((threshold) => `<li>${escapeHtml(threshold)}</li>`).join("");

  return `${renderIncidentPageDocumentStart("deterministic judge preview")}
${renderIncidentPageProductChrome({
  command: "fl judge-preview",
  ideaBeat: "Fixture sandbox only — not COMMIT_PROOF. Product Idea: pnpm fl judge-proof (sample root sha256:f85c446d…) or docs/self-incident-proof-preview.html.",
  notice: "NOT A LIVE DOCKER PROOF. This self-contained page is a read-only snapshot of FaultLine's built-in replay fixture. It did not run Docker, execute repository code, call an API, or verify a live repository. It makes no certified regression, provenance, or prevention claim."
})}
<section class="hero" id="break"><div><div class="eyebrow">FAULTLINE · FIXTURE SANDBOX</div><h1>Freeze the question.<br><em>Inspect the boundary.</em></h1><p class="lede">FaultLine turns one reviewed behavioral witness into a bounded investigation. This preview shows the product contract and its honesty boundary without presenting fixture statistics as production evidence.</p><span class="readonly-badge">STATIC / READ-ONLY</span></div><aside class="card metric-card" aria-label="FaultLine product guarantees"><div class="metric"><b>HUMAN</b><small>approval + freeze</small></div><div class="metric"><b>LOCAL</b><small>no auto-range</small></div><div class="metric"><b>3×</b><small>runs per Git state</small></div><div class="metric"><b>DIGEST</b><small>retained proof root</small></div></aside></section>
<section class="section" id="find"><div class="section-head"><span class="number">01</span><h2>APPROVE</h2></div><div class="grid"><article class="card panel witness"><h3>One frozen witness</h3><p><strong>${escapeHtml(analysis.witness.behavior)}</strong></p><p class="hash">${escapeHtml(analysis.witness.digest)}</p><small>The fixture models a witness approved before localization, with networking disabled and credentials redacted.</small></article><article class="card panel"><h3>Sample boundary</h3><div class="transition"><span>PASS</span><span class="arrow" aria-hidden="true">→</span><span>FAIL</span></div><p><strong>${escapeHtml(boundaryCopy)}</strong></p><small>Cached fixture data only — not commit-path COMMIT_PROOF, and not experimental turn-path evidence.</small></article></div></section>
<section class="section" id="prove"><div class="section-head"><span class="number">02</span><h2>REVIEW</h2></div><article class="card panel"><h3>Three-state sample</h3><div class="table-wrap"><table class="data-table"><thead><tr><th>State</th><th>Fixture verdict</th><th>Reason</th></tr></thead><tbody>${stateRows}</tbody></table></div><small>The values are replayed sample outcomes, not a record of a fresh execution.</small></article></section>
<section class="section" id="fix"><div class="section-head"><span class="number">03</span><h2>EVIDENCE BOUNDARY</h2></div><div class="grid"><article class="card panel"><h3>What the fixture does and does not say</h3>${claimRows}</article><article class="card panel"><h3>Why this stays a preview</h3><p>A real FaultLine <code>COMMIT_PROOF</code> requires an approved frozen witness, isolated recorded executions across immutable Git commit states, and an independently verifiable Git proof package. This page contains none of those live artifacts.</p><ul>${proofThresholds}</ul></article></div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">04</span><h2>NEXT</h2></div><article class="card panel"><h3>Open the product Idea path</h3><p>Run <code>pnpm fl judge-proof</code> (installed live-git sample root <code>sha256:f85c446d…</code>) or open <code>docs/self-incident-proof-preview.html</code>. Historical self-incident root <code>sha256:f6a391b3…</code> is a separate dogfood package — see <code>docs/samples/COMMIT_PROOF_SAMPLE.md</code>.</p></article></section>
${renderIncidentPageDocumentEnd({
  footer: "FaultLine static preview schema: faultline.judge-preview.v1. No code executes when this file is opened."
})}`;
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
