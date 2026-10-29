// Live-oracle ops for internal/turninv (slice 4b Tasks 3 and 6):
// node-oracle.ts spreads them into its handlers after its own entries, so
// each task edits only its own op file. Each op takes canonical JSON args and
// returns JSON.stringify of {value} or {threw, zod}.
import { ZodError } from "zod";
import {
  StableTurnTransitionSchema, TurnInvestigationResultSchema, TurnInvestigationRunFactSchema, TurnStateSchema,
  attributeFailureIntroduction, duplicateTurnOrdinalError, findStableTurnTransitions, reconstructStableTurnStates,
  turnStatesFromLedger
} from "../../src/turn-investigation.js";
import { investigateTurnTrees } from "../../src/turn-investigation.js";
import type { FrozenWitness } from "../../src/witness-lock.js";
import type { SandboxCommandRunner } from "../../src/sandbox.js";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { appendLifecycleEvent, createCodexLifecycleLedger, type CodexLifecycleLedger, type LifecycleEventInput } from "../../src/ledger.js";
import { signTurnTreeSnapshot, TURN_TREE_SNAPSHOT_VERSION } from "../../src/turn-snapshot.js";

// The schemas node-oracle.ts adds to namedSchemas (re-exported so that it
// needs one import line for this module).
export { StableTurnTransitionSchema, TurnInvestigationResultSchema, TurnInvestigationRunFactSchema, TurnStateSchema };

// Handler is node-oracle.ts's: it takes the request's args and may return a
// Promise. (Not imported: node-oracle.ts starts reading stdin when loaded.)
type Handler = (args: any) => unknown;

function outcome(fn: () => unknown): string {
  try {
    return JSON.stringify({ value: fn() });
  } catch (error) {
    return JSON.stringify({ threw: (error as Error).message, zod: error instanceof ZodError });
  }
}

const d = (c: string) => `sha256:${c.repeat(64)}`;
const at = (second: number) => new Date(Date.UTC(2026, 6, 16, 11, 0, second)).toISOString();

// turnLedger builds a valid session with the frozen ledger functions: an
// optional baseline snapshot, then for each state a started, completed and
// snapshotted turn (ordinals must be 1, 2, … for the appends to verify).
function turnLedger(states: { ordinal: number; tree: string; dirty: boolean }[], baseline: boolean): CodexLifecycleLedger {
  let ledger = createCodexLifecycleLedger({ ledgerId: "ledger-turninv", sessionId: "session-turninv", createdAt: at(0) });
  let second = 0;
  const add = (input: LifecycleEventInput) => {
    second += 1;
    ledger = appendLifecycleEvent(ledger, input, { occurredAt: at(second), eventId: `event-${second}` });
  };
  const snapshot = (tree: string, dirty: boolean) => signTurnTreeSnapshot({
    schemaVersion: TURN_TREE_SNAPSHOT_VERSION, repositoryRoot: "/work/turn-repository", headCommit: "a".repeat(40),
    treeDigest: tree.repeat(40), capturedAt: at(second), dirty, statusDigest: d(dirty ? "d" : "e")
  });
  add({ type: "SESSION_STARTED", payload: { transport: "SIDE_CAR", workingDirectory: "/work/turn-repository" } });
  if (baseline) add({ type: "SESSION_BASELINE_SNAPSHOT", payload: { snapshot: snapshot("0", false) } });
  for (const state of states) {
    const turnId = `turn-${state.ordinal}`;
    add({ type: "TURN_STARTED", payload: { turnId, turnOrdinal: state.ordinal, promptDigest: d("1") } });
    add({ type: "TURN_COMPLETED", payload: { turnId, turnOrdinal: state.ordinal, outcome: "COMPLETED" } });
    add({ type: "TURN_TREE_SNAPSHOT", payload: { turnId, turnOrdinal: state.ordinal, snapshot: snapshot(state.tree, state.dirty) } });
  }
  return ledger;
}

// Task 6: a session over real tree digests. Each state is a completed turn
// and its TURN_TREE_SNAPSHOT (again: one more snapshot for the same ordinal,
// which the ledger accepts), after an optional baseline snapshot.
type SessionState = { tree: string; dirty: boolean; again?: { tree: string; dirty: boolean } };
function turnSessionLedger(repositoryRoot: string, headCommit: string, baseline: { tree: string; dirty: boolean } | null,
  states: SessionState[]): CodexLifecycleLedger {
  let ledger = createCodexLifecycleLedger({ ledgerId: "ledger-turn-session", sessionId: "session-turn-session", createdAt: at(0) });
  let second = 0;
  const add = (input: LifecycleEventInput) => {
    second += 1;
    ledger = appendLifecycleEvent(ledger, input, { occurredAt: at(second), eventId: `event-${second}` });
  };
  const snapshot = (tree: string, dirty: boolean) => signTurnTreeSnapshot({
    schemaVersion: TURN_TREE_SNAPSHOT_VERSION, repositoryRoot, headCommit, treeDigest: tree, capturedAt: at(second), dirty,
    statusDigest: d(dirty ? "d" : "e")
  });
  add({ type: "SESSION_STARTED", payload: { transport: "SIDE_CAR", workingDirectory: repositoryRoot } });
  if (baseline) add({ type: "SESSION_BASELINE_SNAPSHOT", payload: { snapshot: snapshot(baseline.tree, baseline.dirty) } });
  states.forEach((state, i) => {
    const turnOrdinal = i + 1;
    const turnId = `turn-${turnOrdinal}`;
    add({ type: "TURN_STARTED", payload: { turnId, turnOrdinal, promptDigest: d("1") } });
    add({ type: "TURN_COMPLETED", payload: { turnId, turnOrdinal, outcome: "COMPLETED" } });
    add({ type: "TURN_TREE_SNAPSHOT", payload: { turnId, turnOrdinal, snapshot: snapshot(state.tree, state.dirty) } });
    if (state.again) add({ type: "TURN_TREE_SNAPSHOT", payload: { turnId, turnOrdinal, snapshot: snapshot(state.again.tree, state.again.dirty) } });
  });
  return ledger;
}

// Runs fn with process.env.PATH and FAULTLINE_ARGV_LOG replaced, as
// node-oracle.ts's withPath and investigateGitRange op do.
async function withEnvironment<T>(path: string, argvLog: string, fn: () => Promise<T>): Promise<T> {
  const saved = { PATH: process.env.PATH, FAULTLINE_ARGV_LOG: process.env.FAULTLINE_ARGV_LOG };
  process.env.PATH = path;
  process.env.FAULTLINE_ARGV_LOG = argvLog;
  try {
    return await fn();
  } finally {
    for (const [key, value] of Object.entries(saved)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

// stateRunner is the injected runner both sides use: it reads state.txt in
// the plan's source directory ("bad" fails the predicate, "broken" fails
// like a witness that does not parse, anything else passes).
const stateRunner: SandboxCommandRunner = {
  async run(invocation) {
    const state = readFileSync(join(invocation.cwd, "state.txt"), "utf8").trim();
    const result = (outcome: string) => `state=${state}\nFAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"${outcome}"}\n`;
    if (state === "broken") return { exitCode: 1, stdout: "", stderr: "witness.mjs:3\nSyntaxError: Unexpected token '}'\n" };
    if (state === "bad") return { exitCode: 1, stdout: result("PREDICATE_FAIL"), stderr: "witness failed" };
    return { exitCode: 0, stdout: result("PREDICATE_PASS"), stderr: "" };
  }
};

type TurnInvestigationRequest = {
  repository: string; ledgerPath: string; frozenWitness: FrozenWitness; expectedFrozenDigest: string; image: string;
  runtimeMapping?: Record<string, string>;
};

export const ops: Record<string, Handler> = {
  turnLedger: ({ states, baseline }: { states: { ordinal: number; tree: string; dirty: boolean }[]; baseline: boolean }) =>
    JSON.stringify(turnLedger(states, baseline)),
  turnStatesFromLedger: ({ ledger }: { ledger: any }) => outcome(() => turnStatesFromLedger(ledger)),
  duplicateTurnOrdinalError: ({ states }: { states: any[] }) => outcome(() => duplicateTurnOrdinalError(states)),
  reconstructStableTurnStates: ({ states, runs, requireNativeExecutor }: { states: any[]; runs: any[]; requireNativeExecutor?: boolean }) =>
    outcome(() => reconstructStableTurnStates(states, runs, requireNativeExecutor === undefined ? {} : { requireNativeExecutor })),
  findStableTurnTransitions: ({ stable }: { stable: any[] }) => outcome(() => findStableTurnTransitions(stable)),
  attributeFailureIntroduction: ({ states, transitions, stableStates }: { states: any[]; transitions: any[]; stableStates?: any[] }) =>
    outcome(() => attributeFailureIntroduction(states, transitions, stableStates)),
  // Task 6.
  turnSessionLedger: ({ repositoryRoot, headCommit, baseline, states }: {
    repositoryRoot: string; headCommit: string; baseline: { tree: string; dirty: boolean } | null; states: SessionState[];
  }) => JSON.stringify(turnSessionLedger(repositoryRoot, headCommit, baseline, states)),
  // investigateTurnTrees with the native runner, with PATH and
  // FAULTLINE_ARGV_LOG (the git and docker recorders' log) replaced:
  // JSON.stringify of the result (key order kept), or "THREW:<message>".
  // Start the oracle seeded (oracle.StartSeeded) to compare nonces and times.
  // With injected, the runner is stateRunner instead.
  investigateTurnTrees: ({ request, path, argvLog, injected }: { request: TurnInvestigationRequest; path: string; argvLog: string; injected?: boolean }) =>
    withEnvironment(path, argvLog, async () => {
      try {
        return JSON.stringify(await investigateTurnTrees(injected ? { ...request, runner: stateRunner } : request));
      } catch (error) {
        return `THREW:${(error as Error).message}`;
      }
    })
};
