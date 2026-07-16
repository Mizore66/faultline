import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { digestJson } from "../src/canonical.js";
import {
  createGithubProvenanceReceiptFromView,
  githubActionsIdentityFromEnvironment,
  verifyGithubProvenanceBinding,
  verifySignedGithubProvenance,
  type GithubArtifactAttestationTrust,
  type GithubProvenanceReceipt
} from "../src/github-provenance.js";
import type { VerifiedGitProofView } from "../src/git-proof-view.js";
import { approveWitnessProposal, freezeApprovedWitness, proposeWitness } from "../src/witness-lock.js";

const loadVerifiedGitProofView = vi.hoisted(() => vi.fn());

vi.mock("../src/git-proof-view.js", async (importOriginal) => {
  const original = await importOriginal<typeof import("../src/git-proof-view.js")>();
  return { ...original, loadVerifiedGitProofView };
});

const digest = (seed: string) => `sha256:${seed.repeat(64).slice(0, 64)}`;
const commit = (seed: string) => seed.repeat(40).slice(0, 40);

function frozenWitness(store: string) {
  const proposal = proposeWitness(store, {
    proposalId: "provenance-witness",
    proposedAt: "2026-07-16T12:00:00.000Z",
    incidentPacket: {
      symptom: "CI found a regression.",
      ciLog: "witness failed",
      repositoryLanguage: "TypeScript",
      repositorySummary: "GitHub provenance test fixture."
    },
    witness: {
      behavior: "The fixture remains safe.",
      command: "node witness.mjs",
      overlays: [{ path: "witness.mjs", bytesBase64: Buffer.from("process.exit(0)\n", "utf8").toString("base64") }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 10 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, { approvedBy: "reviewer@example.test", approvedAt: "2026-07-16T12:01:00.000Z" });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T12:02:00.000Z" });
}

function viewFor(store: string): VerifiedGitProofView {
  const frozen = frozenWitness(store);
  const ancestor = { index: 0, commit: commit("a"), tree: commit("b") };
  const descendant = { index: 1, commit: commit("c"), tree: commit("d") };
  const run = {
    runId: digest("1"),
    executionId: digest("2"),
    startedAt: "2026-07-16T12:03:00.000Z",
    finishedAt: "2026-07-16T12:03:01.000Z",
    result: { executor: "NATIVE_DOCKER" },
    sandbox: {
      kind: "DOCKER_ISOLATED",
      runtime: { image: `registry.example/faultline@sha256:${"f".repeat(64)}` },
      policyDigest: digest("3"),
      environmentPolicyDigest: digest("4")
    }
  };
  const investigationDigest = digest("5");
  return {
    rootDigest: digest("6"),
    checkedFiles: 9,
    externalRootStatus: "NOT_PROVIDED",
    manifest: {
      investigationDigest,
      resolvedRange: { ancestor, descendant }
    },
    investigation: { runs: [run] },
    frozenWitness: frozen
  } as unknown as VerifiedGitProofView;
}

function ciIdentity() {
  return {
    provider: "GITHUB_ACTIONS",
    repository: "Mizore66/faultline",
    workflowRef: "Mizore66/faultline/.github/workflows/verify.yml@refs/heads/main",
    ref: "refs/heads/main",
    commit: commit("e"),
    tree: commit("f"),
    runId: "123456789",
    runAttempt: 1,
    eventName: "push"
  } as const;
}

function trust(): GithubArtifactAttestationTrust {
  return {
    schemaVersion: "faultline.github-artifact-attestation-trust.v1",
    repository: "Mizore66/faultline",
    signerWorkflow: "Mizore66/faultline/.github/workflows/verify.yml",
    sourceRef: "refs/heads/main",
    eventName: "push",
    denySelfHostedRunners: true,
    trustedRootFile: "trusted-root.jsonl"
  };
}

function rehash(receipt: GithubProvenanceReceipt): GithubProvenanceReceipt {
  const { receiptDigest: _receiptDigest, ...payload } = receipt;
  return { ...payload, receiptDigest: digestJson(payload) };
}

describe("GitHub artifact-attested FaultLine provenance", () => {
  it("derives and verifies all proof facts from a verified view before an external attestation is trusted", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-github-provenance-"));
    try {
      const view = viewFor(store);
      const receipt = createGithubProvenanceReceiptFromView(view, ciIdentity(), "2026-07-16T12:04:00.000Z");
      expect(receipt.proof).toMatchObject({
        rootDigest: view.rootDigest,
        investigationDigest: view.manifest.investigationDigest,
        frozenDigest: view.frozenWitness.frozenDigest,
        witnessDigest: view.frozenWitness.witnessDigest,
        approvalDigest: view.frozenWitness.approval.approvalDigest,
        range: view.manifest.resolvedRange,
        execution: { runIds: [digest("1")], executionIds: [digest("2")], executionCount: 1 }
      });
      loadVerifiedGitProofView.mockReturnValue(view);
      expect(verifyGithubProvenanceBinding("unused-bundle", receipt, trust())).toMatchObject({ valid: true, proofRootDigest: view.rootDigest });

      const tampered = rehash({
        ...receipt,
        proof: { ...receipt.proof, frozenDigest: digest("a") }
      });
      expect(verifyGithubProvenanceBinding("unused-bundle", tampered, trust())).toMatchObject({ valid: false });
      expect(verifyGithubProvenanceBinding("unused-bundle", tampered, trust()).errors.join("\n")).toMatch(/proof facts/);
    } finally {
      loadVerifiedGitProofView.mockReset();
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("requires an external GitHub artifact-attestation verification before claiming signed provenance", () => {
    const store = mkdtempSync(join(tmpdir(), "faultline-github-provenance-signed-"));
    try {
      const view = viewFor(join(store, "witnesses"));
      const receipt = createGithubProvenanceReceiptFromView(view, ciIdentity(), "2026-07-16T12:04:00.000Z");
      const receiptFile = join(store, "ci-receipt.json");
      writeFileSync(receiptFile, JSON.stringify(receipt), "utf8");
      loadVerifiedGitProofView.mockReturnValue(view);
      const result = verifySignedGithubProvenance({
        bundleDirectory: "unused-bundle",
        receiptFile,
        attestationBundleFile: join(store, "sigstore-bundle.json"),
        trust: trust()
      }, (signedReceipt, bundle, configuredTrust, sourceCommit) => {
        expect(signedReceipt).toBe(receiptFile);
        expect(bundle).toContain("sigstore-bundle.json");
        expect(configuredTrust).toEqual(trust());
        expect(sourceCommit).toBe(ciIdentity().commit);
        return { valid: true, errors: [], command: ["gh", "attestation", "verify"] };
      });
      expect(result).toMatchObject({ valid: true, assurance: "GITHUB_ARTIFACT_ATTESTATION_VERIFIED" });
    } finally {
      loadVerifiedGitProofView.mockReset();
      rmSync(store, { recursive: true, force: true });
    }
  });

  it("refuses to create a GitHub CI receipt from local process metadata", () => {
    expect(() => githubActionsIdentityFromEnvironment(process.cwd(), { GITHUB_ACTIONS: "false" })).toThrow(/only be created inside GitHub Actions/);
  });
});
