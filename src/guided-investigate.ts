import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { doctorCliExitCode, runFaultLineDoctor, type FaultLineDoctorReport } from "./doctor.js";
import {
  runFrozenIncidentContinuation,
  type IncidentContinuationResult
} from "./incident-continue.js";
import { createIncidentDraft, type IncidentDraft } from "./incident.js";
import { defaultIncidentDraftStore, readIncidentDraft, writeIncidentDraft } from "./incident-store.js";
import { resolveCuratedRuntime } from "./runtime.js";
import { startWitnessReviewServer } from "./witness-review-server.js";
import { proposeWitness, verifyFrozenWitness } from "./witness-lock.js";

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

const FREEZE_POLL_MS = 500;

export type GuidedInvestigatePhase =
  | "PREFLIGHT"
  | "INTAKE"
  | "WITNESS_REVIEW"
  | "RUNTIME_SELECTION"
  | "LOCALIZATION"
  | "PROOF_EXPORT";

export type GuidedFreezeWaitResult = {
  readonly frozenDigest: string;
  readonly reviewUrl: string | null;
  readonly alreadyFrozen: boolean;
};

/**
 * Injectable freeze gate for tests. Production waits on the local review
 * server until a human Approve + Freeze writes the immutable record.
 */
export type WaitForGuidedFreeze = (options: {
  readonly proposalId: string;
  readonly witnessStore: string;
  readonly draftStore: string;
  readonly port?: number;
  readonly pollIntervalMs?: number;
  readonly onReviewReady?: (url: string) => void;
  readonly signal?: AbortSignal;
}) => Promise<GuidedFreezeWaitResult>;

export type GuidedInvestigateResult =
  | {
    readonly status: "PREFLIGHT_BLOCKED";
    readonly doctor: FaultLineDoctorReport;
    readonly proofReady: boolean;
    readonly draft: null;
    readonly draftPath: null;
    readonly reviewUrl: null;
    readonly retainedFrozenDigest: null;
    readonly continuation: null;
    readonly nextCommands: readonly string[];
    readonly note: string;
  }
  | {
    readonly status: "GUIDED_PROOF_BUNDLE_READY" | "GUIDED_INVESTIGATION_NOT_PROOF";
    readonly doctor: FaultLineDoctorReport;
    readonly proofReady: boolean;
    readonly draft: IncidentDraft;
    readonly draftPath: string;
    readonly reviewUrl: string | null;
    readonly retainedFrozenDigest: string;
    readonly continuation: IncidentContinuationResult;
    readonly nextCommands: readonly string[];
    readonly note: string;
  };

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolveSleep, rejectSleep) => {
    if (signal?.aborted) {
      rejectSleep(new Error("Guided investigate cancelled before freeze."));
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolveSleep();
    }, ms);
    const onAbort = (): void => {
      clearTimeout(timer);
      rejectSleep(new Error("Guided investigate cancelled before freeze."));
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function deriveSymptom(ciLog: string): string {
  const lines = ciLog.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  const failure = [...lines].reverse().find((line) => /fail|error|expected|assert/i.test(line));
  return (failure ?? lines.at(-1) ?? "CI reported a failure; review the attached log.").slice(0, 500);
}

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
    throw new Error(`FaultLine could not read a local Git HEAD for guided intake: ${detail || "supply --from and --to explicitly"}`);
  }
  const lines = String(result.stdout ?? "").replace(/\r/g, "").split("\n");
  const head = lines[0]?.trim();
  const parentLine = lines[1]?.trim() ?? "";
  if (!head) throw new Error("FaultLine could not read a local Git HEAD for guided intake; supply --from and --to explicitly.");
  return { head, parents: parentLine ? parentLine.split(/\s+/).filter(Boolean) : [] };
}

/**
 * Production freeze waiter: open the local review workbench, poll until the
 * write-once frozen record exists, then bind its digest in-process as the
 * session-retained `--expect-digest` for the rest of the guided flow.
 *
 * Approve and Freeze remain separate explicit human clicks in the UI.
 * This waiter never auto-approves or auto-freezes.
 */
export const defaultWaitForGuidedFreeze: WaitForGuidedFreeze = async (options) => {
  const existing = verifyFrozenWitness(options.witnessStore, options.proposalId);
  if (existing.valid && existing.frozenDigest !== null) {
    return {
      frozenDigest: existing.frozenDigest,
      reviewUrl: null,
      alreadyFrozen: true
    };
  }

  const server = await startWitnessReviewServer({
    store: options.witnessStore,
    proposalId: options.proposalId,
    draftStore: options.draftStore,
    port: options.port ?? 0
  });
  options.onReviewReady?.(server.url);

  const pollMs = options.pollIntervalMs ?? FREEZE_POLL_MS;
  try {
    for (;;) {
      if (options.signal?.aborted) {
        throw new Error("Guided investigate cancelled before freeze.");
      }
      const verification = verifyFrozenWitness(options.witnessStore, options.proposalId);
      if (verification.valid && verification.frozenDigest !== null) {
        return {
          frozenDigest: verification.frozenDigest,
          reviewUrl: server.url,
          alreadyFrozen: false
        };
      }
      await sleep(pollMs, options.signal);
    }
  } finally {
    await server.close();
  }
};

async function createGuidedDraft(options: {
  readonly repository: string;
  readonly ciLogPath: string;
  readonly command?: string;
  readonly from?: string;
  readonly to?: string;
  readonly incidentId?: string;
  readonly runtimeAlias?: string;
  readonly timeoutSeconds?: number;
  readonly witnessStore: string;
  readonly draftStore: string;
}): Promise<{ draft: IncidentDraft; draftPath: string }> {
  if ((options.from === undefined) !== (options.to === undefined)) {
    throw new Error("Guided investigate accepts --from and --to together, or neither for the conservative local HEAD-parent fallback.");
  }
  const ciLog = readFileSync(resolve(options.ciLogPath), "utf8");
  const command = options.command?.trim()
    || "node -e \"console.log('Replace this command with the failing CI predicate after human review'); process.exit(1)\"";
  const draftId = options.incidentId ?? `guided-${Date.now().toString(36)}`;
  const timeoutSeconds = options.timeoutSeconds ?? 300;
  if (!Number.isInteger(timeoutSeconds) || timeoutSeconds < 1 || timeoutSeconds > 3_600) {
    throw new Error("--timeout-seconds must be an integer from 1 through 3600.");
  }
  const runtime = options.runtimeAlias === undefined
    ? undefined
    : await resolveCuratedRuntime(options.runtimeAlias);
  const localHead = locallyObservedHead(options.repository);
  const draftInput = {
    draftId,
    createdAt: new Date().toISOString(),
    repository: options.repository,
    command,
    ...(options.from && options.to
      ? { range: { ancestor: options.from, descendant: options.to } }
      : { localHead }),
    ...(runtime === undefined ? {} : { runtime: { requested: runtime.requested, image: runtime.image } })
  };

  const preliminary = createIncidentDraft(draftInput);
  const proposal = proposeWitness(options.witnessStore, {
    proposalId: preliminary.draftId,
    proposalOrigin: "HUMAN",
    incidentPacket: {
      symptom: deriveSymptom(ciLog),
      ciLog: ciLog.slice(0, 4_000),
      repositoryLanguage: "Unknown",
      repositorySummary: `Guided CI-log intake for local range ${preliminary.range.ancestor} → ${preliminary.range.descendant}. Review-only until human approval and freeze; nothing executed at intake.`
    },
    witness: {
      behavior: "The human-supplied command must exit successfully at the selected immutable Git states.",
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
  const stored = writeIncidentDraft(options.draftStore, draft);
  return { draft, draftPath: stored.path };
}

async function resolveGuidedRuntimeImage(options: {
  readonly draft: IncidentDraft;
  readonly image?: string;
  readonly runtimeAlias?: string;
  readonly unsafeLocal: boolean;
}): Promise<string | undefined> {
  if (options.draft.runtime !== undefined) {
    if (options.image !== undefined && options.image !== options.draft.runtime.image) {
      throw new Error("--image must match the digest-pinned runtime recorded in the immutable incident draft.");
    }
    return options.draft.runtime.image;
  }
  if (options.image !== undefined) return options.image;
  if (options.runtimeAlias !== undefined) {
    const resolved = await resolveCuratedRuntime(options.runtimeAlias);
    return resolved.image;
  }
  if (options.unsafeLocal) return undefined;
  throw new Error(
    "Guided investigate needs a digest-pinned runtime before localization. Pass --runtime <node|python|go> after pulling the curated tag, or --image <digest-pinned-image>, or use --unsafe-local for non-proof diagnosis only."
  );
}

/**
 * Option B state machine for `fl investigate --ci-log` / `--resume`:
 *
 * 1. PREFLIGHT — `fl doctor` (block only when Node is not READY)
 * 2. INTAKE — create write-once draft + proposed witness from CI log,
 *    or load an existing draft via `--resume <id>` (state preserved on disk)
 * 3. WITNESS_REVIEW — start local review server; pause until human Approve
 *    then Freeze (separate clicks). Register the freeze digest in-process as
 *    the session-retained expect-digest. Never auto-approve/auto-freeze.
 * 4. RUNTIME_SELECTION — draft-bound image, else `--image`, else `--runtime`
 *    resolve (never pulls). Fail closed for proof-grade without an image.
 * 5. LOCALIZATION — `runFrozenIncidentContinuation` → `investigateGitRange`
 * 6. PROOF_EXPORT — write/verify proof bundle when proof-grade; surface
 *    `fl serve` next command. Modular primitives stay intact under one CLI.
 */
export async function guidedInvestigateFromCiLog(options: {
  readonly repository: string;
  readonly ciLogPath?: string;
  readonly resumeId?: string;
  readonly command?: string;
  readonly from?: string;
  readonly to?: string;
  readonly incidentId?: string;
  readonly runtime?: string;
  readonly image?: string;
  readonly expectDigest?: string;
  readonly unsafeLocal?: boolean;
  readonly ledgerFile?: string;
  readonly maxStates?: number;
  readonly outputDirectory?: string;
  readonly reviewPort?: number;
  readonly witnessStore?: string;
  readonly draftStore?: string;
  readonly waitForFreeze?: WaitForGuidedFreeze;
  readonly onPhase?: (phase: GuidedInvestigatePhase, detail?: string) => void;
  readonly onReviewReady?: (url: string) => void;
  readonly signal?: AbortSignal;
}): Promise<GuidedInvestigateResult> {
  const repository = resolve(options.repository);
  const unsafeLocal = options.unsafeLocal === true;
  const draftStore = resolve(options.draftStore ?? defaultIncidentDraftStore(repository));
  const witnessStore = resolve(options.witnessStore ?? join(repository, ".faultline", "witnesses"));
  const waitForFreeze = options.waitForFreeze ?? defaultWaitForGuidedFreeze;

  options.onPhase?.("PREFLIGHT");
  const doctor = await runFaultLineDoctor({ repository });
  const proofReady = doctor.dockerInvestigationPreflight === "READY";
  if (doctorCliExitCode(doctor) !== 0) {
    return {
      status: "PREFLIGHT_BLOCKED",
      doctor,
      proofReady,
      draft: null,
      draftPath: null,
      reviewUrl: null,
      retainedFrozenDigest: null,
      continuation: null,
      nextCommands: ["Install Node.js 22+", "fl doctor --repo ."],
      note: "Local CLI preflight failed; fix Node before continuing the guided investigate flow."
    };
  }

  let draft: IncidentDraft;
  let draftPath: string;
  if (options.resumeId !== undefined) {
    options.onPhase?.("INTAKE", `resume ${options.resumeId}`);
    const stored = readIncidentDraft(draftStore, options.resumeId);
    if (resolve(stored.draft.repository) !== repository) {
      throw new Error("Guided --resume refuses a --repo value that differs from the immutable incident draft repository.");
    }
    draft = stored.draft;
    draftPath = stored.path;
  } else {
    if (options.ciLogPath === undefined) {
      throw new Error("Guided investigate requires --ci-log <file> or --resume <incident-id>.");
    }
    options.onPhase?.("INTAKE", "ci-log");
    const created = await createGuidedDraft({
      repository,
      ciLogPath: options.ciLogPath,
      ...(options.command === undefined ? {} : { command: options.command }),
      ...(options.from === undefined ? {} : { from: options.from }),
      ...(options.to === undefined ? {} : { to: options.to }),
      ...(options.incidentId === undefined ? {} : { incidentId: options.incidentId }),
      ...(options.runtime === undefined ? {} : { runtimeAlias: options.runtime }),
      witnessStore,
      draftStore
    });
    draft = created.draft;
    draftPath = created.draftPath;
  }

  options.onPhase?.("WITNESS_REVIEW");
  const priorFreeze = verifyFrozenWitness(
    witnessStore,
    draft.draftId,
    options.expectDigest
  );
  let freeze: GuidedFreezeWaitResult;
  if (priorFreeze.valid && priorFreeze.frozenDigest !== null) {
    // Already frozen: bind the retained digest without reopening review.
    freeze = {
      frozenDigest: options.expectDigest ?? priorFreeze.frozenDigest,
      reviewUrl: null,
      alreadyFrozen: true
    };
  } else if (priorFreeze.errors.some((error) => /missing|not been frozen/i.test(error))) {
    freeze = await waitForFreeze({
      proposalId: draft.draftId,
      witnessStore,
      draftStore,
      ...(options.reviewPort === undefined ? {} : { port: options.reviewPort }),
      ...(options.onReviewReady === undefined ? {} : { onReviewReady: options.onReviewReady }),
      ...(options.signal === undefined ? {} : { signal: options.signal })
    });
  } else {
    throw new Error(
      `Guided investigate cannot continue with an invalid frozen witness: ${priorFreeze.errors.join("; ")}`
    );
  }

  // Session-retained digest: observed at freeze (or supplied via --expect-digest).
  // Continuation always verifies against this value rather than trusting the store alone.
  const retainedFrozenDigest = freeze.frozenDigest;
  if (options.expectDigest !== undefined && options.expectDigest !== retainedFrozenDigest) {
    throw new Error("--expect-digest does not match the frozen witness observed by the guided flow.");
  }
  const retainedCheck = verifyFrozenWitness(witnessStore, draft.draftId, retainedFrozenDigest);
  if (!retainedCheck.valid || retainedCheck.externalDigestStatus !== "MATCH") {
    throw new Error(
      `Guided investigate failed to bind a retained freeze digest: ${retainedCheck.errors.join("; ") || retainedCheck.externalDigestStatus}`
    );
  }

  options.onPhase?.("RUNTIME_SELECTION");
  const image = await resolveGuidedRuntimeImage({
    draft,
    ...(options.image === undefined ? {} : { image: options.image }),
    ...(options.runtime === undefined ? {} : { runtimeAlias: options.runtime }),
    unsafeLocal
  });

  if (!unsafeLocal && !proofReady) {
    throw new Error(
      "Proof-grade guided investigate requires a READY Docker investigation preflight. Run `fl doctor --repo .`, start Docker, or pass --unsafe-local for non-proof diagnosis only."
    );
  }

  options.onPhase?.("LOCALIZATION");
  const continuation = await runFrozenIncidentContinuation({
    draft,
    repository,
    witnessStore,
    expectedFrozenDigest: retainedFrozenDigest,
    ...(image === undefined ? {} : { image }),
    ...(unsafeLocal ? { unsafeLocal: true } : {}),
    ...(options.ledgerFile === undefined ? {} : { ledgerFile: options.ledgerFile }),
    ...(options.maxStates === undefined ? {} : { maxStates: options.maxStates }),
    ...(options.outputDirectory === undefined ? {} : { outputDirectory: options.outputDirectory })
  });

  if (continuation.status === "PROOF_BUNDLE_READY") {
    options.onPhase?.("PROOF_EXPORT");
  }

  const nextCommands = continuation.status === "PROOF_BUNDLE_READY"
    ? continuation.next
    : [
      ...continuation.next,
      `fl investigate --resume ${draft.draftId} --repo ${repository} --expect-digest ${retainedFrozenDigest}${image === undefined ? "" : ` --image ${image}`}`
    ];

  return {
    status: continuation.status === "PROOF_BUNDLE_READY"
      ? "GUIDED_PROOF_BUNDLE_READY"
      : "GUIDED_INVESTIGATION_NOT_PROOF",
    doctor,
    proofReady,
    draft,
    draftPath,
    reviewUrl: freeze.reviewUrl,
    retainedFrozenDigest,
    continuation,
    nextCommands,
    note: continuation.status === "PROOF_BUNDLE_READY"
      ? "Guided investigate completed intake → human freeze → runtime selection → localization → proof export as one resumable command. FaultLine did not auto-approve or auto-freeze."
      : "Guided investigate reached localization but did not establish a portable proof package. Fix the environment or witness, then resume with a new reviewed draft rather than altering the frozen record."
  };
}
