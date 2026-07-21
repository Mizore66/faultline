import {
  chmodSync,
  existsSync,
  mkdtempSync,
  readFileSync,
  unlinkSync,
  writeFileSync
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { captureGitCleanCheckpoint } from "../src/ledger.js";

function run(command: string, args: readonly string[]): string {
  const result = spawnSync(command, [...args], {
    encoding: "utf8",
    shell: false,
    windowsHide: true,
    timeout: 30_000
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed\n${result.stdout ?? ""}${result.stderr ?? ""}`);
  }
  return result.stdout ?? "";
}

describe("FL-SEC-001 hardened Git checkpoint capture", () => {
  it("does not execute repository-local fsmonitor during captureGitCleanCheckpoint", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-regression-fsmonitor-"));
    const sentinel = join(root, "faultline-security-sentinel");
    try {
      unlinkSync(sentinel);
    } catch {
      /* absent */
    }

    const repo = join(root, "repo");
    run("git", ["init", "-q", repo]);
    run("git", ["-C", repo, "config", "user.email", "audit@example.test"]);
    run("git", ["-C", repo, "config", "user.name", "Audit"]);
    run("git", ["-C", repo, "checkout", "-q", "-b", "main"]);
    writeFileSync(join(repo, "tracked.txt"), "ok\n", "utf8");
    run("git", ["-C", repo, "add", "tracked.txt"]);
    run("git", ["-C", repo, "commit", "-qm", "init"]);

    const monitor = join(root, process.platform === "win32" ? "fsmonitor.cmd" : "fsmonitor.sh");
    if (process.platform === "win32") {
      writeFileSync(
        monitor,
        `@echo off\r\necho fsmonitor-fired> "${sentinel}"\r\nexit /b 0\r\n`,
        "utf8"
      );
    } else {
      writeFileSync(monitor, `#!/bin/sh\nprintf fsmonitor-fired > "${sentinel}"\nexit 0\n`, "utf8");
      chmodSync(monitor, 0o755);
    }
    // Set via unhardened git config write only — never run status/diff with this
    // monitor live through plain Git; some Git builds hang waiting for the
    // fsmonitor protocol handshake and would stall the whole suite.
    run("git", ["-C", repo, "config", "core.fsmonitor", monitor]);

    const checkpoint = captureGitCleanCheckpoint(repo);
    expect(checkpoint.clean).toBe(true);
    expect(existsSync(sentinel) ? readFileSync(sentinel, "utf8") : null).toBe(null);
  });

  it("also keeps fl record checkpoint free of repository-local fsmonitor", () => {
    const workspace = process.cwd();
    const cli = join(workspace, "dist", "cli.js");
    if (!existsSync(cli)) return;

    const root = mkdtempSync(join(tmpdir(), "faultline-regression-fsmonitor-cli-"));
    const sentinel = join(root, "faultline-security-sentinel");
    const repo = join(root, "repo");
    run("git", ["init", "-q", repo]);
    run("git", ["-C", repo, "config", "user.email", "audit@example.test"]);
    run("git", ["-C", repo, "config", "user.name", "Audit"]);
    run("git", ["-C", repo, "checkout", "-q", "-b", "main"]);
    writeFileSync(join(repo, "tracked.txt"), "ok\n", "utf8");
    run("git", ["-C", repo, "add", "tracked.txt"]);
    run("git", ["-C", repo, "commit", "-qm", "init"]);

    const monitor = join(root, process.platform === "win32" ? "fsmonitor.cmd" : "fsmonitor.sh");
    if (process.platform === "win32") {
      writeFileSync(
        monitor,
        `@echo off\r\necho fsmonitor-fired> "${sentinel}"\r\nexit /b 0\r\n`,
        "utf8"
      );
    } else {
      writeFileSync(monitor, `#!/bin/sh\nprintf fsmonitor-fired > "${sentinel}"\nexit 0\n`, "utf8");
      chmodSync(monitor, 0o755);
    }
    run("git", ["-C", repo, "config", "core.fsmonitor", monitor]);

    const ledger = join(root, "ledger.json");
    run("node", [cli, "record", "init", "--session", "audit-session", "--repo", repo, "--ledger", ledger]);
    run("node", [cli, "record", "checkpoint", "--repo", repo, "--after-turn", "0", "--ledger", ledger]);

    expect(existsSync(sentinel) ? readFileSync(sentinel, "utf8") : null).toBe(null);
  });
});
