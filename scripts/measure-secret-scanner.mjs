#!/usr/bin/env node
/**
 * Enhance measure-secret-scanner with corpus digest + Wilson score intervals.
 */
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, mkdirSync, readdirSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const corpusRoot = join(root, "benchmarks", "secret-scanner-corpus");
const labelsPath = join(corpusRoot, "labels.json");

function wilsonInterval(successes, n, z = 1.96) {
  if (n <= 0) return { low: null, high: null, n };
  const p = successes / n;
  const denom = 1 + (z * z) / n;
  const center = p + (z * z) / (2 * n);
  const margin = z * Math.sqrt((p * (1 - p) + (z * z) / (4 * n)) / n);
  return {
    low: Number(((center - margin) / denom).toFixed(4)),
    high: Number(((center + margin) / denom).toFixed(4)),
    n
  };
}

function corpusDigest() {
  const hash = createHash("sha256");
  const walk = (dir, prefix = "") => {
    for (const name of readdirSync(dir).sort()) {
      const absolute = join(dir, name);
      const relative = prefix ? `${prefix}/${name}` : name;
      const st = statSync(absolute);
      if (st.isDirectory()) walk(absolute, relative);
      else {
        hash.update(relative);
        hash.update("\0");
        hash.update(readFileSync(absolute));
        hash.update("\0");
      }
    }
  };
  walk(corpusRoot);
  return `sha256:${hash.digest("hex")}`;
}

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
  } else if (redacted) {
    knownMissDetected += 1;
    classification = "UNEXPECTED_DETECT";
  } else {
    knownMissCorrect += 1;
    classification = "EXPECTED_MISS";
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
const digest = corpusDigest();
const report = {
  schemaVersion: "faultline.secret-scanner-efficacy.v2",
  generatedAt: new Date().toISOString(),
  corpus: "benchmarks/secret-scanner-corpus",
  corpusDigest: digest,
  caseCount: labels.cases.length,
  coverage: "LIMITED",
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
  confidenceIntervals95: {
    recallOnLabeledSecrets: wilsonInterval(truePositive, detectable),
    falsePositiveRateOnBenign: wilsonInterval(falsePositive, benign)
  },
  rows
};

mkdirSync(join(root, "benchmarks"), { recursive: true });
const outJson = join(root, "benchmarks", "secret-scanner-efficacy.json");
writeFileSync(outJson, `${JSON.stringify(report, null, 2)}\n`, "utf8");

const efficacyMd = [
  "# Secret-scanner efficacy (SEC-09)",
  "",
  `Generated: ${report.generatedAt}`,
  `Corpus cases: ${report.caseCount}`,
  `Corpus digest: \`${digest}\``,
  `Coverage label: **LIMITED** (kept until a qualified external review expands trust)`,
  "",
  "## Observed rates",
  "",
  `| Metric | Point | 95% Wilson CI | n |`,
  `| --- | --- | --- | --- |`,
  `| Recall on labeled secrets | ${report.rates.recallOnLabeledSecrets?.toFixed(4) ?? "n/a"} | [${report.confidenceIntervals95.recallOnLabeledSecrets.low}, ${report.confidenceIntervals95.recallOnLabeledSecrets.high}] | ${detectable} |`,
  `| FP rate on benign | ${report.rates.falsePositiveRateOnBenign?.toFixed(4) ?? "n/a"} | [${report.confidenceIntervals95.falsePositiveRateOnBenign.low}, ${report.confidenceIntervals95.falsePositiveRateOnBenign.high}] | ${benign} |`,
  "",
  "Machine twin: `benchmarks/secret-scanner-efficacy.json`.",
  "Regenerate: `node scripts/expand-secret-scanner-corpus.mjs && pnpm measure:secret-scanner`.",
  ""
].join("\n");
writeFileSync(join(root, "docs", "secret-scanner-efficacy.md"), efficacyMd, "utf8");

process.stdout.write(`${JSON.stringify({ caseCount: report.caseCount, corpusDigest: digest, rates: report.rates, confidenceIntervals95: report.confidenceIntervals95 }, null, 2)}\n`);
process.stdout.write(`Wrote ${outJson}\n`);
