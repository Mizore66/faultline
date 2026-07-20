import { existsSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { ensureFaultLineConfig, type FaultLineConfig } from "./config.js";
import { detectLikelyRuntime, runFaultLineDoctor, type FaultLineDoctorReport } from "./doctor.js";
import {
  buildStandingApprovalPolicy,
  writeStandingApprovalPolicy
} from "./standing-approval-policy.js";

export type ProjectInitRuntime = "node" | "python" | "go";

export type StandingPolicyInitInput = {
  readonly policyId?: string;
  readonly frozenBy: string;
  readonly allowedCommands: readonly string[];
  readonly maxTimeoutSeconds?: number;
};

export type ProjectInitResult = {
  readonly repository: string;
  readonly doctor: FaultLineDoctorReport;
  readonly suggestedRuntime: ProjectInitRuntime | null;
  readonly sidecar: {
    readonly status: "SKIPPED" | "PREVIEW_REQUIRED" | "INSTALLED" | "ALREADY_PRESENT" | "CLI_REQUIRED";
    readonly hooksPath: string;
    readonly detail: string;
  };
  readonly ignoreFile: {
    readonly status: "SKIPPED" | "CREATED" | "ALREADY_PRESENT";
    readonly path: string;
  };
  readonly config: {
    readonly status: "SKIPPED" | "CREATED" | "UPDATED" | "UNCHANGED";
    readonly path: string;
    readonly value: FaultLineConfig | null;
  };
  readonly standingPolicy: {
    readonly status: "SKIPPED" | "CREATED" | "PROMPT_AVAILABLE";
    readonly policyId?: string;
    readonly policyDigest?: string;
    readonly detail: string;
  };
  /** Exactly one actionable next command for human-readable exits. */
  readonly next: string;
  readonly nextCommands: readonly string[];
  readonly limitations: readonly string[];
};

export const STARTER_FAULTLINEIGNORE = [
  "# Reviewed FaultLine turn-snapshot exclusions (edit carefully).",
  "# Prefer path exclusions over secret-scan bypasses.",
  "#",
  "# Do NOT ignore environment descriptors (lockfiles, package.json, go.mod,",
  "# Cargo.lock, Dockerfiles, …). FaultLine refuses to suppress them for",
  "# turn snapshots because they drive environment fingerprinting.",
  "#",
  "# Add only paths that must not enter the object database (local secrets,",
  "# huge generated artifacts). Example:",
  "#   scratch/",
  "#   local-secrets/",
  ""
].join("\n");

function mapDetectedRuntime(imageFamily: string | null): ProjectInitRuntime | null {
  if (imageFamily === "node" || imageFamily === "python") return imageFamily;
  if (imageFamily === "golang") return "go";
  return null;
}

/**
 * Compose doctor + optional sidecar scaffolding without pulling images,
 * freezing witnesses, or claiming proof readiness.
 */
export async function planProjectInit(options: {
  repository: string;
  runtime?: ProjectInitRuntime;
  cliPath?: string;
  writeIgnoreIfMissing?: boolean;
  /** When true, write/update `.faultline/config.json` even without other scaffolding. */
  writeConfig?: boolean;
  /** When true and hooks are absent, caller installs hooks separately and reports INSTALLED. */
  markSidecarInstalled?: boolean;
  /** YOLO on-ramp: freeze an exact-string standing approval policy into the witness store. */
  standingPolicy?: StandingPolicyInitInput;
}): Promise<ProjectInitResult> {
  const repository = resolve(options.repository);
  const doctor = await runFaultLineDoctor({ repository });
  const detected = detectLikelyRuntime(repository);
  const suggestedRuntime = options.runtime ?? mapDetectedRuntime(detected.imageFamily);

  const hooksPath = join(repository, ".codex", "hooks.json");
  let sidecar: ProjectInitResult["sidecar"];
  if (options.cliPath === undefined) {
    sidecar = {
      status: "CLI_REQUIRED",
      hooksPath,
      detail: "Pass --cli <built-cli.js> to preview/install Codex sidecar hooks."
    };
  } else if (options.markSidecarInstalled === true) {
    sidecar = {
      status: "INSTALLED",
      hooksPath,
      detail: "Project .codex/hooks.json was created. Review and trust hooks via Codex /hooks before recording."
    };
  } else if (existsSync(hooksPath)) {
    sidecar = {
      status: "ALREADY_PRESENT",
      hooksPath,
      detail: "Existing .codex/hooks.json will not be overwritten. Merge via `fl codex sidecar config` if needed."
    };
  } else {
    sidecar = {
      status: "PREVIEW_REQUIRED",
      hooksPath,
      detail: "Sidecar hooks are ready to install with explicit --yes (never auto-installed without confirmation)."
    };
  }

  const ignorePath = join(repository, ".faultlineignore");
  let ignoreFile: ProjectInitResult["ignoreFile"];
  if (existsSync(ignorePath)) {
    ignoreFile = { status: "ALREADY_PRESENT", path: ignorePath };
  } else if (options.writeIgnoreIfMissing === true) {
    writeFileSync(ignorePath, STARTER_FAULTLINEIGNORE, { encoding: "utf8", flag: "wx" });
    ignoreFile = { status: "CREATED", path: ignorePath };
  } else {
    ignoreFile = { status: "SKIPPED", path: ignorePath };
  }

  const runtime = suggestedRuntime ?? "node";
  const writeConfig = options.writeIgnoreIfMissing === true || options.markSidecarInstalled === true
    || options.writeConfig === true;
  let config: ProjectInitResult["config"];
  if (writeConfig) {
    const written = ensureFaultLineConfig({
      repository,
      ...(suggestedRuntime == null ? {} : { runtimeAlias: suggestedRuntime })
    });
    config = { status: written.status, path: written.path, value: written.config };
  } else if (existsSync(join(repository, ".faultline", "config.json"))) {
    const written = ensureFaultLineConfig({
      repository,
      ...(suggestedRuntime == null ? {} : { runtimeAlias: suggestedRuntime })
    });
    config = { status: written.status, path: written.path, value: written.config };
  } else {
    config = {
      status: "SKIPPED",
      path: join(repository, ".faultline", "config.json"),
      value: null
    };
  }

  let standingPolicy: ProjectInitResult["standingPolicy"];
  if (options.standingPolicy !== undefined && options.standingPolicy.allowedCommands.length > 0) {
    const witnessStore = config.value?.stores.witnesses
      ?? join(repository, ".faultline", "witnesses");
    const policy = writeStandingApprovalPolicy(witnessStore, buildStandingApprovalPolicy({
      schemaVersion: "faultline.standing-approval-policy.v1",
      policyId: options.standingPolicy.policyId ?? "init-default",
      frozenBy: options.standingPolicy.frozenBy,
      frozenAt: new Date().toISOString(),
      allowedCommands: [...options.standingPolicy.allowedCommands],
      allowedOverlayTemplates: [],
      maxTimeoutSeconds: options.standingPolicy.maxTimeoutSeconds ?? 120,
      network: "disabled",
      credentials: "redacted"
    }));
    standingPolicy = {
      status: "CREATED",
      policyId: policy.policyId,
      policyDigest: policy.policyDigest,
      detail: `Standing approval frozen for ${policy.allowedCommands.length} exact command(s). Novel commands still stop for review (COH-09/12).`
    };
  } else if (options.writeIgnoreIfMissing === true || options.writeConfig === true) {
    standingPolicy = {
      status: "PROMPT_AVAILABLE",
      detail: "Pass --standing-policy --standing-command <exact> --standing-frozen-by <you> to enable the YOLO allowlist on-ramp."
    };
  } else {
    standingPolicy = {
      status: "SKIPPED",
      detail: "Standing policy not requested. Use fl init --standing-policy … or fl witness policy freeze."
    };
  }

  const next = config.status === "SKIPPED"
    ? `fl init --repo ${repository} --yes`
    : sidecar.status === "PREVIEW_REQUIRED" && options.cliPath !== undefined
      ? `fl codex sidecar install --repo ${repository} --cli ${options.cliPath} --yes`
      : standingPolicy.status === "PROMPT_AVAILABLE"
        ? `fl init --repo ${repository} --yes --standing-policy --standing-command "<exact predicate>" --standing-frozen-by "<you>"`
        : `fl runtime prepare ${runtime} --yes`;

  const nextCommands = [next];

  return {
    repository,
    doctor,
    suggestedRuntime,
    sidecar,
    ignoreFile,
    config,
    standingPolicy,
    next,
    nextCommands,
    limitations: [
      "fl init does not pull Docker images, freeze witnesses, or create proof packages.",
      "Sidecar install still requires explicit --yes after human review.",
      "Standing policies match exact command strings only — CI-log suggestions never auto-match.",
      "Turn localization remains EXPERIMENTAL_TURN until promotion criteria are met."
    ]
  };
}
