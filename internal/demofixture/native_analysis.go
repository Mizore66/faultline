package demofixture

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nativefixture"
)

// RunNativeAnalysis executes only the reviewed Go demo model. The calling
// executable must dispatch nativefixture.DispatchChild at its private entry.
// Approval records caller authorization; it does not authenticate a person.
func RunNativeAnalysis(ctx context.Context, approval NativeApproval) (NativeResult, error) {
	return runNativeAnalysis(ctx, approval, nativeDeps{
		newExecutor: func() (nativeExecutor, error) { return nativefixture.NewExecutor() },
		now:         clock.Now, nonce: clock.NewUUID,
	})
}

// Observe construction facts when they become available, including if nonce
// acquisition fails before newNativeSession can return a session.
type nativeEnvironmentObserver struct {
	nativeExecutor
	observe func(jsjson.Value)
}

func (e *nativeEnvironmentObserver) Environment() jsjson.Value {
	value := e.nativeExecutor.Environment()
	if e.observe != nil {
		e.observe(value)
		e.observe = nil
	}
	return value
}

func nativePanicError(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("%v", value)
}

func nativeWallTime(now func() time.Time) (value time.Time, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &NativeAnalysisError{Kind: "PANIC", Err: nativePanicError(recovered)}
		}
	}()
	if now == nil {
		return time.Time{}, &NativeAnalysisError{Kind: "ENVIRONMENT", Err: errors.New("native wall clock is unavailable")}
	}
	return now(), nil
}

func runNativeAnalysis(ctx context.Context, approval NativeApproval, deps nativeDeps) (result NativeResult, err error) {
	var session *nativeSession
	var environment jsjson.Value
	var nonce string
	proposal := NativeWitnessProposal()
	entry, entryErr := nativeWallTime(deps.now)
	defer func() {
		if recovered := recover(); recovered != nil {
			cause := nativePanicError(recovered)
			var typed *NativeAnalysisError
			if errors.As(cause, &typed) {
				err = cause
			} else {
				err = &NativeAnalysisError{Kind: "PANIC", Err: cause}
			}
		}
		var generatedAt time.Time
		if entryErr == nil {
			var finalErr error
			generatedAt, finalErr = nativeWallTime(deps.now)
			if finalErr != nil {
				err = errors.Join(err, finalErr)
			}
		}
		status := "COMPLETE"
		if err != nil {
			status = "ERROR"
			result.Analysis = jsjson.Value{}
		}
		if session != nil {
			result.Provenance = session.provenance(status, generatedAt)
		} else {
			env, envDigest := jsjson.MakeNull(), jsjson.MakeNull()
			if environment.Kind() == jsjson.Object {
				env, envDigest = environment, jsjson.MakeString(digest(environment))
			}
			result.Provenance = cloneNativeValue(jsjson.MustObject(
				"schemaVersion", jsjson.MakeString("faultline.native-demo-execution.v1"), "status", jsjson.MakeString("ERROR"),
				"proposal", proposal, "program", cloneNativeValue(proposal.Get("program")), "programDigest", proposal.Get("programDigest"),
				"approval", jsjson.MustObject("proposalDigest", jsjson.MakeString(approval.ProposalDigest), "approvedAt", jsjson.MakeString(approval.ApprovedAt)),
				"environmentPreimage", env, "environmentDigest", envDigest, "nonce", nativeOptionalString(nonce),
				"generatedAt", nativeOptionalTime(generatedAt), "invocations", jsjson.MakeArray(nil)))
		}
		if err == nil {
			result.Analysis.Obj().Set("generatedAt", jsjson.MakeString(clock.ISO(generatedAt)))
			// Analysis and provenance are independently owned return snapshots.
			result.Analysis = cloneNativeValue(result.Analysis)
		}
	}()
	if entryErr != nil {
		return result, entryErr
	}
	if ctx == nil {
		return result, &NativeAnalysisError{Kind: "CANCELLED", Err: errors.New("native analysis requires a context")}
	}
	if cause := ctx.Err(); cause != nil {
		return result, nativeContextError(cause)
	}
	if deps.newExecutor == nil || deps.nonce == nil {
		return result, &NativeAnalysisError{Kind: "ENVIRONMENT", Err: errors.New("native execution dependencies are unavailable")}
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	observedDeps := deps
	observedDeps.newExecutor = func() (nativeExecutor, error) {
		executor, cause := deps.newExecutor()
		if cause != nil {
			return nil, cause
		}
		if executor == nil {
			return nil, errors.New("native executor is unavailable")
		}
		return &nativeEnvironmentObserver{nativeExecutor: executor, observe: func(v jsjson.Value) {
			if v.Kind() != jsjson.Object {
				panic(&NativeAnalysisError{Kind: "ENVIRONMENT", Err: errors.New("native environment observation is not an object")})
			}
			environment = cloneNativeValue(v)
		}}, nil
	}
	observedDeps.nonce = func() string {
		value := deps.nonce()
		if value == "" {
			panic(&NativeAnalysisError{Kind: "ENVIRONMENT", Err: errors.New("native analysis nonce is empty")})
		}
		nonce = value
		return value
	}
	session, err = newNativeSession(approval, observedDeps, entry)
	if err != nil {
		return result, err
	}
	result.Analysis, err = session.analyze(bounded)
	return result, err
}

func nativeContextError(cause error) error {
	kind := "CANCELLED"
	if errors.Is(cause, context.DeadlineExceeded) {
		kind = "DEADLINE"
	}
	return &NativeAnalysisError{Kind: kind, Err: cause}
}

func (s *nativeSession) analyze(ctx context.Context) (jsjson.Value, error) {
	states := fixtureStates()
	var all []jsjson.Value
	proof := func(stateID, message string) error {
		return &NativeAnalysisError{Kind: "PROOF", Ordinal: s.ordinal, StateID: stateID, Err: errors.New(message)}
	}
	run := func(index, attempt int, verdict string) (jsjson.Value, error) {
		state := states[index]
		if cause := ctx.Err(); cause != nil {
			s.ordinal++
			err := nativeContextError(cause).(*NativeAnalysisError)
			err.Ordinal, err.StateID = s.ordinal, state.Get("id").Str()
			s.receipts = append(s.receipts, nativeReceipt(s.ordinal, err.StateID, attempt,
				nativefixture.Capture{}, jsjson.Value{}, jsjson.Value{}, err.Kind))
			return jsjson.Value{}, err
		}
		record, err := s.run(ctx, state, attempt)
		if err != nil {
			return jsjson.Value{}, err
		}
		if record.Get("stateId").Str() != state.Get("id").Str() || record.Get("witnessDigest").Str() != s.witness.Get("digest").Str() || record.Get("environmentDigest").Str() != s.environmentDigest || record.Get("executionKind").Str() != "EXECUTED" || record.Get("id").Str() == "" || verdict != "" && record.Get("verdict").Str() != verdict {
			return jsjson.Value{}, proof(state.Get("id").Str(), "native observation does not establish the required state, identity, or verdict")
		}
		all = append(all, record)
		return record, nil
	}
	var contributions, timeline, transitions []jsjson.Value
	for i := idxContributionAtlas; i <= idxContributionDelta; i++ {
		record, err := run(i, 0, "")
		if err != nil {
			return jsjson.Value{}, err
		}
		contributions = append(contributions, record)
	}
	for i := idxTurn1; i <= idxTurn8; i++ {
		record, err := run(i, 0, "")
		if err != nil {
			return jsjson.Value{}, err
		}
		timeline = append(timeline, record)
	}
	for i := 1; i < len(timeline); i++ {
		expected := ""
		switch i {
		case idxTurn5 - idxTurn1, idxTurn8 - idxTurn1:
			expected = "PASS_TO_FAIL"
		case idxTurn7 - idxTurn1:
			expected = "FAIL_TO_PASS"
		}
		if transitionKind(timeline[i-1].Get("verdict").Str(), timeline[i].Get("verdict").Str()) != expected {
			return jsjson.Value{}, proof(timeline[i].Get("stateId").Str(), "native timeline does not establish the three expected boundaries")
		}
	}
	var firstRegression jsjson.Value
	for i := 1; i < len(timeline); i++ {
		before, after := timeline[i-1], timeline[i]
		kind := transitionKind(before.Get("verdict").Str(), after.Get("verdict").Str())
		if kind == "" {
			continue
		}
		var ids []jsjson.Value
		for _, side := range []struct {
			index    int
			baseline jsjson.Value
		}{{idxTurn1 + i - 1, before}, {idxTurn1 + i, after}} {
			for attempt := 1; attempt <= 3; attempt++ {
				record, err := run(side.index, attempt, side.baseline.Get("verdict").Str())
				if err != nil {
					return jsjson.Value{}, err
				}
				ids = append(ids, record.Get("id"))
			}
		}
		transition := jsjson.MustObject(
			"beforeStateId", before.Get("stateId"), "afterStateId", after.Get("stateId"),
			"beforeVerdict", before.Get("verdict"), "afterVerdict", after.Get("verdict"),
			"stable", jsjson.MakeBool(true), "kind", jsjson.MakeString(kind), "boundaryRunIds", jsjson.MakeArray(ids))
		transitions = append(transitions, transition)
		if firstRegression.Kind() == jsjson.Undefined && kind == "PASS_TO_FAIL" {
			firstRegression = transition
		}
	}
	if len(transitions) != 3 || firstRegression.Get("beforeStateId").Str() != states[idxTurn4].Get("id").Str() || firstRegression.Get("afterStateId").Str() != states[idxTurn5].Get("id").Str() || transitions[1].Get("beforeStateId").Str() != states[idxTurn6].Get("id").Str() || transitions[1].Get("afterStateId").Str() != states[idxTurn7].Get("id").Str() || transitions[1].Get("kind").Str() != "FAIL_TO_PASS" || transitions[2].Get("beforeStateId").Str() != states[idxTurn7].Get("id").Str() || transitions[2].Get("afterStateId").Str() != states[idxTurn8].Get("id").Str() || transitions[2].Get("kind").Str() != "PASS_TO_FAIL" {
		return jsjson.Value{}, proof("", "native timeline does not establish the three expected stable boundaries")
	}
	minimized := make([]jsjson.Value, 0, 6)
	for _, step := range []struct {
		index   int
		verdict string
	}{{idxSourceOnly, "PASS"}, {idxRendererOnly, "PASS"}, {idxUnresolved, "ERROR"}, {idxCandidate, "FAIL"}, {idxReverted, "PASS"}, {idxLastGood, "PASS"}} {
		record, err := run(step.index, 0, step.verdict)
		if err != nil {
			return jsjson.Value{}, err
		}
		if step.index == idxUnresolved && record.Get("reasonCode").Str() != "FIXTURE_PARSE_ERROR" {
			return jsjson.Value{}, proof(record.Get("stateId").Str(), "partial configuration is not unresolved parse evidence")
		}
		minimized = append(minimized, record)
	}
	source, renderer, unresolved, candidate, necessity, empty := minimized[0], minimized[1], minimized[2], minimized[3], minimized[4], minimized[5]
	if jsjson.Stringify(states[idxCandidate].Get("hunks")) != jsjson.Stringify(strArray(sourceHunk, rendererHunk)) || len(states[idxReverted].Get("hunks").Items()) != 0 || len(states[idxLastGood].Get("hunks").Items()) != 0 {
		return jsjson.Value{}, proof(candidate.Get("stateId").Str(), "counterfactual state subsets do not establish the nonempty two-edit interaction")
	}
	minimization := jsjson.MustObject(
		"budget", jsjson.MustObject("used", jsjson.MakeNumber(6), "max", jsjson.MakeNumber(50)),
		"attempts", jsjson.MakeArray([]jsjson.Value{
			attempt("attempt-empty", nil, empty, "No implicated edits preserve the witness."),
			attempt("attempt-source", []string{sourceHunk}, source, "The display default alone does not change settlement currency."),
			attempt("attempt-renderer", []string{rendererHunk}, renderer, "The boundary assignment alone receives USD."),
			attempt("attempt-partial", []string{unresolvedHunk}, unresolved, "The partial native configuration cannot parse and is unresolved, not proof."),
			attempt("attempt-candidate", []string{sourceHunk, rendererHunk}, candidate, "Applying the two edits to last-good reproduces the approved native witness failure."),
			attempt("attempt-necessity", []string{sourceHunk, rendererHunk}, necessity, "Removing the two edits from first-bad restores the approved native witness pass."),
		}), "candidate", strArray(sourceHunk, rendererHunk), "sufficiency", candidate, "necessity", necessity, "termination", jsjson.MakeString("BIDIRECTIONALLY_VALIDATED"))
	prevention := make([]jsjson.Value, 0, 3)
	for _, step := range []struct {
		index   int
		verdict string
	}{{idxLastGood, "PASS"}, {idxFirstBad, "FAIL"}, {idxRepaired, "PASS"}} {
		record, err := run(step.index, 0, step.verdict)
		if err != nil {
			return jsjson.Value{}, err
		}
		prevention = append(prevention, record)
	}
	lastGood, firstBad, repaired := prevention[0], prevention[1], prevention[2]
	catalog := uniqueRunCatalog(all)
	if len(catalog) != 39 || len(s.receipts) != 39 || len(s.seenIDs) != 39 || s.ordinal != 39 {
		return jsjson.Value{}, proof("", "native catalog and invocation evidence do not cover exactly39 unique executions")
	}
	for i, record := range all {
		receipt := s.receipts[i]
		if !receipt.Get("accepted").Bool() || receipt.Get("runId").Str() != record.Get("id").Str() || jsjson.Stringify(receipt.Get("runRecord")) != jsjson.Stringify(record) {
			return jsjson.Value{}, proof(record.Get("stateId").Str(), "catalog execution is not linked to exactly one complete accepted receipt")
		}
	}
	hunkSet := map[string]bool{}
	changedLines := 0
	for i := idxTurn1; i <= idxTurn8; i++ {
		for _, h := range states[i].Get("hunks").Items() {
			hunkSet[h.Str()] = true
			changedLines += 2
		}
	}
	files := len(hunkSet)
	if files == 0 {
		files = 1
	}
	if changedLines == 0 {
		changedLines = 1
	}
	str := jsjson.MakeString
	claim := func(kind, statement string, evidence ...jsjson.Value) jsjson.Value {
		return jsjson.MustObject("kind", str(kind), "statement", str(statement), "evidenceIds", jsjson.MakeArray(append([]jsjson.Value{}, evidence...)))
	}
	var attemptIDs []jsjson.Value
	for _, a := range minimization.Get("attempts").Items() {
		attemptIDs = append(attemptIDs, a.Get("runId"))
	}
	return jsjson.MustObject(
		"schemaVersion", str("faultline.demo.v1"), "mode", str("RERUN"), "generatedAt", jsjson.MakeNull(),
		"fixture", jsjson.MustObject("id", str("parallel-settlement-regression"), "title", str("Parallel settlement-currency regression — Go model"), "summary", str("A reviewed Go model evaluated under materialized configuration, with a real native witness and a two-edit interaction."), "stateFidelity", str("DEMO_SAMPLE")),
		"witness", s.witness,
		"metrics", jsjson.MustObject("sessions", jsjson.MakeNumber(float64(len(contributions))), "turns", jsjson.MakeNumber(float64(len(timeline))), "files", jsjson.MakeNumber(float64(files)), "changedLines", jsjson.MakeNumber(float64(changedLines)), "implicatedHunks", jsjson.MakeNumber(2)),
		"contributionRuns", jsjson.MakeArray(contributions), "timelineRuns", jsjson.MakeArray(timeline), "transitions", jsjson.MakeArray(transitions), "runCatalog", jsjson.MakeArray(catalog), "minimization", minimization,
		"prevention", jsjson.MustObject("lastGood", lastGood, "firstBad", firstBad, "repaired", repaired, "verified", jsjson.MakeBool(true)),
		"grade", jsjson.MustObject("value", str("A"), "reasons", strArray(
			"One approved witness digest is used for all comparable sample runs.",
			"The contribution and turn boundaries are stable across three reruns.",
			"The two-change-unit candidate is validated in both counterfactual directions.",
			"The same witness passes on last-good, fails on first-bad, and passes on the repaired state.",
			"This is a deterministic sample fixture; it demonstrates the protocol, not a claim about an external repository.")),
		"claims", jsjson.MakeArray([]jsjson.Value{
			claim("EXECUTED", "The approved native witness passes before and fails after Session Cedar turn 5.", firstRegression.Get("beforeStateId"), firstRegression.Get("afterStateId")),
			claim("DERIVED", "Session Cedar / turn 5 is the first stable pass-to-fail transition in recorded order.", firstRegression.Get("boundaryRunIds").Items()...),
			claim("DERIVED", "The two-edit candidate is sufficient on last-good and necessary to preserve the sample failure.", attemptIDs...),
			claim("INFERRED", "Settlement and display-currency invariants were mixed at the boundary.", firstBad.Get("id")),
			claim("UNKNOWN", "Whether an agent intended that tradeoff is not evidenced."),
		}), "warning", str("Executed reviewed Go model. This does not execute the original JavaScript or external repository code and is not a general-purpose sandbox.")), nil
}
