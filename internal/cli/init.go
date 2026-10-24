package cli

import (
	"io"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/projinit"
	"github.com/Mizore66/faultline/internal/turnsnap"
)

const (
	initAppliedNote = "Scaffolding applied where safe. Images are not pulled; witnesses are not frozen; proof is not claimed. Standing policies are exact-string allowlists; unmatched autonomous proposals park as AGENT_DRAFT."
	initPlanNote    = "Dry plan only. Re-run with --yes to write .faultline/config.json, .faultlineignore (when missing), and install sidecar hooks when --cli is provided and hooks are absent. Add --standing-policy --standing-command <exact> --standing-frozen-by <you> for the YOLO on-ramp."
)

// initCommand ports src/cli-app.ts:1839 initCommand.
func initCommand(e *env, args []string) error {
	repository := repoOption(args)
	yes := hasFlag(args, "--yes")
	cliPath, hasCli := option(args, "--cli")
	runtimeFlag, hasRuntime := option(args, "--runtime")
	if hasRuntime && runtimeFlag != "node" && runtimeFlag != "python" && runtimeFlag != "go" {
		return fail("--runtime must be one of node, python, or go.")
	}
	resolvedCli := ""
	if hasCli {
		resolvedCli = nodefs.Resolve(cliPath)
	}

	standingPolicyRequested := hasFlag(args, "--standing-policy")
	standingCommand, hasStandingCommand := option(args, "--standing-command")
	standingFrozenBy, hasStandingFrozenBy := option(args, "--standing-frozen-by")
	standing := standingPolicyRequested || hasStandingCommand
	if standing {
		if !yes {
			return fail("Standing policy on-ramp requires --yes (exact allowlist is write-once).")
		}
		if !hasStandingCommand || jsstr.Trim(standingCommand) == "" {
			return fail("Pass --standing-command <exact UTF-8 command> for the YOLO standing-policy allowlist.")
		}
		if !hasStandingFrozenBy || jsstr.Trim(standingFrozenBy) == "" {
			return fail("Pass --standing-frozen-by <actor> so the standing policy records a human owner.")
		}
	}

	installedSidecar := false
	if yes && hasCli {
		preview, err := projinit.Plan(projinit.Options{Repository: repository, Runtime: runtimeFlag, CliPath: resolvedCli}, doctorRunner())
		if err != nil {
			return err
		}
		if preview.Get("sidecar", "status").Str() == "PREVIEW_REQUIRED" {
			command, err := projinit.SidecarCliCommand([]string{"--cli", resolvedCli})
			if err != nil {
				return err
			}
			if _, _, err := projinit.HookTarget(repository); err != nil {
				return err
			}
			if err := projinit.WriteHookConfig(repository, projinit.HookConfigFor(command)); err != nil {
				return err
			}
			installedSidecar = true
		}
	}

	options := projinit.Options{
		Repository:           repository,
		Runtime:              runtimeFlag,
		CliPath:              resolvedCli,
		WriteIgnoreIfMissing: yes,
		WriteConfig:          yes,
		MarkSidecarInstalled: installedSidecar,
	}
	// `standingCommand && standingFrozenBy`: both are non-blank here.
	if standing && standingCommand != "" && standingFrozenBy != "" {
		policy := &projinit.StandingPolicyInput{FrozenBy: standingFrozenBy, AllowedCommands: []string{standingCommand}}
		policyID := optionOr(args, "--standing-policy-id", "init-default")
		policy.PolicyID = &policyID
		if raw, ok := option(args, "--standing-max-timeout"); ok {
			n := jsjson.ToNumber(raw)
			policy.MaxTimeoutSeconds = &n
		}
		options.StandingPolicy = policy
	}
	result, err := projinit.Plan(options, doctorRunner())
	if err != nil {
		return err
	}

	var snapshotPrewarm jsjson.Value
	if yes {
		snapshotPrewarm = e.prewarmSnapshot(repository)
	}

	report := result.Get("doctor")
	out := jsjson.NewObj()
	status := "INIT_PLAN"
	if yes {
		status = "INIT_APPLIED"
	}
	out.Set("status", jsjson.MakeString(status))
	out.Set("repository", result.Get("repository"))
	out.Set("suggestedRuntime", result.Get("suggestedRuntime"))
	out.Set("doctor", jsjson.MustObject(
		"dockerInvestigationPreflight", report.Get("dockerInvestigationPreflight"),
		"likelyRuntime", report.Get("likelyRuntime"),
		"diagnosticCount", jsjson.MakeNumber(float64(len(report.Get("diagnostics").Items())))))
	for _, key := range []string{"ignoreFile", "config", "sidecar", "standingPolicy"} {
		out.Set(key, result.Get(key))
	}
	if snapshotPrewarm.Kind() != jsjson.Undefined {
		out.Set("snapshotPrewarm", snapshotPrewarm)
	}
	for _, key := range []string{"next", "nextCommands", "limitations"} {
		out.Set(key, result.Get(key))
	}
	if yes {
		out.Set("note", jsjson.MakeString(initAppliedNote))
	} else {
		out.Set("note", jsjson.MakeString(initPlanNote))
	}
	e.printJSON(jsjson.MakeObject(out))
	e.exitCode = 0
	return nil
}

// prewarmSnapshot is the `--yes` snapshot pre-warm of src/cli-app.ts:1903:
// any failure becomes SKIPPED with its message, and nothing on stderr.
func (e *env) prewarmSnapshot(repository string) (result jsjson.Value) {
	skipped := func(err error) jsjson.Value {
		return jsjson.MustObject("status", jsjson.MakeString("SKIPPED"), "reason", jsjson.MakeString(jsexc.Message(err)))
	}
	defer jsexc.Catch(func(err error) { result = skipped(err) })
	if err := nodefs.MkdirAll(nodefs.Join(repository, ".faultline"), 0o777); err != nil {
		return skipped(err)
	}
	cachePath, err := turnsnap.RepoCachePath(repository)
	if err != nil {
		return skipped(err)
	}
	snapshot, _, err := turnsnap.Capture(repository, turnsnap.CaptureOptions{
		SessionCachePath: cachePath,
		OnWarning: func(warning string) {
			io.WriteString(e.stderr, jsstr.ToUTF8(jsexc.Concat("FaultLine snapshot warning: ", warning, "\n")))
		},
	})
	if err != nil {
		return skipped(err)
	}
	io.WriteString(e.stderr, "first-turn snapshot pre-warmed\n")
	return jsjson.MustObject(
		"status", jsjson.MakeString("PREWARMED"),
		"cachePath", jsjson.MakeString(cachePath),
		"treeDigest", snapshot.Get("treeDigest"),
		"note", jsjson.MakeString("first-turn snapshot pre-warmed"))
}
