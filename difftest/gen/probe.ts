// Probe: applies templates (JSON array in argv[2]) to their bases with the
// harness mutation engine and prints what frozen TS verify says.
//   pnpm exec tsx difftest/gen/probe.ts '[{"id":"x","op":"json-edit",...,"bases":["git-two-states"]}]'
import { spawnSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { applyMutation, caseEnv, expandCases, normalize, type Template } from "./cases.js";

const repo = resolve(".");
const templates = JSON.parse(process.argv[2]!) as Template[];
const baseEnv = JSON.parse(readFileSync(join(repo, "difftest/testdata/base-env.json"), "utf8")) as Record<string, Record<string, string>>;
for (const base of [...new Set(templates.flatMap((t) => t.bases ?? []))]) {
  const root = join(repo, "difftest/testdata/bases", base);
  for (const c of expandCases(base, root, templates).slice(1)) {
    const work = mkdtempSync(join(tmpdir(), "faultline-probe-"));
    const bundle = join(work, "bundle");
    cpSync(root, bundle, { recursive: true });
    applyMutation(bundle, c);
    const r = spawnSync(process.execPath, [join(repo, "dist/cli.js"), "verify", bundle], { env: caseEnv(c, { ...process.env, GIT_DEFAULT_HASH: "sha1", ...baseEnv[base] }), maxBuffer: 1 << 28 });
    console.log(`== ${c.id} exit ${r.status}\n${normalize(r.stdout.toString(), bundle).split("\n").filter((l) => l.startsWith("- ")).join("\n")}`);
    rmSync(work, { recursive: true, force: true });
  }
}
