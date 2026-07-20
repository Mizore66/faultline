import { z } from "zod";
import { canonicalJson, digestJson, sha256 } from "./canonical.js";
import {
  IncidentPacketSchema,
  WitnessProposalSchema,
  approveWitnessProposal,
  freezeApprovedWitness,
  readWitnessProposal,
  type FrozenWitness,
  type WitnessApproval,
  type WitnessProposal
} from "./witness-lock.js";

/**
 * A local, read-only review snapshot for a proposed witness. It never runs
 * the command, materializes an overlay, or persists an approval by itself.
 */
export const WITNESS_REVIEW_SCHEMA_VERSION = "faultline.witness-review.v1" as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const TimestampSchema = z.string().datetime({ offset: true });
const ReviewerSchema = z.string().trim().min(1).max(512);

export const WitnessReviewSchema = z.object({
  schemaVersion: z.literal(WITNESS_REVIEW_SCHEMA_VERSION),
  mode: z.literal("LOCAL_READ_ONLY"),
  /** Exact reviewed content, including opaque base64 overlay bytes. */
  proposal: WitnessProposalSchema,
  safeguards: z.object({
    packet: z.literal("STRICTLY_BLINDED_PROTOCOL_SHAPE"),
    command: z.literal("NOT_EXECUTED"),
    overlays: z.literal("NOT_MATERIALIZED"),
    approval: z.literal("EXPLICIT_HUMAN_ACTION_REQUIRED"),
    freeze: z.literal("EXPLICIT_HUMAN_ACTION_REQUIRED")
  }).strict(),
  reviewDigest: DigestSchema
}).strict();

export const WitnessReviewApprovalInputSchema = z.object({
  reviewDigest: DigestSchema,
  approvedBy: ReviewerSchema,
  approvedAt: TimestampSchema.optional(),
  note: z.string().max(8_000).optional()
}).strict();

export const WitnessReviewFreezeInputSchema = z.object({
  reviewDigest: DigestSchema,
  frozenAt: TimestampSchema.optional()
}).strict();

type WitnessReviewData = z.infer<typeof WitnessReviewSchema>;

type DeepReadonly<Value> = Value extends (...argumentsList: never[]) => unknown
  ? Value
  : Value extends readonly (infer Item)[]
    ? readonly DeepReadonly<Item>[]
    : Value extends object
      ? { readonly [Key in keyof Value]: DeepReadonly<Value[Key]> }
      : Value;

export type WitnessReview = DeepReadonly<WitnessReviewData>;

export type WitnessReviewErrorCode =
  | "UNBLINDED_OR_INVALID_PROPOSAL"
  | "PROPOSAL_INTEGRITY_FAILURE"
  | "INVALID_REVIEW"
  | "REVIEW_CONFIRMATION_MISMATCH"
  | "STALE_REVIEW";

export class WitnessReviewError extends Error {
  public readonly code: WitnessReviewErrorCode;

  public constructor(code: WitnessReviewErrorCode, message: string) {
    super(message);
    this.name = "WitnessReviewError";
    this.code = code;
  }
}

export type WitnessReviewVerification = {
  readonly valid: boolean;
  readonly errors: readonly string[];
};

function commandDigest(command: string): string {
  return `sha256:${sha256(Buffer.from(command, "utf8"))}`;
}

function overlayDigest(proposal: WitnessProposal): string {
  return digestJson(proposal.witness.overlays.map((overlay) => ({
    path: overlay.path,
    bytesDigest: overlay.bytesDigest
  })));
}

function proposalPayload(proposal: WitnessProposal): Omit<WitnessProposal, "proposalDigest"> {
  const { proposalDigest: _proposalDigest, ...payload } = proposal;
  return payload;
}

function reviewPayload(review: WitnessReviewData): Omit<WitnessReviewData, "reviewDigest"> {
  const { reviewDigest: _reviewDigest, ...payload } = review;
  return payload;
}

function deepFreeze<Value>(value: Value): Value {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const child of Object.values(value as Record<string, unknown>)) deepFreeze(child);
    Object.freeze(value);
  }
  return value;
}

/**
 * Check the portions that bind the proposed command and overlay bytes to the
 * strict blinded incident packet. This is deliberately duplicated at the
 * display boundary: a review screen must not present a malformed proposal as
 * though it were safe to approve.
 */
function proposalIntegrityErrors(proposal: WitnessProposal): string[] {
  const errors: string[] = [];
  if (!IncidentPacketSchema.safeParse(proposal.incidentPacket).success) {
    errors.push("incident packet is not a strictly blinded protocol packet");
  }
  if (proposal.incidentPacketDigest !== digestJson(proposal.incidentPacket)) {
    errors.push("incident packet digest does not match the proposal packet");
  }
  if (proposal.witness.commandDigest !== commandDigest(proposal.witness.command)) {
    errors.push("command digest does not match the exact UTF-8 command bytes");
  }

  const paths = new Set<string>();
  for (const overlay of proposal.witness.overlays) {
    const key = overlay.path.toLocaleLowerCase("en-US");
    if (paths.has(key)) errors.push(`duplicate overlay path: ${overlay.path}`);
    paths.add(key);
    const bytes = Buffer.from(overlay.bytesBase64, "base64");
    if (overlay.bytesDigest !== `sha256:${sha256(bytes)}`) {
      errors.push(`overlay byte digest does not match: ${overlay.path}`);
    }
  }
  for (let index = 1; index < proposal.witness.overlays.length; index += 1) {
    const previous = proposal.witness.overlays[index - 1];
    const current = proposal.witness.overlays[index];
    if (previous !== undefined && current !== undefined && previous.path.localeCompare(current.path) > 0) {
      errors.push("overlay records are not in canonical path order");
      break;
    }
  }
  if (proposal.witness.overlayDigest !== overlayDigest(proposal)) {
    errors.push("overlay digest does not match the ordered overlay byte digests");
  }
  if (proposal.proposalDigest !== digestJson(proposalPayload(proposal))) {
    errors.push("proposal digest does not match the immutable proposal payload");
  }
  return errors;
}

function readReviewableProposal(storeDirectory: string, proposalId: string): WitnessProposal {
  let proposal: WitnessProposal;
  try {
    proposal = readWitnessProposal(storeDirectory, proposalId);
  } catch {
    // Do not attempt to salvage a packet that no longer meets the strict,
    // blinded proposal schema. A user must create a fresh proposal instead.
    throw new WitnessReviewError(
      "UNBLINDED_OR_INVALID_PROPOSAL",
      "Witness review refused: the stored proposal is not a valid strictly blinded protocol packet"
    );
  }
  const errors = proposalIntegrityErrors(proposal);
  if (errors.length > 0) {
    throw new WitnessReviewError(
      "PROPOSAL_INTEGRITY_FAILURE",
      `Witness review refused: proposal integrity verification failed: ${errors.join("; ")}`
    );
  }
  return proposal;
}

function parseAndVerifyReview(input: unknown): WitnessReviewData {
  const parsed = WitnessReviewSchema.safeParse(input);
  if (!parsed.success) {
    throw new WitnessReviewError("INVALID_REVIEW", "Witness review is not a valid local review snapshot");
  }
  if (parsed.data.reviewDigest !== digestJson(reviewPayload(parsed.data))) {
    throw new WitnessReviewError("INVALID_REVIEW", "Witness review digest does not match its immutable review payload");
  }
  const errors = proposalIntegrityErrors(parsed.data.proposal);
  if (errors.length > 0) {
    throw new WitnessReviewError(
      "INVALID_REVIEW",
      `Witness review contains an invalid proposed witness: ${errors.join("; ")}`
    );
  }
  return parsed.data;
}

function assertFreshReview(storeDirectory: string, review: WitnessReviewData): WitnessProposal {
  const current = readReviewableProposal(storeDirectory, review.proposal.proposalId);
  if (canonicalJson(current) !== canonicalJson(review.proposal)) {
    throw new WitnessReviewError(
      "STALE_REVIEW",
      "Witness review is stale because the stored proposal no longer exactly matches the reviewed snapshot"
    );
  }
  return current;
}

function assertConfirmed(expectedDigest: string, suppliedDigest: string): void {
  if (expectedDigest !== suppliedDigest) {
    throw new WitnessReviewError(
      "REVIEW_CONFIRMATION_MISMATCH",
      "Explicit review confirmation does not match the review snapshot"
    );
  }
}

/**
 * Opens a no-side-effect local review snapshot. The exact raw command and
 * base64 overlay bytes are included so an authorized human can inspect what
 * they are approving; callers must treat the snapshot as sensitive.
 */
export function openWitnessReview(storeDirectory: string, proposalId: string): WitnessReview {
  const proposal = readReviewableProposal(storeDirectory, proposalId);
  const unsigned = {
    schemaVersion: WITNESS_REVIEW_SCHEMA_VERSION,
    mode: "LOCAL_READ_ONLY" as const,
    proposal,
    safeguards: {
      packet: "STRICTLY_BLINDED_PROTOCOL_SHAPE" as const,
      command: "NOT_EXECUTED" as const,
      overlays: "NOT_MATERIALIZED" as const,
      approval: "EXPLICIT_HUMAN_ACTION_REQUIRED" as const,
      freeze: "EXPLICIT_HUMAN_ACTION_REQUIRED" as const
    }
  };
  const review = WitnessReviewSchema.parse({ ...unsigned, reviewDigest: digestJson(unsigned) });
  return deepFreeze(review) as WitnessReview;
}

/** Validates an exported local review snapshot without reading or writing the witness store. */
export function verifyWitnessReview(input: unknown): WitnessReviewVerification {
  try {
    parseAndVerifyReview(input);
    return { valid: true, errors: [] };
  } catch (error) {
    return { valid: false, errors: [error instanceof Error ? error.message : String(error)] };
  }
}

/**
 * Persist one human approval only after the caller confirms the exact review
 * digest. The underlying lock store remains write-once and revalidates the
 * proposal immediately before writing.
 */
export function approveWitnessReview(
  storeDirectory: string,
  reviewInput: unknown,
  approvalInput: unknown
): WitnessApproval {
  const review = parseAndVerifyReview(reviewInput);
  const approval = WitnessReviewApprovalInputSchema.parse(approvalInput);
  assertConfirmed(review.reviewDigest, approval.reviewDigest);
  const proposal = assertFreshReview(storeDirectory, review);
  return approveWitnessProposal(storeDirectory, proposal.proposalId, {
    approvedBy: approval.approvedBy,
    ...(approval.approvedAt === undefined ? {} : { approvedAt: approval.approvedAt }),
    ...(approval.note === undefined ? {} : { note: approval.note })
  });
}

/**
 * Create the immutable frozen record only after an explicit matching review
 * confirmation. Any unblinded, malformed, stale, or unapproved proposal is
 * refused before the lock store can create a frozen record.
 */
export function freezeWitnessReview(
  storeDirectory: string,
  reviewInput: unknown,
  freezeInput: unknown
): FrozenWitness {
  const review = parseAndVerifyReview(reviewInput);
  const freeze = WitnessReviewFreezeInputSchema.parse(freezeInput);
  assertConfirmed(review.reviewDigest, freeze.reviewDigest);
  const proposal = assertFreshReview(storeDirectory, review);
  return freezeApprovedWitness(storeDirectory, proposal.proposalId, {
    ...(freeze.frozenAt === undefined ? {} : { frozenAt: freeze.frozenAt })
  });
}

export const WitnessReviewApproveAndFreezeInputSchema = WitnessReviewApprovalInputSchema.extend({
  frozenAt: TimestampSchema.optional()
}).strict();

/**
 * Atomic Approve+Freeze used by the humanized review surface and TTY path.
 * Produces the same write-once approval and frozen records as the two-step flow.
 */
export function approveAndFreezeWitnessReview(
  storeDirectory: string,
  reviewInput: unknown,
  input: unknown
): { readonly approval: WitnessApproval; readonly frozen: FrozenWitness } {
  const review = parseAndVerifyReview(reviewInput);
  const parsed = WitnessReviewApproveAndFreezeInputSchema.parse(input);
  assertConfirmed(review.reviewDigest, parsed.reviewDigest);
  assertFreshReview(storeDirectory, review);
  const approval = approveWitnessProposal(storeDirectory, review.proposal.proposalId, {
    approvedBy: parsed.approvedBy,
    ...(parsed.approvedAt === undefined ? {} : { approvedAt: parsed.approvedAt }),
    ...(parsed.note === undefined ? {} : { note: parsed.note })
  });
  const frozen = freezeApprovedWitness(storeDirectory, review.proposal.proposalId, {
    ...(parsed.frozenAt === undefined ? {} : { frozenAt: parsed.frozenAt })
  });
  return { approval, frozen };
}
