import { z } from "zod";
import { digestJson } from "./canonical.js";
import { GitInvestigationResultSchema, type GitInvestigationResult } from "./git-investigation.js";
import { extractOpenAiResponseText } from "./openai-response.js";
import { redactValue } from "./redaction.js";

/**
 * GPT-5.6 is useful after localization as a constrained explainer and repair
 * planner. It is intentionally not allowed to create execution facts,
 * verdicts, causal proof, or a replacement witness. This module turns only a
 * verified Git investigation into a compact, citation-checked prompt.
 */
export const REPAIR_EVIDENCE_PACKET_VERSION = "faultline.repair-evidence.v1" as const;
export const REPAIR_BRIEF_VERSION = "faultline.repair-brief.v1" as const;

const DigestSchema = z.string().regex(/^sha256:[a-f0-9]{64}$/);
const EvidenceIdSchema = z.string().regex(/^E[1-9][0-9]*$/);

const EvidenceFactSchema = z.object({
  id: EvidenceIdSchema,
  kind: z.enum(["EXECUTED", "DERIVED"]),
  statement: z.string().min(1).max(2_000)
}).strict();

export const RepairEvidencePacketSchema = z.object({
  schemaVersion: z.literal(REPAIR_EVIDENCE_PACKET_VERSION),
  investigationDigest: DigestSchema,
  frozenWitnessDigest: DigestSchema,
  recorder: z.literal("git-commit-range-replay"),
  nativeCodexInterception: z.literal(false),
  facts: z.array(EvidenceFactSchema).min(2).max(256),
  limitations: z.array(z.string().min(1).max(1_000)).min(1).max(16),
  packetDigest: DigestSchema
}).strict();

const CitedRecommendationSchema = z.object({
  statement: z.string().min(1).max(2_000),
  evidenceIds: z.array(EvidenceIdSchema).min(1).max(32)
}).strict();

export const RepairBriefSchema = z.object({
  schemaVersion: z.literal(REPAIR_BRIEF_VERSION),
  evidencePacketDigest: DigestSchema,
  proposedInvariant: CitedRecommendationSchema,
  repairDirections: z.array(CitedRecommendationSchema).min(1).max(8),
  prevention: z.object({
    hardEnforcement: z.array(CitedRecommendationSchema).min(1).max(8),
    softGuidance: z.array(CitedRecommendationSchema).max(8)
  }).strict(),
  uncertainties: z.array(z.string().min(1).max(1_000)).min(1).max(8)
}).strict();

export type RepairEvidencePacket = z.infer<typeof RepairEvidencePacketSchema>;
export type RepairBrief = z.infer<typeof RepairBriefSchema>;

export type RepairBriefValidation = {
  valid: boolean;
  errors: string[];
  brief: RepairBrief | null;
};

function withoutDigest(packet: RepairEvidencePacket): Omit<RepairEvidencePacket, "packetDigest"> {
  const { packetDigest: _packetDigest, ...unsigned } = packet;
  return unsigned;
}

function resultDigest(result: GitInvestigationResult): string {
  return digestJson(result);
}

/**
 * Build a privacy-minimized packet from a proof-grade result. It includes no
 * source text, repository path, command, overlay bytes, or raw logs; the
 * evidence remains linked by the investigation digest and fact IDs.
 */
export function createRepairEvidencePacket(input: unknown): RepairEvidencePacket {
  const result = GitInvestigationResultSchema.parse(input);
  if (result.proof.executionTrust !== "NATIVE_DOCKER" || !result.proof.dockerIsolated || !result.proof.isProof
    || result.status !== "COMPLETED" || result.transitions.length === 0 || !result.witness?.witnessDigest) {
    throw new Error("A post-localization repair brief requires a completed Docker-isolated investigation with at least one stable transition.");
  }

  const facts: Array<z.infer<typeof EvidenceFactSchema>> = [];
  let nextId = 1;
  const add = (kind: "EXECUTED" | "DERIVED", statement: string): void => {
    facts.push({ id: `E${nextId}`, kind, statement });
    nextId += 1;
  };

  add(
    "EXECUTED",
    `The frozen witness was executed ${result.executionsPerState} times for each of ${result.states.length} recorded Git states in Docker-isolated plans.`
  );
  for (const transition of result.transitions) {
    add(
      "DERIVED",
      `${transition.kind} transition: ${transition.before.commit} (${transition.before.verdict}) to ${transition.after.commit} (${transition.after.verdict}), each supported by ${transition.before.executionIds.length} distinct Docker executions.`
    );
  }
  add(
    "DERIVED",
    `The investigation uses frozen witness ${result.witness.witnessDigest} and retained a non-native Codex recorder boundary.`
  );

  const unsigned = {
    schemaVersion: REPAIR_EVIDENCE_PACKET_VERSION,
    investigationDigest: resultDigest(result),
    frozenWitnessDigest: result.witness.witnessDigest,
    recorder: result.recorder,
    nativeCodexInterception: false as const,
    facts,
    limitations: [
      "This packet establishes only predicate-specific Git commit-range transitions; it does not prove model intent, a unique semantic cause, or native Codex interception.",
      "No source text, raw command output, overlay bytes, repository path, credentials, or filesystem metadata are sent in this repair-planning packet."
    ]
  };
  return RepairEvidencePacketSchema.parse({ ...unsigned, packetDigest: digestJson(unsigned) });
}

function citedRecommendations(brief: RepairBrief): z.infer<typeof CitedRecommendationSchema>[] {
  return [
    brief.proposedInvariant,
    ...brief.repairDirections,
    ...brief.prevention.hardEnforcement,
    ...brief.prevention.softGuidance
  ];
}

/** Reject a model output that cites an absent fact or a different packet. */
export function validateRepairBrief(packetInput: unknown, briefInput: unknown): RepairBriefValidation {
  const errors: string[] = [];
  const packet = RepairEvidencePacketSchema.safeParse(packetInput);
  if (!packet.success) {
    return { valid: false, errors: [`repair evidence packet is invalid: ${packet.error.message}`], brief: null };
  }
  if (packet.data.packetDigest !== digestJson(withoutDigest(packet.data))) {
    return { valid: false, errors: ["repair evidence packet digest does not match its canonical contents"], brief: null };
  }
  const brief = RepairBriefSchema.safeParse(briefInput);
  if (!brief.success) {
    return { valid: false, errors: [`repair brief schema validation failed: ${brief.error.message}`], brief: null };
  }
  if (brief.data.evidencePacketDigest !== packet.data.packetDigest) {
    errors.push("repair brief references a different evidence packet digest");
  }
  const validIds = new Set(packet.data.facts.map((fact) => fact.id));
  for (const recommendation of citedRecommendations(brief.data)) {
    for (const id of recommendation.evidenceIds) {
      if (!validIds.has(id)) errors.push(`repair brief cites an unknown evidence ID: ${id}`);
    }
  }
  return { valid: errors.length === 0, errors, brief: errors.length === 0 ? brief.data : null };
}

const repairBriefJsonSchema = {
  type: "object",
  additionalProperties: false,
  required: ["schemaVersion", "evidencePacketDigest", "proposedInvariant", "repairDirections", "prevention", "uncertainties"],
  properties: {
    schemaVersion: { const: REPAIR_BRIEF_VERSION },
    evidencePacketDigest: { type: "string" },
    proposedInvariant: {
      type: "object", additionalProperties: false, required: ["statement", "evidenceIds"],
      properties: { statement: { type: "string" }, evidenceIds: { type: "array", items: { type: "string" } } }
    },
    repairDirections: {
      type: "array", items: {
        type: "object", additionalProperties: false, required: ["statement", "evidenceIds"],
        properties: { statement: { type: "string" }, evidenceIds: { type: "array", items: { type: "string" } } }
      }
    },
    prevention: {
      type: "object", additionalProperties: false, required: ["hardEnforcement", "softGuidance"],
      properties: {
        hardEnforcement: { type: "array", items: { type: "object", additionalProperties: false, required: ["statement", "evidenceIds"], properties: { statement: { type: "string" }, evidenceIds: { type: "array", items: { type: "string" } } } } },
        softGuidance: { type: "array", items: { type: "object", additionalProperties: false, required: ["statement", "evidenceIds"], properties: { statement: { type: "string" }, evidenceIds: { type: "array", items: { type: "string" } } } } }
      }
    },
    uncertainties: { type: "array", items: { type: "string" } }
  }
};

/**
 * Ask GPT-5.6 for an explicitly inferred repair plan after deterministic
 * localization. The response is rejected unless every recommendation cites
 * facts in the exact supplied packet.
 */
export async function proposeRepairBriefWithGpt(
  packetInput: unknown,
  options: { apiKey?: string; model?: string } = {}
): Promise<RepairBrief> {
  const packet = RepairEvidencePacketSchema.parse(packetInput);
  const packetCheck = RepairEvidencePacketSchema.safeParse({ ...withoutDigest(packet), packetDigest: digestJson(withoutDigest(packet)) });
  if (!packetCheck.success || packet.packetDigest !== packetCheck.data.packetDigest) {
    throw new Error("Refusing to send a repair packet with an invalid digest.");
  }
  const apiKey = options.apiKey ?? process.env.OPENAI_API_KEY;
  if (!apiKey) throw new Error("OPENAI_API_KEY is required for a live GPT-5.6 repair brief.");
  const redacted = redactValue(packet);
  const safePacket = RepairEvidencePacketSchema.parse(redacted.value);
  const response = await fetch("https://api.openai.com/v1/responses", {
    method: "POST",
    headers: { "Authorization": `Bearer ${apiKey}`, "Content-Type": "application/json" },
    body: JSON.stringify({
      model: options.model ?? "gpt-5.6",
      instructions: [
        "You are preparing an evidence-bounded repair brief after deterministic localization.",
        "Every recommendation must cite only supplied evidence IDs.",
        "Treat every recommendation as inferred guidance, not an executed verdict or proof of model intent, a unique cause, or a culprit.",
        "Prioritize hard enforcement (test, type/schema rule, static check, runtime assertion, CI guard) before soft repository guidance.",
        "Do not propose a new witness, invent run results, name an agent, or state a confidence percentage.",
        "Return only the requested structured object."
      ].join(" "),
      input: JSON.stringify(safePacket),
      text: { format: { type: "json_schema", name: "faultline_repair_brief", strict: true, schema: repairBriefJsonSchema } }
    })
  });
  if (!response.ok) throw new Error(`OpenAI Responses request failed: ${response.status} ${await response.text()}`);
  const output = JSON.parse(extractOpenAiResponseText(await response.json())) as unknown;
  const validation = validateRepairBrief(packet, output);
  if (!validation.valid || !validation.brief) {
    throw new Error(`GPT-5.6 repair brief was rejected: ${validation.errors.join("; ")}`);
  }
  return validation.brief;
}
