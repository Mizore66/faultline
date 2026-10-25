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
import { readFaultLineConfig, ensureFaultLineConfig, updateFaultLineConfigImage } from "../../src/config.js";
import { writeRuntimeMappingFile } from "../../src/runtime-mapping-file.js";
import { readFileSync as cwReadFile, statSync as cwStat } from "node:fs";
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
import { DOCTOR_SAFE_GIT_CONFIG, HOST_COMMAND_SAFE_GIT_CONFIG } from "../../src/doctor.js";
import {
  CuratedRuntimeResolutionError, ProjectRuntimeImagePreparationError, describeProjectRuntimeImageBuild, prepareCuratedRuntime, prepareProjectRuntimeImage,
  resolveCuratedRuntime, resolveProjectRuntimeImage
} from "../../src/runtime.js";
import * as runtimeModule from "../../src/runtime.js";
import { lstatSync as cwLstat, readdirSync as cwReaddir } from "node:fs";
import { sha256 as cwSha256 } from "../../src/canonical.js";
import type { DockerImageInspectResult } from "../../src/runtime.js";
import { allowlistConfigDigest, filterAllowlistedHighOccurrences, isEntropyTokenAllowlisted, loadSecretAllowlist } from "../../src/secret-allowlist.js";
import { captureTurnTreeSnapshot, defaultTurnSnapshotGitRunner, planTurnSnapshotPaths, turnSnapshotRepoCachePath } from "../../src/turn-snapshot.js";
import type { TurnSnapshotGitRunner } from "../../src/turn-snapshot.js";
import { existsSync as tsExists } from "node:fs";
import { IncidentIntakeError, createNodeIncidentIntakeRunner, suggestIncidentRanges } from "../../src/incident-intake.js";

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

// Runs a project-image call: JSON.stringify of its result, or
// THREW:<code>:<message>.
function projectThrew(error: unknown): string {
  return `THREW:${error instanceof ProjectRuntimeImagePreparationError ? error.code : "?"}:${(error as Error).message}`;
}
function projectOutcome(fn: () => unknown): string {
  try {
    return JSON.stringify(fn());
  } catch (error) {
    return projectThrew(error);
  }
}
async function projectOutcomeAsync(fn: () => Promise<unknown>): Promise<string> {
  try {
    return JSON.stringify(await fn());
  } catch (error) {
    return projectThrew(error);
  }
}

// A Docker runner that replays steps in order ({ throw } rejects with an
// Error) and records [...argv, timeoutMs] per call.
function scriptedDockerRunner(steps: ({ throw: string } | DockerImageInspectResult)[]) {
  const calls: unknown[] = [];
  const runner = { run: async (command: { arguments: readonly string[]; timeoutMs: number }) => {
    calls.push([...command.arguments, command.timeoutMs]);
    const step = steps.shift();
    if (step === undefined) throw new Error("script exhausted");
    if ("throw" in step) throw new Error(step.throw);
    return step;
  } };
  return { runner, calls };
}

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
  // configWriters runs ensure/update steps in dir and reports, per step, the
  // outcome, file mode and file bytes (base64) with dir replaced by <D>.
  configWriters: ({ dir, steps }: { dir: string; steps: { kind: "ensure" | "update"; alias?: string; image?: string }[] }) => {
    const mask = (text: string) => text.split(dir).join("<D>");
    const lines: string[] = [];
    for (const step of steps) {
      let head: string;
      try {
        if (step.kind === "ensure") {
          head = ensureFaultLineConfig({ repository: dir, ...(step.alias === undefined ? {} : { runtimeAlias: step.alias as "node" }), ...(step.image === undefined ? {} : { image: step.image }) }).status;
        } else {
          updateFaultLineConfigImage({ repository: dir, image: step.image!, ...(step.alias === undefined ? {} : { runtimeAlias: step.alias as "node" }) });
          head = "OK";
        }
      } catch (error) {
        head = `THREW:${mask((error as Error).message)}`;
      }
      const file = `${dir}/.faultline/config.json`;
      let tail = "nofile";
      try {
        tail = `${(cwStat(file).mode & 0o777).toString(8)}:${Buffer.from(mask(cwReadFile(file, "utf8")), "utf8").toString("base64")}`;
      } catch { /* absent */ }
      lines.push(`${head}|${tail}`);
    }
    return lines.join("\n");
  },
  // runtimeMappingWriter writes a mapping file under dir and reports its bytes.
  runtimeMappingWriter: ({ dir, pairs, generatedAt, source }: { dir: string; pairs: { fingerprintDigest: string; image: string }[]; generatedAt: string; source?: string }) => {
    try {
      const out = `${dir}/sub/m.json`;
      writeRuntimeMappingFile({ pairs, outputPath: out, generatedAt, ...(source === undefined ? {} : { source }) });
      return `${(cwStat(out).mode & 0o777).toString(8)}:${cwReadFile(out, "utf8")}`;
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
  // Curated runtimes (internal/runtimeimage). The injected runner replays
  // steps in order ({ throw } rejects with an Error); the result is
  // "<canonical result or THREW:<code>:<message>>|<canonical [[...argv, timeoutMs]]>".
  curatedRuntime: async ({ fn, requested, steps }: { fn: "resolve" | "prepare"; requested: string; steps: ({ throw: string } | DockerImageInspectResult)[] }) => {
    const calls: unknown[] = [];
    const runner = { run: async (command: { arguments: readonly string[]; timeoutMs: number }) => {
      calls.push([...command.arguments, command.timeoutMs]);
      const step = steps.shift();
      if (step === undefined) throw new Error("script exhausted");
      if ("throw" in step) throw new Error(step.throw);
      return step;
    } };
    let outcome: string;
    try {
      outcome = canonicalJson(fn === "resolve" ? await resolveCuratedRuntime(requested, runner) : await prepareCuratedRuntime(requested, { pullRunner: runner, inspectRunner: runner }));
    } catch (error) {
      outcome = `THREW:${error instanceof CuratedRuntimeResolutionError ? error.code : "?"}:${(error as Error).message}`;
    }
    return `${outcome}|${canonicalJson(calls)}`;
  },
  // The unexported RepoDigest helpers, evaluated from the compiled
  // dist/runtime.js (pnpm build): parseRepoDigests and normalizeRepository
  // over many inputs.
  repoDigestHelpers: ({ stdouts, repositories }: { stdouts: string[]; repositories: string[] }) => {
    const src = cwReadFile(nodePath.join(process.cwd(), "dist", "runtime.js"), "utf8");
    const take = (name: string) => {
      const start = src.indexOf(`function ${name}(`);
      const end = src.indexOf("\n}\n", start);
      if (start < 0 || end < 0) throw new Error(`${name} not found in dist/runtime.js`);
      return src.slice(start, end + 2);
    };
    const helpers = new Function(`${take("parseRepoDigests")}\n${take("normalizeRepository")}\nreturn { parseRepoDigests, normalizeRepository };`)();
    return canonicalJson({ parsed: stdouts.map((s) => helpers.parseRepoDigests(s)), normalized: repositories.map((r) => helpers.normalizeRepository(r)) });
  },
  // Project runtime images (internal/runtimeimage). Results are
  // JSON.stringify (key order matters to fl runtime project's output) or
  // THREW:<code>:<message>.
  describeProjectRuntimeImageBuild: ({ request }: { request: any }) => projectOutcome(() => describeProjectRuntimeImageBuild(request)),
  // The unexported walk, evaluated from the compiled dist/runtime.js on an
  // already normalized context; files is what it handed to digestJson.
  fingerprintProjectBuildContext: ({ contextDirectory, dockerfile }: { contextDirectory: string; dockerfile: string }) => {
    const src = cwReadFile(nodePath.join(process.cwd(), "dist", "runtime.js"), "utf8");
    const start = src.indexOf("function fingerprintProjectBuildContext(");
    const end = src.indexOf("\n}\n", start);
    if (start < 0 || end < 0) throw new Error("fingerprintProjectBuildContext not found in dist/runtime.js");
    let files: unknown;
    const recordingDigest = (value: { files?: unknown }) => {
      files = value.files;
      return digestJson(value);
    };
    const projectImageInputError = (message: string) => {
      throw new ProjectRuntimeImagePreparationError("PROJECT_IMAGE_INVALID_INPUT", message);
    };
    const fingerprint = new Function(
      "readdirSync", "resolve", "lstatSync", "readFileSync", "sha256", "digestJson", "projectImageInputError",
      "PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_FILES", "PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_BYTES",
      `${src.slice(start, end + 2)}\nreturn fingerprintProjectBuildContext;`
    )(cwReaddir, nodePath.resolve, cwLstat, cwReadFile, cwSha256, recordingDigest, projectImageInputError,
      runtimeModule.PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_FILES, runtimeModule.PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_BYTES);
    return projectOutcome(() => {
      const result = fingerprint({ contextDirectory, dockerfile });
      return { files, ...result };
    });
  },
  // The injected runner replays steps like curatedRuntime; the result is
  // "<outcome>|<canonical [[...argv, timeoutMs]]>".
  resolveProjectRuntimeImage: async ({ tag, steps }: { tag: string; steps: ({ throw: string } | DockerImageInspectResult)[] }) => {
    const { runner, calls } = scriptedDockerRunner(steps);
    return `${await projectOutcomeAsync(() => resolveProjectRuntimeImage(tag, runner))}|${canonicalJson(calls)}`;
  },
  prepareProjectRuntimeImage: async ({ request, steps }: { request: any; steps: ({ throw: string } | DockerImageInspectResult)[] }) => {
    const { runner, calls } = scriptedDockerRunner(steps);
    return `${await projectOutcomeAsync(() => prepareProjectRuntimeImage(request, { buildRunner: runner, inspectRunner: runner }))}|${canonicalJson(calls)}`;
  },
  // The safe Git -c lists (internal/safegit).
  safeGitLists: () => ({ hostCommand: [...HOST_COMMAND_SAFE_GIT_CONFIG], doctor: [...DOCTOR_SAFE_GIT_CONFIG] }),
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
  // Turn-snapshot capture (slice 3a, Task 8): captureTurnTreeSnapshot on
  // `repository` with a no-op sleep, PATH replaced, and runGit the default
  // runner wrapped to record each call as [root, args, env additions as
  // KEY=value, sha256 of the input or null]. sessionCachePath "" is none and
  // "repo" is turnSnapshotRepoCachePath(repository), resolved first as
  // `fl init` does. It returns JSON.stringify of { cachePath, outcome:
  // { snapshot, warnings } or { threw }, onWarning, cache (the cache file's
  // text afterwards when it is a file, or null), calls }. Start the oracle seeded to compare
  // capturedAt and the temporary index names.
  captureTurnTreeSnapshot: ({ repository, sessionCachePath, trackedFilesOnly, path }: {
    repository: string; sessionCachePath: string; trackedFilesOnly: boolean; path: string;
  }) => withPath(path, async () => {
    const onWarning: string[] = [];
    const calls: unknown[] = [];
    const runGit: TurnSnapshotGitRunner = (root, args, env, options) => {
      calls.push([root, [...args], Object.entries(env ?? {}).map(([key, value]) => `${key}=${value}`),
        options?.input === undefined ? null : createHash("sha256").update(options.input).digest("hex")]);
      return defaultTurnSnapshotGitRunner(root, args, env, options);
    };
    let cachePath: string | null = null;
    let outcome: unknown;
    try {
      cachePath = sessionCachePath === "repo" ? turnSnapshotRepoCachePath(repository) : sessionCachePath;
      const { snapshot, warnings } = captureTurnTreeSnapshot(repository, {
        sleep: () => {},
        runGit,
        trackedFilesOnly,
        ...(cachePath === "" ? {} : { sessionCachePath: cachePath }),
        onWarning: (warning) => { onWarning.push(warning); }
      });
      outcome = { snapshot, warnings };
    } catch (error) {
      outcome = { threw: (error as Error).message };
    }
    const cache = cachePath && tsExists(cachePath) && cwStat(cachePath).isFile() ? cwReadFile(cachePath, "utf8") : null;
    return JSON.stringify({ cachePath, outcome, onWarning, cache, calls });
  }),
  // Incident intake (slice 3b, Task 3): suggestIncidentRanges on
  // `repository`. With steps, the runner replays them like curatedRuntime
  // (an exhausted script throws); without, it is the default Node runner.
  // Each call records [...argv, timeoutMs, maxOutputBytes]; the result is
  // "<canonical intake or THREW:<code>:<message>>|<canonical calls>".
  // safeRefNames tests the unexported SAFE_REF_NAME (from dist) on each ref.
  suggestIncidentRanges: async ({ repository, steps }: { repository: string; steps?: ({ throw: string } | DockerImageInspectResult)[] }) => {
    const calls: unknown[] = [];
    const inner = steps === undefined ? createNodeIncidentIntakeRunner() : scriptedDockerRunner(steps).runner;
    const runner = { run: (command: { arguments: readonly string[]; timeoutMs: number; maxOutputBytes: number }) => {
      calls.push([...command.arguments, command.timeoutMs, command.maxOutputBytes]);
      return inner.run(command as any);
    } };
    let outcome: string;
    try {
      outcome = canonicalJson(await suggestIncidentRanges({ repository, runner: runner as any }));
    } catch (error) {
      outcome = `THREW:${error instanceof IncidentIntakeError ? error.code : "?"}:${(error as Error).message}`;
    }
    return `${outcome}|${canonicalJson(calls)}`;
  },
  safeRefNames: ({ refs }: { refs: string[] }) => {
    const line = cwReadFile(nodePath.join(process.cwd(), "dist", "incident-intake.js"), "utf8").split("\n").find((l) => l.startsWith("const SAFE_REF_NAME = "));
    if (line === undefined) throw new Error("SAFE_REF_NAME not found in dist/incident-intake.js");
    const re = new Function(`${line}\nreturn SAFE_REF_NAME;`)() as RegExp;
    return refs.map((ref) => (re.test(ref) ? "1" : "0")).join("");
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
