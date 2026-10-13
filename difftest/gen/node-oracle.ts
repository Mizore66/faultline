// Long-running oracle over frozen TS. One JSON request per stdin line,
// one JSON response per stdout line. Run: pnpm exec tsx difftest/gen/node-oracle.ts
import { createInterface } from "node:readline";
import nodePath from "node:path";
import { spawn } from "node:child_process";
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
import { makeWitness, resignWitness } from "./witness-seed.js";
import { redactText, redactValue } from "../../src/redaction.js";
import { classifyFromWitnessResult, resolveWitnessResultFromStdout, witnessResultConsistentWithExitCode } from "../../src/witness-result.js";

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

type Handler = (args: any) => unknown; // may return a Promise
export const handlers: Record<string, Handler> = {
  echo: (args) => args,
  makeWitness: ({ paths }: { paths: string[] }) => makeWitness(paths),
  resignWitness: ({ text }: { text: string }) => resignWitness(text),
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
  lowerMany: ({ texts }: { texts: string[] }) => texts.map((s) => s.toLocaleLowerCase("en-US")),
  normalizeMany: ({ texts, form }: { texts: string[]; form: "NFC" | "NFD" }) => texts.map((s) => s.normalize(form)),
  compareMany: ({ pairs }: { pairs: [string, string][] }) => pairs.map(([a, b]) => Math.sign(a.localeCompare(b))),
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
  // child_process.spawn as the hardened Git and sandbox runners call it:
  // records the error/exit/close events in order, the code and signal the
  // call sites settle on (the first of 'error' and 'close'), the arguments
  // of 'close', and both outputs; optionally kills after the first stdout
  // chunk.
  spawnProbe: ({ file, args, cwd, env, stdin, stdout, stderr, kill }: {
    file: string; args: string[]; cwd?: string; env?: Record<string, string>; stdin?: string;
    stdout: "pipe" | "ignore"; stderr: "pipe" | "ignore"; kill?: NodeJS.Signals;
  }) => new Promise((resolveProbe) => {
    const events: string[] = [];
    let out = "";
    let err = "";
    let settled: { code: number | null; signal: string | null; error: string | null } | undefined;
    const child = spawn(file, args, {
      ...(cwd === undefined ? {} : { cwd }),
      ...(env === undefined ? {} : { env }),
      shell: false,
      windowsHide: true,
      stdio: [stdin === undefined ? "ignore" : "pipe", stdout, stderr]
    });
    child.stdout?.on("data", (chunk: Buffer) => {
      out += chunk.toString("latin1");
      if (kill !== undefined && events.length === 0 && !child.killed) child.kill(kill);
    });
    child.stderr?.on("data", (chunk: Buffer) => { err += chunk.toString("latin1"); });
    child.on("error", (error) => {
      events.push("error");
      settled ??= { code: null, signal: null, error: error.message };
    });
    child.on("exit", () => events.push("exit"));
    child.on("close", (code, signal) => {
      events.push("close");
      settled ??= { code, signal, error: null };
      resolveProbe({ events, ...settled, close: [code, signal], stdout: out, stderr: err });
    });
    if (stdin !== undefined) {
      child.stdin?.once("error", () => {});
      child.stdin?.end(Buffer.from(stdin, "latin1"));
    }
  }),
  resolveWitnessResultFromStdout: ({ stdout }: { stdout: string }) => canonicalJson(resolveWitnessResultFromStdout(stdout)),
  classifyFromWitnessResult: ({ outcome }: { outcome: any }) => canonicalJson(classifyFromWitnessResult(outcome)),
  witnessResultConsistentWithExitCode: ({ outcome, exitCode }: { outcome: any; exitCode: number }) => canonicalJson(witnessResultConsistentWithExitCode(outcome, exitCode)),
  // [...text.matchAll(re)] (the g flag is added when missing); undefined
  // groups come back as null.
  jsreExec: ({ source, flags, text }: { source: string; flags: string; text: string }) => {
    const re = new RegExp(source, flags.includes("g") ? flags : `${flags}g`);
    return [...text.matchAll(re)].map((m) => ({ start: m.index, end: m.index + m[0].length, groups: m.slice(1) }));
  },
  redactText: ({ text, location }: { text: string; location: string }) => {
    const result = redactText(text, location);
    return { value: result.value, report: canonicalJson(result.report) };
  },
  redactValue: ({ text }: { text: string }) => {
    const result = redactValue(JSON.parse(text));
    return { value: JSON.stringify(result.value), report: canonicalJson(result.report) };
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
rl.on("line", async (line) => {
  let id: unknown = null;
  try {
    const request = JSON.parse(line) as { id: unknown; op: string; args: unknown };
    id = request.id;
    const handler = handlers[request.op];
    if (!handler) throw new Error(`unknown op: ${request.op}`);
    // The client sends one request at a time, so awaiting keeps responses in order.
    const result = await handler(request.args);
    process.stdout.write(`${JSON.stringify({ id, ok: true, result })}\n`);
  } catch (error) {
    process.stdout.write(`${JSON.stringify({ id, ok: false, error: error instanceof Error ? error.message : String(error) })}\n`);
  }
});
