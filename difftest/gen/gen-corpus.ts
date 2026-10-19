// Regenerates difftest/testdata/golden/secret-corpus.jsonl from frozen TS:
//   pnpm build && pnpm exec tsx difftest/gen/gen-corpus.ts
// One line per corpus case: {id, redactedText, report} with the report as
// canonical JSON, from redactText(readFileSync(path, "utf8"), "corpus:<id>").
import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { canonicalJson } from "../../dist/canonical.js";
import { redactText } from "../../dist/redaction.js";

const repo = resolve(".");
const corpus = join(repo, "benchmarks", "secret-scanner-corpus");
const labels = JSON.parse(readFileSync(join(corpus, "labels.json"), "utf8")) as { cases: { id: string; path: string }[] };
const lines = labels.cases.map((c) => {
  const result = redactText(readFileSync(join(corpus, c.path), "utf8"), `corpus:${c.id}`);
  return JSON.stringify({ id: c.id, redactedText: result.value, report: canonicalJson(result.report) });
});
writeFileSync(join(repo, "difftest", "testdata", "golden", "secret-corpus.jsonl"), `${lines.join("\n")}\n`, "utf8");
console.log(`wrote ${lines.length} corpus goldens`);
