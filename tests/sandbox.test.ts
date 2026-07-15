import { describe, expect, it } from "vitest";
import {
  auditSandboxPlan,
  classifySandboxResult,
  createSandboxPlan,
  createUnsafeLocalSandboxPlan,
  executeSandboxPlan,
  type SandboxCommandRunner,
  type SandboxPlanRequest
} from "../src/sandbox.js";

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
    expect(first.arguments).toContain("--pull=never");
    expect(first.arguments).toContain("FOO=green");
    expect(first.arguments.join(" ")).not.toContain("OPENAI_API_KEY");
    expect(first.arguments.join(" ")).not.toContain("sk-should-never-leak");

    const audit = auditSandboxPlan(first);
    expect(audit.environment.redactedKeys).toEqual(["OPENAI_API_KEY"]);
    expect(JSON.stringify(audit)).not.toContain("sk-should-never-leak");
    expect(audit.environment.passed).toHaveLength(1);
    expect(audit.environment.passed[0]?.key).toBe("FOO");
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
    const plan = createSandboxPlan(dockerRequest({ limits: { timeoutMs: 7_000, maxOutputBytes: 64 } }));
    const calls: unknown[] = [];
    const passingRunner: SandboxCommandRunner = {
      async run(invocation) {
        calls.push(invocation);
        return { exitCode: 0, stdout: "witness passed\n", stderr: "" };
      }
    };

    await expect(executeSandboxPlan(plan, passingRunner)).resolves.toMatchObject({
      verdict: "PASS",
      reason: "EXIT_ZERO",
      kind: "DOCKER_ISOLATED"
    });
    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({ executable: "docker", timeoutMs: 7_000, maxOutputBytes: 64 });

    expect(classifySandboxResult(plan, {
      exitCode: 1,
      stdout: "expected failure",
      stderr: ""
    })).toMatchObject({ verdict: "FAIL", reason: "EXIT_NONZERO" });
    expect(classifySandboxResult(plan, {
      exitCode: null,
      stdout: "",
      stderr: "Docker daemon unavailable",
      timedOut: true
    })).toMatchObject({ verdict: "ERROR", reason: "TIMEOUT" });
    expect(classifySandboxResult(plan, {
      exitCode: 0,
      stdout: "x".repeat(65),
      stderr: ""
    })).toMatchObject({ verdict: "ERROR", reason: "OUTPUT_LIMIT_EXCEEDED", outputTruncated: true });
  });

  it("marks every explicitly unsafe local execution as inapplicable evidence", () => {
    const plan = createSandboxPlan(dockerRequest({ mode: "UNSAFE_LOCAL", allowUnsafeLocal: true }));
    expect(classifySandboxResult(plan, {
      exitCode: 0,
      stdout: "local command passed",
      stderr: ""
    })).toMatchObject({ verdict: "INAPPLICABLE", reason: "UNSAFE_LOCAL_NOT_PROOF" });
  });
});
