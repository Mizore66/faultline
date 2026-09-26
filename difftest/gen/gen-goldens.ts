// Regenerates difftest/testdata/golden/*.jsonl from frozen TS.
//   pnpm build && pnpm exec tsx difftest/gen/gen-goldens.ts
import { spawn } from "node:child_process";
import { cpSync, existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { applyMutation, baseRoot, expandCases, normalize, treeDigest, type Case, type Template } from "./cases.js";

const repo = resolve(".");
const cli = join(repo, "dist", "cli.js");
const home = mkdtempSync(join(tmpdir(), "faultline-difftest-home-"));
writeFileSync(join(home, "empty.gitconfig"), "");
// The frozen verifier's temporary `git init --bare` follows GIT_DEFAULT_HASH,
// so it is pinned; base-env.json overrides it per base (git-sha256).
const env = {
  ...process.env, HOME: home, USERPROFILE: home, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(home, "empty.gitconfig"),
  LC_ALL: "C.UTF-8", GIT_DEFAULT_HASH: "sha1"
};
const baseEnv = JSON.parse(readFileSync(join(repo, "difftest/testdata/base-env.json"), "utf8")) as Record<string, Record<string, string>>;

// Goldens must come from a build of the current src/.
function newestMtime(dir: string): number {
  let newest = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    newest = Math.max(newest, entry.isDirectory() ? newestMtime(path) : statSync(path).mtimeMs);
  }
  return newest;
}
if (!existsSync(cli) || statSync(cli).mtimeMs < newestMtime(join(repo, "src"))) {
  console.error("dist/cli.js is missing or older than src/: run `pnpm build` first");
  process.exit(1);
}
const templates = JSON.parse(readFileSync(join(repo, "difftest/testdata/mutations.json"), "utf8")) as Template[];
const ROOT_BAD = `sha256:${"0".repeat(64)}`;

function run(base: string, args: string[]): Promise<{ stdout: string; stderr: string; exit: number }> {
  return new Promise((done) => {
    const child = spawn(process.execPath, [cli, "verify", ...args], { env: { ...env, ...baseEnv[base] }, cwd: repo });
    const out: Buffer[] = [];
    const err: Buffer[] = [];
    child.stdout.on("data", (b) => out.push(b));
    child.stderr.on("data", (b) => err.push(b));
    child.on("close", (code) => done({ stdout: Buffer.concat(out).toString("utf8"), stderr: Buffer.concat(err).toString("utf8"), exit: code ?? -1 }));
  });
}

async function golden(c: Case, root: string, rootDigest: string): Promise<string[]> {
  const work = mkdtempSync(join(tmpdir(), "faultline-difftest-"));
  const bundle = join(work, "bundle");
  try {
    cpSync(root, bundle, { recursive: true });
    applyMutation(bundle, c);
    const digest = treeDigest(bundle);
    const invocations: Array<[string, string[]]> = c.template
      ? [["plain", [bundle]]]
      : [
        ["plain", [bundle]], ["root-ok", [bundle, "--expect-root", rootDigest]], ["root-bad", [bundle, "--expect-root", ROOT_BAD]],
        ["root-malformed", [bundle, "--expect-root", "not-a-digest"]]
      ];
    const lines: string[] = [];
    for (const [inv, args] of invocations) {
      const r = await run(c.base, args);
      lines.push(JSON.stringify({ case: c.id, inv, treeDigest: digest, stdout: normalize(r.stdout, bundle), stderr: normalize(r.stderr, bundle), exit: r.exit }));
    }
    return lines;
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

const basesDir = join(repo, "difftest/testdata/bases");
for (const base of readdirSync(basesDir).sort()) {
  const root = join(basesDir, base);
  const cases = expandCases(base, root, templates);
  const rootDigest = baseRoot(root, base);
  const results: string[][] = new Array(cases.length);
  let next = 0;
  await Promise.all(Array.from({ length: 8 }, async () => {
    while (next < cases.length) {
      const index = next++;
      results[index] = await golden(cases[index]!, root, rootDigest);
    }
  }));
  writeFileSync(join(repo, "difftest/testdata/golden", `${base}.jsonl`), `${results.flat().join("\n")}\n`);
  console.log(`${base}: ${cases.length} cases`);
}
rmSync(home, { recursive: true, force: true });
