import { mkdirSync, mkdtempSync, rmSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
<<<<<<< Updated upstream
import {
  relativeTrustedSystemPath,
  resolveSafeDirectorySegment,
  resolveTrustedSystemPath
} from "../src/safe-directory.js";
=======
import { resolveSafeDirectorySegment } from "../src/safe-directory.js";
>>>>>>> Stashed changes

describe("safe directory segments", () => {
  const itOnDarwin = process.platform === "darwin" ? it : it.skip;

  itOnDarwin("accepts only the verified macOS system temporary-directory aliases", () => {
    expect(resolveSafeDirectorySegment("/var")).toBe("/private/var");
    expect(resolveSafeDirectorySegment("/tmp")).toBe("/private/tmp");
<<<<<<< Updated upstream
    expect(resolveTrustedSystemPath("/var/folders/faultline-cli")).toBe("/private/var/folders/faultline-cli");
    expect(relativeTrustedSystemPath(
      "/private/var/folders/faultline-cli/.faultline/bundles",
      "/var/folders/faultline-cli/.faultline/bundles/receipt-demo"
    )).toBe("receipt-demo");
=======
>>>>>>> Stashed changes
  });

  it("rejects a user-controlled directory symlink", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-safe-directory-"));
    const outside = join(root, "outside");
    const linked = join(root, "linked");
    try {
      mkdirSync(outside);
      try {
        symlinkSync(outside, linked, process.platform === "win32" ? "junction" : "dir");
      } catch (error) {
        const code = error && typeof error === "object" && "code" in error ? (error as { code?: unknown }).code : undefined;
        if (code === "EPERM" || code === "EACCES") return;
        throw error;
      }
      expect(resolveSafeDirectorySegment(linked)).toBeNull();
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
