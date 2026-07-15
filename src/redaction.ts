import { createHash } from "node:crypto";

/**
 * FaultLine only records evidence it can safely explain. This module is a
 * best-effort boundary for text and JSON-like payloads, not a promise that
 * every secret format is recognized. Callers should still minimize what they
 * capture and use the allowlisted capture helper for lifecycle events.
 */
export const REDACTION_LIMITATIONS = [
  "LIMITED_COVERAGE: only the documented credential formats and secret-named fields are recognized.",
  "UNKNOWN_PATTERNS: encoded, split, encrypted, binary, transformed, or unrecognized credentials may remain.",
  "DIGEST_LIMITATION: a digest-bearing placeholder supports correlation; it is not encryption or proof that a secret is unrecoverable elsewhere."
] as const;

export const REDACTION_PLACEHOLDER_PREFIX = "<FAULTLINE_REDACTED";

export type RedactionKind =
  | "OPENAI_API_KEY"
  | "GITHUB_TOKEN"
  | "AWS_ACCESS_KEY_ID"
  | "AWS_SECRET_ACCESS_KEY"
  | "AUTHORIZATION_BEARER"
  | "AUTHORIZATION_BASIC"
  | "PRIVATE_KEY_BLOCK"
  | "GENERIC_SECRET_ASSIGNMENT";

export type RedactionConfidence = "HIGH" | "LIMITED";
export type RedactionCoverage = "LIMITED";

export type RedactionOccurrence = {
  kind: RedactionKind;
  confidence: RedactionConfidence;
  /** SHA-256 of the removed byte-for-byte text, never the removed text itself. */
  digest: `sha256:${string}`;
  placeholder: string;
  /** A JSON-style path plus a character range for text values. */
  location: string;
};

export type RedactionReport = {
  redacted: boolean;
  redactedCount: number;
  highConfidenceCount: number;
  limitedConfidenceCount: number;
  /** Always LIMITED: this is deliberately never a full-secret-discovery claim. */
  coverage: RedactionCoverage;
  occurrences: readonly RedactionOccurrence[];
  unknownPatternWarnings: readonly string[];
  limitations: readonly string[];
};

export type RedactionResult<T> = {
  value: T;
  report: RedactionReport;
};

export type AllowlistedEventInput = {
  type: string;
  payload: unknown;
};

/**
 * `allowedPayloadPaths` is intentionally fail-closed: an allowed event with
 * no entry captures an empty payload. Paths are simple own-property paths;
 * array selectors and wildcards are excluded so the capture surface stays
 * reviewable.
 */
export type EventCapturePolicy = {
  allowedEventTypes: readonly string[];
  allowedPayloadPaths?: Readonly<Record<string, readonly string[]>>;
};

export type AcceptedAllowlistedEvent = {
  accepted: true;
  event: {
    type: string;
    payload: Record<string, unknown>;
  };
  capturedPaths: readonly string[];
  omittedPaths: readonly string[];
  report: RedactionReport;
};

export type RejectedAllowlistedEvent = {
  accepted: false;
  eventType: string;
  reason: "EVENT_TYPE_NOT_ALLOWLISTED";
  capturedPaths: readonly string[];
  omittedPaths: readonly string[];
  report: RedactionReport;
};

export type AllowlistedEventCapture = AcceptedAllowlistedEvent | RejectedAllowlistedEvent;

type Candidate = {
  start: number;
  end: number;
  value: string;
  kind: RedactionKind;
  confidence: RedactionConfidence;
  priority: number;
};

type RedactionContext = {
  occurrences: RedactionOccurrence[];
  warnings: Set<string>;
};

const PLACEHOLDER_PATTERN = /^<FAULTLINE_REDACTED:[A-Z_]+:sha256:[a-f0-9]{64}>$/;
const SENSITIVE_FIELD_NAMES = new Set([
  "apikey",
  "xapikey",
  "secret",
  "secretkey",
  "token",
  "accesstoken",
  "refreshtoken",
  "idtoken",
  "password",
  "passwd",
  "pwd",
  "credential",
  "credentials",
  "authorization",
  "proxyauthorization",
  "accesskey",
  "privatekey",
  "clientsecret",
  "sessiontoken"
]);
const SENSITIVE_FIELD_SUFFIXES = [
  "apikey",
  "secret",
  "secretkey",
  "token",
  "password",
  "credential",
  "credentials",
  "privatekey",
  "clientsecret"
] as const;
const FORBIDDEN_PATH_SEGMENTS = new Set(["__proto__", "prototype", "constructor"]);

const PRIVATE_KEY_BLOCK = /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----/g;
const BEARER_TOKEN = /\bBearer[ \t]+([A-Za-z0-9\-._~+/]+=*)/gi;
const BASIC_TOKEN = /\bBasic[ \t]+([A-Za-z0-9+/]{4,}={0,2})/gi;
const OPENAI_KEY = /\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{16,}\b/g;
const GITHUB_TOKEN = /\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b/g;
const AWS_ACCESS_KEY = /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/g;
const AWS_SECRET_ASSIGNMENT = /\b(?:aws[-_])?secret[-_]?access[-_]?key\s*(?:=|:)\s*(?:"((?:\\.|[^"\\\r\n])*)"|'((?:\\.|[^'\\\r\n])*)'|([^\s,;}&]+))/gi;
const GENERIC_SECRET_ASSIGNMENT = /\b(?:api[-_]?key|apikey|secret(?:[-_]?key)?|token|access[-_]?token|refresh[-_]?token|id[-_]?token|password|passwd|pwd|credential(?:s)?|authorization|access[-_]?key|private[-_]?key|client[-_]?secret|session[-_]?token)\b\s*(?:=|:)\s*(?:"((?:\\.|[^"\\\r\n])*)"|'((?:\\.|[^'\\\r\n])*)'|([^\s,;}&]+))/gi;

function digest(value: string): `sha256:${string}` {
  return `sha256:${createHash("sha256").update(value).digest("hex")}`;
}

/** Exported for callers that need to correlate a value before capture. Do not log the input. */
export function secretDigest(value: string): `sha256:${string}` {
  return digest(value);
}

export function redactionPlaceholder(kind: RedactionKind, value: string): string {
  return `${REDACTION_PLACEHOLDER_PREFIX}:${kind}:${digest(value)}>`;
}

function locationForText(base: string, start: number, end: number): string {
  return `${base}[${start}:${end}]`;
}

function pushCandidate(
  candidates: Candidate[],
  start: number,
  value: string,
  kind: RedactionKind,
  confidence: RedactionConfidence,
  priority: number
): void {
  if (!value || PLACEHOLDER_PATTERN.test(value)) return;
  candidates.push({ start, end: start + value.length, value, kind, confidence, priority });
}

function addWholeMatchCandidates(
  candidates: Candidate[],
  text: string,
  pattern: RegExp,
  kind: RedactionKind,
  confidence: RedactionConfidence,
  priority: number
): void {
  pattern.lastIndex = 0;
  for (const match of text.matchAll(pattern)) {
    const value = match[0];
    if (match.index === undefined) continue;
    pushCandidate(candidates, match.index, value, kind, confidence, priority);
  }
}

function addCapturedValueCandidates(
  candidates: Candidate[],
  text: string,
  pattern: RegExp,
  kind: RedactionKind,
  confidence: RedactionConfidence,
  priority: number
): void {
  pattern.lastIndex = 0;
  for (const match of text.matchAll(pattern)) {
    if (match.index === undefined) continue;
    const value = match[1] ?? match[2] ?? match[3];
    if (!value || /^(?:null|undefined|true|false)$/i.test(value)) continue;
    const relativeOffset = match[0].lastIndexOf(value);
    if (relativeOffset < 0) continue;
    pushCandidate(candidates, match.index + relativeOffset, value, kind, confidence, priority);
  }
}

function collectCandidates(text: string): Candidate[] {
  const candidates: Candidate[] = [];
  addWholeMatchCandidates(candidates, text, PRIVATE_KEY_BLOCK, "PRIVATE_KEY_BLOCK", "HIGH", 100);
  addCapturedValueCandidates(candidates, text, BEARER_TOKEN, "AUTHORIZATION_BEARER", "HIGH", 90);
  addCapturedValueCandidates(candidates, text, BASIC_TOKEN, "AUTHORIZATION_BASIC", "HIGH", 90);
  addWholeMatchCandidates(candidates, text, OPENAI_KEY, "OPENAI_API_KEY", "HIGH", 80);
  addWholeMatchCandidates(candidates, text, GITHUB_TOKEN, "GITHUB_TOKEN", "HIGH", 80);
  addWholeMatchCandidates(candidates, text, AWS_ACCESS_KEY, "AWS_ACCESS_KEY_ID", "HIGH", 80);
  addCapturedValueCandidates(candidates, text, AWS_SECRET_ASSIGNMENT, "AWS_SECRET_ACCESS_KEY", "HIGH", 70);
  addCapturedValueCandidates(candidates, text, GENERIC_SECRET_ASSIGNMENT, "GENERIC_SECRET_ASSIGNMENT", "LIMITED", 10);

  // Pick the strongest detector for an overlapping byte range. The tie-break
  // is fully deterministic, so the same payload always yields the same proof.
  return candidates
    .sort((left, right) => left.start - right.start || right.priority - left.priority || right.end - left.end || left.kind.localeCompare(right.kind))
    .reduce<Candidate[]>((accepted, candidate) => {
      const overlaps = accepted.some((existing) => candidate.start < existing.end && candidate.end > existing.start);
      if (!overlaps) accepted.push(candidate);
      return accepted;
    }, []);
}

function recordRedaction(context: RedactionContext, candidate: Candidate, location: string): string {
  const valueDigest = digest(candidate.value);
  const placeholder = `${REDACTION_PLACEHOLDER_PREFIX}:${candidate.kind}:${valueDigest}>`;
  context.occurrences.push({
    kind: candidate.kind,
    confidence: candidate.confidence,
    digest: valueDigest,
    placeholder,
    location
  });
  return placeholder;
}

function redactStringInContext(value: string, location: string, context: RedactionContext): string {
  const candidates = collectCandidates(value);
  if (candidates.length === 0) return value;

  let cursor = 0;
  let output = "";
  for (const candidate of candidates) {
    output += value.slice(cursor, candidate.start);
    output += recordRedaction(context, candidate, locationForText(location, candidate.start, candidate.end));
    cursor = candidate.end;
  }
  return `${output}${value.slice(cursor)}`;
}

function initialContext(): RedactionContext {
  return {
    occurrences: [],
    warnings: new Set([REDACTION_LIMITATIONS[1]])
  };
}

function reportFor(context: RedactionContext): RedactionReport {
  const highConfidenceCount = context.occurrences.filter((occurrence) => occurrence.confidence === "HIGH").length;
  const limitedConfidenceCount = context.occurrences.length - highConfidenceCount;
  return {
    redacted: context.occurrences.length > 0,
    redactedCount: context.occurrences.length,
    highConfidenceCount,
    limitedConfidenceCount,
    coverage: "LIMITED",
    occurrences: context.occurrences,
    unknownPatternWarnings: [...context.warnings].sort(),
    limitations: [...REDACTION_LIMITATIONS]
  };
}

/** Redacts credentials found in one log line, message, or serialized API packet. */
export function redactText(value: string, location = "$"): RedactionResult<string> {
  const context = initialContext();
  return { value: redactStringInContext(value, location, context), report: reportFor(context) };
}

function fieldKind(key: string): RedactionKind | null {
  const normalized = key.toLowerCase().replace(/[-_.]/g, "");
  if (normalized === "awssecretaccesskey" || normalized === "secretaccesskey") return "AWS_SECRET_ACCESS_KEY";
  if (normalized === "awsaccesskeyid" || normalized === "accesskeyid") return "AWS_ACCESS_KEY_ID";
  return SENSITIVE_FIELD_NAMES.has(normalized) || SENSITIVE_FIELD_SUFFIXES.some((suffix) => normalized.endsWith(suffix))
    ? "GENERIC_SECRET_ASSIGNMENT"
    : null;
}

function isPlainRecord(value: object): value is Record<string, unknown> {
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function opaqueUnsupportedValue(value: object): string {
  const name = value.constructor && typeof value.constructor.name === "string" ? value.constructor.name : "object";
  return `<FAULTLINE_UNSUPPORTED_VALUE:${name}>`;
}

function appendFieldRedaction(context: RedactionContext, value: string, kind: RedactionKind, location: string): string {
  const candidate: Candidate = {
    start: 0,
    end: value.length,
    value,
    kind,
    confidence: kind === "GENERIC_SECRET_ASSIGNMENT" ? "LIMITED" : "HIGH",
    priority: 0
  };
  return recordRedaction(context, candidate, location);
}

function redactValueInContext(value: unknown, location: string, context: RedactionContext, active: WeakSet<object>): unknown {
  if (typeof value === "string") return redactStringInContext(value, location, context);
  if (value === null || typeof value === "boolean" || typeof value === "number") return value;
  if (typeof value === "bigint" || typeof value === "symbol" || typeof value === "function" || value === undefined) {
    context.warnings.add("UNSUPPORTED_VALUE: non-JSON values are replaced before event capture.");
    return `<FAULTLINE_UNSUPPORTED_VALUE:${typeof value}>`;
  }
  if (typeof value !== "object") return value;
  if (active.has(value)) {
    context.warnings.add("UNSUPPORTED_VALUE: cyclic values are replaced before event capture.");
    return "<FAULTLINE_UNSUPPORTED_CYCLIC_VALUE>";
  }
  if (!Array.isArray(value) && !isPlainRecord(value)) {
    context.warnings.add("UNSUPPORTED_VALUE: non-plain objects are replaced before event capture.");
    return opaqueUnsupportedValue(value);
  }

  active.add(value);
  try {
    if (Array.isArray(value)) {
      return value.map((entry, index) => redactValueInContext(entry, `${location}[${index}]`, context, active));
    }

    const output: Record<string, unknown> = {};
    // Canonical key traversal makes both the transformed payload and its report
    // deterministic even when objects were constructed in a different order.
    const keys = Object.keys(value).sort((left, right) => left.localeCompare(right));
    for (const key of keys) {
      const descriptor = Object.getOwnPropertyDescriptor(value, key);
      if (!descriptor || !("value" in descriptor)) {
        context.warnings.add(`UNSUPPORTED_VALUE: accessor property omitted at ${location}.${key}.`);
        continue;
      }
      const child = descriptor.value;
      const childLocation = `${location}.${key}`;
      const sensitiveKind = fieldKind(key);
      if (sensitiveKind && typeof child === "string" && !PLACEHOLDER_PATTERN.test(child)) {
        const stringRedacted = redactStringInContext(child, childLocation, context);
        Object.defineProperty(output, key, {
          value: stringRedacted === child ? appendFieldRedaction(context, child, sensitiveKind, childLocation) : stringRedacted,
          enumerable: true,
          writable: true,
          configurable: true
        });
      } else {
        // Defining rather than assigning keeps an attacker-controlled own
        // `__proto__` field as data instead of invoking Object.prototype's
        // legacy prototype setter.
        Object.defineProperty(output, key, {
          value: redactValueInContext(child, childLocation, context, active),
          enumerable: true,
          writable: true,
          configurable: true
        });
      }
    }
    return output;
  } finally {
    active.delete(value);
  }
}

/**
 * Redacts JSON-like event payloads without mutating the caller's value. Every
 * report remains LIMITED because the utility cannot identify arbitrary secrets.
 */
export function redactValue<T>(value: T): RedactionResult<T> {
  const context = initialContext();
  return {
    value: redactValueInContext(value, "$", context, new WeakSet<object>()) as T,
    report: reportFor(context)
  };
}

function emptyReport(additionalWarning?: string): RedactionReport {
  const context = initialContext();
  if (additionalWarning) context.warnings.add(additionalWarning);
  return reportFor(context);
}

function validPath(path: string): boolean {
  return /^[A-Za-z0-9_$-]+(?:\.[A-Za-z0-9_$-]+)*$/.test(path)
    && path.split(".").every((segment) => !FORBIDDEN_PATH_SEGMENTS.has(segment));
}

function readOwnPath(value: unknown, path: string): { found: true; value: unknown } | { found: false } {
  if (!validPath(path) || value === null || typeof value !== "object") return { found: false };
  let current: unknown = value;
  for (const segment of path.split(".")) {
    if (current === null || typeof current !== "object" || Array.isArray(current)) return { found: false };
    const descriptor = Object.getOwnPropertyDescriptor(current, segment);
    if (!descriptor || !("value" in descriptor)) return { found: false };
    current = descriptor.value;
  }
  return { found: true, value: current };
}

function setOwnPath(target: Record<string, unknown>, path: string, value: unknown): void {
  const segments = path.split(".");
  let current = target;
  for (let index = 0; index < segments.length - 1; index += 1) {
    const segment = segments[index];
    if (!segment) return;
    const existing = current[segment];
    if (!existing || typeof existing !== "object" || Array.isArray(existing)) current[segment] = {};
    current = current[segment] as Record<string, unknown>;
  }
  const finalSegment = segments.at(-1);
  if (finalSegment) current[finalSegment] = value;
}

/**
 * Captures only event types and own-property payload paths explicitly approved
 * by policy, then redacts the selected data. Rejected events are never read or
 * returned, so a caller cannot accidentally log a raw unapproved payload.
 */
export function captureAllowlistedEvent(event: AllowlistedEventInput, policy: EventCapturePolicy): AllowlistedEventCapture {
  if (!policy.allowedEventTypes.includes(event.type)) {
    return {
      accepted: false,
      eventType: event.type,
      reason: "EVENT_TYPE_NOT_ALLOWLISTED",
      capturedPaths: [],
      omittedPaths: [],
      report: emptyReport("EVENT_REJECTED: payload was not inspected because its type is not allowlisted.")
    };
  }

  const requestedPaths = [...new Set(policy.allowedPayloadPaths?.[event.type] ?? [])].sort((left, right) => left.localeCompare(right));
  const payload: Record<string, unknown> = {};
  const capturedPaths: string[] = [];
  const omittedPaths: string[] = [];
  for (const path of requestedPaths) {
    const selected = readOwnPath(event.payload, path);
    if (!selected.found) {
      omittedPaths.push(path);
      continue;
    }
    setOwnPath(payload, path, selected.value);
    capturedPaths.push(path);
  }

  const redacted = redactValue(payload);
  return {
    accepted: true,
    event: { type: event.type, payload: redacted.value as Record<string, unknown> },
    capturedPaths,
    omittedPaths,
    report: redacted.report
  };
}
