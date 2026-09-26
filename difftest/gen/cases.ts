import { lstatSync, mkdirSync, readFileSync, readdirSync, readlinkSync, renameSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";
import { digestJson, sha256 } from "../../src/canonical.js";

export type Template = {
  id: string; op: string; file?: string; path?: string; offset?: number; target?: string; content?: string;
  find?: string; replace?: string; hex?: string; key?: string; value?: unknown; rehash?: boolean;
  bases?: string[]; jsonPath?: Array<string | number>; valueFrom?: Array<string | number>; swapWith?: Array<string | number>;
  delete?: boolean; first?: boolean; to?: string; prefix?: string; suffix?: string; depth?: number; count?: number;
};
export type Case = { id: string; base: string; template?: Template; file?: string };

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

function jsonEditApplicable(t: Template, path: string): boolean {
  if (!isObjectJson(path, false)) return false;
  const v = JSON.parse(readFileSync(path, "utf8")) as Json;
  const parent = at(v, t.jsonPath!.slice(0, -1));
  if (parent === null || typeof parent !== "object") return false;
  if (t.delete || t.valueFrom || t.swapWith) {
    if (at(v, t.jsonPath!) === undefined) return false;
  }
  if (t.valueFrom && at(v, t.valueFrom) === undefined) return false;
  if (t.swapWith && at(v, t.swapWith) === undefined) return false;
  return true;
}

// matches is a template file pattern: "*", "*.json", "dir/*" or an exact path.
function matches(pattern: string, file: string): boolean {
  if (pattern === "*") return true;
  if (pattern === "*.json") return file.endsWith(".json");
  if (pattern.endsWith("/*")) return file.startsWith(pattern.slice(0, -1));
  return pattern === file;
}

// Ops whose target is a path in the bundle (or the bundle itself), not an existing file.
const PATH_OPS = new Set(["add-file", "root-symlink"]);

export function expandCases(base: string, root: string, templates: Template[]): Case[] {
  const files = listFiles(root);
  const cases: Case[] = [{ id: base, base }];
  for (const t of templates) {
    if (t.bases && !t.bases.includes(base)) continue;
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
        (prevention as Record<string, unknown>).digest = digestJson(body);
      }
      const { rootDigest: _dropped, ...unsigned } = manifest;
      manifest.rootDigest = digestJson(unsigned);
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
      const p = t.jsonPath!;
      if (t.delete) {
        const parent = at(v, p.slice(0, -1));
        if (Array.isArray(parent)) parent.splice(p[p.length - 1] as number, 1);
        else delete (parent as Record<string, Json>)[p[p.length - 1] as string];
      } else if (t.swapWith) {
        const a = at(v, p);
        const b = at(v, t.swapWith);
        setAt(v, p, b);
        setAt(v, t.swapWith, a);
      } else {
        setAt(v, p, t.valueFrom ? at(v, t.valueFrom) : t.value);
      }
      writeFileSync(path, `${JSON.stringify(v, null, 2)}\n`);
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

// maskGitDetail hides git's own stderr (it varies across git versions) but
// keeps the text FaultLine composes around it: the "; " join with Node's
// spawnSync error, and the "exit <status>" fallback.
export function maskGitDetail(detail: string): string {
  if (/^exit (?:null|-?\d+)$/.test(detail)) return detail;
  const m = /^(?:([\s\S]*); )?(spawnSync git [A-Z0-9_]+)$/.exec(detail);
  if (m) return m[1] === undefined ? m[2]! : `<GIT-STDERR>; ${m[2]}`;
  return "<GIT-STDERR>";
}

export function normalize(text: string, bundle: string): string {
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
  for (const label of GIT_LABELS) {
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
