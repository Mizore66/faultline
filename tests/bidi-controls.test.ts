import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * Reject Trojan-Source-style bidirectional override/isolate controls in
 * source and specification files. Ordinary typographic punctuation (em dash,
 * arrows) is allowed; these specific controls are not.
 *
 * Ranges covered:
 * - U+202A..U+202E (embedding / override)
 * - U+2066..U+2069 (isolate)
 * - U+200E, U+200F (LTR/RTL marks)
 * - U+061C (Arabic letter mark)
 */
const BIDI_CONTROL_RE = /[\u061C\u200E\u200F\u202A-\u202E\u2066-\u2069]/u;

const REPO_ROOT = fileURLToPath(new URL("..", import.meta.url));

const SCAN_ROOTS = ["src", "tests", "scripts", "docs", ".github"];

const SKIP_DIR_NAMES = new Set([
  "node_modules",
  "dist",
  ".git",
  "fallback-footage",
  "devpost-gallery",
  "samples"
]);

const TEXT_EXTENSIONS = new Set([
  ".ts",
  ".tsx",
  ".js",
  ".mjs",
  ".cjs",
  ".json",
  ".md",
  ".yml",
  ".yaml",
  ".txt",
  ".html",
  ".css"
]);

function shouldScanFile(absolutePath: string): boolean {
  const base = absolutePath.split(sep).pop() ?? "";
  if (base === "SUBMISSION_FROZEN" || base === "LICENSE" || base === "README.md") {
    return true;
  }
  const dot = base.lastIndexOf(".");
  if (dot < 0) return false;
  return TEXT_EXTENSIONS.has(base.slice(dot).toLowerCase());
}

function walk(directory: string, out: string[]): void {
  for (const entry of readdirSync(directory)) {
    if (SKIP_DIR_NAMES.has(entry)) continue;
    const absolute = join(directory, entry);
    const st = statSync(absolute);
    if (st.isDirectory()) {
      walk(absolute, out);
      continue;
    }
    if (st.isFile() && shouldScanFile(absolute)) {
      out.push(absolute);
    }
  }
}

describe("bidi / Trojan Source controls", () => {
  it("rejects bidi override/isolate controls in source and specification files", () => {
    const files: string[] = [];
    for (const root of SCAN_ROOTS) {
      walk(join(REPO_ROOT, root), files);
    }
    // Also scan top-level policy / entry docs
    for (const top of ["SUBMISSION_FROZEN", "README.md", "SECURITY.md", "action.yml"]) {
      files.push(join(REPO_ROOT, top));
    }

    expect(files.length).toBeGreaterThan(50);

    const offenders: string[] = [];
    for (const absolute of files) {
      const text = readFileSync(absolute, "utf8");
      if (!BIDI_CONTROL_RE.test(text)) continue;
      const rel = relative(REPO_ROOT, absolute).split(sep).join("/");
      const lines = text.split(/\r?\n/);
      for (let i = 0; i < lines.length; i++) {
        const match = lines[i]!.match(BIDI_CONTROL_RE);
        if (match) {
          const code = match[0]!.codePointAt(0)!.toString(16).toUpperCase().padStart(4, "0");
          offenders.push(`${rel}:${i + 1} U+${code}`);
        }
      }
    }

    expect(offenders, offenders.join("\n")).toEqual([]);
  });
});
