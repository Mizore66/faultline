import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { computeEnvironmentFingerprint, environmentHomogeneity } from "../src/environment-fingerprint.js";
import { createDemoAnalysis } from "../src/engine.js";

describe("adversarial scenario coverage matrix specs", () => {
  it("publishes eight distinct expected outcomes for the coverage matrix", () => {
    const directory = join(process.cwd(), "benchmarks", "incidents");
    const specs = readdirSync(directory).filter((name) => name.endsWith(".json")).map((name) =>
      JSON.parse(readFileSync(join(directory, name), "utf8")) as { id: string; expectedOutcome: string }
    );
    expect(specs).toHaveLength(8);
    expect(new Set(specs.map((spec) => spec.expectedOutcome)).size).toBe(8);
  });

  it("regenerates a coverage-matrix report (not an E2E benchmark)", async () => {
    const { spawnSync } = await import("node:child_process");
    const result = spawnSync("node", ["scripts/run-coverage-matrix.mjs"], {
      cwd: process.cwd(),
      encoding: "utf8"
    });
    expect(result.status).toBe(0);
    const report = JSON.parse(readFileSync(join(process.cwd(), "benchmarks", "report.json"), "utf8")) as {
      schemaVersion: string;
      kind: string;
      unsupportedExactCauseClaims: number;
      rows: Array<{ expected: string; observed: string; result: string }>;
    };
    expect(report.schemaVersion).toBe("faultline.coverage-matrix.v1");
    expect(report.kind).toBe("COVERAGE_MATRIX");
    expect(report.unsupportedExactCauseClaims).toBe(0);
    expect(report.rows).toHaveLength(8);
    expect(report.rows.every((row) => row.expected === row.observed && row.result === "PASS")).toBe(true);
  });
});

describe("environment fingerprints", () => {
  it("hashes present lockfiles and detects heterogeneous digests", () => {
    const fingerprint = computeEnvironmentFingerprint(process.cwd());
    expect(fingerprint.schemaVersion).toBe("faultline.environment-fingerprint.v2");
    expect(fingerprint.digest.startsWith("sha256:")).toBe(true);
    expect(environmentHomogeneity([fingerprint])).toBe("HOMOGENEOUS");
    expect(environmentHomogeneity([
      fingerprint,
      { ...fingerprint, digest: `sha256:${"d".repeat(64)}` }
    ])).toBe("HETEROGENEOUS");
  });
});

describe("demo metrics honesty", () => {
  it("derives session and turn counts from fixture states rather than theater constants", () => {
    const analysis = createDemoAnalysis("REPLAY");
    expect(analysis.metrics.sessions).toBe(4);
    expect(analysis.metrics.turns).toBe(8);
    expect(analysis.metrics.implicatedHunks).toBe(2);
    expect(analysis.metrics.changedLines).toBeLessThan(100);
  });
});
