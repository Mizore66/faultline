import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { renderJudgePreviewPage, writeJudgePreview } from "../src/judge-preview.js";

describe("deterministic static judge preview", () => {
  it("is self-contained, stable, and explicitly non-live", () => {
    const first = renderJudgePreviewPage();
    const second = renderJudgePreviewPage();

    expect(first).toBe(second);
    expect(first).toMatch(/deterministic judge preview/i);
    expect(first).toMatch(/not a live Docker proof/i);
    expect(first).toContain("It did not run Docker, execute repository code, call an API, or verify a live repository.");
    expect(first).toContain("FaultLine product guarantees");
    expect(first).toContain("approval + freeze");
    expect(first).toContain("runs per Git state");
    expect(first).not.toMatch(/sample sessions|sample turns|changed files|implicated hunks/i);
    expect(first).toContain("No code executes when this file is opened.");
    expect(first).not.toMatch(/<script\b/i);
    expect(first).not.toMatch(/\bfetch\s*\(/i);
    expect(first).not.toContain("/api/");
    expect(first).not.toMatch(/rerun/i);
    expect(first).not.toMatch(/<button\b/i);
    expect(first).not.toMatch(/https?:\/\//i);
  });

  it("writes the same static page on repeated export", () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-judge-preview-"));
    const output = join(root, "nested", "judge-preview.html");
    try {
      const first = writeJudgePreview(output);
      const firstPage = readFileSync(output, "utf8");
      const second = writeJudgePreview(output);

      expect(first.path).toBe(output);
      expect(second.path).toBe(output);
      expect(first.bytes).toBe(second.bytes);
      expect(firstPage).toBe(renderJudgePreviewPage());
      expect(readFileSync(output, "utf8")).toBe(firstPage);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
