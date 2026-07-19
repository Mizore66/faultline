#!/usr/bin/env node
/**
 * Thin CLI entry: help/version/quickstart stay light; everything else lazy-loads cli-app.
 * Production entry is `node dist/cli.js` (see package.json scripts.fl). Use `pnpm fl:dev` for tsx.
 */
import { tryFastCliPath } from "./cli-help.js";

function formatCliFailure(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  const windowsHint = [
    "",
    "Windows tip: If PowerShell blocked pnpm due to ExecutionPolicy restrictions, run:",
    "  Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope Process",
    "Or invoke the command proxy directly:",
    "  pnpm.cmd fl <command> (e.g., pnpm.cmd fl judge-demo)"
  ].join("\n");

  if (/^Unknown command:\s*--\b/.test(message) || message.startsWith("Unknown command: --")) {
    return `FaultLine error: ${message}\nNote: Do not place '--' between 'fl' and your subcommand. Use 'pnpm fl <command>'.\nNext: pnpm fl help`;
  }

  if (message.startsWith("Unknown command:")) {
    return `FaultLine error: ${message}\nNext: pnpm fl help`;
  }

  if (/ExecutionPolicy|running scripts is disabled|PSSecurityException|UnauthorizedAccess/i.test(message)) {
    return [`FaultLine error: ${message}`, windowsHint, "Next: pnpm fl help"].join("\n");
  }

  return `FaultLine error: ${message}\nNext: pnpm fl help`;
}

async function main(): Promise<void> {
  const argv = process.argv.slice(2);
  if (tryFastCliPath(argv)) return;
  const { runCli } = await import("./cli-app.js");
  await runCli(argv);
}

main().catch((error: unknown) => {
  process.stderr.write(`${formatCliFailure(error)}\n`);
  process.exitCode = 1;
});
