#!/usr/bin/env node
/**
 * Devpost / README claims linter (Bucket 1 #4).
 *
 * Every root digest, version string, pin tag, and feedback ID in the Devpost
 * paste surfaces must match a repo artifact. Banned phrases fail closed.
 *
 * Usage: node scripts/lint-devpost-claims.mjs
 */
import { readFileSync, existsSync } from "node:fs";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, "..");

function read(rel) {
  return readFileSync(join(REPO, rel), "utf8");
}

function mustExist(rel) {
  if (!existsSync(join(REPO, rel))) throw new Error(`missing artifact: ${rel}`);
}

const failures = [];
function fail(msg) {
  failures.push(msg);
}

const sampleRoot = read("docs/samples/self-incident-commit-proof/ROOT.sha256").trim();
mustExist("docs/samples/self-incident-commit-proof/ROOT.sha256");
mustExist("package.json");
mustExist("scripts/package-smoke.mjs");

const pkg = JSON.parse(read("package.json"));
const smoke = read("scripts/package-smoke.mjs");
const pinMatch = smoke.match(/pinnedRef\s*=\s*"([^"]+)"/);
const pinnedRef = pinMatch?.[1];
if (!pinnedRef) fail("scripts/package-smoke.mjs missing pinnedRef");

const surfaces = [
  "docs/devpost-paste-ready.md",
  "docs/devpost-description-draft.md",
  "README.md",
  "docs/video-teleprompter.md",
  "docs/build-week-submission-kit.md"
];

const docs = Object.fromEntries(surfaces.map((s) => [s, read(s)]));
const all = Object.values(docs).join("\n");

// Pin tag present in paste-ready + README
for (const s of ["docs/devpost-paste-ready.md", "README.md", "docs/video-teleprompter.md"]) {
  if (!docs[s].includes(pinnedRef)) fail(`${s} missing pin ${pinnedRef}`);
}

// Sample root must appear (full or unambiguous prefix) and match artifact
if (!docs["docs/devpost-paste-ready.md"].includes(sampleRoot)) {
  fail(`docs/devpost-paste-ready.md missing sample root ${sampleRoot}`);
}
if (!sampleRoot.startsWith("sha256:f85c446d")) {
  fail(`unexpected sample root artifact: ${sampleRoot}`);
}

// npm install claims must match the *judging pin* package version, not main.
// main may bump ahead of the frozen pin (e.g. 0.1.3) while paste-ready cites
// the published pin package (e.g. 0.1.2 on v0.1.9-buildweek).
let pinVersion = pkg.version;
if (pinnedRef) {
  const pinPkg = spawnSync("git", ["show", `${pinnedRef}:package.json`], {
    cwd: REPO,
    encoding: "utf8"
  });
  if (pinPkg.status === 0) {
    try {
      pinVersion = JSON.parse(pinPkg.stdout).version;
    } catch {
      fail(`cannot parse ${pinnedRef}:package.json`);
    }
  } else {
    fail(`cannot read ${pinnedRef}:package.json: ${pinPkg.stderr.trim()}`);
  }
}
const version = pinVersion;
if (docs["docs/devpost-paste-ready.md"].includes(`@mizore66/faultline@`) &&
    !docs["docs/devpost-paste-ready.md"].includes(`@mizore66/faultline@${version}`)) {
  fail(`devpost-paste-ready npm version must match pin ${pinnedRef} package.json ${version}`);
}

// Literal SHA for pin (paste-ready) must resolve to the tag
const shaMatch = docs["docs/devpost-paste-ready.md"].match(/literal SHA:\s*`?([0-9a-f]{40})`?/i);
if (shaMatch) {
  const listed = shaMatch[1];
  const peeled = spawnSync("git", ["rev-parse", `${pinnedRef}^{commit}`], {
    cwd: REPO,
    encoding: "utf8"
  });
  if (peeled.status !== 0) {
    fail(`cannot resolve ${pinnedRef}^{commit}: ${peeled.stderr}`);
  } else {
    const actual = peeled.stdout.trim();
    if (actual !== listed) {
      fail(`paste-ready literal SHA ${listed} !== ${pinnedRef}^{commit} ${actual}`);
    }
  }
} else {
  fail("docs/devpost-paste-ready.md missing literal SHA for pin commit");
}

// Feedback session ID
const feedbackId = "019f66bd-0ac1-78f3-8dc1-5968e4f2fa09";
if (!docs["docs/devpost-paste-ready.md"].includes(feedbackId)) {
  fail("devpost-paste-ready missing qualifying /feedback session id");
}

// External-01 root if mentioned
const extRoot = "sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1";
if (all.includes("831885ed") && !all.includes(extRoot) && !all.includes("831885ed72814e3c")) {
  fail("external-01 root abbreviated without matching retained digest");
}

// Historical root must not be presented as the judge-proof sample (denials OK)
{
  const tele = docs["docs/video-teleprompter.md"];
  for (const line of tele.split(/\r?\n/)) {
    if (/do\s+\*\*not\*\*|do not say|never say|avoid/i.test(line)) continue;
    if (/judge-proof/i.test(line) && /f6a391/i.test(line)) {
      fail("teleprompter must not equate judge-proof with historical f6a391 root: " + line.trim().slice(0, 120));
    }
  }
}

// Banned phrases (case-insensitive) — submission-facing surfaces only
const banned = [
  /\bsecurity audited\b/i,
  /\bproduction incident\b/i,
  /\binvisible(?:\s+overhead)?\b/i,
  /\bTURN_PROOF\b(?![^\n]*(?:until|reserved|not|unless|criteria))/i
];
const lintSurfaces = [
  "docs/devpost-paste-ready.md",
  "docs/devpost-description-draft.md",
  "docs/video-teleprompter.md"
];
for (const s of lintSurfaces) {
  const text = docs[s];
  for (const re of banned) {
    // Allow explicit denials in "do not claim" sections
    const lines = text.split(/\r?\n/);
    for (const line of lines) {
      if (/do not|don't|never|avoid|not claim|retracted|unless/i.test(line)) continue;
      if (re.test(line)) fail(`${s}: banned phrase matched ${re} on: ${line.trim().slice(0, 120)}`);
    }
  }
}

// Coverage matrix 8 scenarios
mustExist("benchmarks/report.json");
const matrix = JSON.parse(read("benchmarks/report.json"));
if (matrix.incidents !== 8 && matrix.count !== 8) {
  // report.json uses incidents key per explore note
  const n = matrix.incidents ?? matrix.count;
  if (n !== 8) fail(`benchmarks/report.json expected 8 incidents, got ${n}`);
}

if (failures.length > 0) {
  console.error("Devpost claims linter FAILED:");
  for (const f of failures) console.error(` - ${f}`);
  process.exit(1);
}
console.log("Devpost claims linter PASS");
console.log(` pin=${pinnedRef} sampleRoot=${sampleRoot} version=${version}`);
