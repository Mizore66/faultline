import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { digestJson, sha256 } from "../src/canonical.js";
import {
  PREVENTION_PROOF_SCHEMA_VERSION,
  defaultPreventionProofRoot,
  verifyPreventionProof,
  writePreventionProof,
  type PreventionProofWriteInput
} from "../src/prevention-proof.js";

const digest = (label: string) => `sha256:${sha256(label)}`;
const commit = (n: number) => `${n.toString(16).padStart(40, "0")}`;

function sampleInput(overrides: Partial<PreventionProofWriteInput> = {}): PreventionProofWriteInput {
  const witness = digest("witness");
  const environment = digest("environment");
  return {
    originalProofRoot: digest("proof-root"),
    frozenWitnessDigest: witness,
    investigationDigest: digest("investigation"),
    lastGood: {
      commit: commit(1),
      tree: commit(11),
      witnessDigest: witness,
      environmentDigest: environment,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED",
      distinctExecutionCount: 3
    },
    firstBad: {
      commit: commit(2),
      tree: commit(22),
      witnessDigest: witness,
      environmentDigest: environment,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED",
      distinctExecutionCount: 3
    },
    repaired: {
      commit: commit(3),
      tree: commit(33),
      witnessDigest: witness,
      environmentDigest: environment,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED",
      distinctExecutionCount: 3
    },
    repairPatchDigest: digest("patch"),
    codexThreadId: "thread_prevention_test",
    ...overrides
  };
}

const tempRoots: string[] = [];

afterEach(() => {
  for (const root of tempRoots.splice(0)) {
    rmSync(root, { recursive: true, force: true });
  }
});

describe("faultline.prevention-proof.v1", () => {
  it("writes and verifies a content-addressed prevention package", () => {
    const workspace = mkdtempSync(join(tmpdir(), "faultline-prevention-"));
    tempRoots.push(workspace);
    const previous = process.cwd();
    process.chdir(workspace);
    try {
      const written = writePreventionProof(join(defaultPreventionProofRoot(), "case-1"), sampleInput());
      expect(written.rootDigest).toMatch(/^sha256:[a-f0-9]{64}$/);
      expect(written.prevention.schemaVersion).toBe(PREVENTION_PROOF_SCHEMA_VERSION);
      expect(written.manifest.classification).toBe("PREVENTION_EVIDENCE_SUMMARY");

      const verified = verifyPreventionProof(written.directory, written.rootDigest);
      expect(verified.valid).toBe(true);
      expect(verified.externalRootStatus).toBe("MATCH");
      expect(verified.prevention?.codexThreadId).toBe("thread_prevention_test");
      expect(verified.prevention?.repairPatchDigest).toBe(digest("patch"));
    } finally {
      process.chdir(previous);
    }
  });

  it("fails closed on tamper and mismatched external root", () => {
    const workspace = mkdtempSync(join(tmpdir(), "faultline-prevention-tamper-"));
    tempRoots.push(workspace);
    const previous = process.cwd();
    process.chdir(workspace);
    try {
      const written = writePreventionProof(join(defaultPreventionProofRoot(), "case-2"), sampleInput());
      const bodyPath = join(written.directory, "prevention.json");
      const body = JSON.parse(readFileSync(bodyPath, "utf8")) as { repaired: { verdict: string } };
      body.repaired.verdict = "FAIL";
      writeFileSync(bodyPath, `${JSON.stringify(body, null, 2)}\n`, "utf8");

      const tampered = verifyPreventionProof(written.directory);
      expect(tampered.valid).toBe(false);
      expect(tampered.errors.join("\n")).toMatch(/digest|PASS|schema|verdict/i);

      const mismatch = verifyPreventionProof(written.directory, digest("wrong-root"));
      // restore would be needed for mismatch-only; rewrite clean package
      rmSync(written.directory, { recursive: true, force: true });
      const clean = writePreventionProof(join(defaultPreventionProofRoot(), "case-2b"), sampleInput());
      const external = verifyPreventionProof(clean.directory, digest("wrong-root"));
      expect(external.valid).toBe(false);
      expect(external.externalRootStatus).toBe("MISMATCH");
      expect(digestJson({ a: 1 })).toMatch(/^sha256:/);
    } finally {
      process.chdir(previous);
    }
  });

  it("refuses to write non-PASS/FAIL/PASS or non-Docker evidence", () => {
    const workspace = mkdtempSync(join(tmpdir(), "faultline-prevention-refuse-"));
    tempRoots.push(workspace);
    const previous = process.cwd();
    process.chdir(workspace);
    try {
      expect(() => writePreventionProof(join(defaultPreventionProofRoot(), "bad"), sampleInput({
        repaired: {
          ...sampleInput().repaired,
          distinctExecutionCount: 2
        }
      }))).toThrow(/greater than or equal to 3|three distinct executions|Refusing|Invalid/i);

      expect(() => writePreventionProof(join(defaultPreventionProofRoot(), "bad2"), sampleInput({
        lastGood: {
          ...sampleInput().lastGood,
          executionTrust: "UNSAFE_LOCAL" as "NATIVE_DOCKER"
        }
      }))).toThrow(/NATIVE_DOCKER|Invalid|Refusing/i);
    } finally {
      process.chdir(previous);
    }
  });
});
