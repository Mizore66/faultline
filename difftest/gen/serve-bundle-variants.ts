// Explicit fresh-copy mutations of the coherent fixture. Every mutation copies
// from the retained coherent artifacts, edits only what its name says and, where
// the source verifier permits, recomputes the digests TS itself would compute so
// the first refusal is the linkage check rather than a digest mismatch. When a
// verifier rejects a mutation before any adapter binding check (for example a
// minimization record whose runs no longer match an edited summary), that real
// refusal is what the capture retains; nothing here weakens a verifier.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { canonicalJson, digestJson } from "../../src/canonical.js";
import { MUTATIONS_INDEX_PATH, MUTATIONS_INDEX_SCHEMA, MUTATION_ARTIFACTS } from "./serve-bundle-cases.js";

type J = Record<string, unknown>;
const asJ = (value: unknown): J => {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error("expected a JSON object");
  return value as J;
};
const asS = (value: unknown): string => {
  if (typeof value !== "string") throw new Error("expected a string");
  return value;
};
const readJ = (path: string): J => asJ(JSON.parse(readFileSync(path, "utf8")));
const writeJ = (path: string, value: unknown): void => {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, `${canonicalJson(value)}\n`, { encoding: "utf8", mode: 0o600 });
};
const writeRaw = (path: string, text: string): void => {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, text, { encoding: "utf8", mode: 0o600 });
};
const flipHex = (hex: string): string => `${hex.startsWith("0") ? "1" : "0"}${hex.slice(1)}`;
const flipDigest = (digest: string): string => `sha256:${flipHex(digest.slice("sha256:".length))}`;

// ---- minimization ----

type Endpoint = { commit: string; tree: string };
function recoveryEndpoints(coherent: string): { before: Endpoint; after: Endpoint } {
  const investigation = readJ(join(coherent, "bundle", "investigation.json"));
  const transitions = investigation.transitions;
  if (!Array.isArray(transitions)) throw new Error("investigation has no transitions");
  const recovery = transitions.map(asJ).find((t) => t.kind === "FAIL_TO_PASS");
  if (recovery === undefined) throw new Error("investigation has no FAIL_TO_PASS transition");
  const endpoint = (value: unknown): Endpoint => ({ commit: asS(asJ(value).commit), tree: asS(asJ(value).tree) });
  return { before: endpoint(recovery.before), after: endpoint(recovery.after) };
}

const MIN_NAMES = ["witness-null", "frozen-wrong", "witness-wrong", "before-null", "after-null", "before-commit",
  "before-tree", "after-commit", "after-tree", "wrong-transition"] as const;

function mutateMinimization(source: J, name: (typeof MIN_NAMES)[number], coherent: string): J {
  const result = structuredClone(source);
  switch (name) {
    case "witness-null": result.witness = null; break;
    case "frozen-wrong": { const w = asJ(result.witness); w.frozenDigest = flipDigest(asS(w.frozenDigest)); break; }
    case "witness-wrong": { const w = asJ(result.witness); w.witnessDigest = flipDigest(asS(w.witnessDigest)); break; }
    case "before-null": result.before = null; break;
    case "after-null": result.after = null; break;
    case "before-commit": { const s = asJ(result.before); s.commit = flipHex(asS(s.commit)); break; }
    case "before-tree": { const s = asJ(result.before); s.tree = flipHex(asS(s.tree)); break; }
    case "after-commit": { const s = asJ(result.after); s.commit = flipHex(asS(s.commit)); break; }
    case "after-tree": { const s = asJ(result.after); s.tree = flipHex(asS(s.tree)); break; }
    case "wrong-transition": {
      const recovery = recoveryEndpoints(coherent);
      result.before = { commit: recovery.before.commit, tree: recovery.before.tree };
      result.after = { commit: recovery.after.commit, tree: recovery.after.tree };
      break;
    }
  }
  return result;
}

// ---- repair ----

function signPacket(packet: J): J {
  const { packetDigest: _old, ...unsigned } = packet;
  return { ...unsigned, packetDigest: digestJson(unsigned) };
}
function signRepairManifest(manifest: J): J {
  const { manifestDigest: _old, ...unsigned } = manifest;
  return { ...unsigned, manifestDigest: digestJson(unsigned) };
}

function writeRepairVariant(destination: string, readme: string, packet: unknown, brief: unknown, manifest: unknown): void {
  writeRaw(join(destination, "README.md"), readme);
  writeJ(join(destination, "evidence-packet.json"), packet);
  writeJ(join(destination, "repair-brief.json"), brief);
  writeJ(join(destination, "manifest.json"), manifest);
}

const REPAIR_NAMES = ["investigation-wrong", "frozen-wrong", "legacy", "citations-invalid", "manifest-malformed",
  "packet-malformed", "brief-malformed", "manifest-content"] as const;

function buildRepairVariant(source: string, destination: string, name: (typeof REPAIR_NAMES)[number]): void {
  const readme = readFileSync(join(source, "README.md"), "utf8");
  const packet = readJ(join(source, "evidence-packet.json"));
  const brief = readJ(join(source, "repair-brief.json"));
  const manifest = readJ(join(source, "manifest.json"));
  /** Re-sign packet, brief link and manifest after a packet edit. */
  const resigned = (editedPacket: J): void => {
    const signed = signPacket(editedPacket);
    const linkedBrief = { ...brief, evidencePacketDigest: signed.packetDigest };
    const linkedManifest = signRepairManifest({
      ...manifest,
      evidencePacket: { path: "evidence-packet.json", digest: signed.packetDigest },
      repairBrief: { path: "repair-brief.json", digest: digestJson(linkedBrief) }
    });
    writeRepairVariant(destination, readme, signed, linkedBrief, linkedManifest);
  };
  switch (name) {
    case "investigation-wrong": resigned({ ...packet, investigationDigest: flipDigest(asS(packet.investigationDigest)) }); break;
    case "frozen-wrong": resigned({ ...packet, frozenWitnessDigest: flipDigest(asS(packet.frozenWitnessDigest)) }); break;
    case "legacy":
      // Manifest bytes unchanged: only the packet schema version moves to the legacy value.
      writeRepairVariant(destination, readme, { ...packet, schemaVersion: "faultline.repair-evidence.v1" }, brief, manifest);
      break;
    case "citations-invalid": {
      const edited = { ...brief, proposedInvariant: { ...asJ(brief.proposedInvariant), evidenceIds: ["E1", "E99"] } };
      const linkedManifest = signRepairManifest({ ...manifest, repairBrief: { path: "repair-brief.json", digest: digestJson(edited) } });
      writeRepairVariant(destination, readme, packet, edited, linkedManifest);
      break;
    }
    case "manifest-malformed": writeRepairVariant(destination, readme, packet, brief, { notAManifest: true }); break;
    case "packet-malformed": writeRepairVariant(destination, readme, { schemaVersion: "faultline.repair-evidence.v2" }, brief, manifest); break;
    case "brief-malformed": writeRepairVariant(destination, readme, packet, { schemaVersion: "faultline.repair-brief.v1" }, manifest); break;
    case "manifest-content": {
      const linked = signRepairManifest({ ...manifest, repairBrief: { path: "repair-brief.json", digest: flipDigest(digestJson(brief)) } });
      writeRepairVariant(destination, readme, packet, brief, linked);
      break;
    }
  }
}

// ---- prevention ----

function writePreventionVariant(destination: string, readme: string, body: J, repairedRuns: J | null, manifestSource: J): void {
  const unsigned = {
    schemaVersion: manifestSource.schemaVersion, classification: manifestSource.classification,
    prevention: { path: "prevention.json", digest: digestJson(body) }, limitations: body.limitations
  };
  const manifest = { ...unsigned, rootDigest: digestJson(unsigned) };
  writeRaw(join(destination, "README.md"), readme);
  writeJ(join(destination, "prevention.json"), body);
  writeJ(join(destination, "manifest.json"), manifest);
  if (repairedRuns !== null) writeJ(join(destination, "repaired-runs.json"), repairedRuns);
}

const PREVENTION_NAMES = ["root-wrong", "witness-wrong", "investigation-wrong", "runs-wrong", "runs-missing"] as const;

function buildPreventionVariant(source: string, destination: string, name: (typeof PREVENTION_NAMES)[number]): void {
  const readme = readFileSync(join(source, "README.md"), "utf8");
  const body = structuredClone(readJ(join(source, "prevention.json")));
  const manifest = readJ(join(source, "manifest.json"));
  const runs = structuredClone(readJ(join(source, "repaired-runs.json")));
  switch (name) {
    case "root-wrong":
      body.originalProofRoot = flipDigest(asS(body.originalProofRoot));
      writePreventionVariant(destination, readme, body, runs, manifest);
      break;
    case "witness-wrong": {
      // Change the witness consistently everywhere so the package's own semantic checks still pass.
      const replacement = flipDigest(asS(body.frozenWitnessDigest));
      body.frozenWitnessDigest = replacement;
      for (const key of ["lastGood", "firstBad", "repaired"]) asJ(body[key]).witnessDigest = replacement;
      const list = runs.runs;
      if (!Array.isArray(list)) throw new Error("repaired-runs.json has no runs");
      for (const run of list) asJ(run).witnessDigest = replacement;
      writePreventionVariant(destination, readme, body, runs, manifest);
      break;
    }
    case "investigation-wrong":
      body.investigationDigest = flipDigest(asS(body.investigationDigest));
      writePreventionVariant(destination, readme, body, runs, manifest);
      break;
    case "runs-wrong": {
      const list = runs.runs;
      if (!Array.isArray(list)) throw new Error("repaired-runs.json has no runs");
      asJ(list[0]).runId = flipDigest(asS(asJ(list[0]).runId));
      // Manifest and prevention body are unchanged; only the sidecar disagrees.
      writePreventionVariant(destination, readme, body, runs, manifest);
      break;
    }
    case "runs-missing": writePreventionVariant(destination, readme, body, null, manifest); break;
  }
}

// ---- public builder ----

function artifactDigest(coherent: string, artifact: string): string {
  const absolute = join(coherent, artifact);
  if (artifact.endsWith(".json")) return digestJson(readJ(absolute));
  const repairManifestPath = join(absolute, "manifest.json");
  const manifest = readJ(repairManifestPath);
  if (typeof manifest.manifestDigest === "string") return manifest.manifestDigest;
  if (typeof manifest.rootDigest === "string") return manifest.rootDigest;
  throw new Error(`no retained digest field in ${artifact}`);
}

/**
 * Writes mutations/** and mutations/index.json into the coherent fixture. The
 * index carries TS-recomputed external digests that each case pairs with its
 * mutated artifact; malformed manifests keep the original artifact digest
 * because no valid replacement digest exists.
 */
export function buildServeBundleMutations(coherent: string): void {
  if (existsSync(join(coherent, "mutations"))) throw new Error("mutations already exist; refusing to modify them");
  const certified = readJ(join(coherent, "minimization.json"));
  for (const name of MIN_NAMES) {
    writeJ(join(coherent, "mutations", "min", `${name}.json`), mutateMinimization(certified, name, coherent));
  }
  const repairSource = join(coherent, ".faultline", "repair-briefs", "attached");
  for (const name of REPAIR_NAMES) buildRepairVariant(repairSource, join(coherent, "mutations", "repair", name), name);
  const preventionSource = join(coherent, ".faultline", "prevention-proofs", "verified");
  for (const name of PREVENTION_NAMES) buildPreventionVariant(preventionSource, join(coherent, "mutations", "prevention", name), name);

  const originalRepair = artifactDigest(coherent, ".faultline/repair-briefs/attached");
  const originalVerified = artifactDigest(coherent, ".faultline/prevention-proofs/verified");
  const digests: Record<string, string> = {};
  for (const artifact of MUTATION_ARTIFACTS) {
    if (artifact === "mutations/repair/manifest-malformed" || artifact === "mutations/repair/packet-malformed"
      || artifact === "mutations/repair/brief-malformed" || artifact === "mutations/repair/legacy") {
      // No valid replacement manifest digest exists (or the manifest is intentionally unchanged).
      digests[artifact] = originalRepair;
    } else if (artifact === "mutations/prevention/runs-wrong" || artifact === "mutations/prevention/runs-missing") {
      digests[artifact] = originalVerified;
    } else {
      digests[artifact] = artifactDigest(coherent, artifact);
    }
  }
  writeJ(join(coherent, MUTATIONS_INDEX_PATH.slice("inputs/coherent/".length)), { schema: MUTATIONS_INDEX_SCHEMA, digests });
}

/**
 * Literal strings the privacy-minimized view must never contain, taken from the
 * retained coherent artifacts themselves (witness behavior/command/overlay,
 * reviewer, incident text, the recorded repository path) plus the builder's
 * explicit repair/prevention sentinels and recorded raw output.
 */
export function collectPrivateSentinels(coherent: string): string[] {
  const frozen = readJ(join(coherent, "bundle", "witness", "frozen.json"));
  const proposal = asJ(frozen.proposal);
  const witness = asJ(proposal.witness);
  const incident = asJ(proposal.incidentPacket);
  const investigation = readJ(join(coherent, "bundle", "investigation.json"));
  const repository = investigation.repository;
  if (typeof repository !== "string" || repository === "") throw new Error("investigation.json carries no repository path to inventory");
  return [
    asS(witness.behavior), asS(witness.command), "FL_PRIVATE_OVERLAY_5C_NOT_FOR_HTML",
    asS(asJ(frozen.approval).approvedBy), asS(incident.symptom), asS(incident.ciLog), repository,
    "FL_PRIVATE_INVARIANT_5C_NOT_FOR_HTML", "FL_PRIVATE_REPAIR_5C_NOT_FOR_HTML", "FL_PRIVATE_HARD_5C_NOT_FOR_HTML",
    "FL_PRIVATE_SOFT_5C_NOT_FOR_HTML", "FL_PRIVATE_DIAGNOSTIC_5C_NOT_FOR_HTML", "witness failed", "state=bad"
  ];
}
