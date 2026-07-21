#!/usr/bin/env node
/**
 * Teleprompter dry-run harness (Bucket 1 #1).
 *
 * Checks out the submission pin into a clean temp directory and executes the
 * video's machine-rehearsable commands in script order, asserting exit codes
 * and grepping expected output fragments.
 *
 * Skips live-only beats (OPENAI_API_KEY witness propose, Docker demo full)
 * unless FAULTLINE_REHEARSE_FULL=1 and the prerequisite is available.
 *
 * Usage:
 *   node scripts/rehearse-teleprompter.mjs
 *   FAULTLINE_REHEARSE_TAG=v0.1.9-buildweek node scripts/rehearse-teleprompter.mjs
 */
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, existsSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, "..");
const PIN = process.env.FAULTLINE_REHEARSE_TAG ?? "v0.1.9-buildweek";
const SAMPLE_ROOT =
  "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9";
const FULL = process.env.FAULTLINE_REHEARSE_FULL === "1";

function run(cwd, command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd,
    encoding: "utf8",
    env: { ...process.env, FAULTLINE_NO_BROWSER: "1", ...(options.env ?? {}) },
    maxBuffer: 16 * 1024 * 1024,
    shell: false
  });
  return {
    status: result.status ?? 1,
    stdout: result.stdout ?? "",
    stderr: result.stderr ?? "",
    error: result.error
  };
}

function assertOk(step, result, { expectStatus = 0, stdoutIncludes = [], stdoutRegexes = [] } = {}) {
  const blob = `${result.stdout}\n${result.stderr}`;
  const failures = [];
  if (result.error) failures.push(`spawn error: ${result.error.message}`);
  if (result.status !== expectStatus) {
    failures.push(`exit ${result.status} (expected ${expectStatus})`);
  }
  for (const fragment of stdoutIncludes) {
    if (!blob.includes(fragment)) failures.push(`missing fragment: ${JSON.stringify(fragment)}`);
  }
  for (const re of stdoutRegexes) {
    if (!re.test(blob)) failures.push(`missing regex: ${re}`);
  }
  if (failures.length > 0) {
    console.error(`FAIL ${step}`);
    for (const f of failures) console.error(`  - ${f}`);
    console.error("--- stdout (tail) ---");
    console.error(result.stdout.slice(-4000));
    console.error("--- stderr (tail) ---");
    console.error(result.stderr.slice(-2000));
    process.exit(1);
  }
  console.log(`OK   ${step}`);
}

function dockerAvailable() {
  const r = run(REPO, "docker", ["info"]);
  return r.status === 0;
}

const work = mkdtempSync(join(tmpdir(), "faultline-rehearse-"));
const clone = join(work, "faultline");
console.log(`Rehearsing pin ${PIN} in ${clone}`);

try {
  assertOk(
    "clone",
    run(work, "git", ["clone", "--no-local", REPO, clone]),
    { stdoutIncludes: [] }
  );
  assertOk("fetch-tags", run(clone, "git", ["fetch", "--tags", "--force", "origin"]));
  assertOk("checkout-pin", run(clone, "git", ["checkout", "--detach", PIN]));
  assertOk("pnpm-install", run(clone, "pnpm", ["install", "--frozen-lockfile"]));
  // prepare builds dist; fl entry is node dist/cli.js
  assertOk(
    "doctor",
    run(clone, "pnpm", ["fl", "doctor"]),
    {
      stdoutIncludes: ["Local CLI: READY"]
    }
  );
  // --proof-ready exits non-zero without Docker; report only.
  {
    const proof = run(clone, "pnpm", ["fl", "doctor", "--proof-ready"]);
    const blob = `${proof.stdout}\n${proof.stderr}`;
    if (!/Local CLI:\s*READY/i.test(blob)) {
      console.error("FAIL doctor-proof-ready-report (Local CLI not READY)");
      process.exit(1);
    }
    if (dockerAvailable() && /--proof-ready:\s*NOT READY/i.test(blob)) {
      console.error("FAIL doctor-proof-ready (Docker present but proof-ready NOT READY)");
      process.exit(1);
    }
    if (dockerAvailable() && proof.status !== 0) {
      console.error("FAIL doctor-proof-ready (Docker present but exit non-zero)");
      process.exit(1);
    }
    console.log(
      dockerAvailable()
        ? "OK   doctor-proof-ready"
        : "OK   doctor-proof-ready (Docker absent; non-zero/--proof-ready NOT READY expected)"
    );
  }

  // Cold-open / judge path (teleprompter preferred sample beat)
  assertOk(
    "judge-proof-export",
    run(clone, "pnpm", ["fl", "judge-proof", "--export-only"]),
    {
      stdoutIncludes: [SAMPLE_ROOT.slice(0, 22), "f85c446d"]
    }
  );

  // Offline verify (teleprompter 1:35 beat) — unix path form
  assertOk(
    "verify-sample",
    run(clone, "pnpm", [
      "fl",
      "verify",
      "docs/samples/self-incident-commit-proof",
      "--expect-root",
      SAMPLE_ROOT
    ]),
    {
      stdoutIncludes: ["VALID", SAMPLE_ROOT],
      stdoutRegexes: [/MATCH|External root:\s*MATCH/i]
    }
  );

  // Close beat: unit suite (expensive but scripted)
  if (process.env.FAULTLINE_REHEARSE_SKIP_TEST !== "1") {
    assertOk("pnpm-test", run(clone, "pnpm", ["test"]));
  } else {
    console.log("SKIP pnpm-test (FAULTLINE_REHEARSE_SKIP_TEST=1)");
  }

  // Optional Docker flagship
  if (FULL) {
    if (!dockerAvailable()) {
      console.error("FAULTLINE_REHEARSE_FULL=1 but docker is unavailable");
      process.exit(1);
    }
    assertOk(
      "demo-full",
      run(clone, "pnpm", ["fl", "demo", "full"]),
      {
        stdoutRegexes: [/PREVENTION_VERIFIED|prevention/i]
      }
    );
  } else if (dockerAvailable()) {
    assertOk(
      "demo-live-git-export-only",
      run(clone, "pnpm", ["fl", "demo", "live-git", "--export-only"]),
      {
        stdoutRegexes: [/sha256:[a-f0-9]{64}/i]
      }
    );
    console.log("SKIP demo-full (set FAULTLINE_REHEARSE_FULL=1 for the flagship arc)");
  } else {
    console.log("SKIP demo-live-git / demo-full (Docker absent; judge verify path already rehearsed)");
  }

  // Live GPT beat intentionally not rehearsed without a key
  if (!process.env.OPENAI_API_KEY) {
    console.log("SKIP witness-propose-live (no OPENAI_API_KEY; use docs/samples/gpt-5.6/ footage)");
  }

  console.log(`\nRehearsal PASS for ${PIN}`);
} finally {
  rmSync(work, { recursive: true, force: true });
}
