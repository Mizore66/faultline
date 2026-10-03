import { copyFileSync, lstatSync, mkdirSync, readFileSync, readdirSync, readlinkSync, renameSync, rmSync, statSync, symlinkSync, truncateSync, writeFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { digestJson, sha256 } from "../../src/canonical.js";

export type Template = {
  id: string; op: string; file?: string; path?: string; offset?: number; target?: string; content?: string;
  find?: string; replace?: string; hex?: string; key?: string; value?: unknown; rehash?: boolean;
  bases?: string[]; jsonPath?: Array<string | number>; valueFrom?: Array<string | number>; swapWith?: Array<string | number>;
  delete?: boolean; first?: boolean; to?: string; prefix?: string; suffix?: string; depth?: number; count?: number;
  edits?: JsonEdit[]; size?: number; from?: string; fakeGit?: string; resignLedger?: boolean; total?: number; declared?: boolean;
  // alsoEdit: json-edit steps for other files of the same case.
  alsoEdit?: Array<{ file: string; edits: JsonEdit[] }>;
  // rebindLifecycle: after a ledger edit, rebind manifest.lifecycle to the
  // edited ledger the way the writer's bindLifecycleLedger does.
  rebindLifecycle?: boolean;
  // fakeGitPathOnly: PATH is the fake git's directory and a missing one.
  fakeGitPathOnly?: boolean;
};
// JsonEdit is one json-edit step; a json-edit template is one step itself or
// lists several in `edits`, applied in order.
type JsonEdit = Pick<Template, "jsonPath" | "value" | "valueFrom" | "swapWith" | "delete">;
export type Case = { id: string; base: string; template?: Template; file?: string };

// FIXTURES holds files that copy-file templates put into a bundle.
const FIXTURES = join(dirname(fileURLToPath(import.meta.url)), "..", "testdata", "fixtures");

const byCodeUnit = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

export function listFiles(root: string, rel = ""): string[] {
  const out: string[] = [];
  for (const name of readdirSync(join(root, rel)).sort(byCodeUnit)) {
    const r = rel ? `${rel}/${name}` : name;
    const stat = lstatSync(join(root, r));
    if (stat.isDirectory()) out.push(...listFiles(root, r));
    else if (stat.isFile()) out.push(r);
  }
  return out.sort(byCodeUnit);
}

function isObjectJson(path: string, needKey: boolean): boolean {
  try {
    const v = JSON.parse(readFileSync(path, "utf8"));
    return v !== null && typeof v === "object" && !Array.isArray(v) && (!needKey || Object.keys(v).length > 0);
  } catch {
    return false;
  }
}

function applicable(t: Template, path: string): boolean {
  switch (t.op) {
    case "flip-byte": return readFileSync(path).length > 0;
    case "replace-text": return readFileSync(path).toString("latin1").includes(t.find!);
    case "strip-trailing-newline": return readFileSync(path).at(-1) === 0x0a;
    case "json-add-key": return isObjectJson(path, false);
    case "json-retype": case "json-drop-first": return isObjectJson(path, true);
    case "json-edit": return jsonEditApplicable(t, path);
    case "replace-nested": return readFileSync(path).toString("latin1").includes(t.find!);
    default: return true;
  }
}

type Json = unknown;

// at walks a JSON path (object keys and array indices); undefined when absent.
function at(v: Json, path: Array<string | number>): Json {
  let x = v;
  for (const k of path) {
    if (x === null || typeof x !== "object") return undefined;
    if (Array.isArray(x)) {
      if (typeof k !== "number" || k >= x.length) return undefined;
      x = x[k];
    } else {
      if (typeof k !== "string" || !Object.prototype.hasOwnProperty.call(x, k)) return undefined;
      x = (x as Record<string, Json>)[k];
    }
  }
  return x;
}

function setAt(v: Json, path: Array<string | number>, value: Json): void {
  const parent = at(v, path.slice(0, -1)) as Record<string, Json> | Json[];
  const k = path[path.length - 1]!;
  if (Array.isArray(parent)) parent[k as number] = value;
  else defineOwn(parent, k as string, value);
}

const jsonEdits = (t: Template): JsonEdit[] => t.edits ?? [t];

// jsonEditApplicable checks every step against the file as it would be after
// the steps before it.
function jsonEditApplicable(t: Template, path: string): boolean {
  if (!isObjectJson(path, false)) return false;
  const v = JSON.parse(readFileSync(path, "utf8")) as Json;
  for (const e of jsonEdits(t)) {
    const parent = at(v, e.jsonPath!.slice(0, -1));
    if (parent === null || typeof parent !== "object") return false;
    if (e.delete || e.valueFrom || e.swapWith) {
      if (at(v, e.jsonPath!) === undefined) return false;
    }
    if (e.valueFrom && at(v, e.valueFrom) === undefined) return false;
    if (e.swapWith && at(v, e.swapWith) === undefined) return false;
    applyJsonEdit(v, e);
  }
  return true;
}

function applyJsonEdit(v: Json, e: JsonEdit): void {
  const p = e.jsonPath!;
  if (e.delete) {
    const parent = at(v, p.slice(0, -1));
    if (Array.isArray(parent)) parent.splice(p[p.length - 1] as number, 1);
    else delete (parent as Record<string, Json>)[p[p.length - 1] as string];
  } else if (e.swapWith) {
    const a = at(v, p);
    const b = at(v, e.swapWith);
    setAt(v, p, b);
    setAt(v, e.swapWith, a);
  } else {
    setAt(v, p, e.valueFrom ? at(v, e.valueFrom) : structuredClone(e.value));
  }
}

// matches is a template file pattern: "*", "*.json", "dir/*" or an exact path.
function matches(pattern: string, file: string): boolean {
  if (pattern === "*") return true;
  if (pattern === "*.json") return file.endsWith(".json");
  if (pattern.endsWith("/*")) return file.startsWith(pattern.slice(0, -1));
  return pattern === file;
}

// Ops whose target is a path in the bundle (or the bundle itself), not an existing file.
const PATH_OPS = new Set(["add-file", "root-symlink", "sparse-file", "many-files", "fake-git", "pad-total"]);

export function expandCases(base: string, root: string, templates: Template[]): Case[] {
  const files = listFiles(root);
  const cases: Case[] = [{ id: base, base }];
  for (const t of templates) {
    // git-shape-* bases pin one history shape each (round 5): only the
    // templates that name them run there, not the generic ones.
    if (t.bases ? !t.bases.includes(base) : base.startsWith("git-shape-")) continue;
    const targets = PATH_OPS.has(t.op)
      ? [t.path ?? "."]
      : files.filter((f) => matches(t.file!, f));
    for (const file of t.first ? targets.slice(0, 1) : targets) {
      if (!PATH_OPS.has(t.op) && !applicable(t, join(root, file))) continue;
      cases.push({ id: `${base}__${t.id}__${file.replaceAll("/", "~")}`, base, template: t, file });
    }
  }
  return cases;
}

function defineOwn(o: Record<string, unknown>, key: string, value: unknown): void {
  Object.defineProperty(o, key, { value, enumerable: true, writable: true, configurable: true });
}

// canonicalDigest is src/canonical.ts digestJson without recursion, so the
// harness rehash does not depend on how warmed-up V8 is (the recursive
// version overflows the stack on deep values, or not, depending on JIT
// state). It reproduces canonicalJson's output: members sorted with
// localeCompare, then laid out as JSON.stringify lays out the rebuilt object
// (array-index keys first, ascending), with __proto__ dropped because
// assigning it sets the prototype instead of a property. Go's
// canonical.DigestJSON is iterative too.
export function canonicalDigest(root: unknown): string {
  const out: string[] = [];
  type Frame = { items: unknown[]; i: number } | { entries: Array<[string, unknown]>; i: number };
  const stack: Frame[] = [];
  const isIndex = (k: string) => /^(?:0|[1-9]\d{0,9})$/.test(k) && Number(k) < 4294967295;
  const emit = (v: unknown): void => {
    if (Array.isArray(v)) {
      out.push("[");
      stack.push({ items: v, i: 0 });
    } else if (v !== null && typeof v === "object") {
      const sorted = Object.entries(v).filter(([, c]) => c !== undefined).sort(([l], [r]) => l.localeCompare(r))
        .filter(([k]) => k !== "__proto__");
      const index = sorted.filter(([k]) => isIndex(k)).sort(([l], [r]) => Number(l) - Number(r));
      out.push("{");
      stack.push({ entries: [...index, ...sorted.filter(([k]) => !isIndex(k))], i: 0 });
    } else if (typeof v === "number" && !Number.isFinite(v)) {
      throw new TypeError("Value is not finite JSON: number");
    } else if (v === null || typeof v === "boolean" || typeof v === "number" || typeof v === "string") {
      out.push(JSON.stringify(v));
    } else {
      throw new TypeError(`Value is not JSON-serializable: ${typeof v}`);
    }
  };
  emit(root);
  while (stack.length > 0) {
    const f = stack[stack.length - 1]!;
    if ("items" in f) {
      if (f.i === f.items.length) {
        out.push("]");
        stack.pop();
        continue;
      }
      if (f.i > 0) out.push(",");
      emit(f.items[f.i++]);
    } else {
      if (f.i === f.entries.length) {
        out.push("}");
        stack.pop();
        continue;
      }
      if (f.i > 0) out.push(",");
      const [k, v] = f.entries[f.i++]!;
      out.push(JSON.stringify(k), ":");
      emit(v);
    }
  }
  return `sha256:${sha256(out.join(""))}`;
}

function rehash(root: string, base: string): void {
  if (base.startsWith("prevention-")) {
    let manifest: Record<string, unknown>;
    let body: unknown;
    try {
      manifest = JSON.parse(readFileSync(join(root, "manifest.json"), "utf8"));
      body = JSON.parse(readFileSync(join(root, "prevention.json"), "utf8"));
      if (manifest === null || typeof manifest !== "object" || Array.isArray(manifest)) return;
      const prevention = manifest.prevention;
      if (prevention !== null && typeof prevention === "object" && !Array.isArray(prevention)) {
        (prevention as Record<string, unknown>).digest = canonicalDigest(body);
      }
      const { rootDigest: _dropped, ...unsigned } = manifest;
      manifest.rootDigest = canonicalDigest(unsigned);
    } catch {
      return;
    }
    writeFileSync(join(root, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
    return;
  }
  const files = listFiles(root).filter((f) => f !== "hashes.txt" && f !== "ROOT.sha256");
  const hashes = `${[...files].sort((l, r) => l.localeCompare(r)).map((f) => `${sha256(readFileSync(join(root, f)))}  ${f}`).join("\n")}\n`;
  writeFileSync(join(root, "hashes.txt"), hashes);
  writeFileSync(join(root, "ROOT.sha256"), `sha256:${sha256(hashes)}\n`);
}

type LedgerJson = {
  schemaVersion: string; ledgerId: string; sessionId: string; createdAt: string;
  events: Array<{ hash?: string; previousHash?: string; event: { type: string; payload?: { checkpoint?: { digest?: string } } } }>;
};

// resignLedgerJson re-signs an edited lifecycle ledger the way the writer
// does: each checkpoint's digest (signGitCheckpoint), then the event hash
// chain from the genesis hash (hashLifecycleEvent, ledgerGenesisHash), so an
// edit reaches the checks after the ledger's own verification.
function resignLedgerJson(ledger: LedgerJson): void {
  let previous = digestJson({
    kind: "FAULTLINE_CODEX_LIFECYCLE_GENESIS", schemaVersion: ledger.schemaVersion,
    ledgerId: ledger.ledgerId, sessionId: ledger.sessionId, createdAt: ledger.createdAt
  });
  for (const event of ledger.events) {
    const checkpoint = event.event.payload?.checkpoint;
    if (event.event.type === "WORKTREE_CHECKPOINT" && checkpoint) {
      const { digest: _digest, ...unsigned } = checkpoint;
      checkpoint.digest = digestJson(unsigned);
    }
    event.previousHash = previous;
    const { hash: _hash, ...unsigned } = event;
    event.hash = digestJson(unsigned);
    previous = event.hash;
  }
}

// padTotal adds sparse files path-00000, ... of at most 120 MiB (under the
// per-artifact cap) so that the bundle's regular files, hashes.txt and
// ROOT.sha256 included, total exactly `total` bytes.
// padTotal pads the bundle with zero files until its regular files add up to
// total bytes; with declared, only the files hashes.txt catalogs count (all
// but hashes.txt and ROOT.sha256), the total the git verifier checks.
function padTotal(root: string, base: string, path: string, total: number, declared: boolean): void {
  const chunk = 120 * 1024 * 1024;
  const size = () => listFiles(root)
    .filter((f) => !declared || (f !== "hashes.txt" && f !== "ROOT.sha256"))
    .reduce((n, f) => n + statSync(join(root, f)).size, 0);
  rehash(root, base);
  let need = total - size();
  let last = "";
  for (let i = 0; need > 0; i++) {
    last = `${path}-${String(i).padStart(5, "0")}`;
    const n = Math.min(need, chunk);
    writeFileSync(last, "");
    truncateSync(last, n);
    need -= n;
  }
  rehash(root, base);
  truncateSync(last, statSync(last).size + total - size());
  rehash(root, base);
}

// rebindLifecycle binds manifest.lifecycle to the ledger as the writer's
// bindLifecycleLedger does: the ledger digest, the last event hash, one
// binding per checkpoint whose head commit is an investigation state, and
// FULLY_BOUND when every state is covered.
function rebindLifecycle(root: string, ledger: LedgerJson): void {
  const investigation = JSON.parse(readFileSync(join(root, "investigation.json"), "utf8")) as { states: Array<{ index: number; commit: string }> };
  const manifestPath = join(root, "manifest.json");
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as { lifecycle: Record<string, unknown> };
  const stateByCommit = new Map(investigation.states.map((s) => [s.commit, s]));
  const bindings: Array<{ sequence: number; stateIndex: number; checkpointDigest: string }> = [];
  for (const event of ledger.events as Array<{ sequence: number; event: { type: string; payload?: { checkpoint?: { headCommit?: string; digest?: string } } } }>) {
    const checkpoint = event.event.payload?.checkpoint;
    if (event.event.type !== "WORKTREE_CHECKPOINT" || !checkpoint) continue;
    const state = stateByCommit.get(checkpoint.headCommit!);
    if (state) bindings.push({ sequence: event.sequence, stateIndex: state.index, checkpointDigest: checkpoint.digest! });
  }
  const covered = new Set(bindings.map((b) => b.stateIndex));
  manifest.lifecycle = {
    ...manifest.lifecycle,
    status: covered.size === investigation.states.length ? "FULLY_BOUND" : "PARTIALLY_BOUND",
    ledgerDigest: digestJson(ledger),
    headHash: ledger.events.at(-1)!.hash,
    checkpointBindings: bindings
  };
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
}

// applyMutation mutates the bundle at root; root-symlink replaces root itself
// with a symbolic link to a sibling copy.
export function applyMutation(root: string, c: Case): void {
  const t = c.template;
  if (!t) return;
  const path = join(root, c.file!);
  switch (t.op) {
    case "root-symlink": {
      const real = `${root}-real`;
      renameSync(root, real);
      symlinkSync(basename(real), root);
      break;
    }
    case "rename": mkdirSync(dirname(join(root, t.to!)), { recursive: true }); renameSync(path, join(root, t.to!)); break;
    case "replace-nested": {
      const s = readFileSync(path).toString("latin1");
      const nested = `${t.prefix ?? ""}${"[".repeat(t.depth!)}${"]".repeat(t.depth!)}${t.suffix ?? ""}`;
      writeFileSync(path, Buffer.from(s.replace(t.find!, () => nested), "latin1"));
      break;
    }
    case "append-catalog-lines": {
      let extra = "";
      for (let i = 0; i < t.count!; i++) extra += `${"0".repeat(64)}  extra/${String(i).padStart(5, "0")}.json\n`;
      writeFileSync(path, Buffer.concat([readFileSync(path), Buffer.from(extra, "utf8")]));
      break;
    }
    case "json-edit": {
      const v = JSON.parse(readFileSync(path, "utf8")) as Json;
      for (const e of jsonEdits(t)) applyJsonEdit(v, e);
      if (t.resignLedger) resignLedgerJson(v as LedgerJson);
      writeFileSync(path, `${JSON.stringify(v, null, 2)}\n`);
      for (const also of t.alsoEdit ?? []) {
        const other = join(root, also.file);
        const w = JSON.parse(readFileSync(other, "utf8")) as Json;
        for (const e of also.edits) applyJsonEdit(w, e);
        writeFileSync(other, `${JSON.stringify(w, null, 2)}\n`);
      }
      if (t.rebindLifecycle) rebindLifecycle(root, v as LedgerJson);
      break;
    }
    case "append-bytes": {
      const unit = Buffer.from(t.hex!, "hex");
      writeFileSync(path, Buffer.concat([readFileSync(path), ...Array.from({ length: t.count! }, () => unit)]));
      break;
    }
    case "fake-git": break; // the tree is unchanged; caseEnv puts the fake git on PATH
    case "copy-file": copyFileSync(join(FIXTURES, t.from!), path); break;
    case "sparse-file": {
      // count > 1 makes path-00000, path-00001, ...
      mkdirSync(dirname(path), { recursive: true });
      const paths = t.count === undefined ? [path] : Array.from({ length: t.count }, (_, i) => `${path}-${String(i).padStart(5, "0")}`);
      for (const p of paths) {
        writeFileSync(p, "");
        truncateSync(p, t.size!);
      }
      break;
    }
    case "pad-total": padTotal(root, c.base, path, t.total!, t.declared === true); break;
    case "many-files": {
      mkdirSync(path, { recursive: true });
      for (let i = 0; i < t.count!; i++) writeFileSync(join(path, `${String(i).padStart(5, "0")}.txt`), t.content!);
      break;
    }
    case "flip-byte": {
      const b = readFileSync(path);
      b[t.offset === -1 ? b.length - 1 : 0]! ^= 0x01;
      writeFileSync(path, b);
      break;
    }
    case "delete-file": rmSync(path); break;
    case "make-dir": rmSync(path); mkdirSync(path); break;
    case "symlink": rmSync(path); symlinkSync(t.target!, path); break;
    case "add-file": mkdirSync(dirname(path), { recursive: true }); writeFileSync(path, t.content!); break;
    case "replace-text": {
      const s = readFileSync(path).toString("latin1");
      writeFileSync(path, Buffer.from(s.replace(t.find!, () => t.replace!), "latin1"));
      break;
    }
    case "strip-trailing-newline": { const b = readFileSync(path); writeFileSync(path, b.subarray(0, b.length - 1)); break; }
    case "prepend-bytes": writeFileSync(path, Buffer.concat([Buffer.from(t.hex!, "hex"), readFileSync(path)])); break;
    case "json-add-key": case "json-retype": case "json-drop-first": {
      const v = JSON.parse(readFileSync(path, "utf8")) as Record<string, unknown>;
      const first = Object.keys(v)[0]!;
      if (t.op === "json-add-key") defineOwn(v, t.key!, t.value);
      else if (t.op === "json-retype") defineOwn(v, first, typeof v[first] === "number" ? "retyped" : 12345);
      else delete v[first];
      writeFileSync(path, `${JSON.stringify(v, null, 2)}\n`);
      break;
    }
    default: throw new Error(`unknown op ${t.op}`);
  }
  if (t.rehash) rehash(root, c.base);
}

// caseEnv is the environment a case runs fl with: a fake-git case puts
// difftest/testdata/fakegit/<name> first on PATH (POSIX shell scripts).
export function caseEnv(c: Case, env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  if (!c.template?.fakeGit) return env;
  const dir = join(FIXTURES, "..", "fakegit", c.template.fakeGit);
  if (c.template.fakeGitPathOnly) return { ...env, PATH: `${dir}:/nonexistent-faultline-path` };
  return { ...env, PATH: `${dir}:${env.PATH ?? ""}` };
}

export function treeDigest(root: string): string {
  const lines: string[] = [];
  const walk = (rel: string) => {
    for (const name of readdirSync(join(root, rel)).sort(byCodeUnit)) {
      const r = rel ? `${rel}/${name}` : name;
      const path = join(root, r);
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) lines.push(`L ${r} ${readlinkSync(path)}`);
      else if (stat.isDirectory()) { lines.push(`D ${r}`); walk(r); }
      else lines.push(`F ${r} ${sha256(readFileSync(path))}`);
    }
  };
  walk("");
  return sha256(lines.join("\n"));
}

export const GIT_LABELS = [
  "Git bundle head listing", "Temporary Git verifier initialization", "Git bundle verification",
  "Git bundle extraction", "Git commit resolution", "Git tree resolution", "Git bundle range enumeration",
  "Git binary range patch creation", "Git bundle object-format resolution"
];

function isBoundary(c: string | undefined): boolean {
  return c === undefined || c === "'" || c === "\"" || /\s/.test(c);
}

// maskGitDetail hides git's own diagnostics (they vary across git
// versions): each run of lines starting with "error: ", "fatal: ",
// "warning: " or "hint: " becomes one <GIT-STDERR> (a line containing "; "
// is not masked). Everything else stays:
// other git output, the text FaultLine composes (the "; " join with Node's
// spawnSync error, the "exit <status>" fallback), and anything a port adds.
// The fake gits print fixed text, so their cases are not masked at all.
export function maskGitDetail(detail: string): string {
  if (/^exit (?:null|-?\d+)$/.test(detail)) return detail;
  let body = detail;
  let tail = "";
  const m = /^([\s\S]*?)((?:; )?spawnSync git [A-Z0-9_]+)$/.exec(detail);
  if (m && (m[1] === "") === !m[2]!.startsWith("; ")) {
    body = m[1]!;
    tail = m[2]!;
  }
  const out: string[] = [];
  for (const line of body.split("\n")) {
    if (!/^(?:error|fatal|warning|hint): /.test(line) || line.includes("; ")) out.push(line);
    else if (out.at(-1) !== "<GIT-STDERR>") out.push("<GIT-STDERR>");
  }
  return out.join("\n") + tail;
}

export function normalize(text: string, bundle: string, maskGit = true): string {
  let out = text;
  // Only the path passed to fl is masked (goldens are generated on Linux).
  out = out.split(bundle).join("<BUNDLE>");
  const marker = "faultline-git-proof-verify-";
  for (let i = out.indexOf(marker); i >= 0; i = out.indexOf(marker, i)) {
    let start = i;
    while (start > 0 && !isBoundary(out[start - 1])) start--;
    let end = i + marker.length;
    while (end < out.length && /[A-Za-z0-9]/.test(out[end]!)) end++;
    out = `${out.slice(0, start)}<GITTMP>${out.slice(end)}`;
    i = start + "<GITTMP>".length;
  }
  for (const label of maskGit ? GIT_LABELS : []) {
    const needle = `${label} failed: `;
    for (let i = out.indexOf(needle); i >= 0; i = out.indexOf(needle, i + needle.length)) {
      const from = i + needle.length;
      let to = out.indexOf("\n- ", from);
      if (to < 0) to = out.endsWith("\n") ? out.length - 1 : out.length;
      const detail = maskGitDetail(out.slice(from, to));
      out = `${out.slice(0, from)}${detail}${out.slice(to)}`;
      i = from + detail.length - needle.length;
    }
  }
  return out;
}

export function baseRoot(root: string, base: string): string {
  try {
    if (base.startsWith("prevention-")) return JSON.parse(readFileSync(join(root, "manifest.json"), "utf8")).rootDigest;
    return readFileSync(join(root, "ROOT.sha256"), "utf8").trim();
  } catch {
    return "missing";
  }
}
