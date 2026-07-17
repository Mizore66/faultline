import { createHash } from "node:crypto";
import { existsSync, lstatSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { z } from "zod";
import { digestJson } from "./canonical.js";

export const ENVIRONMENT_FINGERPRINT_VERSION = "faultline.environment-fingerprint.v1" as const;

/** Descriptors that change the execution environment for a historical state. */
export const ENVIRONMENT_DESCRIPTOR_FILES = Object.freeze([
  "package-lock.json",
  "pnpm-lock.yaml",
  "yarn.lock",
  "bun.lock",
  "bun.lockb",
  "requirements.txt",
  "poetry.lock",
  "Pipfile.lock",
  "go.sum",
  "go.mod",
  "Cargo.lock",
  ".nvmrc",
  ".node-version",
  ".python-version",
  "Dockerfile",
  "Dockerfile.faultline"
] as const);

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);

export const EnvironmentFingerprintSchema = z.object({
  schemaVersion: z.literal(ENVIRONMENT_FINGERPRINT_VERSION),
  files: z.record(DigestSchema),
  digest: DigestSchema
}).strict();

export type EnvironmentFingerprint = z.infer<typeof EnvironmentFingerprintSchema>;
export type EnvironmentHomogeneity = "HOMOGENEOUS" | "HETEROGENEOUS" | "EMPTY";

function fileDigest(absolutePath: string): string {
  return `sha256:${createHash("sha256").update(readFileSync(absolutePath)).digest("hex")}`;
}

/**
 * Hash the environment descriptors present in a materialized worktree.
 * Missing files are omitted; the digest covers only what exists.
 */
export function computeEnvironmentFingerprint(worktreeDirectory: string): EnvironmentFingerprint {
  const root = resolve(worktreeDirectory);
  const files: Record<string, string> = {};
  for (const name of ENVIRONMENT_DESCRIPTOR_FILES) {
    const absolute = join(root, name);
    if (!existsSync(absolute)) continue;
    const stat = lstatSync(absolute);
    if (!stat.isFile() || stat.isSymbolicLink()) continue;
    const relativePath = relative(root, absolute).replaceAll("\\", "/");
    files[relativePath] = fileDigest(absolute);
  }
  const sorted = Object.fromEntries(Object.entries(files).sort(([left], [right]) => left.localeCompare(right)));
  const unsigned = {
    schemaVersion: ENVIRONMENT_FINGERPRINT_VERSION,
    files: sorted
  };
  return EnvironmentFingerprintSchema.parse({
    ...unsigned,
    digest: digestJson(unsigned)
  });
}

export function environmentHomogeneity(fingerprints: readonly EnvironmentFingerprint[]): EnvironmentHomogeneity {
  const digests = [...new Set(fingerprints.map((fingerprint) => fingerprint.digest))];
  if (digests.length === 0) return "EMPTY";
  if (digests.length === 1) return "HOMOGENEOUS";
  return "HETEROGENEOUS";
}
