package turnproof

import (
	"errors"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/evidence"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	"github.com/Mizore66/faultline/internal/turninv"
)

// WriteOptions ports TurnProofBundleWriteOptions (turn-proof-bundle.ts:142);
// "" is undefined for ProofRoot, nil for GeneratedAt and Codex.
type WriteOptions struct {
	ProofRoot       string
	GeneratedAt     *string
	LifecycleLedger jsjson.Value
	Repository      string
	Codex           *gitproof.CodexThreadIDs
}

// Written ports WrittenTurnProofBundle (:159).
type Written struct {
	Directory, ManifestDigest, RootDigest string
	Manifest                              jsjson.Value
}

// DefaultProofRoot ports defaultTurnProofRoot (:175).
func DefaultProofRoot() string { return nodefs.Resolve(".faultline", "turn-proof-bundles") }

// jsonBytes ports jsonBytes (:183).
func jsonBytes(v jsjson.Value) []byte {
	return []byte(jsstr.ToUTF8(jsexc.Concat(bundle.Must(canonical.CanonicalJSON(v)), "\n")))
}

// ensureRealDirectoryTree ports ensureRealDirectoryTree (:220).
func ensureRealDirectoryTree(directory string) {
	absolute := nodefs.Resolve(directory)
	root := nodefs.ParseRoot(absolute)
	var parts []string
	if suffix := nodefs.Relative(root, absolute); suffix != "" {
		parts = strings.FieldsFunc(suffix, func(r rune) bool { return r == '/' || r == '\\' })
	}
	safe := func(path string) (string, bool) {
		resolved, ok, err := safedir.ResolveSafeDirectorySegment(path)
		if err != nil {
			bundle.Throw(err)
		}
		return resolved, ok
	}
	current := root
	if nodefs.Exists(current) {
		s, ok := safe(current)
		if !ok {
			bundle.Throw(errors.New(jsexc.Concat("Turn proof root is not a real directory: ", current)))
		}
		current = s
	}
	for _, part := range parts {
		current = nodefs.Join(current, part)
		if !nodefs.Exists(current) {
			if err := nodefs.Mkdir(current, 0o700); err != nil {
				bundle.Throw(err)
			}
		}
		s, ok := safe(current)
		if !ok {
			bundle.Throw(errors.New(jsexc.Concat("Turn proof root cannot traverse a symbolic link or non-directory: ", current)))
		}
		current = s
	}
}

// isNested ports isNested (:242).
func isNested(root, candidate string) bool {
	nested := bundle.Must(safedir.RelativeTrustedSystemPath(root, candidate))
	return nested != "" && !strings.HasPrefix(nested, "..") && !nodefs.IsAbsolute(nested)
}

// prepareFreshOutput ports prepareFreshOutput (:247).
func prepareFreshOutput(outputDirectory, proofRoot string) string {
	root := nodefs.Resolve(proofRoot)
	output := nodefs.Resolve(outputDirectory)
	if !isNested(root, output) {
		bundle.Throw(errors.New(jsexc.Concat("Turn proof bundle output must be a child directory of ", root)))
	}
	ensureRealDirectoryTree(root)
	ensureRealDirectoryTree(nodefs.Dirname(output))
	if nodefs.Exists(output) {
		bundle.Throw(errors.New(jsexc.Concat("Turn proof bundle output already exists and will not be replaced: ", output)))
	}
	return gitproof.AssertSafeProofOutput(output, root)
}

// verificationReadme ports verificationReadme (:358).
func verificationReadme() []byte {
	return []byte(jsstr.ToUTF8(strings.Join([]string{
		"# Verify this experimental turn investigation package",
		"",
		"## Evidence grade",
		"",
		evidence.TurnPathEvidenceLabelExperimental + " (`" + evidence.TurnPathEvidenceGradeExperimental + "`).",
		"This is not the same maturity tier as commit-path portable proof (`COMMIT_PROOF`).",
		"Do not treat a turn package as TURN_PROOF / COMMIT_PROOF. See evidence-grade.ts promotion criteria (external validation, turn-boundary counterfactuals, prevention integration, platform soak).",
		"",
		"## Handling warning",
		"",
		"This package retains a frozen witness, Codex lifecycle ledger, turn-tree object pack, and bounded recorded evidence so it can be independently checked. Treat it as sensitive incident material and share it only with authorized reviewers.",
		"",
		"Run FaultLine's turn package verifier with an externally retained root digest.",
		"",
		"The verifier never executes the frozen witness. It validates every declared byte, reconstructs stable states and transitions from raw run facts, rebinds the lifecycle ledger to turn states, and confirms each recorded tree object is present in the portable pack.",
		"",
		"An external root is necessary to detect an editor who rewrites the mutable hash catalog too.",
	}, "\n")))
}

// makeManifest ports makeManifest (the helper above the writer).
func makeManifest(result, frozen jsjson.Value, generatedAt string, lifecycle jsjson.Value, codex *gitproof.CodexThreadIDs) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(BundleSchemaVersion))
	o.Set("generatedAt", jsjson.MakeString(generatedAt))
	o.Set("integrityScope", jsjson.MakeString("complete-declared-file-set"))
	o.Set("investigationDigest", jsjson.MakeString(bundle.Must(canonical.DigestJSON(result))))
	o.Set("frozenDigest", frozen.Get("frozenDigest"))
	o.Set("witnessDigest", frozen.Get("witnessDigest"))
	o.Set("lifecycle", lifecycle)
	if codex != nil {
		section := jsjson.NewObj()
		if codex.WitnessDraftThreadID != nil {
			section.Set("witnessDraftThreadId", jsjson.MakeString(*codex.WitnessDraftThreadID))
		}
		if codex.RepairThreadID != nil {
			section.Set("repairThreadId", jsjson.MakeString(*codex.RepairThreadID))
		}
		if len(section.Keys()) > 0 {
			o.Set("codex", jsjson.MakeObject(section))
		}
	}
	o.Set("artifacts", ExpectedManifestArtifacts(result))
	return bundle.Must(bundle.ParseValue(jsjson.MakeObject(o), ManifestSchema))
}

// artifactSet is the writer's Map<string, Buffer>: insertion order kept.
type artifactSet struct {
	keys   []string
	values map[string][]byte
}

func (a *artifactSet) put(path string, b []byte) {
	if !IsSafeRelativeArtifactPath(path) {
		bundle.Throw(errors.New(jsexc.Concat("Unsafe generated artifact path: ", path))) // coverage gate: unreachable (generated paths are safe)
	}
	if _, ok := a.values[path]; ok {
		bundle.Throw(errors.New(jsexc.Concat("Turn proof bundle generated duplicate artifact: ", path))) // coverage gate: unreachable (a duplicate runId is refused by the semantics)
	}
	a.keys = append(a.keys, path)
	a.values[path] = b
}

// Write ports writeTurnInvestigationProofBundle (:723): a write-once package
// published only after the staged directory verifies against its own root.
// A thrown TS error is returned.
func Write(outputDirectory string, investigation, frozenWitness jsjson.Value, o WriteOptions) (written Written, err error) {
	err = bundle.Try(func() { written = write(outputDirectory, investigation, frozenWitness, o) })
	return written, err
}

func write(outputDirectory string, investigationInput, frozenInput jsjson.Value, options WriteOptions) Written {
	result := bundle.Must(bundle.ParseValue(investigationInput, turninv.ResultSchema))
	frozen := bundle.Must(bundle.ParseValue(frozenInput, gitproof.FrozenWitnessSchema))
	if semantic := ValidateProofSemantics(result, frozen); len(semantic) > 0 {
		bundle.Throw(errors.New(jsexc.Concat("Cannot write semantically inconsistent turn proof bundle: ", jsexc.Join(semantic, "; "))))
	}
	lifecycle, ledger, err := BindTurnLifecycle(options.LifecycleLedger, result)
	if err != nil {
		bundle.Throw(err)
	}
	var generatedAt string
	if options.GeneratedAt != nil {
		generatedAt = *options.GeneratedAt
	} else {
		generatedAt = clock.ISO(clock.Now())
	}
	bundle.Must(bundle.ParseValue(jsjson.MakeString(generatedAt), timestampSchema))
	proofRoot := options.ProofRoot
	if proofRoot == "" {
		proofRoot = DefaultProofRoot()
	}
	output := prepareFreshOutput(outputDirectory, proofRoot)
	parent := nodefs.Dirname(output)
	stage := nodefs.Join(parent, jsexc.Concat(".", nodefs.Relative(parent, output), ".staging-", clock.NewUUID()))
	var written Written
	if err := bundle.Try(func() {
		if err := nodefs.Mkdir(stage, 0o700); err != nil {
			bundle.Throw(err)
		}
		assertNoLinksOrSpecialFiles(stage)
		packBytes, err := PackTurnTrees(nodefs.Resolve(options.Repository), treeDigests(result))
		if err != nil {
			bundle.Throw(err)
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
		metadata := bundle.Must(bundle.ParseValue(jsjson.MustObject(
			"schemaVersion", jsjson.MakeString(SourceSchemaVersion),
			"trees", jsjson.MakeArray(trees),
			"pack", jsjson.MustObject(
				"path", jsjson.MakeString("source/trees.pack"),
				"digest", jsjson.MakeString(digestBytes(packBytes)),
				"bytes", jsjson.MakeNumber(float64(len(packBytes))),
			),
		), SourceMetadataSchema))
		manifest := makeManifest(result, frozen, generatedAt, lifecycle, options.Codex)
		artifacts := &artifactSet{values: map[string][]byte{}}
		artifacts.put("manifest.json", jsonBytes(manifest))
		artifacts.put("investigation.json", jsonBytes(result))
		artifacts.put("witness/frozen.json", jsonBytes(frozen))
		artifacts.put("source/trees.metadata.json", jsonBytes(metadata))
		artifacts.put("source/trees.pack", packBytes)
		artifacts.put("VERIFY.md", verificationReadme())
		artifacts.put("lifecycle/ledger.json", jsonBytes(ledger))
		for _, run := range result.Get("runs").Items() {
			artifacts.put(RunArtifactPath(run), jsonBytes(run))
		}
		for i, transition := range result.Get("transitions").Items() {
			artifacts.put(TransitionArtifactPath(i), jsonBytes(transition))
		}
		expected := RequiredArtifactPaths(result)
		actual := canonical.SortLocale(slices.Clone(artifacts.keys))
		if !slices.Equal(expected, actual) {
			bundle.Throw(errors.New("Turn proof writer did not produce the complete expected artifact set.")) // coverage gate: unreachable (the artifact list mirrors RequiredArtifactPaths)
		}
		for _, artifact := range artifacts.keys {
			writeArtifact(stage, artifact, artifacts.values[artifact])
		}
		var lines []string
		for _, artifact := range canonical.SortLocale(slices.Clone(artifacts.keys)) {
			lines = append(lines, canonical.SHA256HexBytes(artifacts.values[artifact])+"  "+artifact)
		}
		hashes := []byte(jsstr.ToUTF8(jsexc.Concat(strings.Join(lines, "\n"), "\n")))
		rootDigest := digestBytes(hashes)
		writeArtifact(stage, "hashes.txt", hashes)
		writeArtifact(stage, "ROOT.sha256", []byte(rootDigest+"\n"))
		if v := Verify(stage, rootDigest, true); !v.Valid {
			bundle.Throw(errors.New(jsexc.Concat("Refusing to publish an invalid turn proof bundle: ", jsexc.Join(v.Errors, "; "))))
		}
		if nodefs.Exists(output) {
			bundle.Throw(errors.New(jsexc.Concat("Turn proof bundle output appeared during assembly and will not be replaced: ", output))) // coverage gate: race only
		}
		if err := nodefs.Rename(stage, output); err != nil {
			bundle.Throw(err)
		}
		written = Written{Directory: output, ManifestDigest: bundle.Must(canonical.DigestJSON(manifest)), RootDigest: rootDigest, Manifest: manifest}
	}); err != nil {
		if nodefs.Exists(stage) {
			if rmErr := nodefs.RemoveAll(stage); rmErr != nil {
				bundle.Throw(rmErr)
			}
		}
		bundle.Throw(err)
	}
	return written
}
