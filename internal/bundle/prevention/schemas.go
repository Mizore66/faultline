package prevention

import (
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

const SchemaVersion = "faultline.prevention-proof.v1"

var jsonTrue = jsjson.MakeBool(true)

var (
	digest    = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), ""))
	commit    = s.String(s.Regex(s.Pattern(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`), ""))
	runIDList = s.Array(digest, s.Length(3))

	executedState = s.Object(
		s.F("role", s.Enum("LAST_GOOD", "FIRST_BAD", "REPAIRED")),
		s.F("commit", commit),
		s.F("tree", s.Optional(commit)),
		s.F("verdict", s.Enum("PASS", "FAIL")),
		s.F("witnessDigest", digest),
		s.F("environmentDigest", digest),
		s.F("executionTrust", s.LiteralString("NATIVE_DOCKER")),
		s.F("executionKind", s.LiteralString("EXECUTED")),
		s.F("distinctExecutionCount", s.Number(s.Int(), s.Gte(3))),
		s.F("runIds", runIDList),
	).Strict()

	repairedRunBinding = s.Object(
		s.F("runId", digest),
		s.F("executionId", digest),
		s.F("commit", commit),
		s.F("tree", commit),
		s.F("verdict", s.LiteralString("PASS")),
		s.F("witnessDigest", digest),
		s.F("environmentDigest", digest),
		s.F("executionTrust", s.LiteralString("NATIVE_DOCKER")),
		s.F("executionKind", s.LiteralString("EXECUTED")),
	).Strict()

	limitations = s.Array(s.String(s.MinLength(1)), s.MinItems(1), s.MaxItems(16))

	BodySchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("originalProofRoot", digest),
		s.F("frozenWitnessDigest", digest),
		s.F("investigationDigest", s.Optional(digest)),
		s.F("lastGood", executedState.Extend(s.F("role", s.LiteralString("LAST_GOOD")), s.F("verdict", s.LiteralString("PASS")))),
		s.F("firstBad", executedState.Extend(s.F("role", s.LiteralString("FIRST_BAD")), s.F("verdict", s.LiteralString("FAIL")))),
		s.F("repaired", executedState.Extend(s.F("role", s.LiteralString("REPAIRED")), s.F("verdict", s.LiteralString("PASS")))),
		s.F("repairBaseTree", s.Optional(commit)),
		s.F("repairPatchDigest", s.Optional(digest)),
		s.F("codexThreadId", s.Optional(s.String(s.MinLength(1), s.MaxLength(256)))),
		s.F("hardGuardArtifactDigests", s.Optional(s.Array(digest, s.MaxItems(64)))),
		s.F("verified", s.Literal(jsonTrue)),
		s.F("limitations", limitations),
	).Strict()

	ManifestSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("classification", s.Enum("PREVENTION_EVIDENCE_SUMMARY", "PREVENTION_VERIFIED")),
		s.F("prevention", s.Object(s.F("path", s.LiteralString("prevention.json")), s.F("digest", digest)).Strict()),
		s.F("limitations", limitations),
		s.F("rootDigest", digest),
	).Strict()

	RepairedRunsSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.prevention-repaired-runs.v1")),
		s.F("runs", s.Array(repairedRunBinding, s.Length(3))),
	).Strict()
)
