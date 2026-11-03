// Independent test-only reference. Scripted inputs are not evidence of execution.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, realpathSync } from "node:fs";
import { isAbsolute, join, win32 } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import vm from "node:vm";

const root = fileURLToPath(new URL("../../", import.meta.url));
const frozenCommit = "ce99a793af0b020bf1b9cf21114f8d7897b37207";
const nodeHash = "5c899797c4eb8f1db5563eea56538342ddb3e9276ee1b04a5a1f0f1023d2b011";
const compiledHashes = {
  "engine.js": "7829b3c42f42b3c97b3383e6b976378b989ec354a5617eb706895f5588f88e8d",
  "fixture.js": "1bbd75ea1719e136b23201446b85c2103a23046372c1ebe33f95badc9fd08dc1",
  "canonical.js": "ba79970a81c297f95087d2d5d1be99b5e55adde84ea07a6a706692ee3ee5c91a",
  "witness-result.js": "95f7896120b8f471a05fd3431ce9402f4030ea569f6fa2140224f3426faf1fc7",
};
const clone = (v) => JSON.parse(JSON.stringify(v));
const sha = (v) => createHash("sha256").update(v).digest("hex");
const encoded = (v) => JSON.stringify(v);
const digestPattern = /^sha256:[0-9a-f]{64}$/;
const recordKeys = ["id", "stateId", "witnessDigest", "environmentDigest", "verdict", "reasonCode", "stdout", "stderr", "durationMs", "executionKind", "executedAt"];

function timestamp(v) {
  assert.equal(typeof v, "string", "timestamp must be a string");
  assert.equal(new Date(v).toISOString(), v, "timestamp must be canonical UTC milliseconds");
  return v;
}

function bindingsFor(compiledRoot) {
  assert.ok(isAbsolute(compiledRoot), "absolute compiled root required");
  assert.equal(process.versions.node, "22.22.2", "pinned reference Node required");
  assert.equal(sha(readFileSync(process.execPath)), nodeHash, "pinned Node binary differs");
  const bindings = JSON.parse(readFileSync(join(compiledRoot, "bindings.json"), "utf8"));
  assert.equal(bindings.schemaVersion, "faultline.native-reference-bindings.v1");
  assert.equal(bindings.sourceCommit, frozenCommit);
  assert.equal(bindings.nodeVersion, process.versions.node);
  assert.equal(bindings.nodeSHA256, nodeHash);
  assert.equal(bindings.compilerVersion, "Version 5.9.3");
  assert.equal(sha(readFileSync(join(root, "node_modules/typescript/bin/tsc"))), bindings.compilerEntrypointSHA256);
  assert.equal(sha(readFileSync(join(root, "pnpm-lock.yaml"))), bindings.lockSHA256);
  assert.equal(bindings.sourceFiles.length, 74);
  const paths = new Set();
  for (const f of bindings.sourceFiles) {
    assert.match(f.path, /^src\/[a-zA-Z0-9_./-]+\.ts$/);
    assert.ok(!f.path.split("/").includes("..") && !paths.has(f.path));
    paths.add(f.path);
    assert.equal(sha(readFileSync(join(root, f.path))), f.sha256, `source binding ${f.path}`);
  }
  assert.deepEqual(bindings.compiledFiles.map((f) => f.path).sort(), Object.keys(compiledHashes).sort());
  for (const f of bindings.compiledFiles) {
    assert.equal(f.sha256, compiledHashes[f.path], `frozen compiled binding ${f.path}`);
    assert.equal(sha(readFileSync(join(compiledRoot, f.path))), f.sha256, `compiled bytes ${f.path}`);
  }
  assert.equal(bindings.zod.root, realpathSync(join(root, "node_modules/zod")));
  assert.equal(bindings.zod.version, "3.25.76");
  assert.equal(bindings.zod.files.length, 596);
  const zodPaths = new Set();
  for (const f of bindings.zod.files) {
    assert.ok(!isAbsolute(f.path) && !f.path.split("/").includes("..") && !zodPaths.has(f.path));
    zodPaths.add(f.path);
    assert.equal(sha(readFileSync(join(bindings.zod.root, f.path))), f.sha256, `dependency binding ${f.path}`);
  }
  return bindings;
}

// Importer-specific edges, not a blanket permission for packages or built-ins.
export function referenceImportAllowed(specifier, importer) {
  const edges = {
    "engine.js": ["./canonical.js", "./fixture.js"],
    "fixture.js": ["node:fs", "node:os", "node:path", "node:child_process", "./canonical.js", "./witness-result.js"],
    "canonical.js": ["node:crypto"],
    "witness-result.js": ["zod"],
  };
  assert.ok(edges[importer]?.includes(specifier), `unbound reference import ${importer} -> ${specifier}`);
}

export async function frozenAnalysis({ compiledRoot, calls, generatedAt, witness }) {
  const bindings = bindingsFor(compiledRoot);
  timestamp(generatedAt);
  assert.equal(calls.length, 39, "exact 39-call transcript required");
  const inputs = clone(calls);
  const context = vm.createContext({ process: Object.freeze({ version: process.version }) });
  vm.runInContext(`{
    const PreservedDate = Date;
    globalThis.Date = class extends PreservedDate {
      constructor(...args) { super(...(args.length ? args : [${encoded(generatedAt)}])); }
    };
  }`, context);
  const modules = new Map();
  let fixtureOverride;
  let cursor = 0;
  let selectedWitness;
  let environmentDigest;
  const seen = new Set();
  const blocked = () => { throw new Error("frozen reference executor/file access is blocked"); };
  const zod = await import(pathToFileURL(join(bindings.zod.root, "index.js")).href);
  function synthetic(name, exports) {
    return new vm.SyntheticModule(Object.keys(exports), function () {
      for (const [key, value] of Object.entries(exports)) this.setExport(key, value);
    }, { context, identifier: name });
  }
  function load(name) {
    if (modules.has(name)) return modules.get(name);
    assert.ok(Object.hasOwn(compiledHashes, name), `unbound compiled reference ${name}`);
    const module = new vm.SourceTextModule(readFileSync(join(compiledRoot, name), "utf8"), {
      context, identifier: name,
      importModuleDynamically: blocked,
    });
    modules.set(name, module);
    return module;
  }
  const builtins = new Map([
    ["node:crypto", synthetic("node:crypto", { createHash })],
    ["node:fs", synthetic("node:fs", { mkdirSync: blocked, rmSync: blocked, writeFileSync: blocked })],
    ["node:os", synthetic("node:os", { tmpdir: blocked })],
    ["node:path", synthetic("node:path", { join: blocked })],
    ["node:child_process", synthetic("node:child_process", { spawnSync: blocked })],
    ["zod", synthetic("zod", { z: zod.z })],
  ]);
  async function link(specifier, importer) {
    referenceImportAllowed(specifier, importer.identifier);
    if (importer.identifier === "engine.js" && specifier === "./fixture.js") return fixtureOverride;
    if (specifier.startsWith("./")) return load(specifier.slice(2));
    return builtins.get(specifier);
  }
  const fixture = load("fixture.js");
  await fixture.link(link);
  await fixture.evaluate();
  selectedWitness = clone(witness ?? fixture.namespace.createFrozenWitness());
  assert.match(selectedWitness.digest, digestPattern);
  fixtureOverride = synthetic("engine-fixture-transcript", {
    ...fixture.namespace,
    createFrozenWitness: () => clone(selectedWitness),
    executeFixtureState(state, actualWitness, attempt) {
      const call = inputs[cursor++];
      assert.ok(call, `unexpected reference invocation ${cursor}`);
      assert.equal(encoded(state), encoded(call.state), `state tuple at invocation ${cursor}`);
      assert.equal(attempt, call.executionAttempt, `attempt at invocation ${cursor}`);
      assert.equal(encoded(actualWitness), encoded(selectedWitness), `witness at invocation ${cursor}`);
      const record = call.record;
      assert.deepEqual(Object.keys(record), recordKeys, `complete ordered RunRecord ${cursor}`);
      assert.equal(record.stateId, state.id);
      assert.equal(record.witnessDigest, selectedWitness.digest);
      assert.match(record.environmentDigest, digestPattern);
      environmentDigest ??= record.environmentDigest;
      assert.equal(record.environmentDigest, environmentDigest);
      assert.equal(record.executionKind, "EXECUTED");
      assert.ok(["PASS", "FAIL", "ERROR"].includes(record.verdict));
      for (const key of ["reasonCode", "stdout", "stderr"]) assert.equal(typeof record[key], "string");
      assert.ok(Number.isSafeInteger(record.durationMs) && record.durationMs >= 0);
      timestamp(record.executedAt);
      assert.match(record.id, digestPattern);
      assert.ok(!seen.has(record.id), `duplicate scripted run ID ${record.id}`);
      seen.add(record.id);
      return clone(record);
    },
    replayRun: blocked,
  });
  const engine = load("engine.js");
  await engine.link(link);
  await engine.evaluate();
  const analysis = engine.namespace.createDemoAnalysis("RERUN");
  assert.equal(cursor, inputs.length, "unconsumed scripted invocations");
  return clone(analysis);
}

const copy = [
  [["fixture", "title"], "Parallel settlement-currency regression", "Parallel settlement-currency regression — Go model"],
  [["fixture", "summary"], "A deterministic Node sample with a real executable witness and a two-edit interaction.", "A reviewed Go model evaluated under materialized configuration, with a real native witness and a two-edit interaction."],
  [["minimization", "attempts", 3, "note"], "The partial patch cannot compile and is unresolved, not evidence.", "The partial native configuration cannot parse and is unresolved, not proof."],
  [["minimization", "attempts", 4, "note"], "Applying the two edits to last-good reproduces the frozen witness failure.", "Applying the two edits to last-good reproduces the approved native witness failure."],
  [["minimization", "attempts", 5, "note"], "Removing the two edits from first-bad restores the frozen witness pass.", "Removing the two edits from first-bad restores the approved native witness pass."],
  [["claims", 0, "statement"], "The frozen witness passes before and fails after Session Cedar turn 5.", "The approved native witness passes before and fails after Session Cedar turn 5."],
  [["warning"], "Executed built-in sample. The runner only materializes the reviewed fixture; it is not a general-purpose sandbox.", "Executed reviewed Go model. This does not execute the original JavaScript or external repository code and is not a general-purpose sandbox."],
];

export function nativeCopyContract(frozenAnalysisValue) {
  const value = clone(frozenAnalysisValue);
  assert.equal(value.mode, "RERUN");
  assert.equal(value.fixture.id, "parallel-settlement-regression");
  assert.equal(value.fixture.stateFidelity, "DEMO_SAMPLE");
  assert.equal(value.minimization.attempts.length, 6);
  assert.equal(value.minimization.attempts[3].id, "attempt-partial");
  assert.equal(value.minimization.attempts[4].id, "attempt-candidate");
  assert.equal(value.minimization.attempts[5].id, "attempt-necessity");
  assert.equal(value.claims[0].kind, "EXECUTED");
  for (const [path, before, after] of copy) {
    let parent = value;
    for (const key of path.slice(0, -1)) parent = parent[key];
    const key = path.at(-1);
    assert.equal(parent[key], before, `unreviewed native-copy anchor ${path.join(".")}`);
    parent[key] = after;
  }
  return value;
}

const passOut = 'settlement currency preserved\n{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"}\n';
const failOut = '{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}\n';
const failErr = "expected settlement USD; received EUR\n";
const childArg = "--internal-native-demo-witness-v1";
const childEnvironment = "FAULTLINE_NATIVE_DEMO_CHILD=v1";

function programDescriptor() {
  return {
    schemaVersion: "faultline.native-demo-program.v1", runner: "native-go-config-evaluator-v1",
    configFile: "native-config.json", maxConfigBytes: 16384,
    fields: { displayDefault: ["ORDER_CURRENCY", "EUR"], settlementSource: ["ORDER_CURRENCY", "DISPLAY_CURRENCY"] },
    input: { order: { currency: "USD" }, refund: {} },
    predicate: "result.settlementCurrency === order.currency",
    baseConfig: '{"displayDefault":"ORDER_CURRENCY","settlementSource":"ORDER_CURRENCY"}\n',
    edits: [
      { hunk: "settlement-display-default", from: '"displayDefault":"ORDER_CURRENCY"', to: '"displayDefault":"EUR"' },
      { hunk: "settlement-display-boundary", from: '"settlementSource":"ORDER_CURRENCY"', to: '"settlementSource":"DISPLAY_CURRENCY"' },
      { hunk: "partial-renderer-hunk", from: '"settlementSource":"ORDER_CURRENCY"', to: '"settlementSource":' },
    ],
    stdoutPass: passOut, stdoutFail: failOut, stderrFail: failErr,
    exitCodes: { pass: 0, fail: 1, configError: 3 },
  };
}

function referenceProposal(digestJson) {
  const program = programDescriptor();
  const programDigest = digestJson(program);
  const incidentPacket = {
    symptom: "A completed refund can overwrite settlement currency with display currency.",
    referenceFixture: "parallel-settlement-regression (frozen JavaScript demo)",
    model: "Reviewed Go refund model under native JSON configuration",
    scope: "Built-in DEMO_SAMPLE only; not external repository execution.",
  };
  const witnessTemplate = {
    id: "refund-settlement-currency-go-model", version: 1,
    behavior: "Completed refunds preserve the order settlement currency.",
    command: "fl --internal-native-demo-witness-v1", overlay: ["native-config.json"],
    policy: {
      network: "disabled", credentials: "redacted", runner: "built-in-sample", timeoutSeconds: 2,
      note: `Only the reviewed Go model is executed. Program descriptor ${programDigest}. No network operations or inherited credentials; not an OS sandbox.`,
    },
    proposedBeforeLocalization: true, incidentPacketDigest: digestJson(incidentPacket),
  };
  const body = { schemaVersion: "faultline.native-witness-proposal.v1", program, programDigest, incidentPacket, witnessTemplate };
  return { ...body, digest: digestJson(body) };
}

function configFor(state) {
  const program = programDescriptor();
  assert.ok(Array.isArray(state.hunks));
  assert.equal(new Set(state.hunks).size, state.hunks.length);
  const allowed = program.edits.map((e) => e.hunk);
  for (const hunk of state.hunks) assert.ok(allowed.includes(hunk));
  const partial = state.hunks.includes("partial-renderer-hunk");
  assert.ok(!partial || state.hunks.length === 1);
  let text = program.baseConfig;
  for (const edit of program.edits) {
    if (!state.hunks.includes(edit.hunk)) continue;
    assert.equal(text.split(edit.from).length, 2);
    text = text.replace(edit.from, edit.to);
  }
  return { bytes: Buffer.from(text), partial };
}

function blob(bytes) {
  return { encoding: "base64", data: bytes.toString("base64"), sha256: `sha256:${sha(bytes)}`, complete: true };
}

function rawBlob(value) {
  assert.deepEqual(Object.keys(value), ["encoding", "data", "sha256", "complete"]);
  assert.equal(value.encoding, "base64");
  assert.equal(value.complete, true, "incomplete raw evidence cannot be accepted");
  assert.equal(typeof value.data, "string");
  const bytes = Buffer.from(value.data, "base64");
  assert.equal(bytes.toString("base64"), value.data, "noncanonical base64");
  assert.equal(value.sha256, `sha256:${sha(bytes)}`, "raw blob hash differs");
  return bytes;
}

function fullCallSequence(fixture) {
  const f = fixture.fixtureStates;
  return [
    ...fixture.contributionStates.map((state) => [state, 0]),
    ...fixture.timelineStates.map((state) => [state, 0]),
    ...[[f.turn4, f.turn5], [f.turn6, f.turn7], [f.turn7, f.turn8]]
      .flatMap((pair) => pair.flatMap((state) => [1, 2, 3].map((attempt) => [state, attempt]))),
    ...[f.sourceOnly, f.rendererOnly, f.unresolved, f.candidate, f.reverted, f.lastGood,
      f.lastGood, f.firstBad, f.repaired].map((state) => [state, 0]),
  ];
}

// All paths, binary hashes, times and parse text below are explicitly synthetic test observations.
export async function nativeReferenceInput({ compiledRoot, scenario }) {
  bindingsFor(compiledRoot);
  assert.ok(["ordinary", "equal-wall-time", "backward-wall-time"].includes(scenario));
  const { digestJson } = await import(pathToFileURL(join(compiledRoot, "canonical.js")).href);
  const fixture = await import(pathToFileURL(join(compiledRoot, "fixture.js")).href);
  const proposal = referenceProposal(digestJson);
  const approval = { proposalDigest: proposal.digest, approvedAt: "2026-11-03T00:00:00.000Z" };
  const environmentPreimage = {
    schemaVersion: "faultline.native-demo-environment.v1", runner: "native-go-config-evaluator-v1",
    runtime: "go1.27.3", goos: "darwin", goarch: "arm64", executableSHA256: `sha256:${"a".repeat(64)}`,
    environment: [childEnvironment], network: "disabled", credentials: "not-inherited",
    note: "Reviewed fixed program behavior; not OS isolation or remote attestation.",
  };
  const startedAt = "2026-11-03T00:00:01.000Z";
  const captures = fullCallSequence(fixture).map(([state, executionAttempt], i) => {
    const config = configFor(state);
    const fields = config.partial ? null : JSON.parse(config.bytes.toString("utf8"));
    const fails = fields?.displayDefault === "EUR" && fields?.settlementSource === "DISPLAY_CURRENCY";
    const offset = scenario === "equal-wall-time" ? 0 : scenario === "backward-wall-time" ? -(i + 1) : i + 1;
    const start = Date.parse(startedAt) + offset;
    return {
      state: clone(state), executionAttempt, config: blob(config.bytes), expectedPartial: config.partial,
      executable: "/synthetic/native/fl", argv: ["/synthetic/native/fl", childArg],
      cwd: `/synthetic/native/run-${i + 1}`, environment: [childEnvironment],
      startedAt: new Date(start).toISOString(), executedAt: new Date(start + (scenario === "ordinary" ? 1 : 0)).toISOString(),
      durationMs: i % 3, status: config.partial ? 3 : fails ? 1 : 0, signal: null,
      stdout: blob(Buffer.from(config.partial ? "" : fails ? failOut : passOut)),
      stderr: blob(Buffer.from(config.partial ? "native fixture config: scripted missing assignment value\n" : fails ? failErr : "")),
      incomplete: false, failureKind: null, cleanupError: null,
    };
  });
  return {
    inputKind: "synthetic-observations-not-execution", proposal, approval, environmentPreimage,
    nonce: "12345678-1234-4234-8234-123456789abc", startedAt,
    generatedAt: "2026-11-03T00:01:00.000Z", captures,
  };
}

// compiledRoot and startedAt are explicit test/reference inputs, never production controls.
export async function nativeContract({ compiledRoot, proposal, approval, environmentPreimage, nonce, startedAt, generatedAt, captures }) {
  bindingsFor(compiledRoot);
  const { digestJson } = await import(pathToFileURL(join(compiledRoot, "canonical.js")).href);
  const fixture = await import(pathToFileURL(join(compiledRoot, "fixture.js")).href);
  assert.equal(encoded(proposal), encoded(referenceProposal(digestJson)), "unreviewed proposal/program");
  assert.deepEqual(Object.keys(approval), ["proposalDigest", "approvedAt"]);
  assert.equal(approval.proposalDigest, proposal.digest);
  timestamp(approval.approvedAt);
  timestamp(startedAt);
  timestamp(generatedAt);
  assert.ok(approval.approvedAt <= startedAt, "approval after API entry");
  assert.match(nonce, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  const p = proposal.witnessTemplate;
  const witnessBody = {
    id: p.id, version: p.version, behavior: p.behavior, command: p.command, overlay: p.overlay,
    policy: p.policy, proposedBeforeLocalization: p.proposedBeforeLocalization,
    approvedAt: approval.approvedAt, incidentPacketDigest: p.incidentPacketDigest,
  };
  const witness = { ...witnessBody, digest: digestJson(witnessBody) };
  const env = environmentPreimage;
  assert.deepEqual(Object.keys(env), ["schemaVersion", "runner", "runtime", "goos", "goarch", "executableSHA256", "environment", "network", "credentials", "note"]);
  assert.equal(env.schemaVersion, "faultline.native-demo-environment.v1");
  assert.equal(env.runner, proposal.program.runner);
  assert.match(env.runtime, /^go1\.(?:2[7-9]|[3-9][0-9])\.[0-9]+$/);
  assert.ok(["linux", "darwin", "windows"].includes(env.goos));
  assert.ok(["amd64", "arm64"].includes(env.goarch));
  assert.match(env.executableSHA256, digestPattern);
  assert.equal(env.network, "disabled");
  assert.equal(env.credentials, "not-inherited");
  assert.equal(env.note, "Reviewed fixed program behavior; not OS isolation or remote attestation.");
  assert.equal(env.environment[0], childEnvironment);
  if (env.goos === "windows") {
    assert.equal(env.environment.length, 2);
    assert.match(env.environment[1], /^SYSTEMROOT=.+$/);
  } else assert.deepEqual(env.environment, [childEnvironment]);
  const environmentDigest = digestJson(env);
  const sequence = fullCallSequence(fixture);
  assert.equal(captures.length, sequence.length);
  const receipts = [];
  const calls = [];
  const ids = new Set();
  const executable = captures[0].executable;
  for (const [i, c] of captures.entries()) {
    const [state, attempt] = sequence[i];
    assert.equal(encoded(c.state), encoded(state), `native state at ordinal ${i + 1}`);
    assert.equal(c.executionAttempt, attempt);
    const config = configFor(state);
    assert.ok(rawBlob(c.config).equals(config.bytes), "configuration bytes differ");
    assert.equal(c.expectedPartial, config.partial);
    assert.equal(c.incomplete, false);
    assert.equal(c.failureKind, null);
    assert.equal(c.cleanupError, null);
    assert.ok(isAbsolute(c.executable) || win32.isAbsolute(c.executable));
    assert.equal(c.executable, executable);
    assert.deepEqual(c.argv, [executable, childArg]);
    assert.ok(isAbsolute(c.cwd) || win32.isAbsolute(c.cwd));
    assert.deepEqual(c.environment, env.environment);
    timestamp(c.startedAt);
    timestamp(c.executedAt);
    assert.ok(Number.isSafeInteger(c.durationMs) && c.durationMs >= 0);
    assert.equal(c.signal, null);
    const stdoutBytes = rawBlob(c.stdout);
    const stderrBytes = rawBlob(c.stderr);
    const stdout = stdoutBytes.toString("utf8");
    const stderr = stderrBytes.toString("utf8");
    assert.ok(Buffer.from(stdout).equals(stdoutBytes) && Buffer.from(stderr).equals(stderrBytes), "invalid UTF-8 streams");
    let verdict, reasonCode;
    if (config.partial) {
      assert.equal(c.status, 3);
      assert.equal(stdout, "");
      assert.match(stderr, /^native fixture config: [^\r\n]+\n$/);
      verdict = "ERROR";
      reasonCode = "FIXTURE_PARSE_ERROR";
    } else if (c.status === 0) {
      assert.equal(stdout, passOut);
      assert.equal(stderr, "");
      verdict = "PASS";
      reasonCode = "PREDICATE_PASS";
    } else {
      assert.equal(c.status, 1);
      assert.equal(stdout, failOut);
      assert.equal(stderr, failErr);
      verdict = "FAIL";
      reasonCode = "PREDICATE_FAIL";
    }
    const record = {
      stateId: state.id, witnessDigest: witness.digest, environmentDigest, verdict, reasonCode,
      stdout, stderr, durationMs: c.durationMs, executionKind: "EXECUTED", executedAt: c.executedAt,
    };
    const preimage = {
      schemaVersion: "faultline.native-demo-run-id.v1", nonce, ordinal: i + 1,
      executionAttempt: attempt, startedAt: c.startedAt, configDigest: c.config.sha256,
      status: c.status, signal: c.signal, record,
    };
    const id = digestJson(preimage);
    assert.ok(!ids.has(id), "duplicate native ID");
    ids.add(id);
    const fullRecord = { id, ...record };
    receipts.push({
      ordinal: i + 1, stateId: state.id, executionAttempt: attempt, config: clone(c.config),
      expectedPartial: config.partial, executable: c.executable, argv: clone(c.argv), cwd: c.cwd,
      environment: clone(c.environment), startedAt: c.startedAt, executedAt: c.executedAt,
      durationMs: c.durationMs, status: c.status, signal: c.signal, stdout: clone(c.stdout), stderr: clone(c.stderr),
      accepted: true, runId: id, runRecord: fullRecord, runIdPreimage: preimage,
      failureKind: null, cleanupError: null,
    });
    calls.push({ state: clone(state), executionAttempt: attempt, record: fullRecord });
  }
  // Native proof conditions are stricter than the historical engine's unconditional prose.
  assert.deepEqual(calls.slice(0, 12).map((c) => c.record.verdict), ["PASS", "PASS", "FAIL", "FAIL", "PASS", "PASS", "PASS", "PASS", "FAIL", "FAIL", "PASS", "FAIL"]);
  assert.deepEqual(calls.slice(12, 30).map((c) => c.record.verdict), ["PASS", "PASS", "PASS", "FAIL", "FAIL", "FAIL", "FAIL", "FAIL", "FAIL", "PASS", "PASS", "PASS", "PASS", "PASS", "PASS", "FAIL", "FAIL", "FAIL"]);
  assert.deepEqual(calls.slice(30).map((c) => c.record.verdict), ["PASS", "PASS", "ERROR", "FAIL", "PASS", "PASS", "PASS", "FAIL", "PASS"]);
  const analysis = nativeCopyContract(await frozenAnalysis({ compiledRoot, calls, generatedAt, witness }));
  assert.equal(analysis.grade.value, "A");
  assert.equal(analysis.transitions.length, 3);
  assert.ok(analysis.transitions.every((t) => t.stable));
  return {
    analysis,
    provenance: {
      schemaVersion: "faultline.native-demo-execution.v1", status: "COMPLETE", proposal: clone(proposal),
      program: clone(proposal.program), programDigest: proposal.programDigest, approval: clone(approval),
      environmentPreimage: clone(env), environmentDigest, nonce, generatedAt, invocations: receipts,
    },
  };
}

export function verifyResult({ expected, actualBytes }) {
  assert.ok(Buffer.isBuffer(expected) && Buffer.isBuffer(actualBytes), "complete byte buffers required");
  if (expected.equals(actualBytes)) return;
  let offset = 0;
  while (offset < Math.min(expected.length, actualBytes.length) && expected[offset] === actualBytes[offset]) offset++;
  assert.fail(`complete reference bytes differ at offset ${offset} (expected ${expected.length}, actual ${actualBytes.length})`);
}
