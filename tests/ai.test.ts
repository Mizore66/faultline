import { afterEach, describe, expect, it, vi } from "vitest";
import { proposeWitnessWithGpt } from "../src/ai.js";
import { digestJson } from "../src/canonical.js";
import { proposeRepairBriefWithGpt } from "../src/repair-brief.js";

function officialResponse(text: string): Response {
  return new Response(JSON.stringify({
    object: "response",
    output: [{
      type: "message",
      role: "assistant",
      content: [{ type: "output_text", text, annotations: [] }]
    }]
  }), { status: 200, headers: { "Content-Type": "application/json" } });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("live OpenAI proposal boundaries", () => {
  it("accepts a witness proposal from the official raw Responses API output shape", async () => {
    const proposal = {
      behavior: "A completed refund preserves settlement currency.",
      command: "node witness.mjs",
      overlayFiles: ["witness.mjs"],
      expectedExitCode: 0,
      timeoutSeconds: 30,
      safetyNotes: ["Network disabled"]
    };
    const fetchMock = vi.fn(async () => officialResponse(JSON.stringify(proposal)));
    vi.stubGlobal("fetch", fetchMock);

    await expect(proposeWitnessWithGpt({
      symptom: "Refund currency changed.",
      ciLog: "Expected USD; received EUR.",
      repositoryLanguage: "TypeScript",
      repositorySummary: "A service that completes refunds."
    }, { apiKey: "test-key", model: "gpt-test" })).resolves.toMatchObject({ proposal });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("accepts a cited repair brief from the official raw Responses API output shape", async () => {
    const unsignedPacket = {
      schemaVersion: "faultline.repair-evidence.v2" as const,
      investigationDigest: `sha256:${"a".repeat(64)}`,
      frozenWitnessDigest: `sha256:${"b".repeat(64)}`,
      recorder: "git-commit-range-replay" as const,
      nativeCodexInterception: false as const,
      facts: [
        { id: "E1", kind: "EXECUTED" as const, statement: "The frozen witness ran in a Docker-isolated plan." },
        { id: "E2", kind: "DERIVED" as const, statement: "The recorded range contains a stable pass-to-fail transition." }
      ],
      limitations: ["The evidence does not establish model intent."]
    };
    const packet = { ...unsignedPacket, packetDigest: digestJson(unsignedPacket) };
    const brief = {
      schemaVersion: "faultline.repair-brief.v1",
      evidencePacketDigest: packet.packetDigest,
      proposedInvariant: { statement: "Preserve the frozen witness behavior.", evidenceIds: ["E1"] },
      repairDirections: [{ statement: "Inspect the stable transition before changing code.", evidenceIds: ["E2"] }],
      prevention: { hardEnforcement: [{ statement: "Keep the regression guard in CI.", evidenceIds: ["E1"] }], softGuidance: [] },
      uncertainties: ["The evidence does not identify a unique semantic cause."]
    };
    vi.stubGlobal("fetch", vi.fn(async () => officialResponse(JSON.stringify(brief))));

    await expect(proposeRepairBriefWithGpt(packet, { apiKey: "test-key", model: "gpt-test" })).resolves.toEqual(brief);
  });
});
