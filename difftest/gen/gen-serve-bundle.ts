// Serve-bundle oracle generator. Run from the repository root on the pinned
// Linux oracle (Node 22.22.2) after building dist/:
//   pnpm exec tsx difftest/gen/gen-serve-bundle.ts [--capture-fixtures | --recapture | --check] [--only <exact-case-id>]
// Modes are mutually exclusive; the default is --check.
//  --capture-fixtures  FIRST capture only. Builds the coherent fixture in fresh
//                      owned staging, writes the new corpus only if its
//                      destination is absent, then captures every case.
//  --recapture         Never rebuilds inputs. Copies committed inputs into fresh
//                      scratch, recaptures, compares bytes/inventory, writes
//                      nothing under the corpus.
//  --check             Strictly validates metadata/inventory/case alignment,
//                      then performs the same no-write recapture.
//  --only <id>         Diagnostic selection; it can never mark whole-corpus
//                      acceptance (the run says so and exits nonzero on any
//                      difference, but prints "PARTIAL").
// Environment: a corpus directory override is accepted only for staging
// (SERVE_BUNDLE_CORPUS), never to overwrite committed data.
import { cpSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { serveBundleCases } from "./serve-bundle-cases.js";
import { buildServeBundleFixtures } from "./serve-bundle-fixtures.js";
import { buildServeBundleMutations, collectPrivateSentinels } from "./serve-bundle-variants.js";
import { captureAll } from "./serve-bundle-orchestrate.js";
import {
  BASELINE_TEXT, GITATTRIBUTES_TEXT, assertAbsentDestination, checkCorpusRecords, computeCoherentDigests,
  inventoryDirectory, parseModes, runPreflight, serializeFixturesFile, validateCorpus, type FixturesFile
} from "./serve-bundle-corpus.js";

const corpusDirectory = resolve(process.env.SERVE_BUNDLE_CORPUS ?? "difftest/testdata/serve-bundle");

function pinGit(): void {
  const work = mkdtempSync(join(tmpdir(), "faultline-serve-gen-"));
  writeFileSync(join(work, "empty.gitconfig"), "");
  Object.assign(process.env, {
    GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(work, "empty.gitconfig"),
    GIT_AUTHOR_DATE: "2026-07-16T10:00:00Z", GIT_COMMITTER_DATE: "2026-07-16T10:00:00Z", GIT_DEFAULT_HASH: "sha1"
  });
}


async function captureFixtures(): Promise<void> {
  assertAbsentDestination(corpusDirectory);
  runPreflight();
  pinGit();
  // Stage in an owned tree and keep it. The corpus is written only if its destination is
  // absent: a recursive copy (errorOnExist, no force) runs after full validation. This is
  // not an atomic publish; a failed copy can leave a partial destination, which is then
  // preserved and must be reviewed, never overwritten.
  const staging = mkdtempSync(join(tmpdir(), "faultline-serve-stage-"));
  const coherent = join(staging, "inputs", "coherent");
  mkdirSync(join(staging, "inputs"), { recursive: true });
  await buildServeBundleFixtures(coherent);
  buildServeBundleMutations(coherent);
  const digests = computeCoherentDigests(coherent);
  const file: FixturesFile = {
    schema: "faultline.serve-bundle-fixtures.v1",
    fixtures: [{ id: "coherent", root: "inputs/coherent", files: inventoryDirectory(coherent), digests, privateSentinels: collectPrivateSentinels(coherent) }]
  };
  writeFileSync(join(staging, "fixtures.json"), serializeFixturesFile(file));
  writeFileSync(join(staging, "baseline.json"), BASELINE_TEXT);
  writeFileSync(join(staging, ".gitattributes"), GITATTRIBUTES_TEXT);
  const rows = serveBundleCases(staging);
  writeFileSync(join(staging, "cases.jsonl"), rows.map((r) => `${JSON.stringify(r)}\n`).join(""));
  const rendered = await captureAll(staging, rows);
  console.log(`per-case capture scratch directories preserved: ${rendered.length}`);
  mkdirSync(join(staging, "pages"));
  for (const r of rendered) if (r.page !== null && r.pageName !== null) writeFileSync(join(staging, "pages", r.pageName), r.page);
  writeFileSync(join(staging, "golden.jsonl"), rendered.map((r) => `${r.goldenLine}\n`).join(""));
  validateCorpus(staging);
  checkCorpusRecords(staging, rows);
  assertAbsentDestination(corpusDirectory);
  mkdirSync(dirname(corpusDirectory), { recursive: true });
  cpSync(staging, corpusDirectory, { recursive: true, errorOnExist: true, force: false });
  console.log(`first capture written to ${corpusDirectory}; staging preserved at ${staging}`);
}

async function recapture(only: string | null): Promise<void> {
  // Strict validation first (metadata, inventory), then the no-write comparison.
  validateCorpus(corpusDirectory);
  const all = serveBundleCases(corpusDirectory);
  const committed = checkCorpusRecords(corpusDirectory, all);
  const selected = only === null ? all : all.filter((row) => row.id === only);
  if (selected.length === 0) throw new Error(`--only ${only}: no such case`);
  runPreflight();
  const rendered = await captureAll(corpusDirectory, selected);
  const failures: string[] = [];
  selected.forEach((row, i) => {
    const index = all.findIndex((r) => r.id === row.id);
    const want = committed[index]!;
    const got = rendered[i]!;
    if (got.goldenLine !== want.line) failures.push(`${row.id}: golden differs`);
    const a = got.page; const b = want.page;
    if ((a === null) !== (b === null) || (a !== null && b !== null && a.compare(b) !== 0)) failures.push(`${row.id}: page bytes differ`);
  });
  const after = JSON.stringify(validateCorpus(corpusDirectory));
  if (after !== JSON.stringify(JSON.parse(readFileSync(join(corpusDirectory, "fixtures.json"), "utf8")))) failures.push("fixtures inventory changed");
  if (failures.length > 0) throw new Error(`recapture differs from the committed corpus:\n${failures.join("\n")}`);
  console.log(only === null ? `recapture identical for ${selected.length} cases` : `PARTIAL (--only ${only}): not whole-corpus acceptance`);
}

async function main(): Promise<void> {
  const { mode, only } = parseModes(process.argv.slice(2));
  if (mode === "capture-fixtures") await captureFixtures();
  else await recapture(only);
}

main().catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
