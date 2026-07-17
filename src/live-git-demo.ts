import { execFileSync } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { isAbsolute, join, parse, relative, resolve } from "node:path";
import { investigateGitRange, type GitInvestigationResult } from "./git-investigation.js";
import {
  verifyGitInvestigationProofBundle,
  writeGitInvestigationProofBundle,
  type WrittenGitProofBundle
} from "./git-proof-bundle.js";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "./witness-lock.js";
import { resolveSafeDirectorySegment } from "./safe-directory.js";

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
          'if (state === "bad") { console.error("FaultLine demo witness: bad state"); process.exit(1); }',
          'if (state !== "good" && state !== "repaired") { console.error(`Unexpected state: ${state}`); process.exit(2); }',
          'console.log(`FaultLine demo witness: ${state}`);'
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
