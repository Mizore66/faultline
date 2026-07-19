import { createServer } from "node:http";
import { existsSync, lstatSync, readdirSync, readFileSync } from "node:fs";
import { basename, join, resolve } from "node:path";
import { escapeHtml } from "./incident-page.js";
import { loadVerifiedGitProofView } from "./git-proof-view.js";
import { renderGitProofIncidentPage } from "./git-proof-view.js";
import { verifyTurnInvestigationProofBundle } from "./turn-proof-bundle.js";
import type { FaultLineServer } from "./server.js";

export const DEFAULT_UI_HUB_PORT = 4173 as const;

export type DiscoveredFaultLineArtifact = {
  readonly kind: "git-proof-bundle" | "turn-proof-bundle" | "incident-draft" | "ledger";
  readonly id: string;
  readonly path: string;
  readonly label: string;
};

function isDirectory(path: string): boolean {
  try {
    const stat = lstatSync(path);
    return !stat.isSymbolicLink() && stat.isDirectory();
  } catch {
    return false;
  }
}

function isFile(path: string): boolean {
  try {
    const stat = lstatSync(path);
    return !stat.isSymbolicLink() && stat.isFile();
  } catch {
    return false;
  }
}

function looksLikeGitProofBundle(directory: string): boolean {
  return isFile(join(directory, "ROOT.sha256"))
    && isFile(join(directory, "manifest.json"))
    && isFile(join(directory, "investigation.json"));
}

function looksLikeTurnProofBundle(directory: string): boolean {
  if (!looksLikeGitProofBundle(directory)) return false;
  try {
    const manifest = JSON.parse(readFileSync(join(directory, "manifest.json"), "utf8")) as {
      schemaVersion?: string;
    };
    return typeof manifest.schemaVersion === "string" && manifest.schemaVersion.includes("turn-proof");
  } catch {
    return false;
  }
}

function scanBundleRoots(root: string, kindHint: "git" | "turn" | "any"): DiscoveredFaultLineArtifact[] {
  if (!isDirectory(root)) return [];
  const out: DiscoveredFaultLineArtifact[] = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    if (!entry.isDirectory() || entry.name.startsWith(".")) continue;
    const path = join(root, entry.name);
    if (!looksLikeGitProofBundle(path)) continue;
    const isTurn = looksLikeTurnProofBundle(path);
    if (kindHint === "git" && isTurn) continue;
    if (kindHint === "turn" && !isTurn) continue;
    out.push({
      kind: isTurn ? "turn-proof-bundle" : "git-proof-bundle",
      id: entry.name,
      path,
      label: entry.name
    });
  }
  return out;
}

/** Discover local FaultLine artifacts under `.faultline` for the hub index. */
export function discoverFaultLineArtifacts(repository: string): readonly DiscoveredFaultLineArtifact[] {
  const faultline = join(resolve(repository), ".faultline");
  if (!isDirectory(faultline)) return [];

  const artifacts: DiscoveredFaultLineArtifact[] = [];
  artifacts.push(...scanBundleRoots(join(faultline, "git-proof-bundles"), "git"));
  artifacts.push(...scanBundleRoots(join(faultline, "turn-proof-bundles"), "turn"));
  artifacts.push(...scanBundleRoots(join(faultline, "bundles"), "any"));
  // Nested managed proof roots used by writers.
  for (const nested of ["proofs", "git-proofs", "turn-proofs"]) {
    artifacts.push(...scanBundleRoots(join(faultline, nested), "any"));
  }

  const drafts = join(faultline, "incidents", "drafts");
  if (isDirectory(drafts)) {
    for (const entry of readdirSync(drafts, { withFileTypes: true })) {
      if (!entry.isFile() || !entry.name.endsWith(".json")) continue;
      const path = join(drafts, entry.name);
      if (!isFile(path)) continue;
      artifacts.push({
        kind: "incident-draft",
        id: basename(entry.name, ".json"),
        path,
        label: basename(entry.name, ".json")
      });
    }
  }

  const recordings = join(faultline, "recordings");
  if (isDirectory(recordings)) {
    for (const entry of readdirSync(recordings, { withFileTypes: true })) {
      if (!entry.isFile() || !entry.name.endsWith(".json")) continue;
      if (entry.name.includes("turn-snapshot-cache")) continue;
      const path = join(recordings, entry.name);
      if (!isFile(path)) continue;
      artifacts.push({
        kind: "ledger",
        id: basename(entry.name, ".json"),
        path,
        label: entry.name
      });
    }
  }

  // De-dupe by absolute path.
  const seen = new Set<string>();
  return artifacts.filter((artifact) => {
    const key = resolve(artifact.path);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function renderHubIndex(repository: string, artifacts: readonly DiscoveredFaultLineArtifact[]): string {
  const rows = artifacts.length === 0
    ? `<p class="empty">No bundles, incidents, or ledgers found under <code>.faultline</code>.</p>`
    : `<ul class="catalog">${artifacts.map((artifact, index) => {
      const href = artifact.kind === "incident-draft" || artifact.kind === "ledger"
        ? `/raw/${index}`
        : `/artifact/${index}`;
      return `<li><a href="${href}"><strong>${escapeHtml(artifact.kind)}</strong> — ${escapeHtml(artifact.label)}</a><small>${escapeHtml(artifact.path)}</small></li>`;
    }).join("")}</ul>`;

  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>FaultLine UI hub</title>
<style>
body{font-family:ui-sans-serif,system-ui,sans-serif;margin:0;background:#f6f4ef;color:#1c1917}
main{max-width:52rem;margin:0 auto;padding:2.5rem 1.25rem 4rem}
h1{font-size:1.75rem;margin:0 0 .35rem}
.lede{color:#57534e;margin:0 0 1.5rem}
.catalog{list-style:none;padding:0;margin:0;display:grid;gap:.75rem}
.catalog li{background:#fff;border:1px solid #e7e5e4;padding:1rem 1.1rem}
.catalog a{color:#0c0a09;text-decoration:none;display:block}
.catalog small{display:block;margin-top:.35rem;color:#78716c;word-break:break-all}
.empty{background:#fff;border:1px solid #e7e5e4;padding:1rem}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
</style></head>
<body><main>
<h1>FaultLine UI</h1>
<p class="lede">Local hub for <code>${escapeHtml(repository)}</code>. Open a discovered artifact to reuse the existing read-only page renderer.</p>
${rows}
</main></body></html>`;
}

function renderJsonPage(path: string): string {
  const body = readFileSync(path, "utf8");
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>${escapeHtml(basename(path))}</title>
<style>body{margin:0;background:#111;color:#e7e5e4;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
pre{margin:0;padding:1.25rem;white-space:pre-wrap;word-break:break-word}</style></head>
<body><pre>${escapeHtml(body)}</pre></body></html>`;
}

async function renderArtifactPage(artifact: DiscoveredFaultLineArtifact): Promise<string> {
  if (artifact.kind === "turn-proof-bundle") {
    const verification = await verifyTurnInvestigationProofBundle(artifact.path);
    if (!verification.valid) {
      return `<!doctype html><html><body><h1>Turn package failed verification</h1><pre>${escapeHtml(verification.errors.join("\n"))}</pre></body></html>`;
    }
    // Turn packages reuse the JSON raw view when a dedicated HTML page is unavailable.
    return renderJsonPage(join(artifact.path, "investigation.json"));
  }
  const proof = loadVerifiedGitProofView(artifact.path);
  return renderGitProofIncidentPage(proof);
}

export async function startFaultLineUiHub(options: {
  repository: string;
  port?: number;
}): Promise<FaultLineServer & { artifacts: readonly DiscoveredFaultLineArtifact[] }> {
  const repository = resolve(options.repository);
  const artifacts = discoverFaultLineArtifacts(repository);
  const server = createServer((request, response) => {
    void (async () => {
      try {
        const url = new URL(request.url ?? "/", "http://127.0.0.1");
        if (request.method === "GET" && url.pathname === "/") {
          response.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" });
          response.end(renderHubIndex(repository, artifacts));
          return;
        }
        const artifactMatch = /^\/artifact\/(\d+)$/.exec(url.pathname);
        if (request.method === "GET" && artifactMatch) {
          const index = Number(artifactMatch[1]);
          const artifact = artifacts[index];
          if (!artifact || (artifact.kind !== "git-proof-bundle" && artifact.kind !== "turn-proof-bundle")) {
            response.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
            response.end("Artifact not found");
            return;
          }
          const page = await renderArtifactPage(artifact);
          response.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" });
          response.end(page);
          return;
        }
        const rawMatch = /^\/raw\/(\d+)$/.exec(url.pathname);
        if (request.method === "GET" && rawMatch) {
          const index = Number(rawMatch[1]);
          const artifact = artifacts[index];
          if (!artifact || (artifact.kind !== "incident-draft" && artifact.kind !== "ledger")) {
            response.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
            response.end("Artifact not found");
            return;
          }
          response.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" });
          response.end(renderJsonPage(artifact.path));
          return;
        }
        response.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
        response.end("Not found");
      } catch (error) {
        response.writeHead(500, { "Content-Type": "text/plain; charset=utf-8" });
        response.end(error instanceof Error ? error.message : String(error));
      }
    })();
  });

  await new Promise<void>((resolveListen, rejectListen) => {
    server.once("error", rejectListen);
    server.listen(options.port ?? DEFAULT_UI_HUB_PORT, "127.0.0.1", () => {
      server.off("error", rejectListen);
      resolveListen();
    });
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("FaultLine UI hub did not report a TCP address");
  }
  return {
    url: `http://127.0.0.1:${address.port}`,
    artifacts,
    close: () => new Promise<void>((resolveClose, rejectClose) => {
      server.close((error) => error ? rejectClose(error) : resolveClose());
    })
  };
}

export function faultlineRootExists(repository: string): boolean {
  return existsSync(join(resolve(repository), ".faultline"));
}
