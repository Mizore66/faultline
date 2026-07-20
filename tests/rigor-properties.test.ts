import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import fc from "fast-check";
import { describe, expect, it } from "vitest";
import { digestJson, sha256 } from "../src/canonical.js";
import { splitModifyUnitIntoHunks, type PatchUnitInternal } from "../src/git-minimization.js";
import {
  appendLifecycleEvent,
  createCodexLifecycleLedger,
  verifyCodexLifecycleLedger,
  type CodexLifecycleLedger
} from "../src/ledger.js";
import { verifyProofBundle } from "../src/proof-bundle.js";
import { captureTurnTreeSnapshot } from "../src/turn-snapshot.js";

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function repositoryFixture(): string {
  const repository = mkdtempSync(join(tmpdir(), "faultline-rigor-properties-"));
  git(repository, ["init"]);
  git(repository, ["config", "user.email", "faultline@example.invalid"]);
  git(repository, ["config", "user.name", "FaultLine property test"]);
  git(repository, ["config", "core.autocrlf", "false"]);
  writeFileSync(join(repository, "tracked.txt"), "stable\n", "utf8");
  git(repository, ["add", "tracked.txt"]);
  git(repository, ["commit", "-m", "fixture"]);
  return repository;
}

function hunkRepositoryFixture(): string {
  const repository = repositoryFixture();
  const base = Array.from({ length: 30 }, (_, index) => `line-${index + 1}`).join("\n") + "\n";
  writeFileSync(join(repository, "tracked.txt"), base, "utf8");
  git(repository, ["add", "tracked.txt"]);
  git(repository, ["commit", "-m", "two hunk base"]);
  return repository;
}

function lifecycleLedger(): CodexLifecycleLedger {
  let ledger = createCodexLifecycleLedger({
    ledgerId: "property-ledger",
    sessionId: "property-session",
    createdAt: "2026-07-20T00:00:00.000Z"
  });
  const append = (input: Parameters<typeof appendLifecycleEvent>[1], index: number) => {
    ledger = appendLifecycleEvent(ledger, input, {
      eventId: `event-${index}`,
      occurredAt: `2026-07-20T00:00:0${index}.000Z`
    });
  };
  append({
    type: "SESSION_STARTED",
    payload: { transport: "CODEX_CLI", workingDirectory: "C:/work/faultline" }
  }, 1);
  append({
    type: "TURN_STARTED",
    payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: `sha256:${"a".repeat(64)}` }
  }, 2);
  append({
    type: "TURN_COMPLETED",
    payload: { turnId: "turn-1", turnOrdinal: 1, outcome: "COMPLETED", outputDigest: `sha256:${"b".repeat(64)}` }
  }, 3);
  append({ type: "SESSION_ENDED", payload: { reason: "COMPLETED", completedTurns: 1 } }, 4);
  return ledger;
}

function twoHunkPatchUnit(repository: string, left: number, right: number): { unit: PatchUnitInternal; expected: Buffer } {
  const path = "tracked.txt";
  const base = Array.from({ length: 30 }, (_, index) => `line-${index + 1}`).join("\n") + "\n";
  const expected = base
    .replace("line-2", `left-${left}`)
    .replace("line-28", `right-${right}`);
  writeFileSync(join(repository, path), expected, "utf8");
  const bytes = Buffer.from(git(repository, ["diff", "--no-ext-diff", "--binary", "--", path]) + "\n", "utf8");
  const pathBytes = Buffer.from(path, "utf8");
  const patchDigest = `sha256:${sha256(bytes)}`;
  const pathDigest = `sha256:${sha256(pathBytes)}`;
  return {
    unit: {
      id: digestJson({ kind: "property-test-unit", pathDigest, patchDigest }),
      ordinal: 0,
      changeKind: "MODIFY",
      path,
      pathBytesBase64: pathBytes.toString("base64"),
      pathDigest,
      patchDigest,
      patchBytes: bytes.length,
      binarySafe: true,
      bytes
    },
    expected: Buffer.from(expected, "utf8")
  };
}

describe("RIG-05 generative integrity properties", () => {
  it("rejects every sampled raw reordering of a signed lifecycle hash chain", () => {
    const ledger = lifecycleLedger();
    expect(verifyCodexLifecycleLedger(ledger).valid).toBe(true);

    fc.assert(fc.property(
      fc.integer({ min: 0, max: ledger.events.length - 1 }),
      fc.integer({ min: 0, max: ledger.events.length - 1 }),
      (left, right) => {
        fc.pre(left !== right);
        const reordered = structuredClone(ledger);
        [reordered.events[left], reordered.events[right]] = [reordered.events[right]!, reordered.events[left]!];
        expect(verifyCodexLifecycleLedger(reordered).valid).toBe(false);
      }
    ), { numRuns: 32 });
  });

  it("does not reuse a session-cached snapshot for sampled distinct dirty content", () => {
    const repository = repositoryFixture();
    const sessionCachePath = join(repository, ".faultline", "property-cache.json");
    try {
      fc.assert(fc.property(
        fc.uniqueArray(fc.integer({ min: 1, max: 2_000_000_000 }), { minLength: 2, maxLength: 2 }),
        ([first, second]) => {
          writeFileSync(join(repository, "tracked.txt"), `property-${first}\n`, "utf8");
          const before = captureTurnTreeSnapshot(repository, {
            sessionCachePath,
            quiescenceDelayMs: 0,
            sleep: () => {}
          }).snapshot;
          writeFileSync(join(repository, "tracked.txt"), `property-${second}\n`, "utf8");
          const after = captureTurnTreeSnapshot(repository, {
            sessionCachePath,
            quiescenceDelayMs: 0,
            sleep: () => {}
          }).snapshot;
          expect(after.treeDigest).not.toBe(before.treeDigest);
        }
      ), { numRuns: 12 });
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });

  it("split-and-reapply preserves bytes for sampled independent text hunks", () => {
    const repository = hunkRepositoryFixture();
    try {
      fc.assert(fc.property(
        fc.uniqueArray(fc.integer({ min: 1, max: 2_000_000_000 }), { minLength: 2, maxLength: 2 }),
        ([left, right]) => {
          const { unit, expected } = twoHunkPatchUnit(repository, left, right);
          const hunks = splitModifyUnitIntoHunks(unit);
          expect(hunks).toHaveLength(2);
          git(repository, ["checkout", "--", "tracked.txt"]);
          for (const hunk of hunks ?? []) {
            execFileSync("git", ["-C", repository, "apply", "--whitespace=nowarn", "-"], { input: hunk.bytes });
          }
          expect(readFileSync(join(repository, "tracked.txt"))).toEqual(expected);
        }
      ), { numRuns: 8 });
    } finally {
      rmSync(repository, { recursive: true, force: true });
    }
  });
});

describe("SEC-01 hostile offline verifier corpus", () => {
  it("fails closed without throwing for sampled malformed metadata and adversarial declared paths", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-hostile-bundle-"));
    const paths = ["../outside", "/absolute", "C:/outside", "nested//gap", "dir\\escape", "safe/missing"];
    try {
      fc.assert(fc.property(
        fc.string({ maxLength: 160 }),
        fc.constantFrom(...paths),
        (manifest, declaredPath) => {
          const hashes = `${"0".repeat(64)}  ${declaredPath}\n`;
          writeFileSync(join(directory, "hashes.txt"), hashes, "utf8");
          writeFileSync(join(directory, "ROOT.sha256"), `sha256:${sha256(hashes)}\n`, "utf8");
          writeFileSync(join(directory, "manifest.json"), manifest, "utf8");
          writeFileSync(join(directory, "analysis.json"), "{ malformed", "utf8");
          const verification = verifyProofBundle(directory, `sha256:${"f".repeat(64)}`);
          expect(verification.valid).toBe(false);
          expect(verification.errors.length).toBeGreaterThan(0);
        }
      ), { numRuns: 48 });
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});
