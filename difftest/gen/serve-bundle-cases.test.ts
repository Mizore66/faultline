// Contract tests for the serve-bundle case table. The required ID set below is
// a hand-written literal; it is NOT derived from serveBundleCases(), cases.jsonl
// or golden.jsonl. Adding a case requires editing this table.
//
// serveBundleCases(root) takes the corpus directory holding fixtures.json. These
// tests create independent temporary corpus directories whose fixtures.json
// carries synthetic digest literals chosen here, so expected bound digests are
// asserted against values this file owns, never against implementation output.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  MUTATIONS_INDEX_PATH, MUTATIONS_INDEX_SCHEMA, MUTATION_ARTIFACTS, serveBundleCases, type ServeBundleCase
} from "./serve-bundle-cases.js";

const families: Array<[string, string[]]> = [
  ["view-root-", ["absent", "match", "wrong"]],
  ["view-min-", ["uncertified", "expected-absent", "expected-match", "expected-wrong", "witness-null", "frozen-wrong",
    "witness-wrong", "before-null", "after-null", "before-commit", "before-tree", "after-commit", "after-tree", "wrong-transition"]],
  ["view-repair-", ["expected-absent", "expected-match", "expected-wrong", "investigation-wrong", "frozen-wrong", "legacy",
    "citations-invalid", "manifest-malformed", "packet-malformed", "brief-malformed", "manifest-content"]],
  ["view-prevention-", ["summary", "verified", "expected-absent", "expected-match", "expected-wrong", "root-wrong",
    "witness-wrong", "investigation-wrong", "investigation-absent", "runs-wrong", "runs-missing"]],
  ["view-private-", ["sentinels", "prevention-priority", "prevention-only-hero"]],
  ["cli-options-", ["bundle-missing", "bundle-empty", "bundle-prefixed", "bundle-duplicate", "bundle-equals", "duplicate-first",
    "equals-ignored", "min-path-only", "min-digest-only", "repair-path-only", "repair-digest-only", "prevention-path-only",
    "prevention-digest-only", "all-values-first", "pair-min-first", "pair-repair-first", "root-raw", "relative-paths"]],
  ["cli-order-", ["pair-before-io", "base-before-port", "attachment-before-port", "later-value-before-pair"]],
  ["cli-port-", ["default", "zero", "empty", "missing", "next-flag", "whitespace", "hex", "exponent", "fractional",
    "negative", "nan", "infinity", "out-of-range", "occupied"]],
  ["cli-serve-", ["000", "001", "010", "011", "100", "101", "110", "111", "no-root", "ignored-flags"]]
];
const combinationBits = ["000", "001", "010", "011", "100", "101", "110", "111"];
const rejectSources = [
  "view-min-expected-wrong", "view-min-frozen-wrong", "view-min-witness-wrong", "view-min-before-commit",
  "view-min-before-tree", "view-min-after-commit", "view-min-after-tree", "view-min-wrong-transition",
  "view-repair-expected-wrong", "view-repair-investigation-wrong", "view-repair-frozen-wrong", "view-repair-citations-invalid",
  "view-repair-manifest-malformed", "view-repair-packet-malformed", "view-repair-brief-malformed", "view-repair-manifest-content",
  "view-prevention-expected-wrong", "view-prevention-root-wrong", "view-prevention-witness-wrong",
  "view-prevention-investigation-wrong", "view-prevention-runs-wrong", "view-prevention-runs-missing"
];
const requiredIds: string[] = [
  ...combinationBits.map((bits) => `view-combination-${bits}`),
  ...families.flatMap(([prefix, suffixes]) => suffixes.map((suffix) => prefix + suffix)),
  ...rejectSources.map((id) => `cli-reject-${id}`)
];
const required = new Set(requiredIds);

// Synthetic digests owned by this test (distinct, valid shape).
const SYN = {
  proofRoot: `sha256:${"1".repeat(64)}`,
  minimization: `sha256:${"2".repeat(64)}`,
  repair: `sha256:${"3".repeat(64)}`,
  preventionSummary: `sha256:${"4".repeat(64)}`,
  preventionVerified: `sha256:${"5".repeat(64)}`
};

function corpusRoot(digests = SYN): string {
  const root = mkdtempSync(join(tmpdir(), "faultline-serve-cases-"));
  writeFileSync(join(root, "fixtures.json"), `${JSON.stringify({
    schema: "faultline.serve-bundle-fixtures.v1",
    fixtures: [{
      id: "coherent", root: "inputs/coherent", files: [], digests,
      privateSentinels: ["FL_PRIVATE_OVERLAY_5C_NOT_FOR_HTML"]
    }]
  }, null, 2)}\n`);
  // Independent synthetic mutation digests: distinct per artifact, owned by this test.
  const mutationDigests: Record<string, string> = {};
  MUTATION_ARTIFACTS.forEach((path, i) => { mutationDigests[path] = `sha256:${(i + 16).toString(16).padStart(2, "0").repeat(32)}`; });
  mkdirSync(join(root, "inputs", "coherent", "mutations"), { recursive: true });
  writeFileSync(join(root, MUTATIONS_INDEX_PATH), `${JSON.stringify({ schema: MUTATIONS_INDEX_SCHEMA, digests: mutationDigests })}\n`);
  return root;
}

function table(root = corpusRoot()): Map<string, ServeBundleCase> {
  return new Map(serveBundleCases(root).map((c) => [c.id, c]));
}

const bare = (value: string | undefined) => value?.replace(/^\{ROOT\}\//, "");

test("literal required table has no duplicates and the expected size", () => {
  // Catches a typo in this test's own table silently shrinking the contract.
  assert.equal(required.size, requiredIds.length);
  assert.equal(requiredIds.length, 8 + 3 + 14 + 11 + 11 + 3 + 18 + 4 + 14 + 10 + 22);
  assert.equal(requiredIds.length, 118);
});

test("all attachment presence combinations are retained", () => {
  const rows = serveBundleCases(corpusRoot());
  const ids = new Set(rows.map((c) => c.id));
  for (let bits = 0; bits < 8; bits++) {
    assert.ok(ids.has(`view-combination-${bits.toString(2).padStart(3, "0")}`));
  }
  assert.equal(ids.size, rows.length);
});

test("case table is exactly the independently pinned ID set (no missing, unknown or duplicate)", () => {
  const ids = serveBundleCases(corpusRoot()).map((c) => c.id);
  assert.equal(new Set(ids).size, ids.length, "duplicate case id");
  assert.deepEqual(ids.filter((id) => !required.has(id)), [], "unknown case ids");
  assert.deepEqual(requiredIds.filter((id) => !ids.includes(id)), [], "missing case ids");
});

test("a root without valid fixtures.json is refused instead of inventing metadata", () => {
  // Catches a table that fabricates digests when the corpus metadata is absent or invalid.
  assert.throws(() => serveBundleCases(mkdtempSync(join(tmpdir(), "faultline-serve-cases-empty-"))));
  const bad = mkdtempSync(join(tmpdir(), "faultline-serve-cases-bad-"));
  writeFileSync(join(bad, "fixtures.json"), JSON.stringify({ schema: "faultline.serve-bundle-fixtures.v1", fixtures: [] }));
  assert.throws(() => serveBundleCases(bad));
});

test("ids and structure are independent of the root path; digests come only from fixtures.json", () => {
  const one = serveBundleCases(corpusRoot());
  const two = serveBundleCases(corpusRoot());
  assert.equal(JSON.stringify(one), JSON.stringify(two));
  const other = { ...SYN, proofRoot: `sha256:${"a".repeat(64)}` };
  const changed = serveBundleCases(corpusRoot(other));
  assert.deepEqual(changed.map((c) => c.id), one.map((c) => c.id));
  assert.equal(changed.find((c) => c.id === "view-combination-111")?.expectedRoot, other.proofRoot);
  assert.ok(!JSON.stringify(one).includes(tmpdir()), "no absolute root may leak into rows");
});

test("kinds follow the contract", () => {
  const rows = table();
  for (const [id, c] of rows) {
    assert.ok(["view", "cli-error", "cli-serve"].includes(c.kind), id);
    if (id.startsWith("view-")) assert.equal(c.kind, "view", id);
    if (id.startsWith("cli-serve-")) assert.equal(c.kind, "cli-serve", id);
    if (id.startsWith("cli-reject-") || id.startsWith("cli-order-")) assert.equal(c.kind, "cli-error", id);
  }
  for (const suffix of ["default", "zero", "empty", "missing", "whitespace", "hex", "exponent"]) {
    assert.equal(rows.get(`cli-port-${suffix}`)?.kind, "cli-serve", suffix);
  }
  for (const suffix of ["next-flag", "fractional", "negative", "nan", "infinity", "out-of-range", "occupied"]) {
    assert.equal(rows.get(`cli-port-${suffix}`)?.kind, "cli-error", suffix);
  }
  for (const suffix of ["bundle-missing", "bundle-empty", "bundle-prefixed", "min-path-only", "min-digest-only",
    "repair-path-only", "repair-digest-only", "prevention-path-only", "prevention-digest-only", "all-values-first",
    "pair-min-first", "pair-repair-first", "root-raw"]) {
    assert.equal(rows.get(`cli-options-${suffix}`)?.kind, "cli-error", suffix);
  }
  for (const suffix of ["bundle-duplicate", "bundle-equals", "duplicate-first", "equals-ignored", "relative-paths"]) {
    assert.equal(rows.get(`cli-options-${suffix}`)?.kind, "cli-serve", suffix);
  }
});

test("every row is well formed", () => {
  for (const c of serveBundleCases(corpusRoot())) {
    assert.equal(c.fixture, "coherent", c.id);
    assert.ok(c.expectedRoot === null || /^sha256:[0-9a-f]{64}$/.test(c.expectedRoot), `${c.id}: expectedRoot must be null or a sha256 digest`);
    assert.ok(Array.isArray(c.argv), c.id);
    if (c.kind === "view") assert.deepEqual(c.argv, [], `${c.id}: view cases launch no process`);
    else {
      assert.equal(c.argv[0], "serve", c.id);
      assert.ok(c.argv.includes("--bundle"), c.id);
    }
    for (const [key, value] of Object.entries(c.options)) {
      assert.equal(typeof value, "string", `${c.id}: ${key} must be omitted or a string`);
      if (key.endsWith("File") || key.endsWith("Directory")) assert.ok(/^(\.faultline|mutations|minimization)/.test(String(value)), `${c.id}: ${key} is fixture-relative`);
    }
  }
});

test("combination bits mean minimization, repair, prevention in that order", () => {
  // Catches swapped bit positions and base-only rows that still carry an attachment.
  const rows = table();
  for (const bits of combinationBits) {
    for (const prefix of ["view-combination-", "cli-serve-"]) {
      const c = rows.get(prefix + bits);
      assert.ok(c, prefix + bits);
      const wants = [bits[0] === "1", bits[1] === "1", bits[2] === "1"];
      const o = c.options;
      assert.deepEqual([o.minimizationFile !== undefined, o.repairDirectory !== undefined, o.preventionDirectory !== undefined], wants, c.id);
      assert.equal(o.expectedMinimizationDigest !== undefined, wants[0], c.id);
      assert.equal(o.expectedRepairDigest !== undefined, wants[1], c.id);
      assert.equal(o.expectedPreventionDigest !== undefined, wants[2], c.id);
    }
  }
});

test("combination rows bind the certified minimization, valid repair and summary prevention digests from fixtures.json", () => {
  const rows = table();
  const full = rows.get("view-combination-111")!;
  assert.equal(full.expectedRoot, SYN.proofRoot);
  assert.deepEqual(full.options, {
    minimizationFile: "minimization.json", expectedMinimizationDigest: SYN.minimization,
    repairDirectory: ".faultline/repair-briefs/attached", expectedRepairDigest: SYN.repair,
    preventionDirectory: ".faultline/prevention-proofs/summary", expectedPreventionDigest: SYN.preventionSummary
  });
  for (const bits of combinationBits) {
    assert.equal(rows.get(`view-combination-${bits}`)?.expectedRoot, SYN.proofRoot, bits);
    assert.equal(rows.get(`view-combination-${bits}`)?.fixture, "coherent", bits);
  }
  assert.deepEqual(rows.get("view-combination-000")!.options, {});
});

test("prevention classification rows bind their own package and digest", () => {
  const rows = table();
  assert.deepEqual(rows.get("view-prevention-summary")!.options, {
    preventionDirectory: ".faultline/prevention-proofs/summary", expectedPreventionDigest: SYN.preventionSummary
  });
  assert.deepEqual(rows.get("view-prevention-verified")!.options, {
    preventionDirectory: ".faultline/prevention-proofs/verified", expectedPreventionDigest: SYN.preventionVerified
  });
  assert.equal(rows.get("view-prevention-investigation-absent")!.options.preventionDirectory, ".faultline/prevention-proofs/no-investigation");
});

test("root cases: absent has no root, match and wrong differ, original attachments absent", () => {
  const rows = table();
  assert.equal(rows.get("view-root-absent")?.expectedRoot, null);
  assert.equal(rows.get("view-root-match")?.expectedRoot, SYN.proofRoot);
  const wrong = rows.get("view-root-wrong")?.expectedRoot;
  assert.ok(wrong); assert.notEqual(wrong, SYN.proofRoot);
  for (const id of ["view-root-absent", "view-root-match", "view-root-wrong"]) assert.deepEqual(rows.get(id)!.options, {}, id);
});

test("expected-digest variants: absent drops only the digest, match is the bound digest, wrong differs from every bound digest", () => {
  const rows = table();
  const groups: Array<[string, string, string, string, string]> = [
    ["view-min-", "minimizationFile", "expectedMinimizationDigest", "minimization.json", SYN.minimization],
    ["view-repair-", "repairDirectory", "expectedRepairDigest", ".faultline/repair-briefs/attached", SYN.repair],
    ["view-prevention-", "preventionDirectory", "expectedPreventionDigest", ".faultline/prevention-proofs/summary", SYN.preventionSummary]
  ];
  const bound = new Set(Object.values(SYN));
  for (const [prefix, pathKey, digestKey, path, digest] of groups) {
    const absent = rows.get(`${prefix}expected-absent`)!.options as Record<string, string | undefined>;
    const match = rows.get(`${prefix}expected-match`)!.options as Record<string, string | undefined>;
    const wrong = rows.get(`${prefix}expected-wrong`)!.options as Record<string, string | undefined>;
    assert.equal(absent[pathKey], path, prefix); assert.equal(absent[digestKey], undefined, prefix);
    assert.equal(match[pathKey], path, prefix); assert.equal(match[digestKey], digest, prefix);
    assert.equal(wrong[pathKey], path, prefix);
    assert.ok(wrong[digestKey] && /^sha256:[0-9a-f]{64}$/.test(wrong[digestKey]!), prefix);
    assert.ok(!bound.has(wrong[digestKey]!), `${prefix}: the wrong digest must not equal any real bound digest`);
  }
});

test("variant rows point at their own mutated artifact while keeping the declared path/digest pair", () => {
  const rows = table();
  const expect: Record<string, [string, string]> = {
    "view-min-witness-null": ["minimizationFile", "mutations/min/witness-null.json"],
    "view-min-frozen-wrong": ["minimizationFile", "mutations/min/frozen-wrong.json"],
    "view-min-witness-wrong": ["minimizationFile", "mutations/min/witness-wrong.json"],
    "view-min-before-null": ["minimizationFile", "mutations/min/before-null.json"],
    "view-min-after-null": ["minimizationFile", "mutations/min/after-null.json"],
    "view-min-before-commit": ["minimizationFile", "mutations/min/before-commit.json"],
    "view-min-before-tree": ["minimizationFile", "mutations/min/before-tree.json"],
    "view-min-after-commit": ["minimizationFile", "mutations/min/after-commit.json"],
    "view-min-after-tree": ["minimizationFile", "mutations/min/after-tree.json"],
    "view-min-wrong-transition": ["minimizationFile", "mutations/min/wrong-transition.json"],
    "view-min-uncertified": ["minimizationFile", "minimization-uncertified.json"],
    "view-repair-investigation-wrong": ["repairDirectory", "mutations/repair/investigation-wrong"],
    "view-repair-frozen-wrong": ["repairDirectory", "mutations/repair/frozen-wrong"],
    "view-repair-legacy": ["repairDirectory", "mutations/repair/legacy"],
    "view-repair-citations-invalid": ["repairDirectory", "mutations/repair/citations-invalid"],
    "view-repair-manifest-malformed": ["repairDirectory", "mutations/repair/manifest-malformed"],
    "view-repair-packet-malformed": ["repairDirectory", "mutations/repair/packet-malformed"],
    "view-repair-brief-malformed": ["repairDirectory", "mutations/repair/brief-malformed"],
    "view-repair-manifest-content": ["repairDirectory", "mutations/repair/manifest-content"],
    "view-prevention-root-wrong": ["preventionDirectory", "mutations/prevention/root-wrong"],
    "view-prevention-witness-wrong": ["preventionDirectory", "mutations/prevention/witness-wrong"],
    "view-prevention-investigation-wrong": ["preventionDirectory", "mutations/prevention/investigation-wrong"],
    "view-prevention-runs-wrong": ["preventionDirectory", "mutations/prevention/runs-wrong"],
    "view-prevention-runs-missing": ["preventionDirectory", "mutations/prevention/runs-missing"]
  };
  for (const [id, [key, path]] of Object.entries(expect)) {
    const options = rows.get(id)!.options as Record<string, string | undefined>;
    assert.equal(options[key], path, id);
    const digestKey = key === "minimizationFile" ? "expectedMinimizationDigest" : key === "repairDirectory" ? "expectedRepairDigest" : "expectedPreventionDigest";
    assert.match(options[digestKey] ?? "", /^sha256:[0-9a-f]{64}$/, `${id}: complete pair`);
  }
});

test("private rows: sentinels attach everything with the verified prevention, priority and hero rows are scoped", () => {
  const rows = table();
  const all = rows.get("view-private-sentinels")!.options;
  assert.equal(all.minimizationFile, "minimization.json");
  assert.equal(all.repairDirectory, ".faultline/repair-briefs/attached");
  assert.equal(all.preventionDirectory, ".faultline/prevention-proofs/verified");
  assert.equal(all.expectedPreventionDigest, SYN.preventionVerified);
  const priority = rows.get("view-private-prevention-priority")!.options;
  assert.ok(priority.repairDirectory && priority.preventionDirectory && !priority.minimizationFile);
  const hero = rows.get("view-private-prevention-only-hero")!.options;
  assert.ok(hero.preventionDirectory && !hero.minimizationFile && !hero.repairDirectory);
});

test("cli-reject rows reuse the exact input and complete path/digest pairs of their view source", () => {
  const rows = table();
  for (const source of rejectSources) {
    const view = rows.get(source);
    const reject = rows.get(`cli-reject-${source}`);
    assert.ok(view && reject, source);
    assert.equal(reject.fixture, view.fixture, source);
    assert.equal(reject.expectedRoot, view.expectedRoot, source);
    assert.deepEqual(reject.options, view.options, source);
    const o = reject.options;
    assert.equal(o.minimizationFile === undefined, o.expectedMinimizationDigest === undefined, source);
    assert.equal(o.repairDirectory === undefined, o.expectedRepairDigest === undefined, source);
    assert.equal(o.preventionDirectory === undefined, o.expectedPreventionDigest === undefined, source);
    assert.ok(o.minimizationFile ?? o.repairDirectory ?? o.preventionDirectory, `${source}: carries an attachment`);
    assert.equal(bare(reject.argv[reject.argv.indexOf("--bundle") + 1]), "bundle", source);
    for (const [flag, value] of [["--minimization", o.minimizationFile], ["--expect-minimization", o.expectedMinimizationDigest],
      ["--repair", o.repairDirectory], ["--expect-repair", o.expectedRepairDigest],
      ["--prevention", o.preventionDirectory], ["--expect-prevention", o.expectedPreventionDigest]] as const) {
      if (value === undefined) assert.ok(!reject.argv.includes(flag), `${source}: ${flag} unexpected`);
      else assert.equal(bare(reject.argv[reject.argv.indexOf(flag) + 1]), value, `${source}: ${flag}`);
    }
    if (view.expectedRoot === null) assert.ok(!reject.argv.includes("--expect-root"), source);
    else assert.equal(reject.argv[reject.argv.indexOf("--expect-root") + 1], view.expectedRoot, source);
  }
});

test("one-sided attachment pairs supply exactly one half", () => {
  const rows = table();
  const cases: Array<[string, string, string]> = [
    ["min-path-only", "--minimization", "--expect-minimization"], ["min-digest-only", "--expect-minimization", "--minimization"],
    ["repair-path-only", "--repair", "--expect-repair"], ["repair-digest-only", "--expect-repair", "--repair"],
    ["prevention-path-only", "--prevention", "--expect-prevention"], ["prevention-digest-only", "--expect-prevention", "--prevention"]
  ];
  for (const [suffix, present, absent] of cases) {
    const argv = rows.get(`cli-options-${suffix}`)!.argv;
    assert.ok(argv.includes(present), suffix);
    assert.ok(!argv.includes(absent), suffix);
  }
});

test("argument-order rows exercise their precedence claims with the exact argv shapes", () => {
  const rows = table();
  // pair error must be reachable before any bundle I/O: the bundle path does not exist.
  const pair = rows.get("cli-order-pair-before-io")!.argv;
  assert.ok(pair.includes("--minimization") && !pair.includes("--expect-minimization"));
  assert.ok(!bare(pair[pair.indexOf("--bundle") + 1])!.startsWith("bundle"), "bundle must be a path that cannot load");
  // invalid base (nonexistent) with an invalid port: only the port is unusable otherwise.
  const base = rows.get("cli-order-base-before-port")!.argv;
  assert.equal(base[base.indexOf("--port") + 1], "abc");
  assert.notEqual(bare(base[base.indexOf("--bundle") + 1]), "bundle");
  // attachment error with valid bundle and invalid port.
  const attach = rows.get("cli-order-attachment-before-port")!.argv;
  assert.equal(bare(attach[attach.indexOf("--bundle") + 1]), "bundle");
  assert.equal(attach[attach.indexOf("--port") + 1], "abc");
  assert.ok(attach.includes("--minimization") && attach.includes("--expect-minimization"));
  // a later missing value (last argument) comes after an earlier incomplete pair.
  const later = rows.get("cli-order-later-value-before-pair")!.argv;
  assert.ok(later.includes("--minimization") && !later.includes("--expect-minimization"));
  assert.equal(later[later.length - 1], "--repair", "missing later value is the final argument");
  const all = rows.get("cli-options-all-values-first")!.argv;
  assert.ok(all.at(-1)?.startsWith("--expect-"), "a trailing expect flag with no value");
});

test("cli-serve combinations pass the declared attachments; flags and port shapes are literal", () => {
  const rows = table();
  for (const bits of combinationBits) {
    const argv = rows.get(`cli-serve-${bits}`)!.argv;
    assert.equal(argv.includes("--minimization") && argv.includes("--expect-minimization"), bits[0] === "1", bits);
    assert.equal(argv.includes("--repair") && argv.includes("--expect-repair"), bits[1] === "1", bits);
    assert.equal(argv.includes("--prevention") && argv.includes("--expect-prevention"), bits[2] === "1", bits);
    assert.equal(argv[argv.indexOf("--port") + 1], "0", bits);
    assert.equal(argv[argv.indexOf("--expect-root") + 1], SYN.proofRoot, bits);
  }
  assert.ok(!rows.get("cli-serve-no-root")!.argv.includes("--expect-root"));
  assert.equal(rows.get("cli-serve-no-root")!.expectedRoot, null);
  const ignored = rows.get("cli-serve-ignored-flags")!.argv;
  assert.ok(ignored.includes("--output") && ignored.includes("--serve-ms") && ignored.includes("--browser"));
  const port = (id: string) => rows.get(id)!.argv;
  const value = (id: string) => port(id)[port(id).indexOf("--port") + 1];
  assert.equal(port("cli-port-default").indexOf("--port"), -1);
  assert.equal(value("cli-port-zero"), "0");
  assert.equal(value("cli-port-empty"), "");
  assert.equal(port("cli-port-missing").at(-1), "--port", "--port is the last argument");
  assert.equal(value("cli-port-whitespace"), " 0 ");
  assert.equal(value("cli-port-hex"), "0x0");
  assert.equal(value("cli-port-exponent"), "0e0");
  assert.equal(value("cli-port-fractional"), "0.5");
  assert.equal(value("cli-port-negative"), "-1");
  assert.equal(value("cli-port-nan"), "NaN");
  assert.equal(value("cli-port-infinity"), "Infinity");
  assert.equal(value("cli-port-out-of-range"), "65536");
  const nextFlag = value("cli-port-next-flag");
  assert.ok(nextFlag !== undefined, "the next-flag row supplies a value after --port");
  assert.equal(nextFlag.startsWith("--"), true);
  assert.equal(value("cli-port-occupied"), "{PORT}", "the harness chooses the occupied port");
});

test("duplicate bundle row supplies a valid first and different second value", () => {
  const argv = table().get("cli-options-bundle-duplicate")!.argv;
  const first = argv.indexOf("--bundle");
  const second = argv.indexOf("--bundle", first + 1);
  assert.ok(first >= 0 && second > first);
  assert.equal(bare(argv[first + 1]), "bundle");
  assert.notEqual(argv[first + 1], argv[second + 1]);
});

test("bundle-equals row also carries a later exact --bundle", () => {
  const argv = table().get("cli-options-bundle-equals")!.argv;
  assert.ok(argv.some((a) => a.startsWith("--bundle=")));
  assert.ok(argv.includes("--bundle"));
  assert.equal(bare(argv[argv.indexOf("--bundle") + 1]), "bundle");
});

test("duplicate-first, equals-ignored and root-raw rows keep their exact literal argv semantics", () => {
  const rows = table();
  const dup = rows.get("cli-options-duplicate-first")!.argv;
  const first = dup.indexOf("--expect-root");
  assert.equal(dup[first + 1], SYN.proofRoot);
  assert.notEqual(dup[dup.indexOf("--expect-root", first + 1) + 1], SYN.proofRoot);
  const eq = rows.get("cli-options-equals-ignored")!.argv;
  assert.ok(eq.some((a) => a.startsWith("--expect-root=")) && !eq.includes("--expect-root"));
  const raw = rows.get("cli-options-root-raw")!.argv;
  assert.ok(!/^sha256:[0-9a-f]{64}$/.test(raw[raw.indexOf("--expect-root") + 1]!), "raw root is passed through unvalidated");
  const rel = rows.get("cli-options-relative-paths")!.argv;
  assert.equal(rel[rel.indexOf("--bundle") + 1], "bundle", "relative path, resolved against the process cwd");
  assert.ok(rel.every((a) => !a.includes("{ROOT}")));
});
