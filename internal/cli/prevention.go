package cli

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

const (
	preventionVerified      = "PREVENTION_VERIFIED"
	preventionVerifiedClaim = "Prevention verified"
	preventionSummaryClaim  = "Prevention evidence summary"
)

// preventionCommand ports src/cli-app.ts:2560 preventionCommand.
func preventionCommand(e *env, args []string) error {
	action := ""
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "verify":
		return e.preventionVerify(args)
	case "write":
		return e.preventionWrite(args)
	}
	return fail("Usage: fl prevention write --input <prevention-input.json> | fl prevention write --from-bundle ... | fl prevention verify <directory> [--expect-root <sha256:...>]")
}

// preventionVerify ports the verify branch (src/cli-app.ts:2562-2584).
func (e *env) preventionVerify(args []string) error {
	if len(args) < 2 || args[1] == "" {
		return fail("Usage: fl prevention verify <prevention-proof-directory> [--expect-root <sha256:...>]")
	}
	expectedRoot, rootProvided := option(args, "--expect-root")
	r := prevention.Verify(nodefs.Resolve(args[1]), expectedRoot, rootProvided)
	classification, rootDigest := jsjson.MakeNull(), jsjson.MakeNull()
	if r.Classification != nil {
		classification = jsjson.MakeString(*r.Classification)
	}
	if r.RootDigest != nil {
		rootDigest = jsjson.MakeString(*r.RootDigest)
	}
	verified := r.Valid && r.Classification != nil && *r.Classification == preventionVerified
	claim := "Invalid"
	if verified {
		claim = preventionVerifiedClaim
	} else if r.Valid {
		claim = preventionSummaryClaim
	}
	e.printJSON(obj(
		"valid", jsjson.MakeBool(r.Valid),
		"schemaVersion", prevention.SchemaVersion,
		"classification", classification,
		"claim", claim,
		"rootDigest", rootDigest,
		"externalRootStatus", r.ExternalRootStatus,
		"errors", stringList(r.Errors...),
	))
	if verified {
		e.out("Prevention verified\n")
	} else if r.Valid {
		e.out("Prevention evidence summary\n")
	}
	e.exitCode = 1
	if r.Valid {
		e.exitCode = 0
	}
	return nil
}

// readRawJSON is JSON.parse(readFileSync(resolve(file), "utf8")): the
// read and parse errors are thrown as they are.
func readRawJSON(file string) jsjson.Value {
	return jsexc.Must(bundle.ParseJSONFile(nodefs.Resolve(file)))
}

// preventionWrite ports the write branch (src/cli-app.ts:2586-2662).
func (e *env) preventionWrite(args []string) error {
	fromBundle, hasBundle := option(args, "--from-bundle")
	inputPath, hasInput := option(args, "--input")
	if hasBundle == hasInput {
		return fail("fl prevention write requires exactly one of --input <json> or --from-bundle <git-proof-bundle>.")
	}
	outputOption, hasOutput := option(args, "--output")
	if !hasOutput {
		outputOption = nodefs.Join(nodefs.Resolve(".faultline", "prevention-proofs"), "prevention-"+strconv.FormatInt(clock.NowMillis(), 10))
	}
	output := nodefs.Resolve(outputOption)
	if hasBundle {
		return e.preventionWriteFromBundle(args, fromBundle, output)
	}

	input := readRawJSON(inputPath)
	written, err := prevention.Write(input, output)
	if err != nil {
		return err
	}
	status := written.Manifest.Get("classification").Str()
	var agentsMd jsjson.Value
	if status == preventionVerified {
		agentsMd = upsertAgentsMdFromPrevention(repoOption(args), written)
	}
	claim := preventionSummaryClaim
	if status == preventionVerified {
		claim = preventionVerifiedClaim
	}
	out := obj(
		"status", status,
		"claim", claim,
		"directory", written.Directory,
		"rootDigest", written.RootDigest,
		"originalProofRoot", written.Body.Get("originalProofRoot"),
		"frozenWitnessDigest", written.Body.Get("frozenWitnessDigest"),
	)
	if agentsMd.Kind() != jsjson.Undefined {
		out.Obj().Set("agentsMd", agentsMd)
	}
	e.printJSON(out)
	if status == preventionVerified {
		e.out("Prevention verified\n")
	} else {
		e.out("Prevention evidence summary\n")
	}
	return nil
}

// preventionWriteFromBundle is the --from-bundle branch
// (src/cli-app.ts:2594-2640). The builder may throw a TS TypeError (a null
// repaired run); it reaches the CLI's catch like any uncaught error.
func (e *env) preventionWriteFromBundle(args []string, fromBundle, output string) error {
	repairedRunsPath, err := requiredOption(args, "--repaired-runs")
	if err != nil {
		return err
	}
	document := readRawJSON(repairedRunsPath)
	if document.Kind() == jsjson.Null {
		return fail("Cannot read properties of null (reading 'runs')")
	}
	runs := document.Get("runs")
	if runs.Kind() != jsjson.Array {
		return fail("--repaired-runs must be a JSON object with a runs array of three repaired NATIVE_DOCKER bindings.")
	}
	// The options literal, in its evaluation order.
	o := prevention.ArtifactOptions{Bundle: nodefs.Resolve(fromBundle), RepairedRuns: runs}
	if o.ExpectedRoot, err = requiredOption(args, "--expect-root"); err != nil {
		return err
	}
	if o.RepairedCommit, err = requiredOption(args, "--repaired-commit"); err != nil {
		return err
	}
	if o.RepairedTree, err = requiredOption(args, "--repaired-tree"); err != nil {
		return err
	}
	patch, err := requiredOption(args, "--patch")
	if err != nil {
		return err
	}
	if o.PatchDigest, err = prevention.DigestRepairPatchFile(nodefs.Resolve(patch)); err != nil {
		return err
	}
	if _, ok := option(args, "--codex-thread-id"); ok {
		if o.CodexThreadID, err = requiredOption(args, "--codex-thread-id"); err != nil {
			return err
		}
		o.HasThreadID = true
	}

	written, reasons := prevention.WriteFromArtifacts(o, output)
	if reasons != nil {
		e.printJSON(obj("status", "PREVENTION_EXPORT_FAILED", "reasons", stringList(reasons...)))
		e.exitCode = 1
		return nil
	}
	agentsMd := upsertAgentsMdFromPrevention(repoOption(args), written)
	out := obj(
		"status", preventionVerified,
		"claim", preventionVerifiedClaim,
		"directory", written.Directory,
		"rootDigest", written.RootDigest,
		"originalProofRoot", written.Body.Get("originalProofRoot"),
		"frozenWitnessDigest", written.Body.Get("frozenWitnessDigest"),
		"classification", written.Manifest.Get("classification"),
	)
	if agentsMd.Kind() != jsjson.Undefined {
		out.Obj().Set("agentsMd", agentsMd)
	}
	e.printJSON(out)
	e.out("Prevention verified\n")
	return nil
}

// upsertAgentsMdFromPrevention ports src/cli-app.ts:2539: {path, status}
// for a PREVENTION_VERIFIED package, undefined otherwise or when the upsert
// throws.
func upsertAgentsMdFromPrevention(repository string, written prevention.Written) jsjson.Value {
	if written.Manifest.Get("classification").Str() != preventionVerified {
		return jsjson.Value{}
	}
	status, err := prevention.UpsertAgentsMd(repository, written.Body)
	if err != nil {
		return jsjson.Value{}
	}
	// upsertFaultLineAgentsMd's path: repository is already absolute.
	return obj("path", nodefs.Join(nodefs.Resolve(repository), "AGENTS.md"), "status", status)
}
