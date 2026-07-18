import { join, resolve } from "node:path";
import { investigateGitRange, type GitInvestigationResult } from "./git-investigation.js";
import {
  writeGitInvestigationProofBundle,
  verifyGitInvestigationProofBundle
} from "./git-proof-bundle.js";
import type { IncidentDraft } from "./incident.js";
import { readVerifiedCodexLifecycleLedger, type CodexLifecycleLedger } from "./ledger.js";
import {
  readFrozenWitness,
  verifyFrozenWitness,
  type FrozenWitness
} from "./witness-lock.js";

export type IncidentContinuationResult =
  | {
    readonly status: "PROOF_BUNDLE_READY";
    readonly incident: {
      readonly id: string;
      readonly draftDigest: string;
      readonly range: IncidentDraft["range"];
      readonly runtime: { requested: string; image: string } | null;
      readonly frozenDigest: string;
      readonly frozenDigestExternalStatus: string;
    };
    readonly investigation: GitInvestigationResult;
    readonly proofBundle: {
      readonly directory: string;
      readonly rootDigest: string;
      readonly externalRootStatus: string;
      readonly lifecycle: unknown;
    };
    readonly next: readonly string[];
    readonly limitations: readonly string[];
  }
  | {
    readonly status: "INVESTIGATION_NOT_PROOF";
    readonly incident: {
      readonly id: string;
      readonly draftDigest: string;
      readonly range: IncidentDraft["range"];
      readonly runtime: { requested: string; image: string } | null;
      readonly frozenDigest: string;
      readonly frozenDigestExternalStatus: string;
    };
    readonly investigation: GitInvestigationResult;
    readonly proofBundle: null;
    readonly next: readonly string[];
  };

/** The frozen witness must be the exact proposal, command, and packet bound by intake. */
export function assertIncidentFrozenWitnessBinding(draft: IncidentDraft, frozenWitness: FrozenWitness): void {
  if (draft.review.witnessState !== "PROPOSED") {
    throw new Error("Incident draft has no proposal binding and cannot continue to investigation.");
  }
  const binding = draft.review.proposal;
  const proposal = frozenWitness.proposal;
  const errors: string[] = [];
  if (proposal.proposalId !== draft.draftId || binding.proposalId !== proposal.proposalId) errors.push("proposal identifier");
  if (binding.proposalDigest !== proposal.proposalDigest) errors.push("proposal digest");
  if (binding.incidentPacketDigest !== proposal.incidentPacketDigest) errors.push("blinded incident-packet digest");
  if (binding.commandDigest !== proposal.witness.commandDigest || draft.commandDigest !== proposal.witness.commandDigest) {
    errors.push("exact command digest");
  }
  if (draft.command !== proposal.witness.command) errors.push("exact command bytes");
  if (errors.length > 0) {
    throw new Error(`Frozen witness does not match the immutable incident draft binding: ${errors.join(", ")}.`);
  }
}

/**
 * Shared freeze → runtime → localization → proof-export path used by
 * `fl incident continue` and the guided `fl investigate --ci-log` workflow.
 */
export async function runFrozenIncidentContinuation(options: {
  readonly draft: IncidentDraft;
  readonly repository: string;
  readonly witnessStore: string;
  readonly expectedFrozenDigest?: string;
  readonly image?: string;
  readonly unsafeLocal?: boolean;
  readonly ledgerFile?: string;
  readonly maxStates?: number;
  readonly outputDirectory?: string;
}): Promise<IncidentContinuationResult> {
  const witnessVerification = verifyFrozenWitness(
    options.witnessStore,
    options.draft.draftId,
    options.expectedFrozenDigest
  );
  if (!witnessVerification.valid) {
    throw new Error(
      `Incident cannot continue until its witness is human-approved and frozen intact: ${witnessVerification.errors.join("; ")}`
    );
  }
  const frozenWitness = readFrozenWitness(options.witnessStore, options.draft.draftId);
  assertIncidentFrozenWitnessBinding(options.draft, frozenWitness);

  const unsafeLocal = options.unsafeLocal === true;
  if (!unsafeLocal && options.expectedFrozenDigest === undefined) {
    throw new Error(
      "Proof-grade incident continuation requires --expect-digest <retained-frozen-digest>. FaultLine will not treat the digest stored beside the witness as an external retention record."
    );
  }
  if (
    options.draft.runtime !== undefined
    && options.image !== undefined
    && options.image !== options.draft.runtime.image
  ) {
    throw new Error("--image must match the digest-pinned runtime recorded in the immutable incident draft.");
  }
  const image = options.image ?? options.draft.runtime?.image;
  if (!unsafeLocal && image === undefined) {
    throw new Error(
      "This incident has no selected runtime. Resolve a reviewed local runtime before intake, or supply --image <digest-pinned-image> for proof-grade replay."
    );
  }

  const lifecycleLedger: CodexLifecycleLedger | undefined = options.ledgerFile === undefined
    ? undefined
    : readVerifiedCodexLifecycleLedger(resolve(options.ledgerFile));

  const investigation = await investigateGitRange({
    repository: options.repository,
    range: { ancestor: options.draft.range.ancestor, descendant: options.draft.range.descendant },
    frozenWitness,
    expectedFrozenDigest: options.expectedFrozenDigest ?? frozenWitness.frozenDigest,
    sandbox: unsafeLocal
      ? { mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }
      : { mode: "DOCKER_ISOLATED", image: image as string },
    ...(options.maxStates === undefined ? {} : { maxStates: options.maxStates })
  });

  const incident = {
    id: options.draft.draftId,
    draftDigest: options.draft.draftDigest,
    range: options.draft.range,
    runtime: options.draft.runtime ?? (image === undefined ? null : { requested: "explicit", image }),
    frozenDigest: frozenWitness.frozenDigest,
    frozenDigestExternalStatus: witnessVerification.externalDigestStatus
  };

  if (!investigation.proof.isProof) {
    return {
      status: "INVESTIGATION_NOT_PROOF",
      incident,
      investigation,
      proofBundle: null,
      next: [
        "Fix the recorded environment or witness condition, then create a new reviewed incident draft rather than altering this frozen witness.",
        "Unsafe-local results are intentionally INAPPLICABLE and cannot publish a portable proof bundle."
      ]
    };
  }

  const proofRoot = resolve(options.repository, ".faultline", "git-proof-bundles");
  const descendant = investigation.resolvedRange?.descendant.commit.slice(0, 12) ?? "unknown";
  const output = resolve(
    options.outputDirectory ?? join(proofRoot, `incident-${options.draft.draftId}-${descendant}-${Date.now()}`)
  );
  const bundle = writeGitInvestigationProofBundle(output, investigation, frozenWitness, {
    proofRoot,
    ...(lifecycleLedger === undefined ? {} : { lifecycleLedger })
  });
  const bundleVerification = verifyGitInvestigationProofBundle(bundle.directory, bundle.rootDigest);
  if (!bundleVerification.valid) {
    throw new Error(`Generated incident proof bundle failed verification: ${bundleVerification.errors.join("; ")}`);
  }

  return {
    status: "PROOF_BUNDLE_READY",
    incident,
    investigation,
    proofBundle: {
      directory: bundle.directory,
      rootDigest: bundle.rootDigest,
      externalRootStatus: bundleVerification.externalRootStatus,
      lifecycle: bundle.manifest.lifecycle
    },
    next: [
      `fl serve --bundle ${bundle.directory} --expect-root ${bundle.rootDigest}`,
      "Retain the bundle root outside the package before relying on rewrite detection or sharing the incident."
    ],
    limitations: [
      "FaultLine executed only the human-frozen witness. It did not infer a remote base, modify the draft, approve a witness, or alter the frozen record.",
      options.expectedFrozenDigest === undefined
        ? "No external frozen-witness digest was supplied; this continuation verified the write-once local witness chain."
        : "The supplied external frozen-witness digest matched the immutable review chain."
    ]
  };
}
