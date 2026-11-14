// Test-only frozen-TS evidence generator for the Go guided `fl investigate --ci-log/--resume` port.
//
//   node difftest/gen/gen-guided-investigate.mjs --check
//       Read-only. Verifies Node, the frozen source pin, the compiled CLI, the manifest provenance and
//       every pinned input, and validates the committed corpus. Spawns/writes nothing.
//   node difftest/gen/gen-guided-investigate.mjs --capture --slot ABS_SLOT --scratch ABS_DIR
//       Initial capture of the real frozen dist/cli.js. Refuses when a baseline already exists.
//       Evidence files are created exclusively (flag "wx").
//   node difftest/gen/gen-guided-investigate.mjs --probe --slot ABS_SLOT --scratch ABS_DIR [--only id,id]
//       Development aid: captures the selected cases into --scratch only and never touches the corpus.
//   node difftest/gen/gen-guided-investigate.mjs --recapture --slot ABS_SLOT --scratch ABS_DIR --out ABS_DIR
//       Fresh capture into a new empty --out directory, compared byte-for-byte with the committed
//       corpus. Overwrites nothing; the old evidence is untouched.
//
//   node difftest/gen/gen-guided-investigate.mjs --native-capture --slot ABS_SLOT --scratch ABS_DIR --out ABS_DIR
//       Fresh capture on the CURRENT platform into a new empty --out directory, with a measured "platform" block
//       added to baseline.json. It is never compared with the POSIX corpus and overwrites nothing. On Windows
//       every scripted step runs through a Go-built step runner (GUIDED_STEP_RUNNER with GUIDED_CONSOLE_HELPER)
//       that reuses the servefixture console preparation, process ownership and Ctrl+C console helper; plain
//       child.kill(SIGINT) is not equivalent there. The frozen JS CLI is unchanged.
//   node difftest/gen/gen-guided-investigate.mjs --compare-native --a ABS_DIR --b ABS_DIR
//       Read-only. Requires two native capture directories to be byte-identical.
//
// Every case runs at ONE owned absolute workspace slot (ABS_SLOT, which must not exist). Draft.repository
// is hashed, so TS and Go must see the same input path to produce the same digests; after each case the
// slot is renamed into <scratch>/work/<caseId> (full retained evidence) so the next case can reuse the
// path. ABS_SLOT + ".tools", ".home" and ".tmp" are the owned toolchain, HOME and TMPDIR of that case.
//
// Expected output is whatever the frozen TypeScript produces. A case "expect" block only guards that the
// scenario reached the intended path; a failed guard aborts the capture and is reported exactly.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync, copyFileSync, existsSync, linkSync, lstatSync, mkdirSync, readFileSync, readdirSync, readlinkSync,
  realpathSync, renameSync, writeFileSync,
} from "node:fs";
import http from "node:http";
import { arch as osArch, release as osRelease, type as osType } from "node:os";
import path, { dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

export const frozenCommit = "ce99a793af0b020bf1b9cf21114f8d7897b37207";
export const frozenSrcTree = "47b50ed83511d5e7c27e42e587f03d3c9d637bb7";
export const frozenManifestVersion = "0.1.3";
export const currentManifestVersion = "0.1.4";
export const oracleNode = "22.22.2";
const oracleSchema = "faultline.guided-investigate-oracle.v1";
const captureSchema = "faultline.guided-investigate-capture.v1";
const generatorPath = "difftest/gen/gen-guided-investigate.mjs";
const stepTimeoutMs = 45000;

const generatorFile = fileURLToPath(import.meta.url);
const { canonicalJson, digestJson } = await import(pathToFileURL(join(dirname(generatorFile), "..", "..", "dist", "canonical.js")).href);
const repo = resolve(dirname(generatorFile), "..", "..");
const rel = {
  dir: "difftest/testdata/guided-investigate/oracle",
  inputs: {
    "cases.json": "difftest/testdata/guided-investigate/oracle/cases.json",
    "clock.mjs": "difftest/testdata/guided-investigate/oracle/clock.mjs",
    "fakebox/main.go": "difftest/testdata/guided-investigate/oracle/fakebox/main.go",
    "gen-guided.mjs": generatorPath,
    "seed.mjs": "difftest/gen/seed.mjs",
  },
};
const abs = (r) => join(repo, ...r.split("/"));
const sha256 = (b) => createHash("sha256").update(b).digest("hex");
const win = process.platform === "win32";

// ---------------------------------------------------------------------------------------------
// Pins
// ---------------------------------------------------------------------------------------------
const git = (args, opts = {}) => {
  const r = spawnSync("git", args, { cwd: repo, encoding: "utf8", ...opts });
  assert.equal(r.status, 0, `git ${args.join(" ")} failed: ${r.stderr}`);
  return r.stdout;
};

export function measurePins() {
  assert.equal(process.version, `v${oracleNode}`, `oracle needs Node v${oracleNode}, running ${process.version}`);
  const srcTree = git(["rev-parse", `${frozenCommit}:src`]).trim();
  assert.equal(srcTree, frozenSrcTree, "frozen commit src tree changed");
  // Working-tree src is byte-identical to the frozen commit.
  const diff = spawnSync("git", ["diff", "--quiet", frozenCommit, "--", "src"], { cwd: repo });
  assert.equal(diff.status, 0, "src differs from the frozen commit");
  const frozenManifest = JSON.parse(git(["show", `${frozenCommit}:package.json`]));
  assert.equal(frozenManifest.version, frozenManifestVersion, "frozen manifest version");
  const manifest = JSON.parse(readFileSync(abs("package.json"), "utf8"));
  assert.equal(manifest.version, currentManifestVersion, "current manifest version");
  const cli = spawnSync(process.execPath, [abs("dist/cli.js"), "--version"], { encoding: "utf8" });
  assert.equal(cli.status, 0, "dist/cli.js --version failed");
  assert.equal(cli.stdout, `FaultLine ${currentManifestVersion}\n`, "dist/cli.js version");
  return {
    commit: frozenCommit, srcTree, frozenManifestVersion, manifestVersion: currentManifestVersion,
    cliVersion: cli.stdout.trim(),
  };
}

function inputHashes() {
  const out = {};
  for (const [name, r] of Object.entries(rel.inputs)) out[name] = sha256(readFileSync(abs(r)));
  out["package.json.cli"] = currentManifestVersion;
  return out;
}

// ---------------------------------------------------------------------------------------------
// Case file
// ---------------------------------------------------------------------------------------------
function loadCases() {
  const cases = JSON.parse(readFileSync(abs(rel.inputs["cases.json"]), "utf8"));
  assert.equal(cases.schema, "faultline.guided-investigate-oracle-cases.v1");
  const seen = new Set();
  for (const c of cases.cases) {
    assert.ok(!seen.has(c.id), `duplicate case ${c.id}`);
    seen.add(c.id);
    assert.ok(cases.groups.includes(c.group), `case ${c.id}: unknown group`);
    assert.ok(/^[a-z0-9-]+$/.test(c.id), `case ${c.id}: bad id`);
    assert.ok(c.steps.length > 0, `case ${c.id}: no steps`);
  }
  return cases;
}

function fileBytes(spec) {
  if (typeof spec === "string") return Buffer.from(spec, "utf8");
  const parts = spec.map((p) => {
    if ("text" in p) return Buffer.from(p.text, "utf8");
    if ("repeat" in p) return Buffer.from(p.repeat.repeat(p.count), "utf8");
    if ("hex" in p) return Buffer.from(p.hex, "hex");
    throw new Error(`bad file segment ${JSON.stringify(p)}`);
  });
  return Buffer.concat(parts);
}

// ---------------------------------------------------------------------------------------------
// Owned workspace
// ---------------------------------------------------------------------------------------------
const commitDates = ["2026-07-16T11:00:00Z", "2026-07-16T11:30:00Z"];

function findGit() {
  const r = spawnSync(win ? "where" : "which", ["git"], { encoding: "utf8" });
  assert.equal(r.status, 0, "git not found");
  return r.stdout.split(/\r?\n/)[0].trim();
}

function ownedEnv(slot, extra = {}) {
  const env = {
    PATH: slot + ".tools",
    HOME: slot + ".home",
    USERPROFILE: slot + ".home",
    TMPDIR: slot + ".tmp",
    TEMP: slot + ".tmp",
    TMP: slot + ".tmp",
    LC_ALL: "C.UTF-8",
    LANGUAGE: "",
    FAULTLINE_TEST_SEED: "guided-investigate-oracle-v1",
    FAULTLINE_NO_BROWSER: "1",
    GIT_CONFIG_GLOBAL: win ? "NUL" : "/dev/null",
    GIT_CONFIG_NOSYSTEM: "1",
    ...extra,
  };
  for (const name of ["SystemRoot", "SYSTEMROOT", "PATHEXT", "ComSpec"]) {
    if (process.env[name] !== undefined) env[name] = process.env[name];
  }
  return env;
}

function runGit(realGit, cwd, args, i) {
  const r = spawnSync(realGit, args, {
    cwd, encoding: "utf8",
    env: {
      PATH: dirname(realGit), HOME: cwd, GIT_CONFIG_GLOBAL: win ? "NUL" : "/dev/null", GIT_CONFIG_NOSYSTEM: "1",
      GIT_AUTHOR_NAME: "Oracle", GIT_AUTHOR_EMAIL: "oracle@example.invalid",
      GIT_COMMITTER_NAME: "Oracle", GIT_COMMITTER_EMAIL: "oracle@example.invalid",
      GIT_AUTHOR_DATE: commitDates[i ?? 0], GIT_COMMITTER_DATE: commitDates[i ?? 0],
      ...(win ? { SystemRoot: process.env.SystemRoot } : {}),
    },
  });
  assert.equal(r.status, 0, `git ${args.join(" ")}: ${r.stderr}`);
  return r.stdout.trim();
}

function buildRepo(realGit, dir, fixture) {
  mkdirSync(dir, { recursive: true });
  runGit(realGit, dir, ["init", "-q", "-b", "main"]);
  const shas = [];
  if (fixture.gitExclude) {
    mkdirSync(join(dir, ".git", "info"), { recursive: true });
    writeFileSync(join(dir, ".git", "info", "exclude"), `${fixture.gitExclude.join("\n")}\n`);
  }
  fixture.commits.forEach((c, i) => {
    for (const [name, content] of Object.entries(c.files)) writeFileSync(join(dir, name), content);
    runGit(realGit, dir, ["add", "-A"], i);
    runGit(realGit, dir, ["commit", "-q", "-m", c.message, "--no-gpg-sign"], i);
    shas.push(runGit(realGit, dir, ["rev-parse", "HEAD"]));
  });
  return shas;
}

const helperHashes = new Map();
function helperSha(path) {
  if (!helperHashes.has(path)) helperHashes.set(path, sha256(readFileSync(path)));
  return helperHashes.get(path);
}

function buildTools(fakebox, slot, realGit, tools) {
  const dir = slot + ".tools";
  mkdirSync(dir, { recursive: true });
  const ext = win ? ".exe" : "";
  const names = ["git"];
  if (!tools.noNode) names.push("node");
  if (!tools.noDocker) names.push("docker");
  // The helper is one immutable owned file (mode 0755, fixed once by buildFakebox). Every tool name is a hard
  // link to it, with a plain copy as the fallback (cross-device, unsupported filesystem). The helper never
  // writes itself (fakebox appends only to <tool>.log, a separate per-case file) and nothing captured
  // includes inode or link count, so argv paths and evidence bytes are identical either way.
  for (const n of names) {
    const dst = join(dir, n + ext);
    try { linkSync(fakebox, dst); } catch { copyFileSync(fakebox, dst); chmodSync(dst, 0o755); }
    // POSIX: exactly 0755. Windows has no Unix permission bits (lstat reports synthetic ones), so there the
    // tool must be a regular file with the helper's bytes (checked below); it is proven executable by running.
    const st = lstatSync(dst);
    assert.ok(st.isFile(), `tool ${n} must be a regular file`);
    if (!win) assert.equal(st.mode & 0o777, 0o755, `tool ${n} must be mode 755`);
    assert.equal(sha256(readFileSync(dst)), helperSha(fakebox), `tool ${n} bytes differ from the helper`);
  }
  writeFileSync(join(dir, "real-git"), realGit);
  if (tools.nodeVersion) writeFileSync(join(dir, "node-version"), tools.nodeVersion);
  if (tools.dockerDown) writeFileSync(join(dir, "docker-down"), "");
  if (tools.showFails) writeFileSync(join(dir, "show-fails"), "");
  return dir;
}

// ---------------------------------------------------------------------------------------------
// Tree snapshot (everything under the slot except .git internals)
// ---------------------------------------------------------------------------------------------
function snapshotTree(slot) {
  const entries = [];
  const walk = (relPath) => {
    const dir = join(slot, relPath);
    for (const name of readdirSync(dir).sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)))) {
      if (name === ".git") continue;
      const r = relPath ? `${relPath}/${name}` : name;
      const full = join(slot, r);
      const st = lstatSync(full);
      const mode = (st.mode & 0o777).toString(8);
      if (st.isSymbolicLink()) {
        entries.push({ path: r, kind: "symlink", mode, size: 0, sha256: "", target: readlinkSync(full) });
      } else if (st.isDirectory()) {
        entries.push({ path: r, kind: "dir", mode, size: 0, sha256: "", target: "" });
        walk(r);
      } else {
        const bytes = readFileSync(full);
        entries.push({ path: r, kind: "file", mode, size: bytes.length, sha256: sha256(bytes), target: "", bytes });
      }
    }
  };
  walk("");
  return entries;
}

// A tree entry in its STORED form: size and sha256 describe the stored (token-normalized) bytes, so they are
// identical for any owned slot path. Binary files are stored as base64 and hashed raw.
function describeTree(entries, norm) {
  return entries.map((e) => {
    const out = { path: e.path, kind: e.kind, mode: e.mode, size: 0, sha256: "", target: norm(e.target, false), content: "", base64: "" };
    if (e.kind === "file") {
      const text = e.bytes.toString("utf8");
      if (Buffer.from(text, "utf8").equals(e.bytes)) {
        out.content = norm(text, e.path.endsWith(".json"));
        out.size = Buffer.byteLength(out.content);
        out.sha256 = sha256(Buffer.from(out.content, "utf8"));
      } else {
        out.base64 = e.bytes.toString("base64");
        out.size = e.bytes.length;
        out.sha256 = sha256(e.bytes);
      }
    }
    return out;
  });
}

const compactTree = (described) => described.map(({ path, kind, mode, size, sha256: h, target }) => ({ path, kind, mode, size, sha256: h, target }));

// ---------------------------------------------------------------------------------------------
// Slot tokens and named digest references
//   {SLOT}   the owned slot path as a raw string          {SLOTJ}  the same path JSON-string-escaped
//   {PORT}   the bound ephemeral loopback port of that step's own readiness URL
//   {DIGEST:<id>}  "sha256:<hex>" of a slot-derived object, and {FILE:<id>} its bare-hex file hash, both
//                  derived from a Node-captured preimage template (see collectGraph). A value is replaced
//                  only when it is the validated digest of a known object; no other digest is ever replaced.
// ---------------------------------------------------------------------------------------------
function makeNormalizer(slot) {
  const slotJ = JSON.stringify(slot).slice(1, -1);
  return (text, json, port, refs) => {
    let out = text.split(json ? slotJ : slot).join(json ? "{SLOTJ}" : "{SLOT}");
    assert.ok(!out.includes(slot), "owned slot text survived normalization");
    if (port !== undefined) out = out.replaceAll(`127.0.0.1:${port}`, "127.0.0.1:{PORT}");
    for (const [literal, token] of refs ?? []) out = out.replaceAll(literal, token);
    return out;
  };
}

// ---------------------------------------------------------------------------------------------
// Running one step
// ---------------------------------------------------------------------------------------------
const portRE = /127\.0\.0\.1:([0-9]+)/g;

function request(method, url, body) {
  return new Promise((res, rej) => {
    const u = new URL(url);
    const req = http.request({ method, hostname: u.hostname, port: u.port, path: u.pathname, headers: body === undefined ? {} : { "content-type": "application/x-www-form-urlencoded", "content-length": Buffer.byteLength(body) } }, (r) => {
      const chunks = [];
      r.on("data", (c) => chunks.push(c));
      r.on("end", () => res({ status: r.statusCode, text: Buffer.concat(chunks).toString("utf8") }));
    });
    req.on("error", rej);
    if (body !== undefined) req.write(body);
    req.end();
  });
}

// One child process of a step. POSIX: node spawned directly. Windows with a script: through the Go step runner.
const importArg = (p) => (win ? pathToFileURL(p).href : p);

function nodeArgs(args) {
  return ["--import", importArg(abs("difftest/gen/seed.mjs")), "--import", importArg(abs(rel.inputs["clock.mjs"])), abs("dist/cli.js"), ...args];
}

function spawnDirect({ args, cwd, env, notify }) {
  const child = spawn(process.execPath, nodeArgs(args), { cwd, env, stdio: ["ignore", "pipe", "pipe"] });
  const out = [];
  const err = [];
  child.stdout.on("data", (c) => out.push(c));
  child.stderr.on("data", (c) => { err.push(c); notify(); });
  const exited = new Promise((res) => child.on("close", (code, signal) => { res({ code, signal }); notify(); }));
  return {
    stdoutNow: () => Buffer.concat(out).toString("utf8"),
    stderrNow: () => Buffer.concat(err).toString("utf8"),
    exited,
    signal: (name) => child.kill(name),
    kill: () => child.kill("SIGKILL"),
  };
}

// Windows scripted step. The runner prints "READY <json>" once the review readiness stderr is seen and
// "DONE <json>" with the complete raw streams (base64) and the raw exit status; it reads "SIGINT"/"SIGTERM"
// lines on stdin and delivers them with the servefixture primitives (own console + console helper).
export function spawnViaRunner({ args, cwd, env, notify, envFile }) {
  const runner = process.env.GUIDED_STEP_RUNNER;
  const helper = process.env.GUIDED_CONSOLE_HELPER;
  assert.ok(runner && isAbsolute(runner) && helper && isAbsolute(helper), "Windows scripted steps need GUIDED_STEP_RUNNER and GUIDED_CONSOLE_HELPER (absolute)");
  writeFileSync(envFile, JSON.stringify(Object.entries(env).map(([k, v]) => `${k}=${v}`)), { flag: "wx" });
  const child = spawn(runner, ["--helper", helper, "--cwd", cwd, "--env-file", envFile, "--exe", process.execPath, "--", ...nodeArgs(args)], {
    env: { SystemRoot: process.env.SystemRoot ?? "" }, stdio: ["pipe", "pipe", "pipe"],
  });
  let ready = null;
  let done = null;
  let buf = "";
  const diag = [];
  let stdinError = null;
  // EPIPE on an already-exited runner must not be an uncaught exception: it is recorded and reported together
  // with the runner's own diagnostic through the owned promise.
  child.stdin.on("error", (e) => { stdinError = stdinError ?? e; notify(); });
  child.on("error", (e) => { stdinError = stdinError ?? e; notify(); });
  child.stderr.on("data", (c) => diag.push(c));
  child.stdout.on("data", (c) => {
    buf += c.toString("utf8");
    for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
      const line = buf.slice(0, i);
      buf = buf.slice(i + 1);
      if (line.startsWith("READY ")) ready = JSON.parse(line.slice(6));
      else if (line.startsWith("DONE ")) done = JSON.parse(line.slice(5));
      notify();
    }
  });
  const closed = new Promise((res) => child.on("close", (code) => { res(code); notify(); }));
  const exited = closed.then((rc) => {
    if (done === null) throw new Error(`step runner ended without a result (rc=${rc}${stdinError ? `, ${stdinError.code ?? stdinError.message}` : ""}): ${Buffer.concat(diag).toString("utf8")}`);
    return { code: done.exit, signal: done.signal };
  });
  exited.catch(() => {});
  const text = (b64) => Buffer.from(b64, "base64").toString("utf8");
  return {
    stdoutNow: () => (done ? text(done.stdout) : ""),
    stderrNow: () => (done ? text(done.stderr) : ready ? text(ready.stderr) : ""),
    exited,
    signal: (name) => {
      if (stdinError !== null || child.stdin.destroyed || !child.stdin.writable) throw new Error(`step runner is not accepting ${name}: ${stdinError ? stdinError.message : "its control pipe is closed"}`);
      child.stdin.write(`${name}\n`, (e) => { if (e) { stdinError = stdinError ?? e; notify(); } });
    },
    kill: () => child.kill(),
  };
}

async function runStep({ slot, step, args, cwd, env, caseId, cases, scratch }) {
  const waiters = [];
  const notify = () => { for (const w of waiters.splice(0)) w(); };
  const scripted = (step.script ?? []).length > 0;
  // GUIDED_FORCE_RUNNER=1 exercises the runner protocol on POSIX (local validation only); Windows always uses it.
  const proc = (win || process.env.GUIDED_FORCE_RUNNER === "1") && scripted
    ? spawnViaRunner({ args, cwd, env, notify, envFile: join(scratch, `runner-env-${caseId}-${step.name.replace(/[^a-z0-9]+/gi, "-")}.json`) })
    : spawnDirect({ args, cwd, env, notify });
  const exited = proc.exited;
  let done = false;
  exited.then(() => { done = true; }, () => { done = true; });
  const stderrNow = proc.stderrNow;
  const waitFor = async (pred, what) => {
    const deadline = Date.now() + stepTimeoutMs;
    while (!pred()) {
      if (done) throw new Error(`${caseId}/${step.name}: process exited before ${what}; stderr=${JSON.stringify(stderrNow())}`);
      if (Date.now() > deadline) { proc.kill(); throw new Error(`${caseId}/${step.name}: timeout waiting for ${what}`); }
      await new Promise((r) => { waiters.push(r); setTimeout(r, 200); });
    }
  };
  const actions = [];
  let url = null;
  let page = "";
  try {
  for (const a of step.script ?? []) {
    if (a.do === "ready") {
      await waitFor(() => /FaultLine local witness review: http:\/\/127\.0\.0\.1:[0-9]+\nApprove, then Freeze[^\n]*\n$/.test(stderrNow()), "review readiness");
      url = /FaultLine local witness review: (http:\/\/127\.0\.0\.1:[0-9]+)\n/.exec(stderrNow())[1];
      actions.push({ do: "ready" });
    } else if (a.do === "get") {
      const r = await request("GET", url + "/");
      page = r.text;
      assert.equal(r.status, a.expect, `${caseId}/${step.name}: GET status ${r.status}`);
      actions.push({ do: "get", path: "/", status: r.status });
    } else if (a.do === "post") {
      const token = a.token ?? (/name="token" value="([0-9a-f]{64})"/.exec(page) ?? [])[1];
      const digest = (/name="reviewDigest" value="(sha256:[0-9a-f]{64})"/.exec(page) ?? [])[1];
      assert.ok(token !== undefined && digest !== undefined, `${caseId}/${step.name}: no form fields on the last page`);
      const fields = { token, reviewDigest: digest, approvedBy: cases.approvedBy, acknowledged: "exact-witness-reviewed" };
      if (a.path === "/freeze") delete fields.approvedBy, delete fields.acknowledged;
      if (a.omit) delete fields[a.omit];
      const body = new URLSearchParams(fields).toString();
      const r = await request("POST", url + a.path, body);
      assert.equal(r.status, a.expect, `${caseId}/${step.name}: POST ${a.path} status ${r.status}: ${r.text.slice(0, 300)}`);
      const rec = { do: "post", path: a.path, status: r.status };
      if (a.message !== undefined) {
        assert.ok(r.text.includes(a.message), `${caseId}/${step.name}: response lacks ${JSON.stringify(a.message)}`);
        rec.message = true;
      }
      actions.push(rec);
    } else if (a.do === "sigint" || a.do === "sigterm") {
      proc.signal(a.do === "sigint" ? "SIGINT" : "SIGTERM");
      actions.push({ do: a.do });
    } else {
      throw new Error(`unknown action ${a.do}`);
    }
  }
  } catch (e) {
    // Terminate and join the child; every file of the slot is preserved.
    proc.kill();
    await exited.catch(() => {});
    throw e;
  }
  const timer = setTimeout(() => proc.kill(), stepTimeoutMs);
  const { code, signal } = await exited;
  clearTimeout(timer);
  return { exit: code, signal, stdout: proc.stdoutNow(), stderr: stderrNow(), actions, url };
}

function checkExpect(caseId, step, r) {
  const e = step.expect ?? {};
  const where = `${caseId}/${step.name}`;
  if (e.exitOrSignal) assert.ok(r.exit !== null || r.signal !== null, `${where}: neither exit nor signal`);
  else if (e.exit !== undefined && e.exit !== null) assert.equal(r.exit, e.exit, `${where}: exit ${r.exit}/${r.signal}; stdout=${JSON.stringify(r.stdout.slice(0, 300))} stderr=${JSON.stringify(r.stderr.slice(0, 600))}`);
  if (e.stdout !== undefined) assert.equal(r.stdout, e.stdout, `${where}: stdout`);
  for (const s of e.stdoutContains ?? []) assert.ok(r.stdout.includes(s), `${where}: stdout lacks ${JSON.stringify(s)}: ${r.stdout.slice(0, 400)}`);
  for (const s of e.stderrContains ?? []) assert.ok(r.stderr.includes(s), `${where}: stderr lacks ${JSON.stringify(s)}: ${r.stderr.slice(0, 600)}`);
}

function readToolLogs(slot, offsets) {
  const records = {};
  for (const tool of ["docker", "git", "node"]) {
    const p = join(slot + ".tools", tool + ".log");
    const text = existsSync(p) ? readFileSync(p, "utf8") : "";
    const fresh = text.slice(offsets[tool] ?? 0);
    offsets[tool] = text.length;
    records[tool] = fresh === "" ? [] : fresh.split("\x1e").slice(0, -1);
  }
  return records;
}

// ---------------------------------------------------------------------------------------------
// Slot-derived digest graph
//   Draft.repository and the Git investigation repository are hashed, so a draft digest, a proof bundle's
//   investigation.json / manifest.json / hashes.txt / ROOT.sha256 and every digest they feed differ per slot.
//   Each supported object is stored as a canonical preimage template (slot -> {SLOTJ}, the digest of another
//   template -> {DIGEST:id}, another template's bare-hex file hash -> {FILE:id}). Kinds, by id prefix:
//     draft:<path>  record; digest = sha256 of the canonical preimage, stored in the record's draftDigest
//     json:<path>   canonical-JSON file; file bytes = preimage + LF; digest = sha256 of the preimage
//     text:<path>   plain file; file bytes = preimage; digest = sha256 of the bytes
//   Every digest is first validated against the real bytes Node wrote; only a value that is the validated
//   digest of such an object is ever tokenized. No other sha256 value is masked.
// ---------------------------------------------------------------------------------------------
const bundleFiles = ["investigation.json", "manifest.json", "hashes.txt", "ROOT.sha256"];

export function collectGraph(caseId, slot, snapshots) {
  const slotJ = JSON.stringify(slot).slice(1, -1);
  const nodes = new Map();
  const fail = (m) => { throw new Error(`${caseId}: ${m}`); };
  const add = (node) => {
    const old = nodes.get(node.id);
    if (old === undefined) { nodes.set(node.id, node); return; }
    if (old.preimage !== node.preimage) fail(`${node.id} changed between steps (an unsupported mutation of a digest-bearing object)`);
  };
  const tokenSlot = (text) => {
    const out = text.split(slotJ).join("{SLOTJ}");
    assert.ok(!out.includes(slot), "slot survived in a template");
    return out;
  };
  for (const entries of snapshots) {
    const files = new Map(entries.filter((e) => e.kind === "file").map((e) => [e.path, e]));
    for (const e of files.values()) {
      if (!/(^|\/)drafts\/[^/]+\.json$/.test(e.path)) continue;
      let obj;
      try { obj = JSON.parse(e.bytes.toString("utf8")); } catch { continue; }
      if (obj === null || typeof obj !== "object" || typeof obj.draftDigest !== "string" || !String(obj.schemaVersion ?? "").includes("incident-draft")) continue;
      const { draftDigest, ...unsigned } = obj;
      assert.equal(digestJson(unsigned), draftDigest, `${caseId}: ${e.path} draftDigest is not the digest of its unsigned preimage`);
      add({ id: `draft:${e.path}`, path: e.path, preimage: tokenSlot(canonicalJson(unsigned)), digest: draftDigest, file: null });
    }
    // Complete proof-bundle packages: a directory holding the four package files.
    const dirs = new Map();
    for (const p of files.keys()) {
      const i = p.lastIndexOf("/");
      if (bundleFiles.includes(p.slice(i + 1))) {
        const dir = i < 0 ? "" : p.slice(0, i);
        dirs.set(dir, (dirs.get(dir) ?? 0) + 1);
      }
    }
    for (const [dir, count] of dirs) {
      if (count !== bundleFiles.length) fail(`${dir}: incomplete proof bundle package file set`);
      const at = (n) => (dir === "" ? n : `${dir}/${n}`);
      const bytes = (n) => files.get(at(n)).bytes;
      const jsonNode = (n) => {
        const text = bytes(n).toString("utf8");
        const parsed = JSON.parse(text);
        if (`${canonicalJson(parsed)}\n` !== text) fail(`${at(n)} is not canonical JSON plus LF`);
        return { parsed, node: { id: `json:${at(n)}`, path: at(n), preimage: tokenSlot(canonicalJson(parsed)), digest: digestJson(parsed), file: sha256(bytes(n)) } };
      };
      const inv = jsonNode("investigation.json");
      const man = jsonNode("manifest.json");
      assert.equal(man.parsed.investigationDigest, inv.node.digest, `${at("manifest.json")} investigationDigest does not bind investigation.json`);
      const hashesText = bytes("hashes.txt").toString("utf8");
      const lines = hashesText.split("\n");
      assert.equal(lines.pop(), "", "hashes.txt must end with LF");
      const catalog = new Map(lines.map((l) => { const m = /^([0-9a-f]{64}) {2}(.+)$/.exec(l); assert.ok(m, `bad hashes line ${l}`); return [m[2], m[1]]; }));
      assert.equal(catalog.size, lines.length, "duplicate hashes.txt entry");
      for (const [path, h] of catalog) {
        const f = files.get(at(path));
        assert.ok(f, `${at("hashes.txt")} lists ${path}, absent from the package`);
        assert.equal(sha256(f.bytes), h, `${at("hashes.txt")} line for ${path} is stale`);
      }
      assert.equal(catalog.get("investigation.json"), inv.node.file);
      assert.equal(catalog.get("manifest.json"), man.node.file);
      const hashesDigest = `sha256:${sha256(bytes("hashes.txt"))}`;
      assert.equal(bytes("ROOT.sha256").toString("utf8"), `${hashesDigest}\n`, `${at("ROOT.sha256")} does not bind hashes.txt`);
      assert.ok(!hashesText.includes(slot), "slot in hashes.txt");
      add(inv.node);
      add(man.node);
      add({ id: `text:${at("hashes.txt")}`, path: at("hashes.txt"), preimage: hashesText, digest: hashesDigest, file: sha256(bytes("hashes.txt")) });
    }
  }
  // literal -> token: every prefixed digest first (so a bare hash can never cut into one), then every bare
  // file hash, both in id order. The first id wins when two objects share a digest.
  const ids = [...nodes.keys()].sort();
  const refs = [
    ...ids.map((id) => [nodes.get(id).digest, `{DIGEST:${id}}`]),
    ...ids.filter((id) => nodes.get(id).file !== null).map((id) => [nodes.get(id).file, `{FILE:${id}}`]),
  ];
  const apply = (text) => { let out = text; for (const [lit, tok] of refs) out = out.replaceAll(lit, tok); return out; };
  for (const node of nodes.values()) node.preimage = apply(node.preimage);
  const tokenRE = /\{(?:DIGEST|FILE):([^}]+)\}/g;
  const idsIn = (text) => [...text.matchAll(tokenRE)].map((m) => m[1]);
  return {
    refs,
    // The templates a capture keeps: those referenced by stored text, or by another kept template.
    keep(steps, finalTree, treeTexts) {
      const used = new Set();
      const texts = [...treeTexts];
      for (const st of steps) texts.push(st.stdout, st.stderr, ...st.args, ...Object.values(st.tools).flat());
      for (const t of texts) for (const id of idsIn(t)) used.add(id);
      const queue = [...used];
      while (queue.length) {
        const id = queue.pop();
        const node = nodes.get(id);
        assert.ok(node, `${caseId}: digest reference ${id} has no template`);
        for (const dep of idsIn(node.preimage)) if (!used.has(dep)) { used.add(dep); queue.push(dep); }
      }
      return ids.filter((id) => used.has(id)).map((id) => ({ id, path: nodes.get(id).path, preimage: nodes.get(id).preimage }));
    },
    // Expand every kept template at the original slot, with cycle detection, and require the validated digests.
    selfCheck(templates) {
      const by = new Map(templates.map((t) => [t.id, t]));
      const memo = new Map();
      const resolving = new Set();
      const resolve = (id) => {
        if (memo.has(id)) return memo.get(id);
        const t = by.get(id);
        assert.ok(t, `${caseId}: missing template ${id}`);
        assert.ok(!resolving.has(id), `${caseId}: cyclic digest dependency at ${id}`);
        resolving.add(id);
        const text = t.preimage.replaceAll("{SLOTJ}", slotJ).replace(tokenRE, (m, dep) => {
          const r = resolve(dep);
          return m.startsWith("{FILE:") ? r.file : r.digest;
        });
        resolving.delete(id);
        let out;
        if (id.startsWith("json:")) out = { digest: `sha256:${sha256(Buffer.from(text, "utf8"))}`, file: sha256(Buffer.from(`${text}\n`, "utf8")) };
        else if (id.startsWith("text:")) out = { digest: `sha256:${sha256(Buffer.from(text, "utf8"))}`, file: sha256(Buffer.from(text, "utf8")) };
        else if (id.startsWith("draft:")) out = { digest: `sha256:${sha256(Buffer.from(text, "utf8"))}`, file: null };
        else fail(`unknown template kind ${id}`);
        memo.set(id, out);
        return out;
      };
      for (const t of templates) {
        const got = resolve(t.id);
        const want = nodes.get(t.id);
        assert.equal(got.digest, want.digest, `${caseId}: ${t.id} does not reproduce its digest`);
        assert.equal(got.file, want.file, `${caseId}: ${t.id} does not reproduce its file hash`);
      }
    },
  };
}

// ---------------------------------------------------------------------------------------------
// One case
// ---------------------------------------------------------------------------------------------
// json=true is for text that is itself a JSON document (a repoFiles *.json): the owned paths are then
// JSON-string-escaped (identical to the raw path wherever it has no backslash, quote or control character).
function expand(value, ctx, json = false) {
  const esc = json ? (x) => JSON.stringify(x).slice(1, -1) : (x) => x;
  return value
    .replaceAll("{SLOT}", esc(ctx.slot)).replaceAll("{IN}", esc(join(ctx.slot, "in"))).replaceAll("{REPO}", esc(join(ctx.slot, "repo")))
    .replaceAll("{OTHER}", esc(join(ctx.slot, "other")))
    .replaceAll("{C1}", ctx.shas[0]).replaceAll("{C2}", ctx.shas[1])
    .replace(/\{IMAGE:([a-z]+)\}/g, (_, n) => ctx.images[n])
    .replace(/\{DIGEST:([a-z0-9-]+)\}/g, (_, id) => JSON.parse(readFileSync(join(ctx.slot, "repo", ".faultline", "witnesses", "frozen", `${id}.json`), "utf8")).frozenDigest);
}

async function captureCase({ c, cases, slot, realGit, fakebox, scratch }) {
  for (const suffix of ["", ".tools", ".home", ".tmp"]) {
    assert.ok(!existsSync(slot + suffix), `slot ${slot + suffix} must not exist before ${c.id}`);
  }
  mkdirSync(slot);
  mkdirSync(slot + ".home");
  mkdirSync(slot + ".tmp");
  mkdirSync(join(slot, "in"));
  const shas = buildRepo(realGit, join(slot, "repo"), cases.fixtures[c.fixture]);
  for (const extra of c.extraRepos ?? []) buildRepo(realGit, join(slot, extra), cases.fixtures[c.fixture]);
  buildTools(fakebox, slot, realGit, c.tools ?? {});
  const ctx = { slot, shas, images: cases.images };
  for (const [name, spec] of Object.entries(c.files ?? {})) {
    const p = join(slot, "in", name);
    mkdirSync(dirname(p), { recursive: true });
    writeFileSync(p, fileBytes(spec));
  }
  for (const [name, text] of Object.entries(c.repoFiles ?? {})) {
    const p = join(slot, "repo", name);
    mkdirSync(dirname(p), { recursive: true });
    writeFileSync(p, expand(text, ctx, name.endsWith(".json")));
  }
  const normalizer = makeNormalizer(slot);
  const offsets = {};
  const treeNorm = (refs) => (text, json) => normalizer(text, json, undefined, refs);
  const inputTree = describeTree(snapshotTree(slot), treeNorm([]));
  // Pass 1: run every step and keep its complete raw observation. A slot-derived digest can only be named
  // once the whole case is known (an object created by a later step, or one an earlier stdout mentions).
  const raw = [];
  for (const step of c.steps) {
    const args = step.args.map((a) => expand(a, ctx));
    const cwd = step.cwd ? expand(step.cwd, ctx) : join(slot, "in");
    const env = ownedEnv(slot, { ...(c.env ?? {}), ...(step.env ?? {}) });
    const r = await runStep({ slot, step, args, cwd, env, caseId: c.id, cases, scratch });
    if (process.env.GUIDED_ORACLE_TRACE === "1") process.stderr.write(`TRACE ${c.id}/${step.name}: exit=${r.exit} signal=${r.signal}\nSTDOUT ${JSON.stringify(r.stdout.slice(0, 1500))}\nSTDERR ${JSON.stringify(r.stderr.slice(-800))}\n`);
    checkExpect(c.id, step, r);
    raw.push({ step, args, r, tools: readToolLogs(slot, offsets), entries: snapshotTree(slot) });
  }
  // Pass 2: the validated digest graph of every slot-derived object seen at any step.
  const graph = collectGraph(c.id, slot, raw.map((x) => x.entries));
  const refs = graph.refs;
  const treeTexts = [];
  const steps = raw.map(({ step, args, r, tools, entries }) => {
    const described = describeTree(entries, treeNorm(refs));
    for (const e of described) treeTexts.push(e.content);
    const port = r.url ? /:([0-9]+)$/.exec(r.url)[1] : undefined;
    const n = (text) => normalizer(text, false, port, refs);
    const stdout = n(r.stdout);
    const stderr = n(r.stderr);
    return {
      name: step.name,
      args: args.map((a) => n(a)),
      exit: r.exit,
      signal: r.signal,
      stdout,
      stderr,
      stdoutSha256: sha256(Buffer.from(stdout, "utf8")),
      stderrSha256: sha256(Buffer.from(stderr, "utf8")),
      actions: r.actions,
      tools: Object.fromEntries(Object.entries(tools).map(([k, v]) => [k, v.map((x) => n(x))])),
      treeSha256: sha256(JSON.stringify(compactTree(described))),
    };
  });
  const finalTree = describeTree(raw[raw.length - 1].entries, treeNorm(refs));
  const templates = graph.keep(steps, finalTree, treeTexts);
  // Every kept template must reproduce, for the capture's own slot, exactly the digests it stands for.
  graph.selfCheck(templates);
  // Every case is replayed in full: stdout, tool records and tree are stored for every case, including the
  // localization/proof-export ones, whose slot-derived digests are named by the graph above.
  const capture = {
    schema: captureSchema,
    caseId: c.id,
    group: c.group,
    replay: "FULL",
    fixture: { commits: shas, inputTreeSha256: sha256(JSON.stringify(compactTree(inputTree))) },
    templates,
    steps,
    finalTree,
    finalTreeSha256: sha256(JSON.stringify(compactTree(finalTree))),
  };
  assert.ok(!JSON.stringify(capture).includes(slot), "slot text survived in the capture");
  // Retain the complete evidence of this round before the slot is reused.
  assert.equal(sha256(readFileSync(fakebox)), helperSha(fakebox), "the shared fakebox helper changed during the case");
  const work = join(scratch, "work", c.id);
  mkdirSync(dirname(work), { recursive: true });
  renameSync(slot, work);
  for (const suffix of [".tools", ".home", ".tmp"]) renameSync(slot + suffix, join(scratch, "work", `${c.id}${suffix}`));
  return capture;
}

// ---------------------------------------------------------------------------------------------
// Corpus writing
// ---------------------------------------------------------------------------------------------
const pretty = (v) => `${JSON.stringify(v, null, 2)}\n`;

function writeNew(path, text) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text, { flag: "wx" });
}

function buildBaseline(pins, cases, captureTexts) {
  return {
    schema: oracleSchema,
    source: pins,
    node: { version: process.version },
    generator: { path: generatorPath, mode: "capture" },
    inputs: inputHashes(),
    seed: cases.seed,
    caseIds: cases.cases.map((c) => c.id),
    counts: { cases: cases.cases.length, steps: cases.cases.reduce((n, c) => n + c.steps.length, 0) },
    captures: Object.fromEntries(Object.entries(captureTexts).map(([id, t]) => [id, sha256(Buffer.from(t, "utf8"))])),
  };
}

function buildFakebox(scratch) {
  const out = join(scratch, "fakebox" + (win ? ".exe" : ""));
  const r = spawnSync("go", ["build", "-o", out, "./" + rel.dir + "/fakebox"], {
    cwd: repo, encoding: "utf8", env: { ...process.env, CGO_ENABLED: "0" },
  });
  assert.equal(r.status, 0, `go build fakebox: ${r.stderr}`);
  chmodSync(out, 0o755);
  helperSha(out);
  return out;
}

async function captureAll(opts) {
  assertSameVolume(opts.slot, opts.scratch);
  const pins = measurePins();
  const cases = loadCases();
  if (opts.only) {
    assert.equal(opts.mode, "probe", "--only is only for --probe");
    const keep = new Set(opts.only.split(","));
    cases.cases = cases.cases.filter((c) => keep.has(c.id));
  }
  const realGit = findGit();
  const fakebox = buildFakebox(opts.scratch);
  const captureTexts = {};
  const receipt = { generator: generatorPath, node: process.version, pins, rawProvenance: { slot: opts.slot, platform: process.platform }, cases: [], failures: [] };
  for (const c of cases.cases) {
    const capture = await captureCase({ c, cases, slot: opts.slot, realGit, fakebox, scratch: opts.scratch });
    captureTexts[c.id] = pretty(capture);
    receipt.cases.push(c.id);
    process.stderr.write(`captured ${c.id} (${c.steps.length} steps)\n`);
  }
  const baseline = pretty(buildBaseline(pins, cases, captureTexts));
  return { captureTexts, baseline, receipt };
}

function committedCorpus() {
  const dir = abs(rel.dir);
  const baseline = readFileSync(join(dir, "baseline.json"), "utf8");
  const captures = {};
  for (const name of readdirSync(join(dir, "captures"))) captures[name.replace(/\.json$/, "")] = readFileSync(join(dir, "captures", name), "utf8");
  return { baseline, captures };
}

// A case slot (and its .tools/.home/.tmp siblings) is renamed whole into <scratch>/work/<id>, and a directory
// rename cannot cross volumes. The layout is therefore checked once, before the first case, with the real
// path semantics of the flavor. Windows drive and UNC identity is case-insensitive; this is a layout guard
// only, never a normalization of captured data. There is no copy/delete fallback.
export function volumeOf(p, flavor = win ? "win32" : "posix") {
  const m = flavor === "win32" ? path.win32 : path.posix;
  assert.ok(m.isAbsolute(p), `${p} must be absolute`);
  const root = m.parse(p).root;
  return flavor === "win32" ? root.toLowerCase().replaceAll("/", "\\") : root;
}

export function assertSameVolume(slot, scratch, flavor = win ? "win32" : "posix") {
  const a = volumeOf(slot, flavor);
  const b = volumeOf(scratch, flavor);
  assert.equal(a, b, `slot ${slot} (volume ${a}) and scratch ${scratch} (volume ${b}) are on different volumes; the retained-evidence rename would fail. Put both under one owned base directory.`);
}

// The measured platform of a native capture. The Go names are derived from the Node values and must agree with
// `go env` of the toolchain that built the fakebox and step runner (no emulation, no cross build).
function measurePlatform() {
  const goos = { win32: "windows", linux: "linux", darwin: "darwin" }[process.platform];
  const goarch = { x64: "amd64", arm64: "arm64" }[osArch()];
  assert.ok(goos && goarch, `unsupported native platform ${process.platform}/${osArch()}`);
  assert.equal(process.arch, osArch(), "process.arch and os.arch() disagree");
  const r = spawnSync("go", ["env", "GOOS", "GOARCH"], { encoding: "utf8" });
  assert.equal(r.status, 0, `go env: ${r.stderr}`);
  assert.deepEqual(r.stdout.trim().split(/\r?\n/), [goos, goarch], "go env GOOS/GOARCH differ from the Node platform (cross build or emulation)");
  // Only stable identity goes into the corpus; runner image / OS release are dynamic and live in the receipt.
  return { goos, goarch, nodePlatform: process.platform, nodeArch: process.arch, pathSeparator: win ? "\\" : "/" };
}

function readCorpusDir(dir) {
  const captures = {};
  for (const name of readdirSync(join(dir, "captures"))) captures[name.replace(/\.json$/, "")] = readFileSync(join(dir, "captures", name), "utf8");
  return { baseline: readFileSync(join(dir, "baseline.json"), "utf8"), cases: readFileSync(join(dir, "cases.json"), "utf8"), captures };
}

function parseArgs(argv) {
  const o = { mode: null };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--check" || a === "--capture" || a === "--recapture" || a === "--probe" || a === "--native-capture" || a === "--compare-native") o.mode = a.slice(2);
    else if (a === "--slot" || a === "--scratch" || a === "--out" || a === "--only" || a === "--a" || a === "--b") o[a.slice(2)] = argv[++i];
    else throw new Error(`unknown argument ${a}`);
  }
  if (o.mode === null) throw new Error("need --check, --capture or --recapture");
  for (const k of ["slot", "scratch", "out", "a", "b"]) if (o[k] !== undefined) assert.ok(isAbsolute(o[k]), `--${k} must be absolute`);
  return o;
}

function emptyOrAbsent(dir) {
  return !existsSync(dir) || readdirSync(dir).length === 0;
}

async function main() {
  const o = parseArgs(process.argv.slice(2));
  if (o.mode === "check") {
    const pins = measurePins();
    const dir = abs(rel.dir);
    const baseline = JSON.parse(readFileSync(join(dir, "baseline.json"), "utf8"));
    assert.deepEqual(baseline.source, pins, "baseline source pin differs from measured pins");
    assert.equal(baseline.node.version, process.version);
    assert.deepEqual(baseline.inputs, inputHashes(), "a pinned generator input changed since capture");
    const { captures } = committedCorpus();
    assert.deepEqual(Object.keys(captures).sort(), [...baseline.caseIds].sort(), "capture files do not match the case ids");
    for (const [id, text] of Object.entries(captures)) assert.equal(sha256(Buffer.from(text)), baseline.captures[id], `capture ${id} altered`);
    process.stdout.write(`ok: ${baseline.counts.cases} cases, ${baseline.counts.steps} steps verified\n`);
    return;
  }
  if (o.mode === "compare-native") {
    assert.ok(o.a && o.b, "--a and --b are required");
    const x = readCorpusDir(o.a);
    const y = readCorpusDir(o.b);
    assert.equal(x.baseline, y.baseline, "native baselines differ between the two roots");
    assert.equal(x.cases, y.cases, "native cases.json differs between the two roots");
    assert.deepEqual(Object.keys(x.captures).sort(), Object.keys(y.captures).sort(), "native capture sets differ");
    for (const id of Object.keys(x.captures)) assert.equal(x.captures[id], y.captures[id], `native capture ${id} differs between the two roots`);
    process.stdout.write(`native captures identical (${Object.keys(x.captures).length} cases)\n`);
    return;
  }
  assert.ok(o.slot && o.scratch, "--slot and --scratch are required");
  assert.equal(realpathSync(dirname(o.slot)), dirname(o.slot), "--slot parent must be a real (non-symlink) path");
  assert.ok(emptyOrAbsent(o.scratch), "--scratch must be absent or empty");
  mkdirSync(o.scratch, { recursive: true });
  if (o.mode === "capture") {
    assert.ok(!existsSync(join(abs(rel.dir), "baseline.json")), "a baseline already exists; refusing to overwrite (use --recapture)");
    const { captureTexts, baseline, receipt } = await captureAll(o);
    for (const [id, text] of Object.entries(captureTexts)) writeNew(join(abs(rel.dir), "captures", `${id}.json`), text);
    writeNew(join(abs(rel.dir), "baseline.json"), baseline);
    writeNew(join(o.scratch, "receipt.json"), pretty(receipt));
    process.stdout.write(`captured ${receipt.cases.length} cases\n`);
    return;
  }
  if (o.mode === "native-capture") {
    assert.ok(o.out && emptyOrAbsent(o.out), "--out must be absent or empty");
    const platform = measurePlatform();
    const { captureTexts, baseline, receipt } = await captureAll(o);
    const parsed = JSON.parse(baseline);
    parsed.platform = platform;
    for (const [id, text] of Object.entries(captureTexts)) writeNew(join(o.out, "captures", `${id}.json`), text);
    writeNew(join(o.out, "baseline.json"), pretty(parsed));
    writeNew(join(o.out, "cases.json"), readFileSync(abs(rel.inputs["cases.json"]), "utf8"));
    receipt.platform = platform;
    receipt.dynamic = { osType: osType(), osRelease: osRelease(), slot: o.slot };
    writeNew(join(o.scratch, "receipt.json"), pretty(receipt));
    process.stdout.write(`native capture ${platform.goos}/${platform.goarch}: ${receipt.cases.length} cases\n`);
    return;
  }
  if (o.mode === "probe") {
    const { captureTexts, receipt } = await captureAll(o);
    for (const [id, text] of Object.entries(captureTexts)) writeNew(join(o.scratch, "probe", `${id}.json`), text);
    process.stdout.write(`probed ${receipt.cases.length} cases\n`);
    return;
  }
  assert.ok(o.out && emptyOrAbsent(o.out), "--out must be absent or empty");
  const { captureTexts, baseline, receipt } = await captureAll(o);
  for (const [id, text] of Object.entries(captureTexts)) writeNew(join(o.out, "captures", `${id}.json`), text);
  writeNew(join(o.out, "baseline.json"), baseline);
  writeNew(join(o.scratch, "receipt.json"), pretty(receipt));
  const committed = committedCorpus();
  const diffs = [];
  if (committed.baseline !== baseline) diffs.push("baseline.json");
  for (const id of new Set([...Object.keys(committed.captures), ...Object.keys(captureTexts)])) {
    if (committed.captures[id] !== captureTexts[id]) diffs.push(`captures/${id}.json`);
  }
  if (diffs.length) {
    process.stdout.write(`recapture DIFFERS from the committed corpus: ${diffs.join(", ")}\n`);
    process.exitCode = 1;
  } else {
    process.stdout.write(`recapture is byte-identical to the committed corpus (${receipt.cases.length} cases)\n`);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === generatorFile) {
  main().catch((e) => { process.stderr.write(`${e.stack ?? e}\n`); process.exit(1); });
}
