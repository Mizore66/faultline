import { basename, join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  DOCTOR_SCHEMA_VERSION,
  DOCTOR_SAFE_GIT_CONFIG,
  DOCTOR_SECURITY_SCHEMA_VERSION,
  detectLikelyRuntime,
  doctorCliExitCode,
  doctorSecurityExitCode,
  runFaultLineDoctor,
  runFaultLineSecurityDoctor,
  type DoctorCommand,
  type DoctorCommandResult,
  type DoctorCommandRunner,
  type DoctorFileProbe
} from "../src/doctor.js";

type ResponseMap = (command: DoctorCommand) => DoctorCommandResult | Promise<DoctorCommandResult>;

function response(overrides: Partial<DoctorCommandResult> = {}): DoctorCommandResult {
  return {
    exitCode: 0,
    stdout: "",
    stderr: "",
    timedOut: false,
    outputLimitExceeded: false,
    ...overrides
  };
}

function fakeRunner(respond: ResponseMap): { runner: DoctorCommandRunner; calls: DoctorCommand[] } {
  const calls: DoctorCommand[] = [];
  return {
    calls,
    runner: {
      async run(command) {
        calls.push(command);
        return respond(command);
      }
    }
  };
}

function markerProbe(markers: readonly string[]): DoctorFileProbe {
  return {
    exists(path) {
      return markers.includes(basename(path));
    }
  };
}

function readyResponse(repositoryRoot: string): ResponseMap {
  return (command) => {
    if (command.executable === "git" && command.arguments.includes("--version")) return response({ stdout: "git version 2.47.0\n" });
    if (command.executable === "node") return response({ stdout: "v22.17.0\n" });
    if (command.executable === "docker" && command.arguments[0] === "--version") return response({ stdout: "Docker version 27.3.1, build deadbeef\n" });
    if (command.executable === "git" && command.arguments.includes("rev-parse")) return response({ stdout: `${repositoryRoot}\n` });
    if (command.executable === "git" && command.arguments.includes("status")) return response({ stdout: "" });
    if (command.executable === "docker" && command.arguments[0] === "info") return response({ stdout: "27.3.1\n" });
    throw new Error(`Unexpected fixed doctor command: ${command.executable} ${command.arguments.join(" ")}`);
  };
}

function diagnostic(report: Awaited<ReturnType<typeof runFaultLineDoctor>>, id: string) {
  const item = report.diagnostics.find((candidate) => candidate.id === id);
  if (!item) throw new Error(`Missing diagnostic ${id}`);
  return item;
}

describe("FaultLine doctor", () => {
  it("uses only fixed read-only commands and returns a truthful ready preflight", async () => {
    const requestedRepository = resolve(".faultline-doctor-requested");
    const repositoryRoot = resolve(".faultline-doctor-root");
    const fake = fakeRunner(readyResponse(repositoryRoot));

    const report = await runFaultLineDoctor({
      repository: requestedRepository,
      runner: fake.runner,
      fileProbe: markerProbe(["package.json", "pnpm-lock.yaml"])
    });

    expect(report.schemaVersion).toBe(DOCTOR_SCHEMA_VERSION);
    expect(report.repository).toBe(requestedRepository);
    expect(report.repositoryRoot).toBe(repositoryRoot);
    expect(report.dockerInvestigationPreflight).toBe("READY");
    expect(report.imageSelection).toBe("NOT_CHECKED");
    expect(report.likelyRuntime).toEqual({
      kind: "NODE",
      imageFamily: "node",
      markers: ["package.json", "pnpm-lock.yaml"]
    });
    expect(report.limitations.join(" ")).toMatch(/not a proof/i);
    expect(diagnostic(report, "worktree")).toMatchObject({ status: "READY" });
    expect(diagnostic(report, "runtime")).toMatchObject({ status: "READY" });
    expect(DOCTOR_SAFE_GIT_CONFIG).toEqual([
      "-c", "core.hooksPath=/nonexistent/faultline-hooks",
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
    const gitCalls = fake.calls.filter((command) => command.executable === "git");
    expect(gitCalls).toHaveLength(3);
    expect(gitCalls.map((command) => command.arguments.slice(DOCTOR_SAFE_GIT_CONFIG.length))).toEqual([
      ["--version"],
      ["-C", requestedRepository, "rev-parse", "--show-toplevel"],
      ["-C", repositoryRoot, "status", "--porcelain=v1", "--untracked-files=all", "-z"]
    ]);
    expect(gitCalls.every((command) => command.arguments.slice(0, DOCTOR_SAFE_GIT_CONFIG.length).every(
      (argument, index) => argument === DOCTOR_SAFE_GIT_CONFIG[index]
    ))).toBe(true);
    expect(fake.calls.filter((command) => command.executable !== "git")).toEqual([
      { executable: "node", arguments: ["--version"], timeoutMs: 5_000 },
      { executable: "docker", arguments: ["--version"], timeoutMs: 5_000 },
      { executable: "docker", arguments: ["info", "--format", "{{.ServerVersion}}"], timeoutMs: 5_000 }
    ]);
  });

  it("flags a dirty worktree without printing changed path names or certifying proof", async () => {
    const repositoryRoot = resolve(".faultline-doctor-dirty");
    const fake = fakeRunner((command) => {
      const base = readyResponse(repositoryRoot)(command);
      if (command.executable === "git" && command.arguments.includes("status")) {
        return response({ stdout: " M secret-file.txt\0?? generated-output.log\0" });
      }
      return base;
    });

    const report = await runFaultLineDoctor({
      repository: repositoryRoot,
      runner: fake.runner,
      fileProbe: markerProbe(["pyproject.toml"])
    });

    expect(report.dockerInvestigationPreflight).toBe("ACTION_REQUIRED");
    expect(diagnostic(report, "worktree")).toMatchObject({
      status: "ACTION_REQUIRED",
      observation: "2 changed paths reported by Git."
    });
    expect(JSON.stringify(diagnostic(report, "worktree"))).not.toContain("secret-file.txt");
    expect(report.likelyRuntime).toMatchObject({ kind: "PYTHON", imageFamily: "python" });
  });

  it("reports Git-dependent checks as inapplicable when Git cannot start", async () => {
    const repository = resolve(".faultline-doctor-no-git");
    const fake = fakeRunner((command) => {
      if (command.executable === "git") return response({ exitCode: null });
      if (command.executable === "node") return response({ stdout: "v22.0.0\n" });
      if (command.executable === "docker" && command.arguments[0] === "--version") return response({ stdout: "Docker version 27.0.0\n" });
      if (command.executable === "docker") return response({ stdout: "27.0.0\n" });
      throw new Error("Unexpected command");
    });

    const report = await runFaultLineDoctor({ repository, runner: fake.runner, fileProbe: markerProbe([]) });

    expect(diagnostic(report, "git")).toMatchObject({ status: "UNAVAILABLE" });
    expect(diagnostic(report, "repository")).toMatchObject({ status: "INAPPLICABLE" });
    expect(diagnostic(report, "worktree")).toMatchObject({ status: "INAPPLICABLE" });
    expect(report.dockerInvestigationPreflight).toBe("UNAVAILABLE");
    expect(fake.calls.some((command) => command.arguments.includes("rev-parse"))).toBe(false);
    expect(fake.calls.some((command) => command.arguments.includes("status"))).toBe(false);
  });

  it("does not query a Docker daemon after an unavailable Docker CLI", async () => {
    const repositoryRoot = resolve(".faultline-doctor-no-docker");
    const fake = fakeRunner((command) => {
      if (command.executable === "docker") return response({ exitCode: null });
      return readyResponse(repositoryRoot)(command);
    });

    const report = await runFaultLineDoctor({ repository: repositoryRoot, runner: fake.runner, fileProbe: markerProbe(["go.mod"]) });

    expect(diagnostic(report, "docker-cli")).toMatchObject({ status: "UNAVAILABLE" });
    expect(diagnostic(report, "docker-daemon")).toMatchObject({ status: "INAPPLICABLE" });
    expect(report.dockerInvestigationPreflight).toBe("UNAVAILABLE");
    expect(doctorCliExitCode(report)).toBe(0);
    expect(fake.calls.some((command) => command.executable === "docker" && command.arguments[0] === "info")).toBe(false);
  });

  it("requires Node 22+ and preserves ambiguous runtime detection as a suggestion only", async () => {
    const repositoryRoot = resolve(".faultline-doctor-old-node");
    const fake = fakeRunner((command) => {
      if (command.executable === "node") return response({ stdout: "v20.18.0\n" });
      return readyResponse(repositoryRoot)(command);
    });

    const report = await runFaultLineDoctor({
      repository: repositoryRoot,
      runner: fake.runner,
      fileProbe: markerProbe(["package.json", "pyproject.toml"])
    });

    expect(diagnostic(report, "node")).toMatchObject({ status: "ACTION_REQUIRED" });
    expect(diagnostic(report, "runtime")).toMatchObject({ status: "INAPPLICABLE" });
    expect(report.likelyRuntime).toEqual({
      kind: "MULTIPLE",
      markers: ["package.json", "pyproject.toml"],
      imageFamily: null
    });
    expect(report.dockerInvestigationPreflight).toBe("ACTION_REQUIRED");
    expect(doctorCliExitCode(report)).toBe(1);
  });

  it("keeps unknown marker detection inapplicable instead of inventing an image", () => {
    const runtime = detectLikelyRuntime(join(process.cwd(), ".faultline-doctor-unknown"), markerProbe([]));
    expect(runtime).toEqual({ kind: "UNKNOWN", markers: [], imageFamily: null });
  });

  it("fails a diagnostic safely when a bounded runner reports excessive output", async () => {
    const repositoryRoot = resolve(".faultline-doctor-output-limit");
    const fake = fakeRunner((command) => {
      if (command.executable === "node") return response({ exitCode: null, outputLimitExceeded: true });
      return readyResponse(repositoryRoot)(command);
    });

    const report = await runFaultLineDoctor({ repository: repositoryRoot, runner: fake.runner, fileProbe: markerProbe([]) });
    expect(diagnostic(report, "node")).toMatchObject({
      status: "UNAVAILABLE",
      observation: expect.stringMatching(/output limit/)
    });
    expect(report.dockerInvestigationPreflight).toBe("UNAVAILABLE");
    expect(doctorCliExitCode(report)).toBe(1);
  });

  it("treats a ready Node install as a successful CLI exit even when proof-grade Docker is unavailable", async () => {
    const repositoryRoot = resolve(".faultline-doctor-cli-exit");
    const fake = fakeRunner((command) => {
      if (command.executable === "docker") return response({ exitCode: null });
      return readyResponse(repositoryRoot)(command);
    });
    const report = await runFaultLineDoctor({ repository: repositoryRoot, runner: fake.runner, fileProbe: markerProbe(["package.json"]) });
    expect(report.dockerInvestigationPreflight).toBe("UNAVAILABLE");
    expect(doctorCliExitCode(report)).toBe(0);
  });

  it("live --security self-test blocks malicious hooks and remote protocols", async () => {
    const report = await runFaultLineSecurityDoctor();
    expect(report.schemaVersion).toBe(DOCTOR_SECURITY_SCHEMA_VERSION);
    expect(report.status).toBe("SECURE");
    expect(doctorSecurityExitCode(report)).toBe(0);
    expect(report.checks.map((check) => check.id).sort()).toEqual([
      "hooks-neutralization",
      "overlay-path-traversal",
      "path-filters",
      "protocol-allow-never",
      "secret-shaped-blob"
    ].sort());
    expect(report.checks).toHaveLength(5);
    expect(report.checks.every((check) => check.status === "PASS")).toBe(true);
  });
});
