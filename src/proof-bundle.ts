import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, sha256 } from "./canonical.js";
import { DemoAnalysisSchema, type DemoAnalysis, type RunRecord } from "./domain.js";
import { analysisDigest } from "./engine.js";

const ManifestSchema = z.object({
  schemaVersion: z.literal("faultline.proof-bundle.v2"),
  investigationId: z.string(),
  fixtureId: z.string(),
  generatedAt: z.string(),
  analysisDigest: z.string(),
  witnessDigest: z.string(),
  environmentDigest: z.string(),
  mode: z.enum(["REPLAY", "RERUN"]),
  integrityScope: z.literal("complete-declared-file-set")
});

export type ExternalRootStatus = "NOT_PROVIDED" | "MATCH" | "MISMATCH";
export type BundleVerification = {
  valid: boolean;
  checkedFiles: number;
  errors: string[];
  rootDigest: string | null;
  externalRootStatus: ExternalRootStatus;
  manifest?: z.infer<typeof ManifestSchema>;
};

function safeRunFileName(run: RunRecord): string {
  return run.id.replace(/[^a-zA-Z0-9_-]/g, "_");
}

function runPaths(run: RunRecord): string[] {
  const base = `runs/${safeRunFileName(run)}`;
  return [`${base}/result.json`, `${base}/stdout.log`, `${base}/stderr.log`];
}

function put(files: Map<string, string>, file: string, body: string): void {
  const normalized = file.replaceAll("\\", "/");
  if (files.has(normalized)) {
    throw new Error(`Proof bundle attempted to write the same path twice: ${normalized}`);
  }
  files.set(normalized, body);
}

function asJson(value: unknown): string {
  return `${JSON.stringify(value, null, 2)}\n`;
}

function report(analysis: DemoAnalysis): string {
  const transition = analysis.transitions.find((item) => item.kind === "PASS_TO_FAIL" && item.stable);
  return [
    "# FaultLine proof bundle",
    "",
    `Mode: ${analysis.mode}`,
    `Fixture: ${analysis.fixture.title}`,
    `Witness: ${analysis.witness.digest}`,
    "",
    "## Bounded conclusion",
    "",
    transition ? `The earliest stable recorded transition for the frozen sample witness is ${transition.beforeStateId} -> ${transition.afterStateId}.` : "No stable pass-to-fail transition was established.",
    "",
    "This bundle supports a predicate-specific result. It does not establish model intent, semantic root cause, or a unique minimal cause.",
    "",
    "## Prevention proof",
    "",
    `- Last-good: ${analysis.prevention.lastGood.verdict}`,
    `- First-bad: ${analysis.prevention.firstBad.verdict}`,
    `- Repaired: ${analysis.prevention.repaired.verdict}`,
    "",
    "## Integrity boundary",
    "",
    "`fl verify` checks the complete declared file set without running repository code. Pass an externally recorded bundle root with `--expect-root` to detect an editor who updates the mutable hash list too."
  ].join("\n");
}

function requiredDeclaredFiles(analysis: DemoAnalysis): string[] {
  const base = ["manifest.json", "report.md", "analysis.json", "witness/witness.json", "minimization/attempts.json", "prevention/three-state.json", "VERIFY.md"];
  return [...base, ...analysis.runCatalog.flatMap(runPaths)].sort((left, right) => left.localeCompare(right));
}

function referencedRunIds(analysis: DemoAnalysis): Set<string> {
  const ids = new Set<string>();
  for (const run of [...analysis.contributionRuns, ...analysis.timelineRuns, analysis.prevention.lastGood, analysis.prevention.firstBad, analysis.prevention.repaired, analysis.minimization.sufficiency, analysis.minimization.necessity]) {
    ids.add(run.id);
  }
  for (const transition of analysis.transitions) {
    for (const id of transition.boundaryRunIds) ids.add(id);
  }
  for (const attempt of analysis.minimization.attempts) {
    if (attempt.runId) ids.add(attempt.runId);
  }
  return ids;
}

function validateAnalysisCoverage(analysis: DemoAnalysis, errors: string[]): void {
  const catalog = new Map<string, RunRecord>();
  for (const record of analysis.runCatalog) {
    if (catalog.has(record.id)) errors.push(`duplicate run id in catalog: ${record.id}`);
    catalog.set(record.id, record);
  }
  for (const id of referencedRunIds(analysis)) {
    if (!catalog.has(id)) errors.push(`analysis references a run not present in the catalog: ${id}`);
  }
}

function stageDirectory(output: string): string {
  return `${output}.staging-${randomUUID()}`;
}

function replaceDirectory(stage: string, output: string): void {
  const backup = `${output}.backup-${randomUUID()}`;
  const hadExistingOutput = existsSync(output);
  if (hadExistingOutput) renameSync(output, backup);
  try {
    renameSync(stage, output);
  } catch (error) {
    if (hadExistingOutput && !existsSync(output)) renameSync(backup, output);
    throw error;
  }
  if (hadExistingOutput) rmSync(backup, { recursive: true, force: true });
}

function writeFiles(directory: string, files: Map<string, string>): void {
  for (const [file, body] of files) {
    const destination = join(directory, file);
    mkdirSync(dirname(destination), { recursive: true });
    writeFileSync(destination, body, "utf8");
  }
}

export function writeProofBundle(outputDirectory: string, analysis: DemoAnalysis): { directory: string; manifestDigest: string; rootDigest: string } {
  const output = resolve(outputDirectory);
  const files = new Map<string, string>();
  const coverageErrors: string[] = [];
  validateAnalysisCoverage(analysis, coverageErrors);
  if (coverageErrors.length > 0) throw new Error(`Cannot write incomplete proof bundle: ${coverageErrors.join("; ")}`);
  const manifest = {
    schemaVersion: "faultline.proof-bundle.v2" as const,
    investigationId: `sample-${analysis.fixture.id}`,
    fixtureId: analysis.fixture.id,
    generatedAt: analysis.generatedAt,
    analysisDigest: analysisDigest(analysis),
    witnessDigest: analysis.witness.digest,
    environmentDigest: analysis.prevention.firstBad.environmentDigest,
    mode: analysis.mode,
    integrityScope: "complete-declared-file-set" as const
  };
  put(files, "manifest.json", asJson(manifest));
  put(files, "report.md", report(analysis));
  put(files, "witness/witness.json", asJson(analysis.witness));
  put(files, "analysis.json", asJson(analysis));
  put(files, "minimization/attempts.json", asJson(analysis.minimization));
  put(files, "prevention/three-state.json", asJson(analysis.prevention));
  put(files, "VERIFY.md", [
    "# Verify this bundle",
    "",
    "Run `fl verify <bundle-directory>` for structural self-consistency.",
    "",
    "For tamper detection against an editor who could update hash metadata, pass an externally recorded root: `fl verify <bundle-directory> --expect-root <sha256:...>`.",
    "",
    "Verification never executes repository code. Use the explicit judge rerun path to execute the reviewed built-in sample."
  ].join("\n"));
  for (const run of analysis.runCatalog) {
    const base = `runs/${safeRunFileName(run)}`;
    put(files, `${base}/result.json`, asJson(run));
    put(files, `${base}/stdout.log`, run.stdout);
    put(files, `${base}/stderr.log`, run.stderr);
  }
  const expectedFiles = requiredDeclaredFiles(analysis);
  const actualFiles = [...files.keys()].sort((left, right) => left.localeCompare(right));
  if (canonicalJson(expectedFiles) !== canonicalJson(actualFiles)) {
    throw new Error("Proof bundle writer did not produce the complete expected file set");
  }
  const hashes = `${[...files.entries()].sort(([left], [right]) => left.localeCompare(right)).map(([file, body]) => `${sha256(body)}  ${file}`).join("\n")}\n`;
  const rootDigest = `sha256:${sha256(hashes)}`;
  const stage = stageDirectory(output);
  try {
    mkdirSync(stage, { recursive: true });
    writeFiles(stage, files);
    writeFileSync(join(stage, "hashes.txt"), hashes, "utf8");
    writeFileSync(join(stage, "ROOT.sha256"), `${rootDigest}\n`, "utf8");
    replaceDirectory(stage, output);
  } catch (error) {
    if (existsSync(stage)) rmSync(stage, { recursive: true, force: true });
    throw error;
  }
  return { directory: output, manifestDigest: `sha256:${sha256(canonicalJson(manifest))}`, rootDigest };
}

function collectFiles(directory: string, current = directory): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(current, { withFileTypes: true })) {
    const path = join(current, entry.name);
    if (entry.isDirectory()) files.push(...collectFiles(directory, path));
    else if (entry.isFile()) files.push(relative(directory, path).replaceAll("\\", "/"));
  }
  return files;
}

export function verifyProofBundle(directory: string, expectedRoot?: string): BundleVerification {
  const output = resolve(directory);
  const errors: string[] = [];
  const hashesPath = join(output, "hashes.txt");
  const rootPath = join(output, "ROOT.sha256");
  const manifestPath = join(output, "manifest.json");
  const analysisPath = join(output, "analysis.json");
  if (!existsSync(hashesPath) || !existsSync(rootPath) || !existsSync(manifestPath) || !existsSync(analysisPath)) {
    return { valid: false, checkedFiles: 0, errors: ["bundle is missing hashes.txt, ROOT.sha256, manifest.json, or analysis.json"], rootDigest: null, externalRootStatus: expectedRoot ? "MISMATCH" : "NOT_PROVIDED" };
  }
  const hashes = readFileSync(hashesPath, "utf8");
  const calculatedRoot = `sha256:${sha256(hashes)}`;
  const storedRoot = readFileSync(rootPath, "utf8").trim();
  if (storedRoot !== calculatedRoot) errors.push("ROOT.sha256 does not match hashes.txt");
  const externalRootStatus: ExternalRootStatus = expectedRoot ? (expectedRoot === calculatedRoot ? "MATCH" : "MISMATCH") : "NOT_PROVIDED";
  if (externalRootStatus === "MISMATCH") errors.push("externally supplied bundle root does not match");
  let manifest: z.infer<typeof ManifestSchema> | undefined;
  let analysis: DemoAnalysis | undefined;
  try {
    manifest = ManifestSchema.parse(JSON.parse(readFileSync(manifestPath, "utf8")));
  } catch (error) {
    errors.push(`manifest validation failed: ${error instanceof Error ? error.message : String(error)}`);
  }
  try {
    analysis = DemoAnalysisSchema.parse(JSON.parse(readFileSync(analysisPath, "utf8")));
  } catch (error) {
    errors.push(`analysis validation failed: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (manifest && analysis) {
    if (manifest.analysisDigest !== analysisDigest(analysis)) errors.push("manifest analysisDigest does not match analysis.json");
    if (manifest.fixtureId !== analysis.fixture.id) errors.push("manifest fixtureId does not match analysis.json");
    if (manifest.witnessDigest !== analysis.witness.digest) errors.push("manifest witnessDigest does not match analysis.json");
    validateAnalysisCoverage(analysis, errors);
  }
  const declared = new Map<string, string>();
  const lines = hashes.trim().split("\n").filter(Boolean);
  for (const line of lines) {
    const match = /^(?<digest>[a-f0-9]{64})  (?<file>.+)$/.exec(line);
    if (!match?.groups?.digest || !match.groups.file) {
      errors.push(`invalid hash entry: ${line}`);
      continue;
    }
    if (declared.has(match.groups.file)) {
      errors.push(`duplicate declared file: ${match.groups.file}`);
      continue;
    }
    declared.set(match.groups.file, match.groups.digest);
  }
  if (analysis) {
    const required = new Set(requiredDeclaredFiles(analysis));
    for (const file of required) if (!declared.has(file)) errors.push(`required evidence file is not declared: ${file}`);
    for (const file of declared.keys()) if (!required.has(file)) errors.push(`undeclared-schema file is present in hashes.txt: ${file}`);
  }
  for (const [file, digest] of declared) {
    const path = resolve(output, file);
    const localPath = relative(output, path);
    if (localPath.startsWith("..") || isAbsolute(localPath)) {
      errors.push(`declared path escapes bundle: ${file}`);
      continue;
    }
    if (!existsSync(path)) {
      errors.push(`declared file is missing: ${file}`);
      continue;
    }
    if (sha256(readFileSync(path)) !== digest) errors.push(`digest mismatch: ${file}`);
  }
  const expectedPhysical = new Set([...declared.keys(), "hashes.txt", "ROOT.sha256"]);
  for (const file of collectFiles(output)) {
    if (!expectedPhysical.has(file)) errors.push(`undeclared file exists in bundle: ${file}`);
  }
  if (manifest) {
    return { valid: errors.length === 0, checkedFiles: declared.size, errors, rootDigest: calculatedRoot, externalRootStatus, manifest };
  }
  return { valid: errors.length === 0, checkedFiles: declared.size, errors, rootDigest: calculatedRoot, externalRootStatus };
}

export function describeBundlePath(directory: string): string {
  return relative(process.cwd(), resolve(directory)) || ".";
}
