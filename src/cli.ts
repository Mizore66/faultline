#!/usr/bin/env node
import { exec, spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, extname, isAbsolute, join, relative, resolve } from "node:path";
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
import { doctorCliExitCode, runFaultLineDoctor, type FaultLineDoctorReport } from "./doctor.js";
import { guidedInvestigateFromCiLog } from "./guided-investigate.js";
import { assertIncidentFrozenWitnessBinding, runFrozenIncidentContinuation } from "./incident-continue.js";
import { createIncidentDraft, type IncidentDraft } from "./incident.js";
import { suggestIncidentRanges } from "./incident-intake.js";
import { defaultIncidentDraftStore, readIncidentDraft, writeIncidentDraft } from "./incident-store.js";
import { defaultJudgePreviewPath, writeJudgePreview } from "./judge-preview.js";
import {
  RECORDED_SELF_INCIDENT_ROOT,
  defaultCommitProofPreviewPath,
  defaultSelfIncidentSampleDirectory,
  loadSelfIncidentProofView,
  writeCommitProofPreview
} from "./judge-proof.js";
import { captureCleanGitSnapshot, writeGitSidecarSnapshot } from "./git-snapshot.js";
import {
  CodexSidecarError,
  codexSidecarLedgerPath,
  inspectObservedCodexSidecar,
  recordObservedCodexHook,
  resolveLatestSidecarLedgerPath
} from "./codex-sidecar.js";
import { planProjectInit, type ProjectInitRuntime } from "./project-init.js";
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
  verifyCodexLifecycleLedger,
  verifyCodexLifecycleLedgerFile,
  writeCodexLifecycleLedgerAtomic
} from "./ledger.js";
import { readModelOverlayInput } from "./overlay-input.js";
import { describeBundlePath, verifyProofBundle, writeProofBundle } from "./proof-bundle.js";
import { redactValue } from "./redaction.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";
import {
  PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
  ProjectRuntimeImagePreparationError,
  describeProjectRuntimeImageBuild,
  prepareCuratedRuntime,
  prepareProjectRuntimeImage,
  resolveCuratedRuntime,
  resolveProjectRuntimeImage,
  selectCuratedRuntime
} from "./runtime.js";
import {
  createRepairEvidencePacket,
  proposeRepairBriefWithGpt,
  RepairEvidencePacketSchema,
  validateRepairBrief
} from "./repair-brief.js";
import { defaultRepairBriefRoot, verifyRepairBriefArtifact, writeRepairBriefArtifact } from "./repair-brief-store.js";
import {
  defaultPreventionProofRoot,
  PREVENTION_PROOF_SCHEMA_VERSION,
  verifyPreventionProof,
  writePreventionProof,
  type PreventionProofWriteInput
} from "./prevention-proof.js";
import { startFaultLineServer, startGitProofServer } from "./server.js";
import { startWitnessReviewServer } from "./witness-review-server.js";
import { openWitnessReview } from "./witness-review.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  readFrozenWitness,
  verifyFrozenWitness
} from "./witness-lock.js";
import {
  signAuthenticatedWitnessApproval,
  verifyAuthenticatedWitnessApproval
} from "./authenticated-witness-approval.js";
import type { RunMode } from "./domain.js";
import { ZodError } from "zod";

const usage = `FaultLine — First Bad Turn evidence for agent-assisted code

Usage:
  fl judge-demo [--replay | --rerun-all] [--output <managed-bundle-directory>] [--export-only]
  fl judge-proof [--bundle <git-proof-bundle-directory>] [--expect-root <sha256:...>] [--export-only] [--port <number>]
  fl commit-proof-preview [--bundle <git-proof-bundle-directory>] [--expect-root <sha256:...>] [--output <static-preview.html>]
  fl --version
  fl judge-preview [--output <static-preview.html>]
  fl doctor [--repo <directory>] [--json] [--proof-ready]
  fl init [--repo <directory>] [--cli <built-cli.js>] [--runtime <node|python|go>] [--yes]
  fl incident suggest --repo <directory>
  fl incident start --repo <directory> (--command <failing-command> | --command-file <utf8-file>) [--id <safe-id>] [--from <commit> --to <commit>] [--runtime <node|python|go> | --image <digest-pinned-image>] [--store <directory>]
  fl incident status <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--expect-digest <sha256:...>]
  fl incident continue <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--image <digest-pinned-image>] [--expect-digest <sha256:...>] [--ledger <ledger.json>] [--max-states <count>] [--output <managed-bundle-directory>] [--unsafe-local]
  fl investigate --ci-log <file> --repo <directory> [--command <failing-command>] [--from <commit> --to <commit>] [--runtime <node|python|go> | --image <digest-pinned-image>] [--id <safe-id>] [--unsafe-local]
  fl investigate --resume <incident-id> --repo <directory> [--expect-digest <sha256:...>] [--runtime <node|python|go> | --image <digest-pinned-image>] [--unsafe-local]
  fl investigate turns --repo <directory> (--ledger <ledger.json> | --latest) --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--runtime-mapping <mapping.json>] [--output <managed-bundle-directory>] [--minimize] [--transition <index>] [--max-executions <count>]
  fl prove transition <turn-proof-bundle-directory> --repo <directory> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--transition <index>] [--max-executions <count>] [--output <managed-result.json>]
  fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--ledger <ledger.json>] [--output <managed-bundle-directory>]
  fl runtime resolve <node|python|go>
  fl runtime prepare <node|python|go> --yes
  fl runtime project <plan|build|resolve> [--context <directory>] [--dockerfile <file>] --tag <repository:tag> [--network <none|default>] [--yes]
  fl demo live-git [--image <digest-pinned-image>] [--export-only] [--port <number>]
  fl verify <proof-bundle-directory> [--expect-root <sha256:...>]
  fl serve [--port <number>]
  fl serve --bundle <git-proof-bundle-directory> [--expect-root <sha256:...>] [--minimization <result.json> --expect-minimization <sha256:...>] [--repair <repair-brief-directory> --expect-repair <sha256:...>] [--prevention <prevention-proof-directory> --expect-prevention <sha256:...>] [--port <number>]
  fl codex --dry-run | --snapshot [--repo <directory>]
  fl codex record <init|stdin|checkpoint|verify> [...]
  fl codex sidecar config (--cli <built-cli.js> | --command <hook-command> [--command-windows <hook-command>])
  fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes
  fl codex sidecar hook [--input <hook.json>] [--quiet]
  fl codex sidecar status [--repo <directory>] [--session <session-id>]
  fl record <init|stdin|checkpoint|attach|verify> [...]
  fl minimize git --repo <directory> --before <commit> --after <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--max-executions <count>] [--output <managed-result.json>]
  fl minimize verify <result.json> [--expect-digest <sha256:...>]
  fl ledger bind --ledger <ledger.json> --investigation <investigation.json> --output <binding.json>
  fl ledger verify <binding.json> [--expect-digest <sha256:...>]
  fl attest create --bundle <proof-bundle-directory> --receipt <id> --subject <label> --issuer <label> [--store <directory>]
  fl attest verify <receipt-id> [--expect-digest <sha256:...>] [--store <directory>]
  fl provenance create --bundle <git-proof-bundle-directory> [--output <managed-receipt.json>]
  fl provenance verify --bundle <git-proof-bundle-directory> --receipt <ci-receipt.json> --attestation-bundle <sigstore-bundle.json> --trust <trust.json>
  fl repair --bundle <git-proof-bundle-directory> --expect-root <sha256:...> [--repo <directory>] [--output <directory>] [--with-codex] [--instructions-only] [--keep-worktree]
  fl repair brief (--bundle <git-proof-bundle-directory> | --investigation <verified-bundle>/investigation.json) (--live | --input <repair-brief.json>) [--expect-root <sha256:...>] [--model <model>] [--output <managed-directory>]
  fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]
  fl prevention write --input <prevention-input.json> [--output <managed-directory>]
  fl prevention verify <prevention-proof-directory> [--expect-root <sha256:...>]
  fl witness propose --input <proposal.json> [--store <directory>]
  fl witness propose --live --incident <incident.json> --proposal-id <id> --overlay-root <directory> [--model <model>] [--store <directory>]
  fl witness implement --incident <incident.json> --proposal-id <id> --overlay-out <directory> [--repo <directory>] [--with-codex] [--behavior <text>]
  fl witness review <proposal-id> [--json | --port <number>] [--store <directory>] [--draft-store <directory>]
  fl witness approve <proposal-id> --approved-by <actor> [--store <directory>]
  fl witness freeze <proposal-id> [--store <directory>]
  fl witness sign <proposal-id> --private-key <ed25519-private.pem> --keyring <trusted-reviewers.json> [--store <directory>]
  fl witness verify <proposal-id> [--expect-digest <sha256:...>] [--keyring <trusted-reviewers.json> --require-signature] [--store <directory>]

The judge demo is a reviewed, deterministic Node fixture. It does not require an OpenAI API key.
The lifecycle adapter accepts observed Codex-compatible events; it does not claim to intercept private Codex internals.
Evidence outputs are intentionally confined to their managed .faultline roots; --output selects a child of that root rather than an arbitrary directory.
Use --live for a GPT-5.6 witness proposal or an inferred repair brief after setting OPENAI_API_KEY.
fl doctor exits 0 when Node can run the local CLI; fl doctor --proof-ready exits nonzero unless Docker proof-grade preflight is READY.`;

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

/**
 * Read a reviewable command from a regular UTF-8 text file without asking the
 * caller's shell to re-quote nested command syntax.  The resulting command
 * bytes—not the source file path—become the immutable witness input.
 */
function incidentCommandInput(args: string[]): { command: string; source: "INLINE" | "FILE"; file?: string } {
  const inline = option(args, "--command");
  const commandFile = option(args, "--command-file");
  if ((inline === undefined) === (commandFile === undefined)) {
    throw new Error("Incident intake requires exactly one of --command <failing-command> or --command-file <utf8-file>.");
  }
  if (inline !== undefined) {
    if (!inline || inline.startsWith("--")) throw new Error("Missing required option: --command");
    return { command: inline, source: "INLINE" };
  }
  if (!commandFile || commandFile.startsWith("--")) throw new Error("Missing required option: --command-file");
  const file = resolve(commandFile);
  let bytes: Buffer;
  try {
    const metadata = lstatSync(file);
    if (metadata.isSymbolicLink() || !metadata.isFile()) {
      throw new Error("must be a real regular file, not a symbolic link or directory");
    }
    bytes = readFileSync(file);
  } catch (error) {
    throw new Error(`Unable to read --command-file ${file}: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (bytes.byteLength > 32_000) throw new Error("--command-file exceeds the 32 KB witness-command limit.");
  const command = bytes.toString("utf8");
  if (!Buffer.from(command, "utf8").equals(bytes)) {
    throw new Error("--command-file must contain valid UTF-8 without an ambiguous byte encoding.");
  }
  return { command, source: "FILE", file };
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
  const proofGrade = report.dockerInvestigationPreflight === "READY"
    ? "READY (Docker proof-grade path available)"
    : `${report.dockerInvestigationPreflight} (proof-grade Git investigation unavailable; fl judge-demo and offline verify still work)`;
  return [
    "FaultLine doctor",
    `Repository: ${report.repositoryRoot ?? report.repository}`,
    `Local CLI: ${doctorCliExitCode(report) === 0 ? "READY" : "ACTION_REQUIRED"}`,
    `Proof-grade preflight: ${proofGrade}`,
    `Image selection: ${report.imageSelection} (run fl runtime prepare <runtime> --yes for a reviewed base, fl runtime project plan for an explicit dependency image, or fl runtime resolve after you choose one yourself)`,
    `Likely runtime: ${runtime}`,
    "",
    ...diagnostics,
    "",
    "Limits:",
    ...report.limitations.map((limitation) => `- ${limitation}`),
    "",
    "Exit status: 0 when Node can run the local CLI (including the no-Docker judge path). Non-zero only when Node itself is missing or unsupported. Docker gaps are reported above and do not fail this command."
  ].join("\n");
}

async function doctorCommand(args: string[]): Promise<void> {
  const repository = resolve(option(args, "--repo") ?? process.cwd());
  const report = await runFaultLineDoctor({ repository });
  const proofReadyOnly = hasFlag(args, "--proof-ready");
  if (hasFlag(args, "--json")) {
    process.stdout.write(`${JSON.stringify({
      ...report,
      cliExitCode: doctorCliExitCode(report),
      proofReadyExitCode: report.dockerInvestigationPreflight === "READY" ? 0 : 1
    }, null, 2)}\n`);
  } else {
    process.stdout.write(`${doctorSummary(report)}\n`);
    if (proofReadyOnly) {
      process.stdout.write(`\n--proof-ready: ${report.dockerInvestigationPreflight === "READY" ? "READY" : "NOT READY"}\n`);
    }
  }
  process.exitCode = proofReadyOnly
    ? (report.dockerInvestigationPreflight === "READY" ? 0 : 1)
    : doctorCliExitCode(report);
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
  const image = option(args, "--image");
  const ledgerFile = option(args, "--ledger");
  const maxStates = option(args, "--max-states");
  const outputDirectory = option(args, "--output");
  const result = await runFrozenIncidentContinuation({
    draft: context.draft,
    repository: context.repository,
    witnessStore: context.witnessStore,
    ...(expectedFrozenDigest === undefined ? {} : { expectedFrozenDigest }),
    ...(image === undefined ? {} : { image }),
    ...(hasFlag(args, "--unsafe-local") ? { unsafeLocal: true } : {}),
    ...(ledgerFile === undefined ? {} : { ledgerFile }),
    ...(maxStates === undefined ? {} : { maxStates: Number(maxStates) }),
    ...(outputDirectory === undefined ? {} : { outputDirectory })
  });
  if (result.status === "INVESTIGATION_NOT_PROOF") {
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
    process.exitCode = 1;
    return;
  }
  process.stdout.write(`${JSON.stringify({
    status: result.status,
    incident: result.incident,
    proofBundle: result.proofBundle,
    next: result.next,
    limitations: result.limitations
  }, null, 2)}\n`);
}

async function incidentCommand(args: string[]): Promise<void> {
  if (args[0] === "suggest") {
    const suggestions = await suggestIncidentRanges({ repository: requiredOption(args, "--repo") });
    process.stdout.write(`${JSON.stringify({
      status: suggestions.candidates.length === 0 ? "NO_REVIEWABLE_RANGE_SUGGESTIONS" : "RANGE_SUGGESTIONS_READY",
      ...suggestions,
      next: suggestions.candidates.length === 0
        ? ["Supply an explicit reviewed --from <commit> and --to <commit> when you start the incident. FaultLine intentionally did not guess a range."]
        : ["Review the local candidates. When one applies, pass its exact from/to commits to fl incident start; FaultLine will still require human witness approval and freeze before execution."]
    }, null, 2)}\n`);
    return;
  }
  if (args[0] === "status") return incidentStatusCommand(args);
  if (args[0] === "continue") return continueIncidentCommand(args);
  if (args[0] !== "start") {
    throw new Error("Usage: fl incident suggest|start|status|continue ...");
  }
  const repository = resolve(requiredOption(args, "--repo"));
  const commandInput = incidentCommandInput(args);
  const command = commandInput.command;
  const from = option(args, "--from");
  const to = option(args, "--to");
  if ((from === undefined) !== (to === undefined)) {
    throw new Error("Incident intake accepts --from and --to together, or neither for the conservative local HEAD-parent fallback.");
  }
  const draftId = option(args, "--id") ?? generatedIncidentId();
  const requestedRuntime = option(args, "--runtime");
  const requestedImage = option(args, "--image");
  if (requestedRuntime !== undefined && requestedImage !== undefined) {
    throw new Error("Incident intake accepts either --runtime <node|python|go> or --image <digest-pinned-image>, not both.");
  }
  const timeoutSeconds = Number(option(args, "--timeout-seconds") ?? "300");
  if (!Number.isInteger(timeoutSeconds) || timeoutSeconds < 1 || timeoutSeconds > 3_600) {
    throw new Error("--timeout-seconds must be an integer from 1 through 3600.");
  }
  const runtime = requestedRuntime === undefined
    ? requestedImage === undefined ? undefined : { requested: "explicit", image: requestedImage }
    : await resolveCuratedRuntime(requestedRuntime);
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
      commandInput: commandInput.source === "INLINE"
        ? { kind: "INLINE" }
        : { kind: "FILE", path: commandInput.file },
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

function projectRuntimeBuildInput(args: string[]): {
  readonly contextDirectory: string;
  readonly dockerfile: string;
  readonly imageTag: string;
  readonly network: "none" | "default";
} {
  const requestedNetwork = option(args, "--network") ?? "none";
  if (requestedNetwork !== "none" && requestedNetwork !== "default") {
    throw new Error("--network must be either none or default for a project runtime image build.");
  }
  return {
    contextDirectory: resolve(option(args, "--context") ?? process.cwd()),
    dockerfile: option(args, "--dockerfile") ?? "Dockerfile",
    imageTag: requiredOption(args, "--tag"),
    network: requestedNetwork
  };
}

async function projectRuntimeCommand(args: string[]): Promise<void> {
  const action = args[0];
  if (action === "resolve") {
    const imageTag = requiredOption(args, "--tag");
    try {
      const resolved = await resolveProjectRuntimeImage(imageTag);
      process.stdout.write(`${JSON.stringify({
        status: "PROJECT_IMAGE_RESOLVED_LOCAL_DIGEST",
        setupOnly: true,
        requestedTag: resolved.requestedTag,
        image: resolved.image,
        next: "Use this digest-pinned image with fl incident start --image <digest-pinned-image> so human review can bind it before proof replay."
      }, null, 2)}\n`);
    } catch (error) {
      if (error instanceof ProjectRuntimeImagePreparationError && error.code === "UNRESOLVED_REPO_DIGEST") {
        process.stdout.write(`${JSON.stringify({
          status: "PROJECT_IMAGE_DIGEST_UNAVAILABLE",
          requestedTag: imageTag,
          limitation: error.message,
          next: "FaultLine will not push an image or use registry credentials. Push and pull this reviewed tag with your own registry workflow, then rerun this same resolve command."
        }, null, 2)}\n`);
        process.exitCode = 1;
        return;
      }
      throw error;
    }
    return;
  }
  if (action !== "plan" && action !== "build") {
    throw new Error("Usage: fl runtime project plan [--context <directory>] [--dockerfile <file>] --tag <repository:tag> [--network <none|default>] | fl runtime project build [same options] --expect-plan <sha256:digest> --yes | fl runtime project resolve --tag <repository:tag>");
  }
  const input = projectRuntimeBuildInput(args);
  const plan = describeProjectRuntimeImageBuild(input);
  if (action === "plan") {
    process.stdout.write(`${JSON.stringify({
      status: "PROJECT_IMAGE_BUILD_REVIEW_REQUIRED",
      plan,
      next: "Review the Dockerfile, context boundary, plan.review.planDigest, network policy, and Docker mutation shown above. If they are acceptable, rerun the same request as fl runtime project build ... --expect-plan <plan.review.planDigest> --yes."
    }, null, 2)}\n`);
    return;
  }
  const expectedPlanDigest = option(args, "--expect-plan");
  if (!hasFlag(args, "--yes") || expectedPlanDigest === undefined) {
    process.stdout.write(`${JSON.stringify({
      status: "CONFIRMATION_REQUIRED",
      plan,
      effect: "FaultLine will execute the reviewed Dockerfile as setup only. This may change the local Docker image store and, with --network default, allow Dockerfile build steps to use the network. It does not run a witness or create proof.",
      requiredPlanDigest: plan.review.planDigest,
      next: "Review the complete plan, then rerun the same command with --expect-plan <requiredPlanDigest> --yes."
    }, null, 2)}\n`);
    process.exitCode = 1;
    return;
  }
  try {
    const prepared = await prepareProjectRuntimeImage({
      ...input,
      confirmation: PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
      expectedPlanDigest
    });
    process.stdout.write(`${JSON.stringify({
      status: "PROJECT_IMAGE_PREPARED_LOCAL_DIGEST",
      prepared,
      next: `Start a review-only incident with --image ${prepared.image}; FaultLine will bind that digest to the human-reviewed draft before any proof-grade replay.`
    }, null, 2)}\n`);
  } catch (error) {
    if (error instanceof ProjectRuntimeImagePreparationError && error.code === "UNRESOLVED_REPO_DIGEST") {
      process.stdout.write(`${JSON.stringify({
          status: "PROJECT_IMAGE_BUILT_NEEDS_REGISTRY_DIGEST",
          plan,
          limitation: error.message,
          proofNext: "FaultLine did not push an image or use registry credentials. For a portable proof, push and pull the reviewed tag with your own registry workflow, then run fl runtime project resolve --tag <repository:tag>.",
          localDiagnosticNext: "For local wiring only after a human witness freeze, run fl incident continue <id> --unsafe-local. That route does not use this local image ID, is explicitly non-proof, and never exports a portable proof bundle."
        }, null, 2)}\n`);
      process.exitCode = 1;
      return;
    }
    throw error;
  }
}

async function runtimeCommand(args: string[]): Promise<void> {
  const action = args[0];
  if (action === "project") {
    await projectRuntimeCommand(args.slice(1));
    return;
  }
  const requested = args[1];
  if ((action !== "resolve" && action !== "prepare") || !requested) {
    throw new Error("Usage: fl runtime resolve <node|python|go> | fl runtime prepare <node|python|go> --yes | fl runtime project plan|build|resolve ...");
  }
  if (action === "resolve") {
    if (hasFlag(args, "--pull")) {
      throw new Error("FaultLine never pulls a runtime image implicitly. Use fl runtime prepare <node|python|go> --yes to explicitly pull one reviewed catalog image, or pull it yourself and then run fl runtime resolve again.");
    }
    const resolution = await resolveCuratedRuntime(requested);
    process.stdout.write(`${JSON.stringify({
      status: "RESOLVED_LOCAL_DIGEST",
      runtime: resolution.runtime,
      requested: resolution.requested,
      image: resolution.image,
      next: `Run fl incident start ... --runtime ${resolution.runtime.alias} to persist this resolved image, or after a human freeze pass the image value and retained --expect-digest to fl incident continue <id> --image.`
    }, null, 2)}\n`);
    return;
  }

  const runtime = selectCuratedRuntime(requested);
  if (!hasFlag(args, "--yes")) {
    process.stdout.write(`${JSON.stringify({
      status: "CONFIRMATION_REQUIRED",
      runtime,
      effect: `FaultLine will run docker pull ${runtime.tag}, which may access the network and changes the local Docker image store. It will then inspect and print Docker's immutable RepoDigest. This setup action is not a proof execution.`,
      next: `fl runtime prepare ${runtime.alias} --yes`
    }, null, 2)}\n`);
    process.exitCode = 1;
    return;
  }
  if (hasFlag(args, "--pull")) {
    throw new Error("fl runtime prepare already performs one explicit catalog pull after --yes; do not add --pull.");
  }
  const prepared = await prepareCuratedRuntime(requested);
  process.stdout.write(`${JSON.stringify({
    status: "PREPARED_LOCAL_DIGEST",
    runtime: prepared.runtime,
    requested: prepared.requested,
    pulledTag: prepared.pull.tag,
    image: prepared.image,
    next: `Run fl incident start ... --runtime ${prepared.runtime.alias} to bind this immutable image digest into the human-reviewed incident draft. The pull itself is setup only, not proof.`
  }, null, 2)}\n`);
}

function shouldAutoOpenBrowser(): boolean {
  if (process.env.FAULTLINE_NO_BROWSER === "1") return false;
  if (process.env.CI === "true" || process.env.CI === "1") return false;
  if (process.env.GITHUB_ACTIONS === "true") return false;
  return true;
}

function openLocalDemoUrl(url: string): void {
  if (!shouldAutoOpenBrowser()) {
    process.stdout.write(`Browser auto-open skipped in CI. Open ${url} manually if needed.\n`);
    return;
  }
  const command = process.platform === "win32"
    ? `cmd /c start "" "${url}"`
    : process.platform === "darwin"
      ? `open "${url}"`
      : `xdg-open "${url}"`;
  exec(command, (error) => {
    if (error) {
      process.stderr.write(`FaultLine could not open a browser automatically. Open ${url} manually.\n`);
    }
  });
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
  const port = Number(option(args, "--port") ?? "4173");
  const server = await startFaultLineServer({ analysis, outputDirectory: bundle.directory, port });
  process.stdout.write(`🚀 Launching FaultLine Judge Demo at ${server.url}...\n`);
  process.stdout.write(`Press Ctrl+C to stop.\n`);
  openLocalDemoUrl(server.url);
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

async function judgeProofCommand(args: string[]): Promise<void> {
  const bundleDirectory = option(args, "--bundle") === undefined
    ? defaultSelfIncidentSampleDirectory()
    : resolve(requiredOption(args, "--bundle"));
  const expectRoot = option(args, "--expect-root");
  const proof = loadSelfIncidentProofView({
    directory: bundleDirectory,
    ...(expectRoot === undefined ? {} : { expectedRoot: expectRoot })
  });
  process.stdout.write(`FaultLine COMMIT_PROOF sample verified.\n`);
  process.stdout.write(`Bundle: ${describeBundlePath(bundleDirectory)}\n`);
  process.stdout.write(`Root: ${proof.rootDigest}\n`);
  process.stdout.write(`External root: ${proof.externalRootStatus}\n`);
  process.stdout.write("This is the product Idea path (portable predicate proof), not the judge-demo fixture.\n");
  if (proof.rootDigest === RECORDED_SELF_INCIDENT_ROOT) {
    process.stdout.write("Matched historical self-incident root.\n");
  } else {
    process.stdout.write(`Note: historical self-incident root is ${RECORDED_SELF_INCIDENT_ROOT} (see docs/faultline-self-incident.md).\n`);
  }
  if (hasFlag(args, "--export-only")) return;
  const port = Number(option(args, "--port") ?? "4174");
  const server = await startGitProofServer({ proof, port });
  process.stdout.write(`FaultLine COMMIT_PROOF page: ${server.url}\nPress Ctrl+C to stop.\n`);
  openLocalDemoUrl(server.url);
  await new Promise<void>((resolveExit) => {
    process.once("SIGINT", () => {
      void server.close().finally(resolveExit);
    });
  });
}

function commitProofPreviewCommand(args: string[]): void {
  const bundleDirectory = option(args, "--bundle") === undefined
    ? defaultSelfIncidentSampleDirectory()
    : resolve(requiredOption(args, "--bundle"));
  const expectRoot = option(args, "--expect-root");
  const output = option(args, "--output") === undefined
    ? defaultCommitProofPreviewPath()
    : resolve(requiredOption(args, "--output"));
  const preview = writeCommitProofPreview({
    directory: bundleDirectory,
    ...(expectRoot === undefined ? {} : { expectedRoot: expectRoot }),
    outputFile: output
  });
  process.stdout.write(`FaultLine static COMMIT_PROOF preview written.\n`);
  process.stdout.write(`Preview: ${preview.path}\n`);
  process.stdout.write(`Bytes: ${preview.bytes}\n`);
  process.stdout.write(`Root: ${preview.rootDigest}\n`);
  process.stdout.write("Limitation: static snapshot of a verified package — not a live Docker rerun.\n");
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
    case "implement": {
      const { implementWitnessWithCodex } = await import("./codex-loop.js");
      const incident = JSON.parse(readFileSync(resolve(requiredOption(args, "--incident")), "utf8")) as unknown;
      const result = await implementWitnessWithCodex({
        proposalId: requiredOption(args, "--proposal-id"),
        incident,
        behavior: option(args, "--behavior") ?? "The human-reviewed predicate must hold.",
        overlayOut: resolve(requiredOption(args, "--overlay-out")),
        repository: resolve(option(args, "--repo") ?? process.cwd()),
        ...(hasFlag(args, "--with-codex")
          ? {
              runner: {
                async run(codexArgs: readonly string[], options: { cwd: string; input?: string }) {
                  if (!hasFlag(args, "--allow-codex-full-auto")) {
                    throw new Error("Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex.");
                  }
                  const executed = spawnSync("codex", [...codexArgs], {
                    cwd: options.cwd,
                    encoding: "utf8",
                    input: options.input,
                    timeout: 120_000,
                    env: { PATH: process.env.PATH ?? "", COMSPEC: process.env.COMSPEC }
                  });
                  return {
                    exitCode: executed.status,
                    stdout: executed.stdout ?? "",
                    stderr: executed.stderr ?? ""
                  };
                }
              }
            }
          : {})
      });
      process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
      process.exitCode = result.status === "OVERLAY_DRAFTED" || result.status === "TEMPLATE_ONLY" ? 0 : 1;
      return;
    }
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
          ...(option(args, "--model") ? { model: option(args, "--model") } : {}),
          ...(option(args, "--actor") ? { actor: option(args, "--actor") } : {})
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

    case "attach": {
      const file = ledgerPath(args);
      const repository = option(args, "--repo") ? resolve(requiredOption(args, "--repo")) : undefined;
      const turnId = requiredOption(args, "--turn");
      const turnOrdinal = Number(requiredOption(args, "--ordinal"));
      if (!Number.isInteger(turnOrdinal) || turnOrdinal <= 0) throw new Error("--ordinal must be a positive integer.");
      let ledger = appendLifecycleEventAtomic(file, {
        type: "TURN_STARTED",
        payload: { turnId, turnOrdinal, promptDigest: requiredOption(args, "--prompt-digest") }
      });
      ledger = appendLifecycleEventAtomic(file, {
        type: "TURN_COMPLETED",
        payload: {
          turnId,
          turnOrdinal,
          outcome: (option(args, "--outcome") ?? "COMPLETED") as "COMPLETED" | "FAILED" | "INTERRUPTED",
          ...(option(args, "--output-digest") ? { outputDigest: option(args, "--output-digest") } : {}),
          ...(option(args, "--contribution") ? { contribution: option(args, "--contribution") } : {})
        }
      });
      if (hasFlag(args, "--checkpoint")) {
        if (!repository) throw new Error("--repo is required when --checkpoint is used.");
        ledger = appendLifecycleEventAtomic(file, {
          type: "WORKTREE_CHECKPOINT",
          payload: { checkpoint: captureGitCleanCheckpoint(repository), afterTurnOrdinal: turnOrdinal }
        });
      }
      const verification = verifyCodexLifecycleLedger(ledger);
      process.stdout.write(`${JSON.stringify({
        status: verification.valid ? "ATTACHED" : "INVALID",
        ledger: file,
        turnId,
        turnOrdinal,
        checkpoint: hasFlag(args, "--checkpoint") ? "RECORDED" : "NOT_REQUESTED",
        limitation: "Observed sidecar attribution only; FaultLine does not claim private Codex interception or turn-level blame.",
        ...verification
      }, null, 2)}\n`);
      process.exitCode = verification.valid ? 0 : 1;
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
      throw new Error("Usage: fl record init|stdin|checkpoint|attach|verify ...");
  }
}

/**
 * Build an opt-in Codex hook configuration. Hook definitions execute code in
 * a user's agent loop, so FaultLine makes the command visible for review and
 * only writes a new project file after an explicit `--yes`; Codex retains the
 * separate review/trust decision through `/hooks`.
 */
type SidecarHookCommand = {
  readonly command: string;
  readonly commandWindows?: string;
};

function assertSidecarCommandText(value: string, label: string): string {
  const command = value.trim();
  if (!command) throw new Error(`${label} must not be empty.`);
  if (/[\0\r\n]/.test(command)) throw new Error(`${label} cannot contain a NUL byte or a line break.`);
  return command;
}

function quotePosixShellArgument(value: string): string {
  if (/[\0\r\n]/.test(value)) throw new Error("The sidecar CLI path cannot contain a NUL byte or a line break.");
  return `'${value.replaceAll("'", "'\"'\"'")}'`;
}

/** Quote one Windows argv value without relying on the caller's shell syntax. */
function quoteWindowsCommandArgument(value: string): string {
  if (/[\0\r\n]/.test(value)) throw new Error("The sidecar CLI path cannot contain a NUL byte or a line break.");
  let result = '"';
  let backslashes = 0;
  for (const character of value) {
    if (character === "\\") {
      backslashes += 1;
      continue;
    }
    if (character === '"') {
      result += "\\".repeat((backslashes * 2) + 1);
      result += '"';
      backslashes = 0;
      continue;
    }
    result += "\\".repeat(backslashes);
    result += character;
    backslashes = 0;
  }
  result += "\\".repeat(backslashes * 2);
  return `${result}"`;
}

function lstatIfPresent(path: string): ReturnType<typeof lstatSync> | undefined {
  try {
    return lstatSync(path);
  } catch (error) {
    if (error instanceof Error && "code" in error && (error as NodeJS.ErrnoException).code === "ENOENT") return undefined;
    throw new Error(`FaultLine could not inspect ${path}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function sidecarCliCommand(args: string[]): SidecarHookCommand {
  const explicitCommand = option(args, "--command");
  const cliInput = option(args, "--cli");
  if ((explicitCommand === undefined) === (cliInput === undefined)) {
    throw new Error("Provide exactly one of --cli <built-cli.js> or --command <stable-hook-command>.");
  }
  if (cliInput === undefined) {
    const commandWindows = option(args, "--command-windows");
    return {
      command: assertSidecarCommandText(requiredOption(args, "--command"), "--command"),
      ...(commandWindows === undefined ? {} : { commandWindows: assertSidecarCommandText(commandWindows, "--command-windows") })
    };
  }
  if (option(args, "--command-windows") !== undefined) {
    throw new Error("--command-windows is only valid with an explicit --command; --cli generates both platform commands safely.");
  }
  const cliPath = resolve(requiredOption(args, "--cli"));
  if (extname(cliPath).toLowerCase() !== ".js") {
    throw new Error(`--cli must point to the built JavaScript entry point (normally dist/cli.js), not ${cliPath}.`);
  }
  try {
    const stat = lstatSync(cliPath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error("not a real regular file");
  } catch (error) {
    throw new Error(`--cli must point to an existing built, non-symlink CLI file: ${cliPath} (${error instanceof Error ? error.message : String(error)})`);
  }
  return {
    command: `node ${quotePosixShellArgument(cliPath)} codex sidecar hook --quiet`,
    commandWindows: `node ${quoteWindowsCommandArgument(cliPath)} codex sidecar hook --quiet`
  };
}

function codexSidecarHookConfig(command: SidecarHookCommand): Record<string, unknown> {
  const hook = (statusMessage: string) => ({
    type: "command",
    command: command.command,
    ...(command.commandWindows === undefined ? {} : { commandWindows: command.commandWindows }),
    timeout: 30,
    statusMessage
  });
  return {
    hooks: {
      SessionStart: [{
        matcher: "startup|resume|clear|compact",
        hooks: [hook("FaultLine observes this Codex session")]
      }],
      UserPromptSubmit: [{
        hooks: [hook("FaultLine records an observed Codex turn")]
      }],
      Stop: [{
        hooks: [hook("FaultLine captures a clean observed checkpoint when available")]
      }]
    }
  };
}

function sidecarProjectHookTarget(repository: string): { readonly repository: string; readonly directory: string; readonly file: string } {
  const resolvedRepository = resolve(repository);
  if (lstatIfPresent(resolvedRepository) === undefined || resolveSafeDirectorySegment(resolvedRepository) === null) {
    throw new Error(`--repo must be a real directory, not a symbolic link or file: ${resolvedRepository}`);
  }
  try {
    // Sidecar records are deliberately under Git common metadata; reject a
    // non-Git folder before placing an executable project hook into it.
    codexSidecarLedgerPath(resolvedRepository, "faultline-install-check");
  } catch (error) {
    throw new Error(`--repo must be a usable local Git worktree for FaultLine sidecar capture: ${error instanceof Error ? error.message : String(error)}`);
  }
  const directory = join(resolvedRepository, ".codex");
  if (lstatIfPresent(directory) !== undefined && resolveSafeDirectorySegment(directory) === null) {
    throw new Error(`Project .codex directory must be a real directory, not a symbolic link or file: ${directory}`);
  }
  const file = join(directory, "hooks.json");
  // lstat catches a dangling symlink too; preserving a user's hook document
  // is more important than guessing how its JSON should be merged.
  if (lstatIfPresent(file) !== undefined) {
    throw new Error(`FaultLine will not replace an existing project hook document: ${file}. Run sidecar config and merge the reviewed hooks yourself.`);
  }
  return { repository: resolvedRepository, directory, file };
}

function writeSidecarProjectHookConfig(target: { readonly directory: string; readonly file: string }, config: Record<string, unknown>): void {
  if (lstatIfPresent(target.directory) === undefined) {
    mkdirSync(target.directory, { mode: 0o700 });
  }
  if (resolveSafeDirectorySegment(target.directory) === null) {
    throw new Error(`Project .codex directory must be a real directory, not a symbolic link or file: ${target.directory}`);
  }
  try {
    writeFileSync(target.file, `${JSON.stringify(config, null, 2)}\n`, { encoding: "utf8", flag: "wx", mode: 0o600 });
  } catch (error) {
    throw new Error(`FaultLine could not create the project hook document without replacing anything: ${error instanceof Error ? error.message : String(error)}`);
  }
}

async function codexSidecarCommand(args: string[]): Promise<void> {
  const action = args[0];
  if (action === "config") {
    const command = sidecarCliCommand(args);
    // Stdout is the directly installable root hooks document. It is safe to
    // redirect it to a reviewed config file without stripping metadata.
    process.stdout.write(`${JSON.stringify(codexSidecarHookConfig(command), null, 2)}\n`);
    return;
  }
  if (action === "install") {
    const command = sidecarCliCommand(args);
    const config = codexSidecarHookConfig(command);
    const target = sidecarProjectHookTarget(requiredOption(args, "--repo"));
    if (!hasFlag(args, "--yes")) {
      process.stdout.write(`${JSON.stringify({
        status: "CONFIRMATION_REQUIRED",
        target: target.file,
        effect: "Creates a new project .codex/hooks.json containing the displayed reviewed hook configuration. It does not overwrite or merge an existing hook document.",
        hooks: config,
        next: "After reviewing the displayed configuration, rerun the same install command with --yes."
      }, null, 2)}\n`);
      process.exitCode = 1;
      return;
    }
    writeSidecarProjectHookConfig(target, config);
    process.stdout.write(`${JSON.stringify({
      status: "PROJECT_HOOKS_INSTALLED",
      hookFile: target.file,
      next: "Restart or begin Codex in this trusted project, then use /hooks to review and trust the displayed FaultLine hooks before they run."
    }, null, 2)}\n`);
    return;
  }
  if (action === "status") {
    const repository = resolve(option(args, "--repo") ?? process.cwd());
    const inspection = inspectObservedCodexSidecar(repository, option(args, "--session"));
    const invalid = inspection.recordings.some((recording) => !recording.valid);
    process.stdout.write(`${JSON.stringify({
      status: inspection.recordings.length === 0
        ? "NO_SIDECAR_RECORDINGS"
        : invalid ? "SIDECAR_RECORDING_INVALID" : "SIDECAR_RECORDINGS_READY",
      ...inspection,
      next: inspection.recordings.length === 0
        ? ["Install a reviewed project hook document with fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes, review/trust it through /hooks, then begin a Codex session in this Git worktree."]
        : invalid
          ? ["Do not bind an invalid sidecar ledger. Resolve the reported local integrity/storage issue, then start a new observed session."]
          : inspection.recordings.map((recording) => `After human witness freeze, pass --ledger ${recording.ledgerPath} to fl incident continue <id> only when its observed checkpoints apply to the selected Git states.`),
      privacy: "Status reads only sidecar integrity and checkpoint metadata. It never prints prompts, assistant messages, or transcript paths."
    }, null, 2)}\n`);
    process.exitCode = invalid ? 1 : 0;
    return;
  }
  if (action !== "hook") {
    throw new Error("Usage: fl codex sidecar config (--cli <built-cli.js> | --command <hook-command> [--command-windows <hook-command>]) | fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes | fl codex sidecar hook [--input <hook.json>] [--quiet] | fl codex sidecar status [--repo <directory>] [--session <session-id>]");
  }
  const quiet = hasFlag(args, "--quiet");
  try {
    const input = option(args, "--input") === undefined
      ? JSON.parse(await readStandardInput())
      : readJsonInput(requiredOption(args, "--input"));
    const result = recordObservedCodexHook(input);
    if (quiet) {
      // A JSON continuation response is valid for the three configured Codex
      // hook events and prevents an observation failure from blocking a turn.
      process.stdout.write('{"continue":true}\n');
      return;
    }
    process.stdout.write(`${JSON.stringify({
      ...result,
      limitation: "Observed public Codex lifecycle metadata only; this is not private model-state or intent capture."
    }, null, 2)}\n`);
  } catch (error) {
    if (!quiet) throw error;
    // Telemetry must not become a control plane. Surface the actionable
    // refusal (path/rule) on stderr; keep stdout as a JSON continuation so
    // Codex is not blocked. `sidecar status` remains available for health.
    const detail = error instanceof Error ? error.message : String(error);
    process.stderr.write(`FaultLine sidecar did not record this hook: ${detail}\n`);
    process.stderr.write("Run fl codex sidecar status in the repository after the turn for a detailed health check.\n");
    process.stdout.write('{"continue":true}\n');
  }
}

async function initCommand(args: string[]): Promise<void> {
  const repository = resolve(option(args, "--repo") ?? process.cwd());
  const yes = hasFlag(args, "--yes");
  const cliPath = option(args, "--cli");
  const runtimeFlag = option(args, "--runtime");
  if (runtimeFlag !== undefined && runtimeFlag !== "node" && runtimeFlag !== "python" && runtimeFlag !== "go") {
    throw new Error("--runtime must be one of node, python, or go.");
  }
  const runtime = runtimeFlag as ProjectInitRuntime | undefined;
  const resolvedCli = cliPath === undefined ? undefined : resolve(cliPath);

  let installedSidecar = false;
  if (yes && resolvedCli !== undefined) {
    const preview = await planProjectInit({
      repository,
      ...(runtime === undefined ? {} : { runtime }),
      cliPath: resolvedCli
    });
    if (preview.sidecar.status === "PREVIEW_REQUIRED") {
      const command = sidecarCliCommand(["--cli", resolvedCli]);
      writeSidecarProjectHookConfig(sidecarProjectHookTarget(repository), codexSidecarHookConfig(command));
      installedSidecar = true;
    }
  }

  const result = await planProjectInit({
    repository,
    ...(runtime === undefined ? {} : { runtime }),
    ...(resolvedCli === undefined ? {} : { cliPath: resolvedCli }),
    writeIgnoreIfMissing: yes,
    ...(installedSidecar ? { markSidecarInstalled: true } : {})
  });

  process.stdout.write(`${JSON.stringify({
    status: yes ? "INIT_APPLIED" : "INIT_PLAN",
    repository: result.repository,
    suggestedRuntime: result.suggestedRuntime,
    doctor: {
      dockerInvestigationPreflight: result.doctor.dockerInvestigationPreflight,
      likelyRuntime: result.doctor.likelyRuntime,
      diagnosticCount: result.doctor.diagnostics.length
    },
    ignoreFile: result.ignoreFile,
    sidecar: result.sidecar,
    nextCommands: result.nextCommands,
    limitations: result.limitations,
    note: yes
      ? "Scaffolding applied where safe. Images are not pulled; witnesses are not frozen; proof is not claimed."
      : "Dry plan only. Re-run with --yes to write .faultlineignore (when missing) and install sidecar hooks when --cli is provided and hooks are absent."
  }, null, 2)}\n`);
  process.exitCode = 0;
}

async function investigateCommand(args: string[]): Promise<void> {
  const ciLog = option(args, "--ci-log");
  const resumeId = option(args, "--resume");
  if (ciLog !== undefined || resumeId !== undefined) {
    if (args[0] === "git" || args[0] === "turns") {
      throw new Error("Guided investigate (--ci-log / --resume) cannot be combined with `fl investigate git` or `fl investigate turns`.");
    }
    if (ciLog !== undefined && resumeId !== undefined) {
      throw new Error("Guided investigate accepts either --ci-log <file> or --resume <incident-id>, not both.");
    }
    const repository = resolve(option(args, "--repo") ?? process.cwd());
    const from = option(args, "--from");
    const to = option(args, "--to");
    if ((from === undefined) !== (to === undefined)) {
      throw new Error("Guided investigate accepts --from and --to together, or neither for the conservative local HEAD-parent fallback.");
    }
    const command = option(args, "--command");
    const incidentId = option(args, "--id");
    const runtime = option(args, "--runtime");
    const image = option(args, "--image");
    const expectDigest = option(args, "--expect-digest");
    const ledgerFile = option(args, "--ledger");
    const maxStates = option(args, "--max-states");
    const outputDirectory = option(args, "--output");
    const reviewPortRaw = option(args, "--port");
    const witnessStoreOption = option(args, "--store");
    const draftStoreOption = option(args, "--draft-store");
    const abort = new AbortController();
    const onSigint = (): void => abort.abort();
    process.once("SIGINT", onSigint);
    try {
      const result = await guidedInvestigateFromCiLog({
        repository,
        ...(ciLog === undefined ? {} : { ciLogPath: ciLog }),
        ...(resumeId === undefined ? {} : { resumeId }),
        ...(command === undefined ? {} : { command }),
        ...(from === undefined ? {} : { from }),
        ...(to === undefined ? {} : { to }),
        ...(incidentId === undefined ? {} : { incidentId }),
        ...(runtime === undefined ? {} : { runtime }),
        ...(image === undefined ? {} : { image }),
        ...(expectDigest === undefined ? {} : { expectDigest }),
        ...(hasFlag(args, "--unsafe-local") ? { unsafeLocal: true } : {}),
        ...(ledgerFile === undefined ? {} : { ledgerFile }),
        ...(maxStates === undefined ? {} : { maxStates: Number(maxStates) }),
        ...(outputDirectory === undefined ? {} : { outputDirectory }),
        ...(reviewPortRaw === undefined ? {} : { reviewPort: Number(reviewPortRaw) }),
        ...(witnessStoreOption === undefined ? {} : { witnessStore: resolve(witnessStoreOption) }),
        ...(draftStoreOption === undefined ? {} : { draftStore: resolve(draftStoreOption) }),
        onPhase: (phase, detail) => {
          process.stderr.write(`FaultLine guided investigate: ${phase}${detail === undefined ? "" : ` (${detail})`}\n`);
        },
        onReviewReady: (url) => {
          process.stderr.write(
            `FaultLine local witness review: ${url}\nApprove, then Freeze (separate clicks). This command continues automatically after freeze. Press Ctrl+C to cancel and resume later with --resume <id>.\n`
          );
        },
        signal: abort.signal
      });
      process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
      process.exitCode = result.status === "GUIDED_PROOF_BUNDLE_READY" ? 0 : 1;
    } finally {
      process.removeListener("SIGINT", onSigint);
    }
    return;
  }
  if (args[0] === "turns") {
    const { investigateTurnTrees } = await import("./turn-investigation.js");
    const {
      defaultTurnProofRoot,
      verifyTurnInvestigationProofBundle,
      writeTurnInvestigationProofBundle
    } = await import("./turn-proof-bundle.js");
    const store = witnessStore(args);
    const proposalId = requiredOption(args, "--proposal");
    const repository = resolve(requiredOption(args, "--repo"));
    const latest = hasFlag(args, "--latest");
    const ledgerOption = option(args, "--ledger");
    if (latest === (ledgerOption !== undefined)) {
      throw new Error("fl investigate turns requires exactly one of --ledger <ledger.json> or --latest.");
    }
    let ledgerPath: string;
    let latestResolution: ReturnType<typeof resolveLatestSidecarLedgerPath> | null = null;
    if (latest) {
      try {
        latestResolution = resolveLatestSidecarLedgerPath(repository);
      } catch (error) {
        if (error instanceof CodexSidecarError) throw error;
        throw error;
      }
      ledgerPath = resolve(latestResolution.ledgerPath);
    } else {
      ledgerPath = resolve(requiredOption(args, "--ledger"));
    }
    const frozenWitness = readFrozenWitness(store, proposalId);
    const runtimeMappingPath = option(args, "--runtime-mapping");
    let runtimeMapping: Record<string, string> | undefined;
    if (runtimeMappingPath !== undefined) {
      const parsed = JSON.parse(readFileSync(resolve(runtimeMappingPath), "utf8")) as unknown;
      if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
        throw new Error("--runtime-mapping must be a JSON object of fingerprintDigest → digest-pinned image.");
      }
      runtimeMapping = Object.fromEntries(
        Object.entries(parsed as Record<string, unknown>).map(([digest, image]) => {
          if (typeof image !== "string") {
            throw new Error(`--runtime-mapping entry for ${digest} must be a digest-pinned image string.`);
          }
          return [digest, image];
        })
      );
    }
    const result = await investigateTurnTrees({
      repository,
      ledgerPath,
      frozenWitness,
      expectedFrozenDigest: requiredOption(args, "--expect-digest"),
      image: requiredOption(args, "--image"),
      ...(runtimeMapping === undefined ? {} : { runtimeMapping })
    });
    if (!result.proof.isProof) {
      process.stdout.write(`${JSON.stringify({
        investigation: result,
        ...(latestResolution === null ? {} : { ledger: latestResolution }),
        proofBundle: null,
        note: "No portable turn proof bundle was written because the investigation did not establish Docker-isolated turn proof eligibility. Evidence grade remains experimental even when transitions are present."
      }, null, 2)}\n`);
      process.exitCode = 1;
      return;
    }
    const proofRoot = defaultTurnProofRoot();
    const output = resolve(option(args, "--output") ?? join(proofRoot, `turns-${Date.now()}`));
    const bundle = await writeTurnInvestigationProofBundle(output, result, frozenWitness, {
      proofRoot,
      lifecycleLedger: readVerifiedCodexLifecycleLedger(ledgerPath),
      repository
    });
    const bundleVerification = await verifyTurnInvestigationProofBundle(bundle.directory, bundle.rootDigest);
    if (!bundleVerification.valid) {
      throw new Error(`Generated turn proof bundle failed verification: ${bundleVerification.errors.join("; ")}`);
    }
    let minimization: unknown = null;
    if (hasFlag(args, "--minimize")) {
      const transitionOpt = option(args, "--transition");
      const maxExecOpt = option(args, "--max-executions");
      const minOutOpt = option(args, "--minimization-output");
      minimization = await minimizeFromTurnInvestigation({
        repository,
        result,
        frozenWitness,
        expectedFrozenDigest: requiredOption(args, "--expect-digest"),
        image: requiredOption(args, "--image"),
        ...(transitionOpt === undefined ? {} : { transitionIndex: transitionOpt }),
        ...(maxExecOpt === undefined ? {} : { maxExecutions: maxExecOpt }),
        ...(minOutOpt === undefined ? {} : { output: minOutOpt })
      });
    }
    process.stdout.write(`${JSON.stringify({
      investigation: result,
      ...(latestResolution === null ? {} : { ledger: latestResolution }),
      proofBundle: {
        directory: bundle.directory,
        rootDigest: bundle.rootDigest,
        evidenceGrade: result.proof.evidenceGrade,
        evidenceLabel: result.proof.evidenceLabel,
        externalRootStatus: bundleVerification.externalRootStatus,
        note: "Turn package is experimentally graded (EXPERIMENTAL_TURN), not COMMIT_PROOF."
      },
      minimization
    }, null, 2)}\n`);
    process.exitCode = 0;
    return;
  }
  if (args[0] !== "git") {
    throw new Error("Usage: fl investigate --ci-log <file> --repo <directory> [...] | fl investigate --resume <id> --repo <directory> [...] | fl investigate turns --repo ... (--ledger ... | --latest) --proposal ... --expect-digest ... --image ... [--runtime-mapping <mapping.json>] [--output <dir>] | fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--ledger <ledger.json>] [--output <managed-bundle-directory>]");
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
    evidenceGrade: result.proof.evidenceGrade,
    evidenceLabel: result.proof.evidenceLabel,
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

async function minimizeFromTurnInvestigation(options: {
  repository: string;
  result: { transitions: import("./turn-investigation.js").TurnInvestigationResult["transitions"] };
  frozenWitness: import("./witness-lock.js").FrozenWitness;
  expectedFrozenDigest: string;
  image: string;
  transitionIndex?: string;
  maxExecutions?: string;
  output?: string;
}): Promise<Record<string, unknown>> {
  const { bridgeTurnTransitionToMinimizationCommits } = await import("./turn-minimization-bridge.js");
  const transitionIndex = options.transitionIndex === undefined ? 0 : Number(options.transitionIndex);
  if (!Number.isInteger(transitionIndex) || transitionIndex < 0) {
    throw new Error("--transition must be a non-negative integer.");
  }
  const bridge = bridgeTurnTransitionToMinimizationCommits(
    options.repository,
    options.result,
    transitionIndex
  );
  const minimization = await minimizeGitDiff({
    repository: options.repository,
    before: bridge.beforeCommit,
    after: bridge.afterCommit,
    frozenWitness: options.frozenWitness,
    expectedFrozenDigest: options.expectedFrozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image: options.image },
    ...(options.maxExecutions === undefined ? {} : { budget: { maxExecutions: Number(options.maxExecutions) } })
  });
  const output = resolve(
    options.output
      ?? join(".faultline", "minimizations", `turn-transition-${String(transitionIndex).padStart(4, "0")}-${Date.now()}.json`)
  );
  const written = await writeGitMinimizationResult(output, minimization);
  return {
    bridge: {
      transitionIndex: bridge.transitionIndex,
      beforeTree: bridge.beforeTree,
      afterTree: bridge.afterTree,
      beforeCommit: bridge.beforeCommit,
      afterCommit: bridge.afterCommit,
      note: bridge.note
    },
    result: minimization,
    path: written.path,
    resultDigest: written.resultDigest,
    evidenceGradeNote:
      "Counterfactual minimization from a turn boundary reuses Git-path machinery on synthetic commits. The parent turn package remains EXPERIMENTAL_TURN until TURN_PROOF promotion criteria are met."
  };
}

/**
 * Run Git-path counterfactual minimization against a selected transition from a
 * verified turn package, using tree digests still present in the host repo.
 */
async function proveTransitionCommand(args: string[]): Promise<void> {
  const bundleDirectory = args[0];
  if (!bundleDirectory) {
    throw new Error(
      "Usage: fl prove transition <turn-proof-bundle-directory> --repo <directory> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--transition <index>] [--max-executions <count>] [--output <managed-result.json>]"
    );
  }
  if (args[1] !== undefined && args[1] !== "--repo" && !args[1].startsWith("--")) {
    // allow `fl prove transition <dir> ...` only
  }
  const { verifyTurnInvestigationProofBundle } = await import("./turn-proof-bundle.js");
  const { TurnInvestigationResultSchema } = await import("./turn-investigation.js");
  const directory = resolve(bundleDirectory);
  const expectedRoot = option(args, "--expect-root");
  const verification = await verifyTurnInvestigationProofBundle(directory, expectedRoot);
  if (!verification.valid) {
    throw new Error(`Turn proof bundle failed verification: ${verification.errors.join("; ") || "unknown error"}`);
  }
  const investigation = TurnInvestigationResultSchema.parse(
    JSON.parse(readFileSync(join(directory, "investigation.json"), "utf8"))
  );
  const store = witnessStore(args);
  const frozenWitness = readFrozenWitness(store, requiredOption(args, "--proposal"));
  const transitionOpt = option(args, "--transition");
  const maxExecOpt = option(args, "--max-executions");
  const outOpt = option(args, "--output");
  const minimization = await minimizeFromTurnInvestigation({
    repository: resolve(requiredOption(args, "--repo")),
    result: investigation,
    frozenWitness,
    expectedFrozenDigest: requiredOption(args, "--expect-digest"),
    image: requiredOption(args, "--image"),
    ...(transitionOpt === undefined ? {} : { transitionIndex: transitionOpt }),
    ...(maxExecOpt === undefined ? {} : { maxExecutions: maxExecOpt }),
    ...(outOpt === undefined ? {} : { output: outOpt })
  });
  process.stdout.write(`${JSON.stringify({
    command: "prove transition",
    turnBundle: {
      directory,
      rootDigest: verification.rootDigest,
      externalRootStatus: verification.externalRootStatus,
      evidenceGrade: investigation.proof.evidenceGrade
    },
    minimization
  }, null, 2)}\n`);
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
  if (schemaVersion === "faultline.turn-proof-bundle.v1") {
    throw new Error("Attestation create currently supports Git proof bundles only. Verify turn packages with `fl verify` (EXPERIMENTAL_TURN).");
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

/** Write or verify a `faultline.prevention-proof.v1` package (three-state PASS→FAIL→PASS). */
async function preventionCommand(args: string[]): Promise<void> {
  const [action, target] = args;
  if (action === "verify") {
    if (!target) throw new Error("Usage: fl prevention verify <prevention-proof-directory> [--expect-root <sha256:...>]");
    const verification = verifyPreventionProof(resolve(target), option(args, "--expect-root"));
    process.stdout.write(`${JSON.stringify({
      valid: verification.valid,
      schemaVersion: PREVENTION_PROOF_SCHEMA_VERSION,
      classification: verification.manifest?.classification ?? null,
      rootDigest: verification.rootDigest,
      externalRootStatus: verification.externalRootStatus,
      errors: verification.errors
    }, null, 2)}\n`);
    process.exitCode = verification.valid ? 0 : 1;
    return;
  }
  if (action === "write") {
    const inputPath = requiredOption(args, "--input");
    const output = resolve(option(args, "--output") ?? join(defaultPreventionProofRoot(), `prevention-${Date.now()}`));
    const input = JSON.parse(readFileSync(resolve(inputPath), "utf8")) as PreventionProofWriteInput;
    const written = writePreventionProof(output, input);
    process.stdout.write(`${JSON.stringify({
      status: "PREVENTION_EVIDENCE_SUMMARY",
      directory: written.directory,
      rootDigest: written.rootDigest,
      originalProofRoot: written.prevention.originalProofRoot,
      frozenWitnessDigest: written.prevention.frozenWitnessDigest
    }, null, 2)}\n`);
    return;
  }
  throw new Error("Usage: fl prevention write --input <prevention-input.json> [--output <managed-directory>] | fl prevention verify <directory> [--expect-root <sha256:...>]");
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
  if (hasFlag(args, "--bundle") && args[0] !== "brief") {
    const { createFrozenWitnessRepairVerifier, repairWithCodex } = await import("./codex-loop.js");
    const withCodex = hasFlag(args, "--with-codex");
    const result = await repairWithCodex({
      bundleDirectory: resolve(requiredOption(args, "--bundle")),
      expectRoot: requiredOption(args, "--expect-root"),
      repository: resolve(option(args, "--repo") ?? process.cwd()),
      outputDirectory: resolve(option(args, "--output") ?? join(".faultline", "repairs", `repair-${Date.now()}`)),
      withCodex,
      instructionsOnly: hasFlag(args, "--instructions-only"),
      keepWorktree: hasFlag(args, "--keep-worktree"),
      verifyBundle: (directory, expectRoot) => {
        const verification = verifyGitInvestigationProofBundle(directory, expectRoot);
        return {
          valid: verification.valid,
          errors: [...verification.errors],
          rootDigest: verification.rootDigest
        };
      },
      // When Codex drafts a repair, always verify the frozen witness fail-closed.
      ...(withCodex ? { verifyCandidate: createFrozenWitnessRepairVerifier() } : {}),
      ...(withCodex
        ? {
            runner: {
              async run(codexArgs: readonly string[], options: { cwd: string; input?: string }) {
                if (!hasFlag(args, "--allow-codex-full-auto")) {
                  throw new Error("Codex repair drafting requires explicit --allow-codex-full-auto in addition to --with-codex.");
                }
                const executed = spawnSync("codex", [...codexArgs], {
                  cwd: options.cwd,
                  encoding: "utf8",
                  input: options.input,
                  timeout: 180_000,
                  env: { PATH: process.env.PATH ?? "", COMSPEC: process.env.COMSPEC }
                });
                return {
                  exitCode: executed.status,
                  stdout: executed.stdout ?? "",
                  stderr: executed.stderr ?? ""
                };
              }
            }
          }
        : {})
    });
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
    process.exitCode = result.status === "BUNDLE_INVALID" || result.status === "REPAIR_SETUP_FAILED"
      || result.status === "REPAIR_VERIFICATION_FAILED" || result.status === "WORKTREE_CLEANUP_FAILED"
      ? 1
      : 0;
    return;
  }
  if (args[0] !== "brief") {
    throw new Error("Usage: fl repair --bundle <dir> --expect-root <digest> [--with-codex] | fl repair brief|verify ...");
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
    case "judge-proof":
      await judgeProofCommand(args);
      return;
    case "commit-proof-preview":
      commitProofPreviewCommand(args);
      return;
    case "judge-preview":
      judgePreviewCommand(args);
      return;
    case "doctor":
      await doctorCommand(args);
      return;
    case "init":
      await initCommand(args);
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
      if (schemaVersion === PREVENTION_PROOF_SCHEMA_VERSION) {
        const result = verifyPreventionProof(root, option(args, "--expect-root"));
        process.stdout.write(`${result.externalRootStatus === "NOT_PROVIDED" ? "Prevention proof self-consistency" : "Integrity"}: ${result.valid ? "VALID" : "INVALID"}\n`);
        process.stdout.write(`Classification: ${result.manifest?.classification ?? "unavailable"}\nBundle root: ${result.rootDigest ?? "unavailable"}\nExternal root: ${result.externalRootStatus}\n`);
        if (result.valid) process.stdout.write("Prevention evidence summary\n");
        if (!result.valid) process.stdout.write(`${result.errors.map((error) => `- ${error}`).join("\n")}\n`);
        process.exitCode = result.valid ? 0 : 1;
        return;
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
        const prevention = hasFlag(args, "--prevention") ? requiredOption(args, "--prevention") : undefined;
        const expectedMinimization = hasFlag(args, "--expect-minimization") ? requiredOption(args, "--expect-minimization") : undefined;
        const expectedRepair = hasFlag(args, "--expect-repair") ? requiredOption(args, "--expect-repair") : undefined;
        const expectedPrevention = hasFlag(args, "--expect-prevention") ? requiredOption(args, "--expect-prevention") : undefined;
        if ((minimization === undefined) !== (expectedMinimization === undefined)) {
          throw new Error("A shareable minimization attachment requires both --minimization <result.json> and --expect-minimization <retained-digest>.");
        }
        if ((repair === undefined) !== (expectedRepair === undefined)) {
          throw new Error("A shareable repair attachment requires both --repair <repair-brief-directory> and --expect-repair <retained-artifact-digest>.");
        }
        if ((prevention === undefined) !== (expectedPrevention === undefined)) {
          throw new Error("A shareable prevention attachment requires both --prevention <prevention-proof-directory> and --expect-prevention <retained-root-digest>.");
        }
        const proof = loadVerifiedGitProofView(bundleDirectory, option(args, "--expect-root"), {
          ...(minimization === undefined ? {} : { minimizationFile: resolve(minimization) }),
          ...(expectedMinimization === undefined ? {} : { expectedMinimizationDigest: expectedMinimization }),
          ...(repair === undefined ? {} : { repairDirectory: resolve(repair) }),
          ...(expectedRepair === undefined ? {} : { expectedRepairDigest: expectedRepair }),
          ...(prevention === undefined ? {} : { preventionDirectory: resolve(prevention) }),
          ...(expectedPrevention === undefined ? {} : { expectedPreventionDigest: expectedPrevention })
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
      if (args[0] === "sidecar") {
        await codexSidecarCommand(args.slice(1));
        return;
      }
      if (hasFlag(args, "--dry-run")) {
        process.stdout.write(`${JSON.stringify({ adapter: "observed-codex-hook-sidecar", status: "DRY_RUN", records: ["public session and turn identifiers", "prompt digest only", "clean Git checkpoint when available"], limitation: "Install and trust the emitted hook configuration to observe public lifecycle metadata. FaultLine does not intercept private model state, transcripts, or reasoning." }, null, 2)}\n`);
        return;
      }
      if (hasFlag(args, "--snapshot")) {
        const repository = resolve(option(args, "--repo") ?? process.cwd());
        const snapshot = captureCleanGitSnapshot(repository);
        const file = writeGitSidecarSnapshot(repository, snapshot);
        process.stdout.write(`${JSON.stringify({ snapshot, file, limitation: "This is a clean Git sidecar snapshot, not a live Codex transport event." }, null, 2)}\n`);
        return;
      }
      throw new Error("The current build exposes an observed Codex hook sidecar and a clean Git snapshot. Run: fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes, fl codex --dry-run, or fl codex --snapshot --repo <directory>");
    case "witness":
      await witnessCommand(args);
      return;
    case "record":
      await recordCommand(args);
      return;
    case "investigate":
      await investigateCommand(args);
      return;
    case "prove":
      if (args[0] !== "transition") {
        throw new Error(
          "Usage: fl prove transition <turn-proof-bundle-directory> --repo <directory> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--transition <index>] [--max-executions <count>] [--output <managed-result.json>]"
        );
      }
      await proveTransitionCommand(args.slice(1));
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
    case "prevention":
      await preventionCommand(args);
      return;
    default:
      throw new Error(`Unknown command: ${command}`);
  }
}

function formatCliFailure(error: unknown): string {
  if (error instanceof ZodError) {
    const firstIssue = error.issues[0];
    const pathInfo = firstIssue?.path?.length ? ` at input.${firstIssue.path.join(".")}` : "";
    const issueMsg = firstIssue ? `${firstIssue.message}${pathInfo}` : "Invalid schema layout";
    return [
      `FaultLine error: Invalid input shape (${issueMsg})`,
      "Tip: Verify that the JSON payload or input file matches the expected structure."
    ].join("\n");
  }

  const isPortInUse =
    (error !== null && typeof error === "object" && "code" in error && error.code === "EADDRINUSE")
    || (error instanceof Error && error.message.includes("EADDRINUSE"));
  if (isPortInUse) {
    return [
      "FaultLine error: Port already in use (EADDRINUSE).",
      "Tip: Another instance of FaultLine or another process is running on this port.",
      "     Please stop the conflicting process or pass a different port using the '--port' flag."
    ].join("\n");
  }

  const message = error instanceof Error ? error.message : String(error);

  // Intercept missing required options/flags
  if (
    /^Missing required option:/i.test(message)
    || /missing required argument/i.test(message)
    || /required option/i.test(message)
  ) {
    const flagMatch = message.match(/(--\w+)/);
    const flagTip = flagMatch
      ? `     Make sure to provide the ${flagMatch[0]} flag.`
      : "     Make sure to provide all required flags.";
    return [
      `FaultLine error: ${message}`,
      "Tip: You are missing a mandatory flag for this command.",
      flagTip,
      "     Run 'pnpm fl help' to view valid options and usage instructions."
    ].join("\n");
  }

  const windowsHint = [
    "",
    "Windows tip: If PowerShell blocked pnpm due to ExecutionPolicy restrictions, run:",
    "  Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope Process",
    "Or invoke the command proxy directly:",
    "  pnpm.cmd fl <command> (e.g., pnpm.cmd fl judge-demo)"
  ].join("\n");

  if (/^Unknown command:\s*--\b/.test(message) || message.startsWith("Unknown command: --")) {
    return `FaultLine error: ${message}\nNote: Do not place '--' between 'fl' and your subcommand. Use 'pnpm fl <command>'.`;
  }

  if (message.startsWith("Unknown command:")) {
    return `FaultLine error: ${message}\nRun 'pnpm fl help' or check the documentation for valid options.`;
  }

  if (/ExecutionPolicy|running scripts is disabled|PSSecurityException|UnauthorizedAccess/i.test(message)) {
    return [`FaultLine error: ${message}`, windowsHint].join("\n");
  }

  return `FaultLine error: ${message}`;
}

main().catch((error: unknown) => {
  process.stderr.write(`${formatCliFailure(error)}\n`);
  process.exitCode = 1;
});
