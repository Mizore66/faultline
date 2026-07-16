import { randomUUID } from "node:crypto";
import {
  closeSync,
  existsSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  writeFileSync
} from "node:fs";
import { dirname, isAbsolute, join, parse, relative, resolve } from "node:path";
import { z } from "zod";
import { canonicalJson, digestJson } from "./canonical.js";
import {
  RepairBriefSchema,
  RepairEvidencePacketSchema,
  validateRepairBrief,
  type RepairBrief,
  type RepairEvidencePacket
} from "./repair-brief.js";

/** A private, write-once package containing inferred repair guidance. */
export const REPAIR_BRIEF_ARTIFACT_VERSION = "faultline.repair-brief-artifact.v1" as const;

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);

const RepairBriefArtifactSourceSchema = z.object({
  kind: z.enum(["GPT-5.6", "OFFLINE_INPUT"]),
  model: z.string().min(1).max(256).optional()
}).strict();

export const RepairBriefArtifactManifestSchema = z.object({
  schemaVersion: z.literal(REPAIR_BRIEF_ARTIFACT_VERSION),
  /** Every stored recommendation is guidance, never an executed verdict. */
  classification: z.literal("INFERRED"),
  source: RepairBriefArtifactSourceSchema,
  evidencePacket: z.object({
    path: z.literal("evidence-packet.json"),
    digest: DigestSchema
  }).strict(),
  repairBrief: z.object({
    path: z.literal("repair-brief.json"),
    digest: DigestSchema
  }).strict(),
  limitations: z.array(z.string().min(1)).min(1).max(16),
  manifestDigest: DigestSchema
}).strict();

export type RepairBriefArtifactManifest = z.infer<typeof RepairBriefArtifactManifestSchema>;
export type RepairBriefArtifactSource = z.infer<typeof RepairBriefArtifactSourceSchema>;

export type RepairBriefArtifactVerification = {
  readonly valid: boolean;
  readonly errors: readonly string[];
  readonly manifest: RepairBriefArtifactManifest | null;
};

export type WrittenRepairBriefArtifact = {
  readonly directory: string;
  readonly manifest: RepairBriefArtifactManifest;
};

const EXPECTED_ARTIFACTS = new Set([
  "README.md",
  "evidence-packet.json",
  "repair-brief.json",
  "manifest.json"
]);

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function unsignedManifest(manifest: RepairBriefArtifactManifest): Omit<RepairBriefArtifactManifest, "manifestDigest"> {
  const { manifestDigest: _manifestDigest, ...unsigned } = manifest;
  return unsigned;
}

/** The managed root is deliberately fixed: repair packets must not spill into a repository. */
export function defaultRepairBriefRoot(): string {
  return resolve(".faultline", "repair-briefs");
}

function assertNoLinksOrSpecialFiles(directory: string): void {
  const stat = lstatSync(directory);
  if (stat.isSymbolicLink() || !stat.isDirectory()) {
    throw new Error(`Repair brief directory must be a real directory: ${directory}`);
  }
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const child = join(directory, entry.name);
    const childStat = lstatSync(child);
    if (childStat.isSymbolicLink() || (!childStat.isDirectory() && !childStat.isFile())) {
      throw new Error(`Repair brief directory contains a symbolic link or special file: ${child}`);
    }
    if (childStat.isDirectory()) assertNoLinksOrSpecialFiles(child);
  }
}

/** Create every path segment deliberately, rejecting existing links and devices. */
function ensureRealDirectoryTree(directory: string): void {
  const absolute = resolve(directory);
  const root = parse(absolute).root;
  const suffix = relative(root, absolute);
  const parts = suffix ? suffix.split(/[\\/]+/).filter(Boolean) : [];
  let current = root;
  if (existsSync(current)) {
    const stat = lstatSync(current);
    if (stat.isSymbolicLink() || !stat.isDirectory()) throw new Error(`Repair brief root is not a real directory: ${current}`);
  }
  for (const part of parts) {
    current = join(current, part);
    if (!existsSync(current)) mkdirSync(current, { mode: 0o700 });
    const stat = lstatSync(current);
    if (stat.isSymbolicLink() || !stat.isDirectory()) {
      throw new Error(`Repair brief output cannot traverse a symbolic link or non-directory: ${current}`);
    }
  }
}

function isNested(root: string, candidate: string): boolean {
  const nested = relative(root, candidate);
  return Boolean(nested) && !nested.startsWith("..") && !isAbsolute(nested);
}

/**
 * Reject paths outside the managed repair root and require a fresh directory.
 * A later collision check just before rename preserves the write-once intent.
 */
export function prepareRepairBriefOutput(outputDirectory: string, repairRoot = defaultRepairBriefRoot()): string {
  const root = resolve(repairRoot);
  const output = resolve(outputDirectory);
  if (!isNested(root, output)) {
    throw new Error(`Repair brief output must be a child directory of ${root}`);
  }
  ensureRealDirectoryTree(root);
  ensureRealDirectoryTree(dirname(output));
  if (existsSync(output)) {
    throw new Error(`Repair brief output already exists and will not be replaced: ${output}`);
  }
  return output;
}

function writePrivateFile(path: string, body: string): void {
  let descriptor: number | undefined;
  try {
    descriptor = openSync(path, "wx", 0o600);
    writeFileSync(descriptor, body, "utf8");
    fsyncSync(descriptor);
    closeSync(descriptor);
    descriptor = undefined;
  } finally {
    if (descriptor !== undefined) closeSync(descriptor);
  }
}

function syncDirectory(directory: string): void {
  try {
    const descriptor = openSync(directory, "r");
    try {
      fsyncSync(descriptor);
    } finally {
      closeSync(descriptor);
    }
  } catch {
    // Windows filesystems commonly reject directory fsync. Individual files
    // are always synchronized before the publication rename.
  }
}

function readJson(path: string, label: string, errors: string[]): unknown {
  try {
    return JSON.parse(readFileSync(path, "utf8")) as unknown;
  } catch (error) {
    errors.push(`Unable to read ${label}: ${errorMessage(error)}`);
    return undefined;
  }
}

/** Recheck a stored package without executing repository code or a model request. */
export function verifyRepairBriefArtifact(directory: string): RepairBriefArtifactVerification {
  const errors: string[] = [];
  let manifest: RepairBriefArtifactManifest | null = null;
  try {
    const root = resolve(directory);
    if (!existsSync(root)) {
      return { valid: false, errors: ["Repair brief directory does not exist"], manifest: null };
    }
    assertNoLinksOrSpecialFiles(root);
    const physical = new Set(readdirSync(root, { withFileTypes: true }).map((entry) => entry.name));
    for (const expected of EXPECTED_ARTIFACTS) {
      if (!physical.has(expected)) errors.push(`Repair brief package is missing ${expected}`);
    }
    for (const actual of physical) {
      if (!EXPECTED_ARTIFACTS.has(actual)) errors.push(`Repair brief package contains an unexpected artifact: ${actual}`);
      const path = join(root, actual);
      const stat = lstatSync(path);
      if (!stat.isFile() || stat.isSymbolicLink()) errors.push(`Repair brief artifact must be a regular non-symlink file: ${actual}`);
    }

    const manifestParsed = RepairBriefArtifactManifestSchema.safeParse(readJson(join(root, "manifest.json"), "repair brief manifest", errors));
    if (!manifestParsed.success) {
      errors.push(`Repair brief manifest schema validation failed: ${manifestParsed.error.message}`);
    } else {
      manifest = manifestParsed.data;
      if (manifest.manifestDigest !== digestJson(unsignedManifest(manifest))) {
        errors.push("Repair brief manifest digest does not match its canonical contents.");
      }
    }
    const packetParsed = RepairEvidencePacketSchema.safeParse(readJson(join(root, "evidence-packet.json"), "repair evidence packet", errors));
    if (!packetParsed.success) errors.push(`Repair evidence packet schema validation failed: ${packetParsed.error.message}`);
    const briefParsed = RepairBriefSchema.safeParse(readJson(join(root, "repair-brief.json"), "repair brief", errors));
    if (!briefParsed.success) errors.push(`Repair brief schema validation failed: ${briefParsed.error.message}`);

    if (manifest && packetParsed.success && briefParsed.success) {
      if (manifest.evidencePacket.digest !== packetParsed.data.packetDigest) {
        errors.push("Repair brief manifest evidence packet digest does not match evidence-packet.json.");
      }
      if (manifest.repairBrief.digest !== digestJson(briefParsed.data)) {
        errors.push("Repair brief manifest digest does not match repair-brief.json.");
      }
      const validation = validateRepairBrief(packetParsed.data, briefParsed.data);
      if (!validation.valid) errors.push(...validation.errors.map((error) => `Stored repair brief is invalid: ${error}`));
    }
  } catch (error) {
    errors.push(`Repair brief package verification failed safely: ${errorMessage(error)}`);
  }
  return { valid: errors.length === 0, errors, manifest };
}

/**
 * Store a citation-validated packet and brief as a fresh private directory.
 * The complete staged directory is checked before one final rename publishes
 * it, so no partial packet is ever treated as a FaultLine repair artifact.
 */
export function writeRepairBriefArtifact(
  outputDirectory: string,
  packetInput: unknown,
  briefInput: unknown,
  source: RepairBriefArtifactSource
): WrittenRepairBriefArtifact {
  const packet: RepairEvidencePacket = RepairEvidencePacketSchema.parse(packetInput);
  const validation = validateRepairBrief(packet, briefInput);
  if (!validation.valid || !validation.brief) {
    throw new Error(`Refusing to store an invalid inferred repair brief: ${validation.errors.join("; ")}`);
  }
  const brief: RepairBrief = validation.brief;
  const parsedSource = RepairBriefArtifactSourceSchema.parse(source);
  const output = prepareRepairBriefOutput(outputDirectory);
  const stage = join(dirname(output), `.${output.split(/[\\/]/).at(-1) ?? "repair"}.${randomUUID()}.tmp`);
  const unsigned = {
    schemaVersion: REPAIR_BRIEF_ARTIFACT_VERSION,
    classification: "INFERRED" as const,
    source: parsedSource,
    evidencePacket: { path: "evidence-packet.json" as const, digest: packet.packetDigest },
    repairBrief: { path: "repair-brief.json" as const, digest: digestJson(brief) },
    limitations: [
      "INFERRED guidance is not an executed verdict, proof of model intent, unique semantic cause, or identified culprit.",
      "This package retains only a privacy-minimized evidence packet and citation-validated recommendations; it contains no source text, commands, overlays, raw logs, credentials, or repository paths."
    ]
  };
  const manifest = RepairBriefArtifactManifestSchema.parse({
    ...unsigned,
    manifestDigest: digestJson(unsigned)
  });

  try {
    mkdirSync(stage, { mode: 0o700 });
    const readme = [
      "# FaultLine repair brief",
      "",
      "Status: **INFERRED**",
      "",
      "This is evidence-bounded repair guidance, not an executed verdict, proof of model intent, a unique semantic cause, or an identified culprit.",
      "",
      "The evidence packet and every recommendation are stored as canonical JSON and are citation-validated before publication."
    ].join("\n");
    writePrivateFile(join(stage, "README.md"), `${readme}\n`);
    writePrivateFile(join(stage, "evidence-packet.json"), `${canonicalJson(packet)}\n`);
    writePrivateFile(join(stage, "repair-brief.json"), `${canonicalJson(brief)}\n`);
    writePrivateFile(join(stage, "manifest.json"), `${canonicalJson(manifest)}\n`);
    syncDirectory(stage);

    const verification = verifyRepairBriefArtifact(stage);
    if (!verification.valid) {
      throw new Error(`Refusing to publish an invalid repair brief package: ${verification.errors.join("; ")}`);
    }
    if (existsSync(output)) {
      throw new Error(`Repair brief output appeared during assembly and will not be replaced: ${output}`);
    }
    renameSync(stage, output);
    syncDirectory(dirname(output));
    return { directory: output, manifest };
  } catch (error) {
    if (existsSync(stage)) rmSync(stage, { recursive: true, force: true });
    throw error;
  }
}
