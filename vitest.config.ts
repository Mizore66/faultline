import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["tests/**/*.test.ts"],
    environment: "node",
    // Real-Git evidence tests create and remove several detached worktrees.
    // Under concurrent Windows file-system load they can exceed 15 seconds
    // even though their focused execution is much faster. This is a test
    // budget only; the product's Docker runner still enforces its own limits.
    testTimeout: 60_000
  }
});
