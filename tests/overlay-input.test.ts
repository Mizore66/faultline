import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { readModelOverlayInput } from "../src/overlay-input.js";

describe("model overlay staging input", () => {
  it("reads a regular nested overlay but rejects a regular leaf reached through a symlinked parent", () => {
    const temporary = mkdtempSync(join(tmpdir(), "faultline-overlay-input-"));
    try {
      const root = join(temporary, "staging");
      const regular = join(root, "nested");
      const outside = join(temporary, "outside");
      mkdirSync(regular, { recursive: true });
      mkdirSync(outside, { recursive: true });
      writeFileSync(join(regular, "witness.mjs"), "process.exit(0)\n", "utf8");
      writeFileSync(join(outside, "secret.mjs"), "process.exit(1)\n", "utf8");

      expect(readModelOverlayInput(root, ["nested/witness.mjs"])).toEqual([
        { path: "nested/witness.mjs", bytesBase64: Buffer.from("process.exit(0)\n", "utf8").toString("base64") }
      ]);

      symlinkSync(outside, join(root, "linked"), process.platform === "win32" ? "junction" : "dir");
      expect(() => readModelOverlayInput(root, ["linked/secret.mjs"])).toThrow(/cannot traverse a symbolic link/);
    } finally {
      rmSync(temporary, { recursive: true, force: true });
    }
  });
});
