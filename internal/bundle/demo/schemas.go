package demo

import s "github.com/Mizore66/faultline/internal/schema"

var (
	verdict       = s.Enum("PASS", "FAIL", "UNSTABLE", "ERROR", "INAPPLICABLE")
	evidenceKind  = s.Enum("EXECUTED", "DERIVED", "INFERRED", "UNKNOWN")
	stateFidelity = s.Enum("NATIVE", "RECONSTRUCTED", "DEMO_SAMPLE")
	runMode       = s.Enum("REPLAY", "RERUN")
	positiveInt   = s.Number(s.Int(), s.Positive())

	executionPolicy = s.Object(
		s.F("network", s.LiteralString("disabled")),
		s.F("credentials", s.LiteralString("redacted")),
		s.F("runner", s.Enum("built-in-sample", "sandboxed")),
		s.F("timeoutSeconds", positiveInt),
		s.F("note", s.String()),
	)

	witnessSchema = s.Object(
		s.F("id", s.String()),
		s.F("version", positiveInt),
		s.F("behavior", s.String()),
		s.F("command", s.String()),
		s.F("overlay", s.Array(s.String())),
		s.F("policy", executionPolicy),
		s.F("proposedBeforeLocalization", s.Boolean()),
		s.F("approvedAt", s.String()),
		s.F("incidentPacketDigest", s.String()),
		s.F("digest", s.String()),
	)

	runRecordSchema = s.Object(
		s.F("id", s.String()),
		s.F("stateId", s.String()),
		s.F("witnessDigest", s.String()),
		s.F("environmentDigest", s.String()),
		s.F("verdict", verdict),
		s.F("reasonCode", s.String()),
		s.F("stdout", s.String()),
		s.F("stderr", s.String()),
		s.F("durationMs", s.Number(s.Nonnegative())),
		s.F("executionKind", s.Enum("EXECUTED", "CACHED")),
		s.F("executedAt", s.String()),
	)

	transitionSchema = s.Object(
		s.F("beforeStateId", s.String()),
		s.F("afterStateId", s.String()),
		s.F("beforeVerdict", verdict),
		s.F("afterVerdict", verdict),
		s.F("stable", s.Boolean()),
		s.F("kind", s.Enum("PASS_TO_FAIL", "FAIL_TO_PASS")),
		s.F("boundaryRunIds", s.Array(s.String())),
	)

	minimizationAttemptSchema = s.Object(
		s.F("id", s.String()),
		s.F("subset", s.Array(s.String())),
		s.F("outcome", s.Enum("PASS", "FAIL", "UNRESOLVED")),
		s.F("runId", s.Optional(s.String())),
		s.F("note", s.String()),
	)

	evidenceClaimSchema = s.Object(
		s.F("kind", evidenceKind),
		s.F("statement", s.String()),
		s.F("evidenceIds", s.Array(s.String())),
	)

	// StoredMinimizationSchema (proof-bundle.ts:22) equals DemoAnalysis.minimization.
	minimizationSchema = s.Object(
		s.F("budget", s.Object(s.F("used", s.Number(s.Int(), s.Nonnegative())), s.F("max", positiveInt))),
		s.F("attempts", s.Array(minimizationAttemptSchema)),
		s.F("candidate", s.Array(s.String())),
		s.F("sufficiency", runRecordSchema),
		s.F("necessity", runRecordSchema),
		s.F("termination", s.Enum("BIDIRECTIONALLY_VALIDATED", "NOT_EXECUTED")),
	)

	// StoredPreventionSchema (proof-bundle.ts:31) equals DemoAnalysis.prevention.
	preventionSchema = s.Object(
		s.F("lastGood", runRecordSchema),
		s.F("firstBad", runRecordSchema),
		s.F("repaired", runRecordSchema),
		s.F("verified", s.Boolean()),
	)

	demoAnalysisSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.demo.v1")),
		s.F("mode", runMode),
		s.F("generatedAt", s.String()),
		s.F("fixture", s.Object(s.F("id", s.String()), s.F("title", s.String()), s.F("summary", s.String()), s.F("stateFidelity", stateFidelity))),
		s.F("witness", witnessSchema),
		s.F("metrics", s.Object(s.F("sessions", positiveInt), s.F("turns", positiveInt), s.F("files", positiveInt), s.F("changedLines", positiveInt), s.F("implicatedHunks", positiveInt))),
		s.F("contributionRuns", s.Array(runRecordSchema)),
		s.F("timelineRuns", s.Array(runRecordSchema)),
		s.F("transitions", s.Array(transitionSchema)),
		s.F("runCatalog", s.Array(runRecordSchema)),
		s.F("minimization", minimizationSchema),
		s.F("prevention", preventionSchema),
		s.F("grade", s.Object(s.F("value", s.Enum("A", "B", "C", "D")), s.F("reasons", s.Array(s.String())))),
		s.F("claims", s.Array(evidenceClaimSchema)),
		s.F("warning", s.String()),
	)

	manifestSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.proof-bundle.v2")),
		s.F("investigationId", s.String()),
		s.F("fixtureId", s.String()),
		s.F("generatedAt", s.String()),
		s.F("analysisDigest", s.String()),
		s.F("witnessDigest", s.String()),
		s.F("environmentDigest", s.String()),
		s.F("mode", s.Enum("REPLAY", "RERUN")),
		s.F("integrityScope", s.LiteralString("complete-declared-file-set")),
	)
)
