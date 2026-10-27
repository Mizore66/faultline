package prevention

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// ArtifactOptions are buildPreventionProofFromVerifiedArtifacts' options as
// `fl prevention write --from-bundle` passes them
// (src/prevention-from-artifacts.ts:163, src/cli-app.ts:2603): RepairedRuns
// is the --repaired-runs document's `runs` array (any JSON values), and
// CodexThreadID is set only when HasThreadID. RepairBaseTree and
// HardGuardArtifactDigests are set only when their Has… field is true (false
// is TS `undefined`); the CLI passes neither.
type ArtifactOptions struct {
	Bundle, ExpectedRoot         string
	RepairedRuns                 jsjson.Value
	RepairedCommit, RepairedTree string
	PatchDigest, CodexThreadID   string
	HasThreadID                  bool
	RepairBaseTree               string
	HasRepairBaseTree            bool
	HardGuardArtifactDigests     []string
	HasHardGuardArtifactDigests  bool
}

// verifyGitBundle is verifyGitInvestigationProofBundle; tests memoize it
// (MemoizeGitVerifyForTest).
var verifyGitBundle = gitproof.Verify

// stableExecutionCount is STABLE_EXECUTION_COUNT.
const stableExecutionCount = gitproof.StableExecutionCount

// Port of src/prevention-from-artifacts.ts:70 requireHomogeneousEnvironmentDigest.
func requireHomogeneousEnvironmentDigest(investigation jsjson.Value) (string, bool) {
	if investigation.Get("environment", "homogeneity").Str() != "HOMOGENEOUS" {
		return "", false
	}
	digests := investigation.Get("environment", "distinctDigests").Items()
	if len(digests) != 1 {
		return "", false
	}
	return digests[0].Str(), true
}

// Port of src/prevention-from-artifacts.ts:76 assertNativeDockerPassFail.
func assertNativeDockerPassFail(run jsjson.Value, commit, tree, verdict, witnessDigest, frozenDigest string) string {
	runID := run.Get("runId").Str()
	result := run.Get("result")
	switch {
	case run.Get("commit").Str() != commit || run.Get("tree").Str() != tree:
		return jsexc.Concat("run ", runID, " commit/tree does not match stable state")
	case run.Get("witnessDigest").Str() != witnessDigest || run.Get("frozenDigest").Str() != frozenDigest:
		return jsexc.Concat("run ", runID, " witness digests do not match the frozen witness")
	case result.Get("executor").Str() != "NATIVE_DOCKER":
		return jsexc.Concat("run ", runID, " is not NATIVE_DOCKER")
	case result.Get("kind").Str() != "DOCKER_ISOLATED":
		return jsexc.Concat("run ", runID, " is not DOCKER_ISOLATED")
	case result.Get("verdict").Str() != verdict:
		return jsexc.Concat("run ", runID, " verdict ", result.Get("verdict").Str(), " !== ", verdict)
	case verdict == "PASS" && result.Get("reason").Str() != "PREDICATE_PASS":
		return jsexc.Concat("run ", runID, " PASS reason must be PREDICATE_PASS")
	case verdict == "FAIL" && result.Get("reason").Str() != "PREDICATE_FAIL":
		return jsexc.Concat("run ", runID, " FAIL reason must be PREDICATE_FAIL")
	}
	return ""
}

// Port of src/prevention-from-artifacts.ts:115 bindStableState: the bound
// state, or the refusal.
func bindStableState(state, investigation, frozen jsjson.Value, environmentDigest, expectedVerdict string) (jsjson.Value, string) {
	commit := state.Get("commit").Str()
	if verdict := state.Get("verdict").Str(); verdict != expectedVerdict {
		return jsjson.Value{}, jsexc.Concat("stable state at ", commit, " has verdict ", verdict, ", expected ", expectedVerdict)
	}
	runIDs := state.Get("runIds").Items()
	if len(runIDs) != stableExecutionCount {
		return jsjson.Value{}, jsexc.Concat("stable state ", commit, " lacks ", strconv.Itoa(stableExecutionCount), " run IDs") // unreachable (StableGitStateSchema has exactly three runIds)
	}
	for _, id := range runIDs {
		var run jsjson.Value
		found := false
		for _, entry := range investigation.Get("runs").Items() {
			if entry.Get("runId").Str() == id.Str() {
				run, found = entry, true
				break
			}
		}
		if !found {
			return jsjson.Value{}, jsexc.Concat("run ID ", id.Str(), " is not present in the proof bundle")
		}
		if mismatch := assertNativeDockerPassFail(run, commit, state.Get("tree").Str(), expectedVerdict,
			frozen.Get("witnessDigest").Str(), frozen.Get("frozenDigest").Str()); mismatch != "" {
			return jsjson.Value{}, mismatch
		}
	}
	o := jsjson.NewObj()
	o.Set("commit", state.Get("commit"))
	o.Set("tree", state.Get("tree"))
	o.Set("witnessDigest", frozen.Get("witnessDigest"))
	o.Set("environmentDigest", jsjson.MakeString(environmentDigest))
	o.Set("executionTrust", jsjson.MakeString("NATIVE_DOCKER"))
	o.Set("executionKind", jsjson.MakeString("EXECUTED"))
	o.Set("distinctExecutionCount", jsjson.MakeNumber(stableExecutionCount))
	o.Set("runIds", jsjson.MakeArray([]jsjson.Value{runIDs[0], runIDs[1], runIDs[2]}))
	return jsjson.MakeObject(o), ""
}

// member is `v.<name>` of a JSON value, throwing V8's TypeError on null.
func member(v jsjson.Value, name string) jsjson.Value {
	if v.Kind() == jsjson.Null {
		bundle.Throw(errors.New(jsexc.Concat("Cannot read properties of null (reading '", name, "')")))
	}
	return v.Get(name)
}

// isString is `v === s`.
func isString(v jsjson.Value, s string) bool { return v.Kind() == jsjson.String && v.Str() == s }

// BuildFromArtifacts ports buildPreventionProofFromVerifiedArtifacts
// (src/prevention-from-artifacts.ts:163): the grounded write input, or the
// failure reasons. An exception TS lets escape (a null repaired run) is
// thrown (jsexc.Throw), as the CLI's dispatch catches any uncaught TS error.
//
// Failure order:
//  1. gitproof.Verify of the bundle with the expected root, always:
//     ["Proof bundle verification failed", ...errors].
//  2. The zod parse of investigation.json and witness/frozen.json: [message].
//  3. A Docker-isolated proof-grade investigation.
//  4. A HOMOGENEOUS environment with one distinct digest.
//  5. The first PASS_TO_FAIL transition.
//  6. bindStableState of its before (PASS) and after (FAIL) states.
//  7. The repaired runs: exactly three, distinct run IDs, then per run in
//     order its commit/tree, its trust and verdict, its witness and its
//     environment.
//  8. A repaired commit distinct from the last-good and first-bad commits.
//  9. repairBaseTree (options.repairBaseTree ?? the first-bad tree) equal to
//     the first-bad tree.
func BuildFromArtifacts(o ArtifactOptions) (jsjson.Value, []string) {
	bundleDirectory := nodefs.Resolve(o.Bundle)
	verification := verifyGitBundle(bundleDirectory, o.ExpectedRoot, true)
	if !verification.Valid || verification.RootDigest == nil {
		return jsjson.Value{}, append([]string{"Proof bundle verification failed"}, verification.Errors...)
	}

	var investigation, frozen jsjson.Value
	if err := bundle.Try(func() {
		investigation = bundle.Must(bundle.ParseFile(nodefs.Join(nodefs.Resolve(bundleDirectory), "investigation.json"), gitproof.GitInvestigationResultSchema))
		frozen = bundle.Must(bundle.ParseFile(nodefs.Join(nodefs.Resolve(bundleDirectory), "witness", "frozen.json"), gitproof.FrozenWitnessSchema))
	}); err != nil {
		return jsjson.Value{}, []string{jsexc.Message(err)} // coverage gate: race only (verify parsed both just before)
	}

	if !investigation.Get("proof", "isProof").Bool() || investigation.Get("proof", "executionTrust").Str() != "NATIVE_DOCKER" {
		return jsjson.Value{}, []string{"Prevention export requires a Docker-isolated proof-grade Git investigation."}
	}
	environmentDigest, ok := requireHomogeneousEnvironmentDigest(investigation)
	if !ok {
		return jsjson.Value{}, []string{"Prevention export requires a HOMOGENEOUS environment fingerprint digest across states."}
	}
	var introduction jsjson.Value
	found := false
	for _, transition := range investigation.Get("transitions").Items() {
		if transition.Get("kind").Str() == "PASS_TO_FAIL" {
			introduction, found = transition, true
			break
		}
	}
	if !found {
		return jsjson.Value{}, []string{"Prevention export requires a stable PASS_TO_FAIL transition in the proof bundle."}
	}
	before, after := introduction.Get("before"), introduction.Get("after")
	lastGood, refusal := bindStableState(before, investigation, frozen, environmentDigest, "PASS")
	if refusal != "" {
		return jsjson.Value{}, []string{refusal}
	}
	firstBad, refusal := bindStableState(after, investigation, frozen, environmentDigest, "FAIL")
	if refusal != "" {
		return jsjson.Value{}, []string{refusal}
	}

	runs := o.RepairedRuns.Items()
	if len(runs) != stableExecutionCount {
		return jsjson.Value{}, []string{jsexc.Concat("Repaired state requires exactly ", strconv.Itoa(stableExecutionCount), " NATIVE_DOCKER run bindings.")}
	}
	repairedRunIDs := make([]jsjson.Value, len(runs))
	for i, run := range runs {
		repairedRunIDs[i] = member(run, "runId")
	}
	if distinctValues(repairedRunIDs) != stableExecutionCount {
		return jsjson.Value{}, []string{"Repaired run IDs must be distinct."}
	}
	witnessDigest := frozen.Get("witnessDigest").Str()
	for _, run := range runs {
		// `${run.runId}` is evaluated only in the refusal's text.
		runID := func() string { return jsToString(run.Get("runId")) }
		if !isString(run.Get("commit"), o.RepairedCommit) || !isString(run.Get("tree"), o.RepairedTree) {
			return jsjson.Value{}, []string{jsexc.Concat("Repaired run ", runID(), " commit/tree does not match the repaired state.")}
		}
		if !isString(run.Get("verdict"), "PASS") || !isString(run.Get("executionTrust"), "NATIVE_DOCKER") || !isString(run.Get("executionKind"), "EXECUTED") {
			return jsjson.Value{}, []string{jsexc.Concat("Repaired run ", runID(), " is not a NATIVE_DOCKER EXECUTED PASS.")}
		}
		if !isString(run.Get("witnessDigest"), witnessDigest) {
			return jsjson.Value{}, []string{jsexc.Concat("Repaired run ", runID(), " witness digest does not match the frozen witness.")}
		}
		if !isString(run.Get("environmentDigest"), environmentDigest) {
			return jsjson.Value{}, []string{jsexc.Concat("Repaired run ", runID(), " environment digest does not match the proof-bundle environment.")}
		}
	}
	if o.RepairedCommit == before.Get("commit").Str() || o.RepairedCommit == after.Get("commit").Str() {
		return jsjson.Value{}, []string{"Repaired commit must be distinct from last-good and first-bad commits."}
	}
	repairBaseTree := after.Get("tree") // options.repairBaseTree ?? introduction.after.tree
	if o.HasRepairBaseTree {
		repairBaseTree = jsjson.MakeString(o.RepairBaseTree)
	}
	if !isString(after.Get("tree"), repairBaseTree.Str()) {
		return jsjson.Value{}, []string{"repairBaseTree must equal the first-bad state's tree (repair base)."}
	}

	repaired := jsjson.NewObj()
	repaired.Set("commit", jsjson.MakeString(o.RepairedCommit))
	repaired.Set("tree", jsjson.MakeString(o.RepairedTree))
	repaired.Set("witnessDigest", jsjson.MakeString(witnessDigest))
	repaired.Set("environmentDigest", jsjson.MakeString(environmentDigest))
	repaired.Set("executionTrust", jsjson.MakeString("NATIVE_DOCKER"))
	repaired.Set("executionKind", jsjson.MakeString("EXECUTED"))
	repaired.Set("distinctExecutionCount", jsjson.MakeNumber(stableExecutionCount))
	repaired.Set("runIds", jsjson.MakeArray(repairedRunIDs))

	bindings := make([]jsjson.Value, len(runs))
	for i, run := range runs {
		b := jsjson.NewObj()
		b.Set("runId", run.Get("runId"))
		b.Set("executionId", run.Get("executionId"))
		b.Set("commit", run.Get("commit"))
		b.Set("tree", run.Get("tree"))
		b.Set("verdict", jsjson.MakeString("PASS"))
		b.Set("witnessDigest", run.Get("witnessDigest"))
		b.Set("environmentDigest", run.Get("environmentDigest"))
		b.Set("executionTrust", jsjson.MakeString("NATIVE_DOCKER"))
		b.Set("executionKind", jsjson.MakeString("EXECUTED"))
		bindings[i] = jsjson.MakeObject(b)
	}

	input := jsjson.NewObj()
	input.Set("originalProofRoot", jsjson.MakeString(*verification.RootDigest))
	input.Set("frozenWitnessDigest", jsjson.MakeString(witnessDigest))
	input.Set("investigationDigest", jsjson.MakeString(bundle.Must(canonical.DigestJSON(investigation))))
	input.Set("lastGood", lastGood)
	input.Set("firstBad", firstBad)
	input.Set("repaired", jsjson.MakeObject(repaired))
	input.Set("repairPatchDigest", jsjson.MakeString(o.PatchDigest))
	input.Set("repairBaseTree", repairBaseTree)
	input.Set("grounding", jsjson.MakeString("VERIFIED"))
	if o.HasThreadID {
		input.Set("codexThreadId", jsjson.MakeString(o.CodexThreadID))
	}
	if o.HasHardGuardArtifactDigests {
		digests := make([]jsjson.Value, len(o.HardGuardArtifactDigests))
		for i, d := range o.HardGuardArtifactDigests {
			digests[i] = jsjson.MakeString(d)
		}
		input.Set("hardGuardArtifactDigests", jsjson.MakeArray(digests))
	}
	input.Set("repairedRunBindings", jsjson.MakeArray(bindings))
	return jsjson.MakeObject(input), nil
}

// WriteFromArtifacts ports writePreventionProofFromVerifiedArtifacts
// (src/prevention-from-artifacts.ts:303) into output: the written package,
// or the reasons (a writer exception becomes [message]).
func WriteFromArtifacts(o ArtifactOptions, output string) (Written, []string) {
	input, reasons := BuildFromArtifacts(o)
	if reasons != nil {
		return Written{}, reasons
	}
	written, err := Write(input, output)
	if err != nil {
		return Written{}, []string{jsexc.Message(err)}
	}
	return written, nil
}

// DigestRepairPatchFile ports digestRepairPatchFile
// (src/prevention-from-artifacts.ts:327).
func DigestRepairPatchFile(path string) (string, error) {
	if !nodefs.Exists(path) {
		return "", errors.New(jsexc.Concat("Repair patch does not exist: ", path))
	}
	b, err := nodefs.ReadBytes(path)
	if err != nil {
		return "", err
	}
	return gitproof.ByteDigest(b), nil
}

// distinctValues is new Set(values).size: primitives by SameValueZero,
// every object and array distinct.
func distinctValues(values []jsjson.Value) int {
	seen := map[string]bool{}
	objects := 0
	for _, v := range values {
		switch v.Kind() {
		case jsjson.Array, jsjson.Object:
			objects++
		case jsjson.Number:
			seen["n"+jsjson.FormatNumber(v.Num())] = true
		case jsjson.String:
			seen["s"+v.Str()] = true
		case jsjson.Bool:
			seen["b"+strconv.FormatBool(v.Bool())] = true
		case jsjson.Null:
			seen["null"] = true
		default:
			seen["undefined"] = true
		}
	}
	return len(seen) + objects
}

// jsToString is a template literal's ToString of a JSON value: arrays join
// their elements with "," (null and undefined as ""), objects are
// "[object Object]", or V8's TypeError for an object whose own toString
// member (never callable in JSON) leaves no primitive.
func jsToString(v jsjson.Value) string {
	switch v.Kind() {
	case jsjson.Undefined:
		return "undefined"
	case jsjson.Null:
		return "null"
	case jsjson.Bool:
		return strconv.FormatBool(v.Bool())
	case jsjson.Number:
		return jsjson.FormatNumber(v.Num())
	case jsjson.String:
		return v.Str()
	case jsjson.Array:
		parts := make([]string, len(v.Items()))
		for i, item := range v.Items() {
			if item.Kind() != jsjson.Null && item.Kind() != jsjson.Undefined {
				parts[i] = jsToString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	if _, ok := v.Obj().Get("toString"); ok {
		bundle.Throw(errors.New("Cannot convert object to primitive value"))
	}
	return "[object Object]"
}
