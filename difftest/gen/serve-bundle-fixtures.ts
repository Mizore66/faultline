// Builds the coherent serve-bundle evidence fixture with the frozen TS pipeline.
// First capture only: the caller passes an ABSENT absolute root (the capture
// uses .../inputs/coherent) and must pin Git date/config in its own isolated
// process first, as gen-bases.ts does. Docker facts are synthetic: the bundle
// promotes recorded facts via nativeDockerFixture and minimization runs against
// the scripted fake Docker. This is fixture evidence, never real Docker acceptance.
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join, resolve, delimiter } from "node:path";
import { digestJson, sha256 } from "../../src/canonical.js";
import { investigateGitRange, GitInvestigationResultSchema } from "../../src/git-investigation.js";
import { writeGitInvestigationProofBundle, verifyGitInvestigationProofBundle } from "../../src/git-proof-bundle.js";
import { minimizeGitDiff, verifyGitMinimizationResultFile, writeGitMinimizationResult } from "../../src/git-minimization.js";
import { createRepairEvidencePacket } from "../../src/repair-brief.js";
import { writeRepairBriefArtifact, verifyRepairBriefArtifact } from "../../src/repair-brief-store.js";
import { buildPreventionProofFromVerifiedArtifacts } from "../../src/prevention-from-artifacts.js";
import { writePreventionProof, verifyPreventionProof } from "../../src/prevention-proof.js";
import { loadVerifiedGitProofView } from "../../src/git-proof-view.js";
import { createRepository, createFrozenWitness, deterministicDockerRunner, nativeDockerFixture, pinnedImage } from "./fixtures.js";

export async function buildServeBundleFixtures(root: string): Promise<void> {
  root = resolve(root);
  // Fail before any write (including scratch) if prior output exists.
  assert.equal(existsSync(root), false, "first-capture output already exists");
  const scratch = mkdtempSync(join(tmpdir(), "faultline-serve-fixture-"));
  const fakeDocker = resolve("difftest/testdata/fakedocker/pass-fail");
  const repo = createRepository();
  const frozen = createFrozenWitness(join(scratch, "witness-lock"), [
    { path: "private-sentinel.txt", text: "FL_PRIVATE_OVERLAY_5C_NOT_FOR_HTML\n" }
  ]);
  assert.notEqual(frozen.frozenDigest, frozen.witnessDigest);
  const observed = await investigateGitRange({
    repository: repo.root, range: { ancestor: repo.ancestor, descendant: repo.descendant },
    frozenWitness: frozen, expectedFrozenDigest: frozen.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage }, runner: deterministicDockerRunner()
  });
  assert.equal(observed.proof.isProof, false);
  const investigation = nativeDockerFixture(observed);
  const introduction = investigation.transitions.find((t) => t.kind === "PASS_TO_FAIL");
  const recovery = investigation.transitions.find((t) => t.kind === "FAIL_TO_PASS");
  assert.ok(introduction); assert.ok(recovery);
  mkdirSync(root, { recursive: true });
  const bundle = writeGitInvestigationProofBundle(join(root, "bundle"), investigation, frozen, {
    proofRoot: root, generatedAt: "2026-07-16T11:03:00.000Z"
  });
  const base = verifyGitInvestigationProofBundle(bundle.directory);
  assert.equal(base.valid, true); assert.ok(base.rootDigest);
  const request = {
    repository: repo.root, before: introduction.before.commit, after: introduction.after.commit,
    frozenWitness: frozen, expectedFrozenDigest: frozen.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED" as const, image: pinnedImage }, budget: { maxExecutions: 40 }
  };
  const oldPath = process.env.PATH;
  process.env.PATH = fakeDocker + delimiter + (oldPath ?? "");
  try {
    const certified = await minimizeGitDiff(request);
    assert.equal(certified.proof.isProof, true);
    await writeGitMinimizationResult(join(root, "minimization.json"), certified);
    const uncertified = await minimizeGitDiff({ ...request, runner: deterministicDockerRunner() });
    assert.equal(uncertified.proof.isProof, false);
    await writeGitMinimizationResult(join(root, "minimization-uncertified.json"), uncertified);
  } finally {
    if (oldPath === undefined) delete process.env.PATH; else process.env.PATH = oldPath;
  }
  const parsed = GitInvestigationResultSchema.parse(JSON.parse(readFileSync(join(bundle.directory, "investigation.json"), "utf8")));
  const packet = createRepairEvidencePacket(parsed);
  const cited = (statement: string, evidenceIds: string[]) => ({ statement, evidenceIds });
  const brief = {
    schemaVersion: "faultline.repair-brief.v1", evidencePacketDigest: packet.packetDigest,
    proposedInvariant: cited("FL_PRIVATE_INVARIANT_5C_NOT_FOR_HTML", ["E1", "E1", "E2"]),
    repairDirections: [cited("FL_PRIVATE_REPAIR_5C_NOT_FOR_HTML", ["E2", "E3"])],
    prevention: {
      hardEnforcement: [cited("FL_PRIVATE_HARD_5C_NOT_FOR_HTML", ["E1"])],
      softGuidance: [cited("FL_PRIVATE_SOFT_5C_NOT_FOR_HTML", ["E2"])]
    },
    uncertainties: ["FL_PRIVATE_DIAGNOSTIC_5C_NOT_FOR_HTML"]
  };
  const oldCwd = process.cwd();
  process.chdir(root);
  try {
    const repair = writeRepairBriefArtifact(join(root, ".faultline/repair-briefs/attached"), packet, brief, { kind: "OFFLINE_INPUT" });
    assert.equal(verifyRepairBriefArtifact(repair.directory).valid, true);
    const environmentDigest = parsed.environment.distinctDigests[0]; assert.ok(environmentDigest);
    const repaired = recovery.after;
    const runs = repaired.runIds.map((runId, i) => ({
      runId, executionId: repaired.executionIds[i]!, commit: repaired.commit, tree: repaired.tree,
      verdict: "PASS" as const, witnessDigest: frozen.witnessDigest, environmentDigest,
      executionTrust: "NATIVE_DOCKER" as const, executionKind: "EXECUTED" as const
    }));
    // Raw Buffer (including any trailing LF) is hashed, never a trimmed string.
    const repairPatch = execFileSync("git", ["-C", repo.root, "diff", "--binary", "--no-ext-diff", "--no-textconv",
      introduction.after.commit, repaired.commit, "--"], { maxBuffer: 4 * 1024 * 1024 });
    const built = buildPreventionProofFromVerifiedArtifacts({
      bundleDirectory: bundle.directory, expectRoot: base.rootDigest,
      repaired: { commit: repaired.commit, tree: repaired.tree, runs },
      repairPatchDigest: `sha256:${sha256(repairPatch)}`
    });
    if (!built.ok) throw new Error(built.reasons.join("; "));
    assert.equal(built.ok, true);
    const verified = writePreventionProof(join(root, ".faultline/prevention-proofs/verified"), built.input);
    // Summary packages carry no grounded run bindings; omit the key entirely.
    const { repairedRunBindings: _bindings, ...summaryInput } = built.input;
    const summary = writePreventionProof(join(root, ".faultline/prevention-proofs/summary"), {
      ...summaryInput, grounding: "SUMMARY"
    });
    const { investigationDigest: _omitted, ...withoutInvestigation } = summaryInput;
    writePreventionProof(join(root, ".faultline/prevention-proofs/no-investigation"), {
      ...withoutInvestigation, grounding: "SUMMARY"
    });
    assert.equal(verifyPreventionProof(verified.directory).valid, true);
    assert.equal(verifyPreventionProof(summary.directory).valid, true);
    const minimization = verifyGitMinimizationResultFile(join(root, "minimization.json"));
    assert.equal(minimization.valid, true); assert.ok(minimization.resultDigest);
    const view = loadVerifiedGitProofView(bundle.directory, base.rootDigest, {
      minimizationFile: join(root, "minimization.json"), expectedMinimizationDigest: minimization.resultDigest,
      repairDirectory: repair.directory, expectedRepairDigest: repair.manifest.manifestDigest,
      preventionDirectory: verified.directory, expectedPreventionDigest: verified.manifest.rootDigest
    });
    assert.equal(view.attachments.minimization?.result.proof.isProof, true);
    assert.equal(view.attachments.prevention?.manifest.classification, "PREVENTION_VERIFIED");
    assert.equal(view.attachments.repair?.packet.investigationDigest, digestJson(parsed));
  } finally { process.chdir(oldCwd); }
}
