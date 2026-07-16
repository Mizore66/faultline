import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { join, resolve } from "node:path";

/**
 * Read-only, local preflight diagnostics for the common Git + Docker path.
 * A READY result only says that this narrow prerequisite was observed. It
 * never attests to a witness, a container image, an execution, or a proof.
 */
export const DOCTOR_SCHEMA_VERSION = "faultline.doctor.v1" as const;
export const MINIMUM_NODE_MAJOR = 22;
export const DOCTOR_COMMAND_TIMEOUT_MS = 5_000;
export const DOCTOR_MAX_OUTPUT_BYTES = 64 * 1024;

/**
 * Repository configuration is executable input at this pre-Docker boundary.
 * Keep the doctor aligned with the hardened Git inspection path: every Git
 * invocation receives these fixed overrides before it touches a worktree.
 */
export const DOCTOR_SAFE_GIT_CONFIG = Object.freeze([
  "-c", "core.hooksPath=/nonexistent/faultline-hooks",
  // core.fsmonitor can hold a repository-local command.
  "-c", "core.fsmonitor=false",
  "-c", "core.useBuiltinFSMonitor=false",
  "-c", "core.untrackedCache=false",
  "-c", "core.preloadIndex=false",
  "-c", "core.autocrlf=false",
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
]);

export type DoctorCommandExecutable = "git" | "node" | "docker";

/**
 * The doctor constructs every executable and flag itself. Callers only select
 * a repository directory; no user-provided command text is ever interpreted.
 */
export type DoctorCommand = {
  executable: DoctorCommandExecutable;
  arguments: readonly string[];
  timeoutMs: number;
};

export type DoctorCommandResult = {
  exitCode: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
  outputLimitExceeded: boolean;
};

/** Injectable so tests and product hosts can run the same fixed diagnostics safely. */
export type DoctorCommandRunner = {
  run(command: DoctorCommand): Promise<DoctorCommandResult>;
};

/** A deliberately tiny filesystem seam for deterministic runtime-marker tests. */
export type DoctorFileProbe = {
  exists(path: string): boolean;
};

export type DoctorDiagnosticStatus = "READY" | "ACTION_REQUIRED" | "UNAVAILABLE" | "INAPPLICABLE";

export type DoctorCheckId =
  | "git"
  | "repository"
  | "worktree"
  | "node"
  | "docker-cli"
  | "docker-daemon"
  | "runtime";

export type DoctorDiagnostic = {
  id: DoctorCheckId;
  status: DoctorDiagnosticStatus;
  summary: string;
  observation: string | null;
  remediation: string | null;
};

export type LikelyRuntimeKind = "NODE" | "PYTHON" | "GO" | "MULTIPLE" | "UNKNOWN";
export type RuntimeImageFamily = "node" | "python" | "golang";

/**
 * This is intentionally a marker-based suggestion, not a package inspection
 * or a claim that an image can execute the repository's witness.
 */
export type LikelyRuntime = {
  kind: LikelyRuntimeKind;
  markers: readonly string[];
  imageFamily: RuntimeImageFamily | null;
};

export type DockerInvestigationPreflight = "READY" | "ACTION_REQUIRED" | "UNAVAILABLE" | "INAPPLICABLE";

export type FaultLineDoctorReport = {
  schemaVersion: typeof DOCTOR_SCHEMA_VERSION;
  repository: string;
  repositoryRoot: string | null;
  diagnostics: readonly DoctorDiagnostic[];
  likelyRuntime: LikelyRuntime;
  /** Doctor deliberately does not inspect or select an image. */
  imageSelection: "NOT_CHECKED";
  dockerInvestigationPreflight: DockerInvestigationPreflight;
  limitations: readonly string[];
};

export type FaultLineDoctorOptions = {
  repository?: string;
  runner?: DoctorCommandRunner;
  fileProbe?: DoctorFileProbe;
};

const defaultFileProbe: DoctorFileProbe = { exists: existsSync };

const runtimeMarkers: ReadonlyArray<{
  kind: Exclude<LikelyRuntimeKind, "MULTIPLE" | "UNKNOWN">;
  imageFamily: RuntimeImageFamily;
  files: readonly string[];
}> = [
  {
    kind: "NODE",
    imageFamily: "node",
    files: ["package.json", "pnpm-lock.yaml", "package-lock.json", "yarn.lock", "bun.lock", "bun.lockb"]
  },
  {
    kind: "PYTHON",
    imageFamily: "python",
    files: ["pyproject.toml", "requirements.txt", "poetry.lock", "Pipfile", "setup.py"]
  },
  {
    kind: "GO",
    imageFamily: "golang",
    files: ["go.mod"]
  }
];

function fixedCommand(executable: DoctorCommandExecutable, argv: readonly string[]): DoctorCommand {
  return Object.freeze({
    executable,
    arguments: Object.freeze([...argv]),
    timeoutMs: DOCTOR_COMMAND_TIMEOUT_MS
  });
}

function hardenedGitCommand(argv: readonly string[]): DoctorCommand {
  return fixedCommand("git", [...DOCTOR_SAFE_GIT_CONFIG, ...argv]);
}

function unavailableObservation(result: DoctorCommandResult): string {
  if (result.outputLimitExceeded) return `The command exceeded FaultLine's ${DOCTOR_MAX_OUTPUT_BYTES}-byte diagnostic output limit.`;
  if (result.timedOut) return "The command timed out before returning a result.";
  if (result.exitCode === null) return "The command could not be started or did not return an exit code.";
  return `The command exited with status ${result.exitCode}.`;
}

function outputLine(value: string): string | null {
  const line = value.replace(/[\r\n]+/g, " ").trim();
  return line ? line.slice(0, 160) : null;
}

function commandSucceeded(result: DoctorCommandResult): boolean {
  return !result.timedOut && !result.outputLimitExceeded && result.exitCode === 0;
}

async function runCommand(runner: DoctorCommandRunner, command: DoctorCommand): Promise<DoctorCommandResult> {
  try {
    return await runner.run(command);
  } catch {
    return {
      exitCode: null,
      stdout: "",
      stderr: "",
      timedOut: false,
      outputLimitExceeded: false
    };
  }
}

function gitDiagnostic(result: DoctorCommandResult): DoctorDiagnostic {
  const version = outputLine(result.stdout);
  if (!commandSucceeded(result)) {
    return {
      id: "git",
      status: "UNAVAILABLE",
      summary: "Git is unavailable to FaultLine.",
      observation: unavailableObservation(result),
      remediation: "Install Git and make it available on PATH, then run fl doctor again."
    };
  }
  if (version === null) {
    return {
      id: "git",
      status: "ACTION_REQUIRED",
      summary: "Git returned no recognizable version output.",
      observation: null,
      remediation: "Repair the Git installation or PATH entry, then run fl doctor again."
    };
  }
  return {
    id: "git",
    status: "READY",
    summary: "Git is available.",
    observation: version,
    remediation: null
  };
}

function nodeDiagnostic(result: DoctorCommandResult): DoctorDiagnostic {
  if (!commandSucceeded(result)) {
    return {
      id: "node",
      status: "UNAVAILABLE",
      summary: "Node.js is unavailable to FaultLine.",
      observation: unavailableObservation(result),
      remediation: `Install Node.js ${MINIMUM_NODE_MAJOR} or newer and make it available on PATH, then run fl doctor again.`
    };
  }
  const version = outputLine(result.stdout);
  const match = version === null ? null : /^v(\d+)(?:\.\d+){0,2}(?:\S*)?$/.exec(version);
  if (match === null) {
    return {
      id: "node",
      status: "ACTION_REQUIRED",
      summary: "Node.js returned an unrecognizable version.",
      observation: version,
      remediation: `Use a supported Node.js ${MINIMUM_NODE_MAJOR}+ installation, then run fl doctor again.`
    };
  }
  const major = Number(match[1]);
  if (major < MINIMUM_NODE_MAJOR) {
    return {
      id: "node",
      status: "ACTION_REQUIRED",
      summary: `Node.js ${version} is below FaultLine's required major version.`,
      observation: version,
      remediation: `Switch to Node.js ${MINIMUM_NODE_MAJOR} or newer, then run fl doctor again.`
    };
  }
  return {
    id: "node",
    status: "READY",
    summary: "A supported Node.js runtime is available.",
    observation: version,
    remediation: null
  };
}

function dockerCliDiagnostic(result: DoctorCommandResult): DoctorDiagnostic {
  const version = outputLine(result.stdout);
  if (!commandSucceeded(result)) {
    return {
      id: "docker-cli",
      status: "UNAVAILABLE",
      summary: "The Docker CLI is unavailable to FaultLine.",
      observation: unavailableObservation(result),
      remediation: "Install Docker Desktop or Docker Engine and make the docker command available on PATH."
    };
  }
  if (version === null) {
    return {
      id: "docker-cli",
      status: "ACTION_REQUIRED",
      summary: "The Docker CLI returned no recognizable version output.",
      observation: null,
      remediation: "Repair the Docker CLI installation, then run fl doctor again."
    };
  }
  return {
    id: "docker-cli",
    status: "READY",
    summary: "The Docker CLI is available.",
    observation: version,
    remediation: null
  };
}

function dockerDaemonDiagnostic(result: DoctorCommandResult): DoctorDiagnostic {
  const version = outputLine(result.stdout);
  if (!commandSucceeded(result) || version === null) {
    return {
      id: "docker-daemon",
      status: "UNAVAILABLE",
      summary: "The Docker daemon is not reachable by FaultLine.",
      observation: commandSucceeded(result) ? "Docker returned no server version." : unavailableObservation(result),
      remediation: "Start Docker Desktop or Docker Engine and ensure the current user can access its daemon, then run fl doctor again."
    };
  }
  return {
    id: "docker-daemon",
    status: "READY",
    summary: "The Docker daemon is reachable.",
    observation: version,
    remediation: null
  };
}

function inapplicableDiagnostic(
  id: DoctorCheckId,
  summary: string,
  remediation: string | null
): DoctorDiagnostic {
  return { id, status: "INAPPLICABLE", summary, observation: null, remediation };
}

function safeExists(fileProbe: DoctorFileProbe, path: string): boolean {
  try {
    return fileProbe.exists(path);
  } catch {
    return false;
  }
}

/**
 * Suggest a runtime family only from conventional root markers. We avoid
 * parsing manifests, resolving dependencies, pulling images, or assuming that
 * a suggested family can run the witness.
 */
export function detectLikelyRuntime(repository: string, fileProbe: DoctorFileProbe = defaultFileProbe): LikelyRuntime {
  const found = runtimeMarkers.map((candidate) => ({
    ...candidate,
    markers: candidate.files.filter((file) => safeExists(fileProbe, join(repository, file)))
  })).filter((candidate) => candidate.markers.length > 0);

  if (found.length === 0) return { kind: "UNKNOWN", markers: [], imageFamily: null };
  if (found.length > 1) {
    return {
      kind: "MULTIPLE",
      markers: found.flatMap((candidate) => candidate.markers),
      imageFamily: null
    };
  }
  const candidate = found[0];
  if (candidate === undefined) return { kind: "UNKNOWN", markers: [], imageFamily: null };
  return {
    kind: candidate.kind,
    markers: candidate.markers,
    imageFamily: candidate.imageFamily
  };
}

function runtimeDiagnostic(runtime: LikelyRuntime): DoctorDiagnostic {
  if (runtime.kind === "UNKNOWN") {
    return inapplicableDiagnostic(
      "runtime",
      "FaultLine could not infer a likely runtime family from conventional root markers.",
      "Choose an explicit digest-pinned image and witness command; automatic image selection is deliberately not attempted."
    );
  }
  if (runtime.kind === "MULTIPLE") {
    return {
      id: "runtime",
      status: "INAPPLICABLE",
      summary: "Multiple likely runtime families were detected; FaultLine will not choose an image automatically.",
      observation: runtime.markers.join(", "),
      remediation: "Choose an explicit digest-pinned image and witness command for the failing behavior."
    };
  }
  return {
    id: "runtime",
    status: "READY",
    summary: `Likely ${runtime.kind} repository detected from conventional root markers.`,
    observation: runtime.markers.join(", "),
    remediation: "This is only a suggestion. Select and pin an image digest that matches the witness before investigating."
  };
}

function preflightStatus(diagnostics: readonly DoctorDiagnostic[]): DockerInvestigationPreflight {
  const required = new Set<DoctorCheckId>(["git", "repository", "worktree", "node", "docker-cli", "docker-daemon"]);
  const statuses = diagnostics.filter((diagnostic) => required.has(diagnostic.id)).map((diagnostic) => diagnostic.status);
  if (statuses.every((status) => status === "READY")) return "READY";
  if (statuses.includes("UNAVAILABLE")) return "UNAVAILABLE";
  if (statuses.includes("ACTION_REQUIRED")) return "ACTION_REQUIRED";
  return "INAPPLICABLE";
}

/**
 * Run the fixed, read-only preflight. This does not invoke a witness, pull an
 * image, access the network, write to the repository, or certify any proof.
 */
export async function runFaultLineDoctor(options: FaultLineDoctorOptions = {}): Promise<FaultLineDoctorReport> {
  const repository = resolve(options.repository ?? process.cwd());
  const runner = options.runner ?? createNodeDoctorRunner();
  const fileProbe = options.fileProbe ?? defaultFileProbe;

  const [gitResult, nodeResult, dockerCliResult] = await Promise.all([
    runCommand(runner, hardenedGitCommand(["--version"])),
    runCommand(runner, fixedCommand("node", ["--version"])),
    runCommand(runner, fixedCommand("docker", ["--version"]))
  ]);
  const git = gitDiagnostic(gitResult);
  const node = nodeDiagnostic(nodeResult);
  const dockerCli = dockerCliDiagnostic(dockerCliResult);

  let repositoryRoot: string | null = null;
  let repositoryDiagnostic: DoctorDiagnostic;
  let worktreeDiagnostic: DoctorDiagnostic;
  if (git.status !== "READY") {
    repositoryDiagnostic = inapplicableDiagnostic(
      "repository",
      "Repository detection was not attempted because Git is unavailable.",
      "Make Git available, then run fl doctor again."
    );
    worktreeDiagnostic = inapplicableDiagnostic(
      "worktree",
      "Worktree cleanliness was not checked because repository detection is unavailable.",
      "Make Git available and run fl doctor from a repository."
    );
  } else {
    const repositoryResult = await runCommand(runner, hardenedGitCommand(["-C", repository, "rev-parse", "--show-toplevel"]));
    const reportedRoot = outputLine(repositoryResult.stdout);
    if (!commandSucceeded(repositoryResult) || reportedRoot === null) {
      repositoryDiagnostic = {
        id: "repository",
        status: "ACTION_REQUIRED",
        summary: "No Git repository was detected at the requested directory or one of its parents.",
        observation: commandSucceeded(repositoryResult) ? "Git returned no repository root." : unavailableObservation(repositoryResult),
        remediation: "Run fl doctor with --repo pointing at a Git worktree, then run it again."
      };
      worktreeDiagnostic = inapplicableDiagnostic(
        "worktree",
        "Worktree cleanliness was not checked because no Git repository was detected.",
        "Run fl doctor from a Git worktree."
      );
    } else {
      repositoryRoot = resolve(reportedRoot);
      repositoryDiagnostic = {
        id: "repository",
        status: "READY",
        summary: "A Git repository was detected.",
        observation: repositoryRoot,
        remediation: null
      };
      const statusResult = await runCommand(runner, hardenedGitCommand([
        "-C", repositoryRoot, "status", "--porcelain=v1", "--untracked-files=all", "-z"
      ]));
      if (!commandSucceeded(statusResult)) {
        worktreeDiagnostic = {
          id: "worktree",
          status: "UNAVAILABLE",
          summary: "FaultLine could not read the Git worktree status.",
          observation: unavailableObservation(statusResult),
          remediation: "Fix Git access to this worktree, then run fl doctor again."
        };
      } else {
        const changes = statusResult.stdout.split("\0").filter(Boolean).length;
        worktreeDiagnostic = changes === 0
          ? {
            id: "worktree",
            status: "READY",
            summary: "The Git worktree is clean.",
            observation: "No tracked or untracked changes reported by Git.",
            remediation: null
          }
          : {
            id: "worktree",
            status: "ACTION_REQUIRED",
            summary: "The Git worktree has changes that would make a clean checkpoint ambiguous.",
            observation: `${changes} changed path${changes === 1 ? "" : "s"} reported by Git.`,
            remediation: "Commit or stash the changes, or model them explicitly before recording evidence."
          };
      }
    }
  }

  let dockerDaemon: DoctorDiagnostic;
  if (dockerCli.status !== "READY") {
    dockerDaemon = inapplicableDiagnostic(
      "docker-daemon",
      "Docker daemon reachability was not checked because the Docker CLI is unavailable.",
      "Install or repair the Docker CLI first, then run fl doctor again."
    );
  } else {
    const daemonResult = await runCommand(runner, fixedCommand("docker", ["info", "--format", "{{.ServerVersion}}"]));
    dockerDaemon = dockerDaemonDiagnostic(daemonResult);
  }

  const runtime = detectLikelyRuntime(repositoryRoot ?? repository, fileProbe);
  const diagnostics = [
    git,
    repositoryDiagnostic,
    worktreeDiagnostic,
    node,
    dockerCli,
    dockerDaemon,
    runtimeDiagnostic(runtime)
  ];

  return {
    schemaVersion: DOCTOR_SCHEMA_VERSION,
    repository,
    repositoryRoot,
    diagnostics,
    likelyRuntime: runtime,
    imageSelection: "NOT_CHECKED",
    dockerInvestigationPreflight: preflightStatus(diagnostics),
    limitations: [
      "This is a local prerequisite check, not a proof of behavior or provenance.",
      "FaultLine did not pull or inspect a container image, execute a witness, access the network, or write to the repository.",
      "Likely runtime detection uses root-file markers only; choose and digest-pin the actual image explicitly."
    ]
  };
}

/**
 * Production runner for the fixed commands above. It invokes no shell and
 * bounds each command with a short timeout; callers can inject a runner for
 * tests or another host environment.
 */
export function createNodeDoctorRunner(): DoctorCommandRunner {
  return {
    run(command: DoctorCommand): Promise<DoctorCommandResult> {
      return new Promise((resolveResult) => {
        let settled = false;
        let timedOut = false;
        let outputLimitExceeded = false;
        let stdout = "";
        let stderr = "";
        let timeout: NodeJS.Timeout | undefined;

        const finish = (exitCode: number | null): void => {
          if (settled) return;
          settled = true;
          if (timeout !== undefined) clearTimeout(timeout);
          resolveResult({ exitCode, stdout, stderr, timedOut, outputLimitExceeded });
        };

        let child;
        try {
          child = spawn(command.executable, [...command.arguments], {
            shell: false,
            windowsHide: true,
            stdio: ["ignore", "pipe", "pipe"]
          });
        } catch (error) {
          stderr = error instanceof Error ? error.message : "FaultLine could not start the diagnostic command.";
          finish(null);
          return;
        }

        const appendOutput = (stream: "stdout" | "stderr", chunk: Buffer): void => {
          const bytes = Buffer.from(chunk);
          const used = Buffer.byteLength(stdout, "utf8") + Buffer.byteLength(stderr, "utf8");
          const remaining = DOCTOR_MAX_OUTPUT_BYTES - used;
          if (remaining <= 0) {
            outputLimitExceeded = true;
            child.kill("SIGKILL");
            return;
          }
          const retained = bytes.subarray(0, remaining).toString("utf8");
          if (stream === "stdout") stdout += retained;
          else stderr += retained;
          if (bytes.length > remaining) {
            outputLimitExceeded = true;
            child.kill("SIGKILL");
          }
        };
        child.stdout?.on("data", (chunk: Buffer) => appendOutput("stdout", chunk));
        child.stderr?.on("data", (chunk: Buffer) => appendOutput("stderr", chunk));
        child.once("error", (error) => {
          stderr += error.message;
          finish(null);
        });
        child.once("close", (exitCode) => finish(exitCode));
        timeout = setTimeout(() => {
          timedOut = true;
          child.kill("SIGKILL");
        }, command.timeoutMs);
      });
    }
  };
}
