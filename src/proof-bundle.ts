import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson, sha256 } from "./canonical.js";
import { DemoAnalysisSchema, MinimizationAttemptSchema, RunRecordSchema, WitnessSchema, type DemoAnalysis, type RunRecord, type Verdict } from "./domain.js";
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

const StoredMinimizationSchema = z.object({
  budget: z.object({ used: z.number().int().nonnegative(), max: z.number().int().positive() }),
  attempts: z.array(MinimizationAttemptSchema),
  candidate: z.array(z.string()),
  sufficiency: RunRecordSchema,
  necessity: RunRecordSchema,
  termination: z.enum(["BIDIRECTIONALLY_VALIDATED", "NOT_EXECUTED"])
});

const StoredPreventionSchema = z.object({
  lastGood: RunRecordSchema,
  firstBad: RunRecordSchema,
  repaired: RunRecordSchema,
  verified: z.boolean()
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

export type ProofBundleWriteOptions = {
  /** Override for a controlled embedding or an isolated test root. */
  proofRoot?: string;
};

export function defaultProofRoot(): string {
  return resolve(".faultline", "bundles");
}

function assertNoLinksOrSpecialFiles(directory: string): void {
  const stat = lstatSync(directory);
  if (stat.isSymbolicLink() || !stat.isDirectory()) {
    throw new Error(`Proof bundle destination must be a real directory: ${directory}`);
  }
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const child = join(directory, entry.name);
    const childStat = lstatSync(child);
    if (childStat.isSymbolicLink() || (!childStat.isDirectory() && !childStat.isFile())) {
      throw new Error(`Proof bundle destination contains a symbolic link or special file: ${child}`);
    }
    if (childStat.isDirectory()) assertNoLinksOrSpecialFiles(child);
  }
}

/**
 * Proof bundles are replace-on-write, so their destination must stay inside
 * FaultLine's managed evidence directory. This prevents a typo such as
 * `--output .` from turning an evidence refresh into a repository deletion.
 */
export function assertSafeProofOutput(outputDirectory: string, proofRoot = defaultProofRoot()): string {
  const output = resolve(outputDirectory);
  const root = resolve(proofRoot);
  const nestedPath = relative(root, output);
  if (!nestedPath || nestedPath.startsWith("..") || isAbsolute(nestedPath)) {
    throw new Error(`Proof bundle output must be a child directory of ${root}`);
  }
  const pathParts = nestedPath.split(/[\\/]+/).filter(Boolean);
  let current = root;
  if (existsSync(current) && lstatSync(current).isSymbolicLink()) {
    throw new Error(`Proof bundle root cannot be a symbolic link: ${root}`);
  }
  for (const part of pathParts) {
    current = join(current, part);
    if (!existsSync(current)) continue;
    const stat = lstatSync(current);
    if (stat.isSymbolicLink()) throw new Error(`Proof bundle output cannot traverse a symbolic link: ${current}`);
    if (current === output && !stat.isDirectory()) throw new Error(`Proof bundle output must be a directory: ${output}`);
  }
  if (existsSync(output)) {
    assertNoLinksOrSpecialFiles(output);
    const existing = verifyProofBundle(output);
    if (!existing.valid) {
      throw new Error(`Proof bundle output can only replace a verified FaultLine bundle: ${output}`);
    }
  }
  return output;
}

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

function readJsonArtifact(path: string, label: string, errors: string[]): unknown | undefined {
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    errors.push(`${label} JSON validation failed: ${error instanceof Error ? error.message : String(error)}`);
    return undefined;
  }
}

function sameCanonical(left: unknown, right: unknown): boolean {
  return canonicalJson(left) === canonicalJson(right);
}

function stableExecutedRuns(records: RunRecord[], expectedStateId: string, expectedVerdict: Verdict): boolean {
  if (records.length !== 3 || new Set(records.map((record) => record.id)).size !== records.length) return false;
  const first = records[0]!;
  return records.every((record) => record.executionKind === "EXECUTED"
    && record.verdict === expectedVerdict
    && record.stateId === expectedStateId
    && record.witnessDigest === first.witnessDigest
    && record.environmentDigest === first.environmentDigest);
}

function validateSemanticEvidence(analysis: DemoAnalysis, output: string, errors: string[]): void {
  const witnessPayload = readJsonArtifact(join(output, "witness", "witness.json"), "witness artifact", errors);
  if (witnessPayload !== undefined) {
    try {
      const witness = WitnessSchema.parse(witnessPayload);
      if (!sameCanonical(witness, analysis.witness)) errors.push("witness artifact does not match analysis.witness");
      const { digest, ...unsignedWitness } = witness;
      if (digestJson(unsignedWitness) !== digest) errors.push("witness artifact digest is invalid");
    } catch (error) {
      errors.push(`witness artifact schema validation failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  const minimizationPayload = readJsonArtifact(join(output, "minimization", "attempts.json"), "minimization artifact", errors);
  if (minimizationPayload !== undefined) {
    try {
      const minimization = StoredMinimizationSchema.parse(minimizationPayload);
      if (!sameCanonical(minimization, analysis.minimization)) errors.push("minimization artifact does not match analysis.minimization");
    } catch (error) {
      errors.push(`minimization artifact schema validation failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  const preventionPayload = readJsonArtifact(join(output, "prevention", "three-state.json"), "prevention artifact", errors);
  if (preventionPayload !== undefined) {
    try {
      const prevention = StoredPreventionSchema.parse(preventionPayload);
      if (!sameCanonical(prevention, analysis.prevention)) errors.push("prevention artifact does not match analysis.prevention");
    } catch (error) {
      errors.push(`prevention artifact schema validation failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  const catalog = new Map(analysis.runCatalog.map((record) => [record.id, record]));
  for (const run of analysis.runCatalog) {
    const base = join(output, "runs", safeRunFileName(run));
    const persistedPayload = readJsonArtifact(join(base, "result.json"), `run ${run.id}`, errors);
    if (persistedPayload !== undefined) {
      try {
        const persisted = RunRecordSchema.parse(persistedPayload);
        if (!sameCanonical(persisted, run)) errors.push(`persisted run does not match catalog: ${run.id}`);
      } catch (error) {
        errors.push(`persisted run schema validation failed for ${run.id}: ${error instanceof Error ? error.message : String(error)}`);
      }
    }
    try {
      if (readFileSync(join(base, "stdout.log"), "utf8") !== run.stdout) errors.push(`stdout does not match catalog for ${run.id}`);
      if (readFileSync(join(base, "stderr.log"), "utf8") !== run.stderr) errors.push(`stderr does not match catalog for ${run.id}`);
    } catch (error) {
      errors.push(`run log read failed for ${run.id}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  for (const transition of analysis.transitions) {
    const boundary = transition.boundaryRunIds.map((id) => catalog.get(id)).filter((run): run is RunRecord => run !== undefined);
    if (boundary.length !== transition.boundaryRunIds.length) continue;
    if (new Set(transition.boundaryRunIds).size !== transition.boundaryRunIds.length) {
      errors.push(`transition contains duplicate boundary run IDs: ${transition.beforeStateId} -> ${transition.afterStateId}`);
    }
    const beforeRuns = boundary.filter((run) => run.stateId === transition.beforeStateId);
    const afterRuns = boundary.filter((run) => run.stateId === transition.afterStateId);
    const shouldBeStable = stableExecutedRuns(beforeRuns, transition.beforeStateId, transition.beforeVerdict)
      && stableExecutedRuns(afterRuns, transition.afterStateId, transition.afterVerdict);
    if (transition.stable !== shouldBeStable) errors.push(`transition stability does not match executed boundary evidence: ${transition.beforeStateId} -> ${transition.afterStateId}`);
    const expectedKind = transition.beforeVerdict === "PASS" && transition.afterVerdict === "FAIL"
      ? "PASS_TO_FAIL"
      : transition.beforeVerdict === "FAIL" && transition.afterVerdict === "PASS"
        ? "FAIL_TO_PASS"
        : undefined;
    if (!expectedKind || transition.kind !== expectedKind) errors.push(`transition kind does not match verdicts: ${transition.beforeStateId} -> ${transition.afterStateId}`);
    if (analysis.mode === "REPLAY" && transition.stable) errors.push("cached replay cannot certify a stable transition");
  }

  const sufficiency = catalog.get(analysis.minimization.sufficiency.id);
  const necessity = catalog.get(analysis.minimization.necessity.id);
  const minimizationProof = analysis.minimization.termination === "BIDIRECTIONALLY_VALIDATED";
  if (minimizationProof) {
    if (!sufficiency || !necessity || analysis.mode !== "RERUN"
      || sufficiency.executionKind !== "EXECUTED" || necessity.executionKind !== "EXECUTED"
      || sufficiency.verdict !== "FAIL" || necessity.verdict !== "PASS"
      || sufficiency.witnessDigest !== necessity.witnessDigest
      || sufficiency.environmentDigest !== necessity.environmentDigest
      || analysis.minimization.candidate.length === 0) {
      errors.push("bidirectional minimization claim is not supported by executed evidence");
    }
  } else if (analysis.mode === "REPLAY" && analysis.minimization.termination !== "NOT_EXECUTED") {
    errors.push("cached replay minimization must be marked NOT_EXECUTED");
  }

  const preventionRuns = [analysis.prevention.lastGood, analysis.prevention.firstBad, analysis.prevention.repaired];
  if (analysis.prevention.verified) {
    const [lastGood, firstBad, repaired] = preventionRuns;
    if (analysis.mode !== "RERUN" || preventionRuns.some((run) => run.executionKind !== "EXECUTED")
      || lastGood!.verdict !== "PASS" || firstBad!.verdict !== "FAIL" || repaired!.verdict !== "PASS"
      || new Set(preventionRuns.map((run) => run.witnessDigest)).size !== 1
      || new Set(preventionRuns.map((run) => run.environmentDigest)).size !== 1) {
      errors.push("three-state prevention claim is not supported by executed evidence");
    }
  }
  if (analysis.grade.value === "A" && (!analysis.transitions.some((transition) => transition.stable && transition.kind === "PASS_TO_FAIL")
    || !minimizationProof || !analysis.prevention.verified)) {
    errors.push("A-grade claim is not supported by stable, minimized, and prevention evidence");
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

export function writeProofBundle(outputDirectory: string, analysis: DemoAnalysis, options: ProofBundleWriteOptions = {}): { directory: string; manifestDigest: string; rootDigest: string } {
  const output = assertSafeProofOutput(outputDirectory, options.proofRoot);
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
    const stat = lstatSync(path);
    if (stat.isSymbolicLink() || (!stat.isDirectory() && !stat.isFile())) {
      throw new Error(`bundle contains a symbolic link or special file: ${path}`);
    }
    if (stat.isDirectory()) files.push(...collectFiles(directory, path));
    else files.push(relative(directory, path).replaceAll("\\", "/"));
  }
  return files;
}

export function verifyProofBundle(directory: string, expectedRoot?: string): BundleVerification {
  const output = resolve(directory);
  const errors: string[] = [];
  let rootDigest: string | null = null;
  let externalRootStatus: ExternalRootStatus = expectedRoot ? "MISMATCH" : "NOT_PROVIDED";
  let manifest: z.infer<typeof ManifestSchema> | undefined;
  try {
    if (!existsSync(output)) {
      return { valid: false, checkedFiles: 0, errors: ["bundle directory does not exist"], rootDigest: null, externalRootStatus };
    }
    assertNoLinksOrSpecialFiles(output);
  const hashesPath = join(output, "hashes.txt");
  const rootPath = join(output, "ROOT.sha256");
  const manifestPath = join(output, "manifest.json");
  const analysisPath = join(output, "analysis.json");
  if (!existsSync(hashesPath) || !existsSync(rootPath) || !existsSync(manifestPath) || !existsSync(analysisPath)) {
      return { valid: false, checkedFiles: 0, errors: ["bundle is missing hashes.txt, ROOT.sha256, manifest.json, or analysis.json"], rootDigest: null, externalRootStatus };
  }
  const hashes = readFileSync(hashesPath, "utf8");
    const calculatedRoot = `sha256:${sha256(hashes)}`;
    rootDigest = calculatedRoot;
  const storedRoot = readFileSync(rootPath, "utf8").trim();
  if (storedRoot !== calculatedRoot) errors.push("ROOT.sha256 does not match hashes.txt");
    externalRootStatus = expectedRoot ? (expectedRoot === calculatedRoot ? "MATCH" : "MISMATCH") : "NOT_PROVIDED";
  if (externalRootStatus === "MISMATCH") errors.push("externally supplied bundle root does not match");
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
      if (match.groups.file.includes("\\") || match.groups.file.startsWith("/") || match.groups.file.split("/").some((part) => !part || part === "." || part === "..")) {
        errors.push(`invalid declared path: ${match.groups.file}`);
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
    if (analysis) validateSemanticEvidence(analysis, output, errors);
    if (manifest) {
      return { valid: errors.length === 0, checkedFiles: declared.size, errors, rootDigest, externalRootStatus, manifest };
    }
    return { valid: errors.length === 0, checkedFiles: declared.size, errors, rootDigest, externalRootStatus };
  } catch (error) {
    errors.push(`bundle verification failed safely: ${error instanceof Error ? error.message : String(error)}`);
    return manifest
      ? { valid: false, checkedFiles: 0, errors, rootDigest, externalRootStatus, manifest }
      : { valid: false, checkedFiles: 0, errors, rootDigest, externalRootStatus };
  }
}

export function describeBundlePath(directory: string): string {
  return relative(process.cwd(), resolve(directory)) || ".";
}
