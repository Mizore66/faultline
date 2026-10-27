package cli

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/minimize"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
)

// turnMinEvidenceGradeNote is minimizeFromTurnInvestigation's
// evidenceGradeNote (src/cli-app.ts:2285).
const turnMinEvidenceGradeNote = "Counterfactual minimization from a turn boundary reuses Git-path machinery on synthetic commits. The parent turn package remains EXPERIMENTAL_TURN until TURN_PROOF promotion criteria are met."

// turnMinOptions are minimizeFromTurnInvestigation's options
// (cli-app.ts:2239); nil pointers are undefined.
type turnMinOptions struct {
	repository, expectedFrozenDigest, image string
	transitions                             []jsjson.Value
	frozenWitness                           jsjson.Value
	transitionIndex, maxExecutions, output  *string
}

// minimizeFromTurnInvestigation ports cli-app.ts:2239-2288: the returned
// value is the object TS returns, in its key order.
func minimizeFromTurnInvestigation(o turnMinOptions) (jsjson.Value, error) {
	transitionIndex := 0.0
	if o.transitionIndex != nil {
		transitionIndex = jsjson.ToNumber(*o.transitionIndex)
	}
	if math.IsInf(transitionIndex, 0) || math.IsNaN(transitionIndex) || transitionIndex != math.Trunc(transitionIndex) || transitionIndex < 0 {
		return jsjson.Value{}, errors.New("--transition must be a non-negative integer.")
	}
	bridge, err := minimize.BridgeTurnTransition(o.repository, o.transitions, transitionIndex)
	if err != nil {
		return jsjson.Value{}, err
	}
	request := jsjson.NewObj()
	request.Set("repository", jsjson.MakeString(o.repository))
	request.Set("before", jsjson.MakeString(bridge.BeforeCommit))
	request.Set("after", jsjson.MakeString(bridge.AfterCommit))
	request.Set("frozenWitness", o.frozenWitness)
	request.Set("expectedFrozenDigest", jsjson.MakeString(o.expectedFrozenDigest))
	request.Set("sandbox", obj("mode", "DOCKER_ISOLATED", "image", o.image))
	if o.maxExecutions != nil {
		request.Set("budget", jsjson.MustObject("maxExecutions", jsjson.MakeNumber(jsjson.ToNumber(*o.maxExecutions))))
	}
	minimization, err := minimize.MinimizeGitDiff(jsjson.MakeObject(request), sandbox.NativeRunner(), false)
	if err != nil {
		return jsjson.Value{}, err
	}
	var output string
	if o.output != nil {
		output = nodefs.Resolve(*o.output)
	} else {
		// String(transitionIndex).padStart(4, "0"): ASCII, so bytes are units.
		index := jsjson.FormatNumber(transitionIndex)
		if len(index) < 4 {
			index = strings.Repeat("0", 4-len(index)) + index
		}
		output = nodefs.Resolve(nodefs.Join(".faultline", "minimizations",
			"turn-transition-"+index+"-"+strconv.FormatInt(clock.NowMillis(), 10)+".json"))
	}
	path, resultDigest, err := minimize.WriteResult(output, minimization)
	if err != nil {
		return jsjson.Value{}, err
	}
	return obj(
		"bridge", jsjson.MustObject(
			"transitionIndex", jsjson.MakeNumber(bridge.TransitionIndex),
			"beforeTree", jsjson.MakeString(bridge.BeforeTree),
			"afterTree", jsjson.MakeString(bridge.AfterTree),
			"beforeCommit", jsjson.MakeString(bridge.BeforeCommit),
			"afterCommit", jsjson.MakeString(bridge.AfterCommit),
			"note", jsjson.MakeString(bridge.Note),
		),
		"result", minimization,
		"path", path,
		"resultDigest", resultDigest,
		"evidenceGradeNote", turnMinEvidenceGradeNote,
	), nil
}
