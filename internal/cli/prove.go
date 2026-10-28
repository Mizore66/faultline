package cli

import (
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/turnproof"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/turninv"
	"github.com/Mizore66/faultline/internal/witness"
)

const proveUsage = "Usage: fl prove transition <turn-proof-bundle-directory> --repo <directory> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--transition <index>] [--max-executions <count>] [--output <managed-result.json>]"

// proveCommand ports the `case "prove"` of runCli (src/cli-app.ts:3038-3044).
func proveCommand(e *env, args []string) error {
	if len(args) == 0 || args[0] != "transition" {
		return fail("%s", proveUsage)
	}
	return proveTransitionCommand(e, args[1:])
}

// proveTransitionCommand ports proveTransitionCommand (src/cli-app.ts:2294-2356).
func proveTransitionCommand(e *env, args []string) error {
	if len(args) == 0 || args[0] == "" { // :2296; an empty string is missing
		return fail("%s", proveUsage)
	}
	// The args[1] check at :2301-2303 has an empty body and is not ported:
	// a flag in the directory position is taken as the directory.
	directory := nodefs.Resolve(args[0])
	expectedRoot, rootProvided := option(args, "--expect-root") // "" is provided
	verification := turnproof.Verify(directory, expectedRoot, rootProvided)
	if !verification.Valid {
		message := strings.Join(verification.Errors, "; ")
		if message == "" {
			message = "unknown error"
		}
		return fail("Turn proof bundle failed verification: %s", message)
	}
	text := jsexc.Must(nodefs.ReadText(nodefs.Join(directory, "investigation.json")))
	investigation, err := bundle.ParseValue(jsexc.Must(jsjson.Parse(text)), turninv.ResultSchema)
	if err != nil {
		return err
	}
	store := witnessStore(args)
	proposalID, err := requiredOption(args, "--proposal")
	if err != nil {
		return err
	}
	repoOption, err := requiredOption(args, "--repo")
	if err != nil {
		return err
	}
	repository := nodefs.Resolve(repoOption)
	draftStore, ok := option(args, "--draft-store")
	if !ok {
		draftStore = witness.DefaultIncidentDraftStore(repository)
	}
	draftStore = nodefs.Resolve(draftStore)
	in := witness.InheritInput{StoreDirectory: draftStore, IncidentID: proposalID}
	if v, ok := option(args, "--expect-digest"); ok {
		in.ExpectDigest = &v
	}
	inherited := witness.ResolveInheritedSessionFacts(in)
	var expectedFrozenDigest string
	if inherited.ExpectDigest != nil {
		expectedFrozenDigest = *inherited.ExpectDigest
	} else if expectedFrozenDigest, err = requireExpectDigestOrBinding(args, draftStore, proposalID); err != nil {
		return err
	}
	image, err := requireImageOrConfig(repository, args)
	if err != nil {
		return err
	}
	// No LedgerPath: a bound one is kept.
	if _, err := witness.WriteSessionBinding(draftStore, witness.BindingInput{IncidentID: proposalID,
		ExpectDigest: &expectedFrozenDigest}); err != nil {
		return err
	}
	frozenWitness, err := witness.ReadFrozen(store, proposalID) // the store's, not the bundle's
	if err != nil {
		return err
	}
	opts := turnMinOptions{repository: repository, transitions: investigation.Get("transitions").Items(),
		frozenWitness: frozenWitness, expectedFrozenDigest: expectedFrozenDigest, image: image}
	if v, ok := option(args, "--transition"); ok {
		opts.transitionIndex = &v
	}
	if v, ok := option(args, "--max-executions"); ok {
		opts.maxExecutions = &v
	}
	if v, ok := option(args, "--output"); ok {
		opts.output = &v
	}
	minimization, err := minimizeFromTurnInvestigation(opts)
	if err != nil {
		return err
	}
	rootDigest := jsjson.MakeNull()
	if verification.RootDigest != nil {
		rootDigest = jsjson.MakeString(*verification.RootDigest)
	}
	e.printJSON(obj("command", "prove transition",
		"turnBundle", obj("directory", directory, "rootDigest", rootDigest,
			"externalRootStatus", verification.ExternalRootStatus,
			"evidenceGrade", investigation.Get("proof", "evidenceGrade")),
		"minimization", minimization))
	return nil // exit 0 whether or not the minimization was certified
}
