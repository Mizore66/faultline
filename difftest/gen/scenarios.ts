// Writer replay scenarios (slice 2 spec 2.4): the workspace a writer case
// starts from. Each scenario is a pure function of its name, built once by
// running frozen TS under the seed preload and committed under
// difftest/testdata/scenarios/<name>/, so the Go replay copies the exact
// files fl would have written instead of rebuilding them.
//   pnpm build && pnpm exec tsx difftest/gen/scenarios.ts
import { execFileSync, spawn } from "node:child_process";
import { createHash, createPrivateKey, createPublicKey } from "node:crypto";
import { chmodSync, cpSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, renameSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { deflateSync } from "node:zlib";
import { canonicalJson, digestJson } from "../../src/canonical.js";
import { computeEnvironmentFingerprint } from "../../src/environment-fingerprint.js";
import { appendLifecycleEvent, createCodexLifecycleLedger, GIT_CHECKPOINT_VERSION, signGitCheckpoint, type CodexLifecycleLedger } from "../../src/ledger.js";
import { createIncidentDraft } from "../../src/incident.js";
import { writeIncidentDraft } from "../../src/incident-store.js";
import { proposeWitness } from "../../src/witness-lock.js";

const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cli = join(repo, "dist", "cli.js");
const seedPreload = join(repo, "difftest", "gen", "seed.mjs");
const fetchShim = join(repo, "difftest", "gen", "fetch-shim.mjs");
export const scenariosDir = join(repo, "difftest", "testdata", "scenarios");
const recorderDir = join(repo, "difftest", "testdata", "recorder");
const fakeDockerDir = join(repo, "difftest", "testdata", "fakedocker");
const fakeToolsDir = join(repo, "difftest", "testdata", "faketools");
export const fakeCodexDir = join(repo, "difftest", "testdata", "fakecodex");
export const fakeGhDir = join(repo, "difftest", "testdata", "fakegh");

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
export type CaseRoots = { root: string; ws: string; home: string; tmp: string; argvLog: string; recorder: string };

export function makeCaseRoots(): CaseRoots {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "faultline-writer-")));
  const roots = { root, ws: join(root, "ws"), home: join(root, "home"), tmp: join(root, "tmp"), argvLog: join(root, "argv.log"), recorder: join(root, "recorder") };
  for (const dir of [roots.ws, roots.home, roots.tmp, roots.recorder]) mkdirSync(dir, { mode: 0o755 });
  writeFileSync(join(roots.home, "empty.gitconfig"), "");
  writeRecorders(roots);
  return roots;
}

// writeRecorders copies the recorder templates into the case's own recorder
// directory with the argv log path written into the script: hardened git
// runs with PATH as its only environment variable, so a recorder that read
// FAULTLINE_ARGV_LOG would miss every git call fl hardens (the Go twin is
// writeRecorders in difftest/writer_test.go).
export function writeRecorders(roots: CaseRoots): void {
  for (const name of ["git", "docker"]) {
    const template = readFileSync(join(recorderDir, name), "utf8");
    const line = "log=$FAULTLINE_ARGV_LOG\n";
    if (!template.includes(line) || roots.argvLog.includes("'")) throw new Error(`recorder template ${name} cannot take ${roots.argvLog}`);
    writeFileSync(join(roots.recorder, name), template.replace(line, `log='${roots.argvLog}'\n`), { mode: 0o755 });
    chmodSync(join(roots.recorder, name), 0o755);
  }
}

// inheritedOut names the variables a writer case never inherits: git's own,
// FaultLine's, GitHub Actions' (provenance create reads GITHUB_SHA and the
// rest: slice 4c correction 6), and those src/ reads to detect CI or a live
// model. The Go twin is inheritedOut in difftest/writer_test.go.
export function inheritedOut(name: string): boolean {
  return name.startsWith("GIT_") || name.startsWith("FAULTLINE_") || name.startsWith("GITHUB_") || ["CI", "OPENAI_API_KEY"].includes(name);
}

// installFakeCodex copies difftest/testdata/fakecodex/<name>/codex to
// <HOME>/fakecodex/codex (mode 0755) with the case's argv log path written
// into its `log=` line, as writeRecorders does: fl runs codex with PATH as
// its only environment variable. It returns the copy's path. The Go twin is
// installFakeCodex in difftest/writer_test.go.
export function installFakeCodex(roots: CaseRoots, name: string): string {
  const template = readFileSync(join(fakeCodexDir, name, "codex"), "utf8");
  const line = "log=$FAULTLINE_ARGV_LOG\n";
  if (!template.includes(line) || roots.argvLog.includes("'")) throw new Error(`fake codex ${name} cannot take ${roots.argvLog}`);
  const dir = join(roots.home, "fakecodex");
  mkdirSync(dir, { recursive: true, mode: 0o755 });
  const bin = join(dir, "codex");
  writeFileSync(bin, template.replace(line, `log='${roots.argvLog}'\n`), { mode: 0o755 });
  chmodSync(bin, 0o755);
  return bin;
}

// WriterFakes are a template's slice 4c fakes (difftest/testdata/fakecodex,
// difftest/testdata/fakegh and the fake OpenAI server's URL).
export type WriterFakes = { fakeCodex?: string | undefined; fakeCodexOnPath?: boolean | undefined; fakeGh?: string | undefined; openAIURL?: string | undefined };

// linkPathTools gives a case whose template lists pathTools a PATH with no
// docker on it (difftest/testdata/faketools/README.md): <case>/bin holds a
// symlink to the first executable of each listed name on the inherited
// PATH, and the case's recorder docker is removed unless docker is listed.
// It returns the bin directory. The Go twin is linkPathTools in
// difftest/writer_test.go.
export function linkPathTools(roots: CaseRoots, tools: readonly string[]): string {
  const bin = join(roots.root, "bin");
  if (existsSync(bin)) return bin;
  mkdirSync(bin, { mode: 0o755 });
  for (const name of tools) {
    const found = (process.env.PATH ?? "").split(":").filter(Boolean).map((dir) => join(dir, name)).find((p) => {
      try { return statSync(p).isFile() && (statSync(p).mode & 0o111) !== 0; } catch { return false; }
    });
    if (found === undefined) throw new Error(`pathTools: no ${name} on PATH`);
    symlinkSync(found, join(bin, name));
  }
  if (!tools.includes("docker")) rmSync(join(roots.recorder, "docker"));
  return bin;
}

// The environment every writer invocation runs with; extra is the
// template's env (after placeholder substitution). PATH is the recorders,
// then <HOME>/fakecodex (fakeCodexOnPath), then the template's fake gh
// (difftest/testdata/fakegh/<name>), its fake toolchain
// (difftest/testdata/faketools/<name>) and its fake Docker, then the
// inherited PATH, or, when the template lists pathTools, only those tools
// (linkPathTools). fakeCodex sets FAULTLINE_CODEX_BIN to the installed copy
// unless it is on PATH; a fake OpenAI server's URL is
// FAULTLINE_TEST_OPENAI_URL (fetch-shim.mjs). The Go twin is writerEnv in
// difftest/writer_test.go.
export function writerEnv(roots: CaseRoots, seed: string, fakeDocker: string | undefined, extra: Record<string, string>, fakeTools?: string, pathTools?: readonly string[], fakes: WriterFakes = {}): NodeJS.ProcessEnv {
  const inherited = pathTools ? linkPathTools(roots, pathTools) : process.env.PATH ?? "";
  const codexName = fakes.fakeCodex ?? (fakes.fakeCodexOnPath ? "edits" : undefined);
  const codex = codexName === undefined ? undefined : installFakeCodex(roots, codexName);
  const path = [
    roots.recorder, ...(codex !== undefined && fakes.fakeCodexOnPath ? [dirname(codex)] : []), ...(fakes.fakeGh ? [join(fakeGhDir, fakes.fakeGh)] : []),
    ...(fakeTools ? [join(fakeToolsDir, fakeTools)] : []), ...(fakeDocker ? [join(fakeDockerDir, fakeDocker)] : []), inherited
  ].join(":");
  return {
    ...Object.fromEntries(Object.entries(process.env).filter(([name]) => !inheritedOut(name))),
    HOME: roots.home, USERPROFILE: roots.home, TMPDIR: roots.tmp, TEMP: roots.tmp, TMP: roots.tmp,
    GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(roots.home, "empty.gitconfig"), LC_ALL: "C.UTF-8", LANGUAGE: "", GIT_DEFAULT_HASH: "sha1",
    FAULTLINE_TEST_SEED: seed, FAULTLINE_NO_BROWSER: "1", FAULTLINE_ARGV_LOG: roots.argvLog, PATH: path,
    ...(codex !== undefined && !fakes.fakeCodexOnPath ? { FAULTLINE_CODEX_BIN: codex } : {}),
    ...(fakes.openAIURL !== undefined ? { FAULTLINE_TEST_OPENAI_URL: fakes.openAIURL } : {}),
    ...extra
  };
}

export type RunResult = { stdout: string; stderr: string; exit: number };

// runCli runs frozen TS under the seed preload, then the fetch shim
// (fetch-shim.mjs: the fake OpenAI server), with cwd = the workspace.
export function runCli(roots: CaseRoots, argv: string[], env: NodeJS.ProcessEnv): Promise<RunResult> {
  return new Promise((done) => {
    const child = spawn(process.execPath, ["--import", pathToFileURL(seedPreload).href, "--import", pathToFileURL(fetchShim).href, cli, ...argv], { env, cwd: roots.ws });
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

// Git investigation scenarios (Task 15). A scenario repository is built with
// git in a scratch directory, then written into the workspace as loose
// objects deflated at level 0 (stored blocks: the same bytes from any zlib)
// with a minimal HEAD/config/ref and the descendant's files, and no index,
// logs or hooks, so its bytes depend on nothing but the commits. Each
// scenario also holds a frozen witness (proposal.json, propose, approve,
// freeze --repo repo: the session binding lands in repo/.faultline/incidents).
export const PINNED_IMAGE = `registry.example/faultline-node@sha256:${"b".repeat(64)}`;
const OTHER_IMAGE = `registry.example/faultline-node@sha256:${"c".repeat(64)}`;
type RepoState = Record<string, string>;
const state = (value: string, extra: RepoState = {}): RepoState => ({ "state.txt": `${value}\n`, ...extra });
// A RepoState entry whose text starts with SYMLINK is committed as a
// symbolic link to the rest of the text (only in a state before the last:
// the checked-out workspace holds regular files only).
const SYMLINK = "\0symlink:";
type BuiltRepo = { commits: string[]; trees: string[]; files: RepoState[] };

function scratchGit(dir: string, args: string[], objectFormat: string, input?: Buffer): Buffer {
  return execFileSync("git", ["-C", dir, ...args], {
    ...(input === undefined ? {} : { input }),
    env: {
      PATH: process.env.PATH ?? "", HOME: dir, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null", GIT_DEFAULT_HASH: objectFormat,
      GIT_AUTHOR_NAME: "FaultLine Test", GIT_AUTHOR_EMAIL: "faultline@example.test", GIT_AUTHOR_DATE: "2026-07-16T10:00:00Z",
      GIT_COMMITTER_NAME: "FaultLine Test", GIT_COMMITTER_EMAIL: "faultline@example.test", GIT_COMMITTER_DATE: "2026-07-16T10:00:00Z",
      LC_ALL: "C"
    },
    maxBuffer: 1 << 26
  });
}

// buildRepo writes repo `rel` of the workspace: one commit per state (each
// state is the whole tree), the last one checked out.
// hostile, when given, adds objects and refs git's porcelain cannot make
// (git mktree names no filesystem holds) before the objects are copied:
// it returns the extra branches and the objects to leave out.
type Hostile = (git: (args: string[], input?: Buffer) => Buffer, built: BuiltRepo) => { refs: Record<string, string>; drop: string[] };
function buildRepo(ws: string, rel: string, states: RepoState[], objectFormat: "sha1" | "sha256" = "sha1", hostile?: Hostile): BuiltRepo {
  const scratch = mkdtempSync(join(tmpdir(), "faultline-scenario-repo-"));
  try {
    scratchGit(scratch, ["init", "-q", `--object-format=${objectFormat}`, "--initial-branch=main"], objectFormat);
    const built: BuiltRepo = { commits: [], trees: [], files: states };
    let previous: RepoState = {};
    for (const [index, files] of states.entries()) {
      for (const name of Object.keys(previous)) rmSync(join(scratch, name), { force: true });
      for (const [name, text] of Object.entries(files)) {
        mkdirSync(dirname(join(scratch, name)), { recursive: true });
        if (text.startsWith(SYMLINK)) symlinkSync(text.slice(SYMLINK.length), join(scratch, name));
        else writeFileSync(join(scratch, name), text);
      }
      scratchGit(scratch, ["add", "-A"], objectFormat);
      scratchGit(scratch, ["commit", "-q", "--allow-empty", "-m", `state ${index}`], objectFormat);
      built.commits.push(scratchGit(scratch, ["rev-parse", "HEAD"], objectFormat).toString("utf8").trim());
      built.trees.push(scratchGit(scratch, ["rev-parse", "HEAD^{tree}"], objectFormat).toString("utf8").trim());
      previous = files;
    }
    const gitDir = join(ws, rel, ".git");
    for (const dir of ["objects", "refs/heads", "refs/tags"]) mkdirSync(join(gitDir, dir), { recursive: true });
    writeFileSync(join(gitDir, "HEAD"), "ref: refs/heads/main\n");
    writeFileSync(join(gitDir, "config"), objectFormat === "sha1"
      ? "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n"
      : "[core]\n\trepositoryformatversion = 1\n\tfilemode = true\n\tbare = false\n[extensions]\n\tobjectformat = sha256\n");
    writeFileSync(join(gitDir, "refs", "heads", "main"), `${built.commits.at(-1)}\n`);
    const extra = hostile?.((args, input) => scratchGit(scratch, args, objectFormat, input), built) ?? { refs: {}, drop: [] };
    for (const [name, commit] of Object.entries(extra.refs)) writeFileSync(join(gitDir, "refs", "heads", name), `${commit}\n`);
    const objects = scratchGit(scratch, ["cat-file", "--batch-all-objects", "--batch-check=%(objectname) %(objecttype)"], objectFormat)
      .toString("utf8").split("\n").filter(Boolean);
    for (const line of objects) {
      const [oid, type] = line.split(" ") as [string, string];
      if (extra.drop.includes(oid)) continue;
      const body = scratchGit(scratch, ["cat-file", type, oid], objectFormat);
      mkdirSync(join(gitDir, "objects", oid.slice(0, 2)), { recursive: true });
      writeFileSync(join(gitDir, "objects", oid.slice(0, 2), oid.slice(2)),
        deflateSync(Buffer.concat([Buffer.from(`${type} ${body.length}\0`), body]), { level: 0 }));
    }
    for (const [name, text] of Object.entries(states.at(-1)!)) {
      mkdirSync(dirname(join(ws, rel, name)), { recursive: true });
      writeFileSync(join(ws, rel, name), text);
    }
    return built;
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

// lifecycleLedger is a deterministic SIDE_CAR ledger with one clean
// checkpoint per listed state (fixtures.ts lifecycleBoundToDescendant and
// lifecycleBoundToEveryInvestigatedState, with fixed times and ids).
function lifecycleLedger(repo: BuiltRepo, indices: number[]): string {
  const at = (second: number) => new Date(Date.UTC(2026, 6, 16, 10, 0, second)).toISOString();
  let second = 0;
  let ledger: CodexLifecycleLedger = createCodexLifecycleLedger({ ledgerId: "ledger-scenario", sessionId: "scenario-session", createdAt: at(0) });
  const add = (input: Parameters<typeof appendLifecycleEvent>[1]) => {
    second += 1;
    ledger = appendLifecycleEvent(ledger, input, { occurredAt: at(second), eventId: `event-${second}` });
  };
  add({ type: "SESSION_STARTED", payload: { transport: "SIDE_CAR", workingDirectory: "/work/repository", model: "gpt-5.6" } });
  for (const [offset, index] of indices.entries()) {
    const turnOrdinal = offset + 1;
    add({ type: "TURN_STARTED", payload: { turnId: `turn-${turnOrdinal}`, turnOrdinal, promptDigest: `sha256:${"c".repeat(64)}` } });
    add({ type: "TURN_COMPLETED", payload: { turnId: `turn-${turnOrdinal}`, turnOrdinal, outcome: "COMPLETED", outputDigest: `sha256:${"d".repeat(64)}` } });
    const checkpoint = signGitCheckpoint({
      schemaVersion: GIT_CHECKPOINT_VERSION, repositoryRoot: "/work/repository", headCommit: repo.commits[index]!, treeDigest: repo.trees[index]!,
      capturedAt: at(second), clean: true
    });
    add({ type: "WORKTREE_CHECKPOINT", payload: { checkpoint, afterTurnOrdinal: turnOrdinal } });
  }
  add({ type: "SESSION_ENDED", payload: { reason: "COMPLETED", completedTurns: indices.length } });
  return jsonText(ledger);
}

// runtimeMapping maps the environment fingerprint of every state to an image.
function runtimeMapping(repo: BuiltRepo, images: string[]): string {
  const mapping: Record<string, string> = {};
  for (const [index, files] of repo.files.entries()) {
    const dir = mkdtempSync(join(tmpdir(), "faultline-scenario-fingerprint-"));
    try {
      for (const [name, text] of Object.entries(files)) writeFileSync(join(dir, name), text);
      mapping[computeEnvironmentFingerprint(dir).digest] = images[index % images.length]!;
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }
  return jsonText({ schemaVersion: "faultline.runtime-mapping.v1", mapping });
}

const CJK_OVERLAYS = [
  { path: "说明/视图.md", text: "view\n" },
  { path: "说明/神经.md", text: "nerve\n" },
  { path: "🤔.md", text: "thinking\n" },
  { path: "𠀀.txt", text: "ext-b\n" },
  { path: "中.txt", text: "middle\n" }
];
const witnessInput = (options: { overlays?: { path: string; text: string }[]; timeoutSeconds?: number } = {}): string => jsonText({
  ...PROPOSAL_INPUT,
  witness: {
    ...PROPOSAL_INPUT.witness,
    overlays: [...PROPOSAL_INPUT.witness.overlays, ...(options.overlays ?? []).map((o) => ({ path: o.path, bytesBase64: Buffer.from(o.text, "utf8").toString("base64") }))],
    policy: { ...PROPOSAL_INPUT.witness.policy, timeoutSeconds: options.timeoutSeconds ?? 30 }
  }
});
const FREEZE_IN_REPO = ["witness", "freeze", PROPOSAL_ID, "--repo", "repo"];
const repeat = (value: string, n: number): RepoState[] => Array.from({ length: n }, () => state(value));
function gitScenario(states: RepoState[], options: {
  objectFormat?: "sha1" | "sha256"; proposal?: string; extra?: (repo: BuiltRepo) => Record<string, string>; hostile?: Hostile
} = {}): Scenario {
  return {
    files: { "proposal.json": options.proposal ?? witnessInput() },
    setup: (ws) => {
      const repo = buildRepo(ws, "repo", states, options.objectFormat, options.hostile);
      for (const [path, text] of Object.entries(options.extra?.(repo) ?? {})) writeFileSync(join(ws, path), text);
    },
    argv: [PROPOSE, APPROVE, FREEZE_IN_REPO]
  };
}
const GOOD_BAD_REPAIRED = [state("good"), state("bad"), state("repaired")];
const manyFiles = (value: string): RepoState =>
  Object.fromEntries([["state.txt", "good\n"], ...Array.from({ length: 25 }, (_, i) => [`state${String(i + 1).padStart(2, "0")}.txt`, `${value}\n`])]);

// hostileTrees adds branches whose trees break materialization, patch
// derivation or ancestry in ways the filesystem and git porcelain cannot
// produce (round 8 review): each is a child of the good state (main~1)
// whose tree also holds state.txt = bad.
//   nonutf8      a blob named weird\377.txt (not UTF-8)
//   long         a blob with a 304-byte name (past NAME_MAX)
//   dotgit       a .GIT/config entry (git refuses the path)
//   ob0, ob1     ob0 holds the file `a`; its child ob1 holds `a/b` instead
//   lostblob     a blob whose object is left out of the repository
const hostileTrees: Hostile = (git, built) => {
  const blob = (text: string) => git(["hash-object", "-w", "--stdin"], Buffer.from(text)).toString("utf8").trim();
  const tree = (entries: string) => git(["mktree"], Buffer.from(entries, "latin1")).toString("utf8").trim();
  const commit = (t: string, parent: string | undefined, message: string) =>
    git(["commit-tree", t, ...(parent === undefined ? [] : ["-p", parent]), "-m", message]).toString("utf8").trim();
  const base = built.commits[0]!;
  const good = blob("good\n"), bad = blob("bad\n"), x = blob("x\n"), lost = blob("lost blob\n");
  const withBad = (entries: string) => tree(`100644 blob ${bad}\tstate.txt\n${entries}`);
  const ob0 = commit(tree(`100644 blob ${x}\ta\n100644 blob ${good}\tstate.txt\n`), undefined, "ob0");
  return {
    refs: {
      nonutf8: commit(withBad(`100644 blob ${x}\tweird\xff.txt\n`), base, "nonutf8"),
      long: commit(withBad(`100644 blob ${x}\t${"n".repeat(304)}\n`), base, "long"),
      dotgit: commit(withBad(`040000 tree ${tree(`100644 blob ${x}\tconfig\n`)}\t.GIT\n`), base, "dotgit"),
      ob0,
      ob1: commit(withBad(`040000 tree ${tree(`100644 blob ${x}\tb\n`)}\ta\n`), ob0, "ob1"),
      lostblob: commit(withBad(`100644 blob ${lost}\tlost.txt\n`), base, "lostblob")
    },
    drop: [lost]
  };
};

// Doctor scenarios (slice 3a Task 6). A doctor repository is buildRepo's
// repository at the workspace root plus an index: `git status` needs one
// (without it every tracked file is staged as deleted and untracked).
// writeIndex writes a version 2 index with no extensions whose entries
// carry only mode, size, object id and path (every other stat field zero),
// so its bytes depend on nothing but the entries; git refreshes the stat
// data on its first status, which the writer replay's .git/index rule
// (gen-writers.ts gitIndexDigest) leaves out of the comparison.
type IndexEntry = { path: string; content: string };
function writeIndex(gitDir: string, entries: IndexEntry[]): void {
  const sha1 = (bytes: Buffer) => createHash("sha1").update(bytes).digest();
  const parts: Buffer[] = [Buffer.from("DIRC"), Buffer.from([0, 0, 0, 2]), Buffer.alloc(4)];
  parts[2]!.writeUInt32BE(entries.length);
  for (const entry of [...entries].sort((a, b) => Buffer.compare(Buffer.from(a.path), Buffer.from(b.path)))) {
    const content = Buffer.from(entry.content, "utf8");
    const path = Buffer.from(entry.path, "utf8");
    const head = Buffer.alloc(62);
    head.writeUInt32BE(0o100644, 24);
    head.writeUInt32BE(content.length, 36);
    sha1(Buffer.concat([Buffer.from(`blob ${content.length}\0`), content])).copy(head, 40);
    head.writeUInt16BE(Math.min(path.length, 0xfff), 60);
    const length = 62 + path.length;
    parts.push(head, path, Buffer.alloc(8 - (length % 8)));
  }
  const body = Buffer.concat(parts);
  writeFileSync(join(gitDir, "index"), Buffer.concat([body, sha1(body)]));
}
// doctorRepo builds a one-commit repository of files at the workspace root
// with a matching index; edit then changes the workspace (and index).
function doctorRepo(files: RepoState, edit?: (ws: string) => void, config = ""): Scenario {
  return {
    files: {},
    setup: (ws) => {
      buildRepo(ws, ".", [files]);
      writeIndex(join(ws, ".git"), Object.entries(files).map(([path, content]) => ({ path, content })));
      if (config !== "") writeFileSync(join(ws, ".git", "config"), readFileSync(join(ws, ".git", "config"), "utf8") + config);
      edit?.(ws);
    },
    argv: []
  };
}
const DOCTOR_FILES: RepoState = { "state.txt": "good\n", "src/main.txt": "main\n" };

// Init scenarios (slice 3a Task 10). A Node repository at the workspace
// root. `fl init --yes` writes .faultlineignore before the pre-warm, so the
// repository is dirty by then and the capture takes the slow path (objects
// in .git/faultline/objects, an alternates entry); init-existing-ignore
// commits .faultlineignore and a .gitignore for .faultline/, so its
// pre-warm takes the clean fast path (INIT_CLEAN_FILES). init-sidecar commits a
// .faultlineignore that leaves out .codex/: the hook document holds the
// absolute --cli path, so a capture that included it would give a tree
// digest that depends on the case directory. Its --cli targets sit at the
// workspace root (tool.js, tool.ts, it's/tool.js). INIT_TOKEN is a
// high-entropy token (48 distinct characters) that the snapshot secret
// scan refuses as HIGH_ENTROPY_TOKEN; no real credential is committed.
const INIT_FILES: RepoState = { "package.json": "{\"name\":\"demo\"}\n", "state.txt": "good\n" };
const INIT_CLEAN_FILES: RepoState = { ...INIT_FILES, ".faultlineignore": "scratch/\n", ".gitignore": ".faultline/\n" };
const INIT_TOKEN = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdefghijkl";
const INIT_CONFIG = `${JSON.stringify({
  schemaVersion: "faultline.config.v1",
  image: `registry.example/faultline-node@sha256:${"b".repeat(64)}`,
  runtime: { alias: "node", image: `registry.example/faultline-node@sha256:${"b".repeat(64)}` },
  snapshot: { trackedFilesOnly: true },
  stores: { incidents: ".faultline/incidents", witnesses: ".faultline/witnesses", recordings: ".faultline/recordings" }
}, null, 2)}\n`;
// initWorktree builds a repository at main/ and a linked worktree of its
// commit at wt/ (wt/.git is a gitdir file, main/.git/worktrees/wt its
// administrative directory, every link relative), so wt's
// --git-common-dir is main/.git, not wt/.git. It holds INIT_CLEAN_FILES,
// so `fl init --yes` leaves it clean and the pre-warm's fast path writes
// the cache into main/.git/faultline; a dirty worktree's slow path stops
// at wt/.git (a file) with ENOTDIR, in TS too.
function initWorktree(ws: string): void {
  const built = buildRepo(ws, "main", [INIT_CLEAN_FILES]);
  const entries = Object.entries(INIT_CLEAN_FILES).map(([path, content]) => ({ path, content }));
  writeIndex(join(ws, "main", ".git"), entries);
  const admin = join(ws, "main", ".git", "worktrees", "wt");
  mkdirSync(admin, { recursive: true });
  writeFileSync(join(admin, "HEAD"), `${built.commits.at(-1)}\n`);
  writeFileSync(join(admin, "commondir"), "../..\n");
  writeFileSync(join(admin, "gitdir"), "../../../wt/.git\n");
  writeIndex(admin, entries);
  mkdirSync(join(ws, "wt"), { recursive: true });
  writeFileSync(join(ws, "wt", ".git"), "gitdir: ../main/.git/worktrees/wt\n");
  for (const [path, content] of Object.entries(INIT_CLEAN_FILES)) writeFileSync(join(ws, "wt", path), content);
}

// Incident continuation scenarios (slice 3b Task 6). Each holds a scenario
// repository at repo/ and the records `fl incident start --repo repo --id
// demo-1 --from <ancestor> --to <descendant> [--image <PINNED_IMAGE>]
// --command "node witness.mjs"` writes (cli-app.ts:589-693): a review-only
// proposal in repo/.faultline/witnesses and the draft bound to it in
// repo/.faultline/incidents. They are made with the TS functions start
// calls, not the CLI, for two reasons: start records the absolute
// repository, which would name the build directory, so the draft records
// the relative "repo" (resolve(draft.repository) is <WS>/repo on both
// sides); and the proposal's proposedAt and the draft's createdAt are fixed.
// The cases then run witness approve and freeze, incident status and
// incident continue. witness.mjs is committed in every state so the stored
// command names a real file; pass-fail fake Docker reads only state.txt.
const INCIDENT_ID = "demo-1";
const INCIDENT_COMMAND = "node witness.mjs";
const INCIDENT_AT = "2026-07-16T10:30:00.000Z";
const incidentState = (value: string): RepoState => state(value, { "witness.mjs": "export const faultLineWitness = 'incident';\n" });
function writeIncidentRecords(ws: string, options: { ancestor: string; runtime: boolean; swapProposal?: boolean; unbound?: boolean; foreignProposal?: boolean }): void {
  const repository = "repo";
  const draftInput = {
    draftId: INCIDENT_ID,
    createdAt: INCIDENT_AT,
    repository,
    command: INCIDENT_COMMAND,
    range: { ancestor: options.ancestor, descendant: "HEAD" },
    ...(options.runtime ? { runtime: { requested: "explicit", image: PINNED_IMAGE } } : {})
  };
  const preliminary = createIncidentDraft(draftInput);
  const store = join(ws, repository, ".faultline", "witnesses");
  const proposalInput = (command: string, proposalId: string = preliminary.draftId, symptom = "A human-reported command is failing and requires a reviewed, frozen witness before localization.") => ({
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: INCIDENT_AT,
    incidentPacket: {
      symptom,
      ciLog: "The exact user-supplied command is stored in the reviewable witness, not executed by incident intake.",
      repositoryLanguage: "Unknown",
      repositorySummary: `Review-only local Git range ${preliminary.range.ancestor} to ${preliminary.range.descendant} (${preliminary.range.source}). No remote base was selected.${preliminary.runtime === undefined ? "" : ` The selected local runtime resolves to ${preliminary.runtime.image}.`}`
    },
    witness: {
      behavior: "The human-supplied command must exit successfully at the selected immutable Git states.",
      command,
      overlays: [],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 300 }
    }
  });
  const proposal = proposeWitness(store, proposalInput(INCIDENT_COMMAND));
  const draft = createIncidentDraft({
    ...draftInput,
    proposal: {
      proposalId: proposal.proposalId,
      proposalDigest: proposal.proposalDigest,
      incidentPacketDigest: proposal.incidentPacketDigest,
      commandDigest: proposal.witness.commandDigest
    }
  });
  // An unbound draft: the one start would write without a proposal
  // (createIncidentDraft with no proposal binding, witnessState NOT_PROPOSED).
  writeIncidentDraft(join(ws, repository, ".faultline", "incidents"), options.unbound ? preliminary : draft);
  // A proposal stored under the draft's identifier whose own proposalId and
  // incident packet differ (same command): the witness store keys records
  // by the requested identifier and approve and freeze accept it, so only
  // the draft binding check sees the identifier and packet mismatch.
  if (options.foreignProposal) {
    rmSync(join(store, "proposals", `${INCIDENT_ID}.json`));
    proposeWitness(store, proposalInput(INCIDENT_COMMAND, "demo-2", "A different incident packet."));
    renameSync(join(store, "proposals", "demo-2.json"), join(store, "proposals", `${INCIDENT_ID}.json`));
  }
  // A frozen witness for a different command under the same identifier:
  // the review chain is intact, only the draft binding disagrees.
  if (options.swapProposal) {
    rmSync(join(store, "proposals", `${INCIDENT_ID}.json`));
    proposeWitness(store, proposalInput("node other.mjs"));
  }
}
const INCIDENT_STORE = "repo/.faultline/witnesses";
const INCIDENT_APPROVE = ["witness", "approve", INCIDENT_ID, "--approved-by", "reviewer@example.test", "--store", INCIDENT_STORE];
const INCIDENT_FREEZE = ["witness", "freeze", INCIDENT_ID, "--store", INCIDENT_STORE, "--repo", "repo"];
function incidentScenario(states: RepoState[], options: { runtime?: boolean; swapProposal?: boolean; unbound?: boolean; foreignProposal?: boolean; frozen?: boolean; extra?: (repo: BuiltRepo) => Record<string, string> } = {}): Scenario {
  return {
    files: {},
    setup: (ws) => {
      const built = buildRepo(ws, "repo", states);
      writeIncidentRecords(ws, {
        ancestor: `HEAD~${states.length - 1}`, runtime: options.runtime ?? true,
        ...(options.swapProposal ? { swapProposal: true } : {}),
        ...(options.unbound ? { unbound: true } : {}),
        ...(options.foreignProposal ? { foreignProposal: true } : {})
      });
      for (const [path, text] of Object.entries(options.extra?.(built) ?? {})) writeFileSync(join(ws, path), text);
    },
    argv: options.frozen ? [INCIDENT_APPROVE, INCIDENT_FREEZE] : []
  };
}
const INCIDENT_GOOD_BAD = [incidentState("good"), incidentState("bad")];

// SCENARIOS: the slice 1 bases a scenario copies in (difftest/testdata/bases,
// keyed by workspace path), the files it writes, then the fl invocations that
// build the rest of it (each under FAULTLINE_TEST_SEED=scenario:<name>).
type Scenario = {
  bases?: Record<string, string>; files: Record<string, string>; setup?: (ws: string) => void; argv: string[][]; after?: (ws: string) => void;
  // env is added to the environment of the scenario's own argv (read after setup).
  env?: (ws: string) => Record<string, string>;
};
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
  "ledger-bound": { files: LEDGER_BOUND_FILES, argv: [LEDGER_BIND], after: tamperBound },
  "git-two-states": gitScenario([state("good"), state("bad")]),
  "git-many-states": gitScenario([...repeat("good", 6), ...repeat("bad", 6)]),
  "git-max-states-overflow": gitScenario([...repeat("good", 4), ...repeat("bad", 4)]),
  "git-heterogeneous": gitScenario([state("good", { "package.json": "{\"name\":\"demo\",\"version\":\"1.0.0\"}\n" }),
    state("bad", { "package.json": "{\"name\":\"demo\",\"version\":\"2.0.0\"}\n" })], {
    extra: (repo) => ({ "runtime-mapping.json": runtimeMapping(repo, [PINNED_IMAGE, OTHER_IMAGE]) })
  }),
  "git-ledger-partial": gitScenario(GOOD_BAD_REPAIRED, { extra: (repo) => ({ "ledger.json": lifecycleLedger(repo, [2]) }) }),
  "git-ledger-full": gitScenario(GOOD_BAD_REPAIRED, { extra: (repo) => ({ "ledger.json": lifecycleLedger(repo, [0, 1, 2]) }) }),
  "git-unbound": gitScenario(GOOD_BAD_REPAIRED),
  "git-sha256": gitScenario(GOOD_BAD_REPAIRED, { objectFormat: "sha256" }),
  "git-cjk-overlays": gitScenario([state("good"), state("bad")], { proposal: witnessInput({ overlays: CJK_OVERLAYS }) }),
  "git-non-monotonic": gitScenario([state("good"), state("bad"), state("good"), state("bad")]),
  // Round 10 (r10-3 verify F4): two FAIL_TO_PASS transitions, both listed
  // in the proof page's recovery panel.
  "git-two-recoveries": gitScenario([state("good"), state("bad"), state("good"), state("bad"), state("good")]),
  "git-all-pass": gitScenario(repeat("good", 3)),
  "git-all-fail": gitScenario(repeat("bad", 3)),
  // A one-second witness. The CLI passes no sandbox limits, so this does
  // not reach the sandbox (TS too): investigate-docker-timeout's fake
  // Docker outlives the default 120 s timeout once instead.
  "git-hostile-trees": gitScenario([state("good"), state("bad")], { hostile: hostileTrees }),
  "git-short-timeout": gitScenario([state("good"), state("bad")], { proposal: witnessInput({ timeoutSeconds: 1 }) }),
  // Round 10 (r10-3 G1): the states differ only in uv.lock, an environment
  // descriptor no other case changes, so a name missing from the
  // descriptor list turns CONFIGURATION_ERROR into a proof.
  "git-descriptor-change": gitScenario([state("good", { "uv.lock": "version = 1\n" }), state("bad", { "uv.lock": "version = 2\n" })]),
  // Round 10 (r10-3 N1): 25 files that all turn bad, of which only the pair
  // state13/state14 fails the witness (fake Docker pair-bad), so ddmin
  // narrows a failing subset over several granularities.
  "git-many-files": gitScenario([manyFiles("good"), manyFiles("bad")]),
  // Round 10 (r10-3 G3): an unstable state (the witness does not parse,
  // fake Docker broken-state) between the PASS and the FAIL state, so the
  // two stable states are not adjacent and no transition may be claimed.
  "git-unstable-middle": gitScenario([state("good"), state("broken"), state("bad")]),
  // Round 11 (r11-3 G10): the frozen overlay's target witness.mjs is a
  // symbolic link in one middle state only, so materializing that state
  // fails in the execution loop and the error alone must make the status
  // EXECUTION_ERROR (no transition: the stable states are not adjacent).
  "git-overlay-symlink-middle": gitScenario([state("good"), state("good", { "witness.mjs": `${SYMLINK}state.txt` }), state("bad")]),
  // The same symbolic link beside an adjacent PASS/FAIL pair: the pair is a
  // transition, and only the error keeps it from being claimed as proof.
  "git-overlay-symlink-pair": gitScenario([state("good"), state("bad"), state("bad", { "witness.mjs": `${SYMLINK}state.txt` }), state("bad")]),
  // judge-proof and commit-proof-preview read the committed COMMIT_PROOF
  // sample from the workspace by default.
  "commit-proof-sample": {
    files: {}, argv: [],
    setup: (ws) => cpSync(join(repo, "docs", "samples", "self-incident-commit-proof"), join(ws, "docs", "samples", "self-incident-commit-proof"), { recursive: true })
  },
  // Git proof bundles whose pages take the lifecycle-bound, recovery and
  // unstable-state branches (and one the loader refuses).
  // Doctor (slice 3a Task 6): a clean repository; a modified file and an
  // untracked one (2 changed paths); a staged rename (git status -z lists
  // a rename as two NUL-separated paths, so doctor counts 2); no
  // repository; runtime markers; and a repository-local fsmonitor command
  // that the hardened doctor git calls must never run (tests/cli.test.ts:664:
  // it would leave fsmonitor-ran in the repository).
  "repo-clean": doctorRepo(DOCTOR_FILES),
  "repo-dirty": doctorRepo(DOCTOR_FILES, (ws) => {
    writeFileSync(join(ws, "state.txt"), "bad\n");
    writeFileSync(join(ws, "notes.txt"), "untracked\n");
  }),
  "repo-rename": doctorRepo(DOCTOR_FILES, (ws) => {
    rmSync(join(ws, "state.txt"));
    writeFileSync(join(ws, "renamed.txt"), "good\n");
    writeIndex(join(ws, ".git"), [{ path: "renamed.txt", content: "good\n" }, { path: "src/main.txt", content: "main\n" }]);
  }),
  "not-a-repo": { files: { "notes.txt": "not a repository\n" }, argv: [] },
  "runtime-markers-multiple": doctorRepo({ ...DOCTOR_FILES, "package.json": "{\"name\":\"demo\"}\n", "pyproject.toml": "[project]\nname = \"demo\"\n", "go.mod": "module example.test/demo\n" }),
  "runtime-markers-go": doctorRepo({ ...DOCTOR_FILES, "go.mod": "module example.test/demo\n" }),
  "hostile-fsmonitor": doctorRepo(DOCTOR_FILES, undefined, "\tfsmonitor = \"printf invoked > fsmonitor-ran #\"\n"),
  // fl runtime (slice 3a Task 5). project-context is a fixed project build
  // context at the workspace root (the default --context): its plan's
  // context and Dockerfile digests depend only on these bytes and the 0644
  // modes resetModes gives them. repo-with-config holds an existing project
  // config (relative stores, a node runtime) that `fl runtime resolve`
  // updates in place.
  "project-context": {
    files: {
      "Dockerfile": "FROM node:22-alpine\nWORKDIR /app\nCOPY package.json ./\nCOPY src ./src\nRUN node --version\n",
      "package.json": "{\"name\":\"demo\",\"version\":\"1.0.0\"}\n",
      "src/index.js": "console.log(\"demo\");\n",
      "src/lib/util.js": "export const answer = 42;\n",
      "docker/alt.Dockerfile": "FROM node:22-alpine\nCOPY . /app\n"
    },
    argv: []
  },
  "repo-with-config": {
    files: {
      ".faultline/config.json": `${JSON.stringify({
        schemaVersion: "faultline.config.v1",
        image: PINNED_IMAGE,
        runtime: { alias: "node", image: PINNED_IMAGE },
        snapshot: { trackedFilesOnly: true },
        stores: { incidents: ".faultline/incidents", witnesses: ".faultline/witnesses", recordings: ".faultline/recordings" }
      }, null, 2)}\n`
    },
    argv: []
  },
  "init-fresh": doctorRepo(INIT_FILES),
  "init-existing-config": doctorRepo(INIT_FILES, (ws) => {
    mkdirSync(join(ws, ".faultline"), { recursive: true });
    writeFileSync(join(ws, ".faultline", "config.json"), INIT_CONFIG);
  }),
  "init-existing-ignore": doctorRepo(INIT_CLEAN_FILES),
  "init-existing-hooks": doctorRepo(INIT_FILES, (ws) => {
    mkdirSync(join(ws, ".codex"), { recursive: true });
    writeFileSync(join(ws, ".codex", "hooks.json"), "{}\n");
    writeFileSync(join(ws, "tool.js"), "export {};\n");
  }),
  "init-dirty-secret": doctorRepo(INIT_FILES, (ws) => writeFileSync(join(ws, "notes.txt"), `token ${INIT_TOKEN}\n`)),
  "init-worktree": { files: { "tool.js": "export {};\n" }, setup: initWorktree, argv: [] },
  "init-sidecar": doctorRepo({ ...INIT_FILES, ".faultlineignore": "# the hook document holds the absolute --cli path\n.codex/\n" }, (ws) => {
    writeFileSync(join(ws, "tool.js"), "export {};\n");
    writeFileSync(join(ws, "tool.ts"), "export {};\n");
    mkdirSync(join(ws, "it's"), { recursive: true });
    writeFileSync(join(ws, "it's", "tool.js"), "export {};\n");
  }),
  "proof-bases": {
    bases: Object.fromEntries(["git-fully-bound", "git-partially-bound", "git-shape-mixed-history", "git-sha256"].map((b) => [`bases/${b}`, b])),
    files: {}, argv: []
  },
  // Incident status and continue (slice 3b Task 6).
  "incident-two-states": incidentScenario(INCIDENT_GOOD_BAD),
  "incident-two-states-frozen": incidentScenario(INCIDENT_GOOD_BAD, { frozen: true }),
  "incident-no-runtime": incidentScenario(INCIDENT_GOOD_BAD, { runtime: false }),
  "incident-all-pass": incidentScenario([incidentState("good"), incidentState("good")]),
  "incident-binding-mismatch": incidentScenario(INCIDENT_GOOD_BAD, { swapProposal: true }),
  // Slice 3b Task 9: the binding check's unbound-draft and identifier /
  // packet branches.
  "incident-unbound": incidentScenario(INCIDENT_GOOD_BAD, { unbound: true }),
  "incident-foreign-proposal": incidentScenario(INCIDENT_GOOD_BAD, { foreignProposal: true }),
  "incident-ledger-full": incidentScenario(GOOD_BAD_REPAIRED.map((s) => incidentState(s["state.txt"]!.trim())), {
    extra: (built) => ({ "ledger.json": lifecycleLedger(built, [0, 1, 2]) })
  })
};

// A scenario is committed as <name>/dirs.txt, every directory of the
// workspace one per line (git keeps no empty directories, and the witness
// store makes some), and <name>/tree/, its files.
// copyScenario fills the workspace from the committed scenario.
export function copyScenario(name: string, ws: string): void {
  if (!Object.hasOwn(SCENARIOS, name)) throw new Error(`unknown scenario ${name}`);
  const dir = join(scenariosDir, name);
  for (const rel of readFileSync(join(dir, "dirs.txt"), "utf8").split("\n").filter(Boolean)) mkdirSync(join(ws, rel), { recursive: true });
  if (existsSync(join(dir, "tree"))) copyMapped(join(dir, "tree"), ws, (entry) => (entry === COMMITTED_GIT_DIR ? ".git" : entry));
}

// Git refuses to commit a path with a .git component, so a scenario
// repository's .git directory is committed as COMMITTED_GIT_DIR (dirs.txt
// keeps the real names); the Go twin is copyScenario in scenarios_test.go.
export const COMMITTED_GIT_DIR = "dot-git";

// copyMapped copies the files under from into to, renaming each path
// component with rename; directories are created as needed.
function copyMapped(from: string, to: string, rename: (entry: string) => string): void {
  for (const entry of readdirSync(from)) {
    const source = join(from, entry);
    const target = join(to, rename(entry));
    if (lstatSync(source).isDirectory()) {
      mkdirSync(target, { recursive: true });
      copyMapped(source, target, rename);
    } else {
      cpSync(source, target);
    }
  }
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
    scenario.setup?.(roots.ws);
    for (const argv of scenario.argv) {
      const r = await runCli(roots, argv, writerEnv(roots, `scenario:${name}`, "pass-fail", scenario.env?.(roots.ws) ?? {}));
      if (r.exit !== 0) throw new Error(`scenario ${name}: fl ${argv.join(" ")} exited ${r.exit}\n${r.stdout}${r.stderr}`);
    }
    scenario.after?.(roots.ws);
    const dirs = listDirs(roots.ws);
    const out = join(scenariosDir, name);
    rmSync(out, { recursive: true, force: true });
    mkdirSync(out, { recursive: true });
    writeFileSync(join(out, "dirs.txt"), dirs.map((d) => `${d}\n`).join(""));
    if (readdirSync(roots.ws).length > 0) {
      mkdirSync(join(out, "tree"));
      copyMapped(roots.ws, join(out, "tree"), (entry) => {
        if (entry === COMMITTED_GIT_DIR) throw new Error(`scenario ${name} has a ${COMMITTED_GIT_DIR} entry`);
        return entry === ".git" ? COMMITTED_GIT_DIR : entry;
      });
    }
  } finally {
    rmSync(roots.root, { recursive: true, force: true });
  }
}

// Incident intake scenarios (slice 3b Task 4). Each holds a repository at
// repo/ (buildRepo: objects, refs and the last state's files, no index;
// `git show -s` and the intake's rev-parse calls need none).
//   incident-repo          two commits; repo/sub/ is a subdirectory
//   incident-upstream      three local commits and a refs/remotes/origin/main
//                          tracking ref on a side commit of the first: the
//                          local branch is ahead by two and behind by one,
//                          so the upstream merge-base (commit 0) and the
//                          parent fallback (commit 1) are distinct candidates
//   incident-root          one commit (a root HEAD)
//   incident-merge         HEAD is a merge of commit 1 and a side commit
//   incident-command-file  incident-repo plus --command-file inputs: cmd.txt
//                          (regular), big.txt (32,001 bytes), bad.txt
//                          (invalid UTF-8) and cmd-dir/ (a directory); the
//                          symlink comes from a writer mutation, since a
//                          committed scenario holds regular files only
const INCIDENT_STATES: RepoState[] = [state("good", { "sub/keep.txt": "keep\n" }), state("bad", { "sub/keep.txt": "keep\n" })];
let incidentUpstreamTip = "";
const incidentUpstreamSide: Hostile = (git, built) => {
  const blob = git(["hash-object", "-w", "--stdin"], Buffer.from("upstream\n")).toString("utf8").trim();
  const tree = git(["mktree"], Buffer.from(`100644 blob ${blob}\tstate.txt\n`)).toString("utf8").trim();
  incidentUpstreamTip = git(["commit-tree", tree, "-p", built.commits[0]!, "-m", "upstream"]).toString("utf8").trim();
  return { refs: {}, drop: [] };
};
const incidentMergeHead: Hostile = (git, built) => {
  const blob = git(["hash-object", "-w", "--stdin"], Buffer.from("side\n")).toString("utf8").trim();
  const sideTree = git(["mktree"], Buffer.from(`100644 blob ${blob}\tstate.txt\n`)).toString("utf8").trim();
  const side = git(["commit-tree", sideTree, "-p", built.commits[0]!, "-m", "side"]).toString("utf8").trim();
  const merge = git(["commit-tree", built.trees.at(-1)!, "-p", built.commits.at(-1)!, "-p", side, "-m", "merge"]).toString("utf8").trim();
  // Written after refs/heads/main, so main becomes the merge.
  return { refs: { main: merge }, drop: [] };
};
function intakeScenario(states: RepoState[], hostile?: Hostile, edit?: (ws: string) => void): Scenario {
  return { files: {}, setup: (ws) => { buildRepo(ws, "repo", states, "sha1", hostile); edit?.(ws); }, argv: [] };
}
Object.assign(SCENARIOS, {
  "incident-repo": intakeScenario(INCIDENT_STATES),
  "incident-upstream": intakeScenario([...INCIDENT_STATES, state("worse", { "sub/keep.txt": "keep\n" })], incidentUpstreamSide, (ws) => {
    const gitDir = join(ws, "repo", ".git");
    mkdirSync(join(gitDir, "refs", "remotes", "origin"), { recursive: true });
    writeFileSync(join(gitDir, "refs", "remotes", "origin", "main"), `${incidentUpstreamTip}\n`);
    writeFileSync(join(gitDir, "config"), `${readFileSync(join(gitDir, "config"), "utf8")}[remote "origin"]\n\turl = https://example.invalid/faultline.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n[branch "main"]\n\tremote = origin\n\tmerge = refs/heads/main\n`);
  }),
  "incident-root": intakeScenario([state("bad")]),
  "incident-merge": intakeScenario(INCIDENT_STATES, incidentMergeHead),
  "incident-command-file": intakeScenario(INCIDENT_STATES, undefined, (ws) => {
    writeFileSync(join(ws, "cmd.txt"), "grep -q good state.txt && echo 'état: ✓' # \"quoted\" $HOME\n");
    writeFileSync(join(ws, "big.txt"), "x".repeat(32_001));
    writeFileSync(join(ws, "bad.txt"), Buffer.from([0x65, 0x63, 0x68, 0x6f, 0x20, 0xff, 0x0a]));
    mkdirSync(join(ws, "cmd-dir"));
  })
} satisfies Record<string, Scenario>);
// fl prevention (slice 3b Task 8). prevention-input holds the slice 1
// write inputs (fixtures.ts sampleInput and groundedPreventionInput) and
// the two packages the slice 1 bases wrote from them. prevention-from-bundle
// is the slice 2 base git-unbound plus the repaired runs of its third state
// (transitions[1].after: commit, tree, its three runs' runId/executionId),
// bound to the frozen witness digest and the homogeneous environment digest.
const { sampleInput: preventionSummaryInput, groundedPreventionInput } = await import("./fixtures.js");
function preventionRepairedRuns(): string {
  const investigation = JSON.parse(baseText("git-unbound/investigation.json"));
  const frozen = JSON.parse(baseText("git-unbound/witness/frozen.json"));
  const state = investigation.transitions[1].after;
  return jsonText({
    runs: state.runIds.map((runId: string, i: number) => ({
      runId, executionId: state.executionIds[i], commit: state.commit, tree: state.tree, verdict: "PASS",
      witnessDigest: frozen.witnessDigest, environmentDigest: investigation.environment.distinctDigests[0],
      executionTrust: "NATIVE_DOCKER", executionKind: "EXECUTED"
    }))
  });
}
SCENARIOS["prevention-input"] = {
  bases: { "summary-pkg": "prevention-summary", "verified-pkg": "prevention-verified" },
  files: { "summary.json": jsonText(preventionSummaryInput()), "grounded.json": jsonText(groundedPreventionInput()) },
  argv: []
};
SCENARIOS["prevention-from-bundle"] = { bases: { bundle: "git-unbound" }, files: { "repaired-runs.json": preventionRepairedRuns() }, argv: [] };
// fl attest (slice 4c Task 15): one directory per proof bundle kind. turn is
// the smallest directory whose manifest.json names the turn bundle schema;
// notjson/manifest.json is not JSON.
SCENARIOS["attest-bundles"] = {
  bases: { git: "git-unbound", demo: "demo-replay", prevention: "prevention-verified" },
  files: {
    "turn/manifest.json": `${JSON.stringify({ schemaVersion: "faultline.turn-proof-bundle.v1" }, null, 2)}\n`,
    "notjson/manifest.json": "{"
  },
  argv: []
};

// fl provenance (slice 4c Task 17). provenance-ci is the slice 2 base
// git-unbound at bundle/ plus a repository at . with one commit (the tree
// GITHUB_SHA names); sha.txt holds that commit id. provenance-signed adds
// the TS-written .faultline/provenance/ci-receipt.json (created under the CI
// env below), a trust.json whose trustedRootFile is the relative root.json,
// root.json and attestation.sigstore.json (their content is only ever
// handed to the fake gh).
function provenanceRepository(ws: string): void {
  const built = buildRepo(ws, ".", [state("good")]);
  writeFileSync(join(ws, "sha.txt"), `${built.commits[0]}\n`);
}
function provenanceCiEnv(ws: string): Record<string, string> {
  return {
    GITHUB_ACTIONS: "true", GITHUB_SHA: readFileSync(join(ws, "sha.txt"), "utf8").trim(), GITHUB_REPOSITORY: "Mizore66/faultline",
    GITHUB_WORKFLOW_REF: "Mizore66/faultline/.github/workflows/go.yml@refs/heads/main", GITHUB_REF: "refs/heads/main",
    GITHUB_RUN_ID: "123", GITHUB_RUN_ATTEMPT: "1", GITHUB_EVENT_NAME: "push"
  };
}
const PROVENANCE_FILES = {
  "trust.json": jsonText({
    schemaVersion: "faultline.github-artifact-attestation-trust.v1", repository: "Mizore66/faultline",
    signerWorkflow: "Mizore66/faultline/.github/workflows/go.yml", sourceRef: "refs/heads/main", eventName: "push",
    denySelfHostedRunners: true, trustedRootFile: "root.json"
  }),
  "root.json": jsonText({ mediaType: "application/vnd.dev.sigstore.trustedroot+json;version=0.1" }),
  "attestation.sigstore.json": jsonText({ mediaType: "application/vnd.dev.sigstore.bundle.v0.3+json" })
};
SCENARIOS["provenance-ci"] = { bases: { bundle: "git-unbound" }, files: PROVENANCE_FILES, setup: provenanceRepository, argv: [] };
SCENARIOS["provenance-signed"] = {
  bases: { bundle: "git-unbound" }, files: PROVENANCE_FILES, setup: provenanceRepository,
  argv: [["provenance", "create", "--bundle", "bundle"]], env: provenanceCiEnv
};

// witness-implement: the incident packets `fl witness implement --incident`
// reads (raw readFileSync + JSON.parse).
const IMPLEMENT_INCIDENT = { symptom: "The repository state must not be bad.", ciLog: "state.txt became bad", repositoryLanguage: "Text", repositorySummary: "Temporary repository for the writer replay." };
SCENARIOS["witness-implement"] = {
  files: {
    "incident.json": jsonText(IMPLEMENT_INCIDENT),
    "incident-bad.json": jsonText({ symptom: IMPLEMENT_INCIDENT.symptom, repositoryLanguage: "Text", repositorySummary: IMPLEMENT_INCIDENT.repositorySummary }),
    "incident-extra.json": jsonText({ ...IMPLEMENT_INCIDENT, extra: true }),
    "not-json.json": "this is not json\n"
  },
  argv: []
};

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
