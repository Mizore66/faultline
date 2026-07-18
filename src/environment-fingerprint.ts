import { createHash } from "node:crypto";
import { existsSync, lstatSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { z } from "zod";
import { digestJson } from "./canonical.js";

/** v2 expands descriptors beyond lockfiles (manifests / tool pins / compose). */
export const ENVIRONMENT_FINGERPRINT_VERSION = "faultline.environment-fingerprint.v2" as const;

export const ENVIRONMENT_CHANGED_PROOF_MESSAGE =
  "Environment changed between the passing and failing states. Supply a runtime mapping for each fingerprint before proof can continue." as const;

/**
 * Descriptors that change the execution environment for a historical state.
 * Manifests are included because scripts/tool requirements can change without
 * a lockfile update.
 */
export const ENVIRONMENT_DESCRIPTOR_FILES = Object.freeze([
  "package.json",
  "package-lock.json",
  "pnpm-lock.yaml",
  "yarn.lock",
  "bun.lock",
  "bun.lockb",
  "requirements.txt",
  "pyproject.toml",
  "poetry.lock",
  "uv.lock",
  "Pipfile.lock",
  "go.sum",
  "go.mod",
  "Cargo.toml",
  "Cargo.lock",
  "rust-toolchain.toml",
  ".nvmrc",
  ".node-version",
  ".python-version",
  ".tool-versions",
  "Dockerfile",
  "Dockerfile.faultline",
  "docker-compose.yml",
  "docker-compose.yaml",
  "compose.yml",
  "compose.yaml"
] as const);

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);
const DigestPinnedImageSchema = z.string().regex(
  /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/,
  "expected a digest-pinned image reference"
);

export const EnvironmentFingerprintSchema = z.object({
  schemaVersion: z.literal(ENVIRONMENT_FINGERPRINT_VERSION),
  files: z.record(DigestSchema),
  digest: DigestSchema
}).strict();

export type EnvironmentFingerprint = z.infer<typeof EnvironmentFingerprintSchema>;
export type EnvironmentHomogeneity = "HOMOGENEOUS" | "HETEROGENEOUS" | "EMPTY";

export type RuntimeMapping = Readonly<Record<string, string>>;

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

export function distinctFingerprintDigests(fingerprints: readonly EnvironmentFingerprint[]): string[] {
  return [...new Set(fingerprints.map((fingerprint) => fingerprint.digest))].sort((left, right) => left.localeCompare(right));
}

/** Digests that appear in the investigation but have no mapping entry. */
export function missingRuntimeMappingDigests(
  fingerprints: readonly EnvironmentFingerprint[],
  runtimeMapping: RuntimeMapping
): string[] {
  return distinctFingerprintDigests(fingerprints).filter((digest) => {
    const image = runtimeMapping[digest];
    return image === undefined || image.length === 0;
  });
}

/**
 * Resolve the digest-pinned image for one fingerprint.
 * Heterogeneous environments require an explicit mapping entry (no default fallback).
 * Homogeneous/EMPTY may use mapping[digest] ?? defaultImage.
 */
export function resolveRuntimeImageForFingerprint(options: {
  readonly fingerprintDigest: string;
  readonly homogeneity: EnvironmentHomogeneity;
  readonly runtimeMapping: RuntimeMapping;
  readonly defaultImage?: string;
}): string {
  const mapped = options.runtimeMapping[options.fingerprintDigest];
  if (mapped !== undefined && mapped.length > 0) {
    return DigestPinnedImageSchema.parse(mapped);
  }
  if (options.homogeneity === "HETEROGENEOUS") {
    throw new Error(ENVIRONMENT_CHANGED_PROOF_MESSAGE);
  }
  if (options.defaultImage === undefined || options.defaultImage.length === 0) {
    throw new Error("No digest-pinned runtime image is available for this environment fingerprint.");
  }
  return DigestPinnedImageSchema.parse(options.defaultImage);
}

/** Effective fingerprint → image map actually used for an investigation. */
export function buildEffectiveRuntimeMapping(
  fingerprints: readonly EnvironmentFingerprint[],
  runtimeMapping: RuntimeMapping,
  defaultImage: string | undefined,
  homogeneity: EnvironmentHomogeneity
): Record<string, string> {
  const effective: Record<string, string> = {};
  for (const digest of distinctFingerprintDigests(fingerprints)) {
    effective[digest] = resolveRuntimeImageForFingerprint({
      fingerprintDigest: digest,
      homogeneity,
      runtimeMapping,
      ...(defaultImage === undefined ? {} : { defaultImage })
    });
  }
  return effective;
}
