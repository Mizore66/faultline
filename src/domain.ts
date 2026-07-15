import { z } from "zod";

export const verdictValues = ["PASS", "FAIL", "UNSTABLE", "ERROR", "INAPPLICABLE"] as const;
export const evidenceKindValues = ["EXECUTED", "DERIVED", "INFERRED", "UNKNOWN"] as const;

export const VerdictSchema = z.enum(verdictValues);
export const EvidenceKindSchema = z.enum(evidenceKindValues);
export const StateFidelitySchema = z.enum(["NATIVE", "RECONSTRUCTED", "DEMO_SAMPLE"]);
export const RunModeSchema = z.enum(["REPLAY", "RERUN"]);

export type Verdict = z.infer<typeof VerdictSchema>;
export type EvidenceKind = z.infer<typeof EvidenceKindSchema>;
export type StateFidelity = z.infer<typeof StateFidelitySchema>;
export type RunMode = z.infer<typeof RunModeSchema>;

export const ExecutionPolicySchema = z.object({
  network: z.literal("disabled"),
  credentials: z.literal("redacted"),
  runner: z.enum(["built-in-sample", "sandboxed"]),
  timeoutSeconds: z.number().int().positive(),
  note: z.string()
});

export const WitnessSchema = z.object({
  id: z.string(),
  version: z.number().int().positive(),
  behavior: z.string(),
  command: z.string(),
  overlay: z.array(z.string()),
  policy: ExecutionPolicySchema,
  proposedBeforeLocalization: z.boolean(),
  approvedAt: z.string(),
  incidentPacketDigest: z.string(),
  digest: z.string()
});

export const FixtureStateSchema = z.object({
  id: z.string(),
  contributionId: z.string(),
  sessionName: z.string(),
  turnOrdinal: z.number().int().positive(),
  hunks: z.array(z.string()),
  treeDigest: z.string(),
  fidelity: StateFidelitySchema,
  label: z.string()
});

export const RunRecordSchema = z.object({
  id: z.string(),
  stateId: z.string(),
  witnessDigest: z.string(),
  environmentDigest: z.string(),
  verdict: VerdictSchema,
  reasonCode: z.string(),
  stdout: z.string(),
  stderr: z.string(),
  durationMs: z.number().nonnegative(),
  executionKind: z.enum(["EXECUTED", "CACHED"]),
  executedAt: z.string()
});

export const TransitionSchema = z.object({
  beforeStateId: z.string(),
  afterStateId: z.string(),
  beforeVerdict: VerdictSchema,
  afterVerdict: VerdictSchema,
  stable: z.boolean(),
  kind: z.enum(["PASS_TO_FAIL", "FAIL_TO_PASS"]),
  boundaryRunIds: z.array(z.string())
});

export const MinimizationAttemptSchema = z.object({
  id: z.string(),
  subset: z.array(z.string()),
  outcome: z.enum(["PASS", "FAIL", "UNRESOLVED"]),
  runId: z.string().optional(),
  note: z.string()
});

export const EvidenceClaimSchema = z.object({
  kind: EvidenceKindSchema,
  statement: z.string(),
  evidenceIds: z.array(z.string())
});

export const DemoAnalysisSchema = z.object({
  schemaVersion: z.literal("faultline.demo.v1"),
  mode: RunModeSchema,
  generatedAt: z.string(),
  fixture: z.object({
    id: z.string(),
    title: z.string(),
    summary: z.string(),
    stateFidelity: StateFidelitySchema
  }),
  witness: WitnessSchema,
  metrics: z.object({
    sessions: z.number().int().positive(),
    turns: z.number().int().positive(),
    files: z.number().int().positive(),
    changedLines: z.number().int().positive(),
    implicatedHunks: z.number().int().positive()
  }),
  contributionRuns: z.array(RunRecordSchema),
  timelineRuns: z.array(RunRecordSchema),
  transitions: z.array(TransitionSchema),
  runCatalog: z.array(RunRecordSchema),
  minimization: z.object({
    budget: z.object({ used: z.number().int().nonnegative(), max: z.number().int().positive() }),
    attempts: z.array(MinimizationAttemptSchema),
    candidate: z.array(z.string()),
    sufficiency: RunRecordSchema,
    necessity: RunRecordSchema,
    termination: z.enum(["BIDIRECTIONALLY_VALIDATED", "NOT_EXECUTED"])
  }),
  prevention: z.object({
    lastGood: RunRecordSchema,
    firstBad: RunRecordSchema,
    repaired: RunRecordSchema,
    verified: z.boolean()
  }),
  grade: z.object({
    value: z.enum(["A", "B", "C", "D"]),
    reasons: z.array(z.string())
  }),
  claims: z.array(EvidenceClaimSchema),
  warning: z.string()
});

export type ExecutionPolicy = z.infer<typeof ExecutionPolicySchema>;
export type Witness = z.infer<typeof WitnessSchema>;
export type FixtureState = z.infer<typeof FixtureStateSchema>;
export type RunRecord = z.infer<typeof RunRecordSchema>;
export type Transition = z.infer<typeof TransitionSchema>;
export type MinimizationAttempt = z.infer<typeof MinimizationAttemptSchema>;
export type EvidenceClaim = z.infer<typeof EvidenceClaimSchema>;
export type DemoAnalysis = z.infer<typeof DemoAnalysisSchema>;
