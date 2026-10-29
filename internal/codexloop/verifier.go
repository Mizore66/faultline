package codexloop

import (
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
)

// VerifyCandidate ports the verifyCandidate hook of src/codex-loop.ts:336
// (RepairCandidateVerifier, :199). A non-nil error is the verifier's
// rejected promise: TS reads and parses the bundle's investigation.json and
// witness/frozen.json before its try, so those failures escape it.
type VerifyCandidate func(worktree, baseCommit, patchPath, bundleDirectory string) (prevention.Verdict, error)

// digestPinnedImage ports the /@sha256:[a-f0-9]{64}$/ of src/codex-loop.ts:225.
var digestPinnedImage = jsre.MustCompile(`@sha256:[a-f0-9]{64}$`, "")

// readInvestigationFromBundle ports src/codex-loop.ts:186
// readInvestigationFromBundle.
func readInvestigationFromBundle(bundleDirectory string) (jsjson.Value, error) {
	return bundle.ParseFile(nodefs.Join(nodefs.Resolve(bundleDirectory), "investigation.json"), gitproof.GitInvestigationResultSchema)
}

// FrozenWitnessVerifier ports createFrozenWitnessRepairVerifier
// (src/codex-loop.ts:211). A nil runner is the native Docker runner
// (executor NATIVE_DOCKER); a caller-supplied one sets injected
// (INJECTED_RUNNER).
func FrozenWitnessVerifier(runner sandbox.Runner, injected bool) VerifyCandidate {
	return func(worktree, baseCommit, patchPath, bundleDirectory string) (prevention.Verdict, error) {
		_ = baseCommit
		_ = patchPath
		investigation, err := readInvestigationFromBundle(bundleDirectory)
		if err != nil {
			return prevention.Verdict{}, err
		}
		frozen, err := bundle.ParseFile(nodefs.Join(nodefs.Resolve(bundleDirectory), "witness", "frozen.json"), gitproof.FrozenWitnessSchema)
		if err != nil {
			return prevention.Verdict{}, err
		}
		image := ""
		found := false
		for _, run := range investigation.Get("runs").Items() {
			value := run.Get("sandbox", "runtime", "image")
			if value.Kind() == jsjson.String && digestPinnedImage.Test(value.Str()) {
				image, found = value.Str(), true
				break
			}
		}
		if !found {
			return prevention.Verdict{Detail: "Repair verification fail-closed: proof bundle runs do not record a digest-pinned Docker image."}, nil
		}
		var verdict prevention.Verdict
		if err := jsexc.Try(func() {
			jsexc.Must(gitinv.MaterializeFrozenOverlays(worktree, frozen, nil))
			request := jsjson.MustObject(
				"witness", jsjson.MustObject("digest", frozen.Get("frozenDigest"), "command", frozen.Get("proposal", "witness", "command")),
				"sourceDirectory", jsjson.MakeString(worktree),
				"mode", jsjson.MakeString("DOCKER_ISOLATED"),
				"image", jsjson.MakeString(image),
			)
			plan := jsexc.Must(sandbox.CreatePlan(request))
			sandbox.AuditPlan(plan)
			run := runner
			if run == nil {
				run = sandbox.NativeRunner()
			}
			execution := sandbox.Execute(plan, run, injected)
			if execution.Get("verdict").Str() == "PASS" && execution.Get("reason").Str() == "PREDICATE_PASS" {
				executionID := jsexc.Must(canonical.DigestJSON(jsjson.MustObject(
					"kind", jsjson.MakeString("faultline.repair-verify-execution.v1"),
					"executor", execution.Get("executor"),
					"verdict", execution.Get("verdict"),
					"reason", execution.Get("reason"),
					"policyDigest", execution.Get("policyDigest"),
					"environmentPolicyDigest", execution.Get("environmentPolicyDigest"),
					"exitCode", execution.Get("exitCode"),
					"stdoutDigest", jsjson.MakeString("sha256:"+canonical.SHA256Hex(execution.Get("stdout").Str())),
					"stderrDigest", jsjson.MakeString("sha256:"+canonical.SHA256Hex(execution.Get("stderr").Str())),
					"worktree", jsjson.MakeString(worktree),
					"frozenDigest", frozen.Get("frozenDigest"),
				)))
				verdict = prevention.Verdict{OK: true, Detail: "Frozen witness PREDICATE_PASS on repaired worktree (" + execution.Get("executor").Str() + ").",
					ExecutionID: executionID, HasExecutionID: true}
				return
			}
			verdict = prevention.Verdict{Detail: "Frozen witness did not PREDICATE_PASS (verdict=" + execution.Get("verdict").Str() + ", reason=" + execution.Get("reason").Str() + ")."}
		}); err != nil {
			return prevention.Verdict{Detail: "Repair verification fail-closed: " + jsexc.Message(err)}, nil
		}
		return verdict, nil
	}
}
