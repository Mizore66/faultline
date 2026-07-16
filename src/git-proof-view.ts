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
import { FrozenWitnessSchema, type FrozenWitness } from "./witness-lock.js";

/**
 * A deliberately small, read-only projection of a portable Git proof bundle.
 *
 * The loader verifies the complete package before it reads the JSON artifacts
 * used by the page.  The renderer never reads a repository, invokes a witness,
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
 * Verify first, then construct a presentation-only projection.  Requiring a
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

function escapeHtml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function shortDigest(value: string): string {
  return value.length <= 22 ? value : `${value.slice(0, 15)}…${value.slice(-8)}`;
}

function shortCommit(value: string): string {
  return value.slice(0, 12);
}

function externalRootCopy(status: GitProofBundleExternalRootStatus): string {
  switch (status) {
    case "MATCH":
      return "MATCH — the supplied external root matches this verified package.";
    case "NOT_PROVIDED":
      return "NOT PROVIDED — the package is self-consistent, but no externally retained root was checked.";
    case "MISMATCH":
      return "MISMATCH — this state is never rendered because the bundle loader rejects it.";
  }
}

function transitionRow(transition: StableGitTransition): string {
  return `<tr>
    <td>${escapeHtml(transition.kind.replaceAll("_", " "))}</td>
    <td><code title="${escapeHtml(transition.before.commit)}">${escapeHtml(shortCommit(transition.before.commit))}</code> <span class="verdict ${transition.before.verdict.toLowerCase()}">${escapeHtml(transition.before.verdict)}</span></td>
    <td>→</td>
    <td><code title="${escapeHtml(transition.after.commit)}">${escapeHtml(shortCommit(transition.after.commit))}</code> <span class="verdict ${transition.after.verdict.toLowerCase()}">${escapeHtml(transition.after.verdict)}</span></td>
    <td>${transition.before.executionIds.length} + ${transition.after.executionIds.length} distinct Docker executions</td>
  </tr>`;
}

function sandboxRows(view: VerifiedGitProofView): string {
  const representatives = new Map<string, GitInvestigationResult["runs"][number]>();
  for (const run of view.investigation.runs) {
    const key = `${run.sandbox.kind}:${run.result.executor}:${run.sandbox.policyDigest}`;
    if (!representatives.has(key)) representatives.set(key, run);
  }
  return [...representatives.values()].map((run) => {
    const coveredRuns = view.investigation.runs.filter((candidate) => candidate.sandbox.policyDigest === run.sandbox.policyDigest).length;
    const truncatedRuns = view.investigation.runs.filter((candidate) => candidate.sandbox.policyDigest === run.sandbox.policyDigest && candidate.result.outputTruncated).length;
    const runtime = run.sandbox.runtime;
    const runtimeEvidence = [
      runtime.image === null ? "image not attested" : `image ${runtime.image}`,
      runtime.entrypoint === null ? "entrypoint not attested" : `entrypoint ${runtime.entrypoint}`,
      runtime.network === null ? "network not attested" : `network ${runtime.network}`,
      runtime.rootFilesystemReadOnly ? "read-only root" : "writable root",
      runtime.user === null ? "user not attested" : `user ${runtime.user}`,
      runtime.capDropAll ? "all capabilities dropped" : "capability policy incomplete",
      runtime.noNewPrivileges ? "no-new-privileges" : "privilege policy incomplete",
      runtime.pull === null ? "pull policy not attested" : `pull ${runtime.pull}`,
      `limits ${runtime.limits.cpuCount} CPU / ${runtime.limits.memoryBytes} B / ${runtime.limits.timeoutMs} ms`
    ].join(" · ");
    return `<tr>
      <td>${escapeHtml(run.sandbox.kind.replaceAll("_", " "))}</td>
      <td>${escapeHtml(run.result.executor.replaceAll("_", " "))}</td>
      <td><code title="${escapeHtml(run.sandbox.policyDigest)}">${escapeHtml(shortDigest(run.sandbox.policyDigest))}</code></td>
      <td><code title="${escapeHtml(run.sandbox.commandDigest)}">${escapeHtml(shortDigest(run.sandbox.commandDigest))}</code></td>
      <td><code title="${escapeHtml(run.sandbox.environmentPolicyDigest)}">${escapeHtml(shortDigest(run.sandbox.environmentPolicyDigest))}</code></td>
      <td>${escapeHtml(runtimeEvidence)}</td>
      <td>${coveredRuns} runs${truncatedRuns === 0 ? "" : `; ${truncatedRuns} output-truncated`}</td>
    </tr>`;
  }).join("");
}

function lifecyclePanel(view: VerifiedGitProofView): string {
  const lifecycle = view.manifest.lifecycle;
  if (lifecycle.status === "UNBOUND") {
    return `<section class="card"><h2>Lifecycle binding</h2><p class="warning">UNBOUND</p><p>${escapeHtml(lifecycle.limitation)}</p></section>`;
  }
  const rows = lifecycle.checkpointBindings.map((binding) => `<tr>
    <td>${binding.stateIndex}</td>
    <td>${binding.sequence}</td>
    <td><code title="${escapeHtml(binding.checkpointDigest)}">${escapeHtml(shortDigest(binding.checkpointDigest))}</code></td>
  </tr>`).join("");
  return `<section class="card"><h2>Lifecycle binding</h2>
    <p><strong>BOUND</strong> via observed ${escapeHtml(lifecycle.transport.replaceAll("_", " "))} events. This records checkpoints, not private model reasoning or native interception.</p>
    <dl><div><dt>Ledger digest</dt><dd><code title="${escapeHtml(lifecycle.ledgerDigest)}">${escapeHtml(shortDigest(lifecycle.ledgerDigest))}</code></dd></div><div><dt>Ledger head</dt><dd><code title="${escapeHtml(lifecycle.headHash)}">${escapeHtml(shortDigest(lifecycle.headHash))}</code></dd></div></dl>
    <table><thead><tr><th>Git state</th><th>Ledger sequence</th><th>Checkpoint digest</th></tr></thead><tbody>${rows}</tbody></table>
  </section>`;
}

/** Render only verified, privacy-minimized evidence from a Git proof package. */
export function renderGitProofIncidentPage(view: VerifiedGitProofView): string {
  const { manifest, investigation, frozenWitness } = view;
  const ancestor = manifest.resolvedRange.ancestor;
  const descendant = manifest.resolvedRange.descendant;
  const transitionRows = investigation.transitions.length > 0
    ? investigation.transitions.map(transitionRow).join("")
    : "<tr><td colspan=\"5\">No stable transition was recorded.</td></tr>";
  const approval = frozenWitness.approval;
  const executionSummary = `${investigation.runs.length} recorded Docker-isolated executions across ${investigation.states.length} Git states (${investigation.executionsPerState} per state).`;

  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>FaultLine — verified Git proof</title>
<style>
:root{color-scheme:dark;--ink:#edf5f7;--muted:#9bb0b9;--canvas:#071118;--panel:#10222c;--line:#27424e;--cyan:#63e0dc;--lime:#b8f786;--rose:#ff829d;--amber:#ffbf73}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at top right,#143846,transparent 35%),var(--canvas);color:var(--ink);font:15px/1.5 Inter,ui-sans-serif,system-ui,sans-serif}.shell{max-width:1160px;margin:auto;padding:28px 20px 72px}.eyebrow{color:var(--cyan);font:700 11px/1.2 ui-monospace,Consolas,monospace;letter-spacing:.13em}h1{font-size:clamp(34px,5vw,58px);line-height:1;margin:10px 0}h1 em{color:var(--cyan);font-style:normal}h2{font-size:14px;letter-spacing:.1em;text-transform:uppercase;color:var(--muted);margin:0 0 14px}p{margin:8px 0}.lede{max-width:760px;color:var(--muted);font-size:17px}.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px;margin-top:18px}.card{background:rgba(16,34,44,.9);border:1px solid var(--line);border-radius:14px;padding:18px;overflow:auto}.wide{grid-column:1/-1}.status{display:inline-block;border:1px solid rgba(184,247,134,.38);background:rgba(184,247,134,.09);color:var(--lime);border-radius:999px;padding:5px 9px;font:700 11px ui-monospace,Consolas,monospace;letter-spacing:.08em}.warning{color:var(--amber);font-weight:700}dl{display:grid;gap:10px;margin:0}dl div{border-top:1px solid var(--line);padding-top:10px}dt{font-size:11px;text-transform:uppercase;letter-spacing:.1em;color:var(--muted)}dd{margin:3px 0 0;word-break:break-all}code{font:12px ui-monospace,SFMono-Regular,Consolas,monospace;color:#bfe8e7;word-break:break-all}table{border-collapse:collapse;width:100%;min-width:660px}th,td{text-align:left;vertical-align:top;padding:11px 8px;border-top:1px solid var(--line);font-size:13px}th{color:var(--muted);font-size:11px;letter-spacing:.08em;text-transform:uppercase}.verdict{display:inline-block;border-radius:999px;padding:2px 6px;font:700 10px ui-monospace,Consolas,monospace;letter-spacing:.05em}.pass{color:var(--lime);background:rgba(184,247,134,.11)}.fail{color:var(--rose);background:rgba(255,130,157,.12)}.footer{color:var(--muted);font-size:13px;border-top:1px solid var(--line);padding-top:16px;margin-top:32px}@media(max-width:760px){.grid{grid-template-columns:1fr}.wide{grid-column:auto}}
</style>
</head>
<body><main class="shell">
  <div class="eyebrow">FAULTLINE · PORTABLE GIT INVESTIGATION</div>
  <h1>Verified <em>evidence</em>,<br>read-only review.</h1>
  <p class="lede">This page was rendered only after FaultLine verified the complete declared file set and reconstructed its Git transition claim. It does not rerun a witness or execute repository code.</p>
  <section class="grid">
    <article class="card"><h2>Verifier status</h2><span class="status">SELF-CONSISTENT: VALID</span><dl><div><dt>Bundle root</dt><dd><code title="${escapeHtml(view.rootDigest)}">${escapeHtml(view.rootDigest)}</code></dd></div><div><dt>Declared files checked</dt><dd>${view.checkedFiles}</dd></div><div><dt>External root</dt><dd>${escapeHtml(externalRootCopy(view.externalRootStatus))}</dd></div></dl></article>
    <article class="card"><h2>Frozen witness</h2><p>${escapeHtml(frozenWitness.proposal.witness.behavior)}</p><dl><div><dt>Witness digest</dt><dd><code title="${escapeHtml(frozenWitness.witnessDigest)}">${escapeHtml(frozenWitness.witnessDigest)}</code></dd></div><div><dt>Frozen record</dt><dd><code title="${escapeHtml(frozenWitness.frozenDigest)}">${escapeHtml(frozenWitness.frozenDigest)}</code></dd></div><div><dt>Human approval</dt><dd>${escapeHtml(approval.approvedAt)} · recorded before freeze</dd></div></dl></article>
    <article class="card wide"><h2>Immutable Git range</h2><table><thead><tr><th>Position</th><th>Commit</th><th>Tree</th><th>State index</th></tr></thead><tbody><tr><td>Known ancestor</td><td><code title="${escapeHtml(ancestor.commit)}">${escapeHtml(ancestor.commit)}</code></td><td><code title="${escapeHtml(ancestor.tree)}">${escapeHtml(ancestor.tree)}</code></td><td>${ancestor.index}</td></tr><tr><td>Known descendant</td><td><code title="${escapeHtml(descendant.commit)}">${escapeHtml(descendant.commit)}</code></td><td><code title="${escapeHtml(descendant.tree)}">${escapeHtml(descendant.tree)}</code></td><td>${descendant.index}</td></tr></tbody></table></article>
    <article class="card wide"><h2>Stable transitions</h2><p>${escapeHtml(executionSummary)} ${investigation.nonMonotonic ? "The recorded sequence is non-monotonic; later repairs or regressions remain visible." : "No opposite-direction transition was recorded."}</p><table><thead><tr><th>Transition</th><th>Before</th><th></th><th>After</th><th>Evidence</th></tr></thead><tbody>${transitionRows}</tbody></table></article>
    <article class="card wide"><h2>Sandbox evidence</h2><p>Recorded execution trust: <strong>${escapeHtml(investigation.proof.executionTrust.replaceAll("_", " "))}</strong>. Policy and command values are displayed as digests; raw output and overlay bytes are intentionally omitted.</p><table><thead><tr><th>Kind</th><th>Executor</th><th>Policy</th><th>Command</th><th>Environment</th><th>Runtime constraints</th><th>Coverage</th></tr></thead><tbody>${sandboxRows(view)}</tbody></table></article>
    ${lifecyclePanel(view)}
  </section>
  <footer class="footer">Manifest ${escapeHtml(manifest.schemaVersion)} · generated ${escapeHtml(manifest.generatedAt)} · proof claims remain predicate-specific and do not establish model intent, a unique semantic cause, or native Codex interception.</footer>
</main></body></html>`;
}
