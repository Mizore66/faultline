import { randomUUID } from "node:crypto";
import {
  closeSync,
  existsSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  writeFileSync
} from "node:fs";
import { dirname, isAbsolute, join, parse, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson } from "./canonical.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";

/**
 * Independently verifiable three-state prevention package.
 * Bound to an original Git proof root + frozen witness; never inferred from a repair brief alone.
 */
export const PREVENTION_PROOF_SCHEMA_VERSION = "faultline.prevention-proof.v1" as const;

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);
const CommitSchema = z.string().regex(/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/);
const RunIdListSchema = z.array(DigestSchema).length(3);

const PreventionExecutedStateSchema = z.object({
  role: z.enum(["LAST_GOOD", "FIRST_BAD", "REPAIRED"]),
  commit: CommitSchema,
  tree: CommitSchema.optional(),
  verdict: z.enum(["PASS", "FAIL"]),
  witnessDigest: DigestSchema,
  environmentDigest: DigestSchema,
  executionTrust: z.literal("NATIVE_DOCKER"),
  executionKind: z.literal("EXECUTED"),
  distinctExecutionCount: z.number().int().min(3),
  runIds: RunIdListSchema
}).strict();

const PreventionRepairedRunBindingSchema = z.object({
  runId: DigestSchema,
  executionId: DigestSchema,
  commit: CommitSchema,
  tree: CommitSchema,
  verdict: z.literal("PASS"),
  witnessDigest: DigestSchema,
  environmentDigest: DigestSchema,
  executionTrust: z.literal("NATIVE_DOCKER"),
  executionKind: z.literal("EXECUTED")
}).strict();

export const PreventionProofBodySchema = z.object({
  schemaVersion: z.literal(PREVENTION_PROOF_SCHEMA_VERSION),
  originalProofRoot: DigestSchema,
  frozenWitnessDigest: DigestSchema,
  investigationDigest: DigestSchema.optional(),
  lastGood: PreventionExecutedStateSchema.extend({
    role: z.literal("LAST_GOOD"),
    verdict: z.literal("PASS")
  }),
  firstBad: PreventionExecutedStateSchema.extend({
    role: z.literal("FIRST_BAD"),
    verdict: z.literal("FAIL")
  }),
  repaired: PreventionExecutedStateSchema.extend({
    role: z.literal("REPAIRED"),
    verdict: z.literal("PASS")
  }),
  repairBaseTree: CommitSchema.optional(),
  repairPatchDigest: DigestSchema.optional(),
  codexThreadId: z.string().min(1).max(256).optional(),
  hardGuardArtifactDigests: z.array(DigestSchema).max(64).optional(),
  verified: z.literal(true),
  limitations: z.array(z.string().min(1)).min(1).max(16)
}).strict();

export const PreventionProofClassificationSchema = z.enum([
  "PREVENTION_EVIDENCE_SUMMARY",
  "PREVENTION_VERIFIED"
]);

export const PreventionProofManifestSchema = z.object({
  schemaVersion: z.literal(PREVENTION_PROOF_SCHEMA_VERSION),
  /**
   * PREVENTION_VERIFIED: grounded at write time from a verified Git proof bundle
   * plus repaired run bindings. PREVENTION_EVIDENCE_SUMMARY: caller-supplied only.
   */
  classification: PreventionProofClassificationSchema,
  prevention: z.object({
    path: z.literal("prevention.json"),
    digest: DigestSchema
  }).strict(),
  limitations: z.array(z.string().min(1)).min(1).max(16),
  rootDigest: DigestSchema
}).strict();

export const PreventionRepairedRunsArtifactSchema = z.object({
  schemaVersion: z.literal("faultline.prevention-repaired-runs.v1"),
  runs: z.array(PreventionRepairedRunBindingSchema).length(3)
}).strict();

export type PreventionProofBody = z.infer<typeof PreventionProofBodySchema>;
export type PreventionProofManifest = z.infer<typeof PreventionProofManifestSchema>;
export type PreventionProofClassification = z.infer<typeof PreventionProofClassificationSchema>;
export type PreventionProofExternalRootStatus = "NOT_PROVIDED" | "MATCH" | "MISMATCH";
export type PreventionRepairedRunBinding = z.infer<typeof PreventionRepairedRunBindingSchema>;

export type PreventionProofVerification = {
  readonly valid: boolean;
  readonly errors: readonly string[];
  readonly manifest: PreventionProofManifest | null;
  readonly prevention: PreventionProofBody | null;
  readonly rootDigest: string | null;
  readonly externalRootStatus: PreventionProofExternalRootStatus;
};

export type WrittenPreventionProof = {
  readonly directory: string;
  readonly manifest: PreventionProofManifest;
  readonly prevention: PreventionProofBody;
  readonly rootDigest: string;
};

const BASE_ARTIFACTS = ["README.md", "prevention.json", "manifest.json"] as const;

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function unsignedManifest(manifest: PreventionProofManifest): Omit<PreventionProofManifest, "rootDigest"> {
  const { rootDigest: _rootDigest, ...unsigned } = manifest;
  return unsigned;
}

/** Managed root so prevention packages do not spill into an arbitrary directory. */
export function defaultPreventionProofRoot(): string {
  return resolve(".faultline", "prevention-proofs");
}

function assertNoLinksOrSpecialFiles(directory: string): void {
  const stat = lstatSync(directory);
  if (stat.isSymbolicLink() || !stat.isDirectory()) {
    throw new Error(`Prevention proof directory must be a real directory: ${directory}`);
  }
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const child = join(directory, entry.name);
    const childStat = lstatSync(child);
    if (childStat.isSymbolicLink() || (!childStat.isDirectory() && !childStat.isFile())) {
      throw new Error(`Prevention proof directory contains a symbolic link or special file: ${child}`);
    }
    if (childStat.isDirectory()) assertNoLinksOrSpecialFiles(child);
  }
}

function ensureRealDirectoryTree(directory: string): void {
  const absolute = resolve(directory);
  const root = parse(absolute).root;
  const suffix = relative(root, absolute);
  const parts = suffix ? suffix.split(/[\\/]+/).filter(Boolean) : [];
  let current = root;
  if (existsSync(current)) {
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) throw new Error(`Prevention proof root is not a real directory: ${current}`);
    current = safeCurrent;
  }
  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) {
      throw new Error(`Prevention proof output cannot traverse a symbolic link or non-directory: ${current}`);
    }
    current = safeCurrent;
  }
}

function isNested(root: string, candidate: string): boolean {
  const nested = relativeTrustedSystemPath(root, candidate);
  return Boolean(nested) && !nested.startsWith("..") && !isAbsolute(nested);
}

export function preparePreventionProofOutput(outputDirectory: string, preventionRoot = defaultPreventionProofRoot()): string {
  const root = resolve(preventionRoot);
  const output = resolve(outputDirectory);
  if (!isNested(root, output)) {
    throw new Error(`Prevention proof output must be a child directory of ${root}`);
  }
  ensureRealDirectoryTree(root);
  ensureRealDirectoryTree(dirname(output));
  if (existsSync(output)) {
    throw new Error(`Prevention proof output already exists and will not be replaced: ${output}`);
  }
  return output;
}

function writePrivateFile(path: string, body: string): void {
  let descriptor: number | undefined;
  try {
    descriptor = openSync(path, "wx", 0o600);
    writeFileSync(descriptor, body, "utf8");
    fsyncSync(descriptor);
    closeSync(descriptor);
    descriptor = undefined;
  } finally {
    if (descriptor !== undefined) closeSync(descriptor);
  }
}

function syncDirectory(directory: string): void {
  try {
    const descriptor = openSync(directory, "r");
    try {
      fsyncSync(descriptor);
    } finally {
      closeSync(descriptor);
    }
  } catch {
    // Windows filesystems commonly reject directory fsync.
  }
}

function readJson(path: string, label: string, errors: string[]): unknown {
  try {
    return JSON.parse(readFileSync(path, "utf8")) as unknown;
  } catch (error) {
    errors.push(`Unable to read ${label}: ${errorMessage(error)}`);
    return undefined;
  }
}

/** Semantic gates for a verified three-state prevention claim. */
export function validatePreventionProofSemantics(body: PreventionProofBody): string[] {
  const errors: string[] = [];
  const states = [body.lastGood, body.firstBad, body.repaired];
  if (!body.verified) errors.push("prevention package must set verified=true");
  if (body.lastGood.verdict !== "PASS" || body.firstBad.verdict !== "FAIL" || body.repaired.verdict !== "PASS") {
    errors.push("three-state prevention requires PASS → FAIL → PASS");
  }
  if (new Set(states.map((state) => state.witnessDigest)).size !== 1) {
    errors.push("all three states must share the same frozen witness digest");
  }
  if (body.frozenWitnessDigest !== body.lastGood.witnessDigest) {
    errors.push("package frozenWitnessDigest must match executed witness digests");
  }
  if (new Set(states.map((state) => state.environmentDigest)).size !== 1) {
    errors.push("all three states must share the same environment digest");
  }
  if (states.some((state) => state.executionTrust !== "NATIVE_DOCKER" || state.executionKind !== "EXECUTED")) {
    errors.push("prevention requires NATIVE_DOCKER EXECUTED evidence on every state");
  }
  if (states.some((state) => state.distinctExecutionCount < 3)) {
    errors.push("prevention requires at least three distinct executions per state");
  }
  if (new Set(states.map((state) => state.commit)).size !== 3) {
    errors.push("last-good, first-bad, and repaired commits must be distinct");
  }
  for (const state of states) {
    if (state.runIds.length !== 3 || new Set(state.runIds).size !== 3) {
      errors.push(`${state.role} requires three distinct runIds`);
    }
    if (state.distinctExecutionCount !== state.runIds.length) {
      errors.push(`${state.role} distinctExecutionCount must equal runIds length`);
    }
  }
  if (body.repairBaseTree !== undefined && body.firstBad.tree !== undefined && body.repairBaseTree !== body.firstBad.tree) {
    errors.push("repairBaseTree must match first-bad tree when both are present");
  }
  return errors;
}

function validateRepairedRunsArtifact(
  body: PreventionProofBody,
  artifact: z.infer<typeof PreventionRepairedRunsArtifactSchema> | null
): string[] {
  if (artifact === null) return [];
  const errors: string[] = [];
  const ids = artifact.runs.map((run) => run.runId);
  if (ids.join("\0") !== body.repaired.runIds.join("\0")) {
    errors.push("repaired-runs.json runIds must match prevention.repaired.runIds in order");
  }
  for (const run of artifact.runs) {
    if (run.commit !== body.repaired.commit || run.tree !== body.repaired.tree) {
      errors.push(`repaired run ${run.runId} commit/tree does not match repaired state`);
    }
    if (run.witnessDigest !== body.frozenWitnessDigest) {
      errors.push(`repaired run ${run.runId} witness digest mismatch`);
    }
  }
  return errors;
}

/** Recheck a stored package without executing repository code. */
export function verifyPreventionProof(
  directory: string,
  expectedRootDigest?: string
): PreventionProofVerification {
  const errors: string[] = [];
  let manifest: PreventionProofManifest | null = null;
  let prevention: PreventionProofBody | null = null;
  let rootDigest: string | null = null;
  let externalRootStatus: PreventionProofExternalRootStatus = expectedRootDigest === undefined ? "NOT_PROVIDED" : "MISMATCH";
  try {
    const root = resolve(directory);
    if (!existsSync(root)) {
      return { valid: false, errors: ["Prevention proof directory does not exist"], manifest: null, prevention: null, rootDigest: null, externalRootStatus };
    }
    assertNoLinksOrSpecialFiles(root);
    const physical = new Set(readdirSync(root, { withFileTypes: true }).map((entry) => entry.name));
    for (const expected of BASE_ARTIFACTS) {
      if (!physical.has(expected)) errors.push(`Prevention proof package is missing ${expected}`);
    }
    for (const actual of physical) {
      if (
        actual !== "repaired-runs.json"
        && !(BASE_ARTIFACTS as readonly string[]).includes(actual)
      ) {
        errors.push(`Prevention proof package contains an unexpected artifact: ${actual}`);
      }
      const path = join(root, actual);
      const stat = lstatSync(path);
      if (!stat.isFile() || stat.isSymbolicLink()) errors.push(`Prevention proof artifact must be a regular non-symlink file: ${actual}`);
    }

    const manifestParsed = PreventionProofManifestSchema.safeParse(readJson(join(root, "manifest.json"), "prevention proof manifest", errors));
    if (!manifestParsed.success) {
      errors.push(`Prevention proof manifest schema validation failed: ${manifestParsed.error.message}`);
    } else {
      manifest = manifestParsed.data;
      rootDigest = manifest.rootDigest;
      if (manifest.rootDigest !== digestJson(unsignedManifest(manifest))) {
        errors.push("Prevention proof root digest does not match its canonical contents.");
      }
      if (expectedRootDigest !== undefined) {
        externalRootStatus = manifest.rootDigest === expectedRootDigest ? "MATCH" : "MISMATCH";
        if (externalRootStatus === "MISMATCH") {
          errors.push("Prevention proof root digest does not match the externally supplied digest.");
        }
      }
    }

    const bodyParsed = PreventionProofBodySchema.safeParse(readJson(join(root, "prevention.json"), "prevention body", errors));
    if (!bodyParsed.success) {
      errors.push(`Prevention proof body schema validation failed: ${bodyParsed.error.message}`);
    } else {
      prevention = bodyParsed.data;
      errors.push(...validatePreventionProofSemantics(prevention));
      if (manifest && manifest.prevention.digest !== digestJson(prevention)) {
        errors.push("Prevention proof manifest digest does not match prevention.json.");
      }
      if (manifest?.classification === "PREVENTION_VERIFIED") {
        if (!prevention.repairPatchDigest) errors.push("PREVENTION_VERIFIED requires repairPatchDigest");
        if (!prevention.repairBaseTree) errors.push("PREVENTION_VERIFIED requires repairBaseTree");
        if (!physical.has("repaired-runs.json")) {
          errors.push("PREVENTION_VERIFIED package must include repaired-runs.json");
        }
      }
    }

    if (physical.has("repaired-runs.json") && prevention) {
      const repairedParsed = PreventionRepairedRunsArtifactSchema.safeParse(
        readJson(join(root, "repaired-runs.json"), "repaired runs artifact", errors)
      );
      if (!repairedParsed.success) {
        errors.push(`repaired-runs.json schema validation failed: ${repairedParsed.error.message}`);
      } else {
        errors.push(...validateRepairedRunsArtifact(prevention, repairedParsed.data));
      }
    }
  } catch (error) {
    errors.push(`Prevention proof verification failed safely: ${errorMessage(error)}`);
  }
  return { valid: errors.length === 0, errors, manifest, prevention, rootDigest, externalRootStatus };
}

export type PreventionProofWriteInput = {
  readonly originalProofRoot: string;
  readonly frozenWitnessDigest: string;
  readonly investigationDigest?: string;
  readonly lastGood: Omit<PreventionProofBody["lastGood"], "role" | "verdict"> & { verdict?: "PASS" };
  readonly firstBad: Omit<PreventionProofBody["firstBad"], "role" | "verdict"> & { verdict?: "FAIL" };
  readonly repaired: Omit<PreventionProofBody["repaired"], "role" | "verdict"> & { verdict?: "PASS" };
  readonly repairBaseTree?: string;
  readonly repairPatchDigest?: string;
  readonly codexThreadId?: string;
  readonly hardGuardArtifactDigests?: readonly string[];
  /** VERIFIED requires repairPatchDigest, repairBaseTree, and repairedRunBindings. */
  readonly grounding?: "SUMMARY" | "VERIFIED";
  readonly repairedRunBindings?: readonly PreventionRepairedRunBinding[];
};

/**
 * Store a write-once prevention package after semantic validation.
 * Callers must supply already-executed NATIVE_DOCKER three-state evidence.
 */
export function writePreventionProof(
  outputDirectory: string,
  input: PreventionProofWriteInput
): WrittenPreventionProof {
  const grounding = input.grounding === "VERIFIED" ? "PREVENTION_VERIFIED" : "PREVENTION_EVIDENCE_SUMMARY";
  if (grounding === "PREVENTION_VERIFIED") {
    if (!input.repairPatchDigest) throw new Error("Grounded prevention write requires repairPatchDigest.");
    if (!input.repairBaseTree) throw new Error("Grounded prevention write requires repairBaseTree.");
    if (!input.repairedRunBindings || input.repairedRunBindings.length !== 3) {
      throw new Error("Grounded prevention write requires three repairedRunBindings.");
    }
  }

  const limitations = grounding === "PREVENTION_VERIFIED"
    ? [
      "PREVENTION_VERIFIED binds last-good and first-bad run IDs from a verified Git proof bundle plus three repaired-state NATIVE_DOCKER run bindings under one frozen witness digest.",
      "Offline verify checks package integrity, run-ID self-consistency, and repaired-runs.json; it does not re-execute Docker.",
      "This package does not claim model intent, a unique semantic root cause, or host/Docker-daemon attestation beyond the recorded execution trust."
    ]
    : [
      "PREVENTION_EVIDENCE_SUMMARY records caller-supplied last-good PASS, first-bad FAIL, and repaired PASS fields under one frozen witness digest.",
      "Offline verify checks package integrity and internal field consistency; use fl prevention write --from-bundle for PREVENTION_VERIFIED grounding.",
      "This package does not claim model intent, a unique semantic root cause, or host/Docker-daemon attestation beyond the recorded execution trust."
    ];

  const body = PreventionProofBodySchema.parse({
    schemaVersion: PREVENTION_PROOF_SCHEMA_VERSION,
    originalProofRoot: input.originalProofRoot,
    frozenWitnessDigest: input.frozenWitnessDigest,
    ...(input.investigationDigest === undefined ? {} : { investigationDigest: input.investigationDigest }),
    lastGood: {
      ...input.lastGood,
      role: "LAST_GOOD",
      verdict: "PASS"
    },
    firstBad: {
      ...input.firstBad,
      role: "FIRST_BAD",
      verdict: "FAIL"
    },
    repaired: {
      ...input.repaired,
      role: "REPAIRED",
      verdict: "PASS"
    },
    ...(input.repairBaseTree === undefined ? {} : { repairBaseTree: input.repairBaseTree }),
    ...(input.repairPatchDigest === undefined ? {} : { repairPatchDigest: input.repairPatchDigest }),
    ...(input.codexThreadId === undefined ? {} : { codexThreadId: input.codexThreadId }),
    ...(input.hardGuardArtifactDigests === undefined
      ? {}
      : { hardGuardArtifactDigests: [...input.hardGuardArtifactDigests] }),
    verified: true,
    limitations
  });
  const semanticErrors = validatePreventionProofSemantics(body);
  if (semanticErrors.length > 0) {
    throw new Error(`Refusing to store an invalid prevention proof: ${semanticErrors.join("; ")}`);
  }

  const repairedArtifact = input.repairedRunBindings === undefined
    ? null
    : PreventionRepairedRunsArtifactSchema.parse({
      schemaVersion: "faultline.prevention-repaired-runs.v1",
      runs: input.repairedRunBindings
    });
  if (repairedArtifact) {
    const bindingErrors = validateRepairedRunsArtifact(body, repairedArtifact);
    if (bindingErrors.length > 0) {
      throw new Error(`Refusing to store inconsistent repaired run bindings: ${bindingErrors.join("; ")}`);
    }
  }

  const output = preparePreventionProofOutput(outputDirectory);
  const stage = join(dirname(output), `.${output.split(/[\\/]/).at(-1) ?? "prevention"}.${randomUUID()}.tmp`);
  const unsigned = {
    schemaVersion: PREVENTION_PROOF_SCHEMA_VERSION,
    classification: grounding,
    prevention: { path: "prevention.json" as const, digest: digestJson(body) },
    limitations: body.limitations
  };
  const manifest = PreventionProofManifestSchema.parse({
    ...unsigned,
    rootDigest: digestJson(unsigned)
  });

  try {
    mkdirSync(stage, { mode: 0o700 });
    const statusLine = grounding === "PREVENTION_VERIFIED"
      ? "Status: **Prevention verified** (grounded in verified proof-bundle run IDs + repaired run bindings)"
      : "Status: **PREVENTION_EVIDENCE_SUMMARY** (not fully grounded Prevention verified)";
    const readme = [
      "# FaultLine prevention proof",
      "",
      statusLine,
      "",
      "This package binds an original Git proof root, a frozen witness digest, and three NATIVE_DOCKER state summaries (PASS → FAIL → PASS) with per-state run IDs.",
      "",
      "Verify offline with `fl verify <this-directory> --expect-root <retained-root>` (or `fl prevention verify`).",
      "It does not claim model intent or a unique semantic root cause."
    ].join("\n");
    writePrivateFile(join(stage, "README.md"), `${readme}\n`);
    writePrivateFile(join(stage, "prevention.json"), `${canonicalJson(body)}\n`);
    writePrivateFile(join(stage, "manifest.json"), `${canonicalJson(manifest)}\n`);
    if (repairedArtifact) {
      writePrivateFile(join(stage, "repaired-runs.json"), `${canonicalJson(repairedArtifact)}\n`);
    }
    syncDirectory(stage);

    const verification = verifyPreventionProof(stage);
    if (!verification.valid) {
      throw new Error(`Refusing to publish an invalid prevention proof: ${verification.errors.join("; ")}`);
    }
    if (existsSync(output)) {
      throw new Error(`Prevention proof output appeared during assembly and will not be replaced: ${output}`);
    }
    renameSync(stage, output);
    syncDirectory(dirname(output));
    return { directory: output, manifest, prevention: body, rootDigest: manifest.rootDigest };
  } catch (error) {
    if (existsSync(stage)) rmSync(stage, { recursive: true, force: true });
    throw error;
  }
}
