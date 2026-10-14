// Regenerates difftest/testdata/golden/writers.jsonl from frozen TS: every
// writer template (difftest/testdata/writers.json) runs its invocations in
// one workspace under the seed preload; each invocation gives one line
// (slice 2 spec 2.3). The Go twin is difftest/writer_test.go.
//   pnpm build && pnpm exec tsx difftest/gen/scenarios.ts && pnpm exec tsx difftest/gen/gen-writers.ts
import { existsSync, lstatSync, readFileSync, readdirSync, readlinkSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:net";
import { join, resolve } from "node:path";
import { canonicalJson, digestJson, sha256 } from "../../src/canonical.js";
import { PATH_OPS, applyMutation, normalizeWriter, treeDigest, type Template } from "./cases.js";
import { copyScenario, makeCaseRoots, resetModes, requireFreshBuild, requireOracleNode, runCli, writerEnv, type CaseRoots } from "./scenarios.js";

export type WriterTemplate = {
  id: string; scenario: string; argv: string[][]; fakeDocker?: string; env?: Record<string, string>;
  posixOnly?: boolean; mutations?: Template[];
};

const TEMPLATE_KEYS = new Set(["id", "scenario", "argv", "fakeDocker", "env", "posixOnly", "mutations"]);

// checkTemplates rejects what the Go loader rejects too: unknown keys,
// duplicate ids and cases without invocations.
function checkTemplates(templates: WriterTemplate[]): void {
  const ids = new Set<string>();
  for (const t of templates) {
    for (const key of Object.keys(t)) if (!TEMPLATE_KEYS.has(key)) throw new Error(`writer template ${t.id}: unknown key ${key}`);
    if (ids.has(t.id)) throw new Error(`duplicate writer template ${t.id}`);
    if (!/^[a-z0-9][a-z0-9-]*$/.test(t.id)) throw new Error(`writer template id ${t.id} is not lowercase kebab-case`);
    if (t.argv.length === 0) throw new Error(`writer template ${t.id} has no invocation`);
    ids.add(t.id);
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
export function treeEntries(root: string, fileDigest: (rel: string, bytes: Buffer) => string = (_rel, bytes) => sha256(bytes)): Map<string, string> {
  const out = new Map<string, string>();
  if (!existsSync(root)) return out;
  const walk = (rel: string) => {
    for (const name of readdirSync(join(root, rel)).sort(byCodeUnit)) {
      const r = rel ? `${rel}/${name}` : name;
      const path = join(root, r);
      const stat = lstatSync(path);
      const mode = ` ${(stat.mode & 0o777).toString(8)}`;
      if (stat.isSymbolicLink()) out.set(r, `symlink:${readlinkSync(path)}`);
      else if (stat.isDirectory()) { out.set(r, `dir${mode}`); walk(r); }
      else if (!stat.isFile()) out.set(r, `special${mode}`);
      else out.set(r, `${fileDigest(r, readFileSync(path))}${mode}`);
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
// A file is derived from when it holds a case path or a digest found in the
// previous pass (files sorted by code unit, JSON members in document order,
// .git directories skipped); passes repeat until nothing new is found, and
// the first name a digest gets is kept. text() masks the case paths
// (normalizeWriter) and then each such digest as <PATHDIGEST:name>;
// fileDigest() hashes a path-dependent file's masked text and any other
// file's bytes. The derived digests are also checked directly: the
// generator verifies every bundle TS wrote against the root it printed, the
// Go replay does the same, and TestWriterSameDirectory compares a subset
// unmasked. The Go twin is relocation in difftest/writer_test.go.
export type Relocation = { text: (s: string) => string; fileDigest: (rel: string, bytes: Buffer) => string };
const SELF_DIGEST = /^sha256:[0-9a-f]{64}$/;
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

export function relocation(roots: CaseRoots, records: string[] = []): Relocation {
  const files: [string, Buffer, string][] = [];
  const walk = (rel: string) => {
    for (const name of readdirSync(join(roots.ws, rel)).sort(byCodeUnit)) {
      const r = rel ? `${rel}/${name}` : name;
      const stat = lstatSync(join(roots.ws, r));
      if (stat.isDirectory()) { if (name !== ".git") walk(r); }
      else if (stat.isFile()) { const bytes = readFileSync(join(roots.ws, r)); files.push([r, bytes, bytes.toString("utf8")]); }
    }
  };
  if (existsSync(roots.ws)) walk("");
  const names = new Map<string, string>();
  let found: string[] = [];
  const add = (hex: string, name: string) => { if (!names.has(hex)) { names.set(hex, name); found.push(hex); } };
  const tainted = (text: string) => text.includes(roots.root) || [...names.keys()].some((d) => text.includes(d));
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
  const seeds = [roots.root];
  for (const path of loggedCasePaths(records, roots)) {
    const digest = sha256(Buffer.from(path, "utf8"));
    add(digest, `path:${normalizeWriter(path, roots)}`);
    seeds.push(digest);
  }
  let frontier = seeds;
  while (frontier.length > 0) {
    found = [];
    for (const [rel, bytes, text] of files) {
      if (!frontier.some((needle) => text.includes(needle))) continue;
      add(sha256(bytes), `file:${rel}`);
      try { visit(JSON.parse(text), rel, ""); } catch { /* not JSON */ }
    }
    frontier = found;
  }
  const sorted = [...names.keys()].sort(byCodeUnit);
  const text = (s: string): string => {
    let out = normalizeWriter(s, roots);
    for (const d of sorted) out = out.split(d).join(`<PATHDIGEST:${names.get(d)}>`);
    return out;
  };
  return {
    text,
    fileDigest: (_rel, bytes) => (names.has(sha256(bytes)) ? sha256(text(bytes.toString("utf8"))) : sha256(bytes))
  };
}

// readArgvLog splits the recorders' log into records ("git a\x1fb").
export function readArgvLog(path: string): string[] {
  if (!existsSync(path)) return [];
  const records = readFileSync(path, "utf8").split("\x1e");
  records.pop(); // the text after the last terminator (empty)
  return records;
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

// crossVerify runs TS `fl verify` on every bundle TS wrote (slice 2 spec
// 2.4); TestWriterReplay does the same with Go on the bundles Go wrote. It
// runs after the invocation's argv log is read, so its git calls are not
// recorded.
async function crossVerify(id: string, inv: number, roots: CaseRoots, stdout: string): Promise<void> {
  const directory = writtenBundle(stdout);
  if (directory === undefined) return;
  const root = (JSON.parse(stdout) as { proofBundle: { rootDigest: string } }).proofBundle.rootDigest;
  const r = await runCli(roots, ["verify", directory, "--expect-root", root], writerEnv(roots, `${id}:verify`, undefined, {}));
  if (r.exit !== 0 || !r.stdout.startsWith("Integrity: VALID\n") || !r.stdout.includes("\nExternal root: MATCH\n")) throw new Error(`${id}|${inv}: TS verify of the bundle TS wrote failed (exit ${r.exit})\n${r.stdout}${r.stderr}`);
}

// BUSY_PORT in an argv string is the port of a listener the case holds on
// 127.0.0.1 while it runs (judge-proof-port-in-use); the Go twin is
// holdBusyPort in difftest/writer_test.go.
export const BUSY_PORT = "<BUSYPORT>";

async function holdBusyPort(t: WriterTemplate): Promise<{ port: string; server?: Server }> {
  if (!t.argv.some((argv) => argv.some((a) => a.includes(BUSY_PORT)))) return { port: "" };
  const server = createServer();
  await new Promise<void>((resolveListen, rejectListen) => {
    server.once("error", rejectListen);
    server.listen(0, "127.0.0.1", () => resolveListen());
  });
  return { port: String((server.address() as { port: number }).port), server };
}

async function runCase(t: WriterTemplate): Promise<string[]> {
  const roots = makeCaseRoots();
  const busy = await holdBusyPort(t);
  try {
    copyScenario(t.scenario, roots.ws);
    for (const template of t.mutations ?? []) {
      const m = substituteDeep(template, roots) as Template;
      applyMutation(roots.ws, { id: t.id, base: "writer", template: m, file: PATH_OPS.has(m.op) ? m.path ?? "." : m.file! });
    }
    resetModes(roots.ws);
    const env = Object.fromEntries(Object.entries(t.env ?? {}).map(([k, v]) => [k, substitute(v, roots)]));
    const lines: string[] = [];
    // The recorded paths accumulate over the case's invocations: a later
    // invocation must keep masking the worktree digests an earlier one
    // wrote into a file (the Go twin is runWriterCase).
    const records: string[] = [];
    for (const [inv, argv] of t.argv.entries()) {
      const before = treeEntries(roots.ws, relocation(roots, records).fileDigest);
      rmSync(roots.argvLog, { force: true });
      const r = await runCli(roots, argv.map((a) => substitute(a, roots).split(BUSY_PORT).join(busy.port)), writerEnv(roots, t.id, t.fakeDocker, env));
      const invocationRecords = readArgvLog(roots.argvLog);
      records.push(...invocationRecords);
      const reloc = relocation(roots, records);
      const after = treeEntries(roots.ws, reloc.fileDigest);
      const tree = treeDigest(roots.ws, reloc.fileDigest);
      // After the tree and the argv log are read, like the Go replay.
      await crossVerify(t.id, inv, roots, r.stdout);
      // Windows runs no sh recorder: a case that records argv is POSIX-only.
      if (invocationRecords.length > 0 && !t.posixOnly) throw new Error(`${t.id}: records git/docker argv but is not posixOnly`);
      lines.push(JSON.stringify({
        case: t.id, inv, stdout: reloc.text(r.stdout), stderr: reloc.text(r.stderr), exit: r.exit,
        tree, modes: modesDigest(after), files: changedFiles(before, after),
        argv: invocationRecords.map((record) => normalizeRecord(record, roots, reloc))
      }));
    }
    return lines;
  } finally {
    busy.server?.close();
    rmSync(roots.root, { recursive: true, force: true });
  }
}

requireOracleNode();
requireFreshBuild();
// Modes are compared under umask 0, so the mode fl asks for is the mode on
// disk: a 0o666 (Node's default) and a 0o644 do not collapse (Go: difftest/umask_unix.go).
process.umask(0);
const repo = resolve(".");
const templates = JSON.parse(readFileSync(join(repo, "difftest/testdata/writers.json"), "utf8")) as WriterTemplate[];
checkTemplates(templates);
const results: string[][] = new Array(templates.length);
let next = 0;
await Promise.all(Array.from({ length: 8 }, async () => {
  while (next < templates.length) {
    const index = next++;
    results[index] = await runCase(templates[index]!);
  }
}));
writeFileSync(join(repo, "difftest/testdata/golden/writers.jsonl"), `${results.flat().join("\n")}\n`);
console.log(`writers: ${templates.length} cases, ${results.flat().length} invocations`);
