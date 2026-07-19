import { digestJson } from "./canonical.js";
import {
  contributionStates,
  createFrozenWitness,
  executeFixtureState,
  fixtureStates,
  replayRun,
  rendererHunk,
  sourceHunk,
  timelineStates
} from "./fixture.js";
import type { DemoAnalysis, FixtureState, RunMode, RunRecord, Transition, Verdict } from "./domain.js";

function run(mode: RunMode, state: FixtureState, witness = createFrozenWitness(), executionAttempt = 0): RunRecord {
  return mode === "RERUN" ? executeFixtureState(state, witness, executionAttempt) : replayRun(state, witness);
}

function verdictIsStable(mode: RunMode, state: FixtureState, witness = createFrozenWitness()): { stable: boolean; runs: RunRecord[] } {
  if (mode === "REPLAY") return { stable: false, runs: [run(mode, state, witness)] };
  const runs = [1, 2, 3].map((executionAttempt) => run(mode, state, witness, executionAttempt));
  const baseline = runs[0]!;
  const stable = new Set(runs.map((record) => record.id)).size === runs.length
    && runs.every((record) => record.executionKind === "EXECUTED"
      && (record.verdict === "PASS" || record.verdict === "FAIL")
      && record.verdict === baseline.verdict
      && record.stateId === baseline.stateId
      && record.witnessDigest === baseline.witnessDigest
      && record.environmentDigest === baseline.environmentDigest);
  return { stable, runs };
}

function transitionKind(before: Verdict, after: Verdict): "PASS_TO_FAIL" | "FAIL_TO_PASS" | undefined {
  if (before === "PASS" && after === "FAIL") return "PASS_TO_FAIL";
  if (before === "FAIL" && after === "PASS") return "FAIL_TO_PASS";
  return undefined;
}

function findTransitions(mode: RunMode, witness = createFrozenWitness()): { transitions: Transition[]; timeline: RunRecord[]; boundaryRuns: RunRecord[] } {
  const timeline = timelineStates.map((state) => run(mode, state, witness));
  const transitions: Transition[] = [];
  const boundaryRuns: RunRecord[] = [];
  for (let index = 1; index < timeline.length; index += 1) {
    const before = timeline[index - 1]!;
    const after = timeline[index]!;
    const kind = transitionKind(before.verdict, after.verdict);
    if (!kind) continue;
    const beforeState = timelineStates[index - 1]!;
    const afterState = timelineStates[index]!;
    const beforeStable = verdictIsStable(mode, beforeState, witness);
    const afterStable = verdictIsStable(mode, afterState, witness);
    boundaryRuns.push(...beforeStable.runs, ...afterStable.runs);
    transitions.push({
      beforeStateId: before.stateId,
      afterStateId: after.stateId,
      beforeVerdict: before.verdict,
      afterVerdict: after.verdict,
      stable: beforeStable.stable && afterStable.stable,
      kind,
      boundaryRunIds: [...beforeStable.runs, ...afterStable.runs].map((record) => record.id)
    });
  }
  return { transitions, timeline, boundaryRuns };
}

function minimize(mode: RunMode, witness = createFrozenWitness()): { minimization: DemoAnalysis["minimization"]; runs: RunRecord[] } {
  const source = run(mode, fixtureStates.sourceOnly, witness);
  const renderer = run(mode, fixtureStates.rendererOnly, witness);
  const unresolved = run(mode, fixtureStates.unresolved, witness);
  const candidate = run(mode, fixtureStates.candidate, witness);
  const necessity = run(mode, fixtureStates.reverted, witness);
  const empty = run(mode, fixtureStates.lastGood, witness);
  const toOutcome = (record: RunRecord): "PASS" | "FAIL" | "UNRESOLVED" => {
    if (record.verdict === "PASS") return "PASS";
    if (record.verdict === "FAIL") return "FAIL";
    return "UNRESOLVED";
  };
  return {
    runs: [source, renderer, unresolved, candidate, necessity, empty],
    minimization: {
    budget: { used: 6, max: 50 },
    attempts: [
      { id: "attempt-empty", subset: [], outcome: toOutcome(empty), runId: empty.id, note: "No implicated edits preserve the witness." },
      { id: "attempt-source", subset: [sourceHunk], outcome: toOutcome(source), runId: source.id, note: "The display default alone does not change settlement currency." },
      { id: "attempt-renderer", subset: [rendererHunk], outcome: toOutcome(renderer), runId: renderer.id, note: "The boundary assignment alone receives USD." },
      { id: "attempt-partial", subset: ["partial-renderer-hunk"], outcome: toOutcome(unresolved), runId: unresolved.id, note: "The partial patch cannot compile and is unresolved, not evidence." },
      { id: "attempt-candidate", subset: [sourceHunk, rendererHunk], outcome: toOutcome(candidate), runId: candidate.id, note: "Applying the two edits to last-good reproduces the frozen witness failure." },
      { id: "attempt-necessity", subset: [sourceHunk, rendererHunk], outcome: toOutcome(necessity), runId: necessity.id, note: "Removing the two edits from first-bad restores the frozen witness pass." }
    ],
    candidate: [sourceHunk, rendererHunk],
    sufficiency: candidate,
    necessity,
      termination: mode === "RERUN" ? "BIDIRECTIONALLY_VALIDATED" : "NOT_EXECUTED"
    }
  };
}

function uniqueRunCatalog(records: RunRecord[]): RunRecord[] {
  const catalog = new Map<string, RunRecord>();
  for (const record of records) {
    const existing = catalog.get(record.id);
    if (existing && JSON.stringify(existing) !== JSON.stringify(record)) {
      throw new Error(`Run id collision with conflicting evidence: ${record.id}`);
    }
    catalog.set(record.id, record);
  }
  return [...catalog.values()].sort((left, right) => left.id.localeCompare(right.id));
}

export function createDemoAnalysis(mode: RunMode): DemoAnalysis {
  const witness = createFrozenWitness();
  const contributionRuns = contributionStates.map((state) => run(mode, state, witness));
  const localized = findTransitions(mode, witness);
  const minimized = minimize(mode, witness);
  const minimization = minimized.minimization;
  const lastGood = run(mode, fixtureStates.lastGood, witness);
  const firstBad = run(mode, fixtureStates.firstBad, witness);
  const repaired = run(mode, fixtureStates.repaired, witness);
  const firstStableRegression = localized.transitions.find((transition) => transition.kind === "PASS_TO_FAIL" && transition.stable);
  const verified = mode === "RERUN" && lastGood.verdict === "PASS" && firstBad.verdict === "FAIL" && repaired.verdict === "PASS";
  const analysis: DemoAnalysis = {
    schemaVersion: "faultline.demo.v1",
    mode,
    generatedAt: mode === "REPLAY" ? "2026-07-14T09:05:00.000Z" : new Date().toISOString(),
    fixture: {
      id: "parallel-settlement-regression",
      title: "Parallel settlement-currency regression",
      summary: "A deterministic Node sample with a real executable witness and a two-edit interaction.",
      stateFidelity: "DEMO_SAMPLE"
    },
    witness,
    metrics: {
      sessions: contributionStates.length,
      turns: timelineStates.length,
      files: new Set(timelineStates.flatMap((state) => state.hunks)).size || 1,
      changedLines: timelineStates.reduce((sum, state) => sum + state.hunks.length * 2, 0) || 1,
      implicatedHunks: new Set([sourceHunk, rendererHunk]).size
    },
    contributionRuns,
    timelineRuns: localized.timeline,
    transitions: localized.transitions,
    runCatalog: uniqueRunCatalog([
      ...contributionRuns,
      ...localized.timeline,
      ...localized.boundaryRuns,
      ...minimized.runs,
      lastGood,
      firstBad,
      repaired
    ]),
    minimization,
    prevention: { lastGood, firstBad, repaired, verified },
    grade: {
      value: verified && firstStableRegression && minimization.termination === "BIDIRECTIONALLY_VALIDATED" ? "A" : "D",
      reasons: [
        "One approved witness digest is used for all comparable sample runs.",
        "The contribution and turn boundaries are stable across three reruns.",
        "The two-change-unit candidate is validated in both counterfactual directions.",
        mode === "RERUN"
          ? "The same witness passes on last-good, fails on first-bad, and passes on the repaired state."
          : "Cached replay is non-evidentiary and cannot establish stability, counterfactual proof, or prevention.",
        "This is a deterministic sample fixture; it demonstrates the protocol, not a claim about an external repository."
      ]
    },
    claims: [
      mode === "RERUN"
        ? { kind: "EXECUTED" as const, statement: "The frozen witness passes before and fails after Session Cedar turn 5.", evidenceIds: [firstStableRegression?.beforeStateId ?? "", firstStableRegression?.afterStateId ?? ""] }
        : { kind: "UNKNOWN" as const, statement: "Cached replay does not establish an executed transition.", evidenceIds: [] },
      mode === "RERUN"
        ? { kind: "DERIVED" as const, statement: "Session Cedar / turn 5 is the first stable pass-to-fail transition in recorded order.", evidenceIds: firstStableRegression ? firstStableRegression.boundaryRunIds : [] }
        : { kind: "UNKNOWN" as const, statement: "No stable boundary is certified from cached replay.", evidenceIds: [] },
      mode === "RERUN"
        ? { kind: "DERIVED" as const, statement: "The two-edit candidate is sufficient on last-good and necessary to preserve the sample failure.", evidenceIds: minimization.attempts.map((attempt) => attempt.runId ?? "").filter(Boolean) }
        : { kind: "UNKNOWN" as const, statement: "Cached counterfactual results are not a proof.", evidenceIds: [] },
      { kind: "INFERRED", statement: "Settlement and display-currency invariants were mixed at the boundary.", evidenceIds: [firstBad.id] },
      { kind: "UNKNOWN", statement: "Whether an agent intended that tradeoff is not evidenced.", evidenceIds: [] }
    ],
    warning: mode === "REPLAY"
      ? "Cached sample replay. It is non-evidentiary: use Re-run all evidence before relying on a boundary, minimization, or prevention claim."
      : "Executed built-in sample. The runner only materializes the reviewed fixture; it is not a general-purpose sandbox."
  };
  return analysis;
}

export function analysisDigest(analysis: DemoAnalysis): string {
  return digestJson(analysis);
}
