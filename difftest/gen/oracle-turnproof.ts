// Oracle ops for the turn proof bundle port (slice 4b). The unexported
// functions of src/turn-proof-bundle.ts are evaluated from the compiled
// dist/turn-proof-bundle.js, the way safeGitLists evaluates its constant.
import { readFileSync, readdirSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import nodePath from "node:path";
import { runHardenedGit, toGitPath } from "../../src/git-materialization.js";

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

function catalogFns(): { parseHashCatalog: (hashes: Buffer, errors: string[]) => Map<string, string> } {
  const src = distSource();
  return new Function(
    `${take(src, "isSafeRelativeArtifactPath")}\n${take(src, "parseHashCatalog")}\nreturn { parseHashCatalog };`
  )();
}

function packFns(): {
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

const states = (trees: string[]) => trees.map((treeDigest) => ({ treeDigest }));
const leftovers = (): string[] => readdirSync(tmpdir()).filter((n) => n.startsWith("faultline-turn-proof-pack-")).sort();

export const turnproofOps: Record<string, Handler> = {
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
};
