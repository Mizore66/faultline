package turnproof

import (
	"regexp"
	"strconv"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/turninv"
)

// Verification ports TurnProofBundleVerification (turn-proof-bundle.ts:166).
type Verification struct {
	bundle.Result              // Valid, CheckedFiles, Errors, RootDigest, ExternalRootStatus
	Manifest      jsjson.Value // the schema-parsed manifest; Undefined when absent
}

var sha256Digest = regexp.MustCompile(sha256DigestSource)

// digestBytes ports digestBytes (:179) for a Buffer.
func digestBytes(b []byte) string { return "sha256:" + canonical.SHA256HexBytes(b) }

// parseJSONFile ports parseJsonFile (:380): Undefined on failure.
func parseJSONFile(root, artifact, label string, errs *[]string) jsjson.Value {
	var out jsjson.Value
	if err := bundle.Try(func() {
		path := safeArtifactPath(root, artifact)
		out = bundle.Must(jsjson.Parse(jsstr.DecodeUTF8(readBoundedFile(path, label, MaxSourceArtifactBytes))))
	}); err != nil {
		*errs = append(*errs, jsexc.Concat(label, " JSON validation failed: ", jsexc.Message(err)))
		return jsjson.Value{}
	}
	return out
}

// stringSet is a JS Set<string> in insertion order.
type stringSet struct {
	order []string
	has   map[string]bool
}

func newStringSet(values ...string) *stringSet {
	s := &stringSet{has: map[string]bool{}}
	for _, v := range values {
		if !s.has[v] {
			jsexc.SetAdd(s.has, v)
			s.order = append(s.order, v)
		}
	}
	return s
}

// treeDigests is result.states' treeDigest list, as verifyTreesPack reads it.
func treeDigests(result jsjson.Value) []string {
	var out []string
	for _, state := range result.Get("states").Items() {
		out = append(out, state.Get("treeDigest").Str())
	}
	return out
}

// Verify ports verifyTurnInvestigationProofBundle (:812). rootProvided is
// `expectedRoot !== undefined`, so an empty expectedRoot is provided.
func Verify(directory, expectedRoot string, rootProvided bool) (v Verification) {
	defer jsjson.UseCallSite(jsjson.SiteTurnProof)()
	var errs []string
	var rootDigest *string
	status := "NOT_PROVIDED"
	if rootProvided {
		status = "MISMATCH"
	}
	manifest := jsjson.Value{}
	defer bundle.Catch(func(err error) {
		errs = append(errs, jsexc.Concat("Turn proof bundle verification failed safely: ", jsexc.Message(err)))
		v = Verification{Result: bundle.Result{Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}, Manifest: manifest}
	})

	root := nodefs.Resolve(directory)
	if !nodefs.Exists(root) {
		return Verification{Result: bundle.Result{Errors: []string{"Turn proof bundle directory does not exist"}, ExternalRootStatus: status}}
	}
	assertNoLinksOrSpecialFiles(root)
	hashesPath := nodefs.Join(root, "hashes.txt")
	rootPath := nodefs.Join(root, "ROOT.sha256")
	assertRegularFile(hashesPath, "hashes.txt", MaxHashCatalogBytes)
	assertRegularFile(rootPath, "ROOT.sha256", 1024)
	hashes := readBoundedFile(hashesPath, "hashes.txt", MaxHashCatalogBytes)
	calculated := digestBytes(hashes)
	rootDigest = &calculated
	storedRoot := jsstr.Trim(jsstr.DecodeUTF8(readBoundedFile(rootPath, "ROOT.sha256", 1024)))
	if storedRoot != calculated {
		errs = append(errs, "ROOT.sha256 does not match hashes.txt")
	}
	if rootProvided {
		switch {
		case !sha256Digest.MatchString(expectedRoot):
			errs = append(errs, "externally supplied turn proof root is not a sha256 digest")
		case expectedRoot == calculated:
			status = "MATCH"
		default:
			errs = append(errs, "externally supplied turn proof root does not match")
		}
	}
	catalog := ParseHashCatalog(hashes, &errs)
	var declaredBytes int64
	for _, artifact := range catalog.Keys {
		stop := false
		if err := bundle.Try(func() {
			path := safeArtifactPath(root, artifact)
			label := jsexc.Concat("declared artifact ", artifact)
			declaredBytes += assertRegularFile(path, label, MaxSourceArtifactBytes)
			if declaredBytes > MaxTotalBytes {
				errs = append(errs, "declared artifacts exceed FaultLine's total verification read limit")
				stop = true
				return
			}
			if canonical.SHA256HexBytes(readBoundedFile(path, label, MaxSourceArtifactBytes)) != catalog.Digests[artifact] {
				errs = append(errs, jsexc.Concat("artifact digest mismatch: ", artifact))
			}
		}); err != nil {
			errs = append(errs, jsexc.Concat("declared artifact cannot be read safely (", artifact, "): ", jsexc.Message(err)))
		}
		if stop {
			break
		}
	}

	manifestPayload := parseJSONFile(root, "manifest.json", "manifest", &errs)
	investigationPayload := parseJSONFile(root, "investigation.json", "investigation", &errs)
	frozenPayload := parseJSONFile(root, "witness/frozen.json", "frozen witness", &errs)
	metadataPayload := parseJSONFile(root, "source/trees.metadata.json", "source metadata", &errs)
	parsedManifest, manifestIssues, haveManifest := schema.Parse(ManifestSchema, manifestPayload)
	if !haveManifest {
		errs = append(errs, jsexc.Concat("manifest schema validation failed: ", schema.ErrorMessage(manifestIssues)))
	} else {
		manifest = parsedManifest
	}
	result, resultIssues, haveResult := schema.Parse(turninv.ResultSchema, investigationPayload)
	if !haveResult {
		errs = append(errs, jsexc.Concat("investigation schema validation failed: ", schema.ErrorMessage(resultIssues)))
	}
	frozen, frozenIssues, haveFrozen := schema.Parse(gitproof.FrozenWitnessSchema, frozenPayload)
	if !haveFrozen {
		errs = append(errs, jsexc.Concat("frozen witness schema validation failed: ", schema.ErrorMessage(frozenIssues)))
	}
	metadata, metadataIssues, haveMetadata := schema.Parse(SourceMetadataSchema, metadataPayload)
	if !haveMetadata {
		errs = append(errs, jsexc.Concat("source metadata schema validation failed: ", schema.ErrorMessage(metadataIssues)))
	}

	if haveManifest && haveResult && haveFrozen && haveMetadata {
		if manifest.Get("investigationDigest").Str() != bundle.Must(canonical.DigestJSON(result)) {
			errs = append(errs, "manifest investigation digest does not match investigation.json")
		}
		if manifest.Get("frozenDigest").Str() != frozen.Get("frozenDigest").Str() || manifest.Get("witnessDigest").Str() != frozen.Get("witnessDigest").Str() {
			errs = append(errs, "manifest frozen or witness digest does not match frozen witness artifact")
		}
		if !bundle.SameCanonical(manifest.Get("artifacts"), ExpectedManifestArtifacts(result)) {
			errs = append(errs, "manifest artifact index does not exactly match the investigation run and transition catalog")
		}
		expectedArtifacts := newStringSet(RequiredArtifactPaths(result)...)
		for _, artifact := range expectedArtifacts.order {
			if _, ok := catalog.Digests[artifact]; !ok {
				errs = append(errs, jsexc.Concat("required artifact is missing from hashes.txt: ", artifact))
			}
		}
		for _, artifact := range catalog.Keys {
			if !expectedArtifacts.has[artifact] {
				errs = append(errs, jsexc.Concat("hashes.txt contains an unexpected artifact: ", artifact))
			}
		}
		physical := newStringSet(collectFiles(root, root, &fileBudget{})...)
		expectedPhysical := newStringSet(append(append([]string{}, expectedArtifacts.order...), "hashes.txt", "ROOT.sha256")...)
		for _, artifact := range physical.order {
			if !expectedPhysical.has[artifact] {
				errs = append(errs, jsexc.Concat("undeclared physical file exists in turn proof bundle: ", artifact))
			}
		}
		for _, artifact := range expectedPhysical.order {
			if !physical.has[artifact] {
				errs = append(errs, jsexc.Concat("expected physical file is missing from turn proof bundle: ", artifact))
			}
		}

		runs := map[string]jsjson.Value{} // new Map: the last duplicate runId wins
		for _, run := range result.Get("runs").Items() {
			jsexc.MapSet(runs, run.Get("runId").Str(), run)
		}
		for _, descriptor := range manifest.Get("artifacts", "runs").Items() {
			runID := descriptor.Get("runId").Str()
			payload := parseJSONFile(root, descriptor.Get("path").Str(), jsexc.Concat("run ", runID), &errs)
			parsed, issues, ok := schema.Parse(turninv.RunFactSchema, payload)
			if !ok {
				errs = append(errs, jsexc.Concat("run artifact schema validation failed for ", runID, ": ", schema.ErrorMessage(issues)))
			} else if parsed.Get("runId").Str() != runID || !bundle.SameCanonical(parsed, runs[runID]) {
				// runs.get of a runId no run has is undefined, and
				// canonicalJson(undefined) throws (to the outer catch).
				errs = append(errs, jsexc.Concat("run artifact does not match investigation run catalog: ", runID))
			}
		}
		transitions := result.Get("transitions").Items()
		for index, descriptor := range manifest.Get("artifacts", "transitions").Items() {
			label := strconv.Itoa(index)
			payload := parseJSONFile(root, descriptor.Get("path").Str(), "transition "+label, &errs)
			parsed, issues, ok := schema.Parse(turninv.StableTransitionSchema, payload)
			if !ok {
				errs = append(errs, jsexc.Concat("transition artifact schema validation failed for ", label, ": ", schema.ErrorMessage(issues)))
				continue
			}
			var expected jsjson.Value // result.transitions[index]: undefined past the end
			if index < len(transitions) {
				expected = transitions[index]
			}
			if descriptor.Get("index").Num() != float64(index) || !bundle.SameCanonical(parsed, expected) {
				errs = append(errs, jsexc.Concat("transition artifact does not match investigation transition catalog: ", label))
			}
		}

		ledgerPayload := parseJSONFile(root, "lifecycle/ledger.json", "lifecycle ledger", &errs)
		ledger, ledgerIssues, haveLedger := schema.Parse(gitproof.CodexLifecycleLedgerSchema, ledgerPayload)
		if !haveLedger {
			errs = append(errs, jsexc.Concat("lifecycle ledger schema validation failed: ", schema.ErrorMessage(ledgerIssues)))
		} else {
			var bindErr error
			if err := bundle.Try(func() {
				rebound, _, err := BindTurnLifecycle(ledger, result)
				if err != nil {
					bindErr = err
					return
				}
				if !bundle.SameCanonical(rebound, manifest.Get("lifecycle")) {
					errs = append(errs, "lifecycle ledger binding does not match its valid reconstruction")
				}
			}); err != nil {
				bindErr = err
			}
			if bindErr != nil {
				// errorMessage(error) runs in the catch block: a throwing
				// message getter escapes to the outer catch.
				errs = append(errs, jsexc.Concat("lifecycle ledger cannot bind to the investigation: ", jsexc.Message(bindErr)))
			}
		}

		var trees []jsjson.Value
		for _, state := range result.Get("states").Items() {
			trees = append(trees, jsjson.MustObject(
				"stateIndex", state.Get("index"),
				"turnOrdinal", state.Get("turnOrdinal"),
				"treeDigest", state.Get("treeDigest"),
				"snapshotDigest", state.Get("snapshotDigest"),
			))
		}
		if !bundle.SameCanonical(metadata.Get("trees"), jsjson.MakeArray(trees)) {
			errs = append(errs, "source tree metadata does not match investigation states")
		}
		// Outside any inner try: a missing or unreadable pack throws to the
		// outer catch.
		packPath := safeArtifactPath(root, "source/trees.pack")
		packBytes := readBoundedFile(packPath, "source/trees.pack", MaxSourceArtifactBytes)
		if digestBytes(packBytes) != metadata.Get("pack", "digest").Str() || float64(len(packBytes)) != metadata.Get("pack", "bytes").Num() {
			errs = append(errs, "source trees.pack digest or size does not match metadata")
		}
		if err := VerifyTreesPack(packBytes, treeDigests(result), &errs); err != nil {
			bundle.Throw(err)
		}

		witness := gitproof.VerifyFrozenWitnessRecord(frozen, frozen.Get("frozenDigest").Str(), true)
		if !witness.Valid || witness.ExternalDigestStatus != "MATCH" {
			for _, e := range witness.Errors {
				errs = append(errs, jsexc.Concat("frozen witness verification failed: ", e))
			}
		}
		// errors.push(...validateTurnInvestigationProofSemantics(…)): a
		// spread, which overflows V8's stack past SpreadTurnSemantics.
		semantic := ValidateProofSemantics(result, frozen)
		if !jsjson.SpreadFits(len(semantic), jsjson.SpreadTurnSemantics) {
			bundle.Throw(jsjson.ErrStackOverflow)
		}
		errs = append(errs, semantic...)
	}
	checked := len(catalog.Keys)
	if !haveManifest {
		return Verification{Result: bundle.Result{CheckedFiles: checked, Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}}
	}
	return Verification{
		Result:   bundle.Result{Valid: len(errs) == 0, CheckedFiles: checked, Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status},
		Manifest: manifest,
	}
}
