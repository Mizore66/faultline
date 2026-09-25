import { lstatSync, mkdirSync, readFileSync, readdirSync, readlinkSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { digestJson, sha256 } from "../../src/canonical.js";

export type Template = {
  id: string; op: string; file?: string; path?: string; offset?: number; target?: string; content?: string;
  find?: string; replace?: string; hex?: string; key?: string; value?: unknown; rehash?: boolean;
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
    default: return true;
  }
}

export function expandCases(base: string, root: string, templates: Template[]): Case[] {
  const files = listFiles(root);
  const cases: Case[] = [{ id: base, base }];
  for (const t of templates) {
    const targets = t.op === "add-file"
      ? [t.path!]
      : files.filter((f) => t.file === "*" || (t.file === "*.json" ? f.endsWith(".json") : t.file === f));
    for (const file of targets) {
      if (t.op !== "add-file" && !applicable(t, join(root, file))) continue;
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

export function applyMutation(root: string, c: Case): void {
  const t = c.template;
  if (!t) return;
  const path = join(root, c.file!);
  switch (t.op) {
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

export function normalize(text: string, bundle: string): string {
  let out = text;
  for (const p of new Set([bundle, realpathSync(bundle)])) out = out.split(p).join("<BUNDLE>");
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
      out = `${out.slice(0, from)}<GIT-DETAIL>${out.slice(to)}`;
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
