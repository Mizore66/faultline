import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const packageJson = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")) as {
  private: boolean;
  bin: Record<string, string>;
  files: string[];
  scripts: Record<string, string>;
};

describe("package distribution contract", () => {
  it("ships the compiled CLI while keeping publication an explicit owner decision", () => {
    expect(packageJson.private).toBe(true);
    expect(packageJson.bin.fl).toBe("./dist/cli.js");
    expect(packageJson.files).toContain("dist");
    expect(packageJson.files).toContain("README.md");
    expect(packageJson.files).toContain("docs/faultline-self-incident.md");
    expect(packageJson.scripts.prepack).toBe("pnpm build");
    expect(packageJson.scripts["pack:check"]).toBe("pnpm pack --dry-run");
  });
});
