import { existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson, sha256 } from "./canonical.js";

const MAX_OVERLAY_BYTES_BASE64 = 1_400_000;
const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const SAFE_PROPOSAL_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const TimestampSchema = z.string().datetime({ offset: true });
const ProposalIdSchema = z.string().regex(SAFE_PROPOSAL_ID, "proposalId must be a safe filename segment");

function isSafeOverlayPath(value: string): boolean {
  if (!value || value.startsWith("/") || value.includes("\\") || value.includes("\0")) return false;
  const parts = value.split("/");
  return parts.every((part) => part.length > 0 && part !== "." && part !== "..");
}

function isCanonicalBase64(value: string): boolean {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) return false;
  return Buffer.from(value, "base64").toString("base64") === value;
}

const OverlayPathSchema = z.string().min(1).max(512).refine(isSafeOverlayPath, "overlay path must be a safe POSIX-relative path");
const Base64BytesSchema = z.string().max(MAX_OVERLAY_BYTES_BASE64).refine(isCanonicalBase64, "overlay bytes must use canonical base64");

/**
 * The proposal boundary deliberately accepts only incident symptom material.
 * It is strict so candidate turns, diffs, and localization material cannot
 * silently become part of the proposal protocol.
 */
export const IncidentPacketSchema = z.object({
  symptom: z.string().min(1).max(32_000),
  ciLog: z.string().min(1).max(500_000),
  repositoryLanguage: z.string().min(1).max(200),
  repositorySummary: z.string().min(1).max(8_000)
}).strict();

export const WitnessProposalInputSchema = z.object({
  proposalId: ProposalIdSchema,
  incidentPacket: IncidentPacketSchema,
  proposalOrigin: z.enum(["HUMAN", "MODEL"]).optional(),
  proposedAt: TimestampSchema.optional(),
  witness: z.object({
    behavior: z.string().min(1).max(16_000),
    command: z.string().min(1).max(32_000),
    overlays: z.array(z.object({
      path: OverlayPathSchema,
      bytesBase64: Base64BytesSchema
    }).strict()).max(64),
    policy: z.object({
      network: z.literal("disabled"),
      credentials: z.literal("redacted"),
      timeoutSeconds: z.number().int().min(1).max(3_600)
    }).strict()
  }).strict()
}).strict();

const HashedOverlaySchema = z.object({
  path: OverlayPathSchema,
  bytesBase64: Base64BytesSchema,
  bytesDigest: DigestSchema
}).strict();

const LockedWitnessSchema = z.object({
  behavior: z.string().min(1).max(16_000),
  command: z.string().min(1).max(32_000),
  commandDigest: DigestSchema,
  overlays: z.array(HashedOverlaySchema).max(64),
  overlayDigest: DigestSchema,
  policy: z.object({
    network: z.literal("disabled"),
    credentials: z.literal("redacted"),
    timeoutSeconds: z.number().int().min(1).max(3_600)
  }).strict()
}).strict();

export const WitnessProposalSchema = z.object({
  schemaVersion: z.literal("faultline.witness-proposal.v1"),
  proposalId: ProposalIdSchema,
  proposedAt: TimestampSchema,
  proposalOrigin: z.enum(["HUMAN", "MODEL"]),
  incidentPacket: IncidentPacketSchema,
  incidentPacketDigest: DigestSchema,
  witness: LockedWitnessSchema,
  proposalDigest: DigestSchema
}).strict();

export const HumanApprovalInputSchema = z.object({
  approvedBy: z.string().trim().min(1).max(512),
  approvedAt: TimestampSchema.optional(),
  note: z.string().max(8_000).optional()
}).strict();

export const WitnessApprovalSchema = z.object({
  schemaVersion: z.literal("faultline.witness-approval.v1"),
  proposalId: ProposalIdSchema,
  proposalDigest: DigestSchema,
  incidentPacketDigest: DigestSchema,
  reviewedCommandDigest: DigestSchema,
  reviewedOverlayDigest: DigestSchema,
  reviewerType: z.literal("HUMAN"),
  approvedBy: z.string().trim().min(1).max(512),
  approvedAt: TimestampSchema,
  note: z.string().max(8_000).optional(),
  approvalDigest: DigestSchema
}).strict();

export const FrozenWitnessSchema = z.object({
  schemaVersion: z.literal("faultline.frozen-witness.v1"),
  frozenAt: TimestampSchema,
  proposal: WitnessProposalSchema,
  approval: WitnessApprovalSchema,
  witnessDigest: DigestSchema,
  frozenDigest: DigestSchema
}).strict();

const FreezeOptionsSchema = z.object({
  frozenAt: TimestampSchema.optional()
}).strict();

export type IncidentPacket = z.infer<typeof IncidentPacketSchema>;
export type WitnessProposal = z.infer<typeof WitnessProposalSchema>;
export type WitnessApproval = z.infer<typeof WitnessApprovalSchema>;
export type FrozenWitness = z.infer<typeof FrozenWitnessSchema>;

export type WitnessLockVerification = {
  valid: boolean;
  errors: string[];
  proposalDigest: string | null;
  frozenDigest: string | null;
  approval: { actor: string; approvedAt: string } | null;
  externalDigestStatus: "NOT_PROVIDED" | "MATCH" | "MISMATCH";
};

type StorePaths = {
  proposal: string;
  approval: string;
  frozen: string;
};

function byteDigest(value: string | Buffer): string {
  return `sha256:${sha256(value)}`;
}

function commandDigest(command: string): string {
  return byteDigest(Buffer.from(command, "utf8"));
}

function overlayDigest(overlays: Array<z.infer<typeof HashedOverlaySchema>>): string {
  return digestJson(overlays.map((overlay) => ({ path: overlay.path, bytesDigest: overlay.bytesDigest })));
}

function proposalPayload(proposal: WitnessProposal): Omit<WitnessProposal, "proposalDigest"> {
  const { proposalDigest: _proposalDigest, ...payload } = proposal;
  return payload;
}

function approvalPayload(approval: WitnessApproval): Omit<WitnessApproval, "approvalDigest"> {
  const { approvalDigest: _approvalDigest, ...payload } = approval;
  return payload;
}

function frozenPayload(frozen: FrozenWitness): Omit<FrozenWitness, "frozenDigest"> {
  const { frozenDigest: _frozenDigest, ...payload } = frozen;
  return payload;
}

function witnessPayload(proposal: WitnessProposal): Record<string, unknown> {
  return {
    schemaVersion: "faultline.locked-witness.v1",
    proposalId: proposal.proposalId,
    proposalDigest: proposal.proposalDigest,
    incidentPacketDigest: proposal.incidentPacketDigest,
    behavior: proposal.witness.behavior,
    command: proposal.witness.command,
    commandDigest: proposal.witness.commandDigest,
    overlays: proposal.witness.overlays,
    overlayDigest: proposal.witness.overlayDigest,
    policy: proposal.witness.policy
  };
}

function assertUniqueOverlayPaths(overlays: Array<{ path: string }>): void {
  const seen = new Set<string>();
  for (const overlay of overlays) {
    // Overlay content can eventually be materialized on case-insensitive hosts.
    // Rejecting aliases now keeps the frozen contract unambiguous everywhere.
    const key = overlay.path.toLocaleLowerCase("en-US");
    if (seen.has(key)) throw new Error(`Duplicate overlay path: ${overlay.path}`);
    seen.add(key);
  }
}

function assertSortedOverlayPaths(overlays: Array<{ path: string }>, errors: string[]): void {
  assertUniqueOverlayPathsForVerification(overlays, errors);
  for (let index = 1; index < overlays.length; index += 1) {
    const previous = overlays[index - 1];
    const current = overlays[index];
    if (previous && current && previous.path.localeCompare(current.path) > 0) {
      errors.push("overlay records are not in canonical path order");
      return;
    }
  }
}

function assertUniqueOverlayPathsForVerification(overlays: Array<{ path: string }>, errors: string[]): void {
  const seen = new Set<string>();
  for (const overlay of overlays) {
    const key = overlay.path.toLocaleLowerCase("en-US");
    if (seen.has(key)) errors.push(`duplicate overlay path: ${overlay.path}`);
    seen.add(key);
  }
}

function buildProposal(input: unknown): WitnessProposal {
  const parsed = WitnessProposalInputSchema.parse(input);
  assertUniqueOverlayPaths(parsed.witness.overlays);
  const overlays = parsed.witness.overlays
    .map((overlay) => ({
      path: overlay.path,
      bytesBase64: overlay.bytesBase64,
      bytesDigest: byteDigest(Buffer.from(overlay.bytesBase64, "base64"))
    }))
    .sort((left, right) => left.path.localeCompare(right.path));
  const unsigned = {
    schemaVersion: "faultline.witness-proposal.v1" as const,
    proposalId: parsed.proposalId,
    proposedAt: parsed.proposedAt ?? new Date().toISOString(),
    proposalOrigin: parsed.proposalOrigin ?? "HUMAN",
    incidentPacket: parsed.incidentPacket,
    incidentPacketDigest: digestJson(parsed.incidentPacket),
    witness: {
      behavior: parsed.witness.behavior,
      command: parsed.witness.command,
      commandDigest: commandDigest(parsed.witness.command),
      overlays,
      overlayDigest: overlayDigest(overlays),
      policy: parsed.witness.policy
    }
  };
  return { ...unsigned, proposalDigest: digestJson(unsigned) };
}

function assertDirectoryIsSafe(directory: string): void {
  const stat = lstatSync(directory);
  if (!stat.isDirectory() || stat.isSymbolicLink()) {
    throw new Error(`Witness lock directory must be a non-symlink directory: ${directory}`);
  }
}

function ensureDirectoryIsSafe(directory: string): void {
  if (!existsSync(directory)) mkdirSync(directory, { recursive: true, mode: 0o700 });
  assertDirectoryIsSafe(directory);
}

function storePaths(storeDirectory: string, proposalId: string, createDirectories: boolean): StorePaths {
  const id = ProposalIdSchema.parse(proposalId);
  const root = resolve(storeDirectory);
  const directories = [root, join(root, "proposals"), join(root, "approvals"), join(root, "frozen")];
  if (createDirectories) {
    for (const directory of directories) ensureDirectoryIsSafe(directory);
  } else {
    // A verifier must not follow an attacker-controlled store subdirectory
    // through a link, even though it is not creating any records itself.
    for (const directory of directories) {
      if (existsSync(directory)) assertDirectoryIsSafe(directory);
    }
  }
  const paths = {
    proposal: resolve(root, "proposals", `${id}.json`),
    approval: resolve(root, "approvals", `${id}.json`),
    frozen: resolve(root, "frozen", `${id}.json`)
  };
  for (const path of Object.values(paths)) {
    const nested = relative(root, path);
    if (!nested || nested.startsWith("..") || nested.includes(":")) {
      throw new Error("Witness lock path escaped its configured store");
    }
  }
  return paths;
}

function writeOnceJson(path: string, value: unknown): void {
  try {
    writeFileSync(path, `${canonicalJson(value)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
  } catch (error) {
    const code = error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
    if (code === "EEXIST") throw new Error(`Immutable witness lock record already exists: ${path}`);
    throw error;
  }
}

function readJson(path: string, label: string): unknown {
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`${label} must be a regular non-symlink file`);
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    throw new Error(`${label} is not valid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function readProposal(path: string): WitnessProposal {
  return WitnessProposalSchema.parse(readJson(path, "Witness proposal"));
}

function readApproval(path: string): WitnessApproval {
  return WitnessApprovalSchema.parse(readJson(path, "Witness approval"));
}

function readFrozen(path: string): FrozenWitness {
  return FrozenWitnessSchema.parse(readJson(path, "Frozen witness"));
}

function verifyProposal(proposal: WitnessProposal, errors: string[]): void {
  if (proposal.incidentPacketDigest !== digestJson(proposal.incidentPacket)) {
    errors.push("incident packet digest does not match the persisted packet");
  }
  if (proposal.witness.commandDigest !== commandDigest(proposal.witness.command)) {
    errors.push("command digest does not match the exact UTF-8 command bytes");
  }
  assertSortedOverlayPaths(proposal.witness.overlays, errors);
  for (const overlay of proposal.witness.overlays) {
    const bytesDigest = byteDigest(Buffer.from(overlay.bytesBase64, "base64"));
    if (overlay.bytesDigest !== bytesDigest) {
      errors.push(`overlay byte digest does not match: ${overlay.path}`);
    }
  }
  if (proposal.witness.overlayDigest !== overlayDigest(proposal.witness.overlays)) {
    errors.push("overlay digest does not match the ordered overlay byte digests");
  }
  if (proposal.proposalDigest !== digestJson(proposalPayload(proposal))) {
    errors.push("proposal digest does not match the immutable proposal payload");
  }
}

function verifyApproval(approval: WitnessApproval, proposal: WitnessProposal, errors: string[]): void {
  if (approval.proposalId !== proposal.proposalId) errors.push("approval proposalId does not match proposal");
  if (approval.proposalDigest !== proposal.proposalDigest) errors.push("approval proposal digest does not match proposal");
  if (approval.incidentPacketDigest !== proposal.incidentPacketDigest) errors.push("approval incident packet digest does not match proposal");
  if (approval.reviewedCommandDigest !== proposal.witness.commandDigest) errors.push("approval command digest does not match proposal");
  if (approval.reviewedOverlayDigest !== proposal.witness.overlayDigest) errors.push("approval overlay digest does not match proposal");
  if (approval.approvalDigest !== digestJson(approvalPayload(approval))) {
    errors.push("approval digest does not match the human review record");
  }
}

function verifyFrozenRecord(frozen: FrozenWitness, expectedFrozenDigest?: string): WitnessLockVerification {
  const errors: string[] = [];
  verifyProposal(frozen.proposal, errors);
  verifyApproval(frozen.approval, frozen.proposal, errors);
  if (frozen.witnessDigest !== digestJson(witnessPayload(frozen.proposal))) {
    errors.push("frozen witness digest does not match the reviewed command and overlay bytes");
  }
  if (frozen.frozenDigest !== digestJson(frozenPayload(frozen))) {
    errors.push("frozen digest does not match the immutable freeze record");
  }
  let externalDigestStatus: WitnessLockVerification["externalDigestStatus"] = "NOT_PROVIDED";
  if (expectedFrozenDigest !== undefined) {
    if (!DigestSchema.safeParse(expectedFrozenDigest).success) {
      errors.push("expected frozen digest is not a valid sha256 digest");
      externalDigestStatus = "MISMATCH";
    } else if (expectedFrozenDigest === frozen.frozenDigest) {
      externalDigestStatus = "MATCH";
    } else {
      errors.push("externally supplied frozen digest does not match");
      externalDigestStatus = "MISMATCH";
    }
  }
  return {
    valid: errors.length === 0,
    errors,
    proposalDigest: frozen.proposal.proposalDigest,
    frozenDigest: frozen.frozenDigest,
    approval: { actor: frozen.approval.approvedBy, approvedAt: frozen.approval.approvedAt },
    externalDigestStatus
  };
}

/** Build and persist an immutable witness proposal from a strictly blinded incident packet. */
export function proposeWitness(storeDirectory: string, input: unknown): WitnessProposal {
  const proposal = buildProposal(input);
  const paths = storePaths(storeDirectory, proposal.proposalId, true);
  writeOnceJson(paths.proposal, proposal);
  return proposal;
}

export function readWitnessProposal(storeDirectory: string, proposalId: string): WitnessProposal {
  const paths = storePaths(storeDirectory, proposalId, false);
  return readProposal(paths.proposal);
}

/**
 * Records an explicit human review. The actor and timestamp are bound to the
 * proposal, its incident packet, and the exact command and overlay byte hashes.
 */
export function approveWitnessProposal(storeDirectory: string, proposalId: string, input: unknown): WitnessApproval {
  const approvalInput = HumanApprovalInputSchema.parse(input);
  const paths = storePaths(storeDirectory, proposalId, true);
  if (!existsSync(paths.proposal)) throw new Error(`Cannot approve an unknown proposal: ${proposalId}`);
  const proposal = readProposal(paths.proposal);
  const proposalErrors: string[] = [];
  verifyProposal(proposal, proposalErrors);
  if (proposalErrors.length > 0) throw new Error(`Cannot approve a mutated proposal: ${proposalErrors.join("; ")}`);
  const unsigned = {
    schemaVersion: "faultline.witness-approval.v1" as const,
    proposalId: proposal.proposalId,
    proposalDigest: proposal.proposalDigest,
    incidentPacketDigest: proposal.incidentPacketDigest,
    reviewedCommandDigest: proposal.witness.commandDigest,
    reviewedOverlayDigest: proposal.witness.overlayDigest,
    reviewerType: "HUMAN" as const,
    approvedBy: approvalInput.approvedBy,
    approvedAt: approvalInput.approvedAt ?? new Date().toISOString(),
    ...(approvalInput.note === undefined ? {} : { note: approvalInput.note })
  };
  const approval: WitnessApproval = { ...unsigned, approvalDigest: digestJson(unsigned) };
  writeOnceJson(paths.approval, approval);
  return approval;
}

export function readWitnessApproval(storeDirectory: string, proposalId: string): WitnessApproval {
  const paths = storePaths(storeDirectory, proposalId, false);
  return readApproval(paths.approval);
}

/**
 * Copies the approved proposal and approval into one write-once record. The
 * frozen snapshot is deliberately self-contained so a later mutable proposal
 * file cannot change what was approved for the investigation.
 */
export function freezeApprovedWitness(storeDirectory: string, proposalId: string, options: unknown = {}): FrozenWitness {
  const freezeOptions = FreezeOptionsSchema.parse(options);
  const paths = storePaths(storeDirectory, proposalId, true);
  if (!existsSync(paths.proposal)) throw new Error(`Cannot freeze an unknown proposal: ${proposalId}`);
  if (!existsSync(paths.approval)) throw new Error(`Cannot freeze an unapproved proposal: ${proposalId}`);
  const proposal = readProposal(paths.proposal);
  const approval = readApproval(paths.approval);
  const errors: string[] = [];
  verifyProposal(proposal, errors);
  verifyApproval(approval, proposal, errors);
  if (errors.length > 0) throw new Error(`Cannot freeze an invalid review chain: ${errors.join("; ")}`);
  const unsigned = {
    schemaVersion: "faultline.frozen-witness.v1" as const,
    frozenAt: freezeOptions.frozenAt ?? new Date().toISOString(),
    proposal,
    approval,
    witnessDigest: digestJson(witnessPayload(proposal))
  };
  const frozen: FrozenWitness = { ...unsigned, frozenDigest: digestJson(unsigned) };
  writeOnceJson(paths.frozen, frozen);
  return frozen;
}

export function readFrozenWitness(storeDirectory: string, proposalId: string): FrozenWitness {
  const paths = storePaths(storeDirectory, proposalId, false);
  return readFrozen(paths.frozen);
}

/**
 * Validates the self-contained freeze record. Supplying a digest recorded by a
 * separate actor upgrades this from local consistency checking to detection of
 * an editor who rewrites both a record and its in-file digest.
 */
export function verifyFrozenWitnessRecord(input: unknown, expectedFrozenDigest?: string): WitnessLockVerification {
  const parsed = FrozenWitnessSchema.safeParse(input);
  if (!parsed.success) {
    return {
      valid: false,
      errors: [`frozen witness schema validation failed: ${parsed.error.message}`],
      proposalDigest: null,
      frozenDigest: null,
      approval: null,
      externalDigestStatus: expectedFrozenDigest === undefined ? "NOT_PROVIDED" : "MISMATCH"
    };
  }
  return verifyFrozenRecord(parsed.data, expectedFrozenDigest);
}

/**
 * Verifies the complete on-disk chain: proposal, human approval, and frozen
 * snapshot must all agree. It never runs the proposed command or reads overlay
 * paths from the target repository.
 */
export function verifyFrozenWitness(storeDirectory: string, proposalId: string, expectedFrozenDigest?: string): WitnessLockVerification {
  let paths: StorePaths;
  try {
    paths = storePaths(storeDirectory, proposalId, false);
  } catch (error) {
    return {
      valid: false,
      errors: [`invalid witness lock location: ${error instanceof Error ? error.message : String(error)}`],
      proposalDigest: null,
      frozenDigest: null,
      approval: null,
      externalDigestStatus: expectedFrozenDigest === undefined ? "NOT_PROVIDED" : "MISMATCH"
    };
  }
  if (!existsSync(paths.frozen)) {
    const missingApproval = !existsSync(paths.approval);
    return {
      valid: false,
      errors: [missingApproval ? "human approval is missing; no witness may be frozen" : "approved proposal has not been frozen"],
      proposalDigest: null,
      frozenDigest: null,
      approval: null,
      externalDigestStatus: expectedFrozenDigest === undefined ? "NOT_PROVIDED" : "MISMATCH"
    };
  }

  let frozen: FrozenWitness;
  try {
    frozen = readFrozen(paths.frozen);
  } catch (error) {
    return {
      valid: false,
      errors: [`unable to read frozen witness: ${error instanceof Error ? error.message : String(error)}`],
      proposalDigest: null,
      frozenDigest: null,
      approval: null,
      externalDigestStatus: expectedFrozenDigest === undefined ? "NOT_PROVIDED" : "MISMATCH"
    };
  }
  const result = verifyFrozenRecord(frozen, expectedFrozenDigest);
  const errors = [...result.errors];
  if (!existsSync(paths.proposal)) {
    errors.push("persisted proposal is missing");
  } else {
    try {
      const proposal = readProposal(paths.proposal);
      if (canonicalJson(proposal) !== canonicalJson(frozen.proposal)) {
        errors.push("persisted proposal does not match frozen proposal snapshot");
      }
    } catch (error) {
      errors.push(`persisted proposal cannot be verified: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  if (!existsSync(paths.approval)) {
    errors.push("persisted human approval is missing");
  } else {
    try {
      const approval = readApproval(paths.approval);
      if (canonicalJson(approval) !== canonicalJson(frozen.approval)) {
        errors.push("persisted approval does not match frozen approval snapshot");
      }
    } catch (error) {
      errors.push(`persisted approval cannot be verified: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  return { ...result, valid: errors.length === 0, errors };
}
