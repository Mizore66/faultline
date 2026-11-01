// Captures one case from the frozen TS implementation. Views call the actual
// loadVerifiedGitProofView/renderGitProofIncidentPage; CLI cases launch the
// actual `node dist/cli.js`. Every case runs against a fresh owned copy of its
// retained fixture, so capture never mutates the corpus or the staging tree.
//
// Normalization is deliberately narrow and documented:
//  - the copy's case root is rewritten to the token {ROOT} in view error
//    messages and in process stdout/stderr (never in artifacts or HTML);
//  - for cli-serve only, the startup URL's port is rewritten to {PORT} AFTER the
//    concrete URL has been validated and the page fetched from it;
//  - for the occupied-port case only, the harness-chosen port is rewritten to
//    {PORT} in the substring "127.0.0.1:<port>" of the diagnostics.
// No other number, digest or string is rewritten. HTTP Date is not captured.
import { spawn, type ChildProcess, type SpawnOptions } from "node:child_process";
import { cpSync, mkdtempSync, readdirSync, readFileSync, lstatSync } from "node:fs";
import { request } from "node:http";
import { createServer, type Server } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { loadVerifiedGitProofView, renderGitProofIncidentPage } from "../../src/git-proof-view.js";
import type { ServeBundleCase, ServeBundleGolden } from "./serve-bundle-cases.js";

const PROCESS_DEADLINE_MS = 60_000;
const SHUTDOWN_DEADLINE_MS = 30_000;
const CLEANUP_DEADLINE_MS = 15_000;
const STARTUP_LINE = /^FaultLine read-only Git proof page: http:\/\/127\.0\.0\.1:([0-9]+)$/;

export type CapturedCase = { golden: ServeBundleGolden; pageBytes: Buffer | null; scratch: string };

export function pageFileName(id: string): string {
  return `${id}.html`;
}

/** Copies the retained fixture into fresh owned scratch and returns the copy's root. */
export function copyFixtureToScratch(corpusRoot: string, fixture: string): { scratch: string; fixtureRoot: string } {
  const scratch = mkdtempSync(join(tmpdir(), "faultline-serve-capture-"));
  const fixtureRoot = join(scratch, "case");
  const source = join(resolve(corpusRoot), "inputs", fixture);
  assertNoLinks(source);
  cpSync(source, fixtureRoot, { recursive: true, errorOnExist: true, force: false, dereference: false, verbatimSymlinks: true });
  return { scratch, fixtureRoot };
}

function assertNoLinks(directory: string): void {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    const stat = lstatSync(path);
    if (stat.isSymbolicLink() || (!stat.isFile() && !stat.isDirectory())) throw new Error(`retained fixture has a link or special file: ${path}`);
    if (stat.isDirectory()) assertNoLinks(path);
  }
}

const withRoot = (text: string, caseRoot: string): string => text.split(caseRoot).join("{ROOT}");

function expandToken(arg: string, caseRoot: string, port: number | null): string {
  if (arg === "{PORT}") {
    if (port === null) throw new Error("{PORT} used without a harness-chosen port");
    return String(port);
  }
  if (arg === "{ROOT}") return caseRoot;
  if (arg.startsWith("{ROOT}/")) return join(caseRoot, arg.slice("{ROOT}/".length));
  return arg;
}

function viewOptions(c: ServeBundleCase, caseRoot: string): NonNullable<Parameters<typeof loadVerifiedGitProofView>[2]> {
  const o = c.options;
  return {
    ...(o.minimizationFile === undefined ? {} : { minimizationFile: join(caseRoot, o.minimizationFile) }),
    ...(o.expectedMinimizationDigest === undefined ? {} : { expectedMinimizationDigest: o.expectedMinimizationDigest }),
    ...(o.repairDirectory === undefined ? {} : { repairDirectory: join(caseRoot, o.repairDirectory) }),
    ...(o.expectedRepairDigest === undefined ? {} : { expectedRepairDigest: o.expectedRepairDigest }),
    ...(o.preventionDirectory === undefined ? {} : { preventionDirectory: join(caseRoot, o.preventionDirectory) }),
    ...(o.expectedPreventionDigest === undefined ? {} : { expectedPreventionDigest: o.expectedPreventionDigest })
  };
}

/** The golden always comes from the frozen implementation. */
export function captureView(directory: string, expectedRoot: string | undefined,
  options: Parameters<typeof loadVerifiedGitProofView>[2]): { valueJSON: string; html: string } {
  const view = loadVerifiedGitProofView(directory, expectedRoot, options);
  return { valueJSON: JSON.stringify(view), html: renderGitProofIncidentPage(view) };
}

type Finished = { stdout: string; stderr: string; exitCode: number | null; signal: string | null };

/**
 * Invocation-local test-support seam (never a global or environment switch).
 * Defaults are the real behavior: node:child_process spawn and the production
 * deadlines. Tests pass a different spawn function or shorter deadlines.
 */
export type CaptureHooks = {
  spawnFn?: (file: string, args: string[], options: SpawnOptions) => ChildProcess;
  processDeadlineMs?: number;
  shutdownDeadlineMs?: number;
  cleanupDeadlineMs?: number;
};

type OwnedCli = {
  child: ChildProcess;
  out: { stdout: string; stderr: string };
  done: Promise<Finished>;
  hooks: Required<CaptureHooks>;
};

const resolveHooks = (hooks: CaptureHooks): Required<CaptureHooks> => ({
  spawnFn: hooks.spawnFn ?? ((file, args, options) => spawn(file, args, options)),
  processDeadlineMs: hooks.processDeadlineMs ?? PROCESS_DEADLINE_MS,
  shutdownDeadlineMs: hooks.shutdownDeadlineMs ?? SHUTDOWN_DEADLINE_MS,
  cleanupDeadlineMs: hooks.cleanupDeadlineMs ?? CLEANUP_DEADLINE_MS
});

function startCli(argv: string[], cwd: string, hooks: Required<CaptureHooks>): OwnedCli {
  const cli = resolve("dist/cli.js");
  const child = hooks.spawnFn(process.execPath, [cli, ...argv], { cwd, stdio: ["ignore", "pipe", "pipe"], env: { ...process.env, FAULTLINE_NO_BROWSER: "1" } });
  const out = { stdout: "", stderr: "" };
  child.stdout?.setEncoding("utf8").on("data", (chunk: string) => { out.stdout += chunk; });
  child.stderr?.setEncoding("utf8").on("data", (chunk: string) => { out.stderr += chunk; });
  const done = new Promise<Finished>((resolveDone, rejectDone) => {
    child.once("error", rejectDone);
    child.once("close", (exitCode, signal) => resolveDone({ stdout: out.stdout, stderr: out.stderr, exitCode, signal }));
  });
  // A rejection (for example a spawn error) is always surfaced by whoever awaits
  // `done`; this branch only guarantees it is never an unhandled rejection.
  done.catch(() => undefined);
  return { child, out, done, hooks };
}

/** Kill if still running, then await the child's close within a bounded deadline. */
async function reap(owned: OwnedCli): Promise<void> {
  const { child, done, hooks } = owned;
  if (child.pid === undefined) return; // never started (spawn error)
  if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
  let timer: NodeJS.Timeout | undefined;
  const expired = new Promise<"expired">((ok) => { timer = setTimeout(() => ok("expired"), hooks.cleanupDeadlineMs); });
  try {
    const outcome = await Promise.race([done.then(() => "closed" as const, () => "closed" as const), expired]);
    if (outcome === "expired") throw new Error(`owned CLI child ${child.pid} was not reaped within ${hooks.cleanupDeadlineMs} ms`);
  } finally {
    if (timer !== undefined) clearTimeout(timer);
  }
}

const describe = (error: unknown): string => (error instanceof Error ? error.message : String(error));

/** Runs a body against an owned child; the child is always reaped and neither error is masked. */
async function withOwnedCli<T>(owned: OwnedCli, body: () => Promise<T>): Promise<T> {
  let value: T | undefined;
  let failed = false;
  let original: unknown;
  try { value = await body(); } catch (error) { failed = true; original = error; }
  try {
    await reap(owned);
  } catch (cleanupError) {
    if (!failed) throw cleanupError;
    throw new Error(`${describe(original)}; additionally ${describe(cleanupError)}`, { cause: original });
  }
  if (failed) throw original;
  return value as T;
}

async function within<T>(promise: Promise<T>, ms: number, label: string, kill: () => void): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_, reject) => { timer = setTimeout(() => { kill(); reject(new Error(`${label} did not finish within ${ms} ms`)); }, ms); });
  try { return await Promise.race([promise, timeout]); } finally { if (timer !== undefined) clearTimeout(timer); }
}

function fetchPage(port: number): Promise<{ status: number; contentType: string; body: Buffer }> {
  return new Promise((resolveFetch, rejectFetch) => {
    const req = request({ host: "127.0.0.1", port, path: "/", method: "GET", agent: false, headers: { Connection: "close" } }, (res) => {
      const chunks: Buffer[] = [];
      res.on("data", (chunk: Buffer) => chunks.push(chunk));
      res.on("end", () => resolveFetch({ status: res.statusCode ?? 0, contentType: String(res.headers["content-type"] ?? ""), body: Buffer.concat(chunks) }));
      res.on("error", rejectFetch);
    });
    req.setTimeout(30_000, () => req.destroy(new Error("page fetch timed out")));
    req.on("error", rejectFetch);
    req.end();
  });
}

async function occupyPort(): Promise<{ server: Server; port: number }> {
  const server = createServer();
  await new Promise<void>((ok, fail) => { server.once("error", fail); server.listen(0, "127.0.0.1", ok); });
  const address = server.address();
  if (address === null || typeof address === "string") throw new Error("could not occupy a port");
  return { server, port: address.port };
}

async function captureCliError(c: ServeBundleCase, caseRoot: string, hooks: Required<CaptureHooks>): Promise<ServeBundleGolden> {
  let occupied: { server: Server; port: number } | null = null;
  if (c.argv.includes("{PORT}")) occupied = await occupyPort();
  try {
    const argv = c.argv.map((arg) => expandToken(arg, caseRoot, occupied?.port ?? null));
    const owned = startCli(argv, caseRoot, hooks);
    const result = await withOwnedCli(owned, async () => {
      const finished = await within(owned.done, hooks.processDeadlineMs, `${c.id} process`, () => owned.child.kill("SIGKILL"));
      if (finished.signal !== null) throw new Error(`${c.id}: process ended by signal ${finished.signal}`);
      return finished;
    });
    const normalize = (text: string): string => {
      let value = withRoot(text, caseRoot);
      if (occupied !== null) value = value.split(`127.0.0.1:${occupied.port}`).join("127.0.0.1:{PORT}");
      return value;
    };
    return {
      id: c.id, valueJSON: null, error: null, page: null,
      process: { stdout: normalize(result.stdout), stderr: normalize(result.stderr), exitCode: result.exitCode, signal: null }
    };
  } finally {
    if (occupied !== null) await new Promise<void>((ok) => occupied!.server.close(() => ok()));
  }
}

async function captureCliServe(c: ServeBundleCase, caseRoot: string, hooks: Required<CaptureHooks>): Promise<{ golden: ServeBundleGolden; page: Buffer }> {
  const argv = c.argv.map((arg) => expandToken(arg, caseRoot, null));
  const owned = startCli(argv, caseRoot, hooks);
  const { child, out, done } = owned;
  const kill = (): void => { child.kill("SIGKILL"); };
  return withOwnedCli(owned, async () => {
    // Wait for the exact two startup lines, or an early exit (which is a capture failure).
    const startup = await within(new Promise<string[]>((ok, fail) => {
      const check = (): void => {
        const lines = out.stdout.split("\n");
        if (lines.length >= 3 && lines[2] === "") ok(lines.slice(0, 2));
      };
      child.stdout?.on("data", check);
      // Both branches are handled so a spawn error cannot become an unhandled rejection.
      done.then(
        (r) => fail(new Error(`${c.id}: process exited before startup completed: ${JSON.stringify(r)}`)),
        (error: unknown) => fail(error)
      );
    }), hooks.processDeadlineMs, `${c.id} startup`, kill);
    if (out.stdout !== `${startup[0]}\n${startup[1]}\n`) throw new Error(`${c.id}: unexpected extra startup output ${JSON.stringify(out.stdout)}`);
    const match = STARTUP_LINE.exec(startup[0]!);
    if (match === null) throw new Error(`${c.id}: first startup line is not the concrete URL: ${JSON.stringify(startup[0])}`);
    const port = Number(match[1]);
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error(`${c.id}: startup port ${match[1]} out of range`);
    if (startup[1] !== "Press Ctrl+C to stop.") throw new Error(`${c.id}: second startup line is ${JSON.stringify(startup[1])}`);
    // Fetch the concrete URL BEFORE any substitution.
    const page = await fetchPage(port);
    if (page.status !== 200) throw new Error(`${c.id}: page status ${page.status}`);
    if (page.contentType !== "text/html; charset=utf-8") throw new Error(`${c.id}: page content-type ${page.contentType}`);
    if (Buffer.from(page.body.toString("utf8"), "utf8").compare(page.body) !== 0) throw new Error(`${c.id}: page is not valid UTF-8`);
    child.kill("SIGINT");
    const finished = await within(done, hooks.shutdownDeadlineMs, `${c.id} SIGINT shutdown`, kill);
    if (finished.exitCode !== 0 || finished.signal !== null) {
      throw new Error(`${c.id}: shutdown was not a clean completed SIGINT exit: ${JSON.stringify({ exitCode: finished.exitCode, signal: finished.signal })}`);
    }
    // Normalize only the proven startup URL's port, then only the declared case root.
    const stdout = withRoot(finished.stdout.replace(`http://127.0.0.1:${port}`, "http://127.0.0.1:{PORT}"), caseRoot);
    const stderr = withRoot(finished.stderr, caseRoot);
    return {
      golden: { id: c.id, valueJSON: null, error: null, page: pageFileName(c.id), process: { stdout, stderr, exitCode: 0, signal: null } },
      page: page.body
    };
  });
}

export async function captureServeBundleCaseDetailed(corpusRoot: string, c: ServeBundleCase, hookOptions: CaptureHooks = {}): Promise<CapturedCase> {
  const hooks = resolveHooks(hookOptions);
  const { scratch, fixtureRoot } = copyFixtureToScratch(corpusRoot, c.fixture);
  const bundle = join(fixtureRoot, "bundle");
  if (c.kind === "view") {
    try {
      const { valueJSON, html } = captureView(bundle, c.expectedRoot ?? undefined, viewOptions(c, fixtureRoot));
      return { golden: { id: c.id, valueJSON, error: null, page: pageFileName(c.id), process: null }, pageBytes: Buffer.from(html, "utf8"), scratch };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return { golden: { id: c.id, valueJSON: null, error: withRoot(message, fixtureRoot), page: null, process: null }, pageBytes: null, scratch };
    }
  }
  if (c.kind === "cli-error") return { golden: await captureCliError(c, fixtureRoot, hooks), pageBytes: null, scratch };
  const served = await captureCliServe(c, fixtureRoot, hooks);
  return { golden: served.golden, pageBytes: served.page, scratch };
}

export async function captureServeBundleCase(root: string, c: ServeBundleCase): Promise<ServeBundleGolden> {
  return (await captureServeBundleCaseDetailed(root, c)).golden;
}

export function readPage(corpusRoot: string, name: string): Buffer {
  return readFileSync(join(resolve(corpusRoot), "pages", name));
}
