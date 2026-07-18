import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  buildEffectiveRuntimeMapping,
  computeEnvironmentFingerprint,
  ENVIRONMENT_CHANGED_PROOF_MESSAGE,
  ENVIRONMENT_DESCRIPTOR_FILES,
  ENVIRONMENT_FINGERPRINT_VERSION,
  environmentHomogeneity,
  missingRuntimeMappingDigests,
  resolveRuntimeImageForFingerprint
} from "../src/environment-fingerprint.js";

describe("environment fingerprints", () => {
  it("includes package manifests and compose files in the descriptor set", () => {
    expect(ENVIRONMENT_FINGERPRINT_VERSION).toBe("faultline.environment-fingerprint.v2");
    expect(ENVIRONMENT_DESCRIPTOR_FILES).toEqual(expect.arrayContaining([
      "package.json",
      "pyproject.toml",
      "uv.lock",
      "Cargo.toml",
      "rust-toolchain.toml",
      ".tool-versions",
      "docker-compose.yml",
      "compose.yaml"
    ]));
  });

  it("changes digest when package.json changes with a stable lockfile", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-env-fp-"));
    try {
      writeFileSync(join(root, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\nSAME\n", "utf8");
      writeFileSync(join(root, "package.json"), JSON.stringify({ scripts: { test: "a" } }), "utf8");
      const before = computeEnvironmentFingerprint(root);
      writeFileSync(join(root, "package.json"), JSON.stringify({ scripts: { test: "b" } }), "utf8");
      const after = computeEnvironmentFingerprint(root);
      expect(before.digest).not.toBe(after.digest);
      expect(environmentHomogeneity([before, after])).toBe("HETEROGENEOUS");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("requires an explicit mapping entry for every heterogeneous fingerprint", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-env-map-"));
    try {
      writeFileSync(join(root, "package.json"), JSON.stringify({ name: "a" }), "utf8");
      const first = computeEnvironmentFingerprint(root);
      writeFileSync(join(root, "package.json"), JSON.stringify({ name: "b" }), "utf8");
      const second = computeEnvironmentFingerprint(root);
      const imageA = `registry.example/a@sha256:${"a".repeat(64)}`;
      const imageB = `registry.example/b@sha256:${"b".repeat(64)}`;
      expect(missingRuntimeMappingDigests([first, second], { [first.digest]: imageA })).toEqual([second.digest]);
      expect(() => resolveRuntimeImageForFingerprint({
        fingerprintDigest: second.digest,
        homogeneity: "HETEROGENEOUS",
        runtimeMapping: { [first.digest]: imageA },
        defaultImage: imageB
      })).toThrow(ENVIRONMENT_CHANGED_PROOF_MESSAGE);
      const effective = buildEffectiveRuntimeMapping(
        [first, second],
        { [first.digest]: imageA, [second.digest]: imageB },
        imageA,
        "HETEROGENEOUS"
      );
      expect(effective).toEqual({
        [first.digest]: imageA,
        [second.digest]: imageB
      });
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
