// Pinned-TS capture adapter for the demo-artifacts oracle (Go Slice 5b.1).
// Usage (run with --import seed.mjs): node demo-artifacts-oracle.mjs <request.json> <case-root>
// It performs the request's setup in the fresh, empty case root, runs exactly one real
// frozen-TS call or process, and prints the Capture JSON (unmasked) to stdout.
// It never executes RERUN and never normalizes any byte.
import { spawnSync } from "node:child_process";
import {
  cpSync, lstatSync, mkdirSync, readdirSync, readFileSync, readlinkSync, symlinkSync, unlinkSync, writeFileSync,
} from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const [, , requestPath, rootArg] = process.argv;
if (requestPath === undefined || rootArg === undefined) {
  process.stderr.write("usage: demo-artifacts-oracle.mjs <request.json> <case-root>\n");
  process.exit(2);
}
const request = JSON.parse(readFileSync(requestPath, "utf8"));
const root = resolve(rootArg);
const repo = resolve(fileURLToPath(new URL("../..", import.meta.url)));
process.chdir(root);
const seedPath = fileURLToPath(new URL("./seed.mjs", import.meta.url));

const { createDemoAnalysis } = await import(new URL("../../dist/engine.js", import.meta.url));
const { writeProofBundle } = await import(new URL("../../dist/proof-bundle.js", import.meta.url));
const { renderJudgePreviewPage } = await import(new URL("../../dist/judge-preview.js", import.meta.url));

const bases = join(repo, "difftest", "testdata", "bases");
const baseDirs = {
  rerun: "demo-rerun",
  unicode: "demo-rerun-unicode-stdout",
  "lone-surrogate": "demo-rerun-lone-surrogate-stdout",
};
function parsedBase(name) {
  return JSON.parse(readFileSync(join(bases, name, "analysis.json"), "utf8"));
}
function baseAnalysis() {
  const base = request.base ?? "replay";
  return base === "replay" ? createDemoAnalysis("REPLAY") : parsedBase(baseDirs[base]);
}
function mutate(analysis) {
  switch (request.mutation) {
    case undefined: return analysis;
    case "extra-keys": analysis["z-extra"] = "z-extra"; analysis["a-extra"] = "a-extra"; return analysis;
    case "missing-catalog": analysis.runCatalog.shift(); return analysis;
    case "duplicate-id": analysis.runCatalog.push(structuredClone(analysis.runCatalog[0])); return analysis;
    case "sanitized-collision":
      for (const id of ["collision:", "collision?"]) {
        analysis.runCatalog.push({ ...structuredClone(analysis.runCatalog[0]), id });
      }
      return analysis;
    case "no-boundary": analysis.transitions = []; return analysis;
    case "escape-fallback":
      analysis.witness.behavior = "<>&\"'";
      analysis.claims[0].statement = "<>&\"'";
      analysis.prevention.lastGood.stateId = "<unknown-state>";
      return analysis;
    case "non-replay": analysis.mode = "RERUN"; return analysis;
    default: throw new Error(`unknown mutation ${request.mutation}`);
  }
}

const defaultOutput = request.kind === "writer" ? ".faultline/bundles/demo" : "out.html";
const out = request.output ?? defaultOutput;
const outAbs = join(root, out);
const put = (rel, body) => { mkdirSync(dirname(join(root, rel)), { recursive: true }); writeFileSync(join(root, rel), body); };
const link = (rel, target) => { mkdirSync(dirname(join(root, rel)), { recursive: true }); symlinkSync(target, join(root, rel)); };
const copyBundle = (rel) => { mkdirSync(dirname(join(root, rel)), { recursive: true }); cpSync(join(bases, "demo-replay"), join(root, rel), { recursive: true }); };
const relTo = (fromRel, toRel) => relative(dirname(join(root, fromRel)), join(root, toRel));

// Setup never calls either writer and never consumes the seeded UUID stream.
switch (request.setup) {
  case undefined: break;
  case "parent-file": put(dirname(out), "blocker\n"); break;
  case "output-file": put(out, "old output\n"); break;
  case "output-directory": put(`${out}/sentinel.txt`, "keep\n"); break;
  case "existing-preview": put(out, "old preview\n"); break;
  case "invalid-bundle": put(`${out}/sentinel.txt`, "keep\n"); break;
  case "existing-bundle": copyBundle(out); break;
  case "linked-root":
    mkdirSync(join(root, "actual-managed"), { recursive: true });
    link(".faultline/bundles", "../actual-managed");
    break;
  case "linked-parent":
    mkdirSync(join(root, "actual-parent"), { recursive: true });
    link("linked", "actual-parent");
    break;
  case "linked-output":
    if (request.kind === "writer") { copyBundle("target-bundle"); link(out, relTo(out, "target-bundle")); }
    else { put("target.html", "keep\n"); link(out, relTo(out, "target.html")); }
    break;
  case "dangling-output": link(out, relTo(out, "target.html")); break;
  case "linked-bundle-file": {
    copyBundle(out);
    const file = `${out}/witness/witness.json`;
    put("original-witness.json", readFileSync(join(root, file)));
    unlinkSync(join(root, file));
    symlinkSync(relTo(file, "original-witness.json"), join(root, file));
    break;
  }
  default: throw new Error(`unknown setup ${request.setup}`);
}

function inventory() {
  const entries = [];
  const walk = (dir, prefix) => {
    for (const name of readdirSync(dir)) {
      const path = join(dir, name);
      const rel = prefix === "" ? name : `${prefix}/${name}`;
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) entries.push({ path: rel, kind: "symlink", target: readlinkSync(path) });
      else if (stat.isDirectory()) { entries.push({ path: rel, kind: "directory" }); walk(path, rel); }
      else if (stat.isFile()) entries.push({ path: rel, kind: "file", base64: readFileSync(path).toString("base64") });
      else throw new Error(`unsupported fixture entry ${rel}`);
    }
  };
  walk(root, "");
  return entries.sort((a, b) => Buffer.compare(Buffer.from(a.path), Buffer.from(b.path)));
}

const before = inventory();
let call = null;
let processResult = null;
if (request.kind === "cli") {
  const argv = (request.argv ?? []).map((a) => request.id === "cli-absolute" && a === request.output ? outAbs : a);
  const child = spawnSync(process.execPath, ["--import", seedPath, join(repo, "dist", "cli.js"), "judge-preview", ...argv], {
    cwd: root, encoding: "buffer", timeout: 60000, maxBuffer: 64 * 1024 * 1024, env: process.env,
  });
  if (child.error) throw child.error;
  if (child.status === null) throw new Error(`CLI child has no exit status (signal ${child.signal})`);
  processResult = { stdout: child.stdout.toString("utf8"), stderr: child.stderr.toString("utf8"), exitCode: child.status };
} else {
  try {
    const analysis = request.id === "render-default" ? undefined : mutate(baseAnalysis());
    const actions = {
      analysis: () => createDemoAnalysis("REPLAY"),
      writer: () => writeProofBundle(request.output ?? ".faultline/bundles/demo", analysis,
        request.proofRoot === undefined ? {} : { proofRoot: request.proofRoot }),
      render: () => request.id === "render-default" ? renderJudgePreviewPage() : renderJudgePreviewPage(analysis),
    };
    call = { valueJSON: JSON.stringify(actions[request.kind]()), error: null };
  } catch (error) {
    call = { valueJSON: null, error: error instanceof Error ? error.message : String(error) };
  }
}
const after = inventory();
process.stdout.write(JSON.stringify({ request, call, process: processResult, before, after }));
