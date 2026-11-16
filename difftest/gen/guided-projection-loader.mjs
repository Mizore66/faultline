// Test-only ESM loader for the guided projection capture (Task5C). It never edits the frozen
// orchestration body: dist/guided-investigate.js is loaded byte-for-byte from disk. Only the EXACT
// imported dependency specifiers are resolved to scripted stub modules; every stub just forwards its
// arguments to globalThis.__flProjection, which logs them and returns the fixture's scripted value.
// doctorCliExitCode and defaultIncidentDraftStore are re-exported from the REAL compiled modules.
import { readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { pathToFileURL } from "node:url";

// Exact import map of the frozen compiled module: specifier -> imported names (in source order).
export const importAllowlist = {
  "node:child_process": ["spawnSync"],
  "node:fs": ["readFileSync"],
  "node:path": ["join", "resolve"],
  "./doctor.js": ["doctorCliExitCode", "runFaultLineDoctor"],
  "./incident-continue.js": ["runFrozenIncidentContinuation"],
  "./incident.js": ["createIncidentDraft"],
  "./incident-store.js": ["defaultIncidentDraftStore", "readIncidentDraft", "writeIncidentDraft"],
  "./runtime.js": ["resolveCuratedRuntime"],
  "./witness-review-server.js": ["startWitnessReviewServer"],
  "./witness-lock.js": ["proposeWitness", "verifyFrozenWitness"],
};

export function parseImports(source) {
  const found = {};
  const re = /^import \{([^}]*)\} from "([^"]+)";$/gm;
  let m;
  while ((m = re.exec(source)) !== null) found[m[2]] = m[1].split(",").map((s) => s.trim()).filter(Boolean);
  return found;
}

export function assertImportAllowlist(source) {
  const found = parseImports(source);
  const want = JSON.stringify(importAllowlist);
  const got = JSON.stringify(found);
  if (want !== got) throw new Error(`guided module import set differs from allowlist\nwant ${want}\ngot  ${got}`);
  const total = (source.match(/^import /gm) ?? []).length;
  if (total !== Object.keys(importAllowlist).length) throw new Error(`unexpected import statement count ${total}`);
}

function stubSource(spec, distUrl) {
  const h = `const h = globalThis.__flProjection;\n`;
  const real = (file) => JSON.stringify(new URL(file, distUrl).href);
  switch (spec) {
    case "./doctor.js":
      return `${h}export { doctorCliExitCode } from ${real("doctor.js")};\nexport const runFaultLineDoctor = async (...a) => h.call("doctor", a);\n`;
    case "./incident-continue.js":
      return `${h}export const runFrozenIncidentContinuation = async (...a) => h.call("continue", a);\n`;
    case "./incident.js":
      return `${h}export const createIncidentDraft = (...a) => h.unexpected("createIncidentDraft", a);\n`;
    case "./incident-store.js":
      return `${h}export { defaultIncidentDraftStore } from ${real("incident-store.js")};\n` +
        `export const readIncidentDraft = (...a) => h.call("readDraft", a);\n` +
        `export const writeIncidentDraft = (...a) => h.unexpected("writeIncidentDraft", a);\n`;
    case "./runtime.js":
      return `${h}export const resolveCuratedRuntime = async (...a) => h.call("resolveRuntime", a);\n`;
    case "./witness-review-server.js":
      return `${h}export const startWitnessReviewServer = (...a) => h.unexpected("startWitnessReviewServer", a);\n`;
    case "./witness-lock.js":
      return `${h}export const proposeWitness = (...a) => h.unexpected("proposeWitness", a);\n` +
        `export const verifyFrozenWitness = (...a) => h.call("verify", a);\n`;
    default:
      throw new Error(`no stub for ${spec}`);
  }
}

// install({ distFile }) returns { moduleUrl, resolved } where resolved records every specifier the
// frozen module actually resolved, so the driver can reject missing imports.
export function install({ distFile }) {
  const moduleUrl = pathToFileURL(distFile).href;
  const source = readFileSync(distFile, "utf8");
  assertImportAllowlist(source);
  const resolved = [];
  registerHooks({
    resolve(specifier, context, nextResolve) {
      if (context.parentURL === moduleUrl) {
        if (!Object.hasOwn(importAllowlist, specifier)) throw new Error(`unexpected import ${specifier}`);
        resolved.push(specifier);
        if (specifier.startsWith("node:")) return nextResolve(specifier, context);
        return { url: `faultline-projection-stub:${specifier}`, shortCircuit: true };
      }
      return nextResolve(specifier, context);
    },
    load(url, context, nextLoad) {
      if (url.startsWith("faultline-projection-stub:")) {
        return { format: "module", source: stubSource(url.slice("faultline-projection-stub:".length), moduleUrl), shortCircuit: true };
      }
      return nextLoad(url, context);
    },
  });
  return { moduleUrl, resolved };
}
