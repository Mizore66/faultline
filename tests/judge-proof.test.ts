import { describe, expect, it } from "vitest";
import { existsSync } from "node:fs";
import { join } from "node:path";
import {
  JUDGE_COMMIT_PROOF_SAMPLE_ROOT,
  RECORDED_SELF_INCIDENT_ROOT,
  assertSelfIncidentSamplePresent,
  defaultSelfIncidentSampleDirectory,
  loadSelfIncidentProofView
} from "../src/judge-proof.js";

describe("judge-proof sample path", () => {
  it("keeps the historical self-incident root distinct from the judge sample root", () => {
    expect(RECORDED_SELF_INCIDENT_ROOT).toBe(
      "sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75"
    );
    expect(JUDGE_COMMIT_PROOF_SAMPLE_ROOT).toBe(
      "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9"
    );
    expect(JUDGE_COMMIT_PROOF_SAMPLE_ROOT).not.toBe(RECORDED_SELF_INCIDENT_ROOT);
  });

  it("loads the installed COMMIT_PROOF sample when present", () => {
    const directory = defaultSelfIncidentSampleDirectory();
    if (!existsSync(join(directory, "manifest.json"))) {
      expect(() => assertSelfIncidentSamplePresent(directory)).toThrow(/Missing COMMIT_PROOF sample/);
      return;
    }
    const view = loadSelfIncidentProofView({ directory });
    expect(view.rootDigest).toBe(JUDGE_COMMIT_PROOF_SAMPLE_ROOT);
    expect(view.externalRootStatus).toBe("MATCH");
  });
});
