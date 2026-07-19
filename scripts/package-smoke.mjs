import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const repository = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const npm = process.platform === "win32" ? "npm.cmd" : "npm";
const temporary = mkdtempSync(join(tmpdir(), "faultline-package-smoke-"));
const packDirectory = join(temporary, "pack");
const consumer = join(temporary, "consumer");

function run(command, args, cwd) {
  const executable = process.platform === "win32" ? (process.env.ComSpec ?? "cmd.exe") : command;
  const executableArgs = process.platform === "win32"
    ? ["/d", "/s", "/c", "call", command, ...args]
    : args;
  const result = spawnSync(executable, executableArgs, { cwd, encoding: "utf8" });
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed\n${result.error?.message ?? ""}\n${result.stdout ?? ""}${result.stderr ?? ""}`);
  }
  return result.stdout ?? "";
}

function runInstalledFl(args) {
  const executable = join(consumer, "node_modules", ".bin", process.platform === "win32" ? "fl.cmd" : "fl");
  return run(executable, args, consumer);
}

function readUtf8(relativePath) {
  return readFileSync(join(repository, relativePath), "utf8");
}

function assertSecurityControlTestMatrix() {
  // B-1: every row in docs/security-model.md appendix must name a real test file
  // (and optionally a test-title substring that appears in that file).
  const securityModel = readUtf8("docs/security-model.md");
  const appendixHeading = "## Appendix: claimed controls → enforcing tests";
  const appendixAt = securityModel.indexOf(appendixHeading);
  if (appendixAt < 0) {
    throw new Error("docs/security-model.md must include appendix heading for claimed controls → enforcing tests");
  }
  const appendix = securityModel.slice(appendixAt);
  const rowPattern = /^\|([^|\n]+)\|([^|\n]+)\|([^|\n]*)\|$/gm;
  const rows = [];
  for (const match of appendix.matchAll(rowPattern)) {
    const control = match[1].trim();
    const testFile = match[2].trim().replace(/^`+|`+$/g, "");
    const testName = match[3].trim().replace(/^`+|`+$/g, "");
    if (!control || control === "Claimed control" || control.startsWith("---")) continue;
    if (!testFile.startsWith("tests/") || !testFile.endsWith(".test.ts")) {
      throw new Error(`security-model appendix row for '${control}' must list a tests/*.test.ts file, got: ${testFile}`);
    }
    rows.push({ control, testFile, testName });
  }
  if (rows.length < 8) {
    throw new Error(`security-model appendix must list at least 8 control→test rows (found ${rows.length})`);
  }
  for (const row of rows) {
    const absolute = join(repository, row.testFile);
    let body;
    try {
      body = readFileSync(absolute, "utf8");
    } catch {
      throw new Error(`security-model appendix lists missing test file for '${row.control}': ${row.testFile}`);
    }
    if (row.testName) {
      // Optional: require the test title substring to appear in the file.
      if (!body.includes(row.testName)) {
        throw new Error(
          `security-model appendix test name for '${row.control}' not found in ${row.testFile}: ${row.testName}`
        );
      }
    }
  }
}

function assertDocsLint() {
  // Docs-lint contract for contributors:
  // - README must pin judges to the submission tag (never "checkout main").
  // - Cold-open mode is controlled by a stable HTML marker in docs/video-teleprompter.md:
  //     <!-- faultline-cold-open: prefer-external -->
  //     <!-- faultline-cold-open: sample-until-external -->
  // - Marker must match docs/impact-validation-external-01.md Status (completed => prefer-external).
  // Phrase wording around the marker may change; do not remove the marker without updating this gate.
  const readme = readUtf8("README.md");
  const teleprompter = readUtf8("docs/video-teleprompter.md");
  const impact = readUtf8("docs/impact-validation-external-01.md");
  const pinnedRef = "v0.1.1-buildweek";

  assertSecurityControlTestMatrix();

  if (!readme.includes(`git checkout ${pinnedRef}`)) {
    throw new Error(`README.md must pin judges to git checkout ${pinnedRef}`);
  }
  if (readme.includes("git checkout main")) {
    throw new Error("README.md must not tell judges to checkout main for the submission path");
  }

  const impactStatusMatch = impact.match(/^- Status:\s*(\S+)/m);
  const impactStatus = impactStatusMatch?.[1] ?? "";
  const coldOpenMarker = teleprompter.match(/<!--\s*faultline-cold-open:\s*([a-z-]+)\s*-->/i)?.[1] ?? "";
  const prefersExternalColdOpen = coldOpenMarker === "prefer-external";
  const sampleUntilExternal = coldOpenMarker === "sample-until-external";

  if (impactStatus === "completed") {
    if (!prefersExternalColdOpen) {
      throw new Error(
        "docs/video-teleprompter.md must include <!-- faultline-cold-open: prefer-external --> once impact-validation-external-01 status is completed"
      );
    }
  } else {
    if (prefersExternalColdOpen) {
      throw new Error(
        `docs/video-teleprompter.md prefers external cold-open while impact-validation-external-01 status is '${impactStatus || "missing"}' (must be completed)`
      );
    }
    if (!sampleUntilExternal) {
      throw new Error(
        "docs/video-teleprompter.md must include <!-- faultline-cold-open: sample-until-external --> until external status is completed"
      );
    }
  }

  // Pin / drift contract:
  // - pinnedRef below is the submission checkout tag named in README.
  // - That tag must exist and be an ancestor of HEAD (or equal), so HEAD never drifts
  //   onto an unrelated history while README still advertises the pin.
  // - Tag cut itself is a Wave-4 release act; this gate only fails on drift.
  const localTags = spawnSync("git", ["tag", "-l", pinnedRef], { cwd: repository, encoding: "utf8" });
  if (localTags.status !== 0) {
    throw new Error(`git tag -l ${pinnedRef} failed: ${localTags.stderr || localTags.error?.message || ""}`);
  }
  let hasLocalTag = (localTags.stdout ?? "").split(/\r?\n/).filter(Boolean).includes(pinnedRef);
  if (!hasLocalTag) {
    // Shallow CI checkouts often omit tags; resolve against the configured remote.
    const remoteTags = spawnSync(
      "git",
      ["ls-remote", "--tags", "--refs", "origin", `refs/tags/${pinnedRef}`],
      { cwd: repository, encoding: "utf8" }
    );
    if (remoteTags.status !== 0) {
      throw new Error(
        `Pinned submission tag ${pinnedRef} missing locally and git ls-remote failed: ${remoteTags.stderr || remoteTags.error?.message || ""}`
      );
    }
    const remoteLines = (remoteTags.stdout ?? "").split(/\r?\n/).filter(Boolean);
    if (!remoteLines.some((line) => line.endsWith(`\trefs/tags/${pinnedRef}`))) {
      throw new Error(`Pinned submission tag ${pinnedRef} must exist on origin (cut after P0 lands)`);
    }
    const fetchTag = spawnSync("git", ["fetch", "--no-tags", "origin", `refs/tags/${pinnedRef}:refs/tags/${pinnedRef}`], {
      cwd: repository,
      encoding: "utf8"
    });
    if (fetchTag.status !== 0) {
      throw new Error(
        `Failed to fetch pinned tag ${pinnedRef}: ${fetchTag.stderr || fetchTag.error?.message || ""}`
      );
    }
    hasLocalTag = true;
  }

  const teleprompterPin = /git checkout\s+([^\s`]+)/.exec(teleprompter);
  if (teleprompterPin && teleprompterPin[1] !== pinnedRef) {
    throw new Error(
      `docs/video-teleprompter.md checkout ref '${teleprompterPin[1]}' must match package-smoke pin '${pinnedRef}'`
    );
  }

  const ancestor = spawnSync("git", ["merge-base", "--is-ancestor", pinnedRef, "HEAD"], {
    cwd: repository,
    encoding: "utf8"
  });
  if (ancestor.status !== 0) {
    throw new Error(
      `Pinned submission tag ${pinnedRef} must be an ancestor of HEAD (README pin drifted from this branch history)`
    );
  }
}

try {
  mkdirSync(packDirectory);
  mkdirSync(consumer);

  assertDocsLint();

  run(npm, ["pack", "--pack-destination", packDirectory], repository);
  const dryRun = JSON.parse(run(npm, ["pack", "--dry-run", "--json", "--ignore-scripts"], repository));
  const packedFiles = dryRun[0]?.files?.map(({ path }) => path) ?? [];
  const allowedExact = new Set([
    "package.json",
    "LICENSE",
    "README.md",
    "docs/faultline-self-incident.md",
    "docs/first-incident.md",
    "docs/github-action.md",
    "docs/concepts.md",
    "docs/security-model.md",
    "docs/witness-protocol.md",
    "docs/runtime-preparation.md",
    "docs/proof-bundles.md",
    "docs/codex-sidecar.md",
    "docs/turn-snapshot-overhead.md"
  ]);
  const unexpected = packedFiles.filter((path) => !allowedExact.has(path) && !path.startsWith("dist/"));
  if (unexpected.length > 0) throw new Error(`Package contains files outside the allowlist: ${unexpected.join(", ")}`);
  if (!packedFiles.includes("dist/cli.js")) throw new Error("Package does not contain dist/cli.js");

  const archives = readdirSync(packDirectory).filter((name) => name.endsWith(".tgz"));
  if (archives.length !== 1) throw new Error(`Expected one package archive, found ${archives.length}`);
  const archive = join(packDirectory, archives[0]);

  run(npm, ["install", "--ignore-scripts", "--no-audit", "--no-fund", archive], consumer);
  const metadata = JSON.parse(readFileSync(join(repository, "package.json"), "utf8"));
  const version = runInstalledFl(["--version"]).trim();
  const expectedVersion = `FaultLine ${metadata.version}`;
  if (version !== expectedVersion) throw new Error(`Installed fl reported ${version}, expected ${expectedVersion}`);

  const bundle = join(consumer, ".faultline", "bundles", "package-smoke");
  runInstalledFl(["judge-demo", "--rerun-all", "--export-only", "--output", bundle]);
  runInstalledFl(["verify", bundle]);
  process.stdout.write(`Packed and executed ${metadata.name}@${metadata.version} from a clean install.\n`);
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
