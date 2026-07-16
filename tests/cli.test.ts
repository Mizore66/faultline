import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { createHash, generateKeyPairSync } from "node:crypto";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { describe, expect, it } from "vitest";
import { createRepairEvidencePacket } from "../src/repair-brief.js";
import { digestJson } from "../src/canonical.js";
import {
  GitInvestigationResultSchema,
  investigateGitRange,
  type GitInvestigationResult
} from "../src/git-investigation.js";
import { writeGitInvestigationProofBundle } from "../src/git-proof-bundle.js";
import { readIncidentDraft } from "../src/incident-store.js";
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

function runFl(
  args: string[],
  options: { cwd?: string; input?: string; env?: NodeJS.ProcessEnv } = {}
): { status: number | null; stdout: string; stderr: string } {
  const result = spawnSync(process.execPath, [tsxCli, faultLineCli, ...args], {
    cwd: options.cwd ?? workspace,
    input: options.input,
    // The test process can itself be running in GitHub Actions. Let callers
    // explicitly control the CI identity seen by the CLI under test.
    env: { ...process.env, ...options.env },
    encoding: "utf8"
  });
  return { status: result.status, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
}

function git(repository: string, args: string[]): string {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
  return (result.stdout ?? "").trim();
}

function quoteFsmonitorCommandPart(value: string): string {
  return `"${value.replaceAll("\\", "/").replaceAll('"', '\\"')}"`;
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
  it("reports the package version through the installable fl entry point", () => {
    const version = runFl(["--version"]);
    expect(version.status).toBe(0);
    expect(version.stdout.trim()).toBe("FaultLine 0.1.0");
  });

  it("requires an explicit confirmation before guided runtime setup can pull Docker images", () => {
    const result = runFl(["runtime", "prepare", "node"]);
    expect(result.status).toBe(1);
    const output = JSON.parse(result.stdout) as {
      status: string;
      runtime: { tag: string };
      effect: string;
      next: string;
    };
    expect(output).toMatchObject({
      status: "CONFIRMATION_REQUIRED",
      runtime: { tag: "node:22-alpine" },
      next: "fl runtime prepare node --yes"
    });
    expect(output.effect).toContain("docker pull node:22-alpine");
  });

  it("shows a reviewable dependency-image build plan and requires confirmation before Dockerfile execution", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-runtime-project-"));
    const context = join(directory, "application context");
    try {
      mkdirSync(context, { recursive: true });
      writeFileSync(join(context, "Dockerfile"), "FROM node:22-alpine\nRUN echo prepared\n", "utf8");
      const args = [
        "runtime", "project", "plan", "--context", context, "--dockerfile", "Dockerfile",
        "--tag", "registry.example/faultline/demo:deps-20260717", "--network", "default"
      ];
      const plan = runFl(args, { cwd: directory });
      expect(plan.status).toBe(0);
      expect(JSON.parse(plan.stdout)).toMatchObject({
        status: "PROJECT_IMAGE_BUILD_REVIEW_REQUIRED",
        plan: {
          setupOnly: true,
          contextDirectory: context,
          dockerfile: join(context, "Dockerfile"),
          imageTag: "registry.example/faultline/demo:deps-20260717",
          network: "default",
          effects: {
            dockerfileInstructionsExecute: true,
            mayAccessNetwork: true,
            doesNotExecuteProof: true
          },
          review: { planDigest: expect.stringMatching(/^sha256:[a-f0-9]{64}$/) }
        }
      });

      const confirmation = runFl([...args.slice(0, 2), "build", ...args.slice(3)], { cwd: directory });
      expect(confirmation.status).toBe(1);
      expect(JSON.parse(confirmation.stdout)).toMatchObject({
        status: "CONFIRMATION_REQUIRED",
        plan: { setupOnly: true, network: "default" },
        requiredPlanDigest: expect.stringMatching(/^sha256:[a-f0-9]{64}$/)
      });
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("emits a directly installable Codex hook document with an explicit platform command", () => {
    const command = "node /opt/faultline/dist/cli.js codex sidecar hook --quiet";
    const commandWindows = "node C:\\tools\\faultline\\dist\\cli.js codex sidecar hook --quiet";
    const result = runFl([
      "codex", "sidecar", "config", "--command", command, "--command-windows", commandWindows
    ]);
    expect(result.status).toBe(0);
    const config = JSON.parse(result.stdout) as {
      hooks: {
        SessionStart: Array<{ hooks: Array<{ command: string; commandWindows?: string; timeout: number }> }>;
        UserPromptSubmit: Array<{ hooks: Array<{ command: string; commandWindows?: string; timeout: number }> }>;
        Stop: Array<{ hooks: Array<{ command: string; commandWindows?: string; timeout: number }> }>;
      };
    };
    expect(Object.keys(config)).toEqual(["hooks"]);
    for (const event of [config.hooks.SessionStart, config.hooks.UserPromptSubmit, config.hooks.Stop]) {
      expect(event[0]?.hooks[0]).toMatchObject({ command, commandWindows, timeout: 30 });
    }
  });

  it("generates quoted Unix and Windows hook commands from a real CLI path with spaces", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline cli path "));
    const builtCli = join(directory, "built cli", "faultline cli.js");
    try {
      mkdirSync(dirname(builtCli), { recursive: true });
      writeFileSync(builtCli, "#!/usr/bin/env node\n", "utf8");
      const result = runFl(["codex", "sidecar", "config", "--cli", builtCli]);
      expect(result.status).toBe(0);
      const config = JSON.parse(result.stdout) as {
        hooks: {
          SessionStart: Array<{ hooks: Array<{ command: string; commandWindows?: string }> }>;
        };
      };
      expect(config.hooks.SessionStart[0]?.hooks[0]).toMatchObject({
        command: `node '${builtCli}' codex sidecar hook --quiet`,
        commandWindows: `node "${builtCli}" codex sidecar hook --quiet`
      });
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("requires confirmation, writes a new project hook document once, and preserves an existing one", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-sidecar-install-"));
    const repository = join(directory, "application repository");
    const builtCli = join(directory, "FaultLine build", "cli.js");
    try {
      git(directory, ["init", "application repository"]);
      mkdirSync(dirname(builtCli), { recursive: true });
      writeFileSync(builtCli, "#!/usr/bin/env node\n", "utf8");
      const nonGitDirectory = join(directory, "not a repository");
      mkdirSync(nonGitDirectory, { recursive: true });
      const rejected = runFl(["codex", "sidecar", "install", "--repo", nonGitDirectory, "--cli", builtCli, "--yes"], { cwd: directory });
      expect(rejected.status).toBe(1);
      expect(rejected.stderr).toContain("usable local Git worktree");
      const baseArgs = ["codex", "sidecar", "install", "--repo", repository, "--cli", builtCli];
      const preview = runFl(baseArgs, { cwd: directory });
      expect(preview.status).toBe(1);
      const pending = JSON.parse(preview.stdout) as { status: string; target: string; hooks: { hooks: unknown } };
      expect(pending).toMatchObject({ status: "CONFIRMATION_REQUIRED", target: join(repository, ".codex", "hooks.json") });
      expect(pending.hooks.hooks).toBeDefined();
      expect(existsSync(join(repository, ".codex", "hooks.json"))).toBe(false);

      const installed = runFl([...baseArgs, "--yes"], { cwd: directory });
      expect(installed.status).toBe(0);
      expect(JSON.parse(installed.stdout)).toMatchObject({ status: "PROJECT_HOOKS_INSTALLED", hookFile: join(repository, ".codex", "hooks.json") });
      const config = JSON.parse(readFileSync(join(repository, ".codex", "hooks.json"), "utf8")) as {
        hooks: { Stop: Array<{ hooks: Array<{ commandWindows?: string }> }> };
      };
      expect(config.hooks.Stop[0]?.hooks[0]?.commandWindows).toBe(`node "${builtCli}" codex sidecar hook --quiet`);

      const second = runFl([...baseArgs, "--yes"], { cwd: directory });
      expect(second.status).toBe(1);
      expect(second.stderr).toContain("will not replace an existing project hook document");
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("reports observed Codex sidecar health and its ledger handoff path without prompt text", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-codex-sidecar-"));
    const repository = join(directory, "source");
    const sessionId = "cli-sidecar-session";
    const turnId = "cli-sidecar-turn";
    const prompt = "Do not print this sidecar prompt marker.";
    try {
      git(directory, ["init", "source"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine CLI test"]);
      commit(repository, "clean", "clean sidecar fixture");
      const event = (value: unknown) => runFl(["codex", "sidecar", "hook"], {
        cwd: directory,
        input: JSON.stringify(value)
      });
      expect(event({ hook_event_name: "SessionStart", session_id: sessionId, cwd: repository, model: "gpt-5.6" }).status).toBe(0);
      expect(event({ hook_event_name: "UserPromptSubmit", session_id: sessionId, cwd: repository, model: "gpt-5.6", turn_id: turnId, prompt }).status).toBe(0);
      expect(event({ hook_event_name: "Stop", session_id: sessionId, cwd: repository, model: "gpt-5.6", turn_id: turnId }).status).toBe(0);

      const status = runFl(["codex", "sidecar", "status", "--repo", repository, "--session", sessionId], { cwd: directory });
      expect(status.status).toBe(0);
      const output = JSON.parse(status.stdout) as {
        status: string;
        recordings: Array<{ ledgerPath: string; latestStop?: { status: string } }>;
      };
      expect(output).toMatchObject({
        status: "SIDECAR_RECORDINGS_READY",
        recordings: [{ latestStop: { status: "CHECKPOINT_RECORDED" } }]
      });
      expect(output.recordings[0]?.ledgerPath).toContain("faultline");
      expect(status.stdout).not.toContain(prompt);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

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
      const review = runFl(["witness", "review", "cli-witness", "--json", "--store", store]);
      expect(review.status).toBe(0);
      expect(JSON.parse(review.stdout)).toMatchObject({
        status: "LOCAL_READ_ONLY_REVIEW",
        review: {
          mode: "LOCAL_READ_ONLY",
          proposal: { proposalId: "cli-witness", witness: { command: "node witness.mjs" } }
        }
      });
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

  it("creates a conservative, review-only first-incident draft without executing the pasted command", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-incident-intake-"));
    try {
      const repository = join(directory, "source");
      const store = join(directory, "witnesses");
      git(directory, ["init", "source"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine CLI test"]);
      const ancestor = commit(repository, "good", "known good");
      const descendant = commit(repository, "bad", "reported failure");
      const marker = join(directory, "intake-must-not-execute.txt");
      const command = `node -e \"require('node:fs').writeFileSync(${JSON.stringify(marker)}, 'should-not-run')\"`;

      const intake = runFl([
        "incident", "start", "--repo", repository, "--command", command,
        "--id", "onboarding-incident", "--store", store
      ], { cwd: directory });

      expect(intake.status).toBe(0);
      expect(existsSync(marker)).toBe(false);
      const output = JSON.parse(intake.stdout) as {
        status: string;
        draft: { path: string; range: { ancestor: string; descendant: string; source: string } };
        witnessProposal: { proposalId: string; state: string };
        next: string[];
        limitations: string[];
      };
      expect(output.status).toBe("DRAFT_REQUIRES_HUMAN_REVIEW");
      expect(output.draft.range).toEqual({ ancestor, descendant, source: "LOCAL_HEAD_PARENT" });
      expect(output.witnessProposal).toMatchObject({ proposalId: "onboarding-incident", state: "NOT_APPROVED_NOT_FROZEN" });
      expect(output.next.join(" ")).toContain("fl witness review onboarding-incident");
      expect(output.next.join(" ")).toContain("--draft-store");
      expect(output.limitations.join(" ")).toMatch(/did not execute/i);
      expect(readIncidentDraft(join(repository, ".faultline", "incidents"), "onboarding-incident").draft.review).toMatchObject({
        required: true,
        state: "PENDING_HUMAN_REVIEW",
        witnessState: "PROPOSED",
        freezeState: "NOT_FROZEN",
        autoFreeze: false
      });
      expect(existsSync(join(store, "proposals", "onboarding-incident.json"))).toBe(true);
      expect(existsSync(join(store, "frozen", "onboarding-incident.json"))).toBe(false);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("suggests a locally tracked upstream range without contacting it and can bind an explicit project-image digest", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-incident-suggest-"));
    try {
      const repository = join(directory, "source");
      const store = join(directory, "witnesses");
      git(directory, ["init", "source"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine CLI test"]);
      const ancestor = commit(repository, "good", "locally tracked base");
      const descendant = commit(repository, "bad", "reported failure");
      const branch = git(repository, ["branch", "--show-current"]);
      git(repository, ["remote", "add", "origin", "https://127.0.0.1:1/faultline-never-contacted.git"]);
      git(repository, ["update-ref", `refs/remotes/origin/${branch}`, ancestor]);
      git(repository, ["config", `branch.${branch}.remote`, "origin"]);
      git(repository, ["config", `branch.${branch}.merge`, `refs/heads/${branch}`]);

      const suggestions = runFl(["incident", "suggest", "--repo", repository], { cwd: directory });
      expect(suggestions.status).toBe(0);
      const suggestionOutput = JSON.parse(suggestions.stdout) as {
        status: string;
        automaticSelection: string;
        candidates: Array<Record<string, unknown>>;
      };
      expect(suggestionOutput.status).toBe("RANGE_SUGGESTIONS_READY");
      expect(suggestionOutput.automaticSelection).toBe("NONE");
      expect(suggestionOutput.candidates.find((candidate) => candidate.id === "LOCAL_UPSTREAM_MERGE_BASE"))
        .toMatchObject({
          from: ancestor,
          to: descendant,
          requiresHumanApproval: true,
          evidence: { remoteContacted: false }
        });

      const started = runFl([
        "incident", "start", "--repo", repository, "--command", "node -e \"process.exit(0)\"",
        "--id", "suggested-project-image", "--from", ancestor, "--to", descendant,
        "--image", pinnedImage, "--store", store
      ], { cwd: directory });
      expect(started.status).toBe(0);
      expect(JSON.parse(started.stdout)).toMatchObject({
        draft: {
          range: { ancestor, descendant, source: "EXPLICIT" },
          runtime: { requested: "explicit", image: pinnedImage }
        }
      });
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("continues one immutable incident after a human freeze without retyping its range or witness digest", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-incident-continue-"));
    try {
      const repository = join(directory, "source");
      const store = join(directory, "witnesses");
      git(directory, ["init", "source"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine CLI test"]);
      const ancestor = commit(repository, "good", "known good");
      const descendant = commit(repository, "bad", "reported failure");
      const id = "continue-incident";

      expect(runFl([
        "incident", "start", "--repo", repository, "--command", "node -e \"process.exit(0)\"",
        "--id", id, "--store", store
      ], { cwd: directory }).status).toBe(0);

      const pending = runFl(["incident", "status", id, "--repo", repository, "--store", store], { cwd: directory });
      expect(pending.status).toBe(0);
      expect(JSON.parse(pending.stdout)).toMatchObject({
        status: "REVIEW_REQUIRED",
        incident: { id, range: { ancestor, descendant } },
        frozenWitness: { valid: false }
      });

      expect(runFl(["witness", "approve", id, "--approved-by", "reviewer@example.test", "--store", store], { cwd: directory }).status).toBe(0);
      const frozen = runFl(["witness", "freeze", id, "--store", store], { cwd: directory });
      expect(frozen.status).toBe(0);
      const frozenDigest = (JSON.parse(frozen.stdout) as { frozenDigest: string }).frozenDigest;

      const retentionRequired = runFl(["incident", "status", id, "--repo", repository, "--store", store], { cwd: directory });
      expect(retentionRequired.status).toBe(0);
      expect(JSON.parse(retentionRequired.stdout)).toMatchObject({
        status: "RETAIN_DIGEST_REQUIRED",
        frozenWitness: { valid: true, frozenDigest, externalDigestStatus: "NOT_PROVIDED" }
      });

      const ready = runFl([
        "incident", "status", id, "--repo", repository, "--store", store, "--expect-digest", frozenDigest
      ], { cwd: directory });
      expect(ready.status).toBe(0);
      expect(JSON.parse(ready.stdout)).toMatchObject({
        status: "READY_TO_INVESTIGATE",
        frozenWitness: { valid: true, frozenDigest, externalDigestStatus: "MATCH" }
      });

      const withoutRetainedDigest = runFl([
        "incident", "continue", id, "--repo", repository, "--store", store, "--image", pinnedImage
      ], { cwd: directory });
      expect(withoutRetainedDigest.status).toBe(1);
      expect(withoutRetainedDigest.stderr).toMatch(/requires --expect-digest/i);

      // This local escape hatch is intentionally not proof-grade, but it
      // exercises the durable handoff without retyping the selected range,
      // proposal id, or frozen digest into `fl investigate git`.
      const continued = runFl([
        "incident", "continue", id, "--repo", repository, "--store", store, "--unsafe-local"
      ], { cwd: directory });
      expect(continued.status).toBe(1);
      expect(JSON.parse(continued.stdout)).toMatchObject({
        status: "INVESTIGATION_NOT_PROOF",
        incident: {
          id,
          range: { ancestor, descendant },
          frozenDigest,
          frozenDigestExternalStatus: "NOT_PROVIDED"
        },
        proofBundle: null
      });
      expect(continued.stdout).not.toContain("node -e");
      expect(runFl(["incident", "status", id, "--repo", directory, "--store", store], { cwd: directory }).status).toBe(1);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("refuses incomplete shareable attachment flags instead of silently serving a boundary-only page", () => {
    const missingRepair = runFl(["serve", "--bundle", "placeholder", "--repair"]);
    expect(missingRepair.status).toBe(1);
    expect(missingRepair.stderr).toContain("Missing required option: --repair");

    const missingMinimization = runFl(["serve", "--bundle", "placeholder", "--minimization"]);
    expect(missingMinimization.status).toBe(1);
    expect(missingMinimization.stderr).toContain("Missing required option: --minimization");

    const missingRepairDigest = runFl(["serve", "--bundle", "placeholder", "--repair", "repair-directory"]);
    expect(missingRepairDigest.status).toBe(1);
    expect(missingRepairDigest.stderr).toContain("shareable repair attachment requires both");
  });

  it("keeps doctor Git diagnostics from invoking a repository-local fsmonitor command", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-doctor-fsmonitor-"));
    try {
      const repository = join(directory, "source");
      git(directory, ["init", "source"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine CLI test"]);
      commit(repository, "good", "known good");
      const hook = join(repository, "forbidden-fsmonitor.cjs");
      const marker = join(repository, "fsmonitor-ran");
      writeFileSync(hook, 'require("node:fs").writeFileSync(process.argv[2], "invoked", "utf8");\n', "utf8");
      const hostileHook = [
        quoteFsmonitorCommandPart(process.execPath),
        quoteFsmonitorCommandPart(hook),
        quoteFsmonitorCommandPart(marker)
      ].join(" ");
      git(repository, ["config", "core.fsmonitor", hostileHook]);

      // Docker readiness is environment-dependent and may rightly return a
      // nonzero preflight. The security assertion is that fixed doctor Git
      // probes never dispatch the repository's configured executable.
      runFl(["doctor", "--repo", repository], { cwd: directory });
      expect(existsSync(marker)).toBe(false);
    } finally {
      try {
        // Git or Defender can retain a just-closed handle beneath a temporary
        // worktree longer than Node's short default retry window on hosted
        // Windows runners. The security assertion already ran above; a final
        // EPERM/EBUSY during disposable runner cleanup must not misreport the
        // fsmonitor test as a FaultLine behavior failure.
        rmSync(directory, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 });
      } catch (error) {
        const code = error instanceof Error && "code" in error ? String((error as NodeJS.ErrnoException).code) : "";
        if (process.platform !== "win32" || (code !== "EPERM" && code !== "EBUSY")) throw error;
      }
    }
  });

  it("gives consecutive default incident starts collision-resistant identifiers", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-incident-ids-"));
    try {
      const repository = join(directory, "source");
      git(directory, ["init", "source"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine CLI test"]);
      commit(repository, "good", "known good");
      commit(repository, "bad", "reported failure");
      const first = runFl(["incident", "start", "--repo", repository, "--command", "node witness.mjs"], { cwd: directory });
      const second = runFl(["incident", "start", "--repo", repository, "--command", "node witness.mjs"], { cwd: directory });
      expect(first.status).toBe(0);
      expect(second.status).toBe(0);
      const firstId = (JSON.parse(first.stdout) as { witnessProposal: { proposalId: string } }).witnessProposal.proposalId;
      const secondId = (JSON.parse(second.stdout) as { witnessProposal: { proposalId: string } }).witnessProposal.proposalId;
      expect(firstId).not.toBe(secondId);
      expect(firstId).toMatch(/^incident-\d{17}-[0-9a-f-]{12}$/);
      expect(secondId).toMatch(/^incident-\d{17}-[0-9a-f-]{12}$/);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("records and verifies an optional reviewer-held Ed25519 approval through the CLI", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-authenticated-witness-"));
    try {
      const store = join(directory, "witnesses");
      const inputFile = join(directory, "proposal.json");
      const privateKeyFile = join(directory, "reviewer-private.pem");
      const keyringFile = join(directory, "reviewers.json");
      writeFileSync(inputFile, JSON.stringify({
        proposalId: "cli-authenticated-witness",
        incidentPacket: {
          symptom: "CI failure",
          ciLog: "expected true, received false",
          repositoryLanguage: "TypeScript",
          repositorySummary: "temporary authenticated CLI test"
        },
        witness: {
          behavior: "A true value remains true.",
          command: "node witness.mjs",
          overlays: [{ path: "witness.mjs", bytesBase64: Buffer.from("process.exit(0)\n", "utf8").toString("base64") }],
          policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 10 }
        }
      }), "utf8");
      const pair = generateKeyPairSync("ed25519");
      const publicDer = pair.publicKey.export({ type: "spki", format: "der" });
      const keyId = `sha256:${createHash("sha256").update(publicDer).digest("hex")}`;
      writeFileSync(privateKeyFile, pair.privateKey.export({ type: "pkcs8", format: "pem" }).toString(), "utf8");
      writeFileSync(keyringFile, JSON.stringify({
        schemaVersion: "faultline.reviewer-keyring.v1",
        reviewers: [{
          approvedBy: "reviewer@example.test",
          keyId,
          algorithm: "ED25519",
          publicKeyPem: pair.publicKey.export({ type: "spki", format: "pem" }).toString()
        }]
      }), "utf8");

      expect(runFl(["witness", "propose", "--input", inputFile, "--store", store]).status).toBe(0);
      expect(runFl(["witness", "approve", "cli-authenticated-witness", "--approved-by", "reviewer@example.test", "--store", store]).status).toBe(0);
      expect(runFl(["witness", "freeze", "cli-authenticated-witness", "--store", store]).status).toBe(0);
      const signed = runFl([
        "witness", "sign", "cli-authenticated-witness", "--private-key", privateKeyFile,
        "--keyring", keyringFile, "--store", store
      ]);
      expect(signed.status).toBe(0);
      expect(JSON.parse(signed.stdout)).toMatchObject({ status: "AUTHENTICATED_APPROVAL_RECORDED", keyId });
      const verified = runFl([
        "witness", "verify", "cli-authenticated-witness", "--keyring", keyringFile,
        "--require-signature", "--store", store
      ]);
      expect(verified.status).toBe(0);
      expect(JSON.parse(verified.stdout)).toMatchObject({ valid: true, signatureStatus: "VERIFIED" });
      expect(readFileSync(join(store, "authenticated-approvals", "cli-authenticated-witness.json"), "utf8"))
        .not.toContain(pair.privateKey.export({ type: "pkcs8", format: "pem" }).toString());
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("refuses to label a locally created provenance subject as signed CI evidence", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-provenance-local-"));
    try {
      const result = runFl(["provenance", "create", "--bundle", join(directory, "not-a-proof")], {
        cwd: directory,
        // This test asserts the local boundary even when Vitest runs on a
        // public GitHub Actions runner, whose parent environment sets this.
        env: { GITHUB_ACTIONS: "false" }
      });
      expect(result.status).toBe(1);
      expect(result.stderr).toMatch(/only be created inside GitHub Actions/);
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
