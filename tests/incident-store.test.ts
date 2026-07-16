import { existsSync, mkdirSync, mkdtempSync, rmSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { createIncidentDraft } from "../src/incident.js";
import {
  defaultIncidentDraftStore,
  readIncidentDraft,
  writeIncidentDraft
} from "../src/incident-store.js";

function draft(id = "first-incident") {
  return createIncidentDraft({
    draftId: id,
    createdAt: "2026-07-16T14:00:00.000Z",
    repository: "C:\\work\\example",
    command: "pnpm test -- --runInBand",
    localHead: { head: "b".repeat(40), parents: ["a".repeat(40)] }
  });
}

describe("incident draft store", () => {
  it("writes canonical, write-once drafts and reads the verified state", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-incident-store-"));
    try {
      const store = defaultIncidentDraftStore(root);
      const written = writeIncidentDraft(store, draft());
      expect(written.path).toContain(join("incidents", "drafts", "first-incident.json"));
      expect(existsSync(written.path)).toBe(true);
      expect(readIncidentDraft(store, "first-incident").draft).toEqual(draft());
      expect(() => writeIncidentDraft(store, draft())).toThrow(/already exists/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("rejects a changed in-memory digest before it writes anything", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-incident-store-tamper-"));
    try {
      const store = defaultIncidentDraftStore(root);
      const altered = { ...draft(), draftDigest: `sha256:${"f".repeat(64)}` };
      expect(() => writeIncidentDraft(store, altered)).toThrow(/invalid incident draft/);
      expect(existsSync(join(store, "drafts", "first-incident.json"))).toBe(false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses a store reached through a symlinked parent", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-incident-store-link-"));
    const outside = join(root, "outside");
    const linkedFaultLine = join(root, ".faultline");
    try {
      // Windows junctions keep this regression test portable without requiring
      // developer-mode symbolic-link privileges.
      mkdirSync(outside);
      symlinkSync(outside, linkedFaultLine, process.platform === "win32" ? "junction" : "dir");
      expect(() => writeIncidentDraft(join(linkedFaultLine, "incidents"), draft())).toThrow(/non-symlink directory/);
      expect(existsSync(join(outside, "incidents", "drafts", "first-incident.json"))).toBe(false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
