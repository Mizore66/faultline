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

export const STANDING_APPROVAL_POLICY_SCHEMA_VERSION = "faultline.standing-approval-policy.v1" as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const TimestampSchema = z.string().datetime({ offset: true });

const OverlayTemplateSchema = z.object({
  path: z.string().min(1).max(4_096),
  bytesBase64: z.string().min(0).max(2_000_000)
}).strict();

/**
 * Human-frozen, digest-bound standing approval.
 * Matching is exact-string on command and byte-exact on overlays; network must
 * stay disabled; timeout may not exceed the policy ceiling.
 */
export const StandingApprovalPolicySchema = z.object({
  schemaVersion: z.literal(STANDING_APPROVAL_POLICY_SCHEMA_VERSION),
  policyId: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/),
  frozenBy: z.string().trim().min(1).max(512),
  frozenAt: TimestampSchema,
  allowedCommands: z.array(z.string().min(1).max(32_000)).min(1).max(64),
  /**
   * Empty array means proposals must carry zero overlays.
   * Non-empty means every overlay must match a template byte-for-byte (path + base64).
   */
  allowedOverlayTemplates: z.array(OverlayTemplateSchema).max(64),
  maxTimeoutSeconds: z.number().int().min(1).max(3_600),
  network: z.literal("disabled"),
  credentials: z.literal("redacted"),
  policyDigest: DigestSchema
}).strict();

export type StandingApprovalPolicy = z.infer<typeof StandingApprovalPolicySchema>;

export type StandingPolicyMatch =
  | { readonly matches: true }
  | { readonly matches: false; readonly reasons: readonly string[] };

function policyPayload(policy: Omit<StandingApprovalPolicy, "policyDigest">): Omit<StandingApprovalPolicy, "policyDigest"> {
  return policy;
}

function sortedOverlayKey(path: string, bytesBase64: string): string {
  return `${path}\0${bytesBase64}`;
}

/** Build and digest a standing policy. The digest is bound into approvedBy on auto-freeze. */
export function buildStandingApprovalPolicy(input: unknown): StandingApprovalPolicy {
  const parsed = StandingApprovalPolicySchema.omit({ policyDigest: true }).extend({
    policyDigest: DigestSchema.optional()
  }).parse(input);
  const { policyDigest: _ignored, ...rest } = parsed;
  const unsigned = StandingApprovalPolicySchema.omit({ policyDigest: true }).parse({
    ...rest,
    allowedCommands: [...rest.allowedCommands],
    allowedOverlayTemplates: [...rest.allowedOverlayTemplates].sort((a, b) =>
      a.path.localeCompare(b.path) || a.bytesBase64.localeCompare(b.bytesBase64)
    )
  });
  return StandingApprovalPolicySchema.parse({
    ...unsigned,
    policyDigest: digestJson(policyPayload(unsigned))
  });
}

export function matchStandingApprovalPolicy(
  proposal: WitnessProposal,
  policy: StandingApprovalPolicy
): StandingPolicyMatch {
  const reasons: string[] = [];
  if (policy.network !== "disabled") reasons.push("policy network must be disabled");
  if (proposal.witness.policy.network !== "disabled") reasons.push("proposal network is not disabled");
  if (proposal.witness.policy.credentials !== "redacted") reasons.push("proposal credentials are not redacted");
  if (proposal.witness.policy.timeoutSeconds > policy.maxTimeoutSeconds) {
    reasons.push(`proposal timeout ${proposal.witness.policy.timeoutSeconds}s exceeds policy ceiling ${policy.maxTimeoutSeconds}s`);
  }
  if (!policy.allowedCommands.includes(proposal.witness.command)) {
    reasons.push("proposal command is not an exact allowlist match");
  }

  const templates = policy.allowedOverlayTemplates;
  const overlays = proposal.witness.overlays;
  if (templates.length === 0) {
    if (overlays.length > 0) reasons.push("policy allows zero overlays but the proposal includes overlays");
  } else {
    if (overlays.length !== templates.length) {
      reasons.push(`overlay count ${overlays.length} does not match policy template count ${templates.length}`);
    } else {
      const allowed = new Set(templates.map((item) => sortedOverlayKey(item.path, item.bytesBase64)));
      for (const overlay of overlays) {
        if (!allowed.has(sortedOverlayKey(overlay.path, overlay.bytesBase64))) {
          reasons.push(`overlay does not byte-match a policy template: ${overlay.path}`);
        }
      }
    }
  }

  if (reasons.length > 0) return { matches: false, reasons };
  return { matches: true };
}

export function policyApprovedBy(policy: StandingApprovalPolicy): string {
  return `policy:${policy.policyDigest}`;
}

/**
 * When a proposal matches a standing policy, write the same approval+freeze
 * records as a human path, with approvedBy set to policy:<digest>.
 */
export function applyStandingApprovalPolicy(
  storeDirectory: string,
  proposalId: string,
  policyInput: unknown,
  options: { readonly approvedAt?: string; readonly frozenAt?: string; readonly note?: string } = {}
): FrozenWitness {
  const policy = StandingApprovalPolicySchema.parse(policyInput);
  const { policyDigest: _discard, ...unsigned } = policy;
  const recomputed = buildStandingApprovalPolicy(unsigned);
  if (recomputed.policyDigest !== policy.policyDigest) {
    throw new Error("Standing approval policy digest does not match its canonical contents");
  }
  const proposal = readWitnessProposal(storeDirectory, proposalId);
  const match = matchStandingApprovalPolicy(proposal, policy);
  if (!match.matches) {
    throw new Error(`Standing approval refused: ${match.reasons.join("; ")}`);
  }
  approveWitnessProposal(storeDirectory, proposalId, {
    approvedBy: policyApprovedBy(policy),
    ...(options.approvedAt === undefined ? {} : { approvedAt: options.approvedAt }),
    note: options.note ?? `standing-approval ${policy.policyId}`
  });
  return freezeApprovedWitness(storeDirectory, proposalId, {
    ...(options.frozenAt === undefined ? {} : { frozenAt: options.frozenAt })
  });
}

function ensurePolicyDirectory(storeDirectory: string): string {
  const root = resolve(storeDirectory);
  const directory = join(root, "standing-policies");
  if (!existsSync(directory)) mkdirSync(directory, { recursive: true, mode: 0o700 });
  const safe = resolveSafeDirectorySegment(directory);
  if (safe === null) throw new Error("Standing policy store must be a real directory");
  return safe;
}

export function writeStandingApprovalPolicy(storeDirectory: string, policyInput: unknown): StandingApprovalPolicy {
  const policy = buildStandingApprovalPolicy(policyInput);
  const directory = ensurePolicyDirectory(storeDirectory);
  const path = resolve(directory, `${policy.policyId}.json`);
  const nested = relativeTrustedSystemPath(directory, path);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error("Standing policy path escaped its store");
  }
  writeFileSync(path, `${JSON.stringify(policy, null, 2)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
  return policy;
}

export function readStandingApprovalPolicy(storeDirectory: string, policyId: string): StandingApprovalPolicy {
  const directory = ensurePolicyDirectory(storeDirectory);
  const path = resolve(directory, `${policyId}.json`);
  const nested = relativeTrustedSystemPath(directory, path);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error("Standing policy path escaped its store");
  }
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error("Standing policy must be a regular file");
  return StandingApprovalPolicySchema.parse(JSON.parse(readFileSync(path, "utf8")) as unknown);
}

/**
 * Attempt auto-freeze from an on-disk standing policy. Novel commands always
 * return a non-match so callers fall through to human review.
 */
export function tryAutoFreezeFromStandingPolicy(
  storeDirectory: string,
  proposalId: string,
  policyId: string
): { readonly frozen: FrozenWitness; readonly policy: StandingApprovalPolicy } | { readonly frozen: null; readonly reasons: readonly string[] } {
  const policy = readStandingApprovalPolicy(storeDirectory, policyId);
  const proposal = readWitnessProposal(storeDirectory, proposalId);
  const match = matchStandingApprovalPolicy(proposal, policy);
  if (!match.matches) return { frozen: null, reasons: match.reasons };
  const frozen = applyStandingApprovalPolicy(storeDirectory, proposalId, policy);
  return { frozen, policy };
}
