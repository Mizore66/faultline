// Tests for generator modes, preflight decisions, strict corpus validation and
// the explicit mutation builder. Expectations are literals owned here.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, existsSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  BASELINE_TEXT, assertAbsentDestination, inventoryDirectory, isSafeRelative, parseModes, preflightErrors,
  serializeFixturesFile, validateCorpus, validateFixturesFile, verifyInventory, type FixturesFile
} from "./serve-bundle-corpus.js";

const D = (c: string): string => `sha256:${c.repeat(64)}`;
const sha = (text: string): string => createHash("sha256").update(text).digest("hex");

function makeCorpus(mutate?: (root: string, file: FixturesFile) => void): { root: string; file: FixturesFile } {
  const root = mkdtempSync(join(tmpdir(), "faultline-serve-corpus-"));
  const coherent = join(root, "inputs", "coherent");
  mkdirSync(join(coherent, "bundle"), { recursive: true });
  writeFileSync(join(coherent, "bundle", "a.json"), "{\"a\":1}\r\n");
  writeFileSync(join(coherent, "minimization.json"), "");
  writeFileSync(join(root, "baseline.json"), BASELINE_TEXT);
  writeFileSync(join(root, ".gitattributes"), "* -text\n");
  const file: FixturesFile = {
    schema: "faultline.serve-bundle-fixtures.v1",
    fixtures: [{
      id: "coherent", root: "inputs/coherent",
      files: [
        { path: "bundle/a.json", sha256: sha("{\"a\":1}\r\n"), bytes: 9 },
        { path: "minimization.json", sha256: sha(""), bytes: 0 }
      ],
      digests: { proofRoot: D("1"), minimization: D("2"), repair: D("3"), preventionSummary: D("4"), preventionVerified: D("5") },
      privateSentinels: ["FL_PRIVATE_OVERLAY_5C_NOT_FOR_HTML"]
    }]
  };
  mutate?.(root, file);
  writeFileSync(join(root, "fixtures.json"), serializeFixturesFile(file));
  return { root, file };
}

test("a well-formed synthetic corpus validates (guards the negative tests below)", () => {
  assert.doesNotThrow(() => validateCorpus(makeCorpus().root));
});

test("modes: default is --check; exactly one mode; --only is diagnostic and never with first capture", () => {
  assert.deepEqual(parseModes([]), { mode: "check", only: null });
  assert.deepEqual(parseModes(["--recapture"]), { mode: "recapture", only: null });
  assert.deepEqual(parseModes(["--capture-fixtures"]), { mode: "capture-fixtures", only: null });
  assert.deepEqual(parseModes(["--check", "--only", "view-root-absent"]), { mode: "check", only: "view-root-absent" });
  assert.deepEqual(parseModes(["--only", "cli-port-nan"]), { mode: "check", only: "cli-port-nan" });
  for (const combo of [["--check", "--recapture"], ["--capture-fixtures", "--check"], ["--recapture", "--capture-fixtures"]]) {
    assert.throws(() => parseModes(combo), /mutually exclusive/, combo.join(" "));
  }
  assert.throws(() => parseModes(["--capture-fixtures", "--only", "view-root-absent"]), /diagnostic/);
  assert.throws(() => parseModes(["--only"]), /exact case id/);
  assert.throws(() => parseModes(["--only", "--check"]), /exact case id/);
  assert.throws(() => parseModes(["--only", "a", "--only", "b"]), /once/);
  assert.throws(() => parseModes(["--rebuild"]), /unknown argument/);
});

test("preflight accepts only the pinned Node, manifest and fresh CLI version line", () => {
  const good = { nodeVersion: "22.22.2", manifestVersion: "0.1.4", cliVersionOutput: "FaultLine 0.1.4\n" };
  assert.deepEqual(preflightErrors(good), []);
  assert.equal(preflightErrors({ ...good, nodeVersion: "22.23.4" }).length, 1);
  assert.equal(preflightErrors({ ...good, nodeVersion: "22.22.20" }).length, 1);
  assert.equal(preflightErrors({ ...good, manifestVersion: "0.1.3" }).length, 1);
  assert.equal(preflightErrors({ ...good, manifestVersion: undefined }).length, 1);
  assert.equal(preflightErrors({ ...good, cliVersionOutput: "FaultLine 0.1.4" }).length, 1);
  assert.equal(preflightErrors({ ...good, cliVersionOutput: "FaultLine 0.1.3\n" }).length, 1);
  assert.equal(preflightErrors({ nodeVersion: "20.0.0", manifestVersion: "x", cliVersionOutput: "" }).length, 3);
});

test("first capture refuses an existing destination without touching it", () => {
  const existing = mkdtempSync(join(tmpdir(), "faultline-serve-dest-"));
  writeFileSync(join(existing, "keep"), "x");
  assert.throws(() => assertAbsentDestination(existing), /already exists/);
  assert.equal(readFileSync(join(existing, "keep"), "utf8"), "x");
  assert.doesNotThrow(() => assertAbsentDestination(join(existing, "absent")));
  assert.equal(existsSync(join(existing, "absent")), false);
});

test("safe relative paths reject traversal, absolute, backslash, drive and empty components", () => {
  for (const ok of ["a", "a/b.json", ".faultline/x"]) assert.equal(isSafeRelative(ok), true, ok);
  for (const bad of ["", "/a", "../a", "a/../b", "a//b", "./a", "a/", "a\\b", "C:/x", "a\0b"]) assert.equal(isSafeRelative(bad), false, JSON.stringify(bad));
});

test("fixtures.json must carry exactly the contracted keys, shapes and digest strings", () => {
  const { file } = makeCorpus();
  const base = JSON.parse(JSON.stringify(file));
  const bad: Array<[string, (v: any) => void]> = [
    ["schema", (v) => { v.schema = "faultline.serve-bundle-fixtures.v2"; }],
    ["extra top key", (v) => { v.extra = 1; }],
    ["extra fixture key", (v) => { v.fixtures[0].extra = 1; }],
    ["missing digest key", (v) => { delete v.fixtures[0].digests.repair; }],
    ["uppercase digest", (v) => { v.fixtures[0].digests.repair = `sha256:${"A".repeat(64)}`; }],
    ["short digest", (v) => { v.fixtures[0].digests.proofRoot = "sha256:abc"; }],
    ["root traversal", (v) => { v.fixtures[0].root = "../x"; }],
    ["unsafe file path", (v) => { v.fixtures[0].files[0].path = "../x"; }],
    ["duplicate file path", (v) => { v.fixtures[0].files.push({ ...v.fixtures[0].files[0] }); }],
    ["uppercase file sha", (v) => { v.fixtures[0].files[0].sha256 = v.fixtures[0].files[0].sha256.toUpperCase(); }],
    ["negative bytes", (v) => { v.fixtures[0].files[0].bytes = -1; }],
    ["fractional bytes", (v) => { v.fixtures[0].files[0].bytes = 1.5; }],
    ["duplicate fixture", (v) => { v.fixtures.push(JSON.parse(JSON.stringify(v.fixtures[0]))); }],
    ["empty sentinel", (v) => { v.fixtures[0].privateSentinels = [""]; }],
    ["no fixtures", (v) => { v.fixtures = []; }]
  ];
  for (const [name, mutate] of bad) {
    const copy = JSON.parse(JSON.stringify(base));
    mutate(copy);
    assert.throws(() => validateFixturesFile(copy), Error, name);
  }
});

test("corpus validation rejects wrong baseline, gitattributes, non-canonical or duplicate-member fixtures.json", () => {
  const a = makeCorpus(); writeFileSync(join(a.root, "baseline.json"), BASELINE_TEXT.replace("22.22.2", "22.23.4"));
  assert.throws(() => validateCorpus(a.root), /baseline/);
  const b = makeCorpus(); writeFileSync(join(b.root, ".gitattributes"), "* text=auto\n");
  assert.throws(() => validateCorpus(b.root), /gitattributes/);
  const c = makeCorpus();
  const original = readFileSync(join(c.root, "fixtures.json"), "utf8");
  writeFileSync(join(c.root, "fixtures.json"), original.replace("\"schema\":", "\"schema\": \"x\",\n  \"schema\":"));
  assert.throws(() => validateCorpus(c.root));
  const d = makeCorpus(); writeFileSync(join(d.root, "fixtures.json"), JSON.stringify(JSON.parse(readFileSync(join(d.root, "fixtures.json"), "utf8"))));
  assert.throws(() => validateCorpus(d.root), /canonical/);
});

test("inventory integrity: changed bytes, missing entry, extra file and link all fail", () => {
  for (const [name, mutate, pattern] of [
    ["changed same length", (r: string) => writeFileSync(join(r, "inputs/coherent/bundle/a.json"), "{\"a\":2}\r\n"), /sha256/],
    ["changed length", (r: string) => writeFileSync(join(r, "inputs/coherent/bundle/a.json"), "{\"a\":1}\n"), /bytes/],
    ["extra file", (r: string) => writeFileSync(join(r, "inputs/coherent/extra"), "x"), /not in the inventory/],
    ["removed entry", (r: string) => writeFileSync(join(r, "inputs/coherent/minimization.json"), "x"), /bytes/]
  ] as Array<[string, (r: string) => void, RegExp]>) {
    const { root } = makeCorpus();
    mutate(root);
    assert.throws(() => validateCorpus(root), pattern, name);
  }
  const missing = makeCorpus((root) => { /* inventoried file absent on disk */ writeFileSync(join(root, "inputs/coherent/placeholder"), ""); });
  assert.throws(() => validateCorpus(missing.root), /not in the inventory/);
  const dropped = makeCorpus((_root, file) => { file.fixtures[0]!.files.pop(); });
  assert.throws(() => validateCorpus(dropped.root), /not in the inventory/);
  const absent = makeCorpus((_root, file) => { file.fixtures[0]!.files.push({ path: "ghost.json", sha256: sha(""), bytes: 0 }); });
  assert.throws(() => validateCorpus(absent.root), /missing/);
});

test("inventoryDirectory records exact bytes, sorted, regular files only", () => {
  const { root } = makeCorpus();
  const files = inventoryDirectory(join(root, "inputs/coherent"));
  assert.deepEqual(files.map((f) => f.path), ["bundle/a.json", "minimization.json"]);
  assert.equal(files[0]!.bytes, 9);
  assert.equal(files[1]!.sha256, sha(""));
  assert.doesNotThrow(() => verifyInventory(join(root, "inputs/coherent"), files));
  assert.deepEqual(readdirSync(join(root, "inputs/coherent")).sort(), ["bundle", "minimization.json"]);
});
