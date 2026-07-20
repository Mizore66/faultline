#!/usr/bin/env node
/**
 * CI release helper: bump package.json semver and emit GitHub Actions outputs.
 *
 * Usage:
 *   node scripts/ci-bump-version.mjs              # patch
 *   node scripts/ci-bump-version.mjs patch|minor|major
 *   node scripts/ci-bump-version.mjs 1.2.3        # exact
 */
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const packagePath = resolve("package.json");
const pkg = JSON.parse(readFileSync(packagePath, "utf8"));
const current = String(pkg.version ?? "0.0.0");
const match = /^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/.exec(current);
if (!match) {
  console.error(`Unsupported package version: ${current}`);
  process.exit(1);
}

const arg = (process.argv[2] ?? "patch").trim();
let next;
if (/^\d+\.\d+\.\d+$/.test(arg)) {
  next = arg;
} else {
  let major = Number(match[1]);
  let minor = Number(match[2]);
  let patch = Number(match[3]);
  if (arg === "major") {
    major += 1;
    minor = 0;
    patch = 0;
  } else if (arg === "minor") {
    minor += 1;
    patch = 0;
  } else if (arg === "patch") {
    patch += 1;
  } else {
    console.error(`Usage: node scripts/ci-bump-version.mjs [patch|minor|major|x.y.z]`);
    process.exit(1);
  }
  next = `${major}.${minor}.${patch}`;
}

if (next === current) {
  console.error(`Refusing no-op version bump (${current})`);
  process.exit(1);
}

pkg.version = next;
writeFileSync(packagePath, `${JSON.stringify(pkg, null, 2)}\n`, "utf8");

const tag = `v${next}`;
process.stdout.write(`version=${next}\n`);
process.stdout.write(`tag=${tag}\n`);

if (process.env.GITHUB_OUTPUT) {
  writeFileSync(process.env.GITHUB_OUTPUT, `version=${next}\ntag=${tag}\n`, { flag: "a" });
}
