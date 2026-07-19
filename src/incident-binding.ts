import { existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, parse, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson } from "./canonical.js";
import { IncidentDraftIdSchema } from "./incident.js";
import {
  relativeTrustedSystemPath,
  resolveSafeDirectorySegment,
  resolveTrustedSystemPath
} from "./safe-directory.js";

/**
 * Mutable companion to write-once incident drafts. Captures workspace session
 * facts (frozen digest + ledger path) so downstream commands can inherit them
 * without copy-paste. Explicit CLI flags always override these values.
 */
export const INCIDENT_SESSION_BINDING_SCHEMA_VERSION = "faultline.incident-session-binding.v1" as const;

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/, "expected sha256:<64 lowercase hex characters>");
const TimestampSchema = z.string().datetime({ offset: true });

export const IncidentSessionBindingSchema = z.object({
  schemaVersion: z.literal(INCIDENT_SESSION_BINDING_SCHEMA_VERSION),
  incidentId: IncidentDraftIdSchema,
  expectDigest: DigestSchema.optional(),
  ledgerPath: z.string().min(1).max(16_384).optional(),
  updatedAt: TimestampSchema
}).strict();

export type IncidentSessionBinding = z.infer<typeof IncidentSessionBindingSchema>;

function assertNonSymlinkDirectory(path: string): string {
  const safe = resolveSafeDirectorySegment(path);
  if (safe === null) {
    throw new Error(`Incident binding store path must be a real non-symlink directory: ${path}`);
  }
  return safe;
}

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

function bindingDirectory(storeDirectory: string): string {
  return join(resolveTrustedSystemPath(storeDirectory), "bindings");
}

function bindingPath(storeDirectory: string, incidentId: string): string {
  const id = IncidentDraftIdSchema.parse(incidentId);
  const root = resolveTrustedSystemPath(storeDirectory);
  const path = resolve(bindingDirectory(root), `${id}.json`);
  const nested = relativeTrustedSystemPath(root, path);
  if (!nested || nested.startsWith("..") || nested.includes(":")) {
    throw new Error("Incident binding path escaped its configured store");
  }
  return path;
}

export function readIncidentSessionBinding(
  storeDirectory: string,
  incidentId: string
): IncidentSessionBinding | null {
  const path = bindingPath(storeDirectory, incidentId);
  if (!existsSync(path)) return null;
  const stat = lstatSync(path);
  if (stat.isSymbolicLink() || !stat.isFile()) {
    throw new Error(`Incident session binding must be a regular non-symlink file: ${path}`);
  }
  const parsed = IncidentSessionBindingSchema.parse(JSON.parse(readFileSync(path, "utf8")));
  if (parsed.incidentId !== incidentId) {
    throw new Error("Stored incident session binding identifier does not match the requested identifier");
  }
  return parsed;
}

export function writeIncidentSessionBinding(
  storeDirectory: string,
  input: {
    incidentId: string;
    expectDigest?: string;
    ledgerPath?: string;
    updatedAt?: string;
  }
): IncidentSessionBinding {
  const previous = readIncidentSessionBinding(storeDirectory, input.incidentId);
  const binding = IncidentSessionBindingSchema.parse({
    schemaVersion: INCIDENT_SESSION_BINDING_SCHEMA_VERSION,
    incidentId: input.incidentId,
    expectDigest: input.expectDigest ?? previous?.expectDigest,
    ledgerPath: input.ledgerPath ?? previous?.ledgerPath,
    updatedAt: input.updatedAt ?? new Date().toISOString()
  });
  const directory = bindingDirectory(storeDirectory);
  ensureSafeDirectory(directory);
  const path = bindingPath(storeDirectory, binding.incidentId);
  writeFileSync(path, `${canonicalJson(binding)}\n`, { encoding: "utf8", mode: 0o600 });
  return binding;
}

export type InheritedSessionFacts = {
  readonly expectDigest?: string;
  readonly ledgerPath?: string;
  readonly inheritedExpectDigest: boolean;
  readonly inheritedLedgerPath: boolean;
};

/**
 * Resolve expect-digest / ledger with CLI overrides winning over local binding.
 */
export function resolveInheritedSessionFacts(options: {
  storeDirectory: string;
  incidentId: string;
  expectDigest?: string;
  ledgerPath?: string;
}): InheritedSessionFacts {
  const binding = readIncidentSessionBinding(options.storeDirectory, options.incidentId);
  const expectDigest = options.expectDigest ?? binding?.expectDigest;
  const ledgerPath = options.ledgerPath ?? binding?.ledgerPath;
  return {
    ...(expectDigest === undefined ? {} : { expectDigest }),
    ...(ledgerPath === undefined ? {} : { ledgerPath }),
    inheritedExpectDigest: options.expectDigest === undefined && binding?.expectDigest !== undefined,
    inheritedLedgerPath: options.ledgerPath === undefined && binding?.ledgerPath !== undefined
  };
}
