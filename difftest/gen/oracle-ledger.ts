// Live-oracle ops for the slice 4a ledger tests: node-oracle.ts spreads them
// into its handlers after its own entries, so each slice 4a task edits only
// its own op file.
import { ZodError } from "zod";
import {
  LedgerIntegrityError,
  appendLifecycleEvent,
  appendLifecycleEventAtomic,
  captureGitCleanCheckpoint,
  createCodexLifecycleLedger,
  writeCodexLifecycleLedgerAtomic
} from "../../src/ledger.js";

// Handler is node-oracle.ts's: it takes the request's args and may return a
// Promise. (Not imported: node-oracle.ts starts reading stdin when loaded.)
type Handler = (args: any) => unknown;

// An op's result: {value} or {threw, zod, integrity}, where zod and
// integrity say whether the error was a ZodError or a LedgerIntegrityError.
type Outcome = { value: unknown } | { threw: string; zod: boolean; integrity: boolean };

function outcome(fn: () => unknown): Outcome {
  try {
    return { value: fn() };
  } catch (error) {
    return {
      threw: (error as Error).message,
      zod: error instanceof ZodError,
      integrity: error instanceof LedgerIntegrityError
    };
  }
}

// Runs fn with the process working directory at cwd (relative paths and
// resolve() then match the Go side, which runs in the same directory).
function inDirectory<T>(cwd: string | undefined, fn: () => T): T {
  if (cwd === undefined) return fn();
  const saved = process.cwd();
  process.chdir(cwd);
  try {
    return fn();
  } finally {
    process.chdir(saved);
  }
}

type Step = { out: string } | { throw: string };

export const ops: Record<string, Handler> = {
  // Task 4 (TestLiveLedgerWriters): JSON.stringify of the outcome.
  ledgerCreate: ({ options }: { options: { sessionId: string; ledgerId?: string; createdAt?: string } }) =>
    JSON.stringify(outcome(() => createCodexLifecycleLedger(options))),
  ledgerAppend: ({ ledger, input, options }: { ledger: any; input: any; options?: { eventId?: string; occurredAt?: string } }) =>
    JSON.stringify(outcome(() => appendLifecycleEvent(ledger, input, options))),
  ledgerAppendAtomic: ({ file, input, options, cwd }: { file: string; input: any; options?: { eventId?: string; occurredAt?: string }; cwd?: string }) =>
    inDirectory(cwd, () => JSON.stringify(outcome(() => appendLifecycleEventAtomic(file, input, options)))),
  ledgerWriteAtomic: ({ file, ledger, cwd }: { file: string; ledger: any; cwd?: string }) =>
    inDirectory(cwd, () => JSON.stringify(outcome(() => writeCodexLifecycleLedgerAtomic(file, ledger)))),
  // With steps, the runner replays them in order ({throw} throws an Error;
  // an exhausted script throws "script exhausted") and each call is
  // recorded as [repository, args]; without, it is the default hardened
  // runner, with PATH set to path. JSON.stringify({...outcome, calls}).
  captureGitCleanCheckpoint: ({ repository, path, steps, cwd }: { repository: string; path?: string; steps?: Step[]; cwd?: string }) =>
    inDirectory(cwd, () => {
      const calls: unknown[] = [];
      const queue = steps === undefined ? undefined : [...steps];
      const runGit = queue === undefined ? undefined : (root: string, args: readonly string[]): string => {
        calls.push([root, [...args]]);
        const step = queue.shift();
        if (step === undefined) throw new Error("script exhausted");
        if ("throw" in step) throw new Error(step.throw);
        return step.out;
      };
      const saved = process.env.PATH;
      if (path !== undefined) process.env.PATH = path;
      try {
        const result = outcome(() => captureGitCleanCheckpoint(repository, runGit === undefined ? {} : { runGit }));
        return JSON.stringify({ ...result, calls });
      } finally {
        if (path !== undefined) process.env.PATH = saved;
      }
    })
};
