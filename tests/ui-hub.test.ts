import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  DEFAULT_UI_HUB_PORT,
  UI_HUB_EMPTY_NEXT_COMMAND,
  discoverFaultLineArtifacts,
  startFaultLineUiHub
} from "../src/ui-hub.js";

describe("faultline ui hub", () => {
  it("defaults to the same port as fl serve", () => {
    expect(DEFAULT_UI_HUB_PORT).toBe(4173);
  });

  it("discovers local drafts and ledgers under .faultline", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-ui-"));
    try {
      const drafts = join(root, ".faultline", "incidents", "drafts");
      const recordings = join(root, ".faultline", "recordings");
      mkdirSync(drafts, { recursive: true });
      mkdirSync(recordings, { recursive: true });
      writeFileSync(join(drafts, "demo.json"), "{}\n", "utf8");
      writeFileSync(join(recordings, "ledger-demo.json"), "{}\n", "utf8");
      const artifacts = discoverFaultLineArtifacts(root);
      expect(artifacts.some((item) => item.kind === "incident-draft" && item.id === "demo")).toBe(true);
      expect(artifacts.some((item) => item.kind === "ledger" && item.id === "ledger-demo")).toBe(true);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("serves a local hub index on 127.0.0.1", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-ui-serve-"));
    try {
      mkdirSync(join(root, ".faultline"), { recursive: true });
      const hub = await startFaultLineUiHub({ repository: root, port: 0 });
      try {
        expect(hub.url).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/);
        const response = await fetch(hub.url);
        expect(response.status).toBe(200);
        const html = await response.text();
        expect(html).toContain("FaultLine UI");
        expect(html).toContain("empty-state");
        expect(html).toContain(UI_HUB_EMPTY_NEXT_COMMAND);
        expect(UI_HUB_EMPTY_NEXT_COMMAND).toBe("fl quickstart");
      } finally {
        await hub.close();
      }
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
