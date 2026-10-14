package cli

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/minimize"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/witness"
)

const (
	minimizeVerifyUsage = "Usage: fl minimize verify <result.json> [--expect-digest <sha256:...>]"
	minimizeUsage       = "Usage: fl minimize git|verify ..."

	minimizeVerifyLimitation = "Verification checks the stored minimization record without rerunning Git, Docker, or repository code. An external digest detects rewrites but is not a signature, identity assertion, or host-attestation claim."
	minimizeWriteLimitation  = "Retain resultDigest outside this JSON before relying on it for rewrite detection. It is not a cryptographic signature, identity assertion, or host-attestation claim."
)

// minimizeCommand ports minimizeCommand (src/cli-app.ts:2358).
func minimizeCommand(e *env, args []string) error {
	var first string
	if len(args) > 0 {
		first = args[0]
	}
	if first == "verify" {
		var file string
		if len(args) > 1 {
			file = args[1]
		}
		if file == "" {
			return fail("%s", minimizeVerifyUsage)
		}
		expected, provided := option(args, "--expect-digest")
		verification := minimize.VerifyFile(nodefs.Resolve(file), expected, provided)
		out := jsjson.NewObj()
		out.Set("valid", jsjson.MakeBool(verification.Valid))
		resultDigest := jsjson.MakeNull()
		if verification.ResultDigest != nil {
			resultDigest = jsjson.MakeString(*verification.ResultDigest)
		}
		out.Set("resultDigest", resultDigest)
		out.Set("externalDigestStatus", jsjson.MakeString(verification.ExternalDigestStatus))
		if verification.Result != nil {
			out.Set("status", verification.Result.Get("status"))
			out.Set("proof", verification.Result.Get("proof"))
			out.Set("usedExecutions", verification.Result.Get("budget", "usedExecutions"))
		}
		out.Set("errors", stringList(verification.Errors...))
		out.Set("limitation", jsjson.MakeString(minimizeVerifyLimitation))
		e.printJSON(jsjson.MakeObject(out))
		if !verification.Valid {
			e.exitCode = 1
		}
		return nil
	}
	if first != "git" {
		return fail("%s", minimizeUsage)
	}
	proposalID, err := requiredOption(args, "--proposal")
	if err != nil {
		return err
	}
	maxExecutions, hasMaxExecutions := option(args, "--max-executions")
	repoOption, err := requiredOption(args, "--repo")
	if err != nil {
		return err
	}
	before, err := requiredOption(args, "--before")
	if err != nil {
		return err
	}
	after, err := requiredOption(args, "--after")
	if err != nil {
		return err
	}
	frozenWitness, err := witness.ReadFrozen(witnessStore(args), proposalID)
	if err != nil {
		return err
	}
	expectedFrozenDigest, err := requiredOption(args, "--expect-digest")
	if err != nil {
		return err
	}
	image, err := requiredOption(args, "--image")
	if err != nil {
		return err
	}
	request := jsjson.NewObj()
	request.Set("repository", jsjson.MakeString(nodefs.Resolve(repoOption)))
	request.Set("before", jsjson.MakeString(before))
	request.Set("after", jsjson.MakeString(after))
	request.Set("frozenWitness", frozenWitness)
	request.Set("expectedFrozenDigest", jsjson.MakeString(expectedFrozenDigest))
	request.Set("sandbox", obj("mode", "DOCKER_ISOLATED", "image", image))
	if hasMaxExecutions {
		request.Set("budget", jsjson.MustObject("maxExecutions", jsjson.MakeNumber(jsjson.ToNumber(maxExecutions))))
	}
	result, err := minimize.MinimizeGitDiff(jsjson.MakeObject(request), sandbox.NativeRunner(), false)
	if err != nil {
		return err
	}
	outputFile, ok := option(args, "--output")
	if !ok {
		outputFile = nodefs.Join(".faultline", "minimizations", "minimization-"+strconv.FormatInt(clock.NowMillis(), 10)+".json")
	}
	output, err := safeManagedFileOutput(outputFile, nodefs.Resolve(".faultline", "minimizations"), "Git minimization")
	if err != nil {
		return err
	}
	path, resultDigest, err := minimize.WriteResult(output, result)
	if err != nil {
		return err
	}
	proof := "NOT_CERTIFIED"
	if result.Get("proof", "isProof").Bool() {
		proof = "BIDIRECTIONALLY_CERTIFIED"
	}
	e.printJSON(obj(
		"minimization", result,
		"resultFile", path,
		"resultDigest", resultDigest,
		"proof", proof,
		"verification", "SELF_CONSISTENT",
		"limitation", minimizeWriteLimitation,
	))
	if !result.Get("proof", "isProof").Bool() {
		e.exitCode = 1
	}
	return nil
}
