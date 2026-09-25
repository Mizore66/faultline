// Regenerates difftest/testdata/golden/*.jsonl from frozen TS.
//   pnpm build && pnpm exec tsx difftest/gen/gen-goldens.ts
import { spawn } from "node:child_process";
import { cpSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { applyMutation, baseRoot, expandCases, normalize, treeDigest, type Case, type Template } from "./cases.js";

const repo = resolve(".");
const cli = join(repo, "dist", "cli.js");
const home = mkdtempSync(join(tmpdir(), "faultline-difftest-home-"));
writeFileSync(join(home, "empty.gitconfig"), "");
const env = { ...process.env, HOME: home, USERPROFILE: home, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(home, "empty.gitconfig"), LC_ALL: "C.UTF-8" };
const templates = JSON.parse(readFileSync(join(repo, "difftest/testdata/mutations.json"), "utf8")) as Template[];
const ROOT_BAD = `sha256:${"0".repeat(64)}`;

function run(args: string[]): Promise<{ stdout: string; stderr: string; exit: number }> {
  return new Promise((done) => {
    const child = spawn(process.execPath, [cli, "verify", ...args], { env, cwd: repo });
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
      : [["plain", [bundle]], ["root-ok", [bundle, "--expect-root", rootDigest]], ["root-bad", [bundle, "--expect-root", ROOT_BAD]]];
    const lines: string[] = [];
    for (const [inv, args] of invocations) {
      const r = await run(args);
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
