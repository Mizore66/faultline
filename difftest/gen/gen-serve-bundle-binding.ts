// Supplemental serve-bundle binding generator. Run from the repository root on the
// pinned Linux oracle (Node 22.22.2) after building dist/:
//   pnpm exec tsx difftest/gen/gen-serve-bundle-binding.ts [--capture | --check]
// Modes are mutually exclusive; the default is --check.
//  --capture  FIRST capture only. Builds the five records from the committed
//             original serve-bundle corpus, requires each to be accepted by the
//             real TS minimization verifier with its exact recomputed digest, runs
//             the actual `node dist/cli.js serve --bundle ...` for each, and writes
//             difftest/testdata/serve-bundle-binding only if it is absent.
//  --check    Strictly validates the retained corpus, then replays the retained
//             inputs (never regenerating a record) through the actual CLI and
//             compares every golden byte; it writes nothing.
// The original corpus (difftest/testdata/serve-bundle) is only ever read.
// Environment: SERVE_BUNDLE_BINDING_CORPUS may stage the new corpus elsewhere and
// SERVE_BUNDLE_CORPUS the original, never to overwrite committed data.
import { resolve } from "node:path";
import {
  BINDING_NAMES, bindingId, bindingInputPath, buildBindingRecords, captureBindingCase, loadBindingSource, makeBindingCases,
  serializeRecord, validateBindingCorpus, writeBindingCorpus, type BindingGolden, type BindingName
} from "./serve-bundle-binding.js";
import { assertAbsentDestination, runPreflight } from "./serve-bundle-corpus.js";

const parent = resolve(process.env.SERVE_BUNDLE_CORPUS ?? "difftest/testdata/serve-bundle");
const target = resolve(process.env.SERVE_BUNDLE_BINDING_CORPUS ?? "difftest/testdata/serve-bundle-binding");

export function parseBindingMode(argv: readonly string[]): "capture" | "check" {
  const modes = argv.filter((arg) => arg === "--capture" || arg === "--check");
  const unknown = argv.filter((arg) => arg !== "--capture" && arg !== "--check");
  if (unknown.length > 0) throw new Error(`unknown argument: ${unknown.join(" ")}`);
  if (modes.length > 1) throw new Error("--capture and --check are mutually exclusive");
  return modes[0] === "--capture" ? "capture" : "check";
}

function capture(): void {
  assertAbsentDestination(target);
  runPreflight();
  const source = loadBindingSource(parent);
  const records = buildBindingRecords(source.minimization, source.investigation);
  const texts = {} as Record<BindingName, string>;
  for (const name of BINDING_NAMES) texts[name] = serializeRecord(records[name]);
  const cases = makeBindingCases(texts, source.proofRoot); // verifier-valid, exact digest, or throws
  // Stage the inputs in the destination-shaped tree the capture reads from.
  const staging = resolve(process.env.SERVE_BUNDLE_BINDING_STAGING ?? `${target}.staging`);
  assertAbsentDestination(staging);
  writeBindingCorpus(staging, { texts, cases, goldens: [], source });
  const goldens: BindingGolden[] = cases.map((row) => captureBindingCase(parent, staging, row).golden);
  // Rewrite is not allowed: build the final tree beside it and publish only if absent.
  assertAbsentDestination(target);
  writeBindingCorpus(target, { texts, cases, goldens, source });
  validateBindingCorpus(target, parent);
  console.log(`first capture written to ${target}; staged inputs preserved at ${staging}`);
}

function check(): void {
  const validated = validateBindingCorpus(target, parent);
  runPreflight();
  const failures: string[] = [];
  validated.cases.forEach((row, i) => {
    const got = captureBindingCase(parent, target, row).golden;
    const want = validated.goldens[i]!;
    if (JSON.stringify(got) !== JSON.stringify(want)) failures.push(`${bindingId(BINDING_NAMES[i]!)}: process output differs from the retained golden`);
  });
  if (failures.length > 0) throw new Error(`binding recapture differs from the committed corpus:\n${failures.join("\n")}`);
  console.log(`binding recapture identical for ${validated.cases.length} cases (${BINDING_NAMES.map(bindingInputPath).join(", ")})`);
}

if (process.argv[1] !== undefined && resolve(process.argv[1]).endsWith("gen-serve-bundle-binding.ts")) {
  try {
    if (parseBindingMode(process.argv.slice(2)) === "capture") capture();
    else check();
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
