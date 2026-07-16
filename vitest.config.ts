import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["tests/**/*.test.ts"],
    environment: "node",
    // Real-Git evidence tests create and remove several detached worktrees.
    // Under concurrent Windows file-system load they can exceed 15 seconds
    // even though their focused execution is much faster. This is a test
    // budget only; the product's Docker runner still enforces its own limits.
    testTimeout: 60_000,
    // The proof tests exercise real Git worktrees and cleanup. Running every
    // file concurrently can leave Vitest's Windows worker RPC without a
    // heartbeat even after all assertions pass, which CI reports as a false
    // failure. Keep the verification order deterministic and favor an honest
    // green result over a marginally faster test wall time.
    fileParallelism: false
  }
});
