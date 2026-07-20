import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { randomUUID } from "node:crypto";
import {
  approveWitnessProposal,
  freezeApprovedWitness,
  proposeWitness,
  type FrozenWitness
} from "./witness-lock.js";
import { formatWitnessResult } from "./witness-result.js";

export type TutorialPhase = {
  readonly phase: string;
  readonly status: "READY" | "DONE";
  readonly detail: string;
  readonly nextCommand?: string;
};

export type TutorialResult = {
  readonly ok: true;
  readonly directory: string;
  readonly repository: string;
  readonly store: string;
  readonly proposalId: string;
  readonly frozenWitness: FrozenWitness;
  readonly goodCommit: string;
  readonly badCommit: string;
  readonly phases: readonly TutorialPhase[];
};

function git(repository: string, args: readonly string[]): string {
  return execFileSync("git", ["-C", repository, ...args], {
    encoding: "utf8",
    windowsHide: true
  }).trim();
}

function commit(repository: string, state: "good" | "bad", message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

/**
 * Interactive-first-incident tutorial on a generated disposable toy repo.
 * Completes without Docker: generates repo + frozen witness + printed next steps.
 */
export function runFirstIncidentTutorial(options: {
  readonly workspace: string;
  readonly yes?: boolean;
}): TutorialResult {
  const workspace = resolve(options.workspace);
  const segment = `tutorial-${randomUUID().slice(0, 8)}`;
  const directory = join(workspace, ".faultline", "tutorials", segment);
  const repository = join(directory, "repo");
  const store = join(directory, "witness-store");
  mkdirSync(repository, { recursive: true });
  mkdirSync(store, { recursive: true });

  git(repository, ["init"]);
  git(repository, ["config", "user.email", "tutorial@faultline.local"]);
  git(repository, ["config", "user.name", "FaultLine Tutorial"]);
  const goodCommit = commit(repository, "good", "tutorial: known good");
  const badCommit = commit(repository, "bad", "tutorial: introduced failure");

  const proposalId = "tutorial-first-incident";
  const passLine = formatWitnessResult("PREDICATE_PASS");
  const failLine = formatWitnessResult("PREDICATE_FAIL");
  const proposal = proposeWitness(store, {
    proposalId,
    proposalOrigin: "HUMAN",
    proposedAt: new Date().toISOString(),
    incidentPacket: {
      symptom: "Toy tutorial: state.txt flipped from good to bad.",
      ciLog: "tutorial fixture — not a production CI log",
      repositoryLanguage: "Text fixture",
      repositorySummary: "FaultLine generated first-incident tutorial repository."
    },
    witness: {
      behavior: "The committed state remains good.",
      command: "node witness.mjs",
      overlays: [{
        path: "witness.mjs",
        bytesBase64: Buffer.from([
          'import { readFileSync } from "node:fs";',
          'const state = readFileSync("state.txt", "utf8").trim();',
          'if (state === "good") {',
          `  console.log(${JSON.stringify(passLine)});`,
          "  process.exit(0);",
          "}",
          'console.error("state witness failed");',
          `console.log(${JSON.stringify(failLine)});`,
          "process.exit(1);",
          ""
        ].join("\n"), "utf8").toString("base64")
      }],
      policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 20 }
    }
  });

  approveWitnessProposal(store, proposal.proposalId, {
    approvedBy: "tutorial@faultline.local",
    approvedAt: new Date().toISOString()
  });
  const frozenWitness = freezeApprovedWitness(store, proposal.proposalId, {
    frozenAt: new Date().toISOString()
  });

  const phases: TutorialPhase[] = [
    {
      phase: "GENERATE_TOY_REPO",
      status: "DONE",
      detail: `Created disposable repo at ${repository} (${goodCommit.slice(0, 7)} → ${badCommit.slice(0, 7)}).`
    },
    {
      phase: "FREEZE_WITNESS",
      status: "DONE",
      detail: `Frozen witness ${frozenWitness.frozenDigest}`
    },
    {
      phase: "NEXT_PROOF_GRADE",
      status: "READY",
      detail: "Proof-grade localization needs a digest-pinned Docker image.",
      nextCommand:
        `fl investigate git --repo ${repository} --from ${goodCommit} --to ${badCommit} --proposal ${proposalId} --expect-digest ${frozenWitness.frozenDigest} --image <digest-pinned-image>`
    },
    {
      phase: "ALTERNATE_DEMO",
      status: "READY",
      detail: "Or run the one-shot Docker demo arc when Docker is available.",
      nextCommand: "fl demo full --export-only"
    }
  ];

  writeFileSync(
    join(directory, "tutorial-result.json"),
    `${JSON.stringify({
      schemaVersion: "faultline.tutorial.v1",
      directory,
      repository,
      store,
      proposalId,
      frozenDigest: frozenWitness.frozenDigest,
      goodCommit,
      badCommit,
      phases
    }, null, 2)}\n`,
    "utf8"
  );

  return {
    ok: true,
    directory,
    repository,
    store,
    proposalId,
    frozenWitness,
    goodCommit,
    badCommit,
    phases
  };
}
