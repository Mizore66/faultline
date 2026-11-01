// Direct tests of the binding support module's exported functions. They need no
// built CLI and no committed binding corpus: they use the retained original
// coherent fixture and real TS verifiers. Expectations are literals owned here.
import assert from "node:assert/strict";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { verifyGitMinimizationResult } from "../../src/git-minimization.js";
import {
  BINDING_NAMES, assertIdentitiesConsistent, bindingArgv, bindingId, bindingInputPath, buildBindingRecords, loadBindingSource,
  makeBindingCases, makeInventory, serializeCases, serializeGoldens, serializeRecord, validateBindingCorpus, verifiedDigest,
  writeBindingCorpus, type BindingGolden, type BindingName
} from "./serve-bundle-binding.js";
import { parseBindingMode } from "./gen-serve-bundle-binding.js";

const PARENT = resolve("difftest/testdata/serve-bundle");

function records(): { texts: Record<BindingName, string>; source: ReturnType<typeof loadBindingSource> } {
  const source = loadBindingSource(PARENT);
  const built = buildBindingRecords(source.minimization, source.investigation);
  const texts = {} as Record<BindingName, string>;
  for (const name of BINDING_NAMES) texts[name] = serializeRecord(built[name]);
  return { texts, source };
}

function stagedCorpus(mutate?: (root: string) => void): string {
  const { texts, source } = records();
  const root = join(mkdtempSync(join(tmpdir(), "faultline-binding-test-")), "corpus");
  const cases = makeBindingCases(texts, source.proofRoot);
  const goldens: BindingGolden[] = cases.map((c) => ({ id: c.id, process: { stdout: "", stderr: `FaultLine error: ${c.id}\nNext: pnpm fl help\n`, exitCode: 1, signal: null } }));
  writeBindingCorpus(root, { texts, cases, goldens, source });
  mutate?.(root);
  return root;
}

test("the five records are verifier-valid with distinct digests unlike the original (guards every mutation below)", () => {
  const { texts, source } = records();
  const digests = BINDING_NAMES.map((name) => verifiedDigest(texts[name]));
  assert.equal(new Set(digests).size, 5);
  assert.ok(!digests.includes(source.minimizationDigest));
  for (const name of BINDING_NAMES) {
    const result = verifyGitMinimizationResult(JSON.parse(texts[name]), verifiedDigest(texts[name]));
    assert.equal(result.valid, true, `${name}: ${result.errors.join("; ")}`);
    assert.equal(result.externalDigestStatus, "MATCH");
  }
});

test("each record changes only its named binding and keeps the others", () => {
  const { source } = records();
  const built = buildBindingRecords(source.minimization, source.investigation);
  const original = source.minimization as Record<string, any>;
  const witness = (r: unknown): Record<string, string> => (r as Record<string, any>).witness;
  assert.notEqual(witness(built["foreign-frozen"]).frozenDigest, witness(original).frozenDigest);
  assert.equal(witness(built["foreign-frozen"]).witnessDigest, witness(original).witnessDigest);
  assert.equal(witness(built["foreign-witness"]).frozenDigest, witness(original).frozenDigest);
  assert.notEqual(witness(built["foreign-witness"]).witnessDigest, witness(original).witnessDigest);
  assert.equal((built["witness-null"] as Record<string, unknown>).witness, null);
  const nullable = built["nullable-states"] as Record<string, any>;
  assert.equal(nullable.before, null);
  assert.equal(nullable.after, null);
  assert.equal(nullable.status, "RANGE_ERROR");
  assert.deepEqual(nullable.runs, []);
  assert.deepEqual(nullable.witness, original.witness);
  const range = built["different-range"] as Record<string, any>;
  assert.notDeepEqual(range.before, original.before);
  assert.deepEqual(range.runs.map((r: Record<string, any>) => r.candidateUnitIds), original.runs.map((r: Record<string, any>) => r.candidateUnitIds));
  assert.equal(range.status, "COMPLETED");
});

test("run identities in every record satisfy the verifier's formulas", () => {
  const { source } = records();
  const built = buildBindingRecords(source.minimization, source.investigation);
  assert.doesNotThrow(() => assertIdentitiesConsistent(source.minimization));
  for (const name of ["foreign-frozen", "foreign-witness", "different-range"] as const) {
    assert.doesNotThrow(() => assertIdentitiesConsistent(built[name]), name);
    assert.notEqual(JSON.stringify(built[name].runs), JSON.stringify(source.minimization.runs), `${name} must re-identify its runs`);
  }
});

test("an identity formula that disagrees with a record is refused", () => {
  const { source } = records();
  const broken = structuredClone(source.minimization) as Record<string, any>;
  broken.runs[0].executionId = `sha256:${"0".repeat(64)}`;
  assert.throws(() => assertIdentitiesConsistent(broken), /execution id formula/);
});

test("a record the real verifier rejects is refused instead of retained", () => {
  const { texts } = records();
  const parsed = JSON.parse(texts["witness-null"]) as Record<string, any>;
  parsed.status = "NO_PATCHES";
  assert.throws(() => verifiedDigest(JSON.stringify(parsed)), /not verifier-valid/);
});

test("the argv contract is exact and pairs the recomputed digest with the original base root", () => {
  const root = `sha256:${"a".repeat(64)}`;
  const digest = `sha256:${"b".repeat(64)}`;
  assert.deepEqual(bindingArgv(root, digest), [
    "serve", "--bundle", "{ROOT}/bundle", "--expect-root", root, "--minimization", "{ROOT}/binding-minimization.json",
    "--expect-minimization", digest, "--port", "abc"
  ]);
});

test("a first capture refuses an existing destination and leaves it untouched", () => {
  const root = stagedCorpus();
  const before = readFileSync(join(root, "cases.jsonl"), "utf8");
  const { texts, source } = records();
  const cases = makeBindingCases(texts, source.proofRoot);
  assert.throws(() => writeBindingCorpus(root, { texts, cases, goldens: [], source }), /already exists/);
  assert.equal(readFileSync(join(root, "cases.jsonl"), "utf8"), before);
});

test("a well-formed corpus validates (guards the negative tests below)", () => {
  assert.doesNotThrow(() => validateBindingCorpus(stagedCorpus(), PARENT));
});

test("validation rejects a missing, extra, linked or edited file and a wrong inventory", () => {
  const first = BINDING_NAMES[0]!;
  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => rmSync(join(r, bindingInputPath(first)))), PARENT), /missing/);
  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => writeFileSync(join(r, "extra.json"), "{}")), PARENT), /unexpected files/);  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => writeFileSync(join(r, bindingInputPath(first)), `${readFileSync(join(r, bindingInputPath(first)), "utf8")} `)), PARENT), /bytes|sha256/);
  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => { rmSync(join(r, "baseline.json")); writeFileSync(join(r, "baseline.json"), "{}\n"); }), PARENT), /pinned provenance/);
  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => {
    rmSync(join(r, bindingInputPath(first)));
    symlinkSync(join(r, bindingInputPath(BINDING_NAMES[1]!)), join(r, bindingInputPath(first)));
  }), PARENT), /symbolic link/);
  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => {
    const inv = JSON.parse(readFileSync(join(r, "inventory.json"), "utf8"));
    inv.files.push(inv.files[0]);
    writeFileSync(join(r, "inventory.json"), `${JSON.stringify(inv, null, 2)}\n`);
  }), PARENT), /exactly|once/);
});

test("validation rejects wrong ids, order, duplicates, argv, digests and golden shape", () => {
  const edit = (file: string, change: (lines: string[]) => string[]) => (r: string): void => {
    const lines = readFileSync(join(r, file), "utf8").slice(0, -1).split("\n");
    writeFileSync(join(r, file), `${change(lines).join("\n")}\n`);
  };
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit("cases.jsonl", (l) => [l[1]!, l[0]!, ...l.slice(2)])), PARENT), /is not binding-/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit("cases.jsonl", (l) => [l[0]!, l[0]!, ...l.slice(2)])), PARENT), /is not binding-/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit("cases.jsonl", (l) => l.slice(0, 4))), PARENT), /expected 5/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit("cases.jsonl", (l) => [l[0]!.replace("--port", "--output"), ...l.slice(1)])), PARENT), /fixed serve contract/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit("cases.jsonl", (l) => [l[0]!.replace(/"expectedMinimizationDigest":"sha256:[0-9a-f]{64}"/, `"expectedMinimizationDigest":"sha256:${"0".repeat(64)}"`), ...l.slice(1)])), PARENT), /recomputed digest/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit("golden.jsonl", (l) => [l[0]!.replace('"exitCode":1', '"exitCode":0'), ...l.slice(1)])), PARENT), /nonzero exit/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit("golden.jsonl", (l) => [l[1]!, l[0]!, ...l.slice(2)])), PARENT), /is not binding-/);
});

// ---- strictness regressions (A4d): each mutates a staged tmp corpus only ----

test("validation rejects a corpus root that is a symbolic link", () => {
  const real = stagedCorpus();
  const link = join(mkdtempSync(join(tmpdir(), "faultline-binding-link-")), "link");
  symlinkSync(real, link);
  assert.doesNotThrow(() => validateBindingCorpus(real, PARENT)); // the target itself is fine
  assert.throws(() => validateBindingCorpus(link, PARENT), /symbolic link|not a directory|link/);
});

test("validation rejects an extra empty directory, at the root and below minimization", () => {
  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => mkdirSync(join(r, "empty"))), PARENT), /unexpected|directory/);
  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => mkdirSync(join(r, "minimization", "empty"))), PARENT), /unexpected|directory/);
});

test("validation rejects a duplicate member in a case row even when its value repeats", () => {
  const edit = (change: (line: string) => string) => (r: string): void => {
    const lines = readFileSync(join(r, "cases.jsonl"), "utf8").slice(0, -1).split("\n");
    lines[0] = change(lines[0]!);
    writeFileSync(join(r, "cases.jsonl"), `${lines.join("\n")}\n`);
  };
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit((l) => l.replace(/\}$/, ',"id":"binding-foreign-frozen"}'))), PARENT), /duplicate/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit((l) => l.replace(/\}$/, ',"input":"minimization/foreign-frozen.json"}'))), PARENT), /duplicate/);
});

test("validation rejects a duplicate or missing member in a golden row", () => {
  const edit = (change: (line: string) => string) => (r: string): void => {
    const lines = readFileSync(join(r, "golden.jsonl"), "utf8").slice(0, -1).split("\n");
    lines[0] = change(lines[0]!);
    writeFileSync(join(r, "golden.jsonl"), `${lines.join("\n")}\n`);
  };
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit((l) => l.replace('"signal":null', '"signal":null,"signal":null'))), PARENT), /duplicate|canonical/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit((l) => l.replace(',"signal":null', ""))), PARENT), /exactly the keys|signal/);
  assert.throws(() => validateBindingCorpus(stagedCorpus(edit((l) => l.replace('"stdout":"",', ""))), PARENT), /exactly the keys|stdout/);
});

test("validation rejects a duplicate member in inventory.json", () => {
  assert.throws(() => validateBindingCorpus(stagedCorpus((r) => {
    const text = readFileSync(join(r, "inventory.json"), "utf8");
    writeFileSync(join(r, "inventory.json"), text.replace('"schema": "faultline.serve-bundle-binding.v1",', '"schema": "faultline.serve-bundle-binding.v1",\n  "schema": "faultline.serve-bundle-binding.v1",'));
  }), PARENT), /duplicate|canonical/);
});

test("the corpus serializers are stable and its inventory records the original provenance", () => {
  const { texts, source } = records();
  const cases = makeBindingCases(texts, source.proofRoot);
  assert.equal(serializeCases(cases).split("\n").length, 6);
  assert.equal(serializeGoldens([]).length, 0);
  const inventory = makeInventory(texts, source);
  assert.equal(inventory.source.corpus, "serve-bundle");
  assert.equal(inventory.files.length, 5);
  assert.deepEqual(inventory.files.map((f) => f.path), BINDING_NAMES.map(bindingInputPath).sort());
  assert.deepEqual(BINDING_NAMES.map(bindingId), ["binding-foreign-frozen", "binding-foreign-witness", "binding-witness-null", "binding-nullable-states", "binding-different-range"]);
});

test("generator modes: default is --check, exactly one mode, nothing else", () => {
  assert.equal(parseBindingMode([]), "check");
  assert.equal(parseBindingMode(["--capture"]), "capture");
  assert.equal(parseBindingMode(["--check"]), "check");
  assert.throws(() => parseBindingMode(["--capture", "--check"]), /mutually exclusive/);
  assert.throws(() => parseBindingMode(["--recapture"]), /unknown argument/);
});

// Kept to prove the staging helper really isolates writes from the original corpus.
test("building and staging records never writes under the original corpus", () => {
  const before = existsSync(join(PARENT, "binding-minimization.json"));
  stagedCorpus();
  assert.equal(before, false);
  assert.equal(existsSync(join(PARENT, "binding-minimization.json")), false);
  // cpSync/mkdirSync are used only against tmp roots above.
  void cpSync; void mkdirSync;
});
