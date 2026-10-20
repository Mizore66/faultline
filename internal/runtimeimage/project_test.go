package runtimeimage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

const projectTag = "registry.example/faultline/demo:deps-20260717"

func fixture(t *testing.T) ProjectBuildInput {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ProjectBuildInput{ContextDirectory: dir, Dockerfile: "Dockerfile", ImageTag: "registry.example.test/app:1", Network: "none"}
}

// projectFixture is tests/runtime.test.ts projectBuildFixture with the
// tag the TS cases use; real is the context's realpath and its Dockerfile.
func projectFixture(t *testing.T, network string) (in ProjectBuildInput, real, dockerfile string) {
	in = fixture(t)
	in.ImageTag = projectTag
	in.Network = network
	real, err := nodefs.RealpathSync(in.ContextDirectory)
	if err != nil {
		t.Fatal(err)
	}
	return in, real, filepath.Join(real, "Dockerfile")
}

func describe(t *testing.T, in ProjectBuildInput) jsjson.Value {
	t.Helper()
	plan, err := DescribeProjectBuild(in)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// wantPreparation asserts err is a PreparationError with this code and,
// when message is not empty, this message.
func wantPreparation(t *testing.T, err error, c, message string) {
	t.Helper()
	var pe *PreparationError
	if !errors.As(err, &pe) || pe.Code != c || (message != "" && (pe.Message != message || err.Error() != message)) {
		t.Fatalf("got %v, want %s: %s", err, c, message)
	}
}

func stringsOf(v jsjson.Value) []string {
	var out []string
	for _, item := range v.Items() {
		out = append(out, item.Str())
	}
	return out
}

func TestDescribeCountsDockerfileOnce(t *testing.T) {
	plan, err := DescribeProjectBuild(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Get("review", "fileCount").Num() != 1 {
		t.Fatalf("%v", plan)
	}
	args := plan.Get("command", "arguments").Items()
	if args[0].Str() != "build" || args[5].Str() != "--pull=false" || args[6].Str() != "--network=none" {
		t.Fatalf("%v", args)
	}
}

func TestSymlinkInContextRejected(t *testing.T) {
	in := fixture(t)
	os.Symlink("Dockerfile", filepath.Join(in.ContextDirectory, "link"))
	if _, err := DescribeProjectBuild(in); err == nil {
		t.Fatal("symlink accepted")
	}
}

// tests/runtime.test.ts:283 "describes an explicit, setup-only no-shell
// Docker build before any confirmation".
func TestDescribeSetupOnlyPlan(t *testing.T) {
	in, real, dockerfile := projectFixture(t, "none")
	plan := describe(t, in)
	keys := strings.Join(plan.Obj().Keys(), ",")
	if keys != "kind,setupOnly,contextDirectory,dockerfile,imageTag,network,command,review,effects,description" {
		t.Fatal(keys)
	}
	if plan.Get("kind").Str() != "PROJECT_RUNTIME_IMAGE_BUILD" || !plan.Get("setupOnly").Bool() ||
		plan.Get("contextDirectory").Str() != real || plan.Get("dockerfile").Str() != dockerfile ||
		plan.Get("imageTag").Str() != projectTag || plan.Get("network").Str() != "none" {
		t.Fatal(jsjson.Stringify(plan))
	}
	effects := plan.Get("effects")
	if got := jsjson.Stringify(effects); got != `{"mutatesLocalDockerImageStore":true,"dockerfileInstructionsExecute":true,"buildNetwork":"none","dockerfileRunInstructionsMayAccessNetwork":false,"dockerDaemonNetworkBehaviorNotIndependentlyCertified":true,"mayAccessNetwork":true,"doesNotExecuteProof":true,"requiresDigestPinnedResultForProof":true}` {
		t.Fatal(got)
	}
	review := plan.Get("review")
	if strings.Join(review.Obj().Keys(), ",") != "planDigest,contextDigest,dockerfileDigest,fileCount,totalBytes" {
		t.Fatal(jsjson.Stringify(review))
	}
	for _, k := range []string{"planDigest", "contextDigest", "dockerfileDigest"} {
		if !sha256Digest.Test(review.Get(k).Str()) {
			t.Fatal(k, review.Get(k).Str())
		}
	}
	if review.Get("fileCount").Num() != 1 || review.Get("totalBytes").Num() != 13 {
		t.Fatal(jsjson.Stringify(review))
	}
	if review.Get("dockerfileDigest").Str() != "sha256:"+canonical.SHA256HexBytes([]byte("FROM scratch\n")) {
		t.Fatal(review.Get("dockerfileDigest").Str())
	}
	if !strings.Contains(plan.Get("description").Str(), "Setup only") {
		t.Fatal(plan.Get("description").Str())
	}
	want := `{"executable":"docker","arguments":["build","--file",` + jsjson.Quote(dockerfile) + `,"--tag","` + projectTag + `","--pull=false","--network=none",` + jsjson.Quote(real) + `],"timeoutMs":600000}`
	if got := jsjson.Stringify(plan.Get("command")); got != want {
		t.Fatalf("%s\n%s", got, want)
	}
}

// tests/runtime.test.ts:336 "re-resolves a user-pushed project tag
// read-only without rebuilding or pushing".
func TestResolveProjectImageReadOnly(t *testing.T) {
	repoDigest := "registry.example/faultline/demo@sha256:" + strings.Repeat("f", 64)
	run, calls := recording(ok(`["` + repoDigest + `"]`))
	v, err := ResolveProjectImage(projectTag, run)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsjson.Stringify(v); got != `{"requestedTag":"`+projectTag+`","image":"`+repoDigest+`"}` {
		t.Fatal(got)
	}
	if len(*calls) != 1 || strings.Join((*calls)[0].args, " ") != "image inspect --format {{json .RepoDigests}} "+projectTag ||
		(*calls)[0].timeout != 5*time.Second || (*calls)[0].max != 65536 {
		t.Fatalf("%+v", *calls)
	}

	invalid, invalidCalls := scripted(ok(`["` + repoDigest + `"]`))
	_, err = ResolveProjectImage("registry.example/faultline/demo:latest", invalid)
	wantPreparation(t, err, "PROJECT_IMAGE_INVALID_INPUT", "Project imageTag cannot use the mutable latest tag; choose an explicit setup tag.")
	if len(*invalidCalls) != 0 {
		t.Fatal(*invalidCalls)
	}
}

// tests/runtime.test.ts:358 "builds an explicitly confirmed project image
// through injected argv runners, then returns only its RepoDigest".
func TestPrepareConfirmedProjectImage(t *testing.T) {
	in, real, dockerfile := projectFixture(t, "default")
	repoDigest := "registry.example/faultline/demo@sha256:" + strings.Repeat("e", 64)
	reviewed := describe(t, in)
	run, calls := recording(ok(""), ok(`["`+repoDigest+`"]`))
	v, err := PrepareProjectImage(in, PrepareProjectOptions{Confirmed: true, ExpectedPlanDigest: reviewed.Get("review", "planDigest").Str()}, run)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"PROJECT_RUNTIME_IMAGE","setupOnly":true,"contextDirectory":` + jsjson.Quote(real) + `,"dockerfile":` + jsjson.Quote(dockerfile) +
		`,"requestedTag":"` + projectTag + `","buildNetwork":"default","image":"` + repoDigest + `","build":{"resolvedRepoDigest":"` + repoDigest + `","timeoutMs":600000,"maxOutputBytes":131072}}`
	if got := jsjson.Stringify(v); got != want {
		t.Fatalf("%s\n%s", got, want)
	}
	if len(*calls) != 2 {
		t.Fatalf("%+v", *calls)
	}
	build := (*calls)[0]
	if strings.Join(build.args, "\x00") != strings.Join([]string{"build", "--file", dockerfile, "--tag", projectTag, "--pull=false", "--network=default", real}, "\x00") ||
		build.timeout != 10*time.Minute || build.max != 131072 {
		t.Fatalf("%+v", build)
	}
	inspect := (*calls)[1]
	if strings.Join(inspect.args, " ") != "image inspect --format {{json .RepoDigests}} "+projectTag || inspect.timeout != 5*time.Second || inspect.max != 65536 {
		t.Fatalf("%+v", inspect)
	}
}

// tests/runtime.test.ts:418 "refuses to execute a project Dockerfile
// without the exact acknowledgement".
func TestPrepareRequiresAcknowledgement(t *testing.T) {
	in, _, _ := projectFixture(t, "none")
	run, calls := scripted(ok(""))
	_, err := PrepareProjectImage(in, PrepareProjectOptions{}, run)
	wantPreparation(t, err, "PROJECT_IMAGE_CONFIRMATION_REQUIRED",
		"Project image setup requires confirmation: BUILD_PROJECT_RUNTIME_IMAGE. Review describeProjectRuntimeImageBuild() before confirming.")
	if len(*calls) != 0 {
		t.Fatal(*calls)
	}
}

// tests/runtime.test.ts:438 "fails closed when Docker builds only a local
// image ID rather than a portable RepoDigest".
func TestPrepareImageIDOnly(t *testing.T) {
	in, _, dockerfile := projectFixture(t, "default")
	in.Dockerfile = dockerfile
	reviewed := describe(t, in)
	run, _ := scripted(ok(""), ok("[]"))
	_, err := PrepareProjectImage(in, PrepareProjectOptions{Confirmed: true, ExpectedPlanDigest: reviewed.Get("review", "planDigest").Str()}, run)
	wantPreparation(t, err, "UNRESOLVED_REPO_DIGEST",
		"Docker did not expose a digest-pinned RepoDigest for "+projectTag+". A local Docker image ID is not a proof-grade image reference. Push and pull the image through a registry outside FaultLine so Docker records its RepoDigest, then run resolveProjectRuntimeImage(\""+projectTag+"\") again.")
}

// tests/runtime.test.ts:475 "does not inspect after a failed, timed-out,
// or output-limited project build".
func TestPrepareNoInspectAfterFailedBuild(t *testing.T) {
	in, _, _ := projectFixture(t, "none")
	o := PrepareProjectOptions{Confirmed: true, ExpectedPlanDigest: describe(t, in).Get("review", "planDigest").Str()}
	for _, c := range []struct {
		build         hostcmd.Result
		code, message string
	}{
		{hostcmd.Result{ExitCode: code(1), Stderr: "Dockerfile failed"}, "DOCKER_BUILD_FAILED", "Docker could not build project image " + projectTag + ": Dockerfile failed"},
		{hostcmd.Result{TimedOut: true}, "DOCKER_BUILD_TIMED_OUT", "Docker project image build timed out after 600000ms for " + projectTag + "."},
		{hostcmd.Result{OutputLimitExceeded: true}, "DOCKER_BUILD_OUTPUT_LIMIT", "Docker project image build exceeded FaultLine's 131072-byte output limit for " + projectTag + "."},
	} {
		run, calls := scripted(c.build, ok(`["`+nodeRepoDigest+`"]`))
		_, err := PrepareProjectImage(in, o, run)
		wantPreparation(t, err, c.code, c.message)
		if len(*calls) != 1 || (*calls)[0][0] != "build" {
			t.Fatalf("%s: %v", c.code, *calls)
		}
	}
}

// tests/runtime.test.ts:510 "refuses ambiguous Dockerfile boundaries and
// mutable/unsafe build tags before Docker runs".
func TestRefusesOutsideDockerfileAndUnsafeTags(t *testing.T) {
	in, _, _ := projectFixture(t, "none")
	outside := t.TempDir()
	outer := filepath.Join(outside, "Dockerfile")
	if err := os.WriteFile(outer, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	escaped := in
	escaped.Dockerfile = outer
	_, err := DescribeProjectBuild(escaped)
	wantPreparation(t, err, "PROJECT_IMAGE_INVALID_INPUT", "Project image dockerfile must resolve inside the declared contextDirectory.")

	latest := in
	latest.ImageTag = "registry.example/faultline/demo:latest"
	_, err = DescribeProjectBuild(latest)
	wantPreparation(t, err, "PROJECT_IMAGE_INVALID_INPUT", "Project imageTag cannot use the mutable latest tag; choose an explicit setup tag.")

	escape := in
	escape.ImageTag = "--tag-escape:deps"
	_, err = DescribeProjectBuild(escape)
	wantPreparation(t, err, "PROJECT_IMAGE_INVALID_INPUT", "Project imageTag is not a safe Docker repository:tag reference.")
}

// tests/runtime.test.ts:542 "refuses a stale reviewed plan when any Docker
// build-context input changes".
func TestPrepareRefusesStalePlan(t *testing.T) {
	in, _, _ := projectFixture(t, "none")
	reviewed := describe(t, in)
	if err := os.WriteFile(filepath.Join(in.ContextDirectory, "application.txt"), []byte("changed after review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run, calls := scripted(ok(""))
	_, err := PrepareProjectImage(in, PrepareProjectOptions{Confirmed: true, ExpectedPlanDigest: reviewed.Get("review", "planDigest").Str()}, run)
	wantPreparation(t, err, "PROJECT_IMAGE_PLAN_MISMATCH",
		"The project build plan no longer matches the reviewed context, Dockerfile, tag, or network policy. Run fl runtime project plan again, review its new digest, then retry.")
	if len(*calls) != 0 {
		t.Fatal(*calls)
	}
}

// tests/runtime.test.ts:565 "requires a separately reviewed plan digest
// before Docker can execute".
func TestPrepareRequiresPlanDigest(t *testing.T) {
	in, _, _ := projectFixture(t, "none")
	run, calls := scripted(ok(""))
	_, err := PrepareProjectImage(in, PrepareProjectOptions{Confirmed: true, ExpectedPlanDigest: "not-a-digest"}, run)
	wantPreparation(t, err, "PROJECT_IMAGE_PLAN_DIGEST_REQUIRED",
		"Project image setup requires the exact plan.review.planDigest from a separately reviewed fl runtime project plan response.")
	if len(*calls) != 0 {
		t.Fatal(*calls)
	}
}
