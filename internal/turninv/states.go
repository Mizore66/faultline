package turninv

import (
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/v8sort"
)

// State ports TurnState (:60).
type State struct {
	Index, TurnOrdinal                       int
	TurnID, Role, TreeDigest, SnapshotDigest string
	Dirty                                    bool
}

// Value is the state in TS key order: index, turnId, turnOrdinal, role,
// treeDigest, snapshotDigest, dirty.
func (st State) Value() jsjson.Value {
	return jsjson.MustObject(
		"index", jsjson.MakeNumber(float64(st.Index)),
		"turnId", jsjson.MakeString(st.TurnID),
		"turnOrdinal", jsjson.MakeNumber(float64(st.TurnOrdinal)),
		"role", jsjson.MakeString(st.Role),
		"treeDigest", jsjson.MakeString(st.TreeDigest),
		"snapshotDigest", jsjson.MakeString(st.SnapshotDigest),
		"dirty", jsjson.MakeBool(st.Dirty),
	)
}

// StatesValue is states as a JSON array of State.Value.
func StatesValue(states []State) jsjson.Value {
	items := make([]jsjson.Value, len(states))
	for i, st := range states {
		items[i] = st.Value()
	}
	return jsjson.MakeArray(items)
}

// StatesFromValue reads result.states (already schema-parsed).
func StatesFromValue(v jsjson.Value) []State {
	states := make([]State, 0, len(v.Items()))
	for _, item := range v.Items() {
		states = append(states, State{
			Index:          int(item.Get("index").Num()),
			TurnID:         item.Get("turnId").Str(),
			TurnOrdinal:    int(item.Get("turnOrdinal").Num()),
			Role:           item.Get("role").Str(),
			TreeDigest:     item.Get("treeDigest").Str(),
			SnapshotDigest: item.Get("snapshotDigest").Str(),
			Dirty:          item.Get("dirty").Bool(),
		})
	}
	return states
}

// StatesFromLedger ports turnStatesFromLedger (:347). ledger is a parsed
// CodexLifecycleLedger; a snapshot that fails TurnTreeSnapshotSchema
// throws the ZodError (jsexc), as TS's .parse does.
func StatesFromLedger(ledger jsjson.Value) ([]State, error) {
	states := []State{}
	for _, record := range ledger.Get("events").Items() {
		eventType := record.Get("event", "type")
		if eventType.Kind() == jsjson.String && eventType.Str() == "SESSION_BASELINE_SNAPSHOT" {
			snapshot, err := bundle.ParseValue(record.Get("event", "payload", "snapshot"), gitproof.TurnTreeSnapshotSchema)
			if err != nil {
				return nil, err
			}
			states = append(states, State{
				Index:          len(states),
				TurnID:         SessionBaselineTurnID,
				TurnOrdinal:    0,
				Role:           "SESSION_BASELINE",
				TreeDigest:     snapshot.Get("treeDigest").Str(),
				SnapshotDigest: snapshot.Get("digest").Str(),
				Dirty:          snapshot.Get("dirty").Bool(),
			})
			continue
		}
		if eventType.Kind() != jsjson.String || eventType.Str() != "TURN_TREE_SNAPSHOT" {
			continue
		}
		snapshot, err := bundle.ParseValue(record.Get("event", "payload", "snapshot"), gitproof.TurnTreeSnapshotSchema)
		if err != nil {
			return nil, err
		}
		payload := record.Get("event", "payload")
		states = append(states, State{
			Index:          len(states),
			TurnID:         payload.Get("turnId").Str(),
			TurnOrdinal:    int(payload.Get("turnOrdinal").Num()),
			Role:           "TURN",
			TreeDigest:     snapshot.Get("treeDigest").Str(),
			SnapshotDigest: snapshot.Get("digest").Str(),
			Dirty:          snapshot.Get("dirty").Bool(),
		})
	}
	// (:375) left.turnOrdinal - right.turnOrdinal || left.index - right.index.
	v8sort.Sort(states, func(left, right State) int {
		if d := sign(float64(left.TurnOrdinal) - float64(right.TurnOrdinal)); d != 0 {
			return d
		}
		return sign(float64(left.Index) - float64(right.Index))
	})
	return states, nil
}

func sign(d float64) int {
	switch {
	case d < 0:
		return -1
	case d > 0:
		return 1
	}
	return 0
}

// DuplicateTurnOrdinal ports duplicateTurnOrdinalError (:379); "" is null.
func DuplicateTurnOrdinal(states []State) string {
	ordinals := map[int]bool{}
	for _, st := range states {
		ordinals[st.TurnOrdinal] = true
	}
	if len(ordinals) == len(states) {
		return ""
	}
	return DuplicateOrdinalError
}

// strictEqual is JS === on JSON scalars; objects and arrays (compared by
// identity in JS) are never equal here, as distinct parsed values are not.
func strictEqual(a, b jsjson.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case jsjson.String:
		return a.Str() == b.Str()
	case jsjson.Number:
		return a.Num() == b.Num()
	case jsjson.Bool:
		return a.Bool() == b.Bool()
	case jsjson.Null, jsjson.Undefined:
		return true
	}
	return false
}

func isString(v jsjson.Value, s string) bool { return v.Kind() == jsjson.String && v.Str() == s }

func introduction(status string, turnOrdinal, turnID jsjson.Value, reason string) jsjson.Value {
	return jsjson.MustObject(
		"status", jsjson.MakeString(status),
		"turnOrdinal", turnOrdinal,
		"turnId", turnID,
		"reason", jsjson.MakeString(reason),
	)
}

// AttributeFailureIntroduction ports attributeFailureIntroduction (:393).
// stable nil is the TS default [].
func AttributeFailureIntroduction(states []State, transitions, stable []jsjson.Value) jsjson.Value {
	var firstPassToFail jsjson.Value
	found := false
	for _, transition := range transitions {
		if isString(transition.Get("kind"), "PASS_TO_FAIL") {
			firstPassToFail, found = transition, true
			break
		}
	}
	if !found {
		return introduction("NOT_APPLICABLE", jsjson.MakeNull(), jsjson.MakeNull(),
			"No PASS→FAIL transition is available to attribute.")
	}

	var baselineState *State
	for i := range states {
		if states[i].Role == "SESSION_BASELINE" && states[i].TurnOrdinal == 0 {
			baselineState = &states[i]
			break
		}
	}
	if baselineState == nil {
		return introduction("UNATTRIBUTED", jsjson.MakeNull(), jsjson.MakeNull(),
			"No SESSION_BASELINE_SNAPSHOT; cannot claim a turn introduced the failure.")
	}

	var baselineStable jsjson.Value
	baselineFound := false
	for _, st := range stable {
		if isString(st.Get("role"), "SESSION_BASELINE") && strictEqual(st.Get("turnOrdinal"), jsjson.MakeNumber(0)) &&
			strictEqual(st.Get("stateIndex"), jsjson.MakeNumber(float64(baselineState.Index))) {
			baselineStable, baselineFound = st, true
			break
		}
	}
	// (:420) Without stableStates only a baseline state is required.
	if len(stable) > 0 && (!baselineFound || !isString(baselineStable.Get("verdict"), "PASS")) {
		return introduction("UNATTRIBUTED", jsjson.MakeNull(), jsjson.MakeNull(),
			"No stable passing session baseline; cannot claim a turn introduced the failure.")
	}

	before, after := firstPassToFail.Get("before"), firstPassToFail.Get("after")
	if !isString(before.Get("verdict"), "PASS") || !isString(after.Get("verdict"), "FAIL") ||
		!strictEqual(after.Get("stateIndex"), jsjson.MakeNumber(before.Get("stateIndex").Num()+1)) {
		return introduction("UNATTRIBUTED", jsjson.MakeNull(), jsjson.MakeNull(),
			"The earliest PASS→FAIL transition crosses an observation gap or is not a stable adjacent pair.")
	}

	return introduction("ATTRIBUTED", after.Get("turnOrdinal"), after.Get("turnId"),
		"Earliest recorded stable PASS→FAIL occurred at turn "+jsjson.FormatNumber(after.Get("turnOrdinal").Num())+".")
}

// IsDecisiveRun ports isDecisiveRun (:454): only structured PREDICATE_*
// outcomes of a Docker-isolated run are decisive.
func IsDecisiveRun(run jsjson.Value) bool {
	result := run.Get("result")
	if !isString(result.Get("kind"), "DOCKER_ISOLATED") {
		return false
	}
	switch verdict := result.Get("verdict"); {
	case isString(verdict, "PASS"):
		return isString(result.Get("reason"), "PREDICATE_PASS")
	case isString(verdict, "FAIL"):
		return isString(result.Get("reason"), "PREDICATE_FAIL")
	}
	return false
}

// ReconstructStable ports reconstructStableTurnStates (:467). runs are
// schema-parsed run facts in array order; requireNative is
// options.requireNativeExecutor === true. Each stable state is returned as
// its JSON value in StableStateSchema key order.
func ReconstructStable(states []State, runs []jsjson.Value, requireNative bool) []jsjson.Value {
	stable := []jsjson.Value{}
	for _, st := range states {
		var stateRuns []jsjson.Value
		for _, run := range runs {
			if strictEqual(run.Get("stateIndex"), jsjson.MakeNumber(float64(st.Index))) {
				stateRuns = append(stateRuns, run)
			}
		}
		v8sort.Sort(stateRuns, func(left, right jsjson.Value) int {
			return sign(left.Get("executionAttempt").Num() - right.Get("executionAttempt").Num())
		})
		if len(stateRuns) != gitproof.StableExecutionCount {
			continue
		}
		attempts, executionIDs := map[float64]bool{}, map[string]bool{}
		for _, run := range stateRuns {
			attempts[run.Get("executionAttempt").Num()] = true
			executionIDs[run.Get("executionId").Str()] = true
		}
		if len(attempts) != gitproof.StableExecutionCount || len(executionIDs) != gitproof.StableExecutionCount {
			continue
		}
		verdict := stateRuns[0].Get("result", "verdict")
		if !isString(verdict, "PASS") && !isString(verdict, "FAIL") {
			continue
		}
		consistent := true
		for _, run := range stateRuns {
			if !isString(run.Get("turnId"), st.TurnID) ||
				!strictEqual(run.Get("turnOrdinal"), jsjson.MakeNumber(float64(st.TurnOrdinal))) ||
				!isString(run.Get("role"), st.Role) ||
				!isString(run.Get("treeDigest"), st.TreeDigest) ||
				!isString(run.Get("snapshotDigest"), st.SnapshotDigest) ||
				!isString(run.Get("result", "verdict"), verdict.Str()) ||
				!IsDecisiveRun(run) ||
				(requireNative && !isString(run.Get("result", "executor"), "NATIVE_DOCKER")) {
				consistent = false
				break
			}
		}
		if !consistent {
			continue
		}
		executionIDValues := make([]jsjson.Value, len(stateRuns))
		runIDValues := make([]jsjson.Value, len(stateRuns))
		for i, run := range stateRuns {
			executionIDValues[i] = run.Get("executionId")
			runIDValues[i] = run.Get("runId")
		}
		stable = append(stable, jsjson.MustObject(
			"stateIndex", jsjson.MakeNumber(float64(st.Index)),
			"turnId", jsjson.MakeString(st.TurnID),
			"turnOrdinal", jsjson.MakeNumber(float64(st.TurnOrdinal)),
			"role", jsjson.MakeString(st.Role),
			"treeDigest", jsjson.MakeString(st.TreeDigest),
			"snapshotDigest", jsjson.MakeString(st.SnapshotDigest),
			"verdict", verdict,
			"executionIds", jsjson.MakeArray(executionIDValues),
			"runIds", jsjson.MakeArray(runIDValues),
		))
	}
	return stable
}

// FindTransitions ports findStableTurnTransitions (:511). before and after
// are the stable state values themselves.
func FindTransitions(stable []jsjson.Value) []jsjson.Value {
	output := []jsjson.Value{}
	for index := 1; index < len(stable); index++ {
		before, after := stable[index-1], stable[index]
		if !strictEqual(after.Get("stateIndex"), jsjson.MakeNumber(before.Get("stateIndex").Num()+1)) ||
			strictEqual(before.Get("verdict"), after.Get("verdict")) {
			continue
		}
		kind := "FAIL_TO_PASS"
		if isString(before.Get("verdict"), "PASS") {
			kind = "PASS_TO_FAIL"
		}
		output = append(output, jsjson.MustObject("kind", jsjson.MakeString(kind), "before", before, "after", after))
	}
	return output
}

// HasNonMonotonic ports hasNonMonotonicTransitions (:526).
func HasNonMonotonic(transitions []jsjson.Value) bool {
	passToFail, failToPass := false, false
	for _, transition := range transitions {
		passToFail = passToFail || isString(transition.Get("kind"), "PASS_TO_FAIL")
		failToPass = failToPass || isString(transition.Get("kind"), "FAIL_TO_PASS")
	}
	return passToFail && failToPass
}
