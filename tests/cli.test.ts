import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { createRepairEvidencePacket } from "../src/repair-brief.js";
import { digestJson } from "../src/canonical.js";
import {
  GitInvestigationResultSchema,
  investigateGitRange,
  type GitInvestigationResult
} from "../src/git-investigation.js";
import { writeGitInvestigationProofBundle } from "../src/git-proof-bundle.js";
import type { SandboxCommandRunner } from "../src/sandbox.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../src/witness-lock.js";

const workspace = process.cwd();
const tsxCli = join(workspace, "node_modules", "tsx", "dist", "cli.mjs");
const faultLineCli = join(workspace, "src", "cli.ts");

function runFl(args: string[], options: { cwd?: string; input?: string } = {}): { status: number | null; stdout: string; stderr: string } {
  const result = spawnSync(process.execPath, [tsxCli, faultLineCli, ...args], {
    cwd: options.cwd ?? workspace,
    input: options.input,
    encoding: "utf8"
  });
  return { status: result.status, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
}

function git(repository: string, args: string[]): string {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
  return (result.stdout ?? "").trim();
}

const pinnedImage = `registry.example/faultline-node@sha256:${"b".repeat(64)}`;

function commit(repository: string, state: string, message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

function createRepairWitness(store: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId: "cli-repair-proof",
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T11:00:00.000Z",
    incidentPacket: {
      symptom: "The repository state must not be bad.",
      ciLog: "state.txt became bad",
      repositoryLanguage: "Text",
      repositorySummary: "Temporary Git proof source for the repair CLI test."
    },
    witness: {
      behavior: "state.txt must not be bad",
      command: "node witness.mjs",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from("export const faultLineWitness = 'approved-exact-bytes';\n", "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "reviewer@example.test",
    approvedAt: "2026-07-16T11:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T11:02:00.000Z" });
}

function deterministicDockerRunner(): SandboxCommandRunner {
  return {
    async run(invocation) {
      const state = readFileSync(join(invocation.cwd, "state.txt"), "utf8").trim();
      return state === "bad"
        ? { exitCode: 1, stdout: `state=${state}\n`, stderr: "witness failed" }
        : { exitCode: 0, stdout: `state=${state}\n`, stderr: "" };
    }
  };
}

/** A controlled serialized fixture lets this CLI test exercise package verification without Docker. */
function nativeDockerFixture(observed: GitInvestigationResult): GitInvestigationResult {
  const runs = observed.runs.map((run) => {
    const { runId: _runId, ...unsigned } = {
      ...run,
      result: { ...run.result, executor: "NATIVE_DOCKER" as const }
    };
    return { ...unsigned, runId: digestJson(unsigned) };
  });
  const stableStates = observed.states.map((state) => {
    const stateRuns = runs.filter((run) => run.stateIndex === state.index)
      .sort((left, right) => left.executionAttempt - right.executionAttempt);
    const verdict = stateRuns[0]?.result.verdict;
    if (verdict !== "PASS" && verdict !== "FAIL") throw new Error("repair CLI fixture did not create a decisive state");
    return {
      stateIndex: state.index,
      commit: state.commit,
      tree: state.tree,
      verdict,
      executionIds: stateRuns.map((run) => run.executionId),
      runIds: stateRuns.map((run) => run.runId)
    };
  });
  const transitions = stableStates.slice(1).flatMap((after, index) => {
    const before = stableStates[index];
    if (!before || before.verdict === after.verdict) return [];
    return [{ kind: before.verdict === "PASS" ? "PASS_TO_FAIL" as const : "FAIL_TO_PASS" as const, before, after }];
  });
  return GitInvestigationResultSchema.parse({
    ...observed,
    runs,
    stableStates,
    transitions,
    nonMonotonic: transitions.some((transition) => transition.kind === "PASS_TO_FAIL")
      && transitions.some((transition) => transition.kind === "FAIL_TO_PASS"),
    proof: {
      requiresDockerIsolation: true,
      dockerIsolated: true,
      executionTrust: "NATIVE_DOCKER",
      proofTransitions: transitions.length,
      isProof: transitions.length > 0,
      reason: "Each listed transition has three distinct Docker-isolated executions on both adjacent Git states."
    }
  });
}

async function createVerifiedRepairBundle(root: string): Promise<{ directory: string; investigation: GitInvestigationResult }> {
  const repository = join(root, "repair-proof-source");
  git(root, ["init", "repair-proof-source"]);
  git(repository, ["config", "user.email", "faultline@example.test"]);
  git(repository, ["config", "user.name", "FaultLine CLI test"]);
  const ancestor = commit(repository, "good", "known good");
  commit(repository, "bad", "regression");
  const descendant = commit(repository, "repaired", "repair");
  const frozen = createRepairWitness(join(root, "repair-witnesses"));
  const observed = await investigateGitRange({
    repository,
    range: { ancestor, descendant },
    frozenWitness: frozen,
    expectedFrozenDigest: frozen.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
    runner: deterministicDockerRunner()
  });
  const investigation = nativeDockerFixture(observed);
  const directory = join(root, "verified-proof", "repair-range");
  writeGitInvestigationProofBundle(directory, investigation, frozen, {
    proofRoot: join(root, "verified-proof"),
    generatedAt: "2026-07-16T11:03:00.000Z"
  });
  return { directory, investigation };
}

describe("FaultLine CLI workflows", () => {
  it("creates an externally retained integrity receipt for a verified bundle", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-attestation-"));
    try {
      const bundle = join(directory, ".faultline", "bundles", "receipt-demo");
      const receipts = join(directory, ".faultline", "attestations");
      expect(runFl(["judge-demo", "--rerun-all", "--export-only", "--output", bundle], { cwd: directory }).status).toBe(0);
      expect(runFl(["verify", bundle], { cwd: directory }).status).toBe(0);
      const created = runFl([
        "attest", "create", "--bundle", bundle, "--receipt", "cli-receipt",
        "--subject", "FaultLine CLI test bundle", "--issuer", "test-retention-boundary", "--store", receipts
      ], { cwd: directory });
      expect(created.status).toBe(0);
      const receipt = JSON.parse(created.stdout) as { receiptDigest: string };
      const verified = runFl(["attest", "verify", "cli-receipt", "--expect-digest", receipt.receiptDigest, "--store", receipts], { cwd: directory });
      expect(verified.status).toBe(0);
      expect(JSON.parse(verified.stdout)).toMatchObject({ valid: true, externalDigestStatus: "MATCH" });
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("persists a proposal through human approval and immutable witness freeze", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-witness-"));
    try {
      const store = join(directory, "witnesses");
      const inputFile = join(directory, "proposal.json");
      writeFileSync(inputFile, JSON.stringify({
        proposalId: "cli-witness",
        proposalOrigin: "HUMAN",
        incidentPacket: {
          symptom: "CI failure",
          ciLog: "expected true, received false",
          repositoryLanguage: "TypeScript",
          repositorySummary: "temporary CLI test"
        },
        witness: {
          behavior: "A true value remains true.",
          command: "node witness.mjs",
          overlays: [{ path: "witness.mjs", bytesBase64: Buffer.from("process.exit(0)\n", "utf8").toString("base64") }],
          policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 10 }
        }
      }), "utf8");

      expect(runFl(["witness", "propose", "--input", inputFile, "--store", store]).status).toBe(0);
      expect(runFl(["witness", "approve", "cli-witness", "--approved-by", "reviewer@example.test", "--store", store]).status).toBe(0);
      const freeze = runFl(["witness", "freeze", "cli-witness", "--store", store]);
      expect(freeze.status).toBe(0);
      const frozen = JSON.parse(freeze.stdout) as { frozenDigest: string };
      const verification = runFl(["witness", "verify", "cli-witness", "--expect-digest", frozen.frozenDigest, "--store", store]);
      expect(verification.status).toBe(0);
      expect(JSON.parse(verification.stdout)).toMatchObject({ valid: true, externalDigestStatus: "MATCH" });
      expect(existsSync(join(store, "frozen", "cli-witness.json"))).toBe(true);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("records an observed lifecycle stream and a real clean Git checkpoint", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-ledger-"));
    const repository = join(directory, "repo");
    const ledger = join(directory, "recordings", "session.json");
    try {
      git(directory, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.invalid"]);
      git(repository, ["config", "user.name", "FaultLine CLI test"]);
      writeFileSync(join(repository, "sample.txt"), "stable\n", "utf8");
      git(repository, ["add", "sample.txt"]);
      git(repository, ["commit", "-m", "clean checkpoint"]);

      expect(runFl(["record", "init", "--session", "cli-session", "--ledger", ledger, "--repo", repository, "--transport", "CODEX_CLI"]).status).toBe(0);
      const events = [
        { eventId: "turn-start", event: { type: "TURN_STARTED", payload: { turnId: "turn-1", turnOrdinal: 1, promptDigest: `sha256:${"a".repeat(64)}` } } },
        { eventId: "turn-complete", event: { type: "TURN_COMPLETED", payload: { turnId: "turn-1", turnOrdinal: 1, outcome: "COMPLETED", outputDigest: `sha256:${"b".repeat(64)}` } } }
      ].map((event) => JSON.stringify(event)).join("\n");
      expect(runFl(["record", "stdin", "--ledger", ledger], { input: events }).status).toBe(0);
      expect(runFl(["record", "checkpoint", "--ledger", ledger, "--repo", repository, "--after-turn", "1"]).status).toBe(0);
      const verification = runFl(["record", "verify", "--ledger", ledger]);
      expect(verification.status).toBe(0);
      expect(JSON.parse(verification.stdout)).toMatchObject({ valid: true, eventCount: 4 });
      expect(readFileSync(ledger, "utf8")).toContain("WORKTREE_CHECKPOINT");
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("stores only citation-validated, explicitly inferred repair guidance from a verified Git proof package", async () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-repair-"));
    const inputFile = join(directory, "repair.json");
    const output = join(directory, ".faultline", "repair-briefs", "offline-brief");
    try {
      const proof = await createVerifiedRepairBundle(directory);
      // The input deliberately contains a recognizable credential. The stored
      // repair brief must contain only a deterministic redaction placeholder.
      writeFileSync(inputFile, JSON.stringify({
        schemaVersion: "faultline.repair-brief.v1",
        evidencePacketDigest: "placeholder",
        proposedInvariant: { statement: "Preserve the proven behavior.", evidenceIds: ["E1"] },
        repairDirections: [{ statement: "Rotate token=sk-abcdefghijklmnopqrstuvwxyz123456 before deployment.", evidenceIds: ["E2"] }],
        prevention: { hardEnforcement: [{ statement: "Keep a regression guard in CI.", evidenceIds: ["E1"] }], softGuidance: [] },
        uncertainties: ["The evidence does not identify a unique semantic cause."]
      }), "utf8");

      const packetDigest = createRepairEvidencePacket(proof.investigation).packetDigest;
      const candidate = JSON.parse(readFileSync(inputFile, "utf8")) as Record<string, unknown>;
      candidate.evidencePacketDigest = packetDigest;
      writeFileSync(inputFile, JSON.stringify(candidate), "utf8");

      const result = runFl(["repair", "brief", "--bundle", proof.directory, "--input", inputFile, "--output", output], { cwd: directory });
      expect(result.status).toBe(0);
      const persisted = JSON.parse(result.stdout) as { classification: string; directory: string; evidencePacketDigest: string };
      expect(persisted).toMatchObject({ classification: "INFERRED", directory: output });
      expect(existsSync(join(output, "manifest.json"))).toBe(true);
      expect(existsSync(join(output, "evidence-packet.json"))).toBe(true);
      expect(existsSync(join(output, "repair-brief.json"))).toBe(true);
      expect(readFileSync(join(output, "manifest.json"), "utf8")).toContain("INFERRED");
      const stored = readFileSync(join(output, "repair-brief.json"), "utf8");
      expect(stored).not.toContain("sk-abcdefghijklmnopqrstuvwxyz123456");
      expect(stored).toContain("<FAULTLINE_REDACTED:OPENAI_API_KEY:");
      expect(readFileSync(join(output, "evidence-packet.json"), "utf8")).not.toContain("repair-proof-source");
      const verification = runFl(["repair", "verify", output], { cwd: directory });
      expect(verification.status).toBe(0);
      expect(JSON.parse(verification.stdout)).toMatchObject({ valid: true, manifest: { classification: "INFERRED" } });
      expect(runFl(["repair", "brief", "--bundle", proof.directory, "--input", inputFile, "--output", output], { cwd: directory }).status).toBe(1);
      expect(runFl(["repair", "brief", "--bundle", proof.directory, "--input", inputFile, "--output", join(directory, "outside")], { cwd: directory }).status).toBe(1);
      const byArtifact = join(directory, ".faultline", "repair-briefs", "artifact-brief");
      expect(runFl([
        "repair", "brief", "--investigation", join(proof.directory, "investigation.json"),
        "--input", inputFile, "--output", byArtifact
      ], { cwd: directory }).status).toBe(0);
      expect(runFl(["repair", "brief", "--investigation", join(directory, "not-a-bundle", "investigation.json"), "--input", inputFile], { cwd: directory }).status).toBe(1);
      writeFileSync(join(output, "repair-brief.json"), "{}\n", "utf8");
      expect(runFl(["repair", "verify", output], { cwd: directory }).status).toBe(1);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});
