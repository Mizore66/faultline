import { execFileSync } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { isAbsolute, join, parse, relative, resolve } from "node:path";
import { upsertFaultLineAgentsMd } from "./agents-md.js";
import { investigateGitRange, type GitInvestigationResult } from "./git-investigation.js";
import {
  verifyGitInvestigationProofBundle,
  writeGitInvestigationProofBundle,
  type WrittenGitProofBundle
} from "./git-proof-bundle.js";
import {
  minimizeGitDiff,
  writeGitMinimizationResult,
  type GitMinimizationResult
} from "./git-minimization.js";
import {
  digestRepairPatchFile,
  writePreventionProofFromVerifiedArtifacts,
  type RepairedPreventionRun
} from "./prevention-from-artifacts.js";
import { defaultPreventionProofRoot, verifyPreventionProof, type WrittenPreventionProof } from "./prevention-proof.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "./witness-lock.js";
import { resolveSafeDirectorySegment } from "./safe-directory.js";
import { WITNESS_RESULT_PROTOCOL } from "./witness-result.js";

const DIGEST_PINNED_IMAGE = /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/;
const DEFAULT_DEMO_IMAGE = "node:22-alpine";

export type LiveGitDemoResult = {
  readonly directory: string;
  readonly repository: string;
  readonly image: string;
  readonly frozenWitness: FrozenWitness;
  readonly investigation: GitInvestigationResult;
  readonly proofBundle: WrittenGitProofBundle | null;
};

export type DemoFullPhase = {
  readonly phase: string;
  readonly status: string;
  readonly next?: string;
  readonly detail?: unknown;
};

export type DemoFullResult = LiveGitDemoResult & {
  readonly phases: readonly DemoFullPhase[];
  readonly minimization: GitMinimizationResult | null;
  readonly minimizationPath: string | null;
  readonly prevention: WrittenPreventionProof | null;
  readonly agentsMd: { path: string; status: string } | null;
  readonly ok: boolean;
};

function run(executable: string, argumentsList: readonly string[], label: string): string {
  try {
    const result = execFileSync(executable, [...argumentsList], {
      encoding: "utf8",
      windowsHide: true,
      stdio: ["ignore", "pipe", "pipe"]
    });
    return result.trim();
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    throw new Error(`${label} failed: ${detail}`);
  }
}

/** Create each root segment deliberately so a demo never follows a workspace link. */
function ensureRealDirectoryTree(directory: string): void {
  const absolute = resolve(directory);
  const root = parse(absolute).root;
  const suffix = relative(root, absolute);
  const parts = suffix ? suffix.split(/[\\/]+/).filter(Boolean) : [];
  let current = root;
  if (existsSync(current)) {
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) throw new Error(`Live demo root is not a real directory: ${current}`);
    current = safeCurrent;
  }
  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    const safeCurrent = resolveSafeDirectorySegment(current);
    if (safeCurrent === null) {
      throw new Error(`Live demo cannot traverse a symbolic link or non-directory: ${current}`);
    }
    current = safeCurrent;
  }
}

function isNested(root: string, child: string): boolean {
  const nested = relative(root, child);
  return Boolean(nested) && !nested.startsWith("..") && !isAbsolute(nested);
}

function resolveDemoImage(explicitImage: string | undefined): string {
  if (explicitImage !== undefined) {
    if (!DIGEST_PINNED_IMAGE.test(explicitImage)) {
      throw new Error("--image for fl demo live-git must be a digest-pinned Docker reference.");
    }
    return explicitImage;
  }
  try {
    run("docker", ["pull", DEFAULT_DEMO_IMAGE], "Docker image pull");
    const image = run("docker", ["image", "inspect", DEFAULT_DEMO_IMAGE, "--format", "{{index .RepoDigests 0}}"], "Docker image inspection");
    if (!DIGEST_PINNED_IMAGE.test(image)) throw new Error(`Docker did not resolve ${DEFAULT_DEMO_IMAGE} to a digest-pinned image.`);
    return image;
  } catch (error) {
    throw new Error(`Live Git demo needs Docker and ${DEFAULT_DEMO_IMAGE}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function git(repository: string | undefined, argumentsList: readonly string[], label: string): string {
  return run("git", repository === undefined ? argumentsList : ["-C", repository, ...argumentsList], label);
}

function commit(repository: string, state: "good" | "bad" | "repaired", message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"], "Live demo Git add");
  git(repository, ["commit", "--quiet", "-m", message], "Live demo Git commit");
  return git(repository, ["rev-parse", "HEAD"], "Live demo Git commit resolution");
}

function createFrozenWitness(store: string): FrozenWitness {
  const proposal = proposeWitness(store, {
    proposalId: "live-git-demo",
    proposalOrigin: "HUMAN",
    incidentPacket: {
      symptom: "A known-good state began failing an approved executable witness.",
      ciLog: "state.txt is expected to contain good or repaired, but a commit contains bad.",
      repositoryLanguage: "Node.js fixture",
      repositorySummary: "Self-contained FaultLine live Git demo repository."
    },
    witness: {
      behavior: "The committed state must not be bad.",
      command: "node witness.mjs",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from([
          'import { readFileSync } from "node:fs";',
          'const state = readFileSync("state.txt", "utf8").trim();',
          `const report = (outcome) => console.log(JSON.stringify({ protocol: "${WITNESS_RESULT_PROTOCOL}", outcome }));`,
          'if (state === "bad") { console.error("FaultLine demo witness: bad state"); report("PREDICATE_FAIL"); process.exit(1); }',
          'if (state !== "good" && state !== "repaired") { console.error(`Unexpected state: ${state}`); report("HARNESS_ERROR"); process.exit(2); }',
          'console.log(`FaultLine demo witness: ${state}`);',
          'report("PREDICATE_PASS");'
        ].join("\n"), "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
    }
  });
  approveWitnessProposal(store, proposal.proposalId, { approvedBy: "FaultLine live-demo reviewer" });
  return freezeApprovedWitness(store, proposal.proposalId);
}

/**
 * Creates a disposable real Git regression, freezes its witness, and runs the
 * production Docker path. It intentionally keeps the generated source and
 * proof for a judge to inspect; no existing directory is reused or deleted.
 */
export async function runLiveGitDemo(options: { readonly workspace?: string; readonly image?: string } = {}): Promise<LiveGitDemoResult> {
  const workspace = resolve(options.workspace ?? process.cwd());
  const demoRoot = resolve(workspace, ".faultline", "live-git-demo");
  const proofRoot = resolve(workspace, ".faultline", "git-proof-bundles");
  if (!isNested(workspace, demoRoot) || !isNested(workspace, proofRoot)) throw new Error("Live demo roots must remain inside the workspace.");
  ensureRealDirectoryTree(demoRoot);
  const id = `live-${new Date().toISOString().replace(/[:.]/g, "-")}-${randomUUID().slice(0, 8)}`;
  const directory = join(demoRoot, id);
  mkdirSync(directory, { mode: 0o700 });
  const created = lstatSync(directory);
  if (created.isSymbolicLink() || !created.isDirectory()) throw new Error("Live demo directory was not created as a real directory.");
  const repository = join(directory, "repository");
  const witnessStore = join(directory, "witnesses");
  const image = resolveDemoImage(options.image);

  git(undefined, ["init", "--quiet", repository], "Live demo Git initialization");
  git(repository, ["config", "user.email", "faultline@example.test"], "Live demo Git configuration");
  git(repository, ["config", "user.name", "FaultLine live demo"], "Live demo Git configuration");
  git(repository, ["config", "core.autocrlf", "false"], "Live demo Git configuration");
  const ancestor = commit(repository, "good", "known good");
  commit(repository, "bad", "regression");
  const descendant = commit(repository, "repaired", "repair");
  const frozenWitness = createFrozenWitness(witnessStore);
  const investigation = await investigateGitRange({
    repository,
    range: { ancestor, descendant },
    frozenWitness,
    expectedFrozenDigest: frozenWitness.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image }
  });
  if (!investigation.proof.isProof) {
    return { directory, repository, image, frozenWitness, investigation, proofBundle: null };
  }
  const proofBundle = writeGitInvestigationProofBundle(join(proofRoot, id), investigation, frozenWitness, { proofRoot });
  const verification = verifyGitInvestigationProofBundle(proofBundle.directory, proofBundle.rootDigest);
  if (!verification.valid) throw new Error(`Live Git demo refused to publish an invalid proof bundle: ${verification.errors.join("; ")}`);
  return { directory, repository, image, frozenWitness, investigation, proofBundle };
}

function repairedRunsFromInvestigation(investigation: GitInvestigationResult): {
  commit: string;
  tree: string;
  runs: RepairedPreventionRun[];
} | null {
  const recovery = investigation.transitions.find((transition) => transition.kind === "FAIL_TO_PASS");
  if (!recovery) return null;
  const environmentDigest = investigation.environment.distinctDigests[0];
  if (investigation.environment.homogeneity !== "HOMOGENEOUS" || environmentDigest === undefined) return null;
  const runs: RepairedPreventionRun[] = [];
  for (const runId of recovery.after.runIds) {
    const run = investigation.runs.find((entry) => entry.runId === runId);
    if (!run || run.result.verdict !== "PASS" || run.result.executor !== "NATIVE_DOCKER") return null;
    runs.push({
      runId: run.runId,
      executionId: run.executionId,
      commit: run.commit,
      tree: run.tree,
      verdict: "PASS",
      witnessDigest: run.witnessDigest,
      environmentDigest,
      executionTrust: "NATIVE_DOCKER",
      executionKind: "EXECUTED"
    });
  }
  if (runs.length !== 3) return null;
  return { commit: recovery.after.commit, tree: recovery.after.tree, runs };
}

/**
 * End-to-end flagship arc on the disposable live-git fixture:
 * intake/freeze/Docker localize → offline verify → counterfactual minimize →
 * PREVENTION_VERIFIED (+ AGENTS.md) from repaired-state Docker facts already in the proof.
 */
export async function runDemoFull(options: { readonly workspace?: string; readonly image?: string } = {}): Promise<DemoFullResult> {
  const phases: DemoFullPhase[] = [];
  const demo = await runLiveGitDemo(options);
  if (demo.proofBundle === null) {
    phases.push({
      phase: "intake_freeze_docker_localize",
      status: "DEMO_NOT_PROVEN",
      detail: { investigationStatus: demo.investigation.status, errors: demo.investigation.errors }
    });
    return {
      ...demo,
      phases,
      minimization: null,
      minimizationPath: null,
      prevention: null,
      agentsMd: null,
      ok: false
    };
  }

  phases.push({
    phase: "intake_freeze_docker_localize",
    status: "DEMO_PROOF_READY",
    next: `fl verify ${demo.proofBundle.directory} --expect-root ${demo.proofBundle.rootDigest}`,
    detail: {
      evidenceGrade: "COMMIT_PROOF",
      transitions: demo.investigation.transitions.length,
      rootDigest: demo.proofBundle.rootDigest
    }
  });

  const verification = verifyGitInvestigationProofBundle(
    demo.proofBundle.directory,
    demo.proofBundle.rootDigest
  );
  phases.push({
    phase: "offline_verify",
    status: verification.valid ? "VERIFIED" : "INVALID",
    next: "fl minimize git --help",
    detail: {
      valid: verification.valid,
      externalRootStatus: verification.externalRootStatus,
      evidenceGrade: "COMMIT_PROOF"
    }
  });
  if (!verification.valid) {
    return {
      ...demo,
      phases,
      minimization: null,
      minimizationPath: null,
      prevention: null,
      agentsMd: null,
      ok: false
    };
  }

  const introduction = demo.investigation.transitions.find((transition) => transition.kind === "PASS_TO_FAIL");
  let minimization: GitMinimizationResult | null = null;
  let minimizationPath: string | null = null;
  if (introduction) {
    minimization = await minimizeGitDiff({
      repository: demo.repository,
      before: introduction.before.commit,
      after: introduction.after.commit,
      frozenWitness: demo.frozenWitness,
      expectedFrozenDigest: demo.frozenWitness.frozenDigest,
      sandbox: { mode: "DOCKER_ISOLATED", image: demo.image },
      budget: { maxExecutions: 64 }
    });
    const hunkRefined = minimization.attempts.some((attempt) => attempt.phase === "HUNK_ONE_MINIMAL");
    const grade = minimization.proof.isProof
      ? (hunkRefined ? "hunk-refined counterfactual" : "file-level counterfactual")
      : "ASSOCIATED_NONMINIMAL / non-proof minimize";
    try {
      const written = await writeGitMinimizationResult(
        join(demo.directory, "minimization.json"),
        minimization
      );
      minimizationPath = written.path;
    } catch {
      minimizationPath = null;
    }
    phases.push({
      phase: "minimize",
      status: minimization.proof.isProof ? "MINIMIZED" : minimization.status,
      next: "fl prevention write --from-bundle ...",
      detail: {
        evidenceGrade: grade,
        oneMinimal: minimization.minimality.oneMinimal,
        selectedUnitIds: minimization.candidateUnitIds,
        hunkRefined,
        path: minimizationPath
      }
    });
  } else {
    phases.push({
      phase: "minimize",
      status: "SKIPPED",
      detail: { reason: "No PASS_TO_FAIL transition available for counterfactual minimize." }
    });
  }

  const repaired = repairedRunsFromInvestigation(demo.investigation);
  let prevention: WrittenPreventionProof | null = null;
  let agentsMd: { path: string; status: string } | null = null;
  if (repaired && introduction) {
    const patchPath = join(demo.directory, "repair.patch");
    const patch = git(
      demo.repository,
      ["diff", "--binary", introduction.after.commit, repaired.commit],
      "Demo repair patch"
    );
    writeFileSync(patchPath, patch.endsWith("\n") ? patch : `${patch}\n`, "utf8");
    const exportResult = writePreventionProofFromVerifiedArtifacts({
      bundleDirectory: demo.proofBundle.directory,
      expectRoot: demo.proofBundle.rootDigest,
      outputDirectory: join(defaultPreventionProofRoot(), `demo-full-${Date.now()}`),
      repaired,
      repairPatchDigest: digestRepairPatchFile(patchPath),
      repairBaseTree: introduction.after.tree
    });
    if (!exportResult.ok) {
      phases.push({
        phase: "repair_prevention_agents",
        status: "PREVENTION_EXPORT_FAILED",
        detail: { reasons: exportResult.reasons }
      });
    } else {
      prevention = exportResult.written;
      const preventionVerify = verifyPreventionProof(prevention.directory, prevention.rootDigest);
      if (
        preventionVerify.valid
        && prevention.manifest.classification === "PREVENTION_VERIFIED"
      ) {
        const writtenAgents = upsertFaultLineAgentsMd({
          repository: demo.repository,
          classification: "PREVENTION_VERIFIED",
          originalProofRoot: prevention.prevention.originalProofRoot,
          frozenWitnessDigest: prevention.prevention.frozenWitnessDigest,
          preventionRootDigest: prevention.rootDigest,
          lastGoodRunIds: prevention.prevention.lastGood.runIds,
          firstBadRunIds: prevention.prevention.firstBad.runIds,
          repairedRunIds: prevention.prevention.repaired.runIds
        });
        agentsMd = { path: writtenAgents.path, status: writtenAgents.status };
      }
      phases.push({
        phase: "repair_prevention_agents",
        status: prevention.manifest.classification,
        next: `fl prevention verify ${prevention.directory} --expect-root ${prevention.rootDigest}`,
        detail: {
          evidenceGrade: prevention.manifest.classification,
          preventionRootDigest: prevention.rootDigest,
          agentsMd,
          note: "AGENTS.md is written only on real PREVENTION_VERIFIED; repaired runs reuse NATIVE_DOCKER facts from the proof package."
        }
      });
    }
  } else {
    phases.push({
      phase: "repair_prevention_agents",
      status: "SKIPPED",
      detail: { reason: "Missing FAIL_TO_PASS repaired state or PASS_TO_FAIL introduction." }
    });
  }

  const ok = verification.valid
    && prevention !== null
    && prevention.manifest.classification === "PREVENTION_VERIFIED"
    && agentsMd !== null;
  return {
    ...demo,
    phases,
    minimization,
    minimizationPath,
    prevention,
    agentsMd,
    ok
  };
}
