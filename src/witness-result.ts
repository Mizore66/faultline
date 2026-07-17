import { z } from "zod";

/**
 * A witness process can print one structured outcome line so FaultLine can
 * distinguish a real predicate result from an incompatible or broken
 * execution environment. Without this, any nonzero exit code (a missing
 * dependency, a TypeScript compile error, a Cargo build failure) looks
 * indistinguishable from the approved predicate actually failing.
 */
export const WITNESS_RESULT_PROTOCOL = "faultline.witness-result.v1" as const;

/** Optional line prefix a witness can print before its JSON payload. */
export const WITNESS_RESULT_MARKER = "FAULTLINE_WITNESS_RESULT=" as const;

export const witnessOutcomeValues = [
  "PREDICATE_PASS",
  "PREDICATE_FAIL",
  "INCOMPATIBLE_STATE",
  "HARNESS_ERROR",
  "INFRASTRUCTURE_ERROR"
] as const;

export const WitnessOutcomeSchema = z.enum(witnessOutcomeValues);
export type WitnessOutcome = z.infer<typeof WitnessOutcomeSchema>;

export const WitnessResultSchema = z.object({
  protocol: z.literal(WITNESS_RESULT_PROTOCOL),
  outcome: WitnessOutcomeSchema,
  detail: z.string().max(4_096).optional()
}).strict();

export type WitnessResult = z.infer<typeof WitnessResultSchema>;

/** Render one structured result line. Tests and fixtures inject this into fake stdout. */
export function formatWitnessResult(outcome: WitnessOutcome, detail?: string): string {
  const payload: WitnessResult = detail === undefined
    ? { protocol: WITNESS_RESULT_PROTOCOL, outcome }
    : { protocol: WITNESS_RESULT_PROTOCOL, outcome, detail };
  return `${WITNESS_RESULT_MARKER}${JSON.stringify(payload)}`;
}

function tryParseLine(line: string): WitnessResult | null {
  const trimmed = line.trim();
  if (!trimmed) return null;
  const candidate = trimmed.startsWith(WITNESS_RESULT_MARKER) ? trimmed.slice(WITNESS_RESULT_MARKER.length).trim() : trimmed;
  if (!candidate.startsWith("{")) return null;
  let parsedJson: unknown;
  try {
    parsedJson = JSON.parse(candidate);
  } catch {
    return null;
  }
  const parsed = WitnessResultSchema.safeParse(parsedJson);
  return parsed.success ? parsed.data : null;
}

/**
 * Find the last line of `stdout` that is either a bare JSON witness-result
 * object or one following the `FAULTLINE_WITNESS_RESULT=` marker. Scanning
 * from the end means an earlier diagnostic line accidentally shaped like
 * JSON never shadows the witness's real, final, structured verdict.
 */
export function parseWitnessResult(stdout: string): WitnessResult | null {
  if (!stdout) return null;
  const lines = stdout.split(/\r?\n/);
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    const line = lines[index];
    if (line === undefined) continue;
    const parsed = tryParseLine(line);
    if (parsed) return parsed;
  }
  return null;
}

export interface WitnessResultClassification {
  readonly verdict: "PASS" | "FAIL" | "ERROR" | "INAPPLICABLE";
  /** Always a SandboxReason-compatible string; see src/sandbox.ts. */
  readonly reason: string;
}

/**
 * Map a structured outcome to a verdict/reason pair. `INFRASTRUCTURE_ERROR`
 * intentionally reuses the sandbox's existing `SANDBOX_UNAVAILABLE` reason
 * rather than minting a parallel infra-error vocabulary.
 */
export function classifyFromWitnessResult(outcome: WitnessOutcome): WitnessResultClassification {
  switch (outcome) {
    case "PREDICATE_PASS":
      return { verdict: "PASS", reason: "PREDICATE_PASS" };
    case "PREDICATE_FAIL":
      return { verdict: "FAIL", reason: "PREDICATE_FAIL" };
    case "INCOMPATIBLE_STATE":
      return { verdict: "INAPPLICABLE", reason: "INCOMPATIBLE_STATE" };
    case "HARNESS_ERROR":
      return { verdict: "ERROR", reason: "HARNESS_ERROR" };
    case "INFRASTRUCTURE_ERROR":
      return { verdict: "ERROR", reason: "SANDBOX_UNAVAILABLE" };
  }
}
