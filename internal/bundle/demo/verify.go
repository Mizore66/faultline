// Package demo ports the faultline.proof-bundle.v2 verifier in src/proof-bundle.ts.
package demo

import (
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

func isLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }

func assertNoLinksOrSpecialFiles(directory string) {
	stat := bundle.Must(nodefs.Lstat(directory))
	if isLink(stat) || !stat.IsDir() {
		bundle.Throw(errors.New("Proof bundle destination must be a real directory: " + directory))
	}
	for _, name := range bundle.Must(nodefs.ReadDirNames(directory)) {
		child := nodefs.Join(directory, name)
		childStat := bundle.Must(nodefs.Lstat(child))
		if isLink(childStat) || (!childStat.IsDir() && !childStat.Mode().IsRegular()) {
			bundle.Throw(errors.New("Proof bundle destination contains a symbolic link or special file: " + child))
		}
		if childStat.IsDir() {
			assertNoLinksOrSpecialFiles(child)
		}
	}
}

func collectFiles(directory, current string) []string {
	var files []string
	for _, name := range bundle.Must(nodefs.ReadDirNames(current)) {
		path := nodefs.Join(current, name)
		stat := bundle.Must(nodefs.Lstat(path))
		if isLink(stat) || (!stat.IsDir() && !stat.Mode().IsRegular()) {
			bundle.Throw(errors.New("bundle contains a symbolic link or special file: " + path))
		}
		if stat.IsDir() {
			files = append(files, collectFiles(directory, path)...)
		} else {
			files = append(files, strings.ReplaceAll(nodefs.Relative(directory, path), `\`, "/"))
		}
	}
	return files
}

// safeRunFileName is run.id.replace(/[^a-zA-Z0-9_-]/g, "_") per UTF-16 unit.
func safeRunFileName(id string) string {
	units := jsstr.ToUTF16(id)
	out := make([]byte, len(units))
	for i, u := range units {
		switch {
		case u == '_' || u == '-' || (u >= '0' && u <= '9') || (u >= 'a' && u <= 'z') || (u >= 'A' && u <= 'Z'):
			out[i] = byte(u)
		default:
			out[i] = '_'
		}
	}
	return string(out)
}

func requiredDeclaredFiles(analysis jsjson.Value) []string {
	files := []string{"manifest.json", "report.md", "analysis.json", "witness/witness.json", "minimization/attempts.json", "prevention/three-state.json", "VERIFY.md"}
	for _, run := range analysis.Get("runCatalog").Items() {
		base := "runs/" + safeRunFileName(run.Get("id").Str())
		files = append(files, base+"/result.json", base+"/stdout.log", base+"/stderr.log")
	}
	return canonical.SortLocale(files)
}

// orderedSet is a JS Set of strings.
type orderedSet struct {
	order []string
	has   map[string]bool
}

func newSet(values ...string) *orderedSet {
	s := &orderedSet{has: map[string]bool{}}
	for _, v := range values {
		s.add(v)
	}
	return s
}

func (s *orderedSet) add(v string) {
	if !s.has[v] {
		s.has[v] = true
		s.order = append(s.order, v)
	}
}

func referencedRunIDs(a jsjson.Value) *orderedSet {
	ids := newSet()
	var runs []jsjson.Value
	runs = append(runs, a.Get("contributionRuns").Items()...)
	runs = append(runs, a.Get("timelineRuns").Items()...)
	runs = append(runs, a.Get("prevention", "lastGood"), a.Get("prevention", "firstBad"), a.Get("prevention", "repaired"), a.Get("minimization", "sufficiency"), a.Get("minimization", "necessity"))
	for _, run := range runs {
		ids.add(run.Get("id").Str())
	}
	for _, t := range a.Get("transitions").Items() {
		for _, id := range bundle.Strings(t.Get("boundaryRunIds")) {
			ids.add(id)
		}
	}
	for _, attempt := range a.Get("minimization", "attempts").Items() {
		if id := attempt.Get("runId"); id.Kind() == jsjson.String && id.Str() != "" {
			ids.add(id.Str())
		}
	}
	return ids
}

func validateAnalysisCoverage(a jsjson.Value, errs *[]string) {
	catalog := map[string]bool{}
	for _, record := range a.Get("runCatalog").Items() {
		id := record.Get("id").Str()
		if catalog[id] {
			*errs = append(*errs, "duplicate run id in catalog: "+id)
		}
		catalog[id] = true
	}
	for _, id := range referencedRunIDs(a).order {
		if !catalog[id] {
			*errs = append(*errs, "analysis references a run not present in the catalog: "+id)
		}
	}
}

func readJSONArtifact(path, label string, errs *[]string) (jsjson.Value, bool) {
	v, err := bundle.ParseJSONFile(path)
	if err != nil {
		*errs = append(*errs, label+" JSON validation failed: "+err.Error())
		return jsjson.Value{}, false
	}
	return v, true
}

func stableExecutedRuns(records []jsjson.Value, expectedStateID, expectedVerdict string) bool {
	ids := make([]string, len(records))
	for i, r := range records {
		ids[i] = r.Get("id").Str()
	}
	if len(records) != 3 || bundle.DistinctCount(ids) != len(records) {
		return false
	}
	first := records[0]
	for _, r := range records {
		if r.Get("executionKind").Str() != "EXECUTED" || r.Get("verdict").Str() != expectedVerdict || r.Get("stateId").Str() != expectedStateID ||
			r.Get("witnessDigest").Str() != first.Get("witnessDigest").Str() || r.Get("environmentDigest").Str() != first.Get("environmentDigest").Str() {
			return false
		}
	}
	return true
}

func validateSemanticEvidence(a jsjson.Value, output string, errs *[]string) {
	add := func(s string) { *errs = append(*errs, s) }

	if payload, ok := readJSONArtifact(nodefs.Join(output, "witness", "witness.json"), "witness artifact", errs); ok {
		if err := bundle.Try(func() {
			witness := bundle.Must(bundle.ParseValue(payload, witnessSchema))
			if !bundle.SameCanonical(witness, a.Get("witness")) {
				add("witness artifact does not match analysis.witness")
			}
			if bundle.Must(canonical.DigestJSON(bundle.Without(witness, "digest"))) != witness.Get("digest").Str() {
				add("witness artifact digest is invalid")
			}
		}); err != nil {
			add("witness artifact schema validation failed: " + err.Error())
		}
	}

	if payload, ok := readJSONArtifact(nodefs.Join(output, "minimization", "attempts.json"), "minimization artifact", errs); ok {
		if err := bundle.Try(func() {
			if !bundle.SameCanonical(bundle.Must(bundle.ParseValue(payload, minimizationSchema)), a.Get("minimization")) {
				add("minimization artifact does not match analysis.minimization")
			}
		}); err != nil {
			add("minimization artifact schema validation failed: " + err.Error())
		}
	}

	if payload, ok := readJSONArtifact(nodefs.Join(output, "prevention", "three-state.json"), "prevention artifact", errs); ok {
		if err := bundle.Try(func() {
			if !bundle.SameCanonical(bundle.Must(bundle.ParseValue(payload, preventionSchema)), a.Get("prevention")) {
				add("prevention artifact does not match analysis.prevention")
			}
		}); err != nil {
			add("prevention artifact schema validation failed: " + err.Error())
		}
	}

	catalog := map[string]jsjson.Value{}
	for _, run := range a.Get("runCatalog").Items() {
		catalog[run.Get("id").Str()] = run
	}
	for _, run := range a.Get("runCatalog").Items() {
		id := run.Get("id").Str()
		base := nodefs.Join(output, "runs", safeRunFileName(id))
		if persisted, ok := readJSONArtifact(nodefs.Join(base, "result.json"), "run "+id, errs); ok {
			if err := bundle.Try(func() {
				if !bundle.SameCanonical(bundle.Must(bundle.ParseValue(persisted, runRecordSchema)), run) {
					add("persisted run does not match catalog: " + id)
				}
			}); err != nil {
				add("persisted run schema validation failed for " + id + ": " + err.Error())
			}
		}
		if err := bundle.Try(func() {
			if bundle.Must(nodefs.ReadText(nodefs.Join(base, "stdout.log"))) != run.Get("stdout").Str() {
				add("stdout does not match catalog for " + id)
			}
			if bundle.Must(nodefs.ReadText(nodefs.Join(base, "stderr.log"))) != run.Get("stderr").Str() {
				add("stderr does not match catalog for " + id)
			}
		}); err != nil {
			add("run log read failed for " + id + ": " + err.Error())
		}
	}

	mode := a.Get("mode").Str()
	for _, t := range a.Get("transitions").Items() {
		ids := bundle.Strings(t.Get("boundaryRunIds"))
		var boundary []jsjson.Value
		for _, id := range ids {
			if run, ok := catalog[id]; ok {
				boundary = append(boundary, run)
			}
		}
		if len(boundary) != len(ids) {
			continue
		}
		before, after := t.Get("beforeStateId").Str(), t.Get("afterStateId").Str()
		if bundle.DistinctCount(ids) != len(ids) {
			add("transition contains duplicate boundary run IDs: " + before + " -> " + after)
		}
		var beforeRuns, afterRuns []jsjson.Value
		for _, run := range boundary {
			if run.Get("stateId").Str() == before {
				beforeRuns = append(beforeRuns, run)
			}
			if run.Get("stateId").Str() == after {
				afterRuns = append(afterRuns, run)
			}
		}
		beforeVerdict, afterVerdict := t.Get("beforeVerdict").Str(), t.Get("afterVerdict").Str()
		shouldBeStable := stableExecutedRuns(beforeRuns, before, beforeVerdict) && stableExecutedRuns(afterRuns, after, afterVerdict)
		if t.Get("stable").Bool() != shouldBeStable {
			add("transition stability does not match executed boundary evidence: " + before + " -> " + after)
		}
		expectedKind := ""
		if beforeVerdict == "PASS" && afterVerdict == "FAIL" {
			expectedKind = "PASS_TO_FAIL"
		} else if beforeVerdict == "FAIL" && afterVerdict == "PASS" {
			expectedKind = "FAIL_TO_PASS"
		}
		if expectedKind == "" || t.Get("kind").Str() != expectedKind {
			add("transition kind does not match verdicts: " + before + " -> " + after)
		}
		if mode == "REPLAY" && t.Get("stable").Bool() {
			add("cached replay cannot certify a stable transition")
		}
	}

	m := a.Get("minimization")
	sufficiency, hasSufficiency := catalog[m.Get("sufficiency", "id").Str()]
	necessity, hasNecessity := catalog[m.Get("necessity", "id").Str()]
	minimizationProof := m.Get("termination").Str() == "BIDIRECTIONALLY_VALIDATED"
	if minimizationProof {
		if !hasSufficiency || !hasNecessity || mode != "RERUN" ||
			sufficiency.Get("executionKind").Str() != "EXECUTED" || necessity.Get("executionKind").Str() != "EXECUTED" ||
			sufficiency.Get("verdict").Str() != "FAIL" || necessity.Get("verdict").Str() != "PASS" ||
			sufficiency.Get("witnessDigest").Str() != necessity.Get("witnessDigest").Str() ||
			sufficiency.Get("environmentDigest").Str() != necessity.Get("environmentDigest").Str() ||
			len(m.Get("candidate").Items()) == 0 {
			add("bidirectional minimization claim is not supported by executed evidence")
		}
	} else if mode == "REPLAY" && m.Get("termination").Str() != "NOT_EXECUTED" {
		add("cached replay minimization must be marked NOT_EXECUTED")
	}

	p := a.Get("prevention")
	preventionRuns := []jsjson.Value{p.Get("lastGood"), p.Get("firstBad"), p.Get("repaired")}
	if p.Get("verified").Bool() {
		var witnesses, environments []string
		executed := true
		for _, run := range preventionRuns {
			executed = executed && run.Get("executionKind").Str() == "EXECUTED"
			witnesses = append(witnesses, run.Get("witnessDigest").Str())
			environments = append(environments, run.Get("environmentDigest").Str())
		}
		if mode != "RERUN" || !executed ||
			preventionRuns[0].Get("verdict").Str() != "PASS" || preventionRuns[1].Get("verdict").Str() != "FAIL" || preventionRuns[2].Get("verdict").Str() != "PASS" ||
			bundle.DistinctCount(witnesses) != 1 || bundle.DistinctCount(environments) != 1 {
			add("three-state prevention claim is not supported by executed evidence")
		}
	}
	stablePassToFail := slices.ContainsFunc(a.Get("transitions").Items(), func(t jsjson.Value) bool {
		return t.Get("stable").Bool() && t.Get("kind").Str() == "PASS_TO_FAIL"
	})
	if a.Get("grade", "value").Str() == "A" && (!stablePassToFail || !minimizationProof || !p.Get("verified").Bool()) {
		add("A-grade claim is not supported by stable, minimized, and prevention evidence")
	}
}

// parseHashLine is /^(?<digest>[a-f0-9]{64})  (?<file>.+)$/ where JS `.`
// excludes \r, U+2028, and U+2029 (\n cannot occur after splitting).
func parseHashLine(line string) (digest, file string, ok bool) {
	if len(line) < 67 || line[64:66] != "  " {
		return "", "", false
	}
	for i := 0; i < 64; i++ {
		if c := line[i]; !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", "", false
		}
	}
	file = line[66:]
	if strings.ContainsAny(file, "\r\u2028\u2029") {
		return "", "", false
	}
	return line[:64], file, true
}

func invalidDeclaredPath(file string) bool {
	return strings.Contains(file, `\`) || strings.HasPrefix(file, "/") ||
		slices.ContainsFunc(strings.Split(file, "/"), func(part string) bool { return part == "" || part == "." || part == ".." })
}

// Verify ports verifyProofBundle (src/proof-bundle.ts:439).
func Verify(directory, expectedRoot string, rootProvided bool) (result bundle.Result) {
	output := nodefs.Resolve(directory)
	var errs []string
	var rootDigest *string
	expected := rootProvided && expectedRoot != "" // TS: `expectedRoot ? …`
	status := "NOT_PROVIDED"
	if expected {
		status = "MISMATCH"
	}
	defer bundle.Catch(func(err error) {
		errs = append(errs, "bundle verification failed safely: "+err.Error())
		result = bundle.Result{Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
	})

	if !nodefs.Exists(output) {
		return bundle.Result{Errors: []string{"bundle directory does not exist"}, ExternalRootStatus: status}
	}
	assertNoLinksOrSpecialFiles(output)
	hashesPath := nodefs.Join(output, "hashes.txt")
	rootPath := nodefs.Join(output, "ROOT.sha256")
	manifestPath := nodefs.Join(output, "manifest.json")
	analysisPath := nodefs.Join(output, "analysis.json")
	if !nodefs.Exists(hashesPath) || !nodefs.Exists(rootPath) || !nodefs.Exists(manifestPath) || !nodefs.Exists(analysisPath) {
		return bundle.Result{Errors: []string{"bundle is missing hashes.txt, ROOT.sha256, manifest.json, or analysis.json"}, ExternalRootStatus: status}
	}
	hashes := bundle.Must(nodefs.ReadText(hashesPath))
	calculated := "sha256:" + canonical.SHA256Hex(hashes)
	rootDigest = &calculated
	if jsstr.Trim(bundle.Must(nodefs.ReadText(rootPath))) != calculated {
		errs = append(errs, "ROOT.sha256 does not match hashes.txt")
	}
	status = "NOT_PROVIDED"
	if expected {
		status = "MISMATCH"
		if expectedRoot == calculated {
			status = "MATCH"
		}
	}
	if status == "MISMATCH" {
		errs = append(errs, "externally supplied bundle root does not match")
	}
	manifest, manifestErr := bundle.ParseFile(manifestPath, manifestSchema)
	if manifestErr != nil {
		errs = append(errs, "manifest validation failed: "+manifestErr.Error())
	}
	analysis, analysisErr := bundle.ParseFile(analysisPath, demoAnalysisSchema)
	if analysisErr != nil {
		errs = append(errs, "analysis validation failed: "+analysisErr.Error())
	}
	if manifestErr == nil && analysisErr == nil {
		if manifest.Get("analysisDigest").Str() != bundle.Must(canonical.DigestJSON(analysis)) {
			errs = append(errs, "manifest analysisDigest does not match analysis.json")
		}
		if manifest.Get("fixtureId").Str() != analysis.Get("fixture", "id").Str() {
			errs = append(errs, "manifest fixtureId does not match analysis.json")
		}
		if manifest.Get("witnessDigest").Str() != analysis.Get("witness", "digest").Str() {
			errs = append(errs, "manifest witnessDigest does not match analysis.json")
		}
		validateAnalysisCoverage(analysis, &errs)
	}
	declared := map[string]string{}
	var declaredOrder []string
	for _, line := range strings.Split(jsstr.Trim(hashes), "\n") {
		if line == "" {
			continue
		}
		digest, file, ok := parseHashLine(line)
		if !ok {
			errs = append(errs, "invalid hash entry: "+line)
			continue
		}
		if _, dup := declared[file]; dup {
			errs = append(errs, "duplicate declared file: "+file)
			continue
		}
		if invalidDeclaredPath(file) {
			errs = append(errs, "invalid declared path: "+file)
			continue
		}
		declared[file] = digest
		declaredOrder = append(declaredOrder, file)
	}
	if analysisErr == nil {
		required := newSet(requiredDeclaredFiles(analysis)...)
		for _, file := range required.order {
			if _, ok := declared[file]; !ok {
				errs = append(errs, "required evidence file is not declared: "+file)
			}
		}
		for _, file := range declaredOrder {
			if !required.has[file] {
				errs = append(errs, "undeclared-schema file is present in hashes.txt: "+file)
			}
		}
	}
	for _, file := range declaredOrder {
		path := nodefs.Resolve(output, file)
		local := nodefs.Relative(output, path)
		if strings.HasPrefix(local, "..") || nodefs.IsAbsolute(local) {
			errs = append(errs, "declared path escapes bundle: "+file)
			continue
		}
		if !nodefs.Exists(path) {
			errs = append(errs, "declared file is missing: "+file)
			continue
		}
		if canonical.SHA256HexBytes(bundle.Must(nodefs.ReadBytes(path))) != declared[file] {
			errs = append(errs, "digest mismatch: "+file)
		}
	}
	expectedPhysical := newSet(append(slices.Clone(declaredOrder), "hashes.txt", "ROOT.sha256")...)
	for _, file := range collectFiles(output, output) {
		if !expectedPhysical.has[file] {
			errs = append(errs, "undeclared file exists in bundle: "+file)
		}
	}
	if analysisErr == nil {
		validateSemanticEvidence(analysis, output, &errs)
	}
	return bundle.Result{Valid: len(errs) == 0, CheckedFiles: len(declaredOrder), Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
}
