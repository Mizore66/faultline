import { existsSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { detectLikelyRuntime, runFaultLineDoctor, type FaultLineDoctorReport } from "./doctor.js";

export type ProjectInitRuntime = "node" | "python" | "go";

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
  /** When true and hooks are absent, caller installs hooks separately and reports INSTALLED. */
  markSidecarInstalled?: boolean;
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
  const nextCommands = [
    `fl doctor --repo ${repository}`,
    `fl runtime prepare ${runtime}`,
    `fl runtime prepare ${runtime} --yes`,
    ...(options.cliPath === undefined
      ? ["pnpm build  # in FaultLine checkout", "fl init --repo . --cli <FaultLine>/dist/cli.js --yes"]
      : sidecar.status === "PREVIEW_REQUIRED"
        ? [
          `fl codex sidecar install --repo ${repository} --cli ${options.cliPath}`,
          `fl codex sidecar install --repo ${repository} --cli ${options.cliPath} --yes`
        ]
        : [`fl codex sidecar status --repo ${repository}`]),
    "Open Codex in this repo and trust hooks via /hooks",
    "Freeze an immutable overlay witness that imports production code",
    "fl investigate turns --repo . --latest --proposal <id> --expect-digest sha256:… --image <digest-pinned>"
  ];

  return {
    repository,
    doctor,
    suggestedRuntime,
    sidecar,
    ignoreFile,
    nextCommands,
    limitations: [
      "fl init does not pull Docker images, freeze witnesses, or create proof packages.",
      "Sidecar install still requires explicit --yes after human review.",
      "Turn localization remains EXPERIMENTAL_TURN until promotion criteria are met."
    ]
  };
}
