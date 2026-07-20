import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { randomBytes, timingSafeEqual } from "node:crypto";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { readIncidentDraft } from "./incident-store.js";
import type { IncidentDraft } from "./incident.js";
import { readFrozenWitness, readWitnessApproval } from "./witness-lock.js";
import {
  approveWitnessReview,
  freezeWitnessReview,
  openWitnessReview,
  type WitnessReview
} from "./witness-review.js";
import {
  escapeHtml,
  renderIncidentPageDocumentEnd,
  renderIncidentPageDocumentStart,
  renderIncidentPageProductChrome
} from "./incident-page.js";

const MAX_FORM_BYTES = 32 * 1024;

export type WitnessReviewServer = {
  readonly url: string;
  close(): Promise<void>;
};

export type WitnessReviewServerOptions = {
  readonly store: string;
  readonly proposalId: string;
  /** Optional incident-draft store for the guided intake context. */
  readonly draftStore?: string;
  readonly port?: number;
};

type ReviewState = {
  readonly review: WitnessReview;
  readonly draft: IncidentDraft | null;
  readonly approved: ReturnType<typeof readWitnessApproval> | null;
  readonly frozen: ReturnType<typeof readFrozenWitness> | null;
};

function isMissingRecord(error: unknown): boolean {
  return error instanceof Error
    && "code" in error
    && (error as NodeJS.ErrnoException).code === "ENOENT";
}

function readBoundDraft(draftStore: string | undefined, review: WitnessReview): IncidentDraft | null {
  if (draftStore === undefined) return null;
  // A standalone manually proposed witness has no incident draft. Do not make
  // that optional context a failure, but validate it rigorously if it exists.
  if (!existsSync(join(draftStore, "drafts", `${review.proposal.proposalId}.json`))) return null;
  let draft: IncidentDraft;
  try {
    draft = readIncidentDraft(draftStore, review.proposal.proposalId).draft;
  } catch (error) {
    if (isMissingRecord(error)) return null;
    throw error;
  }
  if (draft.review.witnessState !== "PROPOSED") {
    throw new Error("Incident draft is not bound to a proposed witness; do not review or freeze it through this workbench.");
  }
  const binding = draft.review.proposal;
  if (binding.proposalId !== review.proposal.proposalId
    || binding.proposalDigest !== review.proposal.proposalDigest
    || binding.incidentPacketDigest !== review.proposal.incidentPacketDigest
    || binding.commandDigest !== review.proposal.witness.commandDigest) {
    throw new Error("Incident draft does not match the stored witness proposal; do not approve or freeze either record.");
  }
  return draft;
}

function tryReadState(store: string, proposalId: string, draftStore?: string): ReviewState {
  const review = openWitnessReview(store, proposalId);
  const draft = readBoundDraft(draftStore, review);
  let approved: ReviewState["approved"] = null;
  let frozen: ReviewState["frozen"] = null;
  try {
    approved = readWitnessApproval(store, proposalId);
  } catch (error) {
    // Absence is an expected state in the review workflow. Any malformed
    // proposal was already rejected before this point by openWitnessReview.
    if (!isMissingRecord(error)) throw error;
  }
  try {
    frozen = readFrozenWitness(store, proposalId);
  } catch (error) {
    // Freeze is optional until a human has completed review.
    if (!isMissingRecord(error)) throw error;
  }
  return { review, draft, approved, frozen };
}

function statusPanel(state: ReviewState): string {
  if (state.frozen !== null) {
    return `<article class="card panel"><h3>Freeze record</h3><p><strong>FROZEN</strong></p><div class="proof"><span>Frozen digest</span><code>${escapeHtml(state.frozen.frozenDigest)}</code></div><div class="proof"><span>Proof bundle</span><small>NOT ATTACHED - create one only after Docker-isolated replay.</small></div><small>No server action reruns the witness. Retain this digest outside the workspace before starting proof-grade replay, then open the resulting package with <code>fl serve --bundle &lt;proof&gt;</code>.</small></article>`;
  }
  if (state.approved !== null) {
    return `<article class="card panel"><h3>Human approval</h3><p><strong>APPROVED</strong> by ${escapeHtml(state.approved.approvedBy)}.</p><div class="proof"><span>Approval digest</span><code>${escapeHtml(state.approved.approvalDigest)}</code></div><small>The proposal is not frozen yet. Freezing requires one more explicit click below.</small></article>`;
  }
  return `<article class="card panel"><h3>Human approval</h3><p class="empty-state"><strong>NOT APPROVED</strong><br>Review the exact command, overlays, and policy before recording an approval.</p><small>This local page is a review surface only. It does not execute or materialize the witness.</small></article>`;
}

function intakePanel(state: ReviewState): string {
  if (state.draft === null) {
    return `<article class="card panel"><h3>Incident intake binding</h3><p class="empty-state"><strong>NO LINKED DRAFT</strong><br>This may be a standalone witness proposal rather than a guided incident intake.</p><small>FaultLine can still review the exact proposed witness, but no draft range or selected runtime was supplied to this local screen.</small></article>`;
  }
  const runtime = state.draft.runtime === undefined
    ? "No runtime image selected during intake."
    : state.draft.runtime.image;
  const lifecycle = state.frozen !== null
    ? "DRAFT -> PROPOSED -> HUMAN APPROVED -> FROZEN"
    : state.approved !== null
      ? "DRAFT -> PROPOSED -> HUMAN APPROVED -> awaiting explicit freeze"
      : "DRAFT -> PROPOSED -> awaiting human approval";
  return `<article class="card panel"><h3>Incident intake binding</h3><p><strong>DRAFT &rarr; PROPOSED</strong><br>The immutable intake record is bound to this exact proposal before human approval.</p><div class="proof"><span>Review state</span><small>${escapeHtml(lifecycle)}</small></div><div class="proof"><span>Range source</span><small>${escapeHtml(state.draft.range.source)}</small></div><div class="proof"><span>Known good</span><code>${escapeHtml(state.draft.range.ancestor)}</code></div><div class="proof"><span>Known bad</span><code>${escapeHtml(state.draft.range.descendant)}</code></div><div class="proof"><span>Runtime</span><code>${escapeHtml(runtime)}</code></div><div class="proof"><span>Draft digest</span><code>${escapeHtml(state.draft.draftDigest)}</code></div><small>Nothing in this intake has run. Approval and freeze below do not change that fact.</small></article>`;
}

function approvalForm(state: ReviewState, token: string): string {
  if (state.approved !== null || state.frozen !== null) return "";
  const digest = escapeHtml(state.review.reviewDigest);
  return `<form class="card panel review-form" method="post" action="/approve"><h3>Approve reviewed witness</h3><p>Approval binds the displayed proposal, command, and overlay digests to a human actor. It does not run the command.</p><input type="hidden" name="token" value="${escapeHtml(token)}" /><input type="hidden" name="reviewDigest" value="${digest}" /><label>Reviewer identity<input name="approvedBy" required maxlength="512" autocomplete="name" /></label><label>Optional review note<textarea name="note" maxlength="8000" rows="3"></textarea></label><label class="acknowledgement"><input type="checkbox" name="acknowledged" value="exact-witness-reviewed" required />I reviewed the exact command and every visible overlay byte representation.</label><button type="submit">Approve this reviewed witness</button><small>Confirmation digest: <code>${digest}</code></small></form>`;
}

function freezeForm(state: ReviewState, token: string): string {
  if (state.approved === null || state.frozen !== null) return "";
  const digest = escapeHtml(state.review.reviewDigest);
  return `<form class="card panel review-form" method="post" action="/freeze"><h3>Freeze approved witness</h3><p>Freezing snapshots the already-approved proposal as a write-once record. It does not run the command.</p><input type="hidden" name="token" value="${escapeHtml(token)}" /><input type="hidden" name="reviewDigest" value="${digest}" /><button type="submit">Freeze this approved witness</button><small>Confirmation digest: <code>${digest}</code></small></form>`;
}

function overlayRows(review: WitnessReview): string {
  if (review.proposal.witness.overlays.length === 0) {
    return "<p class=\"empty-state\"><strong>No overlays</strong><br>The proposed command is reviewed without generated overlay bytes.</p>";
  }
  return review.proposal.witness.overlays.map((overlay) => `<details><summary><code>${escapeHtml(overlay.path)}</code> · ${overlay.bytesBase64.length} base64 characters</summary><p><small>Exact base64 bytes (sensitive; never materialized by this page)</small></p><pre>${escapeHtml(overlay.bytesBase64)}</pre><div class="proof"><span>Bytes digest</span><code>${escapeHtml(overlay.bytesDigest)}</code></div></details>`).join("");
}

function renderReviewPage(state: ReviewState, token: string, notice?: string): string {
  const { review } = state;
  const proposal = review.proposal;
  const noticeHtml = notice === undefined ? "" : `<p class="review-notice" role="status">${escapeHtml(notice)}</p>`;
  return `${renderIncidentPageDocumentStart("local witness review")}
${renderIncidentPageProductChrome({
  command: "fl witness review",
  notice: "Local review workbench: exact command and overlay bytes are shown only on 127.0.0.1. FaultLine will not execute or materialize them from this page."
})}
<section class="hero"><div><div class="eyebrow">FAULTLINE · HUMAN WITNESS REVIEW</div><h1>Freeze the <em>question</em><br>only after review.</h1><p class="lede">Inspect the exact immutable proposal before one human approval and one explicit freeze. This page is local, read-only until you press an action, and never runs the displayed command.</p>${noticeHtml}</div><aside class="card metric-card" aria-label="Witness review summary"><div class="metric"><b>${proposal.witness.overlays.length}</b><small>reviewed overlays</small></div><div class="metric"><b>${proposal.witness.policy.timeoutSeconds}s</b><small>timeout</small></div><div class="metric"><b>OFF</b><small>network</small></div><div class="metric"><b>${state.frozen === null ? "PENDING" : "FROZEN"}</b><small>review state</small></div></aside></section>
<section class="section" id="break"><div class="section-head"><span class="number">01</span><h2>REVIEW</h2></div><div class="grid"><article class="card panel witness"><h3>Strictly blinded incident packet</h3><p><strong>${escapeHtml(proposal.incidentPacket.symptom)}</strong></p><div class="proof"><span>Packet digest</span><code>${escapeHtml(proposal.incidentPacketDigest)}</code></div><div class="proof"><span>Allowed fields</span><small>symptom · CI log · language · repository summary</small></div><small>FaultLine verifies protocol shape before it will enable approval or freeze. Raw CI-log content is intentionally not displayed in this browser surface.</small></article>${intakePanel(state)}${statusPanel(state)}</div></section>
<section class="section" id="find"><div class="section-head"><span class="number">02</span><h2>EXACT WITNESS</h2></div><div class="grid"><article class="card panel"><h3>Command — not executed</h3><pre>${escapeHtml(proposal.witness.command)}</pre><div class="proof"><span>Command digest</span><code>${escapeHtml(proposal.witness.commandDigest)}</code></div><p><strong>${escapeHtml(proposal.witness.behavior)}</strong></p></article><article class="card panel"><h3>Execution policy</h3><div class="proof"><span>Network</span><small>${escapeHtml(proposal.witness.policy.network)}</small></div><div class="proof"><span>Credentials</span><small>${escapeHtml(proposal.witness.policy.credentials)}</small></div><div class="proof"><span>Timeout</span><small>${proposal.witness.policy.timeoutSeconds} seconds</small></div><div class="proof"><span>Overlay digest</span><code>${escapeHtml(proposal.witness.overlayDigest)}</code></div></article></div></section>
<section class="section" id="prove"><div class="section-head"><span class="number">03</span><h2>OVERLAYS</h2></div><article class="card panel"><h3>Exact overlay bytes — not materialized</h3><p>Expand each entry only if you are authorized to inspect its sensitive bytes. FaultLine shows the canonical base64 representation to prevent terminal or encoding ambiguity.</p>${overlayRows(review)}</article></section>
<section class="section" id="fix"><div class="section-head"><span class="number">04</span><h2>CONFIRM</h2></div><div class="grid">${approvalForm(state, token)}${freezeForm(state, token)}<article class="card panel"><h3>Review binding</h3><div class="proof"><span>Proposal digest</span><code>${escapeHtml(proposal.proposalDigest)}</code></div><div class="proof"><span>Review digest</span><code>${escapeHtml(review.reviewDigest)}</code></div><small>Each action re-reads the proposal and rejects a stale, malformed, unblinded, or digest-mismatched review before it writes an immutable record.</small></article></div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">05</span><h2>PROOF BOUNDARY</h2></div><article class="card panel"><h3>What this does not establish</h3><p>A local approval/freeze is not a reproduction, Docker execution, first-bad-state result, repair, or prevention claim. It preserves the reviewed question that a later proof-grade investigation may answer.</p><small>Keep this local window open only while reviewing sensitive material, then close it. The server binds to 127.0.0.1, sends no telemetry, and disables browser caching.</small></article></section>
${renderIncidentPageDocumentEnd({ footer: "FaultLine records a human-reviewed witness without executing it. Proof claims require a separately completed Docker-isolated investigation and a verified portable bundle." })}`;
}

function renderFailurePage(message: string): string {
  return `${renderIncidentPageDocumentStart("witness review unavailable")}
${renderIncidentPageProductChrome({ command: "fl witness review", notice: "The local review server refused to present a proposal that failed its blinded-protocol or integrity checks." })}
<section class="hero"><div><div class="eyebrow">FAULTLINE · REVIEW REFUSED</div><h1>Do not freeze<br><em>this proposal.</em></h1><p class="lede">${escapeHtml(message)}</p></div></section>
${renderIncidentPageDocumentEnd({ footer: "Create a fresh, strictly blinded proposal after resolving the integrity problem. FaultLine did not execute, approve, or freeze anything." })}`;
}

function writeHtml(response: ServerResponse, status: number, html: string): void {
  response.writeHead(status, {
    "content-type": "text/html; charset=utf-8",
    "cache-control": "no-store",
    "content-security-policy": "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'",
    "referrer-policy": "no-referrer",
    "x-content-type-options": "nosniff"
  });
  response.end(html);
}

async function readForm(request: IncomingMessage): Promise<Record<string, string>> {
  const chunks: Buffer[] = [];
  let bytes = 0;
  for await (const chunk of request) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    bytes += buffer.length;
    if (bytes > MAX_FORM_BYTES) throw new Error("review form is too large");
    chunks.push(buffer);
  }
  const parsed = new URLSearchParams(Buffer.concat(chunks).toString("utf8"));
  const form: Record<string, string> = {};
  for (const [key, value] of parsed.entries()) form[key] = value;
  return form;
}

function sameToken(expected: string, actual: string | undefined): boolean {
  if (actual === undefined || actual.length !== expected.length) return false;
  return timingSafeEqual(Buffer.from(expected, "utf8"), Buffer.from(actual, "utf8"));
}

/**
 * Serve a deliberately local human review page. Actions are protected by a
 * high-entropy form token and each one revalidates the stored proposal before
 * calling the write-once witness-lock APIs.
 */
export async function startWitnessReviewServer(options: WitnessReviewServerOptions): Promise<WitnessReviewServer> {
  const token = randomBytes(32).toString("hex");
  const server = createServer(async (request, response) => {
    const path = new URL(request.url ?? "/", "http://127.0.0.1").pathname;
    if (request.method === "GET" && path === "/") {
      try {
        writeHtml(response, 200, renderReviewPage(tryReadState(options.store, options.proposalId, options.draftStore), token));
      } catch (error) {
        writeHtml(response, 409, renderFailurePage(error instanceof Error ? error.message : String(error)));
      }
      return;
    }
    if (request.method !== "POST" || (path !== "/approve" && path !== "/freeze")) {
      response.writeHead(404, { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" });
      response.end("Not found\n");
      return;
    }
    try {
      const form = await readForm(request);
      if (!sameToken(token, form.token)) {
        writeHtml(response, 403, renderFailurePage("This local review action did not include the current review token."));
        return;
      }
      const review = tryReadState(options.store, options.proposalId, options.draftStore).review;
      if (path === "/approve") {
        if (form.acknowledged !== "exact-witness-reviewed") {
          writeHtml(response, 409, renderFailurePage("Human approval requires acknowledgement that the exact command and overlay bytes were reviewed."));
          return;
        }
        approveWitnessReview(options.store, review, {
          reviewDigest: form.reviewDigest ?? "",
          approvedBy: form.approvedBy ?? "",
          ...(form.note === undefined || form.note === "" ? {} : { note: form.note })
        });
        writeHtml(response, 200, renderReviewPage(tryReadState(options.store, options.proposalId, options.draftStore), token, "Human approval was recorded. Freezing still requires its own explicit action."));
        return;
      }
      freezeWitnessReview(options.store, review, { reviewDigest: form.reviewDigest ?? "" });
      writeHtml(response, 200, renderReviewPage(tryReadState(options.store, options.proposalId, options.draftStore), token, "The approved witness is now frozen as a write-once record."));
    } catch (error) {
      writeHtml(response, 409, renderFailurePage(error instanceof Error ? error.message : String(error)));
    }
  });

  return await new Promise<WitnessReviewServer>((resolveStart, rejectStart) => {
    const rejectListen = (error: Error) => rejectStart(error);
    server.once("error", rejectListen);
    server.listen(options.port ?? 0, "127.0.0.1", () => {
      server.off("error", rejectListen);
      const address = server.address();
      if (address === null || typeof address === "string") {
        void new Promise<void>((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()))
          .then(() => rejectStart(new Error("Witness review server did not report a TCP address")), rejectStart);
        return;
      }
      resolveStart({
        url: `http://127.0.0.1:${address.port}`,
        close: () => new Promise<void>((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()))
      });
    });
  });
}
