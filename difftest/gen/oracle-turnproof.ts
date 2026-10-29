// Oracle ops for the turn proof bundle port (slice 4b). The unexported
// functions of src/turn-proof-bundle.ts are evaluated from the compiled
// dist/turn-proof-bundle.js, the way safeGitLists evaluates its constant.
import { readFileSync, readdirSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import nodePath from "node:path";
import { z, ZodError } from "zod";
import { runHardenedGit, toGitPath } from "../../src/git-materialization.js";
import { canonicalJson, digestJson } from "../../src/canonical.js";
import { CodexLifecycleLedgerSchema, verifyCodexLifecycleLedger } from "../../src/ledger.js";
import { turnStatesFromLedger } from "../../src/turn-investigation.js";
import { validateTurnInvestigationProofSemantics, writeTurnInvestigationProofBundle } from "../../src/turn-proof-bundle.js";
import { TurnInvestigationResultSchema } from "../../src/turn-investigation.js";
import { nativeTurnFixture } from "./fixtures.js";

type Handler = (args: any) => unknown;

const distSource = (): string => readFileSync(nodePath.join(process.cwd(), "dist", "turn-proof-bundle.js"), "utf8");

// The source text of a top-level function (sync or async) in the dist file.
function take(src: string, name: string): string {
  let start = src.indexOf(`\nfunction ${name}(`);
  const asyncStart = src.indexOf(`\nasync function ${name}(`);
  if (start < 0 || (asyncStart >= 0 && asyncStart < start)) start = asyncStart;
  const end = src.indexOf("\n}\n", start);
  if (start < 0 || end < 0) throw new Error(`${name} not found in dist/turn-proof-bundle.js`);
  return src.slice(start + 1, end + 2);
}

let catalogCache: ReturnType<typeof buildCatalogFns> | undefined;
const catalogFns = () => (catalogCache ??= buildCatalogFns());
function buildCatalogFns(): { parseHashCatalog: (hashes: Buffer, errors: string[]) => Map<string, string> } {
  const src = distSource();
  return new Function(
    `${take(src, "isSafeRelativeArtifactPath")}\n${take(src, "parseHashCatalog")}\nreturn { parseHashCatalog };`
  )();
}

let packCache: ReturnType<typeof buildPackFns> | undefined;
const packFns = () => (packCache ??= buildPackFns());
function buildPackFns(): {
  packTurnTrees: (repository: string, states: { treeDigest: string }[]) => Promise<Buffer>;
  verifyTreesPack: (pack: Buffer, states: { treeDigest: string }[], errors: string[]) => Promise<void>;
} {
  const src = distSource();
  const max = src.match(/const MAX_SOURCE_ARTIFACT_BYTES = ([^;]+);/);
  if (!max) throw new Error("MAX_SOURCE_ARTIFACT_BYTES not found in dist/turn-proof-bundle.js");
  return new Function(
    "runHardenedGit", "toGitPath", "mkdtempSync", "mkdirSync", "writeFileSync", "rmSync", "tmpdir", "join",
    `const MAX_SOURCE_ARTIFACT_BYTES = ${max[1]};\n${take(src, "packTurnTrees")}\n${take(src, "removeTemporaryDirectory")}\n${take(src, "verifyTreesPack")}\nreturn { packTurnTrees, verifyTreesPack };`
  )(runHardenedGit, toGitPath, mkdtempSync, mkdirSync, writeFileSync, rmSync, tmpdir, nodePath.join);
}

let bindCache: ((ledger: unknown, result: unknown) => { binding: unknown; ledger: unknown }) | undefined;
// bindTurnLifecycle is not exported: it is evaluated from dist with the
// module-level names it reads (LifecycleBoundSchema and sameCanonical from
// dist's own text).
function bindFn() {
  if (bindCache) return bindCache;
  const src = distSource();
  const sha = src.match(/const SHA256_DIGEST = [^;]+;/);
  const digest = src.match(/const DigestSchema = [^;]+;/);
  const bound = src.match(/const LifecycleBoundSchema = z\.object\(\{[\s\S]*?\}\)\.strict\(\);/);
  const same = src.match(/function sameCanonical\(left, right\) \{[\s\S]*?\n\}/);
  if (!sha || !digest || !bound || !same) throw new Error("bindTurnLifecycle dependencies not found in dist/turn-proof-bundle.js");
  bindCache = new Function(
    "z", "digestJson", "canonicalJson", "CodexLifecycleLedgerSchema", "verifyCodexLifecycleLedger", "turnStatesFromLedger",
    `${sha[0]}\n${digest[0]}\n${bound[0]}\n${same[0]}\n${take(src, "bindTurnLifecycle")}\nreturn bindTurnLifecycle;`
  )(z, digestJson, canonicalJson, CodexLifecycleLedgerSchema, verifyCodexLifecycleLedger, turnStatesFromLedger) as NonNullable<typeof bindCache>;
  return bindCache;
}
const states = (trees: string[]) => trees.map((treeDigest) => ({ treeDigest }));
const leftovers = (): string[] => readdirSync(tmpdir()).filter((n) => n.startsWith("faultline-turn-proof-pack-")).sort();

const outcome = (fn: () => unknown): string => {
  try {
    return JSON.stringify({ value: fn() });
  } catch (error) {
    return JSON.stringify({ threw: (error as Error).message, zod: error instanceof ZodError });
  }
};

export const turnproofOps: Record<string, Handler> = {
  // validateTurnInvestigationProofSemantics({result, frozen}): { value: errors } or { threw, zod }.
  validateTurnSemantics: ({ result, frozen }: { result: any; frozen: any }) =>
    outcome(() => validateTurnInvestigationProofSemantics(result, frozen)),
  // bindTurnLifecycle(ledger, result): { value: { binding, ledger } } or { threw, zod }.
  bindTurnLifecycle: ({ ledger, result }: { ledger: any; result: any }) =>
    outcome(() => bindFn()(ledger, result)),
  // parseHashCatalog on the bytes in hex: JSON of { errors, entries: [[artifact, digest], ...] }.
  // Map iteration order is first insertion, as the Go Catalog.Keys.
  parseTurnHashCatalog: ({ hex }: { hex: string }) => {
    const errors: string[] = [];
    const catalog = catalogFns().parseHashCatalog(Buffer.from(hex, "hex"), errors);
    return JSON.stringify({ errors, entries: [...catalog.entries()] });
  },
  // packTurnTrees: the pack in hex, or "THREW:<message>". Run it unseeded or
  // seeded; it creates no temporary directory.
  packTurnTrees: async ({ repository, trees }: { repository: string; trees: string[] }) => {
    try {
      return (await packFns().packTurnTrees(repository, states(trees))).toString("hex");
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  // verifyTreesPack: JSON of { errors, threw, leftover } where leftover is the
  // number of faultline-turn-proof-pack-* directories left in tmpdir() that
  // were not there before. Start the oracle seeded to compare mkdtemp names.
  verifyTreesPack: async ({ hex, trees }: { hex: string; trees: string[] }) => {
    const before = new Set(leftovers());
    const errors: string[] = [];
    let threw: string | null = null;
    try {
      await packFns().verifyTreesPack(Buffer.from(hex, "hex"), states(trees), errors);
    } catch (error) {
      threw = (error as Error).message;
    }
    const left = leftovers().filter((n) => !before.has(n));
    return JSON.stringify({ errors, threw, leftover: left.length });
  },
  // Task 8. nativeTurnFixture (fixtures.ts) over an observed result: the
  // promoted investigation as JSON, or "THREW:<message>".
  promoteNativeTurn: ({ observed }: { observed: unknown }) => {
    try {
      return JSON.stringify(nativeTurnFixture(TurnInvestigationResultSchema.parse(observed)));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
  // writeTurnInvestigationProofBundle with PATH, FAULTLINE_ARGV_LOG (the git
  // recorder's log) and, when given, the working directory replaced:
  // JSON.stringify of the written bundle (key order kept), or
  // "THREW:<message>". Start the oracle seeded to compare the staging UUID,
  // the verifier's temporary directory and the generatedAt default.
  writeTurnInvestigationProofBundle: async ({ output, result, frozen, options, path, argvLog, cwd }: {
    output: string; result: unknown; frozen: unknown; options: any; path: string; argvLog: string; cwd?: string;
  }) => {
    const saved = { PATH: process.env.PATH, FAULTLINE_ARGV_LOG: process.env.FAULTLINE_ARGV_LOG };
    const savedCwd = process.cwd();
    process.env.PATH = path;
    process.env.FAULTLINE_ARGV_LOG = argvLog;
    try {
      if (cwd !== undefined) process.chdir(cwd);
      return JSON.stringify(await writeTurnInvestigationProofBundle(output, result as never, frozen as never, options));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    } finally {
      process.chdir(savedCwd);
      for (const [key, value] of Object.entries(saved)) {
        if (value === undefined) delete process.env[key];
        else process.env[key] = value;
      }
    }
  },
};
