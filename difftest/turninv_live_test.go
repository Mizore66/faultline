//go:build faultline_test

package difftest

import (
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/turninv"
)

// turnOutcome renders a Go result as oracle-turninv.ts's outcome does:
// JSON.stringify of {value} or {threw, zod}.
func turnOutcome(v jsjson.Value, err error) string {
	if err != nil {
		var zodErr *schema.Error
		return jsjson.Stringify(jsjson.MustObject("threw", jsjson.MakeString(jsexc.Message(err)), "zod", jsjson.MakeBool(errors.As(err, &zodErr))))
	}
	return jsjson.Stringify(jsjson.MustObject("value", v))
}

// numberSentinel marks a number a generator wants written in a JSON form
// jsjson.Stringify never emits ("-0", "1e0", "9007199254740993"): the value
// is a string "\u0000num:<text>" until withNumberForms rewrites it.
func numberSentinel(text string) jsjson.Value { return jsjson.MakeString("\x00num:" + text) }

var numberSentinelText = regexp.MustCompile(`"\\u0000num:([^"]*)"`)

func withNumberForms(text string) string { return numberSentinelText.ReplaceAllString(text, "$1") }

// hasBigInteger reports whether a generated text carries a bigIntegers form.
func hasBigInteger(text string) bool {
	for _, form := range bigIntegers {
		if strings.Contains(text, ":"+form+",") || strings.Contains(text, ":"+form+"}") {
			return true
		}
	}
	return false
}

// bigIntegers are schema-valid integers an int64 cannot hold or a float64
// cannot hold exactly: 2^53+1 (parses to 2^53), 2^63, 1e19, 2e19, 2^64.
var bigIntegers = []string{"9007199254740993", "9007199254740992", "9223372036854775808", "1e19", "2e19", "18446744073709551616"}

func mustParseText(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return v
}

// mutateTurnLedger applies one of spec 2.5's ledger changes: snapshot
// events reordered, ordinals duplicated or written as -0/1e0, a payload
// leaf corrupted, the baseline dropped or doubled.
func mutateTurnLedger(r *rand.Rand, ledger jsjson.Value) {
	events := ledger.Get("events").Items()
	var snapshots []int
	for i, e := range events {
		if typ := e.Get("event", "type").Str(); typ == "TURN_TREE_SNAPSHOT" || typ == "SESSION_BASELINE_SNAPSHOT" {
			snapshots = append(snapshots, i)
		}
	}
	setEvents := func(items []jsjson.Value) { ledger.Obj().Set("events", jsjson.MakeArray(items)) }
	pickSnapshot := func() (int, bool) {
		if len(snapshots) == 0 {
			return 0, false
		}
		return snapshots[r.IntN(len(snapshots))], true
	}
	turnPayloads := func() []*jsjson.Obj {
		var out []*jsjson.Obj
		for _, e := range events {
			if e.Get("event", "type").Str() == "TURN_TREE_SNAPSHOT" {
				out = append(out, e.Get("event", "payload").Obj())
			}
		}
		return out
	}
	switch r.IntN(9) {
	case 0: // move a snapshot event across turns
		if i, ok := pickSnapshot(); ok {
			e := events[i]
			rest := slices.Delete(slices.Clone(events), i, i+1)
			setEvents(slices.Insert(rest, r.IntN(len(rest)+1), e))
		}
	case 1: // swap two snapshot events
		if len(snapshots) >= 2 {
			a, b := snapshots[r.IntN(len(snapshots))], snapshots[r.IntN(len(snapshots))]
			events[a], events[b] = events[b], events[a]
		}
	case 2: // duplicate an ordinal (another turn's, or the baseline's 0)
		if ps := turnPayloads(); len(ps) > 0 {
			target := ps[r.IntN(len(ps))]
			if r.IntN(3) == 0 {
				target.Set("turnOrdinal", jsjson.MakeNumber(0))
			} else {
				target.Set("turnOrdinal", ps[r.IntN(len(ps))].Field("turnOrdinal"))
			}
		}
	case 3: // duplicate a snapshot event
		if i, ok := pickSnapshot(); ok {
			setEvents(slices.Insert(slices.Clone(events), r.IntN(len(events)+1), cloneJSON(events[i])))
		}
	case 4: // -0 and 1e0 ordinals
		if ps := turnPayloads(); len(ps) > 0 {
			forms := append([]string{"-0", "1e0", "2e0", "0e0"}, bigIntegers...)
			ps[r.IntN(len(ps))].Set("turnOrdinal", numberSentinel(forms[r.IntN(len(forms))]))
		}
	case 5: // corrupt a snapshot payload's leaf (gen.CorruptLeaf)
		if i, ok := pickSnapshot(); ok {
			payload := events[i].Get("event", "payload")
			events[i].Get("event").Obj().Set("payload", mustParseOrSelf(gen.CorruptLeaf(r, jsjson.Stringify(payload)), payload))
		}
	case 6: // drop the baseline (or any snapshot)
		for k := len(events) - 1; k >= 0; k-- {
			if events[k].Get("event", "type").Str() == "SESSION_BASELINE_SNAPSHOT" || r.IntN(8) == 0 && slices.Contains(snapshots, k) {
				setEvents(slices.Delete(slices.Clone(events), k, k+1))
				break
			}
		}
	case 7: // a structural edit of the snapshot itself (key dropped, added or replaced)
		if i, ok := pickSnapshot(); ok {
			payload := events[i].Get("event", "payload").Obj()
			payload.Set("snapshot", mustParseOrSelf(gen.CorruptDeep(r, jsjson.Stringify(payload.Field("snapshot"))), payload.Field("snapshot")))
		}
	case 8: // reverse the whole event list
		slices.Reverse(events)
	}
}

func mustParseOrSelf(text string, self jsjson.Value) jsjson.Value {
	if v, err := jsjson.Parse(text); err == nil {
		return v
	}
	return self
}

// integerTurnPayloads reports whether every TURN_TREE_SNAPSHOT payload has a
// string turnId and an integer turnOrdinal. turnStatesFromLedger reads both
// unchecked, but its callers pass only schema-parsed ledgers (positive
// integer ordinals), so a CorruptLeaf 1.5 is outside its domain (State's
// TurnOrdinal is an int).
func integerTurnPayloads(ledger jsjson.Value) bool {
	for _, e := range ledger.Get("events").Items() {
		if e.Get("event", "type").Str() != "TURN_TREE_SNAPSHOT" {
			continue
		}
		p := e.Get("event", "payload")
		n := p.Get("turnOrdinal")
		if p.Get("turnId").Kind() != jsjson.String || n.Kind() != jsjson.Number || n.Num() != math.Trunc(n.Num()) || math.IsInf(n.Num(), 0) {
			return false
		}
	}
	return true
}

var turnTrees = "0123456789abcdef"

func randomTurnStates(r *rand.Rand) []jsjson.Value {
	n := r.IntN(7)
	var out []jsjson.Value
	ordinal := 0
	baseline := r.IntN(4) != 0
	index := 0
	for i := 0; i < n; i++ {
		role, turnID := "TURN", ""
		if i == 0 && baseline {
			role, turnID = "SESSION_BASELINE", turninv.SessionBaselineTurnID
		} else {
			ordinal += 1 + r.IntN(2)*r.IntN(2) // occasional ordinal gap
			if r.IntN(10) == 0 && ordinal > 1 {
				ordinal-- // duplicate ordinal
			}
			turnID = "turn-" + strconv.Itoa(ordinal)
		}
		ord := ordinal
		if role == "SESSION_BASELINE" {
			ord = 0
		}
		var indexValue, ordinalValue jsjson.Value = jsjson.MakeNumber(float64(index)), jsjson.MakeNumber(float64(ord))
		if index == 0 && r.IntN(6) == 0 {
			indexValue = numberSentinel("-0")
		}
		if ord == 0 && r.IntN(6) == 0 {
			ordinalValue = numberSentinel("-0")
		}
		if r.IntN(12) == 0 { // an index or ordinal of 2^53 and beyond
			if r.IntN(2) == 0 {
				indexValue = numberSentinel(bigIntegers[r.IntN(len(bigIntegers))])
			} else {
				ordinalValue = numberSentinel(bigIntegers[r.IntN(len(bigIntegers))])
			}
		}
		out = append(out, jsjson.MustObject(
			"index", indexValue,
			"turnId", jsjson.MakeString(turnID),
			"turnOrdinal", ordinalValue,
			"role", jsjson.MakeString(role),
			"treeDigest", jsjson.MakeString(strings.Repeat(string(turnTrees[r.IntN(16)]), 40)),
			"snapshotDigest", jsjson.MakeString("sha256:"+strings.Repeat(string(turnTrees[r.IntN(16)]), 64)),
			"dirty", jsjson.MakeBool(r.IntN(2) == 0),
		))
		index += 1 + r.IntN(4)/3 // occasional index gap
	}
	if r.IntN(4) == 0 { // the sort keeps indexes, so position need not equal index
		r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	}
	return out
}

func randomTurnRuns(r *rand.Rand, states []jsjson.Value) []jsjson.Value {
	var runs []jsjson.Value
	for _, st := range states {
		verdict := []string{"PASS", "FAIL", "PASS", "FAIL", "ERROR", "INAPPLICABLE"}[r.IntN(6)]
		for attempt := 1; attempt <= 3; attempt++ {
			if r.IntN(14) == 0 {
				continue // dropped run
			}
			a := attempt
			if r.IntN(16) == 0 {
				a = 1 + r.IntN(3) // duplicate attempt
			}
			v := verdict
			if r.IntN(14) == 0 {
				v = []string{"PASS", "FAIL", "ERROR"}[r.IntN(3)] // mixed verdicts
			}
			reason := map[string]string{"PASS": "PREDICATE_PASS", "FAIL": "PREDICATE_FAIL", "ERROR": "HARNESS_ERROR", "INAPPLICABLE": "INCOMPATIBLE_STATE"}[v]
			if r.IntN(12) == 0 {
				reason = []string{"EXIT_ZERO", "EXIT_NONZERO", "TIMEOUT", "PREDICATE_PASS", "PREDICATE_FAIL"}[r.IntN(5)]
			}
			kind, executor := "DOCKER_ISOLATED", "NATIVE_DOCKER"
			if r.IntN(10) == 0 {
				executor = "INJECTED_RUNNER"
			}
			if r.IntN(25) == 0 {
				kind, executor = "UNSAFE_LOCAL", "UNSAFE_LOCAL"
			}
			idx := st.Get("index").Num()
			if st.Get("index").Kind() == jsjson.String {
				idx = 0
			}
			execID := "sha256:" + strings.Repeat(string(turnTrees[int(math.Mod(idx, 16))]), 62) + string(turnTrees[a]) + string(turnTrees[r.IntN(2)])
			if r.IntN(16) == 0 {
				execID = "sha256:" + strings.Repeat("e", 64) // duplicate execution id
			}
			o := jsjson.NewObj()
			o.Set("stateIndex", st.Get("index"))
			o.Set("executionAttempt", jsjson.MakeNumber(float64(a)))
			if a == 1 && r.IntN(20) == 0 {
				o.Set("executionAttempt", numberSentinel("1e0"))
			}
			o.Set("executionId", jsjson.MakeString(execID))
			o.Set("runId", jsjson.MakeString("sha256:"+strings.Repeat(string(turnTrees[r.IntN(16)]), 64)))
			for _, k := range []string{"turnId", "turnOrdinal", "role", "treeDigest", "snapshotDigest"} {
				o.Set(k, st.Get(k))
			}
			switch r.IntN(30) { // mismatched state fields
			case 0:
				o.Set("treeDigest", jsjson.MakeString(strings.Repeat("9", 40)))
			case 1:
				o.Set("turnId", jsjson.MakeString("turn-other"))
			case 2:
				o.Set("turnOrdinal", jsjson.MakeNumber(99))
			case 3:
				o.Set("role", jsjson.MakeString("TURN"))
			case 4:
				o.Set("snapshotDigest", jsjson.MakeString("sha256:"+strings.Repeat("9", 64)))
			case 5:
				o.Set("stateIndex", jsjson.MakeNumber(idx+1))
			}
			o.Set("result", jsjson.MustObject("kind", jsjson.MakeString(kind), "executor", jsjson.MakeString(executor),
				"verdict", jsjson.MakeString(v), "reason", jsjson.MakeString(reason)))
			runs = append(runs, jsjson.MakeObject(o))
		}
	}
	if r.IntN(3) != 0 {
		r.Shuffle(len(runs), func(i, j int) { runs[i], runs[j] = runs[j], runs[i] })
	}
	return runs
}

// handShapedStable is a stable-state list built directly: index gaps, a
// failing or missing baseline, -0 indexes.
func handShapedStable(r *rand.Rand) (states, stable []jsjson.Value) {
	n := 1 + r.IntN(6)
	index, ordinal := 0, 0
	baseline := r.IntN(3) != 0
	for i := 0; i < n; i++ {
		role, turnID, ord := "TURN", "", 0
		if i == 0 && baseline {
			role, turnID = "SESSION_BASELINE", turninv.SessionBaselineTurnID
		} else {
			ordinal++
			ord = ordinal
			turnID = "turn-" + strconv.Itoa(ord)
		}
		tree := strings.Repeat(string(turnTrees[i%16]), 40)
		snap := "sha256:" + strings.Repeat(string(turnTrees[i%16]), 64)
		var indexValue jsjson.Value = jsjson.MakeNumber(float64(index))
		if index == 0 && r.IntN(5) == 0 {
			indexValue = numberSentinel("-0")
		}
		if r.IntN(10) == 0 { // 2^53+1 is 2^53, so x+1 === x past 2^53
			indexValue = numberSentinel(bigIntegers[r.IntN(len(bigIntegers))])
		}
		states = append(states, jsjson.MustObject("index", indexValue, "turnId", jsjson.MakeString(turnID), "turnOrdinal", jsjson.MakeNumber(float64(ord)),
			"role", jsjson.MakeString(role), "treeDigest", jsjson.MakeString(tree), "snapshotDigest", jsjson.MakeString(snap), "dirty", jsjson.MakeBool(false)))
		if r.IntN(6) != 0 { // a state without a stable entry is an observation gap
			ids := []jsjson.Value{}
			for a := 1; a <= 3; a++ {
				ids = append(ids, jsjson.MakeString("sha256:"+strings.Repeat(string(turnTrees[(i+a)%16]), 64)))
			}
			stableIndex := indexValue
			if r.IntN(20) == 0 {
				stableIndex = jsjson.MakeNumber(float64(index + 7)) // a stable state for another index
			}
			stable = append(stable, jsjson.MustObject("stateIndex", stableIndex, "turnId", jsjson.MakeString(turnID), "turnOrdinal", jsjson.MakeNumber(float64(ord)),
				"role", jsjson.MakeString(role), "treeDigest", jsjson.MakeString(tree), "snapshotDigest", jsjson.MakeString(snap),
				"verdict", jsjson.MakeString([]string{"PASS", "FAIL"}[r.IntN(2)]), "executionIds", jsjson.MakeArray(ids), "runIds", jsjson.MakeArray(slices.Clone(ids))))
		}
		index += 1 + r.IntN(5)/4
	}
	return states, stable
}

// turnTransitionsFor is FindTransitions' list, or (sometimes) a hand-made
// one: a PASS_TO_FAIL across a gap, or one whose verdicts contradict its kind.
func turnTransitionsFor(r *rand.Rand, stable []jsjson.Value) []jsjson.Value {
	transitions := turninv.FindTransitions(stable)
	if len(stable) >= 2 && r.IntN(3) == 0 {
		a, b := r.IntN(len(stable)), r.IntN(len(stable))
		hand := jsjson.MustObject("kind", jsjson.MakeString([]string{"PASS_TO_FAIL", "FAIL_TO_PASS"}[r.IntN(2)]), "before", stable[a], "after", stable[b])
		transitions = slices.Insert(transitions, r.IntN(len(transitions)+1), hand)
	}
	return transitions
}

func TestLiveTurnStates(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(43, 3))

	// 1,500 generated ledgers: richLedger and turnLedger sessions (up to 40
	// turns), each mutated up to three times.
	rich := oracleText(t, c, "richLedger", "{}")
	var pool []string
	for i := 0; i < 60; i++ {
		n := r.IntN(41)
		if i < 3 {
			n = 40
		}
		var specs []string
		for k := 1; k <= n; k++ {
			specs = append(specs, `{"ordinal":`+strconv.Itoa(k)+`,"tree":"`+string(turnTrees[r.IntN(16)])+`","dirty":`+strconv.FormatBool(r.IntN(2) == 0)+`}`)
		}
		pool = append(pool, oracleText(t, c, "turnLedger", `{"states":[`+strings.Join(specs, ",")+`],"baseline":`+strconv.FormatBool(r.IntN(5) != 0)+`}`))
	}
	ledgers, statesSeen, threw, bigLedgers, bigPairs := 0, 0, 0, 0, 0
	for ledgers < 1500 {
		text := pool[r.IntN(len(pool))]
		if r.IntN(5) == 0 {
			text = rich
		}
		ledger := mustParseText(t, text)
		for m := r.IntN(4); m > 0; m-- {
			mutateTurnLedger(r, ledger)
		}
		text = withNumberForms(jsjson.Stringify(ledger))
		if !integerTurnPayloads(mustParseText(t, text)) {
			continue
		}
		ledgers++
		if hasBigInteger(text) {
			bigLedgers++
		}
		states, err := turninv.StatesFromLedger(mustParseText(t, text))
		var value jsjson.Value
		if err == nil {
			value = turninv.StatesValue(states)
		} else {
			threw++
		}
		liveCheck(t, c, "turnStatesFromLedger", `{"ledger":`+text+`}`, turnOutcome(value, err))
		if err == nil {
			statesSeen += len(states)
			got := jsjson.MakeNull()
			if d := turninv.DuplicateTurnOrdinal(states); d != "" {
				got = jsjson.MakeString(d)
			}
			liveCheck(t, c, "duplicateTurnOrdinalError", `{"states":`+jsjson.Stringify(value)+`}`, turnOutcome(got, nil))
		}
	}

	// 2,000 (states, runs) pairs, each with requireNativeExecutor true and
	// false; transitions and attribution on every reconstructed list.
	reconstructed, nonEmpty := 0, 0
	for i := 0; i < 2000; i++ {
		stateValues := randomTurnStates(r)
		runs := randomTurnRuns(r, stateValues)
		statesText := withNumberForms(jsjson.Stringify(jsjson.MakeArray(stateValues)))
		runsText := withNumberForms(jsjson.Stringify(jsjson.MakeArray(runs)))
		if hasBigInteger(statesText) {
			bigPairs++
		}
		parsedStates := turninv.StatesFromValue(mustParseText(t, statesText))
		parsedRuns := mustParseText(t, runsText).Items()
		for _, native := range []bool{true, false} {
			stable := turninv.ReconstructStable(parsedStates, parsedRuns, native)
			reconstructed++
			if len(stable) > 0 {
				nonEmpty++
			}
			liveCheck(t, c, "reconstructStableTurnStates",
				`{"states":`+statesText+`,"runs":`+runsText+`,"requireNativeExecutor":`+strconv.FormatBool(native)+`}`,
				turnOutcome(jsjson.MakeArray(stable), nil))
			checkTransitionsAndAttribution(t, c, r, statesText, parsedStates, stable)
		}
	}

	// 300 hand-shaped lists: gaps, a failing or missing baseline, empty or
	// omitted stableStates.
	for i := 0; i < 300; i++ {
		stateValues, stable := handShapedStable(r)
		statesText := withNumberForms(jsjson.Stringify(jsjson.MakeArray(stateValues)))
		stableText := withNumberForms(jsjson.Stringify(jsjson.MakeArray(stable)))
		checkTransitionsAndAttribution(t, c, r, statesText, turninv.StatesFromValue(mustParseText(t, statesText)), mustParseText(t, stableText).Items())
	}

	liveSummary(t, "%d ledgers (%d threw, %d states, %d with integers past 2^53), %d reconstructions (%d non-empty; %d pairs past 2^53), 300 hand-shaped lists",
		ledgers, threw, statesSeen, bigLedgers, reconstructed, nonEmpty, bigPairs)
	requireReached(t, "internal/turninv/states.go", recordedOutputs(t))
}

// checkTransitionsAndAttribution compares findStableTurnTransitions on
// stable, then attributeFailureIntroduction on its transitions (or a
// hand-made list) with stableStates as given, empty or omitted.
func checkTransitionsAndAttribution(t *testing.T, c *oracle.Client, r *rand.Rand, statesText string, states []turninv.State, stable []jsjson.Value) {
	t.Helper()
	stableText := withNumberForms(jsjson.Stringify(jsjson.MakeArray(stable)))
	stable = mustParseText(t, stableText).Items() // -0 forms as TS sees them
	liveCheck(t, c, "findStableTurnTransitions", `{"stable":`+stableText+`}`, turnOutcome(jsjson.MakeArray(turninv.FindTransitions(stable)), nil))
	transitions := turnTransitionsFor(r, stable)
	transitionsText := withNumberForms(jsjson.Stringify(jsjson.MakeArray(transitions)))
	transitions = mustParseText(t, transitionsText).Items()
	args := `{"states":` + statesText + `,"transitions":` + transitionsText
	var given []jsjson.Value
	switch r.IntN(5) {
	case 0: // omitted: the TS default []
	case 1:
		args += `,"stableStates":[]`
	default:
		args += `,"stableStates":` + stableText
		given = stable
	}
	liveCheck(t, c, "attributeFailureIntroduction", args+"}", turnOutcome(turninv.AttributeFailureIntroduction(states, transitions, given), nil))
}

// Inline seeds for TestLiveNamedSchemas until Task 2's turn bases merge
// (they then replace these with turn-two-states/… and turn-non-monotonic/…
// paths). turnResultSeed builds a COMPLETED investigation over a baseline
// and len(verdicts)-1 turns, three native Docker runs per state.
func turnResultSeed(verdicts ...string) jsjson.Value {
	digest := func(c byte) jsjson.Value { return jsjson.MakeString("sha256:" + strings.Repeat(string(c), 64)) }
	image := "registry.example/faultline-node@sha256:" + strings.Repeat("b", 64)
	fingerprint := jsjson.MustObject("schemaVersion", jsjson.MakeString("faultline.environment-fingerprint.v2"),
		"files", jsjson.MustObject("package.json", digest('7')), "digest", digest('8'))
	var states, runs []jsjson.Value
	for i, verdict := range verdicts {
		role, turnID := "TURN", "turn-"+strconv.Itoa(i)
		if i == 0 {
			role, turnID = "SESSION_BASELINE", turninv.SessionBaselineTurnID
		}
		st := turninv.State{Index: float64(i), TurnID: turnID, TurnOrdinal: float64(i), Role: role,
			TreeDigest: strings.Repeat(string(turnTrees[i+1]), 40), SnapshotDigest: "sha256:" + strings.Repeat(string(turnTrees[i+1]), 64), Dirty: i > 0}
		states = append(states, st.Value())
		for attempt := 1; attempt <= 3; attempt++ {
			execID := jsjson.MakeString("sha256:" + strings.Repeat(string(turnTrees[i+1]), 63) + strconv.Itoa(attempt))
			runID := jsjson.MakeString("sha256:" + strings.Repeat("c", 62) + strconv.Itoa(i) + strconv.Itoa(attempt))
			exitCode, reason := 0.0, "PREDICATE_PASS"
			if verdict == "FAIL" {
				exitCode, reason = 1, "PREDICATE_FAIL"
			}
			runs = append(runs, jsjson.MustObject(
				"schemaVersion", jsjson.MakeString(turninv.SchemaVersion), "runId", runID, "executionId", execID,
				"executionNonce", jsjson.MakeString("00000000-0000-4000-8000-"+strings.Repeat("0", 10)+strconv.Itoa(i)+strconv.Itoa(attempt)),
				"stateIndex", jsjson.MakeNumber(float64(i)), "executionAttempt", jsjson.MakeNumber(float64(attempt)),
				"turnId", jsjson.MakeString(st.TurnID), "turnOrdinal", jsjson.MakeNumber(float64(i)), "role", jsjson.MakeString(role),
				"treeDigest", jsjson.MakeString(st.TreeDigest), "snapshotDigest", jsjson.MakeString(st.SnapshotDigest),
				"environmentFingerprintDigest", digest('8'), "frozenDigest", digest('5'), "witnessDigest", digest('6'),
				"startedAt", jsjson.MakeString("2026-07-16T11:03:00.000Z"), "finishedAt", jsjson.MakeString("2026-07-16T11:03:00.000Z"),
				"durationMs", jsjson.MakeNumber(0),
				"overlays", jsjson.MakeArray([]jsjson.Value{jsjson.MustObject("path", jsjson.MakeString("witness.mjs"), "bytesDigest", digest('4'), "bytesLength", jsjson.MakeNumber(56))}),
				"sandbox", jsjson.MustObject("kind", jsjson.MakeString("DOCKER_ISOLATED"), "witnessDigest", digest('5'), "commandDigest", digest('3'),
					"environmentPolicyDigest", digest('2'), "policyDigest", digest('1'),
					"environment", jsjson.MustObject("fixedKeys", stringValues([]string{"CI", "HOME", "LANG", "LC_ALL", "SOURCE_DATE_EPOCH", "TZ"}),
						"allowedKeys", stringValues([]string{"FEATURE_FLAG"}),
						"passed", jsjson.MakeArray([]jsjson.Value{jsjson.MustObject("key", jsjson.MakeString("FEATURE_FLAG"), "valueDigest", digest('9'))}),
						"redactedKeys", stringValues(nil)),
					"runtime", jsjson.MustObject("image", jsjson.MakeString(image), "entrypoint", jsjson.MakeString("/bin/sh"), "network", jsjson.MakeString("none"),
						"rootFilesystemReadOnly", jsjson.MakeBool(true), "user", jsjson.MakeString("65534:65534"), "capDropAll", jsjson.MakeBool(true),
						"noNewPrivileges", jsjson.MakeBool(true), "pull", jsjson.MakeString("never"),
						"limits", jsjson.MustObject("timeoutMs", jsjson.MakeNumber(120000), "maxOutputBytes", jsjson.MakeNumber(1048576), "cpuCount", jsjson.MakeNumber(1),
							"memoryBytes", jsjson.MakeNumber(536870912), "pidsLimit", jsjson.MakeNumber(128), "tmpfsBytes", jsjson.MakeNumber(67108864)))),
				"result", jsjson.MustObject("kind", jsjson.MakeString("DOCKER_ISOLATED"), "executor", jsjson.MakeString("NATIVE_DOCKER"),
					"verdict", jsjson.MakeString(verdict), "reason", jsjson.MakeString(reason), "exitCode", jsjson.MakeNumber(exitCode), "signal", jsjson.MakeNull(),
					"outputTruncated", jsjson.MakeBool(false), "stdoutDigest", digest('a'), "stdoutBytes", jsjson.MakeNumber(5), "stdoutPreview", jsjson.MakeString("state"),
					"stdoutPreviewTruncated", jsjson.MakeBool(false), "stdoutRedacted", jsjson.MakeBool(false), "stderrDigest", digest('e'), "stderrBytes", jsjson.MakeNumber(0),
					"stderrPreview", jsjson.MakeString(""), "stderrPreviewTruncated", jsjson.MakeBool(false), "stderrRedacted", jsjson.MakeBool(false)),
			))
		}
	}
	parsedStates := turninv.StatesFromValue(jsjson.MakeArray(states))
	stable := turninv.ReconstructStable(parsedStates, runs, true)
	transitions := turninv.FindTransitions(stable)
	return jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(turninv.SchemaVersion), "recorder", jsjson.MakeString("codex-turn-tree-replay"),
		"nativeCodexInterception", jsjson.MakeBool(false), "status", jsjson.MakeString("COMPLETED"),
		"repository", jsjson.MakeString("/work/turn-repository"), "ledgerDigest", digest('d'),
		"witness", jsjson.MustObject("valid", jsjson.MakeBool(true), "errors", stringValues(nil), "frozenDigest", digest('5'), "witnessDigest", digest('6'),
			"externalDigestStatus", jsjson.MakeString("MATCH"),
			"approval", jsjson.MustObject("actor", jsjson.MakeString("reviewer@example.test"), "approvedAt", jsjson.MakeString("2026-07-16T11:01:00.000Z"))),
		"executionsPerState", jsjson.MakeNumber(3),
		"states", jsjson.MakeArray(states), "runs", jsjson.MakeArray(runs), "stableStates", jsjson.MakeArray(stable), "transitions", jsjson.MakeArray(transitions),
		"environmentFingerprints", jsjson.MakeArray([]jsjson.Value{fingerprint}), "environmentHomogeneity", jsjson.MakeString("HOMOGENEOUS"),
		"runtimeMapping", jsjson.MustObject(digest('8').Str(), jsjson.MakeString(image)),
		"introduction", turninv.AttributeFailureIntroduction(parsedStates, transitions, stable),
		"nonMonotonic", jsjson.MakeBool(turninv.HasNonMonotonic(transitions)),
		"proof", jsjson.MustObject("requiresDockerIsolation", jsjson.MakeBool(true), "dockerIsolated", jsjson.MakeBool(true),
			"executionTrust", jsjson.MakeString("NATIVE_DOCKER"), "proofTransitions", jsjson.MakeNumber(float64(len(transitions))),
			"isProof", jsjson.MakeBool(true),
			"reason", jsjson.MakeString("Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states."),
			"evidenceGrade", jsjson.MakeString("EXPERIMENTAL_TURN"),
			"evidenceLabel", jsjson.MakeString("Experimental turn-level evidence — not yet a portable proof")),
		"errors", stringValues(nil),
	)
}

var (
	turnTwoStatesSeed    = turnResultSeed("PASS", "FAIL")
	turnNonMonotonicSeed = turnResultSeed("PASS", "FAIL", "PASS")
	turnTwoStatesText    = jsjson.Stringify(turnTwoStatesSeed)
	turnNonMonotonicText = jsjson.Stringify(turnNonMonotonicSeed)
	turnRunSeeds         = []string{jsjson.Stringify(turnTwoStatesSeed.Get("runs").Items()[0]), jsjson.Stringify(turnNonMonotonicSeed.Get("runs").Items()[0])}
	turnTransitionSeed   = jsjson.Stringify(turnTwoStatesSeed.Get("transitions").Items()[0])
	turnStateSeed        = jsjson.Stringify(turnTwoStatesSeed.Get("states").Items()[0])
)

// TestLiveTurnRuntimeMapping checks the inline seeds parse in zod, then
// compares ResultSchema on runtimeMapping records with bad keys and bad
// values in one entry (RecordOf's key-then-value issue order, plan
// correction 13), a __proto__ key and parsed-key output.
func TestLiveTurnRuntimeMapping(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	for name, seeds := range map[string][]string{
		"TurnInvestigationResultSchema":  {turnTwoStatesText, turnNonMonotonicText},
		"TurnInvestigationRunFactSchema": turnRunSeeds,
		"StableTurnTransitionSchema":     {turnTransitionSeed},
		"TurnStateSchema":                {turnStateSeed},
	} {
		for _, seed := range seeds {
			if want := call(t, c, "zodNamed", `{"name":"`+name+`","text":`+jsjson.Quote(seed)+`}`); !want.Field("success").Bool() {
				t.Fatalf("%s seed is not valid for zod: %s", name, want.Field("message").Str())
			}
		}
	}
	good := `"sha256:` + strings.Repeat("8", 64) + `"`
	image := `"registry.example/x@sha256:` + strings.Repeat("b", 64) + `"`
	mappings := []string{
		`{"bad":"also-bad"}`,
		`{"bad":` + image + `,` + good + `:"nope"}`,
		`{"sha256:AA":1,"x":null}`,
		`{"__proto__":` + image + `,` + good + `:` + image + `}`,
		`{"__proto__":"bad"}`,
		`{"2":"x","1":` + image + `,"b":"y"}`,
		`[]`, `null`, `{}`,
	}
	for _, mapping := range mappings {
		v := mustParseText(t, turnTwoStatesText)
		v.Obj().Set("runtimeMapping", mustParseText(t, mapping))
		text := jsjson.Stringify(v)
		want := call(t, c, "zodNamed", `{"name":"TurnInvestigationResultSchema","text":`+jsjson.Quote(text)+`}`)
		out, issues, ok := schema.Parse(turninv.ResultSchema, mustParseText(t, text))
		if ok != want.Field("success").Bool() {
			t.Fatalf("runtimeMapping %s: ok=%v zod=%v %s", mapping, ok, !ok, want.Field("message").Str())
		}
		if !ok {
			if got := schema.ErrorMessage(issues); got != want.Field("message").Str() {
				t.Fatalf("runtimeMapping %s:\ngot  %s\nwant %s", mapping, got, want.Field("message").Str())
			}
		} else if got := canonicalOf(out); got != want.Field("canonical").Str() {
			t.Fatalf("runtimeMapping %s: data %s, zod %s", mapping, got, want.Field("canonical").Str())
		}
	}
}

// turnPick is sandbox_live_test.go's pickOf, which builds on unix only.
func turnPick(r *rand.Rand, xs []string) string { return xs[r.IntN(len(xs))] }

// turnSessionSpec is one generated session for TestLiveInvestigateTurns.
type turnSessionSpec struct {
	label    string
	format   string   // sha1 or sha256
	name     string   // repository directory name ("" for the temp dir itself)
	baseline bool     // the first state is the session baseline
	verdicts []string // state.txt per state: good, bad or broken
	dirty    []bool   // per state: an uncommitted (write-tree) tree
	// package.json changes from changeAt on (heterogeneous) when >= 1;
	// noPackage drops package.json entirely.
	changeAt  int
	noPackage bool
	mapping   string // heterogeneous runtimeMapping: complete, partial, invalid or ""
	image     string // the homogeneous fallback image
	fake      string // fake Docker; "" with injected
	injected  bool   // the state-reading injected runner instead of Docker
	witness   string // "", invalid or mismatch
	duplicate int    // >= 0: one more snapshot for this state's ordinal
	missingAt int    // >= 0: this state's tree is not in the repository
	symlinkAt int    // >= 0: witness.mjs is a symbolic link in this state
	filter    bool   // the first state declares a Git filter attribute
	noLedger  bool   // the ledger path names no file
}

// randomTurnSessionSpec draws a session: 2 to 8 states (sometimes one), a
// baseline or not, dirty turns, the four verdict patterns, heterogeneous
// package.json with each mapping kind, the fake Dockers, an injected
// runner, witness and ledger refusals and materialization errors.
func randomTurnSessionSpec(r *rand.Rand) turnSessionSpec {
	n := 2 + r.IntN(7)
	if r.IntN(20) == 0 {
		n = 1
	}
	spec := turnSessionSpec{format: turnPick(r, []string{"sha1", "sha1", "sha1", "sha256"}), name: turnPick(r, []string{"", "", "repo with space", "仓库"}),
		baseline: r.IntN(4) != 0, image: gitinv.PinnedImageForTest, fake: "pass-fail", duplicate: -1, missingAt: -1, symlinkAt: -1}
	pattern := turnPick(r, []string{"all-pass", "pass-to-fail", "pass-to-fail", "non-monotonic", "all-fail"})
	flip := 1 + r.IntN(max(1, n-1))
	for i := 0; i < n; i++ {
		v := "good"
		switch pattern {
		case "pass-to-fail":
			if i >= flip {
				v = "bad"
			}
		case "non-monotonic":
			v = turnPick(r, []string{"good", "bad"})
		case "all-fail":
			v = "bad"
		}
		spec.verdicts = append(spec.verdicts, v)
		spec.dirty = append(spec.dirty, r.IntN(3) == 0)
	}
	spec.label = pattern
	if n > 1 && r.IntN(5) == 0 {
		spec.changeAt = 1 + r.IntN(n-1)
		spec.mapping = turnPick(r, []string{"complete", "complete", "partial", "invalid", ""})
	} else if r.IntN(10) == 0 {
		spec.noPackage = true
	}
	switch r.IntN(10) {
	case 0:
		spec.fake = turnPick(r, []string{"broken-state", "daemon-down", "flaky-daemon"})
		if spec.fake == "broken-state" && n > 2 {
			spec.verdicts[r.IntN(n)] = "broken"
		}
	case 1:
		spec.fake, spec.injected = "", true
		if n > 2 && r.IntN(2) == 0 {
			spec.verdicts[r.IntN(n)] = "broken"
		}
	}
	switch r.IntN(30) {
	case 0:
		spec.witness = "invalid"
	case 1:
		spec.witness = "mismatch"
	case 2:
		spec.image = "node:22"
	case 3:
		spec.image = ""
	case 4:
		spec.noLedger = true
	}
	if n > 1 && r.IntN(15) == 0 {
		spec.duplicate = r.IntN(n)
	}
	if r.IntN(15) == 0 {
		spec.missingAt = r.IntN(n)
	}
	if n >= 3 && r.IntN(15) == 0 {
		spec.symlinkAt = 1 + r.IntN(n-2)
	}
	spec.filter = r.IntN(30) == 0
	return spec
}

// fixedTurnSessionSpecs reach every status and refusal once, ahead of the
// generated sessions.
func fixedTurnSessionSpecs() []turnSessionSpec {
	base := func(label string, verdicts ...string) turnSessionSpec {
		return turnSessionSpec{label: label, format: "sha1", baseline: true, verdicts: verdicts, dirty: make([]bool, len(verdicts)),
			image: gitinv.PinnedImageForTest, fake: "pass-fail", duplicate: -1, missingAt: -1, symlinkAt: -1}
	}
	with := func(s turnSessionSpec, f func(*turnSessionSpec)) turnSessionSpec { f(&s); return s }
	return []turnSessionSpec{
		with(base("baseline proof", "good", "bad"), func(s *turnSessionSpec) { s.dirty[1] = true }),
		base("later attribution", "good", "good", "good", "bad"),
		with(base("no baseline", "good", "bad", "good"), func(s *turnSessionSpec) { s.baseline = false }),
		with(base("duplicate ordinal", "good", "bad", "bad"), func(s *turnSessionSpec) { s.duplicate = 1 }),
		base("one snapshot", "good"),
		with(base("invalid witness", "good", "bad"), func(s *turnSessionSpec) { s.witness = "invalid" }),
		with(base("mismatched witness", "good", "bad"), func(s *turnSessionSpec) { s.witness = "mismatch" }),
		with(base("heterogeneous absent", "good", "bad"), func(s *turnSessionSpec) { s.changeAt = 1 }),
		with(base("heterogeneous partial", "good", "bad"), func(s *turnSessionSpec) { s.changeAt, s.mapping = 1, "partial" }),
		with(base("heterogeneous complete", "good", "good", "bad"), func(s *turnSessionSpec) { s.changeAt, s.mapping = 2, "complete" }),
		with(base("heterogeneous invalid", "good", "bad"), func(s *turnSessionSpec) { s.changeAt, s.mapping = 1, "invalid" }),
		with(base("missing tree", "good", "bad", "bad"), func(s *turnSessionSpec) { s.missingAt = 1 }),
		with(base("overlay symlink", "good", "good", "bad"), func(s *turnSessionSpec) { s.symlinkAt = 1 }),
		with(base("filter", "good", "bad"), func(s *turnSessionSpec) { s.filter = true }),
		with(base("flaky daemon", "good", "bad", "bad"), func(s *turnSessionSpec) { s.fake = "flaky-daemon" }),
		with(base("daemon down", "good", "bad"), func(s *turnSessionSpec) { s.fake = "daemon-down" }),
		with(base("broken state", "good", "broken", "bad"), func(s *turnSessionSpec) { s.fake = "broken-state" }),
		with(base("injected", "good", "bad"), func(s *turnSessionSpec) { s.fake, s.injected = "", true }),
		with(base("unpinned image", "good", "bad"), func(s *turnSessionSpec) { s.image = "node:22" }),
		with(base("no image", "good", "bad"), func(s *turnSessionSpec) { s.image = "" }),
		with(base("no ledger", "good", "bad"), func(s *turnSessionSpec) { s.noLedger = true }),
	}
}

func turnGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := captureGitCmd(dir, args...).Output()
	if err != nil {
		t.Fatalf("git %q: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// turnPackageJSON is state i's package.json (heterogeneous from changeAt).
func (s turnSessionSpec) packageJSON(i int) string {
	if s.changeAt >= 1 && i >= s.changeAt {
		return `{"name":"fixture","dependencies":{"left-pad":"1.3.0"}}` + "\n"
	}
	return `{"name":"fixture"}` + "\n"
}

// turnPackageFingerprint is the environment fingerprint digest of a tree
// whose only descriptor file is package.json with this content.
func turnPackageFingerprint(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fp, err := gitinv.ComputeFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	return fp.Get("digest").Str()
}

// buildTurnSession makes the repository (one tree per state: a commit, or
// for a dirty state only `git write-tree` of the staged worktree), the
// ledger (the oracle's turnSessionLedger, frozen src/ledger.ts) and the
// request without its runner.
func buildTurnSession(t *testing.T, c *oracle.Client, spec turnSessionSpec, witnesses []jsjson.Value) (jsjson.Value, turninv.Request) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := root
	if spec.name != "" {
		repo = filepath.Join(root, spec.name)
		if err := os.Mkdir(repo, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	turnGit(t, repo, "init", "-q", "--object-format="+spec.format)
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "turn session\n")
	turnGit(t, repo, "add", "-A")
	turnGit(t, repo, "commit", "-q", "-m", "initial")
	var trees []string
	for i, v := range spec.verdicts {
		write("state.txt", v+"\n")
		write("step.txt", strconv.Itoa(i)+"\n")
		if !spec.noPackage {
			write("package.json", spec.packageJSON(i))
		}
		if i == 0 && spec.filter {
			write(".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
		}
		switch i {
		case spec.symlinkAt:
			if err := os.Symlink("state.txt", filepath.Join(repo, "witness.mjs")); err != nil {
				t.Fatal(err)
			}
		case spec.symlinkAt + 1:
			if spec.symlinkAt >= 0 {
				os.Remove(filepath.Join(repo, "witness.mjs"))
			}
		}
		turnGit(t, repo, "add", "-A")
		if spec.dirty[i] {
			trees = append(trees, turnGit(t, repo, "write-tree"))
		} else {
			turnGit(t, repo, "commit", "-q", "--allow-empty", "-m", "state "+strconv.Itoa(i))
			trees = append(trees, turnGit(t, repo, "rev-parse", "HEAD^{tree}"))
		}
	}
	if spec.missingAt >= 0 {
		trees[spec.missingAt] = strings.Repeat("e", len(trees[spec.missingAt]))
	}
	head := turnGit(t, repo, "rev-parse", "HEAD")
	snapshot := func(i int) string {
		return `{"tree":` + jsjson.Quote(trees[i]) + `,"dirty":` + strconv.FormatBool(spec.dirty[i]) + `}`
	}
	baseline := "null"
	var states []string
	for i := range spec.verdicts {
		if i == 0 && spec.baseline {
			baseline = snapshot(0)
			continue
		}
		state := snapshot(i)
		if spec.duplicate == i {
			other := (i + 1) % len(trees)
			state = state[:len(state)-1] + `,"again":` + snapshot(other) + `}`
		}
		states = append(states, state)
	}
	if spec.duplicate == 0 && spec.baseline && len(states) > 0 {
		states[0] = states[0][:len(states[0])-1] + `,"again":` + snapshot(0) + `}`
	}
	ledgerText := oracleText(t, c, "turnSessionLedger", `{"repositoryRoot":`+jsjson.Quote(repo)+`,"headCommit":`+jsjson.Quote(head)+
		`,"baseline":`+baseline+`,"states":[`+strings.Join(states, ",")+`]}`)
	ledgerPath := filepath.Join(root, "ledger.json")
	if !spec.noLedger {
		if err := os.WriteFile(ledgerPath, []byte(jsjson.StringifyIndent(mustParseText(t, ledgerText), "  ")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	frozen := witnesses[0]
	expected := frozen.Get("frozenDigest").Str()
	switch spec.witness {
	case "invalid": // the witness digest no longer matches the record
		frozen = mustParseText(t, jsjson.Stringify(frozen))
		frozen.Obj().Set("witnessDigest", jsjson.MakeString("sha256:"+strings.Repeat("0", 64)))
	case "mismatch":
		expected = witnesses[1].Get("frozenDigest").Str()
	}
	request := turninv.Request{Repository: repo, LedgerPath: ledgerPath, FrozenWitness: frozen, ExpectedFrozenDigest: expected, Image: spec.image}
	o := jsjson.NewObj()
	for _, kv := range []struct {
		k string
		v jsjson.Value
	}{
		{"repository", jsjson.MakeString(repo)}, {"ledgerPath", jsjson.MakeString(ledgerPath)}, {"frozenWitness", frozen},
		{"expectedFrozenDigest", jsjson.MakeString(expected)}, {"image", jsjson.MakeString(spec.image)},
	} {
		o.Set(kv.k, kv.v)
	}
	if spec.changeAt >= 1 && spec.mapping != "" && !spec.noPackage {
		before, after := turnPackageFingerprint(t, spec.packageJSON(0)), turnPackageFingerprint(t, spec.packageJSON(spec.changeAt))
		other := "ghcr.io/faultline/other@sha256:" + strings.Repeat("c", 64)
		switch spec.mapping {
		case "complete":
			request.RuntimeMapping = []gitinv.MappingEntry{{Fingerprint: after, Image: other}, {Fingerprint: before, Image: gitinv.PinnedImageForTest}}
		case "partial":
			request.RuntimeMapping = []gitinv.MappingEntry{{Fingerprint: before, Image: gitinv.PinnedImageForTest}}
		case "invalid":
			request.RuntimeMapping = []gitinv.MappingEntry{{Fingerprint: after, Image: "node:22"}, {Fingerprint: before, Image: gitinv.PinnedImageForTest}}
		}
		mapping := jsjson.NewObj()
		for _, e := range request.RuntimeMapping {
			mapping.Set(e.Fingerprint, jsjson.MakeString(e.Image))
		}
		o.Set("runtimeMapping", jsjson.MakeObject(mapping))
	}
	return jsjson.MakeObject(o), request
}

// turnStateRunner is oracle-turninv.ts's stateRunner.
func turnStateRunner(p sandbox.Plan) (sandbox.RunResult, error) {
	b, err := os.ReadFile(filepath.Join(p.Value.Get("sourceDirectory").Str(), "state.txt"))
	if err != nil {
		return sandbox.RunResult{}, err
	}
	state := jsstr.Trim(jsstr.DecodeUTF8(b))
	result := func(outcome string) string {
		return "state=" + state + "\n" + `FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"` + outcome + `"}` + "\n"
	}
	zero, one := 0, 1
	switch state {
	case "broken":
		return sandbox.RunResult{ExitCode: &one, Stderr: "witness.mjs:3\nSyntaxError: Unexpected token '}'\n"}, nil
	case "bad":
		return sandbox.RunResult{ExitCode: &one, Stdout: result("PREDICATE_FAIL"), Stderr: "witness failed"}, nil
	}
	return sandbox.RunResult{ExitCode: &zero, Stdout: result("PREDICATE_PASS")}, nil
}

// TestLiveInvestigateTurns runs investigateTurnTrees and
// turninv.InvestigateTurnTrees on 21 fixed sessions and 99 generated ones
// (randomTurnSessionSpec) under one seed, with the same fake Docker behind
// the git and docker recorders, and compares the whole results (nonces,
// times and the seeded mkdtemp name included) and the recorded argv.
// FAULTLINE_TURNINV_CASES=first:end compares only sessions [first, end)
// (each has its own generator seed); the status and requireReached checks
// need all 120.
func TestLiveInvestigateTurns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Dockers and recorders are sh scripts")
	}
	root := oracle.RepoRoot(t)
	oracle.Skip(t)
	clock.ResetForTest("live-investigate-turns-witnesses")
	witnesses := []jsjson.Value{
		gitinv.FrozenWitnessForTest(t),
		gitinv.FrozenWitnessWithOverlaysForTest(t, []string{"notes.txt"}, []string{"overlay notes\n"}),
	}
	const total = 120
	first, end := 0, total
	if window := os.Getenv("FAULTLINE_TURNINV_CASES"); window != "" {
		a, b, ok := strings.Cut(window, ":")
		var errA, errB error
		first, errA = strconv.Atoi(a)
		end, errB = strconv.Atoi(b)
		if !ok || errA != nil || errB != nil || first < 0 || first >= end || end > total {
			t.Fatalf("FAULTLINE_TURNINV_CASES=%q: want first:end with 0 <= first < end <= %d", window, total)
		}
	}
	full := first == 0 && end == total
	fixed := fixedTurnSessionSpecs()
	specAt := func(i int) turnSessionSpec {
		if i < len(fixed) {
			return fixed[i]
		}
		spec := randomTurnSessionSpec(rand.New(rand.NewPCG(61, uint64(i))))
		return spec
	}

	const seed = "live-investigate-turns"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	// tsx reads the seeded Date once while it starts (TestLiveInvestigate).
	oracleCall(t, c, "echo", "null")
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]int{}
	transitions, proofs, runs := 0, 0, 0
	for i := first; i < end; i++ {
		spec := specAt(i)
		label := spec.label + " " + spec.format + " n=" + strconv.Itoa(len(spec.verdicts)) + " " + strings.Join(spec.verdicts, ",")
		requestValue, request := buildTurnSession(t, c, spec, witnesses)
		fake := spec.fake
		if fake == "" {
			fake = "pass-fail"
		}
		path := strings.Join([]string{filepath.Join(root, "difftest/testdata/recorder"), filepath.Join(root, "difftest/testdata/fakedocker", fake),
			filepath.Dir(gitPath), "/usr/bin", "/bin"}, ":")
		// flaky-daemon counts runs in a file beside the script: each side
		// runs a fresh private copy.
		fresh := func() {}
		if fake == "flaky-daemon" {
			private := filepath.Join(logs, strconv.Itoa(i)+"-fake")
			fresh = func() {
				os.RemoveAll(private)
				if err := os.Mkdir(private, 0o755); err != nil {
					t.Fatal(err)
				}
				for name, from := range map[string]string{"docker": "flaky-daemon", "odd-bad": "odd-bad"} {
					script, err := os.ReadFile(filepath.Join(root, "difftest/testdata/fakedocker", from, "docker"))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(private, name), script, 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			path = strings.Join([]string{filepath.Join(root, "difftest/testdata/recorder"), private, filepath.Dir(gitPath), "/usr/bin", "/bin"}, ":")
		}
		tsLog, goLog := filepath.Join(logs, strconv.Itoa(i)+"-ts.log"), filepath.Join(logs, strconv.Itoa(i)+"-go.log")
		// Both clocks must still be in step: each side reads one tick here.
		if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
			t.Fatalf("case %d: oracle clock at %s, Go clock at %d", i, ts, goTick)
		}
		fresh()
		want := oracleText(t, c, "investigateTurnTrees", `{"request":`+jsjson.Stringify(requestValue)+`,"path":`+jsjson.Quote(path)+
			`,"argvLog":`+jsjson.Quote(tsLog)+`,"injected":`+strconv.FormatBool(spec.injected)+`}`)
		fresh()
		t.Setenv("PATH", path)
		t.Setenv("FAULTLINE_ARGV_LOG", goLog)
		if spec.injected {
			request.Runner, request.Injected = turnStateRunner, true
		}
		result, err := turninv.InvestigateTurnTrees(request)
		got := ""
		if err != nil {
			got = "THREW:" + err.Error()
		} else {
			got = jsjson.Stringify(result)
		}
		if got != want {
			t.Fatalf("case %d (%s):\ngot  %s\nwant %s", i, label, got, want)
		}
		recordOutput(t, want)
		tsRecords, _ := os.ReadFile(tsLog)
		goRecords, _ := os.ReadFile(goLog)
		if string(tsRecords) != string(goRecords) {
			t.Fatalf("case %d (%s): git/docker argv differ:\ngo %q\nts %q", i, label, goRecords, tsRecords)
		}
		if err != nil {
			statuses["THREW"]++
			t.Logf("case %d (%s): %s", i, label, got)
			continue
		}
		status := result.Get("status").Str()
		statuses[status]++
		transitions += len(result.Get("transitions").Items())
		runs += len(result.Get("runs").Items())
		if result.Get("proof", "isProof").Bool() {
			proofs++
		}
		t.Logf("case %d (%s): %s, %d runs, %d transitions, %s, proof %v", i, label, status, len(result.Get("runs").Items()),
			len(result.Get("transitions").Items()), result.Get("introduction", "status").Str(), result.Get("proof", "isProof").Bool())
	}
	liveSummary(t, "%d sessions [%d, %d) compared, statuses %v, %d runs, %d transitions, %d proofs", end-first, first, end, statuses, runs, transitions, proofs)
	if !full {
		return
	}
	for _, status := range []string{"COMPLETED", "INVALID_WITNESS", "NO_TURN_SNAPSHOTS", "EXECUTION_ERROR", "ENVIRONMENT_CHANGED",
		"DUPLICATE_TURN_SNAPSHOTS", "SANDBOX_UNAVAILABLE", "THREW"} {
		if statuses[status] == 0 {
			t.Errorf("no session reached %s", status)
		}
	}
	requireReached(t, "internal/turninv/investigate.go", recordedOutputs(t))
	requireReached(t, "internal/turninv/schemas.go", recordedOutputs(t))
}
