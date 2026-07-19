import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { z } from "zod";
import { RepairBriefSchema, RepairEvidencePacketSchema, validateRepairBrief } from "../src/repair-brief.js";

const sampleDirectory = join(process.cwd(), "docs", "samples", "gpt-5.6");

const WitnessProposalSampleSchema = z.object({
  schemaVersion: z.literal("faultline.gpt-witness-proposal-sample.v1"),
  label: z.literal("REDACTED_SAMPLE"),
  model: z.string().min(1),
  note: z.string().min(1),
  proposal: z.object({
    behavior: z.string().min(1),
    command: z.string().min(1),
    overlayFiles: z.array(z.string().min(1)).min(1),
    expectedExitCode: z.number().int(),
    timeoutSeconds: z.number().int().positive(),
    safetyNotes: z.array(z.string().min(1)).min(1)
  }).strict()
}).strict();

describe("committed GPT-5.6 redacted samples", () => {
  it("validates the witness proposal sample shape", () => {
    const sample = JSON.parse(readFileSync(join(sampleDirectory, "witness-proposal.sample.json"), "utf8"));
    expect(WitnessProposalSampleSchema.parse(sample).proposal.overlayFiles).toEqual([
      ".faultline-witness/check-settlement-currency.mjs"
    ]);
  });

  it("validates the repair brief against its evidence packet", () => {
    const packet = RepairEvidencePacketSchema.parse(
      JSON.parse(readFileSync(join(sampleDirectory, "repair-evidence.sample.json"), "utf8"))
    );
    const brief = RepairBriefSchema.parse(
      JSON.parse(readFileSync(join(sampleDirectory, "repair-brief.sample.json"), "utf8"))
    );
    const validation = validateRepairBrief(packet, brief);
    expect(validation.valid).toBe(true);
    expect(validation.errors).toEqual([]);
    expect(validation.brief?.evidencePacketDigest).toBe(packet.packetDigest);
  });
});
