// Package demofixture builds the pure cached (REPLAY) demo fixture and
// analysis. It ports the REPLAY branches of src/fixture.ts and src/engine.ts
// and never reads artifacts, runs Node or Git: every call returns fresh
// ordered jsjson values.
package demofixture

import (
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

const (
	sourceHunk     = "settlement-display-default"
	rendererHunk   = "settlement-display-boundary"
	unresolvedHunk = "partial-renderer-hunk"

	replayExecutedAt = "2026-07-14T09:05:00.000Z"
)

// Fixture state indexes within fixtureStates().
const (
	idxContributionAtlas = iota
	idxContributionBirch
	idxContributionCedar
	idxContributionDelta
	idxTurn1
	idxTurn2
	idxTurn3
	idxTurn4
	idxTurn5
	idxTurn6
	idxTurn7
	idxTurn8
	idxCandidate
	idxReverted
	idxSourceOnly
	idxRendererOnly
	idxUnresolved
	idxRepaired
	idxFirstBad
	idxLastGood
)

func digest(v jsjson.Value) string { return jsexc.Must(canonical.DigestJSON(v)) }

func strArray(items ...string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, s := range items {
		out[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(out)
}

// historicalEnvironment is the environment captured under Node v22.22.2. It is
// cached fixture provenance, not a measurement of the current Go host.
func historicalEnvironment() jsjson.Value {
	return jsjson.MustObject(
		"runtime", jsjson.MakeString("v22.22.2"),
		"packageManager", jsjson.MakeString("none"),
		"runner", jsjson.MakeString("built-in-sample-v1"),
		"network", jsjson.MakeString("disabled"),
		"credentials", jsjson.MakeString("redacted"))
}

func incidentPacket() jsjson.Value {
	return jsjson.MustObject(
		"symptom", jsjson.MakeString("A completed refund can overwrite settlement currency with display currency."),
		"ciExcerpt", jsjson.MakeString("Expected settlement currency USD; received EUR."),
		"repository", jsjson.MakeString("currency-reconciliation-sample"),
		"language", jsjson.MakeString("JavaScript"))
}

// CreateFrozenWitness returns a fresh copy of the approved frozen witness.
func CreateFrozenWitness() jsjson.Value {
	body := jsjson.MustObject(
		"id", jsjson.MakeString("refund-settlement-currency"),
		"version", jsjson.MakeNumber(1),
		"behavior", jsjson.MakeString("Completed refunds preserve the order settlement currency."),
		"command", jsjson.MakeString("node witness.mjs"),
		"overlay", strArray("witness.mjs"),
		"policy", jsjson.MustObject(
			"network", jsjson.MakeString("disabled"),
			"credentials", jsjson.MakeString("redacted"),
			"runner", jsjson.MakeString("built-in-sample"),
			"timeoutSeconds", jsjson.MakeNumber(2),
			"note", jsjson.MakeString("Only the built-in, reviewed sample is executed by the judge fixture.")),
		"proposedBeforeLocalization", jsjson.MakeBool(true),
		"approvedAt", jsjson.MakeString("2026-07-14T09:00:00.000Z"),
		"incidentPacketDigest", jsjson.MakeString(digest(incidentPacket())))
	body.Obj().Set("digest", jsjson.MakeString(digest(body)))
	return body
}

// makeState builds one fixture state. The tree digest covers only id,
// contributionId, turnOrdinal and hunks, in that insertion order.
func makeState(id, contribution, session string, turn int, hunks []string, label string) jsjson.Value {
	treeDigest := digest(jsjson.MustObject(
		"id", jsjson.MakeString(id),
		"contributionId", jsjson.MakeString(contribution),
		"turnOrdinal", jsjson.MakeNumber(float64(turn)),
		"hunks", strArray(hunks...)))
	return jsjson.MustObject(
		"id", jsjson.MakeString(id),
		"contributionId", jsjson.MakeString(contribution),
		"sessionName", jsjson.MakeString(session),
		"turnOrdinal", jsjson.MakeNumber(float64(turn)),
		"hunks", strArray(hunks...),
		"treeDigest", jsjson.MakeString(treeDigest),
		"fidelity", jsjson.MakeString("DEMO_SAMPLE"),
		"label", jsjson.MakeString(label))
}

// fixtureStates returns the 20 source states in the fixed index order.
func fixtureStates() []jsjson.Value {
	both := []string{sourceHunk, rendererHunk}
	return []jsjson.Value{
		makeState("contribution-atlas", "atlas", "Session Atlas", 1, nil, "Atlas contribution boundary"),
		makeState("contribution-birch", "birch", "Session Birch", 1, nil, "Birch contribution boundary"),
		makeState("contribution-cedar", "cedar", "Session Cedar", 5, both, "Cedar contribution boundary"),
		makeState("contribution-delta", "delta", "Session Delta", 1, both, "Delta contribution boundary"),
		makeState("cedar-turn-1", "cedar", "Session Cedar", 1, nil, "Turn 1"),
		makeState("cedar-turn-2", "cedar", "Session Cedar", 2, nil, "Turn 2"),
		makeState("cedar-turn-3", "cedar", "Session Cedar", 3, nil, "Turn 3"),
		makeState("cedar-turn-4", "cedar", "Session Cedar", 4, nil, "Turn 4 — last good"),
		makeState("cedar-turn-5", "cedar", "Session Cedar", 5, both, "Turn 5 — first bad"),
		makeState("cedar-turn-6", "cedar", "Session Cedar", 6, both, "Turn 6"),
		makeState("cedar-turn-7", "cedar", "Session Cedar", 7, nil, "Turn 7 — repaired later"),
		makeState("cedar-turn-8", "cedar", "Session Cedar", 8, both, "Turn 8 — reintroduced"),
		makeState("candidate-two-hunks", "counterfactual", "Counterfactual", 1, both, "Apply two implicated hunks to last good"),
		makeState("candidate-remove-two-hunks", "counterfactual", "Counterfactual", 5, nil, "Remove two implicated hunks from first bad"),
		makeState("candidate-source-only", "counterfactual", "Counterfactual", 2, []string{sourceHunk}, "Apply source hunk only"),
		makeState("candidate-renderer-only", "counterfactual", "Counterfactual", 3, []string{rendererHunk}, "Apply boundary hunk only"),
		makeState("candidate-unresolved", "counterfactual", "Counterfactual", 4, []string{unresolvedHunk}, "Apply partial renderer hunk"),
		makeState("repair-worktree", "repair", "Repair worktree", 1, nil, "Repair worktree"),
		makeState("prevention-first-bad", "prevention", "Prevention proof", 2, both, "First bad state"),
		makeState("prevention-last-good", "prevention", "Prevention proof", 1, nil, "Last good state"),
	}
}

// StateLabel returns the fixture label for a state ID, or the ID unchanged
// when it is not a fixture state.
func StateLabel(id string) string {
	for _, s := range fixtureStates() {
		if s.Get("id").Str() == id {
			return s.Get("label").Str()
		}
	}
	return id
}
