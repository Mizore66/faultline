// Long-running oracle over frozen TS. One JSON request per stdin line,
// one JSON response per stdout line. Run: pnpm exec tsx difftest/gen/node-oracle.ts
import { createInterface } from "node:readline";
import nodePath from "node:path";
import { canonicalJson, digestJson } from "../../src/canonical.js";
import { buildZod } from "./schema-dsl.js";
import { PreventionProofBodySchema, PreventionProofManifestSchema, PreventionRepairedRunsArtifactSchema } from "../../src/prevention-proof.js";
import {
  GitCommitStateSchema, GitInvestigationResultSchema, GitInvestigationRunFactSchema, StableGitStateSchema, StableGitTransitionSchema
} from "../../src/git-investigation.js";
import { GitProofBundleManifestSchema, GitProofSourceMetadataSchema } from "../../src/git-proof-bundle.js";
import { FrozenWitnessSchema, verifyFrozenWitnessRecord } from "../../src/witness-lock.js";
import { EnvironmentFingerprintSchema } from "../../src/environment-fingerprint.js";
import { MaterializedOverlaySchema } from "../../src/safe-overlay.js";
import { CodexLifecycleLedgerSchema, verifyCodexLifecycleLedger } from "../../src/ledger.js";
import { validateSandboxPlanAudit } from "../../src/sandbox.js";
import { resignLedger, richLedger } from "./ledger-seed.js";
import { resignSandbox, sandboxVariant } from "./sandbox-seed.js";

export const namedSchemas: Record<string, { safeParse(v: unknown): any }> = {
  PreventionProofBodySchema,
  PreventionProofManifestSchema,
  PreventionRepairedRunsArtifactSchema,
  GitCommitStateSchema,
  GitInvestigationRunFactSchema,
  StableGitStateSchema,
  StableGitTransitionSchema,
  GitInvestigationResultSchema,
  EnvironmentFingerprintSchema,
  MaterializedOverlaySchema,
  FrozenWitnessSchema,
  GitProofSourceMetadataSchema,
  GitProofBundleManifestSchema,
  CodexLifecycleLedgerSchema,
};

type Handler = (args: any) => unknown;
export const handlers: Record<string, Handler> = {
  echo: (args) => args,
  parse: ({ text }: { text: string }) => {
    try {
      const value = JSON.parse(text);
      return { ok: true, compact: JSON.stringify(value), pretty: JSON.stringify(value, null, 2) };
    } catch (error) {
      return { ok: false, message: (error as Error).message };
    }
  },
  formatNumber: ({ hex }: { hex: string }) => {
    const view = new DataView(new ArrayBuffer(8));
    view.setBigUint64(0, BigInt(`0x${hex}`));
    return String(view.getFloat64(0));
  },
  sort: ({ keys }: { keys: string[] }) => [...keys].sort((left, right) => left.localeCompare(right)),
  canonical: ({ text }: { text: string }) => {
    try {
      const value = JSON.parse(text);
      return { ok: true, canonical: canonicalJson(value), digest: digestJson(value) };
    } catch (error) {
      return { ok: false, message: (error as Error).message };
    }
  },
  decodeUtf8: ({ hex }: { hex: string }) => Buffer.from(hex, "hex").toString("utf8"),
  zodDsl: ({ schema, text }: { schema: unknown; text: string }) => {
    const result = buildZod(schema).safeParse(JSON.parse(text));
    if (!result.success) return { success: false, message: result.error.message };
    try {
      return { success: true, canonical: canonicalJson(result.data) };
    } catch (error) {
      return { success: true, canonical: `ERROR:${(error as Error).message}` };
    }
  },
  zodNamed: ({ name, text }: { name: string; text: string }) => {
    const result = namedSchemas[name]!.safeParse(JSON.parse(text));
    if (!result.success) return { success: false, message: result.error.message };
    try {
      return { success: true, canonical: canonicalJson(result.data) };
    } catch (error) {
      return { success: true, canonical: `ERROR:${(error as Error).message}` };
    }
  },
  verifyFrozenWitnessRecord: ({ text, expected }: { text: string; expected?: string }) =>
    canonicalJson(verifyFrozenWitnessRecord(JSON.parse(text), expected)),
  verifyCodexLifecycleLedger: ({ text }: { text: string }) => canonicalJson(verifyCodexLifecycleLedger(JSON.parse(text))),
  // Verify runs the run-fact schema before the audit check, so the oracle does too.
  validateSandboxPlanAudit: ({ text }: { text: string }) => {
    const parsed = GitInvestigationRunFactSchema.shape.sandbox.safeParse(JSON.parse(text));
    if (!parsed.success) return `SCHEMA:${parsed.error.message}`;
    try {
      return canonicalJson(validateSandboxPlanAudit(parsed.data));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  richLedger: () => JSON.stringify(richLedger()),
  resignLedger: ({ text }: { text: string }) => resignLedger(text),
  resignSandbox: ({ text, legacy }: { text: string; legacy: boolean }) => resignSandbox(text, legacy),
  sandboxVariant: ({ text, variant }: { text: string; variant: "legacy" | "unsafe-local" | "environment" }) => sandboxVariant(text, variant),
  path: ({ platform, fn, args, cwd, env }: { platform: "posix" | "win32"; fn: string; args: string[]; cwd: string; env: Record<string, string> }) => {
    const savedCwd = process.cwd;
    const savedEnv = process.env;
    process.cwd = () => cwd;
    process.env = env;
    try {
      return (nodePath[platform] as unknown as Record<string, (...a: string[]) => unknown>)[fn]!(...args);
    } finally {
      process.cwd = savedCwd;
      process.env = savedEnv;
    }
  },
  sortCodePoints: () => {
    const all: string[] = [];
    for (let c = 0; c <= 0x10ffff; c++) all.push(c >= 0xd800 && c <= 0xdfff ? String.fromCharCode(c) : String.fromCodePoint(c));
    all.sort((left, right) => left.localeCompare(right));
    let ties = "";
    for (let i = 1; i < all.length; i++) ties += all[i - 1]!.localeCompare(all[i]!) === 0 ? "1" : "0";
    return { order: all.map((s) => s.codePointAt(0)), ties };
  },
};

const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on("line", (line) => {
  let id: unknown = null;
  try {
    const request = JSON.parse(line) as { id: unknown; op: string; args: unknown };
    id = request.id;
    const handler = handlers[request.op];
    if (!handler) throw new Error(`unknown op: ${request.op}`);
    process.stdout.write(`${JSON.stringify({ id, ok: true, result: handler(request.args) })}\n`);
  } catch (error) {
    process.stdout.write(`${JSON.stringify({ id, ok: false, error: error instanceof Error ? error.message : String(error) })}\n`);
  }
});
