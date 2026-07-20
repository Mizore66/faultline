import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { randomBytes, timingSafeEqual } from "node:crypto";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { readIncidentDraft } from "./incident-store.js";
import type { IncidentDraft } from "./incident.js";
import { readFrozenWitness, readWitnessApproval } from "./witness-lock.js";
import {
  approveAndFreezeWitnessReview,
  approveWitnessReview,
  freezeWitnessReview,
  openWitnessReview,
  type WitnessReview
} from "./witness-review.js";
import {
  buildHumanizedWitnessReview,
  formatCageBadgeLine,
  type HumanizedWitnessReview
} from "./witness-review-presentation.js";
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
  /**
   * When true, keep the legacy two-step Approve then Freeze forms.
   * Default is atomic Approve & freeze (COH-07).
   */
  readonly requireSeparateFreeze?: boolean;
};

type ReviewState = {
  readonly review: WitnessReview;
  readonly draft: IncidentDraft | null;
  readonly approved: ReturnType<typeof readWitnessApproval> | null;
  readonly frozen: ReturnType<typeof readFrozenWitness> | null;
  readonly humanized: HumanizedWitnessReview;
};

function isMissingRecord(error: unknown): boolean {
  return error instanceof Error
    && "code" in error
    && (error as NodeJS.ErrnoException).code === "ENOENT";
}

function readBoundDraft(draftStore: string | undefined, review: WitnessReview): IncidentDraft | null {
  if (draftStore === undefined) return null;
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
    if (!isMissingRecord(error)) throw error;
  }
  try {
    frozen = readFrozenWitness(store, proposalId);
  } catch (error) {
    if (!isMissingRecord(error)) throw error;
  }
  const humanized = buildHumanizedWitnessReview({
    review,
    draft,
    ...(approved === null ? {} : { approvalDigest: approved.approvalDigest }),
    ...(frozen === null ? {} : { frozenDigest: frozen.frozenDigest })
  });
  return { review, draft, approved, frozen, humanized };
}

function statusPanel(state: ReviewState): string {
  if (state.frozen !== null) {
    return `<article class="card panel"><h3>Freeze record</h3><p><strong>FROZEN</strong> by ${escapeHtml(state.approved?.approvedBy ?? "reviewer")}.</p><p><small>Open Evidence record below for digests. No server action reruns the witness.</small></p></article>`;
  }
  if (state.approved !== null) {
    return `<article class="card panel"><h3>Human approval</h3><p><strong>APPROVED</strong> by ${escapeHtml(state.approved.approvedBy)}.</p><small>The proposal is not frozen yet. Freezing requires one more explicit action below.</small></article>`;
  }
  return `<article class="card panel"><h3>Human approval</h3><p class="empty-state"><strong>NOT APPROVED</strong><br>Review the exact command, overlays, and cage before recording an approval.</p></article>`;
}

function rangePanel(state: ReviewState): string {
  const { range } = state.humanized;
  const lines = range.lines.length === 0
    ? `<p class="empty-state">${escapeHtml(range.unavailableReason ?? "No commits to show.")}</p>`
    : `<ul class="range-log">${range.lines.map((line) =>
      `<li><code>${escapeHtml(line.short)}</code> <strong>${escapeHtml(line.subject)}</strong> <span>${escapeHtml(line.author)}</span> <em>${escapeHtml(line.relativeAge)}</em></li>`
    ).join("")}</ul>`;
  return `<article class="card panel"><h3>Range · ${escapeHtml(range.summary)}</h3>${range.source === undefined ? "" : `<p><small>Source: ${escapeHtml(range.source)}</small></p>`}${range.unavailableReason === undefined ? "" : `<p><small>${escapeHtml(range.unavailableReason)}</small></p>`}${lines}</article>`;
}

function evidenceDetails(state: ReviewState): string {
  const e = state.humanized.evidence;
  const rows: Array<[string, string]> = [
    ["Proposal digest", e.proposalDigest],
    ["Review digest", e.reviewDigest],
    ["Command digest", e.commandDigest],
    ["Overlay digest", e.overlayDigest],
    ["Packet digest", e.incidentPacketDigest]
  ];
  if (e.draftDigest !== undefined) rows.push(["Draft digest", e.draftDigest]);
  if (e.approvalDigest !== undefined) rows.push(["Approval digest", e.approvalDigest]);
  if (e.frozenDigest !== undefined) rows.push(["Frozen digest", e.frozenDigest]);
  return `<details class="card panel evidence-record"><summary>Evidence record</summary><p><small>Digests are collapsed here so the review surface stays human-readable. Expand only when you need to retain or compare hashes.</small></p>${rows.map(([label, value]) =>
    `<div class="proof"><span>${escapeHtml(label)}</span><code>${escapeHtml(value)}</code></div>`
  ).join("")}</details>`;
}

function overlayRows(state: ReviewState): string {
  if (state.humanized.overlays.length === 0) {
    return "<p class=\"empty-state\"><strong>No overlays</strong><br>The proposed command is reviewed without generated overlay bytes.</p>";
  }
  return state.humanized.overlays.map((overlay) => {
    if (overlay.kind === "utf8") {
      return `<details open><summary><code>${escapeHtml(overlay.path)}</code> · UTF-8 · ${overlay.byteLength} bytes</summary><pre>${escapeHtml(overlay.text ?? "")}</pre></details>`;
    }
    return `<details><summary><code>${escapeHtml(overlay.path)}</code> · binary · ${overlay.byteLength} bytes</summary><p><small>Base64 (binary content)</small></p><pre>${escapeHtml(overlay.base64 ?? "")}</pre></details>`;
  }).join("");
}

function atomicForm(state: ReviewState, token: string): string {
  if (state.approved !== null || state.frozen !== null) return "";
  const digest = escapeHtml(state.review.reviewDigest);
  return `<form class="card panel review-form" method="post" action="/approve-freeze"><h3>Approve &amp; freeze</h3><p>One action writes both the human approval and the write-once freeze record. It does not run the command.</p><input type="hidden" name="token" value="${escapeHtml(token)}" /><input type="hidden" name="reviewDigest" value="${digest}" /><label>Reviewer identity<input name="approvedBy" required maxlength="512" autocomplete="name" /></label><label>Optional review note<textarea name="note" maxlength="8000" rows="3"></textarea></label><label class="acknowledgement"><input type="checkbox" name="acknowledged" value="exact-witness-reviewed" required />I reviewed the exact command and every visible overlay.</label><button type="submit">Approve &amp; freeze this witness</button></form>`;
}

function approvalForm(state: ReviewState, token: string): string {
  if (state.approved !== null || state.frozen !== null) return "";
  const digest = escapeHtml(state.review.reviewDigest);
  return `<form class="card panel review-form" method="post" action="/approve"><h3>Approve reviewed witness</h3><p>Two-step mode: approval first, freeze second.</p><input type="hidden" name="token" value="${escapeHtml(token)}" /><input type="hidden" name="reviewDigest" value="${digest}" /><label>Reviewer identity<input name="approvedBy" required maxlength="512" autocomplete="name" /></label><label>Optional review note<textarea name="note" maxlength="8000" rows="3"></textarea></label><label class="acknowledgement"><input type="checkbox" name="acknowledged" value="exact-witness-reviewed" required />I reviewed the exact command and every visible overlay.</label><button type="submit">Approve this reviewed witness</button></form>`;
}

function freezeForm(state: ReviewState, token: string): string {
  if (state.approved === null || state.frozen !== null) return "";
  const digest = escapeHtml(state.review.reviewDigest);
  return `<form class="card panel review-form" method="post" action="/freeze"><h3>Freeze approved witness</h3><p>Freezing snapshots the already-approved proposal as a write-once record.</p><input type="hidden" name="token" value="${escapeHtml(token)}" /><input type="hidden" name="reviewDigest" value="${digest}" /><button type="submit">Freeze this approved witness</button></form>`;
}

function renderReviewPage(state: ReviewState, token: string, notice: string | undefined, requireSeparateFreeze: boolean): string {
  const view = state.humanized;
  const noticeHtml = notice === undefined ? "" : `<p class="review-notice" role="status">${escapeHtml(notice)}</p>`;
  const confirmForms = requireSeparateFreeze
    ? `${approvalForm(state, token)}${freezeForm(state, token)}`
    : atomicForm(state, token);
  return `${renderIncidentPageDocumentStart("local witness review")}
${renderIncidentPageProductChrome({
  command: "fl witness review",
  notice: "Local review workbench: exact command and overlay content are shown only on 127.0.0.1. FaultLine will not execute or materialize them from this page."
})}
<section class="hero"><div><div class="eyebrow">FAULTLINE · HUMAN WITNESS REVIEW</div><h1>Freeze the <em>question</em><br>only after review.</h1><p class="lede">Inspect the exact immutable proposal, then ${requireSeparateFreeze ? "approve and freeze in two steps" : "approve &amp; freeze in one action"}. This page never runs the displayed command.</p>${noticeHtml}<p class="cage-badges" aria-label="Execution cage">${escapeHtml(formatCageBadgeLine(view.cage))}</p></div><aside class="card metric-card" aria-label="Witness review summary"><div class="metric"><b>${view.overlays.length}</b><small>overlays</small></div><div class="metric"><b>${escapeHtml(view.cage.timeout.replace("timeout ", ""))}</b><small>timeout</small></div><div class="metric"><b>OFF</b><small>network</small></div><div class="metric"><b>${state.frozen === null ? "PENDING" : "FROZEN"}</b><small>review state</small></div></aside></section>
<section class="section" id="break"><div class="section-head"><span class="number">01</span><h2>REVIEW</h2></div><div class="grid"><article class="card panel witness"><h3>Strictly blinded incident packet</h3><p><strong>${escapeHtml(view.symptom)}</strong></p><div class="proof"><span>Lifecycle</span><small>${escapeHtml(view.lifecycle)}</small></div><small>Raw CI-log content is intentionally not displayed in this browser surface.</small></article>${rangePanel(state)}${statusPanel(state)}</div></section>
<section class="section" id="find"><div class="section-head"><span class="number">02</span><h2>EXACT WITNESS</h2></div><div class="grid"><article class="card panel"><h3>Command — not executed</h3><pre>${escapeHtml(view.command)}</pre><p><strong>${escapeHtml(view.behavior)}</strong></p></article><article class="card panel"><h3>Execution cage</h3><p>${escapeHtml(formatCageBadgeLine(view.cage))}</p><div class="proof"><span>Credentials</span><small>redacted</small></div></article></div></section>
<section class="section" id="prove"><div class="section-head"><span class="number">03</span><h2>OVERLAYS</h2></div><article class="card panel"><h3>Reviewed overlay content — not materialized</h3><p>UTF-8 overlays are shown decoded. Binary overlays keep canonical base64.</p>${overlayRows(state)}</article></section>
<section class="section" id="fix"><div class="section-head"><span class="number">04</span><h2>CONFIRM</h2></div><div class="grid">${evidenceDetails(state)}${confirmForms}</div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">05</span><h2>PROOF BOUNDARY</h2></div><article class="card panel"><h3>What this does not establish</h3><p>A local approval/freeze is not a reproduction, Docker execution, first-bad-state result, repair, or prevention claim.</p></article></section>
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

function twoStepEnabled(options: WitnessReviewServerOptions): boolean {
  if (options.requireSeparateFreeze === true) return true;
  return process.env.FAULTLINE_WITNESS_TWO_STEP === "1";
}

function requestPath(url: string | undefined): string {
  const raw = (url ?? "/").split("?")[0] || "/";
  // Avoid WHATWG protocol-relative parsing of paths like "//approve".
  if (/^https?:\/\//i.test(raw)) {
    return new URL(raw).pathname.replace(/\/{2,}/g, "/") || "/";
  }
  return raw.replace(/\/{2,}/g, "/") || "/";
}

/**
 * Serve a deliberately local human review page. Actions are protected by a
 * high-entropy form token and each one revalidates the stored proposal before
 * calling the write-once witness-lock APIs.
 */
export async function startWitnessReviewServer(options: WitnessReviewServerOptions): Promise<WitnessReviewServer> {
  const token = randomBytes(32).toString("hex");
  const requireSeparateFreeze = twoStepEnabled(options);
  const server = createServer(async (request, response) => {
    const path = requestPath(request.url);
    if (request.method === "GET" && path === "/") {
      try {
        writeHtml(response, 200, renderReviewPage(tryReadState(options.store, options.proposalId, options.draftStore), token, undefined, requireSeparateFreeze));
      } catch (error) {
        writeHtml(response, 409, renderFailurePage(error instanceof Error ? error.message : String(error)));
      }
      return;
    }
    const allowed = requireSeparateFreeze
      ? path === "/approve" || path === "/freeze"
      : path === "/approve-freeze" || path === "/approve" || path === "/freeze";
    if (request.method !== "POST" || !allowed) {
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
      if (path === "/approve-freeze") {
        if (form.acknowledged !== "exact-witness-reviewed") {
          writeHtml(response, 409, renderFailurePage("Human approval requires acknowledgement that the exact command and overlay were reviewed."));
          return;
        }
        approveAndFreezeWitnessReview(options.store, review, {
          reviewDigest: form.reviewDigest ?? "",
          approvedBy: form.approvedBy ?? "",
          ...(form.note === undefined || form.note === "" ? {} : { note: form.note })
        });
        writeHtml(response, 200, renderReviewPage(tryReadState(options.store, options.proposalId, options.draftStore), token, "Approved and frozen as a write-once record.", requireSeparateFreeze));
        return;
      }
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
        writeHtml(response, 200, renderReviewPage(tryReadState(options.store, options.proposalId, options.draftStore), token, "Human approval was recorded. Freezing still requires its own explicit action.", requireSeparateFreeze));
        return;
      }
      freezeWitnessReview(options.store, review, { reviewDigest: form.reviewDigest ?? "" });
      writeHtml(response, 200, renderReviewPage(tryReadState(options.store, options.proposalId, options.draftStore), token, "The approved witness is now frozen as a write-once record.", requireSeparateFreeze));
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
        rejectStart(new Error("Witness review server failed to bind a TCP port."));
        return;
      }
      resolveStart({
        url: `http://127.0.0.1:${address.port}`,
        close: () => new Promise<void>((resolveClose, rejectClose) => {
          server.close((error) => {
            if (error) rejectClose(error);
            else resolveClose();
          });
        })
      });
    });
  });
}
