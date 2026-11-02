// Corpus-level helpers for the serve-bundle oracle: exact metadata, portable
// inventory, record alignment, generator modes and the pre-write preflight.
// Nothing here consults Go output. Duplicate JSON members cannot be seen after
// JSON.parse, so fixtures.json is additionally required to be byte-identical to
// its own canonical re-serialization (a duplicate collapses and breaks that).
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, lstatSync, readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { verifyGitInvestigationProofBundle } from "../../src/git-proof-bundle.js";
import { verifyGitMinimizationResultFile } from "../../src/git-minimization.js";
import { verifyRepairBriefArtifact } from "../../src/repair-brief-store.js";
import { verifyPreventionProof } from "../../src/prevention-proof.js";
import type { ServeBundleCase, ServeBundleGolden } from "./serve-bundle-cases.js";

export const BASELINE = {
  schema: "faultline.serve-bundle-oracle.v1",
  sourceCommit: "ce99a793af0b020bf1b9cf21114f8d7897b37207",
  manifestCommit: "ff5fe7b1120410e60c321f3ae620cf524c5694ec",
  manifestVersion: "0.1.4",
  nodeVersion: "22.22.2"
} as const;
export const BASELINE_TEXT = `${JSON.stringify(BASELINE)}\n`;
export const GITATTRIBUTES_TEXT = "* -text\n";
export const FIXTURES_SCHEMA = "faultline.serve-bundle-fixtures.v1" as const;
export const SHA256_DIGEST = /^sha256:[0-9a-f]{64}$/;
/** The CLI argument that prints the version line checked by preflight. */
export const CLI_VERSION_ARGS = ["--version"] as const;

export type InventoryFile = { path: string; sha256: string; bytes: number };
export type FixtureDigests = {
  proofRoot: string; minimization: string; repair: string; preventionSummary: string; preventionVerified: string;
};
export type FixtureEntry = {
  id: string; root: string; files: InventoryFile[]; digests: FixtureDigests; privateSentinels: string[];
};
export type FixturesFile = { schema: typeof FIXTURES_SCHEMA; fixtures: FixtureEntry[] };

export function isSafeRelative(path: string): boolean {
  if (path === "" || path.startsWith("/") || path.includes("\\") || path.includes("\0")) return false;
  const parts = path.split("/");
  if (parts[0]?.includes(":")) return false;
  return parts.every((part) => part !== "" && part !== "." && part !== "..");
}

function record(value: unknown, label: string, keys: readonly string[]): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error(`${label} must be an object`);
  const object = value as Record<string, unknown>;
  const actual = Object.keys(object).sort().join("|");
  if (actual !== [...keys].sort().join("|")) throw new Error(`${label} must have exactly the keys ${keys.join(", ")}`);
  return object;
}

function text(value: unknown, label: string): string {
  if (typeof value !== "string") throw new Error(`${label} must be a string`);
  return value;
}

function digest(value: unknown, label: string): string {
  const s = text(value, label);
  if (!SHA256_DIGEST.test(s)) throw new Error(`${label} must be sha256:<64 lowercase hex characters>`);
  return s;
}

export function validateFixturesFile(value: unknown): FixturesFile {
  const top = record(value, "fixtures.json", ["schema", "fixtures"]);
  if (top.schema !== FIXTURES_SCHEMA) throw new Error(`fixtures.json schema must be ${FIXTURES_SCHEMA}`);
  if (!Array.isArray(top.fixtures) || top.fixtures.length === 0) throw new Error("fixtures.json must list fixtures");
  const seen = new Set<string>();
  const fixtures = top.fixtures.map((raw: unknown): FixtureEntry => {
    const f = record(raw, "fixture", ["id", "root", "files", "digests", "privateSentinels"]);
    const id = text(f.id, "fixture id");
    if (!/^[a-z0-9][a-z0-9-]*$/.test(id)) throw new Error(`unsafe fixture id ${id}`);
    if (seen.has(id)) throw new Error(`duplicate fixture ${id}`);
    seen.add(id);
    if (f.root !== `inputs/${id}`) throw new Error(`fixture ${id}: root must be inputs/${id}`);
    if (!Array.isArray(f.files)) throw new Error(`fixture ${id}: files must be an array`);
    const paths = new Set<string>();
    const files = f.files.map((rawFile: unknown): InventoryFile => {
      const file = record(rawFile, `fixture ${id} file`, ["path", "sha256", "bytes"]);
      const path = text(file.path, "file path");
      if (!isSafeRelative(path)) throw new Error(`fixture ${id}: unsafe inventory path ${path}`);
      if (paths.has(path)) throw new Error(`fixture ${id}: duplicate inventory path ${path}`);
      paths.add(path);
      const sha = text(file.sha256, "file sha256");
      if (!/^[0-9a-f]{64}$/.test(sha)) throw new Error(`fixture ${id}: ${path} sha256 is malformed`);
      if (typeof file.bytes !== "number" || !Number.isInteger(file.bytes) || file.bytes < 0) {
        throw new Error(`fixture ${id}: ${path} bytes must be a non-negative integer`);
      }
      return { path, sha256: sha, bytes: file.bytes };
    });
    const d = record(f.digests, `fixture ${id} digests`, ["proofRoot", "minimization", "repair", "preventionSummary", "preventionVerified"]);
    const digests: FixtureDigests = {
      proofRoot: digest(d.proofRoot, "proofRoot"), minimization: digest(d.minimization, "minimization"),
      repair: digest(d.repair, "repair"), preventionSummary: digest(d.preventionSummary, "preventionSummary"),
      preventionVerified: digest(d.preventionVerified, "preventionVerified")
    };
    if (!Array.isArray(f.privateSentinels) || f.privateSentinels.some((s: unknown) => typeof s !== "string" || s === "")) {
      throw new Error(`fixture ${id}: privateSentinels must be non-empty strings`);
    }
    return { id, root: `inputs/${id}`, files, digests, privateSentinels: f.privateSentinels as string[] };
  });
  return { schema: FIXTURES_SCHEMA, fixtures };
}

export function serializeFixturesFile(file: FixturesFile): string {
  return `${JSON.stringify(file, null, 2)}\n`;
}

function regularFileText(path: string): string {
  const stat = lstatSync(path);
  if (!stat.isFile()) throw new Error(`${path} is not a regular file`);
  return readFileSync(path, "utf8");
}

/** Regular files only, sorted by path; links and special files are refused. */
export function inventoryDirectory(directory: string): InventoryFile[] {
  const files: InventoryFile[] = [];
  const walk = (current: string, prefix: string): void => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = join(current, entry.name);
      const rel = prefix === "" ? entry.name : `${prefix}/${entry.name}`;
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) throw new Error(`retained fixture contains a symbolic link: ${rel}`);
      if (stat.isDirectory()) { walk(path, rel); continue; }
      if (!stat.isFile()) throw new Error(`retained fixture contains a special file: ${rel}`);
      const bytes = readFileSync(path);
      files.push({ path: rel, sha256: createHash("sha256").update(bytes).digest("hex"), bytes: bytes.length });
    }
  };
  walk(directory, "");
  return files.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
}

export function verifyInventory(directory: string, expected: readonly InventoryFile[]): void {
  const actual = new Map(inventoryDirectory(directory).map((f) => [f.path, f]));
  for (const want of expected) {
    const got = actual.get(want.path);
    if (got === undefined) throw new Error(`inventoried file is missing: ${want.path}`);
    if (got.bytes !== want.bytes) throw new Error(`${want.path} has ${got.bytes} bytes, inventory says ${want.bytes}`);
    if (got.sha256 !== want.sha256) throw new Error(`${want.path} does not match its inventoried sha256`);
    actual.delete(want.path);
  }
  const extra = [...actual.keys()];
  if (extra.length > 0) throw new Error(`file not in the inventory: ${extra.join(", ")}`);
}

/** Strict metadata + inventory validation. Does not read cases or goldens. */
export function validateCorpus(root: string): FixturesFile {
  const dir = resolve(root);
  if (regularFileText(join(dir, "baseline.json")) !== BASELINE_TEXT) throw new Error("baseline.json is not the exact pinned provenance");
  if (regularFileText(join(dir, ".gitattributes")) !== GITATTRIBUTES_TEXT) throw new Error(".gitattributes must be exactly '* -text\\n'");
  const raw = regularFileText(join(dir, "fixtures.json"));
  const file = validateFixturesFile(JSON.parse(raw));
  if (raw !== serializeFixturesFile(file)) throw new Error("fixtures.json is not in canonical form (duplicate or reordered member?)");
  for (const fixture of file.fixtures) verifyInventory(join(dir, fixture.root), fixture.files);
  return file;
}

function readLines(path: string): string[] {
  const body = regularFileText(path);
  if (body === "" || !body.endsWith("\n")) throw new Error(`${path} must be non-empty and end with a newline`);
  const lines = body.slice(0, -1).split("\n");
  if (lines.some((line) => line === "")) throw new Error(`${path} has a blank line`);
  return lines;
}

export type CommittedGolden = { golden: ServeBundleGolden; line: string; page: Buffer | null };

/** cases.jsonl must equal the regenerated table line for line; goldens must align and own their pages. */
export function checkCorpusRecords(root: string, rows: readonly ServeBundleCase[]): CommittedGolden[] {
  const dir = resolve(root);
  const caseLines = readLines(join(dir, "cases.jsonl"));
  if (caseLines.length !== rows.length) throw new Error(`cases.jsonl has ${caseLines.length} rows, expected ${rows.length}`);
  rows.forEach((row, i) => {
    if (caseLines[i] !== JSON.stringify(row)) throw new Error(`cases.jsonl row ${i + 1} differs from the case table (${row.id})`);
  });
  const goldenLines = readLines(join(dir, "golden.jsonl"));
  if (goldenLines.length !== rows.length) throw new Error(`golden.jsonl has ${goldenLines.length} rows, expected ${rows.length}`);
  const referenced = new Set<string>();
  const out = goldenLines.map((line, i): CommittedGolden => {
    const golden = JSON.parse(line) as ServeBundleGolden;
    const row = rows[i];
    if (row === undefined || golden.id !== row.id) throw new Error(`golden.jsonl row ${i + 1} is not ${row?.id}`);
    let page: Buffer | null = null;
    if (golden.page !== null) {
      if (!isSafeRelative(golden.page)) throw new Error(`${golden.id}: unsafe page path`);
      const path = join(dir, "pages", golden.page);
      if (!lstatSync(path).isFile()) throw new Error(`${golden.id}: page is not a regular file`);
      page = readFileSync(path);
      referenced.add(golden.page);
    }
    return { golden, line, page };
  });
  const pagesDir = join(dir, "pages");
  const present = existsSync(pagesDir) ? readdirSync(pagesDir) : [];
  for (const name of present) if (!referenced.has(name)) throw new Error(`pages/${name} is not referenced by any golden`);
  return out;
}

export function computeCoherentDigests(coherent: string): FixtureDigests {
  const base = verifyGitInvestigationProofBundle(join(coherent, "bundle"));
  const min = verifyGitMinimizationResultFile(join(coherent, "minimization.json"));
  const repair = verifyRepairBriefArtifact(join(coherent, ".faultline/repair-briefs/attached"));
  const summary = verifyPreventionProof(join(coherent, ".faultline/prevention-proofs/summary"));
  const verified = verifyPreventionProof(join(coherent, ".faultline/prevention-proofs/verified"));
  if (!base.valid || base.rootDigest === null) throw new Error("coherent bundle does not verify");
  if (!min.valid || min.resultDigest === null) throw new Error("coherent minimization does not verify");
  if (!repair.valid || repair.manifest === null) throw new Error("coherent repair artifact does not verify");
  if (!summary.valid || summary.rootDigest === null) throw new Error("coherent summary prevention does not verify");
  if (!verified.valid || verified.rootDigest === null) throw new Error("coherent verified prevention does not verify");
  return {
    proofRoot: base.rootDigest, minimization: min.resultDigest, repair: repair.manifest.manifestDigest,
    preventionSummary: summary.rootDigest, preventionVerified: verified.rootDigest
  };
}

// ---- generator modes and preflight ----

export type GeneratorMode = { mode: "capture-fixtures" | "recapture" | "check"; only: string | null };

export function parseModes(argv: readonly string[]): GeneratorMode {
  const modes: GeneratorMode["mode"][] = [];
  let only: string | null = null;
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--capture-fixtures") modes.push("capture-fixtures");
    else if (arg === "--recapture") modes.push("recapture");
    else if (arg === "--check") modes.push("check");
    else if (arg === "--only") {
      if (only !== null) throw new Error("--only may be given once");
      const value = argv[i + 1];
      if (value === undefined || value === "" || value.startsWith("--")) throw new Error("--only requires an exact case id");
      only = value;
      i += 1;
    } else throw new Error(`unknown argument: ${arg}`);
  }
  if (modes.length > 1) throw new Error("--capture-fixtures, --recapture and --check are mutually exclusive");
  const mode = modes[0] ?? "check";
  if (mode === "capture-fixtures" && only !== null) throw new Error("--only is a diagnostic selection and cannot be used with --capture-fixtures");
  return { mode, only };
}

export function preflightErrors(input: { nodeVersion: string; manifestVersion: unknown; cliVersionOutput: string }): string[] {
  const errors: string[] = [];
  if (input.nodeVersion !== BASELINE.nodeVersion) errors.push(`Node ${input.nodeVersion} is not the pinned oracle ${BASELINE.nodeVersion}`);
  if (input.manifestVersion !== BASELINE.manifestVersion) errors.push(`package.json version ${String(input.manifestVersion)} is not ${BASELINE.manifestVersion}`);
  if (input.cliVersionOutput !== `FaultLine ${BASELINE.manifestVersion}\n`) {
    errors.push(`fresh CLI printed ${JSON.stringify(input.cliVersionOutput)}, expected "FaultLine ${BASELINE.manifestVersion}\\n"`);
  }
  return errors;
}

/** Runs before any output is written. Throws on any mismatch. */
export function runPreflight(repoRoot = process.cwd()): void {
  const manifest = JSON.parse(readFileSync(join(repoRoot, "package.json"), "utf8")) as { version?: unknown };
  const cli = resolve(repoRoot, "dist/cli.js");
  if (!existsSync(cli)) throw new Error("preflight: dist/cli.js does not exist (build the frozen tree first)");
  const result = spawnSync(process.execPath, [cli, ...CLI_VERSION_ARGS], { encoding: "utf8", timeout: 30_000, cwd: repoRoot });
  const errors = preflightErrors({ nodeVersion: process.versions.node, manifestVersion: manifest.version, cliVersionOutput: result.stdout ?? "" });
  if (result.status !== 0) errors.push(`CLI version command exited ${String(result.status)}`);
  if (errors.length > 0) throw new Error(`preflight failed: ${errors.join("; ")}`);
}

/** First capture writes only to an absent destination. */
export function assertAbsentDestination(path: string): void {
  try {
    lstatSync(path);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
    throw error;
  }
  throw new Error(`capture destination already exists and will not be modified: ${path}`);
}
