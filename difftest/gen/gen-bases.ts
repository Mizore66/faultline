// Writes difftest/testdata/bases/*. Run from the repo root:
//   pnpm exec tsx difftest/gen/gen-bases.ts [base-id ...]
// With ids, only those bases are rewritten. Git runs isolated from user and
// system config, with fixed commit dates and SHA-1 by default. The committed
// bases are snapshots: bundles record temporary paths and the Node version,
// so a full rerun rewrites them (and the goldens must be regenerated).
// Run it on Linux: git-cjk-overlays writes overlay paths containing U+FDD1,
// which APFS rejects (EILSEQ). On macOS a full run stops with "fixture did
// not create a decisive state" at that base, after rewriting the git bases
// before it and none of the ones after, leaving the tree half regenerated;
// there, pass the ids of the bases to rewrite, leaving out git-cjk-overlays.
import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import type { DemoAnalysis } from "../../src/domain.js";
import { createDemoAnalysis } from "../../src/engine.js";
import { investigateGitRange } from "../../src/git-investigation.js";
import { writeGitInvestigationProofBundle } from "../../src/git-proof-bundle.js";
import { defaultPreventionProofRoot, writePreventionProof } from "../../src/prevention-proof.js";
import { writeProofBundle } from "../../src/proof-bundle.js";
import { writeCodexLifecycleLedgerAtomic } from "../../src/ledger.js";
import { investigateTurnTrees } from "../../src/turn-investigation.js";
import { writeTurnInvestigationProofBundle } from "../../src/turn-proof-bundle.js";
import {
  createFrozenWitness, createRepository, createTurnFrozenWitness, createTurnRepository, type ExtraOverlay,
  deterministicDockerRunner, groundedPreventionInput, lifecycleBoundToDescendant, lifecycleBoundToEveryInvestigatedState,
  git, nativeDockerFixture, nativeTurnFixture, pinnedImage, sampleInput, turnLedger
} from "./fixtures.js";

const basesDir = resolve("difftest/testdata/bases");
const work = mkdtempSync(join(tmpdir(), "faultline-difftest-bases-"));
writeFileSync(join(work, "empty.gitconfig"), "");
Object.assign(process.env, {
  GIT_CONFIG_NOSYSTEM: "1",
  GIT_CONFIG_GLOBAL: join(work, "empty.gitconfig"),
  GIT_AUTHOR_DATE: "2026-07-16T10:00:00Z",
  GIT_COMMITTER_DATE: "2026-07-16T10:00:00Z",
  GIT_DEFAULT_HASH: "sha1"
});
const only = new Set(process.argv.slice(2));
const wanted = (id: string) => only.size === 0 || only.has(id);

function place(id: string, directory: string): void {
  const target = join(basesDir, id);
  rmSync(target, { recursive: true, force: true });
  cpSync(directory, target, { recursive: true });
  console.log(`wrote ${id}`);
}

async function gitBase(
  id: string, ancestor: string, objectFormat: "sha1" | "sha256", lifecycle?: "descendant" | "every", extraOverlays: ExtraOverlay[] = []
): Promise<void> {
  if (!wanted(id)) return;
  const root = mkdtempSync(join(work, `${id}-`));
  const repository = createRepository(objectFormat);
  const frozen = createFrozenWitness(join(root, "witness-lock"), extraOverlays);
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

// A turn proof base: one commit per state, a closed session whose baseline
// is the first state, and the investigation promoted to native Docker with
// every clock read fixed (nativeTurnFixture), so a rerun writes the same
// bytes; only source/trees.pack follows git and zlib (plan correction 8).
// ancestorOnly, when set, also writes a pack of only the first state's tree
// objects to difftest/testdata/fixtures (the turn-pack-wrong-trees case),
// built the way packTurnTrees builds the bundle's pack.
async function turnBase(id: string, states: readonly string[], ancestorOnly?: string): Promise<void> {
  if (!wanted(id)) return;
  const root = mkdtempSync(join(work, `${id}-`));
  const repository = createTurnRepository(states);
  const ledger = turnLedger({
    states: repository.trees.map((tree, index) => ({ ordinal: index, tree, dirty: false, commit: repository.commits[index] })),
    baseline: true
  });
  const ledgerPath = join(root, "ledger.json");
  writeCodexLifecycleLedgerAtomic(ledgerPath, ledger);
  const frozen = createTurnFrozenWitness(join(root, "witness-lock"));
  const observed = await investigateTurnTrees({
    repository: repository.root,
    ledgerPath,
    frozenWitness: frozen,
    expectedFrozenDigest: frozen.frozenDigest,
    image: pinnedImage,
    runner: deterministicDockerRunner()
  });
  if (observed.status !== "COMPLETED" || observed.proof.isProof) {
    throw new Error(`${id}: the injected-runner investigation must complete without proof (${observed.status})`);
  }
  const result = nativeTurnFixture(observed);
  const written = await writeTurnInvestigationProofBundle(join(root, "proofs", id), result, frozen, {
    proofRoot: join(root, "proofs"),
    generatedAt: "2026-07-16T11:04:00.000Z",
    lifecycleLedger: ledger,
    repository: repository.root
  });
  if (ancestorOnly !== undefined) {
    const objects = git(repository.root, ["rev-list", "--objects", repository.trees[0]!]).split("\n")
      .map((line) => line.trim().split(/\s+/)[0]).filter(Boolean).sort((l, r) => l!.localeCompare(r!));
    const pack = execFileSync("git", ["-C", repository.root, "pack-objects", "--stdout", "--compression=9"], { input: `${objects.join("\n")}\n` });
    writeFileSync(resolve("difftest/testdata/fixtures", ancestorOnly), pack);
    console.log(`wrote fixtures/${ancestorOnly}`);
  }
  rmSync(repository.root, { recursive: true, force: true });
  place(id, written.directory);
}

function demoBase(id: string, mode: "REPLAY" | "RERUN", edit?: (analysis: DemoAnalysis) => void): void {
  if (!wanted(id)) return;
  const root = mkdtempSync(join(work, `${id}-`));
  const analysis = createDemoAnalysis(mode);
  edit?.(analysis);
  const bundle = writeProofBundle(join(root, "bundles", id), analysis, { proofRoot: join(root, "bundles") });
  place(id, bundle.directory);
}

function preventionBase(id: string, input: Parameters<typeof writePreventionProof>[1]): void {
  if (!wanted(id)) return;
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

if (wanted("git-sample-self-incident")) {
  cpSync(resolve("docs/samples/self-incident-commit-proof"), join(basesDir, "git-sample-self-incident"), { recursive: true });
}
await gitBase("git-unbound", "HEAD~2", "sha1");
await gitBase("git-partially-bound", "HEAD~2", "sha1", "descendant");
await gitBase("git-fully-bound", "HEAD~2", "sha1", "every");
await gitBase("git-two-states", "HEAD~1", "sha1");
// Non-Latin overlay paths: frozen TS orders them with localeCompare (ICU root
// collation: radical-stroke Han, emoji and Ext B before later code points).
await gitBase("git-cjk-overlays", "HEAD~2", "sha1", undefined, [
  { path: "说明/视图.md", text: "view\n" },
  { path: "说明/神经.md", text: "nerve\n" },
  { path: "🤔.md", text: "thinking\n" },
  { path: "𠀀.txt", text: "ext-b\n" },
  { path: "中.txt", text: "middle\n" },
  // ICU matches contractions on FCD text and skips identical prefixes:
  // Gurung Khema, Tulu-Tigalari and FDD1 sequences exercise both.
  { path: "\u{1611E}\u{16121}.md", text: "gurung khema\n" },
  { path: "\u{16121}.md", text: "gurung khema u\n" },
  { path: "\u{113C2}\u{113C5}.md", text: "tulu-tigalari\n" },
  { path: "\uFDD1\u5B57.md", text: "fdd1 han\n" },
  { path: "\uFDD1\u1FAF.md", text: "fdd1 omega\n" },
  // ICU's FCD iterator leaves a supplementary mark run after a contraction
  // starter (и) un-normalized; the order of these two depends on it.
  { path: "\u0438\u{1E00A}\u0F72.md", text: "cyrillic tibetan\n" },
  { path: "\u0438\u{1E00A}\u1B3E.md", text: "cyrillic balinese\n" },
  // Final sigma: ICU skips Case_Ignorable before testing Cased, so ʰΣ
  // lowercases to ʰσ and these are not duplicate overlay paths.
  { path: "a/\u02B0\u03A3", text: "sigma\n" },
  { path: "a/\u02B0\u03C2", text: "final sigma\n" }
]);
// SHA-256 object format. The verifier's temporary repository is a plain
// `git init --bare`, which follows GIT_DEFAULT_HASH; goldens and replay run
// this base with the environment in difftest/testdata/base-env.json.
process.env.GIT_DEFAULT_HASH = "sha256";
await gitBase("git-sha256", "HEAD~2", "sha256");
process.env.GIT_DEFAULT_HASH = "sha1";
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
await turnBase("turn-two-states", ["good", "bad"], "turn-ancestor-only.pack");
await turnBase("turn-non-monotonic", ["good", "bad", "good"]);
rmSync(work, { recursive: true, force: true });
