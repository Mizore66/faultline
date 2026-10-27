package minimize

// Port of src/turn-minimization-bridge.ts: turn tree digests become
// synthetic orphan commits so the Git-path minimizer can run on a turn
// boundary.

import (
	"errors"
	"math"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
	s "github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/turninv"
)

// BridgeNote is SyntheticTurnCommits.note (turn-minimization-bridge.ts:118).
const BridgeNote = "Synthetic orphan commits were created from turn tree digests so Git-path counterfactual minimization can run. They are not Codex history and do not promote EXPERIMENTAL_TURN to TURN_PROOF / COMMIT_PROOF."

// bridgeGitObjectIDSchema is GitObjectIdSchema (:9): SHA-1 only, and no
// message, so a miss is zod's "Invalid".
var bridgeGitObjectIDSchema = s.String(s.Regex(s.Pattern(`^[a-f0-9]{40}$`), ""))

// SyntheticTurnCommits ports SyntheticTurnCommits (:11).
type SyntheticTurnCommits struct {
	BeforeCommit, AfterCommit, BeforeTree, AfterTree string
	TransitionIndex                                  float64 // as the caller passed it (-0 stays -0)
	Transition                                       jsjson.Value
	Note                                             string
}

// bridgeRunGit ports runGit (:21-32): spawnSync("git", ["-C", repository,
// ...args], {encoding: "utf8", windowsHide: true, maxBuffer: 16 MiB}) with
// the inherited environment (unhardened), no cwd and no timeout.
func bridgeRunGit(repository string, args []string) (string, error) {
	r := nodeproc.SpawnSyncWith("git", append([]string{"-C", repository}, args...), nodeproc.SpawnSyncOptions{MaxBuffer: 16 << 20})
	if r.Err != nil {
		return "", r.Err
	}
	if r.Status == nil || *r.Status != 0 {
		if detail := jsstr.Trim(jsstr.DecodeUTF8(r.Stderr)); detail != "" {
			return "", errors.New(detail)
		}
		return "", errors.New(jsexc.Concat("git ", jsexc.Join(args, " "), " failed"))
	}
	return jsstr.Trim(jsstr.DecodeUTF8(r.Stdout)), nil
}

// parseBridgeObjectID is GitObjectIdSchema.parse(runGit(...)): a ZodError
// is thrown.
func parseBridgeObjectID(out string, err error) string {
	if err != nil {
		jsexc.Throw(err)
	}
	return jsexc.Must(bundle.ParseValue(jsjson.MakeString(out), bridgeGitObjectIDSchema)).Str()
}

// isJSInteger is Number.isInteger.
func isJSInteger(n float64) bool {
	return !math.IsInf(n, 0) && !math.IsNaN(n) && n == math.Trunc(n)
}

// jsTruthy is a JS truthiness test of a JSON value.
func jsTruthy(v jsjson.Value) bool {
	switch v.Kind() {
	case jsjson.Bool:
		return v.Bool()
	case jsjson.Number:
		return v.Num() != 0 && !math.IsNaN(v.Num())
	case jsjson.String:
		return v.Str() != ""
	case jsjson.Array, jsjson.Object:
		return true
	}
	return false
}

// SelectTurnTransition ports selectTurnTransition (:38).
func SelectTurnTransition(transitions []jsjson.Value, index float64) (jsjson.Value, error) {
	if !isJSInteger(index) || index < 0 {
		return jsjson.Value{}, errors.New(jsexc.Concat("Transition index must be a non-negative integer (got ", jsjson.FormatNumber(index), ")."))
	}
	var transition jsjson.Value
	if index < float64(len(transitions)) {
		transition = transitions[int(index)] // -0 is element 0
	}
	if !jsTruthy(transition) {
		return jsjson.Value{}, errors.New(jsexc.Concat("Turn investigation has no transition at index ", jsjson.FormatNumber(index),
			" (", jsjson.FormatNumber(float64(len(transitions))), " available)."))
	}
	return bundle.ParseValue(transition, turninv.StableTransitionSchema)
}

// slice12 is treeDigest.slice(0, 12), counted in UTF-16 units.
func slice12(value string) string {
	units := jsstr.ToUTF16(value)
	return jsstr.FromUTF16(units[:min(12, len(units))])
}

// bridgeCommitTree ports commitTree (:54-71).
func bridgeCommitTree(repository, treeDigest, message string) string {
	tree := parseBridgeObjectID(bridgeRunGit(repository, []string{"rev-parse", "--verify", "--end-of-options", jsexc.Concat(treeDigest, "^{tree}")}))
	return parseBridgeObjectID(bridgeRunGit(repository, []string{
		"-c", "user.name=FaultLine", "-c", "user.email=faultline@example.invalid", "commit-tree", tree, "-m", message,
	}))
}

// SyntheticCommitsForTurnTransition ports :79.
func SyntheticCommitsForTurnTransition(repository string, transition jsjson.Value, index float64) (c SyntheticTurnCommits, err error) {
	if thrown := jsexc.Try(func() { c, err = syntheticCommitsForTurnTransition(repository, transition, index) }); thrown != nil {
		return SyntheticTurnCommits{}, thrown
	}
	return c, err
}

func syntheticCommitsForTurnTransition(repository string, transition jsjson.Value, index float64) (SyntheticTurnCommits, error) {
	before, after := transition.Get("before"), transition.Get("after")
	if kind := transition.Get("kind").Str(); kind != "PASS_TO_FAIL" {
		return SyntheticTurnCommits{}, errors.New(jsexc.Concat("Counterfactual minimization from a turn package currently requires PASS→FAIL (got ", kind, ")."))
	}
	if before.Get("verdict").Str() != "PASS" || after.Get("verdict").Str() != "FAIL" {
		return SyntheticTurnCommits{}, errors.New("Turn transition endpoints are not a stable PASS→FAIL pair.")
	}

	beforeDigest, afterDigest := before.Get("treeDigest").Str(), after.Get("treeDigest").Str()
	beforeTree := parseBridgeObjectID(bridgeRunGit(repository, []string{"rev-parse", "--verify", "--end-of-options", jsexc.Concat(beforeDigest, "^{tree}")}))
	afterTree := parseBridgeObjectID(bridgeRunGit(repository, []string{"rev-parse", "--verify", "--end-of-options", jsexc.Concat(afterDigest, "^{tree}")}))

	beforeCommit := bridgeCommitTree(repository, beforeTree,
		jsexc.Concat("faultline-turn-min before t", jsjson.FormatNumber(before.Get("turnOrdinal").Num()), " (", slice12(beforeDigest), ")"))
	afterCommit := bridgeCommitTree(repository, afterTree,
		jsexc.Concat("faultline-turn-min after t", jsjson.FormatNumber(after.Get("turnOrdinal").Num()), " (", slice12(afterDigest), ")"))

	return SyntheticTurnCommits{
		BeforeCommit:    beforeCommit,
		AfterCommit:     afterCommit,
		BeforeTree:      beforeTree,
		AfterTree:       afterTree,
		TransitionIndex: index,
		Transition:      transition,
		Note:            BridgeNote,
	}, nil
}

// BridgeTurnTransition ports bridgeTurnTransitionToMinimizationCommits (:123).
func BridgeTurnTransition(repository string, transitions []jsjson.Value, index float64) (SyntheticTurnCommits, error) {
	transition, err := SelectTurnTransition(transitions, index)
	if err != nil {
		return SyntheticTurnCommits{}, err
	}
	return SyntheticCommitsForTurnTransition(repository, transition, index)
}
