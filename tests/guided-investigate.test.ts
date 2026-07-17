import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { guidedInvestigateFromCiLog, type WaitForGuidedFreeze } from "../src/guided-investigate.js";
import { approveWitnessProposal, freezeApprovedWitness } from "../src/witness-lock.js";

function git(repository: string, args: string[]): string {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
  return (result.stdout ?? "").trim();
}

function commit(repository: string, state: string, message: string): string {
  writeFileSync(join(repository, "state.txt"), `${state}\n`, "utf8");
  git(repository, ["add", "state.txt"]);
  git(repository, ["commit", "-m", message]);
  return git(repository, ["rev-parse", "HEAD"]);
}

function humanFreezeWaiter(): WaitForGuidedFreeze {
  return async (options) => {
    approveWitnessProposal(options.witnessStore, options.proposalId, {
      approvedBy: "guided-test@example.test"
    });
    const frozen = freezeApprovedWitness(options.witnessStore, options.proposalId);
    return {
      frozenDigest: frozen.frozenDigest,
      reviewUrl: "http://127.0.0.1:0/test-review",
      alreadyFrozen: false
    };
  };
}

describe("guidedInvestigateFromCiLog Option B", () => {
  it("runs intake → freeze pause → unsafe-local localization without dropping draft state", async () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-guided-investigate-"));
    try {
      const repository = join(directory, "source");
      git(directory, ["init", "source"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine guided test"]);
      commit(repository, "good", "known good");
      commit(repository, "bad", "reported failure");

      const ciLog = join(directory, "ci.log");
      writeFileSync(ciLog, "FAIL: expected success\nerror: checkout regression\n", "utf8");
      const store = join(directory, "witnesses");
      const draftStore = join(directory, "incidents");
      const phases: string[] = [];

      const result = await guidedInvestigateFromCiLog({
        repository,
        ciLogPath: ciLog,
        command: "node -e \"process.exit(0)\"",
        incidentId: "guided-option-b",
        witnessStore: store,
        draftStore,
        unsafeLocal: true,
        waitForFreeze: humanFreezeWaiter(),
        onPhase: (phase) => phases.push(phase)
      });

      expect(result.status).toBe("GUIDED_INVESTIGATION_NOT_PROOF");
      expect(result.draft?.draftId).toBe("guided-option-b");
      expect(result.retainedFrozenDigest).toMatch(/^sha256:[a-f0-9]{64}$/);
      expect(result.continuation?.status).toBe("INVESTIGATION_NOT_PROOF");
      expect(result.reviewUrl).toBe("http://127.0.0.1:0/test-review");
      expect(phases).toEqual([
        "PREFLIGHT",
        "INTAKE",
        "WITNESS_REVIEW",
        "RUNTIME_SELECTION",
        "LOCALIZATION"
      ]);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("resumes a frozen incident without reopening review when --expect-digest matches", async () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-guided-resume-"));
    try {
      const repository = join(directory, "source");
      git(directory, ["init", "source"]);
      git(repository, ["config", "user.email", "faultline@example.test"]);
      git(repository, ["config", "user.name", "FaultLine guided test"]);
      commit(repository, "good", "known good");
      commit(repository, "bad", "reported failure");

      const ciLog = join(directory, "ci.log");
      writeFileSync(ciLog, "FAIL: resume path\n", "utf8");
      const store = join(directory, "witnesses");
      const draftStore = join(directory, "incidents");

      const first = await guidedInvestigateFromCiLog({
        repository,
        ciLogPath: ciLog,
        command: "node -e \"process.exit(0)\"",
        incidentId: "guided-resume",
        witnessStore: store,
        draftStore,
        unsafeLocal: true,
        waitForFreeze: humanFreezeWaiter()
      });
      expect(first.status).toBe("GUIDED_INVESTIGATION_NOT_PROOF");
      expect(first.retainedFrozenDigest).toBeTruthy();

      let waitCalled = false;
      const resume = await guidedInvestigateFromCiLog({
        repository,
        resumeId: "guided-resume",
        witnessStore: store,
        draftStore,
        unsafeLocal: true,
        expectDigest: first.retainedFrozenDigest!,
        waitForFreeze: async () => {
          waitCalled = true;
          throw new Error("resume must not reopen the freeze waiter when digest already matches");
        }
      });

      expect(waitCalled).toBe(false);
      expect(resume.status).toBe("GUIDED_INVESTIGATION_NOT_PROOF");
      expect(resume.retainedFrozenDigest).toBe(first.retainedFrozenDigest);
      expect(resume.draft?.draftId).toBe("guided-resume");
      expect(resume.reviewUrl).toBeNull();
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});
