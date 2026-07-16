#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { defaultWitnessProposal, proposeWitnessWithGpt } from "./ai.js";
import {
  defaultAttestationStore,
  verifyStoredBundleAttestation,
  writeBundleAttestation
} from "./attestation.js";
import {
  createGithubProvenanceReceipt,
  defaultGithubProvenanceRoot,
  githubActionsIdentityFromEnvironment,
  readGithubArtifactAttestationTrust,
  verifySignedGithubProvenance,
  writeGithubProvenanceReceipt
} from "./github-provenance.js";
import { createDemoAnalysis } from "./engine.js";
import { runFaultLineDoctor, type FaultLineDoctorReport } from "./doctor.js";
import { createIncidentDraft, type IncidentDraft } from "./incident.js";
import { defaultIncidentDraftStore, readIncidentDraft, writeIncidentDraft } from "./incident-store.js";
import { defaultJudgePreviewPath, writeJudgePreview } from "./judge-preview.js";
import { captureCleanGitSnapshot, writeGitSidecarSnapshot } from "./git-snapshot.js";
import { GitInvestigationResultSchema, investigateGitRange } from "./git-investigation.js";
import {
  defaultGitProofRoot,
  verifyGitInvestigationProofBundle,
  writeGitInvestigationProofBundle
} from "./git-proof-bundle.js";
import { loadVerifiedGitProofView } from "./git-proof-view.js";
import { runLiveGitDemo } from "./live-git-demo.js";
import {
  minimizeGitDiff,
  verifyGitMinimizationResultFile,
  writeGitMinimizationResult
} from "./git-minimization.js";
import {
  createLedgerBoundInvestigation,
  verifyLedgerBoundInvestigationFile,
  writeLedgerBoundInvestigationAtomic
} from "./ledger-binding.js";
import {
  appendLifecycleEvent,
  appendLifecycleEventAtomic,
  captureGitCleanCheckpoint,
  createCodexLifecycleLedger,
  LifecycleEventInputSchema,
  readVerifiedCodexLifecycleLedger,
  verifyCodexLifecycleLedgerFile,
  writeCodexLifecycleLedgerAtomic
} from "./ledger.js";
import { readModelOverlayInput } from "./overlay-input.js";
import { describeBundlePath, verifyProofBundle, writeProofBundle } from "./proof-bundle.js";
import { redactValue } from "./redaction.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";
import { resolveCuratedRuntime } from "./runtime.js";
import {
  createRepairEvidencePacket,
  proposeRepairBriefWithGpt,
  RepairEvidencePacketSchema,
  validateRepairBrief
} from "./repair-brief.js";
import { defaultRepairBriefRoot, verifyRepairBriefArtifact, writeRepairBriefArtifact } from "./repair-brief-store.js";
import { startFaultLineServer, startGitProofServer } from "./server.js";
import { startWitnessReviewServer } from "./witness-review-server.js";
import { openWitnessReview } from "./witness-review.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  readFrozenWitness,
  verifyFrozenWitness,
  type FrozenWitness
} from "./witness-lock.js";
import {
  signAuthenticatedWitnessApproval,
  verifyAuthenticatedWitnessApproval
} from "./authenticated-witness-approval.js";
import type { RunMode } from "./domain.js";

const usage = `FaultLine — executable evidence for agent-assisted code

Usage:
  fl judge-demo [--replay | --rerun-all] [--output <managed-bundle-directory>] [--export-only]
  fl --version
  fl judge-preview [--output <static-preview.html>]
  fl doctor [--repo <directory>] [--json]
  fl incident start --repo <directory> --command <failing-command> [--id <safe-id>] [--from <commit> --to <commit>] [--runtime <node|python|go>] [--store <directory>]
  fl incident status <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--expect-digest <sha256:...>]
  fl incident continue <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--image <digest-pinned-image>] [--expect-digest <sha256:...>] [--ledger <ledger.json>] [--max-states <count>] [--output <managed-bundle-directory>] [--unsafe-local]
  fl runtime resolve <node|python|go>
  fl demo live-git [--image <digest-pinned-image>] [--export-only] [--port <number>]
  fl verify <proof-bundle-directory> [--expect-root <sha256:...>]
  fl serve [--port <number>]
  fl serve --bundle <git-proof-bundle-directory> [--expect-root <sha256:...>] [--minimization <result.json> --expect-minimization <sha256:...>] [--repair <repair-brief-directory> --expect-repair <sha256:...>] [--port <number>]
  fl codex --dry-run | --snapshot [--repo <directory>]
  fl codex record <init|stdin|checkpoint|verify> [...]
  fl record <init|stdin|checkpoint|verify> [...]
  fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--ledger <ledger.json>] [--output <managed-bundle-directory>]
  fl minimize git --repo <directory> --before <commit> --after <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--max-executions <count>] [--output <managed-result.json>]
  fl minimize verify <result.json> [--expect-digest <sha256:...>]
  fl ledger bind --ledger <ledger.json> --investigation <investigation.json> --output <binding.json>
  fl ledger verify <binding.json> [--expect-digest <sha256:...>]
  fl attest create --bundle <proof-bundle-directory> --receipt <id> --subject <label> --issuer <label> [--store <directory>]
  fl attest verify <receipt-id> [--expect-digest <sha256:...>] [--store <directory>]
  fl provenance create --bundle <git-proof-bundle-directory> [--output <managed-receipt.json>]
  fl provenance verify --bundle <git-proof-bundle-directory> --receipt <ci-receipt.json> --attestation-bundle <sigstore-bundle.json> --trust <trust.json>
  fl repair brief (--bundle <git-proof-bundle-directory> | --investigation <verified-bundle>/investigation.json) (--live | --input <repair-brief.json>) [--expect-root <sha256:...>] [--model <model>] [--output <managed-directory>]
  fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]
  fl witness propose --input <proposal.json> [--store <directory>]
  fl witness propose --live --incident <incident.json> --proposal-id <id> --overlay-root <directory> [--model <model>] [--store <directory>]
  fl witness review <proposal-id> [--json | --port <number>] [--store <directory>] [--draft-store <directory>]
  fl witness approve <proposal-id> --approved-by <actor> [--store <directory>]
  fl witness freeze <proposal-id> [--store <directory>]
  fl witness sign <proposal-id> --private-key <ed25519-private.pem> --keyring <trusted-reviewers.json> [--store <directory>]
  fl witness verify <proposal-id> [--expect-digest <sha256:...>] [--keyring <trusted-reviewers.json> --require-signature] [--store <directory>]

The judge demo is a reviewed, deterministic Node fixture. It does not require an OpenAI API key.
The lifecycle adapter accepts observed Codex-compatible events; it does not claim to intercept private Codex internals.
Evidence outputs are intentionally confined to their managed .faultline roots; --output selects a child of that root rather than an arbitrary directory.
Use --live for a GPT-5.6 witness proposal or an inferred repair brief after setting OPENAI_API_KEY.`;

function hasFlag(args: string[], flag: string): boolean {
  return args.includes(flag);
}

function option(args: string[], flag: string): string | undefined {
  const index = args.indexOf(flag);
  return index >= 0 ? args[index + 1] : undefined;
}

function requiredOption(args: string[], flag: string): string {
  const value = option(args, flag);
  if (!value || value.startsWith("--")) throw new Error(`Missing required option: ${flag}`);
  return value;
}

function faultLineVersion(): string {
  try {
    const packageJson = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")) as { version?: unknown };
    return typeof packageJson.version === "string" ? packageJson.version : "unknown";
  } catch {
    return "unknown";
  }
}

function readJsonInput(file: string): unknown {
  try {
    return JSON.parse(readFileSync(resolve(file), "utf8"));
  } catch (error) {
    throw new Error(`Unable to read JSON input ${file}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function witnessStore(args: string[]): string {
  return resolve(option(args, "--store") ?? ".faultline/witnesses");
}

function ledgerPath(args: string[], sessionId?: string): string {
  return resolve(option(args, "--ledger") ?? `.faultline/recordings/${sessionId ?? "session"}.json`);
}

function attestationStore(args: string[]): string {
  return resolve(option(args, "--store") ?? defaultAttestationStore());
}

/** Keep write-once CLI artifacts inside a managed, non-symlink FaultLine root. */
function safeManagedFileOutput(outputFile: string, managedRoot: string, label: string): string {
  const root = resolve(managedRoot);
  const output = resolve(outputFile);
  const nested = relativeTrustedSystemPath(root, output);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error(`${label} output must be a file beneath ${root}`);
  }
  const parts = nested.split(/[\\/]+/).filter(Boolean);
  if (parts.length < 1) throw new Error(`${label} output must have a file name.`);
  let current = root;
  if (existsSync(current)) {
    if (resolveSafeDirectorySegment(current) === null) throw new Error(`${label} root must be a real directory: ${root}`);
  }
  for (const part of parts.slice(0, -1)) {
    current = join(current, part);
    if (!existsSync(current)) continue;
    if (resolveSafeDirectorySegment(current) === null) throw new Error(`${label} output cannot traverse a symbolic link or non-directory: ${current}`);
  }
  if (existsSync(output)) throw new Error(`${label} output already exists and will not be replaced: ${output}`);
  return output;
}

async function readStandardInput(): Promise<string> {
  return new Promise((resolveInput, rejectInput) => {
    let body = "";
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", (chunk: string) => {
      body += chunk;
      if (Buffer.byteLength(body, "utf8") > 4_000_000) {
        rejectInput(new Error("Lifecycle stdin exceeds the 4 MB safety limit."));
      }
    });
    process.stdin.once("error", rejectInput);
    process.stdin.once("end", () => resolveInput(body));
  });
}

function doctorSummary(report: FaultLineDoctorReport): string {
  const diagnostics = report.diagnostics.map((diagnostic) => {
    const observation = diagnostic.observation === null ? "" : ` (${diagnostic.observation})`;
    const remediation = diagnostic.remediation === null ? "" : `\n    Fix: ${diagnostic.remediation}`;
    return `[${diagnostic.status}] ${diagnostic.id}: ${diagnostic.summary}${observation}${remediation}`;
  });
  const runtime = report.likelyRuntime.kind === "UNKNOWN"
    ? "unknown; choose an explicit digest-pinned image"
    : `${report.likelyRuntime.kind.toLowerCase()} (${report.likelyRuntime.markers.join(", ") || "no markers"})`;
  return [
    "FaultLine doctor",
    `Repository: ${report.repositoryRoot ?? report.repository}`,
    `Machine preflight: ${report.dockerInvestigationPreflight}`,
    `Image selection: ${report.imageSelection} (run fl runtime resolve after you choose a reviewed image)`,
    `Likely runtime: ${runtime}`,
    "",
    ...diagnostics,
    "",
    "Limits:",
    ...report.limitations.map((limitation) => `- ${limitation}`)
  ].join("\n");
}

async function doctorCommand(args: string[]): Promise<void> {
  const repository = resolve(option(args, "--repo") ?? process.cwd());
  const report = await runFaultLineDoctor({ repository });
  if (hasFlag(args, "--json")) {
    process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  } else {
    process.stdout.write(`${doctorSummary(report)}\n`);
  }
  process.exitCode = report.dockerInvestigationPreflight === "READY" ? 0 : 1;
}

const INTAKE_SAFE_GIT_CONFIG = [
  "-c", "core.hooksPath=/nonexistent/faultline-hooks",
  "-c", "core.fsmonitor=false",
  "-c", "core.useBuiltinFSMonitor=false",
  "-c", "core.untrackedCache=false",
  "-c", "core.preloadIndex=false",
  "-c", "filter.lfs.process=",
  "-c", "filter.lfs.smudge=",
  "-c", "filter.lfs.required=false",
  "-c", "diff.external=",
  "-c", "submodule.recurse=false",
  "-c", "fetch.recurseSubmodules=false",
  "-c", "protocol.allow=never",
  "-c", "protocol.file.allow=never",
  "-c", "protocol.ext.allow=never",
  "-c", "protocol.git.allow=never",
  "-c", "protocol.ssh.allow=never",
  "-c", "protocol.http.allow=never",
  "-c", "protocol.https.allow=never"
] as const;

/** Read only the local HEAD and its parents; never infer a remote or PR base. */
function locallyObservedHead(repository: string): { head: string; parents: string[] } {
  const result = spawnSync("git", [
    ...INTAKE_SAFE_GIT_CONFIG,
    "-C", repository,
    "show", "-s", "--format=%H%n%P", "HEAD"
  ], {
    encoding: "utf8",
    shell: false,
    windowsHide: true,
    env: {
      PATH: process.env.PATH ?? "",
      GIT_CONFIG_NOSYSTEM: "1",
      GIT_TERMINAL_PROMPT: "0",
      GIT_OPTIONAL_LOCKS: "0",
      GIT_NO_REPLACE_OBJECTS: "1",
      GIT_LFS_SKIP_SMUDGE: "1",
      GIT_ALLOW_PROTOCOL: "none",
      ...(process.platform === "win32" && process.env.SystemRoot ? { SystemRoot: process.env.SystemRoot } : {}),
      ...(process.platform === "win32" && process.env.ComSpec ? { ComSpec: process.env.ComSpec } : {}),
      ...(process.platform === "win32" && process.env.PATHEXT ? { PATHEXT: process.env.PATHEXT } : {})
    }
  });
  if (result.error || result.status !== 0) {
    const detail = `${result.stderr ?? ""}${result.error?.message ?? ""}`.trim();
    throw new Error(`FaultLine could not read a local Git HEAD for incident intake: ${detail || "run fl doctor or supply --from and --to explicitly"}`);
  }
  const lines = String(result.stdout ?? "").replace(/\r/g, "").split("\n");
  const head = lines[0]?.trim();
  const parentLine = lines[1]?.trim() ?? "";
  if (!head) throw new Error("FaultLine could not read a local Git HEAD for incident intake; supply --from and --to explicitly.");
  return { head, parents: parentLine ? parentLine.split(/\s+/).filter(Boolean) : [] };
}

function generatedIncidentId(): string {
  return `incident-${new Date().toISOString().replace(/[^0-9]/g, "").slice(0, 17)}-${randomUUID().slice(0, 12)}`;
}

type IncidentCommandContext = {
  readonly repository: string;
  readonly draftStore: string;
  readonly witnessStore: string;
  readonly draft: IncidentDraft;
};

/** Resolve the durable incident object first; never let continuation swap its repository. */
function loadIncidentCommandContext(args: string[], incidentId: string): IncidentCommandContext {
  const requestedRepository = option(args, "--repo");
  const lookupRepository = resolve(requestedRepository ?? process.cwd());
  const draftStore = resolve(option(args, "--draft-store") ?? defaultIncidentDraftStore(lookupRepository));
  const stored = readIncidentDraft(draftStore, incidentId);
  const repository = resolve(stored.draft.repository);
  if (requestedRepository !== undefined && resolve(requestedRepository) !== repository) {
    throw new Error("Incident continuation refuses a --repo value that differs from the immutable incident draft repository.");
  }
  return {
    repository,
    draftStore,
    witnessStore: resolve(option(args, "--store") ?? join(repository, ".faultline", "witnesses")),
    draft: stored.draft
  };
}

/** The frozen witness must be the exact proposal, command, and packet bound by intake. */
function assertIncidentFrozenWitnessBinding(draft: IncidentDraft, frozenWitness: FrozenWitness): void {
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

function incidentStatusCommand(args: string[]): void {
  const incidentId = args[1];
  if (!incidentId) {
    throw new Error("Usage: fl incident status <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--expect-digest <sha256:...>]");
  }
  const context = loadIncidentCommandContext(args, incidentId);
  const witness = verifyFrozenWitness(context.witnessStore, incidentId, option(args, "--expect-digest"));
  let state: "REVIEW_REQUIRED" | "RETAIN_DIGEST_REQUIRED" | "READY_TO_INVESTIGATE" | "INVALID_FROZEN_WITNESS";
  let bindingError: string | null = null;
  if (!witness.valid) {
    state = witness.errors.some((error) => /missing|not been frozen/i.test(error))
      ? "REVIEW_REQUIRED"
      : "INVALID_FROZEN_WITNESS";
  } else {
    try {
      assertIncidentFrozenWitnessBinding(context.draft, readFrozenWitness(context.witnessStore, incidentId));
      state = witness.externalDigestStatus === "MATCH" ? "READY_TO_INVESTIGATE" : "RETAIN_DIGEST_REQUIRED";
    } catch (error) {
      bindingError = error instanceof Error ? error.message : String(error);
      state = "INVALID_FROZEN_WITNESS";
    }
  }
  process.stdout.write(`${JSON.stringify({
    status: state,
    incident: {
      id: context.draft.draftId,
      draftDigest: context.draft.draftDigest,
      range: context.draft.range,
      runtime: context.draft.runtime ?? null,
      witnessStore: context.witnessStore,
      draftStore: context.draftStore
    },
    frozenWitness: {
      valid: witness.valid,
      frozenDigest: witness.frozenDigest,
      externalDigestStatus: witness.externalDigestStatus,
      approval: witness.approval,
      errors: witness.errors,
      ...(bindingError === null ? {} : { bindingError })
    },
    next: state === "REVIEW_REQUIRED"
      ? [`fl witness review ${incidentId} --store ${context.witnessStore} --draft-store ${context.draftStore}`]
      : state === "RETAIN_DIGEST_REQUIRED"
        ? [
          "Retain the frozen digest shown by the human review/freeze flow outside the witness store.",
          `fl incident status ${incidentId} --repo ${context.repository} --expect-digest <retained-frozen-digest>`
        ]
      : state === "READY_TO_INVESTIGATE"
        ? [`fl incident continue ${incidentId} --repo ${context.repository} --expect-digest <retained-frozen-digest>${context.draft.runtime === undefined ? " --image <digest-pinned-image>" : ""}`]
        : ["Inspect the immutable draft and witness records; FaultLine will not run a mismatched or invalid witness."],
    limitations: [
      "Status reads immutable local records and never executes the stored command, approves a witness, freezes a witness, pulls an image, or runs Docker.",
      "A supplied --expect-digest checks a separately retained frozen-witness digest; without it, status reports local self-consistency only."
    ]
  }, null, 2)}\n`);
  process.exitCode = state === "INVALID_FROZEN_WITNESS" ? 1 : 0;
}

async function continueIncidentCommand(args: string[]): Promise<void> {
  const incidentId = args[1];
  if (!incidentId) {
    throw new Error("Usage: fl incident continue <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--image <digest-pinned-image>] [--expect-digest <sha256:...>] [--ledger <ledger.json>] [--max-states <count>] [--output <managed-bundle-directory>] [--unsafe-local]");
  }
  const context = loadIncidentCommandContext(args, incidentId);
  const expectedFrozenDigest = option(args, "--expect-digest");
  const witnessVerification = verifyFrozenWitness(context.witnessStore, incidentId, expectedFrozenDigest);
  if (!witnessVerification.valid) {
    throw new Error(`Incident cannot continue until its witness is human-approved and frozen intact: ${witnessVerification.errors.join("; ")}`);
  }
  const frozenWitness = readFrozenWitness(context.witnessStore, incidentId);
  assertIncidentFrozenWitnessBinding(context.draft, frozenWitness);

  const unsafeLocal = hasFlag(args, "--unsafe-local");
  if (!unsafeLocal && expectedFrozenDigest === undefined) {
    throw new Error("Proof-grade incident continuation requires --expect-digest <retained-frozen-digest>. FaultLine will not treat the digest stored beside the witness as an external retention record.");
  }
  const requestedImage = option(args, "--image");
  if (context.draft.runtime !== undefined && requestedImage !== undefined && requestedImage !== context.draft.runtime.image) {
    throw new Error("--image must match the digest-pinned runtime recorded in the immutable incident draft.");
  }
  const image = requestedImage ?? context.draft.runtime?.image;
  if (!unsafeLocal && image === undefined) {
    throw new Error("This incident has no selected runtime. Resolve a reviewed local runtime before intake, or supply --image <digest-pinned-image> for proof-grade replay.");
  }

  const ledgerFile = option(args, "--ledger");
  const lifecycleLedger = ledgerFile === undefined ? undefined : readVerifiedCodexLifecycleLedger(resolve(ledgerFile));
  const maxStates = option(args, "--max-states");
  const investigation = await investigateGitRange({
    repository: context.repository,
    range: { ancestor: context.draft.range.ancestor, descendant: context.draft.range.descendant },
    frozenWitness,
    // investigateGitRange requires a digest-shaped comparison input even for
    // unsafe-local diagnostics. The fallback is reachable only in that
    // INAPPLICABLE/non-proof mode; proof-grade continuation above requires a
    // separately retained external digest.
    expectedFrozenDigest: expectedFrozenDigest ?? frozenWitness.frozenDigest,
    sandbox: unsafeLocal
      ? { mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }
      : { mode: "DOCKER_ISOLATED", image: image as string },
    ...(maxStates === undefined ? {} : { maxStates: Number(maxStates) })
  });

  const incident = {
    id: context.draft.draftId,
    draftDigest: context.draft.draftDigest,
    range: context.draft.range,
    runtime: context.draft.runtime ?? (image === undefined ? null : { requested: "explicit", image }),
    frozenDigest: frozenWitness.frozenDigest,
    frozenDigestExternalStatus: witnessVerification.externalDigestStatus
  };
  if (!investigation.proof.isProof) {
    process.stdout.write(`${JSON.stringify({
      status: "INVESTIGATION_NOT_PROOF",
      incident,
      investigation,
      proofBundle: null,
      next: [
        "Fix the recorded environment or witness condition, then create a new reviewed incident draft rather than altering this frozen witness.",
        "Unsafe-local results are intentionally INAPPLICABLE and cannot publish a portable proof bundle."
      ]
    }, null, 2)}\n`);
    process.exitCode = 1;
    return;
  }

  const proofRoot = resolve(context.repository, ".faultline", "git-proof-bundles");
  const descendant = investigation.resolvedRange?.descendant.commit.slice(0, 12) ?? "unknown";
  const output = resolve(option(args, "--output") ?? join(proofRoot, `incident-${incidentId}-${descendant}-${Date.now()}`));
  const bundle = writeGitInvestigationProofBundle(output, investigation, frozenWitness, {
    proofRoot,
    ...(lifecycleLedger === undefined ? {} : { lifecycleLedger })
  });
  const bundleVerification = verifyGitInvestigationProofBundle(bundle.directory, bundle.rootDigest);
  if (!bundleVerification.valid) {
    throw new Error(`Generated incident proof bundle failed verification: ${bundleVerification.errors.join("; ")}`);
  }
  process.stdout.write(`${JSON.stringify({
    status: "PROOF_BUNDLE_READY",
    incident,
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
      expectedFrozenDigest === undefined
        ? "No external frozen-witness digest was supplied; this continuation verified the write-once local witness chain."
        : "The supplied external frozen-witness digest matched the immutable review chain."
    ]
  }, null, 2)}\n`);
}

async function incidentCommand(args: string[]): Promise<void> {
  if (args[0] === "status") return incidentStatusCommand(args);
  if (args[0] === "continue") return continueIncidentCommand(args);
  if (args[0] !== "start") {
    throw new Error("Usage: fl incident start|status|continue ...");
  }
  const repository = resolve(requiredOption(args, "--repo"));
  const command = requiredOption(args, "--command");
  const from = option(args, "--from");
  const to = option(args, "--to");
  if ((from === undefined) !== (to === undefined)) {
    throw new Error("Incident intake accepts --from and --to together, or neither for the conservative local HEAD-parent fallback.");
  }
  const draftId = option(args, "--id") ?? generatedIncidentId();
  const requestedRuntime = option(args, "--runtime");
  const timeoutSeconds = Number(option(args, "--timeout-seconds") ?? "300");
  if (!Number.isInteger(timeoutSeconds) || timeoutSeconds < 1 || timeoutSeconds > 3_600) {
    throw new Error("--timeout-seconds must be an integer from 1 through 3600.");
  }
  const runtime = requestedRuntime === undefined ? undefined : await resolveCuratedRuntime(requestedRuntime);
  // Even an explicit bracket must belong to a local Git worktree.  The
  // observed facts are used only for the no-range fallback; they never make
  // FaultLine select a remote base.
  const localHead = locallyObservedHead(repository);
  const draftInput = {
    draftId,
    createdAt: new Date().toISOString(),
    repository,
    command,
    ...(from === undefined || to === undefined
      ? { localHead }
      : { range: { ancestor: from, descendant: to } }),
    ...(runtime === undefined ? {} : { runtime: { requested: runtime.requested, image: runtime.image } })
  };
  // Resolve the range before creating the proposal so the proposal's blinded
  // packet can describe the exact locally selected bracket. This preliminary
  // value is never persisted; the persisted draft below binds the immutable
  // proposal facts returned by the write-once witness store.
  const preliminaryDraft = createIncidentDraft(draftInput);
  const store = resolve(option(args, "--store") ?? join(repository, ".faultline", "witnesses"));
  const proposal = proposeWitness(store, {
    proposalId: preliminaryDraft.draftId,
    proposalOrigin: "HUMAN",
    incidentPacket: {
      symptom: option(args, "--symptom") ?? "A human-reported command is failing and requires a reviewed, frozen witness before localization.",
      ciLog: "The exact user-supplied command is stored in the reviewable witness, not executed by incident intake.",
      repositoryLanguage: option(args, "--language") ?? "Unknown",
      repositorySummary: `Review-only local Git range ${preliminaryDraft.range.ancestor} to ${preliminaryDraft.range.descendant} (${preliminaryDraft.range.source}). No remote base was selected.${preliminaryDraft.runtime === undefined ? "" : ` The selected local runtime resolves to ${preliminaryDraft.runtime.image}.`}`
    },
    witness: {
      behavior: option(args, "--behavior") ?? "The human-supplied command must exit successfully at the selected immutable Git states.",
      command,
      overlays: [],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds }
    }
  });
  const draft = createIncidentDraft({
    ...draftInput,
    proposal: {
      proposalId: proposal.proposalId,
      proposalDigest: proposal.proposalDigest,
      incidentPacketDigest: proposal.incidentPacketDigest,
      commandDigest: proposal.witness.commandDigest
    }
  });
  const draftStore = resolve(option(args, "--draft-store") ?? defaultIncidentDraftStore(repository));
  const storedDraft = writeIncidentDraft(draftStore, draft);
  process.stdout.write(`${JSON.stringify({
    status: "DRAFT_REQUIRES_HUMAN_REVIEW",
    draft: {
      path: storedDraft.path,
      digest: draft.draftDigest,
      range: draft.range,
      commandDigest: draft.commandDigest,
      runtime: draft.runtime ?? null,
      review: draft.review
    },
    witnessProposal: {
      store,
      proposalId: proposal.proposalId,
      proposalDigest: proposal.proposalDigest,
      incidentPacketDigest: proposal.incidentPacketDigest,
      commandDigest: proposal.witness.commandDigest,
      overlays: proposal.witness.overlays.length,
      state: "NOT_APPROVED_NOT_FROZEN"
    },
    next: [
      `fl witness review ${proposal.proposalId} --store ${store} --draft-store ${draftStore} (starts the local human review workbench and prints its URL; approval and freeze are separate explicit clicks)`,
      draft.runtime === undefined
        ? `Resolve a local curated runtime with fl runtime resolve <node|python|go>, then after human freeze provide a retained --expect-digest and explicit digest-pinned image to fl incident continue ${proposal.proposalId}.`
        : `After human freeze, run fl incident continue ${proposal.proposalId} --expect-digest <retained-frozen-digest> to reuse the reviewed range and selected image ${draft.runtime.image}.`,
      "Proof-grade replay additionally requires a Docker daemon; FaultLine will not treat local debug as proof."
    ],
    limitations: [
      "Incident intake did not execute the command, pull an image, query a remote branch, approve a witness, or freeze a witness.",
      "The local default is only the exactly observed single-parent HEAD range; merge and root HEADs require an explicit range.",
      "A local debug result is not proof-grade. Only completed Docker-isolated replay can produce a portable proof package."
    ]
  }, null, 2)}\n`);
}

async function runtimeCommand(args: string[]): Promise<void> {
  if (args[0] !== "resolve" || !args[1]) {
    throw new Error("Usage: fl runtime resolve <node|python|go>");
  }
  if (hasFlag(args, "--pull")) {
    throw new Error("FaultLine never pulls a runtime image implicitly. Pull a reviewed catalog tag yourself, then run fl runtime resolve again to record its local digest.");
  }
  const resolution = await resolveCuratedRuntime(args[1]);
  process.stdout.write(`${JSON.stringify({
    status: "RESOLVED_LOCAL_DIGEST",
    runtime: resolution.runtime,
    requested: resolution.requested,
    image: resolution.image,
    next: `Run fl incident start ... --runtime ${resolution.runtime.alias} to persist this resolved image, or after a human freeze pass the image value and retained --expect-digest to fl incident continue <id> --image.`
  }, null, 2)}\n`);
}

async function judgeDemo(args: string[]): Promise<void> {
  const mode: RunMode = hasFlag(args, "--rerun-all") ? "RERUN" : "REPLAY";
  const outputDirectory = resolve(option(args, "--output") ?? ".faultline/bundles/judge-demo");
  const analysis = createDemoAnalysis(mode);
  const bundle = writeProofBundle(outputDirectory, analysis);
  const verification = verifyProofBundle(bundle.directory);
  if (!verification.valid) throw new Error(`Generated proof bundle failed integrity verification: ${verification.errors.join("; ")}`);
  const firstStableBoundary = analysis.transitions.find((transition) => transition.kind === "PASS_TO_FAIL" && transition.stable);
  process.stdout.write(`FaultLine ${mode === "RERUN" ? "executed" : "cached replay"} investigation prepared.\n`);
  process.stdout.write(firstStableBoundary
    ? `Stable sample boundary: ${firstStableBoundary.beforeStateId} → ${firstStableBoundary.afterStateId}\n`
    : "Stable sample boundary: not certified from cached replay\n");
  process.stdout.write(`Proof bundle self-consistency: VALID (${verification.checkedFiles} declared files)\n`);
  process.stdout.write(`Bundle root: ${bundle.rootDigest}\n`);
  process.stdout.write(`Proof bundle: ${describeBundlePath(bundle.directory)}\n`);
  if (hasFlag(args, "--export-only")) return;
  const server = await startFaultLineServer({ analysis, outputDirectory: bundle.directory, port: Number(option(args, "--port") ?? "4173") });
  process.stdout.write(`Open ${server.url} to inspect the incident page. Press Ctrl+C to stop.\n`);
  await new Promise<void>((resolveExit) => {
    process.once("SIGINT", () => {
      void server.close().finally(resolveExit);
    });
  });
}

async function witnessProposal(args: string[]): Promise<void> {
  const packet = {
    symptom: "A completed refund may overwrite settlement currency with display currency.",
    ciLog: "Expected settlement currency USD; received EUR.",
    repositoryLanguage: "JavaScript",
    repositorySummary: "A Node service that completes refunds."
  };
  if (!hasFlag(args, "--live")) {
    process.stdout.write(`${JSON.stringify({ source: "sample", proposal: defaultWitnessProposal(), note: "Pass --live to call GPT-5.6 with a blinded incident packet." }, null, 2)}\n`);
    return;
  }
  const result = await proposeWitnessWithGpt(packet, { model: option(args, "--model") ?? "gpt-5.6" });
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
}

function judgePreviewCommand(args: string[]): void {
  const output = option(args, "--output") === undefined
    ? defaultJudgePreviewPath()
    : resolve(requiredOption(args, "--output"));
  const preview = writeJudgePreview(output);
  process.stdout.write(`FaultLine deterministic static judge preview written.\n`);
  process.stdout.write(`Preview: ${preview.path}\n`);
  process.stdout.write(`Bytes: ${preview.bytes}\n`);
  process.stdout.write("Limitation: this read-only replay snapshot is not a live Docker proof, a verified proof bundle, or a record of a fresh execution.\n");
}

/** Run the real Git/Docker product path against a disposable built-in incident. */
async function demoCommand(args: string[]): Promise<void> {
  if (args[0] !== "live-git") {
    throw new Error("Usage: fl demo live-git [--image <digest-pinned-image>] [--export-only] [--port <number>]");
  }
  const requestedImage = option(args, "--image");
  const demo = await runLiveGitDemo({
    workspace: process.cwd(),
    ...(requestedImage === undefined ? {} : { image: requestedImage })
  });
  if (demo.proofBundle === null) {
    process.stdout.write(`${JSON.stringify({
      status: "DEMO_NOT_PROVEN",
      directory: demo.directory,
      repository: demo.repository,
      image: demo.image,
      investigationStatus: demo.investigation.status,
      proof: demo.investigation.proof,
      errors: demo.investigation.errors,
      limitation: "No portable package was published because the native Docker executions did not establish a stable proof."
    }, null, 2)}\n`);
    process.exitCode = 1;
    return;
  }
  const proof = loadVerifiedGitProofView(demo.proofBundle.directory, demo.proofBundle.rootDigest);
  process.stdout.write(`${JSON.stringify({
    status: "DEMO_PROOF_READY",
    directory: demo.directory,
    repository: demo.repository,
    image: demo.image,
    frozenWitnessDigest: demo.frozenWitness.frozenDigest,
    proofBundle: {
      directory: demo.proofBundle.directory,
      rootDigest: demo.proofBundle.rootDigest,
      verified: true,
      transitions: proof.investigation.transitions.length
    },
    sensitivity: "The portable package intentionally retains the frozen witness and recorded evidence. Treat it as sensitive incident material before sharing."
  }, null, 2)}\n`);
  if (hasFlag(args, "--export-only")) return;
  const server = await startGitProofServer({ proof, port: Number(option(args, "--port") ?? "4173") });
  process.stdout.write(`FaultLine live Git proof page: ${server.url}\nPress Ctrl+C to stop.\n`);
  await new Promise<void>((resolveExit) => {
    process.once("SIGINT", () => {
      void server.close().finally(resolveExit);
    });
  });
}

async function witnessCommand(args: string[]): Promise<void> {
  const [action, proposalId] = args;
  const store = witnessStore(args);
  switch (action) {
    case "review": {
      if (!proposalId) throw new Error("Usage: fl witness review <proposal-id> [--json | --port <number>] [--draft-store <directory>]");
      if (hasFlag(args, "--json")) {
        const review = openWitnessReview(store, proposalId);
        process.stdout.write(`${JSON.stringify({
          status: "LOCAL_READ_ONLY_REVIEW",
          review,
          sensitivity: "This export contains the exact command and base64 overlay bytes. Treat it as sensitive incident material and do not publish it."
        }, null, 2)}\n`);
        return;
      }
      const port = Number(option(args, "--port") ?? "0");
      if (!Number.isInteger(port) || port < 0 || port > 65_535) {
        throw new Error("--port must be an integer from 0 through 65535.");
      }
      const draftStore = resolve(option(args, "--draft-store") ?? defaultIncidentDraftStore());
      const server = await startWitnessReviewServer({ store, proposalId, draftStore, port });
      process.stdout.write(`FaultLine local witness review: ${server.url}\nReview the exact command, overlays, and policy. Approve and freeze require separate explicit clicks. Press Ctrl+C to stop.\n`);
      await new Promise<void>((resolveExit) => {
        process.once("SIGINT", () => {
          void server.close().finally(resolveExit);
        });
      });
      return;
    }
    case "propose": {
      const liveIncident = option(args, "--incident");
      if (hasFlag(args, "--live") && liveIncident) {
        const incident = readJsonInput(liveIncident);
        const proposalId = requiredOption(args, "--proposal-id");
        const overlayRoot = requiredOption(args, "--overlay-root");
        const generated = await proposeWitnessWithGpt(incident, { model: option(args, "--model") ?? "gpt-5.6" });
        const redactedIncident = redactValue(incident);
        const proposal = proposeWitness(store, {
          proposalId,
          proposalOrigin: "MODEL",
          incidentPacket: redactedIncident.value,
          witness: {
            behavior: generated.proposal.behavior,
            command: generated.proposal.command,
            overlays: readModelOverlayInput(overlayRoot, generated.proposal.overlayFiles),
            policy: { network: "disabled", credentials: "redacted", timeoutSeconds: generated.proposal.timeoutSeconds }
          }
        });
        process.stdout.write(`${JSON.stringify({ status: "PROPOSED", source: "GPT-5.6", store, proposalId: proposal.proposalId, proposalDigest: proposal.proposalDigest, incidentPacketDigest: proposal.incidentPacketDigest, privacy: generated.redaction }, null, 2)}\n`);
        return;
      }
      const input = option(args, "--input");
      if (!input) {
        await witnessProposal(args.slice(1));
        return;
      }
      const proposalInput = readJsonInput(input);
      const incidentPacket = typeof proposalInput === "object" && proposalInput !== null && !Array.isArray(proposalInput)
        ? (proposalInput as Record<string, unknown>).incidentPacket
        : undefined;
      const redacted = redactValue(incidentPacket);
      const safeProposalInput = typeof proposalInput === "object" && proposalInput !== null && !Array.isArray(proposalInput)
        ? { ...(proposalInput as Record<string, unknown>), incidentPacket: redacted.value }
        : proposalInput;
      const proposal = proposeWitness(store, safeProposalInput);
      process.stdout.write(`${JSON.stringify({ status: "PROPOSED", store, proposalId: proposal.proposalId, proposalDigest: proposal.proposalDigest, incidentPacketDigest: proposal.incidentPacketDigest, privacy: redacted.report }, null, 2)}\n`);
      return;
    }
    case "approve": {
      if (!proposalId) throw new Error("Usage: fl witness approve <proposal-id> --approved-by <actor>");
      const approval = approveWitnessProposal(store, proposalId, {
        approvedBy: requiredOption(args, "--approved-by"),
        ...(option(args, "--note") ? { note: option(args, "--note") } : {})
      });
      process.stdout.write(`${JSON.stringify({ status: "APPROVED", proposalId, approvalDigest: approval.approvalDigest, approvedBy: approval.approvedBy, approvedAt: approval.approvedAt }, null, 2)}\n`);
      return;
    }
    case "freeze": {
      if (!proposalId) throw new Error("Usage: fl witness freeze <proposal-id>");
      const frozen = freezeApprovedWitness(store, proposalId);
      process.stdout.write(`${JSON.stringify({ status: "FROZEN", proposalId, witnessDigest: frozen.witnessDigest, frozenDigest: frozen.frozenDigest, frozenAt: frozen.frozenAt }, null, 2)}\n`);
      return;
    }
    case "sign": {
      if (!proposalId) throw new Error("Usage: fl witness sign <proposal-id> --private-key <ed25519-private.pem> --keyring <trusted-reviewers.json>");
      const privateKeyFile = resolve(requiredOption(args, "--private-key"));
      let privateKeyPem: string;
      try {
        privateKeyPem = readFileSync(privateKeyFile, "utf8");
      } catch (error) {
        throw new Error(`Unable to read reviewer private key ${privateKeyFile}: ${error instanceof Error ? error.message : String(error)}`);
      }
      const receipt = signAuthenticatedWitnessApproval(store, proposalId, {
        privateKeyPem,
        keyring: readJsonInput(requiredOption(args, "--keyring"))
      });
      process.stdout.write(`${JSON.stringify({
        status: "AUTHENTICATED_APPROVAL_RECORDED",
        proposalId,
        frozenDigest: receipt.frozenDigest,
        keyId: receipt.keyId,
        signedAt: receipt.signedAt,
        receiptDigest: receipt.receiptDigest,
        limitation: "The private key is never stored or printed. Trust comes only from the supplied reviewer keyring."
      }, null, 2)}\n`);
      return;
    }
    case "verify": {
      if (!proposalId) throw new Error("Usage: fl witness verify <proposal-id> [--expect-digest <sha256:...>]");
      const keyringFile = option(args, "--keyring");
      const requireSignature = hasFlag(args, "--require-signature");
      if (requireSignature && !keyringFile) {
        throw new Error("--require-signature needs --keyring <trusted-reviewers.json>.");
      }
      const expectedFrozenDigest = option(args, "--expect-digest");
      const result = keyringFile
        ? verifyAuthenticatedWitnessApproval(store, proposalId, {
          ...(expectedFrozenDigest === undefined ? {} : { expectedFrozenDigest }),
          keyring: readJsonInput(keyringFile),
          requireSignature
        })
        : verifyFrozenWitness(store, proposalId, expectedFrozenDigest);
      process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
      process.exitCode = result.valid ? 0 : 1;
      return;
    }
    default:
      throw new Error("Usage: fl witness propose|review|approve|freeze|sign|verify ...");
  }
}

function lifecycleInputFromLine(value: unknown, repository?: string): unknown {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return value;
  const envelope = value as Record<string, unknown>;
  const event = typeof envelope.event === "object" && envelope.event !== null ? envelope.event as Record<string, unknown> : envelope;
  if (event.type !== "WORKTREE_CHECKPOINT") return event;
  const payload = typeof event.payload === "object" && event.payload !== null ? event.payload as Record<string, unknown> : undefined;
  if (!payload || "checkpoint" in payload) return event;
  if (!repository) throw new Error("A --repo is required when stdin asks FaultLine to capture a WORKTREE_CHECKPOINT.");
  return {
    type: "WORKTREE_CHECKPOINT",
    payload: {
      afterTurnOrdinal: payload.afterTurnOrdinal,
      checkpoint: captureGitCleanCheckpoint(repository)
    }
  };
}

async function recordCommand(args: string[]): Promise<void> {
  const [action] = args;
  switch (action) {
    case "init": {
      const sessionId = requiredOption(args, "--session");
      const file = ledgerPath(args, sessionId);
      if (existsSync(file)) throw new Error(`Refusing to overwrite an existing lifecycle ledger: ${file}`);
      const repository = resolve(option(args, "--repo") ?? process.cwd());
      let ledger = createCodexLifecycleLedger({ sessionId });
      ledger = appendLifecycleEvent(ledger, {
        type: "SESSION_STARTED",
        payload: {
          transport: (option(args, "--transport") ?? "SIDE_CAR") as "CODEX_CLI" | "CODEX_APP" | "SIDE_CAR",
          workingDirectory: repository,
          ...(option(args, "--thread") ? { codexThreadId: option(args, "--thread") } : {}),
          ...(option(args, "--model") ? { model: option(args, "--model") } : {})
        }
      });
      writeCodexLifecycleLedgerAtomic(file, ledger);
      process.stdout.write(`${JSON.stringify({ status: "RECORDING_STARTED", ledger: file, sessionId, headHash: ledger.events.at(-1)?.hash }, null, 2)}\n`);
      return;
    }
    case "checkpoint": {
      const file = ledgerPath(args);
      const repository = resolve(requiredOption(args, "--repo"));
      const afterTurnOrdinal = Number(requiredOption(args, "--after-turn"));
      if (!Number.isInteger(afterTurnOrdinal) || afterTurnOrdinal < 0) throw new Error("--after-turn must be a non-negative integer.");
      const ledger = appendLifecycleEventAtomic(file, {
        type: "WORKTREE_CHECKPOINT",
        payload: { checkpoint: captureGitCleanCheckpoint(repository), afterTurnOrdinal }
      });
      process.stdout.write(`${JSON.stringify({ status: "CHECKPOINT_RECORDED", ledger: file, headHash: ledger.events.at(-1)?.hash }, null, 2)}\n`);
      return;
    }
    case "stdin": {
      const file = ledgerPath(args);
      const repository = option(args, "--repo") ? resolve(requiredOption(args, "--repo")) : undefined;
      const lines = (await readStandardInput()).split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
      if (lines.length === 0) throw new Error("Lifecycle stdin contained no NDJSON events.");
      for (const line of lines) {
        let envelope: unknown;
        try {
          envelope = JSON.parse(line);
        } catch (error) {
          throw new Error(`Lifecycle stdin contains invalid JSON: ${error instanceof Error ? error.message : String(error)}`);
        }
        const input = LifecycleEventInputSchema.parse(lifecycleInputFromLine(envelope, repository));
        const metadata = typeof envelope === "object" && envelope !== null && !Array.isArray(envelope) ? envelope as Record<string, unknown> : {};
        appendLifecycleEventAtomic(file, input, {
          ...(typeof metadata.eventId === "string" ? { eventId: metadata.eventId } : {}),
          ...(typeof metadata.occurredAt === "string" ? { occurredAt: metadata.occurredAt } : {})
        });
      }
      const verification = verifyCodexLifecycleLedgerFile(file);
      process.stdout.write(`${JSON.stringify({ status: verification.valid ? "EVENTS_RECORDED" : "INVALID", ledger: file, ...verification }, null, 2)}\n`);
      process.exitCode = verification.valid ? 0 : 1;
      return;
    }
    case "verify": {
      const file = ledgerPath(args);
      const verification = verifyCodexLifecycleLedgerFile(file);
      process.stdout.write(`${JSON.stringify({ ledger: file, ...verification }, null, 2)}\n`);
      process.exitCode = verification.valid ? 0 : 1;
      return;
    }
    default:
      throw new Error("Usage: fl record init|stdin|checkpoint|verify ...");
  }
}

async function investigateCommand(args: string[]): Promise<void> {
  if (args[0] !== "git") {
    throw new Error("Usage: fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--ledger <ledger.json>] [--output <bundle-directory>]");
  }
  const store = witnessStore(args);
  const proposalId = requiredOption(args, "--proposal");
  const unsafeLocal = hasFlag(args, "--unsafe-local");
  const maxStates = option(args, "--max-states");
  const frozenWitness = readFrozenWitness(store, proposalId);
  const ledgerFile = option(args, "--ledger");
  const lifecycleLedger = ledgerFile === undefined ? undefined : readVerifiedCodexLifecycleLedger(resolve(ledgerFile));
  const result = await investigateGitRange({
    repository: resolve(requiredOption(args, "--repo")),
    range: { ancestor: requiredOption(args, "--from"), descendant: requiredOption(args, "--to") },
    frozenWitness,
    expectedFrozenDigest: requiredOption(args, "--expect-digest"),
    sandbox: unsafeLocal
      ? { mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }
      : { mode: "DOCKER_ISOLATED", image: requiredOption(args, "--image") },
    ...(maxStates === undefined ? {} : { maxStates: Number(maxStates) })
  });
  if (!result.proof.isProof) {
    process.stdout.write(`${JSON.stringify({ investigation: result, proofBundle: null, note: "No portable proof bundle was written because the investigation did not establish Docker-isolated proof." }, null, 2)}\n`);
    process.exitCode = 1;
    return;
  }
  const descendant = result.resolvedRange?.descendant.commit.slice(0, 12) ?? "unknown";
  const output = resolve(option(args, "--output") ?? join(defaultGitProofRoot(), `investigation-${descendant}-${Date.now()}`));
  const bundle = writeGitInvestigationProofBundle(
    output,
    result,
    frozenWitness,
    lifecycleLedger === undefined ? {} : { lifecycleLedger }
  );
  const bundleVerification = verifyGitInvestigationProofBundle(bundle.directory, bundle.rootDigest);
  if (!bundleVerification.valid) {
    throw new Error(`Generated Git proof bundle failed verification: ${bundleVerification.errors.join("; ")}`);
  }
  const bindingOutput = option(args, "--ledger-binding-output");
  let ledgerBinding: { path: string; bindingDigest: string } | undefined;
  if (lifecycleLedger !== undefined && bindingOutput !== undefined) {
    const binding = createLedgerBoundInvestigation(lifecycleLedger, result);
    const path = writeLedgerBoundInvestigationAtomic(resolve(bindingOutput), binding);
    ledgerBinding = { path, bindingDigest: binding.bindingDigest };
  }
  process.stdout.write(`${JSON.stringify({
    investigation: result,
    proofBundle: {
      directory: bundle.directory,
      rootDigest: bundle.rootDigest,
      lifecycle: bundle.manifest.lifecycle,
      externalRootStatus: bundleVerification.externalRootStatus
    },
    ...(ledgerBinding === undefined ? {} : { ledgerBinding })
  }, null, 2)}\n`);
  process.exitCode = result.proof.isProof ? 0 : 1;
}

async function minimizeCommand(args: string[]): Promise<void> {
  if (args[0] === "verify") {
    const file = args[1];
    if (!file) throw new Error("Usage: fl minimize verify <result.json> [--expect-digest <sha256:...>]");
    const verification = verifyGitMinimizationResultFile(resolve(file), option(args, "--expect-digest"));
    process.stdout.write(`${JSON.stringify({
      valid: verification.valid,
      resultDigest: verification.resultDigest,
      externalDigestStatus: verification.externalDigestStatus,
      ...(verification.result === undefined ? {} : {
        status: verification.result.status,
        proof: verification.result.proof,
        usedExecutions: verification.result.budget.usedExecutions
      }),
      errors: verification.errors,
      limitation: "Verification checks the stored minimization record without rerunning Git, Docker, or repository code. An external digest detects rewrites but is not a signature, identity assertion, or host-attestation claim."
    }, null, 2)}\n`);
    process.exitCode = verification.valid ? 0 : 1;
    return;
  }
  if (args[0] !== "git") {
    throw new Error("Usage: fl minimize git|verify ...");
  }
  const proposalId = requiredOption(args, "--proposal");
  const unsafeLocal = hasFlag(args, "--unsafe-local");
  const maxExecutions = option(args, "--max-executions");
  const result = await minimizeGitDiff({
    repository: resolve(requiredOption(args, "--repo")),
    before: requiredOption(args, "--before"),
    after: requiredOption(args, "--after"),
    frozenWitness: readFrozenWitness(witnessStore(args), proposalId),
    expectedFrozenDigest: requiredOption(args, "--expect-digest"),
    sandbox: unsafeLocal
      ? { mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }
      : { mode: "DOCKER_ISOLATED", image: requiredOption(args, "--image") },
    ...(maxExecutions === undefined ? {} : { budget: { maxExecutions: Number(maxExecutions) } })
  });
  const output = safeManagedFileOutput(
    option(args, "--output") ?? join(".faultline", "minimizations", `minimization-${Date.now()}.json`),
    resolve(".faultline", "minimizations"),
    "Git minimization"
  );
  const written = await writeGitMinimizationResult(output, result);
  process.stdout.write(`${JSON.stringify({
    minimization: result,
    resultFile: written.path,
    resultDigest: written.resultDigest,
    proof: result.proof.isProof ? "BIDIRECTIONALLY_CERTIFIED" : "NOT_CERTIFIED",
    verification: "SELF_CONSISTENT",
    limitation: "Retain resultDigest outside this JSON before relying on it for rewrite detection. It is not a cryptographic signature, identity assertion, or host-attestation claim."
  }, null, 2)}\n`);
  process.exitCode = result.proof.isProof ? 0 : 1;
}

async function ledgerCommand(args: string[]): Promise<void> {
  const [action, first] = args;
  switch (action) {
    case "bind": {
      const ledger = readVerifiedCodexLifecycleLedger(resolve(requiredOption(args, "--ledger")));
      const investigation = GitInvestigationResultSchema.parse(readJsonInput(requiredOption(args, "--investigation")));
      const binding = createLedgerBoundInvestigation(ledger, investigation);
      const path = writeLedgerBoundInvestigationAtomic(resolve(requiredOption(args, "--output")), binding);
      process.stdout.write(`${JSON.stringify({ status: "BOUND", path, bindingDigest: binding.bindingDigest, stateCount: binding.stateBindings.length }, null, 2)}\n`);
      return;
    }
    case "verify": {
      if (!first) throw new Error("Usage: fl ledger verify <binding.json> [--expect-digest <sha256:...>]");
      const verification = verifyLedgerBoundInvestigationFile(resolve(first), option(args, "--expect-digest"));
      process.stdout.write(`${JSON.stringify(verification, null, 2)}\n`);
      process.exitCode = verification.valid ? 0 : 1;
      return;
    }
    default:
      throw new Error("Usage: fl ledger bind|verify ...");
  }
}

function proofBundleSummary(directory: string): { rootDigest: string; witnessDigest?: string; kind: "GIT" | "DEMO" } {
  const root = resolve(directory);
  let schemaVersion: string | undefined;
  try {
    const value = JSON.parse(readFileSync(join(root, "manifest.json"), "utf8")) as { schemaVersion?: unknown };
    schemaVersion = typeof value.schemaVersion === "string" ? value.schemaVersion : undefined;
  } catch {
    // Fall through to the existing offline verifier, which returns its own safe error.
  }
  if (schemaVersion === "faultline.git-proof-bundle.v1") {
    const verification = verifyGitInvestigationProofBundle(root);
    if (!verification.valid || !verification.rootDigest) throw new Error(`Git proof bundle is invalid: ${verification.errors.join("; ")}`);
    return {
      rootDigest: verification.rootDigest,
      kind: "GIT",
      ...(verification.manifest?.witnessDigest === undefined ? {} : { witnessDigest: verification.manifest.witnessDigest })
    };
  }
  const verification = verifyProofBundle(root);
  if (!verification.valid || !verification.rootDigest) throw new Error(`Proof bundle is invalid: ${verification.errors.join("; ")}`);
  return {
    rootDigest: verification.rootDigest,
    kind: "DEMO",
    ...(verification.manifest?.witnessDigest === undefined ? {} : { witnessDigest: verification.manifest.witnessDigest })
  };
}

async function attestationCommand(args: string[]): Promise<void> {
  const [action, receiptId] = args;
  const store = attestationStore(args);
  switch (action) {
    case "create": {
      const bundle = proofBundleSummary(requiredOption(args, "--bundle"));
      const receipt = writeBundleAttestation(store, {
        receiptId: requiredOption(args, "--receipt"),
        subject: requiredOption(args, "--subject"),
        issuer: requiredOption(args, "--issuer"),
        bundleRootDigest: bundle.rootDigest,
        ...(bundle.witnessDigest === undefined ? {} : { witnessDigest: bundle.witnessDigest }),
        ...(option(args, "--source-digest") === undefined ? {} : { sourceDigest: option(args, "--source-digest") }),
        ...(option(args, "--checkpoint-digest") === undefined ? {} : { gitCheckpointDigest: option(args, "--checkpoint-digest") })
      });
      process.stdout.write(`${JSON.stringify({ status: "ATTESTED", path: receipt.path, receiptDigest: receipt.receiptDigest, bundleKind: bundle.kind, limitation: receipt.attestation.limitation }, null, 2)}\n`);
      return;
    }
    case "verify": {
      if (!receiptId) throw new Error("Usage: fl attest verify <receipt-id> [--expect-digest <sha256:...>] [--store <directory>]");
      const verification = verifyStoredBundleAttestation(store, receiptId, option(args, "--expect-digest"));
      process.stdout.write(`${JSON.stringify(verification, null, 2)}\n`);
      process.exitCode = verification.valid ? 0 : 1;
      return;
    }
    default:
      throw new Error("Usage: fl attest create|verify ...");
  }
}

/**
 * GitHub artifact provenance deliberately has a separate command from `attest`.
 * The older command is an integrity checksum only; this command creates the
 * exact subject file that GitHub Actions signs with actions/attest.
 */
async function provenanceCommand(args: string[]): Promise<void> {
  const [action] = args;
  switch (action) {
    case "create": {
      const bundleDirectory = resolve(requiredOption(args, "--bundle"));
      const provenanceRoot = defaultGithubProvenanceRoot();
      const output = resolve(option(args, "--output") ?? join(provenanceRoot, "ci-receipt.json"));
      const ci = githubActionsIdentityFromEnvironment(process.cwd());
      const receipt = createGithubProvenanceReceipt(bundleDirectory, ci);
      const path = writeGithubProvenanceReceipt(output, receipt, provenanceRoot);
      process.stdout.write(`${JSON.stringify({
        status: "AWAITING_GITHUB_ARTIFACT_ATTESTATION",
        path,
        receiptDigest: receipt.receiptDigest,
        proofRootDigest: receipt.proof.rootDigest,
        signing: "Run actions/attest@v4 with this exact path as subject-path before calling it signed provenance.",
        limitation: receipt.limitation
      }, null, 2)}\n`);
      return;
    }
    case "verify": {
      const trustFile = resolve(requiredOption(args, "--trust"));
      const trust = readGithubArtifactAttestationTrust(trustFile);
      // A relative root in the policy is relative to the policy, not the
      // caller's current directory or an attacker-supplied attestation bundle.
      const resolvedTrust = { ...trust, trustedRootFile: resolve(dirname(trustFile), trust.trustedRootFile) };
      const verification = verifySignedGithubProvenance({
        bundleDirectory: resolve(requiredOption(args, "--bundle")),
        receiptFile: resolve(requiredOption(args, "--receipt")),
        attestationBundleFile: resolve(requiredOption(args, "--attestation-bundle")),
        trust: resolvedTrust
      });
      process.stdout.write(`${JSON.stringify(verification, null, 2)}\n`);
      process.exitCode = verification.valid ? 0 : 1;
      return;
    }
    default:
      throw new Error("Usage: fl provenance create|verify ...");
  }
}

/**
 * A repair brief is deliberately downstream of a verified, proof-grade Git
 * package. It can only express cited, INFERRED guidance; it cannot turn an
 * arbitrary JSON file or model response into an execution fact.
 */
async function repairCommand(args: string[]): Promise<void> {
  if (args[0] === "verify") {
    const directory = args[1];
    if (!directory) throw new Error("Usage: fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]");
    const verification = verifyRepairBriefArtifact(resolve(directory), option(args, "--expect-digest"));
    process.stdout.write(`${JSON.stringify(verification, null, 2)}\n`);
    process.exitCode = verification.valid ? 0 : 1;
    return;
  }
  if (args[0] !== "brief") {
    throw new Error("Usage: fl repair brief|verify ...");
  }
  const live = hasFlag(args, "--live");
  const input = option(args, "--input");
  if (live === Boolean(input)) {
    throw new Error("Choose exactly one repair brief source: --live or --input <repair-brief.json>.");
  }

  const bundle = option(args, "--bundle");
  const investigationFile = option(args, "--investigation");
  if (Boolean(bundle) === Boolean(investigationFile)) {
    throw new Error("Choose exactly one proof source: --bundle <directory> or --investigation <verified-bundle>/investigation.json.");
  }
  const bundleDirectory = bundle === undefined
    ? dirname(resolve(investigationFile as string))
    : resolve(bundle);
  const proof = loadVerifiedGitProofView(bundleDirectory, option(args, "--expect-root"));
  if (investigationFile !== undefined) {
    const expectedInvestigationPath = resolve(bundleDirectory, proof.manifest.artifacts.investigation);
    if (resolve(investigationFile) !== expectedInvestigationPath) {
      throw new Error("--investigation must identify the investigation.json artifact in the verified Git proof bundle.");
    }
  }
  const investigation = proof.investigation;
  const generatedPacket = createRepairEvidencePacket(investigation);
  // This is normally a no-op because createRepairEvidencePacket only emits an
  // allowlisted summary. Keep it at the CLI boundary so a future packet field
  // cannot silently expand what is persisted or sent to a model.
  const packetRedaction = redactValue(generatedPacket);
  const packet = RepairEvidencePacketSchema.parse(packetRedaction.value);
  if (packet.packetDigest !== generatedPacket.packetDigest) {
    throw new Error("Repair evidence packet changed during redaction and cannot be safely correlated to the verified investigation.");
  }

  const model = option(args, "--model") ?? "gpt-5.6";
  const candidate = live
    ? await proposeRepairBriefWithGpt(packet, { model })
    : readJsonInput(input as string);
  // Never retain unredacted model or offline input. Redaction is intentionally
  // limited, so the generated packet remains minimal as the primary boundary.
  const candidateRedaction = redactValue(candidate);
  const validation = validateRepairBrief(packet, candidateRedaction.value);
  if (!validation.valid || !validation.brief) {
    throw new Error(`Repair brief was rejected: ${validation.errors.join("; ")}`);
  }

  const output = option(args, "--output")
    ?? join(defaultRepairBriefRoot(), `repair-${packet.packetDigest.slice("sha256:".length, "sha256:".length + 16)}-${Date.now()}`);
  const written = writeRepairBriefArtifact(
    output,
    packet,
    validation.brief,
    live ? { kind: "GPT-5.6", model } : { kind: "OFFLINE_INPUT" }
  );
  process.stdout.write(`${JSON.stringify({
    status: "PERSISTED",
    classification: "INFERRED",
    source: written.manifest.source,
    directory: written.directory,
    artifactDigest: written.manifest.manifestDigest,
    evidencePacketDigest: packet.packetDigest,
    repairBriefDigest: written.manifest.repairBrief.digest,
    privacy: {
      packet: packetRedaction.report,
      candidate: candidateRedaction.report
    },
    limitation: "INFERRED repair guidance is not an executed verdict, proof of model intent, a unique semantic cause, or an identified culprit."
  }, null, 2)}\n`);
}

async function main(): Promise<void> {
  const [command, ...args] = process.argv.slice(2);
  switch (command) {
    case undefined:
    case "help":
    case "--help":
    case "-h":
      process.stdout.write(`${usage}\n`);
      return;
    case "--version":
    case "-V":
      process.stdout.write(`FaultLine ${faultLineVersion()}\n`);
      return;
    case "judge-demo":
      await judgeDemo(args);
      return;
    case "judge-preview":
      judgePreviewCommand(args);
      return;
    case "doctor":
      await doctorCommand(args);
      return;
    case "incident":
      await incidentCommand(args);
      return;
    case "runtime":
      await runtimeCommand(args);
      return;
    case "demo":
      await demoCommand(args);
      return;
    case "verify": {
      const directory = args[0];
      if (!directory) throw new Error("Usage: fl verify <proof-bundle-directory>");
      const root = resolve(directory);
      let schemaVersion: string | undefined;
      try {
        const manifest = JSON.parse(readFileSync(join(root, "manifest.json"), "utf8")) as { schemaVersion?: unknown };
        schemaVersion = typeof manifest.schemaVersion === "string" ? manifest.schemaVersion : undefined;
      } catch {
        // Let the selected verifier return a detailed safe failure below.
      }
      const result = schemaVersion === "faultline.git-proof-bundle.v1"
        ? verifyGitInvestigationProofBundle(root, option(args, "--expect-root"))
        : verifyProofBundle(root, option(args, "--expect-root"));
      const label = schemaVersion === "faultline.git-proof-bundle.v1" ? "Git proof" : "Bundle";
      process.stdout.write(`${result.externalRootStatus === "NOT_PROVIDED" ? `${label} self-consistency` : "Integrity"}: ${result.valid ? "VALID" : "INVALID"}\n`);
      process.stdout.write(`Declared files checked: ${result.checkedFiles}\nBundle root: ${result.rootDigest ?? "unavailable"}\nExternal root: ${result.externalRootStatus}\n`);
      if (!result.valid) process.stdout.write(`${result.errors.map((error) => `- ${error}`).join("\n")}\n`);
      process.exitCode = result.valid ? 0 : 1;
      return;
    }
    case "serve": {
      if (hasFlag(args, "--bundle")) {
        const bundleDirectory = resolve(requiredOption(args, "--bundle"));
        const minimization = hasFlag(args, "--minimization") ? requiredOption(args, "--minimization") : undefined;
        const repair = hasFlag(args, "--repair") ? requiredOption(args, "--repair") : undefined;
        const expectedMinimization = hasFlag(args, "--expect-minimization") ? requiredOption(args, "--expect-minimization") : undefined;
        const expectedRepair = hasFlag(args, "--expect-repair") ? requiredOption(args, "--expect-repair") : undefined;
        if ((minimization === undefined) !== (expectedMinimization === undefined)) {
          throw new Error("A shareable minimization attachment requires both --minimization <result.json> and --expect-minimization <retained-digest>.");
        }
        if ((repair === undefined) !== (expectedRepair === undefined)) {
          throw new Error("A shareable repair attachment requires both --repair <repair-brief-directory> and --expect-repair <retained-artifact-digest>.");
        }
        const proof = loadVerifiedGitProofView(bundleDirectory, option(args, "--expect-root"), {
          ...(minimization === undefined ? {} : { minimizationFile: resolve(minimization) }),
          ...(expectedMinimization === undefined ? {} : { expectedMinimizationDigest: expectedMinimization }),
          ...(repair === undefined ? {} : { repairDirectory: resolve(repair) }),
          ...(expectedRepair === undefined ? {} : { expectedRepairDigest: expectedRepair })
        });
        const server = await startGitProofServer({ proof, port: Number(option(args, "--port") ?? "4173") });
        process.stdout.write(`FaultLine read-only Git proof page: ${server.url}\nPress Ctrl+C to stop.\n`);
        await new Promise<void>((resolveExit) => {
          process.once("SIGINT", () => {
            void server.close().finally(resolveExit);
          });
        });
        return;
      }
      const outputDirectory = resolve(option(args, "--output") ?? ".faultline/bundles/judge-demo");
      const analysis = createDemoAnalysis("REPLAY");
      const server = await startFaultLineServer({ analysis, outputDirectory, port: Number(option(args, "--port") ?? "4173") });
      process.stdout.write(`FaultLine incident page: ${server.url}\nPress Ctrl+C to stop.\n`);
      await new Promise<void>((resolveExit) => process.once("SIGINT", () => { void server.close().finally(resolveExit); }));
      return;
    }
    case "codex":
      if (args[0] === "record") {
        await recordCommand(args.slice(1));
        return;
      }
      if (hasFlag(args, "--dry-run")) {
        process.stdout.write(`${JSON.stringify({ adapter: "git-sidecar", status: "DRY_RUN", records: ["clean Git HEAD tree", "content-addressed snapshot manifest"], limitation: "No live Codex transport or turn lifecycle capture is claimed by this build." }, null, 2)}\n`);
        return;
      }
      if (hasFlag(args, "--snapshot")) {
        const repository = resolve(option(args, "--repo") ?? process.cwd());
        const snapshot = captureCleanGitSnapshot(repository);
        const file = writeGitSidecarSnapshot(repository, snapshot);
        process.stdout.write(`${JSON.stringify({ snapshot, file, limitation: "This is a clean Git sidecar snapshot, not a live Codex transport event." }, null, 2)}\n`);
        return;
      }
      throw new Error("The current build exposes an honest Git sidecar. Run: fl codex --dry-run or fl codex --snapshot --repo <directory>");
    case "witness":
      await witnessCommand(args);
      return;
    case "record":
      await recordCommand(args);
      return;
    case "investigate":
      await investigateCommand(args);
      return;
    case "minimize":
      await minimizeCommand(args);
      return;
    case "ledger":
      await ledgerCommand(args);
      return;
    case "attest":
      await attestationCommand(args);
      return;
    case "provenance":
      await provenanceCommand(args);
      return;
    case "repair":
      await repairCommand(args);
      return;
    default:
      throw new Error(`Unknown command: ${command}\n\n${usage}`);
  }
}

main().catch((error: unknown) => {
  process.stderr.write(`FaultLine error: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
});
