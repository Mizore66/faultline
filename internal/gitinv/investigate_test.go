//go:build faultline_test && unix

package gitinv

import (
	"os"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/sandbox"
)

func TestInvestigateTwoStatesFindsTransition(t *testing.T) {
	clock.ResetForTest("investigate")
	t.Setenv("PATH", fakeDockerDir(t, "pass-fail")+":"+os.Getenv("PATH"))
	repo, ancestor, descendant := goodThenBadRepo(t) // state.txt: good at ancestor, bad at descendant
	frozen := frozenWitnessFor(t)                    // witness command reads state.txt
	req := jsjson.MustObject(
		"repository", jsjson.MakeString(repo),
		"range", jsjson.MustObject("ancestor", jsjson.MakeString(ancestor), "descendant", jsjson.MakeString(descendant)),
		"frozenWitness", frozen,
		"expectedFrozenDigest", frozen.Get("frozenDigest"),
		"sandbox", jsjson.MustObject("mode", jsjson.MakeString("DOCKER_ISOLATED"), "image", jsjson.MakeString(pinnedImage)),
	)
	res, err := InvestigateGitRange(req, sandbox.NativeRunner(), false)
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Get("transitions").Items()
	if res.Get("status").Str() != "COMPLETED" || len(tr) != 1 || tr[0].Get("kind").Str() != "PASS_TO_FAIL" || !res.Get("proof").Get("isProof").Bool() {
		t.Fatalf("%s", jsjson.Stringify(res))
	}
}

func investigateRequest(repo, ancestor, descendant string, frozen jsjson.Value) jsjson.Value {
	return jsjson.MustObject(
		"repository", jsjson.MakeString(repo),
		"range", jsjson.MustObject("ancestor", jsjson.MakeString(ancestor), "descendant", jsjson.MakeString(descendant)),
		"frozenWitness", frozen,
		"expectedFrozenDigest", frozen.Get("frozenDigest"),
		"sandbox", jsjson.MustObject("image", jsjson.MakeString(pinnedImage)),
	)
}

func TestInvestigateRefusals(t *testing.T) {
	clock.ResetForTest("investigate-refusals")
	repo, ancestor, descendant := goodThenBadRepo(t)
	frozen := frozenWitnessFor(t)

	res, err := InvestigateGitRange(jsjson.MustObject("repository", jsjson.MakeString(repo)), nil, false)
	if err != nil || res.Get("status").Str() != "INVALID_REQUEST" || res.Get("repository").Kind() != jsjson.Null ||
		!strings.HasPrefix(res.Get("errors").Items()[0].Str(), "Invalid Git investigation request: [\n  {\n") {
		t.Fatalf("%v %s", err, jsjson.Stringify(res))
	}

	req := investigateRequest(repo, ancestor, descendant, frozen)
	req.Obj().Set("expectedFrozenDigest", jsjson.MakeString("sha256:"+strings.Repeat("0", 64)))
	res, err = InvestigateGitRange(req, nil, true)
	if err != nil || res.Get("status").Str() != "INVALID_WITNESS" || res.Get("proof", "executionTrust").Str() != "INJECTED_RUNNER" ||
		res.Get("errors").Items()[0].Str() != "externally supplied frozen digest does not match" {
		t.Fatalf("%v %s", err, jsjson.Stringify(res))
	}

	res, err = InvestigateGitRange(investigateRequest(repo, descendant, ancestor, frozen), nil, false)
	want := "Range ancestor " + descendant + " is not an ancestor of descendant " + ancestor + "."
	if err != nil || res.Get("status").Str() != "RANGE_ERROR" || res.Get("errors").Items()[0].Str() != want ||
		res.Get("proof", "reason").Str() != want {
		t.Fatalf("%v %s", err, jsjson.Stringify(res))
	}

	req = investigateRequest(repo, ancestor, descendant, frozen)
	req.Obj().Set("sandbox", jsjson.MustObject())
	if _, err = InvestigateGitRange(req, nil, false); err == nil || err.Error() != "No digest-pinned runtime image is available for this environment fingerprint." {
		t.Fatalf("missing image: %v", err)
	}
}

func TestInvestigateInjectedRunnerIsNeverProof(t *testing.T) {
	clock.ResetForTest("investigate-injected")
	repo, ancestor, descendant := goodThenBadRepo(t)
	frozen := frozenWitnessFor(t)
	runner := func(p sandbox.Plan) (sandbox.RunResult, error) {
		state, _ := os.ReadFile(p.Value.Get("sourceDirectory").Str() + "/state.txt")
		outcome, code := "PREDICATE_PASS", 0
		if strings.TrimSpace(string(state)) == "bad" {
			outcome, code = "PREDICATE_FAIL", 1
		}
		return sandbox.RunResult{ExitCode: &code,
			Stdout: `FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"` + outcome + `"}` + "\n"}, nil
	}
	res, err := InvestigateGitRange(investigateRequest(repo, ancestor, descendant, frozen), runner, true)
	if err != nil {
		t.Fatal(err)
	}
	proof := res.Get("proof")
	if res.Get("status").Str() != "COMPLETED" || len(res.Get("transitions").Items()) != 1 || proof.Get("isProof").Bool() ||
		proof.Get("dockerIsolated").Bool() || proof.Get("evidenceGrade").Str() != "NONE" || len(res.Get("runs").Items()) != 6 {
		t.Fatalf("%s", jsjson.Stringify(res))
	}
	run := res.Get("runs").Items()[0]
	if run.Get("startedAt").Str() == "" || run.Get("durationMs").Num() != 1 || run.Get("result", "executor").Str() != "INJECTED_RUNNER" {
		t.Fatalf("%s", jsjson.Stringify(run))
	}
}

func TestInvestigatePlanErrorStopsAtFirstState(t *testing.T) {
	clock.ResetForTest("investigate-plan-error")
	repo, ancestor, descendant := goodThenBadRepo(t)
	frozen := frozenWitnessFor(t)
	req := investigateRequest(repo, ancestor, descendant, frozen)
	req.Obj().Set("sandbox", jsjson.MustObject("image", jsjson.MakeString(pinnedImage),
		"allowedEnvironment", jsjson.MakeArray([]jsjson.Value{jsjson.MakeString("API_TOKEN")})))
	res, err := InvestigateGitRange(req, func(sandbox.Plan) (sandbox.RunResult, error) { panic("ran") }, true)
	if err != nil {
		t.Fatal(err)
	}
	want := "Could not create sandbox plan for " + ancestor + ": Sandbox refuses secret-like environment name even when allowlisted: API_TOKEN"
	errs := res.Get("errors").Items()
	if res.Get("status").Str() != "CONFIGURATION_ERROR" || len(res.Get("states").Items()) != 2 || len(res.Get("runs").Items()) != 0 ||
		len(res.Get("environment", "fingerprints").Items()) != 1 || len(errs) != 1 || errs[0].Str() != want ||
		res.Get("proof", "reason").Str() != want || res.Get("environment", "homogeneity").Str() != "HOMOGENEOUS" {
		t.Fatalf("%s", jsjson.Stringify(res))
	}
}
