package cli

import (
	"context"
	"io"

	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/guided"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/witness"
)

// guidedDeps are the private seams of the guided wrapper: the guided
// composition and the freeze-wait signal registration.
type guidedDeps struct {
	run       func(guided.Options) (jsjson.Value, error)
	interrupt func() (context.Context, func())
}

func defaultGuidedDeps() guidedDeps {
	return guidedDeps{run: guided.Run, interrupt: guidedInterruptSignal}
}

// guidedInvestigateCommand is the guided branch of investigateCommand
// (src/cli-app.ts:1953-2043).
func guidedInvestigateCommand(e *env, args []string) error {
	return guidedInvestigate(e, args, defaultGuidedDeps())
}

func resolvedOptionPtr(args []string, flag string) *string {
	if v, ok := option(args, flag); ok {
		r := nodefs.Resolve(v)
		return &r
	}
	return nil
}

// numberOptionPtr is `Number(option)` when the option is defined.
func numberOptionPtr(args []string, flag string) *float64 {
	if v, ok := option(args, flag); ok {
		n := jsjson.ToNumber(v)
		return &n
	}
	return nil
}

func guidedInvestigate(e *env, args []string, d guidedDeps) error {
	var first string
	if len(args) > 0 {
		first = args[0]
	}
	ciLog, resumeID := optionPtr(args, "--ci-log"), optionPtr(args, "--resume")
	if first == "git" || first == "turns" {
		return fail("Guided investigate (--ci-log / --resume) cannot be combined with `fl investigate git` or `fl investigate turns`.")
	}
	if ciLog != nil && resumeID != nil {
		return fail("Guided investigate accepts either --ci-log <file> or --resume <incident-id>, not both.")
	}
	repoOption := "" // resolve("") is process.cwd()
	if v, ok := option(args, "--repo"); ok {
		repoOption = v
	}
	repository := nodefs.Resolve(repoOption)
	from, to := optionPtr(args, "--from"), optionPtr(args, "--to")
	if (from == nil) != (to == nil) {
		return fail("Guided investigate accepts --from and --to together, or neither for the conservative local HEAD-parent fallback.")
	}
	incidentID := optionPtr(args, "--id")
	if incidentID == nil {
		incidentID = resumeID
	}
	draftStoreOption, ok := option(args, "--draft-store")
	if !ok {
		draftStoreOption = witness.DefaultIncidentDraftStore(repository)
	}
	draftStoreForInherit := nodefs.Resolve(draftStoreOption)
	var expectDigest, ledgerFile *string
	if incidentID != nil {
		inherited := witness.ResolveInheritedSessionFacts(sessionFlagOverrides(args, draftStoreForInherit, *incidentID))
		expectDigest, ledgerFile = inherited.ExpectDigest, inherited.LedgerPath
	} else {
		expectDigest, ledgerFile = optionPtr(args, "--expect-digest"), optionPtr(args, "--ledger")
	}
	image := optionPtr(args, "--image")
	if image == nil {
		configured, found, err := gitinv.ResolveConfiguredImage(repository)
		if err != nil {
			return err
		}
		if found {
			image = &configured
		}
	}
	var optionID *string // the guided incidentId is omitted on resume
	if resumeID == nil {
		optionID = incidentID
	}
	options := guided.Options{
		Repository: repository, CILogPath: ciLog, ResumeID: resumeID, Command: optionPtr(args, "--command"),
		From: from, To: to, IncidentID: optionID, Runtime: optionPtr(args, "--runtime"), Image: image,
		ExpectDigest: expectDigest, LedgerFile: ledgerFile,
		MaxStates: numberOptionPtr(args, "--max-states"), Output: optionPtr(args, "--output"),
		ReviewPort: numberOptionPtr(args, "--port"), WitnessStore: resolvedOptionPtr(args, "--store"),
		DraftStore: resolvedOptionPtr(args, "--draft-store"),
		OnPhase: func(phase string, detail *string) {
			line := jsexc.Concat("FaultLine guided investigate: ", phase)
			if detail != nil {
				line = jsexc.Concat(line, " (", *detail, ")")
			}
			io.WriteString(e.stderr, jsstr.ToUTF8(line+"\n"))
		},
		OnReviewReady: func(url string) {
			io.WriteString(e.stderr, jsstr.ToUTF8(jsexc.Concat("FaultLine local witness review: ", url,
				"\nApprove, then Freeze (separate clicks). This command continues automatically after freeze. Press Ctrl+C to cancel and resume later with --resume <id>.\n")))
		},
	}
	// Registered immediately before the guided call and held through the
	// post-result binding, JSON output and exit code.
	signalCtx, cleanup := d.interrupt()
	defer cleanup()
	options.Signal = signalCtx
	result, err := d.run(options)
	if err != nil {
		return err
	}
	if incidentID != nil && expectDigest != nil {
		in := witness.BindingInput{IncidentID: *incidentID, ExpectDigest: expectDigest}
		if ledgerFile != nil {
			resolved := nodefs.Resolve(*ledgerFile)
			in.LedgerPath = &resolved
		}
		if _, err := witness.WriteSessionBinding(draftStoreForInherit, in); err != nil {
			return err
		}
	}
	e.printJSON(result)
	e.exitCode = 1
	if status := result.Get("status"); status.Kind() == jsjson.String && status.Str() == "GUIDED_PROOF_BUNDLE_READY" {
		e.exitCode = 0
	}
	return nil
}
