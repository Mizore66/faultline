import { describe, expect, it } from "vitest";
import {
  CodexRunnerError,
  FAULTLINE_CODEX_BIN_ENV,
  resolveCodexRunner
} from "../src/runners/codex-runner.js";

describe("codex runner resolution (CDX-04)", () => {
  it("fails closed when FAULTLINE_CODEX_BIN points at a missing absolute path", () => {
    const previous = process.env[FAULTLINE_CODEX_BIN_ENV];
    process.env[FAULTLINE_CODEX_BIN_ENV] = process.platform === "win32"
      ? "C:\\nonexistent\\faultline-codex-missing.exe"
      : "/nonexistent/faultline-codex-missing";
    try {
      expect(() => resolveCodexRunner()).toThrow(CodexRunnerError);
    } finally {
      if (previous === undefined) delete process.env[FAULTLINE_CODEX_BIN_ENV];
      else process.env[FAULTLINE_CODEX_BIN_ENV] = previous;
    }
  });

  it("probes FAULTLINE_CODEX_BIN when it is a working executable", () => {
    const previous = process.env[FAULTLINE_CODEX_BIN_ENV];
    process.env[FAULTLINE_CODEX_BIN_ENV] = process.execPath;
    try {
      const resolved = resolveCodexRunner();
      expect(resolved.source).toBe("ENV");
      expect(resolved.bin).toBe(process.execPath);
      expect(resolved.version.length).toBeGreaterThan(0);
    } finally {
      if (previous === undefined) delete process.env[FAULTLINE_CODEX_BIN_ENV];
      else process.env[FAULTLINE_CODEX_BIN_ENV] = previous;
    }
  });
});
