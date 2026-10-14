#!/usr/bin/env node
/**
 * SEC-09: expand the labeled secret-scanner corpus to ≥100 cases across families.
 * Generates synthetic, non-live credentials for measurement only.
 */
import { mkdirSync, writeFileSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createHash } from "node:crypto";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const corpusRoot = join(root, "benchmarks", "secret-scanner-corpus");

function h(seed) {
  return createHash("sha256").update(String(seed)).digest("hex");
}

function b64(seed) {
  return Buffer.from(h(seed), "hex").toString("base64");
}

const readFileSync_ = (buf) => buf.toString("utf8"); // same lossy decoding as readFileSync(p, "utf8")
const cases = [];

function add(id, label, relPath, body, extra = {}) {
  const absolute = join(corpusRoot, relPath);
  mkdirSync(dirname(absolute), { recursive: true });
  if (Buffer.isBuffer(body)) writeFileSync(absolute, body);
  else writeFileSync(absolute, body.endsWith("\n") ? body : `${body}\n`, "utf8");
  cases.push({ id, path: relPath.replace(/\\/g, "/"), label, ...extra });
}

// Preserve hand-authored seed cases if present.
try {
  const existing = JSON.parse(readFileSync(join(corpusRoot, "labels.json"), "utf8"));
  for (const item of existing.cases ?? []) {
    if (!cases.some((c) => c.id === item.id)) cases.push(item);
  }
} catch {
  // fresh corpus
}

const seedIds = new Set(cases.map((c) => c.id));

function ensure(id, label, relPath, body, extra = {}) {
  if (seedIds.has(id)) return;
  add(id, label, relPath, body, extra);
  seedIds.add(id);
}

// --- true_secret families (detectable) ---
for (let i = 0; i < 20; i += 1) {
  const suffix = h(`openai-${i}`).slice(0, 32);
  ensure(
    `openai-key-${i}`,
    "true_secret",
    `true_secret/gen-openai-${i}.txt`,
    `OPENAI_API_KEY=sk-proj-${suffix}${suffix.slice(0, 8)}`,
    { expectedKind: "OPENAI_API_KEY", family: "openai" }
  );
}
for (let i = 0; i < 15; i += 1) {
  const token = `ghp_${h(`gh-${i}`).slice(0, 36)}`;
  ensure(
    `github-token-${i}`,
    "true_secret",
    `true_secret/gen-github-${i}.txt`,
    `Authorization: token ${token}`,
    { expectedKind: "GITHUB_TOKEN", family: "github" }
  );
}
for (let i = 0; i < 12; i += 1) {
  const key = `AKIA${h(`aws-${i}`).slice(0, 16).toUpperCase()}`;
  ensure(
    `aws-access-${i}`,
    "true_secret",
    `true_secret/gen-aws-access-${i}.txt`,
    `AWS_ACCESS_KEY_ID=${key}`,
    { expectedKind: "AWS_ACCESS_KEY_ID", family: "aws" }
  );
}
for (let i = 0; i < 12; i += 1) {
  const secret = b64(`aws-secret-${i}`).replace(/=+$/, "").slice(0, 40);
  ensure(
    `aws-secret-${i}`,
    "true_secret",
    `true_secret/gen-aws-secret-${i}.txt`,
    `aws_secret_access_key=${secret}`,
    { expectedKind: "AWS_SECRET_ACCESS_KEY", family: "aws" }
  );
}
for (let i = 0; i < 10; i += 1) {
  const tok = b64(`bearer-${i}`).replace(/=+$/, "");
  ensure(
    `bearer-${i}`,
    "true_secret",
    `true_secret/gen-bearer-${i}.txt`,
    `Authorization: Bearer ${tok}`,
    { expectedKind: "AUTHORIZATION_BEARER", family: "http-auth" }
  );
}
for (let i = 0; i < 8; i += 1) {
  const basic = Buffer.from(`user${i}:pass-${h(i).slice(0, 12)}`).toString("base64");
  ensure(
    `basic-${i}`,
    "true_secret",
    `true_secret/gen-basic-${i}.txt`,
    `Authorization: Basic ${basic}`,
    { expectedKind: "AUTHORIZATION_BASIC", family: "http-auth" }
  );
}
for (let i = 0; i < 8; i += 1) {
  ensure(
    `pem-${i}`,
    "true_secret",
    `true_secret/gen-pem-${i}.txt`,
    [
      "-----BEGIN PRIVATE KEY-----",
      b64(`pem-${i}`).match(/.{1,64}/g).join("\n"),
      "-----END PRIVATE KEY-----"
    ].join("\n"),
    { expectedKind: "PRIVATE_KEY_BLOCK", family: "pem" }
  );
}
for (let i = 0; i < 10; i += 1) {
  ensure(
    `generic-secret-${i}`,
    "true_secret",
    `true_secret/gen-generic-${i}.txt`,
    `api_key = "flt_test_${h(`gen-${i}`).slice(0, 24)}"`,
    { expectedKind: "GENERIC_SECRET_ASSIGNMENT", family: "generic-assignment" }
  );
}

// --- false_positive (benign that may trip patterns) ---
for (let i = 0; i < 15; i += 1) {
  ensure(
    `fp-docs-sk-${i}`,
    "false_positive",
    `false_positive/gen-docs-sk-${i}.txt`,
    `Example docs: use sk-proj-EXAMPLE_PLACEHOLDER_${i} in tutorials only.`,
    { family: "docs-placeholder", note: "Documentation placeholder; FP if redacted." }
  );
}
for (let i = 0; i < 10; i += 1) {
  const uuid = `${h(`uuid-${i}`).slice(0, 8)}-${h(`uuid-${i}`).slice(8, 12)}-4${h(`uuid-${i}`).slice(13, 16)}-a${h(`uuid-${i}`).slice(17, 20)}-${h(`uuid-${i}`).slice(20, 32)}`;
  ensure(
    `fp-uuid-${i}`,
    "false_positive",
    `false_positive/gen-uuid-${i}.txt`,
    `request_id=${uuid}`,
    { family: "uuid", note: "UUID-like identifier without secret markers." }
  );
}

// --- should_miss (known limitations) ---
for (let i = 0; i < 12; i += 1) {
  const key = `sk-proj-${h(`split-${i}`).slice(0, 40)}`;
  ensure(
    `miss-split-${i}`,
    "should_miss",
    `should_miss/gen-split-${i}.txt`,
    `${key.slice(0, 12)}\n${key.slice(12)}`,
    { family: "split-line", note: "Key split across lines — UNKNOWN_PATTERNS." }
  );
}
for (let i = 0; i < 10; i += 1) {
  const encoded = Buffer.from(`sk-proj-${h(`b64-${i}`).slice(0, 32)}`, "utf8").toString("base64");
  ensure(
    `miss-b64-${i}`,
    "should_miss",
    `should_miss/gen-b64-${i}.txt`,
    `payload=${encoded}`,
    { family: "base64-wrapped", note: "Encoded credential without markers." }
  );
}

// --- PR #195 growth families (spec 3.5): inputs only, no new detector kinds ---
const skKey = (seed) => `sk-proj-${h(seed).slice(0, 40)}`;
const NBSP_LIKE = ["\u00a0", "\u3000", "\ufeff"];
for (let i = 0; i < 6; i += 1) {
  const pad = NBSP_LIKE[i % 3];
  ensure(`openai-unicode-wrap-${i}`, "true_secret", `true_secret/gen-openai-unicode-wrap-${i}.txt`,
    `note${pad}${skKey(`ouw-${i}`)}${pad}end`,
    { expectedKind: "OPENAI_API_KEY", family: "openai-unicode-wrap" });
}
for (let i = 0; i < 6; i += 1) {
  ensure(`github-json-escaped-${i}`, "true_secret", `true_secret/gen-github-json-escaped-${i}.txt`,
    `{"log": "leaked \\"${["ghs", "ghr", "ghu", "gho", "ghs", "ghr"][i]}_${h(`gje-${i}`).slice(0, 28)}\\" in output"}`,
    { expectedKind: "GITHUB_TOKEN", family: "github-json-escaped" });
}
for (let i = 0; i < 6; i += 1) {
  const secret = b64(`asc-${i}`).replace(/=+$/, "").slice(0, 40);
  ensure(`aws-secret-crlf-${i}`, "true_secret", `true_secret/gen-aws-secret-crlf-${i}.txt`,
    `[default]\r\naws_secret_access_key = "${secret}"\r\nregion = us-east-1\r\n`,
    { expectedKind: "AWS_SECRET_ACCESS_KEY", family: "aws-secret-crlf" });
}
for (let i = 0; i < 6; i += 1) {
  ensure(`pem-crlf-${i}`, "true_secret", `true_secret/gen-pem-crlf-${i}.txt`,
    ["-----BEGIN PRIVATE KEY-----", ...b64(`pc-${i}`).match(/.{1,64}/g), "-----END PRIVATE KEY-----"].join("\r\n") + "\r\n",
    { expectedKind: "PRIVATE_KEY_BLOCK", family: "pem-crlf" });
}
for (let i = 0; i < 6; i += 1) {
  const tok = b64(`bu-${i}`).replace(/=+$/, "");
  ensure(`bearer-url-${i}`, "true_secret", `true_secret/gen-bearer-url-${i}.txt`,
    `GET https://api.example.test/v1/items?id=${i}&h=Bearer ${tok}&page=2`,
    { expectedKind: "AUTHORIZATION_BEARER", family: "bearer-url" });
}
for (let i = 0; i < 6; i += 1) {
  ensure(`generic-yaml-${i}`, "true_secret", `true_secret/gen-generic-yaml-${i}.txt`,
    `service:\n  password: '${h(`gy-${i}`).slice(0, 24)}'\n`,
    { expectedKind: "GENERIC_SECRET_ASSIGNMENT", family: "generic-yaml" });
}
for (let i = 0; i < 6; i += 1) {
  const basic = Buffer.from(`user${i}:pass-${h(`bf-${i}`).slice(0, 12)}`).toString("base64");
  ensure(`basic-header-folded-${i}`, "true_secret", `true_secret/gen-basic-header-folded-${i}.txt`,
    `Authorization:\t\t\tBasic ${basic}`,
    { expectedKind: "AUTHORIZATION_BASIC", family: "basic-header-folded" });
}

for (let i = 0; i < 6; i += 1) {
  ensure(`fp-placeholder-like-${i}`, "false_positive", `false_positive/gen-placeholder-like-${i}.txt`,
    `Redacted value: <FAULTLINE_REDACTED:OPENAI_API_KEY:sha256:${h(`pl-${i}`)}> is a placeholder.`,
    { family: "placeholder-like", note: "Placeholder-shaped text; FP if redacted." });
}
for (let i = 0; i < 6; i += 1) {
  ensure(`fp-long-s-key-${i}`, "false_positive", `false_positive/gen-long-s-key-${i}.txt`,
    `\u017fecret=${h(`ls-${i}`).slice(0, 16)}\n\u212a=${h(`lk-${i}`).slice(0, 8)}`,
    { family: "long-s-key", note: "Long-s and Kelvin sign look like key letters; not secret names." });
}
for (let i = 0; i < 6; i += 1) {
  ensure(`fp-docs-token-word-${i}`, "false_positive", `false_positive/gen-docs-token-word-${i}.txt`,
    `The token ${h(`dt-${i}`).slice(0, 10)} is described in the guide; every token is rotated daily.`,
    { family: "docs-token-word", note: "The word token in prose, without = or :." });
}

for (let i = 0; i < 6; i += 1) {
  const key = skKey(`sal-${i}`);
  ensure(`miss-split-across-lines-${i}`, "should_miss", `should_miss/gen-split-across-lines-${i}.txt`,
    `${key.slice(0, 14)}\n${key.slice(14)}`,
    { family: "split-across-lines", note: "Key split by a newline." });
}
for (let i = 0; i < 6; i += 1) {
  const key = h(`sc-${i}`).slice(0, 40);
  ensure(`miss-split-concat-${i}`, "should_miss", `should_miss/gen-split-concat-${i}.txt`,
    `const k = "sk-" + "proj-" + "${key}";`,
    { family: "split-concat", note: "Key assembled by concatenation." });
}
for (let i = 0; i < 6; i += 1) {
  ensure(`miss-url-encoded-${i}`, "should_miss", `should_miss/gen-url-encoded-${i}.txt`,
    `q=%73k-proj-${h(`ue-${i}`).slice(0, 40)}`,
    { family: "url-encoded", note: "First key character percent-encoded." });
}
for (let i = 0; i < 6; i += 1) {
  const key = skKey(`lsb-${i}`);
  // A lone surrogate (ED A0 80, WTF-8) inside the key; both sides read it as U+FFFD.
  ensure(`miss-lone-surrogate-break-${i}`, "should_miss", `should_miss/gen-lone-surrogate-break-${i}.txt`,
    Buffer.concat([Buffer.from(key.slice(0, 20)), Buffer.from([0xed, 0xa0, 0x80]), Buffer.from(`${key.slice(20)}\n`)]),
    { family: "lone-surrogate-break", note: "Lone surrogate inside the key; read lossily as U+FFFD." });
}
{
  // tiny-binary: labelled from what the frozen TS scanner actually reports.
  const { redactText } = await import(pathToFileURL(join(root, "dist", "redaction.js")).href);
  for (let i = 0; i < 6; i += 1) {
    const total = 64 + (parseInt(h(`tbn-${i}`).slice(0, 4), 16) % 193);
    const key = Buffer.from(skKey(`tb-${i}`));
    let noise = Buffer.alloc(0);
    for (let n = 0; noise.length < total; n += 1) noise = Buffer.concat([noise, Buffer.from(h(`tbb-${i}-${n}`), "hex")]);
    const at = parseInt(h(`tbp-${i}`).slice(0, 4), 16) % (Math.max(1, total - key.length));
    const body = Buffer.concat([noise.subarray(0, at), key, noise.subarray(at, total)]);
    const text = readFileSync_(body);
    const redacted = redactText(text, `corpus:tiny-binary-${i}`).report.redacted === true;
    const label = redacted ? "true_secret" : "should_miss";
    ensure(`tiny-binary-${i}`, label, `${label}/gen-tiny-binary-${i}.bin`, body,
      redacted ? { expectedKind: "OPENAI_API_KEY", family: "tiny-binary" } : { family: "tiny-binary", note: "Binary noise around a key; labelled from TS behaviour." });
  }
}

const labels = {
  schemaVersion: "faultline.secret-scanner-corpus.v1",
  coverage: "LIMITED",
  limitations: [
    "LIMITED_COVERAGE: only documented credential formats and secret-named fields are recognized.",
    "UNKNOWN_PATTERNS: encoded, split, encrypted, binary, transformed, or unrecognized credentials may remain.",
    "SEC-09 expanded corpus remains LIMITED until FP/FN confidence intervals are published from this labeled set."
  ],
  generatedBy: "scripts/expand-secret-scanner-corpus.mjs",
  caseCount: cases.length,
  cases
};

writeFileSync(join(corpusRoot, "labels.json"), `${JSON.stringify(labels, null, 2)}\n`, "utf8");
process.stdout.write(`Secret-scanner corpus cases: ${cases.length}\n`);
if (cases.length < 100) {
  process.stderr.write(`Expected ≥100 cases, got ${cases.length}\n`);
  process.exit(1);
}
