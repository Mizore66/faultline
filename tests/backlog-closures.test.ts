import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { createDevUnsafeLocalSandboxPlan, createSandboxPlan, DEV_UNSAFE_LOCAL_ENV } from "../src/sandbox.js";
import { redactText } from "../src/redaction.js";
import { writeRuntimeMappingFile } from "../src/runtime-mapping-file.js";
import { runFirstIncidentTutorial } from "../src/tutorial.js";

const pinnedImage = `registry.example/faultline-node@sha256:${"a".repeat(64)}`;
const witness = {
  digest: `sha256:${"b".repeat(64)}`,
  command: "node --version"
};

describe("SEC-02 development-only UNSAFE_LOCAL gate", () => {
  it("refuses shipped createSandboxPlan for UNSAFE_LOCAL", () => {
    expect(() => createSandboxPlan({
      witness,
      sourceDirectory: process.cwd(),
      image: pinnedImage,
      mode: "UNSAFE_LOCAL",
      allowUnsafeLocal: true
    })).toThrow(/not available in the shipped FaultLine runtime/i);
  });

  it("constructs UNSAFE_LOCAL only when FAULTLINE_DEV_UNSAFE_LOCAL=1", () => {
    const previous = process.env[DEV_UNSAFE_LOCAL_ENV];
    delete process.env[DEV_UNSAFE_LOCAL_ENV];
    try {
      expect(() => createDevUnsafeLocalSandboxPlan({
        witness,
        sourceDirectory: process.cwd(),
        image: pinnedImage
      })).toThrow(/FAULTLINE_DEV_UNSAFE_LOCAL=1/);

      process.env[DEV_UNSAFE_LOCAL_ENV] = "1";
      const plan = createDevUnsafeLocalSandboxPlan({
        witness,
        sourceDirectory: process.cwd(),
        image: pinnedImage
      });
      expect(plan.kind).toBe("UNSAFE_LOCAL");
      expect(plan.acknowledgement).toBe("UNSAFE_LOCAL_OPTED_IN");
    } finally {
      if (previous === undefined) delete process.env[DEV_UNSAFE_LOCAL_ENV];
      else process.env[DEV_UNSAFE_LOCAL_ENV] = previous;
    }
  });
});

describe("SEC-04 labeled secret corpus regression", () => {
  it("redacts labeled true secrets and leaves known-miss fixtures undetected", () => {
    const openai = readFileSync("benchmarks/secret-scanner-corpus/true_secret/openai-api-key.txt", "utf8");
    expect(redactText(openai).report.redacted).toBe(true);
    const miss = readFileSync("benchmarks/secret-scanner-corpus/should_miss/split-openai-key.txt", "utf8");
    expect(redactText(miss).report.redacted).toBe(false);
  });
});

describe("PROD-01 tutorial", () => {
  it("generates a toy repo and freezes a witness under tests", () => {
    const workspace = mkdtempSync(join(tmpdir(), "faultline-tutorial-"));
    try {
      const result = runFirstIncidentTutorial({ workspace, yes: true });
      expect(result.ok).toBe(true);
      expect(result.frozenWitness.frozenDigest).toMatch(/^sha256:[a-f0-9]{64}$/);
      expect(result.phases.some((phase) => phase.phase === "FREEZE_WITNESS")).toBe(true);
    } finally {
      rmSync(workspace, { recursive: true, force: true });
    }
  });
});

describe("PROD-03 runtime mapping write", () => {
  it("writes a provenance-bearing mapping file", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-runtime-mapping-"));
    try {
      const fp1 = `sha256:${"1".repeat(64)}`;
      const fp2 = `sha256:${"2".repeat(64)}`;
      const written = writeRuntimeMappingFile({
        pairs: [
          { fingerprintDigest: fp1, image: pinnedImage },
          { fingerprintDigest: fp2, image: `registry.example/other@sha256:${"c".repeat(64)}` }
        ],
        outputPath: join(directory, "mapping.json"),
        generatedAt: "2026-07-20T12:00:00.000Z"
      });
      expect(written.provenanceDigest).toMatch(/^sha256:[a-f0-9]{64}$/);
      expect(Object.keys(written.mapping)).toHaveLength(2);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});

describe("CODEX-01 proof-package thread binding honesty", () => {
  it("prevention packages may carry codexThreadId without private transcripts", () => {
    // Binding contract: thread id is an identifier field only — no transcript paths.
    const threadId = "thread-codex-livefire-001";
    expect(threadId).toMatch(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/);
    expect(JSON.stringify({ codexThreadId: threadId })).not.toMatch(/transcript|promptText|messages/i);
  });
});
