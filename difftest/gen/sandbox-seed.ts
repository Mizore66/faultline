// Sandbox audit variants with digests that match their facts, so the live
// test reaches the acceptance paths (legacy v1 policy, UNSAFE_LOCAL, a
// non-empty environment policy) and not only digest mismatches. The policy
// payloads copy the private helpers in src/sandbox.ts; TestLiveSandbox logs how
// many variants TS accepts, so a drifting copy shows up there.
import { digestJson } from "../../src/canonical.js";
import { ENVIRONMENT_POLICY_VERSION, SANDBOX_POLICY_VERSION, SANDBOX_SOURCE_TARGET } from "../../src/sandbox.js";

const LEGACY_SANDBOX_POLICY_VERSION = "faultline.sandbox.v1";
const LEGACY_SANDBOX_SOURCE_TARGET = "/workspace";
const DETERMINISTIC_ENVIRONMENT = { CI: "1", HOME: "/tmp", LANG: "C.UTF-8", LC_ALL: "C.UTF-8", SOURCE_DATE_EPOCH: "0", TZ: "UTC" };

type Audit = {
  kind: string; witnessDigest: string; environmentPolicyDigest: string; policyDigest: string;
  environment: { allowedKeys: string[]; passed: Array<{ key: string; valueDigest: string }>; redactedKeys: string[]; fixedKeys: string[] };
  runtime: Record<string, unknown> & { limits: Record<string, number>; image: string | null };
};

function policyPayload(audit: Audit, legacy: boolean): Record<string, unknown> {
  const schemaVersion = legacy ? LEGACY_SANDBOX_POLICY_VERSION : SANDBOX_POLICY_VERSION;
  if (audit.kind === "UNSAFE_LOCAL") {
    return {
      schemaVersion, kind: "UNSAFE_LOCAL", witnessDigest: audit.witnessDigest, warning: "No container isolation. Never use as proof.",
      limits: audit.runtime.limits, environmentPolicyDigest: audit.environmentPolicyDigest
    };
  }
  return {
    schemaVersion, kind: "DOCKER_ISOLATED", image: audit.runtime.image ?? "", witnessDigest: audit.witnessDigest,
    source: { target: legacy ? LEGACY_SANDBOX_SOURCE_TARGET : SANDBOX_SOURCE_TARGET, readOnly: true },
    network: "none", rootFilesystem: "read-only", user: "65534:65534", capDrop: "ALL", noNewPrivileges: true, pull: "never",
    entrypoint: "/bin/sh", limits: audit.runtime.limits, environmentPolicyDigest: audit.environmentPolicyDigest
  };
}

// resignSandbox recomputes the environment and policy digests from the
// audit's facts. Input it cannot sign is returned unchanged.
export function resignSandbox(text: string, legacy: boolean): string {
  try {
    const audit = JSON.parse(text) as Audit;
    audit.environmentPolicyDigest = digestJson({
      schemaVersion: ENVIRONMENT_POLICY_VERSION, fixed: DETERMINISTIC_ENVIRONMENT,
      allowedKeys: [...audit.environment.allowedKeys], passed: [...audit.environment.passed]
    });
    audit.policyDigest = digestJson(policyPayload(audit, legacy));
    return JSON.stringify(audit);
  } catch {
    return text;
  }
}

// sandboxVariant turns a committed Docker audit into one of the variants.
export function sandboxVariant(text: string, variant: "legacy" | "unsafe-local" | "environment"): string {
  const audit = JSON.parse(text) as Audit;
  if (variant === "unsafe-local") {
    audit.kind = "UNSAFE_LOCAL";
    Object.assign(audit.runtime, {
      image: null, entrypoint: null, network: null, rootFilesystemReadOnly: false, user: null, capDropAll: false, noNewPrivileges: false, pull: null
    });
  }
  if (variant === "environment") {
    audit.environment.allowedKeys = ["FEATURE_FLAG", "NODE_OPTIONS"];
    audit.environment.passed = [{ key: "FEATURE_FLAG", valueDigest: `sha256:${"e".repeat(64)}` }];
    audit.environment.redactedKeys = ["API_TOKEN"];
  }
  return resignSandbox(JSON.stringify(audit), variant === "legacy");
}
