/**
 * Extract exactly one text payload from a completed Responses API response.
 *
 * The official REST response carries text in `output[].content[].text`. Some
 * OpenAI SDKs additionally expose a convenient `output_text` aggregation. We
 * accept that fast path only when it does not conflict with the raw response,
 * and deliberately reject multiple candidates: a structured-output caller
 * must not silently concatenate or choose between model messages.
 */

type JsonRecord = Record<string, unknown>;

function isRecord(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function own(value: JsonRecord, key: string): unknown {
  return Object.prototype.hasOwnProperty.call(value, key) ? value[key] : undefined;
}

function nonEmptyText(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function rawOutputTexts(response: JsonRecord): string[] {
  const output = own(response, "output");
  if (output === undefined) return [];
  if (!Array.isArray(output)) throw new Error("OpenAI response output must be an array when present");

  const texts: string[] = [];
  for (const item of output) {
    if (!isRecord(item)) throw new Error("OpenAI response output contains a non-object item");
    const content = own(item, "content");
    if (content === undefined) continue;
    if (!Array.isArray(content)) throw new Error("OpenAI response message content must be an array when present");
    for (const part of content) {
      if (!isRecord(part)) throw new Error("OpenAI response content contains a non-object item");
      if (own(part, "type") !== "output_text") continue;
      const text = nonEmptyText(own(part, "text"));
      if (text === null) throw new Error("OpenAI response output_text content is missing non-empty text");
      texts.push(text);
    }
  }
  return texts;
}

/**
 * Return the one structured-output JSON string in a Responses API response.
 * Multiple messages, no text, an invalid fast-path value, or a disagreement
 * between SDK aggregation and raw response are all fail-closed errors.
 */
export function extractOpenAiResponseText(response: unknown): string {
  if (!isRecord(response)) throw new Error("OpenAI response must be an object");

  const raw = rawOutputTexts(response);
  if (raw.length > 1) throw new Error("OpenAI response contains multiple output_text values; refusing an ambiguous structured result");

  const hasFastPath = Object.prototype.hasOwnProperty.call(response, "output_text");
  const fastPath = hasFastPath ? nonEmptyText(own(response, "output_text")) : null;
  if (hasFastPath && fastPath === null) throw new Error("OpenAI response output_text fast path is missing non-empty text");

  if (raw.length === 1) {
    const text = raw[0]!;
    if (fastPath !== null && fastPath !== text) {
      throw new Error("OpenAI response output_text fast path disagrees with raw output content");
    }
    return text;
  }
  if (fastPath !== null) return fastPath;
  throw new Error("OpenAI response did not contain exactly one output_text value");
}
