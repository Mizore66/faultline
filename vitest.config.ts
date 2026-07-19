import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["tests/**/*.test.ts"],
    environment: "node",
    // Real-Git evidence tests create and remove several detached worktrees.
    // Under concurrent Windows file-system load they can exceed 15 seconds
    // even though their focused execution is much faster. This is a test
    // budget only; the product's Docker runner still enforces its own limits.
    testTimeout: 120_000,
    hookTimeout: 120_000,
    teardownTimeout: 120_000,
    // Heavy suites call spawnSync/execFileSync for real Git. That blocks the
    // worker thread and can starve Vitest's birpc heartbeat (default 60s),
    // producing `Timeout calling "onTaskUpdate"` after every assertion already
    // passed. Keep verification serial and single-worker so the coordinator
    // stays responsive and CI stays honestly green.
    fileParallelism: false,
    pool: "forks",
    maxWorkers: 1,
    minWorkers: 1
  }
});
