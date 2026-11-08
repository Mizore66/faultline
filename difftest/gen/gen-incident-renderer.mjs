// Test-only frozen-TS evidence generator for the Go Slice 5b.2 interactive incident renderer.
//
//   node difftest/gen/gen-incident-renderer.mjs --check
//       Read-only validation of the committed corpus and every pin. Imports no TS/dist module, spawns
//       nothing, writes nothing, needs no scratch directory.
//   node difftest/gen/gen-incident-renderer.mjs --scratch ABSOLUTE_FRESH_DIRECTORY
//       Initial capture. Real frozen dist/engine.js + dist/ui.js calls. Evidence files are created
//       exclusively (flag "wx") and only if none of them exists.
//   node difftest/gen/gen-incident-renderer.mjs --recapture --scratch ABSOLUTE_FRESH_DIRECTORY
//       Fresh real calls compared byte-for-byte with the committed evidence. Overwrites nothing.
//
// The scratch directory must be absent or empty; it is never deleted, masked or reused. Receipts of the
// preflight and the candidate corpus bytes are preserved there.
// No RERUN is ever executed: the retained RERUN analysis is parsed as historical JSON input only.
// No entropy/clock/seed override is installed; only the pure renderer is invoked.
// Every non-ASCII or invisible test character below is written as a \u escape so nothing hides in source.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  existsSync, lstatSync, mkdirSync, readdirSync, readFileSync, realpathSync, statSync, writeFileSync,
} from "node:fs";
import { dirname, isAbsolute, join, posix, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

// ---------------------------------------------------------------------------------------------
// Pins
// ---------------------------------------------------------------------------------------------
export const frozenCommit = "ce99a793af0b020bf1b9cf21114f8d7897b37207";
export const frozenManifestVersion = "0.1.3";
export const currentManifestVersion = "0.1.4";
export const oracleNode = "22.22.2";
export const oracleLocale = "en-US";
const baselineSchema = "faultline.incident-renderer-oracle.v1";

export const caseIDs = [
  "replay", "retained-rerun", "no-regression", "unstable-regression", "first-stable-regression",
  "empty-collections", "contribution-truncation", "unknown-state-labels", "checkpoint-names",
  "timeline-names", "caption-names", "verdicts-and-reasons", "change-names", "attempt-notes",
  "claim-kinds-and-statements", "grade-a", "grade-b", "grade-c", "escaping", "unicode",
  "lone-surrogates", "js-whitespace-and-case", "large-integer-metrics", "numeric-durations",
];

const generatorFile = fileURLToPath(import.meta.url);
const repo = resolve(dirname(generatorFile), "..", "..");
const rel = {
  generator: "difftest/gen/gen-incident-renderer.mjs",
  retained: "difftest/testdata/bases/demo-rerun/analysis.json",
  golden: "difftest/testdata/incident-renderer/golden.jsonl",
  html: "difftest/testdata/incident-renderer/incident.html",
  baseline: "difftest/testdata/incident-renderer/baseline.json",
  interactiveEnabled: "difftest/testdata/incident-renderer/interactive-enabled.html",
  interactiveDisabled: "difftest/testdata/incident-renderer/interactive-disabled.html",
  interactiveBaseline: "difftest/testdata/incident-renderer/interactive-baseline.json",
  evidenceDir: "difftest/testdata/incident-renderer",
  src: "src",
  distRoots: ["dist/ui.js", "dist/engine.js"],
};
const abs = (r) => join(repo, ...r.split("/"));
const gitTimeoutMs = 20000;

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const gitBlobSha1 = (bytes) =>
  createHash("sha1").update(Buffer.concat([Buffer.from(`blob ${bytes.length}\0`), bytes])).digest("hex");
const serialize = (value) => JSON.stringify(value);
const clone = (value) => JSON.parse(JSON.stringify(value));
const bytesCompare = (a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b));
const hex = (n) => new RegExp(`^[0-9a-f]{${n}}$`);

// ---------------------------------------------------------------------------------------------
// Row capture and valid-DemoAnalysis value-type guard
// ---------------------------------------------------------------------------------------------
export const captureRow = (id, analysis, render) => {
  const analysisJSON = serialize(analysis);
  const htmlJSON = serialize(render(analysis));
  const afterJSON = serialize(analysis);
  assert.equal(afterJSON, analysisJSON, `${id}: renderer mutated input`);
  return { id, analysisJSON, htmlJSON, afterJSON };
};

const verdicts = ["PASS", "FAIL", "UNSTABLE", "ERROR", "INAPPLICABLE"];
const evidenceKinds = ["EXECUTED", "DERIVED", "INFERRED", "UNKNOWN"];
function assertAnalysisShape(a, id) {
  const isStr = (v, p) => assert.equal(typeof v, "string", `${id}: ${p} must be a string`);
  const isInt = (v, p, min) => assert.ok(Number.isInteger(v) && v >= min, `${id}: ${p} must be an integer >= ${min}`);
  const isArr = (v, p) => assert.ok(Array.isArray(v), `${id}: ${p} must be an array`);
  const run = (r, p) => {
    isStr(r.stateId, `${p}.stateId`);
    isStr(r.reasonCode, `${p}.reasonCode`);
    assert.ok(verdicts.includes(r.verdict), `${id}: ${p}.verdict`);
    assert.ok(["EXECUTED", "CACHED"].includes(r.executionKind), `${id}: ${p}.executionKind`);
    assert.ok(typeof r.durationMs === "number" && Number.isFinite(r.durationMs) && r.durationMs >= 0, `${id}: ${p}.durationMs`);
  };
  assert.equal(a.schemaVersion, "faultline.demo.v1", `${id}: schemaVersion`);
  assert.ok(["REPLAY", "RERUN"].includes(a.mode), `${id}: mode`);
  isStr(a.fixture.title, "fixture.title");
  for (const k of ["sessions", "turns", "files", "changedLines", "implicatedHunks"]) isInt(a.metrics[k], `metrics.${k}`, 1);
  for (const k of ["contributionRuns", "timelineRuns", "transitions", "runCatalog", "claims"]) isArr(a[k], k);
  a.contributionRuns.forEach((r, i) => run(r, `contributionRuns[${i}]`));
  a.timelineRuns.forEach((r, i) => run(r, `timelineRuns[${i}]`));
  a.transitions.forEach((t, i) => {
    assert.ok(["PASS_TO_FAIL", "FAIL_TO_PASS"].includes(t.kind), `${id}: transitions[${i}].kind`);
    assert.equal(typeof t.stable, "boolean", `${id}: transitions[${i}].stable`);
  });
  isInt(a.minimization.budget.used, "budget.used", 0);
  isInt(a.minimization.budget.max, "budget.max", 1);
  isArr(a.minimization.attempts, "attempts");
  a.minimization.attempts.forEach((t, i) => {
    isArr(t.subset, `attempts[${i}].subset`);
    t.subset.forEach((s, j) => isStr(s, `attempts[${i}].subset[${j}]`));
    assert.ok(["PASS", "FAIL", "UNRESOLVED"].includes(t.outcome), `${id}: attempts[${i}].outcome`);
    isStr(t.note, `attempts[${i}].note`);
  });
  isArr(a.minimization.candidate, "candidate");
  a.minimization.candidate.forEach((s, j) => isStr(s, `candidate[${j}]`));
  run(a.minimization.sufficiency, "sufficiency");
  run(a.minimization.necessity, "necessity");
  run(a.prevention.lastGood, "prevention.lastGood");
  run(a.prevention.firstBad, "prevention.firstBad");
  run(a.prevention.repaired, "prevention.repaired");
  assert.ok(["A", "B", "C", "D"].includes(a.grade.value), `${id}: grade.value`);
  a.claims.forEach((c, i) => {
    assert.ok(evidenceKinds.includes(c.kind), `${id}: claims[${i}].kind`);
    isStr(c.statement, `claims[${i}].statement`);
  });
}

// ---------------------------------------------------------------------------------------------
// The exact ordered 24-case request matrix. Every case starts from a fresh JSON clone of either the
// real REPLAY analysis or the retained RERUN JSON and is mutated independently.
// ---------------------------------------------------------------------------------------------
export function buildAnalyses(replay, rerun) {
  const runTemplate = replay.timelineRuns[0];
  const mkRun = (stateId, o = {}) => ({ ...clone(runTemplate), stateId, ...o });
  const tr = (kind, stable, o = {}) => ({
    beforeStateId: "cedar-turn-4",
    afterStateId: "cedar-turn-5",
    beforeVerdict: kind === "PASS_TO_FAIL" ? "PASS" : "FAIL",
    afterVerdict: kind === "PASS_TO_FAIL" ? "FAIL" : "PASS",
    stable,
    kind,
    boundaryRunIds: [],
    ...o,
  });
  const attempt = (id, subset, outcome, note, runId) =>
    runId === undefined ? { id, subset, outcome, note } : { id, subset, outcome, runId, note };
  const claimOf = (kind, statement) => ({ kind, statement, evidenceIds: ["evidence-1"] });
  const kindCycle = (i) => evidenceKinds[i % evidenceKinds.length];
  const turnIDs = [1, 2, 3, 4, 5, 6, 7, 8].map((n) => `cedar-turn-${n}`);
  const H = `<>&"'`;
  // JS whitespace: space, NBSP, BOM, LS, PS, IDEOGRAPHIC SPACE, EM SPACE, tab, LF, VT, FF.
  const WS = " \u00a0\ufeff\u2028\u2029\u3000\u2003\t\n\v\f";
  const NBSP = "\u00a0";
  const BOM = "\ufeff";
  const IDEO = "\u3000";
  const NEL = "\u0085"; // not JS whitespace
  const MVS = "\u180e";
  const LONG_S = "\u017f";
  const DOTLESS_I = "\u0131";
  const DOTTED_I = "\u0130";
  const KELVIN = "\u212a";
  const EMOJI = "\u{1f680}";
  const ASTRAL = "\u{1d4b3}";
  const LINEAR_B = "\u{10000}";
  const EMDASH = "\u2014";
  const ENDASH = "\u2013";
  const ARABIC_3 = "\u0663";
  const FW_TURN = "\uff34\uff35\uff32\uff2e";
  const FW_12 = "\uff11\uff12";
  const COMBINING_ACUTE = "\u0301";
  const RLO = "\u202e";

  const cases = [];
  const add = (id, base, mutate) => {
    const analysis = clone(base);
    mutate(analysis);
    cases.push([id, analysis]);
  };

  add("replay", replay, () => {});
  add("retained-rerun", rerun, () => {});
  add("no-regression", replay, (a) => { a.transitions = []; });
  add("unstable-regression", replay, (a) => {
    a.transitions = [tr("PASS_TO_FAIL", false), tr("FAIL_TO_PASS", true), tr("FAIL_TO_PASS", false)];
  });
  add("first-stable-regression", replay, (a) => {
    a.transitions = [
      tr("FAIL_TO_PASS", true), tr("PASS_TO_FAIL", false),
      tr("PASS_TO_FAIL", true, { beforeStateId: "first-stable" }),
      tr("PASS_TO_FAIL", true, { beforeStateId: "second-stable" }),
    ];
  });
  add("empty-collections", replay, (a) => {
    a.contributionRuns = []; a.timelineRuns = []; a.transitions = []; a.runCatalog = [];
    a.minimization.attempts = []; a.minimization.candidate = []; a.claims = []; a.grade.reasons = [];
  });
  add("contribution-truncation", replay, (a) => {
    const ids = ["contribution-atlas", "contribution-birch", "contribution-cedar", "contribution-delta",
      "truncated-fourth-sentinel", "truncated-fifth-sentinel"];
    a.contributionRuns = ids.map((s, i) => mkRun(s, { verdict: i % 2 ? "FAIL" : "PASS", reasonCode: i % 2 ? "PREDICATE_FAIL" : "PREDICATE_PASS" }));
  });
  add("unknown-state-labels", replay, (a) => {
    a.contributionRuns = ["x-y contribution boundary", "", "ZZ contribution\nboundary"].map((s) => mkRun(s));
    a.timelineRuns = ["unlisted-state", "", "   ", "Step 3", "TURN 12 extra", "turn\t7"].map((s) => mkRun(s));
    a.prevention.lastGood.stateId = "no-such-last";
    a.prevention.firstBad.stateId = "no-such-first";
  });
  add("checkpoint-names", replay, (a) => {
    a.contributionRuns = ["\u03a9mega contribution boundary", "Beta_2  CONTRIBUTION extra", "Contribution boundary"]
      .map((s) => mkRun(s));
  });
  add("timeline-names", replay, (a) => {
    a.timelineRuns = [
      `Turn 4 ${EMDASH} last good`, "Turn 5 FIRST BAD", "turn 7 repaired", "Turn 8 Reintroduced", "Turn 9",
      "turn  010", `Turn ${ARABIC_3}`, "Turn5", "last good and first bad", "first bad repaired",
      "repaired reintroduced", `Turn 3 la${LONG_S}t good`, `Turn 3 f${DOTLESS_I}rst bad`, `Turn 8 re${DOTTED_I}ntroduced`,
      "Turn 3 repair", "LAST GOOD",
    ].map((s) => mkRun(s));
  });
  add("caption-names", replay, (a) => {
    a.prevention.lastGood.stateId = "Repair worktree";
    a.prevention.firstBad.stateId = "Turn 12 reintroduced";
  });
  add("verdicts-and-reasons", replay, (a) => {
    const reasons = ["WITNESS_SATISFIED", "PREDICATE_PASS", "WITNESS_ASSERTION_FAILED", "PREDICATE_FAIL",
      "FIXTURE_COMPILE_ERROR", "", "predicate_pass", "RUNNER_ERROR"];
    a.timelineRuns = turnIDs.map((s, i) => mkRun(s, {
      verdict: verdicts[i % verdicts.length], reasonCode: reasons[i],
      executionKind: i % 2 ? "EXECUTED" : "CACHED", durationMs: i * 3,
    }));
    a.contributionRuns = ["contribution-atlas", "contribution-birch", "contribution-cedar"].map((s, i) =>
      mkRun(s, { verdict: ["INAPPLICABLE", "UNSTABLE", "ERROR"][i], reasonCode: reasons[i + 4] }));
    a.prevention.lastGood.verdict = "UNSTABLE";
    a.prevention.firstBad.verdict = "INAPPLICABLE";
    a.prevention.repaired.verdict = "ERROR";
    a.minimization.sufficiency.verdict = "ERROR";
    a.minimization.necessity.verdict = "PASS";
  });
  add("change-names", replay, (a) => {
    a.minimization.attempts = [
      attempt("a1", ["settlement-display-default", "settlement-display-boundary"], "PASS", "plain note", "run-a1"),
      attempt("a2", ["partial-renderer-hunk"], "UNRESOLVED", "plain note"),
      attempt("a3", [], "FAIL", "plain note"),
      attempt("a4", ["prefix-settlement-display-default-suffix"], "PASS", "plain note"),
      attempt("a5", ["settlement-display-default-partial"], "FAIL", "plain note"),
      attempt("a6", ["xx partial yy", "Partial", "SETTLEMENT-DISPLAY-DEFAULT"], "UNRESOLVED", "plain note"),
      attempt("a7", ["settlement-display-boundary", "partial", "settlement-display-default"], "PASS", "plain note"),
      attempt("a8", ["settlement-display-boundar", "partia", H], "FAIL", "plain note"),
    ];
    a.minimization.candidate = ["settlement-display-default", "settlement-display-boundary", "unmapped <x>", "partial-renderer-hunk"];
  });
  add("attempt-notes", replay, (a) => {
    const notes = [
      "No implicated edits", "NO IMPLICATED EDITS here", "display default alone", "Display Default Alone passes",
      "boundary assignment alone", "partial patch", "UNRESOLVED hunk",
      "reproduces the frozen witness failure", "restores the frozen witness pass",
      "No implicated edits; partial patch", "display default alone and boundary assignment alone",
      "partial patch reproduces the frozen witness failure", "unresolved restores the frozen witness pass",
      "Reproduces the frozen witness failure", "unmapped note", "", "No implicated edits",
    ];
    const outcomes = ["PASS", "FAIL", "UNRESOLVED"];
    a.minimization.attempts = notes.map((n, i) =>
      attempt(`n${i}`, i % 3 === 0 ? [] : ["settlement-display-default"], outcomes[i % 3], n, i % 4 === 0 ? `run-${i}` : undefined));
  });
  add("claim-kinds-and-statements", replay, (a) => {
    const statements = [
      "The test passes before and fails after.", "PASSES BEFORE AND FAILS AFTER", "first stable pass-to-fail",
      "The two-edit candidate is sufficient", "Settlement and display-currency mix", "Whether an agent intended it",
      "Cached replay does not establish a transition", "No stable boundary is certified",
      "Cached counterfactual results are not a proof",
      "passes before and fails after; first stable pass-to-fail", "Cached replay does not establish; Settlement and display-currency",
      "plain statement", "", `first stable pass${ENDASH}to${ENDASH}fail`,
    ];
    a.claims = statements.map((s, i) => claimOf(kindCycle(i), s));
  });
  for (const g of ["A", "B", "C"]) {
    add(`grade-${g.toLowerCase()}`, replay, (a) => { a.grade.value = g; });
  }
  add("escaping", replay, (a) => {
    a.fixture.title = `title ${H}`;
    a.contributionRuns = [`<unknown>&"'`, H, "&amp; &lt;"].map((s) => mkRun(s));
    a.timelineRuns = [`'-- </script><script>alert(1)</script>`, "&#39;&quot;"].map((s) => mkRun(s));
    a.prevention.lastGood.stateId = H;
    a.prevention.firstBad.stateId = "<img src=x onerror=alert(1)>";
    a.minimization.attempts = [attempt("e1", [H, "&amp;", "<b>"], "PASS", H)];
    a.minimization.candidate = [H, `"quoted"`];
    a.claims = [claimOf("EXECUTED", "<script>not markup</script>"), claimOf("UNKNOWN", H)];
  });
  add("unicode", replay, (a) => {
    a.fixture.title = `\u00dcn\u00efc\u00f6d\u00e9 ${EMDASH} \u6d4b\u8bd5 ${EMOJI} e${COMBINING_ACUTE} ${RLO} rtl \u0000 nul`;
    a.contributionRuns = [`${ASTRAL} contribution boundary`, "\u00dcn\u00ef contribution", "A\u0000B contribution boundary"]
      .map((s) => mkRun(s));
    a.timelineRuns = [`Turn 5 ${EMOJI}`, `${FW_TURN} 5`, `Turn ${FW_12}`, `Turn 7${COMBINING_ACUTE}`, `${EMOJI}\u0000`]
      .map((s) => mkRun(s));
    a.prevention.lastGood.stateId = `\u6700\u540e\u826f\u597d ${EMOJI}`;
    a.prevention.firstBad.stateId = "\u00e9\u0000";
    a.minimization.attempts = [attempt("u1", ["\u65e5\u672c\u8a9e", `${EMOJI} emoji`], "PASS", `${EMOJI} NUL\u0000 done`)];
    a.minimization.candidate = ["\u65e5\u672c\u8a9e", EMOJI];
    a.claims = [claimOf("DERIVED", `passes before and fails after ${EMOJI}`), claimOf("INFERRED", "\u6d4b\u8bd5\u0000")];
  });
  add("lone-surrogates", replay, (a) => {
    a.fixture.title = "lone \ud800 high, \udc00 low, \udc00\ud800 reversed, end\ud83d";
    a.contributionRuns = ["\ud800 contribution boundary", "\udc00\ud800 contribution boundary", `ok${EMOJI} valid pair`]
      .map((s) => mkRun(s));
    a.timelineRuns = ["Turn 5 \ud83d", "\udfff", "Turn \ud800 6"].map((s) => mkRun(s));
    a.prevention.lastGood.stateId = "\ud800";
    a.prevention.firstBad.stateId = "last good \udfff";
    a.minimization.attempts = [attempt("s1", ["\ud800", "\udc00x", "x\ud83d"], "FAIL", `note ${LINEAR_B}\ud800`)];
    a.minimization.candidate = ["\ud800", "\ude00\ud83d"];
    a.claims = [claimOf("EXECUTED", "lone \ud800"), claimOf("UNKNOWN", "\udc00")];
  });
  add("js-whitespace-and-case", replay, (a) => {
    a.fixture.title = `${WS}title${WS}`;
    a.contributionRuns = [
      `Gamma${BOM}contribution boundary`, `Delta${NEL}contribution boundary`, `Zeta${WS}contribution`,
    ].map((s) => mkRun(s));
    a.timelineRuns = [
      `Turn${BOM}4 la${LONG_S}t good`, `Turn${BOM}4`, `Turn${NEL} 4`, `TURN${MVS}5`, "Turn\v5", "Turn\f5",
      `Turn${NBSP}5`, "Turn\u20285", `Turn${IDEO}5`, `Turn${KELVIN} 5`, "Last good", "LAST GOOD",
      "FIRST BAD", "RePaIrEd", "Reintroduced", `la${LONG_S}t good`, `f${DOTLESS_I}rst bad`,
      `${KELVIN}elvin repaired`, `${WS}Turn 6${WS}`,
    ].map((s) => mkRun(s));
    a.prevention.lastGood.stateId = "Last good";
    a.prevention.firstBad.stateId = "FIRST BAD";
    a.minimization.attempts = [
      "No implicated edits", `No${WS}implicated edits`, "PARTIAL PATCH", `unre${LONG_S}olved`, "BOUNDARY ASSIGNMENT ALONE",
    ].map((n, i) => attempt(`w${i}`, ["partial ", "SETTLEMENT-DISPLAY-DEFAULT"], ["PASS", "FAIL", "UNRESOLVED"][i % 3], n));
    a.claims = [
      "FIRST STABLE PASS-TO-FAIL", "PASSES BEFORE AND FAILS AFTER", "Passes before and fails after",
      `pa${LONG_S}${LONG_S}es before and fails after`, `${WS}first stable pass-to-fail${WS}`,
    ].map((s, i) => claimOf(kindCycle(i), s));
  });
  add("large-integer-metrics", replay, (a) => {
    a.metrics = {
      ...a.metrics, sessions: 1e21, turns: 2 ** 53, files: 123456789012345680000, changedLines: 2 ** 63,
      implicatedHunks: 1e21,
    };
    a.minimization.budget = { used: 123456789012345680000, max: 1e21 };
  });
  add("numeric-durations", replay, (a) => {
    const durations = [0, 1, 0.5, 12.34, 1e-7, 1e21, 123456789012345680000, 5e-324, 1234567.891, 0.1 + 0.2, 100,
      0.000001, 2 ** 53, Number.MAX_VALUE];
    a.timelineRuns = [
      ...durations.map((d, i) => mkRun(turnIDs[i % 8], { executionKind: "EXECUTED", durationMs: d })),
      mkRun("cedar-turn-1", { executionKind: "CACHED", durationMs: 7 }),
      mkRun("cedar-turn-2", { executionKind: "CACHED", durationMs: 1e21 }),
    ];
    a.contributionRuns = ["contribution-atlas", "contribution-birch", "contribution-cedar"].map((s, i) =>
      mkRun(s, { executionKind: "EXECUTED", durationMs: [1234.5, 1e21, 0.000001][i] }));
  });

  assert.deepEqual(cases.map(([id]) => id), caseIDs, "case matrix differs from the spec's ordered list");
  cases.forEach(([id, analysis]) => assertAnalysisShape(analysis, id));
  return cases;
}

// ---------------------------------------------------------------------------------------------
// Measurement of the pinned inputs (read-only)
// ---------------------------------------------------------------------------------------------
function regularFile(path, label) {
  const st = lstatSync(path);
  assert.ok(st.isFile() && !st.isSymbolicLink(), `${label}: not a regular file`);
  return readFileSync(path);
}

function walkFiles(dir, prefix) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const r = `${prefix}/${name}`;
    const st = lstatSync(p);
    if (st.isDirectory() && !st.isSymbolicLink()) out.push(...walkFiles(p, r));
    else {
      assert.ok(st.isFile() && !st.isSymbolicLink(), `${r}: not a regular file`);
      out.push(r);
    }
  }
  return out;
}

function measureSource() {
  const paths = walkFiles(abs(rel.src), rel.src).sort(bytesCompare);
  assert.ok(paths.length > 0, "src/ is empty");
  const files = paths.map((path) => {
    const bytes = regularFile(abs(path), path);
    return { path, bytes: bytes.length, gitBlob: gitBlobSha1(bytes), sha256: sha256(bytes) };
  });
  const treeSha256 = sha256(files.map((f) => `${f.gitBlob} ${f.sha256} ${f.path}\n`).join(""));
  return { treeSha256, files };
}

// Frozen compiled-graph assumption (tsc ES-module output, 4-space indent, one declaration per statement):
// every static module declaration starts at column 0 of a line, so quoted program text (indented or
// preceded by a quote), `//` comments and indented template content cannot match. Not a JS parser:
// a column-0 `import ...` line inside a block comment or multi-line template would still match.
// Dynamic import("./x") is not followed; a literal relative one fails closed instead of being skipped.
const staticImportPattern =
  /^(?:import|export)[ \t]+(?:[\w$\s,*]|\{[^{}"'`;]*\})+?[ \t\r\n]*from[ \t]*(["'])([^"'\r\n]+)\1|^import[ \t]*(["'])([^"'\r\n]+)\3/gm;
const dynamicImportPattern = /(?<![\w$."'`])import\(\s*["']\./;
export function discoverImports(source) {
  assert.ok(!dynamicImportPattern.test(source), "dynamic relative import() is outside the frozen graph rule");
  const specs = [];
  for (const m of source.matchAll(staticImportPattern)) specs.push(m[2] ?? m[4]);
  return specs;
}
function measureDist() {
  const seen = new Map();
  const queue = [...rel.distRoots];
  while (queue.length > 0) {
    const path = queue.shift();
    if (seen.has(path)) continue;
    assert.ok(path.startsWith("dist/") && path.endsWith(".js"), `unexpected module path ${path}`);
    const bytes = regularFile(abs(path), `${path} (run a fresh build first)`);
    seen.set(path, bytes);
    for (const spec of discoverImports(bytes.toString("utf8"))) {
      if (!spec.startsWith(".")) continue; // node: builtins and bare packages are not pinned here
      queue.push(posix.normalize(posix.join(posix.dirname(path), spec)));
    }
  }
  const files = [...seen.keys()].sort(bytesCompare).map((path) => ({
    path, bytes: seen.get(path).length, sha256: sha256(seen.get(path)),
  }));
  return { roots: [...rel.distRoots], files };
}

const fileDescriptor = (path, bytes) => ({ path, bytes: bytes.length, sha256: sha256(bytes) });

function measure() {
  const retained = regularFile(abs(rel.retained), rel.retained);
  const generator = regularFile(generatorFile, rel.generator);
  return {
    source: measureSource(),
    dist: measureDist(),
    retained: fileDescriptor(rel.retained, retained),
    retainedBytes: retained,
    generator: fileDescriptor(rel.generator, generator),
  };
}
const withoutBytes = (m) => ({ source: m.source, dist: m.dist, retained: m.retained, generator: m.generator });

function buildBaseline(measured, runtime, attest, goldenBytes, htmlBytes) {
  return {
    schema: baselineSchema,
    frozenSource: {
      commit: frozenCommit,
      manifestVersion: frozenManifestVersion,
      manifestGitBlob: attest.manifestGitBlob,
      treeSha256: measured.source.treeSha256,
      files: measured.source.files,
    },
    currentManifestVersion,
    runtime,
    retainedInput: measured.retained,
    generator: measured.generator,
    compiledModules: measured.dist,
    cases: [...caseIDs],
    corpus: {
      golden: { ...fileDescriptor(rel.golden, goldenBytes), rows: caseIDs.length },
      html: fileDescriptor(rel.html, htmlBytes),
    },
  };
}
const baselineText = (baseline) => `${JSON.stringify(baseline, null, 2)}\n`;

function currentRuntime() {
  return {
    node: process.versions.node,
    version: process.version,
    locale: new Intl.NumberFormat().resolvedOptions().locale,
    groupingSample: (1234567).toLocaleString(),
    v8: process.versions.v8,
    icu: process.versions.icu ?? null,
    unicode: process.versions.unicode ?? null,
    cldr: process.versions.cldr ?? null,
  };
}
function assertOracleRuntime(runtime, label) {
  assert.deepEqual(Object.keys(runtime), ["node", "version", "locale", "groupingSample", "v8", "icu", "unicode", "cldr"],
    `${label}: runtime keys`);
  assert.equal(runtime.node, oracleNode, `${label}: Node version drift`);
  assert.equal(runtime.version, `v${oracleNode}`, `${label}: process.version drift`);
  assert.equal(runtime.locale, oracleLocale, `${label}: Intl default locale is not ${oracleLocale}`);
  assert.equal(runtime.groupingSample, "1,234,567", `${label}: number grouping drift`);
  assert.equal(typeof runtime.v8, "string", `${label}: v8`);
  for (const k of ["icu", "unicode", "cldr"]) assert.ok(runtime[k] === null || typeof runtime[k] === "string", `${label}: ${k}`);
}


// ---------------------------------------------------------------------------------------------
// Interactive pages: pure literal derivation from the frozen incident.html (never from Go output).
// ---------------------------------------------------------------------------------------------
const interactiveSchema = "faultline.native-interactive-renderer.v1";
export const interactiveCredential = "test_nonce-ABC123";
const interactiveDisabledStatus = "native RERUN requires startup --approve-native";

function replaceExactly(text, from, to, count, label) {
  const parts = text.split(from);
  assert.equal(parts.length - 1, count, `interactive: ${label} literal count`);
  return parts.join(to);
}

export function deriveInteractive(html, credential = interactiveCredential) {
  const scriptOpen = "<script>";
  const nonceOpen = `<script nonce="${credential}">`;
  const fetchFrom = 'fetch("/api/rerun",{method:"POST"})';
  const fetchTo = `fetch("/api/rerun",{method:"POST",headers:{"X-FaultLine-Native-RERUN":${JSON.stringify(credential)}}})`;
  const enabled = replaceExactly(
    replaceExactly(html, scriptOpen, nonceOpen, 1, "script open"), fetchFrom, fetchTo, 1, "rerun fetch");

  const buttonFrom = '<button id="rerun" type="button">';
  const statusFrom = '<span class="action-status" id="action-status">uses the safe built-in example</span>';
  const listenerStart = 'const rerun=document.getElementById("rerun");';
  const listenerEnd = "rerun.disabled=false}});\n";
  let disabled = replaceExactly(html, scriptOpen, nonceOpen, 1, "script open");
  disabled = replaceExactly(disabled, buttonFrom, '<button id="rerun" type="button" disabled>', 1, "button");
  disabled = replaceExactly(disabled, statusFrom,
    `<span class="action-status" id="action-status">${interactiveDisabledStatus}</span>`, 1, "status");
  const start = disabled.indexOf(listenerStart);
  assert.ok(start >= 0 && disabled.indexOf(listenerStart, start + 1) < 0, "interactive: listener start literal count");
  const end = disabled.indexOf(listenerEnd, start);
  assert.ok(end > start, "interactive: listener end literal");
  assert.ok(disabled.slice(start, end).split("\n").length === 1, "interactive: listener spans one line");
  disabled = disabled.slice(0, start) + disabled.slice(end + listenerEnd.length);
  assert.ok(!disabled.includes("/api/rerun"), "interactive: disabled page still references /api/rerun");
  assert.ok(!disabled.includes("addEventListener(\"click\",async"), "interactive: disabled page keeps rerun listener");
  return { enabled, disabled };
}

function buildInteractiveBaseline(htmlBytes, enabledBytes, disabledBytes) {
  return {
    schema: interactiveSchema,
    source: fileDescriptor(rel.html, htmlBytes),
    credential: interactiveCredential,
    pages: {
      enabled: fileDescriptor(rel.interactiveEnabled, enabledBytes),
      disabled: fileDescriptor(rel.interactiveDisabled, disabledBytes),
    },
  };
}

function interactiveCandidates(htmlBytes) {
  const { enabled, disabled } = deriveInteractive(htmlBytes.toString("utf8"));
  const e = Buffer.from(enabled, "utf8");
  const d = Buffer.from(disabled, "utf8");
  return {
    "interactive-enabled.html": e,
    "interactive-disabled.html": d,
    "interactive-baseline.json": Buffer.from(baselineText(buildInteractiveBaseline(htmlBytes, e, d)), "utf8"),
  };
}
const interactiveTargets = {
  "interactive-enabled.html": rel.interactiveEnabled,
  "interactive-disabled.html": rel.interactiveDisabled,
  "interactive-baseline.json": rel.interactiveBaseline,
};

export function checkInteractive() {
  const htmlBytes = regularFile(abs(rel.html), rel.html);
  const want = interactiveCandidates(htmlBytes);
  for (const [name, r] of Object.entries(interactiveTargets)) {
    assert.ok(existsSync(abs(r)), `absent interactive evidence: ${r}`);
    assert.ok(regularFile(abs(r), r).equals(want[name]), `${r} differs from the pure derivation of ${rel.html}`);
  }
}

// ---------------------------------------------------------------------------------------------
// Read-only validation (--check). Imports no TS/dist module and spawns nothing.
// ---------------------------------------------------------------------------------------------
export function validateGolden(goldenBytes, htmlBytes) {
  const text = goldenBytes.toString("utf8");
  assert.ok(Buffer.from(text, "utf8").equals(goldenBytes), "golden.jsonl is not valid UTF-8");
  assert.ok(text.endsWith("\n") && text.length > 1, "golden.jsonl is empty or lacks a final newline");
  const lines = text.slice(0, -1).split("\n");
  assert.equal(lines.length, caseIDs.length, "golden.jsonl row count differs from the 24-case matrix");
  const rows = lines.map((line, i) => {
    const row = JSON.parse(line);
    assert.deepEqual(Object.keys(row), ["id", "analysisJSON", "htmlJSON", "afterJSON"], `line ${i + 1}: row fields`);
    for (const k of Object.keys(row)) assert.equal(typeof row[k], "string", `line ${i + 1}: ${k} must be a string`);
    assert.equal(line, JSON.stringify(row), `line ${i + 1}: row is not canonical JSON`);
    return row;
  });
  assert.deepEqual(rows.map((r) => r.id), caseIDs, "case IDs/order differ from the spec matrix");
  assert.equal(new Set(rows.map((r) => r.analysisJSON)).size, rows.length, "two cases share identical inputs");
  for (const row of rows) {
    assert.equal(row.afterJSON, row.analysisJSON, `${row.id}: input changed across the render`);
    const analysis = JSON.parse(row.analysisJSON);
    assert.equal(JSON.stringify(analysis), row.analysisJSON, `${row.id}: analysisJSON does not round-trip`);
    assertAnalysisShape(analysis, row.id);
    const html = JSON.parse(row.htmlJSON);
    assert.equal(typeof html, "string", `${row.id}: htmlJSON is not a JSON string`);
    assert.equal(JSON.stringify(html), row.htmlJSON, `${row.id}: htmlJSON does not round-trip`);
    assert.ok(html.startsWith("<!doctype html>\n<html lang=\"en\">") && html.endsWith("</html>"), `${row.id}: html envelope`);
  }
  const byId = new Map(rows.map((r) => [r.id, r]));
  const escaped = JSON.parse(byId.get("escaping").htmlJSON);
  assert.ok(escaped.includes("&lt;script&gt;not markup&lt;/script&gt;"), "escaping: statement is not escaped");
  assert.ok(!escaped.includes("<script>not markup"), "escaping: raw statement markup leaked");
  assert.ok(htmlBytes.equals(Buffer.from(JSON.parse(byId.get("replay").htmlJSON), "utf8")),
    "incident.html differs from the replay row (UTF-8 transport)");
  assert.equal(JSON.parse(byId.get("replay").analysisJSON).grade.value, "D", "replay baseline must supply grade D");
  assert.equal(JSON.parse(byId.get("retained-rerun").analysisJSON).mode, "RERUN", "retained-rerun must be a RERUN analysis");
  return rows;
}

export function checkEvidence() {
  assert.equal(new Set(caseIDs).size, 24, "caseIDs must hold 24 unique IDs");
  for (const r of [rel.baseline, rel.golden, rel.html]) {
    assert.ok(existsSync(abs(r)), `absent capture evidence: ${r}`);
  }
  const goldenBytes = regularFile(abs(rel.golden), rel.golden);
  const htmlBytes = regularFile(abs(rel.html), rel.html);
  validateGolden(goldenBytes, htmlBytes);

  const baselineBytes = regularFile(abs(rel.baseline), rel.baseline);
  const parsed = JSON.parse(baselineBytes.toString("utf8"));
  assert.equal(baselineBytes.toString("utf8"), baselineText(parsed), "baseline.json is not canonical");
  assertOracleRuntime(parsed.runtime, "baseline.runtime");
  const manifestBlob = parsed.frozenSource?.manifestGitBlob;
  assert.ok(typeof manifestBlob === "string" && hex(40).test(manifestBlob), "baseline.frozenSource.manifestGitBlob");
  const expected = buildBaseline(measure(), parsed.runtime, { manifestGitBlob: manifestBlob }, goldenBytes, htmlBytes);
  assert.deepEqual(parsed, expected, "baseline.json pins differ from the current source/dist/input/generator/corpus bytes");
  assert.equal(parsed.frozenSource.commit, frozenCommit);
  assert.equal(parsed.frozenSource.manifestVersion, frozenManifestVersion);
  assert.equal(parsed.currentManifestVersion, currentManifestVersion);
  for (const f of parsed.frozenSource.files) assert.ok(hex(40).test(f.gitBlob) && hex(64).test(f.sha256), `bad pin ${f.path}`);
  for (const f of parsed.compiledModules.files) assert.ok(hex(64).test(f.sha256), `bad pin ${f.path}`);
  checkInteractive();
  return { rows: caseIDs.length, parsed };
}

// ---------------------------------------------------------------------------------------------
// Argument grammar
// ---------------------------------------------------------------------------------------------
const usage = "usage: gen-incident-renderer.mjs --check | --write-interactive | [--recapture] --scratch ABSOLUTE_FRESH_DIRECTORY";
export function parseArgs(argv) {
  if (argv.length === 1 && argv[0] === "--check") return { mode: "check" };
  if (argv.length === 1 && argv[0] === "--write-interactive") return { mode: "write-interactive" };
  let mode = "capture";
  let rest = argv;
  if (argv[0] === "--recapture") { mode = "recapture"; rest = argv.slice(1); }
  if (rest.length === 2 && rest[0] === "--scratch") {
    const scratch = rest[1];
    assert.ok(isAbsolute(scratch) && resolve(scratch) === scratch && scratch !== resolve("/"),
      `--scratch must be a normalized absolute path (no trailing separator or dot segments): ${scratch}`);
    return { mode, scratch };
  }
  throw new Error(usage);
}

function lstatOrNull(path) {
  try { return lstatSync(path); } catch (e) { if (e.code === "ENOENT") return null; throw e; }
}

function prepareScratch(scratch) {
  const within = (child, parent) => {
    const r = relative(parent, child);
    return r === "" || (!r.startsWith("..") && !isAbsolute(r));
  };
  const st = lstatOrNull(scratch);
  if (st) {
    assert.ok(st.isDirectory() && !st.isSymbolicLink(), `scratch is not a plain directory: ${scratch}`);
    assert.equal(readdirSync(scratch).length, 0, `scratch is not empty (refusing to reuse): ${scratch}`);
  } else {
    assert.ok(statSync(dirname(scratch)).isDirectory(), "scratch parent directory does not exist");
    mkdirSync(scratch); // non-recursive: fails if it appeared concurrently
  }
  const real = realpathSync(scratch);
  const realRepo = realpathSync(repo);
  for (const forbidden of [rel.src, "dist", "difftest/testdata"]) {
    assert.ok(!within(real, join(realRepo, ...forbidden.split("/"))), `scratch must not live inside ${forbidden}`);
  }
  return real;
}

const writeNew = (path, data) => writeFileSync(path, data, { flag: "wx" });

// ---------------------------------------------------------------------------------------------
// Capture / recapture
// ---------------------------------------------------------------------------------------------
function git(args) {
  const r = spawnSync("git", ["-C", repo, ...args], {
    timeout: gitTimeoutMs, maxBuffer: 64 * 1024 * 1024, env: { ...process.env, GIT_OPTIONAL_LOCKS: "0" },
  });
  assert.equal(r.error, undefined, `git ${args.join(" ")}: ${r.error?.message}`);
  assert.equal(r.status, 0, `git ${args.join(" ")} failed: ${r.stderr?.toString("utf8")}`);
  return r.stdout;
}

function preflight(measured) {
  const runtime = currentRuntime();
  assertOracleRuntime(runtime, "runtime");
  const manifest = JSON.parse(regularFile(abs("package.json"), "package.json").toString("utf8"));
  assert.equal(manifest.version, currentManifestVersion, "current manifest version drift");

  const resolved = git(["rev-parse", "--verify", `${frozenCommit}^{commit}`]).toString("utf8").trim();
  assert.equal(resolved, frozenCommit, "frozen commit does not resolve to itself");
  const lsTree = git(["ls-tree", "-r", "-z", "--full-tree", frozenCommit, "--", rel.src]);
  const frozen = new Map();
  for (const entry of lsTree.toString("utf8").split("\0").filter((e) => e !== "")) {
    const m = /^(\d+) blob ([0-9a-f]{40})\t(.+)$/.exec(entry);
    assert.ok(m && (m[1] === "100644" || m[1] === "100755"), `unexpected frozen src entry: ${entry}`);
    frozen.set(m[3], m[2]);
  }
  assert.ok(frozen.size > 0, "frozen commit has no src/ files");
  const working = new Map(measured.source.files.map((f) => [f.path, f.gitBlob]));
  assert.deepEqual([...working.keys()].sort(bytesCompare), [...frozen.keys()].sort(bytesCompare),
    "src/ file set differs from the frozen commit");
  for (const [path, blob] of frozen) assert.equal(working.get(path), blob, `${path}: differs from the frozen commit`);

  const frozenPackage = git(["cat-file", "blob", `${frozenCommit}:package.json`]);
  assert.equal(JSON.parse(frozenPackage.toString("utf8")).version, frozenManifestVersion, "frozen manifest version drift");

  // Stale-build guard (mtime is advisory; the controller's fresh build and the dist pins are the evidence).
  const newestSrc = Math.max(...measured.source.files.map((f) => statSync(abs(f.path)).mtimeMs));
  for (const f of measured.dist.files) {
    assert.ok(statSync(abs(f.path)).mtimeMs >= newestSrc, `${f.path} is older than src/: build freshly from frozen source first`);
  }
  return { runtime, attest: { manifestGitBlob: gitBlobSha1(frozenPackage) }, lsTree, frozenPackage };
}

async function capture(mode, scratchArg) {
  assert.equal(new Set(caseIDs).size, 24, "caseIDs must hold 24 unique IDs");
  const evidence = [rel.golden, rel.html, rel.baseline];
  if (mode === "capture") {
    for (const r of evidence) assert.ok(lstatOrNull(abs(r)) === null, `refusing to overwrite existing evidence: ${r}`);
  } else {
    for (const r of evidence) assert.ok(existsSync(abs(r)), `absent capture evidence: ${r}`);
  }
  assert.ok(statSync(abs(rel.evidenceDir)).isDirectory(), `${rel.evidenceDir} must exist`);

  const scratch = prepareScratch(scratchArg);
  const dirs = { preflight: join(scratch, "preflight"), candidate: join(scratch, "candidate"), cwd: join(scratch, "cwd") };
  for (const d of Object.values(dirs)) mkdirSync(d);

  const before = measure();
  const pre = preflight(before);
  writeNew(join(dirs.preflight, "runtime.json"), `${JSON.stringify(pre.runtime, null, 2)}\n`);
  writeNew(join(dirs.preflight, "frozen-src-ls-tree.z"), pre.lsTree);
  writeNew(join(dirs.preflight, "frozen-package.json"), pre.frozenPackage);
  writeNew(join(dirs.preflight, "measured-pins.json"), `${JSON.stringify(withoutBytes(before), null, 2)}\n`);

  // Real frozen calls; the working directory is the preserved scratch cwd so nothing can land in the repo.
  process.chdir(dirs.cwd);
  const { createDemoAnalysis } = await import(pathToFileURL(abs("dist/engine.js")).href);
  const { renderIncidentPage } = await import(pathToFileURL(abs("dist/ui.js")).href);
  assert.equal(typeof createDemoAnalysis, "function", "dist/engine.js createDemoAnalysis");
  assert.equal(typeof renderIncidentPage, "function", "dist/ui.js renderIncidentPage");

  const replay = createDemoAnalysis("REPLAY");
  assert.equal(replay.mode, "REPLAY", "createDemoAnalysis did not return REPLAY");
  const rerun = JSON.parse(before.retainedBytes.toString("utf8"));
  assert.equal(rerun.mode, "RERUN", "retained input is not a RERUN analysis");

  const cases = buildAnalyses(clone(replay), rerun);
  const rows = cases.map(([id, analysis]) => captureRow(id, analysis, renderIncidentPage));
  const goldenBytes = Buffer.from(rows.map((r) => JSON.stringify(r)).join("\n") + "\n", "utf8");
  const htmlBytes = Buffer.from(JSON.parse(rows[0].htmlJSON), "utf8");
  validateGolden(goldenBytes, htmlBytes);

  const after = measure();
  assert.deepEqual(withoutBytes(after), withoutBytes(before), "pinned source/dist/input/generator bytes changed during capture");
  const baselineBytes = Buffer.from(baselineText(buildBaseline(after, pre.runtime, pre.attest, goldenBytes, htmlBytes)), "utf8");

  const candidate = { "golden.jsonl": goldenBytes, "incident.html": htmlBytes, "baseline.json": baselineBytes };
  for (const [name, bytes] of Object.entries(candidate)) {
    writeNew(join(dirs.candidate, name), bytes);
    assert.ok(readFileSync(join(dirs.candidate, name)).equals(bytes), `scratch readback differs: ${name}`);
  }
  const targets = { "golden.jsonl": rel.golden, "incident.html": rel.html, "baseline.json": rel.baseline };

  if (mode === "capture") {
    for (const r of evidence) assert.ok(lstatOrNull(abs(r)) === null, `refusing to overwrite existing evidence: ${r}`);
    // Baseline last: a partial failure leaves --check failing rather than a self-consistent subset.
    for (const name of ["golden.jsonl", "incident.html", "baseline.json"]) writeNew(abs(targets[name]), candidate[name]);
    for (const [name, r] of Object.entries(targets)) {
      assert.ok(readFileSync(abs(r)).equals(candidate[name]), `committed readback differs: ${r}`);
    }
    checkEvidence();
    console.log(`incident-renderer captured: ${rows.length} cases, scratch ${scratch}`);
  } else {
    for (const [name, r] of Object.entries(targets)) {
      assert.ok(readFileSync(abs(r)).equals(candidate[name]), `recapture: ${r} differs from fresh real-call bytes`);
    }
    const interactive = interactiveCandidates(htmlBytes);
    for (const [name, r] of Object.entries(interactiveTargets)) {
      writeNew(join(dirs.candidate, name), interactive[name]);
      assert.ok(readFileSync(abs(r)).equals(interactive[name]), `recapture: ${r} differs from fresh derivation`);
    }
    console.log(`incident-renderer recapture identical: ${rows.length} cases, scratch ${scratch}`);
  }
}

async function main(argv) {
  const args = parseArgs(argv);
  if (args.mode === "check") {
    const { rows } = checkEvidence();
    console.log(`incident-renderer evidence valid: ${rows} cases`);
    return;
  }
  if (args.mode === "write-interactive") {
    // Pure derivation from the committed frozen incident.html; refuses to overwrite.
    const want = interactiveCandidates(regularFile(abs(rel.html), rel.html));
    for (const r of Object.values(interactiveTargets)) assert.ok(lstatOrNull(abs(r)) === null, `refusing to overwrite: ${r}`);
    for (const name of ["interactive-enabled.html", "interactive-disabled.html", "interactive-baseline.json"]) {
      writeNew(abs(interactiveTargets[name]), want[name]);
    }
    checkInteractive();
    console.log("incident-renderer interactive pages written");
    return;
  }
  await capture(args.mode, args.scratch);
}

const invoked = process.argv[1] !== undefined && (() => {
  try { return realpathSync(resolve(process.argv[1])) === realpathSync(generatorFile); } catch { return false; }
})();
if (invoked) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(`gen-incident-renderer: ${error instanceof Error ? error.message : String(error)}`);
    process.exit(1);
  });
}
