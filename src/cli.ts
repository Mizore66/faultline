#!/usr/bin/env node
import { existsSync, lstatSync, readFileSync } from "node:fs";
import { isAbsolute, join, relative, resolve } from "node:path";
import { defaultWitnessProposal, proposeWitnessWithGpt } from "./ai.js";
import {
  defaultAttestationStore,
  verifyStoredBundleAttestation,
  writeBundleAttestation
} from "./attestation.js";
import { createDemoAnalysis } from "./engine.js";
import { captureCleanGitSnapshot, writeGitSidecarSnapshot } from "./git-snapshot.js";
import { GitInvestigationResultSchema, investigateGitRange } from "./git-investigation.js";
import {
  defaultGitProofRoot,
  verifyGitInvestigationProofBundle,
  writeGitInvestigationProofBundle
} from "./git-proof-bundle.js";
import { minimizeGitDiff, writeGitMinimizationResult } from "./git-minimization.js";
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
import { describeBundlePath, verifyProofBundle, writeProofBundle } from "./proof-bundle.js";
import { redactValue } from "./redaction.js";
import { startFaultLineServer } from "./server.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  readFrozenWitness,
  verifyFrozenWitness
} from "./witness-lock.js";
import type { RunMode } from "./domain.js";

const usage = `FaultLine — executable evidence for agent-assisted code

Usage:
  fl judge-demo [--replay | --rerun-all] [--output <directory>] [--export-only]
  fl verify <proof-bundle-directory> [--expect-root <sha256:...>]
  fl serve [--port <number>]
  fl codex --dry-run | --snapshot [--repo <directory>]
  fl codex record <init|stdin|checkpoint|verify> [...]
  fl record <init|stdin|checkpoint|attach|verify> [...]
  fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--ledger <ledger.json>] [--output <bundle-directory>]
  fl minimize git --repo <directory> --before <commit> --after <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--max-executions <count>] [--output <result.json>]
  fl ledger bind --ledger <ledger.json> --investigation <investigation.json> --output <binding.json>
  fl ledger verify <binding.json> [--expect-digest <sha256:...>]
  fl attest create --bundle <proof-bundle-directory> --receipt <id> --subject <label> --issuer <label> [--store <directory>]
  fl attest verify <receipt-id> [--expect-digest <sha256:...>] [--store <directory>]
  fl witness propose --input <proposal.json> [--store <directory>]
  fl witness propose --live --incident <incident.json> --proposal-id <id> --overlay-root <directory> [--model <model>] [--store <directory>]
  fl witness approve <proposal-id> --approved-by <actor> [--store <directory>]
  fl witness freeze <proposal-id> [--store <directory>]
  fl witness verify <proposal-id> [--expect-digest <sha256:...>] [--store <directory>]

The judge demo is a reviewed, deterministic Node fixture. It does not require an OpenAI API key.
The lifecycle adapter accepts observed Codex-compatible events; it does not claim to intercept private Codex internals.
Use --live for a GPT-5.6 witness proposal after setting OPENAI_API_KEY.`;

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
  const nested = relative(root, output);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error(`${label} output must be a file beneath ${root}`);
  }
  const parts = nested.split(/[\\/]+/).filter(Boolean);
  if (parts.length < 1) throw new Error(`${label} output must have a file name.`);
  let current = root;
  if (existsSync(current)) {
    const stat = lstatSync(current);
    if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error(`${label} root must be a real directory: ${root}`);
  }
  for (const part of parts.slice(0, -1)) {
    current = join(current, part);
    if (!existsSync(current)) continue;
    const stat = lstatSync(current);
    if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error(`${label} output cannot traverse a symbolic link or non-directory: ${current}`);
  }
  if (existsSync(output)) throw new Error(`${label} output already exists and will not be replaced: ${output}`);
  return output;
}

function modelOverlayInput(overlayRoot: string, paths: string[]): Array<{ path: string; bytesBase64: string }> {
  const root = resolve(overlayRoot);
  const rootStat = lstatSync(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error(`Overlay root must be a real directory: ${root}`);
  return paths.map((path) => {
    const target = resolve(root, path);
    const nested = relative(root, target);
    if (!nested || nested.startsWith("..") || isAbsolute(nested) || path.includes("\\")) {
      throw new Error(`Model proposed an unsafe overlay path: ${path}`);
    }
    const stat = lstatSync(target);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`Model overlay must be a regular non-symlink file: ${path}`);
    return { path: path.replaceAll("\\", "/"), bytesBase64: readFileSync(target).toString("base64") };
  });
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

async function witnessCommand(args: string[]): Promise<void> {
  const [action, proposalId] = args;
  const store = witnessStore(args);
  switch (action) {
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
            overlays: modelOverlayInput(overlayRoot, generated.proposal.overlayFiles),
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
    case "verify": {
      if (!proposalId) throw new Error("Usage: fl witness verify <proposal-id> [--expect-digest <sha256:...>]");
      const result = verifyFrozenWitness(store, proposalId, option(args, "--expect-digest"));
      process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
      process.exitCode = result.valid ? 0 : 1;
      return;
    }
    default:
      throw new Error("Usage: fl witness propose|approve|freeze|verify ...");
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
  if (args[0] !== "git") {
    throw new Error("Usage: fl minimize git --repo <directory> --before <commit> --after <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--max-executions <count>] [--output <result.json>]");
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
  const path = await writeGitMinimizationResult(output, result);
  process.stdout.write(`${JSON.stringify({
    minimization: result,
    resultFile: path,
    proof: result.proof.isProof ? "BIDIRECTIONALLY_CERTIFIED" : "NOT_CERTIFIED"
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

async function main(): Promise<void> {
  const [command, ...args] = process.argv.slice(2);
  switch (command) {
    case undefined:
    case "help":
    case "--help":
    case "-h":
      process.stdout.write(`${usage}\n`);
      return;
    case "judge-demo":
      await judgeDemo(args);
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
    default:
      throw new Error(`Unknown command: ${command}\n\n${usage}`);
  }
}

main().catch((error: unknown) => {
  process.stderr.write(`FaultLine error: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
});
