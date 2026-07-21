import { z } from "zod";

/**
 * A witness process can print one structured outcome line so FaultLine can
 * distinguish a real predicate result from an incompatible or broken
 * execution environment. Without this, any nonzero exit code (a missing
 * dependency, a TypeScript compile error, a Cargo build failure) looks
 * indistinguishable from the approved predicate actually failing.
 *
 * Proof-bearing PREDICATE_PASS / PREDICATE_FAIL lines on ordinary stdout are
 * only accepted when they are consistent with the process exit code and do
 * not conflict with other structured records. Repository code that prints a
 * forged PASS after a failing run is rejected.
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

function outcomeKey(result: WitnessResult): string {
  return result.detail === undefined ? result.outcome : `${result.outcome}\0${result.detail}`;
}

/**
 * Collect every schema-valid witness-result line from stdout (marker or bare JSON).
 */
export function parseAllWitnessResults(stdout: string): WitnessResult[] {
  if (!stdout) return [];
  const found: WitnessResult[] = [];
  for (const line of stdout.split(/\r?\n/)) {
    const parsed = tryParseLine(line);
    if (parsed) found.push(parsed);
  }
  return found;
}

export type ParsedWitnessStdout =
  | { readonly ok: true; readonly result: WitnessResult | null }
  | { readonly ok: false; readonly reason: "CONFLICTING_WITNESS_RESULTS" };

/**
 * Resolve at most one structured witness result from stdout. Multiple records
 * are allowed only when they agree on outcome (and detail); otherwise fail closed.
 */
export function resolveWitnessResultFromStdout(stdout: string): ParsedWitnessStdout {
  const results = parseAllWitnessResults(stdout);
  if (results.length === 0) return { ok: true, result: null };
  const first = results[0]!;
  const key = outcomeKey(first);
  for (let index = 1; index < results.length; index += 1) {
    const next = results[index]!;
    if (outcomeKey(next) !== key) {
      return { ok: false, reason: "CONFLICTING_WITNESS_RESULTS" };
    }
  }
  // Prefer the last copy when duplicates agree (matches prior last-line scan).
  return { ok: true, result: results[results.length - 1]! };
}

/**
 * Find the last line of `stdout` that is either a bare JSON witness-result
 * object or one following the `FAULTLINE_WITNESS_RESULT=` marker. Conflicting
 * records fail closed via {@link resolveWitnessResultFromStdout}; this helper
 * returns null both when absent and when conflicted (callers that need the
 * conflict signal should use resolveWitnessResultFromStdout).
 */
export function parseWitnessResult(stdout: string): WitnessResult | null {
  const resolved = resolveWitnessResultFromStdout(stdout);
  return resolved.ok ? resolved.result : null;
}

/**
 * Proof-bearing PASS requires exit 0; proof-bearing FAIL requires nonzero.
 * Harness / incompatibility / infrastructure outcomes may use any exit code.
 */
export function witnessResultConsistentWithExitCode(
  outcome: WitnessOutcome,
  exitCode: number
): boolean {
  switch (outcome) {
    case "PREDICATE_PASS":
      return exitCode === 0;
    case "PREDICATE_FAIL":
      return exitCode !== 0;
    case "INCOMPATIBLE_STATE":
    case "HARNESS_ERROR":
    case "INFRASTRUCTURE_ERROR":
      return true;
  }
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
