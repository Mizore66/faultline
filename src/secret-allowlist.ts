import { existsSync, lstatSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { z } from "zod";
import { sha256 } from "./canonical.js";
import type { RedactionReport } from "./redaction.js";

export const SECRET_ALLOWLIST_FILENAME = ".faultline-secret-allowlist.json" as const;
export const SECRET_ALLOWLIST_SCHEMA_VERSION = "faultline.secret-allowlist.v1" as const;

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);

const AllowlistEntrySchema = z.object({
  path: z.string().min(1).max(4_096),
  kind: z.string().min(1).max(128),
  occurrenceDigest: DigestSchema,
  fileDigest: DigestSchema.optional(),
  note: z.string().min(1).max(512).optional()
}).strict();

export const SecretAllowlistSchema = z.object({
  schemaVersion: z.literal(SECRET_ALLOWLIST_SCHEMA_VERSION),
  entries: z.array(AllowlistEntrySchema).max(2_048)
}).strict();

export type SecretAllowlist = z.infer<typeof SecretAllowlistSchema>;
export type SecretAllowlistEntry = z.infer<typeof AllowlistEntrySchema>;

export function secretAllowlistPath(repositoryRoot: string): string {
  return join(repositoryRoot, SECRET_ALLOWLIST_FILENAME);
}

export function loadSecretAllowlist(repositoryRoot: string): SecretAllowlist {
  const path = secretAllowlistPath(repositoryRoot);
  if (!existsSync(path)) {
    return { schemaVersion: SECRET_ALLOWLIST_SCHEMA_VERSION, entries: [] };
  }
  const stat = lstatSync(path);
  if (stat.isSymbolicLink() || !stat.isFile()) {
    throw new Error(`${SECRET_ALLOWLIST_FILENAME} must be a regular non-symlink file.`);
  }
  if (stat.size > 256 * 1024) {
    throw new Error(`${SECRET_ALLOWLIST_FILENAME} exceeds FaultLine's 256 KiB parse limit.`);
  }
  return SecretAllowlistSchema.parse(JSON.parse(readFileSync(path, "utf8")));
}

export function fileContentDigest(absolutePath: string, maxBytes: number): `sha256:${string}` {
  const bytes = readFileSync(absolutePath).subarray(0, maxBytes);
  return `sha256:${sha256(bytes)}`;
}

export function allowlistConfigDigest(allowlist: SecretAllowlist): `sha256:${string}` {
  return `sha256:${sha256(JSON.stringify(allowlist))}`;
}

/**
 * A HIGH occurrence is allowlisted only when path + kind + occurrenceDigest match,
 * and when fileDigest is present on the entry it must match the current file.
 */
export function isHighOccurrenceAllowlisted(
  allowlist: SecretAllowlist,
  relativePath: string,
  kind: string,
  occurrenceDigest: string,
  currentFileDigest: string
): boolean {
  return allowlist.entries.some((entry) =>
    entry.path === relativePath
    && entry.kind === kind
    && entry.occurrenceDigest === occurrenceDigest
    && (entry.fileDigest === undefined || entry.fileDigest === currentFileDigest)
  );
}

export function filterAllowlistedHighOccurrences(
  allowlist: SecretAllowlist,
  relativePath: string,
  report: RedactionReport,
  currentFileDigest: string
): { remainingKinds: string[]; allowlistedCount: number } {
  const remainingKinds = new Set<string>();
  let allowlistedCount = 0;
  for (const occurrence of report.occurrences) {
    if (occurrence.confidence !== "HIGH") continue;
    if (isHighOccurrenceAllowlisted(
      allowlist,
      relativePath,
      occurrence.kind,
      occurrence.digest,
      currentFileDigest
    )) {
      allowlistedCount += 1;
      continue;
    }
    remainingKinds.add(occurrence.kind);
  }
  return { remainingKinds: [...remainingKinds], allowlistedCount };
}

export function isEntropyTokenAllowlisted(
  allowlist: SecretAllowlist,
  relativePath: string,
  token: string,
  currentFileDigest: string
): boolean {
  const occurrenceDigest = `sha256:${sha256(token)}`;
  return isHighOccurrenceAllowlisted(
    allowlist,
    relativePath,
    "HIGH_ENTROPY_TOKEN",
    occurrenceDigest,
    currentFileDigest
  );
}
