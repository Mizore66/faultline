import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  ensureFaultLineConfig,
  faultLineConfigPath,
  readFaultLineConfig,
  resolveConfiguredImage,
  updateFaultLineConfigImage
} from "../src/config.js";

const DIGEST_IMAGE = `node@sha256:${"a".repeat(64)}`;

describe("faultline config", () => {
  it("seeds tracked+untracked snapshot defaults on ensure", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-config-"));
    try {
      const written = ensureFaultLineConfig({ repository: root, runtimeAlias: "node" });
      expect(written.status).toBe("CREATED");
      expect(written.config.snapshot.trackedFilesOnly).toBe(false);
      expect(readFaultLineConfig(root)?.snapshot.trackedFilesOnly).toBe(false);
      expect(JSON.parse(readFileSync(faultLineConfigPath(root), "utf8")).schemaVersion)
        .toBe("faultline.config.v1");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("lets CLI --image override persisted config image", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-config-image-"));
    try {
      updateFaultLineConfigImage({ repository: root, image: DIGEST_IMAGE, runtimeAlias: "node" });
      expect(resolveConfiguredImage(root, undefined)).toBe(DIGEST_IMAGE);
      const override = `python@sha256:${"b".repeat(64)}`;
      expect(resolveConfiguredImage(root, override)).toBe(override);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
