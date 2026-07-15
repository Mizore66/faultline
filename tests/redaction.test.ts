import { describe, expect, it } from "vitest";
import {
  captureAllowlistedEvent,
  redactText,
  redactValue,
  secretDigest,
  type RedactionKind
} from "../src/redaction.js";

const openAiKey = `sk-proj-${"a".repeat(48)}`;
const githubToken = `ghp_${"B".repeat(36)}`;
const awsAccessKey = `AKIA${"C".repeat(16)}`;
const awsSecret = `${"d".repeat(38)}/+=`;
const bearerToken = "bearer-token-which-is-not-a-real-credential";
const basicToken = "dXNlcjpwYXNzd29yZA==";
const privateKey = [
  "-----BEGIN PRIVATE KEY-----",
  "private-material-that-must-not-survive",
  "-----END PRIVATE KEY-----"
].join("\n");

function expectNotToContainAny(value: string, secrets: readonly string[]): void {
  for (const secret of secrets) expect(value).not.toContain(secret);
}

describe("FaultLine evidence redaction", () => {
  it("redacts the documented credential formats with digest-bearing placeholders", () => {
    const source = [
      `openai=${openAiKey}`,
      `github=${githubToken}`,
      `aws_access_key_id=${awsAccessKey}`,
      `AWS_SECRET_ACCESS_KEY=${awsSecret}`,
      `Authorization: Bearer ${bearerToken}`,
      `Proxy-Authorization: Basic ${basicToken}`,
      privateKey,
      "password=correct-horse-battery-staple"
    ].join("\n");

    const redacted = redactText(source, "$.packet");
    expectNotToContainAny(redacted.value, [
      openAiKey,
      githubToken,
      awsAccessKey,
      awsSecret,
      bearerToken,
      basicToken,
      privateKey,
      "correct-horse-battery-staple"
    ]);
    expect(redacted.value).toContain(`<FAULTLINE_REDACTED:OPENAI_API_KEY:${secretDigest(openAiKey)}>`);
    expect(redacted.value).toContain(`<FAULTLINE_REDACTED:PRIVATE_KEY_BLOCK:${secretDigest(privateKey)}>`);
    expect(redacted.report).toMatchObject({ redacted: true, coverage: "LIMITED" });
    expect(redacted.report.unknownPatternWarnings.join(" ")).toMatch(/UNKNOWN_PATTERNS/);

    const kinds = new Set(redacted.report.occurrences.map((occurrence) => occurrence.kind));
    const expected: RedactionKind[] = [
      "OPENAI_API_KEY",
      "GITHUB_TOKEN",
      "AWS_ACCESS_KEY_ID",
      "AWS_SECRET_ACCESS_KEY",
      "AUTHORIZATION_BEARER",
      "AUTHORIZATION_BASIC",
      "PRIVATE_KEY_BLOCK",
      "GENERIC_SECRET_ASSIGNMENT"
    ];
    for (const kind of expected) expect(kinds).toContain(kind);
    for (const occurrence of redacted.report.occurrences) {
      expect(occurrence.digest).toMatch(/^sha256:[a-f0-9]{64}$/);
      expect(occurrence.placeholder).toContain(occurrence.digest);
      expect(occurrence.location).toMatch(/^\$\.packet\[/);
    }
  });

  it("redacts JSON-like values without mutating the payload and handles secret-named fields", () => {
    const payload = {
      password: "plain-but-sensitive",
      request: {
        headers: { authorization: `Bearer ${bearerToken}` },
        apiKey: "short-key-that-needs-the-field-name-boundary",
        harmless: "still visible"
      },
      nested: [{ github_token: githubToken }]
    };

    const result = redactValue(payload);
    const encoded = JSON.stringify(result.value);
    expectNotToContainAny(encoded, ["plain-but-sensitive", bearerToken, "short-key-that-needs-the-field-name-boundary", githubToken]);
    expect(result.value.request.harmless).toBe("still visible");
    expect(payload.password).toBe("plain-but-sensitive");
    expect(payload.request.apiKey).toBe("short-key-that-needs-the-field-name-boundary");
    expect(result.report.redactedCount).toBeGreaterThanOrEqual(4);
    expect(result.report.occurrences.map((occurrence) => occurrence.location)).toContain("$.password");
  });

  it("captures only explicitly allowlisted event paths before redacting them", () => {
    const captured = captureAllowlistedEvent(
      {
        type: "HTTP_REQUEST",
        payload: {
          method: "POST",
          headers: { authorization: `Bearer ${bearerToken}`, trace: "trace-1" },
          body: { apiKey: openAiKey, customerEmail: "person@example.com" },
          rawPrompt: "must never be copied"
        }
      },
      {
        allowedEventTypes: ["HTTP_REQUEST"],
        allowedPayloadPaths: {
          HTTP_REQUEST: ["method", "headers.authorization", "body.apiKey", "missing.path"]
        }
      }
    );

    if (!captured.accepted) throw new Error("expected allowed event to be captured");
    expect(captured.capturedPaths).toEqual(["body.apiKey", "headers.authorization", "method"]);
    expect(captured.omittedPaths).toEqual(["missing.path"]);
    const encoded = JSON.stringify(captured.event.payload);
    expectNotToContainAny(encoded, [bearerToken, openAiKey, "person@example.com", "must never be copied"]);
    expect(captured.event.payload).toEqual({
      body: { apiKey: expect.stringMatching(/^<FAULTLINE_REDACTED:OPENAI_API_KEY:/) },
      headers: { authorization: expect.stringContaining("<FAULTLINE_REDACTED:AUTHORIZATION_BEARER:") },
      method: "POST"
    });

    const rejected = captureAllowlistedEvent(
      { type: "RAW_PROMPT", payload: { prompt: `secret ${openAiKey}` } },
      { allowedEventTypes: ["HTTP_REQUEST"] }
    );
    expect(rejected).toMatchObject({ accepted: false, reason: "EVENT_TYPE_NOT_ALLOWLISTED" });
    if (rejected.accepted) throw new Error("expected unapproved event to be rejected");
    expect(rejected.report.unknownPatternWarnings.join(" ")).toMatch(/payload was not inspected/);
  });

  it("fails closed for unsafe allowlist paths and does not inspect rejected payload getters", () => {
    const rejectedPayload = {};
    Object.defineProperty(rejectedPayload, "prompt", {
      enumerable: true,
      get() {
        throw new Error("a rejected payload must never be read");
      }
    });
    expect(() => captureAllowlistedEvent(
      { type: "UNAPPROVED", payload: rejectedPayload },
      { allowedEventTypes: ["APPROVED"] }
    )).not.toThrow();

    const captured = captureAllowlistedEvent(
      { type: "APPROVED", payload: { safe: "yes", nested: { value: "no" } } },
      {
        allowedEventTypes: ["APPROVED"],
        allowedPayloadPaths: { APPROVED: ["safe", "__proto__.polluted", "nested.constructor"] }
      }
    );
    if (!captured.accepted) throw new Error("expected event to be captured");
    expect(captured.event.payload).toEqual({ safe: "yes" });
    expect(captured.omittedPaths).toEqual(["__proto__.polluted", "nested.constructor"]);
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();

    const protoNamedPayload = JSON.parse('{"__proto__":{"password":"do-not-leak"}}') as Record<string, unknown>;
    const direct = redactValue(protoNamedPayload);
    expect(JSON.stringify(direct.value)).not.toContain("do-not-leak");
    expect(Object.getPrototypeOf(direct.value)).toBe(Object.prototype);
  });

  it("is deterministic for repeated input and preserves a stable correlation digest", () => {
    const source = `token=${githubToken}; token=${githubToken}`;
    const first = redactText(source);
    const second = redactText(source);
    expect(first).toEqual(second);
    expect(first.report.occurrences).toHaveLength(2);
    expect(first.report.occurrences[0]?.digest).toBe(secretDigest(githubToken));
    expect(first.report.occurrences[1]?.digest).toBe(secretDigest(githubToken));
  });
});
