import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const action = readFileSync(new URL("../action.yml", import.meta.url), "utf8");

describe("FaultLine CI intake action", () => {
  it("builds the pinned action and preserves an opaque, review-only command", () => {
    expect(action).toContain("pnpm install --frozen-lockfile");
    expect(action).toContain("pnpm build");
    expect(action).toContain("version: 10.32.1");
    expect(action).toContain("${{ steps.intake.outputs.doctor-report }}");
    expect(action).toContain("${{ steps.intake.outputs.proposal-id }}");
    expect(action).toContain('args=(incident start --repo "$GITHUB_WORKSPACE" --command "$FL_COMMAND")');
    expect(action).toContain('node "$GITHUB_ACTION_PATH/dist/cli.js" "${args[@]}"');
    expect(action).toContain("FaultLine action requires from and to together.");
    expect(action).not.toContain('eval "$FL_COMMAND"');
    expect(action).not.toContain('docker pull');
    expect(action).not.toContain("witness approve");
    expect(action).not.toContain("witness freeze");
  });
});
