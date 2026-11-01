// Supplemental serve-bundle binding corpus (difftest/testdata/serve-bundle-binding).
// The original serve-bundle corpus is never modified. This module builds five
// self-consistent minimization records that the frozen TS minimization verifier
// accepts (valid, exact recomputed digest) but that the serve --bundle adapter
// must refuse at its Git-proof binding checks, and it validates/captures the
// corpus holding them. Goldens are only ever produced by running the actual
// `node dist/cli.js serve --bundle ...`; nothing is authored here.
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { cpSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { canonicalJson, digestJson } from "../../src/canonical.js";
import { verifyGitMinimizationResult } from "../../src/git-minimization.js";
import {
  BASELINE_TEXT, GITATTRIBUTES_TEXT, SHA256_DIGEST, assertAbsentDestination, validateCorpus, type InventoryFile
} from "./serve-bundle-corpus.js";

export const BINDING_SCHEMA = "faultline.serve-bundle-binding.v1" as const;
export const BINDING_NAMES = ["foreign-frozen", "foreign-witness", "witness-null", "nullable-states", "different-range"] as const;
export type BindingName = (typeof BINDING_NAMES)[number];
/** The file name every case's retained record has inside the copied fixture root. */
export const BINDING_INPUT_NAME = "binding-minimization.json";
const MINIMIZATION_SCHEMA_VERSION = "faultline.git-minimization.v1";
const SANDBOX_V2 = "faultline.sandbox.v2";
const SANDBOX_V1 = "faultline.sandbox.v1";

export const bindingId = (name: BindingName): string => `binding-${name}`;
export const bindingInputPath = (name: BindingName): string => `minimization/${name}.json`;

type J = Record<string, unknown>;
const asJ = (value: unknown): J => {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error("expected a JSON object");
  return value as J;
};
const asS = (value: unknown): string => {
  if (typeof value !== "string") throw new Error("expected a string");
  return value;
};
const asA = (value: unknown): unknown[] => {
  if (!Array.isArray(value)) throw new Error("expected a JSON array");
  return value;
};
const flipHex = (hex: string): string => `${hex.startsWith("0") ? "1" : "0"}${hex.slice(1)}`;
const flipDigest = (digest: string): string => `sha256:${flipHex(digest.slice("sha256:".length))}`;
const sha = (data: Buffer | string): string => createHash("sha256").update(data).digest("hex");

// ---- source records ----

export type BindingSource = {
  minimization: J; investigation: J; proofRoot: string; minimizationDigest: string; minimizationFile: string; minimizationSha256: string;
};

/** Reads the retained coherent fixture of the original corpus after its strict validation. */
export function loadBindingSource(parentRoot: string): BindingSource {
  const parent = resolve(parentRoot);
  const fixtures = validateCorpus(parent);
  const coherent = fixtures.fixtures.find((f) => f.id === "coherent");
  if (coherent === undefined) throw new Error("parent corpus has no coherent fixture");
  const file = coherent.files.find((f) => f.path === "minimization.json");
  if (file === undefined) throw new Error("coherent fixture has no minimization.json");
  const base = join(parent, coherent.root);
  return {
    minimization: asJ(JSON.parse(readFileSync(join(base, "minimization.json"), "utf8"))),
    investigation: asJ(JSON.parse(readFileSync(join(base, "bundle", "investigation.json"), "utf8"))),
    proofRoot: coherent.digests.proofRoot,
    minimizationDigest: coherent.digests.minimization,
    minimizationFile: "minimization.json",
    minimizationSha256: file.sha256
  };
}

// ---- identities ----

function executionIdOf(run: J): string {
  const application = asJ(run.application);
  return digestJson({
    schemaVersion: MINIMIZATION_SCHEMA_VERSION, nonce: run.executionNonce, role: run.role, roleAttempt: run.roleAttempt,
    direction: application.direction, base: application.base, candidateUnitIds: run.candidateUnitIds, frozenDigest: run.frozenDigest
  });
}
function runIdOf(run: J): string {
  const { runId: _runId, ...unsigned } = run;
  return digestJson(unsigned);
}

/** The identity formulas restated here must reproduce every id of a verifier-valid record. */
export function assertIdentitiesConsistent(result: J): void {
  for (const raw of asA(result.runs)) {
    const run = asJ(raw);
    if (run.executionId !== executionIdOf(run)) throw new Error(`execution id formula disagrees with the record: ${String(run.runId)}`);
    if (run.runId !== runIdOf(run)) throw new Error(`run id formula disagrees with the record: ${String(run.runId)}`);
  }
}

/** Recomputes every run identity after an edit and rewrites every reference to them. */
function reidentifyRuns(result: J): void {
  const runIds = new Map<string, string>();
  const executionIds = new Map<string, string>();
  for (const raw of asA(result.runs)) {
    const run = asJ(raw);
    const oldRun = asS(run.runId);
    const oldExecution = asS(run.executionId);
    run.executionId = executionIdOf(run);
    run.runId = runIdOf(run);
    runIds.set(oldRun, asS(run.runId));
    executionIds.set(oldExecution, asS(run.executionId));
  }
  const mapped = (map: Map<string, string>, value: unknown): string => {
    const found = map.get(asS(value));
    if (found === undefined) throw new Error(`unreferenced identity ${String(value)}`);
    return found;
  };
  for (const raw of asA(result.attempts)) {
    const attempt = asJ(raw);
    if (attempt.runId !== null) attempt.runId = mapped(runIds, attempt.runId);
  }
  const certification = asJ(result.certification);
  for (const key of ["sufficiency", "necessity"]) {
    const certificate = asJ(certification[key]);
    certificate.runIds = asA(certificate.runIds).map((id) => mapped(runIds, id));
    certificate.executionIds = asA(certificate.executionIds).map((id) => mapped(executionIds, id));
  }
}

function sandboxPolicyPayload(audit: J, witnessDigest: string, version: string): J {
  const runtime = asJ(audit.runtime);
  return {
    schemaVersion: version, kind: "DOCKER_ISOLATED", image: runtime.image, witnessDigest,
    source: { target: version === SANDBOX_V2 ? "/workspace/src" : "/workspace", readOnly: true },
    network: "none", rootFilesystem: "read-only", user: "65534:65534", capDrop: "ALL", noNewPrivileges: true, pull: "never",
    entrypoint: "/bin/sh", limits: runtime.limits, environmentPolicyDigest: audit.environmentPolicyDigest
  };
}

/** Moves a Docker sandbox audit to another frozen witness digest and recomputes its policy digest. */
function rebindSandbox(audit: J, frozenDigest: string): void {
  const current = asS(audit.witnessDigest);
  const version = [SANDBOX_V2, SANDBOX_V1].find((v) => digestJson(sandboxPolicyPayload(audit, current, v)) === audit.policyDigest);
  if (version === undefined) throw new Error("sandbox policy digest matches neither the current nor the legacy payload");
  audit.witnessDigest = frozenDigest;
  audit.policyDigest = digestJson(sandboxPolicyPayload(audit, frozenDigest, version));
}

// ---- the five records ----

function endpoint(value: unknown): J {
  return { commit: asS(asJ(value).commit), tree: asS(asJ(value).tree) };
}

/**
 * Each record keeps every verifier constraint satisfied by editing the summary
 * together with the run facts, sandbox audits, identities and references that
 * depend on it. A record that cannot satisfy the real verifier is an error, never
 * a weaker fixture (see verifiedDigest).
 */
export function buildBindingRecords(source: J, investigation: J): Record<BindingName, J> {
  assertIdentitiesConsistent(source);
  const clone = (): J => structuredClone(source);
  const runsOf = (result: J): J[] => asA(result.runs).map(asJ);

  const foreignFrozen = clone();
  {
    const witness = asJ(foreignFrozen.witness);
    const replacement = flipDigest(asS(witness.frozenDigest));
    witness.frozenDigest = replacement;
    for (const run of runsOf(foreignFrozen)) {
      run.frozenDigest = replacement;
      if (run.sandbox !== null) rebindSandbox(asJ(run.sandbox), replacement);
    }
    reidentifyRuns(foreignFrozen);
  }

  const foreignWitness = clone();
  {
    const witness = asJ(foreignWitness.witness);
    const replacement = flipDigest(asS(witness.witnessDigest));
    witness.witnessDigest = replacement;
    for (const run of runsOf(foreignWitness)) run.witnessDigest = replacement;
    reidentifyRuns(foreignWitness);
  }

  const witnessNull = clone();
  witnessNull.witness = null;

  // A recorder result that stopped before any state was resolved: the shape the
  // frozen recorder's baseResult produces for RANGE_ERROR, with the witness
  // summary kept bound to the incident.
  const reason = "The supplied revisions could not be resolved to commits.";
  const nullableStates: J = {
    schemaVersion: MINIMIZATION_SCHEMA_VERSION, recorder: "git-diff-counterfactual-minimization", nativeCodexInterception: false,
    status: "RANGE_ERROR", repository: null, before: null, after: null, witness: structuredClone(source.witness),
    budget: { usedExecutions: 0, maxExecutions: asJ(source.budget).maxExecutions },
    patchUnits: [], candidateUnitIds: [], attempts: [], runs: [],
    minimality: {
      oneMinimal: false, reason: "No candidate was established.",
      enumeration: { status: "NOT_RUN", searchedCandidateSets: 0, maxPatchUnits: 10, maxDistinctMinimalSets: 8, distinctMinimalSets: [], note: "Enumeration did not run." },
      nonMonotonicInteraction: { verdict: "INCONCLUSIVE", evidence: null, note: "No subset outcome matrix was retained." }
    },
    certification: {
      sufficiency: { direction: "FORWARD_FROM_BEFORE", expectedOutcome: "FAIL", requiredExecutions: 3, runIds: [], executionIds: [], status: "NOT_RUN", note: "No sufficiency certification was run." },
      necessity: { direction: "REVERSE_FROM_AFTER", expectedOutcome: "PASS", requiredExecutions: 3, runIds: [], executionIds: [], status: "NOT_RUN", note: "No necessity certification was run." }
    },
    proof: {
      requiresDockerIsolation: true, dockerIsolated: true, executionTrust: "NATIVE_DOCKER", sufficiencyCertified: false,
      necessityCertified: false, isProof: false, reason
    },
    errors: [reason]
  };

  const differentRange = clone();
  {
    const transitions = asA(investigation.transitions).map(asJ);
    const recovery = transitions.find((t) => t.kind === "FAIL_TO_PASS");
    if (recovery === undefined) throw new Error("investigation has no FAIL_TO_PASS transition");
    const before = endpoint(recovery.before);
    const after = endpoint(recovery.after);
    differentRange.before = before;
    differentRange.after = after;
    for (const run of runsOf(differentRange)) {
      const application = asJ(run.application);
      application.base = application.direction === "FORWARD_FROM_BEFORE" ? structuredClone(before) : structuredClone(after);
    }
    reidentifyRuns(differentRange);
  }

  return {
    "foreign-frozen": foreignFrozen, "foreign-witness": foreignWitness, "witness-null": witnessNull,
    "nullable-states": nullableStates, "different-range": differentRange
  };
}

export const serializeRecord = (record: J): string => `${canonicalJson(record)}\n`;

/**
 * The real frozen TS verifier must accept the retained bytes and report MATCH for
 * their own recomputed digest; returns that digest.
 */
export function verifiedDigest(text: string): string {
  const parsed: unknown = JSON.parse(text);
  const first = verifyGitMinimizationResult(parsed);
  if (!first.valid || first.resultDigest === null) throw new Error(`record is not verifier-valid: ${first.errors.join("; ")}`);
  const second = verifyGitMinimizationResult(parsed, first.resultDigest);
  if (!second.valid || second.externalDigestStatus !== "MATCH") throw new Error(`record does not match its own digest: ${second.errors.join("; ")}`);
  return first.resultDigest;
}

// ---- cases, goldens, inventory ----

export type BindingCase = { id: string; input: string; expectedRoot: string; expectedMinimizationDigest: string; argv: string[] };
export type BindingGolden = { id: string; process: { stdout: string; stderr: string; exitCode: number; signal: null } };
export type BindingInventory = {
  schema: typeof BINDING_SCHEMA;
  source: { corpus: "serve-bundle"; fixture: "coherent"; proofRoot: string; minimizationDigest: string; minimizationFile: string; minimizationSha256: string };
  files: InventoryFile[];
};

/** The only command shape a case may run; {ROOT} is the copied coherent fixture. --port abc keeps any unexpected acceptance from serving. */
export function bindingArgv(expectedRoot: string, digest: string): string[] {
  return ["serve", "--bundle", "{ROOT}/bundle", "--expect-root", expectedRoot, "--minimization", `{ROOT}/${BINDING_INPUT_NAME}`,
    "--expect-minimization", digest, "--port", "abc"];
}

export function makeBindingCases(texts: Record<BindingName, string>, proofRoot: string): BindingCase[] {
  return BINDING_NAMES.map((name) => {
    const digest = verifiedDigest(texts[name]);
    return { id: bindingId(name), input: bindingInputPath(name), expectedRoot: proofRoot, expectedMinimizationDigest: digest, argv: bindingArgv(proofRoot, digest) };
  });
}

export const serializeCases = (cases: readonly BindingCase[]): string => cases.map((c) => `${JSON.stringify(c)}\n`).join("");
export const serializeGoldens = (goldens: readonly BindingGolden[]): string =>
  goldens.map((g) => `${JSON.stringify({ id: g.id, process: { stdout: g.process.stdout, stderr: g.process.stderr, exitCode: g.process.exitCode, signal: g.process.signal } })}\n`).join("");
export const serializeInventory = (inventory: BindingInventory): string => `${JSON.stringify(inventory, null, 2)}\n`;

export function makeInventory(texts: Record<BindingName, string>, source: BindingSource): BindingInventory {
  const files = BINDING_NAMES.map((name): InventoryFile => ({ path: bindingInputPath(name), sha256: sha(texts[name]), bytes: Buffer.byteLength(texts[name]) }));
  return {
    schema: BINDING_SCHEMA,
    source: { corpus: "serve-bundle", fixture: "coherent", proofRoot: source.proofRoot, minimizationDigest: source.minimizationDigest, minimizationFile: source.minimizationFile, minimizationSha256: source.minimizationSha256 },
    files: files.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
  };
}

export type BindingParts = { texts: Record<BindingName, string>; cases: BindingCase[]; goldens: BindingGolden[]; source: BindingSource };

/** First write only: refuses any existing destination and writes nothing else. */
export function writeBindingCorpus(destination: string, parts: BindingParts): void {
  assertAbsentDestination(destination);
  mkdirSync(join(destination, "minimization"), { recursive: true });
  writeFileSync(join(destination, "baseline.json"), BASELINE_TEXT);
  writeFileSync(join(destination, ".gitattributes"), GITATTRIBUTES_TEXT);
  for (const name of BINDING_NAMES) writeFileSync(join(destination, bindingInputPath(name)), parts.texts[name]);
  writeFileSync(join(destination, "cases.jsonl"), serializeCases(parts.cases));
  writeFileSync(join(destination, "golden.jsonl"), serializeGoldens(parts.goldens));
  writeFileSync(join(destination, "inventory.json"), serializeInventory(makeInventory(parts.texts, parts.source)));
}

// ---- strict validation of a retained corpus ----

function regularText(path: string): string {
  if (!lstatSync(path).isFile()) throw new Error(`${path} is not a regular file`);
  return readFileSync(path, "utf8");
}

/** The only directory the corpus may hold besides its root. */
const BINDING_DIRECTORIES = ["minimization"] as const;

function listFiles(root: string): string[] {
  const rootStat = lstatSync(root);
  if (rootStat.isSymbolicLink()) throw new Error(`binding corpus root is a symbolic link: ${root}`);
  if (!rootStat.isDirectory()) throw new Error(`binding corpus root is not a directory: ${root}`);
  const out: string[] = [];
  const walk = (current: string, prefix: string): void => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = join(current, entry.name);
      const rel = prefix === "" ? entry.name : `${prefix}/${entry.name}`;
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) throw new Error(`binding corpus contains a symbolic link: ${rel}`);
      if (stat.isDirectory()) {
        if (!(BINDING_DIRECTORIES as readonly string[]).includes(rel)) throw new Error(`binding corpus has an unexpected directory: ${rel}`);
        walk(path, rel);
        continue;
      }
      if (!stat.isFile()) throw new Error(`binding corpus contains a special file: ${rel}`);
      out.push(rel);
    }
  };
  walk(root, "");
  return out.sort();
}

function exactKeys(value: unknown, label: string, keys: readonly string[]): J {
  const object = asJ(value);
  if (Object.keys(object).sort().join("|") !== [...keys].sort().join("|")) throw new Error(`${label} must have exactly the keys ${keys.join(", ")}`);
  return object;
}

function readLines(path: string): string[] {
  const body = regularText(path);
  if (body === "" || !body.endsWith("\n")) throw new Error(`${path} must be non-empty and end with a newline`);
  const lines = body.slice(0, -1).split("\n");
  if (lines.some((line) => line === "")) throw new Error(`${path} has a blank line`);
  return lines;
}

export type ValidatedBinding = { inventory: BindingInventory; cases: BindingCase[]; goldens: BindingGolden[]; texts: Record<BindingName, string> };

/**
 * Checks exact provenance, the exact file set (no missing, duplicate, extra,
 * link or special entry), per-file bytes and hashes, the exact five ids in
 * order, the fixed argv, the verifier-valid digests, and golden shape. It runs
 * nothing and compares no output.
 */
export function validateBindingCorpus(root: string, parentRoot: string): ValidatedBinding {
  const dir = resolve(root);
  const present = listFiles(dir); // lstats the root; rejects links, special files and unexpected directories
  if (regularText(join(dir, "baseline.json")) !== BASELINE_TEXT) throw new Error("baseline.json is not the exact pinned provenance");
  if (regularText(join(dir, ".gitattributes")) !== GITATTRIBUTES_TEXT) throw new Error(".gitattributes must be exactly '* -text\\n'");
  const expectedFiles = [".gitattributes", "baseline.json", "cases.jsonl", "golden.jsonl", "inventory.json", ...BINDING_NAMES.map(bindingInputPath)].sort();
  const missing = expectedFiles.filter((f) => !present.includes(f));
  const extra = present.filter((f) => !expectedFiles.includes(f));
  if (missing.length > 0) throw new Error(`binding corpus is missing: ${missing.join(", ")}`);
  if (extra.length > 0) throw new Error(`binding corpus has unexpected files: ${extra.join(", ")}`);

  const source = loadBindingSource(parentRoot);
  const rawInventory = regularText(join(dir, "inventory.json"));
  const top = exactKeys(JSON.parse(rawInventory), "inventory.json", ["schema", "source", "files"]);
  if (top.schema !== BINDING_SCHEMA) throw new Error(`inventory schema must be ${BINDING_SCHEMA}`);
  const src = exactKeys(top.source, "inventory source", ["corpus", "fixture", "proofRoot", "minimizationDigest", "minimizationFile", "minimizationSha256"]);
  const wantSource = { corpus: "serve-bundle", fixture: "coherent", proofRoot: source.proofRoot, minimizationDigest: source.minimizationDigest, minimizationFile: source.minimizationFile, minimizationSha256: source.minimizationSha256 };
  if (JSON.stringify(src) !== JSON.stringify(wantSource)) throw new Error("inventory source does not match the retained coherent fixture of the original corpus");
  const files = asA(top.files).map((raw): InventoryFile => {
    const f = exactKeys(raw, "inventory file", ["path", "sha256", "bytes"]);
    if (typeof f.path !== "string" || typeof f.sha256 !== "string" || typeof f.bytes !== "number") throw new Error("inventory file has the wrong member types");
    return { path: f.path, sha256: f.sha256, bytes: f.bytes };
  });
  const inventory: BindingInventory = { schema: BINDING_SCHEMA, source: wantSource as BindingInventory["source"], files };
  if (rawInventory !== serializeInventory(inventory)) throw new Error("inventory.json is not in canonical form (duplicate or reordered member?)");
  const wantPaths = BINDING_NAMES.map(bindingInputPath).sort();
  if (files.map((f) => f.path).join("|") !== wantPaths.join("|")) throw new Error(`inventory must list exactly ${wantPaths.join(", ")} once each, in order`);

  const texts = {} as Record<BindingName, string>;
  for (const name of BINDING_NAMES) {
    const entry = files.find((f) => f.path === bindingInputPath(name));
    const bytes = readFileSync(join(dir, bindingInputPath(name)));
    if (entry === undefined || entry.bytes !== bytes.length) throw new Error(`${bindingInputPath(name)} has ${bytes.length} bytes, inventory says ${String(entry?.bytes)}`);
    if (entry.sha256 !== sha(bytes)) throw new Error(`${bindingInputPath(name)} does not match its inventoried sha256`);
    texts[name] = bytes.toString("utf8");
  }

  const caseLines = readLines(join(dir, "cases.jsonl"));
  if (caseLines.length !== BINDING_NAMES.length) throw new Error(`cases.jsonl has ${caseLines.length} rows, expected ${BINDING_NAMES.length}`);
  const cases = caseLines.map((line, i): BindingCase => {
    const name = BINDING_NAMES[i]!;
    const row = exactKeys(JSON.parse(line), `case row ${i + 1}`, ["id", "input", "expectedRoot", "expectedMinimizationDigest", "argv"]);
    if (row.id !== bindingId(name)) throw new Error(`case row ${i + 1} is not ${bindingId(name)}`);
    if (row.input !== bindingInputPath(name)) throw new Error(`${bindingId(name)}: input path differs`);
    if (row.expectedRoot !== source.proofRoot) throw new Error(`${bindingId(name)}: expectedRoot is not the original base root`);
    const digest = verifiedDigest(texts[name]);
    if (row.expectedMinimizationDigest !== digest || !SHA256_DIGEST.test(digest)) throw new Error(`${bindingId(name)}: digest is not the verifier's recomputed digest`);
    if (JSON.stringify(row.argv) !== JSON.stringify(bindingArgv(source.proofRoot, digest))) throw new Error(`${bindingId(name)}: argv is not the fixed serve contract`);
    const canonicalRow: BindingCase = { id: bindingId(name), input: bindingInputPath(name), expectedRoot: source.proofRoot, expectedMinimizationDigest: digest, argv: bindingArgv(source.proofRoot, digest) };
    // JSON.parse keeps the last of a duplicated member; the exact serialization does not.
    if (line !== JSON.stringify(canonicalRow)) throw new Error(`${bindingId(name)}: case row is not canonical (duplicate or reordered member?)`);
    return canonicalRow;
  });
  if (new Set(cases.map((c) => c.expectedMinimizationDigest)).size !== cases.length || cases.some((c) => c.expectedMinimizationDigest === source.minimizationDigest)) {
    throw new Error("binding records must have distinct digests, all unlike the original minimization");
  }

  const goldenLines = readLines(join(dir, "golden.jsonl"));
  if (goldenLines.length !== BINDING_NAMES.length) throw new Error(`golden.jsonl has ${goldenLines.length} rows, expected ${BINDING_NAMES.length}`);
  const goldens = goldenLines.map((line, i): BindingGolden => {
    const row = exactKeys(JSON.parse(line), `golden row ${i + 1}`, ["id", "process"]);
    if (row.id !== bindingId(BINDING_NAMES[i]!)) throw new Error(`golden row ${i + 1} is not ${bindingId(BINDING_NAMES[i]!)}`);
    const p = exactKeys(row.process, `golden ${String(row.id)} process`, ["stdout", "stderr", "exitCode", "signal"]);
    if (typeof p.stdout !== "string" || typeof p.stderr !== "string") throw new Error(`${String(row.id)}: stdout/stderr must be strings`);
    if (typeof p.exitCode !== "number" || !Number.isInteger(p.exitCode) || p.exitCode === 0 || p.signal !== null) {
      throw new Error(`${String(row.id)}: a binding refusal must be a completed nonzero exit with no signal`);
    }
    if (line !== JSON.stringify({ id: row.id, process: { stdout: p.stdout, stderr: p.stderr, exitCode: p.exitCode, signal: null } })) throw new Error(`${String(row.id)}: golden row is not canonical`);
    return { id: String(row.id), process: { stdout: p.stdout, stderr: p.stderr, exitCode: p.exitCode, signal: null } };
  });
  return { inventory, cases, goldens, texts };
}

// ---- capture from the actual frozen CLI ----

const resolveToken = (arg: string, caseRoot: string): string => {
  if (arg === "{ROOT}") return caseRoot;
  if (arg.startsWith("{ROOT}/")) return join(caseRoot, arg.slice("{ROOT}/".length));
  return arg;
};

function assertNoLinks(directory: string): void {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    const stat = lstatSync(path);
    if (stat.isSymbolicLink() || (!stat.isFile() && !stat.isDirectory())) throw new Error(`retained fixture has a link or special file: ${path}`);
    if (stat.isDirectory()) assertNoLinks(path);
  }
}

/** Runs one case against a fresh owned copy of the original coherent fixture; scratch is retained. */
export function captureBindingCase(parentRoot: string, bindingRoot: string, row: BindingCase): { golden: BindingGolden; scratch: string } {
  const source = join(resolve(parentRoot), "inputs", "coherent");
  assertNoLinks(source);
  const scratch = mkdtempSync(join(tmpdir(), "faultline-serve-binding-"));
  const caseRoot = join(scratch, "case");
  cpSync(source, caseRoot, { recursive: true, errorOnExist: true, force: false, dereference: false, verbatimSymlinks: true });
  cpSync(join(resolve(bindingRoot), row.input), join(caseRoot, BINDING_INPUT_NAME), { errorOnExist: true, force: false });
  const argv = row.argv.map((arg) => resolveToken(arg, caseRoot));
  const result = spawnSync(process.execPath, [resolve("dist/cli.js"), ...argv], {
    cwd: caseRoot, encoding: "utf8", timeout: 60_000, env: { ...process.env, FAULTLINE_NO_BROWSER: "1" }
  });
  if (result.error !== undefined) throw result.error;
  if (result.signal !== null || result.status === null) throw new Error(`${row.id}: process did not complete (signal ${String(result.signal)})`);
  const normalize = (text: string): string => text.split(caseRoot).join("{ROOT}");
  console.log(`captured ${row.id} (scratch preserved at ${scratch})`);
  return { golden: { id: row.id, process: { stdout: normalize(result.stdout), stderr: normalize(result.stderr), exitCode: result.status, signal: null } }, scratch };
}
