// Test-only reference-loader tests. Scripted records are synthetic, not process evidence.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";
import { frozenAnalysis, nativeCopyContract, referenceImportAllowed, verifyResult } from "./native-demo-reference.mjs";
import * as reference from "./native-demo-reference.mjs";

const compiledRoot = process.env.NATIVE_REFERENCE_COMPILED_ROOT;
assert.ok(compiledRoot && isAbsolute(compiledRoot), "explicit bound compiled root required");
const inputPath = process.env.NATIVE_REFERENCE_INPUT;
assert.ok(inputPath && isAbsolute(inputPath), "explicit retained reference input required");
const retainedRows = readFileSync(inputPath, "utf8").trimEnd().split("\n").map((s) => JSON.parse(s));
assert.deepEqual(retainedRows.map((r) => r.id), ["ordinary", "equal-wall-time", "backward-wall-time"]);
const retainedInputs = new Map(retainedRows.map((r) => [r.id, r.input]));
const fixture = await import(pathToFileURL(join(compiledRoot, "fixture.js")).href);
const generatedAt = "2026-11-03T00:01:00.000Z";
const clone = (v) => JSON.parse(JSON.stringify(v));

// Only execution is scripted: expected order/state tuples come from the real frozen definitions.
function scriptedInput() {
  const f = fixture.fixtureStates;
  const sequence = [
    ...fixture.contributionStates.map((s) => [s, 0]),
    ...fixture.timelineStates.map((s) => [s, 0]),
    ...[[f.turn4, f.turn5], [f.turn6, f.turn7], [f.turn7, f.turn8]]
      .flatMap((pair) => pair.flatMap((s) => [1, 2, 3].map((attempt) => [s, attempt]))),
    ...[f.sourceOnly, f.rendererOnly, f.unresolved, f.candidate, f.reverted, f.lastGood,
      f.lastGood, f.firstBad, f.repaired].map((s) => [s, 0]),
  ];
  const witness = fixture.createFrozenWitness();
  const calls = sequence.map(([state, executionAttempt], i) => {
    const partial = state.hunks.includes("partial-renderer-hunk");
    const fail = state.hunks.includes("settlement-display-default")
      && state.hunks.includes("settlement-display-boundary");
    const id = `sha256:${createHash("sha256").update(`synthetic-reference-input:${i + 1}`).digest("hex")}`;
    const record = {
      id, stateId: state.id, witnessDigest: witness.digest,
      environmentDigest: fixture.environmentDigest,
      verdict: partial ? "ERROR" : fail ? "FAIL" : "PASS",
      reasonCode: partial ? "FIXTURE_PARSE_ERROR" : fail ? "PREDICATE_FAIL" : "PREDICATE_PASS",
      stdout: partial ? "" : fail
        ? '{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}\n'
        : 'settlement currency preserved\n{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"}\n',
      stderr: partial ? "native fixture config: scripted missing assignment value\n"
        : fail ? "expected settlement USD; received EUR\n" : "",
      durationMs: i % 3, executionKind: "EXECUTED", executedAt: "2026-11-03T00:00:00.000Z",
    };
    return { state: clone(state), executionAttempt, record };
  });
  return { compiledRoot, calls, generatedAt };
}

// A missing/extra/reordered call or altered record must not silently produce a plausible analysis.
test("unchanged frozen engine assembles all 39 complete scripted calls", async () => {
  const input = scriptedInput();
  const result = await frozenAnalysis(input);
  assert.equal(result.generatedAt, generatedAt);
  assert.equal(result.mode, "RERUN");
  assert.equal(result.runCatalog.length, 39);
  assert.equal(result.transitions.length, 3);
  assert.deepEqual(result.transitions.map((t) => [t.beforeStateId, t.afterStateId, t.kind, t.stable]), [
    ["cedar-turn-4", "cedar-turn-5", "PASS_TO_FAIL", true],
    ["cedar-turn-6", "cedar-turn-7", "FAIL_TO_PASS", true],
    ["cedar-turn-7", "cedar-turn-8", "PASS_TO_FAIL", true],
  ]);
  assert.equal(result.minimization.attempts.length, 6);
  assert.equal(result.minimization.attempts[3].outcome, "UNRESOLVED");
  assert.equal(result.prevention.verified, true);
  assert.equal(result.grade.value, "A");
  assert.deepEqual(result.witness, fixture.createFrozenWitness());
  assert.deepEqual(result.contributionRuns, input.calls.slice(0, 4).map((c) => c.record));
  const ordered = [...input.calls.map((c) => c.record)].sort((a, b) => a.id.localeCompare(b.id));
  assert.deepEqual(result.runCatalog, ordered);
});

for (const [name, mutate, error] of [
  ["missing call", (c) => c.shift(), /exact 39-call transcript required/],
  ["extra call", (c) => c.push(clone(c[0])), /exact 39-call transcript required/],
  ["reordered calls", (c) => [c[0], c[1]] = [c[1], c[0]], /state tuple at invocation 1/],
  ["wrong attempt", (c) => c[12].executionAttempt = 0, /attempt at invocation 13/],
  ["wrong state tuple", (c) => c[0].state.treeDigest = "sha256:wrong", /state tuple at invocation 1/],
  ["wrong state hunks", (c) => c[0].state.hunks = ["settlement-display-default"], /state tuple at invocation 1/],
  ["wrong record state", (c) => c[0].record.stateId = "cedar-turn-5",
    { code: "ERR_ASSERTION", actual: "cedar-turn-5", expected: fixture.contributionStates[0].id }],
  ["wrong witness", (c) => c[0].record.witnessDigest = "sha256:wrong",
    { code: "ERR_ASSERTION", actual: "sha256:wrong", expected: fixture.createFrozenWitness().digest }],
  ["duplicate id", (c) => c[1].record.id = c[0].record.id, /duplicate scripted run ID/],
]) {
  test(`strict reference rejects ${name}`, async () => {
    const input = scriptedInput();
    mutate(input.calls);
    await assert.rejects(() => frozenAnalysis(input), error);
  });
}

test("strict reference rejects unconsumed calls after a 39-call transcript changes engine control flow", async () => {
  const input = scriptedInput();
  // No timeline transitions: the engine consumes 12 initial calls and 9 final calls.
  const original = input.calls;
  input.calls = [...original.slice(0, 12), ...original.slice(30), ...original.slice(12, 30)];
  for (const call of input.calls.slice(4, 12)) call.record.verdict = "PASS";
  assert.equal(input.calls.length, 39);
  await assert.rejects(() => frozenAnalysis(input), {
    code: "ERR_ASSERTION", message: /unconsumed scripted invocations/, actual: 21, expected: 39,
  });
});

test("strict reference rejects unexpected invocation 40 after 39 fully matched calls", async () => {
  const input = scriptedInput();
  // Alternating timeline verdicts cause seven boundaries instead of three.
  const boundaries = fixture.timelineStates.slice(1).flatMap((after, i) =>
    [fixture.timelineStates[i], after].flatMap((state) => [1, 2, 3].map((attempt) => {
      const call = clone(input.calls.find((c) => c.state.id === state.id));
      call.executionAttempt = attempt;
      call.record.id = `sha256:${createHash("sha256").update(`expanded-boundary:${i}:${state.id}:${attempt}`).digest("hex")}`;
      return call;
    })));
  input.calls = [...input.calls.slice(0, 12), ...boundaries.slice(0, 27)];
  for (const [i, call] of input.calls.slice(4, 12).entries()) call.record.verdict = i % 2 ? "FAIL" : "PASS";
  assert.equal(input.calls.length, 39);
  await assert.rejects(() => frozenAnalysis(input), /unexpected reference invocation 40/);
});

test("native witness is only an explicit reference profile input", async () => {
  const input = scriptedInput();
  const witness = fixture.createFrozenWitness();
  witness.id = "refund-settlement-currency-go-model";
  witness.digest = `sha256:${"1".repeat(64)}`;
  for (const c of input.calls) c.record.witnessDigest = witness.digest;
  const result = await frozenAnalysis({ ...input, witness });
  assert.deepEqual(result.witness, witness);
});

test("native copy contract changes only the seven approved copy paths", async () => {
  const frozen = await frozenAnalysis(scriptedInput());
  const before = JSON.stringify(frozen);
  const native = nativeCopyContract(frozen);
  const expected = clone(frozen);
  expected.fixture.title = "Parallel settlement-currency regression — Go model";
  expected.fixture.summary = "A reviewed Go model evaluated under materialized configuration, with a real native witness and a two-edit interaction.";
  expected.minimization.attempts[3].note = "The partial native configuration cannot parse and is unresolved, not proof.";
  expected.minimization.attempts[4].note = "Applying the two edits to last-good reproduces the approved native witness failure.";
  expected.minimization.attempts[5].note = "Removing the two edits from first-bad restores the approved native witness pass.";
  expected.claims[0].statement = "The approved native witness passes before and fails after Session Cedar turn 5.";
  expected.warning = "Executed reviewed Go model. This does not execute the original JavaScript or external repository code and is not a general-purpose sandbox.";
  assert.equal(JSON.stringify(native), JSON.stringify(expected));
  assert.equal(JSON.stringify(frozen), before, "native expected-copy construction mutated frozen reference");
  frozen.minimization.attempts[4].note += " unreviewed copy";
  assert.throws(() => nativeCopyContract(frozen), /unreviewed native-copy anchor minimization\.attempts\.4\.note/);
});

test("import edges reject unbound packages, built-ins, and importer substitutions", () => {
  referenceImportAllowed("./fixture.js", "engine.js");
  referenceImportAllowed("node:crypto", "canonical.js");
  for (const [specifier, importer] of [["node:net", "engine.js"], ["node:fs", "canonical.js"], ["other-package", "fixture.js"], ["../engine.js", "engine.js"]]) {
    assert.throws(() => referenceImportAllowed(specifier, importer), /unbound reference import/);
  }
});

for (const scenario of ["ordinary", "equal-wall-time", "backward-wall-time"]) {
  test(`independent native result constructs complete identity and receipts: ${scenario}`, async () => {
    assert.equal(typeof reference.nativeReferenceInput, "function", "missing independent synthetic input constructor");
    assert.equal(typeof reference.nativeContract, "function", "missing independent native contract constructor");
    const input = clone(retainedInputs.get(scenario));
    assert.deepEqual(await reference.nativeReferenceInput({ compiledRoot, scenario }), input);
    const result = await reference.nativeContract({ compiledRoot, ...input });
    const { analysis, provenance } = result;
    const { digestJson } = await import(pathToFileURL(join(compiledRoot, "canonical.js")).href);
    assert.equal(analysis.runCatalog.length, 39);
    assert.equal(provenance.invocations.length, 39);
    assert.equal(new Set(provenance.invocations.map((r) => r.runId)).size, 39);
    assert.equal(provenance.status, "COMPLETE");
    assert.equal(analysis.witness.approvedAt, input.approval.approvedAt);
    assert.equal(analysis.witness.id, "refund-settlement-currency-go-model");
    const { digest: proposalDigest, ...proposalBody } = input.proposal;
    assert.equal(proposalDigest, digestJson(proposalBody));
    assert.equal(provenance.programDigest, digestJson(input.proposal.program));
    assert.equal(provenance.environmentDigest, digestJson(input.environmentPreimage));
    for (const [i, receipt] of provenance.invocations.entries()) {
      const c = input.captures[i];
      const { id, ...body } = receipt.runRecord;
      const expectedPreimage = {
        schemaVersion: "faultline.native-demo-run-id.v1", nonce: input.nonce,
        ordinal: i + 1, executionAttempt: c.executionAttempt, startedAt: c.startedAt,
        configDigest: c.config.sha256, status: c.status, signal: c.signal, record: body,
      };
      assert.equal(JSON.stringify(receipt.runIdPreimage), JSON.stringify(expectedPreimage));
      assert.equal(id, digestJson(expectedPreimage));
      assert.deepEqual(receipt, {
        ordinal: i + 1, stateId: c.state.id, executionAttempt: c.executionAttempt,
        config: c.config, expectedPartial: c.expectedPartial, executable: c.executable,
        argv: c.argv, cwd: c.cwd, environment: c.environment, startedAt: c.startedAt,
        executedAt: c.executedAt, durationMs: c.durationMs, status: c.status, signal: c.signal,
        stdout: c.stdout, stderr: c.stderr, accepted: true, runId: id,
        runRecord: receipt.runRecord, runIdPreimage: expectedPreimage, failureKind: null, cleanupError: null,
      });
      assert.deepEqual(analysis.runCatalog.find((r) => r.id === id), receipt.runRecord);
      for (const blob of [receipt.config, receipt.stdout, receipt.stderr]) {
        assert.equal(blob.sha256, `sha256:${createHash("sha256").update(Buffer.from(blob.data, "base64")).digest("hex")}`);
      }
    }
    const frozen = await frozenAnalysis({ compiledRoot, calls: provenance.invocations.map((r, i) => ({ state: input.captures[i].state, executionAttempt: r.executionAttempt, record: r.runRecord })), generatedAt: input.generatedAt, witness: analysis.witness });
    assert.equal(JSON.stringify(analysis), JSON.stringify(nativeCopyContract(frozen)));
    const first = provenance.invocations[0];
    if (scenario === "equal-wall-time") assert.ok(provenance.invocations.every((r) => r.startedAt === first.startedAt && r.executedAt === first.executedAt));
    if (scenario === "backward-wall-time") assert.ok(provenance.invocations.every((r, i, a) => !i || r.startedAt < a[i - 1].startedAt));
    assert.equal(input.inputKind, "synthetic-observations-not-execution");
  });
}

for (const [name, mutate, error] of [
  ["blob hash", (v) => v.captures[0].stdout.sha256 = `sha256:${"0".repeat(64)}`, /raw blob hash differs/],
  ["blob bytes", (v) => v.captures[0].stdout.data = "eA==", /raw blob hash differs/],
  ["noncanonical base64", (v) => v.captures[0].stdout.data += "\n", /noncanonical base64/],
  ["incomplete stream", (v) => v.captures[0].stdout.complete = false, /incomplete raw evidence cannot be accepted/],
  ["wrong config", (v) => v.captures[0].config = v.captures[2].config, /configuration bytes differ/],
  ["missing invocation", (v) => v.captures.pop(), { code: "ERR_ASSERTION", actual: 38, expected: 39 }],
  ["wrong environment", (v) => v.captures[0].environment.push("PATH=/unreviewed"), /PATH=\/unreviewed/],
  ["status contradiction", (v) => v.captures[0].status = 1,
    { code: "ERR_ASSERTION", actual: 'settlement currency preserved\n{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"}\n', expected: '{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}\n' }],
  ["wrong attempt", (v) => v.captures[12].executionAttempt = 0, { code: "ERR_ASSERTION", actual: 0, expected: 1 }],
  ["changed program", (v) => v.proposal.program.input.order.currency = "EUR", /unreviewed proposal\/program/],
  ["wrong proposal approval", (v) => v.approval.proposalDigest = `sha256:${"0".repeat(64)}`,
    { code: "ERR_ASSERTION", actual: `sha256:${"0".repeat(64)}`, expected: retainedInputs.get("ordinary").proposal.digest }],
  ["future approval", (v) => v.approval.approvedAt = "2026-12-03T00:00:00.000Z", /approval after API entry/],
  ["failed cleanup", (v) => v.captures[0].cleanupError = "scripted cleanup failure",
    { code: "ERR_ASSERTION", actual: "scripted cleanup failure", expected: null }],
]) {
  test(`independent native contract rejects ${name}`, async () => {
    assert.equal(typeof reference.nativeReferenceInput, "function", "missing independent synthetic input constructor");
    const input = clone(retainedInputs.get("ordinary"));
    mutate(input);
    await assert.rejects(() => reference.nativeContract({ compiledRoot, ...input }), error);
  });
}

function withPartialDiagnostic(stderr) {
  const input = clone(retainedInputs.get("ordinary"));
  const bytes = Buffer.from(stderr);
  input.captures[32].stderr = {
    encoding: "base64", data: bytes.toString("base64"),
    sha256: `sha256:${createHash("sha256").update(bytes).digest("hex")}`, complete: true,
  };
  return input;
}

test("native parse diagnostic retains the actual decoder cause instead of the scripted example", async () => {
  const stderr = "native fixture config: unexpected end of JSON input\n";
  const input = withPartialDiagnostic(stderr);
  const result = await reference.nativeContract({ compiledRoot, ...input });
  const receipt = result.provenance.invocations[32];
  assert.deepEqual(receipt.stderr, input.captures[32].stderr);
  assert.equal(receipt.runRecord.stderr, stderr);
  assert.equal(receipt.runIdPreimage.record.stderr, stderr);
  assert.equal(receipt.runRecord.reasonCode, "FIXTURE_PARSE_ERROR");
  assert.deepEqual(result.analysis.runCatalog.find((r) => r.id === receipt.runId), receipt.runRecord);
});

for (const [name, stderr] of [
  ["absent decoder identity", "unexpected end of JSON input\n"],
  ["absent cause", "native fixture config: \n"],
  ["extra LF", "native fixture config: unexpected EOF\n\n"],
  ["embedded LF", "native fixture config: unexpected\nEOF\n"],
  ["absent final LF", "native fixture config: unexpected EOF"],
  ["CRLF", "native fixture config: unexpected EOF\r\n"],
]) {
  test(`native parse diagnostic rejects ${name}`, async () => {
    const input = withPartialDiagnostic(stderr);
    await assert.rejects(() => reference.nativeContract({ compiledRoot, ...input }), {
      code: "ERR_ASSERTION", actual: stderr, expected: /^native fixture config: [^\r\n]+\n$/,
    });
  });
}

const root = fileURLToPath(new URL("../../", import.meta.url));
const generator = fileURLToPath(new URL("./gen-native-demo.mjs", import.meta.url));
const serialize = (value) => JSON.stringify(value, null, 2) + "\n";
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");
const scratch = () => mkdtempSync(join(dirname(compiledRoot), "native-reference-cli "));

function runGenerator(args, script = generator) {
  const child = spawnSync(process.execPath, ["--experimental-vm-modules", script, ...args], {
    env: { ...process.env, NODE_OPTIONS: "" }, encoding: "utf8", timeout: 15000, maxBuffer: 1024 * 1024,
  });
  const log = scratch();
  writeFileSync(join(log, "stdout"), child.stdout ?? "", { flag: "wx" });
  writeFileSync(join(log, "stderr"), child.stderr ?? "", { flag: "wx" });
  assert.equal(child.error, undefined);
  assert.equal(child.signal, null);
  return child;
}

function liveFixture(inputKind = "observed-native-process-transcript") {
  const dir = scratch();
  const rows = readFileSync(join(dirname(inputPath), "native-contract.jsonl"), "utf8")
    .trimEnd().split("\n").map((s) => JSON.parse(s));
  const expected = rows.find((r) => r.id === "ordinary").expected;
  const input = { ...clone(retainedInputs.get("ordinary")), inputKind };
  const path = join(dir, "retained input.json");
  const expectedOut = join(dir, "expected output.json");
  return {
    dir, input, expected, path, expectedOut,
    args: ["verify-live", "--compiled-root", compiledRoot, "--input", path, "--expected-out", expectedOut],
  };
}

// The CLI boundary is real; these remain synthetic observations, not proof of child execution.
test("generator verify-live accepts complete matching bytes and retains the entire expected output", () => {
  const f = liveFixture();
  writeFileSync(f.path, serialize({ input: f.input, actual: f.expected }), { flag: "wx" });
  const child = runGenerator(f.args);
  assert.equal(child.status, 0, child.stderr);
  assert.match(child.stdout, /complete observed native result matches independent reference/);
  assert.ok(readFileSync(f.expectedOut).equals(Buffer.from(f.expected)));
});

test("generator verify-live rejects one changed byte at its offset and retains the entire expected output", () => {
  const f = liveFixture();
  const offset = f.expected.indexOf("2026-11-03T00:01:00.000Z");
  assert.ok(offset >= 0);
  const actual = f.expected.slice(0, offset) + "1" + f.expected.slice(offset + 1);
  writeFileSync(f.path, serialize({ input: f.input, actual }), { flag: "wx" });
  const child = runGenerator(f.args);
  assert.equal(child.status, 1);
  assert.equal(child.stdout, "");
  assert.ok(child.stderr.includes(`complete reference bytes differ at offset ${offset} (expected ${Buffer.byteLength(f.expected)}, actual ${Buffer.byteLength(actual)})`));
  assert.ok(readFileSync(f.expectedOut).equals(Buffer.from(f.expected)));
  assert.equal(JSON.parse(readFileSync(f.path, "utf8")).actual, actual);
});

test("generator verify-live rejects synthetic input kind before writing expected output", () => {
  const f = liveFixture("synthetic-observations-not-execution");
  writeFileSync(f.path, serialize({ input: f.input, actual: f.expected }), { flag: "wx" });
  const child = runGenerator(f.args);
  assert.equal(child.status, 1);
  assert.match(child.stderr, /observed-native-process-transcript/);
  assert.equal(existsSync(f.expectedOut), false);
});

test("generator verify-live refuses to overwrite existing expected output", () => {
  const f = liveFixture();
  writeFileSync(f.path, serialize({ input: f.input, actual: f.expected }), { flag: "wx" });
  writeFileSync(f.expectedOut, "retained sentinel\n", { flag: "wx" });
  const child = runGenerator(f.args);
  assert.equal(child.status, 1);
  assert.match(child.stderr, /EEXIST/);
  assert.equal(readFileSync(f.expectedOut, "utf8"), "retained sentinel\n");
});

for (const mode of ["capture", "recapture"]) {
  test(`generator ${mode} refuses an existing output directory without changing it`, () => {
    const out = scratch();
    writeFileSync(join(out, "sentinel"), "retained sentinel\n", { flag: "wx" });
    const child = runGenerator([mode, "--compiled-root", compiledRoot, "--out", out]);
    assert.equal(child.status, 1);
    assert.match(child.stderr, /EEXIST/);
    assert.deepEqual(readdirSync(out), ["sentinel"]);
    assert.equal(readFileSync(join(out, "sentinel"), "utf8"), "retained sentinel\n");
  });
}

for (const [name, args, error] of [
  ["unknown mode", ["unknown"], /expected capture, recapture, check, or verify-live/],
  ["duplicate option", ["verify-live", "--input", inputPath, "--input", inputPath], /duplicate option/],
  ["missing option", ["verify-live", "--compiled-root", compiledRoot, "--input", inputPath], /--expected-out/],
]) {
  test(`generator rejects ${name}`, () => {
    const child = runGenerator(args);
    assert.equal(child.status, 1);
    assert.equal(child.stdout, "");
    assert.match(child.stderr, error);
  });
}

// Isolate check fixtures: editing a hash-bound test must not require accepting a stale repository baseline.
// These bindings exercise retained-integrity checking only; they are not fresh oracle captures.
function checkFixture(omitBaseline = false) {
  const dir = scratch();
  const corpus = join(dir, "difftest/testdata/native-demo");
  const bindings = JSON.parse(readFileSync(join(compiledRoot, "bindings.json"), "utf8"));
  const modules = ["difftest/gen/gen-native-demo.mjs", "difftest/gen/native-demo-reference.mjs", "difftest/gen/native-demo-reference.test.mjs"];
  for (const path of [...modules, ...bindings.sourceFiles.map((f) => f.path), "pnpm-lock.yaml"]) {
    const dest = join(dir, path);
    mkdirSync(dirname(dest), { recursive: true });
    writeFileSync(dest, readFileSync(join(root, path)), { flag: "wx" });
  }
  mkdirSync(corpus, { recursive: true });
  const files = ["inputs.jsonl", "frozen-reference.jsonl", "native-contract.jsonl"].map((path) => {
    const data = readFileSync(join(dirname(inputPath), path));
    writeFileSync(join(corpus, path), data, { flag: "wx" });
    return { path, bytes: data.length, sha256: sha(data) };
  });
  const baseline = {
    schemaVersion: "faultline.native-demo-corpus.v1", inputKind: "synthetic-observations-not-execution",
    caseIds: ["ordinary", "equal-wall-time", "backward-wall-time"], bindings,
    modules: modules.map((path) => ({ path, sha256: sha(readFileSync(join(dir, path))) })), files,
  };
  if (!omitBaseline) writeFileSync(join(corpus, "baseline.json"), serialize(baseline), { flag: "wx" });
  return { dir, corpus, script: join(dir, modules[0]) };
}

test("generator check verifies complete retained corpus and current module bindings", () => {
  const f = checkFixture();
  const child = runGenerator(["check"], f.script);
  assert.equal(child.status, 0, child.stderr);
  assert.match(child.stdout, /complete native corpus verified: 3 cases/);
});

test("generator check explicitly rejects absent capture evidence", () => {
  const f = checkFixture(true);
  const child = runGenerator(["check"], f.script);
  assert.equal(child.status, 1);
  assert.match(child.stderr, /absent capture evidence: difftest\/testdata\/native-demo\/baseline.json/);
});

test("generator check rejects a stale hash-bound test module", () => {
  const f = checkFixture();
  writeFileSync(join(f.dir, "difftest/gen/native-demo-reference.test.mjs"), "\n", { flag: "a" });
  const child = runGenerator(["check"], f.script);
  assert.equal(child.status, 1);
  assert.match(child.stderr, /bound source\/module difftest\/gen\/native-demo-reference\.test\.mjs/);
});

test("generator check rejects a one-byte corpus corruption before parsing", () => {
  const f = checkFixture();
  const path = join(f.corpus, "native-contract.jsonl");
  const bytes = readFileSync(path);
  bytes[0] = "[".charCodeAt(0);
  writeFileSync(path, bytes);
  const child = runGenerator(["check"], f.script);
  assert.equal(child.status, 1);
  assert.match(child.stderr, /complete corpus bytes native-contract\.jsonl/);
});

test("full-byte result comparison rejects timestamp and final-newline differences", () => {
  const expected = Buffer.from('{"executedAt":"2026-11-03T00:00:00.000Z"}\n');
  verifyResult({ expected, actualBytes: Buffer.from(expected) });
  assert.throws(() => verifyResult({ expected, actualBytes: Buffer.from('{"executedAt":"2026-11-03T00:00:00.001Z"}\n') }), /complete reference bytes differ at offset 37 \(expected 42, actual 42\)/);
  assert.throws(() => verifyResult({ expected, actualBytes: expected.subarray(0, expected.length - 1) }), /complete reference bytes differ at offset 41 \(expected 42, actual 41\)/);
});
