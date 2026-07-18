import { spawnSync } from "node:child_process";
import { z } from "zod";
import {
  StableTurnTransitionSchema,
  type StableTurnTransition,
  type TurnInvestigationResult
} from "./turn-investigation.js";

const GitObjectIdSchema = z.string().regex(/^[a-f0-9]{40}$/);

export type SyntheticTurnCommits = {
  readonly beforeCommit: string;
  readonly afterCommit: string;
  readonly beforeTree: string;
  readonly afterTree: string;
  readonly transitionIndex: number;
  readonly transition: StableTurnTransition;
  readonly note: string;
};

function runGit(repository: string, args: readonly string[]): string {
  const result = spawnSync("git", ["-C", repository, ...args], {
    encoding: "utf8",
    windowsHide: true,
    maxBuffer: 16 * 1024 * 1024
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(result.stderr.trim() || `git ${args.join(" ")} failed`);
  }
  return (result.stdout ?? "").trim();
}

/**
 * Select a stable turn transition for counterfactual minimization.
 * Default index 0 is the earliest recorded transition in the investigation.
 */
export function selectTurnTransition(
  result: Pick<TurnInvestigationResult, "transitions">,
  transitionIndex = 0
): StableTurnTransition {
  if (!Number.isInteger(transitionIndex) || transitionIndex < 0) {
    throw new Error(`Transition index must be a non-negative integer (got ${transitionIndex}).`);
  }
  const transition = result.transitions[transitionIndex];
  if (!transition) {
    throw new Error(
      `Turn investigation has no transition at index ${transitionIndex} (${result.transitions.length} available).`
    );
  }
  return StableTurnTransitionSchema.parse(transition);
}

function commitTree(repository: string, treeDigest: string, message: string): string {
  const tree = GitObjectIdSchema.parse(
    runGit(repository, ["rev-parse", "--verify", "--end-of-options", `${treeDigest}^{tree}`])
  );
  const commit = GitObjectIdSchema.parse(
    runGit(repository, [
      "-c",
      "user.name=FaultLine",
      "-c",
      "user.email=faultline@example.invalid",
      "commit-tree",
      tree,
      "-m",
      message
    ])
  );
  return commit;
}

/**
 * Bridge turn-tree digests into orphan commits so existing `minimizeGitDiff`
 * (commit-shaped) can run counterfactual edit isolation on a turn boundary.
 *
 * These commits are synthetic localization aids — not the original Codex history.
 */
export function syntheticCommitsForTurnTransition(
  repository: string,
  transition: StableTurnTransition,
  transitionIndex: number
): SyntheticTurnCommits {
  if (transition.kind !== "PASS_TO_FAIL") {
    throw new Error(
      `Counterfactual minimization from a turn package currently requires PASS→FAIL (got ${transition.kind}).`
    );
  }
  if (transition.before.verdict !== "PASS" || transition.after.verdict !== "FAIL") {
    throw new Error("Turn transition endpoints are not a stable PASS→FAIL pair.");
  }

  const beforeTree = GitObjectIdSchema.parse(
    runGit(repository, ["rev-parse", "--verify", "--end-of-options", `${transition.before.treeDigest}^{tree}`])
  );
  const afterTree = GitObjectIdSchema.parse(
    runGit(repository, ["rev-parse", "--verify", "--end-of-options", `${transition.after.treeDigest}^{tree}`])
  );

  const beforeCommit = commitTree(
    repository,
    beforeTree,
    `faultline-turn-min before t${transition.before.turnOrdinal} (${transition.before.treeDigest.slice(0, 12)})`
  );
  const afterCommit = commitTree(
    repository,
    afterTree,
    `faultline-turn-min after t${transition.after.turnOrdinal} (${transition.after.treeDigest.slice(0, 12)})`
  );

  return {
    beforeCommit,
    afterCommit,
    beforeTree,
    afterTree,
    transitionIndex,
    transition,
    note:
      "Synthetic orphan commits were created from turn tree digests so Git-path counterfactual minimization can run. They are not Codex history and do not promote EXPERIMENTAL_TURN to TURN_PROOF / COMMIT_PROOF."
  };
}

export function bridgeTurnTransitionToMinimizationCommits(
  repository: string,
  result: Pick<TurnInvestigationResult, "transitions">,
  transitionIndex = 0
): SyntheticTurnCommits {
  const transition = selectTurnTransition(result, transitionIndex);
  return syntheticCommitsForTurnTransition(repository, transition, transitionIndex);
}
