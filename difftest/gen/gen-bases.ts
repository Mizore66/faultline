// Writes difftest/testdata/bases/*. Run from the repo root:
//   pnpm exec tsx difftest/gen/gen-bases.ts
import { cpSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import type { DemoAnalysis } from "../../src/domain.js";
import { createDemoAnalysis } from "../../src/engine.js";
import { investigateGitRange } from "../../src/git-investigation.js";
import { writeGitInvestigationProofBundle } from "../../src/git-proof-bundle.js";
import { defaultPreventionProofRoot, writePreventionProof } from "../../src/prevention-proof.js";
import { writeProofBundle } from "../../src/proof-bundle.js";
import {
  createFrozenWitness, createRepository, deterministicDockerRunner, groundedPreventionInput,
  lifecycleBoundToDescendant, lifecycleBoundToEveryInvestigatedState, nativeDockerFixture, pinnedImage, sampleInput
} from "./fixtures.js";

const basesDir = resolve("difftest/testdata/bases");
const work = mkdtempSync(join(tmpdir(), "faultline-difftest-bases-"));

function place(id: string, directory: string): void {
  const target = join(basesDir, id);
  rmSync(target, { recursive: true, force: true });
  cpSync(directory, target, { recursive: true });
  console.log(`wrote ${id}`);
}

async function gitBase(id: string, ancestor: string, objectFormat: "sha1" | "sha256", lifecycle?: "descendant" | "every"): Promise<void> {
  const root = mkdtempSync(join(work, `${id}-`));
  const repository = createRepository(objectFormat);
  const frozen = createFrozenWitness(join(root, "witness-lock"));
  const observed = await investigateGitRange({
    repository: repository.root,
    range: { ancestor, descendant: "HEAD" },
    frozenWitness: frozen,
    expectedFrozenDigest: frozen.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
    runner: deterministicDockerRunner()
  });
  const result = nativeDockerFixture(observed);
  const lifecycleLedger = lifecycle === "descendant"
    ? lifecycleBoundToDescendant(repository.root)
    : lifecycle === "every"
      ? lifecycleBoundToEveryInvestigatedState(repository.root, result.states.map((state) => state.commit))
      : undefined;
  const written = writeGitInvestigationProofBundle(join(root, "proofs", id), result, frozen, {
    proofRoot: join(root, "proofs"),
    generatedAt: "2026-07-16T11:03:00.000Z",
    ...(lifecycleLedger === undefined ? {} : { lifecycleLedger })
  });
  place(id, written.directory);
}

function demoBase(id: string, mode: "REPLAY" | "RERUN", edit?: (analysis: DemoAnalysis) => void): void {
  const root = mkdtempSync(join(work, `${id}-`));
  const analysis = createDemoAnalysis(mode);
  edit?.(analysis);
  const bundle = writeProofBundle(join(root, "bundles", id), analysis, { proofRoot: join(root, "bundles") });
  place(id, bundle.directory);
}

function preventionBase(id: string, input: Parameters<typeof writePreventionProof>[1]): void {
  const root = mkdtempSync(join(work, `${id}-`));
  const previous = process.cwd();
  process.chdir(root);
  try {
    const written = writePreventionProof(join(defaultPreventionProofRoot(), id), input);
    place(id, written.directory);
  } finally {
    process.chdir(previous);
  }
}

cpSync(resolve("docs/samples/self-incident-commit-proof"), join(basesDir, "git-sample-self-incident"), { recursive: true });
await gitBase("git-unbound", "HEAD~2", "sha1");
await gitBase("git-partially-bound", "HEAD~2", "sha1", "descendant");
await gitBase("git-fully-bound", "HEAD~2", "sha1", "every");
await gitBase("git-two-states", "HEAD~1", "sha1");
await gitBase("git-sha256", "HEAD~2", "sha256");
demoBase("demo-replay", "REPLAY");
demoBase("demo-rerun", "RERUN");
demoBase("demo-rerun-unicode-stdout", "RERUN", (analysis) => {
  analysis.runCatalog[0]!.stdout = "héllo 😀\nsecond line\n";
});
demoBase("demo-rerun-lone-surrogate-stdout", "RERUN", (analysis) => {
  analysis.runCatalog[0]!.stdout = "bad \ud800 unit\n";
});
preventionBase("prevention-summary", sampleInput());
preventionBase("prevention-verified", groundedPreventionInput());
rmSync(work, { recursive: true, force: true });
