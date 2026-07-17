import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { digestJson } from "./canonical.js";
import type { FixtureState, RunRecord, Verdict, Witness } from "./domain.js";
import { classifyFromWitnessResult, parseWitnessResult, WITNESS_RESULT_PROTOCOL } from "./witness-result.js";

export const sourceHunk = "settlement-display-default";
export const rendererHunk = "settlement-display-boundary";
export const unresolvedHunk = "partial-renderer-hunk";

const environment = {
  runtime: process.version,
  packageManager: "none",
  runner: "built-in-sample-v1",
  network: "disabled",
  credentials: "redacted"
};

export const environmentDigest = digestJson(environment);
export const incidentPacket = {
  symptom: "A completed refund can overwrite settlement currency with display currency.",
  ciExcerpt: "Expected settlement currency USD; received EUR.",
  repository: "currency-reconciliation-sample",
  language: "JavaScript"
};
export const incidentPacketDigest = digestJson(incidentPacket);

const state = (
  id: string,
  contributionId: string,
  sessionName: string,
  turnOrdinal: number,
  hunks: string[],
  label: string
): FixtureState => ({
  id,
  contributionId,
  sessionName,
  turnOrdinal,
  hunks,
  treeDigest: digestJson({ id, contributionId, turnOrdinal, hunks }),
  fidelity: "DEMO_SAMPLE",
  label
});

export const fixtureStates = {
  contributionAtlas: state("contribution-atlas", "atlas", "Session Atlas", 1, [], "Atlas contribution boundary"),
  contributionBirch: state("contribution-birch", "birch", "Session Birch", 1, [], "Birch contribution boundary"),
  contributionCedar: state("contribution-cedar", "cedar", "Session Cedar", 5, [sourceHunk, rendererHunk], "Cedar contribution boundary"),
  contributionDelta: state("contribution-delta", "delta", "Session Delta", 1, [sourceHunk, rendererHunk], "Delta contribution boundary"),
  turn1: state("cedar-turn-1", "cedar", "Session Cedar", 1, [], "Turn 1"),
  turn2: state("cedar-turn-2", "cedar", "Session Cedar", 2, [], "Turn 2"),
  turn3: state("cedar-turn-3", "cedar", "Session Cedar", 3, [], "Turn 3"),
  turn4: state("cedar-turn-4", "cedar", "Session Cedar", 4, [], "Turn 4 — last good"),
  turn5: state("cedar-turn-5", "cedar", "Session Cedar", 5, [sourceHunk, rendererHunk], "Turn 5 — first bad"),
  turn6: state("cedar-turn-6", "cedar", "Session Cedar", 6, [sourceHunk, rendererHunk], "Turn 6"),
  turn7: state("cedar-turn-7", "cedar", "Session Cedar", 7, [], "Turn 7 — repaired later"),
  turn8: state("cedar-turn-8", "cedar", "Session Cedar", 8, [sourceHunk, rendererHunk], "Turn 8 — reintroduced"),
  candidate: state("candidate-two-hunks", "counterfactual", "Counterfactual", 1, [sourceHunk, rendererHunk], "Apply two implicated hunks to last good"),
  reverted: state("candidate-remove-two-hunks", "counterfactual", "Counterfactual", 5, [], "Remove two implicated hunks from first bad"),
  sourceOnly: state("candidate-source-only", "counterfactual", "Counterfactual", 2, [sourceHunk], "Apply source hunk only"),
  rendererOnly: state("candidate-renderer-only", "counterfactual", "Counterfactual", 3, [rendererHunk], "Apply boundary hunk only"),
  unresolved: state("candidate-unresolved", "counterfactual", "Counterfactual", 4, [unresolvedHunk], "Apply partial renderer hunk"),
  repaired: state("repair-worktree", "repair", "Repair worktree", 1, [], "Repair worktree"),
  firstBad: state("prevention-first-bad", "prevention", "Prevention proof", 2, [sourceHunk, rendererHunk], "First bad state"),
  lastGood: state("prevention-last-good", "prevention", "Prevention proof", 1, [], "Last good state")
} as const;

export const timelineStates: FixtureState[] = [
  fixtureStates.turn1,
  fixtureStates.turn2,
  fixtureStates.turn3,
  fixtureStates.turn4,
  fixtureStates.turn5,
  fixtureStates.turn6,
  fixtureStates.turn7,
  fixtureStates.turn8
];

export const contributionStates: FixtureState[] = [
  fixtureStates.contributionAtlas,
  fixtureStates.contributionBirch,
  fixtureStates.contributionCedar,
  fixtureStates.contributionDelta
];

export function fixtureStateById(id: string): FixtureState | undefined {
  return [...contributionStates, ...timelineStates, fixtureStates.candidate, fixtureStates.reverted, fixtureStates.sourceOnly, fixtureStates.rendererOnly, fixtureStates.unresolved, fixtureStates.repaired, fixtureStates.firstBad, fixtureStates.lastGood]
    .find((candidate) => candidate.id === id);
}

export function createFrozenWitness(): Witness {
  const withoutDigest = {
    id: "refund-settlement-currency",
    version: 1,
    behavior: "Completed refunds preserve the order settlement currency.",
    command: "node witness.mjs",
    overlay: ["witness.mjs"],
    policy: {
      network: "disabled" as const,
      credentials: "redacted" as const,
      runner: "built-in-sample" as const,
      timeoutSeconds: 2,
      note: "Only the built-in, reviewed sample is executed by the judge fixture."
    },
    proposedBeforeLocalization: true,
    approvedAt: "2026-07-14T09:00:00.000Z",
    incidentPacketDigest
  };
  return { ...withoutDigest, digest: digestJson(withoutDigest) };
}

function implementationFor(hunks: string[]): string {
  if (hunks.includes(unresolvedHunk)) {
    return "export const broken = ;\n";
  }
  const sourceLine = hunks.includes(sourceHunk)
    ? 'const displayCurrency = refund.displayCurrency ?? "EUR";'
    : "const displayCurrency = refund.displayCurrency ?? order.currency;";
  const settlementLine = hunks.includes(rendererHunk)
    ? "const settlementCurrency = displayCurrency;"
    : "const settlementCurrency = order.currency;";
  return [
    "export function completeRefund(order, refund) {",
    `  ${sourceLine}`,
    `  ${settlementLine}`,
    "  return { settlementCurrency, displayCurrency };",
    "}"
  ].join("\n");
}

// This built-in sample witness emits a structured faultline.witness-result.v1
// line before exiting, so the judge demo dogfoods the same protocol that
// distinguishes a real predicate result from a compile/setup incompatibility
// instead of trusting a bare exit code.
const witnessProgram = [
  'import { completeRefund } from "./reconcile.mjs";',
  'const result = completeRefund({ currency: "USD" }, {});',
  'if (result.settlementCurrency !== "USD") {',
  '  console.error(`expected settlement USD; received ${result.settlementCurrency}`);',
  `  console.log(JSON.stringify({ protocol: "${WITNESS_RESULT_PROTOCOL}", outcome: "PREDICATE_FAIL" }));`,
  '  process.exit(1);',
  '}',
  'console.log("settlement currency preserved");',
  `console.log(JSON.stringify({ protocol: "${WITNESS_RESULT_PROTOCOL}", outcome: "PREDICATE_PASS" }));`
].join("\n");

function verdictFor(result: ReturnType<typeof spawnSync>): { verdict: Verdict; reasonCode: string } {
  if (result.error || result.status === null || result.signal) {
    return { verdict: "ERROR", reasonCode: "RUNNER_ERROR" };
  }
  const structured = parseWitnessResult(String(result.stdout ?? ""));
  if (structured) {
    const classified = classifyFromWitnessResult(structured.outcome);
    return { verdict: classified.verdict, reasonCode: classified.reason };
  }
  if (String(result.stderr ?? "").includes("SyntaxError")) {
    return { verdict: "ERROR", reasonCode: "FIXTURE_COMPILE_ERROR" };
  }
  if (result.status === 0) {
    return { verdict: "PASS", reasonCode: "WITNESS_SATISFIED" };
  }
  if (result.status === 1) {
    return { verdict: "FAIL", reasonCode: "WITNESS_ASSERTION_FAILED" };
  }
  return { verdict: "ERROR", reasonCode: "FIXTURE_COMPILE_ERROR" };
}

export function executeFixtureState(input: FixtureState, witness: Witness, executionAttempt = 0): RunRecord {
  const started = Date.now();
  // Wall clocks can move backwards (notably after Windows time synchronization).
  // Evidence durations are measurements, so derive them from Node's monotonic clock.
  const startedMonotonic = process.hrtime.bigint();
  const runDirectory = join(tmpdir(), "faultline-sample", `${input.id}-${process.pid}-${Date.now()}-${Math.random().toString(16).slice(2)}`);
  mkdirSync(runDirectory, { recursive: true });
  try {
    writeFileSync(join(runDirectory, "reconcile.mjs"), implementationFor(input.hunks), "utf8");
    writeFileSync(join(runDirectory, "witness.mjs"), witnessProgram, "utf8");
    const result = spawnSync(process.execPath, ["witness.mjs"], {
      cwd: runDirectory,
      encoding: "utf8",
      timeout: witness.policy.timeoutSeconds * 1_000,
      env: { PATH: process.env.PATH ?? "" }
    });
    const classified = verdictFor(result);
    const stdout = result.stdout ?? "";
    const stderr = `${result.stderr ?? ""}${result.error ? `${result.error.message}\n` : ""}`;
    return {
      id: digestJson({ state: input.id, witness: witness.digest, executionAttempt, started, stdout, stderr }),
      stateId: input.id,
      witnessDigest: witness.digest,
      environmentDigest,
      verdict: classified.verdict,
      reasonCode: classified.reasonCode,
      stdout,
      stderr,
      durationMs: Number((process.hrtime.bigint() - startedMonotonic) / 1_000_000n),
      executionKind: "EXECUTED",
      executedAt: new Date().toISOString()
    };
  } finally {
    rmSync(runDirectory, { recursive: true, force: true, maxRetries: 2 });
  }
}

export function replayRun(input: FixtureState, witness: Witness): RunRecord {
  const expected = input.hunks.includes(unresolvedHunk)
    ? { verdict: "ERROR" as const, reasonCode: "FIXTURE_COMPILE_ERROR", stdout: "", stderr: "synthetic partial hunk does not compile\n" }
    : input.hunks.includes(sourceHunk) && input.hunks.includes(rendererHunk)
      ? { verdict: "FAIL" as const, reasonCode: "WITNESS_ASSERTION_FAILED", stdout: "", stderr: "expected settlement USD; received EUR\n" }
      : { verdict: "PASS" as const, reasonCode: "WITNESS_SATISFIED", stdout: "settlement currency preserved\n", stderr: "" };
  return {
    id: digestJson({ state: input.id, witness: witness.digest, replay: true }),
    stateId: input.id,
    witnessDigest: witness.digest,
    environmentDigest,
    verdict: expected.verdict,
    reasonCode: expected.reasonCode,
    stdout: expected.stdout,
    stderr: expected.stderr,
    durationMs: 0,
    executionKind: "CACHED",
    executedAt: "2026-07-14T09:05:00.000Z"
  };
}
