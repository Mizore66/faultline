// Regenerates difftest/testdata/golden/writers.jsonl from frozen TS: every
// writer template (difftest/testdata/writers.json) runs its invocations in
// one workspace under the seed preload; each invocation gives one line
// (slice 2 spec 2.3). The templates of difftest/testdata/writers.d/<name>.json
// write their lines to golden/writers.d/<name>.jsonl instead (one pair per
// task, slice 4a). The Go twin is difftest/writer_test.go.
//   pnpm build && pnpm exec tsx difftest/gen/scenarios.ts && pnpm exec tsx difftest/gen/gen-writers.ts
// (FAULTLINE_WRITERS_ONLY=<regex>: only the matching cases, see below.)
import { execFileSync } from "node:child_process";
import { chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, readlinkSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:net";
import { tmpdir } from "node:os";
import { basename, dirname, isAbsolute, join, resolve } from "node:path";
import { canonicalJson, digestJson, sha256 } from "../../src/canonical.js";
import { PATH_OPS, applyMutation, expandRepeats, normalizeWriter, treeDigest, type Template } from "./cases.js";
import { startFakeOpenAI, type FakeOpenAI } from "./fake-openai.js";
import { copyScenario, makeCaseRoots, refreshScenarioIndexes, resetModes, restoreScenarioModes, requireFreshBuild, requireOracleNode, runCli, writerEnv, type CaseRoots, type WriterFakes } from "./scenarios.js";

// input / inputHex: each invocation's stdin (text, where <WS>, <TMP>, <HOME>
// and <REPEAT:c:n> are expanded; or hex bytes), null or a missing entry an
// empty one. advanceClock: invocation n >= 1 runs under seed <id>/<n> with
// the clock n hours on. A mutation's inv: the invocation it is applied just
// before (default: case setup).
export type WriterTemplate = {
  id: string; scenario: string; argv: string[][]; fakeDocker?: string; fakeTools?: string; pathTools?: string[]; env?: Record<string, string>;
  posixOnly?: boolean; mutations?: Template[]; input?: (string | null)[]; inputHex?: (string | null)[]; advanceClock?: boolean;
  // Slice 4c fakes: difftest/testdata/fakecodex/codex (one script; the
  // variant <fakeCodex> is written into its variant= line) as
  // FAULTLINE_CODEX_BIN (or on PATH with fakeCodexOnPath),
  // difftest/testdata/fakegh/<fakeGh> on PATH, and a fake OpenAI server
  // answering from the workspace file fakeOpenAI (fake-openai.ts).
  fakeCodex?: string; fakeCodexOnPath?: boolean; fakeGh?: string; fakeOpenAI?: string;
};

const TEMPLATE_KEYS = new Set(["id", "scenario", "argv", "fakeDocker", "fakeTools", "pathTools", "env", "posixOnly", "mutations", "input", "inputHex", "advanceClock",
  "fakeCodex", "fakeCodexOnPath", "fakeGh", "fakeOpenAI"]);

// TMPDIR_FAKE_DOCKERS are the fake Dockers that keep state in $TMPDIR on
// purpose (difftest/testdata/fakedocker/README.md).
const TMPDIR_FAKE_DOCKERS = new Set(["timeout-once", "tmpdir-swap"]);

// checksTmpdir says whether a case's TMPDIR (<TMP>) must be empty after
// each invocation (spec 3.3, "same files left behind"): every case but those
// whose fake Docker or env puts files there itself. The Go twin is
// checksTmpdir in difftest/writer_test.go.
export function checksTmpdir(t: WriterTemplate): boolean {
  return !TMPDIR_FAKE_DOCKERS.has(t.fakeDocker ?? "") && !Object.values(t.env ?? {}).some((v) => v.includes("<TMP>"));
}

// tmpLeftovers lists what an invocation left in the case's TMPDIR, sorted.
export function tmpLeftovers(roots: CaseRoots): string[] {
  return existsSync(roots.tmp) ? readdirSync(roots.tmp).sort(byCodeUnit) : [];
}

// checkTemplates rejects what the Go loader rejects too: unknown keys,
// duplicate ids (across every template file), cases without invocations,
// both input and inputHex, a malformed input entry and a mutation inv that
// names no invocation.
function checkTemplates(templates: WriterTemplate[]): void {
  const ids = new Set<string>();
  for (const t of templates) {
    for (const key of Object.keys(t)) if (!TEMPLATE_KEYS.has(key)) throw new Error(`writer template ${t.id}: unknown key ${key}`);
    if (ids.has(t.id)) throw new Error(`duplicate writer template ${t.id}`);
    if (!/^[a-z0-9][a-z0-9-]*$/.test(t.id)) throw new Error(`writer template id ${t.id} is not lowercase kebab-case`);
    if (t.argv.length === 0) throw new Error(`writer template ${t.id} has no invocation`);
    if (t.input !== undefined && t.inputHex !== undefined) throw new Error(`writer template ${t.id} has both input and inputHex`);
    const inputs = t.input ?? t.inputHex;
    if (inputs !== undefined && (!Array.isArray(inputs) || inputs.some((i) => i !== null && typeof i !== "string"))) throw new Error(`writer template ${t.id}: an input entry is neither a string nor null`);
    for (const hex of t.inputHex ?? []) if (hex !== null && !/^(?:[0-9a-fA-F]{2})*$/.test(hex)) throw new Error(`writer template ${t.id}: inputHex ${hex} is not hex`);
    for (const m of t.mutations ?? []) {
      if (m.inv !== undefined && !(Number.isInteger(m.inv) && m.inv >= 0 && m.inv < t.argv.length)) throw new Error(`writer template ${t.id}: mutation inv ${JSON.stringify(m.inv)} is not one of its invocations`);
    }
    ids.add(t.id);
  }
}

// invocationInput is invocation inv's stdin bytes: its input text
// (substituted, repeats expanded, as UTF-8) or inputHex bytes, else none.
// The Go twin is invocationInput in difftest/writer_test.go.
export function invocationInput(t: WriterTemplate, inv: number, roots: CaseRoots): Buffer {
  const text = t.input?.[inv];
  if (typeof text === "string") return Buffer.from(expandRepeats(substitute(text, roots)), "utf8");
  const hex = t.inputHex?.[inv];
  return typeof hex === "string" ? Buffer.from(hex, "hex") : Buffer.alloc(0);
}

// invocationSeed is the seed and extra environment of invocation inv: with
// advanceClock, invocation n >= 1 runs under seed <id>/<n> with the clock n
// hours on (seed.mjs FAULTLINE_TEST_CLOCK_OFFSET_MS), since the seeded clock
// restarts in every process. The Go twin is invocationEnv.
export function invocationSeed(t: WriterTemplate, inv: number): { seed: string; env: Record<string, string> } {
  if (!t.advanceClock || inv === 0) return { seed: t.id, env: {} };
  return { seed: `${t.id}/${inv}`, env: { FAULTLINE_TEST_CLOCK_OFFSET_MS: String(inv * 3_600_000) } };
}

// pathsUnder lists every entry under root (lstat; not following links).
function pathsUnder(root: string, rel = "", out = new Set<string>()): Set<string> {
  for (const name of readdirSync(join(root, rel))) {
    const r = rel ? `${rel}/${name}` : name;
    out.add(r);
    if (lstatSync(join(root, r)).isDirectory()) pathsUnder(root, r, out);
  }
  return out;
}

// resetNewModes gives the entries a mutation applied before an invocation
// created the modes resetModes gives (0755 directories, 0644 files): the
// harness's own writes (Node's 0o666/0o777 under umask 0, Go's 0o644/0o755)
// would otherwise differ, and no later resetModes runs. Entries that existed
// keep their modes. The Go twin is resetNewModes in difftest/writer_test.go.
export function resetNewModes(root: string, before: Set<string>): void {
  for (const rel of [...pathsUnder(root)].sort(byCodeUnit)) {
    if (before.has(rel)) continue;
    const stat = lstatSync(join(root, rel));
    if (stat.isDirectory()) chmodSync(join(root, rel), 0o755);
    else if (stat.isFile()) chmodSync(join(root, rel), 0o644);
  }
}

// substitute replaces <WS>, <TMP> and <HOME> in argv and env values.
export function substitute(value: string, roots: CaseRoots): string {
  return value.split("<WS>").join(roots.ws).split("<TMP>").join(roots.tmp).split("<HOME>").join(roots.home);
}

// substituteDeep applies substitute to every string inside a mutation.
function substituteDeep(value: unknown, roots: CaseRoots): unknown {
  if (typeof value === "string") return substitute(value, roots);
  if (Array.isArray(value)) return value.map((item) => substituteDeep(item, roots));
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, substituteDeep(v, roots)]));
  }
  return value;
}

const byCodeUnit = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

// treeEntries describes every path under root the way treeDigest walks it:
// a file by its SHA-256, a symlink by its target, a directory, or anything
// else (a FIFO); all but symlinks also carry their permission bits in octal
// ("<sha256> 600", "dir 700"), since the stores are written 0600/0700.
export function treeEntries(root: string, fileDigest: (rel: string, bytes: Buffer) => string = (_rel, bytes) => sha256(bytes),
  alias: (rel: string) => string | null = (rel) => rel): Map<string, string> {
  const out = new Map<string, string>();
  if (!existsSync(root)) return out;
  const walk = (rel: string) => {
    for (const name of readdirSync(join(root, rel)).sort(byCodeUnit)) {
      const r = rel ? `${rel}/${name}` : name;
      const path = join(root, r);
      const stat = lstatSync(path);
      const mode = ` ${(stat.mode & 0o777).toString(8)}`;
      const key = alias(r);
      const set = (entry: string) => { if (key !== null) out.set(key, entry); };
      if (stat.isSymbolicLink()) set(`symlink:${readlinkSync(path)}`);
      else if (stat.isDirectory()) { set(`dir${mode}`); walk(r); }
      else if (!stat.isFile()) set(`special${mode}`);
      else set(`${fileDigest(r, readFileSync(path))}${mode}`);
    }
  };
  walk("");
  return out;
}

// modesDigest hashes "<path> <mode>" for every entry but symlinks (their
// own mode differs across systems): the permission bits treeDigest leaves out.
export function modesDigest(entries: Map<string, string>): string {
  const lines: string[] = [];
  for (const [path, entry] of [...entries].sort(([a], [b]) => byCodeUnit(a, b))) {
    if (!entry.startsWith("symlink:")) lines.push(`${path} ${entry.slice(entry.lastIndexOf(" ") + 1)}`);
  }
  return sha256(lines.join("\n"));
}

// changedFiles lists what one invocation changed, sorted: the new entry, or
// "deleted".
export function changedFiles(before: Map<string, string>, after: Map<string, string>): Record<string, string> {
  const keys = [...new Set([...before.keys(), ...after.keys()])].sort(byCodeUnit);
  const out: Record<string, string> = {};
  for (const k of keys) {
    const now = after.get(k) ?? "deleted";
    if (before.get(k) !== now) out[k] = now;
  }
  return out;
}


// Relocation (Task 15). A writer that records the workspace path inside a
// file (a Git investigation's repository, a session binding's ledgerPath)
// makes that file's digest, and every digest computed over it (the bundle
// catalog, its root, a ledger-bound record's bindingDigest), depend on where
// the case ran. relocation finds those path-dependent digests in the tree
// after an invocation and names each by where it was first derived:
//   file:<rel>               the SHA-256 of workspace file rel
//   json:<rel>#<pointer>     the digestJson of that object or array of rel
//   self:<rel>#<pointer>     a "sha256:<hex>" member equal to the digestJson
//                            of the rest of its object (checkpoint digest,
//                            event hash, bindingDigest)
//   path:<masked path>       the SHA-256 of a case path a recorder logged
//                            (a minimization worktree)
//   worktree:<rel>#<k>       the k-th worktreeDigest member of rel whose
//                            path no recorder logged
// A file is derived from when it holds a case path or a digest found in the
// previous pass (files sorted by code unit, JSON members in document order,
// .git directories skipped); passes repeat until nothing new is found, and
// the first name a digest gets is kept; of a .git directory only
// faultline/recordings is walked. text() masks the case paths
// (normalizeWriter) and then each such digest as <PATHDIGEST:name> (its
// abbreviated page form, <8 hex>...<8 hex>, as <PATHDIGEST:name:short>);
// fileDigest() hashes a path-dependent file's masked text and any other
// file's bytes. The derived digests are also checked directly: the
// generator verifies every bundle TS wrote against the root it printed, the
// Go replay does the same, and TestWriterSameDirectory compares a subset
// unmasked. The Go twin is relocation in difftest/writer_test.go.
// names is each path-dependent digest's name (carriedNames reads the previous
// invocation's).
export type Relocation = { names: Map<string, string>; text: (s: string) => string; fileDigest: (rel: string, bytes: Buffer) => string; alias: (rel: string) => string | null };

// Created Git objects and repaired-run digests (slice 4c Task 2). A case that
// makes a commit records the case directory in its README blob, so the blob,
// the tree and the commit ids vary with where the case ran (the commit also
// with the clock). Each object the case creates is named by its content with
// the case paths masked (objectNames); the digests computed over those ids
// (the repair verifier's executionId and each repaired run's runId) are
// recomputed and named (repairedRunSeeds). The Go twins are in
// difftest/writer_test.go.

// GitBase is what the case's repositories held before its first invocation:
// the object ids of each repository (by directory relative to the workspace)
// and the byte digest of each reflog.
export type GitBase = { objects: Map<string, Set<string>>; logs: Map<string, string> };

// OBJECT_ID is a SHA-1 or SHA-256 object id (the Go twin is objectIDPattern).
const OBJECT_ID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const harnessGitEnv = (): NodeJS.ProcessEnv => ({ PATH: process.env.PATH ?? "", GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null" });

// harnessGit runs the harness's own git (not fl's, and not through the
// recorder: the PATH is the inherited one, as in gitIndexDigest) on the
// repository whose .git directory is gitDir.
function harnessGit(gitDir: string, args: string[]): Buffer | undefined {
  try {
    return execFileSync("git", ["-c", "core.fsmonitor=false", `--git-dir=${gitDir}`, ...args], {
      cwd: gitDir, env: harnessGitEnv(), stdio: ["ignore", "pipe", "ignore"], maxBuffer: 1 << 28
    });
  } catch {
    return undefined;
  }
}

// gitRepos lists the directories under ws, relative to ws ("." for ws
// itself), that hold a .git directory (a linked worktree's .git file does
// not count), sorted. The Go twin is gitRepos.
export function gitRepos(ws: string): string[] {
  const out: string[] = [];
  const walk = (rel: string) => {
    for (const name of readdirSync(join(ws, rel)).sort(byCodeUnit)) {
      const r = rel ? `${rel}/${name}` : name;
      if (!lstatSync(join(ws, r)).isDirectory()) continue;
      if (name === ".git") out.push(rel === "" ? "." : rel);
      else walk(r);
    }
  };
  if (existsSync(ws)) walk("");
  return out;
}

// objectTypes is the id and type of every object repoDir's repository can
// read, except those whose loose file lives in the turn-snapshot store
// (.git/faultline/objects, an alternate of the repository): cat-file
// --batch-all-objects lists them, and they already get snapshotObjectDigest.
// A repository git cannot read lists nothing. The Go twin is objectTypes.
export function objectTypes(repoDir: string): Map<string, string> {
  const gitDir = join(repoDir, ".git");
  const out = new Map<string, string>();
  const listing = harnessGit(gitDir, ["cat-file", "--batch-all-objects", "--batch-check=%(objectname) %(objecttype)"]);
  if (listing === undefined) return out;
  for (const line of listing.toString("utf8").split("\n")) {
    const space = line.indexOf(" ");
    if (space < 0) continue;
    const id = line.slice(0, space);
    if (!OBJECT_ID.test(id) || line.indexOf(" ", space + 1) >= 0) continue;
    if (existsSync(join(gitDir, "faultline", "objects", id.slice(0, 2), id.slice(2)))) continue;
    out.set(id, line.slice(space + 1));
  }
  return out;
}
export const objectSet = (repoDir: string): Set<string> => new Set(objectTypes(repoDir).keys());

// createdObjects maps each repository directory (relative to ws) to the
// objects it holds now that it did not hold in before, id to type.
export function createdObjects(ws: string, before: Map<string, Set<string>>): Map<string, Map<string, string>> {
  const out = new Map<string, Map<string, string>>();
  for (const repo of gitRepos(ws)) {
    for (const [id, type] of objectTypes(join(ws, repo))) {
      if (before.get(repo)?.has(id)) continue;
      if (!out.has(repo)) out.set(repo, new Map());
      out.get(repo)!.set(id, type);
    }
  }
  return out;
}

// REFLOG is a reflog: any file under a logs directory of a .git directory.
const REFLOG = /(?:^|\/)\.git\/(?:[^/]+\/)*logs\/.+$/;
// maskReflogTimes replaces each line's "<seconds> <zone>\t" with "<TIME>\t".
const maskReflogTimes = (text: string): string => text.replace(/[0-9]+ [+-][0-9]{4}\t/g, "<TIME>\t");

// snapshotGit records what the workspace's repositories hold now: call it
// after the case is built and before its first invocation.
export function snapshotGit(ws: string): GitBase {
  const base: GitBase = { objects: new Map(), logs: new Map() };
  for (const repo of gitRepos(ws)) {
    base.objects.set(repo, objectSet(join(ws, repo)));
    const walk = (rel: string) => {
      for (const name of readdirSync(join(ws, rel)).sort(byCodeUnit)) {
        const r = `${rel}/${name}`;
        const stat = lstatSync(join(ws, r));
        if (stat.isDirectory()) walk(r);
        else if (stat.isFile() && REFLOG.test(r)) base.logs.set(r, sha256(readFileSync(join(ws, r))));
      }
    };
    walk(repo === "." ? ".git" : `${repo}/.git`);
  }
  return base;
}

// namedObjects names every created object of one repository, in dependency
// order, and gives each created commit's tree id. Each name is
// <BLOB:h12>, <TREE:h12> or <COMMIT:h12>, h12 being the first 12 hex digits
// of the SHA-256 of "<type>\n" and the object's `git cat-file -p` text with
// the case paths (text) and every already-named id masked. A commit's text
// leaves out its author and committer times. An id that existed before the
// case is never masked. The Go twin is namedObjects.
export function namedObjects(repoDir: string, created: Map<string, string>, text: (s: string) => string): { names: Map<string, string>; commitTree: Map<string, string> } {
  const gitDir = join(repoDir, ".git");
  const names = new Map<string, string>();
  const commitTree = new Map<string, string>();
  const name = (id: string): string => {
    const type = created.get(id);
    if (type === undefined) return id;
    const known = names.get(id);
    if (known !== undefined) return known;
    const raw = harnessGit(gitDir, ["cat-file", "-p", id]);
    if (raw === undefined) throw new Error(`git cat-file -p ${id} in ${repoDir} failed`);
    const body = raw.toString("utf8");
    let label: string;
    let material: string;
    if (type === "blob") {
      label = "BLOB"; material = `blob\n${text(body)}`;
    } else if (type === "tree") {
      label = "TREE";
      const lines = body === "" ? [] : (body.endsWith("\n") ? body.slice(0, -1) : body).split("\n");
      for (const [i, line] of lines.entries()) {
        const tab = line.indexOf("\t");
        if (tab < 0) continue;
        const fields = line.slice(0, tab).split(" ");
        if (fields.length < 3) continue;
        const rest = fields.slice(2).join(" ");
        lines[i] = `${fields[0]} ${fields[1]} ${name(rest)}${line.slice(tab)}`;
      }
      material = `tree\n${text(lines.join("\n"))}`;
    } else if (type === "commit") {
      label = "COMMIT";
      const split = body.indexOf("\n\n");
      const header = split < 0 ? body : body.slice(0, split);
      const message = split < 0 ? "" : body.slice(split + 2);
      const lines = header.split("\n").map((line) => {
        if (line.startsWith("tree ")) { commitTree.set(id, line.slice(5)); return `tree ${name(line.slice(5))}`; }
        if (line.startsWith("parent ")) return `parent ${name(line.slice(7))}`;
        if (line.startsWith("author ") || line.startsWith("committer ")) return line.replace(/ [0-9]+ [+-][0-9]{4}$/, "");
        return line;
      });
      material = `commit\n${text(`${lines.join("\n")}\n\n${message}`)}`;
    } else {
      label = "BLOB"; material = `${type}\n${text(body)}`; // a tag: named by its text like a blob
    }
    const result = `<${label}:${sha256(material).slice(0, 12)}>`;
    names.set(id, result);
    return result;
  };
  for (const id of [...created.keys()].sort(byCodeUnit)) name(id);
  return { names, commitTree };
}

// objectNames maps each created id to its name.
export const objectNames = (repoDir: string, created: Map<string, string>, text: (s: string) => string): Map<string, string> => namedObjects(repoDir, created, text).names;

// The pass-fail fake Docker's stdout for state=good.
const FAKE_DOCKER_PASS_STDOUT = 'state=good\nFAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"}\n';

// repairedRunSeeds computes the digests a repaired run binds to a created
// commit and tree: for each repaired worktree a recorder logged and each
// PASS run fact of a bundle in the workspace, the repair verifier's
// executionId (src/codex-loop.ts:243) named exec:<masked worktree>; for each
// created commit with its tree and each executionId, the runId of attempts 1
// to 3 (src/prevention-from-artifacts.ts:525) named run:<attempt>. A
// candidate that appears in no file is dropped; a wrong one is never named.
// It returns [hex, name] pairs. The Go twin is repairedRunSeeds.
export function repairedRunSeeds(roots: CaseRoots, records: string[], commits: Map<string, string>, texts: Map<string, string>): [string, string][] {
  if (commits.size === 0) return [];
  const parse = (rel: string): any => { try { return JSON.parse(texts.get(rel) ?? ""); } catch { return undefined; } };
  const passes: { policy: string; envPolicy: string; frozen: string; witness: string; environment: string | undefined }[] = [];
  for (const rel of [...texts.keys()].sort(byCodeUnit)) {
    const m = /^(.*\/)?runs\/[^/]+\.json$/.exec(rel);
    if (!m) continue;
    const dir = m[1] ?? "";
    const run = parse(rel);
    if (run?.schemaVersion !== "faultline.git-investigation.v1" || run.result?.verdict !== "PASS" || run.result?.reason !== "PREDICATE_PASS") continue;
    const digests = parse(`${dir}investigation.json`)?.environment?.distinctDigests;
    // A heterogeneous bundle has no single environment digest: the exec:
    // seed still exists (the verifier ran), but no run: seed does.
    const environment = Array.isArray(digests) && digests.length === 1 && typeof digests[0] === "string" ? digests[0] : undefined;
    const frozen = parse(`${dir}witness/frozen.json`);
    const witness = typeof frozen?.witnessDigest === "string" ? frozen.witnessDigest : String(run.witnessDigest ?? "");
    passes.push({ policy: String(run.sandbox?.policyDigest ?? ""), envPolicy: String(run.sandbox?.environmentPolicyDigest ?? ""),
      frozen: String(run.frozenDigest ?? ""), witness, environment });
  }
  const appears = (hex: string): boolean => [...texts.values()].some((text) => text.includes(hex));
  const out: [string, string][] = [];
  const seen = new Set<string>();
  for (const w of loggedCasePaths(records, roots)) {
    for (const p of passes) {
      const execution = digestJson({
        kind: "faultline.repair-verify-execution.v1", executor: "NATIVE_DOCKER", verdict: "PASS", reason: "PREDICATE_PASS",
        policyDigest: p.policy, environmentPolicyDigest: p.envPolicy, exitCode: 0,
        stdoutDigest: `sha256:${sha256(FAKE_DOCKER_PASS_STDOUT)}`, stderrDigest: `sha256:${sha256("")}`, worktree: w, frozenDigest: p.frozen
      });
      if (seen.has(execution) || !appears(execution.slice(7))) continue;
      seen.add(execution);
      out.push([execution.slice(7), `exec:${normalizeWriter(w, roots)}`]);
      if (p.environment === undefined) continue;
      for (const commit of [...commits.keys()].sort(byCodeUnit)) {
        for (let attempt = 1; attempt <= 3; attempt += 1) {
          const run = digestJson({
            schemaVersion: "faultline.prevention-repaired-run.v1", attempt, commit, tree: commits.get(commit), witnessDigest: p.witness,
            environmentDigest: p.environment, executionId: execution, detail: "Frozen witness PREDICATE_PASS on repaired worktree (NATIVE_DOCKER)."
          });
          if (!seen.has(run)) { seen.add(run); out.push([run.slice(7), `run:${attempt}`]); }
        }
      }
    }
  }
  return out;
}

// A Git index (.git/index at any depth) holds stat data (times, inode,
// device) that differs between the TS and the Go copy of a scenario, and
// git rewrites it on a plain `git status` (doctor sets no
// GIT_OPTIONAL_LOCKS). It is compared by what it records instead: the
// SHA-256 of `git ls-files --stage -z` read from it (the raw bytes' SHA-256
// when git cannot read it). The Go twin is gitIndexDigest in
// difftest/writer_test.go.
// A linked worktree's index (.git/worktrees/<name>/index) takes the rule too.
export const isGitIndex = (rel: string): boolean => rel === ".git/index" || rel.endsWith("/.git/index") || /(?:^|\/)\.git\/worktrees\/[^/]+\/index$/.test(rel);
export function gitIndexDigest(gitDir: string, bytes: Buffer): string {
  try {
    const listing = execFileSync("git", ["-c", "core.fsmonitor=false", `--git-dir=${gitDir}`, "ls-files", "--stage", "-z"], {
      cwd: gitDir, env: { PATH: process.env.PATH ?? "", GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null" },
      stdio: ["ignore", "pipe", "ignore"], maxBuffer: 1 << 26
    });
    return sha256(Buffer.concat([Buffer.from("git-index:"), listing]));
  } catch {
    return sha256(bytes);
  }
}
// A loose object in the turn-snapshot store (.git/faultline/objects/xx/…,
// slice 3a Task 10) is zlib-compressed by git, so its bytes depend on the
// zlib build git links (macOS, Linux); its name is the object id of what it
// holds, so it is compared by that name. The Go twin is snapshotObjectDigest
// in difftest/writer_test.go.
const SNAPSHOT_OBJECT = /(?:^|\/)\.git\/faultline\/objects\/([0-9a-f]{2})\/([0-9a-f]{38}|[0-9a-f]{62})$/;
export function snapshotObjectDigest(rel: string): string | undefined {
  const m = SNAPSHOT_OBJECT.exec(rel);
  return m ? sha256(`git-object:${m[1]}${m[2]}`) : undefined;
}

// ---- Created Git objects (slice 4b Task 1; the same rule as slice 4c Task 2) ----
// A command that writes Git objects into a workspace repository (the turn
// minimization bridge's `commit-tree`, which takes its author and committer
// time from the wall clock) gives ids that differ between runs, and so does
// every file and digest that names them. The rule:
//   Listing. For each .git directory of the workspace, `git cat-file
//     --batch-all-objects --batch-check='%(objectname) %(objecttype)'` runs
//     before the case's first invocation and after each one, with the
//     harness's own git environment (no argv log, as gitIndexDigest). The
//     new ids are the difference. An object whose loose file lies in the
//     turn-snapshot store (.git/faultline/objects, which batch-all-objects
//     reaches through objects/info/alternates) is not the repository's own:
//     snapshotObjectDigest already compares it by name, and the ids fl
//     prints for it stay as they are.
//   Naming, from `git cat-file -p` on that side, blobs then trees then
//     commits (a referenced created object is named first). Each object is
//     named from text in which the case paths (text()) and every
//     already-named id are masked:
//       blob    <BLOB:h12>   sha256("blob\n" + content)
//       tree    <TREE:h12>   sha256("tree\n" + the "<mode> <type> <id or
//                            name>\t<name>" entries joined with "\n")
//       commit  <COMMIT:h12> sha256("commit\n" + "tree <name>", each
//                            "parent <name or id>", the author and committer
//                            lines without their "<seconds> <zone>", "", the
//                            message)
//     h12 is the first 12 hex digits. A commit with a wrong tree, parent,
//     identity or message gets a different name (spec 2.2).
//   Unmasked ids. An id that existed before the case is never masked.
//   Loose objects. A created object's loose file (<repo>/.git/objects/xx/…)
//     is hashed as sha256("git-object:" + its name); loose objects that
//     existed before keep their byte digest.
//   Reflogs. A .git/**/logs/** file that is new or changed has each line's
//     "<seconds> <zone>\t" replaced with "<TIME>\t" (and its text masked)
//     before hashing.
// The ids are also relocation seeds, so a file that holds one, and every
// digest over it, gets a <PATHDIGEST:…> name. The Go twins are in
// difftest/writer_test.go.
// checkObjectNames is TestBridgeCommitNamesIgnoreTimeButNotTreeOrMessage's
// twin, run on start-up: two bridge commits of one tree and message made at
// different times get one name, another message another, and the tree that
// existed before is not named.
function checkObjectNames(): void {
  const repoDir = mkdtempSync(join(tmpdir(), "faultline-object-names-"));
  try {
    const git = (args: string[], input = "", date = "1700000000 +0000"): string => execFileSync("git", ["-c", "user.name=FaultLine", "-c", "user.email=faultline@example.invalid", ...args], {
      cwd: repoDir, input, env: { ...harnessGitEnv(), GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date }, stdio: ["pipe", "pipe", "ignore"]
    }).toString("utf8").trim();
    git(["init", "-q"]);
    const blob = git(["hash-object", "-w", "--stdin"], "bad\n");
    const tree = git(["mktree"], `100644 blob ${blob}\tstate.txt\n`);
    const before = new Map([[".", objectSet(repoDir)]]);
    const message = `faultline-turn-min after t1 (${tree.slice(0, 12)})`;
    const c1 = git(["commit-tree", tree, "-m", message]);
    const c2 = git(["commit-tree", tree, "-m", message], "", "1700000001 +0100");
    const c3 = git(["commit-tree", tree, "-m", `faultline-turn-min before t0 (${tree.slice(0, 12)})`]);
    const created = createdObjects(repoDir, before).get(".") ?? new Map<string, string>();
    const names = objectNames(repoDir, created, (s) => s);
    if (c1 === c2 || !names.get(c1)?.startsWith("<COMMIT:") || names.get(c1) !== names.get(c2)) throw new Error(`checkObjectNames: c1 ${names.get(c1)} c2 ${names.get(c2)}`);
    if (names.get(c3) === names.get(c1)) throw new Error("checkObjectNames: a different message must give a different name");
    if (names.has(tree) || names.has(blob)) throw new Error("checkObjectNames: an object that existed before the case is never masked");
    // The pinned names (TestCreatedObjectNamesArePinned pins the same):
    // every object created, the message holding a path text() masks.
    const pinned = mkdtempSync(join(tmpdir(), "faultline-object-names-"));
    try {
      const pin = (args: string[], input = ""): string => execFileSync("git", ["-c", "user.name=FaultLine", "-c", "user.email=faultline@example.invalid", ...args], {
        cwd: pinned, input, env: harnessGitEnv(), stdio: ["pipe", "pipe", "ignore"]
      }).toString("utf8").trim();
      pin(["init", "-q"]);
      const empty = new Map([[".", objectSet(pinned)]]);
      const b = pin(["hash-object", "-w", "--stdin"], "bad\n");
      const tr = pin(["mktree"], `100644 blob ${b}\tstate.txt\n`);
      const c = pin(["commit-tree", tr, "-m", `m ${pinned}`]);
      const got = objectNames(pinned, createdObjects(pinned, empty).get(".") ?? new Map<string, string>(), (s) => s.split(pinned).join("<WS>"));
      const want: [string, string][] = [[b, "<BLOB:bf9ddff4bbf5>"], [tr, "<TREE:b352e6817bf5>"], [c, "<COMMIT:c43d2f3c0662>"]];
      for (const [id, name] of want) if (got.get(id) !== name) throw new Error(`checkObjectNames: ${id} is ${got.get(id)}, want ${name}`);
    } finally {
      rmSync(pinned, { recursive: true, force: true });
    }
  } finally {
    rmSync(repoDir, { recursive: true, force: true });
  }
}
// ---- end of the created Git objects rule ----

const SELF_DIGEST = /^sha256:[0-9a-f]{64}$/;
// A minimization run fact's worktreeDigest member, in canonical or indented
// JSON (the Go twin is worktreeDigestMember).
const WORKTREE_DIGEST = /"worktreeDigest":\s*"sha256:([0-9a-f]{64})"/g;
const pointerToken = (key: string | number): string => String(key).split("~").join("~0").split("/").join("~1");

// loggedCasePaths are the paths under the case root the recorders logged
// (every `git -C <worktree>` and `--mount src=<worktree>` argument), in log
// order. The Go twin is loggedCasePaths in difftest/writer_test.go.
export function loggedCasePaths(records: string[], roots: CaseRoots): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const consider = (s: string): void => {
    if (s.startsWith(roots.root) && !seen.has(s)) { seen.add(s); out.push(s); }
  };
  for (const record of records) {
    for (const arg of record.split("\x1f")) {
      consider(arg);
      for (const part of arg.split(",")) {
        const index = part.indexOf("=");
        if (index >= 0) consider(part.slice(index + 1));
      }
    }
  }
  return out;
}

// carriedNames: for invocation inv's flip-byte and replace-text mutations
// ONLY (rename, chmod and the rest carry nothing), the previous invocation's
// names of the mutated file itself (file:<rel>, json:<rel>#..., self:<rel>#...),
// prefixed "prior:". The Go twin is carriedNames in difftest/writer_test.go.
export function carriedNames(prev: Map<string, string>, mutations: Template[], inv: number, roots: CaseRoots): Map<string, string> {
  const out = new Map<string, string>();
  for (const m of mutations) {
    if (m.inv !== inv || (m.op !== "flip-byte" && m.op !== "replace-text")) continue;
    const file = substitute(m.file!, roots);
    for (const [d, n] of prev) {
      if (n === `file:${file}` || n.startsWith(`json:${file}#`) || n.startsWith(`self:${file}#`)) out.set(d, `prior:${n}`);
    }
  }
  return out;
}

// openAIRequestInputs are the input strings of the request bodies the fake
// OpenAI server recorded ("" for a body that is not JSON or has no string
// input), in log order. The Go twin is openAIRequestInputs in
// difftest/writer_test.go.
export function openAIRequestInputs(records: string[]): string[] {
  return records.filter((record) => record.startsWith("openai\x1f")).map((record) => {
    const body = record.split("\x1f").slice(5).join("\x1f");
    try {
      const input = (JSON.parse(body) as { input?: unknown }).input;
      return typeof input === "string" ? input : "";
    } catch {
      return "";
    }
  });
}

const WORKTREE_GITDIR = /(?:^|\/)\.git\/worktrees\/[^/]+\/gitdir$/;
// WORKTREE_REFS is the empty refs directory that newer Git versions (2.54)
// make in a linked worktree's administrative directory and older ones (2.39)
// do not: Git's own layout, left out of the comparison.
const WORKTREE_REFS = /(?:^|\/)\.git\/worktrees\/[^/]+\/refs$/;
const LOOSE_OBJECT = /^(?:(.*)\/)?\.git\/objects\/([0-9a-f]{2})\/([0-9a-f]{38}|[0-9a-f]{62})$/;
const FAN_OUT =/^(?:(.*)\/)?\.git\/objects\/([0-9a-f]{2})$/;
// relocation's carried argument: digest hex -> name of digests that named a
// root-dependent file or value BEFORE an invocation's flip-byte/replace-text
// edit of that file (carriedNames). Bundle artifacts that still cite the
// pre-edit digest (hashes.txt, manifest.json) would otherwise hold an unnamed
// root-dependent digest, so Go and TS (whose roots differ in length) would
// disagree on their file digests. A carried digest is named only when the
// current tree does not already name it, and always "prior:"-prefixed, so a
// stale checksum and a rewritten one (the new actual digest, "file:...")
// stay distinguishable. The Go twin is relocationCarrying.
export function relocation(roots: CaseRoots, records: string[] = [], base?: GitBase, carried?: Map<string, string>): Relocation {
  const files: [string, Buffer, string][] = [];
  const walk = (rel: string) => {
    for (const name of readdirSync(join(roots.ws, rel)).sort(byCodeUnit)) {
      const r = rel ? `${rel}/${name}` : name;
      const stat = lstatSync(join(roots.ws, r));
      // Only the sidecar recordings of a .git are walked: they hold the case
      // path (workingDirectory, repositoryRoot) and digests over it.
      if (stat.isDirectory() && name === ".git") { if (isRealDir(join(roots.ws, r, "faultline")) && isRealDir(join(roots.ws, r, "faultline", "recordings"))) walk(`${r}/faultline/recordings`); }
      else if (stat.isDirectory()) walk(r);
      else if (stat.isFile()) { const bytes = readFileSync(join(roots.ws, r)); files.push([r, bytes, bytes.toString("utf8")]); }
    }
  };
  if (existsSync(roots.ws)) walk("");
  const names = new Map<string, string>();
  const ids = new Map<string, string>(); // created object id -> its name
  const commits = new Map<string, string>(); // created commit -> its tree
  const created = base ? createdObjects(roots.ws, base.objects) : new Map<string, Map<string, string>>();
  for (const [repo, objects] of [...created].sort(([a], [b]) => byCodeUnit(a, b))) {
    const named = namedObjects(join(roots.ws, repo), objects, (s) => normalizeWriter(s, roots));
    for (const [id, name] of named.names) ids.set(id, name);
    for (const [commit, tree] of named.commitTree) commits.set(commit, tree);
  }
  const idKeys = [...ids.keys()].sort(byCodeUnit);
  let found: string[] = [];
  const add = (hex: string, name: string) => { if (!names.has(hex)) { names.set(hex, name); found.push(hex); } };
  const tainted = (text: string) => text.includes(roots.root) || [...names.keys()].some((d) => text.includes(d)) || idKeys.some((id) => text.includes(id));
  const visit = (value: unknown, rel: string, pointer: string): void => {
    if (value === null || typeof value !== "object") return;
    if (!tainted(canonicalJson(value))) return;
    add(digestJson(value).slice(7), `json:${rel}#${pointer}`);
    if (!Array.isArray(value)) {
      const record = value as Record<string, unknown>;
      for (const [key, member] of Object.entries(record)) {
        if (typeof member !== "string" || !SELF_DIGEST.test(member)) continue;
        const { [key]: _self, ...rest } = record;
        if (digestJson(rest) === member) add(member.slice(7), `self:${rel}#${pointer}/${pointerToken(key)}`);
      }
    }
    for (const [key, member] of Object.entries(value)) visit(member, rel, `${pointer}/${pointerToken(key)}`);
  };
  // A minimization run fact hashes its materialized worktree path, which
  // appears in no file: name those digests after the masked path.
  // The ids of the Git objects the case created (a bridge commit's id holds
  // the wall-clock time) are seeds too: a file that holds one, and every
  // digest over it, gets a name.
  const seeds = [roots.root];
  for (const path of loggedCasePaths(records, roots)) {
    const digest = sha256(Buffer.from(path, "utf8"));
    add(digest, `path:${normalizeWriter(path, roots)}`);
    seeds.push(digest);
  }
  // The Git objects the case created are seeds (named by objectNames in
  // text), and so are the repaired-run digests computed from them.
  seeds.push(...idKeys);
  for (const [hex, name] of repairedRunSeeds(roots, records, commits, new Map(files.map(([rel, , text]) => [rel, text])))) {
    add(hex, name);
    seeds.push(hex);
  }
  // A worktree no recorder logged (a fault before git ran in it) is still
  // hashed into its run fact: name each worktreeDigest value not named
  // above after where it appears (round 7 #15).
  for (const [rel, , text] of files) {
    let k = 0;
    for (const m of text.matchAll(WORKTREE_DIGEST)) {
      if (!names.has(m[1]!)) { add(m[1]!, `worktree:${rel}#${k}`); seeds.push(m[1]!); }
      k += 1;
    }
  }
  // The fake OpenAI server's records hold the request body, whose input is
  // the evidence packet: its packetDigest covers the path-dependent
  // investigationDigest, and a failed request leaves the packet in no file.
  // Each is walked like a file's JSON as openai:<k> (the k-th openai
  // record), without a file: name.
  const requests = openAIRequestInputs(records);
  const expand = (start: string[]): void => {
    let frontier = start;
    while (frontier.length > 0) {
      found = [];
      for (const [rel, bytes, text] of files) {
        if (!frontier.some((needle) => text.includes(needle))) continue;
        add(sha256(bytes), `file:${rel}`);
        try { visit(JSON.parse(text), rel, ""); } catch { /* not JSON */ }
      }
      for (const [k, text] of requests.entries()) {
        if (!frontier.some((needle) => text.includes(needle))) continue;
        try { visit(JSON.parse(text), `openai:${k}`, ""); } catch { /* not JSON */ }
      }
      frontier = found;
    }
  };
  expand(seeds);
  // Second pass: carried digests the current tree does not name.
  found = [];
  for (const d of [...(carried?.keys() ?? [])].sort(byCodeUnit)) add(d, carried!.get(d)!);
  expand(found);
  const sorted = [...names.keys()].sort(byCodeUnit);
  const text = (s: string): string => {
    let out = normalizeWriter(s, roots);
    for (const id of idKeys) out = out.split(id).join(ids.get(id)!);
    for (const d of sorted) out = out.split(d).join(`<PATHDIGEST:${names.get(d)}>`);
    // The proof page also prints a digest abbreviated (git-proof-view.ts
    // shortDigest: its first and last 8 hex digits).
    for (const d of sorted) out = out.split(`${d.slice(0, 8)}...${d.slice(-8)}`).join(`<PATHDIGEST:${names.get(d)}:short>`);
    return out;
  };
  // alias names the entries whose paths hold a created object's id: a loose
  // object the case created is renamed to its content name; an object that
  // existed before the case, or one in the turn-snapshot store, keeps its
  // path. Two Git-layout entries are left out, each only where its exact
  // condition holds: a fan-out directory .git/objects/<xx> that no object
  // from before the case lives in and that holds nothing but created loose
  // objects (its name is an id prefix), and the EMPTY directory
  // .git/worktrees/<name>/refs that Git 2.54 makes in a linked worktree's
  // administrative directory and the pinned 2.39.5 does not. A non-empty
  // refs directory, a file or symlink of that name, and every other path
  // stay compared. The Go twin is relocated.alias.
  const alias = (rel: string): string | null => {
    const abs = join(roots.ws, rel);
    if (WORKTREE_REFS.test(rel)) {
      return lstatSync(abs).isDirectory() && readdirSync(abs).length === 0 ? null : rel;
    }
    const loose = LOOSE_OBJECT.exec(rel);
    if (loose && ids.has(`${loose[2]}${loose[3]}`) && created.get(loose[1] ?? ".")?.has(`${loose[2]}${loose[3]}`)) {
      return `${loose[1] === undefined ? "" : `${loose[1]}/`}.git/objects/${ids.get(`${loose[2]}${loose[3]}`)}`;
    }
    const fan = FAN_OUT.exec(rel);
    if (fan && base) {
      const repo = fan[1] ?? ".";
      if ([...(base.objects.get(repo) ?? [])].some((id) => id.startsWith(fan[2]!))) return rel;
      const names = readdirSync(abs);
      if (names.length > 0 && names.every((name) => created.get(repo)?.has(`${fan[2]}${name}`))) return null;
    }
    return rel;
  };
  return {
    names,
    text,
    alias,
    fileDigest: (rel, bytes) => {
      if (isGitIndex(rel)) return gitIndexDigest(dirname(join(roots.ws, rel)), bytes);
      const object = snapshotObjectDigest(rel);
      if (object !== undefined) return object;
      const loose = LOOSE_OBJECT.exec(rel);
      if (loose && ids.has(`${loose[2]}${loose[3]}`) && created.get(loose[1] ?? ".")?.has(`${loose[2]}${loose[3]}`)) {
        return sha256(`git-object:${ids.get(`${loose[2]}${loose[3]}`)}`);
      }
      if (base && REFLOG.test(rel) && base.logs.get(rel) !== sha256(bytes)) return sha256(text(maskReflogTimes(bytes.toString("utf8"))));
      // A linked worktree's gitdir file holds the absolute path of the
      // worktree's .git file (slice 4c Task 20, --keep-worktree): hashed by
      // its text with the case paths masked. The Go twin is fileDigest in
      // difftest/writer_test.go.
      if (WORKTREE_GITDIR.test(rel)) return sha256(text(bytes.toString("utf8")));
      return names.has(sha256(bytes)) ? sha256(text(bytes.toString("utf8"))) : sha256(bytes);
    }
  };
}

const isRealDir = (path: string): boolean => { try { return lstatSync(path).isDirectory(); } catch { return false; } };

// readArgvLog splits the recorders' log into records ("git a\x1fb").
export function readArgvLog(path: string): string[] {
  if (!existsSync(path)) return [];
  const records = readFileSync(path, "utf8").split("\x1e");
  records.pop(); // the text after the last terminator (empty)
  return records;
}

// sortVersionRecordRuns sorts each run of consecutive `--version` records
// (a record whose last argument is --version): doctor starts git, node and
// docker --version together (Promise.all), so the order the recorders
// append them in is racy on both sides, and `fl init --yes --cli` runs
// doctor twice. Nothing else is reordered. The Go twin is
// sortVersionRecordRuns in difftest/writer_test.go.
export function isVersionRecord(record: string): boolean {
  return record.slice(record.indexOf(" ") + 1).split("\x1f").at(-1) === "--version";
}
export function sortVersionRecordRuns(records: string[]): string[] {
  const out = [...records];
  for (let start = 0; start < out.length;) {
    let end = start;
    while (end < out.length && isVersionRecord(out[end]!)) end += 1;
    if (end > start) out.splice(start, end - start, ...out.slice(start, end).sort(byCodeUnit));
    start = end + 1;
  }
  return out;
}

// normalizeRecord normalizes each argument of a record on its own, so a mask
// that widens to the previous boundary (<GITTMP>) stays inside its argument.
export function normalizeRecord(record: string, roots: CaseRoots, reloc?: Relocation): string {
  return record.split("\x1f").map((arg) => (reloc ? reloc.text(arg) : normalizeWriter(arg, roots))).join("\x1f");
}

// writtenBundle is the proof bundle directory an `fl investigate git`
// invocation reports on stdout, or undefined when it wrote none.
export function writtenBundle(stdout: string): string | undefined {
  try {
    const directory = (JSON.parse(stdout) as { proofBundle?: { directory?: unknown } | null }).proofBundle?.directory;
    return typeof directory === "string" ? directory : undefined;
  } catch {
    return undefined;
  }
}

// writtenPrevention is the package an `fl prevention write` invocation
// reports on stdout (its JSON, then the claim line): the directory and
// root digest, or undefined when it wrote none. The Go twin is
// writtenPrevention in difftest/writer_test.go.
export function writtenPrevention(stdout: string): { directory: string; rootDigest: string } | undefined {
  const claim = /\n(?:Prevention verified|Prevention evidence summary)\n$/.exec(stdout);
  if (!claim) return undefined;
  try {
    const v = JSON.parse(stdout.slice(0, claim.index + 1)) as { status?: unknown; directory?: unknown; rootDigest?: unknown };
    if (typeof v.status !== "string" || !v.status.startsWith("PREVENTION_") || typeof v.directory !== "string" || typeof v.rootDigest !== "string") return undefined;
    return { directory: v.directory, rootDigest: v.rootDigest };
  } catch {
    return undefined;
  }
}

// parsedObject is stdout parsed as a JSON object, or undefined.
function parsedObject(stdout: string): Record<string, unknown> | undefined {
  try {
    const v = JSON.parse(stdout) as unknown;
    return v !== null && typeof v === "object" && !Array.isArray(v) ? v as Record<string, unknown> : undefined;
  } catch {
    return undefined;
  }
}

// writtenAttestation is the receipt an `fl attest create` invocation
// reports (status ATTESTED): its path and receipt digest. The Go twin is
// writtenAttestation in difftest/writer_test.go.
export function writtenAttestation(stdout: string): { path: string; receiptDigest: string } | undefined {
  const v = parsedObject(stdout);
  if (v?.status !== "ATTESTED" || typeof v.path !== "string" || typeof v.receiptDigest !== "string") return undefined;
  return { path: v.path, receiptDigest: v.receiptDigest };
}

// writtenRepairBrief is the package an `fl repair brief` invocation reports
// (status PERSISTED): its directory and artifact digest. The Go twin is
// writtenRepairBrief in difftest/writer_test.go.
export function writtenRepairBrief(stdout: string): { directory: string; artifactDigest: string } | undefined {
  const v = parsedObject(stdout);
  if (v?.status !== "PERSISTED" || typeof v.directory !== "string" || typeof v.artifactDigest !== "string") return undefined;
  return { directory: v.directory, artifactDigest: v.artifactDigest };
}

// writtenProvenanceReceipt is the receipt an `fl provenance create`
// invocation reports (status AWAITING_GITHUB_ARTIFACT_ATTESTATION). The Go
// twin is writtenProvenanceReceipt in difftest/writer_test.go.
export function writtenProvenanceReceipt(stdout: string): { path: string } | undefined {
  const v = parsedObject(stdout);
  if (v?.status !== "AWAITING_GITHUB_ARTIFACT_ATTESTATION" || typeof v.path !== "string") return undefined;
  return { path: v.path };
}

// writtenRepairPrevention is the prevention package an `fl repair --bundle`
// invocation reports (preventionExport with ok: true): its directory and
// root digest. The Go twin is writtenRepairPrevention in
// difftest/writer_test.go.
export function writtenRepairPrevention(stdout: string): { directory: string; rootDigest: string } | undefined {
  const exported = parsedObject(stdout)?.preventionExport as Record<string, unknown> | null | undefined;
  if (exported === null || typeof exported !== "object" || exported.ok !== true || typeof exported.directory !== "string" || typeof exported.rootDigest !== "string") return undefined;
  return { directory: exported.directory, rootDigest: exported.rootDigest };
}

// checkDetectors is the start-up self-check of the artifact detectors (the
// Go twin is TestWrittenArtifactDetectors in difftest/fakes_test.go).
function checkDetectors(): void {
  const attestation = writtenAttestation("{\n  \"status\": \"ATTESTED\",\n  \"path\": \"/s/r1.json\",\n  \"receiptDigest\": \"sha256:aa\"\n}\n");
  if (attestation?.path !== "/s/r1.json" || attestation.receiptDigest !== "sha256:aa") throw new Error("writtenAttestation self-check failed");
  if (writtenRepairBrief("{\"status\":\"PERSISTED\"}\n") !== undefined) throw new Error("writtenRepairBrief self-check failed: no directory is not a brief");
  const prevention = writtenRepairPrevention(`{"status":"REPAIR_CANDIDATE_VERIFIED","preventionExport":{"ok":true,"directory":"/p","rootDigest":"sha256:bb"}}`);
  if (prevention?.directory !== "/p" || prevention.rootDigest !== "sha256:bb") throw new Error("writtenRepairPrevention self-check failed");
  if (writtenRepairPrevention(`{"preventionExport":{"ok":false,"reasons":["x"]}}`) !== undefined) throw new Error("writtenRepairPrevention self-check failed: a failed export is not a package");
}

// optionValue is the value after flag in argv (cli-app.ts option), or
// undefined.
function optionValue(argv: string[], flag: string): string | undefined {
  const index = argv.indexOf(flag);
  return index >= 0 ? argv[index + 1] : undefined;
}

// crossVerify runs TS `fl verify` on every bundle (slice 2 spec 2.4) and
// every prevention package TS wrote, and the TS verifier of every
// attestation receipt, repair brief, provenance receipt (with fake gh
// `pass`) and repair-written prevention package; TestWriterReplay does the
// same with TS on the ones Go wrote. It runs after the invocation's argv log
// is read, so its git and gh calls are not recorded. argv is the
// invocation's, after substitution. The Go twin is crossVerify in
// difftest/writer_test.go.
async function crossVerify(id: string, inv: number, roots: CaseRoots, stdout: string, argv: string[]): Promise<void> {
  const fail = (what: string, r: { exit: number; stdout: string; stderr: string }) => new Error(`${id}|${inv}: TS ${what} TS wrote failed (exit ${r.exit})\n${r.stdout}${r.stderr}`);
  const env = (fakes: WriterFakes = {}) => writerEnv(roots, `${id}:verify`, undefined, {}, undefined, undefined, fakes);
  const prevention = writtenPrevention(stdout) ?? writtenRepairPrevention(stdout);
  const directory = prevention?.directory ?? writtenBundle(stdout);
  if (directory !== undefined) {
    const root = prevention?.rootDigest ?? (JSON.parse(stdout) as { proofBundle: { rootDigest: string } }).proofBundle.rootDigest;
    const r = await runCli(roots, ["verify", directory, "--expect-root", root], env());
    if (r.exit !== 0 || !r.stdout.startsWith("Integrity: VALID\n") || !r.stdout.includes("\nExternal root: MATCH\n")) throw fail("verify of the bundle", r);
  }
  const attestation = writtenAttestation(stdout);
  if (attestation !== undefined) {
    const receiptId = basename(attestation.path).replace(/\.json$/, "");
    const r = await runCli(roots, ["attest", "verify", receiptId, "--store", dirname(attestation.path), "--expect-digest", attestation.receiptDigest], env());
    if (r.exit !== 0) throw fail("attest verify of the receipt", r);
  }
  const brief = writtenRepairBrief(stdout);
  if (brief !== undefined) {
    const r = await runCli(roots, ["repair", "verify", brief.directory, "--expect-digest", brief.artifactDigest], env());
    if (r.exit !== 0 || !r.stdout.includes('"valid": true')) throw fail("repair verify of the brief", r);
  }
  const receipt = writtenProvenanceReceipt(stdout);
  if (receipt !== undefined) {
    const bundle = optionValue(argv, "--bundle");
    if (bundle === undefined) throw new Error(`${id}|${inv}: a provenance receipt without --bundle in the argv`);
    const r = await runCli(roots, ["provenance", "verify", "--bundle", bundle, "--receipt", receipt.path, "--trust", join(roots.ws, "trust.json"),
      "--attestation-bundle", join(roots.ws, "attestation.sigstore.json")], env({ fakeGh: "pass" }));
    if (r.exit !== 0) throw fail("provenance verify of the receipt", r);
  }
}

// BUSY_PORT in an argv string is the port of a listener the case holds on
// 127.0.0.1 while it runs (judge-proof-port-in-use); the Go twin is
// holdBusyPort in difftest/writer_test.go.
export const BUSY_PORT = "<BUSYPORT>";

// An argv string that is exactly <FILE:rel> becomes the trimmed text of the
// workspace file rel when the invocation starts: a bundle's ROOT.sha256
// depends on the case directory, so a later invocation that must pass
// --expect-root reads it (the Go twin is fileArgument in writer_test.go).
export function fileArgument(arg: string, roots: CaseRoots): string {
  const m = /^<FILE:([^>]+)>$/.exec(arg);
  return m ? readFileSync(join(roots.ws, m[1]!), "utf8").trim() : arg;
}

// invocationEnv is a template's env for one invocation: <WS>, <TMP> and
// <HOME> substituted, and a value that is exactly <FILE:rel> replaced by
// the trimmed text of the workspace file rel when the invocation starts, as
// fileArgument does for argv (a provenance case's GITHUB_SHA). The Go twin
// is the env loop in writerEnv, difftest/writer_test.go.
export function invocationEnv(t: WriterTemplate, roots: CaseRoots): Record<string, string> {
  return Object.fromEntries(Object.entries(t.env ?? {}).map(([k, v]) => [k, fileArgument(substitute(v, roots), roots)]));
}

// maskCodexPath masks, in a fake codex record's PATH= field, the harness's
// inherited PATH as <PATH>, then the case root as <ROOT> and the
// repository as <REPO> (the recorder, fake codex and fake tool
// directories): fl passes codex its whole PATH, which differs between
// machines. Nothing else is touched. The Go twin is maskCodexPath in
// difftest/writer_test.go.
export function maskCodexPath(record: string, roots: CaseRoots, repoDir: string, inheritedPath: string): string {
  if (!record.startsWith("codex ")) return record;
  return record.split("\x1f").map((arg) => {
    if (!arg.startsWith("PATH=")) return arg;
    let value = arg.slice("PATH=".length);
    if (inheritedPath !== "") value = value.split(inheritedPath).join("<PATH>");
    return `PATH=${value.split(roots.root).join("<ROOT>").split(repoDir).join("<REPO>")}`;
  }).join("\x1f");
}

// An argv string that is exactly <STDOUT:n:/json/pointer> becomes the
// string at that JSON pointer of the case's invocation n's stdout (an
// earlier invocation): a project build plan's digest covers the absolute
// context directory, so `fl runtime project build --expect-plan` takes it
// from the plan invocation (the Go twin is stdoutArgument in writer_test.go).
export function stdoutArgument(arg: string, stdouts: string[]): string {
  const m = /^<STDOUT:([0-9]+):(\/[^>]*)>$/.exec(arg);
  if (!m) return arg;
  let value: unknown = JSON.parse(stdouts[Number(m[1])]!);
  for (const token of m[2]!.slice(1).split("/")) {
    value = (value as Record<string, unknown>)[token.split("~1").join("/").split("~0").join("~")];
  }
  if (typeof value !== "string") throw new Error(`${arg}: not a string in invocation ${m[1]}'s stdout`);
  return value;
}

// A project build plan's planDigest (src/runtime.ts:481) covers the
// absolute context directory and Dockerfile, so it differs between the TS
// and the Go case directory. maskPlanDigests replaces it with <PLANDIGEST>
// in text, but only where it is the digestJson of the plan fields printed
// beside it in stdout (every object with kind PROJECT_RUNTIME_IMAGE_BUILD):
// a wrong digest stays unmasked and fails the comparison. The Go twin is
// maskPlanDigests in writer_test.go.
export function planDigests(stdout: string): string[] {
  let parsed: unknown;
  try { parsed = JSON.parse(stdout); } catch { return []; }
  const out: string[] = [];
  const visit = (value: unknown): void => {
    if (value === null || typeof value !== "object") return;
    const plan = value as Record<string, unknown>;
    const review = plan.review as Record<string, unknown> | undefined;
    if (plan.kind === "PROJECT_RUNTIME_IMAGE_BUILD" && review !== null && typeof review === "object" && typeof review.planDigest === "string") {
      const digest = digestJson({
        schema: "faultline.project-build-plan.v1", contextDirectory: plan.contextDirectory, dockerfile: plan.dockerfile,
        imageTag: plan.imageTag, network: plan.network, contextDigest: review.contextDigest, dockerfileDigest: review.dockerfileDigest
      });
      if (digest === review.planDigest) out.push(digest);
    }
    for (const member of Object.values(value)) visit(member);
  };
  visit(parsed);
  return out;
}
export function maskPlanDigests(text: string, digests: string[]): string {
  let out = text;
  for (const d of digests) out = out.split(d).join("<PLANDIGEST>");
  return out;
}

// HOLD_PORT in a template's env names a fixed port the case holds on
// 127.0.0.1 while it runs (a command's default port: judge-proof's 4174);
// if another process already holds it, it is busy all the same. The Go
// twin is holdPortVariable in difftest/writer_test.go.
export const HOLD_PORT = "FAULTLINE_TEST_HOLD_PORT";

async function listenOn(port: number): Promise<Server | undefined> {
  const server = createServer();
  return new Promise((resolveListen) => {
    server.once("error", () => resolveListen(undefined));
    server.listen(port, "127.0.0.1", () => resolveListen(server));
  });
}

async function holdBusyPort(t: WriterTemplate): Promise<{ port: string; servers: Server[] }> {
  const servers: Server[] = [];
  const fixed = t.env?.[HOLD_PORT];
  if (fixed !== undefined) {
    const held = await listenOn(Number(fixed));
    if (held) servers.push(held);
  }
  if (!t.argv.some((argv) => argv.some((a) => a.includes(BUSY_PORT)))) return { port: "", servers };
  const server = await listenOn(0);
  if (!server) throw new Error(`${t.id}: cannot listen on 127.0.0.1:0`);
  servers.push(server);
  return { port: String((server.address() as { port: number }).port), servers };
}

// tmpFailures collects the invocations that left files in TMPDIR; the run
// fails after every case has run, naming them all.
const tmpFailures: string[] = [];

async function runCase(t: WriterTemplate): Promise<string[]> {
  const roots = makeCaseRoots();
  const busy = await holdBusyPort(t);
  let openAI: FakeOpenAI | undefined;
  try {
    if (t.fakeOpenAI !== undefined) openAI = await startFakeOpenAI(roots.ws, t.fakeOpenAI, roots.argvLog);
    const fakes: WriterFakes = { fakeCodex: t.fakeCodex, fakeCodexOnPath: t.fakeCodexOnPath, fakeGh: t.fakeGh, openAIURL: openAI?.url };
    copyScenario(t.scenario, roots.ws);
    // chmod mutations run after resetModes, which would undo them (the Go
    // twin is prepareWriterCase). A mutation with inv runs just before that
    // invocation instead, and only setup mutations are reset.
    const mutations = (t.mutations ?? []).map((template) => substituteDeep(template, roots) as Template);
    const apply = (m: Template) => applyMutation(roots.ws, { id: t.id, base: "writer", template: m, file: PATH_OPS.has(m.op) ? m.path ?? "." : m.file! });
    const setup = mutations.filter((m) => m.inv === undefined);
    for (const m of setup) if (m.op !== "chmod") apply(m);
    resetModes(roots.ws);
    restoreScenarioModes(t.scenario, roots.ws);
    refreshScenarioIndexes(t.scenario, roots.ws);
    // A read-only directory refuses writes only to a non-root user: a golden
    // generated as root would drift.
    if (mutations.some((m) => m.op === "chmod") && process.getuid?.() === 0) throw new Error(`${t.id}: chmod cases must be generated by a non-root user`);
    for (const m of setup) if (m.op === "chmod") apply(m);
    const lines: string[] = [];
    // The recorded paths accumulate over the case's invocations: a later
    // invocation must keep masking the worktree digests an earlier one
    // wrote into a file (the Go twin is runWriterCase).
    const records: string[] = [];
    const stdouts: string[] = [];
    // What the repositories hold before the first invocation (the Go twin
    // is snapshotGit in runWriterCaseWith).
    const base = snapshotGit(roots.ws);
    let prevNames = new Map<string, string>(); // the previous invocation's names (carriedNames)
    for (const [inv, argv] of t.argv.entries()) {
      // This invocation's mutations: the others, then the new entries' modes
      // reset, then the chmods (as at setup).
      const due = mutations.filter((m) => m.inv === inv);
      if (due.length > 0) {
        const existing = pathsUnder(roots.ws);
        for (const m of due) if (m.op !== "chmod") apply(m);
        resetNewModes(roots.ws, existing);
        for (const m of due) if (m.op === "chmod") apply(m);
      }
      const carried = due.length > 0 ? carriedNames(prevNames, mutations, inv, roots) : undefined;
      const beforeReloc = relocation(roots, records, base, carried);
      const before = treeEntries(roots.ws, beforeReloc.fileDigest, beforeReloc.alias);
      rmSync(roots.argvLog, { force: true });
      const clock = invocationSeed(t, inv);
      const args = argv.map((a) => stdoutArgument(fileArgument(substitute(a, roots).split(BUSY_PORT).join(busy.port), roots), stdouts));
      const r = await runCli(roots, args, writerEnv(roots, clock.seed, t.fakeDocker, { ...invocationEnv(t, roots), ...clock.env }, t.fakeTools, t.pathTools, fakes),
        invocationInput(t, inv, roots));
      // Temporary files fl makes (mkdtemp roots, the snapshot index, the
      // doctor --security fixture) must be gone, as in Go.
      const leftovers = checksTmpdir(t) ? tmpLeftovers(roots) : [];
      if (leftovers.length > 0) tmpFailures.push(`${t.id}|${inv}: TS left ${leftovers.join(", ")} in TMPDIR`);
      stdouts.push(r.stdout);
      const plans = planDigests(r.stdout);
      const invocationRecords = readArgvLog(roots.argvLog).map((record) => maskCodexPath(record, roots, repo, process.env.PATH ?? ""));
      records.push(...invocationRecords);
      const reloc = relocation(roots, records, base, carried);
      prevNames = relocation(roots, records, base).names;
      const after = treeEntries(roots.ws, reloc.fileDigest, reloc.alias);
      const tree = treeDigest(roots.ws, reloc.fileDigest, reloc.alias);
      // After the tree and the argv log are read, like the Go replay.
      await crossVerify(t.id, inv, roots, r.stdout, args);
      // Windows runs no sh recorder: a case that records argv is POSIX-only.
      if (invocationRecords.length > 0 && !t.posixOnly) throw new Error(`${t.id}: records git/docker argv but is not posixOnly`);
      lines.push(JSON.stringify({
        case: t.id, inv, stdout: maskPlanDigests(reloc.text(r.stdout), plans), stderr: maskPlanDigests(reloc.text(r.stderr), plans), exit: r.exit,
        tree, modes: modesDigest(after), files: changedFiles(before, after),
        argv: sortVersionRecordRuns(invocationRecords.map((record) => normalizeRecord(record, roots, reloc)))
      }));
    }
    return lines;
  } finally {
    for (const server of busy.servers) server.close();
    await openAI?.close();
    restoreWritable(roots.root);
    rmSync(roots.root, { recursive: true, force: true });
  }
}

// restoreWritable makes every directory under root writable again (a chmod
// mutation may leave one read-only), so the case directory can be removed.
// The Go twin is restoreWritable in difftest/writer_test.go.
export function restoreWritable(root: string): void {
  const walk = (dir: string) => {
    chmodSync(dir, 0o755);
    for (const name of readdirSync(dir)) {
      const path = join(dir, name);
      if (lstatSync(path).isDirectory()) walk(path);
    }
  };
  if (existsSync(root)) walk(root);
}

requireOracleNode();
requireFreshBuild();
checkObjectNames();
// The oracle starts as process.execPath, an absolute path, so a template's
// fake toolchain `node` (difftest/testdata/faketools) answers fl's own
// `node --version` and never runs TS itself.
if (!isAbsolute(process.execPath)) throw new Error(`the oracle node ${process.execPath} is not an absolute path`);
// Modes are compared under umask 0, so the mode fl asks for is the mode on
// disk: a 0o666 (Node's default) and a 0o644 do not collapse (Go: difftest/umask_unix.go).
process.umask(0);
const repo = resolve(".");
const testdata = join(repo, "difftest", "testdata");
// templateFiles are the template files, relative to difftest/testdata:
// writers.json, then each writers.d/*.json in byte order. goldenFileFor is
// the golden (relative to testdata/golden) that holds a file's lines. The Go
// twins are writerTemplateFiles and writerGoldenPath.
const templateFiles = ["writers.json", ...(existsSync(join(testdata, "writers.d"))
  ? readdirSync(join(testdata, "writers.d")).filter((n) => n.endsWith(".json")).sort(byCodeUnit).map((n) => `writers.d/${n}`) : [])];
const goldenFileFor = (file: string): string => (file === "writers.json" ? "writers.jsonl" : `${file.slice(0, -".json".length)}.jsonl`);
const templates: WriterTemplate[] = [];
const fileOf: string[] = [];
for (const file of templateFiles) {
  for (const t of JSON.parse(readFileSync(join(testdata, file), "utf8")) as WriterTemplate[]) {
    templates.push(t);
    fileOf.push(file);
  }
}
checkTemplates(templates);
checkDetectors();
const results: string[][] = new Array(templates.length);
// FAULTLINE_WRITERS_ONLY=<regex> regenerates only the cases whose id
// matches and keeps every other case's lines from the committed goldens,
// whichever golden file holds them (local iteration; CI regenerates
// everything).
const only = process.env.FAULTLINE_WRITERS_ONLY ? new RegExp(process.env.FAULTLINE_WRITERS_ONLY) : undefined;
const pending: number[] = [];
if (only) {
  const kept = new Map<string, string[]>();
  for (const golden of new Set(templateFiles.map(goldenFileFor))) {
    const path = join(testdata, "golden", golden);
    if (!existsSync(path)) continue;
    for (const line of readFileSync(path, "utf8").split("\n").filter(Boolean)) {
      const id = (JSON.parse(line) as { case: string }).case;
      kept.set(id, [...(kept.get(id) ?? []), line]);
    }
  }
  for (const [index, t] of templates.entries()) {
    if (only.test(t.id) || !kept.has(t.id)) pending.push(index);
    else results[index] = kept.get(t.id)!;
  }
} else {
  pending.push(...templates.keys());
}
let next = 0;
await Promise.all(Array.from({ length: 8 }, async () => {
  while (next < pending.length) {
    const index = pending[next++]!;
    results[index] = await runCase(templates[index]!);
  }
}));
if (tmpFailures.length > 0) {
  console.error(tmpFailures.sort(byCodeUnit).join("\n"));
  process.exit(1);
}
// Each template file's lines go to its own golden, in template order; a
// golden whose template file is gone is removed.
const byGolden = new Map<string, string[]>(templateFiles.map((file) => [goldenFileFor(file), []]));
for (const [index, lines] of results.entries()) byGolden.get(goldenFileFor(fileOf[index]!))!.push(...lines);
const goldenDir = join(testdata, "golden", "writers.d");
for (const name of existsSync(goldenDir) ? readdirSync(goldenDir) : []) {
  if (!byGolden.has(`writers.d/${name}`)) rmSync(join(goldenDir, name), { force: true });
}
for (const [golden, lines] of byGolden) {
  mkdirSync(dirname(join(testdata, "golden", golden)), { recursive: true });
  writeFileSync(join(testdata, "golden", golden), lines.length === 0 ? "" : `${lines.join("\n")}\n`);
}
console.log(`writers: ${templates.length} cases, ${results.flat().length} invocations`);
