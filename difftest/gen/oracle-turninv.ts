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

export const ops: Record<string, Handler> = {
  turnLedger: ({ states, baseline }: { states: { ordinal: number; tree: string; dirty: boolean }[]; baseline: boolean }) =>
    JSON.stringify(turnLedger(states, baseline)),
  turnStatesFromLedger: ({ ledger }: { ledger: any }) => outcome(() => turnStatesFromLedger(ledger)),
  duplicateTurnOrdinalError: ({ states }: { states: any[] }) => outcome(() => duplicateTurnOrdinalError(states)),
  reconstructStableTurnStates: ({ states, runs, requireNativeExecutor }: { states: any[]; runs: any[]; requireNativeExecutor?: boolean }) =>
    outcome(() => reconstructStableTurnStates(states, runs, requireNativeExecutor === undefined ? {} : { requireNativeExecutor })),
  findStableTurnTransitions: ({ stable }: { stable: any[] }) => outcome(() => findStableTurnTransitions(stable)),
  attributeFailureIntroduction: ({ states, transitions, stableStates }: { states: any[]; transitions: any[]; stableStates?: any[] }) =>
    outcome(() => attributeFailureIntroduction(states, transitions, stableStates))
};
