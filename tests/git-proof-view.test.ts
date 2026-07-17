import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import type { GitProofBundleManifest } from "../src/git-proof-bundle.js";
import { GitInvestigationResultSchema, type GitInvestigationResult } from "../src/git-investigation.js";
import { startGitProofServer } from "../src/server.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../src/witness-lock.js";

const verifyGitProofBundle = vi.hoisted(() => vi.fn());

vi.mock("../src/git-proof-bundle.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../src/git-proof-bundle.js")>();
  return { ...actual, verifyGitInvestigationProofBundle: verifyGitProofBundle };
});

const { loadVerifiedGitProofView, renderGitProofIncidentPage } = await import("../src/git-proof-view.js");

function digest(character: string): string {
  return `sha256:${character.repeat(64)}`;
}

function frozenWitness(store: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId: "read-only-view",
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T12:00:00.000Z",
    incidentPacket: {
      symptom: "The published price must remain nonnegative.",
      ciLog: "expected a nonnegative price",
      repositoryLanguage: "TypeScript",
      repositorySummary: "Synthetic read-only UI test fixture."
    },
    witness: {
      behavior: "A published price remains nonnegative.",
      command: "node witness.mjs",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from("private-overlay-bytes\n", "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: "2026-07-16T12:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T12:02:00.000Z" });
}

function syntheticInvestigation(frozen: FrozenWitness): GitInvestigationResult {
  const goodCommit = "1".repeat(40);
  const badCommit = "2".repeat(40);
  const goodTree = "3".repeat(40);
  const badTree = "4".repeat(40);
  const runFor = (stateIndex: number, attempt: number, verdict: "PASS" | "FAIL") => {
    const sequence = stateIndex * 3 + attempt;
    return {
      schemaVersion: "faultline.git-investigation.v1" as const,
      runId: digest(sequence.toString(16)),
      executionId: digest((sequence + 6).toString(16)),
      executionNonce: `00000000-0000-4000-8000-${String(sequence).padStart(12, "0")}`,
      stateIndex,
      executionAttempt: attempt,
      commit: stateIndex === 0 ? goodCommit : badCommit,
      tree: stateIndex === 0 ? goodTree : badTree,
      frozenDigest: frozen.frozenDigest,
      witnessDigest: frozen.witnessDigest,
      startedAt: "2026-07-16T12:03:00.000Z",
      finishedAt: "2026-07-16T12:03:01.000Z",
      durationMs: 1_000,
      overlays: [{ path: "witness.mjs", bytesDigest: digest("a"), bytesLength: 22 }],
      sandbox: {
        kind: "DOCKER_ISOLATED" as const,
        witnessDigest: frozen.frozenDigest,
        commandDigest: digest("b"),
        environmentPolicyDigest: digest("c"),
        policyDigest: digest("d"),
        environment: { fixedKeys: ["PATH"], allowedKeys: [], passed: [], redactedKeys: ["OPENAI_API_KEY"] },
        runtime: {
          image: `registry.example/faultline@sha256:${"e".repeat(64)}`,
          entrypoint: "/bin/sh",
          network: "none" as const,
          rootFilesystemReadOnly: true,
          user: "65534:65534",
          capDropAll: true,
          noNewPrivileges: true,
          pull: "never" as const,
          limits: {
            timeoutMs: 30_000,
            maxOutputBytes: 1_048_576,
            cpuCount: 1,
            memoryBytes: 536_870_912,
            pidsLimit: 64,
            tmpfsBytes: 67_108_864
          }
        }
      },
      result: {
        kind: "DOCKER_ISOLATED" as const,
        executor: "NATIVE_DOCKER" as const,
        verdict,
        reason: verdict === "PASS" ? "PREDICATE_PASS" as const : "PREDICATE_FAIL" as const,
        exitCode: verdict === "PASS" ? 0 : 1,
        signal: null,
        outputTruncated: false,
        stdoutDigest: digest("e"),
        stdoutBytes: 0,
        stdoutPreview: "",
        stdoutPreviewTruncated: false,
        stdoutRedacted: false,
        stderrDigest: digest("f"),
        stderrBytes: 0,
        stderrPreview: "",
        stderrPreviewTruncated: false,
        stderrRedacted: false
      }
    };
  };
  const runs = [
    ...[1, 2, 3].map((attempt) => runFor(0, attempt, "PASS")),
    ...[1, 2, 3].map((attempt) => runFor(1, attempt, "FAIL"))
  ];
  const stable = (stateIndex: number, verdict: "PASS" | "FAIL") => ({
    stateIndex,
    commit: stateIndex === 0 ? goodCommit : badCommit,
    tree: stateIndex === 0 ? goodTree : badTree,
    verdict,
    executionIds: runs.filter((run) => run.stateIndex === stateIndex).map((run) => run.executionId),
    runIds: runs.filter((run) => run.stateIndex === stateIndex).map((run) => run.runId)
  });
  const before = stable(0, "PASS");
  const after = stable(1, "FAIL");
  return GitInvestigationResultSchema.parse({
    schemaVersion: "faultline.git-investigation.v1",
    recorder: "git-commit-range-replay",
    nativeCodexInterception: false,
    status: "COMPLETED",
    repository: "/private/customer/repository",
    requestedRange: { ancestor: goodCommit, descendant: badCommit },
    resolvedRange: {
      ancestor: { index: 0, commit: goodCommit, tree: goodTree },
      descendant: { index: 1, commit: badCommit, tree: badTree }
    },
    witness: {
      valid: true,
      errors: [],
      frozenDigest: frozen.frozenDigest,
      witnessDigest: frozen.witnessDigest,
      externalDigestStatus: "MATCH",
      approval: { actor: "reviewer@example.test", approvedAt: frozen.approval.approvedAt }
    },
    executionsPerState: 3,
    states: [
      { index: 0, commit: goodCommit, tree: goodTree },
      { index: 1, commit: badCommit, tree: badTree }
    ],
    runs,
    stableStates: [before, after],
    transitions: [{ kind: "PASS_TO_FAIL", before, after }],
    nonMonotonic: false,
    environment: { homogeneity: "HOMOGENEOUS", fingerprints: [], distinctDigests: [] },
    proof: {
      requiresDockerIsolation: true,
      dockerIsolated: true,
      executionTrust: "NATIVE_DOCKER",
      proofTransitions: 1,
      isProof: true,
      reason: "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states.",
      evidenceGrade: "COMMIT_PROOF",
      evidenceLabel: "Commit-path localization — portable proof"
    },
    errors: []
  });
}

function syntheticManifest(result: GitInvestigationResult, frozen: FrozenWitness): GitProofBundleManifest {
  return {
    schemaVersion: "faultline.git-proof-bundle.v1",
    generatedAt: "2026-07-16T12:04:00.000Z",
    integrityScope: "complete-declared-file-set",
    investigationDigest: digest("a"),
    frozenDigest: frozen.frozenDigest,
    witnessDigest: frozen.witnessDigest,
    lifecycle: {
      status: "BOUND",
      path: "lifecycle/ledger.json",
      ledgerDigest: digest("b"),
      headHash: digest("c"),
      transport: "CODEX_APP",
      checkpointBindings: [{ sequence: 4, stateIndex: 1, checkpointDigest: digest("d") }]
    },
    resolvedRange: result.resolvedRange!,
    artifacts: {
      investigation: "investigation.json",
      frozenWitness: "witness/frozen.json",
      runs: result.runs.map((run) => ({ runId: run.runId, path: `runs/${run.runId.slice("sha256:".length)}.json` })),
      transitions: [{ index: 0, path: "transitions/0000.json" }],
      sourceMetadata: "source/metadata.json",
      gitBundle: "source/descendant.bundle",
      rangePatch: "source/range.patch",
      verification: "VERIFY.md",
      lifecycleLedger: "lifecycle/ledger.json"
    }
  };
}

describe("read-only Git proof view", () => {
  it("renders only a verified portable package and serves no rerun route", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-git-proof-view-"));
    const store = join(root, "witness-store");
    const directory = join(root, "bundle");
    try {
      const frozen = frozenWitness(store);
      const investigation = syntheticInvestigation(frozen);
      const manifest = syntheticManifest(investigation, frozen);
      const rootDigest = digest("f");
      mkdirSync(join(directory, "witness"), { recursive: true });
      writeFileSync(join(directory, "investigation.json"), JSON.stringify(investigation), "utf8");
      writeFileSync(join(directory, "witness", "frozen.json"), JSON.stringify(frozen), "utf8");
      verifyGitProofBundle.mockReturnValue({
        valid: true,
        checkedFiles: 17,
        errors: [],
        rootDigest,
        externalRootStatus: "MATCH",
        manifest
      });

      const view = loadVerifiedGitProofView(directory, rootDigest);
      expect(verifyGitProofBundle).toHaveBeenCalledTimes(3);
      expect(view).toMatchObject({
        rootDigest,
        checkedFiles: 17,
        externalRootStatus: "MATCH",
        manifest: { lifecycle: { status: "BOUND", transport: "CODEX_APP" } }
      });
      const page = renderGitProofIncidentPage(view);
      expect(page).toContain("Verified <em>evidence</em>");
      expect(page).toContain("BREAK");
      expect(page).toContain("FIND");
      expect(page).toContain("PROVE");
      expect(page).toContain("FIX");
      expect(page).toContain("PREVENT");
      expect(page).toContain("COMMIT_PROOF");
      expect(page).toContain("Commit-path localization — portable proof");
      expect(page).toContain("EXPERIMENTAL_TURN");
      expect(page).toContain("Turn localization — experimental evidence");
      expect(page).toContain("Turn-path contrast");
      expect(page).toContain("Stable Git states");
      expect(page).toContain("Stable transitions");
      expect(page).toContain("Recovery evidence");
      expect(page).not.toContain("Recovery and prevention");
      expect(page).toContain("Lifecycle binding");
      expect(page).toContain("LEGACY BOUND");
      expect(page).toContain("NATIVE DOCKER");
      expect(page).toContain("registry.example/faultline");
      expect(page).toContain("No minimization record was supplied to this read-only view.");
      expect(page).toContain("No repair artifact was supplied to this view.");
      expect(page).not.toContain("/private/customer/repository");
      expect(page).not.toContain("A published price remains nonnegative.");
      expect(page).not.toContain(Buffer.from("private-overlay-bytes\n", "utf8").toString("base64"));
      expect(page).not.toContain("Re-run all evidence");
      expect(page).not.toContain("/api/rerun");
      expect(page).not.toMatch(/<script\b/i);
      expect(page).not.toMatch(/<button\b/i);

      const attachedPage = renderGitProofIncidentPage({
        ...view,
        attachments: {
          minimization: {
            result: {
              proof: { isProof: true, reason: "Raw diagnostic from /private/customer/repository must not render." },
              candidateUnitIds: [digest("1"), digest("2")],
              minimality: { oneMinimal: true },
              certification: { sufficiency: { status: "CERTIFIED" }, necessity: { status: "CERTIFIED" } }
            } as never,
            resultDigest: digest("a"),
            externalDigestStatus: "MATCH"
          },
          repair: {
            manifest: { manifestDigest: digest("b") } as never,
            packet: {} as never,
            brief: {
              proposedInvariant: { statement: "Do not render /private/customer/repository or sk-proj-secret-value.", evidenceIds: ["E1"] },
              repairDirections: [{ statement: "node -e private command", evidenceIds: ["E2"] }],
              prevention: {
                hardEnforcement: [{ statement: "private source text", evidenceIds: ["E1"] }],
                softGuidance: []
              }
            } as never,
            externalDigestStatus: "MATCH"
          }
        }
      });
      expect(attachedPage).toContain("BIDIRECTIONALLY CERTIFIED");
      expect(attachedPage).toContain("Cited repair directions");
      expect(attachedPage).toContain("Validated citations");
      expect(attachedPage).toContain("Free-form guidance remains in the private repair artifact");
      expect(attachedPage).not.toContain("Raw diagnostic from");
      expect(attachedPage).not.toContain("node -e private command");
      expect(attachedPage).not.toContain("sk-proj-secret-value");

      const server = await startGitProofServer({ proof: view, port: 0 });
      try {
        const pageResponse = await fetch(server.url);
        expect(pageResponse.status).toBe(200);
        expect(pageResponse.headers.get("content-security-policy")).toContain("default-src 'none'");
        const pageText = await pageResponse.text();
        expect(pageText).toContain("COMMIT-PATH PORTABLE PROOF");
        expect(pageText).toContain("COMMIT_PROOF");
        expect(pageText).toContain("Commit-path localization — portable proof");
        expect(pageText).toContain("EXPERIMENTAL_TURN");
        expect(pageText).toContain("Turn localization — experimental evidence");
        expect(pageText).toContain("Turn-path contrast");
        expect((await fetch(`${server.url}/api/rerun`, { method: "POST" })).status).toBe(404);
        expect((await fetch(`${server.url}/api/analysis`)).status).toBe(404);
      } finally {
        await server.close();
      }
    } finally {
      verifyGitProofBundle.mockReset();
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("refuses to render when the verifier rejects the package", () => {
    verifyGitProofBundle.mockReturnValue({
      valid: false,
      checkedFiles: 0,
      errors: ["semantic proof verification failed"],
      rootDigest: null,
      externalRootStatus: "MISMATCH"
    });
    try {
      expect(() => loadVerifiedGitProofView("missing-proof", digest("a"))).toThrow(/Refusing to render an invalid Git proof bundle/);
    } finally {
      verifyGitProofBundle.mockReset();
    }
  });
});
