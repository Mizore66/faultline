package prevention

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// executionIDPattern is collectRepairedPreventionRuns' content-bound
// execution id (src/prevention-from-artifacts.ts:507).
var executionIDPattern = jsre.MustCompile(`^sha256:[a-f0-9]{64}$`, "")

// Verdict is the verifyOnce result collectRepairedPreventionRuns consumes:
// { ok, detail, executionId? }; ExecutionID is set only when
// HasExecutionID.
type Verdict struct {
	OK             bool
	Detail         string
	ExecutionID    string
	HasExecutionID bool
}

// JSON is the verdict as a JS object, keys in TS order: ok, detail,
// executionId when present.
func (v Verdict) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("ok", jsjson.MakeBool(v.OK))
	o.Set("detail", jsjson.MakeString(v.Detail))
	if v.HasExecutionID {
		o.Set("executionId", jsjson.MakeString(v.ExecutionID))
	}
	return jsjson.MakeObject(o)
}

// CollectRepairedRuns ports collectRepairedPreventionRuns
// (src/prevention-from-artifacts.ts:486): witness/frozen.json and then
// investigation.json are parsed (a failure is err, the TS throw), the
// environment must be HOMOGENEOUS with one digest, and verifyOnce runs up
// to three times, each PASS with a content-bound execution id becoming a
// run binding (keys runId, executionId, commit, tree, verdict,
// witnessDigest, environmentDigest, executionTrust, executionKind). A
// refusal is reasons (TS { ok: false }); a verifyOnce error is err.
// worktreePath is accepted and unused, as in TS.
func CollectRepairedRuns(worktreePath, bundleDirectory, repairedCommit, repairedTree string, verifyOnce func() (Verdict, error)) (runs jsjson.Value, reasons []string, err error) {
	_ = worktreePath
	frozen, err := bundle.ParseFile(nodefs.Join(nodefs.Resolve(bundleDirectory), "witness", "frozen.json"), gitproof.FrozenWitnessSchema)
	if err != nil {
		return jsjson.Value{}, nil, err
	}
	investigation, err := bundle.ParseFile(nodefs.Join(nodefs.Resolve(bundleDirectory), "investigation.json"), gitproof.GitInvestigationResultSchema)
	if err != nil {
		return jsjson.Value{}, nil, err
	}
	environmentDigest, ok := requireHomogeneousEnvironmentDigest(investigation)
	if !ok {
		return jsjson.Value{}, []string{"Cannot collect repaired runs without a homogeneous environment digest."}, nil
	}

	witnessDigest := frozen.Get("witnessDigest")
	count := strconv.Itoa(stableExecutionCount)
	var collected []jsjson.Value
	var runIDs []string
	for attempt := 1; attempt <= stableExecutionCount; attempt++ {
		verdict, err := verifyOnce()
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		n := strconv.Itoa(attempt)
		if !verdict.OK {
			return jsjson.Value{}, []string{jsexc.Concat("Repaired-state execution ", n, "/", count, " failed: ", verdict.Detail)}, nil
		}
		if !verdict.HasExecutionID || !executionIDPattern.Test(verdict.ExecutionID) {
			return jsjson.Value{}, []string{jsexc.Concat("Repaired-state execution ", n, "/", count, " omitted a content-bound executionId; refusing PREVENTION_VERIFIED placeholder synthesis.")}, nil
		}
		executionID := jsjson.MakeString(verdict.ExecutionID)
		unsigned := jsjson.NewObj()
		unsigned.Set("schemaVersion", jsjson.MakeString("faultline.prevention-repaired-run.v1"))
		unsigned.Set("attempt", jsjson.MakeNumber(float64(attempt)))
		unsigned.Set("commit", jsjson.MakeString(repairedCommit))
		unsigned.Set("tree", jsjson.MakeString(repairedTree))
		unsigned.Set("witnessDigest", witnessDigest)
		unsigned.Set("environmentDigest", jsjson.MakeString(environmentDigest))
		unsigned.Set("executionId", executionID)
		unsigned.Set("detail", jsjson.MakeString(verdict.Detail))
		runID, err := canonical.DigestJSON(jsjson.MakeObject(unsigned))
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		run := jsjson.NewObj()
		run.Set("runId", jsjson.MakeString(runID))
		run.Set("executionId", executionID)
		run.Set("commit", jsjson.MakeString(repairedCommit))
		run.Set("tree", jsjson.MakeString(repairedTree))
		run.Set("verdict", jsjson.MakeString("PASS"))
		run.Set("witnessDigest", witnessDigest)
		run.Set("environmentDigest", jsjson.MakeString(environmentDigest))
		run.Set("executionTrust", jsjson.MakeString("NATIVE_DOCKER"))
		run.Set("executionKind", jsjson.MakeString("EXECUTED"))
		collected = append(collected, jsjson.MakeObject(run))
		runIDs = append(runIDs, runID)
	}
	// src/prevention-from-artifacts.ts:535: cannot fire, because attempt is
	// part of each digested object, so the three run ids always differ.
	distinct := map[string]bool{}
	for _, id := range runIDs {
		distinct[id] = true
	}
	if len(distinct) != stableExecutionCount {
		return jsjson.Value{}, []string{"Collected repaired run digests were not distinct."}, nil
	}
	return jsjson.MakeArray(collected), nil, nil
}
