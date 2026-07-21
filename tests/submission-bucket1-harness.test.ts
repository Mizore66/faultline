import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const root = process.cwd();

describe("submission bucket-1 harnesses", () => {
  it("tracks SUBMISSION_FROZEN policy (present after pin cut, briefly absent during Critical-fix pin PR)", () => {
    const freezePath = join(root, "SUBMISSION_FROZEN");
    if (existsSync(freezePath)) {
      const text = readFileSync(freezePath, "utf8");
      expect(text).toMatch(/v0\.1\.10-buildweek/);
      expect(text).toMatch(/SUBMISSION_FROZEN|freeze/i);
    } else {
      // Allowed only while pin surfaces for v0.1.10 land (src/cli-help retarget).
      expect(readFileSync(join(root, "scripts/package-smoke.mjs"), "utf8")).toMatch(
        /v0\.1\.10-buildweek/
      );
    }
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
