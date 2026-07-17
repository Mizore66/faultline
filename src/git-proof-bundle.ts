import { spawnSync } from "node:child_process";
import {
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  writeFileSync
} from "node:fs";
import { randomUUID } from "node:crypto";
import { tmpdir } from "node:os";
import { dirname, isAbsolute, join, parse, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson, sha256 } from "./canonical.js";
import {
  GIT_INVESTIGATION_SCHEMA_VERSION,
  STABLE_EXECUTION_COUNT,
  GitCommitStateSchema,
  GitInvestigationResultSchema,
  GitInvestigationRunFactSchema,
  StableGitTransitionSchema,
  type GitInvestigationResult,
  type GitInvestigationRunFact,
  type StableGitState,
  type StableGitTransition
} from "./git-investigation.js";
import { assertSafeProofOutput } from "./proof-bundle.js";
import {
  FrozenWitnessSchema,
  verifyFrozenWitnessRecord,
  type FrozenWitness
} from "./witness-lock.js";
import {
  CodexLifecycleLedgerSchema,
  verifyCodexLifecycleLedger,
  type CodexLifecycleLedger,
  type CodexTransport
} from "./ledger.js";
import { validateSandboxPlanAudit } from "./sandbox.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";

/** A portable, Git-native proof package for one completed FaultLine investigation. */
export const GIT_PROOF_BUNDLE_SCHEMA_VERSION = "faultline.git-proof-bundle.v1" as const;
export const GIT_PROOF_SOURCE_SCHEMA_VERSION = "faultline.git-proof-source.v1" as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const GIT_OBJECT_ID = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/;
const DIGEST_PINNED_IMAGE = /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/;
const BUNDLE_HEAD_REF = "refs/faultline/portable-descendant" as const;
const MAX_SOURCE_ARTIFACT_BYTES = 128 * 1024 * 1024;
const MAX_GIT_OUTPUT_BYTES = 4 * 1024 * 1024;
const MAX_HASH_CATALOG_BYTES = 4 * 1024 * 1024;
const MAX_GIT_PROOF_ARTIFACTS = 2_048;
const MAX_GIT_PROOF_TOTAL_BYTES = 512 * 1024 * 1024;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const TimestampSchema = z.string().datetime({ offset: true });

function isSafeRelativeArtifactPath(value: string): boolean {
  if (!value || value.startsWith("/") || value.includes("\\") || value.includes("\0")) return false;
  const parts = value.split("/");
  return parts.every((part) => part.length > 0 && part !== "." && part !== "..");
}

const ArtifactPathSchema = z.string().min(1).max(1_024).refine(isSafeRelativeArtifactPath, "artifact path must be a safe POSIX-relative path");

const RunArtifactSchema = z.object({
  runId: DigestSchema,
  path: ArtifactPathSchema
}).strict();

const TransitionArtifactSchema = z.object({
  index: z.number().int().nonnegative(),
  path: ArtifactPathSchema
}).strict();

const LifecycleUnboundSchema = z.object({
  status: z.literal("UNBOUND"),
  limitation: z.literal("No caller-supplied Codex lifecycle ledger is bound to this Git investigation package.")
}).strict();

const LifecycleBindingFields = z.object({
  path: z.literal("lifecycle/ledger.json"),
  ledgerDigest: DigestSchema,
  headHash: DigestSchema,
  transport: z.enum(["CODEX_CLI", "CODEX_APP", "SIDE_CAR"]),
  /** Only checkpoints that exactly match a state in this investigation are listed. */
  checkpointBindings: z.array(z.object({
    sequence: z.number().int().positive(),
    stateIndex: z.number().int().nonnegative(),
    checkpointDigest: DigestSchema
  }).strict()).min(1)
}).strict();

/**
 * `BOUND` is accepted only to verify packages written before FaultLine
 * distinguished a descendant-only attachment from coverage of every replayed
 * state. New packages always declare their actual coverage explicitly.
 */
const LifecycleLegacyBoundSchema = LifecycleBindingFields.extend({ status: z.literal("BOUND") }).strict();
const LifecyclePartiallyBoundSchema = LifecycleBindingFields.extend({ status: z.literal("PARTIALLY_BOUND") }).strict();
const LifecycleFullyBoundSchema = LifecycleBindingFields.extend({ status: z.literal("FULLY_BOUND") }).strict();

const LifecycleBindingSchema = z.discriminatedUnion("status", [
  LifecycleUnboundSchema,
  LifecycleLegacyBoundSchema,
  LifecyclePartiallyBoundSchema,
  LifecycleFullyBoundSchema
]);

export const GitProofSourceMetadataSchema = z.object({
  schemaVersion: z.literal(GIT_PROOF_SOURCE_SCHEMA_VERSION),
  objectFormat: z.enum(["sha1", "sha256"]),
  ancestor: GitCommitStateSchema,
  descendant: GitCommitStateSchema,
  bundle: z.object({
    path: z.literal("source/descendant.bundle"),
    digest: DigestSchema,
    bytes: z.number().int().positive(),
    headRef: z.literal(BUNDLE_HEAD_REF),
    headCommit: z.string().regex(GIT_OBJECT_ID, "expected Git object id")
  }).strict(),
  rangePatch: z.object({
    path: z.literal("source/range.patch"),
    digest: DigestSchema,
    bytes: z.number().int().nonnegative(),
    format: z.literal("git-diff --binary --full-index --no-ext-diff --no-textconv --no-renames")
  }).strict()
}).strict();

export const GitProofBundleManifestSchema = z.object({
  schemaVersion: z.literal(GIT_PROOF_BUNDLE_SCHEMA_VERSION),
  generatedAt: TimestampSchema,
  integrityScope: z.literal("complete-declared-file-set"),
  investigationDigest: DigestSchema,
  frozenDigest: DigestSchema,
  witnessDigest: DigestSchema,
  lifecycle: LifecycleBindingSchema,
  resolvedRange: z.object({
    ancestor: GitCommitStateSchema,
    descendant: GitCommitStateSchema
  }).strict(),
  artifacts: z.object({
    investigation: z.literal("investigation.json"),
    frozenWitness: z.literal("witness/frozen.json"),
    runs: z.array(RunArtifactSchema),
    transitions: z.array(TransitionArtifactSchema),
    sourceMetadata: z.literal("source/metadata.json"),
    gitBundle: z.literal("source/descendant.bundle"),
    rangePatch: z.literal("source/range.patch"),
    verification: z.literal("VERIFY.md"),
    lifecycleLedger: z.literal("lifecycle/ledger.json").optional()
  }).strict()
}).strict();

export type GitProofSourceMetadata = z.infer<typeof GitProofSourceMetadataSchema>;
export type GitProofBundleManifest = z.infer<typeof GitProofBundleManifestSchema>;
export type GitProofBundleExternalRootStatus = "NOT_PROVIDED" | "MATCH" | "MISMATCH";

export interface GitProofBundleWriteOptions {
  /** Override only for a controlled embedding or an isolated test root. */
  readonly proofRoot?: string;
  /** Pins a reproducible manifest timestamp for controlled use and tests. */
  readonly generatedAt?: string;
  /**
   * Optional caller-supplied lifecycle record. FaultLine verifies its hash
   * chain and only states a binding when its clean checkpoints match an
   * investigated Git state. It never claims native Codex interception.
   */
  readonly lifecycleLedger?: CodexLifecycleLedger;
}

export interface WrittenGitProofBundle {
  readonly directory: string;
  readonly manifestDigest: string;
  readonly rootDigest: string;
  readonly manifest: GitProofBundleManifest;
}

export interface GitProofBundleVerification {
  readonly valid: boolean;
  readonly checkedFiles: number;
  readonly errors: readonly string[];
  readonly rootDigest: string | null;
  readonly externalRootStatus: GitProofBundleExternalRootStatus;
  readonly manifest?: GitProofBundleManifest;
}

export function defaultGitProofRoot(): string {
  return resolve(".faultline", "git-proof-bundles");
}

function digestBytes(bytes: Buffer | string): string {
  return `sha256:${sha256(bytes)}`;
}

function jsonBytes(value: unknown): Buffer {
  return Buffer.from(`${canonicalJson(value)}\n`, "utf8");
}

function sameCanonical(left: unknown, right: unknown): boolean {
  return canonicalJson(left) === canonicalJson(right);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function safeArtifactPath(root: string, artifact: string): string {
  if (!isSafeRelativeArtifactPath(artifact)) throw new Error(`Unsafe artifact path: ${artifact}`);
  const destination = resolve(root, artifact);
  const nested = relative(root, destination);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error(`Artifact path escapes its bundle: ${artifact}`);
  }
  return destination;
}

/** Refuse links and device files before any bundle reader follows a path. */
function assertNoLinksOrSpecialFiles(directory: string): void {
  const stat = lstatSync(directory);
  if (stat.isSymbolicLink() || !stat.isDirectory()) {
    throw new Error(`Git proof bundle must be a real directory: ${directory}`);
  }
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const child = join(directory, entry.name);
    const childStat = lstatSync(child);
    if (childStat.isSymbolicLink() || (!childStat.isDirectory() && !childStat.isFile())) {
      throw new Error(`Git proof bundle contains a symbolic link or special file: ${child}`);
    }
    if (childStat.isDirectory()) assertNoLinksOrSpecialFiles(child);
  }
}

/** Create every parent one segment at a time, never traversing a link. */
function ensureRealDirectoryTree(directory: string): void {
  const absolute = resolve(directory);
  const parsed = parse(absolute);
  const suffix = relative(parsed.root, absolute);
  const parts = suffix ? suffix.split(/[\\/]+/).filter(Boolean) : [];
  let current = parsed.root;
  if (existsSync(current)) {
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) throw new Error(`Git proof root is not a real directory: ${current}`);
    current = safeCurrent;
  }
  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) {
      throw new Error(`Git proof root cannot traverse a symbolic link or non-directory: ${current}`);
    }
    current = safeCurrent;
  }
}

function isNested(root: string, candidate: string): boolean {
  const nested = relativeTrustedSystemPath(root, candidate);
  return Boolean(nested) && !nested.startsWith("..") && !isAbsolute(nested);
}

/**
 * Git proof packages are intentionally write-once. A rerun must use a new
 * directory, preventing a recorder from replacing a real incident package.
 */
function prepareFreshOutput(outputDirectory: string, proofRoot: string): string {
  const root = resolve(proofRoot);
  const output = resolve(outputDirectory);
  if (!isNested(root, output)) throw new Error(`Git proof bundle output must be a child directory of ${root}`);
  ensureRealDirectoryTree(root);
  ensureRealDirectoryTree(dirname(output));
  if (existsSync(output)) {
    throw new Error(`Git proof bundle output already exists and will not be replaced: ${output}`);
  }
  // Reuse the project-wide guard too: it rejects symlink traversal and keeps
  // this new bundle family under FaultLine's managed proof roots.
  return assertSafeProofOutput(output, root);
}

function assertRegularFile(path: string, label: string, maximumBytes = MAX_SOURCE_ARTIFACT_BYTES): number {
  const stat = lstatSync(path);
  if (stat.isSymbolicLink() || !stat.isFile()) throw new Error(`${label} must be a regular non-symlink file`);
  if (stat.size > maximumBytes) throw new Error(`${label} exceeds FaultLine's ${String(maximumBytes)} byte read limit`);
  return stat.size;
}

function readBoundedFile(path: string, label: string, maximumBytes = MAX_SOURCE_ARTIFACT_BYTES): Buffer {
  assertRegularFile(path, label, maximumBytes);
  return readFileSync(path);
}

function collectFiles(
  root: string,
  current = root,
  budget: { files: number; bytes: number } = { files: 0, bytes: 0 }
): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(current, { withFileTypes: true })) {
    const child = join(current, entry.name);
    const stat = lstatSync(child);
    if (stat.isSymbolicLink() || (!stat.isDirectory() && !stat.isFile())) {
      throw new Error(`Git proof bundle contains a symbolic link or special file: ${child}`);
    }
    if (stat.isDirectory()) files.push(...collectFiles(root, child, budget));
    else {
      if (stat.size > MAX_SOURCE_ARTIFACT_BYTES) throw new Error(`Git proof bundle artifact exceeds FaultLine's read limit: ${child}`);
      budget.files += 1;
      budget.bytes += stat.size;
      if (budget.files > MAX_GIT_PROOF_ARTIFACTS) throw new Error("Git proof bundle contains too many files to verify safely");
      if (budget.bytes > MAX_GIT_PROOF_TOTAL_BYTES) throw new Error("Git proof bundle exceeds FaultLine's total verification read limit");
      files.push(relative(root, child).replaceAll("\\", "/"));
    }
  }
  return files;
}

function writeArtifact(root: string, artifact: string, bytes: Buffer): void {
  const destination = safeArtifactPath(root, artifact);
  mkdirSync(dirname(destination), { recursive: true, mode: 0o700 });
  writeFileSync(destination, bytes, { mode: 0o600, flag: "wx" });
  assertRegularFile(destination, `artifact ${artifact}`);
}

function runArtifactPath(run: GitInvestigationRunFact): string {
  return `runs/${run.runId.slice("sha256:".length)}.json`;
}

function transitionArtifactPath(index: number): string {
  return `transitions/${String(index).padStart(4, "0")}.json`;
}

function requiredArtifactPaths(result: GitInvestigationResult, lifecycleBound = false): string[] {
  return [
    "manifest.json",
    "investigation.json",
    "witness/frozen.json",
    "source/metadata.json",
    "source/descendant.bundle",
    "source/range.patch",
    "VERIFY.md",
    ...(lifecycleBound ? ["lifecycle/ledger.json"] : []),
    ...result.runs.map(runArtifactPath),
    ...result.transitions.map((_, index) => transitionArtifactPath(index))
  ].sort((left, right) => left.localeCompare(right));
}

type GitCommandResult = {
  status: number | null;
  stdout: Buffer;
  stderr: Buffer;
  error?: Error;
};

function runGit(repository: string | undefined, argumentsList: readonly string[]): GitCommandResult {
  const argumentsWithRepository = repository === undefined ? [...argumentsList] : ["-C", repository, ...argumentsList];
  const result = spawnSync("git", argumentsWithRepository, {
    encoding: "buffer",
    windowsHide: true,
    maxBuffer: MAX_GIT_OUTPUT_BYTES
  });
  return {
    status: result.status,
    stdout: Buffer.isBuffer(result.stdout) ? result.stdout : Buffer.from(result.stdout ?? "", "utf8"),
    stderr: Buffer.isBuffer(result.stderr) ? result.stderr : Buffer.from(result.stderr ?? "", "utf8"),
    ...(result.error === undefined ? {} : { error: result.error })
  };
}

function gitBytes(repository: string | undefined, argumentsList: readonly string[], label: string): Buffer {
  const result = runGit(repository, argumentsList);
  if (result.error || result.status !== 0) {
    const detail = [result.stderr.toString("utf8").trim(), result.error?.message].filter(Boolean).join("; ");
    throw new Error(`${label} failed: ${detail || `exit ${String(result.status)}`}`);
  }
  return result.stdout;
}

function gitText(repository: string | undefined, argumentsList: readonly string[], label: string): string {
  return gitBytes(repository, argumentsList, label).toString("utf8").trim();
}

function resolveGitState(repository: string, commit: string): { commit: string; tree: string } {
  const resolvedCommit = gitText(repository, ["rev-parse", "--verify", "--end-of-options", `${commit}^{commit}`], "Git commit resolution");
  const tree = gitText(repository, ["rev-parse", "--verify", "--end-of-options", `${resolvedCommit}^{tree}`], "Git tree resolution");
  if (!GIT_OBJECT_ID.test(resolvedCommit) || !GIT_OBJECT_ID.test(tree)) {
    throw new Error("Git returned an invalid object identifier while writing a proof bundle.");
  }
  return { commit: resolvedCommit, tree };
}

function sourceRepositoryFor(result: GitInvestigationResult): { repository: string; ancestor: z.infer<typeof GitCommitStateSchema>; descendant: z.infer<typeof GitCommitStateSchema>; objectFormat: "sha1" | "sha256" } {
  if (result.repository === null || result.resolvedRange === null) {
    throw new Error("Only a completed Git investigation with a resolved source range can be bundled.");
  }
  const repository = resolve(gitText(result.repository, ["rev-parse", "--show-toplevel"], "Git repository resolution"));
  const ancestor = resolveGitState(repository, result.resolvedRange.ancestor.commit);
  const descendant = resolveGitState(repository, result.resolvedRange.descendant.commit);
  if (!sameCanonical(ancestor, { commit: result.resolvedRange.ancestor.commit, tree: result.resolvedRange.ancestor.tree })
    || !sameCanonical(descendant, { commit: result.resolvedRange.descendant.commit, tree: result.resolvedRange.descendant.tree })) {
    throw new Error("The current Git repository no longer contains the exact investigation endpoint objects.");
  }
  const ancestry = runGit(repository, ["merge-base", "--is-ancestor", ancestor.commit, descendant.commit]);
  if (ancestry.error || ancestry.status !== 0) throw new Error("The investigation ancestor is no longer an ancestor of its descendant in the source repository.");

  const listed = gitText(repository, [
    "rev-list",
    "--reverse",
    "--ancestry-path",
    "--end-of-options",
    `${ancestor.commit}..${descendant.commit}`
  ], "Git investigation range enumeration");
  const commits = ancestor.commit === descendant.commit ? [ancestor.commit] : [ancestor.commit, ...listed.split(/\r?\n/).filter(Boolean)];
  const states = commits.map((commit, index) => ({ index, ...resolveGitState(repository, commit) }));
  if (!sameCanonical(states, result.states)) {
    throw new Error("The source repository range does not exactly match the investigation state sequence.");
  }
  const objectFormat = gitText(repository, ["rev-parse", "--show-object-format"], "Git object-format resolution");
  if (objectFormat !== "sha1" && objectFormat !== "sha256") throw new Error(`Unsupported Git object format: ${objectFormat}`);
  return { repository, ancestor: result.resolvedRange.ancestor, descendant: result.resolvedRange.descendant, objectFormat };
}

function parseBundleHeads(bytes: Buffer): Array<{ commit: string; ref: string }> {
  const heads: Array<{ commit: string; ref: string }> = [];
  for (const line of bytes.toString("utf8").split(/\r?\n/).filter(Boolean)) {
    const match = /^(?<commit>[a-f0-9]{40}|[a-f0-9]{64})\s+(?<ref>\S+)$/.exec(line.trim());
    if (!match?.groups?.commit || !match.groups.ref) throw new Error(`Git bundle returned an invalid head line: ${line}`);
    heads.push({ commit: match.groups.commit, ref: match.groups.ref });
  }
  return heads;
}

function writePortableGitBundle(repository: string, descendant: string, destination: string): { bytes: Buffer; headCommit: string } {
  const temporaryBare = mkdtempSync(join(tmpdir(), "faultline-git-proof-source-"));
  try {
    gitBytes(undefined, ["clone", "--bare", "--shared", "--no-tags", "--quiet", repository, temporaryBare], "Temporary Git source clone");
    gitBytes(temporaryBare, ["update-ref", BUNDLE_HEAD_REF, descendant], "Temporary Git bundle ref creation");
    gitBytes(temporaryBare, ["bundle", "create", destination, BUNDLE_HEAD_REF], "Git bundle creation");
    assertRegularFile(destination, "Git bundle");
    const heads = parseBundleHeads(gitBytes(undefined, ["bundle", "list-heads", destination], "Git bundle head listing"));
    if (heads.length !== 1 || heads[0]?.ref !== BUNDLE_HEAD_REF || heads[0].commit !== descendant) {
      throw new Error("Git bundle did not expose exactly the expected descendant reference.");
    }
    gitBytes(temporaryBare, ["bundle", "verify", destination], "Git bundle verification");
    const bytes = readBoundedFile(destination, "Git bundle");
    if (bytes.length === 0 || bytes.length > MAX_SOURCE_ARTIFACT_BYTES) {
      throw new Error("Git bundle is empty or exceeds FaultLine's portable artifact limit.");
    }
    return { bytes, headCommit: heads[0].commit };
  } finally {
    rmSync(temporaryBare, { recursive: true, force: true });
  }
}

function binaryRangePatch(repository: string, ancestor: string, descendant: string): Buffer {
  const patch = gitBytes(repository, [
    "diff",
    "--binary",
    "--full-index",
    "--no-ext-diff",
    "--no-textconv",
    "--no-renames",
    "--end-of-options",
    ancestor,
    descendant
  ], "Git binary range patch creation");
  if (patch.length > MAX_SOURCE_ARTIFACT_BYTES) throw new Error("Git range patch exceeds FaultLine's portable artifact limit.");
  return patch;
}

function expectedExecutionId(run: GitInvestigationRunFact): string {
  return digestJson({
    schemaVersion: GIT_INVESTIGATION_SCHEMA_VERSION,
    executionNonce: run.executionNonce,
    stateIndex: run.stateIndex,
    commit: run.commit,
    tree: run.tree,
    frozenDigest: run.frozenDigest,
    executionAttempt: run.executionAttempt
  });
}

function expectedRunId(run: GitInvestigationRunFact): string {
  const { runId: _runId, ...unsigned } = run;
  return digestJson(unsigned);
}

function reconstructStableStates(result: GitInvestigationResult): StableGitState[] {
  if (!result.proof.dockerIsolated || result.proof.executionTrust !== "NATIVE_DOCKER") return [];
  const stable: StableGitState[] = [];
  for (const state of result.states) {
    const runs = result.runs
      .filter((run) => run.stateIndex === state.index)
      .sort((left, right) => left.executionAttempt - right.executionAttempt);
    if (runs.length !== STABLE_EXECUTION_COUNT) continue;
    const attempts = new Set(runs.map((run) => run.executionAttempt));
    const executionIds = new Set(runs.map((run) => run.executionId));
    const verdict = runs[0]?.result.verdict;
    if (attempts.size !== STABLE_EXECUTION_COUNT || executionIds.size !== STABLE_EXECUTION_COUNT
      || (verdict !== "PASS" && verdict !== "FAIL")
      || !runs.every((run) => run.commit === state.commit && run.tree === state.tree
        && run.result.kind === "DOCKER_ISOLATED" && run.result.executor === "NATIVE_DOCKER"
        && run.result.verdict === verdict)) continue;
    stable.push({
      stateIndex: state.index,
      commit: state.commit,
      tree: state.tree,
      verdict,
      executionIds: runs.map((run) => run.executionId),
      runIds: runs.map((run) => run.runId)
    });
  }
  return stable;
}

function reconstructTransitions(states: readonly StableGitState[]): StableGitTransition[] {
  const transitions: StableGitTransition[] = [];
  for (let index = 1; index < states.length; index += 1) {
    const before = states[index - 1];
    const after = states[index];
    if (!before || !after || after.stateIndex !== before.stateIndex + 1 || before.verdict === after.verdict) continue;
    transitions.push({
      kind: before.verdict === "PASS" ? "PASS_TO_FAIL" : "FAIL_TO_PASS",
      before,
      after
    });
  }
  return transitions;
}

type LifecycleBinding = z.infer<typeof LifecycleBindingSchema>;

function hasLifecycleLedger(binding: LifecycleBinding): binding is Exclude<LifecycleBinding, z.infer<typeof LifecycleUnboundSchema>> {
  return binding.status !== "UNBOUND";
}

function unboundLifecycle(): z.infer<typeof LifecycleUnboundSchema> {
  return {
    status: "UNBOUND",
    limitation: "No caller-supplied Codex lifecycle ledger is bound to this Git investigation package."
  };
}

/**
 * Bind only factual, caller-supplied lifecycle data. The ledger protocol can
 * represent Codex CLI, app, or side-car observations, so this does not infer
 * native model lifecycle access from a Git replay.
 */
function bindLifecycleLedger(
  ledgerInput: CodexLifecycleLedger | undefined,
  result: GitInvestigationResult
): { binding: LifecycleBinding; ledger?: CodexLifecycleLedger } {
  if (ledgerInput === undefined) return { binding: unboundLifecycle() };
  const ledger = CodexLifecycleLedgerSchema.parse(ledgerInput);
  const verification = verifyCodexLifecycleLedger(ledger);
  if (!verification.valid || verification.headHash === null) {
    throw new Error(`Cannot bind an invalid Codex lifecycle ledger: ${verification.errors.join("; ")}`);
  }
  const sessionStarted = ledger.events.find((event) => event.event.type === "SESSION_STARTED");
  if (!sessionStarted || sessionStarted.event.type !== "SESSION_STARTED") {
    throw new Error("Cannot bind a lifecycle ledger that has no SESSION_STARTED observation.");
  }
  const byCommit = new Map(result.states.map((state) => [state.commit, state]));
  const checkpointBindings: Array<{ sequence: number; stateIndex: number; checkpointDigest: string }> = [];
  for (const event of ledger.events) {
    if (event.event.type !== "WORKTREE_CHECKPOINT") continue;
    const checkpoint = event.event.payload.checkpoint;
    const state = byCommit.get(checkpoint.headCommit);
    if (!state) continue;
    if (checkpoint.treeDigest !== state.tree) {
      throw new Error(`Lifecycle checkpoint ${event.sequence} has a tree that disagrees with investigated commit ${checkpoint.headCommit}.`);
    }
    checkpointBindings.push({ sequence: event.sequence, stateIndex: state.index, checkpointDigest: checkpoint.digest });
  }
  if (checkpointBindings.length === 0) {
    throw new Error("Cannot bind a lifecycle ledger without a clean checkpoint matching an investigated Git state.");
  }
  const descendantBinding = checkpointBindings.some((binding) => binding.stateIndex === result.resolvedRange?.descendant.index);
  if (!descendantBinding) {
    throw new Error("Lifecycle ledger must include a clean checkpoint for the investigated descendant state.");
  }
  const coveredStateIndices = new Set(checkpointBindings.map((binding) => binding.stateIndex));
  const status = coveredStateIndices.size === result.states.length ? "FULLY_BOUND" : "PARTIALLY_BOUND";
  return {
    binding: (status === "FULLY_BOUND" ? LifecycleFullyBoundSchema : LifecyclePartiallyBoundSchema).parse({
      status,
      path: "lifecycle/ledger.json",
      ledgerDigest: digestJson(ledger),
      headHash: verification.headHash,
      transport: sessionStarted.event.payload.transport as CodexTransport,
      checkpointBindings
    }),
    ledger
  };
}

/**
 * Reconstruct the claim from raw run facts rather than trusting the persisted
 * stable states, transitions, or proof bit. This catches an attacker who
 * rehashes every mutable metadata file after changing a verdict.
 */
export function validateGitInvestigationProofSemantics(
  result: GitInvestigationResult,
  frozenWitness: FrozenWitness
): string[] {
  const errors: string[] = [];
  if (result.status !== "COMPLETED") errors.push("Git proof bundle only accepts a COMPLETED investigation");
  if (result.errors.length !== 0) errors.push("completed Git investigation must not contain recorder errors");
  if (result.repository === null || result.resolvedRange === null) errors.push("completed Git investigation must include repository and resolved range");
  if (result.states.length < 2) errors.push("Git proof bundle requires at least two resolved commit states");
  if (!result.proof.dockerIsolated) errors.push("Git proof bundle requires Docker-isolated execution");
  if (result.proof.executionTrust !== "NATIVE_DOCKER") errors.push("Git proof bundle requires native Docker executor provenance");
  if (!result.proof.isProof) errors.push("Git proof bundle requires at least one reconstructed stable transition");
  if (!result.witness?.valid || result.witness.externalDigestStatus !== "MATCH") {
    errors.push("investigation does not attest a valid externally matched frozen witness");
  }

  const witnessVerification = verifyFrozenWitnessRecord(frozenWitness, frozenWitness.frozenDigest);
  if (!witnessVerification.valid || witnessVerification.externalDigestStatus !== "MATCH") {
    errors.push(...witnessVerification.errors.map((error) => `frozen witness is invalid: ${error}`));
  }
  if (result.witness) {
    if (result.witness.frozenDigest !== frozenWitness.frozenDigest) errors.push("investigation witness frozen digest does not match frozen witness artifact");
    if (result.witness.witnessDigest !== frozenWitness.witnessDigest) errors.push("investigation witness digest does not match frozen witness artifact");
    if (!sameCanonical(result.witness.errors, witnessVerification.errors)) errors.push("investigation witness errors do not match frozen witness verification");
    if (!sameCanonical(result.witness.approval, witnessVerification.approval)) errors.push("investigation witness approval does not match frozen witness verification");
  }

  const stateByIndex = new Map<number, z.infer<typeof GitCommitStateSchema>>();
  const commits = new Set<string>();
  for (const [offset, state] of result.states.entries()) {
    if (state.index !== offset) errors.push(`state index is not contiguous at offset ${offset}`);
    if (stateByIndex.has(state.index)) errors.push(`duplicate state index: ${state.index}`);
    if (commits.has(state.commit)) errors.push(`duplicate commit in Git state sequence: ${state.commit}`);
    stateByIndex.set(state.index, state);
    commits.add(state.commit);
  }
  if (result.resolvedRange && result.states.length > 0) {
    const first = result.states[0];
    const last = result.states[result.states.length - 1];
    if (!sameCanonical(first, result.resolvedRange.ancestor) || !sameCanonical(last, result.resolvedRange.descendant)) {
      errors.push("resolved range endpoints do not match the first and last state");
    }
  }

  const runIds = new Set<string>();
  const executionIds = new Set<string>();
  const executionNonces = new Set<string>();
  const attemptsByState = new Map<number, Set<number>>();
  for (const run of result.runs) {
    if (run.runId !== expectedRunId(run)) errors.push(`runId does not match the canonical run fact: ${run.runId}`);
    if (run.executionId !== expectedExecutionId(run)) errors.push(`executionId does not match its state and attempt: ${run.runId}`);
    if (runIds.has(run.runId)) errors.push(`duplicate runId: ${run.runId}`);
    if (executionIds.has(run.executionId)) errors.push(`duplicate executionId: ${run.executionId}`);
    if (executionNonces.has(run.executionNonce)) errors.push(`duplicate execution nonce: ${run.executionNonce}`);
    runIds.add(run.runId);
    executionIds.add(run.executionId);
    executionNonces.add(run.executionNonce);
    const state = stateByIndex.get(run.stateIndex);
    if (!state || state.commit !== run.commit || state.tree !== run.tree) errors.push(`run state does not match the state sequence: ${run.runId}`);
    if (run.frozenDigest !== frozenWitness.frozenDigest || run.witnessDigest !== frozenWitness.witnessDigest) {
      errors.push(`run witness digest does not match the frozen witness: ${run.runId}`);
    }
    if (run.sandbox.kind !== run.result.kind) errors.push(`run sandbox kind does not match result kind: ${run.runId}`);
    for (const auditError of validateSandboxPlanAudit(run.sandbox)) {
      errors.push(`run sandbox audit is invalid: ${run.runId}: ${auditError}`);
    }
    if (run.sandbox.witnessDigest !== frozenWitness.frozenDigest) errors.push(`run sandbox witness digest does not match frozen witness: ${run.runId}`);
    if (run.sandbox.commandDigest !== digestBytes(Buffer.from(frozenWitness.proposal.witness.command, "utf8"))) {
      errors.push(`run sandbox command digest does not match frozen command bytes: ${run.runId}`);
    }
    const runtime = run.sandbox.runtime;
    if (!DIGEST_PINNED_IMAGE.test(runtime.image ?? "")) errors.push(`run sandbox image is not digest-pinned: ${run.runId}`);
    if (runtime.entrypoint !== "/bin/sh" || runtime.network !== "none" || !runtime.rootFilesystemReadOnly
      || runtime.user !== "65534:65534" || !runtime.capDropAll || !runtime.noNewPrivileges || runtime.pull !== "never") {
      errors.push(`run sandbox runtime policy is not a locked Docker plan: ${run.runId}`);
    }
    if (runtime.limits.timeoutMs <= 0 || runtime.limits.maxOutputBytes <= 0 || runtime.limits.cpuCount <= 0
      || runtime.limits.memoryBytes <= 0 || runtime.limits.pidsLimit <= 0 || runtime.limits.tmpfsBytes <= 0) {
      errors.push(`run sandbox limits are invalid: ${run.runId}`);
    }
    if (run.result.kind !== "DOCKER_ISOLATED") errors.push(`non-Docker run cannot support this proof bundle: ${run.runId}`);
    if (run.result.executor !== "NATIVE_DOCKER") errors.push(`non-native Docker executor cannot support this proof bundle: ${run.runId}`);
    if (run.result.verdict !== "PASS" && run.result.verdict !== "FAIL") errors.push(`non-decisive run cannot support this proof bundle: ${run.runId}`);
    if (run.result.verdict === "PASS" && (run.result.reason !== "EXIT_ZERO" || run.result.exitCode !== 0)) {
      errors.push(`PASS run has inconsistent execution result: ${run.runId}`);
    }
    if (run.result.verdict === "FAIL" && (run.result.reason !== "EXIT_NONZERO" || run.result.exitCode === 0 || run.result.exitCode === null)) {
      errors.push(`FAIL run has inconsistent execution result: ${run.runId}`);
    }
    const attempts = attemptsByState.get(run.stateIndex) ?? new Set<number>();
    if (attempts.has(run.executionAttempt)) errors.push(`duplicate execution attempt for state ${run.stateIndex}`);
    attempts.add(run.executionAttempt);
    attemptsByState.set(run.stateIndex, attempts);
  }
  if (result.runs.length !== result.states.length * STABLE_EXECUTION_COUNT) {
    errors.push("completed proof investigation does not contain exactly three runs per state");
  }
  for (const state of result.states) {
    const attempts = attemptsByState.get(state.index);
    if (!attempts || attempts.size !== STABLE_EXECUTION_COUNT || ![1, 2, 3].every((attempt) => attempts.has(attempt))) {
      errors.push(`state ${state.index} does not contain attempts 1, 2, and 3 exactly once`);
    }
  }

  const stableStates = reconstructStableStates(result);
  if (!sameCanonical(result.stableStates, stableStates)) errors.push("persisted stable states contradict the reconstructed run facts");
  const transitions = reconstructTransitions(stableStates);
  if (!sameCanonical(result.transitions, transitions)) errors.push("persisted transitions contradict the reconstructed stable states");
  const nonMonotonic = transitions.some((transition) => transition.kind === "PASS_TO_FAIL")
    && transitions.some((transition) => transition.kind === "FAIL_TO_PASS");
  if (result.nonMonotonic !== nonMonotonic) errors.push("persisted nonMonotonic flag contradicts reconstructed transitions");
  if (result.proof.proofTransitions !== transitions.length) errors.push("proof transition count contradicts reconstructed transitions");
  const expectedProof = result.proof.dockerIsolated && result.proof.executionTrust === "NATIVE_DOCKER"
    && result.status === "COMPLETED" && transitions.length > 0;
  if (result.proof.isProof !== expectedProof) errors.push("proof isProof flag contradicts reconstructed transitions and status");
  if (expectedProof && result.proof.reason !== "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states.") {
    errors.push("proof reason does not match a completed Docker transition proof");
  }
  return errors;
}

function makeManifest(
  result: GitInvestigationResult,
  frozenWitness: FrozenWitness,
  generatedAt: string,
  lifecycle: LifecycleBinding
): GitProofBundleManifest {
  if (result.resolvedRange === null) throw new Error("Cannot create a manifest without a resolved Git range.");
  return GitProofBundleManifestSchema.parse({
    schemaVersion: GIT_PROOF_BUNDLE_SCHEMA_VERSION,
    generatedAt,
    integrityScope: "complete-declared-file-set",
    investigationDigest: digestJson(result),
    frozenDigest: frozenWitness.frozenDigest,
    witnessDigest: frozenWitness.witnessDigest,
    lifecycle,
    resolvedRange: result.resolvedRange,
    artifacts: {
      investigation: "investigation.json",
      frozenWitness: "witness/frozen.json",
      runs: result.runs.map((run) => ({ runId: run.runId, path: runArtifactPath(run) })),
      transitions: result.transitions.map((_, index) => ({ index, path: transitionArtifactPath(index) })),
      sourceMetadata: "source/metadata.json",
      gitBundle: "source/descendant.bundle",
      rangePatch: "source/range.patch",
      verification: "VERIFY.md",
      ...(hasLifecycleLedger(lifecycle) ? { lifecycleLedger: lifecycle.path } : {})
    }
  });
}

function verificationReadme(): Buffer {
  return Buffer.from([
    "# Verify this Git investigation package",
    "",
    "## Handling warning",
    "",
    "This package retains a frozen witness, Git object references, and bounded recorded evidence so it can be independently checked. Treat it as sensitive incident material and share it only with authorized reviewers.",
    "",
    "Run FaultLine's Git proof verifier with an externally retained root digest.",
    "",
    "The verifier never executes the frozen witness. It validates every declared byte, reconstructs the recorded stable states and transitions from raw run facts, and reconstructs the Git source range from the bundled descendant history before checking the binary range patch.",
    "",
    "An external root is necessary to detect an editor who rewrites the mutable hash catalog too."
  ].join("\n"), "utf8");
}

function parseJsonFile(root: string, artifact: string, label: string, errors: string[]): unknown | undefined {
  try {
    const path = safeArtifactPath(root, artifact);
    return JSON.parse(readBoundedFile(path, label).toString("utf8"));
  } catch (error) {
    errors.push(`${label} JSON validation failed: ${errorMessage(error)}`);
    return undefined;
  }
}

function parseHashCatalog(hashes: Buffer, errors: string[]): Map<string, string> {
  const catalog = new Map<string, string>();
  const source = hashes.toString("utf8");
  if (!source.endsWith("\n")) errors.push("hashes.txt must end with a newline");
  for (const line of source.split("\n").filter(Boolean)) {
    const match = /^(?<digest>[a-f0-9]{64})  (?<artifact>.+)$/.exec(line);
    if (!match?.groups?.digest || !match.groups.artifact) {
      errors.push(`invalid hash catalog entry: ${line}`);
      continue;
    }
    const artifact = match.groups.artifact;
    if (!isSafeRelativeArtifactPath(artifact)) {
      errors.push(`hash catalog path is unsafe: ${artifact}`);
      continue;
    }
    if (catalog.has(artifact)) {
      errors.push(`hash catalog lists an artifact more than once: ${artifact}`);
      continue;
    }
    if (catalog.size >= MAX_GIT_PROOF_ARTIFACTS) {
      errors.push("hash catalog lists too many artifacts to verify safely");
      break;
    }
    catalog.set(artifact, match.groups.digest);
  }
  return catalog;
}

function sourceMetadataFromArtifacts(
  root: string,
  metadata: GitProofSourceMetadata,
  result: GitInvestigationResult,
  manifest: GitProofBundleManifest,
  errors: string[]
): void {
  if (result.resolvedRange === null) {
    errors.push("investigation has no resolved range for source metadata cross-check");
    return;
  }
  if (!sameCanonical(metadata.ancestor, result.resolvedRange.ancestor) || !sameCanonical(metadata.descendant, result.resolvedRange.descendant)) {
    errors.push("source metadata endpoints do not match the investigation range");
  }
  if (metadata.bundle.path !== manifest.artifacts.gitBundle || metadata.rangePatch.path !== manifest.artifacts.rangePatch) {
    errors.push("source metadata artifact paths do not match the manifest");
  }
  const bundle = readBoundedFile(safeArtifactPath(root, metadata.bundle.path), "source Git bundle");
  const patch = readBoundedFile(safeArtifactPath(root, metadata.rangePatch.path), "source range patch");
  if (metadata.bundle.digest !== digestBytes(bundle) || metadata.bundle.bytes !== bundle.length) errors.push("source metadata Git bundle digest or byte count is invalid");
  if (metadata.rangePatch.digest !== digestBytes(patch) || metadata.rangePatch.bytes !== patch.length) errors.push("source metadata range patch digest or byte count is invalid");
}

function verifyPortableGitSource(
  root: string,
  metadata: GitProofSourceMetadata,
  result: GitInvestigationResult,
  errors: string[]
): void {
  const bundlePath = safeArtifactPath(root, metadata.bundle.path);
  const patchPath = safeArtifactPath(root, metadata.rangePatch.path);
  assertRegularFile(bundlePath, "portable Git bundle");
  assertRegularFile(patchPath, "portable Git range patch");
  const temporaryBare = mkdtempSync(join(tmpdir(), "faultline-git-proof-verify-"));
  try {
    const heads = parseBundleHeads(gitBytes(undefined, ["bundle", "list-heads", bundlePath], "Git bundle head listing"));
    if (heads.length !== 1 || heads[0]?.ref !== metadata.bundle.headRef || heads[0].commit !== metadata.bundle.headCommit
      || heads[0].commit !== metadata.descendant.commit) {
      errors.push("Git bundle heads do not match source metadata descendant");
    }
    gitBytes(undefined, ["init", "--bare", "--quiet", temporaryBare], "Temporary Git verifier initialization");
    gitBytes(temporaryBare, ["bundle", "verify", bundlePath], "Git bundle verification");
    gitBytes(temporaryBare, ["fetch", "--quiet", bundlePath, `${BUNDLE_HEAD_REF}:refs/heads/faultline-descendant`], "Git bundle extraction");
    const descendant = resolveGitState(temporaryBare, "refs/heads/faultline-descendant");
    const ancestor = resolveGitState(temporaryBare, metadata.ancestor.commit);
    if (!sameCanonical(descendant, { commit: metadata.descendant.commit, tree: metadata.descendant.tree })) {
      errors.push("Git bundle descendant object does not match source metadata");
    }
    if (!sameCanonical(ancestor, { commit: metadata.ancestor.commit, tree: metadata.ancestor.tree })) {
      errors.push("Git bundle ancestor object does not match source metadata");
    }
    const ancestry = runGit(temporaryBare, ["merge-base", "--is-ancestor", metadata.ancestor.commit, metadata.descendant.commit]);
    if (ancestry.error || ancestry.status !== 0) errors.push("Git bundle does not preserve ancestor-to-descendant ancestry");
    const listed = gitText(temporaryBare, [
      "rev-list",
      "--reverse",
      "--ancestry-path",
      "--end-of-options",
      `${metadata.ancestor.commit}..${metadata.descendant.commit}`
    ], "Git bundle range enumeration");
    const commits = metadata.ancestor.commit === metadata.descendant.commit
      ? [metadata.ancestor.commit]
      : [metadata.ancestor.commit, ...listed.split(/\r?\n/).filter(Boolean)];
    const reconstructedStates = commits.map((commit, index) => ({ index, ...resolveGitState(temporaryBare, commit) }));
    if (!sameCanonical(reconstructedStates, result.states)) {
      errors.push("investigation state sequence does not exactly match the bundled ancestor-to-descendant Git path");
    }
    const generatedPatch = binaryRangePatch(temporaryBare, metadata.ancestor.commit, metadata.descendant.commit);
    const storedPatch = readBoundedFile(patchPath, "portable Git range patch");
    if (!generatedPatch.equals(storedPatch)) errors.push("binary range patch does not reproduce the bundled Git endpoint diff");
    const objectFormat = gitText(temporaryBare, ["rev-parse", "--show-object-format"], "Git bundle object-format resolution");
    if (objectFormat !== metadata.objectFormat) errors.push("Git bundle object format does not match source metadata");
  } catch (error) {
    errors.push(`portable Git source verification failed: ${errorMessage(error)}`);
  } finally {
    rmSync(temporaryBare, { recursive: true, force: true });
  }
}

function expectedManifestArtifacts(result: GitInvestigationResult, lifecycle: LifecycleBinding): GitProofBundleManifest["artifacts"] {
  return {
    investigation: "investigation.json",
    frozenWitness: "witness/frozen.json",
    runs: result.runs.map((run) => ({ runId: run.runId, path: runArtifactPath(run) })),
    transitions: result.transitions.map((_, index) => ({ index, path: transitionArtifactPath(index) })),
    sourceMetadata: "source/metadata.json",
    gitBundle: "source/descendant.bundle",
    rangePatch: "source/range.patch",
    verification: "VERIFY.md",
    ...(hasLifecycleLedger(lifecycle) ? { lifecycleLedger: lifecycle.path } : {})
  };
}

function verifyLifecycleBinding(
  root: string,
  manifest: GitProofBundleManifest,
  result: GitInvestigationResult,
  errors: string[]
): void {
  if (manifest.lifecycle.status === "UNBOUND") {
    if (manifest.artifacts.lifecycleLedger !== undefined) errors.push("unbound manifest unexpectedly declares a lifecycle ledger artifact");
    return;
  }
  if (manifest.artifacts.lifecycleLedger !== manifest.lifecycle.path) {
    errors.push("bound manifest lifecycle artifact path does not match its lifecycle binding");
    return;
  }
  const payload = parseJsonFile(root, manifest.lifecycle.path, "lifecycle ledger", errors);
  const parsedLedger = CodexLifecycleLedgerSchema.safeParse(payload);
  if (!parsedLedger.success) {
    errors.push(`lifecycle ledger schema validation failed: ${parsedLedger.error.message}`);
    return;
  }
  const verification = verifyCodexLifecycleLedger(parsedLedger.data);
  if (!verification.valid || verification.headHash === null) {
    errors.push(...verification.errors.map((error) => `lifecycle ledger verification failed: ${error}`));
    return;
  }
  try {
    const rebound = bindLifecycleLedger(parsedLedger.data, result).binding;
    // Packages written before coverage labels existed used `BOUND` for the
    // same factual checkpoint binding. Reconstruct every factual field from
    // the ledger, but compare it with that legacy label when verifying one of
    // those historical packages. New writers never emit the ambiguous label.
    const expected = manifest.lifecycle.status === "BOUND"
      ? LifecycleLegacyBoundSchema.parse({ ...rebound, status: "BOUND" })
      : rebound;
    if (!sameCanonical(expected, manifest.lifecycle)) errors.push("lifecycle ledger binding does not match its valid checkpoint-to-state reconstruction");
  } catch (error) {
    errors.push(`lifecycle ledger cannot bind to the investigation: ${errorMessage(error)}`);
  }
}

/**
 * Write a write-once portable package. The bundled Git history is created in a
 * temporary shared bare repository so the source repository never receives a
 * synthetic ref or any other mutation.
 */
export function writeGitInvestigationProofBundle(
  outputDirectory: string,
  investigationInput: GitInvestigationResult,
  frozenWitnessInput: FrozenWitness,
  options: GitProofBundleWriteOptions = {}
): WrittenGitProofBundle {
  const result = GitInvestigationResultSchema.parse(investigationInput);
  const frozenWitness = FrozenWitnessSchema.parse(frozenWitnessInput);
  const semanticErrors = validateGitInvestigationProofSemantics(result, frozenWitness);
  if (semanticErrors.length > 0) throw new Error(`Cannot write semantically inconsistent Git proof bundle: ${semanticErrors.join("; ")}`);
  const lifecycle = bindLifecycleLedger(options.lifecycleLedger, result);
  const source = sourceRepositoryFor(result);
  const generatedAt = options.generatedAt ?? new Date().toISOString();
  TimestampSchema.parse(generatedAt);
  const proofRoot = options.proofRoot ?? defaultGitProofRoot();
  const output = prepareFreshOutput(outputDirectory, proofRoot);
  const stage = join(dirname(output), `.${relative(dirname(output), output)}.staging-${randomUUID()}`);
  let manifest: GitProofBundleManifest | undefined;
  let rootDigest: string | undefined;
  try {
    mkdirSync(stage, { recursive: false, mode: 0o700 });
    assertNoLinksOrSpecialFiles(stage);
    const bundleDestination = safeArtifactPath(stage, "source/descendant.bundle");
    mkdirSync(dirname(bundleDestination), { recursive: true, mode: 0o700 });
    const portableBundle = writePortableGitBundle(source.repository, source.descendant.commit, bundleDestination);
    const rangePatch = binaryRangePatch(source.repository, source.ancestor.commit, source.descendant.commit);
    const metadata = GitProofSourceMetadataSchema.parse({
      schemaVersion: GIT_PROOF_SOURCE_SCHEMA_VERSION,
      objectFormat: source.objectFormat,
      ancestor: source.ancestor,
      descendant: source.descendant,
      bundle: {
        path: "source/descendant.bundle",
        digest: digestBytes(portableBundle.bytes),
        bytes: portableBundle.bytes.length,
        headRef: BUNDLE_HEAD_REF,
        headCommit: portableBundle.headCommit
      },
      rangePatch: {
        path: "source/range.patch",
        digest: digestBytes(rangePatch),
        bytes: rangePatch.length,
        format: "git-diff --binary --full-index --no-ext-diff --no-textconv --no-renames"
      }
    });
    manifest = makeManifest(result, frozenWitness, generatedAt, lifecycle.binding);
    const artifacts = new Map<string, Buffer>();
    const put = (path: string, bytes: Buffer): void => {
      if (!isSafeRelativeArtifactPath(path)) throw new Error(`Unsafe generated artifact path: ${path}`);
      if (artifacts.has(path)) throw new Error(`Git proof bundle generated duplicate artifact: ${path}`);
      artifacts.set(path, bytes);
    };
    put("manifest.json", jsonBytes(manifest));
    put("investigation.json", jsonBytes(result));
    put("witness/frozen.json", jsonBytes(frozenWitness));
    put("source/metadata.json", jsonBytes(metadata));
    put("source/descendant.bundle", portableBundle.bytes);
    put("source/range.patch", rangePatch);
    put("VERIFY.md", verificationReadme());
    if (lifecycle.ledger) put("lifecycle/ledger.json", jsonBytes(lifecycle.ledger));
    for (const run of result.runs) put(runArtifactPath(run), jsonBytes(run));
    for (const [index, transition] of result.transitions.entries()) put(transitionArtifactPath(index), jsonBytes(transition));
    const expected = requiredArtifactPaths(result, hasLifecycleLedger(lifecycle.binding));
    const actual = [...artifacts.keys()].sort((left, right) => left.localeCompare(right));
    if (!sameCanonical(expected, actual)) throw new Error("Git proof writer did not produce the complete expected artifact set.");
    for (const [artifact, bytes] of artifacts) {
      const destination = safeArtifactPath(stage, artifact);
      if (existsSync(destination)) {
        const present = readFileSync(destination);
        if (!present.equals(bytes)) throw new Error(`Git proof source artifact changed before package assembly: ${artifact}`);
        continue;
      }
      writeArtifact(stage, artifact, bytes);
    }
    const hashes = Buffer.from(`${[...artifacts.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([artifact, bytes]) => `${sha256(bytes)}  ${artifact}`)
      .join("\n")}\n`, "utf8");
    rootDigest = digestBytes(hashes);
    writeArtifact(stage, "hashes.txt", hashes);
    writeArtifact(stage, "ROOT.sha256", Buffer.from(`${rootDigest}\n`, "utf8"));
    const stageVerification = verifyGitInvestigationProofBundle(stage, rootDigest);
    if (!stageVerification.valid) throw new Error(`Refusing to publish an invalid Git proof bundle: ${stageVerification.errors.join("; ")}`);
    if (existsSync(output)) throw new Error(`Git proof bundle output appeared during assembly and will not be replaced: ${output}`);
    renameSync(stage, output);
    return {
      directory: output,
      manifestDigest: digestJson(manifest),
      rootDigest,
      manifest
    };
  } catch (error) {
    if (existsSync(stage)) rmSync(stage, { recursive: true, force: true });
    throw error;
  }
}

/**
 * Verify all raw artifacts without executing a witness or checking out a user
 * repository. Git is used only to inspect the self-contained descendant
 * bundle, reconstruct endpoint objects, and regenerate the supplied patch.
 */
export function verifyGitInvestigationProofBundle(
  directory: string,
  expectedRoot?: string
): GitProofBundleVerification {
  const errors: string[] = [];
  let rootDigest: string | null = null;
  let externalRootStatus: GitProofBundleExternalRootStatus = expectedRoot === undefined ? "NOT_PROVIDED" : "MISMATCH";
  let manifest: GitProofBundleManifest | undefined;
  try {
    const root = resolve(directory);
    if (!existsSync(root)) return { valid: false, checkedFiles: 0, errors: ["Git proof bundle directory does not exist"], rootDigest, externalRootStatus };
    assertNoLinksOrSpecialFiles(root);
    const hashesPath = join(root, "hashes.txt");
    const rootPath = join(root, "ROOT.sha256");
    assertRegularFile(hashesPath, "hashes.txt", MAX_HASH_CATALOG_BYTES);
    assertRegularFile(rootPath, "ROOT.sha256", 1_024);
    const hashes = readBoundedFile(hashesPath, "hashes.txt", MAX_HASH_CATALOG_BYTES);
    rootDigest = digestBytes(hashes);
    const storedRoot = readBoundedFile(rootPath, "ROOT.sha256", 1_024).toString("utf8").trim();
    if (storedRoot !== rootDigest) errors.push("ROOT.sha256 does not match hashes.txt");
    if (expectedRoot !== undefined) {
      if (!SHA256_DIGEST.test(expectedRoot)) {
        errors.push("externally supplied Git proof root is not a sha256 digest");
      } else if (expectedRoot === rootDigest) {
        externalRootStatus = "MATCH";
      } else {
        errors.push("externally supplied Git proof root does not match");
      }
    }
    const catalog = parseHashCatalog(hashes, errors);
    let declaredBytes = 0;
    for (const [artifact, digest] of catalog) {
      try {
        const path = safeArtifactPath(root, artifact);
        declaredBytes += assertRegularFile(path, `declared artifact ${artifact}`);
        if (declaredBytes > MAX_GIT_PROOF_TOTAL_BYTES) {
          errors.push("declared artifacts exceed FaultLine's total verification read limit");
          break;
        }
        if (sha256(readBoundedFile(path, `declared artifact ${artifact}`)) !== digest) errors.push(`artifact digest mismatch: ${artifact}`);
      } catch (error) {
        errors.push(`declared artifact cannot be read safely (${artifact}): ${errorMessage(error)}`);
      }
    }

    const manifestPayload = parseJsonFile(root, "manifest.json", "manifest", errors);
    const investigationPayload = parseJsonFile(root, "investigation.json", "investigation", errors);
    const frozenPayload = parseJsonFile(root, "witness/frozen.json", "frozen witness", errors);
    const metadataPayload = parseJsonFile(root, "source/metadata.json", "source metadata", errors);
    const parsedManifest = GitProofBundleManifestSchema.safeParse(manifestPayload);
    if (!parsedManifest.success) errors.push(`manifest schema validation failed: ${parsedManifest.error.message}`);
    else manifest = parsedManifest.data;
    const parsedResult = GitInvestigationResultSchema.safeParse(investigationPayload);
    if (!parsedResult.success) errors.push(`investigation schema validation failed: ${parsedResult.error.message}`);
    const parsedFrozen = FrozenWitnessSchema.safeParse(frozenPayload);
    if (!parsedFrozen.success) errors.push(`frozen witness schema validation failed: ${parsedFrozen.error.message}`);
    const parsedMetadata = GitProofSourceMetadataSchema.safeParse(metadataPayload);
    if (!parsedMetadata.success) errors.push(`source metadata schema validation failed: ${parsedMetadata.error.message}`);

    if (manifest && parsedResult.success && parsedFrozen.success && parsedMetadata.success) {
      const result = parsedResult.data;
      const frozenWitness = parsedFrozen.data;
      const metadata = parsedMetadata.data;
      if (manifest.investigationDigest !== digestJson(result)) errors.push("manifest investigation digest does not match investigation.json");
      if (manifest.frozenDigest !== frozenWitness.frozenDigest || manifest.witnessDigest !== frozenWitness.witnessDigest) {
        errors.push("manifest frozen or witness digest does not match frozen witness artifact");
      }
      if (result.resolvedRange === null || !sameCanonical(manifest.resolvedRange, result.resolvedRange)) {
        errors.push("manifest range does not match investigation resolved range");
      }
      if (!sameCanonical(manifest.artifacts, expectedManifestArtifacts(result, manifest.lifecycle))) {
        errors.push("manifest artifact index does not exactly match the investigation run and transition catalog");
      }
      const expectedArtifacts = new Set(requiredArtifactPaths(result, hasLifecycleLedger(manifest.lifecycle)));
      for (const artifact of expectedArtifacts) if (!catalog.has(artifact)) errors.push(`required artifact is missing from hashes.txt: ${artifact}`);
      for (const artifact of catalog.keys()) if (!expectedArtifacts.has(artifact)) errors.push(`hashes.txt contains an unexpected artifact: ${artifact}`);
      const physical = new Set(collectFiles(root));
      const expectedPhysical = new Set([...expectedArtifacts, "hashes.txt", "ROOT.sha256"]);
      for (const artifact of physical) if (!expectedPhysical.has(artifact)) errors.push(`undeclared physical file exists in Git proof bundle: ${artifact}`);
      for (const artifact of expectedPhysical) if (!physical.has(artifact)) errors.push(`expected physical file is missing from Git proof bundle: ${artifact}`);

      const runs = new Map(result.runs.map((run) => [run.runId, run]));
      for (const descriptor of manifest.artifacts.runs) {
        const payload = parseJsonFile(root, descriptor.path, `run ${descriptor.runId}`, errors);
        const parsed = GitInvestigationRunFactSchema.safeParse(payload);
        if (!parsed.success) {
          errors.push(`run artifact schema validation failed for ${descriptor.runId}: ${parsed.error.message}`);
        } else if (parsed.data.runId !== descriptor.runId || !sameCanonical(parsed.data, runs.get(descriptor.runId))) {
          errors.push(`run artifact does not match investigation run catalog: ${descriptor.runId}`);
        }
      }
      for (const [index, descriptor] of manifest.artifacts.transitions.entries()) {
        const payload = parseJsonFile(root, descriptor.path, `transition ${String(index)}`, errors);
        const parsed = StableGitTransitionSchema.safeParse(payload);
        if (!parsed.success) {
          errors.push(`transition artifact schema validation failed for ${String(index)}: ${parsed.error.message}`);
        } else if (descriptor.index !== index || !sameCanonical(parsed.data, result.transitions[index])) {
          errors.push(`transition artifact does not match investigation transition catalog: ${String(index)}`);
        }
      }
      const witnessVerification = verifyFrozenWitnessRecord(frozenWitness, frozenWitness.frozenDigest);
      if (!witnessVerification.valid || witnessVerification.externalDigestStatus !== "MATCH") {
        errors.push(...witnessVerification.errors.map((error) => `frozen witness verification failed: ${error}`));
      }
      errors.push(...validateGitInvestigationProofSemantics(result, frozenWitness));
      verifyLifecycleBinding(root, manifest, result, errors);
      sourceMetadataFromArtifacts(root, metadata, result, manifest, errors);
      verifyPortableGitSource(root, metadata, result, errors);
    }
    return manifest
      ? { valid: errors.length === 0, checkedFiles: catalog.size, errors, rootDigest, externalRootStatus, manifest }
      : { valid: false, checkedFiles: catalog.size, errors, rootDigest, externalRootStatus };
  } catch (error) {
    errors.push(`Git proof bundle verification failed safely: ${errorMessage(error)}`);
    return manifest
      ? { valid: false, checkedFiles: 0, errors, rootDigest, externalRootStatus, manifest }
      : { valid: false, checkedFiles: 0, errors, rootDigest, externalRootStatus };
  }
}
