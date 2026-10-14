// Writer replay scenarios (slice 2 spec 2.4): the workspace a writer case
// starts from. Each scenario is a pure function of its name, built once by
// running frozen TS under the seed preload and committed under
// difftest/testdata/scenarios/<name>/, so the Go replay copies the exact
// files fl would have written instead of rebuilding them.
//   pnpm build && pnpm exec tsx difftest/gen/scenarios.ts
import { spawn } from "node:child_process";
import { createHash, createPrivateKey, createPublicKey } from "node:crypto";
import { chmodSync, cpSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { canonicalJson, digestJson } from "../../src/canonical.js";

const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cli = join(repo, "dist", "cli.js");
const seedPreload = join(repo, "difftest", "gen", "seed.mjs");
export const scenariosDir = join(repo, "difftest", "testdata", "scenarios");
const recorderDir = join(repo, "difftest", "testdata", "recorder");
const fakeDockerDir = join(repo, "difftest", "testdata", "fakedocker");

// The pinned oracle release: CI regenerates on it and fails on drift.
export function requireOracleNode(): void {
  if (process.version !== "v22.22.2" && process.env.FAULTLINE_ALLOW_NODE_MISMATCH !== "1") {
    console.error(`writer goldens come from Node v22.22.2, not ${process.version} (FAULTLINE_ALLOW_NODE_MISMATCH=1 for local experiments only)`);
    process.exit(1);
  }
}

// Goldens must come from a build of the current src/.
function newestMtime(dir: string): number {
  let newest = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    newest = Math.max(newest, entry.isDirectory() ? newestMtime(path) : statSync(path).mtimeMs);
  }
  return newest;
}
export function requireFreshBuild(): void {
  if (!existsSync(cli) || statSync(cli).mtimeMs < newestMtime(join(repo, "src"))) {
    console.error("dist/cli.js is missing or older than src/: run `pnpm build` first");
    process.exit(1);
  }
}

// CaseRoots are the directories one case owns: the workspace (the command's
// cwd), HOME and TMPDIR, all under one physical mkdtemp directory so the
// paths fl prints are the ones the harness masks.
export type CaseRoots = { root: string; ws: string; home: string; tmp: string; argvLog: string };

export function makeCaseRoots(): CaseRoots {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "faultline-writer-")));
  const roots = { root, ws: join(root, "ws"), home: join(root, "home"), tmp: join(root, "tmp"), argvLog: join(root, "argv.log") };
  for (const dir of [roots.ws, roots.home, roots.tmp]) mkdirSync(dir, { mode: 0o755 });
  writeFileSync(join(roots.home, "empty.gitconfig"), "");
  return roots;
}

// inheritedOut names the variables a writer case never inherits: git's own,
// FaultLine's, and those src/ reads to detect CI or a live model.
export function inheritedOut(name: string): boolean {
  return name.startsWith("GIT_") || name.startsWith("FAULTLINE_") || ["CI", "GITHUB_ACTIONS", "OPENAI_API_KEY"].includes(name);
}

// The environment every writer invocation runs with; extra is the
// template's env (after placeholder substitution).
export function writerEnv(roots: CaseRoots, seed: string, fakeDocker: string | undefined, extra: Record<string, string>): NodeJS.ProcessEnv {
  const path = [recorderDir, ...(fakeDocker ? [join(fakeDockerDir, fakeDocker)] : []), process.env.PATH ?? ""].join(":");
  return {
    ...Object.fromEntries(Object.entries(process.env).filter(([name]) => !inheritedOut(name))),
    HOME: roots.home, USERPROFILE: roots.home, TMPDIR: roots.tmp, TEMP: roots.tmp, TMP: roots.tmp,
    GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(roots.home, "empty.gitconfig"), LC_ALL: "C.UTF-8", LANGUAGE: "", GIT_DEFAULT_HASH: "sha1",
    FAULTLINE_TEST_SEED: seed, FAULTLINE_NO_BROWSER: "1", FAULTLINE_ARGV_LOG: roots.argvLog, PATH: path,
    ...extra
  };
}

export type RunResult = { stdout: string; stderr: string; exit: number };

// runCli runs frozen TS under the seed preload with cwd = the workspace.
export function runCli(roots: CaseRoots, argv: string[], env: NodeJS.ProcessEnv): Promise<RunResult> {
  return new Promise((done) => {
    const child = spawn(process.execPath, ["--import", pathToFileURL(seedPreload).href, cli, ...argv], { env, cwd: roots.ws });
    const out: Buffer[] = [];
    const err: Buffer[] = [];
    child.stdout.on("data", (b) => out.push(b));
    child.stderr.on("data", (b) => err.push(b));
    child.on("close", (code) => done({ stdout: Buffer.concat(out).toString("utf8"), stderr: Buffer.concat(err).toString("utf8"), exit: code ?? -1 }));
  });
}

export const PROPOSAL_ID = "demo-1";

// PROPOSAL_INPUT is the `witness propose --input` file the witness scenarios
// start from; it stays in the workspace as proposal.json.
export const PROPOSAL_INPUT = {
  proposalId: PROPOSAL_ID,
  incidentPacket: {
    symptom: "The repository state must not be bad.",
    ciLog: "state.txt became bad",
    repositoryLanguage: "Text",
    repositorySummary: "Temporary repository for the writer replay."
  },
  witness: {
    behavior: "state.txt must not be bad",
    command: "node witness.mjs",
    overlays: [{ path: "witness.mjs", bytesBase64: Buffer.from("export const faultLineWitness = 'approved-exact-bytes';\n", "utf8").toString("base64") }],
    policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 30 }
  }
};

const PROPOSE = ["witness", "propose", "--input", "proposal.json"];
const APPROVE = ["witness", "approve", PROPOSAL_ID, "--approved-by", "reviewer@example.test"];
const FREEZE = ["witness", "freeze", PROPOSAL_ID];

// Reviewer key material for the authenticated approval cases. The Ed25519
// keys come from fixed 32-byte seeds (PKCS#8 = this prefix + the seed), so
// the committed PEM files, keyrings and (Ed25519 being deterministic) the
// signatures never change. EC_PUBLIC_PEM is a P-256 public key generated
// once: a well-formed key that is not Ed25519.
const PKCS8_ED25519_PREFIX = Buffer.from("302e020100300506032b657004220420", "hex");
const EC_PUBLIC_PEM = "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEU2XZiU7j36AfpHTmuOZEZ2mFYM+I\nZTCOIJ2bfqCoMGaDZCGDX78GItE2wsgVj+xSpCzpOJ/9EwO81d3+N1OiDA==\n-----END PUBLIC KEY-----\n";
// Well-formed keys that are not Ed25519 (generated once; Ed448/secp256k1 and
// the encrypted key are not reproducible from a seed).
const ED448_PRIVATE_PEM = "-----BEGIN PRIVATE KEY-----\nMEcCAQAwBQYDK2VxBDsEOdqvXW7sO1iXB6BRc4sn+F/f0jMqrH6lGHY+Oy03FHKu\nqZ2KYhuuf16t1NvHuCZG3Hi6+H6DqLYapA==\n-----END PRIVATE KEY-----\n";
const ED448_PUBLIC_PEM = "-----BEGIN PUBLIC KEY-----\nMEMwBQYDK2VxAzoAOj/Ms7xqdC1QlHezuPgrgdUyvK7uYmT4vkmVrRtDGaVn2TWQ\nawWjXb5D0TGKIzuUeUZf0pptOtoA\n-----END PUBLIC KEY-----\n";
const SECP256K1_PRIVATE_PEM = "-----BEGIN EC PRIVATE KEY-----\nMHQCAQEEIDSHyk7wRzw0YflBhDutD562C5xXxxbstdZfiip/QlncoAcGBSuBBAAK\noUQDQgAEUp5H3ToOAUymGbgmlDpwHeWE9ArepYvcrSfjhvTtjfM8Yi0WopHoLU5u\n7qqxBe7j1HjnB7paWOwQwI60+l4dhQ==\n-----END EC PRIVATE KEY-----\n";
const ENCRYPTED_PRIVATE_PEM = "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIGjMF8GCSqGSIb3DQEFDTBSMDEGCSqGSIb3DQEFDDAkBBAVpSZOUbkT/x3QSiHy\nue+pAgIIADAMBggqhkiG9w0CCQUAMB0GCWCGSAFlAwQBKgQQLOooz/ez8eiHfX+2\nLk8lugRAj0sFD8p9nJ0j5ztbsKwOL/kG0bGjbnVE7Lao3p/qEOk6IZAxm6pEVR/5\n+3ajc+6Z0dCzY7XmRqjz3tqpQgOfJA==\n-----END ENCRYPTED PRIVATE KEY-----\n";
function reviewerKey(seedByte: number) {
  const privateKey = createPrivateKey({ key: Buffer.concat([PKCS8_ED25519_PREFIX, Buffer.alloc(32, seedByte)]), format: "der", type: "pkcs8" });
  const publicKey = createPublicKey(privateKey);
  const der = publicKey.export({ type: "spki", format: "der" });
  return {
    privatePem: privateKey.export({ type: "pkcs8", format: "pem" }) as string,
    publicPem: publicKey.export({ type: "spki", format: "pem" }) as string,
    keyId: `sha256:${createHash("sha256").update(der).digest("hex")}`
  };
}
const KEY1 = reviewerKey(1);
const KEY2 = reviewerKey(2);
const reviewerEntry = (approvedBy: string, k: { publicPem: string; keyId: string }) => ({ approvedBy, keyId: k.keyId, algorithm: "ED25519", publicKeyPem: k.publicPem });
const keyring = (...reviewers: object[]) => `${JSON.stringify({ schemaVersion: "faultline.reviewer-keyring.v1", reviewers }, null, 2)}\n`;
const REVIEWER = "reviewer@example.test";
const KEY_FILES: Record<string, string> = {
  "keys/reviewer.pem": KEY1.privatePem,
  "keys/other.pem": KEY2.privatePem,
  "keys/garbage.pem": "this is not a key\n",
  "keys/keyring.json": keyring(reviewerEntry(REVIEWER, KEY1)),
  "keys/keyring-other-key.json": keyring(reviewerEntry(REVIEWER, KEY2)),
  "keys/keyring-other-identity.json": keyring(reviewerEntry("someone-else@example.test", KEY1)),
  "keys/keyring-duplicate.json": keyring(reviewerEntry(REVIEWER, KEY1), reviewerEntry(REVIEWER, KEY1)),
  "keys/keyring-ec.json": keyring({ approvedBy: REVIEWER, keyId: KEY1.keyId, algorithm: "ED25519", publicKeyPem: EC_PUBLIC_PEM }),
  "keys/keyring-bad-pem.json": keyring({ approvedBy: REVIEWER, keyId: KEY1.keyId, algorithm: "ED25519", publicKeyPem: "not a pem" }),
  "keys/keyring-wrong-keyid.json": keyring({ approvedBy: REVIEWER, keyId: KEY2.keyId, algorithm: "ED25519", publicKeyPem: KEY1.publicPem }),
  "keys/keyring-empty.json": keyring(),
  "keys/ed448.pem": ED448_PRIVATE_PEM,
  "keys/secp256k1.pem": SECP256K1_PRIVATE_PEM,
  "keys/encrypted.pem": ENCRYPTED_PRIVATE_PEM,
  "keys/keyring-ed448.json": keyring({ approvedBy: REVIEWER, keyId: KEY1.keyId, algorithm: "ED25519", publicKeyPem: ED448_PUBLIC_PEM }),
  // OpenSSL reads the PUBLIC KEY block first whatever the order of the blocks.
  "keys/keyring-mixed-pub-priv.json": keyring({ ...reviewerEntry(REVIEWER, KEY1), publicKeyPem: KEY1.publicPem + KEY2.privatePem }),
  "keys/keyring-mixed-priv-pub.json": keyring({ ...reviewerEntry(REVIEWER, KEY1), publicKeyPem: KEY2.privatePem + KEY1.publicPem }),
  "keys/keyring-mixed-mismatch.json": keyring({ ...reviewerEntry(REVIEWER, KEY2), publicKeyPem: KEY1.publicPem + KEY2.privatePem }),
  "keys/keyring-two-reviewers.json": keyring(reviewerEntry(REVIEWER, KEY2), reviewerEntry("someone-else@example.test", KEY1))
};
const SIGN = ["witness", "sign", PROPOSAL_ID, "--private-key", "keys/reviewer.pem", "--keyring", "keys/keyring.json"];
const PROPOSAL_FILES = { "proposal.json": `${JSON.stringify(PROPOSAL_INPUT, null, 2)}\n` };

// forgeSignature flips one signature byte of the receipt and recomputes
// receiptDigest, so only the Ed25519 check can fail.
function forgeSignature(ws: string): void {
  const path = join(ws, ".faultline", "witnesses", "authenticated-approvals", `${PROPOSAL_ID}.json`);
  const receipt = JSON.parse(readFileSync(path, "utf8")) as Record<string, string>;
  const signature = Buffer.from(receipt.signatureBase64!, "base64");
  signature[0]! ^= 0x01;
  receipt.signatureBase64 = signature.toString("base64");
  delete receipt.receiptDigest;
  receipt.receiptDigest = digestJson(receipt);
  writeFileSync(path, `${canonicalJson(receipt)}\n`);
}


// Standing-policy scenarios (Task 8). policyFile hand-writes a policy the
// CLI cannot (overlay templates, a tampered digest); the others are frozen
// through `fl witness policy freeze`, whose policies never carry templates.
const NO_OVERLAY_INPUT = { ...PROPOSAL_INPUT, witness: { ...PROPOSAL_INPUT.witness, overlays: [] } };
const WITNESS_BYTES = PROPOSAL_INPUT.witness.overlays[0]!.bytesBase64;
const policyFreeze = (id: string, command: string, maxTimeout: string): string[] =>
  ["witness", "policy", "freeze", "--policy-id", id, "--frozen-by", "ops@example.test", "--command", command, "--max-timeout", maxTimeout];
const POLICY_DIR = ".faultline/witnesses/standing-policies";
function policyFile(policyId: string, templates: object[], maxTimeoutSeconds: number, digestOf = maxTimeoutSeconds): string {
  const body = (timeout: number) => ({
    schemaVersion: "faultline.standing-approval-policy.v1", policyId, frozenBy: "ops@example.test", frozenAt: "2026-07-16T11:00:00.000Z",
    allowedCommands: ["node witness.mjs"], allowedOverlayTemplates: templates, maxTimeoutSeconds: timeout, network: "disabled", credentials: "redacted"
  });
  return `${JSON.stringify({ ...body(maxTimeoutSeconds), policyDigest: digestJson(body(digestOf)) }, null, 2)}\n`;
}
const POLICY_FILES: Record<string, string> = {
  [`${POLICY_DIR}/pt-match.json`]: policyFile("pt-match", [{ path: "witness.mjs", bytesBase64: WITNESS_BYTES }], 60),
  [`${POLICY_DIR}/pt-bytes.json`]: policyFile("pt-bytes", [{ path: "witness.mjs", bytesBase64: "b3RoZXI=" }], 60),
  [`${POLICY_DIR}/pt-count.json`]: policyFile("pt-count", [{ path: "a.mjs", bytesBase64: "" }, { path: "witness.mjs", bytesBase64: WITNESS_BYTES }], 60),
  [`${POLICY_DIR}/pt-tamper.json`]: policyFile("pt-tamper", [{ path: "witness.mjs", bytesBase64: WITNESS_BYTES }], 61, 60),
  [`${POLICY_DIR}/pt-badschema.json`]: `${JSON.stringify({ schemaVersion: "faultline.standing-approval-policy.v1", policyId: "pt-badschema" }, null, 2)}\n`,
  [`${POLICY_DIR}/pt-badjson.json`]: "{\n"
};
const POLICY_FREEZES = [policyFreeze("p-ok", "node witness.mjs", "60"), policyFreeze("p-cmd", "node other.mjs", "60"), policyFreeze("p-timeout", "node witness.mjs", "10")];

// Ledger-binding scenario (Task 9): the committed fully bound ledger and Git
// investigation, a partially bound ledger (valid, but without a checkpoint
// for every state), investigations that break the binding, and a bound
// record `fl ledger bind` writes; `after` derives tampered copies of it.
const baseText = (rel: string): string => readFileSync(join(repo, "difftest", "testdata", "bases", rel), "utf8");
const flipLast = (hex: string): string => `${hex.slice(0, -1)}${hex.endsWith("0") ? "1" : "0"}`;
const jsonText = (value: unknown): string => `${JSON.stringify(value, null, 2)}\n`;
const editedJson = (rel: string, edit: (v: any) => void): string => {
  const value = JSON.parse(baseText(rel));
  edit(value);
  return jsonText(value);
};
const LEDGER_BOUND_FILES: Record<string, string> = {
  "ledger.json": baseText("git-fully-bound/lifecycle/ledger.json"),
  "ledger-partial.json": baseText("git-partially-bound/lifecycle/ledger.json"),
  "ledger-tampered.json": editedJson("git-fully-bound/lifecycle/ledger.json", (v) => { v.events[0].hash = flipLast(v.events[0].hash); }),
  "ledger-invalid.json": editedJson("git-fully-bound/lifecycle/ledger.json", (v) => { delete v.ledgerId; }),
  "investigation.json": baseText("git-fully-bound/investigation.json"),
  "investigation-tree-mismatch.json": editedJson("git-fully-bound/investigation.json", (v) => { v.states[0].tree = flipLast(v.states[0].tree); }),
  "investigation-invalid.json": editedJson("git-fully-bound/investigation.json", (v) => { delete v.schemaVersion; }),
  "not-json.txt": "{\n",
  "out.json/keep": "occupied\n"
};
const LEDGER_BIND = ["ledger", "bind", "--ledger", "ledger.json", "--investigation", "investigation.json", "--output", "bound.json"];
function tamperBound(ws: string): void {
  const bound = JSON.parse(readFileSync(join(ws, "bound.json"), "utf8"));
  const write = (name: string, edit: (v: any) => void): void => {
    const copy = JSON.parse(JSON.stringify(bound));
    edit(copy);
    writeFileSync(join(ws, name), `${canonicalJson(copy)}\n`);
  };
  write("bound-digest-tampered.json", (v) => { v.bindingDigest = `sha256:${flipLast(v.bindingDigest.slice(7))}`; });
  write("bound-schema-tampered.json", (v) => { v.nativeCodexInterception = true; });
  write("bound-binding-tampered.json", (v) => { v.stateBindings[0].checkpointEventSequence += 1; });
}

// SCENARIOS: the slice 1 bases a scenario copies in (difftest/testdata/bases,
// keyed by workspace path), the files it writes, then the fl invocations that
// build the rest of it (each under FAULTLINE_TEST_SEED=scenario:<name>).
type Scenario = { bases?: Record<string, string>; files: Record<string, string>; argv: string[][]; after?: (ws: string) => void };
export const SCENARIOS: Record<string, Scenario> = {
  "empty": { files: {}, argv: [] },
  "witness-proposed": { files: { "proposal.json": `${JSON.stringify(PROPOSAL_INPUT, null, 2)}\n` }, argv: [PROPOSE] },
  "witness-approved": { files: { "proposal.json": `${JSON.stringify(PROPOSAL_INPUT, null, 2)}\n` }, argv: [PROPOSE, APPROVE] },
  "witness-frozen": { files: { "proposal.json": `${JSON.stringify(PROPOSAL_INPUT, null, 2)}\n` }, argv: [PROPOSE, APPROVE, FREEZE] },
  "witness-approved-keys": { files: { ...PROPOSAL_FILES, ...KEY_FILES }, argv: [PROPOSE, APPROVE] },
  "witness-frozen-keys": { files: { ...PROPOSAL_FILES, ...KEY_FILES }, argv: [PROPOSE, APPROVE, FREEZE] },
  "witness-signed": { files: { ...PROPOSAL_FILES, ...KEY_FILES }, argv: [PROPOSE, APPROVE, FREEZE, SIGN] },
  "witness-signed-forged": { files: { ...PROPOSAL_FILES, ...KEY_FILES }, argv: [PROPOSE, APPROVE, FREEZE, SIGN], after: forgeSignature },
  "witness-overlay-policies": { files: { ...PROPOSAL_FILES, ...POLICY_FILES }, argv: [PROPOSE, ...POLICY_FREEZES] },
  "witness-nooverlay-policies": { files: { "proposal.json": `${JSON.stringify(NO_OVERLAY_INPUT, null, 2)}\n`, ...POLICY_FILES }, argv: [PROPOSE, ...POLICY_FREEZES] },
  // A Git proof bundle: `fl verify bundle` runs git, so it checks the
  // recorders and the argv normalization.
  "git-two-states-bundle": { bases: { bundle: "git-two-states" }, files: {}, argv: [] },
  "ledger-bound": { files: LEDGER_BOUND_FILES, argv: [LEDGER_BIND], after: tamperBound }
};

// A scenario is committed as <name>/dirs.txt, every directory of the
// workspace one per line (git keeps no empty directories, and the witness
// store makes some), and <name>/tree/, its files.
// copyScenario fills the workspace from the committed scenario.
export function copyScenario(name: string, ws: string): void {
  if (!Object.hasOwn(SCENARIOS, name)) throw new Error(`unknown scenario ${name}`);
  const dir = join(scenariosDir, name);
  for (const rel of readFileSync(join(dir, "dirs.txt"), "utf8").split("\n").filter(Boolean)) mkdirSync(join(ws, rel), { recursive: true });
  if (existsSync(join(dir, "tree"))) cpSync(join(dir, "tree"), ws, { recursive: true });
}

// resetModes sets directories to 0755 and files to 0644 under root. A case
// calls it once its scenario and mutations are in place, so the workspace
// fl starts from never depends on the checkout or on how the harness wrote
// it (Node's 0o666/0o777 defaults vs Go's 0o644/0o755 under umask 0); the Go
// twin is resetModes in difftest/scenarios_test.go.
export function resetModes(root: string, rel = ""): void {
  for (const name of readdirSync(join(root, rel))) {
    const r = rel ? `${rel}/${name}` : name;
    const stat = lstatSync(join(root, r));
    if (stat.isDirectory()) {
      chmodSync(join(root, r), 0o755);
      resetModes(root, r);
    } else if (stat.isFile()) chmodSync(join(root, r), 0o644);
  }
}

// listDirs returns every directory under root, sorted; it refuses anything
// but directories and regular files.
function listDirs(root: string, rel = ""): string[] {
  const out: string[] = [];
  for (const name of readdirSync(join(root, rel)).sort()) {
    const r = rel ? `${rel}/${name}` : name;
    const stat = lstatSync(join(root, r));
    if (stat.isDirectory()) out.push(r, ...listDirs(root, r));
    else if (!stat.isFile()) throw new Error(`scenario entry ${r} is not a regular file`);
  }
  return out;
}

async function buildScenario(name: string): Promise<void> {
  const scenario = SCENARIOS[name]!;
  const roots = makeCaseRoots();
  try {
    for (const [path, base] of Object.entries(scenario.bases ?? {})) {
      cpSync(join(repo, "difftest", "testdata", "bases", base), join(roots.ws, path), { recursive: true });
    }
    for (const [path, text] of Object.entries(scenario.files)) {
      mkdirSync(dirname(join(roots.ws, path)), { recursive: true });
      writeFileSync(join(roots.ws, path), text);
    }
    for (const argv of scenario.argv) {
      const r = await runCli(roots, argv, writerEnv(roots, `scenario:${name}`, "pass-fail", {}));
      if (r.exit !== 0) throw new Error(`scenario ${name}: fl ${argv.join(" ")} exited ${r.exit}\n${r.stdout}${r.stderr}`);
    }
    scenario.after?.(roots.ws);
    const dirs = listDirs(roots.ws);
    const out = join(scenariosDir, name);
    rmSync(out, { recursive: true, force: true });
    mkdirSync(out, { recursive: true });
    writeFileSync(join(out, "dirs.txt"), dirs.map((d) => `${d}\n`).join(""));
    if (readdirSync(roots.ws).length > 0) cpSync(roots.ws, join(out, "tree"), { recursive: true });
  } finally {
    rmSync(roots.root, { recursive: true, force: true });
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  // Umask 0, like gen-writers.ts: fl's modes reach the files as requested.
  process.umask(0);
  requireOracleNode();
  requireFreshBuild();
  for (const entry of existsSync(scenariosDir) ? readdirSync(scenariosDir) : []) {
    if (!Object.hasOwn(SCENARIOS, entry)) rmSync(join(scenariosDir, entry), { recursive: true, force: true });
  }
  for (const name of Object.keys(SCENARIOS).sort()) {
    await buildScenario(name);
    console.log(`scenario ${name}`);
  }
}
