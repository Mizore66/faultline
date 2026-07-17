import { existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, parse, relative, resolve } from "node:path";
import { canonicalJson } from "./canonical.js";
import {
  IncidentDraftIdSchema,
  IncidentDraftSchema,
  verifyIncidentDraft,
  type IncidentDraft
} from "./incident.js";
import {
  relativeTrustedSystemPath,
  resolveSafeDirectorySegment,
  resolveTrustedSystemPath
} from "./safe-directory.js";

/**
 * A small, write-once local store for review-only intake records.
 *
 * The store deliberately has no connection to approval or freezing.  Keeping
 * the draft distinct makes it possible to prove that an intake did not turn
 * a pasted CI command into an approved witness behind the user's back.
 */
export type StoredIncidentDraft = {
  path: string;
  draft: IncidentDraft;
};

function assertNonSymlinkDirectory(path: string): string {
  const safe = resolveSafeDirectorySegment(path);
  if (safe === null) {
    throw new Error(`Incident draft store path must be a real non-symlink directory: ${path}`);
  }
  return safe;
}

/** Create each directory segment while rejecting symlink traversal. */
function ensureSafeDirectory(path: string): void {
  const target = resolveTrustedSystemPath(path);
  const volumeRoot = parse(target).root;
  const relativePath = relative(volumeRoot, target);
  const segments = relativePath.split(/[\\/]+/).filter(Boolean);
  let current = volumeRoot;
  for (const segment of segments) {
    current = join(current, segment);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    current = assertNonSymlinkDirectory(current);
  }
}

/** Check existing parent segments only; reads must never create paths. */
function assertSafeExistingDirectory(path: string): void {
  const target = resolveTrustedSystemPath(path);
  const volumeRoot = parse(target).root;
  const relativePath = relative(volumeRoot, target);
  const segments = relativePath.split(/[\\/]+/).filter(Boolean);
  let current = volumeRoot;
  for (const segment of segments) {
    current = join(current, segment);
    if (!existsSync(current)) throw new Error(`Incident draft store directory does not exist: ${current}`);
    current = assertNonSymlinkDirectory(current);
  }
}

function storeRoot(storeDirectory: string): string {
  return resolveTrustedSystemPath(storeDirectory);
}

function draftDirectory(storeDirectory: string): string {
  return join(storeRoot(storeDirectory), "drafts");
}

function draftPath(storeDirectory: string, draftId: string): string {
  const id = IncidentDraftIdSchema.parse(draftId);
  const root = storeRoot(storeDirectory);
  const path = resolve(draftDirectory(root), `${id}.json`);
  const nested = relativeTrustedSystemPath(root, path);
  if (!nested || nested.startsWith("..") || nested.includes(":")) {
    throw new Error("Incident draft path escaped its configured store");
  }
  return path;
}

export function defaultIncidentDraftStore(repository: string = process.cwd()): string {
  return join(resolve(repository), ".faultline", "incidents");
}

/** Persist a valid draft as canonical, write-once JSON. */
export function writeIncidentDraft(storeDirectory: string, input: unknown): StoredIncidentDraft {
  const parsed = IncidentDraftSchema.parse(input);
  const verification = verifyIncidentDraft(parsed);
  if (!verification.valid) throw new Error(`Refusing to persist an invalid incident draft: ${verification.errors.join("; ")}`);
  const directory = draftDirectory(storeDirectory);
  ensureSafeDirectory(directory);
  const path = draftPath(storeDirectory, parsed.draftId);
  try {
    writeFileSync(path, `${canonicalJson(parsed)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
  } catch (error) {
    const code = error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
    if (code === "EEXIST") throw new Error(`Immutable incident draft already exists: ${path}`);
    throw error;
  }
  return { path, draft: parsed as IncidentDraft };
}

/** Read a complete stored draft only after checking directory and file safety. */
export function readIncidentDraft(storeDirectory: string, draftId: string): StoredIncidentDraft {
  const directory = draftDirectory(storeDirectory);
  assertSafeExistingDirectory(directory);
  const path = draftPath(storeDirectory, draftId);
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) {
    throw new Error(`Incident draft must be a regular non-symlink file: ${path}`);
  }
  let value: unknown;
  try {
    value = JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    throw new Error(`Incident draft is not valid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
  const parsed = IncidentDraftSchema.parse(value);
  const verification = verifyIncidentDraft(parsed);
  if (!verification.valid) throw new Error(`Stored incident draft failed verification: ${verification.errors.join("; ")}`);
  if (parsed.draftId !== draftId) throw new Error("Stored incident draft identifier does not match the requested identifier");
  return { path, draft: parsed as IncidentDraft };
}
