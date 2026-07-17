import {
  closeSync,
  existsSync,
  fsyncSync,
  linkSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  unlinkSync,
  writeFileSync
} from "node:fs";
import { isAbsolute, join, parse, relative, resolve } from "node:path";
import { randomUUID } from "node:crypto";
import { z } from "zod";
import { canonicalJson, digestJson } from "./canonical.js";
<<<<<<< Updated upstream
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";
=======
import { resolveSafeDirectorySegment } from "./safe-directory.js";
>>>>>>> Stashed changes

/**
 * A bundle attestation is deliberately not a signature. It gives a reviewer a
 * small, separately stored, write-once record to compare with an externally
 * retained receipt digest. Without that external digest, an editor who can
 * rewrite both files can also rewrite this record.
 */
export const BUNDLE_ATTESTATION_SCHEMA_VERSION = "faultline.bundle-attestation.v1";
export const INTEGRITY_ATTESTATION_LIMITATION = "Integrity attestation only; not a cryptographic signature, proof of authorship, identity verification, provenance verification, or ownership claim.";

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const RECEIPT_ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const CANONICAL_TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

const DigestSchema = z.string().regex(SHA256_DIGEST, "must be a sha256:<64-lowercase-hex> digest");
const ReceiptIdSchema = z.string().regex(RECEIPT_ID, "must use only letters, numbers, underscores, or hyphens and cannot be a path");
const IdentifierSchema = z.string()
  .min(1)
  .max(512)
  .refine((value) => value === value.trim(), "cannot start or end with whitespace")
  .refine((value) => !/[\u0000-\u001f\u007f]/.test(value), "cannot contain control characters");
const TimestampSchema = z.string()
  .regex(CANONICAL_TIMESTAMP, "must be a canonical UTC ISO-8601 timestamp")
  .refine((value) => {
    const parsed = new Date(value);
    return Number.isFinite(parsed.getTime()) && parsed.toISOString() === value;
  }, "must be a valid canonical UTC ISO-8601 timestamp");

const UnsignedBundleAttestationSchema = z.object({
  schemaVersion: z.literal(BUNDLE_ATTESTATION_SCHEMA_VERSION),
  attestationKind: z.literal("BUNDLE_INTEGRITY_ATTESTATION"),
  limitation: z.literal(INTEGRITY_ATTESTATION_LIMITATION),
  receiptId: ReceiptIdSchema,
  subject: IdentifierSchema,
  issuer: IdentifierSchema,
  issuedAt: TimestampSchema,
  bundleRootDigest: DigestSchema,
  sourceDigest: DigestSchema.optional(),
  gitCheckpointDigest: DigestSchema.optional(),
  witnessDigest: DigestSchema.optional()
}).strict();

const BundleAttestationSchema = UnsignedBundleAttestationSchema.extend({
  receiptDigest: DigestSchema
}).strict();

const CreationInputSchema = z.object({
  receiptId: ReceiptIdSchema,
  subject: IdentifierSchema,
  issuer: IdentifierSchema,
  issuedAt: z.union([z.string(), z.date()]).optional(),
  bundleRootDigest: DigestSchema,
  sourceDigest: DigestSchema.optional(),
  gitCheckpointDigest: DigestSchema.optional(),
  witnessDigest: DigestSchema.optional()
}).strict();

export type BundleAttestation = z.infer<typeof BundleAttestationSchema>;
export type BundleAttestationInput = z.input<typeof CreationInputSchema>;
export type UnsignedBundleAttestation = z.infer<typeof UnsignedBundleAttestationSchema>;

export type ExternalAttestationDigestStatus = "NOT_PROVIDED" | "MATCH" | "MISMATCH";

export interface BundleAttestationVerification {
  readonly valid: boolean;
  readonly errors: readonly string[];
  /** The digest calculated from the received record, never merely its stored field. */
  readonly receiptDigest: string | null;
  readonly externalDigestStatus: ExternalAttestationDigestStatus;
  readonly attestation?: BundleAttestation;
}

export interface AttestationStoreOptions {
  /**
   * Override only for a controlled embedding or isolated test root. Production
   * callers should retain the default `.faultline/attestations` root.
   */
  readonly attestationRoot?: string;
}

export interface WrittenBundleAttestation {
  readonly directory: string;
  readonly path: string;
  readonly attestation: BundleAttestation;
  readonly receiptDigest: string;
}

export function defaultAttestationStore(): string {
  return resolve(".faultline", "attestations");
}

function canonicalTimestamp(value: string | Date | undefined): string {
  const date = value === undefined ? new Date() : value instanceof Date ? value : new Date(value);
  if (!Number.isFinite(date.getTime())) {
    throw new Error("Attestation issuedAt must be a valid date or ISO-8601 timestamp.");
  }
  return date.toISOString();
}

function isDescendantOrSame(root: string, candidate: string): boolean {
  const pathFromRoot = relativeTrustedSystemPath(root, candidate);
  return pathFromRoot === "" || (!pathFromRoot.startsWith("..") && !isAbsolute(pathFromRoot));
}

/**
 * Walk each existing path component with lstat, never allowing a symlink (or
 * special file) to become a directory traversal point. Missing components are
 * created one at a time only after their parent has been checked.
 */
function ensureRealDirectory(directory: string, create: boolean): void {
  const absolute = resolve(directory);
  const parsed = parse(absolute);
  const suffix = relative(parsed.root, absolute);
  const parts = suffix ? suffix.split(/[\\/]+/).filter(Boolean) : [];
  let current = parsed.root;

  if (existsSync(current)) {
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) {
      throw new Error(`Attestation path root must be a real directory: ${current}`);
    }
    current = safeCurrent;
  }

  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) {
      if (!create) throw new Error(`Attestation store does not exist: ${absolute}`);
      mkdirSync(current);
    }
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) {
      throw new Error(`Attestation store cannot traverse a symbolic link or non-directory: ${current}`);
    }
    current = safeCurrent;
  }
}

function safeStoreDirectory(storeDirectory: string, attestationRoot: string, create: boolean): string {
  if (!storeDirectory || !storeDirectory.trim()) {
    throw new Error("Attestation store directory must be non-empty.");
  }
  const root = resolve(attestationRoot);
  const store = resolve(storeDirectory);
  if (!isDescendantOrSame(root, store)) {
    throw new Error(`Attestation store must be inside the managed root: ${root}`);
  }
  ensureRealDirectory(root, create);
  ensureRealDirectory(store, create);
  return store;
}

/**
 * Validate (and create when missing) a managed receipt store. The store is
 * constrained to the FaultLine attestation root and may not include symlinks.
 */
export function assertSafeAttestationStore(storeDirectory: string, attestationRoot = defaultAttestationStore()): string {
  return safeStoreDirectory(storeDirectory, attestationRoot, true);
}

function receiptPath(storeDirectory: string, receiptId: string): string {
  const safeId = ReceiptIdSchema.parse(receiptId);
  const path = join(storeDirectory, `${safeId}.json`);
  if (!isDescendantOrSame(storeDirectory, path) || relative(storeDirectory, path).includes("..")) {
    throw new Error("Attestation receipt path escaped its managed store.");
  }
  return path;
}

function unsignedRecord(attestation: BundleAttestation | UnsignedBundleAttestation): UnsignedBundleAttestation {
  const { receiptDigest: _receiptDigest, ...record } = attestation as BundleAttestation;
  return UnsignedBundleAttestationSchema.parse(record);
}

/** Canonically hash every integrity-relevant attestation field except the checksum itself. */
export function bundleAttestationDigest(attestation: BundleAttestation | UnsignedBundleAttestation): string {
  return digestJson(unsignedRecord(attestation));
}

/**
 * Construct a canonical receipt. `issuer` is an unverified label; this method
 * intentionally does not and cannot make an authorship or signature claim.
 */
export function createBundleAttestation(input: BundleAttestationInput): BundleAttestation {
  const parsed = CreationInputSchema.parse(input);
  const base: UnsignedBundleAttestation = {
    schemaVersion: BUNDLE_ATTESTATION_SCHEMA_VERSION,
    attestationKind: "BUNDLE_INTEGRITY_ATTESTATION",
    limitation: INTEGRITY_ATTESTATION_LIMITATION,
    receiptId: parsed.receiptId,
    subject: parsed.subject,
    issuer: parsed.issuer,
    issuedAt: canonicalTimestamp(parsed.issuedAt),
    bundleRootDigest: parsed.bundleRootDigest,
    ...(parsed.sourceDigest === undefined ? {} : { sourceDigest: parsed.sourceDigest }),
    ...(parsed.gitCheckpointDigest === undefined ? {} : { gitCheckpointDigest: parsed.gitCheckpointDigest }),
    ...(parsed.witnessDigest === undefined ? {} : { witnessDigest: parsed.witnessDigest })
  };
  const unsigned = UnsignedBundleAttestationSchema.parse(base);
  return BundleAttestationSchema.parse({ ...unsigned, receiptDigest: digestJson(unsigned) });
}

function externalDigestStatus(expectedReceiptDigest: string | undefined, errors: string[]): { status: ExternalAttestationDigestStatus; expected?: string } {
  if (expectedReceiptDigest === undefined) return { status: "NOT_PROVIDED" };
  const parsed = DigestSchema.safeParse(expectedReceiptDigest);
  if (!parsed.success) {
    errors.push("externally supplied receipt digest is not a sha256 digest");
    return { status: "MISMATCH" };
  }
  return { status: "MISMATCH", expected: parsed.data };
}

/**
 * Verify an in-memory receipt. Passing an independently retained expected
 * receipt digest is what detects an editor who recomputes the mutable checksum.
 */
export function verifyBundleAttestation(attestation: unknown, expectedReceiptDigest?: string): BundleAttestationVerification {
  const errors: string[] = [];
  const external = externalDigestStatus(expectedReceiptDigest, errors);
  const parsed = BundleAttestationSchema.safeParse(attestation);
  if (!parsed.success) {
    errors.push(`attestation schema validation failed: ${parsed.error.message}`);
    return { valid: false, errors, receiptDigest: null, externalDigestStatus: external.status };
  }

  const calculatedDigest = bundleAttestationDigest(parsed.data);
  if (parsed.data.receiptDigest !== calculatedDigest) {
    errors.push("receiptDigest does not match the canonical receipt contents");
  }
  const status: ExternalAttestationDigestStatus = external.expected === undefined
    ? external.status
    : external.expected === calculatedDigest ? "MATCH" : "MISMATCH";
  if (status === "MISMATCH" && expectedReceiptDigest !== undefined) {
    errors.push("externally supplied receipt digest does not match");
  }
  return {
    valid: errors.length === 0,
    errors,
    receiptDigest: calculatedDigest,
    externalDigestStatus: status,
    attestation: parsed.data
  };
}

function writeNewReceipt(store: string, destination: string, body: string): void {
  const temporary = join(store, `.attestation-${randomUUID()}.tmp`);
  let descriptor: number | undefined;
  try {
    descriptor = openSync(temporary, "wx", 0o600);
    writeFileSync(descriptor, body, "utf8");
    fsyncSync(descriptor);
    closeSync(descriptor);
    descriptor = undefined;

    const temporaryStat = lstatSync(temporary);
    if (temporaryStat.isSymbolicLink() || !temporaryStat.isFile()) {
      throw new Error("Attestation staging file is not a regular file.");
    }
    // link(2) is no-clobber: unlike rename, it cannot replace an existing
    // receipt. The completed file becomes visible atomically before the temp
    // link is removed, preserving write-once semantics even under a race.
    linkSync(temporary, destination);
  } catch (error) {
    const code = error && typeof error === "object" && "code" in error ? (error as { code?: unknown }).code : undefined;
    if (code === "EEXIST") {
      throw new Error(`Attestation receipt already exists and cannot be overwritten: ${destination}`);
    }
    throw error;
  } finally {
    if (descriptor !== undefined) closeSync(descriptor);
    if (existsSync(temporary)) unlinkSync(temporary);
  }
}

/**
 * Persist a separately stored, write-once integrity receipt. It is kept out of
 * the proof bundle on purpose, so its digest can be retained by another system.
 */
export function writeBundleAttestation(
  storeDirectory: string,
  input: BundleAttestationInput,
  options: AttestationStoreOptions = {}
): WrittenBundleAttestation {
  const store = assertSafeAttestationStore(storeDirectory, options.attestationRoot ?? defaultAttestationStore());
  const attestation = createBundleAttestation(input);
  const destination = receiptPath(store, attestation.receiptId);
  // Re-check after construction immediately before the final no-clobber link.
  safeStoreDirectory(store, options.attestationRoot ?? defaultAttestationStore(), false);
  writeNewReceipt(store, destination, `${canonicalJson(attestation)}\n`);
  return { directory: store, path: destination, attestation, receiptDigest: attestation.receiptDigest };
}

function failedVerification(errors: string[], expectedReceiptDigest?: string): BundleAttestationVerification {
  const external = externalDigestStatus(expectedReceiptDigest, errors);
  return { valid: false, errors, receiptDigest: null, externalDigestStatus: external.status };
}

/**
 * Read a receipt without following links and validate both its canonical
 * checksum and, when supplied, an independently retained external digest.
 */
export function verifyStoredBundleAttestation(
  storeDirectory: string,
  receiptId: string,
  expectedReceiptDigest?: string,
  options: AttestationStoreOptions = {}
): BundleAttestationVerification {
  try {
    const store = safeStoreDirectory(storeDirectory, options.attestationRoot ?? defaultAttestationStore(), false);
    const path = receiptPath(store, receiptId);
    if (!existsSync(path)) {
      return failedVerification(["attestation receipt does not exist"], expectedReceiptDigest);
    }
    const stat = lstatSync(path);
    if (stat.isSymbolicLink() || !stat.isFile()) {
      return failedVerification(["attestation receipt is a symbolic link or non-regular file"], expectedReceiptDigest);
    }
    const parsed = JSON.parse(readFileSync(path, "utf8")) as unknown;
    return verifyBundleAttestation(parsed, expectedReceiptDigest);
  } catch (error) {
    return failedVerification([
      `attestation verification failed safely: ${error instanceof Error ? error.message : String(error)}`
    ], expectedReceiptDigest);
  }
}

/** Throw on any invalid or externally mismatched receipt; useful for callers that need the parsed record. */
export function readVerifiedBundleAttestation(
  storeDirectory: string,
  receiptId: string,
  expectedReceiptDigest?: string,
  options: AttestationStoreOptions = {}
): BundleAttestation {
  const verification = verifyStoredBundleAttestation(storeDirectory, receiptId, expectedReceiptDigest, options);
  if (!verification.valid || !verification.attestation) {
    throw new Error(`Attestation receipt is invalid: ${verification.errors.join("; ")}`);
  }
  return verification.attestation;
}
