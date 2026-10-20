package runtimeimage

import (
	"github.com/Mizore66/faultline/internal/jsjson"
)

// ProjectRuntimeImageBuildConfirmation ports src/runtime.ts:52
// PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION.
const ProjectRuntimeImageBuildConfirmation = "BUILD_PROJECT_RUNTIME_IMAGE"

// Port of src/runtime.ts:573 createDockerProjectImageBuildCommandForNormalized
// (its arguments).
func buildArgs(n normalizedBuild) []string {
	return []string{"build", "--file", n.dockerfile, "--tag", n.imageTag, "--pull=false", "--network=" + n.network, n.contextDirectory}
}

func stringArray(items []string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, s := range items {
		out[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(out)
}

// commandJSON is a DockerProjectImageBuildCommand: { executable, arguments,
// timeoutMs }.
func commandJSON(args []string) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("executable", jsjson.MakeString("docker"))
	o.Set("arguments", stringArray(args))
	o.Set("timeoutMs", jsjson.MakeNumber(float64(DockerProjectImageBuildTimeout.Milliseconds())))
	return jsjson.MakeObject(o)
}

// Port of src/runtime.ts:520 describeProjectRuntimeImageBuild's description.
const (
	describeDefaultNetwork = "Setup only: FaultLine will ask Docker to build the chosen Dockerfile using Docker's default build network. Dockerfile RUN instructions may access the network and the local Docker image store will change; no incident proof is executed."
	describeNoNetwork      = "Setup only: FaultLine will ask Docker to build the chosen Dockerfile with Dockerfile RUN networking disabled. Docker may still communicate for daemon or base-image behavior, which FaultLine does not independently certify. The local Docker image store will change; no incident proof is executed."
)

// DescribeProjectBuild ports src/runtime.ts:520
// describeProjectRuntimeImageBuild: it validates the request, fingerprints
// the whole build context and returns the plan, without running Docker.
func DescribeProjectBuild(in ProjectBuildInput) (jsjson.Value, error) {
	if err := assertProjectBuildNetwork(in.Network); err != nil {
		return jsjson.Value{}, err
	}
	n, err := normalizeProjectBuildPaths(in)
	if err != nil {
		return jsjson.Value{}, err
	}
	command := commandJSON(buildArgs(n))
	f, err := fingerprintProjectBuildContext(n)
	if err != nil {
		return jsjson.Value{}, err
	}
	planDigest, err := projectBuildPlanDigest(n, f)
	if err != nil {
		return jsjson.Value{}, err
	}

	review := jsjson.NewObj()
	review.Set("planDigest", jsjson.MakeString(planDigest))
	review.Set("contextDigest", jsjson.MakeString(f.contextDigest))
	review.Set("dockerfileDigest", jsjson.MakeString(f.dockerfileDigest))
	review.Set("fileCount", jsjson.MakeNumber(float64(f.fileCount)))
	review.Set("totalBytes", jsjson.MakeNumber(float64(f.totalBytes)))

	effects := jsjson.NewObj()
	effects.Set("mutatesLocalDockerImageStore", jsjson.MakeBool(true))
	effects.Set("dockerfileInstructionsExecute", jsjson.MakeBool(true))
	effects.Set("buildNetwork", jsjson.MakeString(n.network))
	effects.Set("dockerfileRunInstructionsMayAccessNetwork", jsjson.MakeBool(n.network == "default"))
	effects.Set("dockerDaemonNetworkBehaviorNotIndependentlyCertified", jsjson.MakeBool(true))
	effects.Set("mayAccessNetwork", jsjson.MakeBool(true))
	effects.Set("doesNotExecuteProof", jsjson.MakeBool(true))
	effects.Set("requiresDigestPinnedResultForProof", jsjson.MakeBool(true))

	description := describeNoNetwork
	if n.network == "default" {
		description = describeDefaultNetwork
	}
	plan := jsjson.NewObj()
	plan.Set("kind", jsjson.MakeString("PROJECT_RUNTIME_IMAGE_BUILD"))
	plan.Set("setupOnly", jsjson.MakeBool(true))
	plan.Set("contextDirectory", jsjson.MakeString(n.contextDirectory))
	plan.Set("dockerfile", jsjson.MakeString(n.dockerfile))
	plan.Set("imageTag", jsjson.MakeString(n.imageTag))
	plan.Set("network", jsjson.MakeString(n.network))
	plan.Set("command", command)
	plan.Set("review", jsjson.MakeObject(review))
	plan.Set("effects", jsjson.MakeObject(effects))
	plan.Set("description", jsjson.MakeString(description))
	return jsjson.MakeObject(plan), nil
}

// ResolveProjectImage ports src/runtime.ts:661 resolveProjectRuntimeImage: a
// read-only `docker image inspect` of a validated project tag. The result is
// { requestedTag, image }.
func ResolveProjectImage(tag string, run Runner) (jsjson.Value, error) {
	requestedTag, err := validateProjectImageTag(tag)
	if err != nil {
		return jsjson.Value{}, err
	}
	result, thrown := call(run, inspectArgs(requestedTag), DockerImageInspectTimeout)
	if thrown != nil {
		return jsjson.Value{}, preparationError("DOCKER_INSPECT_FAILED", "FaultLine could not inspect the project image ", requestedTag, errorDetail(thrown))
	}
	if result.TimedOut {
		return jsjson.Value{}, preparationError("DOCKER_INSPECT_TIMED_OUT", "Docker image inspection timed out for project image ", requestedTag, ".")
	}
	if result.OutputLimitExceeded {
		return jsjson.Value{}, preparationError("DOCKER_INSPECT_OUTPUT_LIMIT", "Docker image inspection exceeded FaultLine's 65536-byte output limit for project image ", requestedTag, ".")
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		return jsjson.Value{}, preparationError("DOCKER_INSPECT_FAILED", "Docker could not inspect project image ", requestedTag, dockerFailureDetail(result))
	}

	var pinned []string
	for _, d := range parseRepoDigests(result.Stdout) {
		if isDigestPinnedRepoDigest(d) {
			pinned = append(pinned, d)
		}
	}
	if len(pinned) == 0 {
		return jsjson.Value{}, preparationError("UNRESOLVED_REPO_DIGEST", "Docker did not expose a digest-pinned RepoDigest for ", requestedTag,
			". A local Docker image ID is not a proof-grade image reference. Push and pull the image through a registry outside FaultLine so Docker records its RepoDigest, then run resolveProjectRuntimeImage(",
			jsjson.Quote(requestedTag), ") again.")
	}
	expected, found := "", false
	for _, d := range pinned {
		if isExpectedProjectImageRepository(tag, d) {
			expected, found = d, true
			break
		}
	}
	if !found {
		return jsjson.Value{}, preparationError("UNEXPECTED_REPOSITORY", "Docker returned a RepoDigest for a repository other than project image ", requestedTag, ".")
	}
	o := jsjson.NewObj()
	o.Set("requestedTag", jsjson.MakeString(requestedTag))
	o.Set("image", jsjson.MakeString(expected))
	return jsjson.MakeObject(o), nil
}

// PrepareProjectOptions holds the parts of src/runtime.ts:208
// PrepareProjectRuntimeImageRequest beyond the build input: Confirmed stands
// for `confirmation === PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION`, and
// ExpectedPlanDigest is the reviewed plan.review.planDigest.
type PrepareProjectOptions struct {
	Confirmed          bool
	ExpectedPlanDigest string
}

// PrepareProjectImage ports src/runtime.ts:842 prepareProjectRuntimeImage:
// after the confirmation and a fresh plan whose digest must equal the
// reviewed one, it runs the fixed `docker build`, then ResolveProjectImage
// with the same runner. Nothing is inspected after a failed, timed-out or
// output-limited build.
func PrepareProjectImage(in ProjectBuildInput, o PrepareProjectOptions, run Runner) (jsjson.Value, error) {
	if !o.Confirmed {
		return jsjson.Value{}, preparationError("PROJECT_IMAGE_CONFIRMATION_REQUIRED",
			"Project image setup requires confirmation: BUILD_PROJECT_RUNTIME_IMAGE. Review describeProjectRuntimeImageBuild() before confirming.")
	}
	plan, err := DescribeProjectBuild(in)
	if err != nil {
		return jsjson.Value{}, err
	}
	if !sha256Digest.Test(o.ExpectedPlanDigest) {
		return jsjson.Value{}, preparationError("PROJECT_IMAGE_PLAN_DIGEST_REQUIRED",
			"Project image setup requires the exact plan.review.planDigest from a separately reviewed fl runtime project plan response.")
	}
	if o.ExpectedPlanDigest != plan.Get("review", "planDigest").Str() {
		return jsjson.Value{}, preparationError("PROJECT_IMAGE_PLAN_MISMATCH",
			"The project build plan no longer matches the reviewed context, Dockerfile, tag, or network policy. Run fl runtime project plan again, review its new digest, then retry.")
	}

	imageTag := plan.Get("imageTag").Str()
	var args []string
	for _, a := range plan.Get("command", "arguments").Items() {
		args = append(args, a.Str())
	}
	result, thrown := call(run, args, DockerProjectImageBuildTimeout)
	if thrown != nil {
		return jsjson.Value{}, preparationError("DOCKER_BUILD_FAILED", "FaultLine could not build project image ", imageTag, errorDetail(thrown))
	}
	if result.TimedOut {
		return jsjson.Value{}, preparationError("DOCKER_BUILD_TIMED_OUT", "Docker project image build timed out after 600000ms for ", imageTag, ".")
	}
	if result.OutputLimitExceeded {
		return jsjson.Value{}, preparationError("DOCKER_BUILD_OUTPUT_LIMIT", "Docker project image build exceeded FaultLine's 131072-byte output limit for ", imageTag, ".")
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		return jsjson.Value{}, preparationError("DOCKER_BUILD_FAILED", "Docker could not build project image ", imageTag, dockerFailureDetail(result))
	}

	resolved, err := ResolveProjectImage(imageTag, run)
	if err != nil {
		return jsjson.Value{}, err
	}
	build := jsjson.NewObj()
	build.Set("resolvedRepoDigest", resolved.Get("image"))
	build.Set("timeoutMs", jsjson.MakeNumber(float64(DockerProjectImageBuildTimeout.Milliseconds())))
	build.Set("maxOutputBytes", jsjson.MakeNumber(DockerProjectImageBuildMaxOutputBytes))
	prepared := jsjson.NewObj()
	prepared.Set("kind", jsjson.MakeString("PROJECT_RUNTIME_IMAGE"))
	prepared.Set("setupOnly", jsjson.MakeBool(true))
	prepared.Set("contextDirectory", plan.Get("contextDirectory"))
	prepared.Set("dockerfile", plan.Get("dockerfile"))
	prepared.Set("requestedTag", plan.Get("imageTag"))
	prepared.Set("buildNetwork", plan.Get("network"))
	prepared.Set("image", resolved.Get("image"))
	prepared.Set("build", jsjson.MakeObject(build))
	return jsjson.MakeObject(prepared), nil
}
