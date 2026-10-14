// Writer replay scenarios (slice 2 spec 2.4): the workspace a writer case
// starts from. Each scenario is a pure function of its name, built once by
// running frozen TS under the seed preload and committed under
// difftest/testdata/scenarios/<name>/, so the Go replay copies the exact
// files fl would have written instead of rebuilding them.
//   pnpm build && pnpm exec tsx difftest/gen/scenarios.ts
import { spawn } from "node:child_process";
import { chmodSync, cpSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cli = join(repo, "dist", "cli.js");
const seedPreload = join(repo, "difftest", "gen", "seed.mjs");
export const scenariosDir = join(repo, "difftest", "testdata", "scenarios");
const recorderDir = join(repo, "difftest", "testdata", "recorder");
const fakeDockerDir = join(repo, "difftest", "testdata", "fakedocker");

// The pinned oracle release: CI regenerates on it and fails on drift.
export function requireOracleNode(): void {
  if (process.version !== "v22.22.2" && process.env.FAULTLINE_ALLOW_NODE_MISMATCH !== "1") {
    console.error(`writer goldens come from Node v22.22.2, not ${process.version} (FAULTLINE_ALLOW_NODE_MISMATCH=1 for local experiments only)`);
    process.exit(1);
  }
}

// Goldens must come from a build of the current src/.
function newestMtime(dir: string): number {
  let newest = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    newest = Math.max(newest, entry.isDirectory() ? newestMtime(path) : statSync(path).mtimeMs);
  }
  return newest;
}
export function requireFreshBuild(): void {
  if (!existsSync(cli) || statSync(cli).mtimeMs < newestMtime(join(repo, "src"))) {
    console.error("dist/cli.js is missing or older than src/: run `pnpm build` first");
    process.exit(1);
  }
}

// CaseRoots are the directories one case owns: the workspace (the command's
// cwd), HOME and TMPDIR, all under one physical mkdtemp directory so the
// paths fl prints are the ones the harness masks.
export type CaseRoots = { root: string; ws: string; home: string; tmp: string; argvLog: string };

export function makeCaseRoots(): CaseRoots {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "faultline-writer-")));
  const roots = { root, ws: join(root, "ws"), home: join(root, "home"), tmp: join(root, "tmp"), argvLog: join(root, "argv.log") };
  for (const dir of [roots.ws, roots.home, roots.tmp]) mkdirSync(dir);
  writeFileSync(join(roots.home, "empty.gitconfig"), "");
  return roots;
}

// inheritedOut names the variables a writer case never inherits: git's own,
// FaultLine's, and those src/ reads to detect CI or a live model.
export function inheritedOut(name: string): boolean {
  return name.startsWith("GIT_") || name.startsWith("FAULTLINE_") || ["CI", "GITHUB_ACTIONS", "OPENAI_API_KEY"].includes(name);
}

// The environment every writer invocation runs with; extra is the
// template's env (after placeholder substitution).
export function writerEnv(roots: CaseRoots, seed: string, fakeDocker: string | undefined, extra: Record<string, string>): NodeJS.ProcessEnv {
  const path = [recorderDir, ...(fakeDocker ? [join(fakeDockerDir, fakeDocker)] : []), process.env.PATH ?? ""].join(":");
  return {
    ...Object.fromEntries(Object.entries(process.env).filter(([name]) => !inheritedOut(name))),
    HOME: roots.home, USERPROFILE: roots.home, TMPDIR: roots.tmp, TEMP: roots.tmp, TMP: roots.tmp,
    GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(roots.home, "empty.gitconfig"), LC_ALL: "C.UTF-8", LANGUAGE: "", GIT_DEFAULT_HASH: "sha1",
    FAULTLINE_TEST_SEED: seed, FAULTLINE_NO_BROWSER: "1", FAULTLINE_ARGV_LOG: roots.argvLog, PATH: path,
    ...extra
  };
}

export type RunResult = { stdout: string; stderr: string; exit: number };

// runCli runs frozen TS under the seed preload with cwd = the workspace.
export function runCli(roots: CaseRoots, argv: string[], env: NodeJS.ProcessEnv): Promise<RunResult> {
  return new Promise((done) => {
    const child = spawn(process.execPath, ["--import", pathToFileURL(seedPreload).href, cli, ...argv], { env, cwd: roots.ws });
    const out: Buffer[] = [];
    const err: Buffer[] = [];
    child.stdout.on("data", (b) => out.push(b));
    child.stderr.on("data", (b) => err.push(b));
    child.on("close", (code) => done({ stdout: Buffer.concat(out).toString("utf8"), stderr: Buffer.concat(err).toString("utf8"), exit: code ?? -1 }));
  });
}

export const PROPOSAL_ID = "demo-1";

// PROPOSAL_INPUT is the `witness propose --input` file the witness scenarios
// start from; it stays in the workspace as proposal.json.
export const PROPOSAL_INPUT = {
  proposalId: PROPOSAL_ID,
  incidentPacket: {
    symptom: "The repository state must not be bad.",
    ciLog: "state.txt became bad",
    repositoryLanguage: "Text",
    repositorySummary: "Temporary repository for the writer replay."
  },
  witness: {
    behavior: "state.txt must not be bad",
    command: "node witness.mjs",
    overlays: [{ path: "witness.mjs", bytesBase64: Buffer.from("export const faultLineWitness = 'approved-exact-bytes';\n", "utf8").toString("base64") }],
    policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
  }
};

const PROPOSE = ["witness", "propose", "--input", "proposal.json"];
const APPROVE = ["witness", "approve", PROPOSAL_ID, "--approved-by", "reviewer@example.test"];
const FREEZE = ["witness", "freeze", PROPOSAL_ID];

// SCENARIOS: the slice 1 bases a scenario copies in (difftest/testdata/bases,
// keyed by workspace path), the files it writes, then the fl invocations that
// build the rest of it (each under FAULTLINE_TEST_SEED=scenario:<name>).
type Scenario = { bases?: Record<string, string>; files: Record<string, string>; argv: string[][] };
export const SCENARIOS: Record<string, Scenario> = {
  "empty": { files: {}, argv: [] },
  "witness-proposed": { files: { "proposal.json": `${JSON.stringify(PROPOSAL_INPUT, null, 2)}\n` }, argv: [PROPOSE] },
  "witness-approved": { files: { "proposal.json": `${JSON.stringify(PROPOSAL_INPUT, null, 2)}\n` }, argv: [PROPOSE, APPROVE] },
  "witness-frozen": { files: { "proposal.json": `${JSON.stringify(PROPOSAL_INPUT, null, 2)}\n` }, argv: [PROPOSE, APPROVE, FREEZE] },
  // A Git proof bundle: `fl verify bundle` runs git, so it checks the
  // recorders and the argv normalization.
  "git-two-states-bundle": { bases: { bundle: "git-two-states" }, files: {}, argv: [] }
};

// A scenario is committed as <name>/dirs.txt, every directory of the
// workspace one per line (git keeps no empty directories, and the witness
// store makes some), and <name>/tree/, its files.
// copyScenario fills the workspace from the committed scenario.
export function copyScenario(name: string, ws: string): void {
  if (!Object.hasOwn(SCENARIOS, name)) throw new Error(`unknown scenario ${name}`);
  const dir = join(scenariosDir, name);
  for (const rel of readFileSync(join(dir, "dirs.txt"), "utf8").split("\n").filter(Boolean)) mkdirSync(join(ws, rel), { recursive: true });
  if (existsSync(join(dir, "tree"))) cpSync(join(dir, "tree"), ws, { recursive: true });
  resetModes(ws);
}

// resetModes makes a copied scenario's modes independent of the checkout:
// directories 0755, files 0644 (the Go twin does the same).
function resetModes(root: string, rel = ""): void {
  for (const name of readdirSync(join(root, rel))) {
    const r = rel ? `${rel}/${name}` : name;
    const stat = lstatSync(join(root, r));
    if (stat.isDirectory()) {
      chmodSync(join(root, r), 0o755);
      resetModes(root, r);
    } else if (stat.isFile()) chmodSync(join(root, r), 0o644);
  }
}

// listDirs returns every directory under root, sorted; it refuses anything
// but directories and regular files.
function listDirs(root: string, rel = ""): string[] {
  const out: string[] = [];
  for (const name of readdirSync(join(root, rel)).sort()) {
    const r = rel ? `${rel}/${name}` : name;
    const stat = lstatSync(join(root, r));
    if (stat.isDirectory()) out.push(r, ...listDirs(root, r));
    else if (!stat.isFile()) throw new Error(`scenario entry ${r} is not a regular file`);
  }
  return out;
}

async function buildScenario(name: string): Promise<void> {
  const scenario = SCENARIOS[name]!;
  const roots = makeCaseRoots();
  try {
    for (const [path, base] of Object.entries(scenario.bases ?? {})) {
      cpSync(join(repo, "difftest", "testdata", "bases", base), join(roots.ws, path), { recursive: true });
    }
    for (const [path, text] of Object.entries(scenario.files)) {
      mkdirSync(dirname(join(roots.ws, path)), { recursive: true });
      writeFileSync(join(roots.ws, path), text);
    }
    for (const argv of scenario.argv) {
      const r = await runCli(roots, argv, writerEnv(roots, `scenario:${name}`, "pass-fail", {}));
      if (r.exit !== 0) throw new Error(`scenario ${name}: fl ${argv.join(" ")} exited ${r.exit}\n${r.stdout}${r.stderr}`);
    }
    const dirs = listDirs(roots.ws);
    const out = join(scenariosDir, name);
    rmSync(out, { recursive: true, force: true });
    mkdirSync(out, { recursive: true });
    writeFileSync(join(out, "dirs.txt"), dirs.map((d) => `${d}\n`).join(""));
    if (readdirSync(roots.ws).length > 0) cpSync(roots.ws, join(out, "tree"), { recursive: true });
  } finally {
    rmSync(roots.root, { recursive: true, force: true });
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  process.umask(0o022);
  requireOracleNode();
  requireFreshBuild();
  for (const entry of existsSync(scenariosDir) ? readdirSync(scenariosDir) : []) {
    if (!Object.hasOwn(SCENARIOS, entry)) rmSync(join(scenariosDir, entry), { recursive: true, force: true });
  }
  for (const name of Object.keys(SCENARIOS).sort()) {
    await buildScenario(name);
    console.log(`scenario ${name}`);
  }
}
