import { createServer } from "node:http";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createDemoAnalysis } from "./engine.js";
import { writeProofBundle } from "./proof-bundle.js";
import { renderIncidentPage } from "./ui.js";
import type { DemoAnalysis } from "./domain.js";

export type FaultLineServer = { url: string; close: () => Promise<void> };

export async function startFaultLineServer(options: { analysis: DemoAnalysis; outputDirectory: string; port?: number }): Promise<FaultLineServer> {
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

const launchedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : "";
if (launchedPath === import.meta.url) {
  const server = await startFaultLineServer({ analysis: createDemoAnalysis("REPLAY"), outputDirectory: resolve(".faultline/judge-demo") });
  process.stdout.write(`FaultLine incident page: ${server.url}\n`);
}
