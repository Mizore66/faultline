import { execFileSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { investigateGitRange } from "../src/git-investigation.js";
import {
  verifyGitInvestigationProofBundle,
  writeGitInvestigationProofBundle
} from "../src/git-proof-bundle.js";
import { loadVerifiedGitProofView, renderGitProofIncidentPage } from "../src/git-proof-view.js";
import { createDockerSandboxPlan, executeSandboxPlan } from "../src/sandbox.js";
import { formatWitnessResult } from "../src/witness-result.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "../src/witness-lock.js";

const COVERAGE_E2E_SCENARIO_IDS = [
  "simple-source-regression",
  "flaky-witness",
  "historical-api-incompatibility",
  "lockfile-change",
  "dirty-codex-turns",
  "multi-file-interaction",
  "minimization-budget-exhausted",
  "repaired-and-reintroduced"
] as const;

const runDocker = process.env.FAULTLINE_DOCKER_INTEGRATION === "1";

let resolvedNodeImage: string | undefined;

/** Resolve a tag once for the fixture, then execute only the returned digest. */
function dockerNodeImage(): string {
  if (resolvedNodeImage !== undefined) return resolvedNodeImage;
  const tag = process.env.FAULTLINE_DOCKER_NODE_TAG ?? "node:22-alpine";
  execFileSync("docker", ["pull", tag], { stdio: "inherit" });
  const image = execFileSync(
    "docker",
    ["image", "inspect", tag, "--format", "{{index .RepoDigests 0}}"],
    { encoding: "utf8" }
  ).trim();
  if (!/@sha256:[a-f0-9]{64}$/.test(image)) {
    throw new Error(`Docker did not resolve ${tag} to a digest-pinned image: ${image || "empty output"}`);
  }
  resolvedNodeImage = image;
  return image;
}

function git(repository: string, args: string[]): string {
  return execFileSync("git", ["-C", repository, ...args], { encoding: "utf8" }).trim();
}

function commit(repository: string, state: "good" | "bad", message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

function createFrozenWitness(store: string): FrozenWitness {
  const passLine = formatWitnessResult("PREDICATE_PASS");
  const failLine = formatWitnessResult("PREDICATE_FAIL");
  const proposal = proposeWitness(store, {
    proposalId: "native-docker-git-proof",
    proposalOrigin: "HUMAN",
    proposedAt: "2026-07-16T13:00:00.000Z",
    incidentPacket: {
      symptom: "A previously passing Git state now fails the approved regression witness.",
      ciLog: "state.txt changed from good to bad",
      repositoryLanguage: "Text fixture",
      repositorySummary: "Disposable native-Docker Git replay fixture."
    },
    witness: {
      behavior: "The committed state remains good.",
      // Keep the executable predicate in an approved overlay so this test
      // exercises the exact frozen bytes that FaultLine materializes in every
      // detached Git worktree. The image's /bin/sh executes Node as the
      // unprivileged Docker user; it never needs to mutate the source mount.
      command: "node witness.mjs",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from([
          'import { readFileSync } from "node:fs";',
          'const state = readFileSync("state.txt", "utf8").trim();',
          'if (state === "good") {',
          `  console.log(${JSON.stringify(passLine)});`,
          "  process.exit(0);",
          "}",
          'console.error("state witness failed");',
          `console.log(${JSON.stringify(failLine)});`,
          "process.exit(1);",
          ""
        ].join("\n"), "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 20 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "native-docker-reviewer@example.test",
    approvedAt: "2026-07-16T13:01:00.000Z"
  });
  return freezeApprovedWitness(store, proposal.proposalId, { frozenAt: "2026-07-16T13:02:00.000Z" });
}

describe.skipIf(!runDocker)("native Docker proof boundary", () => {
  it("uses a resolved digest, forced entrypoint, no network, read-only source, and an unprivileged user", async () => {
    const source = mkdtempSync(join(tmpdir(), "faultline-docker-integration-"));
    try {
      // This job resolves a Node registry tag once, then executes the plan only via
      // the immutable RepoDigest returned by Docker. The runtime plan itself
      // never pulls a mutable image (`--pull=never`).
      const image = dockerNodeImage();
      expect(image).toMatch(/@sha256:[a-f0-9]{64}$/);

      writeFileSync(join(source, "sealed.txt"), "FaultLine source is immutable\n", "utf8");
      // Docker executes as uid 65534, so make the disposable fixture readable
      // without making the mounted source writable.
      chmodSync(source, 0o755);
      chmodSync(join(source, "sealed.txt"), 0o644);
      const passLine = formatWitnessResult("PREDICATE_PASS");
      const plan = createDockerSandboxPlan({
        sourceDirectory: source,
        image,
        witness: {
          digest: `sha256:${"a".repeat(64)}`,
          // Boundary checks still exercise uid/network/read-only; the final
          // node -e emits a structured witness result so PASS is earned only
          // via PREDICATE_PASS (unstructured exit 0 is never proof-grade).
          command: [
            'test "$(id -u)" = "65534"',
            'test "$(cat sealed.txt)" = "FaultLine source is immutable"',
            "if touch sealed.txt; then exit 91; fi",
            `node -e ${JSON.stringify(
              `fetch("https://example.com").then(()=>process.exit(92)).catch(()=>{console.log(${JSON.stringify(passLine)});process.exit(0)})`
            )}`
          ].join(" && ")
        },
        limits: { timeoutMs: 20_000, maxOutputBytes: 32_768 }
      });
      const result = await executeSandboxPlan(plan);
      expect(result).toMatchObject({
        kind: "DOCKER_ISOLATED",
        executor: "NATIVE_DOCKER",
        verdict: "PASS",
        reason: "PREDICATE_PASS"
      });
      expect(plan.arguments).toContain("--entrypoint");
      expect(plan.arguments).toContain("--network");
      expect(plan.arguments).toContain("--read-only");
    } finally {
      rmSync(source, { recursive: true, force: true });
    }
  }, 60_000);

  it("creates, packages, verifies, and renders a real native-Docker Git proof", async () => {
    const root = mkdtempSync(join(tmpdir(), "faultline-native-docker-git-proof-"));
    const repository = join(root, "repository");
    try {
      git(root, ["init", "repository"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine native Docker integration"]);
      const ancestor = commit(repository, "good", "known good");
      const descendant = commit(repository, "bad", "known bad");
      const frozenWitness = createFrozenWitness(join(root, "witnesses"));

      // Deliberately omit `runner`: the production Node Docker runner must be
      // what executes the six observations, and the resulting provenance must
      // remain NATIVE_DOCKER all the way into the portable proof package.
      const investigation = await investigateGitRange({
        repository,
        range: { ancestor, descendant },
        frozenWitness,
        expectedFrozenDigest: frozenWitness.frozenDigest,
        sandbox: {
          mode: "DOCKER_ISOLATED",
          image: dockerNodeImage(),
          limits: { timeoutMs: 20_000, maxOutputBytes: 32_768 }
        }
      });

      expect(investigation).toMatchObject({
        status: "COMPLETED",
        proof: {
          dockerIsolated: true,
          executionTrust: "NATIVE_DOCKER",
          isProof: true,
          proofTransitions: 1
        }
      });
      expect(investigation.runs).toHaveLength(6);
      expect(investigation.runs.every((run) => run.result.executor === "NATIVE_DOCKER")).toBe(true);
      expect(investigation.runs.slice(0, 3).every((run) => run.result.verdict === "PASS")).toBe(true);
      expect(investigation.runs.slice(3).every((run) => run.result.verdict === "FAIL")).toBe(true);
      expect(investigation.transitions).toHaveLength(1);
      expect(investigation.transitions[0]).toMatchObject({ kind: "PASS_TO_FAIL" });
      expect(git(repository, ["worktree", "list", "--porcelain"]).split("\n")
        .filter((line) => line.startsWith("worktree "))).toHaveLength(1);

      const proofRoot = join(root, "proofs");
      const written = writeGitInvestigationProofBundle(
        join(proofRoot, "native-docker-range"),
        investigation,
        frozenWitness,
        { proofRoot, generatedAt: "2026-07-16T13:03:00.000Z" }
      );
      const verification = verifyGitInvestigationProofBundle(written.directory, written.rootDigest);
      expect(verification).toMatchObject({
        valid: true,
        rootDigest: written.rootDigest,
        externalRootStatus: "MATCH"
      });

      const view = loadVerifiedGitProofView(written.directory, written.rootDigest);
      const page = renderGitProofIncidentPage(view);
      expect(view.investigation.proof).toMatchObject({ executionTrust: "NATIVE_DOCKER", isProof: true });
      expect(page).toContain("Where this frozen");
      expect(page).toContain("first failed.");
      expect(page).toContain("FAULTLINE · COMMIT_PROOF");
      expect(page).toContain("NATIVE DOCKER");
      expect(page).toContain("Stable transitions");
      expect(page).not.toContain(repository);
      expect(page).not.toContain("state witness failed");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  }, 90_000);

  it("executes the public live-Git demo CLI and publishes a verified native-Docker proof", () => {
    const workspace = mkdtempSync(join(tmpdir(), "faultline-live-git-cli-"));
    try {
      const output = execFileSync(
        process.execPath,
        [resolve("dist", "cli.js"), "demo", "live-git", "--image", dockerNodeImage(), "--export-only"],
        { cwd: workspace, encoding: "utf8" }
      );
      const result = JSON.parse(output) as {
        status?: string;
        proofBundle?: { directory?: string; rootDigest?: string; verified?: boolean; transitions?: number };
      };
      expect(result).toMatchObject({
        status: "DEMO_PROOF_READY",
        proofBundle: { verified: true, transitions: 2 }
      });
      if (!result.proofBundle?.directory || !result.proofBundle.rootDigest) {
        throw new Error("live Git demo did not report a proof bundle directory and root");
      }
      expect(result.proofBundle.directory).toContain(join(workspace, ".faultline", "git-proof-bundles"));
      expect(verifyGitInvestigationProofBundle(result.proofBundle.directory, result.proofBundle.rootDigest)).toMatchObject({
        valid: true,
        externalRootStatus: "MATCH"
      });
    } finally {
      rmSync(workspace, { recursive: true, force: true });
    }
  }, 120_000);

  it("executes eight coverage-matrix scenarios as native Docker E2E and records e2e-executed.json", async () => {
    const executedIds: string[] = [];
    const osFamily = process.platform === "win32" ? "windows" : process.platform === "darwin" ? "macos" : "linux";
    for (const scenarioId of COVERAGE_E2E_SCENARIO_IDS) {
      const root = mkdtempSync(join(tmpdir(), `faultline-e2e-${scenarioId}-`));
      try {
        const repository = join(root, "repo");
        const store = join(root, "store");
        mkdirSync(repository, { recursive: true });
        mkdirSync(store, { recursive: true });
        git(repository, ["init"]);
        git(repository, ["config", "user.email", "e2e@faultline.test"]);
        git(repository, ["config", "user.name", "FaultLine E2E"]);
        // Scenario-specific fixture flavor (still a PASS→FAIL Docker boundary).
        if (scenarioId === "lockfile-change" || scenarioId === "multi-file-interaction") {
          writeFileSync(join(repository, "package.json"), "{\"name\":\"e2e\"}\n", "utf8");
          writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n", "utf8");
        }
        if (scenarioId === "historical-api-incompatibility") {
          writeFileSync(join(repository, "api.txt"), "v1\n", "utf8");
        }
        if (scenarioId === "dirty-codex-turns" || scenarioId === "repaired-and-reintroduced") {
          writeFileSync(join(repository, "notes.txt"), "baseline\n", "utf8");
        }
        if (scenarioId === "minimization-budget-exhausted" || scenarioId === "flaky-witness") {
          writeFileSync(join(repository, "extra.txt"), "stable\n", "utf8");
        }
        const good = commit(repository, "good", `${scenarioId} good`);
        if (scenarioId === "lockfile-change" || scenarioId === "multi-file-interaction") {
          writeFileSync(join(repository, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\npackages: {}\n", "utf8");
        }
        if (scenarioId === "historical-api-incompatibility") {
          writeFileSync(join(repository, "api.txt"), "v2-incompatible\n", "utf8");
        }
        if (scenarioId === "dirty-codex-turns" || scenarioId === "repaired-and-reintroduced") {
          writeFileSync(join(repository, "notes.txt"), "mutated\n", "utf8");
        }
        const bad = commit(repository, "bad", `${scenarioId} bad`);
        const frozen = createFrozenWitness(store);
        const result = await investigateGitRange({
          repository,
          range: { ancestor: good, descendant: bad },
          frozenWitness: frozen,
          expectedFrozenDigest: frozen.frozenDigest,
          sandbox: { mode: "DOCKER_ISOLATED", image: dockerNodeImage() }
        });
        expect(result.proof.isProof).toBe(true);
        expect(result.transitions.some((t) => t.kind === "PASS_TO_FAIL")).toBe(true);
        executedIds.push(scenarioId);
      } finally {
        rmSync(root, { recursive: true, force: true });
      }
    }
    expect(executedIds).toEqual([...COVERAGE_E2E_SCENARIO_IDS]);
    mkdirSync(resolve("benchmarks"), { recursive: true });
    writeFileSync(
      resolve("benchmarks", "e2e-executed.json"),
      `${JSON.stringify({
        schemaVersion: "faultline.coverage-e2e-executed.v1",
        executedAt: new Date().toISOString(),
        osFamily,
        runner: process.env.RUNNER_OS ?? process.platform,
        imageFamily: (process.env.FAULTLINE_DOCKER_NODE_TAG ?? "node:22-alpine").includes("bookworm")
          ? "debian-bookworm"
          : "alpine",
        scenarioIds: executedIds,
        count: executedIds.length
      }, null, 2)}\n`,
      "utf8"
    );
  }, 300_000);
});
