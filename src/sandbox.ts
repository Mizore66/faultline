import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { realpathSync, statSync } from "node:fs";
import { isAbsolute, parse, resolve } from "node:path";
import { digestJson, sha256 } from "./canonical.js";
import type { Witness } from "./domain.js";
import { classifyFromWitnessResult, parseWitnessResult } from "./witness-result.js";

/**
 * The sandbox boundary is intentionally conservative. A plan is evidence only
 * when it uses the Docker-isolated form below; an explicitly opted-in local
 * execution is marked INAPPLICABLE so it cannot silently become proof.
 */
export const SANDBOX_POLICY_VERSION = "faultline.sandbox.v2" as const;
const LEGACY_SANDBOX_POLICY_VERSION = "faultline.sandbox.v1" as const;
/**
 * The source is mounted one level below the image workspace. This preserves
 * dependencies deliberately baked into `/workspace/node_modules` by the
 * explicit project-runtime setup flow while keeping the Git source read-only.
 */
export const SANDBOX_SOURCE_TARGET = "/workspace/src" as const;
const LEGACY_SANDBOX_SOURCE_TARGET = "/workspace" as const;
export const ENVIRONMENT_POLICY_VERSION = "faultline.sandbox-environment.v1" as const;

export const DEFAULT_SANDBOX_LIMITS = Object.freeze({
  timeoutMs: 120_000,
  maxOutputBytes: 1_048_576,
  cpuCount: 1,
  memoryBytes: 536_870_912,
  pidsLimit: 128,
  tmpfsBytes: 67_108_864
});

export const MAX_SANDBOX_LIMITS = Object.freeze({
  timeoutMs: 300_000,
  maxOutputBytes: 8_388_608,
  cpuCount: 4,
  memoryBytes: 2_147_483_648,
  pidsLimit: 512,
  tmpfsBytes: 536_870_912
});

const DETERMINISTIC_ENVIRONMENT = Object.freeze({
  CI: "1",
  HOME: "/tmp",
  LANG: "C.UTF-8",
  LC_ALL: "C.UTF-8",
  SOURCE_DATE_EPOCH: "0",
  TZ: "UTC"
});

const SECRET_ENVIRONMENT_NAME = /(api[_-]?key|auth|bearer|cookie|credential|passwd|password|private[_-]?key|secret|session|token)/i;
const ENVIRONMENT_NAME = /^[A-Z_][A-Z0-9_]*$/;
const IMAGE_REFERENCE = /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/;
const WITNESS_DIGEST = /^sha256:[a-f0-9]{64}$/;

export type SandboxKind = "DOCKER_ISOLATED" | "UNSAFE_LOCAL";
/** Identifies whether FaultLine itself launched Docker or a test double supplied the result. */
export type SandboxExecutor = "NATIVE_DOCKER" | "INJECTED_RUNNER" | "UNSAFE_LOCAL";
export type SandboxVerdict = "PASS" | "FAIL" | "ERROR" | "INAPPLICABLE";
export type SandboxReason =
  // Legacy reasons retained only so old stored records still parse. New runs
  // never emit EXIT_ZERO/EXIT_NONZERO as PASS/FAIL.
  | "EXIT_ZERO"
  | "EXIT_NONZERO"
  | "PREDICATE_PASS"
  | "PREDICATE_FAIL"
  | "INCOMPATIBLE_STATE"
  | "HARNESS_ERROR"
  | "EXIT_NONZERO_UNSTRUCTURED"
  | "EXIT_ZERO_UNSTRUCTURED"
  | "TIMEOUT"
  | "OUTPUT_LIMIT_EXCEEDED"
  | "SANDBOX_UNAVAILABLE"
  | "WITNESS_SETUP_ERROR"
  | "RUNNER_FAILURE"
  | "UNSAFE_LOCAL_NOT_PROOF";

const DOCKER_INFRASTRUCTURE_ERROR = /(?:cannot connect to the docker daemon|is the docker daemon running|error during connect|docker daemon is not running|error response from daemon|unable to find image|pull access denied|no such image)/i;

/**
 * Signatures of a compile/setup incompatibility rather than a genuine
 * predicate failure. "cannot find module" and "permission denied" are
 * deliberately absent here: they are already classified as
 * WITNESS_SETUP_ERROR above this check runs.
 */
const COMPILE_OR_SETUP_INCOMPATIBILITY = /(?:SyntaxError|TS\d{4}\s*:|error\s+TS\d{4}|failed to compile|error\[E\d{4}\]|could not compile `|cargo:.*error|rustc.*error:|ModuleNotFoundError|ImportError:\s*No module named)/i;

export interface SandboxLimits {
  readonly timeoutMs: number;
  readonly maxOutputBytes: number;
  readonly cpuCount: number;
  readonly memoryBytes: number;
  readonly pidsLimit: number;
  readonly tmpfsBytes: number;
}

export interface FrozenSandboxWitness {
  readonly digest: Pick<Witness, "digest">["digest"];
  readonly command: Pick<Witness, "command">["command"];
}

export interface SandboxPlanRequest {
  readonly witness: FrozenSandboxWitness;
  /** Must identify a real, non-root source directory to bind read-only. */
  readonly sourceDirectory: string;
  /** Required for Docker plans and must be pinned by immutable digest. */
  readonly image?: string;
  /** Values are never inherited implicitly from the host. */
  readonly environment?: Readonly<Record<string, string | undefined>>;
  /** Additional non-secret names that may be passed into the container. */
  readonly allowedEnvironment?: readonly string[];
  readonly limits?: Partial<SandboxLimits>;
  readonly mode?: SandboxKind;
  /** Required alongside mode: UNSAFE_LOCAL; no automatic fallback exists. */
  readonly allowUnsafeLocal?: boolean;
}

export interface EnvironmentEntry {
  readonly key: string;
  /** A digest permits reproducibility checks without serializing a value. */
  readonly valueDigest: string;
}

export interface SandboxEnvironmentPolicy {
  readonly schemaVersion: typeof ENVIRONMENT_POLICY_VERSION;
  readonly fixed: Readonly<Record<string, string>>;
  readonly allowedKeys: readonly string[];
  readonly passed: readonly EnvironmentEntry[];
}

export interface SandboxPlanAudit {
  readonly kind: SandboxKind;
  readonly witnessDigest: string;
  readonly commandDigest: string;
  readonly environmentPolicyDigest: string;
  readonly policyDigest: string;
  readonly environment: {
    readonly fixedKeys: readonly string[];
    readonly allowedKeys: readonly string[];
    readonly passed: readonly EnvironmentEntry[];
    /** Names only: values were rejected and are deliberately never logged. */
    readonly redactedKeys: readonly string[];
  };
  /** Reconstructable non-secret policy facts, not an assertion that Docker ran. */
  readonly runtime: {
    readonly image: string | null;
    readonly entrypoint: string | null;
    readonly network: "none" | null;
    readonly rootFilesystemReadOnly: boolean;
    readonly user: string | null;
    readonly capDropAll: boolean;
    readonly noNewPrivileges: boolean;
    readonly pull: "never" | null;
    readonly limits: SandboxLimits;
  };
}

interface CommonSandboxPlan {
  readonly kind: SandboxKind;
  readonly executable: string;
  readonly arguments: readonly string[];
  readonly sourceDirectory: string;
  readonly limits: SandboxLimits;
  /** This is only supplied to the runner; use auditSandboxPlan before persisting a plan. */
  readonly environment: Readonly<Record<string, string>>;
  readonly environmentPolicy: SandboxEnvironmentPolicy;
  readonly environmentPolicyDigest: string;
  readonly policyDigest: string;
  readonly audit: SandboxPlanAudit;
}

export interface DockerSandboxPlan extends CommonSandboxPlan {
  readonly kind: "DOCKER_ISOLATED";
  readonly executable: "docker";
  readonly image: string;
  /** Ephemeral Docker name used to force-remove an orphaned container. */
  readonly containerName: string;
}

export interface UnsafeLocalSandboxPlan extends CommonSandboxPlan {
  readonly kind: "UNSAFE_LOCAL";
  readonly acknowledgement: "UNSAFE_LOCAL_OPTED_IN";
}

export type SandboxPlan = DockerSandboxPlan | UnsafeLocalSandboxPlan;

export interface SandboxCommandInvocation {
  readonly kind: SandboxKind;
  readonly executable: string;
  readonly arguments: readonly string[];
  readonly cwd: string;
  readonly timeoutMs: number;
  readonly maxOutputBytes: number;
  readonly environment: Readonly<Record<string, string>>;
  readonly containerName?: string;
}

export interface SandboxCommandResult {
  readonly exitCode: number | null;
  readonly signal?: string | null;
  readonly stdout: string;
  readonly stderr: string;
  readonly timedOut?: boolean;
  readonly outputLimitExceeded?: boolean;
}

export interface SandboxCommandRunner {
  run(invocation: SandboxCommandInvocation): Promise<SandboxCommandResult>;
}

export interface SandboxExecutionResult {
  readonly kind: SandboxKind;
  readonly executor: SandboxExecutor;
  readonly verdict: SandboxVerdict;
  readonly reason: SandboxReason;
  readonly exitCode: number | null;
  readonly signal: string | null;
  readonly stdout: string;
  readonly stderr: string;
  readonly outputTruncated: boolean;
  readonly policyDigest: string;
  readonly environmentPolicyDigest: string;
}

function uniqueSorted(values: readonly string[]): string[] {
  return [...new Set(values)].sort((left, right) => left.localeCompare(right));
}

function assertEnvironmentName(name: string): void {
  if (!ENVIRONMENT_NAME.test(name)) {
    throw new Error(`Sandbox environment name is invalid: ${name}`);
  }
}

function isSecretEnvironmentName(name: string): boolean {
  return SECRET_ENVIRONMENT_NAME.test(name);
}

function normalizeAllowlist(allowedEnvironment: readonly string[] | undefined): string[] {
  const normalized = uniqueSorted(allowedEnvironment ?? []);
  for (const name of normalized) {
    assertEnvironmentName(name);
    if (name in DETERMINISTIC_ENVIRONMENT) {
      throw new Error(`Sandbox environment ${name} is fixed by the deterministic policy and must not be allowlisted.`);
    }
    if (isSecretEnvironmentName(name)) {
      throw new Error(`Sandbox refuses secret-like environment name even when allowlisted: ${name}`);
    }
  }
  return normalized;
}

function resolveSourceDirectory(sourceDirectory: string): string {
  if (!sourceDirectory || !isAbsolute(sourceDirectory)) {
    throw new Error("Sandbox sourceDirectory must be an absolute path.");
  }
  const resolved = realpathSync(resolve(sourceDirectory));
  if (!statSync(resolved).isDirectory()) {
    throw new Error(`Sandbox sourceDirectory is not a directory: ${sourceDirectory}`);
  }
  if (resolved === parse(resolved).root) {
    throw new Error("Sandbox refuses to mount a filesystem root, even read-only.");
  }
  // Docker's --mount parser uses commas as separators. Refuse ambiguous paths.
  if (/[\r\n,]/.test(resolved)) {
    throw new Error("Sandbox sourceDirectory contains a character unsafe for Docker --mount.");
  }
  return resolved;
}

function validateImage(image: string | undefined): string {
  if (!image || !IMAGE_REFERENCE.test(image)) {
    throw new Error("Sandbox image must be an immutable digest-pinned reference (for example registry.example/faultline@sha256:<64-hex-digest>).");
  }
  return image;
}

function strictLimit(name: keyof SandboxLimits, value: number, maximum: number): number {
  if (!Number.isInteger(value) || value <= 0 || value > maximum) {
    throw new Error(`Sandbox limit ${name} must be a positive integer no greater than ${maximum}.`);
  }
  return value;
}

function resolveLimits(overrides: Partial<SandboxLimits> | undefined): SandboxLimits {
  const candidate = { ...DEFAULT_SANDBOX_LIMITS, ...overrides };
  return Object.freeze({
    timeoutMs: strictLimit("timeoutMs", candidate.timeoutMs, MAX_SANDBOX_LIMITS.timeoutMs),
    maxOutputBytes: strictLimit("maxOutputBytes", candidate.maxOutputBytes, MAX_SANDBOX_LIMITS.maxOutputBytes),
    cpuCount: strictLimit("cpuCount", candidate.cpuCount, MAX_SANDBOX_LIMITS.cpuCount),
    memoryBytes: strictLimit("memoryBytes", candidate.memoryBytes, MAX_SANDBOX_LIMITS.memoryBytes),
    pidsLimit: strictLimit("pidsLimit", candidate.pidsLimit, MAX_SANDBOX_LIMITS.pidsLimit),
    tmpfsBytes: strictLimit("tmpfsBytes", candidate.tmpfsBytes, MAX_SANDBOX_LIMITS.tmpfsBytes)
  });
}

function validateWitness(witness: FrozenSandboxWitness): FrozenSandboxWitness {
  if (!WITNESS_DIGEST.test(witness.digest)) {
    throw new Error("Sandbox requires a frozen witness with a sha256 digest.");
  }
  if (!witness.command.trim() || witness.command.includes("\0")) {
    throw new Error("Sandbox witness command must be non-empty and cannot contain a NUL byte.");
  }
  return witness;
}

interface BuiltEnvironment {
  readonly values: Readonly<Record<string, string>>;
  readonly policy: SandboxEnvironmentPolicy;
  readonly redactedKeys: readonly string[];
}

function buildEnvironment(
  supplied: Readonly<Record<string, string | undefined>> | undefined,
  allowedKeys: readonly string[]
): BuiltEnvironment {
  const values: Record<string, string> = { ...DETERMINISTIC_ENVIRONMENT };
  const passed: EnvironmentEntry[] = [];
  const redactedKeys: string[] = [];
  const allowed = new Set(allowedKeys);

  for (const [key, value] of Object.entries(supplied ?? {}).sort(([left], [right]) => left.localeCompare(right))) {
    assertEnvironmentName(key);
    if (value === undefined) continue;
    if (isSecretEnvironmentName(key)) {
      redactedKeys.push(key);
      continue;
    }
    if (key in DETERMINISTIC_ENVIRONMENT) {
      if (value !== DETERMINISTIC_ENVIRONMENT[key as keyof typeof DETERMINISTIC_ENVIRONMENT]) {
        throw new Error(`Sandbox refuses to override deterministic environment ${key}.`);
      }
      continue;
    }
    if (!allowed.has(key)) {
      redactedKeys.push(key);
      continue;
    }
    values[key] = value;
    passed.push({ key, valueDigest: `sha256:${sha256(value)}` });
  }

  const policy: SandboxEnvironmentPolicy = Object.freeze({
    schemaVersion: ENVIRONMENT_POLICY_VERSION,
    fixed: Object.freeze({ ...DETERMINISTIC_ENVIRONMENT }),
    allowedKeys: Object.freeze([...allowedKeys]),
    passed: Object.freeze([...passed])
  });
  return {
    values: Object.freeze(values),
    policy,
    redactedKeys: Object.freeze(uniqueSorted(redactedKeys))
  };
}

function environmentArguments(environment: Readonly<Record<string, string>>): string[] {
  return Object.entries(environment)
    .sort(([left], [right]) => left.localeCompare(right))
    .flatMap(([key, value]) => ["--env", `${key}=${value}`]);
}

function createAudit(
  kind: SandboxKind,
  witness: FrozenSandboxWitness,
  environment: BuiltEnvironment,
  environmentPolicyDigest: string,
  policyDigest: string,
  runtime: SandboxPlanAudit["runtime"]
): SandboxPlanAudit {
  return Object.freeze({
    kind,
    witnessDigest: witness.digest,
    commandDigest: `sha256:${sha256(witness.command)}`,
    environmentPolicyDigest,
    policyDigest,
    environment: Object.freeze({
      fixedKeys: Object.freeze(Object.keys(DETERMINISTIC_ENVIRONMENT).sort((left, right) => left.localeCompare(right))),
      allowedKeys: Object.freeze([...environment.policy.allowedKeys]),
      passed: Object.freeze([...environment.policy.passed]),
      redactedKeys: Object.freeze([...environment.redactedKeys])
    }),
    runtime: Object.freeze({ ...runtime, limits: Object.freeze({ ...runtime.limits }) })
  });
}

function dockerPolicyPayload(
  image: string,
  witness: FrozenSandboxWitness,
  limits: SandboxLimits,
  environmentPolicyDigest: string,
  schemaVersion: typeof SANDBOX_POLICY_VERSION | typeof LEGACY_SANDBOX_POLICY_VERSION,
  sourceTarget: typeof SANDBOX_SOURCE_TARGET | typeof LEGACY_SANDBOX_SOURCE_TARGET
): Record<string, unknown> {
  return {
    schemaVersion,
    kind: "DOCKER_ISOLATED",
    image,
    witnessDigest: witness.digest,
    source: { target: sourceTarget, readOnly: true },
    network: "none",
    rootFilesystem: "read-only",
    user: "65534:65534",
    capDrop: "ALL",
    noNewPrivileges: true,
    pull: "never",
    entrypoint: "/bin/sh",
    limits,
    environmentPolicyDigest
  };
}

function dockerPolicyDigest(
  image: string,
  witness: FrozenSandboxWitness,
  limits: SandboxLimits,
  environmentPolicyDigest: string
): string {
  return digestJson(dockerPolicyPayload(image, witness, limits, environmentPolicyDigest, SANDBOX_POLICY_VERSION, SANDBOX_SOURCE_TARGET));
}

function unsafeLocalPolicyPayload(
  witness: FrozenSandboxWitness,
  limits: SandboxLimits,
  environmentPolicyDigest: string,
  schemaVersion: typeof SANDBOX_POLICY_VERSION | typeof LEGACY_SANDBOX_POLICY_VERSION
): Record<string, unknown> {
  return {
    schemaVersion,
    kind: "UNSAFE_LOCAL",
    witnessDigest: witness.digest,
    warning: "No container isolation. Never use as proof.",
    limits,
    environmentPolicyDigest
  };
}

function unsafeLocalPolicyDigest(
  witness: FrozenSandboxWitness,
  limits: SandboxLimits,
  environmentPolicyDigest: string
): string {
  return digestJson(unsafeLocalPolicyPayload(witness, limits, environmentPolicyDigest, SANDBOX_POLICY_VERSION));
}

/**
 * Construct a Docker command as an argv vector; no host shell evaluates it.
 * The caller can persist `plan.audit`, but should never serialize raw argv
 * because it contains allowlisted values needed for Docker execution.
 */
export function createDockerSandboxPlan(request: SandboxPlanRequest): DockerSandboxPlan {
  const witness = validateWitness(request.witness);
  const sourceDirectory = resolveSourceDirectory(request.sourceDirectory);
  const image = validateImage(request.image);
  const limits = resolveLimits(request.limits);
  const allowedKeys = normalizeAllowlist(request.allowedEnvironment);
  const environment = buildEnvironment(request.environment, allowedKeys);
  const environmentPolicyDigest = digestJson(environment.policy);
  const policyDigest = dockerPolicyDigest(image, witness, limits, environmentPolicyDigest);
  const containerName = `faultline-${randomUUID().replaceAll("-", "")}`;
  const fsizeLimit = limits.maxOutputBytes;
  const commandArguments = Object.freeze([
    "run",
    "--rm",
    "--name",
    containerName,
    "--init",
    "--pull=never",
    "--network",
    "none",
    "--ipc",
    "none",
    "--read-only",
    "--user",
    "65534:65534",
    "--cap-drop",
    "ALL",
    "--security-opt",
    "no-new-privileges:true",
    "--ulimit",
    `fsize=${fsizeLimit}:${fsizeLimit}`,
    "--pids-limit",
    String(limits.pidsLimit),
    "--memory",
    String(limits.memoryBytes),
    "--memory-swap",
    String(limits.memoryBytes),
    "--cpus",
    String(limits.cpuCount),
    "--tmpfs",
    `/tmp:rw,noexec,nosuid,nodev,size=${limits.tmpfsBytes}`,
    "--mount",
    `type=bind,src=${sourceDirectory},dst=${SANDBOX_SOURCE_TARGET},readonly`,
    "--workdir",
    SANDBOX_SOURCE_TARGET,
    ...environmentArguments(environment.values),
    "--entrypoint",
    "/bin/sh",
    image,
    "-lc",
    witness.command
  ]);
  return Object.freeze({
    kind: "DOCKER_ISOLATED" as const,
    executable: "docker" as const,
    arguments: commandArguments,
    sourceDirectory,
    image,
    containerName,
    limits,
    environment: environment.values,
    environmentPolicy: environment.policy,
    environmentPolicyDigest,
    policyDigest,
    audit: createAudit("DOCKER_ISOLATED", witness, environment, environmentPolicyDigest, policyDigest, {
      image,
      entrypoint: "/bin/sh",
      network: "none",
      rootFilesystemReadOnly: true,
      user: "65534:65534",
      capDropAll: true,
      noNewPrivileges: true,
      pull: "never",
      limits
    })
  });
}

/** Development-only gate for constructing UNSAFE_LOCAL plans (never shipped selection). */
export const DEV_UNSAFE_LOCAL_ENV = "FAULTLINE_DEV_UNSAFE_LOCAL" as const;

export function isDevUnsafeLocalEnabled(env: NodeJS.ProcessEnv = process.env): boolean {
  return env[DEV_UNSAFE_LOCAL_ENV] === "1";
}

/**
 * Local execution is deliberately opt-in, development-gated, and produces no
 * PASS/FAIL evidence. The shipped `createSandboxPlan` API never constructs it.
 */
function createUnsafeLocalSandboxPlan(request: SandboxPlanRequest): UnsafeLocalSandboxPlan {
  if (!isDevUnsafeLocalEnabled()) {
    throw new Error(
      `UNSAFE_LOCAL construction requires ${DEV_UNSAFE_LOCAL_ENV}=1 (development-only). Docker is the only shipped runtime.`
    );
  }
  if (request.allowUnsafeLocal !== true) {
    throw new Error("UNSAFE_LOCAL execution requires allowUnsafeLocal: true; Docker is the only default.");
  }
  const witness = validateWitness(request.witness);
  const sourceDirectory = resolveSourceDirectory(request.sourceDirectory);
  const limits = resolveLimits(request.limits);
  const allowedKeys = normalizeAllowlist(request.allowedEnvironment);
  const environment = buildEnvironment(request.environment, allowedKeys);
  const environmentPolicyDigest = digestJson(environment.policy);
  const policyDigest = unsafeLocalPolicyDigest(witness, limits, environmentPolicyDigest);
  const isWindows = process.platform === "win32";
  const executable = isWindows ? "cmd.exe" : "/bin/sh";
  const commandArguments = Object.freeze(isWindows ? ["/d", "/s", "/c", witness.command] : ["-lc", witness.command]);
  return Object.freeze({
    kind: "UNSAFE_LOCAL" as const,
    acknowledgement: "UNSAFE_LOCAL_OPTED_IN" as const,
    executable,
    arguments: commandArguments,
    sourceDirectory,
    limits,
    environment: environment.values,
    environmentPolicy: environment.policy,
    environmentPolicyDigest,
    policyDigest,
    audit: createAudit("UNSAFE_LOCAL", witness, environment, environmentPolicyDigest, policyDigest, {
      image: null,
      entrypoint: null,
      network: null,
      rootFilesystemReadOnly: false,
      user: null,
      capDropAll: false,
      noNewPrivileges: false,
      pull: null,
      limits
    })
  });
}

export function createSandboxPlan(request: SandboxPlanRequest): SandboxPlan {
  if (request.mode === "UNSAFE_LOCAL") {
    throw new Error("UNSAFE_LOCAL execution is not available in the shipped FaultLine runtime.");
  }
  if (request.allowUnsafeLocal === true) {
    throw new Error("allowUnsafeLocal is only valid together with mode: UNSAFE_LOCAL.");
  }
  return createDockerSandboxPlan(request);
}

/**
 * Development-only escape hatch. Requires FAULTLINE_DEV_UNSAFE_LOCAL=1 and
 * allowUnsafeLocal: true. Never used by production CLI selection paths.
 */
export function createDevUnsafeLocalSandboxPlan(request: SandboxPlanRequest): UnsafeLocalSandboxPlan {
  return createUnsafeLocalSandboxPlan({
    ...request,
    allowUnsafeLocal: true
  });
}

/** A serializable, value-redacted representation safe for proof bundles. */
export function auditSandboxPlan(plan: SandboxPlan): SandboxPlanAudit {
  return plan.audit;
}

/**
 * Revalidate the serializable audit form before trusting it in an offline
 * proof package. This validates recorder-declared policy consistency; it does
 * not itself attest that a host or Docker daemon enforced that policy.
 */
export function validateSandboxPlanAudit(audit: SandboxPlanAudit): string[] {
  const errors: string[] = [];
  const digest = /^sha256:[a-f0-9]{64}$/;
  if (!digest.test(audit.witnessDigest)) errors.push("witness digest is invalid");
  if (!digest.test(audit.commandDigest)) errors.push("command digest is invalid");
  if (!digest.test(audit.environmentPolicyDigest)) errors.push("environment policy digest is invalid");
  if (!digest.test(audit.policyDigest)) errors.push("sandbox policy digest is invalid");
  const expectedFixed = Object.keys(DETERMINISTIC_ENVIRONMENT).sort((left, right) => left.localeCompare(right));
  if (JSON.stringify([...audit.environment.fixedKeys].sort((left, right) => left.localeCompare(right))) !== JSON.stringify(expectedFixed)) {
    errors.push("fixed environment keys do not match the deterministic policy");
  }
  const allowedNames = new Set(audit.environment.allowedKeys);
  const passedNames = new Set(audit.environment.passed.map((entry) => entry.key));
  const redactedNames = new Set(audit.environment.redactedKeys);
  if (allowedNames.size !== audit.environment.allowedKeys.length || passedNames.size !== audit.environment.passed.length
    || redactedNames.size !== audit.environment.redactedKeys.length) errors.push("sandbox environment audit contains duplicate names");
  for (const entry of audit.environment.passed) {
    if (!ENVIRONMENT_NAME.test(entry.key) || !digest.test(entry.valueDigest)) {
      errors.push("sandbox environment audit contains an invalid passed value");
      break;
    }
    if (!allowedNames.has(entry.key)) {
      errors.push("sandbox environment audit passes a value outside its allowlist");
      break;
    }
  }
  for (const key of [...audit.environment.allowedKeys, ...audit.environment.redactedKeys]) {
    if (!ENVIRONMENT_NAME.test(key)) {
      errors.push("sandbox environment audit contains an invalid environment name");
      break;
    }
  }
  const reconstructedEnvironmentPolicy = {
    schemaVersion: ENVIRONMENT_POLICY_VERSION,
    fixed: DETERMINISTIC_ENVIRONMENT,
    allowedKeys: [...audit.environment.allowedKeys],
    passed: [...audit.environment.passed]
  };
  if (audit.environmentPolicyDigest !== digestJson(reconstructedEnvironmentPolicy)) {
    errors.push("environment policy digest does not match the serializable audit facts");
  }
  const limits = audit.runtime.limits;
  for (const [name, maximum] of Object.entries(MAX_SANDBOX_LIMITS) as Array<[keyof SandboxLimits, number]>) {
    const value = limits[name];
    if (!Number.isInteger(value) || value <= 0 || value > maximum) errors.push(`sandbox limit ${name} is outside the allowed policy range`);
  }
  if (audit.kind === "DOCKER_ISOLATED") {
    const runtime = audit.runtime;
    if (!runtime.image || !IMAGE_REFERENCE.test(runtime.image)) errors.push("Docker image is not digest-pinned");
    if (runtime.entrypoint !== "/bin/sh" || runtime.network !== "none" || !runtime.rootFilesystemReadOnly
      || runtime.user !== "65534:65534" || !runtime.capDropAll || !runtime.noNewPrivileges || runtime.pull !== "never") {
      errors.push("Docker runtime policy is not locked down");
    }
    const reconstructedPolicy = dockerPolicyPayload(
      runtime.image ?? "",
      { digest: audit.witnessDigest, command: "" },
      limits,
      audit.environmentPolicyDigest,
      SANDBOX_POLICY_VERSION,
      SANDBOX_SOURCE_TARGET
    );
    const legacyPolicy = dockerPolicyPayload(
      runtime.image ?? "",
      { digest: audit.witnessDigest, command: "" },
      limits,
      audit.environmentPolicyDigest,
      LEGACY_SANDBOX_POLICY_VERSION,
      LEGACY_SANDBOX_SOURCE_TARGET
    );
    if (audit.policyDigest !== digestJson(reconstructedPolicy) && audit.policyDigest !== digestJson(legacyPolicy)) {
      errors.push("Docker policy digest does not match the serializable audit facts");
    }
  } else if (audit.runtime.image !== null || audit.runtime.entrypoint !== null || audit.runtime.network !== null
    || audit.runtime.rootFilesystemReadOnly || audit.runtime.user !== null || audit.runtime.capDropAll
    || audit.runtime.noNewPrivileges || audit.runtime.pull !== null) {
    errors.push("unsafe-local runtime audit contradicts its declared mode");
  } else {
    const reconstructedPolicy = unsafeLocalPolicyPayload(
      { digest: audit.witnessDigest, command: "" },
      limits,
      audit.environmentPolicyDigest,
      SANDBOX_POLICY_VERSION
    );
    const legacyPolicy = unsafeLocalPolicyPayload(
      { digest: audit.witnessDigest, command: "" },
      limits,
      audit.environmentPolicyDigest,
      LEGACY_SANDBOX_POLICY_VERSION
    );
    if (audit.policyDigest !== digestJson(reconstructedPolicy) && audit.policyDigest !== digestJson(legacyPolicy)) {
      errors.push("unsafe-local policy digest does not match the serializable audit facts");
    }
  }
  return errors;
}

function outputSize(stdout: string, stderr: string): number {
  return Buffer.byteLength(stdout, "utf8") + Buffer.byteLength(stderr, "utf8");
}

function truncateOutput(stdout: string, stderr: string, maxOutputBytes: number): { stdout: string; stderr: string } {
  let remaining = maxOutputBytes;
  const take = (value: string): string => {
    if (remaining <= 0) return "";
    const bytes = Buffer.from(value, "utf8");
    const part = bytes.subarray(0, remaining);
    remaining -= part.length;
    return part.toString("utf8");
  };
  return { stdout: take(stdout), stderr: take(stderr) };
}

/**
 * Interpret runner output fail-closed. A timeout, missing exit code, Docker
 * infrastructure failure, or output overflow is an ERROR, never a PASS.
 */
export function classifySandboxResult(
  plan: SandboxPlan,
  result: SandboxCommandResult,
  executor: SandboxExecutor = plan.kind === "DOCKER_ISOLATED" ? "NATIVE_DOCKER" : "UNSAFE_LOCAL"
): SandboxExecutionResult {
  const exceedsOutputLimit = result.outputLimitExceeded === true || outputSize(result.stdout, result.stderr) > plan.limits.maxOutputBytes;
  const clipped = exceedsOutputLimit ? truncateOutput(result.stdout, result.stderr, plan.limits.maxOutputBytes) : result;
  const signal = result.signal ?? null;
  const base = {
    kind: plan.kind,
    executor,
    exitCode: result.exitCode,
    signal,
    stdout: clipped.stdout,
    stderr: clipped.stderr,
    outputTruncated: exceedsOutputLimit,
    policyDigest: plan.policyDigest,
    environmentPolicyDigest: plan.environmentPolicyDigest
  };

  if (plan.kind === "UNSAFE_LOCAL") {
    return { ...base, verdict: "INAPPLICABLE", reason: "UNSAFE_LOCAL_NOT_PROOF" };
  }
  if (result.timedOut === true) {
    return { ...base, verdict: "ERROR", reason: "TIMEOUT" };
  }
  if (exceedsOutputLimit) {
    return { ...base, verdict: "ERROR", reason: "OUTPUT_LIMIT_EXCEEDED" };
  }
  if (result.exitCode === null) {
    return { ...base, verdict: "ERROR", reason: "RUNNER_FAILURE" };
  }
  // Docker reserves 125 for CLI/daemon failures before the container command
  // runs. Its daemon/image failures can also arrive as exit 1; those stderr
  // signatures are infrastructure errors, never predicate failures.
  if (result.exitCode === 125 || (plan.kind === "DOCKER_ISOLATED" && DOCKER_INFRASTRUCTURE_ERROR.test(result.stderr))) {
    return { ...base, verdict: "ERROR", reason: "SANDBOX_UNAVAILABLE" };
  }
  // Command-not-found and permission-denied are infrastructure/setup errors,
  // not evidence that the approved predicate failed. Only apply the stderr
  // heuristic when the container exited nonzero and emitted no structured
  // witness result — otherwise a deliberate read-only probe that prints
  // "Permission denied" (Debian) would erase a valid PREDICATE_PASS.
  if (result.exitCode === 126 || result.exitCode === 127) {
    return { ...base, verdict: "ERROR", reason: "WITNESS_SETUP_ERROR" };
  }
  const witnessResultEarly = parseWitnessResult(result.stdout);
  if (
    result.exitCode !== 0
    && witnessResultEarly === null
    && /(?:command not found|not found|no such file|cannot find module|permission denied)/i.test(result.stderr)
  ) {
    return { ...base, verdict: "ERROR", reason: "WITNESS_SETUP_ERROR" };
  }
  // A witness that opts into the structured witness-result protocol is
  // classified from that outcome alone; its exit code is not consulted.
  // This is what keeps a compile/setup incompatibility that happens to exit
  // nonzero from ever being reported as a behavioral predicate failure.
  const witnessResult = witnessResultEarly;
  if (witnessResult) {
    const classified = classifyFromWitnessResult(witnessResult.outcome);
    return { ...base, verdict: classified.verdict, reason: classified.reason as SandboxReason };
  }
  // No structured result was found. Before trusting a nonzero exit as a real
  // predicate failure, check for well-known compile/syntax incompatibility
  // signatures: these mean the witness never actually ran to a verdict.
  if (COMPILE_OR_SETUP_INCOMPATIBILITY.test(result.stderr) || COMPILE_OR_SETUP_INCOMPATIBILITY.test(result.stdout)) {
    return { ...base, verdict: "INAPPLICABLE", reason: "INCOMPATIBLE_STATE" };
  }
  if (result.exitCode === 0) {
    // Unstructured exit 0 is not proof of predicate satisfaction.
    return { ...base, verdict: "ERROR", reason: "EXIT_ZERO_UNSTRUCTURED" };
  }
  // An unstructured nonzero exit is never treated as a behavioral FAIL: it
  // could just as easily be an unclassified setup problem in a witness that
  // never opted into structured results.
  return { ...base, verdict: "ERROR", reason: "EXIT_NONZERO_UNSTRUCTURED" };
}

/**
 * Execute a plan through an injectable runner. Tests can exercise all safety
 * paths with a fake runner; production may use createNodeSandboxRunner().
 */
export async function executeSandboxPlan(
  plan: SandboxPlan,
  runner?: SandboxCommandRunner
): Promise<SandboxExecutionResult> {
  const executor: SandboxExecutor = plan.kind === "UNSAFE_LOCAL"
    ? "UNSAFE_LOCAL"
    : runner === undefined ? "NATIVE_DOCKER" : "INJECTED_RUNNER";
  // Do not put this construction in a default parameter. A default parameter
  // makes `runner` defined before provenance is classified, which would turn
  // every genuine production Docker invocation into an injected one.
  const effectiveRunner = runner ?? createNodeSandboxRunner();
  try {
    const result = await effectiveRunner.run({
      kind: plan.kind,
      executable: plan.executable,
      arguments: plan.arguments,
      cwd: plan.sourceDirectory,
      timeoutMs: plan.limits.timeoutMs,
      maxOutputBytes: plan.limits.maxOutputBytes,
      environment: plan.environment,
      ...(plan.kind === "DOCKER_ISOLATED" ? { containerName: plan.containerName } : {})
    });
    return classifySandboxResult(plan, result, executor);
  } catch (error) {
    return {
      kind: plan.kind,
      executor,
      verdict: plan.kind === "UNSAFE_LOCAL" ? "INAPPLICABLE" : "ERROR",
      reason: plan.kind === "UNSAFE_LOCAL" ? "UNSAFE_LOCAL_NOT_PROOF" : "RUNNER_FAILURE",
      exitCode: null,
      signal: null,
      stdout: "",
      stderr: error instanceof Error ? error.message : "Sandbox runner threw a non-Error value.",
      outputTruncated: false,
      policyDigest: plan.policyDigest,
      environmentPolicyDigest: plan.environmentPolicyDigest
    };
  }
}

/**
 * A small direct-process runner for production wiring. It executes Docker with
 * argv (never a host shell), bounds captured output, and kills the Docker CLI
 * on timeout. Docker itself remains optional until this runner is invoked.
 */
export function createNodeSandboxRunner(): SandboxCommandRunner {
  return {
    run(invocation): Promise<SandboxCommandResult> {
      return new Promise((resolveResult) => {
        // The Docker client needs the host environment to locate its daemon,
        // but only explicit --env flags reach the container. Local execution
        // gets the scrubbed environment with no inherited credentials.
        const childEnvironment = invocation.kind === "DOCKER_ISOLATED" ? process.env : invocation.environment;
        let stdout = Buffer.alloc(0);
        let stderr = Buffer.alloc(0);
        let observedOutputBytes = 0;
        let timedOut = false;
        let outputLimitExceeded = false;
        let settled = false;
        let timeout: NodeJS.Timeout | undefined;

        const forceRemoveContainer = (): Promise<string | null> => new Promise((resolveCleanup) => {
          if (invocation.kind !== "DOCKER_ISOLATED" || !invocation.containerName) {
            resolveCleanup(null);
            return;
          }
          let cleanup;
          let timeout: NodeJS.Timeout | undefined;
          const complete = (detail: string | null): void => {
            if (timeout) clearTimeout(timeout);
            resolveCleanup(detail);
          };
          try {
            cleanup = spawn("docker", ["rm", "--force", invocation.containerName], {
              env: process.env,
              shell: false,
              stdio: ["ignore", "ignore", "pipe"],
              windowsHide: true
            });
          } catch (error) {
            complete(`FaultLine could not force-remove its named container: ${error instanceof Error ? error.message : String(error)}`);
            return;
          }
          let cleanupStderr = "";
          cleanup.stderr?.on("data", (chunk: Buffer) => { cleanupStderr += Buffer.from(chunk).toString("utf8"); });
          cleanup.once("error", (error) => complete(`FaultLine could not force-remove its named container: ${error.message}`));
          cleanup.once("close", (code) => complete(code === 0 ? null : `FaultLine attempted docker rm --force for its named container (exit ${code ?? "unknown"}): ${cleanupStderr.trim()}`));
          timeout = setTimeout(() => {
            cleanup.kill("SIGKILL");
            complete("FaultLine timed out while force-removing its named container.");
          }, 5_000);
        });

        const finish = (exitCode: number | null, signal: string | null, extraStderr?: string): void => {
          if (settled) return;
          settled = true;
          if (timeout) clearTimeout(timeout);
          if (extraStderr) stderr = Buffer.concat([stderr, Buffer.from(extraStderr, "utf8")]);
          const resolveExecution = (cleanupDetail: string | null): void => {
            if (cleanupDetail) stderr = Buffer.concat([stderr, Buffer.from(`\n${cleanupDetail}`, "utf8")]);
            resolveResult({
              exitCode,
              signal,
              stdout: stdout.toString("utf8"),
              stderr: stderr.toString("utf8"),
              timedOut,
              outputLimitExceeded
            });
          };
          if (timedOut || outputLimitExceeded || exitCode === null) {
            void forceRemoveContainer().then(resolveExecution);
          } else {
            resolveExecution(null);
          }
        };

        let child;
        try {
          child = spawn(invocation.executable, [...invocation.arguments], {
            cwd: invocation.cwd,
            env: childEnvironment,
            shell: false,
            stdio: ["ignore", "pipe", "pipe"],
            windowsHide: true
          });
        } catch (error) {
          finish(null, null, error instanceof Error ? error.message : "Unable to start sandbox runner.");
          return;
        }

        const collect = (chunk: Buffer, destination: "stdout" | "stderr"): void => {
          observedOutputBytes += chunk.length;
          const remaining = Math.max(0, invocation.maxOutputBytes - (stdout.length + stderr.length));
          if (remaining > 0) {
            const captured = chunk.subarray(0, remaining);
            if (destination === "stdout") stdout = Buffer.concat([stdout, captured]);
            else stderr = Buffer.concat([stderr, captured]);
          }
          if (observedOutputBytes > invocation.maxOutputBytes && !outputLimitExceeded) {
            outputLimitExceeded = true;
            child.kill("SIGKILL");
          }
        };

        child.stdout?.on("data", (chunk: Buffer) => collect(Buffer.from(chunk), "stdout"));
        child.stderr?.on("data", (chunk: Buffer) => collect(Buffer.from(chunk), "stderr"));
        child.once("error", (error) => finish(null, null, error.message));
        child.once("close", (exitCode, signal) => finish(exitCode, signal));
        timeout = setTimeout(() => {
          timedOut = true;
          child.kill("SIGKILL");
        }, invocation.timeoutMs);
      });
    }
  };
}
