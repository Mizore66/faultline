import {
  AGENT_DRAFT_EVIDENCE_GRADE,
  AGENT_DRAFT_EVIDENCE_LABEL
} from "./evidence-grade.js";
import { existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { isAbsolute, join, resolve } from "node:path";
import { z } from "zod";
import { digestJson } from "./canonical.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  readWitnessProposal,
  type FrozenWitness,
  type WitnessProposal
} from "./witness-lock.js";
import {
  matchStandingApprovalPolicy,
  policyApprovedBy,
  type StandingApprovalPolicy
} from "./standing-approval-policy.js";

export {
  AGENT_DRAFT_EVIDENCE_GRADE,
  AGENT_DRAFT_EVIDENCE_LABEL
} from "./evidence-grade.js";

export const APPROVED_AFTER_EXECUTION = "APPROVED_AFTER_EXECUTION" as const;
export const PARKED_INCIDENT_SCHEMA_VERSION = "faultline.parked-incident.v1" as const;
export const RATIFICATION_SCHEMA_VERSION = "faultline.witness-ratification.v1" as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const TimestampSchema = z.string().datetime({ offset: true });
const IdSchema = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/);

export const ParkedIncidentSchema = z.object({
  schemaVersion: z.literal(PARKED_INCIDENT_SCHEMA_VERSION),
  status: z.literal("PARKED"),
  evidenceGrade: z.literal(AGENT_DRAFT_EVIDENCE_GRADE),
  evidenceLabel: z.literal(AGENT_DRAFT_EVIDENCE_LABEL),
  proposalId: IdSchema,
  proposalDigest: DigestSchema,
  witnessDigest: DigestSchema,
  parkedAt: TimestampSchema,
  notification: z.string().min(1).max(4_096),
  blocking: z.literal(false),
  parkDigest: DigestSchema
}).strict();

export type ParkedIncident = z.infer<typeof ParkedIncidentSchema>;

export const WitnessRatificationSchema = z.object({
  schemaVersion: z.literal(RATIFICATION_SCHEMA_VERSION),
  recordKind: z.literal(APPROVED_AFTER_EXECUTION),
  proposalId: IdSchema,
  /** Digest of the witness that actually ran (must equal ratified digest). */
  executedWitnessDigest: DigestSchema,
  /** Digest presented for human ownership after the run. */
  ratifiedWitnessDigest: DigestSchema,
  ratifiedBy: z.string().trim().min(1).max(512),
  ratifiedAt: TimestampSchema,
  note: z.string().max(8_000).optional(),
  ratificationDigest: DigestSchema
}).strict();

export type WitnessRatification = z.infer<typeof WitnessRatificationSchema>;

export class AutonomousSessionError extends Error {
  public readonly code:
    | "DIGEST_MISMATCH"
    | "PROOF_EXPORT_REFUSED"
    | "MISSING_DRAFT"
    | "ALREADY_RATIFIED";

  public constructor(code: AutonomousSessionError["code"], message: string) {
    super(message);
    this.name = "AutonomousSessionError";
    this.code = code;
  }
}

function parkPayload(park: Omit<ParkedIncident, "parkDigest">): Omit<ParkedIncident, "parkDigest"> {
  return park;
}

function ratificationPayload(
  value: Omit<WitnessRatification, "ratificationDigest">
): Omit<WitnessRatification, "ratificationDigest"> {
  return value;
}

function ensureStoreDir(storeDirectory: string, segment: string): string {
  const root = resolve(storeDirectory);
  const directory = join(root, segment);
  if (!existsSync(directory)) mkdirSync(directory, { recursive: true, mode: 0o700 });
  const safe = resolveSafeDirectorySegment(directory);
  if (safe === null) throw new Error(`Autonomous session store must be a real directory: ${directory}`);
  return safe;
}

function recordPath(storeDirectory: string, segment: string, id: string): string {
  const directory = ensureStoreDir(storeDirectory, segment);
  const path = resolve(directory, `${IdSchema.parse(id)}.json`);
  const nested = relativeTrustedSystemPath(directory, path);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error("Autonomous session path escaped its store");
  }
  return path;
}

function witnessDigestFromProposal(proposal: WitnessProposal): string {
  return digestJson({
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
  });
}

/**
 * Heuristic extractor for commands that appear inside CI-log prose.
 * Intentionally never consulted by standing-policy matching — COH-12 (c).
 */
export function commandsSuggestedInCiLog(ciLog: string): readonly string[] {
  const suggestions: string[] = [];
  for (const line of ciLog.split(/\r?\n/)) {
    const trimmed = line.trim();
    const runMatch = /(?:^|\b)(?:run|suggested|try|command)\s*[:\-]\s*(.+)$/i.exec(trimmed);
    if (runMatch?.[1]) suggestions.push(runMatch[1].trim());
    const codeMatch = /`([^`]+)`/.exec(trimmed);
    if (codeMatch?.[1] && /\s/.test(codeMatch[1])) suggestions.push(codeMatch[1].trim());
  }
  return [...new Set(suggestions)];
}

export function agentDraftAnnotation(): { evidenceGrade: typeof AGENT_DRAFT_EVIDENCE_GRADE; evidenceLabel: typeof AGENT_DRAFT_EVIDENCE_LABEL } {
  return {
    evidenceGrade: AGENT_DRAFT_EVIDENCE_GRADE,
    evidenceLabel: AGENT_DRAFT_EVIDENCE_LABEL
  };
}

/**
 * When a proposal does not match standing policy, park it as AGENT_DRAFT and
 * return a non-blocking notification so the autonomous session can continue.
 */
export function parkUnmatchedProposal(
  storeDirectory: string,
  proposalId: string,
  options: { readonly parkedAt?: string; readonly notification?: string } = {}
): ParkedIncident {
  const proposal = readWitnessProposal(storeDirectory, proposalId);
  const unsigned = {
    schemaVersion: PARKED_INCIDENT_SCHEMA_VERSION,
    status: "PARKED" as const,
    ...agentDraftAnnotation(),
    proposalId: proposal.proposalId,
    proposalDigest: proposal.proposalDigest,
    witnessDigest: witnessDigestFromProposal(proposal),
    parkedAt: options.parkedAt ?? new Date().toISOString(),
    notification: options.notification
      ?? `Parked AGENT_DRAFT ${proposal.proposalId}: no standing-policy match. Session continues; human review required before proof.`,
    blocking: false as const
  };
  const parked = ParkedIncidentSchema.parse({
    ...unsigned,
    parkDigest: digestJson(parkPayload(unsigned))
  });
  const path = recordPath(storeDirectory, "parked", proposalId);
  writeFileSync(path, `${JSON.stringify(parked, null, 2)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
  return parked;
}

export function readParkedIncident(storeDirectory: string, proposalId: string): ParkedIncident {
  const path = recordPath(storeDirectory, "parked", proposalId);
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error("Parked incident must be a regular file");
  return ParkedIncidentSchema.parse(JSON.parse(readFileSync(path, "utf8")) as unknown);
}

/**
 * Resolve an autonomous proposal: auto-freeze on standing-policy match, otherwise
 * park as AGENT_DRAFT without blocking the session.
 */
export function resolveAutonomousProposal(
  storeDirectory: string,
  proposalId: string,
  policy: StandingApprovalPolicy | null
): {
  readonly outcome: "FROZEN_BY_POLICY" | "PARKED_AGENT_DRAFT";
  readonly frozen?: FrozenWitness;
  readonly parked?: ParkedIncident;
  readonly notification: string;
} {
  const proposal = readWitnessProposal(storeDirectory, proposalId);
  if (policy !== null) {
    const match = matchStandingApprovalPolicy(proposal, policy);
    if (match.matches) {
      approveWitnessProposal(storeDirectory, proposalId, {
        approvedBy: policyApprovedBy(policy),
        note: `standing-approval ${policy.policyId}`
      });
      const frozen = freezeApprovedWitness(storeDirectory, proposalId);
      return {
        outcome: "FROZEN_BY_POLICY",
        frozen,
        notification: `Standing policy ${policy.policyId} auto-froze ${proposalId}.`
      };
    }
  }
  const parked = parkUnmatchedProposal(storeDirectory, proposalId);
  return {
    outcome: "PARKED_AGENT_DRAFT",
    parked,
    notification: parked.notification
  };
}

/**
 * Human owns the question after execution only when the ratified digest is
 * byte-identical to the executed witness digest.
 */
export function ratifyExecutedWitness(
  storeDirectory: string,
  proposalId: string,
  input: {
    readonly executedWitnessDigest: string;
    readonly ratifiedBy: string;
    readonly ratifiedAt?: string;
    readonly note?: string;
  }
): { readonly ratification: WitnessRatification; readonly frozen: FrozenWitness } {
  const proposal = readWitnessProposal(storeDirectory, proposalId);
  const currentDigest = witnessDigestFromProposal(proposal);
  if (input.executedWitnessDigest !== currentDigest) {
    throw new AutonomousSessionError(
      "DIGEST_MISMATCH",
      "Ratification refused: executed witness digest does not match the stored proposal witness digest."
    );
  }
  if (DigestSchema.safeParse(input.executedWitnessDigest).success !== true) {
    throw new AutonomousSessionError("DIGEST_MISMATCH", "Ratification refused: executed witness digest is malformed.");
  }

  const unsigned = {
    schemaVersion: RATIFICATION_SCHEMA_VERSION,
    recordKind: APPROVED_AFTER_EXECUTION,
    proposalId,
    executedWitnessDigest: input.executedWitnessDigest,
    ratifiedWitnessDigest: currentDigest,
    ratifiedBy: input.ratifiedBy,
    ratifiedAt: input.ratifiedAt ?? new Date().toISOString(),
    ...(input.note === undefined ? {} : { note: input.note })
  };
  const ratification = WitnessRatificationSchema.parse({
    ...unsigned,
    ratificationDigest: digestJson(ratificationPayload(unsigned))
  });

  const path = recordPath(storeDirectory, "ratifications", proposalId);
  try {
    writeFileSync(path, `${JSON.stringify(ratification, null, 2)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
  } catch (error) {
    const code = error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
    if (code === "EEXIST") {
      throw new AutonomousSessionError("ALREADY_RATIFIED", `Ratification already exists for ${proposalId}`);
    }
    throw error;
  }

  approveWitnessProposal(storeDirectory, proposalId, {
    approvedBy: input.ratifiedBy,
    approvedAt: ratification.ratifiedAt,
    note: `${APPROVED_AFTER_EXECUTION}: ${input.note ?? "human owned the question after execution; digests identical"}`
  });
  const frozen = freezeApprovedWitness(storeDirectory, proposalId, { frozenAt: ratification.ratifiedAt });
  return { ratification, frozen };
}

/** Proof packages must never be exported from AGENT_DRAFT / parked exploratory results. */
export function assertCanExportProofFromWitnessStore(storeDirectory: string, proposalId: string): void {
  const parkedPath = join(resolve(storeDirectory), "parked", `${proposalId}.json`);
  if (existsSync(parkedPath)) {
    const parked = readParkedIncident(storeDirectory, proposalId);
    if (parked.evidenceGrade === AGENT_DRAFT_EVIDENCE_GRADE) {
      const ratifiedPath = join(resolve(storeDirectory), "ratifications", `${proposalId}.json`);
      if (!existsSync(ratifiedPath)) {
        throw new AutonomousSessionError(
          "PROOF_EXPORT_REFUSED",
          `Refusing proof export: ${proposalId} is still AGENT_DRAFT (parked). Ratify with digest-identical APPROVED_AFTER_EXECUTION first, or freeze via human/policy approval before execution.`
        );
      }
    }
  }
}

export function assertEvidenceGradeAllowsProofExport(evidenceGrade: string): void {
  if (evidenceGrade === AGENT_DRAFT_EVIDENCE_GRADE) {
    throw new AutonomousSessionError(
      "PROOF_EXPORT_REFUSED",
      "Refusing proof export: AGENT_DRAFT evidence cannot become a portable proof bundle."
    );
  }
}

export function readWitnessRatification(storeDirectory: string, proposalId: string): WitnessRatification {
  const path = recordPath(storeDirectory, "ratifications", proposalId);
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error("Ratification must be a regular file");
  return WitnessRatificationSchema.parse(JSON.parse(readFileSync(path, "utf8")) as unknown);
}

/** Convenience: frozen witness after ratification must carry APPROVED_AFTER_EXECUTION in the approval note. */
export function frozenApprovalIsAfterExecution(frozen: FrozenWitness): boolean {
  return (frozen.approval.note ?? "").includes(APPROVED_AFTER_EXECUTION);
}
