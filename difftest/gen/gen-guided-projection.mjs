// Test-only generator for the Task5C guided orchestration PROJECTION corpus.
//
//   node gen-guided-projection.mjs --check
//       Read-only: Node pin, frozen source pin, compiled module hash, import allowlist, committed corpus.
//   node gen-guided-projection.mjs --capture --out ABS_DIR
//       Initial capture. ABS_DIR is the corpus dir (holding only cases.json) or a new empty dir.
//       Evidence files are created exclusively (flag "wx"); nothing is ever overwritten.
//   node gen-guided-projection.mjs --recapture --out ABS_NEW_EMPTY_DIR
//       Independent fresh capture into a new directory, compared byte-for-byte with the committed corpus.
//
// Run with the pinned Node (22.22.2). The UNCHANGED compiled dist/guided-investigate.js is executed;
// guided-projection-loader.mjs replaces only its exactly-allowlisted imported dependency modules with
// stubs that log their arguments and return the fixture's scripted step. Expected output is solely what
// this Node execution produces. Scripted-dependency projection: NOT end-to-end and NOT native proof.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { importAllowlist, install } from "./guided-projection-loader.mjs";

export const frozenCommit = "ce99a793af0b020bf1b9cf21114f8d7897b37207";
export const frozenSourceSha256 = "8117310db75752f5e7faf160adf0be53c9832070b8c6a38a83b0572fd5a1783c";
export const frozenCompiledSha256 = "b50a8060c8d85b3539c05ed8dfa5d44c4d3ce7d01d6af8ff6418c647b2fbfb7a";
export const frozenRealDeps = {
  "dist/doctor.js": "b3c03fe38ebba86a74fab01579402d5b3573ccf8da0e42ae61b5f66fecf4e6c0",
  "dist/incident-store.js": "777062b0aea9f1cccc92556f4ae14f50e1be61e91c6549fcd129c5af4d0003a3",
};
export const oracleNode = "22.22.2";
export const schema = "faultline.guided-projection.v1";
const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, "..", "..");
const corpusDir = join(repo, "difftest", "testdata", "guided-investigate", "projection");
const sha256 = (b) => createHash("sha256").update(b).digest("hex");

// Declared owned path roles. Fixtures/captures carry only the placeholders; the driver expands them to the
// actual owned roots and normalizes ONLY exact owned-root tokens (token-bounded, followed by a separator,
// whitespace or end) back to placeholders. Lookalike or foreign prefixes are never touched.
export const pathRoots = { $ROOT: "/tmp/fl-proj-owned", $FOREIGN: "/tmp/fl-proj-foreign" };
const roleOf = (s) => Object.keys(pathRoots).find((r) => s === r || s.startsWith(`${r}/`));
export const expandPath = (s, roots, sep) => {
  const r = typeof s === "string" ? roleOf(s) : undefined;
  return r === undefined ? s : roots[r] + s.slice(r.length).split("/").join(sep);
};
export const normalizePaths = (s, roots, sep) => {
  let out = "";
  let i = 0;
  const isWs = (c) => c === undefined || /\s/.test(c);
  outer: while (i < s.length) {
    if (isWs(s[i - 1]) || i === 0) {
      for (const [role, root] of Object.entries(roots)) {
        if (s.startsWith(root, i)) {
          const next = s[i + root.length];
          if (next === undefined || isWs(next) || next === sep) {
            let j = i + root.length;
            while (j < s.length && !isWs(s[j])) j++;
            out += role + s.slice(i + root.length, j).split(sep).join("/");
            i = j;
            continue outer;
          }
        }
      }
    }
    out += s[i++];
  }
  return out;
};
const deepMap = (v, f) => {
  if (typeof v === "string") return f(v);
  if (Array.isArray(v)) return v.map((x) => deepMap(x, f));
  if (v !== null && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, deepMap(x, f)]));
  return v;
};

// Generic tagged encoding: distinguishes undefined, key presence, function and signal identity.
function makeEncoder(fnIds, signalIds) {
  const enc = (v) => {
    if (v === undefined) return { $t: "undefined" };
    if (typeof v === "string") return normalizePaths(v, pathRoots, "/");
    if (v === null || typeof v === "boolean") return v;
    if (typeof v === "number") {
      if (!Number.isFinite(v)) throw new Error("non-finite number in transcript");
      return v;
    }
    if (typeof v === "function") {
      if (!fnIds.has(v)) throw new Error("unregistered function identity in transcript");
      return { $t: "fn", id: fnIds.get(v) };
    }
    if (v instanceof AbortSignal) {
      if (!signalIds.has(v)) throw new Error("unregistered signal identity in transcript");
      return { $t: "signal", id: signalIds.get(v), aborted: v.aborted };
    }
    if (Array.isArray(v)) return { $t: "array", items: v.map(enc) };
    if (typeof v === "object") return { $t: "object", members: Object.keys(v).map((k) => [k, enc(v[k])]) };
    throw new Error(`unencodable ${typeof v}`);
  };
  return enc;
}

const kinds = ["doctor", "readDraft", "verify", "waitForFreeze", "resolveRuntime", "continue"];

async function runCase(guided, rawCase, state) {
  const c = deepMap(rawCase, (s) => expandPath(s, pathRoots, "/"));
  const events = [];
  const fnIds = new Map();
  const signalIds = new Map();
  const enc = makeEncoder(fnIds, signalIds);
  const scripts = {};
  for (const k of Object.keys(c.script ?? {})) if (!kinds.includes(k)) throw new Error(`${c.id}: unknown script kind ${k}`);
  for (const k of kinds) scripts[k] = [...(c.script?.[k] ?? [])];
  if (scripts.waitForFreeze.length > 0 && !c.options.waitForFreeze) throw new Error(`${c.id}: waiter script needs options.waitForFreeze`);
  state.hooks = {
    call(kind, args) {
      events.push({ kind, args: args.map(enc) });
      const step = scripts[kind].shift();
      if (step === undefined) throw new Error(`${c.id}: unscripted ${kind} call`);
      if (Object.hasOwn(step, "throw")) throw new Error(step.throw);
      return step.return;
    },
    unexpected(name) { throw new Error(`${c.id}: unexpected dependency ${name}`); },
  };
  // Fixture option values: {"$fn":id} and {"$signal":id,"aborted":bool} are identity markers.
  const options = {};
  for (const [k, v] of Object.entries(c.options)) {
    if (v !== null && typeof v === "object" && "$fn" in v) {
      const fn = k === "waitForFreeze"
        ? async (...a) => state.hooks.call("waitForFreeze", a)
        : () => { throw new Error("identity-only function must not be invoked"); };
      fnIds.set(fn, v.$fn);
      options[k] = fn;
    } else if (v !== null && typeof v === "object" && "$signal" in v) {
      const ac = new AbortController();
      if (v.aborted) ac.abort();
      signalIds.set(ac.signal, v.$signal);
      options[k] = ac.signal;
    } else {
      options[k] = v;
    }
  }
  const onPhase = (...a) => { events.push({ kind: "phase", args: a.map(enc) }); };
  fnIds.set(onPhase, "onPhase");
  options.onPhase = onPhase;
  let outcome;
  try {
    const result = await guided.guidedInvestigateFromCiLog(options);
    outcome = { kind: "result", json: JSON.stringify(deepMap(result, (s) => normalizePaths(s, pathRoots, "/")), null, 2) };
  } catch (e) {
    if (!(e instanceof Error)) throw e;
    outcome = { kind: "error", message: normalizePaths(e.message, pathRoots, "/") };
  }
  for (const k of kinds) if (scripts[k].length !== 0) throw new Error(`${c.id}: ${scripts[k].length} unused ${k} script step(s)`);
  return { id: c.id, outcome, events };
}

function pinnedChecks() {
  if (process.version !== `v${oracleNode}`) throw new Error(`pinned Node v${oracleNode} required, running ${process.version}`);
  const src = readFileSync(join(repo, "src", "guided-investigate.ts"));
  if (sha256(src) !== frozenSourceSha256) throw new Error("src/guided-investigate.ts differs from the frozen source pin");
  const shown = spawnSync("git", ["show", `${frozenCommit}:src/guided-investigate.ts`], { cwd: repo });
  if (shown.status !== 0 || sha256(shown.stdout) !== frozenSourceSha256) throw new Error("git frozen source does not match the pin");
}

const stable = (o) => `${JSON.stringify(o, null, 2)}\n`;

async function captureAll(casesBytes) {
  pinnedChecks();
  const cases = JSON.parse(casesBytes);
  if (cases.schema !== schema) throw new Error("cases.json schema mismatch");
  // --dist-file exists only so a negative control can feed a COPY; the fixed pins below are unconditional
  // and are verified before anything is imported.
  const distFile = argOf("--dist-file") ?? join(repo, "dist", "guided-investigate.js");
  const distBytes = readFileSync(distFile);
  if (sha256(distBytes) !== frozenCompiledSha256) throw new Error(`compiled guided-investigate.js sha256 ${sha256(distBytes)} differs from the pinned legitimate frozen build ${frozenCompiledSha256}`);
  for (const [name, want] of Object.entries(frozenRealDeps)) {
    const got = sha256(readFileSync(join(dirname(distFile), name.slice("dist/".length))));
    if (got !== want) throw new Error(`real dependency ${name} sha256 ${got} differs from its pin ${want}`);
  }
  const state = { hooks: null };
  globalThis.__flProjection = { call: (...a) => state.hooks.call(...a), unexpected: (...a) => state.hooks.unexpected(...a) };
  const { moduleUrl, resolved } = install({ distFile });
  const guided = await import(moduleUrl);
  const files = new Map();
  const entries = [];
  const ids = new Set();
  for (const c of cases.cases) {
    if (ids.has(c.id)) throw new Error(`duplicate case ${c.id}`);
    ids.add(c.id);
    const cap = await runCase(guided, c, state);
    const body = stable(cap);
    files.set(`captures/${c.id}.json`, body);
    entries.push({ id: c.id, inputSha256: sha256(JSON.stringify(c)), output: `captures/${c.id}.json`, outputSha256: sha256(body) });
  }
  const missing = Object.keys(importAllowlist).filter((s) => !resolved.includes(s));
  if (missing.length) throw new Error(`frozen module did not resolve allowlisted imports: ${missing.join(", ")}`);
  const manifest = {
    schema,
    provenance: "Expected outputs are solely Node-derived: the unchanged compiled frozen dist/guided-investigate.js executed with scripted dependency stubs. This is a scripted-dependency orchestration projection, not end-to-end and not native Docker proof evidence.",
    frozenCommit,
    node: process.version.slice(1),
    frozenSource: { path: "src/guided-investigate.ts", sha256: frozenSourceSha256 },
    compiledModule: { path: "dist/guided-investigate.js", sha256: frozenCompiledSha256 },
    realDependencies: frozenRealDeps,
    generator: { path: "difftest/gen/gen-guided-projection.mjs", sha256: sha256(readFileSync(fileURLToPath(import.meta.url))) },
    loader: { path: "difftest/gen/guided-projection-loader.mjs", sha256: sha256(readFileSync(join(here, "guided-projection-loader.mjs"))) },
    importAllowlist,
    pathRoles: Object.keys(pathRoots),
    casesFileSha256: sha256(casesBytes),
    cases: entries,
  };
  files.set("manifest.json", stable(manifest));
  return files;
}

function writeAll(dir, files, casesBytes) {
  for (const [name, body] of files) {
    const p = join(dir, name);
    mkdirSync(dirname(p), { recursive: true });
    writeFileSync(p, body, { flag: "wx" });
  }
  if (casesBytes) writeFileSync(join(dir, "cases.json"), casesBytes, { flag: "wx" });
}

function argOf(name) {
  const i = process.argv.indexOf(name);
  return i < 0 ? undefined : process.argv[i + 1];
}

async function main() {
  const mode = ["--check", "--capture", "--recapture"].find((m) => process.argv.includes(m));
  if (!mode) throw new Error("mode required: --check | --capture --out DIR | --recapture --out DIR");
  const casesBytes = readFileSync(argOf("--cases") ?? join(corpusDir, "cases.json"));
  const fresh = await captureAll(casesBytes);
  if (mode !== "--capture") {
    for (const [name, body] of fresh) {
      const p = join(corpusDir, name);
      if (!existsSync(p) || readFileSync(p, "utf8") !== body) throw new Error(`committed ${name} differs from fresh Node capture`);
    }
    for (const f of readdirSync(join(corpusDir, "captures"))) {
      if (!fresh.has(`captures/${f}`)) throw new Error(`committed extra capture ${f}`);
    }
  }
  if (mode === "--check") { console.log(`check ok: ${fresh.size - 1} captures`); return; }
  const out = argOf("--out");
  if (!out || !isAbsolute(out)) throw new Error("--out ABS_DIR required");
  if (mode === "--capture" && resolve(out) === corpusDir) {
    if (existsSync(join(out, "manifest.json")) || existsSync(join(out, "captures"))) throw new Error("capture refuses to overwrite existing evidence");
    writeAll(out, fresh, undefined);
  } else {
    if (existsSync(out) && readdirSync(out).length) throw new Error("--out must be new or empty");
    mkdirSync(out, { recursive: true });
    writeAll(out, fresh, casesBytes);
  }
  console.log(`${mode} wrote ${fresh.size} files to ${out}`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((e) => { console.error(`FAIL: ${e.message}`); process.exit(1); });
}
