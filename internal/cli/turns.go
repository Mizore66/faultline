package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/bundle/turnproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/sidecar"
	"github.com/Mizore66/faultline/internal/turninv"
	"github.com/Mizore66/faultline/internal/witness"
)

// turnsCommand ports the `args[0] === "turns"` branch of investigateCommand
// (src/cli-app.ts:2045-2160).
func turnsCommand(e *env, args []string) error {
	store := witnessStore(args) // :2052
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
	latest := hasFlag(args, "--latest")
	ledgerOption, hasLedger := option(args, "--ledger")
	// A broken binding throws here, before the --latest/--ledger conflict.
	inherited := witness.ResolveInheritedSessionFacts(sessionFlagOverrides(args, draftStore, proposalID))
	if latest && hasLedger {
		return errors.New("fl investigate turns accepts only one of --ledger <ledger.json> or --latest.")
	}
	var ledgerPath string
	var latestResolution *jsjson.Value
	switch {
	case latest:
		resolved, err := sidecar.ResolveLatestLedgerPath(repository)
		if err != nil {
			return err
		}
		v := resolved.JSON()
		latestResolution = &v
		ledgerPath = nodefs.Resolve(resolved.LedgerPath)
	case hasLedger:
		ledgerPath = nodefs.Resolve(ledgerOption)
	case inherited.LedgerPath != nil:
		ledgerPath = nodefs.Resolve(*inherited.LedgerPath)
	default:
		return errors.New("fl investigate turns requires --ledger <ledger.json>, --latest, or a local session binding ledger path.")
	}
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
	// The binding is written before the witness is read (spec 3.4).
	if _, err := witness.WriteSessionBinding(draftStore, witness.BindingInput{IncidentID: proposalID,
		ExpectDigest: &expectedFrozenDigest, LedgerPath: &ledgerPath}); err != nil {
		return err
	}
	frozenWitness, err := witness.ReadFrozen(store, proposalID)
	if err != nil {
		return err
	}
	var runtimeMapping []gitinv.MappingEntry
	if path, ok := option(args, "--runtime-mapping"); ok {
		mapping, err := readRuntimeMapping(path)
		if err != nil {
			return err
		}
		for _, k := range mapping.Obj().Keys() {
			runtimeMapping = append(runtimeMapping, gitinv.MappingEntry{Fingerprint: k, Image: mapping.Obj().Field(k).Str()})
		}
	}
	result, err := turninv.InvestigateTurnTrees(turninv.Request{Repository: repository, LedgerPath: ledgerPath,
		FrozenWitness: frozenWitness, ExpectedFrozenDigest: expectedFrozenDigest, Image: image,
		RuntimeMapping: runtimeMapping, Runner: sandbox.NativeRunner(), Injected: false})
	if err != nil {
		return err
	}
	if !result.Get("proof", "isProof").Bool() { // :2108
		out := jsjson.NewObj()
		out.Set("investigation", result)
		if latestResolution != nil {
			out.Set("ledger", *latestResolution)
		}
		out.Set("proofBundle", jsjson.MakeNull())
		out.Set("note", jsjson.MakeString("No portable turn proof bundle was written because the investigation did not establish Docker-isolated turn proof eligibility. Evidence grade remains experimental even when transitions are present."))
		e.printJSON(jsjson.MakeObject(out))
		e.exitCode = 1
		return nil
	}
	proofRoot := turnproof.DefaultProofRoot()
	output, ok := option(args, "--output")
	if !ok {
		output = nodefs.Join(proofRoot, "turns-"+strconv.FormatInt(clock.NowMillis(), 10))
	}
	output = nodefs.Resolve(output)
	lifecycleLedger, err := gitproof.ReadVerifiedCodexLifecycleLedger(ledgerPath) // second read
	if err != nil {
		return err
	}
	written, err := turnproof.Write(output, result, frozenWitness, turnproof.WriteOptions{ProofRoot: proofRoot,
		LifecycleLedger: lifecycleLedger, Repository: repository})
	if err != nil {
		return err
	}
	verification := turnproof.Verify(written.Directory, written.RootDigest, true)
	if !verification.Valid {
		return errors.New(jsexc.Concat("Generated turn proof bundle failed verification: ", strings.Join(verification.Errors, "; ")))
	}
	minimization := jsjson.MakeNull()
	if hasFlag(args, "--minimize") {
		opts := turnMinOptions{repository: repository, transitions: result.Get("transitions").Items(),
			frozenWitness: frozenWitness, expectedFrozenDigest: expectedFrozenDigest, image: image}
		if v, ok := option(args, "--transition"); ok {
			opts.transitionIndex = &v
		}
		if v, ok := option(args, "--max-executions"); ok {
			opts.maxExecutions = &v
		}
		if v, ok := option(args, "--minimization-output"); ok {
			opts.output = &v
		}
		if minimization, err = minimizeFromTurnInvestigation(opts); err != nil {
			return err
		}
	}
	out := jsjson.NewObj()
	out.Set("investigation", result)
	if latestResolution != nil {
		out.Set("ledger", *latestResolution)
	}
	out.Set("proofBundle", obj("directory", written.Directory, "rootDigest", written.RootDigest,
		"evidenceGrade", result.Get("proof", "evidenceGrade"), "evidenceLabel", result.Get("proof", "evidenceLabel"),
		"externalRootStatus", verification.ExternalRootStatus,
		"note", "Turn package is experimentally graded (EXPERIMENTAL_TURN), not COMMIT_PROOF."))
	out.Set("minimization", minimization)
	e.printJSON(jsjson.MakeObject(out))
	e.exitCode = 0
	return nil
}
