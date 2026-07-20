import { writeFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { z } from "zod";
import { digestJson } from "./canonical.js";

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);
const DigestPinnedImageSchema = z.string().regex(
  /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/,
  "expected a digest-pinned image reference"
);

export const RUNTIME_MAPPING_FILE_SCHEMA_VERSION = "faultline.runtime-mapping.v1" as const;

export type RuntimeMappingPair = {
  readonly fingerprintDigest: string;
  readonly image: string;
};

export type WrittenRuntimeMapping = {
  readonly path: string;
  readonly mapping: Readonly<Record<string, string>>;
  readonly provenanceDigest: string;
  readonly generatedAt: string;
};

/**
 * Write a provenance-bearing runtime mapping file for heterogeneous fingerprints.
 * Mapping values are digest-pinned images only.
 */
export function writeRuntimeMappingFile(options: {
  readonly pairs: readonly RuntimeMappingPair[];
  readonly outputPath: string;
  readonly generatedAt?: string;
  readonly source?: string;
}): WrittenRuntimeMapping {
  if (options.pairs.length === 0) {
    throw new Error("runtime mapping write requires at least one --fingerprint / --image pair");
  }
  const mapping: Record<string, string> = {};
  for (const pair of options.pairs) {
    const fingerprint = DigestSchema.parse(pair.fingerprintDigest);
    const image = DigestPinnedImageSchema.parse(pair.image);
    if (mapping[fingerprint] !== undefined && mapping[fingerprint] !== image) {
      throw new Error(`conflicting runtime mapping for ${fingerprint}`);
    }
    mapping[fingerprint] = image;
  }
  const generatedAt = options.generatedAt ?? new Date().toISOString();
  const unsigned = {
    schemaVersion: RUNTIME_MAPPING_FILE_SCHEMA_VERSION,
    generatedAt,
    source: options.source ?? "fl runtime mapping write",
    mapping
  };
  const provenanceDigest = digestJson(unsigned);
  const document = {
    ...unsigned,
    provenanceDigest
  };
  const path = resolve(options.outputPath);
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(document, null, 2)}\n`, "utf8");
  return { path, mapping, provenanceDigest, generatedAt };
}

/** Extract fingerprint→image map from a mapping file (with or without provenance wrapper). */
export function loadRuntimeMappingDocument(payload: unknown): Readonly<Record<string, string>> {
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
    throw new Error("runtime mapping must be a JSON object");
  }
  const record = payload as Record<string, unknown>;
  const mappingPayload = record.mapping !== undefined && typeof record.mapping === "object" && record.mapping !== null && !Array.isArray(record.mapping)
    ? record.mapping as Record<string, unknown>
    : record;
  const mapping: Record<string, string> = {};
  for (const [digest, image] of Object.entries(mappingPayload)) {
    if (digest === "schemaVersion" || digest === "generatedAt" || digest === "source" || digest === "provenanceDigest") {
      continue;
    }
    if (typeof image !== "string") {
      throw new Error(`runtime mapping entry for ${digest} must be a digest-pinned image string`);
    }
    DigestSchema.parse(digest);
    DigestPinnedImageSchema.parse(image);
    mapping[digest] = image;
  }
  return mapping;
}
