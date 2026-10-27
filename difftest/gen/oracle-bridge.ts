// Live-oracle op for internal/minimize/bridge.go (slice 4b Task 4):
// node-oracle.ts spreads it into its handlers after its own entries, so this
// task edits only its own op file.
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { inflateSync } from "node:zlib";
import nodePath from "node:path";
import { ZodError } from "zod";
import { bridgeTurnTransitionToMinimizationCommits } from "../../src/turn-minimization-bridge.js";

// Handler is node-oracle.ts's: it takes the request's args and may return a
// Promise. (Not imported: node-oracle.ts starts reading stdin when loaded.)
type Handler = (args: any) => unknown;

// looseObjects lists the ids of the loose objects in repository/.git/objects
// (unsorted).
function looseObjects(repository: string): string[] {
  const objects = nodePath.join(repository, ".git", "objects");
  if (!existsSync(objects)) return [];
  const ids: string[] = [];
  for (const dir of readdirSync(objects)) {
    if (!/^[0-9a-f]{2}$/.test(dir)) continue;
    for (const file of readdirSync(nodePath.join(objects, dir))) ids.push(dir + file);
  }
  return ids;
}

export const ops: Record<string, Handler> = {
  // bridgeTurnTransition runs bridgeTurnTransitionToMinimizationCommits with
  // process.env changed by env for the call only. It returns
  // JSON.stringify of {value} or {threw, zod}, plus the sorted ids of the
  // loose objects the call created, each with its inflated content
  // ("commit <size>\0<body>", read without git so it works without PATH).
  bridgeTurnTransition: ({ repository, transitions, transitionIndex, env }: {
    repository: string; transitions: any[]; transitionIndex: number; env: Record<string, string>;
  }) => {
    const saved: Record<string, string | undefined> = {};
    for (const [key, value] of Object.entries(env)) {
      saved[key] = process.env[key];
      process.env[key] = value;
    }
    const before = new Set(looseObjects(repository));
    let outcome: Record<string, unknown>;
    try {
      outcome = { value: bridgeTurnTransitionToMinimizationCommits(repository, { transitions }, transitionIndex) };
    } catch (error) {
      outcome = { threw: (error as Error).message, zod: error instanceof ZodError };
    } finally {
      for (const [key, value] of Object.entries(saved)) {
        if (value === undefined) delete process.env[key];
        else process.env[key] = value;
      }
    }
    const created = looseObjects(repository).filter((id) => !before.has(id)).sort().map((id) => ({
      id,
      object: inflateSync(readFileSync(nodePath.join(repository, ".git", "objects", id.slice(0, 2), id.slice(2)))).toString("utf8")
    }));
    return JSON.stringify({ ...outcome, created });
  }
};
