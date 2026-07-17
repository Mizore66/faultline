import { z } from "zod";
import { digestJson, sha256 } from "./canonical.js";

/**
 * A review-only intake record.  This module deliberately does not read Git,
 * execute a command, resolve a remote ref, write a witness record, or freeze
 * anything.  Those effects belong to explicit adapters and human approval.
 */
export const INCIDENT_DRAFT_SCHEMA_VERSION = "faultline.incident-draft.v1" as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const SAFE_DRAFT_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const SAFE_GIT_REVISION = /^(?!-)[^\0\r\n]{1,512}$/;
const DIGEST_PINNED_IMAGE = /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const TimestampSchema = z.string().datetime({ offset: true });
export const IncidentDraftIdSchema = z.string().regex(SAFE_DRAFT_ID, "draftId must be a safe identifier");

function isTagFreeDigestImage(value: string): boolean {
  if (!DIGEST_PINNED_IMAGE.test(value)) return false;
  const repository = value.slice(0, value.indexOf("@"));
  // A colon after the last slash is a mutable image tag. A registry port is
  // before the first slash and remains valid in a digest-only reference.
  return repository.lastIndexOf(":") <= repository.lastIndexOf("/");
}

/**
 * This is intentionally a stored opaque shell command, not an argv parser.
 * Keeping its exact UTF-8 bytes lets a reviewer approve the same command that
 * was pasted from CI; a later execution adapter must choose how to run it.
 */
export const IncidentCommandSchema = z.string()
  .min(1)
  .max(32_000)
  .refine((value) => !value.includes("\0"), "command must not contain a NUL byte")
  .refine((value) => /\S/.test(value), "command must contain a non-whitespace character");

/** Repository paths are opaque local facts and are never expanded or resolved here. */
export const IncidentRepositorySchema = z.string()
  .min(1)
  .max(16_384)
  .refine((value) => !/[\0\r\n]/.test(value), "repository must not contain NUL or line-break characters")
  .refine((value) => /\S/.test(value), "repository must contain a non-whitespace character");

/** Safe as one Git argument; this module never interpolates it into a shell. */
export const IncidentRevisionSchema = z.string()
  .regex(SAFE_GIT_REVISION, "revision must be non-empty, cannot start with '-', and cannot contain NUL or line breaks");

export const ExplicitIncidentRangeSchema = z.object({
  ancestor: IncidentRevisionSchema,
  descendant: IncidentRevisionSchema
}).strict();

/** A resolved image is optional, but a mutable tag is never persisted as a draft runtime. */
export const IncidentRuntimeSchema = z.object({
  requested: z.string().min(1).max(256),
  image: z.string().refine(isTagFreeDigestImage, "runtime image must be a tag-free digest-pinned reference")
}).strict();

/** Immutable references to the proposal this intake record created. */
export const IncidentProposalBindingSchema = z.object({
  proposalId: IncidentDraftIdSchema,
  proposalDigest: DigestSchema,
  incidentPacketDigest: DigestSchema,
  commandDigest: DigestSchema
}).strict();

/**
 * Facts supplied by a local Git adapter after resolving HEAD and its parents.
 * The core does not invoke Git itself, so it cannot accidentally query or
 * select a remote branch as a base.
 */
export const LocalHeadFactsSchema = z.object({
  head: IncidentRevisionSchema,
  parents: z.array(IncidentRevisionSchema).max(16)
}).strict();

export const IncidentRangeFactsSchema = z.object({
  range: ExplicitIncidentRangeSchema.optional(),
  localHead: LocalHeadFactsSchema.optional()
}).strict();

export const ResolvedIncidentRangeSchema = ExplicitIncidentRangeSchema.extend({
  source: z.enum(["EXPLICIT", "LOCAL_HEAD_PARENT"])
}).strict();

export const IncidentDraftInputSchema = z.object({
  draftId: IncidentDraftIdSchema,
  createdAt: TimestampSchema,
  repository: IncidentRepositorySchema,
  command: IncidentCommandSchema,
  range: ExplicitIncidentRangeSchema.optional(),
  localHead: LocalHeadFactsSchema.optional(),
  runtime: IncidentRuntimeSchema.optional(),
  proposal: IncidentProposalBindingSchema.optional()
}).strict();

const ReviewWithoutProposalSchema = z.object({
  required: z.literal(true),
  state: z.literal("PENDING_HUMAN_REVIEW"),
  witnessState: z.literal("NOT_PROPOSED"),
  freezeState: z.literal("NOT_FROZEN"),
  autoFreeze: z.literal(false)
}).strict();

const ReviewWithProposalSchema = z.object({
  required: z.literal(true),
  state: z.literal("PENDING_HUMAN_REVIEW"),
  witnessState: z.literal("PROPOSED"),
  freezeState: z.literal("NOT_FROZEN"),
  autoFreeze: z.literal(false),
  proposal: IncidentProposalBindingSchema
}).strict();

const IncidentReviewSchema = z.union([ReviewWithoutProposalSchema, ReviewWithProposalSchema]);

export const IncidentDraftSchema = z.object({
  schemaVersion: z.literal(INCIDENT_DRAFT_SCHEMA_VERSION),
  draftId: IncidentDraftIdSchema,
  createdAt: TimestampSchema,
  status: z.literal("DRAFT_REQUIRES_HUMAN_REVIEW"),
  repository: IncidentRepositorySchema,
  /** Exact pasted command bytes, stored without shell escaping or execution. */
  command: IncidentCommandSchema,
  commandDigest: DigestSchema,
  range: ResolvedIncidentRangeSchema,
  runtime: IncidentRuntimeSchema.optional(),
  review: IncidentReviewSchema,
  draftDigest: DigestSchema
}).strict();

export type IncidentDraftInput = z.input<typeof IncidentDraftInputSchema>;
export type IncidentRangeFacts = z.input<typeof IncidentRangeFactsSchema>;
export type ResolvedIncidentRange = z.infer<typeof ResolvedIncidentRangeSchema>;
type IncidentDraftData = z.infer<typeof IncidentDraftSchema>;

type DeepReadonly<Value> = Value extends (...argumentsList: never[]) => unknown
  ? Value
  : Value extends readonly (infer Item)[]
    ? readonly DeepReadonly<Item>[]
    : Value extends object
      ? { readonly [Key in keyof Value]: DeepReadonly<Value[Key]> }
      : Value;

/** A read-only, content-addressed draft. Mutating it is prevented at runtime too. */
export type IncidentDraft = DeepReadonly<IncidentDraftData>;

export type IncidentDraftRangeErrorCode =
  | "LOCAL_HEAD_REQUIRED"
  | "ROOT_HEAD"
  | "AMBIGUOUS_HEAD"
  | "DEGENERATE_RANGE";

export class IncidentDraftRangeError extends Error {
  public readonly code: IncidentDraftRangeErrorCode;

  public constructor(code: IncidentDraftRangeErrorCode, message: string) {
    super(message);
    this.name = "IncidentDraftRangeError";
    this.code = code;
  }
}

export type IncidentDraftVerification = {
  valid: boolean;
  errors: string[];
};

function assertNonDegenerateRange(range: { ancestor: string; descendant: string }): void {
  if (range.ancestor === range.descendant) {
    throw new IncidentDraftRangeError(
      "DEGENERATE_RANGE",
      "incident range ancestor and descendant must be different"
    );
  }
}

/**
 * Selects only an explicit caller-provided range or one observed local parent.
 * It never performs Git I/O and never guesses an upstream, PR base, or remote
 * branch.  A merge HEAD is intentionally rejected rather than silently using
 * first-parent semantics.
 */
export function resolveIncidentRange(input: unknown): ResolvedIncidentRange {
  const facts = IncidentRangeFactsSchema.parse(input);

  if (facts.range !== undefined) {
    assertNonDegenerateRange(facts.range);
    return Object.freeze({ ...facts.range, source: "EXPLICIT" as const });
  }

  if (facts.localHead === undefined) {
    throw new IncidentDraftRangeError(
      "LOCAL_HEAD_REQUIRED",
      "an explicit range is required when locally observed HEAD facts are unavailable"
    );
  }

  if (facts.localHead.parents.length === 0) {
    throw new IncidentDraftRangeError(
      "ROOT_HEAD",
      "HEAD is a root commit; select an explicit range instead of guessing a base"
    );
  }

  if (facts.localHead.parents.length !== 1) {
    throw new IncidentDraftRangeError(
      "AMBIGUOUS_HEAD",
      "HEAD has multiple parents; select an explicit range instead of guessing a parent"
    );
  }

  const ancestor = facts.localHead.parents[0];
  if (ancestor === undefined) {
    throw new IncidentDraftRangeError("LOCAL_HEAD_REQUIRED", "locally observed HEAD parent is unavailable");
  }
  const range = { ancestor, descendant: facts.localHead.head };
  assertNonDegenerateRange(range);
  // This is the concrete, locally observed equivalent of HEAD~1 -> HEAD.
  return Object.freeze({ ...range, source: "LOCAL_HEAD_PARENT" as const });
}

function commandDigest(command: string): string {
  return `sha256:${sha256(Buffer.from(command, "utf8"))}`;
}

function unsignedDraft(draft: IncidentDraftData): Omit<IncidentDraftData, "draftDigest"> {
  const { draftDigest: _draftDigest, ...unsigned } = draft;
  return unsigned;
}

function deepFreeze<Value>(value: Value): Value {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const child of Object.values(value as Record<string, unknown>)) deepFreeze(child);
    Object.freeze(value);
  }
  return value;
}

/**
 * Builds an immutable, review-only draft from supplied facts. It does not
 * create, approve, or freeze a witness itself; an adapter may bind a
 * separately created proposal, but approval and freeze remain later explicit
 * human actions through the witness-lock workflow.
 */
export function createIncidentDraft(input: unknown): IncidentDraft {
  const parsed = IncidentDraftInputSchema.parse(input);
  const range = resolveIncidentRange({ range: parsed.range, localHead: parsed.localHead });
  const exactCommandDigest = commandDigest(parsed.command);
  if (parsed.proposal !== undefined && parsed.proposal.proposalId !== parsed.draftId) {
    throw new Error("incident proposal binding must use the same identifier as its draft");
  }
  if (parsed.proposal !== undefined && parsed.proposal.commandDigest !== exactCommandDigest) {
    throw new Error("incident proposal binding does not match the exact draft command");
  }
  const review = parsed.proposal === undefined
    ? {
      required: true as const,
      state: "PENDING_HUMAN_REVIEW" as const,
      witnessState: "NOT_PROPOSED" as const,
      freezeState: "NOT_FROZEN" as const,
      autoFreeze: false as const
    }
    : {
      required: true as const,
      state: "PENDING_HUMAN_REVIEW" as const,
      witnessState: "PROPOSED" as const,
      freezeState: "NOT_FROZEN" as const,
      autoFreeze: false as const,
      proposal: parsed.proposal
    };
  const unsigned = {
    schemaVersion: INCIDENT_DRAFT_SCHEMA_VERSION,
    draftId: parsed.draftId,
    createdAt: parsed.createdAt,
    status: "DRAFT_REQUIRES_HUMAN_REVIEW" as const,
    repository: parsed.repository,
    command: parsed.command,
    commandDigest: exactCommandDigest,
    range,
    ...(parsed.runtime === undefined ? {} : { runtime: parsed.runtime }),
    review
  };
  const draft = IncidentDraftSchema.parse({ ...unsigned, draftDigest: digestJson(unsigned) });
  return deepFreeze(draft) as IncidentDraft;
}

/** Validates a serialized draft and its content digest without performing any I/O. */
export function verifyIncidentDraft(input: unknown): IncidentDraftVerification {
  const parsed = IncidentDraftSchema.safeParse(input);
  if (!parsed.success) {
    return { valid: false, errors: [`incident draft schema validation failed: ${parsed.error.message}`] };
  }
  const expectedDigest = digestJson(unsignedDraft(parsed.data));
  if (parsed.data.draftDigest !== expectedDigest) {
    return { valid: false, errors: ["draft digest does not match the immutable draft payload"] };
  }
  return { valid: true, errors: [] };
}
