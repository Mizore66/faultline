package gitproof

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
)

// digestBytes ports git-proof-bundle.ts:214 for Buffer input.
func digestBytes(b []byte) string { return "sha256:" + canonical.SHA256HexBytes(b) }

func isLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }

// safeArtifactPath ports git-proof-bundle.ts:230.
func safeArtifactPath(root, artifact string) string {
	if !isSafeRelativeArtifactPath(artifact) {
		bundle.Throw(errors.New("Unsafe artifact path: " + artifact))
	}
	destination := nodefs.Resolve(root, artifact)
	nested := nodefs.Relative(root, destination)
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) {
		bundle.Throw(errors.New("Artifact path escapes its bundle: " + artifact))
	}
	return destination
}

// assertNoLinksOrSpecialFiles ports git-proof-bundle.ts:241.
func assertNoLinksOrSpecialFiles(directory string) {
	stat := bundle.Must(nodefs.Lstat(directory))
	if isLink(stat) || !stat.IsDir() {
		bundle.Throw(errors.New("Git proof bundle must be a real directory: " + directory))
	}
	for _, name := range bundle.Must(nodefs.ReadDirNames(directory)) {
		child := nodefs.Join(directory, name)
		childStat := bundle.Must(nodefs.Lstat(child))
		if isLink(childStat) || (!childStat.IsDir() && !childStat.Mode().IsRegular()) {
			bundle.Throw(errors.New("Git proof bundle contains a symbolic link or special file: " + child))
		}
		if childStat.IsDir() {
			assertNoLinksOrSpecialFiles(child)
		}
	}
}

// assertRegularFile ports git-proof-bundle.ts:302.
func assertRegularFile(path, label string, maximumBytes int64) int64 {
	stat := bundle.Must(nodefs.Lstat(path))
	if isLink(stat) || !stat.Mode().IsRegular() {
		bundle.Throw(errors.New(label + " must be a regular non-symlink file"))
	}
	if stat.Size() > maximumBytes {
		bundle.Throw(fmt.Errorf("%s exceeds FaultLine's %d byte read limit", label, maximumBytes))
	}
	return stat.Size()
}

// readBoundedFile ports git-proof-bundle.ts:309.
func readBoundedFile(path, label string, maximumBytes int64) []byte {
	assertRegularFile(path, label, maximumBytes)
	return bundle.Must(nodefs.ReadBytes(path))
}

type fileBudget struct{ files, bytes int64 }

// collectFiles ports git-proof-bundle.ts:314.
func collectFiles(root, current string, budget *fileBudget) []string {
	var files []string
	for _, name := range bundle.Must(nodefs.ReadDirNames(current)) {
		child := nodefs.Join(current, name)
		stat := bundle.Must(nodefs.Lstat(child))
		if isLink(stat) || (!stat.IsDir() && !stat.Mode().IsRegular()) {
			bundle.Throw(errors.New("Git proof bundle contains a symbolic link or special file: " + child))
		}
		if stat.IsDir() {
			files = append(files, collectFiles(root, child, budget)...)
			continue
		}
		if stat.Size() > maxSourceArtifactBytes {
			bundle.Throw(errors.New("Git proof bundle artifact exceeds FaultLine's read limit: " + child))
		}
		budget.files++
		budget.bytes += stat.Size()
		if budget.files > maxGitProofArtifacts {
			bundle.Throw(errors.New("Git proof bundle contains too many files to verify safely"))
		}
		if budget.bytes > maxGitProofTotalBytes {
			bundle.Throw(errors.New("Git proof bundle exceeds FaultLine's total verification read limit"))
		}
		files = append(files, strings.ReplaceAll(nodefs.Relative(root, child), `\`, "/"))
	}
	return files
}

// runArtifactPath ports git-proof-bundle.ts:346.
func runArtifactPath(run jsjson.Value) string {
	return "runs/" + strings.TrimPrefix(run.Get("runId").Str(), "sha256:") + ".json"
}

// transitionArtifactPath ports git-proof-bundle.ts:350.
func transitionArtifactPath(index int) string { return fmt.Sprintf("transitions/%04d.json", index) }

// requiredArtifactPaths ports git-proof-bundle.ts:354.
func requiredArtifactPaths(result jsjson.Value, lifecycleBound bool) []string {
	paths := []string{"manifest.json", "investigation.json", "witness/frozen.json", "source/metadata.json", "source/descendant.bundle", "source/range.patch", "VERIFY.md"}
	if lifecycleBound {
		paths = append(paths, "lifecycle/ledger.json")
	}
	for _, run := range result.Get("runs").Items() {
		paths = append(paths, runArtifactPath(run))
	}
	for i := range result.Get("transitions").Items() {
		paths = append(paths, transitionArtifactPath(i))
	}
	return canonical.SortLocale(paths)
}

// parseJSONFile ports parseJsonFile (git-proof-bundle.ts:852); Undefined on failure.
func parseJSONFile(root, artifact, label string, errs *[]string) jsjson.Value {
	var out jsjson.Value
	if err := bundle.Try(func() {
		path := safeArtifactPath(root, artifact)
		out = bundle.Must(jsjson.Parse(nodefs.DecodeUTF8(readBoundedFile(path, label, maxSourceArtifactBytes))))
	}); err != nil {
		*errs = append(*errs, label+" JSON validation failed: "+err.Error())
		return jsjson.Value{}
	}
	return out
}

// orderedMap is a JS Map<string, string>.
type orderedMap struct {
	keys   []string
	values map[string]string
}

func (m *orderedMap) has(k string) bool { _, ok := m.values[k]; return ok }

// parseCatalogLine is /^(?<digest>[a-f0-9]{64})  (?<artifact>.+)$/ where JS
// `.` excludes \r, U+2028, and U+2029 (\n cannot occur after splitting).
func parseCatalogLine(line string) (digest, artifact string, ok bool) {
	if len(line) < 67 || line[64:66] != "  " || !isHex(line[:64]) {
		return "", "", false
	}
	artifact = line[66:]
	if strings.ContainsAny(artifact, "\r  ") {
		return "", "", false
	}
	return line[:64], artifact, true
}

// parseHashCatalog ports git-proof-bundle.ts:862.
func parseHashCatalog(hashes []byte, errs *[]string) *orderedMap {
	catalog := &orderedMap{values: map[string]string{}}
	source := nodefs.DecodeUTF8(hashes)
	if !strings.HasSuffix(source, "\n") {
		*errs = append(*errs, "hashes.txt must end with a newline")
	}
	for _, line := range strings.Split(source, "\n") {
		if line == "" {
			continue
		}
		digest, artifact, ok := parseCatalogLine(line)
		if !ok {
			*errs = append(*errs, "invalid hash catalog entry: "+line)
			continue
		}
		if !isSafeRelativeArtifactPath(artifact) {
			*errs = append(*errs, "hash catalog path is unsafe: "+artifact)
			continue
		}
		if catalog.has(artifact) {
			*errs = append(*errs, "hash catalog lists an artifact more than once: "+artifact)
			continue
		}
		if len(catalog.keys) >= maxGitProofArtifacts {
			*errs = append(*errs, "hash catalog lists too many artifacts to verify safely")
			break
		}
		catalog.keys = append(catalog.keys, artifact)
		catalog.values[artifact] = digest
	}
	return catalog
}

// expectedManifestArtifacts ports git-proof-bundle.ts:970.
func expectedManifestArtifacts(result, lifecycle jsjson.Value) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("investigation", jsjson.MakeString("investigation.json"))
	o.Set("frozenWitness", jsjson.MakeString("witness/frozen.json"))
	var runs, transitions []jsjson.Value
	for _, run := range result.Get("runs").Items() {
		r := jsjson.NewObj()
		r.Set("runId", run.Get("runId"))
		r.Set("path", jsjson.MakeString(runArtifactPath(run)))
		runs = append(runs, jsjson.MakeObject(r))
	}
	for i := range result.Get("transitions").Items() {
		t := jsjson.NewObj()
		t.Set("index", jsjson.MakeNumber(float64(i)))
		t.Set("path", jsjson.MakeString(transitionArtifactPath(i)))
		transitions = append(transitions, jsjson.MakeObject(t))
	}
	o.Set("runs", jsjson.MakeArray(runs))
	o.Set("transitions", jsjson.MakeArray(transitions))
	o.Set("sourceMetadata", jsjson.MakeString("source/metadata.json"))
	o.Set("gitBundle", jsjson.MakeString("source/descendant.bundle"))
	o.Set("rangePatch", jsjson.MakeString("source/range.patch"))
	o.Set("verification", jsjson.MakeString("VERIFY.md"))
	if hasLifecycleLedger(lifecycle) {
		o.Set("lifecycleLedger", lifecycle.Get("path"))
	}
	return jsjson.MakeObject(o)
}

var sha256DigestRe = regexp.MustCompile(sha256DigestSource)

// stringSet is a JS Set<string>.
type stringSet struct {
	order []string
	has   map[string]bool
}

func newStringSet(values ...string) *stringSet {
	s := &stringSet{has: map[string]bool{}}
	for _, v := range values {
		if !s.has[v] {
			s.has[v] = true
			s.order = append(s.order, v)
		}
	}
	return s
}

// Verify ports verifyGitInvestigationProofBundle (src/git-proof-bundle.ts:1142).
func Verify(directory, expectedRoot string, rootProvided bool) (result bundle.Result) {
	var errs []string
	var rootDigest *string
	status := "NOT_PROVIDED"
	if rootProvided {
		status = "MISMATCH"
	}
	defer bundle.Catch(func(err error) {
		errs = append(errs, "Git proof bundle verification failed safely: "+err.Error())
		result = bundle.Result{Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
	})

	root := nodefs.Resolve(directory)
	if !nodefs.Exists(root) {
		return bundle.Result{Errors: []string{"Git proof bundle directory does not exist"}, ExternalRootStatus: status}
	}
	assertNoLinksOrSpecialFiles(root)
	hashesPath := nodefs.Join(root, "hashes.txt")
	rootPath := nodefs.Join(root, "ROOT.sha256")
	assertRegularFile(hashesPath, "hashes.txt", maxHashCatalogBytes)
	assertRegularFile(rootPath, "ROOT.sha256", 1024)
	hashes := readBoundedFile(hashesPath, "hashes.txt", maxHashCatalogBytes)
	calculated := digestBytes(hashes)
	rootDigest = &calculated
	if jsstr.Trim(nodefs.DecodeUTF8(readBoundedFile(rootPath, "ROOT.sha256", 1024))) != calculated {
		errs = append(errs, "ROOT.sha256 does not match hashes.txt")
	}
	if rootProvided {
		switch {
		case !sha256DigestRe.MatchString(expectedRoot):
			errs = append(errs, "externally supplied Git proof root is not a sha256 digest")
		case expectedRoot == calculated:
			status = "MATCH"
		default:
			errs = append(errs, "externally supplied Git proof root does not match")
		}
	}
	catalog := parseHashCatalog(hashes, &errs)
	var declaredBytes int64
	for _, artifact := range catalog.keys {
		stop := false
		if err := bundle.Try(func() {
			path := safeArtifactPath(root, artifact)
			label := "declared artifact " + artifact
			declaredBytes += assertRegularFile(path, label, maxSourceArtifactBytes)
			if declaredBytes > maxGitProofTotalBytes {
				errs = append(errs, "declared artifacts exceed FaultLine's total verification read limit")
				stop = true
				return
			}
			if canonical.SHA256HexBytes(readBoundedFile(path, label, maxSourceArtifactBytes)) != catalog.values[artifact] {
				errs = append(errs, "artifact digest mismatch: "+artifact)
			}
		}); err != nil {
			errs = append(errs, "declared artifact cannot be read safely ("+artifact+"): "+err.Error())
		}
		if stop {
			break
		}
	}

	manifestPayload := parseJSONFile(root, "manifest.json", "manifest", &errs)
	investigationPayload := parseJSONFile(root, "investigation.json", "investigation", &errs)
	frozenPayload := parseJSONFile(root, "witness/frozen.json", "frozen witness", &errs)
	metadataPayload := parseJSONFile(root, "source/metadata.json", "source metadata", &errs)
	manifest, manifestIssues, haveManifest := schema.Parse(GitProofBundleManifestSchema, manifestPayload)
	if !haveManifest {
		errs = append(errs, "manifest schema validation failed: "+schema.ErrorMessage(manifestIssues))
	}
	investigation, investigationIssues, haveResult := schema.Parse(GitInvestigationResultSchema, investigationPayload)
	if !haveResult {
		errs = append(errs, "investigation schema validation failed: "+schema.ErrorMessage(investigationIssues))
	}
	frozen, frozenIssues, haveFrozen := schema.Parse(FrozenWitnessSchema, frozenPayload)
	if !haveFrozen {
		errs = append(errs, "frozen witness schema validation failed: "+schema.ErrorMessage(frozenIssues))
	}
	metadata, metadataIssues, haveMetadata := schema.Parse(GitProofSourceMetadataSchema, metadataPayload)
	if !haveMetadata {
		errs = append(errs, "source metadata schema validation failed: "+schema.ErrorMessage(metadataIssues))
	}

	if haveManifest && haveResult && haveFrozen && haveMetadata {
		if manifest.Get("investigationDigest").Str() != bundle.Must(canonical.DigestJSON(investigation)) {
			errs = append(errs, "manifest investigation digest does not match investigation.json")
		}
		if manifest.Get("frozenDigest").Str() != frozen.Get("frozenDigest").Str() || manifest.Get("witnessDigest").Str() != frozen.Get("witnessDigest").Str() {
			errs = append(errs, "manifest frozen or witness digest does not match frozen witness artifact")
		}
		if investigation.Get("resolvedRange").Kind() == jsjson.Null || !bundle.SameCanonical(manifest.Get("resolvedRange"), investigation.Get("resolvedRange")) {
			errs = append(errs, "manifest range does not match investigation resolved range")
		}
		if !bundle.SameCanonical(manifest.Get("artifacts"), expectedManifestArtifacts(investigation, manifest.Get("lifecycle"))) {
			errs = append(errs, "manifest artifact index does not exactly match the investigation run and transition catalog")
		}
		expectedArtifacts := newStringSet(requiredArtifactPaths(investigation, hasLifecycleLedger(manifest.Get("lifecycle")))...)
		for _, artifact := range expectedArtifacts.order {
			if !catalog.has(artifact) {
				errs = append(errs, "required artifact is missing from hashes.txt: "+artifact)
			}
		}
		for _, artifact := range catalog.keys {
			if !expectedArtifacts.has[artifact] {
				errs = append(errs, "hashes.txt contains an unexpected artifact: "+artifact)
			}
		}
		physical := newStringSet(collectFiles(root, root, &fileBudget{})...)
		expectedPhysical := newStringSet(append(append([]string{}, expectedArtifacts.order...), "hashes.txt", "ROOT.sha256")...)
		for _, artifact := range physical.order {
			if !expectedPhysical.has[artifact] {
				errs = append(errs, "undeclared physical file exists in Git proof bundle: "+artifact)
			}
		}
		for _, artifact := range expectedPhysical.order {
			if !physical.has[artifact] {
				errs = append(errs, "expected physical file is missing from Git proof bundle: "+artifact)
			}
		}

		runs := map[string]jsjson.Value{} // new Map: the last duplicate wins
		for _, run := range investigation.Get("runs").Items() {
			runs[run.Get("runId").Str()] = run
		}
		for _, descriptor := range manifest.Get("artifacts", "runs").Items() {
			runID := descriptor.Get("runId").Str()
			payload := parseJSONFile(root, descriptor.Get("path").Str(), "run "+runID, &errs)
			parsed, issues, ok := schema.Parse(GitInvestigationRunFactSchema, payload)
			if !ok {
				errs = append(errs, "run artifact schema validation failed for "+runID+": "+schema.ErrorMessage(issues))
			} else if parsed.Get("runId").Str() != runID || !bundle.SameCanonical(parsed, runs[runID]) {
				errs = append(errs, "run artifact does not match investigation run catalog: "+runID)
			}
		}
		transitions := investigation.Get("transitions").Items()
		for index, descriptor := range manifest.Get("artifacts", "transitions").Items() {
			label := strconv.Itoa(index)
			payload := parseJSONFile(root, descriptor.Get("path").Str(), "transition "+label, &errs)
			parsed, issues, ok := schema.Parse(StableGitTransitionSchema, payload)
			if !ok {
				errs = append(errs, "transition artifact schema validation failed for "+label+": "+schema.ErrorMessage(issues))
				continue
			}
			var expected jsjson.Value // result.transitions[index]: undefined when out of range
			if index < len(transitions) {
				expected = transitions[index]
			}
			if descriptor.Get("index").Num() != float64(index) || !bundle.SameCanonical(parsed, expected) {
				errs = append(errs, "transition artifact does not match investigation transition catalog: "+label)
			}
		}
		witness := VerifyFrozenWitnessRecord(frozen, frozen.Get("frozenDigest").Str(), true)
		if !witness.Valid || witness.ExternalDigestStatus != "MATCH" {
			for _, e := range witness.Errors {
				errs = append(errs, "frozen witness verification failed: "+e)
			}
		}
		errs = append(errs, validateGitInvestigationProofSemantics(investigation, frozen)...)
		verifyLifecycleBinding(root, manifest, investigation, &errs)
		sourceMetadataFromArtifacts(root, metadata, investigation, manifest, &errs)
		verifyPortableGitSource(root, metadata, investigation, &errs)
	}
	checked := len(catalog.keys)
	if !haveManifest {
		return bundle.Result{CheckedFiles: checked, Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
	}
	return bundle.Result{Valid: len(errs) == 0, CheckedFiles: checked, Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
}
