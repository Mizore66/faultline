import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const files = [
  new URL("../.github/workflows/verify.yml", import.meta.url),
  new URL("../.github/workflows/action-smoke.yml", import.meta.url),
  new URL("../actions/proof/action.yml", import.meta.url)
];

describe("GitHub Actions supply-chain pins", () => {
  it("pins every third-party action to a full-length commit SHA", () => {
    for (const file of files) {
      const text = readFileSync(file, "utf8");
      const uses = [...text.matchAll(/^\s*-?\s*uses:\s*([^\s#]+)/gm)].map((match) => match[1]!);
      expect(uses.length).toBeGreaterThan(0);
      for (const action of uses) {
        if (action.startsWith("./")) continue;
        expect(action, `${file.pathname} -> ${action}`).toMatch(
          /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+@[0-9a-f]{40}$/
        );
      }
      expect(text).not.toMatch(/uses:\s+[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+@v\d+/);
    }
  });

  it("includes a non-blocking pnpm audit --prod step in verify.yml", () => {
    const verify = readFileSync(new URL("../.github/workflows/verify.yml", import.meta.url), "utf8");
    expect(verify).toContain("pnpm audit --prod");
    expect(verify).toContain("continue-on-error: true");
  });
});
