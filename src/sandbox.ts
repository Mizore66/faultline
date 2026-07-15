import { spawn } from "node:child_process";
import { realpathSync, statSync } from "node:fs";
import { isAbsolute, parse, resolve } from "node:path";
import { digestJson, sha256 } from "./canonical.js";
import type { Witness } from "./domain.js";

/**
 * The sandbox boundary is intentionally conservative. A plan is evidence only
 * when it uses the Docker-isolated form below; an explicitly opted-in local
 * execution is marked INAPPLICABLE so it cannot silently become proof.
 */
export const SANDBOX_POLICY_VERSION = "faultline.sandbox.v1" as const;
export const ENVIRONMENT_POLICY_VERSION = "faultline.sandbox-environment.v1" as const;

export const DEFAULT_SANDBOX_LIMITS = Object.freeze({
  timeoutMs: 120_000,
  maxOutputBytes: 1_048_576,
  cpuCount: 1,
  memoryBytes: 536_870_912,
  pidsLimit: 128,
  tmpfsBytes: 67_108_864
});

const MAX_SANDBOX_LIMITS = Object.freeze({
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
export type SandboxVerdict = "PASS" | "FAIL" | "ERROR" | "INAPPLICABLE";
export type SandboxReason =
  | "EXIT_ZERO"
  | "EXIT_NONZERO"
  | "TIMEOUT"
  | "OUTPUT_LIMIT_EXCEEDED"
  | "SANDBOX_UNAVAILABLE"
  | "RUNNER_FAILURE"
  | "UNSAFE_LOCAL_NOT_PROOF";

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
  policyDigest: string
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
    })
  });
}

function dockerPolicyDigest(
  image: string,
  witness: FrozenSandboxWitness,
  limits: SandboxLimits,
  environmentPolicyDigest: string
): string {
  return digestJson({
    schemaVersion: SANDBOX_POLICY_VERSION,
    kind: "DOCKER_ISOLATED",
    image,
    witnessDigest: witness.digest,
    source: { target: "/workspace", readOnly: true },
    network: "none",
    rootFilesystem: "read-only",
    user: "65534:65534",
    capDrop: "ALL",
    noNewPrivileges: true,
    pull: "never",
    limits,
    environmentPolicyDigest
  });
}

function unsafeLocalPolicyDigest(
  witness: FrozenSandboxWitness,
  limits: SandboxLimits,
  environmentPolicyDigest: string
): string {
  return digestJson({
    schemaVersion: SANDBOX_POLICY_VERSION,
    kind: "UNSAFE_LOCAL",
    witnessDigest: witness.digest,
    warning: "No container isolation. Never use as proof.",
    limits,
    environmentPolicyDigest
  });
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
  const commandArguments = Object.freeze([
    "run",
    "--rm",
    "--init",
    "--pull=never",
    "--network",
    "none",
    "--read-only",
    "--user",
    "65534:65534",
    "--cap-drop",
    "ALL",
    "--security-opt",
    "no-new-privileges:true",
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
    `type=bind,src=${sourceDirectory},dst=/workspace,readonly`,
    "--workdir",
    "/workspace",
    ...environmentArguments(environment.values),
    image,
    "/bin/sh",
    "-lc",
    witness.command
  ]);
  return Object.freeze({
    kind: "DOCKER_ISOLATED" as const,
    executable: "docker" as const,
    arguments: commandArguments,
    sourceDirectory,
    image,
    limits,
    environment: environment.values,
    environmentPolicy: environment.policy,
    environmentPolicyDigest,
    policyDigest,
    audit: createAudit("DOCKER_ISOLATED", witness, environment, environmentPolicyDigest, policyDigest)
  });
}

/**
 * Local execution is deliberately opt-in and produces no PASS/FAIL evidence.
 * It exists only to make a developer's explicitly acknowledged escape hatch
 * visible in the proof record rather than silently falling back from Docker.
 */
export function createUnsafeLocalSandboxPlan(request: SandboxPlanRequest): UnsafeLocalSandboxPlan {
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
    audit: createAudit("UNSAFE_LOCAL", witness, environment, environmentPolicyDigest, policyDigest)
  });
}

export function createSandboxPlan(request: SandboxPlanRequest): SandboxPlan {
  if (request.mode === "UNSAFE_LOCAL") {
    return createUnsafeLocalSandboxPlan(request);
  }
  if (request.allowUnsafeLocal === true) {
    throw new Error("allowUnsafeLocal is only valid together with mode: UNSAFE_LOCAL.");
  }
  return createDockerSandboxPlan(request);
}

/** A serializable, value-redacted representation safe for proof bundles. */
export function auditSandboxPlan(plan: SandboxPlan): SandboxPlanAudit {
  return plan.audit;
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
export function classifySandboxResult(plan: SandboxPlan, result: SandboxCommandResult): SandboxExecutionResult {
  const exceedsOutputLimit = result.outputLimitExceeded === true || outputSize(result.stdout, result.stderr) > plan.limits.maxOutputBytes;
  const clipped = exceedsOutputLimit ? truncateOutput(result.stdout, result.stderr, plan.limits.maxOutputBytes) : result;
  const signal = result.signal ?? null;
  const base = {
    kind: plan.kind,
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
  // Docker reserves 125 for CLI/daemon failures before the container command runs.
  if (result.exitCode === 125) {
    return { ...base, verdict: "ERROR", reason: "SANDBOX_UNAVAILABLE" };
  }
  if (result.exitCode === 0) {
    return { ...base, verdict: "PASS", reason: "EXIT_ZERO" };
  }
  return { ...base, verdict: "FAIL", reason: "EXIT_NONZERO" };
}

/**
 * Execute a plan through an injectable runner. Tests can exercise all safety
 * paths with a fake runner; production may use createNodeSandboxRunner().
 */
export async function executeSandboxPlan(
  plan: SandboxPlan,
  runner: SandboxCommandRunner = createNodeSandboxRunner()
): Promise<SandboxExecutionResult> {
  try {
    const result = await runner.run({
      kind: plan.kind,
      executable: plan.executable,
      arguments: plan.arguments,
      cwd: plan.sourceDirectory,
      timeoutMs: plan.limits.timeoutMs,
      maxOutputBytes: plan.limits.maxOutputBytes,
      environment: plan.environment
    });
    return classifySandboxResult(plan, result);
  } catch (error) {
    return {
      kind: plan.kind,
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

        const finish = (exitCode: number | null, signal: string | null, extraStderr?: string): void => {
          if (settled) return;
          settled = true;
          if (timeout) clearTimeout(timeout);
          if (extraStderr) stderr = Buffer.concat([stderr, Buffer.from(extraStderr, "utf8")]);
          resolveResult({
            exitCode,
            signal,
            stdout: stdout.toString("utf8"),
            stderr: stderr.toString("utf8"),
            timedOut,
            outputLimitExceeded
          });
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
