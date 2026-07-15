import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { makeBlindIncidentPacket } from "../src/ai.js";
import { sha256 } from "../src/canonical.js";
import { createDemoAnalysis } from "../src/engine.js";
import { verifyProofBundle, writeProofBundle } from "../src/proof-bundle.js";
import { renderIncidentPage } from "../src/ui.js";

function refreshBundleHash(directory: string, file: string): string {
  const hashesPath = join(directory, "hashes.txt");
  const updatedHashes = `${readFileSync(hashesPath, "utf8").trim().split("\n").map((line) => {
    if (!line.endsWith(`  ${file}`)) return line;
    return `${sha256(readFileSync(join(directory, file)))}  ${file}`;
  }).join("\n")}\n`;
  writeFileSync(hashesPath, updatedHashes, "utf8");
  const root = `sha256:${sha256(updatedHashes)}`;
  writeFileSync(join(directory, "ROOT.sha256"), `${root}\n`, "utf8");
  return root;
}

describe("FaultLine deterministic sample", () => {
  it("finds the stable boundary and proves the two-hunk interaction", () => {
    const analysis = createDemoAnalysis("RERUN");
    const firstRegression = analysis.transitions.find((transition) => transition.kind === "PASS_TO_FAIL" && transition.stable);
    expect(firstRegression?.beforeStateId).toBe("cedar-turn-4");
    expect(firstRegression?.afterStateId).toBe("cedar-turn-5");
    expect(analysis.minimization.candidate).toEqual(["settlement-display-default", "settlement-display-boundary"]);
    expect(analysis.minimization.attempts.find((attempt) => attempt.id === "attempt-source")?.outcome).toBe("PASS");
    expect(analysis.minimization.attempts.find((attempt) => attempt.id === "attempt-renderer")?.outcome).toBe("PASS");
    expect(analysis.minimization.attempts.find((attempt) => attempt.id === "attempt-partial")?.outcome).toBe("UNRESOLVED");
    expect(analysis.minimization.sufficiency.verdict).toBe("FAIL");
    expect(analysis.minimization.necessity.verdict).toBe("PASS");
    expect(analysis.prevention.verified).toBe(true);
    expect(analysis.prevention.lastGood.verdict).toBe("PASS");
    expect(analysis.prevention.firstBad.verdict).toBe("FAIL");
    expect(analysis.prevention.repaired.verdict).toBe("PASS");
    expect(new Set(firstRegression?.boundaryRunIds).size).toBe(6);
    expect(firstRegression?.boundaryRunIds).toHaveLength(6);
    expect(analysis.runCatalog.filter((run) => firstRegression?.boundaryRunIds.includes(run.id)).every((run) => run.executionKind === "EXECUTED")).toBe(true);
  });

  it("does not certify cached replay as executable proof", () => {
    const replay = createDemoAnalysis("REPLAY");
    expect(replay.transitions.some((transition) => transition.stable)).toBe(false);
    expect(replay.grade.value).toBe("D");
    expect(replay.prevention.verified).toBe(false);
    expect(replay.minimization.termination).toBe("NOT_EXECUTED");
  });

  it("covers every cited run and requires an external root for tamper detection", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-proof-"));
    const proofRoot = join(root, ".faultline", "bundles");
    const directory = join(proofRoot, "bundle");
    try {
      mkdirSync(proofRoot, { recursive: true });
      const written = writeProofBundle(directory, createDemoAnalysis("REPLAY"), { proofRoot });
      const verified = verifyProofBundle(directory, written.rootDigest);
      expect(verified).toMatchObject({ valid: true, externalRootStatus: "MATCH" });
      expect(verified.checkedFiles).toBeGreaterThan(52);
      const reportPath = join(directory, "report.md");
      const changedReport = `${readFileSync(reportPath, "utf8")}tamper`;
      writeFileSync(reportPath, changedReport, "utf8");
      const hashesPath = join(directory, "hashes.txt");
      const rewrittenHashes = readFileSync(hashesPath, "utf8").replace(/^[a-f0-9]{64}(?=  report\.md$)/m, sha256(changedReport));
      writeFileSync(hashesPath, rewrittenHashes, "utf8");
      writeFileSync(join(directory, "ROOT.sha256"), `sha256:${sha256(rewrittenHashes)}\n`, "utf8");
      expect(verifyProofBundle(directory)).toMatchObject({ valid: true, externalRootStatus: "NOT_PROVIDED" });
      expect(verifyProofBundle(directory, written.rootDigest)).toMatchObject({ valid: false, externalRootStatus: "MISMATCH" });
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses dangerous proof output paths before replacing anything", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-output-"));
    const proofRoot = join(root, ".faultline", "bundles");
    const protectedFile = join(root, "keep.txt");
    try {
      mkdirSync(proofRoot, { recursive: true });
      writeFileSync(protectedFile, "do not touch\n", "utf8");
      expect(() => writeProofBundle(root, createDemoAnalysis("REPLAY"), { proofRoot })).toThrow(/child directory/);
      expect(readFileSync(protectedFile, "utf8")).toBe("do not touch\n");
      expect(existsSync(protectedFile)).toBe(true);
      const unmanaged = join(proofRoot, "unmanaged");
      mkdirSync(unmanaged);
      writeFileSync(join(unmanaged, "sentinel.txt"), "keep\n", "utf8");
      expect(() => writeProofBundle(unmanaged, createDemoAnalysis("REPLAY"), { proofRoot })).toThrow(/only replace a verified/);
      expect(readFileSync(join(unmanaged, "sentinel.txt"), "utf8")).toBe("keep\n");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("rejects a rehashed bundle whose persisted evidence contradicts the analysis", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-semantic-"));
    const proofRoot = join(root, ".faultline", "bundles");
    const directory = join(proofRoot, "bundle");
    try {
      mkdirSync(proofRoot, { recursive: true });
      const analysis = createDemoAnalysis("RERUN");
      writeProofBundle(directory, analysis, { proofRoot });
      const firstBad = analysis.prevention.firstBad;
      const resultFile = `runs/${firstBad.id.replace(/[^a-zA-Z0-9_-]/g, "_")}/result.json`;
      const altered = JSON.parse(readFileSync(join(directory, resultFile), "utf8")) as { verdict: string };
      altered.verdict = "PASS";
      writeFileSync(join(directory, resultFile), `${JSON.stringify(altered, null, 2)}\n`, "utf8");
      const forgedRoot = refreshBundleHash(directory, resultFile);
      const verified = verifyProofBundle(directory, forgedRoot);
      expect(verified.valid).toBe(false);
      expect(verified.errors.some((error) => /persisted run does not match catalog|prevention claim/.test(error))).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("rejects localization data from the blinded GPT packet", () => {
    expect(() => makeBlindIncidentPacket({
      symptom: "CI fails",
      ciLog: "expected 0, received 1",
      repositoryLanguage: "TypeScript",
      repositorySummary: "sample",
      candidateTurn: 5
    })).toThrow();
  });

  it("renders the five-beat incident page from executable analysis data", () => {
    const page = renderIncidentPage(createDemoAnalysis("REPLAY"));
    expect(page).toContain("BREAK");
    expect(page).toContain("FIND");
    expect(page).toContain("PROVE");
    expect(page).toContain("FIX");
    expect(page).toContain("PREVENT");
    expect(page).toContain("Re-run all evidence");
  });
});
