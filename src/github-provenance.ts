import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, parse, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson } from "./canonical.js";
import { loadVerifiedGitProofView, type VerifiedGitProofView } from "./git-proof-view.js";
import { relativeTrustedSystemPath, resolveSafeDirectorySegment } from "./safe-directory.js";

export const GITHUB_PROVENANCE_SCHEMA_VERSION = "faultline.github-artifact-provenance.v1";
export const GITHUB_PROVENANCE_TRUST_SCHEMA_VERSION = "faultline.github-artifact-attestation-trust.v1";

export const GITHUB_PROVENANCE_LIMITATION = "A GitHub artifact attestation can bind this receipt's exact bytes to a configured GitHub Actions workflow. It does not independently attest the runner, Docker daemon, wall-clock time, or host enforcement of the recorded sandbox policy.";

const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const GIT_OBJECT_ID = /^[0-9a-f]{40,64}$/;
const REPOSITORY = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
const MAX_RECEIPT_BYTES = 1_000_000;

const DigestSchema = z.string().regex(SHA256_DIGEST, "expected sha256:<64 lowercase hex characters>");
const GitObjectIdSchema = z.string().regex(GIT_OBJECT_ID, "expected a Git object id");
const TimestampSchema = z.string().datetime({ offset: true }).refine((value) => {
  const parsed = new Date(value);
  return Number.isFinite(parsed.getTime()) && parsed.toISOString() === value;
}, "expected a canonical UTC ISO-8601 timestamp");
const RepositorySchema = z.string().regex(REPOSITORY, "expected an owner/repository identifier");
const NonEmptyIdentifierSchema = z.string().trim().min(1).max(1_024).refine((value) => !/[\u0000-\u001f\u007f]/.test(value), "must not contain control characters");

const GitEndpointSchema = z.object({
  index: z.number().int().nonnegative(),
  commit: GitObjectIdSchema,
  tree: GitObjectIdSchema
}).strict();

const SandboxBindingSchema = z.object({
  image: z.string().regex(/@sha256:[a-f0-9]{64}$/, "expected a digest-pinned container image"),
  policyDigest: DigestSchema,
  environmentPolicyDigest: DigestSchema
}).strict();

export const GithubActionsIdentitySchema = z.object({
  provider: z.literal("GITHUB_ACTIONS"),
  repository: RepositorySchema,
  workflowRef: NonEmptyIdentifierSchema,
  ref: NonEmptyIdentifierSchema,
  commit: GitObjectIdSchema,
  tree: GitObjectIdSchema,
  runId: z.string().regex(/^\d+$/, "expected a numeric GitHub run id"),
  runAttempt: z.number().int().positive(),
  eventName: z.literal("push")
}).strict();

const UnsignedGithubProvenanceReceiptSchema = z.object({
  schemaVersion: z.literal(GITHUB_PROVENANCE_SCHEMA_VERSION),
  receiptKind: z.literal("GITHUB_ARTIFACT_ATTESTATION_SUBJECT"),
  createdAt: TimestampSchema,
  signing: z.object({
    mechanism: z.literal("GITHUB_ARTIFACT_ATTESTATION"),
    state: z.literal("AWAITING_EXTERNAL_SIGNATURE")
  }).strict(),
  proof: z.object({
    rootDigest: DigestSchema,
    manifestDigest: DigestSchema,
    investigationDigest: DigestSchema,
    frozenDigest: DigestSchema,
    witnessDigest: DigestSchema,
    approvalDigest: DigestSchema,
    range: z.object({
      ancestor: GitEndpointSchema,
      descendant: GitEndpointSchema
    }).strict(),
    sandboxBindings: z.array(SandboxBindingSchema).min(1),
    execution: z.object({
      runIds: z.array(DigestSchema).min(1),
      executionIds: z.array(DigestSchema).min(1),
      startedAt: TimestampSchema,
      finishedAt: TimestampSchema,
      executionCount: z.number().int().positive()
    }).strict()
  }).strict(),
  ci: GithubActionsIdentitySchema,
  limitation: z.literal(GITHUB_PROVENANCE_LIMITATION)
}).strict();

export const GithubProvenanceReceiptSchema = UnsignedGithubProvenanceReceiptSchema.extend({
  receiptDigest: DigestSchema
}).strict();

export const GithubArtifactAttestationTrustSchema = z.object({
  schemaVersion: z.literal(GITHUB_PROVENANCE_TRUST_SCHEMA_VERSION),
  repository: RepositorySchema,
  signerWorkflow: NonEmptyIdentifierSchema,
  sourceRef: NonEmptyIdentifierSchema,
  eventName: z.literal("push"),
  denySelfHostedRunners: z.boolean(),
  trustedRootFile: z.string().trim().min(1).max(4_096),
  sourceDigest: GitObjectIdSchema.optional()
}).strict();

export type GithubActionsIdentity = z.infer<typeof GithubActionsIdentitySchema>;
export type GithubProvenanceReceipt = z.infer<typeof GithubProvenanceReceiptSchema>;
export type GithubArtifactAttestationTrust = z.infer<typeof GithubArtifactAttestationTrustSchema>;

export type ProvenanceBindingVerification = {
  readonly valid: boolean;
  readonly errors: readonly string[];
  readonly receiptDigest: string | null;
  readonly proofRootDigest: string | null;
  readonly receipt?: GithubProvenanceReceipt;
};

export type GithubArtifactAttestationVerification = {
  readonly valid: boolean;
  readonly errors: readonly string[];
  readonly command: readonly string[];
};

export type SignedGithubProvenanceVerification = {
  readonly valid: boolean;
  readonly assurance: "GITHUB_ARTIFACT_ATTESTATION_VERIFIED" | "NOT_VERIFIED";
  readonly errors: readonly string[];
  readonly binding: ProvenanceBindingVerification;
  readonly artifactAttestation: GithubArtifactAttestationVerification;
  readonly limitation: typeof GITHUB_PROVENANCE_LIMITATION;
};

export type GithubArtifactAttestationRunner = (
  receiptFile: string,
  attestationBundleFile: string,
  trustInput: unknown,
  sourceCommit: string
) => GithubArtifactAttestationVerification;

function receiptPayload(receipt: GithubProvenanceReceipt | z.infer<typeof UnsignedGithubProvenanceReceiptSchema>): z.infer<typeof UnsignedGithubProvenanceReceiptSchema> {
  const { receiptDigest: _receiptDigest, ...payload } = receipt as GithubProvenanceReceipt;
  return UnsignedGithubProvenanceReceiptSchema.parse(payload);
}

function canonicalTimestamp(value: string | Date | undefined): string {
  const timestamp = value === undefined ? new Date().toISOString() : value instanceof Date ? value.toISOString() : value;
  return TimestampSchema.parse(timestamp);
}

function sortedUnique(values: readonly string[]): string[] {
  return [...new Set(values)].sort((left, right) => left.localeCompare(right));
}

/**
 * Construct the receipt entirely from an already verified proof view. The
 * caller cannot provide a root, image, policy, commit, tree, or execution time
 * independently of the verified package.
 */
export function createGithubProvenanceReceiptFromView(
  view: VerifiedGitProofView,
  ciIdentity: unknown,
  createdAt?: string | Date
): GithubProvenanceReceipt {
  const ci = GithubActionsIdentitySchema.parse(ciIdentity);
  const runBindings = new Map<string, z.infer<typeof SandboxBindingSchema>>();
  const runIds: string[] = [];
  const executionIds: string[] = [];
  const startedAt: string[] = [];
  const finishedAt: string[] = [];

  for (const run of view.investigation.runs) {
    if (run.result.executor !== "NATIVE_DOCKER" || run.sandbox.kind !== "DOCKER_ISOLATED" || run.sandbox.runtime.image === null) {
      throw new Error("GitHub provenance requires a verified native-Docker proof bundle.");
    }
    const binding = SandboxBindingSchema.parse({
      image: run.sandbox.runtime.image,
      policyDigest: run.sandbox.policyDigest,
      environmentPolicyDigest: run.sandbox.environmentPolicyDigest
    });
    runBindings.set(canonicalJson(binding), binding);
    runIds.push(run.runId);
    executionIds.push(run.executionId);
    startedAt.push(run.startedAt);
    finishedAt.push(run.finishedAt);
  }

  if (runIds.length === 0) throw new Error("GitHub provenance requires at least one recorded execution.");
  const bindings = [...runBindings.values()].sort((left, right) => canonicalJson(left).localeCompare(canonicalJson(right)));
  const unsigned = {
    schemaVersion: GITHUB_PROVENANCE_SCHEMA_VERSION,
    receiptKind: "GITHUB_ARTIFACT_ATTESTATION_SUBJECT" as const,
    createdAt: canonicalTimestamp(createdAt),
    signing: {
      mechanism: "GITHUB_ARTIFACT_ATTESTATION" as const,
      state: "AWAITING_EXTERNAL_SIGNATURE" as const
    },
    proof: {
      rootDigest: view.rootDigest,
      manifestDigest: digestJson(view.manifest),
      investigationDigest: view.manifest.investigationDigest,
      frozenDigest: view.frozenWitness.frozenDigest,
      witnessDigest: view.frozenWitness.witnessDigest,
      approvalDigest: view.frozenWitness.approval.approvalDigest,
      range: view.manifest.resolvedRange,
      sandboxBindings: bindings,
      execution: {
        runIds: sortedUnique(runIds),
        executionIds: sortedUnique(executionIds),
        startedAt: [...startedAt].sort((left, right) => left.localeCompare(right))[0] as string,
        finishedAt: [...finishedAt].sort((left, right) => right.localeCompare(left))[0] as string,
        executionCount: runIds.length
      }
    },
    ci,
    limitation: GITHUB_PROVENANCE_LIMITATION
  };
  const payload = UnsignedGithubProvenanceReceiptSchema.parse(unsigned);
  return GithubProvenanceReceiptSchema.parse({ ...payload, receiptDigest: digestJson(payload) });
}

/** Verify a package before deriving a provenance subject from it. */
export function createGithubProvenanceReceipt(
  bundleDirectory: string,
  ciIdentity: unknown,
  createdAt?: string | Date
): GithubProvenanceReceipt {
  return createGithubProvenanceReceiptFromView(loadVerifiedGitProofView(bundleDirectory), ciIdentity, createdAt);
}

function requiredGithubEnvironment(environment: NodeJS.ProcessEnv, name: string): string {
  const value = environment[name];
  if (!value || !value.trim()) throw new Error(`GitHub Actions provenance requires ${name}.`);
  return value.trim();
}

/**
 * Extract the CI identity from GitHub Actions itself. The tree is resolved from
 * the checked-out commit, not accepted as a user-provided CLI label.
 */
export function githubActionsIdentityFromEnvironment(
  workspace = process.cwd(),
  environment: NodeJS.ProcessEnv = process.env
): GithubActionsIdentity {
  if (environment.GITHUB_ACTIONS !== "true") {
    throw new Error("GitHub provenance receipts can only be created inside GitHub Actions; local metadata is not a signed CI receipt.");
  }
  const commit = requiredGithubEnvironment(environment, "GITHUB_SHA");
  GitObjectIdSchema.parse(commit);
  let tree: string;
  try {
    tree = execFileSync("git", ["rev-parse", `${commit}^{tree}`], {
      cwd: resolve(workspace),
      encoding: "utf8",
      windowsHide: true,
      stdio: ["ignore", "pipe", "pipe"]
    }).trim();
  } catch (error) {
    throw new Error(`GitHub provenance could not resolve the checked-out commit tree: ${error instanceof Error ? error.message : String(error)}`);
  }
  return GithubActionsIdentitySchema.parse({
    provider: "GITHUB_ACTIONS",
    repository: requiredGithubEnvironment(environment, "GITHUB_REPOSITORY"),
    workflowRef: requiredGithubEnvironment(environment, "GITHUB_WORKFLOW_REF"),
    ref: requiredGithubEnvironment(environment, "GITHUB_REF"),
    commit,
    tree,
    runId: requiredGithubEnvironment(environment, "GITHUB_RUN_ID"),
    runAttempt: Number(requiredGithubEnvironment(environment, "GITHUB_RUN_ATTEMPT")),
    eventName: requiredGithubEnvironment(environment, "GITHUB_EVENT_NAME")
  });
}

function ensureRealDirectoryTree(directory: string): void {
  const absolute = resolve(directory);
  const parsed = parse(absolute);
  const parts = relative(parsed.root, absolute).split(/[\\/]+/).filter(Boolean);
  let current = parsed.root;
  if (existsSync(current)) {
    const safe = resolveSafeDirectorySegment(current);
    if (safe === null) throw new Error(`Provenance root must be a real directory: ${current}`);
    current = safe;
  }
  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    const safe = resolveSafeDirectorySegment(current);
    if (safe === null) throw new Error(`Provenance output cannot traverse a symbolic link or non-directory: ${current}`);
    current = safe;
  }
}

export function defaultGithubProvenanceRoot(workspace = process.cwd()): string {
  return resolve(workspace, ".faultline", "provenance");
}

/** Write-once managed storage for the unsigned subject that actions/attest will sign. */
export function writeGithubProvenanceReceipt(
  outputFile: string,
  receipt: GithubProvenanceReceipt,
  provenanceRoot = defaultGithubProvenanceRoot()
): string {
  const root = resolve(provenanceRoot);
  const output = resolve(outputFile);
  const nested = relativeTrustedSystemPath(root, output);
  if (!nested || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error(`GitHub provenance output must be a file beneath ${root}`);
  }
  ensureRealDirectoryTree(root);
  ensureRealDirectoryTree(dirname(output));
  if (existsSync(output)) throw new Error(`GitHub provenance receipt already exists and will not be replaced: ${output}`);
  try {
    writeFileSync(output, `${canonicalJson(GithubProvenanceReceiptSchema.parse(receipt))}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
  } catch (error) {
    const code = error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
    if (code === "EEXIST") throw new Error(`GitHub provenance receipt already exists and will not be replaced: ${output}`);
    throw error;
  }
  return output;
}

function readRegularJson(path: string, label: string): unknown {
  const file = resolve(path);
  const stat = lstatSync(file);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`${label} must be a regular non-symlink file`);
  if (stat.size > MAX_RECEIPT_BYTES) throw new Error(`${label} exceeds the ${MAX_RECEIPT_BYTES}-byte limit`);
  try {
    return JSON.parse(readFileSync(file, "utf8"));
  } catch (error) {
    throw new Error(`${label} is not valid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
}

export function readGithubProvenanceReceipt(path: string): GithubProvenanceReceipt {
  return GithubProvenanceReceiptSchema.parse(readRegularJson(path, "GitHub provenance receipt"));
}

export function readGithubArtifactAttestationTrust(path: string): GithubArtifactAttestationTrust {
  return GithubArtifactAttestationTrustSchema.parse(readRegularJson(path, "GitHub attestation trust configuration"));
}

function trustErrors(receipt: GithubProvenanceReceipt, trust: GithubArtifactAttestationTrust, errors: string[]): void {
  if (receipt.ci.repository !== trust.repository) errors.push("receipt repository is not permitted by the trust configuration");
  if (receipt.ci.ref !== trust.sourceRef) errors.push("receipt ref is not permitted by the trust configuration");
  if (receipt.ci.eventName !== trust.eventName) errors.push("receipt event is not permitted by the trust configuration");
  if (receipt.ci.workflowRef !== `${trust.signerWorkflow}@${trust.sourceRef}`) {
    errors.push("receipt workflow reference is not the configured signer workflow at the configured source ref");
  }
  if (trust.sourceDigest !== undefined && receipt.ci.commit !== trust.sourceDigest) {
    errors.push("receipt source commit does not match the configured source digest");
  }
}

/**
 * Re-verifies the full Git package, then reconstructs every provenance field
 * before checking the explicit GitHub trust constraints. This step is fully
 * offline and does not itself represent a signature verification.
 */
export function verifyGithubProvenanceBinding(
  bundleDirectory: string,
  receiptInput: unknown,
  trustInput: unknown
): ProvenanceBindingVerification {
  const errors: string[] = [];
  const receiptParsed = GithubProvenanceReceiptSchema.safeParse(receiptInput);
  const trustParsed = GithubArtifactAttestationTrustSchema.safeParse(trustInput);
  if (!receiptParsed.success) errors.push(`GitHub provenance receipt schema validation failed: ${receiptParsed.error.message}`);
  if (!trustParsed.success) errors.push(`GitHub attestation trust schema validation failed: ${trustParsed.error.message}`);
  if (!receiptParsed.success || !trustParsed.success) {
    return { valid: false, errors, receiptDigest: null, proofRootDigest: null };
  }
  const receipt = receiptParsed.data;
  const trust = trustParsed.data;
  const calculatedDigest = digestJson(receiptPayload(receipt));
  if (receipt.receiptDigest !== calculatedDigest) errors.push("receiptDigest does not match the canonical provenance receipt contents");
  try {
    const view = loadVerifiedGitProofView(bundleDirectory);
    const expected = createGithubProvenanceReceiptFromView(view, receipt.ci, receipt.createdAt);
    if (canonicalJson(receiptPayload(receipt)) !== canonicalJson(receiptPayload(expected))) {
      errors.push("receipt proof facts do not exactly match the verified Git proof bundle");
    }
    trustErrors(receipt, trust, errors);
    return {
      valid: errors.length === 0,
      errors,
      receiptDigest: calculatedDigest,
      proofRootDigest: view.rootDigest,
      receipt
    };
  } catch (error) {
    errors.push(`GitHub provenance binding verification failed safely: ${error instanceof Error ? error.message : String(error)}`);
    return { valid: false, errors, receiptDigest: calculatedDigest, proofRootDigest: null, receipt };
  }
}

function assertRegularFile(path: string, label: string): void {
  const stat = lstatSync(resolve(path));
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`${label} must be a regular non-symlink file`);
}

/**
 * Invoke GitHub CLI's offline verifier. `--bundle` and `--custom-trusted-root`
 * ensure the caller supplies the Sigstore collateral and trust root rather
 * than silently consulting a mutable online trust source.
 */
export function verifyGithubArtifactAttestation(
  receiptFile: string,
  attestationBundleFile: string,
  trustInput: unknown,
  sourceCommit: string,
  executable = "gh"
): GithubArtifactAttestationVerification {
  const command = ["attestation", "verify", resolve(receiptFile)];
  try {
    const trust = GithubArtifactAttestationTrustSchema.parse(trustInput);
    GitObjectIdSchema.parse(sourceCommit);
    assertRegularFile(receiptFile, "GitHub provenance receipt");
    assertRegularFile(attestationBundleFile, "GitHub artifact attestation bundle");
    const trustedRoot = resolve(dirname(resolve(attestationBundleFile)), trust.trustedRootFile);
    assertRegularFile(trustedRoot, "GitHub trusted root");
    command.push(
      "--repo", trust.repository,
      "--bundle", resolve(attestationBundleFile),
      "--custom-trusted-root", trustedRoot,
      "--signer-workflow", trust.signerWorkflow,
      "--source-ref", trust.sourceRef,
      "--source-digest", sourceCommit
    );
    if (trust.denySelfHostedRunners) command.push("--deny-self-hosted-runners");
    const result = spawnSync(executable, command, { encoding: "utf8", windowsHide: true });
    if (result.error) {
      return { valid: false, command: [executable, ...command], errors: [`Could not run ${executable}: ${result.error.message}`] };
    }
    if (result.status !== 0) {
      const detail = `${result.stdout ?? ""}\n${result.stderr ?? ""}`.trim().slice(0, 8_000);
      return { valid: false, command: [executable, ...command], errors: [`GitHub artifact attestation verification failed${detail ? `: ${detail}` : ""}`] };
    }
    return { valid: true, command: [executable, ...command], errors: [] };
  } catch (error) {
    return {
      valid: false,
      command: [executable, ...command],
      errors: [`GitHub artifact attestation verification failed safely: ${error instanceof Error ? error.message : String(error)}`]
    };
  }
}

/**
 * Full offline verification: first check the receipt's exact binding to a
 * verified FaultLine package, then verify the receipt bytes with GitHub's
 * Sigstore bundle and a caller-controlled trust root.
 */
export function verifySignedGithubProvenance(
  input: {
    readonly bundleDirectory: string;
    readonly receiptFile: string;
    readonly attestationBundleFile: string;
    readonly trust: unknown;
  },
  runner: GithubArtifactAttestationRunner = verifyGithubArtifactAttestation
): SignedGithubProvenanceVerification {
  let receipt: GithubProvenanceReceipt;
  try {
    receipt = readGithubProvenanceReceipt(input.receiptFile);
  } catch (error) {
    const errors = [`GitHub provenance receipt could not be read safely: ${error instanceof Error ? error.message : String(error)}`];
    const binding: ProvenanceBindingVerification = { valid: false, errors, receiptDigest: null, proofRootDigest: null };
    return {
      valid: false,
      assurance: "NOT_VERIFIED",
      errors,
      binding,
      artifactAttestation: { valid: false, command: [], errors: ["artifact attestation was not attempted"] },
      limitation: GITHUB_PROVENANCE_LIMITATION
    };
  }
  const binding = verifyGithubProvenanceBinding(input.bundleDirectory, receipt, input.trust);
  if (!binding.valid) {
    return {
      valid: false,
      assurance: "NOT_VERIFIED",
      errors: binding.errors,
      binding,
      artifactAttestation: { valid: false, command: [], errors: ["artifact attestation was not attempted because receipt binding is invalid"] },
      limitation: GITHUB_PROVENANCE_LIMITATION
    };
  }
  const artifactAttestation = runner(input.receiptFile, input.attestationBundleFile, input.trust, receipt.ci.commit);
  const errors = [...binding.errors, ...artifactAttestation.errors];
  return {
    valid: binding.valid && artifactAttestation.valid,
    assurance: binding.valid && artifactAttestation.valid ? "GITHUB_ARTIFACT_ATTESTATION_VERIFIED" : "NOT_VERIFIED",
    errors,
    binding,
    artifactAttestation,
    limitation: GITHUB_PROVENANCE_LIMITATION
  };
}
