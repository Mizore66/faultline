// Harness-only controls for the REAL collectGraph / selfCheck of gen-guided-investigate.mjs (not a product
// CLI, never run by the permanent Go replay). Run with the pinned Node:
//   node --test difftest/gen/gen-guided-investigate.selftest.mjs
// Owned synthetic snapshots are built with Node's own SHA-256 and the frozen canonical.js; the accepted
// baseline proves the rejections below are specific and not an always-rejecting generator.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const { collectGraph } = await import(pathToFileURL(join(here, "gen-guided-investigate.mjs")).href);
const { canonicalJson, digestJson } = await import(pathToFileURL(join(here, "..", "..", "dist", "canonical.js")).href);

const slot = "/owned/slot";
const sha = (b) => createHash("sha256").update(b).digest("hex");
const file = (path, text) => ({ path, kind: "file", bytes: Buffer.from(text, "utf8") });

// A valid proof-bundle package under B/ (investigation, manifest, hashes, ROOT) plus an extra hashed artifact.
function packageFiles(mut = {}) {
  const inv = { repository: `${slot}/repo`, runs: [] };
  const invText = `${canonicalJson(inv)}\n`;
  const man = { investigationDigest: digestJson(inv), schemaVersion: "x" };
  const manText = `${canonicalJson(man)}\n`;
  const extra = "artifact\n";
  const lines = [
    [sha(invText), "investigation.json"], [sha(manText), "manifest.json"], [sha(extra), "source/extra.txt"],
  ];
  let hashes = `${lines.map(([h, p]) => `${h}  ${p}`).join("\n")}\n`;
  if (mut.hashes) hashes = mut.hashes(hashes, { invText, manText, extra });
  const root = mut.root ?? `sha256:${sha(hashes)}\n`;
  let files = [
    file("B/investigation.json", mut.invText ?? invText), file("B/manifest.json", mut.manText ?? manText),
    file("B/hashes.txt", hashes), file("B/ROOT.sha256", root), file("B/source/extra.txt", extra),
  ];
  if (mut.drop) files = files.filter((f) => f.path !== mut.drop);
  return files;
}

const graphOf = (...snaps) => collectGraph("selftest", slot, snaps);

test("accepted baseline: a valid package resolves, tokenizes, keeps the exact graph, self-checks", () => {
  const g = graphOf(packageFiles());
  const body = packageFiles();
  const text = (path) => body.find((f) => f.path === path).bytes.toString();
  const tokenize = (t) => g.refs.reduce((x, [lit, tok]) => x.replaceAll(lit, tok), t);
  // hashes.txt: the investigation and manifest lines become file-hash references; the extra artifact stays literal.
  const hashes = tokenize(text("B/hashes.txt")).split("\n");
  assert.equal(hashes[0], "{FILE:json:B/investigation.json}  investigation.json");
  assert.equal(hashes[1], "{FILE:json:B/manifest.json}  manifest.json");
  assert.equal(hashes[2], `${sha("artifact\n")}  source/extra.txt`);
  // ROOT.sha256 is the digest of hashes.txt; the manifest binds the investigation digest.
  assert.equal(tokenize(text("B/ROOT.sha256")), "{DIGEST:text:B/hashes.txt}\n");
  assert.match(tokenize(text("B/manifest.json")), /"investigationDigest":"\{DIGEST:json:B\/investigation\.json\}"/);
  assert.deepEqual(g.refs.map(([, tok]) => tok), [
    "{DIGEST:json:B/investigation.json}", "{DIGEST:json:B/manifest.json}", "{DIGEST:text:B/hashes.txt}",
    "{FILE:json:B/investigation.json}", "{FILE:json:B/manifest.json}", "{FILE:text:B/hashes.txt}",
  ]);
  const kept = g.keep(
    [{ stdout: "", stderr: "", args: [], tools: {} }],
    [{ content: tokenize(text("B/ROOT.sha256")) }],
    [tokenize(text("B/ROOT.sha256"))],
  );
  // ROOT names hashes.txt, which names both JSON files, and the manifest names the investigation digest.
  assert.deepEqual(kept.map((t) => t.id), ["json:B/investigation.json", "json:B/manifest.json", "text:B/hashes.txt"]);
  assert.ok(kept.every((t) => t.preimage.length > 0 && !t.preimage.includes(slot)));
  g.selfCheck(kept);
});

test("accepted baseline: the same package seen in two steps is stable", () => {
  graphOf(packageFiles(), packageFiles());
});

test("stale hashes.txt line is rejected", () => {
  assert.throws(() => graphOf(packageFiles({ hashes: (h) => h.replace(/^[0-9a-f]{64}/, "0".repeat(64)) })), /is stale/);
});

test("hashes.txt entry for an absent file is rejected", () => {
  assert.throws(() => graphOf(packageFiles({ hashes: (h) => `${h}${"1".repeat(64)}  ghost.txt\n` })), /absent from the package/);
});

test("ROOT.sha256 mismatch is rejected", () => {
  assert.throws(() => graphOf(packageFiles({ root: `sha256:${"0".repeat(64)}\n` })), /does not bind hashes\.txt/);
});

test("incomplete package (no ROOT.sha256) is rejected", () => {
  assert.throws(() => graphOf(packageFiles({ drop: "B/ROOT.sha256" })), /incomplete proof bundle package/);
});

test("duplicate hashes.txt entry is rejected", () => {
  assert.throws(() => graphOf(packageFiles({ hashes: (h) => `${h.split("\n")[0]}\n${h}` })), /duplicate hashes\.txt entry/);
});

test("manifest not binding investigation.json is rejected", () => {
  const man = { investigationDigest: `sha256:${"0".repeat(64)}`, schemaVersion: "x" };
  const manText = `${canonicalJson(man)}\n`;
  assert.throws(() => graphOf(packageFiles({
    manText, hashes: (h, o) => h.replace(sha(o.manText), sha(manText)),
  })), /investigationDigest does not bind/);
});

test("non-canonical JSON file is rejected", () => {
  const inv = { repository: `${slot}/repo`, runs: [] };
  const invText = `${JSON.stringify(inv, null, 1)}\n`;
  assert.throws(() => graphOf(packageFiles({ invText, hashes: (h, o) => h.replace(sha(o.invText), sha(invText)) })), /not canonical JSON/);
});

test("a digest-bearing object changed between steps is rejected", () => {
  const a = packageFiles();
  const inv2 = { repository: `${slot}/repo`, runs: [1] };
  const invText2 = `${canonicalJson(inv2)}\n`;
  const man2 = { investigationDigest: digestJson(inv2), schemaVersion: "x" };
  const manText2 = `${canonicalJson(man2)}\n`;
  const b = packageFiles({
    invText: invText2, manText: manText2,
    hashes: (h, o) => h.replace(sha(o.invText), sha(invText2)).replace(sha(o.manText), sha(manText2)),
  });
  assert.throws(() => graphOf(a, b), /changed between steps/);
});

test("selfCheck: missing and cyclic template dependencies are rejected", () => {
  const g = graphOf(packageFiles());
  const good = g.keep([{ stdout: "{DIGEST:text:B/hashes.txt}", stderr: "", args: [], tools: {} }], [], []);
  g.selfCheck(good);
  const missing = good.map((t) => (t.id.startsWith("json:B/manifest") ? { ...t, preimage: `${t.preimage}{DIGEST:json:B/none.json}` } : t));
  assert.throws(() => g.selfCheck(missing), /missing template/);
  const inv = good.find((t) => t.id === "json:B/investigation.json");
  const cyc = good.map((t) => (t === inv ? { ...t, preimage: `${t.preimage}{DIGEST:json:B/manifest.json}` } : t));
  assert.throws(() => g.selfCheck(cyc), /cyclic digest dependency/);
});

test("selfCheck: a swapped dependency (wrong template) does not reproduce its digest", () => {
  const g = graphOf(packageFiles());
  const good = g.keep([{ stdout: "{DIGEST:text:B/hashes.txt}", stderr: "", args: [], tools: {} }], [], []);
  const text = good.find((t) => t.id.startsWith("text:"));
  const swapped = good.map((t) => (t === text ? { ...t, preimage: t.preimage.replace("{FILE:json:B/investigation.json}", "{FILE:json:B/manifest.json}") } : t));
  assert.throws(() => g.selfCheck(swapped), /does not reproduce/);
});

test("draft digest that is not its preimage digest is rejected", () => {
  const unsigned = { schemaVersion: "faultline.incident-draft.v1", repository: `${slot}/repo` };
  const ok = file("R/drafts/d.json", JSON.stringify({ ...unsigned, draftDigest: digestJson(unsigned) }));
  graphOf([ok]);
  const bad = file("R/drafts/d.json", JSON.stringify({ ...unsigned, draftDigest: `sha256:${"0".repeat(64)}` }));
  assert.throws(() => graphOf([bad]), /draftDigest is not the digest/);
});

// ---- Fix1 controls: layout guard and the runner companion ------------------------------------------------
const { volumeOf, assertSameVolume, spawnViaRunner } = await import(pathToFileURL(join(here, "gen-guided-investigate.mjs")).href);
const { mkdtempSync } = await import("node:fs");
const { tmpdir } = await import("node:os");

test("volume guard: same Windows drive in different case is accepted, different drives are rejected", () => {
  assertSameVolume("C:\\gcap\\slot", "c:\\gcap\\scratch", "win32");
  assertSameVolume("C:\\gcap second root\\y\\slot", "C:\\gcap\\scratch", "win32");
  assert.throws(() => assertSameVolume("C:\\gcap\\slot", "D:\\a\\_temp\\scratch", "win32"), /different volumes/);
  assert.throws(() => assertSameVolume("C:\\gcap\\slot", "\\\\server\\share\\scratch", "win32"), /different volumes/);
  assert.equal(volumeOf("c:\\x", "win32"), volumeOf("C:\\y", "win32"));
  assert.throws(() => volumeOf("relative\\path", "win32"), /must be absolute/);
  assertSameVolume("/a/slot", "/b/scratch", "posix");
});

test("runner companion that already exited: no uncaught EPIPE, the real diagnostic is preserved", async () => {
  // node itself is a deterministic failing companion: "--helper" is a bad option, so it exits nonzero at once.
  process.env.GUIDED_STEP_RUNNER = process.execPath;
  process.env.GUIDED_CONSOLE_HELPER = process.execPath;
  const dir = mkdtempSync(join(tmpdir(), "guided-selftest-"));
  const proc = spawnViaRunner({ args: [], cwd: dir, env: { A: "b" }, notify() {}, envFile: join(dir, "env.json") });
  await assert.rejects(proc.exited, (e) => /step runner ended without a result/.test(e.message) && /bad option|--helper/.test(e.message));
  // The runner is gone: asking it to deliver a signal must fail deterministically through an Error, not crash.
  await new Promise((r) => setTimeout(r, 50));
  assert.throws(() => proc.signal("SIGINT"), /not accepting SIGINT/);
});
