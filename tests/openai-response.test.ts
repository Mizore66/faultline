import { describe, expect, it } from "vitest";
import { extractOpenAiResponseText } from "../src/openai-response.js";

describe("OpenAI Responses text extraction", () => {
  it("reads exactly one output_text from the documented raw REST response shape", () => {
    const text = '{"kind":"structured"}';
    expect(extractOpenAiResponseText({
      object: "response",
      output: [{
        type: "message",
        role: "assistant",
        content: [{ type: "output_text", text, annotations: [] }]
      }]
    })).toBe(text);
  });

  it("accepts an SDK output_text convenience field when no raw output is present", () => {
    expect(extractOpenAiResponseText({ output_text: "{\"kind\":\"sdk\"}" })).toBe('{"kind":"sdk"}');
  });

  it("fails closed for missing, multiple, or conflicting text candidates", () => {
    expect(() => extractOpenAiResponseText({ output: [] })).toThrow(/exactly one output_text/);
    expect(() => extractOpenAiResponseText({
      output: [{ type: "message", content: [{ type: "output_text", text: "one" }, { type: "output_text", text: "two" }] }]
    })).toThrow(/multiple output_text/);
    expect(() => extractOpenAiResponseText({
      output_text: "sdk",
      output: [{ type: "message", content: [{ type: "output_text", text: "raw" }] }]
    })).toThrow(/disagrees/);
  });
});
