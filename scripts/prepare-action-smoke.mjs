import { appendFileSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness
} from "../dist/witness-lock.js";

const workspace = resolve(process.env.GITHUB_WORKSPACE ?? process.cwd());
const outputFile = process.env.GITHUB_OUTPUT;
if (!outputFile) throw new Error("GITHUB_OUTPUT is required.");

const root = join(workspace, ".action-smoke");
const repository = join(root, "repository");
const store = join(root, "witness-store");
const proposalId = "action-smoke";

function git(args) {
  const result = spawnSync("git", args, {
    cwd: repository,
    encoding: "utf8",
    env: {
      ...process.env,
      GIT_AUTHOR_DATE: "2026-01-01T00:00:00Z",
      GIT_COMMITTER_DATE: "2026-01-01T00:00:00Z"
    }
  });
  if (result.status !== 0) throw new Error(`git ${args.join(" ")} failed\n${result.stderr ?? ""}`);
  return (result.stdout ?? "").trim();
}

function relativeOutput(path) {
  return relative(workspace, path).replaceAll("\\", "/");
}

rmSync(root, { recursive: true, force: true });
mkdirSync(repository, { recursive: true });
git(["init"]);
git(["config", "user.name", "FaultLine Action Smoke"]);
git(["config", "user.email", "faultline@example.invalid"]);

writeFileSync(join(repository, "value.txt"), "good\n", "utf8");
git(["add", "value.txt"]);
git(["commit", "-m", "known good"]);
const base = git(["rev-parse", "HEAD"]);

writeFileSync(join(repository, "value.txt"), "bad\n", "utf8");
git(["add", "value.txt"]);
git(["commit", "-m", "known bad"]);
const head = git(["rev-parse", "HEAD"]);

const witnessScript = [
  'import { readFileSync } from "node:fs";',
  'const value = readFileSync("value.txt", "utf8").trim();',
  'process.exit(value === "good" ? 0 : 1);',
  ""
].join("\n");
proposeWitness(store, {
  proposalId,
  proposalOrigin: "HUMAN",
  proposedAt: "2026-01-01T00:01:00.000Z",
  incidentPacket: {
    symptom: "A known-good value changed to a known-bad value.",
    ciLog: "Expected good; received bad.",
    repositoryLanguage: "JavaScript",
    repositorySummary: "Deterministic repository generated to verify the reusable FaultLine Action."
  },
  witness: {
    behavior: "value.txt contains the word good.",
    command: "node witness.mjs",
    overlays: [{
      path: "witness.mjs",
      bytesBase64: Buffer.from(witnessScript, "utf8").toString("base64")
    }],
    policy: {
      network: "disabled",
      credentials: "redacted",
      timeoutSeconds: 30
    }
  }
});
approveWitnessProposal(store, proposalId, {
  approvedBy: "FaultLine Action smoke workflow",
  approvedAt: "2026-01-01T00:02:00.000Z",
  note: "Deterministic CI-only fixture."
});
freezeApprovedWitness(store, proposalId, { frozenAt: "2026-01-01T00:03:00.000Z" });

appendFileSync(outputFile, [
  `repository=${relativeOutput(repository)}`,
  `witness=${relativeOutput(join(store, "frozen", `${proposalId}.json`))}`,
  `base=${base}`,
  `head=${head}`,
  ""
].join("\n"), "utf8");
