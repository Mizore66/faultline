import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  readIncidentSessionBinding,
  resolveInheritedSessionFacts,
  writeIncidentSessionBinding
} from "../src/incident-binding.js";
import { defaultIncidentDraftStore } from "../src/incident-store.js";

const DIGEST = `sha256:${"c".repeat(64)}`;
const DIGEST_B = `sha256:${"d".repeat(64)}`;

describe("incident session binding", () => {
  it("stores mutable companion facts without requiring a draft mutation", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-binding-"));
    try {
      const store = defaultIncidentDraftStore(root);
      const written = writeIncidentSessionBinding(store, {
        incidentId: "demo-incident",
        expectDigest: DIGEST,
        ledgerPath: join(root, "ledger.json")
      });
      expect(written.expectDigest).toBe(DIGEST);
      expect(readIncidentSessionBinding(store, "demo-incident")?.expectDigest).toBe(DIGEST);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("inherits binding facts and lets explicit CLI values win", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-binding-inherit-"));
    try {
      const store = defaultIncidentDraftStore(root);
      writeIncidentSessionBinding(store, {
        incidentId: "demo-incident",
        expectDigest: DIGEST,
        ledgerPath: join(root, "ledger-a.json")
      });
      const inherited = resolveInheritedSessionFacts({
        storeDirectory: store,
        incidentId: "demo-incident"
      });
      expect(inherited.expectDigest).toBe(DIGEST);
      expect(inherited.inheritedExpectDigest).toBe(true);

      const overridden = resolveInheritedSessionFacts({
        storeDirectory: store,
        incidentId: "demo-incident",
        expectDigest: DIGEST_B,
        ledgerPath: join(root, "ledger-b.json")
      });
      expect(overridden.expectDigest).toBe(DIGEST_B);
      expect(overridden.inheritedExpectDigest).toBe(false);
      expect(overridden.inheritedLedgerPath).toBe(false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
