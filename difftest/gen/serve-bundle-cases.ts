// Explicit serve-bundle case table. Every case has a stable literal ID and
// exact literal argv; nothing is generated from the Go side. The table is
// enumerated from a real corpus directory: its fixtures.json carries the
// nondeterministic roots/digests of the coherent fixture, and
// inputs/coherent/mutations/index.json carries the TS-recomputed digests of
// the explicit fixture-copy mutations (see serve-bundle-variants.ts). Paths in
// `options` are fixture-relative; `argv` uses whole-argument "{ROOT}/..." and
// "{PORT}" tokens that only the capture/replay adapters expand.
//
// Convention: case "cli-options-relative-paths" is run with its working
// directory set to the copied fixture root so its relative argv resolves.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { SHA256_DIGEST, validateFixturesFile, type FixtureDigests } from "./serve-bundle-corpus.js";

export type ServeBundleCase = {
  id: string;
  kind: "view" | "cli-error" | "cli-serve";
  fixture: string;
  expectedRoot: string | null;
  options: {
    minimizationFile?: string; expectedMinimizationDigest?: string;
    repairDirectory?: string; expectedRepairDigest?: string;
    preventionDirectory?: string; expectedPreventionDigest?: string;
  };
  argv: string[];
};
export type ServeBundleGolden = {
  id: string;
  valueJSON: string | null;
  error: string | null;
  page: string | null;
  process: { stdout: string; stderr: string; exitCode: number | null; signal: string | null } | null;
};

export const MUTATIONS_INDEX_SCHEMA = "faultline.serve-bundle-mutations.v1" as const;
export const MUTATIONS_INDEX_PATH = "inputs/coherent/mutations/index.json";

/** Every fixture-relative artifact whose digest the index must carry. */
export const MUTATION_ARTIFACTS = [
  "minimization-uncertified.json",
  "mutations/min/witness-null.json", "mutations/min/frozen-wrong.json", "mutations/min/witness-wrong.json",
  "mutations/min/before-null.json", "mutations/min/after-null.json", "mutations/min/before-commit.json",
  "mutations/min/before-tree.json", "mutations/min/after-commit.json", "mutations/min/after-tree.json",
  "mutations/min/wrong-transition.json",
  "mutations/repair/investigation-wrong", "mutations/repair/frozen-wrong", "mutations/repair/legacy",
  "mutations/repair/citations-invalid", "mutations/repair/manifest-malformed", "mutations/repair/packet-malformed",
  "mutations/repair/brief-malformed", "mutations/repair/manifest-content",
  ".faultline/prevention-proofs/no-investigation",
  "mutations/prevention/root-wrong", "mutations/prevention/witness-wrong", "mutations/prevention/investigation-wrong",
  "mutations/prevention/runs-wrong", "mutations/prevention/runs-missing"
] as const;

/** Never equal to a real artifact digest (all zero-nibble f). */
export const WRONG_DIGEST = `sha256:${"f".repeat(64)}`;
/** Valid-shape digest guaranteed unlike the wrong-root and real roots. */
const OTHER_ROOT = `sha256:${"e".repeat(64)}`;

const CERTIFIED = "minimization.json";
const REPAIR = ".faultline/repair-briefs/attached";
const SUMMARY = ".faultline/prevention-proofs/summary";
const VERIFIED = ".faultline/prevention-proofs/verified";
const B = "{ROOT}/bundle";
const R = (path: string): string => `{ROOT}/${path}`;

function readMutationDigests(root: string): Record<string, string> {
  const parsed = JSON.parse(readFileSync(join(root, MUTATIONS_INDEX_PATH), "utf8")) as { schema?: unknown; digests?: unknown };
  if (parsed.schema !== MUTATIONS_INDEX_SCHEMA || typeof parsed.digests !== "object" || parsed.digests === null) {
    throw new Error("mutations index has the wrong schema");
  }
  const digests = parsed.digests as Record<string, unknown>;
  const out: Record<string, string> = {};
  for (const path of MUTATION_ARTIFACTS) {
    const value = digests[path];
    if (typeof value !== "string" || !SHA256_DIGEST.test(value)) throw new Error(`mutations index lacks a digest for ${path}`);
    out[path] = value;
  }
  if (Object.keys(digests).length !== MUTATION_ARTIFACTS.length) throw new Error("mutations index has unexpected entries");
  return out;
}

export function serveBundleCases(corpusRoot: string): ServeBundleCase[] {
  const fixtures = validateFixturesFile(JSON.parse(readFileSync(join(corpusRoot, "fixtures.json"), "utf8")));
  const coherent = fixtures.fixtures.find((f) => f.id === "coherent");
  if (coherent === undefined) throw new Error("fixtures.json has no coherent fixture");
  const d: FixtureDigests = coherent.digests;
  const m = readMutationDigests(corpusRoot);
  const rows: ServeBundleCase[] = [];
  type Opts = ServeBundleCase["options"];

  const min = (path = CERTIFIED, digest = d.minimization): Opts => ({ minimizationFile: path, expectedMinimizationDigest: digest });
  const rep = (path = REPAIR, digest = d.repair): Opts => ({ repairDirectory: path, expectedRepairDigest: digest });
  const prev = (path = SUMMARY, digest = d.preventionSummary): Opts => ({ preventionDirectory: path, expectedPreventionDigest: digest });
  const add = (id: string, kind: ServeBundleCase["kind"], expectedRoot: string | null, options: Opts, argv: string[]): void => {
    rows.push({ id, kind, fixture: "coherent", expectedRoot, options, argv });
  };
  const view = (id: string, options: Opts, expectedRoot: string | null = d.proofRoot): void => add(id, "view", expectedRoot, options, []);
  const flags = (options: Opts): string[] => [
    ...(options.minimizationFile === undefined ? [] : ["--minimization", R(options.minimizationFile)]),
    ...(options.expectedMinimizationDigest === undefined ? [] : ["--expect-minimization", options.expectedMinimizationDigest]),
    ...(options.repairDirectory === undefined ? [] : ["--repair", R(options.repairDirectory)]),
    ...(options.expectedRepairDigest === undefined ? [] : ["--expect-repair", options.expectedRepairDigest]),
    ...(options.preventionDirectory === undefined ? [] : ["--prevention", R(options.preventionDirectory)]),
    ...(options.expectedPreventionDigest === undefined ? [] : ["--expect-prevention", options.expectedPreventionDigest])
  ];
  const serve = (options: Opts, expectedRoot: string | null, extra: string[] = []): string[] => [
    "serve", "--bundle", B, ...(expectedRoot === null ? [] : ["--expect-root", expectedRoot]), ...flags(options), ...extra
  ];
  const combo = (bits: string): Opts => ({
    ...(bits[0] === "1" ? min() : {}), ...(bits[1] === "1" ? rep() : {}), ...(bits[2] === "1" ? prev() : {})
  });
  /** Registers a view case and, for CLI-reachable rejections, its cli-reject twin. */
  const viewWithReject = (id: string, options: Opts): void => {
    view(id, options);
    add(`cli-reject-${id}`, "cli-error", d.proofRoot, options, serve(options, d.proofRoot));
  };

  for (const bits of ["000", "001", "010", "011", "100", "101", "110", "111"]) view(`view-combination-${bits}`, combo(bits));

  view("view-root-absent", {}, null);
  view("view-root-match", {});
  view("view-root-wrong", {}, OTHER_ROOT);

  view("view-min-uncertified", min("minimization-uncertified.json", m["minimization-uncertified.json"]!));
  view("view-min-expected-absent", { minimizationFile: CERTIFIED });
  view("view-min-expected-match", min());
  viewWithReject("view-min-expected-wrong", min(CERTIFIED, WRONG_DIGEST));
  view("view-min-witness-null", min("mutations/min/witness-null.json", m["mutations/min/witness-null.json"]!));
  for (const name of ["frozen-wrong", "witness-wrong", "before-commit", "before-tree", "after-commit", "after-tree", "wrong-transition"]) {
    const path = `mutations/min/${name}.json`;
    viewWithReject(`view-min-${name}`, min(path, m[path]!));
  }
  view("view-min-before-null", min("mutations/min/before-null.json", m["mutations/min/before-null.json"]!));
  view("view-min-after-null", min("mutations/min/after-null.json", m["mutations/min/after-null.json"]!));

  view("view-repair-expected-absent", { repairDirectory: REPAIR });
  view("view-repair-expected-match", rep());
  viewWithReject("view-repair-expected-wrong", rep(REPAIR, WRONG_DIGEST));
  view("view-repair-legacy", rep("mutations/repair/legacy", m["mutations/repair/legacy"]!));
  for (const name of ["investigation-wrong", "frozen-wrong", "citations-invalid", "manifest-malformed", "packet-malformed", "brief-malformed", "manifest-content"]) {
    const path = `mutations/repair/${name}`;
    viewWithReject(`view-repair-${name}`, rep(path, m[path]!));
  }

  view("view-prevention-summary", prev());
  view("view-prevention-verified", prev(VERIFIED, d.preventionVerified));
  view("view-prevention-expected-absent", { preventionDirectory: SUMMARY });
  view("view-prevention-expected-match", prev());
  viewWithReject("view-prevention-expected-wrong", prev(SUMMARY, WRONG_DIGEST));
  view("view-prevention-investigation-absent", prev(".faultline/prevention-proofs/no-investigation", m[".faultline/prevention-proofs/no-investigation"]!));
  for (const name of ["root-wrong", "witness-wrong", "investigation-wrong", "runs-wrong", "runs-missing"]) {
    const path = `mutations/prevention/${name}`;
    viewWithReject(`view-prevention-${name}`, prev(path, m[path]!));
  }

  view("view-private-sentinels", { ...min(), ...rep(), ...prev(VERIFIED, d.preventionVerified) });
  view("view-private-prevention-priority", { ...rep(), ...prev(VERIFIED, d.preventionVerified) });
  view("view-private-prevention-only-hero", prev(VERIFIED, d.preventionVerified));

  // ---- CLI option parsing. Literal argv; odd spellings are passed through unchanged. ----
  const root = d.proofRoot;
  const NO_BUNDLE = R("no-such-bundle");
  add("cli-options-bundle-missing", "cli-error", null, {}, ["serve", "--bundle"]);
  add("cli-options-bundle-empty", "cli-error", null, {}, ["serve", "--bundle", ""]);
  add("cli-options-bundle-prefixed", "cli-error", null, {}, ["serve", "--bundle", "--port", "0"]);
  add("cli-options-bundle-duplicate", "cli-serve", root, {}, ["serve", "--bundle", B, "--bundle", NO_BUNDLE, "--expect-root", root, "--port", "0"]);
  add("cli-options-bundle-equals", "cli-serve", root, {}, ["serve", `--bundle=${NO_BUNDLE}`, "--bundle", B, "--expect-root", root, "--port", "0"]);
  add("cli-options-duplicate-first", "cli-serve", root, {}, ["serve", "--bundle", B, "--expect-root", root, "--expect-root", `sha256:${"0".repeat(64)}`, "--port", "0"]);
  add("cli-options-equals-ignored", "cli-serve", null, {}, ["serve", "--bundle", B, `--expect-root=${root}`, "--port", "0"]);
  add("cli-options-min-path-only", "cli-error", root, { minimizationFile: CERTIFIED }, ["serve", "--bundle", B, "--expect-root", root, "--minimization", R(CERTIFIED)]);
  add("cli-options-min-digest-only", "cli-error", root, { expectedMinimizationDigest: d.minimization }, ["serve", "--bundle", B, "--expect-root", root, "--expect-minimization", d.minimization]);
  add("cli-options-repair-path-only", "cli-error", root, { repairDirectory: REPAIR }, ["serve", "--bundle", B, "--expect-root", root, "--repair", R(REPAIR)]);
  add("cli-options-repair-digest-only", "cli-error", root, { expectedRepairDigest: d.repair }, ["serve", "--bundle", B, "--expect-root", root, "--expect-repair", d.repair]);
  add("cli-options-prevention-path-only", "cli-error", root, { preventionDirectory: SUMMARY }, ["serve", "--bundle", B, "--expect-root", root, "--prevention", R(SUMMARY)]);
  add("cli-options-prevention-digest-only", "cli-error", root, { expectedPreventionDigest: d.preventionSummary }, ["serve", "--bundle", B, "--expect-root", root, "--expect-prevention", d.preventionSummary]);
  add("cli-options-all-values-first", "cli-error", root, {}, [
    "serve", "--bundle", B, "--expect-root", root, "--minimization", R(CERTIFIED), "--repair", R(REPAIR), "--prevention", R(SUMMARY),
    "--expect-minimization", d.minimization, "--expect-repair", d.repair, "--expect-prevention"
  ]);
  add("cli-options-pair-min-first", "cli-error", root, {}, ["serve", "--bundle", B, "--expect-root", root, "--minimization", R(CERTIFIED), "--repair", R(REPAIR)]);
  add("cli-options-pair-repair-first", "cli-error", root, {}, [
    "serve", "--bundle", B, "--expect-root", root, "--minimization", R(CERTIFIED), "--expect-minimization", d.minimization,
    "--repair", R(REPAIR), "--prevention", R(SUMMARY)
  ]);
  add("cli-options-root-raw", "cli-error", null, {},["serve", "--bundle", B, "--expect-root", "not-a-digest"]);
  add("cli-options-relative-paths", "cli-serve", root, { ...min() }, [
    "serve", "--bundle", "bundle", "--expect-root", root, "--minimization", CERTIFIED, "--expect-minimization", d.minimization, "--port", "0"
  ]);

  // ---- validation order ----
  add("cli-order-pair-before-io", "cli-error", null, {}, ["serve", "--bundle", NO_BUNDLE, "--minimization", R(CERTIFIED)]);
  add("cli-order-base-before-port", "cli-error", null, {}, ["serve", "--bundle", NO_BUNDLE, "--port", "abc"]);
  add("cli-order-attachment-before-port", "cli-error", root, min(CERTIFIED, WRONG_DIGEST), serve(min(CERTIFIED, WRONG_DIGEST), root, ["--port", "abc"]));
  add("cli-order-later-value-before-pair", "cli-error", root, {}, ["serve", "--bundle", B, "--expect-root", root, "--minimization", R(CERTIFIED), "--repair"]);

  // ---- port coercion, after a valid load. Values stay literal argv strings. ----
  const port = (id: string, kind: "cli-serve" | "cli-error", args: string[]): void => add(`cli-port-${id}`, kind, root, {}, ["serve", "--bundle", B, "--expect-root", root, ...args]);
  port("default", "cli-serve", []);
  port("zero", "cli-serve", ["--port", "0"]);
  port("empty", "cli-serve", ["--port", ""]);
  port("missing", "cli-serve", ["--port"]);
  port("next-flag", "cli-error", ["--port", "--expect-root"]);
  port("whitespace", "cli-serve", ["--port", " 0 "]);
  port("hex", "cli-serve", ["--port", "0x0"]);
  port("exponent", "cli-serve", ["--port", "0e0"]);
  port("fractional", "cli-error", ["--port", "0.5"]);
  port("negative", "cli-error", ["--port", "-1"]);
  port("nan", "cli-error", ["--port", "NaN"]);
  port("infinity", "cli-error", ["--port", "Infinity"]);
  port("out-of-range", "cli-error", ["--port", "65536"]);
  port("occupied", "cli-error", ["--port", "{PORT}"]);

  // ---- successful servers ----
  for (const bits of ["000", "001", "010", "011", "100", "101", "110", "111"]) {
    add(`cli-serve-${bits}`, "cli-serve", root, combo(bits), serve(combo(bits), root, ["--port", "0"]));
  }
  add("cli-serve-no-root", "cli-serve", null, {}, serve({}, null, ["--port", "0"]));
  add("cli-serve-ignored-flags", "cli-serve", root, {}, serve({}, root, ["--port", "0", "--output", R("ignored-output"), "--browser", "--serve-ms", "1"]));

  return orderTable(rows);
}

/** Stable table order: combinations, then each family in the documented order. */
function orderTable(rows: ServeBundleCase[]): ServeBundleCase[] {
  const rank = (id: string): number => {
    const order = ["view-combination-", "view-root-", "view-min-", "view-repair-", "view-prevention-", "view-private-",
      "cli-options-", "cli-order-", "cli-port-", "cli-serve-", "cli-reject-"];
    const index = order.findIndex((prefix) => id.startsWith(prefix));
    return index < 0 ? order.length : index;
  };
  return rows.map((row, i) => ({ row, i })).sort((a, b) => rank(a.row.id) - rank(b.row.id) || a.i - b.i).map(({ row }) => row);
}
