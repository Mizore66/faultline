import { appendFileSync, copyFileSync, lstatSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { isAbsolute, join, relative, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const workspace = resolve(process.env.GITHUB_WORKSPACE ?? process.cwd());
const actionPath = resolve(requiredEnvironment("FAULTLINE_ACTION_PATH"));
const witnessInput = requiredEnvironment("FAULTLINE_WITNESS");
const base = requiredEnvironment("FAULTLINE_BASE");
const head = requiredEnvironment("FAULTLINE_HEAD");
const runtime = requiredEnvironment("FAULTLINE_RUNTIME");
const repositoryInput = process.env.FAULTLINE_REPOSITORY?.trim() || ".";
const proofMode = requiredEnvironment("FAULTLINE_PROOF_MODE");
const outputFile = requiredEnvironment("GITHUB_OUTPUT");

function requiredEnvironment(name) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`Missing required environment variable: ${name}`);
  return value;
}

function workspacePath(input, label, allowRoot = false) {
  const path = resolve(workspace, input);
  const nested = relative(workspace, path);
  if ((!allowRoot && !nested) || nested.startsWith("..") || isAbsolute(nested)) {
    throw new Error(`${label} must resolve beneath the checked-out repository.`);
  }
  return path;
}

function setOutput(name, value) {
  appendFileSync(outputFile, `${name}=${value}\n`, "utf8");
}

if (proofMode !== "required" && proofMode !== "diagnostic") {
  throw new Error("proof-mode must be either required or diagnostic.");
}
if (proofMode === "required" && !/@sha256:[a-f0-9]{64}$/.test(runtime)) {
  throw new Error("runtime must be a digest-pinned Docker image in required proof mode.");
}

const witnessPath = workspacePath(witnessInput, "witness");
const witnessStat = lstatSync(witnessPath);
if (!witnessStat.isFile() || witnessStat.isSymbolicLink()) {
  throw new Error("witness must be a regular non-symlink file.");
}
const repository = workspacePath(repositoryInput, "repository", true);
const repositoryStat = lstatSync(repository);
if (!repositoryStat.isDirectory() || repositoryStat.isSymbolicLink()) {
  throw new Error("repository must be a real directory.");
}
const witness = JSON.parse(readFileSync(witnessPath, "utf8"));
const proposalId = witness?.proposal?.proposalId;
const frozenDigest = witness?.frozenDigest;
if (witness?.schemaVersion !== "faultline.frozen-witness.v1"
  || typeof proposalId !== "string"
  || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(proposalId)
  || typeof frozenDigest !== "string"
  || !/^sha256:[a-f0-9]{64}$/.test(frozenDigest)) {
  throw new Error("witness is not a valid frozen-witness envelope.");
}

const temporaryStore = join(tmpdir(), `faultline-action-${process.pid}-${Date.now()}`);
const frozenStore = join(temporaryStore, "frozen");
const outputDirectory = join(workspace, ".faultline", "git-proof-bundles", `action-${process.env.GITHUB_RUN_ID ?? "local"}-${process.env.GITHUB_RUN_ATTEMPT ?? "1"}-${Date.now()}`);

try {
  mkdirSync(frozenStore, { recursive: true, mode: 0o700 });
  copyFileSync(witnessPath, join(frozenStore, `${proposalId}.json`));

  const args = [
    join(actionPath, "dist", "cli.js"),
    "investigate", "git",
    "--repo", repository,
    "--from", base,
    "--to", head,
    "--proposal", proposalId,
    "--expect-digest", frozenDigest,
    "--store", temporaryStore,
    "--output", outputDirectory,
    ...(proofMode === "required" ? ["--image", runtime] : ["--unsafe-local"])
  ];
  const result = spawnSync(process.execPath, args, { cwd: workspace, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);

  let response;
  try {
    response = JSON.parse(result.stdout ?? "");
  } catch {
    throw new Error(`FaultLine did not return a JSON result (exit ${result.status ?? "unknown"}).`);
  }

  if (proofMode === "required") {
    if (result.status !== 0) throw new Error(`FaultLine proof run failed with exit code ${result.status ?? "unknown"}.`);
    const proofPackage = response?.proofBundle?.directory;
    const rootDigest = response?.proofBundle?.rootDigest;
    if (typeof proofPackage !== "string" || !isAbsolute(proofPackage)
      || typeof rootDigest !== "string" || !/^sha256:[a-f0-9]{64}$/.test(rootDigest)) {
      throw new Error("FaultLine completed without a valid proof package output.");
    }
    setOutput("proof-package", proofPackage);
    setOutput("root-digest", rootDigest);
  } else {
    if (!response?.investigation || response?.proofBundle !== null) {
      throw new Error("Diagnostic replay returned an unexpected result.");
    }
    setOutput("proof-package", "");
    setOutput("root-digest", "");
  }
} finally {
  rmSync(temporaryStore, { recursive: true, force: true });
}
