// Package prevention ports src/prevention-proof.ts verification.
package prevention

import (
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
)

var baseArtifacts = []string{"README.md", "prevention.json", "manifest.json"}

func isLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }

func assertNoLinksOrSpecialFiles(directory string) {
	stat := bundle.Must(nodefs.Lstat(directory))
	if isLink(stat) || !stat.IsDir() {
		bundle.Throw(errors.New(jsexc.Concat("Prevention proof directory must be a real directory: ", directory)))
	}
	for _, name := range bundle.Must(nodefs.ReadDirNames(directory)) {
		child := nodefs.Join(directory, name)
		childStat := bundle.Must(nodefs.Lstat(child))
		if isLink(childStat) || (!childStat.IsDir() && !childStat.Mode().IsRegular()) {
			bundle.Throw(errors.New(jsexc.Concat("Prevention proof directory contains a symbolic link or special file: ", child)))
		}
		if childStat.IsDir() {
			assertNoLinksOrSpecialFiles(child)
		}
	}
}

// readJSON returns Undefined on failure, like the TS helper returning undefined.
func readJSON(path, label string, errs *[]string) jsjson.Value {
	v, err := bundle.ParseJSONFile(path)
	if err != nil {
		*errs = append(*errs, jsexc.Concat("Unable to read ", label, ": ", jsexc.Message(err)))
		return jsjson.Value{}
	}
	return v
}

func validateSemantics(b jsjson.Value) []string {
	var errs []string
	states := []jsjson.Value{b.Get("lastGood"), b.Get("firstBad"), b.Get("repaired")}
	field := func(name string) []string {
		out := make([]string, len(states))
		for i, st := range states {
			out[i] = st.Get(name).Str()
		}
		return out
	}
	if !b.Get("verified").Bool() {
		errs = append(errs, "prevention package must set verified=true")
	}
	if states[0].Get("verdict").Str() != "PASS" || states[1].Get("verdict").Str() != "FAIL" || states[2].Get("verdict").Str() != "PASS" {
		errs = append(errs, "three-state prevention requires PASS → FAIL → PASS")
	}
	if bundle.DistinctCount(field("witnessDigest")) != 1 {
		errs = append(errs, "all three states must share the same frozen witness digest")
	}
	if b.Get("frozenWitnessDigest").Str() != states[0].Get("witnessDigest").Str() {
		errs = append(errs, "package frozenWitnessDigest must match executed witness digests")
	}
	if bundle.DistinctCount(field("environmentDigest")) != 1 {
		errs = append(errs, "all three states must share the same environment digest")
	}
	if slices.ContainsFunc(states, func(st jsjson.Value) bool {
		return st.Get("executionTrust").Str() != "NATIVE_DOCKER" || st.Get("executionKind").Str() != "EXECUTED"
	}) {
		errs = append(errs, "prevention requires NATIVE_DOCKER EXECUTED evidence on every state")
	}
	if slices.ContainsFunc(states, func(st jsjson.Value) bool { return st.Get("distinctExecutionCount").Num() < 3 }) {
		errs = append(errs, "prevention requires at least three distinct executions per state")
	}
	if bundle.DistinctCount(field("commit")) != 3 {
		errs = append(errs, "last-good, first-bad, and repaired commits must be distinct")
	}
	for _, st := range states {
		ids := bundle.Strings(st.Get("runIds"))
		role := st.Get("role").Str()
		if len(ids) != 3 || bundle.DistinctCount(ids) != 3 {
			errs = append(errs, jsexc.Concat(role, " requires three distinct runIds"))
		}
		if st.Get("distinctExecutionCount").Num() != float64(len(ids)) {
			errs = append(errs, jsexc.Concat(role, " distinctExecutionCount must equal runIds length"))
		}
	}
	base, firstBadTree := b.Get("repairBaseTree"), b.Get("firstBad", "tree")
	if base.Kind() != jsjson.Undefined && firstBadTree.Kind() != jsjson.Undefined && base.Str() != firstBadTree.Str() {
		errs = append(errs, "repairBaseTree must match first-bad tree when both are present")
	}
	return errs
}

func validateRepairedRuns(body, artifact jsjson.Value) []string {
	var errs []string
	var ids []string
	for _, run := range artifact.Get("runs").Items() {
		ids = append(ids, run.Get("runId").Str())
	}
	if strings.Join(ids, "\x00") != strings.Join(bundle.Strings(body.Get("repaired", "runIds")), "\x00") {
		errs = append(errs, "repaired-runs.json runIds must match prevention.repaired.runIds in order")
	}
	repaired := body.Get("repaired")
	for _, run := range artifact.Get("runs").Items() {
		runID := run.Get("runId").Str()
		tree := repaired.Get("tree")
		if run.Get("commit").Str() != repaired.Get("commit").Str() || tree.Kind() != jsjson.String || run.Get("tree").Str() != tree.Str() {
			errs = append(errs, jsexc.Concat("repaired run ", runID, " commit/tree does not match repaired state"))
		}
		if run.Get("witnessDigest").Str() != body.Get("frozenWitnessDigest").Str() {
			errs = append(errs, jsexc.Concat("repaired run ", runID, " witness digest mismatch"))
		}
	}
	return errs
}

// Verify ports verifyPreventionProof (src/prevention-proof.ts:296).
func Verify(directory, expectedRoot string, rootProvided bool) bundle.Result {
	result, _, _ := verifyWithArtifacts(directory, expectedRoot, rootProvided)
	return result
}

// VerifyWithArtifacts is Verify plus the manifest and body parsed by that same
// single verification pass. A value is Undefined unless it was actually parsed
// (schema-valid), even when the verdict is invalid or the pass failed safely.
func VerifyWithArtifacts(directory, expectedRoot string, rootProvided bool) (bundle.Result, jsjson.Value, jsjson.Value) {
	return verifyWithArtifacts(directory, expectedRoot, rootProvided)
}

// VerifyWithArtifactsForAttachment is VerifyWithArtifacts for a `serve
// --bundle` attachment: the same single pass, with the manifest and body
// schema messages read at their own fixed serve sites.
func VerifyWithArtifactsForAttachment(directory, expectedRoot string, rootProvided bool) (bundle.Result, jsjson.Value, jsjson.Value) {
	return verifyWithSites(directory, expectedRoot, rootProvided, &attachmentSites{
		manifest:     jsjson.SiteServePreventionManifest,
		body:         jsjson.SiteServePreventionBody,
		repairedRuns: jsjson.SiteServePreventionRepairedRuns,
	})
}

// attachmentSites are the fixed call sites of the manifest, body and
// repaired-runs schema messages; nil is ordinary verification (SitePrevention).
type attachmentSites struct{ manifest, body, repairedRuns jsjson.Site }

func schemaMessage(issues []schema.Issue, site *jsjson.Site) string {
	if site == nil {
		return schema.ErrorMessage(issues)
	}
	return jsjson.AtCallSite(*site, func() string { return schema.ErrorMessage(issues) })
}

func verifyWithArtifacts(directory, expectedRoot string, rootProvided bool) (bundle.Result, jsjson.Value, jsjson.Value) {
	return verifyWithSites(directory, expectedRoot, rootProvided, nil)
}

func verifyWithSites(directory, expectedRoot string, rootProvided bool, sites *attachmentSites) (result bundle.Result, manifest, prevention jsjson.Value) {
	defer jsjson.UseCallSite(jsjson.SitePrevention)()
	var manifestSite, bodySite, repairedRunsSite *jsjson.Site
	if sites != nil {
		manifestSite, bodySite, repairedRunsSite = &sites.manifest, &sites.body, &sites.repairedRuns
	}
	var errs []string
	haveManifest, havePrevention := false, false
	var rootDigest *string
	status := "NOT_PROVIDED"
	if rootProvided {
		status = "MISMATCH"
	}
	finish := func() bundle.Result {
		r := bundle.Result{Valid: len(errs) == 0, Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
		if haveManifest {
			c := manifest.Get("classification").Str()
			r.Classification = &c
		}
		return r
	}
	defer bundle.Catch(func(err error) {
		errs = append(errs, jsexc.Concat("Prevention proof verification failed safely: ", jsexc.Message(err)))
		result = finish()
	})

	root := nodefs.Resolve(directory)
	if !nodefs.Exists(root) {
		return bundle.Result{Errors: []string{"Prevention proof directory does not exist"}, ExternalRootStatus: status}, manifest, prevention
	}
	assertNoLinksOrSpecialFiles(root)
	// new Set(readdirSync(...).map(name)): decoded names can collide, and a
	// Set keeps the first occurrence only.
	var physical []string
	seen := map[string]bool{}
	for _, name := range bundle.Must(nodefs.ReadDirNames(root)) {
		if !seen[name] {
			jsexc.SetAdd(seen, name)
			physical = append(physical, name)
		}
	}
	for _, expected := range baseArtifacts {
		if !slices.Contains(physical, expected) {
			errs = append(errs, jsexc.Concat("Prevention proof package is missing ", expected))
		}
	}
	for _, actual := range physical {
		if actual != "repaired-runs.json" && !slices.Contains(baseArtifacts, actual) {
			errs = append(errs, jsexc.Concat("Prevention proof package contains an unexpected artifact: ", actual))
		}
		stat := bundle.Must(nodefs.Lstat(nodefs.Join(root, actual)))
		if !stat.Mode().IsRegular() || isLink(stat) {
			errs = append(errs, jsexc.Concat("Prevention proof artifact must be a regular non-symlink file: ", actual))
		}
	}

	if out, issues, ok := schema.Parse(ManifestSchema, readJSON(nodefs.Join(root, "manifest.json"), "prevention proof manifest", &errs)); !ok {
		errs = append(errs, jsexc.Concat("Prevention proof manifest schema validation failed: ", schemaMessage(issues, manifestSite)))
	} else {
		manifest, haveManifest = out, true
		rd := manifest.Get("rootDigest").Str()
		rootDigest = &rd
		if rd != bundle.Must(canonical.DigestJSON(bundle.Without(manifest, "rootDigest"))) {
			errs = append(errs, "Prevention proof root digest does not match its canonical contents.")
		}
		if rootProvided {
			if rd == expectedRoot {
				status = "MATCH"
			} else {
				status = "MISMATCH"
				errs = append(errs, "Prevention proof root digest does not match the externally supplied digest.")
			}
		}
	}

	if out, issues, ok := schema.Parse(BodySchema, readJSON(nodefs.Join(root, "prevention.json"), "prevention body", &errs)); !ok {
		errs = append(errs, jsexc.Concat("Prevention proof body schema validation failed: ", schemaMessage(issues, bodySite)))
	} else {
		prevention, havePrevention = out, true
		errs = append(errs, validateSemantics(prevention)...)
		if haveManifest && manifest.Get("prevention", "digest").Str() != bundle.Must(canonical.DigestJSON(prevention)) {
			errs = append(errs, "Prevention proof manifest digest does not match prevention.json.")
		}
		if haveManifest && manifest.Get("classification").Str() == "PREVENTION_VERIFIED" {
			if prevention.Get("repairPatchDigest").Str() == "" {
				errs = append(errs, "PREVENTION_VERIFIED requires repairPatchDigest")
			}
			if prevention.Get("repairBaseTree").Str() == "" {
				errs = append(errs, "PREVENTION_VERIFIED requires repairBaseTree")
			}
			if !slices.Contains(physical, "repaired-runs.json") {
				errs = append(errs, "PREVENTION_VERIFIED package must include repaired-runs.json")
			}
		}
	}

	if slices.Contains(physical, "repaired-runs.json") && havePrevention {
		if out, issues, ok := schema.Parse(RepairedRunsSchema, readJSON(nodefs.Join(root, "repaired-runs.json"), "repaired runs artifact", &errs)); !ok {
			errs = append(errs, jsexc.Concat("repaired-runs.json schema validation failed: ", schemaMessage(issues, repairedRunsSite)))
		} else {
			errs = append(errs, validateRepairedRuns(prevention, out)...)
		}
	}
	return finish(), manifest, prevention
}
