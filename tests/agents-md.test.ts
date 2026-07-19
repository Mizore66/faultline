import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  AGENTS_MD_BEGIN,
  AGENTS_MD_END,
  upsertFaultLineAgentsMd
} from "../src/agents-md.js";

const digests = {
  originalProofRoot: `sha256:${"a".repeat(64)}`,
  frozenWitnessDigest: `sha256:${"b".repeat(64)}`,
  preventionRootDigest: `sha256:${"c".repeat(64)}`,
  lastGoodRunIds: [`sha256:${"d".repeat(64)}`],
  firstBadRunIds: [`sha256:${"e".repeat(64)}`],
  repairedRunIds: [`sha256:${"f".repeat(64)}`]
};

describe("AGENTS.md materialization (CDX-06)", () => {
  it("creates and idempotently replaces the marked FaultLine block", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-agents-md-"));
    try {
      const first = upsertFaultLineAgentsMd({
        repository: root,
        classification: "PREVENTION_VERIFIED",
        ...digests
      });
      expect(first.status).toBe("CREATED");
      const text = readFileSync(first.path, "utf8");
      expect(text).toContain(AGENTS_MD_BEGIN);
      expect(text).toContain(AGENTS_MD_END);
      expect(text).toContain(digests.preventionRootDigest);

      const second = upsertFaultLineAgentsMd({
        repository: root,
        classification: "PREVENTION_VERIFIED",
        ...digests
      });
      expect(second.status).toBe("UNCHANGED");

      const third = upsertFaultLineAgentsMd({
        repository: root,
        classification: "PREVENTION_VERIFIED",
        ...digests,
        preventionRootDigest: `sha256:${"1".repeat(64)}`
      });
      expect(third.status).toBe("UPDATED");
      const updated = readFileSync(third.path, "utf8");
      expect(updated).toContain(`sha256:${"1".repeat(64)}`);
      expect(updated.indexOf(AGENTS_MD_BEGIN)).toBe(updated.lastIndexOf(AGENTS_MD_BEGIN));
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses non-digest payload fields", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-agents-md-bad-"));
    try {
      expect(() => upsertFaultLineAgentsMd({
        repository: root,
        classification: "PREVENTION_VERIFIED",
        ...digests,
        frozenWitnessDigest: "not-a-digest"
      })).toThrow(/non-digest/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("preserves surrounding AGENTS.md content outside the marker block", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-agents-md-wrap-"));
    try {
      writeFileSync(join(root, "AGENTS.md"), "# Project\n\nKeep me.\n", "utf8");
      upsertFaultLineAgentsMd({
        repository: root,
        classification: "PREVENTION_VERIFIED",
        ...digests
      });
      const text = readFileSync(join(root, "AGENTS.md"), "utf8");
      expect(text).toContain("Keep me.");
      expect(text).toContain(AGENTS_MD_BEGIN);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
