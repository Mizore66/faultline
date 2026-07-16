import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import {
  GitInvestigationResultSchema,
  type GitInvestigationResult,
  type StableGitTransition
} from "./git-investigation.js";
import {
  verifyGitInvestigationProofBundle,
  type GitProofBundleManifest,
  type GitProofBundleExternalRootStatus
} from "./git-proof-bundle.js";
import {
  escapeHtml,
  renderIncidentPageDocumentEnd,
  renderIncidentPageDocumentStart,
  renderIncidentPageProductChrome
} from "./incident-page.js";
import { FrozenWitnessSchema, type FrozenWitness } from "./witness-lock.js";

/**
 * A deliberately small, read-only projection of a portable Git proof bundle.
 *
 * The loader verifies the complete package before it reads the JSON artifacts
 * used by the page. The renderer never reads a repository, invokes a witness,
 * or exposes raw command output / overlay bytes.
 */
export type VerifiedGitProofView = {
  readonly rootDigest: string;
  readonly checkedFiles: number;
  readonly externalRootStatus: GitProofBundleExternalRootStatus;
  readonly manifest: GitProofBundleManifest;
  readonly investigation: GitInvestigationResult;
  readonly frozenWitness: FrozenWitness;
};

function readJson(path: string, label: string): unknown {
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    throw new Error(`Unable to read verified ${label}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

/**
 * Verify first, then construct a presentation-only projection. Requiring a
 * valid result is important: this view must never make an invalid bundle look
 * authoritative merely because a few JSON fields happen to render.
 */
export function loadVerifiedGitProofView(directory: string, expectedRoot?: string): VerifiedGitProofView {
  const root = resolve(directory);
  const verification = verifyGitInvestigationProofBundle(root, expectedRoot);
  if (!verification.valid || !verification.manifest || !verification.rootDigest) {
    const detail = verification.errors.length > 0 ? verification.errors.join("; ") : "no valid root digest was produced";
    throw new Error(`Refusing to render an invalid Git proof bundle: ${detail}`);
  }

  // The verifier has already checked that these manifest paths are safe,
  // regular files and that their canonical contents match the manifest.
  const investigation = GitInvestigationResultSchema.parse(
    readJson(join(root, verification.manifest.artifacts.investigation), "investigation artifact")
  );
  const frozenWitness = FrozenWitnessSchema.parse(
    readJson(join(root, verification.manifest.artifacts.frozenWitness), "frozen witness artifact")
  );

  // Detect a package changed while its presentation artifacts were being
  // loaded. Use the first root as the comparison anchor without changing the
  // user-facing external-root status (an internal recheck is not an external
  // retention claim).
  const recheck = verifyGitInvestigationProofBundle(root, verification.rootDigest);
  if (!recheck.valid || !recheck.manifest || recheck.rootDigest !== verification.rootDigest) {
    const detail = recheck.errors.length > 0 ? recheck.errors.join("; ") : "the bundle changed while its artifacts were being read";
    throw new Error(`Refusing to render a Git proof bundle that changed during verification: ${detail}`);
  }

  return {
    rootDigest: verification.rootDigest,
    checkedFiles: recheck.checkedFiles,
    externalRootStatus: verification.externalRootStatus,
    manifest: recheck.manifest,
    investigation,
    frozenWitness
  };
}

function shortDigest(value: string): string {
  return value.length <= 22 ? value : `${value.slice(0, 15)}...${value.slice(-8)}`;
}

function shortCommit(value: string): string {
  return value.slice(0, 12);
}

function verdictBadge(verdict: string): string {
  return `<span class="verdict ${escapeHtml(verdict.toLowerCase().replaceAll("_", "-"))}">${escapeHtml(verdict)}</span>`;
}

function externalRootCopy(status: GitProofBundleExternalRootStatus): string {
  switch (status) {
    case "MATCH":
      return "MATCH - the supplied external root matches this verified package.";
    case "NOT_PROVIDED":
      return "NOT PROVIDED - the package is self-consistent, but no externally retained root was checked.";
    case "MISMATCH":
      return "MISMATCH - this state is never rendered because the bundle loader rejects it.";
  }
}

function transitionRows(transitions: readonly StableGitTransition[]): string {
  if (transitions.length === 0) {
    return "<tr><td colspan=\"5\">No stable transition was recorded.</td></tr>";
  }
  return transitions.map((transition) => `<tr>
    <td>${escapeHtml(transition.kind.replaceAll("_", " "))}</td>
    <td><code title="${escapeHtml(transition.before.commit)}">${escapeHtml(shortCommit(transition.before.commit))}</code> ${verdictBadge(transition.before.verdict)}</td>
    <td>&rarr;</td>
    <td><code title="${escapeHtml(transition.after.commit)}">${escapeHtml(shortCommit(transition.after.commit))}</code> ${verdictBadge(transition.after.verdict)}</td>
    <td>${transition.before.executionIds.length} + ${transition.after.executionIds.length} distinct Docker executions</td>
  </tr>`).join("");
}

function stableStateRows(view: VerifiedGitProofView): string {
  if (view.investigation.stableStates.length === 0) {
    return "<tr><td colspan=\"4\">No stable state was recorded.</td></tr>";
  }
  return view.investigation.stableStates.map((state) => `<tr>
    <td>${state.stateIndex}</td>
    <td><code title="${escapeHtml(state.commit)}">${escapeHtml(shortCommit(state.commit))}</code></td>
    <td>${verdictBadge(state.verdict)}</td>
    <td>${state.executionIds.length} distinct Docker executions</td>
  </tr>`).join("");
}

function sandboxRows(view: VerifiedGitProofView): string {
  const representatives = new Map<string, GitInvestigationResult["runs"][number]>();
  for (const run of view.investigation.runs) {
    const key = `${run.sandbox.kind}:${run.result.executor}:${run.sandbox.policyDigest}`;
    if (!representatives.has(key)) representatives.set(key, run);
  }
  if (representatives.size === 0) {
    return "<tr><td colspan=\"5\">No sandbox execution facts were recorded.</td></tr>";
  }
  return [...representatives.values()].map((run) => {
    const coveredRuns = view.investigation.runs.filter((candidate) => candidate.sandbox.policyDigest === run.sandbox.policyDigest).length;
    const truncatedRuns = view.investigation.runs.filter((candidate) => candidate.sandbox.policyDigest === run.sandbox.policyDigest && candidate.result.outputTruncated).length;
    const runtime = run.sandbox.runtime;
    const runtimeEvidence = [
      runtime.image === null ? "image not attested" : `image ${runtime.image}`,
      runtime.network === null ? "network not attested" : `network ${runtime.network}`,
      runtime.rootFilesystemReadOnly ? "read-only root" : "writable root",
      runtime.user === null ? "user not attested" : `user ${runtime.user}`,
      runtime.capDropAll ? "all capabilities dropped" : "capability policy incomplete",
      runtime.noNewPrivileges ? "no-new-privileges" : "privilege policy incomplete",
      runtime.pull === null ? "pull policy not attested" : `pull ${runtime.pull}`
    ].join(" | ");
    return `<tr>
      <td>${escapeHtml(run.sandbox.kind.replaceAll("_", " "))}</td>
      <td>${escapeHtml(run.result.executor.replaceAll("_", " "))}</td>
      <td><code title="${escapeHtml(run.sandbox.policyDigest)}">${escapeHtml(shortDigest(run.sandbox.policyDigest))}</code></td>
      <td>${escapeHtml(runtimeEvidence)}</td>
      <td>${coveredRuns} runs${truncatedRuns === 0 ? "" : `; ${truncatedRuns} output-truncated`}</td>
    </tr>`;
  }).join("");
}

function lifecyclePanel(view: VerifiedGitProofView): string {
  const lifecycle = view.manifest.lifecycle;
  if (lifecycle.status === "UNBOUND") {
    return `<article class="card panel"><h3>Lifecycle binding</h3><p class="empty-state"><strong>UNBOUND</strong><br>${escapeHtml(lifecycle.limitation)}</p><small>FaultLine does not infer private model reasoning or native Codex interception from an unbound package.</small></article>`;
  }
  const rows = lifecycle.checkpointBindings.map((binding) => `<tr>
    <td>${binding.stateIndex}</td>
    <td>${binding.sequence}</td>
    <td><code title="${escapeHtml(binding.checkpointDigest)}">${escapeHtml(shortDigest(binding.checkpointDigest))}</code></td>
  </tr>`).join("");
  return `<article class="card panel"><h3>Lifecycle binding</h3>
    <p><strong>BOUND</strong> via observed ${escapeHtml(lifecycle.transport.replaceAll("_", " "))} events. This records checkpoints, not private model reasoning or native interception.</p>
    <div class="proof"><span>Ledger digest</span><code title="${escapeHtml(lifecycle.ledgerDigest)}">${escapeHtml(shortDigest(lifecycle.ledgerDigest))}</code></div>
    <div class="proof"><span>Ledger head</span><code title="${escapeHtml(lifecycle.headHash)}">${escapeHtml(shortDigest(lifecycle.headHash))}</code></div>
    <div class="table-wrap"><table class="data-table"><thead><tr><th>Git state</th><th>Ledger sequence</th><th>Checkpoint digest</th></tr></thead><tbody>${rows}</tbody></table></div>
  </article>`;
}

function recoveryPanel(transitions: readonly StableGitTransition[]): string {
  const recoveries = transitions.filter((transition) => transition.kind === "FAIL_TO_PASS");
  if (recoveries.length === 0) {
    return `<article class="card panel"><h3>Recovery evidence</h3><p class="empty-state"><strong>Not verified</strong><br>No later stable FAIL_TO_PASS transition is attached to this package.</p><small>A verified boundary does not establish a repair or prevention. Attach separate recovery and prevention evidence to make either claim.</small></article>`;
  }
  const rows = recoveries.map((transition) => `<div class="proof"><span><code title="${escapeHtml(transition.before.commit)}">${escapeHtml(shortCommit(transition.before.commit))}</code> ${verdictBadge(transition.before.verdict)}</span><span>&rarr;</span><span><code title="${escapeHtml(transition.after.commit)}">${escapeHtml(shortCommit(transition.after.commit))}</code> ${verdictBadge(transition.after.verdict)}</span></div>`).join("");
  return `<article class="card panel"><h3>Recorded recovery</h3><p><strong>Recorded stable recovery</strong><small>Each displayed state has three distinct Docker executions under the same frozen witness. This establishes recovery only, not prevention.</small></p>${rows}</article>`;
}

/** Render only verified, privacy-minimized evidence from a Git proof package. */
export function renderGitProofIncidentPage(view: VerifiedGitProofView): string {
  const { manifest, investigation, frozenWitness } = view;
  const firstRegression = investigation.transitions.find((transition) => transition.kind === "PASS_TO_FAIL");
  const approval = frozenWitness.approval;
  const executionSummary = `${investigation.runs.length} recorded Docker-isolated executions across ${investigation.states.length} Git states (${investigation.executionsPerState} per state).`;
  const omittedUnstableStateCount = Math.max(0, investigation.states.length - investigation.stableStates.length);
  const stableStateSummary = omittedUnstableStateCount === 0
    ? `${executionSummary} All recorded states reached a stable pass/fail verdict.`
    : `${executionSummary} ${omittedUnstableStateCount} recorded ${omittedUnstableStateCount === 1 ? "state did" : "states did"} not reach a stable pass/fail verdict and ${omittedUnstableStateCount === 1 ? "is" : "are"} omitted from this table.`;
  const proofStatus = investigation.proof.isProof ? "VERIFIED PROOF" : "NOT CERTIFIED AS PROOF";
  const proofDetail = investigation.proof.isProof
    ? "Stable transition evidence is retained for this frozen witness."
    : investigation.proof.reason;

  return `${renderIncidentPageDocumentStart("verified Git proof")}
${renderIncidentPageProductChrome({
  command: "fl serve --bundle",
  notice: "Read-only verified bundle: FaultLine checked its complete declared file set before rendering. This page does not execute repository code or rerun the witness."
})}
<section class="hero" id="break"><div><div class="eyebrow">FAULTLINE - PORTABLE GIT INVESTIGATION</div><h1>Verified <em>evidence</em>,<br>one product path.</h1><p class="lede">The same incident experience that explains the sample now renders a retained Git proof package. It distinguishes an observed boundary from minimization, repair, and prevention artifacts that were not attached.</p><span class="readonly-badge">READ-ONLY REVIEW</span></div><aside class="card metric-card" aria-label="Verified proof summary"><div class="metric"><b>${view.checkedFiles}</b><small>declared files checked</small></div><div class="metric"><b>${investigation.runs.length}</b><small>recorded executions</small></div><div class="metric"><b>${investigation.states.length}</b><small>Git states</small></div><div class="metric"><b>${investigation.transitions.length}</b><small>stable transitions</small></div></aside></section>
<section class="section" id="find"><div class="section-head"><span class="number">01</span><h2>BREAK &rarr; FIND</h2></div><div class="grid"><article class="card panel witness"><h3>Frozen witness</h3><p><strong>${escapeHtml(frozenWitness.proposal.witness.behavior)}</strong></p><span class="pill">human-approved before freeze</span><span class="pill">network disabled</span><span class="pill">read-only review</span><p class="hash">${escapeHtml(frozenWitness.witnessDigest)}</p><small>Command text, overlay bytes, incident-packet contents, and reviewer identity are intentionally omitted from this view.</small></article><article class="card panel"><h3>Package verification</h3><p><strong>${escapeHtml(proofStatus)}</strong></p><div class="proof"><span>Bundle root</span><code title="${escapeHtml(view.rootDigest)}">${escapeHtml(shortDigest(view.rootDigest))}</code></div><div class="proof"><span>External root</span><small>${escapeHtml(externalRootCopy(view.externalRootStatus))}</small></div><div class="proof"><span>Human approval</span><small>${escapeHtml(approval.approvedAt)} recorded before freeze</small></div></article></div></section>
<section class="section"><div class="section-head"><span class="number">02</span><h2>FIND</h2></div><div class="grid"><article class="card panel"><h3>Stable Git states</h3><div class="table-wrap"><table class="data-table"><thead><tr><th>Index</th><th>Commit</th><th>Verdict</th><th>Evidence</th></tr></thead><tbody>${stableStateRows(view)}</tbody></table></div><small>${escapeHtml(stableStateSummary)}</small></article><article class="card panel"><h3>Stable transitions</h3><p>Three reruns on either side prevent a single noisy result from becoming attribution.</p><div class="table-wrap"><table class="data-table"><thead><tr><th>Transition</th><th>Before</th><th></th><th>After</th><th>Evidence</th></tr></thead><tbody>${transitionRows(investigation.transitions)}</tbody></table></div><p><strong>${firstRegression === undefined ? "No stable pass-to-fail transition" : "First stable pass-to-fail transition"}</strong></p><small>${firstRegression === undefined ? "FaultLine refuses to name a first bad state without a stable bracket." : `${shortCommit(firstRegression.before.commit)} -> ${shortCommit(firstRegression.after.commit)}`}</small></article></div></section>
<section class="section" id="prove"><div class="section-head"><span class="number">03</span><h2>PROVE</h2></div><div class="grid"><article class="card panel"><h3>Sandbox evidence</h3><p>Recorded execution trust: <strong>${escapeHtml(investigation.proof.executionTrust.replaceAll("_", " "))}</strong>.</p><div class="table-wrap"><table class="data-table"><thead><tr><th>Kind</th><th>Executor</th><th>Policy</th><th>Runtime constraints</th><th>Coverage</th></tr></thead><tbody>${sandboxRows(view)}</tbody></table></div><small>Policy and command values are represented by digests; raw output and overlay bytes are intentionally omitted.</small></article><article class="card panel"><h3>Counterfactual minimization</h3><p class="empty-state"><strong>Not attached</strong><br>Minimization artifact not attached to this bundle.</p><p><strong>${escapeHtml(proofStatus)}</strong><small>${escapeHtml(proofDetail)}</small></p><small>A stable Git boundary does not by itself establish a 1-minimal set, agent intent, or a unique semantic root cause.</small></article></div></section>
<section class="section"><div class="section-head"><span class="number">04</span><h2>FIX</h2></div><div class="grid"><article class="card panel"><h3>Evidence-bounded repair packet</h3><p>FaultLine can hand a repair workflow the frozen witness, verified boundary, and evidence digests without choosing a model culprit.</p><p class="empty-state"><strong>Repair not verified</strong><br>Repair artifact not attached to this bundle.</p><small>No repair claim is rendered from a boundary-only proof package.</small></article>${lifecyclePanel(view)}</div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">05</span><h2>PREVENT</h2></div><div class="grid">${recoveryPanel(investigation.transitions)}<article class="card panel"><h3>Portable proof bundle</h3><div class="proof"><span>Integrity scope</span><small>${escapeHtml(manifest.integrityScope)}</small></div><div class="proof"><span>Bundle root</span><code title="${escapeHtml(view.rootDigest)}">${escapeHtml(shortDigest(view.rootDigest))}</code></div><div class="proof"><span>Verified command</span><code>fl verify &lt;bundle&gt; --expect-root ${escapeHtml(view.rootDigest)}</code></div><details><summary>Integrity boundary</summary><pre>Verifies the complete declared file set against an externally retained root. It does not execute repository code, reveal raw witness material, or turn inspection into a new run.</pre></details></article></div></section>
${renderIncidentPageDocumentEnd({
  footer: "FaultLine reports verified predicate-specific evidence, states when minimization or repair artifacts are absent, and does not claim private model reasoning, a unique semantic cause, or native Codex interception."
})}`;
}
