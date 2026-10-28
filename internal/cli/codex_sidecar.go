package cli

import (
	"io"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/projinit"
	"github.com/Mizore66/faultline/internal/sidecar"
)

const sidecarUsage = "Usage: fl codex sidecar config (--cli <built-cli.js> | --command <hook-command> [--command-windows <hook-command>]) | fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes | fl codex sidecar hook [--input <hook.json>] [--quiet] | fl codex sidecar status [--repo <directory>] [--session <session-id>]"

const sidecarContinue = "{\"continue\":true}\n"

// codexSidecarCommand ports src/cli-app.ts:1757 codexSidecarCommand.
func codexSidecarCommand(e *env, args []string) error {
	action := ""
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "config":
		command, err := projinit.SidecarCliCommand(args)
		if err != nil {
			return err
		}
		e.printJSON(projinit.HookConfigFor(command))
		return nil
	case "install":
		return sidecarInstall(e, args)
	case "status":
		return sidecarStatus(e, args)
	case "hook":
		return sidecarHook(e, args)
	}
	return fail("%s", sidecarUsage)
}

// sidecarInstall ports the install branch (src/cli-app.ts:1766-1788).
func sidecarInstall(e *env, args []string) error {
	command, err := projinit.SidecarCliCommand(args)
	if err != nil {
		return err
	}
	config := projinit.HookConfigFor(command)
	repo, err := requiredOption(args, "--repo")
	if err != nil {
		return err
	}
	file, _, err := projinit.HookTarget(repo)
	if err != nil {
		return err
	}
	if !hasFlag(args, "--yes") {
		e.printJSON(obj(
			"status", "CONFIRMATION_REQUIRED",
			"target", file,
			"effect", "Creates a new project .codex/hooks.json containing the displayed reviewed hook configuration. It does not overwrite or merge an existing hook document.",
			"hooks", config,
			"next", "After reviewing the displayed configuration, rerun the same install command with --yes."))
		e.exitCode = 1
		return nil
	}
	if err := projinit.WriteHookConfig(repo, config); err != nil {
		return err
	}
	e.printJSON(obj(
		"status", "PROJECT_HOOKS_INSTALLED",
		"hookFile", file,
		"next", "Restart or begin Codex in this trusted project, then use /hooks to review and trust the displayed FaultLine hooks before they run."))
	return nil
}

// sidecarStatus ports the status branch (src/cli-app.ts:1789-1806).
func sidecarStatus(e *env, args []string) (err error) {
	defer jsexc.Catch(func(t error) { err = t })
	repo, ok := option(args, "--repo")
	if !ok {
		repo = "."
	}
	repository := nodefs.Resolve(repo)
	session, _ := option(args, "--session")
	inspection, err := sidecar.Inspect(repository, session)
	if err != nil {
		return err
	}
	invalid := false
	for _, r := range inspection.Recordings {
		if !r.Valid {
			invalid = true
		}
	}
	var status string
	var next jsjson.Value
	switch {
	case len(inspection.Recordings) == 0:
		status = "NO_SIDECAR_RECORDINGS"
		next = stringList("Install a reviewed project hook document with fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes, review/trust it through /hooks, then begin a Codex session in this Git worktree.")
	case invalid:
		status = "SIDECAR_RECORDING_INVALID"
		next = stringList("Do not bind an invalid sidecar ledger. Resolve the reported local integrity/storage issue, then start a new observed session.")
	default:
		status = "SIDECAR_RECORDINGS_READY"
		items := make([]string, len(inspection.Recordings))
		for i, r := range inspection.Recordings {
			items[i] = jsexc.Concat("After human witness freeze, pass --ledger ", r.LedgerPath, " to fl incident continue <id> only when its observed checkpoints apply to the selected Git states.")
		}
		next = stringList(items...)
	}
	body := inspection.JSON()
	e.printJSON(obj(
		"status", status,
		"storageDirectory", body.Get("storageDirectory"),
		"recordings", body.Get("recordings"),
		"next", next,
		"privacy", "Status reads only sidecar integrity and checkpoint metadata. It never prints prompts, assistant messages, or transcript paths."))
	if invalid {
		e.exitCode = 1
	}
	return nil
}

// sidecarHook ports the hook branch (src/cli-app.ts:1811-1836).
func sidecarHook(e *env, args []string) error {
	quiet := hasFlag(args, "--quiet")
	err := jsexc.Try(func() {
		var input jsjson.Value
		if _, ok := option(args, "--input"); !ok {
			body := jsexc.Must(e.readStandardInput())
			input = jsexc.Must(jsjson.Parse(body))
		} else {
			file := jsexc.Must(requiredOption(args, "--input"))
			input = jsexc.Must(readJSONInput(file))
		}
		result := jsexc.Must(sidecar.RecordObservedHook(input, e.stderr))
		if quiet {
			e.out(sidecarContinue)
			return
		}
		o := jsjson.NewObj()
		body := result.JSON()
		for _, k := range body.Obj().Keys() {
			o.Set(k, body.Get(k))
		}
		o.Set("limitation", jsjson.MakeString("Observed public Codex lifecycle metadata only; this is not private model-state or intent capture."))
		e.printJSON(jsjson.MakeObject(o))
	})
	if err == nil {
		return nil
	}
	if !quiet {
		return err
	}
	detail := jsjson.AtCallSite(jsjson.SiteCLI, func() string { return jsexc.Message(err) })
	io.WriteString(e.stderr, jsstr.ToUTF8(jsexc.Concat("FaultLine sidecar did not record this hook: ", detail, "\n")))
	io.WriteString(e.stderr, "Run fl codex sidecar status in the repository after the turn for a detailed health check.\n")
	e.out(sidecarContinue)
	return nil
}
