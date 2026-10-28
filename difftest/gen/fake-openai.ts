// The writer replay's fake OpenAI Responses server (slice 4 spec 2.3). A
// template's "fakeOpenAI": "<ws-relative reply file>" starts one for the
// case on 127.0.0.1:0, and the case runs with
// FAULTLINE_TEST_OPENAI_URL=http://127.0.0.1:<port>/v1/responses (TS
// reaches it through fetch-shim.mjs). For each request it appends one record
// to the case's argv log: "openai", the method, the request target, the
// Authorization and Content-Type headers ("" when absent) and the request
// body, joined by \x1f and terminated by \x1e. It then answers from the
// reply file, read at request time: {"status": <int>, "contentType":
// "<header>", "body": "<raw response text>"}, the body sent as UTF-8. It
// reads no clock (no Date header). The Go twin is startFakeOpenAI in
// difftest/fakeopenai.go.
import { appendFileSync, readFileSync } from "node:fs";
import { createServer } from "node:http";
import { join } from "node:path";

export type FakeOpenAI = { url: string; close: () => Promise<void> };

type Reply = { status: number; contentType?: string; body: string };

export function startFakeOpenAI(ws: string, replyFile: string, argvLog: string): Promise<FakeOpenAI> {
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (chunk: Buffer) => chunks.push(chunk));
    req.on("end", () => {
      const fields = ["openai", req.method ?? "", req.url ?? "", req.headers.authorization ?? "", req.headers["content-type"] ?? ""];
      // One write per record, as the recorders do.
      appendFileSync(argvLog, Buffer.concat([Buffer.from(`${fields.join("\x1f")}\x1f`, "utf8"), ...chunks, Buffer.from("\x1e", "utf8")]));
      let reply: Reply;
      try {
        reply = JSON.parse(readFileSync(join(ws, replyFile), "utf8")) as Reply;
      } catch {
        reply = { status: 500, contentType: "text/plain", body: `fake openai: cannot read reply file ${replyFile}` };
      }
      const body = Buffer.from(reply.body, "utf8");
      res.sendDate = false;
      const headers: Record<string, string | number> = { "Content-Length": body.length };
      if (reply.contentType !== undefined) headers["Content-Type"] = reply.contentType;
      res.writeHead(reply.status, headers);
      res.end(body);
    });
  });
  return new Promise((resolveStart, rejectStart) => {
    server.once("error", rejectStart);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address() as { port: number };
      resolveStart({
        url: `http://127.0.0.1:${port}/v1/responses`,
        close: () => new Promise((done) => { server.closeAllConnections(); server.close(() => done()); })
      });
    });
  });
}
