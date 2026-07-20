#!/usr/bin/env node
/**
 * SEC-03 / SEC-08 helper: stranger verification chain checklist + path checks.
 * Does not invent attestation artifacts — validates local paths when provided.
 */
import { existsSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

const args = process.argv.slice(2);
function opt(name) {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
}

if (args.includes("--help") || args.length === 0) {
  process.stdout.write(`Usage:
  node scripts/verify-security-chain.mjs --checklist
  node scripts/verify-security-chain.mjs --tag <vX.Y.Z-buildweek> [--require-signed]
  node scripts/verify-security-chain.mjs --bundle <dir> --receipt <json> --attestation <json> --trust <json> [--tag <tag>]

See docs/security-chain.md
`);
  process.exit(args.includes("--help") ? 0 : 1);
}

if (args.includes("--checklist")) {
  process.stdout.write(JSON.stringify({
    schemaVersion: "faultline.security-chain-checklist.v2",
    steps: [
      { id: "TAG", action: "Checkout pinned tag from README" },
      { id: "TAG_SIGN", action: "git verify-tag <pin> (GPG/SSH signed annotated tag — SEC-08)" },
      { id: "CI", action: "Confirm green Verify FaultLine for that commit" },
      { id: "ATTEST", action: "Download faultline-ci-provenance artifact from main push" },
      { id: "PROVENANCE", action: "fl provenance verify --bundle ... --receipt ... --attestation-bundle ... --trust ..." },
      { id: "VERIFY", action: "fl verify <bundle> --expect-root <digest>" },
      { id: "JUDGE", action: "fl judge-proof --bundle ... --expect-root ... --export-only" }
    ],
    doc: "docs/security-chain.md"
  }, null, 2) + "\n");
  process.exit(0);
}

const tag = opt("--tag");
if (tag !== undefined) {
  let tagStatus = "NOT_CHECKED";
  let tagDetail = null;
  try {
    const out = execFileSync("git", ["tag", "-v", tag], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"]
    });
    tagStatus = "VERIFIED";
    tagDetail = out.trim().slice(0, 500);
  } catch (error) {
    const message = error instanceof Error ? (error.stderr?.toString?.() ?? error.message) : String(error);
    tagStatus = args.includes("--require-signed") ? "UNSIGNED_OR_UNTRUSTED" : "UNSIGNED_OR_UNAVAILABLE";
    tagDetail = message.trim().slice(0, 500);
    if (args.includes("--require-signed")) {
      process.stdout.write(JSON.stringify({ status: tagStatus, tag, detail: tagDetail }, null, 2) + "\n");
      process.exit(1);
    }
  }
  if (opt("--bundle") === undefined) {
    process.stdout.write(JSON.stringify({ status: "TAG_CHECK", tag, tagStatus, detail: tagDetail }, null, 2) + "\n");
    process.exit(tagStatus === "UNSIGNED_OR_UNTRUSTED" ? 1 : 0);
  }
}

const bundle = opt("--bundle");
const receipt = opt("--receipt");
const attestation = opt("--attestation");
const trust = opt("--trust");
if (!bundle || !receipt || !attestation || !trust) {
  process.stderr.write("Missing required --bundle/--receipt/--attestation/--trust\n");
  process.exit(1);
}

const missing = [bundle, receipt, attestation, trust].filter((p) => !existsSync(resolve(p)));
if (missing.length > 0) {
  process.stdout.write(JSON.stringify({
    status: "ARTIFACTS_MISSING",
    missing
  }, null, 2) + "\n");
  process.exit(1);
}

const receiptJson = JSON.parse(readFileSync(resolve(receipt), "utf8"));
process.stdout.write(JSON.stringify({
  status: "ARTIFACTS_PRESENT",
  tag: tag ?? null,
  next: "fl provenance verify --bundle ... --receipt ... --attestation-bundle ... --trust ...",
  receiptKeys: Object.keys(receiptJson)
}, null, 2) + "\n");
