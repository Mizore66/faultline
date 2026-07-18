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
import { STABLE_EXECUTION_COUNT } from "./git-investigation.js";
import { runHardenedGit, toGitPath } from "./git-materialization.js";
import {
  CodexLifecycleLedgerSchema,
  verifyCodexLifecycleLedger,
  type CodexLifecycleLedger,
  type CodexTransport
} from "./ledger.js";
import { assertSafeProofOutput } from "./proof-bundle.js";
import { validateSandboxPlanAudit } from "./sandbox.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";
import {
  findStableTurnTransitions,
  reconstructStableTurnStates,
  turnStatesFromLedger,
  TURN_INVESTIGATION_SCHEMA_VERSION,
  TurnInvestigationResultSchema,
  TurnInvestigationRunFactSchema,
  StableTurnTransitionSchema,
  type TurnInvestigationResult,
  type TurnInvestigationRunFact,
  type TurnState
} from "./turn-investigation.js";
import {
  FrozenWitnessSchema,
  verifyFrozenWitnessRecord,
  type FrozenWitness
} from "./witness-lock.js";

import {
  TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL,
  TURN_PATH_EVIDENCE_GRADE_PARITY_RESERVED,
  TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL
} from "./evidence-grade.js";

/**
 * A turn-tree investigation package with Git-inspired integrity checks.
 * User-facing maturity remains `EXPERIMENTAL_TURN` until the named TURN_PROOF
 * promotion criteria in `evidence-grade.ts` are met — not merely because a
 * write-once portable package exists.
 */
export const TURN_PROOF_BUNDLE_SCHEMA_VERSION = "faultline.turn-proof-bundle.v1" as const;
export const TURN_PROOF_SOURCE_SCHEMA_VERSION = "faultline.turn-proof-source.v1" as const;

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const DIGEST_PINNED_IMAGE = /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/;
const MAX_SOURCE_ARTIFACT_BYTES = 128 * 1024 * 1024;
const MAX_HASH_CATALOG_BYTES = 4 * 1024 * 1024;
const MAX_TURN_PROOF_ARTIFACTS = 2_048;
const MAX_TURN_PROOF_TOTAL_BYTES = 512 * 1024 * 1024;

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

const LifecycleBoundSchema = z.object({
  status: z.literal("BOUND"),
  path: z.literal("lifecycle/ledger.json"),
  ledgerDigest: DigestSchema,
  headHash: DigestSchema,
  transport: z.enum(["CODEX_CLI", "CODEX_APP", "SIDE_CAR"])
}).strict();

export const TurnProofSourceMetadataSchema = z.object({
  schemaVersion: z.literal(TURN_PROOF_SOURCE_SCHEMA_VERSION),
  trees: z.array(z.object({
    stateIndex: z.number().int().nonnegative(),
    turnOrdinal: z.number().int().nonnegative(),
    treeDigest: z.string().regex(/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/),
    snapshotDigest: DigestSchema
  }).strict()).min(2),
  pack: z.object({
    path: z.literal("source/trees.pack"),
    digest: DigestSchema,
    bytes: z.number().int().positive()
  }).strict()
}).strict();

export const TurnProofBundleManifestSchema = z.object({
  schemaVersion: z.literal(TURN_PROOF_BUNDLE_SCHEMA_VERSION),
  generatedAt: TimestampSchema,
  integrityScope: z.literal("complete-declared-file-set"),
  investigationDigest: DigestSchema,
  frozenDigest: DigestSchema,
  witnessDigest: DigestSchema,
  lifecycle: LifecycleBoundSchema,
  artifacts: z.object({
    investigation: z.literal("investigation.json"),
    frozenWitness: z.literal("witness/frozen.json"),
    runs: z.array(RunArtifactSchema),
    transitions: z.array(TransitionArtifactSchema),
    sourceMetadata: z.literal("source/trees.metadata.json"),
    treesPack: z.literal("source/trees.pack"),
    verification: z.literal("VERIFY.md"),
    lifecycleLedger: z.literal("lifecycle/ledger.json")
  }).strict()
}).strict();

export type TurnProofSourceMetadata = z.infer<typeof TurnProofSourceMetadataSchema>;
export type TurnProofBundleManifest = z.infer<typeof TurnProofBundleManifestSchema>;
export type TurnProofBundleExternalRootStatus = "NOT_PROVIDED" | "MATCH" | "MISMATCH";

export interface TurnProofBundleWriteOptions {
  readonly proofRoot?: string;
  readonly generatedAt?: string;
  /** Required: turn identity is ledger-derived. */
  readonly lifecycleLedger: CodexLifecycleLedger;
  /** Host repository used only to export tree objects into the portable pack. */
  readonly repository: string;
}

export interface WrittenTurnProofBundle {
  readonly directory: string;
  readonly manifestDigest: string;
  readonly rootDigest: string;
  readonly manifest: TurnProofBundleManifest;
}

export interface TurnProofBundleVerification {
  readonly valid: boolean;
  readonly checkedFiles: number;
  readonly errors: readonly string[];
  readonly rootDigest: string | null;
  readonly externalRootStatus: TurnProofBundleExternalRootStatus;
  readonly manifest?: TurnProofBundleManifest;
}

export function defaultTurnProofRoot(): string {
  return resolve(".faultline", "turn-proof-bundles");
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

function assertNoLinksOrSpecialFiles(directory: string): void {
  const stat = lstatSync(directory);
  if (stat.isSymbolicLink() || !stat.isDirectory()) {
    throw new Error(`Turn proof bundle must be a real directory: ${directory}`);
  }
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const child = join(directory, entry.name);
    const childStat = lstatSync(child);
    if (childStat.isSymbolicLink() || (!childStat.isDirectory() && !childStat.isFile())) {
      throw new Error(`Turn proof bundle contains a symbolic link or special file: ${child}`);
    }
    if (childStat.isDirectory()) assertNoLinksOrSpecialFiles(child);
  }
}

function ensureRealDirectoryTree(directory: string): void {
  const absolute = resolve(directory);
  const parsed = parse(absolute);
  const suffix = relative(parsed.root, absolute);
  const parts = suffix ? suffix.split(/[\\/]+/).filter(Boolean) : [];
  let current = parsed.root;
  if (existsSync(current)) {
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) throw new Error(`Turn proof root is not a real directory: ${current}`);
    current = safeCurrent;
  }
  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) {
      throw new Error(`Turn proof root cannot traverse a symbolic link or non-directory: ${current}`);
    }
    current = safeCurrent;
  }
}

function isNested(root: string, candidate: string): boolean {
  const nested = relativeTrustedSystemPath(root, candidate);
  return Boolean(nested) && !nested.startsWith("..") && !isAbsolute(nested);
}

function prepareFreshOutput(outputDirectory: string, proofRoot: string): string {
  const root = resolve(proofRoot);
  const output = resolve(outputDirectory);
  if (!isNested(root, output)) throw new Error(`Turn proof bundle output must be a child directory of ${root}`);
  ensureRealDirectoryTree(root);
  ensureRealDirectoryTree(dirname(output));
  if (existsSync(output)) {
    throw new Error(`Turn proof bundle output already exists and will not be replaced: ${output}`);
  }
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
      throw new Error(`Turn proof bundle contains a symbolic link or special file: ${child}`);
    }
    if (stat.isDirectory()) files.push(...collectFiles(root, child, budget));
    else {
      if (stat.size > MAX_SOURCE_ARTIFACT_BYTES) throw new Error(`Turn proof bundle artifact exceeds FaultLine's read limit: ${child}`);
      budget.files += 1;
      budget.bytes += stat.size;
      if (budget.files > MAX_TURN_PROOF_ARTIFACTS) throw new Error("Turn proof bundle contains too many files to verify safely");
      if (budget.bytes > MAX_TURN_PROOF_TOTAL_BYTES) throw new Error("Turn proof bundle exceeds FaultLine's total verification read limit");
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

function runArtifactPath(run: TurnInvestigationRunFact): string {
  return `runs/${run.runId.slice("sha256:".length)}.json`;
}

function transitionArtifactPath(index: number): string {
  return `transitions/${String(index).padStart(4, "0")}.json`;
}

function requiredArtifactPaths(result: TurnInvestigationResult): string[] {
  return [
    "manifest.json",
    "investigation.json",
    "witness/frozen.json",
    "source/trees.metadata.json",
    "source/trees.pack",
    "VERIFY.md",
    "lifecycle/ledger.json",
    ...result.runs.map(runArtifactPath),
    ...result.transitions.map((_, index) => transitionArtifactPath(index))
  ].sort((left, right) => left.localeCompare(right));
}

function expectedManifestArtifacts(result: TurnInvestigationResult): TurnProofBundleManifest["artifacts"] {
  return {
    investigation: "investigation.json",
    frozenWitness: "witness/frozen.json",
    runs: result.runs.map((run) => ({ runId: run.runId, path: runArtifactPath(run) })),
    transitions: result.transitions.map((_, index) => ({ index, path: transitionArtifactPath(index) })),
    sourceMetadata: "source/trees.metadata.json",
    treesPack: "source/trees.pack",
    verification: "VERIFY.md",
    lifecycleLedger: "lifecycle/ledger.json"
  };
}

function expectedExecutionId(run: TurnInvestigationRunFact): string {
  return digestJson({
    schemaVersion: TURN_INVESTIGATION_SCHEMA_VERSION,
    executionNonce: run.executionNonce,
    stateIndex: run.stateIndex,
    turnId: run.turnId,
    turnOrdinal: run.turnOrdinal,
    treeDigest: run.treeDigest,
    snapshotDigest: run.snapshotDigest,
    environmentFingerprintDigest: run.environmentFingerprintDigest,
    frozenDigest: run.frozenDigest,
    executionAttempt: run.executionAttempt
  });
}

function expectedRunId(run: TurnInvestigationRunFact): string {
  const { runId: _runId, ...unsigned } = run;
  return digestJson(unsigned);
}

function verificationReadme(): Buffer {
  return Buffer.from([
    "# Verify this experimental turn investigation package",
    "",
    "## Evidence grade",
    "",
    `${TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL} (\`${TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL}\`).`,
    "This is not the same maturity tier as commit-path portable proof (`COMMIT_PROOF`).",
    "Do not treat a turn package as TURN_PROOF / COMMIT_PROOF. See evidence-grade.ts promotion criteria (external validation, turn-boundary counterfactuals, prevention integration, platform soak).",
    "",
    "## Handling warning",
    "",
    "This package retains a frozen witness, Codex lifecycle ledger, turn-tree object pack, and bounded recorded evidence so it can be independently checked. Treat it as sensitive incident material and share it only with authorized reviewers.",
    "",
    "Run FaultLine's turn package verifier with an externally retained root digest.",
    "",
    "The verifier never executes the frozen witness. It validates every declared byte, reconstructs stable states and transitions from raw run facts, rebinds the lifecycle ledger to turn states, and confirms each recorded tree object is present in the portable pack.",
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
  const text = hashes.toString("utf8");
  if (!text.endsWith("\n")) errors.push("hashes.txt must end with a trailing newline");
  for (const line of text.trimEnd().split("\n")) {
    const match = /^([a-f0-9]{64})  (.+)$/.exec(line);
    if (!match) {
      errors.push(`hashes.txt contains a malformed line: ${line}`);
      continue;
    }
    const digest = match[1];
    const artifact = match[2];
    if (!digest || !artifact || !isSafeRelativeArtifactPath(artifact)) {
      errors.push(`hashes.txt contains an unsafe artifact path: ${line}`);
      continue;
    }
    if (catalog.has(artifact)) errors.push(`hashes.txt declares duplicate artifact: ${artifact}`);
    catalog.set(artifact, digest);
  }
  return catalog;
}

async function packTurnTrees(repository: string, states: readonly TurnState[]): Promise<Buffer> {
  const objects = new Set<string>();
  for (const state of states) {
    const listed = await runHardenedGit(repository, ["rev-list", "--objects", state.treeDigest]);
    if (listed.exitCode !== 0 || listed.error !== undefined) {
      throw new Error(`Could not enumerate objects for turn tree ${state.treeDigest}: ${listed.stderr.toString("utf8").trim() || listed.error || "Git failed."}`);
    }
    for (const line of listed.stdout.toString("utf8").split(/\r?\n/)) {
      const oid = line.trim().split(/\s+/)[0];
      if (oid) objects.add(oid);
    }
  }
  if (objects.size === 0) throw new Error("Turn tree pack enumeration produced no Git objects.");
  const stdin = Buffer.from(`${[...objects].sort((left, right) => left.localeCompare(right)).join("\n")}\n`, "utf8");
  const packed = await runHardenedGit(repository, ["pack-objects", "--stdout", "--compression=9"], { stdin });
  if (packed.exitCode !== 0 || packed.error !== undefined || packed.stdout.length === 0) {
    throw new Error(`Could not pack turn trees: ${packed.stderr.toString("utf8").trim() || packed.error || "Git failed."}`);
  }
  if (packed.stdout.length > MAX_SOURCE_ARTIFACT_BYTES) {
    throw new Error("Turn tree pack exceeds FaultLine's portable artifact limit.");
  }
  return packed.stdout;
}

function removeTemporaryDirectory(path: string): void {
  try {
    rmSync(path, {
      recursive: true,
      force: true,
      maxRetries: process.platform === "win32" ? 20 : 3,
      retryDelay: process.platform === "win32" ? 250 : 50
    });
  } catch (error) {
    const code = error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
    // Hosted Windows runners can retain a just-closed Git pack handle briefly.
    if (process.platform === "win32" && (code === "EPERM" || code === "EBUSY")) return;
    throw error;
  }
}

async function verifyTreesPack(pack: Buffer, states: readonly TurnState[], errors: string[]): Promise<void> {
  const bare = mkdtempSync(join(tmpdir(), "faultline-turn-proof-pack-"));
  try {
    const init = await runHardenedGit(bare, ["init", "--bare"]);
    if (init.exitCode !== 0 || init.error !== undefined) {
      errors.push(`Could not create temporary bare repository for pack verification: ${init.stderr.toString("utf8").trim() || init.error || "Git failed."}`);
      return;
    }
    // Prefer unpack-objects over `index-pack --stdin --strict`: older Windows
    // Git builds reject that flag combination. File-path index-pack is kept as
    // a fallback and always receives a forward-slash path.
    const unpacked = await runHardenedGit(bare, ["unpack-objects", "-q", "--strict"], {
      stdin: pack,
      gitDir: bare
    });
    if (unpacked.exitCode !== 0 || unpacked.error !== undefined) {
      const packDir = join(bare, "objects", "pack");
      mkdirSync(packDir, { recursive: true, mode: 0o700 });
      const packFile = join(packDir, "turn-trees.pack");
      writeFileSync(packFile, pack, { mode: 0o600 });
      const indexed = await runHardenedGit(bare, ["index-pack", "--strict", toGitPath(packFile)], { gitDir: bare });
      if (indexed.exitCode !== 0 || indexed.error !== undefined) {
        errors.push(
          `Turn tree pack is not a valid Git pack: ${
            indexed.stderr.toString("utf8").trim()
            || unpacked.stderr.toString("utf8").trim()
            || indexed.error
            || unpacked.error
            || "Git failed."
          }`
        );
        return;
      }
    }
    for (const state of states) {
      const typed = await runHardenedGit(bare, ["cat-file", "-t", state.treeDigest], { gitDir: bare });
      if (typed.exitCode !== 0 || typed.error !== undefined || typed.stdout.toString("utf8").trim() !== "tree") {
        errors.push(`Turn tree object missing or wrong type in portable pack: ${state.treeDigest}`);
      }
    }
  } finally {
    removeTemporaryDirectory(bare);
  }
}

function bindTurnLifecycle(
  ledgerInput: CodexLifecycleLedger,
  result: TurnInvestigationResult
): { binding: z.infer<typeof LifecycleBoundSchema>; ledger: CodexLifecycleLedger } {
  const ledger = CodexLifecycleLedgerSchema.parse(ledgerInput);
  const verification = verifyCodexLifecycleLedger(ledger);
  if (!verification.valid || verification.headHash === null) {
    throw new Error(`Cannot bind an invalid Codex lifecycle ledger: ${verification.errors.join("; ")}`);
  }
  if (result.ledgerDigest === null || digestJson(ledger) !== result.ledgerDigest) {
    throw new Error("Lifecycle ledger digest does not match the turn investigation ledgerDigest.");
  }
  const reboundStates = turnStatesFromLedger(ledger);
  if (!sameCanonical(reboundStates, result.states)) {
    throw new Error("Lifecycle ledger turn states do not canonically match the investigation state sequence.");
  }
  const sessionStarted = ledger.events.find((event) => event.event.type === "SESSION_STARTED");
  if (!sessionStarted || sessionStarted.event.type !== "SESSION_STARTED") {
    throw new Error("Cannot bind a lifecycle ledger that has no SESSION_STARTED observation.");
  }
  return {
    binding: LifecycleBoundSchema.parse({
      status: "BOUND",
      path: "lifecycle/ledger.json",
      ledgerDigest: result.ledgerDigest,
      headHash: verification.headHash,
      transport: sessionStarted.event.payload.transport as CodexTransport
    }),
    ledger
  };
}

/**
 * Reconstruct the claim from raw run facts rather than trusting persisted
 * stable states, transitions, or the proof bit.
 */
export function validateTurnInvestigationProofSemantics(
  result: TurnInvestigationResult,
  frozenWitness: FrozenWitness
): string[] {
  const errors: string[] = [];
  if (result.status !== "COMPLETED") errors.push("Turn proof bundle only accepts a COMPLETED investigation");
  if (result.errors.length !== 0) errors.push("completed turn investigation must not contain recorder errors");
  if (result.repository === null || result.ledgerDigest === null) {
    errors.push("completed turn investigation must include repository and ledgerDigest");
  }
  if (result.states.length < 2) errors.push("Turn proof bundle requires at least two recorded turn states");
  if (!result.proof.dockerIsolated) errors.push("Turn proof bundle requires Docker-isolated execution");
  if (result.proof.executionTrust !== "NATIVE_DOCKER") errors.push("Turn proof bundle requires native Docker executor provenance");
  if (!result.proof.isProof) errors.push("Turn proof bundle requires at least one reconstructed stable transition");
  if (!result.witness?.valid || result.witness.externalDigestStatus !== "MATCH") {
    errors.push("investigation does not attest a valid externally matched frozen witness");
  }
  if (result.environmentFingerprints.length !== result.states.length) {
    errors.push("turn proof requires one environment fingerprint per recorded state");
  }
  for (const [index, fingerprint] of result.environmentFingerprints.entries()) {
    const mappedImage = result.runtimeMapping[fingerprint.digest];
    if (!mappedImage) {
      errors.push(`runtimeMapping is missing fingerprint ${fingerprint.digest}`);
    }
    const state = result.states[index];
    if (state) {
      const stateRuns = result.runs.filter((run) => run.stateIndex === state.index);
      for (const run of stateRuns) {
        if (run.environmentFingerprintDigest !== fingerprint.digest) {
          errors.push(`run fingerprint digest does not match state fingerprint: ${run.runId}`);
        }
        if (mappedImage && run.sandbox.runtime.image !== mappedImage) {
          errors.push(`run image does not match runtimeMapping for its fingerprint: ${run.runId}`);
        }
      }
    }
  }
  if (result.environmentHomogeneity === "HETEROGENEOUS") {
    const mapped = new Set(Object.keys(result.runtimeMapping));
    const required = new Set(result.environmentFingerprints.map((fingerprint) => fingerprint.digest));
    for (const digest of required) {
      if (!mapped.has(digest)) errors.push(`heterogeneous proof missing runtimeMapping entry: ${digest}`);
    }
  }

  const witnessVerification = verifyFrozenWitnessRecord(frozenWitness, frozenWitness.frozenDigest);
  if (!witnessVerification.valid || witnessVerification.externalDigestStatus !== "MATCH") {
    errors.push(...witnessVerification.errors.map((error) => `frozen witness is invalid: ${error}`));
  }
  if (result.witness) {
    if (result.witness.frozenDigest !== frozenWitness.frozenDigest) errors.push("investigation witness frozen digest does not match frozen witness artifact");
    if (result.witness.witnessDigest !== frozenWitness.witnessDigest) errors.push("investigation witness digest does not match frozen witness artifact");
  }

  const stateByIndex = new Map<number, TurnState>();
  for (const [offset, state] of result.states.entries()) {
    if (state.index !== offset) errors.push(`state index is not contiguous at offset ${offset}`);
    if (stateByIndex.has(state.index)) errors.push(`duplicate state index: ${state.index}`);
    stateByIndex.set(state.index, state);
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
    if (!state
      || state.turnId !== run.turnId
      || state.turnOrdinal !== run.turnOrdinal
      || state.role !== run.role
      || state.treeDigest !== run.treeDigest
      || state.snapshotDigest !== run.snapshotDigest) {
      errors.push(`run state does not match the state sequence: ${run.runId}`);
    }
    if (run.frozenDigest !== frozenWitness.frozenDigest || run.witnessDigest !== frozenWitness.witnessDigest) {
      errors.push(`run witness digest does not match the frozen witness: ${run.runId}`);
    }
    if (run.sandbox.kind !== run.result.kind) errors.push(`run sandbox kind does not match result kind: ${run.runId}`);
    for (const auditError of validateSandboxPlanAudit(run.sandbox)) {
      errors.push(`run sandbox audit is invalid: ${run.runId}: ${auditError}`);
    }
    if (run.sandbox.witnessDigest !== frozenWitness.frozenDigest) {
      errors.push(`run sandbox witness digest does not match frozen witness: ${run.runId}`);
    }
    if (run.sandbox.commandDigest !== digestBytes(Buffer.from(frozenWitness.proposal.witness.command, "utf8"))) {
      errors.push(`run sandbox command digest does not match frozen command bytes: ${run.runId}`);
    }
    const runtime = run.sandbox.runtime;
    if (!DIGEST_PINNED_IMAGE.test(runtime.image ?? "")) errors.push(`run sandbox image is not digest-pinned: ${run.runId}`);
    if (runtime.entrypoint !== "/bin/sh" || runtime.network !== "none" || !runtime.rootFilesystemReadOnly
      || runtime.user !== "65534:65534" || !runtime.capDropAll || !runtime.noNewPrivileges || runtime.pull !== "never") {
      errors.push(`run sandbox runtime policy is not a locked Docker plan: ${run.runId}`);
    }
    if (run.result.kind !== "DOCKER_ISOLATED") errors.push(`non-Docker run cannot support this proof bundle: ${run.runId}`);
    if (run.result.executor !== "NATIVE_DOCKER") errors.push(`non-native Docker executor cannot support this proof bundle: ${run.runId}`);
    if (run.result.verdict !== "PASS" && run.result.verdict !== "FAIL") errors.push(`non-decisive run cannot support this proof bundle: ${run.runId}`);
    // Match investigation isDecisiveRun: only structured PREDICATE_* outcomes
    // may establish decisive PASS/FAIL. Legacy EXIT_ZERO / EXIT_NONZERO are rejected.
    if (run.result.verdict === "PASS" && run.result.reason !== "PREDICATE_PASS") {
      errors.push(`PASS run must use PREDICATE_PASS (legacy EXIT_ZERO is not decisive): ${run.runId}`);
    }
    if (run.result.verdict === "FAIL" && run.result.reason !== "PREDICATE_FAIL") {
      errors.push(`FAIL run must use PREDICATE_FAIL (legacy EXIT_NONZERO is not decisive): ${run.runId}`);
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

  const stableStates = reconstructStableTurnStates(result.states, result.runs, { requireNativeExecutor: true });
  if (!sameCanonical(result.stableStates, stableStates)) errors.push("persisted stable states contradict the reconstructed run facts");
  const transitions = findStableTurnTransitions(stableStates);
  if (!sameCanonical(result.transitions, transitions)) errors.push("persisted transitions contradict the reconstructed stable states");
  const nonMonotonic = transitions.some((transition) => transition.kind === "PASS_TO_FAIL")
    && transitions.some((transition) => transition.kind === "FAIL_TO_PASS");
  if (result.nonMonotonic !== nonMonotonic) errors.push("persisted nonMonotonic flag contradicts reconstructed transitions");
  if (result.proof.proofTransitions !== transitions.length) errors.push("proof transition count contradicts reconstructed transitions");
  const expectedProof = result.proof.dockerIsolated && result.proof.executionTrust === "NATIVE_DOCKER"
    && result.status === "COMPLETED" && transitions.length > 0;
  if (result.proof.isProof !== expectedProof) errors.push("proof isProof flag contradicts reconstructed transitions and status");
  if (expectedProof && result.proof.reason !== "Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states.") {
    errors.push("proof reason does not match a completed Docker transition proof");
  }
  if (result.proof.evidenceGrade === TURN_PATH_EVIDENCE_GRADE_PARITY_RESERVED) {
    errors.push(
      "TURN_PROOF is reserved until external validation, turn-boundary counterfactuals, prevention integration, and platform soak are met; use EXPERIMENTAL_TURN"
    );
  }
  if (expectedProof && result.proof.evidenceGrade !== TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL) {
    errors.push("until turn/Git proof parity, isProof turn packages must remain graded EXPERIMENTAL_TURN");
  }
  if (expectedProof && result.proof.evidenceLabel !== TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL) {
    errors.push(`isProof turn packages must carry evidenceLabel "${TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL}"`);
  }
  return errors;
}

function makeManifest(
  result: TurnInvestigationResult,
  frozenWitness: FrozenWitness,
  generatedAt: string,
  lifecycle: z.infer<typeof LifecycleBoundSchema>
): TurnProofBundleManifest {
  return TurnProofBundleManifestSchema.parse({
    schemaVersion: TURN_PROOF_BUNDLE_SCHEMA_VERSION,
    generatedAt,
    integrityScope: "complete-declared-file-set",
    investigationDigest: digestJson(result),
    frozenDigest: frozenWitness.frozenDigest,
    witnessDigest: frozenWitness.witnessDigest,
    lifecycle,
    artifacts: expectedManifestArtifacts(result)
  });
}

/**
 * Write a write-once portable turn proof package. Publishes only after the
 * staged directory passes independent verification against its own root digest.
 */
export async function writeTurnInvestigationProofBundle(
  outputDirectory: string,
  investigationInput: TurnInvestigationResult,
  frozenWitnessInput: FrozenWitness,
  options: TurnProofBundleWriteOptions
): Promise<WrittenTurnProofBundle> {
  const result = TurnInvestigationResultSchema.parse(investigationInput);
  const frozenWitness = FrozenWitnessSchema.parse(frozenWitnessInput);
  const semanticErrors = validateTurnInvestigationProofSemantics(result, frozenWitness);
  if (semanticErrors.length > 0) {
    throw new Error(`Cannot write semantically inconsistent turn proof bundle: ${semanticErrors.join("; ")}`);
  }
  const lifecycle = bindTurnLifecycle(options.lifecycleLedger, result);
  const generatedAt = options.generatedAt ?? new Date().toISOString();
  TimestampSchema.parse(generatedAt);
  const proofRoot = options.proofRoot ?? defaultTurnProofRoot();
  const output = prepareFreshOutput(outputDirectory, proofRoot);
  const stage = join(dirname(output), `.${relative(dirname(output), output)}.staging-${randomUUID()}`);
  let manifest: TurnProofBundleManifest | undefined;
  let rootDigest: string | undefined;
  try {
    mkdirSync(stage, { recursive: false, mode: 0o700 });
    assertNoLinksOrSpecialFiles(stage);
    const packBytes = await packTurnTrees(resolve(options.repository), result.states);
    const metadata = TurnProofSourceMetadataSchema.parse({
      schemaVersion: TURN_PROOF_SOURCE_SCHEMA_VERSION,
      trees: result.states.map((state) => ({
        stateIndex: state.index,
        turnOrdinal: state.turnOrdinal,
        treeDigest: state.treeDigest,
        snapshotDigest: state.snapshotDigest
      })),
      pack: {
        path: "source/trees.pack",
        digest: digestBytes(packBytes),
        bytes: packBytes.length
      }
    });
    manifest = makeManifest(result, frozenWitness, generatedAt, lifecycle.binding);
    const artifacts = new Map<string, Buffer>();
    const put = (path: string, bytes: Buffer): void => {
      if (!isSafeRelativeArtifactPath(path)) throw new Error(`Unsafe generated artifact path: ${path}`);
      if (artifacts.has(path)) throw new Error(`Turn proof bundle generated duplicate artifact: ${path}`);
      artifacts.set(path, bytes);
    };
    put("manifest.json", jsonBytes(manifest));
    put("investigation.json", jsonBytes(result));
    put("witness/frozen.json", jsonBytes(frozenWitness));
    put("source/trees.metadata.json", jsonBytes(metadata));
    put("source/trees.pack", packBytes);
    put("VERIFY.md", verificationReadme());
    put("lifecycle/ledger.json", jsonBytes(lifecycle.ledger));
    for (const run of result.runs) put(runArtifactPath(run), jsonBytes(run));
    for (const [index, transition] of result.transitions.entries()) put(transitionArtifactPath(index), jsonBytes(transition));
    const expected = requiredArtifactPaths(result);
    const actual = [...artifacts.keys()].sort((left, right) => left.localeCompare(right));
    if (!sameCanonical(expected, actual)) throw new Error("Turn proof writer did not produce the complete expected artifact set.");
    for (const [artifact, bytes] of artifacts) {
      writeArtifact(stage, artifact, bytes);
    }
    const hashes = Buffer.from(`${[...artifacts.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([artifact, bytes]) => `${sha256(bytes)}  ${artifact}`)
      .join("\n")}\n`, "utf8");
    rootDigest = digestBytes(hashes);
    writeArtifact(stage, "hashes.txt", hashes);
    writeArtifact(stage, "ROOT.sha256", Buffer.from(`${rootDigest}\n`, "utf8"));
    const stageVerification = await verifyTurnInvestigationProofBundle(stage, rootDigest);
    if (!stageVerification.valid) {
      throw new Error(`Refusing to publish an invalid turn proof bundle: ${stageVerification.errors.join("; ")}`);
    }
    if (existsSync(output)) throw new Error(`Turn proof bundle output appeared during assembly and will not be replaced: ${output}`);
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
 * Verify all raw artifacts without executing a witness. Reconstructs stable
 * transitions from run facts and checks the portable tree pack.
 */
export async function verifyTurnInvestigationProofBundle(
  directory: string,
  expectedRoot?: string
): Promise<TurnProofBundleVerification> {
  const errors: string[] = [];
  let rootDigest: string | null = null;
  let externalRootStatus: TurnProofBundleExternalRootStatus = expectedRoot === undefined ? "NOT_PROVIDED" : "MISMATCH";
  let manifest: TurnProofBundleManifest | undefined;
  try {
    const root = resolve(directory);
    if (!existsSync(root)) {
      return { valid: false, checkedFiles: 0, errors: ["Turn proof bundle directory does not exist"], rootDigest, externalRootStatus };
    }
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
        errors.push("externally supplied turn proof root is not a sha256 digest");
      } else if (expectedRoot === rootDigest) {
        externalRootStatus = "MATCH";
      } else {
        errors.push("externally supplied turn proof root does not match");
      }
    }
    const catalog = parseHashCatalog(hashes, errors);
    let declaredBytes = 0;
    for (const [artifact, digest] of catalog) {
      try {
        const path = safeArtifactPath(root, artifact);
        declaredBytes += assertRegularFile(path, `declared artifact ${artifact}`);
        if (declaredBytes > MAX_TURN_PROOF_TOTAL_BYTES) {
          errors.push("declared artifacts exceed FaultLine's total verification read limit");
          break;
        }
        if (sha256(readBoundedFile(path, `declared artifact ${artifact}`)) !== digest) {
          errors.push(`artifact digest mismatch: ${artifact}`);
        }
      } catch (error) {
        errors.push(`declared artifact cannot be read safely (${artifact}): ${errorMessage(error)}`);
      }
    }

    const manifestPayload = parseJsonFile(root, "manifest.json", "manifest", errors);
    const investigationPayload = parseJsonFile(root, "investigation.json", "investigation", errors);
    const frozenPayload = parseJsonFile(root, "witness/frozen.json", "frozen witness", errors);
    const metadataPayload = parseJsonFile(root, "source/trees.metadata.json", "source metadata", errors);
    const parsedManifest = TurnProofBundleManifestSchema.safeParse(manifestPayload);
    if (!parsedManifest.success) errors.push(`manifest schema validation failed: ${parsedManifest.error.message}`);
    else manifest = parsedManifest.data;
    const parsedResult = TurnInvestigationResultSchema.safeParse(investigationPayload);
    if (!parsedResult.success) errors.push(`investigation schema validation failed: ${parsedResult.error.message}`);
    const parsedFrozen = FrozenWitnessSchema.safeParse(frozenPayload);
    if (!parsedFrozen.success) errors.push(`frozen witness schema validation failed: ${parsedFrozen.error.message}`);
    const parsedMetadata = TurnProofSourceMetadataSchema.safeParse(metadataPayload);
    if (!parsedMetadata.success) errors.push(`source metadata schema validation failed: ${parsedMetadata.error.message}`);

    if (manifest && parsedResult.success && parsedFrozen.success && parsedMetadata.success) {
      const result = parsedResult.data;
      const frozenWitness = parsedFrozen.data;
      const metadata = parsedMetadata.data;
      if (manifest.investigationDigest !== digestJson(result)) {
        errors.push("manifest investigation digest does not match investigation.json");
      }
      if (manifest.frozenDigest !== frozenWitness.frozenDigest || manifest.witnessDigest !== frozenWitness.witnessDigest) {
        errors.push("manifest frozen or witness digest does not match frozen witness artifact");
      }
      if (!sameCanonical(manifest.artifacts, expectedManifestArtifacts(result))) {
        errors.push("manifest artifact index does not exactly match the investigation run and transition catalog");
      }
      const expectedArtifacts = new Set(requiredArtifactPaths(result));
      for (const artifact of expectedArtifacts) {
        if (!catalog.has(artifact)) errors.push(`required artifact is missing from hashes.txt: ${artifact}`);
      }
      for (const artifact of catalog.keys()) {
        if (!expectedArtifacts.has(artifact)) errors.push(`hashes.txt contains an unexpected artifact: ${artifact}`);
      }
      const physical = new Set(collectFiles(root));
      const expectedPhysical = new Set([...expectedArtifacts, "hashes.txt", "ROOT.sha256"]);
      for (const artifact of physical) {
        if (!expectedPhysical.has(artifact)) errors.push(`undeclared physical file exists in turn proof bundle: ${artifact}`);
      }
      for (const artifact of expectedPhysical) {
        if (!physical.has(artifact)) errors.push(`expected physical file is missing from turn proof bundle: ${artifact}`);
      }

      const runs = new Map(result.runs.map((run) => [run.runId, run]));
      for (const descriptor of manifest.artifacts.runs) {
        const payload = parseJsonFile(root, descriptor.path, `run ${descriptor.runId}`, errors);
        const parsed = TurnInvestigationRunFactSchema.safeParse(payload);
        if (!parsed.success) {
          errors.push(`run artifact schema validation failed for ${descriptor.runId}: ${parsed.error.message}`);
        } else if (parsed.data.runId !== descriptor.runId || !sameCanonical(parsed.data, runs.get(descriptor.runId))) {
          errors.push(`run artifact does not match investigation run catalog: ${descriptor.runId}`);
        }
      }
      for (const [index, descriptor] of manifest.artifacts.transitions.entries()) {
        const payload = parseJsonFile(root, descriptor.path, `transition ${String(index)}`, errors);
        const parsed = StableTurnTransitionSchema.safeParse(payload);
        if (!parsed.success) {
          errors.push(`transition artifact schema validation failed for ${String(index)}: ${parsed.error.message}`);
        } else if (descriptor.index !== index || !sameCanonical(parsed.data, result.transitions[index])) {
          errors.push(`transition artifact does not match investigation transition catalog: ${String(index)}`);
        }
      }

      const ledgerPayload = parseJsonFile(root, "lifecycle/ledger.json", "lifecycle ledger", errors);
      const parsedLedger = CodexLifecycleLedgerSchema.safeParse(ledgerPayload);
      if (!parsedLedger.success) {
        errors.push(`lifecycle ledger schema validation failed: ${parsedLedger.error.message}`);
      } else {
        try {
          const rebound = bindTurnLifecycle(parsedLedger.data, result).binding;
          if (!sameCanonical(rebound, manifest.lifecycle)) {
            errors.push("lifecycle ledger binding does not match its valid reconstruction");
          }
        } catch (error) {
          errors.push(`lifecycle ledger cannot bind to the investigation: ${errorMessage(error)}`);
        }
      }

      if (!sameCanonical(
        metadata.trees,
        result.states.map((state) => ({
          stateIndex: state.index,
          turnOrdinal: state.turnOrdinal,
          treeDigest: state.treeDigest,
          snapshotDigest: state.snapshotDigest
        }))
      )) {
        errors.push("source tree metadata does not match investigation states");
      }
      const packPath = safeArtifactPath(root, "source/trees.pack");
      const packBytes = readBoundedFile(packPath, "source/trees.pack");
      if (digestBytes(packBytes) !== metadata.pack.digest || packBytes.length !== metadata.pack.bytes) {
        errors.push("source trees.pack digest or size does not match metadata");
      }
      await verifyTreesPack(packBytes, result.states, errors);

      const witnessVerification = verifyFrozenWitnessRecord(frozenWitness, frozenWitness.frozenDigest);
      if (!witnessVerification.valid || witnessVerification.externalDigestStatus !== "MATCH") {
        errors.push(...witnessVerification.errors.map((error) => `frozen witness verification failed: ${error}`));
      }
      errors.push(...validateTurnInvestigationProofSemantics(result, frozenWitness));
    }
    return manifest
      ? { valid: errors.length === 0, checkedFiles: catalog.size, errors, rootDigest, externalRootStatus, manifest }
      : { valid: false, checkedFiles: catalog.size, errors, rootDigest, externalRootStatus };
  } catch (error) {
    errors.push(`Turn proof bundle verification failed safely: ${errorMessage(error)}`);
    return manifest
      ? { valid: false, checkedFiles: 0, errors, rootDigest, externalRootStatus, manifest }
      : { valid: false, checkedFiles: 0, errors, rootDigest, externalRootStatus };
  }
}
