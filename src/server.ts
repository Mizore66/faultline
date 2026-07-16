import { createServer } from "node:http";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createDemoAnalysis } from "./engine.js";
import { renderGitProofIncidentPage, type VerifiedGitProofView } from "./git-proof-view.js";
import { assertSafeProofOutput, writeProofBundle } from "./proof-bundle.js";
import { renderIncidentPage } from "./ui.js";
import type { DemoAnalysis } from "./domain.js";

export type FaultLineServer = { url: string; close: () => Promise<void> };

export async function startFaultLineServer(options: { analysis: DemoAnalysis; outputDirectory: string; port?: number }): Promise<FaultLineServer> {
  assertSafeProofOutput(options.outputDirectory);
  let analysis = options.analysis;
  const server = createServer((request, response) => {
    const requestUrl = new URL(request.url ?? "/", "http://127.0.0.1");
    if (request.method === "GET" && requestUrl.pathname === "/") {
      response.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" });
      response.end(renderIncidentPage(analysis));
      return;
    }
    if (request.method === "GET" && requestUrl.pathname === "/api/analysis") {
      response.writeHead(200, { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store" });
      response.end(JSON.stringify(analysis));
      return;
    }
    if (request.method === "POST" && requestUrl.pathname === "/api/rerun") {
      try {
        analysis = createDemoAnalysis("RERUN");
        writeProofBundle(options.outputDirectory, analysis);
        response.writeHead(200, { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store" });
        response.end(JSON.stringify({ ok: true, mode: analysis.mode, generatedAt: analysis.generatedAt }));
      } catch (error) {
        response.writeHead(500, { "Content-Type": "application/json; charset=utf-8" });
        response.end(JSON.stringify({ ok: false, error: error instanceof Error ? error.message : String(error) }));
      }
      return;
    }
    response.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
    response.end("Not found");
  });
  await new Promise<void>((resolveListen, rejectListen) => {
    server.once("error", rejectListen);
    server.listen(options.port ?? 4173, "127.0.0.1", () => {
      server.off("error", rejectListen);
      resolveListen();
    });
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("FaultLine server did not report a TCP address");
  }
  return {
    url: `http://127.0.0.1:${address.port}`,
    close: () => new Promise<void>((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()))
  };
}

/**
 * Serve an already verified portable Git proof package without any mutation or
 * execution route.  In particular, this server intentionally has no rerun
 * endpoint: a viewer should never turn inspection of a retained package into
 * a new, unrecorded execution.
 */
export async function startGitProofServer(options: { proof: VerifiedGitProofView; port?: number }): Promise<FaultLineServer> {
  const page = renderGitProofIncidentPage(options.proof);
  const server = createServer((request, response) => {
    const requestUrl = new URL(request.url ?? "/", "http://127.0.0.1");
    if (request.method === "GET" && requestUrl.pathname === "/") {
      response.writeHead(200, {
        "Content-Type": "text/html; charset=utf-8",
        "Cache-Control": "no-store",
        "Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
        "Referrer-Policy": "no-referrer",
        "X-Content-Type-Options": "nosniff"
      });
      response.end(page);
      return;
    }
    response.writeHead(404, { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" });
    response.end("Not found");
  });
  await new Promise<void>((resolveListen, rejectListen) => {
    server.once("error", rejectListen);
    server.listen(options.port ?? 4173, "127.0.0.1", () => {
      server.off("error", rejectListen);
      resolveListen();
    });
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    await new Promise<void>((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
    throw new Error("FaultLine Git proof server did not report a TCP address");
  }
  return {
    url: `http://127.0.0.1:${address.port}`,
    close: () => new Promise<void>((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()))
  };
}

const launchedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : "";
if (launchedPath === import.meta.url) {
  const server = await startFaultLineServer({ analysis: createDemoAnalysis("REPLAY"), outputDirectory: resolve(".faultline/bundles/judge-demo") });
  process.stdout.write(`FaultLine incident page: ${server.url}\n`);
}
