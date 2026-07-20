import { readFileSync } from "node:fs";

export const usage = `FaultLine — freeze one reviewed witness; prove only what executions support

Quickstart (judges / first look):
  fl doctor [--proof-ready] [--security]
  fl judge-proof [--export-only]          # verified COMMIT_PROOF sample (no Docker / no API key)
  fl quickstart                          # print the recommended next commands
  fl ui [--repo <directory>] [--port <number>]
  fl --version

Common product commands:
  fl init [--yes]
  fl investigate --ci-log <file> --repo <directory> [...]
  fl investigate git ...
  fl investigate turns ...
  fl verify <proof-bundle-directory> [--expect-root <sha256:...>]
  fl witness propose|review|approve|freeze ...
  fl repair [--with-codex] | fl repair brief --live ...
  fl prevention write|verify ...

More:
  fl advanced                            # full command reference
  fl help advanced

Notes:
  fl doctor exits 0 for local CLI readiness; fl doctor --proof-ready exits nonzero unless Docker proof-grade preflight is READY.
  fl doctor --security runs a live hardened-Git + fixture self-test (≥5 PASS: hooks / protocol.allow=never / filters / overlay traversal / secret blob).
  Headless judges: FAULTLINE_NO_BROWSER=1 pnpm fl judge-proof --export-only
  GPT-5.6 samples (no key): docs/samples/gpt-5.6/
  The judge demo/fixture paths do not require an OpenAI API key.`;

export const advancedUsage = `FaultLine — full command reference

Usage:
  fl judge-demo [--replay | --rerun-all] [--output <managed-bundle-directory>] [--export-only]
  fl judge-proof [--bundle <git-proof-bundle-directory>] [--expect-root <sha256:...>] [--export-only] [--port <number>]
  fl commit-proof-preview [--bundle <git-proof-bundle-directory>] [--expect-root <sha256:...>] [--output <static-preview.html>]
  fl --version
  fl judge-preview [--output <static-preview.html>]
  fl doctor [--repo <directory>] [--json] [--proof-ready] [--security]
  fl quickstart
  fl ui [--repo <directory>] [--port <number>]
  fl init [--repo <directory>] [--cli <built-cli.js>] [--runtime <node|python|go>] [--yes]
  fl incident suggest --repo <directory>
  fl incident start --repo <directory> (--command <failing-command> | --command-file <utf8-file>) [--id <safe-id>] [--from <commit> --to <commit>] [--runtime <node|python|go> | --image <digest-pinned-image>] [--store <directory>]
  fl incident status <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--expect-digest <sha256:...>]
  fl incident continue <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--image <digest-pinned-image>] [--expect-digest <sha256:...>] [--ledger <ledger.json>] [--max-states <count>] [--output <managed-bundle-directory>]
  fl investigate --ci-log <file> --repo <directory> [--command <failing-command>] [--from <commit> --to <commit>] [--runtime <node|python|go> | --image <digest-pinned-image>] [--id <safe-id>]
  fl investigate --resume <incident-id> --repo <directory> [--expect-digest <sha256:...>] [--runtime <node|python|go> | --image <digest-pinned-image>]
  fl tutorial [--repo <directory>] [--yes]
  fl investigate turns --repo <directory> (--ledger <ledger.json> | --latest) --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--runtime-mapping <mapping.json>] [--output <managed-bundle-directory>] [--minimize] [--transition <index>] [--max-executions <count>]
  fl prove transition <turn-proof-bundle-directory> --repo <directory> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--transition <index>] [--max-executions <count>] [--output <managed-result.json>]
  fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--runtime-mapping <mapping.json>] [--ledger <ledger.json>] [--output <managed-bundle-directory>]
  fl runtime mapping write --fingerprint <sha256:...> --image <digest-pinned-image> [--fingerprint <sha256:...> --image <digest-pinned-image> ...] --output <mapping.json>
  fl runtime resolve <node|python|go>
  fl runtime prepare <node|python|go> --yes
  fl runtime project <plan|build|resolve> [--context <directory>] [--dockerfile <file>] --tag <repository:tag> [--network <none|default>] [--yes]
  fl demo live-git|full [--image <digest-pinned-image>] [--export-only] [--port <number>]
  fl verify <proof-bundle-directory> [--expect-root <sha256:...>]
  fl serve [--port <number>]
  fl serve --bundle <git-proof-bundle-directory> [--expect-root <sha256:...>] [--minimization <result.json> --expect-minimization <sha256:...>] [--repair <repair-brief-directory> --expect-repair <sha256:...>] [--prevention <prevention-proof-directory> --expect-prevention <sha256:...>] [--port <number>]
  fl codex --dry-run | --snapshot [--repo <directory>]
  fl codex snapshot gc [--repo <directory>] [--force]
  fl codex record <init|stdin|checkpoint|verify> [...]
  fl codex sidecar config (--cli <built-cli.js> | --command <hook-command> [--command-windows <hook-command>])
  fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes
  fl codex sidecar hook [--input <hook.json>] [--quiet]
  fl codex sidecar status [--repo <directory>] [--session <session-id>]
  fl record <init|stdin|checkpoint|attach|verify> [...]
  fl minimize git --repo <directory> --before <commit> --after <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--max-executions <count>] [--output <managed-result.json>]
  fl minimize verify <result.json> [--expect-digest <sha256:...>]
  fl ledger bind --ledger <ledger.json> --investigation <investigation.json> --output <binding.json>
  fl ledger verify <binding.json> [--expect-digest <sha256:...>]
  fl attest create --bundle <proof-bundle-directory> --receipt <id> --subject <label> --issuer <label> [--store <directory>]
  fl attest verify <receipt-id> [--expect-digest <sha256:...>] [--store <directory>]
  fl provenance create --bundle <git-proof-bundle-directory> [--output <managed-receipt.json>]
  fl provenance verify --bundle <git-proof-bundle-directory> --receipt <ci-receipt.json> --attestation-bundle <sigstore-bundle.json> --trust <trust.json>
  fl repair --bundle <git-proof-bundle-directory> --expect-root <sha256:...> [--repo <directory>] [--output <directory>] [--with-codex] [--instructions-only] [--keep-worktree]
  fl repair brief (--bundle <git-proof-bundle-directory> | --investigation <verified-bundle>/investigation.json) (--live | --input <repair-brief.json>) [--expect-root <sha256:...>] [--model <model>] [--output <managed-directory>]
  fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]
  fl prevention write --input <prevention-input.json> [--output <managed-directory>]
  fl prevention write --from-bundle <git-proof-bundle> --expect-root <sha256:...> --patch <repair.patch> --repaired-commit <id> --repaired-tree <id> --repaired-runs <runs.json> [--output <managed-directory>] [--codex-thread-id <id>]
  fl prevention verify <prevention-proof-directory> [--expect-root <sha256:...>]
  fl witness propose --input <proposal.json> [--store <directory>]
  fl witness propose --live --incident <incident.json> --proposal-id <id> --overlay-root <directory> [--model <model>] [--store <directory>]
  fl witness implement --incident <incident.json> --proposal-id <id> --overlay-out <directory> [--repo <directory>] [--with-codex] [--behavior <text>]
  fl witness review <proposal-id> [--json | --port <number>] [--store <directory>] [--draft-store <directory>]
  fl witness approve <proposal-id> --approved-by <actor> [--store <directory>]
  fl witness freeze <proposal-id> [--repo <directory>] [--draft-store <directory>] [--store <directory>]
  fl witness sign <proposal-id> --private-key <ed25519-private.pem> --keyring <trusted-reviewers.json> [--store <directory>]
  fl witness verify <proposal-id> [--expect-digest <sha256:...>] [--keyring <trusted-reviewers.json> --require-signature] [--store <directory>]

The judge demo is a reviewed, deterministic Node fixture. It does not require an OpenAI API key.
The lifecycle adapter accepts observed Codex-compatible events; it does not claim to intercept private Codex internals.
Evidence outputs are intentionally confined to their managed .faultline roots; --output selects a child of that root rather than an arbitrary directory.
Use --live for a GPT-5.6 witness proposal or an inferred repair brief after setting OPENAI_API_KEY.
fl doctor exits 0 when Node can run the local CLI; fl doctor --proof-ready exits nonzero unless Docker proof-grade preflight is READY.`;

export function faultLineVersion(): string {
  try {
    const packageJson = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")) as {
      version?: unknown;
    };
    return typeof packageJson.version === "string" ? packageJson.version : "unknown";
  } catch {
    return "unknown";
  }
}

export function printQuickstart(): void {
  process.stdout.write(`FaultLine quickstart

1) Local readiness (Docker-less judges stay on the sample path):
   fl doctor
   fl doctor --proof-ready

2) Verified COMMIT_PROOF sample (no Docker, no API key):
   fl judge-proof --export-only
   fl judge-proof

3) Inspect GPT-5.6 response shapes without a key:
   docs/samples/gpt-5.6/

4) Live paths (optional):
   OPENAI_API_KEY=… fl witness propose --live …
   OPENAI_API_KEY=… fl repair brief --live …
   Docker required: fl demo full   # or: fl demo live-git --export-only

Pinned submission checkout: git checkout v0.1.5-buildweek
Full command list: fl advanced
`);
}

/** Fast path for help / version / quickstart — no heavy CLI graph. */
export function tryFastCliPath(argv: readonly string[]): boolean {
  const [command, ...args] = argv;
  switch (command) {
    case undefined:
    case "help":
    case "--help":
    case "-h":
      if (args[0] === "advanced" || args.includes("--advanced")) {
        process.stdout.write(`${advancedUsage}\n`);
        return true;
      }
      process.stdout.write(`${usage}\n`);
      return true;
    case "advanced":
      process.stdout.write(`${advancedUsage}\n`);
      return true;
    case "quickstart":
      printQuickstart();
      return true;
    case "--version":
    case "-V":
      process.stdout.write(`FaultLine ${faultLineVersion()}\n`);
      return true;
    default:
      return false;
  }
}
