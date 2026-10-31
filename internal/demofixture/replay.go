package demofixture

import (
	"errors"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// replayRun ports replayRun in src/fixture.ts. The run ID hashes the state ID
// string (not the whole state), the witness digest and the replay marker.
func replayRun(state, witness jsjson.Value) jsjson.Value {
	hunks := make(map[string]bool)
	for _, h := range state.Get("hunks").Items() {
		hunks[h.Str()] = true
	}
	verdict, reason, stdout, stderr := "PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""
	if hunks[unresolvedHunk] {
		verdict, reason, stdout, stderr = "ERROR", "FIXTURE_COMPILE_ERROR", "", "synthetic partial hunk does not compile\n"
	} else if hunks[sourceHunk] && hunks[rendererHunk] {
		verdict, reason, stdout, stderr = "FAIL", "WITNESS_ASSERTION_FAILED", "", "expected settlement USD; received EUR\n"
	}
	id := digest(jsjson.MustObject(
		"state", state.Get("id"),
		"witness", witness.Get("digest"),
		"replay", jsjson.MakeBool(true)))
	return jsjson.MustObject(
		"id", jsjson.MakeString(id),
		"stateId", state.Get("id"),
		"witnessDigest", witness.Get("digest"),
		"environmentDigest", jsjson.MakeString(digest(historicalEnvironment())),
		"verdict", jsjson.MakeString(verdict),
		"reasonCode", jsjson.MakeString(reason),
		"stdout", jsjson.MakeString(stdout),
		"stderr", jsjson.MakeString(stderr),
		"durationMs", jsjson.MakeNumber(0),
		"executionKind", jsjson.MakeString("CACHED"),
		"executedAt", jsjson.MakeString(replayExecutedAt))
}

// uniqueRunCatalog ports uniqueRunCatalog in src/engine.ts: identical records
// by ID collapse, conflicting ones throw, and the result is locale-sorted.
func uniqueRunCatalog(records []jsjson.Value) []jsjson.Value {
	byID := map[string]jsjson.Value{}
	var ids []string
	for _, r := range records {
		id := r.Get("id").Str()
		previous, exists := byID[id]
		if exists && jsjson.Stringify(previous) != jsjson.Stringify(r) {
			jsexc.Throw(errors.New(jsexc.Concat("Run id collision with conflicting evidence: ", id)))
		}
		if !exists {
			ids = append(ids, id)
		}
		byID[id] = r
	}
	ids = canonical.SortLocale(ids)
	out := make([]jsjson.Value, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}
