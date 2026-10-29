// Live-oracle ops for the slice 4a sidecar tests: node-oracle.ts spreads them
// into its handlers after its own entries, so each slice 4a task edits only
// its own op file.
import { ZodError } from "zod";
import {
  codexSidecarLedgerPath,
  inspectObservedCodexSidecar,
  recordObservedCodexHook,
  resolveLatestSidecarLedgerPath
} from "../../src/codex-sidecar.js";

// Handler is node-oracle.ts's: it takes the request's args and may return a
// Promise. (Not imported: node-oracle.ts starts reading stdin when loaded.)
type Handler = (args: any) => unknown;

// Runs fn with PATH and the given environment overrides ("" deletes a
// variable) and process.stderr.write captured; JSON.stringify of
// {result} or {threw, zod}, with the captured stderr.
function sidecarOutcome(fn: () => unknown, path: string | undefined, env: Record<string, string> | undefined, chdir?: string): string {
  const savedEnv: Record<string, string | undefined> = {};
  const overrides: Record<string, string> = { ...(env ?? {}), ...(path === undefined ? {} : { PATH: path }) };
  for (const [key, value] of Object.entries(overrides)) {
    savedEnv[key] = process.env[key];
    process.env[key] = value;
  }
  const savedCwd = process.cwd();
  if (chdir !== undefined) process.chdir(chdir);
  const originalWrite = process.stderr.write;
  let stderr = "";
  (process.stderr as any).write = (chunk: string | Uint8Array): boolean => {
    stderr += typeof chunk === "string" ? chunk : Buffer.from(chunk).toString("utf8");
    return true;
  };
  try {
    try {
      return JSON.stringify({ result: fn(), stderr });
    } catch (error) {
      return JSON.stringify({ threw: (error as Error).message, zod: error instanceof ZodError, stderr });
    }
  } finally {
    process.stderr.write = originalWrite;
    if (chdir !== undefined) process.chdir(savedCwd);
    for (const [key, value] of Object.entries(savedEnv)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

export const ops: Record<string, Handler> = {
  // Task 6 (TestLiveSidecarHooks): recordObservedCodexHook on `value`; env
  // overrides (FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY) and PATH are set for the
  // call only.
  // chdir runs the call with that working directory (a relative hook cwd
  // resolves against it, as against the Go side's).
  recordObservedCodexHook: ({ value, path, env, chdir }: { value: unknown; path?: string; env?: Record<string, string>; chdir?: string }) =>
    sidecarOutcome(() => recordObservedCodexHook(value), path, env, chdir),
  inspectObservedCodexSidecar: ({ cwd, sessionId, path }: { cwd: string; sessionId?: string; path?: string }) =>
    sidecarOutcome(() => inspectObservedCodexSidecar(cwd, sessionId), path, undefined),
  resolveLatestSidecarLedgerPath: ({ cwd, path }: { cwd: string; path?: string }) =>
    sidecarOutcome(() => resolveLatestSidecarLedgerPath(cwd), path, undefined),
  codexSidecarLedgerPath: ({ cwd, sessionId, path }: { cwd: string; sessionId: string; path?: string }) =>
    sidecarOutcome(() => codexSidecarLedgerPath(cwd, sessionId), path, undefined),
  // The oracle's own process id, for the leftover receipt stage name.
  processPid: () => process.pid
};
