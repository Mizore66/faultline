/**
 * Shared product chrome for every FaultLine incident view.
 *
 * Callers own the evidence projection placed between the chrome and footer.
 * This module intentionally does not read a bundle, execute a witness, or
 * decide whether evidence is valid; verified-bundle callers must do that work
 * before they call the renderer.
 */

export function escapeHtml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

const incidentPageStyle = `
:root{color-scheme:dark;--ink:#ecf4f7;--muted:#94a8b1;--canvas:#071118;--panel:rgba(14,30,39,.88);--line:rgba(149,191,200,.18);--cyan:#65e4df;--lime:#b4f37b;--orange:#ffbd72;--rose:#ff7f9c;--violet:#ad9bff}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at top right,#123442 0,transparent 34%),radial-gradient(circle at 8% 22%,#15293d 0,transparent 30%),var(--canvas);color:var(--ink);font-family:Inter,ui-sans-serif,system-ui,sans-serif}.shell{max-width:1240px;margin:0 auto;padding:24px 22px 80px}.topbar{display:flex;justify-content:space-between;gap:24px;align-items:center;padding-bottom:22px;border-bottom:1px solid var(--line)}.brand{display:flex;align-items:center;gap:12px;letter-spacing:-.02em;font-weight:800;font-size:24px}.brand-mark{width:28px;height:28px;border:2px solid var(--cyan);border-radius:7px;position:relative;transform:rotate(45deg)}.brand-mark::after{content:"";position:absolute;background:var(--rose);width:2px;height:34px;left:11px;top:-5px;transform:rotate(18deg)}.command,.mono,code{font-family:"SFMono-Regular",Consolas,monospace}.command{color:var(--lime);background:rgba(180,243,123,.08);border:1px solid rgba(180,243,123,.19);padding:8px 10px;border-radius:8px}.beat-nav{display:grid;grid-template-columns:repeat(5,1fr);gap:6px;margin:22px 0}.beat-nav span{color:var(--muted);font-size:11px;letter-spacing:.13em;font-weight:750;padding:8px 0;border-bottom:2px solid var(--line)}.beat-nav span:nth-child(1){color:var(--rose);border-color:var(--rose)}.beat-nav span:nth-child(2){color:var(--cyan);border-color:var(--cyan)}.beat-nav span:nth-child(3){color:var(--orange);border-color:var(--orange)}.beat-nav span:nth-child(4){color:var(--violet);border-color:var(--violet)}.beat-nav span:nth-child(5){color:var(--lime);border-color:var(--lime)}.notice{border:1px solid rgba(255,189,114,.28);color:#ffdda6;background:rgba(255,189,114,.07);padding:12px 14px;border-radius:10px;font-size:14px}.hero{display:grid;grid-template-columns:1.45fr .85fr;gap:18px;padding:34px 0 22px}.eyebrow{color:var(--cyan);font-size:12px;font-weight:800;letter-spacing:.12em}h1{font-size:clamp(34px,5vw,68px);line-height:.98;letter-spacing:-.06em;max-width:760px;margin:12px 0 16px}h1 em{color:var(--cyan);font-style:normal}.lede{color:var(--muted);max-width:720px;line-height:1.6;font-size:17px}.card{border:1px solid var(--line);border-radius:16px;background:var(--panel);box-shadow:0 18px 42px rgba(0,0,0,.14)}.metric-card{display:grid;grid-template-columns:repeat(2,1fr);padding:10px;align-content:start}.metric{padding:16px;border-bottom:1px solid var(--line)}.metric:nth-child(odd){border-right:1px solid var(--line)}.metric:nth-last-child(-n+2){border-bottom:0}.metric b{display:block;font-size:26px;letter-spacing:-.05em}small{color:var(--muted);display:block;margin-top:4px;line-height:1.35}.section{padding:30px 0 6px}.section-head{display:flex;gap:12px;align-items:baseline;margin-bottom:14px}.number{font-family:"SFMono-Regular",Consolas,monospace;color:var(--cyan);font-size:12px}h2{font-size:22px;letter-spacing:-.03em;margin:0}.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}.panel{padding:19px}.panel h3{font-size:13px;text-transform:uppercase;letter-spacing:.11em;color:var(--muted);margin:0 0 12px}.run-row{display:flex;justify-content:space-between;gap:16px;padding:11px 0;border-top:1px solid var(--line)}.run-row:first-of-type{border-top:0}.run-row strong{font-size:14px}.run-meta{text-align:right;display:flex;flex-direction:column;align-items:end;gap:3px}.verdict{display:inline-block;padding:3px 7px;border-radius:999px;font-size:11px;font-family:"SFMono-Regular",Consolas,monospace;font-weight:800;letter-spacing:.04em}.verdict.pass{background:rgba(180,243,123,.12);color:var(--lime)}.verdict.fail{background:rgba(255,127,156,.14);color:var(--rose)}.verdict.error,.verdict.unstable{background:rgba(255,189,114,.14);color:var(--orange)}.verdict.inapplicable{background:rgba(173,155,255,.14);color:var(--violet)}.witness{border-left:3px solid var(--cyan);padding-left:16px}.hash{color:var(--cyan);font-family:"SFMono-Regular",Consolas,monospace;word-break:break-all;font-size:12px}.pill{display:inline-block;border:1px solid rgba(101,228,223,.3);background:rgba(101,228,223,.08);color:var(--cyan);padding:5px 8px;border-radius:99px;font-size:11px;margin:3px 3px 0 0}.transition{display:flex;align-items:center;gap:12px;padding:14px;margin-top:12px;background:rgba(101,228,223,.06);border:1px solid rgba(101,228,223,.2);border-radius:10px}.arrow{color:var(--cyan);font-size:22px}.attempt{border-top:1px solid var(--line);padding:11px 0;display:grid;grid-template-columns:minmax(110px,.9fr) 90px 2fr;gap:12px;align-items:start}.attempt:first-of-type{border-top:0}.attempt-pass{color:var(--lime);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.attempt-fail{color:var(--rose);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.attempt-unresolved{color:var(--orange);font-family:"SFMono-Regular",Consolas,monospace;font-size:12px}.evidence-row{display:grid;grid-template-columns:96px 1fr;gap:14px;border-top:1px solid var(--line);padding:12px 0}.evidence-row span{font-family:"SFMono-Regular",Consolas,monospace;font-size:11px;color:var(--cyan);font-weight:700}.evidence-row p{margin:0;line-height:1.45;font-size:14px}.proof{display:grid;grid-template-columns:1fr auto;gap:9px;align-items:center;padding:13px 0;border-top:1px solid var(--line)}.proof:first-of-type{border-top:0}.callout{border:1px solid rgba(180,243,123,.27);background:rgba(180,243,123,.06);padding:17px;border-radius:12px}.callout strong{color:var(--lime);letter-spacing:.05em}button{appearance:none;border:1px solid rgba(101,228,223,.45);background:var(--cyan);color:#071118;padding:11px 14px;border-radius:9px;font:inherit;font-weight:800;cursor:pointer}button:hover{filter:brightness(1.08)}button:disabled{opacity:.7;cursor:wait}.action-row{display:flex;justify-content:space-between;gap:16px;align-items:center;margin-top:18px}.action-status{color:var(--muted);font-size:13px}details{margin-top:14px;border-top:1px solid var(--line);padding-top:12px}summary{cursor:pointer;color:var(--muted);font-size:13px}pre{white-space:pre-wrap;margin:10px 0 0;font:12px/1.45 "SFMono-Regular",Consolas,monospace;color:#b9d2d8}.footer{color:var(--muted);font-size:13px;border-top:1px solid var(--line);margin-top:40px;padding-top:16px;line-height:1.5}.readonly-badge{display:inline-block;margin-top:10px;border:1px solid rgba(180,243,123,.35);border-radius:999px;background:rgba(180,243,123,.08);color:var(--lime);font:700 11px "SFMono-Regular",Consolas,monospace;letter-spacing:.08em;padding:5px 8px}.table-wrap{overflow:auto}.data-table{border-collapse:collapse;width:100%;min-width:520px}.data-table th,.data-table td{text-align:left;vertical-align:top;padding:11px 8px;border-top:1px solid var(--line);font-size:13px}.data-table th{color:var(--muted);font-size:11px;letter-spacing:.08em;text-transform:uppercase}.data-table code{color:#bfe8e7;word-break:break-all}.empty-state{border:1px dashed rgba(255,189,114,.45);border-radius:10px;padding:14px;color:#ffdda6;background:rgba(255,189,114,.05)}@media(max-width:780px){.hero,.grid{grid-template-columns:1fr}.beat-nav{overflow-x:auto;grid-template-columns:repeat(5,115px)}.attempt{grid-template-columns:1fr;gap:4px}.topbar{align-items:flex-start;flex-direction:column}}
`;

export function renderIncidentPageDocumentStart(title: string): string {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>FaultLine - ${escapeHtml(title)}</title>
<style>${incidentPageStyle}
.review-form{display:grid;gap:12px}.review-form label{display:grid;gap:6px;color:var(--muted);font-size:13px}.review-form input,.review-form textarea{width:100%;border:1px solid var(--line);border-radius:8px;background:#09171e;color:var(--ink);padding:10px;font:inherit}.review-form textarea{resize:vertical}.review-form .acknowledgement{display:flex;align-items:flex-start;gap:9px;line-height:1.4}.review-form .acknowledgement input{width:auto;margin-top:3px}.review-notice{border:1px solid rgba(180,243,123,.35);border-radius:10px;background:rgba(180,243,123,.08);color:var(--lime);padding:12px 14px;max-width:720px}
</style>
</head>
<body>
<main class="shell">`;
}

export function renderIncidentPageProductChrome(options: { command: string; notice?: string }): string {
  const notice = options.notice === undefined ? "" : `<div class="notice">${escapeHtml(options.notice)}</div>`;
  return `<header class="topbar"><div class="brand"><span class="brand-mark" aria-hidden="true"></span><span>FaultLine</span></div><div class="command">${escapeHtml(options.command)}</div></header>
<nav class="beat-nav" aria-label="Investigation steps"><span>BREAK</span><span>FIND</span><span>PROVE</span><span>FIX</span><span>PREVENT</span></nav>
${notice}`;
}

export function renderIncidentPageDocumentEnd(options: { footer: string; script?: string }): string {
  const script = options.script === undefined ? "" : options.script;
  return `<footer class="footer">${options.footer}</footer>
</main>
${script}
</body>
</html>`;
}
