#!/usr/bin/env node
/**
 * SEC-03 helper: print the executable stranger verification chain checklist.
 * Does not invent attestation artifacts — validates local paths when provided.
 */
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const args = process.argv.slice(2);
function opt(name) {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
}

if (args.includes("--help") || args.length === 0) {
  process.stdout.write(`Usage:
  node scripts/verify-security-chain.mjs --checklist
  node scripts/verify-security-chain.mjs --bundle <dir> --receipt <json> --attestation <json> --trust <json>

See docs/security-chain.md
`);
  process.exit(args.includes("--help") ? 0 : 1);
}

if (args.includes("--checklist")) {
  process.stdout.write(JSON.stringify({
    schemaVersion: "faultline.security-chain-checklist.v1",
    steps: [
      { id: "TAG", action: "Checkout pinned tag (README / v0.1.5-buildweek)" },
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

// Path presence only — full cryptographic verify is `fl provenance verify`.
const receiptJson = JSON.parse(readFileSync(resolve(receipt), "utf8"));
process.stdout.write(JSON.stringify({
  status: "ARTIFACTS_PRESENT",
  next: "fl provenance verify --bundle ... --receipt ... --attestation-bundle ... --trust ...",
  receiptKeys: Object.keys(receiptJson)
}, null, 2) + "\n");
