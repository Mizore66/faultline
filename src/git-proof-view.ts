import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { canonicalJson } from "./canonical.js";
import {
  COMMIT_PATH_EVIDENCE_GRADE,
  TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL,
  TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL
} from "./evidence-grade.js";
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
  loadVerifiedIncidentAttachments,
  type IncidentAttachmentOptions,
  type VerifiedIncidentAttachments
} from "./incident-attachments.js";
import {
  escapeHtml,
  renderIncidentPageDocumentEnd,
  renderIncidentPageDocumentStart,
  renderIncidentPageProductChrome
} from "./incident-page.js";
import { commitProofPackageIdentityNotice } from "./proof-roots.js";
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
  readonly attachments: VerifiedIncidentAttachments;
};

function readJson(path: string, label: string): unknown {
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    throw new Error(`Unable to read verified ${label}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

type ProofPresentationArtifacts = {
  readonly investigation: GitInvestigationResult;
  readonly frozenWitness: FrozenWitness;
};

/**
 * Read only the two proof artifacts whose parsed values reach the renderer.
 * Callers pair this with a verifier pass after the read so that a mutable
 * directory cannot substitute an unrelated schema-valid JSON document.
 */
function readProofPresentationArtifacts(root: string, manifest: GitProofBundleManifest): ProofPresentationArtifacts {
  return {
    investigation: GitInvestigationResultSchema.parse(
      readJson(join(root, manifest.artifacts.investigation), "investigation artifact")
    ),
    frozenWitness: FrozenWitnessSchema.parse(
      readJson(join(root, manifest.artifacts.frozenWitness), "frozen witness artifact")
    )
  };
}

function sameProofPresentationArtifacts(left: ProofPresentationArtifacts, right: ProofPresentationArtifacts): boolean {
  return canonicalJson(left.investigation) === canonicalJson(right.investigation)
    && canonicalJson(left.frozenWitness) === canonicalJson(right.frozenWitness);
}

/**
 * Verify first, then construct a presentation-only projection. Requiring a
 * valid result is important: this view must never make an invalid bundle look
 * authoritative merely because a few JSON fields happen to render.
 */
export function loadVerifiedGitProofView(
  directory: string,
  expectedRoot?: string,
  attachmentOptions: IncidentAttachmentOptions = {}
): VerifiedGitProofView {
  const root = resolve(directory);
  const verification = verifyGitInvestigationProofBundle(root, expectedRoot);
  if (!verification.valid || !verification.manifest || !verification.rootDigest) {
    const detail = verification.errors.length > 0 ? verification.errors.join("; ") : "no valid root digest was produced";
    throw new Error(`Refusing to render an invalid Git proof bundle: ${detail}`);
  }

  // The verifier has checked that these manifest paths are safe, regular
  // files and canonical. Read twice around verifier passes and retain only
  // matching parsed snapshots. This is deliberately stronger than a plain
  // verify-then-read sequence: a schema-valid transient replacement is not a
  // verified proof artifact.
  const initialArtifacts = readProofPresentationArtifacts(root, verification.manifest);
  const recheck = verifyGitInvestigationProofBundle(root, verification.rootDigest);
  if (!recheck.valid || !recheck.manifest || recheck.rootDigest !== verification.rootDigest) {
    const detail = recheck.errors.length > 0 ? recheck.errors.join("; ") : "the bundle changed while its artifacts were being read";
    throw new Error(`Refusing to render a Git proof bundle that changed during verification: ${detail}`);
  }
  const recheckedArtifacts = readProofPresentationArtifacts(root, recheck.manifest);
  const finalCheck = verifyGitInvestigationProofBundle(root, verification.rootDigest);
  if (!finalCheck.valid || !finalCheck.manifest || finalCheck.rootDigest !== verification.rootDigest
    || canonicalJson(verification.manifest) !== canonicalJson(recheck.manifest)
    || canonicalJson(verification.manifest) !== canonicalJson(finalCheck.manifest)
    || !sameProofPresentationArtifacts(initialArtifacts, recheckedArtifacts)) {
    const detail = finalCheck.errors.length > 0 ? finalCheck.errors.join("; ") : "the bundle presentation artifacts changed during verification";
    throw new Error(`Refusing to render a Git proof bundle that changed during verification: ${detail}`);
  }

  const attachments = loadVerifiedIncidentAttachments(
    attachmentOptions,
    recheckedArtifacts.investigation,
    recheckedArtifacts.frozenWitness,
    verification.rootDigest
  );

  return {
    rootDigest: verification.rootDigest,
    checkedFiles: finalCheck.checkedFiles,
    externalRootStatus: verification.externalRootStatus,
    manifest: finalCheck.manifest,
    investigation: recheckedArtifacts.investigation,
    frozenWitness: recheckedArtifacts.frozenWitness,
    attachments
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
      <td><span class="policy-text" title="${escapeHtml(runtimeEvidence)}">${escapeHtml(runtimeEvidence)}</span></td>
      <td>${coveredRuns} runs${truncatedRuns === 0 ? "" : `; ${truncatedRuns} output-truncated`}</td>
    </tr>`;
  }).join("");
}

function lifecyclePanel(view: VerifiedGitProofView): string {
  const lifecycle = view.manifest.lifecycle;
  if (lifecycle.status === "UNBOUND") {
    return `<article class="card panel"><h3>Lifecycle binding</h3><p class="empty-state"><strong>UNBOUND</strong><br>${escapeHtml(lifecycle.limitation)}</p><small>FaultLine does not infer private model reasoning or native Codex interception from an unbound package.</small></article>`;
  }
  const coveredStates = new Set(lifecycle.checkpointBindings.map((binding) => binding.stateIndex)).size;
  const coverage = `${coveredStates} of ${view.investigation.states.length} investigated Git states`;
  const bindingStatus = lifecycle.status === "FULLY_BOUND"
    ? "FULLY BOUND"
    : lifecycle.status === "PARTIALLY_BOUND"
      ? "PARTIALLY BOUND"
      : "LEGACY BOUND";
  const bindingScope = lifecycle.status === "FULLY_BOUND"
    ? `Every investigated state has an observed checkpoint (${coverage}).`
    : lifecycle.status === "PARTIALLY_BOUND"
      ? `Only ${coverage} have observed checkpoints; the remaining replayed states are not lifecycle-bound.`
      : `This package predates explicit lifecycle coverage labels. It lists ${coverage}, but its old BOUND label does not assert complete coverage.`;
  const rows = lifecycle.checkpointBindings.map((binding) => `<tr>
    <td>${binding.stateIndex}</td>
    <td>${binding.sequence}</td>
    <td><code title="${escapeHtml(binding.checkpointDigest)}">${escapeHtml(shortDigest(binding.checkpointDigest))}</code></td>
  </tr>`).join("");
  return `<article class="card panel"><h3>Lifecycle binding</h3>
    <p><strong>${bindingStatus}</strong> via observed ${escapeHtml(lifecycle.transport.replaceAll("_", " "))} events. ${escapeHtml(bindingScope)} This records checkpoints, not private model reasoning or native interception.</p>
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

function minimizationPanel(view: VerifiedGitProofView): string {
  const attachment = view.attachments.minimization;
  if (attachment === null) {
    return `<article class="card panel"><h3>Counterfactual minimization</h3><p class="empty-state"><strong>Not attached</strong><br>No minimization record was supplied to this read-only view.</p><small>A stable Git boundary does not by itself establish a 1-minimal set, agent intent, or a unique semantic root cause.</small></article>`;
  }
  const result = attachment.result;
  const proof = result.proof.isProof ? "BIDIRECTIONALLY CERTIFIED" : "NOT CERTIFIED AS COUNTERFACTUAL PROOF";
  const summary = result.proof.isProof
    ? "The attached record certifies both counterfactual directions for its selected units."
    : "The attached record did not establish both counterfactual directions.";
  return `<article class="card panel"><h3>Counterfactual minimization</h3>
    <p><strong>${escapeHtml(proof)}</strong><br><small>${escapeHtml(summary)}</small></p>
    <div class="proof"><span>Candidate units</span><strong>${result.candidateUnitIds.length}</strong></div>
    <div class="proof"><span>1-minimal</span><small>${result.minimality.oneMinimal ? "recorded" : "not established"}</small></div>
    <div class="proof"><span>Sufficiency</span><small>${escapeHtml(result.certification.sufficiency.status.replaceAll("_", " "))}</small></div>
    <div class="proof"><span>Necessity</span><small>${escapeHtml(result.certification.necessity.status.replaceAll("_", " "))}</small></div>
    <div class="proof"><span>Attachment digest</span><code title="${escapeHtml(attachment.resultDigest)}">${escapeHtml(shortDigest(attachment.resultDigest))}</code></div>
    <small>Separately verified record; external digest: ${escapeHtml(attachment.externalDigestStatus)}. File paths, patch bytes, raw output, and free-form diagnostic text are intentionally omitted.</small>
  </article>`;
}

function citationSummary(items: readonly { evidenceIds: readonly string[] }[]): string {
  const citations = [...new Set(items.flatMap((item) => item.evidenceIds))].sort();
  return `<div class="proof"><span>Validated citations</span><small>[${escapeHtml(citations.join(", "))}]</small></div>`;
}

function repairPanel(view: VerifiedGitProofView): string {
  const attachment = view.attachments.repair;
  if (attachment === null) {
    return `<article class="card panel"><h3>Evidence-bounded repair packet</h3><p>FaultLine can hand a repair workflow the frozen witness, verified boundary, and evidence digests without choosing a model culprit.</p><p class="empty-state"><strong>Repair not attached</strong><br>No repair artifact was supplied to this view.</p><small>No repair claim is rendered from a boundary-only proof package.</small></article>`;
  }
  const brief = attachment.brief;
  return `<article class="card panel"><h3>Evidence-bounded repair guidance</h3>
    <p><span class="pill">INFERRED</span> Citation-validated guidance from a separately verified repair artifact; it is not an executed repair or proof of model intent.</p>
    <div class="proof"><span>Cited invariant</span><small>${brief.proposedInvariant.evidenceIds.length} evidence reference${brief.proposedInvariant.evidenceIds.length === 1 ? "" : "s"}</small></div>
    <div class="proof"><span>Cited repair directions</span><strong>${brief.repairDirections.length}</strong></div>
    ${citationSummary([brief.proposedInvariant, ...brief.repairDirections])}
    <small>Artifact ${escapeHtml(shortDigest(attachment.manifest.manifestDigest))} is bound to this proof's investigation and frozen-witness digests; external digest: ${escapeHtml(attachment.externalDigestStatus)}. Free-form guidance remains in the private repair artifact and is deliberately not rendered in this shareable proof view.</small>
  </article>`;
}

function preventionGuidancePanel(view: VerifiedGitProofView): string {
  const verifiedPrevention = view.attachments.prevention;
  if (verifiedPrevention !== null) {
    const body = verifiedPrevention.prevention;
    const verified = verifiedPrevention.manifest.classification === "PREVENTION_VERIFIED";
    return `<article class="card panel"><h3>${verified ? "Prevention verified" : "Prevention evidence summary"}</h3>
      <p><span class="pill">${verified ? "Prevention verified" : "PREVENTION_EVIDENCE_SUMMARY"}</span> ${
        verified
          ? "Grounded three-state package: last-good / first-bad run IDs from this proof root plus repaired-state NATIVE_DOCKER bindings under the same frozen witness."
          : "Offline-checked three-state package bound to this proof root and frozen witness. Prefer a grounded package from <code>fl prevention write --from-bundle</code>."
      }</p>
      <div class="proof"><span>Last good</span><code title="${escapeHtml(body.lastGood.commit)}">${escapeHtml(shortCommit(body.lastGood.commit))}</code> ${verdictBadge(body.lastGood.verdict)}</div>
      <div class="proof"><span>First bad</span><code title="${escapeHtml(body.firstBad.commit)}">${escapeHtml(shortCommit(body.firstBad.commit))}</code> ${verdictBadge(body.firstBad.verdict)}</div>
      <div class="proof"><span>Repaired</span><code title="${escapeHtml(body.repaired.commit)}">${escapeHtml(shortCommit(body.repaired.commit))}</code> ${verdictBadge(body.repaired.verdict)}</div>
      <div class="proof"><span>Prevention root</span><code title="${escapeHtml(verifiedPrevention.rootDigest)}">${escapeHtml(shortDigest(verifiedPrevention.rootDigest))}</code></div>
      <small>Claims only PASS→FAIL→PASS under the same frozen witness in NATIVE_DOCKER. Does not claim model intent or a unique semantic cause.</small>
    </article>`;
  }
  const attachment = view.attachments.repair;
  if (attachment === null) {
    return `<article class="card panel"><h3>Prevention guidance</h3><p class="empty-state"><strong>Not attached</strong><br>No evidence-cited prevention guidance or prevention-proof package was supplied.</p><small>Recovery evidence and a stable boundary do not establish prevention. Attach a grounded <code>faultline.prevention-proof.v1</code> package via <code>fl prevention write --from-bundle</code>.</small></article>`;
  }
  const prevention = attachment.brief.prevention;
  const items = [...prevention.hardEnforcement, ...prevention.softGuidance];
  return `<article class="card panel"><h3>Prevention guidance</h3>
    <p><span class="pill">INFERRED</span> These are cited recommendations, not a three-state prevention proof.</p>
    <div class="proof"><span>Hard enforcement directions</span><strong>${prevention.hardEnforcement.length}</strong></div>
    <div class="proof"><span>Soft guidance directions</span><strong>${prevention.softGuidance.length}</strong></div>
    ${citationSummary(items)}
    <small>Free-form prevention text remains in the private repair artifact. A repaired state must still be executed under the same frozen witness and packaged via <code>fl prevention write --from-bundle</code> for Prevention verified.</small>
  </article>`;
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
  const evidenceGrade = investigation.proof.evidenceGrade;
  const evidenceLabel = investigation.proof.evidenceLabel;
  const attachmentSummary = view.attachments.minimization === null && view.attachments.repair === null
    ? "It distinguishes an observed boundary from downstream artifacts that were not supplied."
    : "It separately verifies each supplied downstream artifact and its applicable evidence binding before rendering it.";

  const ideaBoundary = firstRegression === undefined
    ? "No stable PASS→FAIL transition is recorded in this package."
    : `First stable PASS→FAIL at ${shortCommit(firstRegression.after.commit)} under a human-frozen witness; package root ${shortDigest(view.rootDigest)}.`;
  const packageBanner = commitProofPackageIdentityNotice(view.rootDigest);
  return `${renderIncidentPageDocumentStart("verified Git proof")}
${renderIncidentPageProductChrome({
  command: "fl serve --bundle / fl judge-proof",
  ...(packageBanner === undefined ? {} : { packageBanner }),
  ideaBeat: "Portable offline-verifiable evidence for one frozen predicate — another engineer verifies without re-running repository code.",
  notice: "Read-only verified bundle: FaultLine checked its complete declared file set before rendering. This page does not execute repository code or rerun the witness."
})}
<section class="hero"><div><div class="eyebrow">FAULTLINE · COMMIT_PROOF</div><h1>Where this frozen<br>predicate <em>first failed.</em></h1><p class="lede">${escapeHtml(ideaBoundary)} Grade <code>${escapeHtml(evidenceGrade)}</code> (${escapeHtml(evidenceLabel)}). When certified: <code>${COMMIT_PATH_EVIDENCE_GRADE}</code>. Not turn-path (<code>${TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL}</code>). ${escapeHtml(attachmentSummary)}</p><span class="readonly-badge">READ-ONLY REVIEW</span></div><aside class="card metric-card" aria-label="Verified proof summary"><div class="metric"><b>${view.checkedFiles}</b><small>declared files checked</small></div><div class="metric"><b>${investigation.runs.length}</b><small>recorded executions</small></div><div class="metric"><b>${investigation.states.length}</b><small>Git states</small></div><div class="metric"><b>${investigation.transitions.length}</b><small>stable transitions</small></div></aside></section>
<section class="section" id="break"><div class="section-head"><span class="number">01</span><h2>BREAK → FIND</h2></div><div class="grid"><article class="card panel witness"><h3>Frozen witness</h3><p><strong>Human-reviewed executable predicate</strong></p><span class="pill">human-approved before freeze</span><span class="pill">network disabled</span><span class="pill">read-only review</span><p class="hash">${escapeHtml(frozenWitness.witnessDigest)}</p><small>Behavior text, command text, overlay bytes, incident-packet contents, and reviewer identity are intentionally omitted from this shareable view.</small></article><article class="card panel"><h3>Package verification</h3><p><strong>${escapeHtml(proofStatus)}</strong></p><div class="proof"><span>Evidence grade</span><code>${escapeHtml(evidenceGrade)}</code></div><div class="proof"><span>Evidence label</span><small>${escapeHtml(evidenceLabel)}</small></div><div class="proof proof-stack"><span>Turn-path contrast</span><small><code>${TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL}</code> — ${escapeHtml(TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL)} (not this page)</small></div><div class="proof"><span>Bundle root</span><code title="${escapeHtml(view.rootDigest)}">${escapeHtml(shortDigest(view.rootDigest))}</code></div><div class="proof"><span>External root</span><small>${escapeHtml(externalRootCopy(view.externalRootStatus))}</small></div><div class="proof"><span>Human approval</span><small>${escapeHtml(approval.approvedAt)} recorded before freeze</small></div></article></div></section>
<section class="section" id="find"><div class="section-head"><span class="number">02</span><h2>FIND</h2></div><div class="grid"><article class="card panel"><h3>Stable Git states</h3><div class="table-wrap"><table class="data-table"><thead><tr><th>Index</th><th>Commit</th><th>Verdict</th><th>Evidence</th></tr></thead><tbody>${stableStateRows(view)}</tbody></table></div><small>${escapeHtml(stableStateSummary)}</small></article><article class="card panel"><h3>Stable transitions</h3><p>Three reruns on either side prevent a single noisy result from becoming attribution.</p><div class="table-wrap"><table class="data-table transitions-table"><thead><tr><th>Transition</th><th>Before</th><th></th><th>After</th><th>Evidence</th></tr></thead><tbody>${transitionRows(investigation.transitions)}</tbody></table></div><p><strong>${firstRegression === undefined ? "No stable pass-to-fail transition" : "First stable pass-to-fail transition"}</strong></p><small>${firstRegression === undefined ? "FaultLine refuses to name a first bad state without a stable bracket." : `${shortCommit(firstRegression.before.commit)} -> ${shortCommit(firstRegression.after.commit)}`}</small></article></div></section>
<section class="section" id="prove"><div class="section-head"><span class="number">03</span><h2>PROVE</h2></div><div class="grid"><article class="card panel"><h3>Sandbox evidence</h3><p>Recorded execution trust: <strong>${escapeHtml(investigation.proof.executionTrust.replaceAll("_", " "))}</strong>.</p><div class="table-wrap"><table class="data-table sandbox-table"><thead><tr><th>Kind</th><th>Executor</th><th>Policy</th><th>Runtime constraints</th><th>Coverage</th></tr></thead><tbody>${sandboxRows(view)}</tbody></table></div><small>Policy and command values are represented by digests; raw output and overlay bytes are intentionally omitted.</small></article>${minimizationPanel(view)}</div></section>
<section class="section" id="fix"><div class="section-head"><span class="number">04</span><h2>FIX</h2></div><div class="grid">${repairPanel(view)}${lifecyclePanel(view)}</div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">05</span><h2>PREVENT</h2></div><div class="grid">${recoveryPanel(investigation.transitions)}${preventionGuidancePanel(view)}<article class="card panel"><h3>Portable proof bundle</h3><div class="proof"><span>Integrity scope</span><small>${escapeHtml(manifest.integrityScope)}</small></div><div class="proof"><span>Bundle root</span><code title="${escapeHtml(view.rootDigest)}">${escapeHtml(shortDigest(view.rootDigest))}</code></div><div class="proof-command"><span>Verified command</span><pre class="command-snippet"><code>fl verify &lt;bundle&gt; --expect-root ${escapeHtml(view.rootDigest)}</code></pre></div><details><summary>Integrity boundary</summary><pre>Verifies the complete declared file set against an externally retained root. Separately supplied attachments are re-verified and linked before rendering, but are not retroactively included in this Git proof root.</pre></details></article></div></section>
${renderIncidentPageDocumentEnd({
  footer: "FaultLine reports verified predicate-specific evidence, states when minimization or repair artifacts are absent, and does not claim private model reasoning, a unique semantic cause, or native Codex interception."
})}`;
}
