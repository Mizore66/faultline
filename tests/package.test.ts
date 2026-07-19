import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const packageJson = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")) as {
  name: string;
  private?: boolean;
  publishConfig?: { access?: string };
  bin: Record<string, string>;
  files: string[];
  scripts: Record<string, string | undefined>;
  license?: string;
};

describe("package distribution contract", () => {
  it("publishes the compiled CLI under an available scoped npm name", () => {
    expect(packageJson.name).toBe("@mizore66/faultline");
    expect(packageJson.private).toBeUndefined();
    expect(packageJson.publishConfig?.access).toBe("public");
    expect(packageJson.license).toBe("MIT");
    expect(packageJson.bin.fl).toBe("./dist/cli.js");
    expect(packageJson.scripts.fl).toBe("node dist/cli.js");
    expect(packageJson.scripts["fl:dev"]).toBe("tsx src/cli.ts");
    expect(packageJson.scripts.prepare).toBe("pnpm run build");
    expect(packageJson.files).toContain("dist");
    expect(packageJson.files).toContain("LICENSE");
    expect(packageJson.files).toContain("README.md");
    expect(packageJson.files).toContain("docs/faultline-self-incident.md");
    expect(packageJson.scripts.prepack).toBe("pnpm build");
    expect(packageJson.scripts["pack:check"]).toBe("pnpm pack --dry-run");
    expect(packageJson.scripts["test:package"]).toBe("node scripts/package-smoke.mjs");
  });
});
