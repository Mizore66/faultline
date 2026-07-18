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

try {
  mkdirSync(packDirectory);
  mkdirSync(consumer);

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
