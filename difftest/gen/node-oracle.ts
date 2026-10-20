// Long-running oracle over frozen TS. One JSON request per stdin line,
// one JSON response per stdout line. Run: pnpm exec tsx difftest/gen/node-oracle.ts
import { createInterface } from "node:readline";
import nodePath from "node:path";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { canonicalJson, digestJson } from "../../src/canonical.js";
import { buildZod } from "./schema-dsl.js";
import { PreventionProofBodySchema, PreventionProofManifestSchema, PreventionRepairedRunsArtifactSchema } from "../../src/prevention-proof.js";
import {
  GitCommitStateSchema, GitInvestigationResultSchema, GitInvestigationRunFactSchema, StableGitStateSchema, StableGitTransitionSchema,
  investigateGitRange
} from "../../src/git-investigation.js";
import type { GitInvestigationRequest } from "../../src/git-investigation.js";
import { minimizeGitDiff, verifyGitMinimizationResult } from "../../src/git-minimization.js";
import type { GitMinimizationRequest } from "../../src/git-minimization.js";
import { GitProofBundleManifestSchema, GitProofSourceMetadataSchema, writeGitInvestigationProofBundle } from "../../src/git-proof-bundle.js";
import type { GitProofBundleWriteOptions } from "../../src/git-proof-bundle.js";
import { FrozenWitnessSchema, verifyFrozenWitnessRecord } from "../../src/witness-lock.js";
import { ENVIRONMENT_DESCRIPTOR_FILES, EnvironmentFingerprintSchema, computeEnvironmentFingerprint, resolveRuntimeImageForFingerprint } from "../../src/environment-fingerprint.js";
import { loadRuntimeMappingDocument } from "../../src/runtime-mapping-file.js";
import { readFaultLineConfig } from "../../src/config.js";
import { MaterializedOverlaySchema, materializeFrozenOverlays } from "../../src/safe-overlay.js";
import { materializeGitTree, runHardenedGit } from "../../src/git-materialization.js";
import { CodexLifecycleLedgerSchema, verifyCodexLifecycleLedger } from "../../src/ledger.js";
import { ledgerBoundInvestigationDigest, verifyLedgerBoundInvestigation } from "../../src/ledger-binding.js";
import { classifySandboxResult, createSandboxPlan, executeSandboxPlan, validateSandboxPlanAudit } from "../../src/sandbox.js";
import type { SandboxCommandResult, SandboxPlan, SandboxPlanRequest } from "../../src/sandbox.js";
import { resignLedger, richLedger } from "./ledger-seed.js";
import { resignSandbox, sandboxVariant } from "./sandbox-seed.js";
import { makeWitness, resignWitness } from "./witness-seed.js";
import { lifecycleBoundToDescendant, lifecycleBoundToEveryInvestigatedState } from "./fixtures.js";
import { redactText, redactValue } from "../../src/redaction.js";
import { classifyFromWitnessResult, resolveWitnessResultFromStdout, witnessResultConsistentWithExitCode } from "../../src/witness-result.js";
import { allowlistConfigDigest, filterAllowlistedHighOccurrences, isEntropyTokenAllowlisted, loadSecretAllowlist } from "../../src/secret-allowlist.js";
import { planTurnSnapshotPaths } from "../../src/turn-snapshot.js";

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

// Git output as the hardened-git ops report it: its length and SHA-256, and
// the bytes themselves (latin1) up to 64 KiB.
const outputView = (bytes: Buffer) => ({
  length: bytes.length,
  sha256: createHash("sha256").update(bytes).digest("hex"),
  text: bytes.length <= 65536 ? bytes.toString("latin1") : null
});

// Runs fn with process.env.PATH replaced (the hardened Git environment
// copies the host PATH when it spawns).
async function withPath<T>(path: string | undefined, fn: () => Promise<T>): Promise<T> {
  if (path === undefined) return fn();
  const saved = process.env.PATH;
  process.env.PATH = path;
  try {
    return await fn();
  } finally {
    process.env.PATH = saved;
  }
}

// Trees from materializeGitTree, kept for materializeFrozenOverlays and
// cleanupGitTree.
const materializedTrees = new Map<number, Awaited<ReturnType<typeof materializeGitTree>>>();
let nextMaterializedTree = 0;
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
  verifyLedgerBoundInvestigation: ({ text, expected }: { text: string; expected?: string }) =>
    canonicalJson(verifyLedgerBoundInvestigation(JSON.parse(text), expected)),
  // Recomputes bindingDigest, so a mutated record reaches the checks behind it.
  resignBound: ({ text }: { text: string }) => {
    const value = JSON.parse(text);
    try {
      value.bindingDigest = ledgerBoundInvestigationDigest(value);
    } catch {
      return text;
    }
    return JSON.stringify(value);
  },
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
  // The image-configuration and fingerprint helpers: canonical JSON, or
  // "THREW:<message>" when TS threw.
  loadRuntimeMappingDocument: ({ text }: { text: string }) => {
    try {
      return canonicalJson(Object.entries(loadRuntimeMappingDocument(JSON.parse(text))));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  // The descriptor list itself, so the live test builds its directories
  // from the TS names rather than from the Go list it checks.
  environmentDescriptorFiles: () => [...ENVIRONMENT_DESCRIPTOR_FILES],
  computeEnvironmentFingerprint: ({ dir }: { dir: string }) => {
    try {
      return canonicalJson(computeEnvironmentFingerprint(dir));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  resolveRuntimeImageForFingerprint: ({ fingerprintDigest, homogeneity, mapping, defaultImage }: {
    fingerprintDigest: string; homogeneity: "HOMOGENEOUS" | "HETEROGENEOUS" | "EMPTY"; mapping: Record<string, string>; defaultImage?: string;
  }) => {
    try {
      return resolveRuntimeImageForFingerprint({
        fingerprintDigest, homogeneity, runtimeMapping: mapping, ...(defaultImage === undefined ? {} : { defaultImage })
      });
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  readFaultLineConfig: ({ dir }: { dir: string }) => {
    try {
      const config = readFaultLineConfig(dir);
      return config === null ? "NULL" : canonicalJson(config);
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
  // Buffer.from(s, "base64") for each string, as latin1.
  base64Lenient: ({ texts }: { texts: string[] }) => texts.map((s) => Buffer.from(s, "base64").toString("latin1")),
  runHardenedGit: async ({ repository, args, stdin, gitDir, worktree, indexFile, path }: {
    repository: string; args: string[]; stdin?: string; gitDir?: string; worktree?: string; indexFile?: string; path?: string;
  }) => {
    const result = await withPath(path, () => runHardenedGit(repository, args, {
      ...(stdin === undefined ? {} : { stdin: Buffer.from(stdin, "latin1") }),
      ...(gitDir === undefined ? {} : { gitDir }),
      ...(worktree === undefined ? {} : { worktree }),
      ...(indexFile === undefined ? {} : { indexFile })
    }));
    return { exitCode: result.exitCode, stdout: outputView(result.stdout), stderr: outputView(result.stderr), error: result.error ?? null };
  },
  // { id, worktree }, or "THREW:<message>".
  materializeGitTree: async ({ repository, commit, tempRoot, name, path }: {
    repository: string; commit: string; tempRoot: string; name: string; path?: string;
  }) => {
    try {
      const tree = await withPath(path, () => materializeGitTree({ repository, commit, tempRoot, name }));
      const id = nextMaterializedTree++;
      materializedTrees.set(id, tree);
      return { id, worktree: tree.worktree };
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  // Canonical JSON of the overlay facts, or "THREW:<message>"; with id the
  // tree's index refuses Git symlink targets.
  materializeFrozenOverlays: async ({ worktree, witness, id }: { worktree: string; witness: string; id?: number }) => {
    try {
      const index = id === undefined ? undefined : materializedTrees.get(id);
      return canonicalJson(await materializeFrozenOverlays(worktree, JSON.parse(witness), index));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  cleanupGitTree: async ({ id }: { id: number }) => {
    const tree = materializedTrees.get(id)!;
    materializedTrees.delete(id);
    return { result: await tree.cleanup() };
  },
  // JSON.stringify of the plan (key order kept), or "THREW:<message>".
  // Start the oracle seeded (oracle.StartSeeded) to compare container names.
  createSandboxPlan: ({ request }: { request: SandboxPlanRequest }) => {
    try {
      return JSON.stringify(createSandboxPlan(request));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  // classifySandboxResult needs only these plan fields.
  classifySandboxResult: ({ plan, result }: { plan: SandboxPlan; result: SandboxCommandResult }) =>
    JSON.stringify(classifySandboxResult(plan, result)),
  // truncateOutput is not exported: classifySandboxResult applies it to
  // both strings when outputLimitExceeded is true.
  truncateOutput: ({ stdout, stderr, max }: { stdout: string; stderr: string; max: number }) => {
    const plan = { kind: "DOCKER_ISOLATED", limits: { maxOutputBytes: max } } as unknown as SandboxPlan;
    const result = classifySandboxResult(plan, { exitCode: 0, stdout, stderr, outputLimitExceeded: true });
    return { stdout: result.stdout, stderr: result.stderr };
  },
  // createSandboxPlan then executeSandboxPlan with the native runner, with
  // PATH and FAULTLINE_ARGV_LOG (the docker recorder's log) replaced:
  // JSON.stringify({ arguments, result }), or "THREW:<message>".
  executeSandboxPlan: async ({ request, path, argvLog }: { request: SandboxPlanRequest; path: string; argvLog: string }) => {
    const saved = process.env.FAULTLINE_ARGV_LOG;
    process.env.FAULTLINE_ARGV_LOG = argvLog;
    try {
      return await withPath(path, async () => {
        let plan;
        try {
          plan = createSandboxPlan(request);
        } catch (error) {
          return `THREW:${(error as Error).message}`;
        }
        return JSON.stringify({ arguments: plan.arguments, result: await executeSandboxPlan(plan) });
      });
    } finally {
      if (saved === undefined) delete process.env.FAULTLINE_ARGV_LOG;
      else process.env.FAULTLINE_ARGV_LOG = saved;
    }
  },
  // Date.now() minus the seeded epoch: under seed.mjs, how many clock ticks
  // the oracle had used (tsx's startup takes some), plus this read.
  seededTick: () => Date.now() - 1784203200000,
  // investigateGitRange with the native runner, with PATH and
  // FAULTLINE_ARGV_LOG (the git and docker recorders' log) replaced:
  // JSON.stringify of the result (key order kept), or "THREW:<message>".
  // Start the oracle seeded (oracle.StartSeeded) to compare nonces and times.
  investigateGitRange: async ({ request, path, argvLog }: { request: GitInvestigationRequest; path: string; argvLog: string }) => {
    const saved = process.env.FAULTLINE_ARGV_LOG;
    process.env.FAULTLINE_ARGV_LOG = argvLog;
    try {
      return await withPath(path, async () => {
        try {
          return JSON.stringify(await investigateGitRange(request));
        } catch (error) {
          return `THREW:${(error as Error).message}`;
        }
      });
    } finally {
      if (saved === undefined) delete process.env.FAULTLINE_ARGV_LOG;
      else process.env.FAULTLINE_ARGV_LOG = saved;
    }
  },
  verifyGitMinimizationResult: ({ text, expected }: { text: string; expected?: string }) =>
    canonicalJson(verifyGitMinimizationResult(JSON.parse(text), expected)),
  // minimizeGitDiff with the native runner, with PATH and
  // FAULTLINE_ARGV_LOG (the git and docker recorders' log) replaced:
  // JSON.stringify of the result (key order kept), or "THREW:<message>".
  // Start the oracle seeded (oracle.StartSeeded) to compare nonces and times.
  minimizeGitDiff: async ({ request, path, argvLog }: { request: GitMinimizationRequest; path: string; argvLog: string }) => {
    const saved = process.env.FAULTLINE_ARGV_LOG;
    process.env.FAULTLINE_ARGV_LOG = argvLog;
    try {
      return await withPath(path, async () => {
        try {
          return JSON.stringify(await minimizeGitDiff(request));
        } catch (error) {
          return `THREW:${(error as Error).message}`;
        }
      });
    } finally {
      if (saved === undefined) delete process.env.FAULTLINE_ARGV_LOG;
      else process.env.FAULTLINE_ARGV_LOG = saved;
    }
  },
  // writeGitInvestigationProofBundle with PATH and FAULTLINE_ARGV_LOG (the
  // git recorder's log) replaced: JSON.stringify of the written bundle (key
  // order kept), or "THREW:<message>". Start the oracle seeded to compare
  // the staging UUID, the temporary clone and the generatedAt default.
  writeGitInvestigationProofBundle: ({ output, result, frozen, options, path, argvLog }: {
    output: string; result: unknown; frozen: unknown; options: GitProofBundleWriteOptions; path: string; argvLog: string;
  }) => {
    const saved = process.env.FAULTLINE_ARGV_LOG;
    const savedPath = process.env.PATH;
    process.env.FAULTLINE_ARGV_LOG = argvLog;
    process.env.PATH = path;
    try {
      return JSON.stringify(writeGitInvestigationProofBundle(output, result as never, frozen as never, options));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    } finally {
      if (saved === undefined) delete process.env.FAULTLINE_ARGV_LOG;
      else process.env.FAULTLINE_ARGV_LOG = saved;
      process.env.PATH = savedPath;
    }
  },
  // A lifecycle ledger over a fixture repository (it reads the clock, so use
  // an unseeded oracle): with commits, one clean checkpoint per commit
  // (checking each out); without, one checkpoint of the current HEAD.
  gitLifecycleLedger: ({ repository, commits }: { repository: string; commits?: string[] }) =>
    JSON.stringify(commits === undefined ? lifecycleBoundToDescendant(repository) : lifecycleBoundToEveryInvestigatedState(repository, commits)),
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
  // Secret allowlist and turn-snapshot path policy (slice 3a, Task 7). The
  // ignore rules and the secret scan are private to turn-snapshot.ts, so they
  // run through planTurnSnapshotPaths with a stub Git runner that lists
  // `paths` as the tracked files; it returns the kept paths and warnings.
  loadSecretAllowlist: ({ dir }: { dir: string }) => {
    try {
      const allowlist = loadSecretAllowlist(dir);
      return `${allowlistConfigDigest(allowlist)} ${JSON.stringify(allowlist)}`;
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  filterAllowlistedHighOccurrences: ({ allowlist, path, occurrences, fileDigest }: { allowlist: any; path: string; occurrences: any[]; fileDigest: string }) =>
    canonicalJson(filterAllowlistedHighOccurrences(allowlist, path, { occurrences } as any, fileDigest)),
  isEntropyTokenAllowlisted: ({ allowlist, path, token, fileDigest }: { allowlist: any; path: string; token: string; fileDigest: string }) =>
    String(isEntropyTokenAllowlisted(allowlist, path, token, fileDigest)),
  planTurnSnapshotPaths: ({ root, paths, skipSecretScan }: { root: string; paths: string[]; skipSecretScan: boolean }) => {
    try {
      const runGit = (_root: string, args: readonly string[]) => (args.includes("--others") ? "" : paths.join("\0"));
      const { paths: kept, warnings } = planTurnSnapshotPaths(root, runGit, { trackedFilesOnly: true, skipSecretScan });
      return canonicalJson({ paths: kept, warnings });
    } catch (error) {
      return `THREW:${(error as Error).message}`;
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
