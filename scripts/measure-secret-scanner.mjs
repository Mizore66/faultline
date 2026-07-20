#!/usr/bin/env node
/**
 * SEC-04: measure secret-scanner efficacy on the labeled corpus.
 * Emits rates only from observed redactText results — no invented conclusions.
 */
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const corpusRoot = join(root, "benchmarks", "secret-scanner-corpus");
const labelsPath = join(corpusRoot, "labels.json");

const redactionModule = await import(pathToFileURL(join(root, "dist", "redaction.js")).href);
const { redactText } = redactionModule;

const labels = JSON.parse(readFileSync(labelsPath, "utf8"));
const rows = [];

let truePositive = 0;
let falseNegative = 0;
let falsePositive = 0;
let trueNegative = 0;
let knownMissDetected = 0;
let knownMissCorrect = 0;

for (const item of labels.cases) {
  const text = readFileSync(join(corpusRoot, item.path), "utf8");
  const result = redactText(text, `corpus:${item.id}`);
  const redacted = result.report.redacted === true;
  const kinds = result.report.occurrences.map((o) => o.kind);
  let classification;
  if (item.label === "true_secret") {
    if (redacted) {
      truePositive += 1;
      classification = "TP";
    } else {
      falseNegative += 1;
      classification = "FN";
    }
  } else if (item.label === "false_positive") {
    if (redacted) {
      falsePositive += 1;
      classification = "FP";
    } else {
      trueNegative += 1;
      classification = "TN";
    }
  } else {
    // should_miss: expected to miss; detecting is over-eager, missing matches the limitation claim.
    if (redacted) {
      knownMissDetected += 1;
      classification = "UNEXPECTED_DETECT";
    } else {
      knownMissCorrect += 1;
      classification = "EXPECTED_MISS";
    }
  }
  rows.push({
    id: item.id,
    label: item.label,
    redacted,
    kinds,
    classification,
    expectedKind: item.expectedKind ?? null
  });
}

const detectable = truePositive + falseNegative;
const benign = falsePositive + trueNegative;
const report = {
  schemaVersion: "faultline.secret-scanner-efficacy.v1",
  generatedAt: new Date().toISOString(),
  corpus: "benchmarks/secret-scanner-corpus",
  coverage: labels.coverage,
  limitations: labels.limitations,
  counts: {
    truePositive,
    falseNegative,
    falsePositive,
    trueNegative,
    knownMissDetected,
    knownMissCorrect,
    detectable,
    benign
  },
  rates: {
    recallOnLabeledSecrets: detectable === 0 ? null : truePositive / detectable,
    falsePositiveRateOnBenign: benign === 0 ? null : falsePositive / benign,
    knownLimitationMissRate:
      knownMissDetected + knownMissCorrect === 0
        ? null
        : knownMissCorrect / (knownMissDetected + knownMissCorrect)
  },
  rows
};

mkdirSync(join(root, "benchmarks", "secret-scanner-corpus"), { recursive: true });
const outJson = join(root, "benchmarks", "secret-scanner-efficacy.json");
writeFileSync(outJson, `${JSON.stringify(report, null, 2)}\n`, "utf8");
process.stdout.write(JSON.stringify(report, null, 2) + "\n");
process.stdout.write(`Wrote ${outJson}\n`);
