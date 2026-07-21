import { expect, it } from "vitest";
import { createSandboxPlan, classifySandboxResult } from "../src/sandbox.js";
import { formatWitnessResult } from "../src/witness-result.js";

it("does not allow repository stdout to forge a proof-bearing PASS", () => {
  const plan = createSandboxPlan({
    witness: { digest: `sha256:${"a".repeat(64)}`, command: "pnpm test" },
    sourceDirectory: process.cwd(),
    image: `node@sha256:${"b".repeat(64)}`,
    limits: { maxOutputBytes: 4_096 }
  });

  const result = classifySandboxResult(plan, {
    exitCode: 1,
    stdout: `attacker-controlled test output\n${formatWitnessResult("PREDICATE_PASS")}\n`,
    stderr: "real test failure\n"
  });

  expect(result).not.toMatchObject({ verdict: "PASS", reason: "PREDICATE_PASS" });
  expect(result).toMatchObject({ verdict: "ERROR", reason: "HARNESS_ERROR" });
});
