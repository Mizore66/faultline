// Package projinit is the Go port of src/project-init.ts (planProjectInit,
// the .faultlineignore starter) and of the Codex sidecar hook-config writer
// `fl init --cli` uses (src/cli-app.ts:1600-1755, src/codex-sidecar.ts).
package projinit

import (
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/doctor"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/witness"
)

// StarterFaultlineIgnore ports src/project-init.ts:50
// STARTER_FAULTLINEIGNORE: eleven comment lines and "" joined by "\n".
const StarterFaultlineIgnore = "# Reviewed FaultLine turn-snapshot exclusions (edit carefully).\n" +
	"# Prefer path exclusions over secret-scan bypasses.\n" +
	"#\n" +
	"# Do NOT ignore environment descriptors (lockfiles, package.json, go.mod,\n" +
	"# Cargo.lock, Dockerfiles, …). FaultLine refuses to suppress them for\n" +
	"# turn snapshots because they drive environment fingerprinting.\n" +
	"#\n" +
	"# Add only paths that must not enter the object database (local secrets,\n" +
	"# huge generated artifacts). Example:\n" +
	"#   scratch/\n" +
	"#   local-secrets/\n"

// StandingPolicyInput ports src/project-init.ts:12 StandingPolicyInitInput.
// A nil PolicyID or MaxTimeoutSeconds is TS undefined.
type StandingPolicyInput struct {
	PolicyID          *string
	FrozenBy          string
	AllowedCommands   []string
	MaxTimeoutSeconds *float64
}

// Options are planProjectInit's options (src/project-init.ts:77). An empty
// Runtime or CliPath is TS undefined.
type Options struct {
	Repository, Runtime, CliPath                          string
	WriteIgnoreIfMissing, WriteConfig, MarkSidecarInstalled bool
	StandingPolicy                                        *StandingPolicyInput
}

// limitations ports the limitations list of src/project-init.ts:217.
var limitations = []string{
	"fl init does not pull Docker images, freeze witnesses, or create proof packages.",
	"Sidecar install still requires explicit --yes after human review.",
	"Standing policies match exact command strings only — CI-log suggestions never auto-match.",
	"Turn localization remains EXPERIMENTAL_TURN until promotion criteria are met.",
}

// mapDetectedRuntime ports src/project-init.ts:65; "" is null.
func mapDetectedRuntime(imageFamily jsjson.Value) string {
	switch {
	case imageFamily.Kind() != jsjson.String:
		return ""
	case imageFamily.Str() == "node" || imageFamily.Str() == "python":
		return imageFamily.Str()
	case imageFamily.Str() == "golang":
		return "go"
	}
	return ""
}

func str(s string) jsjson.Value { return jsjson.MakeString(s) }

func strings2(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = str(item)
	}
	return jsjson.MakeArray(values)
}

// Plan ports src/project-init.ts:75 planProjectInit: doctor plus optional
// scaffolding (.faultlineignore, .faultline/config.json, a standing
// approval policy), without pulling images, freezing witnesses or claiming
// proof readiness. The result's keys are ProjectInitResult's, in TS order.
// Failures are the errors TS throws (fs, zod, policy-store errors).
func Plan(o Options, run doctor.Runner) (result jsjson.Value, err error) {
	defer jsexc.Catch(func(e error) { result, err = jsjson.Value{}, e })
	return plan(o, run), nil
}

func plan(o Options, run doctor.Runner) jsjson.Value {
	repository := nodefs.Resolve(o.Repository)
	report := doctor.Run(repository, run)
	detected := doctor.DetectLikelyRuntime(repository)
	suggestedRuntime := o.Runtime
	if suggestedRuntime == "" {
		suggestedRuntime = mapDetectedRuntime(detected.Get("imageFamily"))
	}

	hooksPath := nodefs.Join(repository, ".codex", "hooks.json")
	var sidecarStatus, sidecarDetail string
	switch {
	case o.CliPath == "":
		sidecarStatus, sidecarDetail = "CLI_REQUIRED", "Pass --cli <built-cli.js> to preview/install Codex sidecar hooks."
	case o.MarkSidecarInstalled:
		sidecarStatus, sidecarDetail = "INSTALLED", "Project .codex/hooks.json was created. Review and trust hooks via Codex /hooks before recording."
	case nodefs.Exists(hooksPath):
		sidecarStatus, sidecarDetail = "ALREADY_PRESENT", "Existing .codex/hooks.json will not be overwritten. Merge via `fl codex sidecar config` if needed."
	default:
		sidecarStatus, sidecarDetail = "PREVIEW_REQUIRED", "Sidecar hooks are ready to install with explicit --yes (never auto-installed without confirmation)."
	}
	sidecar := jsjson.MustObject("status", str(sidecarStatus), "hooksPath", str(hooksPath), "detail", str(sidecarDetail))

	ignorePath := nodefs.Join(repository, ".faultlineignore")
	ignoreStatus := "SKIPPED"
	if nodefs.Exists(ignorePath) {
		ignoreStatus = "ALREADY_PRESENT"
	} else if o.WriteIgnoreIfMissing {
		if err := nodefs.WriteFileUTF8(ignorePath, StarterFaultlineIgnore, 0o666, true); err != nil {
			jsexc.Throw(err)
		}
		ignoreStatus = "CREATED"
	}
	ignoreFile := jsjson.MustObject("status", str(ignoreStatus), "path", str(ignorePath))

	runtime := suggestedRuntime
	if runtime == "" {
		runtime = "node"
	}
	writeConfig := o.WriteIgnoreIfMissing || o.MarkSidecarInstalled || o.WriteConfig
	var config jsjson.Value
	if writeConfig || nodefs.Exists(nodefs.Join(repository, ".faultline", "config.json")) {
		written := jsexc.Must(gitinv.EnsureConfig(gitinv.EnsureConfigOptions{Repository: repository, RuntimeAlias: suggestedRuntime}))
		config = jsjson.MustObject("status", str(written.Status), "path", str(written.Path), "value", written.Config)
	} else {
		config = jsjson.MustObject("status", str("SKIPPED"), "path", str(nodefs.Join(repository, ".faultline", "config.json")), "value", jsjson.MakeNull())
	}

	var standingPolicy jsjson.Value
	switch sp := o.StandingPolicy; {
	case sp != nil && len(sp.AllowedCommands) > 0:
		witnessStore := config.Get("value", "stores", "witnesses")
		store := nodefs.Join(repository, ".faultline", "witnesses")
		if witnessStore.Kind() == jsjson.String {
			store = witnessStore.Str()
		}
		policyID := "init-default"
		if sp.PolicyID != nil {
			policyID = *sp.PolicyID
		}
		maxTimeout := 120.0
		if sp.MaxTimeoutSeconds != nil {
			maxTimeout = *sp.MaxTimeoutSeconds
		}
		input := jsjson.MustObject(
			"schemaVersion", str("faultline.standing-approval-policy.v1"),
			"policyId", str(policyID),
			"frozenBy", str(sp.FrozenBy),
			"frozenAt", str(clock.ISO(clock.Now())),
			"allowedCommands", strings2(sp.AllowedCommands),
			"allowedOverlayTemplates", jsjson.MakeArray(nil),
			"maxTimeoutSeconds", jsjson.MakeNumber(maxTimeout),
			"network", str("disabled"),
			"credentials", str("redacted"))
		// writeStandingApprovalPolicy(store, buildStandingApprovalPolicy(input)):
		// the write builds the policy again from the built one, which gives
		// the same policy back.
		policy := jsexc.Must(witness.BuildPolicy(input))
		jsexc.Must(witness.WritePolicy(store, policy))
		standingPolicy = jsjson.MustObject(
			"status", str("CREATED"),
			"policyId", policy.Get("policyId"),
			"policyDigest", policy.Get("policyDigest"),
			"detail", str(jsexc.Concat("Standing approval frozen for ", jsjson.FormatNumber(float64(len(policy.Get("allowedCommands").Items()))),
				" exact command(s). Novel commands still stop for review (COH-09/12).")))
	case o.WriteIgnoreIfMissing || o.WriteConfig:
		standingPolicy = jsjson.MustObject("status", str("PROMPT_AVAILABLE"),
			"detail", str("Pass --standing-policy --standing-command <exact> --standing-frozen-by <you> to enable the YOLO allowlist on-ramp."))
	default:
		standingPolicy = jsjson.MustObject("status", str("SKIPPED"),
			"detail", str("Standing policy not requested. Use fl init --standing-policy … or fl witness policy freeze."))
	}

	var next string
	switch {
	case config.Get("status").Str() == "SKIPPED":
		next = jsexc.Concat("fl init --repo ", repository, " --yes")
	case sidecarStatus == "PREVIEW_REQUIRED" && o.CliPath != "":
		next = jsexc.Concat("fl codex sidecar install --repo ", repository, " --cli ", o.CliPath, " --yes")
	case standingPolicy.Get("status").Str() == "PROMPT_AVAILABLE":
		next = jsexc.Concat("fl init --repo ", repository, ` --yes --standing-policy --standing-command "<exact predicate>" --standing-frozen-by "<you>"`)
	default:
		next = "fl runtime prepare " + runtime + " --yes"
	}

	suggested := jsjson.MakeNull()
	if suggestedRuntime != "" {
		suggested = str(suggestedRuntime)
	}
	return jsjson.MustObject(
		"repository", str(repository),
		"doctor", report,
		"suggestedRuntime", suggested,
		"sidecar", sidecar,
		"ignoreFile", ignoreFile,
		"config", config,
		"standingPolicy", standingPolicy,
		"next", str(next),
		"nextCommands", strings2([]string{next}),
		"limitations", strings2(limitations))
}
