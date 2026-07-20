import { createHash, randomUUID } from "node:crypto";
import {
  existsSync,
  lstatSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync
} from "node:fs";
import { tmpdir } from "node:os";
import { join, relative, resolve, sep } from "node:path";
import { spawnSync } from "node:child_process";
import { digestJson, sha256 } from "./canonical.js";
import { DOCTOR_SAFE_GIT_CONFIG } from "./doctor.js";
import {
  GitInvestigationResultSchema,
  STABLE_EXECUTION_COUNT,
  type GitInvestigationResult,
  type GitInvestigationRunFact,
  type StableGitState
} from "./git-investigation.js";
import { verifyGitInvestigationProofBundle } from "./git-proof-bundle.js";
import {
  writePreventionProof,
  type PreventionProofWriteInput,
  type WrittenPreventionProof
} from "./prevention-proof.js";
import { FrozenWitnessSchema, type FrozenWitness } from "./witness-lock.js";

export type PreventionExportFailure = {
  readonly ok: false;
  readonly reasons: readonly string[];
};

export type PreventionExportSuccess = {
  readonly ok: true;
  readonly written: WrittenPreventionProof;
};

export type PreventionExportResult = PreventionExportFailure | PreventionExportSuccess;

export type RepairedPreventionRun = {
  readonly runId: string;
  readonly executionId: string;
  readonly commit: string;
  readonly tree: string;
  readonly verdict: "PASS";
  readonly witnessDigest: string;
  readonly environmentDigest: string;
  readonly executionTrust: "NATIVE_DOCKER";
  readonly executionKind: "EXECUTED";
};

function fail(reasons: string[]): PreventionExportFailure {
  return { ok: false, reasons };
}

function readInvestigation(bundleDirectory: string): GitInvestigationResult {
  return GitInvestigationResultSchema.parse(
    JSON.parse(readFileSync(join(resolve(bundleDirectory), "investigation.json"), "utf8"))
  );
}

function readFrozenWitness(bundleDirectory: string): FrozenWitness {
  return FrozenWitnessSchema.parse(
    JSON.parse(readFileSync(join(resolve(bundleDirectory), "witness", "frozen.json"), "utf8"))
  );
}

function requireHomogeneousEnvironmentDigest(investigation: GitInvestigationResult): string | null {
  if (investigation.environment.homogeneity !== "HOMOGENEOUS") return null;
  if (investigation.environment.distinctDigests.length !== 1) return null;
  return investigation.environment.distinctDigests[0] ?? null;
}

function assertNativeDockerPassFail(
  run: GitInvestigationRunFact,
  expected: { commit: string; tree: string; verdict: "PASS" | "FAIL"; witnessDigest: string; frozenDigest: string }
): string | null {
  if (run.commit !== expected.commit || run.tree !== expected.tree) {
    return `run ${run.runId} commit/tree does not match stable state`;
  }
  if (run.witnessDigest !== expected.witnessDigest || run.frozenDigest !== expected.frozenDigest) {
    return `run ${run.runId} witness digests do not match the frozen witness`;
  }
  if (run.result.executor !== "NATIVE_DOCKER") {
    return `run ${run.runId} is not NATIVE_DOCKER`;
  }
  if (run.result.kind !== "DOCKER_ISOLATED") {
    return `run ${run.runId} is not DOCKER_ISOLATED`;
  }
  if (run.result.verdict !== expected.verdict) {
    return `run ${run.runId} verdict ${run.result.verdict} !== ${expected.verdict}`;
  }
  if (expected.verdict === "PASS" && run.result.reason !== "PREDICATE_PASS") {
    return `run ${run.runId} PASS reason must be PREDICATE_PASS`;
  }
  if (expected.verdict === "FAIL" && run.result.reason !== "PREDICATE_FAIL") {
    return `run ${run.runId} FAIL reason must be PREDICATE_FAIL`;
  }
  return null;
}

type BoundExecutedState = {
  commit: string;
  tree: string;
  witnessDigest: string;
  environmentDigest: string;
  executionTrust: "NATIVE_DOCKER";
  executionKind: "EXECUTED";
  distinctExecutionCount: number;
  runIds: [string, string, string];
};

function bindStableState(options: {
  state: StableGitState;
  investigation: GitInvestigationResult;
  frozen: FrozenWitness;
  environmentDigest: string;
  expectedVerdict: "PASS" | "FAIL";
}): { state: BoundExecutedState; error?: undefined } | { state?: undefined; error: string } {
  const { state, investigation, frozen, environmentDigest, expectedVerdict } = options;
  if (state.verdict !== expectedVerdict) {
    return {
      error: `stable state at ${state.commit} has verdict ${state.verdict}, expected ${expectedVerdict}`
    };
  }
  if (state.runIds.length !== STABLE_EXECUTION_COUNT) {
    return { error: `stable state ${state.commit} lacks ${STABLE_EXECUTION_COUNT} run IDs` };
  }
  for (const runId of state.runIds) {
    const run = investigation.runs.find((entry) => entry.runId === runId);
    if (!run) {
      return { error: `run ID ${runId} is not present in the proof bundle` };
    }
    const mismatch = assertNativeDockerPassFail(run, {
      commit: state.commit,
      tree: state.tree,
      verdict: expectedVerdict,
      witnessDigest: frozen.witnessDigest,
      frozenDigest: frozen.frozenDigest
    });
    if (mismatch) return { error: mismatch };
  }
  return {
    state: {
      commit: state.commit,
      tree: state.tree,
      witnessDigest: frozen.witnessDigest,
      environmentDigest,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED",
      distinctExecutionCount: STABLE_EXECUTION_COUNT,
      runIds: [state.runIds[0]!, state.runIds[1]!, state.runIds[2]!]
    }
  };
}

/**
 * Build a grounded prevention write input from a verified Git proof bundle plus
 * three repaired-state NATIVE_DOCKER run bindings and the exact repair patch.
 */
export function buildPreventionProofFromVerifiedArtifacts(options: {
  bundleDirectory: string;
  expectRoot: string;
  repaired: {
    commit: string;
    tree: string;
    runs: readonly RepairedPreventionRun[];
  };
  repairPatchDigest: string;
  repairBaseTree?: string;
  codexThreadId?: string;
  hardGuardArtifactDigests?: readonly string[];
}): { ok: true; input: PreventionProofWriteInput } | PreventionExportFailure {
  const bundleDirectory = resolve(options.bundleDirectory);
  const verification = verifyGitInvestigationProofBundle(bundleDirectory, options.expectRoot);
  if (!verification.valid || verification.rootDigest === null) {
    return fail([
      "Proof bundle verification failed",
      ...verification.errors
    ]);
  }

  let investigation: GitInvestigationResult;
  let frozen: FrozenWitness;
  try {
    investigation = readInvestigation(bundleDirectory);
    frozen = readFrozenWitness(bundleDirectory);
  } catch (error) {
    return fail([error instanceof Error ? error.message : String(error)]);
  }

  if (!investigation.proof.isProof || investigation.proof.executionTrust !== "NATIVE_DOCKER") {
    return fail(["Prevention export requires a Docker-isolated proof-grade Git investigation."]);
  }

  const environmentDigest = requireHomogeneousEnvironmentDigest(investigation);
  if (environmentDigest === null) {
    return fail(["Prevention export requires a HOMOGENEOUS environment fingerprint digest across states."]);
  }

  const introduction = investigation.transitions.find((transition) => transition.kind === "PASS_TO_FAIL");
  if (!introduction) {
    return fail(["Prevention export requires a stable PASS_TO_FAIL transition in the proof bundle."]);
  }

  const lastGood = bindStableState({
    state: introduction.before,
    investigation,
    frozen,
    environmentDigest,
    expectedVerdict: "PASS"
  });
  if (lastGood.error !== undefined || lastGood.state === undefined) {
    return fail([lastGood.error ?? "last-good binding failed"]);
  }
  const firstBad = bindStableState({
    state: introduction.after,
    investigation,
    frozen,
    environmentDigest,
    expectedVerdict: "FAIL"
  });
  if (firstBad.error !== undefined || firstBad.state === undefined) {
    return fail([firstBad.error ?? "first-bad binding failed"]);
  }

  if (options.repaired.runs.length !== STABLE_EXECUTION_COUNT) {
    return fail([`Repaired state requires exactly ${STABLE_EXECUTION_COUNT} NATIVE_DOCKER run bindings.`]);
  }
  const repairedRunIds = options.repaired.runs.map((run) => run.runId);
  if (new Set(repairedRunIds).size !== STABLE_EXECUTION_COUNT) {
    return fail(["Repaired run IDs must be distinct."]);
  }
  for (const run of options.repaired.runs) {
    if (run.commit !== options.repaired.commit || run.tree !== options.repaired.tree) {
      return fail([`Repaired run ${run.runId} commit/tree does not match the repaired state.`]);
    }
    if (run.verdict !== "PASS" || run.executionTrust !== "NATIVE_DOCKER" || run.executionKind !== "EXECUTED") {
      return fail([`Repaired run ${run.runId} is not a NATIVE_DOCKER EXECUTED PASS.`]);
    }
    if (run.witnessDigest !== frozen.witnessDigest) {
      return fail([`Repaired run ${run.runId} witness digest does not match the frozen witness.`]);
    }
    if (run.environmentDigest !== environmentDigest) {
      return fail([`Repaired run ${run.runId} environment digest does not match the proof-bundle environment.`]);
    }
  }

  if (
    options.repaired.commit === introduction.before.commit
    || options.repaired.commit === introduction.after.commit
  ) {
    return fail(["Repaired commit must be distinct from last-good and first-bad commits."]);
  }

  const repairBaseTree = options.repairBaseTree ?? introduction.after.tree;
  if (repairBaseTree !== introduction.after.tree) {
    return fail(["repairBaseTree must equal the first-bad state's tree (repair base)."]);
  }

  return {
    ok: true,
    input: {
      originalProofRoot: verification.rootDigest,
      frozenWitnessDigest: frozen.witnessDigest,
      investigationDigest: digestJson(investigation),
      lastGood: lastGood.state,
      firstBad: firstBad.state,
      repaired: {
        commit: options.repaired.commit,
        tree: options.repaired.tree,
        witnessDigest: frozen.witnessDigest,
        environmentDigest,
        executionTrust: "NATIVE_DOCKER",
        executionKind: "EXECUTED",
        distinctExecutionCount: STABLE_EXECUTION_COUNT,
        runIds: repairedRunIds
      },
      repairPatchDigest: options.repairPatchDigest,
      repairBaseTree,
      grounding: "VERIFIED",
      ...(options.codexThreadId === undefined ? {} : { codexThreadId: options.codexThreadId }),
      ...(options.hardGuardArtifactDigests === undefined
        ? {}
        : { hardGuardArtifactDigests: [...options.hardGuardArtifactDigests] }),
      repairedRunBindings: options.repaired.runs.map((run) => ({
        runId: run.runId,
        executionId: run.executionId,
        commit: run.commit,
        tree: run.tree,
        verdict: "PASS" as const,
        witnessDigest: run.witnessDigest,
        environmentDigest: run.environmentDigest,
        executionTrust: "NATIVE_DOCKER" as const,
        executionKind: "EXECUTED" as const
      }))
    }
  };
}

export function writePreventionProofFromVerifiedArtifacts(options: {
  bundleDirectory: string;
  expectRoot: string;
  outputDirectory: string;
  repaired: {
    commit: string;
    tree: string;
    runs: readonly RepairedPreventionRun[];
  };
  repairPatchDigest: string;
  repairBaseTree?: string;
  codexThreadId?: string;
  hardGuardArtifactDigests?: readonly string[];
}): PreventionExportResult {
  const built = buildPreventionProofFromVerifiedArtifacts(options);
  if (!built.ok) return built;
  try {
    const written = writePreventionProof(options.outputDirectory, built.input);
    return { ok: true, written };
  } catch (error) {
    return fail([error instanceof Error ? error.message : String(error)]);
  }
}

export function digestRepairPatchFile(patchPath: string): string {
  if (!existsSync(patchPath)) {
    throw new Error(`Repair patch does not exist: ${patchPath}`);
  }
  return `sha256:${sha256(readFileSync(patchPath))}`;
}

/**
 * Create a commit object for the current dirty repaired worktree without
 * advancing the user's branch (orphan commit-tree on top of the repair base).
 *
 * Never uses host-side `git add` or `git status` (both can invoke clean filters).
 * Walks the worktree on disk, hashes bytes via `hash-object --stdin`, and builds
 * a temporary index under hardened Git config/env.
 */
export function commitRepairedWorktreeState(worktreePath: string, baseCommit: string): {
  commit: string;
  tree: string;
} {
  const root = resolve(worktreePath);
  const temporaryIndexPath = join(tmpdir(), `faultline-repair-${randomUUID()}.index`);
  const nullDevice = process.platform === "win32" ? "NUL" : "/dev/null";
  const hardenedEnv = (): NodeJS.ProcessEnv => ({
    PATH: process.env.PATH ?? "",
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: nullDevice,
    GIT_ATTR_NOSYSTEM: "1",
    GIT_TERMINAL_PROMPT: "0",
    GIT_OPTIONAL_LOCKS: "0",
    GIT_NO_REPLACE_OBJECTS: "1",
    GIT_LFS_SKIP_SMUDGE: "1",
    GIT_ALLOW_PROTOCOL: "none",
    GIT_INDEX_FILE: temporaryIndexPath,
    ...(process.platform === "win32" && process.env.SystemRoot ? { SystemRoot: process.env.SystemRoot } : {}),
    ...(process.platform === "win32" && process.env.ComSpec ? { ComSpec: process.env.ComSpec } : {}),
    ...(process.platform === "win32" && process.env.PATHEXT ? { PATHEXT: process.env.PATHEXT } : {})
  });

  const run = (args: string[]) => {
    const result = spawnSync(
      "git",
      [
        ...DOCTOR_SAFE_GIT_CONFIG,
        "-c", "core.attributesFile=/nonexistent/faultline-attributes",
        "-C",
        root,
        ...args
      ],
      {
        encoding: "utf8",
        shell: false,
        windowsHide: true,
        timeout: 120_000,
        maxBuffer: 32 * 1024 * 1024,
        env: hardenedEnv()
      }
    );
    if (result.error || result.status !== 0) {
      throw new Error(result.stderr || result.stdout || result.error?.message || `git ${args.join(" ")} failed`);
    }
    return (result.stdout ?? "").trim();
  };

  const hashBlob = (absolutePath: string): string => {
    const content = readFileSync(absolutePath);
    const hashResult = spawnSync(
      "git",
      [
        ...DOCTOR_SAFE_GIT_CONFIG,
        "-c", "core.attributesFile=/nonexistent/faultline-attributes",
        "-C",
        root,
        "hash-object",
        "-w",
        "--stdin"
      ],
      {
        encoding: "utf8",
        shell: false,
        windowsHide: true,
        timeout: 120_000,
        maxBuffer: 32 * 1024 * 1024,
        env: hardenedEnv(),
        input: content
      }
    );
    if (hashResult.error || hashResult.status !== 0) {
      throw new Error(
        hashResult.stderr || hashResult.stdout || hashResult.error?.message || `git hash-object --stdin failed for ${absolutePath}`
      );
    }
    const blob = (hashResult.stdout ?? "").trim();
    if (!/^[a-f0-9]{40}([a-f0-9]{24})?$/.test(blob)) {
      throw new Error(`git hash-object --stdin did not return a blob id for ${absolutePath}`);
    }
    return blob;
  };

  const listWorktreeFiles = (directory: string, out: string[]): void => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (entry.name === ".git") continue;
      const absolute = join(directory, entry.name);
      if (entry.isDirectory()) {
        listWorktreeFiles(absolute, out);
        continue;
      }
      if (!entry.isFile()) continue;
      out.push(absolute);
    }
  };

  try {
    run(["read-tree", baseCommit]);
    const indexed = new Set(
      run(["ls-files", "-z"]).split("\0").filter(Boolean).map((path) => path.replace(/\\/g, "/"))
    );
    const absoluteFiles: string[] = [];
    listWorktreeFiles(root, absoluteFiles);
    const seen = new Set<string>();
    for (const absolutePath of absoluteFiles) {
      const relativePath = relative(root, absolutePath).split(sep).join("/");
      if (relativePath === "" || relativePath.startsWith("..")) continue;
      const stat = lstatSync(absolutePath);
      if (stat.isSymbolicLink() || !stat.isFile()) {
        throw new Error(`Repaired worktree path is not a regular file: ${relativePath}`);
      }
      const mode = (stat.mode & 0o111) !== 0 ? "100755" : "100644";
      const blob = hashBlob(absolutePath);
      run(["update-index", "--add", "--cacheinfo", `${mode},${blob},${relativePath}`]);
      seen.add(relativePath);
    }
    for (const indexedPath of indexed) {
      if (!seen.has(indexedPath)) {
        run(["update-index", "--force-remove", "--", indexedPath]);
      }
    }

    const tree = run(["write-tree"]);
    const commit = run([
      "commit-tree",
      tree,
      "-p",
      baseCommit,
      "-m",
      "faultline: repaired prevention state"
    ]);
    if (!/^[a-f0-9]{40}([a-f0-9]{24})?$/.test(commit)) {
      throw new Error("git commit-tree did not return a Git object id for the repaired state.");
    }
    return { commit, tree };
  } finally {
    rmSync(temporaryIndexPath, { force: true });
  }
}

/**
 * Run the frozen-witness Docker verifier three times on a repaired worktree and
 * produce content-addressed repaired run bindings for prevention export.
 */
export async function collectRepairedPreventionRuns(options: {
  worktreePath: string;
  bundleDirectory: string;
  repairedCommit: string;
  repairedTree: string;
  verifyOnce: () => Promise<{ ok: boolean; detail: string; executionId?: string }>;
}): Promise<{ ok: true; runs: RepairedPreventionRun[] } | PreventionExportFailure> {
  const frozen = readFrozenWitness(options.bundleDirectory);
  const investigation = readInvestigation(options.bundleDirectory);
  const environmentDigest = requireHomogeneousEnvironmentDigest(investigation);
  if (environmentDigest === null) {
    return fail(["Cannot collect repaired runs without a homogeneous environment digest."]);
  }

  const runs: RepairedPreventionRun[] = [];
  for (let attempt = 1; attempt <= STABLE_EXECUTION_COUNT; attempt += 1) {
    const verdict = await options.verifyOnce();
    if (!verdict.ok) {
      return fail([
        `Repaired-state execution ${attempt}/${STABLE_EXECUTION_COUNT} failed: ${verdict.detail}`
      ]);
    }
    const executionId = verdict.executionId;
    if (typeof executionId !== "string" || !/^sha256:[a-f0-9]{64}$/.test(executionId)) {
      return fail([
        `Repaired-state execution ${attempt}/${STABLE_EXECUTION_COUNT} omitted a content-bound executionId; refusing PREVENTION_VERIFIED placeholder synthesis.`
      ]);
    }
    const unsigned = {
      schemaVersion: "faultline.prevention-repaired-run.v1",
      attempt,
      commit: options.repairedCommit,
      tree: options.repairedTree,
      witnessDigest: frozen.witnessDigest,
      environmentDigest,
      executionId,
      detail: verdict.detail
    };
    runs.push({
      runId: digestJson(unsigned),
      executionId,
      commit: options.repairedCommit,
      tree: options.repairedTree,
      verdict: "PASS",
      witnessDigest: frozen.witnessDigest,
      environmentDigest,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED"
    });
  }
  if (new Set(runs.map((run) => run.runId)).size !== STABLE_EXECUTION_COUNT) {
    return fail(["Collected repaired run digests were not distinct."]);
  }
  return { ok: true, runs };
}

/** Digest hard-enforcement recommendation strings from a repair brief when present. */
export function hardGuardDigestsFromStrings(values: readonly string[]): string[] {
  return [...new Set(values.map((value) => `sha256:${sha256(value)}`))].sort((left, right) => left.localeCompare(right));
}

export function writeHardGuardDigestSidecar(directory: string, digests: readonly string[]): string {
  const path = join(directory, "hard-guard-digests.json");
  writeFileSync(path, `${JSON.stringify({ digests }, null, 2)}\n`, "utf8");
  return `sha256:${createHash("sha256").update(readFileSync(path)).digest("hex")}`;
}
