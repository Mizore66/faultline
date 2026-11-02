// Phase A pipeline test for the coherent serve-bundle fixture builder. It runs
// the real frozen TS pipeline (real Git, POSIX fake Docker) and so belongs on
// the pinned Linux oracle. The builder's scripted-Docker facts are synthetic
// fixture evidence, never real Docker acceptance.
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { before } from "node:test";
import { digestJson } from "../../src/canonical.js";
import { GitInvestigationResultSchema } from "../../src/git-investigation.js";
import { verifyGitInvestigationProofBundle } from "../../src/git-proof-bundle.js";
import { verifyGitMinimizationResultFile } from "../../src/git-minimization.js";
import { loadVerifiedGitProofView } from "../../src/git-proof-view.js";
import { verifyPreventionProof } from "../../src/prevention-proof.js";
import { verifyRepairBriefArtifact } from "../../src/repair-brief-store.js";
import { buildServeBundleFixtures } from "./serve-bundle-fixtures.js";

let root = "";

before(async () => {
  // Pin Git exactly as gen-bases.ts does, in this isolated test process only.
  const work = mkdtempSync(join(tmpdir(), "faultline-serve-fixture-test-"));
  writeFileSync(join(work, "empty.gitconfig"), "");
  Object.assign(process.env, {
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: join(work, "empty.gitconfig"),
    GIT_AUTHOR_DATE: "2026-07-16T10:00:00Z",
    GIT_COMMITTER_DATE: "2026-07-16T10:00:00Z",
    GIT_DEFAULT_HASH: "sha1"
  });
  root = join(work, "coherent");
  await buildServeBundleFixtures(root);
});

const bundle = () => join(root, "bundle");
const prevention = (name: string) => join(root, ".faultline", "prevention-proofs", name);
const repairDir = () => join(root, ".faultline", "repair-briefs", "attached");

test("base bundle verifies with a root and records a real PASS_TO_FAIL then FAIL_TO_PASS investigation", () => {
  // Catches a fixture whose bundle is invalid or lacks the transitions the cases bind to.
  const base = verifyGitInvestigationProofBundle(bundle());
  assert.equal(base.valid, true);
  assert.match(base.rootDigest ?? "", /^sha256:[0-9a-f]{64}$/);
  const investigation = GitInvestigationResultSchema.parse(JSON.parse(readFileSync(join(bundle(), "investigation.json"), "utf8")));
  assert.deepEqual(investigation.transitions.map((t) => t.kind), ["PASS_TO_FAIL", "FAIL_TO_PASS"]);
});

test("frozen witness has distinct frozenDigest and witnessDigest (asymmetric repair/prevention binding stays testable)", () => {
  // Catches a fixture where the two digests coincide, which would hide a swapped binding.
  const frozen = JSON.parse(readFileSync(join(bundle(), "witness", "frozen.json"), "utf8")) as { frozenDigest: string; witnessDigest: string };
  assert.match(frozen.frozenDigest, /^sha256:[0-9a-f]{64}$/);
  assert.match(frozen.witnessDigest, /^sha256:[0-9a-f]{64}$/);
  assert.notEqual(frozen.frozenDigest, frozen.witnessDigest);
});

test("certified and uncertified minimization files both verify, with opposite proof flags", () => {
  const certified = verifyGitMinimizationResultFile(join(root, "minimization.json"));
  const uncertified = verifyGitMinimizationResultFile(join(root, "minimization-uncertified.json"));
  assert.equal(certified.valid, true);
  assert.equal(uncertified.valid, true);
  assert.equal(certified.result?.proof.isProof, true);
  assert.equal(uncertified.result?.proof.isProof, false);
  assert.notEqual(certified.resultDigest, uncertified.resultDigest);
});

test("repair artifact verifies and prevention packages cover verified, summary and no-investigation", () => {
  assert.equal(verifyRepairBriefArtifact(repairDir()).valid, true);
  for (const name of ["verified", "summary", "no-investigation"]) {
    assert.equal(verifyPreventionProof(prevention(name)).valid, true, name);
  }
});

test("actual frozen loader accepts the coherent set and sees all three attachments bound to one investigation", () => {
  const base = verifyGitInvestigationProofBundle(bundle());
  assert.ok(base.rootDigest);
  const min = verifyGitMinimizationResultFile(join(root, "minimization.json"));
  const repair = verifyRepairBriefArtifact(repairDir());
  const verified = verifyPreventionProof(prevention("verified"));
  const summary = verifyPreventionProof(prevention("summary"));
  assert.ok(min.resultDigest && repair.manifest && verified.manifest && summary.manifest);
  const investigation = GitInvestigationResultSchema.parse(JSON.parse(readFileSync(join(bundle(), "investigation.json"), "utf8")));
  const originalCwd = process.cwd();
  process.chdir(root);
  try {
    const view = loadVerifiedGitProofView(bundle(), base.rootDigest, {
      minimizationFile: join(root, "minimization.json"), expectedMinimizationDigest: min.resultDigest,
      repairDirectory: repairDir(), expectedRepairDigest: repair.manifest.manifestDigest,
      preventionDirectory: prevention("verified"), expectedPreventionDigest: verified.manifest.rootDigest
    });
    assert.equal(view.attachments.minimization?.result.proof.isProof, true);
    assert.equal(view.attachments.prevention?.manifest.classification, "PREVENTION_VERIFIED");
    assert.equal(view.attachments.repair?.packet.investigationDigest, digestJson(investigation));
    const summaryView = loadVerifiedGitProofView(bundle(), base.rootDigest, {
      preventionDirectory: prevention("summary"), expectedPreventionDigest: summary.manifest.rootDigest
    });
    assert.equal(summaryView.attachments.prevention?.manifest.classification, "PREVENTION_EVIDENCE_SUMMARY");
  } finally { process.chdir(originalCwd); }
});

test("the builder refuses an existing output root without modifying it", async () => {
  // Catches a first-capture builder that overwrites or merges into prior evidence.
  const existing = mkdtempSync(join(tmpdir(), "faultline-serve-fixture-existing-"));
  const marker = join(existing, "marker.txt");
  writeFileSync(marker, "keep\n");
  await assert.rejects(buildServeBundleFixtures(existing), /first-capture output already exists/);
  assert.equal(readFileSync(marker, "utf8"), "keep\n");
  assert.equal(existsSync(join(existing, "bundle")), false);
});

test("the builder does not leak the PATH override or cwd change into the process", () => {
  assert.ok(!(process.env.PATH ?? "").includes("fakedocker"));
  assert.notEqual(process.cwd(), root);
});
