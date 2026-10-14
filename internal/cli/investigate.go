package cli

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/ledgerbind"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/witness"
)

const investigateUsage = "Usage: fl investigate --ci-log <file> --repo <directory> [...] | fl investigate --resume <id> --repo <directory> [...] | fl investigate turns --repo ... (--ledger ... | --latest) --proposal ... --expect-digest ... --image ... [--runtime-mapping <mapping.json>] [--output <dir>] | fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--runtime-mapping <mapping.json>] [--ledger <ledger.json>] [--output <managed-bundle-directory>]"

// codexThreadIDPattern is the manifest schema's identifier rule
// (src/git-proof-bundle.ts:152-155). It admits ASCII only, so the
// 160-character bound is a byte count.
var codexThreadIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

func validCodexThreadID(s string) bool {
	return len(s) >= 1 && len(s) <= 160 && codexThreadIDPattern.MatchString(s)
}

// codexThreadIDs reads the Go-only --witness-draft-thread-id and
// --repair-thread-id flags (slice 2 spec 3.6); nil when neither is given,
// so the manifest has no codex key, as in TS.
func codexThreadIDs(args []string) (*gitproof.CodexThreadIDs, error) {
	var ids gitproof.CodexThreadIDs
	for _, f := range []struct {
		flag string
		dest **string
	}{{"--witness-draft-thread-id", &ids.WitnessDraftThreadID}, {"--repair-thread-id", &ids.RepairThreadID}} {
		value, ok := option(args, f.flag)
		if !ok {
			continue
		}
		if !validCodexThreadID(value) {
			return nil, fail("%s must be a Codex thread identifier (1-160 characters, [A-Za-z0-9._:-], starting with a letter or digit).", f.flag)
		}
		*f.dest = &value
	}
	if ids.WitnessDraftThreadID == nil && ids.RepairThreadID == nil {
		return nil, nil
	}
	return &ids, nil
}

// sessionFlagOverrides ports src/cli-app.ts:171.
func sessionFlagOverrides(args []string, storeDirectory, incidentID string) witness.InheritInput {
	in := witness.InheritInput{StoreDirectory: storeDirectory, IncidentID: incidentID}
	if v, ok := option(args, "--expect-digest"); ok {
		in.ExpectDigest = &v
	}
	if v, ok := option(args, "--ledger"); ok {
		in.LedgerPath = &v
	}
	return in
}

// requireExpectDigestOrBinding ports src/cli-app.ts:183: a resolved digest
// is persisted so later commands inherit it without flags.
func requireExpectDigestOrBinding(args []string, storeDirectory, incidentID string) (string, error) {
	inherited := witness.ResolveInheritedSessionFacts(sessionFlagOverrides(args, storeDirectory, incidentID))
	if inherited.ExpectDigest == nil {
		return "", fail("Missing required option: --expect-digest (no local session binding for %s). Freeze a witness first or pass --expect-digest explicitly.", incidentID)
	}
	if _, flagged := option(args, "--expect-digest"); inherited.InheritedExpectDigest || flagged {
		if _, err := witness.WriteSessionBinding(storeDirectory, witness.BindingInput{IncidentID: incidentID,
			ExpectDigest: inherited.ExpectDigest, LedgerPath: inherited.LedgerPath}); err != nil {
			return "", err
		}
	}
	return *inherited.ExpectDigest, nil
}

// readRuntimeMapping is
// `{ ...loadRuntimeMappingDocument(JSON.parse(readFileSync(resolve(path), "utf8"))) }`;
// read and parse failures throw their raw Node messages.
func readRuntimeMapping(path string) (jsjson.Value, error) {
	text := jsexc.Must(nodefs.ReadText(nodefs.Resolve(path)))
	parsed := jsexc.Must(jsjson.Parse(text))
	entries, err := gitinv.LoadRuntimeMapping(parsed)
	if err != nil {
		return jsjson.Value{}, err
	}
	mapping := jsjson.NewObj()
	for _, e := range entries {
		mapping.Set(e.Fingerprint, jsjson.MakeString(e.Image))
	}
	return jsjson.MakeObject(mapping), nil
}

// investigateCommand ports investigateCommand (src/cli-app.ts:1953): the
// guided-mode guard and the git branch (2161-2237). Guided mode and
// `investigate turns` are later slices.
func investigateCommand(e *env, args []string) error {
	var first string
	if len(args) > 0 {
		first = args[0]
	}
	_, ciLog := option(args, "--ci-log")
	_, resume := option(args, "--resume")
	if ciLog || resume {
		if first == "git" || first == "turns" {
			return fail("Guided investigate (--ci-log / --resume) cannot be combined with `fl investigate git` or `fl investigate turns`.")
		}
		return fail("not yet ported: investigate --ci-log/--resume")
	}
	if first == "turns" {
		return fail("not yet ported: investigate turns")
	}
	if first != "git" {
		return fail("%s", investigateUsage)
	}
	// Go only: refuse a malformed thread identifier before anything is read.
	codex, err := codexThreadIDs(args)
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
	maxStates, hasMaxStates := option(args, "--max-states")
	frozenWitness, err := witness.ReadFrozen(store, proposalID)
	if err != nil {
		return err
	}
	inherited := witness.ResolveInheritedSessionFacts(sessionFlagOverrides(args, draftStore, proposalID))
	var expectedFrozenDigest string
	if inherited.ExpectDigest != nil {
		expectedFrozenDigest = *inherited.ExpectDigest
	} else if expectedFrozenDigest, err = requireExpectDigestOrBinding(args, draftStore, proposalID); err != nil {
		return err
	}
	ledgerFile := inherited.LedgerPath
	if ledgerFile == nil {
		if v, ok := option(args, "--ledger"); ok {
			ledgerFile = &v
		}
	}
	var lifecycleLedger *jsjson.Value
	var resolvedLedger *string
	if ledgerFile != nil {
		path := nodefs.Resolve(*ledgerFile)
		ledger, err := gitproof.ReadVerifiedCodexLifecycleLedger(path)
		if err != nil {
			return err
		}
		lifecycleLedger = &ledger
	}
	image, err := requireImageOrConfig(repository, args)
	if err != nil {
		return err
	}
	var runtimeMapping *jsjson.Value
	if path, ok := option(args, "--runtime-mapping"); ok {
		mapping, err := readRuntimeMapping(path)
		if err != nil {
			return err
		}
		runtimeMapping = &mapping
	}
	if ledgerFile != nil {
		resolved := nodefs.Resolve(*ledgerFile)
		resolvedLedger = &resolved
	}
	if _, err := witness.WriteSessionBinding(draftStore, witness.BindingInput{IncidentID: proposalID,
		ExpectDigest: &expectedFrozenDigest, LedgerPath: resolvedLedger}); err != nil {
		return err
	}
	from, err := requiredOption(args, "--from")
	if err != nil {
		return err
	}
	to, err := requiredOption(args, "--to")
	if err != nil {
		return err
	}
	request := jsjson.NewObj()
	request.Set("repository", jsjson.MakeString(repository))
	request.Set("range", obj("ancestor", from, "descendant", to))
	request.Set("frozenWitness", frozenWitness)
	request.Set("expectedFrozenDigest", jsjson.MakeString(expectedFrozenDigest))
	request.Set("sandbox", obj("mode", "DOCKER_ISOLATED", "image", image))
	if hasMaxStates {
		request.Set("maxStates", jsjson.MakeNumber(jsjson.ToNumber(maxStates)))
	}
	if runtimeMapping != nil {
		request.Set("runtimeMapping", *runtimeMapping)
	}
	result, err := gitinv.InvestigateGitRange(jsjson.MakeObject(request), sandbox.NativeRunner(), false)
	if err != nil {
		return err
	}
	if !result.Get("proof", "isProof").Bool() {
		e.printJSON(obj("investigation", result, "proofBundle", jsjson.MakeNull(),
			"note", "No portable proof bundle was written because the investigation did not establish Docker-isolated proof."))
		e.exitCode = 1
		return nil
	}
	output, ok := option(args, "--output")
	if !ok {
		descendant := "unknown"
		if commit := result.Get("resolvedRange", "descendant", "commit"); commit.Kind() == jsjson.String {
			descendant = commit.Str()[:min(12, len(commit.Str()))] // a hex commit id
		}
		output = nodefs.Join(gitproof.DefaultGitProofRoot(), "investigation-"+descendant+"-"+strconv.FormatInt(clock.NowMillis(), 10))
	}
	output = nodefs.Resolve(output)
	written, err := gitproof.WriteGitInvestigationProofBundle(output, result, frozenWitness,
		gitproof.WriteOptions{LifecycleLedger: lifecycleLedger, Codex: codex})
	if err != nil {
		return err
	}
	verification := gitproof.Verify(written.Directory, written.RootDigest, true)
	if !verification.Valid {
		return errors.New(jsexc.Concat("Generated Git proof bundle failed verification: ", strings.Join(verification.Errors, "; ")))
	}
	var ledgerBinding *jsjson.Value
	if bindingOutput, ok := option(args, "--ledger-binding-output"); ok && lifecycleLedger != nil {
		binding, err := ledgerbind.Create(*lifecycleLedger, result, *ledgerFile)
		if err != nil {
			return err
		}
		path, err := ledgerbind.WriteAtomic(nodefs.Resolve(bindingOutput), binding)
		if err != nil {
			return err
		}
		v := obj("path", path, "bindingDigest", binding.Get("bindingDigest"))
		ledgerBinding = &v
	}
	out := obj("evidenceGrade", result.Get("proof", "evidenceGrade"), "evidenceLabel", result.Get("proof", "evidenceLabel"),
		"investigation", result,
		"proofBundle", obj("directory", written.Directory, "rootDigest", written.RootDigest,
			"lifecycle", written.Manifest.Get("lifecycle"), "externalRootStatus", verification.ExternalRootStatus))
	if ledgerBinding != nil {
		out.Obj().Set("ledgerBinding", *ledgerBinding)
	}
	e.printJSON(out)
	e.exitCode = 0
	return nil
}
