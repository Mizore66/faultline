package cli

import (
	"errors"

	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/runtimeimage"
)

// runtimeRunner is the Docker runner `fl runtime` uses; tests replace it.
var runtimeRunner = runtimeimage.NodeRunner

const (
	runtimeUsage        = "Usage: fl runtime resolve <node|python|go> | fl runtime prepare <node|python|go> --yes | fl runtime mapping write ... | fl runtime project plan|build|resolve ..."
	runtimeMappingUsage = "Usage: fl runtime mapping write --fingerprint <sha256:...> --image <digest-pinned-image> [--fingerprint ... --image ...] --output <mapping.json>"
	projectRuntimeUsage = "Usage: fl runtime project plan [--context <directory>] [--dockerfile <file>] --tag <repository:tag> [--network <none|default>] | fl runtime project build [same options] --expect-plan <sha256:digest> --yes | fl runtime project resolve --tag <repository:tag>"
)

// arg is args[i], or "" (TS undefined, falsy like "") past the end.
func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// optionOr is `option(args, flag) ?? fallback`.
func optionOr(args []string, flag, fallback string) string {
	if v, ok := option(args, flag); ok {
		return v
	}
	return fallback
}

// repoOption is `resolve(option(args, "--repo") ?? process.cwd())`.
func repoOption(args []string) string {
	repo, ok := option(args, "--repo")
	if !ok {
		repo = jsexc.Must(nodefs.Cwd())
	}
	return nodefs.Resolve(repo)
}

// unresolvedRepoDigest is `error instanceof
// ProjectRuntimeImagePreparationError && error.code ===
// "UNRESOLVED_REPO_DIGEST"`.
func unresolvedRepoDigest(err error) (*runtimeimage.PreparationError, bool) {
	var pe *runtimeimage.PreparationError
	if errors.As(err, &pe) && pe.Code == "UNRESOLVED_REPO_DIGEST" {
		return pe, true
	}
	return nil, false
}

// runtimeMappingPairs ports the pair scan of src/cli-app.ts:805-815: at
// every --fingerprint (values are scanned again, the step is 1) the next
// element is the fingerprint, the one after must be --image, and the one
// after that is the image.
func runtimeMappingPairs(args []string) ([]gitinv.MappingEntry, error) {
	var pairs []gitinv.MappingEntry
	for index := range args {
		if args[index] != "--fingerprint" {
			continue
		}
		fingerprintDigest := arg(args, index+1)
		imageFlag := arg(args, index+2) // "" for undefined, which is not "--image" either
		image := arg(args, index+3)
		if fingerprintDigest == "" || imageFlag != "--image" || image == "" {
			return pairs, fail("Each --fingerprint must be followed by --image <digest-pinned-image>.")
		}
		pairs = append(pairs, gitinv.MappingEntry{Fingerprint: fingerprintDigest, Image: image})
	}
	return pairs, nil
}

// projectRuntimeBuildInput ports src/cli-app.ts:695.
func projectRuntimeBuildInput(args []string) (runtimeimage.ProjectBuildInput, error) {
	requestedNetwork := optionOr(args, "--network", "none")
	if requestedNetwork != "none" && requestedNetwork != "default" {
		return runtimeimage.ProjectBuildInput{}, fail("--network must be either none or default for a project runtime image build.")
	}
	context, ok := option(args, "--context")
	if !ok {
		context = jsexc.Must(nodefs.Cwd())
	}
	contextDirectory := nodefs.Resolve(context)
	dockerfile := optionOr(args, "--dockerfile", "Dockerfile")
	imageTag, err := requiredOption(args, "--tag")
	if err != nil {
		return runtimeimage.ProjectBuildInput{}, err
	}
	return runtimeimage.ProjectBuildInput{ContextDirectory: contextDirectory, Dockerfile: dockerfile, ImageTag: imageTag, Network: requestedNetwork}, nil
}

// projectRuntimeCommand ports src/cli-app.ts:713 projectRuntimeCommand.
func projectRuntimeCommand(e *env, args []string) error {
	action := arg(args, 0)
	if action == "resolve" {
		imageTag, err := requiredOption(args, "--tag")
		if err != nil {
			return err
		}
		resolved, err := runtimeimage.ResolveProjectImage(imageTag, runtimeRunner())
		if err != nil {
			if pe, ok := unresolvedRepoDigest(err); ok {
				e.printJSON(obj(
					"status", "PROJECT_IMAGE_DIGEST_UNAVAILABLE",
					"requestedTag", imageTag,
					"limitation", pe.Message,
					"next", "FaultLine will not push an image or use registry credentials. Push and pull this reviewed tag with your own registry workflow, then rerun this same resolve command."))
				e.exitCode = 1
				return nil
			}
			return err
		}
		e.printJSON(obj(
			"status", "PROJECT_IMAGE_RESOLVED_LOCAL_DIGEST",
			"setupOnly", jsjson.MakeBool(true),
			"requestedTag", resolved.Get("requestedTag"),
			"image", resolved.Get("image"),
			"next", "Use this digest-pinned image with fl incident start --image <digest-pinned-image> so human review can bind it before proof replay."))
		return nil
	}
	if action != "plan" && action != "build" {
		return fail("%s", projectRuntimeUsage)
	}
	input, err := projectRuntimeBuildInput(args)
	if err != nil {
		return err
	}
	plan, err := runtimeimage.DescribeProjectBuild(input)
	if err != nil {
		return err
	}
	if action == "plan" {
		e.printJSON(obj(
			"status", "PROJECT_IMAGE_BUILD_REVIEW_REQUIRED",
			"plan", plan,
			"next", "Review the Dockerfile, context boundary, plan.review.planDigest, network policy, and Docker mutation shown above. If they are acceptable, rerun the same request as fl runtime project build ... --expect-plan <plan.review.planDigest> --yes."))
		return nil
	}
	expectedPlanDigest, hasExpected := option(args, "--expect-plan")
	if !hasFlag(args, "--yes") || !hasExpected {
		e.printJSON(obj(
			"status", "CONFIRMATION_REQUIRED",
			"plan", plan,
			"effect", "FaultLine will execute the reviewed Dockerfile as setup only. This may change the local Docker image store and, with --network default, allow Dockerfile build steps to use the network. It does not run a witness or create proof.",
			"requiredPlanDigest", plan.Get("review", "planDigest"),
			"next", "Review the complete plan, then rerun the same command with --expect-plan <requiredPlanDigest> --yes."))
		e.exitCode = 1
		return nil
	}
	prepared, err := runtimeimage.PrepareProjectImage(input,
		runtimeimage.PrepareProjectOptions{Confirmed: true, ExpectedPlanDigest: expectedPlanDigest}, runtimeRunner())
	if err != nil {
		if pe, ok := unresolvedRepoDigest(err); ok {
			e.printJSON(obj(
				"status", "PROJECT_IMAGE_BUILT_NEEDS_REGISTRY_DIGEST",
				"plan", plan,
				"limitation", pe.Message,
				"proofNext", "FaultLine did not push an image or use registry credentials. For a portable proof, push and pull the reviewed tag with your own registry workflow, then run fl runtime project resolve --tag <repository:tag>.",
				"localDiagnosticNext", "FaultLine will not fall back to host execution. Push a reviewed digest-pinned image before continuing the incident."))
			e.exitCode = 1
			return nil
		}
		return err
	}
	e.printJSON(obj(
		"status", "PROJECT_IMAGE_PREPARED_LOCAL_DIGEST",
		"prepared", prepared,
		"next", jsexc.Concat("Start a review-only incident with --image ", prepared.Get("image").Str(), "; FaultLine will bind that digest to the human-reviewed draft before any proof-grade replay.")))
	return nil
}

// runtimeCommand ports src/cli-app.ts:796 runtimeCommand.
func runtimeCommand(e *env, args []string) error {
	action := arg(args, 0)
	if action == "project" {
		return projectRuntimeCommand(e, args[1:])
	}
	if action == "mapping" {
		if arg(args, 1) != "write" {
			return fail("%s", runtimeMappingUsage)
		}
		output, err := requiredOption(args, "--output")
		if err != nil {
			return err
		}
		pairs, err := runtimeMappingPairs(args)
		if err != nil {
			return err
		}
		written, err := gitinv.WriteRuntimeMapping(gitinv.RuntimeMappingWrite{Pairs: pairs, OutputPath: output})
		if err != nil {
			return err
		}
		e.printJSON(obj(
			"status", "RUNTIME_MAPPING_WRITTEN",
			"path", written.Path,
			"provenanceDigest", written.ProvenanceDigest,
			"fingerprints", jsjson.MakeNumber(float64(written.Mapping.Len()))))
		return nil
	}
	requested := arg(args, 1)
	if (action != "resolve" && action != "prepare") || requested == "" {
		return fail("%s", runtimeUsage)
	}
	if action == "resolve" {
		if hasFlag(args, "--pull") {
			return fail("FaultLine never pulls a runtime image implicitly. Use fl runtime prepare <node|python|go> --yes to explicitly pull one reviewed catalog image, or pull it yourself and then run fl runtime resolve again.")
		}
		resolution, err := runtimeimage.ResolveCuratedRuntime(requested, runtimeRunner())
		if err != nil {
			return err
		}
		alias := resolution.Get("runtime", "alias").Str()
		if _, err := gitinv.UpdateConfigImage(repoOption(args), resolution.Get("image").Str(), alias); err != nil {
			return err
		}
		e.printJSON(obj(
			"status", "RESOLVED_LOCAL_DIGEST",
			"runtime", resolution.Get("runtime"),
			"requested", resolution.Get("requested"),
			"image", resolution.Get("image"),
			"next", jsexc.Concat(`fl incident start --repo . --command "<failing-command>" --runtime `, alias)))
		return nil
	}

	runtime, err := runtimeimage.SelectCuratedRuntime(requested)
	if err != nil {
		return err
	}
	if !hasFlag(args, "--yes") {
		e.printJSON(obj(
			"status", "CONFIRMATION_REQUIRED",
			"runtime", runtime.JSON(),
			"effect", "FaultLine will run docker pull "+runtime.Tag+", which may access the network and changes the local Docker image store. It will then inspect and print Docker's immutable RepoDigest. This setup action is not a proof execution.",
			"next", "fl runtime prepare "+runtime.Alias+" --yes"))
		e.exitCode = 1
		return nil
	}
	if hasFlag(args, "--pull") {
		return fail("fl runtime prepare already performs one explicit catalog pull after --yes; do not add --pull.")
	}
	prepared, err := runtimeimage.PrepareCuratedRuntime(requested, runtimeRunner())
	if err != nil {
		return err
	}
	alias := prepared.Get("runtime", "alias").Str()
	if _, err := gitinv.UpdateConfigImage(repoOption(args), prepared.Get("image").Str(), alias); err != nil {
		return err
	}
	e.printJSON(obj(
		"status", "PREPARED_LOCAL_DIGEST",
		"runtime", prepared.Get("runtime"),
		"requested", prepared.Get("requested"),
		"pulledTag", prepared.Get("pull", "tag"),
		"image", prepared.Get("image"),
		"next", jsexc.Concat(`fl incident start --repo . --command "<failing-command>" --runtime `, alias)))
	return nil
}
