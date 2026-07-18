import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { canonicalJson, digestJson } from "./canonical.js";
import {
  type GitInvestigationResult,
  type StableGitTransition
} from "./git-investigation.js";
import {
  verifyGitMinimizationResultFile,
  type GitMinimizationExternalDigestStatus,
  type GitMinimizationResult
} from "./git-minimization.js";
import {
  RepairBriefSchema,
  RepairEvidencePacketSchema,
  validateRepairBrief,
  type RepairBrief,
  type RepairEvidencePacket
} from "./repair-brief.js";
import {
  verifyRepairBriefArtifact,
  type RepairBriefArtifactManifest,
  type RepairBriefArtifactExternalDigestStatus
} from "./repair-brief-store.js";
import {
  verifyPreventionProof,
  type PreventionProofBody,
  type PreventionProofExternalRootStatus,
  type PreventionProofManifest
} from "./prevention-proof.js";
import type { FrozenWitness } from "./witness-lock.js";

/**
 * Optional artifacts shown alongside a Git proof page. They deliberately
 * remain separate from the immutable Git-proof root: a viewer verifies each
 * input and its binding before rendering it, rather than treating a mutable
 * neighboring file as part of the original proof package.
 */
export type IncidentAttachmentOptions = {
  readonly minimizationFile?: string;
  readonly expectedMinimizationDigest?: string;
  readonly repairDirectory?: string;
  readonly expectedRepairDigest?: string;
  readonly preventionDirectory?: string;
  readonly expectedPreventionDigest?: string;
};

export type VerifiedMinimizationAttachment = {
  readonly result: GitMinimizationResult;
  readonly resultDigest: string;
  readonly externalDigestStatus: GitMinimizationExternalDigestStatus;
};

export type VerifiedRepairAttachment = {
  readonly manifest: RepairBriefArtifactManifest;
  readonly packet: RepairEvidencePacket;
  readonly brief: RepairBrief;
  readonly externalDigestStatus: RepairBriefArtifactExternalDigestStatus;
};

export type VerifiedPreventionAttachment = {
  readonly manifest: PreventionProofManifest;
  readonly prevention: PreventionProofBody;
  readonly rootDigest: string;
  readonly externalDigestStatus: PreventionProofExternalRootStatus;
};

export type VerifiedIncidentAttachments = {
  readonly minimization: VerifiedMinimizationAttachment | null;
  readonly repair: VerifiedRepairAttachment | null;
  readonly prevention: VerifiedPreventionAttachment | null;
};

function readJson(path: string, label: string): unknown {
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    throw new Error(`Unable to read verified ${label}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function sameTransitionEndpoint(
  transition: StableGitTransition,
  before: NonNullable<GitMinimizationResult["before"]>,
  after: NonNullable<GitMinimizationResult["after"]>
): boolean {
  return transition.kind === "PASS_TO_FAIL"
    && transition.before.commit === before.commit
    && transition.before.tree === before.tree
    && transition.after.commit === after.commit
    && transition.after.tree === after.tree;
}

function validateMinimizationBinding(
  result: GitMinimizationResult,
  investigation: GitInvestigationResult,
  frozenWitness: FrozenWitness
): string[] {
  const errors: string[] = [];
  if (result.witness === null) {
    errors.push("minimization has no frozen-witness summary");
  } else {
    if (result.witness.frozenDigest !== frozenWitness.frozenDigest) errors.push("minimization frozen digest does not match the Git proof witness");
    if (result.witness.witnessDigest !== frozenWitness.witnessDigest) errors.push("minimization witness digest does not match the Git proof witness");
  }
  if (result.before === null || result.after === null) {
    errors.push("minimization has no resolved before/after states");
  } else if (!investigation.transitions.some((transition) => sameTransitionEndpoint(transition, result.before!, result.after!))) {
    errors.push("minimization range is not a stable PASS_TO_FAIL transition in the Git proof package");
  }
  return errors;
}

function loadMinimization(
  file: string,
  expectedDigest: string | undefined,
  investigation: GitInvestigationResult,
  frozenWitness: FrozenWitness
): VerifiedMinimizationAttachment {
  const first = verifyGitMinimizationResultFile(resolve(file), expectedDigest);
  if (!first.valid || first.result === undefined || first.resultDigest === null) {
    throw new Error(`Refusing to attach an invalid minimization record: ${first.errors.join("; ")}`);
  }
  const linkageErrors = validateMinimizationBinding(first.result, investigation, frozenWitness);
  if (linkageErrors.length > 0) throw new Error(`Refusing to attach a minimization record for a different incident: ${linkageErrors.join("; ")}`);

  // A second safe read closes the normal local read/verify window before the
  // object reaches the renderer. Retain the externally supplied digest status
  // from this final read, not the earlier probe.
  const recheck = verifyGitMinimizationResultFile(resolve(file), expectedDigest);
  if (!recheck.valid || recheck.result === undefined || recheck.resultDigest === null
    || recheck.resultDigest !== first.resultDigest
    || canonicalJson(recheck.result) !== canonicalJson(first.result)) {
    throw new Error(`Refusing to attach a minimization record that changed during verification: ${recheck.errors.join("; ") || "digest changed"}`);
  }
  return {
    result: recheck.result,
    resultDigest: recheck.resultDigest,
    externalDigestStatus: recheck.externalDigestStatus
  };
}

type RepairArtifactContents = {
  readonly packet: RepairEvidencePacket;
  readonly brief: RepairBrief;
};

/**
 * Parse only a self-consistent private repair record. The artifact verifier
 * validates its on-disk snapshot too; this protects the exact objects that
 * the renderer receives rather than trusting a separate filesystem read.
 */
function readRepairArtifactContents(root: string): RepairArtifactContents {
  const packet = RepairEvidencePacketSchema.parse(readJson(join(root, "evidence-packet.json"), "repair evidence packet"));
  const brief = RepairBriefSchema.parse(readJson(join(root, "repair-brief.json"), "repair brief"));
  const validation = validateRepairBrief(packet, brief);
  if (!validation.valid || validation.brief === null) {
    throw new Error(`Refusing to attach an invalid repair brief payload: ${validation.errors.join("; ")}`);
  }
  return { packet, brief: validation.brief };
}

function sameRepairContents(left: RepairArtifactContents, right: RepairArtifactContents): boolean {
  return canonicalJson(left.packet) === canonicalJson(right.packet)
    && canonicalJson(left.brief) === canonicalJson(right.brief);
}

function manifestMatchesRepairContents(manifest: RepairBriefArtifactManifest, contents: RepairArtifactContents): boolean {
  return manifest.evidencePacket.digest === contents.packet.packetDigest
    && manifest.repairBrief.digest === digestJson(contents.brief);
}

function loadRepair(
  directory: string,
  expectedDigest: string | undefined,
  investigation: GitInvestigationResult,
  frozenWitness: FrozenWitness
): VerifiedRepairAttachment {
  const root = resolve(directory);
  const first = verifyRepairBriefArtifact(root, expectedDigest);
  if (!first.valid || first.manifest === null) {
    throw new Error(`Refusing to attach an invalid repair brief: ${first.errors.join("; ")}`);
  }
  const contents = readRepairArtifactContents(root);
  if (!manifestMatchesRepairContents(first.manifest, contents)) {
    throw new Error("Refusing to attach repair guidance whose in-memory payload does not match its verified manifest.");
  }
  const packet = contents.packet;
  if (packet.investigationDigest !== digestJson(investigation)) {
    throw new Error("Refusing to attach repair guidance from a different Git investigation.");
  }
  if (packet.frozenWitnessDigest !== frozenWitness.frozenDigest) {
    throw new Error("Refusing to attach repair guidance for a different frozen witness.");
  }

  // Read and verify twice. The final verifier runs *after* the second parsed
  // snapshot, and both parsed snapshots must be byte-for-byte canonical
  // equivalents. This fails closed if a mutable directory changes during the
  // normal verify/read window.
  const recheck = verifyRepairBriefArtifact(root, expectedDigest);
  if (!recheck.valid || recheck.manifest === null
    || canonicalJson(recheck.manifest) !== canonicalJson(first.manifest)) {
    throw new Error(`Refusing to attach repair guidance that changed during verification: ${recheck.errors.join("; ") || "manifest changed"}`);
  }
  const recheckedContents = readRepairArtifactContents(root);
  const finalCheck = verifyRepairBriefArtifact(root, expectedDigest);
  if (!finalCheck.valid || finalCheck.manifest === null
    || canonicalJson(finalCheck.manifest) !== canonicalJson(first.manifest)
    || !sameRepairContents(contents, recheckedContents)
    || !manifestMatchesRepairContents(finalCheck.manifest, recheckedContents)) {
    throw new Error(`Refusing to attach repair guidance that changed during verification: ${finalCheck.errors.join("; ") || "artifact contents changed"}`);
  }
  return {
    manifest: finalCheck.manifest,
    packet: recheckedContents.packet,
    brief: recheckedContents.brief,
    externalDigestStatus: finalCheck.externalDigestStatus
  };
}

function loadPrevention(
  directory: string,
  expectedRootDigest: string | undefined,
  investigation: GitInvestigationResult,
  frozenWitness: FrozenWitness,
  proofRootDigest: string
): VerifiedPreventionAttachment {
  const root = resolve(directory);
  const first = verifyPreventionProof(root, expectedRootDigest);
  if (!first.valid || first.manifest === null || first.prevention === null || first.rootDigest === null) {
    throw new Error(`Refusing to attach an invalid prevention proof: ${first.errors.join("; ")}`);
  }
  if (first.prevention.originalProofRoot !== proofRootDigest) {
    throw new Error("Refusing to attach a prevention proof bound to a different Git proof root.");
  }
  if (first.prevention.frozenWitnessDigest !== frozenWitness.witnessDigest) {
    throw new Error("Refusing to attach a prevention proof for a different frozen witness.");
  }
  if (first.prevention.investigationDigest !== undefined
    && first.prevention.investigationDigest !== digestJson(investigation)) {
    throw new Error("Refusing to attach a prevention proof from a different Git investigation.");
  }

  const recheck = verifyPreventionProof(root, expectedRootDigest);
  if (!recheck.valid || recheck.manifest === null || recheck.prevention === null || recheck.rootDigest === null
    || recheck.rootDigest !== first.rootDigest
    || canonicalJson(recheck.prevention) !== canonicalJson(first.prevention)) {
    throw new Error(`Refusing to attach a prevention proof that changed during verification: ${recheck.errors.join("; ") || "digest changed"}`);
  }
  return {
    manifest: recheck.manifest,
    prevention: recheck.prevention,
    rootDigest: recheck.rootDigest,
    externalDigestStatus: recheck.externalRootStatus
  };
}

/** Verify and bind optional downstream artifacts to this exact proof view. */
export function loadVerifiedIncidentAttachments(
  options: IncidentAttachmentOptions,
  investigation: GitInvestigationResult,
  frozenWitness: FrozenWitness,
  proofRootDigest?: string
): VerifiedIncidentAttachments {
  return {
    minimization: options.minimizationFile === undefined
      ? null
      : loadMinimization(options.minimizationFile, options.expectedMinimizationDigest, investigation, frozenWitness),
    repair: options.repairDirectory === undefined
      ? null
      : loadRepair(options.repairDirectory, options.expectedRepairDigest, investigation, frozenWitness),
    prevention: options.preventionDirectory === undefined
      ? null
      : loadPrevention(
        options.preventionDirectory,
        options.expectedPreventionDigest,
        investigation,
        frozenWitness,
        proofRootDigest ?? (() => {
          throw new Error("Prevention attachment requires the verified Git proof root digest.");
        })()
      )
  };
}
