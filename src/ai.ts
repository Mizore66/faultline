import { z } from "zod";
import { digestJson } from "./canonical.js";
import { redactValue, type RedactionReport } from "./redaction.js";

const BlindIncidentPacketSchema = z.object({
  symptom: z.string().min(1),
  ciLog: z.string().min(1),
  repositoryLanguage: z.string().min(1),
  repositorySummary: z.string().min(1)
}).strict();

const WitnessProposalSchema = z.object({
  behavior: z.string().min(1),
  command: z.string().min(1),
  overlayFiles: z.array(z.string()).max(5),
  expectedExitCode: z.number().int().min(0).max(255),
  timeoutSeconds: z.number().int().min(1).max(120),
  safetyNotes: z.array(z.string()).max(8)
}).strict();

export type BlindIncidentPacket = z.infer<typeof BlindIncidentPacketSchema>;
export type WitnessProposal = z.infer<typeof WitnessProposalSchema>;

const witnessProposalJsonSchema = {
  type: "object",
  additionalProperties: false,
  required: ["behavior", "command", "overlayFiles", "expectedExitCode", "timeoutSeconds", "safetyNotes"],
  properties: {
    behavior: { type: "string" },
    command: { type: "string" },
    overlayFiles: { type: "array", items: { type: "string" }, maxItems: 5 },
    expectedExitCode: { type: "integer", minimum: 0, maximum: 255 },
    timeoutSeconds: { type: "integer", minimum: 1, maximum: 120 },
    safetyNotes: { type: "array", items: { type: "string" }, maxItems: 8 }
  }
};

function responseText(response: unknown): string {
  if (typeof response === "object" && response !== null && "output_text" in response && typeof response.output_text === "string") {
    return response.output_text;
  }
  throw new Error("OpenAI response did not contain output_text");
}

export function makeBlindIncidentPacket(input: unknown): { packet: BlindIncidentPacket; digest: string } {
  const packet = BlindIncidentPacketSchema.parse(input);
  return { packet, digest: digestJson(packet) };
}

export function defaultWitnessProposal(): WitnessProposal {
  return {
    behavior: "Completed refunds preserve settlement currency.",
    command: "node witness.mjs",
    overlayFiles: ["witness.mjs"],
    expectedExitCode: 0,
    timeoutSeconds: 30,
    safetyNotes: ["Network disabled", "Review the exact overlay before approval", "Do not reveal candidate states to the proposal model"]
  };
}

export async function proposeWitnessWithGpt(input: unknown, options: { apiKey?: string; model?: string } = {}): Promise<{ proposal: WitnessProposal; incidentPacketDigest: string; redaction: RedactionReport }> {
  const { packet } = makeBlindIncidentPacket(input);
  const redacted = redactValue(packet);
  const redactedPacket = BlindIncidentPacketSchema.parse(redacted.value);
  const apiKey = options.apiKey ?? process.env.OPENAI_API_KEY;
  if (!apiKey) {
    throw new Error("OPENAI_API_KEY is required for a live GPT-5.6 witness proposal. The judge demo does not require one.");
  }
  const response = await fetch("https://api.openai.com/v1/responses", {
    method: "POST",
    headers: {
      "Authorization": `Bearer ${apiKey}`,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      model: options.model ?? "gpt-5.6",
      instructions: [
        "Propose an executable witness from the blinded incident packet.",
        "You have not been given candidate sessions, turns, diffs, timeline data, or localization results.",
        "Do not assign verdicts, name a culprit, or make causal claims.",
        "Return only the required structured proposal."
      ].join(" "),
      input: JSON.stringify(redactedPacket),
      text: {
        format: {
          type: "json_schema",
          name: "faultline_witness_proposal",
          strict: true,
          schema: witnessProposalJsonSchema
        }
      }
    })
  });
  if (!response.ok) {
    throw new Error(`OpenAI Responses request failed: ${response.status} ${await response.text()}`);
  }
  const proposal = WitnessProposalSchema.parse(JSON.parse(responseText(await response.json())));
  return { proposal, incidentPacketDigest: digestJson(redactedPacket), redaction: redacted.report };
}
