import { describe, expect, it } from "vitest";
import {
  classifySubmissionFreezePaths,
  isSubmissionFrozenBlockedPath,
  shouldEnforceSubmissionFreeze
} from "../scripts/submission-freeze-guard.mjs";

describe("submission freeze guard (C-1)", () => {
  it("blocks src/**, package.json, and pnpm-lock.yaml", () => {
    expect(isSubmissionFrozenBlockedPath("src/turn-snapshot.ts")).toBe(true);
    expect(isSubmissionFrozenBlockedPath("src/cli-app.ts")).toBe(true);
    expect(isSubmissionFrozenBlockedPath("package.json")).toBe(true);
    expect(isSubmissionFrozenBlockedPath("pnpm-lock.yaml")).toBe(true);
  });

  it("allows docs, tests, workflows, and plugin scaffolds", () => {
    expect(isSubmissionFrozenBlockedPath("docs/README.md")).toBe(false);
    expect(isSubmissionFrozenBlockedPath("tests/turn-snapshot.test.ts")).toBe(false);
    expect(isSubmissionFrozenBlockedPath(".github/workflows/verify.yml")).toBe(false);
    expect(isSubmissionFrozenBlockedPath("plugin/README.md")).toBe(false);
    expect(isSubmissionFrozenBlockedPath("README.md")).toBe(false);
  });

  it("classifies mixed diffs", () => {
    const result = classifySubmissionFreezePaths([
      "docs/foo.md",
      "src/bar.ts",
      "tests/baz.test.ts",
      "package.json"
    ]);
    expect(result.blocked.sort()).toEqual(["package.json", "src/bar.ts"]);
    expect(result.allowed.sort()).toEqual(["docs/foo.md", "tests/baz.test.ts"]);
  });

  it("enforces only when freeze file exists (unless unfreeze override)", () => {
    expect(shouldEnforceSubmissionFreeze({
      freezeFileExists: false,
      eventName: "push",
      ref: "refs/heads/main",
      unfreezeOverride: false
    })).toBe(false);
    expect(shouldEnforceSubmissionFreeze({
      freezeFileExists: true,
      eventName: "push",
      ref: "refs/heads/main",
      unfreezeOverride: false
    })).toBe(true);
    expect(shouldEnforceSubmissionFreeze({
      freezeFileExists: true,
      eventName: "workflow_dispatch",
      ref: "refs/heads/main",
      unfreezeOverride: true
    })).toBe(false);
    expect(shouldEnforceSubmissionFreeze({
      freezeFileExists: true,
      eventName: "pull_request",
      ref: "refs/pull/1/head",
      unfreezeOverride: false
    })).toBe(true);
  });
});
