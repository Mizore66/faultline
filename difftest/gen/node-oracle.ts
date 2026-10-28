// Long-running oracle over frozen TS. One JSON request per stdin line,
// one JSON response per stdout line. Run: pnpm exec tsx difftest/gen/node-oracle.ts
import { createInterface } from "node:readline";
import nodePath from "node:path";
import { spawn } from "node:child_process";
import { spawnSync } from "node:child_process";
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
import { createIncidentDraft, resolveIncidentRange, verifyIncidentDraft } from "../../src/incident.js";
import { readIncidentDraft, writeIncidentDraft } from "../../src/incident-store.js";
import { realpathSync } from "node:fs";
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
  // Slice 3b Task 2 (incident drafts): JSON.stringify of the result (key
  // order kept), or "THREW:<message>". createIncidentDraft reads
  // `new Date()` for createdAt when the input has none, as the CLI does
  // (cli-app.ts:617).
  createIncidentDraft: ({ input }: { input: Record<string, unknown> }) => {
    try {
      return JSON.stringify(createIncidentDraft("createdAt" in input ? input : { ...input, createdAt: new Date().toISOString() }));
    } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  verifyIncidentDraft: ({ text }: { text: string }) => {
    try { return JSON.stringify(verifyIncidentDraft(JSON.parse(text))); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  resolveIncidentRange: ({ facts }: { facts: unknown }) => {
    try { return JSON.stringify(resolveIncidentRange(facts)); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  // Runs write/read steps on draft stores under dir, then lists dir's tree
  // (path, type, mode, file bytes in base64), with dir and its realpath
  // masked as <D>.
  incidentStoreRoundTrip: ({ dir, steps }: { dir: string; steps: { kind: "write" | "read"; store: string; text?: string; id?: string }[] }) => {
    const real = realpathSync(dir);
    const mask = (text: string) => text.split(real).join("<D>").split(dir).join("<D>");
    const lines = steps.map((step) => {
      try {
        if (step.kind === "write") return `OK:${mask(writeIncidentDraft(step.store, JSON.parse(step.text!)).path)}`;
        const stored = readIncidentDraft(step.store, step.id!);
        return `OK:${mask(stored.path)}:${canonicalJson(stored.draft)}`;
      } catch (error) { return `THREW:${mask((error as Error).message)}`; }
    });
    const walk = (rel: string): void => {
      for (const name of cwReaddir(nodePath.join(dir, rel)).sort()) {
        const child = rel === "" ? name : `${rel}/${name}`;
        const stat = cwLstat(nodePath.join(dir, child));
        const kind = stat.isSymbolicLink() ? "l" : stat.isDirectory() ? "d" : "f";
        lines.push(`${child}|${kind}|${(stat.mode & 0o777).toString(8)}${kind === "f" ? `|${cwReadFile(nodePath.join(dir, child)).toString("base64")}` : ""}`);
        if (kind === "d") { try { walk(child); } catch { lines.push(`${child}|unreadable`); } }
      }
    };
    walk("");
    return lines.join("\n");
  },
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
  // intake is INTAKE_SAFE_GIT_CONFIG, unexported from cli-app.ts, evaluated
  // from the module-level const array literal in dist/cli-app.js.
  safeGitLists: () => {
    const src = cwReadFile(nodePath.join(process.cwd(), "dist", "cli-app.js"), "utf8");
    const start = src.indexOf("const INTAKE_SAFE_GIT_CONFIG = [");
    const end = src.indexOf("\n];", start);
    if (start < 0 || end < 0) throw new Error("INTAKE_SAFE_GIT_CONFIG not found in dist/cli-app.js");
    const intake = new Function(`${src.slice(start, end + 3)}\nreturn INTAKE_SAFE_GIT_CONFIG;`)();
    return { hostCommand: [...HOST_COMMAND_SAFE_GIT_CONFIG], doctor: [...DOCTOR_SAFE_GIT_CONFIG], intake };
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
  // Prevention writer, --from-bundle builder and AGENTS.md (slice 3b, Task 7).
  // writePreventionProof runs in `cwd` (the default root is resolved from
  // it) under `umask` when given: JSON.stringify of the written package, or
  // "THREW:<message>". Start the oracle seeded to compare the stage UUID.
  writePreventionProof: async ({ cwd, output, input, umask }: { cwd: string; output: string; input: any; umask?: number }) => {
    const { writePreventionProof } = await import("../../src/prevention-proof.js");
    const saved = process.cwd();
    const savedUmask = umask === undefined ? undefined : process.umask(umask);
    process.chdir(cwd);
    try {
      return JSON.stringify(writePreventionProof(output, input));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    } finally {
      process.chdir(saved);
      if (savedUmask !== undefined) process.umask(savedUmask);
    }
  },
  // fixtures.ts sampleInput and groundedPreventionInput (the slice 1 bases).
  preventionFixtures: async () => {
    const { sampleInput, groundedPreventionInput } = await import("./fixtures.js");
    return JSON.stringify({ summary: sampleInput(), grounded: groundedPreventionInput() });
  },
  // upsertFaultLineAgentsMd with classification PREVENTION_VERIFIED:
  // JSON.stringify of { path, status }, or "THREW:<message>".
  upsertFaultLineAgentsMd: async ({ repository, input }: { repository: string; input: any }) => {
    const { upsertFaultLineAgentsMd } = await import("../../src/agents-md.js");
    try {
      return JSON.stringify(upsertFaultLineAgentsMd({ repository, classification: "PREVENTION_VERIFIED", ...input }));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  // The --from-bundle build as cli-app.ts:2603 calls it: the patch digest
  // first (digestRepairPatchFile), then buildPreventionProofFromVerifiedArtifacts.
  // JSON.stringify of the input, {"reasons":[...]}, or "THREW:<message>".
  // Start the oracle seeded to compare the verifier's mkdtemp names.
  // repairBaseTree and hardGuardArtifactDigests (slice 4c) are passed only
  // when given; the CLI passes neither.
  buildPreventionProofFromVerifiedArtifacts: async ({ bundle, expectRoot, commit, tree, runs, patch, codexThreadId, repairBaseTree, hardGuardArtifactDigests }: {
    bundle: string; expectRoot: string; commit: string; tree: string; runs: unknown[]; patch: string; codexThreadId?: string;
    repairBaseTree?: string; hardGuardArtifactDigests?: string[];
  }) => {
    const { buildPreventionProofFromVerifiedArtifacts, digestRepairPatchFile } = await import("../../src/prevention-from-artifacts.js");
    try {
      const built = buildPreventionProofFromVerifiedArtifacts({
        bundleDirectory: bundle, expectRoot, repaired: { commit, tree, runs: runs as never },
        repairPatchDigest: digestRepairPatchFile(patch), ...(codexThreadId === undefined ? {} : { codexThreadId }),
        ...(repairBaseTree === undefined ? {} : { repairBaseTree }),
        ...(hardGuardArtifactDigests === undefined ? {} : { hardGuardArtifactDigests })
      });
      return JSON.stringify(built.ok ? built.input : { reasons: built.reasons });
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
  // Slice 4c Task 6 (repair brief). createRepairEvidencePacket: canonicalJson
  // of the packet, or "THREW:<message>". validateRepairBrief: JSON.stringify
  // of the validation (key order kept). extractOpenAiResponseText: the text,
  // or "THREW:<message>". Inputs are JSON texts so both sides parse the same.
  createRepairEvidencePacket: async ({ text }: { text: string }) => {
    const { createRepairEvidencePacket } = await import("../../src/repair-brief.js");
    try { return canonicalJson(createRepairEvidencePacket(JSON.parse(text))); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  validateRepairBrief: async ({ packet, brief }: { packet: string; brief: string }) => {
    const { validateRepairBrief } = await import("../../src/repair-brief.js");
    try { return JSON.stringify(validateRepairBrief(JSON.parse(packet), JSON.parse(brief))); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  extractOpenAiResponseText: async ({ text }: { text: string }) => {
    const { extractOpenAiResponseText } = await import("../../src/openai-response.js");
    try { return extractOpenAiResponseText(JSON.parse(text)); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  // repairBriefJsonSchema is not exported: JSON.stringify of the request
  // body's `text` member, recorded from proposeRepairBriefWithGpt with fetch
  // stubbed (a 500 reply ends the call) for git-unbound's packet.
  repairBriefJsonSchema: async () => {
    const { createRepairEvidencePacket, proposeRepairBriefWithGpt } = await import("../../src/repair-brief.js");
    const packet = createRepairEvidencePacket(JSON.parse(cwReadFile("difftest/testdata/bases/git-unbound/investigation.json", "utf8")));
    const realFetch = globalThis.fetch;
    let body = "";
    globalThis.fetch = (async (_url: unknown, init?: { body?: unknown }) => {
      body = String(init?.body);
      return new Response("stub", { status: 500 });
    }) as typeof fetch;
    try { await proposeRepairBriefWithGpt(packet, { apiKey: "fl-test-key" }).catch(() => undefined); } finally { globalThis.fetch = realFetch; }
    return JSON.stringify((JSON.parse(body) as { text: unknown }).text);
  },
  // Slice 4c Task 5: the hard-guard helpers (no src/ caller).
  // hardGuardDigestsFromStrings: JSON.stringify of the digests.
  hardGuardDigestsFromStrings: async ({ values }: { values: string[] }) => {
    const { hardGuardDigestsFromStrings } = await import("../../src/prevention-from-artifacts.js");
    return JSON.stringify(hardGuardDigestsFromStrings(values));
  },
  // writeHardGuardDigestSidecar: the returned digest, or "THREW:<message>".
  writeHardGuardDigestSidecar: async ({ directory, digests }: { directory: string; digests: string[] }) => {
    const { writeHardGuardDigestSidecar } = await import("../../src/prevention-from-artifacts.js");
    try {
      return writeHardGuardDigestSidecar(directory, digests);
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  // Slice 4c, Task 7: src/attestation.ts. Each op runs in `cwd` when given
  // (relative stores resolve from it). create: canonicalJson of the receipt;
  // write and the verifiers: JSON.stringify of the result; "THREW:<message>"
  // on a throw. isCanonicalIsoDate: one 0/1 per string. Start seeded to
  // compare issuedAt and the staging UUID.
  createBundleAttestation: async ({ input }: { input: any }) => {
    const { createBundleAttestation } = await import("../../src/attestation.js");
    try { return canonicalJson(createBundleAttestation(input)); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  verifyBundleAttestation: async ({ record, expected }: { record: unknown; expected?: string }) => {
    const { verifyBundleAttestation } = await import("../../src/attestation.js");
    try { return JSON.stringify(verifyBundleAttestation(record, expected)); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  writeBundleAttestation: async ({ cwd, store, input, root }: { cwd?: string; store: string; input: any; root: string }) => {
    const { writeBundleAttestation } = await import("../../src/attestation.js");
    const saved = process.cwd();
    if (cwd !== undefined) process.chdir(cwd);
    try { return JSON.stringify(writeBundleAttestation(store, input, { attestationRoot: root })); } catch (error) { return `THREW:${(error as Error).message}`; } finally { process.chdir(saved); }
  },
  verifyStoredBundleAttestation: async ({ cwd, store, receiptId, expected, root }: { cwd?: string; store: string; receiptId: string; expected?: string; root: string }) => {
    const { verifyStoredBundleAttestation } = await import("../../src/attestation.js");
    const saved = process.cwd();
    if (cwd !== undefined) process.chdir(cwd);
    try { return JSON.stringify(verifyStoredBundleAttestation(store, receiptId, expected, { attestationRoot: root })); } catch (error) { return `THREW:${(error as Error).message}`; } finally { process.chdir(saved); }
  },
  isCanonicalIsoDate: ({ values }: { values: string[] }) =>
    values.map((v) => { const d = new Date(v); return Number.isFinite(d.getTime()) && d.toISOString() === v ? "1" : "0"; }).join(""),
  // spawnSync with a timeout, as the Codex runner and
  // commitRepairedWorktreeState call it: status, signal, error message and
  // output lengths. input is latin1 (one char per byte).
  spawnSyncTimeout: async ({ file, args, timeout, maxBuffer, input }: {
    file: string; args: string[]; timeout: number; maxBuffer?: number; input?: string;
  }) => {
    const { spawnSync } = await import("node:child_process");
    const result = spawnSync(file, args, {
      encoding: "buffer",
      shell: false,
      windowsHide: true,
      timeout,
      ...(maxBuffer === undefined ? {} : { maxBuffer }),
      ...(input === undefined ? {} : { input: Buffer.from(input, "latin1") })
    });
    return {
      status: result.status,
      signal: result.signal,
      error: result.error?.message ?? null,
      stdoutLength: result.stdout?.length ?? null,
      stderrLength: result.stderr?.length ?? null
    };
  },
  // execFileSync(file, args, { encoding: "utf8", stdio: ["ignore", "pipe",
  // "pipe"], windowsHide: true }) as github-provenance.ts calls it: the
  // stdout, or the thrown error's message.
  execFileSyncError: async ({ file, args, cwd, timeout, maxBuffer }: {
    file: string; args: string[]; cwd?: string; timeout?: number; maxBuffer?: number;
  }) => {
    const { execFileSync } = await import("node:child_process");
    try {
      const stdout = execFileSync(file, args, {
        ...(cwd === undefined ? {} : { cwd }),
        encoding: "utf8",
        windowsHide: true,
        stdio: ["ignore", "pipe", "pipe"],
        ...(timeout === undefined ? {} : { timeout }),
        ...(maxBuffer === undefined ? {} : { maxBuffer })
      });
      return { stdout, error: null };
    } catch (error) {
      return { stdout: null, error: (error as Error).message };
    }
  },
  // Slice 4c Task 9 (repair brief store). writeRepairBriefArtifact runs in
  // `cwd` under `umask` when given, with packet and brief as JSON texts:
  // JSON.stringify of the written package, or "THREW:<message>". Start the
  // oracle seeded to compare the stage UUID. verifyRepairBriefArtifact:
  // JSON.stringify of the verification (an absent `expected` is undefined).
  writeRepairBriefArtifact: async ({ cwd, output, packet, brief, source, umask }: {
    cwd: string; output: string; packet: string; brief: string; source: any; umask?: number;
  }) => {
    const { writeRepairBriefArtifact } = await import("../../src/repair-brief-store.js");
    const saved = process.cwd();
    const savedUmask = umask === undefined ? undefined : process.umask(umask);
    process.chdir(cwd);
    try {
      return JSON.stringify(writeRepairBriefArtifact(output, JSON.parse(packet), JSON.parse(brief), source));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    } finally {
      process.chdir(saved);
      if (savedUmask !== undefined) process.umask(savedUmask);
    }
  },
  verifyRepairBriefArtifact: async ({ directory, expected }: { directory: string; expected?: string }) => {
    const { verifyRepairBriefArtifact } = await import("../../src/repair-brief-store.js");
    try { return JSON.stringify(verifyRepairBriefArtifact(directory, expected)); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  // Slice 4c Task 4: cpSync(src, dest, { recursive: true, dereference: false,
  // errorOnExist: true, force: false }) as repair-worktree.ts:111 calls it,
  // run in a child node (umask 022, cwd) so a C++ abort inside cpSync does
  // not take the oracle with it: "OK", "THREW:<message>" or "ABORT:<stderr>".
  cpSyncTree: ({ src, dest, cwd }: { src: string; dest: string; cwd: string }) => {
    const script = `try { require("node:fs").cpSync(process.argv[1], process.argv[2], { recursive: true, dereference: false, errorOnExist: true, force: false }); process.stdout.write("OK"); } catch (e) { process.stdout.write("THREW:" + e.message); }`;
    const run = spawnSync(process.execPath, ["-e", script, src, dest], { cwd, encoding: "utf8", env: process.env });
    if (run.signal !== null || run.status !== 0) return `ABORT:${run.stderr}`;
    return run.stdout;
  },
  // resolveCodexRunner from the compiled dist/runners/codex-runner.js (pnpm
  // build), run in a child node with the given cwd, PATH and
  // FAULTLINE_CODEX_BIN (null: unset): the resolution, or the error message.
  resolveCodexRunner: async ({ cwd, path, bin }: { cwd: string; path: string | null; bin: string | null }) => {
    const { spawnSync } = await import("node:child_process");
    const { pathToFileURL } = await import("node:url");
    const env: Record<string, string | undefined> = { ...process.env, PATH: path ?? undefined, FAULTLINE_CODEX_BIN: bin ?? undefined };
    for (const key of Object.keys(env)) if (env[key] === undefined) delete env[key];
    const script = [
      "const { resolveCodexRunner } = await import(process.argv[1]);",
      "let out;",
      "try { out = { resolution: resolveCodexRunner() }; } catch (error) { out = { error: error.message }; }",
      "process.stdout.write(JSON.stringify(out));"
    ].join("\n");
    const module = pathToFileURL(nodePath.join(process.cwd(), "dist", "runners", "codex-runner.js")).href;
    const child = spawnSync(process.execPath, ["--input-type=module", "-e", script, module], { cwd, env: env as NodeJS.ProcessEnv, encoding: "utf8" });
    if (child.status !== 0) throw new Error(`resolveCodexRunner child failed: ${child.status} ${child.stderr}`);
    return JSON.parse(child.stdout) as unknown;
  },
  // implementWitnessWithCodex with the runner object cli-app.ts:1181-1196
  // builds for witness implement --with-codex (allow: whether
  // --allow-codex-full-auto is given), with FAULTLINE_CODEX_BIN set to bin
  // for the call: the result, JSON.stringify'd.
  implementWitnessCodexCli: async ({ bin, allow, proposalId, incident, behavior, overlayOut, repository }: {
    bin: string; allow: boolean; proposalId: string; incident: unknown; behavior: string; overlayOut: string; repository: string;
  }) => {
    const { implementWitnessWithCodex } = await import("../../src/codex-loop.js");
    const saved = process.env.FAULTLINE_CODEX_BIN;
    process.env.FAULTLINE_CODEX_BIN = bin;
    try {
      const result = await implementWitnessWithCodex({
        proposalId, incident, behavior, overlayOut, repository,
        runner: {
          async run(codexArgs: readonly string[], options: { cwd: string; input?: string }) {
            if (!allow) {
              throw new Error("Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex.");
            }
            const { spawnCodex } = await import("../../src/runners/codex-runner.js");
            const executed = spawnCodex([...codexArgs], { cwd: options.cwd, input: options.input, timeout: 120_000 });
            return {
              exitCode: executed.status,
              stdout: typeof executed.stdout === "string" ? executed.stdout : "",
              stderr: typeof executed.stderr === "string" ? executed.stderr : ""
            };
          }
        }
      });
      return JSON.stringify(result);
    } finally {
      if (saved === undefined) delete process.env.FAULTLINE_CODEX_BIN;
      else process.env.FAULTLINE_CODEX_BIN = saved;
    }
  },
  // Slice 4c Task 11 (src/repair-worktree.ts). Each returns a string: the
  // session / cleanup / patch as JSON.stringify (patch raw), or "THREW:<message>".
  // `path` replaces PATH (the hardened Git environment copies it) so the case's
  // argv recorder sees every Git call. Start seeded to compare repairId.
  createRepairWorktree: async ({ repository, baseCommit, bundleDirectory, outputDirectory, path }: { repository: string; baseCommit: string; bundleDirectory: string; outputDirectory: string; path?: string }) => {
    const { createRepairWorktree } = await import("../../src/repair-worktree.js");
    try {
      return JSON.stringify(await withPath(path, () => createRepairWorktree({ repository, baseCommit, bundleDirectory, outputDirectory })));
    } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  removeRepairWorktree: async ({ repository, worktreePath, managedRoot, keepManagedRoot, path }: { repository: string; worktreePath: string; managedRoot?: string; keepManagedRoot?: boolean; path?: string }) => {
    const { removeRepairWorktree } = await import("../../src/repair-worktree.js");
    try {
      return JSON.stringify(await withPath(path, () => removeRepairWorktree({ repository, worktreePath, ...(managedRoot === undefined ? {} : { managedRoot }), ...(keepManagedRoot === undefined ? {} : { keepManagedRoot }) })));
    } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  collectRepairPatch: async ({ worktreePath, path }: { worktreePath: string; path?: string }) => {
    const { collectRepairPatch } = await import("../../src/repair-worktree.js");
    try {
      return (await withPath(path, () => collectRepairPatch(worktreePath))).patch;
    } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  // Slice 4c Task 13: commitRepairedWorktreeState, with process.env.PATH
  // set to `path` for the call when given (a fake git ahead of the real
  // one): JSON.stringify of { commit, tree }, or "THREW:<message>".
  commitRepairedWorktreeState: async ({ worktreePath, baseCommit, path }: { worktreePath: string; baseCommit: string; path?: string }) => {
    const { commitRepairedWorktreeState } = await import("../../src/prevention-from-artifacts.js");
    const saved = process.env.PATH;
    if (path !== undefined) process.env.PATH = path;
    try { return JSON.stringify(commitRepairedWorktreeState(worktreePath, baseCommit)); } catch (error) { return `THREW:${(error as Error).message}`; } finally { process.env.PATH = saved; }
  },
  // collectRepairedPreventionRuns with a scripted verifyOnce: each call
  // returns the next entry of `verdicts` as is, or throws Error(<rest>) for
  // a "THROW:<rest>" string. JSON.stringify of the result, or
  // "THREW:<message>".
  collectRepairedPreventionRuns: async ({ bundleDirectory, repairedCommit, repairedTree, verdicts }: {
    bundleDirectory: string; repairedCommit: string; repairedTree: string; verdicts: unknown[];
  }) => {
    const { collectRepairedPreventionRuns } = await import("../../src/prevention-from-artifacts.js");
    let next = 0;
    const verifyOnce = async () => {
      const verdict = verdicts[next++];
      if (typeof verdict === "string" && verdict.startsWith("THROW:")) throw new Error(verdict.slice(6));
      return verdict as { ok: boolean; detail: string; executionId?: string };
    };
    try {
      return JSON.stringify(await collectRepairedPreventionRuns({ worktreePath: "unused", bundleDirectory, repairedCommit, repairedTree, verifyOnce }));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  // Slice 4c Task 10: proposeRepairBriefWithGpt({ model }) against a local
  // test server. fetch of the fixed OpenAI URL goes to `url` instead (as
  // difftest/gen/fetch-shim.mjs does); OPENAI_API_KEY is `apiKey`, or unset
  // when null. JSON.stringify of the brief, or "THREW:<message>".
  proposeRepairBriefWithGpt: async ({ packet, model, apiKey, url }: { packet: string; model: string; apiKey: string | null; url: string }) => {
    const { proposeRepairBriefWithGpt } = await import("../../src/repair-brief.js");
    const realFetch = globalThis.fetch;
    const savedKey = process.env.OPENAI_API_KEY;
    globalThis.fetch = ((input: unknown, init?: RequestInit) =>
      realFetch(input === "https://api.openai.com/v1/responses" ? url : input as string, init)) as typeof fetch;
    if (apiKey === null) delete process.env.OPENAI_API_KEY; else process.env.OPENAI_API_KEY = apiKey;
    try {
      return JSON.stringify(await proposeRepairBriefWithGpt(JSON.parse(packet), { model }));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    } finally {
      globalThis.fetch = realFetch;
      if (savedKey === undefined) delete process.env.OPENAI_API_KEY; else process.env.OPENAI_API_KEY = savedKey;
    }
  },
  // Slice 4c Task 10: undici's response decoders (fetch/index.js onHeaders):
  // createGunzip, or InflateStream (zlib by the first byte's low nibble,
  // else raw), with flush and finishFlush Z_SYNC_FLUSH. Hex of the bytes,
  // or "E" when the stream errors.
  responseDecode: async ({ kind, hex }: { kind: "gzip" | "deflate"; hex: string }) => {
    const zlib = await import("node:zlib");
    const b = Buffer.from(hex, "hex");
    const opts = { flush: zlib.constants.Z_SYNC_FLUSH, finishFlush: zlib.constants.Z_SYNC_FLUSH };
    try {
      if (kind === "gzip") return zlib.gunzipSync(b, opts).toString("hex");
      if (b.length === 0) return "";
      return ((b[0]! & 15) === 8 ? zlib.inflateSync(b, opts) : zlib.inflateRawSync(b, opts)).toString("hex");
    } catch { return "E"; }
  },
  // Slice 4c, Task 14: src/github-provenance.ts, from a copy of the
  // compiled dist/github-provenance.js (pnpm build) whose
  // loadVerifiedGitProofView is memoized per directory (a view or the
  // thrown error), as the Go live tests memoize theirs: a bundle load takes
  // seconds and the bases do not change. Results are canonicalJson (create)
  // or JSON.stringify (key order kept: the verifiers, readTrust), or
  // "THREW:<message>". create takes a bundle directory or a view object;
  // without createdAt it reads the seeded clock.
  // env is set around the call (git-sha256's verification needs
  // GIT_DEFAULT_HASH=sha256 on its first, memoized, load).
  createGithubProvenanceReceiptFromView: async ({ bundle, view, ci, createdAt, env }: {
    bundle?: string; view?: any; ci: unknown; createdAt?: string; env?: Record<string, string>;
  }) => {
    const p = await provenanceModule();
    const saved = Object.fromEntries(Object.keys(env ?? {}).map((k) => [k, process.env[k]]));
    Object.assign(process.env, env ?? {});
    try {
      return canonicalJson(view === undefined ? p.createGithubProvenanceReceipt(bundle, ci, createdAt) : p.createGithubProvenanceReceiptFromView(view, ci, createdAt));
    } catch (error) { return `THREW:${(error as Error).message}`; } finally {
      for (const [k, v] of Object.entries(saved)) { if (v === undefined) delete process.env[k]; else process.env[k] = v; }
    }
  },
  // githubActionsIdentityFromEnvironment(workspace) with the default
  // process.env, run in one child node (dist/) per batch: for each case the
  // child deletes every GITHUB_* name from its process.env, sets the case's
  // env (a null value leaves the name unset; Node stores a lone surrogate
  // as its WTF-8 bytes) and calls the function, whose execFileSync git
  // inherits that environment. One canonicalJson or "THREW:<message>" per
  // case.
  githubActionsIdentityFromEnvironment: async ({ cases }: { cases: Array<{ workspace: string; env: Record<string, string | null> }> }) => {
    const { spawnSync } = await import("node:child_process");
    const { pathToFileURL } = await import("node:url");
    const dist = (name: string) => pathToFileURL(nodePath.join(process.cwd(), "dist", name)).href;
    const script = `const { githubActionsIdentityFromEnvironment } = await import(${JSON.stringify(dist("github-provenance.js"))});
const { canonicalJson } = await import(${JSON.stringify(dist("canonical.js"))});
const { readFileSync } = await import("node:fs");
const cases = JSON.parse(readFileSync(0, "utf8"));
const outs = [];
for (const c of cases) {
  for (const k of Object.keys(process.env)) if (k.startsWith("GITHUB_")) delete process.env[k];
  for (const [k, v] of Object.entries(c.env)) if (v !== null) process.env[k] = v;
  try { outs.push(canonicalJson(githubActionsIdentityFromEnvironment(c.workspace))); } catch (error) { outs.push("THREW:" + error.message); }
}
process.stdout.write(JSON.stringify(outs));`;
    const result = spawnSync(process.execPath, ["--input-type=module", "-e", script], { input: JSON.stringify(cases), encoding: "utf8", maxBuffer: 1 << 28 });
    if (result.status !== 0) throw new Error(`identity child failed: ${result.status} ${result.stderr}`);
    return JSON.parse(result.stdout);
  },
  verifyGithubProvenanceBinding: async ({ bundle, receipt, trust }: { bundle: string; receipt: unknown; trust: unknown }) => {
    const p = await provenanceModule();
    try { return JSON.stringify(p.verifyGithubProvenanceBinding(bundle, receipt, trust)); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  // The gh runners spawn "gh" from PATH, set to path for the call.
  verifyGithubArtifactAttestation: async ({ receiptFile, bundleFile, trust, sourceCommit, path }: {
    receiptFile: string; bundleFile: string; trust: unknown; sourceCommit: string; path: string;
  }) => {
    const p = await provenanceModule();
    const saved = process.env.PATH;
    process.env.PATH = path;
    try { return JSON.stringify(p.verifyGithubArtifactAttestation(receiptFile, bundleFile, trust, sourceCommit)); } catch (error) { return `THREW:${(error as Error).message}`; } finally { process.env.PATH = saved; }
  },
  verifySignedGithubProvenance: async ({ bundle, receiptFile, attestationBundleFile, trust, path }: {
    bundle: string; receiptFile: string; attestationBundleFile: string; trust: unknown; path: string;
  }) => {
    const p = await provenanceModule();
    const saved = process.env.PATH;
    process.env.PATH = path;
    try { return JSON.stringify(p.verifySignedGithubProvenance({ bundleDirectory: bundle, receiptFile, attestationBundleFile, trust })); } catch (error) { return `THREW:${(error as Error).message}`; } finally { process.env.PATH = saved; }
  },
  readGithubArtifactAttestationTrust: async ({ path }: { path: string }) => {
    const p = await provenanceModule();
    try { return JSON.stringify(p.readGithubArtifactAttestationTrust(path)); } catch (error) { return `THREW:${(error as Error).message}`; }
  },
  writeGithubProvenanceReceipt: async ({ cwd, output, receipt, root }: { cwd?: string; output: string; receipt: unknown; root: string }) => {
    const p = await provenanceModule();
    const saved = process.cwd();
    if (cwd !== undefined) process.chdir(cwd);
    try { return p.writeGithubProvenanceReceipt(output, receipt, root); } catch (error) { return `THREW:${(error as Error).message}`; } finally { process.chdir(saved); }
  },
  // Slice 4c Task 16: repairWithCodex (src/codex-loop.ts) with scripted
  // runners. a.env replaces process.env entries (PATH with the case's
  // recorders, TMPDIR, ...) and a.cwd is the working directory for the call.
  //   a.codex: "none" (no runner), {kind:"result", exitCode, stdout, stderr,
  //     edits} (edits are applied in the worktree before returning) or
  //     {kind:"throw", message}.
  //   a.verifier: "none", "frozen" (createFrozenWitnessRepairVerifier with
  //     the scripted a.sandbox runner: a list of PASS/FAIL/ERROR answered in
  //     call order, the last repeating; "native" gives no runner, so Docker
  //     runs from PATH) or "scripted" (a.verdicts, in call order, the last
  //     repeating; a "THROW:<message>" string rejects; removeFrozen and
  //     removeAdminDir break the bundle or the worktree before the verdict).
  //   a.defaultOutput: the CLI's default output name (Date.now()).
  // Returns the result (JSON.stringify, or "THREW:<message>"), the seeded
  // {ticks, uuids} counters before and after, and the scripted calls.
  repairWithCodex: async (a: {
    bundle: string; expectRoot: string; repository: string; output: string; cwd: string; env: Record<string, string>;
    invariant?: string; withCodex: boolean; instructionsOnly: boolean; keepWorktree: boolean;
    verify: { kind: "real" } | { kind: "valid"; rootDigest: string | null } | { kind: "invalid"; errors: string[] };
    codex: "none" | { kind: "result"; exitCode: number | null; stdout: string; stderr: string; edits?: { path: string; content?: string; mode?: number; remove?: boolean }[] } | { kind: "throw"; message: string };
    verifier: "none" | "frozen" | "scripted";
    sandbox: "native" | string[];
    verdicts?: (({ ok: boolean; detail: string; executionId?: string; removeAdminDir?: boolean; removeFrozen?: boolean }) | string)[];
    defaultOutput?: boolean;
  }) => {
    // repairWithCodex imports these lazily; tsx reads the seeded Date while it
    // loads a module, so load them before the counters are read.
    await Promise.all([
      "../../src/prevention-from-artifacts.js", "../../src/prevention-proof.js", "../../src/agents-md.js", "../../src/sandbox.js",
      "../../src/safe-overlay.js", "../../src/witness-lock.js", "../../src/repair-worktree.js", "../../src/codex-loop.js"
    ].map((module) => import(module)));
    const counters = () => ({ ...(globalThis as unknown as { __faultlineSeedCounters: { ticks: number; uuids: number } }).__faultlineSeedCounters });
    const { repairWithCodex, createFrozenWitnessRepairVerifier } = await import("../../src/codex-loop.js");
    const { verifyGitInvestigationProofBundle } = await import("../../src/git-proof-bundle.js");
    const { formatWitnessResult } = await import("../../src/witness-result.js");
    const { mkdirSync, rmSync, writeFileSync, chmodSync, readFileSync } = await import("node:fs");
    const codexCalls: unknown[] = [];
    const sandboxCalls: unknown[] = [];
    const codex = a.codex;
    const runner = codex === "none" ? undefined : {
      async run(args: readonly string[], options: { cwd: string; input?: string }) {
        codexCalls.push({ args, cwd: options.cwd, input: options.input ?? null });
        if (codex.kind === "throw") throw new Error(codex.message);
        for (const edit of codex.edits ?? []) {
          const target = nodePath.isAbsolute(edit.path) ? edit.path : nodePath.join(options.cwd, edit.path);
          if (edit.remove) { rmSync(target, { force: true, recursive: true }); continue; }
          mkdirSync(nodePath.dirname(target), { recursive: true });
          writeFileSync(target, edit.content ?? "");
          if (edit.mode !== undefined) chmodSync(target, edit.mode);
        }
        return { exitCode: codex.exitCode, stdout: codex.stdout, stderr: codex.stderr };
      }
    };
    let sandboxNext = 0;
    const script = a.sandbox === "native" ? undefined : a.sandbox;
    const sandboxRunner = script === undefined ? undefined : {
      async run(invocation: { arguments: readonly string[]; cwd: string; containerName?: string }) {
        const kind = script[Math.min(sandboxNext++, script.length - 1)]!;
        sandboxCalls.push({ kind, arguments: invocation.arguments, cwd: invocation.cwd, containerName: invocation.containerName ?? null });
        if (kind === "ERROR") throw new Error("scripted sandbox failure");
        if (kind === "FAIL") return { exitCode: 1, stdout: `state=bad\n${formatWitnessResult("PREDICATE_FAIL")}\n`, stderr: "witness failed" };
        return { exitCode: 0, stdout: `state=good\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
      }
    };
    let verdictNext = 0;
    const verifyCandidate = a.verifier === "none" ? undefined
      : a.verifier === "frozen" ? createFrozenWitnessRepairVerifier(sandboxRunner === undefined ? undefined : { runner: sandboxRunner })
      : async (context: { worktreePath: string }) => {
        const entry = a.verdicts![Math.min(verdictNext++, a.verdicts!.length - 1)]!;
        if (typeof entry === "string") throw new Error(entry.slice("THROW:".length));
        const { removeAdminDir, removeFrozen, ...verdict } = entry;
        // removeFrozen: the bundle's witness/frozen.json goes away, so a later read of it fails.
        if (removeFrozen) rmSync(nodePath.join(a.bundle, "witness", "frozen.json"), { force: true });
        if (removeAdminDir) {
          // Delete the linked worktree's administrative directory, so the later cleanup fails.
          const pointer = readFileSync(nodePath.join(context.worktreePath, ".git"), "utf8").trim();
          rmSync(pointer.slice("gitdir: ".length), { recursive: true, force: true });
        }
        return verdict;
      };
    const savedEnv: Record<string, string | undefined> = {};
    for (const [key, value] of Object.entries(a.env)) { savedEnv[key] = process.env[key]; process.env[key] = value; }
    const savedCwd = process.cwd();
    process.chdir(a.cwd);
    // Modes are compared under umask 0 (the Go side runs the same way).
    const savedUmask = process.umask(0);
    const before = counters();
    let result: string;
    try {
      // The CLI's default output name reads Date.now() (cli-app.ts:2688).
      const output = a.defaultOutput ? nodePath.resolve(nodePath.join(".faultline", "repairs", `repair-${Date.now()}`)) : a.output;
      result = JSON.stringify(await repairWithCodex({
        bundleDirectory: a.bundle, expectRoot: a.expectRoot, repository: a.repository, outputDirectory: output,
        ...(a.invariant === undefined ? {} : { invariant: a.invariant }),
        withCodex: a.withCodex, instructionsOnly: a.instructionsOnly, keepWorktree: a.keepWorktree,
        ...(runner === undefined ? {} : { runner }),
        ...(verifyCandidate === undefined ? {} : { verifyCandidate }),
        verifyBundle: (directory, expectRoot) => {
          if (a.verify.kind === "real") {
            const v = verifyGitInvestigationProofBundle(directory, expectRoot);
            return { valid: v.valid, errors: [...v.errors], rootDigest: v.rootDigest };
          }
          if (a.verify.kind === "valid") return { valid: true, errors: [], rootDigest: a.verify.rootDigest };
          return { valid: false, errors: a.verify.errors, rootDigest: null };
        }
      }));
    } catch (error) {
      result = `THREW:${(error as Error).message}`;
    } finally {
      process.chdir(savedCwd);
      process.umask(savedUmask);
      for (const [key, value] of Object.entries(savedEnv)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
    }
    return { result, before, after: counters(), codexCalls, sandboxCalls };
  },
};

// provenanceModule is dist/github-provenance.js with its
// loadVerifiedGitProofView import replaced by a per-directory memo (the
// view, or the error it threw), written next to it as
// dist/.difftest-github-provenance-memo-<pid>.js (dist/ is git-ignored),
// imported once and removed.
let provenanceMemo: Promise<any> | undefined;
function provenanceModule(): Promise<any> {
  provenanceMemo ??= (async () => {
    const { unlinkSync, writeFileSync } = await import("node:fs");
    const { pathToFileURL } = await import("node:url");
    const dist = nodePath.join(process.cwd(), "dist");
    const source = cwReadFile(nodePath.join(dist, "github-provenance.js"), "utf8");
    const line = 'import { loadVerifiedGitProofView } from "./git-proof-view.js";';
    if (!source.includes(line)) throw new Error("loadVerifiedGitProofView import not found in dist/github-provenance.js");
    const memo = `import { loadVerifiedGitProofView as loadUncached } from "./git-proof-view.js";
const views = new Map();
function loadVerifiedGitProofView(directory) {
  if (!views.has(directory)) {
    try { views.set(directory, { view: loadUncached(directory) }); } catch (error) { views.set(directory, { error }); }
  }
  const entry = views.get(directory);
  if (entry.error !== undefined) throw entry.error;
  return entry.view;
}`;
    const file = nodePath.join(dist, `.difftest-github-provenance-memo-${process.pid}.js`);
    writeFileSync(file, source.replace(line, memo));
    try { return await import(pathToFileURL(file).href); } finally { unlinkSync(file); }
  })();
  return provenanceMemo;
}

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
