import { createPrivateKey, createPublicKey, sign, verify, type KeyObject } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { isAbsolute, join, parse, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson, sha256 } from "./canonical.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";
import {
  readFrozenWitness,
  verifyFrozenWitness,
  verifyFrozenWitnessRecord,
  type FrozenWitness,
  type WitnessLockVerification
} from "./witness-lock.js";

export const AUTHENTICATED_WITNESS_APPROVAL_SCHEMA_VERSION = "faultline.authenticated-witness-approval.v1";
export const REVIEWER_KEYRING_SCHEMA_VERSION = "faultline.reviewer-keyring.v1";

export const AUTHENTICATED_WITNESS_APPROVAL_LIMITATION = "A verified receipt proves that the holder of a key trusted for the recorded reviewer identity signed this frozen witness approval. It does not prove a legal identity, review quality, CI provenance, or host enforcement.";

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const SAFE_PROPOSAL_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const MAX_APPROVAL_BYTES = 500_000;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const ProposalIdSchema = z.string().regex(SAFE_PROPOSAL_ID, "proposalId must be a safe filename segment");
const ReviewerIdentitySchema = z.string().trim().min(1).max(512).refine((value) => !/[\u0000-\u001f\u007f]/.test(value), "must not contain control characters");
const TimestampSchema = z.string().datetime({ offset: true }).refine((value) => {
  const parsed = new Date(value);
  return Number.isFinite(parsed.getTime()) && parsed.toISOString() === value;
}, "expected a canonical UTC ISO-8601 timestamp");

function isCanonicalBase64(value: string): boolean {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) return false;
  return Buffer.from(value, "base64").toString("base64") === value;
}

const SignatureSchema = z.string().min(1).max(16_384).refine(isCanonicalBase64, "expected canonical base64 signature bytes");

export const ReviewerKeyringSchema = z.object({
  schemaVersion: z.literal(REVIEWER_KEYRING_SCHEMA_VERSION),
  reviewers: z.array(z.object({
    approvedBy: ReviewerIdentitySchema,
    keyId: DigestSchema,
    algorithm: z.literal("ED25519"),
    publicKeyPem: z.string().min(1).max(32_000)
  }).strict()).min(1).max(1_024)
}).strict();

const UnsignedAuthenticatedWitnessApprovalSchema = z.object({
  schemaVersion: z.literal(AUTHENTICATED_WITNESS_APPROVAL_SCHEMA_VERSION),
  recordKind: z.literal("FROZEN_WITNESS_APPROVAL"),
  proposalId: ProposalIdSchema,
  frozenDigest: DigestSchema,
  witnessDigest: DigestSchema,
  approvalDigest: DigestSchema,
  approvedBy: ReviewerIdentitySchema,
  keyId: DigestSchema,
  algorithm: z.literal("ED25519"),
  signedAt: TimestampSchema,
  signatureBase64: SignatureSchema
}).strict();

export const AuthenticatedWitnessApprovalSchema = UnsignedAuthenticatedWitnessApprovalSchema.extend({
  receiptDigest: DigestSchema
}).strict();

export type ReviewerKeyring = z.infer<typeof ReviewerKeyringSchema>;
export type AuthenticatedWitnessApproval = z.infer<typeof AuthenticatedWitnessApprovalSchema>;

export type AuthenticatedWitnessApprovalStatus = "NOT_PRESENT" | "NOT_CHECKED" | "VERIFIED" | "INVALID" | "UNTRUSTED";

export type AuthenticatedWitnessApprovalVerification = {
  readonly valid: boolean;
  readonly errors: readonly string[];
  readonly witness: WitnessLockVerification;
  readonly signatureStatus: AuthenticatedWitnessApprovalStatus;
  readonly receipt?: AuthenticatedWitnessApproval;
  readonly limitation: typeof AUTHENTICATED_WITNESS_APPROVAL_LIMITATION;
};

export type SignAuthenticatedWitnessApprovalInput = {
  readonly privateKeyPem: string;
  readonly keyring: unknown;
  readonly signedAt?: string | Date;
};

export type VerifyAuthenticatedWitnessApprovalOptions = {
  readonly expectedFrozenDigest?: string;
  readonly keyring?: unknown;
  readonly requireSignature?: boolean;
};

function unsignedPayload(receipt: AuthenticatedWitnessApproval | z.infer<typeof UnsignedAuthenticatedWitnessApprovalSchema>): z.infer<typeof UnsignedAuthenticatedWitnessApprovalSchema> {
  const { receiptDigest: _receiptDigest, ...payload } = receipt as AuthenticatedWitnessApproval;
  return UnsignedAuthenticatedWitnessApprovalSchema.parse(payload);
}

function signaturePayload(receipt: AuthenticatedWitnessApproval | z.infer<typeof UnsignedAuthenticatedWitnessApprovalSchema>): Omit<z.infer<typeof UnsignedAuthenticatedWitnessApprovalSchema>, "signatureBase64"> {
  const { signatureBase64: _signatureBase64, ...payload } = unsignedPayload(receipt);
  return payload;
}

function signingBytes(receipt: AuthenticatedWitnessApproval | z.infer<typeof UnsignedAuthenticatedWitnessApprovalSchema>): Buffer {
  return Buffer.from(canonicalJson({
    domain: AUTHENTICATED_WITNESS_APPROVAL_SCHEMA_VERSION,
    payload: signaturePayload(receipt)
  }), "utf8");
}

function canonicalTimestamp(value: string | Date | undefined): string {
  const timestamp = value === undefined ? new Date().toISOString() : value instanceof Date ? value.toISOString() : value;
  return TimestampSchema.parse(timestamp);
}

function publicKeyFingerprint(key: KeyObject): string {
  if (key.asymmetricKeyType !== "ed25519") throw new Error("Reviewer key must use Ed25519.");
  const der = key.export({ type: "spki", format: "der" });
  if (!Buffer.isBuffer(der)) throw new Error("Reviewer public key could not be exported as SPKI DER.");
  return `sha256:${sha256(der)}`;
}

function publicKeyBytes(key: KeyObject): Buffer {
  const der = key.export({ type: "spki", format: "der" });
  if (!Buffer.isBuffer(der)) throw new Error("Reviewer public key could not be exported as SPKI DER.");
  return der;
}

function parseReviewerKeyring(input: unknown): ReviewerKeyring {
  const keyring = ReviewerKeyringSchema.parse(input);
  const seenKeyIds = new Set<string>();
  for (const reviewer of keyring.reviewers) {
    if (seenKeyIds.has(reviewer.keyId)) throw new Error(`Reviewer keyring contains a duplicate keyId: ${reviewer.keyId}`);
    seenKeyIds.add(reviewer.keyId);
    let key: KeyObject;
    try {
      key = createPublicKey(reviewer.publicKeyPem);
    } catch (error) {
      throw new Error(`Reviewer keyring public key ${reviewer.keyId} is invalid: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (key.asymmetricKeyType !== "ed25519") throw new Error(`Reviewer keyring public key ${reviewer.keyId} is not Ed25519.`);
    if (publicKeyFingerprint(key) !== reviewer.keyId) throw new Error(`Reviewer keyring keyId does not match its public key: ${reviewer.keyId}`);
  }
  return keyring;
}

function ensureRealDirectoryTree(directory: string): void {
  const absolute = resolve(directory);
  const parsed = parse(absolute);
  const parts = relative(parsed.root, absolute).split(/[\\/]+/).filter(Boolean);
  let current = parsed.root;
  if (existsSync(current)) {
    const safe = resolveSafeDirectorySegment(current);
    if (safe === null) throw new Error(`Witness approval root must be a real directory: ${current}`);
    current = safe;
  }
  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    const safe = resolveSafeDirectorySegment(current);
    if (safe === null) throw new Error(`Witness approval path cannot traverse a symbolic link or non-directory: ${current}`);
    current = safe;
  }
}

function assertExistingSafeDirectories(root: string, directory: string): void {
  const normalizedRoot = resolve(root);
  const normalizedDirectory = resolve(directory);
  const relativePath = relativeTrustedSystemPath(normalizedRoot, normalizedDirectory);
  if (relativePath.startsWith("..") || isAbsolute(relativePath)) throw new Error("Witness approval path escaped its configured store");
  const parts = relativePath.split(/[\\/]+/).filter(Boolean);
  let current = normalizedRoot;
  if (existsSync(current) && resolveSafeDirectorySegment(current) === null) {
    throw new Error(`Witness approval root is not a real directory: ${current}`);
  }
  for (const part of parts) {
    current = join(current, part);
    if (existsSync(current) && resolveSafeDirectorySegment(current) === null) {
      throw new Error(`Witness approval path cannot traverse a symbolic link or non-directory: ${current}`);
    }
  }
}

function receiptPath(storeDirectory: string, proposalId: string, createDirectories: boolean): string {
  const id = ProposalIdSchema.parse(proposalId);
  const root = resolve(storeDirectory);
  const directory = join(root, "authenticated-approvals");
  if (createDirectories) ensureRealDirectoryTree(directory);
  else assertExistingSafeDirectories(root, directory);
  const path = resolve(directory, `${id}.json`);
  const nested = relativeTrustedSystemPath(root, path);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) throw new Error("Witness approval path escaped its configured store");
  return path;
}

function readReceipt(path: string): AuthenticatedWitnessApproval {
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error("Authenticated witness approval must be a regular non-symlink file");
  if (stat.size > MAX_APPROVAL_BYTES) throw new Error(`Authenticated witness approval exceeds the ${MAX_APPROVAL_BYTES}-byte limit`);
  try {
    return AuthenticatedWitnessApprovalSchema.parse(JSON.parse(readFileSync(path, "utf8")) as unknown);
  } catch (error) {
    throw new Error(`Authenticated witness approval is not valid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function receiptFromFrozen(
  frozen: FrozenWitness,
  keyId: string,
  signedAt: string,
  privateKey: KeyObject
): AuthenticatedWitnessApproval {
  const unsigned = {
    schemaVersion: AUTHENTICATED_WITNESS_APPROVAL_SCHEMA_VERSION,
    recordKind: "FROZEN_WITNESS_APPROVAL" as const,
    proposalId: frozen.proposal.proposalId,
    frozenDigest: frozen.frozenDigest,
    witnessDigest: frozen.witnessDigest,
    approvalDigest: frozen.approval.approvalDigest,
    approvedBy: frozen.approval.approvedBy,
    keyId,
    algorithm: "ED25519" as const,
    signedAt,
    signatureBase64: "placeholder"
  };
  const signatureBase64 = sign(null, Buffer.from(canonicalJson({
    domain: AUTHENTICATED_WITNESS_APPROVAL_SCHEMA_VERSION,
    payload: {
      schemaVersion: unsigned.schemaVersion,
      recordKind: unsigned.recordKind,
      proposalId: unsigned.proposalId,
      frozenDigest: unsigned.frozenDigest,
      witnessDigest: unsigned.witnessDigest,
      approvalDigest: unsigned.approvalDigest,
      approvedBy: unsigned.approvedBy,
      keyId: unsigned.keyId,
      algorithm: unsigned.algorithm,
      signedAt: unsigned.signedAt
    }
  }), "utf8"), privateKey).toString("base64");
  const payload = UnsignedAuthenticatedWitnessApprovalSchema.parse({ ...unsigned, signatureBase64 });
  return AuthenticatedWitnessApprovalSchema.parse({ ...payload, receiptDigest: digestJson(payload) });
}

/**
 * Sign the already-frozen witness with a reviewer-held Ed25519 key. The key is
 * first matched to an externally supplied keyring and is never persisted.
 */
export function signAuthenticatedWitnessApproval(
  storeDirectory: string,
  proposalId: string,
  input: SignAuthenticatedWitnessApprovalInput
): AuthenticatedWitnessApproval {
  const frozenVerification = verifyFrozenWitness(storeDirectory, proposalId);
  if (!frozenVerification.valid) throw new Error(`Cannot sign an invalid frozen witness: ${frozenVerification.errors.join("; ")}`);
  const frozen = readFrozenWitness(storeDirectory, proposalId);
  const currentFrozenVerification = verifyFrozenWitnessRecord(frozen);
  if (!currentFrozenVerification.valid) {
    throw new Error(`Cannot sign a frozen witness that changed during verification: ${currentFrozenVerification.errors.join("; ")}`);
  }
  const keyring = parseReviewerKeyring(input.keyring);
  let privateKey: KeyObject;
  try {
    privateKey = createPrivateKey(input.privateKeyPem);
  } catch (error) {
    throw new Error(`Reviewer private key is invalid: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (privateKey.asymmetricKeyType !== "ed25519") throw new Error("Reviewer private key must use Ed25519.");
  const publicKey = createPublicKey(privateKey);
  const keyId = publicKeyFingerprint(publicKey);
  const configured = keyring.reviewers.find((reviewer) => reviewer.keyId === keyId);
  if (!configured) throw new Error("Reviewer private key is not present in the supplied reviewer keyring.");
  if (configured.approvedBy !== frozen.approval.approvedBy) {
    throw new Error("Reviewer keyring identity does not match the frozen witness approval actor.");
  }
  const configuredPublicKey = createPublicKey(configured.publicKeyPem);
  if (!publicKeyBytes(configuredPublicKey).equals(publicKeyBytes(publicKey))) {
    throw new Error("Reviewer private key does not match the trusted reviewer public key.");
  }
  const receipt = receiptFromFrozen(frozen, keyId, canonicalTimestamp(input.signedAt), privateKey);
  const path = receiptPath(storeDirectory, proposalId, true);
  try {
    writeFileSync(path, `${canonicalJson(receipt)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
  } catch (error) {
    const code = error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
    if (code === "EEXIST") throw new Error(`Authenticated witness approval already exists and cannot be overwritten: ${path}`);
    throw error;
  }
  return receipt;
}

export function readAuthenticatedWitnessApproval(storeDirectory: string, proposalId: string): AuthenticatedWitnessApproval {
  return readReceipt(receiptPath(storeDirectory, proposalId, false));
}

function baseFailure(
  witness: WitnessLockVerification,
  errors: string[],
  signatureStatus: AuthenticatedWitnessApprovalStatus,
  receipt?: AuthenticatedWitnessApproval
): AuthenticatedWitnessApprovalVerification {
  return {
    valid: false,
    errors,
    witness,
    signatureStatus,
    ...(receipt === undefined ? {} : { receipt }),
    limitation: AUTHENTICATED_WITNESS_APPROVAL_LIMITATION
  };
}

/**
 * Verify normal freeze integrity first, then (when present and a keyring is
 * supplied) verify the immutable Ed25519 approval receipt against that trusted
 * keyring. Missing receipts remain an explicitly lower-assurance valid state
 * unless the caller requires a signature.
 */
export function verifyAuthenticatedWitnessApproval(
  storeDirectory: string,
  proposalId: string,
  options: VerifyAuthenticatedWitnessApprovalOptions = {}
): AuthenticatedWitnessApprovalVerification {
  const witness = verifyFrozenWitness(storeDirectory, proposalId, options.expectedFrozenDigest);
  if (!witness.valid) return baseFailure(witness, [...witness.errors], "INVALID");
  if (options.requireSignature && options.keyring === undefined) {
    return baseFailure(witness, ["--require-signature needs a reviewer keyring"], "NOT_CHECKED");
  }
  let path: string;
  try {
    path = receiptPath(storeDirectory, proposalId, false);
  } catch (error) {
    return baseFailure(witness, [`Authenticated witness approval lookup failed safely: ${error instanceof Error ? error.message : String(error)}`], "INVALID");
  }
  if (!existsSync(path)) {
    if (options.requireSignature) return baseFailure(witness, ["authenticated reviewer signature is required but missing"], "NOT_PRESENT");
    return {
      valid: true,
      errors: [],
      witness,
      signatureStatus: "NOT_PRESENT",
      limitation: AUTHENTICATED_WITNESS_APPROVAL_LIMITATION
    };
  }
  let receipt: AuthenticatedWitnessApproval;
  try {
    receipt = readReceipt(path);
  } catch (error) {
    return baseFailure(witness, [`Authenticated witness approval could not be read safely: ${error instanceof Error ? error.message : String(error)}`], "INVALID");
  }
  if (options.keyring === undefined) {
    if (options.requireSignature) return baseFailure(witness, ["authenticated reviewer signature cannot be trusted without a reviewer keyring"], "NOT_CHECKED", receipt);
    return {
      valid: true,
      errors: [],
      witness,
      signatureStatus: "NOT_CHECKED",
      receipt,
      limitation: AUTHENTICATED_WITNESS_APPROVAL_LIMITATION
    };
  }
  const errors: string[] = [];
  let frozen: FrozenWitness;
  let keyring: ReviewerKeyring;
  try {
    frozen = readFrozenWitness(storeDirectory, proposalId);
    const currentFrozenVerification = verifyFrozenWitnessRecord(frozen, options.expectedFrozenDigest);
    if (!currentFrozenVerification.valid) {
      return baseFailure(witness, ["frozen witness changed during authenticated approval verification", ...currentFrozenVerification.errors], "INVALID", receipt);
    }
    keyring = parseReviewerKeyring(options.keyring);
  } catch (error) {
    return baseFailure(witness, [`Authenticated witness approval trust setup failed safely: ${error instanceof Error ? error.message : String(error)}`], "UNTRUSTED", receipt);
  }
  if (receipt.proposalId !== frozen.proposal.proposalId) errors.push("signed proposalId does not match the frozen witness");
  if (receipt.frozenDigest !== frozen.frozenDigest) errors.push("signed frozenDigest does not match the frozen witness");
  if (receipt.witnessDigest !== frozen.witnessDigest) errors.push("signed witnessDigest does not match the frozen witness");
  if (receipt.approvalDigest !== frozen.approval.approvalDigest) errors.push("signed approvalDigest does not match the frozen human approval");
  if (receipt.approvedBy !== frozen.approval.approvedBy) errors.push("signed reviewer identity does not match the frozen human approval");
  if (receipt.receiptDigest !== digestJson(unsignedPayload(receipt))) errors.push("authenticated approval receiptDigest does not match its canonical contents");
  const configured = keyring.reviewers.find((reviewer) => reviewer.keyId === receipt.keyId);
  if (!configured) {
    errors.push("signed reviewer keyId is not present in the trusted keyring");
    return baseFailure(witness, errors, "UNTRUSTED", receipt);
  }
  if (configured.approvedBy !== receipt.approvedBy) {
    errors.push("trusted reviewer key identity does not match the signed reviewer identity");
    return baseFailure(witness, errors, "UNTRUSTED", receipt);
  }
  let publicKey: KeyObject;
  try {
    publicKey = createPublicKey(configured.publicKeyPem);
    if (publicKey.asymmetricKeyType !== "ed25519") throw new Error("trusted public key is not Ed25519");
    if (publicKeyFingerprint(publicKey) !== receipt.keyId) throw new Error("trusted public-key fingerprint does not match the signed keyId");
  } catch (error) {
    errors.push(`trusted reviewer public key is invalid: ${error instanceof Error ? error.message : String(error)}`);
    return baseFailure(witness, errors, "UNTRUSTED", receipt);
  }
  try {
    const signature = Buffer.from(receipt.signatureBase64, "base64");
    if (!verify(null, signingBytes(receipt), publicKey, signature)) errors.push("Ed25519 signature does not verify against the trusted reviewer key");
  } catch (error) {
    errors.push(`Ed25519 signature verification failed safely: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (errors.length > 0) return baseFailure(witness, errors, "INVALID", receipt);
  return {
    valid: true,
    errors: [],
    witness,
    signatureStatus: "VERIFIED",
    receipt,
    limitation: AUTHENTICATED_WITNESS_APPROVAL_LIMITATION
  };
}
