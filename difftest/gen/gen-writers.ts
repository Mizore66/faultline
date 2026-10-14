// Regenerates difftest/testdata/golden/writers.jsonl from frozen TS: every
// writer template (difftest/testdata/writers.json) runs its invocations in
// one workspace under the seed preload; each invocation gives one line
// (slice 2 spec 2.3). The Go twin is difftest/writer_test.go.
//   pnpm build && pnpm exec tsx difftest/gen/scenarios.ts && pnpm exec tsx difftest/gen/gen-writers.ts
import { existsSync, lstatSync, readFileSync, readdirSync, readlinkSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { sha256 } from "../../src/canonical.js";
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

const byCodeUnit = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

// treeEntries describes every path under root the way treeDigest walks it:
// a file by its SHA-256, a symlink by its target, a directory, or anything
// else (a FIFO); all but symlinks also carry their permission bits in octal
// ("<sha256> 600", "dir 700"), since the stores are written 0600/0700.
export function treeEntries(root: string): Map<string, string> {
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
      else out.set(r, `${sha256(readFileSync(path))}${mode}`);
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

// readArgvLog splits the recorders' log into records ("git a\x1fb").
export function readArgvLog(path: string): string[] {
  if (!existsSync(path)) return [];
  const records = readFileSync(path, "utf8").split("\x1e");
  records.pop(); // the text after the last terminator (empty)
  return records;
}

// normalizeRecord normalizes each argument of a record on its own, so a mask
// that widens to the previous boundary (<GITTMP>) stays inside its argument.
export function normalizeRecord(record: string, roots: CaseRoots): string {
  return record.split("\x1f").map((arg) => normalizeWriter(arg, roots)).join("\x1f");
}

async function runCase(t: WriterTemplate): Promise<string[]> {
  const roots = makeCaseRoots();
  try {
    copyScenario(t.scenario, roots.ws);
    for (const m of t.mutations ?? []) {
      applyMutation(roots.ws, { id: t.id, base: "writer", template: m, file: PATH_OPS.has(m.op) ? m.path ?? "." : m.file! });
    }
    resetModes(roots.ws);
    const env = Object.fromEntries(Object.entries(t.env ?? {}).map(([k, v]) => [k, substitute(v, roots)]));
    const lines: string[] = [];
    for (const [inv, argv] of t.argv.entries()) {
      const before = treeEntries(roots.ws);
      rmSync(roots.argvLog, { force: true });
      const r = await runCli(roots, argv.map((a) => substitute(a, roots)), writerEnv(roots, t.id, t.fakeDocker, env));
      const after = treeEntries(roots.ws);
      const records = readArgvLog(roots.argvLog);
      // Windows runs no sh recorder: a case that records argv is POSIX-only.
      if (records.length > 0 && !t.posixOnly) throw new Error(`${t.id}: records git/docker argv but is not posixOnly`);
      lines.push(JSON.stringify({
        case: t.id, inv, stdout: normalizeWriter(r.stdout, roots), stderr: normalizeWriter(r.stderr, roots), exit: r.exit,
        tree: treeDigest(roots.ws), modes: modesDigest(after), files: changedFiles(before, after),
        argv: records.map((record) => normalizeRecord(record, roots))
      }));
    }
    return lines;
  } finally {
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
