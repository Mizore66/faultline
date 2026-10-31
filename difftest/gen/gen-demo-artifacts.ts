// Frozen-TS capture/evidence generator for the Go Slice 5b.1 demo-artifacts oracle.
//   gen-demo-artifacts.ts             capture once; refuses to overwrite existing evidence
//   gen-demo-artifacts.ts --check     validate committed evidence only; writes nothing
//   gen-demo-artifacts.ts --recapture re-capture in fresh roots and compare to committed bytes; writes nothing
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, realpathSync, rmSync, statSync, writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

export type Request = {
  id: string;
  kind: "analysis" | "writer" | "render" | "cli";
  base?: "replay" | "rerun" | "unicode" | "lone-surrogate";
  mutation?: "extra-keys" | "missing-catalog" | "duplicate-id" | "sanitized-collision" |
    "no-boundary" | "escape-fallback" | "non-replay";
  setup?: "parent-file" | "output-file" | "output-directory" | "existing-preview" | "invalid-bundle" |
    "existing-bundle" | "linked-root" | "linked-parent" | "linked-output" | "dangling-output" | "linked-bundle-file";
  output?: string;
  proofRoot?: string;
  argv?: string[];
};
type Entry =
  | { path: string; kind: "directory" }
  | { path: string; kind: "file"; base64: string }
  | { path: string; kind: "symlink"; target: string };
type Capture = {
  schema: "faultline.demo-artifacts-case.v1";
  request: Request;
  call: { valueJSON: string | null; error: string | null } | null;
  process: { stdout: string; stderr: string; exitCode: number } | null;
  before: Entry[];
  after: Entry[];
};

const baseline = {
  schema: "faultline.demo-artifacts-oracle.v1",
  sourceCommit: "ce99a793af0b020bf1b9cf21114f8d7897b37207",
  manifestCommit: "647440884a6668042f7726f2cbade38886d00a41",
  manifestVersion: "0.1.4",
  nodeVersion: "22.22.2",
  cachedRuntimeVersion: "v22.22.2",
} as const;
const environmentDigest = "sha256:7de997bc873dc0e1909a6aa3bf7a5a40fe4958fbb0bb66b3c624e4c75074d334";
const replayRootDigest = "sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa";
const tempPlaceholder = "<TMP>";

const bundleOut = ".faultline/bundles/demo";
const w = (id: string, r: Omit<Request, "id" | "kind"> = {}): Request => ({ id, kind: "writer", ...r });
const c = (id: string, argv: string[], r: Omit<Request, "id" | "kind" | "argv"> = {}): Request =>
  ({ id, kind: "cli", argv, ...r });
const outHTML = (o = "out.html") => ["--output", o];

// The single explicit request table; the generated records carry these for Go replay/live reuse.
export const requests: Request[] = [
  { id: "analysis-replay", kind: "analysis" },
  { id: "render-default", kind: "render" },
  { id: "render-replay", kind: "render" },
  { id: "render-rerun-refused", kind: "render", mutation: "non-replay" },
  { id: "render-no-boundary", kind: "render", mutation: "no-boundary" },
  { id: "render-escape-fallback", kind: "render", mutation: "escape-fallback" },
  w("writer-replay"),
  w("writer-rerun", { base: "rerun" }),
  w("writer-unicode", { base: "unicode" }),
  w("writer-lone-surrogate", { base: "lone-surrogate" }),
  w("writer-extra-keys", { mutation: "extra-keys" }),
  w("writer-replace", { base: "rerun", setup: "existing-bundle" }),
  w("writer-custom-root", { proofRoot: "managed", output: "managed/demo" }),
  w("writer-empty-root", { proofRoot: "", output: "demo" }),
  w("writer-missing-catalog", { mutation: "missing-catalog" }),
  w("writer-duplicate-id", { mutation: "duplicate-id" }),
  w("writer-sanitized-collision", { mutation: "sanitized-collision" }),
  w("writer-root", { output: ".faultline/bundles" }),
  w("writer-outside", { output: "outside" }),
  w("writer-parent-file", { output: ".faultline/bundles/blocked/demo", setup: "parent-file" }),
  w("writer-output-file", { setup: "output-file" }),
  w("writer-invalid-existing", { setup: "invalid-bundle" }),
  w("writer-linked-root", { setup: "linked-root" }),
  w("writer-linked-output", { setup: "linked-output" }),
  w("writer-linked-bundle-file", { setup: "linked-bundle-file" }),
  c("cli-default", []),
  c("cli-custom", outHTML("out/custom.html"), { output: "out/custom.html" }),
  c("cli-absolute", outHTML("absolute/out.html"), { output: "absolute/out.html" }),
  c("cli-upper-ext", outHTML("out.HTML"), { output: "out.HTML" }),
  c("cli-hidden-ext", outHTML(".hidden.HTML"), { output: ".hidden.HTML" }),
  c("cli-double-dot-ext", outHTML("..html"), { output: "..html" }),
  c("cli-trailing-output", ["--output"]),
  c("cli-ignored", ["--help", "ignored"]),
  c("cli-equals", ["--output=unused.html"]),
  c("cli-duplicate", ["--output", "first.html", "--output", "second.html"]),
  c("cli-overwrite", outHTML(), { output: "out.html", setup: "existing-preview" }),
  c("cli-empty-output", ["--output", ""]),
  c("cli-next-flag", ["--output", "--other"]),
  c("cli-extension", outHTML("out.txt"), { output: "out.txt" }),
  c("cli-dotfile-no-ext", outHTML(".html"), { output: ".html" }),
  c("cli-parent-file", outHTML("blocked/out.html"), { output: "blocked/out.html", setup: "parent-file" }),
  c("cli-output-directory", outHTML(), { output: "out.html", setup: "output-directory" }),
  c("cli-linked-parent", outHTML("linked/out.html"), { output: "linked/out.html", setup: "linked-parent" }),
  c("cli-linked-output", outHTML(), { output: "out.html", setup: "linked-output" }),
  c("cli-dangling-output", outHTML(), { output: "out.html", setup: "dangling-output" }),
];
void bundleOut;

const repo = resolve(".");
const dir = join(repo, "difftest", "testdata", "demo-artifacts");
const paths = {
  baseline: join(dir, "baseline.json"),
  html: join(dir, "judge-preview.html"),
  golden: join(dir, "golden.jsonl"),
};
const mode = process.argv[2];
assert.ok([undefined, "--check", "--recapture"].includes(mode) && process.argv.length <= 3,
  `usage: gen-demo-artifacts.ts [--check|--recapture]`);

function validateRequests(): void {
  assert.equal(requests.length, 45, "request matrix must have 45 cases");
  assert.equal(new Set(requests.map((r) => r.id)).size, requests.length, "duplicate request id");
}

function baselineText(): string {
  return `${JSON.stringify(baseline, null, 2)}\n`;
}

function validEntries(entries: Entry[], label: string): void {
  assert.ok(Array.isArray(entries), `${label}: inventory is not an array`);
  let previous: Buffer | null = null;
  for (const e of entries) {
    const key = Buffer.from(e.path);
    assert.ok(previous === null || Buffer.compare(previous, key) < 0, `${label}: inventory not byte-sorted/unique at ${e.path}`);
    previous = key;
    if (e.kind === "file") {
      assert.match(e.base64, /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/, `${label}: bad base64 ${e.path}`);
      assert.equal(Buffer.from(e.base64, "base64").toString("base64"), e.base64, `${label}: non-canonical base64 ${e.path}`);
    } else if (e.kind === "symlink") {
      assert.ok(typeof e.target === "string" && e.target !== "" && !e.target.startsWith("/"), `${label}: bad link ${e.path}`);
    } else assert.equal(e.kind, "directory", `${label}: bad kind ${e.path}`);
  }
}

function validateGolden(text: string): Capture[] {
  assert.ok(text.endsWith("\n") && text.length > 1, "golden.jsonl is empty or lacks a final newline");
  const rows = text.slice(0, -1).split("\n").map((line, i) => {
    const row: Capture = JSON.parse(line);
    assert.equal(row.schema, "faultline.demo-artifacts-case.v1", `line ${i + 1}: schema`);
    const id = row.request.id;
    if (row.request.kind === "cli") {
      assert.ok(row.call === null && row.process !== null, `${id}: cli needs process only`);
      assert.ok(Number.isInteger(row.process.exitCode), `${id}: exit`);
      assert.ok(typeof row.process.stdout === "string" && typeof row.process.stderr === "string", `${id}: streams`);
      assert.ok(row.process.stdout.length + row.process.stderr.length > 0, `${id}: empty process capture`);
    } else {
      assert.ok(row.process === null && row.call !== null, `${id}: library needs call only`);
      assert.ok((row.call.valueJSON === null) !== (row.call.error === null), `${id}: exactly one of value/error`);
      assert.ok((row.call.valueJSON ?? row.call.error!).length > 0, `${id}: empty call capture`);
    }
    validEntries(row.before, `${id} before`);
    validEntries(row.after, `${id} after`);
    return row;
  });
  const ids = rows.map((r) => r.request.id);
  assert.equal(new Set(ids).size, ids.length, "duplicate case in golden.jsonl");
  assert.deepEqual(ids, requests.map((r) => r.id), "request-ID set/order differs from the generator table");
  rows.forEach((row, i) => assert.deepEqual(row.request, requests[i], `${row.request.id}: request differs from table`));
  assert.equal(text, rows.map((r) => JSON.stringify(r)).join("\n") + "\n", "golden.jsonl is not canonical JSONL");
  return rows;
}

function check(): void {
  validateRequests();
  assert.ok(existsSync(paths.baseline), "absent capture evidence: baseline.json");
  assert.ok(existsSync(paths.golden), "absent capture evidence: golden.jsonl");
  assert.ok(existsSync(paths.html), "absent capture evidence: judge-preview.html");
  assert.equal(readFileSync(paths.baseline, "utf8"), baselineText(), "baseline.json differs from the pinned metadata");
  const rows = validateGolden(readFileSync(paths.golden, "utf8"));
  const html = readFileSync(paths.html);
  assert.ok(html.length > 0, "judge-preview.html is empty");
  const byId = new Map(rows.map((r) => [r.request.id, r]));
  const rendered: unknown = JSON.parse(byId.get("render-default")!.call!.valueJSON!);
  assert.equal(html.toString("utf8"), rendered, "judge-preview.html differs from render-default capture");
  console.log(`demo-artifacts evidence valid: ${rows.length} cases`);
}

function newestMtime(d: string): number {
  let newest = 0;
  for (const entry of readdirSync(d, { withFileTypes: true })) {
    const p = join(d, entry.name);
    newest = Math.max(newest, entry.isDirectory() ? newestMtime(p) : statSync(p).mtimeMs);
  }
  return newest;
}

function preflight(): string {
  assert.equal(process.versions.node, baseline.nodeVersion, "demo-artifacts oracle Node drift");
  const cli = join(repo, "dist", "cli.js");
  assert.ok(existsSync(cli) && statSync(cli).mtimeMs >= newestMtime(join(repo, "src")),
    "dist/cli.js is missing or older than src/: run pnpm build first");
  const manifest: { version?: unknown } = JSON.parse(readFileSync(join(repo, "package.json"), "utf8"));
  assert.equal(manifest.version, baseline.manifestVersion, "demo-artifacts manifest drift");
  const version = spawnSync(process.execPath, [cli, "--version"], { cwd: repo, encoding: "utf8", timeout: 10000 });
  assert.deepEqual({ stdout: version.stdout, stderr: version.stderr, exit: version.status },
    { stdout: "FaultLine 0.1.4\n", stderr: "", exit: 0 }, "demo-artifacts CLI version drift");
  return cli;
}

const seedPath = join(repo, "difftest", "gen", "seed.mjs");
const adapterPath = join(repo, "difftest", "gen", "demo-artifacts-oracle.mjs");

function mask(value: string, roots: string[]): string {
  let out = value;
  for (const r of roots) out = out.split(r).join(tempPlaceholder);
  return out;
}

function captureAll(scratch: string): Capture[] {
  const rows: Capture[] = [];
  for (const request of requests) {
    const caseDir = join(scratch, request.id);
    const root = join(caseDir, "root");
    mkdirSync(root, { recursive: true });
    const requestFile = join(caseDir, "request.json");
    writeFileSync(requestFile, JSON.stringify(request));
    const realRoot = realpathSync(root);
    const child = spawnSync(process.execPath, ["--import", seedPath, adapterPath, requestFile, realRoot], {
      cwd: caseDir, encoding: "utf8", timeout: 120000, maxBuffer: 256 * 1024 * 1024,
      env: { ...process.env, FAULTLINE_TEST_SEED: "demo-artifacts-v1" },
    });
    assert.equal(child.error, undefined, `${request.id}: adapter spawn`);
    assert.equal(child.status, 0, `${request.id}: adapter failed: ${child.stderr}`);
    const row: Capture = JSON.parse(child.stdout);
    const roots = [...new Set([realRoot, root])];
    // Only root prefixes in call errors, returned directory fields and CLI streams are masked.
    if (row.call?.error) row.call.error = mask(row.call.error, roots);
    if (row.call?.valueJSON && request.kind === "writer") {
      // Only the returned directory field carries a case-root prefix; other fields are untouched.
      const value: { directory: string; manifestDigest: string; rootDigest: string } = JSON.parse(row.call.valueJSON);
      const directory = mask(value.directory, roots);
      assert.notEqual(directory, value.directory, `${request.id}: directory has no case-root prefix`);
      assert.deepEqual(Object.keys(value), ["directory", "manifestDigest", "rootDigest"], `${request.id}: writer result shape`);
      row.call.valueJSON = JSON.stringify({ ...value, directory });
    }
    if (row.process) {
      row.process.stdout = mask(row.process.stdout, roots);
      row.process.stderr = mask(row.process.stderr, roots);
    }
    const leaked = JSON.stringify([row.call?.valueJSON, ...[...row.before, ...row.after].map((e) => e.kind === "file" ? Buffer.from(e.base64, "base64").toString("latin1") : e.kind === "symlink" ? e.target : "")]);
    assert.ok(!roots.some((r) => leaked.includes(JSON.stringify(r).slice(1, -1))), `${request.id}: temporary root leaked into unmasked bytes`);
    assert.deepEqual(row.request, request, `${request.id}: adapter echoed a different request`);
    rows.push({ schema: "faultline.demo-artifacts-case.v1", ...row, request });
  }
  return rows;
}

function reportReference(rows: Capture[]): { html: string } {
  const byId = new Map(rows.map((r) => [r.request.id, r]));
  const render = JSON.parse(byId.get("render-default")!.call!.valueJSON!) as string;
  assert.ok(render.length > 0);
  const cli = byId.get("cli-default")!;
  const written = cli.after.find((e) => e.path === "docs/judge-preview.html");
  assert.ok(written?.kind === "file" && Buffer.from(written.base64, "base64").toString("utf8") === render,
    "cli-default preview bytes differ from render-default");
  // Retained reference: writer-replay subtree against the unchanged replay base, byte for byte.
  const base = join(repo, "difftest", "testdata", "bases", "demo-replay");
  const expected: Entry[] = [];
  const walk = (d: string, prefix: string) => {
    for (const name of readdirSync(d)) {
      const p = join(d, name);
      const rel = prefix === "" ? name : `${prefix}/${name}`;
      if (statSync(p).isDirectory()) { expected.push({ path: rel, kind: "directory" }); walk(p, rel); }
      else expected.push({ path: rel, kind: "file", base64: readFileSync(p).toString("base64") });
    }
  };
  walk(base, "");
  const prefix = ".faultline/bundles/demo/";
  const actual = byId.get("writer-replay")!.after.filter((e) => e.path.startsWith(prefix))
    .map((e) => ({ ...e, path: e.path.slice(prefix.length) }));
  const key = (e: Entry) => Buffer.from(e.path);
  expected.sort((a, b) => Buffer.compare(key(a), key(b)));
  assert.equal(expected.filter((e) => e.kind === "file").length, 69, "retained base is not 69 files");
  assert.deepEqual(actual, expected, "writer-replay differs from retained demo-replay base");
  const value: { rootDigest: string } = JSON.parse(byId.get("writer-replay")!.call!.valueJSON!);
  assert.equal(value.rootDigest, replayRootDigest, "writer-replay root digest drift");
  const analysis: { prevention: { firstBad: { environmentDigest: string } } } =
    JSON.parse(byId.get("analysis-replay")!.call!.valueJSON!);
  assert.equal(analysis.prevention.firstBad.environmentDigest, environmentDigest, "environment digest drift");
  const docs = join(repo, "docs", "judge-preview.html");
  const same = existsSync(docs) && readFileSync(docs, "utf8") === render;
  console.log(same ? "docs/judge-preview.html: byte-identical to captured default preview"
    : "docs/judge-preview.html: MISMATCH against captured default preview (retained, not replaced)");
  const dangling = byId.get("cli-dangling-output")!;
  console.log(`cli-dangling-output TS: exit=${dangling.process!.exitCode} stderr=${JSON.stringify(dangling.process!.stderr)} after=${JSON.stringify(dangling.after.map((e) => e.path + ":" + e.kind))}`);
  return { html: render };
}

function capture(): Capture[] {
  validateRequests();
  preflight();
  const scratch = mkdtempSync(join(tmpdir(), "demo-artifacts-"));
  try {
    const rows = captureAll(scratch);
    const { html } = reportReference(rows);
    const text = rows.map((r) => JSON.stringify(r)).join("\n") + "\n";
    validateGolden(text);
    if (mode === undefined) {
      // All preflight and capture succeed before any output is touched; never overwrite evidence.
      for (const p of Object.values(paths)) assert.ok(!existsSync(p), `refusing to overwrite existing evidence: ${p}`);
      mkdirSync(dir, { recursive: true });
      writeFileSync(paths.baseline, baselineText(), "utf8");
      writeFileSync(paths.html, html, "utf8");
      writeFileSync(paths.golden, text, "utf8");
      console.log(`wrote ${rows.length} demo-artifacts captures`);
    } else {
      assert.equal(readFileSync(paths.baseline, "utf8"), baselineText(), "recapture: baseline differs");
      assert.equal(readFileSync(paths.html, "utf8"), html, "recapture: HTML differs");
      assert.equal(readFileSync(paths.golden, "utf8"), text, "recapture: golden.jsonl differs");
      console.log(`recapture identical: ${rows.length} cases`);
    }
    return rows;
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

if (mode === "--check") check();
else capture();
