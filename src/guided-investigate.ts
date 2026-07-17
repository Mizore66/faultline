import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { doctorCliExitCode, runFaultLineDoctor, type FaultLineDoctorReport } from "./doctor.js";
import { createIncidentDraft, type IncidentDraft } from "./incident.js";
import { defaultIncidentDraftStore, writeIncidentDraft } from "./incident-store.js";
import { proposeWitness } from "./witness-lock.js";

export type GuidedInvestigateResult = {
  status: "GUIDED_DRAFT_READY" | "PREFLIGHT_BLOCKED";
  doctor: FaultLineDoctorReport;
  proofReady: boolean;
  draft: IncidentDraft | null;
  draftPath: string | null;
  nextCommands: readonly string[];
  note: string;
};

function deriveSymptom(ciLog: string): string {
  const lines = ciLog.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  const failure = [...lines].reverse().find((line) => /fail|error|expected|assert/i.test(line));
  return (failure ?? lines.at(-1) ?? "CI reported a failure; review the attached log.").slice(0, 500);
}

function locallyObservedHead(repository: string): { head: string; parents: string[] } {
  const safe = [
    "-c", "core.hooksPath=/nonexistent/faultline-hooks",
    "-c", "core.fsmonitor=false",
    "-c", "core.useBuiltinFSMonitor=false",
    "-c", "core.untrackedCache=false",
    "-c", "filter.lfs.process=",
    "-c", "filter.lfs.smudge=",
    "-c", "filter.lfs.required=false"
  ];
  const head = spawnSync("git", ["-C", repository, ...safe, "rev-parse", "HEAD"], {
    encoding: "utf8",
    env: { PATH: process.env.PATH ?? "", COMSPEC: process.env.COMSPEC, GIT_TERMINAL_PROMPT: "0" }
  });
  if (head.status !== 0) throw new Error(head.stderr || "Unable to resolve local HEAD");
  const parents = spawnSync("git", ["-C", repository, ...safe, "rev-parse", "HEAD^"], {
    encoding: "utf8",
    env: { PATH: process.env.PATH ?? "", COMSPEC: process.env.COMSPEC, GIT_TERMINAL_PROMPT: "0" }
  });
  return {
    head: (head.stdout ?? "").trim(),
    parents: parents.status === 0 && (parents.stdout ?? "").trim() ? [(parents.stdout ?? "").trim()] : []
  };
}

/**
 * One guided entrypoint: preflight + review-only incident draft from a CI log.
 * Never approves, freezes, or executes the witness.
 */
export async function guidedInvestigateFromCiLog(options: {
  repository: string;
  ciLogPath: string;
  command?: string;
  from?: string;
  to?: string;
  incidentId?: string;
}): Promise<GuidedInvestigateResult> {
  const repository = resolve(options.repository);
  const doctor = await runFaultLineDoctor({ repository });
  const proofReady = doctor.dockerInvestigationPreflight === "READY";
  if (doctorCliExitCode(doctor) !== 0) {
    return {
      status: "PREFLIGHT_BLOCKED",
      doctor,
      proofReady,
      draft: null,
      draftPath: null,
      nextCommands: ["Install Node.js 22+", "fl doctor --repo ."],
      note: "Local CLI preflight failed; fix Node before continuing."
    };
  }

  const ciLog = readFileSync(resolve(options.ciLogPath), "utf8");
  const command = options.command?.trim()
    || "node -e \"console.log('Replace this command with the failing CI predicate after human review'); process.exit(1)\"";
  const draftId = options.incidentId ?? `guided-${Date.now().toString(36)}`;
  const localHead = locallyObservedHead(repository);
  const draftInput = {
    draftId,
    createdAt: new Date().toISOString(),
    repository,
    command,
    ...(options.from && options.to
      ? { range: { ancestor: options.from, descendant: options.to } }
      : { localHead })
  };

  const preliminary = createIncidentDraft(draftInput);
  const store = join(repository, ".faultline", "witnesses");
  const proposal = proposeWitness(store, {
    proposalId: preliminary.draftId,
    proposalOrigin: "HUMAN",
    incidentPacket: {
      symptom: deriveSymptom(ciLog),
      ciLog: ciLog.slice(0, 4_000),
      repositoryLanguage: "Unknown",
      repositorySummary: `Guided CI-log intake for local range ${preliminary.range.ancestor} → ${preliminary.range.descendant}. Review-only; nothing executed.`
    },
    witness: {
      behavior: "The human-supplied command must exit successfully at the selected immutable Git states.",
      command,
      overlays: [],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 300 }
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
  const stored = writeIncidentDraft(defaultIncidentDraftStore(repository), draft);
  const id = draft.draftId;
  return {
    status: "GUIDED_DRAFT_READY",
    doctor,
    proofReady,
    draft,
    draftPath: stored.path,
    nextCommands: [
      proofReady ? "Proof-grade Docker looks ready." : "fl doctor --proof-ready  # start Docker, then re-check",
      `fl witness review ${id}`,
      "# In the review UI: Approve, then Freeze. Retain the freeze digest outside .faultline.",
      `fl incident continue ${id} --expect-digest <retained-frozen-digest>`,
      "fl serve --bundle <proof> --expect-root <retained-root>"
    ],
    note: "Guided intake created a review-only draft from the CI log. FaultLine did not approve, freeze, or execute anything."
  };
}
