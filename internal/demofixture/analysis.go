package demofixture

import (
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func transitionKind(before, after string) string {
	if before == "PASS" && after == "FAIL" {
		return "PASS_TO_FAIL"
	}
	if before == "FAIL" && after == "PASS" {
		return "FAIL_TO_PASS"
	}
	return ""
}

func attempt(id string, subset []string, run jsjson.Value, note string) jsjson.Value {
	outcome := "UNRESOLVED"
	if v := run.Get("verdict").Str(); v == "PASS" || v == "FAIL" {
		outcome = v
	}
	return jsjson.MustObject(
		"id", jsjson.MakeString(id),
		"subset", strArray(subset...),
		"outcome", jsjson.MakeString(outcome),
		"runId", run.Get("id"),
		"note", jsjson.MakeString(note))
}

// CreateReplayAnalysis builds the cached REPLAY analysis. Every call builds a
// fresh value tree; cached records are never executed evidence.
func CreateReplayAnalysis() jsjson.Value { return createDemoAnalysis("REPLAY") }

// CreateRerunAnalysis builds the RERUN analysis from real built-in executions.
func CreateRerunAnalysis() jsjson.Value { return createDemoAnalysis("RERUN") }

func runsStable(runs []jsjson.Value) bool {
	ids := map[string]bool{}
	for _, r := range runs {
		v := r.Get("verdict").Str()
		if r.Get("executionKind").Str() != "EXECUTED" || (v != "PASS" && v != "FAIL") ||
			v != runs[0].Get("verdict").Str() ||
			r.Get("stateId").Str() != runs[0].Get("stateId").Str() ||
			r.Get("witnessDigest").Str() != runs[0].Get("witnessDigest").Str() ||
			r.Get("environmentDigest").Str() != runs[0].Get("environmentDigest").Str() {
			return false
		}
		ids[r.Get("id").Str()] = true
	}
	return len(ids) == len(runs)
}

func createDemoAnalysis(mode string) jsjson.Value {
	rerun := mode == "RERUN"
	witness := CreateFrozenWitness()
	if rerun {
		// The digest covers the body without itself, so recompute it after the edit.
		witness.Obj().Delete("digest")
		witness.Obj().Set("command", jsjson.MakeString("built-in-go-sample"))
		witness.Obj().Set("digest", jsjson.MakeString(digest(witness)))
	}
	states := fixtureStates()
	run := func(i int) jsjson.Value {
		if rerun {
			return executeFixtureState(states[i], witness, 0)
		}
		return replayRun(states[i], witness)
	}
	stableRuns := func(i int) (bool, []jsjson.Value) {
		if !rerun {
			return false, []jsjson.Value{run(i)}
		}
		var runs []jsjson.Value
		for n := 1; n <= 3; n++ {
			runs = append(runs, executeFixtureState(states[i], witness, n))
		}
		return runsStable(runs), runs
	}

	var contributionRuns, timelineRuns, boundaryRuns []jsjson.Value
	for i := idxContributionAtlas; i <= idxContributionDelta; i++ {
		contributionRuns = append(contributionRuns, run(i))
	}
	for i := idxTurn1; i <= idxTurn8; i++ {
		timelineRuns = append(timelineRuns, run(i))
	}

	var transitions []jsjson.Value
	for i := 1; i < len(timelineRuns); i++ {
		before, after := timelineRuns[i-1], timelineRuns[i]
		kind := transitionKind(before.Get("verdict").Str(), after.Get("verdict").Str())
		if kind == "" {
			continue
		}
		// REPLAY never re-runs: one cached run per side, never stable.
		beforeStable, beforeRuns := stableRuns(idxTurn1 + i - 1)
		afterStable, afterRuns := stableRuns(idxTurn1 + i)
		sideRuns := append(append([]jsjson.Value{}, beforeRuns...), afterRuns...)
		boundaryRuns = append(boundaryRuns, sideRuns...)
		var boundaryIDs []jsjson.Value
		for _, r := range sideRuns {
			boundaryIDs = append(boundaryIDs, r.Get("id"))
		}
		transitions = append(transitions, jsjson.MustObject(
			"beforeStateId", before.Get("stateId"),
			"afterStateId", after.Get("stateId"),
			"beforeVerdict", before.Get("verdict"),
			"afterVerdict", after.Get("verdict"),
			"stable", jsjson.MakeBool(beforeStable && afterStable),
			"kind", jsjson.MakeString(kind),
			"boundaryRunIds", jsjson.MakeArray(boundaryIDs)))
	}

	source, renderer, unresolved := run(idxSourceOnly), run(idxRendererOnly), run(idxUnresolved)
	candidate, necessity, empty := run(idxCandidate), run(idxReverted), run(idxLastGood)
	termination := "NOT_EXECUTED"
	if rerun {
		termination = "BIDIRECTIONALLY_VALIDATED"
	}
	minimization := jsjson.MustObject(
		"budget", jsjson.MustObject("used", jsjson.MakeNumber(6), "max", jsjson.MakeNumber(50)),
		"attempts", jsjson.MakeArray([]jsjson.Value{
			attempt("attempt-empty", nil, empty, "No implicated edits preserve the witness."),
			attempt("attempt-source", []string{sourceHunk}, source, "The display default alone does not change settlement currency."),
			attempt("attempt-renderer", []string{rendererHunk}, renderer, "The boundary assignment alone receives USD."),
			attempt("attempt-partial", []string{"partial-renderer-hunk"}, unresolved, "The partial patch cannot compile and is unresolved, not evidence."),
			attempt("attempt-candidate", []string{sourceHunk, rendererHunk}, candidate, "Applying the two edits to last-good reproduces the frozen witness failure."),
			attempt("attempt-necessity", []string{sourceHunk, rendererHunk}, necessity, "Removing the two edits from first-bad restores the frozen witness pass."),
		}),
		"candidate", strArray(sourceHunk, rendererHunk),
		"sufficiency", candidate,
		"necessity", necessity,
		"termination", jsjson.MakeString(termination))

	lastGood, firstBad, repaired := run(idxLastGood), run(idxFirstBad), run(idxRepaired)

	var all []jsjson.Value
	all = append(all, contributionRuns...)
	all = append(all, timelineRuns...)
	all = append(all, boundaryRuns...)
	all = append(all, source, renderer, unresolved, candidate, necessity, empty)
	all = append(all, lastGood, firstBad, repaired)

	// Port of src/engine.ts metrics: lengths of the contribution/timeline
	// state lists, distinct timeline hunks, and the implicated source/renderer set.
	sessions := idxContributionDelta - idxContributionAtlas + 1
	turns := idxTurn8 - idxTurn1 + 1
	implicated := map[string]bool{sourceHunk: true, rendererHunk: true}
	hunkSet := map[string]bool{}
	changedLines := 0
	for i := idxTurn1; i <= idxTurn8; i++ {
		hs := states[i].Get("hunks").Items()
		changedLines += len(hs) * 2
		for _, h := range hs {
			hunkSet[h.Str()] = true
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
	verified := rerun && lastGood.Get("verdict").Str() == "PASS" && firstBad.Get("verdict").Str() == "FAIL" && repaired.Get("verdict").Str() == "PASS"
	var firstStable jsjson.Value
	hasStable := false
	for _, tr := range transitions {
		if tr.Get("kind").Str() == "PASS_TO_FAIL" && tr.Get("stable").Bool() {
			firstStable, hasStable = tr, true
			break
		}
	}
	grade := "D"
	if verified && hasStable && termination == "BIDIRECTIONALLY_VALIDATED" {
		grade = "A"
	}
	fourth := "Cached replay is non-evidentiary and cannot establish stability, counterfactual proof, or prevention."
	warning := "Cached sample replay. It is non-evidentiary: use Re-run all evidence before relying on a boundary, minimization, or prevention claim."
	generatedAt := replayExecutedAt
	claim := func(kind, statement string, evidence ...jsjson.Value) jsjson.Value {
		return jsjson.MustObject("kind", str(kind), "statement", str(statement),
			"evidenceIds", jsjson.MakeArray(append([]jsjson.Value{}, evidence...)))
	}
	claims := []jsjson.Value{
		claim("UNKNOWN", "Cached replay does not establish an executed transition."),
		claim("UNKNOWN", "No stable boundary is certified from cached replay."),
		claim("UNKNOWN", "Cached counterfactual results are not a proof."),
	}
	if rerun {
		var boundaryIDs, attemptIDs []jsjson.Value
		stateEvidence := []jsjson.Value{str(""), str("")}
		if hasStable {
			boundaryIDs = firstStable.Get("boundaryRunIds").Items()
			stateEvidence = []jsjson.Value{firstStable.Get("beforeStateId"), firstStable.Get("afterStateId")}
		}
		for _, a := range minimization.Get("attempts").Items() {
			if id := a.Get("runId").Str(); id != "" {
				attemptIDs = append(attemptIDs, a.Get("runId"))
			}
		}
		claims = []jsjson.Value{
			claim("EXECUTED", "The frozen witness passes before and fails after Session Cedar turn 5.", stateEvidence...),
			claim("DERIVED", "Session Cedar / turn 5 is the first stable pass-to-fail transition in recorded order.", boundaryIDs...),
			claim("DERIVED", "The two-edit candidate is sufficient on last-good and necessary to preserve the sample failure.", attemptIDs...),
		}
		fourth = "The same witness passes on last-good, fails on first-bad, and passes on the repaired state."
		warning = "Executed built-in sample. The runner only materializes the reviewed fixture; it is not a general-purpose sandbox."
		generatedAt = clock.ISO(clock.Now())
	}
	claims = append(claims,
		claim("INFERRED", "Settlement and display-currency invariants were mixed at the boundary.", firstBad.Get("id")),
		claim("UNKNOWN", "Whether an agent intended that tradeoff is not evidenced."))

	return jsjson.MustObject(
		"schemaVersion", str("faultline.demo.v1"),
		"mode", str(mode),
		"generatedAt", str(generatedAt),
		"fixture", jsjson.MustObject(
			"id", str("parallel-settlement-regression"),
			"title", str("Parallel settlement-currency regression"),
			"summary", str("A deterministic Node sample with a real executable witness and a two-edit interaction."),
			"stateFidelity", str("DEMO_SAMPLE")),
		"witness", witness,
		"metrics", jsjson.MustObject(
			"sessions", jsjson.MakeNumber(float64(sessions)),
			"turns", jsjson.MakeNumber(float64(turns)),
			"files", jsjson.MakeNumber(float64(files)),
			"changedLines", jsjson.MakeNumber(float64(changedLines)),
			"implicatedHunks", jsjson.MakeNumber(float64(len(implicated)))),
		"contributionRuns", jsjson.MakeArray(contributionRuns),
		"timelineRuns", jsjson.MakeArray(timelineRuns),
		"transitions", jsjson.MakeArray(transitions),
		"runCatalog", jsjson.MakeArray(uniqueRunCatalog(all)),
		"minimization", minimization,
		"prevention", jsjson.MustObject(
			"lastGood", lastGood, "firstBad", firstBad, "repaired", repaired,
			"verified", jsjson.MakeBool(verified)),
		"grade", jsjson.MustObject(
			"value", str(grade),
			"reasons", strArray(
				"One approved witness digest is used for all comparable sample runs.",
				"The contribution and turn boundaries are stable across three reruns.",
				"The two-change-unit candidate is validated in both counterfactual directions.",
				fourth,
				"This is a deterministic sample fixture; it demonstrates the protocol, not a claim about an external repository.")),
		"claims", jsjson.MakeArray(claims),
		"warning", str(warning))
}
