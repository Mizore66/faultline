import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { makeBlindIncidentPacket } from "../src/ai.js";
import { sha256 } from "../src/canonical.js";
import { createDemoAnalysis } from "../src/engine.js";
import { verifyProofBundle, writeProofBundle } from "../src/proof-bundle.js";
import { renderIncidentPage } from "../src/ui.js";

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
  });

  it("covers every cited run and requires an external root for tamper detection", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-proof-"));
    try {
      const written = writeProofBundle(directory, createDemoAnalysis("REPLAY"));
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
      rmSync(directory, { recursive: true, force: true });
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
