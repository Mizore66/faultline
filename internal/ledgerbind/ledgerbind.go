// Package ledgerbind ports src/ledger-binding.ts: a LedgerBoundInvestigation
// is an integrity bridge between a Git investigation result and the
// checkpoints of an independently verified Codex lifecycle ledger. It does
// not claim native Codex interception.
package ledgerbind

import (
	"runtime"
	"strconv"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	s "github.com/Mizore66/faultline/internal/schema"
)

// SchemaVersion ports LEDGER_BOUND_INVESTIGATION_SCHEMA_VERSION (src/ledger-binding.ts:33).
const SchemaVersion = "faultline.ledger-bound-investigation.v1"

var (
	// src/ledger-binding.ts:35-39.
	digestSchema      = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), "expected sha256:<64 lowercase hex characters>"))
	gitObjectIDSchema = s.String(s.Regex(s.Pattern(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`), "expected a 40- or 64-character lowercase Git object id"))

	// src/ledger-binding.ts:42.
	checkpointReferenceSchema = s.Object(
		s.F("checkpointEventSequence", s.Number(s.Int(), s.Positive())),
		s.F("checkpointEventId", s.String(s.MinLength(1))),
		s.F("checkpointEventHash", digestSchema),
		s.F("checkpointDigest", digestSchema),
		s.F("repositoryRoot", s.String(s.MinLength(1))),
		s.F("commit", gitObjectIDSchema),
		s.F("tree", gitObjectIDSchema),
		s.F("afterTurnOrdinal", s.Number(s.Int(), s.Nonnegative())),
		s.F("turnId", s.Nullable(s.String(s.MinLength(1)))),
		s.F("turnCompletedEventSequence", s.Nullable(s.Number(s.Int(), s.Positive()))),
	).Strict()

	// src/ledger-binding.ts:57.
	stateBindingSchema = checkpointReferenceSchema.Extend(
		s.F("stateIndex", s.Number(s.Int(), s.Nonnegative())),
	).Strict()

	// src/ledger-binding.ts:61.
	checkpointIssueSchema = checkpointReferenceSchema.Extend(
		s.F("reason", s.Enum("DUPLICATE_CHECKPOINT", "OUT_OF_RANGE_CHECKPOINT", "MISMATCHED_CHECKPOINT")),
	).Strict()

	// src/ledger-binding.ts:69.
	ledgerVerificationSummarySchema = s.Object(
		s.F("valid", s.Boolean()),
		s.F("errors", s.Array(s.String())),
		s.F("eventCount", s.Number(s.Int(), s.Nonnegative())),
		s.F("headHash", s.Nullable(digestSchema)),
	).Strict()

	// src/ledger-binding.ts:80.
	bindingReportSchema = s.Object(
		s.F("valid", s.Boolean()),
		s.F("errors", s.Array(s.String())),
		s.F("ledgerVerification", ledgerVerificationSummarySchema),
		s.F("investigationDigest", s.Nullable(digestSchema)),
		s.F("checkpointCount", s.Number(s.Int(), s.Nonnegative())),
		s.F("investigatedStateCount", s.Number(s.Int(), s.Nonnegative())),
		s.F("matchedStateCount", s.Number(s.Int(), s.Nonnegative())),
		s.F("bindings", s.Array(stateBindingSchema)),
		s.F("missingStates", s.Array(gitproof.GitCommitStateSchema)),
		s.F("duplicateStates", s.Array(gitproof.GitCommitStateSchema)),
		s.F("unmatchedCheckpoints", s.Array(checkpointIssueSchema)),
		s.F("duplicateCheckpoints", s.Array(checkpointIssueSchema)),
		s.F("outOfRangeCheckpoints", s.Array(checkpointIssueSchema)),
	).Strict()

	// src/ledger-binding.ts:95.
	unsignedSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("recorder", s.LiteralString("ledger-bound-git-investigation")),
		s.F("nativeCodexInterception", s.Literal(jsjson.MakeBool(false))),
		s.F("ledger", gitproof.CodexLifecycleLedgerSchema),
		s.F("ledgerHeadHash", digestSchema),
		s.F("investigation", gitproof.GitInvestigationResultSchema),
		s.F("investigationDigest", digestSchema),
		s.F("stateBindings", s.Array(stateBindingSchema, s.MinItems(1), s.MaxItems(512))),
	).Strict()

	// LedgerBoundInvestigationSchema ports src/ledger-binding.ts:113.
	boundSchema = unsignedSchema.Extend(s.F("bindingDigest", digestSchema)).Strict()
)

// BindingError ports LedgerBindingError (src/ledger-binding.ts:136).
type BindingError struct{ Message string }

func (e *BindingError) Error() string { return e.Message }

func object(pairs ...any) jsjson.Value {
	o := jsjson.NewObj()
	for i := 0; i < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1].(jsjson.Value))
	}
	return jsjson.MakeObject(o)
}

func str(v string) jsjson.Value { return jsjson.MakeString(v) }
func number(n int) jsjson.Value { return jsjson.MakeNumber(float64(n)) }

func stringList(items []string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, item := range items {
		out[i] = str(item)
	}
	return jsjson.MakeArray(out)
}

func parse(schema s.Schema, v jsjson.Value) jsjson.Value {
	return bundle.Must(bundle.ParseValue(v, schema))
}

func digestJSON(v jsjson.Value) string { return bundle.Must(canonical.DigestJSON(v)) }

func fmtNum(v jsjson.Value) string { return jsjson.FormatNumber(v.Num()) }

// Port of src/ledger-binding.ts:155 emptyReport.
func emptyReport(errs []string, verification gitproof.LedgerVerification, investigationDigest jsjson.Value) jsjson.Value {
	return parse(bindingReportSchema, object(
		"valid", jsjson.MakeBool(false),
		"errors", stringList(errs),
		"ledgerVerification", verification.JSON(),
		"investigationDigest", investigationDigest,
		"checkpointCount", number(0),
		"investigatedStateCount", number(0),
		"matchedStateCount", number(0),
		"bindings", jsjson.MakeArray(nil),
		"missingStates", jsjson.MakeArray(nil),
		"duplicateStates", jsjson.MakeArray(nil),
		"unmatchedCheckpoints", jsjson.MakeArray(nil),
		"duplicateCheckpoints", jsjson.MakeArray(nil),
		"outOfRangeCheckpoints", jsjson.MakeArray(nil),
	))
}

type completedTurn struct {
	turnID                 string
	completedEventSequence float64
}

// Port of src/ledger-binding.ts:176 checkpointReference.
func checkpointReference(event jsjson.Value, completedTurns map[float64]completedTurn) (jsjson.Value, bool) {
	if event.Get("event", "type").Str() != "WORKTREE_CHECKPOINT" {
		return jsjson.Value{}, false
	}
	payload := event.Get("event", "payload")
	checkpoint, afterTurnOrdinal := payload.Get("checkpoint"), payload.Get("afterTurnOrdinal")
	turnID, turnSequence := jsjson.MakeNull(), jsjson.MakeNull()
	if afterTurnOrdinal.Num() != 0 {
		if completed, ok := completedTurns[afterTurnOrdinal.Num()]; ok {
			turnID, turnSequence = str(completed.turnID), jsjson.MakeNumber(completed.completedEventSequence)
		}
	}
	return parse(checkpointReferenceSchema, object(
		"checkpointEventSequence", event.Get("sequence"),
		"checkpointEventId", event.Get("eventId"),
		"checkpointEventHash", event.Get("hash"),
		"checkpointDigest", checkpoint.Get("digest"),
		"repositoryRoot", checkpoint.Get("repositoryRoot"),
		"commit", checkpoint.Get("headCommit"),
		"tree", checkpoint.Get("treeDigest"),
		"afterTurnOrdinal", afterTurnOrdinal,
		"turnId", turnID,
		"turnCompletedEventSequence", turnSequence,
	)), true
}

// Port of src/ledger-binding.ts:198 checkpointReferences.
func checkpointReferences(ledger jsjson.Value) []jsjson.Value {
	completedTurns := map[float64]completedTurn{}
	var checkpoints []jsjson.Value
	for _, event := range ledger.Get("events").Items() {
		if event.Get("event", "type").Str() == "TURN_COMPLETED" {
			payload := event.Get("event", "payload")
			completedTurns[payload.Get("turnOrdinal").Num()] = completedTurn{payload.Get("turnId").Str(), event.Get("sequence").Num()}
		}
		if checkpoint, ok := checkpointReference(event, completedTurns); ok {
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	return checkpoints
}

// Port of src/ledger-binding.ts:215 stateKey.
func stateKey(commit, tree string) string { return commit + "\x00" + tree }

// Port of src/ledger-binding.ts:219 pathsEqual. On win32 the comparison
// lower-cases with the default locale (toLocaleLowerCase()).
func pathsEqual(left, right string) bool {
	normalizedLeft, normalizedRight := nodefs.Resolve(left), nodefs.Resolve(right)
	if runtime.GOOS == "windows" {
		return jscase.Lower(normalizedLeft) == jscase.Lower(normalizedRight)
	}
	return normalizedLeft == normalizedRight
}

// Port of src/ledger-binding.ts:227 issue.
func issue(reference jsjson.Value, reason string) jsjson.Value {
	o := jsjson.NewObj()
	for _, k := range reference.Obj().Keys() {
		o.Set(k, reference.Obj().Field(k))
	}
	o.Set("reason", str(reason))
	return parse(checkpointIssueSchema, jsjson.MakeObject(o))
}

// Port of src/ledger-binding.ts:231 sameState.
func sameState(left, right jsjson.Value) bool {
	return left.Get("commit").Str() == right.Get("commit").Str() && left.Get("tree").Str() == right.Get("tree").Str()
}

// Port of src/ledger-binding.ts:235 resultSemanticErrors.
func resultSemanticErrors(investigation jsjson.Value) (errs []string, duplicateStates []jsjson.Value) {
	states := investigation.Get("states").Items()
	seenIndices := map[float64]bool{}
	seenFacts := map[string]bool{}
	for position, state := range states {
		index := state.Get("index")
		if index.Num() != float64(position) {
			errs = append(errs, "Investigation state at position "+strconv.Itoa(position)+" has index "+fmtNum(index)+"; expected "+strconv.Itoa(position)+".")
		}
		if seenIndices[index.Num()] {
			errs = append(errs, "Investigation repeats state index "+fmtNum(index)+".")
		}
		seenIndices[index.Num()] = true
		key := stateKey(state.Get("commit").Str(), state.Get("tree").Str())
		if seenFacts[key] {
			errs = append(errs, "Investigation repeats Git state "+state.Get("commit").Str()+"/"+state.Get("tree").Str()+".")
			duplicateStates = append(duplicateStates, state)
		}
		seenFacts[key] = true
	}

	if len(states) == 0 {
		errs = append(errs, "Investigation has no Git states to bind.")
	}
	if investigation.Get("repository").Kind() == jsjson.Null {
		errs = append(errs, "Investigation has no resolved repository root to bind.")
	}
	resolved := investigation.Get("resolvedRange")
	if resolved.Kind() == jsjson.Null {
		errs = append(errs, "Investigation has no resolved Git range to bind.")
	} else if len(states) == 0 || !sameState(states[0], resolved.Get("ancestor")) || !sameState(states[len(states)-1], resolved.Get("descendant")) {
		errs = append(errs, "Investigation state endpoints do not match its resolved Git range.")
	}
	return errs, duplicateStates
}

// Bind ports bindVerifiedLedgerToGitInvestigation (src/ledger-binding.ts:272):
// it examines a ledger/result pair without accepting a partial mapping and
// returns the LedgerBindingReport.
func Bind(ledgerValue, investigationValue jsjson.Value) jsjson.Value {
	ledger, ledgerIssues, ledgerOK := s.Parse(gitproof.CodexLifecycleLedgerSchema, ledgerValue)
	investigation, investigationIssues, investigationOK := s.Parse(gitproof.GitInvestigationResultSchema, investigationValue)
	var errs []string

	if !ledgerOK {
		ledgerErrors := gitproof.IssueLines("Invalid lifecycle ledger at ", ledgerIssues)
		digest := jsjson.MakeNull()
		if investigationOK {
			digest = str(digestJSON(investigation))
		}
		return emptyReport(ledgerErrors, gitproof.LedgerVerification{Errors: ledgerErrors}, digest)
	}

	ledgerVerification := gitproof.VerifyCodexLifecycleLedger(ledger)
	if !ledgerVerification.Valid {
		for _, message := range ledgerVerification.Errors {
			errs = append(errs, "Lifecycle ledger is invalid: "+message)
		}
	}

	if !investigationOK {
		errs = append(errs, gitproof.IssueLines("Invalid Git investigation at ", investigationIssues)...)
		return emptyReport(errs, ledgerVerification, jsjson.MakeNull())
	}

	investigationDigest := digestJSON(investigation)
	semanticErrors, duplicateStates := resultSemanticErrors(investigation)
	errs = append(errs, semanticErrors...)

	checkpoints := checkpointReferences(ledger)
	states := investigation.Get("states").Items()
	stateByFact := map[string][]jsjson.Value{}
	stateByCommit := map[string]int{}
	stateByTree := map[string]int{}
	for _, state := range states {
		factKey := stateKey(state.Get("commit").Str(), state.Get("tree").Str())
		stateByFact[factKey] = append(stateByFact[factKey], state)
		stateByCommit[state.Get("commit").Str()]++
		stateByTree[state.Get("tree").Str()]++
	}

	// A Map iterates in insertion order: keep the keys in a slice.
	var factOrder []string
	checkpointsByFact := map[string][]jsjson.Value{}
	for _, checkpoint := range checkpoints {
		key := stateKey(checkpoint.Get("commit").Str(), checkpoint.Get("tree").Str())
		if _, seen := checkpointsByFact[key]; !seen {
			factOrder = append(factOrder, key)
		}
		checkpointsByFact[key] = append(checkpointsByFact[key], checkpoint)
	}

	duplicateCheckpoints := []jsjson.Value{}
	for _, key := range factOrder {
		references := checkpointsByFact[key]
		if len(references) < 2 {
			continue
		}
		for _, duplicate := range references[1:] {
			duplicateCheckpoints = append(duplicateCheckpoints, issue(duplicate, "DUPLICATE_CHECKPOINT"))
			errs = append(errs, "Checkpoint event "+fmtNum(duplicate.Get("checkpointEventSequence"))+" duplicates commit/tree facts already recorded in this ledger.")
		}
	}

	unmatchedCheckpoints := []jsjson.Value{}
	outOfRangeCheckpoints := []jsjson.Value{}
	candidateByStateIndex := map[float64]jsjson.Value{}
	repository := investigation.Get("repository")
	for _, checkpoint := range checkpoints {
		key := stateKey(checkpoint.Get("commit").Str(), checkpoint.Get("tree").Str())
		matchingStates := stateByFact[key]
		matchingCheckpoints := checkpointsByFact[key]
		sequence := fmtNum(checkpoint.Get("checkpointEventSequence"))

		if len(matchingCheckpoints) != 1 {
			continue
		}

		if repository.Kind() == jsjson.Null || !pathsEqual(checkpoint.Get("repositoryRoot").Str(), repository.Str()) {
			outOfRangeCheckpoints = append(outOfRangeCheckpoints, issue(checkpoint, "OUT_OF_RANGE_CHECKPOINT"))
			errs = append(errs, "Checkpoint event "+sequence+" belongs to a repository outside the investigation root.")
			continue
		}
		if len(matchingStates) == 0 {
			hasPartialMatch := stateByCommit[checkpoint.Get("commit").Str()] > 0 || stateByTree[checkpoint.Get("tree").Str()] > 0
			if hasPartialMatch {
				unmatchedCheckpoints = append(unmatchedCheckpoints, issue(checkpoint, "MISMATCHED_CHECKPOINT"))
				errs = append(errs, "Checkpoint event "+sequence+" has a commit/tree pair that does not match any investigated state.")
			} else {
				outOfRangeCheckpoints = append(outOfRangeCheckpoints, issue(checkpoint, "OUT_OF_RANGE_CHECKPOINT"))
				errs = append(errs, "Checkpoint event "+sequence+" is outside the investigated Git range.")
			}
			continue
		}
		if len(matchingStates) != 1 {
			unmatchedCheckpoints = append(unmatchedCheckpoints, issue(checkpoint, "MISMATCHED_CHECKPOINT"))
			errs = append(errs, "Checkpoint event "+sequence+" ambiguously matches duplicate investigated Git states.")
			continue
		}
		state := matchingStates[0]
		index := state.Get("index")
		if _, taken := candidateByStateIndex[index.Num()]; taken {
			// This should already be covered by duplicate commit/tree facts, but it
			// remains a safety check if a malicious result repeats only an index.
			unmatchedCheckpoints = append(unmatchedCheckpoints, issue(checkpoint, "MISMATCHED_CHECKPOINT"))
			errs = append(errs, "Checkpoint event "+sequence+" would bind state index "+fmtNum(index)+" more than once.")
			continue
		}
		candidateByStateIndex[index.Num()] = checkpoint
	}

	missingStates := []jsjson.Value{}
	bindings := []jsjson.Value{}
	for _, state := range states {
		checkpoint, ok := candidateByStateIndex[state.Get("index").Num()]
		if !ok {
			missingStates = append(missingStates, state)
			errs = append(errs, "Investigation state "+fmtNum(state.Get("index"))+" ("+state.Get("commit").Str()+"/"+state.Get("tree").Str()+") has no unique verified checkpoint.")
			continue
		}
		binding := jsjson.NewObj()
		for _, k := range checkpoint.Obj().Keys() {
			binding.Set(k, checkpoint.Obj().Field(k))
		}
		binding.Set("stateIndex", state.Get("index"))
		bindings = append(bindings, parse(stateBindingSchema, jsjson.MakeObject(binding)))
	}

	for index := 1; index < len(bindings); index++ {
		previous, current := bindings[index-1], bindings[index]
		between := " between states " + fmtNum(previous.Get("stateIndex")) + " and " + fmtNum(current.Get("stateIndex")) + "."
		if current.Get("checkpointEventSequence").Num() <= previous.Get("checkpointEventSequence").Num() {
			errs = append(errs, "Checkpoint ordering does not preserve investigation state order"+between)
		}
		if current.Get("afterTurnOrdinal").Num() < previous.Get("afterTurnOrdinal").Num() {
			errs = append(errs, "Checkpoint turn ordering regresses"+between)
		}
		previousTurn, currentTurn := previous.Get("turnCompletedEventSequence"), current.Get("turnCompletedEventSequence")
		if previousTurn.Kind() != jsjson.Null && currentTurn.Kind() != jsjson.Null && currentTurn.Num() < previousTurn.Num() {
			errs = append(errs, "Completed-turn event ordering regresses"+between)
		}
	}

	return parse(bindingReportSchema, object(
		"valid", jsjson.MakeBool(len(errs) == 0),
		"errors", stringList(errs),
		"ledgerVerification", ledgerVerification.JSON(),
		"investigationDigest", str(investigationDigest),
		"checkpointCount", number(len(checkpoints)),
		"investigatedStateCount", number(len(states)),
		"matchedStateCount", number(len(bindings)),
		"bindings", jsjson.MakeArray(bindings),
		"missingStates", jsjson.MakeArray(missingStates),
		"duplicateStates", jsjson.MakeArray(append([]jsjson.Value{}, duplicateStates...)),
		"unmatchedCheckpoints", jsjson.MakeArray(unmatchedCheckpoints),
		"duplicateCheckpoints", jsjson.MakeArray(duplicateCheckpoints),
		"outOfRangeCheckpoints", jsjson.MakeArray(outOfRangeCheckpoints),
	))
}

// Port of src/ledger-binding.ts:419 unsignedBoundInvestigation.
func unsignedBound(v jsjson.Value) jsjson.Value {
	return parse(unsignedSchema, bundle.Without(v, "bindingDigest"))
}

// Digest ports ledgerBoundInvestigationDigest (src/ledger-binding.ts:425):
// the canonical digest of every field except bindingDigest.
func Digest(v jsjson.Value) string { return digestJSON(unsignedBound(v)) }

func reportErrors(report jsjson.Value) []string { return bundle.Strings(report.Get("errors")) }

// Create ports createLedgerBoundInvestigation (src/ledger-binding.ts:436): a
// bound record only after the ledger and every state mapping verify.
// ledgerPath names where the ledger was read from; the TS function has no
// such parameter, so it does not affect the result.
func Create(ledger, investigation jsjson.Value, ledgerPath string) (jsjson.Value, error) {
	_ = ledgerPath
	report := Bind(ledger, investigation)
	if !report.Get("valid").Bool() {
		return jsjson.Value{}, &BindingError{jsexc.Concat("Cannot bind lifecycle ledger to Git investigation: ", jsexc.Join(reportErrors(report), "; "))}
	}
	headHash := report.Get("ledgerVerification", "headHash")
	investigationDigest := report.Get("investigationDigest")
	if headHash.Kind() != jsjson.String || headHash.Str() == "" || investigationDigest.Kind() != jsjson.String || investigationDigest.Str() == "" {
		return jsjson.Value{}, &BindingError{"Cannot bind without a verified ledger head and investigation digest."}
	}
	unsigned := parse(unsignedSchema, object(
		"schemaVersion", str(SchemaVersion),
		"recorder", str("ledger-bound-git-investigation"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"ledger", ledger,
		"ledgerHeadHash", headHash,
		"investigation", investigation,
		"investigationDigest", investigationDigest,
		"stateBindings", report.Get("bindings"),
	))
	signed := jsjson.NewObj()
	for _, k := range unsigned.Obj().Keys() {
		signed.Set(k, unsigned.Obj().Field(k))
	}
	signed.Set("bindingDigest", str(digestJSON(unsigned)))
	return parse(boundSchema, jsjson.MakeObject(signed)), nil
}

// Verify ports verifyLedgerBoundInvestigation (src/ledger-binding.ts:469):
// every nested integrity and correspondence check is recomputed. provided is
// `expectedBindingDigest !== undefined`. The result keeps the TS key order;
// `investigation` is absent when the record does not parse.
func Verify(value jsjson.Value, expected string, provided bool) jsjson.Value {
	bound, issues, ok := s.Parse(boundSchema, value)
	if !ok {
		status := "NOT_PROVIDED"
		if provided {
			status = "MISMATCH"
		}
		return object(
			"valid", jsjson.MakeBool(false),
			"errors", stringList(gitproof.IssueLines("Invalid ledger-bound investigation at ", issues)),
			"bindingDigest", jsjson.MakeNull(),
			"externalDigestStatus", str(status),
			"ledgerBinding", jsjson.MakeNull(),
		)
	}

	var errs []string
	calculatedDigest := Digest(bound)
	if bound.Get("bindingDigest").Str() != calculatedDigest {
		errs = append(errs, "bindingDigest does not match the canonical ledger-bound investigation contents.")
	}
	report := Bind(bound.Get("ledger"), bound.Get("investigation"))
	if !report.Get("valid").Bool() {
		for _, message := range reportErrors(report) {
			errs = append(errs, "Ledger/Git binding is invalid: "+message)
		}
	}
	headHash := report.Get("ledgerVerification", "headHash")
	if headHash.Kind() == jsjson.Null || bound.Get("ledgerHeadHash").Str() != headHash.Str() {
		errs = append(errs, "ledgerHeadHash does not match the verified lifecycle ledger head.")
	}
	if bound.Get("investigationDigest").Str() != digestJSON(bound.Get("investigation")) {
		errs = append(errs, "investigationDigest does not match the canonical Git investigation result.")
	}
	if !bundle.SameCanonical(bound.Get("stateBindings"), report.Get("bindings")) {
		errs = append(errs, "stateBindings do not exactly match the verified ledger checkpoint mapping.")
	}
	externalDigestStatus := "NOT_PROVIDED"
	if provided {
		if _, _, wellFormed := s.Parse(digestSchema, str(expected)); !wellFormed {
			errs = append(errs, "externally supplied binding digest is not a sha256 digest.")
			externalDigestStatus = "MISMATCH"
		} else if expected != calculatedDigest {
			errs = append(errs, "externally supplied binding digest does not match.")
			externalDigestStatus = "MISMATCH"
		} else {
			externalDigestStatus = "MATCH"
		}
	}
	return object(
		"valid", jsjson.MakeBool(len(errs) == 0),
		"errors", stringList(errs),
		"bindingDigest", str(calculatedDigest),
		"externalDigestStatus", str(externalDigestStatus),
		"ledgerBinding", report,
		"investigation", bound.Get("investigation"),
	)
}

// WriteAtomic ports writeLedgerBoundInvestigationAtomic (src/ledger-binding.ts:567):
// only a fully valid record is written, as canonical JSON and a newline.
func WriteAtomic(path string, v jsjson.Value) (string, error) {
	verification := Verify(v, "", false)
	if !verification.Get("valid").Bool() {
		return "", &BindingError{jsexc.Concat("Refusing to write an invalid ledger-bound investigation: ", jsexc.Join(reportErrors(verification), "; "))}
	}
	parsed := parse(boundSchema, v)
	// atomicWrite (src/ledger-binding.ts:536) is src/ledger.ts:599's code.
	return nodefs.AtomicWriteFile(path, jsexc.Concat(bundle.Must(canonical.CanonicalJSON(parsed)), "\n"))
}

// VerifyFile ports verifyLedgerBoundInvestigationFile (src/ledger-binding.ts:578):
// an unreadable or malformed file is an invalid result, not an exception.
func VerifyFile(path, expected string, provided bool) jsjson.Value {
	var result jsjson.Value
	if err := jsexc.Try(func() {
		parsed := bundle.Must(bundle.ParseJSONFile(nodefs.Resolve(path)))
		result = Verify(parsed, expected, provided)
	}); err != nil {
		status := "NOT_PROVIDED"
		if provided {
			status = "MISMATCH"
		}
		return object(
			"valid", jsjson.MakeBool(false),
			"errors", stringList([]string{jsexc.Concat("Unable to read ledger-bound investigation: ", jsexc.Message(err))}),
			"bindingDigest", jsjson.MakeNull(),
			"externalDigestStatus", str(status),
			"ledgerBinding", jsjson.MakeNull(),
		)
	}
	return result
}

// ReadVerified ports readVerifiedLedgerBoundInvestigation (src/ledger-binding.ts:600)
// without an expected digest.
func ReadVerified(path string) (jsjson.Value, error) {
	verification := VerifyFile(path, "", false)
	if !verification.Get("valid").Bool() {
		return jsjson.Value{}, &BindingError{jsexc.Concat("Ledger-bound investigation verification failed: ", jsexc.Join(reportErrors(verification), "; "))}
	}
	var record jsjson.Value
	if err := jsexc.Try(func() {
		record = parse(boundSchema, bundle.Must(bundle.ParseJSONFile(nodefs.Resolve(path))))
	}); err != nil {
		return jsjson.Value{}, &BindingError{jsexc.Concat("Unable to read verified ledger-bound investigation: ", jsexc.Message(err))}
	}
	return record, nil
}
