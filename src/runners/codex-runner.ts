import { spawnSync, type SpawnSyncOptions, type SpawnSyncReturns } from "node:child_process";
import { existsSync } from "node:fs";
import { delimiter, isAbsolute } from "node:path";

export const FAULTLINE_CODEX_BIN_ENV = "FAULTLINE_CODEX_BIN" as const;

export type CodexRunnerResolution = {
  readonly bin: string;
  readonly version: string;
  readonly source: "ENV" | "PATH";
};

export class CodexRunnerError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CodexRunnerError";
  }
}

function pathEntries(): string[] {
  return (process.env.PATH ?? "").split(delimiter).filter(Boolean);
}

function candidateNames(): readonly string[] {
  return process.platform === "win32" ? ["codex.cmd", "codex.exe", "codex"] : ["codex"];
}

function resolveOnPath(): string | undefined {
  for (const directory of pathEntries()) {
    for (const name of candidateNames()) {
      const candidate = `${directory.replace(/[\\/]+$/, "")}${directory.includes("\\") ? "\\" : "/"}${name}`;
      if (existsSync(candidate)) return candidate;
    }
  }
  // Last resort: bare name (spawn may still resolve via PATH / PATHEXT).
  return process.platform === "win32" ? "codex.cmd" : "codex";
}

function probeVersion(bin: string): string {
  const result = spawnSync(bin, ["--version"], {
    encoding: "utf8",
    shell: false,
    windowsHide: true,
    env: {
      PATH: process.env.PATH ?? "",
      ...(process.platform === "win32" && process.env.ComSpec ? { ComSpec: process.env.ComSpec } : {}),
      ...(process.platform === "win32" && process.env.SystemRoot ? { SystemRoot: process.env.SystemRoot } : {})
    }
  });
  if (result.error) {
    throw new CodexRunnerError(
      `Codex CLI probe failed for ${bin}: ${result.error.message}. Set ${FAULTLINE_CODEX_BIN_ENV} or install Codex on PATH.`
    );
  }
  if (result.status !== 0) {
    const detail = `${result.stderr ?? ""}${result.stdout ?? ""}`.trim();
    throw new CodexRunnerError(
      `Codex CLI --version failed for ${bin}${detail ? `: ${detail}` : ""}. Set ${FAULTLINE_CODEX_BIN_ENV} or install Codex on PATH.`
    );
  }
  const version = (result.stdout ?? "").trim().split(/\r?\n/, 1)[0] ?? "";
  if (!version) {
    throw new CodexRunnerError(`Codex CLI --version returned empty output for ${bin}.`);
  }
  return version;
}

/**
 * Resolve a local Codex CLI binary: FAULTLINE_CODEX_BIN override, else PATH,
 * then require a successful cheap `--version` probe. Discovery never implies
 * permission to run — callers still need `--with-codex` and `--allow-codex-full-auto`.
 */
export function resolveCodexRunner(): CodexRunnerResolution {
  const override = process.env[FAULTLINE_CODEX_BIN_ENV]?.trim();
  if (override) {
    if (isAbsolute(override) && !existsSync(override)) {
      throw new CodexRunnerError(`${FAULTLINE_CODEX_BIN_ENV} points to a missing binary: ${override}`);
    }
    const version = probeVersion(override);
    return { bin: override, version, source: "ENV" };
  }
  const bin = resolveOnPath();
  if (bin === undefined) {
    throw new CodexRunnerError(
      `Codex CLI was not found on PATH. Install Codex or set ${FAULTLINE_CODEX_BIN_ENV}.`
    );
  }
  const version = probeVersion(bin);
  return { bin, version, source: "PATH" };
}

export function spawnCodex(
  args: readonly string[],
  options: SpawnSyncOptions = {}
): SpawnSyncReturns<string | Buffer> {
  const resolved = resolveCodexRunner();
  return spawnSync(resolved.bin, [...args], {
    encoding: "utf8",
    shell: false,
    windowsHide: true,
    ...options,
    env: {
      PATH: process.env.PATH ?? "",
      ...(process.platform === "win32" && process.env.ComSpec ? { ComSpec: process.env.ComSpec } : {}),
      ...(process.platform === "win32" && process.env.SystemRoot ? { SystemRoot: process.env.SystemRoot } : {}),
      ...(options.env ?? {})
    }
  });
}
