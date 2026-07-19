import { describe, expect, it } from "vitest";
import {
  SANDBOX_SOURCE_TARGET,
  auditSandboxPlan,
  classifySandboxResult,
  createSandboxPlan,
  createUnsafeLocalSandboxPlan,
  executeSandboxPlan,
  validateSandboxPlanAudit,
  type SandboxCommandRunner,
  type SandboxPlanRequest
} from "../src/sandbox.js";
import { digestJson } from "../src/canonical.js";
import { formatWitnessResult } from "../src/witness-result.js";

const pinnedImage = `registry.example/faultline-node@sha256:${"a".repeat(64)}`;
const witness = {
  digest: `sha256:${"b".repeat(64)}`,
  command: "node --version"
};

function dockerRequest(overrides: Partial<SandboxPlanRequest> = {}): SandboxPlanRequest {
  return {
    witness,
    sourceDirectory: process.cwd(),
    image: pinnedImage,
    ...overrides
  };
}

describe("FaultLine frozen-witness sandbox plans", () => {
  it("builds a locked-down Docker argv plan with a stable environment policy digest", () => {
    const first = createSandboxPlan(dockerRequest({
      environment: { FOO: "green", CI: "1", OPENAI_API_KEY: "sk-should-never-leak" },
      allowedEnvironment: ["FOO"]
    }));
    const second = createSandboxPlan(dockerRequest({
      environment: { OPENAI_API_KEY: "sk-should-never-leak", CI: "1", FOO: "green" },
      allowedEnvironment: ["FOO"]
    }));

    expect(first.kind).toBe("DOCKER_ISOLATED");
    expect(first.environmentPolicyDigest).toBe(second.environmentPolicyDigest);
    expect(first.policyDigest).toBe(second.policyDigest);
    expect(first.arguments).toContain("--network");
    expect(first.arguments).toContain("none");
    expect(first.arguments).toContain("--ipc");
    expect(first.arguments).toContain("none");
    expect(first.arguments).toContain("--ulimit");
    expect(first.arguments).toContain("fsize=1048576:1048576");
    expect(first.arguments).toContain("--read-only");
    expect(first.arguments).toContain("65534:65534");
    expect(first.arguments).toContain("--cap-drop");
    expect(first.arguments).toContain("ALL");
    expect(first.arguments).toContain("no-new-privileges:true");
    expect(first.arguments).toContain("--pids-limit");
    expect(first.arguments).toContain("--memory");
    expect(first.arguments).toContain("--cpus");
    expect(first.arguments).toContain("--tmpfs");
    expect(first.arguments).toContain("--mount");
    expect(first.arguments).toContain(`type=bind,src=${first.sourceDirectory},dst=${SANDBOX_SOURCE_TARGET},readonly`);
    expect(first.arguments).toContain("--workdir");
    expect(first.arguments).toContain(SANDBOX_SOURCE_TARGET);
    expect(first.arguments).toContain("--pull=never");
    expect(first.arguments).toContain("FOO=green");
    expect(first.arguments.join(" ")).not.toContain("OPENAI_API_KEY");
    expect(first.arguments.join(" ")).not.toContain("sk-should-never-leak");

    const audit = auditSandboxPlan(first);
    expect(audit.environment.redactedKeys).toEqual(["OPENAI_API_KEY"]);
    expect(JSON.stringify(audit)).not.toContain("sk-should-never-leak");
    expect(audit.environment.passed).toHaveLength(1);
    expect(audit.environment.passed[0]?.key).toBe("FOO");
    expect(validateSandboxPlanAudit(audit)).toEqual([]);
    const legacyPolicyDigest = digestJson({
      schemaVersion: "faultline.sandbox.v1",
      kind: "DOCKER_ISOLATED",
      image: audit.runtime.image,
      witnessDigest: audit.witnessDigest,
      source: { target: "/workspace", readOnly: true },
      network: "none",
      rootFilesystem: "read-only",
      user: "65534:65534",
      capDrop: "ALL",
      noNewPrivileges: true,
      pull: "never",
      entrypoint: "/bin/sh",
      limits: audit.runtime.limits,
      environmentPolicyDigest: audit.environmentPolicyDigest
    });
    expect(validateSandboxPlanAudit({ ...audit, policyDigest: legacyPolicyDigest })).toEqual([]);
    expect(validateSandboxPlanAudit({
      ...audit,
      runtime: { ...audit.runtime, limits: { ...audit.runtime.limits, memoryBytes: 9_999_999_999 } }
    })).toContain("sandbox limit memoryBytes is outside the allowed policy range");
  });

  it("fails closed for mutable images, dangerous environment entries, and oversized limits", () => {
    expect(() => createSandboxPlan(dockerRequest({ image: "node:22" }))).toThrow(/digest-pinned/);
    expect(() => createSandboxPlan(dockerRequest({ allowedEnvironment: ["OPENAI_API_KEY"] }))).toThrow(/secret-like/);
    expect(() => createSandboxPlan(dockerRequest({ environment: { TZ: "America/New_York" } }))).toThrow(/deterministic environment/);
    expect(() => createSandboxPlan(dockerRequest({ limits: { memoryBytes: 2_147_483_649 } }))).toThrow(/memoryBytes/);
  });

  it("never creates an unsafe local plan without an explicit acknowledgement", () => {
    expect(() => createSandboxPlan(dockerRequest({ mode: "UNSAFE_LOCAL" }))).toThrow(/allowUnsafeLocal: true/);
    expect(() => createSandboxPlan(dockerRequest({ allowUnsafeLocal: true }))).toThrow(/only valid together/);

    const plan = createUnsafeLocalSandboxPlan(dockerRequest({
      mode: "UNSAFE_LOCAL",
      allowUnsafeLocal: true
    }));
    expect(plan.kind).toBe("UNSAFE_LOCAL");
    expect(plan.acknowledgement).toBe("UNSAFE_LOCAL_OPTED_IN");
    expect(plan.audit.kind).toBe("UNSAFE_LOCAL");
  });

  it("classifies injected runner results without requiring a Docker daemon", async () => {
    const plan = createSandboxPlan(dockerRequest({ limits: { timeoutMs: 7_000, maxOutputBytes: 1_024 } }));
    const calls: unknown[] = [];
    const passingRunner: SandboxCommandRunner = {
      async run(invocation) {
        calls.push(invocation);
        return { exitCode: 0, stdout: `witness passed\n${formatWitnessResult("PREDICATE_PASS")}\n`, stderr: "" };
      }
    };

    await expect(executeSandboxPlan(plan, passingRunner)).resolves.toMatchObject({
      verdict: "PASS",
      reason: "PREDICATE_PASS",
      kind: "DOCKER_ISOLATED"
    });
    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({ executable: "docker", timeoutMs: 7_000, maxOutputBytes: 1_024 });

    expect(classifySandboxResult(plan, {
      exitCode: 0,
      stdout: "unstructured pass",
      stderr: ""
    })).toMatchObject({ verdict: "ERROR", reason: "EXIT_ZERO_UNSTRUCTURED" });
    const dockerUnavailablePlan = createSandboxPlan(dockerRequest({ limits: { maxOutputBytes: 1_024 } }));
    // An unstructured nonzero exit is never trusted as a real predicate
    // failure: a compile/setup incompatibility looks identical to it.
    expect(classifySandboxResult(dockerUnavailablePlan, {
      exitCode: 1,
      stdout: "expected failure",
      stderr: ""
    })).toMatchObject({ verdict: "ERROR", reason: "EXIT_NONZERO_UNSTRUCTURED" });
    expect(classifySandboxResult(plan, {
      exitCode: null,
      stdout: "",
      stderr: "Docker daemon unavailable",
      timedOut: true
    })).toMatchObject({ verdict: "ERROR", reason: "TIMEOUT" });
    const tightLimitPlan = createSandboxPlan(dockerRequest({ limits: { maxOutputBytes: 64 } }));
    expect(classifySandboxResult(tightLimitPlan, {
      exitCode: 0,
      stdout: "x".repeat(65),
      stderr: ""
    })).toMatchObject({ verdict: "ERROR", reason: "OUTPUT_LIMIT_EXCEEDED", outputTruncated: true });
    expect(classifySandboxResult(dockerUnavailablePlan, {
      exitCode: 1,
      stdout: "",
      stderr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"
    })).toMatchObject({ verdict: "ERROR", reason: "SANDBOX_UNAVAILABLE" });
  });

  it("marks every explicitly unsafe local execution as inapplicable evidence", () => {
    const plan = createSandboxPlan(dockerRequest({ mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }));
    expect(classifySandboxResult(plan, {
      exitCode: 0,
      stdout: "local command passed",
      stderr: ""
    })).toMatchObject({ verdict: "INAPPLICABLE", reason: "UNSAFE_LOCAL_NOT_PROOF" });
  });

  it("classifies structured witness-result outcomes independently of exit code", () => {
    const plan = createSandboxPlan(dockerRequest({ limits: { maxOutputBytes: 4_096 } }));

    expect(classifySandboxResult(plan, {
      exitCode: 1,
      stdout: `some diagnostic noise\n${formatWitnessResult("PREDICATE_FAIL")}\n`,
      stderr: ""
    })).toMatchObject({ verdict: "FAIL", reason: "PREDICATE_FAIL" });

    expect(classifySandboxResult(plan, {
      exitCode: 0,
      stdout: `${formatWitnessResult("PREDICATE_PASS")}\n`,
      stderr: ""
    })).toMatchObject({ verdict: "PASS", reason: "PREDICATE_PASS" });

    // A structured outcome wins even against an exit code that would
    // otherwise imply the opposite legacy verdict.
    expect(classifySandboxResult(plan, {
      exitCode: 0,
      stdout: `${formatWitnessResult("PREDICATE_FAIL")}\n`,
      stderr: ""
    })).toMatchObject({ verdict: "FAIL", reason: "PREDICATE_FAIL" });

    expect(classifySandboxResult(plan, {
      exitCode: 1,
      stdout: `${formatWitnessResult("INCOMPATIBLE_STATE")}\n`,
      stderr: ""
    })).toMatchObject({ verdict: "INAPPLICABLE", reason: "INCOMPATIBLE_STATE" });

    expect(classifySandboxResult(plan, {
      exitCode: 1,
      stdout: `${formatWitnessResult("HARNESS_ERROR")}\n`,
      stderr: ""
    })).toMatchObject({ verdict: "ERROR", reason: "HARNESS_ERROR" });

    expect(classifySandboxResult(plan, {
      exitCode: 1,
      stdout: `${formatWitnessResult("INFRASTRUCTURE_ERROR")}\n`,
      stderr: ""
    })).toMatchObject({ verdict: "ERROR", reason: "SANDBOX_UNAVAILABLE" });
  });

  it("classifies compile/setup incompatibility signatures as inapplicable rather than a predicate failure", () => {
    const plan = createSandboxPlan(dockerRequest({ limits: { maxOutputBytes: 4_096 } }));

    expect(classifySandboxResult(plan, {
      exitCode: 1,
      stdout: "",
      stderr: "file.ts:3:1 - error TS2304: Cannot find name 'foo'.\nSyntaxError: Unexpected token"
    })).toMatchObject({ verdict: "INAPPLICABLE", reason: "INCOMPATIBLE_STATE" });

    expect(classifySandboxResult(plan, {
      exitCode: 1,
      stdout: "",
      stderr: "error[E0433]: failed to resolve: use of undeclared crate\nerror: could not compile `witness`"
    })).toMatchObject({ verdict: "INAPPLICABLE", reason: "INCOMPATIBLE_STATE" });

    // Still ERROR (not FAIL) for a genuinely unstructured nonzero exit that
    // matches none of the known infra/compile signatures.
    expect(classifySandboxResult(plan, {
      exitCode: 3,
      stdout: "predicate produced no structured result\n",
      stderr: ""
    })).toMatchObject({ verdict: "ERROR", reason: "EXIT_NONZERO_UNSTRUCTURED" });
  });
});
