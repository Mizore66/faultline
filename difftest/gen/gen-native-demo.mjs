#!/usr/bin/env node
// Test-only full-output reference capture. Never invokes Go or the frozen Node witness.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { isAbsolute, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { frozenAnalysis, nativeContract, nativeReferenceInput, verifyResult } from "./native-demo-reference.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const corpus = join(root, "difftest/testdata/native-demo");
const ids = ["ordinary", "equal-wall-time", "backward-wall-time"];
const fileNames = ["inputs.jsonl", "frozen-reference.jsonl", "native-contract.jsonl"];
const modules = ["difftest/gen/gen-native-demo.mjs", "difftest/gen/native-demo-reference.mjs", "difftest/gen/native-demo-reference.test.mjs"];
const sha = (v) => createHash("sha256").update(v).digest("hex");
const serialize = (v) => JSON.stringify(v, null, 2) + "\n";
const jsonl = (rows) => rows.map((row) => JSON.stringify(row) + "\n").join("");

function assertRuntime() {
  assert.equal(process.versions.node, "22.22.2");
  assert.equal(sha(readFileSync(process.execPath)), "5c899797c4eb8f1db5563eea56538342ddb3e9276ee1b04a5a1f0f1023d2b011");
}

function parseRows(data) {
  assert.equal(data.at(-1), 10, "JSONL final LF required");
  const rows = data.toString("utf8").slice(0, -1).split("\n").map((s) => JSON.parse(s));
  assert.deepEqual(rows.map((r) => r.id), ids);
  for (const row of rows) {
    assert.deepEqual(Object.keys(row), ["id", "input", "expected"]);
    assert.equal(typeof row.expected, "string");
    assert.equal(serialize(JSON.parse(row.expected)), row.expected, "complete indented expected bytes required");
  }
  assert.equal(jsonl(rows), data.toString("utf8"));
  return rows;
}

function check() {
  const baselinePath = join(corpus, "baseline.json");
  if (!existsSync(baselinePath)) throw new Error("absent capture evidence: difftest/testdata/native-demo/baseline.json");
  assertRuntime();
  const baseline = JSON.parse(readFileSync(baselinePath, "utf8"));
  assert.equal(baseline.schemaVersion, "faultline.native-demo-corpus.v1");
  assert.equal(baseline.inputKind, "synthetic-observations-not-execution");
  assert.deepEqual(baseline.caseIds, ids);
  assert.equal(baseline.bindings.sourceCommit, "ce99a793af0b020bf1b9cf21114f8d7897b37207");
  assert.equal(baseline.bindings.nodeVersion, process.versions.node);
  assert.deepEqual(baseline.modules.map((f) => f.path), modules);
  for (const f of [...baseline.modules, ...baseline.bindings.sourceFiles]) {
    assert.equal(sha(readFileSync(join(root, f.path))), f.sha256, `bound source/module ${f.path}`);
  }
  assert.equal(sha(readFileSync(join(root, "pnpm-lock.yaml"))), baseline.bindings.lockSHA256);
  assert.deepEqual(baseline.files.map((f) => f.path), fileNames);
  const rows = {};
  for (const f of baseline.files) {
    const data = readFileSync(join(corpus, f.path));
    assert.equal(data.length, f.bytes);
    assert.equal(sha(data), f.sha256, `complete corpus bytes ${f.path}`);
    rows[f.path] = parseRows(data);
  }
  for (const [i, id] of ids.entries()) {
    const input = rows["inputs.jsonl"][i].input;
    assert.equal(rows["inputs.jsonl"][i].expected, serialize(input));
    assert.equal(JSON.stringify(rows["native-contract.jsonl"][i].input), JSON.stringify(input));
    const native = JSON.parse(rows["native-contract.jsonl"][i].expected);
    const frozen = JSON.parse(rows["frozen-reference.jsonl"][i].expected);
    assert.equal(input.captures.length, 39, id);
    assert.equal(native.provenance.invocations.length, 39, id);
    assert.equal(native.analysis.runCatalog.length, 39, id);
    assert.equal(frozen.runCatalog.length, 39, id);
    assert.equal(frozen.witness.id, "refund-settlement-currency");
    assert.equal(native.analysis.witness.id, "refund-settlement-currency-go-model");
  }
  console.log("complete native corpus verified: 3 cases; full file bytes/hashes, inputs and both output profiles; not execution evidence");
}

async function capture(compiledRoot, out, recapture) {
  assertRuntime();
  assert.ok(isAbsolute(compiledRoot) && isAbsolute(out), "absolute compiled root/output required");
  // Exclusive allocation: failures leave their partial evidence in place.
  mkdirSync(out);
  const inputs = [], frozenRows = [], nativeRows = [];
  for (const id of ids) {
    const input = await nativeReferenceInput({ compiledRoot, scenario: id });
    const result = await nativeContract({ compiledRoot, ...input });
    const fixture = await import(pathToFileURL(join(compiledRoot, "fixture.js")).href);
    const { digestJson } = await import(pathToFileURL(join(compiledRoot, "canonical.js")).href);
    const witness = fixture.createFrozenWitness();
    const calls = result.provenance.invocations.map((r, i) => {
      const { id: ignored, ...body } = r.runRecord;
      body.witnessDigest = witness.digest;
      const runId = digestJson({ schemaVersion: "faultline.scripted-frozen-reference-input.v1", ordinal: i + 1, record: body });
      return { state: input.captures[i].state, executionAttempt: r.executionAttempt, record: { id: runId, ...body } };
    });
    const frozenInput = { inputKind: input.inputKind, calls, generatedAt: input.generatedAt };
    const frozen = await frozenAnalysis({ compiledRoot, ...frozenInput });
    inputs.push({ id, input, expected: serialize(input) });
    frozenRows.push({ id, input: frozenInput, expected: serialize(frozen) });
    nativeRows.push({ id, input, expected: serialize(result) });
  }
  const contents = [jsonl(inputs), jsonl(frozenRows), jsonl(nativeRows)].map((s) => Buffer.from(s));
  for (const [i, file] of fileNames.entries()) writeFileSync(join(out, file), contents[i], { flag: "wx" });
  const tests = spawnSync(process.execPath, ["--experimental-vm-modules", join(root, "difftest/gen/native-demo-reference.test.mjs")], {
    cwd: root, env: { ...process.env, NODE_OPTIONS: "", NATIVE_REFERENCE_INPUT: join(out, "inputs.jsonl"), NATIVE_REFERENCE_COMPILED_ROOT: compiledRoot },
    timeout: 30000, maxBuffer: 1024 * 1024,
  });
  writeFileSync(join(out, "reference-tests.stdout"), tests.stdout ?? Buffer.alloc(0), { flag: "wx" });
  writeFileSync(join(out, "reference-tests.stderr"), tests.stderr ?? Buffer.alloc(0), { flag: "wx" });
  assert.equal(tests.error, undefined);
  assert.equal(tests.signal, null);
  assert.equal(tests.status, 0, `reference tests failed; retained complete streams in ${out}`);
  const baseline = {
    schemaVersion: "faultline.native-demo-corpus.v1", inputKind: "synthetic-observations-not-execution", caseIds: ids,
    bindings: JSON.parse(readFileSync(join(compiledRoot, "bindings.json"), "utf8")),
    modules: modules.map((path) => ({ path, sha256: sha(readFileSync(join(root, path))) })),
    files: fileNames.map((path, i) => ({ path, bytes: contents[i].length, sha256: sha(contents[i]) })),
  };
  const baselineBytes = Buffer.from(serialize(baseline));
  writeFileSync(join(out, "baseline.json"), baselineBytes, { flag: "wx" });
  if (recapture) {
    check();
    for (const [i, file] of fileNames.entries()) verifyResult({ expected: readFileSync(join(corpus, file)), actualBytes: contents[i] });
    verifyResult({ expected: readFileSync(join(corpus, "baseline.json")), actualBytes: baselineBytes });
  }
  console.log(`${recapture ? "independent byte-identical recapture" : "reference capture"}: 3 cases, 39 calls each, full frozen/native outputs; ${out}`);
  for (const f of baseline.files) console.log(`${f.path}: ${f.bytes} bytes sha256:${f.sha256}`);
}

async function verifyLive(compiledRoot, inputPath, expectedOut) {
  assert.ok([compiledRoot, inputPath, expectedOut].every(isAbsolute), "absolute live-reference paths required");
  const retained = JSON.parse(readFileSync(inputPath, "utf8"));
  assert.equal(retained.input.inputKind, "observed-native-process-transcript");
  assert.equal(typeof retained.actual, "string", "entire original candidate result bytes required");
  const result = await nativeContract({ compiledRoot, ...retained.input });
  const expected = Buffer.from(serialize(result));
  writeFileSync(expectedOut, expected, { flag: "wx" });
  verifyResult({ expected, actualBytes: Buffer.from(retained.actual) });
  console.log("complete observed native result matches independent reference; reference replay alone is not proof of process execution");
}

async function main(args) {
  const [mode, ...flags] = args;
  if (mode === "check" && flags.length === 0) return check();
  const opts = new Map();
  assert.equal(flags.length % 2, 0);
  for (let i = 0; i < flags.length; i += 2) {
    assert.ok(!opts.has(flags[i]), "duplicate option");
    opts.set(flags[i], flags[i + 1]);
  }
  if (["capture", "recapture"].includes(mode)) {
    assert.deepEqual([...opts.keys()].sort(), ["--compiled-root", "--out"]);
    return capture(opts.get("--compiled-root"), opts.get("--out"), mode === "recapture");
  }
  assert.equal(mode, "verify-live", "expected capture, recapture, check, or verify-live");
  assert.deepEqual([...opts.keys()].sort(), ["--compiled-root", "--expected-out", "--input"]);
  return verifyLive(opts.get("--compiled-root"), opts.get("--input"), opts.get("--expected-out"));
}

try {
  await main(process.argv.slice(2));
} catch (error) {
  console.error(`gen-native-demo: ${error.message}`);
  process.exitCode = 1;
}
