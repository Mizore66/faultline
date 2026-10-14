package gitproof

import (
	"bytes"
	"errors"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
)

// WriteOptions is GitProofBundleWriteOptions (src/git-proof-bundle.ts:165).
type WriteOptions struct {
	ProofRoot       string  // "" = resolve(".faultline", "git-proof-bundles")
	GeneratedAt     *string // nil = clock
	LifecycleLedger *jsjson.Value
	Codex           *CodexThreadIDs
}

// CodexThreadIDs is the options.codex object: identifier strings only.
type CodexThreadIDs struct{ WitnessDraftThreadID, RepairThreadID *string }

// Written is WrittenGitProofBundle.
type Written struct {
	Directory, ManifestDigest, RootDigest string
	Manifest                              jsjson.Value
}

const rangePatchFormat = "git-diff --binary --full-index --no-ext-diff --no-textconv --no-renames"

// DefaultGitProofRoot ports defaultGitProofRoot (src/git-proof-bundle.ts:210).
func DefaultGitProofRoot() string { return nodefs.Resolve(".faultline", "git-proof-bundles") }

// jsonBytes ports jsonBytes (src/git-proof-bundle.ts:218).
func jsonBytes(v jsjson.Value) []byte {
	return []byte(jsstr.ToUTF8(jsexc.Concat(bundle.Must(canonical.CanonicalJSON(v)), "\n")))
}

// splitPathParts is suffix.split(/[\\/]+/).filter(Boolean).
func splitPathParts(suffix string) []string {
	return strings.FieldsFunc(suffix, func(r rune) bool { return r == '/' || r == '\\' })
}

// safeSegment is resolveSafeDirectorySegment with its lstat error thrown.
func safeSegment(path string) (string, bool) {
	resolved, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		bundle.Throw(err)
	}
	return resolved, ok
}

// realDirectoryTree is ensureRealDirectoryTree: src/git-proof-bundle.ts:257
// for the Git proof root, and src/proof-bundle.ts:73 (its own texts) when
// proofBundle is set.
func realDirectoryTree(directory string, proofBundle bool) {
	absolute := nodefs.Resolve(directory)
	root := nodefs.ParseRoot(absolute)
	var parts []string
	if suffix := nodefs.Relative(root, absolute); suffix != "" {
		parts = splitPathParts(suffix)
	}
	current := root
	if nodefs.Exists(current) {
		safe, ok := safeSegment(current)
		switch {
		case !ok && proofBundle:
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle root cannot traverse a symbolic link or non-directory: ", current))) // race only (the Git proof walk refused it first)
		case !ok:
			bundle.Throw(errors.New(jsexc.Concat("Git proof root is not a real directory: ", current)))
		}
		current = safe
	}
	for _, part := range parts {
		current = nodefs.Join(current, part)
		if !nodefs.Exists(current) {
			if err := nodefs.Mkdir(current, 0o700); err != nil {
				bundle.Throw(err)
			}
		}
		safe, ok := safeSegment(current)
		switch {
		case !ok && proofBundle:
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle root cannot traverse a symbolic link or non-directory: ", current))) // race only (the Git proof walk refused it first)
		case !ok:
			bundle.Throw(errors.New(jsexc.Concat("Git proof root cannot traverse a symbolic link or non-directory: ", current)))
		}
		current = safe
	}
}

// Port of src/git-proof-bundle.ts:257 ensureRealDirectoryTree.
func ensureRealDirectoryTree(directory string) { realDirectoryTree(directory, false) }

// Port of src/git-proof-bundle.ts:279 isNested.
func isNested(root, candidate string) bool {
	nested := bundle.Must(safedir.RelativeTrustedSystemPath(root, candidate))
	return nested != "" && !strings.HasPrefix(nested, "..") && !nodefs.IsAbsolute(nested)
}

// Port of src/proof-bundle.ts:102 assertSafeProofOutput.
func assertSafeProofOutput(outputDirectory, proofRoot string) string {
	output := nodefs.Resolve(outputDirectory)
	root := nodefs.Resolve(proofRoot)
	nestedPath := bundle.Must(safedir.RelativeTrustedSystemPath(root, output))
	if nestedPath == "" || strings.HasPrefix(nestedPath, "..") || nodefs.IsAbsolute(nestedPath) {
		bundle.Throw(errors.New(jsexc.Concat("Proof bundle output must be a child directory of ", root)))
	}
	realDirectoryTree(root, true)
	realDirectoryTree(nodefs.Dirname(output), true)
	current := root
	if nodefs.Exists(current) {
		if _, ok := safeSegment(current); !ok {
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle root cannot be a symbolic link: ", root)))
		}
	}
	for _, part := range splitPathParts(nestedPath) {
		current = nodefs.Join(current, part)
		if !nodefs.Exists(current) {
			continue
		}
		stat := bundle.Must(nodefs.Lstat(current))
		if isLink(stat) {
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle output cannot traverse a symbolic link: ", current)))
		}
		if current == output && !stat.IsDir() {
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle output must be a directory: ", output)))
		}
	}
	// TS then verifies an existing output as a replaceable demo bundle; the
	// writer reaches assertSafeProofOutput only after refusing an existing
	// output, so that branch is left unported.
	return output
}

// Port of src/git-proof-bundle.ts:288 prepareFreshOutput.
func prepareFreshOutput(outputDirectory, proofRoot string) string {
	root := nodefs.Resolve(proofRoot)
	output := nodefs.Resolve(outputDirectory)
	if !isNested(root, output) {
		bundle.Throw(errors.New(jsexc.Concat("Git proof bundle output must be a child directory of ", root)))
	}
	ensureRealDirectoryTree(root)
	ensureRealDirectoryTree(nodefs.Dirname(output))
	if nodefs.Exists(output) {
		bundle.Throw(errors.New(jsexc.Concat("Git proof bundle output already exists and will not be replaced: ", output)))
	}
	return assertSafeProofOutput(output, root)
}

// Port of src/git-proof-bundle.ts:339 writeArtifact.
func writeArtifact(root, artifact string, b []byte) {
	destination := safeArtifactPath(root, artifact)
	if err := nodefs.MkdirAll(nodefs.Dirname(destination), 0o700); err != nil {
		bundle.Throw(err)
	}
	if err := nodefs.WriteFileBytes(destination, b, 0o600, true); err != nil {
		bundle.Throw(err)
	}
	assertRegularFile(destination, jsexc.Concat("artifact ", artifact), maxSourceArtifactBytes)
}

type gitSource struct {
	repository           string
	ancestor, descendant jsjson.Value
	objectFormat         string
}

func stateValue(index int, commit, tree string) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("index", jsjson.MakeNumber(float64(index)))
	o.Set("commit", jsjson.MakeString(commit))
	o.Set("tree", jsjson.MakeString(tree))
	return jsjson.MakeObject(o)
}

// Port of src/git-proof-bundle.ts:420 sourceRepositoryFor.
func sourceRepositoryFor(result jsjson.Value) gitSource {
	resolved := result.Get("resolvedRange")
	if result.Get("repository").Kind() == jsjson.Null || resolved.Kind() == jsjson.Null {
		bundle.Throw(errors.New("Only a completed Git investigation with a resolved source range can be bundled."))
	}
	repository := nodefs.Resolve(gitText(result.Get("repository").Str(), "Git repository resolution", "rev-parse", "--show-toplevel"))
	ac, at := resolveGitState(repository, resolved.Get("ancestor", "commit").Str())
	dc, dt := resolveGitState(repository, resolved.Get("descendant", "commit").Str())
	if !bundle.SameCanonical(gitStateValue(ac, at), gitStateValue(resolved.Get("ancestor", "commit").Str(), resolved.Get("ancestor", "tree").Str())) ||
		!bundle.SameCanonical(gitStateValue(dc, dt), gitStateValue(resolved.Get("descendant", "commit").Str(), resolved.Get("descendant", "tree").Str())) {
		bundle.Throw(errors.New("The current Git repository no longer contains the exact investigation endpoint objects."))
	}
	if ancestry := runGit(repository, "merge-base", "--is-ancestor", ac, dc); ancestry.Err != nil || ancestry.Status == nil || *ancestry.Status != 0 {
		bundle.Throw(errors.New("The investigation ancestor is no longer an ancestor of its descendant in the source repository."))
	}
	listed := gitText(repository, "Git investigation range enumeration", "rev-list", "--reverse", "--ancestry-path", "--end-of-options", ac+".."+dc)
	commits := []string{ac}
	if ac != dc {
		commits = append(commits, splitLines(listed)...)
	}
	states := make([]jsjson.Value, len(commits))
	for i, commit := range commits {
		c, tree := resolveGitState(repository, commit)
		states[i] = stateValue(i, c, tree)
	}
	if !bundle.SameCanonical(jsjson.MakeArray(states), result.Get("states")) {
		bundle.Throw(errors.New("The source repository range does not exactly match the investigation state sequence."))
	}
	objectFormat := gitText(repository, "Git object-format resolution", "rev-parse", "--show-object-format")
	if objectFormat != "sha1" && objectFormat != "sha256" {
		bundle.Throw(errors.New(jsexc.Concat("Unsupported Git object format: ", objectFormat)))
	}
	return gitSource{repository: repository, ancestor: resolved.Get("ancestor"), descendant: resolved.Get("descendant"), objectFormat: objectFormat}
}

// removeTree is rmSync(path, { recursive: true, force: true }) in a finally
// block: its own failure escapes, as does rimrafSync's stack overflow.
func removeTree(path string) {
	if err := nodefs.RemoveAllDepth(path, jsjson.RimrafDepth); err != nil {
		if err == nodefs.ErrTooDeep {
			bundle.Throw(jsjson.ErrStackOverflow)
		}
		bundle.Throw(err)
	}
}

// Port of src/git-proof-bundle.ts:461 writePortableGitBundle.
func writePortableGitBundle(repository, descendant, destination string) (data []byte, headCommit string) {
	temporaryBare := bundle.Must(nodefs.Mkdtemp(nodefs.Join(nodefs.Tmpdir(), "faultline-git-proof-source-")))
	err := bundle.Try(func() {
		sourceRepo := toGitPath(repository)
		barePath := toGitPath(temporaryBare)
		bundlePath := toGitPath(destination)
		gitBytes("", "Temporary Git source clone", "clone", "--bare", "--shared", "--no-tags", "--quiet", sourceRepo, barePath)
		gitBytes(temporaryBare, "Temporary Git bundle ref creation", "update-ref", BundleHeadRef, descendant)
		gitBytes(temporaryBare, "Git bundle creation", "bundle", "create", bundlePath, BundleHeadRef)
		assertRegularFile(destination, "Git bundle", maxSourceArtifactBytes)
		heads := parseBundleHeads(gitBytes("", "Git bundle head listing", "bundle", "list-heads", bundlePath))
		if len(heads) != 1 || heads[0].ref != BundleHeadRef || heads[0].commit != descendant {
			bundle.Throw(errors.New("Git bundle did not expose exactly the expected descendant reference."))
		}
		gitBytes(temporaryBare, "Git bundle verification", "bundle", "verify", bundlePath)
		data = readBoundedFile(destination, "Git bundle", maxSourceArtifactBytes)
		if len(data) == 0 || len(data) > maxSourceArtifactBytes {
			bundle.Throw(errors.New("Git bundle is empty or exceeds FaultLine's portable artifact limit."))
		}
		headCommit = heads[0].commit
	})
	removeTree(temporaryBare)
	if err != nil {
		bundle.Throw(err)
	}
	return data, headCommit
}

// Port of src/git-proof-bundle.ts:569 unboundLifecycle.
func unboundLifecycle() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("status", jsjson.MakeString("UNBOUND"))
	o.Set("limitation", jsjson.MakeString("No caller-supplied Codex lifecycle ledger is bound to this Git investigation package."))
	return jsjson.MakeObject(o)
}

// Port of src/git-proof-bundle.ts:797 makeManifest.
func makeManifest(result, frozen jsjson.Value, generatedAt string, lifecycle jsjson.Value, codex *CodexThreadIDs) jsjson.Value {
	if result.Get("resolvedRange").Kind() == jsjson.Null {
		bundle.Throw(errors.New("Cannot create a manifest without a resolved Git range.")) // unreachable (sourceRepositoryFor refuses first)
	}
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(GitProofBundleSchemaVersion))
	o.Set("generatedAt", jsjson.MakeString(generatedAt))
	o.Set("integrityScope", jsjson.MakeString("complete-declared-file-set"))
	o.Set("investigationDigest", jsjson.MakeString(bundle.Must(canonical.DigestJSON(result))))
	o.Set("frozenDigest", frozen.Get("frozenDigest"))
	o.Set("witnessDigest", frozen.Get("witnessDigest"))
	o.Set("lifecycle", lifecycle)
	o.Set("resolvedRange", result.Get("resolvedRange"))
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
	runs := result.Get("runs").Items()
	runArtifacts := make([]jsjson.Value, len(runs))
	for i, run := range runs {
		a := jsjson.NewObj()
		a.Set("runId", run.Get("runId"))
		a.Set("path", jsjson.MakeString(runArtifactPath(run)))
		runArtifacts[i] = jsjson.MakeObject(a)
	}
	transitionArtifacts := make([]jsjson.Value, len(result.Get("transitions").Items()))
	for i := range transitionArtifacts {
		a := jsjson.NewObj()
		a.Set("index", jsjson.MakeNumber(float64(i)))
		a.Set("path", jsjson.MakeString(transitionArtifactPath(i)))
		transitionArtifacts[i] = jsjson.MakeObject(a)
	}
	artifacts := jsjson.NewObj()
	artifacts.Set("investigation", jsjson.MakeString("investigation.json"))
	artifacts.Set("frozenWitness", jsjson.MakeString("witness/frozen.json"))
	artifacts.Set("runs", jsjson.MakeArray(runArtifacts))
	artifacts.Set("transitions", jsjson.MakeArray(transitionArtifacts))
	artifacts.Set("sourceMetadata", jsjson.MakeString("source/metadata.json"))
	artifacts.Set("gitBundle", jsjson.MakeString("source/descendant.bundle"))
	artifacts.Set("rangePatch", jsjson.MakeString("source/range.patch"))
	artifacts.Set("verification", jsjson.MakeString("VERIFY.md"))
	if hasLifecycleLedger(lifecycle) {
		artifacts.Set("lifecycleLedger", lifecycle.Get("path"))
	}
	o.Set("artifacts", jsjson.MakeObject(artifacts))
	return bundle.Must(bundle.ParseValue(jsjson.MakeObject(o), GitProofBundleManifestSchema))
}

// Port of src/git-proof-bundle.ts:836 verificationReadme.
func verificationReadme() []byte {
	return []byte(strings.Join([]string{
		"# Verify this Git investigation package",
		"",
		"## Handling warning",
		"",
		"This package retains a frozen witness, Git object references, and bounded recorded evidence so it can be independently checked. Treat it as sensitive incident material and share it only with authorized reviewers.",
		"",
		"Run FaultLine's Git proof verifier with an externally retained root digest.",
		"",
		"The verifier never executes the frozen witness. It validates every declared byte, reconstructs the recorded stable states and transitions from raw run facts, and reconstructs the Git source range from the bundled descendant history before checking the binary range patch.",
		"",
		"An external root is necessary to detect an editor who rewrites the mutable hash catalog too.",
	}, "\n"))
}

// artifactSet is the writer's Map<string, Buffer>: insertion order kept.
type artifactSet struct {
	keys   []string
	values map[string][]byte
}

func (a *artifactSet) put(path string, b []byte) {
	if !isSafeRelativeArtifactPath(path) {
		bundle.Throw(errors.New(jsexc.Concat("Unsafe generated artifact path: ", path))) // unreachable (generated paths are safe)
	}
	if _, ok := a.values[path]; ok {
		bundle.Throw(errors.New(jsexc.Concat("Git proof bundle generated duplicate artifact: ", path))) // unreachable (run ids are distinct after semantic validation)
	}
	a.keys = append(a.keys, path)
	a.values[path] = b
}

// WriteGitInvestigationProofBundle ports writeGitInvestigationProofBundle
// (src/git-proof-bundle.ts:1029): a write-once package whose Git history is
// bundled from a temporary shared bare clone. A thrown TS error is returned.
func WriteGitInvestigationProofBundle(output string, result, frozen jsjson.Value, o WriteOptions) (written Written, err error) {
	err = bundle.Try(func() { written = writeGitInvestigationProofBundle(output, result, frozen, o) })
	return written, err
}

func writeGitInvestigationProofBundle(outputDirectory string, investigationInput, frozenInput jsjson.Value, options WriteOptions) Written {
	result := bundle.Must(bundle.ParseValue(investigationInput, GitInvestigationResultSchema))
	frozen := bundle.Must(bundle.ParseValue(frozenInput, FrozenWitnessSchema))
	var unsafeLocal []string
	for _, run := range result.Get("runs").Items() {
		if run.Get("result", "kind").Str() == "UNSAFE_LOCAL" || run.Get("sandbox", "kind").Str() == "UNSAFE_LOCAL" || run.Get("result", "executor").Str() == "UNSAFE_LOCAL" {
			unsafeLocal = append(unsafeLocal, run.Get("runId").Str())
		}
	}
	if len(unsafeLocal) > 0 {
		bundle.Throw(errors.New(jsexc.Concat("Refusing to compile Git proof bundle: UNSAFE_LOCAL run facts are structurally unexportable (", strings.Join(unsafeLocal, ", "), ").")))
	}
	if semanticErrors := validateGitInvestigationProofSemantics(result, frozen); len(semanticErrors) > 0 {
		bundle.Throw(errors.New(jsexc.Concat("Cannot write semantically inconsistent Git proof bundle: ", strings.Join(semanticErrors, "; "))))
	}
	lifecycle := unboundLifecycle()
	var ledger *jsjson.Value
	if options.LifecycleLedger != nil {
		binding, parsed := bindParsedLifecycleLedger(*options.LifecycleLedger, result)
		lifecycle, ledger = binding, &parsed
	}
	source := sourceRepositoryFor(result)
	var generatedAt string
	if options.GeneratedAt != nil {
		generatedAt = *options.GeneratedAt
	} else {
		generatedAt = clock.ISO(clock.Now())
	}
	bundle.Must(bundle.ParseValue(jsjson.MakeString(generatedAt), timestampSchema))
	proofRoot := options.ProofRoot
	if proofRoot == "" {
		proofRoot = DefaultGitProofRoot()
	}
	output := prepareFreshOutput(outputDirectory, proofRoot)
	parent := nodefs.Dirname(output)
	stage := nodefs.Join(parent, jsexc.Concat(".", nodefs.Relative(parent, output), ".staging-", clock.NewUUID()))
	var written Written
	err := bundle.Try(func() {
		if err := nodefs.Mkdir(stage, 0o700); err != nil {
			bundle.Throw(err)
		}
		assertNoLinksOrSpecialFiles(stage)
		bundleDestination := safeArtifactPath(stage, "source/descendant.bundle")
		if err := nodefs.MkdirAll(nodefs.Dirname(bundleDestination), 0o700); err != nil {
			bundle.Throw(err)
		}
		portableBytes, headCommit := writePortableGitBundle(source.repository, source.descendant.Get("commit").Str(), bundleDestination)
		rangePatch := binaryRangePatch(source.repository, source.ancestor.Get("commit").Str(), source.descendant.Get("commit").Str())

		bundleMeta := jsjson.NewObj()
		bundleMeta.Set("path", jsjson.MakeString("source/descendant.bundle"))
		bundleMeta.Set("digest", jsjson.MakeString(digestBytes(portableBytes)))
		bundleMeta.Set("bytes", jsjson.MakeNumber(float64(len(portableBytes))))
		bundleMeta.Set("headRef", jsjson.MakeString(BundleHeadRef))
		bundleMeta.Set("headCommit", jsjson.MakeString(headCommit))
		patchMeta := jsjson.NewObj()
		patchMeta.Set("path", jsjson.MakeString("source/range.patch"))
		patchMeta.Set("digest", jsjson.MakeString(digestBytes(rangePatch)))
		patchMeta.Set("bytes", jsjson.MakeNumber(float64(len(rangePatch))))
		patchMeta.Set("format", jsjson.MakeString(rangePatchFormat))
		meta := jsjson.NewObj()
		meta.Set("schemaVersion", jsjson.MakeString(GitProofSourceSchemaVersion))
		meta.Set("objectFormat", jsjson.MakeString(source.objectFormat))
		meta.Set("ancestor", source.ancestor)
		meta.Set("descendant", source.descendant)
		meta.Set("bundle", jsjson.MakeObject(bundleMeta))
		meta.Set("rangePatch", jsjson.MakeObject(patchMeta))
		metadata := bundle.Must(bundle.ParseValue(jsjson.MakeObject(meta), GitProofSourceMetadataSchema))
		manifest := makeManifest(result, frozen, generatedAt, lifecycle, options.Codex)

		artifacts := &artifactSet{values: map[string][]byte{}}
		artifacts.put("manifest.json", jsonBytes(manifest))
		artifacts.put("investigation.json", jsonBytes(result))
		artifacts.put("witness/frozen.json", jsonBytes(frozen))
		artifacts.put("source/metadata.json", jsonBytes(metadata))
		artifacts.put("source/descendant.bundle", portableBytes)
		artifacts.put("source/range.patch", rangePatch)
		artifacts.put("VERIFY.md", verificationReadme())
		if ledger != nil {
			artifacts.put("lifecycle/ledger.json", jsonBytes(*ledger))
		}
		for _, run := range result.Get("runs").Items() {
			artifacts.put(runArtifactPath(run), jsonBytes(run))
		}
		for i, transition := range result.Get("transitions").Items() {
			artifacts.put(transitionArtifactPath(i), jsonBytes(transition))
		}
		expected := requiredArtifactPaths(result, hasLifecycleLedger(lifecycle))
		actual := canonical.SortLocale(slices.Clone(artifacts.keys))
		if !slices.Equal(expected, actual) {
			bundle.Throw(errors.New("Git proof writer did not produce the complete expected artifact set.")) // unreachable (the artifact list mirrors requiredArtifactPaths)
		}
		for _, artifact := range artifacts.keys {
			b := artifacts.values[artifact]
			destination := safeArtifactPath(stage, artifact)
			if nodefs.Exists(destination) {
				if present := bundle.Must(nodefs.ReadBytes(destination)); !bytes.Equal(present, b) {
					bundle.Throw(errors.New(jsexc.Concat("Git proof source artifact changed before package assembly: ", artifact))) // race only (git wrote the bundle just before)
				}
				continue
			}
			writeArtifact(stage, artifact, b)
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
			bundle.Throw(errors.New(jsexc.Concat("Refusing to publish an invalid Git proof bundle: ", strings.Join(v.Errors, "; "))))
		}
		if nodefs.Exists(output) {
			bundle.Throw(errors.New(jsexc.Concat("Git proof bundle output appeared during assembly and will not be replaced: ", output))) // race only
		}
		if err := nodefs.Rename(stage, output); err != nil {
			bundle.Throw(err)
		}
		written = Written{Directory: output, ManifestDigest: bundle.Must(canonical.DigestJSON(manifest)), RootDigest: rootDigest, Manifest: manifest}
	})
	if err != nil {
		if nodefs.Exists(stage) {
			removeTree(stage)
		}
		bundle.Throw(err)
	}
	return written
}
