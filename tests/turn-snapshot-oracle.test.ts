import { chmodSync, existsSync, lstatSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import fc from "fast-check";
import { describe, expect, it } from "vitest";
import { captureTurnTreeSnapshot } from "../src/turn-snapshot.js";
import { oracleFullStageTreeDigest } from "./helpers/turn-snapshot-oracle.js";

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function repositoryFixture(): string {
  const repository = mkdtempSync(join(tmpdir(), "faultline-turn-oracle-"));
  git(repository, ["init"]);
  git(repository, ["config", "user.email", "oracle@faultline.test"]);
  git(repository, ["config", "user.name", "FaultLine Oracle"]);
  git(repository, ["config", "core.autocrlf", "false"]);
  writeFileSync(join(repository, "tracked.txt"), "stable\n", "utf8");
  writeFileSync(join(repository, "second.txt"), "second-stable\n", "utf8");
  git(repository, ["add", "."]);
  git(repository, ["commit", "-m", "oracle fixture"]);
  return repository;
}

type Op =
  | { readonly kind: "edit"; readonly path: "tracked.txt" | "second.txt"; readonly tag: number }
  | { readonly kind: "revert"; readonly path: "tracked.txt" | "second.txt" }
  | { readonly kind: "add-untracked"; readonly tag: number }
  | { readonly kind: "delete"; readonly path: "tracked.txt" | "second.txt" | "untracked.txt" }
  | { readonly kind: "toggle-exec"; readonly path: "tracked.txt" | "second.txt" };

const headContents: Record<"tracked.txt" | "second.txt", string> = {
  "tracked.txt": "stable\n",
  "second.txt": "second-stable\n"
};

function applyOp(repository: string, op: Op): void {
  switch (op.kind) {
    case "edit":
      writeFileSync(join(repository, op.path), `edited-${op.tag}\n`, "utf8");
      return;
    case "revert":
      writeFileSync(join(repository, op.path), headContents[op.path], "utf8");
      try {
        chmodSync(join(repository, op.path), 0o644);
      } catch {
        // best-effort
      }
      return;
    case "add-untracked":
      writeFileSync(join(repository, "untracked.txt"), `untracked-${op.tag}\n`, "utf8");
      return;
    case "delete":
      rmSync(join(repository, op.path), { force: true });
      return;
    case "toggle-exec":
      try {
        const mode = lstatSync(join(repository, op.path)).mode;
        chmodSync(join(repository, op.path), (mode & 0o111) !== 0 ? 0o644 : 0o755);
      } catch {
        // file may be deleted
      }
      return;
  }
}

const opArbitrary: fc.Arbitrary<Op> = fc.oneof(
  fc.record({
    kind: fc.constant("edit" as const),
    path: fc.constantFrom("tracked.txt" as const, "second.txt" as const),
    tag: fc.integer({ min: 1, max: 1_000_000 })
  }),
  fc.record({
    kind: fc.constant("revert" as const),
    path: fc.constantFrom("tracked.txt" as const, "second.txt" as const)
  }),
  fc.record({
    kind: fc.constant("add-untracked" as const),
    tag: fc.integer({ min: 1, max: 1_000_000 })
  }),
  fc.record({
    kind: fc.constant("delete" as const),
    path: fc.constantFrom("tracked.txt" as const, "second.txt" as const, "untracked.txt" as const)
  }),
  fc.record({
    kind: fc.constant("toggle-exec" as const),
    path: fc.constantFrom("tracked.txt" as const, "second.txt" as const)
  })
);

describe("turn-snapshot oracle equivalence", () => {
  // ~5–7s/case on Windows; scale timeout with FAULTLINE_ORACLE_RUNS (default 200).
  const numRuns = Number(process.env.FAULTLINE_ORACLE_RUNS ?? "200");
  const timeoutMs = Math.max(3_600_000, Math.ceil(numRuns * 12_000) + 300_000);

  it("matches from-scratch staging across random edit/revert sequences", () => {
    expect(Number.isInteger(numRuns) && numRuns >= 1).toBe(true);

    fc.assert(
      fc.property(fc.array(opArbitrary, { minLength: 3, maxLength: 8 }), (ops) => {
        const repository = repositoryFixture();
        const sessionCachePath = join(repository, ".faultline", "oracle-cache.json");
        try {
          for (const op of ops) {
            applyOp(repository, op);
            if (!existsSync(join(repository, "tracked.txt")) && !existsSync(join(repository, "second.txt"))) {
              writeFileSync(join(repository, "tracked.txt"), headContents["tracked.txt"], "utf8");
            }
            const incremental = captureTurnTreeSnapshot(repository, {
              sessionCachePath,
              sleep: () => {},
              quiescenceDelayMs: 0
            }).snapshot.treeDigest;
            const oracle = oracleFullStageTreeDigest(repository);
            expect(incremental).toBe(oracle);
          }
        } finally {
          rmSync(repository, { recursive: true, force: true });
        }
      }),
      { numRuns }
    );
  }, timeoutMs);
});
