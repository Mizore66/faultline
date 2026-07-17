import { describe, expect, it } from "vitest";
import { sha256 } from "../src/canonical.js";
import {
  IncidentDraftRangeError,
  createIncidentDraft,
  resolveIncidentRange,
  verifyIncidentDraft
} from "../src/incident.js";

const parent = "a".repeat(40);
const head = "b".repeat(40);
const alternateParent = "c".repeat(40);

function draftInput(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    draftId: "ci-failure-001",
    createdAt: "2026-07-16T12:00:00.000Z",
    repository: "C:\\work\\faultline",
    command: "pnpm test -- --runInBand",
    localHead: { head, parents: [parent] },
    ...overrides
  };
}

describe("incident draft intake", () => {
  it("uses an explicit range even when local HEAD facts are ambiguous", () => {
    const draft = createIncidentDraft(draftInput({
      range: { ancestor: "release/known-good", descendant: "HEAD" },
      localHead: { head, parents: [parent, alternateParent] }
    }));

    expect(draft.range).toEqual({
      ancestor: "release/known-good",
      descendant: "HEAD",
      source: "EXPLICIT"
    });
    expect(draft.status).toBe("DRAFT_REQUIRES_HUMAN_REVIEW");
    expect(verifyIncidentDraft(draft)).toEqual({ valid: true, errors: [] });
  });

  it("falls back only to the one locally observed HEAD parent", () => {
    const draft = createIncidentDraft(draftInput({ range: undefined }));

    expect(draft.range).toEqual({ ancestor: parent, descendant: head, source: "LOCAL_HEAD_PARENT" });
    expect(draft.range.source).not.toContain("REMOTE");
  });

  it("refuses root and merge HEAD fallback rather than guessing a base", () => {
    expect(() => createIncidentDraft(draftInput({ range: undefined, localHead: { head, parents: [] } })))
      .toThrow(/root commit/);
    expect(() => createIncidentDraft(draftInput({ range: undefined, localHead: { head, parents: [parent, alternateParent] } })))
      .toThrow(/multiple parents/);

    try {
      resolveIncidentRange({ localHead: { head, parents: [] } });
      throw new Error("expected root HEAD range resolution to throw");
    } catch (error) {
      expect(error).toBeInstanceOf(IncidentDraftRangeError);
      expect((error as IncidentDraftRangeError).code).toBe("ROOT_HEAD");
    }
  });

  it("stores an opaque pasted command byte-for-byte and rejects NUL input", () => {
    const command = "pnpm test -- --filter \"a; $(whoami)\"\r\n";
    const draft = createIncidentDraft(draftInput({ command }));

    expect(draft.command).toBe(command);
    expect(draft.commandDigest).toBe(`sha256:${sha256(Buffer.from(command, "utf8"))}`);
    expect(Object.isFrozen(draft)).toBe(true);
    expect(Object.isFrozen(draft.range)).toBe(true);
    expect(() => createIncidentDraft(draftInput({ command: "pnpm test\0 --unsafe" }))).toThrow(/NUL byte/);
    expect(() => createIncidentDraft(draftInput({ command: " \t\r\n" }))).toThrow(/non-whitespace/);
  });

  it("never auto-freezes: every new draft remains pending human review", () => {
    const draft = createIncidentDraft(draftInput());

    expect(draft.review).toEqual({
      required: true,
      state: "PENDING_HUMAN_REVIEW",
      witnessState: "NOT_PROPOSED",
      freezeState: "NOT_FROZEN",
      autoFreeze: false
    });
    expect("frozenWitnessDigest" in draft).toBe(false);
  });

  it("persists a selected runtime only when it is a tag-free immutable digest", () => {
    const image = `registry.example:5000/faultline-node@sha256:${"d".repeat(64)}`;
    const draft = createIncidentDraft(draftInput({ runtime: { requested: "node", image } }));
    expect(draft.runtime).toEqual({ requested: "node", image });
    expect(() => createIncidentDraft(draftInput({
      runtime: { requested: "node", image: `node:22-alpine@sha256:${"e".repeat(64)}` }
    }))).toThrow(/tag-free/);
  });

  it("can bind a persisted proposal without pretending it was approved or frozen", () => {
    const command = "pnpm test -- checkout";
    const commandDigest = `sha256:${sha256(Buffer.from(command, "utf8"))}`;
    const draft = createIncidentDraft(draftInput({
      command,
      proposal: {
        proposalId: "ci-failure-001",
        proposalDigest: `sha256:${"a".repeat(64)}`,
        incidentPacketDigest: `sha256:${"b".repeat(64)}`,
        commandDigest
      }
    }));
    expect(draft.review).toEqual({
      required: true,
      state: "PENDING_HUMAN_REVIEW",
      witnessState: "PROPOSED",
      freezeState: "NOT_FROZEN",
      autoFreeze: false,
      proposal: {
        proposalId: "ci-failure-001",
        proposalDigest: `sha256:${"a".repeat(64)}`,
        incidentPacketDigest: `sha256:${"b".repeat(64)}`,
        commandDigest
      }
    });
    expect(verifyIncidentDraft(draft)).toEqual({ valid: true, errors: [] });
    expect(() => createIncidentDraft(draftInput({
      command,
      proposal: {
        proposalId: "other-id",
        proposalDigest: `sha256:${"a".repeat(64)}`,
        incidentPacketDigest: `sha256:${"b".repeat(64)}`,
        commandDigest
      }
    }))).toThrow(/same identifier/);
    expect(() => createIncidentDraft(draftInput({
      command,
      proposal: {
        proposalId: "ci-failure-001",
        proposalDigest: `sha256:${"a".repeat(64)}`,
        incidentPacketDigest: `sha256:${"b".repeat(64)}`,
        commandDigest: `sha256:${"c".repeat(64)}`
      }
    }))).toThrow(/exact draft command/);
  });
});
