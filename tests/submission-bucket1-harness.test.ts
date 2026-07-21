import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { join } from "node:path";

const root = process.cwd();

describe("submission bucket-1 harnesses", () => {
  it("keeps SUBMISSION_FROZEN at repo root", () => {
    expect(existsSync(join(root, "SUBMISSION_FROZEN"))).toBe(true);
  });

  it("lint-devpost-claims exits 0 on current paste surfaces", () => {
    const result = spawnSync("node", ["scripts/lint-devpost-claims.mjs"], {
      cwd: root,
      encoding: "utf8"
    });
    expect(result.status, result.stderr + result.stdout).toBe(0);
    expect(result.stdout).toMatch(/PASS/);
  });

  it("submission-freeze-guard classifies path buckets", () => {
    const result = spawnSync(
      "node",
      [
        "scripts/submission-freeze-guard.mjs",
        "--classify",
        "docs/a.md",
        "tests/b.test.ts",
        "src/cli.ts",
        "package.json"
      ],
      { cwd: root, encoding: "utf8" }
    );
    expect(result.status).toBe(2);
    const parsed = JSON.parse(result.stdout);
    expect(parsed.blocked).toEqual(expect.arrayContaining(["src/cli.ts", "package.json"]));
    expect(parsed.allowed).toEqual(expect.arrayContaining(["docs/a.md", "tests/b.test.ts"]));
  });
});
